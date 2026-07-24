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
	t.Setenv("ARISE_QUERY_ROOT_VDB", rootVDB)
	t.Setenv("ARISE_QUERY_BROOT_VDB", brootVDB)
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
