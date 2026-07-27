package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/fetch"
)

func TestConcurrentFetchProgressUsesCompleteLines(t *testing.T) {
	var output bytes.Buffer
	progress := newFetchProgress(true, &output)
	progress.terminal = true
	progress.setConcurrent(true)
	for _, event := range []fetch.Progress{
		{Stage: fetch.ProgressDownload, Artifact: "first.tar.xz", Source: "https://example/first.tar.xz", Downloaded: 5, Total: 10},
		{Stage: fetch.ProgressChecking, Artifact: "second.tar.xz"},
		{Stage: fetch.ProgressVerifying, Artifact: "first.tar.xz"},
		{Stage: fetch.ProgressComplete, Artifact: "first.tar.xz"},
	} {
		progress.Report(event)
	}
	got := output.String()
	if strings.Contains(got, "\r") {
		t.Fatalf("concurrent output contains carriage return: %q", got)
	}
	for _, want := range []string{
		">>> Downloading https://example/first.tar.xz\n",
		">>> Checking second.tar.xz\n",
		">>> Verifying first.tar.xz against Manifest\n",
		">>> Fetched and verified first.tar.xz\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("concurrent output %q lacks %q", got, want)
		}
	}
}

func TestTerminalProgressKeepsStatusBelowMessages(t *testing.T) {
	var output bytes.Buffer
	progress := &terminalProgress{output: true, terminal: true, writer: &output}
	progress.setStatus(">>> Jobs: 19 of 206 complete    Load avg: 4.28, 3.54, 2.02")
	progress.message(">>> Building package (20 of 206) dev-python/example-1")
	got := output.String()
	status := ">>> Jobs: 19 of 206 complete    Load avg: 4.28, 3.54, 2.02"
	if strings.Count(got, status) != 2 {
		t.Fatalf("status was not restored below message: %q", got)
	}
	if !strings.Contains(got, "\r\033[K>>> Building package (20 of 206) dev-python/example-1\n\r\033[K"+status) {
		t.Fatalf("message/status terminal ordering = %q", got)
	}
}

func TestNonTerminalProgressEmitsStatusAsCompleteLine(t *testing.T) {
	var output bytes.Buffer
	progress := &terminalProgress{output: true, writer: &output}
	progress.setStatus(">>> Jobs: 2 of 5 complete")
	if got, want := output.String(), ">>> Jobs: 2 of 5 complete\n"; got != want {
		t.Fatalf("non-terminal status = %q, want %q", got, want)
	}
}

func TestTerminalPackageProgressRewritesOneLine(t *testing.T) {
	var output bytes.Buffer
	progress := &terminalProgress{output: true, terminal: true, writer: &output, progressBucket: -1}
	progress.setStatus(">>> Jobs: 15 of 19 complete")
	progress.setProgress(">>> Installing package contents: 100/1000 entries (10.0%)", 100, 1000)
	progress.setProgress(">>> Installing package contents: 200/1000 entries (20.0%)", 200, 1000)
	progress.clearProgress()
	got := output.String()
	if strings.Count(got, "\n") != 0 {
		t.Fatalf("transient terminal progress emitted durable lines: %q", got)
	}
	if !strings.Contains(got, "\r\033[K>>> Installing package contents: 200/1000 entries (20.0%)") {
		t.Fatalf("latest transient progress was not rendered in place: %q", got)
	}
	if !strings.HasSuffix(got, "\r\033[K>>> Jobs: 15 of 19 complete") {
		t.Fatalf("clearing progress did not restore job status: %q", got)
	}
}

func TestTerminalPackageProgressUsesBoundedMilestones(t *testing.T) {
	var output bytes.Buffer
	progress := &terminalProgress{output: true, terminal: true, writer: &output, progressBucket: -1}
	for current := 1; current <= 1000; current++ {
		progress.setProgress(fmt.Sprintf("%d/1000 entries (%d%%)", current, current/10), current, 1000)
	}
	progress.setProgress("duplicate completion", 1000, 1000)

	got := output.String()
	if renders := strings.Count(got, "\r\033[K"); renders != 11 {
		t.Fatalf("terminal render count = %d, want 11 bounded milestones: %q", renders, got)
	}
	if strings.Contains(got, "duplicate completion") {
		t.Fatalf("duplicate completion was rendered: %q", got)
	}
	if !strings.Contains(got, "1000/1000 entries (100%)") {
		t.Fatalf("completion milestone is missing: %q", got)
	}
}

func TestNonTerminalPackageProgressUsesMilestones(t *testing.T) {
	var output bytes.Buffer
	progress := &terminalProgress{output: true, writer: &output, progressBucket: -1}
	for current := 1; current <= 100; current++ {
		progress.setProgress(fmt.Sprintf("%d/100", current), current, 100)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if got, want := len(lines), 11; got != want {
		t.Fatalf("milestone line count = %d, want %d: %q", got, want, output.String())
	}
	if lines[len(lines)-1] != "100/100" {
		t.Fatalf("final milestone = %q, want completion", lines[len(lines)-1])
	}
}

func TestNonAnimatedTerminalProgressHasNoBackgroundRedraw(t *testing.T) {
	var output bytes.Buffer
	progress := startTerminalProgressWriter("package transaction", true, false, true, &output)
	if progress.done != nil {
		t.Fatal("non-animated terminal progress started a redraw loop")
	}
	progress.setStatus(">>> Jobs: 0 of 1 complete")
	progress.setProgress(">>> Installing package contents (1 of 1) cat/pkg-1", 1, 1)
	progress.clearProgress()
	progress.stop()

	got := output.String()
	if strings.Count(got, "Installing package contents") != 1 {
		t.Fatalf("measured progress was redrawn without a state change: %q", got)
	}
	if strings.Count(got, ">>> Jobs: 0 of 1 complete") != 2 {
		t.Fatalf("status render count=%d want=2: %q", strings.Count(got, ">>> Jobs: 0 of 1 complete"), got)
	}
}

func TestFetchProgressCanShareTerminalMessageOwner(t *testing.T) {
	var output bytes.Buffer
	terminal := &terminalProgress{output: true, terminal: true, writer: &output}
	terminal.setStatus(">>> Jobs: 0 of 2 complete")
	fp := newFetchProgress(true, &output)
	fp.setConcurrent(true)
	fp.line = terminal.message
	fp.Report(fetch.Progress{Stage: fetch.ProgressChecking, Artifact: "source.tar.xz"})
	got := output.String()
	if !strings.Contains(got, "\r\033[K>>> Checking source.tar.xz\n\r\033[K>>> Jobs: 0 of 2 complete") {
		t.Fatalf("shared fetch/status ordering = %q", got)
	}
}
