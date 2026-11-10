//go:build live_portage

package rebuild

import (
	"context"
	"debug/elf"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/fetch"
	"github.com/airencracken/arise/internal/portage"
)

func TestLiveLibxcryptMultilibDisposableBuild(t *testing.T) {
	if os.Getenv("ARISE_LIVE_LIBXCRYPT_BUILD") == "" {
		t.Skip("ARISE_LIVE_LIBXCRYPT_BUILD is required")
	}
	t.Setenv("FEATURES", "-fixlafiles -multilib-strict -qa-unresolved-soname-deps -strict -test -userpriv -usersandbox")
	configuration, err := portage.LoadEffectiveConfig("/etc/portage")
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	root, distfiles := filepath.Join(base, "root"), filepath.Join(base, "distfiles")
	for _, directory := range []string{root, filepath.Join(root, "var/db/pkg"), distfiles, filepath.Join(base, "work"), filepath.Join(base, "logs"), filepath.Join(base, "journals")} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	const archive = "libxcrypt-4.5.2-autotools.tar.xz"
	source, err := os.Open(filepath.Join("/var/cache/distfiles", archive))
	if err != nil {
		t.Fatal(err)
	}
	destination, err := os.Create(filepath.Join(distfiles, archive))
	if err != nil {
		source.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(destination, source)
	closeErr, sourceCloseErr := destination.Close(), source.Close()
	if copyErr != nil || closeErr != nil || sourceCloseErr != nil {
		t.Fatalf("copy distfile: %v, %v, %v", copyErr, closeErr, sourceCloseErr)
	}
	cfg := &RebuildConfig{
		RepoDir: "/var/db/repos/gentoo", Repository: "gentoo", SelectedSlot: "0/1",
		SourceURI:    "https://distfiles.gentoo.org/pub/proj/toolchain/libxcrypt/" + archive,
		DistfilesDir: distfiles, RootDir: root, SysrootDir: "/", BrootDir: "/",
		VdbDir: filepath.Join(root, "var/db/pkg"), WorkDirBase: filepath.Join(base, "work"),
		PhaseProtocol: true, PortageConfig: configuration, ConfigRoot: "/etc/portage",
		PhaseLogDir: filepath.Join(base, "logs"), JournalDir: filepath.Join(base, "journals"),
		UseFlags: map[string]bool{"abi_x86_32": true, "abi_x86_64": true, "compat": true,
			"system": true, "static-libs": true, "elibc_glibc": true, "kernel_linux": true, "amd64": true, "multilib": true},
		Fetcher: &fetch.Fetcher{}, CFLAGS: configuration.CFLAGS, CXXFLAGS: configuration.CXXFLAGS,
		LDFLAGS: configuration.MakeConf["LDFLAGS"], MAKEOPTS: "-j8", Arch: "amd64",
	}
	if err := RebuildPackage(context.Background(), "sys-libs/libxcrypt-4.5.2-r1", cfg); err != nil {
		matches, _ := filepath.Glob(filepath.Join(base, "logs", "*"))
		for _, path := range matches {
			if data, readErr := os.ReadFile(path); readErr == nil {
				if len(data) > 30000 {
					data = data[len(data)-30000:]
				}
				t.Logf("%s tail:\n%s", path, data)
			}
		}
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "var/db/pkg/sys-libs/libxcrypt-4.5.2-r1/CONTENTS"))
	if err != nil {
		t.Fatal(err)
	}
	for abi, class := range map[string]elf.Class{"x86": elf.ELFCLASS32, "amd64": elf.ELFCLASS64} {
		libdir := configuration.MakeConf["LIBDIR_"+abi]
		if libdir == "" {
			t.Fatalf("profile lacks LIBDIR_%s", abi)
		}
		if !strings.Contains(string(data), "/usr/"+libdir+"/libcrypt.so") {
			t.Fatalf("%s library is missing from the profile directory:\n%s", abi, data)
		}
		for _, soname := range []string{"libcrypt.so.1", "libcrypt.so.2"} {
			path := filepath.Join(root, "usr", libdir, soname)
			library, err := elf.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			got := library.Class
			library.Close()
			if got != class {
				t.Fatalf("%s is %v; want %v", path, got, class)
			}
		}
	}
}
