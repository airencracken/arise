//go:build live_portage

package rebuild

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/airencracken/arise/internal/portage"
)

func TestLiveThinkfanDisposableRoot(t *testing.T) {
	source := os.Getenv("ARISE_LIVE_THINKFAN_DISTFILE")
	if source == "" {
		t.Skip("ARISE_LIVE_THINKFAN_DISTFILE is required")
	}
	repository := "/var/db/repos/gentoo"
	base := t.TempDir()
	distdir, root := filepath.Join(base, "distfiles"), filepath.Join(base, "root")
	for _, directory := range []string{distdir, root, filepath.Join(root, "var", "db", "pkg"), filepath.Join(base, "work"), filepath.Join(base, "logs"), filepath.Join(base, "journals")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distdir, "thinkfan-1.3.1.tar.gz"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	configuration, err := portage.LoadEffectiveConfig("/etc/portage")
	if err != nil {
		t.Fatal(err)
	}
	configuration.MakeConf["FEATURES"] = "sandbox network-sandbox ipc-sandbox pid-sandbox collision-protect protect-owned preserve-libs xattr"
	cfg := &RebuildConfig{
		RepoDir: repository, Repository: "gentoo", DistfilesDir: distdir,
		RootDir: root, SysrootDir: "/", BrootDir: "/", VdbDir: filepath.Join(root, "var", "db", "pkg"), WorkDirBase: filepath.Join(base, "work"),
		PhaseProtocol: true, PortageConfig: configuration, ConfigRoot: "/etc/portage",
		PhaseLogDir: filepath.Join(base, "logs"), JournalDir: filepath.Join(base, "journals"),
		UseFlags: map[string]bool{"abi_x86_64": true, "elibc_glibc": true, "kernel_linux": true, "yaml": true},
		CFLAGS:   configuration.CFLAGS, CXXFLAGS: configuration.CXXFLAGS, MAKEOPTS: "-j1", Arch: "amd64",
	}
	if err := RebuildPackage(context.Background(), "app-laptop/thinkfan-1.3.1", cfg); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "usr", "sbin", "thinkfan")
	if _, err := os.Stat(binary); err != nil {
		t.Fatal(err)
	}
}
