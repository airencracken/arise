package binpkg

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParseContentsSymlinkFormats(t *testing.T) {
	for _, test := range []struct {
		name string
		line string
	}{
		{"portage", "sym /usr/bin/go -> ../lib/go/bin/go 1784921240"},
		{"checksummed", "sym /usr/bin/go -> ../lib/go/bin/go fd9b1c2c3a76082fa4c70377f3165997 1784921240"},
		{"uppercase-checksum", "sym /usr/bin/go -> ../lib/go/bin/go FD9B1C2C3A76082FA4C70377F3165997 1784921240"},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry, err := parseContentsLine(test.line)
			if err != nil {
				t.Fatal(err)
			}
			if entry.Type != "sym" || entry.Path != "/usr/bin/go" ||
				entry.Target != "../lib/go/bin/go" || entry.Mtime != 1784921240 {
				t.Fatalf("incorrect symlink evidence: %+v", entry)
			}
		})
	}
}

func TestParseContentsSymlinkRejectsMalformedEvidence(t *testing.T) {
	for _, line := range []string{
		"sym /usr/bin/go -> ../lib/go/bin/go invalid 1784921240",
		"sym /usr/bin/go -> ../lib/go/bin/go fd9b1c2c3a76082fa4c70377f316599 1784921240",
		"sym /usr/bin/go -> ../lib/go/bin/go gd9b1c2c3a76082fa4c70377f3165997 1784921240",
		"sym /usr/bin/go -> ../lib/go/bin/go fd9b1c2c3a76082fa4c70377f3165997 invalid",
		"sym /usr/bin/go -> ../lib/go/bin/go fd9b1c2c3a76082fa4c70377f3165997 1784921240 extra",
		"sym /usr/bin/go -> 1784921240",
	} {
		t.Run(line, func(t *testing.T) {
			if entry, err := parseContentsLine(line); err == nil || entry != nil {
				t.Fatalf("accepted malformed symlink: %+v, %v", entry, err)
			}
		})
	}
}

func TestCaptureAndRestoreChecksummedSymlinks(t *testing.T) {
	base := t.TempDir()
	// These are the records that blocked recovery capture of installed Go.
	contents := "dir /usr\ndir /usr/bin\n" +
		"sym /usr/bin/go -> ../lib/go/bin/go fd9b1c2c3a76082fa4c70377f3165997 1784921240\n" +
		"sym /usr/bin/gofmt -> ../lib/go/bin/gofmt 7c14f5102800be2c68504721493a3a6c 1784921240\n"
	vdb, root := createCaptureFixture(t, base, contents)
	for _, name := range []string{"go", "gofmt"} {
		if err := os.Symlink("../lib/go/bin/"+name, filepath.Join(root, "usr", "bin", name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, format := range []struct {
		name   string
		create func(context.Context, string, string, string) (string, error)
	}{
		{"recovery", Create},
		{"gpkg", CreateInstalledGPKG},
	} {
		t.Run(format.name, func(t *testing.T) {
			artifact, err := format.create(context.Background(), vdb, root, filepath.Join(base, format.name))
			if err != nil {
				t.Fatal(err)
			}
			if format.name == "recovery" {
				manifest, err := ReadRecoveryManifest(artifact)
				if err != nil {
					t.Fatal(err)
				}
				links := 0
				for _, evidence := range manifest.Payload {
					if evidence.Type == "symlink" {
						links++
						if evidence.LinkTarget != "../lib/go/bin/"+filepath.Base(evidence.Path) {
							t.Fatalf("incorrect captured target: %+v", evidence)
						}
					}
				}
				if links != 2 {
					t.Fatalf("captured %d symlinks, want 2", links)
				}
			}
			destination := t.TempDir()
			if err := Extract(context.Background(), artifact, destination); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"go", "gofmt"} {
				target, err := os.Readlink(filepath.Join(destination, "usr", "bin", name))
				if err != nil {
					t.Fatal(err)
				}
				if target != "../lib/go/bin/"+name {
					t.Fatalf("restored %s target %q", name, target)
				}
			}
		})
	}
}
