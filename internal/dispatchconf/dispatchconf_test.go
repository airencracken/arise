package dispatchconf

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
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
	opts.DiffCommand = "true %s %s"
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
	if _, err := os.Stat(oldCandidate); err != nil {
		t.Fatalf("read-only discovery removed superseded candidate: %v", err)
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
		if first[i-1].New > first[i].New {
			t.Fatalf("not sorted: %#v", first)
		}
	}
}

func TestDiscoverMatchesPortageCandidatePathOrdering(t *testing.T) {
	root, opts := fixtureOptions(t)
	for _, fixture := range []struct {
		name     string
		sequence string
	}{
		{name: "alpha", sequence: "0009"},
		{name: "zulu", sequence: "0000"},
	} {
		writeFixture(t, filepath.Join(root, "etc", fixture.name), "old", 0o644)
		writeFixture(t, filepath.Join(root, "etc", "._cfg"+fixture.sequence+"_"+fixture.name), "new", 0o644)
	}
	got, err := Discover(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || filepath.Base(got[0].Current) != "zulu" || filepath.Base(got[1].Current) != "alpha" {
		t.Fatalf("candidate order = %#v, want Portage path order zulu then alpha", got)
	}
}

func TestRunClearsScreenBeforeEachInteractiveComparison(t *testing.T) {
	root, opts := fixtureOptions(t)
	writeFixture(t, filepath.Join(root, "etc/value"), "old", 0o644)
	writeFixture(t, filepath.Join(root, "etc/._cfg0000_value"), "new", 0o644)
	opts.ClearScreen = true
	opts.Input = strings.NewReader("hn")

	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	got := opts.Output.(*bytes.Buffer).String()
	if count := strings.Count(got, "\033[H\033[2J"); count != 2 {
		t.Fatalf("screen clear count = %d, want one before each comparison: %q", count, got)
	}
	if !strings.HasPrefix(got, "\033[H\033[2J") {
		t.Fatalf("first comparison was not preceded by a clear: %q", got)
	}
}

func TestColorDiffCommandOnlyEnhancesPlainDiff(t *testing.T) {
	tests := []struct {
		name    string
		parts   []string
		enabled bool
		want    []string
	}{
		{name: "enabled", parts: []string{"diff", "-Nu", "old", "new"}, enabled: true, want: []string{"diff", "--color=always", "-Nu", "old", "new"}},
		{name: "disabled", parts: []string{"diff", "-Nu"}, want: []string{"diff", "-Nu"}},
		{name: "configured", parts: []string{"diff", "--color=auto", "-Nu"}, enabled: true, want: []string{"diff", "--color=auto", "-Nu"}},
		{name: "custom", parts: []string{"colordiff", "-Nu"}, enabled: true, want: []string{"colordiff", "-Nu"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := colorDiffCommand(test.parts, test.enabled); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("colorDiffCommand() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestShowDiffColorizesGNUDiffOutputWhenEnabled(t *testing.T) {
	diff, err := exec.LookPath("diff")
	if err != nil {
		t.Skip("diff is unavailable")
	}
	if output, err := exec.Command(diff, "--help").CombinedOutput(); err != nil ||
		!strings.Contains(string(output), "--color") {
		t.Skip("installed diff does not support color")
	}
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old")
	newPath := filepath.Join(dir, "new")
	writeFixture(t, oldPath, "old\n", 0o644)
	writeFixture(t, newPath, "new\n", 0o644)
	var output bytes.Buffer
	opts := Options{
		DiffCommand: diff + " -Nu %s %s",
		Color:       true,
		Output:      &output,
		Error:       &output,
	}
	if err := showDiff(context.Background(), opts, oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "\033[") {
		t.Fatalf("color-enabled GNU diff output lacks ANSI styling: %q", output.String())
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
	outside := filepath.Join(filepath.Dir(root), "outside")
	escape := Candidate{Current: filepath.Join(outside, "value"), New: filepath.Join(outside, "._cfg0000_value")}
	writeFixture(t, escape.Current, "old", 0o644)
	writeFixture(t, escape.New, "new", 0o644)
	if err := archive(escape, opts); err == nil || !strings.Contains(err.Error(), "escapes root") {
		t.Fatalf("archive escape error = %v", err)
	}
}

func TestArchiveRejectsSymlinkAncestorEscape(t *testing.T) {
	root, opts := fixtureOptions(t)
	current := filepath.Join(root, "etc/sub/value")
	candidate := Candidate{Current: current, New: filepath.Join(root, "etc/sub/._cfg0000_value")}
	writeFixture(t, current, "old", 0o644)
	writeFixture(t, candidate.New, "new", 0o644)
	outside := t.TempDir()
	if err := os.MkdirAll(opts.ArchiveDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(opts.ArchiveDir, "etc")); err != nil {
		t.Fatal(err)
	}
	if err := archive(candidate, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "sub/value")); !os.IsNotExist(err) {
		t.Fatalf("archive escaped through symlink: %v", err)
	}
	if info, err := os.Lstat(filepath.Join(opts.ArchiveDir, "etc")); err != nil || !info.IsDir() {
		t.Fatalf("unsafe ancestor was not replaced by a directory: %v %v", info, err)
	}
}

func TestAdversarialRejectsProtectedPathTraversal(t *testing.T) {
	root, opts := fixtureOptions(t)
	opts.Protect = []string{"../../etc"}
	if _, err := Discover(opts); err == nil || !strings.Contains(err.Error(), "escapes root") {
		t.Fatalf("protected path traversal error = %v", err)
	}
	_ = root
}

func TestArchiveRotationPreservesDirectoryAtMaximumSuffix(t *testing.T) {
	root, opts := fixtureOptions(t)
	current := filepath.Join(root, "etc/value")
	candidate := Candidate{Current: current, New: filepath.Join(root, "etc/._cfg0000_value")}
	writeFixture(t, current, "current", 0o644)
	writeFixture(t, candidate.New, "new", 0o644)
	archiveFile, err := archivePath(candidate, opts)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, archiveFile, "previous", 0o600)
	for i := 1; i < 9; i++ {
		writeFixture(t, archiveFile+"."+strconv.Itoa(i), "old", 0o600)
	}
	if err := os.MkdirAll(archiveFile+".9/keep", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := archive(candidate, opts); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(archiveFile), "."+filepath.Base(archiveFile)+".9.displaced-*"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("preserved directory matches = %v, %v", matches, err)
	}
	if _, err := os.Stat(filepath.Join(matches[0], "keep")); err != nil {
		t.Fatalf("maximum-suffix directory contents lost: %v", err)
	}
}

func TestRunMissingCurrentAndSymlinkCandidates(t *testing.T) {
	root, opts := fixtureOptions(t)
	missing := filepath.Join(root, "etc/new.conf")
	writeFixture(t, filepath.Join(root, "etc/._cfg0000_new.conf"), "created\n", 0o640)
	opts.Input = strings.NewReader("u")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, missing, "created\n")

	link := filepath.Join(root, "etc/link")
	candidate := filepath.Join(root, "etc/._cfg0000_link")
	if err := os.Symlink("old-target", link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("new-target", candidate); err != nil {
		t.Fatal(err)
	}
	opts.Input = strings.NewReader("u")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(link); err != nil || target != "new-target" {
		t.Fatalf("updated symlink = %q, %v", target, err)
	}
}

func TestRunRejectsUnsupportedCandidateTypeWithoutMutation(t *testing.T) {
	root, opts := fixtureOptions(t)
	current := filepath.Join(root, "etc/value")
	candidate := filepath.Join(root, "etc/._cfg0000_value")
	writeFixture(t, current, "old", 0o644)
	if err := syscall.Mkfifo(candidate, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "unsupported object type") {
		t.Fatalf("unsupported candidate error = %v", err)
	}
	assertFileContent(t, current, "old")
}

func TestArchivePublicationLeavesNoTemporaryFiles(t *testing.T) {
	root, opts := fixtureOptions(t)
	current := filepath.Join(root, "etc/value")
	candidate := Candidate{Current: current, New: filepath.Join(root, "etc/._cfg0000_value")}
	writeFixture(t, current, strings.Repeat("current\n", 1024), 0o600)
	writeFixture(t, candidate.New, strings.Repeat("new\n", 1024), 0o640)
	if err := archive(candidate, opts); err != nil {
		t.Fatal(err)
	}
	var temporary []string
	if err := filepath.WalkDir(opts.ArchiveDir, func(path string, entry os.DirEntry, err error) error {
		if err == nil && strings.Contains(entry.Name(), ".tmp-") {
			temporary = append(temporary, path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(temporary) != 0 {
		t.Fatalf("archive temporary files remain: %v", temporary)
	}
}

func TestRunUsesNonConflictingThreeWayMerge(t *testing.T) {
	root, opts := fixtureOptions(t)
	current := filepath.Join(root, "etc/value")
	candidate := filepath.Join(root, "etc/._cfg0000_value")
	writeFixture(t, current, "a=local\nseparator=true\nb=base\n", 0o600)
	writeFixture(t, candidate, "a=base\nseparator=true\nb=new\n", 0o640)
	archive := filepath.Join(opts.ArchiveDir, "etc/value")
	writeFixture(t, archive+".dist", "a=base\nseparator=true\nb=base\n", 0o600)
	opts.Input = strings.NewReader("u")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, current, "a=local\nseparator=true\nb=new\n")
}

func TestRunReportsDiffAndPostSessionHookFailures(t *testing.T) {
	t.Run("diff", func(t *testing.T) {
		root, opts := fixtureOptions(t)
		current := filepath.Join(root, "etc/value")
		candidate := filepath.Join(root, "etc/._cfg0000_value")
		writeFixture(t, current, "old", 0o644)
		writeFixture(t, candidate, "new", 0o644)
		opts.DiffCommand = "sh -c 'exit 2' ignored %s %s"
		if _, err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "show diff") {
			t.Fatalf("diff failure = %v", err)
		}
		assertFileContent(t, current, "old")
		assertFileContent(t, candidate, "new")
	})
	t.Run("post-session", func(t *testing.T) {
		root, opts := fixtureOptions(t)
		hook := filepath.Join(opts.HookDir, "10-fail")
		writeFixture(t, hook, "#!/bin/sh\n[ \"$1\" != post-session ]\n", 0o755)
		if _, err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "post-session") {
			t.Fatalf("post-session failure = %v", err)
		}
		_ = root
	})
	t.Run("post-update-committed", func(t *testing.T) {
		root, opts := fixtureOptions(t)
		current := filepath.Join(root, "etc/value")
		candidate := filepath.Join(root, "etc/._cfg0000_value")
		writeFixture(t, current, "old", 0o644)
		writeFixture(t, candidate, "new", 0o644)
		hook := filepath.Join(opts.HookDir, "10-fail")
		writeFixture(t, hook, "#!/bin/sh\n[ \"$1\" != post-update ]\n", 0o755)
		opts.Input = strings.NewReader("u")
		if _, err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "updated config committed") {
			t.Fatalf("post-update failure = %v", err)
		}
		assertFileContent(t, current, "new")
	})
}

func TestCommandTemplatesAndQuotedEditorArguments(t *testing.T) {
	if _, err := commandParts("diff %s", "old", "new"); err == nil {
		t.Fatal("missing command placeholder accepted")
	}
	if _, err := commandParts("diff %s %s %s", "old", "new"); err == nil {
		t.Fatal("extra command placeholder accepted")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	editor := filepath.Join(dir, "editor")
	writeFixture(t, editor, "#!/bin/sh\nprintf '%s\\n' \"$@\" > \""+log+"\"\n", 0o755)
	if err := runEditor(context.Background(), editor+" --label 'two words'", "/target"); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, log, "--label\ntwo words\n/target\n")
}

func TestInvalidCommandConfigurationFailsBeforeHooksOrMutation(t *testing.T) {
	root, opts := fixtureOptions(t)
	current := filepath.Join(root, "etc/value")
	candidate := filepath.Join(root, "etc/._cfg0000_value")
	writeFixture(t, current, "old", 0o644)
	writeFixture(t, candidate, "new", 0o644)
	hookLog := filepath.Join(root, "hook.log")
	writeFixture(t, filepath.Join(opts.HookDir, "10-record"), "#!/bin/sh\nprintf called > \""+hookLog+"\"\n", 0o755)
	opts.DiffCommand = "diff %s"
	if _, err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "invalid diff command") {
		t.Fatalf("invalid command error = %v", err)
	}
	if _, err := os.Stat(hookLog); !os.IsNotExist(err) {
		t.Fatalf("hook ran before command validation: %v", err)
	}
	assertFileContent(t, current, "old")
	assertFileContent(t, candidate, "new")
}

func TestMixedDiffOperandsRepresentMissingSymlinkAndFIFO(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	link := filepath.Join(dir, "link")
	if err := os.Symlink("target", link); err != nil {
		t.Fatal(err)
	}
	left, right, cleanup, err := mixedDiffOperands(missing, link)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	assertFileContent(t, left, "/dev/null\n")
	assertFileContent(t, right, "SYM: "+link+" -> target\n")

	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	rendered, _, cleanupFIFO, err := mixedDiffOperands(fifo, missing)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupFIFO()
	assertFileContent(t, rendered, "FIF: "+fifo+"\n")
}

func TestMergeOutputPublishesAtomically(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "._mrg0000_value")
	writeFixture(t, output, "previous", 0o600)
	failing := `sh -c 'printf partial > "$1"; exit 2' ignored %s %s %s`
	if err := runMerge(context.Background(), failing, output, "/old", "/new"); err == nil {
		t.Fatal("failing merge returned success")
	}
	assertFileContent(t, output, "previous")
	matches, err := filepath.Glob(filepath.Join(dir, ".*.tmp-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("merge temporary files = %v, %v", matches, err)
	}
	success := `sh -c 'printf merged > "$1"' ignored %s %s %s`
	if err := runMerge(context.Background(), success, output, "/old", "/new"); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, output, "merged")
}

func TestCancellationAndSkipPreservePendingCandidates(t *testing.T) {
	root, opts := fixtureOptions(t)
	oldCandidate := filepath.Join(root, "etc/._cfg0000_value")
	newCandidate := filepath.Join(root, "etc/._cfg0001_value")
	writeFixture(t, filepath.Join(root, "etc/value"), "old", 0o644)
	writeFixture(t, oldCandidate, "older", 0o644)
	writeFixture(t, newCandidate, "new", 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, opts); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	for _, path := range []string{oldCandidate, newCandidate} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("cancellation removed %s: %v", path, err)
		}
	}
	opts.Input = strings.NewReader("n")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{oldCandidate, newCandidate} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("skip removed %s: %v", path, err)
		}
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
