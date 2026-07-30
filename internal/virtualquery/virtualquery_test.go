package virtualquery

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/airencracken/arise/internal/portage"
)

func TestExpandMergesProfileMappingsAndRemovals(t *testing.T) {
	root := t.TempDir()
	parent, leaf := filepath.Join(root, "parent"), filepath.Join(root, "leaf")
	for _, directory := range []string{parent, leaf} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(parent, "virtuals"), []byte("virtual/editor app-editors/vim app-editors/nano\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "virtuals"), []byte("virtual/editor -app-editors/nano app-editors/helix !app-editors/blocked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Expand(&portage.Config{ProfileParents: []string{parent}, ProfilePath: leaf}, "virtual/editor")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"app-editors/helix", "app-editors/vim"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Expand = %v, want %v", got, want)
	}
}

func TestExpandRetainsUnknownAndNonVirtualAtoms(t *testing.T) {
	for _, query := range []string{"virtual/missing", "app-editors/vim"} {
		got, err := Expand(&portage.Config{}, query)
		if err != nil || !reflect.DeepEqual(got, []string{query}) {
			t.Fatalf("Expand(%q) = %v, %v", query, got, err)
		}
	}
}

func TestExpandRejectsAdversarialAtom(t *testing.T) {
	if _, err := Expand(&portage.Config{}, "../../etc/passwd"); err == nil {
		t.Fatal("path-like atom accepted")
	}
}
