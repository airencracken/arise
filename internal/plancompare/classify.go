package plancompare

import (
	"fmt"
	"sort"
	"strings"
)

const (
	MaxStateDifferences  = 256
	MaxActionDiagnostics = 256
)

const (
	ClassEquivalentValid          = "equivalent-valid"
	ClassValidDivergence          = "valid-divergence"
	ClassAriseValidPortageInvalid = "arise-valid-portage-invalid"
	ClassAriseInvalidPortageValid = "arise-invalid-portage-valid"
	ClassBothInvalid              = "both-invalid"
	ClassInconclusive             = "inconclusive"
)

type StatePackage struct {
	CP           string            `json:"cp"`
	Version      string            `json:"version"`
	Slot         string            `json:"slot"`
	Subslot      string            `json:"subslot,omitempty"`
	Repository   string            `json:"repository,omitempty"`
	EffectiveUse map[string]bool   `json:"effective_use,omitempty"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
}

func (pkg StatePackage) Identity() string {
	return pkg.CP + ":" + pkg.Slot
}

func (pkg StatePackage) CPV() string {
	if pkg.Version == "" {
		return pkg.CP
	}
	return pkg.CP + "-" + pkg.Version
}

type StateAssessment struct {
	Validated  bool           `json:"validated"`
	Valid      bool           `json:"valid"`
	Violations []string       `json:"violations,omitempty"`
	Packages   []StatePackage `json:"packages"`
}

type ClassificationPolicy struct {
	RequiredIdentities         []string `json:"required_identities,omitempty"`
	OptionalIdentities         []string `json:"optional_identities,omitempty"`
	PolicyEquivalentIdentities []string `json:"policy_equivalent_identities,omitempty"`
}

type StateDifference struct {
	Identity           string        `json:"identity"`
	Kind               string        `json:"kind"`
	Classification     string        `json:"classification"`
	Arise              *StatePackage `json:"arise,omitempty"`
	Portage            *StatePackage `json:"portage,omitempty"`
	UseMismatch        []string      `json:"use_mismatch,omitempty"`
	DependencyMismatch []string      `json:"dependency_mismatch,omitempty"`
}

type ClassifiedComparison struct {
	Class                      string            `json:"class"`
	Equivalent                 bool              `json:"equivalent"`
	Differences                []StateDifference `json:"differences"`
	ActionDiagnostics          []Difference      `json:"action_diagnostics,omitempty"`
	ActionDiagnosticsTruncated bool              `json:"action_diagnostics_truncated"`
	OmittedActionDiagnostics   int               `json:"omitted_action_diagnostics"`
	Truncated                  bool              `json:"truncated"`
	OmittedDifferences         int               `json:"omitted_differences"`
}

func ClassifyFinalStates(arise, portage StateAssessment, policy ClassificationPolicy, actionDiagnostics []Difference) (ClassifiedComparison, error) {
	if err := validateClassificationPolicy(policy); err != nil {
		return ClassifiedComparison{}, err
	}
	ariseIndex, err := indexState(arise.Packages)
	if err != nil {
		return ClassifiedComparison{}, fmt.Errorf("normalize Arise final state: %w", err)
	}
	portageIndex, err := indexState(portage.Packages)
	if err != nil {
		return ClassifiedComparison{}, fmt.Errorf("normalize Portage final state: %w", err)
	}
	differences := compareStates(ariseIndex, portageIndex, policy, arise.Valid && portage.Valid)
	result := ClassifiedComparison{
		Differences:       differences,
		ActionDiagnostics: append([]Difference(nil), actionDiagnostics...),
	}
	if len(result.ActionDiagnostics) > MaxActionDiagnostics {
		result.OmittedActionDiagnostics = len(result.ActionDiagnostics) - MaxActionDiagnostics
		result.ActionDiagnostics = result.ActionDiagnostics[:MaxActionDiagnostics]
		result.ActionDiagnosticsTruncated = true
	}
	if result.Differences == nil {
		result.Differences = []StateDifference{}
	}
	if len(result.Differences) > MaxStateDifferences {
		result.OmittedDifferences = len(result.Differences) - MaxStateDifferences
		result.Differences = result.Differences[:MaxStateDifferences]
		result.Truncated = true
	}
	switch {
	case !arise.Validated || !portage.Validated:
		result.Class = ClassInconclusive
	case arise.Valid && portage.Valid && len(differences) == 0:
		result.Class, result.Equivalent = ClassEquivalentValid, true
	case arise.Valid && portage.Valid:
		result.Class = ClassValidDivergence
	case arise.Valid:
		result.Class = ClassAriseValidPortageInvalid
	case portage.Valid:
		result.Class = ClassAriseInvalidPortageValid
	default:
		result.Class = ClassBothInvalid
	}
	return result, nil
}

func indexState(packages []StatePackage) (map[string]StatePackage, error) {
	result := make(map[string]StatePackage, len(packages))
	for _, pkg := range packages {
		if strings.TrimSpace(pkg.CP) == "" || strings.TrimSpace(pkg.Slot) == "" {
			return nil, fmt.Errorf("package CP and slot are required")
		}
		parsed, err := parseAtom(pkg.CP)
		if err != nil || parsed.CP != pkg.CP || parsed.Version != "" {
			return nil, fmt.Errorf("invalid package CP %q", pkg.CP)
		}
		identity := pkg.Identity()
		if _, exists := result[identity]; exists {
			return nil, fmt.Errorf("duplicate package identity %s", identity)
		}
		cloned := pkg
		if pkg.EffectiveUse != nil {
			cloned.EffectiveUse = make(map[string]bool, len(pkg.EffectiveUse))
			for flag, enabled := range pkg.EffectiveUse {
				cloned.EffectiveUse[flag] = enabled
			}
		}
		result[identity] = cloned
	}
	return result, nil
}

func validateClassificationPolicy(policy ClassificationPolicy) error {
	owners := make(map[string]string)
	for class, identities := range map[string][]string{
		"required":          policy.RequiredIdentities,
		"optional":          policy.OptionalIdentities,
		"policy-equivalent": policy.PolicyEquivalentIdentities,
	} {
		for _, identity := range identities {
			if previous, exists := owners[identity]; exists && previous != class {
				return fmt.Errorf("classification policy assigns %s to both %s and %s", identity, previous, class)
			}
			owners[identity] = class
		}
	}
	return nil
}

func compareStates(arise, portage map[string]StatePackage, policy ClassificationPolicy, bothValid bool) []StateDifference {
	keys := make(map[string]bool, len(arise)+len(portage))
	for key := range arise {
		keys[key] = true
	}
	for key := range portage {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	required := stringSet(policy.RequiredIdentities)
	optional := stringSet(policy.OptionalIdentities)
	equivalent := stringSet(policy.PolicyEquivalentIdentities)
	var result []StateDifference
	for _, identity := range ordered {
		left, leftOK := arise[identity]
		right, rightOK := portage[identity]
		difference := StateDifference{Identity: identity}
		switch {
		case !leftOK:
			difference.Kind, difference.Portage = "only-portage", packagePointer(right)
		case !rightOK:
			difference.Kind, difference.Arise = "only-arise", packagePointer(left)
		case left.Version != right.Version || left.Subslot != right.Subslot:
			difference.Kind, difference.Arise, difference.Portage = "version", packagePointer(left), packagePointer(right)
		case left.Repository != right.Repository:
			difference.Kind, difference.Arise, difference.Portage = "location", packagePointer(left), packagePointer(right)
		default:
			difference.DependencyMismatch = stateDependencyMismatches(left.Dependencies, right.Dependencies)
			if len(difference.DependencyMismatch) != 0 {
				difference.Kind, difference.Arise, difference.Portage = "dependency", packagePointer(left), packagePointer(right)
				break
			}
			difference.UseMismatch = stateUseMismatches(left.EffectiveUse, right.EffectiveUse)
			if len(difference.UseMismatch) == 0 {
				continue
			}
			difference.Kind, difference.Arise, difference.Portage = "use", packagePointer(left), packagePointer(right)
		}
		switch {
		case equivalent[identity]:
			difference.Classification = "policy-equivalent"
		case optional[identity] || bothValid && !required[identity]:
			difference.Classification = "optional"
		default:
			difference.Classification = "required"
		}
		result = append(result, difference)
	}
	return result
}

func stateDependencyMismatches(left, right map[string]string) []string {
	keys := make(map[string]bool, len(left)+len(right))
	for key, expression := range left {
		if strings.TrimSpace(expression) != "" {
			keys[key] = true
		}
	}
	for key, expression := range right {
		if strings.TrimSpace(expression) != "" {
			keys[key] = true
		}
	}
	var result []string
	for key := range keys {
		if strings.TrimSpace(left[key]) != strings.TrimSpace(right[key]) {
			result = append(result, key)
		}
	}
	sort.Strings(result)
	return result
}

func packagePointer(pkg StatePackage) *StatePackage {
	copy := pkg
	return &copy
}

func stateUseMismatches(left, right map[string]bool) []string {
	var result []string
	// VDB and evaluated repository metadata can expose different implicit IUSE
	// domains (for example architecture versus elibc expansion flags). Only a
	// flag declared by both frozen package views has comparable semantics.
	for key, leftValue := range left {
		if rightValue, comparable := right[key]; comparable && leftValue != rightValue {
			result = append(result, key)
		}
	}
	sort.Strings(result)
	return result
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}
