package preserved

import (
	"bufio"
	"context"
	"debug/elf"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/airencracken/gentooling"
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

// RebuildReason explains one concrete ELF edge that requires an installed
// package to be rebuilt. Preserved-library edges are kept distinct from
// generally missing SONAMEs so callers cannot mistake a damaged installation
// for a normal preserve-libs transition.
type RebuildReason struct {
	Package        string `json:"package"`
	Binary         string `json:"binary"`
	Needed         string `json:"needed"`
	Kind           string `json:"kind"`
	PreservedPath  string `json:"preserved_path,omitempty"`
	PreservedOwner string `json:"preserved_owner,omitempty"`
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
	available := loaderLibraryNames(root)

	paths := make(map[string]bool)
	for _, dir := range elfScanDirs {
		fullDir := filepath.Join(root, dir)
		_ = filepath.Walk(fullDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !info.Mode().IsRegular() {
				return nil
			}
			paths[path] = true
			return nil
		})
	}
	jobs := make(chan string)
	results := make(chan []BrokenLink)
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	} else if workers > 16 {
		workers = 16
	}
	var workerGroup sync.WaitGroup
	for range workers {
		workerGroup.Add(1)
		go func() {
			defer workerGroup.Done()
			for path := range jobs {
				if !isELF(path) {
					continue
				}
				needed, err := elfNeededLibraries(path)
				if err != nil {
					continue
				}
				var found []BrokenLink
				for _, library := range needed {
					if !available[library] {
						found = append(found, BrokenLink{Binary: path, Needs: library})
					}
				}
				if len(found) > 0 {
					results <- found
				}
			}
		}()
	}
	go func() {
		for path := range paths {
			jobs <- path
		}
		close(jobs)
		workerGroup.Wait()
		close(results)
	}()
	var broken []BrokenLink
	for found := range results {
		broken = append(broken, found...)
	}
	sort.Slice(broken, func(i, j int) bool {
		if broken[i].Binary != broken[j].Binary {
			return broken[i].Binary < broken[j].Binary
		}
		return broken[i].Needs < broken[j].Needs
	})
	return broken, nil
}

func loaderLibraryNames(root string) map[string]bool {
	directories := loaderSearchDirectories(root)
	available := make(map[string]bool)
	for directory := range directories {
		entries, err := os.ReadDir(directory)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				// glibc hardware-capability directories are part of the loader's
				// search beneath an otherwise configured directory.
				if entry.Name() == "glibc-hwcaps" {
					collectLibraryNames(filepath.Join(directory, entry.Name()), available, true)
				}
				continue
			}
			available[entry.Name()] = true
		}
	}
	return available
}

func loaderSearchDirectories(root string) map[string]bool {
	directories := make(map[string]bool)
	for _, relative := range libSearchPaths {
		directories[filepath.Join(root, relative)] = true
	}
	readLoaderConfig(filepath.Join(root, "etc/ld.so.conf"), root, directories, make(map[string]bool))
	return directories
}

func neededLibraryReachable(root, binary, runpath, library string, loaderDirectories map[string]bool) bool {
	search := make(map[string]bool, len(loaderDirectories)+4)
	for directory := range loaderDirectories {
		search[directory] = true
	}
	origin := filepath.Dir(binary)
	for _, raw := range strings.Split(runpath, ":") {
		if raw == "" {
			continue
		}
		raw = strings.ReplaceAll(raw, "${ORIGIN}", origin)
		raw = strings.ReplaceAll(raw, "$ORIGIN", origin)
		if !filepath.IsAbs(raw) {
			raw = filepath.Join(origin, raw)
		}
		search[filepath.Join(root, strings.TrimPrefix(filepath.Clean(raw), string(filepath.Separator)))] = true
	}
	for directory := range search {
		if info, err := os.Stat(filepath.Join(directory, library)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func collectLibraryNames(directory string, available map[string]bool, recursive bool) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && recursive {
			collectLibraryNames(filepath.Join(directory, entry.Name()), available, true)
			continue
		}
		if !entry.IsDir() {
			available[entry.Name()] = true
		}
	}
}

func readLoaderConfig(path, root string, directories, visited map[string]bool) {
	if visited[path] {
		return
	}
	visited[path] = true
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.SplitN(raw, "#", 2)[0])
		if line == "" {
			continue
		}
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "include" {
			pattern := fields[1]
			if filepath.IsAbs(pattern) {
				pattern = filepath.Join(root, strings.TrimPrefix(pattern, string(filepath.Separator)))
			} else {
				pattern = filepath.Join(filepath.Dir(path), pattern)
			}
			matches, _ := filepath.Glob(pattern)
			for _, match := range matches {
				readLoaderConfig(match, root, directories, visited)
			}
			continue
		}
		directory := line
		if filepath.IsAbs(directory) {
			directory = filepath.Join(root, strings.TrimPrefix(directory, string(filepath.Separator)))
		} else {
			directory = filepath.Join(root, directory)
		}
		directories[filepath.Clean(directory)] = true
	}
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
	registryPath := filepath.Join(root, "var/lib/portage/preserved_libs_registry")
	if registered, found, err := scanPreservedRegistry(root, registryPath); found || err != nil {
		return registered, err
	}

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

func scanPreservedRegistry(root, path string) ([]PreservedLib, bool, error) {
	_, statErr := os.Stat(path)
	if os.IsNotExist(statErr) {
		return nil, false, nil
	}
	if statErr != nil {
		return nil, true, fmt.Errorf("could not inspect preserved library registry %q: %w", path, statErr)
	}
	records, err := gentooling.ReadPreservedLibraries(context.Background(), root, path)
	if err != nil {
		return nil, true, err
	}
	seen := make(map[string]bool)
	var preserved []PreservedLib
	for _, record := range records {
		for _, fullPath := range record.RootedPaths {
			if seen[fullPath] {
				continue
			}
			if _, err := os.Lstat(fullPath); err != nil {
				continue
			}
			seen[fullPath] = true
			soname, err := elfSoname(fullPath)
			if err != nil || soname == "" {
				soname = sonameFromPath(fullPath)
			}
			preserved = append(preserved, PreservedLib{
				Path: fullPath, Soname: soname, Version: versionFromSONAME(soname), OwningPkg: record.Owner.CPV(),
			})
		}
	}
	sort.Slice(preserved, func(i, j int) bool { return preserved[i].Path < preserved[j].Path })
	return preserved, true, nil
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

// ReverseELFConsumers returns installed packages other than installedCPV whose
// VDB linkage metadata requires a SONAME supplied by installedCPV. This guards
// removals that dependency strings alone cannot safely model during package
// renames, repository transitions, or stale dynamic-deps metadata.
func ReverseELFConsumers(vdbRoot, installedCPV string) ([]string, error) {
	target := filepath.Join(vdbRoot, filepath.FromSlash(installedCPV), "NEEDED.ELF.2")
	data, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			hasELF, inspectErr := packageOwnsELFWithoutLinkageMetadata(vdbRoot, installedCPV)
			if inspectErr != nil {
				return nil, fmt.Errorf("verify absent linkage metadata for %s: %w", installedCPV, inspectErr)
			}
			if !hasELF {
				return []string{}, nil
			}
		}
		return nil, fmt.Errorf("read linkage metadata for %s: %w", installedCPV, err)
	}
	provided := make(map[string][]string)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ";")
		if len(fields) >= 3 && fields[2] != "" {
			provided[fields[2]] = append(provided[fields[2]], filepath.Clean(fields[1]))
		}
	}
	if len(provided) == 0 {
		return []string{}, nil
	}
	entries, err := os.ReadDir(vdbRoot)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	for _, category := range entries {
		if !category.IsDir() {
			continue
		}
		packages, err := os.ReadDir(filepath.Join(vdbRoot, category.Name()))
		if err != nil {
			continue
		}
		for _, pkg := range packages {
			if !pkg.IsDir() {
				continue
			}
			cpv := category.Name() + "/" + pkg.Name()
			if cpv == installedCPV {
				continue
			}
			consumerData, err := os.ReadFile(filepath.Join(vdbRoot, category.Name(), pkg.Name(), "NEEDED.ELF.2"))
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(consumerData), "\n") {
				fields := strings.Split(line, ";")
				if len(fields) < 5 {
					continue
				}
				for _, needed := range strings.Split(fields[4], ",") {
					if providerReachableFromRunpath(fields[1], fields[3], provided[needed]) {
						seen[cpv] = true
					}
				}
			}
		}
	}
	consumers := make([]string, 0, len(seen))
	for cpv := range seen {
		consumers = append(consumers, cpv)
	}
	sort.Strings(consumers)
	return consumers, nil
}

func packageOwnsELFWithoutLinkageMetadata(vdbRoot, installedCPV string) (bool, error) {
	contentsPath := filepath.Join(vdbRoot, filepath.FromSlash(installedCPV), "CONTENTS")
	data, err := os.ReadFile(contentsPath)
	if err != nil {
		return false, err
	}
	root := filepath.Clean(strings.TrimSuffix(filepath.Clean(vdbRoot), filepath.Join("var", "db", "pkg")))
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "obj" {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(fields[1], "/")))
		file, err := os.Open(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		var magic [4]byte
		count, readErr := io.ReadFull(file, magic[:])
		closeErr := file.Close()
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return false, readErr
		}
		if closeErr != nil {
			return false, closeErr
		}
		if count == len(magic) && magic == [4]byte{0x7f, 'E', 'L', 'F'} {
			return true, nil
		}
	}
	return false, nil
}

func providerReachableFromRunpath(binary, runpath string, providerPaths []string) bool {
	if len(providerPaths) == 0 {
		return false
	}
	if strings.TrimSpace(runpath) == "" {
		return true
	}
	origin := filepath.Dir(binary)
	for _, raw := range strings.Split(runpath, ":") {
		raw = strings.ReplaceAll(raw, "${ORIGIN}", origin)
		raw = strings.ReplaceAll(raw, "$ORIGIN", origin)
		if !filepath.IsAbs(raw) {
			raw = filepath.Join(origin, raw)
		}
		directory := filepath.Clean(raw)
		for _, providerPath := range providerPaths {
			if filepath.Dir(filepath.Clean(providerPath)) == directory {
				return true
			}
		}
	}
	// RUNPATH is searched before the loader's default directories, which remain
	// eligible as a fallback. Version-private providers outside the encoded
	// RUNPATH are not reachable.
	for _, providerPath := range providerPaths {
		switch filepath.Dir(filepath.Clean(providerPath)) {
		case "/lib", "/lib64", "/usr/lib", "/usr/lib64":
			return true
		}
	}
	return false
}

// ReverseELFRemovalClosure expands a provider through all installed ELF
// consumers and returns a deterministic consumer-first removal order. Every
// prefix of the result is linkage-safe relative to the remaining packages.
func ReverseELFRemovalClosure(vdbRoot, seed string) ([]string, error) {
	closure := map[string]bool{seed: true}
	for changed := true; changed; {
		changed = false
		current := make([]string, 0, len(closure))
		for cpv := range closure {
			current = append(current, cpv)
		}
		sort.Strings(current)
		for _, provider := range current {
			consumers, err := ReverseELFConsumers(vdbRoot, provider)
			if err != nil {
				return nil, err
			}
			for _, consumer := range consumers {
				if !closure[consumer] {
					closure[consumer], changed = true, true
				}
			}
		}
	}
	consumerEdges := make(map[string][]string, len(closure))
	for provider := range closure {
		consumers, err := ReverseELFConsumers(vdbRoot, provider)
		if err != nil {
			return nil, err
		}
		for _, consumer := range consumers {
			if closure[consumer] {
				consumerEdges[provider] = append(consumerEdges[provider], consumer)
			}
		}
	}
	remaining := make(map[string]bool, len(closure))
	for cpv := range closure {
		remaining[cpv] = true
	}
	order := make([]string, 0, len(closure))
	for len(remaining) > 0 {
		var leaves []string
		for provider := range remaining {
			hasConsumer := false
			for _, consumer := range consumerEdges[provider] {
				if remaining[consumer] {
					hasConsumer = true
					break
				}
			}
			if !hasConsumer {
				leaves = append(leaves, provider)
			}
		}
		if len(leaves) == 0 {
			return nil, fmt.Errorf("reverse ELF removal closure contains a cycle")
		}
		sort.Strings(leaves)
		order = append(order, leaves...)
		for _, leaf := range leaves {
			delete(remaining, leaf)
		}
	}
	return order, nil
}

// ---------------------------------------------------------------------------
// RebuildNeeded
// ---------------------------------------------------------------------------

// RebuildNeeded returns installed packages that consume libraries recorded in
// Portage's preserve-libs registry. General missing-library detection belongs
// to RevdepRebuild and must not expand @preserved-rebuild.
func RebuildNeeded(root, vdbRoot string) ([]string, error) {
	reasons, err := RebuildReasons(root, vdbRoot)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var atoms []string
	for _, reason := range reasons {
		if !seen[reason.Package] {
			seen[reason.Package] = true
			atoms = append(atoms, reason.Package)
		}
	}
	sort.Strings(atoms)
	return atoms, nil
}

// RebuildReasons returns deterministic, actionable evidence for every package
// selected by RebuildNeeded.
func RebuildReasons(root, vdbRoot string) ([]RebuildReason, error) {
	preserved, err := ScanPreservedLibs(root)
	if err != nil {
		return nil, fmt.Errorf("could not scan for preserved libraries: %w", err)
	}

	seen := make(map[string]bool)
	var reasons []RebuildReason
	add := func(reason RebuildReason) {
		key := reason.Kind + "\x00" + reason.Package + "\x00" + reason.Binary + "\x00" + reason.Needed + "\x00" + reason.PreservedPath
		if !seen[key] {
			seen[key] = true
			reasons = append(reasons, reason)
		}
	}

	// Rebuild consumers of preserved libraries. The package recorded as owner
	// supplied the old object and is not itself evidence of a broken consumer.
	if len(preserved) > 0 {
		contentsMap, cerr := vdbContentsMap(vdbRoot)
		if cerr != nil {
			return nil, fmt.Errorf("could not read installed package database contents: %w", cerr)
		}
		preservedPaths := make(map[string]bool)
		preservedBySONAME := make(map[string][]PreservedLib)
		for _, pl := range preserved {
			preservedPaths[pl.Path] = true
			preservedBySONAME[pl.Soname] = append(preservedBySONAME[pl.Soname], pl)
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
				for _, pl := range preservedBySONAME[n] {
					add(RebuildReason{Package: pkgKey, Binary: fullPath, Needed: n, Kind: "preserved-library", PreservedPath: pl.Path, PreservedOwner: pl.OwningPkg})
				}
			}
		}
	}

	sort.Slice(reasons, func(i, j int) bool {
		a, b := reasons[i], reasons[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Binary != b.Binary {
			return a.Binary < b.Binary
		}
		if a.Needed != b.Needed {
			return a.Needed < b.Needed
		}
		return a.PreservedPath < b.PreservedPath
	})
	return reasons, nil
}

// ---------------------------------------------------------------------------
// RevdepRebuild
// ---------------------------------------------------------------------------

// RevdepRebuild performs a full reverse dependency scan, checking all
// installed packages for broken shared library links.
func RevdepRebuild(root, vdbRoot string) ([]string, error) {
	categories, err := os.ReadDir(vdbRoot)
	if err != nil {
		return nil, fmt.Errorf("could not read installed package database: %w", err)
	}

	needRebuild := make(map[string]bool)
	// Portage's linkage consistency scan matches installed providers by ELF
	// class/ABI and SONAME.  It does not reject a provider merely because the
	// consumer's recorded RUNPATH would not find it (ldd can therefore be
	// stricter than revdep-rebuild for private toolchain layouts).
	providers := make(map[string]bool)
	for _, category := range categories {
		if !category.IsDir() {
			continue
		}
		packages, _ := os.ReadDir(filepath.Join(vdbRoot, category.Name()))
		for _, pkg := range packages {
			if !pkg.IsDir() {
				continue
			}
			metadata, readErr := os.ReadFile(filepath.Join(vdbRoot, category.Name(), pkg.Name(), "NEEDED.ELF.2"))
			if readErr != nil {
				continue
			}
			for _, line := range strings.Split(string(metadata), "\n") {
				fields := strings.Split(line, ";")
				if len(fields) < 5 || strings.TrimSpace(fields[2]) == "" {
					continue
				}
				payload := filepath.Join(root, strings.TrimPrefix(filepath.Clean(fields[1]), string(filepath.Separator)))
				if info, statErr := os.Stat(payload); statErr == nil && !info.IsDir() {
					providers[fields[0]+"\x00"+strings.TrimSpace(fields[2])] = true
				}
			}
		}
	}
	loaderNames := loaderLibraryNames(root)
	for _, category := range categories {
		if !category.IsDir() {
			continue
		}
		packages, _ := os.ReadDir(filepath.Join(vdbRoot, category.Name()))
		for _, pkg := range packages {
			if !pkg.IsDir() {
				continue
			}
			metadata, readErr := os.ReadFile(filepath.Join(vdbRoot, category.Name(), pkg.Name(), "NEEDED.ELF.2"))
			if readErr != nil {
				continue
			}
			broken := false
			for _, line := range strings.Split(string(metadata), "\n") {
				fields := strings.Split(line, ";")
				if len(fields) < 5 {
					continue
				}
				binaryPath := filepath.Join(root, strings.TrimPrefix(filepath.Clean(fields[1]), string(filepath.Separator)))
				if info, statErr := os.Stat(binaryPath); statErr != nil || info.IsDir() {
					// Stale VDB linkage rows for payload paths that no longer
					// exist cannot represent a currently broken executable.
					continue
				}
				for _, library := range strings.Split(fields[4], ",") {
					library = strings.TrimSpace(library)
					if library != "" && !providers[fields[0]+"\x00"+library] && !loaderNames[library] {
						needRebuild[category.Name()+"/"+pkg.Name()] = true
						broken = true
						break
					}
				}
				if broken {
					break
				}
			}
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
