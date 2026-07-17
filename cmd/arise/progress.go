package main

import (
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/term"
)

type terminalProgress struct {
	enabled bool
	done    chan struct{}
	wait    sync.WaitGroup
}

var progressFrames = [...]string{"|", "/", "-", "\\"}

func startTerminalProgress(label string, enabled bool) *terminalProgress {
	p := &terminalProgress{enabled: enabled && term.IsTerminal(int(os.Stdout.Fd()))}
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
		fmt.Printf("\r%s %s", progressFrames[frame], label)
		for {
			select {
			case <-ticker.C:
				frame = (frame + 1) % len(progressFrames)
				fmt.Printf("\r%s %s", progressFrames[frame], label)
			case <-p.done:
				return
			}
		}
	}()
	return p
}

func (p *terminalProgress) stop() {
	if p == nil || !p.enabled {
		return
	}
	close(p.done)
	p.wait.Wait()
	fmt.Print("\r\033[K")
}
