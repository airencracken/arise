// Package virtualquery expands legacy GLEP 37 virtual mappings from the
// effective Portage profile stack.
package virtualquery

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/portage"
)

// Expand returns the real provider atoms for a legacy virtual. A virtual with
// no mapping is retained so callers can distinguish it from an empty mapping.
func Expand(cfg *portage.Config, query string) ([]string, error) {
	parsed, err := atom.ParsePackageAtom(query)
	if err != nil {
		return nil, fmt.Errorf("virtual query: parse atom: %w", err)
	}
	if parsed.Category != "virtual" {
		return []string{query}, nil
	}
	if cfg == nil {
		return []string{query}, nil
	}
	directories := append([]string(nil), cfg.ProfileParents...)
	if cfg.ProfilePath != "" {
		directories = append(directories, cfg.ProfilePath)
	}
	providers := make(map[string]bool)
	for _, directory := range directories {
		lines, err := portage.ReadConfigFile(filepath.Join(directory, "virtuals"))
		if err != nil {
			continue
		}
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) < 2 || fields[0] != parsed.Key() {
				continue
			}
			for _, provider := range fields[1:] {
				if strings.HasPrefix(provider, "!") {
					continue
				}
				if strings.HasPrefix(provider, "-") {
					delete(providers, strings.TrimPrefix(provider, "-"))
					continue
				}
				if _, err := atom.ParsePackageAtom(provider); err == nil {
					providers[provider] = true
				}
			}
		}
	}
	if len(providers) == 0 {
		return []string{query}, nil
	}
	result := make([]string, 0, len(providers))
	for provider := range providers {
		result = append(result, provider)
	}
	sort.Strings(result)
	return result, nil
}
