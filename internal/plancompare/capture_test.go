package plancompare

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/airencracken/arise/internal/planvalidate"
)

func captureFixture() planvalidate.Fixture {
	old := planvalidate.Package{
		CPV: "dev-libs/library-1", Slot: "0", Repository: "gentoo",
		Authority: planvalidate.AuthorityVDB,
	}
	next := old
	next.CPV = "dev-libs/library-2"
	next.Authority = planvalidate.AuthorityEvaluated
	next.Use = map[string]bool{"feature": true}
	return planvalidate.Fixture{
		Schema:    planvalidate.SchemaVersion,
		Request:   planvalidate.Request{Operation: "update", Targets: []string{"dev-libs/library"}},
		Installed: []planvalidate.Package{old}, Available: []planvalidate.Package{next},
		Domains: map[string][]planvalidate.Package{
			planvalidate.DomainSysroot: {next}, planvalidate.DomainBroot: {next},
		},
	}
}

func TestParseAriseValidationAndExternalPlanCapture(t *testing.T) {
	fixture := captureFixture()
	arisePlan := planvalidate.Plan{Schema: planvalidate.SchemaVersion, Actions: []planvalidate.Action{{
		Kind: planvalidate.ActionInstall, Package: fixture.Available[0], Replaces: fixture.Installed[0].CPV,
	}}}
	envelope := map[string]any{
		"schema": 1,
		"independent_validation": map[string]any{
			"schema": planvalidate.SchemaVersion, "fixture": fixture, "plan": arisePlan,
		},
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	gotFixture, gotPlan, ok, err := ParseAriseValidation(string(encoded))
	if err != nil || !ok || !reflect.DeepEqual(gotFixture, fixture) || !reflect.DeepEqual(gotPlan, arisePlan) {
		t.Fatalf("validation capture = %#v, %#v, %t, %v", gotFixture, gotPlan, ok, err)
	}

	actions := []Action{{
		CP: "dev-libs/library", Version: "2", Slot: "0", Repository: "gentoo",
		EffectiveUse: map[string]bool{"feature": false},
	}}
	assessment, externalFixture, externalPlan, err := AssessmentFromExternalActions(fixture, actions)
	if err != nil {
		t.Fatal(err)
	}
	if !assessment.Valid || len(externalPlan.Actions) != 1 ||
		externalPlan.Actions[0].Package.Use["feature"] ||
		externalFixture.Available[0].Use["feature"] {
		t.Fatalf("external assessment = %#v, %#v, %#v", assessment, externalFixture, externalPlan)
	}
	document := CaptureDocument(externalFixture, externalPlan)
	if !document.Fixture.DomainsAliasToRoot || document.Fixture.Domains != nil {
		t.Fatalf("capture retained duplicated root domains: %#v", document.Fixture)
	}
	first, err := EncodeStateDocument(document)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncodeStateDocument(document)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("capture encoding is nondeterministic: %v", err)
	}
	decoded, err := DecodeStateDocument(bytes.NewReader(first))
	if err != nil || !decoded.Valid || !reflect.DeepEqual(decoded.Packages, assessment.Packages) {
		t.Fatalf("capture round trip = %#v, %v", decoded, err)
	}
}

func TestExternalPlanCaptureRejectsMissingAndDuplicateCandidates(t *testing.T) {
	fixture := captureFixture()
	missing := Action{CP: "dev-libs/missing", Version: "1", Slot: "0", Repository: "gentoo"}
	if _, err := PlanFromActions(fixture, []Action{missing}); err == nil {
		t.Fatal("missing external candidate accepted")
	}
	action := Action{CP: "dev-libs/library", Version: "2", Slot: "0", Repository: "gentoo"}
	if _, err := PlanFromActions(fixture, []Action{action, action}); err == nil {
		t.Fatal("duplicate external action accepted")
	}
}

func TestClassificationPolicyForRequestIsCanonical(t *testing.T) {
	request := planvalidate.Request{Targets: []string{">=dev-libs/library-1"}}
	state := StateAssessment{Packages: []StatePackage{
		{CP: "dev-libs/library", Version: "2", Slot: "1"},
		{CP: "dev-libs/library", Version: "2", Slot: "0"},
	}}
	policy := ClassificationPolicyForRequest(request, state)
	want := []string{"dev-libs/library:0", "dev-libs/library:1"}
	if !reflect.DeepEqual(policy.RequiredIdentities, want) {
		t.Fatalf("required request identities = %v, want %v", policy.RequiredIdentities, want)
	}
}

func TestCaptureDocumentReducesAvailablePackagesWithoutMutation(t *testing.T) {
	fixture := captureFixture()
	unrelated := fixture.Available[0]
	unrelated.CPV = "dev-libs/unrelated-1"
	fixture.Available = append(fixture.Available, unrelated)
	plan := planvalidate.Plan{Schema: planvalidate.SchemaVersion, Actions: []planvalidate.Action{{
		Kind: planvalidate.ActionInstall, Package: fixture.Available[0], Replaces: fixture.Installed[0].CPV,
	}}}

	document := CaptureDocument(fixture, plan)
	if len(document.Fixture.Available) != 1 || document.Fixture.Available[0].CPV != "dev-libs/library-2" {
		t.Fatalf("reduced available packages = %#v", document.Fixture.Available)
	}
	if len(fixture.Available) != 2 {
		t.Fatalf("capture mutated source fixture: %#v", fixture.Available)
	}
	validation := planvalidate.ValidatePlanImpact(document.Fixture, document.Plan)
	if !validation.Valid {
		t.Fatalf("reduced capture changed validation: %#v", validation)
	}
}
