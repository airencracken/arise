package moveinst

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCheckPlansMovesSlotMovesAndDependencyMetadata(t *testing.T) {
	root := t.TempDir()
	vdbRoot := filepath.Join(root, "var", "db", "pkg")
	repoRoot := filepath.Join(root, "repo")
	writeUpdate(t, repoRoot, "move old-cat/old-pkg new-cat/new-pkg\nslotmove dev-libs/lib 0 1\n")
	writeInstalled(t, vdbRoot, "old-cat/old-pkg-2", "0", "gentoo", map[string]string{
		"RDEPEND": "dev-libs/lib:0 old-cat/old-pkg",
	})
	writeInstalled(t, vdbRoot, "app-misc/consumer-1", "0", "gentoo", map[string]string{
		"DEPEND": "flag? ( >=old-cat/old-pkg-2 ) !dev-libs/lib:0",
	})
	writeInstalled(t, vdbRoot, "dev-libs/lib-3", "0/sub", "gentoo", nil)

	report, err := Check(vdbRoot, []Repository{{Name: "gentoo", Root: repoRoot, Master: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Actions) != 3 {
		t.Fatalf("actions=%#v", report.Actions)
	}
	byCPV := map[string]Action{}
	for _, action := range report.Actions {
		byCPV[action.CPV] = action
	}
	moved := byCPV["old-cat/old-pkg-2"]
	if moved.ResultCPV != "new-cat/new-pkg-2" || moved.Files["RDEPEND"] != "dev-libs/lib:1 new-cat/new-pkg\n" {
		t.Fatalf("move action=%#v", moved)
	}
	if got := byCPV["app-misc/consumer-1"].Files["DEPEND"]; got != "flag? ( >=new-cat/new-pkg-2 ) !dev-libs/lib:1\n" {
		t.Fatalf("consumer DEPEND=%q", got)
	}
	if got := byCPV["dev-libs/lib-3"].Files["SLOT"]; got != "1/sub\n" {
		t.Fatalf("slot update=%q", got)
	}
}

func TestCheckUsesRepositoryRulesAndMasterFallback(t *testing.T) {
	root := t.TempDir()
	vdbRoot := filepath.Join(root, "vdb")
	master, overlay := filepath.Join(root, "gentoo"), filepath.Join(root, "overlay")
	writeUpdate(t, master, "move cat/old cat/master-new\n")
	writeUpdate(t, overlay, "move cat/old cat/overlay-new\n")
	writeInstalled(t, vdbRoot, "cat/old-1", "0", "overlay", nil)
	writeInstalled(t, vdbRoot, "cat/old-2", "0", "unknown", nil)
	report, err := Check(vdbRoot, []Repository{
		{Name: "gentoo", Root: master, Master: true},
		{Name: "overlay", Root: overlay},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, action := range report.Actions {
		got[action.CPV] = action.ResultCPV
	}
	want := map[string]string{"cat/old-1": "cat/overlay-new-1", "cat/old-2": "cat/master-new-2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repository moves=%v, want %v", got, want)
	}
}

func TestCheckDoesNotReapplyMoveToSameBuildButStillRepairsMetadata(t *testing.T) {
	root := t.TempDir()
	vdbRoot, repoRoot := filepath.Join(root, "vdb"), filepath.Join(root, "repo")
	writeUpdate(t, repoRoot, "move cat/old new/pkg\n")
	writeInstalled(t, vdbRoot, "cat/old-1", "0", "gentoo", map[string]string{"RDEPEND": "cat/old"})
	writeInstalled(t, vdbRoot, "new/pkg-1", "0", "gentoo", nil)
	report, err := Check(vdbRoot, []Repository{{Name: "gentoo", Root: repoRoot, Master: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Actions) != 1 || report.Actions[0].From != report.Actions[0].To ||
		report.Actions[0].Files["RDEPEND"] != "new/pkg\n" {
		t.Fatalf("same-build repair=%#v", report)
	}
}

func TestDependencyRewriteIsAtomAwareAndOrdered(t *testing.T) {
	value := "|| ( =cat/pkg-1* cat/pkg-extra:0 ) !!cat/pkg:0[flag?]"
	first, err := updateDependency(value, Command{Kind: "move", Package: "cat/pkg", Destination: "new/pkg"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := updateDependency(first, Command{Kind: "slotmove", Package: "new/pkg", OldSlot: "0", NewSlot: "2"})
	if err != nil {
		t.Fatal(err)
	}
	if second != "|| ( =new/pkg-1* cat/pkg-extra:0 ) !!new/pkg:2[flag?]" {
		t.Fatalf("rewritten dependency=%q", second)
	}
}

func TestDependencyRewritePreservesBytesWhenNoAtomChanges(t *testing.T) {
	value := "flag?   (   cat/other:0   )"
	updated, err := updateDependency(value, Command{Kind: "move", Package: "cat/pkg", Destination: "new/pkg"})
	if err != nil {
		t.Fatal(err)
	}
	if updated != value {
		t.Fatalf("unchanged dependency was normalized: %q -> %q", value, updated)
	}
}

func TestApplyIsJournaledAndIdempotent(t *testing.T) {
	root := t.TempDir()
	vdbRoot, repoRoot := filepath.Join(root, "var", "db", "pkg"), filepath.Join(root, "repo")
	writeUpdate(t, repoRoot, "move cat/old new/pkg\n")
	writeInstalled(t, vdbRoot, "cat/old-1", "0", "gentoo", map[string]string{"RDEPEND": "cat/old"})
	if err := os.Chmod(filepath.Join(vdbRoot, "cat", "old-1", "RDEPEND"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := Check(vdbRoot, []Repository{{Name: "gentoo", Root: repoRoot, Master: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(report.Actions, ApplyConfig{RootDir: root, VDBRoot: vdbRoot, JournalDir: filepath.Join(root, "journals")}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(vdbRoot, "new", "pkg-1")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(vdbRoot, "new", "pkg-1", "RDEPEND"))
	if err != nil || string(data) != "new/pkg\n" {
		t.Fatalf("RDEPEND=%q, err=%v", data, err)
	}
	info, err := os.Stat(filepath.Join(vdbRoot, "new", "pkg-1", "RDEPEND"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("RDEPEND mode=%v", info.Mode().Perm())
	}
	second, err := Check(vdbRoot, []Repository{{Name: "gentoo", Root: repoRoot, Master: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Actions) != 0 || len(second.Issues) != 0 {
		t.Fatalf("second check=%#v", second)
	}
}

func TestApplyRejectsTraversalAndUnknownMetadataBeforeMutation(t *testing.T) {
	root := t.TempDir()
	vdbRoot := filepath.Join(root, "var", "db", "pkg")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(filepath.Join(vdbRoot, "cat", "pkg-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []Action{
		{CPV: "cat/pkg-1", From: filepath.Join(vdbRoot, "cat", "pkg-1"), To: outside},
		{CPV: "cat/pkg-1", From: filepath.Join(vdbRoot, "cat", "pkg-1"), To: filepath.Join(vdbRoot, "cat", "pkg-1"), Files: map[string]string{"../../escape": "bad"}},
	}
	for _, action := range tests {
		if err := Apply([]Action{action}, ApplyConfig{RootDir: root, VDBRoot: vdbRoot, JournalDir: filepath.Join(root, "journals")}); err == nil {
			t.Fatalf("unsafe action accepted: %#v", action)
		}
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("unsafe action mutated outside path: %v", err)
	}
}

func TestApplyRollsBackRenameAndMetadataOnFailure(t *testing.T) {
	root := t.TempDir()
	vdbRoot, repoRoot := filepath.Join(root, "var", "db", "pkg"), filepath.Join(root, "repo")
	writeUpdate(t, repoRoot, "move cat/old new/pkg\n")
	writeInstalled(t, vdbRoot, "cat/old-1", "0", "gentoo", map[string]string{"RDEPEND": "cat/old"})
	report, err := Check(vdbRoot, []Repository{{Name: "gentoo", Root: repoRoot, Master: true}})
	if err != nil {
		t.Fatal(err)
	}
	original := writeMetadata
	writeMetadata = func(string, []byte) error { return errors.New("injected metadata failure") }
	defer func() { writeMetadata = original }()
	err = Apply(report.Actions, ApplyConfig{RootDir: root, VDBRoot: vdbRoot, JournalDir: filepath.Join(root, "journals")})
	if err == nil || !strings.Contains(err.Error(), "injected metadata failure") {
		t.Fatalf("apply error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(vdbRoot, "cat", "old-1")); err != nil {
		t.Fatalf("old VDB was not restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vdbRoot, "new", "pkg-1")); !os.IsNotExist(err) {
		t.Fatalf("new VDB survived rollback: %v", err)
	}
}

func TestCheckRejectsMalformedUpdateWithoutMutation(t *testing.T) {
	root := t.TempDir()
	vdbRoot, repoRoot := filepath.Join(root, "vdb"), filepath.Join(root, "repo")
	writeUpdate(t, repoRoot, "move cat/only-two-fields\n")
	writeInstalled(t, vdbRoot, "cat/pkg-1", "0", "gentoo", nil)
	if _, err := Check(vdbRoot, []Repository{{Name: "gentoo", Root: repoRoot, Master: true}}); err == nil {
		t.Fatal("malformed update accepted")
	}
	if _, err := os.Stat(filepath.Join(vdbRoot, "cat", "pkg-1")); err != nil {
		t.Fatalf("check mutated VDB: %v", err)
	}
}

func writeUpdate(t *testing.T, repoRoot, content string) {
	t.Helper()
	path := filepath.Join(repoRoot, "profiles", "updates", "1Q-2026")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeInstalled(t *testing.T, vdbRoot, cpv, slot, repository string, extra map[string]string) {
	t.Helper()
	parts := strings.SplitN(cpv, "/", 2)
	path := filepath.Join(vdbRoot, parts[0], parts[1])
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"CONTENTS": "", "EAPI": "8\n", "SLOT": slot + "\n", "repository": repository + "\n", "BUILD_TIME": "100\n"}
	for _, key := range dependencyKeys {
		files[key] = "\n"
	}
	for name, value := range extra {
		files[name] = value + "\n"
	}
	for name, value := range files {
		if err := os.WriteFile(filepath.Join(path, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
