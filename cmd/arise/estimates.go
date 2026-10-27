package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/airencracken/arise/internal/mergehistory"
	"github.com/airencracken/arise/internal/resolve"
)

type mergeEstimates struct {
	events []mergehistory.Event
	err    error
}

func loadMergeEstimates(path string) mergeEstimates {
	report, err := mergehistory.Read(context.Background(), []string{path})
	return mergeEstimates{events: report.Events, err: err}
}

func (e mergeEstimates) forAction(action resolve.PkgAction) (time.Duration, bool) {
	prediction, ok := e.prediction(action)
	return time.Duration(prediction.Seconds * float64(time.Second)), ok
}

func (e mergeEstimates) prediction(action resolve.PkgAction) (mergehistory.Prediction, bool) {
	if action.Atom == nil || action.MergeType == "binary" || action.Action == "uninstall" {
		return mergehistory.Prediction{}, false
	}
	return mergehistory.Predict(e.events, action.Atom.CP(), action.Atom.CPV(), action.Repository, commandEnv("ROOT", "/"))
}

func formatEstimate(d time.Duration) string {
	return historyDuration(int64(max(d, 0).Round(time.Second) / time.Second))
}

type portageMergeLog struct {
	mu   sync.Mutex
	file *os.File
	err  error
	root string
}

func openPortageMergeLog(path string) (*portageMergeLog, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o664)
	if err != nil {
		return nil, err
	}
	buffer := make([]byte, 64*1024)
	for {
		count, readErr := f.Read(buffer)
		if bytes.IndexByte(buffer[:count], 0) >= 0 {
			_ = f.Close()
			return nil, fmt.Errorf("timing log %s contains NUL bytes; repair it before live execution", path)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = f.Close()
			return nil, fmt.Errorf("validate timing log %s: %w", path, readErr)
		}
	}
	return &portageMergeLog{file: f, root: commandEnv("ROOT", "/")}, nil
}

func (l *portageMergeLog) event(completed bool, index, total int, action resolve.PkgAction) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	verb := ">>> emerge"
	if completed {
		verb = "::: completed emerge"
	}
	line := fmt.Sprintf("%s (%d of %d) %s to %s", verb, index, total, executionActionLabel(action), l.root)
	if action.Action == "uninstall" && action.Atom != nil {
		line = "=== Unmerging... (" + action.Atom.CPV() + ")"
		if completed {
			line = ">>> unmerge success: " + action.Atom.CPV()
		}
	}
	_, l.err = fmt.Fprintf(l.file, "%d:  %s\n", time.Now().Unix(), line)
	if l.err == nil {
		l.err = l.file.Sync()
	}
	return l.err
}

func (l *portageMergeLog) close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.file.Close(); l.err == nil {
		l.err = err
	}
	return l.err
}

func openPackageExecutionLog(path string, buildOnly bool) (*portageMergeLog, error) {
	if buildOnly {
		return nil, nil
	}
	return openPortageMergeLog(path)
}
