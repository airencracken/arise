package main

import (
	"context"
	"fmt"
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
	fmt.Printf("%s %d repositories\n", color.Bold("Syncing"), len(targets))
	for _, target := range targets {
		if target.URL == "" {
			fmt.Printf("\n%s\n", color.Bold(target.Name))
			fmt.Printf("  Location: %s\n", target.Location)
			fmt.Println("  Status:   local-only; no sync URI or Git origin")
			continue
		}
		targetStarted := time.Now()
		fmt.Printf("\n%s\n", color.Bold(target.Name))
		fmt.Printf("  URI:      %s\n", target.URL)
		fmt.Printf("  Location: %s\n", target.Location)
		cfg := sync.SyncConfig{
			RepoURL:   target.URL,
			TargetDir: target.Location,
			SyncType:  target.SyncType,
			Output:    os.Stdout,
			Progress: func(stage, detail string) {
				icons := map[string]string{"check": "1/4", "fetch": "2/4", "update": "3/4", "changes": "4/4", "unchanged": "3/3", "clone": "1/1", "rsync": "1/1"}
				fmt.Printf("  %s %s\n", color.Cyan("["+icons[stage]+"]"), detail)
			},
			Changes: func(changes sync.ChangeSummary) {
				printSyncChanges(changes)
			},
		}
		if err := sync.Sync(context.Background(), cfg); err != nil {
			fmt.Fprintf(os.Stderr, "\n%s %s: %v\n", color.Red("Sync failed:"), target.Name, err)
			os.Exit(1)
		}
		fmt.Printf("  %s synchronized in %s\n", color.Green("Done."), time.Since(targetStarted).Round(time.Millisecond))
	}
	fmt.Printf("\n%s Repository checkouts synchronized in %s\n", color.Green("Fetched."), time.Since(started).Round(time.Millisecond))
	fmt.Printf("\n%s\n", color.Bold("Refreshing resolver index"))
	runIndex(dbPath, repoPath)
	fmt.Printf("\n%s Repository and resolver index synchronized in %s\n", color.Green("Done."), time.Since(started).Round(time.Millisecond))
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
