package main

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/planvalidate"
	"github.com/airencracken/arise/internal/resolve"
)

func TestIndependentPlanAuditRouteAndLockedReplay(t *testing.T) {
	graph := resolve.NewDepGraph()
	old := graph.AddVersionFromRepository("dev-libs/library", "1", "0", "0", true, nil, "amd64", "gentoo")
	old.InstalledEAPI = "8"
	old.DependencyMetadataKnown = true
	next := graph.AddVersionFromRepository("dev-libs/library", "2", "0", "0", false, nil, "amd64", "gentoo")
	next.Available = true
	next.DependencyMetadataKnown = true
	next.EAPI = "8"
	selected, err := atom.Parse("dev-libs/library-2")
	if err != nil {
		t.Fatal(err)
	}
	result := &resolve.ResolveResult{
		Verified: true, Verification: resolve.VerificationVerified,
		Install: []resolve.PkgAction{{
			Atom: selected, Action: "update", Slot: "0", Subslot: "0",
			Repository: "gentoo", InstalledVersion: "1",
		}},
	}
	audit, err := prepareIndependentPlanAudit(graph, result, []string{">=dev-libs/library-2"}, resolve.ResolveConfig{Update: true})
	if err != nil {
		t.Fatal(err)
	}
	if audit == nil {
		t.Fatal("direct verified plan did not reach independent audit")
	}
	first, second := audit.validate(), audit.validate()
	if !first.Valid || !second.Valid || len(first.Violations) != 0 {
		t.Fatalf("valid audit replay = %#v, %#v", first, second)
	}
}

func TestIndependentPlanAuditCanonicalizesBarePackageTarget(t *testing.T) {
	graph := resolve.NewDepGraph()
	version := graph.AddVersionFromRepository("sys-apps/arise", "0.0.8", "0", "0", false, nil, "~amd64", "arise-overlay")
	version.Available = true
	version.DependencyMetadataKnown = true
	version.EAPI = "8"
	selected, err := atom.Parse("sys-apps/arise-0.0.8")
	if err != nil {
		t.Fatal(err)
	}
	result := &resolve.ResolveResult{
		Verified: true, Verification: resolve.VerificationVerified,
		Install: []resolve.PkgAction{{
			Atom: selected, Action: "install", Slot: "0", Subslot: "0",
			Repository: "arise-overlay",
		}},
	}
	audit, err := prepareIndependentPlanAudit(graph, result, []string{"arise"}, resolve.ResolveConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := audit.fixture.Request.Targets, []string{"sys-apps/arise"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("canonical targets = %v, want %v", got, want)
	}
	if result := audit.validate(); !result.Valid {
		t.Fatalf("bare target audit failed: %#v", result)
	}
}

func TestIndependentPlanAuditBareTargetFailsClosedOnAmbiguity(t *testing.T) {
	graph := resolve.NewDepGraph()
	graph.AddPackage("app-misc/tool")
	graph.AddPackage("dev-util/tool")
	_, err := canonicalizeIndependentAuditTargets(graph, []string{"tool"})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous bare target error = %v", err)
	}
	_, err = canonicalizeIndependentAuditTargets(graph, []string{"missing"})
	if err == nil || !strings.Contains(err.Error(), "absent") {
		t.Fatalf("missing bare target error = %v", err)
	}
}

func TestIndependentPlanAuditFreezesPartialPlanModes(t *testing.T) {
	graph := resolve.NewDepGraph()
	for name, test := range map[string]struct {
		targets      []string
		cfg          resolve.ResolveConfig
		verification string
		verified     bool
	}{
		"nodeps": {
			targets: []string{"app-misc/example"}, cfg: resolve.ResolveConfig{NoDeps: true},
			verification: resolve.VerificationSkippedNoDeps,
		},
		"onlydeps": {
			targets:      []string{"app-misc/example"},
			cfg:          resolve.ResolveConfig{OnlyDeps: true},
			verification: resolve.VerificationVerified, verified: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			result := &resolve.ResolveResult{Verified: test.verified, Verification: test.verification}
			audit, err := prepareIndependentPlanAudit(graph, result, test.targets, test.cfg)
			if err != nil || audit == nil {
				t.Fatalf("partial route audit = %#v, %v", audit, err)
			}
			if audit.fixture.Request.PartialMode != name {
				t.Fatalf("partial mode = %q, want %q", audit.fixture.Request.PartialMode, name)
			}
		})
	}
}

func TestIndependentPlanAuditFreezesSetExpansion(t *testing.T) {
	cfg := resolve.ResolveConfig{
		SystemSet: &resolve.WorldSet{Entries: []string{"sys-apps/coreutils", "virtual/libc"}},
		WorldSet:  &resolve.WorldSet{Entries: []string{"app-editors/vim", "virtual/libc"}},
		PackageSetExpander: func(name string) ([]string, error) {
			if name != "@custom" {
				t.Fatalf("unexpected set %q", name)
			}
			return []string{"app-misc/example"}, nil
		},
	}
	got, err := expandIndependentAuditTargets([]string{"@world", "@custom", "dev-libs/direct"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"sys-apps/coreutils", "virtual/libc", "app-editors/vim", "app-misc/example", "dev-libs/direct"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expanded targets = %v, want %v", got, want)
	}
}

func TestIndependentPlanAuditRejectsUnfrozenSet(t *testing.T) {
	_, err := expandIndependentAuditTargets([]string{"@missing"}, resolve.ResolveConfig{})
	if err == nil || !strings.Contains(err.Error(), "no expander") {
		t.Fatalf("missing set expander error = %v", err)
	}
	sentinel := errors.New("invalid set")
	_, err = expandIndependentAuditTargets([]string{"@broken"}, resolve.ResolveConfig{
		PackageSetExpander: func(string) ([]string, error) { return nil, sentinel },
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("set expansion error = %v", err)
	}
}

func TestIndependentPlanAuditHumanOutputIsBounded(t *testing.T) {
	violations := make([]planvalidate.Violation, 20)
	for index := range violations {
		violations[index] = planvalidate.Violation{Kind: "failure", Package: "cat/pkg", Requirement: "cat/dep", Message: "invalid"}
	}
	var output bytes.Buffer
	reportIndependentPlanAudit(&output, "post-resolution", planvalidate.ValidationResult{
		Violations: violations, OmittedViolations: 7,
	})
	got := output.String()
	if !strings.Contains(got, "validation failed at post-resolution") ||
		!strings.Contains(got, "refusing package-state mutation") ||
		!strings.Contains(got, "additional violations omitted") {
		t.Fatalf("audit output = %q", got)
	}
	if count := strings.Count(got, "failure:"); count != 5 {
		t.Fatalf("rendered violation count = %d, want 5: %q", count, got)
	}
}

func TestValidIndependentPlanAuditIsSilent(t *testing.T) {
	var output bytes.Buffer
	reportIndependentPlanAudit(&output, "locked-pre-mutation", planvalidate.ValidationResult{Valid: true})
	if output.Len() != 0 {
		t.Fatalf("valid audit emitted output: %q", output.String())
	}
}

func TestIndependentPlanAuditEnforcementFailsClosed(t *testing.T) {
	audit := &independentPlanAudit{
		fixture: planvalidate.Fixture{
			Schema:  planvalidate.SchemaVersion,
			Request: planvalidate.Request{Operation: "install", Targets: []string{"app-misc/missing"}},
		},
		plan: planvalidate.Plan{Schema: planvalidate.SchemaVersion},
	}
	var output bytes.Buffer
	err := enforceIndependentPlanAudit(&output, "locked-pre-mutation", audit)
	if err == nil || !strings.Contains(err.Error(), "locked-pre-mutation") {
		t.Fatalf("invalid plan enforcement error = %v", err)
	}
	if !strings.Contains(output.String(), "refusing package-state mutation") {
		t.Fatalf("invalid plan enforcement output = %q", output.String())
	}

	output.Reset()
	if err := enforceIndependentPlanAudit(&output, "post-resolution", nil); err != nil || output.Len() != 0 {
		t.Fatalf("unsupported route enforcement = %v, %q", err, output.String())
	}
}

func TestDecisionLedgerHumanRenderingIsBounded(t *testing.T) {
	ledger := resolve.DecisionLedger{
		Records: []resolve.CandidateDecision{
			{Outcome: resolve.DecisionSelected, CPV: "app-misc/target-1", Reasons: []string{"explicit target"}},
			{Outcome: resolve.DecisionRejected, CPV: "app-misc/target-2", Reasons: []string{"masked"}},
			{Outcome: resolve.DecisionSkipped, CPV: "app-misc/target-3", Reasons: []string{"lower committed preference"}},
		},
		Truncated: true, OmittedRecords: 12,
	}
	lines := renderDecisionLedger(ledger, 1)
	if len(lines) != 2 || !strings.Contains(lines[0], "1 selected") ||
		!strings.Contains(lines[0], "12 omitted") ||
		!strings.Contains(lines[1], "rejected app-misc/target-2") {
		t.Fatalf("decision rendering = %#v", lines)
	}
}
