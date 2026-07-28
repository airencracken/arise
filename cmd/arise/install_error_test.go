package main

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/resolve"
)

func TestPrintExecutionErrorSeparatesWrappedContext(t *testing.T) {
	leaf := errors.New("exit status 1 (durable log: /var/log/portage/cat:pkg.log)")
	err := fmt.Errorf("executor: cat/pkg-1: %w", fmt.Errorf("rebuild: phase worker: %w", leaf))
	var output bytes.Buffer

	printExecutionError(&output, err)

	want := "arise: package transaction failed\n\n" +
		"  executor: cat/pkg-1\n" +
		"  rebuild: phase worker\n" +
		"  exit status 1\n" +
		"\n  Log file:\n" +
		"    '/var/log/portage/cat:pkg.log'\n\n"
	if output.String() != want {
		t.Fatalf("unexpected output:\n%s\nwant:\n%s", output.String(), want)
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

	want := "arise: package transaction failed\n\n  first line\n  second line\n\n"
	if output.String() != want {
		t.Fatalf("unexpected output %q, want %q", output.String(), want)
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
