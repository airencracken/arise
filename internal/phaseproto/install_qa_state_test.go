package phaseproto

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallQAChecksCannotChangePackageShellState(t *testing.T) {
	request := phaseStateRequest(t, `PACKAGE_VALUE=package
package_helper() { printf package; }
src_unpack() { mkdir -p "$S"; }
src_install() { printf installed > "$ED/payload"; }
pkg_preinst() {
	[[ $PACKAGE_VALUE == package && $(package_helper) == package ]] || die 'QA changed package state'
	[[ ! ${QA_LEAK+present} ]] || die 'QA variable leaked'
	! declare -F qa_helper >/dev/null || die 'QA function leaked'
	! shopt -q extglob || die 'QA shell option leaked'
	printf passed > "$T/observed"
}`)
	checks := []string{
		`PACKAGE_VALUE=qa
readonly QA_LEAK=qa
package_helper() { printf qa; }
shopt -s extglob
qa_helper() { case $1 in /..?(/*)) printf qa;; esac; }
printf qa > "$ED/qa-payload"
cd "$T" || die
`,
		`[[ $PACKAGE_VALUE == package && $(package_helper) == package ]] || die 'earlier QA changed package state'
[[ ! ${QA_LEAK+present} ]] || die 'earlier QA variable leaked'
! declare -F qa_helper >/dev/null || die 'earlier QA function leaked'
! shopt -q extglob || die 'earlier QA shell option leaked'
[[ $PWD == "$S" ]] || die 'earlier QA directory change leaked'
printf independent > "$ED/independent-check"
`,
	}
	for index, content := range checks {
		path := filepath.Join(request.TempDir, []string{"10-mutate", "20-observe"}[index])
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		request.InstallQAChecks = append(request.InstallQAChecks, path)
	}
	runPhaseStateSequence(t, request, true, "src_unpack", "src_install", "pkg_preinst")
	for name, want := range map[string]string{"payload": "installed", "qa-payload": "qa", "independent-check": "independent"} {
		data, err := os.ReadFile(filepath.Join(request.ImageDir, name))
		if err != nil || string(data) != want {
			t.Fatalf("%s = %q, %v; want %q", name, data, err, want)
		}
	}
}

func TestInstallQACheckFailureReportsAndContinues(t *testing.T) {
	for _, failure := range []string{"return 3", "exit 7", "die 'QA probe failed'"} {
		t.Run(failure, func(t *testing.T) {
			request := phaseStateRequest(t, `src_install() { printf installed > "$ED/payload"; }`)
			if err := os.MkdirAll(request.SourceDir, 0755); err != nil {
				t.Fatal(err)
			}
			for name, content := range map[string]string{
				"10-fail":  "readonly QA_FAILURE_LEAK=bad\n" + failure + "\n",
				"20-after": "[[ ! ${QA_FAILURE_LEAK+present} ]] || die 'failed QA leaked state'\nprintf checked > \"$ED/after-check\"\n",
			} {
				if err := os.WriteFile(filepath.Join(request.TempDir, name), []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			request.InstallQAChecks = []string{filepath.Join(request.TempDir, "10-fail"), filepath.Join(request.TempDir, "20-after")}
			request.ID, request.Command, request.Phase = "qa-failure", "run_phase", "src_install"
			events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
			if err != nil {
				t.Fatalf("advisory QA terminated the package phase: %v; events=%+v", err, events)
			}
			found := false
			for _, event := range events {
				if event.Kind == "elog" && event.Class == "ERROR" && strings.Contains(event.Message, "10-fail failed to run") {
					found = true
				}
			}
			if !found {
				t.Fatalf("QA failure was not reported: %+v", events)
			}
			if data, err := os.ReadFile(filepath.Join(request.ImageDir, "after-check")); err != nil || string(data) != "checked" {
				t.Fatalf("subsequent check = %q, %v", data, err)
			}
		})
	}
}

func TestSavedEnvironmentParsesExtendedFunctionsWithoutChangingPhaseOptions(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(map[bool]string{false: "build snapshot", true: "installed environment"}[installed], func(t *testing.T) {
			request := phaseStateRequest(t, "")
			state := `EAPI=8
extended_helper() { case $1 in /..?(/*)) printf matched;; esac; }
pkg_preinst() {
	! shopt -q extglob || die 'snapshot parser option leaked into hook'
	declare -F extended_helper >/dev/null || die 'saved function was lost'
	shopt -s extglob
	[[ $(extended_helper /../path) == matched ]] || die 'saved function changed'
	shopt -u extglob
	printf passed > "$T/observed"
}
`
			path := filepath.Join(request.TempDir, "existing.environment")
			if err := os.WriteFile(path, []byte(state), 0644); err != nil {
				t.Fatal(err)
			}
			if installed {
				request.Environment = path
			} else {
				request.EnvironmentOverlay = path
			}
			runPhaseStateSequence(t, request, true, "pkg_preinst")
			if data, err := os.ReadFile(filepath.Join(request.TempDir, "observed")); err != nil || string(data) != "passed" {
				t.Fatalf("hook = %q, %v", data, err)
			}
		})
	}
}
