package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBugReportRouteExists(t *testing.T) {
	command, args := selectCommand([]string{"bug-report", "--output", "report"})
	if command != "bug-report" || !reflect.DeepEqual(args, []string{"--output", "report"}) {
		t.Fatalf("route=%q %#v", command, args)
	}
}

func TestBugReportCommandExistsAndWritesReviewableArtifacts(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "report")
	oldResume, oldJournal := *resumeFile, *journalDir
	*resumeFile, *journalDir = filepath.Join(root, "missing-resume"), filepath.Join(root, "missing-journals")
	defer func() { *resumeFile, *journalDir = oldResume, oldJournal }()
	t.Setenv("ROOT", root)
	t.Setenv("PORTAGE_TMPDIR", root)
	t.Setenv("DISTDIR", root)
	if code := runBugReport([]string{"--output", output, "--latest-failure=false", "--package", "cat/pkg"}); code != 0 {
		t.Fatalf("bug-report exit=%d", code)
	}
	for _, name := range []string{"report.json", "report.md"} {
		data, err := os.ReadFile(filepath.Join(output, name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "cat/pkg") {
			t.Fatalf("%s does not contain selection: %s", name, data)
		}
	}
}

func TestBugReportRejectsUnknownArgumentsWithoutMutation(t *testing.T) {
	output := filepath.Join(t.TempDir(), "report")
	if code := runBugReport([]string{"--output", output, "--unknown"}); code != 2 {
		t.Fatalf("invalid options exit=%d", code)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("invalid invocation created output: %v", err)
	}
}
