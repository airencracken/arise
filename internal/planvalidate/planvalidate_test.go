package planvalidate

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

func pkg(cpv string, dependencies map[string]string) Package {
	return Package{CPV: cpv, Slot: "0", Repository: "gentoo", Authority: AuthorityVDB, Dependencies: dependencies}
}

func validUpgradeFixture() (Fixture, Plan) {
	oldLibrary := pkg("dev-libs/library-1.0", nil)
	newLibrary := pkg("dev-libs/library-2.0", nil)
	client := pkg("app-misc/client-1.0", map[string]string{"RDEPEND": ">=dev-libs/library-2"})
	newLibrary.Authority = AuthorityEvaluated
	client.Authority = AuthorityEvaluated
	return Fixture{
			Schema:  SchemaVersion,
			Request: Request{Operation: "update", Targets: []string{"app-misc/client"}},
			Installed: []Package{
				oldLibrary,
				pkg("app-misc/client-1.0", map[string]string{"RDEPEND": ">=dev-libs/library-1"}),
			},
			Available: []Package{newLibrary, client},
		}, Plan{
			Schema: SchemaVersion,
			Actions: []Action{
				{Kind: ActionInstall, Package: newLibrary, Replaces: oldLibrary.CPV},
				{Kind: ActionInstall, Package: client, Replaces: "app-misc/client-1.0"},
			},
		}
}

func TestApplyPlanUnitUpgrade(t *testing.T) {
	fixture, plan := validUpgradeFixture()
	result := ApplyPlan(fixture.Installed, plan)
	if len(result.Violations) != 0 {
		t.Fatalf("ApplyPlan violations = %#v", result.Violations)
	}
	got := []string{result.State.Packages[0].CPV, result.State.Packages[1].CPV}
	want := []string{"app-misc/client-1.0", "dev-libs/library-2.0"}
	if !slices.Equal(got, want) {
		t.Fatalf("final CPVs = %v, want %v", got, want)
	}
}

func TestApplyPlanStructuralViolations(t *testing.T) {
	installed := pkg("dev-libs/library-1.0", nil)
	replacement := pkg("dev-libs/library-2.0", nil)
	cases := []struct {
		name      string
		installed []Package
		action    Action
		kind      string
	}{
		{"duplicate installed", []Package{installed, installed}, Action{}, "duplicate-installed-package"},
		{"missing removal", nil, Action{Kind: ActionRemove, Package: installed}, "missing-removal-target"},
		{"missing replacement", []Package{installed}, Action{Kind: ActionInstall, Package: replacement, Replaces: "dev-libs/missing-1"}, "invalid-replacement-target"},
		{"ambiguous replacement", []Package{{CPV: installed.CPV, Slot: "0", Repository: "gentoo"}, {CPV: installed.CPV, Slot: "1", Repository: "overlay"}}, Action{Kind: ActionInstall, Package: replacement, Replaces: installed.CPV}, "invalid-replacement-target"},
		{"already installed", []Package{installed}, Action{Kind: ActionInstall, Package: installed}, "already-installed"},
		{"slot collision", []Package{installed}, Action{Kind: ActionInstall, Package: replacement}, "slot-collision"},
		{"unknown action", nil, Action{Kind: "teleport", Package: replacement}, "unknown-action"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			actions := []Action{test.action}
			if test.name == "duplicate installed" {
				actions = nil
			}
			result := ApplyPlan(test.installed, Plan{Schema: 1, Actions: actions})
			if !hasApplicationViolation(result, test.kind) {
				t.Fatalf("missing %q violation: %#v", test.kind, result.Violations)
			}
		})
	}

	duplicate := Action{Kind: ActionInstall, Package: replacement}
	result := ApplyPlan(nil, Plan{Schema: 1, Actions: []Action{duplicate, duplicate}})
	if !hasApplicationViolation(result, "duplicate-action") {
		t.Fatalf("duplicate action accepted: %#v", result)
	}
}

func TestValidateFinalStateIntegration(t *testing.T) {
	fixture, plan := validUpgradeFixture()
	result := ValidateFinalState(fixture, plan)
	if !result.Valid || len(result.Violations) != 0 {
		t.Fatalf("valid upgrade rejected: %#v", result)
	}
}

func TestUncachedOverlayConstrainedDocutilsRegression(t *testing.T) {
	installedDisplay := pkg("gui-libs/display-manager-init-1.1.2-r3", nil)
	installedDocutils := pkg("dev-python/docutils-0.22.3", nil)
	sphinx := pkg("dev-python/sphinx-8.2.3", map[string]string{"RDEPEND": "<dev-python/docutils-0.23"})
	overlayDisplay := Package{
		CPV: "gui-libs/display-manager-init-1.1.2-r4", Slot: "0",
		Repository: "xlibre", Authority: AuthorityEvaluated,
	}
	newDocutils := pkg("dev-python/docutils-0.23", nil)
	newDocutils.Authority = AuthorityMD5Cache
	fixture := Fixture{
		Schema:    SchemaVersion,
		Request:   Request{Operation: "update", Targets: []string{"gui-libs/display-manager-init"}},
		Installed: []Package{installedDisplay, installedDocutils, sphinx},
		Available: []Package{overlayDisplay, newDocutils},
	}

	validPlan := Plan{Schema: SchemaVersion, Actions: []Action{{
		Kind: ActionInstall, Package: overlayDisplay, Replaces: installedDisplay.CPV,
	}}}
	if result := ValidateFinalState(fixture, validPlan); !result.Valid {
		t.Fatalf("authoritative uncached overlay update rejected: %#v", result)
	}

	brokenPlan := cloneJSON(t, validPlan)
	brokenPlan.Actions = append(brokenPlan.Actions, Action{
		Kind: ActionInstall, Package: newDocutils, Replaces: installedDocutils.CPV,
	})
	result := ValidateFinalState(fixture, brokenPlan)
	if result.Valid || !hasViolation(result, "unsatisfied-dependency") {
		t.Fatalf("constrained Docutils update accepted: %#v", result)
	}

	unauthoritative := cloneJSON(t, validPlan)
	unauthoritative.Actions[0].Package.Authority = ""
	fixture.Available[0].Authority = ""
	result = ValidateFinalState(fixture, unauthoritative)
	if result.Valid || !hasViolation(result, "non-authoritative-package-source") {
		t.Fatalf("uncached overlay without evaluated metadata accepted: %#v", result)
	}
}

func TestMutationCasesAreRejected(t *testing.T) {
	fixture, plan := validUpgradeFixture()
	cases := map[string]func(Plan) Plan{
		"missing dependency action": func(mutated Plan) Plan {
			mutated.Actions = mutated.Actions[1:]
			return mutated
		},
		"duplicate action": func(mutated Plan) Plan {
			mutated.Actions = append(mutated.Actions, mutated.Actions[0])
			return mutated
		},
		"contradictory removal": func(mutated Plan) Plan {
			mutated.Actions = append(mutated.Actions, Action{Kind: ActionRemove, Package: mutated.Actions[1].Package})
			return mutated
		},
		"fabricated metadata": func(mutated Plan) Plan {
			mutated.Actions[0].Package.Dependencies = map[string]string{"RDEPEND": "dev-libs/phantom"}
			return mutated
		},
		"unknown action": func(mutated Plan) Plan {
			mutated.Actions[0].Kind = "teleport"
			return mutated
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			mutated := Plan{Schema: plan.Schema, Actions: append([]Action(nil), plan.Actions...)}
			result := ValidateFinalState(fixture, mutate(mutated))
			if result.Valid || len(result.Violations) == 0 {
				t.Fatalf("mutated plan accepted: %#v", result)
			}
		})
	}
}

func TestDependencyAndBlockerContract(t *testing.T) {
	cases := []struct {
		name       string
		dependency string
		installed  []Package
		valid      bool
		kind       string
	}{
		{"version accepted", ">=dev-libs/library-2", []Package{pkg("dev-libs/library-2.1", nil)}, true, ""},
		{"version rejected", "<dev-libs/library-2", []Package{pkg("dev-libs/library-2.1", nil)}, false, "unsatisfied-dependency"},
		{"repository accepted", "dev-libs/library::overlay", []Package{{CPV: "dev-libs/library-2.1", Slot: "0", Repository: "overlay", Authority: AuthorityVDB}}, true, ""},
		{"repository rejected", "dev-libs/library::overlay", []Package{pkg("dev-libs/library-2.1", nil)}, false, "unsatisfied-dependency"},
		{"blocker rejected", "!dev-libs/library", []Package{pkg("dev-libs/library-2.1", nil)}, false, "blocker-violation"},
		{"any-of accepted", "|| ( dev-libs/missing dev-libs/library )", []Package{pkg("dev-libs/library-2.1", nil)}, true, ""},
		{"enabled conditional rejected", "feature? ( dev-libs/missing )", nil, false, "unsatisfied-dependency"},
		{"disabled conditional accepted", "feature? ( dev-libs/missing )", nil, true, ""},
		{"use dependency accepted", "dev-libs/library[ssl]", []Package{{CPV: "dev-libs/library-2.1", Slot: "0", Repository: "gentoo", Authority: AuthorityVDB, Use: map[string]bool{"ssl": true}, IUse: map[string]bool{"ssl": true}}}, true, ""},
		{"use dependency rejected", "dev-libs/library[ssl]", []Package{pkg("dev-libs/library-2.1", nil)}, false, "unsatisfied-dependency"},
		{"slot operator accepted", "dev-libs/library:=", []Package{pkg("dev-libs/library-2.1", nil)}, true, ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			owner := pkg("app-misc/client-1.0", map[string]string{"RDEPEND": test.dependency})
			if test.name == "enabled conditional rejected" {
				owner.Use = map[string]bool{"feature": true}
			}
			fixture := Fixture{Schema: 1, Request: Request{Operation: "install", Targets: []string{"app-misc/client"}}, Installed: append(test.installed, owner)}
			result := ValidateFinalState(fixture, Plan{Schema: 1})
			if result.Valid != test.valid {
				t.Fatalf("Valid = %v, violations = %#v", result.Valid, result.Violations)
			}
			if test.kind != "" && !hasViolation(result, test.kind) {
				t.Fatalf("missing %q violation: %#v", test.kind, result.Violations)
			}
		})
	}
}

func TestDependencyDomainsAreExplicitAndIndependent(t *testing.T) {
	owner := pkg("app-misc/client-1", map[string]string{
		"DEPEND":  "dev-libs/target",
		"BDEPEND": "dev-libs/host",
		"IDEPEND": "dev-libs/installer",
		"RDEPEND": "dev-libs/runtime",
		"PDEPEND": "dev-libs/post",
	})
	fixture := Fixture{
		Schema:  SchemaVersion,
		Request: Request{Operation: "install", Targets: []string{"app-misc/client"}},
		Installed: []Package{
			owner, pkg("dev-libs/runtime-1", nil), pkg("dev-libs/post-1", nil),
		},
		Domains: map[string][]Package{
			DomainSysroot: {pkg("dev-libs/target-1", nil)},
			DomainBroot:   {pkg("dev-libs/host-1", nil), pkg("dev-libs/installer-1", nil)},
		},
	}
	if result := ValidateFinalState(fixture, Plan{Schema: SchemaVersion}); !result.Valid {
		t.Fatalf("valid cross-domain dependencies rejected: %#v", result)
	}
	delete(fixture.Domains, DomainBroot)
	result := ValidateFinalState(fixture, Plan{Schema: SchemaVersion})
	if result.Valid || !hasViolation(result, "missing-dependency-domain") {
		t.Fatalf("missing BROOT domain accepted: %#v", result)
	}
}

func TestDependencyDomainsCanExplicitlyAliasFinalRoot(t *testing.T) {
	provider := pkg("dev-libs/provider-1", nil)
	owner := pkg("app-misc/client-1", map[string]string{
		"DEPEND": "dev-libs/provider", "BDEPEND": "dev-libs/provider",
	})
	fixture := Fixture{
		Schema: SchemaVersion, Request: Request{Operation: "install", Targets: []string{"app-misc/client"}},
		Installed: []Package{owner, provider}, DomainsAliasToRoot: true,
	}
	if result := ValidateFinalState(fixture, Plan{Schema: SchemaVersion}); !result.Valid {
		t.Fatalf("aliased dependency domains rejected: %#v", result)
	}
	fixture.DomainsAliasToRoot = false
	result := ValidateFinalState(fixture, Plan{Schema: SchemaVersion})
	if result.Valid || !hasViolation(result, "missing-dependency-domain") {
		t.Fatalf("implicit missing domains accepted: %#v", result)
	}
}

func TestUseDependencyConditionalsDefaultsAndEquality(t *testing.T) {
	target := Package{
		CPV: "dev-libs/library-1", Slot: "0", Repository: "gentoo",
		Authority: AuthorityVDB,
		Use:       map[string]bool{"ssl": true, "debug": false},
		IUse:      map[string]bool{"ssl": true, "debug": true},
	}
	cases := []struct {
		name       string
		dependency string
		ownerUse   map[string]bool
		valid      bool
	}{
		{"positive", "dev-libs/library[ssl]", nil, true},
		{"negative", "dev-libs/library[-debug]", nil, true},
		{"conditional active", "dev-libs/library[ssl?]", map[string]bool{"ssl": true}, true},
		{"conditional inactive", "dev-libs/library[debug?]", map[string]bool{"debug": false}, true},
		{"conditional inactive missing target flag", "dev-libs/library[missing?]", map[string]bool{"missing": false}, true},
		{"equal", "dev-libs/library[ssl=]", map[string]bool{"ssl": true}, true},
		{"negated equal rejected", "dev-libs/library[!ssl=]", map[string]bool{"ssl": true}, false},
		{"missing default enabled", "dev-libs/library[implicit(+)]", nil, true},
		{"missing default disabled rejected", "dev-libs/library[implicit(-)]", nil, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			owner := pkg("app-misc/client-1", map[string]string{"RDEPEND": test.dependency})
			owner.Use = test.ownerUse
			fixture := Fixture{
				Schema:    SchemaVersion,
				Request:   Request{Operation: "install", Targets: []string{"app-misc/client"}},
				Installed: []Package{owner, target},
			}
			result := ValidateFinalState(fixture, Plan{Schema: SchemaVersion})
			if result.Valid != test.valid {
				t.Fatalf("Valid = %v, violations = %#v", result.Valid, result.Violations)
			}
		})
	}
}

func TestRequiredUseCardinalityAndConditionals(t *testing.T) {
	cases := []struct {
		expression string
		use        map[string]bool
		valid      bool
	}{
		{"^^ ( a b )", map[string]bool{"a": true}, true},
		{"^^ ( a b )", map[string]bool{"a": true, "b": true}, false},
		{"?? ( a b )", map[string]bool{}, true},
		{"?? ( a b )", map[string]bool{"a": true, "b": true}, false},
		{"feature? ( child )", map[string]bool{"feature": false}, true},
		{"feature? ( child )", map[string]bool{"feature": true, "child": false}, false},
	}
	for _, test := range cases {
		owner := pkg("app-misc/client-1", nil)
		owner.RequiredUse = test.expression
		owner.Use = test.use
		fixture := Fixture{
			Schema:    SchemaVersion,
			Request:   Request{Operation: "install", Targets: []string{"app-misc/client"}},
			Installed: []Package{owner},
		}
		result := ValidateFinalState(fixture, Plan{Schema: SchemaVersion})
		if result.Valid != test.valid {
			t.Fatalf("%q with %#v: Valid = %v, violations = %#v", test.expression, test.use, result.Valid, result.Violations)
		}
	}
}

func TestFrozenInstallPolicy(t *testing.T) {
	candidate := pkg("app-misc/client-2", nil)
	candidate.Authority = AuthorityMD5Cache
	candidate.EAPI = "8"
	candidate.Keywords = []string{"~amd64"}
	candidate.License = "|| ( MIT GPL-3 )"
	fixture := Fixture{
		Schema:    SchemaVersion,
		Request:   Request{Operation: "install", Targets: []string{"app-misc/client"}},
		Available: []Package{candidate},
		Policy: Policy{
			AcceptedKeywords: []string{"~amd64"},
			AcceptedLicenses: []string{"-*", "MIT"},
			SupportedEAPIs:   []string{"8", "9"},
		},
	}
	plan := Plan{Schema: SchemaVersion, Actions: []Action{{Kind: ActionInstall, Package: candidate}}}
	if result := ValidateFinalState(fixture, plan); !result.Valid {
		t.Fatalf("accepted frozen policy rejected: %#v", result)
	}
	for name, mutate := range map[string]func(*Package){
		"mask":    func(pkg *Package) { pkg.Masked = true },
		"keyword": func(pkg *Package) { pkg.Keywords = []string{"arm64"} },
		"license": func(pkg *Package) { pkg.License = "EULA" },
		"eapi":    func(pkg *Package) { pkg.EAPI = "10" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := cloneJSON(t, candidate)
			mutate(&changed)
			changedFixture := cloneJSON(t, fixture)
			changedFixture.Available = []Package{changed}
			result := ValidateFinalState(changedFixture, Plan{Schema: SchemaVersion, Actions: []Action{{Kind: ActionInstall, Package: changed}}})
			if result.Valid {
				t.Fatalf("policy mutation accepted: %#v", result)
			}
		})
	}
}

func TestPackageSpecificPolicyProvenance(t *testing.T) {
	candidate := pkg("app-misc/client-2", nil)
	candidate.Authority = AuthorityMD5Cache
	candidate.EAPI = "8"
	candidate.Keywords = []string{"~amd64"}
	candidate.License = "EULA"
	candidate.Policy = PackagePolicy{
		BaseKeyword:    "amd64",
		KeywordChanges: []string{"~amd64"},
		LicenseChanges: []string{"@BINARY"},
	}
	fixture := Fixture{
		Schema: SchemaVersion, Request: Request{Operation: "install", Targets: []string{"app-misc/client"}},
		Available: []Package{candidate},
		Policy: Policy{
			AcceptedLicenses: []string{"-*"},
			SupportedEAPIs:   []string{"7", "8", "9"},
			LicenseGroups:    map[string][]string{"BINARY": {"EULA"}},
		},
	}
	plan := Plan{Schema: SchemaVersion, Actions: []Action{{Kind: ActionInstall, Package: candidate}}}
	if result := ValidateFinalState(fixture, plan); !result.Valid {
		t.Fatalf("package-specific policy rejected: %#v", result)
	}
	for name, mutate := range map[string]func(*Package){
		"keyword removal": func(pkg *Package) { pkg.Policy.KeywordChanges = []string{"-~amd64"} },
		"license removal": func(pkg *Package) { pkg.Policy.LicenseChanges = []string{"-@BINARY"} },
		"mask": func(pkg *Package) {
			pkg.Masked = true
			pkg.Policy.Masked = true
			pkg.Policy.MaskAtom = "=app-misc/client-2"
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := cloneJSON(t, candidate)
			mutate(&changed)
			changedFixture := cloneJSON(t, fixture)
			changedFixture.Available = []Package{changed}
			result := ValidateFinalState(changedFixture, Plan{Schema: SchemaVersion, Actions: []Action{{Kind: ActionInstall, Package: changed}}})
			if result.Valid {
				t.Fatalf("policy mutation accepted: %#v", result)
			}
		})
	}
}

func TestTransactionOrderingAndProviderProof(t *testing.T) {
	dependency := pkg("dev-libs/library-2", nil)
	dependency.Authority = AuthorityEvaluated
	target := pkg("app-misc/client-2", map[string]string{"RDEPEND": "dev-libs/library"})
	target.Authority = AuthorityEvaluated
	fixture := Fixture{
		Schema: SchemaVersion, Request: Request{Operation: "install", Targets: []string{"app-misc/client"}},
		Available: []Package{dependency, target},
	}
	plan := Plan{Schema: SchemaVersion, Actions: []Action{
		{ID: "library", Kind: ActionInstall, Package: dependency},
		{ID: "client", Kind: ActionInstall, Package: target, Prerequisites: []string{"library"}},
	}}
	plan.Decisions = testDecisionLedger(t, plan.Actions)
	if result := ValidateFinalState(fixture, plan); !result.Valid {
		t.Fatalf("ordered provider plan rejected: %#v", result)
	}

	reversed := cloneJSON(t, plan)
	slices.Reverse(reversed.Actions)
	result := ValidateFinalState(fixture, reversed)
	if result.Valid || !hasViolation(result, "transaction-order-violation") {
		t.Fatalf("reversed transaction accepted: %#v", result)
	}

	orphan := pkg("dev-libs/orphan-1", nil)
	orphan.Authority = AuthorityEvaluated
	orphanPlan := cloneJSON(t, plan)
	orphanPlan.Actions = append(orphanPlan.Actions, Action{ID: "orphan", Kind: ActionInstall, Package: orphan})
	orphanFixture := cloneJSON(t, fixture)
	orphanFixture.Available = append(orphanFixture.Available, orphan)
	result = ValidateFinalState(orphanFixture, orphanPlan)
	if result.Valid || !hasViolation(result, "unjustified-action") {
		t.Fatalf("unjustified provider accepted: %#v", result)
	}

	oldOrphan := pkg("dev-libs/orphan-0", nil)
	replacementPlan := cloneJSON(t, plan)
	replacementPlan.Actions = append(replacementPlan.Actions, Action{
		ID: "orphan-replacement", Kind: ActionInstall, Package: orphan, Replaces: oldOrphan.CPV,
	})
	replacementPlan.Decisions = testDecisionLedger(t, replacementPlan.Actions)
	replacementFixture := cloneJSON(t, fixture)
	replacementFixture.Installed = append(replacementFixture.Installed, oldOrphan)
	replacementFixture.Available = append(replacementFixture.Available, orphan)
	result = ValidateFinalState(replacementFixture, replacementPlan)
	if result.Valid || !hasViolation(result, "unjustified-action") {
		t.Fatalf("unjustified replacement accepted: %#v", result)
	}
}

func TestReplacementIsJustifiedByRepairedRuntimeDependency(t *testing.T) {
	perl := pkg("dev-lang/perl-5.42.2", nil)
	perl.Slot, perl.Subslot = "0", "5.42"
	old := pkg("dev-perl/consumer-1", map[string]string{
		"RDEPEND": "dev-lang/perl:0/5.40=",
	})
	old.Authority = AuthorityVDB
	replacement := pkg("dev-perl/consumer-2", map[string]string{
		"RDEPEND": "dev-lang/perl:0=",
	})
	replacement.Authority = AuthorityEvaluated
	action := Action{
		ID: "consumer-rebuild", Kind: ActionInstall, Package: replacement,
		Replaces: old.CPV,
	}
	fixture := Fixture{
		Schema:    SchemaVersion,
		Request:   Request{Operation: "install", Targets: []string{}},
		Installed: []Package{old, perl}, Available: []Package{replacement},
		Domains: map[string][]Package{
			DomainSysroot: {replacement, perl},
			DomainBroot:   {replacement, perl},
		},
	}
	plan := Plan{
		Schema: SchemaVersion, Actions: []Action{action},
	}
	plan.Decisions = testDecisionLedger(t, plan.Actions)

	if result := ValidateFinalState(fixture, plan); !result.Valid {
		t.Fatalf("runtime dependency repair rejected: %#v", result)
	}

	noDefect := cloneJSON(t, fixture)
	noDefect.Installed[1].Subslot = "5.40"
	result := ValidateFinalState(noDefect, plan)
	if result.Valid || !hasViolation(result, "unjustified-action") {
		t.Fatalf("replacement without baseline defect accepted: %#v", result)
	}

	unrepaired := cloneJSON(t, plan)
	unrepaired.Actions[0].Package.Dependencies["RDEPEND"] = "dev-lang/perl:0/5.40="
	unrepairedFixture := cloneJSON(t, fixture)
	unrepairedFixture.Available[0] = unrepaired.Actions[0].Package
	result = ValidateFinalState(unrepairedFixture, unrepaired)
	if result.Valid || !hasViolation(result, "unsatisfied-dependency") ||
		!hasViolation(result, "unjustified-action") {
		t.Fatalf("replacement retaining broken dependency accepted: %#v", result)
	}
}

func testDecisionLedger(t *testing.T, actions []Action) DecisionLedger {
	t.Helper()
	ledger := DecisionLedger{Records: make([]DecisionRecord, 0, len(actions))}
	for _, action := range actions {
		state := "available"
		if action.Kind == ActionRemove {
			state = "installed"
		}
		record := DecisionRecord{
			ID:      strings.Join([]string{action.Package.CPV, action.Package.Slot, action.Package.Repository, state}, "|"),
			Outcome: "selected", State: state, CPV: action.Package.CPV,
			Slot: action.Package.Slot, Repository: action.Package.Repository,
			ActionID: action.ID, Reasons: []string{"test selection"},
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		ledger.EncodedBytes += len(encoded)
		ledger.Records = append(ledger.Records, record)
	}
	sort.Slice(ledger.Records, func(i, j int) bool { return ledger.Records[i].ID < ledger.Records[j].ID })
	return ledger
}

func TestTransactionOrderingRejectsMalformedProofs(t *testing.T) {
	candidate := pkg("app-misc/client-1", nil)
	candidate.Authority = AuthorityEvaluated
	fixture := Fixture{
		Schema: SchemaVersion, Request: Request{Operation: "install", Targets: []string{"app-misc/client"}},
		Available: []Package{candidate},
	}
	cases := []struct {
		name    string
		actions []Action
		kind    string
	}{
		{"missing id", []Action{{Kind: ActionInstall, Package: candidate, Prerequisites: []string{"missing"}}}, "missing-action-id"},
		{"missing prerequisite", []Action{{ID: "client", Kind: ActionInstall, Package: candidate, Prerequisites: []string{"missing"}}}, "missing-prerequisite-action"},
		{"duplicate id", []Action{{ID: "same", Kind: ActionInstall, Package: candidate}, {ID: "same", Kind: ActionRemove, Package: candidate}}, "duplicate-action-id"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result := ValidateFinalState(fixture, Plan{Schema: SchemaVersion, Actions: test.actions})
			if result.Valid || !hasViolation(result, test.kind) {
				t.Fatalf("malformed ordering proof accepted: %#v", result)
			}
		})
	}
}

func TestDecisionLedgerActionContract(t *testing.T) {
	candidate := pkg("app-misc/client-1", nil)
	candidate.Authority = AuthorityEvaluated
	action := Action{ID: "root|app-misc/client-1|0||gentoo", Kind: ActionInstall, Package: candidate}
	record := DecisionRecord{
		ID: "app-misc/client-1|0|gentoo|available", Outcome: "selected",
		State: "available", CPV: candidate.CPV, Slot: candidate.Slot,
		Repository: candidate.Repository, ActionID: action.ID,
		Reasons: []string{"explicit target"},
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	fixture := Fixture{
		Schema: SchemaVersion, Request: Request{Operation: "install", Targets: []string{"app-misc/client"}},
		Available: []Package{candidate},
	}
	plan := Plan{
		Schema: SchemaVersion, Actions: []Action{action},
		Decisions: DecisionLedger{Records: []DecisionRecord{record}, EncodedBytes: len(encoded)},
	}
	if result := ValidateFinalState(fixture, plan); !result.Valid {
		t.Fatalf("valid decision ledger rejected: %#v", result)
	}
	for name, mutate := range map[string]func(*Plan){
		"missing": func(plan *Plan) {
			plan.Decisions = DecisionLedger{}
		},
		"fabricated action": func(plan *Plan) {
			plan.Decisions.Records[0].ActionID = "fabricated"
		},
		"non-selected action": func(plan *Plan) {
			plan.Decisions.Records[0].Outcome = "skipped"
		},
		"empty reason": func(plan *Plan) {
			plan.Decisions.Records[0].Reasons = nil
		},
		"byte count": func(plan *Plan) {
			plan.Decisions.EncodedBytes++
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := cloneJSON(t, plan)
			mutate(&changed)
			result := ValidateFinalState(fixture, changed)
			if result.Valid {
				t.Fatalf("decision mutation accepted: %#v", result)
			}
		})
	}
}

func TestDecisionLedgerCanonicalOrderAndTruncationContract(t *testing.T) {
	records := []DecisionRecord{
		{ID: "b", Outcome: "skipped", State: "available", CPV: "cat/b-1", Slot: "0", Reasons: []string{"lower preference"}},
		{ID: "a", Outcome: "rejected", State: "available", CPV: "cat/a-1", Slot: "0", Reasons: []string{"masked"}},
	}
	bytes := 0
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		bytes += len(encoded)
	}
	result := ValidateFinalState(
		Fixture{Schema: SchemaVersion, Request: Request{Operation: "install"}},
		Plan{Schema: SchemaVersion, Decisions: DecisionLedger{Records: records, EncodedBytes: bytes, Truncated: true}},
	)
	if result.Valid || !hasViolation(result, "noncanonical-decision-order") ||
		!hasViolation(result, "invalid-decision-truncation") {
		t.Fatalf("malformed canonical ledger accepted: %#v", result)
	}
}

func TestPlanImpactClassifiesLegacyDefectsWithoutWaivingRequests(t *testing.T) {
	legacy := pkg("app-misc/legacy-1", map[string]string{"RDEPEND": "${RDEPEND}"})
	target := pkg("dev-libs/library-1", nil)
	fixture := Fixture{
		Schema:    SchemaVersion,
		Request:   Request{Operation: "install", Targets: []string{"dev-libs/library"}},
		Installed: []Package{legacy, target},
	}
	result := ValidatePlanImpact(fixture, Plan{Schema: SchemaVersion})
	if !result.Valid || result.PreExisting == 0 || len(result.Violations) != 0 {
		t.Fatalf("unchanged legacy defect was not classified: %#v", result)
	}

	fixture.Request.Targets = []string{">=dev-libs/library-2"}
	result = ValidatePlanImpact(fixture, Plan{Schema: SchemaVersion})
	if result.Valid || !hasViolation(result, "unsatisfied-request-target") {
		t.Fatalf("pre-existing comparison waived requested target: %#v", result)
	}
}

func TestPlanImpactRejectsIntroducedSemanticDefect(t *testing.T) {
	fixture, plan := validUpgradeFixture()
	fixture.Installed = append(fixture.Installed, pkg("app-misc/legacy-1", map[string]string{"RDEPEND": "${RDEPEND}"}))
	plan.Actions[1].Package.Dependencies = map[string]string{"RDEPEND": "dev-libs/missing"}
	fixture.Available[1] = plan.Actions[1].Package
	result := ValidatePlanImpact(fixture, plan)
	if result.Valid || !hasViolation(result, "unsatisfied-dependency") {
		t.Fatalf("introduced invalid dependency was waived: %#v", result)
	}
}

func TestPlanImpactNeverWaivesSchemaViolations(t *testing.T) {
	fixture := Fixture{Schema: SchemaVersion, Request: Request{Operation: "install"}}
	result := ValidatePlanImpact(fixture, Plan{Schema: SchemaVersion + 1})
	if result.Valid || !hasViolation(result, "unsupported-plan-schema") {
		t.Fatalf("invalid plan schema was classified as pre-existing: %#v", result)
	}
}

func TestRemoveRequestContract(t *testing.T) {
	target := pkg("app-misc/target-1", nil)
	fixture := Fixture{Schema: 1, Request: Request{Operation: "remove", Targets: []string{"app-misc/target"}}, Installed: []Package{target}}
	valid := ValidateFinalState(fixture, Plan{Schema: 1, Actions: []Action{{Kind: ActionRemove, Package: target}}})
	if !valid.Valid {
		t.Fatalf("valid removal rejected: %#v", valid)
	}
	invalid := ValidateFinalState(fixture, Plan{Schema: 1})
	if invalid.Valid || !hasViolation(invalid, "retained-removal-target") {
		t.Fatalf("retained removal target accepted: %#v", invalid)
	}
}

func TestApplyPlanAtomicityDoesNotMutateInputsOnFailure(t *testing.T) {
	fixture, plan := validUpgradeFixture()
	plan.Actions = append(plan.Actions, Action{Kind: ActionRemove, Package: pkg("app-misc/not-installed-1", nil)})
	beforeFixture := cloneJSON(t, fixture)
	beforePlan := cloneJSON(t, plan)
	_ = ValidateFinalState(fixture, plan)
	if !reflect.DeepEqual(fixture, beforeFixture) || !reflect.DeepEqual(plan, beforePlan) {
		t.Fatal("validation mutated its fixture or plan after a rejected action")
	}
}

func TestPropertyInputOrderDoesNotChangeResult(t *testing.T) {
	fixture, plan := validUpgradeFixture()
	want := ValidateFinalState(fixture, plan)
	random := rand.New(rand.NewSource(42))
	for iteration := 0; iteration < 100; iteration++ {
		candidateFixture := cloneJSON(t, fixture)
		candidatePlan := cloneJSON(t, plan)
		random.Shuffle(len(candidateFixture.Installed), func(i, j int) {
			candidateFixture.Installed[i], candidateFixture.Installed[j] = candidateFixture.Installed[j], candidateFixture.Installed[i]
		})
		if got := ValidateFinalState(candidateFixture, candidatePlan); !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d changed result:\ngot  %#v\nwant %#v", iteration, got, want)
		}
	}
}

func TestSchemaValidationRejectsUnknownFieldsVersionsAndTrailingValues(t *testing.T) {
	fixtureJSON := `{"schema":1,"request":{"operation":"install","targets":[]},"installed":[],"available":[]}`
	if _, err := DecodeFixture(strings.NewReader(fixtureJSON)); err != nil {
		t.Fatalf("valid fixture schema rejected: %v", err)
	}
	for name, input := range map[string]string{
		"unknown field":  `{"schema":1,"request":{"operation":"install","targets":[]},"installed":[],"available":[],"surprise":true}`,
		"future version": `{"schema":2,"request":{"operation":"install","targets":[]},"installed":[],"available":[]}`,
		"trailing value": fixtureJSON + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeFixture(strings.NewReader(input)); err == nil {
				t.Fatal("invalid fixture schema accepted")
			}
		})
	}
	if _, err := DecodePlan(strings.NewReader(`{"schema":1,"actions":[]}`)); err != nil {
		t.Fatalf("valid plan schema rejected: %v", err)
	}
	for name, input := range map[string]string{
		"unknown plan field":  `{"schema":1,"actions":[],"surprise":true}`,
		"future plan version": `{"schema":2,"actions":[]}`,
		"trailing plan value": `{"schema":1,"actions":[]} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePlan(strings.NewReader(input)); err == nil {
				t.Fatal("invalid plan schema accepted")
			}
		})
	}
}

func TestPublicAPIContractAndRouteExistence(t *testing.T) {
	var apply func([]Package, Plan) ApplicationResult = ApplyPlan
	var validate func(Fixture, Plan) ValidationResult = ValidateFinalState
	var validateImpact func(Fixture, Plan) ValidationResult = ValidatePlanImpact
	var decodeFixture func(ioReader) (Fixture, error)
	decodeFixture = func(reader ioReader) (Fixture, error) { return DecodeFixture(reader) }
	if apply == nil || validate == nil || validateImpact == nil || decodeFixture == nil {
		t.Fatal("plan validation API route is unavailable")
	}
}

type ioReader interface {
	Read([]byte) (int, error)
}

func TestAdversarialInputIsBoundedAndFailClosed(t *testing.T) {
	var dependency strings.Builder
	for index := 0; index < 2_000; index++ {
		dependency.WriteString("dev-libs/missing ")
	}
	owner := pkg("app-misc/client-1", map[string]string{"RDEPEND": dependency.String(), "DEPEND": "dev-libs/build-only"})
	fixture := Fixture{Schema: 1, Request: Request{Operation: "install", Targets: []string{"app-misc/client"}}, Installed: []Package{owner}}
	result := ValidateFinalState(fixture, Plan{Schema: 1})
	if result.Valid || !hasViolation(result, "missing-dependency-domain") || !hasViolation(result, "unsatisfied-dependency") {
		t.Fatalf("adversarial fixture did not fail closed: %#v", result)
	}
	if len(result.Violations) > MaxViolationRecords || !result.Truncated || result.OmittedViolations == 0 {
		t.Fatalf("diagnostics are not structurally bounded: %#v", result)
	}
}

func hasViolation(result ValidationResult, kind string) bool {
	for _, item := range result.Violations {
		if item.Kind == kind {
			return true
		}
	}
	return false
}

func hasApplicationViolation(result ApplicationResult, kind string) bool {
	for _, item := range result.Violations {
		if item.Kind == kind {
			return true
		}
	}
	return false
}

func cloneJSON[T any](t *testing.T, value T) T {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var cloned T
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}
