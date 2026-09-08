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
		"Initial C2 baseline",
		"0.0.5 resolver tuning",
		"0.0.6 repository synchronization",
		"phase protocol",
		"acceptance gates",
	)
	assertPresent(t, readme,
		"arise sync\narise -1 --reinstall =sys-apps/arise-0.0.32",
		"sys-apps/arise ~amd64",
		"| Workload | Arise | Compared with | Other tool | Arise speedup |",
		"| Plan a deep/newuse `@world` update | 3.25 s | `emerge` | 17.98 s | **5.54x** |",
		"[performance results](docs/performance-results.md)",
		"[development guide](docs/development.md)",
		"[documentation index](docs/README.md)",
		"make build",
		"make static",
		"Arise validates a proposed package plan against an independently constructed",
	)

	performance := read("../docs/performance-results.md")
	assertPresent(t, performance,
		"Dependency-aware parallel preparation with serialized commits",
		"installed lifecycle execution, durable journals",
		"General Portage execution parity is not claimed.",
	)

	snapshots := read("../docs/planning/FILESYSTEM_SNAPSHOT_ROLLBACK_PLAN.md")
	assertPresent(t, snapshots,
		"This is not an immutable package store.",
		"### Btrfs",
		"### OpenZFS",
		"### LVM",
		"### OverlayFS and fuse-overlayfs",
		"Overlay providers are not persistent whole-operation snapshot backends.",
		"Each provider is an independently USE-gated implementation.",
		"The static journal/recovery baseline remains fully functional with every",
		"Future `quickpkg` compatibility should add pre-update recovery binpkgs",
		"unit tests for topology, capability, capacity and provider-output parsers;",
		"atomicity tests at every boundary between lock, record publication, snapshot",
	)
	assertAbsent(t, snapshots,
		"snapshot exists, therefore rollback is available",
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
		`recover inspect-set`,
		`recover verify-set`,
		`recover prune-sets`,
		`approve-recovery-drift-sha256`,
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
