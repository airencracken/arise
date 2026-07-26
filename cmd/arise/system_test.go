package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/atom"
)

func writeConfigTarget(t *testing.T, root, category, pf, slot, repository string) string {
	t.Helper()
	directory := filepath.Join(root, category, pf)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"CONTENTS": "", "EAPI": "8\n", "SLOT": slot + "\n", "repository": repository + "\n",
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

func TestFindInstalledConfigTargetSupportsEmergeSlotSyntax(t *testing.T) {
	root := t.TempDir()
	writeConfigTarget(t, root, "dev-db", "postgresql-17.8", "17", "gentoo")
	want := writeConfigTarget(t, root, "dev-db", "postgresql-18.4", "18", "gentoo")
	requested, err := atom.Parse("dev-db/postgresql:18")
	if err != nil {
		t.Fatal(err)
	}
	got, err := findInstalledConfigTarget(root, requested)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("target = %q, want %q", got, want)
	}
}

func TestFindInstalledConfigTargetRejectsMissingAndAmbiguousAtoms(t *testing.T) {
	root := t.TempDir()
	writeConfigTarget(t, root, "dev-db", "postgresql-18.3", "18", "gentoo")
	writeConfigTarget(t, root, "dev-db", "postgresql-18.4", "18", "gentoo")
	requested, _ := atom.Parse("dev-db/postgresql:18")
	if _, err := findInstalledConfigTarget(root, requested); err == nil || !strings.Contains(err.Error(), "multiple installed packages") {
		t.Fatalf("ambiguous target error = %v", err)
	}
	missing, _ := atom.Parse("dev-db/postgresql:19")
	if _, err := findInstalledConfigTarget(root, missing); err == nil || !strings.Contains(err.Error(), "no installed package") {
		t.Fatalf("missing target error = %v", err)
	}
	if _, err := findInstalledConfigTarget(root, nil); err == nil {
		t.Fatal("nil atom was accepted")
	}
}

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
