package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/journal"
	"github.com/airencracken/arise/internal/merge"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/rebuild"
	"github.com/airencracken/arise/internal/resolve"
)

func action(t *testing.T, cpv string) resolve.PkgAction {
	t.Helper()
	a, err := atom.Parse(cpv)
	if err != nil {
		t.Fatal(err)
	}
	return resolve.PkgAction{Atom: a, Action: "install", Repository: "test", RepositoryPath: "/repo", MergeType: "source", Domain: resolve.DomainROOT}
}

func TestPreflightAllRejectsEveryInvalidActionWithoutShortCircuiting(t *testing.T) {
	valid := action(t, "cat/valid-1")
	wrongDomain := valid
	wrongDomain.Atom, _ = atom.Parse("cat/domain-1")
	wrongDomain.Domain = resolve.DomainBROOT
	missingRepository := valid
	missingRepository.Atom, _ = atom.Parse("cat/repository-1")
	missingRepository.Repository = ""
	result := &resolve.ResolveResult{
		Verified:     true,
		Verification: resolve.VerificationVerified,
		Install: []resolve.PkgAction{
			{Action: "install", Repository: "test", RepositoryPath: "/repo", MergeType: "source", Domain: resolve.DomainROOT},
			wrongDomain,
			missingRepository,
		},
	}
	failures := PreflightAll(result, rebuild.RebuildConfig{})
	if len(failures) != 3 {
		t.Fatalf("PreflightAll returned %d failures, want complete set of 3: %#v", len(failures), failures)
	}
	want := []string{
		"preflight <nil>: action lacks exact package version",
		"preflight cat/domain-1: unsupported mutation domain BROOT",
		"preflight cat/repository-1: action lacks repository identity",
	}
	for index, failure := range failures {
		if failure.Err == nil || failure.Error() != want[index] {
			t.Errorf("failure[%d] = %q (%v), want %q", index, failure.Error(), failure.Err, want[index])
		}
	}
}

func TestPreflightAllRejectsEveryUntrustedPlanShape(t *testing.T) {
	for name, result := range map[string]*resolve.ResolveResult{
		"nil":          nil,
		"unverified":   {Verification: resolve.VerificationVerified},
		"wrong status": {Verified: true, Verification: "incomplete"},
		"conflicted": {
			Verified: true, Verification: resolve.VerificationVerified,
			Conflicts: []string{"slot conflict"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			failures := PreflightAll(result, rebuild.RebuildConfig{})
			if len(failures) != 1 || failures[0].Action.Atom != nil ||
				failures[0].Err == nil || failures[0].Err.Error() != "refusing non-verified plan" {
				t.Fatalf("PreflightAll(%s) = %#v", name, failures)
			}
		})
	}
}

func TestPreflightAllEmptyVerifiedPlanIsReadOnlySuccess(t *testing.T) {
	result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified}
	if failures := PreflightAll(result, rebuild.RebuildConfig{}); len(failures) != 0 {
		t.Fatalf("empty verified plan failures = %#v", failures)
	}
}

func TestActionRebuildConfigCopiesEveryFrozenActionFieldWithoutAliasing(t *testing.T) {
	baseUse := map[string]bool{"base": true}
	base := rebuild.RebuildConfig{
		RepoDir: "/base/repo", Repository: "base", UseFlags: baseUse,
		SourceURI: "base-uri", SelectedSlot: "base-slot", SelectedIUSE: "base-iuse",
		AllowLiveRoot: true, AllowLiveReplacement: true, AllowLiveUpgrade: true,
	}
	item := action(t, "cat/pkg-2")
	item.RepositoryPath = "/repos/overlay"
	item.Repository = "overlay"
	item.UseFlags = map[string]bool{"enabled": true, "disabled": false}
	item.SrcURI = "https://example.invalid/source.tar.xz"
	item.Slot = "3"
	item.Subslot = "7"
	item.IUse = "+enabled disabled"
	item.Action = "install"

	got := actionRebuildConfig(base, item)
	if got.RepoDir != item.RepositoryPath || got.Repository != item.Repository ||
		got.SourceURI != item.SrcURI || got.SelectedSlot != "3/7" ||
		got.SelectedIUSE != item.IUse || got.AllowLiveReplacement || got.AllowLiveUpgrade ||
		!reflect.DeepEqual(got.UseFlags, item.UseFlags) {
		t.Fatalf("actionRebuildConfig() = %#v", got)
	}
	got.UseFlags["enabled"] = false
	got.UseFlags["new"] = true
	if !item.UseFlags["enabled"] || item.UseFlags["new"] || !baseUse["base"] {
		t.Fatalf("derived USE map aliases action or base: got=%v action=%v base=%v", got.UseFlags, item.UseFlags, baseUse)
	}
	if base.RepoDir != "/base/repo" || base.Repository != "base" ||
		base.SourceURI != "base-uri" || base.SelectedSlot != "base-slot" ||
		!base.AllowLiveReplacement || !base.AllowLiveUpgrade {
		t.Fatalf("base configuration mutated: %#v", base)
	}
}

func TestActionRebuildConfigLiveMutationEligibilityMatrix(t *testing.T) {
	for _, test := range []struct {
		action          string
		live            bool
		wantReplacement bool
		wantUpgrade     bool
	}{
		{action: "install", live: false},
		{action: "reinstall", live: false},
		{action: "update", live: false},
		{action: "install", live: true},
		{action: "reinstall", live: true, wantReplacement: true},
		{action: "update", live: true, wantReplacement: true, wantUpgrade: true},
	} {
		t.Run(fmt.Sprintf("%s/live=%t", test.action, test.live), func(t *testing.T) {
			item := action(t, "cat/pkg-1")
			item.Action = test.action
			got := actionRebuildConfig(rebuild.RebuildConfig{AllowLiveRoot: test.live}, item)
			if got.AllowLiveReplacement != test.wantReplacement || got.AllowLiveUpgrade != test.wantUpgrade {
				t.Fatalf("replacement=%t upgrade=%t, want %t/%t", got.AllowLiveReplacement, got.AllowLiveUpgrade, test.wantReplacement, test.wantUpgrade)
			}
		})
	}
}

func TestActionLabelContract(t *testing.T) {
	if got := actionLabel(resolve.PkgAction{}); got != "<nil>" {
		t.Fatalf("nil action label = %q", got)
	}
	a, err := atom.Parse("cat/pkg")
	if err != nil {
		t.Fatal(err)
	}
	if got := actionLabel(resolve.PkgAction{Atom: a}); got != "cat/pkg" {
		t.Fatalf("unversioned action label = %q", got)
	}
	if got := actionLabel(action(t, "cat/pkg-1-r2")); got != "cat/pkg-1-r2" {
		t.Fatalf("versioned action label = %q", got)
	}
}

func TestTmpdirRequiredBytesMatchesEmergeDecay(t *testing.T) {
	const gib = uint64(1 << 30)
	if got, want := tmpdirRequiredBytes(18, 2), 18*gib+9*gib+6*gib; got != want {
		t.Fatalf("required=%d want=%d", got, want)
	}
}

func TestAdmitTmpdirJobReducesParallelism(t *testing.T) {
	var waited bool
	cfg := Config{
		Rebuild:             rebuild.RebuildConfig{WorkDirBase: "/work"},
		TmpdirRequireFreeGB: 18,
		FreeSpace:           func(string) (uint64, error) { return 20 << 30, nil },
		OnSpaceWait:         func(string, uint64, uint64) { waited = true },
	}
	admitted, err := admitTmpdirJob(cfg, 1)
	if err != nil || admitted || !waited {
		t.Fatalf("admitted=%v waited=%v err=%v", admitted, waited, err)
	}
	admitted, err = admitTmpdirJob(cfg, 0)
	if err != nil || !admitted {
		t.Fatalf("serial forward progress admitted=%v err=%v", admitted, err)
	}
}

func TestAdmitTmpdirJobRejectsFullFilesystem(t *testing.T) {
	cfg := Config{
		Rebuild:   rebuild.RebuildConfig{WorkDirBase: "/work"},
		FreeSpace: func(string) (uint64, error) { return 0, nil },
	}
	if admitted, err := admitTmpdirJob(cfg, 0); admitted || err == nil || !strings.Contains(err.Error(), "no free space") {
		t.Fatalf("admitted=%v err=%v", admitted, err)
	}
}

func TestExecuteMarksPostCommitLifecycleFailureComplete(t *testing.T) {
	item := action(t, "cat/pkg-1")
	result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{item}}
	root, resume := filepath.Join(t.TempDir(), "root"), filepath.Join(t.TempDir(), "resume.json")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	err := Execute(context.Background(), result, Config{
		ResumePath: resume, Rebuild: rebuild.RebuildConfig{RootDir: root},
		Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
		Runner: func(context.Context, string, *rebuild.RebuildConfig) error {
			return &merge.PostCommitError{Err: fmt.Errorf("postinst failed")}
		},
	})
	if err != nil {
		t.Fatalf("committed lifecycle failure stopped execution: %v", err)
	}
	remaining, loadErr := resolve.LoadResume(resume)
	if loadErr != nil || len(remaining) != 0 {
		t.Fatalf("remaining=%v err=%v", remaining, loadErr)
	}
}

func TestExecuteConcurrentContinuesAfterPostCommitLifecycleFailure(t *testing.T) {
	first, second := action(t, "cat/first-1"), action(t, "cat/second-1")
	result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{first, second}}
	root := filepath.Join(t.TempDir(), "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var ran []string
	err := Execute(context.Background(), result, Config{
		Jobs: 2, Rebuild: rebuild.RebuildConfig{RootDir: root},
		Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
		Runner: func(_ context.Context, label string, cfg *rebuild.RebuildConfig) error {
			mu.Lock()
			ran = append(ran, label)
			mu.Unlock()
			if cfg.OnTransactionCommit != nil {
				if err := cfg.OnTransactionCommit(nil); err != nil {
					return err
				}
			}
			if strings.Contains(label, "first") {
				return &merge.PostCommitError{Err: fmt.Errorf("postinst failed")}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("committed lifecycle failure stopped parallel execution: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 2 {
		t.Fatalf("ran=%v, want both package jobs", ran)
	}
}

func TestExecutePreflightsWholePlanBeforeFirstRunner(t *testing.T) {
	result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{action(t, "cat/first-1"), action(t, "cat/second-1")}}
	root := filepath.Join(t.TempDir(), "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var preflighted, ran []string
	err := Execute(context.Background(), result, Config{Rebuild: rebuild.RebuildConfig{RootDir: root},
		Preflight: func(a resolve.PkgAction, _ *rebuild.RebuildConfig) error {
			label := actionLabel(a)
			preflighted = append(preflighted, label)
			if len(preflighted) == 2 {
				return fmt.Errorf("unsupported helper")
			}
			return nil
		},
		Runner: func(_ context.Context, label string, _ *rebuild.RebuildConfig) error {
			ran = append(ran, label)
			return nil
		},
	})
	if err == nil || len(ran) != 0 || !reflect.DeepEqual(preflighted, []string{"cat/first-1", "cat/second-1"}) {
		t.Fatalf("err=%v preflighted=%v ran=%v", err, preflighted, ran)
	}
}

func TestExecuteSeriallyRunsVerifiedDisposablePlan(t *testing.T) {
	result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{action(t, "cat/first-1"), action(t, "cat/second-1")}}
	root := filepath.Join(t.TempDir(), "root")
	resume := filepath.Join(t.TempDir(), "resume.json")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var ran []string
	var events []string
	err := Execute(context.Background(), result, Config{ResumePath: resume, Rebuild: rebuild.RebuildConfig{RootDir: root},
		Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
		OnActionStart: func(index, total int, action resolve.PkgAction) {
			events = append(events, fmt.Sprintf("start:%d/%d:%s", index, total, actionLabel(action)))
		},
		OnActionInstall: func(index, total int, action resolve.PkgAction) {
			events = append(events, fmt.Sprintf("install:%d/%d:%s", index, total, actionLabel(action)))
		},
		OnActionComplete: func(index, total int, action resolve.PkgAction) {
			events = append(events, fmt.Sprintf("complete:%d/%d:%s", index, total, actionLabel(action)))
		},
		Runner: func(_ context.Context, label string, cfg *rebuild.RebuildConfig) error {
			ran = append(ran, label)
			cfg.OnPhaseStart("src_install")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ran, []string{"cat/first-1", "cat/second-1"}) {
		t.Fatalf("run order=%v", ran)
	}
	wantEvents := []string{"start:1/2:cat/first-1", "install:1/2:cat/first-1", "complete:1/2:cat/first-1", "start:2/2:cat/second-1", "install:2/2:cat/second-1", "complete:2/2:cat/second-1"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events=%v want=%v", events, wantEvents)
	}
	remaining, err := resolve.LoadResume(resume)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("remaining=%v err=%v", remaining, err)
	}
}

func TestExecuteResumeMatrixAdvancesOnlyAfterCommittedRunner(t *testing.T) {
	for _, failureStage := range []string{"fetch", "build", "lifecycle", "merge"} {
		t.Run(failureStage, func(t *testing.T) {
			actions := []resolve.PkgAction{action(t, "cat/first-1"), action(t, "cat/failing-1"), action(t, "cat/last-1")}
			result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: actions}
			root, resume := filepath.Join(t.TempDir(), "root"), filepath.Join(t.TempDir(), "resume.json")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			var firstRun []string
			err := Execute(context.Background(), result, Config{
				ResumePath: resume, Rebuild: rebuild.RebuildConfig{RootDir: root},
				Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
				Runner: func(_ context.Context, label string, _ *rebuild.RebuildConfig) error {
					firstRun = append(firstRun, label)
					if label == "cat/failing-1" {
						return fmt.Errorf("injected %s failure", failureStage)
					}
					return nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), "injected "+failureStage+" failure") {
				t.Fatalf("failure = %v", err)
			}
			if !reflect.DeepEqual(firstRun, []string{"cat/first-1", "cat/failing-1"}) {
				t.Fatalf("first run = %v", firstRun)
			}
			remaining, err := resolve.LoadResume(resume)
			if err != nil {
				t.Fatal(err)
			}
			wantRemaining := []string{actions[1].Atom.String(), actions[2].Atom.String()}
			if !reflect.DeepEqual(remaining, wantRemaining) {
				t.Fatalf("remaining after %s failure = %v, want %v", failureStage, remaining, wantRemaining)
			}

			var retried []string
			retry := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: actions[1:]}
			if err := Execute(context.Background(), retry, Config{
				ResumePath: resume, Rebuild: rebuild.RebuildConfig{RootDir: root},
				Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
				Runner: func(_ context.Context, label string, _ *rebuild.RebuildConfig) error {
					retried = append(retried, label)
					return nil
				},
			}); err != nil {
				t.Fatalf("retry after %s failure: %v", failureStage, err)
			}
			if !reflect.DeepEqual(retried, []string{"cat/failing-1", "cat/last-1"}) {
				t.Fatalf("retry after %s = %v", failureStage, retried)
			}
			remaining, err = resolve.LoadResume(resume)
			if err != nil || len(remaining) != 0 {
				t.Fatalf("remaining after retry = %v, %v", remaining, err)
			}
		})
	}
}

func TestExecuteRejectsLiveRootAndUnverifiedPlan(t *testing.T) {
	result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified}
	if err := Execute(context.Background(), result, Config{Rebuild: rebuild.RebuildConfig{RootDir: "/"}}); err == nil {
		t.Fatal("live ROOT accepted")
	}
	root := filepath.Join(t.TempDir(), "root")
	result.Verified = false
	if err := Execute(context.Background(), result, Config{Rebuild: rebuild.RebuildConfig{RootDir: root}}); err == nil {
		t.Fatal("unverified plan accepted")
	}
}

func TestExecutePromotesExplicitLivePlansWithDurableResume(t *testing.T) {
	result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{action(t, "cat/pkg-1")}}
	ran := false
	vdbDir := filepath.Join(t.TempDir(), "var", "db", "pkg")
	err := Execute(context.Background(), result, Config{Rebuild: rebuild.RebuildConfig{RootDir: "/", VdbDir: vdbDir, AllowLiveRoot: true},
		Preflight:      func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
		Runner:         func(context.Context, string, *rebuild.RebuildConfig) error { ran = true; return nil },
		ValidateLocked: func() error { return nil },
	})
	if err != nil || !ran {
		t.Fatalf("single live canary err=%v ran=%t", err, ran)
	}
	result.Install = append(result.Install, action(t, "cat/other-1"))
	var order []string
	err = Execute(context.Background(), result, Config{Rebuild: rebuild.RebuildConfig{RootDir: "/", VdbDir: vdbDir, AllowLiveRoot: true},
		Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
		Runner: func(_ context.Context, label string, _ *rebuild.RebuildConfig) error {
			order = append(order, label)
			return nil
		},
		ValidateLocked: func() error { return nil },
	})
	if err != nil || !reflect.DeepEqual(order, []string{"cat/pkg-1", "cat/other-1"}) {
		t.Fatalf("two-action live canary err=%v order=%v", err, order)
	}
	result.Install = append(result.Install, action(t, "cat/third-1"))
	if err := Execute(context.Background(), result, Config{Rebuild: rebuild.RebuildConfig{RootDir: "/", VdbDir: vdbDir, AllowLiveRoot: true}}); err == nil {
		t.Fatal("large live plan without resume state accepted")
	}
	resume := filepath.Join(t.TempDir(), "resume.json")
	order = nil
	err = Execute(context.Background(), result, Config{ResumePath: resume, Rebuild: rebuild.RebuildConfig{RootDir: "/", VdbDir: vdbDir, AllowLiveRoot: true},
		Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
		Runner: func(_ context.Context, label string, _ *rebuild.RebuildConfig) error {
			order = append(order, label)
			return nil
		},
		ValidateLocked: func() error { return nil },
	})
	if err != nil || !reflect.DeepEqual(order, []string{"cat/pkg-1", "cat/other-1", "cat/third-1"}) {
		t.Fatalf("resumable live plan err=%v order=%v", err, order)
	}
}

func TestExecuteValidatesEveryLiveActionKind(t *testing.T) {
	result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{
		action(t, "cat/first-1"), action(t, "cat/second-1"), action(t, "cat/third-1"),
	}}
	result.Install[2].Action = "remove"
	err := Execute(context.Background(), result, Config{
		ResumePath: filepath.Join(t.TempDir(), "resume.json"),
		Rebuild:    rebuild.RebuildConfig{RootDir: "/", VdbDir: filepath.Join(t.TempDir(), "vdb"), AllowLiveRoot: true},
	})
	if err == nil || !strings.Contains(err.Error(), "cat/third-1") {
		t.Fatalf("unsupported tail action error = %v", err)
	}
}

func TestExecuteConcurrentRunsReadyActionsAndSerializesCommit(t *testing.T) {
	first, second, dependent := action(t, "cat/first-1"), action(t, "cat/second-1"), action(t, "cat/dependent-1")
	dependent.Prerequisites = []string{resolve.ActionIdentity(first)}
	result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{first, second, dependent}}
	root, resume := filepath.Join(t.TempDir(), "root"), filepath.Join(t.TempDir(), "resume.json")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 3)
	release := make(chan struct{})
	var firstCommitted atomic.Bool
	var commitActive, commitMaximum atomic.Int32
	var releaseOnce sync.Once
	runner := func(ctx context.Context, label string, cfg *rebuild.RebuildConfig) error {
		started <- label
		if label == "cat/dependent-1" && !firstCommitted.Load() {
			return fmt.Errorf("dependent started before prerequisite commit")
		}
		if label != "cat/dependent-1" {
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		cfg.CommitLock.Lock()
		current := commitActive.Add(1)
		if current > commitMaximum.Load() {
			commitMaximum.Store(current)
		}
		time.Sleep(5 * time.Millisecond)
		if label == "cat/first-1" {
			firstCommitted.Store(true)
		}
		err := cfg.OnTransactionCommit(nil)
		commitActive.Add(-1)
		cfg.CommitLock.Unlock()
		return err
	}
	done := make(chan error, 1)
	go func() {
		done <- Execute(context.Background(), result, Config{
			Jobs: 2, ResumePath: resume, Rebuild: rebuild.RebuildConfig{RootDir: root},
			Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil }, Runner: runner,
		})
	}()
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case label := <-started:
			seen[label] = true
		case <-time.After(time.Second):
			t.Fatal("independent actions did not start concurrently")
		}
	}
	if !seen["cat/first-1"] || !seen["cat/second-1"] {
		t.Fatalf("initial ready actions = %v", seen)
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if commitMaximum.Load() != 1 {
		t.Fatalf("maximum concurrent commits = %d, want 1", commitMaximum.Load())
	}
	remaining, err := resolve.LoadResume(resume)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("remaining=%v err=%v", remaining, err)
	}
}

func TestExecuteConcurrentRejectsMissingPrerequisite(t *testing.T) {
	first, item := action(t, "cat/first-1"), action(t, "cat/pkg-1")
	item.Prerequisites = []string{"ROOT|cat/missing-1|0||test"}
	result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{first, item}}
	err := Execute(context.Background(), result, Config{Jobs: 2, Rebuild: rebuild.RebuildConfig{RootDir: t.TempDir()},
		Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil }})
	if err == nil || !strings.Contains(err.Error(), "missing prerequisite") {
		t.Fatalf("missing prerequisite error = %v", err)
	}
}

func TestExecuteConcurrentRequiresCommitProofBeforeSuccess(t *testing.T) {
	first, second := action(t, "cat/first-1"), action(t, "cat/second-1")
	result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{first, second}}
	resume := filepath.Join(t.TempDir(), "resume.json")
	var completed atomic.Int32
	err := Execute(context.Background(), result, Config{
		Jobs: 2, ResumePath: resume, Rebuild: rebuild.RebuildConfig{RootDir: t.TempDir()},
		Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
		Runner: func(context.Context, string, *rebuild.RebuildConfig) error {
			return nil
		},
		OnActionComplete: func(int, int, resolve.PkgAction) {
			completed.Add(1)
		},
	})
	if err == nil || !strings.Contains(err.Error(), "without transaction commit notification") {
		t.Fatalf("missing commit proof error = %v", err)
	}
	if completed.Load() != 0 {
		t.Fatalf("completed callbacks = %d without commit proof", completed.Load())
	}
	remaining, loadErr := resolve.LoadResume(resume)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	want := []string{first.Atom.String(), second.Atom.String()}
	if !reflect.DeepEqual(remaining, want) {
		t.Fatalf("resume advanced without commit proof: got %v, want %v", remaining, want)
	}
}

func TestExecuteConcurrentPostCommitErrorRequiresCommitProof(t *testing.T) {
	first, second := action(t, "cat/first-1"), action(t, "cat/second-1")
	result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{first, second}}
	var notices atomic.Int32
	err := Execute(context.Background(), result, Config{
		Jobs: 2, Rebuild: rebuild.RebuildConfig{RootDir: t.TempDir()},
		Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
		Runner: func(context.Context, string, *rebuild.RebuildConfig) error {
			return &merge.PostCommitError{Err: fmt.Errorf("claimed lifecycle failure")}
		},
		OnActionNotice: func(int, int, resolve.PkgAction, string, string) {
			notices.Add(1)
		},
	})
	if err == nil || !strings.Contains(err.Error(), "claimed lifecycle failure") {
		t.Fatalf("unproved post-commit error = %v", err)
	}
	if notices.Load() != 0 {
		t.Fatalf("unproved post-commit failure emitted %d committed notices", notices.Load())
	}
}

func TestExecuteConcurrentRejectsDuplicateCommitNotificationExactlyOnce(t *testing.T) {
	first, second := action(t, "cat/first-1"), action(t, "cat/second-1")
	result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{first, second}}
	resume := filepath.Join(t.TempDir(), "resume.json")
	var completed atomic.Int32
	err := Execute(context.Background(), result, Config{
		Jobs: 2, ResumePath: resume, Rebuild: rebuild.RebuildConfig{RootDir: t.TempDir()},
		Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
		Runner: func(ctx context.Context, label string, cfg *rebuild.RebuildConfig) error {
			if label == "cat/second-1" {
				<-ctx.Done()
				return ctx.Err()
			}
			if err := cfg.OnTransactionCommit(nil); err != nil {
				return err
			}
			return cfg.OnTransactionCommit(nil)
		},
		OnActionComplete: func(int, int, resolve.PkgAction) {
			completed.Add(1)
		},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate transaction commit notification") {
		t.Fatalf("duplicate commit error = %v", err)
	}
	if completed.Load() != 1 {
		t.Fatalf("completion callback count = %d, want exactly 1", completed.Load())
	}
	remaining, loadErr := resolve.LoadResume(resume)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if !reflect.DeepEqual(remaining, []string{second.Atom.String()}) {
		t.Fatalf("remaining after duplicate notification = %v", remaining)
	}
}

func TestExecuteConcurrentRejectsMalformedPrerequisiteGraphBeforeMutation(t *testing.T) {
	tests := []struct {
		name    string
		actions func(*testing.T) []resolve.PkgAction
		want    string
	}{
		{
			name: "duplicate identity",
			actions: func(t *testing.T) []resolve.PkgAction {
				return []resolve.PkgAction{action(t, "cat/same-1"), action(t, "cat/same-1")}
			},
			want: "duplicate planned action identity",
		},
		{
			name: "duplicate prerequisite",
			actions: func(t *testing.T) []resolve.PkgAction {
				first, second := action(t, "cat/first-1"), action(t, "cat/second-1")
				identity := resolve.ActionIdentity(first)
				second.Prerequisites = []string{identity, identity}
				return []resolve.PkgAction{first, second}
			},
			want: "repeats prerequisite",
		},
		{
			name: "cycle after independent action",
			actions: func(t *testing.T) []resolve.PkgAction {
				independent := action(t, "cat/independent-1")
				first, second := action(t, "cat/first-1"), action(t, "cat/second-1")
				first.Prerequisites = []string{resolve.ActionIdentity(second)}
				second.Prerequisites = []string{resolve.ActionIdentity(first)}
				return []resolve.PkgAction{independent, first, second}
			},
			want: "graph stalled after 1 of 3 actions",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actions := test.actions(t)
			result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: actions}
			var ran, committed atomic.Int32
			err := Execute(context.Background(), result, Config{
				Jobs: 2, Rebuild: rebuild.RebuildConfig{RootDir: t.TempDir()},
				Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
				Runner: func(_ context.Context, _ string, cfg *rebuild.RebuildConfig) error {
					ran.Add(1)
					if err := cfg.OnTransactionCommit(nil); err != nil {
						return err
					}
					committed.Add(1)
					return nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Execute error = %v, want substring %q", err, test.want)
			}
			switch test.name {
			case "cycle after independent action":
				if ran.Load() != 1 || committed.Load() != 1 {
					t.Fatalf("independent work ran=%d committed=%d, want 1/1", ran.Load(), committed.Load())
				}
			default:
				if ran.Load() != 0 || committed.Load() != 0 {
					t.Fatalf("malformed graph mutated runner state: ran=%d committed=%d", ran.Load(), committed.Load())
				}
			}
		})
	}
}

func TestExecuteConcurrentFailureCancelsPeersAndDoesNotReleaseDependent(t *testing.T) {
	failing, peer, dependent := action(t, "cat/failing-1"), action(t, "cat/peer-1"), action(t, "cat/dependent-1")
	dependent.Prerequisites = []string{resolve.ActionIdentity(failing)}
	result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{failing, peer, dependent}}
	root, resume := filepath.Join(t.TempDir(), "root"), filepath.Join(t.TempDir(), "resume.json")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var dependentStarted atomic.Bool
	peerCanceled := make(chan struct{})
	err := Execute(context.Background(), result, Config{
		Jobs: 2, ResumePath: resume, Rebuild: rebuild.RebuildConfig{RootDir: root},
		Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
		Runner: func(ctx context.Context, label string, _ *rebuild.RebuildConfig) error {
			switch label {
			case "cat/failing-1":
				return fmt.Errorf("injected build failure")
			case "cat/peer-1":
				<-ctx.Done()
				close(peerCanceled)
				return ctx.Err()
			case "cat/dependent-1":
				dependentStarted.Store(true)
			}
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "injected build failure") {
		t.Fatalf("failure = %v", err)
	}
	select {
	case <-peerCanceled:
	default:
		t.Fatal("running independent peer was not canceled and joined")
	}
	if dependentStarted.Load() {
		t.Fatal("dependent started after prerequisite failure")
	}
	remaining, loadErr := resolve.LoadResume(resume)
	if loadErr != nil || len(remaining) != 3 {
		t.Fatalf("remaining=%v err=%v", remaining, loadErr)
	}
}

func TestExecuteConcurrentSiblingFailureDoesNotCancelPostCommitContext(t *testing.T) {
	failing, committing := action(t, "cat/failing-1"), action(t, "cat/committing-1")
	result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{failing, committing}}
	committingStarted := make(chan struct{})
	peerCanceled := make(chan struct{})
	err := Execute(context.Background(), result, Config{
		Jobs: 2, Rebuild: rebuild.RebuildConfig{RootDir: t.TempDir()},
		Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
		Runner: func(ctx context.Context, label string, cfg *rebuild.RebuildConfig) error {
			if label == "cat/failing-1" {
				<-committingStarted
				return fmt.Errorf("injected sibling failure")
			}
			close(committingStarted)
			<-ctx.Done()
			select {
			case <-cfg.PostCommitContext.Done():
				return fmt.Errorf("post-commit context canceled with sibling")
			default:
				close(peerCanceled)
				return ctx.Err()
			}
		},
	})
	if err == nil || !strings.Contains(err.Error(), "injected sibling failure") {
		t.Fatalf("failure = %v", err)
	}
	select {
	case <-peerCanceled:
	default:
		t.Fatal("peer did not observe build-context cancellation")
	}
}

func TestExecuteConcurrentRealBuildsOverlapAndCommitValidTransactions(t *testing.T) {
	if _, err := exec.LookPath("sandbox"); err != nil {
		t.Skip("Portage sandbox is not installed")
	}
	base := t.TempDir()
	repository := filepath.Join(base, "repo")
	root := filepath.Join(base, "root")
	vdb := filepath.Join(root, "var", "db", "pkg")
	work := filepath.Join(base, "work")
	logs := filepath.Join(base, "logs")
	journals := filepath.Join(base, "journals")
	for _, directory := range []string{filepath.Join(repository, "eclass"), root, vdb, work, logs, journals} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var actions []resolve.PkgAction
	for _, name := range []string{"first", "second"} {
		packageDir := filepath.Join(repository, "app-misc", name)
		if err := os.MkdirAll(packageDir, 0o755); err != nil {
			t.Fatal(err)
		}
		ebuild := "EAPI=8\nS=\"${WORKDIR}/${P}\"\n" +
			"src_unpack() { mkdir -p \"${S}\"; }\n" +
			"src_compile() { sleep 0.15; }\n" +
			"src_install() { insinto /usr/share/" + name + "; printf '%s\\n' " + name + " > \"${T}/payload\"; doins \"${T}/payload\"; }\n"
		if err := os.WriteFile(filepath.Join(packageDir, name+"-1.ebuild"), []byte(ebuild), 0o644); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, resolve.PkgAction{
			Atom: action(t, "app-misc/"+name+"-1").Atom, Action: "install", Slot: "0", Domain: resolve.DomainROOT,
			Repository: "test", RepositoryPath: repository, MergeType: "source",
		})
	}
	var compileActive, compileMaximum atomic.Int32
	result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: actions}
	resume := filepath.Join(base, "resume.json")
	err := Execute(context.Background(), result, Config{
		Jobs: 2, ResumePath: resume,
		Rebuild: rebuild.RebuildConfig{
			RootDir: root, VdbDir: vdb, WorkDirBase: work, PhaseLogDir: logs, JournalDir: journals,
			PhaseProtocol: true, Repository: "test", Repositories: []portage.RepoEntry{{Name: "test", Location: repository}},
			OnPhaseStart: func(phase string) {
				if phase == "src_compile" {
					current := compileActive.Add(1)
					for current > compileMaximum.Load() && !compileMaximum.CompareAndSwap(compileMaximum.Load(), current) {
					}
				}
			},
			OnPhaseEnd: func(phase string, _ error) {
				if phase == "src_compile" {
					compileActive.Add(-1)
				}
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if compileMaximum.Load() < 2 {
		t.Fatalf("maximum concurrent compile phases = %d, want 2", compileMaximum.Load())
	}
	for _, name := range []string{"first", "second"} {
		if _, err := os.Stat(filepath.Join(root, "usr", "share", name, "payload")); err != nil {
			t.Fatalf("committed payload %s: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(vdb, "app-misc", name+"-1")); err != nil {
			t.Fatalf("committed VDB %s: %v", name, err)
		}
	}
	summaries, err := journal.List(journals)
	if err != nil || len(summaries) != 2 {
		t.Fatalf("journals=%v err=%v", summaries, err)
	}
	for _, summary := range summaries {
		if summary.Status != "committed" || summary.Entries == 0 {
			t.Fatalf("journal = %#v", summary)
		}
	}
}
