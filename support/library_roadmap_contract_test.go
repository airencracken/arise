package support

import (
	"os"
	"strings"
	"testing"
)

func TestLibraryRoadmapRecordsExternalConsumerBoundary(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("../docs/library-roadmap.md")
	if err != nil {
		t.Fatal(err)
	}
	roadmap := string(data)
	for _, fragment := range []string{
		"Shared module: gentooling",
		"github.com/airencracken/gentooling",
		"Reusable Go libraries for Gentoo system and package tooling.",
		"Arise and Maize should become peer applications",
		"first extracted surface provides explicit system paths",
		"partial inspection with typed issues",
		"strict validation",
		"Arise consumes this API for its VDB scans",
		"Gentooling `v0.1.0`",
		"structured IUSE defaults",
		"bounded concurrent record scans",
		"final mutation revalidation",
		"pre-1.0 compatibility policy",
		"Gentooling `v0.2.0`",
		"root-to-leaf layers",
		"source-line provenance",
		"rejecting parent escapes",
		"Arise now uses this graph",
		"Concrete downstream consumer: Maize",
		"hardware- and package-informed",
		"not a requirement to merge kernel tooling into Arise",
		"If Maize must execute `arise` and parse human output",
		"Portage configuration loading and effective per-package policy",
		"dependency and reverse-dependency inspection",
		"hardware- and package-capability discovery",
		"`context.Context`, explicit roots",
		"remain usable with `CGO_ENABLED=0`",
		"the Arise executable",
		"downstream compile test",
		"future kernel build and installation tool",
		"Neither concern belongs in Gentooling",
	} {
		if !strings.Contains(roadmap, fragment) {
			t.Errorf("library roadmap omits external-consumer contract %q", fragment)
		}
	}
}
