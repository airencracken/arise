package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/sync"
)

func runSync(requested []string, dbPath, repoPath, repoURL string) {
	if err := syncPrivilegeError(os.Geteuid(), repoPath); err != nil {
		fmt.Fprintf(os.Stderr, "sync: %v\n", err)
		os.Exit(1)
	}
	repositories, err := portage.ReadReposConf(filepath.Join(*portageConfigRoot, "repos.conf"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "sync: read repositories: %v\n", err)
		os.Exit(1)
	}
	targets := configuredSyncTargets(repoPath, repoURL, repositories)
	targets, err = selectSyncTargets(targets, requested)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sync: %v\n", err)
		os.Exit(2)
	}
	if len(targets) == 0 {
		fmt.Fprintln(os.Stderr, "sync: no repositories are configured")
		os.Exit(1)
	}
	started := time.Now()
	fmt.Printf("%s %s\n", color.Bold("Syncing"), formatRepositoryCount(len(targets)))
	for _, target := range targets {
		if target.URL == "" {
			fmt.Printf("  %-20s %s\n", target.Name, color.Yellow("local only"))
			if *verbose {
				fmt.Printf("    Location: %s\n", target.Location)
			}
			continue
		}
		targetStarted := time.Now()
		var transport bytes.Buffer
		output := io.Writer(&transport)
		if *verbose {
			fmt.Printf("\n%s\n", color.Bold(target.Name))
			fmt.Printf("  URI:      %s\n", target.URL)
			fmt.Printf("  Location: %s\n", target.Location)
			output = os.Stdout
		}
		report := syncTargetReport{}
		cfg := sync.SyncConfig{
			RepoURL:        target.URL,
			TargetDir:      target.Location,
			RepositoryName: target.Name,
			SyncType:       target.SyncType,
			Output:         output,
			Progress: func(stage, detail string) {
				report.Stage = stage
				if *verbose {
					fmt.Printf("  %s %s\n", color.Cyan("["+stage+"]"), detail)
				}
			},
			Changes: func(changes sync.ChangeSummary) {
				report.Changes = changes
				report.HasChanges = true
				if *verbose {
					printSyncChanges(changes)
				}
			},
		}
		if err := sync.Sync(commandContext, cfg); err != nil {
			if errors.Is(err, context.Canceled) {
				fmt.Fprintln(os.Stderr, "sync: interrupted by user")
				os.Exit(130)
			}
			if transport.Len() > 0 {
				fmt.Fprint(os.Stderr, transport.String())
			}
			fmt.Fprintf(os.Stderr, "\n%s %s: %v\n", color.Red("Sync failed:"), target.Name, err)
			os.Exit(1)
		}
		if *verbose {
			fmt.Printf("  %s synchronized in %s\n", color.Green("Done."), time.Since(targetStarted).Round(time.Millisecond))
		} else {
			printSyncTargetReport(os.Stdout, target.Name, report, time.Since(targetStarted))
		}
	}
	if err := commandContext.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "sync: interrupted by user")
		os.Exit(130)
	}
	fmt.Printf("\n%s\n", color.Bold("Refreshing resolver index"))
	runIndex(dbPath, repoPath)
	fmt.Printf("%s in %s\n", color.Green("Synchronized"), time.Since(started).Round(time.Millisecond))
}

type syncTargetReport struct {
	Stage      string
	Changes    sync.ChangeSummary
	HasChanges bool
}

func formatRepositoryCount(count int) string {
	if count == 1 {
		return "1 repository"
	}
	return fmt.Sprintf("%d repositories", count)
}

func printSyncTargetReport(w io.Writer, name string, report syncTargetReport, elapsed time.Duration) {
	status := color.Green("updated")
	switch {
	case report.Stage == "unchanged":
		status = "unchanged"
	case report.Stage == "clone":
		status = color.Green("cloned")
	case !report.HasChanges:
		status = color.Green("synchronized")
	}
	fmt.Fprintf(w, "  %-20s %-12s %s\n", name, status, elapsed.Round(time.Millisecond))
	if report.HasChanges {
		printPackageChanges(w, report.Changes)
	}
}

func printPackageChanges(w io.Writer, changes sync.ChangeSummary) {
	if len(changes.Packages) == 0 {
		if count := len(changes.Added) + len(changes.Removed) + len(changes.Modified); count > 0 {
			fmt.Fprintf(w, "    %d ebuild file change(s)\n", count)
		}
		return
	}
	for _, change := range changes.Packages {
		tag := formatEixTag("C", color.BoldYellow)
		cp := formatEixPackageName(change.CP)
		relation := color.Yellow("==")
		versions := bestVersion(change.After)
		switch change.Kind {
		case "new":
			tag = formatEixTag("N", color.BoldGreen)
			relation = color.BoldGreen(">>")
		case "removed":
			tag = formatEixTag("D", color.BoldRed)
			relation = color.BoldRed("<<")
			versions = bestVersion(change.Before)
		case "upgrade":
			tag = formatEixTag("U", color.ReverseBoldCyan)
			versions = formatBestVersionTransition(change.Before, change.After)
		case "better":
			tag = formatEixTag(">", color.Yellow)
			versions = formatBestVersionTransition(change.Before, change.After)
		case "downgrade":
			tag = formatEixTag("?", color.ReverseBoldBlue)
			versions = formatBestVersionTransition(change.Before, change.After)
		case "worse":
			tag = formatEixTag("<", color.BoldRed)
			versions = formatBestVersionTransition(change.Before, change.After)
		case "changed":
			if strings.Join(change.Before, "\x00") != strings.Join(change.After, "\x00") {
				versions = formatBestVersionTransition(change.Before, change.After)
			}
		}
		description := ""
		if change.Description != "" {
			description = ": " + change.Description
		}
		fmt.Fprintf(w, "    %s   %s %s (%s)%s\n", tag, relation, cp, versions, description)
	}
}

func formatEixTag(marker string, style func(string) string) string {
	return "[" + style(marker) + "]"
}

func formatEixPackageName(cp string) string {
	parts := strings.SplitN(cp, "/", 2)
	if len(parts) != 2 {
		return color.Bold(cp)
	}
	return parts[0] + "/" + color.Bold(parts[1])
}

func formatBestVersionTransition(before, after []string) string {
	return bestVersion(before) + " -> " + bestVersion(after)
}

func bestVersion(versions []string) string {
	for index := len(versions) - 1; index >= 0; index-- {
		if versions[index] != "9999" {
			return versions[index]
		}
	}
	if len(versions) == 0 {
		return ""
	}
	return versions[len(versions)-1]
}

func selectSyncTargets(targets []repositorySyncTarget, requested []string) ([]repositorySyncTarget, error) {
	if len(requested) == 0 {
		return targets, nil
	}
	byName := make(map[string]repositorySyncTarget, len(targets))
	for _, target := range targets {
		byName[target.Name] = target
	}
	selected := make([]repositorySyncTarget, 0, len(requested))
	seen := make(map[string]bool, len(requested))
	for _, name := range requested {
		if seen[name] {
			continue
		}
		target, ok := byName[name]
		if !ok {
			available := make([]string, 0, len(byName))
			for candidate := range byName {
				available = append(available, candidate)
			}
			sort.Strings(available)
			return nil, fmt.Errorf("unknown repository %q (configured: %s)", name, strings.Join(available, ", "))
		}
		selected = append(selected, target)
		seen[name] = true
	}
	return selected, nil
}

type repositorySyncTarget struct {
	Name     string
	Location string
	URL      string
	SyncType string
	Primary  bool
}

func configuredSyncTargets(repoPath, repoURL string, repositories []portage.RepoEntry) []repositorySyncTarget {
	cleanPrimary := filepath.Clean(repoPath)
	targets := make([]repositorySyncTarget, 0, len(repositories)+1)
	seenLocations := make(map[string]bool)
	primary := repositorySyncTarget{
		Name:     filepath.Base(cleanPrimary),
		Location: cleanPrimary,
		URL:      strings.TrimSpace(repoURL),
		Primary:  true,
	}
	for _, repository := range repositories {
		if repository.Location == "" || filepath.Clean(repository.Location) != cleanPrimary {
			continue
		}
		primary.Name = repository.Name
		primary.SyncType = repository.SyncType
		if primary.URL == "" {
			primary.URL = repository.SyncURI
		}
		break
	}
	if primary.URL == "" {
		primary.URL = sync.RemoteURL(cleanPrimary)
	}
	targets = append(targets, primary)
	seenLocations[cleanPrimary] = true

	var additional []repositorySyncTarget
	for _, repository := range repositories {
		location := filepath.Clean(repository.Location)
		if repository.Location == "" || seenLocations[location] {
			continue
		}
		url := strings.TrimSpace(repository.SyncURI)
		if url == "" {
			url = sync.RemoteURL(location)
		}
		additional = append(additional, repositorySyncTarget{
			Name: repository.Name, Location: location, URL: url, SyncType: repository.SyncType,
		})
		seenLocations[location] = true
	}
	sort.Slice(additional, func(i, j int) bool {
		if additional[i].Name != additional[j].Name {
			return additional[i].Name < additional[j].Name
		}
		return additional[i].Location < additional[j].Location
	})
	return append(targets, additional...)
}

func printSyncChanges(changes sync.ChangeSummary) {
	fmt.Println()
	fmt.Printf("%s  %s added, %s removed, %s changed\n",
		color.Bold("Package changes:"),
		color.Green(fmt.Sprintf("%d", len(changes.Added))),
		color.Red(fmt.Sprintf("%d", len(changes.Removed))),
		color.Yellow(fmt.Sprintf("%d", len(changes.Modified))))
	for _, item := range changes.Added {
		fmt.Printf("  %s %s\n", color.Green("+"), item)
	}
	for _, item := range changes.Removed {
		fmt.Printf("  %s %s\n", color.Red("-"), item)
	}
	for _, item := range changes.Modified {
		fmt.Printf("  %s %s\n", color.Yellow("~"), item)
	}
}

func syncPrivilegeError(euid int, repoPath string) error {
	if euid != 0 && (strings.HasPrefix(repoPath, "/var/db/repos/") || repoPath == "/usr/portage") {
		return fmt.Errorf("updating the system repository at %s requires root; run su -c 'arise sync'", repoPath)
	}
	return nil
}
