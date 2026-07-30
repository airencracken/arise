package perlcleaner

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/resolve"
)

func resumeFixture() ResumeState {
	report := Report{
		Mode: AllMode(),
		ABI: ABI{
			Version: "5.42", Arch: "x86_64-linux", SourceCPV: "dev-lang/perl-5.42.2",
			LibPerlSONames: []string{"libperl.so.5.42"},
		},
	}
	return NewResumeState(report, []string{"dev-perl/Foo:0", "virtual/perl-Carp"}, true)
}

func TestResumeRoundTripAndModeContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "perl-cleaner-resume.json")
	state := resumeFixture()
	if err := SaveResume(path, state); err != nil {
		t.Fatal(err)
	}
	got, err := LoadResume(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.PlanSHA256 != state.PlanSHA256 || got.Mode != AllMode() || !got.DeleteLeftovers ||
		len(got.Targets) != 2 {
		t.Fatalf("resume state = %#v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("resume mode = %o", info.Mode().Perm())
	}
}

func TestResumeRejectsTamperingUnknownFieldsAndTrailingJSON(t *testing.T) {
	state := resumeFixture()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	cases := []string{
		strings.Replace(string(data), `"dev-perl/Foo:0"`, `"dev-perl/Evil:0"`, 1),
		strings.TrimSuffix(string(data), "}") + `,"unknown":true}`,
		string(data) + `{}`,
		`{"schema":1}`,
	}
	for _, input := range cases {
		t.Run(input[:min(20, len(input))], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "resume")
			if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadResume(path); err == nil {
				t.Fatal("invalid resume state accepted")
			}
		})
	}
}

func TestSaveResumeAtomicFailurePreservesPreviousState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume")
	old := []byte("previous")
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	originalRename := renameResume
	t.Cleanup(func() { renameResume = originalRename })
	renameResume = func(string, string) error { return errors.New("injected rename failure") }
	if err := SaveResume(path, resumeFixture()); err == nil {
		t.Fatal("rename failure ignored")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(old) {
		t.Fatalf("previous state changed: %q %v", got, err)
	}
}

func TestRemoveResumeIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume")
	if err := os.WriteFile(path, []byte("state"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveResume(path); err != nil {
		t.Fatal(err)
	}
	if err := RemoveResume(path); err != nil {
		t.Fatalf("second remove: %v", err)
	}
}

func TestCleanerContextAndExecutorProgressResumeTogether(t *testing.T) {
	root := t.TempDir()
	contextPath := filepath.Join(root, "resume.perl-cleaner")
	progressPath := filepath.Join(root, "resume")
	state := resumeFixture()
	if err := SaveResume(contextPath, state); err != nil {
		t.Fatal(err)
	}
	first, err := atom.Parse("dev-perl/Foo:0")
	if err != nil {
		t.Fatal(err)
	}
	second, err := atom.Parse("virtual/perl-Carp")
	if err != nil {
		t.Fatal(err)
	}
	result := &resolve.ResolveResult{Install: []resolve.PkgAction{{Atom: first}, {Atom: second}}}
	if err := resolve.SaveResume(progressPath, result); err != nil {
		t.Fatal(err)
	}
	if err := resolve.MarkResumeComplete(progressPath, first.String()); err != nil {
		t.Fatal(err)
	}
	restored, err := LoadResume(contextPath)
	if err != nil {
		t.Fatal(err)
	}
	remaining, err := resolve.LoadResume(progressPath)
	if err != nil {
		t.Fatal(err)
	}
	if restored.PlanSHA256 != state.PlanSHA256 || len(remaining) != 1 || remaining[0] != second.String() {
		t.Fatalf("restored context=%#v remaining=%v", restored, remaining)
	}
	if err := RemoveResume(contextPath); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadResume(contextPath); !os.IsNotExist(err) {
		t.Fatalf("completed context remains: %v", err)
	}
}
