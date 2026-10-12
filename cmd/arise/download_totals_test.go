package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/resolve"
)

func TestPlanActionDownloadSizesDoesNotVerifyCachedContents(t *testing.T) {
	repo, cache := t.TempDir(), t.TempDir()
	dir := filepath.Join(repo, "app-misc", "example")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("original"))
	if err := os.WriteFile(filepath.Join(dir, "Manifest"), []byte(fmt.Sprintf("DIST pkg.tar 8 SHA256 %x\n", digest)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "pkg.tar"), []byte("modified"), 0644); err != nil {
		t.Fatal(err)
	}
	parsed, err := atom.Parse("app-misc/example-1")
	if err != nil {
		t.Fatal(err)
	}
	actions := []resolve.PkgAction{{Atom: parsed, RepositoryPath: repo, SrcURI: "https://example/pkg.tar"}}
	for _, size := range planActionDownloadSizes(actions, cache, false) {
		if size != 0 {
			t.Fatalf("display verified cache contents instead of estimating missing bytes: %d", size)
		}
	}
}

func BenchmarkPlanActionDownloadSizesCachedArtifact(b *testing.B) {
	repo, cache := b.TempDir(), b.TempDir()
	dir := filepath.Join(repo, "app-misc", "example")
	if err := os.MkdirAll(dir, 0755); err != nil {
		b.Fatal(err)
	}
	file, err := os.Create(filepath.Join(cache, "pkg.tar"))
	if err != nil {
		b.Fatal(err)
	}
	if err := file.Truncate(64 << 20); err != nil {
		b.Fatal(err)
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		b.Fatal(err)
	}
	if err := file.Close(); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Manifest"), []byte(fmt.Sprintf("DIST pkg.tar %d SHA256 %x\n", 64<<20, digest.Sum(nil))), 0644); err != nil {
		b.Fatal(err)
	}
	parsed, err := atom.Parse("app-misc/example-1")
	if err != nil {
		b.Fatal(err)
	}
	actions := []resolve.PkgAction{{Atom: parsed, RepositoryPath: repo, SrcURI: "https://example/pkg.tar"}}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		for _, size := range planActionDownloadSizes(actions, cache, false) {
			if size != 0 {
				b.Fatal("cached download size changed")
			}
		}
	}
}

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
