package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ProfileInfo struct {
	Path         string              // path to the active profile
	Parents      []string            // ordered list of parent paths
	MakeDefaults map[string]string   // merged make.defaults variables
	SystemSet    []string            // @system packages (merged from all parents)
	UseForce     []string            // forced USE flags
	UseMask      []string            // masked USE flags
	PkgUseForce  map[string][]string // package.use.force (per-package forced USE)
	PkgUseMask   map[string][]string // package.use.mask (per-package masked USE)
}

func LoadProfile(profileSymlink string, profilesRoot string) (*ProfileInfo, error) {
	target, err := os.Readlink(profileSymlink)
	if err != nil {
		return nil, fmt.Errorf("profile: readlink %s: %w", profileSymlink, err)
	}

	profilePath := target
	if !filepath.IsAbs(profilePath) {
		profilePath = filepath.Join(filepath.Dir(profileSymlink), target)
	}

	return MergeParents(profilePath, profilesRoot)
}

func ResolveParent(profilePath string, parentLine string, profilesRoot string) (string, error) {
	parentLine = strings.TrimSpace(parentLine)
	if parentLine == "" {
		return "", fmt.Errorf("profile: empty parent line in %s", profilePath)
	}

	if filepath.IsAbs(parentLine) {
		return filepath.Clean(parentLine), nil
	}

	// Parent lines are relative to the profile directory (where the parent file lives)
	resolved := filepath.Join(filepath.Clean(profilePath), parentLine)
	return filepath.Clean(resolved), nil
}

func MergeParents(profilePath string, profilesRoot string) (*ProfileInfo, error) {
	visited := make(map[string]bool)
	return mergeParentsRecursive(profilePath, profilesRoot, visited)
}

func mergeParentsRecursive(profilePath string, profilesRoot string, visited map[string]bool) (*ProfileInfo, error) {
	cleanPath := filepath.Clean(profilePath)
	if visited[cleanPath] {
		return nil, fmt.Errorf("profile: circular parent reference at %s", cleanPath)
	}
	visited[cleanPath] = true

	info, err := statProfileDir(cleanPath)
	if err != nil {
		return nil, err
	}
	info.Path = cleanPath

	parentFile := filepath.Join(cleanPath, "parent")
	parentData, parentErr := os.ReadFile(parentFile)

	if parentErr == nil {
		lines := splitLines(string(parentData))
		for _, line := range lines {
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parentPath, resolveErr := ResolveParent(cleanPath, line, profilesRoot)
			if resolveErr != nil {
				return nil, resolveErr
			}
			info.Parents = append(info.Parents, parentPath)

			parentInfo, mergeErr := mergeParentsRecursive(parentPath, profilesRoot, visited)
			if mergeErr != nil {
				return nil, mergeErr
			}

			info = mergeProfileInfo(info, parentInfo)
		}
	}

	return info, nil
}

func statProfileDir(profilePath string) (*ProfileInfo, error) {
	st, err := os.Stat(profilePath)
	if err != nil {
		return nil, fmt.Errorf("profile: %s: %w", profilePath, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("profile: %s is not a directory", profilePath)
	}

	info := &ProfileInfo{
		Path:         profilePath,
		MakeDefaults: make(map[string]string),
		PkgUseForce:  make(map[string][]string),
		PkgUseMask:   make(map[string][]string),
	}

	info.loadMakeDefaults(profilePath)
	info.loadPackages(profilePath)
	info.loadUseFlags(profilePath)
	info.loadPkgUse(profilePath)
	info.loadBashrc(profilePath)

	return info, nil
}

func (info *ProfileInfo) loadMakeDefaults(profilePath string) {
	path := filepath.Join(profilePath, "make.defaults")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	lines := splitLines(string(data))
	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if idx := strings.IndexByte(line, '='); idx >= 0 {
			key := strings.TrimSpace(line[:idx])
			value := strings.TrimSpace(line[idx+1:])
			key = stripVarQuotes(key)
			value = stripVarQuotes(value)
			if key != "" {
				info.MakeDefaults[key] = value
			}
		}
	}
}

func (info *ProfileInfo) loadPackages(profilePath string) {
	seen := make(map[string]bool)

	for _, fname := range []string{"packages", "packages.build"} {
		path := filepath.Join(profilePath, fname)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		lines := splitLines(string(data))
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if !seen[line] {
				seen[line] = true
				info.SystemSet = append(info.SystemSet, line)
			}
		}
	}
}

func (info *ProfileInfo) loadUseFlags(profilePath string) {
	for _, pair := range []struct{ file string; dst *[]string }{
		{"use.force", &info.UseForce},
		{"use.mask", &info.UseMask},
	} {
		path := filepath.Join(profilePath, pair.file)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		lines := splitLines(string(data))
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			*pair.dst = append(*pair.dst, line)
		}
	}
}

func (info *ProfileInfo) loadPkgUse(profilePath string) {
	for _, pair := range []struct{ file string; dst *map[string][]string }{
		{"package.use.force", &info.PkgUseForce},
		{"package.use.mask", &info.PkgUseMask},
	} {
		path := filepath.Join(profilePath, pair.file)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		lines := splitLines(string(data))
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			pkg := parts[0]
			flags := parts[1:]
			(*pair.dst)[pkg] = append((*pair.dst)[pkg], flags...)
		}
	}
}

func (info *ProfileInfo) loadBashrc(profilePath string) {
	path := filepath.Join(profilePath, "profile.bashrc")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	lines := splitLines(string(data))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if name, value, ok := parseBashrcAssignment(line); ok {
			if info.MakeDefaults[name] == "" {
				info.MakeDefaults[name] = value
			}
		}
	}
}

func parseBashrcAssignment(line string) (string, string, bool) {
	after, hasExport := strings.CutPrefix(line, "export ")
	if !hasExport {
		if idx := strings.IndexByte(line, '='); idx > 0 {
			name := strings.TrimSpace(line[:idx])
			value := strings.TrimSpace(line[idx+1:])
			if isBashVarName(name) {
				return name, cleanBashrcValue(value), true
			}
		}
		return "", "", false
	}

	trimmed := strings.TrimSpace(after)
	if idx := strings.IndexByte(trimmed, '='); idx > 0 {
		name := strings.TrimSpace(trimmed[:idx])
		value := strings.TrimSpace(trimmed[idx+1:])
		if isBashVarName(name) {
			return name, cleanBashrcValue(value), true
		}
	}

	if isBashVarName(trimmed) {
		return trimmed, "", true
	}

	return "", "", false
}

func cleanBashrcValue(value string) string {
	value = stripQuotes(value)
	value = strings.ReplaceAll(value, "${ARCH}", "")
	value = strings.TrimSpace(value)
	return value
}

func isBashVarName(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i, r := range s {
		if i == 0 && !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_') {
			return false
		}
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	return true
}

func stripQuotes(s string) string {
	for len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			s = s[1 : len(s)-1]
		} else {
			break
		}
	}
	return s
}

func stripVarQuotes(s string) string {
	for len(s) >= 2 {
		if s[0] == '"' && s[len(s)-1] == '"' {
			s = s[1 : len(s)-1]
		} else if s[0] == '\'' && s[len(s)-1] == '\'' {
			s = s[1 : len(s)-1]
		} else {
			break
		}
	}
	return s
}

func splitLines(input string) []string {
	var result []string
	current := input
	for {
		idx := strings.IndexByte(current, '\n')
		if idx < 0 {
			if current != "" {
				result = append(result, current)
			}
			break
		}
		line := current[:idx]
		if strings.HasSuffix(line, "\r") {
			line = line[:len(line)-1]
		}
		result = append(result, line)
		current = current[idx+1:]
	}
	return result
}

func mergeProfileInfo(child, parent *ProfileInfo) *ProfileInfo {
	merged := &ProfileInfo{
		Path:         child.Path,
		Parents:      child.Parents,
		MakeDefaults: make(map[string]string),
		PkgUseForce:  make(map[string][]string),
		PkgUseMask:   make(map[string][]string),
	}

	// parent values first (lower priority)
	for k, v := range parent.MakeDefaults {
		merged.MakeDefaults[k] = v
	}
	// child values override
	for k, v := range child.MakeDefaults {
		merged.MakeDefaults[k] = v
	}

	// parent system set first, then child
	seen := make(map[string]bool)
	for _, pkg := range parent.SystemSet {
		if !seen[pkg] {
			seen[pkg] = true
			merged.SystemSet = append(merged.SystemSet, pkg)
		}
	}
	for _, pkg := range child.SystemSet {
		if !seen[pkg] {
			seen[pkg] = true
			merged.SystemSet = append(merged.SystemSet, pkg)
		}
	}

	// accumulate USE flags
	seenForce := make(map[string]bool)
	for _, flag := range parent.UseForce {
		if !seenForce[flag] {
			seenForce[flag] = true
			merged.UseForce = append(merged.UseForce, flag)
		}
	}
	for _, flag := range child.UseForce {
		if !seenForce[flag] {
			seenForce[flag] = true
			merged.UseForce = append(merged.UseForce, flag)
		}
	}

	seenMask := make(map[string]bool)
	for _, flag := range parent.UseMask {
		if !seenMask[flag] {
			seenMask[flag] = true
			merged.UseMask = append(merged.UseMask, flag)
		}
	}
	for _, flag := range child.UseMask {
		if !seenMask[flag] {
			seenMask[flag] = true
			merged.UseMask = append(merged.UseMask, flag)
		}
	}

	// per-package USE: parent first, child appended
	for pkg, flags := range parent.PkgUseForce {
		merged.PkgUseForce[pkg] = append([]string{}, flags...)
	}
	for pkg, flags := range child.PkgUseForce {
		merged.PkgUseForce[pkg] = append(merged.PkgUseForce[pkg], flags...)
	}

	for pkg, flags := range parent.PkgUseMask {
		merged.PkgUseMask[pkg] = append([]string{}, flags...)
	}
	for pkg, flags := range child.PkgUseMask {
		merged.PkgUseMask[pkg] = append(merged.PkgUseMask[pkg], flags...)
	}

	return merged
}
