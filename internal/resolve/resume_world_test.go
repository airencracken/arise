package resolve

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestResumeWorldSelectionsSurviveCompletionAndSkip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume")
	first, second := mustParse("cat/dependency-1"), mustParse("cat/target-1")
	if err := SaveResumeWithWorldSelections(path, &ResolveResult{Install: []PkgAction{{Atom: first}, {Atom: second}}}, []string{"cat/target"}); err != nil {
		t.Fatal(err)
	}
	if err := MarkResumeComplete(path, first.String()); err != nil {
		t.Fatal(err)
	}
	remaining, favorites, err := LoadResumeWithWorldSelections(path)
	if err != nil || !slices.Equal(remaining, []string{second.String()}) || !slices.Equal(favorites, []string{"cat/target"}) {
		t.Fatalf("partial progress = %v, %v, %v", remaining, favorites, err)
	}
	if err := SkipFirstResume(path); err != nil {
		t.Fatal(err)
	}
	remaining, favorites, err = LoadResumeWithWorldSelections(path)
	if err != nil || len(remaining) != 0 || !slices.Equal(favorites, []string{"cat/target"}) {
		t.Fatalf("completed progress = %v, %v, %v", remaining, favorites, err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, selections := range [][]string{{"cat/target", "cat/target"}, {"../target"}, {"=cat/target-1"}, {"cat/target:0"}} {
		if err := SaveResumeWithWorldSelections(path, &ResolveResult{Install: []PkgAction{{Atom: second}}}, selections); err == nil {
			t.Fatalf("invalid favorite accepted: %v", selections)
		}
		if after, err := os.ReadFile(path); err != nil || string(after) != string(before) {
			t.Fatalf("invalid favorite replaced checkpoint: %q, %v", after, err)
		}
	}
}

func TestResumeRejectsMalformedWorldSelectionSchema(t *testing.T) {
	for _, favorites := range []string{`"cat/pkg"`, `["../pkg"]`, `["cat/pkg", "cat/pkg"]`, `["=cat/pkg-1"]`} {
		path := filepath.Join(t.TempDir(), "resume")
		if err := os.WriteFile(path, []byte(`{"packages":[],"world_selections":`+favorites+`}`), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadResume(path); err == nil {
			t.Fatalf("malformed favorites accepted: %s", favorites)
		}
	}
}
