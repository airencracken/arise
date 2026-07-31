package support

import (
	"os"
	"regexp"
	"testing"
)

func TestReleaseVersionReferencesAgree(t *testing.T) {
	t.Parallel()

	const want = "0.0.15"
	checks := []struct {
		path    string
		pattern string
	}{
		{"../Makefile", `PROJECT_VERSION := ` + regexp.QuoteMeta(want)},
		{"../cmd/arise/main.go", `var version = "` + regexp.QuoteMeta(want) + `"`},
		{"../cmd/arise/version_test.go", `want := version, "` + regexp.QuoteMeta(want) + `"`},
		{"../arise.texi", `@set VERSION ` + regexp.QuoteMeta(want)},
		{"../README.md", `=sys-apps/arise-` + regexp.QuoteMeta(want)},
		{"../docs/releases/0.0.15.md", `# Arise ` + regexp.QuoteMeta(want)},
	}

	for _, check := range checks {
		check := check
		t.Run(check.path, func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(check.path)
			if err != nil {
				t.Fatal(err)
			}
			if !regexp.MustCompile(check.pattern).Match(data) {
				t.Fatalf("%s does not contain release version %s", check.path, want)
			}
		})
	}
}
