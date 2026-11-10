package binpkg

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/portage"
)

func TestLegacyXPAKExecutionABICannotBeHiddenOrOverridden(t *testing.T) {
	for _, change := range []string{"missing top-level marker", "conflicting marker"} {
		t.Run(change, func(t *testing.T) {
			base := t.TempDir()
			vdb, root := createMockVDB(t, base)
			if err := os.WriteFile(filepath.Join(vdb, "ARISE_PHASE_ENV_ABI"), []byte("6\n"), 0644); err != nil {
				t.Fatal(err)
			}
			path, err := Create(context.Background(), vdb, root, filepath.Join(base, "packages"))
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if change == "missing top-level marker" {
				data = bytes.ReplaceAll(data, []byte("ARISE_PHASE_ENV_ABI"), []byte("ARISE_PHASE_ENV_OLD"))
			} else {
				_, start, ok := xpakValueRangeForTest(data, "ARISE_PHASE_ENV_ABI")
				if !ok {
					t.Fatal("missing top-level marker in fixture")
				}
				data[start] = '7'
			}
			if err := os.WriteFile(path, data, 0644); err != nil {
				t.Fatal(err)
			}
			info, err := ReadInfo(path)
			if change == "conflicting marker" {
				if err == nil || !strings.Contains(err.Error(), "execution ABI disagrees") {
					t.Fatalf("conflicting marker accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := ReadMetadata(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			entry, err := NewPackageIndexEntry(path, filepath.Join(base, "packages"), info, metadata)
			if err != nil || entry["ARISE_PHASE_ENV_ABI"] != "6" {
				t.Fatalf("index lost captured ABI: %#v, %v", entry, err)
			}
			selected, err := FindCompatibleCandidate([]*BinPkgInfo{info}, "=sys-devel/mockpkg-1.0", CompatibilityPolicy{})
			if err != nil || selected != nil {
				t.Fatalf("legacy ABI 6 capture selected: %v, %v", selected, err)
			}
		})
	}
}

func TestBinaryExecutionABIIsPreservedAndChecked(t *testing.T) {
	for _, format := range []string{"xpak", "gpkg"} {
		for _, abi := range []string{"6", portage.PhaseEnvironmentABI, "", "unknown"} {
			t.Run(format+"/"+abi, func(t *testing.T) {
				base := t.TempDir()
				vdb, root := createMockVDB(t, base)
				if abi != "" {
					if err := os.WriteFile(filepath.Join(vdb, "ARISE_PHASE_ENV_ABI"), []byte(abi+"\n"), 0644); err != nil {
						t.Fatal(err)
					}
				}
				packages := filepath.Join(base, "packages")
				create := Create
				if format == "gpkg" {
					create = CreateInstalledGPKG
				}
				path, err := create(context.Background(), vdb, root, packages)
				if err != nil {
					t.Fatal(err)
				}
				metadata, err := ReadMetadata(context.Background(), path)
				if err != nil || strings.TrimSpace(string(metadata["ARISE_PHASE_ENV_ABI"])) != abi {
					t.Fatalf("binary lost its execution ABI: metadata=%q, error=%v", metadata["ARISE_PHASE_ENV_ABI"], err)
				}
				info, err := ReadInfo(path)
				if err != nil {
					t.Fatal(err)
				}
				entry, err := NewPackageIndexEntry(path, packages, info, metadata)
				if err != nil {
					t.Fatal(err)
				}
				if entry["ARISE_PHASE_ENV_ABI"] != abi {
					t.Fatalf("index lost execution ABI: %#v", entry)
				}
				remote, err := PackageInfoFromIndex(entry, packages)
				if err != nil {
					t.Fatal(err)
				}
				for name, candidates := range map[string][]*BinPkgInfo{"local": {info}, "remote": {remote}} {
					selected, err := FindCompatibleCandidate(candidates, "=sys-devel/mockpkg-1.0", CompatibilityPolicy{})
					want := abi == "" || abi == portage.PhaseEnvironmentABI
					if err != nil || (selected != nil) != want {
						t.Fatalf("%s binary ABI %q selected=%v, error=%v; want selected=%v", name, abi, selected, err, want)
					}
				}
			})
		}
	}
}
