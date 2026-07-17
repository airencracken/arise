package perf

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunRequiresEquivalentOutput(t *testing.T) {
	w := Workload{Name: "test", Runs: 2, Cases: []Case{{
		Name: "same", Normalize: "sorted-lines", MinSpeedup: speedupPtr(0),
		Arise:     Command{Tool: "arise", Path: "printf", Args: []string{"b\\na\\n"}},
		Reference: Command{Tool: "reference", Path: "printf", Args: []string{"a\\nb\\n"}},
	}}}
	r, err := Run(context.Background(), w, "fixture-1")
	if err != nil {
		t.Fatal(err)
	}
	if !r.AllEquivalent || !r.Results[0].Equivalent {
		t.Fatal("sorted equivalent output rejected")
	}
	if r.Results[0].AriseMedianNS <= 0 || len(r.Results[0].Arise) != 2 {
		t.Fatal("timing samples missing")
	}
}

func TestRunDetectsMismatch(t *testing.T) {
	w := Workload{Name: "test", Runs: 1, Cases: []Case{{
		Name: "different", Normalize: "exact", MinSpeedup: speedupPtr(0),
		Arise:     Command{Path: "printf", Args: []string{"a"}},
		Reference: Command{Path: "printf", Args: []string{"b"}},
	}}}
	r, err := Run(context.Background(), w, "fixture-1")
	if err != nil {
		t.Fatal(err)
	}
	if r.AllEquivalent || r.Results[0].Equivalent {
		t.Fatal("mismatch accepted")
	}
}

func TestRunUsesPostBuildValidation(t *testing.T) {
	aValidate := Command{Path: "printf", Args: []string{"app-editors/vim\n"}}
	rValidate := Command{Path: "printf", Args: []string{"app-editors/vim\n"}}
	w := Workload{Name: "index", Runs: 1, Cases: []Case{{
		Name: "build", Normalize: "sorted-lines", MinSpeedup: speedupPtr(0),
		Arise: Command{Path: "printf", Args: []string{"arise build log"}}, Reference: Command{Path: "printf", Args: []string{"eix build log"}},
		AriseValidate: &aValidate, ReferenceValidate: &rValidate,
	}}}
	r, err := Run(context.Background(), w, "fixture-1")
	if err != nil {
		t.Fatal(err)
	}
	if !r.AllEquivalent {
		t.Fatal("equivalent post-build indexes rejected because build logs differ")
	}
}

func TestPackageNamesNormalization(t *testing.T) {
	w := Workload{Name: "test", Runs: 1, Cases: []Case{{
		Name: "package names", Normalize: "package-names", MinSpeedup: speedupPtr(0),
		Arise:     Command{Path: "printf", Args: []string{"firefox\\nfirefox-bin\\n"}},
		Reference: Command{Path: "printf", Args: []string{"www-client/firefox-bin\\nwww-client/firefox\\n"}},
	}}}
	r, err := Run(context.Background(), w, "fixture-1")
	if err != nil {
		t.Fatal(err)
	}
	if !r.AllEquivalent {
		t.Fatal("package-name normalization rejected equivalent results")
	}
}

func TestEmergeSearchNormalization(t *testing.T) {
	w := Workload{Name: "test", Runs: 1, Cases: []Case{{
		Name: "emerge search", Normalize: "search-package-names", MinSpeedup: speedupPtr(0),
		Arise: Command{Path: "printf", Args: []string{"firefox\\nfirefox-bin\\n"}},
		Reference: Command{Path: "printf", Args: []string{
			"[ Results for search key : firefox ]\\nSearching...\\n*  www-client/firefox\\n*  www-client/firefox-bin [ Masked ]\\n[ Applications found : 2 ]\\n",
		}},
	}}}
	r, err := Run(context.Background(), w, "fixture-1")
	if err != nil {
		t.Fatal(err)
	}
	if !r.AllEquivalent {
		t.Fatal("emerge search normalization rejected equivalent results")
	}
}

func TestPackagePlanNormalization(t *testing.T) {
	arise := `{"schema":1,"actions":[{"action":"install","cpv":"net-im/signal-desktop-bin-1","slot":"0","repository":"gentoo","merge_type":"source"}]}`
	emerge := `[ebuild  N     ] net-im/signal-desktop-bin-1:0::gentoo`
	if got, want := string(normalize([]byte(arise), "package-plan")), string(normalize([]byte(emerge), "package-plan")); got != want {
		t.Fatalf("plan normalization differs:\nArise: %s\nemerge: %s", got, want)
	}
}

func TestRunUsesSemanticPackagePlanEquivalence(t *testing.T) {
	arise := `{"schema":1,"actions":[{"action":"install","cpv":"net-im/signal-desktop-bin-1","slot":"0","repository":"gentoo","merge_type":"source","use_enabled":["extra","ssl"]}]}`
	emerge := `[ebuild  N     ] net-im/signal-desktop-bin-1:0::gentoo USE="ssl"`
	w := Workload{Name: "resolver", Runs: 1, Cases: []Case{{
		Name: "plan", Normalize: "package-plan", MinSpeedup: speedupPtr(0),
		Arise: Command{Path: "printf", Args: []string{arise}}, Reference: Command{Path: "printf", Args: []string{emerge}},
	}}}
	report, err := Run(context.Background(), w, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if !report.AllEquivalent {
		t.Fatal("semantic plan equivalence rejected Arise-only non-Portage output flags")
	}
}

func TestRunRejectsEquivalentButSlower(t *testing.T) {
	w := Workload{Name: "test", Runs: 2, Cases: []Case{{
		Name: "slow", Normalize: "exit-code",
		Arise:     Command{Path: "sleep", Args: []string{"0.02"}},
		Reference: Command{Path: "true"},
	}}}
	r, err := Run(context.Background(), w, "fixture-1")
	if err != nil {
		t.Fatal(err)
	}
	if !r.AllEquivalent {
		t.Fatal("commands should be behaviorally equivalent")
	}
	if r.AllPassed || r.Results[0].PerformancePass {
		t.Fatal("equivalent but slower result passed")
	}
}

func TestRunReportsButDoesNotEnforceAspirationalTarget(t *testing.T) {
	w := Workload{Name: "test", Runs: 1, Cases: []Case{{
		Name: "native target", Normalize: "exit-code", ReportOnly: true,
		Arise: Command{Path: "sleep", Args: []string{"0.02"}}, Reference: Command{Path: "true"},
	}}}
	r, err := Run(context.Background(), w, "fixture-1")
	if err != nil {
		t.Fatal(err)
	}
	if !r.AllPassed || r.Results[0].PerformancePass || r.Results[0].PerformanceEnforced {
		t.Fatal("aspirational performance target affected workload status")
	}
}

func speedupPtr(value float64) *float64 { return &value }

func TestLoadWorkloadValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workload.json")
	if err := os.WriteFile(path, []byte(`{"name":"x","runs":1,"cases":[{"name":"x","normalize":"bad","arise":{"path":"a"},"reference":{"path":"b"}}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWorkload(path); err == nil {
		t.Fatal("invalid normalizer accepted")
	}
}
