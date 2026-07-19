package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeUninstallNeeded(t *testing.T, vdb, cpv, metadata string) {
	t.Helper()
	dir := filepath.Join(vdb, filepath.FromSlash(cpv))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "NEEDED.ELF.2"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidateELFRemovalOrder(t *testing.T) {
	vdb := t.TempDir()
	writeUninstallNeeded(t, vdb, "cat/provider-1", "X86_64;/usr/lib/libprovider.so.1;libprovider.so.1;;libc.so.6;x86_64\n")
	writeUninstallNeeded(t, vdb, "cat/consumer-1", "X86_64;/usr/bin/consumer;;;libprovider.so.1,libc.so.6;x86_64\n")

	if err := validateELFRemovalOrder(vdb, []string{"cat/consumer-1", "cat/provider-1"}); err != nil {
		t.Fatalf("safe consumer-first order rejected: %v", err)
	}
	if err := validateELFRemovalOrder(vdb, []string{"cat/provider-1", "cat/consumer-1"}); err == nil || !strings.Contains(err.Error(), "unsafe order") {
		t.Fatalf("provider-first order error=%v", err)
	}
	if err := validateELFRemovalOrder(vdb, []string{"cat/provider-1"}); err == nil || !strings.Contains(err.Error(), "outside the removal plan") {
		t.Fatalf("external consumer error=%v", err)
	}
	if err := validateELFRemovalOrder(vdb, []string{"cat/consumer-1", "cat/consumer-1"}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate error=%v", err)
	}
}
