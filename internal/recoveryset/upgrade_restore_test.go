package recoveryset

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/airencracken/arise/internal/merge"
)

func TestRestoreReplacesUpgradedSlotAndIsRepeatable(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	old := createInstalledFixture(t, root, root, "app-misc", "demo", "1", "usr/bin/demo")
	parallel := createInstalledFixture(t, root, root, "app-misc", "demo", "3", "usr/bin/demo-slot1")
	if err := os.WriteFile(filepath.Join(parallel, "SLOT"), []byte("1/3"), 0644); err != nil {
		t.Fatal(err)
	}
	publish := validRequest(base, root, []Package{{VDBEntryPath: old}})
	setPath, err := Publish(context.Background(), publish)
	if err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(base, "image")
	if err := os.MkdirAll(filepath.Join(image, "usr/bin"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"demo", "demo-new-only"} {
		if err := os.WriteFile(filepath.Join(image, "usr/bin", name), []byte("upgraded"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	vdb := filepath.Join(root, "vdb")
	journals := filepath.Join(base, "journals")
	if err := merge.Merge(context.Background(), image, merge.MergeConfig{
		RootDir: root, VdbDir: vdb, JournalDir: journals,
		Category: "app-misc", Package: "demo", Version: "2",
		ReplacedVDBPath: old, VDBMetadata: map[string]string{"SLOT": "0/2", "EAPI": "8"},
	}); err != nil {
		t.Fatal(err)
	}
	request := RestoreRequest{
		SetPath: setPath, RootDir: root, VDBDir: vdb, JournalDir: journals,
		CurrentConfiguration: publish.ConfigurationFingerprint,
		CurrentRepository:    publish.RepositoryFingerprint,
	}
	injected := errors.New("interrupted before restore commit")
	request.Merge = func(ctx context.Context, image string, cfg merge.MergeConfig) error {
		cfg.AfterPayloadSync = func() error { return injected }
		return merge.Merge(ctx, image, cfg)
	}
	if err := Restore(context.Background(), request); !errors.Is(err, injected) {
		t.Fatalf("interrupted restore: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(root, "usr/bin/demo")); err != nil || string(content) != "upgraded" {
		t.Fatalf("interrupted restore did not roll back: %q, %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(vdb, "app-misc/demo-2/CONTENTS")); err != nil {
		t.Fatalf("interrupted restore lost upgraded VDB: %v", err)
	}
	request.Merge = nil
	for attempt := 0; attempt < 2; attempt++ {
		if err := Restore(context.Background(), request); err != nil {
			t.Fatalf("restore attempt %d: %v", attempt+1, err)
		}
		for _, path := range []string{old, parallel, filepath.Join(root, "usr/bin/demo-slot1")} {
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("restore lost %s: %v", path, err)
			}
		}
		for _, path := range []string{filepath.Join(vdb, "app-misc/demo-2"), filepath.Join(root, "usr/bin/demo-new-only")} {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("restore retained upgrade state %s: %v", path, err)
			}
		}
		if content, err := os.ReadFile(filepath.Join(root, "usr/bin/demo")); err != nil || string(content) != "app-misc/demo" {
			t.Fatalf("restored payload = %q, %v", content, err)
		}
	}
}

func TestCanceledRestorePreservesSetStatusAndFilesystem(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "source")
	old := createInstalledFixture(t, base, root, "app-misc", "demo", "1", "usr/bin/demo")
	publish := validRequest(base, root, []Package{{VDBEntryPath: old}})
	setPath, err := Publish(context.Background(), publish)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := RestoreRequest{
		SetPath: setPath, RootDir: filepath.Join(base, "target"), VDBDir: filepath.Join(base, "target/vdb"),
		JournalDir: filepath.Join(base, "journals"), WorkDir: filepath.Join(base, "work"),
		CurrentConfiguration: publish.ConfigurationFingerprint, CurrentRepository: publish.RepositoryFingerprint,
	}
	if err := Restore(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled restore: %v", err)
	}
	status, err := ReadStatus(setPath)
	if err != nil || status.Status != StatusActive {
		t.Fatalf("canceled restore changed set status: %+v, %v", status, err)
	}
	for _, path := range []string{request.RootDir, request.WorkDir, request.JournalDir} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("canceled restore created %s: %v", path, err)
		}
	}
}

func TestRestoreReplacementRejectsAmbiguousOrUnreadableSlot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	first := createInstalledFixture(t, base, root, "app-misc", "demo", "1", "usr/bin/demo")
	second := createInstalledFixture(t, base, root, "app-misc", "demo", "2", "usr/bin/demo")
	vdb := filepath.Join(base, "vdb")
	if _, err := restoreReplacement(vdb, "app-misc", "demo", "0/old", first); err == nil {
		t.Fatal("ambiguous installed slot accepted")
	}
	if err := os.Remove(filepath.Join(second, "SLOT")); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreReplacement(vdb, "app-misc", "demo", "0", first); err == nil {
		t.Fatal("unreadable installed slot accepted")
	}
}
