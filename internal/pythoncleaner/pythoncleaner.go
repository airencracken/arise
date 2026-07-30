// Package pythoncleaner inventories Gentoo Python interpreter transitions and
// builds typed repair evidence without executing Python or Portage.
package pythoncleaner

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/depstring"
	"github.com/airencracken/arise/internal/vdb"
)

type Policy struct {
	Targets      []string `json:"targets"`
	SingleTarget string   `json:"single_target,omitempty"`
	Preference   []string `json:"preference"`
}

type Interpreter struct {
	Target  string `json:"target"`
	Version string `json:"version"`
	CPV     string `json:"cpv"`
	Slot    string `json:"slot"`
}

type Reason struct {
	Kind     string `json:"kind"`
	Evidence string `json:"evidence"`
	Target   string `json:"target,omitempty"`
}

type Consumer struct {
	CPV              string   `json:"cpv"`
	Atom             string   `json:"atom"`
	EnabledTargets   []string `json:"enabled_targets"`
	SupportedTargets []string `json:"supported_targets"`
	Dependencies     []string `json:"dependencies"`
	Reasons          []Reason `json:"reasons"`
}

type Removal struct {
	Interpreter Interpreter `json:"interpreter"`
	Blockers    []string    `json:"blockers"`
	Safe        bool        `json:"safe"`
}

type Orphan struct {
	Path   string `json:"path"`
	Target string `json:"target"`
}

type Report struct {
	Policy         Policy        `json:"policy"`
	Interpreters   []Interpreter `json:"interpreters"`
	Missing        []string      `json:"missing_targets"`
	Consumers      []Consumer    `json:"consumers"`
	Removals       []Removal     `json:"removals"`
	Orphans        []Orphan      `json:"orphans"`
	OmittedOrphans int           `json:"omitted_orphans"`
}

var (
	pythonPathRE = regexp.MustCompile(`/usr/lib(?:32|64|x32)?/python([0-9]+\.[0-9]+)(?:/|$)`)
	libPythonRE  = regexp.MustCompile(`^libpython([0-9]+\.[0-9]+)[^,]*`)
	shebangRE    = regexp.MustCompile(`^#![ \t]*(?:/usr/bin/env(?:[ \t]+-S)?[ \t]+|/usr/bin/)(python[0-9]+\.[0-9]+)(?:[ \t]|$)`)
)

func Check(vdbRoot, root string, policy Policy) (Report, error) {
	policy.Targets = normalizedTargets(policy.Targets)
	policy.Preference = orderedTargets(policy.Preference)
	if policy.SingleTarget != "" {
		normalized := normalizeTarget(policy.SingleTarget)
		if normalized == "" {
			return Report{}, fmt.Errorf("python-cleaner: invalid PYTHON_SINGLE_TARGET %q", policy.SingleTarget)
		}
		policy.SingleTarget = normalized
	}
	if len(policy.Targets) == 0 {
		return Report{}, fmt.Errorf("python-cleaner: effective PYTHON_TARGETS is empty")
	}
	packages, err := vdb.Scan(vdbRoot)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		Policy: policy, Interpreters: []Interpreter{}, Missing: []string{},
		Consumers: []Consumer{}, Removals: []Removal{}, Orphans: []Orphan{},
	}
	installedTargets := map[string]Interpreter{}
	for _, pkg := range packages {
		if pkg.CP() != "dev-lang/python" {
			continue
		}
		target := versionTarget(pkg.Slot)
		if target == "" {
			continue
		}
		interpreter := Interpreter{Target: target, Version: pkg.Version, CPV: pkg.CPV(), Slot: pkg.Slot}
		report.Interpreters = append(report.Interpreters, interpreter)
		installedTargets[target] = interpreter
	}
	sort.Slice(report.Interpreters, func(i, j int) bool { return report.Interpreters[i].Target < report.Interpreters[j].Target })
	for _, target := range policy.Targets {
		if _, exists := installedTargets[target]; !exists {
			report.Missing = append(report.Missing, target)
		}
	}

	blockers := make(map[string]map[string]bool)
	unknownBlockers := map[string]bool{}
	for _, pkg := range packages {
		if pkg.CP() == "dev-lang/python" || pkg.CP() == "dev-lang/python-exec" || pkg.CP() == "dev-lang/python-exec-conf" {
			continue
		}
		enabled := flagsWithPrefix(pkg.Use, "python_targets_")
		single := flagsWithPrefix(pkg.Use, "python_single_target_")
		enabled = append(enabled, single...)
		enabled = normalizedTargets(enabled)
		supported := flagsWithPrefix(pkg.IUse, "python_targets_")
		supported = append(supported, flagsWithPrefix(pkg.IUse, "python_single_target_")...)
		supported = normalizedTargets(supported)
		reasons, err := packageReasons(vdbRoot, root, pkg, policy, enabled, supported)
		if err != nil {
			return Report{}, err
		}
		if len(reasons) == 0 {
			continue
		}
		dependencies, err := packageDependencyCPs(pkg)
		if err != nil {
			return Report{}, fmt.Errorf("python-cleaner: parse dependencies for %s: %w", pkg.CPV(), err)
		}
		for _, reason := range reasons {
			if reason.Kind == "shebang-probe-unavailable" {
				unknownBlockers[pkg.CP()] = true
			}
			if reason.Target != "" && !contains(policy.Targets, reason.Target) {
				if blockers[reason.Target] == nil {
					blockers[reason.Target] = map[string]bool{}
				}
				blockers[reason.Target][pkg.CP()] = true
			}
		}
		report.Consumers = append(report.Consumers, Consumer{
			CPV: pkg.CPV(), Atom: pkg.CP() + ":" + pkg.Slot,
			EnabledTargets: enabled, SupportedTargets: supported,
			Dependencies: dependencies, Reasons: reasons,
		})
	}
	sort.Slice(report.Consumers, func(i, j int) bool { return report.Consumers[i].CPV < report.Consumers[j].CPV })
	for _, interpreter := range report.Interpreters {
		if contains(policy.Targets, interpreter.Target) {
			continue
		}
		var blocked []string
		for cp := range blockers[interpreter.Target] {
			blocked = append(blocked, cp)
		}
		for cp := range unknownBlockers {
			blocked = append(blocked, cp+" (probe unavailable)")
		}
		if contains(policy.Preference, interpreter.Target) {
			blocked = append(blocked, "python-exec preference")
		}
		sort.Strings(blocked)
		report.Removals = append(report.Removals, Removal{
			Interpreter: interpreter, Blockers: blocked, Safe: len(blocked) == 0,
		})
	}
	report.Orphans, report.OmittedOrphans, err = findOrphans(root, packages, policy.Targets, 1000)
	if err != nil {
		return Report{}, err
	}
	return report, nil
}

func packageDependencyCPs(pkg vdb.Package) ([]string, error) {
	seen := map[string]bool{}
	for name, value := range map[string]string{
		"DEPEND": pkg.Depend, "RDEPEND": pkg.RDepend, "BDEPEND": pkg.BDepend,
		"IDEPEND": pkg.IDepend, "PDEPEND": pkg.PDepend,
	} {
		if strings.TrimSpace(value) == "" {
			continue
		}
		tree, err := depstring.Parse(value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		for _, raw := range tree.Atoms() {
			parsed, err := atom.Parse(raw)
			if err != nil || parsed.Category == "" || parsed.Package == "" {
				return nil, fmt.Errorf("%s: invalid atom %q", name, raw)
			}
			seen[parsed.CP()] = true
		}
	}
	result := make([]string, 0, len(seen))
	for cp := range seen {
		result = append(result, cp)
	}
	sort.Strings(result)
	return result, nil
}

func findOrphans(root string, packages []vdb.Package, desired []string, limit int) ([]Orphan, int, error) {
	owned := map[string]bool{}
	for _, pkg := range packages {
		scanner := bufio.NewScanner(strings.NewReader(pkg.Contents))
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 2 && (fields[0] == "obj" || fields[0] == "sym") && filepath.IsAbs(fields[1]) {
				owned[filepath.Clean(fields[1])] = true
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, 0, err
		}
	}
	var result []Orphan
	omitted := 0
	for _, relative := range []string{"usr/lib", "usr/lib32", "usr/lib64", "usr/libx32"} {
		base := filepath.Join(root, relative)
		entries, err := os.ReadDir(base)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "python") {
				continue
			}
			target := normalizeTarget(entry.Name())
			if target == "" || contains(desired, target) {
				continue
			}
			tree := filepath.Join(base, entry.Name(), "site-packages")
			err := filepath.WalkDir(tree, func(path string, item os.DirEntry, walkErr error) error {
				if os.IsNotExist(walkErr) {
					return filepath.SkipDir
				}
				if walkErr != nil {
					return walkErr
				}
				if item.IsDir() {
					return nil
				}
				if item.Type()&os.ModeSymlink != 0 {
					return nil
				}
				info, err := item.Info()
				if err != nil {
					return err
				}
				if !info.Mode().IsRegular() {
					return nil
				}
				if strings.HasSuffix(item.Name(), ".pyc") || strings.HasSuffix(item.Name(), ".pyo") ||
					strings.Contains(filepath.ToSlash(path), "/__pycache__/") {
					return nil
				}
				relativeToRoot, err := filepath.Rel(root, path)
				if err != nil || filepath.IsAbs(relativeToRoot) || strings.HasPrefix(relativeToRoot, ".."+string(filepath.Separator)) {
					return fmt.Errorf("python-cleaner: orphan path escaped root: %s", path)
				}
				display := "/" + filepath.ToSlash(relativeToRoot)
				if owned[filepath.Clean(display)] {
					return nil
				}
				if limit <= 0 || len(result) < limit {
					result = append(result, Orphan{Path: path, Target: target})
				} else {
					omitted++
				}
				return nil
			})
			if err != nil && !os.IsNotExist(err) {
				return nil, 0, err
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, omitted, nil
}

func packageReasons(vdbRoot, root string, pkg vdb.Package, policy Policy, enabled, supported []string) ([]Reason, error) {
	var reasons []Reason
	if len(enabled) != 0 && !intersects(enabled, policy.Targets) && intersects(supported, policy.Targets) {
		for _, target := range enabled {
			if !contains(policy.Targets, target) {
				reasons = append(reasons, Reason{
					Kind: "targets-outside-policy", Evidence: strings.Join(enabled, ","), Target: target,
				})
			}
		}
	}
	for _, version := range pythonVersionsInContents(pkg.Contents) {
		target := versionTarget(version)
		if !contains(policy.Targets, target) {
			reasons = append(reasons, Reason{
				Kind: "stale-python-path", Evidence: "python" + version, Target: target,
			})
		}
	}
	neededPath := filepath.Join(vdbRoot, pkg.Category, pkg.Package+"-"+pkg.Version, "NEEDED.ELF.2")
	linked, elfPaths, err := neededPythonEvidence(neededPath)
	if err != nil {
		return nil, fmt.Errorf("python-cleaner: inspect %s linkage: %w", pkg.CPV(), err)
	}
	for _, version := range linked {
		target := versionTarget(version)
		if !contains(policy.Targets, target) {
			reasons = append(reasons, Reason{
				Kind: "stale-libpython", Evidence: "libpython" + version, Target: target,
			})
		}
	}
	shebangs, err := staleShebangs(root, pkg.Contents, policy.Targets, elfPaths)
	if err != nil {
		return nil, fmt.Errorf("python-cleaner: inspect %s shebangs: %w", pkg.CPV(), err)
	}
	reasons = append(reasons, shebangs...)
	return uniqueReasons(reasons), nil
}

func staleShebangs(root, contents string, desired []string, elfPaths map[string]bool) ([]Reason, error) {
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var reasons []Reason
	scanner := bufio.NewScanner(strings.NewReader(contents))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || fields[0] != "obj" {
			continue
		}
		recorded := fields[1]
		if !strings.HasPrefix(recorded, "/usr/bin/") && !strings.HasPrefix(recorded, "/usr/sbin/") {
			continue
		}
		if elfPaths[recorded] {
			continue
		}
		path := filepath.Join(cleanRoot, filepath.FromSlash(strings.TrimPrefix(recorded, "/")))
		relative, err := filepath.Rel(cleanRoot, path)
		if err != nil || relative == "." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("path escaped root: %q", recorded)
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			reasons = append(reasons, Reason{Kind: "shebang-probe-unavailable", Evidence: recorded + ": " + err.Error()})
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			reasons = append(reasons, Reason{Kind: "shebang-probe-unavailable", Evidence: recorded + ": " + err.Error()})
			continue
		}
		reader := bufio.NewReaderSize(file, 4096)
		line, readErr := reader.ReadString('\n')
		closeErr := file.Close()
		if readErr != nil && len(line) == 0 {
			if closeErr != nil {
				return nil, closeErr
			}
			continue
		}
		if closeErr != nil {
			return nil, closeErr
		}
		match := shebangRE.FindStringSubmatch(strings.TrimSpace(line))
		if len(match) != 2 {
			continue
		}
		target := normalizeTarget(match[1])
		if !contains(desired, target) {
			reasons = append(reasons, Reason{Kind: "stale-shebang", Evidence: recorded, Target: target})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return reasons, nil
}

func ParsePreference(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var result []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		target := normalizeTarget(line)
		if target == "" {
			return nil, fmt.Errorf("python-cleaner: invalid python-exec preference %q", line)
		}
		result = append(result, target)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return orderedTargets(result), nil
}

func pythonVersionsInContents(contents string) []string {
	matches := pythonPathRE.FindAllStringSubmatch(contents, -1)
	var result []string
	for _, match := range matches {
		result = append(result, match[1])
	}
	sort.Strings(result)
	return uniqueStrings(result)
}

func linkedPythonVersions(path string) ([]string, error) {
	versions, _, err := neededPythonEvidence(path)
	return versions, err
}

func neededPythonEvidence(path string) ([]string, map[string]bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []string{}, map[string]bool{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var result []string
	elfPaths := map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ";")
		if len(fields) != 6 {
			return nil, nil, fmt.Errorf("python-cleaner: malformed NEEDED.ELF.2 record %q", scanner.Text())
		}
		elfPaths[fields[1]] = true
		for _, needed := range strings.Split(fields[4], ",") {
			if match := libPythonRE.FindStringSubmatch(needed); len(match) == 2 {
				result = append(result, match[1])
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	sort.Strings(result)
	return uniqueStrings(result), elfPaths, nil
}

func flagsWithPrefix(flags []string, prefix string) []string {
	var result []string
	for _, flag := range flags {
		flag = strings.TrimPrefix(flag, "+")
		if strings.HasPrefix(flag, prefix) {
			result = append(result, strings.TrimPrefix(flag, prefix))
		}
	}
	return result
}

func normalizedTargets(values []string) []string {
	var result []string
	for _, value := range values {
		if target := normalizeTarget(value); target != "" {
			result = append(result, target)
		}
	}
	sort.Strings(result)
	return uniqueStrings(result)
}

func orderedTargets(values []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, value := range values {
		target := normalizeTarget(value)
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		result = append(result, target)
	}
	return result
}

func normalizeTarget(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(value, "-"))
	if strings.HasPrefix(value, "python_targets_") {
		value = strings.TrimPrefix(value, "python_targets_")
	}
	if strings.HasPrefix(value, "python_single_target_") {
		value = strings.TrimPrefix(value, "python_single_target_")
	}
	if strings.HasPrefix(value, "python") {
		version := strings.TrimPrefix(value, "python")
		version = strings.ReplaceAll(version, "_", ".")
		if version != "" {
			return "python" + strings.ReplaceAll(version, ".", "_")
		}
	}
	return ""
}

func versionTarget(version string) string {
	parts := strings.Split(strings.TrimPrefix(version, "python"), ".")
	if len(parts) < 2 {
		return ""
	}
	return "python" + parts[0] + "_" + parts[1]
}

func uniqueReasons(values []Reason) []Reason {
	sort.Slice(values, func(i, j int) bool {
		if values[i].Kind != values[j].Kind {
			return values[i].Kind < values[j].Kind
		}
		if values[i].Target != values[j].Target {
			return values[i].Target < values[j].Target
		}
		return values[i].Evidence < values[j].Evidence
	})
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func uniqueStrings(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func intersects(left, right []string) bool {
	for _, value := range left {
		if contains(right, value) {
			return true
		}
	}
	return false
}
