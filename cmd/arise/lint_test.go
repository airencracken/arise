package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLintPropagatesInstalledToolFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "golangci-lint"), []byte("#!/bin/sh\necho injected-lint-finding >&2\nexit 42\n"), 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("make", "-C", "../..", "GO=true", "lint")
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	data, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(data), "injected-lint-finding") || strings.Contains(string(data), "not installed") {
		t.Fatalf("lint failure was swallowed: %v\n%s", err, data)
	}
}
