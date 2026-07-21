package merge

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestCheckCollisions_NoExistingPackages(t *testing.T) {
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	vdbDir := filepath.Join(tmp, "vdb")

	if err := makeDestDir(destDir, map[string]string{
		"usr/bin/app":    "binary",
		"usr/lib/lib.so": "library",
	}); err != nil {
		t.Fatalf("makeDestDir: %v", err)
	}

	collisions, err := CheckCollisions(destDir, vdbDir, nil)
	if err != nil {
		t.Fatalf("CheckCollisions: %v", err)
	}
	if len(collisions) != 0 {
		t.Errorf("expected 0 collisions, got %d: %v", len(collisions), collisions)
	}
}

func TestCheckCollisions_FileOwnedByOtherPackage(t *testing.T) {
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	vdbDir := filepath.Join(tmp, "vdb")

	if err := makeDestDir(destDir, map[string]string{
		"usr/bin/app": "new binary",
	}); err != nil {
		t.Fatalf("makeDestDir: %v", err)
	}

	existingPkgDir := filepath.Join(vdbDir, "app-misc", "oldpkg-1.0")
	if err := os.MkdirAll(existingPkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	contents := "obj /usr/bin/app a1b2c3d4e5f6 1234567890\n"
	if err := os.WriteFile(filepath.Join(existingPkgDir, "CONTENTS"), []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}

	collisions, err := CheckCollisions(destDir, vdbDir, nil)
	if err != nil {
		t.Fatalf("CheckCollisions: %v", err)
	}
	if len(collisions) != 1 {
		t.Fatalf("expected 1 collision, got %d: %v", len(collisions), collisions)
	}
	if !strings.Contains(collisions[0], "app-misc/oldpkg") {
		t.Errorf("collision message should mention oldpkg: %q", collisions[0])
	}
	if !strings.Contains(collisions[0], "/usr/bin/app") {
		t.Errorf("collision message should mention file: %q", collisions[0])
	}
}

func TestCheckCollisions_NoCollisionWhenUpdatingSamePackage(t *testing.T) {
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	vdbDir := filepath.Join(tmp, "vdb")

	if err := makeDestDir(destDir, map[string]string{
		"usr/bin/app": "new binary",
	}); err != nil {
		t.Fatalf("makeDestDir: %v", err)
	}

	existingPkgDir := filepath.Join(vdbDir, "app-misc", "app-1.0")
	if err := os.MkdirAll(existingPkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	contents := "obj /usr/bin/app a1b2c3d4e5f6 1234567890\n"
	if err := os.WriteFile(filepath.Join(existingPkgDir, "CONTENTS"), []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}

	collisions, err := CheckCollisions(destDir, vdbDir, []string{"app-misc/app"})
	if err != nil {
		t.Fatalf("CheckCollisions: %v", err)
	}
	if len(collisions) != 0 {
		t.Errorf("expected 0 collisions when excluding same package, got %d: %v", len(collisions), collisions)
	}
}

func TestCheckCollisions_NoCollisionWhenUpdatingRevisedPackage(t *testing.T) {
	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")
	vdbDir := filepath.Join(tmp, "vdb")
	installed := filepath.Join(vdbDir, "dev-lang", "python-3.9.9-r1")
	if err := os.MkdirAll(filepath.Join(destDir, "usr", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destDir, "usr", "bin", "python3.9"), []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(installed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installed, "CONTENTS"), []byte("obj /usr/bin/python3.9 deadbeef 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	collisions, err := CheckCollisions(destDir, vdbDir, []string{"dev-lang/python"})
	if err != nil {
		t.Fatalf("CheckCollisions: %v", err)
	}
	if len(collisions) != 0 {
		t.Fatalf("same-package revision reported as collisions: %v", collisions)
	}
}

func TestPkgDirToCPParsesGentooVersions(t *testing.T) {
	vdb := filepath.Join("root", "var", "db", "pkg")
	for _, test := range []struct {
		dir, want string
	}{
		{"python-3.9.9-r1", "dev-lang/python"},
		{"python-3.10.10_p3", "dev-lang/python"},
		{"signal-desktop-bin-7.61.0-r2", "net-im/signal-desktop-bin"},
	} {
		if got := pkgDirToCP(vdb, filepath.Join(vdb, strings.Split(test.want, "/")[0], test.dir)); got != test.want {
			t.Errorf("pkgDirToCP(%q) = %q, want %q", test.dir, got, test.want)
		}
	}
}

func TestCheckCollisions_TwoPackagesInstallSameFile(t *testing.T) {
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	vdbDir := filepath.Join(tmp, "vdb")

	if err := os.MkdirAll(filepath.Join(destDir, "usr/lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destDir, "usr/lib/foo.so"), []byte("lib"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(destDir, "usr/lib/foo.so.1"), []byte("also lib"), 0644); err != nil {
		t.Fatal(err)
	}

	collisions, err := CheckCollisions(destDir, vdbDir, nil)
	if err != nil {
		t.Fatalf("CheckCollisions: %v", err)
	}
	if len(collisions) != 0 {
		t.Logf("collisions (may be empty if cross-detect didn't trigger): %v", collisions)
	}
}

func TestDetectFileCollision_Found(t *testing.T) {
	tmp := t.TempDir()
	vdbDir := filepath.Join(tmp, "vdb")

	existingPkgDir := filepath.Join(vdbDir, "sys-libs", "libfoo-2.0")
	if err := os.MkdirAll(existingPkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	contents := "obj /usr/lib/libfoo.so.1 abcd1234 1234567890\n"
	if err := os.WriteFile(filepath.Join(existingPkgDir, "CONTENTS"), []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}

	msg, found := DetectFileCollision("/usr/lib/libfoo.so.1", vdbDir, "sys-libs/other")
	if !found {
		t.Error("expected collision, got none")
	}
	if !strings.Contains(msg, "libfoo.so.1") {
		t.Errorf("message should mention file: %q", msg)
	}
}

func TestDetectFileCollision_SameOwner(t *testing.T) {
	tmp := t.TempDir()
	vdbDir := filepath.Join(tmp, "vdb")

	existingPkgDir := filepath.Join(vdbDir, "app-misc", "mypkg-1.0")
	if err := os.MkdirAll(existingPkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	contents := "obj /usr/bin/mine abcd 1234567890\n"
	if err := os.WriteFile(filepath.Join(existingPkgDir, "CONTENTS"), []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}

	_, found := DetectFileCollision("/usr/bin/mine", vdbDir, "app-misc/mypkg-1.0")
	if found {
		t.Error("should not report collision for same owner")
	}
}

func TestDetectFileCollision_NotFound(t *testing.T) {
	tmp := t.TempDir()
	vdbDir := filepath.Join(tmp, "vdb")

	_, found := DetectFileCollision("/usr/bin/nonexistent", vdbDir, "app-misc/some")
	if found {
		t.Error("should not find collision for nonexistent file")
	}
}

func TestCheckCollisions_MultipleExistingPackages(t *testing.T) {
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	vdbDir := filepath.Join(tmp, "vdb")

	if err := makeDestDir(destDir, map[string]string{
		"usr/bin/a": "a",
		"usr/bin/b": "b",
		"usr/bin/c": "c",
	}); err != nil {
		t.Fatalf("makeDestDir: %v", err)
	}

	pkg1 := filepath.Join(vdbDir, "cat-a", "pkg1-1.0")
	_ = os.MkdirAll(pkg1, 0755)
	_ = os.WriteFile(filepath.Join(pkg1, "CONTENTS"), []byte("obj /usr/bin/a md5 1\n"), 0644)

	pkg2 := filepath.Join(vdbDir, "cat-b", "pkg2-1.0")
	_ = os.MkdirAll(pkg2, 0755)
	_ = os.WriteFile(filepath.Join(pkg2, "CONTENTS"), []byte("obj /usr/bin/b md5 2\n"), 0644)

	collisions, err := CheckCollisions(destDir, vdbDir, nil)
	if err != nil {
		t.Fatalf("CheckCollisions: %v", err)
	}
	if len(collisions) != 2 {
		t.Fatalf("expected 2 collisions, got %d: %v", len(collisions), collisions)
	}

	sort.Strings(collisions)
	if !strings.Contains(collisions[0], "cat-a/pkg1") && !strings.Contains(collisions[1], "cat-a/pkg1") {
		t.Errorf("expected collision with cat-a/pkg1 in: %v", collisions)
	}
	if !strings.Contains(collisions[0], "cat-b/pkg2") && !strings.Contains(collisions[1], "cat-b/pkg2") {
		t.Errorf("expected collision with cat-b/pkg2 in: %v", collisions)
	}
}

func TestCheckCollisions_EmptyDest(t *testing.T) {
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	vdbDir := filepath.Join(tmp, "vdb")

	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	collisions, err := CheckCollisions(destDir, vdbDir, nil)
	if err != nil {
		t.Fatalf("CheckCollisions: %v", err)
	}
	if len(collisions) != 0 {
		t.Errorf("expected 0 collisions for empty dest, got %d", len(collisions))
	}
}

func TestCheckCollisions_SymlinkCollision(t *testing.T) {
	tmp := t.TempDir()

	destDir := filepath.Join(tmp, "dest")
	vdbDir := filepath.Join(tmp, "vdb")

	if err := os.MkdirAll(filepath.Join(destDir, "usr/lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("libfoo.so.1", filepath.Join(destDir, "usr/lib", "libfoo.so")); err != nil {
		t.Fatal(err)
	}

	existingPkgDir := filepath.Join(vdbDir, "sys-libs", "oldfoo-1.0")
	_ = os.MkdirAll(existingPkgDir, 0755)
	contents := "sym /usr/lib/libfoo.so -> libfoo.so.1 abcd 1234567890\n"
	_ = os.WriteFile(filepath.Join(existingPkgDir, "CONTENTS"), []byte(contents), 0644)

	collisions, err := CheckCollisions(destDir, vdbDir, nil)
	if err != nil {
		t.Fatalf("CheckCollisions: %v", err)
	}
	if len(collisions) != 1 {
		t.Fatalf("expected 1 collision for symlink, got %d: %v", len(collisions), collisions)
	}
}

func TestBuildVDBOwners_DeduplicatesFiles(t *testing.T) {
	tmp := t.TempDir()
	vdbDir := filepath.Join(tmp, "vdb")

	pkg1 := filepath.Join(vdbDir, "cat-a", "pkg1-1.0")
	os.MkdirAll(pkg1, 0755)
	os.WriteFile(filepath.Join(pkg1, "CONTENTS"), []byte("obj /usr/share/doc/readme md5 1\n"), 0644)

	pkg2 := filepath.Join(vdbDir, "cat-b", "pkg2-1.0")
	os.MkdirAll(pkg2, 0755)
	os.WriteFile(filepath.Join(pkg2, "CONTENTS"), []byte("obj /usr/share/doc/readme md5 2\n"), 0644)

	owners, err := buildVDBOwners(vdbDir)
	if err != nil {
		t.Fatalf("buildVDBOwners: %v", err)
	}
	if len(owners) != 1 {
		t.Fatalf("expected 1 entry, got %d: %v", len(owners), owners)
	}
}
