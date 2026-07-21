//go:build live_portage

package rebuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/portage"
)

func TestLivePythonLegacyClusterPreflight(t *testing.T) {
	configuration, err := portage.LoadEffectiveConfig("/etc/portage")
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	for _, directory := range []string{filepath.Join(base, "work"), filepath.Join(base, "logs"), filepath.Join(base, "journals")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	readUse := func(cpv string) map[string]bool {
		data, err := os.ReadFile(filepath.Join("/var/db/pkg", filepath.FromSlash(cpv), "USE"))
		if err != nil {
			t.Fatal(err)
		}
		result := make(map[string]bool)
		for _, flag := range strings.Fields(string(data)) {
			result[flag] = true
		}
		return result
	}
	common := RebuildConfig{
		RepoDir: "/var/db/repos/gentoo", Repository: "gentoo", RootDir: "/", SysrootDir: "/", BrootDir: "/", VdbDir: "/var/db/pkg",
		WorkDirBase: filepath.Join(base, "work"), PhaseLogDir: filepath.Join(base, "logs"), JournalDir: filepath.Join(base, "journals"),
		PhaseProtocol: true, PortageConfig: configuration, ConfigRoot: "/etc/portage", AllowLiveRoot: true, AllowLiveUpgrade: true,
	}
	for _, test := range []struct {
		installed string
		selected  string
		slot      string
	}{
		{installed: "dev-lang/python-3.10.10_p3", selected: "dev-lang/python-3.10.20", slot: "3.10"},
		{installed: "dev-lang/python-3.9.9-r1", selected: "dev-lang/python-3.9.25", slot: "3.9"},
	} {
		cfg := common
		cfg.UseFlags = readUse(test.installed)
		cfg.SelectedSlot = test.slot
		if err := PreflightPackage(test.selected, &cfg); err != nil {
			t.Fatalf("%s upgrade preflight: %v", test.selected, err)
		}
	}
}
