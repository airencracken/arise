//go:build live_portage

package rebuild

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/airencracken/arise/internal/portage"
)

func TestLiveXrandrDisposableUpgrade(t *testing.T) {
	source := os.Getenv("ARISE_LIVE_XRANDR_DISTFILE")
	if source == "" {
		t.Skip("ARISE_LIVE_XRANDR_DISTFILE is required")
	}
	repository := "/var/db/repos/gentoo"
	base := t.TempDir()
	root := filepath.Join(base, "root")
	distdir := filepath.Join(base, "distfiles")
	oldVDB := filepath.Join(root, "var", "db", "pkg", "x11-apps", "xrandr-1.5.3")
	for _, directory := range []string{distdir, filepath.Join(root, "usr", "bin"), oldVDB, filepath.Join(base, "work"), filepath.Join(base, "logs"), filepath.Join(base, "journals")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distdir, "xrandr-1.5.4.tar.xz"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	oldBinary := filepath.Join(root, "usr", "bin", "xrandr")
	if err := os.WriteFile(oldBinary, []byte("old-xrandr"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldEbuild, err := os.ReadFile("/var/db/pkg/x11-apps/xrandr-1.5.3/xrandr-1.5.3.ebuild")
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{
		"CONTENTS":            []byte("obj /usr/bin/xrandr old 1\n"),
		"SLOT":                []byte("0\n"),
		"xrandr-1.5.3.ebuild": oldEbuild,
	} {
		if err := os.WriteFile(filepath.Join(oldVDB, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	portageConfig, err := portage.LoadEffectiveConfig("/etc/portage")
	if err != nil {
		t.Fatal(err)
	}
	portageConfig.MakeConf["FEATURES"] = "sandbox network-sandbox ipc-sandbox pid-sandbox collision-protect protect-owned xattr"
	cfg := &RebuildConfig{
		RepoDir: repository, Repository: "gentoo", DistfilesDir: distdir,
		SourceURI: "https://www.x.org/releases/individual/app/xrandr-1.5.4.tar.xz",
		RootDir:   root, SysrootDir: "/", BrootDir: "/", VdbDir: filepath.Join(root, "var", "db", "pkg"), WorkDirBase: filepath.Join(base, "work"),
		PhaseProtocol: true, PortageConfig: portageConfig, ConfigRoot: "/etc/portage",
		PhaseLogDir: filepath.Join(base, "logs"), JournalDir: filepath.Join(base, "journals"),
		UseFlags: map[string]bool{"abi_x86_64": true, "elibc_glibc": true, "kernel_linux": true},
		CFLAGS:   portageConfig.CFLAGS, CXXFLAGS: portageConfig.CXXFLAGS, MAKEOPTS: "-j1", Arch: "amd64",
		AllowLiveUpgrade: true,
	}
	livePreflight := *cfg
	livePreflight.RootDir, livePreflight.SysrootDir, livePreflight.BrootDir = "/", "/", "/"
	livePreflight.VdbDir = "/var/db/pkg"
	livePreflight.AllowLiveRoot = true
	if err := PreflightPackage("x11-apps/xrandr-1.5.4", &livePreflight); err != nil {
		t.Fatalf("xrandr live-upgrade eligibility: %v", err)
	}
	if err := RebuildPackage(context.Background(), "x11-apps/xrandr-1.5.4", cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "usr", "bin", "xrandr")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "var", "db", "pkg", "x11-apps", "xrandr-1.5.4", "CONTENTS")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(oldVDB); !os.IsNotExist(err) {
		t.Fatalf("old VDB remains: %v", err)
	}
}
