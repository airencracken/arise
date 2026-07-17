package portage

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/profile"
)

var refPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

type Config struct {
	MakeConf map[string]string

	USE             []string
	CFLAGS          string
	CXXFLAGS        string
	MAKEOPTS        string
	ACCEPT_KEYWORDS []string
	ACCEPT_LICENSE  []string
	FEATURES        []string

	PackageUse            map[string][]string
	PackageUseRules       []PackageUseRule
	PackageAcceptKeywords map[string]string
	PackageLicense        map[string]string
	PackageMask           []string
	PackageUnmask         []string
	PackageEnv            map[string]string
	PackageProvided       []string

	ProfilePath          string
	ProfileParents       []string
	SystemSet            []string
	UseForce             []string
	UseMask              []string
	PackageUseForce      map[string][]string
	PackageUseMask       map[string][]string
	PackageUseForceRules []PackageUseRule
	PackageUseMaskRules  []PackageUseRule
	UseOrder             []string
	UseExpand            []string
	UseExpandHidden      []string
	UseExpandImplicit    []string
}

// PackageUseRule preserves package.use file order. A map is insufficient here:
// multiple overlapping atoms may match one CPV and later entries must win.
type PackageUseRule struct {
	Atom  string
	Flags []string
}

type MaskStatus struct {
	Masked bool
	Atom   string
	Source string
}

func LoadConfig(portageConfigRoot string) (*Config, error) {
	cfg := &Config{
		MakeConf:              make(map[string]string),
		PackageUse:            make(map[string][]string),
		PackageAcceptKeywords: make(map[string]string),
		PackageLicense:        make(map[string]string),
		PackageEnv:            make(map[string]string),
	}

	if _, err := os.Stat(portageConfigRoot); os.IsNotExist(err) {
		return cfg, nil
	}

	mc, err := ParseMakeConf(filepath.Join(portageConfigRoot, "make.conf"))
	if err != nil {
		return nil, fmt.Errorf("portage: could not parse make.conf: %w", err)
	}
	if mc != nil {
		cfg.MakeConf = mc
		ResolveMakeConfRefs(cfg.MakeConf)
	}

	cfg.populateAccessors()

	if err := cfg.loadPackageFiles(portageConfigRoot); err != nil {
		return nil, err
	}

	return cfg, nil
}

// LoadEffectiveConfig overlays the active profile defaults and policy with
// /etc/portage configuration. User make.conf values retain highest priority.
func LoadEffectiveConfig(portageConfigRoot string) (*Config, error) {
	cfg, err := LoadConfig(portageConfigRoot)
	if err != nil {
		return nil, err
	}
	profileLink := filepath.Join(portageConfigRoot, "make.profile")
	if _, err := os.Lstat(profileLink); os.IsNotExist(err) {
		return cfg, nil
	} else if err != nil {
		return nil, fmt.Errorf("portage: inspect active profile: %w", err)
	}
	info, err := profile.LoadProfile(profileLink, "")
	if err != nil {
		return nil, fmt.Errorf("portage: load active profile: %w", err)
	}
	merged := make(map[string]string)
	for _, directory := range info.Directories {
		layer, err := parseMakeConfAssignments(filepath.Join(directory, "make.defaults"))
		if err != nil {
			return nil, fmt.Errorf("portage: parse profile defaults %s: %w", directory, err)
		}
		mergeConfigAssignments(merged, layer)
	}
	userLayer, err := parseMakeConfAssignments(filepath.Join(portageConfigRoot, "make.conf"))
	if err != nil {
		return nil, fmt.Errorf("portage: parse user make.conf: %w", err)
	}
	mergeConfigAssignments(merged, userLayer)
	ResolveMakeConfRefs(merged)
	cfg.MakeConf = merged
	cfg.populateAccessors()
	cfg.ProfilePath = info.Path
	if len(info.Directories) > 1 {
		cfg.ProfileParents = append([]string(nil), info.Directories[:len(info.Directories)-1]...)
	}
	cfg.SystemSet = append([]string(nil), info.SystemSet...)
	cfg.UseForce = append([]string(nil), info.UseForce...)
	cfg.UseMask = append([]string(nil), info.UseMask...)
	cfg.PackageUseForce = cloneFlagMap(info.PkgUseForce)
	cfg.PackageUseMask = cloneFlagMap(info.PkgUseMask)
	cfg.PackageUseForceRules = profilePackageRules(info.PkgUseForceRules)
	cfg.PackageUseMaskRules = profilePackageRules(info.PkgUseMaskRules)
	profileUseRules := profilePackageRules(info.PkgUseRules)
	cfg.PackageUseRules = append(profileUseRules, cfg.PackageUseRules...)
	profileMasks, profileUnmasks, err := loadProfileMaskStack(info.Directories)
	if err != nil {
		return nil, err
	}
	cfg.PackageMask = applyAtomChanges(profileMasks, cfg.PackageMask)
	cfg.PackageUnmask = applyAtomChanges(profileUnmasks, cfg.PackageUnmask)
	cfg.UseOrder = splitShWords(merged["USE_ORDER"])
	cfg.UseExpand = splitShWords(merged["USE_EXPAND"])
	cfg.UseExpandHidden = splitShWords(merged["USE_EXPAND_HIDDEN"])
	cfg.UseExpandImplicit = splitShWords(merged["USE_EXPAND_IMPLICIT"])
	// USE_EXPAND_IMPLICIT controls IUSE declaration semantics; unlike
	// USE_EXPAND it does not itself add the variable values to USE.
	cfg.USE = appendUseExpand(cfg.USE, cfg.UseExpand, merged)
	cfg.USE = applyEffectiveGlobalUse(cfg.USE, cfg.UseForce, cfg.UseMask)
	return cfg, nil
}

func loadProfileMaskStack(directories []string) ([]string, []string, error) {
	var masks, unmasks []string
	if len(directories) == 0 {
		return nil, nil, nil
	}
	// profiles/package.mask is repository-wide and is not necessarily an
	// explicit parent of the selected profile.
	profilesRoot := ""
	marker := string(filepath.Separator) + "profiles" + string(filepath.Separator)
	if index := strings.Index(filepath.Clean(directories[len(directories)-1]), marker); index >= 0 {
		profilesRoot = filepath.Clean(directories[len(directories)-1])[:index+len(marker)-1]
	}
	layers := append([]string(nil), directories...)
	if profilesRoot != "" {
		layers = append([]string{profilesRoot}, layers...)
	}
	seen := make(map[string]bool)
	for _, directory := range layers {
		directory = filepath.Clean(directory)
		if seen[directory] {
			continue
		}
		seen[directory] = true
		layerMasks, err := ParsePackageMask(filepath.Join(directory, "package.mask"))
		if err != nil {
			return nil, nil, fmt.Errorf("portage: parse profile package.mask %s: %w", directory, err)
		}
		layerUnmasks, err := ParsePackageUnmask(filepath.Join(directory, "package.unmask"))
		if err != nil {
			return nil, nil, fmt.Errorf("portage: parse profile package.unmask %s: %w", directory, err)
		}
		masks = applyAtomChanges(masks, layerMasks)
		unmasks = applyAtomChanges(unmasks, layerUnmasks)
	}
	return masks, unmasks, nil
}

func applyAtomChanges(previous, changes []string) []string {
	result := append([]string(nil), previous...)
	for _, change := range changes {
		if strings.HasPrefix(change, "-") {
			remove := strings.TrimPrefix(change, "-")
			filtered := result[:0]
			for _, existing := range result {
				if existing != remove {
					filtered = append(filtered, existing)
				}
			}
			result = filtered
			continue
		}
		result = append(result, change)
	}
	return result
}

func applyEffectiveGlobalUse(use, force, mask []string) []string {
	state := make(map[string]bool)
	var order []string
	set := func(raw string, enabled bool) {
		name := strings.TrimPrefix(raw, "-")
		if name == "" {
			return
		}
		if _, exists := state[name]; !exists {
			order = append(order, name)
		}
		state[name] = enabled
	}
	for _, raw := range use {
		set(raw, !strings.HasPrefix(raw, "-"))
	}
	for _, raw := range force {
		set(raw, true)
	}
	// A mask wins when a flag is present in both policy sets. This is how
	// profiles express architecture constraints such as big-endian.
	for _, raw := range mask {
		set(raw, false)
	}
	result := make([]string, 0, len(order))
	for _, name := range order {
		if state[name] {
			result = append(result, name)
		} else {
			result = append(result, "-"+name)
		}
	}
	return result
}

var incrementalVariables = map[string]bool{
	"USE": true, "USE_EXPAND": true, "USE_EXPAND_HIDDEN": true,
	"USE_EXPAND_IMPLICIT": true, "FEATURES": true, "ACCEPT_LICENSE": true,
	"CONFIG_PROTECT": true, "CONFIG_PROTECT_MASK": true,
}

type configAssignment struct{ key, value string }

func parseMakeConfAssignments(path string) ([]configAssignment, error) {
	lines, err := readMakeConfLines(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var assignments []configAssignment
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 1 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		value := unquote(strings.TrimSpace(line[idx+1:]))
		assignments = append(assignments, configAssignment{key: key, value: value})
	}
	return assignments, nil
}

func mergeConfigAssignments(target map[string]string, assignments []configAssignment) {
	for _, assignment := range assignments {
		value := expandLayerValue(assignment.value, target)
		if incrementalVariables[assignment.key] {
			target[assignment.key] = mergeIncremental(target[assignment.key], value)
		} else {
			target[assignment.key] = value
		}
	}
}

func expandLayerValue(value string, current map[string]string) string {
	return refPattern.ReplaceAllStringFunc(value, func(reference string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(reference, "${"), "}")
		if previous, ok := current[name]; ok {
			return previous
		}
		return reference
	})
}

func mergeIncremental(previous, next string) string {
	var order []string
	values := make(map[string]string)
	apply := func(raw string) {
		for _, token := range splitShWords(raw) {
			if token == "-*" {
				order = nil
				values = make(map[string]string)
				continue
			}
			name := strings.TrimPrefix(token, "-")
			if name == "" {
				continue
			}
			if _, exists := values[name]; !exists {
				order = append(order, name)
			}
			values[name] = token
		}
	}
	apply(previous)
	apply(next)
	result := make([]string, 0, len(order))
	for _, name := range order {
		result = append(result, values[name])
	}
	return strings.Join(result, " ")
}

func appendUseExpand(use, groups []string, values map[string]string) []string {
	result := append([]string(nil), use...)
	for _, group := range groups {
		prefix := strings.ToLower(group) + "_"
		for _, value := range splitShWords(values[group]) {
			negative := strings.HasPrefix(value, "-")
			name := strings.TrimPrefix(value, "-")
			if name == "" {
				continue
			}
			flag := prefix + name
			if negative {
				flag = "-" + flag
			}
			result = append(result, flag)
		}
	}
	return splitShWords(mergeIncremental("", strings.Join(result, " ")))
}

func cloneFlagMap(input map[string][]string) map[string][]string {
	result := make(map[string][]string, len(input))
	for key, values := range input {
		result[key] = append([]string(nil), values...)
	}
	return result
}

func profilePackageRules(input []profile.PackageFlagRule) []PackageUseRule {
	result := make([]PackageUseRule, 0, len(input))
	for _, rule := range input {
		result = append(result, PackageUseRule{Atom: rule.Atom, Flags: append([]string(nil), rule.Flags...)})
	}
	return result
}

func (cfg *Config) PackageUseForceFor(cpv, slot, repo string) []string {
	return packagePolicyFlagsFor(cfg.PackageUseForceRules, cpv, slot, repo)
}

func (cfg *Config) PackageUseMaskFor(cpv, slot, repo string) []string {
	return packagePolicyFlagsFor(cfg.PackageUseMaskRules, cpv, slot, repo)
}

// EffectiveUseFor reduces the configuration layers that apply to a selected
// package version. IUSE filtering and defaults remain the caller's concern.
func (cfg *Config) EffectiveUseFor(cpv, slot, repo string) map[string]bool {
	result := make(map[string]bool)
	if cfg == nil {
		return result
	}
	applyChanges := func(changes []string) {
		for _, change := range changes {
			name := strings.TrimPrefix(change, "-")
			if name != "" {
				result[name] = !strings.HasPrefix(change, "-")
			}
		}
	}
	applyPolicy := func(changes []string, enabled bool) {
		for _, change := range changes {
			name := strings.TrimPrefix(change, "-")
			if name != "" {
				result[name] = enabled
			}
		}
	}
	applyChanges(cfg.USE)
	applyChanges(cfg.PackageUseFor(cpv, slot, repo))
	applyPolicy(cfg.UseForce, true)
	applyPolicy(cfg.UseMask, false)

	candidateCP := cpv
	if candidate, err := atom.Parse(cpv); err == nil {
		candidateCP = candidate.CP()
	}
	force := cfg.PackageUseForceFor(cpv, slot, repo)
	if len(force) == 0 {
		force = cfg.PackageUseForce[candidateCP]
	}
	mask := cfg.PackageUseMaskFor(cpv, slot, repo)
	if len(mask) == 0 {
		mask = cfg.PackageUseMask[candidateCP]
	}
	applyPolicy(force, true)
	applyPolicy(mask, false)
	return result
}

func packagePolicyFlagsFor(rules []PackageUseRule, cpv, slot, repo string) []string {
	var result []string
	for _, rule := range rules {
		if !PackageAtomMatches(rule.Atom, cpv, slot, repo) {
			continue
		}
		for _, change := range rule.Flags {
			name := strings.TrimPrefix(change, "-")
			if name == "" {
				continue
			}
			filtered := result[:0]
			for _, current := range result {
				if current != name {
					filtered = append(filtered, current)
				}
			}
			result = filtered
			if !strings.HasPrefix(change, "-") {
				result = append(result, name)
			}
		}
	}
	return result
}

func (cfg *Config) populateAccessors() {
	if v, ok := cfg.MakeConf["USE"]; ok {
		cfg.USE = splitShWords(v)
	}
	if v, ok := cfg.MakeConf["CFLAGS"]; ok {
		cfg.CFLAGS = v
	}
	if v, ok := cfg.MakeConf["CXXFLAGS"]; ok {
		cfg.CXXFLAGS = v
	}
	if v, ok := cfg.MakeConf["MAKEOPTS"]; ok {
		cfg.MAKEOPTS = v
	}
	if v, ok := cfg.MakeConf["ACCEPT_KEYWORDS"]; ok {
		cfg.ACCEPT_KEYWORDS = splitShWords(v)
	}
	if v, ok := cfg.MakeConf["ACCEPT_LICENSE"]; ok {
		cfg.ACCEPT_LICENSE = splitShWords(v)
	}
	if v, ok := cfg.MakeConf["FEATURES"]; ok {
		cfg.FEATURES = splitShWords(v)
	}
}

func (cfg *Config) loadPackageFiles(root string) error {
	var err error

	cfg.PackageUse, err = ParsePackageUse(filepath.Join(root, "package.use"))
	if err != nil {
		return fmt.Errorf("portage: could not parse package.use: %w", err)
	}
	cfg.PackageUseRules, err = ParsePackageUseRules(filepath.Join(root, "package.use"))
	if err != nil {
		return fmt.Errorf("portage: could not parse ordered package.use: %w", err)
	}

	cfg.PackageAcceptKeywords, err = ParsePackageAcceptKeywords(filepath.Join(root, "package.accept_keywords"))
	if err != nil {
		return fmt.Errorf("portage: could not parse package.accept_keywords: %w", err)
	}

	cfg.PackageLicense, err = ParsePackageLicense(filepath.Join(root, "package.license"))
	if err != nil {
		return fmt.Errorf("portage: could not parse package.license: %w", err)
	}

	cfg.PackageMask, err = ParsePackageMask(filepath.Join(root, "package.mask"))
	if err != nil {
		return fmt.Errorf("portage: could not parse package.mask: %w", err)
	}

	cfg.PackageUnmask, err = ParsePackageUnmask(filepath.Join(root, "package.unmask"))
	if err != nil {
		return fmt.Errorf("portage: could not parse package.unmask: %w", err)
	}

	cfg.PackageEnv, err = ParsePackageEnv(filepath.Join(root, "package.env"))
	if err != nil {
		return fmt.Errorf("portage: could not parse package.env: %w", err)
	}

	cfg.PackageProvided, err = ParsePackageProvided(filepath.Join(root, "profile", "package.provided"))
	if err != nil {
		return fmt.Errorf("portage: could not parse profile/package.provided: %w", err)
	}

	return nil
}

func ParseMakeConf(path string) (map[string]string, error) {
	lines, err := readMakeConfLines(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	m := make(map[string]string)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		val = unquote(val)
		if key != "" {
			m[key] = val
		}
	}
	return m, nil
}

func readMakeConfLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			/* Best effort */
		}
	}()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	var buf strings.Builder
	for scanner.Scan() {
		raw := scanner.Text()
		stripped := strings.TrimRight(raw, " \t")

		if len(stripped) > 0 && stripped[len(stripped)-1] == '\\' && !strings.HasSuffix(stripped, "\\\\") {
			buf.WriteString(stripped[:len(stripped)-1])
			continue
		}

		if buf.Len() > 0 {
			buf.WriteString(raw)
			lines = append(lines, buf.String())
			buf.Reset()
		} else {
			lines = append(lines, raw)
		}
	}
	if buf.Len() > 0 {
		lines = append(lines, buf.String())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("portage: could not read %s: %w", path, err)
	}
	return lines, nil
}

func ParsePackageUse(path string) (map[string][]string, error) {
	rules, err := ParsePackageUseRules(path)
	if err != nil {
		return nil, err
	}
	if rules == nil {
		return nil, nil
	}
	m := make(map[string][]string)
	for _, rule := range rules {
		m[rule.Atom] = append(m[rule.Atom], rule.Flags...)
	}
	return m, nil
}

// ParsePackageUseRules parses package.use without discarding ordering between
// different, potentially overlapping atoms.
func ParsePackageUseRules(path string) ([]PackageUseRule, error) {
	lines, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	if lines == nil {
		return nil, nil
	}

	var rules []PackageUseRule
	for _, line := range lines {
		ruleAtom, flagsStr := parseAtomConfig(line)
		if ruleAtom == "" {
			continue
		}
		flags := splitShWords(flagsStr)
		if len(flags) == 0 {
			continue
		}
		rules = append(rules, PackageUseRule{Atom: ruleAtom, Flags: flags})
	}
	return rules, nil
}

// PackageUseFor returns the ordered user package.use changes matching a CPV.
func (cfg *Config) PackageUseFor(cpv, slot, repo string) []string {
	if cfg == nil {
		return nil
	}
	if len(cfg.PackageUseRules) == 0 {
		candidate, err := atom.Parse(cpv)
		if err != nil {
			return append([]string(nil), cfg.PackageUse[cpv]...)
		}
		return append([]string(nil), cfg.PackageUse[candidate.CP()]...)
	}
	var result []string
	for _, rule := range cfg.PackageUseRules {
		if PackageAtomMatches(rule.Atom, cpv, slot, repo) {
			result = append(result, rule.Flags...)
		}
	}
	return result
}

// PackageAtomMatches reports whether a configuration atom applies to a
// concrete CPV and its selected slot/repository.
func PackageAtomMatches(rawRule, cpv, slot, repo string) bool {
	candidate, err := atom.Parse(cpv)
	if err != nil {
		return false
	}
	if rawRule == "*/*" {
		return true
	}
	rule, err := atom.Parse(rawRule)
	if err != nil {
		return false
	}
	if rule.Category != "*" && rule.Category != candidate.Category {
		return false
	}
	if rule.Package != "*" && rule.Package != candidate.Package {
		return false
	}
	if rule.Repo != "" && rule.Repo != repo {
		return false
	}
	if rule.Slot != "" && rule.Slot != slot {
		return false
	}
	if rule.Version == nil {
		return true
	}
	if candidate.Version == nil {
		return false
	}
	cmp := candidate.Version.Compare(rule.Version)
	switch rule.Op {
	case atom.OpLess:
		return cmp < 0
	case atom.OpLessEq:
		return cmp <= 0
	case atom.OpGt:
		return cmp > 0
	case atom.OpGtEq:
		return cmp >= 0
	case atom.OpTilde:
		return strings.TrimSuffix(candidate.Version.Raw, fmt.Sprintf("-r%d", candidate.Version.Revision)) ==
			strings.TrimSuffix(rule.Version.Raw, fmt.Sprintf("-r%d", rule.Version.Revision))
	case atom.OpEqGlob:
		return strings.HasPrefix(candidate.Version.Raw, strings.TrimSuffix(rule.Version.Raw, "*"))
	default:
		return cmp == 0
	}
}

// PackageMaskStatus evaluates administrator masks for a concrete candidate.
func (cfg *Config) PackageMaskStatus(cpv, slot, repo string) MaskStatus {
	var status MaskStatus
	if cfg == nil {
		return status
	}
	for _, entry := range cfg.PackageMask {
		if PackageAtomMatches(entry, cpv, slot, repo) {
			status = MaskStatus{Masked: true, Atom: entry, Source: "package.mask"}
		}
	}
	for _, entry := range cfg.PackageUnmask {
		if PackageAtomMatches(entry, cpv, slot, repo) {
			status = MaskStatus{Atom: entry, Source: "package.unmask"}
		}
	}
	return status
}

func ParsePackageAcceptKeywords(path string) (map[string]string, error) {
	lines, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	if lines == nil {
		return nil, nil
	}

	m := make(map[string]string)
	for _, line := range lines {
		atom, keyword := parseAtomConfig(line)
		if atom == "" {
			continue
		}
		m[atom] = keyword
	}
	return m, nil
}

func ParsePackageLicense(path string) (map[string]string, error) {
	lines, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	if lines == nil {
		return nil, nil
	}

	m := make(map[string]string)
	for _, line := range lines {
		atom, license := parseAtomConfig(line)
		if atom == "" {
			continue
		}
		m[atom] = license
	}
	return m, nil
}

func ParsePackageMask(path string) ([]string, error) {
	return parseAtomList(path)
}

func ParsePackageUnmask(path string) ([]string, error) {
	return parseAtomList(path)
}

func parseAtomList(path string) ([]string, error) {
	lines, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	if lines == nil {
		return nil, nil
	}

	var atoms []string
	for _, line := range lines {
		atom, _ := parseAtomConfig(line)
		if atom != "" {
			atoms = append(atoms, atom)
		}
	}
	return atoms, nil
}

// ParsePackageProvided reads a package.provided file (or directory) and
// returns the list of package atoms that are declared as externally provided.
func ParsePackageProvided(path string) ([]string, error) {
	return parseAtomList(path)
}

func ParsePackageEnv(path string) (map[string]string, error) {
	lines, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	if lines == nil {
		return nil, nil
	}

	m := make(map[string]string)
	for _, line := range lines {
		atom, envFile := parseAtomConfig(line)
		if atom == "" {
			continue
		}
		m[atom] = envFile
	}
	return m, nil
}

func ResolveMakeConfRefs(m map[string]string) {
	if m == nil {
		return
	}

	const maxIter = 24
	for i := 0; i < maxIter; i++ {
		changed := false
		for key, val := range m {
			matches := refPattern.FindAllStringSubmatchIndex(val, -1)
			if len(matches) == 0 {
				continue
			}

			var result strings.Builder
			lastEnd := 0
			for _, match := range matches {
				fullStart, fullEnd := match[0], match[1]
				refStart, refEnd := match[2], match[3]
				varName := val[refStart:refEnd]

				result.WriteString(val[lastEnd:fullStart])

				if varName == key {
					result.WriteString("")
				} else if refVal, ok := m[varName]; ok {
					result.WriteString(refVal)
				} else {
					result.WriteString(val[fullStart:fullEnd])
				}
				lastEnd = fullEnd
			}
			result.WriteString(val[lastEnd:])

			newVal := result.String()
			if newVal != val {
				m[key] = newVal
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

func ReadConfigFile(path string) ([]string, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var files []string
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			files = append(files, filepath.Join(path, e.Name()))
		}
		sort.Strings(files)
	} else {
		files = []string{path}
	}

	var allLines []string
	for _, f := range files {
		lines, err := readFileLines(f)
		if err != nil {
			return nil, err
		}
		allLines = append(allLines, lines...)
	}
	return allLines, nil
}

func readFileLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			/* Best effort */
		}
	}()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("portage: could not read %s: %w", path, err)
	}
	return lines, nil
}

func splitShWords(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}

	var words []string
	var current strings.Builder
	inSingle := false
	inDouble := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case inSingle:
			if ch == '\'' {
				inSingle = false
			} else {
				current.WriteByte(ch)
			}
		case inDouble:
			if ch == '"' {
				inDouble = false
			} else {
				current.WriteByte(ch)
			}
		case ch == '\'':
			inSingle = true
		case ch == '"':
			inDouble = true
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			if current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		words = append(words, current.String())
	}
	return words
}

func parseAtomConfig(line string) (atom, value string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", ""
	}

	for i := 0; i < len(line); i++ {
		if line[i] == ' ' || line[i] == '\t' {
			return line[:i], strings.TrimSpace(line[i:])
		}
	}
	return line, ""
}

func unquote(s string) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) >= 2 {
		if (trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"') ||
			(trimmed[0] == '\'' && trimmed[len(trimmed)-1] == '\'') {
			return trimmed[1 : len(trimmed)-1]
		}
	}
	return s
}

// RepoEntry holds a single repository configuration from repos.conf.
type RepoEntry struct {
	Name     string
	Location string
	SyncURI  string
	SyncType string
}

// ReadReposConf returns all repository entries from a repos.conf file or
// directory in deterministic file order.
func ReadReposConf(path string) ([]RepoEntry, error) {
	return parseReposConfDir(path)
}

// ParseReposConf reads repos.conf from the given path (file or directory)
// and returns the sync-uri for the repo whose location matches targetDir.
// If targetDir is empty, returns the sync-uri for the first repo found.
func ParseReposConf(reposConfPath, targetDir string) string {
	entries, err := parseReposConfDir(reposConfPath)
	if err != nil || len(entries) == 0 {
		return ""
	}

	if targetDir != "" {
		cleanTarget := filepath.Clean(targetDir)
		for _, e := range entries {
			if e.Location != "" && filepath.Clean(e.Location) == cleanTarget {
				return e.SyncURI
			}
		}

		// Portage installations are sometimes migrated from /usr/portage to
		// /var/db/repos/gentoo without updating repos.conf. The repository
		// section name remains stable and is the next-best identity.
		targetName := filepath.Base(cleanTarget)
		for _, preferredType := range []string{"git", ""} {
			for _, e := range entries {
				if e.Name == targetName && e.SyncURI != "" &&
					(preferredType == "" || e.SyncType == preferredType) {
					return e.SyncURI
				}
			}
		}
		return ""
	}

	for _, e := range entries {
		if e.SyncURI != "" {
			return e.SyncURI
		}
	}
	return ""
}

func parseReposConfDir(root string) ([]RepoEntry, error) {
	info, err := os.Stat(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var files []string
	if info.IsDir() {
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			files = append(files, filepath.Join(root, e.Name()))
		}
		sort.Strings(files)
	} else {
		files = []string{root}
	}

	var allEntries []RepoEntry
	for _, f := range files {
		entries, err := parseReposConfFile(f)
		if err != nil {
			continue
		}
		allEntries = append(allEntries, entries...)
	}
	return allEntries, nil
}

func parseReposConfFile(path string) ([]RepoEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []RepoEntry
	var current *RepoEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if current != nil && current.Name != "" {
				entries = append(entries, *current)
			}
			name := line[1 : len(line)-1]
			current = &RepoEntry{Name: name}
			continue
		}

		if current == nil {
			continue
		}

		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		switch key {
		case "location":
			current.Location = val
		case "sync-uri":
			current.SyncURI = val
		case "sync-type":
			current.SyncType = val
		}
	}

	if current != nil && current.Name != "" {
		entries = append(entries, *current)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	return entries, nil
}

// ParseBinhostConfig reads binhost URLs from portage config.
func ParseBinhostConfig(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	if v, ok := cfg.MakeConf["PORTAGE_BINHOST"]; ok && v != "" {
		return splitShWords(v)
	}
	return nil
}
