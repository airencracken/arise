package binpkg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseContentsPathsWithSpaces(t *testing.T) {
	for _, test := range []struct {
		line   string
		path   string
		target string
	}{
		{"obj /lib/firmware/brcm/brcmfmac43241b4-sdio.Intel Corp.-VALLEYVIEW C0 PLATFORM.txt b219396d9341e6eabc9c4d6eb3e8a87c 1784921933", "/lib/firmware/brcm/brcmfmac43241b4-sdio.Intel Corp.-VALLEYVIEW C0 PLATFORM.txt", ""},
		{"obj /lib/firmware/brcm/brcmfmac4356-pcie.Intel Corporation-CHERRYVIEW D1 PLATFORM.txt 85fa34ffea0dc3c7d5fda13d4e4f293d 1784921933", "/lib/firmware/brcm/brcmfmac4356-pcie.Intel Corporation-CHERRYVIEW D1 PLATFORM.txt", ""},
		{"sym /lib/firmware/brcm/brcmfmac43455-sdio.Raspberry Pi Foundation-Raspberry Pi 4 Model B.txt -> brcmfmac43455-sdio.raspberrypi,4-model-b.txt bbfa4a58812bdbf5f01678c083ad5d57 1784921960", "/lib/firmware/brcm/brcmfmac43455-sdio.Raspberry Pi Foundation-Raspberry Pi 4 Model B.txt", "brcmfmac43455-sdio.raspberrypi,4-model-b.txt"},
		{"sym /lib/firmware/brcm/brcmfmac43455-sdio.Raspberry Pi Foundation-Raspberry Pi Compute Module 4.txt -> brcmfmac43455-sdio.raspberrypi,4-model-b.txt bbfa4a58812bdbf5f01678c083ad5d57 1784921960", "/lib/firmware/brcm/brcmfmac43455-sdio.Raspberry Pi Foundation-Raspberry Pi Compute Module 4.txt", "brcmfmac43455-sdio.raspberrypi,4-model-b.txt"},
		{"dir /usr/share/two  spaces ", "/usr/share/two  spaces ", ""},
		{"obj /usr/share/two  spaces  digest 1700000000", "/usr/share/two  spaces ", ""},
		{"sym /usr/bin/link->with spaces -> target 1700000000", "/usr/bin/link->with spaces", "target"},
	} {
		t.Run(test.path, func(t *testing.T) {
			entry, err := parseContentsLine(test.line)
			if err != nil {
				t.Fatal(err)
			}
			if entry.Path != test.path || entry.Target != test.target {
				t.Fatalf("parsed entry = %+v; want path %q and target %q", entry, test.path, test.target)
			}
		})
	}
}

func TestCaptureAndRestorePathsWithSpaces(t *testing.T) {
	base := t.TempDir()
	directory := "/usr/share/firmware  files "
	file := directory + "/Intel Corp.-VALLEYVIEW C0 PLATFORM.txt"
	link := directory + "/Raspberry Pi 4 Model B.txt"
	const target = "Intel.txt"
	contents := "dir " + directory + "\nobj " + file + " digest 1784921933\nsym " + link + " -> " + target + " bbfa4a58812bdbf5f01678c083ad5d57 1784921960\n"
	vdb, root := createCaptureFixture(t, base, contents)
	if err := os.MkdirAll(filepath.Join(root, directory), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, file), []byte("firmware"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
		t.Fatal(err)
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
				if len(manifest.Payload) != 3 {
					t.Fatalf("payload entries = %d, want 3", len(manifest.Payload))
				}
			}
			destination := t.TempDir()
			if err := Extract(context.Background(), artifact, destination); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(destination, file))
			if err != nil || string(data) != "firmware" {
				t.Fatalf("restored file = %q, %v", data, err)
			}
			got, err := os.Readlink(filepath.Join(destination, link))
			if err != nil || got != target {
				t.Fatalf("restored symlink = %q, %v", got, err)
			}
		})
	}
}

func TestParseContentsMalformedSpacedPaths(t *testing.T) {
	for _, line := range []string{
		"dir ", "obj /path with spaces digest invalid", "obj /path", "obj /path digest",
		"sym /path with spaces target 1700000000", "sym  -> target 1700000000",
	} {
		if entry, err := parseContentsLine(line); err == nil || entry != nil {
			t.Errorf("parseContentsLine(%q) = %+v, %v; want rejection", line, entry, err)
		}
	}
	path := filepath.Join(t.TempDir(), "CONTENTS")
	if err := os.WriteFile(path, []byte("dir /valid path\nobj /bad path digest invalid\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if entries, err := parseContents(path); err == nil || !strings.Contains(err.Error(), "line 2:") || entries != nil {
		t.Fatalf("parseContents() = %+v, %v; want line 2 rejection without partial results", entries, err)
	}
}
