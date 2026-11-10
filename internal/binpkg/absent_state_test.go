package binpkg

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAbsentCaptureEvidenceIsRechecked(t *testing.T) {
	for _, kind := range []string{"still absent", "file", "directory", "dangling symlink", "symlink parent", "non-directory parent"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			parent := filepath.Join(root, "usr/src")
			if err := os.MkdirAll(parent, 0755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(parent, "initrd")
			var err error
			switch kind {
			case "file":
				err = os.WriteFile(path, []byte("appeared"), 0644)
			case "directory":
				err = os.Mkdir(path, 0755)
			case "dangling symlink":
				err = os.Symlink("missing-target", path)
			case "symlink parent", "non-directory parent":
				if err := os.Remove(parent); err != nil {
					t.Fatal(err)
				}
				if kind == "symlink parent" {
					err = os.Symlink(t.TempDir(), parent)
				} else {
					err = os.WriteFile(parent, []byte("not a directory"), 0644)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			err = verifyAbsentCapturePath(root, "/usr/src/initrd")
			if kind == "still absent" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("capture ignored a changed or inaccessible path")
			} else if kind == "file" || kind == "directory" || kind == "dangling symlink" {
				if !errors.Is(err, ErrCaptureChanged) {
					t.Fatalf("missing change classification: %v", err)
				}
			}
		})
	}
}

func TestDisappearingPresentFileIsAReadFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	item, err := fileEvidenceFromInfo(path, "payload", "obj", info)
	if err == nil || item.Type == "absent" {
		t.Fatalf("file disappearing after inspection was treated as preexisting absence: %#v, %v", item, err)
	}
}

func TestSchemaOneRecoveryArtifactsRemainReadable(t *testing.T) {
	base := t.TempDir()
	vdb, root := createMockVDB(t, base)
	path, err := Create(context.Background(), vdb, root, filepath.Join(base, "packages"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadRecoveryManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Schema = 1
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := ReadMetadata(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]string)
	for key, value := range metadata {
		values[key] = strings.TrimSpace(string(value))
	}
	embedRecoveryManifest(values, encoded)
	archive, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	size := int(binary.BigEndian.Uint32(archive[len(archive)-8 : len(archive)-4]))
	legacy := append(archive[:len(archive)-size-8:len(archive)-size-8], buildXPAKMetadata(values)...)
	legacyPath := filepath.Join(base, "legacy.tbz2")
	if err := os.WriteFile(legacyPath, legacy, 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRecoveryManifest(legacyPath)
	if err != nil || got.Schema != 1 {
		t.Fatalf("legacy backup = %+v, %v", got, err)
	}
}

func TestAbsentEvidenceRequiresSchemaTwoAndCannotContainFileData(t *testing.T) {
	for _, kind := range []string{"valid", "schema one", "file data", "metadata absence", "unknown recorded type"} {
		t.Run(kind, func(t *testing.T) {
			manifest := validRecoveryManifestForTest()
			item := FileEvidence{Path: "usr/src/initrd", Type: "absent", RecordedType: "obj"}
			switch kind {
			case "schema one":
				manifest.Schema = 1
			case "file data":
				item.SHA256 = strings.Repeat("a", 64)
			case "unknown recorded type":
				item.RecordedType = "unknown"
			case "metadata absence":
				item.Path = "EAPI"
				manifest.SourceVDB = []FileEvidence{item}
			}
			manifest.Payload = []FileEvidence{item}
			manifest.SourceVDBSHA256, _ = evidenceSHA256(manifest.SourceVDB)
			manifest.SourceRootSHA256, _ = evidenceSHA256(manifest.Payload)
			err := validateRecoveryManifest(&manifest)
			if (err == nil) != (kind == "valid") {
				t.Fatalf("validation = %v", err)
			}
		})
	}
}

func TestAbsentParentCannotContainCapturedFile(t *testing.T) {
	manifest := validRecoveryManifestForTest()
	manifest.Payload = append([]FileEvidence{{Path: "usr", Type: "absent", RecordedType: "dir"}}, manifest.Payload...)
	manifest.SourceRootSHA256, _ = evidenceSHA256(manifest.Payload)
	if err := validateRecoveryManifest(&manifest); err == nil || !strings.Contains(err.Error(), "absent parent") {
		t.Fatalf("contradictory snapshot accepted: %v", err)
	}
}
