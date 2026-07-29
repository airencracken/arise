package main

import (
	"bytes"
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

func TestIndependentPlanAuditSkipsUnsupportedEntryRoutes(t *testing.T) {
	graph := resolve.NewDepGraph()
	result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified}
	for name, test := range map[string]struct {
		targets []string
		cfg     resolve.ResolveConfig
	}{
		"set":    {targets: []string{"@world"}},
		"nodeps": {targets: []string{"app-misc/example"}, cfg: resolve.ResolveConfig{NoDeps: true}},
		"onlydeps": {
			targets: []string{"app-misc/example"},
			cfg:     resolve.ResolveConfig{OnlyDeps: true},
		},
	} {
		t.Run(name, func(t *testing.T) {
			audit, err := prepareIndependentPlanAudit(graph, result, test.targets, test.cfg)
			if err != nil || audit != nil {
				t.Fatalf("unsupported route audit = %#v, %v", audit, err)
			}
		})
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
	if !strings.Contains(got, "audit failed at post-resolution") ||
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
