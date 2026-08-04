package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/airencracken/arise/internal/resolvertrace"
)

func TestWriteResolverTraceCreatesPrivateExclusiveFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.json")
	trace := resolvertrace.Trace{Schema: resolvertrace.SchemaVersion, Targets: []string{}, Backtracks: nil, Branches: nil, Conflicts: []string{}, Warnings: []string{}}
	if err := writeResolverTrace(path, trace); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("trace mode = %o", info.Mode().Perm())
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeResolverTrace(path, trace); err == nil {
		t.Fatal("existing trace was overwritten")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("failed overwrite changed existing trace")
	}
}

func TestWriteResolverTraceRejectsEmptyPath(t *testing.T) {
	if err := writeResolverTrace(" ", resolvertrace.Trace{Schema: resolvertrace.SchemaVersion}); err == nil {
		t.Fatal("empty trace path accepted")
	}
}
