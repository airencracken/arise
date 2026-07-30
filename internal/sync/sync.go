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

	// RepositoryName, when set, must match profiles/repo_name before sync can
	// report success.
	RepositoryName string

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
	CP          string
	Kind        string
	Before      []string
	After       []string
	Description string
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
	if err := repairExistingRepositoryAccess(cfg.TargetDir); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(cfg.SyncType)) {
	case "rsync":
		if err := syncRsync(ctx, cfg); err != nil {
			return err
		}
		return validateRepositoryIdentity(cfg)
	case "", "git":
	default:
		return fmt.Errorf("sync: unsupported sync type %q", cfg.SyncType)
	}

	if isGitRepo(cfg.TargetDir) {
		if err := validateRepositoryIdentity(cfg); err != nil {
			return err
		}
		primaryErr := updateGitRepo(ctx, cfg)
		if primaryErr == nil || errors.Is(primaryErr, errDirtyWorktree) ||
			errors.Is(primaryErr, errDetachedHead) || !GitAvailable() {
			if primaryErr != nil {
				return primaryErr
			}
			return validateRepositoryIdentity(cfg)
		}
		fallbackErr := updateGitRepoCommand(ctx, cfg)
		if fallbackErr == nil {
			return validateRepositoryIdentity(cfg)
		}
		return errors.Join(
			fmt.Errorf("sync: built-in Git failed: %w", primaryErr),
			fmt.Errorf("sync: system Git fallback failed: %w", fallbackErr),
		)
	}

	primaryErr := cloneGitRepo(ctx, cfg)
	if primaryErr == nil {
		return validateRepositoryIdentity(cfg)
	}
	if errors.Is(primaryErr, errCloneTarget) {
		return primaryErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if GitAvailable() {
		if fallbackErr := cloneGitRepoCommand(ctx, cfg); fallbackErr == nil {
			return validateRepositoryIdentity(cfg)
		} else {
			return errors.Join(
				fmt.Errorf("sync: built-in Git failed: %w", primaryErr),
				fmt.Errorf("sync: system Git fallback failed: %w", fallbackErr),
			)
		}
	}
	return primaryErr
}

func repairExistingRepositoryAccess(target string) error {
	info, err := os.Stat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("sync: inspect repository access: %w", err)
	}
	if !info.IsDir() {
		return nil
	}
	parent, err := os.Stat(filepath.Dir(target))
	if err != nil {
		return fmt.Errorf("sync: inspect repository parent access: %w", err)
	}
	return addParentVisibility(target, info, parent)
}

func addParentVisibility(path string, info, parent os.FileInfo) error {
	visibility := parent.Mode().Perm() & 0o055
	mode := info.Mode().Perm() | visibility
	if mode == info.Mode().Perm() {
		return nil
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("sync: make repository root traversable: %w", err)
	}
	return nil
}

func validateRepositoryIdentity(cfg SyncConfig) error {
	if strings.TrimSpace(cfg.RepositoryName) == "" {
		return nil
	}
	path := filepath.Join(cfg.TargetDir, "profiles", "repo_name")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("sync: validate repository identity %s: %w", path, err)
	}
	name := strings.TrimSpace(string(data))
	if name != cfg.RepositoryName {
		return fmt.Errorf("sync: repository identity is %q, expected %q", name, cfg.RepositoryName)
	}
	return nil
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

	branch, err := gitOutput(ctx, "-C", cfg.TargetDir, "branch", "--show-current")
	if err != nil {
		return err
	}
	if branch == "" {
		return errDetachedHead
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

	cfg.progress("validate", "Checking working tree for local changes")
	dirty, err := gitOutput(ctx, "-C", cfg.TargetDir, "status", "--porcelain")
	if err != nil {
		return err
	}
	if dirty != "" {
		return errDirtyWorktree
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
			CP:          cp,
			Kind:        packageChangeKind(before, after),
			Before:      before,
			After:       after,
			Description: treePackageDescription(newTree, oldTree, cp, before, after),
		})
	}
	sort.Slice(summary.Packages, func(i, j int) bool { return summary.Packages[i].CP < summary.Packages[j].CP })
	return summary, nil
}

func treePackageDescription(newTree, oldTree *object.Tree, cp string, before, after []string) string {
	tree := newTree
	versions := after
	if len(versions) == 0 {
		tree = oldTree
		versions = before
	}
	if tree == nil || len(versions) == 0 {
		return ""
	}
	version := preferredDisplayVersion(versions)
	for _, path := range []string{
		"metadata/md5-cache/" + cp + "-" + version,
		packageEbuildPath(cp, version),
	} {
		file, err := tree.File(path)
		if err != nil {
			continue
		}
		content, err := file.Contents()
		if err != nil {
			continue
		}
		if description := descriptionFromMetadata(content); description != "" {
			return description
		}
	}
	return ""
}

func packageEbuildPath(cp, version string) string {
	category, packageName, ok := strings.Cut(cp, "/")
	if !ok {
		return ""
	}
	return category + "/" + packageName + "/" + packageName + "-" + version + ".ebuild"
}

func descriptionFromMetadata(content string) string {
	for _, line := range strings.Split(content, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || key != "DESCRIPTION" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'')) {
			if value[0] == '"' {
				if unquoted, err := strconv.Unquote(value); err == nil {
					return unquoted
				}
			}
			return value[1 : len(value)-1]
		}
		return value
	}
	return ""
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
	stagingInfo, err := os.Stat(staging)
	if err != nil {
		return fmt.Errorf("sync: inspect clone staging access: %w", err)
	}
	if err := addParentVisibility(staging, stagingInfo, parentInfo); err != nil {
		return err
	}
	stagedCfg := cfg
	stagedCfg.TargetDir = staging
	if err := validateRepositoryIdentity(stagedCfg); err != nil {
		return err
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

	cfg.progress("validate", "Checking working tree for local changes")
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
	var before repositoryEbuildSnapshot
	var err error
	if cfg.Changes != nil {
		before, err = snapshotRepositoryEbuilds(cfg.TargetDir)
		if err != nil {
			return fmt.Errorf("could not snapshot repository before rsync: %w", err)
		}
	}
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
	if cfg.Changes != nil {
		cfg.progress("changes", "Calculating package changes")
		after, snapshotErr := snapshotRepositoryEbuilds(cfg.TargetDir)
		if snapshotErr != nil {
			return fmt.Errorf("could not snapshot repository after rsync: %w", snapshotErr)
		}
		cfg.Changes(compareRepositoryEbuildSnapshots(before, after))
	}
	return nil
}

type repositoryEbuildSnapshot struct {
	Files       map[string]string
	Versions    map[string][]string
	Description map[string]string
}

func snapshotRepositoryEbuilds(root string) (repositoryEbuildSnapshot, error) {
	snapshot := repositoryEbuildSnapshot{
		Files:       make(map[string]string),
		Versions:    make(map[string][]string),
		Description: make(map[string]string),
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) && path == root {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ebuild") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		cpv, ok := ebuildCPV(filepath.ToSlash(relative))
		if !ok {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snapshot.Files[cpv] = string(content)
		cp, ok := cpFromCPV(cpv)
		if !ok {
			return nil
		}
		version := strings.TrimPrefix(cpv, cp+"-")
		snapshot.Versions[cp] = append(snapshot.Versions[cp], version)
		if description := descriptionFromMetadata(string(content)); description != "" {
			snapshot.Description[cpv] = description
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return repositoryEbuildSnapshot{}, err
	}
	for cp := range snapshot.Versions {
		sortVersions(snapshot.Versions[cp])
	}
	return snapshot, nil
}

func compareRepositoryEbuildSnapshots(before, after repositoryEbuildSnapshot) ChangeSummary {
	var summary ChangeSummary
	affected := make(map[string]struct{})
	for cpv, oldContent := range before.Files {
		newContent, exists := after.Files[cpv]
		switch {
		case !exists:
			summary.Removed = append(summary.Removed, cpv)
		case newContent != oldContent:
			summary.Modified = append(summary.Modified, cpv)
		}
	}
	for cpv := range after.Files {
		if _, exists := before.Files[cpv]; !exists {
			summary.Added = append(summary.Added, cpv)
		}
	}
	for _, cpv := range append(append(append([]string{}, summary.Added...), summary.Removed...), summary.Modified...) {
		if cp, ok := cpFromCPV(cpv); ok {
			affected[cp] = struct{}{}
		}
	}
	sort.Strings(summary.Added)
	sort.Strings(summary.Removed)
	sort.Strings(summary.Modified)
	for cp := range affected {
		oldVersions := before.Versions[cp]
		newVersions := after.Versions[cp]
		description := snapshotPackageDescription(after, cp, newVersions)
		if len(newVersions) == 0 {
			description = snapshotPackageDescription(before, cp, oldVersions)
		}
		summary.Packages = append(summary.Packages, PackageChange{
			CP: cp, Kind: packageChangeKind(oldVersions, newVersions),
			Before: oldVersions, After: newVersions, Description: description,
		})
	}
	sort.Slice(summary.Packages, func(i, j int) bool { return summary.Packages[i].CP < summary.Packages[j].CP })
	return summary
}

func snapshotPackageDescription(snapshot repositoryEbuildSnapshot, cp string, versions []string) string {
	if len(versions) == 0 {
		return ""
	}
	return snapshot.Description[cp+"-"+preferredDisplayVersion(versions)]
}

func preferredDisplayVersion(versions []string) string {
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

func sortVersions(versions []string) {
	sort.Slice(versions, func(i, j int) bool {
		left, leftErr := atom.ParseVersion(versions[i])
		right, rightErr := atom.ParseVersion(versions[j])
		if leftErr != nil || rightErr != nil {
			return versions[i] < versions[j]
		}
		return left.Compare(right) < 0
	})
}
