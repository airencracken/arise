package support

import (
	"os"
	"strings"
	"testing"
)

func TestComplexityAuditRatchetsAndRequiresPerformanceEvidence(t *testing.T) {
	script, err := os.ReadFile("../scripts/check-complexity.sh")
	if err != nil {
		t.Fatal(err)
	}
	report, err := os.ReadFile("../docs/audits/cyclomatic-complexity-2026-08-26.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"actual <= 7.89", "over_fifty > 20", "-over 221"} {
		if !strings.Contains(string(script), required) {
			t.Errorf("complexity ratchet is missing %q", required)
		}
	}
	for _, required := range []string{"wall-clock benchmarks", "allocations", "production-style resolver timings", "was rejected"} {
		if !strings.Contains(string(report), required) {
			t.Errorf("complexity audit is missing performance rule %q", required)
		}
	}
}
