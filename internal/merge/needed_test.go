package merge

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeNeededMetadataReadsELFWithoutExecutingIt(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ELF fixture uses the running Linux test executable")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image := t.TempDir()
	target := filepath.Join(image, "usr", "bin", "fixture")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy, elf2, err := nativeNeededMetadata(image)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(legacy, "/usr/bin/fixture") {
		t.Fatalf("legacy NEEDED = %q", legacy)
	}
	fields := strings.Split(elf2, ";")
	if len(fields) != 6 || fields[1] != "/usr/bin/fixture" || fields[0] == "" || fields[5] == "" {
		t.Fatalf("NEEDED.ELF.2 = %q", elf2)
	}
}

func TestMergePublishesNativeNeededMetadata(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ELF fixture uses the running Linux test executable")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	image := filepath.Join(tmp, "image")
	target := filepath.Join(image, "usr", "bin", "fixture")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(tmp, "root")
	cfg := MergeConfig{RootDir: root, VdbDir: filepath.Join(root, "var", "db", "pkg"), Category: "cat", Package: "fixture", Version: "1", JournalDir: filepath.Join(tmp, "journals")}
	if err := Merge(context.Background(), image, cfg); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"NEEDED", "NEEDED.ELF.2"} {
		metadata, err := os.ReadFile(filepath.Join(cfg.VdbPath(), name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(metadata), "/usr/bin/fixture") {
			t.Fatalf("%s = %q", name, metadata)
		}
	}
}

func TestNativeNeededMetadataIgnoresNonELF(t *testing.T) {
	image := t.TempDir()
	if err := os.WriteFile(filepath.Join(image, "text"), []byte("not an elf"), 0o644); err != nil {
		t.Fatal(err)
	}
	legacy, elf2, err := nativeNeededMetadata(image)
	if err != nil || legacy != "" || elf2 != "" {
		t.Fatalf("legacy=%q elf2=%q err=%v", legacy, elf2, err)
	}
}
