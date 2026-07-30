package support

import (
	"encoding/json"
	"os"
	"testing"
)

func TestMaintainMoveInstPublishedSchemaRequiresCommandContract(t *testing.T) {
	data, err := os.ReadFile("../docs/schemas/maintain-moveinst-plan-v1.schema.json")
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
	required := map[string]bool{}
	for _, name := range schema.Required {
		required[name] = true
	}
	for _, name := range []string{"schema", "operation", "complete", "state_sha256", "plan_sha256", "vdb", "issues", "actions"} {
		if !required[name] {
			t.Errorf("schema does not require %q", name)
		}
		if _, exists := schema.Properties[name]; !exists {
			t.Errorf("schema does not define %q", name)
		}
	}
}
