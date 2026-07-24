package portage

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/profile"
)

// PhaseEnvironmentABI identifies artifact-affecting package execution
// semantics. Packages built by Arise persist this value in VDB so a later ABI
// change cannot be hidden behind unchanged USE metadata.
const PhaseEnvironmentABI = "4"

// PhaseEnvironmentABICompatible reports whether an installed Arise artifact
// remains valid under the current execution ABI. ABI 3 changed only
// architecture-aware distfile selection. ABI 4 adds a read-only fallback for
// version queries which previously failed the phase instead of committing an
// ambiguous artifact. Successfully committed ABI-2/3 artifacts therefore do
// not require a mass rebuild.
func PhaseEnvironmentABICompatible(installed string) bool {
	switch installed {
	case PhaseEnvironmentABI, "3", "2":
		return true
	default:
		return false
	}
}

var refPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

type cachedPolicyAtomEntry struct {
	atom  *atom.Atom
	valid bool
}

var policyAtomCache sync.Map

func cachedPolicyAtom(raw string) (*atom.Atom, bool) {
	if cached, found := policyAtomCache.Load(raw); found {
		entry := cached.(cachedPolicyAtomEntry)
		return entry.atom, entry.valid
	}
	parsed, err := atom.Parse(raw)
	entry := cachedPolicyAtomEntry{atom: parsed, valid: err == nil}
	actual, _ := policyAtomCache.LoadOrStore(raw, entry)
	entry = actual.(cachedPolicyAtomEntry)
	return entry.atom, entry.valid
}

type Config struct {
	MakeConf map[string]string

	USE []string
	// UserUSE and CommandUSE retain the layers which outrank package-internal
	// IUSE defaults. USE is the fully merged effective profile/config value and
	// cannot by itself distinguish a profile `-flag` from an explicit user
	// override of IUSE="+flag".
	UserUSE         []string
	CommandUSE      []string
	CFLAGS          string
	CXXFLAGS        string
	MAKEOPTS        string
	ACCEPT_KEYWORDS []string
	ACCEPT_LICENSE  []string
	FEATURES        []string
	LicenseGroups   map[string][]string

	PackageUse                map[string][]string
	PackageUseRules           []PackageUseRule
	PackageAcceptKeywords     map[string]string
	PackageAcceptKeywordRules []PackageUseRule
	PackageLicense            map[string]string
	PackageLicenseRules       []PackageUseRule
	PackageMask               []string
	PackageMaskRules          []PackageMaskRule
	PackageUnmask             []string
	PackageEnv                map[string]string
	PackageEnvRules           []PackageUseRule
	commandEnvironment        []configAssignment
	commandEnvironmentBase    map[string]string
	commandEnvironmentExisted map[string]bool
	ConfigRoot                string
	PackageProvided           []string

	ProfilePath                string
	ProfileParents             []string
	SystemSet                  []string
	UseForce                   []string
	UseMask                    []string
	UseStableForce             []string
	UseStableMask              []string
	PackageUseForce            map[string][]string
	PackageUseMask             map[string][]string
	PackageUseForceRules       []PackageUseRule
	PackageUseMaskRules        []PackageUseRule
	PackageUseStableForceRules []PackageUseRule
	PackageUseStableMaskRules  []PackageUseRule
	UseOrder                   []string
	UseExpand                  []string
	UseExpandHidden            []string
	UseExpandImplicit          []string
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
	Reason string
}

type PackageMaskRule struct{ Atom, Source, Reason string }

func LoadConfig(portageConfigRoot string) (*Config, error) {
	cfg := &Config{
		MakeConf:              make(map[string]string),
		PackageUse:            make(map[string][]string),
		PackageAcceptKeywords: make(map[string]string),
		PackageLicense:        make(map[string]string),
		PackageEnv:            make(map[string]string),
	}
	cfg.ConfigRoot = filepath.Clean(portageConfigRoot)

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
	return LoadEffectiveConfigWithEnvironment(portageConfigRoot, os.Environ())
}

// LoadEffectiveConfigWithEnvironment loads the disk configuration and then
// applies the small, explicit set of variables Portage accepts from a command
// invocation. Taking the environment as data keeps precedence testable and
// avoids accidentally importing unrelated process state.
func LoadEffectiveConfigWithEnvironment(portageConfigRoot string, environ []string) (*Config, error) {
	cfg, err := loadEffectiveConfig(portageConfigRoot)
	if err != nil {
		return nil, err
	}
	cfg.ApplyCommandEnvironment(environ)
	return cfg, nil
}

func loadEffectiveConfig(portageConfigRoot string) (*Config, error) {
	cfg, err := LoadConfig(portageConfigRoot)
	if err != nil {
		return nil, err
	}
	cfg.UserUSE = append([]string(nil), cfg.USE...)
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
	removals := make(map[string]map[string]bool)
	globalLayer, err := parseSelectedAssignments("/usr/share/portage/config/make.globals", effectiveGlobalVariables)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("portage: parse make.globals: %w", err)
	}
	trackIncrementalRemovals(removals, globalLayer)
	mergeConfigAssignments(merged, globalLayer)
	for _, directory := range info.Directories {
		layer, err := parseMakeConfAssignments(filepath.Join(directory, "make.defaults"))
		if err != nil {
			return nil, fmt.Errorf("portage: parse profile defaults %s: %w", directory, err)
		}
		trackIncrementalRemovals(removals, layer)
		mergeConfigAssignments(merged, layer)
	}
	// ProfileInfo retains the leaf-effective assignment for each variable. Use
	// it as the authoritative tombstone source when intervening self-references
	// repeat values across a diamond profile graph.
	var profileEffective []configAssignment
	for key, value := range info.MakeDefaults {
		profileEffective = append(profileEffective, configAssignment{key: key, value: value})
	}
	trackIncrementalRemovals(removals, profileEffective)
	userLayer, err := parseMakeConfAssignments(filepath.Join(portageConfigRoot, "make.conf"))
	if err != nil {
		return nil, fmt.Errorf("portage: parse user make.conf: %w", err)
	}
	trackIncrementalRemovals(removals, userLayer)
	for _, assignment := range userLayer {
		if assignment.key == "ACCEPT_LICENSE" {
			// An explicit user policy replaces the profile's default acceptance
			// baseline; additions/removals within the user layer remain ordered.
			delete(merged, "ACCEPT_LICENSE")
			break
		}
	}
	mergeConfigAssignments(merged, userLayer)
	for variable := range removalOnlyIncrementalVariables {
		values := applyOrderedChanges(nil, splitShWords(merged[variable]))
		var filtered []string
		for _, value := range values {
			if !removals[variable][value] {
				filtered = append(filtered, value)
			}
		}
		merged[variable] = strings.Join(filtered, " ")
	}
	if merged["USE_ORDER"] == "" {
		merged["USE_ORDER"] = "env:pkg:conf:defaults:pkginternal:features:repo:env.d"
	}
	ResolveMakeConfRefs(merged)
	cfg.MakeConf = merged
	cfg.populateAccessors()
	cfg.ProfilePath = info.Path
	if len(info.Directories) > 1 {
		cfg.ProfileParents = append([]string(nil), info.Directories[:len(info.Directories)-1]...)
	}
	cfg.SystemSet = append([]string(nil), info.SystemSet...)
	cfg.PackageProvided = applyAtomChanges(info.PackageProvided, cfg.PackageProvided)
	cfg.UseForce = append([]string(nil), info.UseForce...)
	cfg.UseMask = append([]string(nil), info.UseMask...)
	cfg.UseStableForce = append([]string(nil), info.UseStableForce...)
	cfg.UseStableMask = append([]string(nil), info.UseStableMask...)
	cfg.PackageUseForce = cloneFlagMap(info.PkgUseForce)
	cfg.PackageUseMask = cloneFlagMap(info.PkgUseMask)
	cfg.PackageUseForceRules = profilePackageRules(info.PkgUseForceRules)
	cfg.PackageUseMaskRules = profilePackageRules(info.PkgUseMaskRules)
	cfg.PackageUseStableForceRules = profilePackageRules(info.PkgUseStableForceRules)
	cfg.PackageUseStableMaskRules = profilePackageRules(info.PkgUseStableMaskRules)
	profileUseRules := profilePackageRules(info.PkgUseRules)
	cfg.PackageUseRules = append(profileUseRules, cfg.PackageUseRules...)
	repositories, err := RepositoryPolicyOrder(filepath.Join(portageConfigRoot, "repos.conf"))
	if err != nil {
		return nil, err
	}
	var repositoryRoots []string
	for _, repository := range repositories {
		if repository.Location != "" {
			repositoryRoots = append(repositoryRoots, repository.Location)
		}
	}
	if len(repositoryRoots) == 0 {
		repositoryRoots = profileRepositories(info.Directories)
	}
	cfg.LicenseGroups, err = ParseLicenseGroups(repositoryRoots)
	if err != nil {
		return nil, fmt.Errorf("portage: load license groups: %w", err)
	}
	profileMasks, profileUnmasks, err := loadProfileMaskStack(repositoryRoots, info.Directories)
	if err != nil {
		return nil, err
	}
	cfg.PackageMask = applyAtomChanges(profileMasks, cfg.PackageMask)
	var policyRules []PackageMaskRule
	for _, root := range repositoryRoots {
		rules, rulesErr := ParsePackageMaskRules(filepath.Join(root, "profiles", "package.mask"))
		if rulesErr != nil {
			return nil, rulesErr
		}
		policyRules = applyPackageMaskRuleChanges(policyRules, rules)
	}
	for _, directory := range info.Directories {
		rules, rulesErr := ParsePackageMaskRules(filepath.Join(directory, "package.mask"))
		if rulesErr != nil {
			return nil, rulesErr
		}
		policyRules = applyPackageMaskRuleChanges(policyRules, rules)
	}
	cfg.PackageMaskRules = applyPackageMaskRuleChanges(policyRules, cfg.PackageMaskRules)
	cfg.PackageUnmask = applyAtomChanges(profileUnmasks, cfg.PackageUnmask)
	cfg.UseOrder = splitShWords(merged["USE_ORDER"])
	cfg.UseExpand = splitShWords(merged["USE_EXPAND"])
	cfg.UseExpandHidden = splitShWords(merged["USE_EXPAND_HIDDEN"])
	cfg.UseExpandImplicit = splitShWords(merged["USE_EXPAND_IMPLICIT"])
	cfg.USE = appendUseExpand(cfg.USE, cfg.UseExpand, merged)
	// USE_EXPAND_IMPLICIT declares flags implicitly for IUSE validation, but
	// unlike USE_EXPAND it does not add the variable's values to effective USE.
	cfg.USE = applyEffectiveGlobalUse(cfg.USE, cfg.UseForce, cfg.UseMask)
	return cfg, nil
}

var effectiveGlobalVariables = map[string]bool{
	"FEATURES":       true,
	"GENTOO_MIRRORS": true,
	"USE_ORDER":      true,
}

var commandEnvironmentVariables = map[string]bool{
	// Package policy and toolchain controls.
	"USE": true, "FEATURES": true, "ACCEPT_KEYWORDS": true, "ACCEPT_LICENSE": true,
	"ARCH": true, "CHOST": true, "CBUILD": true, "CTARGET": true,
	"CFLAGS": true, "CXXFLAGS": true, "CPPFLAGS": true, "LDFLAGS": true,
	"MAKEOPTS": true, "EMERGE_DEFAULT_OPTS": true,
	// Portage roots, repositories, storage and binary-package selectors.
	"PORTAGE_CONFIGROOT": true, "ROOT": true, "SYSROOT": true, "BROOT": true,
	"PORTDIR": true, "PORTDIR_OVERLAY": true, "DISTDIR": true, "PKGDIR": true,
	"PORTAGE_TMPDIR": true, "PORTAGE_BINHOST": true,
	// One-shot presentation controls used by emerge-compatible front ends.
	"NOCOLOR": true, "TERM": true, "COLUMNS": true, "PORTAGE_NICENESS": true,
}

// ApplyCommandEnvironment overlays only documented command-facing Portage
// variables. Unknown entries are ignored, including variables that merely
// happen to resemble make.conf assignments.
func (cfg *Config) ApplyCommandEnvironment(environ []string) {
	if cfg == nil {
		return
	}
	var assignments []configAssignment
	useExpandOverride := false
	for _, entry := range environ {
		name, value, ok := strings.Cut(entry, "=")
		if ok && cfg.isCommandEnvironmentVariable(name) {
			assignments = append(assignments, configAssignment{key: name, value: value})
			for _, group := range cfg.UseExpand {
				if name == group {
					useExpandOverride = true
					break
				}
			}
			if name == "USE" {
				cfg.CommandUSE = splitShWords(value)
			}
		}
	}
	if len(assignments) == 0 {
		return
	}
	cfg.commandEnvironmentBase = make(map[string]string)
	cfg.commandEnvironmentExisted = make(map[string]bool)
	for _, assignment := range assignments {
		if _, recorded := cfg.commandEnvironmentExisted[assignment.key]; recorded {
			continue
		}
		value, existed := cfg.MakeConf[assignment.key]
		cfg.commandEnvironmentBase[assignment.key] = value
		cfg.commandEnvironmentExisted[assignment.key] = existed
	}
	cfg.commandEnvironment = append(cfg.commandEnvironment[:0], assignments...)
	mergeConfigAssignments(cfg.MakeConf, assignments)
	ResolveMakeConfRefs(cfg.MakeConf)
	cfg.populateAccessors()
	cfg.UseOrder = splitShWords(cfg.MakeConf["USE_ORDER"])
	cfg.UseExpand = splitShWords(cfg.MakeConf["USE_EXPAND"])
	cfg.UseExpandHidden = splitShWords(cfg.MakeConf["USE_EXPAND_HIDDEN"])
	cfg.UseExpandImplicit = splitShWords(cfg.MakeConf["USE_EXPAND_IMPLICIT"])
	if useExpandOverride {
		cfg.USE = removeUseExpandFlags(cfg.USE, cfg.UseExpand)
	}
	cfg.USE = appendUseExpand(cfg.USE, cfg.UseExpand, cfg.MakeConf)
	cfg.USE = applyEffectiveGlobalUse(cfg.USE, cfg.UseForce, cfg.UseMask)
}

func removeUseExpandFlags(use, groups []string) []string {
	prefixes := make([]string, 0, len(groups))
	for _, group := range groups {
		if group = strings.TrimSpace(group); group != "" {
			prefixes = append(prefixes, strings.ToLower(group)+"_")
		}
	}
	result := make([]string, 0, len(use))
	for _, flag := range use {
		name := strings.ToLower(strings.TrimPrefix(flag, "-"))
		remove := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(name, prefix) {
				remove = true
				break
			}
		}
		if !remove {
			result = append(result, flag)
		}
	}
	return result
}

func (cfg *Config) isCommandEnvironmentVariable(name string) bool {
	if commandEnvironmentVariables[name] {
		return true
	}
	// Portage accepts active USE_EXPAND variables from the command
	// environment. A static allowlist cannot cover profile-defined groups and
	// previously caused LLVM_TARGETS, ABI_X86 and language target selectors to
	// be silently ignored.
	for _, group := range cfg.UseExpand {
		if name == group {
			return true
		}
	}
	return false
}

// ExplicitUseOverride reports whether a layer with higher precedence than the
// package's IUSE defaults mentions flag. Profile make.defaults deliberately do
// not count: Portage's pkginternal layer lets IUSE="+flag" override a profile
// default, while make.conf, package.use, command USE and mask/force policy may
// override it again.
func (cfg *Config) ExplicitUseOverride(cpv, slot, repo, flag string, stable bool) bool {
	if cfg == nil || flag == "" {
		return false
	}
	mentions := func(changes []string) bool {
		for _, change := range changes {
			if strings.TrimPrefix(change, "-") == flag {
				return true
			}
		}
		return false
	}
	if mentions(cfg.UserUSE) || mentions(cfg.CommandUSE) || mentions(cfg.PackageUseFor(cpv, slot, repo)) ||
		mentions(cfg.UseForce) || mentions(cfg.UseMask) {
		return true
	}
	if stable && (mentions(cfg.UseStableForce) || mentions(cfg.UseStableMask)) {
		return true
	}
	return mentions(packagePolicyChangesFor(cfg.PackageUseForceRules, cpv, slot, repo)) ||
		mentions(packagePolicyChangesFor(cfg.PackageUseMaskRules, cpv, slot, repo)) ||
		stable && (mentions(packagePolicyChangesFor(cfg.PackageUseStableForceRules, cpv, slot, repo)) ||
			mentions(packagePolicyChangesFor(cfg.PackageUseStableMaskRules, cpv, slot, repo)))
}

// UseMaskedFor reports the final profile/package mask state for one flag.
func (cfg *Config) UseMaskedFor(cpv, slot, repo, flag string, stable bool) bool {
	masked := false
	apply := func(changes []string) {
		for _, change := range changes {
			if strings.TrimPrefix(change, "-") == flag {
				masked = !strings.HasPrefix(change, "-")
			}
		}
	}
	apply(cfg.UseMask)
	if stable {
		apply(cfg.UseStableMask)
	}
	apply(packagePolicyChangesFor(cfg.PackageUseMaskRules, cpv, slot, repo))
	if stable {
		apply(packagePolicyChangesFor(cfg.PackageUseStableMaskRules, cpv, slot, repo))
	}
	return masked
}

// UseForcedFor reports the final profile/package force state for one flag.
func (cfg *Config) UseForcedFor(cpv, slot, repo, flag string, stable bool) bool {
	forced := false
	apply := func(changes []string) {
		for _, change := range changes {
			if strings.TrimPrefix(change, "-") == flag {
				forced = !strings.HasPrefix(change, "-")
			}
		}
	}
	apply(cfg.UseForce)
	if stable {
		apply(cfg.UseStableForce)
	}
	apply(packagePolicyChangesFor(cfg.PackageUseForceRules, cpv, slot, repo))
	if stable {
		apply(packagePolicyChangesFor(cfg.PackageUseStableForceRules, cpv, slot, repo))
	}
	return forced
}

var packageExecutionEnvironmentVariables = map[string]bool{
	"USE": true, "FEATURES": true, "ACCEPT_KEYWORDS": true, "ACCEPT_LICENSE": true,
	"ARCH": true, "CHOST": true, "CBUILD": true, "CTARGET": true,
	"ABI": true, "DEFAULT_ABI": true, "MULTILIB_ABIS": true,
	"CFLAGS": true, "CXXFLAGS": true, "CPPFLAGS": true, "FFLAGS": true,
	"FCFLAGS": true, "ADAFLAGS": true, "GDCFLAGS": true, "RUSTFLAGS": true,
	"LDFLAGS": true, "MAKEOPTS": true, "NINJAFLAGS": true,
	"CC": true, "CXX": true, "CPP": true, "AR": true, "AS": true,
	"LD": true, "NM": true, "OBJCOPY": true, "OBJDUMP": true,
	"RANLIB": true, "READELF": true, "STRIP": true, "STRINGS": true,
	"FC": true, "F77": true, "OBJC": true, "OBJCXX": true,
	"PKG_CONFIG": true, "PKG_CONFIG_PATH": true, "PKG_CONFIG_LIBDIR": true,
	"PKG_CONFIG_SYSROOT_DIR": true, "GCC_SPECS": true,
}

func isPackageExecutionEnvironmentVariable(name string) bool {
	if packageExecutionEnvironmentVariables[name] {
		return true
	}
	// Portage exports the active profile's per-ABI toolchain layout.  Eclasses
	// such as multilib.eclass resolve get_abi_CHOST through these variables;
	// dropping them can silently configure a compiler for an empty target.
	for _, prefix := range []string{
		"CHOST_", "CTARGET_", "CFLAGS_", "CXXFLAGS_", "CPPFLAGS_", "LDFLAGS_", "LIBDIR_",
		"BUILD_",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// PackageExecutionEnvironmentFor reduces the three execution layers in
// Portage precedence order: effective global configuration, matching
// package.env files, then one-shot command/request overrides. The command layer
// retained by ApplyCommandEnvironment is replayed because cfg.MakeConf already
// contains it and package.env must not accidentally gain precedence over it.
func (cfg *Config) PackageExecutionEnvironmentFor(cpv, slot, repo string, request map[string]string) (map[string]string, error) {
	result := make(map[string]string)
	if cfg == nil {
		for name, value := range request {
			result[name] = value
		}
		return result, nil
	}
	for name, value := range cfg.MakeConf {
		if isPackageExecutionEnvironmentVariable(name) {
			result[name] = value
		}
	}
	for name, existed := range cfg.commandEnvironmentExisted {
		if !isPackageExecutionEnvironmentVariable(name) {
			continue
		}
		if existed {
			result[name] = cfg.commandEnvironmentBase[name]
		} else {
			delete(result, name)
		}
	}
	packageEnvironment, err := cfg.PackageEnvironmentFor(cpv, slot, repo)
	if err != nil {
		return nil, err
	}
	mergeEnvironmentMap(result, packageEnvironment)
	var command []configAssignment
	for _, assignment := range cfg.commandEnvironment {
		if isPackageExecutionEnvironmentVariable(assignment.key) {
			command = append(command, assignment)
		}
	}
	mergeConfigAssignments(result, command)
	mergeEnvironmentMap(result, request)
	ResolveMakeConfRefs(result)
	materializeUseExpandEnvironment(result, cfg.UseExpand)
	return result, nil
}

// materializeUseExpandEnvironment mirrors Portage's package execution
// environment: the final package-local USE state is projected back into each
// USE_EXPAND variable.  Ebuilds commonly consume the variable rather than its
// individual flags (for example llvm-core/llvm passes ${LLVM_TARGETS} to
// CMake), so exporting USE alone is insufficient.
func materializeUseExpandEnvironment(environment map[string]string, groups []string) {
	use := strings.Fields(environment["USE"])
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		prefix := strings.ToLower(group) + "_"
		var values []string
		for _, flag := range use {
			if strings.HasPrefix(flag, "-") || !strings.HasPrefix(strings.ToLower(flag), prefix) {
				continue
			}
			if value := flag[len(prefix):]; value != "" {
				values = append(values, value)
			}
		}
		environment[group] = strings.Join(values, " ")
	}
}

func mergeEnvironmentMap(target, layer map[string]string) {
	names := make([]string, 0, len(layer))
	for name := range layer {
		names = append(names, name)
	}
	sort.Strings(names)
	assignments := make([]configAssignment, 0, len(names))
	for _, name := range names {
		assignments = append(assignments, configAssignment{key: name, value: layer[name]})
	}
	mergeConfigAssignments(target, assignments)
}

func trackIncrementalRemovals(state map[string]map[string]bool, assignments []configAssignment) {
	for _, assignment := range assignments {
		if !removalOnlyIncrementalVariables[assignment.key] {
			continue
		}
		if state[assignment.key] == nil {
			state[assignment.key] = make(map[string]bool)
		}
		for _, token := range splitShWords(assignment.value) {
			if strings.Contains(token, "${") {
				continue
			}
			name := strings.TrimPrefix(token, "-")
			if name == "" || name == "*" {
				continue
			}
			state[assignment.key][name] = strings.HasPrefix(token, "-")
		}
	}
}

func profileRepositories(directories []string) []string {
	seen := make(map[string]bool)
	var roots []string
	marker := string(filepath.Separator) + "profiles" + string(filepath.Separator)
	for _, directory := range directories {
		clean := filepath.Clean(directory)
		index := strings.Index(clean, marker)
		if index < 0 {
			continue
		}
		root := clean[:index]
		if !seen[root] {
			seen[root] = true
			roots = append(roots, root)
		}
	}
	return roots
}

func ParseLicenseGroups(repositoryRoots []string) (map[string][]string, error) {
	groups := make(map[string][]string)
	for _, root := range repositoryRoots {
		lines, err := ReadConfigFile(filepath.Join(root, "profiles", "license_groups"))
		if err != nil {
			return nil, err
		}
		for _, line := range lines {
			fields := splitShWords(line)
			if len(fields) < 2 {
				continue
			}
			groups[fields[0]] = applyOrderedChanges(groups[fields[0]], fields[1:])
		}
	}
	return groups, nil
}

func applyOrderedChanges(previous, changes []string) []string {
	result := append([]string(nil), previous...)
	for _, change := range changes {
		if change == "-*" {
			result = nil
			continue
		}
		name := strings.TrimPrefix(change, "-")
		filtered := result[:0]
		for _, current := range result {
			if current != name {
				filtered = append(filtered, current)
			}
		}
		result = filtered
		if !strings.HasPrefix(change, "-") {
			result = append(result, change)
		}
	}
	return result
}

func ExpandLicenseGroups(values []string, groups map[string][]string) []string {
	var result []string
	var expand func(string, map[string]bool)
	expand = func(value string, stack map[string]bool) {
		negative := strings.HasPrefix(value, "-")
		name := strings.TrimPrefix(value, "-")
		if !strings.HasPrefix(name, "@") {
			result = append(result, value)
			return
		}
		group := strings.TrimPrefix(name, "@")
		members, found := groups[group]
		if !found || stack[group] {
			result = append(result, value)
			return
		}
		stack[group] = true
		for _, member := range members {
			if negative && !strings.HasPrefix(member, "-") {
				member = "-" + member
			}
			expand(member, stack)
		}
		delete(stack, group)
	}
	for _, value := range values {
		expand(value, make(map[string]bool))
	}
	return result
}

func loadProfileMaskStack(repositoryRoots, directories []string) ([]string, []string, error) {
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
	var layers []string
	for _, root := range repositoryRoots {
		layers = append(layers, filepath.Join(root, "profiles"))
	}
	layers = append(layers, directories...)
	if profilesRoot != "" && len(repositoryRoots) == 0 {
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

var removalOnlyIncrementalVariables = map[string]bool{
	"USE_EXPAND": true, "USE_EXPAND_HIDDEN": true, "USE_EXPAND_IMPLICIT": true,
	"FEATURES": true,
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

func parseSelectedAssignments(path string, selected map[string]bool) ([]configAssignment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	physical := strings.Split(string(data), "\n")
	var result []configAssignment
	for index := 0; index < len(physical); index++ {
		line := strings.TrimSpace(physical[index])
		pos := strings.IndexByte(line, '=')
		if pos < 1 || !selected[strings.TrimSpace(line[:pos])] {
			continue
		}
		logical := line
		for hasUnclosedShellQuote(logical) && index+1 < len(physical) {
			index++
			logical += " " + strings.TrimSpace(physical[index])
		}
		pos = strings.IndexByte(logical, '=')
		result = append(result, configAssignment{
			key: strings.TrimSpace(logical[:pos]), value: unquote(strings.TrimSpace(logical[pos+1:])),
		})
	}
	return result, nil
}

func dropNegativeTokens(value string) string {
	return strings.Join(applyOrderedChanges(nil, splitShWords(value)), " ")
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
	return cfg.EffectiveUseForStability(cpv, slot, repo, false)
}

func (cfg *Config) EffectiveUseForStability(cpv, slot, repo string, stable bool) map[string]bool {
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

	candidateCP := cpv
	if candidate, err := atom.Parse(cpv); err == nil {
		candidateCP = candidate.CP()
	}
	force := append([]string(nil), cfg.UseForce...)
	mask := append([]string(nil), cfg.UseMask...)
	if stable {
		force = applyOrderedChanges(force, cfg.UseStableForce)
		mask = applyOrderedChanges(mask, cfg.UseStableMask)
	}
	forceChanges := packagePolicyChangesFor(cfg.PackageUseForceRules, cpv, slot, repo)
	if len(forceChanges) == 0 {
		forceChanges = cfg.PackageUseForce[candidateCP]
	}
	maskChanges := packagePolicyChangesFor(cfg.PackageUseMaskRules, cpv, slot, repo)
	if len(maskChanges) == 0 {
		maskChanges = cfg.PackageUseMask[candidateCP]
	}
	force = applyOrderedChanges(force, forceChanges)
	mask = applyOrderedChanges(mask, maskChanges)
	if stable {
		force = applyOrderedChanges(force, packagePolicyChangesFor(cfg.PackageUseStableForceRules, cpv, slot, repo))
		mask = applyOrderedChanges(mask, packagePolicyChangesFor(cfg.PackageUseStableMaskRules, cpv, slot, repo))
	}
	applyPolicy(force, true)
	applyPolicy(mask, false)
	return result
}

func packagePolicyChangesFor(rules []PackageUseRule, cpv, slot, repo string) []string {
	var result []string
	for _, rule := range rules {
		if PackageAtomMatches(rule.Atom, cpv, slot, repo) {
			result = append(result, rule.Flags...)
		}
	}
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
	cfg.PackageAcceptKeywordRules, err = ParsePackageAcceptKeywordRules(filepath.Join(root, "package.accept_keywords"))
	if err != nil {
		return fmt.Errorf("portage: could not parse ordered package.accept_keywords: %w", err)
	}

	cfg.PackageLicense, err = ParsePackageLicense(filepath.Join(root, "package.license"))
	if err != nil {
		return fmt.Errorf("portage: could not parse package.license: %w", err)
	}
	cfg.PackageLicenseRules, err = ParsePackageLicenseRules(filepath.Join(root, "package.license"))
	if err != nil {
		return fmt.Errorf("portage: could not parse ordered package.license: %w", err)
	}

	cfg.PackageMask, err = ParsePackageMask(filepath.Join(root, "package.mask"))
	if err != nil {
		return fmt.Errorf("portage: could not parse package.mask: %w", err)
	}
	cfg.PackageMaskRules, err = ParsePackageMaskRules(filepath.Join(root, "package.mask"))
	if err != nil {
		return fmt.Errorf("portage: could not parse package.mask reasons: %w", err)
	}

	cfg.PackageUnmask, err = ParsePackageUnmask(filepath.Join(root, "package.unmask"))
	if err != nil {
		return fmt.Errorf("portage: could not parse package.unmask: %w", err)
	}

	cfg.PackageEnv, err = ParsePackageEnv(filepath.Join(root, "package.env"))
	if err != nil {
		return fmt.Errorf("portage: could not parse package.env: %w", err)
	}
	cfg.PackageEnvRules, err = ParsePackageEnvRules(filepath.Join(root, "package.env"))
	if err != nil {
		return fmt.Errorf("portage: could not parse ordered package.env: %w", err)
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

func hasUnclosedShellQuote(value string) bool {
	var quote byte
	escaped := false
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote == 0 && (ch == '\'' || ch == '"') {
			quote = ch
		} else if ch == quote {
			quote = 0
		}
	}
	return quote != 0
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
	if rawRule == "*/*" {
		return true
	}
	candidate, valid := cachedPolicyAtom(cpv)
	if !valid {
		return false
	}
	rule, valid := cachedPolicyAtom(rawRule)
	if !valid {
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
	candidateSlot, candidateSubslot := slot, ""
	if before, after, found := strings.Cut(slot, "/"); found {
		candidateSlot, candidateSubslot = before, after
	}
	if rule.Slot != "" && rule.Slot != candidateSlot {
		return false
	}
	if rule.Subslot != "" && rule.Subslot != candidateSubslot {
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
	for _, rule := range cfg.PackageMaskRules {
		if PackageAtomMatches(rule.Atom, cpv, slot, repo) {
			status = MaskStatus{Masked: true, Atom: rule.Atom, Source: rule.Source, Reason: rule.Reason}
		}
	}
	for _, entry := range cfg.PackageMask {
		if PackageAtomMatches(entry, cpv, slot, repo) {
			if status.Atom != entry {
				status = MaskStatus{Masked: true, Atom: entry, Source: "package.mask"}
			}
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

// ParsePackageAcceptKeywordRules preserves file order and repeated or
// overlapping atoms. Keyword configuration is incremental, so reducing it to
// a map loses both removal operations and precedence.
func ParsePackageAcceptKeywordRules(path string) ([]PackageUseRule, error) {
	lines, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	var rules []PackageUseRule
	for _, line := range lines {
		ruleAtom, keywords := parseAtomConfig(line)
		if ruleAtom == "" {
			continue
		}
		rules = append(rules, PackageUseRule{Atom: ruleAtom, Flags: splitShWords(keywords)})
	}
	return rules, nil
}

// PackageAcceptKeywordsFor returns ordered keyword changes for a concrete
// package. An empty matching rule means the unstable keyword for the host
// architecture, as in Portage.
func (cfg *Config) PackageAcceptKeywordsFor(cpv, slot, repo, arch string) []string {
	if cfg == nil {
		return nil
	}
	var result []string
	if len(cfg.PackageAcceptKeywordRules) == 0 {
		for rule, keywords := range cfg.PackageAcceptKeywords {
			if PackageAtomMatches(rule, cpv, slot, repo) {
				values := splitShWords(keywords)
				if len(values) == 0 {
					values = []string{"~" + arch}
				}
				result = append(result, values...)
			}
		}
		return result
	}
	for _, rule := range cfg.PackageAcceptKeywordRules {
		if !PackageAtomMatches(rule.Atom, cpv, slot, repo) {
			continue
		}
		if len(rule.Flags) == 0 {
			result = append(result, "~"+arch)
		} else {
			result = append(result, rule.Flags...)
		}
	}
	return result
}

func (cfg *Config) KeywordAcceptedFor(cpv, slot, repo, keywords, arch string) bool {
	if cfg == nil {
		return true
	}
	accepted := []string{arch}
	accepted = applyOrderedChanges(accepted, cfg.ACCEPT_KEYWORDS)
	accepted = applyOrderedChanges(accepted, cfg.PackageAcceptKeywordsFor(cpv, slot, repo, arch))
	for _, allow := range accepted {
		if allow == "**" {
			return true
		}
		for _, keyword := range strings.Fields(keywords) {
			if strings.HasPrefix(keyword, "-") {
				continue
			}
			if allow == keyword || allow == "*" && !strings.HasPrefix(keyword, "~") ||
				allow == "~*" && strings.HasPrefix(keyword, "~") {
				return true
			}
		}
	}
	return false
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

func ParsePackageLicenseRules(path string) ([]PackageUseRule, error) {
	lines, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	var rules []PackageUseRule
	for _, line := range lines {
		ruleAtom, licenses := parseAtomConfig(line)
		if ruleAtom != "" {
			rules = append(rules, PackageUseRule{Atom: ruleAtom, Flags: splitShWords(licenses)})
		}
	}
	return rules, nil
}

func (cfg *Config) PackageLicensesFor(cpv, slot, repo string) []string {
	if cfg == nil {
		return nil
	}
	var result []string
	if len(cfg.PackageLicenseRules) == 0 {
		for rule, licenses := range cfg.PackageLicense {
			if PackageAtomMatches(rule, cpv, slot, repo) {
				result = append(result, splitShWords(licenses)...)
			}
		}
		return result
	}
	for _, rule := range cfg.PackageLicenseRules {
		if PackageAtomMatches(rule.Atom, cpv, slot, repo) {
			result = append(result, rule.Flags...)
		}
	}
	return result
}

func ParsePackageMask(path string) ([]string, error) {
	return parseAtomList(path)
}

func ParsePackageMaskRules(path string) ([]PackageMaskRule, error) {
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
		for _, entry := range entries {
			if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
				files = append(files, filepath.Join(path, entry.Name()))
			}
		}
		sort.Strings(files)
	} else {
		files = []string{path}
	}
	var rules []PackageMaskRule
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		var comments []string
		sawAtom := false
		for _, raw := range strings.Split(string(data), "\n") {
			line := strings.TrimSpace(raw)
			switch {
			case line == "":
				comments, sawAtom = nil, false
			case strings.HasPrefix(line, "#"):
				if sawAtom {
					comments, sawAtom = nil, false
				}
				text := strings.TrimSpace(strings.TrimPrefix(line, "#"))
				if text != "" && !strings.HasPrefix(text, "---") {
					comments = append(comments, text)
				}
			default:
				entry, _ := parseAtomConfig(line)
				if entry != "" {
					rules = append(rules, PackageMaskRule{entry, file, strings.Join(comments, " ")})
					sawAtom = true
				}
			}
		}
	}
	return rules, nil
}

func applyPackageMaskRuleChanges(previous, changes []PackageMaskRule) []PackageMaskRule {
	result := append([]PackageMaskRule(nil), previous...)
	for _, change := range changes {
		if strings.HasPrefix(change.Atom, "-") {
			remove := strings.TrimPrefix(change.Atom, "-")
			filtered := result[:0]
			for _, current := range result {
				if current.Atom != remove {
					filtered = append(filtered, current)
				}
			}
			result = filtered
		} else {
			result = append(result, change)
		}
	}
	return result
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

func ParsePackageEnvRules(path string) ([]PackageUseRule, error) {
	lines, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	var rules []PackageUseRule
	for _, line := range lines {
		ruleAtom, files := parseAtomConfig(line)
		if ruleAtom != "" {
			rules = append(rules, PackageUseRule{Atom: ruleAtom, Flags: splitShWords(files)})
		}
	}
	return rules, nil
}

func (cfg *Config) PackageEnvFilesFor(cpv, slot, repo string) []string {
	if cfg == nil {
		return nil
	}
	var result []string
	if len(cfg.PackageEnvRules) == 0 {
		for rule, files := range cfg.PackageEnv {
			if PackageAtomMatches(rule, cpv, slot, repo) {
				result = append(result, splitShWords(files)...)
			}
		}
		return result
	}
	for _, rule := range cfg.PackageEnvRules {
		if PackageAtomMatches(rule.Atom, cpv, slot, repo) {
			result = append(result, rule.Flags...)
		}
	}
	return result
}

// PackageEnvironmentFor composes referenced /etc/portage/env files in
// package.env rule order. Later files override scalar variables, while the
// standard incremental variables retain their ordered add/remove semantics.
func (cfg *Config) PackageEnvironmentFor(cpv, slot, repo string) (map[string]string, error) {
	result := make(map[string]string)
	if cfg == nil {
		return result, nil
	}
	envRoot := filepath.Join(cfg.ConfigRoot, "env")
	for _, name := range cfg.PackageEnvFilesFor(cpv, slot, repo) {
		if name == "" || filepath.IsAbs(name) {
			return nil, fmt.Errorf("portage: invalid package.env file %q", name)
		}
		path := filepath.Clean(filepath.Join(envRoot, name))
		if path != envRoot && !strings.HasPrefix(path, envRoot+string(filepath.Separator)) {
			return nil, fmt.Errorf("portage: package.env file %q escapes %s", name, envRoot)
		}
		assignments, err := parseMakeConfAssignments(path)
		if err != nil {
			return nil, fmt.Errorf("portage: parse package environment %s: %w", name, err)
		}
		mergeConfigAssignments(result, assignments)
	}
	ResolveMakeConfRefs(result)
	return result, nil
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
	Masters  []string
}

// EclassLookupDirectories returns the selected repository followed by its
// declared masters. Lookup precedence is intentionally child-first.
func EclassLookupDirectories(entries []RepoEntry, selected string) ([]string, error) {
	byName := make(map[string]RepoEntry, len(entries))
	for _, entry := range entries {
		byName[entry.Name] = entry
	}
	var directories []string
	state := make(map[string]uint8)
	var visit func(string) error
	visit = func(name string) error {
		if state[name] == 1 {
			return fmt.Errorf("portage: repository master cycle at %s", name)
		}
		if state[name] == 2 {
			return nil
		}
		entry, ok := byName[name]
		if !ok || entry.Location == "" {
			return fmt.Errorf("portage: repository %s has no configured location", name)
		}
		state[name] = 1
		directories = append(directories, filepath.Join(entry.Location, "eclass"))
		for _, master := range entry.Masters {
			if err := visit(master); err != nil {
				return err
			}
		}
		state[name] = 2
		return nil
	}
	if err := visit(selected); err != nil {
		return nil, err
	}
	return directories, nil
}

// UserPatchDirectories reproduces Portage's least-to-most-specific user patch
// order. Later directories override equal patch basenames.
func UserPatchDirectories(configRoot, category, pn, p, pr, slot string) []string {
	base := filepath.Join(configRoot, "etc", "portage", "patches", category)
	slot = strings.SplitN(slot, "/", 2)[0]
	var result []string
	for _, packageName := range []string{pn, p, p + "-" + pr} {
		result = append(result, filepath.Join(base, packageName))
		if slot != "" {
			result = append(result, filepath.Join(base, packageName+":"+slot))
		}
	}
	return result
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
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") || strings.HasSuffix(e.Name(), "~") {
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

// RepositoryPolicyOrder returns repositories in deterministic master-before-
// child order. Repository masters come from metadata/layout.conf, while
// repos.conf file/section order breaks ties between independent repositories.
func RepositoryPolicyOrder(reposConfPath string) ([]RepoEntry, error) {
	entries, err := ReadReposConf(reposConfPath)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]*RepoEntry)
	var names []string
	for i := range entries {
		entry := &entries[i]
		if entry.Name == "DEFAULT" || entry.Name == "" {
			continue
		}
		if _, exists := byName[entry.Name]; exists {
			continue
		}
		if entry.Location == "" || !pathIsDirectory(entry.Location) {
			fallback := filepath.Join("/var/db/repos", entry.Name)
			if pathIsDirectory(fallback) {
				entry.Location = fallback
			}
		}
		entry.Masters = readRepositoryMasters(entry.Location)
		byName[entry.Name] = entry
		names = append(names, entry.Name)
	}
	var order []RepoEntry
	state := make(map[string]uint8)
	var visit func(string) error
	visit = func(name string) error {
		if state[name] == 2 {
			return nil
		}
		if state[name] == 1 {
			return fmt.Errorf("portage: repository master cycle at %s", name)
		}
		entry := byName[name]
		if entry == nil {
			return fmt.Errorf("portage: repository master %s is not configured", name)
		}
		state[name] = 1
		for _, master := range entry.Masters {
			if err := visit(master); err != nil {
				return err
			}
		}
		state[name] = 2
		order = append(order, *entry)
		return nil
	}
	for _, name := range names {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return order, nil
}

func pathIsDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func readRepositoryMasters(root string) []string {
	data, err := os.ReadFile(filepath.Join(root, "metadata", "layout.conf"))
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, found := strings.Cut(line, "=")
		if found && strings.TrimSpace(key) == "masters" {
			return strings.Fields(strings.TrimSpace(value))
		}
	}
	return nil
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
