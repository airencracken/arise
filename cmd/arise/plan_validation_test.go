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

func TestIndependentPlanAuditSkipsPartialPlanModes(t *testing.T) {
	graph := resolve.NewDepGraph()
	result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified}
	for name, test := range map[string]struct {
		targets []string
		cfg     resolve.ResolveConfig
	}{
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
