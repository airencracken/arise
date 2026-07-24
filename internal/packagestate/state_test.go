package packagestate

import (
	"bytes"
	"encoding/json"
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
		{Repository: "local", RepositoryPath: "/repos/local", RepositoryPriority: 10, Category: "app-editors", Package: "vim", Version: "9.1", SLOT: "0", EAPI: "8", RDEPEND: "dev-libs/libfoo", IUSE: "+gtk", REQUIRED_USE: "gtk"},
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
		for name, value := range map[string]string{"SLOT": "3", "repository": "gentoo", "EAPI": "8", "USE": "ssl threads", "IUSE": "+ssl threads", "RDEPEND": "dev-libs/openssl", "CONTENTS": ""} {
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
	if snapshot.Available[2].RDEPEND != "dev-libs/libfoo" || snapshot.Available[2].REQUIREDUSE != "gtk" {
		t.Fatalf("available resolver metadata lost: %+v", snapshot.Available[2])
	}
	if got := snapshot.Installed[0]; got.RDEPEND != "dev-libs/openssl" || !reflect.DeepEqual(got.USE, []string{"ssl", "threads"}) {
		t.Fatalf("installed resolver metadata lost: %+v", got)
	}
	if len(snapshot.Fingerprint) != len("sha256:")+64 {
		t.Fatalf("fingerprint = %q", snapshot.Fingerprint)
	}

	portable, err := snapshot.Portable()
	if err != nil {
		t.Fatal(err)
	}
	if portable.Available[2].Path != "repositories/local" {
		t.Fatalf("portable repository path = %q", portable.Available[2].Path)
	}
	again, err := portable.Portable()
	if err != nil {
		t.Fatal(err)
	}
	if portable.Fingerprint != again.Fingerprint {
		t.Fatalf("portable fingerprint is not stable: %q != %q", portable.Fingerprint, again.Fingerprint)
	}
	encoded, err := json.Marshal(portable)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	availableMetadata, err := decoded.AvailableMetadata()
	if err != nil {
		t.Fatal(err)
	}
	installedPackages, err := decoded.InstalledPackages()
	if err != nil {
		t.Fatal(err)
	}
	if availableMetadata[2].RepositoryPath != "repositories/local" || availableMetadata[2].RDEPEND != "dev-libs/libfoo" {
		t.Fatalf("available replay lost state: %+v", availableMetadata[2])
	}
	if installedPackages[0].RDepend != "dev-libs/openssl" || !reflect.DeepEqual(installedPackages[0].Use, []string{"ssl", "threads"}) {
		t.Fatalf("installed replay lost state: %+v", installedPackages[0])
	}

	tampered := portable
	tampered.Installed[0].RDEPEND = "dev-libs/libressl"
	encoded, err = json.Marshal(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(bytes.NewReader(encoded)); err == nil {
		t.Fatal("tampered fixture unexpectedly passed fingerprint validation")
	}
}
