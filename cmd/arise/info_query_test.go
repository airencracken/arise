package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureInfoOutput(t *testing.T, operation func() int) (string, int) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = writer
	code := operation()
	_ = writer.Close()
	os.Stdout = previous
	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(data), code
}

func TestInfoRepositoryAndAssetQueriesUseExplicitConfigRoot(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "etc", "portage")
	repository := filepath.Join(root, "repos", "gentoo")
	for _, directory := range []string{
		filepath.Join(config, "repos.conf"), filepath.Join(repository, "profiles"),
		filepath.Join(repository, "eclass"), filepath.Join(repository, "licenses"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(config, "repos.conf", "gentoo.conf"), []byte("[gentoo]\nlocation = "+repository+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "profiles", "repo_name"), []byte("gentoo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "eclass", "tool.eclass"), []byte("# fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "licenses", "MIT"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous := *portageConfigRoot
	*portageConfigRoot = config
	t.Cleanup(func() { *portageConfigRoot = previous })

	output, code := captureInfoOutput(t, func() int { return runInfoRepoPaths([]string{"gentoo"}) })
	if code != 0 || strings.TrimSpace(output) != repository {
		t.Fatalf("repo path = %q, code %d", output, code)
	}
	output, code = captureInfoOutput(t, func() int { return runInfoAssetPaths("eclass", []string{"gentoo", "tool"}) })
	if code != 0 || strings.TrimSpace(output) != filepath.Join(repository, "eclass", "tool.eclass") {
		t.Fatalf("eclass path = %q, code %d", output, code)
	}
	output, code = captureInfoOutput(t, func() int { return runInfoAssetPaths("license", []string{"gentoo", "MIT"}) })
	if code != 0 || strings.TrimSpace(output) != filepath.Join(repository, "licenses", "MIT") {
		t.Fatalf("license path = %q, code %d", output, code)
	}
}

func TestInfoColorsAreDeterministicAndRejectOperands(t *testing.T) {
	output, code := captureInfoOutput(t, func() int { return runInfoColors(nil) })
	if code != 0 || !strings.Contains(output, "bad=\"\\x1b[91m\"") || !strings.Contains(output, "normal=\"\\x1b[0m\"") {
		t.Fatalf("colors = %q, code %d", output, code)
	}
	if code := runInfoColors([]string{"unexpected"}); code != 2 {
		t.Fatalf("colors accepted operand with code %d", code)
	}
}

func TestInfoValuesSupportsBatchAndWildcardInterop(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "etc", "portage")
	if err := os.MkdirAll(config, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "make.conf"), []byte("CUSTOM_ONE=one\nCUSTOM_TWO=two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous := *portageConfigRoot
	*portageConfigRoot = config
	t.Cleanup(func() { *portageConfigRoot = previous })
	output, code := captureInfoOutput(t, func() int { return runInfoValues([]string{"CUSTOM_*"}) })
	if code != 0 || output != "CUSTOM_ONE=one\nCUSTOM_TWO=two\n" {
		t.Fatalf("wildcard values = %q, code %d", output, code)
	}
}
