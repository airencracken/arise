//go:build live_portage

package rebuild

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/airencracken/arise/internal/fetch"
	"github.com/airencracken/arise/internal/portage"
)

func TestLiveLibarchiveDisposableBuild(t *testing.T) {
	if os.Getenv("ARISE_LIVE_LIBARCHIVE_BUILD") == "" {
		t.Skip("ARISE_LIVE_LIBARCHIVE_BUILD is required")
	}
	t.Setenv("FEATURES", "-fixlafiles -multilib-strict -qa-unresolved-soname-deps -strict -userpriv -usersandbox")
	configuration, err := portage.LoadEffectiveConfig("/etc/portage")
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	root := filepath.Join(base, "root")
	distfiles := filepath.Join(base, "distfiles")
	for _, directory := range []string{root, filepath.Join(root, "var", "db", "pkg"), distfiles, filepath.Join(base, "work"), filepath.Join(base, "logs"), filepath.Join(base, "journals")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	source, err := os.Open("/var/cache/distfiles/libarchive-3.8.7.tar.xz")
	if err != nil {
		t.Fatal(err)
	}
	destination, err := os.Create(filepath.Join(distfiles, "libarchive-3.8.7.tar.xz"))
	if err != nil {
		source.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(destination, source)
	closeErr, sourceCloseErr := destination.Close(), source.Close()
	if copyErr != nil || closeErr != nil || sourceCloseErr != nil {
		t.Fatalf("copy distfile: copy=%v close=%v source-close=%v", copyErr, closeErr, sourceCloseErr)
	}
	useFlags := map[string]bool{
		"abi_x86_64": true, "acl": true, "bzip2": true, "e2fsprogs": true, "iconv": true,
		"lzma": true, "xattr": true, "zstd": true,
	}
	cfg := &RebuildConfig{
		RepoDir: "/var/db/repos/gentoo", Repository: "gentoo", SelectedSlot: "0/13",
		DistfilesDir: distfiles, RootDir: root, SysrootDir: "/", BrootDir: "/",
		VdbDir: filepath.Join(root, "var", "db", "pkg"), WorkDirBase: filepath.Join(base, "work"),
		PhaseProtocol: true, PortageConfig: configuration, ConfigRoot: "/etc/portage",
		PhaseLogDir: filepath.Join(base, "logs"), JournalDir: filepath.Join(base, "journals"),
		UseFlags: useFlags, Fetcher: &fetch.Fetcher{}, CFLAGS: configuration.CFLAGS,
		CXXFLAGS: configuration.CXXFLAGS, LDFLAGS: configuration.MakeConf["LDFLAGS"], MAKEOPTS: "-j2", Arch: "amd64",
		AllowLiveRoot: true,
	}
	if err := RebuildPackage(context.Background(), "app-arch/libarchive-3.8.7", cfg); err != nil {
		_ = filepath.WalkDir(filepath.Join(base, "work"), func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr == nil && !entry.IsDir() && entry.Name() == "config.log" {
				if data, readErr := os.ReadFile(path); readErr == nil {
					if len(data) > 16000 {
						data = data[len(data)-16000:]
					}
					t.Logf("%s tail:\n%s", path, data)
				}
			}
			return nil
		})
		matches, _ := filepath.Glob(filepath.Join(base, "logs", "*"))
		for _, match := range matches {
			if data, readErr := os.ReadFile(match); readErr == nil {
				if len(data) > 4000 {
					data = data[len(data)-4000:]
				}
				t.Logf("%s:\n%s", match, data)
			}
		}
		t.Fatal(err)
	}
}
