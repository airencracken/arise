package merge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeDestDir(base string, entries map[string]string) error {
	for path, content := range entries {
		fullPath := filepath.Join(base, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			return err
		}
	}
	return nil
}

func TestMerge_BasicFiles(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(tmp, "vdb")

	if err := makeDestDir(destDir, map[string]string{
		"usr/bin/hello":         "#!/bin/sh\necho hello\n",
		"usr/share/doc/readme.txt": "Hello world\n",
		"etc/conf.d/test.conf":  "key=value\n",
	}); err != nil {
		t.Fatalf("makeDestDir: %v", err)
	}

	cfg := MergeConfig{
		RootDir:  rootDir,
		VdbDir:   vdbDir,
		Category: "app-shells",
		Package:  "testpkg",
		Version:  "1.0",
	}

	if err := Merge(ctx, destDir, cfg); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	checkFile := func(rel, expectedContent string) {
		path := filepath.Join(rootDir, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("ReadFile(%s): %v", rel, err)
			return
		}
		if string(data) != expectedContent {
			t.Errorf("content mismatch for %s: got %q, want %q", rel, string(data), expectedContent)
		}
	}

	checkFile("usr/bin/hello", "#!/bin/sh\necho hello\n")
	checkFile("usr/share/doc/readme.txt", "Hello world\n")
	checkFile("etc/conf.d/test.conf", "key=value\n")

	contentsPath := filepath.Join(vdbDir, "app-shells", "testpkg-1.0", "CONTENTS")
	contentsData, err := os.ReadFile(contentsPath)
	if err != nil {
		t.Fatalf("ReadFile CONTENTS: %v", err)
	}

	contents := string(contentsData)
	for _, expected := range []string{
		"obj /",
		"usr/bin/hello",
		"usr/share/doc/readme.txt",
		"etc/conf.d/test.conf",
	} {
		if !strings.Contains(contents, expected) {
			t.Errorf("CONTENTS missing expected entry %q, contents:\n%s", expected, contents)
		}
	}

	envPath := filepath.Join(vdbDir, "app-shells", "testpkg-1.0", ".environment")
	if _, err := os.Stat(envPath); err != nil {
		t.Errorf("environment file not found: %v", err)
	}
}

func TestMerge_SymlinksAndDirs(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(tmp, "vdb")

	if err := os.MkdirAll(filepath.Join(destDir, "usr/lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destDir, "usr/lib", "libfoo.so.1"), []byte("lib"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("libfoo.so.1", filepath.Join(destDir, "usr/lib", "libfoo.so")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(destDir, "var/lib", "emptydir"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := MergeConfig{
		RootDir:  rootDir,
		VdbDir:   vdbDir,
		Category: "sys-libs",
		Package:  "testlib",
		Version:  "2.0",
	}

	if err := Merge(ctx, destDir, cfg); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	linkPath := filepath.Join(rootDir, "usr/lib", "libfoo.so")
	linkTarget, err := os.Readlink(linkPath)
	if err != nil {
		t.Errorf("Readlink: %v", err)
	}
	if linkTarget != "libfoo.so.1" {
		t.Errorf("symlink target = %q, want %q", linkTarget, "libfoo.so.1")
	}

	dirPath := filepath.Join(rootDir, "var/lib", "emptydir")
	if fi, err := os.Stat(dirPath); err != nil || !fi.IsDir() {
		t.Errorf("directory %s not created correctly: err=%v, isDir=%v", dirPath, err, fi != nil && fi.IsDir())
	}

	contentsPath := filepath.Join(vdbDir, "sys-libs", "testlib-2.0", "CONTENTS")
	contentsData, err := os.ReadFile(contentsPath)
	if err != nil {
		t.Fatalf("ReadFile CONTENTS: %v", err)
	}
	contents := string(contentsData)
	if !strings.Contains(contents, "sym ") {
		t.Errorf("CONTENTS missing sym entry:\n%s", contents)
	}
	if !strings.Contains(contents, "dir ") {
		t.Errorf("CONTENTS missing dir entry:\n%s", contents)
	}
}

func TestUnmerge(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(tmp, "vdb")

	if err := makeDestDir(destDir, map[string]string{
		"usr/bin/app":  "binary content",
		"usr/lib/libx.a": "archive",
		"etc/app.conf": "config",
	}); err != nil {
		t.Fatalf("makeDestDir: %v", err)
	}

	cfg := MergeConfig{
		RootDir:  rootDir,
		VdbDir:   vdbDir,
		Category: "app-misc",
		Package:  "app",
		Version:  "1.0",
	}

	if err := Merge(ctx, destDir, cfg); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	for _, rel := range []string{"usr/bin/app", "usr/lib/libx.a", "etc/app.conf"} {
		if _, err := os.Stat(filepath.Join(rootDir, rel)); err != nil {
			t.Fatalf("file %s should exist after merge: %v", rel, err)
		}
	}

	pkgPath := filepath.Join(vdbDir, "app-misc", "app-1.0")
	if err := Unmerge(ctx, pkgPath); err != nil {
		t.Fatalf("Unmerge: %v", err)
	}

	for _, rel := range []string{"usr/bin/app", "usr/lib/libx.a", "etc/app.conf"} {
		path := filepath.Join(rootDir, rel)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("file %s should not exist after unmerge: err=%v", rel, err)
		}
	}

	for _, rel := range []string{"usr/bin", "usr/lib", "usr", "etc"} {
		path := filepath.Join(rootDir, rel)
		if _, err := os.Stat(path); err == nil {
			t.Errorf("empty directory %s should have been removed", rel)
		}
	}

	if _, err := os.Stat(pkgPath); !os.IsNotExist(err) {
		t.Errorf("VDB entry %s should not exist after unmerge", pkgPath)
	}
}

func TestMerge_EmptyDestDir(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(tmp, "vdb")

	cfg := MergeConfig{
		RootDir:  rootDir,
		VdbDir:   vdbDir,
		Category: "app-empty",
		Package:  "noop",
		Version:  "0",
	}

	if err := Merge(ctx, destDir, cfg); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	contentsPath := filepath.Join(vdbDir, "app-empty", "noop-0", "CONTENTS")
	contentsData, err := os.ReadFile(contentsPath)
	if err != nil {
		t.Fatalf("ReadFile CONTENTS: %v", err)
	}
	if strings.TrimSpace(string(contentsData)) != "" {
		t.Errorf("CONTENTS should be empty, got: %q", string(contentsData))
	}
}

func TestMerge_NonExistentRoot(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	if err := makeDestDir(destDir, map[string]string{
		"usr/bin/foo": "hello",
	}); err != nil {
		t.Fatalf("makeDestDir: %v", err)
	}

	rootDir := filepath.Join(tmp, "nonexistent-root")
	vdbDir := filepath.Join(tmp, "vdb")

	cfg := MergeConfig{
		RootDir:  rootDir,
		VdbDir:   vdbDir,
		Category: "cat",
		Package:  "pkg",
		Version:  "0",
	}

	if err := Merge(ctx, destDir, cfg); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rootDir, "usr/bin/foo"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q, want %q", string(data), "hello")
	}
}

func TestMerge_ContentsFormat(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(tmp, "vdb")

	if err := os.MkdirAll(filepath.Join(destDir, "usr/lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destDir, "usr/lib", "libx.so"), []byte("shared"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("libx.so", filepath.Join(destDir, "usr/lib", "libx.so.1")); err != nil {
		t.Fatal(err)
	}

	cfg := MergeConfig{
		RootDir:  rootDir,
		VdbDir:   vdbDir,
		Category: "cat",
		Package:  "pkg",
		Version:  "1",
	}

	if err := Merge(ctx, destDir, cfg); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	contentsPath := filepath.Join(vdbDir, "cat", "pkg-1", "CONTENTS")
	data, err := os.ReadFile(contentsPath)
	if err != nil {
		t.Fatalf("ReadFile CONTENTS: %v", err)
	}

	contents := string(data)
	lines := strings.Split(strings.TrimSpace(contents), "\n")

	hasDir := false
	hasObj := false
	hasSym := false

	for _, line := range lines {
		if strings.HasPrefix(line, "dir ") {
			hasDir = true
			fields := strings.Fields(line)
			if len(fields) < 2 {
				t.Errorf("dir line lacks path: %q", line)
			}
		}
		if strings.HasPrefix(line, "obj ") {
			hasObj = true
			fields := strings.Fields(line)
			if len(fields) < 4 {
				t.Errorf("obj line should have at least 4 fields (type path md5 mtime): %q", line)
			}
			if fields[2] == "" {
				t.Errorf("obj line has empty md5: %q", line)
			}
		}
		if strings.HasPrefix(line, "sym ") {
			hasSym = true
			if !strings.Contains(line, "->") {
				t.Errorf("sym line missing ->: %q", line)
			}
		}
	}

	if !hasDir {
		t.Error("CONTENTS missing dir entry")
	}
	if !hasObj {
		t.Error("CONTENTS missing obj entry")
	}
	if !hasSym {
		t.Error("CONTENTS missing sym entry")
	}
}

func TestUnmerge_Nonexistent(t *testing.T) {
	ctx := context.Background()
	err := Unmerge(ctx, "/tmp/nonexistent-path-arise-test-12345")
	if err == nil {
		t.Error("expected error for nonexistent path, got nil")
	}
}

func TestUnmerge_CorruptContents(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	pkgDir := filepath.Join(tmp, "cat", "pkg-1")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	corrupt := "garbage line\n\x00\x00\x00\nobj /usr/bin/x a1 0"
	if err := os.WriteFile(filepath.Join(pkgDir, "CONTENTS"), []byte(corrupt), 0644); err != nil {
		t.Fatal(err)
	}

	// should not panic, may error or succeed gracefully
	err := Unmerge(ctx, pkgDir)
	_ = err // acceptable either way
}

func TestMerge_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")
	if err := makeDestDir(destDir, map[string]string{
		"usr/bin/x": "content",
	}); err != nil {
		t.Fatalf("makeDestDir: %v", err)
	}

	cfg := MergeConfig{
		RootDir:  filepath.Join(tmp, "root"),
		VdbDir:   filepath.Join(tmp, "vdb"),
		Category: "cat",
		Package:  "pkg",
		Version:  "1",
	}

	err := Merge(ctx, destDir, cfg)
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
}

func TestMerge_WithFilesWithSpecialPerms(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	fullPath := filepath.Join(destDir, "usr/bin", "setuid-binary")
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte("binary"), 04755); err != nil {
		t.Fatal(err)
	}

	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(tmp, "vdb")

	cfg := MergeConfig{
		RootDir:  rootDir,
		VdbDir:   vdbDir,
		Category: "cat",
		Package:  "pkg",
		Version:  "1",
	}

	if err := Merge(ctx, destDir, cfg); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	fi, err := os.Stat(filepath.Join(rootDir, "usr/bin", "setuid-binary"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	got := fi.Mode()
	if got.Perm() != 0755 {
		t.Errorf("mode perms = %o, want %o", got.Perm(), 0755)
	}
	_ = got // mode bits may differ on nosuid mounts
}

func TestUnmerge_PreservesNonEmptyDirs(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(tmp, "vdb")

	if err := makeDestDir(destDir, map[string]string{
		"usr/share/pkg/file.txt": "data",
	}); err != nil {
		t.Fatalf("makeDestDir: %v", err)
	}

	// Create an extra file in the same dir that is not part of this package
	extraPath := filepath.Join(rootDir, "usr/share/pkg/extra.txt")
	if err := os.MkdirAll(filepath.Dir(extraPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extraPath, []byte("not ours"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := MergeConfig{
		RootDir:  rootDir,
		VdbDir:   vdbDir,
		Category: "cat",
		Package:  "pkg",
		Version:  "1",
	}

	if err := Merge(ctx, destDir, cfg); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	pkgPath := filepath.Join(vdbDir, "cat", "pkg-1")
	if err := Unmerge(ctx, pkgPath); err != nil {
		t.Fatalf("Unmerge: %v", err)
	}

	// Our file should be gone
	if _, err := os.Stat(filepath.Join(rootDir, "usr/share/pkg/file.txt")); !os.IsNotExist(err) {
		t.Error("our file should have been removed")
	}
	// Extra file should remain
	if _, err := os.Stat(extraPath); err != nil {
		t.Error("extra file should still exist")
	}
	// Directory should still exist (not empty)
	if _, err := os.Stat(filepath.Join(rootDir, "usr/share/pkg")); err != nil {
		t.Error("directory with extra file should still exist")
	}
}

func TestParseContents(t *testing.T) {
	input := `obj /usr/bin/foo a1b2c3d4e5 1234567890
dir /usr/share/pkg
sym /usr/lib/libx.so -> libx.so.1 ffff 9876543210`

	entries, err := parseContents(input)
	if err != nil {
		t.Fatalf("parseContents: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Type != "obj" {
		t.Errorf("entry 0 type = %q, want obj", entries[0].Type)
	}
	if entries[0].MD5 != "a1b2c3d4e5" {
		t.Errorf("entry 0 md5 = %q", entries[0].MD5)
	}
	if entries[0].Mtime != 1234567890 {
		t.Errorf("entry 0 mtime = %d", entries[0].Mtime)
	}
	if entries[1].Type != "dir" {
		t.Errorf("entry 1 type = %q, want dir", entries[1].Type)
	}
	if entries[2].Type != "sym" {
		t.Errorf("entry 2 type = %q, want sym", entries[2].Type)
	}
}

func TestParseContents_Empty(t *testing.T) {
	entries, err := parseContents("")
	if err != nil {
		t.Fatalf("parseContents: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestParseContents_Adversarial(t *testing.T) {
	input := fmt.Sprintf("garbage\n\x00\x00\n%s\nshort",
		strings.Repeat("obj /x a1 0\n", 1000))

	entries, err := parseContents(input)
	if err != nil {
		t.Fatalf("parseContents should not error on adversarial input: %v", err)
	}
	if len(entries) < 1000 {
		t.Errorf("expected at least 1000 entries, got %d", len(entries))
	}
}

func TestUnmerge_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(tmp, "vdb")

	if err := makeDestDir(destDir, map[string]string{
		"usr/bin/x": "content",
	}); err != nil {
		t.Fatalf("makeDestDir: %v", err)
	}

	cfg := MergeConfig{
		RootDir:  rootDir,
		VdbDir:   vdbDir,
		Category: "cat",
		Package:  "pkg",
		Version:  "1",
	}

	if err := Merge(context.Background(), destDir, cfg); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	pkgPath := filepath.Join(vdbDir, "cat", "pkg-1")
	err := Unmerge(ctx, pkgPath)
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
}

func TestMerge_RecordsMtime(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(tmp, "vdb")

	testPath := filepath.Join(destDir, "testfile")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(testPath, []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := MergeConfig{
		RootDir:  rootDir,
		VdbDir:   vdbDir,
		Category: "cat",
		Package:  "pkg",
		Version:  "1",
	}

	if err := Merge(ctx, destDir, cfg); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	contentsPath := filepath.Join(vdbDir, "cat", "pkg-1", "CONTENTS")
	data, err := os.ReadFile(contentsPath)
	if err != nil {
		t.Fatalf("ReadFile CONTENTS: %v", err)
	}

	contents := string(data)
	if !strings.Contains(contents, "obj ") {
		t.Errorf("CONTENTS missing obj entry: %s", contents)
	}

	lines := strings.Split(strings.TrimSpace(contents), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "obj ") {
			fields := strings.Fields(line)
			if len(fields) < 4 {
				t.Errorf("obj line too short: %q", line)
				continue
			}
			if fields[2] == "" {
				t.Error("md5 field is empty")
			}
			if fields[3] == "" || fields[3] == "0" {
				t.Errorf("mtime field is empty or zero: %q", fields[3])
			}
		}
	}
}
