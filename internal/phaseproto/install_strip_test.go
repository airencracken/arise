package phaseproto

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Keep a real object outside the image: thin archives reference their members
// rather than containing them. Finalization must not modify those members.
func installStripFixtures(t *testing.T) string {
	t.Helper()
	for _, tool := range []string{"cc", "ar", "nm", "file", "readelf", "strip", "ranlib"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("ARISE_REQUIRE_PHASE_CONTRACT") == "1" {
				t.Fatalf("required strip fixture tool unavailable: %s", tool)
			}
			t.Skipf("%s is unavailable", tool)
		}
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "probe.c")
	if err := os.WriteFile(source, []byte("static int fixture_local = 7;\nint fixture_exported(void) { return fixture_local; }\nint main(void) { return fixture_exported() != 7; }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	commands := [][]string{
		{"cc", "-g", "-fPIC", "-c", source, "-o", filepath.Join(directory, "member.o")},
		{"cc", "-g", source, "-o", filepath.Join(directory, "executable")},
		{"cc", "-shared", filepath.Join(directory, "member.o"), "-o", filepath.Join(directory, "shared.so")},
		{"ar", "crs", filepath.Join(directory, "regular.a"), filepath.Join(directory, "member.o")},
		{"ar", "crsT", filepath.Join(directory, "thin.a"), filepath.Join(directory, "member.o")},
	}
	for _, arguments := range commands {
		if output, err := exec.Command(arguments[0], arguments[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", arguments, err, output)
		}
	}
	return directory
}

func TestInstallStripClassifiesObjectsAndArchives(t *testing.T) {
	fixtures := installStripFixtures(t)
	for _, name := range []string{"executable", "shared.so", "member.o", "regular.a", "thin.a", "excluded module.ko"} {
		t.Run(name, func(t *testing.T) {
			request := phaseStateRequest(t, `src_install() {
	insinto /usr/lib/probe
	newins "$WORKDIR/input" payload
	if [[ $EXCLUDE_PROBE == yes ]]; then dostrip -x /usr/lib/probe; fi
}`)
			if err := os.MkdirAll(request.SourceDir, 0755); err != nil {
				t.Fatal(err)
			}
			inputName := name
			if name == "excluded module.ko" {
				inputName = "member.o"
				request.Env = map[string]string{"EXCLUDE_PROBE": "yes"}
			}
			original, err := os.ReadFile(filepath.Join(fixtures, inputName))
			if err != nil {
				t.Fatal(err)
			}
			member, err := os.ReadFile(filepath.Join(fixtures, "member.o"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(request.WorkDir, "input"), original, 0644); err != nil {
				t.Fatal(err)
			}
			request.Policy = ExecutionPolicy{Configured: true, Strip: true}
			runPhaseStateSequence(t, request, true, "src_install")
			installed := filepath.Join(request.ImageDir, "usr/lib/probe/payload")
			got, err := os.ReadFile(installed)
			if err != nil {
				t.Fatal(err)
			}
			if name == "thin.a" || name == "excluded module.ko" {
				if !bytes.Equal(got, original) {
					t.Fatal("finalization changed an excluded file or thin archive")
				}
			} else {
				sections, err := exec.Command("readelf", "-S", installed).CombinedOutput()
				if err != nil || strings.Contains(string(sections), ".debug_info") {
					t.Fatalf("debug sections remain: %v\n%s", err, sections)
				}
				if name == "regular.a" {
					symbols, err := exec.Command("nm", "-a", installed).CombinedOutput()
					if err != nil || !strings.Contains(string(symbols), "fixture_local") || !strings.Contains(string(symbols), "fixture_exported") {
						t.Fatalf("archive symbols were lost: %v\n%s", err, symbols)
					}
					linked := filepath.Join(request.TempDir, "linked")
					if output, err := exec.Command("cc", "-x", "c", "/dev/null", "-x", "none", installed, "-o", linked).CombinedOutput(); err != nil {
						t.Fatalf("stripped archive cannot be linked: %v\n%s", err, output)
					}
					if output, err := exec.Command(linked).CombinedOutput(); err != nil {
						t.Fatalf("linked archive changed behavior: %v\n%s", err, output)
					}
				}
			}
			after, err := os.ReadFile(filepath.Join(fixtures, "member.o"))
			if err != nil || !bytes.Equal(after, member) {
				t.Fatalf("finalization changed a member outside the image: %v", err)
			}
		})
	}
}

func TestInstallQAVerboseHelperIsQuietAwareAndIsolated(t *testing.T) {
	for _, quiet := range []string{"0", "1"} {
		t.Run("quiet="+quiet, func(t *testing.T) {
			request := phaseStateRequest(t, `src_install() { :; }
pkg_preinst() {
	! declare -F __vecho >/dev/null || die 'QA verbose helper leaked'
	! declare -F __quiet_mode >/dev/null || die 'QA quiet helper leaked'
}`)
			if err := os.MkdirAll(request.SourceDir, 0755); err != nil {
				t.Fatal(err)
			}
			check := filepath.Join(request.TempDir, "10-verbose")
			if err := os.WriteFile(check, []byte(`__vecho -ne 'QA probe\n' > "$ED/qa-stdout" 2> "$ED/qa-stderr" || die 'missing verbose helper'
`), 0644); err != nil {
				t.Fatal(err)
			}
			request.InstallQAChecks = []string{check}
			request.Env = map[string]string{"PORTAGE_QUIET": quiet}
			runPhaseStateSequence(t, request, true, "src_install", "pkg_preinst")
			for _, stream := range []string{"stdout", "stderr"} {
				want := ""
				if quiet == "0" && stream == "stderr" {
					want = "QA probe\n"
				}
				got, err := os.ReadFile(filepath.Join(request.ImageDir, "qa-"+stream))
				if err != nil || string(got) != want {
					t.Fatalf("QA %s = %q, %v; want %q", stream, got, err, want)
				}
			}
		})
	}
}
