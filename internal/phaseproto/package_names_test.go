package phaseproto

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestPackageNamesDoNotCollideWithWorkerNamespace(t *testing.T) {
	request := phaseStateRequest(t, `ARISE_COMMIT=source-commit
ARISE_STATUS=package-status
ARISE_ID=package-id
ARISE_DOCOMPRESS=package-setting
ARISE_INHERITED=package-inherited
ARISE_TEXT=$'first\n# ARISE_PHASE_STATE_V2\nlast'
BASH_COMPLETION_COMPAT_DIR=package-completion
arise_package_helper() { printf helper; }
emit() { printf package-emit; }
escape_json() { printf package-json; }
run_one_phase() { printf package-run; }
src_unpack() { mkdir -p "$S"; ARISE_PACKAGE_VALUE=unpacked; }
src_prepare() {
	[[ $ARISE_COMMIT == source-commit ]] || die 'lost source commit'
	[[ $ARISE_PACKAGE_VALUE == unpacked ]] || die 'lost package value'
	[[ $ARISE_STATUS == package-status && $ARISE_ID == package-id ]] || die 'worker overwrote package state'
	[[ $ARISE_DOCOMPRESS == package-setting && $ARISE_INHERITED == package-inherited ]] || die 'legacy names were mistaken for current worker state'
	[[ $ARISE_TEXT == $'first\n# ARISE_PHASE_STATE_V2\nlast' ]] || die 'marker-like variable data changed'
	[[ $BASH_COMPLETION_COMPAT_DIR == package-completion ]] || die 'lost completion setting'
	[[ $(arise_package_helper) == helper ]] || die 'lost package helper'
	[[ $(emit) == package-emit && $(escape_json) == package-json && $(run_one_phase) == package-run ]] || die 'worker replaced package functions'
	printf prepared > "$T/observed"
}`)
	runPhaseStateSequence(t, request, true, "src_unpack", "src_prepare")
	if data, err := os.ReadFile(filepath.Join(request.TempDir, "observed")); err != nil || string(data) != "prepared" {
		t.Fatalf("prepare = %q, %v", data, err)
	}
}

func TestFailedLegacyNormalizationPreservesSnapshots(t *testing.T) {
	request := phaseStateRequest(t, "")
	request.ID, request.Command, request.Phase = "legacy-failed", "run_phase", "pkg_setup"
	request.EnvironmentOverlay = filepath.Join(request.TempDir, "legacy.environment")
	request.SaveEnvironment = filepath.Join(request.TempDir, "good.environment")
	input := "# ARISE_PHASE_STATE_V2\nEAPI=8\nreturn 7\n"
	for path, content := range map[string]string{request.EnvironmentOverlay: input, request.SaveEnvironment: "last good snapshot\n"} {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err == nil {
		t.Fatal("failed legacy normalization continued to package execution")
	}
	for path, want := range map[string]string{request.EnvironmentOverlay: input, request.SaveEnvironment: "last good snapshot\n"} {
		if data, err := os.ReadFile(path); err != nil || string(data) != want {
			t.Fatalf("failed normalization changed %s: %v", path, err)
		}
	}
	if paths, err := filepath.Glob(filepath.Join(request.TempDir, ".arise-state.*")); err != nil || len(paths) != 0 {
		t.Fatalf("failed normalization left temporary files: %v, %v", paths, err)
	}
}

func TestInstalledEnvironmentUsesCurrentRuntimeSettings(t *testing.T) {
	request := phaseStateRequest(t, `pkg_preinst() {
	printf '%s|%s|%s|%s|%s' "$FEATURES" "$LC_ALL" "${LANG-unset}" "${LC_MESSAGES-unset}" "$LC_PACKAGE_DATA" > "$T/observed"
}`)
	request.Env = map[string]string{"FEATURES": "current-features", "LC_ALL": "C.UTF-8"}
	ebuild, err := os.ReadFile(request.Ebuild)
	if err != nil {
		t.Fatal(err)
	}
	request.Environment = filepath.Join(request.TempDir, "installed.environment")
	state := SavedEnvironmentMarker + "\n" + SavedLifecyclePrefix + " pkg_preinst\nexport FEATURES=old LC_ALL=C LANG=C LC_MESSAGES=C LC_PACKAGE_DATA=package-data\n" + string(ebuild)
	if err := os.WriteFile(request.Environment, []byte(state), 0644); err != nil {
		t.Fatal(err)
	}
	runPhaseStateSequence(t, request, true, "pkg_preinst")
	if data, err := os.ReadFile(filepath.Join(request.TempDir, "observed")); err != nil || string(data) != "current-features|C.UTF-8|unset|unset|package-data" {
		t.Fatalf("installed runtime settings = %q, %v", data, err)
	}
}

func TestEclassGlobalsDoNotBindToInheritLocals(t *testing.T) {
	request := phaseStateRequest(t, `inherit names
src_unpack() { mkdir -p "$S"; }
src_prepare() {
	[[ $name == package-name && $directory == package-directory && $path == package-path && $old_eclass == package-eclass ]] || die 'inherit shadowed eclass globals'
}`)
	eclassDir := filepath.Join(request.TempDir, "eclass")
	if err := os.MkdirAll(eclassDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(eclassDir, "names.eclass"), []byte("name=package-name\ndirectory=package-directory\npath=package-path\nold_eclass=package-eclass\n"), 0644); err != nil {
		t.Fatal(err)
	}
	request.EclassDirs = []string{eclassDir}
	runPhaseStateSequence(t, request, true, "src_unpack", "src_prepare")
}

func TestPackageEnvironmentMayUseApplicationPrefix(t *testing.T) {
	request := phaseStateRequest(t, `pkg_setup() { [[ $ARISE_COMMIT == configured ]] || die 'lost package.env value'; }`)
	request.Env = map[string]string{"ARISE_COMMIT": "configured"}
	if _, err := mergePackageEnvironment(request.Env, nil); err != nil {
		t.Fatal(err)
	}
	runPhaseStateSequence(t, request, true, "pkg_setup")
	if _, err := mergePackageEnvironment(map[string]string{"___ARISE_ID": "poison"}, nil); err == nil {
		t.Fatal("package.env replaced a reserved worker control")
	}
}

func TestLegacyHelperStateSurvivesNamespaceMigration(t *testing.T) {
	request := phaseStateRequest(t, "")
	state := `# ARISE_PHASE_STATE_V2
EAPI=8
declare -A ARISE_INHERITED=([legacy]=1)
declare -a ARISE_DOCOMPRESS=([0]="/usr/share/doc")
declare -a ARISE_DOCOMPRESS_SKIP=([0]="/usr/share/doc/state-probe-1")
readonly LEGACY_CONSTANT=initial
src_install() {
	LEGACY_CONSTANT=updated
	[[ $LEGACY_CONSTANT == updated ]] || die 'legacy readonly was not normalized'
	mkdir -p "$ED/usr/share/doc/state-probe-1" || die
	printf '%0200d' 0 > "$ED/usr/share/doc/state-probe-1/plain"
}
`
	if err := os.MkdirAll(request.SourceDir, 0755); err != nil {
		t.Fatal(err)
	}
	request.EnvironmentOverlay = filepath.Join(request.TempDir, "legacy.environment")
	if err := os.WriteFile(request.EnvironmentOverlay, []byte(state), 0644); err != nil {
		t.Fatal(err)
	}
	runPhaseStateSequence(t, request, true, "src_install")
	if _, err := os.Stat(filepath.Join(request.ImageDir, "usr/share/doc/state-probe-1/plain")); err != nil {
		t.Fatalf("legacy compression exclusion was lost: %v", err)
	}
	if data, err := os.ReadFile(request.EnvironmentOverlay); err != nil || string(data) != state {
		t.Fatalf("legacy input was modified: %v", err)
	}
	if paths, err := filepath.Glob(filepath.Join(request.TempDir, ".arise-state.*")); err != nil || len(paths) != 0 {
		t.Fatalf("legacy normalization temporaries remain: %v, %v", paths, err)
	}
}

func TestCompleteEnvironmentMarkersRemainCompatible(t *testing.T) {
	for _, state := range []string{"export CFLAGS='-O2'\n# ARISE_PHASE_STATE_V2\n", SavedEnvironmentMarker + "\n" + SavedLifecyclePrefix + "\nexport CFLAGS='-O2'\n"} {
		if !HasCompleteSavedEnvironment([]byte(state)) {
			t.Fatalf("lost complete snapshot semantics for %s", state)
		}
	}
	if HasCompleteSavedEnvironment([]byte("export CFLAGS='-O2'\n")) {
		t.Fatal("initial variables were treated as a complete package snapshot")
	}
	if HasCompleteSavedEnvironment([]byte("export PACKAGE_TEXT='first\n" + SavedEnvironmentMarker + "\nlast'\n")) {
		t.Fatal("quoted package data was treated as a current format marker")
	}
}

func TestPackageFunctionAndVariableAttributesMatchPortage(t *testing.T) {
	request := phaseStateRequest(t, `src_unpack() {
	mkdir -p "$S" || die
	readonly PACKAGE_CONSTANT=initial
	declare -gi PACKAGE_INTEGER=19
	declare -gl PACKAGE_LOWER=MiXeD
	declare -gn PACKAGE_REF=PACKAGE_INTEGER
	package_exported() { printf exported; }
	export -f package_exported
	package_readonly() { :; }
	readonly -f package_readonly
	package_traced() { :; }
	declare -ft package_traced
	package_document() {
		cat <<'END'
declare -fr literal-data
END
	}
}
src_prepare() {
	PACKAGE_CONSTANT=restored
	(( PACKAGE_INTEGER += 2 ))
	[[ $PACKAGE_CONSTANT == restored && $PACKAGE_REF == 21 && $PACKAGE_LOWER == mixed ]] || die 'lost variable attributes'
	[[ $(bash -c package_exported) == exported ]] || die 'lost exported function'
	unset -f package_readonly || die 'restored function incorrectly remained readonly'
	[[ $(declare -Fp package_traced) == 'declare -ft package_traced' ]] || die 'lost function trace attribute'
	[[ $(package_document) == 'declare -fr literal-data' ]] || die 'function body was rewritten'
	printf passed > "$T/observed"
}`)
	runPhaseStateSequence(t, request, true, "src_unpack", "src_prepare")
	if data, err := os.ReadFile(filepath.Join(request.TempDir, "observed")); err != nil || string(data) != "passed" {
		t.Fatalf("restored attributes = %q, %v", data, err)
	}
}

func TestPackageChildDoesNotInheritWorkerProtocol(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	// A child that outlives its phase must not keep the worker event pipe open.
	content := "EAPI=8\nsrc_compile() { sleep 3 >/dev/null 2>&1 & }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request := Request{Protocol: Version, ID: "child-protocol", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild}
	if _, err := runWorkerCommandWithCancelGrace(exec.CommandContext(ctx, "bash", "--noprofile", "--norc", "-c", bashWorker), request, 100*time.Millisecond); err != nil {
		t.Fatalf("completed phase waited for its background child: %v", err)
	}
}
