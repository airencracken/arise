package portage

import (
	"testing"

	"github.com/airencracken/gentooling"
)

func TestGentoolingVisibilityMatchesAriseKeywordPolicyContract(t *testing.T) {
	config := &Config{
		ACCEPT_KEYWORDS: []string{"amd64"},
		PackageAcceptKeywordRules: []PackageUseRule{
			{Atom: "=dev-lang/go-1.24", Flags: nil},
			{Atom: "=dev-lang/go-1.25", Flags: []string{"~amd64", "-amd64"}},
		},
	}
	tests := []struct {
		cpv, keywords string
		want          bool
	}{
		{"dev-lang/go-1.23", "amd64 ~arm64", true},
		{"dev-lang/go-1.24", "~amd64", true},
		{"dev-lang/go-1.25", "~amd64", true},
		{"dev-lang/go-1.26", "~amd64", false},
		{"dev-lang/go-1.26", "arm64", false},
	}
	for _, test := range tests {
		if got := config.KeywordAcceptedFor(test.cpv, "0", "gentoo", test.keywords, "amd64"); got != test.want {
			t.Errorf("KeywordAcceptedFor(%q, %q) = %v, want %v", test.cpv, test.keywords, got, test.want)
		}
	}

	result, err := (gentooling.EffectiveConfig{
		Variables: map[string]string{"ARCH": "amd64"},
		PackageMasks: []gentooling.PackageMaskRule{{
			Atom:   "=dev-lang/go-1.24",
			Source: gentooling.PolicySource{Path: "/repo/profiles/package.mask", Line: 7},
			Reason: "Regression.",
		}},
	}).EvaluateVisibility(t.Context(), gentooling.PackageVisibilityContext{
		ID:       gentooling.PackageID{Category: "dev-lang", Name: "go", Version: "1.24"},
		Keywords: []string{"amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Visible || result.Status != gentooling.VisibilityPackageMasked ||
		result.Evidence[0].Source.Line != 7 {
		t.Fatalf("Gentooling mask consumer result = %+v", result)
	}
}
