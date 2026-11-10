package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/oplock"
	"github.com/airencracken/arise/internal/recoveryset"
	"github.com/airencracken/arise/internal/resolve"
)

func TestPrintExecutionErrorsGiveHumanExplanationAndNextStep(t *testing.T) {
	for _, test := range []struct {
		name            string
		err             error
		summary, action string
	}{
		{"backup permission", &recoveryset.CaptureError{VDBEntryPath: "/var/db/pkg/sys-kernel/gentoo-kernel-bin-6.18.62", Directory: "/var/cache/binpkgs/.arise-recovery", Err: &os.PathError{Op: "open", Path: "/usr/src/kernel/vmlinuz", Err: syscall.EACCES}}, "rollback backup for sys-kernel/gentoo-kernel-bin-6.18.62", "access permissions"},
		{"busy", fmt.Errorf("acquire mutation lock: %w", oplock.ErrBusy), "Another package operation", "finish"},
		{"storage", &os.PathError{Op: "write", Path: "/var/tmp/arise/build/output", Err: syscall.ENOSPC}, "storage is full", "Free space"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			printExecutionError(&output, fmt.Errorf("executor: app-arch/gzip-1.15: %w", test.err))
			for _, want := range []string{test.summary, test.action, "Next step:"} {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("missing %q:\n%s", want, output.String())
				}
			}
			if strings.Contains(output.String(), "executor:") || strings.Contains(output.String(), "prepare mutation:") {
				t.Fatalf("internal context replaced the human explanation:\n%s", output.String())
			}
		})
	}
}

func TestPrintExecutionErrorSeparatesWrappedContext(t *testing.T) {
	leaf := errors.New("exit status 1 (durable log: /var/log/portage/cat:pkg.log)")
	err := fmt.Errorf("executor: cat/pkg-1: %w", fmt.Errorf("rebuild: phase worker: %w", leaf))
	var output bytes.Buffer

	printExecutionError(&output, err)

	if strings.Contains(output.String(), "executor:") || strings.Contains(output.String(), "rebuild: phase worker") || !strings.Contains(output.String(), "Next step:") || !strings.Contains(output.String(), "'/var/log/portage/cat:pkg.log'") {
		t.Fatalf("default error is not human-readable:\n%s", output.String())
	}
	output.Reset()
	printExecutionErrorWithOptions(&output, err, true)
	for _, detail := range []string{"Technical details:", "executor: cat/pkg-1", "rebuild: phase worker", "exit status 1"} {
		if !strings.Contains(output.String(), detail) {
			t.Fatalf("verbose error lost %q:\n%s", detail, output.String())
		}
	}
}

func TestPrintExecutionInterruptedIsActionable(t *testing.T) {
	var output bytes.Buffer
	printExecutionInterrupted(&output)
	want := "arise: interrupted by user\n\n" +
		"  Completed package progress has been preserved.\n" +
		"  Rerun preflight to calculate a continuation plan.\n\n"
	if output.String() != want {
		t.Fatalf("unexpected output %q, want %q", output.String(), want)
	}
}

func TestPrintExecutionErrorShowsBoundedMultilineTail(t *testing.T) {
	lines := make([]string, 20)
	for index := range lines {
		lines[index] = fmt.Sprintf("detail %d", index+1)
	}
	var output bytes.Buffer
	printExecutionError(&output, errors.New(strings.Join(lines, "\n")))

	if strings.Contains(output.String(), "  detail 2\n") || !strings.Contains(output.String(), "  detail 20\n") {
		t.Fatalf("unbounded diagnostic output:\n%s", output.String())
	}
}

func TestPrintExecutionErrorPrefersCompilerErrorOverCleanupTail(t *testing.T) {
	lines := []string{
		"checking headers", "source.c:9: error: missing type", "compilation terminated",
		"make[3]: Leaving directory '/build/a'", "make[2]: Leaving directory '/build'", "make[1]: Leaving directory '/build'",
	}
	var output bytes.Buffer
	printExecutionError(&output, errors.New(strings.Join(lines, "\n")))
	if !strings.Contains(output.String(), "missing type") || strings.Contains(output.String(), "make[1]: Leaving") {
		t.Fatalf("causal diagnostic selection:\n%s", output.String())
	}
}

func TestPrintExecutionErrorIndentsMultilineLeaf(t *testing.T) {
	var output bytes.Buffer
	printExecutionError(&output, errors.New("first line\nsecond line"))

	if !strings.Contains(output.String(), "    first line\n    second line\n") || !strings.Contains(output.String(), "Next step:") {
		t.Fatalf("multiline details were lost or not indented: %q", output.String())
	}
}

func TestPrintExecutionErrorPreservesLongRecoveryFailure(t *testing.T) {
	message := "rebuild: enter mutation boundary: prepare mutation: recovery set: capture /var/db/pkg/sys-apps/util-linux-2.42.2: binpkg: capture installed path /etc/pam.d/runuser-l: lstat /etc/pam.d/runuser-l: no such file or directory"
	var output bytes.Buffer
	printExecutionError(&output, fmt.Errorf("executor: app-arch/gzip-1.15: %w", errors.New(message)))
	if !strings.Contains(output.String(), message) {
		t.Fatalf("recovery failure lost its path or cause:\n%s", output.String())
	}
}

func TestPrintExecutionErrorPreservesLongUnicodeDiagnostic(t *testing.T) {
	message := strings.Repeat("診断", 40) + ": permission denied"
	var output bytes.Buffer
	printExecutionError(&output, errors.New(message))
	if !strings.Contains(output.String(), message) {
		t.Fatalf("long diagnostic was truncated:\n%s", output.String())
	}
}

func TestPrintExecutionErrorExplainsTemporaryStorageExhaustion(t *testing.T) {
	err := fmt.Errorf(
		"executor: sys-apps/arise-0.0.5: %w",
		errors.New("compile: mkdir /var/tmp/arise/build/temp/go-build/b444: no space left on device"),
	)
	var output bytes.Buffer

	printExecutionError(&output, err)

	got := output.String()
	if !strings.Contains(got, "Temporary build storage is full.") ||
		!strings.Contains(got, "PORTAGE_TMPDIR (normally /var/tmp/arise)") ||
		!strings.Contains(got, "no space left on device") {
		t.Fatalf("storage exhaustion was not actionable:\n%s", got)
	}
}

func TestExecutionStorageHintIgnoresUnrelatedFailures(t *testing.T) {
	if got := executionStorageHint(errors.New("compiler error")); got != "" {
		t.Fatalf("unrelated failure produced storage hint %q", got)
	}
}

func TestRecoveryPackagesForActionsIncludesOnlyReplacedInstalledInstances(t *testing.T) {
	updated, err := atom.Parse("sys-devel/gcc-15")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := atom.Parse("app-misc/new-1")
	if err != nil {
		t.Fatal(err)
	}
	actions := []resolve.PkgAction{
		{Atom: updated, Action: "update", InstalledVersion: "14.2.1"},
		{Atom: fresh, Action: "install"},
		{Atom: updated, Action: "update", InstalledVersion: "14.2.1"},
	}
	packages := recoveryPackagesForActions("/vdb", actions)
	if len(packages) != 1 {
		t.Fatalf("recovery packages = %+v", packages)
	}
	want := filepath.Join("/vdb", "sys-devel", "gcc-14.2.1")
	if packages[0].VDBEntryPath != want {
		t.Fatalf("recovery VDB path = %q, want %q", packages[0].VDBEntryPath, want)
	}
}

func TestRecoveryPackagesForActionsIncludesBlockerRemovals(t *testing.T) {
	removed, err := atom.Parse("perl-core/Test-Harness-3.500.0")
	if err != nil {
		t.Fatal(err)
	}
	actions := []resolve.PkgAction{{Atom: removed, Action: "uninstall"}, {Atom: removed, Action: "uninstall"}}
	packages := recoveryPackagesForActions("/vdb", actions)
	if len(packages) != 1 || packages[0].VDBEntryPath != "/vdb/perl-core/Test-Harness-3.500.0" {
		t.Fatalf("blocker recovery packages = %#v", packages)
	}
}
