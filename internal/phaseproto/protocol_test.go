package phaseproto

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/distfiles"
)

func TestBashWorkerImageAndArchiveHelperABI(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source")
	image := filepath.Join(directory, "image")
	temporary := filepath.Join(directory, "temp")
	for _, path := range []string{filepath.Join(source, "payload"), filepath.Join(source, "docs"), image, temporary} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "payload", "program"), []byte("program"), 0o644); err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte("changes\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "docs", "changes.gz"), compressed.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := `EAPI=8
S="${WORKDIR}"
src_prepare() { default; unpack docs/changes.gz || die; }
src_install() {
  insinto /opt/example
  doins payload/program changes
  fperms +x /opt/example/program
  dosym ../../opt/example/program /usr/bin/example
}
`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"src_prepare", "src_install"} {
		request := Request{Protocol: Version, ID: "helpers-" + phase, Command: "run_phase", Phase: phase, EAPI: "8", Ebuild: ebuild, WorkDir: source, SourceDir: source, ImageDir: image, TempDir: temporary, UserPatchDirs: []string{filepath.Join(directory, "missing-patches")}}
		// Missing patch directories are ignored by the worker's explicit list;
		// policy construction filters them before production requests.
		request.UserPatchDirs = nil
		events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
		if err != nil {
			t.Fatalf("%s: %v; events=%#v", phase, err, events)
		}
	}
	program := filepath.Join(image, "opt", "example", "program")
	info, err := os.Stat(program)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("program mode = %o, want executable", info.Mode().Perm())
	}
	data, err := os.ReadFile(filepath.Join(image, "opt", "example", "changes"))
	if err != nil || string(data) != "changes\n" {
		t.Fatalf("installed changes = %q, error=%v", data, err)
	}
	target, err := os.Readlink(filepath.Join(image, "usr", "bin", "example"))
	if err != nil || target != "../../opt/example/program" {
		t.Fatalf("installed symlink = %q, error=%v", target, err)
	}
}

func TestBashWorkerUnpackUsesWorkDirAndLifecycleDefaultsAreNoOps(t *testing.T) {
	directory := t.TempDir()
	work := filepath.Join(directory, "work")
	source := filepath.Join(work, "pkg-1")
	image := filepath.Join(directory, "image")
	for _, path := range []string{work, image} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_unpack() { [[ $PWD == $WORKDIR ]] || return 41; mkdir -p \"$S\"; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := Request{Protocol: Version, EAPI: "8", Ebuild: ebuild, WorkDir: work, SourceDir: source, ImageDir: image}
	for _, phase := range []string{"src_unpack", "pkg_setup", "pkg_preinst", "pkg_postinst", "pkg_prerm", "pkg_postrm"} {
		request := base
		request.ID, request.Command, request.Phase = "directory-"+phase, "run_phase", phase
		if events, err := RunBashWorker(context.Background(), request); err != nil {
			t.Fatalf("%s: %v; events=%#v", phase, err, events)
		}
	}
}

func TestBashWorkerPhaseBatchPreservesEclassStateAndDieIsTerminal(t *testing.T) {
	directory := t.TempDir()
	work, source, image := filepath.Join(directory, "work"), filepath.Join(directory, "source"), filepath.Join(directory, "image")
	for _, path := range []string{work, source, image} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte(`EAPI=8
src_prepare() { PREPARED=yes; }
src_configure() { [[ ${PREPARED-} == yes ]] || die "prepare state lost"; }
src_compile() { printf compiled > "${T}/compiled"; }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "phase-batch", Command: "run_phases", Phases: []string{"src_prepare", "src_configure", "src_compile"}, EAPI: "8", Ebuild: ebuild, WorkDir: work, SourceDir: source, ImageDir: image, TempDir: directory}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("stateful phase batch: %v; events=%#v", err, events)
	}
	if _, err := os.Stat(filepath.Join(directory, "compiled")); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_prepare() { die fatal; printf leaked > \"${T}/leaked\"; }\nsrc_configure() { printf later > \"${T}/later\"; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request.ID = "phase-batch-die"
	if _, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err == nil {
		t.Fatal("die returned successful batch")
	}
	for _, name := range []string{"leaked", "later"} {
		if _, err := os.Stat(filepath.Join(directory, name)); !os.IsNotExist(err) {
			t.Fatalf("%s executed after die: %v", name, err)
		}
	}
}

func TestBashWorkerHasUsesExactArgumentMembership(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := `EAPI=8
src_compile() {
  has 8 7 8 || return 31
  ! has 8 7 80 || return 32
  ! has 8 || return 33
}`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "has-contract", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild, WorkDir: directory, SourceDir: directory}
	if events, err := RunBashWorker(context.Background(), request); err != nil {
		t.Fatalf("has contract: %v; events=%#v", err, events)
	}
}

func TestRequestValidation(t *testing.T) {
	request := Request{Protocol: Version, ID: "pkg-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: "/repo/cat/pkg/pkg-1.ebuild"}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	request.Protocol++
	if err := request.Validate(); err == nil {
		t.Fatal("future protocol version accepted")
	}
}

func TestRequestRejectsUnsupportedEAPIAndRelativeEclassDirectory(t *testing.T) {
	request := Request{Protocol: Version, ID: "pkg-1", Command: "run_phase", Phase: "src_compile", EAPI: "9", Ebuild: "/repo/pkg.ebuild"}
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported EAPI") {
		t.Fatalf("EAPI error = %v", err)
	}
	request.EAPI = "8"
	request.EclassDirs = []string{"relative/eclass"}
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("eclass directory error = %v", err)
	}
}

func TestRequestRejectsRelativeExecutionDirectories(t *testing.T) {
	base := Request{Protocol: Version, ID: "pkg-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: "/repo/pkg.ebuild"}
	for name, mutate := range map[string]func(*Request){
		"root":      func(r *Request) { r.RootDir = "root" },
		"sysroot":   func(r *Request) { r.SysrootDir = "sysroot" },
		"broot":     func(r *Request) { r.BrootDir = "broot" },
		"temporary": func(r *Request) { r.TempDir = "tmp" },
		"home":      func(r *Request) { r.HomeDir = "home" },
	} {
		t.Run(name, func(t *testing.T) {
			request := base
			mutate(&request)
			if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "must be absolute") {
				t.Fatalf("relative %s error = %v", name, err)
			}
		})
	}
}

func TestDefaultPhasesDeclareSupportedEAPIs(t *testing.T) {
	for _, eapi := range []string{"7", "8"} {
		phases, err := DefaultPhases(eapi)
		if err != nil || len(phases) != 11 || phases[2] != "src_prepare" {
			t.Fatalf("EAPI %s defaults = %#v, %v", eapi, phases, err)
		}
	}
	if _, err := DefaultPhases("6"); err == nil {
		t.Fatal("unsupported EAPI defaults accepted")
	}
}

func TestSupportedEAPIDefaultAndLifecycleMatrix(t *testing.T) {
	for _, eapi := range []string{"7", "8"} {
		t.Run("EAPI-"+eapi, func(t *testing.T) {
			directory := t.TempDir()
			work := filepath.Join(directory, "work")
			source := filepath.Join(work, "pkg-1")
			image := filepath.Join(directory, "image")
			for _, path := range []string{work, source, image} {
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			ebuild := filepath.Join(directory, "pkg-1.ebuild")
			if err := os.WriteFile(ebuild, []byte("EAPI="+eapi+"\nA=\nPF=pkg-1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			phases, err := DefaultPhases(eapi)
			if err != nil {
				t.Fatal(err)
			}
			for _, phase := range phases {
				request := Request{
					Protocol:  Version,
					ID:        "matrix-" + eapi + "-" + phase,
					Command:   "run_phase",
					Phase:     phase,
					EAPI:      eapi,
					Ebuild:    ebuild,
					WorkDir:   work,
					SourceDir: source,
					ImageDir:  image,
				}
				events, runErr := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
				if runErr != nil {
					t.Fatalf("%s: %v; events=%#v", phase, runErr, events)
				}
				if len(events) != 2 || events[0].Kind != "phase" || events[0].Message != phase || events[1].Kind != "result" || events[1].ExitCode == nil || *events[1].ExitCode != 0 {
					t.Fatalf("%s events = %#v", phase, events)
				}
			}
		})
	}
}

func TestDecoderRequiresOrderedTerminalResult(t *testing.T) {
	stream := strings.NewReader("" +
		`{"protocol":1,"id":"pkg-1","sequence":0,"kind":"phase","message":"src_compile"}` + "\n" +
		`{"protocol":1,"id":"pkg-1","sequence":1,"kind":"log","stream":"stdout","message":"building"}` + "\n" +
		`{"protocol":1,"id":"pkg-1","sequence":2,"kind":"result","exit_code":0}` + "\n")
	decoder := NewDecoder(stream, "pkg-1")
	for range 3 {
		if _, err := decoder.Next(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := decoder.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("post-result decode = %v, want EOF", err)
	}
}

func TestDecoderRejectsCrossTalkAndSequenceGaps(t *testing.T) {
	for _, input := range []string{
		`{"protocol":1,"id":"other","sequence":0,"kind":"log"}`,
		`{"protocol":1,"id":"pkg-1","sequence":2,"kind":"log"}`,
		`{"protocol":1,"id":"pkg-1","sequence":0,"kind":"result"}`,
		`{"protocol":1,"id":"pkg-1","sequence":0,"kind":"unknown"}`,
	} {
		if _, err := NewDecoder(strings.NewReader(input), "pkg-1").Next(); err == nil {
			t.Fatalf("invalid event accepted: %s", input)
		}
	}
}

func TestWorkerProtocolRequiresExactlyOneTerminalResult(t *testing.T) {
	request := Request{Protocol: Version, ID: "terminal-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: "/tmp/pkg.ebuild"}
	event := func(sequence int, kind string, exit string) string {
		field := ""
		if exit != "" {
			field = `,"exit_code":` + exit
		}
		return fmt.Sprintf(`{"protocol":1,"id":"terminal-1","sequence":%d,"kind":"%s"%s}`, sequence, kind, field)
	}
	tests := []struct {
		name   string
		output string
	}{
		{name: "missing result", output: event(0, "phase", "")},
		{name: "duplicate result", output: event(0, "result", "0") + "\n" + event(1, "result", "0")},
		{name: "trailing garbage", output: event(0, "result", "0") + "\nnot-json"},
		{name: "status mismatch", output: event(0, "result", "7")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command := exec.CommandContext(context.Background(), "bash", "-c", "printf '%s' \"$PAYLOAD\"")
			request.Env = map[string]string{"PAYLOAD": tt.output}
			if events, err := runWorkerCommand(command, request); err == nil {
				t.Fatalf("invalid terminal stream accepted: %#v", events)
			}
		})
	}
}

func TestBashWorkerHandshakeAndOrderedLogs(t *testing.T) {
	ebuild := filepath.Join(t.TempDir(), "pkg-1.ebuild")
	content := "EAPI=8\nsrc_compile() { printf 'hello from phase\\n'; }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "cat-pkg-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild}
	command := exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker)
	events, err := runWorkerCommand(command, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Kind != "phase" || events[1].Kind != "log" || events[1].Message != "hello from phase" || events[2].Kind != "result" || events[2].ExitCode == nil || *events[2].ExitCode != 0 {
		t.Fatalf("worker events = %#v", events)
	}
}

func TestBashWorkerEmitsTypedElogClasses(t *testing.T) {
	ebuild := filepath.Join(t.TempDir(), "pkg-1.ebuild")
	content := "EAPI=8\nsrc_compile() { einfo info; elog log; ewarn warn; eerror error; eqawarn qa; }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "elog-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatal(err)
	}
	var classes []string
	for _, event := range events {
		if event.Kind == "elog" {
			classes = append(classes, event.Class+":"+event.Message)
		}
	}
	want := []string{"INFO:info", "LOG:log", "WARN:warn", "ERROR:error", "QA:qa"}
	if !reflect.DeepEqual(classes, want) {
		t.Fatalf("elog classes = %#v, want %#v; events=%#v", classes, want, events)
	}
}

func TestEveryDeclaredPhaseFailurePreservesDurableLog(t *testing.T) {
	for _, eapi := range []string{"7", "8"} {
		phases, err := DefaultPhases(eapi)
		if err != nil {
			t.Fatal(err)
		}
		for _, phaseName := range phases {
			t.Run("EAPI-"+eapi+"-"+phaseName, func(t *testing.T) {
				directory := t.TempDir()
				ebuild := filepath.Join(directory, "pkg-1.ebuild")
				content := fmt.Sprintf("EAPI=%s\n%s() { printf 'before failure in %s\\n'; return 23; }\n", eapi, phaseName, phaseName)
				if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
				request := Request{Protocol: Version, ID: "failure-" + eapi + "-" + phaseName, Command: "run_phase", Phase: phaseName, EAPI: eapi, Ebuild: ebuild, WorkDir: directory, SourceDir: directory, ImageDir: filepath.Join(directory, "image"), TempDir: filepath.Join(directory, "temp")}
				for _, path := range []string{request.ImageDir, request.TempDir} {
					if err := os.MkdirAll(path, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				events, runErr := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
				if runErr == nil {
					t.Fatal("failing phase returned success")
				}
				manager, err := NewPackageLog(PackageLogOptions{Root: filepath.Join(directory, "logs"), TempDir: request.TempDir, Category: "cat", PF: "pkg-1"})
				if err != nil {
					t.Fatal(err)
				}
				_, persistErr := persistWorkerEvents(request, events, runErr, WorkerOptions{DurableLog: manager, FinalizeLog: true})
				if persistErr == nil || !strings.Contains(persistErr.Error(), manager.Path()) {
					t.Fatalf("persist error = %v", persistErr)
				}
				contentBytes, err := os.ReadFile(manager.Path())
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(contentBytes), "before failure in "+phaseName) || !strings.Contains(string(contentBytes), "exit_code=23") || !strings.Contains(string(contentBytes), "terminal-error") {
					t.Fatalf("durable failure log = %s", contentBytes)
				}
			})
		}
	}
}

func TestBashWorkerCancellationKillsProcessGroup(t *testing.T) {
	directory := t.TempDir()
	pidFile := filepath.Join(directory, "child.pid")
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := "EAPI=8\nsrc_compile() { ( trap '' TERM; printf '%s\\n' \"$BASHPID\" > \"$PID_FILE\"; while :; do sleep 1; done ) \u0026 wait; }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	request := Request{Protocol: Version, ID: "cancel-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild, Env: map[string]string{"PID_FILE": pidFile}}
	started := time.Now()
	_, err := runWorkerCommandWithCancelGrace(exec.CommandContext(ctx, "bash", "--noprofile", "--norc", "-c", bashWorker), request, 100*time.Millisecond)
	if err == nil {
		t.Fatal("cancelled worker returned success")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("worker cancellation took %s", elapsed)
	}
	rawPID, readErr := os.ReadFile(pidFile)
	if readErr != nil {
		t.Fatalf("child did not record PID: %v", readErr)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(rawPID)))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	deadline := time.Now().Add(time.Second)
	for {
		probeErr := syscall.Kill(pid, 0)
		if probeErr == syscall.ESRCH {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker descendant %d survived cancellation (probe error %v)", pid, probeErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBashWorkerReceivesControlledRootAndScratchContract(t *testing.T) {
	directory := t.TempDir()
	dirs := make(map[string]string)
	for _, name := range []string{"root", "sysroot", "broot", "temp", "home"} {
		dirs[name] = filepath.Join(directory, name)
		if err := os.MkdirAll(dirs[name], 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ebuild := filepath.Join(directory, "pkg.ebuild")
	content := "EAPI=8\nsrc_compile() { printf '%s|%s|%s|%s|%s|%s|%s|%s\\n' \"$ROOT\" \"$SYSROOT\" \"$BROOT\" \"$T\" \"$TMPDIR\" \"$TMP\" \"$TEMP\" \"$HOME\"; }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "roots-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild, RootDir: dirs["root"], SysrootDir: dirs["sysroot"], BrootDir: dirs["broot"], TempDir: dirs["temp"], HomeDir: dirs["home"]}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{dirs["root"], dirs["sysroot"], dirs["broot"], dirs["temp"], dirs["temp"], dirs["temp"], dirs["temp"], dirs["home"]}, "|")
	if len(events) != 3 || events[1].Message != want {
		t.Fatalf("controlled environment = %#v, want %q", events, want)
	}
}

func TestBashWorkerSourcesNestedEclassesAndExportedPhase(t *testing.T) {
	directory := t.TempDir()
	eclassDir := filepath.Join(directory, "eclass")
	if err := os.MkdirAll(eclassDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(eclassDir, "base.eclass"), []byte("BASE_VALUE=base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	derived := "inherit base\nderived_src_compile() { printf '%s-%s\\n' \"$BASE_VALUE\" derived; }\nEXPORT_FUNCTIONS src_compile\n"
	if err := os.WriteFile(filepath.Join(eclassDir, "derived.eclass"), []byte(derived), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\ninherit derived\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "inherit-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild, EclassDirs: []string{eclassDir}}
	command := exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker)
	events, err := runWorkerCommand(command, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[1].Message != "base-derived" {
		t.Fatalf("events = %#v", events)
	}
}

func TestBashWorkerDiscoversEbuildAndExportedEclassPhases(t *testing.T) {
	directory := t.TempDir()
	eclassDir := filepath.Join(directory, "eclass")
	if err := os.MkdirAll(eclassDir, 0o755); err != nil {
		t.Fatal(err)
	}
	eclass := "tool_src_compile() { :; }\nEXPORT_FUNCTIONS src_compile\n"
	if err := os.WriteFile(filepath.Join(eclassDir, "tool.eclass"), []byte(eclass), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\ninherit tool\nsrc_install() { :; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "discover-1", Command: "discover_phases", EAPI: "8", Ebuild: ebuild, EclassDirs: []string{eclassDir}}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatal(err)
	}
	var phases []string
	for _, event := range events {
		if event.Kind == "phase" {
			phases = append(phases, event.Message)
		}
	}
	if got := strings.Join(phases, " "); got != "src_compile src_install" {
		t.Fatalf("discovered phases = %q; events = %#v", got, events)
	}
}

func TestBashWorkerRejectsEAPIMismatchAfterSource(t *testing.T) {
	ebuild := filepath.Join(t.TempDir(), "pkg.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=9\nsrc_compile() { :; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "eapi-mismatch", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err == nil || len(events) != 2 || events[0].Kind != "log" || !strings.Contains(events[0].Message, "does not match") {
		t.Fatalf("events = %#v, error = %v", events, err)
	}
}

func TestDefaultSrcPrepareAppliesUserPatches(t *testing.T) {
	directory := t.TempDir()
	workDir := filepath.Join(directory, "work")
	patchDir := filepath.Join(directory, "patches")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(patchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	message := filepath.Join(workDir, "message.txt")
	if err := os.WriteFile(message, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patch := "--- a/message.txt\n+++ b/message.txt\n@@ -1 +1 @@\n-old\n+new\n"
	if err := os.WriteFile(filepath.Join(patchDir, "001-message.patch"), []byte(patch), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "prepare-1", Command: "run_phase", Phase: "src_prepare", EAPI: "8", Ebuild: ebuild, WorkDir: workDir, UserPatchDirs: []string{patchDir}}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("events = %#v: %v", events, err)
	}
	content, err := os.ReadFile(message)
	if err != nil || string(content) != "new\n" {
		t.Fatalf("message = %q, %v", content, err)
	}
}

func TestEapplyUserMoreSpecificBasenameOverridesGeneric(t *testing.T) {
	directory := t.TempDir()
	workDir := filepath.Join(directory, "work")
	generic := filepath.Join(directory, "generic")
	specific := filepath.Join(directory, "specific")
	for _, path := range []string{workDir, generic, specific} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	message := filepath.Join(workDir, "message.txt")
	if err := os.WriteFile(message, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	genericPatch := "--- a/message.txt\n+++ b/message.txt\n@@ -1 +1 @@\n-old\n+generic\n"
	specificPatch := "--- a/message.txt\n+++ b/message.txt\n@@ -1 +1 @@\n-old\n+specific\n"
	if err := os.WriteFile(filepath.Join(generic, "same.patch"), []byte(genericPatch), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specific, "same.patch"), []byte(specificPatch), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "prepare-override", Command: "run_phase", Phase: "src_prepare", EAPI: "8", Ebuild: ebuild, WorkDir: workDir, UserPatchDirs: []string{generic, specific}}
	if _, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(message)
	if err != nil || string(content) != "specific\n" {
		t.Fatalf("message = %q, %v", content, err)
	}
}

func TestWorkerDoesNotEnableNounset(t *testing.T) {
	ebuild := filepath.Join(t.TempDir(), "pkg.ebuild")
	content := "EAPI=8\nsrc_compile() { printf '<%s>\\n' \"$OPTIONAL_FLAG\"; }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "unset-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil || len(events) != 3 || events[1].Message != "<>" {
		t.Fatalf("events = %#v, error = %v", events, err)
	}
}

func TestDefaultConfigureCompileTestInstallPipeline(t *testing.T) {
	directory := t.TempDir()
	sourceDir := filepath.Join(directory, "source")
	imageDir := filepath.Join(directory, "image")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configure := "#!/bin/sh\nprintf 'configured\\n' > configured\n"
	if err := os.WriteFile(filepath.Join(sourceDir, "configure"), []byte(configure), 0o755); err != nil {
		t.Fatal(err)
	}
	makefile := "all:\n\tprintf 'built\\n' > built\ncheck:\n\ttest -f built\ninstall:\n\tmkdir -p $(DESTDIR)/usr/bin\n\tcp built $(DESTDIR)/usr/bin/built\n"
	if err := os.WriteFile(filepath.Join(sourceDir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "README"), []byte("docs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nPF=pkg-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"src_configure", "src_compile", "src_test", "src_install"} {
		request := Request{Protocol: Version, ID: "default-" + phase, Command: "run_phase", Phase: phase, EAPI: "8", Ebuild: ebuild, WorkDir: directory, SourceDir: sourceDir, ImageDir: imageDir}
		events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
		if err != nil {
			t.Fatalf("%s events = %#v: %v", phase, events, err)
		}
	}
	for _, path := range []string{filepath.Join(sourceDir, "configured"), filepath.Join(sourceDir, "built"), filepath.Join(imageDir, "usr/bin/built"), filepath.Join(imageDir, "usr/share/doc/pkg-1/README")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected output %s: %v", path, err)
		}
	}
}

func TestBashWorkerFailsForMissingAndCircularEclass(t *testing.T) {
	for _, fixture := range []struct {
		name, ebuild string
		files        map[string]string
	}{
		{name: "missing", ebuild: "EAPI=8\ninherit absent\n"},
		{name: "circular", ebuild: "EAPI=8\ninherit a\n", files: map[string]string{"a.eclass": "inherit b\n", "b.eclass": "inherit a\n"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			directory := t.TempDir()
			eclassDir := filepath.Join(directory, "eclass")
			if err := os.MkdirAll(eclassDir, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, content := range fixture.files {
				if err := os.WriteFile(filepath.Join(eclassDir, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			ebuild := filepath.Join(directory, "pkg.ebuild")
			if err := os.WriteFile(ebuild, []byte(fixture.ebuild), 0o644); err != nil {
				t.Fatal(err)
			}
			request := Request{Protocol: Version, ID: fixture.name, Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild, EclassDirs: []string{eclassDir}}
			events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
			if err == nil || len(events) < 2 || events[len(events)-1].ExitCode == nil || *events[len(events)-1].ExitCode == 0 {
				t.Fatalf("events = %#v, error = %v", events, err)
			}
		})
	}
}

func TestPortageSandboxWorkerIsDefault(t *testing.T) {
	ebuild := filepath.Join(t.TempDir(), "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_compile() { :; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "isolated-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild}
	events, err := RunBashWorker(context.Background(), request)
	if err != nil {
		if strings.Contains(err.Error(), "Portage sandbox is required") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	if len(events) < 2 || events[len(events)-1].Kind != "result" {
		t.Fatalf("sandbox worker returned incomplete events: %#v", events)
	}
}

func TestBubblewrapEnhancedModeNeverFallsBack(t *testing.T) {
	ebuild := filepath.Join(t.TempDir(), "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_compile() { :; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "enhanced-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild}
	events, err := RunBashWorkerWithOptions(context.Background(), request, WorkerOptions{Isolation: IsolationBubblewrap})
	if err == nil && (len(events) < 2 || events[len(events)-1].Kind != "result") {
		t.Fatalf("enhanced worker returned incomplete events: %#v", events)
	}
	if err != nil && !strings.Contains(err.Error(), "phase isolation") {
		t.Fatalf("enhanced isolation failure was not explicit: %v", err)
	}
}

func TestNamespaceSelectionDegradesIndependently(t *testing.T) {
	specs := namespaceSpecs(NamespaceOptions{Network: true, IPC: true, Mount: true, PID: true})
	enabled, warnings := selectNamespaces(specs, func(arguments []string) error {
		argument := strings.Join(arguments, " ")
		if argument == "--net" || argument == "--pid --fork" {
			return errors.New("operation not permitted")
		}
		return nil
	})
	if got, want := strings.Join(enabled, " "), "--ipc --mount"; got != want {
		t.Fatalf("enabled namespaces = %q, want %q", got, want)
	}
	if len(warnings) != 2 || !strings.Contains(warnings[0], "network") || !strings.Contains(warnings[1], "PID") {
		t.Fatalf("namespace warnings = %#v", warnings)
	}
}

func TestNamespaceSelectionNeverRequestsUserNamespace(t *testing.T) {
	for _, spec := range namespaceSpecs(NamespaceOptions{Network: true, IPC: true, Mount: true, PID: true}) {
		if strings.Contains(strings.Join(spec.arguments, " "), "user") {
			t.Fatalf("Portage-compatible backend requested a user namespace: %#v", spec)
		}
	}
}

func TestPortageWorkerGracefullyDegradesUnavailableNamespaces(t *testing.T) {
	ebuild := filepath.Join(t.TempDir(), "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_compile() { printf 'ran\\n'; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "namespace-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild}
	var diagnostics strings.Builder
	events, err := RunBashWorkerWithOptions(context.Background(), request, WorkerOptions{
		Isolation:   IsolationPortage,
		Namespaces:  NamespaceOptions{Network: true, IPC: true, Mount: true, PID: true},
		Diagnostics: &diagnostics,
	})
	if err != nil {
		if strings.Contains(err.Error(), "Portage sandbox is required") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	if len(events) != 3 || events[1].Message != "ran" || events[2].ExitCode == nil || *events[2].ExitCode != 0 {
		t.Fatalf("namespace worker events = %#v; diagnostics = %q", events, diagnostics.String())
	}
}

func TestFilesystemSandboxHidesUnboundHostFiles(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	secret := filepath.Join(directory, "host-secret")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	content := "EAPI=8\nsrc_compile() { if test -e \"$HOST_SECRET\"; then echo visible; else echo hidden; fi; }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "fs-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild, Env: map[string]string{"HOST_SECRET": secret}}
	command, isolatedRequest, err := isolatedBashCommand(context.Background(), request, false)
	if err != nil {
		t.Skipf("filesystem isolation unavailable: %v", err)
	}
	events, err := runWorkerCommand(command, isolatedRequest)
	if err != nil {
		t.Fatalf("filesystem-isolated worker: %v", err)
	}
	if len(events) != 3 || events[1].Message != "hidden" {
		t.Fatalf("host file visibility events = %#v", events)
	}
}

func TestBashWorkerRejectsReservedEnvironment(t *testing.T) {
	for _, name := range []string{"BASH_ENV", "ROOT", "SYSROOT", "BROOT", "HOME", "TMPDIR", "PORTAGE_LOG_FILE", "CATEGORY", "PF", "SLOT", "PORTAGE_REPO_NAME"} {
		request := Request{Protocol: Version, ID: "pkg-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: "/tmp/pkg.ebuild", Env: map[string]string{name: "/tmp/inject"}}
		if _, err := RunBashWorker(context.Background(), request); err == nil {
			t.Fatalf("reserved environment %s accepted", name)
		}
	}
}

func TestRequestRequiresAbsolutePortageLogFile(t *testing.T) {
	request := Request{Protocol: Version, ID: "log-path", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: "/tmp/pkg.ebuild", LogFile: "relative.log"}
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "PORTAGE_LOG_FILE") {
		t.Fatalf("Validate error = %v", err)
	}
}

func TestWorkerEnvironmentIsMinimalAndExplicit(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := `EAPI=8
src_compile() {
  printf 'leak=%s custom=%s path=%s locale=%s id=%s phase=%s\n' \
    "${HOST_ENV_LEAK-unset}" "${EXPLICIT_VALUE-unset}" "$PATH" "$LC_ALL" "$ARISE_ID" "$ARISE_PHASE"
}
`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Protocol: Version,
		ID:       "environment-1",
		Command:  "run_phase",
		Phase:    "src_compile",
		EAPI:     "8",
		Ebuild:   ebuild,
		Env:      map[string]string{"EXPLICIT_VALUE": "present"},
	}
	command := exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker)
	command.Env = append(os.Environ(), "HOST_ENV_LEAK=poison")
	events, err := runWorkerCommand(command, request)
	if err != nil {
		t.Fatalf("minimal environment worker: %v; events=%#v", err, events)
	}
	if len(events) != 3 {
		t.Fatalf("events = %#v, want phase, log, result", events)
	}
	want := "leak=unset custom=present path=/usr/bin:/bin locale=C id=environment-1 phase=src_compile"
	if events[1].Kind != "log" || events[1].Message != want {
		t.Fatalf("environment log = %#v, want %q", events[1], want)
	}
}

func TestWorkerExposesProtocolOwnedPackageIdentity(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "python-3.13.7-r2.ebuild")
	content := `EAPI=8
src_compile() {
  printf '%s|%s|%s|%s|%s|%s|%s|%s|%s\n' "$CATEGORY" "$PN" "$PV" "$PR" "$P" "$PVR" "$PF" "$SLOT" "$PORTAGE_REPO_NAME"
}
`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Protocol: Version, ID: "identity-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild,
		Package: PackageIdentity{Category: "dev-lang", PN: "python", PV: "3.13.7", PR: "r2", P: "python-3.13.7", PVR: "3.13.7-r2", PF: "python-3.13.7-r2", Slot: "3.13/3.13", Repository: "gentoo"},
	}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("identity worker: %v; events=%#v", err, events)
	}
	want := "dev-lang|python|3.13.7|r2|python-3.13.7|3.13.7-r2|python-3.13.7-r2|3.13/3.13|gentoo"
	if len(events) != 3 || events[1].Message != want {
		t.Fatalf("identity events = %#v, want log %q", events, want)
	}
}

func TestWorkerAcceptsOnlyVerifiedDistfileSet(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_unpack() { printf '%s\\n' \"$DISTDIR\"; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content := []byte("source")
	digest := sha512.Sum512(content)
	artifact := distfiles.Artifact{Name: "source.tar", Size: int64(len(content)), Digests: map[string]string{"SHA512": hex.EncodeToString(digest[:])}}
	path := filepath.Join(directory, artifact.Name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "verified-1", Command: "run_phase", Phase: "src_unpack", EAPI: "8", Ebuild: ebuild, Distfiles: &distfiles.VerifiedSet{Directory: directory, Artifacts: []distfiles.Artifact{artifact}}}
	events, err := RunBashWorker(context.Background(), request)
	if err != nil {
		if strings.Contains(err.Error(), "Portage sandbox is required") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	if len(events) != 3 || events[1].Message != directory {
		t.Fatalf("events = %#v", events)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RunBashWorker(context.Background(), request); err == nil || !strings.Contains(err.Error(), "refusing unverified distfile") {
		t.Fatalf("corrupt verified set error = %v", err)
	}
}
