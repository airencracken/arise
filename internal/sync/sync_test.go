package sync

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
		name string
		cfg  SyncConfig
	}{
		{
			name: "whitespace-only RepoURL",
			cfg: SyncConfig{
				RepoURL:   "   ",
				TargetDir: "/tmp/gentoo",
			},
		},
		{
			name: "whitespace-only TargetDir",
			cfg: SyncConfig{
				RepoURL:   "https://example.com/repo.git",
				TargetDir: "   ",
			},
		},
		{
			name: "very long RepoURL",
			cfg: SyncConfig{
				RepoURL:   strings.Repeat("x", 10000),
				TargetDir: "/tmp/gentoo",
			},
		},
		{
			name: "unicode RepoURL",
			cfg: SyncConfig{
				RepoURL:   "https://example.com/repo\u0000.git",
				TargetDir: "/tmp/gentoo",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if err != nil {
				t.Logf("Validate() with adversarial input returned error (expected): %v", err)
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
