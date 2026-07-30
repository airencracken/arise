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
	} {
		if !strings.Contains(roadmap, fragment) {
			t.Errorf("library roadmap omits external-consumer contract %q", fragment)
		}
	}
}
