//go:build live_portage

package rebuild

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/fetch"
	"github.com/airencracken/arise/internal/portage"
)

func TestLivePythonLegacyDisposableBuilds(t *testing.T) {
	if os.Getenv("ARISE_LIVE_PYTHON_BUILD") == "" {
		t.Skip("ARISE_LIVE_PYTHON_BUILD is required")
	}
	configuration, err := portage.LoadEffectiveConfig("/etc/portage")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		installed string
		selected  string
		version   string
		slot      string
	}{
		{installed: "dev-lang/python-3.10.10_p3", selected: "dev-lang/python-3.10.20", version: "3.10.20", slot: "3.10"},
		{installed: "dev-lang/python-3.9.9-r1", selected: "dev-lang/python-3.9.25", version: "3.9.25", slot: "3.9"},
	} {
		t.Run(test.slot, func(t *testing.T) {
			base := t.TempDir()
			if err := os.Chmod(base, 0o755); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(base, "root")
			for _, directory := range []string{root, filepath.Join(root, "var", "db", "pkg"), filepath.Join(base, "work"), filepath.Join(base, "logs"), filepath.Join(base, "journals")} {
				if err := os.MkdirAll(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			useData, err := os.ReadFile(filepath.Join("/var/db/pkg", filepath.FromSlash(test.installed), "USE"))
			if err != nil {
				t.Fatal(err)
			}
			useFlags := make(map[string]bool)
			for _, flag := range strings.Fields(string(useData)) {
				useFlags[flag] = true
			}
			sourceURI := strings.Join([]string{
				"https://www.python.org/ftp/python/" + test.version + "/Python-" + test.version + ".tar.xz",
				"https://distfiles.gentoo.org/pub/proj/python/patchsets/" + test.slot + "/python-gentoo-patches-" + test.version + ".tar.xz",
			}, " ")
			fetcher := &fetch.Fetcher{}
			verified, err := fetcher.AcquireManifest(context.Background(), "/var/db/repos/gentoo/dev-lang/python/Manifest", sourceURI, useFlags, fetch.FetchConfig{DistfilesDir: "/tmp/arise-python-distfiles"})
			if err != nil {
				t.Fatal(err)
			}
			if len(verified.Artifacts) != 2 {
				t.Fatalf("verified artifacts=%v, want source and patchset", verified.Artifacts)
			}
			cfg := &RebuildConfig{
				RepoDir: "/var/db/repos/gentoo", Repository: "gentoo", SelectedSlot: test.slot,
				DistfilesDir: "/tmp/arise-python-distfiles", SourceURI: sourceURI,
				RootDir: root, SysrootDir: "/", BrootDir: "/", VdbDir: filepath.Join(root, "var", "db", "pkg"), WorkDirBase: filepath.Join(base, "work"),
				PhaseProtocol: true, PortageConfig: configuration, ConfigRoot: "/etc/portage",
				PhaseLogDir: filepath.Join(base, "logs"), JournalDir: filepath.Join(base, "journals"),
				UseFlags: useFlags, Fetcher: fetcher, CFLAGS: configuration.CFLAGS, CXXFLAGS: configuration.CXXFLAGS, LDFLAGS: configuration.MakeConf["LDFLAGS"], MAKEOPTS: "-j8", Arch: "amd64",
			}
			if err := RebuildPackage(context.Background(), test.selected, cfg); err != nil {
				configLogs, _ := filepath.Glob(filepath.Join(base, "work", "*", "Python-*", "config.log"))
				for _, configLog := range configLogs {
					if data, readErr := os.ReadFile(configLog); readErr == nil {
						if len(data) > 12000 {
							data = data[len(data)-12000:]
						}
						t.Logf("%s tail:\n%s", configLog, data)
					}
				}
				_ = filepath.WalkDir(filepath.Join(base, "work"), func(path string, entry os.DirEntry, walkErr error) error {
					if walkErr == nil && entry.IsDir() {
						relative, _ := filepath.Rel(filepath.Join(base, "work"), path)
						if strings.Count(relative, string(filepath.Separator)) < 2 {
							t.Logf("work directory: %s", relative)
						}
					}
					return nil
				})
				matches, _ := filepath.Glob(filepath.Join(base, "logs", "*"))
				for _, match := range matches {
					if data, readErr := os.ReadFile(match); readErr == nil {
						if len(data) > 12000 {
							data = data[len(data)-12000:]
						}
						t.Logf("%s:\n%s", match, data)
					}
				}
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, "usr", "bin", "python"+test.slot)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
