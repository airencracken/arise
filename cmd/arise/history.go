package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/mergehistory"
)

type historyPaths []string

func (p *historyPaths) String() string { return strings.Join(*p, ", ") }
func (p *historyPaths) Set(value string) error {
	if value == "" {
		return fmt.Errorf("log path cannot be empty")
	}
	*p = append(*p, value)
	return nil
}

type historyDocument struct {
	Schema         string                    `json:"schema"`
	Mode           string                    `json:"mode"`
	Logs           []string                  `json:"logs"`
	Matched        int                       `json:"matched"`
	Events         []mergehistory.Event      `json:"events"`
	Statistics     []mergehistory.Statistics `json:"statistics"`
	RecoveredLines int                       `json:"recovered_lines"`
	MalformedLines int                       `json:"malformed_lines"`
}

func runHistory(args []string) int {
	return runHistoryWithWriters(commandContext, args, os.Stdout, os.Stderr, time.Now())
}

func runHistoryWithWriters(ctx context.Context, args []string, stdout, stderr io.Writer, now time.Time) int {
	mode := "list"
	if len(args) > 0 && (args[0] == "list" || args[0] == "stats" || args[0] == "incomplete") {
		mode, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("history", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var logs historyPaths
	flags.Var(&logs, "log", "log file; repeat for rotated logs (.gz/.bz2 supported)")
	jsonMode := flags.Bool("json", *jsonOutput, "emit arise.history.v1 JSON")
	utc := flags.Bool("utc", false, "display dates in UTC")
	since := flags.String("since", "", "include actions started at/after DATE or DURATION ago (e.g. 7d)")
	until := flags.String("until", "", "include actions through DATE, or before RFC3339 timestamp")
	kind := flags.String("kind", "all", "all, merge, unmerge, or sync")
	limit := flags.Int("limit", 20, "maximum displayed rows; 0 shows all")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: arise history [list|stats|incomplete] [options] [package|glob...]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *limit < 0 || (*kind != "all" && *kind != "merge" && *kind != "unmerge" && *kind != "sync") {
		fmt.Fprintln(stderr, "history: invalid --limit or --kind")
		return 2
	}
	location := time.Local
	if *utc {
		location = time.UTC
	}
	from, err := historyDate(*since, now, location, false)
	if err != nil {
		fmt.Fprintf(stderr, "history: --since: %v\n", err)
		return 2
	}
	to, err := historyDate(*until, now, location, true)
	if err != nil {
		fmt.Fprintf(stderr, "history: --until: %v\n", err)
		return 2
	}
	if !from.IsZero() && !to.IsZero() && !from.Before(to) {
		fmt.Fprintln(stderr, "history: --since must be before --until")
		return 2
	}
	patterns := flags.Args()
	for _, pattern := range patterns {
		if _, err := path.Match(pattern, ""); err != nil {
			fmt.Fprintf(stderr, "history: invalid package glob %q\n", pattern)
			return 2
		}
	}
	if len(logs) == 0 {
		logs = historyPaths{*emergeLog}
	}
	report, err := mergehistory.Read(ctx, logs)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	selected := make([]mergehistory.Event, 0)
	for _, event := range report.Events {
		if *kind != "all" && event.Kind != *kind {
			continue
		}
		if mode == "incomplete" && event.Status == "completed" {
			continue
		}
		stamp := mergehistory.EventTime(event)
		if !from.IsZero() && stamp.Before(from) || !to.IsZero() && !stamp.Before(to) {
			continue
		}
		if !historyMatches(event, patterns) {
			continue
		}
		selected = append(selected, event)
	}
	sort.SliceStable(selected, func(i, j int) bool {
		return mergehistory.EventTime(selected[i]).After(mergehistory.EventTime(selected[j]))
	})
	document := historyDocument{Schema: "arise.history.v1", Mode: mode, Logs: logs, Matched: len(selected), Events: []mergehistory.Event{}, Statistics: []mergehistory.Statistics{}, RecoveredLines: report.RecoveredLines, MalformedLines: report.MalformedLines}
	if mode == "stats" {
		document.Statistics = mergehistory.Summarize(selected)
		if *limit > 0 && len(document.Statistics) > *limit {
			document.Statistics = document.Statistics[:*limit]
		}
	} else {
		document.Events = selected
		if *limit > 0 && len(document.Events) > *limit {
			document.Events = document.Events[:*limit]
		}
	}
	var output bytes.Buffer
	if *jsonMode {
		encoder := json.NewEncoder(&output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(document); err != nil {
			fmt.Fprintf(stderr, "history: encode: %v\n", err)
			return 1
		}
	} else {
		writeHistoryReport(&output, document, location)
	}
	if _, err := io.Copy(stdout, &output); err != nil {
		fmt.Fprintf(stderr, "history: write: %v\n", err)
		return 1
	}
	return 0
}

func historyMatches(event mergehistory.Event, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		for _, candidate := range []string{event.Package, event.CPV, path.Base(event.Package), path.Base(event.CPV)} {
			if match, _ := path.Match(pattern, candidate); match {
				return true
			}
		}
	}
	return false
}

func historyDate(value string, now time.Time, location *time.Location, end bool) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if value == "today" || value == "yesterday" {
		y, m, d := now.In(location).Date()
		date := time.Date(y, m, d, 0, 0, 0, 0, location)
		if value == "yesterday" {
			date = date.AddDate(0, 0, -1)
		}
		if end {
			date = date.AddDate(0, 0, 1)
		}
		return date, nil
	}
	if date, err := time.ParseInLocation("2006-01-02", value, location); err == nil {
		if end {
			date = date.AddDate(0, 0, 1)
		}
		return date, nil
	}
	if date, err := time.Parse(time.RFC3339, value); err == nil {
		return date, nil
	}
	if strings.HasSuffix(value, "d") || strings.HasSuffix(value, "w") {
		n, err := strconv.Atoi(value[:len(value)-1])
		if err == nil && n > 0 && n <= 36500 {
			days := n
			if strings.HasSuffix(value, "w") {
				days *= 7
			}
			return now.In(location).AddDate(0, 0, -days), nil
		}
	}
	if duration, err := time.ParseDuration(value); err == nil && duration > 0 {
		return now.Add(-duration), nil
	}
	return time.Time{}, fmt.Errorf("invalid date %q; use YYYY-MM-DD, RFC3339, today, yesterday, or a positive duration", value)
}

func historyText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '?'
		}
		return r
	}, value)
}

func historyDuration(seconds int64) string {
	if seconds < 0 {
		return "unknown"
	}
	days := seconds / 86400
	hours := seconds / 3600 % 24
	minutes := seconds / 60 % 60
	rest := seconds % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %02dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %02ds", minutes, rest)
	}
	return fmt.Sprintf("%ds", rest)
}

func writeHistoryReport(writer io.Writer, document historyDocument, location *time.Location) {
	if document.Mode == "stats" {
		writeHistoryStatistics(writer, document, location)
		return
	}
	title := "Package history"
	if document.Mode == "incomplete" {
		title = "Unfinished actions"
	}
	fmt.Fprintf(writer, "%s  %d shown of %d matching actions\n\n", color.Bold(title), len(document.Events), document.Matched)
	if len(document.Events) == 0 {
		fmt.Fprintln(writer, "  No matching history.")
	} else {
		fmt.Fprintln(writer, color.Bold("  DATE / TIME          ACTION     DURATION    PACKAGE / REPOSITORY"))
		for _, event := range document.Events {
			duration := "unknown"
			if event.DurationSeconds != nil {
				duration = historyDuration(*event.DurationSeconds)
			}
			subject := event.CPV
			if subject == "" {
				subject = event.Package
			}
			if event.Repository != "" {
				subject += "::" + event.Repository
			}
			row := fmt.Sprintf("  %s  %-8s %10s  %s", mergehistory.EventTime(event).In(location).Format("2006-01-02 15:04:05"), event.Kind, duration, historyText(subject))
			if event.Root != "" && event.Root != "/" {
				row += "  [root " + historyText(event.Root) + "]"
			}
			if event.Status != "completed" {
				row += "  [" + event.Status + "]"
			}
			fmt.Fprintln(writer, row)
		}
	}
	writeHistoryNotes(writer, document, location)
}

func writeHistoryStatistics(writer io.Writer, document historyDocument, location *time.Location) {
	fmt.Fprintf(writer, "%s  %d matching actions\n\n", color.Bold("Timing summary"), document.Matched)
	width := 24
	for _, stats := range document.Statistics {
		width = max(width, len(historyText(stats.Package)))
	}
	fmt.Fprintf(writer, "  %-*s  %-7s %6s %10s %10s %10s %11s\n", width, "PACKAGE / REPOSITORY", "ACTION", "RUNS", "MEDIAN", "P90", "AVERAGE", "TOTAL WORK")
	for _, stats := range document.Statistics {
		fmt.Fprintf(writer, "  %-*s  %-7s %6d %10s %10s %10s %11s\n", width, historyText(stats.Package), stats.Kind, stats.Completed, historyStatistic(stats.Samples, stats.MedianSeconds), historyStatistic(stats.Samples, float64(stats.P90Seconds)), historyStatistic(stats.Samples, stats.MeanSeconds), historyStatistic(stats.Samples, float64(stats.TotalSeconds)))
		sampleLabel := "samples"
		if stats.Samples == 1 {
			sampleLabel = "sample"
		}
		details := []string{fmt.Sprintf("%d timed %s", stats.Samples, sampleLabel)}
		if stats.Repository != "" {
			details = append(details, historyText(stats.Repository))
		}
		if stats.Root != "" {
			details = append(details, "root "+historyText(stats.Root))
		}
		if stats.Incomplete > 0 {
			details = append(details, fmt.Sprintf("%d unfinished", stats.Incomplete))
		}
		fmt.Fprintf(writer, "    %s\n", strings.Join(details, " | "))
	}
	if len(document.Statistics) == 0 {
		fmt.Fprintln(writer, "  No matching history.")
	}
	writeHistoryNotes(writer, document, location)
}

func historyStatistic(samples int, value float64) string {
	if samples == 0 {
		return "unknown"
	}
	if value >= float64(math.MaxInt64) {
		return historyDuration(math.MaxInt64)
	}
	return historyDuration(int64(math.Round(value)))
}

func writeHistoryNotes(writer io.Writer, document historyDocument, location *time.Location) {
	fmt.Fprintf(writer, "\n  Dates: %s (start, or completion when the start is missing).\n", location)
	fmt.Fprintln(writer, "  Durations are recorded work, not parallel wall time.")
	if document.Mode == "incomplete" {
		fmt.Fprintln(writer, "  An unfinished log entry does not prove a process is still running.")
	}
	if document.RecoveredLines > 0 || document.MalformedLines > 0 {
		fmt.Fprintf(writer, "  Log quality: %d NUL-padded records recovered; %d malformed records skipped.\n", document.RecoveredLines, document.MalformedLines)
	}
}
