package world

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/oplock"
	"github.com/airencracken/arise/internal/preserved"
)

type WorldSet struct {
	Atoms []string
}

// Update holds Portage's world-file lock across load, mutation and atomic save,
// preventing concurrent package-manager operations from losing entries.
func Update(path string, mutate func(*WorldSet) error) (returnErr error) {
	if mutate == nil {
		return fmt.Errorf("world: update callback is required")
	}
	lock, err := oplock.TryAcquirePath(path)
	if err != nil {
		return fmt.Errorf("world: %w", err)
	}
	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil && returnErr == nil {
			returnErr = fmt.Errorf("world: %w", releaseErr)
		}
	}()
	set, err := LoadWorld(path)
	if err != nil {
		return err
	}
	before := append([]string(nil), set.Atoms...)
	if err := mutate(set); err != nil {
		return err
	}
	if equalAtoms(before, set.Atoms) {
		return nil
	}
	return set.Save(path)
}

func equalAtoms(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
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
		return nil, fmt.Errorf("world: could not open file %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			/* Best effort */
		}
	}()

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
		return nil, fmt.Errorf("world: could not read file: %w", err)
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

	directory := filepath.Dir(path)
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("world: could not inspect file %s: %w", path, err)
	}
	f, err := os.CreateTemp(directory, ".world-arise-*")
	if err != nil {
		return fmt.Errorf("world: could not create temporary file beside %s: %w", path, err)
	}
	temporary := f.Name()
	committed := false
	defer func() {
		f.Close()
		if !committed {
			os.Remove(temporary)
		}
	}()
	if err := f.Chmod(mode); err != nil {
		return fmt.Errorf("world: could not set temporary file mode: %w", err)
	}

	sorted := make([]string, len(ws.Atoms))
	copy(sorted, ws.Atoms)
	sort.Strings(sorted)

	for _, a := range sorted {
		if _, err := fmt.Fprintln(f, a); err != nil {
			return fmt.Errorf("world: could not write to file %s: %w", path, err)
		}
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("world: could not sync temporary file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("world: could not close temporary file: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("world: could not atomically replace %s: %w", path, err)
	}
	committed = true
	dir, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("world: could not open parent directory for sync: %w", err)
	}
	if err := dir.Sync(); err != nil {
		dir.Close()
		return fmt.Errorf("world: could not sync parent directory: %w", err)
	}
	if err := dir.Close(); err != nil {
		return fmt.Errorf("world: could not close parent directory: %w", err)
	}
	return nil
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
func PreservedRebuild(root, vdbRoot string) ([]string, error) {
	return preserved.RebuildNeeded(root, vdbRoot)
}

// ExpandSet expands a set name into its atom list.
// The depGraph parameter is used for sets that require package graph traversal.
func ExpandSet(setName, root, vdbRoot string) ([]string, error) {
	switch setName {
	case "@module-rebuild":
		return expandModuleRebuild(vdbRoot)
	case "@live-rebuild":
		return expandLiveRebuild(vdbRoot)
	case "@x11-module-rebuild":
		return expandX11ModuleRebuild(vdbRoot)
	case "@preserved-rebuild":
		return PreservedRebuild(root, vdbRoot)
	default:
		return nil, fmt.Errorf("world: unknown package set %q", setName)
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
		return nil, fmt.Errorf("world: could not determine module-rebuild packages: %w", err)
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
		return nil, fmt.Errorf("world: could not determine x11-module-rebuild packages: %w", err)
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
		return nil, fmt.Errorf("world: could not determine live-rebuild packages: %w", err)
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
