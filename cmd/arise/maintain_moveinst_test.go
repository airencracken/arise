package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/moveinst"
)

func TestMaintainMoveInstPlanSchemaAndDigestAreDeterministic(t *testing.T) {
	oldVDB := *vdbDir
	*vdbDir = "/test/vdb"
	defer func() { *vdbDir = oldVDB }()
	report := moveInstCommandFixtureReport()
	first := newMaintainMoveInstDocument(report, strings.Repeat("a", 64))
	second := newMaintainMoveInstDocument(report, strings.Repeat("a", 64))
	if first.PlanSHA256 != second.PlanSHA256 || len(first.PlanSHA256) != 64 {
		t.Fatalf("plan digest=%q %q", first.PlanSHA256, second.PlanSHA256)
	}
	data, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var decoded maintainMoveInstDocument
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Schema != 1 || decoded.Operation != "maintain-moveinst" || !decoded.Complete || len(decoded.Actions) != 1 {
		t.Fatalf("schema contract=%#v", decoded)
	}
}

func TestMaintainMoveInstCheckPretendFixAndIdempotence(t *testing.T) {
	root, vdbRoot, repoRoot, configRoot := writeMoveInstCommandFixture(t)
	restore := setMoveInstGlobals(t, root, vdbRoot, repoRoot, configRoot)
	defer restore()

	if code := runMaintain([]string{"moveinst", "--check"}); code != 1 {
		t.Fatalf("dirty check exit=%d", code)
	}
	if _, err := os.Stat(filepath.Join(vdbRoot, "cat", "old-1")); err != nil {
		t.Fatalf("check mutated VDB: %v", err)
	}
	*pretend = true
	if code := runMaintain([]string{"moveinst", "--fix"}); code != 0 {
		t.Fatalf("pretend fix exit=%d", code)
	}
	if _, err := os.Stat(filepath.Join(vdbRoot, "cat", "old-1")); err != nil {
		t.Fatalf("pretend mutated VDB: %v", err)
	}
	*pretend = false
	if code := runMaintain([]string{"moveinst", "--fix"}); code != 0 {
		t.Fatalf("fix exit=%d", code)
	}
	if _, err := os.Stat(filepath.Join(vdbRoot, "new", "pkg-1")); err != nil {
		t.Fatalf("moved VDB missing: %v", err)
	}
	if code := runMaintain([]string{"moveinst", "--check"}); code != 0 {
		t.Fatalf("clean check exit=%d", code)
	}
}

func TestMaintainMoveInstRejectsConcurrentStateChange(t *testing.T) {
	root, vdbRoot, repoRoot, configRoot := writeMoveInstCommandFixture(t)
	restore := setMoveInstGlobals(t, root, vdbRoot, repoRoot, configRoot)
	defer restore()
	originalHook := maintainMoveInstBeforeLock
	maintainMoveInstBeforeLock = func() error {
		return os.WriteFile(filepath.Join(vdbRoot, "cat", "old-1", "DEPEND"), []byte("cat/concurrent\n"), 0o644)
	}
	defer func() { maintainMoveInstBeforeLock = originalHook }()
	if code := runMaintain([]string{"moveinst", "--fix"}); code == 0 {
		t.Fatal("concurrent state change was accepted")
	}
	if _, err := os.Stat(filepath.Join(vdbRoot, "cat", "old-1")); err != nil {
		t.Fatalf("concurrent VDB was overwritten: %v", err)
	}
}

func TestMaintainMoveInstInterruptionBeforeLockHasZeroMutation(t *testing.T) {
	root, vdbRoot, repoRoot, configRoot := writeMoveInstCommandFixture(t)
	restore := setMoveInstGlobals(t, root, vdbRoot, repoRoot, configRoot)
	defer restore()
	originalHook := maintainMoveInstBeforeLock
	maintainMoveInstBeforeLock = func() error { return os.ErrClosed }
	defer func() { maintainMoveInstBeforeLock = originalHook }()
	if code := runMaintain([]string{"moveinst", "--fix"}); code == 0 {
		t.Fatal("interruption was accepted")
	}
	if _, err := os.Stat(filepath.Join(vdbRoot, "cat", "old-1")); err != nil {
		t.Fatalf("interruption mutated VDB: %v", err)
	}
}

func moveInstCommandFixtureReport() moveinst.Report {
	return moveinst.Report{
		Issues: []moveinst.Issue{{CPV: "cat/old-1", Kind: "move", Message: "'cat/old-1' moved to 'new/pkg'"}},
		Actions: []moveinst.Action{{
			CPV: "cat/old-1", ResultCPV: "new/pkg-1", From: "/vdb/cat/old-1", To: "/vdb/new/pkg-1",
			Files: map[string]string{"RDEPEND": "new/pkg\n"}, Reasons: []string{"move"},
		}},
	}
}

func writeMoveInstCommandFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	root := t.TempDir()
	vdbRoot, repoRoot := filepath.Join(root, "var", "db", "pkg"), filepath.Join(root, "repo")
	configRoot := filepath.Join(root, "etc", "portage")
	for _, directory := range []string{
		filepath.Join(vdbRoot, "cat", "old-1"), filepath.Join(repoRoot, "profiles", "updates"), configRoot,
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(vdbRoot, "cat", "old-1", "CONTENTS"):   "",
		filepath.Join(vdbRoot, "cat", "old-1", "EAPI"):       "8\n",
		filepath.Join(vdbRoot, "cat", "old-1", "SLOT"):       "0\n",
		filepath.Join(vdbRoot, "cat", "old-1", "repository"): "gentoo\n",
		filepath.Join(vdbRoot, "cat", "old-1", "BUILD_TIME"): "100\n",
		filepath.Join(vdbRoot, "cat", "old-1", "RDEPEND"):    "cat/old\n",
		filepath.Join(repoRoot, "profiles", "repo_name"):     "gentoo\n",
		filepath.Join(repoRoot, "profiles", "updates", "1Q"): "move cat/old new/pkg\n",
	}
	for path, value := range files {
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, vdbRoot, repoRoot, configRoot
}

func setMoveInstGlobals(t *testing.T, root, vdbRoot, repoRoot, configRoot string) func() {
	t.Helper()
	oldVDB, oldRepo, oldConfig, oldJournal := *vdbDir, *repoPath, *portageConfigRoot, *journalDir
	oldPretend, oldJSON, oldSave, oldApprove, oldDigest := *pretend, *jsonOutput, *savePlan, *approvePlan, *approvePlanSHA256
	*vdbDir, *repoPath, *portageConfigRoot = vdbRoot, repoRoot, configRoot
	*journalDir = filepath.Join(root, "journals")
	*pretend, *jsonOutput, *savePlan, *approvePlan, *approvePlanSHA256 = false, false, "", "", ""
	t.Setenv("ROOT", root)
	return func() {
		*vdbDir, *repoPath, *portageConfigRoot, *journalDir = oldVDB, oldRepo, oldConfig, oldJournal
		*pretend, *jsonOutput, *savePlan, *approvePlan, *approvePlanSHA256 = oldPretend, oldJSON, oldSave, oldApprove, oldDigest
	}
}
