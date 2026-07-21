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
	if err == nil || !strings.Contains(err.Error(), "postinst failed") {
		t.Fatalf("error=%v", err)
	}
	remaining, loadErr := resolve.LoadResume(resume)
	if loadErr != nil || len(remaining) != 0 {
		t.Fatalf("remaining=%v err=%v", remaining, loadErr)
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
		OnActionComplete: func(index, total int, action resolve.PkgAction) {
			events = append(events, fmt.Sprintf("complete:%d/%d:%s", index, total, actionLabel(action)))
		},
		Runner: func(_ context.Context, label string, _ *rebuild.RebuildConfig) error {
			ran = append(ran, label)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ran, []string{"cat/first-1", "cat/second-1"}) {
		t.Fatalf("run order=%v", ran)
	}
	wantEvents := []string{"start:1/2:cat/first-1", "complete:1/2:cat/first-1", "start:2/2:cat/second-1", "complete:2/2:cat/second-1"}
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
