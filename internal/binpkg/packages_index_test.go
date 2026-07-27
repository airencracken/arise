package binpkg

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPackagesIndexRoundTripCanonicalAndInherited(t *testing.T) {
	index := &PackagesIndex{
		Header: map[string]string{"VERSION": "0", "ARCH": "amd64"},
		Packages: []PackageIndexEntry{
			{"CPV": "app-test/pkg-2", "PATH": "app-test/pkg/pkg-2-2.gpkg.tar", "BUILD_ID": "2"},
			{"CPV": "app-test/pkg-1", "PATH": "app-test/pkg/pkg-1-1.gpkg.tar", "BUILD_ID": "1"},
		},
	}
	encoded, err := index.Encode(time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte("PACKAGES: 2\n")) ||
		bytes.Index(encoded, []byte("CPV: app-test/pkg-1")) > bytes.Index(encoded, []byte("CPV: app-test/pkg-2")) {
		t.Fatalf("non-canonical Packages index:\n%s", encoded)
	}
	parsed, err := ParsePackagesIndex(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Packages) != 2 || parsed.Packages[0]["ARCH"] != "amd64" {
		t.Fatalf("parsed index = %+v", parsed)
	}
}

func TestPackagesIndexRejectsAdversarialContracts(t *testing.T) {
	tests := map[string]string{
		"count":     "PACKAGES: 2\n\nCPV: app/pkg-1\nPATH: app/pkg-1.gpkg.tar\n\n",
		"traversal": "PACKAGES: 1\n\nCPV: app/pkg-1\nPATH: ../pkg.gpkg.tar\n\n",
		"duplicate": "PACKAGES: 1\n\nCPV: app/pkg-1\nCPV: app/pkg-2\nPATH: app/pkg.gpkg.tar\n\n",
		"missing":   "PACKAGES: 1\n\nCPV: app/pkg-1\n\n",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParsePackagesIndex(strings.NewReader(input)); err == nil {
				t.Fatalf("ParsePackagesIndex() accepted %s", name)
			}
		})
	}
}

func TestSelectPackageInstanceUsesExactOrNewestBuildID(t *testing.T) {
	entries := []PackageIndexEntry{
		{"CPV": "app/pkg-1", "BUILD_ID": "2", "PATH": "two"},
		{"CPV": "app/pkg-1", "BUILD_ID": "10", "PATH": "ten"},
		{"CPV": "app/other-1", "BUILD_ID": "99", "PATH": "other"},
	}
	selected, err := SelectPackageInstance(entries, "app/pkg-1", "")
	if err != nil || selected["BUILD_ID"] != "10" {
		t.Fatalf("newest selection = %+v, %v", selected, err)
	}
	selected, err = SelectPackageInstance(entries, "app/pkg-1", "2")
	if err != nil || selected["PATH"] != "two" {
		t.Fatalf("exact selection = %+v, %v", selected, err)
	}
}

func TestPackagesIndexCrossReadsWithInstalledPortage(t *testing.T) {
	python, err := exec.LookPath("python3.14")
	if err != nil {
		t.Skip("installed Portage Python is unavailable")
	}
	index := &PackagesIndex{
		Header: map[string]string{"VERSION": "0", "ARCH": "amd64"},
		Packages: []PackageIndexEntry{{
			"CPV": "app-test/pkg-1", "PATH": "app-test/pkg/pkg-1-7.gpkg.tar",
			"BUILD_ID": "7", "SLOT": "0",
		}},
	}
	path := filepath.Join(t.TempDir(), "Packages")
	if err := WritePackagesIndex(path, index, time.Unix(1_700_000_000, 0)); err != nil {
		t.Fatal(err)
	}
	script := "import sys\nfrom portage.getbinpkg import PackageIndex\np=PackageIndex()\nwith open(sys.argv[1]) as f:p.read(f)\nprint(p.packages[0]['CPV'],p.packages[0]['BUILD_ID'])\n"
	command := exec.Command(python, "-c", script, path)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Portage rejected Arise Packages index: %v\n%s", err, output)
	}
	if string(output) != "app-test/pkg-1 7\n" {
		t.Fatalf("Portage Packages result = %q", output)
	}
}

func TestPackagesIndexEncodeParseProperty(t *testing.T) {
	for count := 1; count <= 64; count++ {
		index := &PackagesIndex{Header: map[string]string{"VERSION": "0"}}
		for build := count; build >= 1; build-- {
			index.Packages = append(index.Packages, PackageIndexEntry{
				"CPV":      "app-test/pkg-" + strconv.Itoa(build),
				"PATH":     "app-test/pkg/pkg-" + strconv.Itoa(build) + ".gpkg.tar",
				"BUILD_ID": strconv.Itoa(build),
			})
		}
		encoded, err := index.Encode(time.Unix(1_700_000_000, 0))
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := ParsePackagesIndex(bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		if len(parsed.Packages) != count {
			t.Fatalf("count %d round trip produced %d entries", count, len(parsed.Packages))
		}
	}
}
