package binpkg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// installedCaptureEntries follows ownership of protected configuration through
// dispatch-conf: CONTENTS may name a staged update that has since been consumed.
// Capture the current configuration as well as any still-pending update, without
// changing the original VDB evidence or ignoring missing ordinary payloads.
func installedCaptureEntries(root string, entries []contentEntry) ([]contentEntry, error) {
	recorded := make(map[string]bool, len(entries))
	for _, entry := range entries {
		archivePath, _, err := installedContentPath(root, entry.Path)
		if err != nil {
			return nil, fmt.Errorf("binpkg: invalid installed path %q: %w", entry.Path, err)
		}
		if recorded[archivePath] {
			return nil, fmt.Errorf("binpkg: duplicate installed path %q", entry.Path)
		}
		recorded[archivePath] = true
	}
	result := make([]contentEntry, 0, len(entries))
	added := make(map[string]bool)
	for _, entry := range entries {
		current, ok := configUpdateTarget(entry.Path)
		if entry.Type != "obj" || !ok {
			result = append(result, entry)
			continue
		}
		_, pendingPath, err := installedContentPath(root, entry.Path)
		if err != nil {
			return nil, err
		}
		if err := rejectSymlinkParents(root, pendingPath); err != nil {
			return nil, fmt.Errorf("binpkg: cannot capture installed path %s: %w", entry.Path, err)
		}
		_, pendingErr := os.Lstat(pendingPath)
		if pendingErr != nil && !os.IsNotExist(pendingErr) {
			return nil, fmt.Errorf("binpkg: inspect config update %s: %w", entry.Path, pendingErr)
		}
		if pendingErr == nil {
			result = append(result, entry)
		}
		archivePath, currentPath, err := installedContentPath(root, current)
		if err != nil {
			return nil, err
		}
		if err := rejectSymlinkParents(root, currentPath); err != nil {
			return nil, fmt.Errorf("binpkg: cannot capture current config %s: %w", current, err)
		}
		if _, err := os.Lstat(currentPath); err != nil {
			if os.IsNotExist(err) && pendingErr == nil {
				continue
			}
			return nil, fmt.Errorf("binpkg: capture current config %s for update %s: %w", current, entry.Path, err)
		}
		if !recorded[archivePath] && !added[archivePath] {
			entry.Path = current
			result = append(result, entry)
			added[archivePath] = true
		}
	}
	return result, nil
}

func configUpdateTarget(path string) (string, bool) {
	name := filepath.Base(path)
	if len(name) <= 10 || !strings.HasPrefix(name, "._cfg") || name[9] != '_' {
		return "", false
	}
	for _, digit := range name[5:9] {
		if digit < '0' || digit > '9' {
			return "", false
		}
	}
	if name[10:] == "." || name[10:] == ".." {
		return "", false
	}
	return filepath.Join(filepath.Dir(path), name[10:]), true
}
