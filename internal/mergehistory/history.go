// Package mergehistory reads Portage-compatible logs without changing them.
package mergehistory

import (
	"bufio"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/airencracken/arise/internal/atom"
)

type Event struct {
	Kind            string     `json:"kind"`
	Package         string     `json:"package"`
	CPV             string     `json:"cpv,omitempty"`
	Repository      string     `json:"repository,omitempty"`
	Root            string     `json:"root,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	DurationSeconds *int64     `json:"duration_seconds,omitempty"`
	Status          string     `json:"status"`
}

type Report struct {
	Events         []Event `json:"events"`
	RecoveredLines int     `json:"recovered_lines"`
	MalformedLines int     `json:"malformed_lines"`
}

type entry struct {
	stamp     time.Time
	event     Event
	key       string
	finish    bool
	terminate bool
}

// Read accepts explicitly selected plain, gzip and bzip2 logs. Records are
// ordered across rotated files and overlapping copies are deduplicated.
// Failure returns no partial report, including decompression/scanner failures.
func Read(ctx context.Context, paths []string) (Report, error) {
	var entries []entry
	report := Report{Events: []Event{}}
	seen := make(map[string]bool)
	for _, path := range paths {
		if err := readFile(ctx, path, &entries, seen, &report); err != nil {
			return Report{}, err
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].stamp.Equal(entries[j].stamp) {
			return syncOrder(entries[i]) < syncOrder(entries[j])
		}
		return entries[i].stamp.Before(entries[j].stamp)
	})
	pending := make(map[string][]int)
	for _, record := range entries {
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
		if record.terminate {
			for _, indexes := range pending {
				for _, index := range indexes {
					report.Events[index].Status = "interrupted"
				}
			}
			clear(pending)
			continue
		}
		if nestedRsync(record, report.Events) {
			continue
		}
		if !record.finish {
			report.Events = append(report.Events, record.event)
			pending[record.key] = append(pending[record.key], len(report.Events)-1)
			continue
		}
		queue := pending[record.key]
		if len(queue) == 0 {
			// A rotated log may begin with a completion whose start is missing.
			// Preserve the successful event without inventing its duration.
			event := record.event
			event.StartedAt = nil
			event.Status, event.FinishedAt = "completed", &record.stamp
			report.Events = append(report.Events, event)
			continue
		}
		index := queue[len(queue)-1]
		pending[record.key] = queue[:len(queue)-1]
		event := &report.Events[index]
		event.Status, event.FinishedAt = "completed", &record.stamp
		duration := record.stamp.Unix() - event.StartedAt.Unix()
		event.DurationSeconds = &duration
	}
	return report, nil
}

func syncOrder(record entry) int {
	if record.event.Kind != "sync" {
		return 1
	}
	if strings.Contains(record.event.Package, "://") {
		return 2
	}
	return 0
}

// Modern Portage reports both a repository sync and its rsync transport. The
// transport is not a second repository operation; old URL-only logs still work.
func nestedRsync(record entry, events []Event) bool {
	if record.event.Kind != "sync" || !strings.Contains(record.event.Package, "://") {
		return false
	}
	for _, event := range events {
		if event.Kind != "sync" || strings.Contains(event.Package, "://") || event.StartedAt == nil || record.stamp.Before(*event.StartedAt) {
			continue
		}
		if event.Status == "incomplete" || event.FinishedAt != nil && !record.stamp.After(*event.FinishedAt) {
			return true
		}
	}
	return false
}

func readFile(ctx context.Context, path string, entries *[]entry, seen map[string]bool, report *Report) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("history: %w", err)
	}
	defer file.Close()
	var reader io.Reader = file
	switch {
	case strings.HasSuffix(path, ".gz"):
		compressed, err := gzip.NewReader(file)
		if err != nil {
			return fmt.Errorf("history: read %s: %w", path, err)
		}
		defer compressed.Close()
		reader = compressed
	case strings.HasSuffix(path, ".bz2"):
		reader = bzip2.NewReader(file)
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		original := scanner.Text()
		line := strings.TrimLeft(original, "\x00")
		record, recognized, err := parseLine(line)
		if !recognized {
			continue
		}
		if seen[line] {
			continue
		}
		seen[line] = true
		if line != original {
			report.RecoveredLines++
		}
		if err != nil {
			report.MalformedLines++
			continue
		}
		*entries = append(*entries, record)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("history: read %s: %w", path, err)
	}
	return nil
}

func parseLine(line string) (entry, bool, error) {
	rawStamp, body, ok := strings.Cut(line, ":")
	if !ok {
		return entry{}, false, nil
	}
	body = strings.TrimSpace(body)
	kind, subject, root, finish := "", "", "", false
	switch {
	case strings.HasPrefix(body, "*** exiting "), strings.HasPrefix(body, "*** terminating."):
		kind = "terminate"
	case strings.HasPrefix(body, ">>> emerge ("), strings.HasPrefix(body, "::: completed emerge ("),
		strings.HasPrefix(body, ">>> unmerge ("), strings.HasPrefix(body, "::: completed unmerge ("):
		kind = "merge"
		if strings.Contains(body, "unmerge (") {
			kind = "unmerge"
		}
		finish = strings.HasPrefix(body, ":::")
		_, rest, found := strings.Cut(body, ") ")
		if !found {
			return entry{}, true, fmt.Errorf("missing package")
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return entry{}, true, fmt.Errorf("missing package")
		}
		subject = fields[0]
		if _, value, found := strings.Cut(rest, " to "); found {
			root = strings.TrimSpace(value)
		}
	case strings.HasPrefix(body, "=== Unmerging... ("):
		kind, subject = "unmerge", strings.TrimSuffix(strings.TrimPrefix(body, "=== Unmerging... ("), ")")
	case strings.HasPrefix(body, ">>> unmerge success: "):
		kind, subject, finish = "unmerge", strings.TrimPrefix(body, ">>> unmerge success: "), true
	case strings.HasPrefix(body, ">>> Syncing repository '"):
		kind = "sync"
		subject, _, _ = strings.Cut(strings.TrimPrefix(body, ">>> Syncing repository '"), "'")
	case strings.HasPrefix(body, "=== Sync completed for "):
		kind, subject, finish = "sync", strings.TrimSpace(strings.TrimPrefix(body, "=== Sync completed for ")), true
	case strings.HasPrefix(strings.ToLower(body), ">>> starting rsync with "):
		kind, subject = "sync", strings.TrimSpace(body[len(">>> starting rsync with "):])
	case strings.HasPrefix(body, "=== Sync completed with "):
		kind, subject, finish = "sync", strings.TrimSpace(strings.TrimPrefix(body, "=== Sync completed with ")), true
	default:
		return entry{}, false, nil
	}
	unix, err := strconv.ParseInt(strings.TrimSpace(rawStamp), 10, 64)
	if err != nil || unix < 0 || unix > 253402300799 {
		return entry{}, true, fmt.Errorf("invalid timestamp")
	}
	stamp := time.Unix(unix, 0).UTC()
	if kind == "terminate" {
		return entry{stamp: stamp, terminate: true}, true, nil
	}
	event := Event{Kind: kind, Package: subject, Root: root, StartedAt: &stamp, Status: "incomplete"}
	if kind != "sync" {
		parsed, err := atom.Parse(subject)
		if err != nil || parsed.Version == nil {
			return entry{}, true, fmt.Errorf("invalid package")
		}
		event.Package, event.CPV, event.Repository = parsed.CP(), parsed.CPV(), parsed.Repo
	}
	if subject == "" {
		return entry{}, true, fmt.Errorf("missing subject")
	}
	key := strings.Join([]string{kind, event.CPV, event.Package, event.Repository, root}, "\x00")
	return entry{stamp: stamp, event: event, key: key, finish: finish}, true, nil
}

// EventTime uses the completion time only when the start was outside the log.
func EventTime(event Event) time.Time {
	if event.StartedAt != nil {
		return *event.StartedAt
	}
	if event.FinishedAt != nil {
		return *event.FinishedAt
	}
	return time.Time{}
}
