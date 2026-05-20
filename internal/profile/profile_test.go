package profile

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func makeProfileDir(t *testing.T, base string, name string) string {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir profile: %v", err)
	}
	return dir
}

func TestMergeParentsSingleProfile(t *testing.T) {
	profilesRoot := t.TempDir()
	prof := makeProfileDir(t, profilesRoot, "default/linux/amd64/23.0")

	writeTemp(t, prof, "make.defaults", `ARCH="amd64"
CFLAGS="-O2 -pipe"
`)
	writeTemp(t, prof, "packages", "sys-apps/busybox\nsys-libs/glibc\n")

	info, err := MergeParents(prof, profilesRoot)
	if err != nil {
		t.Fatalf("MergeParents: %v", err)
	}

	if v := info.MakeDefaults["ARCH"]; v != "amd64" {
		t.Errorf("ARCH: got %q, want amd64", v)
	}
	if v := info.MakeDefaults["CFLAGS"]; v != "-O2 -pipe" {
		t.Errorf("CFLAGS: got %q, want %q", v, "-O2 -pipe")
	}
	if len(info.SystemSet) != 2 {
		t.Errorf("SystemSet: expected 2, got %d", len(info.SystemSet))
	}
}

func TestMergeParentsWithParentChain(t *testing.T) {
	profilesRoot := t.TempDir()

	grandparent := makeProfileDir(t, profilesRoot, "default/linux")
	parent := makeProfileDir(t, profilesRoot, "default/linux/amd64")
	child := makeProfileDir(t, profilesRoot, "default/linux/amd64/23.0")

	writeTemp(t, grandparent, "make.defaults", `ARCH="x86"
CFLAGS="-O2"
`)
	writeTemp(t, parent, "make.defaults", `ARCH="amd64"
CXXFLAGS="-O2 -pipe"
`)
	writeTemp(t, parent, "parent", "..")
	writeTemp(t, child, "make.defaults", `CFLAGS="-O2 -pipe -march=native"
`)
	writeTemp(t, child, "parent", "..")

	info, err := MergeParents(child, profilesRoot)
	if err != nil {
		t.Fatalf("MergeParents: %v", err)
	}

	if v := info.MakeDefaults["ARCH"]; v != "amd64" {
		t.Errorf("ARCH: got %q, want amd64", v)
	}
	if v := info.MakeDefaults["CFLAGS"]; v != "-O2 -pipe -march=native" {
		t.Errorf("CFLAGS: got %q, want '-O2 -pipe -march=native'", v)
	}
	if v := info.MakeDefaults["CXXFLAGS"]; v != "-O2 -pipe" {
		t.Errorf("CXXFLAGS: got %q, want '-O2 -pipe'", v)
	}
	if len(info.Parents) == 0 {
		t.Error("expected parent paths to be populated")
	}
}

func TestSystemSetMerging(t *testing.T) {
	profilesRoot := t.TempDir()

	base := makeProfileDir(t, profilesRoot, "default/linux")
	child := makeProfileDir(t, profilesRoot, "default/linux/amd64")

	writeTemp(t, base, "packages", "sys-apps/busybox\nsys-libs/glibc\n")
	writeTemp(t, base, "packages.build", "sys-apps/baselayout\n")
	writeTemp(t, child, "packages", "sys-apps/coreutils\nsys-libs/glibc\n")
	writeTemp(t, child, "parent", "..")

	info, err := MergeParents(child, profilesRoot)
	if err != nil {
		t.Fatalf("MergeParents: %v", err)
	}

	if len(info.SystemSet) < 3 {
		t.Errorf("SystemSet: expected at least 3, got %d: %v", len(info.SystemSet), info.SystemSet)
	}

	found := make(map[string]bool)
	for _, pkg := range info.SystemSet {
		found[pkg] = true
	}
	for _, expected := range []string{"sys-apps/busybox", "sys-libs/glibc", "sys-apps/baselayout", "sys-apps/coreutils"} {
		if !found[expected] {
			t.Errorf("SystemSet missing %q", expected)
		}
	}
}

func TestUseFlagsMerging(t *testing.T) {
	profilesRoot := t.TempDir()

	base := makeProfileDir(t, profilesRoot, "base")
	child := makeProfileDir(t, profilesRoot, "base/desktop")

	writeTemp(t, base, "use.force", "elogind\nsystemd\n")
	writeTemp(t, base, "use.mask", "consolekit\n")
	writeTemp(t, child, "use.force", "wayland\n")
	writeTemp(t, child, "use.mask", "qt4\n")
	writeTemp(t, child, "parent", "..")

	info, err := MergeParents(child, profilesRoot)
	if err != nil {
		t.Fatalf("MergeParents: %v", err)
	}

	if len(info.UseForce) != 3 {
		t.Errorf("UseForce: expected 3, got %d: %v", len(info.UseForce), info.UseForce)
	}
	if len(info.UseMask) != 2 {
		t.Errorf("UseMask: expected 2, got %d: %v", len(info.UseMask), info.UseMask)
	}

	hasFlag := func(flags []string, target string) bool {
		for _, f := range flags {
			if f == target {
				return true
			}
		}
		return false
	}

	if !hasFlag(info.UseForce, "wayland") {
		t.Error("UseForce missing wayland")
	}
	if !hasFlag(info.UseMask, "qt4") {
		t.Error("UseMask missing qt4")
	}
}

func TestCircularParentDetection(t *testing.T) {
	profilesRoot := t.TempDir()

	a := makeProfileDir(t, profilesRoot, "a")
	b := makeProfileDir(t, profilesRoot, "b")

	writeTemp(t, a, "parent", "../b")
	writeTemp(t, b, "parent", "../a")

	_, err := MergeParents(a, profilesRoot)
	if err == nil {
		t.Error("expected error for circular parent reference")
	}
	if err != nil && !strings.Contains(err.Error(), "circular") {
		t.Errorf("expected circular error, got: %v", err)
	}
}

func TestMissingParentFile(t *testing.T) {
	profilesRoot := t.TempDir()
	prof := makeProfileDir(t, profilesRoot, "standalone")

	writeTemp(t, prof, "make.defaults", `FOO="bar"`)

	info, err := MergeParents(prof, profilesRoot)
	if err != nil {
		t.Fatalf("MergeParents: %v", err)
	}

	if v := info.MakeDefaults["FOO"]; v != "bar" {
		t.Errorf("FOO: got %q, want bar", v)
	}
}

func TestNonExistentProfile(t *testing.T) {
	_, err := MergeParents("/nonexistent/profile/path", "/var/db/repos/gentoo/profiles")
	if err == nil {
		t.Error("expected error for non-existent profile")
	}
}

func TestPkgUseAcrossProfiles(t *testing.T) {
	profilesRoot := t.TempDir()

	base := makeProfileDir(t, profilesRoot, "base")
	child := makeProfileDir(t, profilesRoot, "base/desktop")

	writeTemp(t, base, "package.use.force", "sys-libs/glibc nptl\nsys-devel/gcc openmp\n")
	writeTemp(t, child, "package.use.force", "sys-libs/glibc ssp\n")
	writeTemp(t, child, "package.use.mask", "app-editors/vim X\n")
	writeTemp(t, child, "parent", "..")

	info, err := MergeParents(child, profilesRoot)
	if err != nil {
		t.Fatalf("MergeParents: %v", err)
	}

	if flags, ok := info.PkgUseForce["sys-libs/glibc"]; !ok {
		t.Error("expected sys-libs/glibc in PkgUseForce")
	} else {
		if len(flags) < 2 {
			t.Errorf("expected at least 2 USE flags for glibc, got %d: %v", len(flags), flags)
		}
		hasSsp := false
		for _, f := range flags {
			if f == "ssp" {
				hasSsp = true
			}
		}
		if !hasSsp {
			t.Error("PkgUseForce for glibc missing ssp")
		}
	}

	if flags, ok := info.PkgUseForce["sys-devel/gcc"]; !ok {
		t.Error("expected sys-devel/gcc in PkgUseForce")
	} else if len(flags) == 0 || flags[0] != "openmp" {
		t.Errorf("expected openmp for gcc, got %v", flags)
	}

	if flags, ok := info.PkgUseMask["app-editors/vim"]; !ok {
		t.Error("expected app-editors/vim in PkgUseMask")
	} else if len(flags) == 0 || flags[0] != "X" {
		t.Errorf("expected X masked for vim, got %v", flags)
	}
}

func TestResolveParentRelative(t *testing.T) {
	profilePath := "/var/db/repos/gentoo/profiles/default/linux/amd64/23.0"

	resolved, err := ResolveParent(profilePath, "../../../../targets/desktop", "/var/db/repos/gentoo/profiles")
	if err != nil {
		t.Fatalf("ResolveParent: %v", err)
	}

	expected := filepath.Clean("/var/db/repos/gentoo/profiles/targets/desktop")
	if resolved != expected {
		t.Errorf("resolved: got %q, want %q", resolved, expected)
	}
}

func TestResolveParentAbsolute(t *testing.T) {
	resolved, err := ResolveParent("/some/profile", "/absolute/path/to/parent", "/profiles")
	if err != nil {
		t.Fatalf("ResolveParent: %v", err)
	}
	if resolved != "/absolute/path/to/parent" {
		t.Errorf("resolved: got %q", resolved)
	}
}

func TestResolveParentEmpty(t *testing.T) {
	_, err := ResolveParent("/some/profile", "", "/profiles")
	if err == nil {
		t.Error("expected error for empty parent line")
	}
}

func TestLoadProfileSymlink(t *testing.T) {
	profilesRoot := t.TempDir()
	prof := makeProfileDir(t, profilesRoot, "default/linux/amd64/23.0")
	writeTemp(t, prof, "make.defaults", `ARCH="amd64"`)

	linkPath := filepath.Join(t.TempDir(), "make.profile")
	if err := os.Symlink(prof, linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	info, err := LoadProfile(linkPath, profilesRoot)
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}

	if v := info.MakeDefaults["ARCH"]; v != "amd64" {
		t.Errorf("ARCH: got %q, want amd64", v)
	}
}

func TestLoadProfileSymlinkNonExistent(t *testing.T) {
	_, err := LoadProfile("/nonexistent/make.profile", "/profiles")
	if err == nil {
		t.Error("expected error for non-existent symlink")
	}
}

func TestMakeDefaultsEmptyLinesAndComments(t *testing.T) {
	profilesRoot := t.TempDir()
	prof := makeProfileDir(t, profilesRoot, "test")

	writeTemp(t, prof, "make.defaults", `
# Comment line
ARCH="amd64"

# Another comment
CFLAGS="-O2"
`)
	info, err := MergeParents(prof, profilesRoot)
	if err != nil {
		t.Fatalf("MergeParents: %v", err)
	}

	if len(info.MakeDefaults) != 2 {
		t.Errorf("expected 2 vars, got %d: %v", len(info.MakeDefaults), info.MakeDefaults)
	}
}

func TestEmptyProfile(t *testing.T) {
	profilesRoot := t.TempDir()
	prof := makeProfileDir(t, profilesRoot, "empty")

	info, err := MergeParents(prof, profilesRoot)
	if err != nil {
		t.Fatalf("MergeParents: %v", err)
	}

	if len(info.MakeDefaults) != 0 {
		t.Errorf("expected empty MakeDefaults, got %v", info.MakeDefaults)
	}
	if len(info.SystemSet) != 0 {
		t.Errorf("expected empty SystemSet, got %v", info.SystemSet)
	}
}

func TestAdversarialHugeParentChain(t *testing.T) {
	profilesRoot := t.TempDir()

	depth := 100
	var prevDir string
	for i := 0; i < depth; i++ {
		dirName := filepath.Join("chain", strings.Repeat("x", i%50), strings.Repeat("d", 10), fmt.Sprintf("level%d", i))
		dir := makeProfileDir(t, profilesRoot, dirName)
		writeTemp(t, dir, "make.defaults",
			fmt.Sprintf("LEVEL_%d=value_%d\n", i, i))
		if prevDir != "" {
			writeTemp(t, dir, "parent", prevDir)
		}
		prevDir = dir
	}

	leaf := prevDir
	info, err := MergeParents(leaf, profilesRoot)
	if err != nil {
		t.Fatalf("MergeParents on deep chain: %v", err)
	}

	if len(info.MakeDefaults) < depth {
		t.Errorf("expected at least %d MakeDefaults, got %d", depth, len(info.MakeDefaults))
	}
}

func TestAdversarialBinaryGarbageParents(t *testing.T) {
	profilesRoot := t.TempDir()

	a := makeProfileDir(t, profilesRoot, "a")
	b := makeProfileDir(t, profilesRoot, "b")

	garbage := make([]byte, 2048)
	rng := rand.New(rand.NewSource(42))
	for i := range garbage {
		garbage[i] = byte(rng.Intn(256))
		if garbage[i] == 0 {
			garbage[i] = 0x41
		}
	}

	writeTemp(t, a, "parent", string(garbage))
	writeTemp(t, b, "parent", "../a")

	info, err := MergeParents(b, profilesRoot)
	if err != nil {
		_ = info
	} else {
		_ = info
	}
}

func TestAdversarialDeepRecursion(t *testing.T) {
	profilesRoot := t.TempDir()

	a := makeProfileDir(t, profilesRoot, "self-ref")
	writeTemp(t, a, "parent", "self-ref")

	_, err := MergeParents(a, profilesRoot)
	if err == nil {
		t.Error("expected error for circular self-reference")
	}
}

func TestMutationParentByteFlip(t *testing.T) {
	profilesRoot := t.TempDir()
	child := makeProfileDir(t, profilesRoot, "child")

	valid := `..`
	for i := 0; i < len(valid); i++ {
		mutated := []byte(valid)
		mutated[i] = mutated[i] ^ 0xFF

		writeTemp(t, child, "parent", string(mutated))

		info, err := MergeParents(child, profilesRoot)
		_ = info
		_ = err
	}
}

func TestLoadBashrc(t *testing.T) {
	profilesRoot := t.TempDir()
	prof := makeProfileDir(t, profilesRoot, "test")

	writeTemp(t, prof, "profile.bashrc", `FOO="bar"
export BAR="baz"
`)

	info, err := MergeParents(prof, profilesRoot)
	if err != nil {
		t.Fatalf("MergeParents: %v", err)
	}

	if v := info.MakeDefaults["FOO"]; v != "bar" {
		t.Errorf("FOO: got %q, want bar", v)
	}
	if v := info.MakeDefaults["BAR"]; v != "baz" {
		t.Errorf("BAR: got %q, want baz", v)
	}
}

func TestLoadBashrc_CommentsAndBlankLines(t *testing.T) {
	profilesRoot := t.TempDir()
	prof := makeProfileDir(t, profilesRoot, "test")

	writeTemp(t, prof, "profile.bashrc", `
# This is a comment
FOO="bar"

# Another comment
`)
	info, err := MergeParents(prof, profilesRoot)
	if err != nil {
		t.Fatalf("MergeParents: %v", err)
	}

	if v := info.MakeDefaults["FOO"]; v != "bar" {
		t.Errorf("FOO: got %q, want bar", v)
	}
}

func TestParseBashrcAssignment_Simple(t *testing.T) {
	name, value, ok := parseBashrcAssignment("VAR=value")
	if !ok {
		t.Fatal("expected ok for simple assignment")
	}
	if name != "VAR" {
		t.Errorf("name: got %q, want VAR", name)
	}
	if value != "value" {
		t.Errorf("value: got %q, want value", value)
	}
}

func TestParseBashrcAssignment_QuotedValue(t *testing.T) {
	name, value, ok := parseBashrcAssignment(`VAR="quoted value"`)
	if !ok {
		t.Fatal("expected ok for quoted value")
	}
	if name != "VAR" {
		t.Errorf("name: got %q, want VAR", name)
	}
	if value != "quoted value" {
		t.Errorf("value: got %q, want 'quoted value'", value)
	}
}

func TestParseBashrcAssignment_Export(t *testing.T) {
	name, value, ok := parseBashrcAssignment("export FOO=bar")
	if !ok {
		t.Fatal("expected ok for export assignment")
	}
	if name != "FOO" {
		t.Errorf("name: got %q, want FOO", name)
	}
	if value != "bar" {
		t.Errorf("value: got %q, want bar", value)
	}
}

func TestParseBashrcAssignment_InvalidAssignment(t *testing.T) {
	_, _, ok := parseBashrcAssignment("# comment")
	if ok {
		t.Error("comment line should not be a valid assignment")
	}
	_, _, ok = parseBashrcAssignment("")
	if ok {
		t.Error("empty line should not be a valid assignment")
	}
}

func TestIsBashVarName_ValidNames(t *testing.T) {
	validNames := []string{"VAR", "var", "MY_VAR", "CFLAGS", "A123", "_private", "a"}
	for _, name := range validNames {
		if !isBashVarName(name) {
			t.Errorf("%q should be a valid bash var name", name)
		}
	}
}

func TestIsBashVarName_InvalidNames(t *testing.T) {
	invalidNames := []string{"", " ", "1abc", "123", "var with space", "a-b", "my.var"}
	for _, name := range invalidNames {
		if isBashVarName(name) {
			t.Errorf("%q should be invalid", name)
		}
	}
}

func TestDeduplicationUseFlags(t *testing.T) {
	profilesRoot := t.TempDir()

	base := makeProfileDir(t, profilesRoot, "base")
	child := makeProfileDir(t, profilesRoot, "base/child")

	writeTemp(t, base, "use.force", "elogind\nsystemd\n")
	writeTemp(t, child, "use.force", "elogind\nwayland\n")
	writeTemp(t, child, "parent", "..")

	info, err := MergeParents(child, profilesRoot)
	if err != nil {
		t.Fatalf("MergeParents: %v", err)
	}

	count := 0
	for _, f := range info.UseForce {
		if f == "elogind" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("elogind should appear once, got %d times", count)
	}
}
