package support

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestDiagnosticIntelligencePlanPreservesSafetyBoundary(t *testing.T) {
	data, err := os.ReadFile("../docs/planning/DIAGNOSTIC_INTELLIGENCE_PLAN.md")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(string(data)), " ")
	for _, required := range []string{
		"read-only evidence", "never permission to mutate policy", "mode `0600`",
		"refuses overwrites", "strictly decoded", "stable identity", "separately reviewed atomic write plan",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("diagnostic plan lost contract %q", required)
		}
	}
}

func TestDiagnosticSchemasAreValidJSONAndBugReportReferencesTrace(t *testing.T) {
	for _, path := range []string{"../docs/schemas/bug-report-v1.schema.json", "../docs/schemas/resolver-trace-v1.schema.json"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if document["additionalProperties"] != false {
			t.Errorf("%s is not default-deny", path)
		}
	}
	bugSchema, err := os.ReadFile("../docs/schemas/bug-report-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bugSchema), `"resolver_trace": {"$ref": "resolver-trace-v1.schema.json"}`) {
		t.Fatal("bug-report schema lost resolver-trace reference")
	}
}

func TestInstalledManualsDocumentDiagnosticCommands(t *testing.T) {
	for _, path := range []string{"../arise.1", "../arise.texi"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Join(strings.Fields(string(data)), " ")
		for _, required := range []string{"save-resolver-trace", "plan-diff", "doctor package-use", "strictly decoded", "read-only"} {
			if !strings.Contains(strings.ToLower(text), required) {
				t.Errorf("%s lost diagnostic command contract %q", path, required)
			}
		}
	}
}
