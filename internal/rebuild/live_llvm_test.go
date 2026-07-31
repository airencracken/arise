//go:build live_portage

package rebuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/portage"
)

func TestLiveLLVM19ClusterPreflight(t *testing.T) {
	for _, cpv := range []string{"llvm-core/llvm-19.1.7", "llvm-core/clang-19.1.7"} {
		if _, err := os.Stat(filepath.Join("/var/db/pkg", filepath.FromSlash(cpv))); os.IsNotExist(err) {
			t.Skipf("installed parity fixture %s is no longer present", cpv)
		} else if err != nil {
			t.Fatalf("inspect installed parity fixture %s: %v", cpv, err)
		}
	}
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
		PhaseProtocol: true, PortageConfig: configuration, ConfigRoot: "/etc/portage", AllowLiveRoot: true,
	}
	llvm := common
	llvm.UseFlags = readUse("llvm-core/llvm-19.1.7")
	llvm.SelectedSlot = "19/19.1"
	llvm.AllowLiveReplacement = true
	if err := PreflightPackage("llvm-core/llvm-19.1.7", &llvm); err != nil {
		t.Fatalf("LLVM reinstall preflight: %v", err)
	}
	clang := common
	clang.UseFlags = readUse("llvm-core/clang-19.1.7")
	clang.SelectedSlot = "19/19.1"
	clang.AllowLiveUpgrade = true
	if err := PreflightPackage("llvm-core/clang-19.1.7-r1", &clang); err != nil {
		t.Fatalf("Clang upgrade preflight: %v", err)
	}
}
