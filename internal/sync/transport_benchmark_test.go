package sync

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func BenchmarkGitTransportClone(b *testing.B) {
	remote := initSyncRemote(b)
	repoURL := startTestGitDaemon(b, mustLookPath(b, "git"), remote)
	for _, transport := range []struct {
		name  string
		clone func(context.Context, SyncConfig) error
	}{
		{name: "builtin", clone: cloneGitRepo},
		{name: "system", clone: cloneGitRepoCommand},
	} {
		b.Run(transport.name, func(b *testing.B) {
			parent := b.TempDir()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				target := filepath.Join(parent, "clone")
				cfg := SyncConfig{RepoURL: repoURL, TargetDir: target, Depth: 2, Output: io.Discard}
				if err := transport.clone(context.Background(), cfg); err != nil {
					b.Fatal(err)
				}
				if err := os.RemoveAll(target); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkGitTransportNoopUpdate(b *testing.B) {
	benchmarkGitUpdates(b, false)
}

func BenchmarkGitTransportIncrementalUpdate(b *testing.B) {
	benchmarkGitUpdates(b, true)
}

func benchmarkGitUpdates(b *testing.B, mutate bool) {
	for _, transport := range []struct {
		name   string
		clone  func(context.Context, SyncConfig) error
		update func(context.Context, SyncConfig) error
	}{
		{name: "builtin", clone: cloneGitRepo, update: updateGitRepo},
		{name: "system", clone: cloneGitRepoCommand, update: updateGitRepoCommand},
	} {
		b.Run(transport.name, func(b *testing.B) {
			remote := initSyncRemote(b)
			repoURL := startTestGitDaemon(b, mustLookPath(b, "git"), remote)
			cfg := SyncConfig{
				RepoURL: repoURL, TargetDir: filepath.Join(b.TempDir(), "clone"),
				Depth: 2, Output: io.Discard,
			}
			if err := transport.clone(context.Background(), cfg); err != nil {
				b.Fatal(err)
			}
			for index := 0; index < b.N; index++ {
				if mutate {
					b.StopTimer()
					writeSyncFile(b, remote, "README", fmt.Sprintf("iteration %d\n", index))
					commitSyncRemote(b, remote, fmt.Sprintf("update %d", index))
					b.StartTimer()
				}
				if err := transport.update(context.Background(), cfg); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func mustLookPath(tb testing.TB, name string) string {
	tb.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		tb.Fatal(err)
	}
	return path
}
