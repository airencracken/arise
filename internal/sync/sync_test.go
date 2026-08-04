package sync

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestRemoteURL(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	const want = "https://github.com/gentoo-mirror/gentoo"
	if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{want}}); err != nil {
		t.Fatal(err)
	}
	if got := RemoteURL(dir); got != want {
		t.Fatalf("RemoteURL() = %q, want %q", got, want)
	}
}

func TestRemoteURLMissing(t *testing.T) {
	if got := RemoteURL(t.TempDir()); got != "" {
		t.Fatalf("RemoteURL() = %q, want empty", got)
	}
}

func TestSyncConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     SyncConfig
		wantErr bool
	}{
		{
			name:    "empty config",
			cfg:     SyncConfig{},
			wantErr: true,
		},
		{
			name: "missing RepoURL",
			cfg: SyncConfig{
				TargetDir: "/tmp/gentoo",
			},
			wantErr: true,
		},
		{
			name: "missing TargetDir",
			cfg: SyncConfig{
				RepoURL: "https://github.com/gentoo/gentoo.git",
			},
			wantErr: true,
		},
		{
			name: "valid config",
			cfg: SyncConfig{
				RepoURL:   "https://github.com/gentoo/gentoo.git",
				TargetDir: "/tmp/gentoo",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestSyncRejectsUnsupportedConfiguredTransport(t *testing.T) {
	err := Sync(context.Background(), SyncConfig{
		RepoURL:   "https://example.invalid/repo",
		TargetDir: t.TempDir(),
		SyncType:  "mercurial",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported sync type") {
		t.Fatalf("Sync() error = %v", err)
	}
}

func TestSyncConfig_Validate_ErrorMessages(t *testing.T) {
	tests := []struct {
		name      string
		cfg       SyncConfig
		wantInErr string
	}{
		{
			name:      "missing RepoURL message",
			cfg:       SyncConfig{TargetDir: "/tmp/gentoo"},
			wantInErr: "RepoURL",
		},
		{
			name:      "missing TargetDir message",
			cfg:       SyncConfig{RepoURL: "https://example.com/repo.git"},
			wantInErr: "TargetDir",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("Validate() error = %v, want substring %q", err, tt.wantInErr)
			}
		})
	}
}

func TestSyncConfig_Validate_Adversarial(t *testing.T) {
	tests := []struct {
		name    string
		cfg     SyncConfig
		wantErr string
	}{
		{
			name: "whitespace-only RepoURL",
			cfg: SyncConfig{
				RepoURL:   "   ",
				TargetDir: "/tmp/gentoo",
			},
			wantErr: "RepoURL is required",
		},
		{
			name: "whitespace-only TargetDir",
			cfg: SyncConfig{
				RepoURL:   "https://example.com/repo.git",
				TargetDir: "   ",
			},
			wantErr: "TargetDir is required",
		},
		{
			name: "NUL in RepoURL",
			cfg: SyncConfig{
				RepoURL:   "https://example.com/repo\x00.git",
				TargetDir: "/tmp/gentoo",
			},
			wantErr: "RepoURL contains NUL",
		},
		{
			name: "NUL in TargetDir",
			cfg: SyncConfig{
				RepoURL:   "https://example.com/repo.git",
				TargetDir: "/tmp/gentoo\x00outside",
			},
			wantErr: "TargetDir contains NUL",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if err == nil || err.Error() != "sync: "+tt.wantErr {
				t.Fatalf("Validate() error = %v, want %q", err, "sync: "+tt.wantErr)
			}
		})
	}
}

func TestSyncConfig_Defaults(t *testing.T) {
	cfg := SyncConfig{
		RepoURL:   "https://example.com/repo.git",
		TargetDir: "/tmp/repo",
	}
	cfg.defaults()

	if cfg.Depth != 1 {
		t.Errorf("expected default Depth=1, got %d", cfg.Depth)
	}
	if cfg.RsyncPath != "rsync" {
		t.Errorf("expected default RsyncPath=rsync, got %s", cfg.RsyncPath)
	}
}

func TestSyncConfig_DefaultsPreserveCustom(t *testing.T) {
	cfg := SyncConfig{
		RepoURL:   "https://example.com/repo.git",
		TargetDir: "/tmp/repo",
		Depth:     5,
		RsyncPath: "/usr/local/bin/rsync",
	}
	cfg.defaults()

	if cfg.Depth != 5 {
		t.Errorf("expected Depth=5, got %d", cfg.Depth)
	}
	if cfg.RsyncPath != "/usr/local/bin/rsync" {
		t.Errorf("expected RsyncPath=/usr/local/bin/rsync, got %s", cfg.RsyncPath)
	}
}

func TestSyncConfig_DefaultsZeroDepth(t *testing.T) {
	cfg := SyncConfig{
		RepoURL:   "https://example.com/repo.git",
		TargetDir: "/tmp/repo",
		Depth:     0,
	}
	cfg.defaults()
	if cfg.Depth != 1 {
		t.Errorf("expected Depth=1 when set to 0, got %d", cfg.Depth)
	}
}

func TestSyncConfig_DefaultsNegativeDepth(t *testing.T) {
	cfg := SyncConfig{
		RepoURL:   "https://example.com/repo.git",
		TargetDir: "/tmp/repo",
		Depth:     -1,
	}
	cfg.defaults()
	if cfg.Depth != 1 {
		t.Errorf("expected Depth=1 when set to negative, got %d", cfg.Depth)
	}
}

func TestGitAvailable_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("GitAvailable() panicked: %v", r)
		}
	}()

	available := GitAvailable()
	t.Logf("GitAvailable() = %v", available)
}

func TestGitAvailable_ReturnsBool(t *testing.T) {
	result := GitAvailable()
	if result != true && result != false {
		t.Errorf("GitAvailable() returned non-bool: %v", result)
	}
}

func TestIsGitRepo_NonExistentPath(t *testing.T) {
	if isGitRepo("/nonexistent/path/that/does/not/exist") {
		t.Error("nonexistent path should not be detected as a git repo")
	}
}

func TestIsGitRepo_FileNotDir(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "regular-file")
	if err := os.WriteFile(filePath, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	if isGitRepo(filePath) {
		t.Error("regular file should not be detected as a git repo")
	}
}

func TestIsGitRepo_EmptyTempDir(t *testing.T) {
	dir := t.TempDir()
	if isGitRepo(dir) {
		t.Error("empty temp dir should not be detected as a git repo")
	}
}

func TestIsGitRepo_ActualGitRepo(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if repo == nil {
		t.Fatal("PlainInit returned nil repo")
	}
	if !isGitRepo(dir) {
		t.Error("actual git repo should be detected")
	}
}

func TestCloneGitRepo_Success(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := filepath.Join(t.TempDir(), "clone-target")

	srcRepo, err := git.PlainInit(srcDir, false)
	if err != nil {
		t.Fatal(err)
	}
	w, err := srcRepo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(srcDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Add("test.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Commit("initial commit", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Test User",
			Email: "test@test.com",
			When:  time.Now(),
		},
	}); err != nil {
		t.Fatal(err)
	}

	cfg := SyncConfig{
		RepoURL:   srcDir,
		TargetDir: dstDir,
		Depth:     1,
	}
	if err := cloneGitRepo(context.Background(), cfg); err != nil {
		t.Fatalf("cloneGitRepo() error = %v", err)
	}

	if !isGitRepo(dstDir) {
		t.Error("target dir should be a git repo after clone")
	}

	data, err := os.ReadFile(filepath.Join(dstDir, "test.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Errorf("cloned file content = %q, want %q", string(data), "hello world")
	}
}

func TestCloneGitRepoPublishesTraversableRoot(t *testing.T) {
	remote := initSyncRemote(t)
	parent := filepath.Join(t.TempDir(), "repos")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "fixture")
	if err := cloneGitRepo(context.Background(), SyncConfig{
		RepoURL: remote, TargetDir: target, Depth: 1,
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o055 != 0o055 {
		t.Fatalf("published repository mode = %04o, want group/other read and traverse", info.Mode().Perm())
	}
}

func TestSyncRepairsCloneStagingModeOnExistingRepository(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "repos")
	target := filepath.Join(parent, "fixture")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := repairExistingRepositoryAccess(target); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o055 != 0o055 {
		t.Fatalf("repaired repository mode = %04o", info.Mode().Perm())
	}
}

func TestSyncRepositoryIdentityFailsClosed(t *testing.T) {
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "profiles", "repo_name"), []byte("actual\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := validateRepositoryIdentity(SyncConfig{TargetDir: target, RepositoryName: "expected"})
	if err == nil || !strings.Contains(err.Error(), `identity is "actual", expected "expected"`) {
		t.Fatalf("identity mismatch error = %v", err)
	}
	if err := validateRepositoryIdentity(SyncConfig{TargetDir: target, RepositoryName: "actual"}); err != nil {
		t.Fatalf("matching identity rejected: %v", err)
	}
	if err := os.Remove(filepath.Join(target, "profiles", "repo_name")); err != nil {
		t.Fatal(err)
	}
	if err := validateRepositoryIdentity(SyncConfig{TargetDir: target, RepositoryName: "actual"}); err == nil {
		t.Fatal("missing repository identity accepted")
	}
}

func TestCloneRepositoryIdentityMismatchDoesNotPublish(t *testing.T) {
	remote := initSyncRemote(t)
	writeSyncFile(t, remote, "profiles/repo_name", "actual\n")
	commitSyncRemote(t, remote, "add repository identity")
	parent := t.TempDir()
	target := filepath.Join(parent, "expected")

	err := cloneGitRepo(context.Background(), SyncConfig{
		RepoURL:        remote,
		TargetDir:      target,
		RepositoryName: "expected",
		Depth:          1,
	})
	if err == nil || !strings.Contains(err.Error(), `identity is "actual", expected "expected"`) {
		t.Fatalf("identity mismatch error = %v", err)
	}
	if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
		t.Fatalf("identity mismatch published target: %v", statErr)
	}
	assertNoCloneStagingDirectories(t, parent, filepath.Base(target))
}

func TestCloneGitRepo_ContextCancelled(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := filepath.Join(t.TempDir(), "clone-target")

	_, err := git.PlainInit(srcDir, false)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := SyncConfig{
		RepoURL:   srcDir,
		TargetDir: dstDir,
		Depth:     1,
	}
	err = cloneGitRepo(ctx, cfg)
	if err == nil {
		t.Error("cloneGitRepo with cancelled context should return error")
	}
	if _, statErr := os.Lstat(dstDir); !os.IsNotExist(statErr) {
		t.Fatalf("cancelled clone published target: %v", statErr)
	}
	assertNoCloneStagingDirectories(t, filepath.Dir(dstDir), filepath.Base(dstDir))
}

func TestUpdateGitRepo_Success(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := filepath.Join(t.TempDir(), "update-target")

	srcRepo, err := git.PlainInit(srcDir, false)
	if err != nil {
		t.Fatal(err)
	}
	w, err := srcRepo.Worktree()
	if err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(srcDir, "file1.txt")
	if err := os.WriteFile(testFile, []byte("version 1"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Add("file1.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Commit("first commit", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com", When: time.Now()},
	}); err != nil {
		t.Fatal(err)
	}

	cfg := SyncConfig{
		RepoURL:   srcDir,
		TargetDir: dstDir,
		Depth:     1,
	}
	if err := cloneGitRepo(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	testFile2 := filepath.Join(srcDir, "file2.txt")
	if err := os.WriteFile(testFile2, []byte("version 2"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Add("file2.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Commit("second commit", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com", When: time.Now()},
	}); err != nil {
		t.Fatal(err)
	}

	cfg.TargetDir = dstDir
	if err := updateGitRepo(context.Background(), cfg); err != nil {
		t.Fatalf("updateGitRepo() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dstDir, "file2.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "version 2" {
		t.Errorf("updated file content = %q, want %q", string(data), "version 2")
	}
}

func TestUpdateGitRepo_ContextCancelled(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := filepath.Join(t.TempDir(), "update-target")

	srcRepo, err := git.PlainInit(srcDir, false)
	if err != nil {
		t.Fatal(err)
	}
	w, err := srcRepo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(srcDir, "test.txt")
	os.WriteFile(testFile, []byte("content"), 0644)
	w.Add("test.txt")
	w.Commit("commit", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com", When: time.Now()},
	})

	cloneCfg := SyncConfig{RepoURL: srcDir, TargetDir: dstDir, Depth: 1}
	if err := cloneGitRepo(context.Background(), cloneCfg); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	updateCfg := SyncConfig{RepoURL: srcDir, TargetDir: dstDir, Depth: 1}
	err = updateGitRepo(ctx, updateCfg)
	if err == nil {
		t.Error("updateGitRepo with cancelled context should return error")
	}
}

func TestSync_EndToEndClone(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := filepath.Join(t.TempDir(), "sync-target")

	srcRepo, err := git.PlainInit(srcDir, false)
	if err != nil {
		t.Fatal(err)
	}
	w, err := srcRepo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "README"), []byte("gentoo"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Add("README"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Commit("initial", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com", When: time.Now()},
	}); err != nil {
		t.Fatal(err)
	}

	cfg := SyncConfig{
		RepoURL:   srcDir,
		TargetDir: dstDir,
		Depth:     1,
	}

	if err := Sync(context.Background(), cfg); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	if !isGitRepo(dstDir) {
		t.Error("sync target should be a git repo")
	}
}

func TestSync_EndToEndUpdate(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := filepath.Join(t.TempDir(), "sync-target")

	srcRepo, err := git.PlainInit(srcDir, false)
	if err != nil {
		t.Fatal(err)
	}
	w, err := srcRepo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	w.Add("a.txt")
	w.Commit("first", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com", When: time.Now()},
	})

	cfg := SyncConfig{
		RepoURL:   srcDir,
		TargetDir: dstDir,
		Depth:     1,
	}

	if err := Sync(context.Background(), cfg); err != nil {
		t.Fatalf("initial Sync() error = %v", err)
	}

	if err := os.WriteFile(filepath.Join(srcDir, "b.txt"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}
	w.Add("b.txt")
	w.Commit("second", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com", When: time.Now()},
	})

	if err := Sync(context.Background(), cfg); err != nil {
		t.Fatalf("second Sync() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(dstDir, "b.txt")); err != nil {
		t.Error("updated file b.txt should exist after second sync")
	}
}

func TestSync_InvalidConfig(t *testing.T) {
	cfg := SyncConfig{}
	err := Sync(context.Background(), cfg)
	if err == nil {
		t.Error("Sync with empty config should return error")
	}
}

func TestSync_ContextCancelled(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := filepath.Join(t.TempDir(), "sync-target")

	srcRepo, err := git.PlainInit(srcDir, false)
	if err != nil {
		t.Fatal(err)
	}
	w, err := srcRepo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(srcDir, "x.txt"), []byte("x"), 0644)
	w.Add("x.txt")
	w.Commit("commit", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com", When: time.Now()},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := SyncConfig{
		RepoURL:   srcDir,
		TargetDir: dstDir,
		Depth:     1,
	}

	err = Sync(ctx, cfg)
	if err == nil {
		t.Error("Sync with cancelled context should return error")
	}
}

func TestSyncRsync_LocalCopy(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync not available")
	}

	srcDir := t.TempDir()
	dstDir := filepath.Join(t.TempDir(), "rsync-target")

	if err := os.WriteFile(filepath.Join(srcDir, "foo.txt"), []byte("foo"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(srcDir, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "subdir", "bar.txt"), []byte("bar"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := SyncConfig{
		RepoURL:   srcDir,
		TargetDir: dstDir,
		RsyncPath: "rsync",
	}
	if err := syncRsync(context.Background(), cfg); err != nil {
		t.Fatalf("syncRsync() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dstDir, "foo.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "foo" {
		t.Errorf("rsynced file = %q, want %q", string(data), "foo")
	}
}

func TestSyncRsyncReportsPackageVersionTransition(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync not available")
	}

	srcDir := t.TempDir()
	dstDir := t.TempDir()
	for _, fixture := range []struct {
		root        string
		relative    string
		description string
	}{
		{dstDir, "app-containers/lxc-templates/lxc-templates-3.0.4_p20240917.ebuild", "Old templates"},
		{dstDir, "app-containers/lxc-templates/lxc-templates-9999.ebuild", "Live templates"},
		{srcDir, "app-containers/lxc-templates/lxc-templates-3.0.4_p20240917.ebuild", "Old templates"},
		{srcDir, "app-containers/lxc-templates/lxc-templates-3.0.4_p20260719.ebuild", "Old style template scripts for LXC"},
		{srcDir, "app-containers/lxc-templates/lxc-templates-9999.ebuild", "Live templates"},
	} {
		writeSyncFile(t, fixture.root, fixture.relative, "EAPI=8\nDESCRIPTION=\""+fixture.description+"\"\n")
	}

	var stages []string
	var got ChangeSummary
	cfg := SyncConfig{
		RepoURL: srcDir, TargetDir: dstDir, RsyncPath: "rsync",
		Progress: func(stage, _ string) { stages = append(stages, stage) },
		Changes:  func(summary ChangeSummary) { got = summary },
	}
	if err := syncRsync(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	want := ChangeSummary{
		Added: []string{"app-containers/lxc-templates-3.0.4_p20260719"},
		Packages: []PackageChange{{
			CP:          "app-containers/lxc-templates",
			Kind:        "better",
			Before:      []string{"3.0.4_p20240917", "9999"},
			After:       []string{"3.0.4_p20240917", "3.0.4_p20260719", "9999"},
			Description: "Old style template scripts for LXC",
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rsync change summary = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(stages, []string{"rsync", "changes"}) {
		t.Fatalf("rsync stages = %v, want [rsync changes]", stages)
	}
}

func TestSyncRsync_ContextCancelled(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync not available")
	}

	srcDir := t.TempDir()
	dstDir := filepath.Join(t.TempDir(), "rsync-target")

	os.WriteFile(filepath.Join(srcDir, "x.txt"), []byte("x"), 0644)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := SyncConfig{
		RepoURL:   srcDir,
		TargetDir: dstDir,
		RsyncPath: "rsync",
	}
	err := syncRsync(ctx, cfg)
	if err == nil {
		t.Error("syncRsync with cancelled context should return error")
	}
}

func TestIsGitRepo_Adversarial(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantRepo bool
	}{
		{
			name:     "empty string",
			path:     "",
			wantRepo: false,
		},
		{
			name:     "root directory",
			path:     "/",
			wantRepo: false,
		},
		{
			name:     "dev null",
			path:     "/dev/null",
			wantRepo: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isGitRepo(tt.path); got != tt.wantRepo {
				t.Errorf("isGitRepo(%q) = %v, want %v", tt.path, got, tt.wantRepo)
			}
		})
	}
}

func TestSyncConfig_DefaultDepthApplies(t *testing.T) {
	var cfg SyncConfig
	cfg.defaults()
	if cfg.RsyncPath != "rsync" {
		t.Errorf("expected RsyncPath=rsync, got %s", cfg.RsyncPath)
	}
}

func TestCloneGitRepo_InvalidSource(t *testing.T) {
	dstDir := filepath.Join(t.TempDir(), "clone-target")

	cfg := SyncConfig{
		RepoURL:   "/nonexistent/path/to/repo",
		TargetDir: dstDir,
		Depth:     1,
	}
	err := cloneGitRepo(context.Background(), cfg)
	if err == nil {
		t.Error("cloneGitRepo with nonexistent source should return error")
	}
}

func TestUpdateGitRepo_NonRepo(t *testing.T) {
	dstDir := t.TempDir()

	cfg := SyncConfig{
		RepoURL:   "https://example.com/repo.git",
		TargetDir: dstDir,
		Depth:     1,
	}
	err := updateGitRepo(context.Background(), cfg)
	if err == nil {
		t.Error("updateGitRepo on non-repo directory should return error")
	}
}

func TestAtomicitySyncCommandRefusesDirtyWorktreeBeforeUpdate(t *testing.T) {
	remote := initSyncRemote(t)
	target := filepath.Join(t.TempDir(), "target")
	cfg := SyncConfig{RepoURL: remote, TargetDir: target, Depth: 2}
	if err := Sync(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	before := testGitOutput(t, "-C", target, "rev-parse", "HEAD")
	tracked := filepath.Join(target, "app-misc", "modified", "modified-1.ebuild")
	if err := os.WriteFile(tracked, []byte("local change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSyncFile(t, remote, "app-misc/remote/remote-1.ebuild", "EAPI=8\n")
	commitSyncRemote(t, remote, "remote update")

	var changesCalled bool
	cfg.Changes = func(ChangeSummary) { changesCalled = true }
	err := Sync(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "local changes") {
		t.Fatalf("dirty sync error = %v", err)
	}
	if changesCalled {
		t.Fatal("dirty refusal reported a successful change summary")
	}
	if got := testGitOutput(t, "-C", target, "rev-parse", "HEAD"); got != before {
		t.Fatalf("dirty refusal changed HEAD from %s to %s", before, got)
	}
	data, readErr := os.ReadFile(tracked)
	if readErr != nil || string(data) != "local change\n" {
		t.Fatalf("dirty refusal overwrote tracked file: value=%q err=%v", data, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(target, "app-misc", "remote", "remote-1.ebuild")); !os.IsNotExist(statErr) {
		t.Fatalf("dirty refusal applied remote file: %v", statErr)
	}
}

func TestSyncCommandRejectsDetachedHeadWithoutReset(t *testing.T) {
	remote := initSyncRemote(t)
	target := filepath.Join(t.TempDir(), "target")
	cfg := SyncConfig{RepoURL: remote, TargetDir: target, Depth: 2}
	if err := Sync(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	before := testGitOutput(t, "-C", target, "rev-parse", "HEAD")
	testGit(t, "-C", target, "checkout", "--detach", before)
	writeSyncFile(t, remote, "app-misc/remote/remote-1.ebuild", "EAPI=8\n")
	commitSyncRemote(t, remote, "remote update")

	err := Sync(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "detached HEAD") {
		t.Fatalf("detached sync error = %v", err)
	}
	if got := testGitOutput(t, "-C", target, "rev-parse", "HEAD"); got != before {
		t.Fatalf("detached refusal changed HEAD from %s to %s", before, got)
	}
	if branch := testGitOutput(t, "-C", target, "branch", "--show-current"); branch != "" {
		t.Fatalf("detached refusal selected branch %q", branch)
	}
}

func TestSyncCommandReportsExactEbuildChangesAndProgress(t *testing.T) {
	remote := initSyncRemote(t)
	target := filepath.Join(t.TempDir(), "target")
	cfg := SyncConfig{RepoURL: remote, TargetDir: target, Depth: 2}
	if err := Sync(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	writeSyncFile(t, remote, "app-misc/modified/modified-1.ebuild", "EAPI=8\nDESCRIPTION=changed\n")
	if err := os.Remove(filepath.Join(remote, "app-misc", "old", "old-1.ebuild")); err != nil {
		t.Fatal(err)
	}
	writeSyncFile(t, remote, "app-misc/added/added-2.ebuild", "EAPI=8\n")
	writeSyncFile(t, remote, "README", "not an ebuild\n")
	commitSyncRemote(t, remote, "package changes")

	var stages []string
	var summaries []ChangeSummary
	cfg.Progress = func(stage, _ string) { stages = append(stages, stage) }
	cfg.Changes = func(summary ChangeSummary) { summaries = append(summaries, summary) }
	if err := Sync(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stages, []string{"check", "fetch", "validate", "update", "changes"}) {
		t.Fatalf("progress stages = %v", stages)
	}
	want := ChangeSummary{
		Added:    []string{"app-misc/added-2"},
		Removed:  []string{"app-misc/old-1"},
		Modified: []string{"app-misc/modified-1"},
		Packages: []PackageChange{
			{CP: "app-misc/added", Kind: "new", After: []string{"2"}},
			{CP: "app-misc/modified", Kind: "changed", Before: []string{"1"}, After: []string{"1"}, Description: "changed"},
			{CP: "app-misc/old", Kind: "removed", Before: []string{"1"}},
		},
	}
	if !reflect.DeepEqual(summaries, []ChangeSummary{want}) {
		t.Fatalf("change summaries = %#v, want %#v", summaries, want)
	}
	for relative, content := range map[string]string{
		"app-misc/added/added-2.ebuild":       "EAPI=8\n",
		"app-misc/modified/modified-1.ebuild": "EAPI=8\nDESCRIPTION=changed\n",
	} {
		data, err := os.ReadFile(filepath.Join(target, relative))
		if err != nil || string(data) != content {
			t.Fatalf("updated %s=%q err=%v", relative, data, err)
		}
	}
	if _, err := os.Stat(filepath.Join(target, "app-misc", "old", "old-1.ebuild")); !os.IsNotExist(err) {
		t.Fatalf("removed ebuild remains: %v", err)
	}
}

func TestSyncConfigDepthDefaultsAndExplicitFullHistory(t *testing.T) {
	cfg := SyncConfig{}
	cfg.defaults()
	if cfg.CloneDepth == nil || *cfg.CloneDepth != 1 || cfg.SyncDepth == nil || *cfg.SyncDepth != 1 {
		t.Fatalf("default depths = clone %v sync %v", cfg.CloneDepth, cfg.SyncDepth)
	}
	zero, five := 0, 5
	cfg = SyncConfig{CloneDepth: &zero, SyncDepth: &five}
	cfg.defaults()
	if *cfg.CloneDepth != 0 || *cfg.SyncDepth != 5 {
		t.Fatalf("explicit depths changed = clone %d sync %d", *cfg.CloneDepth, *cfg.SyncDepth)
	}
}

func TestGitEbuildChangesReportsPackageVersionTransition(t *testing.T) {
	repository := initSyncRemote(t)
	oldRevision := testGitOutput(t, "-C", repository, "rev-parse", "HEAD")
	if err := os.Remove(filepath.Join(repository, "app-misc", "modified", "modified-1.ebuild")); err != nil {
		t.Fatal(err)
	}
	writeSyncFile(t, repository, "app-misc/modified/modified-2-r1.ebuild", "EAPI=8\nDESCRIPTION=\"Improved package\"\n")
	commitSyncRemote(t, repository, "upgrade package")
	newRevision := testGitOutput(t, "-C", repository, "rev-parse", "HEAD")

	changes, err := gitEbuildChanges(context.Background(), repository, oldRevision, newRevision)
	if err != nil {
		t.Fatal(err)
	}
	want := []PackageChange{{
		CP: "app-misc/modified", Kind: "upgrade", Before: []string{"1"}, After: []string{"2-r1"},
		Description: "Improved package",
	}}
	if !reflect.DeepEqual(changes.Packages, want) {
		t.Fatalf("package changes = %#v, want %#v", changes.Packages, want)
	}
}

func TestCompareRepositoryEbuildSnapshotsReportsBestVersionAndDescription(t *testing.T) {
	before := repositoryEbuildSnapshot{
		Files: map[string]string{
			"app-containers/lxc-templates-3.0.4_p20240917": "old",
			"app-containers/lxc-templates-9999":            "live",
		},
		Versions: map[string][]string{
			"app-containers/lxc-templates": {"3.0.4_p20240917", "9999"},
		},
		Description: map[string]string{
			"app-containers/lxc-templates-9999": "Live templates",
		},
	}
	after := repositoryEbuildSnapshot{
		Files: map[string]string{
			"app-containers/lxc-templates-3.0.4_p20240917": "old",
			"app-containers/lxc-templates-3.0.4_p20260719": "new",
			"app-containers/lxc-templates-9999":            "live",
		},
		Versions: map[string][]string{
			"app-containers/lxc-templates": {"3.0.4_p20240917", "3.0.4_p20260719", "9999"},
		},
		Description: map[string]string{
			"app-containers/lxc-templates-3.0.4_p20260719": "Old style template scripts for LXC",
		},
	}

	got := compareRepositoryEbuildSnapshots(before, after)
	want := ChangeSummary{
		Added: []string{"app-containers/lxc-templates-3.0.4_p20260719"},
		Packages: []PackageChange{{
			CP:          "app-containers/lxc-templates",
			Kind:        "better",
			Before:      []string{"3.0.4_p20240917", "9999"},
			After:       []string{"3.0.4_p20240917", "3.0.4_p20260719", "9999"},
			Description: "Old style template scripts for LXC",
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot change = %#v, want %#v", got, want)
	}
}

func TestDescriptionFromMetadataHandlesQuotedAndAdversarialValues(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "cache", content: "EAPI=8\nDESCRIPTION=Old style template scripts for LXC\n", want: "Old style template scripts for LXC"},
		{name: "double quoted", content: "DESCRIPTION=\"A package with spaces\"\n", want: "A package with spaces"},
		{name: "single quoted", content: "DESCRIPTION='literal package'\n", want: "literal package"},
		{name: "escaped", content: "DESCRIPTION=\"quoted \\\"package\\\"\"\n", want: `quoted "package"`},
		{name: "comment only", content: "# DESCRIPTION=not metadata\n", want: ""},
		{name: "similarly named", content: "LONG_DESCRIPTION=wrong\n", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := descriptionFromMetadata(test.content); got != test.want {
				t.Fatalf("description = %q, want %q", got, test.want)
			}
		})
	}
}

func TestPackageChangeKindHandlesLiveEbuildAlongsideReleaseGrowth(t *testing.T) {
	before := []string{"0.0.2", "9999"}
	after := []string{"0.0.2", "0.0.3", "9999"}
	if got, want := packageChangeKind(before, after), "better"; got != want {
		t.Fatalf("package change kind = %q, want %q", got, want)
	}
	if got, want := packageChangeKind(after, before), "worse"; got != want {
		t.Fatalf("reverse package change kind = %q, want %q", got, want)
	}
}

func TestSyncCommandDoesNotResetUnchangedRepository(t *testing.T) {
	remote := initSyncRemote(t)
	target := filepath.Join(t.TempDir(), "target")
	cfg := SyncConfig{RepoURL: remote, TargetDir: target, Depth: 2}
	if err := Sync(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	var stages []string
	cfg.Progress = func(stage, _ string) { stages = append(stages, stage) }
	cfg.Changes = func(ChangeSummary) { t.Fatal("unchanged repository reported package changes") }
	if err := Sync(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stages, []string{"check", "fetch", "unchanged"}) {
		t.Fatalf("progress stages = %v", stages)
	}
}

func TestSyncRsyncCancellationStopsTransport(t *testing.T) {
	script := filepath.Join(t.TempDir(), "rsync")
	// Replace the shell so context cancellation kills the only process holding
	// the test binary's inherited output descriptors.
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	cfg := SyncConfig{
		RepoURL: "rsync://example.invalid/repo", TargetDir: t.TempDir(),
		SyncType: "rsync", RsyncPath: script,
		Progress: func(stage, _ string) {
			if stage == "rsync" {
				close(started)
			}
		},
	}
	result := make(chan error, 1)
	go func() { result <- Sync(ctx, cfg) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("rsync transport did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Sync error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Sync did not stop after cancellation")
	}
}

func TestSyncCanceledContextDoesNotStartFallbackTransport(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "invoked")
	script := filepath.Join(t.TempDir(), "rsync")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Sync(ctx, SyncConfig{
		RepoURL: marker, TargetDir: t.TempDir(), RsyncPath: script,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Sync error = %v, want context.Canceled", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("fallback transport ran after cancellation: %v", statErr)
	}
}

func initSyncRemote(t testing.TB) string {
	t.Helper()
	remote := t.TempDir()
	testGit(t, "init", "-b", "master", remote)
	writeSyncFile(t, remote, "app-misc/modified/modified-1.ebuild", "EAPI=8\nDESCRIPTION=initial\n")
	writeSyncFile(t, remote, "app-misc/old/old-1.ebuild", "EAPI=8\n")
	commitSyncRemote(t, remote, "initial")
	return remote
}

func writeSyncFile(t testing.TB, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitSyncRemote(t testing.TB, remote, message string) {
	t.Helper()
	testGit(t, "-C", remote, "add", "-A")
	testGit(t, "-C", remote, "-c", "user.name=Arise Test", "-c", "user.email=arise@example.invalid", "commit", "-m", message)
}

func testGit(t testing.TB, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func testGitOutput(t testing.TB, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
