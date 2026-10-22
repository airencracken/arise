package portage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/airencracken/gentooling"
)

type ProfileCandidate struct {
	Selector   string        `json:"selector"`
	Path       string        `json:"path"`
	Arch       string        `json:"architecture"`
	Status     string        `json:"status"`
	Deprecated bool          `json:"deprecated"`
	Selected   bool          `json:"selected"`
	Source     ProfileSource `json:"source"`
}

type ProfileCatalog struct {
	Schema     string             `json:"schema"`
	ActivePath string             `json:"active_path"`
	Arch       string             `json:"architecture"`
	All        bool               `json:"all_architectures"`
	Profiles   []ProfileCandidate `json:"profiles"`
}

// ListProfiles discovers repository-advertised profiles. Matching architecture
// is a discovery filter, not a prediction that a profile transition is safe.
func ListProfiles(ctx context.Context, configRoot string, all bool) (ProfileCatalog, error) {
	paths, err := profileInspectionPaths(configRoot)
	if err != nil {
		return ProfileCatalog{}, err
	}
	active, err := inspectProfilePaths(ctx, paths)
	if err != nil {
		return ProfileCatalog{}, err
	}
	arch := profileArchitecture(active)
	if arch == "" && !all {
		return ProfileCatalog{}, fmt.Errorf("active profile has no ARCH; use profile list --all to list all architectures")
	}
	result := ProfileCatalog{Schema: "arise.profile-catalog.v1", ActivePath: active.ActivePath, Arch: arch, All: all, Profiles: []ProfileCandidate{}}
	for _, repository := range paths.Repositories {
		entries, err := readProfileContributions(ctx, filepath.Join(repository.Path, "profiles", "profiles.desc"))
		if err != nil {
			return ProfileCatalog{}, err
		}
		for _, entry := range entries {
			text, _, _ := strings.Cut(entry.Value, "#")
			fields := strings.Fields(text)
			if len(fields) != 3 {
				return ProfileCatalog{}, fmt.Errorf("malformed profile description at %s:%d", entry.Source.Path, entry.Source.Line)
			}
			if !all && fields[0] != arch {
				continue
			}
			path, err := resolveRepositoryProfile(repository, fields[1])
			if err != nil {
				return ProfileCatalog{}, fmt.Errorf("%s:%d: %w", entry.Source.Path, entry.Source.Line, err)
			}
			_, deprecatedErr := os.Lstat(filepath.Join(path, "deprecated"))
			if deprecatedErr != nil && !os.IsNotExist(deprecatedErr) {
				return ProfileCatalog{}, deprecatedErr
			}
			result.Profiles = append(result.Profiles, ProfileCandidate{
				Selector: repository.Name + ":" + fields[1], Path: path, Arch: fields[0], Status: fields[2],
				Deprecated: deprecatedErr == nil, Selected: path == active.ActivePath, Source: entry.Source,
			})
		}
	}
	sort.SliceStable(result.Profiles, func(i, j int) bool {
		if result.Profiles[i].Selector == result.Profiles[j].Selector {
			return result.Profiles[i].Arch < result.Profiles[j].Arch
		}
		return result.Profiles[i].Selector < result.Profiles[j].Selector
	})
	return result, nil
}

func profileArchitecture(report ProfileReport) string {
	for _, value := range report.Defaults {
		if value.Name == "ARCH" {
			return value.Value
		}
	}
	return ""
}

func resolveRepositoryProfile(repository gentooling.RepositoryPath, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", fmt.Errorf("require a relative profile path in repository %s", repository.Name)
	}
	for _, component := range strings.Split(filepath.ToSlash(name), "/") {
		if component == ".." {
			return "", fmt.Errorf("profile path %q contains parent traversal", name)
		}
	}
	root := filepath.Join(repository.Path, "profiles")
	path := filepath.Join(root, name)
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if realPath != realRoot && !strings.HasPrefix(realPath, realRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("profile %q escapes repository %s", name, repository.Name)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("profile %q is not a directory", path)
	}
	return path, nil
}

func resolveProfileSelector(paths gentooling.SystemPaths, selector string) (string, error) {
	if repositoryName, name, qualified := strings.Cut(selector, ":"); qualified && !filepath.IsAbs(selector) {
		for _, repository := range paths.Repositories {
			if repository.Name == repositoryName {
				return resolveRepositoryProfile(repository, name)
			}
		}
		return "", fmt.Errorf("unknown profile repository %q", repositoryName)
	}
	var matches []string
	for _, repository := range paths.Repositories {
		name := selector
		if filepath.IsAbs(selector) {
			var err error
			name, err = filepath.Rel(filepath.Join(repository.Path, "profiles"), selector)
			if err != nil {
				continue
			}
		}
		path, err := resolveRepositoryProfile(repository, name)
		if err == nil {
			matches = append(matches, path)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("ambiguous profile %q; use REPOSITORY:PROFILE", selector)
	}
	return "", fmt.Errorf("profile %q is not a readable profile directory in a configured repository", selector)
}

// The shared graph loader currently accepts a make.profile symlink. Adapt an
// explicit directory with a private temporary link, never the live selection.
func inspectProfileSelector(ctx context.Context, paths gentooling.SystemPaths, selector string) (ProfileReport, error) {
	if selector == "active" {
		return inspectProfilePaths(ctx, paths)
	}
	if err := ctx.Err(); err != nil {
		return ProfileReport{}, err
	}
	path, err := resolveProfileSelector(paths, selector)
	if err != nil {
		return ProfileReport{}, err
	}
	directory, err := os.MkdirTemp("", "arise-profile-inspect-")
	if err != nil {
		return ProfileReport{}, err
	}
	defer os.RemoveAll(directory)
	paths.ActiveProfile = filepath.Join(directory, "make.profile")
	if err := os.Symlink(path, paths.ActiveProfile); err != nil {
		return ProfileReport{}, err
	}
	return inspectProfilePaths(ctx, paths)
}

type ProfileDiffSide struct {
	Path        string   `json:"path"`
	Arch        string   `json:"architecture"`
	Layers      []string `json:"layers"`
	Unsupported []string `json:"unsupported_files"`
}

type ProfileDefaultChange struct {
	Name   string          `json:"name"`
	Kind   string          `json:"kind"`
	Before *ProfileDefault `json:"before"`
	After  *ProfileDefault `json:"after"`
}

type ProfilePolicyChange struct {
	Name          string                `json:"name"`
	Kind          string                `json:"kind"`
	Before        []string              `json:"before"`
	After         []string              `json:"after"`
	BeforeSources []ProfileContribution `json:"before_contributions"`
	AfterSources  []ProfileContribution `json:"after_contributions"`
}

type ProfileDiff struct {
	Schema   string                 `json:"schema"`
	Scope    string                 `json:"scope"`
	Before   ProfileDiffSide        `json:"before"`
	After    ProfileDiffSide        `json:"after"`
	Defaults []ProfileDefaultChange `json:"defaults"`
	Policies []ProfilePolicyChange  `json:"policies"`
}

func CompareProfiles(ctx context.Context, configRoot, before, after string) (ProfileDiff, error) {
	paths, err := profileInspectionPaths(configRoot)
	if err != nil {
		return ProfileDiff{}, err
	}
	left, err := inspectProfileSelector(ctx, paths, before)
	if err != nil {
		return ProfileDiff{}, fmt.Errorf("before profile: %w", err)
	}
	right, err := inspectProfileSelector(ctx, paths, after)
	if err != nil {
		return ProfileDiff{}, fmt.Errorf("after profile: %w", err)
	}
	return compareProfileReports(left, right), nil
}

func compareProfileReports(before, after ProfileReport) ProfileDiff {
	result := ProfileDiff{
		Schema: "arise.profile-diff.v1", Scope: "profile", Before: profileDiffSide(before), After: profileDiffSide(after),
		Defaults: []ProfileDefaultChange{}, Policies: []ProfilePolicyChange{},
	}
	left, right := make(map[string]ProfileDefault), make(map[string]ProfileDefault)
	names := make(map[string]bool)
	for _, value := range before.Defaults {
		left[value.Name], names[value.Name] = value, true
	}
	for _, value := range after.Defaults {
		right[value.Name], names[value.Name] = value, true
	}
	for name := range names {
		old, wasSet := left[name]
		value, isSet := right[name]
		kind := "value"
		oldValue, newValue := old.Value, value.Value
		// License wildcards and exclusions are order-sensitive even though
		// assignments accumulate. Preserve their complete expression.
		if incrementalVariables[name] && name != "ACCEPT_LICENSE" {
			kind = "tokens"
			oldTokens, newTokens := strings.Fields(oldValue), strings.Fields(newValue)
			sort.Strings(oldTokens)
			sort.Strings(newTokens)
			oldValue, newValue = strings.Join(oldTokens, " "), strings.Join(newTokens, " ")
		}
		if wasSet && isSet && oldValue == newValue {
			continue
		}
		change := ProfileDefaultChange{Name: name, Kind: kind}
		if wasSet {
			change.Before = &old
		}
		if isSet {
			change.After = &value
		}
		result.Defaults = append(result.Defaults, change)
	}
	sort.Slice(result.Defaults, func(i, j int) bool { return result.Defaults[i].Name < result.Defaults[j].Name })
	previous := make(map[string]ProfilePolicy)
	for _, policy := range before.Policies {
		previous[policy.Name] = policy
	}
	for _, policy := range after.Policies {
		old := previous[policy.Name]
		left, right := normalizedProfilePolicy(old), normalizedProfilePolicy(policy)
		if !reflect.DeepEqual(left, right) {
			result.Policies = append(result.Policies, ProfilePolicyChange{
				Name: policy.Name, Kind: policy.Kind, Before: left, After: right,
				BeforeSources: append([]ProfileContribution{}, old.Contributions...),
				AfterSources:  append([]ProfileContribution{}, policy.Contributions...),
			})
		}
	}
	return result
}

func normalizedProfilePolicy(policy ProfilePolicy) []string {
	values := []string{}
	seen := make(map[string]bool)
	for _, value := range policy.Values {
		if policy.Kind == "ordered_rules" {
			values = append(values, strings.Join(strings.Fields(value), " "))
		} else if !seen[value] {
			values = append(values, value)
			seen[value] = true
		}
	}
	if policy.Kind != "ordered_rules" {
		sort.Strings(values)
	}
	return values
}

func profileDiffSide(report ProfileReport) ProfileDiffSide {
	result := ProfileDiffSide{Path: report.ActivePath, Arch: profileArchitecture(report), Layers: []string{}, Unsupported: append([]string{}, report.Unsupported...)}
	for _, layer := range report.Layers {
		result.Layers = append(result.Layers, layer.Path)
	}
	return result
}
