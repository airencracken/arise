package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"syscall"
	"time"

	"github.com/airencracken/arise/internal/journal"
)

// startDiagnosticTrapHandler keeps SIGTRAP distinct from operator
// cancellation. It records useful process/recovery state, then restores and
// re-raises the signal so debuggers and core-dump policy retain normal trap
// semantics.
func startDiagnosticTrapHandler() func() {
	traps := make(chan os.Signal, 1)
	signal.Notify(traps, syscall.SIGTRAP)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		if _, ok := <-traps; !ok {
			return
		}
		path, err := writeTrapReport()
		if err != nil {
			fmt.Fprintf(os.Stderr, "arise: SIGTRAP diagnostic report failed: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "arise: SIGTRAP diagnostic report: %s\n", path)
		}
		signal.Stop(traps)
		signal.Reset(syscall.SIGTRAP)
		_ = syscall.Kill(os.Getpid(), syscall.SIGTRAP)
	}()
	return func() {
		signal.Stop(traps)
		select {
		case <-stopped:
		default:
			close(traps)
			<-stopped
		}
	}
}

func writeTrapReport() (string, error) {
	directory := os.Getenv("ARISE_TRAP_DIR")
	if directory == "" {
		directory = filepath.Join(*workDir, "traps")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(directory, fmt.Sprintf("arise-trap-%d-%s.txt", os.Getpid(), time.Now().UTC().Format("20060102T150405.000000000Z")))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	writeErr := writeTrapDiagnostics(file, time.Now())
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return path, writeErr
	}
	return path, closeErr
}

func writeTrapDiagnostics(w io.Writer, now time.Time) error {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	if _, err := fmt.Fprintf(w,
		"Arise SIGTRAP diagnostic\nTime: %s\nPID: %d\nVersion: %s\nInvocation: %q\nGoroutines: %d\nHeapAlloc: %d\nHeapSys: %d\n\n",
		now.UTC().Format(time.RFC3339Nano), os.Getpid(), version, os.Args, runtime.NumGoroutine(), memory.HeapAlloc, memory.HeapSys,
	); err != nil {
		return err
	}
	summaries, err := journal.List(*journalDir)
	if err != nil {
		if _, writeErr := fmt.Fprintf(w, "Journal status error: %v\n\n", err); writeErr != nil {
			return writeErr
		}
	} else {
		active := 0
		for _, summary := range summaries {
			if summary.Status == "active" {
				active++
				if _, err := fmt.Fprintf(w, "Active journal: %s root=%s entries=%d path=%s\n", summary.ID, summary.Root, summary.Entries, summary.Path); err != nil {
					return err
				}
			}
		}
		if active == 0 {
			if _, err := fmt.Fprintln(w, "Active journals: none"); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	profile := pprof.Lookup("goroutine")
	if profile == nil {
		return fmt.Errorf("goroutine profile unavailable")
	}
	if _, err := fmt.Fprintln(w, "Goroutine stacks:"); err != nil {
		return err
	}
	return profile.WriteTo(w, 2)
}
