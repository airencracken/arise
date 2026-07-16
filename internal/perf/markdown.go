package perf

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// MarkdownMatrix renders correctness-gated reports as a stable README table.
func MarkdownMatrix(reports []Report) string {
	type row struct {
		workload string
		result   Result
	}
	var rows []row
	for _, report := range reports {
		for _, result := range report.Results {
			rows = append(rows, row{workload: report.Workload, result: result})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].workload != rows[j].workload {
			return rows[i].workload < rows[j].workload
		}
		return rows[i].result.Name < rows[j].result.Name
	})
	var b strings.Builder
	b.WriteString("| Workload | Test | Reference | Correct | Performance gate | Overall | Arise median | Reference median | Speedup | Arise cache | Reference cache |\n")
	b.WriteString("|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, entry := range rows {
		r := entry.result
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			escapeCell(entry.workload), escapeCell(r.Name), escapeCell(r.ReferenceTool), yesNo(r.Equivalent),
			yesNo(r.PerformancePass), yesNo(r.Equivalent && r.PerformancePass), formatDuration(r.AriseMedianNS), formatDuration(r.ReferenceMedianNS), formatSpeedup(r.Speedup), formatBytes(r.AriseCacheBytes), formatBytes(r.ReferenceCacheBytes)))
	}
	return b.String()
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "**no**"
}

func formatDuration(ns int64) string {
	if ns <= 0 {
		return "—"
	}
	return time.Duration(ns).Round(time.Microsecond).String()
}

func formatSpeedup(value float64) string {
	if value <= 0 {
		return "—"
	}
	if value < 1 {
		return fmt.Sprintf("**%.3fx**", value)
	}
	return fmt.Sprintf("%.2fx", value)
}

func formatBytes(value int64) string {
	if value <= 0 {
		return "—"
	}
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	div, exp := int64(unit), 0
	for n := value / unit; n >= unit && exp < 5; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(value)/float64(div), "KMGTPE"[exp])
}

func escapeCell(value string) string { return strings.ReplaceAll(value, "|", "\\|") }
