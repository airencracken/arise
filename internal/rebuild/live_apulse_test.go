//go:build live_portage

package rebuild

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/airencracken/arise/internal/portage"
)

func TestLiveApulseDisposableRoot(t *testing.T) {
	source := os.Getenv("ARISE_LIVE_APULSE_DISTFILE")
	if source == "" {
		t.Skip("ARISE_LIVE_APULSE_DISTFILE is required")
	}
	repository := os.Getenv("ARISE_LIVE_GENTOO_REPO")
	if repository == "" {
		repository = "/var/db/repos/gentoo"
	}
	base := t.TempDir()
	distdir := filepath.Join(base, "distfiles")
	for _, directory := range []string{distdir, filepath.Join(base, "root"), filepath.Join(base, "root", "var", "db", "pkg"), filepath.Join(base, "work"), filepath.Join(base, "logs"), filepath.Join(base, "journals")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distdir, "apulse-0.1.14.tar.gz"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	portageConfig, err := portage.LoadEffectiveConfig("/etc/portage")
	if err != nil {
		t.Fatal(err)
	}
	portageConfig.MakeConf["FEATURES"] = "sandbox network-sandbox ipc-sandbox pid-sandbox collision-protect protect-owned preserve-libs xattr"
	cfg := &RebuildConfig{
		RepoDir: repository, Repository: "gentoo", DistfilesDir: distdir,
		RootDir: filepath.Join(base, "root"), VdbDir: filepath.Join(base, "root", "var", "db", "pkg"), WorkDirBase: filepath.Join(base, "work"),
		// Exercise a disposable target ROOT while using the host's already
		// resolved target/build dependency roots. No host package state is mutated.
		SysrootDir: "/", BrootDir: "/",
		PhaseProtocol: true, PortageConfig: portageConfig, ConfigRoot: "/etc/portage",
		PhaseLogDir: filepath.Join(base, "logs"), JournalDir: filepath.Join(base, "journals"),
		UseFlags: map[string]bool{"abi_x86_64": true, "elibc_glibc": true, "kernel_linux": true},
		CFLAGS:   portageConfig.CFLAGS, CXXFLAGS: portageConfig.CXXFLAGS, MAKEOPTS: "-j1",
		Arch: "amd64", HasVersion: map[string]bool{"<dev-build/cmake-4.2.1": false},
	}
	livePreflight := *cfg
	livePreflight.RootDir, livePreflight.SysrootDir, livePreflight.BrootDir = "/", "/", "/"
	livePreflight.VdbDir = "/var/db/pkg"
	livePreflight.AllowLiveRoot = true
	if err := PreflightPackage("media-sound/apulse-0.1.14", &livePreflight); err != nil {
		t.Fatalf("apulse live-canary eligibility: %v", err)
	}
	if err := RebuildPackage(context.Background(), "media-sound/apulse-0.1.14", cfg); err != nil {
		t.Fatal(err)
	}
	// Same-version replacement exercises VDB preimage capture and package-owned
	// payload replacement against the real ebuild before the live reinstall lane.
	if err := RebuildPackage(context.Background(), "media-sound/apulse-0.1.14", cfg); err != nil {
		t.Fatalf("disposable apulse reinstall: %v", err)
	}
	for _, path := range []string{"usr/bin/apulse", "usr/lib64/apulse/libpulse.so.0"} {
		if _, err := os.Stat(filepath.Join(cfg.RootDir, path)); err != nil {
			var installed []string
			_ = filepath.WalkDir(cfg.RootDir, func(current string, _ os.DirEntry, _ error) error {
				if rel, relErr := filepath.Rel(cfg.RootDir, current); relErr == nil {
					installed = append(installed, rel)
				}
				return nil
			})
			t.Fatalf("missing %s: %v; installed=%v", path, err, installed)
		}
	}
}
