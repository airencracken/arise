package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCompareSavedPlansTreatsVersionAndUseAsChangedAction(t *testing.T) {
	directory := t.TempDir()
	before := filepath.Join(directory, "before.json")
	after := filepath.Join(directory, "after.json")
	write := func(path, value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(before, `{"schema":1,"operation":"install","actions":[{"action":"install","cpv":"cat/pkg-1","slot":"0","repository":"gentoo","domain":"ROOT","use_enabled":["old"]}],"targets":[],"options":{},"complete":true,"resolution":{"candidate_decisions":{"records":[]}},"conflicts":[],"warnings":[]}`)
	write(after, `{"schema":1,"operation":"install","actions":[{"action":"update","cpv":"cat/pkg-2","slot":"0","repository":"gentoo","domain":"ROOT","use_enabled":["new"]}],"targets":[],"options":{},"complete":true,"resolution":{"candidate_decisions":{"records":[]}},"conflicts":[],"warnings":[]}`)
	diff, err := compareSavedPlanFiles(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Changes) != 1 || diff.Changes[0].Kind != "changed" {
		t.Fatalf("plan diff = %#v", diff)
	}
}

func TestReadSavedPlanRejectsUnknownSchemaFieldsAndTrailingValues(t *testing.T) {
	for name, value := range map[string]string{
		"unknown":    `{"schema":1,"surprise":true}`,
		"schema":     `{"schema":2}`,
		"trailing":   `{"schema":1} {}`,
		"incomplete": `{"schema":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "plan.json")
			if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readSavedPlan(path); err == nil {
				t.Fatal("invalid saved plan accepted")
			}
		})
	}
}

func TestCompareSavedPlansRejectsMalformedActionIdentity(t *testing.T) {
	directory := t.TempDir()
	valid := `{"schema":1,"operation":"install","targets":[],"actions":[],"options":{},"complete":true,"resolution":{"candidate_decisions":{"records":[]}},"conflicts":[],"warnings":[]}`
	invalid := `{"schema":1,"operation":"install","targets":[],"actions":[{"action":"install","cpv":"not-a-cpv","domain":"ROOT"}],"options":{},"complete":true,"resolution":{"candidate_decisions":{"records":[]}},"conflicts":[],"warnings":[]}`
	before, after := filepath.Join(directory, "before.json"), filepath.Join(directory, "after.json")
	if err := os.WriteFile(before, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(after, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compareSavedPlanFiles(before, after); err == nil {
		t.Fatal("malformed action CPV accepted")
	}
}

func TestCompareSavedPlansReportsStateAndRequestContextChanges(t *testing.T) {
	directory := t.TempDir()
	before, after := filepath.Join(directory, "before.json"), filepath.Join(directory, "after.json")
	left := `{"schema":1,"operation":"install","targets":["cat/pkg"],"actions":[],"options":{},"complete":true,"resolution":{"candidate_decisions":{"records":[]}},"conflicts":[],"warnings":[],"state_sha256":"aaa"}`
	right := `{"schema":1,"operation":"update","targets":["@world"],"actions":[],"options":{},"complete":false,"resolution":{"candidate_decisions":{"records":[]}},"conflicts":[],"warnings":[],"state_sha256":"bbb"}`
	if err := os.WriteFile(before, []byte(left), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(after, []byte(right), 0o600); err != nil {
		t.Fatal(err)
	}
	diff, err := compareSavedPlanFiles(before, after)
	if err != nil {
		t.Fatal(err)
	}
	var fields []string
	for _, change := range diff.Context {
		fields = append(fields, change.Field)
	}
	want := []string{"operation", "targets", "complete", "state_sha256"}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("context fields = %v, want %v", fields, want)
	}
	if len(diff.Changes) != 0 {
		t.Fatalf("unexpected action changes: %#v", diff.Changes)
	}
}
