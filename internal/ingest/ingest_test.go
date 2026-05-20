package ingest

import (
	"bytes"
	"encoding/gob"
	"errors"
	"path/filepath"
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
		Category:  "sys-devel",
		Package:   "gcc",
		KEYWORDS:  "",
		IUSE:      "",
	}

	// Set through Unknown map to test gob round-trip
	m.Unknown = map[string]string{
		"KEYWORDS":  "amd64 x86 ~arm64",
		"IUSE":      "fortran openmp lto",
		"DEPEND":    ">=dev-libs/gmp-6.2 >=dev-libs/mpfr-4.1",
		"RDEPEND":   "dev-libs/mpc",
		"BDEPEND":   "sys-devel/bison sys-devel/flex",
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
