package merge

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/airencracken/arise/internal/binpkg"
)

func TestMergeSymlinkRecoveryRoundTrip(t *testing.T) {
	base := t.TempDir()
	image := filepath.Join(base, "image")
	if err := makeDestDir(image, map[string]string{"usr/lib/go/bin/go": "fixture\n"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(image, "usr", "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../lib/go/bin/go", filepath.Join(image, "usr", "bin", "go")); err != nil {
		t.Fatal(err)
	}
	cfg := MergeConfig{
		RootDir: filepath.Join(base, "root"), VdbDir: filepath.Join(base, "vdb"),
		Category: "dev-lang", Package: "go", Version: "1.26.5",
		VDBMetadata: map[string]string{
			"CATEGORY": "dev-lang", "PF": "go-1.26.5", "SLOT": "0", "EAPI": "8",
		},
	}
	if err := Merge(context.Background(), image, cfg); err != nil {
		t.Fatal(err)
	}
	artifact, err := binpkg.Create(context.Background(), cfg.VdbPath(), cfg.RootDir, filepath.Join(base, "packages"))
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(base, "restored")
	if err := binpkg.Extract(context.Background(), artifact, destination); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(destination, "usr", "bin", "go"))
	if err != nil || target != "../lib/go/bin/go" {
		t.Fatalf("restore target=%q, error=%v", target, err)
	}
}
