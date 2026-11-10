package recoveryset

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/airencracken/arise/internal/merge"
)

func TestUpdateAndRestoreWithRemovedKernelPlaceholders(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	const kernelPath = "usr/src/linux-6.18.62-gentoo-dist-bin"
	kernel := createInstalledFixture(t, root, root, "sys-kernel", "gentoo-kernel-bin", "6.18.62", kernelPath+"/vmlinuz")
	gzip := createInstalledFixture(t, root, root, "app-arch", "gzip", "1.14", "usr/bin/gzip")
	contents, err := os.ReadFile(filepath.Join(kernel, "CONTENTS"))
	if err != nil {
		t.Fatal(err)
	}
	contents = append(contents, []byte("obj /"+kernelPath+"/arch/x86/boot/initrd digest 1700000000\nobj /"+kernelPath+"/arch/x86/boot/uki.efi digest 1700000000\n")...)
	if err := os.WriteFile(filepath.Join(kernel, "CONTENTS"), contents, 0644); err != nil {
		t.Fatal(err)
	}
	request := validRequest(base, root, []Package{{VDBEntryPath: kernel}, {VDBEntryPath: gzip}})
	setPath, err := Publish(context.Background(), request)
	if err != nil {
		t.Fatalf("kernel post-install cleanup blocked the update backup: %v", err)
	}
	manifest, err := Read(setPath)
	if err != nil || len(manifest.Artifacts) != 2 {
		t.Fatalf("backup set = %+v, %v", manifest, err)
	}
	for _, update := range []struct {
		category, name, version, replaced string
		files                             map[string]string
	}{
		{"app-arch", "gzip", "1.15", gzip, map[string]string{"usr/bin/gzip": "new gzip"}},
		{"sys-kernel", "gentoo-kernel-bin", "6.18.62-r1", kernel, map[string]string{kernelPath + "/vmlinuz": "new kernel", kernelPath + "/arch/x86/boot/initrd": "new initrd", kernelPath + "/arch/x86/boot/uki.efi": "new UKI"}},
	} {
		image := t.TempDir()
		for path, data := range update.files {
			full := filepath.Join(image, path)
			if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(data), 0755); err != nil {
				t.Fatal(err)
			}
		}
		if err := merge.Merge(context.Background(), image, merge.MergeConfig{RootDir: root, VdbDir: filepath.Join(root, "vdb"), Category: update.category, Package: update.name, Version: update.version, ReplacedVDBPath: update.replaced, JournalDir: filepath.Join(base, "upgrade-journal"), VDBMetadata: map[string]string{"CATEGORY": update.category, "PF": update.name + "-" + update.version, "SLOT": "0", "EAPI": "8"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := Restore(context.Background(), RestoreRequest{SetPath: setPath, RootDir: root, VDBDir: filepath.Join(root, "vdb"), JournalDir: filepath.Join(base, "restore-journal"), CurrentConfiguration: request.ConfigurationFingerprint, CurrentRepository: request.RepositoryFingerprint}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{kernelPath + "/arch/x86/boot/initrd", kernelPath + "/arch/x86/boot/uki.efi"} {
		if _, err := os.Lstat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Fatalf("restore invented originally absent %s: %v", path, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, kernelPath, "vmlinuz"))
	if err != nil || string(data) != "sys-kernel/gentoo-kernel-bin" {
		t.Fatalf("restored kernel = %q, %v", data, err)
	}
}
