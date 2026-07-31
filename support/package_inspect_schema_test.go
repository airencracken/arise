package support

import (
	"encoding/json"
	"os"
	"testing"
)

func TestPackageInspectSchemaContract(t *testing.T) {
	data, err := os.ReadFile("../docs/schemas/package-inspect-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("draft = %v", schema["$schema"])
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties is not an object")
	}
	identity, ok := properties["schema"].(map[string]any)
	if !ok || identity["const"] != "arise.package-inspect.v1" {
		t.Fatalf("schema identity = %#v", properties["schema"])
	}
	required, ok := schema["required"].([]any)
	if !ok || len(required) != 8 {
		t.Fatalf("required fields = %#v", schema["required"])
	}
}
