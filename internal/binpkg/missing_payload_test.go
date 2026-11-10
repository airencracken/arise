package binpkg

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureRecordsAlreadyAbsentInstalledPayload(t *testing.T) {
	for _, format := range []struct {
		name   string
		create func(context.Context, string, string, string) (string, error)
	}{{"recovery", Create}, {"gpkg", CreateInstalledGPKG}} {
		t.Run(format.name, func(t *testing.T) {
			base := t.TempDir()
			const contents = "obj /usr/src/linux/arch/x86/boot/initrd digest 1700000000\nobj /usr/src/linux/arch/x86/boot/uki.efi digest 1700000000\nsym /usr/src/linux/removed-link -> initrd 1700000000\ndir /usr/src/linux/removed-directory\nobj /usr/src/linux/vmlinuz digest 1700000000\n"
			vdb, root := createCaptureFixture(t, base, contents)
			if err := os.MkdirAll(filepath.Join(root, "usr/src/linux"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "usr/src/linux/vmlinuz"), []byte("kernel"), 0644); err != nil {
				t.Fatal(err)
			}
			path, err := format.create(context.Background(), vdb, root, filepath.Join(base, "packages"))
			if err != nil {
				t.Fatal(err)
			}
			if format.name == "recovery" {
				manifest, err := ReadRecoveryManifest(path)
				if err != nil {
					t.Fatal(err)
				}
				absent := 0
				for _, item := range manifest.Payload {
					if item.Type == "absent" {
						absent++
					}
				}
				if absent != 4 {
					t.Fatalf("recorded %d absent paths; want 4", absent)
				}
			}
			image := filepath.Join(base, "extracted")
			if err := Extract(context.Background(), path, image); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(image, "usr/src/linux/vmlinuz"))
			if err != nil || string(data) != "kernel" {
				t.Fatalf("kernel = %q, %v", data, err)
			}
			for _, relative := range []string{"usr/src/linux/arch/x86/boot/initrd", "usr/src/linux/arch/x86/boot/uki.efi", "usr/src/linux/removed-link", "usr/src/linux/removed-directory"} {
				if _, err := os.Lstat(filepath.Join(image, relative)); !os.IsNotExist(err) {
					t.Fatalf("capture invented %s: %v", relative, err)
				}
			}
			data, err = os.ReadFile(filepath.Join(vdb, "CONTENTS"))
			if err != nil || string(data) != contents {
				t.Fatalf("capture changed ownership: %q, %v", data, err)
			}
		})
	}
}
