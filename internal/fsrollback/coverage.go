package fsrollback

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type Coverage struct {
	Required   []string `json:"required"`
	Boundaries []Mount  `json:"boundaries"`
	Excluded   []Mount  `json:"excluded,omitempty"`
}

// EvaluateCoverage resolves every required persistent path to its longest
// containing mount boundary and reports every nested mount below a required
// directory. Callers must reject any excluded boundary before mutation.
func EvaluateCoverage(mounts []Mount, required []string, supported func(Mount) bool) (Coverage, error) {
	if len(required) == 0 {
		return Coverage{}, fmt.Errorf("at least one required path is required")
	}
	normalized, err := normalizeMounts(mounts)
	if err != nil {
		return Coverage{}, err
	}
	result := Coverage{}
	boundaries := make(map[string]Mount)
	excluded := make(map[string]Mount)
	seenRequired := make(map[string]bool)
	for _, raw := range required {
		path, err := cleanAbsolute(raw)
		if err != nil {
			return Coverage{}, fmt.Errorf("required path %q: %w", raw, err)
		}
		if !seenRequired[path] {
			result.Required = append(result.Required, path)
			seenRequired[path] = true
		}
		mount, ok := containingMount(normalized, path)
		if !ok {
			return Coverage{}, fmt.Errorf("required path %q has no mount boundary", path)
		}
		if supported == nil || !supported(mount) {
			excluded[mount.Path] = mount
		} else {
			boundaries[mount.Path] = mount
		}
		for _, child := range normalized {
			if child.Path == mount.Path || !pathWithin(path, child.Path) {
				continue
			}
			if supported == nil || !supported(child) {
				excluded[child.Path] = child
			} else {
				boundaries[child.Path] = child
			}
		}
	}
	sort.Strings(result.Required)
	result.Boundaries = sortedMountValues(boundaries)
	result.Excluded = sortedMountValues(excluded)
	return result, nil
}

func (c Coverage) Eligible() bool {
	return len(c.Required) > 0 && len(c.Boundaries) > 0 && len(c.Excluded) == 0
}

func normalizeMounts(mounts []Mount) ([]Mount, error) {
	if len(mounts) == 0 {
		return nil, fmt.Errorf("mount topology is empty")
	}
	result := make([]Mount, 0, len(mounts))
	seen := make(map[string]bool)
	for _, mount := range mounts {
		path, err := cleanAbsolute(mount.Path)
		if err != nil {
			return nil, fmt.Errorf("mount path %q: %w", mount.Path, err)
		}
		mount.Path = path
		if err := validateMount(mount); err != nil {
			return nil, err
		}
		if seen[path] {
			return nil, fmt.Errorf("duplicate mount boundary %q", path)
		}
		seen[path] = true
		result = append(result, mount)
	}
	sort.Slice(result, func(i, j int) bool {
		if len(result[i].Path) != len(result[j].Path) {
			return len(result[i].Path) > len(result[j].Path)
		}
		return result[i].Path < result[j].Path
	})
	return result, nil
}

func validateMount(mount Mount) error {
	if _, err := cleanAbsolute(mount.Path); err != nil {
		return fmt.Errorf("invalid mount path %q: %w", mount.Path, err)
	}
	if strings.TrimSpace(mount.Source) == "" {
		return fmt.Errorf("mount %q has no source", mount.Path)
	}
	if strings.TrimSpace(mount.Filesystem) == "" {
		return fmt.Errorf("mount %q has no filesystem type", mount.Path)
	}
	if strings.TrimSpace(mount.StableID) == "" {
		return fmt.Errorf("mount %q has no stable identity", mount.Path)
	}
	return nil
}

func cleanAbsolute(path string) (string, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be absolute")
	}
	clean := filepath.Clean(path)
	if clean == "." {
		return "", fmt.Errorf("path must be absolute")
	}
	return clean, nil
}

func containingMount(mounts []Mount, path string) (Mount, bool) {
	for _, mount := range mounts {
		if pathWithin(mount.Path, path) {
			return mount, true
		}
	}
	return Mount{}, false
}

func pathWithin(parent, child string) bool {
	if parent == "/" {
		return filepath.IsAbs(child)
	}
	return child == parent || strings.HasPrefix(child, parent+string(filepath.Separator))
}

func sortedMountValues(values map[string]Mount) []Mount {
	result := make([]Mount, 0, len(values))
	for _, mount := range values {
		result = append(result, mount)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}
