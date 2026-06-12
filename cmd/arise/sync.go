package main

import (
	"context"
	"fmt"
	"os"

	arise "github.com/airencracken/arise/internal/log"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/sync"
)

func runSync(dbPath, repoPath, repoURL string) {
	url := repoURL
	if url == "" {
		url = portage.ParseReposConf(*portageConfigRoot+"/repos.conf", repoPath)
	}
	if url == "" {
		arise.Error("no repository URL configured")
		fmt.Fprintf(os.Stderr, "sync: -repo-url is required\n")
		os.Exit(1)
	}
	cfg := sync.SyncConfig{
		RepoURL:   url,
		TargetDir: repoPath,
	}
	arise.Info("starting sync", "url", url, "target", repoPath)
	if err := sync.Sync(context.Background(), cfg); err != nil {
		arise.Error("sync failed", "error", err)
		fmt.Fprintf(os.Stderr, "sync: %v\n", err)
		os.Exit(1)
	}
	arise.Info("sync completed")
}
