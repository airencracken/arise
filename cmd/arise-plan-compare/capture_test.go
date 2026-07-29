package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/airencracken/arise/internal/plancompare"
	"github.com/airencracken/arise/internal/planvalidate"
)

func TestWriteComparisonCaptureIsDeterministicAndAtomic(t *testing.T) {
	fixture := planvalidate.Fixture{
		Schema:    planvalidate.SchemaVersion,
		Request:   planvalidate.Request{Operation: "install", Targets: []string{}},
		Installed: []planvalidate.Package{}, Available: []planvalidate.Package{},
	}
	plan := planvalidate.Plan{
		Schema: planvalidate.SchemaVersion, Actions: []planvalidate.Action{},
		Decisions: planvalidate.DecisionLedger{Records: []planvalidate.DecisionRecord{}},
	}
	document := plancompare.CaptureDocument(fixture, plan)
	comparison := plancompare.ClassifiedComparison{
		Class: plancompare.ClassEquivalentValid, Equivalent: true,
		Differences: []plancompare.StateDifference{}, ActionDiagnostics: []plancompare.Difference{},
	}
	parent := t.TempDir()
	first := filepath.Join(parent, "first")
	second := filepath.Join(parent, "second")
	for _, directory := range []string{first, second} {
		if err := writeComparisonCapture(directory, "cat/pkg", "update", comparison, document, document, plancompare.ClassificationPolicy{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"arise-state.json", "portage-state.json", "classification-policy.json", "capture.json"} {
		left, err := os.ReadFile(filepath.Join(first, name))
		if err != nil {
			t.Fatal(err)
		}
		right, err := os.ReadFile(filepath.Join(second, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(left, right) {
			t.Fatalf("%s differs across deterministic captures", name)
		}
	}
	before, err := os.ReadFile(filepath.Join(first, "capture.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeComparisonCapture(first, "other", "install", comparison, document, document, plancompare.ClassificationPolicy{}); err == nil {
		t.Fatal("existing capture was overwritten")
	}
	after, err := os.ReadFile(filepath.Join(first, "capture.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed capture changed committed output")
	}
}
