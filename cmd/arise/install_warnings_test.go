package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/portage"
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
		"inspect dependent versions: arise search --exact --versions dev-python/sphinx",
		"if no longer needed: arise --pretend uninstall =dev-python/sphinx-9.1.0",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("warning output omits %q:\n%s", want, got)
		}
	}
	for _, misleading := range []string{"To clear this block:", "newer candidate unavailable:", "inspect compatible versions:"} {
		if strings.Contains(got, misleading) {
			t.Errorf("warning suggests an unverified remedy %q:\n%s", misleading, got)
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
	if got := strings.Count(output.String(), "inspect dependent versions:"); got != 1 {
		t.Fatalf("blocker advice count = %d, want 1:\n%s", got, output.String())
	}
}

func TestSkippedLibgpiodWarningDoesNotSuggestKeywordingSvxlink(t *testing.T) {
	graph := resolve.NewDepGraph()
	for _, version := range []string{"1.6.4", "1.6.5", "2.2.4"} {
		vi := graph.AddVersionFromRepository("dev-libs/libgpiod", version, "0", "2", version == "1.6.4", nil, "amd64", "gentoo")
		vi.Available, vi.DependencyMetadataKnown, vi.EAPI = true, true, "8"
	}
	for _, version := range []string{"25.05.1", "26.05.2"} {
		keywords := "amd64"
		if version == "26.05.2" {
			keywords = "~amd64"
		}
		vi := graph.AddVersionFromRepository("media-radio/svxlink", version, "0", "0", version == "25.05.1", nil, keywords, "gentoo")
		vi.Available, vi.DependencyMetadataKnown, vi.EAPI, vi.InstalledEAPI = true, true, "8", "8"
		vi.Rdepend, vi.InstalledRdepend = "=dev-libs/libgpiod-1*:=", "=dev-libs/libgpiod-1*:0/2="
	}
	cfg := resolve.DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{ACCEPT_KEYWORDS: []string{"amd64"}}
	cfg.Update, cfg.Deep = true, true
	cfg.WorldSet = &resolve.WorldSet{Entries: []string{"media-radio/svxlink", "dev-libs/libgpiod"}}
	result, err := resolve.Resolve(graph, []string{"@world"}, cfg)
	if err != nil || result == nil || !result.Verified {
		t.Fatalf("resolve failed: result=%#v err=%v", result, err)
	}
	if len(result.Install) != 1 || result.Install[0].Atom.CPV() != "dev-libs/libgpiod-1.6.5" {
		t.Fatalf("unexpected update actions: %#v", result.Install)
	}
	var output bytes.Buffer
	printResolutionWarnings(&output, result, false)
	got := output.String()
	if !strings.Contains(got, "=dev-libs/libgpiod-1*:=") || !strings.Contains(got, "required by media-radio/svxlink") {
		t.Fatalf("missing dependency explanation:\n%s", got)
	}
	if strings.Contains(got, "26.05.2") || strings.Contains(got, "To clear this block") || strings.Contains(got, "compatible versions") {
		t.Fatalf("suggested newer svxlink despite unchanged dependency:\n%s", got)
	}
}
