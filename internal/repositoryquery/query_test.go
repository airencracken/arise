package repositoryquery

import (
	"testing"

	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/portage"
	"github.com/dgraph-io/badger/v4"
)

func queryTestDB(t *testing.T, records ...*metadata.PackageMetadata) *badger.DB {
	t.Helper()
	db, err := badger.Open(badger.DefaultOptions("").WithInMemory(true).WithLogger(nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	entries := make(chan *metadata.PackageMetadata, len(records))
	for _, record := range records {
		entries <- record
	}
	close(entries)
	if _, err := ingest.Ingest(db, entries); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestBestBinaryHonorsAtomPolicyAndBuildOrdering(t *testing.T) {
	index := &binpkg.PackagesIndex{Packages: []binpkg.PackageIndexEntry{
		{"CPV": "cat/pkg-1", "SLOT": "0", "repository": "gentoo", "KEYWORDS": "amd64", "BUILD_ID": "1"},
		{"CPV": "cat/pkg-2", "SLOT": "1", "repository": "gentoo", "KEYWORDS": "~amd64", "BUILD_ID": "1"},
		{"CPV": "cat/pkg-1", "SLOT": "0", "repository": "gentoo", "KEYWORDS": "amd64", "BUILD_ID": "2"},
	}}
	cfg := &portage.Config{MakeConf: map[string]string{"ARCH": "amd64"}}
	entry, err := BestBinary(index, cfg, "cat/pkg:0")
	if err != nil || entry["BUILD_ID"] != "2" {
		t.Fatalf("BestBinary = %#v, %v", entry, err)
	}
	entry, err = BestBinary(index, cfg, "cat/pkg:1")
	if err != nil || entry != nil {
		t.Fatalf("testing binary unexpectedly visible: %#v, %v", entry, err)
	}
}

func TestBestVisibleHonorsAtomKeywordsMasksAndGentooVersionOrder(t *testing.T) {
	db := queryTestDB(t,
		&metadata.PackageMetadata{Category: "cat", Package: "pkg", Version: "1.9", SLOT: "0", Repository: "gentoo", KEYWORDS: "amd64"},
		&metadata.PackageMetadata{Category: "cat", Package: "pkg", Version: "1.10", SLOT: "1", Repository: "gentoo", KEYWORDS: "~amd64"},
		&metadata.PackageMetadata{Category: "cat", Package: "pkg", Version: "2", SLOT: "0", Repository: "overlay", KEYWORDS: "amd64"},
	)
	cfg := &portage.Config{
		MakeConf:    map[string]string{"ARCH": "amd64"},
		PackageMask: []string{"=cat/pkg-2"},
	}
	best, err := BestVisible(db, cfg, "cat/pkg")
	if err != nil || best == nil || best.Version != "1.9" {
		t.Fatalf("BestVisible = %#v, %v", best, err)
	}
	best, err = BestVisible(db, cfg, "cat/pkg:1")
	if err != nil || best != nil {
		t.Fatalf("masked-keyword slot unexpectedly visible: %#v, %v", best, err)
	}
	cfg.ACCEPT_KEYWORDS = []string{"~amd64"}
	best, err = BestVisible(db, cfg, "cat/pkg:1")
	if err != nil || best == nil || best.Version != "1.10" {
		t.Fatalf("testing-keyword slot = %#v, %v", best, err)
	}
}

func TestBestMatchingInspectsExactInvisibleRevision(t *testing.T) {
	db := queryTestDB(t,
		&metadata.PackageMetadata{Category: "dev-python", Package: "sphinx", Version: "9.1.0", SLOT: "0", Repository: "gentoo", KEYWORDS: "amd64"},
		&metadata.PackageMetadata{Category: "dev-python", Package: "sphinx", Version: "9.1.0-r1", SLOT: "0", Repository: "gentoo", KEYWORDS: "~amd64"},
	)
	record, err := BestMatching(db, "=dev-python/sphinx-9.1.0-r1")
	if err != nil {
		t.Fatal(err)
	}
	if record == nil || record.Version != "9.1.0-r1" || record.KEYWORDS != "~amd64" {
		t.Fatalf("BestMatching exact testing revision = %#v", record)
	}
}

func TestBestMatchingRejectsAdversarialAtom(t *testing.T) {
	db := queryTestDB(t)
	if _, err := BestMatching(db, "../../etc/passwd"); err == nil {
		t.Fatal("path-like exact metadata atom accepted")
	}
}

func TestAllBestVisibleReturnsOneSortedRecordPerPackage(t *testing.T) {
	db := queryTestDB(t,
		&metadata.PackageMetadata{Category: "z", Package: "last", Version: "1", SLOT: "0", Repository: "gentoo", KEYWORDS: "amd64"},
		&metadata.PackageMetadata{Category: "a", Package: "first", Version: "1", SLOT: "0", Repository: "gentoo", KEYWORDS: "amd64"},
		&metadata.PackageMetadata{Category: "a", Package: "first", Version: "2", SLOT: "0", Repository: "gentoo", KEYWORDS: "amd64"},
	)
	cfg := &portage.Config{MakeConf: map[string]string{"ARCH": "amd64"}}
	records, err := AllBestVisible(db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].CPV() != "a/first-2" || records[1].CPV() != "z/last-1" {
		t.Fatalf("AllBestVisible = %#v", records)
	}
}

func TestVisibleRejectsAdversarialAtom(t *testing.T) {
	db := queryTestDB(t)
	if _, err := Visible(db, nil, "../../etc/passwd"); err == nil {
		t.Fatal("path-like atom accepted")
	}
}

func TestBestVisibleMutationMaskAndVersionComparisonsAreNotInvertible(t *testing.T) {
	db := queryTestDB(t,
		&metadata.PackageMetadata{Category: "cat", Package: "pkg", Version: "9", SLOT: "0", Repository: "gentoo", KEYWORDS: "amd64"},
		&metadata.PackageMetadata{Category: "cat", Package: "pkg", Version: "10", SLOT: "0", Repository: "gentoo", KEYWORDS: "amd64"},
	)
	cfg := &portage.Config{MakeConf: map[string]string{"ARCH": "amd64"}, PackageMask: []string{"=cat/pkg-10"}}
	best, err := BestVisible(db, cfg, ">=cat/pkg-9")
	if err != nil || best == nil || best.Version != "9" {
		t.Fatalf("masked highest version selected or comparison inverted: %#v, %v", best, err)
	}
}
