package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/resolve"
)

func TestPlanActionDownloadSizesDeduplicatesSharedArtifact(t *testing.T) {
	repo := t.TempDir()
	var actions []resolve.PkgAction
	for _, cpv := range []string{"sys-devel/binutils-2", "sys-libs/binutils-libs-2"} {
		parsed, err := atom.Parse(cpv)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(repo, parsed.Category, parsed.Package)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "Manifest"), []byte("DIST binutils.tar 1024 SHA256 aa\n"), 0644); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, resolve.PkgAction{Atom: parsed, RepositoryPath: repo, SrcURI: "https://example/binutils.tar"})
	}
	sizes := planActionDownloadSizes(actions, "", false)
	var total int64
	for _, size := range sizes {
		total += size
	}
	if total != 1024 || len(sizes) != 2 {
		t.Fatalf("shared download counted incorrectly: %v", sizes)
	}
}
