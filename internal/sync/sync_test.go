package sync

import (
	"testing"
)

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

func TestGitAvailable_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("GitAvailable() panicked: %v", r)
		}
	}()

	available := GitAvailable()
	t.Logf("GitAvailable() = %v", available)
}

func TestIsGitRepo_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("isGitRepo() panicked: %v", r)
		}
	}()

	_ = isGitRepo("/nonexistent/path/that/does/not/exist")
}

func TestIsGitRepo_TempDir(t *testing.T) {
	dir := t.TempDir()
	if isGitRepo(dir) {
		t.Error("empty temp dir should not be detected as a git repo")
	}
}
