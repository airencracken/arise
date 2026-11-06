package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/executor"
	"github.com/airencracken/arise/internal/oplock"
	"github.com/airencracken/arise/internal/rebuild"
	"github.com/airencracken/arise/internal/resolve"
)

// This subprocess models another installer committing under Portage's lock,
// then updating world after releasing it. Every write stays in t.TempDir.
func TestMutationGuardExternalInstallerProcess(t *testing.T) {
	root := os.Getenv("ARISE_TEST_EXTERNAL_INSTALL_ROOT")
	if root == "" {
		return
	}
	lock, err := oplock.AcquireVDB(filepath.Join(root, "vdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	writeMutationTestPackage(t, filepath.Join(root, "vdb"), "www-apps/comfyware-1", os.Getenv("ARISE_TEST_EXTERNAL_INSTALL_DEP"))
	if err := os.WriteFile(filepath.Join(root, "external-ready"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	waitForMutationTestFile(t, filepath.Join(root, "external-release"))
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "world"), []byte("media-video/ffmpeg\nwww-apps/comfyware\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func waitForMutationTestFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestMutationGuardExecutorWaitsAndRevalidatesConcurrentInstaller(t *testing.T) {
	for _, jobs := range []int{1, 2} {
		for _, conflicting := range []bool{false, true} {
			t.Run(fmt.Sprintf("jobs=%d/conflict=%t", jobs, conflicting), func(t *testing.T) {
				guard, result := newMutationTestGuard(t)
				if err := guard.validate(); err != nil {
					t.Fatal(err)
				}
				root := filepath.Dir(guard.vdbDir)
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				dependency := "dev-libs/codec"
				if conflicting {
					dependency = "<media-video/ffmpeg-8"
				}
				command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMutationGuardExternalInstallerProcess$")
				command.Env = append(os.Environ(), "ARISE_TEST_EXTERNAL_INSTALL_ROOT="+root, "ARISE_TEST_EXTERNAL_INSTALL_DEP="+dependency)
				if err := command.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
				waitForMutationTestFile(t, filepath.Join(root, "external-ready"))
				attempted := make(chan struct{})
				var attempt sync.Once
				checkpoint := filepath.Join(root, "resume")
				done := make(chan error, 1)
				go func() {
					done <- executor.Execute(ctx, result, executor.Config{
						Jobs: jobs, ResumePath: checkpoint, WorldSelections: []string{},
						Rebuild:   rebuild.RebuildConfig{RootDir: "/", VdbDir: guard.vdbDir, AllowLiveRoot: true},
						Preflight: func(resolve.PkgAction, *rebuild.RebuildConfig) error { return nil },
						AcquireMutationLock: func(context.Context, string) (func() error, error) {
							attempt.Do(func() { close(attempted) })
							lock, err := oplock.AcquireVDB(guard.vdbDir)
							if err != nil {
								return nil, err
							}
							return lock.Release, nil
						},
						ValidateLocked: guard.validate, RecordLockedMutation: guard.record,
						OnActionComplete: func(_, _ int, action resolve.PkgAction) { guard.complete(action) },
						Runner: func(ctx context.Context, _ string, cfg *rebuild.RebuildConfig) error {
							cfg.CommitLock.Lock()
							defer cfg.CommitLock.Unlock()
							release, err := cfg.BeginMutation(ctx)
							if err != nil {
								return err
							}
							defer release()
							// The image is already prepared; this is only its temporary
							// VDB commit. No host payload or metadata is touched.
							if err := os.RemoveAll(filepath.Join(guard.vdbDir, "media-video/ffmpeg-7")); err != nil {
								return err
							}
							if err := writeMutationPackage(guard.vdbDir, "media-video/ffmpeg-8", "dev-libs/codec"); err != nil {
								return err
							}
							return cfg.OnTransactionCommit(nil)
						},
					})
				}()
				select {
				case <-attempted:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				select {
				case err := <-done:
					t.Fatalf("executor did not wait for the other installer: %v", err)
				default:
				}
				if err := os.WriteFile(filepath.Join(root, "external-release"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				var executionErr error
				select {
				case executionErr = <-done:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if err := command.Wait(); err != nil {
					t.Fatalf("external installer failed: %v", err)
				}
				if conflicting {
					if executionErr == nil || !strings.Contains(executionErr.Error(), "invalidate the remaining plan") {
						t.Fatalf("conflicting install was not rejected: %v", executionErr)
					}
				} else if executionErr != nil {
					t.Fatalf("unrelated concurrent install aborted the prepared merge: %v", executionErr)
				}
				remaining, err := resolve.LoadResume(checkpoint)
				if err != nil {
					t.Fatal(err)
				}
				wantRemaining := 0
				if conflicting {
					wantRemaining = 1
				}
				if len(remaining) != wantRemaining {
					t.Fatalf("checkpoint progress = %v, want %d pending", remaining, wantRemaining)
				}
				world, err := os.ReadFile(guard.worldPath)
				if err != nil || !strings.Contains(string(world), "www-apps/comfyware") {
					t.Fatal("concurrent world update was lost")
				}
				if _, err := os.Stat(filepath.Join(guard.vdbDir, "www-apps/comfyware-1/CONTENTS")); err != nil {
					t.Fatal("concurrent installed package was lost")
				}
				ffmpeg := "media-video/ffmpeg-8/CONTENTS"
				if conflicting {
					ffmpeg = "media-video/ffmpeg-7/CONTENTS"
				}
				if _, err := os.Stat(filepath.Join(guard.vdbDir, ffmpeg)); err != nil {
					t.Fatal("installed ffmpeg differs from committed checkpoint")
				}
			})
		}
	}
}
