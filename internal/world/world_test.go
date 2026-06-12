package world

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestLoadWorld_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "world")
	content := "sys-apps/portage\napp-shells/bash\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ws, err := LoadWorld(path)
	if err != nil {
		t.Fatalf("LoadWorld: %v", err)
	}
	if len(ws.Atoms) != 2 {
		t.Fatalf("expected 2 atoms, got %d", len(ws.Atoms))
	}
	sort.Strings(ws.Atoms) // LoadWorld sorts, but verify
	if ws.Atoms[0] != "app-shells/bash" {
		t.Errorf("atom[0] = %q, want app-shells/bash", ws.Atoms[0])
	}
	if ws.Atoms[1] != "sys-apps/portage" {
		t.Errorf("atom[1] = %q, want sys-apps/portage", ws.Atoms[1])
	}
}

func TestLoadWorld_CommentsAndBlankLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "world")
	content := "# This is a comment\nsys-apps/portage\n\n# Another comment\napp-shells/bash\n\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ws, err := LoadWorld(path)
	if err != nil {
		t.Fatalf("LoadWorld: %v", err)
	}
	if len(ws.Atoms) != 2 {
		t.Fatalf("expected 2 atoms, got %d: %v", len(ws.Atoms), ws.Atoms)
	}
}

func TestLoadWorld_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "world")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	ws, err := LoadWorld(path)
	if err != nil {
		t.Fatalf("LoadWorld: %v", err)
	}
	if len(ws.Atoms) != 0 {
		t.Errorf("expected 0 atoms, got %d", len(ws.Atoms))
	}
}

func TestLoadWorld_MissingFile(t *testing.T) {
	ws, err := LoadWorld("/nonexistent/path/world")
	if err != nil {
		t.Fatalf("LoadWorld should not error on missing file: %v", err)
	}
	if len(ws.Atoms) != 0 {
		t.Errorf("expected 0 atoms for missing file, got %d", len(ws.Atoms))
	}
}

func TestLoadWorld_OnlyComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "world")
	content := "# only comments\n# and nothing else\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ws, err := LoadWorld(path)
	if err != nil {
		t.Fatalf("LoadWorld: %v", err)
	}
	if len(ws.Atoms) != 0 {
		t.Errorf("expected 0 atoms, got %d", len(ws.Atoms))
	}
}

func TestLoadSystem(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "packages")
	content := "sys-apps/baselayout\nsys-devel/gcc\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ws, err := LoadSystem(path)
	if err != nil {
		t.Fatalf("LoadSystem: %v", err)
	}
	if len(ws.Atoms) != 2 {
		t.Fatalf("expected 2 atoms, got %d", len(ws.Atoms))
	}
}

func TestAdd(t *testing.T) {
	ws := &WorldSet{}
	Add(ws, "sys-apps/portage")
	Add(ws, "app-shells/bash")

	if len(ws.Atoms) != 2 {
		t.Fatalf("expected 2 atoms, got %d", len(ws.Atoms))
	}
	sort.Strings(ws.Atoms)
	if ws.Atoms[0] != "app-shells/bash" {
		t.Errorf("atom[0] = %q", ws.Atoms[0])
	}
}

func TestAdd_Duplicate(t *testing.T) {
	ws := &WorldSet{}
	Add(ws, "sys-apps/portage")
	Add(ws, "sys-apps/portage")

	if len(ws.Atoms) != 1 {
		t.Errorf("expected 1 atom after duplicate add, got %d", len(ws.Atoms))
	}
}

func TestAdd_Empty(t *testing.T) {
	ws := &WorldSet{}
	Add(ws, "")
	Add(ws, "  ")

	if len(ws.Atoms) != 0 {
		t.Errorf("expected 0 atoms after empty add, got %d", len(ws.Atoms))
	}
}

func TestAdd_Nil(t *testing.T) {
	Add(nil, "sys-apps/portage")
}

func TestRemove(t *testing.T) {
	ws := &WorldSet{Atoms: []string{"app-shells/bash", "sys-apps/portage"}}
	Remove(ws, "app-shells/bash")

	if len(ws.Atoms) != 1 {
		t.Fatalf("expected 1 atom, got %d", len(ws.Atoms))
	}
	if ws.Atoms[0] != "sys-apps/portage" {
		t.Errorf("atom[0] = %q, want sys-apps/portage", ws.Atoms[0])
	}
}

func TestRemove_NotFound(t *testing.T) {
	ws := &WorldSet{Atoms: []string{"sys-apps/portage"}}
	Remove(ws, "app-shells/bash")

	if len(ws.Atoms) != 1 {
		t.Errorf("expected 1 atom, got %d", len(ws.Atoms))
	}
}

func TestRemove_Empty(t *testing.T) {
	ws := &WorldSet{}
	Remove(ws, "sys-apps/portage")

	if len(ws.Atoms) != 0 {
		t.Errorf("expected 0 atoms, got %d", len(ws.Atoms))
	}
}

func TestRemove_Nil(t *testing.T) {
	Remove(nil, "sys-apps/portage")
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "world")

	ws := &WorldSet{Atoms: []string{"sys-apps/portage", "app-shells/bash"}}
	if err := ws.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := splitLines(string(data))
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines in saved file, got %d: %q", len(lines), string(data))
	}
	if lines[0] != "app-shells/bash" || lines[1] != "sys-apps/portage" {
		t.Errorf("saved file content = %q", string(data))
	}
}

func TestRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "world")

	content := "sys-apps/portage\napp-shells/bash\ndev-lang/python\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ws, err := LoadWorld(path)
	if err != nil {
		t.Fatalf("LoadWorld: %v", err)
	}
	if len(ws.Atoms) != 3 {
		t.Fatalf("expected 3 atoms after load, got %d", len(ws.Atoms))
	}

	Add(ws, "sys-devel/gcc")
	Remove(ws, "dev-lang/python")

	if err := ws.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := LoadWorld(path)
	if err != nil {
		t.Fatalf("LoadWorld (reload): %v", err)
	}

	expected := []string{"app-shells/bash", "sys-apps/portage", "sys-devel/gcc"}
	if len(reloaded.Atoms) != len(expected) {
		t.Fatalf("reloaded atoms = %d, want %d: %v", len(reloaded.Atoms), len(expected), reloaded.Atoms)
	}
	sort.Strings(reloaded.Atoms)
	for i, exp := range expected {
		if reloaded.Atoms[i] != exp {
			t.Errorf("reloaded[%d] = %q, want %q", i, reloaded.Atoms[i], exp)
		}
	}
}

func TestSave_Nil(t *testing.T) {
	var ws *WorldSet
	err := ws.Save("/tmp/test-world")
	if err == nil {
		t.Error("expected error saving nil WorldSet, got nil")
	}
}

func TestSave_EmptySet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "world")

	ws := &WorldSet{}
	if err := ws.Save(path); err != nil {
		t.Fatalf("Save empty: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("empty set should produce empty file, got %q", string(data))
	}
}

func TestContains(t *testing.T) {
	ws := &WorldSet{Atoms: []string{"sys-apps/portage", "app-shells/bash"}}

	if !ws.Contains("sys-apps/portage") {
		t.Error("Contains(sys-apps/portage) = false, want true")
	}
	if ws.Contains("dev-lang/python") {
		t.Error("Contains(dev-lang/python) = true, want false")
	}
	if ws.Contains("") {
		t.Error("Contains('') = true, want false")
	}
}

func TestLen(t *testing.T) {
	ws := &WorldSet{}
	if ws.Len() != 0 {
		t.Errorf("Len empty = %d, want 0", ws.Len())
	}

	Add(ws, "sys-apps/portage")
	Add(ws, "app-shells/bash")
	if ws.Len() != 2 {
		t.Errorf("Len = %d, want 2", ws.Len())
	}
}

func TestDedup_OnLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "world")
	content := "sys-apps/portage\nsys-apps/portage\napp-shells/bash\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ws, err := LoadWorld(path)
	if err != nil {
		t.Fatalf("LoadWorld: %v", err)
	}
	if len(ws.Atoms) != 2 {
		t.Fatalf("expected 2 deduped atoms, got %d: %v", len(ws.Atoms), ws.Atoms)
	}
}

func TestDeselect_Removes(t *testing.T) {
	ws := &WorldSet{Atoms: []string{"app-shells/bash", "sys-apps/portage"}}
	ws.Deselect("app-shells/bash")

	if len(ws.Atoms) != 1 {
		t.Fatalf("expected 1 atom, got %d: %v", len(ws.Atoms), ws.Atoms)
	}
	if ws.Atoms[0] != "sys-apps/portage" {
		t.Errorf("expected sys-apps/portage, got %q", ws.Atoms[0])
	}
}

func TestDeselect_NotFound(t *testing.T) {
	ws := &WorldSet{Atoms: []string{"sys-apps/portage"}}
	ws.Deselect("app-shells/bash")

	if len(ws.Atoms) != 1 {
		t.Errorf("expected 1 atom, got %d", len(ws.Atoms))
	}
	if ws.Atoms[0] != "sys-apps/portage" {
		t.Errorf("expected sys-apps/portage, got %q", ws.Atoms[0])
	}
}

func TestDeselect_EmptyWorldSet(t *testing.T) {
	ws := &WorldSet{}
	ws.Deselect("sys-apps/portage")
	if len(ws.Atoms) != 0 {
		t.Errorf("expected 0 atoms, got %d", len(ws.Atoms))
	}
}

func TestDeselect_NilWorldSet(t *testing.T) {
	var ws *WorldSet
	ws.Deselect("sys-apps/portage")
}

func TestDeselect_EmptyString(t *testing.T) {
	ws := &WorldSet{Atoms: []string{"sys-apps/portage"}}
	ws.Deselect("")
	if len(ws.Atoms) != 1 {
		t.Errorf("expected 1 atom, got %d", len(ws.Atoms))
	}
}

func TestDeselect_WhitespaceOnly(t *testing.T) {
	ws := &WorldSet{Atoms: []string{"sys-apps/portage"}}
	ws.Deselect("  ")
	if len(ws.Atoms) != 1 {
		t.Errorf("expected 1 atom, got %d", len(ws.Atoms))
	}
}

func TestDeselect_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "world")

	content := "sys-apps/portage\napp-shells/bash\ndev-lang/python\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ws, err := LoadWorld(path)
	if err != nil {
		t.Fatalf("LoadWorld: %v", err)
	}

	ws.Deselect("dev-lang/python")

	if err := ws.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := LoadWorld(path)
	if err != nil {
		t.Fatalf("LoadWorld (reload): %v", err)
	}

	if len(reloaded.Atoms) != 2 {
		t.Fatalf("expected 2 atoms after deselect, got %d: %v", len(reloaded.Atoms), reloaded.Atoms)
	}

	for _, a := range reloaded.Atoms {
		if a == "dev-lang/python" {
			t.Error("dev-lang/python should have been deselected")
		}
	}
}

func TestExpandSet_UnknownSet(t *testing.T) {
	_, err := ExpandSet("@unknown-set", "/var/db/pkg")
	if err == nil {
		t.Error("expected error for unknown set")
	}
}

func TestExpandSet_KnownSets(t *testing.T) {
	sets := []string{"@module-rebuild", "@live-rebuild", "@x11-module-rebuild"}
	for _, s := range sets {
		_, err := ExpandSet(s, "/var/db/pkg")
		if err != nil {
			t.Logf("ExpandSet(%s) error (may be expected in test env): %v", s, err)
		}
	}
}

func TestExpandLiveRebuild(t *testing.T) {
	tmp := t.TempDir()
	vdbRoot := filepath.Join(tmp, "vdb")

	livePath := filepath.Join(vdbRoot, "app-editors", "vim-9999")
	if err := os.MkdirAll(livePath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(livePath, "CONTENTS"), []byte("obj /usr/bin/vim md5 1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	nonLivePath := filepath.Join(vdbRoot, "app-editors", "nano-7.0")
	if err := os.MkdirAll(nonLivePath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonLivePath, "CONTENTS"), []byte("obj /usr/bin/nano md5 2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	atoms, err := expandLiveRebuild(vdbRoot)
	if err != nil {
		t.Fatalf("expandLiveRebuild: %v", err)
	}
	if len(atoms) != 1 {
		t.Fatalf("expected 1 live package, got %d: %v", len(atoms), atoms)
	}
	if !strings.Contains(atoms[0], "vim") {
		t.Errorf("expected vim in live-rebuild, got %q", atoms[0])
	}
}

func TestExpandX11ModuleRebuild(t *testing.T) {
	tmp := t.TempDir()
	vdbRoot := filepath.Join(tmp, "vdb")

	pkgPath := filepath.Join(vdbRoot, "x11-drivers", "xf86-video-intel-2.99.917_p20210115")
	if err := os.MkdirAll(pkgPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgPath, "CONTENTS"), []byte("obj /usr/lib/xorg/modules/drivers/intel_drv.so md5 1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	atoms, err := expandX11ModuleRebuild(vdbRoot)
	if err != nil {
		t.Fatalf("expandX11ModuleRebuild: %v", err)
	}
	if len(atoms) != 1 {
		t.Fatalf("expected 1 x11 driver, got %d: %v", len(atoms), atoms)
	}
	if !strings.Contains(atoms[0], "video-intel") {
		t.Errorf("expected video-intel in x11-module-rebuild, got %q", atoms[0])
	}
}

func TestExpandX11ModuleRebuild_EmptyIfNoDrivers(t *testing.T) {
	tmp := t.TempDir()
	vdbRoot := filepath.Join(tmp, "vdb")

	atoms, err := expandX11ModuleRebuild(vdbRoot)
	if err != nil {
		t.Fatalf("expandX11ModuleRebuild: %v", err)
	}
	if len(atoms) != 0 {
		t.Errorf("expected 0 atoms, got %d: %v", len(atoms), atoms)
	}
}

func TestHasVersionChar(t *testing.T) {
	if !hasVersionChar("1") {
		t.Error("'1' should have version char")
	}
	if !hasVersionChar("a-1") {
		t.Error("'a-1' should have version char (contains digit)")
	}
	if hasVersionChar("abc") {
		t.Error("'abc' should not have version char (no digits)")
	}
	if hasVersionChar("") {
		t.Error("empty string should not have version char")
	}
	if !hasVersionChar("1.2.3") {
		t.Error("'1.2.3' should have version char")
	}
	if !hasVersionChar("2.99.917_p20210115") {
		t.Error("'2.99.917_p20210115' should have version char")
	}
}

func TestSplitVDBPkgName(t *testing.T) {
	tests := []struct {
		name        string
		entryName   string
		wantPkgName string
		wantVersion string
		wantOk      bool
	}{
		{"gcc with version", "gcc-13.2.0", "gcc", "13.2.0", true},
		{"no version", "gcc", "", "", false},
		{"complex version", "xf86-video-intel-2.99.917_p20210115", "xf86-video-intel", "2.99.917_p20210115", true},
		{"revision as version", "pkg-1.2.3-r5", "pkg-1.2.3", "r5", true},
		{"single digit version", "pkg-9", "pkg", "9", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkgName, version, ok := splitVDBPkgName(tt.entryName)
			if ok != tt.wantOk {
				t.Errorf("ok = %v, want %v", ok, tt.wantOk)
			}
			if pkgName != tt.wantPkgName {
				t.Errorf("pkgName = %q, want %q", pkgName, tt.wantPkgName)
			}
			if version != tt.wantVersion {
				t.Errorf("version = %q, want %q", version, tt.wantVersion)
			}
		})
	}
}

func TestSplitVDBPkgName_SysDevelGcc(t *testing.T) {
	// This is the specific case from the requirements: "sys-devel/gcc-13.2.0"
	// splitVDBPkgName takes just the directory entry name, which would be "gcc-13.2.0"
	pkgName, version, ok := splitVDBPkgName("gcc-13.2.0")
	if !ok {
		t.Fatal("expected ok")
	}
	if pkgName != "gcc" {
		t.Errorf("pkgName = %q, want gcc", pkgName)
	}
	if version != "13.2.0" {
		t.Errorf("version = %q, want 13.2.0", version)
	}
}

func TestExpandLiveRebuild_NoLivePackages(t *testing.T) {
	tmp := t.TempDir()
	vdbRoot := filepath.Join(tmp, "vdb")

	nonLivePath := filepath.Join(vdbRoot, "app-editors", "nano-7.0")
	if err := os.MkdirAll(nonLivePath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonLivePath, "CONTENTS"), []byte("obj /usr/bin/nano md5 2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	atoms, err := expandLiveRebuild(vdbRoot)
	if err != nil {
		t.Fatalf("expandLiveRebuild: %v", err)
	}
	if len(atoms) != 0 {
		t.Errorf("expected 0 live packages, got %d: %v", len(atoms), atoms)
	}
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var lines []string
	current := ""
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, current)
			current = ""
		} else if s[i] == '\r' {
			continue
		} else {
			current += string(s[i])
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}
