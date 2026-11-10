package rebuild

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/binpkg"
)

func TestBinaryExecutionABIRejectsBeforeMutation(t *testing.T) {
	base := t.TempDir()
	image := t.TempDir()
	path := filepath.Join(base, "old.gpkg.tar")
	if err := binpkg.CreateGPKG(context.Background(), binpkg.GPKGCreateRequest{
		Path: path, Basename: "demo-1", ImageRoot: image,
		Metadata: map[string][]byte{
			"CATEGORY": []byte("app-misc\n"), "PF": []byte("demo-1\n"),
			"SLOT": []byte("0\n"), "EAPI": []byte("8\n"), "ARISE_PHASE_ENV_ABI": []byte("6\n"),
		},
	}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "root")
	cfg := &RebuildConfig{BinaryPackagePath: path, RootDir: root,
		VdbDir: filepath.Join(root, "var/db/pkg"), WorkDirBase: filepath.Join(base, "work"), JournalDir: filepath.Join(base, "journal")}
	if err := PreflightBinaryPackage("=app-misc/demo-1", cfg); err == nil || !strings.Contains(err.Error(), "execution ABI") {
		t.Fatalf("binary ABI preflight = %v", err)
	}
	if err := InstallBinaryPackage(context.Background(), "=app-misc/demo-1", cfg); err == nil || !strings.Contains(err.Error(), "execution ABI") {
		t.Fatalf("binary ABI installation = %v", err)
	}
	for _, path := range []string{root, cfg.WorkDirBase, cfg.JournalDir} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("rejected binary changed %s: %v", path, err)
		}
	}
}
