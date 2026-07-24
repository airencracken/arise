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

func TestApplyPackagePolicyDerivesCanonicalPackageIdentity(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "eclass"), 0o755); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "identity-policy", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: filepath.Join(root, "pkg.ebuild")}
	got, err := ApplyPackagePolicy(request, PackagePolicy{
		Repositories: []portage.RepoEntry{{Name: "gentoo", Location: repo}},
		Repository:   "gentoo", ConfigRoot: root, CPV: "dev-lang/python-3.13.7-r2", Slot: "3.13/3.13",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := PackageIdentity{
		Category: "dev-lang", PN: "python", PV: "3.13.7", PR: "r2",
		P: "python-3.13.7", PVR: "3.13.7-r2", PF: "python-3.13.7-r2",
		Slot: "3.13/3.13", Repository: "gentoo",
	}
	if got.Package != want {
		t.Fatalf("package identity = %#v, want %#v", got.Package, want)
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
	dirs := []string{filepath.Join(root, "target"), filepath.Join(root, "sysroot"), filepath.Join(root, "broot"), filepath.Join(root, "temp"), filepath.Join(root, "home"), filepath.Join(root, "logs", "cat:pkg:timestamp.log")}
	request := Request{Protocol: Version, ID: "roots-policy", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild}
	got, err := ApplyPackagePolicy(request, PackagePolicy{Repositories: []portage.RepoEntry{{Name: "gentoo", Location: repo}}, Repository: "gentoo", ConfigRoot: root, RootDir: dirs[0], SysrootDir: dirs[1], BrootDir: dirs[2], TempDir: dirs[3], HomeDir: dirs[4], LogFile: dirs[5]})
	if err != nil {
		t.Fatal(err)
	}
	if got.RootDir != dirs[0] || got.SysrootDir != dirs[1] || got.BrootDir != dirs[2] || got.TempDir != dirs[3] || got.HomeDir != dirs[4] || got.LogFile != dirs[5] {
		t.Fatalf("policy directory contract = %#v", got)
	}
}

func TestApplyPackagePolicyAuthorizesScratchPathsOutsideTmp(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "eclass"), 0o755); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "sandbox-paths", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: filepath.Join(root, "pkg.ebuild"), Env: map[string]string{"SANDBOX_WRITE": "/existing"}}
	policy := PackagePolicy{
		Repositories: []portage.RepoEntry{{Name: "gentoo", Location: repo}}, Repository: "gentoo",
		WorkDir: "/home/build/work", BuildDir: "/home/build/work", SourceDir: "/home/build/work/source",
		ImageDir: "/home/build/image", TempDir: "/home/build/temp", HomeDir: "/home/build/home", LogFile: "/home/build/build.log",
	}
	got, err := ApplyPackagePolicy(request, policy)
	if err != nil {
		t.Fatal(err)
	}
	want := "/existing:/home/build/work:/home/build/work/source:/home/build/image:/home/build/temp:/home/build/home:/home/build/build.log"
	if got.Env["SANDBOX_WRITE"] != want {
		t.Fatalf("SANDBOX_WRITE=%q want=%q", got.Env["SANDBOX_WRITE"], want)
	}
}

func TestEvaluateExecutionPolicyAppliesUseConditionalRestrictions(t *testing.T) {
	policy, err := EvaluateExecutionPolicy("sandbox network-sandbox ipc-sandbox pid-sandbox mount-sandbox test nostrip", "minimal? ( network-sandbox test ) !minimal? ( strip )", "", map[string]bool{"minimal": true})
	if err != nil {
		t.Fatal(err)
	}
	if !policy.Configured || !policy.Sandbox || policy.NetworkSandbox || !policy.IPCSandbox || !policy.PIDSandbox || !policy.MountSandbox || policy.Tests || policy.Strip {
		t.Fatalf("policy = %#v", policy)
	}
}

func TestEvaluateExecutionPolicyRejectsUnsupportedEnabledBehavior(t *testing.T) {
	for _, test := range []struct{ features, restrict, properties, want string }{
		{features: "unknown-feature", want: "FEATURE"},
		{restrict: "unknown-restrict", want: "RESTRICT"},
		{properties: "interactive", want: "PROPERTY"},
	} {
		if _, err := EvaluateExecutionPolicy(test.features, test.restrict, test.properties, nil); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("EvaluateExecutionPolicy(%q,%q,%q) error = %v", test.features, test.restrict, test.properties, err)
		}
	}
}

func TestEvaluateExecutionPolicyAcceptsControlPlaneFeatures(t *testing.T) {
	features := "assume-digests binpkg-docompress binpkg-dostrip binpkg-logs binpkg-multi-instance buildpkg-live compress-index config-protect-if-modified distlocks ebuild-locks merge-sync merge-wait news parallel-fetch parallel-install pkgdir-index-trusted unknown-features-warn unmerge-logs unmerge-orphans userfetch usersync"
	policy, err := EvaluateExecutionPolicy(features, "", "", nil)
	if err != nil {
		t.Fatalf("control-plane FEATURES: %v", err)
	}
	if !reflect.DeepEqual(policy.Features, strings.Fields(features)) {
		t.Fatalf("FEATURES=%v", policy.Features)
	}
}

func TestEvaluateExecutionPolicySeparatesUserprivAndUsersandbox(t *testing.T) {
	policy, err := EvaluateExecutionPolicy("sandbox userpriv -usersandbox", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !policy.UserPriv || policy.UserSandbox {
		t.Fatalf("policy = %+v", policy)
	}
}

func TestCredentialCommandRunsInsideNamespaceLauncher(t *testing.T) {
	executable, arguments := credentialCommand("/sbin/runuser", "portage", "/usr/bin/sandbox", []string{"/bin/bash", "-c", "true"})
	executable, arguments = namespaceCommand("/usr/bin/unshare", []string{"--net"}, executable, arguments)
	want := []string{"--net", "--", "/sbin/runuser", "-u", "portage", "--", "/usr/bin/sandbox", "/bin/bash", "-c", "true"}
	if executable != "/usr/bin/unshare" || !reflect.DeepEqual(arguments, want) {
		t.Fatalf("launcher = %s %q, want /usr/bin/unshare %q", executable, arguments, want)
	}
}
