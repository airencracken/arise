package configdoctor

import (
	"reflect"
	"testing"

	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/resolve"
)

func TestPackageUseFindsInvalidStaleDuplicateContradictoryAndShadowedRules(t *testing.T) {
	graph := resolve.NewDepGraph()
	graph.AddVersion("app-editors/vim", "9.1", "0", "0", false, nil, "amd64")
	config := &portage.Config{PackageUseRules: []portage.PackageUseRule{
		{Atom: "app-editors/vim", Flags: []string{"python", "-python"}},
		{Atom: "app-editors/vim", Flags: []string{"-python", "python"}},
		{Atom: "app-editors/vim", Flags: []string{"python", "-python"}},
		{Atom: "dev-libs/missing", Flags: []string{"test"}},
		{Atom: "not an atom", Flags: []string{"test"}},
	}}
	original := append([]portage.PackageUseRule(nil), config.PackageUseRules...)
	report := PackageUse(config, graph)
	kinds := map[string]bool{}
	for _, finding := range report.Findings {
		kinds[finding.Kind] = true
	}
	for _, kind := range []string{"invalid-atom", "stale-atom", "duplicate-rule", "contradictory-rule", "shadowed-setting"} {
		if !kinds[kind] {
			t.Fatalf("missing %s finding: %#v", kind, report.Findings)
		}
	}
	if !reflect.DeepEqual(config.PackageUseRules, original) {
		t.Fatal("doctor mutated Portage configuration")
	}
}

func TestPackageUseReportIsDeterministicAndNilSafe(t *testing.T) {
	if report := PackageUse(nil, nil); report.Schema != SchemaVersion || report.Findings == nil {
		t.Fatalf("nil report = %#v", report)
	}
	config := &portage.Config{PackageUseRules: []portage.PackageUseRule{{Atom: "cat/pkg", Flags: []string{"flag", "-flag"}}}}
	first, second := PackageUse(config, nil), PackageUse(config, nil)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("reports differ: %#v then %#v", first, second)
	}
}

func TestPackageUseDistinguishesMissingPackageFromStaleVersionConstraint(t *testing.T) {
	graph := resolve.NewDepGraph()
	graph.AddVersion("app-editors/vim", "9.1", "0", "0", false, nil, "amd64")
	config := &portage.Config{PackageUseRules: []portage.PackageUseRule{
		{Atom: ">=app-editors/vim-10", Flags: []string{"python"}},
		{Atom: "dev-libs/missing", Flags: []string{"test"}},
	}}
	report := PackageUse(config, graph)
	if len(report.Findings) != 2 || report.Findings[0].Kind != "stale-version-atom" || report.Findings[1].Kind != "stale-atom" {
		t.Fatalf("stale findings = %#v", report.Findings)
	}
}

func TestPackageUseReportsUnknownFlagsOnlyForMatchedVersions(t *testing.T) {
	graph := resolve.NewDepGraph()
	version := graph.AddVersion("app-editors/vim", "9.1", "0", "0", false, map[string]bool{"python": false}, "amd64")
	version.IUse = "+python terminal"
	config := &portage.Config{PackageUseRules: []portage.PackageUseRule{
		{Atom: "app-editors/vim", Flags: []string{"python", "terminal", "typo", "typo", "-*"}},
		{Atom: ">=app-editors/vim-10", Flags: []string{"future-flag"}},
	}}
	report := PackageUse(config, graph)
	var unknown []string
	for _, finding := range report.Findings {
		if finding.Kind == "unknown-use-flag" {
			unknown = append(unknown, finding.Flag)
		}
	}
	if !reflect.DeepEqual(unknown, []string{"typo"}) {
		t.Fatalf("unknown flags = %v; findings = %#v", unknown, report.Findings)
	}
	foundDuplicate := false
	for _, finding := range report.Findings {
		foundDuplicate = foundDuplicate || (finding.Kind == "duplicate-flag" && finding.Flag == "typo")
		if finding.Kind == "shadowed-setting" && finding.Rule == finding.Related {
			t.Fatalf("within-rule contradiction was mislabeled as shadowing: %#v", finding)
		}
	}
	if !foundDuplicate {
		t.Fatalf("duplicate flag not reported: %#v", report.Findings)
	}
}

func TestNormalizeFlagHandlesAdversarialEmptyTokens(t *testing.T) {
	for _, raw := range []string{"", " ", "-"} {
		flag, _ := normalizeFlag(raw)
		if flag != "" {
			t.Fatalf("normalizeFlag(%q) = %q", raw, flag)
		}
	}
}

func TestPackagePolicyAuditsFamiliesWithoutCrossFamilyDuplicateNoise(t *testing.T) {
	graph := resolve.NewDepGraph()
	graph.AddVersion("app-editors/vim", "9.1", "0", "0", false, nil, "amd64")
	config := &portage.Config{
		PackageAcceptKeywordRules: []portage.PackageUseRule{{Atom: "app-editors/vim", Flags: []string{"~amd64"}}, {Atom: "app-editors/vim", Flags: []string{"~amd64"}}},
		PackageLicenseRules:       []portage.PackageUseRule{{Atom: ">=app-editors/vim-10", Flags: []string{"GPL-2"}}},
		PackageEnvRules:           []portage.PackageUseRule{{Atom: "dev-libs/missing", Flags: []string{"clang.conf"}}},
		PackageMaskRules:          []portage.PackageMaskRule{{Atom: "not an atom"}},
		PackageUnmask:             []string{"app-editors/vim", "app-editors/vim"},
	}
	report := PackagePolicy(config, graph)
	want := map[string]bool{
		"package.accept_keywords/duplicate-rule": false,
		"package.license/stale-version-atom":     false,
		"package.env/stale-atom":                 false,
		"package.mask/invalid-atom":              false,
		"package.unmask/duplicate-rule":          false,
	}
	for _, finding := range report.Findings {
		key := finding.Family + "/" + finding.Kind
		if _, exists := want[key]; exists {
			want[key] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("missing %s: %#v", key, report.Findings)
		}
	}
}

func TestAllDoctorFindingsAreFamilyQualifiedAndDeterministic(t *testing.T) {
	config := &portage.Config{PackageUseRules: []portage.PackageUseRule{{Atom: "bad atom"}}, PackageUnmask: []string{"also bad"}}
	first, second := All(config, nil, nil), All(config, nil, nil)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("all reports differ: %#v then %#v", first, second)
	}
	for _, finding := range first.Findings {
		if finding.Family == "" {
			t.Fatalf("unqualified finding: %#v", finding)
		}
	}
}

func TestWorldTargetsReportsInvalidDuplicateAndObsoleteSelections(t *testing.T) {
	graph := resolve.NewDepGraph()
	graph.AddVersion("app-editors/vim", "9.1", "0", "0", true, nil, "amd64")
	report := WorldTargets([]string{"app-editors/vim", "app-editors/vim", ">=app-editors/vim-10", "dev-libs/missing", "bad atom", "@system"}, graph)
	want := map[string]bool{"duplicate-target": false, "obsolete-version-target": false, "obsolete-target": false, "invalid-atom": false}
	for _, finding := range report.Findings {
		if _, exists := want[finding.Kind]; exists {
			want[finding.Kind] = true
		}
	}
	for kind, found := range want {
		if !found {
			t.Errorf("missing %s: %#v", kind, report.Findings)
		}
	}
	for _, finding := range report.Findings {
		if finding.Atom == "@system" {
			t.Fatalf("package set was treated as atom: %#v", finding)
		}
	}
}
