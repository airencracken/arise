package merge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveNewInstallConfigProtection(t *testing.T) {
	for _, test := range []struct {
		name, path, staged, installed string
		protect, mask                 []string
		wantErr                       bool
	}{
		{"local settings", "etc/conf.d/imvault", "file", "file", []string{"/etc"}, nil, false},
		{"local logrotate rule", "etc/logrotate.d/imvault", "file", "file", []string{"/etc"}, nil, false},
		{"exact protected file", "etc/conf.d/imvault", "file", "file", []string{"/etc/conf.d/imvault"}, nil, false},
		{"missing policy", "etc/conf.d/imvault", "file", "file", nil, nil, true},
		{"masked configuration", "etc/conf.d/imvault", "file", "file", []string{"/etc"}, []string{"/etc/conf.d"}, true},
		{"unprotected executable", "usr/bin/imvault", "file", "file", []string{"/etc"}, nil, true},
		{"prefix sibling", "etc-backup/imvault", "file", "file", []string{"/etc"}, nil, true},
		{"installed symlink", "etc/conf.d/imvault", "file", "symlink", []string{"/etc"}, nil, true},
		{"staged symlink", "etc/conf.d/imvault", "symlink", "file", []string{"/etc"}, nil, true},
		{"installed directory", "etc/conf.d/imvault", "file", "directory", []string{"/etc"}, nil, true},
		{"staged directory", "etc/conf.d/imvault", "directory", "file", []string{"/etc"}, nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			image, root := filepath.Join(base, "image"), filepath.Join(base, "root")
			for parent, kind := range map[string]string{image: test.staged, root: test.installed} {
				path := filepath.Join(parent, test.path)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				var err error
				switch kind {
				case "file":
					err = os.WriteFile(path, []byte(parent), 0o600)
				case "symlink":
					err = os.Symlink("missing", path)
				case "directory":
					err = os.Mkdir(path, 0o755)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			err := validateLiveNewInstallTargets(image, root, test.protect, test.mask)
			if (err != nil) != test.wantErr {
				t.Fatalf("preflight error = %v, want error %v", err, test.wantErr)
			}
		})
	}
}

func TestNewInstallPreservesManualConfigurationAndLogrotate(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		name := "commit"
		if rollback {
			name = "rollback"
		}
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			image, root := filepath.Join(base, "image"), filepath.Join(base, "root")
			packaged := map[string]string{
				"etc/conf.d/imvault": "packaged settings", "etc/logrotate.d/imvault": "packaged rotation",
				"etc/init.d/imvault": "same service", "usr/bin/imvault": "packaged binary",
			}
			local := map[string]string{
				"etc/conf.d/imvault": "local settings", "etc/conf.d/._cfg0000_imvault": "earlier pending settings",
				"etc/logrotate.d/imvault": "custom rotation", "etc/init.d/imvault": "same service",
				"var/lib/imvault/imvault.db": "existing database", "usr/local/bin/imvault": "manual binary",
			}
			if err := makeDestDir(image, packaged); err != nil {
				t.Fatal(err)
			}
			if err := makeDestDir(root, local); err != nil {
				t.Fatal(err)
			}
			cfg := MergeConfig{RootDir: root, VdbDir: filepath.Join(root, "var/db/pkg"), Category: "www-apps", Package: "imvault", Version: "0.6.0", JournalDir: filepath.Join(base, "journal"), ConfigProtect: []string{"/etc"}}
			// Exercise the live preflight against a disposable root, then the
			// same transactional merge used after that gate on the running host.
			if err := validateLiveNewInstallTargets(image, root, cfg.ConfigProtect, cfg.ConfigProtectMask); err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected failure after payload sync")
			if rollback {
				cfg.AfterPayloadSync = func() error { return injected }
			}
			err := Merge(context.Background(), image, cfg)
			if rollback && !errors.Is(err, injected) || !rollback && err != nil {
				t.Fatalf("merge: %v", err)
			}
			for path, want := range local {
				got, err := os.ReadFile(filepath.Join(root, path))
				if err != nil || string(got) != want {
					t.Errorf("existing %s = %q, %v; want %q", path, got, err, want)
				}
			}
			updates := map[string]string{
				"etc/conf.d/._cfg0001_imvault": "packaged settings", "etc/logrotate.d/._cfg0000_imvault": "packaged rotation",
				"usr/bin/imvault": "packaged binary",
			}
			for path, want := range updates {
				got, err := os.ReadFile(filepath.Join(root, path))
				if rollback {
					if !os.IsNotExist(err) {
						t.Errorf("rollback left %s: %v", path, err)
					}
				} else if err != nil || string(got) != want {
					t.Errorf("installed %s = %q, %v; want %q", path, got, err, want)
				}
			}
			if _, err := os.Stat(filepath.Join(root, "etc/init.d/._cfg0000_imvault")); !os.IsNotExist(err) {
				t.Fatalf("identical service created an unnecessary config update: %v", err)
			}
			contents, err := os.ReadFile(filepath.Join(cfg.VdbPath(), "CONTENTS"))
			if rollback {
				if !os.IsNotExist(err) {
					t.Fatalf("rollback left package ownership: %v", err)
				}
				return
			}
			if err != nil || !strings.Contains(string(contents), "obj /etc/logrotate.d/._cfg0000_imvault ") || !strings.Contains(string(contents), "obj /etc/conf.d/._cfg0001_imvault ") {
				t.Fatalf("config updates missing from CONTENTS: %q, %v", contents, err)
			}
			if err := validateLiveReplacementTargetsWithConfig(image, root, cfg.VdbPath(), cfg.ConfigProtect, cfg.ConfigProtectMask); err != nil {
				t.Fatalf("pending config updates prevented subsequent reinstall: %v", err)
			}
		})
	}
}
