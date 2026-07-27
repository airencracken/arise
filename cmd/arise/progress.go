package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/airencracken/arise/internal/fetch"
	"golang.org/x/term"
)

type terminalProgress struct {
	output         bool
	enabled        bool // animation is reserved for work with measured progress events
	terminal       bool
	animate        bool
	displayed      bool
	done           chan struct{}
	wait           sync.WaitGroup
	mu             sync.Mutex
	writer         io.Writer
	label          string
	status         string
	transient      string
	progressBucket int
}

var progressFrames = [...]string{"|", "/", "-", "\\"}

func startTerminalProgress(label string, enabled bool) *terminalProgress {
	return startTerminalProgressMode(label, enabled, true)
}

func startTerminalProgressMode(label string, output, animate bool) *terminalProgress {
	terminal := output && os.Getenv("TERM") != "dumb" && term.IsTerminal(int(os.Stdout.Fd()))
	return startTerminalProgressWriter(label, output, animate, terminal, os.Stdout)
}

func startTerminalProgressWriter(label string, output, animate, terminal bool, writer io.Writer) *terminalProgress {
	p := &terminalProgress{output: output, enabled: terminal && animate, terminal: terminal, animate: animate, writer: writer, label: label, progressBucket: -1}
	if !p.terminal || !p.animate {
		return p
	}
	p.done = make(chan struct{})
	p.wait.Add(1)
	go func() {
		defer p.wait.Done()
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		frame := 0
		p.render(frame)
		for {
			select {
			case <-ticker.C:
				frame = (frame + 1) % len(progressFrames)
				p.render(frame)
			case <-p.done:
				return
			}
		}
	}()
	return p
}

func (p *terminalProgress) render(frame int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.renderLocked(frame)
}

func (p *terminalProgress) renderLocked(frame int) {
	if !p.terminal {
		return
	}
	line := p.transient
	if line == "" {
		line = p.status
	}
	if line == "" && p.animate {
		line = progressFrames[frame] + " " + p.label
	}
	if line == "" {
		return
	}
	if file, ok := p.writer.(*os.File); ok {
		if width, _, err := term.GetSize(int(file.Fd())); err == nil && width > 0 && len(line) > width {
			line = line[:width]
		}
	}
	fmt.Fprintf(p.writer, "\r\033[K%s", line)
	p.displayed = true
}

func (p *terminalProgress) setLabel(label string) {
	if p == nil || !p.output {
		return
	}
	p.mu.Lock()
	p.label = label
	p.mu.Unlock()
}

func (p *terminalProgress) setStatus(status string) {
	if p == nil || !p.output {
		return
	}
	p.mu.Lock()
	p.status = status
	if p.terminal {
		p.renderLocked(0)
	} else {
		fmt.Fprintln(p.writer, status)
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
	p.transient = message
	bucket := current * 10 / total
	if bucket <= p.progressBucket {
		return
	}
	p.progressBucket = bucket
	if p.terminal {
		p.renderLocked(0)
		return
	}
	fmt.Fprintln(p.writer, message)
}

func (p *terminalProgress) clearProgress() {
	if p == nil || !p.output {
		return
	}
	p.mu.Lock()
	p.transient = ""
	p.progressBucket = -1
	if p.terminal {
		p.renderLocked(0)
	}
	p.mu.Unlock()
}

func (p *terminalProgress) message(message string) {
	if p == nil || !p.output {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.terminal && p.displayed {
		fmt.Fprint(p.writer, "\r\033[K")
		p.displayed = false
	}
	fmt.Fprintln(p.writer, message)
	if p.terminal && p.status != "" {
		p.renderLocked(0)
	}
}

func (p *terminalProgress) stop() {
	if p == nil || !p.terminal {
		return
	}
	if p.done != nil {
		close(p.done)
		p.wait.Wait()
	}
	p.mu.Lock()
	if p.displayed {
		fmt.Fprint(p.writer, "\r\033[K\n")
		p.displayed = false
	}
	p.mu.Unlock()
}

type fetchProgress struct {
	mu       sync.Mutex
	writer   io.Writer
	terminal bool
	started  map[string]time.Time
	last     map[string]time.Time
	active   bool
	line     func(string)
}

func newFetchProgress(enabled bool, writer io.Writer) *fetchProgress {
	progress := &fetchProgress{writer: writer, started: make(map[string]time.Time), last: make(map[string]time.Time)}
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
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	switch event.Stage {
	case fetch.ProgressChecking:
		p.writeLine(">>> Checking %s", event.Artifact)
	case fetch.ProgressCached:
		p.writeLine(">>> Using verified distfile %s", event.Artifact)
	case fetch.ProgressDownload:
		if p.started[event.Artifact].IsZero() {
			p.started[event.Artifact] = now
			p.writeLine(">>> Downloading %s", event.Source)
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
		p.writeLine(">>> Verifying %s against Manifest", event.Artifact)
	case fetch.ProgressComplete:
		p.finishLine()
		p.writeLine(">>> Fetched and verified %s", event.Artifact)
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
