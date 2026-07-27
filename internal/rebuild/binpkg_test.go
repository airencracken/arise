package rebuild

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/binpkg"
)

func TestInstallBinaryPackageGPKG(t *testing.T) {
	base := t.TempDir()
	image := filepath.Join(base, "image")
	if err := os.MkdirAll(filepath.Join(image, "usr", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(image, "usr", "bin", "demo"), []byte("payload"), 0o755); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(base, "demo.gpkg.tar")
	metadata := map[string][]byte{
		"CATEGORY": []byte("app-misc\n"), "PF": []byte("demo-1\n"),
		"SLOT": []byte("0\n"), "EAPI": []byte("8\n"),
		"USE": []byte("feature\n"), "IUSE": []byte("feature\n"),
	}
	if err := binpkg.CreateGPKG(context.Background(), binpkg.GPKGCreateRequest{
		Path: packagePath, Basename: "demo-1", ImageRoot: image, Metadata: metadata,
	}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "root")
	cfg := &RebuildConfig{
		BinaryPackagePath: packagePath,
		RootDir:           root, VdbDir: filepath.Join(root, "var", "db", "pkg"),
		WorkDirBase: filepath.Join(base, "work"), JournalDir: filepath.Join(base, "journal"),
	}
	if err := InstallBinaryPackage(context.Background(), "=app-misc/demo-1", cfg); err != nil {
		t.Fatalf("InstallBinaryPackage: %v", err)
	}
	payload, err := os.ReadFile(filepath.Join(root, "usr", "bin", "demo"))
	if err != nil || string(payload) != "payload" {
		t.Fatalf("installed payload = %q, %v", payload, err)
	}
	for name, want := range map[string]string{"CATEGORY": "app-misc", "PF": "demo-1", "SLOT": "0", "EAPI": "8"} {
		got, err := os.ReadFile(filepath.Join(cfg.VdbDir, "app-misc", "demo-1", name))
		if err != nil || strings.TrimSpace(string(got)) != want {
			t.Fatalf("VDB %s = %q, %v; want %q", name, got, err, want)
		}
	}
}

func TestInstallBinaryPackageRejectsIdentityMismatchAtomically(t *testing.T) {
	base := t.TempDir()
	image := filepath.Join(base, "image")
	if err := os.MkdirAll(image, 0o755); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(base, "demo.gpkg.tar")
	if err := binpkg.CreateGPKG(context.Background(), binpkg.GPKGCreateRequest{
		Path: packagePath, Basename: "demo-1", ImageRoot: image,
		Metadata: map[string][]byte{
			"CATEGORY": []byte("app-misc\n"), "PF": []byte("demo-1\n"),
			"SLOT": []byte("0\n"), "EAPI": []byte("8\n"),
		},
	}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "root")
	cfg := &RebuildConfig{
		BinaryPackagePath: packagePath,
		RootDir:           root, VdbDir: filepath.Join(root, "var", "db", "pkg"),
		WorkDirBase: filepath.Join(base, "work"), JournalDir: filepath.Join(base, "journal"),
	}
	err := InstallBinaryPackage(context.Background(), "=app-misc/other-1", cfg)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("identity mismatch error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "usr")); !os.IsNotExist(statErr) {
		t.Fatalf("ROOT mutated after rejected package: %v", statErr)
	}
}

func TestPublishBuiltGPKGRequiresPackageDirectory(t *testing.T) {
	_, err := publishBuiltGPKG(context.Background(), &RebuildConfig{}, "app-misc", "demo-1", t.TempDir(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "PKGDIR") {
		t.Fatalf("publish error = %v", err)
	}
}

func TestPublishBuiltGPKGRoundTrip(t *testing.T) {
	base := t.TempDir()
	image := filepath.Join(base, "image")
	if err := os.MkdirAll(filepath.Join(image, "usr", "share"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(image, "usr", "share", "demo"), []byte("built"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &RebuildConfig{PackageDir: filepath.Join(base, "packages")}
	path, err := publishBuiltGPKG(context.Background(), cfg, "app-misc", "demo-1", image, map[string]string{
		"CATEGORY": "app-misc", "PF": "demo-1", "SLOT": "0", "EAPI": "8",
	}, []byte("export USE='feature'\n"))
	if err != nil {
		t.Fatalf("publishBuiltGPKG: %v", err)
	}
	if path != filepath.Join(cfg.PackageDir, "app-misc", "demo-1.gpkg.tar") {
		t.Fatalf("published path = %s", path)
	}
	metadata, err := binpkg.ReadMetadata(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata["environment.bz2"]) == 0 {
		t.Fatal("published package omitted environment.bz2")
	}
	extracted := filepath.Join(base, "extracted")
	if err := binpkg.Extract(context.Background(), path, extracted); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(extracted, "usr", "share", "demo"))
	if err != nil || string(got) != "built" {
		t.Fatalf("round-trip payload = %q, %v", got, err)
	}
}
