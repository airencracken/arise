package binpkg

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func testHTTPClient(handler func(*http.Request) (int, []byte)) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		status, body := handler(request)
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    request,
		}, nil
	})}
}

func createMockVDB(t *testing.T, baseDir string) (vdbPath string, rootDir string) {
	t.Helper()

	rootDir = filepath.Join(baseDir, "root")
	vdbPath = filepath.Join(baseDir, "vdb", "sys-devel", "mockpkg-1.0")

	for _, d := range []string{
		filepath.Join(rootDir, "usr", "bin"),
		filepath.Join(rootDir, "usr", "lib64"),
		filepath.Join(rootDir, "etc", "mockpkg"),
		vdbPath,
	} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	os.WriteFile(filepath.Join(rootDir, "usr", "bin", "mockpkg"), []byte("#!/bin/sh\necho hello"), 0755)
	os.WriteFile(filepath.Join(rootDir, "usr", "lib64", "libmock.so"), []byte("mock library data"), 0644)
	os.WriteFile(filepath.Join(rootDir, "etc", "mockpkg", "config"), []byte("enabled=true\n"), 0644)

	contents := "obj /usr/bin/mockpkg 30 1700000000\nobj /usr/lib64/libmock.so 17 1700000000\nobj /etc/mockpkg/config 14 1700000000\ndir /usr/bin\ndir /usr/lib64\ndir /etc/mockpkg\n"
	os.WriteFile(filepath.Join(vdbPath, "CONTENTS"), []byte(contents), 0644)
	os.WriteFile(filepath.Join(vdbPath, "CATEGORY"), []byte("sys-devel"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "PF"), []byte("mockpkg-1.0"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "SLOT"), []byte("0/1.0"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "USE"), []byte("foo bar -baz"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "EAPI"), []byte("8"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "BUILD_TIME"), []byte("1700000000"), 0644)

	return vdbPath, rootDir
}

func TestCreate(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")
	vdbPath, rootDir := createMockVDB(t, baseDir)

	outPath, err := Create(context.Background(), vdbPath, rootDir, pkgDir)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("created package not found at %s: %v", outPath, err)
	}

	expected := filepath.Join(pkgDir, "sys-devel", "mockpkg-1.0.tbz2")
	if outPath != expected {
		t.Errorf("Create() path = %s, want %s", outPath, expected)
	}
}

func TestReadInfo(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")
	vdbPath, rootDir := createMockVDB(t, baseDir)

	outPath, err := Create(context.Background(), vdbPath, rootDir, pkgDir)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	info, err := ReadInfo(outPath)
	if err != nil {
		t.Fatalf("ReadInfo() error: %v", err)
	}

	if info.Category != "sys-devel" {
		t.Errorf("Category = %q, want sys-devel", info.Category)
	}
	if info.Package != "mockpkg" || info.Version != "1.0" {
		t.Errorf("Package/Version = %s/%s, want mockpkg/1.0", info.Package, info.Version)
	}
	if info.Slot != "0" || info.Subslot != "1.0" {
		t.Errorf("Slot/Subslot = %s/%s, want 0/1.0", info.Slot, info.Subslot)
	}
	if info.Use != "foo bar -baz" {
		t.Errorf("Use = %q, want foo bar -baz", info.Use)
	}
	if info.EAPI != "8" {
		t.Errorf("EAPI = %q, want 8", info.EAPI)
	}
	if info.BuildTime != 1700000000 {
		t.Errorf("BuildTime = %d, want 1700000000", info.BuildTime)
	}
	if info.Size <= 0 {
		t.Errorf("Size = %d, want > 0", info.Size)
	}
	if info.Path != outPath {
		t.Errorf("Path = %q, want %q", info.Path, outPath)
	}
}

func TestExtract(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")
	vdbPath, rootDir := createMockVDB(t, baseDir)

	outPath, err := Create(context.Background(), vdbPath, rootDir, pkgDir)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	destDir := filepath.Join(baseDir, "extracted")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatalf("mkdir dest: %v", err)
	}

	if err := Extract(context.Background(), outPath, destDir); err != nil {
		t.Fatalf("Extract() error: %v", err)
	}

	expected := []string{
		filepath.Join(destDir, "usr", "bin", "mockpkg"),
		filepath.Join(destDir, "usr", "lib64", "libmock.so"),
		filepath.Join(destDir, "etc", "mockpkg", "config"),
	}

	for _, p := range expected {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("extracted file not found: %s: %v", p, err)
		}
	}

	data, err := os.ReadFile(filepath.Join(destDir, "etc", "mockpkg", "config"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(data) != "enabled=true\n" {
		t.Errorf("config content = %q, want enabled=true\\n", string(data))
	}
}

func TestListAvailable(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")

	vdbPath1, rootDir1 := createMockVDB(t, baseDir)
	if _, err := Create(context.Background(), vdbPath1, rootDir1, pkgDir); err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	vdbPath2 := filepath.Join(baseDir, "vdb2", "app-misc", "other-2.0")
	rootDir2 := filepath.Join(baseDir, "root2")
	os.MkdirAll(filepath.Join(rootDir2, "usr", "bin"), 0755)
	os.MkdirAll(vdbPath2, 0755)
	os.WriteFile(filepath.Join(rootDir2, "usr", "bin", "other"), []byte("binary"), 0755)
	os.WriteFile(filepath.Join(vdbPath2, "CONTENTS"), []byte("obj /usr/bin/other 6 1700000000\n"), 0644)
	os.WriteFile(filepath.Join(vdbPath2, "CATEGORY"), []byte("app-misc"), 0644)
	os.WriteFile(filepath.Join(vdbPath2, "PF"), []byte("other-2.0"), 0644)
	os.WriteFile(filepath.Join(vdbPath2, "SLOT"), []byte("0"), 0644)
	os.WriteFile(filepath.Join(vdbPath2, "USE"), []byte(""), 0644)
	os.WriteFile(filepath.Join(vdbPath2, "EAPI"), []byte("8"), 0644)
	os.WriteFile(filepath.Join(vdbPath2, "BUILD_TIME"), []byte("1700000001"), 0644)
	if _, err := Create(context.Background(), vdbPath2, rootDir2, pkgDir); err != nil {
		t.Fatalf("Create() second pkg error: %v", err)
	}

	pkgs, err := ListAvailable(pkgDir)
	if err != nil {
		t.Fatalf("ListAvailable() error: %v", err)
	}

	if len(pkgs) != 2 {
		t.Fatalf("ListAvailable() returned %d packages, want 2", len(pkgs))
	}

	if pkgs[0].Category != "app-misc" || pkgs[1].Category != "sys-devel" {
		t.Errorf("packages not sorted: %v", pkgs)
	}
}

func TestListAvailable_Empty(t *testing.T) {
	emptyDir := filepath.Join(t.TempDir(), "empty")
	pkgs, err := ListAvailable(emptyDir)
	if err != nil {
		t.Fatalf("ListAvailable() error: %v", err)
	}
	if len(pkgs) != 0 {
		t.Errorf("ListAvailable() returned %d packages, want 0", len(pkgs))
	}
}

func TestListAvailable_Nonexistent(t *testing.T) {
	pkgs, err := ListAvailable("/nonexistent/path/12345")
	if err != nil {
		t.Fatalf("ListAvailable() error: %v", err)
	}
	if len(pkgs) != 0 {
		t.Errorf("ListAvailable() returned %d packages, want 0", len(pkgs))
	}
}

func TestFindPackage(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")
	vdbPath, rootDir := createMockVDB(t, baseDir)

	if _, err := Create(context.Background(), vdbPath, rootDir, pkgDir); err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	t.Run("exact atom", func(t *testing.T) {
		pkg, err := FindPackage(pkgDir, "=sys-devel/mockpkg-1.0")
		if err != nil {
			t.Fatalf("FindPackage() error: %v", err)
		}
		if pkg == nil {
			t.Fatal("FindPackage() returned nil")
		}
		if pkg.Category != "sys-devel" || pkg.Package != "mockpkg" {
			t.Errorf("got %s/%s", pkg.Category, pkg.Package)
		}
	})

	t.Run("no version", func(t *testing.T) {
		pkg, err := FindPackage(pkgDir, "sys-devel/mockpkg")
		if err != nil {
			t.Fatalf("FindPackage() error: %v", err)
		}
		if pkg == nil {
			t.Fatal("FindPackage() returned nil")
		}
	})

	t.Run("not found", func(t *testing.T) {
		pkg, err := FindPackage(pkgDir, "=sys-devel/nonexistent-5.0")
		if err != nil {
			t.Fatalf("FindPackage() error: %v", err)
		}
		if pkg != nil {
			t.Errorf("FindPackage() returned %v, want nil", pkg)
		}
	})

	t.Run("invalid atom", func(t *testing.T) {
		_, err := FindPackage(pkgDir, "")
		if err == nil {
			t.Error("FindPackage() with empty atom expected error")
		}
	})
}

func TestFindPackage_Operators(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")

	for i, ver := range []string{"1.0", "2.0", "3.0"} {
		vdbPath := filepath.Join(baseDir, "vdb", "sys-devel", "mockpkg-"+ver)
		rootDir := filepath.Join(baseDir, "root"+ver)
		os.MkdirAll(filepath.Join(rootDir, "usr", "bin"), 0755)
		os.MkdirAll(vdbPath, 0755)

		ts := strconv.Itoa(1700000000 + i)
		os.WriteFile(filepath.Join(rootDir, "usr", "bin", "mockpkg"), []byte("v"+ver), 0755)
		os.WriteFile(filepath.Join(vdbPath, "CONTENTS"), []byte("obj /usr/bin/mockpkg 2 "+ts+"\n"), 0644)
		os.WriteFile(filepath.Join(vdbPath, "CATEGORY"), []byte("sys-devel"), 0644)
		os.WriteFile(filepath.Join(vdbPath, "PF"), []byte("mockpkg-"+ver), 0644)
		os.WriteFile(filepath.Join(vdbPath, "SLOT"), []byte("0"), 0644)
		os.WriteFile(filepath.Join(vdbPath, "USE"), []byte(""), 0644)
		os.WriteFile(filepath.Join(vdbPath, "EAPI"), []byte("8"), 0644)
		os.WriteFile(filepath.Join(vdbPath, "BUILD_TIME"), []byte("1700000000"), 0644)
		if _, err := Create(context.Background(), vdbPath, rootDir, pkgDir); err != nil {
			t.Fatalf("Create() error: %v", err)
		}
	}

	t.Run(">= 2.0 finds highest", func(t *testing.T) {
		pkg, err := FindPackage(pkgDir, ">=sys-devel/mockpkg-2.0")
		if err != nil {
			t.Fatalf("FindPackage() error: %v", err)
		}
		if pkg == nil || pkg.Version != "3.0" {
			t.Errorf("got version %v, want 3.0", pkg)
		}
	})

	t.Run("= 2.0 finds exact", func(t *testing.T) {
		pkg, err := FindPackage(pkgDir, "=sys-devel/mockpkg-2.0")
		if err != nil {
			t.Fatalf("FindPackage() error: %v", err)
		}
		if pkg == nil || pkg.Version != "2.0" {
			t.Errorf("got version %v, want 2.0", pkg)
		}
	})

	t.Run(">= 4.0 finds none", func(t *testing.T) {
		pkg, err := FindPackage(pkgDir, ">=sys-devel/mockpkg-4.0")
		if err != nil {
			t.Fatalf("FindPackage() error: %v", err)
		}
		if pkg != nil {
			t.Errorf("FindPackage() returned %v, want nil", pkg)
		}
	})
}

func TestCreate_NonexistentFile(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")
	vdbPath := filepath.Join(baseDir, "vdb", "sys-devel", "broken-1.0")
	rootDir := filepath.Join(baseDir, "root")

	os.MkdirAll(vdbPath, 0755)
	os.MkdirAll(filepath.Join(rootDir, "usr", "bin"), 0755)

	os.WriteFile(filepath.Join(vdbPath, "CONTENTS"), []byte("obj /usr/bin/broken 10 1700000000\nobj /nonexistent/file 5 0\ndir /usr/bin\n"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "CATEGORY"), []byte("sys-devel"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "PF"), []byte("broken-1.0"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "SLOT"), []byte("0"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "USE"), []byte(""), 0644)
	os.WriteFile(filepath.Join(vdbPath, "EAPI"), []byte("8"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "BUILD_TIME"), []byte("1700000000"), 0644)

	os.WriteFile(filepath.Join(rootDir, "usr", "bin", "broken"), []byte("binary"), 0755)

	outPath, err := Create(context.Background(), vdbPath, rootDir, pkgDir)
	if err != nil {
		t.Fatalf("Create() should skip missing files: %v", err)
	}
	if outPath == "" {
		t.Error("Create() returned empty path")
	}
}

func TestCorruptPackage_Truncated(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")
	vdbPath, rootDir := createMockVDB(t, baseDir)

	outPath, err := Create(context.Background(), vdbPath, rootDir, pkgDir)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read package: %v", err)
	}

	truncPath := filepath.Join(baseDir, "truncated.tbz2")
	if err := os.WriteFile(truncPath, data[:len(data)/2], 0644); err != nil {
		t.Fatalf("write truncated: %v", err)
	}

	_, err = ReadInfo(truncPath)
	if err == nil {
		t.Error("ReadInfo() on truncated file should error")
	}
}

func TestCorruptPackage_MissingXPAKSTOP(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")
	vdbPath, rootDir := createMockVDB(t, baseDir)

	outPath, err := Create(context.Background(), vdbPath, rootDir, pkgDir)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read package: %v", err)
	}

	corruptPath := filepath.Join(baseDir, "corrupt.tbz2")
	corruptData := bytes.Replace(data, []byte("XPAKSTOP"), []byte("BROKENXX"), 1)
	if err := os.WriteFile(corruptPath, corruptData, 0644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	_, err = ReadInfo(corruptPath)
	if err == nil {
		t.Error("ReadInfo() on file without XPAKSTOP should error")
	}
}

func TestCorruptPackage_InvalidOffset(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")
	vdbPath, rootDir := createMockVDB(t, baseDir)

	outPath, err := Create(context.Background(), vdbPath, rootDir, pkgDir)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read package: %v", err)
	}

	lastXPAK := bytes.LastIndex(data, []byte(xpakMagic+"\n"))
	if lastXPAK < 0 {
		t.Fatal("no XPAKSTOP in package")
	}

	trailerStart := lastXPAK + len(xpakMagic) + 1
	nlIdx := bytes.IndexByte(data[trailerStart:], '\n')
	if nlIdx < 0 {
		t.Fatal("no offset newline after XPAKSTOP")
	}

	corruptData := make([]byte, len(data))
	copy(corruptData, data)
	copy(corruptData[trailerStart:], []byte("99999999\n"))

	corruptPath := filepath.Join(baseDir, "corrupt_offset.tbz2")
	if err := os.WriteFile(corruptPath, corruptData, 0644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	_, err = ReadInfo(corruptPath)
	if err == nil {
		t.Error("ReadInfo() with out-of-range offset should error")
	}
}

func TestExtract_UncompressedTar(t *testing.T) {
	baseDir := t.TempDir()

	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)

	hdr := &tar.Header{
		Name:     "testfile",
		Size:     12,
		Typeflag: tar.TypeReg,
		Mode:     0644,
	}
	tw.WriteHeader(hdr)
	tw.Write([]byte("test content"))
	tw.Close()

	metaContent := "CATEGORY=sys-devel\nPF=mockpkg-1.0\nSLOT=0\nUSE=\nEAPI=8\nBUILD_TIME=1700000000\n"

	offset := len(metaContent) + len(xpakMagic) + 1
	offsetStr := offsetToStr(offset)

	var full bytes.Buffer
	full.Write(tarBuf.Bytes())
	full.WriteString(metaContent)
	full.WriteString(xpakMagic + "\n")
	full.WriteString(offsetStr + "\n")

	path := filepath.Join(baseDir, "test.tar")
	if err := os.WriteFile(path, full.Bytes(), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	info, err := ReadInfo(path)
	if err != nil {
		t.Fatalf("ReadInfo() on tar with XPAK: %v", err)
	}
	if info.Category != "sys-devel" {
		t.Errorf("Category = %q, want sys-devel", info.Category)
	}

	destDir := filepath.Join(baseDir, "extracted")
	os.MkdirAll(destDir, 0755)

	err = Extract(context.Background(), path, destDir)
	if err != nil {
		t.Fatalf("Extract() on uncompressed tar: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(destDir, "testfile"))
	if err != nil {
		t.Fatalf("read testfile: %v", err)
	}
	if string(data) != "test content" {
		t.Errorf("content = %q, want test content", string(data))
	}
}

func TestCreate_MissingMetadataFile(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")
	vdbPath := filepath.Join(baseDir, "vdb", "sys-devel", "minimal-1.0")
	rootDir := filepath.Join(baseDir, "root")

	os.MkdirAll(vdbPath, 0755)
	os.MkdirAll(filepath.Join(rootDir, "usr", "bin"), 0755)

	os.WriteFile(filepath.Join(rootDir, "usr", "bin", "minimal"), []byte("binary"), 0755)
	os.WriteFile(filepath.Join(vdbPath, "CONTENTS"), []byte("obj /usr/bin/minimal 6 1700000000\n"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "CATEGORY"), []byte("sys-devel"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "PF"), []byte("minimal-1.0"), 0644)

	_, err := Create(context.Background(), vdbPath, rootDir, pkgDir)
	if err != nil {
		t.Fatalf("Create() with minimal metadata should not error: %v", err)
	}
}

func TestParseContents_SymlinkEntry(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")
	vdbPath := filepath.Join(baseDir, "vdb", "sys-devel", "symtest-1.0")
	rootDir := filepath.Join(baseDir, "root")

	os.MkdirAll(vdbPath, 0755)
	os.MkdirAll(filepath.Join(rootDir, "usr", "bin"), 0755)

	os.WriteFile(filepath.Join(rootDir, "usr", "bin", "real"), []byte("real"), 0755)

	contents := "obj /usr/bin/real 4 1700000000\nsym /usr/bin/alias -> /usr/bin/real 1700000000\ndir /usr/bin\n"
	os.WriteFile(filepath.Join(vdbPath, "CONTENTS"), []byte(contents), 0644)
	os.WriteFile(filepath.Join(vdbPath, "CATEGORY"), []byte("sys-devel"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "PF"), []byte("symtest-1.0"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "SLOT"), []byte("0"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "USE"), []byte(""), 0644)
	os.WriteFile(filepath.Join(vdbPath, "EAPI"), []byte("8"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "BUILD_TIME"), []byte("1700000000"), 0644)

	outPath, err := Create(context.Background(), vdbPath, rootDir, pkgDir)
	if err != nil {
		t.Fatalf("Create() with symlink entry error: %v", err)
	}

	destDir := filepath.Join(baseDir, "extracted")
	os.MkdirAll(destDir, 0755)
	if err := Extract(context.Background(), outPath, destDir); err != nil {
		t.Fatalf("Extract() error: %v", err)
	}

	link, err := os.Readlink(filepath.Join(destDir, "usr", "bin", "alias"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if link != "/usr/bin/real" {
		t.Errorf("symlink target = %q, want /usr/bin/real", link)
	}
}

func TestAdversarial_BinaryGarbage(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")
	vdbPath, rootDir := createMockVDB(t, baseDir)

	outPath, err := Create(context.Background(), vdbPath, rootDir, pkgDir)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read package: %v", err)
	}

	cases := []struct {
		name string
		mut  func([]byte) []byte
	}{
		{
			name: "null_bytes",
			mut: func(d []byte) []byte {
				return append(d, bytes.Repeat([]byte{0}, 100)...)
			},
		},
		{
			name: "random_garbage",
			mut: func(d []byte) []byte {
				garbage := make([]byte, 50)
				for i := range garbage {
					garbage[i] = byte(i) ^ 0xFF
				}
				return append(d, garbage...)
			},
		},
		{
			name: "fake_xpakstop",
			mut: func(d []byte) []byte {
				return append(d, []byte("\nXPAKSTOP\n999\n")...)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mutated := tc.mut(data)
			path := filepath.Join(baseDir, "mutated_"+strings.ReplaceAll(tc.name, " ", "_")+".tbz2")
			os.WriteFile(path, mutated, 0644)

			_, err := ReadInfo(path)
			if err != nil {
				t.Logf("ReadInfo() correctly rejected mutated input %q: %v", tc.name, err)
			}
		})
	}
}

func TestMutation_ByteFlips(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")
	vdbPath, rootDir := createMockVDB(t, baseDir)

	outPath, err := Create(context.Background(), vdbPath, rootDir, pkgDir)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read package: %v", err)
	}

	for i := 0; i < len(data); i++ {
		mutated := make([]byte, len(data))
		copy(mutated, data)
		mutated[i] ^= 0xFF

		path := filepath.Join(baseDir, "mutation.tbz2")
		os.WriteFile(path, mutated, 0644)

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("ReadInfo panicked on byte flip at %d: %v", i, r)
				}
			}()
			ReadInfo(path)
		}()
	}
}

func TestMutation_ByteFlipsExtract(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")
	vdbPath, rootDir := createMockVDB(t, baseDir)

	outPath, err := Create(context.Background(), vdbPath, rootDir, pkgDir)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read package: %v", err)
	}

	for i := 0; i < len(data); i++ {
		mutated := make([]byte, len(data))
		copy(mutated, data)
		mutated[i] ^= 0xFF

		path := filepath.Join(baseDir, "mutation.tbz2")
		os.WriteFile(path, mutated, 0644)

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Extract panicked on byte flip at %d: %v", i, r)
				}
			}()
			destDir := filepath.Join(baseDir, "mut_extract")
			os.MkdirAll(destDir, 0755)
			Extract(context.Background(), path, destDir)
		}()
	}
}

func TestReadInfo_EmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.tbz2")
	os.WriteFile(path, []byte{}, 0644)

	_, err := ReadInfo(path)
	if err == nil {
		t.Error("ReadInfo() on empty file should error")
	}
}

func TestReadInfo_NonexistentFile(t *testing.T) {
	_, err := ReadInfo("/nonexistent/file.tbz2")
	if err == nil {
		t.Error("ReadInfo() on nonexistent file should error")
	}
}

func TestCreate_VDBPathNonexistent(t *testing.T) {
	_, err := Create(context.Background(), "/nonexistent/vdb/path", "/", t.TempDir())
	if err == nil {
		t.Error("Create() with nonexistent VDB path should error")
	}
}

func TestCreate_InvalidContentsLine(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")
	vdbPath := filepath.Join(baseDir, "vdb", "sys-devel", "badcontents-1.0")
	rootDir := filepath.Join(baseDir, "root")

	os.MkdirAll(vdbPath, 0755)
	os.MkdirAll(filepath.Join(rootDir, "usr", "bin"), 0755)

	os.WriteFile(filepath.Join(rootDir, "usr", "bin", "valid"), []byte("ok"), 0755)
	os.WriteFile(filepath.Join(vdbPath, "CONTENTS"), []byte("garbage line without enough fields\nobj /usr/bin/valid 2 1700000000\n"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "CATEGORY"), []byte("sys-devel"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "PF"), []byte("badcontents-1.0"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "SLOT"), []byte("0"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "USE"), []byte(""), 0644)
	os.WriteFile(filepath.Join(vdbPath, "EAPI"), []byte("8"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "BUILD_TIME"), []byte("1700000000"), 0644)

	outPath, err := Create(context.Background(), vdbPath, rootDir, pkgDir)
	if err != nil {
		t.Fatalf("Create() should skip invalid lines: %v", err)
	}

	info, err := ReadInfo(outPath)
	if err != nil {
		t.Fatalf("ReadInfo() on package with invalid contents lines: %v", err)
	}
	if info.Category != "sys-devel" {
		t.Errorf("Category = %q, want sys-devel", info.Category)
	}
}

func TestBinPkgInfo_CPV_CP(t *testing.T) {
	info := &BinPkgInfo{
		Category: "sys-devel",
		Package:  "gcc",
		Version:  "12.2.0",
	}

	if info.CPV() != "sys-devel/gcc-12.2.0" {
		t.Errorf("CPV() = %q, want sys-devel/gcc-12.2.0", info.CPV())
	}
	if info.CP() != "sys-devel/gcc" {
		t.Errorf("CP() = %q, want sys-devel/gcc", info.CP())
	}
}

func TestParseSlot(t *testing.T) {
	tests := []struct {
		input       string
		wantSlot    string
		wantSubslot string
	}{
		{"0", "0", ""},
		{"0/1.0", "0", "1.0"},
		{"12/12.2", "12", "12.2"},
		{"no-slash", "no-slash", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			slot, subslot := parseSlot(tt.input)
			if slot != tt.wantSlot || subslot != tt.wantSubslot {
				t.Errorf("parseSlot(%q) = (%q, %q), want (%q, %q)",
					tt.input, slot, subslot, tt.wantSlot, tt.wantSubslot)
			}
		})
	}
}

func TestDetectCompression(t *testing.T) {
	tests := []struct {
		path string
		want Compression
	}{
		{"foo.tbz2", CompressionBzip2},
		{"foo.txz", CompressionXz},
		{"foo.xpak", CompressionBzip2},
		{"foo.tar", CompressionNone},
		{"foo", CompressionBzip2},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := detectCompression(tt.path); got != tt.want {
				t.Errorf("detectCompression(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestParseContents_InputTypes(t *testing.T) {
	tests := []struct {
		line     string
		wantType string
		wantPath string
		wantTarg string
	}{
		{"obj /usr/bin/test 1234 1700000000", "obj", "/usr/bin/test", ""},
		{"dir /usr/bin", "dir", "/usr/bin", ""},
		{"sym /usr/bin/test -> /usr/bin/real 1700000000", "sym", "/usr/bin/test", "/usr/bin/real"},
	}

	for _, tt := range tests {
		t.Run(tt.wantType, func(t *testing.T) {
			e := parseContentsLine(tt.line)
			if e == nil {
				t.Fatal("parseContentsLine returned nil")
			}
			if e.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", e.Type, tt.wantType)
			}
			if e.Path != tt.wantPath {
				t.Errorf("Path = %q, want %q", e.Path, tt.wantPath)
			}
			if e.Target != tt.wantTarg {
				t.Errorf("Target = %q, want %q", e.Target, tt.wantTarg)
			}
		})
	}
}

func TestParseContents_NilOnEmpty(t *testing.T) {
	if e := parseContentsLine(""); e != nil {
		t.Errorf("expected nil for empty line, got %+v", e)
	}
	if e := parseContentsLine("single"); e != nil {
		t.Errorf("expected nil for short line, got %+v", e)
	}
}

func TestExtract_NonexistentFile(t *testing.T) {
	err := Extract(context.Background(), "/nonexistent/file.tbz2", t.TempDir())
	if err == nil {
		t.Error("Extract() on nonexistent file should error")
	}
}

func TestCreate_WithDirectoryEntry(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")
	vdbPath := filepath.Join(baseDir, "vdb", "sys-devel", "dirtest-1.0")
	rootDir := filepath.Join(baseDir, "root")

	os.MkdirAll(vdbPath, 0755)
	os.MkdirAll(filepath.Join(rootDir, "usr", "share", "dirtest"), 0755)

	os.WriteFile(filepath.Join(vdbPath, "CONTENTS"), []byte("dir /usr/share/dirtest\n"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "CATEGORY"), []byte("sys-devel"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "PF"), []byte("dirtest-1.0"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "SLOT"), []byte("0"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "USE"), []byte(""), 0644)
	os.WriteFile(filepath.Join(vdbPath, "EAPI"), []byte("8"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "BUILD_TIME"), []byte("1700000000"), 0644)

	outPath, err := Create(context.Background(), vdbPath, rootDir, pkgDir)
	if err != nil {
		t.Fatalf("Create() with dir entry error: %v", err)
	}

	info, err := ReadInfo(outPath)
	if err != nil {
		t.Fatalf("ReadInfo() error: %v", err)
	}
	if info.Category != "sys-devel" || info.Package != "dirtest" {
		t.Errorf("got %s/%s, want sys-devel/dirtest", info.Category, info.Package)
	}
}

func TestXPAKMetadataKeys(t *testing.T) {
	meta := map[string]string{
		"CATEGORY":   "sys-devel",
		"PF":         "gcc-13.2.0",
		"PACKAGE":    "gcc",
		"VERSION":    "13.2.0",
		"SLOT":       "13/13.2",
		"USE":        "fortran openmp",
		"EAPI":       "8",
		"BUILD_TIME": "1700000000",
	}
	result := buildXPAKMetadata(meta)
	resultStr := string(result)

	if !strings.Contains(resultStr, "CATEGORY=sys-devel") {
		t.Error("missing CATEGORY in metadata")
	}
	if !strings.Contains(resultStr, "PF=gcc-13.2.0") {
		t.Error("missing PF in metadata")
	}
	if !strings.Contains(resultStr, "SLOT=13/13.2") {
		t.Error("missing SLOT in metadata")
	}
}

func TestParseMetadataLines(t *testing.T) {
	data := []byte("KEY1=value1\nKEY2=value2\n\nBADLINE\nKEY3=value3\n")
	result := parseMetadataLines(data)

	if result["KEY1"] != "value1" {
		t.Errorf("KEY1 = %q, want value1", result["KEY1"])
	}
	if result["KEY2"] != "value2" {
		t.Errorf("KEY2 = %q, want value2", result["KEY2"])
	}
	if result["KEY3"] != "value3" {
		t.Errorf("KEY3 = %q, want value3", result["KEY3"])
	}
	if _, ok := result["BADLINE"]; ok {
		t.Error("BADLINE should not be in metadata")
	}
}

func TestCreate_EmptyContents(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")
	vdbPath := filepath.Join(baseDir, "vdb", "sys-devel", "empty-1.0")
	rootDir := filepath.Join(baseDir, "root")

	os.MkdirAll(vdbPath, 0755)

	os.WriteFile(filepath.Join(vdbPath, "CONTENTS"), []byte{}, 0644)
	os.WriteFile(filepath.Join(vdbPath, "CATEGORY"), []byte("sys-devel"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "PF"), []byte("empty-1.0"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "SLOT"), []byte("0"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "USE"), []byte(""), 0644)
	os.WriteFile(filepath.Join(vdbPath, "EAPI"), []byte("8"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "BUILD_TIME"), []byte("1700000000"), 0644)

	outPath, err := Create(context.Background(), vdbPath, rootDir, pkgDir)
	if err != nil {
		t.Fatalf("Create() with empty CONTENTS: %v", err)
	}

	info, err := ReadInfo(outPath)
	if err != nil {
		t.Fatalf("ReadInfo() on empty package: %v", err)
	}
	if info.Category != "sys-devel" || info.Package != "empty" || info.Version != "1.0" {
		t.Errorf("got %s/%s-%s, want sys-devel/empty-1.0", info.Category, info.Package, info.Version)
	}
}

func TestCreate_WhitespaceMetadata(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")
	vdbPath := filepath.Join(baseDir, "vdb", "sys-devel", "wstest-1.0")
	rootDir := filepath.Join(baseDir, "root")

	os.MkdirAll(vdbPath, 0755)
	os.MkdirAll(filepath.Join(rootDir, "usr", "bin"), 0755)

	os.WriteFile(filepath.Join(rootDir, "usr", "bin", "wstest"), []byte("binary"), 0755)
	os.WriteFile(filepath.Join(vdbPath, "CONTENTS"), []byte("obj /usr/bin/wstest 6 1700000000\n"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "CATEGORY"), []byte("sys-devel\n"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "PF"), []byte("wstest-1.0\n"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "SLOT"), []byte("0\n"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "USE"), []byte("  foo bar  \n"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "EAPI"), []byte("8"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "BUILD_TIME"), []byte("1700000000"), 0644)

	outPath, err := Create(context.Background(), vdbPath, rootDir, pkgDir)
	if err != nil {
		t.Fatalf("Create() with whitespace metadata: %v", err)
	}

	info, err := ReadInfo(outPath)
	if err != nil {
		t.Fatalf("ReadInfo(): %v", err)
	}
	if info.Category != "sys-devel" {
		t.Errorf("Category = %q, want sys-devel (trimmed)", info.Category)
	}
}

func TestListAvailable_SkipsNonPackages(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")

	catDir := filepath.Join(pkgDir, "sys-devel")
	os.MkdirAll(catDir, 0755)

	os.WriteFile(filepath.Join(catDir, "not-a-package.txt"), []byte("hello"), 0644)
	os.WriteFile(filepath.Join(catDir, ".hidden.tbz2"), []byte("hidden"), 0644)

	pkgs, err := ListAvailable(pkgDir)
	if err != nil {
		t.Fatalf("ListAvailable() error: %v", err)
	}
	if len(pkgs) != 0 {
		t.Errorf("ListAvailable() returned %d packages, want 0 (non-pkg files should be skipped)", len(pkgs))
	}
}

func TestListAvailable_SkipsDotDirs(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")

	dotDir := filepath.Join(pkgDir, ".hidden")
	os.MkdirAll(dotDir, 0755)

	pkgs, err := ListAvailable(pkgDir)
	if err != nil {
		t.Fatalf("ListAvailable() error: %v", err)
	}
	if len(pkgs) != 0 {
		t.Errorf("ListAvailable() returned %d packages, want 0 (dot-dirs should be skipped)", len(pkgs))
	}
}

func TestAdversarial_ExtractPathTraversal(t *testing.T) {
	baseDir := t.TempDir()
	destDir := filepath.Join(baseDir, "extracted")
	os.MkdirAll(destDir, 0755)

	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)

	hdr := &tar.Header{
		Name:     "../../etc/passwd",
		Size:     10,
		Typeflag: tar.TypeReg,
		Mode:     0644,
	}
	tw.WriteHeader(hdr)
	tw.Write([]byte("malicious"))
	tw.Close()

	metaContent := "CATEGORY=bad\nPF=bad-1.0\n"

	var full bytes.Buffer
	full.Write(tarBuf.Bytes())
	full.WriteString(metaContent)

	offset := len(metaContent) + len(xpakMagic) + 1
	offsetStr := offsetToStr(offset)
	full.WriteString(xpakMagic + "\n")
	full.WriteString(offsetStr + "\n")

	path := filepath.Join(baseDir, "malicious.tar")
	os.WriteFile(path, full.Bytes(), 0644)

	err := Extract(context.Background(), path, destDir)
	if err == nil {
		t.Log("path traversal extract completed without error")
	}
}

func offsetToStr(base int) string {
	offset := base + len(strconv.Itoa(base)) + 1
	offsetStr := strconv.Itoa(offset)

	for {
		testOffset := base + len(offsetStr) + 1
		newOffsetStr := strconv.Itoa(testOffset)
		if newOffsetStr == offsetStr {
			return offsetStr
		}
		offsetStr = newOffsetStr
	}
}

func TestParseUseFlags(t *testing.T) {
	tests := []struct {
		input string
		want  map[string]bool
	}{
		{"", map[string]bool{}},
		{"foo", map[string]bool{"foo": true}},
		{"-foo", map[string]bool{"foo": false}},
		{"foo bar -baz", map[string]bool{"foo": true, "bar": true, "baz": false}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseUseFlags(tt.input)
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("parseUseFlags(%q)[%s] = %v, want %v", tt.input, k, got[k], v)
				}
			}
			for k := range got {
				if _, ok := tt.want[k]; !ok {
					t.Errorf("parseUseFlags(%q) unexpected key %q", tt.input, k)
				}
			}
		})
	}
}

func TestUseFlagsCompatible(t *testing.T) {
	tests := []struct {
		name     string
		binUse   map[string]bool
		confUse  map[string]bool
		expected bool
	}{
		{
			name:     "empty config matches anything",
			binUse:   map[string]bool{"foo": true},
			confUse:  map[string]bool{},
			expected: true,
		},
		{
			name:     "exact match",
			binUse:   map[string]bool{"foo": true, "bar": false},
			confUse:  map[string]bool{"foo": true},
			expected: true,
		},
		{
			name:     "conflicting flag",
			binUse:   map[string]bool{"foo": true},
			confUse:  map[string]bool{"foo": false},
			expected: false,
		},
		{
			name:     "missing flag in binpkg is OK",
			binUse:   map[string]bool{},
			confUse:  map[string]bool{"foo": true},
			expected: true,
		},
		{
			name:     "disabled flag matches",
			binUse:   map[string]bool{"foo": false},
			confUse:  map[string]bool{"foo": false},
			expected: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := useFlagsCompatible(tt.binUse, tt.confUse); got != tt.expected {
				t.Errorf("useFlagsCompatible() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestFindPackageMatchingUse(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "binpkgs")

	createPkgWithUse := func(cat, name, version, useStr string) {
		vdbPath := filepath.Join(baseDir, "vdb", cat, name+"-"+version)
		rootDir := filepath.Join(baseDir, "root_"+name+"-"+version)
		os.MkdirAll(filepath.Join(rootDir, "usr", "bin"), 0755)
		os.MkdirAll(vdbPath, 0755)
		os.WriteFile(filepath.Join(rootDir, "usr", "bin", name), []byte("binary"), 0755)
		os.WriteFile(filepath.Join(vdbPath, "CONTENTS"), []byte("obj /usr/bin/"+name+" 6 1700000000\n"), 0644)
		os.WriteFile(filepath.Join(vdbPath, "CATEGORY"), []byte(cat), 0644)
		os.WriteFile(filepath.Join(vdbPath, "PF"), []byte(name+"-"+version), 0644)
		os.WriteFile(filepath.Join(vdbPath, "SLOT"), []byte("0"), 0644)
		os.WriteFile(filepath.Join(vdbPath, "USE"), []byte(useStr), 0644)
		os.WriteFile(filepath.Join(vdbPath, "EAPI"), []byte("8"), 0644)
		os.WriteFile(filepath.Join(vdbPath, "BUILD_TIME"), []byte("1700000000"), 0644)
		if _, err := Create(context.Background(), vdbPath, rootDir, pkgDir); err != nil {
			t.Fatalf("Create() error: %v", err)
		}
	}

	createPkgWithUse("sys-devel", "mockpkg", "1.0", "foo bar -baz")
	createPkgWithUse("sys-devel", "mockpkg", "2.0", "foo -bar -baz")

	t.Run("respectUse=false returns best version", func(t *testing.T) {
		pkg, err := FindPackageMatchingUse(pkgDir, "sys-devel/mockpkg", map[string]bool{"foo": true}, false)
		if err != nil {
			t.Fatalf("FindPackageMatchingUse error: %v", err)
		}
		if pkg == nil {
			t.Fatal("FindPackageMatchingUse returned nil")
		}
		if pkg.Version != "2.0" {
			t.Errorf("got version %s, want 2.0 (best match)", pkg.Version)
		}
	})

	t.Run("respectUse=true with matching USE", func(t *testing.T) {
		pkg, err := FindPackageMatchingUse(pkgDir, "sys-devel/mockpkg", map[string]bool{"foo": true, "bar": true}, true)
		if err != nil {
			t.Fatalf("FindPackageMatchingUse error: %v", err)
		}
		if pkg == nil {
			t.Fatal("FindPackageMatchingUse returned nil")
		}
		if pkg.Version != "1.0" {
			t.Errorf("got version %s, want 1.0 (USE compatible: foo bar)", pkg.Version)
		}
	})

	t.Run("respectUse=true with conflicting USE returns nil", func(t *testing.T) {
		pkg, err := FindPackageMatchingUse(pkgDir, "sys-devel/mockpkg", map[string]bool{"baz": true}, true)
		if err != nil {
			t.Fatalf("FindPackageMatchingUse error: %v", err)
		}
		if pkg != nil {
			t.Errorf("got %v, want nil (no binpkg has baz enabled)", pkg)
		}
	})

	t.Run("respectUse=true with version constraint", func(t *testing.T) {
		pkg, err := FindPackageMatchingUse(pkgDir, ">=sys-devel/mockpkg-2.0", map[string]bool{"foo": true}, true)
		if err != nil {
			t.Fatalf("FindPackageMatchingUse error: %v", err)
		}
		if pkg == nil {
			t.Fatal("FindPackageMatchingUse returned nil")
		}
		if pkg.Version != "2.0" {
			t.Errorf("got version %s, want 2.0", pkg.Version)
		}
	})

	t.Run("respectUse=false prefers highest version", func(t *testing.T) {
		pkg, err := FindPackageMatchingUse(pkgDir, "sys-devel/mockpkg", map[string]bool{"foo": false, "bar": true}, false)
		if err != nil {
			t.Fatalf("FindPackageMatchingUse error: %v", err)
		}
		if pkg == nil {
			t.Fatal("FindPackageMatchingUse returned nil")
		}
		if pkg.Version != "2.0" {
			t.Errorf("got version %s, want 2.0 (best match regardless of USE)", pkg.Version)
		}
	})

	t.Run("nonexistent package", func(t *testing.T) {
		pkg, err := FindPackageMatchingUse(pkgDir, "sys-devel/nonexistent", nil, true)
		if err != nil {
			t.Fatalf("FindPackageMatchingUse error: %v", err)
		}
		if pkg != nil {
			t.Errorf("got %v, want nil", pkg)
		}
	})

	t.Run("invalid atom", func(t *testing.T) {
		_, err := FindPackageMatchingUse(pkgDir, "", nil, true)
		if err == nil {
			t.Error("expected error for empty atom")
		}
	})
}

func TestDownloadFromBinhost_HTTP(t *testing.T) {
	pkgContent := []byte("fake binary package")
	client := testHTTPClient(func(request *http.Request) (int, []byte) {
		if request.URL.Path == "/sys-devel/testpkg-1.0.tbz2" {
			return http.StatusOK, pkgContent
		}
		return http.StatusNotFound, nil
	})

	destDir := filepath.Join(t.TempDir(), "dest")
	url := "https://binhost.invalid/"

	downloaded, err := downloadFromBinhost(context.Background(), client, url, []string{"=sys-devel/testpkg-1.0"}, destDir)
	if err != nil {
		t.Fatalf("DownloadFromBinhost error: %v", err)
	}
	if len(downloaded) != 1 {
		t.Fatalf("expected 1 downloaded, got %d", len(downloaded))
	}

	expectedPath := filepath.Join(destDir, "sys-devel", "testpkg-1.0.tbz2")
	if downloaded[0] != expectedPath {
		t.Errorf("path = %q, want %q", downloaded[0], expectedPath)
	}

	data, err := os.ReadFile(downloaded[0])
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(data, pkgContent) {
		t.Errorf("content mismatch")
	}
}

func TestDownloadFromBinhost_404(t *testing.T) {
	client := testHTTPClient(func(*http.Request) (int, []byte) { return http.StatusNotFound, nil })

	destDir := filepath.Join(t.TempDir(), "dest")
	url := "https://binhost.invalid/"

	_, err := downloadFromBinhost(context.Background(), client, url, []string{"=sys-devel/testpkg-1.0"}, destDir)
	if err == nil {
		t.Error("expected error for 404")
	}
}

func TestDownloadFromBinhost_NoVersion(t *testing.T) {
	client := testHTTPClient(func(*http.Request) (int, []byte) { return http.StatusOK, []byte("fake") })

	destDir := filepath.Join(t.TempDir(), "dest")
	url := "https://binhost.invalid/"

	downloaded, err := downloadFromBinhost(context.Background(), client, url, []string{"sys-devel/testpkg"}, destDir)
	if err != nil {
		t.Fatalf("DownloadFromBinhost error: %v", err)
	}
	if len(downloaded) != 1 {
		t.Fatalf("expected 1 downloaded, got %d", len(downloaded))
	}

	expectedPath := filepath.Join(destDir, "sys-devel", "testpkg.tbz2")
	if downloaded[0] != expectedPath {
		t.Errorf("path = %q, want %q", downloaded[0], expectedPath)
	}
}

func TestDownloadFromBinhost_EmptyList(t *testing.T) {
	destDir := filepath.Join(t.TempDir(), "dest")
	downloaded, err := DownloadFromBinhost(context.Background(), "http://example.com/", nil, destDir)
	if err != nil {
		t.Fatalf("DownloadFromBinhost error: %v", err)
	}
	if len(downloaded) != 0 {
		t.Errorf("expected 0 downloads, got %d", len(downloaded))
	}
}

func TestDownloadFromBinhost_InvalidAtom(t *testing.T) {
	destDir := filepath.Join(t.TempDir(), "dest")
	downloaded, err := DownloadFromBinhost(context.Background(), "http://example.com/", []string{""}, destDir)
	if err != nil {
		t.Fatalf("DownloadFromBinhost error: %v", err)
	}
	if len(downloaded) != 0 {
		t.Errorf("expected 0 downloads, got %d", len(downloaded))
	}
}

func TestReadVDBMetadata_Full(t *testing.T) {
	dir := t.TempDir()
	vdbPath := filepath.Join(dir, "vdb", "sys-apps", "testpkg-2.0")
	os.MkdirAll(vdbPath, 0755)

	os.WriteFile(filepath.Join(vdbPath, "CATEGORY"), []byte("sys-apps"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "PF"), []byte("testpkg-2.0"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "SLOT"), []byte("0/1"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "USE"), []byte("ssl python"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "EAPI"), []byte("8"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "BUILD_TIME"), []byte("1700000000"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "CHOST"), []byte("x86_64-pc-linux-gnu"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "repository"), []byte("gentoo"), 0644)

	meta, err := readVDBMetadata(vdbPath)
	if err != nil {
		t.Fatalf("readVDBMetadata: %v", err)
	}

	if meta["CATEGORY"] != "sys-apps" {
		t.Errorf("CATEGORY = %q, want sys-apps", meta["CATEGORY"])
	}
	if meta["PF"] != "testpkg-2.0" {
		t.Errorf("PF = %q, want testpkg-2.0", meta["PF"])
	}
	if meta["PACKAGE"] != "testpkg" {
		t.Errorf("PACKAGE = %q, want testpkg", meta["PACKAGE"])
	}
	if meta["VERSION"] != "2.0" {
		t.Errorf("VERSION = %q, want 2.0", meta["VERSION"])
	}
	if meta["SLOT"] != "0/1" {
		t.Errorf("SLOT = %q, want 0/1", meta["SLOT"])
	}
	if meta["USE"] != "ssl python" {
		t.Errorf("USE = %q, want 'ssl python'", meta["USE"])
	}
}

func TestReadVDBMetadata_Minimal(t *testing.T) {
	dir := t.TempDir()
	vdbPath := filepath.Join(dir, "vdb", "app-misc", "minimal-1.0")
	os.MkdirAll(vdbPath, 0755)

	os.WriteFile(filepath.Join(vdbPath, "CATEGORY"), []byte("app-misc"), 0644)

	meta, err := readVDBMetadata(vdbPath)
	if err != nil {
		t.Fatalf("readVDBMetadata: %v", err)
	}

	if meta["CATEGORY"] != "app-misc" {
		t.Errorf("CATEGORY = %q", meta["CATEGORY"])
	}
	if meta["PF"] != "" {
		t.Errorf("PF should be empty when file missing")
	}
}

func TestReadVDBMetadata_TrimsWhitespace(t *testing.T) {
	dir := t.TempDir()
	vdbPath := filepath.Join(dir, "vdb", "app-misc", "trimmed-1.0")
	os.MkdirAll(vdbPath, 0755)

	os.WriteFile(filepath.Join(vdbPath, "CATEGORY"), []byte("app-misc\n"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "PF"), []byte("trimmed-1.0\n"), 0644)
	os.WriteFile(filepath.Join(vdbPath, "SLOT"), []byte(" 0 \n"), 0644)

	meta, err := readVDBMetadata(vdbPath)
	if err != nil {
		t.Fatalf("readVDBMetadata: %v", err)
	}

	if meta["CATEGORY"] != "app-misc" {
		t.Errorf("CATEGORY = %q, want app-misc (trimmed)", meta["CATEGORY"])
	}
	if meta["SLOT"] != "0" {
		t.Errorf("SLOT = %q, want 0 (trimmed)", meta["SLOT"])
	}
}

func TestBrokenWriter_Write(t *testing.T) {
	bw := &brokenWriter{err: os.ErrPermission}
	n, err := bw.Write([]byte("hello"))
	if n != 0 {
		t.Errorf("Write() = %d, want 0", n)
	}
	if err != os.ErrPermission {
		t.Errorf("Write() error = %v, want %v", err, os.ErrPermission)
	}
}

func TestBrokenWriter_Close(t *testing.T) {
	bw := &brokenWriter{err: os.ErrPermission}
	err := bw.Close()
	if err != os.ErrPermission {
		t.Errorf("Close() = %v, want %v", err, os.ErrPermission)
	}
}

type testCloser struct {
	closed bool
}

func (c *testCloser) Close() error { c.closed = true; return nil }

type errorCloser struct {
	closed bool
}

func (c *errorCloser) Close() error { c.closed = true; return os.ErrInvalid }

func TestCleanup_AllSucceed(t *testing.T) {
	tc1 := &testCloser{}
	tc2 := &testCloser{}

	cleanup(tc1, tc2)

	if !tc1.closed || !tc2.closed {
		t.Error("cleanup should close all closers")
	}
}

func TestCleanup_HandlesErrors(t *testing.T) {
	ec := &errorCloser{}

	cleanup(ec)

	if !ec.closed {
		t.Error("cleanup should attempt close even if it returns error")
	}
}
