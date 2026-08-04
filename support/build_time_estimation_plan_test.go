package support

import (
	"os"
	"strings"
	"testing"
)

func TestBuildTimeEstimationPlanRetainsDistributedPrivacyAndTradeoffContracts(t *testing.T) {
	data, err := os.ReadFile("../docs/planning/BUILD_TIME_ESTIMATION_PLAN.md")
	if err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(strings.Fields(string(data)), " ")
	required := []string{
		"explicit opt-in",
		"allowlist",
		"Never report hostnames, IP addresses, usernames",
		"client-specific scaling factor",
		"Scale phase models independently",
		"effective weighted sample size",
		"local source build time",
		"package-farm queue plus build time",
		"telemetry disabled means no report is queued, written, or sent",
		"Estimates remain advisory",
		"Treat “bloat” as a vector",
		"installed exclusive bytes",
		"dependency-closure package count and bytes",
		"shared dependencies once",
		"release-to-release and rolling-baseline deltas",
		"labeled discontinuities",
		"never compare incompatible build configurations",
	}
	for _, contract := range required {
		if !strings.Contains(plan, contract) {
			t.Errorf("distributed estimation plan lost contract %q", contract)
		}
	}
}

func TestBuildTimeEstimationPlanRequiresCorpusFallbackDisclosure(t *testing.T) {
	data, err := os.ReadFile("../docs/planning/BUILD_TIME_ESTIMATION_PLAN.md")
	if err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(strings.Fields(string(data)), " ")
	for _, tier := range []string{
		"same CPV and similar hardware",
		"same package and nearby versions",
		"calibrated client scaling",
		"architecture-wide baseline",
		"unavailable",
	} {
		if !strings.Contains(plan, tier) {
			t.Errorf("distributed estimation fallback tier %q is undocumented", tier)
		}
	}
}
