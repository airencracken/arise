package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepositoryHasSlotDoesNotConfuseParallelSlot(t *testing.T) {
	repo := t.TempDir()
	oldRepo := *repoPath
	*repoPath = repo
	t.Cleanup(func() { *repoPath = oldRepo })
	pkgDir := filepath.Join(repo, "cat", "slot-probe")
	cacheDir := filepath.Join(repo, "metadata", "md5-cache", "cat")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "slot-probe-6.ebuild"), []byte("EAPI=8\nSLOT=6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "slot-probe-6"), []byte("SLOT=6/6.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if repositoryHasSlot("cat", "slot-probe", "5") {
		t.Fatal("slot 6 ebuild satisfied installed slot 5")
	}
	if !repositoryHasSlot("cat", "slot-probe", "6") {
		t.Fatal("matching slot 6 ebuild was not found")
	}
}
