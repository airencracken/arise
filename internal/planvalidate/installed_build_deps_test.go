package planvalidate

import "testing"

func TestPlanImpactGoUpgradeDoesNotRevalidateAgeBuildDependencies(t *testing.T) {
	for _, class := range []string{"DEPEND", "BDEPEND", "IDEPEND", "RDEPEND", "PDEPEND"} {
		for _, rebuild := range []bool{false, true} {
			t.Run(class+map[bool]string{false: "/unchanged", true: "/rebuilt"}[rebuild], func(t *testing.T) {
				oldGo := pkg("dev-lang/go-1.26.5", nil)
				oldGo.Subslot = "1.26.5"
				nextGo := pkg("dev-lang/go-1.26.6", nil)
				nextGo.Subslot = "1.26.6"
				nextGo.Authority = AuthorityEvaluated
				age := pkg("app-crypt/age-1.3.1-r1", map[string]string{class: ">=dev-lang/go-1.24.11:0/1.26.5="})
				age.EAPI = "8"
				fixture := Fixture{Schema: 1, Request: Request{Operation: "install", Targets: []string{"dev-lang/go"}}, Installed: []Package{oldGo, age}, Available: []Package{nextGo}, DomainsAliasToRoot: true}
				plan := Plan{Schema: 1, Actions: []Action{{Kind: ActionInstall, Package: nextGo, Replaces: oldGo.CPV}}}
				if rebuild {
					age.Authority = AuthorityEvaluated
					fixture.Available = append(fixture.Available, age)
					fixture.Request.Targets = append(fixture.Request.Targets, "app-crypt/age")
					plan.Actions = append(plan.Actions, Action{Kind: ActionInstall, Package: age, Replaces: age.CPV})
				}
				result := ValidatePlanImpact(fixture, plan)
				mustReject := rebuild || class == "RDEPEND" || class == "PDEPEND"
				if mustReject {
					if !hasViolation(result, "unsatisfied-dependency") {
						t.Fatalf("required dependency escaped validation: %#v", result)
					}
				} else if !result.Valid {
					t.Fatalf("historical build dependency blocked Go upgrade: %#v", result)
				}
			})
		}
	}
}

func TestReinstallCannotWaiveExistingRuntimeFailure(t *testing.T) {
	for _, class := range []string{"RDEPEND", "PDEPEND"} {
		t.Run(class, func(t *testing.T) {
			owner := pkg("app-misc/client-1", map[string]string{class: "dev-libs/missing"})
			fixture := Fixture{Schema: 1, Request: Request{Operation: "install", Targets: []string{"app-misc/client"}}, Installed: []Package{owner}, DomainsAliasToRoot: true}
			if result := ValidatePlanImpact(fixture, Plan{Schema: 1}); !result.Valid || result.PreExisting != 1 {
				t.Fatalf("untouched failure not classified: %#v", result)
			}
			owner.Authority = AuthorityEvaluated
			fixture.Available = []Package{owner}
			plan := Plan{Schema: 1, Actions: []Action{{Kind: ActionInstall, Package: owner, Replaces: owner.CPV}}}
			if result := ValidatePlanImpact(fixture, plan); result.Valid || !hasViolation(result, "unsatisfied-dependency") {
				t.Fatalf("reinstall waived broken dependency: %#v", result)
			}
		})
	}
}

func TestPredictionPreservesRetainedPackageMetadata(t *testing.T) {
	for _, class := range []string{"DEPEND", "BDEPEND", "IDEPEND", "RDEPEND", "PDEPEND"} {
		t.Run(class, func(t *testing.T) {
			owner := pkg("app-misc/client-1", map[string]string{class: "  dev-libs/provider:=  "})
			provider := pkg("dev-libs/provider-2", nil)
			provider.Subslot = "2"
			state := State{Packages: []Package{owner, provider}}
			predicted, err := PredictCommittedState(state)
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range predicted.Packages {
				if candidate.CPV == owner.CPV && candidate.Dependencies[class] != owner.Dependencies[class] {
					t.Fatalf("retained metadata rewritten: %#v", candidate)
				}
			}
			if result := ValidateCommittedState(predicted, state); !result.Valid {
				t.Fatalf("unchanged VDB rejected: %#v", result)
			}
		})
	}
}

func TestBinaryDependencyLifetimeAndAuthority(t *testing.T) {
	for _, mergeType := range []string{"", "source", "binary", "unknown"} {
		for _, class := range []string{"DEPEND", "BDEPEND", "IDEPEND", "RDEPEND", "PDEPEND"} {
			t.Run(mergeType+"/"+class, func(t *testing.T) {
				owner := pkg("app-misc/client-1", map[string]string{class: "dev-libs/missing"})
				owner.Authority, owner.MergeType, owner.EAPI = AuthorityEvaluated, mergeType, "8"
				fixture := Fixture{Schema: 1, Request: Request{Operation: "install", Targets: []string{"app-misc/client"}}, Available: []Package{owner}, DomainsAliasToRoot: true}
				plan := Plan{Schema: 1, Actions: []Action{{Kind: ActionInstall, Package: owner}}}
				result := ValidatePlanImpact(fixture, plan)
				wantValid := mergeType == "binary" && (class == "DEPEND" || class == "BDEPEND")
				if result.Valid != wantValid {
					t.Fatalf("valid=%v, want %v: %#v", result.Valid, wantValid, result)
				}
				if mergeType == "source" {
					plan.Actions[0].Package.MergeType = "binary"
					if result := ValidatePlanImpact(fixture, plan); result.Valid || !hasViolation(result, "non-authoritative-package-metadata") {
						t.Fatalf("forged binary classification accepted: %#v", result)
					}
				}
			})
		}
	}
}

func TestSourceDependDomainFollowsEAPI(t *testing.T) {
	for _, eapi := range []string{"6", "7", "8"} {
		for _, domain := range []string{DomainBroot, DomainSysroot} {
			t.Run(eapi+"/"+domain, func(t *testing.T) {
				owner := pkg("app-misc/client-1", map[string]string{"DEPEND": "dev-libs/provider"})
				owner.Authority, owner.EAPI = AuthorityEvaluated, eapi
				fixture := Fixture{Schema: 1, Request: Request{Operation: "install", Targets: []string{"app-misc/client"}}, Available: []Package{owner}, Domains: map[string][]Package{domain: {pkg("dev-libs/provider-1", nil)}}}
				result := ValidateFinalState(fixture, Plan{Schema: 1, Actions: []Action{{Kind: ActionInstall, Package: owner}}})
				wantValid := (eapi == "6") == (domain == DomainBroot)
				if result.Valid != wantValid {
					t.Fatalf("valid=%v, want %v: %#v", result.Valid, wantValid, result)
				}
			})
		}
	}
}

func TestPredictionPreservesBinaryBuiltMetadata(t *testing.T) {
	owner := pkg("app-misc/client-1", map[string]string{"RDEPEND": "dev-libs/provider:="})
	owner.Authority, owner.MergeType = AuthorityEvaluated, "binary"
	provider := pkg("dev-libs/provider-2", nil)
	provider.Subslot = "2"
	predicted, err := PredictCommittedState(State{Packages: []Package{owner, provider}})
	if err != nil {
		t.Fatal(err)
	}
	if got := predicted.Packages[0].Dependencies["RDEPEND"]; got != owner.Dependencies["RDEPEND"] {
		t.Fatalf("binary metadata rebound: %q", got)
	}
}
