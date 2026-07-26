package support

import (
	"os"
	"strings"
	"testing"
)

func TestActiveDocumentationTracksImplementedOperationalSurface(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	readme := read("../README.md")
	assertAbsent(t, readme,
		"Not yet claimed:",
		"Parallel scheduling, fetch policy,\n  lifecycle coverage, failure recovery",
	)
	assertPresent(t, readme,
		"arise sync\narise -1 --reinstall =sys-apps/arise-0.0.1",
		"Dependency-aware parallel preparation with serialized commits",
		"installed lifecycle execution, durable journals",
		"General Portage execution parity is still not claimed.",
	)

	punchlist := read("../PUNCHLIST.md")
	assertAbsent(t, punchlist,
		"- [ ] Implement pkg_config execution.",
		"- [ ] Complete dispatch-conf-style recursive config management:",
		"Install/update still fail explicitly\n  after a successful non-pretend plan",
		"- [ ] Update world only for successful explicit installs and respect oneshot.",
		"- [ ] Implement uninstall with reverse-dependency safety.",
		"- [!] Connect resolved plans to fetch/build/binpkg/merge/unmerge execution.",
	)
	assertPresent(t, punchlist,
		"- [x] Implement installed `pkg_config` execution.",
		"- [~] Complete dispatch-conf-style recursive config management.",
		"- [x] Update world only after successful explicit installs and respect oneshot.",
		"- [x] Implement exact uninstall with whole-state reverse-dependency and reverse-",
		"- [~] Connect resolved plans to fetch/build/binpkg/merge/unmerge execution.",
	)

	matrix := read("../docs/compatibility/PORTAGE_COMPATIBILITY_MATRIX.md")
	assertPresent(t, matrix,
		"| installed `pkg_config` phase | `arise config ATOM` | supported subset |",
		"| `dispatch-conf` | `arise dispatch-conf` | partial |",
	)

	manual := read("../arise.1")
	assertAbsent(t, manual, `Replaces \fBdispatch-conf\fR(1).`)
	assertPresent(t, manual,
		"Provides a usable replacement workflow.",
		"confined atomic archives, mixed-file diffs, and interruption-aware processing.",
		"command-level differential parity remain\nunder development.",
	)
}

func assertPresent(t *testing.T, text string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(text, value) {
			t.Errorf("documentation omits current contract %q", value)
		}
	}
}

func assertAbsent(t *testing.T, text string, values ...string) {
	t.Helper()
	for _, value := range values {
		if strings.Contains(text, value) {
			t.Errorf("documentation retains stale text %q", value)
		}
	}
}
