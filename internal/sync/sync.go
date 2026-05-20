package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/go-git/go-git/v5"
)

// SyncConfig holds configuration for syncing a repository.
type SyncConfig struct {
	// RepoURL is the URL of the remote repository (git clone URL or rsync URL).
	RepoURL string

	// TargetDir is the local directory to sync into.
	TargetDir string

	// Depth controls shallow clone depth. If <= 0, defaults to 1.
	Depth int

	// RsyncPath is the path to the rsync binary (default: "rsync").
	RsyncPath string
}

// Validate checks that required fields are present.
func (c SyncConfig) Validate() error {
	if c.RepoURL == "" {
		return errors.New("sync: RepoURL is required")
	}
	if c.TargetDir == "" {
		return errors.New("sync: TargetDir is required")
	}
	return nil
}

func (c *SyncConfig) defaults() {
	if c.Depth <= 0 {
		c.Depth = 1
	}
	if c.RsyncPath == "" {
		c.RsyncPath = "rsync"
	}
}

// Sync clones a fresh repository or updates an existing one.
// If go-git is unavailable or fails, it falls back to rsync.
func Sync(ctx context.Context, cfg SyncConfig) error {
	cfg.defaults()

	if err := cfg.Validate(); err != nil {
		return err
	}

	if isGitRepo(cfg.TargetDir) {
		return updateGitRepo(ctx, cfg)
	}

	if GitAvailable() {
		if err := cloneGitRepo(ctx, cfg); err == nil {
			return nil
		}
	}

	return syncRsync(ctx, cfg)
}

// GitAvailable reports whether git is in PATH.
func GitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func isGitRepo(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	gitDir := dir + string(os.PathSeparator) + ".git"
	fi, err := os.Stat(gitDir)
	return err == nil && fi.IsDir()
}

func cloneGitRepo(ctx context.Context, cfg SyncConfig) error {
	_, err := git.PlainCloneContext(ctx, cfg.TargetDir, false, &git.CloneOptions{
		URL:   cfg.RepoURL,
		Depth: cfg.Depth,
	})
	if err != nil {
		return fmt.Errorf("sync: clone failed: %w", err)
	}
	return nil
}

func updateGitRepo(ctx context.Context, cfg SyncConfig) error {
	repo, err := git.PlainOpen(cfg.TargetDir)
	if err != nil {
		return fmt.Errorf("sync: open repo: %w", err)
	}

	rem, err := repo.Remote("origin")
	if err != nil {
		return fmt.Errorf("sync: get remote: %w", err)
	}

	err = rem.FetchContext(ctx, &git.FetchOptions{
		Depth: cfg.Depth,
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) && !strings.Contains(err.Error(), "already up-to-date") {
		return fmt.Errorf("sync: fetch: %w", err)
	}

	w, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("sync: worktree: %w", err)
	}

	err = w.PullContext(ctx, &git.PullOptions{
		RemoteName: "origin",
		Depth:      cfg.Depth,
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) && !strings.Contains(err.Error(), "already up-to-date") {
		return fmt.Errorf("sync: pull: %w", err)
	}

	return nil
}

func syncRsync(ctx context.Context, cfg SyncConfig) error {
	cmd := exec.CommandContext(ctx, cfg.RsyncPath, "-av", "--delete", cfg.RepoURL+"/", cfg.TargetDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
