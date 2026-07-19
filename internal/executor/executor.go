// Package executor connects verified resolver actions to the serial build and
// transaction pipeline. Live ROOT remains fail-closed until lifecycle writes
// are staged inside the journal boundary.
package executor

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/airencracken/arise/internal/oplock"
	"github.com/airencracken/arise/internal/rebuild"
	"github.com/airencracken/arise/internal/resolve"
)

type PackageRunner func(context.Context, string, *rebuild.RebuildConfig) error
type ActionPreflight func(resolve.PkgAction, *rebuild.RebuildConfig) error

type Config struct {
	Rebuild    rebuild.RebuildConfig
	ResumePath string
	Runner     PackageRunner
	Preflight  ActionPreflight
	// ValidateLocked reruns state authorization after acquiring the live VDB
	// lock and immediately before the first worker starts.
	ValidateLocked func() error
}

func Execute(ctx context.Context, result *resolve.ResolveResult, cfg Config) error {
	if result == nil || !result.Verified || result.Verification != resolve.VerificationVerified || len(result.Conflicts) != 0 {
		return fmt.Errorf("executor: refusing non-verified plan")
	}
	root, err := filepath.Abs(cfg.Rebuild.RootDir)
	if err != nil {
		return fmt.Errorf("executor: resolve ROOT: %w", err)
	}
	if filepath.Clean(root) == string(filepath.Separator) {
		if !cfg.Rebuild.AllowLiveRoot {
			return fmt.Errorf("executor: live ROOT requires explicit canary eligibility")
		}
		if len(result.Install) != 1 {
			return fmt.Errorf("executor: live canary requires exactly one install action")
		}
		if action := result.Install[0].Action; action != "install" && action != "reinstall" && action != "update" {
			return fmt.Errorf("executor: live canary does not support action %q", action)
		}
	}
	if len(result.Uninstall) != 0 {
		return fmt.Errorf("executor: removal actions are not supported by the install canary executor")
	}
	if cfg.Runner == nil {
		cfg.Runner = rebuild.RebuildPackage
	}
	if cfg.Preflight == nil {
		cfg.Preflight = PreflightAction
	}
	// Preflight every action before the first build or merge.
	for _, action := range result.Install {
		actionCfg := actionRebuildConfig(cfg.Rebuild, action)
		if err := cfg.Preflight(action, &actionCfg); err != nil {
			return fmt.Errorf("executor: preflight %s: %w", actionLabel(action), err)
		}
	}
	if filepath.Clean(root) == string(filepath.Separator) {
		lock, err := oplock.TryAcquireVDB(cfg.Rebuild.VdbDir)
		if err != nil {
			return fmt.Errorf("executor: acquire operation VDB lock: %w", err)
		}
		defer lock.Release()
		cfg.Rebuild.VDBLockHeld = true
		if cfg.ValidateLocked == nil {
			return fmt.Errorf("executor: live canary requires locked state validation")
		}
		if err := cfg.ValidateLocked(); err != nil {
			return fmt.Errorf("executor: locked state validation: %w", err)
		}
	}
	if cfg.ResumePath != "" {
		if err := resolve.SaveResume(cfg.ResumePath, result); err != nil {
			return fmt.Errorf("executor: initialize resume state: %w", err)
		}
	}
	for _, action := range result.Install {
		if err := ctx.Err(); err != nil {
			return err
		}
		actionCfg := actionRebuildConfig(cfg.Rebuild, action)
		atomText := actionLabel(action)
		if err := cfg.Runner(ctx, atomText, &actionCfg); err != nil {
			return fmt.Errorf("executor: %s: %w", atomText, err)
		}
		if cfg.ResumePath != "" {
			if err := resolve.MarkResumeComplete(cfg.ResumePath, action.Atom.String()); err != nil {
				return fmt.Errorf("executor: commit resume state for %s: %w", atomText, err)
			}
		}
	}
	return nil
}

func PreflightAction(action resolve.PkgAction, cfg *rebuild.RebuildConfig) error {
	if action.Atom == nil || action.Atom.Version == nil || action.Atom.Version.Raw == "" {
		return fmt.Errorf("action lacks exact package version")
	}
	if action.Domain != "" && action.Domain != resolve.DomainROOT {
		return fmt.Errorf("unsupported mutation domain %s", action.Domain)
	}
	if action.MergeType != "" && action.MergeType != "source" {
		return fmt.Errorf("unsupported merge type %q", action.MergeType)
	}
	if action.Repository == "" || action.RepositoryPath == "" {
		return fmt.Errorf("action lacks repository identity")
	}
	if cfg == nil || !cfg.PhaseProtocol {
		return fmt.Errorf("versioned phase protocol is required")
	}
	if cfg.PhaseLogDir == "" {
		return fmt.Errorf("durable package log directory is required")
	}
	if cfg.JournalDir == "" {
		return fmt.Errorf("durable journal directory is required")
	}
	return rebuild.PreflightPackage(actionLabel(action), cfg)
}

func actionRebuildConfig(base rebuild.RebuildConfig, action resolve.PkgAction) rebuild.RebuildConfig {
	base.RepoDir = action.RepositoryPath
	base.Repository = action.Repository
	base.UseFlags = cloneUse(action.UseFlags)
	base.SourceURI = action.SrcURI
	base.AllowLiveReplacement = base.AllowLiveRoot && action.Action == "reinstall"
	base.AllowLiveUpgrade = base.AllowLiveRoot && action.Action == "update"
	if base.AllowLiveUpgrade {
		base.AllowLiveReplacement = true
	}
	return base
}

func cloneUse(src map[string]bool) map[string]bool {
	dst := make(map[string]bool, len(src))
	for name, enabled := range src {
		dst[name] = enabled
	}
	return dst
}

func actionLabel(action resolve.PkgAction) string {
	if action.Atom == nil {
		return "<nil>"
	}
	label := action.Atom.CP()
	if action.Atom.Version != nil && action.Atom.Version.Raw != "" {
		label += "-" + action.Atom.Version.Raw
	}
	return label
}
