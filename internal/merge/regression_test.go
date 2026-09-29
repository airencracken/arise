package merge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAuditMissingContentsFailsCollisionCheck(t *testing.T) {
	root := t.TempDir()
	image, vdb := filepath.Join(root, "image"), filepath.Join(root, "vdb")
	if err := makeDestDir(image, map[string]string{"usr/bin/shared": "new"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(vdb, "cat", "old-1"), 0755); err != nil {
		t.Fatal(err)
	}
	cols, err := CheckCollisions(image, vdb, nil)
	if err == nil {
		t.Fatalf("missing CONTENTS accepted as no ownership: collisions=%v", cols)
	}
}

func TestAuditRetainedSlotOwnership(t *testing.T) {
	root := t.TempDir()
	image, vdb := filepath.Join(root, "image"), filepath.Join(root, "vdb")
	if err := makeDestDir(image, map[string]string{"usr/bin/shared": "slot-two"}); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(vdb, "cat", "pkg-1")
	if err := os.MkdirAll(old, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "SLOT"), []byte("1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "CONTENTS"), []byte("obj /usr/bin/shared 0123456789abcdef0123456789abcdef 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cols, err := CheckCollisions(image, vdb, []string{"cat/pkg"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) == 0 {
		t.Fatal("installing slot 2 excludes ownership of retained slot 1")
	}
	err = Merge(context.Background(), image, MergeConfig{
		RootDir: root, VdbDir: vdb, Category: "cat", Package: "pkg", Version: "2",
		VDBMetadata: map[string]string{"SLOT": "2"},
	})
	if err == nil {
		t.Fatal("merge accepted a retained-slot ownership collision")
	}
	if _, err := os.Stat(filepath.Join(old, "CONTENTS")); err != nil {
		t.Fatalf("collision changed retained package ownership: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vdb, "cat", "pkg-2")); !os.IsNotExist(err) {
		t.Fatalf("collision created a new VDB entry: %v", err)
	}
}

func TestAuditMalformedContentsMustNotDeleteVDB(t *testing.T) {
	root := t.TempDir()
	vdb := filepath.Join(root, "var", "db", "pkg")
	pkg := filepath.Join(vdb, "cat", "pkg-1")
	if err := os.MkdirAll(pkg, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "CONTENTS"), []byte("obj /usr/bin/owned\n"), 0644); err != nil {
		t.Fatal(err)
	}
	err := UnmergeAt(context.Background(), root, vdb, pkg, filepath.Join(root, "journals"))
	if err == nil {
		t.Fatal("malformed CONTENTS accepted and package VDB deleted")
	}
	if _, err := os.Stat(filepath.Join(pkg, "CONTENTS")); err != nil {
		t.Fatalf("failed unmerge deleted ownership evidence: %v", err)
	}
}

func TestMalformedContentsReturnsNoPartialOwnership(t *testing.T) {
	for _, record := range []string{
		"obj /file", "obj /file digest invalid", "sym /link -> target invalid",
		"sym /link", "unknown /file", "dir relative", "dir /nul\x00path",
	} {
		if entries, err := parseContents("dir /valid\n" + record); err == nil || entries != nil {
			t.Fatalf("malformed record %q returned partial or successful ownership: %#v, %v", record, entries, err)
		}
	}
}
