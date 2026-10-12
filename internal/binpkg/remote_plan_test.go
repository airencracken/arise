package binpkg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdvertisedBinaryMetadataPreservesSubslot(t *testing.T) {
	base := t.TempDir()
	image := filepath.Join(base, "image")
	if err := os.Mkdir(image, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "demo-1.gpkg.tar")
	meta := map[string][]byte{"CATEGORY": []byte("app-misc"), "PF": []byte("demo-1"), "SLOT": []byte("0/abi2"), "EAPI": []byte("8")}
	if err := CreateGPKG(context.Background(), GPKGCreateRequest{Path: path, Basename: "demo-1", ImageRoot: image, Metadata: meta}); err != nil {
		t.Fatal(err)
	}
	info, err := ReadInfo(path)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := NewPackageIndexEntry(path, base, info, meta)
	if err != nil {
		t.Fatal(err)
	}
	if entry["SLOT"] != "0/abi2" {
		t.Errorf("advertised SLOT = %q", entry["SLOT"])
	}
	advertised, err := PackageInfoFromIndex(entry, filepath.Join(base, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	if advertised.Slot != info.Slot || advertised.Subslot != info.Subslot {
		t.Errorf("advertised ABI differs: %+v vs %+v", advertised, info)
	}
}

func TestCompatibleCandidatesSelectNewestUsableBuild(t *testing.T) {
	older := &BinPkgInfo{Category: "app-misc", Package: "demo", Version: "1", BuildID: "2", Use: "feature", Path: "older"}
	newer := &BinPkgInfo{Category: "app-misc", Package: "demo", Version: "1", BuildID: "10", Use: "feature", Path: "newer"}
	incompatible := &BinPkgInfo{Category: "app-misc", Package: "demo", Version: "1", BuildID: "20", Path: "wrong-use"}
	policy := CompatibilityPolicy{RespectUse: true, IUse: "feature", UseFlags: map[string]bool{"feature": true}}
	for _, candidates := range [][]*BinPkgInfo{{older, newer, incompatible}, {incompatible, newer, older}} {
		got, err := FindCompatibleCandidate(candidates, "=app-misc/demo-1", policy)
		if err != nil || got != newer {
			t.Fatalf("newest compatible instance = %+v, %v", got, err)
		}
	}
}

func TestAdvertisedBinaryRejectsUnusableAcquisitionMetadata(t *testing.T) {
	valid := PackageIndexEntry{"CPV": "app-misc/demo-1", "PATH": "app-misc/demo/demo-1.gpkg.tar", "SIZE": "12", "SHA512": strings.Repeat("a", 128), "SLOT": "0", "EAPI": "8"}
	for key, value := range map[string]string{"CPV": "invalid", "PATH": "../escape", "SIZE": "0", "SHA512": "", "SLOT": "", "EAPI": ""} {
		t.Run(key, func(t *testing.T) {
			entry := cloneStringMap(valid)
			entry[key] = value
			if _, err := PackageInfoFromIndex(entry, t.TempDir()); err == nil {
				t.Fatal("unusable remote entry accepted")
			}
		})
	}
}
