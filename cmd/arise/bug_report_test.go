package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/resolve"
	"github.com/airencracken/arise/internal/resolvertrace"
)

func TestBugReportRouteExists(t *testing.T) {
	command, args := selectCommand([]string{"bug-report", "--output", "report"})
	if command != "bug-report" || !reflect.DeepEqual(args, []string{"--output", "report"}) {
		t.Fatalf("route=%q %#v", command, args)
	}
}

func TestBugReportEmbedsAndRedactsImportedResolverTrace(t *testing.T) {
	root := t.TempDir()
	tracePath, output := filepath.Join(root, "trace.json"), filepath.Join(root, "report")
	private := filepath.Join(os.Getenv("HOME"), "private-customer-path")
	trace := resolvertrace.Trace{Schema: resolvertrace.SchemaVersion, Targets: []string{private}, Backtracks: []resolve.BacktrackDecision{}, Branches: []resolve.BranchEvaluation{}, Candidates: resolve.DecisionLedger{Records: []resolve.CandidateDecision{}}, Conflicts: []string{"token=hunter2"}, Warnings: []string{}}
	var encoded bytes.Buffer
	if err := resolvertrace.Encode(&encoded, trace); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracePath, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	oldResume, oldJournal := *resumeFile, *journalDir
	*resumeFile, *journalDir = filepath.Join(root, "missing-resume"), filepath.Join(root, "missing-journals")
	defer func() { *resumeFile, *journalDir = oldResume, oldJournal }()
	t.Setenv("ROOT", root)
	t.Setenv("PORTAGE_TMPDIR", root)
	t.Setenv("DISTDIR", root)
	if code := runBugReport([]string{"--output", output, "--latest-failure=false", "--resolver-trace", tracePath}); code != 0 {
		t.Fatalf("bug-report exit=%d", code)
	}
	jsonData, err := os.ReadFile(filepath.Join(output, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	markdown, err := os.ReadFile(filepath.Join(output, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	artifacts := string(jsonData) + string(markdown)
	if !strings.Contains(artifacts, "resolver_trace") || !strings.Contains(artifacts, "Resolver trace") {
		t.Fatalf("trace disclosure missing: %s", artifacts)
	}
	for _, secret := range []string{private, "hunter2"} {
		if strings.Contains(artifacts, secret) {
			t.Fatalf("embedded trace leaked %q", secret)
		}
	}
}

func TestBugReportRejectsInvalidResolverTraceBeforePublishing(t *testing.T) {
	root := t.TempDir()
	tracePath, output := filepath.Join(root, "trace.json"), filepath.Join(root, "report")
	if err := os.WriteFile(tracePath, []byte(`{"schema":1,"surprise":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runBugReport([]string{"--output", output, "--latest-failure=false", "--resolver-trace", tracePath}); code != 1 {
		t.Fatalf("invalid trace exit=%d", code)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("invalid trace published report: %v", err)
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
