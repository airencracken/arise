package phaseproto

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/distfiles"
)

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
	for _, name := range []string{"BASH_ENV", "ROOT", "SYSROOT", "BROOT", "HOME", "TMPDIR"} {
		request := Request{Protocol: Version, ID: "pkg-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: "/tmp/pkg.ebuild", Env: map[string]string{name: "/tmp/inject"}}
		if _, err := RunBashWorker(context.Background(), request); err == nil {
			t.Fatalf("reserved environment %s accepted", name)
		}
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
