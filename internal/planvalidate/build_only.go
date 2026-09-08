package planvalidate

// Build-only actions produce archives, not installed providers. Validate build
// requirements against the existing dependency roots and leave runtime state
// untouched. In particular, another archive in this plan cannot supply a tool.
func validateBuildOnly(fixture Fixture, plan Plan) ValidationResult {
	baselineFixture := fixture
	baselineFixture.Request.BuildOnly = false
	baselineFixture.Request.Targets = nil
	baselineFixture.Request.OriginalTargets = nil
	baselineFixture.Request.Operation = "install"
	baseline := ValidateFinalState(baselineFixture, Plan{Schema: plan.Schema})
	violations := append([]Violation(nil), baseline.Violations...)
	actionStart := len(violations)
	validateActionAuthority(fixture.Available, plan.Actions, &violations)
	validateActionPolicy(fixture.Policy, plan.Actions, &violations)
	validateActionOrder(plan.Actions, &violations)
	validateDecisionLedger(plan, &violations)
	installed := newPackageState(fixture.Installed)
	outputs := append([]Package(nil), fixture.Installed...)
	for _, action := range plan.Actions {
		if action.Kind != ActionInstall || action.Package.MergeType == "binary" {
			violations = append(violations, violation("unsupported-build-action", action.Package.CPV, action.Kind, "", "build-only plans require source build actions"))
			continue
		}
		owner := action.Package
		if _, err := parseCPV(owner.CPV); err != nil {
			violations = append(violations, violation("invalid-package-identity", owner.CPV, "", "", err.Error()))
			continue
		}
		outputs = append(outputs, owner)
		dependencies := make(map[string]string)
		for class, expression := range owner.Dependencies {
			if class == "DEPEND" || class == "BDEPEND" || !supportedDependencyClasses[class] {
				dependencies[class] = expression
			}
		}
		owner.Dependencies = dependencies
		validatePackageDependencies(fixture, owner, true, installed, &violations)
		validatePackageRequiredUse(owner, &violations)
	}
	if fixture.Request.PartialMode != "onlydeps" {
		validateTargets(fixture.Request, newPackageState(outputs), &violations)
		validateActionJustification(fixture, plan, outputs, &violations)
	}
	// An invalid build is not a waivable historical defect, even when the same
	// CPV and REQUIRED_USE failure are already present in the installed state.
	for index := actionStart; index < len(violations); index++ {
		violations[index].Message = "build action: " + violations[index].Message
	}
	sortViolations(violations)
	violations, truncated, omitted := boundViolations(violations, baseline.OmittedViolations)
	if violations == nil {
		violations = []Violation{}
	}
	return ValidationResult{Valid: len(violations) == 0 && omitted == 0, Violations: violations, Truncated: truncated, OmittedViolations: omitted}
}
