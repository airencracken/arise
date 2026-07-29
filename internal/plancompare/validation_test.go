package plancompare

import (
	"testing"

	"github.com/airencracken/arise/internal/planvalidate"
)

func TestClassifiedHarnessUsesIndependentFinalStateValidation(t *testing.T) {
	versionOne := planvalidate.Package{
		CPV: "dev-libs/library-1", Slot: "0", Repository: "gentoo",
		Authority: planvalidate.AuthorityEvaluated,
	}
	versionTwo := versionOne
	versionTwo.CPV = "dev-libs/library-2"
	fixture := planvalidate.Fixture{
		Schema:    planvalidate.SchemaVersion,
		Request:   planvalidate.Request{Operation: "install", Targets: []string{">=dev-libs/library-1"}},
		Available: []planvalidate.Package{versionOne, versionTwo},
	}
	arisePlan := planvalidate.Plan{Schema: planvalidate.SchemaVersion, Actions: []planvalidate.Action{{
		Kind: planvalidate.ActionInstall, Package: versionTwo,
	}}}
	portagePlan := planvalidate.Plan{Schema: planvalidate.SchemaVersion, Actions: []planvalidate.Action{{
		Kind: planvalidate.ActionInstall, Package: versionOne,
	}}}
	ariseValidation := planvalidate.ValidateFinalState(fixture, arisePlan)
	portageValidation := planvalidate.ValidateFinalState(fixture, portagePlan)
	arise := AssessmentFromValidation(ariseValidation, planvalidate.ApplyPlan(fixture.Installed, arisePlan).State)
	portage := AssessmentFromValidation(portageValidation, planvalidate.ApplyPlan(fixture.Installed, portagePlan).State)
	result, err := ClassifyFinalStates(arise, portage, ClassificationPolicy{
		PolicyEquivalentIdentities: []string{"dev-libs/library:0"},
	}, []Difference{{Identity: "dev-libs/library:0", Kind: "version"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Class != ClassValidDivergence || len(result.Differences) != 1 ||
		result.Differences[0].Classification != "policy-equivalent" {
		t.Fatalf("independently valid divergence = %#v", result)
	}

	invalidPortagePlan := planvalidate.Plan{Schema: planvalidate.SchemaVersion}
	invalidPortage := AssessmentFromValidation(
		planvalidate.ValidateFinalState(fixture, invalidPortagePlan),
		planvalidate.ApplyPlan(fixture.Installed, invalidPortagePlan).State,
	)
	result, err = ClassifyFinalStates(arise, invalidPortage, ClassificationPolicy{
		RequiredIdentities: []string{"dev-libs/library:0"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Class != ClassAriseValidPortageInvalid ||
		len(result.Differences) != 1 || result.Differences[0].Classification != "required" {
		t.Fatalf("invalid Portage state classification = %#v", result)
	}
}
