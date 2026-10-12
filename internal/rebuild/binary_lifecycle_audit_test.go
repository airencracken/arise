package rebuild

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/binpkg"
)

func TestBinaryRequiredLifecycleRejectsBeforeRootMutation(t *testing.T) {
	for _, fixture := range []struct{ name, declared, environment string }{
		{"Portage phase names", "postinst", ""},
		{"Arise phase names", "pkg_setup pkg_postinst", ""},
		{"undeclared saved hook", "", "pkg_preinst ()\n{\necho required\n}\n"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			base := t.TempDir()
			image := filepath.Join(base, "image")
			if err := os.MkdirAll(filepath.Join(image, "usr/bin"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(image, "usr/bin/demo"), []byte("payload"), 0755); err != nil {
				t.Fatal(err)
			}
			archive, err := publishBuiltGPKG(context.Background(), &RebuildConfig{PackageDir: filepath.Join(base, "packages")}, "app-misc", "demo-1", image,
				map[string]string{"CATEGORY": "app-misc", "PF": "demo-1", "SLOT": "0", "EAPI": "8", "DEFINED_PHASES": fixture.declared}, []byte(fixture.environment))
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(base, "root")
			cfg := &RebuildConfig{BinaryPackagePath: archive, RootDir: root, VdbDir: filepath.Join(root, "var/db/pkg"), WorkDirBase: filepath.Join(base, "work"), JournalDir: filepath.Join(base, "journal")}
			if fixture.declared != "" {
				if err := PreflightBinaryPackage("=app-misc/demo-1", cfg); err == nil || !strings.Contains(err.Error(), "rebuild from source") {
					t.Fatalf("lifecycle preflight = %v", err)
				}
			}
			if err := InstallBinaryPackage(context.Background(), "=app-misc/demo-1", cfg); err == nil || !strings.Contains(err.Error(), "unsupported lifecycle") {
				t.Fatalf("required hooks silently ignored: %v", err)
			}
			if _, err := os.Lstat(root); !os.IsNotExist(err) {
				t.Fatalf("unsupported binary changed ROOT: %v", err)
			}
		})
	}
}

func TestBinaryReplacementRejectsOldRemovalHook(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	old := filepath.Join(root, "var/db/pkg/app-misc/demo-1")
	if err := os.MkdirAll(old, 0755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"SLOT": "0", "DEFINED_PHASES": "prerm"} {
		if err := os.WriteFile(filepath.Join(old, name), []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	image := filepath.Join(base, "image")
	if err := os.Mkdir(image, 0755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(base, "demo-2.gpkg.tar")
	if err := binpkg.CreateGPKG(context.Background(), binpkg.GPKGCreateRequest{Path: archive, Basename: "demo-2", ImageRoot: image,
		Metadata: map[string][]byte{"CATEGORY": []byte("app-misc"), "PF": []byte("demo-2"), "SLOT": []byte("0"), "EAPI": []byte("8")}}); err != nil {
		t.Fatal(err)
	}
	cfg := &RebuildConfig{BinaryPackagePath: archive, RootDir: root, VdbDir: filepath.Join(root, "var/db/pkg"), WorkDirBase: filepath.Join(base, "work"), JournalDir: filepath.Join(base, "journal"), AllowLiveUpgrade: true}
	for _, run := range []func(context.Context, string, *RebuildConfig) error{func(_ context.Context, a string, c *RebuildConfig) error { return PreflightBinaryPackage(a, c) }, InstallBinaryPackage} {
		if err := run(context.Background(), "=app-misc/demo-2", cfg); err == nil || !strings.Contains(err.Error(), "prerm") {
			t.Fatalf("old removal hook ignored: %v", err)
		}
		if _, err := os.Stat(old); err != nil {
			t.Fatalf("rejection lost installed package: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(root, "var/db/pkg/app-misc/demo-2")); !os.IsNotExist(err) {
			t.Fatalf("rejection installed replacement: %v", err)
		}
	}
}
