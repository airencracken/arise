package support

import (
	"os"
	"strings"
	"testing"
)

func TestComplexityRatchetsAndRequiresPerformanceEvidence(t *testing.T) {
	script, err := os.ReadFile("../scripts/check-complexity.sh")
	if err != nil {
		t.Fatal(err)
	}
	report, err := os.ReadFile("../docs/development.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"actual <= 7.87", "over_fifty > 19", "-over 215"} {
		if !strings.Contains(string(script), required) {
			t.Errorf("complexity ratchet is missing %q", required)
		}
	}
	for _, required := range []string{"wall-clock benchmarks", "allocations", "production-style resolver timings", "Reject cosmetic complexity improvements"} {
		if !strings.Contains(string(report), required) {
			t.Errorf("development guide is missing performance rule %q", required)
		}
	}
}
