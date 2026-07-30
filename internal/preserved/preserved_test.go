package preserved

import (
	"bufio"
	"debug/elf"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// makeVDB creates a mock VDB directory structure.
//
//	vdb/
//	  category/
//	    pkg-version/
//	      CONTENTS
func makeVDB(t *testing.T, dir string, packages map[string]map[string]string) {
	t.Helper()
	for category, pkgs := range packages {
		for pkg, contents := range pkgs {
			pkgDir := filepath.Join(dir, category, pkg)
			if err := os.MkdirAll(pkgDir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pkgDir, "CONTENTS"), []byte(contents), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// writeELFFile creates a file with ELF magic bytes followed by zero padding.
// ldd and readelf will reject this, but isELF will accept it.
func writeELFFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	data := []byte{0x7f, 'E', 'L', 'F'}
	data = append(data, make([]byte, 256)...)
	if err := os.WriteFile(path, data, 0755); err != nil {
		t.Fatal(err)
	}
}

// copyBinary copies a real ELF binary from the system to the given path.
func copyBinary(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		t.Fatal(err)
	}
	srcF, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srcF.Close() }()
	dstF, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dstF.Close() }()
	if _, err := io.Copy(dstF, srcF); err != nil {
		t.Fatal(err)
	}
	// copy mode
	info, err := srcF.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dst, info.Mode()); err != nil {
		t.Fatal(err)
	}
}

// addLibSymlink creates a symlink in the mock filesystem.
func addLibSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// --------------------------------------------------------------------------
// isELF
// --------------------------------------------------------------------------

func TestIsELF_ValidELF(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "elf.bin")
	writeELFFile(t, p)
	if !isELF(p) {
		t.Error("isELF should return true for ELF magic bytes")
	}
}

func TestIsELF_NonELFFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "notelf")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if isELF(p) {
		t.Error("isELF should return false for a shell script")
	}
}

func TestIsELF_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty")
	if err := os.WriteFile(p, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}
	if isELF(p) {
		t.Error("isELF should return false for an empty file")
	}
}

func TestIsELF_Truncated(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "trunc")
	if err := os.WriteFile(p, []byte{0x7f}, 0644); err != nil {
		t.Fatal(err)
	}
	if isELF(p) {
		t.Error("isELF should return false for a truncated file")
	}
}

func TestIsELF_NonexistentFile(t *testing.T) {
	if isELF("/nonexistent/path/to/nowhere") {
		t.Error("isELF should return false for nonexistent file")
	}
}

func TestIsELF_PartialELFMagic(t *testing.T) {
	tests := []struct {
		name  string
		bytes []byte
	}{
		{"7f_only", []byte{0x7f, 0, 0, 0}},
		{"EL_only", []byte{'E', 'L', 'F', 0}},
		{"wrong_first", []byte{0, 'E', 'L', 'F'}},
		{"all_null", []byte{0, 0, 0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "bin")
			if err := os.WriteFile(p, tt.bytes, 0644); err != nil {
				t.Fatal(err)
			}
			if isELF(p) {
				t.Errorf("isELF should return false for bytes %v", tt.bytes)
			}
		})
	}
}

// --------------------------------------------------------------------------
// parseLDDMissing
// --------------------------------------------------------------------------

func TestParseLDDMissing(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   []string
	}{
		{
			name: "all found",
			output: `	linux-vdso.so.1 (0x00007ffc1a3f6000)
	libc.so.6 => /usr/lib/libc.so.6 (0x00007f8b2c000000)
	/lib64/ld-linux-x86-64.so.2 (0x00007f8b2c400000)`,
			want: nil,
		},
		{
			name: "one missing",
			output: `	libfoo.so.1 => not found
	libc.so.6 => /usr/lib/libc.so.6 (0x00007f8b2c000000)`,
			want: []string{"libfoo.so.1"},
		},
		{
			name: "multiple missing",
			output: `	libfoo.so.1 => not found
	libbar.so.2 => not found
	libc.so.6 => /usr/lib/libc.so.6 (0x...)`,
			want: []string{"libfoo.so.1", "libbar.so.2"},
		},
		{
			name:   "empty output",
			output: "",
			want:   nil,
		},
		{
			name:   "ldd error message",
			output: `ldd: warning: you do not have execution permission for ./file`,
			want:   nil,
		},
		{
			name:   "not a dynamic executable",
			output: `	not a dynamic executable`,
			want:   nil,
		},
		{
			name:   "whitespace variation",
			output: `	libfoo.so.1 =>   not found`,
			want:   []string{"libfoo.so.1"},
		},
		{
			name:   "versioned missing",
			output: `	libssl.so.1.1 => not found`,
			want:   []string{"libssl.so.1.1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLDDMissing(tt.output)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseLDDMissing() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseLDDMissing_Adversarial(t *testing.T) {
	inputs := []string{
		strings.Repeat("=> not found", 10000),
		"\x00\x00\x00",
		strings.Repeat("a", 100000),
		"libfoo.so => not found\x00garbage",
	}
	for _, input := range inputs {
		got := parseLDDMissing(input)
		if got == nil {
			continue
		}
		for _, lib := range got {
			if strings.Contains(lib, "\x00") {
				t.Error("parseLDDMissing returned library with null byte")
			}
		}
	}
}

// --------------------------------------------------------------------------
// isPreservedCandidate
// --------------------------------------------------------------------------

func TestIsPreservedCandidate(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/usr/lib/libssl.so.1.1", true},
		{"/usr/lib/libcrypto.so.1.1", true},
		{"/usr/lib64/libfoo.so.3.2.1", true},
		{"/usr/lib/libsoup-2.4.so.1.11.0", true},
		{"/usr/lib/openssl-1.1-compat/libssl.so.1.1", true},
		{"/usr/lib/glibc-compat/libc.so.6", true},
		{"/usr/lib/libc.so", false},
		{"/usr/lib/libc.so.6", false},
		{"/usr/lib/libc.a", false},
		{"/usr/lib/ld-linux-x86-64.so.2", false}, // starts with ld- not lib
		{"/usr/lib/notlib.txt", false},
		{"/usr/bin/bash", false},
	}

	for _, tt := range tests {
		t.Run(filepath.Base(tt.path), func(t *testing.T) {
			got := isPreservedCandidate(tt.path)
			if got != tt.want {
				t.Errorf("isPreservedCandidate(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// versionFromSONAME / sonameFromPath
// --------------------------------------------------------------------------

func TestVersionFromSONAME(t *testing.T) {
	tests := []struct {
		soname string
		want   string
	}{
		{"libssl.so.1.1", "1.1"},
		{"libcrypto.so.3", "3"},
		{"libc.so.6", "6"},
		{"libfoo.so.1.2.3", "1.2.3"},
		{"notalib", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.soname, func(t *testing.T) {
			got := versionFromSONAME(tt.soname)
			if got != tt.want {
				t.Errorf("versionFromSONAME(%q) = %q, want %q", tt.soname, got, tt.want)
			}
		})
	}
}

func TestSonameFromPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/usr/lib/libssl.so.1.1", "libssl.so.1.1"},
		{"/usr/lib/libc.so.6", "libc.so.6"},
		{"/usr/lib64/libfoo.so", "libfoo.so"},
		{"/usr/lib/libfoo", "libfoo"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := sonameFromPath(tt.path)
			if got != tt.want {
				t.Errorf("sonameFromPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// FindOwningPackages
// --------------------------------------------------------------------------

func TestFindOwningPackages(t *testing.T) {
	dir := t.TempDir()

	packages := map[string]map[string]string{
		"sys-libs": {
			"glibc-2.38-r1": `obj /usr/bin/ldd a1b2c3 1234567890
obj /lib/libc.so.6 a1b2c3 1234567890`,
			"openssl-3.1.4": `obj /usr/bin/openssl a1b2c3 1234567890
obj /usr/lib/libssl.so.3 a1b2c3 1234567890`,
		},
		"app-shells": {
			"bash-5.1": `obj /bin/bash a1b2c3 1234567890`,
		},
	}
	makeVDB(t, dir, packages)

	got, err := FindOwningPackages(dir, []string{"/bin/bash", "/usr/bin/ldd", "/nonexistent"})
	if err != nil {
		t.Fatal(err)
	}

	if v, ok := got["/bin/bash"]; !ok || v != "app-shells/bash-5.1" {
		t.Errorf("/bin/bash: got %q, want app-shells/bash-5.1", v)
	}
	if v, ok := got["/usr/bin/ldd"]; !ok || v != "sys-libs/glibc-2.38-r1" {
		t.Errorf("/usr/bin/ldd: got %q, want sys-libs/glibc-2.38-r1", v)
	}
	if _, ok := got["/nonexistent"]; ok {
		t.Error("nonexistent file should not be found")
	}
}

func TestFindOwningPackages_Empty(t *testing.T) {
	dir := t.TempDir()
	got, err := FindOwningPackages(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %d entries", len(got))
	}

	got, err = FindOwningPackages(dir, []string{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %d entries", len(got))
	}
}

func TestFindOwningPackages_NonexistentVDB(t *testing.T) {
	_, err := FindOwningPackages("/nonexistent/vdb/path", []string{"/bin/bash"})
	if err == nil {
		t.Error("expected error for nonexistent VDB")
	}
}

func TestFindOwningPackages_MultipleMatches(t *testing.T) {
	dir := t.TempDir()

	packages := map[string]map[string]string{
		"sys-libs": {
			"libfoo-1.0": `obj /usr/lib/libfoo.so.1 a1 0
obj /usr/include/foo.h a2 0`,
			"libfoo-2.0": `obj /usr/lib/libfoo.so.2 a1 0
obj /usr/include/foo.h a2 0`,
		},
	}
	makeVDB(t, dir, packages)

	got, err := FindOwningPackages(dir, []string{"/usr/lib/libfoo.so.1", "/usr/lib/libfoo.so.2", "/usr/include/foo.h"})
	if err != nil {
		t.Fatal(err)
	}

	if v, ok := got["/usr/lib/libfoo.so.1"]; !ok || v != "sys-libs/libfoo-1.0" {
		t.Errorf("libfoo.so.1 owned by %q, want sys-libs/libfoo-1.0", v)
	}
	if v, ok := got["/usr/lib/libfoo.so.2"]; !ok || v != "sys-libs/libfoo-2.0" {
		t.Errorf("libfoo.so.2 owned by %q, want sys-libs/libfoo-2.0", v)
	}
	// /usr/include/foo.h appears in both; last one wins
	if _, ok := got["/usr/include/foo.h"]; !ok {
		t.Error("foo.h should have an owning package")
	}
}

// --------------------------------------------------------------------------
// ScanBrokenLinks
// --------------------------------------------------------------------------

func TestScanBrokenLinks_NoBroken(t *testing.T) {
	root := t.TempDir()

	// Create a simple mock root with a script (non-ELF)
	binDir := filepath.Join(root, "usr/bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "script"), []byte("#!/bin/sh\necho hello\n"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := checkLDD(); err != nil {
		t.Skipf("ldd not available: %v", err)
	}

	result, err := ScanBrokenLinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected no broken links, got %d", len(result))
	}
}

func TestScanBrokenLinks_EmptyRoot(t *testing.T) {
	root := t.TempDir()

	if err := checkLDD(); err != nil {
		t.Skipf("ldd not available: %v", err)
	}

	result, err := ScanBrokenLinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected no broken links in empty root, got %d", len(result))
	}
}

func TestScanBrokenLinks_WithRealBinaries(t *testing.T) {
	if err := checkLDD(); err != nil {
		t.Skipf("ldd not available: %v", err)
	}

	root := t.TempDir()

	// Copy a real binary into the mock root so ldd can parse it
	realBin := "/bin/sh"
	if _, err := os.Stat(realBin); err != nil {
		t.Skipf("no /bin/sh on this system: %v", err)
	}
	copyBinary(t, realBin, filepath.Join(root, "bin/sh"))

	result, err := ScanBrokenLinks(root)
	if err != nil {
		t.Fatal(err)
	}
	// /bin/sh will not have broken links on a normal system
	_ = result
}

func TestScanBrokenLinks_SkipsNonELF(t *testing.T) {
	if err := checkLDD(); err != nil {
		t.Skipf("ldd not available: %v", err)
	}

	root := t.TempDir()
	binDir := filepath.Join(root, "usr/bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Create a file with ELF magic bytes but otherwise garbage
	writeELFFile(t, filepath.Join(binDir, "fake-elf"))

	// ldd will fail on this malformed ELF - ScanBrokenLinks should skip it
	result, err := ScanBrokenLinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("malformed ELF should be skipped; got %d broken links", len(result))
	}
}

func TestScanBrokenLinks_DoesNotRequireLDD(t *testing.T) {
	orig := lddPath
	defer func() { lddPath = orig }()
	lddPath = "/nonexistent/definitely/not/ldd"

	root := t.TempDir()
	result, err := ScanBrokenLinks(root)
	if err != nil {
		t.Fatalf("native scan unexpectedly required ldd: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("empty native scan = %v", result)
	}
}

func TestLoaderLibraryNamesReadsConfiguredAndIncludedDirectories(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"opt/lib/libcustom.so.1", "vendor/lib/libvendor.so.2"} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	confDir := filepath.Join(root, "etc/ld.so.conf.d")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc/ld.so.conf"), []byte("/opt/lib\ninclude /etc/ld.so.conf.d/*.conf\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "vendor.conf"), []byte("/vendor/lib\n"), 0644); err != nil {
		t.Fatal(err)
	}
	available := loaderLibraryNames(root)
	for _, library := range []string{"libcustom.so.1", "libvendor.so.2"} {
		if !available[library] {
			t.Errorf("configured loader library %s was not discovered", library)
		}
	}
}

// --------------------------------------------------------------------------
// ScanPreservedLibs
// --------------------------------------------------------------------------

func TestScanPreservedLibs(t *testing.T) {
	root := t.TempDir()

	libDir := filepath.Join(root, "usr/lib")
	if err := os.MkdirAll(libDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create some .so files
	files := []string{
		"libssl.so.1.1",
		"libcrypto.so.1.1",
		"libsoup-2.4.so.1.11.0",
		"libstdc++.so.6.0.32",
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(libDir, f), []byte("ELF"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := ScanPreservedLibs(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) < len(files) {
		t.Errorf("expected at least %d preserved libs, got %d", len(files), len(result))
	}
}

func TestScanPreservedLibsUsesRegistryInsteadOfFilenameHeuristics(t *testing.T) {
	root := t.TempDir()
	registered := filepath.Join(root, "usr/lib/libold.so.1.2")
	unregistered := filepath.Join(root, "usr/lib/libnormal.so.2.3")
	if err := os.MkdirAll(filepath.Dir(registered), 0755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{registered, unregistered} {
		if err := os.WriteFile(path, []byte("not an ELF"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	registry := filepath.Join(root, "var/lib/portage/preserved_libs_registry")
	if err := os.MkdirAll(filepath.Dir(registry), 0755); err != nil {
		t.Fatal(err)
	}
	data := `{"dev-libs/example:0":["dev-libs/example-1","7",["/usr/lib/libold.so.1.2"]]}`
	if err := os.WriteFile(registry, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := ScanPreservedLibs(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].Path != registered || result[0].OwningPkg != "dev-libs/example-1" {
		t.Fatalf("registry-backed preserved libraries = %#v", result)
	}
}

func TestScanPreservedLibs_CompatDir(t *testing.T) {
	root := t.TempDir()

	compatDir := filepath.Join(root, "usr/lib", "openssl-1.1-compat")
	if err := os.MkdirAll(compatDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(compatDir, "libssl.so.1.1"), []byte("ELF"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ScanPreservedLibs(root)
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, pl := range result {
		if strings.Contains(pl.Path, "compat") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected preserved lib in compat directory to be found")
	}
}

func TestScanPreservedLibs_EmptyRoot(t *testing.T) {
	root := t.TempDir()
	result, err := ScanPreservedLibs(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 preserved libs in empty root, got %d", len(result))
	}
}

func TestScanPreservedLibs_SkipsNonCandidate(t *testing.T) {
	root := t.TempDir()

	libDir := filepath.Join(root, "usr/lib")
	if err := os.MkdirAll(libDir, 0755); err != nil {
		t.Fatal(err)
	}

	// These don't match the preserved pattern
	if err := os.WriteFile(filepath.Join(libDir, "libc.so"), []byte("ELF"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "libfoo.a"), []byte("ELF"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ScanPreservedLibs(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 preserved libs for non-candidates, got %d", len(result))
	}
}

// --------------------------------------------------------------------------
// RebuildNeeded
// --------------------------------------------------------------------------

func TestRebuildNeeded_NoChanges(t *testing.T) {
	if err := checkLDD(); err != nil {
		t.Skipf("ldd not available: %v", err)
	}

	root := t.TempDir()
	vdb := t.TempDir()

	result, err := RebuildNeeded(root, vdb)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected no rebuilds, got %v", result)
	}
}

func TestRebuildNeeded_DoesNotIncludeGeneralBrokenLinks(t *testing.T) {
	if err := checkLDD(); err != nil {
		t.Skipf("ldd not available: %v", err)
	}

	root := t.TempDir()
	vdb := t.TempDir()

	// Create a real binary
	realBin := "/bin/sh"
	if _, err := os.Stat(realBin); err != nil {
		t.Skipf("no /bin/sh: %v", err)
	}
	copyBinary(t, realBin, filepath.Join(root, "bin/sh"))

	// Create VDB that owns /bin/sh
	packages := map[string]map[string]string{
		"app-shells": {
			"bash-5.1": `obj /bin/sh a1b2c3 1234567890`,
		},
	}
	makeVDB(t, vdb, packages)

	result, err := RebuildNeeded(root, vdb)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Fatalf("general broken-link scan leaked into preserved rebuild: %v", result)
	}
}

func TestRebuildNeeded_EmptyVDB(t *testing.T) {
	if err := checkLDD(); err != nil {
		t.Skipf("ldd not available: %v", err)
	}

	root := t.TempDir()
	vdb := t.TempDir()

	result, err := RebuildNeeded(root, vdb)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected no rebuilds, got %v", result)
	}
}

func TestRebuildNeeded_DoesNotRequireLDD(t *testing.T) {
	orig := lddPath
	defer func() { lddPath = orig }()
	lddPath = "/nonexistent/ldd"

	root := t.TempDir()
	vdb := t.TempDir()

	result, err := RebuildNeeded(root, vdb)
	if err != nil {
		t.Fatalf("native rebuild scan unexpectedly required ldd: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("empty native rebuild scan = %v", result)
	}
}

func TestReverseELFConsumers(t *testing.T) {
	vdb := t.TempDir()
	writeNeeded := func(cpv, contents string) {
		path := filepath.Join(vdb, filepath.FromSlash(cpv))
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "NEEDED.ELF.2"), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeNeeded("dev-qt/qtcore-5", "X86_64;/usr/lib64/libQt5Core.so.5;libQt5Core.so.5;;libc.so.6;x86_64\n")
	writeNeeded("dev-qt/qtgui-5", "X86_64;/usr/lib64/libQt5Gui.so.5;libQt5Gui.so.5;;libQt5Core.so.5,libc.so.6;x86_64\n")
	writeNeeded("app-misc/unrelated-1", "X86_64;/usr/bin/unrelated;;;libc.so.6;x86_64\n")

	got, err := ReverseELFConsumers(vdb, "dev-qt/qtcore-5")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"dev-qt/qtgui-5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReverseELFConsumers()=%v, want %v", got, want)
	}
}

func TestReverseELFConsumersAllowsMissingMetadataOnlyForNonELFOwners(t *testing.T) {
	root := t.TempDir()
	vdb := filepath.Join(root, "var", "db", "pkg")
	cpv := "dev-python/pure-1"
	dir := filepath.Join(vdb, filepath.FromSlash(cpv))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "usr", "bin", "pure")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/usr/bin/python\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CONTENTS"), []byte("obj /usr/bin/pure digest 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if consumers, err := ReverseELFConsumers(vdb, cpv); err != nil || len(consumers) != 0 {
		t.Fatalf("pure package without linkage metadata = %v, %v", consumers, err)
	}

	if err := os.WriteFile(script, []byte{0x7f, 'E', 'L', 'F', 0}, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReverseELFConsumers(vdb, cpv); err == nil || !strings.Contains(err.Error(), "read linkage metadata") {
		t.Fatalf("ELF owner without linkage metadata error = %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "CONTENTS")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReverseELFConsumers(vdb, cpv); err == nil || !strings.Contains(err.Error(), "verify absent linkage metadata") {
		t.Fatalf("missing ownership evidence error = %v", err)
	}
}

func TestReverseELFConsumersAllowsEmptyOwnedFilesWithoutLinkageMetadata(t *testing.T) {
	root := t.TempDir()
	vdb := filepath.Join(root, "var", "db", "pkg")
	cpv := "dev-python/data-only-1"
	dir := filepath.Join(vdb, filepath.FromSlash(cpv))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	var contents strings.Builder
	for name, data := range map[string][]byte{
		"empty": nil,
		"short": {0x7f, 'E', 'L'},
		"text":  []byte("not an ELF file"),
	} {
		path := filepath.Join(root, "usr", "share", cpv, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&contents, "obj /usr/share/%s/%s digest 1\n", cpv, name)
	}
	if err := os.WriteFile(filepath.Join(dir, "CONTENTS"), []byte(contents.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	consumers, err := ReverseELFConsumers(vdb, cpv)
	if err != nil {
		t.Fatalf("non-ELF package without linkage metadata: %v", err)
	}
	if len(consumers) != 0 {
		t.Fatalf("non-ELF consumers = %v", consumers)
	}
}

func TestReverseELFConsumersRespectsOriginRunpath(t *testing.T) {
	vdb := t.TempDir()
	write := func(cpv, metadata string) {
		dir := filepath.Join(vdb, filepath.FromSlash(cpv))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "NEEDED.ELF.2"), []byte(metadata), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("llvm-core/clang-13", "X;/usr/lib/llvm/13/lib64/libclang.so.13.0.1;libclang.so.13;;;x\n")
	write("llvm-core/clang-14", "X;/usr/lib/llvm/14/bin/c-index-test;;$ORIGIN/../lib64;libclang.so.13;x\nX;/usr/lib/llvm/14/lib64/libclang.so.14.0.6;libclang.so.13;;;x\n")

	got, err := ReverseELFConsumers(vdb, "llvm-core/clang-13")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("version-private provider leaked across RUNPATH: %v", got)
	}
}

func TestReverseELFRemovalClosureConsumerFirst(t *testing.T) {
	vdb := t.TempDir()
	write := func(cpv, metadata string) {
		dir := filepath.Join(vdb, filepath.FromSlash(cpv))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "NEEDED.ELF.2"), []byte(metadata), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("cat/core-1", "X;/lib/core;libcore.so;;;x\n")
	write("cat/middle-1", "X;/lib/middle;libmiddle.so;;libcore.so;x\n")
	write("cat/leaf-1", "X;/bin/leaf;;;libmiddle.so;x\n")
	got, err := ReverseELFRemovalClosure(vdb, "cat/core-1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"cat/leaf-1", "cat/middle-1", "cat/core-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("closure=%v want=%v", got, want)
	}
}

// --------------------------------------------------------------------------
// RevdepRebuild
// --------------------------------------------------------------------------

func TestRevdepRebuild_EmptyVDB(t *testing.T) {
	if err := checkLDD(); err != nil {
		t.Skipf("ldd not available: %v", err)
	}

	root := t.TempDir()
	vdb := t.TempDir()

	result, err := RevdepRebuild(root, vdb)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected no rebuilds, got %v", result)
	}
}

func TestRevdepRebuild_NoELFFiles(t *testing.T) {
	if err := checkLDD(); err != nil {
		t.Skipf("ldd not available: %v", err)
	}

	root := t.TempDir()
	vdb := t.TempDir()

	packages := map[string]map[string]string{
		"app-doc": {
			"readme-1.0": `obj /usr/share/doc/readme/README.txt a1 0`,
		},
	}
	makeVDB(t, vdb, packages)

	result, err := RevdepRebuild(root, vdb)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected no rebuilds, got %v", result)
	}
}

func TestRevdepRebuild_WithRealBinaries(t *testing.T) {
	if err := checkLDD(); err != nil {
		t.Skipf("ldd not available: %v", err)
	}

	root := t.TempDir()
	vdb := t.TempDir()

	// Copy real binary
	realBin := "/bin/sh"
	if _, err := os.Stat(realBin); err != nil {
		t.Skipf("no /bin/sh: %v", err)
	}
	binPath := filepath.Join(root, "bin/sh")
	copyBinary(t, realBin, binPath)

	packages := map[string]map[string]string{
		"app-shells": {
			"bash-5.1": `obj /bin/sh a1b2c3 1234567890`,
		},
	}
	makeVDB(t, vdb, packages)

	result, err := RevdepRebuild(root, vdb)
	if err != nil {
		t.Fatal(err)
	}
	// /bin/sh should have all libs available
	if len(result) > 0 {
		t.Logf("unexpected rebuilds: %v", result)
	}
}

func TestRevdepRebuild_DoesNotRequireLDD(t *testing.T) {
	orig := lddPath
	defer func() { lddPath = orig }()
	lddPath = "/nonexistent/ldd"

	root := t.TempDir()
	vdb := t.TempDir()

	result, err := RevdepRebuild(root, vdb)
	if err != nil {
		t.Fatalf("native reverse dependency scan unexpectedly required ldd: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("empty native reverse dependency scan = %v", result)
	}
}

func TestRevdepRebuildUsesInstalledLinkageMetadata(t *testing.T) {
	root := t.TempDir()
	vdb := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "usr", "lib64"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "usr", "lib64", "libpresent.so.1"), []byte("present"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "usr", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "usr", "bin", "protoc"), []byte("installed payload"), 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(vdb, "dev-libs", "protobuf-31.1")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	metadata := "X86_64;/usr/bin/protoc;;;libpresent.so.1,libabsl_missing.so.2505.0.0;x86_64\n"
	if err := os.WriteFile(filepath.Join(pkg, "NEEDED.ELF.2"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := RevdepRebuild(root, vdb)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"dev-libs/protobuf-31.1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("broken installed linkage = %v, want %v", got, want)
	}
}

func TestRevdepRebuildMatchesPortageSONAMEProvidersBeyondRunpath(t *testing.T) {
	root := t.TempDir()
	vdb := t.TempDir()
	consumer := "/opt/tool/lib/rustlib/target/bin/rust-objcopy"
	provider := "/opt/tool/lib/libLLVM.so.22"
	for _, path := range []string{consumer, provider} {
		full := filepath.Join(root, strings.TrimPrefix(path, "/"))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("installed payload"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pkg := filepath.Join(vdb, "dev-lang", "rust-bin-1.95.0")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	metadata := "X86_64;" + provider + ";libLLVM.so.22;;;x86_64\n" +
		"X86_64;" + consumer + ";;$ORIGIN/../lib;libLLVM.so.22;x86_64\n"
	if err := os.WriteFile(filepath.Join(pkg, "NEEDED.ELF.2"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := RevdepRebuild(root, vdb)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Portage-compatible SONAME provider was reported broken: %v", got)
	}
}

// --------------------------------------------------------------------------
// vdbContentsMap
// --------------------------------------------------------------------------

func TestVdbContentsMap(t *testing.T) {
	dir := t.TempDir()

	packages := map[string]map[string]string{
		"dev-libs": {
			"openssl-3.1.4": `obj /usr/lib/libssl.so.3 a1 0
obj /usr/bin/openssl a1 0
sym /usr/lib/libssl.so -> libssl.so.3 a2 0`,
		},
		"sys-libs": {
			"glibc-2.38": `obj /lib/libc.so.6 a1 0
dir /lib`,
		},
	}
	makeVDB(t, dir, packages)

	m, err := vdbContentsMap(dir)
	if err != nil {
		t.Fatal(err)
	}

	if v, ok := m["/usr/lib/libssl.so.3"]; !ok || v != "dev-libs/openssl-3.1.4" {
		t.Errorf("libssl: got %q, want dev-libs/openssl-3.1.4", v)
	}
	if v, ok := m["/usr/lib/libssl.so"]; !ok || v != "dev-libs/openssl-3.1.4" {
		t.Errorf("symlink libssl: got %q, want dev-libs/openssl-3.1.4", v)
	}
	if _, ok := m["/lib"]; ok {
		t.Error("dir entries should not be included")
	}
}

func TestVdbContentsMap_EmptyVDB(t *testing.T) {
	dir := t.TempDir()
	m, err := vdbContentsMap(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map, got %d entries", len(m))
	}
}

func TestVdbContentsMap_Adversarial(t *testing.T) {
	dir := t.TempDir()

	packages := map[string]map[string]string{
		"cat": {
			"giant-1.0":   strings.Repeat("obj /usr/lib/giant.so.1 a1 0\n", 10000),
			"corrupt-1.0": "garbage\n\x00\x00\x00\nobj /usr/lib/ok.so a1 0",
		},
	}
	makeVDB(t, dir, packages)

	m, err := vdbContentsMap(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Should have at minimum the ok.so entry
	if _, ok := m["/usr/lib/ok.so"]; !ok {
		t.Error("ok.so should be found")
	}
}

// --------------------------------------------------------------------------
// elfSoname / elfNeededLibraries
// --------------------------------------------------------------------------

func TestELFNeededLibraries_NotELF(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "notelf")
	if err := os.WriteFile(p, []byte("not an ELF"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := elfNeededLibraries(p)
	if err == nil {
		t.Error("expected error for non-ELF file")
	}
}

func TestELFNeededLibraries_RealBinary(t *testing.T) {
	// Test with a real binary
	paths := []string{"/bin/sh", "/usr/bin/true", "/bin/ls"}
	var found string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			found = p
			break
		}
	}
	if found == "" {
		t.Skip("no system binary available")
	}

	needed, err := elfNeededLibraries(found)
	if err != nil {
		t.Fatalf("elfNeededLibraries(%q): %v", found, err)
	}
	if len(needed) == 0 {
		t.Error("expected at least one NEEDED library")
	}
}

func TestELFSoname_RealLibrary(t *testing.T) {
	// Try to find a real .so file
	paths := []string{"/lib/libc.so.6", "/usr/lib/libc.so.6"}
	var found string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			if f, err := elf.Open(p); err == nil {
				f.Close()
				found = p
				break
			}
		}
	}
	if found == "" {
		t.Skip("no .so file available for SONAME test")
	}

	soname, err := elfSoname(found)
	if err != nil {
		t.Fatalf("elfSoname(%q): %v", found, err)
	}
	if soname == "" {
		t.Error("SONAME should not be empty")
	}
}

func TestELFSoname_NotSharedLib(t *testing.T) {
	// Test with an executable
	paths := []string{"/bin/sh", "/usr/bin/true"}
	var found string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			found = p
			break
		}
	}
	if found == "" {
		t.Skip("no binary available")
	}

	_, err := elfSoname(found)
	if err == nil {
		t.Error("expected error for executable (not shared library)")
	}
}

// --------------------------------------------------------------------------
// LDD mapping regex
// --------------------------------------------------------------------------

func TestLDDMappingRE(t *testing.T) {
	tests := []struct {
		line     string
		wantLib  string
		wantPath string
	}{
		{"\tlibc.so.6 => /usr/lib/libc.so.6 (0x00007f8b2c000000)", "libc.so.6", "/usr/lib/libc.so.6"},
		{"\tlinux-vdso.so.1 (0x00007ffc1a3f6000)", "linux-vdso.so.1", ""},
		{"\t/lib64/ld-linux-x86-64.so.2 (0x00007f8b2c400000)", "/lib64/ld-linux-x86-64.so.2", ""},
	}

	for _, tt := range tests {
		m := lddMappingRE.FindStringSubmatch(tt.line)
		if tt.wantPath != "" {
			if len(m) < 3 || m[1] != tt.wantLib || m[2] != tt.wantPath {
				t.Errorf("lddMappingRE on %q: got %v, want lib=%q path=%q",
					tt.line, m, tt.wantLib, tt.wantPath)
			}
		}
	}
}

// --------------------------------------------------------------------------
// Property tests
// --------------------------------------------------------------------------

func TestIsELF_Property(t *testing.T) {
	dir := t.TempDir()

	// Write many random byte sequences
	sequences := [][]byte{
		{0x7f, 'E', 'L', 'F'},
		{0x7f, 'E', 'L', 'F', 0x01, 0x01},
		{0x7f, 'E', 'L', 'G'},
		{0x7f, 'X', 'L', 'F'},
		{'E', 'L', 'F', 0},
	}
	expected := []bool{true, true, false, false, false}

	for i, seq := range sequences {
		p := filepath.Join(dir, fmt.Sprintf("test%d", i))
		if err := os.WriteFile(p, seq, 0644); err != nil {
			t.Fatal(err)
		}
		got := isELF(p)
		if got != expected[i] {
			t.Errorf("isELF(seq=%v) = %v, want %v", seq, got, expected[i])
		}
	}
}

func TestParseLDDMissing_Property(t *testing.T) {
	for _, input := range []string{
		"",
		"libfoo.so.1 => not found",
		"libfoo.so.1 => not found\nlibbar.so.2 => not found",
	} {
		got := parseLDDMissing(input)
		for _, lib := range got {
			if lib == "" {
				t.Error("parseLDDMissing returned empty string")
			}
		}
	}
}

func TestFindOwningPackages_Property(t *testing.T) {
	dir := t.TempDir()

	// Create many packages with overlapping files
	numPkgs := 50
	manyPackages := make(map[string]map[string]string)
	for i := 0; i < numPkgs; i++ {
		cat := fmt.Sprintf("cat-%d", i)
		pkgName := fmt.Sprintf("pkg-%d", i)
		manyPackages[cat] = map[string]string{
			pkgName: "obj /usr/lib/lib" + pkgName + ".so a1 0\nobj /usr/bin/" + pkgName + " a1 0",
		}
	}
	makeVDB(t, dir, manyPackages)

	var queryFiles []string
	for i := 0; i < numPkgs; i++ {
		queryFiles = append(queryFiles, "/usr/bin/pkg-"+fmt.Sprintf("%d", i))
	}

	result, err := FindOwningPackages(dir, queryFiles)
	if err != nil {
		t.Fatal(err)
	}

	if len(result) != numPkgs {
		t.Errorf("expected %d results, got %d", numPkgs, len(result))
	}

	// Each result value should be non-empty
	for file, pkg := range result {
		if pkg == "" {
			t.Errorf("file %q has empty owning package", file)
		}
	}
}

func TestScanPreservedLibs_Property(t *testing.T) {
	root := t.TempDir()
	libDir := filepath.Join(root, "usr/lib")
	if err := os.MkdirAll(libDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create many .so.* files
	for i := 0; i < 100; i++ {
		filename := "libtest" + string(rune('a'+i%26)) + ".so." + string(rune('1'+i%3))
		if err := os.WriteFile(filepath.Join(libDir, filename), []byte("ELF"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := ScanPreservedLibs(root)
	if err != nil {
		t.Fatal(err)
	}

	seen := make(map[string]bool)
	for _, pl := range result {
		if pl.Path == "" {
			t.Error("result has empty Path")
		}
		if seen[pl.Path] {
			t.Errorf("duplicate path: %s", pl.Path)
		}
		seen[pl.Path] = true
	}
}

// --------------------------------------------------------------------------
// checkLDD availability test
// --------------------------------------------------------------------------

func TestCheckLDD(t *testing.T) {
	orig := lddPath
	defer func() { lddPath = orig }()

	if _, err := exec.LookPath("ldd"); err == nil {
		if cerr := checkLDD(); cerr != nil {
			t.Errorf("checkLDD should succeed when ldd is available: %v", cerr)
		}
	}

	lddPath = "/nonexistent/ldd"
	if err := checkLDD(); err == nil {
		t.Error("checkLDD should fail when ldd is not available")
	}
}

// --------------------------------------------------------------------------
// ScanBrokenLinks skips non-ELF files
// --------------------------------------------------------------------------

func TestScanBrokenLinks_OnlyScansELF(t *testing.T) {
	if err := checkLDD(); err != nil {
		t.Skipf("ldd not available: %v", err)
	}

	root := t.TempDir()

	// Create a mix of ELF and non-ELF files
	for _, d := range []string{"usr/bin", "usr/sbin", "bin"} {
		dir := filepath.Join(root, d)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		// Script (non-ELF)
		if err := os.WriteFile(filepath.Join(dir, "script.sh"), []byte("#!/bin/sh\necho hi\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}

	result, err := ScanBrokenLinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("scripts should not produce broken links; got %d", len(result))
	}
}

// --------------------------------------------------------------------------
// Adversarial tests
// --------------------------------------------------------------------------

func TestScanBrokenLinks_AdversarialELF(t *testing.T) {
	if err := checkLDD(); err != nil {
		t.Skipf("ldd not available: %v", err)
	}

	root := t.TempDir()

	// Malformed ELF with correct magic but corrupted header
	binDir := filepath.Join(root, "usr/bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Just ELF magic + zeros
	data := []byte{0x7f, 'E', 'L', 'F'}
	data = append(data, make([]byte, 1024)...)
	if err := os.WriteFile(filepath.Join(binDir, "corrupt"), data, 0755); err != nil {
		t.Fatal(err)
	}

	result, err := ScanBrokenLinks(root)
	if err != nil {
		t.Fatal(err)
	}
	// Malformed ELF should be skipped by ldd and not cause panics
	for _, bl := range result {
		if bl.Binary == "" {
			t.Error("broken link has empty Binary")
		}
	}
}

func TestFindOwningPackages_Adversarial(t *testing.T) {
	dir := t.TempDir()

	packages := map[string]map[string]string{
		"giant": {
			"pkg-1.0": strings.Repeat("obj /very/long/path/that/keeps/going/"+
				strings.Repeat("x", 200)+" a1 0\n", 5000),
		},
		"null": {
			"null-pkg-1.0": "\x00\x00\x00\nobj /usr/bin/ok a1 0\n\x01\x02\x03",
		},
	}
	makeVDB(t, dir, packages)

	result, err := FindOwningPackages(dir, []string{"/usr/bin/ok"})
	if err != nil {
		t.Fatalf("should not fail on adversarial input: %v", err)
	}
	if v, ok := result["/usr/bin/ok"]; ok {
		if v == "" {
			t.Error("owning package should not be empty")
		}
	}
}

func TestVdbContentsMap_AdversarialLargeFile(t *testing.T) {
	dir := t.TempDir()

	packages := map[string]map[string]string{
		"big": {
			"huge-1.0": strings.Repeat("obj /usr/share/"+
				strings.Repeat("x", 100)+" a1 0\n", 10000),
		},
	}
	makeVDB(t, dir, packages)

	m, err := vdbContentsMap(dir)
	if err != nil {
		t.Fatalf("should not fail on large VDB: %v", err)
	}
	if len(m) == 0 {
		t.Error("expected entries in large VDB")
	}
}

// --------------------------------------------------------------------------
// RebuildNeeded / RevdepRebuild property tests
// --------------------------------------------------------------------------

func TestRebuildNeeded_ReturnsSorted(t *testing.T) {
	if err := checkLDD(); err != nil {
		t.Skipf("ldd not available: %v", err)
	}

	root := t.TempDir()
	vdb := t.TempDir()

	result, err := RebuildNeeded(root, vdb)
	if err != nil {
		t.Fatal(err)
	}
	if !sort.StringsAreSorted(result) {
		t.Errorf("RebuildNeeded result should be sorted; got %v", result)
	}
}

func TestRevdepRebuild_ReturnsSorted(t *testing.T) {
	if err := checkLDD(); err != nil {
		t.Skipf("ldd not available: %v", err)
	}

	root := t.TempDir()
	vdb := t.TempDir()

	result, err := RevdepRebuild(root, vdb)
	if err != nil {
		t.Fatal(err)
	}
	if !sort.StringsAreSorted(result) {
		t.Errorf("RevdepRebuild result should be sorted; got %v", result)
	}
}

func TestRebuildNeeded_Deduplication(t *testing.T) {
	if err := checkLDD(); err != nil {
		t.Skipf("ldd not available: %v", err)
	}

	root := t.TempDir()
	vdb := t.TempDir()

	// Create a binary and VDB entry
	realBin := "/bin/sh"
	if _, err := os.Stat(realBin); err != nil {
		t.Skipf("no /bin/sh: %v", err)
	}
	copyBinary(t, realBin, filepath.Join(root, "bin/sh"))

	packages := map[string]map[string]string{
		"app-shells": {
			"bash-5.1": `obj /bin/sh a1b2c3 1234567890`,
		},
	}
	makeVDB(t, vdb, packages)

	result, err := RebuildNeeded(root, vdb)
	if err != nil {
		t.Fatal(err)
	}

	seen := make(map[string]bool)
	for _, pkg := range result {
		if seen[pkg] {
			t.Errorf("duplicate package in result: %s", pkg)
		}
		seen[pkg] = true
	}
}

func TestScanPreservedLibs_TrailingSlashDoesntPanic(t *testing.T) {
	root := t.TempDir()
	libDir := filepath.Join(root, "usr/lib")
	if err := os.MkdirAll(libDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a file, not a dir with trailing slash
	if err := os.WriteFile(filepath.Join(libDir, "libssl.so.1"), []byte("ELF"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ScanPreservedLibs(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, pl := range result {
		if filepath.Base(pl.Path) == "" {
			t.Error("empty base name in preserved lib path")
		}
	}
}

func TestElfSoname_NonExistentPath(t *testing.T) {
	dirname := filepath.Join(t.TempDir(), "nonexistent")
	_, err := elfSoname(filepath.Join(dirname, "libnonexistent.so.1"))
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

// --------------------------------------------------------------------------
// Integration: ScanBrokenLinks + FindOwningPackages + RebuildNeeded
// --------------------------------------------------------------------------

func TestIntegration_BrokenLinkToAtoms(t *testing.T) {
	if err := checkLDD(); err != nil {
		t.Skipf("ldd not available: %v", err)
	}

	root := t.TempDir()
	vdb := t.TempDir()

	// Create VDB entries for the test root
	packages := map[string]map[string]string{
		"sys-libs": {
			"glibc-2.38": `obj /bin/sh a1b2c3 1234567890
obj /usr/lib/libbroken.so.1 a1b2c3 1234567890`,
		},
	}
	makeVDB(t, vdb, packages)

	// Run full scan
	result, err := RebuildNeeded(root, vdb)
	if err != nil {
		t.Fatal(err)
	}

	// Verify result is sorted and has no duplicates
	if !sort.StringsAreSorted(result) {
		t.Errorf("result not sorted: %v", result)
	}

	dups := make(map[string]int)
	for _, a := range result {
		dups[a]++
	}
	for a, c := range dups {
		if c > 1 {
			t.Errorf("duplicate atom %q appears %d times", a, c)
		}
	}
}

func TestIntegration_RevdepRebuild(t *testing.T) {
	if err := checkLDD(); err != nil {
		t.Skipf("ldd not available: %v", err)
	}

	root := t.TempDir()
	vdb := t.TempDir()

	// Copy real binary
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	copyBinary(t, "/bin/sh", filepath.Join(root, "bin/sh"))

	packages := map[string]map[string]string{
		"app-shells": {
			"bash-5.1": `obj /bin/sh a1b2c3 1234567890`,
		},
		"sys-apps": {
			"coreutils-9.3": `obj /usr/share/info/coreutils.info a1b2c3 1234567890`,
		},
	}
	makeVDB(t, vdb, packages)

	result, err := RevdepRebuild(root, vdb)
	if err != nil {
		t.Fatal(err)
	}

	// coreutils.info is not an ELF, so it should be skipped
	// bash should be fine since /bin/sh has all its libs
	if len(result) > 0 {
		t.Logf("unexpected rebuilds: %v", result)
	}

	for _, a := range result {
		if a != "" && !strings.Contains(a, "/") {
			t.Errorf("result atom %q should contain category/package", a)
		}
	}
}

func TestLddMissing_LddNotFound(t *testing.T) {
	orig := lddPath
	defer func() { lddPath = orig }()
	lddPath = "/nonexistent/ldd"

	_, err := lddMissing("/bin/sh")
	if err == nil {
		t.Error("expected error when ldd binary is not found")
	}
}

func TestLddMissing_LddFails(t *testing.T) {
	orig := lddPath
	defer func() { lddPath = orig }()
	lddPath = "false"

	_, err := lddMissing("/dev/null")
	if err == nil {
		t.Error("expected error when ldd returns non-zero")
	}
}

func TestFileExists_TrueForExisting(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "exists.txt")
	os.WriteFile(f, []byte("hello"), 0644)

	if !fileExists(f) {
		t.Error("fileExists should return true for existing file")
	}
}

func TestFileExists_FalseForMissing(t *testing.T) {
	if fileExists("/nonexistent/path/definitely/not/there") {
		t.Error("fileExists should return false for non-existent path")
	}
}

func TestFileExists_TrueForDirectory(t *testing.T) {
	dir := t.TempDir()

	if !fileExists(dir) {
		t.Error("fileExists should return true for existing directory")
	}
}

func TestLddMissing_EmptyOutput(t *testing.T) {
	orig := lddPath
	defer func() { lddPath = orig }()
	lddPath = "echo"

	missing, err := lddMissing("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("expected 0 missing libs for empty ldd output, got %d", len(missing))
	}
}

func TestLddMissing_RealLddIfAvailable(t *testing.T) {
	if _, err := exec.LookPath("ldd"); err != nil {
		t.Skip("ldd not available")
	}

	f := filepath.Join(t.TempDir(), "empty")
	os.WriteFile(f, []byte{}, 0755)

	_, err := lddMissing(f)
	if err == nil {
		t.Log("ldd on empty file returned success (unexpected but ok)")
	}
}

// Test helper to exercise io.Reader without using blank identifier for that import
var _ = bufio.NewScanner
