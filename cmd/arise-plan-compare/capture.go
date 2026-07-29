package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/airencracken/arise/internal/plancompare"
	"github.com/airencracken/arise/internal/planvalidate"
)

type captureManifest struct {
	Schema            int      `json:"schema"`
	Target            string   `json:"target"`
	Operation         string   `json:"operation"`
	ComparisonClass   string   `json:"comparison_class"`
	Equivalent        bool     `json:"equivalent"`
	Files             []string `json:"files"`
	StateDifferences  int      `json:"state_differences"`
	ActionDiagnostics int      `json:"action_diagnostics"`
	SemanticFeatures  []string `json:"semantic_features"`
}

func writeComparisonCapture(directory, target, operation string, comparison plancompare.ClassifiedComparison, arise, portage plancompare.StateDocument, policy plancompare.ClassificationPolicy) error {
	if _, err := os.Stat(directory); err == nil {
		return fmt.Errorf("comparison capture directory already exists: %s", directory)
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(directory)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create comparison capture parent: %w", err)
	}
	ariseBytes, err := plancompare.EncodeStateDocument(arise)
	if err != nil {
		return err
	}
	portageBytes, err := plancompare.EncodeStateDocument(portage)
	if err != nil {
		return err
	}
	policyBytes, err := encodeIndented(plancompare.PolicyDocument{
		Schema: plancompare.StateSchemaVersion, Policy: policy,
	})
	if err != nil {
		return err
	}
	files := []string{"arise-state.json", "portage-state.json", "classification-policy.json"}
	manifestBytes, err := encodeIndented(captureManifest{
		Schema: 1, Target: target, Operation: operation,
		ComparisonClass: comparison.Class, Equivalent: comparison.Equivalent,
		Files: files, StateDifferences: len(comparison.Differences),
		ActionDiagnostics: len(comparison.ActionDiagnostics),
		SemanticFeatures:  combinedSemanticFeatures(arise, portage),
	})
	if err != nil {
		return err
	}
	content := map[string][]byte{
		files[0]: ariseBytes, files[1]: portageBytes, files[2]: policyBytes,
		"capture.json": manifestBytes,
	}
	staging, err := os.MkdirTemp(parent, ".capture-*")
	if err != nil {
		return fmt.Errorf("create comparison capture staging directory: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(staging)
		}
	}()
	for _, name := range append(files, "capture.json") {
		if err := os.WriteFile(filepath.Join(staging, name), content[name], 0o644); err != nil {
			return fmt.Errorf("stage comparison capture %s: %w", name, err)
		}
	}
	if err := os.Rename(staging, directory); err != nil {
		return fmt.Errorf("commit comparison capture: %w", err)
	}
	committed = true
	return nil
}

func combinedSemanticFeatures(documents ...plancompare.StateDocument) []string {
	seen := make(map[string]bool)
	for _, document := range documents {
		for _, feature := range planvalidate.SemanticFeatures(document.Fixture, document.Plan) {
			seen[feature] = true
		}
	}
	result := make([]string, 0, len(seen))
	for feature := range seen {
		result = append(result, feature)
	}
	sort.Strings(result)
	return result
}

func encodeIndented(value any) ([]byte, error) {
	return json.MarshalIndent(value, "", "  ")
}
