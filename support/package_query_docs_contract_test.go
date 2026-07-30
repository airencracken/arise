package support

import (
	"os"
	"strings"
	"testing"
)

func TestPackageQueryExamplesRemainInInstalledManuals(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"../arise.1", "../arise.texi"} {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			manual := strings.ReplaceAll(string(data), `\-`, "-")
			for _, fragment := range []string{
				"Native Package Query Examples",
				"installed",
				"--best",
				"--owner",
				"--check",
				"query",
				"--best-visible",
				"--metadata",
				"--expand-virtual",
				"info",
				"--repositories",
				"--preserved-libs",
				"--search-maintainer",
				"state json",
			} {
				if !strings.Contains(manual, fragment) {
					t.Errorf("%s omits package-query example %q", path, fragment)
				}
			}
			if strings.Contains(manual, "arise equery") {
				t.Errorf("%s retains the removed arise equery command", path)
			}
		})
	}
}
