package world

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/preserved"
)

type WorldSet struct {
	Atoms []string
}

func LoadWorld(path string) (*WorldSet, error) {
	return loadFile(path)
}

func LoadSystem(path string) (*WorldSet, error) {
	return loadFile(path)
}

func loadFile(path string) (*WorldSet, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &WorldSet{}, nil
		}
		return nil, fmt.Errorf("world: open %s: %w", path, err)
	}
	defer f.Close()

	return parseFile(f)
}

func parseFile(f *os.File) (*WorldSet, error) {
	ws := &WorldSet{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ws.Atoms = append(ws.Atoms, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("world: read: %w", err)
	}
	ws.dedup()
	return ws, nil
}

func (ws *WorldSet) dedup() {
	if len(ws.Atoms) <= 1 {
		return
	}
	seen := make(map[string]bool, len(ws.Atoms))
	uniq := make([]string, 0, len(ws.Atoms))
	for _, a := range ws.Atoms {
		if seen[a] {
			continue
		}
		seen[a] = true
		uniq = append(uniq, a)
	}
	ws.Atoms = uniq
	sort.Strings(ws.Atoms)
}

func Add(ws *WorldSet, atom string) {
	if ws == nil {
		return
	}
	atom = strings.TrimSpace(atom)
	if atom == "" {
		return
	}
	for _, a := range ws.Atoms {
		if a == atom {
			return
		}
	}
	ws.Atoms = append(ws.Atoms, atom)
	sort.Strings(ws.Atoms)
}

func Remove(ws *WorldSet, atom string) {
	if ws == nil {
		return
	}
	atom = strings.TrimSpace(atom)
	for i, a := range ws.Atoms {
		if a == atom {
			ws.Atoms = append(ws.Atoms[:i], ws.Atoms[i+1:]...)
			return
		}
	}
}

func (ws *WorldSet) Save(path string) error {
	if ws == nil {
		return fmt.Errorf("world: cannot save nil WorldSet")
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("world: create %s: %w", path, err)
	}
	defer f.Close()

	sorted := make([]string, len(ws.Atoms))
	copy(sorted, ws.Atoms)
	sort.Strings(sorted)

	for _, a := range sorted {
		if _, err := fmt.Fprintln(f, a); err != nil {
			return fmt.Errorf("world: write %s: %w", path, err)
		}
	}

	return f.Sync()
}

func (ws *WorldSet) Contains(atom string) bool {
	for _, a := range ws.Atoms {
		if a == atom {
			return true
		}
	}
	return false
}

func (ws *WorldSet) Len() int {
	return len(ws.Atoms)
}

// Deselect removes an atom from the world set (equivalent to emerge --deselect).
// The atom string must be an exact match to an existing world entry.
func (ws *WorldSet) Deselect(atomStr string) {
	if ws == nil {
		return
	}
	atomStr = strings.TrimSpace(atomStr)
	if atomStr == "" {
		return
	}
	for i, a := range ws.Atoms {
		if a == atomStr {
			ws.Atoms = append(ws.Atoms[:i], ws.Atoms[i+1:]...)
			return
		}
	}
}

// PreservedRebuild returns the list of packages needing rebuild from
// preserved-rebuild, for use with the @preserved-rebuild set.
func PreservedRebuild() ([]string, error) {
	return preserved.RebuildNeeded("/", "/var/db/pkg")
}

// ExpandSet expands a set name into its atom list.
// The depGraph parameter is used for sets that require package graph traversal.
func ExpandSet(setName string, vdbRoot string) ([]string, error) {
	switch setName {
	case "@module-rebuild":
		return expandModuleRebuild(vdbRoot)
	case "@live-rebuild":
		return expandLiveRebuild(vdbRoot)
	case "@x11-module-rebuild":
		return expandX11ModuleRebuild(vdbRoot)
	case "@preserved-rebuild":
		return PreservedRebuild()
	default:
		return nil, fmt.Errorf("world: unknown set %q", setName)
	}
}

// expandModuleRebuild finds packages with installed kernel modules
// by scanning /lib/modules for .ko files and mapping them to VDB packages.
func expandModuleRebuild(vdbRoot string) ([]string, error) {
	modulesDirs := []string{"/lib/modules"}
	var moduleFiles []string
	for _, dir := range modulesDirs {
		filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() && strings.HasSuffix(path, ".ko") {
				moduleFiles = append(moduleFiles, path)
			}
			return nil
		})
	}

	if len(moduleFiles) == 0 {
		return nil, nil
	}

	owners, err := preserved.FindOwningPackages(vdbRoot, moduleFiles)
	if err != nil {
		return nil, fmt.Errorf("module-rebuild: %w", err)
	}

	seen := make(map[string]bool)
	var atoms []string
	for _, pkg := range owners {
		if !seen[pkg] {
			seen[pkg] = true
			atoms = append(atoms, pkg)
		}
	}
	return atoms, nil
}

// expandX11ModuleRebuild finds X11 driver packages that are installed.
func expandX11ModuleRebuild(vdbRoot string) ([]string, error) {
	var atoms []string

	x11Path := filepath.Join(vdbRoot, "x11-drivers")
	pkgs, err := os.ReadDir(x11Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("x11-module-rebuild: %w", err)
	}

	for _, pkgEntry := range pkgs {
		if !pkgEntry.IsDir() {
			continue
		}
		name := pkgEntry.Name()
		pkgName, version, ok := splitVDBPkgName(name)
		if !ok {
			continue
		}
		_ = version
		a := &atom.Atom{Category: "x11-drivers", Package: pkgName}
		atoms = append(atoms, a.String())
	}

	return atoms, nil
}

// splitVDBPkgName splits a VDB entry name like "xf86-video-intel-2.99.917_p20210115"
// into the package name "xf86-video-intel" and version "2.99.917_p20210115".
func splitVDBPkgName(entryName string) (pkgName, version string, ok bool) {
	idx := strings.LastIndex(entryName, "-")
	if idx < 0 {
		return "", "", false
	}
	candidate := entryName[idx+1:]
	if !hasVersionChar(candidate) {
		tail := entryName[:idx]
		idx2 := strings.LastIndex(tail, "-")
		if idx2 < 0 {
			return "", "", false
		}
		candidate = entryName[idx2+1:]
		idx = idx2
	}
	if !hasVersionChar(candidate) {
		return "", "", false
	}
	return entryName[:idx], entryName[idx+1:], true
}

func hasVersionChar(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
		if r == '_' {
			continue
		}
		if r == '-' {
			continue
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			continue
		}
	}
	return false
}

// expandLiveRebuild finds packages installed from VCS ebuilds (9999 versions).
func expandLiveRebuild(vdbRoot string) ([]string, error) {
	var atoms []string
	seen := make(map[string]bool)

	categories, err := os.ReadDir(vdbRoot)
	if err != nil {
		return nil, fmt.Errorf("live-rebuild: %w", err)
	}

	for _, catEntry := range categories {
		if !catEntry.IsDir() {
			continue
		}
		catName := catEntry.Name()
		catPath := filepath.Join(vdbRoot, catName)
		pkgs, err := os.ReadDir(catPath)
		if err != nil {
			continue
		}
		for _, pkgEntry := range pkgs {
			name := pkgEntry.Name()
			if !pkgEntry.IsDir() {
				continue
			}
			pkgName, version, ok := splitVDBPkgName(name)
			if !ok {
				continue
			}
			if version != "9999" {
				continue
			}
			cp := catName + "/" + pkgName + "-9999"
			if !seen[cp] {
				seen[cp] = true
				a := &atom.Atom{Category: catName, Package: pkgName, Version: &atom.Version{Raw: "9999"}}
				atoms = append(atoms, a.String())
			}
		}
	}

	return atoms, nil
}
