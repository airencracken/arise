package dispatchconf

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func fixtureOptions(t *testing.T) (string, Options) {
	t.Helper()
	root := t.TempDir()
	opts := DefaultOptions()
	opts.Root = root
	opts.Protect = []string{"/etc"}
	opts.ArchiveDir = filepath.Join(root, "archive")
	opts.HookDir = filepath.Join(root, "hooks")
	opts.DiffCommand = "true"
	opts.Input = strings.NewReader("")
	opts.Output = &bytes.Buffer{}
	opts.Error = &bytes.Buffer{}
	return root, opts
}

func TestDiscoverRecursiveNewestStableAndPrunesHiddenDirectories(t *testing.T) {
	root, opts := fixtureOptions(t)
	writeFixture(t, filepath.Join(root, "etc/a"), "old", 0o640)
	oldCandidate := filepath.Join(root, "etc/._cfg0001_a")
	writeFixture(t, oldCandidate, "older", 0o640)
	newCandidate := filepath.Join(root, "etc/._cfg0002_a")
	writeFixture(t, newCandidate, "new", 0o640)
	writeFixture(t, filepath.Join(root, "etc/sub/b"), "old", 0o644)
	writeFixture(t, filepath.Join(root, "etc/sub/._cfg0000_b"), "new", 0o644)
	writeFixture(t, filepath.Join(root, "etc/.git/._cfg9999_ignored"), "bad", 0o644)

	got, err := Discover(opts)
	if err != nil {
		t.Fatal(err)
	}
	want := []Candidate{
		{Current: filepath.Join(root, "etc/a"), New: newCandidate, Sequence: 2},
		{Current: filepath.Join(root, "etc/sub/b"), New: filepath.Join(root, "etc/sub/._cfg0000_b"), Sequence: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Discover() = %#v, want %#v", got, want)
	}
	if _, err := os.Stat(oldCandidate); !os.IsNotExist(err) {
		t.Fatalf("superseded candidate was not removed: %v", err)
	}
}

func TestDiscoverExplicitFileAndMask(t *testing.T) {
	root, opts := fixtureOptions(t)
	opts.Protect = []string{"/etc/specific"}
	opts.Mask = []string{"/etc"}
	writeFixture(t, filepath.Join(root, "etc/specific"), "old", 0o644)
	writeFixture(t, filepath.Join(root, "etc/._cfg0000_specific"), "new", 0o644)
	writeFixture(t, filepath.Join(root, "etc/._cfg0000_other"), "other", 0o644)
	got, err := Discover(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Masked || filepath.Base(got[0].Current) != "specific" {
		t.Fatalf("unexpected candidates: %#v", got)
	}
}

func TestRunUpdateArchivesAtomicallyPreservesNewModeAndRunsHooks(t *testing.T) {
	root, opts := fixtureOptions(t)
	current := filepath.Join(root, "etc/service.conf")
	candidate := filepath.Join(root, "etc/._cfg0000_service.conf")
	writeFixture(t, current, "old\n", 0o600)
	writeFixture(t, candidate, "new\n", 0o640)
	hookLog := filepath.Join(root, "hook.log")
	hook := filepath.Join(opts.HookDir, "10-record")
	writeFixture(t, hook, "#!/bin/sh\nprintf '%s %s\\n' \"$1\" \"$2\" >> \""+hookLog+"\"\n", 0o755)
	opts.Input = strings.NewReader("u")

	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated != 1 || result.Discovered != 1 {
		t.Fatalf("result = %#v", result)
	}
	data, err := os.ReadFile(current)
	if err != nil || string(data) != "new\n" {
		t.Fatalf("current = %q, %v", data, err)
	}
	info, err := os.Stat(current)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, %v", info.Mode().Perm(), err)
	}
	archive := filepath.Join(opts.ArchiveDir, "etc/service.conf")
	assertFileContent(t, archive, "old\n")
	assertFileContent(t, archive+".dist", "new\n")
	hooks, err := os.ReadFile(hookLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"pre-session ", "pre-update " + current, "post-update " + current, "post-session "} {
		if !strings.Contains(string(hooks), expected) {
			t.Fatalf("hook log %q lacks %q", hooks, expected)
		}
	}
}

func TestRunMaskedUpdateAndIdenticalZapAreAutomatic(t *testing.T) {
	root, opts := fixtureOptions(t)
	opts.Mask = []string{"/etc/masked"}
	writeFixture(t, filepath.Join(root, "etc/masked/value"), "old", 0o644)
	writeFixture(t, filepath.Join(root, "etc/masked/._cfg0000_value"), "new", 0o644)
	writeFixture(t, filepath.Join(root, "etc/same"), "same", 0o644)
	writeFixture(t, filepath.Join(root, "etc/._cfg0000_same"), "same", 0o644)

	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Automatic != 2 || result.Updated != 1 || result.Zapped != 1 {
		t.Fatalf("result = %#v", result)
	}
	assertFileContent(t, filepath.Join(root, "etc/masked/value"), "new")
	if _, err := os.Stat(filepath.Join(root, "etc/._cfg0000_same")); !os.IsNotExist(err) {
		t.Fatalf("identical candidate remains: %v", err)
	}
}

func TestRunSkipThenQuitLeavesCandidates(t *testing.T) {
	root, opts := fixtureOptions(t)
	for _, name := range []string{"a", "b"} {
		writeFixture(t, filepath.Join(root, "etc", name), "old", 0o644)
		writeFixture(t, filepath.Join(root, "etc", "._cfg0000_"+name), "new", 0o644)
	}
	opts.Input = strings.NewReader("nq")
	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Skipped != 1 || !result.Quit {
		t.Fatalf("result = %#v", result)
	}
	for _, name := range []string{"a", "b"} {
		if _, err := os.Stat(filepath.Join(root, "etc", "._cfg0000_"+name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestArchiveRotation(t *testing.T) {
	root, opts := fixtureOptions(t)
	current := filepath.Join(root, "etc/value")
	candidate := filepath.Join(root, "etc/._cfg0000_value")
	writeFixture(t, current, "current", 0o644)
	writeFixture(t, candidate, "new", 0o644)
	archive := filepath.Join(opts.ArchiveDir, "etc/value")
	writeFixture(t, archive, "previous", 0o600)
	writeFixture(t, archive+".1", "oldest", 0o600)
	opts.Input = strings.NewReader("n")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, archive, "current")
	assertFileContent(t, archive+".1", "previous")
	assertFileContent(t, archive+".2", "oldest")
}

func TestPropertyDiscoverOrderingIsDeterministic(t *testing.T) {
	root, opts := fixtureOptions(t)
	for _, name := range []string{"z", "a", "middle", "00"} {
		writeFixture(t, filepath.Join(root, "etc", name), "old", 0o644)
		writeFixture(t, filepath.Join(root, "etc", "._cfg0000_"+name), "new", 0o644)
	}
	first, err := Discover(opts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Discover(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("nondeterministic results: %#v != %#v", first, second)
	}
	for i := 1; i < len(first); i++ {
		if first[i-1].Current > first[i].Current {
			t.Fatalf("not sorted: %#v", first)
		}
	}
}

func TestAdversarialRejectsRelativeRootAndArchiveEscape(t *testing.T) {
	_, err := Discover(Options{Root: "relative", Protect: []string{"/etc"}})
	if err == nil {
		t.Fatal("relative root accepted")
	}
	root, opts := fixtureOptions(t)
	candidate := Candidate{Current: filepath.Join(root, "etc/value"), New: filepath.Join(root, "etc/._cfg0000_value")}
	writeFixture(t, candidate.Current, "old", 0o644)
	writeFixture(t, candidate.New, "new", 0o644)
	opts.ArchiveDir = filepath.Join(root, "archive", "..", "archive")
	if err := archive(candidate, opts); err != nil {
		t.Fatalf("clean archive path rejected: %v", err)
	}
}

func TestSchemaCandidateJSONFieldStability(t *testing.T) {
	candidateType := reflect.TypeOf(Candidate{})
	want := map[string]string{"Current": "current", "New": "new", "Sequence": "sequence", "Masked": "masked"}
	for field, jsonName := range want {
		got, ok := candidateType.FieldByName(field)
		if !ok || got.Tag.Get("json") != jsonName {
			t.Fatalf("%s JSON tag = %q, want %q", field, got.Tag.Get("json"), jsonName)
		}
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}
