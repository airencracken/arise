package support

import (
	"os"
	"strings"
	"testing"
)

func TestPackageOutputUXPlanTracksConciseFetchAndLifecycleVocabulary(t *testing.T) {
	data, err := os.ReadFile("../docs/planning/PACKAGE_OUTPUT_UX_PLAN.md")
	if err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(strings.Fields(string(data)), " ")
	for _, required := range []string{
		"suppressed in default execution output",
		"retained by `--verbose`",
		"automatic redirected output and JSON remain uncolored",
		"Installing into staging area",
		"Validating package contents",
		"bounded by package rather than distfile count",
	} {
		if !strings.Contains(plan, required) {
			t.Errorf("package output UX plan lost contract %q", required)
		}
	}
	for _, stale := range []string{"Installing into image", "Validating package image"} {
		if strings.Contains(plan, stale) {
			t.Errorf("package output UX plan retains implementation-facing label %q", stale)
		}
	}
}

func TestInstalledManualsDocumentAutomaticColorPolicy(t *testing.T) {
	for _, path := range []string{"../arise.1", "../arise.texi"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		manual := strings.Join(strings.Fields(string(data)), " ")
		for _, required := range []string{"auto|y|n", "NO_COLOR", "NOCOLOR", "TERM=dumb", "JSON remains uncolored"} {
			if !strings.Contains(manual, required) {
				t.Errorf("%s does not document color contract %q", path, required)
			}
		}
	}
}
