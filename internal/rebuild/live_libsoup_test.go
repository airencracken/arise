//go:build live_portage

package rebuild

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/airencracken/arise/internal/portage"
)

func TestLiveLibsoupDisposableRoot(t *testing.T) {
	source := os.Getenv("ARISE_LIVE_LIBSOUP_DISTFILE")
	if source == "" {
		t.Skip("ARISE_LIVE_LIBSOUP_DISTFILE is required")
	}
	base := t.TempDir()
	distdir, root := filepath.Join(base, "distfiles"), filepath.Join(base, "root")
	for _, directory := range []string{distdir, root, filepath.Join(root, "var", "db", "pkg"), filepath.Join(base, "work"), filepath.Join(base, "logs"), filepath.Join(base, "journals")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	oldVDB := filepath.Join(root, "var", "db", "pkg", "net-libs", "libsoup-2.74.2")
	if err := os.MkdirAll(oldVDB, 0o755); err != nil {
		t.Fatal(err)
	}
	oldEbuild, err := os.ReadFile("/var/db/pkg/net-libs/libsoup-2.74.2/libsoup-2.74.2.ebuild")
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{
		"libsoup-2.74.2.ebuild": oldEbuild,
		"SLOT":                  []byte("2.4/1\n"),
		"COUNTER":               []byte("1\n"),
		"CONTENTS":              []byte("obj /usr/lib64/libsoup-2.4.so.1.11.2 0 0\n"),
	} {
		if err := os.WriteFile(filepath.Join(oldVDB, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distdir, "libsoup-2.74.3.tar.xz"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	configuration, err := portage.LoadEffectiveConfig("/etc/portage")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &RebuildConfig{
		RepoDir: "/var/db/repos/gentoo", Repository: "gentoo", DistfilesDir: distdir,
		SourceURI: "https://download.gnome.org/sources/libsoup/2.74/libsoup-2.74.3.tar.xz",
		RootDir:   root, SysrootDir: "/", BrootDir: "/", VdbDir: filepath.Join(root, "var", "db", "pkg"), WorkDirBase: filepath.Join(base, "work"),
		PhaseProtocol: true, PortageConfig: configuration, ConfigRoot: "/etc/portage",
		PhaseLogDir: filepath.Join(base, "logs"), JournalDir: filepath.Join(base, "journals"),
		AllowLiveRoot: true, AllowLiveUpgrade: true,
		UseFlags: map[string]bool{
			"abi_x86_64": true, "elibc_glibc": true, "kernel_linux": true,
			"introspection": true, "ssl": true, "vala": true,
		},
		CFLAGS: configuration.CFLAGS, CXXFLAGS: configuration.CXXFLAGS, MAKEOPTS: "-j1", Arch: "amd64",
	}
	if err := RebuildPackage(context.Background(), "net-libs/libsoup-2.74.3-r1", cfg); err != nil {
		matches, _ := filepath.Glob(filepath.Join(base, "logs", "*"))
		for _, match := range matches {
			if data, readErr := os.ReadFile(match); readErr == nil {
				t.Logf("%s:\n%s", match, data)
			}
		}
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "usr", "lib64", "libsoup-2.4.so.1.11.2")); err != nil {
		t.Fatal(err)
	}
}
