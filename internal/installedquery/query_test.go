package installedquery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMatchAndBestHonorVersionsSlotsRepositoriesAndUse(t *testing.T) {
	vdb := t.TempDir()
	add := func(pf, slot, repo, use, iuse string) {
		t.Helper()
		path := filepath.Join(vdb, "dev-libs", pf)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, value := range map[string]string{"SLOT": slot, "repository": repo, "USE": use, "IUSE": iuse} {
			if err := os.WriteFile(filepath.Join(path, name), []byte(value+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	add("sample-1", "0/1", "gentoo", "ssl", "ssl debug")
	add("sample-2", "0/2", "overlay", "debug", "ssl debug")

	for query, want := range map[string]bool{
		"dev-libs/sample": true, ">=dev-libs/sample-2": true,
		"dev-libs/sample:0/1::gentoo[ssl,-debug]": true,
		"dev-libs/sample[ssl,debug]":              false,
		"dev-libs/sample[missing(+)]":             true,
		"dev-libs/sample[missing]":                false,
	} {
		got, err := Match(vdb, query, nil)
		if err != nil {
			t.Fatalf("Match(%q): %v", query, err)
		}
		if got != want {
			t.Errorf("Match(%q) = %t, want %t", query, got, want)
		}
	}
	best, err := Best(vdb, "dev-libs/sample:0", nil)
	if err != nil || best != "dev-libs/sample-2" {
		t.Fatalf("Best = %q, %v", best, err)
	}
}

func TestConditionalUseDependenciesUseCallerState(t *testing.T) {
	vdb := t.TempDir()
	path := filepath.Join(vdb, "dev-libs", "sample-1")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"SLOT": "0", "repository": "gentoo", "USE": "ssl", "IUSE": "ssl"} {
		if err := os.WriteFile(filepath.Join(path, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Match(vdb, "dev-libs/sample[ssl?]", map[string]bool{"ssl": true})
	if err != nil || !got {
		t.Fatalf("enabled conditional = %t, %v", got, err)
	}
	got, err = Match(vdb, "dev-libs/sample[ssl=]", map[string]bool{"ssl": false})
	if err != nil || got {
		t.Fatalf("disabled equality = %t, %v", got, err)
	}
}
