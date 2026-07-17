package phaseproto

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/portage"
)

func TestApplyPackagePolicyBuildsEclassAndPatchPrecedence(t *testing.T) {
	root := t.TempDir()
	master := filepath.Join(root, "master")
	overlay := filepath.Join(root, "overlay")
	config := filepath.Join(root, "config")
	work := filepath.Join(root, "work")
	for _, directory := range []string{filepath.Join(master, "eclass"), filepath.Join(overlay, "eclass"), work, filepath.Join(config, "etc/portage/patches/cat/pkg"), filepath.Join(config, "etc/portage/patches/cat/pkg-1-r0:2")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	request := Request{Protocol: Version, ID: "policy-1", Command: "run_phase", Phase: "src_prepare", EAPI: "8", Ebuild: filepath.Join(root, "pkg.ebuild")}
	if err := os.WriteFile(request.Ebuild, []byte("EAPI=8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	policy := PackagePolicy{Repositories: []portage.RepoEntry{{Name: "master", Location: master}, {Name: "overlay", Location: overlay, Masters: []string{"master"}}}, Repository: "overlay", ConfigRoot: config, Category: "cat", PN: "pkg", P: "pkg-1", PR: "r0", Slot: "2/3", WorkDir: work}
	got, err := ApplyPackagePolicy(request, policy)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.EclassDirs, []string{filepath.Join(overlay, "eclass"), filepath.Join(master, "eclass")}) {
		t.Fatalf("eclass dirs = %#v", got.EclassDirs)
	}
	wantPatches := []string{filepath.Join(config, "etc/portage/patches/cat/pkg"), filepath.Join(config, "etc/portage/patches/cat/pkg-1-r0:2")}
	if !reflect.DeepEqual(got.UserPatchDirs, wantPatches) {
		t.Fatalf("patch dirs = %#v, want %#v", got.UserPatchDirs, wantPatches)
	}
}

func TestApplyPackagePolicyComposesPackageEnvironment(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	config := filepath.Join(root, "config")
	for _, directory := range []string{filepath.Join(repo, "eclass"), filepath.Join(config, "env")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(config, "env", "common.conf"), []byte("USE=\"first -old\"\nFEATURES=\"sandbox test\"\nCFLAGS=\"-O2\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "env", "package.conf"), []byte("USE=\"-first second\"\nFEATURES=\"-test usersandbox\"\nCFLAGS=\"-O3\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	configuration := &portage.Config{ConfigRoot: config, PackageEnvRules: []portage.PackageUseRule{
		{Atom: "*/*", Flags: []string{"common.conf"}},
		{Atom: "dev-lang/python", Flags: []string{"package.conf"}},
	}}
	ebuild := filepath.Join(root, "python-3.13.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "env-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild, Env: map[string]string{"CFLAGS": "-Og", "MAKEOPTS": "-j8"}}
	got, err := ApplyPackagePolicy(request, PackagePolicy{Configuration: configuration, Repositories: []portage.RepoEntry{{Name: "gentoo", Location: repo}}, Repository: "gentoo", ConfigRoot: config, CPV: "dev-lang/python-3.13", Slot: "3.13"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"USE": "-first -old second", "FEATURES": "sandbox -test usersandbox", "CFLAGS": "-Og", "MAKEOPTS": "-j8"}
	if !reflect.DeepEqual(got.Env, want) {
		t.Fatalf("environment = %#v, want %#v", got.Env, want)
	}
}

func TestApplyPackagePolicyRejectsPackageEnvironmentStartupInjection(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	config := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(repo, "eclass"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(config, "env"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "env", "inject.conf"), []byte("BASH_ENV=/tmp/inject\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	configuration := &portage.Config{ConfigRoot: config, PackageEnvRules: []portage.PackageUseRule{{Atom: "*/*", Flags: []string{"inject.conf"}}}}
	ebuild := filepath.Join(root, "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "env-unsafe", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild}
	_, err := ApplyPackagePolicy(request, PackagePolicy{Configuration: configuration, Repositories: []portage.RepoEntry{{Name: "gentoo", Location: repo}}, Repository: "gentoo", ConfigRoot: config, CPV: "cat/pkg-1"})
	if err == nil || !strings.Contains(err.Error(), "BASH_ENV") {
		t.Fatalf("ApplyPackagePolicy error = %v, want BASH_ENV rejection", err)
	}
}

func TestApplyPackagePolicyCopiesRootAndScratchDirectories(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "eclass"), 0o755); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(root, "pkg.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirs := []string{filepath.Join(root, "target"), filepath.Join(root, "sysroot"), filepath.Join(root, "broot"), filepath.Join(root, "temp"), filepath.Join(root, "home")}
	request := Request{Protocol: Version, ID: "roots-policy", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild}
	got, err := ApplyPackagePolicy(request, PackagePolicy{Repositories: []portage.RepoEntry{{Name: "gentoo", Location: repo}}, Repository: "gentoo", ConfigRoot: root, RootDir: dirs[0], SysrootDir: dirs[1], BrootDir: dirs[2], TempDir: dirs[3], HomeDir: dirs[4]})
	if err != nil {
		t.Fatal(err)
	}
	if got.RootDir != dirs[0] || got.SysrootDir != dirs[1] || got.BrootDir != dirs[2] || got.TempDir != dirs[3] || got.HomeDir != dirs[4] {
		t.Fatalf("policy directory contract = %#v", got)
	}
}
