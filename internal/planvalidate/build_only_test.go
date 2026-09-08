package planvalidate

import "testing"

func TestBuildOnlyValidatesInstalledBuildToolsWithoutChangingRuntimeState(t *testing.T) {
	for _, class := range []string{"DEPEND", "BDEPEND", "IDEPEND", "RDEPEND", "PDEPEND"} {
		t.Run(class, func(t *testing.T) {
			owner := pkg("app/pkg-2", map[string]string{class: "dev/tool"})
			owner.Authority = AuthorityEvaluated
			owner.EAPI = "8"
			fixture := Fixture{Schema: 1, Request: Request{Operation: "install", Targets: []string{"app/pkg"}, BuildOnly: true}, Available: []Package{owner}, DomainsAliasToRoot: true}
			plan := Plan{Schema: 1, Actions: []Action{{Kind: ActionInstall, Package: owner}}}
			result := ValidatePlanImpact(fixture, plan)
			wantValid := class != "DEPEND" && class != "BDEPEND"
			if result.Valid != wantValid {
				t.Fatalf("valid=%v want %v: %#v", result.Valid, wantValid, result)
			}
			tool := pkg("dev/tool-1", nil)
			tool.Authority = AuthorityEvaluated
			fixture.Available = append(fixture.Available, tool)
			fixture.Request.Targets = append(fixture.Request.Targets, "dev/tool")
			plan.Actions = append(plan.Actions, Action{Kind: ActionInstall, Package: tool})
			if result := ValidatePlanImpact(fixture, plan); result.Valid != wantValid {
				t.Fatalf("building tool must not install it: %#v", result)
			}
			fixture.Installed = []Package{pkg("dev/tool-1", nil)}
			if result := ValidatePlanImpact(fixture, plan); !result.Valid {
				t.Fatalf("installed build tool rejected: %#v", result)
			}
		})
	}
}

func TestBuildOnlyCannotBreakRetainedRuntimeOrBypassAuthority(t *testing.T) {
	old := pkg("dev/lib-1", nil)
	old.Subslot = "1"
	owner := pkg("app/client-1", map[string]string{"RDEPEND": "dev/lib:0/1="})
	next := pkg("dev/lib-2", nil)
	next.Authority = AuthorityEvaluated
	next.Subslot = "2"
	fixture := Fixture{Schema: 1, Request: Request{Operation: "install", Targets: []string{"dev/lib"}, BuildOnly: true}, Installed: []Package{old, owner}, Available: []Package{next}, DomainsAliasToRoot: true}
	plan := Plan{Schema: 1, Actions: []Action{{Kind: ActionInstall, Package: next, Replaces: old.CPV}}}
	if result := ValidatePlanImpact(fixture, plan); !result.Valid {
		t.Fatalf("building library incorrectly replaces installed provider: %#v", result)
	}
	plan.Actions[0].Package.EAPI = "invalid"
	if result := ValidatePlanImpact(fixture, plan); result.Valid {
		t.Fatal("build-only bypassed frozen metadata")
	}
}

func TestBuildOnlyDoesNotWaiveBrokenRequiredUseOnRebuild(t *testing.T) {
	owner := pkg("app/pkg-1", nil)
	owner.RequiredUse = "feature"
	owner.IUse = map[string]bool{"feature": true}
	fixture := Fixture{Schema: 1, Request: Request{Operation: "install", Targets: []string{"app/pkg"}, BuildOnly: true}, Installed: []Package{owner}, DomainsAliasToRoot: true}
	owner.Authority = AuthorityEvaluated
	fixture.Available = []Package{owner}
	result := ValidatePlanImpact(fixture, Plan{Schema: 1, Actions: []Action{{Kind: ActionInstall, Package: owner, Replaces: owner.CPV}}})
	if result.Valid || !hasViolation(result, "required-use-violation") {
		t.Fatalf("invalid build waived as historical defect: %#v", result)
	}
}
