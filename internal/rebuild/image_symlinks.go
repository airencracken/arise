package rebuild

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Reject references to the private source tree while it still exists. Testing
// target existence would miss the defect until the successful build is cleaned.
func checkWorkDirSymlinks(image, workDir string) error {
	work, err := filepath.Abs(workDir)
	if err != nil {
		return err
	}
	return filepath.WalkDir(image, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.Type()&os.ModeSymlink == 0 {
			return err
		}
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		name, err := filepath.Rel(image, path)
		if err != nil {
			return err
		}
		resolved := filepath.Clean(target)
		if !filepath.IsAbs(target) {
			resolved = filepath.Join("/", filepath.Dir(name), target)
		}
		if resolved == work || strings.HasPrefix(resolved, work+"/") {
			return fmt.Errorf("rebuild: symlink /%s -> %s refers to the temporary build directory", name, target)
		}
		return nil
	})
}
