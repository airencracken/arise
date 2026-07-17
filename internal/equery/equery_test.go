package equery

import (
	"bytes"
	"crypto/md5"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/dgraph-io/badger/v4"
)

func setupVDB(t *testing.T, base string, packages map[string]map[string]string) {
	t.Helper()
	for pv, files := range packages {
		dir := filepath.Join(base, pv)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func makeFlatVDB(t *testing.T, base string, entries map[string]map[string]string) {
	t.Helper()
	for pv, files := range entries {
		parts := strings.Split(pv, "/")
		if len(parts) != 2 {
			t.Fatalf("invalid pv key %q, expected category/package-version", pv)
		}
		category := parts[0]
		catDir := filepath.Join(base, category)
		if err := os.MkdirAll(catDir, 0755); err != nil {
			t.Fatal(err)
		}
		pvDir := filepath.Join(catDir, parts[1])
		if err := os.MkdirAll(pvDir, 0755); err != nil {
			t.Fatal(err)
		}
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(pvDir, name), []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func createTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func createSymlink(t *testing.T, path, target string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func md5sum(data []byte) string {
	s := md5.Sum(data)
	return fmt.Sprintf("%x", s[:])
}

func TestBelongs_FindsOwningPackage(t *testing.T) {
	vdbDir := t.TempDir()
	contents := "obj /usr/bin/foo " + md5sum([]byte("foo")) + " 1680000000\n" +
		"obj /usr/lib/libfoo.so " + md5sum([]byte("libfoo")) + " 1680000001\n" +
		"dir /usr/share/foo\n"
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/myapp-1.0": {"CONTENTS": contents},
	})

	pkg, err := Belongs(vdbDir, "/usr/bin/foo")
	if err != nil {
		t.Fatal(err)
	}
	if pkg != "app-misc/myapp-1.0" {
		t.Errorf("expected app-misc/myapp-1.0, got %q", pkg)
	}
}

func TestBelongs_FilenameOnlyMatching(t *testing.T) {
	vdbDir := t.TempDir()
	contents := "obj /usr/bin/myapp " + md5sum([]byte("myapp")) + " 1680000000\n"
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"sys-apps/example-2.0": {"CONTENTS": contents},
	})

	pkg, err := Belongs(vdbDir, "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if pkg != "sys-apps/example-2.0" {
		t.Errorf("expected sys-apps/example-2.0, got %q", pkg)
	}
}

func TestBelongs_FileNotOwnedByAnyPackage(t *testing.T) {
	vdbDir := t.TempDir()
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/foo-1.0": {"CONTENTS": "obj /usr/bin/foo abc123 1680000000\n"},
	})

	_, err := Belongs(vdbDir, "/nonexistent/file")
	if err == nil {
		t.Fatal("expected error for unowned file")
	}
}

func TestBelongs_EmptyVDB(t *testing.T) {
	vdbDir := t.TempDir()

	_, err := Belongs(vdbDir, "/usr/bin/foo")
	if err == nil {
		t.Fatal("expected error for empty VDB")
	}
}

func TestBelongs_MultiplePackagesSameFile(t *testing.T) {
	vdbDir := t.TempDir()
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/first-1.0":  {"CONTENTS": "obj /usr/bin/shared " + md5sum([]byte("a")) + " 1680000000\n"},
		"app-misc/second-1.0": {"CONTENTS": "obj /usr/bin/shared " + md5sum([]byte("b")) + " 1680000001\n"},
	})

	pkg, err := Belongs(vdbDir, "/usr/bin/shared")
	if err != nil {
		t.Fatal(err)
	}
	if pkg != "app-misc/first-1.0" && pkg != "app-misc/second-1.0" {
		t.Errorf("expected one of the packages, got %q", pkg)
	}
}

func TestFiles_ListAllFiles(t *testing.T) {
	vdbDir := t.TempDir()
	contents := "obj /usr/bin/app " + md5sum([]byte("app")) + " 1680000000\n" +
		"obj /etc/app.conf " + md5sum([]byte("conf")) + " 1680000001\n" +
		"dir /usr/share/app\n" +
		"sym /usr/lib/libapp.so -> libapp.so.1 1680000002\n"
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/myapp-1.0": {"CONTENTS": contents},
	})

	files, err := Files(vdbDir, "app-misc/myapp-1.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Errorf("expected 2 obj files, got %d: %v", len(files), files)
	}
	if files[0] != "/usr/bin/app" {
		t.Errorf("expected /usr/bin/app, got %q", files[0])
	}
	if files[1] != "/etc/app.conf" {
		t.Errorf("expected /etc/app.conf, got %q", files[1])
	}
}

func TestFiles_UnversionedAtomFindsBestInstalled(t *testing.T) {
	vdbDir := t.TempDir()
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/myapp-1.0": {"CONTENTS": "obj /usr/bin/myapp-v1 abc1 100\n"},
		"app-misc/myapp-2.0": {"CONTENTS": "obj /usr/bin/myapp-v2 abc2 200\n"},
		"app-misc/myapp-1.5": {"CONTENTS": "obj /usr/bin/myapp-v15 abc3 300\n"},
	})

	files, err := Files(vdbDir, "app-misc/myapp")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0] != "/usr/bin/myapp-v2" {
		t.Errorf("expected /usr/bin/myapp-v2 (best version 2.0), got %q", files[0])
	}
}

func TestFiles_UnknownPackage(t *testing.T) {
	vdbDir := t.TempDir()
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/foo-1.0": {"CONTENTS": "obj /usr/bin/foo abc 100\n"},
	})

	_, err := Files(vdbDir, "app-misc/bar")
	if err == nil {
		t.Fatal("expected error for unknown package")
	}
}

func TestFiles_WithOperatorPrefix(t *testing.T) {
	vdbDir := t.TempDir()
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/myapp-1.0": {"CONTENTS": "obj /usr/bin/app abc1 100\n"},
	})

	files, err := Files(vdbDir, "=app-misc/myapp-1.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0] != "/usr/bin/app" {
		t.Errorf("expected /usr/bin/app, got %q", files[0])
	}
}

func TestUses_ReturnsIuseAndActiveUse(t *testing.T) {
	vdbDir := t.TempDir()
	useContent := "X gtk python ssl -qt5"
	iuseContent := "X gtk python ssl qt5"

	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"x11-misc/mypkg-1.0": {
			"USE":  useContent,
			"IUSE": iuseContent,
		},
	})

	iuse, active, err := Uses(nil, vdbDir, "x11-misc/mypkg-1.0")
	if err != nil {
		t.Fatal(err)
	}
	if iuse != iuseContent {
		t.Errorf("expected iuse %q, got %q", iuseContent, iuse)
	}
	if active != useContent {
		t.Errorf("expected active use %q, got %q", active, useContent)
	}
}

func TestUses_PackageWithNoUseFlags(t *testing.T) {
	vdbDir := t.TempDir()
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/nouse-1.0": {},
	})

	iuse, active, err := Uses(nil, vdbDir, "app-misc/nouse-1.0")
	if err != nil {
		t.Fatal(err)
	}
	if iuse != "" {
		t.Errorf("expected empty iuse, got %q", iuse)
	}
	if active != "" {
		t.Errorf("expected empty active use, got %q", active)
	}
}

func TestUses_FromBadgerDB(t *testing.T) {
	vdbDir := t.TempDir()
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/withdb-1.0": {
			"USE":  "ssl python",
			"IUSE": "",
		},
	})

	db, err := badger.Open(badger.DefaultOptions(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	m := &metadata.PackageMetadata{
		Category:    "app-misc",
		Package:     "withdb",
		IUSE:        "X ssl python qt5",
		DESCRIPTION: "A test package",
	}
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(m); err != nil {
		t.Fatal(err)
	}

	err = db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte("pkg:"+m.RepositoryCPVKey()), buf.Bytes())
	})
	if err != nil {
		t.Fatal(err)
	}

	iuse, active, err := Uses(db, vdbDir, "app-misc/withdb-1.0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(iuse, "ssl") {
		t.Errorf("expected iuse from db to contain 'ssl', got %q", iuse)
	}
	if active != "ssl python" {
		t.Errorf("expected active use 'ssl python', got %q", active)
	}
}

func TestUses_UnversionedFindsBest(t *testing.T) {
	vdbDir := t.TempDir()
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/myapp-1.0": {"USE": "old", "IUSE": "old"},
		"app-misc/myapp-3.0": {"USE": "new", "IUSE": "new"},
		"app-misc/myapp-2.0": {"USE": "mid", "IUSE": "mid"},
	})

	iuse, active, err := Uses(nil, vdbDir, "app-misc/myapp")
	if err != nil {
		t.Fatal(err)
	}
	if iuse != "new" {
		t.Errorf("expected iuse 'new' from best version, got %q", iuse)
	}
	if active != "new" {
		t.Errorf("expected active use 'new' from best version, got %q", active)
	}
}

func TestSize_SumsFileSizes(t *testing.T) {
	vdbDir := t.TempDir()
	tmpDir := t.TempDir()

	f1 := filepath.Join(tmpDir, "file1")
	f2 := filepath.Join(tmpDir, "file2")
	createTestFile(t, f1, strings.Repeat("x", 100))
	createTestFile(t, f2, strings.Repeat("y", 250))

	f1md5 := md5sum([]byte(strings.Repeat("x", 100)))
	f2md5 := md5sum([]byte(strings.Repeat("y", 250)))

	contents := fmt.Sprintf("obj %s %s 1680000000\nobj %s %s 1680000001\n", f1, f1md5, f2, f2md5)
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/sizer-1.0": {"CONTENTS": contents},
	})

	size, err := Size(vdbDir, "app-misc/sizer-1.0")
	if err != nil {
		t.Fatal(err)
	}
	if size != 350 {
		t.Errorf("expected size 350, got %d", size)
	}
}

func TestSize_HandlesSymlinksAndDirs(t *testing.T) {
	vdbDir := t.TempDir()
	tmpDir := t.TempDir()

	regFile := filepath.Join(tmpDir, "regular")
	createTestFile(t, regFile, "hello")
	linkFile := filepath.Join(tmpDir, "link")
	createSymlink(t, linkFile, regFile)
	dirPath := filepath.Join(tmpDir, "adir")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatal(err)
	}

	contents := fmt.Sprintf("obj %s %s 1680000000\nsym %s -> %s 1680000001\n", regFile, md5sum([]byte("hello")), linkFile, regFile)
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/symtest-1.0": {"CONTENTS": contents},
	})

	size, err := Size(vdbDir, "app-misc/symtest-1.0")
	if err != nil {
		t.Fatal(err)
	}
	if size != 5 {
		t.Errorf("expected size 5 (only regular file), got %d", size)
	}
}

func TestCheck_DetectsCorruptedFile(t *testing.T) {
	vdbDir := t.TempDir()
	tmpDir := t.TempDir()

	f := filepath.Join(tmpDir, "testfile")
	createTestFile(t, f, "original content")

	contents := fmt.Sprintf("obj %s %s 0\n", f, md5sum([]byte("modified content")))
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/corrupt-1.0": {"CONTENTS": contents},
	})

	mismatches, err := Check(vdbDir, "app-misc/corrupt-1.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(mismatches) != 1 {
		t.Fatalf("expected 1 mismatch, got %d: %v", len(mismatches), mismatches)
	}
	if !strings.Contains(mismatches[0], "checksum mismatch") {
		t.Errorf("expected checksum mismatch, got %q", mismatches[0])
	}
}

func TestCheck_AllFilesMatch(t *testing.T) {
	vdbDir := t.TempDir()
	tmpDir := t.TempDir()

	content := "hello world"
	f := filepath.Join(tmpDir, "goodfile")
	createTestFile(t, f, content)

	contents := fmt.Sprintf("obj %s %s 0\n", f, md5sum([]byte(content)))
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/good-1.0": {"CONTENTS": contents},
	})

	mismatches, err := Check(vdbDir, "app-misc/good-1.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(mismatches) != 0 {
		t.Errorf("expected 0 mismatches, got %v", mismatches)
	}
}

func TestCheck_MissingFile(t *testing.T) {
	vdbDir := t.TempDir()
	contents := "obj /nonexistent/file " + md5sum([]byte("x")) + " 1680000000\n"
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/missing-1.0": {"CONTENTS": contents},
	})

	mismatches, err := Check(vdbDir, "app-misc/missing-1.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(mismatches) != 1 {
		t.Fatalf("expected 1 mismatch, got %d", len(mismatches))
	}
	if !strings.Contains(mismatches[0], "missing") {
		t.Errorf("expected 'missing' in mismatch message, got %q", mismatches[0])
	}
}

func TestCheck_MtimeMismatch(t *testing.T) {
	vdbDir := t.TempDir()
	tmpDir := t.TempDir()

	f := filepath.Join(tmpDir, "mtimefile")
	createTestFile(t, f, "content")

	contents := fmt.Sprintf("obj %s %s 9999999999\n", f, md5sum([]byte("content")))
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/mtimetest-1.0": {"CONTENTS": contents},
	})

	mismatches, err := Check(vdbDir, "app-misc/mtimetest-1.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(mismatches) != 1 {
		t.Fatalf("expected 1 mismatch, got %d", len(mismatches))
	}
	if !strings.Contains(mismatches[0], "mtime mismatch") {
		t.Errorf("expected mtime mismatch, got %q", mismatches[0])
	}
}

func TestCheck_SymlinksAreSkipped(t *testing.T) {
	vdbDir := t.TempDir()
	tmpDir := t.TempDir()

	target := filepath.Join(tmpDir, "target")
	createTestFile(t, target, "target content")
	link := filepath.Join(tmpDir, "link")
	createSymlink(t, link, target)

	contents := fmt.Sprintf("sym %s -> %s 1680000000\n", link, target)
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/symcheck-1.0": {"CONTENTS": contents},
	})

	mismatches, err := Check(vdbDir, "app-misc/symcheck-1.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(mismatches) != 0 {
		t.Errorf("symlinks should be skipped, got mismatches: %v", mismatches)
	}
}

func TestWhich_FindsEbuildPath(t *testing.T) {
	repoDir := t.TempDir()
	pkgDir := filepath.Join(repoDir, "app-misc", "myapp")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	ebuildPath := filepath.Join(pkgDir, "myapp-1.0.ebuild")
	createTestFile(t, ebuildPath, "EAPI=8\n")

	path, err := Which(repoDir, "app-misc/myapp-1.0")
	if err != nil {
		t.Fatal(err)
	}
	if path != ebuildPath {
		t.Errorf("expected %q, got %q", ebuildPath, path)
	}
}

func TestWhich_FindsBestVersionWithoutVersion(t *testing.T) {
	repoDir := t.TempDir()
	pkgDir := filepath.Join(repoDir, "app-misc", "myapp")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	createTestFile(t, filepath.Join(pkgDir, "myapp-1.0.ebuild"), "EAPI=7\n")
	createTestFile(t, filepath.Join(pkgDir, "myapp-2.0.ebuild"), "EAPI=8\n")
	createTestFile(t, filepath.Join(pkgDir, "myapp-1.5.ebuild"), "EAPI=7\n")

	path, err := Which(repoDir, "app-misc/myapp")
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(pkgDir, "myapp-2.0.ebuild")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestWhich_UnknownPackage(t *testing.T) {
	repoDir := t.TempDir()

	_, err := Which(repoDir, "app-misc/nope")
	if err == nil {
		t.Fatal("expected error for unknown package")
	}
}

func TestWhich_UnknownVersion(t *testing.T) {
	repoDir := t.TempDir()
	pkgDir := filepath.Join(repoDir, "app-misc", "myapp")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	createTestFile(t, filepath.Join(pkgDir, "myapp-1.0.ebuild"), "EAPI=7\n")

	_, err := Which(repoDir, "app-misc/myapp-9.9")
	if err == nil {
		t.Fatal("expected error for unknown version")
	}
}

func TestList_ListsAllInstalled(t *testing.T) {
	vdbDir := t.TempDir()
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/foo-1.0": {"CONTENTS": "obj /usr/bin/foo abc 100\n"},
		"app-misc/bar-2.0": {"CONTENTS": "obj /usr/bin/bar def 200\n"},
		"sys-apps/baz-3.0": {"CONTENTS": "obj /usr/bin/baz ghi 300\n"},
	})

	packages, err := List(vdbDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 3 {
		t.Errorf("expected 3 packages, got %d: %v", len(packages), packages)
	}

	sort.Strings(packages)
	if packages[0] != "app-misc/bar-2.0" {
		t.Errorf("expected app-misc/bar-2.0, got %q", packages[0])
	}
	if packages[1] != "app-misc/foo-1.0" {
		t.Errorf("expected app-misc/foo-1.0, got %q", packages[1])
	}
	if packages[2] != "sys-apps/baz-3.0" {
		t.Errorf("expected sys-apps/baz-3.0, got %q", packages[2])
	}
}

func TestList_PatternMatch(t *testing.T) {
	vdbDir := t.TempDir()
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/foo-1.0": {"CONTENTS": "obj /usr/bin/foo abc 100\n"},
		"app-misc/bar-2.0": {"CONTENTS": "obj /usr/bin/bar def 200\n"},
		"sys-apps/baz-3.0": {"CONTENTS": "obj /usr/bin/baz ghi 300\n"},
	})

	packages, err := List(vdbDir, "foo")
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 {
		t.Fatalf("expected 1 package, got %d", len(packages))
	}
	if packages[0] != "app-misc/foo-1.0" {
		t.Errorf("expected app-misc/foo-1.0, got %q", packages[0])
	}

	packages, err = List(vdbDir, "app-misc")
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 2 {
		t.Fatalf("expected 2 packages matching 'app-misc', got %d: %v", len(packages), packages)
	}
}

func TestList_EmptyVDB(t *testing.T) {
	vdbDir := t.TempDir()

	packages, err := List(vdbDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 0 {
		t.Errorf("expected 0 packages, got %d", len(packages))
	}
}

func TestList_NestedVDBLayout(t *testing.T) {
	vdbDir := t.TempDir()

	catPkgDir := filepath.Join(vdbDir, "app-misc", "myapp")
	if err := os.MkdirAll(catPkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"myapp-1.0", "myapp-2.0"} {
		vDir := filepath.Join(catPkgDir, v)
		if err := os.MkdirAll(vDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(vDir, "CONTENTS"), []byte("obj /usr/bin/foo abc 100\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	flatCatDir := filepath.Join(vdbDir, "sys-apps")
	if err := os.MkdirAll(flatCatDir, 0755); err != nil {
		t.Fatal(err)
	}
	flatPkgDir := filepath.Join(flatCatDir, "baz-1.0")
	if err := os.MkdirAll(flatPkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(flatPkgDir, "CONTENTS"), []byte("obj /usr/bin/baz def 200\n"), 0644); err != nil {
		t.Fatal(err)
	}

	packages, err := List(vdbDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 3 {
		t.Errorf("expected 3 packages (2 nested + 1 flat), got %d: %v", len(packages), packages)
	}
}

func TestVerComparesVersionsCorrectly(t *testing.T) {
	tests := []struct {
		a, b string
		less bool
	}{
		{"1.0", "2.0", true},
		{"2.0", "1.0", false},
		{"1.0", "1.0", false},
		{"1.0-r1", "1.0-r2", true},
		{"1.0_p1", "1.0_p2", true},
		{"1.0_alpha", "1.0_beta", true},
		{"1.0_rc1", "1.0", true},
		{"1.0", "1.0a", true},
	}

	for _, tt := range tests {
		va, _ := atom.ParseVersion(tt.a)
		vb, _ := atom.ParseVersion(tt.b)
		got := va.Compare(vb)
		if tt.less && got >= 0 {
			t.Errorf("%s.Compare(%s) = %d, expected < 0", tt.a, tt.b, got)
		}
		if !tt.less && got < 0 {
			t.Errorf("%s.Compare(%s) = %d, expected >= 0", tt.a, tt.b, got)
		}
	}
}

func TestParseContentsLine(t *testing.T) {
	entry, ok := parseContentsLine("obj /usr/bin/test abc123 1680000000")
	if !ok {
		t.Fatal("expected ok")
	}
	if entry.typ != "obj" {
		t.Errorf("expected typ 'obj', got %q", entry.typ)
	}
	if entry.path != "/usr/bin/test" {
		t.Errorf("expected path '/usr/bin/test', got %q", entry.path)
	}
	if entry.md5 != "abc123" {
		t.Errorf("expected md5 'abc123', got %q", entry.md5)
	}
	if entry.mtime != 1680000000 {
		t.Errorf("expected mtime 1680000000, got %d", entry.mtime)
	}
}

func TestParseContentsLine_Dir(t *testing.T) {
	entry, ok := parseContentsLine("dir /usr/share/foo")
	if !ok {
		t.Fatal("expected ok")
	}
	if entry.typ != "dir" {
		t.Errorf("expected typ 'dir', got %q", entry.typ)
	}
	if entry.path != "/usr/share/foo" {
		t.Errorf("expected path '/usr/share/foo', got %q", entry.path)
	}
}

func TestParseContentsLine_Empty(t *testing.T) {
	_, ok := parseContentsLine("")
	if ok {
		t.Error("expected false for empty line")
	}
	_, ok = parseContentsLine("   ")
	if ok {
		t.Error("expected false for whitespace line")
	}
}

func TestFindBestInstalledVersion(t *testing.T) {
	vdbDir := t.TempDir()
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/myapp-1.0": {},
		"app-misc/myapp-2.0": {},
		"app-misc/myapp-1.5": {},
	})

	best, err := findBestInstalledVersion(vdbDir, "app-misc", "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if best != "myapp-2.0" {
		t.Errorf("expected myapp-2.0, got %q", best)
	}
}

func TestFindBestInstalledVersion_NoCandidates(t *testing.T) {
	vdbDir := t.TempDir()

	_, err := findBestInstalledVersion(vdbDir, "app-misc", "nope")
	if err == nil {
		t.Fatal("expected error for no candidates")
	}
}

func TestList_SortedOutput(t *testing.T) {
	vdbDir := t.TempDir()
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"sys-apps/z-1.0": {"CONTENTS": "obj /usr/bin/z abc 100\n"},
		"app-misc/a-1.0": {"CONTENTS": "obj /usr/bin/a def 200\n"},
		"app-misc/m-1.0": {"CONTENTS": "obj /usr/bin/m ghi 300\n"},
	})

	packages, err := List(vdbDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !sort.StringsAreSorted(packages) {
		t.Errorf("results not sorted: %v", packages)
	}
	if packages[0] != "app-misc/a-1.0" {
		t.Errorf("expected app-misc/a-1.0 first, got %q", packages[0])
	}
}

func TestSize_MissingFilesIgnored(t *testing.T) {
	vdbDir := t.TempDir()
	contents := "obj /nonexistent/file " + md5sum([]byte("x")) + " 1680000000\n" +
		"obj /nonexistent/file2 " + md5sum([]byte("y")) + " 1680000001\n"
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/missing-1.0": {"CONTENTS": contents},
	})

	size, err := Size(vdbDir, "app-misc/missing-1.0")
	if err != nil {
		t.Fatal(err)
	}
	if size != 0 {
		t.Errorf("expected 0 for missing files, got %d", size)
	}
}

func TestBelongs_SymlinkEntry(t *testing.T) {
	vdbDir := t.TempDir()
	contents := "sym /usr/lib/libfoo.so -> libfoo.so.1 1680000000\n"
	makeFlatVDB(t, vdbDir, map[string]map[string]string{
		"app-misc/symowner-1.0": {"CONTENTS": contents},
	})

	pkg, err := Belongs(vdbDir, "/usr/lib/libfoo.so")
	if err != nil {
		t.Fatal(err)
	}
	if pkg != "app-misc/symowner-1.0" {
		t.Errorf("expected app-misc/symowner-1.0, got %q", pkg)
	}
}
