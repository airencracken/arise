package support

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/airencracken/arise/internal/bugreport"
)

func TestBugReportJSONMatchesPublishedSchemaSurface(t *testing.T) {
	schemaData, err := os.ReadFile("../docs/schemas/bug-report-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	reportData, err := bugreport.JSON(bugreport.Collect(bugreport.Options{
		Version: "test", Package: "cat/pkg", PlanSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(reportData, &document); err != nil {
		t.Fatal(err)
	}
	for name := range document {
		if _, exists := schema.Properties[name]; !exists {
			t.Errorf("generated property %q is absent from schema", name)
		}
	}
	for _, name := range schema.Required {
		if _, exists := document[name]; !exists {
			t.Errorf("required property %q is absent from generated report", name)
		}
	}
}
