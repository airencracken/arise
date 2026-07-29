package plancompare

import "github.com/airencracken/arise/internal/planvalidate"

func AssessmentFromValidation(validation planvalidate.ValidationResult, state planvalidate.State) StateAssessment {
	packages := make([]StatePackage, len(state.Packages))
	for index, pkg := range state.Packages {
		packages[index] = StatePackage{
			CP: cpFromCPV(pkg.CPV), Version: versionFromCPV(pkg.CPV),
			Slot: pkg.Slot, Subslot: pkg.Subslot, Repository: pkg.Repository,
			EffectiveUse: cloneUse(pkg.Use),
		}
	}
	return StateAssessment{
		Validated: true, Valid: validation.Valid,
		Violations: validationMessages(validation.Violations),
		Packages:   packages,
	}
}

func validationMessages(violations []planvalidate.Violation) []string {
	result := make([]string, len(violations))
	for index, violation := range violations {
		result[index] = violation.Kind + ": " + violation.Message
	}
	return result
}

func cpFromCPV(cpv string) string {
	parsed, err := parseAtom(cpv)
	if err != nil {
		return cpv
	}
	return parsed.CP
}

func versionFromCPV(cpv string) string {
	parsed, err := parseAtom(cpv)
	if err != nil {
		return ""
	}
	return parsed.Version
}

func cloneUse(source map[string]bool) map[string]bool {
	if source == nil {
		return nil
	}
	result := make(map[string]bool, len(source))
	for flag, enabled := range source {
		result[flag] = enabled
	}
	return result
}
