package packagestate

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/dgraph-io/badger/v4"
)

func TestCapturePreservesAndSortsAllState(t *testing.T) {
	db, err := badger.Open(badger.DefaultOptions("").WithInMemory(true).WithLogger(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entries := []*metadata.PackageMetadata{
		{Repository: "local", RepositoryPath: "/repos/local", Category: "app-editors", Package: "vim", Version: "9.1", SLOT: "0", EAPI: "8"},
		{Repository: "gentoo", RepositoryPath: "/repos/gentoo", Category: "app-editors", Package: "vim", Version: "9.0", SLOT: "0", EAPI: "8"},
		{Repository: "gentoo", RepositoryPath: "/repos/gentoo", Category: "app-editors", Package: "vim", Version: "9.1", SLOT: "0", EAPI: "8"},
	}
	ch := make(chan *metadata.PackageMetadata, len(entries))
	for _, entry := range entries {
		ch <- entry
	}
	close(ch)
	if _, err := ingest.Ingest(db, ch); err != nil {
		t.Fatal(err)
	}

	vdbPath := t.TempDir()
	for _, pf := range []string{"python-3.12.4", "python-3.11.9"} {
		dir := filepath.Join(vdbPath, "dev-lang", pf)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		for name, value := range map[string]string{"SLOT": "3", "repository": "gentoo", "EAPI": "8"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(value+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	snapshot, err := Capture(db, vdbPath)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Schema != Schema || len(snapshot.Available) != 3 || len(snapshot.Installed) != 2 {
		t.Fatalf("incomplete snapshot: %+v", snapshot)
	}
	available := []string{snapshot.Available[0].CPV + "::" + snapshot.Available[0].Repository, snapshot.Available[1].CPV + "::" + snapshot.Available[1].Repository, snapshot.Available[2].CPV + "::" + snapshot.Available[2].Repository}
	wantAvailable := []string{"app-editors/vim-9.0::gentoo", "app-editors/vim-9.1::gentoo", "app-editors/vim-9.1::local"}
	if !reflect.DeepEqual(available, wantAvailable) {
		t.Fatalf("available = %v", available)
	}
	if snapshot.Installed[0].CPV != "dev-lang/python-3.11.9" || snapshot.Installed[1].CPV != "dev-lang/python-3.12.4" {
		t.Fatalf("installed order = %+v", snapshot.Installed)
	}
}
