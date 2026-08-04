package support

import (
	"encoding/json"
	"os"
	"testing"
)

func TestHardenedStage3GCCObservationPreservesKnownDataAndUnknowns(t *testing.T) {
	data, err := os.ReadFile("../docs/evidence/HARDENED_STAGE3_WORLD_UPDATE_2026-08-04.json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Schema      int `json:"schema"`
		Environment struct {
			CPU      any `json:"cpu"`
			Makeopts any `json:"makeopts"`
		} `json:"environment"`
		Transaction struct {
			Planned  int    `json:"planned_actions"`
			Position int    `json:"action_position"`
			Outcome  string `json:"outcome"`
		} `json:"transaction"`
		FinalState struct {
			PortageActions int `json:"portage_actions"`
			AriseActions   int `json:"arise_actions"`
			AriseConflicts int `json:"arise_conflicts"`
			Backtrack      int `json:"arise_backtrack_used"`
		} `json:"final_state"`
		Package struct {
			CPV      string `json:"cpv"`
			Duration int    `json:"duration_seconds"`
			Outcome  string `json:"outcome"`
		} `json:"package"`
		Limitations []string `json:"limitations"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.Schema != 1 || document.Package.CPV != "sys-devel/gcc-15.3.0" || document.Package.Duration != 8312 || document.Package.Outcome != "committed" {
		t.Fatalf("GCC observation = %#v", document)
	}
	if document.Transaction.Planned != 65 || document.Transaction.Position != 58 || document.Transaction.Outcome != "committed" {
		t.Fatalf("transaction position = %#v", document.Transaction)
	}
	if document.FinalState.PortageActions != 0 || document.FinalState.AriseActions != 0 || document.FinalState.AriseConflicts != 0 || document.FinalState.Backtrack != 0 {
		t.Fatalf("final state = %#v", document.FinalState)
	}
	if document.Environment.CPU != nil || document.Environment.Makeopts != nil {
		t.Fatalf("unknown provenance was invented: %#v", document.Environment)
	}
	if len(document.Limitations) < 2 {
		t.Fatalf("limitations = %v", document.Limitations)
	}
}
