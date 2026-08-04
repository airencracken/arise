package support

import (
	"os"
	"strings"
	"testing"
)

func TestPerformancePlanRequiresGentooHostProvenanceForParityClaims(t *testing.T) {
	data, err := os.ReadFile("../docs/planning/PERFORMANCE_IMPROVEMENT_PLAN.md")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(string(data)), " ")
	for _, required := range []string{
		"development evidence only", "authoritative comparison requires a Gentoo execution host",
		"same frozen inputs", "cold, warm-distfile and warm-compiler-cache trials",
		"CachyOS workstation measurements", "cannot satisfy this gate",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("performance plan lost provenance contract %q", required)
		}
	}
}
