package planvalidate

import (
	"fmt"
)

const (
	ActionInstall = "install"
	ActionRemove  = "remove"
)

// ApplyPlan is a pure transformation. It never mutates installed or plan.
func ApplyPlan(installed []Package, plan Plan) ApplicationResult {
	current := make(map[string]Package, len(installed))
	var violations []Violation
	for _, pkg := range installed {
		identity := packageIdentity(pkg)
		if _, exists := current[identity]; exists {
			violations = append(violations, violation("duplicate-installed-package", pkg.CPV, "", "", "installed state contains duplicate "+identity))
			continue
		}
		current[identity] = clonePackage(pkg)
	}

	seenActions := make(map[string]bool, len(plan.Actions))
	for _, action := range plan.Actions {
		key := action.Kind + "\x00" + packageIdentity(action.Package) + "\x00" + action.Replaces
		if seenActions[key] {
			violations = append(violations, violation("duplicate-action", action.Package.CPV, "", "", "plan contains duplicate action"))
			continue
		}
		seenActions[key] = true
		switch action.Kind {
		case ActionRemove:
			identity := packageIdentity(action.Package)
			if _, exists := current[identity]; !exists {
				violations = append(violations, violation("missing-removal-target", action.Package.CPV, "", "", "remove target is not installed"))
				continue
			}
			delete(current, identity)
		case ActionInstall:
			if action.Replaces != "" {
				replaced, matches := findCPV(current, action.Replaces)
				if matches != 1 {
					violations = append(violations, violation("invalid-replacement-target", action.Package.CPV, action.Replaces, "", fmt.Sprintf("replacement target matched %d installed packages", matches)))
					continue
				}
				delete(current, replaced)
			}
			identity := packageIdentity(action.Package)
			if _, exists := current[identity]; exists {
				violations = append(violations, violation("already-installed", action.Package.CPV, "", "", "install target already exists"))
				continue
			}
			if conflict := sameSlotIdentity(current, action.Package); conflict != "" {
				violations = append(violations, violation("slot-collision", action.Package.CPV, action.Package.Slot, conflict, "plan leaves two package instances in the same slot"))
				continue
			}
			current[identity] = clonePackage(action.Package)
		default:
			violations = append(violations, violation("unknown-action", action.Package.CPV, action.Kind, "", "plan contains an unknown action kind"))
		}
	}

	packages := make([]Package, 0, len(current))
	for _, pkg := range current {
		packages = append(packages, pkg)
	}
	result := ApplicationResult{State: canonicalState(packages), Violations: violations}
	sortViolations(result.Violations)
	result.Violations, result.Truncated, result.OmittedViolations = boundViolations(result.Violations, 0)
	if result.Violations == nil {
		result.Violations = []Violation{}
	}
	return result
}

func findCPV(packages map[string]Package, cpv string) (string, int) {
	var identity string
	count := 0
	for key, pkg := range packages {
		if pkg.CPV == cpv {
			identity = key
			count++
		}
	}
	return identity, count
}

func sameSlotIdentity(packages map[string]Package, candidate Package) string {
	candidateAtom, err := parseCPV(candidate.CPV)
	if err != nil {
		return ""
	}
	for _, pkg := range packages {
		existingAtom, err := parseCPV(pkg.CPV)
		if err == nil && existingAtom.CP() == candidateAtom.CP() && pkg.Slot == candidate.Slot {
			return packageIdentity(pkg)
		}
	}
	return ""
}
