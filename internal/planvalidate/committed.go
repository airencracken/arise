package planvalidate

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/depstring"
)

// PredictCommittedState applies deterministic merge-time metadata
// transformations to a pure planned state. In particular, built := operators
// are bound to the selected provider subslot recorded in VDB.
func PredictCommittedState(planned State) (State, error) {
	result := canonicalState(planned.Packages)
	for index := range result.Packages {
		pkg := &result.Packages[index]
		for _, class := range []string{"DEPEND", "RDEPEND", "BDEPEND", "IDEPEND", "PDEPEND"} {
			raw := strings.TrimSpace(pkg.Dependencies[class])
			if raw == "" {
				continue
			}
			tree, err := depstring.Parse(raw)
			if err != nil {
				return State{}, fmt.Errorf("%s %s: %w", pkg.CPV, class, err)
			}
			if err := bindBuiltSlotOperators(tree, *pkg, result.Packages); err != nil {
				return State{}, fmt.Errorf("%s %s: %w", pkg.CPV, class, err)
			}
			pkg.Dependencies[class] = tree.String()
		}
	}
	return result, nil
}

func bindBuiltSlotOperators(node depstring.DepNode, owner Package, packages []Package) error {
	switch item := node.(type) {
	case *depstring.AtomDep:
		constraint, err := atom.ParsePackageAtom(item.Atom)
		if err != nil {
			return err
		}
		if constraint.SlotOp != atom.SlotOpEq || constraint.Subslot != "" {
			return nil
		}
		var matches []Package
		for _, candidate := range packages {
			if stateMatches([]Package{candidate}, constraint, &owner) {
				matches = append(matches, candidate)
			}
		}
		if len(matches) != 1 {
			return fmt.Errorf("built slot operator %s matched %d final providers", item.Atom, len(matches))
		}
		constraint.Slot = matches[0].Slot
		constraint.Subslot = matches[0].Subslot
		if constraint.Subslot == "" {
			constraint.Subslot = constraint.Slot
		}
		item.Atom = constraint.String()
	case *depstring.AllOfGroup:
		for _, child := range item.Children {
			if err := bindBuiltSlotOperators(child, owner, packages); err != nil {
				return err
			}
		}
	case *depstring.AnyOfGroup:
		for _, child := range item.Children {
			if err := bindBuiltSlotOperators(child, owner, packages); err != nil {
				return err
			}
		}
	case *depstring.UseConditional:
		for _, child := range item.Children {
			if err := bindBuiltSlotOperators(child, owner, packages); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateCommittedState compares the package-manager state predicted before
// execution with state independently observed from the committed VDB.
func ValidateCommittedState(predicted, actual State) ValidationResult {
	left, leftErr := committedStateIndex(predicted)
	right, rightErr := committedStateIndex(actual)
	var violations []Violation
	if leftErr != nil {
		violations = append(violations, violation("invalid-predicted-state", "", "", "", leftErr.Error()))
	}
	if rightErr != nil {
		violations = append(violations, violation("invalid-actual-state", "", "", "", rightErr.Error()))
	}
	keys := make(map[string]bool, len(left)+len(right))
	for key := range left {
		keys[key] = true
	}
	for key := range right {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		expected, expectedOK := left[key]
		observed, observedOK := right[key]
		switch {
		case !expectedOK:
			violations = append(violations, violation("unexpected-actual-package", observed.CPV, key, "", "package was not predicted"))
		case !observedOK:
			violations = append(violations, violation("missing-actual-package", expected.CPV, key, "", "predicted package was not committed"))
		case expected.CPV != observed.CPV || expected.Subslot != observed.Subslot || expected.Repository != observed.Repository:
			violations = append(violations, violation("actual-package-identity-mismatch", observed.CPV, key, expected.CPV, "committed identity differs from prediction"))
		case !reflect.DeepEqual(normalizedEffectiveUse(expected), normalizedEffectiveUse(observed)):
			violations = append(violations, violation("actual-use-mismatch", observed.CPV, key, expected.CPV, "committed USE differs from prediction"))
		case !reflect.DeepEqual(expected.Dependencies, observed.Dependencies):
			violations = append(violations, violation("actual-dependency-mismatch", observed.CPV, key, expected.CPV, "committed dependency bindings differ from prediction"))
		}
	}
	sortViolations(violations)
	violations, truncated, omitted := boundViolations(violations, 0)
	if violations == nil {
		violations = []Violation{}
	}
	return ValidationResult{Valid: len(violations) == 0, Violations: violations, Truncated: truncated, OmittedViolations: omitted}
}

func committedStateIndex(state State) (map[string]Package, error) {
	result := make(map[string]Package, len(state.Packages))
	for _, pkg := range state.Packages {
		key := cpFromPackage(pkg.CPV) + ":" + pkg.Slot
		if key == ":"+pkg.Slot {
			return nil, fmt.Errorf("invalid CPV %q", pkg.CPV)
		}
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("duplicate package identity %s", key)
		}
		result[key] = pkg
	}
	return result, nil
}

func normalizedEffectiveUse(pkg Package) map[string]bool {
	result := make(map[string]bool, len(pkg.IUse))
	for flag := range pkg.IUse {
		result[flag] = pkg.Use[flag]
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
