package snapshotstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPublishKeepsOldGenerationUntilAtomicSwitch(t *testing.T) {
	root := t.TempDir()
	logical := filepath.Join(root, "data")
	if err := os.MkdirAll(logical, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logical, "value"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	candidate, err := Prepare(logical)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidate.GenerationPath, "value"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(logical, "value"))
	if err != nil || string(before) != "old" {
		t.Fatalf("old generation unavailable before publish: %q, %v", before, err)
	}
	if err := candidate.Publish(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(logical, "value"))
	if err != nil || string(after) != "new" {
		t.Fatalf("new generation unavailable after publish: %q, %v", after, err)
	}
	legacy, err := filepath.Glob(filepath.Join(root, ".data.generations", "legacy-*", "value"))
	if err != nil || len(legacy) != 1 {
		t.Fatalf("previous generation not retained: %v, %v", legacy, err)
	}
	old, err := os.ReadFile(legacy[0])
	if err != nil || string(old) != "old" {
		t.Fatalf("retained generation = %q, %v", old, err)
	}
}

func TestAbandonedCandidateDoesNotAffectReaders(t *testing.T) {
	root := t.TempDir()
	logical := filepath.Join(root, "data")
	if err := os.MkdirAll(logical, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logical, "value"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	candidate, err := Prepare(logical)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidate.GenerationPath, "value"), []byte("partial"), 0644); err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile(filepath.Join(logical, "value"))
	if err != nil || string(value) != "old" {
		t.Fatalf("partial candidate became visible: %q, %v", value, err)
	}
}

func TestSeedHardLinksImmutableTablesAndCopiesMutableFiles(t *testing.T) {
	root := t.TempDir()
	logical := filepath.Join(root, "data")
	if err := os.MkdirAll(logical, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logical, "000001.sst"), []byte("table"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logical, "MANIFEST"), []byte("old manifest"), 0644); err != nil {
		t.Fatal(err)
	}
	candidate, err := Prepare(logical)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.SeedFromActive(); err != nil {
		t.Fatal(err)
	}
	sourceInfo, err := os.Stat(filepath.Join(logical, "000001.sst"))
	if err != nil {
		t.Fatal(err)
	}
	seedInfo, err := os.Stat(filepath.Join(candidate.GenerationPath, "000001.sst"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(sourceInfo, seedInfo) {
		t.Fatal("immutable SSTable was not hard-linked")
	}
	if err := os.WriteFile(filepath.Join(candidate.GenerationPath, "MANIFEST"), []byte("new manifest"), 0644); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(logical, "MANIFEST"))
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != "old manifest" {
		t.Fatal("mutable manifest was shared with active generation")
	}
}

func TestPruneKeepsActiveAndOneRollbackGeneration(t *testing.T) {
	root := t.TempDir()
	logical := filepath.Join(root, "data")
	for i, value := range []string{"one", "two", "three"} {
		candidate, err := Prepare(logical)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(candidate.GenerationPath, "value"), []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(int64(i+1), 0)
		if err := os.Chtimes(candidate.GenerationPath, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if err := candidate.Publish(); err != nil {
			t.Fatal(err)
		}
		if i == 2 {
			if err := candidate.Prune(2); err != nil {
				t.Fatal(err)
			}
		}
	}
	active, err := os.ReadFile(filepath.Join(logical, "value"))
	if err != nil {
		t.Fatal(err)
	}
	if string(active) != "three" {
		t.Fatalf("active value = %q", active)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".data.generations"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("retained %d generations, want 2", count)
	}
}
