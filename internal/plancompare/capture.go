package plancompare

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/planvalidate"
)

type validationEnvelope struct {
	Schema                int `json:"schema"`
	IndependentValidation *struct {
		Schema  int                  `json:"schema"`
		Fixture planvalidate.Fixture `json:"fixture"`
		Plan    planvalidate.Plan    `json:"plan"`
	} `json:"independent_validation"`
}

func ParseAriseValidation(output string) (planvalidate.Fixture, planvalidate.Plan, bool, error) {
	var envelope validationEnvelope
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		return planvalidate.Fixture{}, planvalidate.Plan{}, false, fmt.Errorf("parse Arise validation capture: %w", err)
	}
	if envelope.Schema != 1 {
		return planvalidate.Fixture{}, planvalidate.Plan{}, false, fmt.Errorf("unsupported Arise JSON plan schema %d", envelope.Schema)
	}
	if envelope.IndependentValidation == nil {
		return planvalidate.Fixture{}, planvalidate.Plan{}, false, nil
	}
	if envelope.IndependentValidation.Schema != planvalidate.SchemaVersion {
		return planvalidate.Fixture{}, planvalidate.Plan{}, false,
			fmt.Errorf("unsupported independent validation schema %d", envelope.IndependentValidation.Schema)
	}
	return envelope.IndependentValidation.Fixture, envelope.IndependentValidation.Plan, true, nil
}

func AssessmentFromExternalActions(fixture planvalidate.Fixture, actions []Action) (StateAssessment, planvalidate.Fixture, planvalidate.Plan, error) {
	plan, err := PlanFromActions(fixture, actions)
	if err != nil {
		return StateAssessment{}, planvalidate.Fixture{}, planvalidate.Plan{}, err
	}
	final := planvalidate.ApplyPlan(fixture.Installed, plan).State
	externalFixture := fixture
	externalFixture.Available = append([]planvalidate.Package(nil), fixture.Available...)
	for _, action := range plan.Actions {
		for index, candidate := range externalFixture.Available {
			if candidate.CPV == action.Package.CPV && candidate.Slot == action.Package.Slot &&
				candidate.Repository == action.Package.Repository {
				externalFixture.Available[index] = action.Package
				break
			}
		}
	}
	externalFixture.Domains = map[string][]planvalidate.Package{
		planvalidate.DomainSysroot: final.Packages,
		planvalidate.DomainBroot:   final.Packages,
	}
	validation := planvalidate.ValidatePlanImpact(externalFixture, plan)
	return AssessmentFromValidation(validation, final), externalFixture, plan, nil
}

func PlanFromActions(fixture planvalidate.Fixture, actions []Action) (planvalidate.Plan, error) {
	plan := planvalidate.Plan{Schema: planvalidate.SchemaVersion}
	seen := make(map[string]bool, len(actions))
	for _, action := range actions {
		identity := action.Identity()
		if seen[identity] {
			return planvalidate.Plan{}, fmt.Errorf("external plan contains duplicate action identity %s", identity)
		}
		seen[identity] = true
		candidate, err := matchAvailablePackage(fixture.Available, action)
		if err != nil {
			return planvalidate.Plan{}, err
		}
		if action.EffectiveUse != nil {
			candidate.Use = cloneUse(action.EffectiveUse)
		}
		replaces := ""
		for _, installed := range fixture.Installed {
			if cpFromCPV(installed.CPV) == action.CP && installed.Slot == candidate.Slot {
				replaces = installed.CPV
				break
			}
		}
		plan.Actions = append(plan.Actions, planvalidate.Action{
			Kind: planvalidate.ActionInstall, Package: candidate, Replaces: replaces,
		})
	}
	plan.Decisions.Records = []planvalidate.DecisionRecord{}
	return plan, nil
}

func matchAvailablePackage(available []planvalidate.Package, action Action) (planvalidate.Package, error) {
	var matches []planvalidate.Package
	for _, candidate := range available {
		if candidate.CPV != action.CPV() || candidate.Slot != action.Slot {
			continue
		}
		if action.Repository != "" && candidate.Repository != action.Repository {
			continue
		}
		if action.Subslot != "" && candidate.Subslot != action.Subslot {
			continue
		}
		matches = append(matches, candidate)
	}
	if len(matches) != 1 {
		return planvalidate.Package{}, fmt.Errorf(
			"external action %s:%s::%s matched %d frozen available packages",
			action.CPV(), action.Slot, action.Repository, len(matches),
		)
	}
	return matches[0], nil
}

func CaptureDocument(fixture planvalidate.Fixture, plan planvalidate.Plan) StateDocument {
	return StateDocument{Schema: StateSchemaVersion, Fixture: reduceAvailable(fixture, plan), Plan: plan}
}

func reduceAvailable(fixture planvalidate.Fixture, plan planvalidate.Plan) planvalidate.Fixture {
	selected := make(map[string]bool, len(plan.Actions))
	for _, action := range plan.Actions {
		selected[packageKey(action.Package)] = true
	}
	available := make([]planvalidate.Package, 0, len(selected))
	for _, candidate := range fixture.Available {
		if selected[packageKey(candidate)] {
			available = append(available, candidate)
		}
	}
	fixture.Available = available
	return fixture
}

func packageKey(pkg planvalidate.Package) string {
	return pkg.CPV + "\x00" + pkg.Slot + "\x00" + pkg.Subslot + "\x00" + pkg.Repository
}

func ClassificationPolicyForRequest(request planvalidate.Request, assessments ...StateAssessment) ClassificationPolicy {
	required := make(map[string]bool)
	for _, raw := range request.Targets {
		target, err := atom.ParsePackageAtom(raw)
		if err != nil {
			continue
		}
		for _, assessment := range assessments {
			for _, pkg := range assessment.Packages {
				if pkg.CP != target.CP() || target.Slot != "" && pkg.Slot != target.Slot {
					continue
				}
				required[pkg.Identity()] = true
			}
		}
	}
	result := ClassificationPolicy{}
	for identity := range required {
		result.RequiredIdentities = append(result.RequiredIdentities, identity)
	}
	sort.Strings(result.RequiredIdentities)
	return result
}

func EncodeStateDocument(document StateDocument) ([]byte, error) {
	var output strings.Builder
	encoder := json.NewEncoder(&output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return nil, err
	}
	return []byte(output.String()), nil
}
