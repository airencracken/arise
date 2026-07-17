package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ProfileInfo struct {
	Path             string              // path to the active profile
	Directories      []string            // root-to-leaf profile stacking order
	Parents          []string            // ordered list of parent paths
	MakeDefaults     map[string]string   // merged make.defaults variables
	SystemSet        []string            // @system packages (merged from all parents)
	UseForce         []string            // forced USE flags
	UseMask          []string            // masked USE flags
	PkgUseForce      map[string][]string // package.use.force (per-package forced USE)
	PkgUseMask       map[string][]string // package.use.mask (per-package masked USE)
	PkgUse           map[string][]string // package.use profile defaults
	PkgUseRules      []PackageFlagRule
	PkgUseForceRules []PackageFlagRule
	PkgUseMaskRules  []PackageFlagRule
}

type PackageFlagRule struct {
	Atom  string
	Flags []string
}

func LoadProfile(profileSymlink string, profilesRoot string) (*ProfileInfo, error) {
	target, err := os.Readlink(profileSymlink)
	if err != nil {
		return nil, fmt.Errorf("profile: could not read symlink %s: %w", profileSymlink, err)
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
		return "", fmt.Errorf("profile: empty parent reference in %s", profilePath)
	}

	if filepath.IsAbs(parentLine) {
		return filepath.Clean(parentLine), nil
	}

	// Parent lines are relative to the profile directory (where the parent file lives)
	resolved := filepath.Join(filepath.Clean(profilePath), parentLine)
	return filepath.Clean(resolved), nil
}

func MergeParents(profilePath string, profilesRoot string) (*ProfileInfo, error) {
	var order []string
	seen := make(map[string]bool)
	stack := make(map[string]bool)
	if err := collectProfileOrder(filepath.Clean(profilePath), profilesRoot, seen, stack, &order); err != nil {
		return nil, err
	}
	var merged *ProfileInfo
	for _, path := range order {
		layer, err := statProfileDir(path)
		if err != nil {
			return nil, err
		}
		layer.Path = filepath.Clean(profilePath)
		layer.Directories = []string{path}
		if merged == nil {
			merged = layer
		} else {
			merged = mergeProfileInfo(layer, merged)
		}
	}
	if merged == nil {
		return nil, fmt.Errorf("profile: empty profile graph at %s", profilePath)
	}
	merged.Path = filepath.Clean(profilePath)
	merged.Directories = append([]string(nil), order...)
	if len(order) > 1 {
		merged.Parents = append([]string(nil), order[:len(order)-1]...)
	}
	return merged, nil
}

func collectProfileOrder(path, profilesRoot string, seen, stack map[string]bool, order *[]string) error {
	path = filepath.Clean(path)
	if stack[path] {
		return fmt.Errorf("profile: circular parent reference at %s", path)
	}
	if seen[path] {
		return nil
	}
	if _, err := statProfileDir(path); err != nil {
		return err
	}
	stack[path] = true
	data, err := os.ReadFile(filepath.Join(path, "parent"))
	if err == nil {
		for _, line := range splitLines(string(data)) {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parent, err := ResolveParent(path, line, profilesRoot)
			if err != nil {
				return err
			}
			if err := collectProfileOrder(parent, profilesRoot, seen, stack, order); err != nil {
				return err
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("profile: read parent %s: %w", path, err)
	}
	delete(stack, path)
	seen[path] = true
	*order = append(*order, path)
	return nil
}

func statProfileDir(profilePath string) (*ProfileInfo, error) {
	st, err := os.Stat(profilePath)
	if err != nil {
		return nil, fmt.Errorf("profile: could not read profile directory %s: %w", profilePath, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("profile: %s is not a directory", profilePath)
	}

	info := &ProfileInfo{
		Path:         profilePath,
		MakeDefaults: make(map[string]string),
		PkgUseForce:  make(map[string][]string),
		PkgUseMask:   make(map[string][]string),
		PkgUse:       make(map[string][]string),
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

	for _, fname := range []string{"packages"} {
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
			entry := line
			switch {
			case strings.HasPrefix(line, "-*"):
				entry = "-" + strings.TrimPrefix(line, "-*")
			case strings.HasPrefix(line, "*"):
				entry = strings.TrimPrefix(line, "*")
			default:
				continue
			}
			if !seen[entry] {
				seen[entry] = true
				info.SystemSet = append(info.SystemSet, entry)
			}
		}
	}
}

func (info *ProfileInfo) loadUseFlags(profilePath string) {
	for _, pair := range []struct {
		file string
		dst  *[]string
	}{
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
	for _, pair := range []struct {
		file  string
		dst   *map[string][]string
		rules *[]PackageFlagRule
	}{
		{"package.use.force", &info.PkgUseForce, &info.PkgUseForceRules},
		{"package.use.mask", &info.PkgUseMask, &info.PkgUseMaskRules},
		{"package.use", &info.PkgUse, &info.PkgUseRules},
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
			*pair.rules = append(*pair.rules, PackageFlagRule{Atom: pkg, Flags: append([]string(nil), flags...)})
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
		PkgUse:       make(map[string][]string),
	}
	for _, dir := range append(append([]string{}, parent.Directories...), child.Directories...) {
		if len(merged.Directories) == 0 || merged.Directories[len(merged.Directories)-1] != dir {
			merged.Directories = append(merged.Directories, dir)
		}
	}

	// parent values first (lower priority)
	for k, v := range parent.MakeDefaults {
		merged.MakeDefaults[k] = v
	}
	// child values override
	for k, v := range child.MakeDefaults {
		merged.MakeDefaults[k] = v
	}

	merged.SystemSet = applyFlagChanges(parent.SystemSet, child.SystemSet)

	// accumulate USE flags
	merged.UseForce = applyFlagChanges(parent.UseForce, child.UseForce)
	merged.UseMask = applyFlagChanges(parent.UseMask, child.UseMask)
	merged.PkgUseForceRules = append(append([]PackageFlagRule(nil), parent.PkgUseForceRules...), child.PkgUseForceRules...)
	merged.PkgUseMaskRules = append(append([]PackageFlagRule(nil), parent.PkgUseMaskRules...), child.PkgUseMaskRules...)
	merged.PkgUseRules = append(append([]PackageFlagRule(nil), parent.PkgUseRules...), child.PkgUseRules...)

	// per-package USE: parent first, child appended
	for pkg, flags := range parent.PkgUseForce {
		merged.PkgUseForce[pkg] = append([]string{}, flags...)
	}
	for pkg, flags := range child.PkgUseForce {
		merged.PkgUseForce[pkg] = applyFlagChanges(merged.PkgUseForce[pkg], flags)
	}

	for pkg, flags := range parent.PkgUseMask {
		merged.PkgUseMask[pkg] = append([]string{}, flags...)
	}
	for pkg, flags := range child.PkgUseMask {
		merged.PkgUseMask[pkg] = applyFlagChanges(merged.PkgUseMask[pkg], flags)
	}
	for pkg, flags := range parent.PkgUse {
		merged.PkgUse[pkg] = append([]string{}, flags...)
	}
	for pkg, flags := range child.PkgUse {
		merged.PkgUse[pkg] = applyFlagChanges(merged.PkgUse[pkg], flags)
	}

	return merged
}

func applyFlagChanges(previous, changes []string) []string {
	result := append([]string(nil), previous...)
	for _, change := range changes {
		if strings.HasPrefix(change, "-") {
			name := strings.TrimPrefix(change, "-")
			filtered := result[:0]
			for _, existing := range result {
				if existing != name {
					filtered = append(filtered, existing)
				}
			}
			result = filtered
			continue
		}
		found := false
		for _, existing := range result {
			if existing == change {
				found = true
				break
			}
		}
		if !found {
			result = append(result, change)
		}
	}
	return result
}
