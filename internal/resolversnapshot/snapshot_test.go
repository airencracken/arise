package resolversnapshot

import (
	"testing"

	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/metadata"
)

func TestRoundTrip(t *testing.T) {
	directory := t.TempDir()
	db, err := ingest.OpenDB(directory)
	if err != nil {
		t.Fatal(err)
	}
	record := &metadata.PackageMetadata{
		Repository: "gentoo", RepositoryPath: "/repo", Category: "cat", Package: "pkg", Version: "1.2.3",
		SLOT: "0", KEYWORDS: "amd64", IUSE: "+ssl", RDEPEND: "dev-libs/foo", EAPI: "8", SRC_URI: "https://example/pkg.tar",
	}
	entries := make(chan *metadata.PackageMetadata, 1)
	entries <- record
	close(entries)
	if _, _, err := ingest.ReconcileWithProgress(db, entries, nil); err != nil {
		t.Fatal(err)
	}
	if err := Write(db, directory, 2); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := Read(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].CPV() != "cat/pkg-1.2.3" || got[0].RDEPEND != "dev-libs/foo" {
		t.Fatalf("snapshot = %+v", got)
	}
}
