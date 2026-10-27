package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/resolve"
)

func historyTestLog(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "emerge.log")
	data := `1700000000: >>> emerge (1 of 1) www-client/firefox-120::gentoo to /
1700000060: ::: completed emerge (1 of 1) www-client/firefox-120::gentoo to /
1700000100: >>> emerge (1 of 1) www-client/firefox-121::gentoo to /
1700000220: ::: completed emerge (1 of 1) www-client/firefox-121::gentoo to /
1700000300: === Unmerging... (cat/old-1)
1700000305: >>> unmerge success: cat/old-1
1700000400: >>> emerge (1 of 1) cat/unfinished-1 to /
`
	if err := os.WriteFile(file, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestHistoryFiltersSummariesAndJSON(t *testing.T) {
	log := historyTestLog(t)
	now := time.Unix(1700000500, 0)
	for _, test := range []struct {
		args                    []string
		matched, events, groups int
		contains                string
	}{
		{[]string{"list", "--limit", "1"}, 4, 1, 0, "cat/unfinished-1"},
		{[]string{"list", "--kind", "merge", "firefox"}, 2, 2, 0, "www-client/firefox"},
		{[]string{"list", "--since", "2023-11-14T22:15:00Z", "--until", "2023-11-14T22:19:00Z"}, 2, 2, 0, "cat/old-1"},
		{[]string{"stats", "www-client/*"}, 2, 0, 1, "90"},
		{[]string{"incomplete"}, 1, 1, 0, "incomplete"},
		{[]string{"list", "--kind", "sync"}, 0, 0, 0, "\"events\": []"},
	} {
		var out, stderr bytes.Buffer
		// Options precede package operands, as in the documented interface.
		args := append([]string{test.args[0], "--log", log, "--json", "--utc"}, test.args[1:]...)
		if code := runHistoryWithWriters(context.Background(), args, &out, &stderr, now); code != 0 {
			t.Fatalf("args=%v code=%d stderr=%s", args, code, &stderr)
		}
		var report historyDocument
		if !strings.Contains(out.String(), test.contains) {
			t.Fatalf("missing %q in %s", test.contains, &out)
		}
		decoder := json.NewDecoder(&out)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&report); err != nil {
			t.Fatal(err)
		}
		if report.Schema != "arise.history.v1" || report.Matched != test.matched || len(report.Events) != test.events || len(report.Statistics) != test.groups {
			t.Fatalf("args=%v report=%+v", args, report)
		}
		if report.Mode == "stats" && (report.Statistics[0].MedianSeconds != 90 || report.Statistics[0].Samples != 2) {
			t.Fatalf("summary=%+v", report.Statistics)
		}
	}
}

func TestHistoryPlainOutputAndMissingTiming(t *testing.T) {
	for _, mode := range []string{"list", "stats", "incomplete"} {
		var out, stderr bytes.Buffer
		if code := runHistoryWithWriters(context.Background(), []string{mode, "--log", historyTestLog(t), "--utc"}, &out, &stderr, time.Now()); code != 0 {
			t.Fatalf("code=%d: %s", code, &stderr)
		}
		if !strings.Contains(out.String(), "Dates: UTC") || strings.Contains(out.String(), "\r") {
			t.Fatalf("report=%s", &out)
		}
		if mode == "incomplete" && !strings.Contains(out.String(), "does not prove a process is still running") {
			t.Fatal("unfinished history was represented as live status")
		}
		if mode == "list" && !strings.Contains(out.String(), "unknown") {
			t.Fatal("missing duration was invented")
		}
	}
}

func TestHistoryInvalidArgumentsAndAtomicReadFailure(t *testing.T) {
	for _, args := range [][]string{{"--limit", "-1"}, {"--kind", "merge|unmerge"}, {"--since", "nonsense"}, {"--since", "2024-01-02", "--until", "2024-01-01"}, {"[invalid"}, {"--unknown"}} {
		var out, stderr bytes.Buffer
		if code := runHistoryWithWriters(context.Background(), args, &out, &stderr, time.Now()); code != 2 || out.Len() != 0 {
			t.Fatalf("invalid %v returned %d: %s", args, code, &out)
		}
	}
	var out, stderr bytes.Buffer
	args := []string{"--log", historyTestLog(t), "--log", filepath.Join(t.TempDir(), "absent")}
	if code := runHistoryWithWriters(context.Background(), args, &out, &stderr, time.Now()); code != 1 || out.Len() != 0 {
		t.Fatalf("partial report escaped read failure: %d %s", code, &out)
	}
	if code := runHistoryWithWriters(context.Background(), []string{"--help"}, io.Discard, io.Discard, time.Now()); code != 0 {
		t.Fatalf("help=%d", code)
	}
	if code := runHistoryWithWriters(context.Background(), []string{"--log", historyTestLog(t)}, historyBrokenWriter{}, io.Discard, time.Now()); code != 1 {
		t.Fatalf("write failure=%d", code)
	}
}

type historyBrokenWriter struct{}

func (historyBrokenWriter) Write([]byte) (int, error) {
	return 0, errors.New("injected output failure")
}

func TestHistoryDateBounds(t *testing.T) {
	location := time.FixedZone("PST", -8*3600)
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, location)
	for _, test := range []struct {
		value string
		end   bool
		want  time.Time
	}{{"today", false, time.Date(2026, 10, 27, 0, 0, 0, 0, location)}, {"yesterday", true, time.Date(2026, 10, 27, 0, 0, 0, 0, location)}, {"2026-10-27", true, time.Date(2026, 10, 28, 0, 0, 0, 0, location)}, {"7d", false, now.AddDate(0, 0, -7)}, {"2w", false, now.AddDate(0, 0, -14)}, {"12h", false, now.Add(-12 * time.Hour)}} {
		got, err := historyDate(test.value, now, location, test.end)
		if err != nil || !got.Equal(test.want) {
			t.Fatalf("%s=%v, %v; want %v", test.value, got, err, test.want)
		}
	}
}

func TestHistoryCalendarOffsetUsesSelectedTimezone(t *testing.T) {
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	got, err := historyDate("7d", now, location, false)
	want := now.In(location).AddDate(0, 0, -7)
	if err != nil || !got.Equal(want) {
		t.Fatalf("calendar offset across DST=%v, %v; want %v", got, err, want)
	}
}

func TestExecutionEstimateProgressAndOverrun(t *testing.T) {
	log := historyTestLog(t)
	action := resolve.PkgAction{Atom: planTestAtom(t, "www-client/firefox-122"), Action: "install", Repository: "gentoo"}
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	estimate := newExecutionEstimate([]resolve.PkgAction{action}, 1, 2, loadMergeEstimates(log), now)
	estimate.start(action, now)
	status := estimate.status(now.Add(30 * time.Second))
	for _, part := range []string{"0/1 complete", "1 running", "elapsed 30s", "~1m 00s left", "history 1/1"} {
		if !strings.Contains(status, part) {
			t.Errorf("missing %q in %s", part, status)
		}
	}
	if status := estimate.status(now.Add(100 * time.Second)); !strings.Contains(status, "ETA unknown (1 over estimate)") || strings.Contains(status, "~0s left") {
		t.Fatalf("overrun=%s", status)
	}
	estimate.complete(action)
	if status := estimate.status(now.Add(120 * time.Second)); !strings.Contains(status, "1/1 complete") || !strings.Contains(status, "finishing") {
		t.Fatalf("completion=%s", status)
	}
}

func TestPlanEstimatesDiscloseParallelismAndMissingHistory(t *testing.T) {
	estimates := loadMergeEstimates(historyTestLog(t))
	first := resolve.PkgAction{Atom: planTestAtom(t, "www-client/firefox-122"), Action: "install"}
	second := resolve.PkgAction{Atom: planTestAtom(t, "www-client/firefox-123"), Action: "install"}
	unknown := resolve.PkgAction{Atom: planTestAtom(t, "cat/unknown-1"), Action: "install"}
	for _, actions := range [][]resolve.PkgAction{{first, second}, {first, unknown}} {
		var out bytes.Buffer
		writePlanEstimates(&out, actions, 2, estimates)
		if !strings.Contains(out.String(), "~1m 30s elapsed at 2 job(s)") || !strings.Contains(out.String(), "package work") {
			t.Fatalf("parallel estimate=%s", &out)
		}
		if actions[1].Atom.CP() == "cat/unknown" && !strings.Contains(out.String(), "Partial historical estimate") {
			t.Fatal("missing history concealed")
		}
	}
	first.MergeType = "binary"
	if _, known := estimates.forAction(first); known {
		t.Fatal("source timing applied to a binary install")
	}
	var out bytes.Buffer
	progress := startTerminalProgressWriter("test", true, true, false, &out)
	stop := watchExecutionEstimate(progress, newExecutionEstimate(nil, 0, 1, estimates, time.Now()))
	stop()
	if out.Len() != 0 {
		t.Fatalf("redirected output got a periodic status: %q", out.String())
	}
	if got := historyText("repo\x1b[31m\r\n"); strings.ContainsAny(got, "\x1b\r\n") {
		t.Fatalf("terminal control escaped: %q", got)
	}
}

func TestEstimatesPreservePlanAndAuthorization(t *testing.T) {
	action := resolve.PkgAction{Atom: planTestAtom(t, "www-client/firefox-122"), Action: "install", UseFlags: map[string]bool{"test": false}}
	result := &resolve.ResolveResult{Install: []resolve.PkgAction{action}, Verified: true, Verification: resolve.VerificationVerified}
	before, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	digest := canonicalPlanSHA256([]string{"www-client/firefox"}, resolve.DefaultResolveConfig(), result, "state")
	estimates := loadMergeEstimates(historyTestLog(t))
	writePlanEstimates(io.Discard, result.Install, 2, estimates)
	tracker := newExecutionEstimate(result.Install, 1, 2, estimates, time.Now())
	tracker.start(action, time.Now())
	_ = tracker.status(time.Now())
	tracker.complete(action)
	after, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || canonicalPlanSHA256([]string{"www-client/firefox"}, resolve.DefaultResolveConfig(), result, "state") != digest {
		t.Fatal("advisory estimates changed the executable plan or its authorization")
	}
}

func TestExecutionEstimateConcurrentUpdates(t *testing.T) {
	actions := make([]resolve.PkgAction, 8)
	for i := range actions {
		actions[i] = resolve.PkgAction{Atom: planTestAtom(t, "cat/pkg-"+string(rune('1'+i))), Action: "install"}
	}
	tracker := newExecutionEstimate(actions, len(actions), 4, mergeEstimates{}, time.Now())
	var workers sync.WaitGroup
	for _, action := range actions {
		workers.Go(func() { tracker.start(action, time.Now()); _ = tracker.status(time.Now()); tracker.complete(action) })
	}
	workers.Wait()
	if status := tracker.status(time.Now()); !strings.Contains(status, "8/8 complete") || !strings.Contains(status, "0 running") {
		t.Fatalf("concurrent status=%s", status)
	}
}
