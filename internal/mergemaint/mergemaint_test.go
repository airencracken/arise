package mergemaint

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/journal"
)

func TestCheckUnionsVDBAndTrackingDeterministically(t *testing.T) {
	root := t.TempDir()
	vdb := filepath.Join(root, "var/db/pkg")
	tracking := filepath.Join(root, "var/lib/portage/failed-merges")
	present := filepath.Join(vdb, "dev-util", "pkgcheck-0.10.40-r1-MERGING-")
	if err := os.MkdirAll(present, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(tracking), 0o755); err != nil {
		t.Fatal(err)
	}
	data := "sys-apps/portage-3.0.70-MERGING- 17\ndev-util/pkgcheck-0.10.40-r1-MERGING- 11\n"
	if err := os.WriteFile(tracking, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := Check(vdb, tracking)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Failed) != 2 {
		t.Fatalf("failed entries = %d, want 2: %#v", len(report.Failed), report)
	}
	if got := []string{report.Failed[0].Entry, report.Failed[1].Entry}; !reflect.DeepEqual(got, []string{
		"dev-util/pkgcheck-0.10.40-r1-MERGING-",
		"sys-apps/portage-3.0.70-MERGING-",
	}) {
		t.Fatalf("entries = %v", got)
	}
	first := report.Failed[0]
	if first.Atom != "=dev-util/pkgcheck-0.10.40-r1" || !first.Present || !first.Tracked {
		t.Fatalf("present entry = %#v", first)
	}
	if report.Failed[1].Present || !report.Failed[1].Tracked || report.Failed[1].MTimeUnix != 17 {
		t.Fatalf("tracked entry = %#v", report.Failed[1])
	}
}

func TestCheckRejectsAdversarialTrackingEntries(t *testing.T) {
	cases := []string{
		"dev-util/pkgcheck-1-MERGING-\n",
		"../dev-util/pkgcheck-1-MERGING- 1\n",
		"dev-util/../../pkgcheck-1-MERGING- 1\n",
		"dev-util/pkgcheck-1 1\n",
		"dev-util/pkgcheck-1-MERGING- nope\n",
	}
	for _, data := range cases {
		t.Run(strings.ReplaceAll(data, "/", "_"), func(t *testing.T) {
			root := t.TempDir()
			tracking := filepath.Join(root, "failed-merges")
			if err := os.WriteFile(tracking, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Check(filepath.Join(root, "missing-vdb"), tracking); err == nil {
				t.Fatalf("Check accepted %q", data)
			}
		})
	}
}

func TestSaveAndPurgeTrackingAreValidatedAndIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "failed-merges")
	failed := []Failed{
		{Entry: "sys-apps/portage-3.0.70-MERGING-", MTimeUnix: 2},
		{Entry: "dev-util/pkgcheck-0.10.40-r1-MERGING-", MTimeUnix: 1},
	}
	if err := SaveTracking(path, failed); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "dev-util/pkgcheck-0.10.40-r1-MERGING- 1\nsys-apps/portage-3.0.70-MERGING- 2"
	if string(data) != want {
		t.Fatalf("tracking = %q, want %q", data, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	if err := SaveTracking(path, []Failed{{Entry: "../../escape-MERGING-", MTimeUnix: 1}}); err == nil {
		t.Fatal("SaveTracking accepted traversal")
	}
	if err := PurgeTracking(path); err != nil {
		t.Fatal(err)
	}
	if err := PurgeTracking(path); err != nil {
		t.Fatalf("second purge: %v", err)
	}
	if err := PurgeTracking(filepath.Join(rootOfMissing(t), "absent", "failed-merges")); err != nil {
		t.Fatalf("purge with missing parent: %v", err)
	}
}

func rootOfMissing(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "missing")
}

func TestCleanupRemovesOnlyValidatedVDBEntries(t *testing.T) {
	root := t.TempDir()
	vdb := filepath.Join(root, "var/db/pkg")
	path := filepath.Join(vdb, "dev-util", "pkgcheck-1-MERGING-")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "CONTENTS"), []byte("state"), 0o644); err != nil {
		t.Fatal(err)
	}
	failed := []Failed{{Path: path, Entry: "dev-util/pkgcheck-1-MERGING-", Present: true}}
	if err := Cleanup(root, vdb, filepath.Join(root, "journal"), failed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("failed directory remains: %v", err)
	}
	if err := Cleanup(root, vdb, filepath.Join(root, "journal-2"), failed); err != nil {
		t.Fatalf("idempotent cleanup: %v", err)
	}

	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	failed[0].Path = outside
	if err := Cleanup(root, vdb, filepath.Join(root, "journal-3"), failed); err == nil {
		t.Fatal("Cleanup accepted a path outside the VDB")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside directory changed: %v", err)
	}
}

func TestCleanupRollsBackOnRemovalFailure(t *testing.T) {
	root := t.TempDir()
	vdb := filepath.Join(root, "var/db/pkg")
	var failed []Failed
	for _, name := range []string{"one-1-MERGING-", "two-1-MERGING-"} {
		path := filepath.Join(vdb, "cat", name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "CONTENTS"), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		failed = append(failed, Failed{Path: path, Entry: "cat/" + name, Present: true})
	}
	oldRemove := removeTree
	t.Cleanup(func() { removeTree = oldRemove })
	calls := 0
	removeTree = func(operation *journal.Journal, path string) error {
		calls++
		if calls == 2 {
			return errors.New("injected removal failure")
		}
		return operation.RemoveTree(path)
	}
	err := Cleanup(root, vdb, filepath.Join(root, "journal"), failed)
	if err == nil || !strings.Contains(err.Error(), "injected removal failure") {
		t.Fatalf("Cleanup error = %v", err)
	}
	for _, entry := range failed {
		data, readErr := os.ReadFile(filepath.Join(entry.Path, "CONTENTS"))
		if readErr != nil || string(data) != filepath.Base(entry.Path) {
			t.Fatalf("rollback did not restore %s: data=%q err=%v", entry.Path, data, readErr)
		}
	}
}
