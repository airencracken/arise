package phaseproto

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func phaseStateRequest(t *testing.T, body string) Request {
	return phaseStateRequestForEAPI(t, "8", body)
}

func phaseStateRequestForEAPI(t *testing.T, eapi, body string) Request {
	t.Helper()
	directory := t.TempDir()
	work, temporary, image := filepath.Join(directory, "work"), filepath.Join(directory, "temp"), filepath.Join(directory, "image")
	for _, path := range []string{work, temporary, image} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	ebuild := filepath.Join(directory, "state-probe-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI="+eapi+"\n"+body+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return Request{Protocol: Version, EAPI: eapi, Ebuild: ebuild, WorkDir: work, BuildDir: directory,
		SourceDir: filepath.Join(work, "state-probe-1"), TempDir: temporary, ImageDir: image,
		Package: PackageIdentity{Category: "app-misc", PN: "state-probe", PV: "1", PR: "r0", P: "state-probe-1", PVR: "1", PF: "state-probe-1", Slot: "0", Repository: "test"}}
}

func runPhaseStateSequence(t *testing.T, request Request, split bool, phases ...string) {
	t.Helper()
	if !split {
		request.ID, request.Command, request.Phases = "state-batch", "run_phases", phases
		if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
			t.Fatalf("batch failed: %v; events=%+v", err, events)
		}
		return
	}
	overlay := filepath.Join(request.TempDir, "phase.environment")
	for index, phase := range phases {
		request.ID, request.Command, request.Phase = fmt.Sprintf("state-%d", index), "run_phase", phase
		request.SaveEnvironment = overlay
		if index > 0 {
			request.EnvironmentOverlay = overlay
		}
		if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
			t.Fatalf("%s failed: %v; events=%+v", phase, err, events)
		}
	}
}

func TestPhaseStateSplitMatchesBatch(t *testing.T) {
	for _, test := range []struct{ name, body, want string }{
		{"unpack changes S", `src_unpack() { S="${WORKDIR}/binutils-2.47"; mkdir -p "$S"; printf payload > "$S/input"; }
src_prepare() { [[ $PWD == "$S" ]] || die 'wrong cwd'; cat input > "$T/observed" || die; }`, "payload"},
		{"phase-created helper", `src_unpack() { mkdir -p "$S"; state_helper() { printf helper; }; }
src_prepare() { state_helper > "$T/observed" || die 'lost helper'; }`, "helper"},
		{"eclass helper closure", `state_helper() { printf '%s' "$PHASE_VALUE"; }
src_unpack() { mkdir -p "$S"; PHASE_VALUE=closure; }
src_prepare() { state_helper > "$T/observed" || die; }`, "closure"},
		{"phase-created readonly", `src_unpack() { mkdir -p "$S"; readonly PHASE_CONSTANT=constant; }
src_prepare() { printf '%s' "${PHASE_CONSTANT-unset}" > "$T/observed"; }`, "constant"},
		{"unset global", `PHASE_VALUE=global
src_unpack() { mkdir -p "$S"; unset PHASE_VALUE; }
src_prepare() { printf '%s' "${PHASE_VALUE-unset}" > "$T/observed"; }`, "unset"},
		{"unset helper", `state_helper() { printf resurrected; }
src_unpack() { mkdir -p "$S"; unset -f state_helper; }
src_prepare() { if declare -F state_helper >/dev/null; then printf resurrected; else printf unset; fi > "$T/observed"; }`, "unset"},
		{"serializer local collisions", `src_unpack() { mkdir -p "$S"; name=package-name; output=package-output; temporary=package-temp; declaration=package-declaration; phase_name=package-phase; }
src_prepare() { printf '%s|%s|%s|%s|%s' "$name" "$output" "$temporary" "$declaration" "$phase_name" > "$T/observed"; }`, "package-name|package-output|package-temp|package-declaration|package-phase"},
		{"exported values and arrays", `src_unpack() { mkdir -p "$S"; export PHASE_VALUE=exported; PHASE_ARRAY=(one 'two words'); declare -gA PHASE_MAP=([key]='mapped value'); }
src_prepare() { printf '%s|%s|%s' "$(printenv PHASE_VALUE)" "${PHASE_ARRAY[1]}" "${PHASE_MAP[key]}" > "$T/observed"; }`, "exported|two words|mapped value"},
		{"replacement phase function", `src_unpack() { mkdir -p "$S"; src_prepare() { printf replaced > "$T/observed"; }; }
src_prepare() { printf original > "$T/observed"; }`, "replaced"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, split := range []bool{false, true} {
				t.Run(fmt.Sprintf("split=%t", split), func(t *testing.T) {
					request := phaseStateRequest(t, test.body)
					runPhaseStateSequence(t, request, split, "src_unpack", "src_prepare")
					data, err := os.ReadFile(filepath.Join(request.TempDir, "observed"))
					if err != nil || string(data) != test.want {
						t.Fatalf("observed %q, %v; want %q", data, err, test.want)
					}
				})
			}
		})
	}
}

func TestPhaseStatePreservesCompressionQueue(t *testing.T) {
	request := phaseStateRequest(t, `src_unpack() { mkdir -p "$S"; }
src_prepare() { docompress -x /usr/share/doc/state-probe-1; }
src_install() { mkdir -p "$ED/usr/share/doc/state-probe-1"; printf '%0200d' 0 > "$ED/usr/share/doc/state-probe-1/plain"; }`)
	runPhaseStateSequence(t, request, true, "src_unpack", "src_prepare", "src_install")
	if _, err := os.Stat(filepath.Join(request.ImageDir, "usr/share/doc/state-probe-1/plain")); err != nil {
		t.Fatalf("compression exclusion from an earlier phase was lost: %v", err)
	}
}

func TestPhaseStatePackageHooksStartInEmptyDirectory(t *testing.T) {
	request := phaseStateRequest(t, `src_unpack() { mkdir -p "$S"; printf source > "$WORKDIR/source-marker"; }
pkg_preinst() { [[ -z $(find . -mindepth 1 -print -quit) ]] || die 'hook directory is not empty'; printf passed > "$T/observed"; }`)
	runPhaseStateSequence(t, request, true, "src_unpack", "pkg_preinst")
}

func TestPhaseStateRejectsSourceDirectoryEscape(t *testing.T) {
	for _, destination := range []string{`${WORKDIR}/../escaped`, `${WORKDIR}/link/escaped`} {
		t.Run(destination, func(t *testing.T) {
			request := phaseStateRequest(t, `src_unpack() { S="`+destination+`"; }`)
			outside := t.TempDir()
			if err := os.Symlink(outside, filepath.Join(request.WorkDir, "link")); err != nil {
				t.Fatal(err)
			}
			request.ID, request.Command, request.Phase = "state-escape", "run_phase", "src_unpack"
			request.SaveEnvironment = filepath.Join(request.TempDir, "phase.environment")
			events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
			if err == nil {
				t.Fatalf("accepted source escape: %+v", events)
			}
			if _, err := os.Stat(filepath.Join(outside, "escaped")); !os.IsNotExist(err) {
				t.Fatalf("created directory outside WORKDIR: %v", err)
			}
		})
	}
}

func TestPhaseStateDoesNotReuseFailedSnapshot(t *testing.T) {
	request := phaseStateRequest(t, `src_unpack() { mkdir -p "$S"; PHASE_VALUE=good; }
src_prepare() { PHASE_VALUE=bad; die 'prepare failed'; }`)
	runPhaseStateSequence(t, request, true, "src_unpack")
	overlay := filepath.Join(request.TempDir, "phase.environment")
	before, err := os.ReadFile(overlay)
	if err != nil {
		t.Fatal(err)
	}
	request.ID, request.Command, request.Phase = "state-failed", "run_phase", "src_prepare"
	request.EnvironmentOverlay, request.SaveEnvironment = overlay, overlay
	if _, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err == nil {
		t.Fatal("failed phase succeeded")
	}
	after, err := os.ReadFile(overlay)
	if err != nil || string(after) != string(before) || strings.Contains(string(after), `PHASE_VALUE="bad"`) {
		t.Fatalf("failed phase replaced the good snapshot: %v", err)
	}
}

func TestPhaseStatePathsAreRemappedIntoIsolation(t *testing.T) {
	for _, test := range []struct {
		name, input, output, wantInput, wantOutput string
		wantBindings                               int
	}{
		{"work tree", "/host/work/state", "/host/work/state", "/run/arise/work/state", "/run/arise/work/state", 0},
		{"temporary directory", "/host/temp/state", "/host/temp/state", "/run/arise/temp/state", "/run/arise/temp/state", 0},
		{"external atomic output", "/other/state", "/other/state", "/run/arise/phase-output/state", "/run/arise/phase-output/state", 1},
		{"separate external input", "/input/state", "/output/state", "/run/arise/phase-input", "/run/arise/phase-output/state", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := Request{WorkDir: "/host/work", TempDir: "/host/temp", EnvironmentOverlay: test.input, SaveEnvironment: test.output}
			request := original
			request.WorkDir, request.TempDir = "/run/arise/work", "/run/arise/temp"
			var bindings [][3]string
			remapPhaseEnvironmentPaths(&request, original, func(mode, source, target string) { bindings = append(bindings, [3]string{mode, source, target}) })
			if request.EnvironmentOverlay != test.wantInput || request.SaveEnvironment != test.wantOutput || len(bindings) != test.wantBindings {
				t.Fatalf("mapped input=%q output=%q bindings=%v", request.EnvironmentOverlay, request.SaveEnvironment, bindings)
			}
			for _, binding := range bindings {
				if binding[2] == "/run/arise/phase-output" && (binding[0] != "--bind" || binding[1] != filepath.Dir(test.output)) {
					t.Fatalf("atomic output parent is not writable: %v", binding)
				}
			}
		})
	}
}

func TestPhaseStateDoesNotSavePretendEnvironment(t *testing.T) {
	request := phaseStateRequest(t, `pkg_pretend() { PHASE_VALUE=pretend; }`)
	request.ID, request.Command, request.Phase = "state-pretend", "run_phase", "pkg_pretend"
	request.SaveEnvironment = filepath.Join(request.TempDir, "phase.environment")
	if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
		t.Fatalf("pkg_pretend failed: %v; events=%+v", err, events)
	}
	if _, err := os.Stat(request.SaveEnvironment); !os.IsNotExist(err) {
		t.Fatalf("pkg_pretend published build state: %v", err)
	}
	request.Command, request.Phase, request.Phases = "run_phases", "", []string{"pkg_pretend", "pkg_setup"}
	if err := request.Validate(); err == nil {
		t.Fatal("request allowed pkg_pretend to leak state through a shared build shell")
	}
}

func TestPhaseStatePreservesUnsetConfiguredVariable(t *testing.T) {
	request := phaseStateRequest(t, `src_unpack() { mkdir -p "$S"; unset CFLAGS; }
src_prepare() { printf '%s' "${CFLAGS-unset}" > "$T/observed"; }`)
	request.Env = map[string]string{"CFLAGS": "-O2"}
	runPhaseStateSequence(t, request, true, "src_unpack", "src_prepare")
	data, err := os.ReadFile(filepath.Join(request.TempDir, "observed"))
	if err != nil || string(data) != "unset" {
		t.Fatalf("configured value was resurrected after unset: %q, %v", data, err)
	}
}

func TestPhaseStateRejectsReplacingDefaultSourceWithEscapingSymlink(t *testing.T) {
	request := phaseStateRequest(t, `src_unpack() { ln -s "$AUDIT_OUTSIDE" "$S" || die; }`)
	request.Env = map[string]string{"AUDIT_OUTSIDE": t.TempDir()}
	request.ID, request.Command, request.Phase = "default-source-escape", "run_phase", "src_unpack"
	if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err == nil {
		t.Fatalf("accepted an escaping symlink in the default source directory: %+v", events)
	}
}

func TestPhaseStateRestoresCurrentRuntimePathsFromInstalledSnapshot(t *testing.T) {
	request := phaseStateRequest(t, `pkg_preinst() {
	printf '%s|%s|%s|%s|%s' "$HOME" "$TMPDIR" "$PORTAGE_BUILDDIR" "$PORTAGE_LOG_FILE" "$PATH" > "$T/observed"
}`)
	environment := filepath.Join(request.TempDir, "installed.environment")
	ebuild, err := os.ReadFile(request.Ebuild)
	if err != nil {
		t.Fatal(err)
	}
	state := "export HOME=/gone/home TMPDIR=/gone/temp PORTAGE_BUILDDIR=/gone/build PORTAGE_LOG_FILE=/gone/log PATH=/gone/bin\n" + string(ebuild)
	if err := os.WriteFile(environment, []byte(state), 0644); err != nil {
		t.Fatal(err)
	}
	request.Environment, request.HomeDir, request.LogFile = environment, filepath.Join(request.TempDir, "home"), filepath.Join(request.TempDir, "current.log")
	request.ID, request.Command, request.Phase = "installed-runtime-paths", "run_phase", "pkg_preinst"
	if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
		t.Fatalf("installed phase failed: %v; events=%+v", err, events)
	}
	data, err := os.ReadFile(filepath.Join(request.TempDir, "observed"))
	want := strings.Join([]string{request.HomeDir, request.TempDir, request.BuildDir, request.LogFile, "/usr/sbin:/usr/bin:/sbin:/bin"}, "|")
	if err != nil || string(data) != want {
		t.Fatalf("runtime paths = %q, %v; want %q", data, err, want)
	}
}

func TestPhaseStatePreservesStderrWhenWorkerExitContradictsResult(t *testing.T) {
	request := Request{ID: "stderr-state"}
	command := exec.CommandContext(context.Background(), "bash", "-c", `printf '%s\n' '{"protocol":1,"id":"stderr-state","sequence":0,"kind":"result","exit_code":0}'; printf 'worker boundary failed\n' >&2; exit 1`)
	if _, err := runWorkerCommand(command, request); err == nil || !strings.Contains(err.Error(), "worker boundary failed") {
		t.Fatalf("worker failure lost its stderr: %v", err)
	}
}

func TestPhaseStateIdentifierAndVisibilityAcrossWorkers(t *testing.T) {
	for _, eapi := range []string{"7", "8", "9"} {
		t.Run("EAPI-"+eapi, func(t *testing.T) {
			request := phaseStateRequestForEAPI(t, eapi, `src_unpack() { mkdir -p "$S"; }
src_prepare() {
	[[ $EBUILD_PHASE == prepare && $EBUILD_PHASE_FUNC == src_prepare ]] || die 'wrong phase identity'
	[[ ${EBUILD_PHASE_FUNC@a} == *r* ]] || die 'phase identity is mutable'
	if [[ $EAPI == 9 ]]; then
		[[ ${S@a} != *x* && ${EBUILD_PHASE_FUNC@a} != *x* ]] || die 'EAPI 9 variables were exported'
	else
		[[ ${S@a} == *x* && ${EBUILD_PHASE_FUNC@a} == *x* ]] || die 'phase variables were not exported'
	fi
}`)
			runPhaseStateSequence(t, request, true, "src_unpack", "src_prepare")
		})
	}
}

func TestPhaseStateMissingSourcesFailBeforeExplicitPrepare(t *testing.T) {
	request := phaseStateRequest(t, `src_unpack() { :; }
src_prepare() { printf ran > "$T/observed"; }`)
	runPhaseStateSequence(t, request, true, "src_unpack")
	request.ID, request.Command, request.Phase = "missing-source", "run_phase", "src_prepare"
	request.EnvironmentOverlay = filepath.Join(request.TempDir, "phase.environment")
	if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err == nil {
		t.Fatalf("missing source directory was accepted: %+v", events)
	}
	if _, err := os.Stat(filepath.Join(request.TempDir, "observed")); !os.IsNotExist(err) {
		t.Fatalf("prepare ran without its source directory: %v", err)
	}
}

func TestPhaseStateRejectsReadonlyIsolationOverride(t *testing.T) {
	request := phaseStateRequest(t, `readonly SANDBOX_WRITE=/untrusted
src_unpack() { printf ran > "$T/observed"; }`)
	request.ID, request.Command, request.Phase = "readonly-isolation", "run_phase", "src_unpack"
	request.Env = map[string]string{"SANDBOX_WRITE": request.WorkDir}
	if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err == nil {
		t.Fatalf("readonly sandbox override was accepted: %+v", events)
	}
	if _, err := os.Stat(filepath.Join(request.TempDir, "observed")); !os.IsNotExist(err) {
		t.Fatalf("phase ran with overridden isolation controls: %v", err)
	}
}
