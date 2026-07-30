package support

import (
	"encoding/json"
	"os"
	"testing"
)

func TestPerlCleanerPublishedSchemaRequiresCommandContract(t *testing.T) {
	data, err := os.ReadFile("../docs/schemas/perl-cleaner-report-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	required := make(map[string]bool)
	for _, name := range schema.Required {
		required[name] = true
	}
	for _, name := range []string{"schema", "operation", "complete", "report", "targets", "leftovers"} {
		if !required[name] {
			t.Errorf("schema does not require %q", name)
		}
		if _, exists := schema.Properties[name]; !exists {
			t.Errorf("schema does not define %q", name)
		}
	}
}

func TestPerlCleanerResumeSchemaRequiresIntegrityBoundContext(t *testing.T) {
	data, err := os.ReadFile("../docs/schemas/perl-cleaner-resume-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	required := make(map[string]bool)
	for _, name := range schema.Required {
		required[name] = true
	}
	for _, name := range []string{
		"schema", "operation", "stage", "plan_sha256", "mode",
		"delete_leftovers", "abi", "targets",
	} {
		if !required[name] {
			t.Errorf("resume schema does not require %q", name)
		}
		if _, exists := schema.Properties[name]; !exists {
			t.Errorf("resume schema does not define %q", name)
		}
	}
}
