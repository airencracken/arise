package perf

import (
	"strings"
	"testing"
)

func TestMarkdownMatrix(t *testing.T) {
	reports := []Report{{Workload: "search", Results: []Result{{
		Name: "firefox", ReferenceTool: "eix", Equivalent: true,
		PerformancePass: false, AriseMedianNS: 1_000_000_000,
		ReferenceMedianNS: 25_000_000, Speedup: 0.025,
	}}}}
	got := MarkdownMatrix(reports)
	for _, want := range []string{"| search | firefox | eix | yes | **no** | **no** | 1s | 25ms | **0.025x** |", "| Workload |"} {
		if !strings.Contains(got, want) {
			t.Fatalf("matrix missing %q:\n%s", want, got)
		}
	}
}
