package sync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
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

	// Output receives transport progress from git or rsync.
	Output io.Writer

	// Progress reports high-level stages suitable for a user interface.
	Progress func(stage, detail string)

	// Changes receives the ebuild-level difference after a successful update.
	Changes func(ChangeSummary)
}

type ChangeSummary struct {
	Added    []string
	Removed  []string
	Modified []string
}

// RemoteURL returns the first URL configured for the origin remote in an
// existing Git repository. It returns an empty string when no usable origin
// exists.
func RemoteURL(dir string) string {
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return ""
	}
	remote, err := repo.Remote("origin")
	if err != nil {
		return ""
	}
	urls := remote.Config().URLs
	if len(urls) == 0 {
		return ""
	}
	return urls[0]
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
	if c.Output == nil {
		c.Output = os.Stdout
	}
}

func (c SyncConfig) progress(stage, detail string) {
	if c.Progress != nil {
		c.Progress(stage, detail)
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
		if GitAvailable() {
			return updateGitRepoCommand(ctx, cfg)
		}
		return updateGitRepo(ctx, cfg)
	}

	if GitAvailable() {
		if err := cloneGitRepoCommand(ctx, cfg); err == nil {
			return nil
		}
	}
	if err := cloneGitRepo(ctx, cfg); err == nil {
		return nil
	}

	return syncRsync(ctx, cfg)
}

func runGit(ctx context.Context, output io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
	}
	return nil
}

func gitOutput(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func updateGitRepoCommand(ctx context.Context, cfg SyncConfig) error {
	cfg.progress("check", "Checking repository state")
	oldRevision, err := gitOutput(ctx, "-C", cfg.TargetDir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	dirty, err := gitOutput(ctx, "-C", cfg.TargetDir, "status", "--porcelain")
	if err != nil {
		return err
	}
	if dirty != "" {
		return errors.New("repository has local changes; refusing to overwrite them")
	}

	branch, err := gitOutput(ctx, "-C", cfg.TargetDir, "branch", "--show-current")
	if err != nil {
		return err
	}
	if branch == "" {
		return errors.New("repository has a detached HEAD; select a branch before syncing")
	}

	cfg.progress("fetch", "Fetching "+branch+" from origin")
	if err := runGit(ctx, cfg.Output, "-C", cfg.TargetDir, "fetch", "--progress", "--depth", strconv.Itoa(cfg.Depth), "origin", branch); err != nil {
		return err
	}

	cfg.progress("update", "Updating working tree")
	if err := runGit(ctx, cfg.Output, "-C", cfg.TargetDir, "reset", "--hard", "origin/"+branch); err != nil {
		return err
	}
	newRevision, err := gitOutput(ctx, "-C", cfg.TargetDir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if cfg.Changes != nil {
		cfg.progress("changes", "Calculating package changes")
		changes := ChangeSummary{}
		if oldRevision != newRevision {
			changes, err = gitEbuildChanges(ctx, cfg.TargetDir, oldRevision, newRevision)
			if err != nil {
				return err
			}
		}
		cfg.Changes(changes)
	}
	return nil
}

func gitEbuildChanges(ctx context.Context, dir, oldRevision, newRevision string) (ChangeSummary, error) {
	out, err := gitOutput(ctx, "-C", dir, "diff", "--name-status", oldRevision, newRevision, "--", "*.ebuild")
	if err != nil {
		return ChangeSummary{}, err
	}
	var summary ChangeSummary
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		path := fields[len(fields)-1]
		parts := strings.Split(path, "/")
		if len(parts) < 3 || !strings.HasSuffix(path, ".ebuild") {
			continue
		}
		cpv := parts[0] + "/" + strings.TrimSuffix(parts[len(parts)-1], ".ebuild")
		switch fields[0][0] {
		case 'A':
			summary.Added = append(summary.Added, cpv)
		case 'D':
			summary.Removed = append(summary.Removed, cpv)
		default:
			summary.Modified = append(summary.Modified, cpv)
		}
	}
	return summary, nil
}

func cloneGitRepoCommand(ctx context.Context, cfg SyncConfig) error {
	cfg.progress("clone", "Cloning repository with git")
	return runGit(ctx, cfg.Output, "clone", "--progress", "--depth", strconv.Itoa(cfg.Depth), cfg.RepoURL, cfg.TargetDir)
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
	cfg.progress("clone", "Cloning repository with built-in transport")
	_, err := git.PlainCloneContext(ctx, cfg.TargetDir, false, &git.CloneOptions{
		URL:   cfg.RepoURL,
		Depth: cfg.Depth,
	})
	if err != nil {
		return fmt.Errorf("could not clone the repository: %w", err)
	}
	return nil
}

func updateGitRepo(ctx context.Context, cfg SyncConfig) error {
	repo, err := git.PlainOpen(cfg.TargetDir)
	if err != nil {
		return fmt.Errorf("could not open the local repository: %w", err)
	}

	rem, err := repo.Remote("origin")
	if err != nil {
		return fmt.Errorf("could not contact the remote repository: %w", err)
	}

	err = rem.FetchContext(ctx, &git.FetchOptions{
		Depth: cfg.Depth,
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) && !strings.Contains(err.Error(), "already up-to-date") {
		return fmt.Errorf("could not fetch updates from the remote: %w", err)
	}

	w, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("could not check repository working tree status: %w", err)
	}

	err = w.PullContext(ctx, &git.PullOptions{
		RemoteName: "origin",
		Depth:      cfg.Depth,
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) && !strings.Contains(err.Error(), "already up-to-date") {
		return fmt.Errorf("could not pull updates into the local repository: %w", err)
	}

	return nil
}

func syncRsync(ctx context.Context, cfg SyncConfig) error {
	cmd := exec.CommandContext(ctx, cfg.RsyncPath, "-av", "--delete", cfg.RepoURL+"/", cfg.TargetDir)
	cfg.progress("rsync", "Synchronizing repository")
	cmd.Stdout = cfg.Output
	cmd.Stderr = cfg.Output
	return cmd.Run()
}
