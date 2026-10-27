package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/airencracken/arise/internal/ebuild"
	"github.com/airencracken/arise/internal/merge"
	"github.com/airencracken/arise/internal/rebuild"
	"github.com/airencracken/arise/internal/resolve"
)

// Blocker removals run after the requesting package has built, under the same
// mutation lock as its merge. Its install checkpoint remains pending until both
// operations finish. On resume, resolution re-creates any still-needed removals
// from the pending requester's dependencies; a committed removal is not repeated.
type blockerRemovals struct {
	mu        sync.Mutex
	actions   []resolve.PkgAction
	completed map[int]bool
}

func (removals *blockerRemovals) wrapMutation(action resolve.PkgAction, packageCfg *rebuild.RebuildConfig, cfg Config, installs int) {
	beginMutation := packageCfg.BeginMutation
	packageCfg.VDBLockHeld = true
	packageCfg.BeginMutation = func(ctx context.Context) (func() error, error) {
		var release func() error
		var err error
		if beginMutation != nil {
			release, err = beginMutation(ctx)
		} else {
			release, err = cfg.AcquireMutationLock(ctx, cfg.Rebuild.VdbDir)
			if err == nil && release != nil && cfg.ValidateLocked != nil {
				err = cfg.ValidateLocked()
			}
			if err == nil && release != nil && cfg.PrepareMutation != nil {
				err = cfg.PrepareMutation(ctx)
			}
		}
		if err == nil && release == nil {
			err = fmt.Errorf("mutation boundary returned a nil release function")
		}
		if err == nil {
			err = removals.beforeMerge(ctx, action, packageCfg, cfg, installs)
		}
		if err != nil {
			if release != nil {
				err = errors.Join(err, release())
			}
			return nil, err
		}
		return release, nil
	}
}

func planBlockerRemovals(result *resolve.ResolveResult, base rebuild.RebuildConfig) (*blockerRemovals, error) {
	removals := &blockerRemovals{actions: result.Uninstall, completed: make(map[int]bool)}
	seen := make(map[string]bool)
	for _, removal := range result.Uninstall {
		if base.BuildOnly {
			return nil, fmt.Errorf("executor: build-only plan cannot remove installed packages")
		}
		identity := resolve.ActionIdentity(removal)
		if seen[identity] {
			return nil, fmt.Errorf("executor: duplicate removal %s", actionLabel(removal))
		}
		seen[identity] = true
		if err := preflightRemoval(removal, &base); err != nil {
			return nil, fmt.Errorf("executor: preflight removal %s: %w", actionLabel(removal), err)
		}
		if len(removal.RemovalBefore) == 0 {
			return nil, fmt.Errorf("executor: removal %s has no planned blocker requester", actionLabel(removal))
		}
		for _, requester := range removal.RemovalBefore {
			found := false
			for _, install := range result.Install {
				if install.Atom != nil && install.Atom.CP() == requester && (install.Domain == "" || install.Domain == resolve.DomainROOT) {
					found = true
				}
			}
			if !found {
				return nil, fmt.Errorf("executor: removal %s references missing blocker requester %s", actionLabel(removal), requester)
			}
		}
	}
	return removals, nil
}

func preflightRemoval(action resolve.PkgAction, cfg *rebuild.RebuildConfig) error {
	if action.Action != "uninstall" || action.Atom == nil || action.Atom.Version == nil {
		return fmt.Errorf("removal requires an exact uninstall action")
	}
	if action.Domain != "" && action.Domain != resolve.DomainROOT {
		return fmt.Errorf("unsupported removal domain %s", action.Domain)
	}
	if cfg == nil || !cfg.PhaseProtocol || cfg.JournalDir == "" || cfg.PhaseLogDir == "" || cfg.WorkDirBase == "" || !filepath.IsAbs(cfg.VdbDir) {
		return fmt.Errorf("removal requires phase protocol, an absolute VDB path, and work, log and journal paths")
	}
	path := filepath.Join(cfg.VdbDir, actionLabel(action))
	// Never follow an installed-entry/category symlink into a different VDB.
	for _, directory := range []string{filepath.Dir(path), path} {
		info, err := os.Lstat(directory)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("removal VDB path is not a directory: %s", directory)
		}
	}
	for name, expected := range map[string]string{"SLOT": action.Slot, "repository": action.Repository} {
		data, err := os.ReadFile(filepath.Join(path, name))
		if err != nil {
			return err
		}
		actual := strings.TrimSpace(string(data))
		if name == "SLOT" {
			slot, subslot, _ := strings.Cut(actual, "/")
			if subslot == "" {
				subslot = slot
			}
			if action.Subslot != "" && subslot != action.Subslot {
				return fmt.Errorf("installed subslot changed for %s", actionLabel(action))
			}
			actual = slot
		}
		if expected != "" && actual != expected {
			return fmt.Errorf("installed %s changed for %s", name, actionLabel(action))
		}
	}
	for _, name := range []string{"CONTENTS", "environment.bz2", action.Atom.Package + "-" + action.Atom.Version.Raw + ".ebuild"} {
		info, err := os.Lstat(filepath.Join(path, name))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("installed %s is not a regular file", name)
		}
	}
	_, err := ebuild.ParseEbuild(filepath.Join(path, action.Atom.Package+"-"+action.Atom.Version.Raw+".ebuild"))
	return err
}

func (removals *blockerRemovals) beforeMerge(ctx context.Context, requester resolve.PkgAction, packageCfg *rebuild.RebuildConfig, cfg Config, installs int) error {
	removals.mu.Lock()
	defer removals.mu.Unlock()
	for index, action := range removals.actions {
		if removals.completed[index] || !slices.Contains(action.RemovalBefore, requester.Atom.CP()) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := preflightRemoval(action, packageCfg); err != nil {
			return err
		}
		position, total := installs+index+1, installs+len(removals.actions)
		if cfg.OnActionStart != nil {
			cfg.OnActionStart(position, total, action)
		}
		// Use the installed lifecycle, not the requesting package's selected
		// source metadata. The operation VDB lock is already held by BeginMutation.
		removeCfg := cfg.Rebuild
		removeCfg.VDBLockHeld = true
		removeCfg.OnNotice = func(class, message string) {
			if cfg.OnActionNotice != nil {
				cfg.OnActionNotice(position, total, action, class, message)
			}
		}
		removeCfg.OnTransactionCommit = func(error) error {
			removals.completed[index] = true
			if cfg.RecordLockedMutation != nil {
				if err := cfg.RecordLockedMutation(); err != nil {
					return err
				}
			}
			if cfg.OnActionComplete != nil {
				cfg.OnActionComplete(position, total, action)
			}
			return nil
		}
		if err := removeInstalledPackage(ctx, action, &removeCfg); err != nil {
			return fmt.Errorf("remove blocker %s: %w", actionLabel(action), err)
		}
	}
	return nil
}

func removeInstalledPackage(ctx context.Context, action resolve.PkgAction, cfg *rebuild.RebuildConfig) (returnErr error) {
	path := filepath.Join(cfg.VdbDir, actionLabel(action))
	lifecycle, err := rebuild.OpenInstalledLifecycle(path, cfg)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, lifecycle.Close()) }()
	var hookErrors []error
	runHook := func(phase string) func() error {
		return func() error {
			if err := lifecycle.Run(ctx, phase); err != nil {
				hookErrors = append(hookErrors, err)
			}
			return nil // Portage removes the package even when a removal hook fails.
		}
	}
	err = merge.UnmergeWithConfig(ctx, merge.UnmergeConfig{
		RootDir: cfg.RootDir, VDBDir: cfg.VdbDir, PackagePath: path, JournalDir: cfg.JournalDir,
		AllowLiveRoot: cfg.AllowLiveRoot, VDBLockHeld: cfg.VDBLockHeld,
		BeforeRemoval: runHook("pkg_prerm"), AfterRemoval: runHook("pkg_postrm"),
	})
	if err != nil {
		return err
	}
	if cfg.OnTransactionCommit != nil {
		if err := cfg.OnTransactionCommit(nil); err != nil {
			return &merge.PostCommitError{Err: err}
		}
	}
	if len(hookErrors) != 0 && cfg.OnNotice != nil {
		cfg.OnNotice("WARN", "removed "+actionLabel(action)+" with lifecycle errors: "+errors.Join(hookErrors...).Error())
	}
	return nil
}
