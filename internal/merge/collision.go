package merge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/airencracken/arise/internal/metadata"
)

// Exclusions name exact VDB directories replaced by this transaction.
func CheckCollisions(destDir, vdbRoot string, replacedVDBPaths []string) ([]string, error) {
	exclude := make(map[string]bool, len(replacedVDBPaths))
	for _, path := range replacedVDBPaths {
		exclude[filepath.Clean(path)] = true
	}

	destFiles, err := gatherDestFiles(destDir)
	if err != nil {
		return nil, fmt.Errorf("collision: could not scan %s for file conflicts: %w", destDir, err)
	}

	vdbOwners, err := buildVDBOwners(vdbRoot)
	if err != nil {
		return nil, fmt.Errorf("collision: could not read installed package database: %w", err)
	}

	var collisions []string

	for _, df := range destFiles {
		for _, ownerPkg := range vdbOwners[df] {
			if exclude[filepath.Clean(ownerPkg)] {
				continue
			}
			collisions = append(collisions, fmt.Sprintf(
				"file %s already owned by package %s", df, pkgDirToCP(vdbRoot, ownerPkg),
			))
		}
	}

	crossCols := detectCrossCollisions(destFiles, vdbOwners)
	collisions = append(collisions, crossCols...)

	return collisions, nil
}

func DetectFileCollision(targetPath, vdbRoot, owner string) (string, bool) {
	vdbOwners, err := buildVDBOwners(vdbRoot)
	if err != nil {
		return fmt.Sprintf("could not inspect file ownership: %v", err), true
	}
	for _, owningPkg := range vdbOwners[filepath.Clean(targetPath)] {
		if filepath.Clean(owningPkg) != filepath.Join(vdbRoot, filepath.FromSlash(owner)) {
			return fmt.Sprintf("file %s already owned by package %s", targetPath, pkgDirToCP(vdbRoot, owningPkg)), true
		}
	}
	return "", false
}

func gatherDestFiles(destDir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(destDir, func(srcPath string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(destDir, srcPath)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		files = append(files, filepath.Join("/", rel))
		return nil
	})
	return files, err
}

func buildVDBOwners(vdbRoot string) (map[string][]string, error) {
	owners := make(map[string][]string)

	categories, err := os.ReadDir(vdbRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return owners, nil
		}
		return nil, err
	}

	for _, catEntry := range categories {
		if !catEntry.IsDir() {
			continue
		}
		catPath := filepath.Join(vdbRoot, catEntry.Name())
		pkgs, err := os.ReadDir(catPath)
		if err != nil {
			return nil, fmt.Errorf("read category %s: %w", catPath, err)
		}
		for _, pkgEntry := range pkgs {
			if !pkgEntry.IsDir() {
				continue
			}
			pkgDir := filepath.Join(catPath, pkgEntry.Name())
			contentsPath := filepath.Join(pkgDir, "CONTENTS")
			data, err := os.ReadFile(contentsPath)
			if err != nil {
				return nil, fmt.Errorf("read ownership %s: %w", contentsPath, err)
			}
			entries, err := parseContents(string(data))
			if err != nil {
				return nil, fmt.Errorf("parse ownership %s: %w", contentsPath, err)
			}
			for _, e := range entries {
				if e.Type == "dir" {
					continue
				}
				path := filepath.Clean(e.Path)
				owners[path] = append(owners[path], pkgDir)
			}
		}
	}

	return owners, nil
}

func detectCrossCollisions(destFiles []string, vdbOwners map[string][]string) []string {
	counts := make(map[string]int)
	for _, f := range destFiles {
		if _, ok := vdbOwners[f]; ok {
			continue
		}
		counts[f]++
	}

	var collisions []string
	for f, count := range counts {
		if count > 1 {
			collisions = append(collisions, fmt.Sprintf(
				"file %s would be installed by multiple packages", f,
			))
		}
	}
	return collisions
}

func pkgDirToCP(vdbRoot, pkgDir string) string {
	rel, err := filepath.Rel(vdbRoot, pkgDir)
	if err == nil {
		category, pkg, _, parseErr := metadata.ParseCPV(filepath.ToSlash(rel))
		if parseErr == nil {
			return category + "/" + pkg
		}
	}

	// Keep malformed or non-relative paths diagnosable. Valid VDB CPVs always
	// take the parser path above, including revisions and hyphenated package
	// names that cannot be split correctly at the final hyphen.
	dirname := filepath.Dir(pkgDir)
	base := filepath.Base(pkgDir)
	category := filepath.Base(dirname)
	if category == "." || category == string(filepath.Separator) {
		return strings.ReplaceAll(rel, string(os.PathSeparator), "/")
	}
	return category + "/" + base
}
