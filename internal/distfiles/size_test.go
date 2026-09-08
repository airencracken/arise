package distfiles

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestDownloadSizeHonorsUseAndRename(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, "cat", "pkg")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := "DIST base.tar 100 SHA512 aa\nDIST docs.tar 20 SHA512 bb\nDIST renamed.tar 7 SHA512 cc\n"
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

func TestDownloadSizerCacheAndAtomicity(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, "cat/pkg")
	cache := t.TempDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("original"))
	manifest := fmt.Sprintf("DIST pkg.tar 8 SHA256 %x\n", digest)
	if err := os.WriteFile(filepath.Join(dir, "Manifest"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	for _, contents := range []string{"original", "partial", "modified"} {
		if err := os.WriteFile(filepath.Join(cache, "pkg.tar"), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := ManifestDownloadSize(repo, "cat", "pkg", "https://example/pkg.tar", cache, nil)
		want := int64(8)
		if contents == "original" {
			want = 0
		}
		if err != nil || got != want {
			t.Fatalf("cache %q: %d, %v; want %d", contents, got, err, want)
		}
	}
	var sizer DownloadSizer
	if got, err := sizer.Size(repo, "cat", "pkg", "https://example/pkg.tar https://example/missing", "", nil); err == nil || got != 0 {
		t.Fatal("missing Manifest record accepted")
	}
	got, err := sizer.Size(repo, "cat", "pkg", "https://example/pkg.tar", "", nil)
	if err != nil || got != 8 {
		t.Fatalf("failed request consumed artifact: %d, %v", got, err)
	}
	got, err = sizer.Size(repo, "cat", "pkg", "https://mirror/pkg.tar", "", nil)
	if err != nil || got != 0 {
		t.Fatalf("shared artifact counted twice: %d, %v", got, err)
	}
}

func TestDownloadSizerRejectsInvalidAndOverflowingManifest(t *testing.T) {
	for _, manifest := range []string{
		"DIST pkg.tar -1 SHA256 aa\n",
		"DIST pkg.tar 1 SHA256 aa\nDIST pkg.tar 2 SHA256 aa\n",
		"DIST pkg.tar 9223372036854775807 SHA256 aa\nDIST other.tar 1 SHA256 bb\n",
	} {
		repo := t.TempDir()
		dir := filepath.Join(repo, "cat/pkg")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "Manifest"), []byte(manifest), 0644); err != nil {
			t.Fatal(err)
		}
		if got, err := ManifestDownloadSize(repo, "cat", "pkg", "https://example/pkg.tar https://example/other.tar", "", nil); err == nil || got != 0 {
			t.Fatalf("invalid manifest accepted: %d, %v", got, err)
		}
	}
}

func TestPropertyDownloadSizingIgnoresMirrorOrderAndDuplicates(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, "cat/pkg")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Manifest"), []byte("DIST pkg.tar 8 SHA256 aa\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for count := 1; count <= 20; count++ {
		var urls []string
		for index := count; index > 0; index-- {
			urls = append(urls, fmt.Sprintf("https://mirror%d.example/pkg.tar", index))
		}
		var sizer DownloadSizer
		for call := 0; call < 3; call++ {
			got, err := sizer.Size(repo, "cat", "pkg", strings.Join(urls, " "), "", nil)
			want := int64(0)
			if call == 0 {
				want = 8
			}
			if err != nil || got != want {
				t.Fatalf("mirrors=%d call=%d: %d %v", count, call, got, err)
			}
		}
	}
}
