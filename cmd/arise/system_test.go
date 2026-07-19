package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestInstalledVDBForCPRejectsHyphenatedSibling(t *testing.T) {
	vdb := t.TempDir()
	category := filepath.Join(vdb, "net-dns")
	for _, name := range []string{"bind-tools-9.18.0-r1", "bind-9.18.42", "bind-9.20.1", "bind-invalid"} {
		if err := os.MkdirAll(filepath.Join(category, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(category, "bind-9.99"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := installedVDBForCP(vdb, "net-dns", "bind")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(category, "bind-9.18.42"),
		filepath.Join(category, "bind-9.20.1"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("installedVDBForCP() = %q, want %q", got, want)
	}
}

func TestInstalledVDBForCPMissingCategory(t *testing.T) {
	got, err := installedVDBForCP(t.TempDir(), "net-dns", "bind")
	if err != nil || len(got) != 0 {
		t.Fatalf("installedVDBForCP() = %q, %v; want empty, nil", got, err)
	}
}
