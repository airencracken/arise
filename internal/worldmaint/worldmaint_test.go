package worldmaint

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/portage"
)

func TestCheckClassifiesWorldProblemsAndBuildsRepair(t *testing.T) {
	root := t.TempDir()
	worldPath := filepath.Join(root, "world")
	vdbRoot := filepath.Join(root, "vdb")
	repoRoot := filepath.Join(root, "repo")
	writeFixture(t, worldPath, "cat/good\ncat/missing\ncat/not-installed\ncat/masked\ncat/old\ncat/good\nnot-an-atom\n")
	writeInstalled(t, vdbRoot, "cat/good-1", "0", "test")
	writeInstalled(t, vdbRoot, "cat/missing-1", "0", "test")
	writeInstalled(t, vdbRoot, "cat/masked-1", "0", "test")
	writeInstalled(t, vdbRoot, "cat/old-1", "0", "test")
	writeCache(t, repoRoot, "cat/good-1", "0")
	writeCache(t, repoRoot, "cat/not-installed-1", "0")
	writeCache(t, repoRoot, "cat/masked-1", "0")
	writeFixture(t, filepath.Join(repoRoot, "profiles", "repo_name"), "test\n")
	writeFixture(t, filepath.Join(repoRoot, "profiles", "updates", "1Q-2026"), "move cat/old cat/new\n")

	report, err := Check(worldPath, vdbRoot, repoRoot, &portage.Config{PackageMask: []string{"cat/masked"}})
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]Kind)
	for _, issue := range report.Issues {
		got[issue.Entry+"|"+string(issue.Kind)] = issue.Kind
	}
	for _, key := range []string{
		"cat/good|duplicate", "cat/missing|unavailable", "cat/not-installed|not_installed",
		"cat/masked|masked", "cat/old|moved", "not-an-atom|invalid",
	} {
		if _, found := got[key]; !found {
			t.Fatalf("missing issue %s in %#v", key, report.Issues)
		}
	}
	want := []string{"cat/good", "cat/new"}
	if repaired := Apply(report.Entries, report.Actions); !reflect.DeepEqual(repaired, want) {
		t.Fatalf("repaired=%v, want %v; actions=%#v", repaired, want, report.Actions)
	}
}

func TestCheckMatchesCapturedUnavailableWorldState(t *testing.T) {
	root := t.TempDir()
	worldPath := filepath.Join(root, "world")
	vdbRoot := filepath.Join(root, "vdb")
	repoRoot := filepath.Join(root, "repo")
	entries := []string{"sys-fs/udev", "sys-power/powernowd", "x11-base/xorg-x11"}
	writeFixture(t, worldPath, strings.Join(entries, "\n")+"\n")
	for _, fixture := range []string{"sys-fs/udev-250", "sys-power/powernowd-1.00-r5", "x11-base/xorg-x11-7.4-r3"} {
		writeInstalled(t, vdbRoot, fixture, "0", "gentoo")
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, "metadata", "md5-cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	report, err := Check(worldPath, vdbRoot, repoRoot, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Issues) != 3 || len(report.Actions) != 3 {
		t.Fatalf("report=%#v", report)
	}
	for index, issue := range report.Issues {
		if issue.Entry != entries[index] || issue.Kind != Unavailable {
			t.Fatalf("issue[%d]=%#v, want unavailable %s", index, issue, entries[index])
		}
	}
}

func TestApplyIsDeterministicAndIdempotent(t *testing.T) {
	entries := []string{"cat/b", "cat/a", "cat/a", "cat/old"}
	actions := []Action{
		{Action: "deduplicate", Entry: "cat/a", Reason: Duplicate},
		{Action: "replace", Entry: "cat/old", Value: "cat/new", Reason: Moved},
	}
	first := Apply(entries, actions)
	second := Apply(first, actions)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repair is not idempotent: first=%v second=%v", first, second)
	}
	if want := []string{"cat/a", "cat/b", "cat/new"}; !reflect.DeepEqual(first, want) {
		t.Fatalf("repair=%v, want %v", first, want)
	}
}

func writeInstalled(t *testing.T, root, cpv, slot, repo string) {
	t.Helper()
	category, packageVersion, ok := strings.Cut(cpv, "/")
	if !ok {
		t.Fatalf("bad fixture CPV %q", cpv)
	}
	dir := filepath.Join(root, category, packageVersion)
	for name, value := range map[string]string{"CONTENTS": "obj /file 0 0\n", "EAPI": "8\n", "SLOT": slot + "\n", "repository": repo + "\n"} {
		writeFixture(t, filepath.Join(dir, name), value)
	}
}

func writeCache(t *testing.T, root, cpv, slot string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, "metadata", "md5-cache", cpv), "EAPI=8\nSLOT="+slot+"\nKEYWORDS=amd64\n")
}

func writeFixture(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}
