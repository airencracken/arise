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

func TestScanResolverStateOmitsContentsButPreservesResolutionMetadata(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "dev-lang", "python-3.13.1")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"CONTENTS":   "obj /usr/bin/python3.13 digest 1\n",
		"EAPI":       "8\n",
		"SLOT":       "3.13/3.13\n",
		"repository": "gentoo\n",
		"USE":        "ssl\n",
		"IUSE":       "ssl test\n",
		"DEPEND":     "dev-libs/libffi\n",
		"RDEPEND":    "sys-libs/zlib\n",
	}
	for name, value := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	packages, err := ScanResolverState(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 {
		t.Fatalf("resolver packages = %d", len(packages))
	}
	got := packages[0]
	if got.Contents != "" {
		t.Fatalf("resolver scan retained CONTENTS: %q", got.Contents)
	}
	if got.CPV() != "dev-lang/python-3.13.1" || got.Slot != "3.13" || got.Subslot != "3.13" ||
		got.Repository != "gentoo" || got.Depend != "dev-libs/libffi" || got.RDepend != "sys-libs/zlib" {
		t.Fatalf("resolver metadata lost: %+v", got)
	}
	full, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != 1 || full[0].Contents == "" {
		t.Fatal("full VDB scan no longer retains CONTENTS")
	}
}

func TestMutationScanResolverStateStillRequiresCommittedContents(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "app-misc", "example-1")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"EAPI": "8\n", "SLOT": "0\n", "repository": "gentoo\n"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	packages, err := ScanResolverState(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 0 {
		t.Fatalf("uncommitted record entered resolver state: %+v", packages)
	}
}

func TestScanIgnoresInterruptedPartialRecord(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "sys-kernel", "gentoo-sources-7.1.3")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "SLOT"), []byte("7.1.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	packages, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 0 {
		t.Fatalf("partial VDB directory treated as installed: %+v", packages)
	}
}

func TestScanRejectsStructurallyInvalidCommittedRecords(t *testing.T) {
	for _, test := range []struct {
		name        string
		breakRecord func(*testing.T, string)
	}{
		{"empty slot", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "SLOT"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"empty eapi", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "EAPI"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"empty repository", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "repository"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlinked contents", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "CONTENTS")); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(t.TempDir(), "CONTENTS")
			if err := os.WriteFile(outside, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(dir, "CONTENTS")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "app-misc", "example-1")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, value := range map[string]string{
				"CONTENTS": "", "EAPI": "8\n", "SLOT": "0\n", "repository": "gentoo\n",
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			test.breakRecord(t, dir)
			packages, err := Scan(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(packages) != 0 {
				t.Fatalf("invalid record treated as installed: %+v", packages)
			}
		})
	}
}
