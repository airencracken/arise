package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/resolve"
)

func TestWarningsForDisplayNeverExposesInternalCircularDependencyPaths(t *testing.T) {
	warnings := []string{
		"circular dependency: cat/a-1 -> cat/b-1 -> cat/a-1",
		"selected cat/old-1 uses deprecated EAPI 7",
		"selected cat/old-1 uses deprecated EAPI 7",
	}

	want := []string{"selected cat/old-1 uses deprecated EAPI 7"}
	if got := warningsForDisplay(warnings, false); !reflect.DeepEqual(got, want) {
		t.Fatalf("non-verbose warnings = %#v, want %#v", got, want)
	}
	verboseWant := want
	if got := warningsForDisplay(warnings, true); !reflect.DeepEqual(got, verboseWant) {
		t.Fatalf("verbose warnings = %#v, want %#v", got, verboseWant)
	}
}

func TestPrintResolutionWarningsIncludesConstraintAndRecoveryAdvice(t *testing.T) {
	previous := color.UseColor
	color.UseColor = false
	t.Cleanup(func() { color.UseColor = previous })

	warning := "skipped update dev-python/docutils-0.23"
	result := &resolve.ResolveResult{
		Warnings: []string{warning},
		DecisionLedger: resolve.DecisionLedger{Records: []resolve.CandidateDecision{{
			Outcome: resolve.DecisionRejected, State: "available",
			CPV: "dev-python/sphinx-9.1.0-r1", Reasons: []string{"keywords not accepted"},
		}}},
		WarningDiagnostics: []resolve.WarningDiagnostic{{
			Summary: warning, Message: "dev-python/docutils-0.23 was skipped because an installed dependency requires:",
			Source: "<dev-python/docutils-0.23[python_targets_python3_14(-)]",
			Start:  0, End: 57, Annotation: "required by dev-python/sphinx",
			Blocker: "dev-python/sphinx", BlockerCPV: "dev-python/sphinx-9.1.0",
		}},
	}
	var output bytes.Buffer
	printResolutionWarnings(&output, result, false)
	got := output.String()
	for _, want := range []string{
		"\nWarnings:\n",
		"dev-python/docutils-0.23 was skipped",
		"<dev-python/docutils-0.23[python_targets_python3_14(-)]",
		"inspect compatible versions: arise search --exact --versions dev-python/sphinx",
		"newer candidate unavailable: dev-python/sphinx-9.1.0-r1 (keywords not accepted)",
		"if no longer needed: arise --pretend uninstall =dev-python/sphinx-9.1.0",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("warning output omits %q:\n%s", want, got)
		}
	}
}

func TestRenderDecisionLedgerPrioritizesWarningBlockers(t *testing.T) {
	ledger := resolve.DecisionLedger{Records: []resolve.CandidateDecision{
		{Outcome: resolve.DecisionRejected, State: "available", CPV: "app-admin/first-2", Reasons: []string{"keywords not accepted"}},
		{Outcome: resolve.DecisionRejected, State: "available", CPV: "dev-python/sphinx-9.1.0-r1", Reasons: []string{"keywords not accepted"}},
	}}
	lines := renderDecisionLedger(ledger, 1, "dev-python/sphinx")
	if len(lines) != 2 || !strings.Contains(lines[1], "dev-python/sphinx-9.1.0-r1") {
		t.Fatalf("focused decision ledger = %#v", lines)
	}
}

func TestPrintResolutionWarningsDeduplicatesBlockerAdvice(t *testing.T) {
	warning := "skipped update cat/pkg-2"
	result := &resolve.ResolveResult{
		Warnings: []string{warning},
		WarningDiagnostics: []resolve.WarningDiagnostic{
			{Summary: warning, Source: "<cat/pkg-2[a]", End: 13, Blocker: "cat/blocker"},
			{Summary: warning, Source: "<cat/pkg-2[b]", End: 13, Blocker: "cat/blocker"},
		},
	}
	var output bytes.Buffer
	printResolutionWarnings(&output, result, false)
	if got := strings.Count(output.String(), "To clear this block:"); got != 1 {
		t.Fatalf("blocker advice count = %d, want 1:\n%s", got, output.String())
	}
}
