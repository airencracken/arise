package phaseproto

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequiredHelperFailureStopsPhase(t *testing.T) {
	for _, tc := range []struct{ helper, command string }{
		{"eapply_user", "eapply_user"},
		{"unpack", "unpack absent.tar"},
		{"emake", "MAKE=false emake"},
		{"doins", "doins missing"},
		{"dobin", "dobin missing"},
		{"newexe", "newexe missing tool"},
		{"dodoc", "dodoc missing"},
		{"doman", "doman missing.1"},
		{"domo", "domo"},
		{"dosym", "touch \"$ED/file\" && dosym target /file/link"},
		{"fperms", "fperms 0755 /missing"},
	} {
		t.Run(tc.helper, func(t *testing.T) {
			phase := "src_install"
			if tc.helper == "eapply_user" {
				phase = "src_prepare"
			}
			if tc.helper == "unpack" {
				phase = "src_unpack"
			}
			if tc.helper == "emake" {
				phase = "src_compile"
			}
			r := phaseStateRequest(t, phase+"() { "+tc.command+"; echo continued > \"$T/continued\"; }")
			if err := os.Mkdir(r.SourceDir, 0755); err != nil {
				t.Fatal(err)
			}
			if tc.helper == "eapply_user" {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "broken.patch"), []byte("invalid patch\n"), 0644); err != nil {
					t.Fatal(err)
				}
				r.UserPatchDirs = []string{dir}
			}
			r.ID, r.Command, r.Phase = "required-helper", "run_phase", phase
			events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), r)
			if err == nil {
				t.Fatalf("ignored %s failure: %+v", tc.helper, events)
			}
			if _, err := os.Stat(filepath.Join(r.TempDir, "continued")); !os.IsNotExist(err) {
				t.Fatalf("phase continued after %s: %v", tc.helper, err)
			}
		})
	}
}

func TestNonfatalBuildAndInstallHelpers(t *testing.T) {
	r := phaseStateRequest(t, `src_unpack() { mkdir -p "$S"; }
src_install() {
	nonfatal doins missing && die "missing input accepted"
	nonfatal newinitd missing daemon && die "missing service accepted"
	MAKE=false nonfatal emake && die "failed make succeeded"
	echo recovered > "$T/result"
}`)
	runPhaseStateSequence(t, r, false, "src_unpack", "src_install")
	got, err := os.ReadFile(filepath.Join(r.TempDir, "result"))
	if err != nil || strings.TrimSpace(string(got)) != "recovered" {
		t.Fatalf("result %q: %v", got, err)
	}
}

func TestSubshellDieCannotBeMasked(t *testing.T) {
	for _, command := range []string{"(doins missing)", "ignored=$(die failure)"} {
		r := phaseStateRequest(t, "src_install() { "+command+"; :; }")
		if err := os.Mkdir(r.SourceDir, 0755); err != nil {
			t.Fatal(err)
		}
		r.ID, r.Command, r.Phase = "subshell-failure", "run_phase", "src_install"
		r.SaveEnvironment = filepath.Join(r.TempDir, "snapshot")
		if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), r); err == nil {
			t.Fatalf("accepted %s: %+v", command, events)
		}
		if _, err := os.Stat(r.SaveEnvironment); !os.IsNotExist(err) {
			t.Fatalf("committed failed phase: %v", err)
		}
	}
}

func TestDefaultTestFallsBackToAvailableMakeTarget(t *testing.T) {
	r := phaseStateRequest(t, `src_unpack() { mkdir -p "$S"; printf 'test:\n\t@echo tested > "$(T)/result"\n' > "$S/Makefile"; }`)
	runPhaseStateSequence(t, r, false, "src_unpack", "src_test")
	got, err := os.ReadFile(filepath.Join(r.TempDir, "result"))
	if err != nil || string(got) != "tested\n" {
		t.Fatalf("test target result %q: %v", got, err)
	}
}

func TestBannedHelpersStopPhase(t *testing.T) {
	for _, tc := range []struct{ eapi, call string }{
		{"7", "dohard /src /dst"}, {"8", "dohtml doc.html"}, {"8", "dosed /foo"}, {"8", "dolib lib.a"}, {"8", "libopts -m0644"}, {"9", "domo example.mo"},
	} {
		t.Run(tc.call, func(t *testing.T) {
			r := phaseStateRequestForEAPI(t, tc.eapi, "src_install() { "+tc.call+"; echo continued > \"$T/continued\"; }")
			if err := os.Mkdir(r.SourceDir, 0755); err != nil {
				t.Fatal(err)
			}
			r.ID, r.Command, r.Phase = "banned-helper", "run_phase", "src_install"
			if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), r); err == nil {
				t.Fatalf("accepted %s in EAPI %s: %+v", tc.call, tc.eapi, events)
			}
			if _, err := os.Stat(filepath.Join(r.TempDir, "continued")); !os.IsNotExist(err) {
				t.Fatalf("phase continued after %s: %v", tc.call, err)
			}
		})
	}
}
