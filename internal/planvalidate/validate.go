package planvalidate

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/depstring"
)

var supportedDependencyClasses = map[string]bool{
	"DEPEND": true, "RDEPEND": true, "BDEPEND": true,
	"IDEPEND": true, "PDEPEND": true,
}

func ValidateFinalState(fixture Fixture, plan Plan) ValidationResult {
	applied := ApplyPlan(fixture.Installed, plan)
	violations := append([]Violation(nil), applied.Violations...)
	if fixture.Schema != SchemaVersion {
		violations = append(violations, violation("unsupported-fixture-schema", "", fmt.Sprint(fixture.Schema), "", "fixture schema is unsupported"))
	}
	if plan.Schema != SchemaVersion {
		violations = append(violations, violation("unsupported-plan-schema", "", fmt.Sprint(plan.Schema), "", "plan schema is unsupported"))
	}
	validateActionAuthority(fixture.Available, plan.Actions, &violations)
	validateActionPolicy(fixture.Policy, plan.Actions, &violations)
	validateActionOrder(plan.Actions, &violations)

	for _, pkg := range applied.State.Packages {
		if _, err := parseCPV(pkg.CPV); err != nil {
			violations = append(violations, violation("invalid-package-identity", pkg.CPV, "", "", err.Error()))
			continue
		}
		classes := make([]string, 0, len(pkg.Dependencies))
		for class := range pkg.Dependencies {
			classes = append(classes, class)
		}
		sort.Strings(classes)
		for _, class := range classes {
			expression := strings.TrimSpace(pkg.Dependencies[class])
			if expression == "" {
				continue
			}
			if !supportedDependencyClasses[class] {
				violations = append(violations, violation("unsupported-dependency-class", pkg.CPV, class, pkg.CPV, "validator does not yet implement this dependency class"))
				continue
			}
			domainPackages, ok := dependencyDomainPackages(fixture, class, applied.State.Packages)
			if !ok {
				violations = append(violations, violation(
					"missing-dependency-domain", pkg.CPV, class, pkg.CPV,
					"fixture does not provide the required independent dependency domain",
				))
				continue
			}
			node, err := depstring.Parse(expression)
			if err != nil {
				violations = append(violations, violation("invalid-dependency-expression", pkg.CPV, expression, pkg.CPV, err.Error()))
				continue
			}
			if err := depstring.ValidatePackageDependenciesEAPI(node, pkg.EAPI); err != nil {
				violations = append(violations, violation("invalid-dependency-expression", pkg.CPV, expression, pkg.CPV, err.Error()))
				continue
			}
			validateNode(node, pkg, domainPackages, &violations)
		}
		if strings.TrimSpace(pkg.RequiredUse) != "" {
			node, err := depstring.Parse(pkg.RequiredUse)
			if err != nil {
				violations = append(violations, violation("invalid-required-use", pkg.CPV, pkg.RequiredUse, pkg.CPV, err.Error()))
			} else if !requiredUseSatisfied(node, pkg.Use) {
				violations = append(violations, violation("required-use-violation", pkg.CPV, pkg.RequiredUse, pkg.CPV, "selected USE state does not satisfy REQUIRED_USE"))
			}
		}
	}
	validateTargets(fixture.Request, applied.State.Packages, &violations)
	validateActionJustification(fixture, plan.Actions, &violations)
	sortViolations(violations)
	violations, truncated, omitted := boundViolations(violations, applied.OmittedViolations)
	if violations == nil {
		violations = []Violation{}
	}
	return ValidationResult{
		Valid:      len(violations) == 0 && omitted == 0,
		Violations: violations, Truncated: truncated, OmittedViolations: omitted,
	}
}

func validateActionJustification(fixture Fixture, actions []Action, violations *[]Violation) {
	byID := make(map[string]Action, len(actions))
	required := make(map[string]bool, len(actions))
	for _, action := range actions {
		if action.ID != "" {
			byID[action.ID] = action
		}
		if action.Kind == ActionInstall &&
			(actionMatchesRequest(action, fixture.Request) || action.Replaces != "") {
			required[action.ID] = true
		}
	}
	var visit func(string)
	visit = func(id string) {
		action, ok := byID[id]
		if !ok {
			return
		}
		for _, prerequisite := range action.Prerequisites {
			if !required[prerequisite] {
				required[prerequisite] = true
				visit(prerequisite)
			}
		}
	}
	for id := range required {
		visit(id)
	}
	for _, action := range actions {
		if action.Kind == ActionInstall && action.ID != "" && !required[action.ID] {
			*violations = append(*violations, violation(
				"unjustified-action", action.Package.CPV, action.ID, "",
				"install action has no independently proven path from a request or retained package requirement",
			))
		}
	}
}

func actionMatchesRequest(action Action, request Request) bool {
	for _, target := range request.Targets {
		constraint, err := atom.ParsePackageAtom(target)
		if err == nil && stateMatches([]Package{action.Package}, constraint, nil) {
			return true
		}
	}
	return false
}

func validateActionOrder(actions []Action, violations *[]Violation) {
	positions := make(map[string]int, len(actions))
	for index, action := range actions {
		if action.ID == "" {
			if len(action.Prerequisites) != 0 {
				*violations = append(*violations, violation("missing-action-id", action.Package.CPV, "", "", "action with prerequisites has no stable identity"))
			}
			continue
		}
		if _, exists := positions[action.ID]; exists {
			*violations = append(*violations, violation("duplicate-action-id", action.Package.CPV, action.ID, "", "plan contains duplicate action identity"))
			continue
		}
		positions[action.ID] = index
	}
	for index, action := range actions {
		for _, prerequisite := range action.Prerequisites {
			position, exists := positions[prerequisite]
			switch {
			case !exists:
				*violations = append(*violations, violation("missing-prerequisite-action", action.Package.CPV, prerequisite, action.ID, "prerequisite is absent from plan"))
			case position >= index:
				*violations = append(*violations, violation("transaction-order-violation", action.Package.CPV, prerequisite, action.ID, "prerequisite does not precede dependent action"))
			}
		}
	}
}

// ValidatePlanImpact rejects violations introduced by a plan while classifying
// identical defects already present in the frozen installed state as
// pre-existing. Request and application violations are never waived.
func ValidatePlanImpact(fixture Fixture, plan Plan) ValidationResult {
	baselineFixture := fixture
	baselineFixture.Request = Request{Operation: "install", Targets: []string{}}
	baseline := ValidateFinalState(baselineFixture, Plan{Schema: plan.Schema})
	planned := ValidateFinalState(fixture, plan)

	// A full record set is required to prove absence of introduced violations.
	// Exact duplicate omission is safe because membership is unchanged; reaching
	// the record cap is not.
	if len(baseline.Violations) == MaxViolationRecords || len(planned.Violations) == MaxViolationRecords {
		inconclusive := violation(
			"comparison-inconclusive", "", "", "",
			"validation impact exceeds the bounded unique diagnostic comparison",
		)
		return ValidationResult{Valid: false, Violations: []Violation{inconclusive}, Truncated: true}
	}

	preExisting := make(map[Violation]bool, len(baseline.Violations))
	for _, item := range baseline.Violations {
		preExisting[item] = true
	}
	introduced := make([]Violation, 0, len(planned.Violations))
	preExistingCount := 0
	for _, item := range planned.Violations {
		if !nonWaivableViolation(item.Kind) && preExisting[item] {
			preExistingCount++
			continue
		}
		introduced = append(introduced, item)
	}
	sortViolations(introduced)
	introduced, truncated, omitted := boundViolations(introduced, 0)
	if introduced == nil {
		introduced = []Violation{}
	}
	return ValidationResult{
		Valid: len(introduced) == 0, Violations: introduced,
		Truncated: truncated, OmittedViolations: omitted, PreExisting: preExistingCount,
	}
}

func nonWaivableViolation(kind string) bool {
	switch kind {
	case "unsupported-fixture-schema", "unsupported-plan-schema",
		"invalid-request-target", "unsupported-atom-semantics",
		"unsatisfied-request-target", "retained-removal-target",
		"unsupported-operation":
		return true
	}
	return strings.HasPrefix(kind, "duplicate-") ||
		strings.HasPrefix(kind, "missing-removal") ||
		strings.HasPrefix(kind, "invalid-replacement") ||
		kind == "already-installed" || kind == "slot-collision" || kind == "unknown-action"
}

func dependencyDomainPackages(fixture Fixture, class string, finalRoot []Package) ([]Package, bool) {
	switch class {
	case "RDEPEND", "PDEPEND":
		return finalRoot, true
	case "DEPEND":
		packages, ok := fixture.Domains[DomainSysroot]
		return packages, ok
	case "BDEPEND", "IDEPEND":
		packages, ok := fixture.Domains[DomainBroot]
		return packages, ok
	default:
		return nil, false
	}
}

func validateActionAuthority(available []Package, actions []Action, violations *[]Violation) {
	for _, action := range actions {
		if action.Kind != ActionInstall {
			continue
		}
		foundIdentity := false
		for _, candidate := range available {
			if packageIdentity(candidate) != packageIdentity(action.Package) {
				continue
			}
			foundIdentity = true
			if !reflect.DeepEqual(candidate, action.Package) {
				*violations = append(*violations, violation("non-authoritative-package-metadata", action.Package.CPV, packageIdentity(action.Package), "", "plan package metadata differs from frozen available metadata"))
			}
			if candidate.Authority != AuthorityMD5Cache && candidate.Authority != AuthorityEvaluated {
				*violations = append(*violations, violation("non-authoritative-package-source", action.Package.CPV, string(candidate.Authority), "", "install target lacks authoritative evaluated repository metadata"))
			}
			break
		}
		if !foundIdentity {
			*violations = append(*violations, violation("unavailable-package", action.Package.CPV, packageIdentity(action.Package), "", "install target is absent from frozen available state"))
		}
	}
}

func boundViolations(violations []Violation, alreadyOmitted int) ([]Violation, bool, int) {
	omitted := alreadyOmitted
	if len(violations) > 1 {
		unique := make([]Violation, 0, len(violations))
		seen := make(map[Violation]bool, len(violations))
		for _, item := range violations {
			if seen[item] {
				omitted++
				continue
			}
			seen[item] = true
			unique = append(unique, item)
		}
		violations = unique
	}
	if len(violations) > MaxViolationRecords {
		omitted += len(violations) - MaxViolationRecords
		violations = violations[:MaxViolationRecords]
	}
	return violations, omitted != 0, omitted
}

func validateTargets(request Request, packages []Package, violations *[]Violation) {
	switch request.Operation {
	case "install", "update":
		for _, target := range request.Targets {
			constraint, err := atom.ParsePackageAtom(target)
			if err != nil {
				*violations = append(*violations, violation("invalid-request-target", "", target, "", err.Error()))
				continue
			}
			if unsupported := unsupportedAtomSemantics(constraint); unsupported != "" {
				*violations = append(*violations, violation("unsupported-atom-semantics", "", target, "", unsupported))
				continue
			}
			if !stateMatches(packages, constraint, nil) {
				*violations = append(*violations, violation("unsatisfied-request-target", "", target, "", "requested target is absent from final state"))
			}
		}
	case "remove":
		for _, target := range request.Targets {
			constraint, err := atom.ParsePackageAtom(target)
			if err != nil {
				*violations = append(*violations, violation("invalid-request-target", "", target, "", err.Error()))
				continue
			}
			if unsupported := unsupportedAtomSemantics(constraint); unsupported != "" {
				*violations = append(*violations, violation("unsupported-atom-semantics", "", target, "", unsupported))
				continue
			}
			if stateMatches(packages, constraint, nil) {
				*violations = append(*violations, violation("retained-removal-target", "", target, "", "removed target remains in final state"))
			}
		}
	default:
		*violations = append(*violations, violation("unsupported-operation", "", request.Operation, "", "request operation is unsupported"))
	}
}

func validateNode(node depstring.DepNode, owner Package, packages []Package, violations *[]Violation) bool {
	switch item := node.(type) {
	case *depstring.AtomDep:
		constraint, err := atom.ParsePackageAtom(item.Atom)
		if err != nil {
			*violations = append(*violations, violation("invalid-dependency-atom", owner.CPV, item.Atom, owner.CPV, err.Error()))
			return false
		}
		if unsupported := unsupportedAtomSemantics(constraint); unsupported != "" {
			*violations = append(*violations, violation("unsupported-atom-semantics", owner.CPV, item.Atom, owner.CPV, unsupported))
			return false
		}
		if stateMatches(packages, constraint, &owner) {
			return true
		}
		*violations = append(*violations, violation("unsatisfied-dependency", owner.CPV, item.Atom, owner.CPV, "final state does not satisfy dependency"))
		return false
	case *depstring.Block:
		return validateBlock(item.String(), owner, packages, violations)
	case *depstring.WeakBlock:
		return validateBlock(item.String(), owner, packages, violations)
	case *depstring.AllOfGroup:
		valid := true
		for _, child := range item.Children {
			if !validateNode(child, owner, packages, violations) {
				valid = false
			}
		}
		return valid
	case *depstring.AnyOfGroup:
		for _, child := range item.Children {
			var candidate []Violation
			if validateNode(child, owner, packages, &candidate) {
				return true
			}
		}
		*violations = append(*violations, violation("unsatisfied-any-of", owner.CPV, item.String(), owner.CPV, "no dependency alternative is satisfied"))
		return false
	case *depstring.UseConditional:
		flag := strings.TrimPrefix(item.Flag, "!")
		enabled := owner.Use[flag]
		if strings.HasPrefix(item.Flag, "!") {
			enabled = !enabled
		}
		if !enabled {
			return true
		}
		valid := true
		for _, child := range item.Children {
			if !validateNode(child, owner, packages, violations) {
				valid = false
			}
		}
		return valid
	default:
		*violations = append(*violations, violation("unsupported-dependency-expression", owner.CPV, node.String(), owner.CPV, "validator does not yet implement this dependency expression"))
		return false
	}
}

func validateBlock(raw string, owner Package, packages []Package, violations *[]Violation) bool {
	constraint, err := atom.ParsePackageAtom(strings.TrimLeft(raw, "!"))
	if err != nil {
		*violations = append(*violations, violation("invalid-blocker-atom", owner.CPV, raw, owner.CPV, err.Error()))
		return false
	}
	if unsupported := unsupportedAtomSemantics(constraint); unsupported != "" {
		*violations = append(*violations, violation("unsupported-atom-semantics", owner.CPV, raw, owner.CPV, unsupported))
		return false
	}
	if !stateMatches(packages, constraint, &owner) {
		return true
	}
	*violations = append(*violations, violation("blocker-violation", owner.CPV, raw, owner.CPV, "blocked package remains in final state"))
	return false
}

func unsupportedAtomSemantics(constraint *atom.Atom) string {
	return ""
}

func stateMatches(packages []Package, constraint *atom.Atom, owner *Package) bool {
	for _, pkg := range packages {
		candidate, err := parseCPV(pkg.CPV)
		if err == nil && matches(candidate, pkg, constraint) && useDependenciesMatch(pkg, constraint.UseFlags, owner) {
			return true
		}
	}
	return false
}

func useDependenciesMatch(target Package, requirements []atom.UseFlag, owner *Package) bool {
	for _, requirement := range requirements {
		if requirement.Conditional {
			if owner == nil {
				return false
			}
			active := owner.Use[requirement.Name]
			if requirement.Negated {
				active = !active
			}
			if !active {
				continue
			}
		}
		targetEnabled, declared := target.Use[requirement.Name]
		if _, inIUse := target.IUse[requirement.Name]; inIUse {
			declared = true
		} else if target.IUse != nil {
			declared = false
		}
		if !declared {
			if requirement.Default == nil {
				return false
			}
			targetEnabled = *requirement.Default
		}
		if requirement.Equal {
			if owner == nil {
				return false
			}
			required := owner.Use[requirement.Name]
			if requirement.Negated {
				required = !required
			}
			if targetEnabled != required {
				return false
			}
			continue
		}
		if requirement.Conditional {
			if !targetEnabled {
				return false
			}
			continue
		}
		if targetEnabled != requirement.Enabled {
			return false
		}
	}
	return true
}

func requiredUseSatisfied(node depstring.DepNode, use map[string]bool) bool {
	switch item := node.(type) {
	case *depstring.AtomDep:
		return use[item.Atom]
	case *depstring.AllOfGroup:
		for _, child := range item.Children {
			if !requiredUseSatisfied(child, use) {
				return false
			}
		}
		return true
	case *depstring.AnyOfGroup:
		for _, child := range item.Children {
			if requiredUseSatisfied(child, use) {
				return true
			}
		}
		return false
	case *depstring.XorOfGroup:
		count := 0
		for _, child := range item.Children {
			if requiredUseSatisfied(child, use) {
				count++
			}
		}
		return count == 1
	case *depstring.AtMostOneOfGroup:
		count := 0
		for _, child := range item.Children {
			if requiredUseSatisfied(child, use) {
				count++
			}
		}
		return count <= 1
	case *depstring.UseConditional:
		flag := strings.TrimPrefix(item.Flag, "!")
		active := use[flag]
		if strings.HasPrefix(item.Flag, "!") {
			active = !active
		}
		if !active {
			return true
		}
		for _, child := range item.Children {
			if !requiredUseSatisfied(child, use) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func matches(candidate *atom.Atom, pkg Package, constraint *atom.Atom) bool {
	if candidate.CP() != constraint.CP() || constraint.Repo != "" && pkg.Repository != constraint.Repo {
		return false
	}
	if constraint.Slot != "" && pkg.Slot != constraint.Slot {
		return false
	}
	if constraint.Subslot != "" && pkg.Subslot != constraint.Subslot {
		return false
	}
	if constraint.Version == nil {
		return true
	}
	if candidate.Version == nil {
		return false
	}
	compared := candidate.Version.Compare(constraint.Version)
	switch constraint.Op {
	case atom.OpEq:
		return compared == 0
	case atom.OpLess:
		return compared < 0
	case atom.OpLessEq:
		return compared <= 0
	case atom.OpGt:
		return compared > 0
	case atom.OpGtEq:
		return compared >= 0
	case atom.OpTilde:
		return versionWithoutRevision(candidate.Version.Raw) == versionWithoutRevision(constraint.Version.Raw)
	case atom.OpEqGlob:
		return strings.HasPrefix(candidate.Version.Raw, strings.TrimSuffix(constraint.Version.Raw, "*"))
	default:
		return false
	}
}

func versionWithoutRevision(raw string) string {
	index := strings.LastIndex(raw, "-r")
	if index < 0 {
		return raw
	}
	for _, character := range raw[index+2:] {
		if character < '0' || character > '9' {
			return raw
		}
	}
	return raw[:index]
}

func parseCPV(cpv string) (*atom.Atom, error) {
	parsed, err := atom.Parse(cpv)
	if err != nil {
		return nil, fmt.Errorf("invalid CPV %q: %w", cpv, err)
	}
	if parsed.Version == nil {
		return nil, fmt.Errorf("invalid CPV %q: version is required", cpv)
	}
	return parsed, nil
}

func violation(kind, pkg, requirement, requiredBy, message string) Violation {
	return Violation{Kind: kind, Package: pkg, Requirement: requirement, RequiredBy: requiredBy, Message: message}
}

func sortViolations(violations []Violation) {
	sort.SliceStable(violations, func(i, j int) bool {
		left := violations[i].Kind + "\x00" + violations[i].Package + "\x00" + violations[i].Requirement + "\x00" + violations[i].RequiredBy + "\x00" + violations[i].Message
		right := violations[j].Kind + "\x00" + violations[j].Package + "\x00" + violations[j].Requirement + "\x00" + violations[j].RequiredBy + "\x00" + violations[j].Message
		return left < right
	})
}
