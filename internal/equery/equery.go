package equery

import (
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/dgraph-io/badger/v4"
)

type contentEntry struct {
	typ   string
	path  string
	md5   string
	mtime int64
}

func parseContentsLine(line string) (contentEntry, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return contentEntry{}, false
	}

	fields := strings.Fields(line)
	if len(fields) < 2 {
		return contentEntry{}, false
	}

	entry := contentEntry{
		typ:  fields[0],
		path: fields[1],
	}

	if entry.typ == "obj" && len(fields) >= 4 {
		entry.md5 = fields[2]
		if ts, err := strconv.ParseInt(fields[3], 10, 64); err == nil {
			entry.mtime = ts
		}
	}

	return entry, true
}

func findBestInstalledVersion(vdbPath, category, pkg string) (string, error) {
	catDir := filepath.Join(vdbPath, category)
	entries, err := os.ReadDir(catDir)
	if err != nil {
		return "", fmt.Errorf("could not read package category directory %s: %w", catDir, err)
	}

	prefix := pkg + "-"
	var candidates []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, prefix) {
			candidates = append(candidates, name)
		}
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf("no installed version found for %s/%s -- the package may need to be installed first", category, pkg)
	}

	sort.Slice(candidates, func(i, j int) bool {
		vi := extractVersion(candidates[i])
		vj := extractVersion(candidates[j])
		pi, _ := atom.ParseVersion(vi)
		pj, _ := atom.ParseVersion(vj)
		return pi.Compare(pj) < 0
	})

	return candidates[len(candidates)-1], nil
}

func extractVersion(name string) string {
	dashIdx := strings.Index(name, "-")
	if dashIdx < 0 {
		return name
	}
	return name[dashIdx+1:]
}

func Belongs(vdbPath string, filePath string) (string, error) {
	categories, err := os.ReadDir(vdbPath)
	if err != nil {
		return "", fmt.Errorf("could not read installed package database at %s: %w", vdbPath, err)
	}

	for _, catEntry := range categories {
		if !catEntry.IsDir() {
			continue
		}
		category := catEntry.Name()

		pkgs, err := os.ReadDir(filepath.Join(vdbPath, category))
		if err != nil {
			continue
		}

		for _, pkgEntry := range pkgs {
			if !pkgEntry.IsDir() {
				continue
			}
			pv := pkgEntry.Name()

			contentsPath := filepath.Join(vdbPath, category, pv, "CONTENTS")
			data, err := os.ReadFile(contentsPath)
			if err != nil {
				continue
			}

			for _, line := range strings.Split(string(data), "\n") {
				entry, ok := parseContentsLine(line)
				if !ok {
					continue
				}
				if entry.path == "" {
					continue
				}

				if entry.path == filePath {
					return category + "/" + pv, nil
				}

				if filepath.Base(entry.path) == filePath {
					return category + "/" + pv, nil
				}

				if strings.HasSuffix(filePath, entry.path) || strings.HasSuffix(entry.path, filePath) {
					return category + "/" + pv, nil
				}
			}
		}
	}

		return "", fmt.Errorf("no installed package owns the file %q", filePath)
}

func Files(vdbPath string, atomStr string) ([]string, error) {
	a, err := atom.Parse(atomStr)
	if err != nil {
		return nil, fmt.Errorf("could not parse package name %q: %w", atomStr, err)
	}

	category := a.Category
	pkg := a.Package

	var pvDir string
	if a.Version != nil && a.Version.Raw != "" {
		pvDir = pkg + "-" + a.Version.Raw
	} else {
		best, err := findBestInstalledVersion(vdbPath, category, pkg)
		if err != nil {
			return nil, err
		}
		pvDir = best
	}

	contentsPath := filepath.Join(vdbPath, category, pvDir, "CONTENTS")
	data, err := os.ReadFile(contentsPath)
	if err != nil {
		return nil, fmt.Errorf("could not read file list for %s/%s: %w", category, pvDir, err)
	}

	var files []string
	for _, line := range strings.Split(string(data), "\n") {
		entry, ok := parseContentsLine(line)
		if !ok {
			continue
		}
		if entry.typ == "obj" && entry.path != "" {
			files = append(files, entry.path)
		}
	}

	return files, nil
}

func Uses(db *badger.DB, vdbPath string, atomStr string) (string, string, error) {
	a, err := atom.Parse(atomStr)
	if err != nil {
		return "", "", fmt.Errorf("could not parse package name %q: %w", atomStr, err)
	}

	category := a.Category
	pkg := a.Package

	var pvDir string
	if a.Version != nil && a.Version.Raw != "" {
		pvDir = pkg + "-" + a.Version.Raw
	} else {
		best, err := findBestInstalledVersion(vdbPath, category, pkg)
		if err != nil {
			return "", "", err
		}
		pvDir = best
	}

	var iuse string
	if db != nil {
		m, err := ingest.Query(db, a.Key())
		if err == nil && m != nil {
			iuse = m.IUSE
		}
	}

	if iuse == "" {
		iusePath := filepath.Join(vdbPath, category, pvDir, "IUSE")
		if data, err := os.ReadFile(iusePath); err == nil {
			iuse = strings.TrimSpace(string(data))
		}
	}

	usePath := filepath.Join(vdbPath, category, pvDir, "USE")
	var activeUse string
	if data, err := os.ReadFile(usePath); err == nil {
		activeUse = strings.TrimSpace(string(data))
	}

	return iuse, activeUse, nil
}

func Size(vdbPath string, atomStr string) (int64, error) {
	files, err := Files(vdbPath, atomStr)
	if err != nil {
		return 0, err
	}

	var total int64
	for _, f := range files {
		info, err := os.Lstat(f)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if info.IsDir() {
			continue
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
	}

	return total, nil
}

func Check(vdbPath string, atomStr string) ([]string, error) {
	a, err := atom.Parse(atomStr)
	if err != nil {
		return nil, fmt.Errorf("could not parse package name %q: %w", atomStr, err)
	}

	category := a.Category
	pkg := a.Package

	var pvDir string
	if a.Version != nil && a.Version.Raw != "" {
		pvDir = pkg + "-" + a.Version.Raw
	} else {
		best, err := findBestInstalledVersion(vdbPath, category, pkg)
		if err != nil {
			return nil, err
		}
		pvDir = best
	}

	contentsPath := filepath.Join(vdbPath, category, pvDir, "CONTENTS")
	data, err := os.ReadFile(contentsPath)
	if err != nil {
		return nil, fmt.Errorf("could not read file list for %s/%s: %w", category, pvDir, err)
	}

	var mismatches []string
	for _, line := range strings.Split(string(data), "\n") {
		entry, ok := parseContentsLine(line)
		if !ok {
			continue
		}
		if entry.typ != "obj" || entry.path == "" {
			continue
		}

		info, err := os.Lstat(entry.path)
		if err != nil {
			mismatches = append(mismatches, entry.path+": file missing")
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}

		mtimeOK := entry.mtime <= 0 || info.ModTime().Unix() == entry.mtime

		if !mtimeOK {
			mismatches = append(mismatches, entry.path+": mtime mismatch")
		}

		if entry.md5 != "" {
			computed, err := computeMD5(entry.path)
			if err != nil {
				mismatches = append(mismatches, entry.path+": cannot hash: "+err.Error())
			} else if computed != entry.md5 {
				mismatches = append(mismatches, entry.path+": checksum mismatch")
			}
		}
	}

	return mismatches, nil
}

func computeMD5(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			/* Best effort */
		}
	}()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func Which(repoDir string, atomStr string) (string, error) {
	a, err := atom.Parse(atomStr)
	if err != nil {
		return "", fmt.Errorf("parsing atom %q: %w", atomStr, err)
	}

	ebuildDir := filepath.Join(repoDir, a.Category, a.Package)

	if a.Version != nil && a.Version.Raw != "" {
		ebuildPath := filepath.Join(ebuildDir, a.Package+"-"+a.Version.Raw+".ebuild")
		if _, err := os.Stat(ebuildPath); err != nil {
			return "", fmt.Errorf("build recipe not found: %s", ebuildPath)
		}
		return ebuildPath, nil
	}

	pattern := filepath.Join(ebuildDir, a.Package+"-*.ebuild")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("could not match build recipe pattern %s: %w", pattern, err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no build recipes found for %s", a.CP())
	}

	sort.Slice(matches, func(i, j int) bool {
		vi := versionFromEbuildPath(matches[i])
		vj := versionFromEbuildPath(matches[j])
		pi, _ := atom.ParseVersion(vi)
		pj, _ := atom.ParseVersion(vj)
		return pi.Compare(pj) < 0
	})

	return matches[len(matches)-1], nil
}

func versionFromEbuildPath(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, ".ebuild")
	dashIdx := strings.Index(base, "-")
	if dashIdx < 0 {
		return base
	}
	return base[dashIdx+1:]
}

func List(vdbPath string, pattern string) ([]string, error) {
	var results []string

	categories, err := os.ReadDir(vdbPath)
	if err != nil {
		return nil, fmt.Errorf("reading vdb dir %s: %w", vdbPath, err)
	}

	for _, catEntry := range categories {
		if !catEntry.IsDir() {
			continue
		}
		category := catEntry.Name()

		entries, err := os.ReadDir(filepath.Join(vdbPath, category))
		if err != nil {
			continue
		}

		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()

			if strings.Contains(name, "-") {
				cpv := category + "/" + name
				if pattern == "" || strings.Contains(cpv, pattern) {
					results = append(results, cpv)
				}
			} else {
				pkgEntries, err := os.ReadDir(filepath.Join(vdbPath, category, name))
				if err != nil {
					continue
				}
				for _, pe := range pkgEntries {
					if !pe.IsDir() {
						continue
					}
					cpv := category + "/" + pe.Name()
					if pattern == "" || strings.Contains(cpv, pattern) {
						results = append(results, cpv)
					}
				}
			}
		}
	}

	sort.Strings(results)
	return results, nil
}
