package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/fetch"
	"github.com/clipperhouse/displaywidth"
	"golang.org/x/term"
)

type terminalProgress struct {
	output            bool
	enabled           bool // activity frames are reserved for callers that report real events
	terminal          bool
	animate           bool
	displayed         bool
	stopped           bool
	mu                sync.Mutex
	writer            io.Writer
	label             string
	status            string
	transient         string
	renderedLine      string
	progressBucket    int
	concurrent        bool
	completedProgress map[string]bool
	frame             int
}

var progressFrames = [...]string{"|", "/", "-", "\\"}

func startTerminalProgress(label string, enabled bool) *terminalProgress {
	return startTerminalProgressMode(label, enabled, true)
}

func startTerminalProgressMode(label string, output, animate bool) *terminalProgress {
	terminal := false
	if output && os.Getenv("TERM") != "dumb" {
		width, _, err := term.GetSize(int(os.Stdout.Fd()))
		terminal = err == nil && width > 1
	}
	return startTerminalProgressWriter(label, output, animate, terminal, os.Stdout)
}

func startTerminalProgressWriter(label string, output, animate, terminal bool, writer io.Writer) *terminalProgress {
	p := &terminalProgress{output: output, enabled: terminal && animate, terminal: terminal, animate: animate, writer: writer, label: label, progressBucket: -1}
	if p.terminal && p.animate {
		p.writeFrameLocked("")
	}
	return p
}

// Position explicitly: a tty can translate carriage returns in its output driver.
const progressColumnOne = "\x1b[1G"
const eraseProgressLine = progressColumnOne + "\x1b[K"

var liveLineWhitespace = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ")

func (p *terminalProgress) liveLine() string {
	line := p.transient
	if line == "" {
		line = p.status
	}
	if line == "" && p.animate {
		line = progressFrames[p.frame] + " " + p.label
	}
	line = liveLineWhitespace.Replace(line)
	if file, ok := p.writer.(*os.File); ok {
		width, _, err := term.GetSize(int(file.Fd()))
		if err != nil || width <= 1 {
			return ""
		}
		// Leave the final column unused, and keep color sequences intact.
		return (displaywidth.Options{ControlSequences: true}).TruncateString(line, width-1, "")
	}
	return line
}

// record contains complete, newline-terminated messages. A single write commits
// their insertion above the live row together with the replacement status.
func (p *terminalProgress) writeFrameLocked(record string) {
	if !p.terminal || p.stopped {
		if record != "" {
			io.WriteString(p.writer, record)
		}
		return
	}
	var line string
	if record == "" || p.status != "" {
		line = p.liveLine()
	}
	if record == "" && line == p.renderedLine {
		return
	}
	var frame strings.Builder
	frame.WriteString(eraseProgressLine)
	if record != "" {
		message := strings.TrimSuffix(record, "\n")
		frame.WriteString(strings.ReplaceAll(message, "\n", "\n"+progressColumnOne))
		frame.WriteByte('\n')
	}
	if line != "" {
		if record != "" {
			frame.WriteString(eraseProgressLine)
		}
		frame.WriteString(line)
	}
	io.WriteString(p.writer, frame.String())
	p.displayed = line != ""
	p.renderedLine = line
}

func (p *terminalProgress) advanceActivityLocked() {
	if p.animate {
		p.frame = (p.frame + 1) % len(progressFrames)
	}
}

func (p *terminalProgress) setLabel(label string) {
	if p == nil || !p.output {
		return
	}
	p.mu.Lock()
	p.label = label
	if p.terminal {
		p.advanceActivityLocked()
		p.writeFrameLocked("")
	}
	p.mu.Unlock()
}

// setActivity advances and replaces the terminal activity label, or writes a durable stage
// line when output is redirected. It is intended for work that has meaningful
// stages but no measurable item count.
func (p *terminalProgress) setActivity(activity string) {
	if p == nil || !p.output {
		return
	}
	p.mu.Lock()
	p.label = activity
	p.status = ""
	p.transient = ""
	if p.terminal {
		p.advanceActivityLocked()
		p.writeFrameLocked("")
	} else {
		fmt.Fprintln(p.writer, activity)
	}
	p.mu.Unlock()
}

func (p *terminalProgress) setStatus(status string) {
	if p == nil || !p.output {
		return
	}
	p.mu.Lock()
	p.status = status
	if p.terminal {
		p.writeFrameLocked("")
	} else {
		fmt.Fprintln(p.writer, status)
	}
	p.mu.Unlock()
}

func (p *terminalProgress) setConcurrent(concurrent bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.concurrent = concurrent
	if concurrent && p.completedProgress == nil {
		p.completedProgress = make(map[string]bool)
	}
	if !concurrent {
		p.completedProgress = nil
	}
	p.mu.Unlock()
}

// setProgress updates one transient measurement in place on a terminal. For
// redirected output it emits only ten-percent milestones and completion, so a
// large merge does not turn one measurement into thousands of log records.
func (p *terminalProgress) setProgress(message string, current, total int) {
	if p == nil || !p.output || total <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.concurrent {
		// Parallel packages cannot safely share one cursor-owned transient line
		// or percentage bucket. Emit only each package's durable completion
		// record; stage messages retain ownership of the live status line.
		if current != total {
			return
		}
		if p.completedProgress == nil {
			p.completedProgress = make(map[string]bool)
		}
		if p.completedProgress[message] {
			return
		}
		p.completedProgress[message] = true
		p.writeFrameLocked(message + "\n")
		return
	}
	p.transient = message
	bucket := current * 10 / total
	if bucket <= p.progressBucket {
		return
	}
	p.progressBucket = bucket
	if p.terminal {
		p.writeFrameLocked("")
		return
	}
	fmt.Fprintln(p.writer, message)
}

// stage ends the previous measurement and displays the next phase atomically.
func (p *terminalProgress) stage(message, nextStatus string) {
	if p == nil || !p.output {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.transient = ""
	p.progressBucket = -1
	if message != "" {
		message += "\n"
	}
	if nextStatus != "" {
		p.status = nextStatus
		if !p.terminal {
			message += nextStatus + "\n"
		}
	}
	p.writeFrameLocked(message)
}

func (p *terminalProgress) message(message string) {
	if p == nil || !p.output {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writeFrameLocked(message + "\n")
}

func (p *terminalProgress) notice(class, message string) {
	prefix := " *"
	if class != "" && class != "INFO" {
		prefix += " " + strings.ToLower(class) + ":"
	}
	var notice []string
	for _, line := range strings.Split(message, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			notice = append(notice, prefix+" "+line)
		}
	}
	if len(notice) != 0 {
		p.message(strings.Join(notice, "\n"))
	}
}

func (p *terminalProgress) stop() {
	p.stopMode(true)
}

// stopAndClear releases a transient terminal line so a durable final report
// can replace it without leaving an empty line. Redirected output is already
// durable and needs no cleanup.
func (p *terminalProgress) stopAndClear() {
	p.stopMode(false)
}

func (p *terminalProgress) stopMode(newline bool) {
	if p == nil || !p.terminal {
		return
	}
	p.mu.Lock()
	if p.displayed {
		frame := eraseProgressLine
		if newline {
			frame += "\n"
		}
		io.WriteString(p.writer, frame)
		p.displayed = false
		p.renderedLine = ""
	}
	p.stopped = true
	p.mu.Unlock()
}

type fetchProgress struct {
	mu       sync.Mutex
	writer   io.Writer
	terminal bool
	verbose  bool
	started  map[string]time.Time
	last     map[string]time.Time
	active   bool
	line     func(string)
}

func newFetchProgress(enabled, verbose bool, writer io.Writer) *fetchProgress {
	progress := &fetchProgress{writer: writer, verbose: verbose, started: make(map[string]time.Time), last: make(map[string]time.Time)}
	if file, ok := writer.(*os.File); ok {
		progress.terminal = enabled && term.IsTerminal(int(file.Fd()))
	}
	if !enabled {
		progress.writer = io.Discard
	}
	return progress
}

// A carriage-return percentage display has only one owner. Concurrent
// downloads therefore use complete event lines rather than allowing workers to
// overwrite or accidentally terminate each other's progress line.
func (p *fetchProgress) setConcurrent(concurrent bool) {
	if concurrent {
		p.terminal = false
	}
}

func (p *fetchProgress) Report(event fetch.Progress) {
	if !p.verbose {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	switch event.Stage {
	case fetch.ProgressChecking:
		p.writeLine("%s %s %s", color.PortageGreen(">>>"), color.Bold("Checking"), event.Artifact)
	case fetch.ProgressCached:
		p.writeLine("%s %s %s", color.PortageGreen(">>>"), color.Bold("Using verified distfile"), event.Artifact)
	case fetch.ProgressDownload:
		if p.started[event.Artifact].IsZero() {
			p.started[event.Artifact] = now
			p.writeLine("%s %s %s", color.PortageGreen(">>>"), color.Bold("Downloading"), event.Source)
		}
		if !p.terminal || (event.Downloaded < event.Total && now.Sub(p.last[event.Artifact]) < 100*time.Millisecond) {
			return
		}
		p.last[event.Artifact] = now
		elapsed := now.Sub(p.started[event.Artifact]).Seconds()
		rate := int64(0)
		if elapsed > 0 {
			rate = int64(float64(event.Downloaded) / elapsed)
		}
		percent := float64(0)
		if event.Total > 0 {
			percent = 100 * float64(event.Downloaded) / float64(event.Total)
		}
		fmt.Fprintf(p.writer, "\r    %6.2f%%  %s / %s  %s/s", percent, formatSize(event.Downloaded), formatSize(event.Total), formatSize(rate))
		p.active = true
	case fetch.ProgressVerifying:
		p.finishLine()
		p.writeLine("%s %s %s against Manifest", color.PortageGreen(">>>"), color.Bold("Verifying"), event.Artifact)
	case fetch.ProgressComplete:
		p.finishLine()
		p.writeLine("%s %s %s", color.PortageGreen(">>>"), color.Bold("Fetched and verified"), event.Artifact)
	}
}

func (p *fetchProgress) writeLine(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if p.line != nil {
		p.line(message)
		return
	}
	fmt.Fprintln(p.writer, message)
}

func (p *fetchProgress) finishLine() {
	if p.active {
		fmt.Fprintln(p.writer)
		p.active = false
	}
}
