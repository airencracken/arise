package merge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func CheckCollisions(destDir, vdbRoot string, excludeCPs []string) ([]string, error) {
	exclude := make(map[string]bool, len(excludeCPs))
	for _, cp := range excludeCPs {
		exclude[cp] = true
	}

	destFiles, err := gatherDestFiles(destDir)
	if err != nil {
		return nil, fmt.Errorf("collision: walk dest %s: %w", destDir, err)
	}

	vdbOwners, err := buildVDBOwners(vdbRoot)
	if err != nil {
		return nil, fmt.Errorf("collision: read vdb: %w", err)
	}

	var collisions []string

	for _, df := range destFiles {
		ownerPkg, owned := vdbOwners[df]
		if !owned {
			continue
		}
		ownerCP := pkgDirToCP(vdbRoot, ownerPkg)
		if exclude[ownerCP] {
			continue
		}
		collisions = append(collisions, fmt.Sprintf(
			"file %s already owned by package %s", df, ownerCP,
		))
	}

	crossCols := detectCrossCollisions(destFiles, vdbOwners)
	collisions = append(collisions, crossCols...)

	return collisions, nil
}

func DetectFileCollision(targetPath, vdbRoot, owner string) (string, bool) {
	vdbOwners, err := buildVDBOwners(vdbRoot)
	if err != nil {
		return "", false
	}
	owningPkg, owned := vdbOwners[targetPath]
	if !owned {
		return "", false
	}
	ownerCP := pkgDirToCP(vdbRoot, owningPkg)
	// compare category/package only (strip version from owner param)
	ownerCPOnly := owner
	if idx := strings.LastIndex(owner, "/"); idx >= 0 {
		rest := owner[idx+1:]
		if vIdx := strings.LastIndex(rest, "-"); vIdx >= 0 {
			ownerCPOnly = owner[:idx+1+vIdx]
		}
	}
	if ownerCP == owner || ownerCP == ownerCPOnly || ownerCP == "" {
		return "", false
	}
	return fmt.Sprintf("file %s already owned by package %s", targetPath, ownerCP), true
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

func buildVDBOwners(vdbRoot string) (map[string]string, error) {
	owners := make(map[string]string)

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
			continue
		}
		for _, pkgEntry := range pkgs {
			if !pkgEntry.IsDir() {
				continue
			}
			pkgDir := filepath.Join(catPath, pkgEntry.Name())
			contentsPath := filepath.Join(pkgDir, "CONTENTS")
			data, err := os.ReadFile(contentsPath)
			if err != nil {
				continue
			}
			entries, err := parseContents(string(data))
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.Type == "dir" {
					continue
				}
				owners[e.Path] = pkgDir
			}
		}
	}

	return owners, nil
}

func detectCrossCollisions(destFiles []string, vdbOwners map[string]string) []string {
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
	if err != nil {
		dirname := filepath.Dir(pkgDir)
		base := filepath.Base(pkgDir)
		cat := filepath.Base(dirname)
		return cat + "/" + base
	}
	// VDB paths are category/package-version, convert to category/package
	parts := strings.SplitN(rel, string(os.PathSeparator), 2)
	if len(parts) != 2 {
		return strings.ReplaceAll(rel, string(os.PathSeparator), "/")
	}
	cat := parts[0]
	pkgVer := parts[1]
	idx := strings.LastIndex(pkgVer, "-")
	if idx < 0 {
		return cat + "/" + pkgVer
	}
	return cat + "/" + pkgVer[:idx]
}
