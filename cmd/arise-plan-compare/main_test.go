package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/airencracken/arise/internal/plancompare"
)

func TestWithoutNewsPreservesCallerFeatures(t *testing.T) {
	input := []string{"PATH=/bin", "FEATURES=test sandbox", "USE=ssl"}
	want := []string{"PATH=/bin", "FEATURES=test sandbox -news", "USE=ssl"}
	if got := withoutNews(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("withoutNews() = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(input, []string{"PATH=/bin", "FEATURES=test sandbox", "USE=ssl"}) {
		t.Fatalf("withoutNews mutated caller environment: %v", input)
	}
}

func TestComparisonClassification(t *testing.T) {
	for _, test := range []struct {
		arise, portage, same bool
		want                 string
	}{
		{true, true, true, plancompare.ClassEquivalentValid},
		{true, false, false, plancompare.ClassInconclusive},
		{false, true, false, plancompare.ClassInconclusive},
		{true, true, false, plancompare.ClassInconclusive},
	} {
		var differences []plancompare.Difference
		if !test.same {
			differences = []plancompare.Difference{{Identity: "cat/pkg:0", Kind: "version"}}
		}
		got, err := classifyPlans(test.arise, test.portage, differences, "", "", "")
		if err != nil || got.Class != test.want {
			t.Fatalf("classifyPlans(%t,%t,%t)=%#v,%v want=%q", test.arise, test.portage, test.same, got, err, test.want)
		}
	}
}

func TestClassifyPlansRequiresFinalStateEvidenceForDifferentActions(t *testing.T) {
	diagnostics := []plancompare.Difference{{Identity: "cat/pkg:0", Kind: "only-arise"}}
	result, err := classifyPlans(true, true, diagnostics, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Class != plancompare.ClassInconclusive || result.Equivalent {
		t.Fatalf("difference without state evidence = %#v", result)
	}

	dir := t.TempDir()
	arisePath := filepath.Join(dir, "arise.json")
	portagePath := filepath.Join(dir, "portage.json")
	document := `{
		"schema": 1,
		"fixture": {
			"schema": 1,
			"request": {"operation": "install", "targets": []},
			"installed": [{"cpv":"cat/pkg-1","slot":"0","repository":"gentoo","metadata_authority":"vdb"}],
			"available": []
		},
		"plan": {
			"schema": 1,
			"actions": [],
			"decisions": {"records":[],"truncated":false,"omitted_records":0,"encoded_bytes":0}
		}
	}`
	if err := os.WriteFile(arisePath, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(portagePath, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = classifyPlans(true, true, diagnostics, arisePath, portagePath, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Class != plancompare.ClassEquivalentValid || !result.Equivalent ||
		!reflect.DeepEqual(result.ActionDiagnostics, diagnostics) {
		t.Fatalf("validated equal final states = %#v", result)
	}
}

func TestClassifyPlansRejectsOneSidedStateEvidence(t *testing.T) {
	if _, err := classifyPlans(true, true, nil, "arise.json", "", ""); err == nil {
		t.Fatal("one-sided state evidence accepted")
	}
}

func TestClassificationAcceptanceContract(t *testing.T) {
	for class, want := range map[string]bool{
		plancompare.ClassEquivalentValid:          true,
		plancompare.ClassValidDivergence:          true,
		plancompare.ClassAriseValidPortageInvalid: true,
		plancompare.ClassAriseInvalidPortageValid: false,
		plancompare.ClassBothInvalid:              false,
		plancompare.ClassInconclusive:             false,
	} {
		if got := classificationAccepted(class); got != want {
			t.Fatalf("classificationAccepted(%q) = %t, want %t", class, got, want)
		}
	}
}

func TestParseAriseVerifiedRequiresCompleteVerifiedResult(t *testing.T) {
	if !parseAriseVerified(`{"complete":true,"resolution":{"verified":true,"verification":"verified"}}`) {
		t.Fatal("complete verified result rejected")
	}
	if parseAriseVerified(`{"complete":false,"resolution":{"verified":true,"verification":"verified"}}`) {
		t.Fatal("incomplete result accepted")
	}
}

func TestUnresolvedPortageDiagnostics(t *testing.T) {
	for _, diagnostic := range []string{"resulting in a slot conflict", "impossible to satisfy simultaneously", "4 unsatisfied blockers"} {
		if !looksUnresolved(diagnostic) {
			t.Fatalf("unresolved diagnostic not recognized: %q", diagnostic)
		}
	}
	if looksUnresolved("ordinary warning") {
		t.Fatal("ordinary warning classified as unresolved")
	}
}

func TestWithoutNewsAddsMissingFeatures(t *testing.T) {
	want := []string{"PATH=/bin", "FEATURES=-news"}
	if got := withoutNews([]string{"PATH=/bin"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("withoutNews() = %v, want %v", got, want)
	}
}
