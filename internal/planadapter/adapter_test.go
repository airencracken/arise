package planadapter

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/planvalidate"
	"github.com/airencracken/arise/internal/resolve"
)

func TestFreezeProducesIndependentlyValidUpgrade(t *testing.T) {
	graph, result := upgradeGraph(t)
	fixture, plan, err := Freeze(graph, result, Options{
		Operation: "update", Targets: []string{">=dev-libs/library-2"},
		PackagePolicy: func(cpv, slot, repository string) planvalidate.PackagePolicy {
			return planvalidate.PackagePolicy{
				BaseKeyword: "amd64", LicenseChanges: []string{"MIT"},
				MaskAtom: "=" + cpv, MaskSource: "package.unmask",
			}
		},
		DomainsAliasToRoot: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	validation := planvalidate.ValidateFinalState(fixture, plan)
	if !validation.Valid {
		t.Fatalf("adapted upgrade is invalid: %#v", validation)
	}
	if len(plan.Actions) != 1 || plan.Actions[0].Replaces != "dev-libs/library-1" {
		t.Fatalf("adapted actions = %#v", plan.Actions)
	}
	if plan.Actions[0].ID == "" || plan.Actions[0].Package.Policy.Masked ||
		plan.Actions[0].Package.Policy.MaskSource != "package.unmask" {
		t.Fatalf("action identity or policy provenance missing: %#v", plan.Actions[0])
	}
}

func TestFreezeMutationMissingActionIsRejected(t *testing.T) {
	graph, result := upgradeGraph(t)
	fixture, plan, err := Freeze(graph, result, Options{
		Operation: "update", Targets: []string{">=dev-libs/library-2"},
		DomainsAliasToRoot: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan.Actions = nil
	validation := planvalidate.ValidateFinalState(fixture, plan)
	if validation.Valid || !containsViolation(validation, "unsatisfied-request-target") {
		t.Fatalf("missing action mutation accepted: %#v", validation)
	}
}

func TestFreezeRejectsSelectedIncompleteMetadata(t *testing.T) {
	graph, result := upgradeGraph(t)
	for _, version := range graph.Packages["dev-libs/library"].Versions {
		if version.Version.Raw == "2" {
			version.DependencyMetadataKnown = false
		}
	}
	fixture, plan, err := Freeze(graph, result, Options{
		Operation: "update", Targets: []string{">=dev-libs/library-2"},
		DomainsAliasToRoot: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	validation := planvalidate.ValidateFinalState(fixture, plan)
	if validation.Valid || !containsViolation(validation, "non-authoritative-package-source") {
		t.Fatalf("incomplete selected metadata accepted: %#v", validation)
	}
}

func TestFreezeIsDeterministicAndDoesNotMutateResolverData(t *testing.T) {
	graph, result := upgradeGraph(t)
	before, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	firstFixture, firstPlan, err := Freeze(graph, result, Options{Operation: "update", Targets: []string{">=dev-libs/library-2"}, DomainsAliasToRoot: true})
	if err != nil {
		t.Fatal(err)
	}
	secondFixture, secondPlan, err := Freeze(graph, result, Options{Operation: "update", Targets: []string{">=dev-libs/library-2"}, DomainsAliasToRoot: true})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstFixture, secondFixture) || !reflect.DeepEqual(firstPlan, secondPlan) {
		t.Fatal("adapter output is nondeterministic")
	}
	after, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("adapter mutated resolver result")
	}
}

func upgradeGraph(t *testing.T) (*resolve.DepGraph, *resolve.ResolveResult) {
	t.Helper()
	graph := resolve.NewDepGraph()
	old := graph.AddVersionFromRepository("dev-libs/library", "1", "0", "0", true, nil, "amd64", "gentoo")
	old.InstalledEAPI = "8"
	old.InstalledUseFlags = map[string]bool{}
	old.DependencyMetadataKnown = true
	next := graph.AddVersionFromRepository("dev-libs/library", "2", "0", "0", false, map[string]bool{"ssl": true}, "amd64", "gentoo")
	next.Available = true
	next.DependencyMetadataKnown = true
	next.EAPI = "8"
	next.IUse = "ssl"
	next.License = "MIT"
	selected, err := atom.Parse("dev-libs/library-2")
	if err != nil {
		t.Fatal(err)
	}
	result := &resolve.ResolveResult{
		Verified: true, Verification: resolve.VerificationVerified,
		Install: []resolve.PkgAction{{
			Atom: selected, Action: "update", Slot: "0", Subslot: "0",
			Repository: "gentoo", UseFlags: map[string]bool{"ssl": true},
			InstalledVersion: "1",
		}},
	}
	return graph, result
}

func containsViolation(result planvalidate.ValidationResult, kind string) bool {
	for _, violation := range result.Violations {
		if violation.Kind == kind {
			return true
		}
	}
	return false
}
