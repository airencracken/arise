//go:build live_portage

package rebuild

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/portage"
)

func TestLiveLLVM19DisposableBuild(t *testing.T) {
	if os.Getenv("ARISE_LIVE_LLVM_BUILD") == "" {
		t.Skip("ARISE_LIVE_LLVM_BUILD is required")
	}
	configuration, err := portage.LoadEffectiveConfig("/etc/portage")
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	root := filepath.Join(base, "root")
	distdir := filepath.Join(base, "distfiles")
	for _, directory := range []string{root, filepath.Join(root, "var", "db", "pkg"), distdir, filepath.Join(base, "work"), filepath.Join(base, "logs"), filepath.Join(base, "journals")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	source, err := os.Open("/var/cache/distfiles/llvm-project-19.1.7.src.tar.xz")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.Create(filepath.Join(distdir, "llvm-project-19.1.7.src.tar.xz"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(target, source); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	useData, err := os.ReadFile("/var/db/pkg/llvm-core/llvm-19.1.7/USE")
	if err != nil {
		t.Fatal(err)
	}
	useFlags := make(map[string]bool)
	for _, flag := range strings.Fields(string(useData)) {
		useFlags[flag] = true
	}
	cfg := &RebuildConfig{
		RepoDir: "/var/db/repos/gentoo", Repository: "gentoo", SelectedSlot: "19/19.1",
		DistfilesDir: distdir, SourceURI: "https://github.com/llvm/llvm-project/releases/download/llvmorg-19.1.7/llvm-project-19.1.7.src.tar.xz",
		RootDir: root, SysrootDir: "/", BrootDir: "/", VdbDir: filepath.Join(root, "var", "db", "pkg"), WorkDirBase: filepath.Join(base, "work"),
		PhaseProtocol: true, PortageConfig: configuration, ConfigRoot: "/etc/portage",
		PhaseLogDir: filepath.Join(base, "logs"), JournalDir: filepath.Join(base, "journals"),
		UseFlags: useFlags, CFLAGS: configuration.CFLAGS, CXXFLAGS: configuration.CXXFLAGS, LDFLAGS: configuration.MakeConf["LDFLAGS"], MAKEOPTS: "-j8", Arch: "amd64",
	}
	if err := RebuildPackage(context.Background(), "llvm-core/llvm-19.1.7", cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "usr", "lib", "llvm", "19", "lib64", "libLLVM-19.so")); err != nil {
		t.Fatal(err)
	}
}

func TestLiveClang19DisposableBuild(t *testing.T) {
	if os.Getenv("ARISE_LIVE_CLANG_BUILD") == "" {
		t.Skip("ARISE_LIVE_CLANG_BUILD is required")
	}
	configuration, err := portage.LoadEffectiveConfig("/etc/portage")
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	root := filepath.Join(base, "root")
	distdir := filepath.Join(base, "distfiles")
	for _, directory := range []string{root, filepath.Join(root, "var", "db", "pkg"), distdir, filepath.Join(base, "work"), filepath.Join(base, "logs"), filepath.Join(base, "journals")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	source, err := os.Open("/var/cache/distfiles/llvm-project-19.1.7.src.tar.xz")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.Create(filepath.Join(distdir, "llvm-project-19.1.7.src.tar.xz"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(target, source); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	useData, err := os.ReadFile("/var/db/pkg/llvm-core/clang-19.1.7/USE")
	if err != nil {
		t.Fatal(err)
	}
	useFlags := make(map[string]bool)
	for _, flag := range strings.Fields(string(useData)) {
		useFlags[flag] = true
	}
	// Match the currently resolved profile transition for the -r1 upgrade.
	delete(useFlags, "python_single_target_python3_12")
	useFlags["python_single_target_python3_14"] = true
	cfg := &RebuildConfig{
		RepoDir: "/var/db/repos/gentoo", Repository: "gentoo", SelectedSlot: "19/19.1",
		DistfilesDir: distdir, SourceURI: "https://github.com/llvm/llvm-project/releases/download/llvmorg-19.1.7/llvm-project-19.1.7.src.tar.xz",
		RootDir: root, SysrootDir: "/", BrootDir: "/", VdbDir: filepath.Join(root, "var", "db", "pkg"), WorkDirBase: filepath.Join(base, "work"),
		PhaseProtocol: true, PortageConfig: configuration, ConfigRoot: "/etc/portage",
		PhaseLogDir: filepath.Join(base, "logs"), JournalDir: filepath.Join(base, "journals"),
		UseFlags: useFlags, CFLAGS: configuration.CFLAGS, CXXFLAGS: configuration.CXXFLAGS, LDFLAGS: configuration.MakeConf["LDFLAGS"], MAKEOPTS: "-j8", Arch: "amd64",
	}
	if err := RebuildPackage(context.Background(), "llvm-core/clang-19.1.7-r1", cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "usr", "lib", "llvm", "19", "bin", "clang")); err != nil {
		t.Fatal(err)
	}
}
