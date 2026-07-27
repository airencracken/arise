//go:build linux

package binpkg

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRecoveryArtifactPreservesHardlinksSparseFilesMetadataAndXAttrs(t *testing.T) {
	base := t.TempDir()
	vdb, root := createCaptureFixture(t, base,
		"obj /usr/bin/first 0 1700000000\nobj /usr/bin/second 0 1700000000\nobj /usr/bin/sparse 0 1700000000\n")
	first := filepath.Join(root, "usr", "bin", "first")
	second := filepath.Join(root, "usr", "bin", "second")
	sparse := filepath.Join(root, "usr", "bin", "sparse")
	if err := os.WriteFile(first, []byte("hardlinked payload"), 04750); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, second); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(sparse)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("begin"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("end"), 8<<20); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	mtime := time.Unix(1_700_000_000, 123_000_000)
	if err := os.Chtimes(first, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	xattrSupported := true
	if err := unix.Setxattr(first, "user.arise-test", []byte("retained"), 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
			xattrSupported = false
		} else {
			t.Fatal(err)
		}
	}
	artifact, err := Create(context.Background(), vdb, root, filepath.Join(base, "packages"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadRecoveryManifest(artifact)
	if err != nil {
		t.Fatal(err)
	}
	var sawHardlink, sawSparse bool
	for _, evidence := range manifest.Payload {
		sawHardlink = sawHardlink || evidence.Type == "hardlink"
		sawSparse = sawSparse || len(evidence.SparseExtents) > 0
	}
	if !sawHardlink || !sawSparse {
		t.Fatalf("manifest hardlink=%v sparse=%v", sawHardlink, sawSparse)
	}
	destination := filepath.Join(base, "destination")
	if err := Extract(context.Background(), artifact, destination); err != nil {
		t.Fatal(err)
	}
	firstInfo, err := os.Stat(filepath.Join(destination, "usr", "bin", "first"))
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(filepath.Join(destination, "usr", "bin", "second"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(firstInfo, secondInfo) {
		t.Fatal("hardlink identity was not restored")
	}
	if firstInfo.Mode().Perm() != 0750 || firstInfo.ModTime().UnixNano() != mtime.UnixNano() {
		t.Fatalf("restored metadata mode=%o mtime=%v", firstInfo.Mode().Perm(), firstInfo.ModTime())
	}
	var stat syscall.Stat_t
	if err := syscall.Stat(filepath.Join(destination, "usr", "bin", "sparse"), &stat); err != nil {
		t.Fatal(err)
	}
	if stat.Blocks*512 >= stat.Size {
		t.Fatalf("sparse allocation blocks=%d size=%d", stat.Blocks, stat.Size)
	}
	if xattrSupported {
		value := make([]byte, 32)
		size, err := unix.Getxattr(filepath.Join(destination, "usr", "bin", "first"), "user.arise-test", value)
		if err != nil || string(value[:size]) != "retained" {
			t.Fatalf("restored xattr=%q, %v", value[:max(size, 0)], err)
		}
	}
}

func TestExtractionPolicyRejectsEntryCountSizeAndXAttrBombs(t *testing.T) {
	tests := []struct {
		name    string
		headers []*tar.Header
		bodies  [][]byte
		policy  ExtractionPolicy
	}{
		{
			name:    "entries",
			headers: []*tar.Header{{Name: "one", Mode: 0600, Size: 0}, {Name: "two", Mode: 0600, Size: 0}},
			bodies:  [][]byte{nil, nil},
			policy:  ExtractionPolicy{MaxEntries: 1, MaxTotalBytes: 10, MaxFileBytes: 10, MaxXAttrBytes: 10},
		},
		{
			name:    "file size",
			headers: []*tar.Header{{Name: "large", Mode: 0600, Size: 4}},
			bodies:  [][]byte{[]byte("data")},
			policy:  ExtractionPolicy{MaxEntries: 1, MaxTotalBytes: 10, MaxFileBytes: 3, MaxXAttrBytes: 10},
		},
		{
			name:    "xattrs",
			headers: []*tar.Header{{Name: "attrs", Mode: 0600, Size: 0, PAXRecords: map[string]string{xattrPAXPrefix + "user.large": "0123456789"}}},
			bodies:  [][]byte{nil},
			policy:  ExtractionPolicy{MaxEntries: 1, MaxTotalBytes: 10, MaxFileBytes: 10, MaxXAttrBytes: 2},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var archive bytes.Buffer
			writer := tar.NewWriter(&archive)
			for index, header := range test.headers {
				if err := writer.WriteHeader(header); err != nil {
					t.Fatal(err)
				}
				if _, err := writer.Write(test.bodies[index]); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := untar(bytes.NewReader(archive.Bytes()), t.TempDir(), test.policy); err == nil {
				t.Fatalf("untar() accepted %s bomb", test.name)
			}
		})
	}
}
