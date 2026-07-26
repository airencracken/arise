package ingest

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/metadata"
	"github.com/dgraph-io/badger/v4"
)

func openTestDB(t *testing.T) *badger.DB {
	t.Helper()
	opts := badger.DefaultOptions("").WithInMemory(true).WithLogger(nil)
	db, err := badger.Open(opts)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func sendEntries(t *testing.T, entries ...*metadata.PackageMetadata) <-chan *metadata.PackageMetadata {
	t.Helper()
	ch := make(chan *metadata.PackageMetadata, len(entries))
	for _, e := range entries {
		ch <- e
	}
	close(ch)
	return ch
}

func TestOpenDB(t *testing.T) {
	db := openTestDB(t)
	if db == nil {
		t.Fatal("expected non-nil db")
	}
}

func TestSchemaVersionIsInitializedAndFutureSchemaRejected(t *testing.T) {
	db := openTestDB(t)
	previousWriter := WriterVersion
	WriterVersion = "test-version"
	t.Cleanup(func() { WriterVersion = previousWriter })
	if err := ensureSchema(db, true); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(schemaKey))
		if err != nil {
			return err
		}
		return item.Value(func(value []byte) error { got = string(value); return nil })
	}); err != nil {
		t.Fatal(err)
	}
	if got != "3" {
		t.Fatalf("schema = %q", got)
	}
	if err := db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(writerKey))
		if err != nil {
			return err
		}
		return item.Value(func(value []byte) error {
			if string(value) != "test-version" {
				t.Fatalf("writer = %q", value)
			}
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(txn *badger.Txn) error { return txn.Set([]byte(schemaKey), []byte("999")) }); err != nil {
		t.Fatal(err)
	}
	if err := ensureSchema(db, false); err == nil {
		t.Fatal("future database schema was accepted")
	}
}

func TestResetPackageIndex(t *testing.T) {
	db := openTestDB(t)
	entry := &metadata.PackageMetadata{Repository: "gentoo", Category: "sys-apps", Package: "portage", Version: "3.0", SLOT: "0"}
	if _, err := Ingest(db, sendEntries(t, entry)); err != nil {
		t.Fatal(err)
	}
	if err := ResetPackageIndex(db); err != nil {
		t.Fatal(err)
	}
	got, err := Query(db, "sys-apps/portage")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("package remained after reset: %+v", got)
	}
	if records, err := QuerySlot(db, "0"); err != nil || len(records) != 0 {
		t.Fatalf("slot index remained after reset: %d records, %v", len(records), err)
	}
	if records, err := QueryRepository(db, "gentoo"); err != nil || len(records) != 0 {
		t.Fatalf("repository index remained after reset: %d records, %v", len(records), err)
	}
}

func TestIngestRoundTrip(t *testing.T) {
	db := openTestDB(t)

	m := &metadata.PackageMetadata{
		Category:    "sys-devel",
		Package:     "gcc",
		Version:     "12.2.0",
		SLOT:        "12",
		Subslot:     "12.2",
		DESCRIPTION: "The GNU Compiler Collection",
		HOMEPAGE:    "https://gcc.gnu.org",
		LICENSE:     "GPL-3",
		EAPI:        "8",
	}

	count, err := Ingest(db, sendEntries(t, m))
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected count=1, got %d", count)
	}
	if records, err := CountRecords(db); err != nil || records != 1 {
		t.Fatalf("CountRecords = %d, %v", records, err)
	}

	got, err := Query(db, "sys-devel/gcc")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if got == nil {
		t.Fatal("Query returned nil")
	}

	if got.Category != m.Category {
		t.Errorf("Category: got %q, want %q", got.Category, m.Category)
	}
	if got.Package != m.Package {
		t.Errorf("Package: got %q, want %q", got.Package, m.Package)
	}
	if got.Version != m.Version {
		t.Errorf("Version: got %q, want %q", got.Version, m.Version)
	}
	if got.SLOT != m.SLOT {
		t.Errorf("SLOT: got %q, want %q", got.SLOT, m.SLOT)
	}
	if got.Subslot != m.Subslot {
		t.Errorf("Subslot: got %q, want %q", got.Subslot, m.Subslot)
	}
	if got.DESCRIPTION != m.DESCRIPTION {
		t.Errorf("DESCRIPTION: got %q, want %q", got.DESCRIPTION, m.DESCRIPTION)
	}
	if got.HOMEPAGE != m.HOMEPAGE {
		t.Errorf("HOMEPAGE: got %q, want %q", got.HOMEPAGE, m.HOMEPAGE)
	}
	if got.LICENSE != m.LICENSE {
		t.Errorf("LICENSE: got %q, want %q", got.LICENSE, m.LICENSE)
	}
	if got.EAPI != m.EAPI {
		t.Errorf("EAPI: got %q, want %q", got.EAPI, m.EAPI)
	}
}

func TestQueryNotFound(t *testing.T) {
	db := openTestDB(t)

	got, err := Query(db, "nonexistent/package")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing package, got %+v", got)
	}
}

func TestQueryEmptyDB(t *testing.T) {
	db := openTestDB(t)

	got, err := Query(db, "sys-apps/portage")
	if err != nil {
		t.Fatalf("Query on empty DB: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil query on empty DB, got %+v", got)
	}
}

func TestReconcileNoChangeAndRemoveMissing(t *testing.T) {
	db := openTestDB(t)
	first := sendEntries(t,
		&metadata.PackageMetadata{Category: "app-editors", Package: "vim", Version: "9.0"},
		&metadata.PackageMetadata{Category: "app-editors", Package: "vim", Version: "9.1"},
		&metadata.PackageMetadata{Category: "sys-apps", Package: "portage", Version: "3.0"},
	)
	stats, seen, err := ReconcileWithProgress(db, first, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Changed != 3 || len(seen) != 3 {
		t.Fatalf("first reconcile stats = %+v, seen=%d", stats, len(seen))
	}
	vim, err := Query(db, "app-editors/vim")
	if err != nil || vim.Version != "9.1" {
		t.Fatalf("selected vim = %+v, err=%v", vim, err)
	}

	second := sendEntries(t, &metadata.PackageMetadata{Category: "app-editors", Package: "vim", Version: "9.1"})
	stats, seen, err = ReconcileWithProgress(db, second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Changed != 0 || stats.Unchanged != 1 {
		t.Fatalf("no-change reconcile stats = %+v", stats)
	}
	removed, err := RemoveMissing(db, seen)
	if err != nil || removed != 2 {
		t.Fatalf("RemoveMissing = %d, %v", removed, err)
	}
	portage, err := Query(db, "sys-apps/portage")
	if err != nil || portage != nil {
		t.Fatalf("stale package remains: %+v, %v", portage, err)
	}
}

func TestReconcilePreservesVersionsAndRepositories(t *testing.T) {
	db := openTestDB(t)
	entries := sendEntries(t,
		&metadata.PackageMetadata{Repository: "gentoo", RepositoryPath: "/repos/gentoo", Category: "www-client", Package: "firefox", Version: "140.12.0", OverlayIndex: 0},
		&metadata.PackageMetadata{Repository: "gentoo", RepositoryPath: "/repos/gentoo", Category: "www-client", Package: "firefox", Version: "152.0.6", OverlayIndex: 0},
		&metadata.PackageMetadata{Repository: "local", RepositoryPath: "/repos/local", Category: "www-client", Package: "firefox", Version: "152.0.6", OverlayIndex: 1},
	)
	stats, seen, err := ReconcileWithProgress(db, entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Changed != 3 || len(seen) != 3 {
		t.Fatalf("records collapsed during reconcile: stats=%+v seen=%d", stats, len(seen))
	}
	records, err := QueryVersions(db, "www-client/firefox")
	if err != nil || len(records) != 3 {
		t.Fatalf("QueryVersions returned %d records, err=%v", len(records), err)
	}
	preferred, err := Query(db, "www-client/firefox")
	if err != nil {
		t.Fatal(err)
	}
	if preferred == nil || preferred.Version != "152.0.6" || preferred.Repository != "local" {
		t.Fatalf("preferred record = %+v", preferred)
	}
	var keys []string
	if err := QueryKeys(db, "pkg:", func(cp string) error { keys = append(keys, cp); return nil }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(keys, []string{"www-client/firefox"}) {
		t.Fatalf("secondary CP keys = %v", keys)
	}
}

func TestSecondaryIndexesTrackUpdatesAndRemoval(t *testing.T) {
	db := openTestDB(t)
	record := &metadata.PackageMetadata{Repository: "gentoo", RepositoryPath: "/repos/gentoo", Category: "dev-lang", Package: "python", Version: "3.12.4", SLOT: "3.12"}
	_, seen, err := ReconcileWithProgress(db, sendEntries(t, record), nil)
	if err != nil {
		t.Fatal(err)
	}
	bySlot, err := QuerySlot(db, "3.12")
	if err != nil || len(bySlot) != 1 {
		t.Fatalf("slot index = %d, %v", len(bySlot), err)
	}
	byRepo, err := QueryRepository(db, "gentoo")
	if err != nil || len(byRepo) != 1 {
		t.Fatalf("repo index = %d, %v", len(byRepo), err)
	}

	updated := *record
	updated.SLOT = "3.13"
	_, seen, err = ReconcileWithProgress(db, sendEntries(t, &updated), nil)
	if err != nil {
		t.Fatal(err)
	}
	oldSlot, err := QuerySlot(db, "3.12")
	if err != nil || len(oldSlot) != 0 {
		t.Fatalf("stale slot index = %d, %v", len(oldSlot), err)
	}
	newSlot, err := QuerySlot(db, "3.13")
	if err != nil || len(newSlot) != 1 {
		t.Fatalf("new slot index = %d, %v", len(newSlot), err)
	}

	if removed, err := RemoveMissing(db, map[string]struct{}{}); err != nil || removed != 1 {
		t.Fatalf("RemoveMissing = %d, %v (seen=%d)", removed, err, len(seen))
	}
	newSlot, err = QuerySlot(db, "3.13")
	if err != nil || len(newSlot) != 0 {
		t.Fatalf("removed slot index = %d, %v", len(newSlot), err)
	}
	byRepo, err = QueryRepository(db, "gentoo")
	if err != nil || len(byRepo) != 0 {
		t.Fatalf("removed repo index = %d, %v", len(byRepo), err)
	}
}

func TestVisibilityInputIndexes(t *testing.T) {
	db := openTestDB(t)
	record := &metadata.PackageMetadata{Repository: "gentoo", Category: "app-editors", Package: "vim", Version: "9.1", KEYWORDS: "amd64 ~arm64", LICENSE: "vim GPL-2", EAPI: "8"}
	if _, _, err := ReconcileWithProgress(db, sendEntries(t, record), nil); err != nil {
		t.Fatal(err)
	}
	for name, query := range map[string]func(*badger.DB, string) ([]*metadata.PackageMetadata, error){"keyword": QueryKeyword, "license": QueryLicense, "eapi": QueryEAPI} {
		value := map[string]string{"keyword": "~arm64", "license": "GPL-2", "eapi": "8"}[name]
		records, err := query(db, value)
		if err != nil || len(records) != 1 || records[0].CPV() != record.CPV() {
			t.Fatalf("%s index = %+v, %v", name, records, err)
		}
	}
}

func TestFingerprintDetectsCacheDigestAndMtimeOnlyChanges(t *testing.T) {
	db := openTestDB(t)
	record := &metadata.PackageMetadata{Repository: "gentoo", Category: "app-misc", Package: "fingerprint", Version: "1.0", Unknown: map[string]string{"_md5_": "one", "_mtime_": "1"}}
	stats, _, err := ReconcileWithProgress(db, sendEntries(t, record), nil)
	if err != nil || stats.Changed != 1 {
		t.Fatalf("first reconcile = %+v, %v", stats, err)
	}
	updated := *record
	updated.Unknown = map[string]string{"_md5_": "two", "_mtime_": "2"}
	stats, _, err = ReconcileWithProgress(db, sendEntries(t, &updated), nil)
	if err != nil || stats.Changed != 1 || stats.Unchanged != 0 {
		t.Fatalf("digest-only reconcile = %+v, %v", stats, err)
	}
}

func TestReconcileIsIndependentOfConcurrentArrivalOrder(t *testing.T) {
	entries := []*metadata.PackageMetadata{
		{Repository: "gentoo", RepositoryPath: "/repos/gentoo", Category: "app-misc", Package: "order-test", Version: "1.0", DESCRIPTION: "alpha"},
		{Repository: "gentoo", RepositoryPath: "/repos/gentoo", Category: "app-misc", Package: "order-test", Version: "1.0", DESCRIPTION: "beta"},
		{Repository: "gentoo", RepositoryPath: "/repos/gentoo", Category: "app-misc", Package: "order-test", Version: "1.0", DESCRIPTION: "gamma"},
	}
	orders := [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	var want [32]byte
	for i, order := range orders {
		db := openTestDB(t)
		ordered := make([]*metadata.PackageMetadata, 0, len(order))
		for _, index := range order {
			ordered = append(ordered, entries[index])
		}
		if _, _, err := ReconcileWithProgress(db, sendEntries(t, ordered...), nil); err != nil {
			t.Fatal(err)
		}
		got, err := Query(db, "app-misc/order-test")
		if err != nil {
			t.Fatal(err)
		}
		fingerprint, err := metadata.Fingerprint(got)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			want = fingerprint
		} else if fingerprint != want {
			t.Fatalf("arrival order %v selected a different record", order)
		}
	}
}

func TestQueryRange(t *testing.T) {
	db := openTestDB(t)

	entries := []*metadata.PackageMetadata{
		{Category: "sys-devel", Package: "gcc"},
		{Category: "sys-devel", Package: "make"},
		{Category: "sys-apps", Package: "portage"},
		{Category: "sys-devel", Package: "binutils"},
	}

	ch := make(chan *metadata.PackageMetadata, len(entries))
	for _, e := range entries {
		ch <- e
	}
	close(ch)

	count, err := Ingest(db, ch)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if count != len(entries) {
		t.Fatalf("expected %d ingested, got %d", len(entries), count)
	}

	t.Run("all packages", func(t *testing.T) {
		var found []string
		err := QueryRange(db, "pkg:", func(m *metadata.PackageMetadata) error {
			found = append(found, m.Key())
			return nil
		})
		if err != nil {
			t.Fatalf("QueryRange: %v", err)
		}
		if len(found) != len(entries) {
			t.Errorf("expected %d entries, got %d", len(entries), len(found))
		}
	})

	t.Run("category prefix", func(t *testing.T) {
		var found []string
		err := QueryRange(db, "pkg:sys-devel/", func(m *metadata.PackageMetadata) error {
			found = append(found, m.Key())
			return nil
		})
		if err != nil {
			t.Fatalf("QueryRange: %v", err)
		}
		if len(found) != 3 {
			t.Errorf("expected 3 sys-devel entries, got %d: %v", len(found), found)
		}
	})

	t.Run("keys without values", func(t *testing.T) {
		var found []string
		err := QueryKeys(db, "pkg:sys-devel/", func(cp string) error {
			found = append(found, cp)
			return nil
		})
		if err != nil {
			t.Fatalf("QueryKeys: %v", err)
		}
		want := []string{"sys-devel/binutils", "sys-devel/gcc", "sys-devel/make"}
		if !reflect.DeepEqual(found, want) {
			t.Fatalf("QueryKeys = %v, want %v", found, want)
		}
	})

	t.Run("no match prefix", func(t *testing.T) {
		var found []string
		err := QueryRange(db, "pkg:nonexistent/", func(m *metadata.PackageMetadata) error {
			found = append(found, m.Key())
			return nil
		})
		if err != nil {
			t.Fatalf("QueryRange: %v", err)
		}
		if len(found) != 0 {
			t.Errorf("expected 0 entries, got %d", len(found))
		}
	})
}

func TestPropertyQueryRangeParallelMatchesSerialOrderAndStoppingContracts(t *testing.T) {
	db := openTestDB(t)
	entries := []*metadata.PackageMetadata{
		{Repository: "gentoo", Category: "app-editors", Package: "vim", Version: "9.1"},
		{Repository: "gentoo", Category: "dev-lang", Package: "go", Version: "1.26"},
		{Repository: "local", Category: "dev-lang", Package: "go", Version: "1.27"},
		{Repository: "gentoo", Category: "sys-apps", Package: "portage", Version: "3.0"},
	}
	if _, err := Ingest(db, sendEntries(t, entries...)); err != nil {
		t.Fatal(err)
	}
	var serial []string
	if err := QueryRange(db, keyPrefix, func(entry *metadata.PackageMetadata) error {
		serial = append(serial, entry.RepositoryCPVKey())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, workers := range []int{1, 2, 7, 0, -1} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			var parallel []string
			if err := QueryRangeParallel(db, keyPrefix, workers, func(entry *metadata.PackageMetadata) error {
				parallel = append(parallel, entry.RepositoryCPVKey())
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(parallel, serial) {
				t.Fatalf("parallel order = %v, serial = %v", parallel, serial)
			}
		})
	}

	stop := errors.New("callback stop")
	calls := 0
	err := QueryRangeParallel(db, keyPrefix, 4, func(*metadata.PackageMetadata) error {
		calls++
		if calls == 2 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || calls != 2 {
		t.Fatalf("callback error = %v after %d calls", err, calls)
	}
	calls = 0
	if err := QueryRangeParallel(db, keyPrefix, 4, func(*metadata.PackageMetadata) error {
		calls++
		return io.EOF
	}); err != nil || calls != 1 {
		t.Fatalf("EOF stop error = %v after %d calls", err, calls)
	}
}

func TestAtomicityQueryRangeParallelDecodesAllRecordsBeforeCallbacks(t *testing.T) {
	db := openTestDB(t)
	valid := &metadata.PackageMetadata{Repository: "gentoo", Category: "sys-apps", Package: "portage", Version: "3.0"}
	if _, err := Ingest(db, sendEntries(t, valid)); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(keyPrefix+"aaa/corrupt\x00identity"), []byte("not a gob record"))
	}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	err := QueryRangeParallel(db, keyPrefix, 4, func(*metadata.PackageMetadata) error {
		calls++
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("corrupt parallel query error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("parallel query invoked %d callbacks before rejecting corrupt snapshot", calls)
	}
}

func TestIngestBatchExceedsMaxBatchSize(t *testing.T) {
	db := openTestDB(t)

	n := maxBatchSize + 500
	ch := make(chan *metadata.PackageMetadata, n)
	for i := 0; i < n; i++ {
		ch <- &metadata.PackageMetadata{
			Category: "test",
			Package:  "pkg",
			Version:  "1.0",
		}
	}
	close(ch)

	count, err := Ingest(db, ch)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if count != n {
		t.Errorf("expected %d entries, got %d", n, count)
	}
}

func TestIngestProgressReportsCommittedBatchesExactlyOnce(t *testing.T) {
	tests := []struct {
		name string
		size int
		want []int
	}{
		{"empty", 0, nil},
		{"partial batch", 3, []int{3}},
		{"exact batch", maxBatchSize, []int{maxBatchSize}},
		{"batch plus one", maxBatchSize + 1, []int{maxBatchSize, maxBatchSize + 1}},
		{"two exact batches", maxBatchSize * 2, []int{maxBatchSize, maxBatchSize * 2}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openTestDB(t)
			entries := make([]*metadata.PackageMetadata, 0, test.size)
			for index := 0; index < test.size; index++ {
				entries = append(entries, &metadata.PackageMetadata{
					Repository: "test",
					Category:   "cat",
					Package:    "pkg",
					Version:    fmt.Sprintf("%d", index),
				})
			}
			var reports []int
			count, err := IngestWithProgress(db, sendEntries(t, entries...), func(committed int) {
				reports = append(reports, committed)
				if records, countErr := CountRecords(db); countErr != nil || records != committed {
					t.Fatalf("progress(%d) observed %d records, err=%v", committed, records, countErr)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if count != test.size {
				t.Fatalf("count = %d, want %d", count, test.size)
			}
			if !reflect.DeepEqual(reports, test.want) {
				t.Fatalf("progress reports = %v, want %v", reports, test.want)
			}
		})
	}
}

func TestIngestNilEntries(t *testing.T) {
	db := openTestDB(t)

	ch := make(chan *metadata.PackageMetadata, 5)
	ch <- &metadata.PackageMetadata{Category: "a", Package: "b"}
	ch <- nil
	ch <- nil
	ch <- &metadata.PackageMetadata{Category: "c", Package: "d"}
	ch <- nil
	close(ch)

	count, err := Ingest(db, ch)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 non-nil entries, got %d", count)
	}
}

func TestQueryRangeWithErrorStop(t *testing.T) {
	db := openTestDB(t)

	ch := sendEntries(t, &metadata.PackageMetadata{
		Category: "test",
		Package:  "pkg",
		Version:  "1.0",
	})
	_, err := Ingest(db, ch)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	var calls int
	stopErr := errors.New("stop early")

	err = QueryRange(db, "pkg:test/", func(m *metadata.PackageMetadata) error {
		calls++
		return stopErr
	})
	if err != stopErr {
		t.Errorf("expected stopErr, got %v", err)
	}
	if calls == 0 {
		t.Error("expected at least one callback invocation")
	}
}

func TestEncodeKey(t *testing.T) {
	key := encodeKey("sys-devel/gcc")
	expected := "pkg:sys-devel/gcc"
	if string(key) != expected {
		t.Errorf("encodeKey: got %q, want %q", string(key), expected)
	}
}

func TestIngestSliceFields(t *testing.T) {
	db := openTestDB(t)

	m := &metadata.PackageMetadata{
		Category: "sys-devel",
		Package:  "gcc",
		KEYWORDS: "",
		IUSE:     "",
	}

	// Set through Unknown map to test gob round-trip
	m.Unknown = map[string]string{
		"KEYWORDS": "amd64 x86 ~arm64",
		"IUSE":     "fortran openmp lto",
		"DEPEND":   ">=dev-libs/gmp-6.2 >=dev-libs/mpfr-4.1",
		"RDEPEND":  "dev-libs/mpc",
		"BDEPEND":  "sys-devel/bison sys-devel/flex",
	}

	count, err := Ingest(db, sendEntries(t, m))
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected count=1, got %d", count)
	}

	got, err := Query(db, "sys-devel/gcc")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if got == nil {
		t.Fatal("Query returned nil")
	}

	if got.Unknown["KEYWORDS"] != m.Unknown["KEYWORDS"] {
		t.Errorf("Unknown[KEYWORDS]: got %q, want %q", got.Unknown["KEYWORDS"], m.Unknown["KEYWORDS"])
	}
	if got.Unknown["IUSE"] != m.Unknown["IUSE"] {
		t.Errorf("Unknown[IUSE]: got %q, want %q", got.Unknown["IUSE"], m.Unknown["IUSE"])
	}
	if got.Unknown["DEPEND"] != m.Unknown["DEPEND"] {
		t.Errorf("Unknown[DEPEND]: got %q, want %q", got.Unknown["DEPEND"], m.Unknown["DEPEND"])
	}
	if got.Unknown["RDEPEND"] != m.Unknown["RDEPEND"] {
		t.Errorf("Unknown[RDEPEND]: got %q, want %q", got.Unknown["RDEPEND"], m.Unknown["RDEPEND"])
	}
	if got.Unknown["BDEPEND"] != m.Unknown["BDEPEND"] {
		t.Errorf("Unknown[BDEPEND]: got %q, want %q", got.Unknown["BDEPEND"], m.Unknown["BDEPEND"])
	}
}

func TestOpenDB_DiskDb(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if db == nil {
		t.Fatal("expected non-nil db")
	}
	db.Close()
}

func TestOpenDB_InvalidPath(t *testing.T) {
	_, err := OpenDB("/dev/null/nonexistent/impossible/path/db")
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

func TestOpenReadOnlyDBDoesNotRequireWritableIndex(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "db")
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := MakeReadable(path); err != nil {
		t.Fatal(err)
	}
	discard, err := os.Stat(filepath.Join(path, "DISCARD"))
	if err != nil {
		t.Fatal(err)
	}
	if got := discard.Mode().Perm(); got != 0644 {
		t.Fatalf("authoritative DISCARD mode = %04o, want 0644", got)
	}
	privateValueDir, err := prepareReadOnlyValueDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if privateValueDir == path {
		t.Fatal("read-only value log must not use the authoritative index directory")
	}
	readOnly, err := OpenReadOnlyDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := readOnly.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeEntry_Roundtrip(t *testing.T) {
	m := &metadata.PackageMetadata{
		Category: "sys-devel",
		Package:  "gcc",
		Version:  "13.2.0",
		SLOT:     "13",
		DEPEND:   "virtual/libc",
		RDEPEND:  "",
		EAPI:     "8",
	}

	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(m); err != nil {
		t.Fatalf("encode: %v", err)
	}

	got, err := DecodeEntry(buf.Bytes())
	if err != nil {
		t.Fatalf("DecodeEntry: %v", err)
	}

	if got.Category != m.Category {
		t.Errorf("Category: got %q, want %q", got.Category, m.Category)
	}
	if got.Package != m.Package {
		t.Errorf("Package: got %q, want %q", got.Package, m.Package)
	}
	if got.Version != m.Version {
		t.Errorf("Version: got %q, want %q", got.Version, m.Version)
	}
	if got.DEPEND != m.DEPEND {
		t.Errorf("DEPEND: got %q, want %q", got.DEPEND, m.DEPEND)
	}
}

func TestDecodeEntry_CorruptBytes(t *testing.T) {
	_, err := DecodeEntry([]byte{0xFF, 0x00, 0xDE, 0xAD, 0xBE, 0xEF})
	if err == nil {
		t.Error("expected error for corrupt bytes")
	}
}
