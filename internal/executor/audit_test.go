package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/airencracken/arise/internal/rebuild"
	"github.com/airencracken/arise/internal/resolve"
)

func TestAuditRejectsInvalidGraphBeforeResumeOrRunner(t *testing.T) {
	for _, jobs := range []int{1, 2} {
		for _, shape := range []string{"missing", "duplicate", "cycle"} {
			t.Run(fmt.Sprintf("%d/%s", jobs, shape), func(t *testing.T) {
				first, second, third := action(t, "cat/first-1"), action(t, "cat/second-1"), action(t, "cat/third-1")
				switch shape {
				case "missing":
					second.Prerequisites = []string{"missing"}
				case "duplicate":
					second = first
				case "cycle":
					second.Prerequisites = []string{resolve.ActionIdentity(third)}
					third.Prerequisites = []string{resolve.ActionIdentity(second)}
				}
				path := filepath.Join(t.TempDir(), "resume.json")
				original := []byte("existing resume checkpoint")
				if err := os.WriteFile(path, original, 0644); err != nil {
					t.Fatal(err)
				}
				runs := 0
				err := Execute(context.Background(), &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{first, second, third}}, Config{
					Jobs: jobs, ResumePath: path, Rebuild: rebuild.RebuildConfig{RootDir: t.TempDir()}, Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
					Runner: func(_ context.Context, _ string, cfg *rebuild.RebuildConfig) error {
						runs++
						if cfg.OnTransactionCommit != nil {
							return cfg.OnTransactionCommit(nil)
						}
						return nil
					},
				})
				if err == nil || runs != 0 {
					t.Fatalf("invalid graph executed: runs=%d error=%v", runs, err)
				}
				got, readErr := os.ReadFile(path)
				if readErr != nil || string(got) != string(original) {
					t.Fatalf("resume overwritten before rejection: %q %v", got, readErr)
				}
			})
		}
	}
}

func TestAuditSerialRequiresExactlyOneCommitProof(t *testing.T) {
	for _, count := range []int{0, 2} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			err := Execute(context.Background(), &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{action(t, "cat/first-1")}}, Config{
				Rebuild: rebuild.RebuildConfig{RootDir: t.TempDir()}, Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
				Runner: func(_ context.Context, _ string, cfg *rebuild.RebuildConfig) error {
					for i := 0; i < count; i++ {
						if cfg.OnTransactionCommit != nil {
							if err := cfg.OnTransactionCommit(nil); err != nil {
								return err
							}
						}
					}
					return nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), "commit notification") {
				t.Fatalf("bad commit count accepted: %v", err)
			}
		})
	}
}

func TestAuditBuildOnlyCompletesWithoutPackageMutation(t *testing.T) {
	for _, jobs := range []int{1, 2} {
		t.Run(fmt.Sprint(jobs), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "resume.json")
			var completed atomic.Int32
			mutations := 0
			err := Execute(context.Background(), &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{action(t, "cat/first-1"), action(t, "cat/second-1")}}, Config{
				Jobs: jobs, ResumePath: path, Rebuild: rebuild.RebuildConfig{RootDir: "/", BuildOnly: true, BuildPackage: true, AllowLiveRoot: true},
				Preflight:      func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
				ValidateLocked: func() error { mutations++; return nil }, RecordLockedMutation: func() error { mutations++; return nil },
				Runner: func(_ context.Context, _ string, cfg *rebuild.RebuildConfig) error {
					if !cfg.BuildOnly {
						return fmt.Errorf("build-only flag lost")
					}
					return nil
				},
				OnActionComplete: func(int, int, resolve.PkgAction) { completed.Add(1) },
			})
			if err != nil || mutations != 0 || completed.Load() != 2 {
				t.Fatalf("build-only failed: completed=%d mutations=%d err=%v", completed.Load(), mutations, err)
			}
			left, err := resolve.LoadResume(path)
			if err != nil || len(left) != 0 {
				t.Fatalf("build-only checkpoint incomplete: %v %v", left, err)
			}
		})
	}
}

func TestAuditIgnoredCommitCallbackFailureStillStopsExecution(t *testing.T) {
	first, second := action(t, "cat/first-1"), action(t, "cat/second-1")
	second.Prerequisites = []string{resolve.ActionIdentity(first)}
	for _, jobs := range []int{1, 2} {
		t.Run(fmt.Sprint(jobs), func(t *testing.T) {
			runs := 0
			err := Execute(context.Background(), &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{first, second}}, Config{
				Jobs: jobs, Rebuild: rebuild.RebuildConfig{RootDir: t.TempDir()}, Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
				Runner: func(_ context.Context, _ string, cfg *rebuild.RebuildConfig) error {
					runs++
					_ = cfg.OnTransactionCommit(nil)
					_ = cfg.OnTransactionCommit(nil)
					return nil
				},
			})
			if err == nil || runs != 1 {
				t.Fatalf("ignored callback failure released dependent: runs=%d err=%v", runs, err)
			}
		})
	}
}

func TestAuditCanceledExecutionPreservesResumeAndSkipsPreflight(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume")
	if err := os.WriteFile(path, []byte("previous checkpoint"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Execute(ctx, &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{action(t, "cat/pkg-1")}}, Config{ResumePath: path, Rebuild: rebuild.RebuildConfig{RootDir: t.TempDir()}, Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error {
		t.Fatal("preflight after cancellation")
		return nil
	}})
	if err != context.Canceled {
		t.Fatalf("cancellation lost: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "previous checkpoint" {
		t.Fatalf("canceled execution changed resume: %q %v", got, err)
	}
}

func TestAuditBuildOnlyCannotIgnoreMutationNotification(t *testing.T) {
	completed := 0
	err := Execute(context.Background(), &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{action(t, "cat/pkg-1")}}, Config{
		Rebuild: rebuild.RebuildConfig{RootDir: t.TempDir(), BuildOnly: true, BuildPackage: true}, Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
		Runner: func(_ context.Context, _ string, cfg *rebuild.RebuildConfig) error {
			_ = cfg.OnTransactionCommit(nil)
			return nil
		}, OnActionComplete: func(int, int, resolve.PkgAction) { completed++ },
	})
	if err == nil || completed != 0 {
		t.Fatalf("build-only mutation notification accepted: completed=%d err=%v", completed, err)
	}
}
