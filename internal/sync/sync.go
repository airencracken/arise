package sync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
)

var (
	errDirtyWorktree = errors.New("repository has local changes; refusing to overwrite them")
	errDetachedHead  = errors.New("repository has a detached HEAD; select a branch before syncing")
	errCloneTarget   = errors.New("clone target already exists")
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

// Sync clones a fresh repository or updates an existing one. The in-process
// Git transport is primary; the system Git executable is a compatibility
// fallback. Rsync is used only when explicitly configured.
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
		primaryErr := updateGitRepo(ctx, cfg)
		if primaryErr == nil || errors.Is(primaryErr, errDirtyWorktree) ||
			errors.Is(primaryErr, errDetachedHead) || !GitAvailable() {
			return primaryErr
		}
		fallbackErr := updateGitRepoCommand(ctx, cfg)
		if fallbackErr == nil {
			return nil
		}
		return errors.Join(
			fmt.Errorf("sync: built-in Git failed: %w", primaryErr),
			fmt.Errorf("sync: system Git fallback failed: %w", fallbackErr),
		)
	}

	primaryErr := cloneGitRepo(ctx, cfg)
	if primaryErr == nil {
		return nil
	}
	if errors.Is(primaryErr, errCloneTarget) {
		return primaryErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if GitAvailable() {
		if fallbackErr := cloneGitRepoCommand(ctx, cfg); fallbackErr == nil {
			return nil
		} else {
			return errors.Join(
				fmt.Errorf("sync: built-in Git failed: %w", primaryErr),
				fmt.Errorf("sync: system Git fallback failed: %w", fallbackErr),
			)
		}
	}
	return primaryErr
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
	if err := ctx.Err(); err != nil {
		return ChangeSummary{}, err
	}
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return ChangeSummary{}, fmt.Errorf("could not open repository for change calculation: %w", err)
	}
	oldCommit, err := repo.CommitObject(plumbing.NewHash(oldRevision))
	if err != nil {
		return ChangeSummary{}, fmt.Errorf("could not load old commit %s: %w", oldRevision, err)
	}
	newCommit, err := repo.CommitObject(plumbing.NewHash(newRevision))
	if err != nil {
		return ChangeSummary{}, fmt.Errorf("could not load new commit %s: %w", newRevision, err)
	}
	oldTree, err := oldCommit.Tree()
	if err != nil {
		return ChangeSummary{}, fmt.Errorf("could not load old tree: %w", err)
	}
	newTree, err := newCommit.Tree()
	if err != nil {
		return ChangeSummary{}, fmt.Errorf("could not load new tree: %w", err)
	}
	changes, err := object.DiffTreeContext(ctx, oldTree, newTree)
	if err != nil {
		return ChangeSummary{}, fmt.Errorf("could not compare repository trees: %w", err)
	}
	var summary ChangeSummary
	for _, change := range changes {
		action, actionErr := change.Action()
		if actionErr != nil {
			return ChangeSummary{}, actionErr
		}
		path := change.To.Name
		if action == merkletrie.Delete {
			path = change.From.Name
		}
		cpv, ok := ebuildCPV(path)
		if !ok {
			continue
		}
		switch action {
		case merkletrie.Insert:
			summary.Added = append(summary.Added, cpv)
		case merkletrie.Delete:
			summary.Removed = append(summary.Removed, cpv)
		case merkletrie.Modify:
			summary.Modified = append(summary.Modified, cpv)
		}
	}
	sort.Strings(summary.Added)
	sort.Strings(summary.Removed)
	sort.Strings(summary.Modified)
	affected := make(map[string]struct{})
	for _, cpv := range append(append(append([]string{}, summary.Added...), summary.Removed...), summary.Modified...) {
		if cp, ok := cpFromCPV(cpv); ok {
			affected[cp] = struct{}{}
		}
	}
	for cp := range affected {
		before, versionErr := treePackageVersions(oldTree, cp)
		if versionErr != nil {
			return ChangeSummary{}, versionErr
		}
		after, versionErr := treePackageVersions(newTree, cp)
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return nil, err
	}
	commit, err := repo.CommitObject(plumbing.NewHash(revision))
	if err != nil {
		return nil, err
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, err
	}
	return treePackageVersions(tree, cp)
}

func treePackageVersions(tree *object.Tree, cp string) ([]string, error) {
	_, packageName, ok := strings.Cut(cp, "/")
	if !ok {
		return nil, fmt.Errorf("sync: invalid package key %q", cp)
	}
	packageTree, err := tree.Tree(cp)
	if errors.Is(err, object.ErrDirectoryNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var versions []string
	prefix := packageName + "-"
	for _, entry := range packageTree.Entries {
		if !strings.HasPrefix(entry.Name, prefix) || !strings.HasSuffix(entry.Name, ".ebuild") {
			continue
		}
		versions = append(versions, strings.TrimSuffix(strings.TrimPrefix(entry.Name, prefix), ".ebuild"))
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
	return cloneGitRepoAtomically(ctx, cfg, func(staging string) error {
		cfg.progress("clone", "Cloning repository with system Git fallback")
		return runGit(ctx, cfg.Output, "clone", "--progress", "--depth", strconv.Itoa(cfg.Depth), cfg.RepoURL, staging)
	})
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
	return cloneGitRepoAtomically(ctx, cfg, func(staging string) error {
		cfg.progress("clone", "Cloning repository with built-in transport")
		_, err := git.PlainCloneContext(ctx, staging, false, &git.CloneOptions{
			URL:      cfg.RepoURL,
			Depth:    cfg.Depth,
			Progress: cfg.Output,
		})
		if err != nil {
			return fmt.Errorf("could not clone the repository: %w", err)
		}
		return nil
	})
}

func cloneGitRepoAtomically(ctx context.Context, cfg SyncConfig, clone func(string) error) error {
	if _, err := os.Lstat(cfg.TargetDir); err == nil {
		return fmt.Errorf("sync: %w: %s", errCloneTarget, cfg.TargetDir)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("sync: inspect clone target: %w", err)
	}
	parent := filepath.Dir(cfg.TargetDir)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("sync: inspect clone target parent: %w", err)
	}
	if !parentInfo.IsDir() {
		return fmt.Errorf("sync: clone target parent is not a directory: %s", parent)
	}
	staging, err := os.MkdirTemp(parent, "."+filepath.Base(cfg.TargetDir)+".clone-")
	if err != nil {
		return fmt.Errorf("sync: create clone staging directory: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(staging)
		}
	}()
	if err := clone(staging); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !isGitRepo(staging) {
		return errors.New("sync: clone transport completed without producing a Git repository")
	}
	if err := os.Rename(staging, cfg.TargetDir); err != nil {
		return fmt.Errorf("sync: publish cloned repository: %w", err)
	}
	published = true
	return nil
}

func updateGitRepo(ctx context.Context, cfg SyncConfig) error {
	cfg.progress("check", "Checking repository state")
	repo, err := git.PlainOpen(cfg.TargetDir)
	if err != nil {
		return fmt.Errorf("could not open the local repository: %w", err)
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("could not check repository working tree status: %w", err)
	}
	status, err := worktree.Status()
	if err != nil {
		return fmt.Errorf("could not check repository working tree status: %w", err)
	}
	if !status.IsClean() {
		return errDirtyWorktree
	}

	head, err := repo.Reference(plumbing.HEAD, false)
	if err != nil {
		return fmt.Errorf("could not inspect repository HEAD: %w", err)
	}
	if head.Type() != plumbing.SymbolicReference ||
		!head.Target().IsBranch() {
		return errDetachedHead
	}
	branch := head.Target()
	oldRevision, err := repo.ResolveRevision(plumbing.Revision(branch.String()))
	if err != nil {
		return fmt.Errorf("could not resolve local branch %s: %w", branch.Short(), err)
	}

	remote, err := repo.Remote("origin")
	if err != nil {
		return fmt.Errorf("could not contact the remote repository: %w", err)
	}
	cfg.progress("fetch", "Fetching "+branch.Short()+" from origin")
	err = remote.FetchContext(ctx, &git.FetchOptions{
		RemoteName: "origin",
		Depth:      cfg.Depth,
		Progress:   cfg.Output,
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) && !strings.Contains(err.Error(), "already up-to-date") {
		return fmt.Errorf("could not fetch updates from the remote: %w", err)
	}

	remoteBranch := plumbing.NewRemoteReferenceName("origin", branch.Short())
	newRevision, err := repo.ResolveRevision(plumbing.Revision(remoteBranch.String()))
	if err != nil {
		return fmt.Errorf("could not resolve origin/%s after fetch: %w", branch.Short(), err)
	}
	if *oldRevision == *newRevision {
		cfg.progress("unchanged", "Already up to date")
		return nil
	}

	cfg.progress("update", "Updating working tree")
	if err := worktree.Reset(&git.ResetOptions{Commit: *newRevision, Mode: git.HardReset}); err != nil {
		return fmt.Errorf("could not update the working tree: %w", err)
	}
	if cfg.Changes != nil {
		cfg.progress("changes", "Calculating package changes")
		changes, changeErr := gitEbuildChanges(ctx, cfg.TargetDir, oldRevision.String(), newRevision.String())
		if changeErr != nil {
			return changeErr
		}
		cfg.Changes(changes)
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
