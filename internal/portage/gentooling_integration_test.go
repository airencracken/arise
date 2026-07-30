package portage

import (
	"testing"

	"github.com/airencracken/gentooling"
)

func TestGentoolingAtomConsumerContract(t *testing.T) {
	tests := []struct {
		rule, cpv, slot, repository string
		want                        bool
	}{
		{"dev-libs/example", "dev-libs/example-1", "0", "gentoo", true},
		{">=dev-libs/example-2", "dev-libs/example-2-r1", "0", "gentoo", true},
		{"<dev-libs/example-2", "dev-libs/example-2", "0", "gentoo", false},
		{"~dev-libs/example-2", "dev-libs/example-2-r7", "0", "gentoo", true},
		{"=dev-libs/example-2*", "dev-libs/example-2.3", "0", "gentoo", true},
		{"dev-libs/example:1/2::overlay", "dev-libs/example-4", "1/2", "overlay", true},
		{"dev-libs/example:1/2::overlay", "dev-libs/example-4", "1/3", "overlay", false},
	}
	for _, test := range tests {
		if got := PackageAtomMatches(test.rule, test.cpv, test.slot, test.repository); got != test.want {
			t.Errorf("PackageAtomMatches(%q, %q, %q, %q) = %v, want %v",
				test.rule, test.cpv, test.slot, test.repository, got, test.want)
		}
	}

	configuration := gentooling.EffectiveConfig{
		UserPackageUse: []gentooling.PackageFlagRule{{
			Atom: "dev-libs/example", Flags: []string{"feature"},
			Source: gentooling.PolicySource{Path: "package.use", Line: 1},
		}},
	}
	evaluation, err := configuration.EvaluateUse(t.Context(), gentooling.PackageContext{
		ID:          gentooling.PackageID{Category: "dev-libs", Name: "example", Version: "1"},
		DeclaredUse: []gentooling.UseDeclaration{{Name: "feature"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, found := evaluation.Decision("feature")
	if !found || !decision.Enabled || len(decision.Evidence) != 1 {
		t.Fatalf("Gentooling USE consumer contract = %+v, found %v", decision, found)
	}
}
