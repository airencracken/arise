package sync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/go-git/go-git/v5"
)

// SyncConfig holds configuration for syncing a repository.
type SyncConfig struct {
	// RepoURL is the URL of the remote repository (git clone URL or rsync URL).
	RepoURL string

	// SyncType selects the configured transport. Empty and "git" use the Git
	// path; "rsync" bypasses Git probing and invokes rsync directly.
	SyncType string

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
	Packages []PackageChange
}

type PackageChange struct {
	CP     string
	Kind   string
	Before []string
	After  []string
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
	if strings.TrimSpace(c.RepoURL) == "" {
		return errors.New("sync: RepoURL is required")
	}
	if strings.IndexByte(c.RepoURL, 0) >= 0 {
		return errors.New("sync: RepoURL contains NUL")
	}
	if strings.TrimSpace(c.TargetDir) == "" {
		return errors.New("sync: TargetDir is required")
	}
	if strings.IndexByte(c.TargetDir, 0) >= 0 {
		return errors.New("sync: TargetDir contains NUL")
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
	if err := ctx.Err(); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(cfg.SyncType)) {
	case "rsync":
		return syncRsync(ctx, cfg)
	case "", "git":
	default:
		return fmt.Errorf("sync: unsupported sync type %q", cfg.SyncType)
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
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if err := cloneGitRepo(ctx, cfg); err == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	return syncRsync(ctx, cfg)
}

func runGit(ctx context.Context, output io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
	}
	return nil
}

func gitOutput(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
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
	remoteRevision, err := gitOutput(ctx, "-C", cfg.TargetDir, "rev-parse", "FETCH_HEAD")
	if err != nil {
		return err
	}
	if oldRevision == remoteRevision {
		cfg.progress("unchanged", "Already up to date")
		return nil
	}

	cfg.progress("update", "Updating working tree")
	if err := runGit(ctx, cfg.Output, "-C", cfg.TargetDir, "reset", "--hard", remoteRevision); err != nil {
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
		status := fields[0][0]
		if status == 'R' && len(fields) >= 3 {
			if cpv, ok := ebuildCPV(fields[1]); ok {
				summary.Removed = append(summary.Removed, cpv)
			}
			if cpv, ok := ebuildCPV(fields[2]); ok {
				summary.Added = append(summary.Added, cpv)
			}
			continue
		}
		if status == 'C' && len(fields) >= 3 {
			if cpv, ok := ebuildCPV(fields[2]); ok {
				summary.Added = append(summary.Added, cpv)
			}
			continue
		}
		cpv, ok := ebuildCPV(fields[len(fields)-1])
		if !ok {
			continue
		}
		switch status {
		case 'A':
			summary.Added = append(summary.Added, cpv)
		case 'D':
			summary.Removed = append(summary.Removed, cpv)
		default:
			summary.Modified = append(summary.Modified, cpv)
		}
	}
	affected := make(map[string]struct{})
	for _, cpv := range append(append(append([]string{}, summary.Added...), summary.Removed...), summary.Modified...) {
		if cp, ok := cpFromCPV(cpv); ok {
			affected[cp] = struct{}{}
		}
	}
	for cp := range affected {
		before, versionErr := gitPackageVersions(ctx, dir, oldRevision, cp)
		if versionErr != nil {
			return ChangeSummary{}, versionErr
		}
		after, versionErr := gitPackageVersions(ctx, dir, newRevision, cp)
		if versionErr != nil {
			return ChangeSummary{}, versionErr
		}
		summary.Packages = append(summary.Packages, PackageChange{
			CP: cp, Kind: packageChangeKind(before, after), Before: before, After: after,
		})
	}
	sort.Slice(summary.Packages, func(i, j int) bool { return summary.Packages[i].CP < summary.Packages[j].CP })
	return summary, nil
}

func cpFromCPV(cpv string) (string, bool) {
	category, pf, ok := strings.Cut(cpv, "/")
	if !ok {
		return "", false
	}
	for index := len(pf) - 1; index >= 0; index-- {
		if pf[index] == '-' && index+1 < len(pf) && pf[index+1] >= '0' && pf[index+1] <= '9' {
			return category + "/" + pf[:index], true
		}
	}
	return "", false
}

func gitPackageVersions(ctx context.Context, dir, revision, cp string) ([]string, error) {
	category, packageName, ok := strings.Cut(cp, "/")
	if !ok {
		return nil, fmt.Errorf("sync: invalid package key %q", cp)
	}
	out, err := gitOutput(ctx, "-C", dir, "ls-tree", "-r", "--name-only", revision, "--", cp)
	if err != nil {
		return nil, err
	}
	var versions []string
	prefix := packageName + "-"
	for _, path := range strings.Split(out, "\n") {
		parts := strings.Split(path, "/")
		if len(parts) != 3 || parts[0] != category || parts[1] != packageName ||
			!strings.HasPrefix(parts[2], prefix) || !strings.HasSuffix(parts[2], ".ebuild") {
			continue
		}
		versions = append(versions, strings.TrimSuffix(strings.TrimPrefix(parts[2], prefix), ".ebuild"))
	}
	sort.Slice(versions, func(i, j int) bool {
		left, leftErr := atom.ParseVersion(versions[i])
		right, rightErr := atom.ParseVersion(versions[j])
		if leftErr != nil || rightErr != nil {
			return versions[i] < versions[j]
		}
		return left.Compare(right) < 0
	})
	return versions, nil
}

func packageChangeKind(before, after []string) string {
	switch {
	case len(before) == 0:
		return "new"
	case len(after) == 0:
		return "removed"
	case strings.Join(before, "\x00") == strings.Join(after, "\x00"):
		return "changed"
	case versionsContain(after, before):
		return "better"
	case versionsContain(before, after):
		return "worse"
	default:
		oldBest, oldErr := atom.ParseVersion(before[len(before)-1])
		newBest, newErr := atom.ParseVersion(after[len(after)-1])
		if oldErr == nil && newErr == nil {
			if comparison := newBest.Compare(oldBest); comparison > 0 {
				return "upgrade"
			} else if comparison < 0 {
				return "downgrade"
			}
		}
		return "changed"
	}
}

func versionsContain(superset, subset []string) bool {
	available := make(map[string]struct{}, len(superset))
	for _, version := range superset {
		available[version] = struct{}{}
	}
	for _, version := range subset {
		if _, ok := available[version]; !ok {
			return false
		}
	}
	return true
}

func ebuildCPV(path string) (string, bool) {
	parts := strings.Split(path, "/")
	if len(parts) < 3 || !strings.HasSuffix(path, ".ebuild") {
		return "", false
	}
	cpv := parts[0] + "/" + strings.TrimSuffix(parts[len(parts)-1], ".ebuild")
	return cpv, true
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
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	return nil
}
