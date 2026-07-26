package rebuild

import (
	"archive/tar"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/airencracken/arise/internal/ebuild"
	"github.com/airencracken/arise/internal/fetch"
	"github.com/airencracken/arise/internal/journal"
	mergepkg "github.com/airencracken/arise/internal/merge"
	"github.com/airencracken/arise/internal/phaseproto"
	"github.com/airencracken/arise/internal/portage"
)

type rebuildRoundTripFunc func(*http.Request) (*http.Response, error)

func (f rebuildRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestPhaseBrootMatchesNativePortagePrefixContract(t *testing.T) {
	if got := phaseBroot("/"); got != "" {
		t.Fatalf("native BROOT = %q, want empty", got)
	}
	if got := phaseBroot("/build-host"); got != "/build-host" {
		t.Fatalf("cross BROOT = %q, want /build-host", got)
	}
}

func TestUseFlagsWithArchSelectsImplicitArchitecture(t *testing.T) {
	got := useFlagsWithArch(map[string]bool{"ssl": true, "test": false}, "amd64")
	if !got["amd64"] || !got["ssl"] || got["test"] {
		t.Fatalf("architecture-aware USE = %#v", got)
	}
}

func TestFindEbuild(t *testing.T) {
	tmp := t.TempDir()
	pkgDir := filepath.Join(tmp, "sys-apps", "hello")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}

	files := []string{
		"hello-1.0.ebuild",
		"hello-1.1.ebuild",
		"hello-2.0.ebuild",
		"metadata.xml",
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(pkgDir, f), []byte("EAPI=8"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("exact version match", func(t *testing.T) {
		path, err := findEbuild(tmp, "sys-apps", "hello", "1.0")
		if err != nil {
			t.Fatalf("findEbuild: %v", err)
		}
		if filepath.Base(path) != "hello-1.0.ebuild" {
			t.Errorf("got %q, want hello-1.0.ebuild", filepath.Base(path))
		}
	})

	t.Run("missing version", func(t *testing.T) {
		_, err := findEbuild(tmp, "sys-apps", "hello", "9.9.9")
		if err == nil {
			t.Error("expected error for missing version, got nil")
		}
	})

	t.Run("missing package", func(t *testing.T) {
		_, err := findEbuild(tmp, "sys-apps", "nonexistent", "1.0")
		if err == nil {
			t.Error("expected error for missing package, got nil")
		}
	})
}

func TestAllowLifecycleRootWritesIsScopedAndDoesNotMutateBase(t *testing.T) {
	base := phaseproto.Request{RootDir: "/target", Env: map[string]string{"SANDBOX_WRITE": "/build", "USE": "test"}, Policy: phaseproto.ExecutionPolicy{Configured: true, Sandbox: true, NetworkSandbox: true, IPCSandbox: true, PIDSandbox: true, MountSandbox: true}}
	compile := applyPortageLifecyclePolicy(base, "src_compile")
	if compile.Env["SANDBOX_WRITE"] != "/build" {
		t.Fatalf("compile SANDBOX_WRITE=%q", compile.Env["SANDBOX_WRITE"])
	}
	if !compile.Policy.Sandbox {
		t.Fatal("src_compile unexpectedly disabled sandbox")
	}
	for _, phase := range []string{"pkg_preinst", "pkg_postinst", "pkg_prerm", "pkg_postrm"} {
		request := applyPortageLifecyclePolicy(base, phase)
		if request.Env["SANDBOX_WRITE"] != "/build:/target" {
			t.Fatalf("%s SANDBOX_WRITE=%q", phase, request.Env["SANDBOX_WRITE"])
		}
		if request.Policy.Sandbox {
			t.Fatalf("%s retained Portage sandbox", phase)
		}
		if request.Policy.NetworkSandbox || request.Policy.IPCSandbox || request.Policy.PIDSandbox {
			t.Fatalf("%s retained host-incompatible namespaces: %#v", phase, request.Policy)
		}
		if !request.Policy.MountSandbox {
			t.Fatalf("%s unexpectedly disabled Portage mount namespace", phase)
		}
	}
	for _, phase := range []string{"pkg_setup", "pkg_pretend"} {
		request := applyPortageLifecyclePolicy(base, phase)
		if request.Policy.Sandbox || request.Policy.NetworkSandbox || request.Policy.IPCSandbox {
			t.Fatalf("%s did not receive Portage free/host policy: %#v", phase, request.Policy)
		}
		if !request.Policy.PIDSandbox || !request.Policy.MountSandbox {
			t.Fatalf("%s lost retained namespaces: %#v", phase, request.Policy)
		}
	}
	if base.Env["SANDBOX_WRITE"] != "/build" {
		t.Fatalf("base request was mutated: %#v", base.Env)
	}
	if !base.Policy.Sandbox {
		t.Fatal("base policy was mutated")
	}
}

func TestApplyPortageUserprivPolicyByPhase(t *testing.T) {
	base := phaseproto.Request{Policy: phaseproto.ExecutionPolicy{Configured: true, Sandbox: true, UserPriv: true, UserSandbox: true}}
	for _, phase := range []string{"src_unpack", "src_prepare", "src_configure", "src_compile", "src_test"} {
		got := applyPortageLifecyclePolicy(base, phase)
		if !got.Policy.DropPrivileges || !got.Policy.Sandbox {
			t.Fatalf("%s policy = %+v", phase, got.Policy)
		}
	}
	for _, phase := range []string{"pkg_setup", "src_install", "pkg_preinst", "pkg_postinst", "pkg_config"} {
		got := applyPortageLifecyclePolicy(base, phase)
		if got.Policy.DropPrivileges {
			t.Fatalf("%s unexpectedly drops privileges: %+v", phase, got.Policy)
		}
	}
	config := applyPortageLifecyclePolicy(base, "pkg_config")
	if config.Policy.Sandbox || config.Policy.NetworkSandbox || config.Policy.IPCSandbox || config.Policy.PIDSandbox {
		t.Fatalf("pkg_config retained Portage namespaces: %+v", config.Policy)
	}
	withoutUserSandbox := base
	withoutUserSandbox.Policy.UserSandbox = false
	got := applyPortageLifecyclePolicy(withoutUserSandbox, "src_compile")
	if !got.Policy.DropPrivileges || got.Policy.Sandbox {
		t.Fatalf("userpriv without usersandbox policy = %+v", got.Policy)
	}
}

func TestInstalledLifecycleHasPhaseIsNilSafeAndExact(t *testing.T) {
	var nilLifecycle *InstalledLifecycle
	if nilLifecycle.HasPhase("pkg_config") {
		t.Fatal("nil lifecycle reported a phase")
	}
	lifecycle := &InstalledLifecycle{phases: map[string]bool{"pkg_config": true}}
	if !lifecycle.HasPhase("pkg_config") {
		t.Fatal("stored pkg_config phase was not reported")
	}
	if lifecycle.HasPhase("pkg_config_extra") || lifecycle.HasPhase("") {
		t.Fatal("nonexistent phase was reported")
	}
}

func TestEnsureWorkDirectoryRecreatesDeletedRuntimeState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deleted", "var", "tmp", "arise")
	if err := ensureWorkDirectory(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		t.Fatalf("work directory info=%v err=%v", info, err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("work directory mode = %o, want 755", info.Mode().Perm())
	}
	if err := ensureWorkDirectory(path); err != nil {
		t.Fatalf("idempotent ensure: %v", err)
	}
}

func TestEnsureWorkDirectoryRejectsUnsafeAndConflictingPaths(t *testing.T) {
	if err := ensureWorkDirectory(""); err == nil {
		t.Fatal("empty work path was accepted")
	}
	if err := ensureWorkDirectory("relative/work"); err == nil {
		t.Fatal("relative work path was accepted")
	}
	path := filepath.Join(t.TempDir(), "arise")
	if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureWorkDirectory(path); err == nil {
		t.Fatal("regular-file work path was accepted")
	}
}

func TestProtocolBuildPhasesKeepsPreinstOutOfBuildSandbox(t *testing.T) {
	phases := protocolBuildPhases(phaseproto.ExecutionPolicy{Configured: true})
	for _, phase := range phases {
		if phase == "pkg_preinst" {
			t.Fatalf("pkg_preinst inherited build-worker isolation: %v", phases)
		}
	}
	if got := phases[len(phases)-1]; got != "src_install" {
		t.Fatalf("last build phase=%q phases=%v", got, phases)
	}
}

func TestPhaseFailureDiagnosticsPreferCausalContextOverMakeCleanupTail(t *testing.T) {
	var events []phaseproto.Event
	for _, message := range []string{
		"checking compiler", "building generated source", "source.c:41: error: missing declaration", "compilation terminated",
		"make[4]: *** [source.o] Error 1", "make[4]: Leaving directory '/build/a'", "make[3]: Leaving directory '/build'",
		"make[2]: Leaving directory '/build'", "make[1]: Leaving directory '/build'", "make: Leaving directory '/build'",
	} {
		events = append(events, phaseproto.Event{Kind: "log", Message: message})
	}
	got := phaseFailureDiagnostics(events, 5)
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "missing declaration") {
		t.Fatalf("causal diagnostic missing: %q", joined)
	}
	if strings.Contains(joined, "make[1]: Leaving") {
		t.Fatalf("cleanup tail displaced causal context: %q", joined)
	}
}

func TestPhaseFailureDiagnosticsNamesSilentFailingPhase(t *testing.T) {
	events := []phaseproto.Event{
		{Kind: "phase", Message: "src_install"},
		{Kind: "log", Message: "make[1]: Leaving directory '/build'"},
	}
	got := phaseFailureDiagnostics(events, 5)
	if joined := strings.Join(got, "\n"); joined != "phase src_install returned non-zero status without an explicit error diagnostic" {
		t.Fatalf("silent diagnostic = %q", joined)
	}
}

func TestPhaseFailureDiagnosticsUsesFinalDieMessage(t *testing.T) {
	events := []phaseproto.Event{
		{Kind: "phase", Message: "src_compile"},
		{Kind: "log", Message: "go build ./cmd/arise"},
		{Kind: "log", Message: "supported build must produce a statically linked binary"},
	}
	got := phaseFailureDiagnostics(events, 5)
	if joined := strings.Join(got, "\n"); joined != "supported build must produce a statically linked binary" {
		t.Fatalf("die diagnostic = %q", joined)
	}
}

func TestRegenerateLiveInfoIndexReportsAllFailures(t *testing.T) {
	base := t.TempDir()
	root, image := filepath.Join(base, "root"), filepath.Join(base, "image")
	for _, directory := range []string{filepath.Join(root, "usr/share/info"), filepath.Join(image, "usr/share/info")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"one.info", "two.info"} {
		if err := os.WriteFile(filepath.Join(root, "usr/share/info", name), []byte("manual"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(image, "usr/share/info", name), []byte("manual"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	installer := filepath.Join(base, "install-info")
	if err := os.WriteFile(installer, []byte("#!/bin/sh\necho broken >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	result := regenerateLiveInfoIndexReport(root, image, true, installer)
	if result.Processed != 2 || len(result.Errors) != 2 {
		t.Fatalf("result=%+v", result)
	}
}

func TestRegenerateLiveInfoIndexReplacesGeneratedVariants(t *testing.T) {
	base := t.TempDir()
	root, image := filepath.Join(base, "root"), filepath.Join(base, "image")
	installedInfo, stagedInfo := filepath.Join(root, "usr", "share", "info"), filepath.Join(image, "usr", "share", "info")
	for _, directory := range []string{installedInfo, stagedInfo} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{filepath.Join(installedInfo, "manual.info.gz"), filepath.Join(stagedInfo, "manual.info.gz"), filepath.Join(installedInfo, "dir.gz")} {
		if err := os.WriteFile(path, []byte("manual"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("manual.info", filepath.Join(installedInfo, "manual-ref.info")); err != nil {
		t.Fatal(err)
	}
	installer := filepath.Join(base, "install-info")
	calls := filepath.Join(base, "calls")
	script := fmt.Sprintf("#!/bin/sh\nindex=${1#--dir-file=}\nprintf '%%s\\n' \"$2\" >> %q\nprintf regenerated > \"$index\"\n", calls)
	if err := os.WriteFile(installer, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := regenerateLiveInfoIndex(root, image, true, installer); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(installedInfo, "dir")); err != nil || string(raw) != "regenerated" {
		t.Fatalf("regenerated index = %q, %v", raw, err)
	}
	if _, err := os.Lstat(filepath.Join(installedInfo, "dir.gz")); !os.IsNotExist(err) {
		t.Fatalf("old compressed index remains: %v", err)
	}
	invocations, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(invocations), "manual-ref.info") {
		t.Fatalf("dangling compatibility symlink passed to install-info: %s", invocations)
	}
}

func TestPreflightHasVersionQueriesUsesInstalledVDB(t *testing.T) {
	tmp := t.TempDir()
	repo, vdb := filepath.Join(tmp, "repo"), filepath.Join(tmp, "vdb")
	eclassDir := filepath.Join(repo, "eclass")
	installed := filepath.Join(vdb, "dev-build", "cmake-4.2.0")
	for _, directory := range []string{eclassDir, installed} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	eclass := `probe() {
  has_version -b "<dev-build/cmake-4.2.1"
  has_version -b ">=dev-build/cmake-4"
}`
	if err := os.WriteFile(filepath.Join(eclassDir, "probe.eclass"), []byte(eclass), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"SLOT": "0\n", "repository": "gentoo\n"} {
		if err := os.WriteFile(filepath.Join(installed, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ebuildPath := filepath.Join(tmp, "pkg-1.ebuild")
	if err := os.WriteFile(ebuildPath, []byte("EAPI=8\ninherit probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eb, err := ebuild.ParseEbuild(ebuildPath)
	if err != nil {
		t.Fatal(err)
	}
	answers, err := preflightHasVersionQueries(ebuildPath, eb, []portage.RepoEntry{{Name: "gentoo", Location: repo}}, "gentoo", t.TempDir(), vdb, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"<dev-build/cmake-4.2.1", ">=dev-build/cmake-4"} {
		if !answers[query] {
			t.Fatalf("has_version %s = false, answers=%v", query, answers)
		}
	}
}

func TestPreflightBestVersionsSelectsHighestInstalledCPVByDomain(t *testing.T) {
	rootVDB, brootVDB := filepath.Join(t.TempDir(), "root"), filepath.Join(t.TempDir(), "broot")
	for _, directory := range []string{
		filepath.Join(rootVDB, "dev-python", "gpep517-18"),
		filepath.Join(brootVDB, "dev-python", "gpep517-18"),
		filepath.Join(brootVDB, "dev-python", "gpep517-19"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := preflightBestVersions(rootVDB, brootVDB)
	if got["r\tdev-python/gpep517"] != "dev-python/gpep517-18" {
		t.Fatalf("root best version = %q", got["r\tdev-python/gpep517"])
	}
	if got["b\tdev-python/gpep517"] != "dev-python/gpep517-19" {
		t.Fatalf("build-root best version = %q", got["b\tdev-python/gpep517"])
	}
}

func TestPreflightHasVersionQueriesExpandsEbuildVariables(t *testing.T) {
	tmp := t.TempDir()
	repo, vdb := filepath.Join(tmp, "repo"), filepath.Join(tmp, "vdb")
	installed := filepath.Join(vdb, "dev-lang", "go-1.25.0")
	if err := os.MkdirAll(installed, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"SLOT": "0\n", "repository": "gentoo\n"} {
		if err := os.WriteFile(filepath.Join(installed, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ebuildPath := filepath.Join(tmp, "go-1.26.ebuild")
	content := "EAPI=8\nGO_BOOTSTRAP_MIN=1.24.6\nsrc_compile() { has_version -b \">=dev-lang/go-${GO_BOOTSTRAP_MIN}\"; has_version -b \">=dev-lang/go-bootstrap-${GO_BOOTSTRAP_MIN}\"; }\n"
	if err := os.WriteFile(ebuildPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	eb, err := ebuild.ParseEbuild(ebuildPath)
	if err != nil {
		t.Fatal(err)
	}
	answers, err := preflightHasVersionQueries(ebuildPath, eb, []portage.RepoEntry{{Name: "gentoo", Location: repo}}, "gentoo", t.TempDir(), vdb, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !answers[">=dev-lang/go-1.24.6"] {
		t.Fatalf("expanded Go query not satisfied: %v", answers)
	}
	if answer, exists := answers[">=dev-lang/go-bootstrap-1.24.6"]; !exists || answer {
		t.Fatalf("expanded bootstrap query = %t, exists=%t; answers=%v", answer, exists, answers)
	}
}

func TestPreflightHasVersionQueriesExpandsDerivedPVInEclass(t *testing.T) {
	tmp := t.TempDir()
	repo, vdb := filepath.Join(tmp, "repo"), filepath.Join(tmp, "vdb")
	eclassDir := filepath.Join(repo, "eclass")
	installed := filepath.Join(vdb, "dev-qt", "qtdeclarative-6.11.1")
	for _, directory := range []string{eclassDir, installed} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(eclassDir, "qt6-build.eclass"), []byte(`probe() { has_version -d "~dev-qt/qtdeclarative-${PV}"; }`), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"SLOT": "6/6.11.1\n", "repository": "gentoo\n"} {
		if err := os.WriteFile(filepath.Join(installed, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ebuildPath := filepath.Join(tmp, "repo", "dev-qt", "qt5compat", "qt5compat-6.11.1.ebuild")
	if err := os.MkdirAll(filepath.Dir(ebuildPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ebuildPath, []byte("EAPI=8\ninherit qt6-build\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eb, err := ebuild.ParseEbuild(ebuildPath)
	if err != nil {
		t.Fatal(err)
	}
	answers, err := preflightHasVersionQueries(ebuildPath, eb, []portage.RepoEntry{{Name: "gentoo", Location: repo}}, "gentoo", vdb, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !answers["~dev-qt/qtdeclarative-6.11.1"] {
		t.Fatalf("derived PV query not satisfied: %v", answers)
	}
}

func TestPreflightHasVersionQueriesEnumeratesRustEclassSlots(t *testing.T) {
	tmp := t.TempDir()
	repo, brootVDB := filepath.Join(tmp, "repo"), filepath.Join(tmp, "broot-vdb")
	eclassDir := filepath.Join(repo, "eclass")
	installed := filepath.Join(brootVDB, "dev-lang", "rust-bin-1.94.1")
	for _, directory := range []string{eclassDir, installed} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rustEclass := `declare -a -g -r _RUST_SLOTS_ORDERED=(
	"9999"
	"1.95.0"
	"1.94.1"
)
_get_rust_slot() {
	local slot
	for slot in "${_RUST_SLOTS_ORDERED[@]}"; do
		has_version -b "dev-lang/rust:${slot}"
		has_version -b "dev-lang/rust-bin:${slot}"
	done
}`
	if err := os.WriteFile(filepath.Join(eclassDir, "rust.eclass"), []byte(rustEclass), 0o644); err != nil {
		t.Fatal(err)
	}
	cargoEclass := `case ${EAPI} in
	8) ;;
	*) die "unsupported" ;;
esac
if [[ -n ${CRATE_PATHS_OVERRIDE} ]]; then
	CRATES="${CRATES} ${CRATE_PATHS_OVERRIDE}"
fi
inherit rust
`
	if err := os.WriteFile(filepath.Join(eclassDir, "cargo.eclass"), []byte(cargoEclass), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"SLOT": "1.94.1\n", "repository": "gentoo\n"} {
		if err := os.WriteFile(filepath.Join(installed, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ebuildPath := filepath.Join(tmp, "cbindgen-1.ebuild")
	if err := os.WriteFile(ebuildPath, []byte("EAPI=8\nif use rust; then\n\tinherit cargo\nfi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eb, err := ebuild.ParseEbuild(ebuildPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(eb.Inherit) != 0 {
		t.Fatalf("test requires a conditional inherit omitted by the metadata parser: %v", eb.Inherit)
	}
	answers, err := preflightHasVersionQueries(ebuildPath, eb, []portage.RepoEntry{{Name: "gentoo", Location: repo}}, "gentoo", t.TempDir(), brootVDB, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !answers["dev-lang/rust-bin:1.94.1"] {
		t.Fatalf("installed Rust slot missing from snapshot: %v", answers)
	}
	for _, query := range []string{"dev-lang/rust:9999", "dev-lang/rust-bin:1.95.0"} {
		if answer, exists := answers[query]; !exists || answer {
			t.Fatalf("query %q answer=%t exists=%t, snapshot=%v", query, answer, exists, answers)
		}
	}
}

func TestPreflightHasVersionQueriesIncludesAbsentPythonCompatSlots(t *testing.T) {
	tmp := t.TempDir()
	repo, vdb := filepath.Join(tmp, "repo"), filepath.Join(tmp, "vdb")
	eclassDir := filepath.Join(repo, "eclass")
	installed := filepath.Join(vdb, "dev-lang", "python-3.14.9")
	for _, directory := range []string{eclassDir, installed} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(eclassDir, "distutils-r1.eclass"), []byte("# transitively inherits python-r1 in the real repository\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"SLOT": "3.14\n", "repository": "gentoo\n"} {
		if err := os.WriteFile(filepath.Join(installed, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ebuildPath := filepath.Join(tmp, "pkg-1.ebuild")
	if err := os.WriteFile(ebuildPath, []byte("EAPI=8\nPYTHON_COMPAT=( python3_{12..15} python3_{14..15}t )\nPYTHON_REQ_USE=\"threads(+)\"\ninherit distutils-r1\npython_check_deps() { python_has_version \"dev-python/setuptools[${PYTHON_USEDEP}]\"; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eb, err := ebuild.ParseEbuild(ebuildPath)
	if err != nil {
		t.Fatal(err)
	}
	answers, err := preflightHasVersionQueries(ebuildPath, eb, []portage.RepoEntry{{Name: "gentoo", Location: repo}}, "gentoo", t.TempDir(), vdb, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !answers["dev-lang/python:3.14"] {
		t.Fatalf("installed Python slot missing from snapshot: %v", answers)
	}
	if answer, exists := answers["dev-lang/python:3.15"]; !exists || answer {
		t.Fatalf("absent Python slot answer=%t exists=%t, snapshot=%v", answer, exists, answers)
	}
	if !answers["dev-lang/python:3.14[threads(+)]"] {
		t.Fatalf("installed qualified Python slot missing from snapshot: %v", answers)
	}
	if answer, exists := answers["dev-lang/python:3.15[threads(+)]"]; !exists || answer {
		t.Fatalf("absent qualified Python slot answer=%t exists=%t, snapshot=%v", answer, exists, answers)
	}
	for _, minor := range []string{"12", "13", "14", "15"} {
		query := "dev-python/setuptools[python_targets_python3_" + minor + "(-)]"
		if _, exists := answers[query]; !exists {
			t.Fatalf("dynamic python_check_deps query %q missing from snapshot: %v", query, answers)
		}
	}
}

func TestPreflightHasVersionQueriesFollowsTransitivePythonEclass(t *testing.T) {
	tmp := t.TempDir()
	repo, vdb := filepath.Join(tmp, "repo"), filepath.Join(tmp, "vdb")
	eclassDir := filepath.Join(repo, "eclass")
	installed := filepath.Join(vdb, "dev-lang", "python-3.14.9")
	for _, directory := range []string{eclassDir, installed} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(eclassDir, "gstreamer-meson.eclass"), []byte("PYTHON_COMPAT=( python3_{12..14} )\ninherit python-any-r1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(eclassDir, "python-any-r1.eclass"), []byte("inherit python-utils-r1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(eclassDir, "python-utils-r1.eclass"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"SLOT": "3.14\n", "repository": "gentoo\n"} {
		if err := os.WriteFile(filepath.Join(installed, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ebuildPath := filepath.Join(tmp, "gstreamer-1.ebuild")
	if err := os.WriteFile(ebuildPath, []byte("EAPI=8\ninherit gstreamer-meson\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eb, err := ebuild.ParseEbuild(ebuildPath)
	if err != nil {
		t.Fatal(err)
	}
	answers, err := preflightHasVersionQueries(ebuildPath, eb, []portage.RepoEntry{{Name: "gentoo", Location: repo}}, "gentoo", t.TempDir(), vdb, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"dev-lang/python:3.12", "dev-lang/python:3.13", "dev-lang/python:3.14"} {
		if _, exists := answers[query]; !exists {
			t.Fatalf("transitive Python query %q missing from snapshot: %v", query, answers)
		}
	}
}

func TestDynamicPythonQueriesExpandsPlainBraceRange(t *testing.T) {
	got := dynamicPythonQueries("PYTHON_COMPAT=( python3_{11..14} )\n")
	want := []string{
		"dev-lang/python:3.11",
		"dev-lang/python:3.12",
		"dev-lang/python:3.13",
		"dev-lang/python:3.14",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("plain Python brace range = %v, want %v", got, want)
	}
}

func TestDynamicPythonUseDepQueriesExpandsArbitraryPackageAtoms(t *testing.T) {
	source := `python_check_deps() {
	python_has_version "dev-python/setuptools[${PYTHON_USEDEP}]" &&
		python_has_version 'dev-python/installer[${PYTHON_SINGLE_USEDEP}]'
}`
	got := dynamicPythonUseDepQueries(source, []string{"3.13", "3.14"})
	want := []string{
		"dev-python/installer[python_single_target_python3_13(-)]",
		"dev-python/installer[python_single_target_python3_14(-)]",
		"dev-python/setuptools[python_targets_python3_13(-)]",
		"dev-python/setuptools[python_targets_python3_14(-)]",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("dynamic Python USE-dependency queries = %v, want %v", got, want)
	}
}

func TestShellArrayValuesSupportsLiteralAndDescendingBraceSlots(t *testing.T) {
	source := `declare -g -r _LLVM_KNOWN_SLOTS=( {22..20} )
declare -a -g -r _RUST_SLOTS_ORDERED=( "9999" "1.95.0" "1.94.1" )`
	if got, want := shellArrayValues(source, "_LLVM_KNOWN_SLOTS"), []string{"20", "21", "22"}; !slices.Equal(got, want) {
		t.Fatalf("LLVM slots = %v, want %v", got, want)
	}
	if got, want := shellArrayValues(source, "_RUST_SLOTS_ORDERED"), []string{"1.94.1", "1.95.0", "9999"}; !slices.Equal(got, want) {
		t.Fatalf("Rust slots = %v, want %v", got, want)
	}
}

func TestFindEbuild_EmptyDir(t *testing.T) {
	tmp := t.TempDir()
	pkgDir := filepath.Join(tmp, "cat", "pkg")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}

	_, err := findEbuild(tmp, "cat", "pkg", "1.0")
	if err == nil {
		t.Error("expected error for empty directory, got nil")
	}
}

func TestResolveURIs(t *testing.T) {
	vars := map[string]string{
		"PN":   "hello",
		"PV":   "1.0",
		"P":    "hello-1.0",
		"MY_P": "Hello-v1.0",
	}

	uris := []string{
		"https://example.com/${P}.tar.gz",
		"mirror://gentoo/${MY_P}.xz",
	}

	resolved := resolveURIs(uris, vars)
	if len(resolved) != 2 {
		t.Fatalf("expected 2 URIs, got %d", len(resolved))
	}
	if resolved[0] != "https://example.com/hello-1.0.tar.gz" {
		t.Errorf("URI[0] = %q, want %q", resolved[0], "https://example.com/hello-1.0.tar.gz")
	}
	if resolved[1] != "mirror://gentoo/Hello-v1.0.xz" {
		t.Errorf("URI[1] = %q, want %q", resolved[1], "mirror://gentoo/Hello-v1.0.xz")
	}
}

func TestResolveURIs_Empty(t *testing.T) {
	resolved := resolveURIs(nil, nil)
	if len(resolved) != 0 {
		t.Errorf("expected 0 URIs, got %d", len(resolved))
	}
}

func TestRebuildPackage_MissingEbuild(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	cfg := RebuildConfig{
		RepoDir:      tmp,
		DistfilesDir: filepath.Join(tmp, "distfiles"),
		RootDir:      filepath.Join(tmp, "root"),
		VdbDir:       filepath.Join(tmp, "vdb"),
		WorkDirBase:  filepath.Join(tmp, "work"),
	}

	for _, d := range []string{cfg.DistfilesDir, cfg.RootDir, cfg.VdbDir, cfg.WorkDirBase} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	err := RebuildPackage(ctx, "nonexistent/pkg-1.0", &cfg)
	if err == nil {
		t.Error("expected error for missing ebuild, got nil")
	}
}

func TestRebuildPackage_InvalidAtom(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	cfg := RebuildConfig{
		WorkDirBase: filepath.Join(tmp, "work"),
	}
	if err := os.MkdirAll(cfg.WorkDirBase, 0755); err != nil {
		t.Fatal(err)
	}

	invalidAtoms := []string{
		"",
		"not-an-atom",
		"missing-category/",
		"/missing-package-1.0",
	}

	for _, a := range invalidAtoms {
		t.Run("atom="+a, func(t *testing.T) {
			err := RebuildPackage(ctx, a, &cfg)
			if err == nil {
				t.Errorf("expected error for invalid atom %q, got nil", a)
			}
		})
	}
}

func TestRebuildPackage_NoVersion(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	cfg := RebuildConfig{
		WorkDirBase: filepath.Join(tmp, "work"),
	}
	if err := os.MkdirAll(cfg.WorkDirBase, 0755); err != nil {
		t.Fatal(err)
	}

	err := RebuildPackage(ctx, "sys-apps/hello", &cfg)
	if err == nil {
		t.Error("expected error for atom without version, got nil")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("error should mention version: %v", err)
	}
}

func TestRebuildPackage_ContextCancellation(t *testing.T) {
	tmp := t.TempDir()

	pkgDir := filepath.Join(tmp, "sys-apps", "hello")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "hello-1.0.ebuild"), []byte("EAPI=8"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := RebuildConfig{
		RepoDir:      tmp,
		DistfilesDir: filepath.Join(tmp, "distfiles"),
		RootDir:      filepath.Join(tmp, "root"),
		VdbDir:       filepath.Join(tmp, "vdb"),
		WorkDirBase:  filepath.Join(tmp, "work"),
	}

	for _, d := range []string{cfg.DistfilesDir, cfg.RootDir, cfg.VdbDir, cfg.WorkDirBase} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := RebuildPackage(ctx, "sys-apps/hello-1.0", &cfg)
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
}

func TestRebuildPackages(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	pkgDir := filepath.Join(tmp, "sys-apps", "hello")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"hello-1.0.ebuild", "hello-1.1.ebuild"} {
		if err := os.WriteFile(filepath.Join(pkgDir, f), []byte("EAPI=8"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := RebuildConfig{
		RepoDir:      tmp,
		DistfilesDir: filepath.Join(tmp, "distfiles"),
		RootDir:      filepath.Join(tmp, "root"),
		VdbDir:       filepath.Join(tmp, "vdb"),
		WorkDirBase:  filepath.Join(tmp, "work"),
	}

	for _, d := range []string{cfg.DistfilesDir, cfg.RootDir, cfg.VdbDir, cfg.WorkDirBase} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	atoms := []string{
		"sys-apps/hello-1.0",
		"sys-apps/hello-1.1",
		"sys-apps/nonexistent-1.0",
	}

	err := RebuildPackages(ctx, atoms, &cfg)
	if err == nil {
		t.Error("expected error due to nonexistent package, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention nonexistent: %v", err)
	}
}

func TestRebuildPackages_Empty(t *testing.T) {
	err := RebuildPackages(context.Background(), nil, &RebuildConfig{})
	if err != nil {
		t.Errorf("expected nil for empty atoms, got %v", err)
	}
}

func TestRebuildPackagesParallel_Basic(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	pkgDir := filepath.Join(tmp, "sys-apps", "hello")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"hello-1.0.ebuild", "hello-1.1.ebuild"} {
		if err := os.WriteFile(filepath.Join(pkgDir, f), []byte("EAPI=8"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := RebuildConfig{
		RepoDir:      tmp,
		DistfilesDir: filepath.Join(tmp, "distfiles"),
		RootDir:      filepath.Join(tmp, "root"),
		VdbDir:       filepath.Join(tmp, "vdb"),
		WorkDirBase:  filepath.Join(tmp, "work"),
	}

	for _, d := range []string{cfg.DistfilesDir, cfg.RootDir, cfg.VdbDir, cfg.WorkDirBase} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	runParallelTest := func(t *testing.T, jobs int) {
		atoms := []string{
			"sys-apps/hello-1.0",
			"sys-apps/hello-1.1",
			"sys-apps/nonexistent-1.0",
		}

		err := RebuildPackagesParallel(ctx, atoms, &cfg, jobs)
		if err == nil {
			t.Error("expected error due to nonexistent package, got nil")
		}
		if !strings.Contains(err.Error(), "nonexistent") {
			t.Errorf("error should mention nonexistent: %v", err)
		}
	}

	t.Run("workers=2", func(t *testing.T) { runParallelTest(t, 2) })
	t.Run("workers=4", func(t *testing.T) { runParallelTest(t, 4) })
	t.Run("workers=8", func(t *testing.T) { runParallelTest(t, 8) })
}

func TestRebuildPackagesParallel_Empty(t *testing.T) {
	err := RebuildPackagesParallel(context.Background(), nil, &RebuildConfig{}, 4)
	if err != nil {
		t.Errorf("expected nil for empty atoms, got %v", err)
	}
	err = RebuildPackagesParallel(context.Background(), []string{}, &RebuildConfig{}, 4)
	if err != nil {
		t.Errorf("expected nil for empty atoms, got %v", err)
	}
}

func TestRebuildPackagesParallel_ContextCancellation(t *testing.T) {
	tmp := t.TempDir()

	pkgDir := filepath.Join(tmp, "sys-apps", "hello")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "hello-1.0.ebuild"), []byte("EAPI=8"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := RebuildConfig{
		RepoDir:      tmp,
		DistfilesDir: filepath.Join(tmp, "distfiles"),
		RootDir:      filepath.Join(tmp, "root"),
		VdbDir:       filepath.Join(tmp, "vdb"),
		WorkDirBase:  filepath.Join(tmp, "work"),
	}

	for _, d := range []string{cfg.DistfilesDir, cfg.RootDir, cfg.VdbDir, cfg.WorkDirBase} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	atoms := []string{"sys-apps/hello-1.0", "sys-apps/hello-1.1"}
	err := RebuildPackagesParallel(ctx, atoms, &cfg, 4)
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
}

func TestWaitForLoad(t *testing.T) {
	// maxLoad <= 0 should return immediately
	if err := WaitForLoad(0); err != nil {
		t.Errorf("WaitForLoad(0) should not error: %v", err)
	}
	if err := WaitForLoad(-1); err != nil {
		t.Errorf("WaitForLoad(-1) should not error: %v", err)
	}
}

func TestWaitForLoad_WithHighThreshold(t *testing.T) {
	// With a very high threshold, should pass immediately on a normal system
	err := WaitForLoad(9999.0)
	if err != nil {
		t.Errorf("WaitForLoad(9999) should not error on normal system: %v", err)
	}
}

func TestWithLoadControl(t *testing.T) {
	ctx := WithLoadControl(context.Background(), 0)
	if ctx == nil {
		t.Fatal("expected non-nil context")
	}

	// maxLoad <= 0 should return original context, not LoadControlContext
	lc := LoadControlFromContext(ctx)
	if lc != nil {
		t.Error("expected nil LoadControlContext for maxLoad <= 0")
	}

	ctx2 := WithLoadControl(context.Background(), 2.5)
	lc2 := LoadControlFromContext(ctx2)
	if lc2 == nil {
		t.Fatal("expected non-nil LoadControlContext for maxLoad > 0")
	}
	if lc2.MaxLoad != 2.5 {
		t.Errorf("expected MaxLoad=2.5, got %f", lc2.MaxLoad)
	}
}

func TestLoadControlContext_Wait(t *testing.T) {
	ctx := WithLoadControl(context.Background(), 9999.0)
	lc := LoadControlFromContext(ctx)
	if lc == nil {
		t.Fatal("expected non-nil LoadControlContext")
	}
	if err := lc.Wait(); err != nil {
		t.Errorf("Wait() with high threshold should not error: %v", err)
	}
}

func TestRebuildPackagesParallel_ContinuesOnError(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	repoDir := filepath.Join(tmp, "repo")
	vdbDir := filepath.Join(tmp, "vdb")
	rootDir := filepath.Join(tmp, "root")
	distDir := filepath.Join(tmp, "distfiles")
	workDir := filepath.Join(tmp, "work")

	for _, d := range []string{repoDir, vdbDir, rootDir, distDir, workDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	goodDir := filepath.Join(repoDir, "app-good", "good")
	if err := os.MkdirAll(goodDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goodDir, "good-1.0.ebuild"), []byte("EAPI=8\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var erroredPkgs []string
	var mu sync.Mutex
	cfg := RebuildConfig{
		RepoDir:      repoDir,
		DistfilesDir: distDir,
		RootDir:      rootDir,
		VdbDir:       vdbDir,
		WorkDirBase:  workDir,
		OnError: func(pkg string, err error) {
			mu.Lock()
			erroredPkgs = append(erroredPkgs, pkg)
			mu.Unlock()
		},
	}

	atoms := []string{
		"app-bad/bad-1.0",
		"app-good/good-1.0",
		"app-bad/missing-1.0",
	}

	err := RebuildPackagesParallel(ctx, atoms, &cfg, 4)
	if err == nil {
		t.Error("expected error from RebuildPackagesParallel with failing atoms")
	}

	mu.Lock()
	count := len(erroredPkgs)
	mu.Unlock()
	if count < 2 {
		t.Errorf("expected at least 2 errored packages, got %d: %v", count, erroredPkgs)
	}

	contentsPath := filepath.Join(vdbDir, "app-good", "good-1.0", "CONTENTS")
	if _, err := os.Stat(contentsPath); err != nil {
		t.Errorf("CONTENTS for good package should exist: %v", err)
	}
}

func TestRebuildPackagesParallel_SingleWorker(t *testing.T) {
	tmp := t.TempDir()

	pkgDir := filepath.Join(tmp, "sys-apps", "hello")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "hello-1.0.ebuild"), []byte("EAPI=8"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := RebuildConfig{
		RepoDir:      tmp,
		DistfilesDir: filepath.Join(tmp, "distfiles"),
		RootDir:      filepath.Join(tmp, "root"),
		VdbDir:       filepath.Join(tmp, "vdb"),
		WorkDirBase:  filepath.Join(tmp, "work"),
	}

	for _, d := range []string{cfg.DistfilesDir, cfg.RootDir, cfg.VdbDir, cfg.WorkDirBase} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	atoms := []string{"sys-apps/hello-1.0"}
	err := RebuildPackagesParallel(context.Background(), atoms, &cfg, 1)
	if err != nil {
		t.Errorf("RebuildPackagesParallel with 1 worker failed: %v", err)
	}
}

func TestProgressCallbacks(t *testing.T) {
	tmp := t.TempDir()

	pkgDir := filepath.Join(tmp, "sys-apps", "hello")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "hello-1.0.ebuild"), []byte("EAPI=8"), 0644); err != nil {
		t.Fatal(err)
	}

	var phaseStarts []string
	var phaseEnds []string
	var errors []string

	cfg := RebuildConfig{
		RepoDir:      tmp,
		DistfilesDir: filepath.Join(tmp, "distfiles"),
		RootDir:      filepath.Join(tmp, "root"),
		VdbDir:       filepath.Join(tmp, "vdb"),
		WorkDirBase:  filepath.Join(tmp, "work"),

		OnPhaseStart: func(phase string) {
			phaseStarts = append(phaseStarts, phase)
		},
		OnPhaseEnd: func(phase string, err error) {
			phaseEnds = append(phaseEnds, phase)
		},
		OnError: func(pkg string, err error) {
			errors = append(errors, pkg)
		},
	}

	for _, d := range []string{cfg.DistfilesDir, cfg.RootDir, cfg.VdbDir, cfg.WorkDirBase} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	err := RebuildPackage(context.Background(), "sys-apps/hello-1.0", &cfg)
	if err != nil {
		t.Fatalf("RebuildPackage: %v", err)
	}

	if len(phaseStarts) == 0 {
		t.Error("OnPhaseStart was never called")
	}
	if len(phaseEnds) == 0 {
		t.Error("OnPhaseEnd was never called")
	}

	for i, start := range phaseStarts {
		if i < len(phaseEnds) && phaseEnds[i] != start {
			t.Errorf("phase start/end mismatch: start=%s end=%s", start, phaseEnds[i])
		}
	}
}

func TestRebuildPackage_EndToEnd(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	repoDir := filepath.Join(tmp, "repo")
	distDir := filepath.Join(tmp, "distfiles")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(tmp, "vdb")
	workDirBase := filepath.Join(tmp, "work")

	for _, d := range []string{repoDir, distDir, rootDir, vdbDir, workDirBase} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	ebuildDir := filepath.Join(repoDir, "app-misc", "gmtest")
	if err := os.MkdirAll(ebuildDir, 0755); err != nil {
		t.Fatal(err)
	}

	// No SRC_URI to avoid fetch issues; this tests the orchestration
	ebuildContent := `EAPI=8
DESCRIPTION="GM test package"
`
	if err := os.WriteFile(filepath.Join(ebuildDir, "gmtest-1.0.ebuild"), []byte(ebuildContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := RebuildConfig{
		RepoDir:      repoDir,
		DistfilesDir: distDir,
		RootDir:      rootDir,
		VdbDir:       vdbDir,
		WorkDirBase:  workDirBase,
		CFLAGS:       "-O2 -pipe",
		CXXFLAGS:     "-O2 -pipe",
		MAKEOPTS:     "-j4",
		Arch:         "amd64",

		OnPhaseStart: func(phase string) {},
		OnPhaseEnd:   func(phase string, err error) {},
		OnError:      func(pkg string, err error) {},
	}

	err := RebuildPackage(ctx, "app-misc/gmtest-1.0", &cfg)
	if err != nil {
		t.Fatalf("RebuildPackage failed: %v", err)
	}

	contentsPath := filepath.Join(vdbDir, "app-misc", "gmtest-1.0", "CONTENTS")
	if _, err := os.Stat(contentsPath); err != nil {
		t.Errorf("CONTENTS file not found: %v", err)
	}
}

func TestRebuildPackagePhaseProtocolIntoDisposableRoot(t *testing.T) {
	if _, err := exec.LookPath("sandbox"); err != nil {
		t.Skip("Portage sandbox is not installed")
	}
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	root := filepath.Join(tmp, "root")
	vdb := filepath.Join(root, "var", "db", "pkg")
	work := filepath.Join(tmp, "work")
	dist := filepath.Join(tmp, "distfiles")
	packageDir := filepath.Join(repo, "app-misc", "protocol-test")
	for _, directory := range []string{filepath.Join(repo, "eclass"), packageDir, root, vdb, work, dist} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ebuildContent := `EAPI=8
S="${WORKDIR}/${P}"
src_unpack() { mkdir -p "${S}"; printf 'protocol image\n' > "${S}/payload"; }
src_install() { insinto /usr/share/protocol-test; doins payload; }
pkg_postinst() { printf 'postinst\n' > "${ROOT}/postinst-marker"; }
`
	if err := os.WriteFile(filepath.Join(packageDir, "protocol-test-1.ebuild"), []byte(ebuildContent), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := RebuildConfig{
		RepoDir: repo, DistfilesDir: dist, RootDir: root, VdbDir: vdb, WorkDirBase: work,
		PhaseProtocol: true, Repository: "test", Repositories: []portage.RepoEntry{{Name: "test", Location: repo}},
	}
	if err := RebuildPackage(context.Background(), "app-misc/protocol-test-1", &cfg); err != nil {
		t.Fatalf("protocol rebuild: %v", err)
	}
	for _, path := range []string{
		filepath.Join(root, "usr", "share", "protocol-test", "payload"),
		filepath.Join(root, "postinst-marker"),
		filepath.Join(vdb, "app-misc", "protocol-test-1", "CONTENTS"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected disposable-root result %s: %v", path, err)
		}
	}
	vdbEntry := filepath.Join(vdb, "app-misc", "protocol-test-1")
	for name, want := range map[string]string{"CATEGORY": "app-misc", "PF": "protocol-test-1", "EAPI": "8", "repository": "test"} {
		data, err := os.ReadFile(filepath.Join(vdbEntry, name))
		if err != nil || strings.TrimSpace(string(data)) != want {
			t.Errorf("VDB %s=%q err=%v", name, data, err)
		}
	}
	if _, err := os.Stat(filepath.Join(vdbEntry, "protocol-test-1.ebuild")); err != nil {
		t.Errorf("VDB ebuild missing: %v", err)
	}
	compressed, err := os.Open(filepath.Join(vdbEntry, "environment.bz2"))
	if err != nil {
		t.Fatalf("VDB environment missing: %v", err)
	}
	environment, err := io.ReadAll(bzip2.NewReader(compressed))
	compressed.Close()
	if err != nil || !strings.Contains(string(environment), "export PF='protocol-test-1'") || !strings.Contains(string(environment), "export ROOT='") {
		t.Fatalf("VDB environment=%q err=%v", environment, err)
	}
}

func TestMaterializeInstalledEnvironmentRemovesStaleIsolationControls(t *testing.T) {
	if _, err := exec.LookPath("bzip2"); err != nil {
		t.Skip("bzip2 is not installed")
	}
	vdb := t.TempDir()
	plain := filepath.Join(t.TempDir(), "environment")
	content := "export EAPI='8'\n" +
		"export SANDBOX_WRITE='/stale/build'\n" +
		"declare -x SANDBOX_READ='/stale/read'\n" +
		"export LD_PRELOAD='/stale/libsandbox.so'\n" +
		"export FEATURES='sandbox'\n"
	if err := os.WriteFile(plain, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	compressed, err := os.OpenFile(filepath.Join(vdb, "environment.bz2"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bzip2", "-c", plain)
	command.Stdout = compressed
	if err := command.Run(); err != nil {
		compressed.Close()
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	path, err := materializeInstalledEnvironment(vdb, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if strings.Contains(text, "SANDBOX_") || strings.Contains(text, "LD_PRELOAD") {
		t.Fatalf("materialized environment retained isolation controls:\n%s", text)
	}
	if !strings.Contains(text, "export EAPI='8'") || !strings.Contains(text, "export FEATURES='sandbox'") {
		t.Fatalf("materialized environment lost package state:\n%s", text)
	}
}

func TestExpandBuiltSlotOperatorsRecordsInstalledSubslot(t *testing.T) {
	vdb := t.TempDir()
	provider := filepath.Join(vdb, "dev-libs", "jsoncpp-1.9.8")
	if err := os.MkdirAll(provider, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"SLOT": "0/27", "repository": "gentoo", "USE": "", "IUSE": "",
	} {
		if err := os.WriteFile(filepath.Join(provider, name), []byte(value+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := expandBuiltSlotOperatorsInDependency(
		"ssl? ( >=dev-libs/jsoncpp-1.9:= ) dev-libs/other:=", vdb, map[string]bool{"ssl": true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ssl? ( >=dev-libs/jsoncpp-1.9:0/27= ) dev-libs/other:=" {
		t.Fatalf("expanded dependency = %q", got)
	}
}

func TestPhaseProtocolDoesNotPrecreateSourceDirectoryBeforeUnpack(t *testing.T) {
	if _, err := exec.LookPath("sandbox"); err != nil {
		t.Skip("Portage sandbox is not installed")
	}
	tmp := t.TempDir()
	repo, root := filepath.Join(tmp, "repo"), filepath.Join(tmp, "root")
	vdb, work, dist := filepath.Join(root, "var", "db", "pkg"), filepath.Join(tmp, "work"), filepath.Join(tmp, "distfiles")
	packageDir := filepath.Join(repo, "dev-lang", "renamed-source")
	for _, directory := range []string{filepath.Join(repo, "eclass"), packageDir, root, vdb, work, dist} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ebuild := `EAPI=8
src_unpack() {
  [[ ! -e ${S} ]] || die "S was created before src_unpack"
  mkdir extracted || die
  printf 'payload\n' > extracted/components || die
  mv extracted "${S}" || die
}
src_install() {
  [[ -f ./components ]] || die "source tree was nested below S"
  insinto /usr/share/renamed-source
  doins components
}`
	if err := os.WriteFile(filepath.Join(packageDir, "renamed-source-1.ebuild"), []byte(ebuild), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := RebuildConfig{RepoDir: repo, DistfilesDir: dist, RootDir: root, VdbDir: vdb, WorkDirBase: work,
		PhaseProtocol: true, Repository: "test", Repositories: []portage.RepoEntry{{Name: "test", Location: repo}}}
	if err := RebuildPackage(context.Background(), "dev-lang/renamed-source-1", &cfg); err != nil {
		t.Fatalf("custom source rename rebuild: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "usr", "share", "renamed-source", "components")); err != nil {
		t.Fatalf("renamed source payload missing: %v", err)
	}
}

func TestRestrictedFetchCacheMissRunsPkgNofetchBeforeMutation(t *testing.T) {
	if _, err := exec.LookPath("sandbox"); err != nil {
		t.Skip("Portage sandbox is not installed")
	}
	tmp := t.TempDir()
	repo, root := filepath.Join(tmp, "repo"), filepath.Join(tmp, "root")
	packageDir := filepath.Join(repo, "app-misc", "manual")
	cfg := RebuildConfig{
		RepoDir: repo, DistfilesDir: filepath.Join(tmp, "distfiles"), RootDir: root,
		VdbDir: filepath.Join(root, "var", "db", "pkg"), WorkDirBase: filepath.Join(tmp, "work"),
		PhaseProtocol: true, Repository: "test", Repositories: []portage.RepoEntry{{Name: "test", Location: repo}},
	}
	for _, directory := range []string{filepath.Join(repo, "eclass"), packageDir, cfg.DistfilesDir, root, cfg.VdbDir, cfg.WorkDirBase} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	payload := []byte("manual artifact")
	digest := sha512.Sum512(payload)
	if err := os.WriteFile(filepath.Join(packageDir, "Manifest"), []byte(fmt.Sprintf("DIST manual.tar %d SHA512 %x\n", len(payload), digest)), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuildText := `EAPI=8
SRC_URI="https://invalid.example/manual.tar"
RESTRICT="fetch"
pkg_nofetch() { eerror "place manual.tar in DISTDIR"; }
src_install() { die "build must not start"; }
`
	if err := os.WriteFile(filepath.Join(packageDir, "manual-1.ebuild"), []byte(ebuildText), 0o644); err != nil {
		t.Fatal(err)
	}
	var phases, notices []string
	cfg.OnPhaseStart = func(phase string) { phases = append(phases, phase) }
	cfg.OnNotice = func(_, message string) { notices = append(notices, message) }
	err := RebuildPackage(context.Background(), "app-misc/manual-1", &cfg)
	var required *fetch.ManualFetchRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("RebuildPackage error = %v, want ManualFetchRequiredError", err)
	}
	if !slices.Equal(phases, []string{"pkg_nofetch"}) {
		t.Fatalf("phase starts = %v, want only pkg_nofetch", phases)
	}
	if !slices.ContainsFunc(notices, func(message string) bool { return strings.Contains(message, "place manual.tar in DISTDIR") }) {
		t.Fatalf("pkg_nofetch notices = %v", notices)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil || len(entries) != 1 || entries[0].Name() != "var" {
		t.Fatalf("ROOT changed before fetch completion: entries=%v err=%v", entries, readErr)
	}
}

func TestRestrictedFetchUsesVerifiedDISTDIRWithoutPkgNofetch(t *testing.T) {
	if _, err := exec.LookPath("sandbox"); err != nil {
		t.Skip("Portage sandbox is not installed")
	}
	tmp := t.TempDir()
	repo, root := filepath.Join(tmp, "repo"), filepath.Join(tmp, "root")
	packageDir := filepath.Join(repo, "app-misc", "manual-cached")
	cfg := RebuildConfig{
		RepoDir: repo, DistfilesDir: filepath.Join(tmp, "distfiles"), RootDir: root,
		VdbDir: filepath.Join(root, "var", "db", "pkg"), WorkDirBase: filepath.Join(tmp, "work"),
		PhaseProtocol: true, Repository: "test", Repositories: []portage.RepoEntry{{Name: "test", Location: repo}},
	}
	for _, directory := range []string{filepath.Join(repo, "eclass"), packageDir, cfg.DistfilesDir, root, cfg.VdbDir, cfg.WorkDirBase} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	payload := []byte("already downloaded")
	digest := sha512.Sum512(payload)
	if err := os.WriteFile(filepath.Join(packageDir, "Manifest"), []byte(fmt.Sprintf("DIST manual.tar %d SHA512 %x\n", len(payload), digest)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.DistfilesDir, "manual.tar"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	ebuildText := `EAPI=8
SRC_URI="https://invalid.example/manual.tar"
RESTRICT="fetch"
pkg_nofetch() { die "pkg_nofetch must not run for a verified cache entry"; }
src_unpack() { mkdir -p "${S}"; cp "${DISTDIR}/manual.tar" "${S}/payload"; }
src_install() { insinto /usr/share/manual-cached; doins payload; }
`
	if err := os.WriteFile(filepath.Join(packageDir, "manual-cached-1.ebuild"), []byte(ebuildText), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RebuildPackage(context.Background(), "app-misc/manual-cached-1", &cfg); err != nil {
		t.Fatalf("RebuildPackage: %v", err)
	}
	installed, err := os.ReadFile(filepath.Join(root, "usr", "share", "manual-cached", "payload"))
	if err != nil || !bytes.Equal(installed, payload) {
		t.Fatalf("installed payload = %q err=%v", installed, err)
	}
}

func TestFailedOrdinaryFetchRunsExplicitPkgNofetchOverride(t *testing.T) {
	if _, err := exec.LookPath("sandbox"); err != nil {
		t.Skip("Portage sandbox is not installed")
	}
	tmp := t.TempDir()
	repo, root := filepath.Join(tmp, "repo"), filepath.Join(tmp, "root")
	packageDir := filepath.Join(repo, "app-misc", "custom-nofetch")
	cfg := RebuildConfig{
		RepoDir: repo, DistfilesDir: filepath.Join(tmp, "distfiles"), RootDir: root,
		VdbDir: filepath.Join(root, "var", "db", "pkg"), WorkDirBase: filepath.Join(tmp, "work"),
		PhaseProtocol: true, Repository: "test", Repositories: []portage.RepoEntry{{Name: "test", Location: repo}},
		Fetcher: &fetch.Fetcher{Client: &http.Client{Transport: rebuildRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("synthetic unavailable source")
		})}},
	}
	for _, directory := range []string{filepath.Join(repo, "eclass"), packageDir, cfg.DistfilesDir, root, cfg.VdbDir, cfg.WorkDirBase} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	payload := []byte("unavailable")
	digest := sha512.Sum512(payload)
	if err := os.WriteFile(filepath.Join(packageDir, "Manifest"), []byte(fmt.Sprintf("DIST source.tar %d SHA512 %x\n", len(payload), digest)), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuildText := `EAPI=8
SRC_URI="https://invalid.example/source.tar"
pkg_nofetch() { eerror "custom upstream acquisition instructions"; }
`
	if err := os.WriteFile(filepath.Join(packageDir, "custom-nofetch-1.ebuild"), []byte(ebuildText), 0o644); err != nil {
		t.Fatal(err)
	}
	var phases, notices []string
	cfg.OnPhaseStart = func(phase string) { phases = append(phases, phase) }
	cfg.OnNotice = func(_, message string) { notices = append(notices, message) }
	err := RebuildPackage(context.Background(), "app-misc/custom-nofetch-1", &cfg)
	if err == nil || !strings.Contains(err.Error(), "all sources failed") {
		t.Fatalf("RebuildPackage error = %v", err)
	}
	if !slices.Equal(phases, []string{"pkg_nofetch"}) {
		t.Fatalf("phase starts = %v", phases)
	}
	if !slices.ContainsFunc(notices, func(message string) bool {
		return strings.Contains(message, "custom upstream acquisition instructions")
	}) {
		t.Fatalf("pkg_nofetch notices = %v", notices)
	}
}

func TestRebuildPackageRevisionUsesPWithoutRevision(t *testing.T) {
	if _, err := exec.LookPath("sandbox"); err != nil {
		t.Skip("Portage sandbox is not installed")
	}
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	root := filepath.Join(tmp, "root")
	vdb := filepath.Join(root, "var", "db", "pkg")
	work := filepath.Join(tmp, "work")
	packageDir := filepath.Join(repo, "app-misc", "revision-test")
	for _, directory := range []string{filepath.Join(repo, "eclass"), packageDir, root, vdb, work, filepath.Join(tmp, "distfiles")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ebuildContent := `EAPI=8
src_unpack() {
	[[ ${PV} == 1.2.3 && ${PR} == r1 && ${PVR} == 1.2.3-r1 ]] || return 41
	[[ ${P} == revision-test-1.2.3 && ${PF} == revision-test-1.2.3-r1 ]] || return 42
	[[ ${S} == "${WORKDIR}/revision-test-1.2.3" ]] || return 43
	mkdir -p "${S}"
	printf 'revision identity\n' > "${S}/payload"
}
src_install() { insinto /usr/share/revision-test; doins payload; }
`
	if err := os.WriteFile(filepath.Join(packageDir, "revision-test-1.2.3-r1.ebuild"), []byte(ebuildContent), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := RebuildConfig{
		RepoDir: repo, DistfilesDir: filepath.Join(tmp, "distfiles"), RootDir: root, VdbDir: vdb, WorkDirBase: work,
		PhaseProtocol: true, Repository: "test", Repositories: []portage.RepoEntry{{Name: "test", Location: repo}},
	}
	if err := RebuildPackage(context.Background(), "app-misc/revision-test-1.2.3-r1", &cfg); err != nil {
		t.Fatalf("revisioned protocol rebuild: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "usr", "share", "revision-test", "payload")); err != nil {
		t.Fatalf("revisioned package payload: %v", err)
	}
}

func TestRebuildPackageDisposableRootReplacementMatrix(t *testing.T) {
	if _, err := exec.LookPath("sandbox"); err != nil {
		t.Skip("Portage sandbox is not installed")
	}
	tmp := t.TempDir()
	repo, root := filepath.Join(tmp, "repo"), filepath.Join(tmp, "root")
	vdb, work := filepath.Join(root, "var", "db", "pkg"), filepath.Join(tmp, "work")
	dist, journals := filepath.Join(tmp, "distfiles"), filepath.Join(tmp, "journals")
	packageDir := filepath.Join(repo, "app-misc", "cycle-test")
	for _, directory := range []string{filepath.Join(repo, "eclass"), packageDir, root, vdb, work, dist} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeEbuild := func(version, slot string) {
		t.Helper()
		body := fmt.Sprintf(`EAPI=8
S="${WORKDIR}/${P}"
SLOT=%q
src_unpack() { mkdir -p "${S}"; printf 'version=%s\n' > "${S}/current"; printf 'payload=%s\n' > "${S}/versioned"; }
src_install() {
  insinto /usr/share/cycle-test
  newins current current
  newins versioned version-%s
  if [[ ${SLOT} == 1 ]]; then newins versioned slot-1; fi
}
`, slot, version, version, version)
		if err := os.WriteFile(filepath.Join(packageDir, "cycle-test-"+version+".ebuild"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeEbuild("1", "0")
	writeEbuild("2", "0")
	writeEbuild("3", "1")
	cfg := RebuildConfig{
		RepoDir: repo, DistfilesDir: dist, RootDir: root, VdbDir: vdb, WorkDirBase: work,
		PhaseProtocol: true, Repository: "test", Repositories: []portage.RepoEntry{{Name: "test", Location: repo}},
		JournalDir: journals,
	}
	run := func(version string, replacement bool) {
		t.Helper()
		cfg.AllowLiveUpgrade = replacement
		if err := RebuildPackage(context.Background(), "app-misc/cycle-test-"+version, &cfg); err != nil {
			t.Fatalf("install cycle-test-%s (replacement=%v): %v", version, replacement, err)
		}
	}
	assertCurrent := func(version string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, "usr", "share", "cycle-test", "current"))
		if err != nil || string(data) != "version="+version+"\n" {
			t.Fatalf("current after %s = %q, %v", version, data, err)
		}
	}

	run("1", false) // fresh install
	assertCurrent("1")
	run("2", true) // upgrade
	assertCurrent("2")
	if _, err := os.Lstat(filepath.Join(root, "usr", "share", "cycle-test", "version-1")); !os.IsNotExist(err) {
		t.Fatalf("upgrade retained obsolete version-1 payload: %v", err)
	}
	run("1", true) // downgrade
	assertCurrent("1")
	if _, err := os.Lstat(filepath.Join(root, "usr", "share", "cycle-test", "version-2")); !os.IsNotExist(err) {
		t.Fatalf("downgrade retained obsolete version-2 payload: %v", err)
	}
	run("1", false) // same-version reinstall
	assertCurrent("1")
	run("3", false) // parallel slot
	assertCurrent("3")

	for _, entry := range []string{"cycle-test-1", "cycle-test-3"} {
		if _, err := os.Stat(filepath.Join(vdb, "app-misc", entry, "CONTENTS")); err != nil {
			t.Fatalf("coexisting VDB entry %s: %v", entry, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "usr", "share", "cycle-test", "slot-1")); err != nil {
		t.Fatalf("parallel-slot payload: %v", err)
	}
	if err := mergepkg.UnmergeAt(context.Background(), root, vdb, filepath.Join(vdb, "app-misc", "cycle-test-1"), journals); err != nil {
		t.Fatalf("unmerge slot 0: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(vdb, "app-misc", "cycle-test-1")); !os.IsNotExist(err) {
		t.Fatalf("unmerge retained slot-0 VDB: %v", err)
	}
	for _, path := range []string{"current", "slot-1", "version-3"} {
		if _, err := os.Stat(filepath.Join(root, "usr", "share", "cycle-test", path)); err != nil {
			t.Fatalf("unmerge removed slot-1-owned %s: %v", path, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "usr", "share", "cycle-test", "version-1")); !os.IsNotExist(err) {
		t.Fatalf("unmerge retained exclusively owned slot-0 payload: %v", err)
	}
	summaries, err := journal.List(journals)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 6 {
		t.Fatalf("journal count = %d, want 6", len(summaries))
	}
	for _, summary := range summaries {
		if summary.Status != "committed" || summary.Entries == 0 {
			t.Fatalf("replacement journal = %#v", summary)
		}
	}
}

func TestRebuildPackagePostinstFailureRetainsCommittedTransactionAndPreservesLog(t *testing.T) {
	if _, err := exec.LookPath("sandbox"); err != nil {
		t.Skip("Portage sandbox is not installed")
	}
	for _, eapi := range []string{"7", "8", "9"} {
		t.Run("EAPI-"+eapi, func(t *testing.T) {
			tmp := t.TempDir()
			repo := filepath.Join(tmp, "repo")
			root := filepath.Join(tmp, "root")
			vdb := filepath.Join(root, "var", "db", "pkg")
			work := filepath.Join(tmp, "work")
			dist := filepath.Join(tmp, "distfiles")
			logs := filepath.Join(tmp, "logs")
			journals := filepath.Join(tmp, "journals")
			packageDir := filepath.Join(repo, "app-misc", "failure-test")
			for _, directory := range []string{filepath.Join(repo, "eclass"), packageDir, root, vdb, work, dist} {
				if err := os.MkdirAll(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			ebuildContent := fmt.Sprintf(`EAPI=%s
S="${WORKDIR}/${P}"
src_unpack() { mkdir -p "${S}"; printf 'transaction payload\n' > "${S}/payload"; }
src_install() { insinto /usr/share/failure-test; doins payload; }
pkg_postinst() { printf 'postinst failure\n'; die 'postinst failed'; }
`, eapi)
			if err := os.WriteFile(filepath.Join(packageDir, "failure-test-1.ebuild"), []byte(ebuildContent), 0o644); err != nil {
				t.Fatal(err)
			}
			commitCallbackCalled := false
			cfg := RebuildConfig{
				RepoDir: repo, DistfilesDir: dist, RootDir: root, VdbDir: vdb, WorkDirBase: work,
				PhaseProtocol: true, Repository: "test", Repositories: []portage.RepoEntry{{Name: "test", Location: repo}},
				PhaseLogDir: logs, JournalDir: journals,
				OnTransactionCommit: func(committedErr error) error {
					commitCallbackCalled = true
					var postCommit *mergepkg.PostCommitError
					if !errors.As(committedErr, &postCommit) {
						t.Fatalf("commit callback error = %v, want PostCommitError", committedErr)
					}
					return nil
				},
			}
			err := RebuildPackage(context.Background(), "app-misc/failure-test-1", &cfg)
			if err == nil || !strings.Contains(err.Error(), "pkg_postinst") || !strings.Contains(err.Error(), "exit status 1") {
				t.Fatalf("postinst failure = %v", err)
			}
			if !commitCallbackCalled {
				t.Fatal("committed postinst failure did not invoke transaction callback")
			}
			for _, path := range []string{
				filepath.Join(root, "usr", "share", "failure-test", "payload"),
				filepath.Join(vdb, "app-misc", "failure-test-1"),
			} {
				if _, statErr := os.Lstat(path); statErr != nil {
					t.Fatalf("post-commit failure lost %s: %v", path, statErr)
				}
			}
			summaries, err := journal.List(journals)
			if err != nil {
				t.Fatal(err)
			}
			if len(summaries) != 1 || summaries[0].Status != "committed" || summaries[0].Entries == 0 {
				t.Fatalf("failure journal = %#v", summaries)
			}
			logPaths, err := filepath.Glob(filepath.Join(logs, "app-misc:failure-test-1:*.log"))
			if err != nil || len(logPaths) != 1 {
				t.Fatalf("durable logs = %v, %v", logPaths, err)
			}
			content, err := os.ReadFile(logPaths[0])
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(content), "postinst failure") || !strings.Contains(string(content), "exit_code=1") || !strings.Contains(string(content), "terminal-error") {
				t.Fatalf("durable postinst log = %s", content)
			}
		})
	}
}

func TestRebuildPackageUsesVerifiedCachedDISTDIR(t *testing.T) {
	tmp := t.TempDir()
	repoDir := filepath.Join(tmp, "repo")
	distDir := filepath.Join(tmp, "distfiles")
	workDir := filepath.Join(tmp, "work")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(tmp, "vdb")
	packageDir := filepath.Join(repoDir, "app-misc", "cached")
	for _, directory := range []string{distDir, workDir, rootDir, vdbDir, packageDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	content := archive.Bytes()
	digest := sha512.Sum512(content)
	if err := os.WriteFile(filepath.Join(distDir, "source.tar.gz"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := "DIST source.tar.gz " + fmt.Sprint(len(content)) + " SHA512 " + hex.EncodeToString(digest[:]) + "\n"
	if err := os.WriteFile(filepath.Join(packageDir, "Manifest"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := "EAPI=8\nSRC_URI=\"https://invalid.example/source.tar.gz\"\n"
	if err := os.WriteFile(filepath.Join(packageDir, "cached-1.ebuild"), []byte(ebuild), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := RebuildConfig{RepoDir: repoDir, DistfilesDir: distDir, RootDir: rootDir, VdbDir: vdbDir, WorkDirBase: workDir}
	if err := RebuildPackage(context.Background(), "app-misc/cached-1", &cfg); err != nil {
		t.Fatal(err)
	}
}

func TestRebuildPackageRefusesSourceWithoutManifest(t *testing.T) {
	tmp := t.TempDir()
	packageDir := filepath.Join(tmp, "repo", "app-misc", "missing")
	for _, directory := range []string{packageDir, filepath.Join(tmp, "dist"), filepath.Join(tmp, "work")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(packageDir, "missing-1.ebuild"), []byte("EAPI=8\nSRC_URI=\"https://invalid.example/source.tar\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := RebuildConfig{RepoDir: filepath.Join(tmp, "repo"), DistfilesDir: filepath.Join(tmp, "dist"), RootDir: filepath.Join(tmp, "root"), VdbDir: filepath.Join(tmp, "vdb"), WorkDirBase: filepath.Join(tmp, "work")}
	err := RebuildPackage(context.Background(), "app-misc/missing-1", &cfg)
	if err == nil || !strings.Contains(err.Error(), "open Manifest") {
		t.Fatalf("error = %v", err)
	}
}

func TestRebuildPackages_ContinuesOnError(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	repoDir := filepath.Join(tmp, "repo")
	vdbDir := filepath.Join(tmp, "vdb")
	rootDir := filepath.Join(tmp, "root")
	distDir := filepath.Join(tmp, "distfiles")
	workDir := filepath.Join(tmp, "work")

	for _, d := range []string{repoDir, vdbDir, rootDir, distDir, workDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	// Good package
	goodDir := filepath.Join(repoDir, "app-good", "good")
	if err := os.MkdirAll(goodDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goodDir, "good-1.0.ebuild"), []byte("EAPI=8\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var erroredPkgs []string
	cfg := RebuildConfig{
		RepoDir:      repoDir,
		DistfilesDir: distDir,
		RootDir:      rootDir,
		VdbDir:       vdbDir,
		WorkDirBase:  workDir,
		OnError: func(pkg string, err error) {
			erroredPkgs = append(erroredPkgs, pkg)
		},
	}

	atoms := []string{
		"app-bad/bad-1.0",
		"app-good/good-1.0",
		"app-bad/missing-1.0",
	}

	err := RebuildPackages(ctx, atoms, &cfg)
	if err == nil {
		t.Error("expected error from RebuildPackages with failing atoms")
	}

	if len(erroredPkgs) < 2 {
		t.Errorf("expected at least 2 errored packages, got %d: %v", len(erroredPkgs), erroredPkgs)
	}

	// The good package should have been merged
	contentsPath := filepath.Join(vdbDir, "app-good", "good-1.0", "CONTENTS")
	if _, err := os.Stat(contentsPath); err != nil {
		t.Errorf("CONTENTS for good package should exist: %v", err)
	}
}

func TestRebuildPackage_AdversarialAtom(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	cfg := RebuildConfig{
		WorkDirBase: filepath.Join(tmp, "work"),
	}
	if err := os.MkdirAll(cfg.WorkDirBase, 0755); err != nil {
		t.Fatal(err)
	}

	adversarial := []string{
		strings.Repeat("a", 10000) + "/pkg-1.0",
		"../../etc/passwd-1.0",
		"\x00/pkg-1.0",
		"cat/pkg-99999999999999999999",
	}

	for _, a := range adversarial {
		err := RebuildPackage(ctx, a, &cfg)
		_ = err
	}
}

func createTestTar(t *testing.T, tarPath string, files map[string]string) {
	t.Helper()

	fh, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fh.Close() }()

	gw := gzip.NewWriter(fh)
	defer func() { _ = gw.Close() }()
	tw := tar.NewWriter(gw)
	defer func() { _ = tw.Close() }()

	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0644,
			Size: int64(len(content)),
		}
		if strings.Contains(name, "usr/bin") {
			hdr.Mode = 0755
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
}

func mkdirAll(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProtocolBuildPhasesHonorTestPolicy(t *testing.T) {
	without := strings.Join(protocolBuildPhases(phaseproto.ExecutionPolicy{Configured: true}), " ")
	with := strings.Join(protocolBuildPhases(phaseproto.ExecutionPolicy{Configured: true, Tests: true}), " ")
	if strings.Contains(without, "src_test") || !strings.Contains(with, "src_test") {
		t.Fatalf("without=%q with=%q", without, with)
	}
}

func TestLifecycleOnlyDisabledUseGuards(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pkg-1.ebuild")
	content := "pkg_setup() {\n\tuse test && check-reqs_pkg_setup\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if !lifecycleOnlyDisabledUseGuards(path, "pkg_setup", map[string]bool{"test": false}) {
		t.Fatal("disabled USE guard was not accepted")
	}
	if lifecycleOnlyDisabledUseGuards(path, "pkg_setup", map[string]bool{"test": true}) {
		t.Fatal("enabled USE guard was accepted")
	}
	content = "pkg_setup() {\n\tuse test && check-reqs_pkg_setup\n\teinfo unsafe-unguarded-tail\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if lifecycleOnlyDisabledUseGuards(path, "pkg_setup", map[string]bool{"test": false}) {
		t.Fatal("unguarded lifecycle command was accepted")
	}
}

func TestDynamicAutotoolsQueries(t *testing.T) {
	source := "_LATEST_AUTOCONF=( 2.73:2.73 2.72-r1:2.72 )\n_LATEST_AUTOMAKE=( 1.18.1:1.18 )\n"
	want := []string{"=dev-build/autoconf-2.72*", "=dev-build/autoconf-2.73*", "=dev-build/automake-1.18*"}
	if got := dynamicAutotoolsQueries(source); !slices.Equal(got, want) {
		t.Fatalf("queries=%v want=%v", got, want)
	}
}

func TestStaticHasVersionQueryAcceptsQuotedAndUnquotedAtoms(t *testing.T) {
	source := `has_version dev-libs/libffi[pax-kernel]; has_version -b "=dev-build/automake-1.18*"; has_version 'dev-lang/python:3.14'`
	matches := staticHasVersionQuery.FindAllStringSubmatch(source, -1)
	if len(matches) != 3 {
		t.Fatalf("matches=%v", matches)
	}
	var got []string
	for _, match := range matches {
		query := match[2]
		if query == "" {
			query = match[3]
		}
		if query == "" {
			query = match[4]
		}
		got = append(got, match[1]+":"+query)
	}
	want := []string{":dev-libs/libffi[pax-kernel]", "b:=dev-build/automake-1.18*", ":dev-lang/python:3.14"}
	if !slices.Equal(got, want) {
		t.Fatalf("queries=%v want=%v", got, want)
	}
}

func TestPhaseRequestEnvironmentPreservesPackageEnvPrecedence(t *testing.T) {
	cfg := &RebuildConfig{
		CFLAGS: "-O2", CXXFLAGS: "-O2", LDFLAGS: "-Wl,-O1", MAKEOPTS: "-j8", Arch: "amd64",
		PortageConfig: &portage.Config{MakeConf: map[string]string{"CFLAGS": "-O2", "CHOST": "x86_64-pc-linux-gnu"}},
	}
	got := phaseRequestEnvironment(cfg, "ssl", "source.tar")
	if len(got) != 2 || got["USE"] != "amd64 ssl" || got["A"] != "source.tar" {
		t.Fatalf("configured request overrides = %#v", got)
	}

	cfg.PortageConfig = nil
	got = phaseRequestEnvironment(cfg, "ssl", "source.tar")
	for name, want := range map[string]string{"CFLAGS": "-O2", "CXXFLAGS": "-O2", "LDFLAGS": "-Wl,-O1", "MAKEOPTS": "-j8", "ARCH": "amd64"} {
		if got[name] != want {
			t.Fatalf("fallback %s = %q, want %q", name, got[name], want)
		}
	}
}

func TestEnabledUseWithArchSuppliesImplicitArchitecture(t *testing.T) {
	got := enabledUseWithArch(map[string]bool{"ssl": true, "test": false, "amd64": true}, "amd64")
	if got != "amd64 ssl" {
		t.Fatalf("effective phase USE=%q", got)
	}
}

func TestProtocolVDBMetadataUsesSelectedPackageUSE(t *testing.T) {
	directory := t.TempDir()
	ebuildFile := filepath.Join(directory, "pkg-1.ebuild")
	if err := os.WriteFile(ebuildFile, []byte("EAPI=8\nSLOT=0\nIUSE=ssl\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eb, err := ebuild.ParseEbuild(ebuildFile)
	if err != nil {
		t.Fatal(err)
	}
	request := phaseproto.Request{
		Env:     map[string]string{"USE": "ssl alsa bluetooth qt6 x264"},
		Package: phaseproto.PackageIdentity{Slot: "0", Repository: "gentoo"},
	}
	metadata := protocolVDBMetadata(eb, ebuildFile, "app-misc", "pkg-1", "ssl verify-sig", map[string]bool{"ssl": true, "test": false, "abi_x86_64": true}, request, nil)
	if metadata["ARISE_PHASE_ENV_ABI"] != portage.PhaseEnvironmentABI {
		t.Fatalf("phase environment ABI = %q", metadata["ARISE_PHASE_ENV_ABI"])
	}
	if got, want := metadata["USE"], "abi_x86_64 ssl"; got != want {
		t.Fatalf("VDB USE=%q want=%q", got, want)
	}
	if got, want := metadata["IUSE"], "ssl verify-sig"; got != want {
		t.Fatalf("VDB IUSE=%q want=%q", got, want)
	}
}
