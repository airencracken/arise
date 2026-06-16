package preserved

import (
	"bufio"
	"debug/elf"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// BrokenLink represents an ELF binary that requires a shared library
// that is no longer available on the filesystem.
type BrokenLink struct {
	Binary    string
	Needs     string
	OwningPkg string
}

// PreservedLib represents an old shared library that was kept during
// an upgrade and may still be referenced by installed binaries.
type PreservedLib struct {
	Path      string
	Soname    string
	Version   string
	OwningPkg string
}

// lddNotFoundRE matches ldd output lines like:
//
//	libfoo.so.1 => not found
var lddNotFoundRE = regexp.MustCompile(`^\s*(\S+)\s*=>\s*not found`)

// lddMappingRE matches ldd output lines like:
//
//	libfoo.so.1 => /usr/lib/libfoo.so.1 (0x7f...)
var lddMappingRE = regexp.MustCompile(`^\s*(\S+)\s*=>\s*(\S+)`)

// lddPath is the path to the ldd binary; overridable in tests.
var lddPath = "ldd"

// readelfPath is the path to the readelf binary; overridable in tests.
var readelfPath = "readelf"

// elfScanDirs lists directories to scan for ELF binaries.
var elfScanDirs = []string{
	"usr/bin",
	"usr/sbin",
	"usr/lib",
	"usr/lib64",
	"usr/libexec",
	"bin",
	"sbin",
	"lib",
	"lib64",
}

// libSearchPaths lists directories to search for shared libraries.
var libSearchPaths = []string{
	"usr/lib",
	"usr/lib64",
	"lib",
	"lib64",
}

// ---------------------------------------------------------------------------
// ScanBrokenLinks
// ---------------------------------------------------------------------------

// ScanBrokenLinks scans the filesystem for ELF binaries that require
// shared libraries that are no longer available.
// root is the root directory to scan (typically "/").
func ScanBrokenLinks(root string) ([]BrokenLink, error) {
	if err := checkLDD(); err != nil {
		return nil, err
	}

	var broken []BrokenLink
	for _, dir := range elfScanDirs {
		fullDir := filepath.Join(root, dir)
		_ = filepath.Walk(fullDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !info.Mode().IsRegular() {
				return nil
			}
			if !isELF(path) {
				return nil
			}
			missing, ldErr := lddMissing(path)
			if ldErr != nil {
				return nil
			}
			for _, lib := range missing {
				broken = append(broken, BrokenLink{
					Binary: path,
					Needs:  lib,
				})
			}
			return nil
		})
	}
	return broken, nil
}

// checkLDD verifies that ldd is available on the system.
func checkLDD() error {
	if _, err := exec.LookPath(lddPath); err != nil {
		return fmt.Errorf("could not run ldd to check library dependencies: %w", err)
	}
	return nil
}

// lddMissing runs ldd on an ELF binary and returns the list of
// shared libraries that were not found.
func lddMissing(path string) ([]string, error) {
	cmd := exec.Command(lddPath, path)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseLDDMissing(string(out)), nil
}

// parseLDDMissing extracts "not found" libraries from ldd output.
func parseLDDMissing(output string) []string {
	var missing []string
	sc := bufio.NewScanner(strings.NewReader(output))
	for sc.Scan() {
		m := lddNotFoundRE.FindStringSubmatch(sc.Text())
		if len(m) >= 2 {
			missing = append(missing, m[1])
		}
	}
	return missing
}

// ---------------------------------------------------------------------------
// ScanPreservedLibs
// ---------------------------------------------------------------------------

// ScanPreservedLibs scans /usr/lib and /usr/lib64 for preserved libraries.
func ScanPreservedLibs(root string) ([]PreservedLib, error) {
	var preserved []PreservedLib

	libDirs := []string{
		filepath.Join(root, "usr/lib"),
		filepath.Join(root, "usr/lib64"),
	}

	for _, dir := range libDirs {
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !info.Mode().IsRegular() {
				return nil
			}
			if !isPreservedCandidate(path) {
				return nil
			}
			pl := PreservedLib{Path: path}
			if soname, err := elfSoname(path); err == nil {
				pl.Soname = soname
			}
			if pl.Soname == "" {
				pl.Soname = sonameFromPath(path)
			}
			pl.Version = versionFromSONAME(pl.Soname)
			preserved = append(preserved, pl)
			return nil
		})
	}
	return preserved, nil
}

// isPreservedCandidate checks whether a file looks like a preserved library.
func isPreservedCandidate(path string) bool {
	base := filepath.Base(path)
	dir := filepath.Dir(path)

	// Match directories like *-compat*
	if strings.Contains(dir, "compat") {
		return strings.HasPrefix(base, "lib") && strings.Contains(base, ".so")
	}

	// Match *.so.<major>.<minor> or *.so.<major>.<minor>.<patch>
	if matched, _ := filepath.Match("lib*.so.*.*", base); matched {
		return true
	}
	if matched, _ := filepath.Match("lib*.so.*.*.*", base); matched {
		return true
	}

	return false
}

// elfSoname reads the SONAME from an ELF shared library using readelf.
func elfSoname(path string) (string, error) {
	f, err := elf.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			/* Best effort */
		}
	}()

	if f.Type != elf.ET_DYN {
		return "", fmt.Errorf("file is not a shared library")
	}

	dynStrings, err := f.DynString(elf.DT_SONAME)
	if err != nil || len(dynStrings) == 0 || dynStrings[0] == "" {
		return "", fmt.Errorf("shared library has no SONAME field")
	}
	return dynStrings[0], nil
}

// sonameFromPath derives a soname from the filename.
func sonameFromPath(path string) string {
	base := filepath.Base(path)
	idx := strings.LastIndex(base, ".so")
	if idx >= 0 {
		return base[:idx+3] + base[idx+3:]
	}
	return base
}

// versionFromSONAME extracts the version portion from a soname.
func versionFromSONAME(soname string) string {
	idx := strings.LastIndex(soname, ".so")
	if idx < 0 {
		return ""
	}
	rest := soname[idx+3:]
	rest = strings.TrimPrefix(rest, ".")
	return rest
}

// ---------------------------------------------------------------------------
// FindOwningPackages
// ---------------------------------------------------------------------------

// FindOwningPackages resolves which installed packages own the given files
// by reading VDB CONTENTS files.
// vdbRoot is the path to /var/db/pkg.
func FindOwningPackages(vdbRoot string, files []string) (map[string]string, error) {
	if len(files) == 0 {
		return map[string]string{}, nil
	}

	need := make(map[string]bool, len(files))
	for _, f := range files {
		need[f] = true
	}

	result := make(map[string]string, len(files))

	entries, err := os.ReadDir(vdbRoot)
	if err != nil {
		return nil, fmt.Errorf("could not read installed package database at %q: %w", vdbRoot, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		category := entry.Name()
		catPath := filepath.Join(vdbRoot, category)

		pkgs, rderr := os.ReadDir(catPath)
		if rderr != nil {
			continue
		}
		for _, pkg := range pkgs {
			if !pkg.IsDir() {
				continue
			}
			vdbPkgPath := filepath.Join(catPath, pkg.Name())
			contentsPath := filepath.Join(vdbPkgPath, "CONTENTS")
			data, err := os.ReadFile(contentsPath)
			if err != nil {
				continue
			}

			pkgKey := category + "/" + pkg.Name()
			sc := bufio.NewScanner(strings.NewReader(string(data)))
			for sc.Scan() {
				line := sc.Text()
				if !strings.HasPrefix(line, "obj ") {
					continue
				}
				parts := strings.Fields(line)
				if len(parts) < 2 {
					continue
				}
				filePath := parts[1]
				if need[filePath] {
					result[filePath] = pkgKey
				}
			}
		}
	}

	return result, nil
}

// vdbContentsMap builds a complete file -> package mapping from VDB.
func vdbContentsMap(vdbRoot string) (map[string]string, error) {
	m := make(map[string]string)

	entries, err := os.ReadDir(vdbRoot)
	if err != nil {
		return nil, fmt.Errorf("could not read installed package database at %q: %w", vdbRoot, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		category := entry.Name()
		catPath := filepath.Join(vdbRoot, category)

		pkgs, rderr := os.ReadDir(catPath)
		if rderr != nil {
			continue
		}
		for _, pkg := range pkgs {
			if !pkg.IsDir() {
				continue
			}
			vdbPkgPath := filepath.Join(catPath, pkg.Name())
			contentsPath := filepath.Join(vdbPkgPath, "CONTENTS")
			data, err := os.ReadFile(contentsPath)
			if err != nil {
				continue
			}

			pkgKey := category + "/" + pkg.Name()
			sc := bufio.NewScanner(strings.NewReader(string(data)))
			for sc.Scan() {
				line := sc.Text()
				if !strings.HasPrefix(line, "obj ") && !strings.HasPrefix(line, "sym ") {
					continue
				}
				parts := strings.Fields(line)
				if len(parts) < 2 {
					continue
				}
				filePath := parts[1]
				m[filePath] = pkgKey
			}
		}
	}

	return m, nil
}

// ---------------------------------------------------------------------------
// RebuildNeeded
// ---------------------------------------------------------------------------

// RebuildNeeded returns the list of atom strings for packages that need
// to be rebuilt based on broken links and preserved libraries.
func RebuildNeeded(root, vdbRoot string) ([]string, error) {
	broken, err := ScanBrokenLinks(root)
	if err != nil {
		return nil, fmt.Errorf("could not scan for broken library links: %w", err)
	}

	preserved, err := ScanPreservedLibs(root)
	if err != nil {
		return nil, fmt.Errorf("could not scan for preserved libraries: %w", err)
	}

	seen := make(map[string]bool)
	var atoms []string

	// Broken links: need to rebuild the package owning the binary
	var binaryFiles []string
	for _, bl := range broken {
		binaryFiles = append(binaryFiles, bl.Binary)
	}
	if len(binaryFiles) > 0 {
		owning, ferr := FindOwningPackages(vdbRoot, binaryFiles)
		if ferr != nil {
			return nil, fmt.Errorf("could not find owning packages for broken binaries: %w", ferr)
		}
		for _, bl := range broken {
			if pkg, ok := owning[bl.Binary]; ok {
				if !seen[pkg] {
					seen[pkg] = true
					atoms = append(atoms, pkg)
				}
			}
		}
	}

	// Preserved libs: need to rebuild packages that own or link to them
	for _, pl := range preserved {
		if pl.OwningPkg != "" && !seen[pl.OwningPkg] {
			seen[pl.OwningPkg] = true
			atoms = append(atoms, pl.OwningPkg)
		}
	}

	// Also check which packages link to preserved libs
	if len(preserved) > 0 {
		contentsMap, cerr := vdbContentsMap(vdbRoot)
		if cerr != nil {
			return nil, fmt.Errorf("could not read installed package database contents: %w", cerr)
		}
		preservedPaths := make(map[string]bool)
		for _, pl := range preserved {
			preservedPaths[pl.Path] = true
		}
		// Walk ELF binaries in VDB and check their NEEDED libs
		for filePath, pkgKey := range contentsMap {
			if _, ok := preservedPaths[filePath]; ok {
				continue
			}
			fullPath := filepath.Join(root, filePath)
			if !isELF(fullPath) {
				continue
			}
			needed, err := elfNeededLibraries(fullPath)
			if err != nil {
				continue
			}
			for _, n := range needed {
				for _, pl := range preserved {
					if pl.Soname == n {
						if !seen[pkgKey] {
							seen[pkgKey] = true
							atoms = append(atoms, pkgKey)
						}
					}
				}
			}
		}
	}

	sort.Strings(atoms)
	return atoms, nil
}

// ---------------------------------------------------------------------------
// RevdepRebuild
// ---------------------------------------------------------------------------

// RevdepRebuild performs a full reverse dependency scan, checking all
// installed packages for broken shared library links.
func RevdepRebuild(root, vdbRoot string) ([]string, error) {
	if err := checkLDD(); err != nil {
		return nil, err
	}

	contentsMap, err := vdbContentsMap(vdbRoot)
	if err != nil {
		return nil, fmt.Errorf("could not read installed package database contents: %w", err)
	}

	needRebuild := make(map[string]bool)

	for filePath, pkgKey := range contentsMap {
		fullPath := filepath.Join(root, filePath)
		if !isELF(fullPath) {
			continue
		}
		missing, ldErr := lddMissing(fullPath)
		if ldErr != nil {
			continue
		}
		if len(missing) > 0 {
			needRebuild[pkgKey] = true
		}
	}

	var atoms []string
	for pkg := range needRebuild {
		atoms = append(atoms, pkg)
	}
	sort.Strings(atoms)
	return atoms, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// isELF checks whether a file has the ELF magic bytes.
func isELF(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			/* Best effort */
		}
	}()

	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return false
	}
	return magic[0] == 0x7f && magic[1] == 'E' && magic[2] == 'L' && magic[3] == 'F'
}

// elfNeededLibraries extracts NEEDED library names from an ELF binary
// using debug/elf (no external tool required).
func elfNeededLibraries(path string) ([]string, error) {
	f, err := elf.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			/* Best effort */
		}
	}()

	needed, err := f.ImportedLibraries()
	if err != nil {
		return nil, err
	}
	return needed, nil
}

// fileExists is a test helper for checking filesystem paths.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
