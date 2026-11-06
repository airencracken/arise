package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunPhaseQueryUsesSelectedDomainAndInstalledUse(t *testing.T) {
	rootVDB, brootVDB := filepath.Join(t.TempDir(), "root"), filepath.Join(t.TempDir(), "broot")
	path := filepath.Join(brootVDB, "dev-libs", "sample-2")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"SLOT": "0", "repository": "gentoo", "USE": "ssl", "IUSE": "ssl"} {
		if err := os.WriteFile(filepath.Join(path, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("___ARISE_QUERY_ROOT_VDB", rootVDB)
	t.Setenv("___ARISE_QUERY_BROOT_VDB", brootVDB)
	if code := runPhaseQuery([]string{"has-version", "b", "dev-libs/sample[ssl]", ""}); code != 0 {
		t.Fatalf("has-version exit = %d", code)
	}
	if code := runPhaseQuery([]string{"has-version", "r", "dev-libs/sample", ""}); code != 0 {
		t.Fatalf("valid false query exit = %d", code)
	}
	if code := runPhaseQuery([]string{"has-version", "invalid", "dev-libs/sample", ""}); code != 2 {
		t.Fatalf("invalid domain exit = %d", code)
	}
}

func TestRunPhaseQueryAcceptsOlderParentButPrefersReservedControls(t *testing.T) {
	for _, name := range []string{"QUERY_ROOT_VDB", "QUERY_BROOT_VDB"} {
		t.Setenv("___ARISE_"+name, "")
		if err := os.Unsetenv("___ARISE_" + name); err != nil {
			t.Fatal(err)
		}
		t.Setenv("ARISE_"+name, t.TempDir())
	}
	for _, domain := range []string{"r", "b"} {
		if code := runPhaseQuery([]string{"has-version", domain, "dev-libs/absent", ""}); code != 0 {
			t.Fatalf("older parent's %s query failed: %d", domain, code)
		}
	}
	t.Setenv("___ARISE_QUERY_ROOT_VDB", "")
	if code := runPhaseQuery([]string{"has-version", "r", "dev-libs/absent", ""}); code != 2 {
		t.Fatal("malformed reserved controls fell back to an application variable")
	}
}
