package support

import (
	"os"
	"regexp"
	"testing"
)

func TestReleaseVersionReferencesAgree(t *testing.T) {
	t.Parallel()

	want := projectReleaseVersion(t)
	checks := []struct {
		path    string
		pattern string
	}{
		{"../Makefile", `PROJECT_VERSION := ` + regexp.QuoteMeta(want)},
		{"../cmd/arise/main.go", `var version = "` + regexp.QuoteMeta(want) + `"`},
		{"../cmd/arise/version_test.go", `want := version, "` + regexp.QuoteMeta(want) + `"`},
		{"../arise.texi", `@set VERSION ` + regexp.QuoteMeta(want)},
		{"../README.md", `=sys-apps/arise-` + regexp.QuoteMeta(want)},
		{"../docs/releases/" + want + ".md", `# Arise ` + regexp.QuoteMeta(want)},
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

func projectReleaseVersion(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^PROJECT_VERSION := ([0-9]+\.[0-9]+\.[0-9]+)$`).FindSubmatch(data)
	if len(match) != 2 {
		t.Fatal("Makefile does not declare a release version")
	}
	return string(match[1])
}
