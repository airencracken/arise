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
			if issue := applyInstallAction(current, action); issue != nil {
				violations = append(violations, *issue)
			}
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

func sameSlotIdentity(packages map[string]Package, candidate Package, excluded string) string {
	candidateAtom, err := parseCPV(candidate.CPV)
	if err != nil {
		return ""
	}
	for identity, pkg := range packages {
		if identity == excluded {
			continue
		}
		existingAtom, err := parseCPV(pkg.CPV)
		if err == nil && existingAtom.CP() == candidateAtom.CP() && pkg.Slot == candidate.Slot {
			return packageIdentity(pkg)
		}
	}
	return ""
}

func applyInstallAction(current map[string]Package, action Action) *Violation {
	var replaced string
	reject := func(code, atom, cause, message string) *Violation {
		issue := violation(code, action.Package.CPV, atom, cause, message)
		return &issue
	}
	if action.Replaces != "" {
		var matches int
		replaced, matches = findCPV(current, action.Replaces)
		if matches != 1 {
			return reject("invalid-replacement-target", action.Replaces, "", fmt.Sprintf("replacement target matched %d installed packages", matches))
		}
	}
	identity := packageIdentity(action.Package)
	if _, exists := current[identity]; exists && identity != replaced {
		return reject("already-installed", "", "", "install target already exists")
	}
	if conflict := sameSlotIdentity(current, action.Package, replaced); conflict != "" {
		return reject("slot-collision", action.Package.Slot, conflict, "plan leaves two package instances in the same slot")
	}
	// No state changes occur until every condition for this action has passed.
	if replaced != "" {
		delete(current, replaced)
	}
	current[identity] = clonePackage(action.Package)
	return nil
}
