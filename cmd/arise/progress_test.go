package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/fetch"
)

func TestConcurrentFetchProgressUsesCompleteLines(t *testing.T) {
	var output bytes.Buffer
	progress := newFetchProgress(true, true, &output)
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

func TestConciseFetchProgressSuppressesSuccessfulArtifactChatter(t *testing.T) {
	var output bytes.Buffer
	progress := newFetchProgress(true, false, &output)
	for _, event := range []fetch.Progress{
		{Stage: fetch.ProgressChecking, Artifact: "tinyvec-1.10.0.crate"},
		{Stage: fetch.ProgressDownload, Artifact: "tinyvec-1.10.0.crate", Source: "https://distfiles.example/tinyvec-1.10.0.crate"},
		{Stage: fetch.ProgressVerifying, Artifact: "tinyvec-1.10.0.crate"},
		{Stage: fetch.ProgressComplete, Artifact: "tinyvec-1.10.0.crate"},
	} {
		progress.Report(event)
	}
	if output.Len() != 0 {
		t.Fatalf("concise fetch output leaked artifact events: %q", output.String())
	}
}

func TestVerboseFetchProgressUsesColorWithoutChangingText(t *testing.T) {
	previous := color.UseColor
	t.Cleanup(func() { color.UseColor = previous })
	events := []fetch.Progress{
		{Stage: fetch.ProgressChecking, Artifact: "source.tar.xz"},
		{Stage: fetch.ProgressComplete, Artifact: "source.tar.xz"},
	}
	var plain bytes.Buffer
	color.UseColor = false
	plainProgress := newFetchProgress(true, true, &plain)
	for _, event := range events {
		plainProgress.Report(event)
	}
	var colored bytes.Buffer
	color.UseColor = true
	coloredProgress := newFetchProgress(true, true, &colored)
	for _, event := range events {
		coloredProgress.Report(event)
	}
	if !strings.Contains(colored.String(), "\x1b[") {
		t.Fatalf("verbose fetch output contains no ANSI styling: %q", colored.String())
	}
	if got := stripANSIForProgressTest(colored.String()); got != plain.String() {
		t.Fatalf("color changed fetch text: colored=%q plain=%q", got, plain.String())
	}
}

func stripANSIForProgressTest(value string) string {
	for {
		start := strings.Index(value, "\x1b[")
		if start < 0 {
			return value
		}
		end := strings.IndexByte(value[start:], 'm')
		if end < 0 {
			return value[:start]
		}
		value = value[:start] + value[start+end+1:]
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
	if !strings.Contains(got, "\x1b[1G\x1b[K>>> Building package (20 of 206) dev-python/example-1\n\x1b[1G\x1b[K"+status) {
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
	progress.stage("", "")
	got := output.String()
	if strings.Count(got, "\n") != 0 {
		t.Fatalf("transient terminal progress emitted durable lines: %q", got)
	}
	if !strings.Contains(got, "\x1b[1G\x1b[K>>> Installing package contents: 200/1000 entries (20.0%)") {
		t.Fatalf("latest transient progress was not rendered in place: %q", got)
	}
	if !strings.HasSuffix(got, "\x1b[1G\x1b[K>>> Jobs: 15 of 19 complete") {
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
	if renders := strings.Count(got, "\x1b[1G\x1b[K"); renders != 11 {
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

func TestConcurrentTerminalPackageProgressUsesDurableCompletionLines(t *testing.T) {
	var output bytes.Buffer
	progress := &terminalProgress{
		output: true, terminal: true, writer: &output, progressBucket: -1,
		status: ">>> Jobs: 0 of 2 complete", displayed: true,
	}
	progress.setConcurrent(true)
	progress.setProgress("first partial", 4, 10)
	progress.setProgress("first complete", 10, 10)
	for range 20 {
		progress.setProgress("first complete", 10, 10)
	}
	progress.message(">>> Syncing package contents (1 of 2) cat/first-1")

	got := output.String()
	if strings.Contains(got, "first partial") {
		t.Fatalf("concurrent partial progress was rendered: %q", got)
	}
	if strings.Count(got, "first complete\n") != 1 {
		t.Fatalf("concurrent completion was not one durable line: %q", got)
	}
	if !strings.Contains(got, "\x1b[1G\x1b[Kfirst complete\n") {
		t.Fatalf("completion did not clear the shared terminal line first: %q", got)
	}
	if !strings.Contains(got, "\x1b[1G\x1b[K>>> Syncing package contents (1 of 2) cat/first-1\n") {
		t.Fatalf("next stage did not start on a clean durable line: %q", got)
	}
}

func TestConcurrentNonTerminalPackageProgressDeduplicatesCompletionCallbacks(t *testing.T) {
	var output bytes.Buffer
	progress := &terminalProgress{output: true, writer: &output, progressBucket: -1}
	progress.setConcurrent(true)
	message := ">>> Installing package contents: 33/33 entries (100%) (1 of 3) dev-python/tomli-2.4.1:0"
	for range 100 {
		progress.setProgress(message, 33, 33)
	}
	progress.message(">>> Syncing package contents (1 of 3) dev-python/tomli-2.4.1:0::gentoo")
	if got := output.String(); got != message+"\n>>> Syncing package contents (1 of 3) dev-python/tomli-2.4.1:0::gentoo\n" {
		t.Fatalf("deduplicated progress = %q", got)
	}
}

func TestNonAnimatedTerminalProgressHasNoBackgroundRedraw(t *testing.T) {
	var output bytes.Buffer
	progress := startTerminalProgressWriter("package transaction", true, false, true, &output)
	if output.Len() != 0 {
		t.Fatalf("non-animated terminal progress rendered without an event: %q", output.String())
	}
	progress.setStatus(">>> Jobs: 0 of 1 complete")
	progress.setProgress(">>> Installing package contents (1 of 1) cat/pkg-1", 1, 1)
	progress.stage("", "")
	progress.stop()

	got := output.String()
	if strings.Count(got, "Installing package contents") != 1 {
		t.Fatalf("measured progress was redrawn without a state change: %q", got)
	}
	if strings.Count(got, ">>> Jobs: 0 of 1 complete") != 2 {
		t.Fatalf("status render count=%d want=2: %q", strings.Count(got, ">>> Jobs: 0 of 1 complete"), got)
	}
}

func TestAnimatedTerminalProgressDoesNotRedrawUnchangedMeasuredProgress(t *testing.T) {
	var output bytes.Buffer
	progress := &terminalProgress{
		output: true, terminal: true, animate: true, writer: &output,
		progressBucket: -1,
	}
	message := ">>> Installing package contents: 110/110 entries (100%) (2 of 14) dev-go/gopls-0.22.0:0"
	progress.setProgress(message, 110, 110)
	for range 100 {
		progress.setProgress(message, 110, 110)
	}
	progress.message(">>> Syncing package contents (2 of 14) dev-go/gopls-0.22.0:0::gentoo")

	got := output.String()
	if count := strings.Count(got, message); count != 1 {
		t.Fatalf("unchanged completion rendered %d times, want 1: %q", count, got)
	}
	if !strings.Contains(got, "\x1b[1G\x1b[K"+message+"\x1b[1G\x1b[K>>> Syncing package contents (2 of 14) dev-go/gopls-0.22.0:0::gentoo\n") {
		t.Fatalf("next stage did not replace completion with a clean line: %q", got)
	}
}

func TestAnimatedTerminalProgressAdvancesOnlyOnActivity(t *testing.T) {
	var output bytes.Buffer
	progress := startTerminalProgressWriter("starting", true, true, true, &output)
	initial := output.String()
	if !strings.Contains(initial, "| starting") {
		t.Fatalf("initial activity frame = %q", initial)
	}

	// No clock-driven renderer exists: without an event, output stays byte-for-byte stable.
	time.Sleep(200 * time.Millisecond)
	if got := output.String(); got != initial {
		t.Fatalf("progress advanced without activity: before=%q after=%q", initial, got)
	}

	progress.setActivity("checking repository")
	progress.setActivity("fetching main")
	got := output.String()
	if !strings.Contains(got, "/ checking repository") || !strings.Contains(got, "- fetching main") {
		t.Fatalf("activity did not advance frames: %q", got)
	}
}

func TestFetchProgressCanShareTerminalMessageOwner(t *testing.T) {
	var output bytes.Buffer
	terminal := &terminalProgress{output: true, terminal: true, writer: &output}
	terminal.setStatus(">>> Jobs: 0 of 2 complete")
	fp := newFetchProgress(true, true, &output)
	fp.setConcurrent(true)
	fp.line = terminal.message
	fp.Report(fetch.Progress{Stage: fetch.ProgressChecking, Artifact: "source.tar.xz"})
	got := output.String()
	if !strings.Contains(got, "\x1b[1G\x1b[K>>> Checking source.tar.xz\n\x1b[1G\x1b[K>>> Jobs: 0 of 2 complete") {
		t.Fatalf("shared fetch/status ordering = %q", got)
	}
}

type recordedProgressWrites struct{ frames []string }

func (w *recordedProgressWrites) Write(data []byte) (int, error) {
	w.frames = append(w.frames, string(data))
	return len(data), nil
}

func TestConcurrentMessagesEachWriteOneTerminalFrame(t *testing.T) {
	var output recordedProgressWrites
	p := startTerminalProgressWriter("", true, false, true, &output)
	const status = ">>> Jobs: 0 of 1 complete"
	p.setStatus(status)
	var workers sync.WaitGroup
	for i := range 24 {
		workers.Go(func() { p.message(fmt.Sprintf("worker %d starting\nworker %d finished", i, i)) })
	}
	workers.Wait()
	want := map[string]bool{}
	for i := range 24 {
		want[fmt.Sprintf("worker %d starting\nworker %d finished", i, i)] = true
	}
	for _, frame := range output.frames[1:] {
		start := strings.Index(frame, "worker ")
		end := strings.LastIndex(frame, "\n")
		if start < 0 || end < start || !want[strings.ReplaceAll(frame[start:end], progressColumnOne, "")] || !strings.HasSuffix(frame, status) {
			t.Fatalf("incomplete, duplicate or interleaved terminal frame %q", frame)
		}
		delete(want, strings.ReplaceAll(frame[start:end], progressColumnOne, ""))
	}
	if len(want) != 0 {
		t.Fatalf("missing %d worker messages", len(want))
	}
}

// Exercise the real terminal file descriptor and output processing. In this
// mode a carriage return becomes a newline and cannot return to column one.
func TestMergeProgressOnNarrowTTY(t *testing.T) {
	const binEnv = "ARISE_TEST_PROGRESS_EXECUTABLE"
	const status = ">>> Jobs: 0 of 1 complete"
	const phase = ">>> Syncing package contents (1 of 1) sys-apps/arise-0.0.56:0::arise-overlay"
	const prefix = ">>> Installing package contents: 15/15 (100%) "
	const colored = "\x1b[32m界界sys-apps/arise-0.0.56\x1b[0m"
	if os.Getenv(binEnv) != "" {
		p := startTerminalProgressMode("", true, false)
		p.setStatus(status)
		p.setProgress(prefix+colored, 15, 15)
		p.stage(phase, "")
		p.stopAndClear()
		return
	}

	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("util-linux script is required for the pseudo-terminal test")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Allow the first double-width character and leave the final column unused.
	width := len(prefix) + 3
	command := fmt.Sprintf("stty rows 12 cols %d ocrnl -onlcr && exec \"$%s\" -test.run='^TestMergeProgressOnNarrowTTY$'", width, binEnv)
	cmd := exec.Command(script, "-qec", command, "/dev/null")
	cmd.Env = append(os.Environ(), binEnv+"="+binary, "TERM=xterm")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("PTY: %v: %s", err, out)
	}
	row := "\x1b[1G\x1b[K"
	want := row + status + row + prefix + "\x1b[32m界\x1b[0m" + row + phase + "\n" + row + status + row
	if !strings.HasPrefix(string(out), want) {
		t.Fatalf("incorrect merge display on a %d-column terminal:\n got %q\nwant prefix %q", width, out, want)
	}
}

func TestTransactionStageAndNoticeRecords(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		var output recordedProgressWrites
		p := startTerminalProgressWriter("", true, false, terminal, &output)
		p.setStatus("Jobs: 0 of 1")
		p.setProgress("installing", 1, 1)
		p.stage("completed", "Jobs: 1 of 1")
		p.notice("WARN", "first\n\nsecond")
		p.stopAndClear()
		p.message("after transaction")

		stage, notice := "completed\nJobs: 1 of 1\n", " * warn: first\n * warn: second\n"
		if terminal {
			stage = eraseProgressLine + "completed\n" + eraseProgressLine + "Jobs: 1 of 1"
			notice = eraseProgressLine + " * warn: first\n" + progressColumnOne + " * warn: second\n" + eraseProgressLine + "Jobs: 1 of 1"
		}
		if len(output.frames) < 5 || output.frames[2] != stage || output.frames[3] != notice || output.frames[len(output.frames)-1] != "after transaction\n" {
			t.Errorf("terminal=%t, transaction writes = %q", terminal, output.frames)
		}
	}
}
