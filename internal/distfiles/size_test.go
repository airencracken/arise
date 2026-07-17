package distfiles

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManifestDownloadSizeHonorsUseAndRename(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, "cat", "pkg")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := "DIST base.tar 100 SHA512 x\nDIST docs.tar 20 SHA512 y\nDIST renamed.tar 7 SHA512 z\n"
	if err := os.WriteFile(filepath.Join(dir, "Manifest"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	src := "https://example/base.tar doc? ( https://example/docs.tar ) https://example/source.tar -> renamed.tar"
	got, err := ManifestDownloadSize(repo, "cat", "pkg", src, "", map[string]bool{"doc": false})
	if err != nil {
		t.Fatal(err)
	}
	if got != 107 {
		t.Fatalf("size = %d, want 107", got)
	}
}
