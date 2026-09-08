package planvalidate

import (
	"reflect"
	"testing"
)

func TestFrozenLicensePolicyNestedGroupsAndExclusions(t *testing.T) {
	groups := map[string][]string{
		"FREE": {"@FSF", "@OSI"}, "FSF": {"GPL-2", "MIT"}, "OSI": {"MIT", "BSD"},
		"CYCLE": {"@LOOP"}, "LOOP": {"@CYCLE"},
		"EMPTY": {},
	}
	for _, tc := range []struct {
		name          string
		global, local []string
		license       string
		valid         bool
	}{
		{"stage3 default", []string{"@FREE"}, nil, "GPL-2 MIT", true},
		{"nested alternative", []string{"@FREE"}, nil, "|| ( EULA BSD )", true},
		{"unaccepted conjunction", []string{"@FREE"}, nil, "MIT EULA", false},
		{"wildcard exclusion", []string{"*"}, []string{"-MIT"}, "MIT", false},
		{"nested exclusion", []string{"*"}, []string{"-@FREE"}, "GPL-2", false},
		{"reset alone", []string{"*"}, []string{"-*"}, "MIT", false},
		{"reset then allow", []string{"*"}, []string{"-*", "MIT"}, "MIT", true},
		{"allow then reset", []string{"MIT"}, []string{"-*"}, "MIT", false},
		{"later reaccept", []string{"*", "-@FREE"}, []string{"MIT"}, "MIT", true},
		{"cycle terminates", []string{"@CYCLE"}, nil, "MIT", false},
		{"unknown group", []string{"@MISSING"}, nil, "MIT", false},
		{"empty group", []string{"@EMPTY"}, nil, "MIT", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := pkg("app-misc/client-1", nil)
			candidate.Authority = AuthorityMD5Cache
			candidate.License = tc.license
			candidate.Policy.LicenseChanges = tc.local
			fixture := Fixture{Schema: SchemaVersion, Request: Request{Operation: "install", Targets: []string{"app-misc/client"}}, Available: []Package{candidate}, Policy: Policy{AcceptedLicenses: tc.global, LicenseGroups: groups}}
			plan := Plan{Schema: SchemaVersion, Actions: []Action{{Kind: ActionInstall, Package: candidate}}}
			before := cloneJSON(t, fixture)
			result := ValidatePlanImpact(fixture, plan)
			if result.Valid != tc.valid {
				t.Fatalf("valid=%t, want %t: %#v", result.Valid, tc.valid, result.Violations)
			}
			// Validation must not rewrite the frozen inputs used for later replay.
			if !reflect.DeepEqual(before, fixture) {
				t.Fatal("validation mutated frozen input")
			}
		})
	}
}
