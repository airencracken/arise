// Package configprotect evaluates Portage CONFIG_PROTECT path policy without
// importing Portage or mutating protected files.
package configprotect

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Protected reports whether an absolute path beneath root is protected after
// applying CONFIG_PROTECT_MASK.
func Protected(root, path string, protect, mask []string) (bool, error) {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return false, fmt.Errorf("config protect: path must be absolute: %q", path)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false, fmt.Errorf("config protect: path %q is outside root %q", path, root)
	}
	canonical := filepath.Join(string(filepath.Separator), relative)
	matches := func(entries []string) bool {
		for _, entry := range entries {
			entry = filepath.Clean(filepath.Join(string(filepath.Separator), strings.TrimSpace(entry)))
			if canonical == entry || strings.HasPrefix(canonical, entry+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	return matches(protect) && !matches(mask), nil
}
