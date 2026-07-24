//go:build live_portage

package rebuild

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/fetch"
	"github.com/airencracken/arise/internal/portage"
)

func TestLiveGMPDisposableBuild(t *testing.T) {
	if os.Getenv("ARISE_LIVE_GMP_BUILD") == "" {
		t.Skip("ARISE_LIVE_GMP_BUILD is required")
	}
	t.Setenv("FEATURES", "-fixlafiles -multilib-strict -qa-unresolved-soname-deps -strict -test -userpriv -usersandbox")
	configuration, err := portage.LoadEffectiveConfig("/etc/portage")
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	root, distfiles := filepath.Join(base, "root"), filepath.Join(base, "distfiles")
	for _, directory := range []string{root, filepath.Join(root, "var", "db", "pkg"), distfiles, filepath.Join(base, "work"), filepath.Join(base, "logs"), filepath.Join(base, "journals")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	source, err := os.Open("/var/cache/distfiles/gmp-6.3.0.tar.xz")
	if err != nil {
		t.Fatal(err)
	}
	destination, err := os.Create(filepath.Join(distfiles, "gmp-6.3.0.tar.xz"))
	if err != nil {
		source.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(destination, source)
	closeErr, sourceCloseErr := destination.Close(), source.Close()
	if copyErr != nil || closeErr != nil || sourceCloseErr != nil {
		t.Fatalf("copy distfile: copy=%v close=%v source-close=%v", copyErr, closeErr, sourceCloseErr)
	}
	cfg := &RebuildConfig{
		RepoDir: "/var/db/repos/gentoo", Repository: "gentoo", SelectedSlot: "0/10.4",
		SourceURI:    "https://gmplib.org/download/gmp/gmp-6.3.0.tar.xz mirror://gnu/gmp/gmp-6.3.0.tar.xz",
		DistfilesDir: distfiles, RootDir: root, SysrootDir: "/", BrootDir: "/",
		VdbDir: filepath.Join(root, "var", "db", "pkg"), WorkDirBase: filepath.Join(base, "work"),
		PhaseProtocol: true, PortageConfig: configuration, ConfigRoot: "/etc/portage",
		PhaseLogDir: filepath.Join(base, "logs"), JournalDir: filepath.Join(base, "journals"),
		UseFlags: map[string]bool{
			"abi_x86_64": true, "asm": true, "cpudetection": true, "cxx": true,
			"elibc_glibc": true, "kernel_linux": true,
		},
		Fetcher: &fetch.Fetcher{}, CFLAGS: configuration.CFLAGS, CXXFLAGS: configuration.CXXFLAGS,
		LDFLAGS: configuration.MakeConf["LDFLAGS"], MAKEOPTS: "-j8", Arch: "amd64",
	}
	if err := RebuildPackage(context.Background(), "dev-libs/gmp-6.3.0-r2", cfg); err != nil {
		matches, _ := filepath.Glob(filepath.Join(base, "logs", "*"))
		for _, match := range matches {
			if data, readErr := os.ReadFile(match); readErr == nil {
				if len(data) > 50000 {
					data = data[len(data)-50000:]
				}
				t.Logf("%s tail:\n%s", match, data)
			}
		}
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(root, "var", "db", "pkg", "dev-libs", "gmp-6.3.0-r2", "CONTENTS"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "/usr/share/info/dir") {
		t.Fatalf("generated Info directory index became package-owned:\n%s", contents)
	}
}
