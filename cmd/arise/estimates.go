package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/resolve"
)

type mergeEstimates map[string]time.Duration

func loadMergeEstimates(path string) mergeEstimates {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	starts := make(map[string][]time.Time)
	samples := make(map[string][]time.Duration)
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		// Historical emerge.log files can contain sparse/corrupt NUL padding
		// before an otherwise intact record. Do not let that poison timing
		// history for every later package; recover the timestamped suffix while
		// leaving the source log untouched for explicit, backed-up repair.
		line = strings.TrimLeft(line, "\x00")
		colon := strings.IndexByte(line, ':')
		if colon < 1 {
			continue
		}
		unix, err := strconv.ParseInt(strings.TrimSpace(line[:colon]), 10, 64)
		if err != nil {
			continue
		}
		stamp := time.Unix(unix, 0)
		kind := ""
		switch {
		case strings.Contains(line, ">>> emerge ("):
			kind = "start"
		case strings.Contains(line, "::: completed emerge ("):
			kind = "done"
		default:
			continue
		}
		close := strings.Index(line, ") ")
		if close < 0 {
			continue
		}
		cpv := strings.Fields(strings.TrimSuffix(strings.SplitN(line[close+2:], " to ", 2)[0], "::gentoo"))
		if len(cpv) == 0 {
			continue
		}
		key := cpv[0]
		if kind == "start" {
			starts[key] = append(starts[key], stamp)
			continue
		}
		queue := starts[key]
		if len(queue) == 0 {
			continue
		}
		start := queue[len(queue)-1]
		starts[key] = queue[:len(queue)-1]
		if duration := stamp.Sub(start); duration > 0 && duration < 7*24*time.Hour {
			category, pkg, _, parseErr := metadata.ParseCPV(key)
			if parseErr == nil {
				samples[category+"/"+pkg] = append(samples[category+"/"+pkg], duration)
			}
		}
	}
	result := make(mergeEstimates)
	for cp, values := range samples {
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		result[cp] = values[len(values)/2]
	}
	return result
}

func (e mergeEstimates) forAction(action resolve.PkgAction) (time.Duration, bool) {
	if action.Atom == nil {
		return 0, false
	}
	d, ok := e[action.Atom.CP()]
	return d, ok
}

func formatEstimate(d time.Duration) string {
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	return d.Round(time.Minute).String()
}

type portageMergeLog struct {
	mu   sync.Mutex
	file *os.File
	err  error
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
	return &portageMergeLog{file: f}, nil
}

func (l *portageMergeLog) event(completed bool, index, total int, action resolve.PkgAction) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	verb := ">>> emerge"
	if completed {
		verb = "::: completed emerge"
	}
	_, l.err = fmt.Fprintf(l.file, "%d:  %s (%d of %d) %s to /\n", time.Now().Unix(), verb, index, total, executionActionLabel(action))
	if l.err == nil {
		l.err = l.file.Sync()
	}
	return l.err
}

func (l *portageMergeLog) close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.file.Close(); l.err == nil {
		l.err = err
	}
	return l.err
}
