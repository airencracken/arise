// Package executor connects verified resolver actions to the serial build and
// transaction pipeline. Live ROOT remains fail-closed until lifecycle writes
// are staged inside the journal boundary.
package executor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"

	"github.com/airencracken/arise/internal/fetch"
	"github.com/airencracken/arise/internal/merge"
	"github.com/airencracken/arise/internal/oplock"
	"github.com/airencracken/arise/internal/rebuild"
	"github.com/airencracken/arise/internal/resolve"
)

type PackageRunner func(context.Context, string, *rebuild.RebuildConfig) error
type ActionPreflight func(resolve.PkgAction, *rebuild.RebuildConfig) error

type PreflightFailure struct {
	Action resolve.PkgAction
	Err    error
}

func (f PreflightFailure) Error() string {
	return fmt.Sprintf("preflight %s: %v", actionLabel(f.Action), f.Err)
}

// PreflightAll validates every install action and deliberately does not apply
// Execute's live-root canary size/removal limits. It performs no fetch, build,
// merge, VDB/world, journal, resume or operation-lock mutation and returns the
// complete failure set for large-plan compatibility audits.
func PreflightAll(result *resolve.ResolveResult, base rebuild.RebuildConfig) []PreflightFailure {
	if result == nil || !result.Verified || result.Verification != resolve.VerificationVerified || len(result.Conflicts) != 0 {
		return []PreflightFailure{{Err: fmt.Errorf("refusing non-verified plan")}}
	}
	var failures []PreflightFailure
	for _, action := range result.Install {
		actionCfg := actionRebuildConfig(base, action)
		if err := PreflightAction(action, &actionCfg); err != nil {
			failures = append(failures, PreflightFailure{Action: action, Err: err})
		}
	}
	return failures
}

type Config struct {
	Rebuild          rebuild.RebuildConfig
	ResumePath       string
	Jobs             int
	LoadAverage      float64
	Runner           PackageRunner
	Preflight        ActionPreflight
	OnActionStart    func(index, total int, action resolve.PkgAction)
	OnActionComplete func(index, total int, action resolve.PkgAction)
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
		if len(result.Install) == 0 {
			return fmt.Errorf("executor: live execution requires at least one install action")
		}
		if len(result.Install) > 2 && cfg.ResumePath == "" {
			return fmt.Errorf("executor: live plans larger than two actions require durable resume state")
		}
		for _, item := range result.Install {
			if action := item.Action; action != "install" && action != "reinstall" && action != "update" {
				return fmt.Errorf("executor: live execution does not support action %q for %s", action, actionLabel(item))
			}
		}
	}
	if len(result.Uninstall) != 0 {
		return fmt.Errorf("executor: removal actions are not supported by the install canary executor")
	}
	if cfg.Runner == nil {
		cfg.Runner = rebuild.RebuildPackage
	}
	if cfg.Rebuild.Fetcher == nil {
		cfg.Rebuild.Fetcher = &fetch.Fetcher{}
	}
	if cfg.Rebuild.CallbackLock == nil {
		cfg.Rebuild.CallbackLock = &sync.Mutex{}
	}
	if cfg.Preflight == nil {
		cfg.Preflight = PreflightAction
	}
	// Preflight every action before the first build or merge, retaining the
	// action-specific frozen policy/query results for its eventual worker.
	actionConfigs := make([]rebuild.RebuildConfig, len(result.Install))
	for index, action := range result.Install {
		actionCfg := actionRebuildConfig(cfg.Rebuild, action)
		if err := cfg.Preflight(action, &actionCfg); err != nil {
			return fmt.Errorf("executor: preflight %s: %w", actionLabel(action), err)
		}
		actionConfigs[index] = actionCfg
	}
	if filepath.Clean(root) == string(filepath.Separator) {
		lock, err := oplock.TryAcquireVDB(cfg.Rebuild.VdbDir)
		if err != nil {
			return fmt.Errorf("executor: acquire operation VDB lock: %w", err)
		}
		defer lock.Release()
		cfg.Rebuild.VDBLockHeld = true
		for index := range actionConfigs {
			actionConfigs[index].VDBLockHeld = true
		}
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
	if cfg.Jobs > 1 && len(result.Install) > 1 {
		return executeConcurrent(ctx, result.Install, actionConfigs, cfg)
	}
	for index, action := range result.Install {
		if err := ctx.Err(); err != nil {
			return err
		}
		if cfg.OnActionStart != nil {
			cfg.OnActionStart(index+1, len(result.Install), action)
		}
		actionCfg := actionConfigs[index]
		atomText := actionLabel(action)
		if err := cfg.Runner(ctx, atomText, &actionCfg); err != nil {
			var postCommit *merge.PostCommitError
			if errors.As(err, &postCommit) && cfg.ResumePath != "" {
				if markErr := resolve.MarkResumeComplete(cfg.ResumePath, action.Atom.String()); markErr != nil {
					return fmt.Errorf("executor: %s committed but post-commit lifecycle failed: %v; mark resume complete: %w", atomText, err, markErr)
				}
			}
			return fmt.Errorf("executor: %s: %w", atomText, err)
		}
		if cfg.OnActionComplete != nil {
			cfg.OnActionComplete(index+1, len(result.Install), action)
		}
		if cfg.ResumePath != "" {
			if err := resolve.MarkResumeComplete(cfg.ResumePath, action.Atom.String()); err != nil {
				return fmt.Errorf("executor: commit resume state for %s: %w", atomText, err)
			}
		}
	}
	return nil
}

type concurrentResult struct {
	index int
	err   error
}

func executeConcurrent(ctx context.Context, actions []resolve.PkgAction, actionConfigs []rebuild.RebuildConfig, cfg Config) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	identities := make(map[string]int, len(actions))
	for index, action := range actions {
		identity := resolve.ActionIdentity(action)
		if _, exists := identities[identity]; exists {
			return fmt.Errorf("executor: duplicate planned action identity %q", identity)
		}
		identities[identity] = index
	}
	remaining := make([]int, len(actions))
	dependents := make(map[int][]int)
	for index, action := range actions {
		for _, prerequisite := range action.Prerequisites {
			before, ok := identities[prerequisite]
			if !ok {
				return fmt.Errorf("executor: %s references missing prerequisite %q", actionLabel(action), prerequisite)
			}
			remaining[index]++
			dependents[before] = append(dependents[before], index)
		}
	}
	ready := make([]int, 0, len(actions))
	for index, count := range remaining {
		if count == 0 {
			ready = append(ready, index)
		}
	}
	if len(ready) == 0 {
		return fmt.Errorf("executor: planned prerequisite graph has no ready action")
	}
	sort.Ints(ready)
	results := make(chan concurrentResult, len(actions))
	commitLock := &sync.Mutex{}
	running, completed := 0, 0
	launch := func(index int) {
		running++
		action := actions[index]
		go func() {
			if err := rebuild.WaitForLoadContext(ctx, cfg.LoadAverage); err != nil {
				results <- concurrentResult{index: index, err: err}
				return
			}
			if cfg.OnActionStart != nil {
				cfg.OnActionStart(index+1, len(actions), action)
			}
			actionCfg := actionConfigs[index]
			actionCfg.CommitLock = commitLock
			actionCfg.OnTransactionCommit = func(committedErr error) error {
				if cfg.ResumePath != "" {
					if err := resolve.MarkResumeComplete(cfg.ResumePath, action.Atom.String()); err != nil {
						return err
					}
				}
				if cfg.OnActionComplete != nil {
					cfg.OnActionComplete(index+1, len(actions), action)
				}
				return nil
			}
			err := cfg.Runner(ctx, actionLabel(action), &actionCfg)
			results <- concurrentResult{index: index, err: err}
		}()
	}
	for completed < len(actions) {
		for len(ready) > 0 && running < cfg.Jobs {
			index := ready[0]
			ready = ready[1:]
			launch(index)
		}
		if running == 0 {
			return fmt.Errorf("executor: planned prerequisite graph stalled after %d of %d actions", completed, len(actions))
		}
		outcome := <-results
		running--
		if outcome.err != nil {
			cancel()
			for running > 0 {
				<-results
				running--
			}
			return fmt.Errorf("executor: %s: %w", actionLabel(actions[outcome.index]), outcome.err)
		}
		completed++
		for _, dependent := range dependents[outcome.index] {
			remaining[dependent]--
			if remaining[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
		sort.Ints(ready)
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
	base.SelectedSlot = action.Slot
	if action.Subslot != "" {
		base.SelectedSlot += "/" + action.Subslot
	}
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
