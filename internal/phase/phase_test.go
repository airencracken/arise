package phase

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewRunner_NilConfig(t *testing.T) {
	_, err := NewRunner(PhaseConfig{})
	if err == nil {
		t.Error("expected error for empty config, got nil")
	}
}

func TestNewRunner_MissingWorkDir(t *testing.T) {
	_, err := NewRunner(PhaseConfig{
		Sourcedir: "/tmp/src",
		DESTDIR:   "/tmp/install",
	})
	if err == nil {
		t.Error("expected error for missing WorkDir, got nil")
	}
	if !strings.Contains(err.Error(), "WorkDir") {
		t.Errorf("error should mention WorkDir: %v", err)
	}
}

func TestNewRunner_MissingSourcedir(t *testing.T) {
	_, err := NewRunner(PhaseConfig{
		WorkDir: "/tmp/work",
		DESTDIR: "/tmp/install",
	})
	if err == nil {
		t.Error("expected error for missing Sourcedir, got nil")
	}
	if !strings.Contains(err.Error(), "Sourcedir") {
		t.Errorf("error should mention Sourcedir: %v", err)
	}
}

func TestNewRunner_MissingDESTDIR(t *testing.T) {
	_, err := NewRunner(PhaseConfig{
		WorkDir:   "/tmp/work",
		Sourcedir: "/tmp/src",
	})
	if err == nil {
		t.Error("expected error for missing DESTDIR, got nil")
	}
	if !strings.Contains(err.Error(), "DESTDIR") {
		t.Errorf("error should mention DESTDIR: %v", err)
	}
}

func TestNewRunner_ValidConfig(t *testing.T) {
	_, err := NewRunner(PhaseConfig{
		WorkDir:   "/tmp/work",
		Sourcedir: "/tmp/src",
		DESTDIR:   "/tmp/install",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunPhase_NoOp(t *testing.T) {
	dir := t.TempDir()
	cfg := PhaseConfig{
		WorkDir:   dir,
		Sourcedir: dir,
		DESTDIR:   dir,
	}
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	if err := r.RunPhase(context.Background(), "src_test"); err != nil {
		t.Errorf("unknown phase should be no-op, got error: %v", err)
	}
}

func TestRunPhase_CompileAndInstall(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not found, skipping compile test")
	}

	srcDir := t.TempDir()
	destDir := t.TempDir()

	helloC := `#include <stdio.h>
int main(void) {
	printf("hello from arise\n");
	return 0;
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "hello.c"), []byte(helloC), 0644); err != nil {
		t.Fatal(err)
	}

	makefile := `PREFIX = /usr
BINDIR = $(PREFIX)/bin

all: hello

hello: hello.c
	$(CC) $(CFLAGS) -o hello hello.c

install: hello
	install -d $(DESTDIR)$(BINDIR)
	install -m 0755 hello $(DESTDIR)$(BINDIR)/hello

clean:
	rm -f hello
`
	if err := os.WriteFile(filepath.Join(srcDir, "Makefile"), []byte(makefile), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := PhaseConfig{
		WorkDir:   srcDir,
		Sourcedir: srcDir,
		DESTDIR:   destDir,
		CFLAGS:    "-O2",
	}
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	if err := r.RunPhase(context.Background(), "src_compile"); err != nil {
		t.Fatalf("src_compile: %v", err)
	}

	helloBin := filepath.Join(srcDir, "hello")
	if _, err := os.Stat(helloBin); os.IsNotExist(err) {
		t.Fatal("hello binary not built")
	}

	if err := r.RunPhase(context.Background(), "src_install"); err != nil {
		t.Fatalf("src_install: %v", err)
	}

	installedHello := filepath.Join(destDir, "usr", "bin", "hello")
	if _, err := os.Stat(installedHello); os.IsNotExist(err) {
		t.Fatalf("hello not installed to DESTDIR: %s does not exist", installedHello)
	}
}

func TestRunPhase_BrokenMakefile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not found, skipping test")
	}

	srcDir := t.TempDir()
	destDir := t.TempDir()

	makefile := `all:
	$(error intentional failure)
`
	if err := os.WriteFile(filepath.Join(srcDir, "Makefile"), []byte(makefile), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := PhaseConfig{
		WorkDir:   srcDir,
		Sourcedir: srcDir,
		DESTDIR:   destDir,
	}
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	err = r.RunPhase(context.Background(), "src_compile")
	if err == nil {
		t.Error("expected error for broken Makefile, got nil")
	}
}

func TestRunPhase_MissingConfigure(t *testing.T) {
	dir := t.TempDir()
	cfg := PhaseConfig{
		WorkDir:   dir,
		Sourcedir: dir,
		DESTDIR:   dir,
	}
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	if err := r.RunPhase(context.Background(), "src_configure"); err != nil {
		t.Errorf("missing configure should be no-op, got error: %v", err)
	}
}

func TestRunPhases_OrderAndStopOnError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not found, skipping test")
	}

	dir := t.TempDir()

	makefile := `all:
	$(error build failure)
`
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := PhaseConfig{
		WorkDir:   dir,
		Sourcedir: dir,
		DESTDIR:   dir,
	}
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	err = r.RunPhases(context.Background(), []string{"src_compile", "src_install"})
	if err == nil {
		t.Error("expected error from failed build, got nil")
	}
	if !strings.Contains(err.Error(), "src_compile") {
		t.Errorf("error should mention src_compile: %v", err)
	}
}

func TestExtractTarGz(t *testing.T) {
	dir := t.TempDir()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	content := []byte("file content inside archive")
	if err := tw.WriteHeader(&tar.Header{
		Name:     "test-file.txt",
		Size:     int64(len(content)),
		Mode:     0644,
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gw.Close()

	archivePath := filepath.Join(dir, "test.tar.gz")
	if err := os.WriteFile(archivePath, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	workDir := filepath.Join(dir, "work")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := PhaseConfig{
		WorkDir:   workDir,
		Sourcedir: dir,
		DESTDIR:   filepath.Join(dir, "install"),
	}
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	if err := r.RunPhase(context.Background(), "src_unpack"); err != nil {
		t.Fatalf("src_unpack: %v", err)
	}

	extracted := filepath.Join(workDir, "test-file.txt")
	data, err := os.ReadFile(extracted)
	if err != nil {
		t.Fatalf("extracted file not found: %v", err)
	}
	if string(data) != "file content inside archive" {
		t.Errorf("content = %q, want %q", string(data), "file content inside archive")
	}
}

func TestExtractTarGz_DirectoryEntries(t *testing.T) {
	dir := t.TempDir()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	if err := tw.WriteHeader(&tar.Header{
		Name:     "subdir/",
		Mode:     0755,
		Typeflag: tar.TypeDir,
	}); err != nil {
		t.Fatal(err)
	}

	content := []byte("nested")
	if err := tw.WriteHeader(&tar.Header{
		Name:     "subdir/nested.txt",
		Size:     int64(len(content)),
		Mode:     0644,
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gw.Close()

	archivePath := filepath.Join(dir, "dirs.tar.gz")
	if err := os.WriteFile(archivePath, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	workDir := filepath.Join(dir, "work")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := PhaseConfig{
		WorkDir:   workDir,
		Sourcedir: dir,
		DESTDIR:   filepath.Join(dir, "install"),
	}
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	if err := r.RunPhase(context.Background(), "src_unpack"); err != nil {
		t.Fatalf("src_unpack: %v", err)
	}

	nested := filepath.Join(workDir, "subdir", "nested.txt")
	data, err := os.ReadFile(nested)
	if err != nil {
		t.Fatalf("nested file not found: %v", err)
	}
	if string(data) != "nested" {
		t.Errorf("content = %q, want %q", string(data), "nested")
	}
}

func TestExtractUnsupportedFormat(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatal(err)
	}

	unsupported := filepath.Join(dir, "file.7z")
	if err := os.WriteFile(unsupported, []byte("not an archive"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := PhaseConfig{
		WorkDir:   workDir,
		Sourcedir: dir,
		DESTDIR:   filepath.Join(dir, "install"),
	}
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	if err := r.RunPhase(context.Background(), "src_unpack"); err != nil {
		t.Fatalf("unsupported format should be silently skipped: %v", err)
	}

	entries, _ := os.ReadDir(workDir)
	if len(entries) != 0 {
		t.Errorf("workdir should be empty, got %d entries", len(entries))
	}
}

func TestRunner_EnvVars(t *testing.T) {
	cfg := PhaseConfig{
		WorkDir:           "/tmp/work",
		Sourcedir:         "/tmp/src",
		DESTDIR:           "/tmp/dest",
		Arch:              "amd64",
		CFLAGS:            "-O2 -pipe",
		CXXFLAGS:          "-O2 -pipe",
		LDFLAGS:           "-Wl,-O1",
		MAKEOPTS:          "-j4",
		PortageConfigRoot: "/etc/portage",
	}
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	env := r.buildEnv()

	envMap := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	if envMap["ARCH"] != "amd64" {
		t.Errorf("expected ARCH=amd64, got %q", envMap["ARCH"])
	}
	if envMap["CFLAGS"] != "-O2 -pipe" {
		t.Errorf("expected CFLAGS=-O2 -pipe, got %q", envMap["CFLAGS"])
	}
	if envMap["CXXFLAGS"] != "-O2 -pipe" {
		t.Errorf("expected CXXFLAGS=-O2 -pipe, got %q", envMap["CXXFLAGS"])
	}
	if envMap["LDFLAGS"] != "-Wl,-O1" {
		t.Errorf("expected LDFLAGS=-Wl,-O1, got %q", envMap["LDFLAGS"])
	}
}

func TestRunner_MakeOpts(t *testing.T) {
	tests := []struct {
		name     string
		makeopts string
		wantLen  int
	}{
		{"empty", "", 0},
		{"single", "-j4", 1},
		{"multiple", "-j4 -s", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := PhaseConfig{
				WorkDir:   "/tmp",
				Sourcedir: "/tmp",
				DESTDIR:   "/tmp",
				MAKEOPTS:  tt.makeopts,
			}
			r := &Runner{cfg: cfg}
			opts := r.makeOpts()
			if len(opts) != tt.wantLen {
				t.Errorf("makeOpts() len = %d, want %d", len(opts), tt.wantLen)
			}
		})
	}
}

func TestRunner_Run_NoOp(t *testing.T) {
	dir := t.TempDir()
	cfg := PhaseConfig{
		WorkDir:   dir,
		Sourcedir: dir,
		DESTDIR:   dir,
	}
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	if err := r.Run(context.Background(), "src_test"); err != nil {
		t.Errorf("Run with src_test should be no-op, got error: %v", err)
	}
}

func TestRunner_Run_SrcUnpackEmptyDir(t *testing.T) {
	dir := t.TempDir()
	cfg := PhaseConfig{
		WorkDir:   dir,
		Sourcedir: dir,
		DESTDIR:   dir,
	}
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	if err := r.Run(context.Background(), "src_unpack"); err != nil {
		t.Errorf("Run with src_unpack on empty dir should be no-op, got error: %v", err)
	}
}

func TestCmdError(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("syscall.WaitStatus test is Linux-specific")
	}

	t.Run("plain error", func(t *testing.T) {
		plain := cmdError(exec.ErrNotFound)
		if plain != exec.ErrNotFound {
			t.Errorf("plain error should be passed through: %v", plain)
		}
	})
}
