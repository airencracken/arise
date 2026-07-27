package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/worldmaint"
)

func TestMaintainWorldPlanSchemaAndDigestAreDeterministic(t *testing.T) {
	originalWorld := *worldFile
	*worldFile = "/test/world"
	t.Cleanup(func() { *worldFile = originalWorld })
	report := worldmaint.Report{
		Entries: []string{"cat/missing"},
		Issues:  []worldmaint.Issue{{Entry: "cat/missing", Kind: worldmaint.Unavailable, Message: `"cat/missing" has no available ebuilds`}},
		Actions: []worldmaint.Action{{Action: "remove", Entry: "cat/missing", Reason: worldmaint.Unavailable}},
	}
	first := newMaintainWorldDocument(report, strings.Repeat("a", 64))
	second := newMaintainWorldDocument(report, strings.Repeat("a", 64))
	if first.PlanSHA256 != second.PlanSHA256 || len(first.PlanSHA256) != 64 {
		t.Fatalf("non-deterministic digest: %q %q", first.PlanSHA256, second.PlanSHA256)
	}
	data, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var decoded maintainWorldDocument
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Schema != 1 || decoded.Operation != "maintain-world" || !decoded.Complete ||
		decoded.Summary != (maintainWorldSummary{Entries: 1, Issues: 1, Actions: 1}) {
		t.Fatalf("schema contract=%#v", decoded)
	}
}

func TestMaintainWorldPlanDigestBindsStateAndActions(t *testing.T) {
	base := maintainWorldDocument{
		Schema: 1, Operation: "maintain-world", Complete: true, StateSHA256: strings.Repeat("a", 64),
		Entries: []string{"cat/missing"},
		Actions: []worldmaint.Action{{Action: "remove", Entry: "cat/missing", Reason: worldmaint.Unavailable}},
	}
	first := maintainWorldPlanSHA256(base)
	changedState := base
	changedState.StateSHA256 = strings.Repeat("b", 64)
	changedAction := base
	changedAction.Actions = []worldmaint.Action{{Action: "remove", Entry: "cat/other", Reason: worldmaint.Unavailable}}
	if first == maintainWorldPlanSHA256(changedState) || first == maintainWorldPlanSHA256(changedAction) {
		t.Fatal("authorization digest did not bind state and actions")
	}
}

func TestMaintainCommandExists(t *testing.T) {
	command, args := selectCommand([]string{"maintain", "world", "--check"})
	if command != "maintain" || !reflect.DeepEqual(args, []string{"world", "--check"}) {
		t.Fatalf("selectCommand=%q %v", command, args)
	}
}

func TestMaintainWorldFixRepairsDirectlyAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	worldPath := filepath.Join(root, "world")
	vdbRoot := filepath.Join(root, "vdb")
	repoRoot := filepath.Join(root, "repo")
	configRoot := filepath.Join(root, "etc", "portage")
	plans := filepath.Join(root, "plans")
	for _, directory := range []string{vdbRoot, filepath.Join(repoRoot, "metadata", "md5-cache"), configRoot, plans} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(worldPath, []byte("cat/missing\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	restore := setMaintainGlobals(t, worldPath, vdbRoot, repoRoot, configRoot, plans)
	defer restore()

	if code := runMaintain([]string{"world", "--fix"}); code != 0 {
		t.Fatalf("direct repair exit=%d", code)
	}
	data, err := os.ReadFile(worldPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("world after repair=%q", data)
	}
	info, err := os.Stat(worldPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("world mode=%#o, want 0640", info.Mode().Perm())
	}
	if code := runMaintain([]string{"world", "--check"}); code != 0 {
		t.Fatalf("clean second check exit=%d", code)
	}
}

func TestMaintainWorldFixAcceptsFreshApprovedPlan(t *testing.T) {
	root := t.TempDir()
	worldPath := filepath.Join(root, "world")
	vdbRoot := filepath.Join(root, "vdb")
	repoRoot := filepath.Join(root, "repo")
	configRoot := filepath.Join(root, "etc", "portage")
	plans := filepath.Join(root, "plans")
	for _, directory := range []string{vdbRoot, filepath.Join(repoRoot, "metadata", "md5-cache"), configRoot, plans} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(worldPath, []byte("cat/missing\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	restore := setMaintainGlobals(t, worldPath, vdbRoot, repoRoot, configRoot, plans)
	defer restore()

	*pretend = true
	*savePlan = "repair"
	if code := runMaintain([]string{"world", "--fix"}); code != 0 {
		t.Fatalf("pretend plan exit=%d", code)
	}
	planPath := filepath.Join(plans, "repair.json")
	if _, err := os.Stat(planPath); err != nil {
		t.Fatalf("saved plan: %v", err)
	}

	*pretend = false
	*savePlan = ""
	*approvePlan = planPath
	if code := runMaintain([]string{"world", "--fix"}); code != 0 {
		t.Fatalf("approved repair exit=%d", code)
	}
	data, err := os.ReadFile(worldPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("world after repair=%q", data)
	}
}

func TestMaintainWorldRejectsStaleApprovedPlan(t *testing.T) {
	root := t.TempDir()
	worldPath := filepath.Join(root, "world")
	vdbRoot := filepath.Join(root, "vdb")
	repoRoot := filepath.Join(root, "repo")
	configRoot := filepath.Join(root, "config")
	plans := filepath.Join(root, "plans")
	for _, directory := range []string{vdbRoot, filepath.Join(repoRoot, "metadata", "md5-cache"), configRoot, plans} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(worldPath, []byte("cat/missing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := setMaintainGlobals(t, worldPath, vdbRoot, repoRoot, configRoot, plans)
	defer restore()
	*pretend, *savePlan = true, "stale"
	if code := runMaintain([]string{"world", "--fix"}); code != 0 {
		t.Fatalf("save plan exit=%d", code)
	}
	if err := os.WriteFile(worldPath, []byte("cat/missing\ncat/other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	*pretend, *savePlan, *approvePlan = false, "", filepath.Join(plans, "stale.json")
	if code := runMaintain([]string{"world", "--fix"}); code == 0 {
		t.Fatal("stale plan unexpectedly authorized repair")
	}
	data, err := os.ReadFile(worldPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "cat/missing\ncat/other\n" {
		t.Fatalf("stale repair mutated world: %q", data)
	}
}

func setMaintainGlobals(t *testing.T, worldPath, vdbRoot, repoRoot, configRoot, plans string) func() {
	t.Helper()
	oldWorld, oldVDB, oldRepo, oldConfig, oldPlans := *worldFile, *vdbDir, *repoPath, *portageConfigRoot, *planDir
	oldPretend, oldJSON, oldSave, oldApprove, oldDigest := *pretend, *jsonOutput, *savePlan, *approvePlan, *approvePlanSHA256
	*worldFile, *vdbDir, *repoPath, *portageConfigRoot, *planDir = worldPath, vdbRoot, repoRoot, configRoot, plans
	*pretend, *jsonOutput, *savePlan, *approvePlan, *approvePlanSHA256 = false, false, "", "", ""
	return func() {
		*worldFile, *vdbDir, *repoPath, *portageConfigRoot, *planDir = oldWorld, oldVDB, oldRepo, oldConfig, oldPlans
		*pretend, *jsonOutput, *savePlan, *approvePlan, *approvePlanSHA256 = oldPretend, oldJSON, oldSave, oldApprove, oldDigest
	}
}
