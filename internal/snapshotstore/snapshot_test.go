package snapshotstore

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestAtomicityPublishFailureLeavesLogicalPathAndCandidateIntact(t *testing.T) {
	root := t.TempDir()
	logical := filepath.Join(root, "data")
	if err := os.WriteFile(logical, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	candidate, err := Prepare(logical)
	if err != nil {
		t.Fatal(err)
	}
	candidateValue := filepath.Join(candidate.GenerationPath, "value")
	if err := os.WriteFile(candidateValue, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	err = candidate.Publish()
	if err == nil || !strings.Contains(err.Error(), "neither a directory nor symlink") {
		t.Fatalf("Publish error = %v, want invalid logical path type", err)
	}
	old, readErr := os.ReadFile(logical)
	if readErr != nil || string(old) != "old" {
		t.Fatalf("logical path changed after failed publish: value=%q err=%v", old, readErr)
	}
	newValue, readErr := os.ReadFile(candidateValue)
	if readErr != nil || string(newValue) != "new" {
		t.Fatalf("candidate changed after failed publish: value=%q err=%v", newValue, readErr)
	}
	nextLinks, globErr := filepath.Glob(filepath.Join(root, ".data.next-*"))
	if globErr != nil {
		t.Fatal(globErr)
	}
	if len(nextLinks) != 0 {
		t.Fatalf("failed publish leaked temporary links: %v", nextLinks)
	}
}

func TestPublishContractRejectsNilAndMissingCandidates(t *testing.T) {
	var nilCandidate *Candidate
	if err := nilCandidate.Publish(); err == nil || err.Error() != "snapshot: nil candidate" {
		t.Fatalf("nil Publish error = %v", err)
	}

	root := t.TempDir()
	candidate, err := Prepare(filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(candidate.GenerationPath); err != nil {
		t.Fatal(err)
	}
	if err := candidate.Publish(); err == nil || err.Error() != "snapshot: candidate is not a directory" {
		t.Fatalf("missing candidate Publish error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "data")); !os.IsNotExist(err) {
		t.Fatalf("failed publish created logical path: %v", err)
	}
}

func TestMutationSeedSkipsLockAndDirectoriesAndCopiesMutableMode(t *testing.T) {
	root := t.TempDir()
	logical := filepath.Join(root, "data")
	if err := os.MkdirAll(filepath.Join(logical, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logical, "LOCK"), []byte("process state"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(logical, "MANIFEST")
	if err := os.WriteFile(source, []byte("manifest"), 0o640); err != nil {
		t.Fatal(err)
	}
	candidate, err := Prepare(logical)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.SeedFromActive(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Lstat(filepath.Join(candidate.GenerationPath, "LOCK")); !os.IsNotExist(err) {
		t.Fatalf("LOCK copied into candidate: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(candidate.GenerationPath, "nested")); !os.IsNotExist(err) {
		t.Fatalf("nested directory copied into candidate: %v", err)
	}
	copied := filepath.Join(candidate.GenerationPath, "MANIFEST")
	info, err := os.Stat(copied)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("copied mode = %04o, want 0640", got)
	}
	if err := os.WriteFile(copied, []byte("changed"), 0o640); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(source)
	if err != nil || string(original) != "manifest" {
		t.Fatalf("mutable source changed through candidate: value=%q err=%v", original, err)
	}
}

func TestPropertyPruneRetainsActiveAndExactlyRequestedGenerations(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("legacy directory publication uses rename exchange on Linux")
	}
	for keep := 1; keep <= 4; keep++ {
		t.Run(string(rune('0'+keep)), func(t *testing.T) {
			root := t.TempDir()
			logical := filepath.Join(root, "data")
			var latest *Candidate
			for generation := 1; generation <= 5; generation++ {
				candidate, err := Prepare(logical)
				if err != nil {
					t.Fatal(err)
				}
				value := []byte{byte('0' + generation)}
				if err := os.WriteFile(filepath.Join(candidate.GenerationPath, "value"), value, 0o600); err != nil {
					t.Fatal(err)
				}
				stamp := time.Unix(int64(generation), 0)
				if err := os.Chtimes(candidate.GenerationPath, stamp, stamp); err != nil {
					t.Fatal(err)
				}
				if err := candidate.Publish(); err != nil {
					t.Fatal(err)
				}
				latest = candidate
			}
			if err := latest.Prune(keep); err != nil {
				t.Fatal(err)
			}
			active, err := os.ReadFile(filepath.Join(logical, "value"))
			if err != nil || string(active) != "5" {
				t.Fatalf("active generation after Prune(%d) = %q, %v", keep, active, err)
			}
			entries, err := os.ReadDir(latest.generationRoot)
			if err != nil {
				t.Fatal(err)
			}
			retained := 0
			for _, entry := range entries {
				if entry.IsDir() {
					retained++
				}
			}
			if retained != keep {
				t.Fatalf("Prune(%d) retained %d directories", keep, retained)
			}
		})
	}
}

func TestAdversarialPruneRejectsNonPositiveRetentionWithoutMutation(t *testing.T) {
	root := t.TempDir()
	logical := filepath.Join(root, "data")
	candidate, err := Prepare(logical)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidate.GenerationPath, "value"), []byte("active"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := candidate.Publish(); err != nil {
		t.Fatal(err)
	}

	for _, keep := range []int{0, -1, -int(^uint(0)>>1) - 1} {
		err := candidate.Prune(keep)
		if err == nil || err.Error() != "snapshot: retention must keep at least one generation" {
			t.Fatalf("Prune(%d) error = %v", keep, err)
		}
		value, readErr := os.ReadFile(filepath.Join(logical, "value"))
		if readErr != nil || string(value) != "active" {
			t.Fatalf("Prune(%d) changed active generation: value=%q err=%v", keep, value, readErr)
		}
	}
}
