package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
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
