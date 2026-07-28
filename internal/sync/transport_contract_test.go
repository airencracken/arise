package sync

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestSyncUsesBuiltInGitBeforeSystemGit(t *testing.T) {
	remote := initSyncRemote(t)
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	repoURL := startTestGitDaemon(t, gitPath, remote)
	target := filepath.Join(t.TempDir(), "target")
	marker := filepath.Join(t.TempDir(), "system-git-invoked")
	fakeBin := t.TempDir()
	fakeGit := filepath.Join(fakeBin, "git")
	script := "#!/bin/sh\n: >\"" + marker + "\"\nexit 97\n"
	if err := os.WriteFile(fakeGit, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)

	if err := Sync(context.Background(), SyncConfig{
		RepoURL: repoURL, TargetDir: target, SyncType: "git", Depth: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("system Git was invoked by successful built-in clone: %v", err)
	}
	if err := Sync(context.Background(), SyncConfig{
		RepoURL: repoURL, TargetDir: target, SyncType: "git", Depth: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("system Git was invoked by successful built-in update: %v", err)
	}
}

func TestSyncInvokesSystemGitOnlyAfterBuiltInFailure(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "system-git-invoked")
	fakeBin := t.TempDir()
	fakeGit := filepath.Join(fakeBin, "git")
	script := "#!/bin/sh\n: >\"" + marker + "\"\nexit 96\n"
	if err := os.WriteFile(fakeGit, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)

	err := Sync(context.Background(), SyncConfig{
		RepoURL:   "unsupported://example.invalid/repository",
		TargetDir: filepath.Join(t.TempDir(), "target"),
		SyncType:  "git",
	})
	if err == nil || !strings.Contains(err.Error(), "system Git fallback failed") {
		t.Fatalf("Sync() error = %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("system Git fallback was not invoked: %v", err)
	}
}

func TestSystemGitFallbackPublishesCloneAtomically(t *testing.T) {
	fakeBin := t.TempDir()
	fakeGit := filepath.Join(fakeBin, "git")
	script := `#!/bin/sh
target=
for argument in "$@"; do
	target=$argument
done
/bin/mkdir -p "$target/.git" || exit 91
exit 0
`
	if err := os.WriteFile(fakeGit, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := Sync(context.Background(), SyncConfig{
		RepoURL: "unsupported://example.invalid/repository", TargetDir: target, SyncType: "git",
	}); err != nil {
		t.Fatal(err)
	}
	if !isGitRepo(target) {
		t.Fatal("successful system fallback did not publish a Git repository")
	}
	assertNoCloneStagingDirectories(t, parent, filepath.Base(target))
}

func TestFailedCloneLeavesTargetAndParentClean(t *testing.T) {
	fakeBin := t.TempDir()
	fakeGit := filepath.Join(fakeBin, "git")
	if err := os.WriteFile(fakeGit, []byte("#!/bin/sh\nexit 92\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	err := Sync(context.Background(), SyncConfig{
		RepoURL: "unsupported://example.invalid/repository", TargetDir: target, SyncType: "git",
	})
	if err == nil {
		t.Fatal("failed clone returned nil")
	}
	if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
		t.Fatalf("failed clone published target: %v", statErr)
	}
	assertNoCloneStagingDirectories(t, parent, filepath.Base(target))
}

func TestCloneRefusesAndPreservesExistingNonRepositoryTarget(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(target, "keep")
	if err := os.WriteFile(sentinel, []byte("preserve\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "system-git-invoked")
	fakeBin := t.TempDir()
	fakeGit := filepath.Join(fakeBin, "git")
	script := "#!/bin/sh\n: >\"" + marker + "\"\nexit 93\n"
	if err := os.WriteFile(fakeGit, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)
	err := Sync(context.Background(), SyncConfig{
		RepoURL: "unsupported://example.invalid/repository", TargetDir: target, SyncType: "git",
	})
	if err == nil || !strings.Contains(err.Error(), "clone target already exists") {
		t.Fatalf("Sync() error = %v", err)
	}
	data, readErr := os.ReadFile(sentinel)
	if readErr != nil || string(data) != "preserve\n" {
		t.Fatalf("existing target changed: data=%q err=%v", data, readErr)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("system fallback ran for clone-target policy failure: %v", statErr)
	}
	assertNoCloneStagingDirectories(t, parent, filepath.Base(target))
}

func TestAtomicityCorruptRepositoryFailurePreservesWorkingTree(t *testing.T) {
	remote := initSyncRemote(t)
	target := filepath.Join(t.TempDir(), "target")
	if err := cloneGitRepo(context.Background(), SyncConfig{
		RepoURL: remote, TargetDir: target, Depth: 2,
	}); err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(target, "app-misc", "modified", "modified-1.ebuild")
	before, err := os.ReadFile(tracked)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(target, ".git", "HEAD")); err != nil {
		t.Fatal(err)
	}
	fakeBin := t.TempDir()
	fakeGit := filepath.Join(fakeBin, "git")
	if err := os.WriteFile(fakeGit, []byte("#!/bin/sh\nexit 94\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)

	err = Sync(context.Background(), SyncConfig{
		RepoURL: remote, TargetDir: target, SyncType: "git", Depth: 2,
	})
	if err == nil || !strings.Contains(err.Error(), "built-in Git failed") {
		t.Fatalf("Sync() error = %v", err)
	}
	after, readErr := os.ReadFile(tracked)
	if readErr != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("corrupt-repository failure changed working tree: before=%q after=%q err=%v", before, after, readErr)
	}
}

func TestPropertyNativeChangeSummaryMatchesSystemGitNameStatus(t *testing.T) {
	repository := initSyncRemote(t)
	oldRevision := testGitOutput(t, "-C", repository, "rev-parse", "HEAD")
	if err := os.Rename(
		filepath.Join(repository, "app-misc", "old", "old-1.ebuild"),
		filepath.Join(repository, "app-misc", "old", "old-2.ebuild"),
	); err != nil {
		t.Fatal(err)
	}
	writeSyncFile(t, repository, "app-misc/modified/modified-1.ebuild", "EAPI=8\nDESCRIPTION=changed\n")
	writeSyncFile(t, repository, "app-misc/new/new-1.ebuild", "EAPI=8\n")
	writeSyncFile(t, repository, "metadata/not-an-ebuild", "ignored\n")
	commitSyncRemote(t, repository, "change matrix")
	newRevision := testGitOutput(t, "-C", repository, "rev-parse", "HEAD")

	native, err := gitEbuildChanges(context.Background(), repository, oldRevision, newRevision)
	if err != nil {
		t.Fatal(err)
	}
	system := systemGitEbuildNameStatus(t, repository, oldRevision, newRevision)
	if !reflect.DeepEqual(
		[][]string{native.Added, native.Removed, native.Modified},
		[][]string{system.Added, system.Removed, system.Modified},
	) {
		t.Fatalf("native summary = %#v, system Git summary = %#v", native, system)
	}
}

func TestSyncDoesNotBypassBuiltInSafetyPolicyWithSystemFallback(t *testing.T) {
	remote := initSyncRemote(t)
	target := filepath.Join(t.TempDir(), "target")
	if err := cloneGitRepo(context.Background(), SyncConfig{
		RepoURL: remote, TargetDir: target, Depth: 2, Output: os.Stderr,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "README"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "system-git-invoked")
	fakeBin := t.TempDir()
	fakeGit := filepath.Join(fakeBin, "git")
	script := "#!/bin/sh\n: >\"" + marker + "\"\nexit 95\n"
	if err := os.WriteFile(fakeGit, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)

	err := Sync(context.Background(), SyncConfig{
		RepoURL: remote, TargetDir: target, SyncType: "git", Depth: 2,
	})
	if !errors.Is(err, errDirtyWorktree) {
		t.Fatalf("Sync() error = %v, want dirty-worktree policy error", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("system fallback bypassed built-in safety policy: %v", err)
	}
}

func startTestGitDaemon(t testing.TB, gitPath, repository string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(repository)
	command := exec.Command(
		gitPath, "daemon", "--reuseaddr", "--export-all",
		"--base-path="+base, "--listen=127.0.0.1", fmt.Sprintf("--port=%d", port), base,
	)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	address := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(5 * time.Second)
	for {
		connection, dialErr := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("git daemon did not listen on %s: %v", address, dialErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Sprintf("git://%s/%s", address, filepath.Base(repository))
}

func assertNoCloneStagingDirectories(t testing.TB, parent, targetBase string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(parent, "."+targetBase+".clone-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("clone staging directories remain: %v", matches)
	}
}

func systemGitEbuildNameStatus(t testing.TB, repository, oldRevision, newRevision string) ChangeSummary {
	t.Helper()
	output := testGitOutput(t, "-C", repository, "diff", "--name-status", oldRevision, newRevision, "--", "*.ebuild")
	var summary ChangeSummary
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		status := fields[0][0]
		if status == 'R' && len(fields) == 3 {
			if cpv, ok := ebuildCPV(fields[1]); ok {
				summary.Removed = append(summary.Removed, cpv)
			}
			if cpv, ok := ebuildCPV(fields[2]); ok {
				summary.Added = append(summary.Added, cpv)
			}
			continue
		}
		cpv, ok := ebuildCPV(fields[len(fields)-1])
		if !ok {
			continue
		}
		switch status {
		case 'A':
			summary.Added = append(summary.Added, cpv)
		case 'D':
			summary.Removed = append(summary.Removed, cpv)
		default:
			summary.Modified = append(summary.Modified, cpv)
		}
	}
	sort.Strings(summary.Added)
	sort.Strings(summary.Removed)
	sort.Strings(summary.Modified)
	return summary
}
