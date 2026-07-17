package vdb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanPreservesInstalledVersionsSlotsAndState(t *testing.T) {
	root := t.TempDir()
	writePackage := func(pf, slot, repo string) {
		dir := filepath.Join(root, "dev-lang", pf)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		files := map[string]string{
			"SLOT": slot, "repository": repo, "USE": "ssl -test", "IUSE": "+ssl test",
			"DEPEND": "dev-libs/libfoo", "RDEPEND": "dev-libs/libbar", "BDEPEND": "sys-devel/make",
			"IDEPEND": "app-alternatives/python", "PDEPEND": "app-misc/post", "BUILD_TIME": "1700000000",
			"BUILD_ID": "7", "COUNTER": "42", "EAPI": "8", "CONTENTS": "obj /usr/bin/python abc 1\n",
		}
		for name, value := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(value+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	writePackage("python-3.11.9", "3.11/3.11", "gentoo")
	writePackage("python-3.12.4", "3.12/3.12", "local")

	packages, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 2 {
		t.Fatalf("got %d installed records", len(packages))
	}
	got := packages[1]
	if got.CPV() != "dev-lang/python-3.12.4" || got.Slot != "3.12" || got.Subslot != "3.12" || got.Repository != "local" {
		t.Fatalf("installed identity lost: %+v", got)
	}
	if got.BuildTime != 1700000000 || got.BuildID != "7" || got.Counter != 42 || got.EAPI != "8" || got.Contents == "" {
		t.Fatalf("installed state lost: %+v", got)
	}
	if got.Depend == "" || got.RDepend == "" || got.BDepend == "" || got.IDepend == "" || got.PDepend == "" {
		t.Fatalf("dependency state lost: %+v", got)
	}
}
