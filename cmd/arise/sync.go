package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/sync"
)

func runSync(dbPath, repoPath, repoURL string) {
	if err := syncPrivilegeError(os.Geteuid(), repoPath); err != nil {
		fmt.Fprintf(os.Stderr, "sync: %v\n", err)
		os.Exit(1)
	}
	url := repoURL
	if url == "" {
		url = portage.ParseReposConf(*portageConfigRoot+"/repos.conf", repoPath)
	}
	if url == "" {
		url = sync.RemoteURL(repoPath)
	}
	if url == "" {
		fmt.Fprintf(os.Stderr, "sync: no sync-uri found in repos.conf and no origin remote found; use -repo-url\n")
		os.Exit(1)
	}
	cfg := sync.SyncConfig{
		RepoURL:   url,
		TargetDir: repoPath,
		Output:    os.Stdout,
		Progress: func(stage, detail string) {
			icons := map[string]string{"check": "1/4", "fetch": "2/4", "update": "3/4", "changes": "4/4", "clone": "1/1", "rsync": "1/1"}
			fmt.Printf("  %s %s\n", color.Cyan("["+icons[stage]+"]"), detail)
		},
		Changes: printSyncChanges,
	}
	started := time.Now()
	fmt.Printf("%s Gentoo repository\n", color.Bold("Syncing"))
	fmt.Printf("  URI:      %s\n", url)
	fmt.Printf("  Location: %s\n\n", repoPath)
	if err := sync.Sync(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "\n%s %v\n", color.Red("Sync failed:"), err)
		os.Exit(1)
	}
	fmt.Printf("\n%s Repository checkout synchronized in %s\n", color.Green("Fetched."), time.Since(started).Round(time.Millisecond))
	fmt.Printf("\n%s\n", color.Bold("Refreshing resolver index"))
	runIndex(dbPath, repoPath)
	fmt.Printf("\n%s Repository and resolver index synchronized in %s\n", color.Green("Done."), time.Since(started).Round(time.Millisecond))
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
