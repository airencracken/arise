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
	output  bool
	enabled bool
	done    chan struct{}
	wait    sync.WaitGroup
	mu      sync.Mutex
	label   string
}

var progressFrames = [...]string{"|", "/", "-", "\\"}

func startTerminalProgress(label string, enabled bool) *terminalProgress {
	return startTerminalProgressMode(label, enabled, true)
}

func startTerminalProgressMode(label string, output, animate bool) *terminalProgress {
	p := &terminalProgress{output: output, enabled: output && animate && term.IsTerminal(int(os.Stdout.Fd())), label: label}
	if !p.enabled {
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
	fmt.Printf("\r%s %s", progressFrames[frame], p.label)
}

func (p *terminalProgress) setLabel(label string) {
	if p == nil || !p.output {
		return
	}
	p.mu.Lock()
	p.label = label
	p.mu.Unlock()
}

func (p *terminalProgress) message(message string) {
	if p == nil || !p.output {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.enabled {
		fmt.Print("\r\033[K")
	}
	fmt.Println(message)
}

func (p *terminalProgress) stop() {
	if p == nil || !p.enabled {
		return
	}
	close(p.done)
	p.wait.Wait()
	p.mu.Lock()
	fmt.Print("\r\033[K")
	p.mu.Unlock()
}

type fetchProgress struct {
	mu       sync.Mutex
	writer   io.Writer
	terminal bool
	started  map[string]time.Time
	last     map[string]time.Time
	active   bool
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
		fmt.Fprintf(p.writer, ">>> Checking %s\n", event.Artifact)
	case fetch.ProgressCached:
		fmt.Fprintf(p.writer, ">>> Using verified distfile %s\n", event.Artifact)
	case fetch.ProgressDownload:
		if p.started[event.Artifact].IsZero() {
			p.started[event.Artifact] = now
			fmt.Fprintf(p.writer, ">>> Downloading %s\n", event.Source)
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
		fmt.Fprintf(p.writer, ">>> Verifying %s against Manifest\n", event.Artifact)
	case fetch.ProgressComplete:
		p.finishLine()
		fmt.Fprintf(p.writer, ">>> Fetched and verified %s\n", event.Artifact)
	}
}

func (p *fetchProgress) finishLine() {
	if p.active {
		fmt.Fprintln(p.writer)
		p.active = false
	}
}
