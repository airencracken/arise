package resolve

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResumeDuplicateSaveAndUnknownCompletionAreAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume")
	item := PkgAction{Atom: mustParse("cat/pkg-1")}
	if err := SaveResume(path, &ResolveResult{Install: []PkgAction{item}}); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{"duplicate save", func() error { return SaveResume(path, &ResolveResult{Install: []PkgAction{item, item}}) }},
		{"unknown completion", func() error { return MarkResumeComplete(path, "cat/other-1") }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(); err == nil {
				t.Fatal("invalid resume mutation accepted")
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != string(original) {
				t.Fatalf("invalid mutation changed checkpoint: %q %v", got, err)
			}
		})
	}
}
