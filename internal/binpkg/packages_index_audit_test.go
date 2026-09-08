package binpkg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIndexPublicationRejectsInjectedRecordsAtomically(t *testing.T) {
	for _, attack := range []string{"value newline", "key delimiter", "path", "size", "nul"} {
		t.Run(attack, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "Packages")
			original := []byte("previous complete index\n")
			if err := os.WriteFile(path, original, 0644); err != nil {
				t.Fatal(err)
			}
			entry := PackageIndexEntry{"CPV": "app/pkg-1", "PATH": "app/pkg-1.gpkg.tar"}
			switch attack {
			case "value newline":
				entry["USE"] = "safe\n\nCPV: app/injected-1\nPATH: injected.gpkg.tar"
			case "key delimiter":
				entry["USE: injected"] = "yes"
			case "path":
				entry["PATH"] = "../escape"
			case "size":
				entry["SIZE"] = "-1"
			case "nul":
				entry["USE"] = "x\x00y"
			}
			err := WritePackagesIndex(path, &PackagesIndex{Header: map[string]string{"VERSION": "0"}, Packages: []PackageIndexEntry{entry}}, time.Unix(1, 0))
			if err == nil {
				t.Fatal("invalid index published")
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != string(original) {
				t.Fatalf("previous index changed: %q %v", got, err)
			}
		})
	}
}

func TestPublishedIndexIsReadableByBinhost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Packages")
	if err := WritePackagesIndex(path, &PackagesIndex{Header: map[string]string{"VERSION": "0"}}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatalf("published index mode %o prevents unprivileged readers", info.Mode().Perm())
	}
}

func TestNewestInstanceUsesNumericBuildTime(t *testing.T) {
	for _, times := range [][2]string{{"9", "10"}, {"999999999", "1000000000"}} {
		entries := []PackageIndexEntry{{"CPV": "app/pkg-1", "PATH": "old", "BUILD_TIME": times[0]}, {"CPV": "app/pkg-1", "PATH": "new", "BUILD_TIME": times[1]}}
		for i := 0; i < 2; i++ {
			got, err := SelectPackageInstance(entries, "app/pkg-1", "")
			if err != nil || got["PATH"] != "new" {
				t.Fatalf("wrong newest build for %v: %v %v", times, got, err)
			}
			entries[0], entries[1] = entries[1], entries[0]
		}
	}
	if _, err := ParsePackagesIndex(strings.NewReader("PACKAGES: 1\n\nCPV: app/pkg-1\nPATH: pkg.gpkg.tar\nBUILD_TIME: invalid\n\n")); err == nil {
		t.Fatal("malformed build time accepted")
	}
}

func FuzzPackagesIndexRoundTrip(f *testing.F) {
	f.Add("PACKAGES: 1\n\nCPV: app/pkg-1\nPATH: app/pkg-1.gpkg.tar\nBUILD_TIME: 10\n\n")
	f.Add("PACKAGES: 0\n\n")
	f.Add("PACKAGES: 1\n\nCPV: app/pkg-1\nPATH: ../escape\n\n")
	f.Fuzz(func(t *testing.T, raw string) {
		index, err := ParsePackagesIndex(strings.NewReader(raw))
		if err != nil {
			return
		}
		encoded, err := index.Encode(time.Unix(1, 0))
		if err != nil {
			t.Fatalf("accepted index cannot encode: %v", err)
		}
		roundTrip, err := ParsePackagesIndex(strings.NewReader(string(encoded)))
		if err != nil {
			t.Fatalf("encoded index rejected: %v", err)
		}
		if len(index.Packages) != len(roundTrip.Packages) {
			t.Fatal("round trip changed package count")
		}
	})
}
