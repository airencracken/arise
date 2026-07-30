package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaintainMergesCheckPretendAndPurge(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ROOT", root)
	vdb := filepath.Join(root, "var/db/pkg")
	failed := filepath.Join(vdb, "dev-util", "pkgcheck-1-MERGING-")
	tracking := filepath.Join(root, "var/lib/portage/failed-merges")
	if err := os.MkdirAll(failed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(tracking), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracking, []byte("dev-util/pkgcheck-1-MERGING- 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldVDB, oldPretend := *vdbDir, *pretend
	*vdbDir, *pretend = vdb, false
	t.Cleanup(func() { *vdbDir, *pretend = oldVDB, oldPretend })

	if code := runMaintain([]string{"merges", "--check"}); code != 1 {
		t.Fatalf("dirty check exit = %d", code)
	}
	*pretend = true
	if code := runMaintain([]string{"merges", "--fix"}); code != 0 {
		t.Fatalf("pretend fix exit = %d", code)
	}
	if _, err := os.Stat(failed); err != nil {
		t.Fatalf("pretend changed VDB: %v", err)
	}
	*pretend = false
	*pretend = true
	if code := runMaintain([]string{"merges", "--purge"}); code != 0 {
		t.Fatalf("pretend purge exit = %d", code)
	}
	if _, err := os.Stat(tracking); err != nil {
		t.Fatalf("pretend purge changed tracking: %v", err)
	}
	*pretend = false
	if code := runMaintain([]string{"merges", "--purge"}); code != 0 {
		t.Fatalf("purge exit = %d", code)
	}
	if _, err := os.Stat(tracking); !os.IsNotExist(err) {
		t.Fatalf("tracking remains: %v", err)
	}
	if _, err := os.Stat(failed); err != nil {
		t.Fatalf("purge changed VDB: %v", err)
	}
}

func TestMaintainResumeCheckFixAndIdempotence(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ROOT", root)
	arise := filepath.Join(root, "var/tmp/arise/resume")
	mtimedb := filepath.Join(root, "var/cache/edb/mtimedb")
	for path, data := range map[string]string{
		arise:   `{"packages":[{"cpv":"cat/pkg-1","atom":"=cat/pkg-1","completed":false}]}`,
		mtimedb: `{"resume":{},"keep":{"value":1}}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldResume, oldJournal, oldPretend := *resumeFile, *journalDir, *pretend
	*resumeFile = arise
	*journalDir = filepath.Join(root, "journals")
	*pretend = false
	t.Cleanup(func() {
		*resumeFile, *journalDir, *pretend = oldResume, oldJournal, oldPretend
	})

	if code := runMaintain([]string{"resume", "--check"}); code != 1 {
		t.Fatalf("dirty check exit = %d", code)
	}
	*pretend = true
	if code := runMaintain([]string{"cleanresume", "--fix"}); code != 0 {
		t.Fatalf("pretend fix exit = %d", code)
	}
	if _, err := os.Stat(arise); err != nil {
		t.Fatalf("pretend changed Arise resume: %v", err)
	}
	*pretend = false
	if code := runMaintain([]string{"resume", "--fix"}); code != 0 {
		t.Fatalf("fix exit = %d", code)
	}
	if code := runMaintain([]string{"resume", "--check"}); code != 0 {
		t.Fatalf("clean check exit = %d", code)
	}
	if code := runMaintain([]string{"resume", "--fix"}); code != 0 {
		t.Fatalf("idempotent fix exit = %d", code)
	}
	data, err := os.ReadFile(mtimedb)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || !containsAll(string(data), `"keep"`, `"value"`) {
		t.Fatalf("unrelated mtimedb state lost: %s", data)
	}
}

func TestMaintainStateOptionContracts(t *testing.T) {
	for _, args := range [][]string{
		{"merges"}, {"merges", "--check", "--fix"}, {"resume"}, {"resume", "--check", "--fix"},
	} {
		if code := runMaintain(args); code != 2 {
			t.Fatalf("%v exit = %d, want 2", args, code)
		}
	}
}

func containsAll(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			return false
		}
	}
	return true
}
