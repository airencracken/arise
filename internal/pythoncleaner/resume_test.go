package pythoncleaner

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func pythonResumeFixture() ResumeState {
	return NewResumeState(
		Policy{Targets: []string{"python3_14"}, SingleTarget: "python3_14", Preference: []string{"python3_13"}},
		ResumeStageCohort,
		[]string{"=dev-python/six-1.17.0", "dev-python/ecdsa:0"},
		[][]string{{"dev-python/appdirs:0"}},
	)
}

func TestPythonResumeRoundTripAdvanceAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "python-cleaner.json")
	state := pythonResumeFixture()
	if err := SaveResume(path, state); err != nil {
		t.Fatal(err)
	}
	got, err := LoadResume(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, state) {
		t.Fatalf("state = %#v, want %#v", got, state)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("resume mode = %o", info.Mode().Perm())
	}
	advanced := AdvanceResume(got, ResumeStageValidate, nil)
	if len(advanced.CompletedCohorts) != 2 ||
		!reflect.DeepEqual(advanced.CompletedCohorts[1], state.CurrentTargets) ||
		advanced.PlanSHA256 == state.PlanSHA256 {
		t.Fatalf("advanced = %#v", advanced)
	}
}

func TestPythonResumeRejectsTamperingUnknownFieldsTrailingJSONAndInvalidStages(t *testing.T) {
	state := pythonResumeFixture()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	cases := []string{
		strings.Replace(string(data), `"dev-python/ecdsa:0"`, `"dev-python/evil:0"`, 1),
		strings.TrimSuffix(string(data), "}") + `,"unknown":true}`,
		string(data) + `{}`,
		`{"schema":1}`,
	}
	for _, input := range cases {
		path := filepath.Join(t.TempDir(), "resume")
		if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadResume(path); err == nil {
			t.Fatalf("invalid state accepted: %.40q", input)
		}
	}
	for _, invalid := range []ResumeState{
		NewResumeState(state.Policy, ResumeStageCohort, nil, nil),
		NewResumeState(state.Policy, "unknown", nil, nil),
		NewResumeState(state.Policy, ResumeStageValidate, []string{"dev-python/a"}, nil),
		NewResumeState(Policy{}, ResumeStageComplete, nil, nil),
	} {
		if err := validatePythonResume(invalid); err == nil {
			t.Fatalf("invalid state accepted: %#v", invalid)
		}
	}
}

func TestSavePythonResumeAtomicFailurePreservesPreviousState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume")
	if err := os.WriteFile(path, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := renamePythonResume
	t.Cleanup(func() { renamePythonResume = original })
	renamePythonResume = func(string, string) error { return errors.New("injected rename failure") }
	if err := SaveResume(path, pythonResumeFixture()); err == nil {
		t.Fatal("rename failure ignored")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "previous" {
		t.Fatalf("previous state changed: %q %v", got, err)
	}
}

func TestRemovePythonResumeIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume")
	if err := os.WriteFile(path, []byte("state"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveResume(path); err != nil {
		t.Fatal(err)
	}
	if err := RemoveResume(path); err != nil {
		t.Fatal(err)
	}
}
