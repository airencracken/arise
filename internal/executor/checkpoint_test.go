package executor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/rebuild"
	"github.com/airencracken/arise/internal/resolve"
)

func checkpointResult(t *testing.T, cpv string) *resolve.ResolveResult {
	t.Helper()
	return &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{action(t, cpv)}}
}

func TestConcurrentExecutorsPreserveDefaultCheckpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume")
	ready, finish := make(chan struct{}), make(chan struct{})
	first := checkpointResult(t, "cat/first-1")
	root := t.TempDir()
	done := make(chan error, 1)
	go func() {
		done <- Execute(context.Background(), first, Config{
			ResumePath: path, Rebuild: rebuild.RebuildConfig{RootDir: root},
			Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
			Runner: func(_ context.Context, _ string, cfg *rebuild.RebuildConfig) error {
				close(ready)
				<-finish
				return cfg.OnTransactionCommit(nil)
			},
		})
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("first executor did not start")
	}
	err := Execute(context.Background(), checkpointResult(t, "cat/second-1"), Config{
		ResumePath: path, Rebuild: rebuild.RebuildConfig{RootDir: t.TempDir()},
		Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
		Runner: func(context.Context, string, *rebuild.RebuildConfig) error {
			return errors.New("injected second build failure")
		},
	})
	if err == nil {
		t.Error("second build failure was hidden")
	}
	remaining, readErr := resolve.LoadResume(path)
	if readErr != nil || len(remaining) != 1 || remaining[0] != "cat/first-1" {
		t.Errorf("concurrent transaction overwrote first checkpoint: %v, %v", remaining, readErr)
	}
	close(finish)
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first executor did not finish")
	}
}

func TestCheckpointLeaseCoversFinalBookkeeping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume")
	finishing, finish := make(chan struct{}), make(chan struct{})
	root := t.TempDir()
	first := checkpointResult(t, "cat/first-1")
	done := make(chan error, 1)
	go func() {
		done <- Execute(context.Background(), first, Config{
			ResumePath: path, Rebuild: rebuild.RebuildConfig{RootDir: root},
			Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
			Runner: func(_ context.Context, _ string, cfg *rebuild.RebuildConfig) error {
				return cfg.OnTransactionCommit(nil)
			},
			FinishTransaction: func() error { close(finishing); <-finish; return nil },
		})
	}()
	select {
	case <-finishing:
	case <-time.After(5 * time.Second):
		t.Fatal("first transaction did not reach bookkeeping")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		close(finish)
		<-done
		t.Fatal(err)
	}
	private := ""
	err = Execute(context.Background(), checkpointResult(t, "cat/second-1"), Config{
		ResumePath: path, OnResumePath: func(path string) { private = path }, Rebuild: rebuild.RebuildConfig{RootDir: t.TempDir()},
		Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
		Runner: func(_ context.Context, _ string, cfg *rebuild.RebuildConfig) error {
			return cfg.OnTransactionCommit(nil)
		},
	})
	if err != nil {
		t.Error(err)
	}
	if private == "" || private == path {
		t.Error("concurrent transaction did not receive its own checkpoint")
	}
	if _, err := os.Stat(private); !os.IsNotExist(err) {
		t.Errorf("completed private checkpoint was retained: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Error("finalizing transaction lost its checkpoint ownership")
	}
	runs := 0
	err = Execute(context.Background(), first, Config{
		ResumePath: path, Resume: true, Rebuild: rebuild.RebuildConfig{RootDir: t.TempDir()},
		Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
		Runner:    func(context.Context, string, *rebuild.RebuildConfig) error { runs++; return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "active transaction") || runs != 0 {
		t.Errorf("active checkpoint was resumed: runs=%d err=%v", runs, err)
	}
	close(finish)
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first transaction did not finish")
	}
}

func TestCheckpointLeaseProcess(t *testing.T) {
	path := os.Getenv("ARISE_TEST_CHECKPOINT_LEASE")
	if path == "" {
		return
	}
	release, err := tryResumeLease(path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := os.WriteFile(path+".ready", nil, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCheckpointLeaseSurvivesProcessDeath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume")
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCheckpointLeaseProcess$")
	command.Env = append(os.Environ(), "ARISE_TEST_CHECKPOINT_LEASE="+path)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(path + ".ready"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("lease subprocess did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	private, err := reserveResumeCheckpoint(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if !private.private || private.path == path {
		t.Error("cross-process lease did not isolate checkpoint")
	}
	if err := private.release(); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(private.path) // this reservation never received progress
	if _, err := reserveResumeCheckpoint(path, true); err == nil {
		t.Fatal("resumed another process's active transaction")
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	resumed, err := reserveResumeCheckpoint(path, true)
	if err != nil {
		t.Fatalf("dead transaction retained a lease: %v", err)
	}
	if err := resumed.release(); err != nil {
		t.Fatal(err)
	}
}

func TestResumeRejectsCheckpointAdvancedDuringPlanning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume")
	plan := checkpointResult(t, "cat/first-1")
	if err := resolve.SaveResume(path, plan); err != nil {
		t.Fatal(err)
	}
	_, _, digest, err := resolve.LoadResumeSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := resolve.MarkResumeComplete(path, "cat/first-1"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	runs := 0
	err = Execute(context.Background(), plan, Config{
		ResumePath: path, Resume: true, ResumeSHA256: digest, Rebuild: rebuild.RebuildConfig{RootDir: t.TempDir()},
		Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
		Runner:    func(context.Context, string, *rebuild.RebuildConfig) error { runs++; return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "checkpoint changed while planning") || runs != 0 {
		t.Fatalf("stale resume executed: runs=%d err=%v", runs, err)
	}
	finalized := false
	if err := FinalizeResumeCheckpoint(path, digest, func() error { finalized = true; return nil }); err == nil || finalized {
		t.Fatal("stale completed resume changed bookkeeping")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("stale resume replaced newer progress")
	}
}

func TestEmptyResumeCannotFinalizeActiveCheckpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume")
	if err := resolve.SaveResume(path, checkpointResult(t, "cat/first-1")); err != nil {
		t.Fatal(err)
	}
	if err := resolve.MarkResumeComplete(path, "cat/first-1"); err != nil {
		t.Fatal(err)
	}
	_, _, digest, err := resolve.LoadResumeSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := FinalizeResumeCheckpoint(path, digest, func() error {
		called := false
		err := FinalizeResumeCheckpoint(path, digest, func() error { called = true; return nil })
		if err == nil || called || !strings.Contains(err.Error(), "active transaction") {
			t.Errorf("active completed checkpoint was retired: called=%t err=%v", called, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
