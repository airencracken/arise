package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/airencracken/arise/internal/atom"
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
	err := Execute(context.Background(), result, Config{ResumePath: resume, Rebuild: rebuild.RebuildConfig{RootDir: root},
		Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
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
	remaining, err := resolve.LoadResume(resume)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("remaining=%v err=%v", remaining, err)
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

func TestExecuteAllowsOnlyExplicitSingleActionLiveCanary(t *testing.T) {
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
	if err := Execute(context.Background(), result, Config{Rebuild: rebuild.RebuildConfig{RootDir: "/", VdbDir: vdbDir, AllowLiveRoot: true}}); err == nil {
		t.Fatal("multi-action live plan accepted")
	}
}
