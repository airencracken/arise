package portage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/airencracken/gentooling"
)

// ProfileReport describes profile contributions only. Local configuration,
// make.globals, the command environment and package IUSE are not inputs.
type ProfileReport struct {
	Schema      string               `json:"schema"`
	Scope       string               `json:"scope"`
	ActivePath  string               `json:"active_path"`
	Layers      []ProfileReportLayer `json:"layers"`
	Defaults    []ProfileDefault     `json:"defaults"`
	Policies    []ProfilePolicy      `json:"policies"`
	Unsupported []string             `json:"unsupported_files"`
}

type ProfileReportLayer struct {
	Path    string   `json:"path"`
	Parents []string `json:"parent_references"`
}

type ProfileSource struct {
	Path string `json:"path"`
	Line int    `json:"line"`
}

type ProfileContribution struct {
	Value  string        `json:"value"`
	Source ProfileSource `json:"source"`
}

type ProfileDefault struct {
	Name          string                `json:"name"`
	Value         string                `json:"value"`
	Contributions []ProfileContribution `json:"contributions"`
}

type ProfilePolicy struct {
	Name          string                `json:"name"`
	Kind          string                `json:"kind"`
	Values        []string              `json:"values"`
	Contributions []ProfileContribution `json:"contributions"`
}

// InspectProfile reuses the production graph and assignment merge functions.
// Package USE rules remain ordered because their result needs package context.
func InspectProfile(ctx context.Context, configRoot string) (ProfileReport, error) {
	paths, err := profileInspectionPaths(configRoot)
	if err != nil {
		return ProfileReport{}, err
	}
	return inspectProfilePaths(ctx, paths)
}

func profileInspectionPaths(configRoot string) (gentooling.SystemPaths, error) {
	repositories, err := EffectiveRepositoryPolicyOrder(configRoot)
	if err != nil {
		return gentooling.SystemPaths{}, err
	}
	paths := gentooling.SystemPaths{ActiveProfile: filepath.Join(configRoot, "make.profile")}
	for _, repository := range repositories {
		if repository.Name != "" && repository.Location != "" {
			paths.Repositories = append(paths.Repositories, gentooling.RepositoryPath{
				Name: repository.Name, Path: repository.Location,
			})
		}
	}
	return paths, nil
}

func inspectProfilePaths(ctx context.Context, paths gentooling.SystemPaths) (ProfileReport, error) {
	profile, err := gentooling.ReadProfile(ctx, paths)
	if err != nil {
		return ProfileReport{}, err
	}
	report := ProfileReport{
		Schema: "arise.profile.v1", Scope: "profile", ActivePath: profile.ActivePath,
		Layers: []ProfileReportLayer{}, Defaults: []ProfileDefault{},
		Policies: []ProfilePolicy{}, Unsupported: []string{},
	}
	for _, layer := range profile.Layers {
		report.Layers = append(report.Layers, ProfileReportLayer{
			Path: layer.Path, Parents: append([]string{}, layer.Parents...),
		})
	}
	if err := report.loadDefaults(ctx); err != nil {
		return ProfileReport{}, err
	}
	if err := report.loadPolicies(ctx, profile); err != nil {
		return ProfileReport{}, err
	}
	if err := report.findUnsupported(ctx); err != nil {
		return ProfileReport{}, err
	}
	return report, nil
}

func (report *ProfileReport) loadDefaults(ctx context.Context) error {
	values := make(map[string]string)
	history := make(map[string][]ProfileContribution)
	for _, layer := range report.Layers {
		if err := ctx.Err(); err != nil {
			return err
		}
		assignments, err := parseMakeConfAssignments(filepath.Join(layer.Path, "make.defaults"))
		if err != nil {
			return err
		}
		for _, assignment := range assignments {
			history[assignment.key] = append(history[assignment.key], ProfileContribution{
				Value:  assignment.value,
				Source: ProfileSource{Path: assignment.source.Path, Line: assignment.source.Line},
			})
		}
		mergeConfigAssignments(values, assignments)
	}
	for variable := range removalOnlyIncrementalVariables {
		if value, exists := values[variable]; exists {
			values[variable] = dropNegativeTokens(value)
		}
	}
	ResolveMakeConfRefs(values)
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		report.Defaults = append(report.Defaults, ProfileDefault{
			Name: name, Value: values[name], Contributions: history[name],
		})
	}
	return nil
}

func (report *ProfileReport) loadPolicies(ctx context.Context, profile gentooling.Profile) error {
	for _, policy := range []struct {
		name   string
		values []string
	}{
		{"packages", profile.System}, {"package.provided", profile.PackageProvided},
		{"use.force", profile.UseForce}, {"use.mask", profile.UseMask},
		{"use.stable.force", profile.UseStableForce}, {"use.stable.mask", profile.UseStableMask},
		{"package.mask", nil}, {"package.unmask", nil},
		{"package.use", nil}, {"package.use.force", nil}, {"package.use.mask", nil},
		{"package.use.stable.force", nil}, {"package.use.stable.mask", nil},
	} {
		entry := ProfilePolicy{
			Name: policy.name, Kind: "effective", Values: append([]string{}, policy.values...),
			Contributions: []ProfileContribution{},
		}
		if strings.HasPrefix(policy.name, "package.use") {
			entry.Kind = "ordered_rules"
		}
		for _, layer := range report.Layers {
			changes, err := readProfileContributions(ctx, filepath.Join(layer.Path, policy.name))
			if err != nil {
				return err
			}
			entry.Contributions = append(entry.Contributions, changes...)
			if err := entry.mergePolicy(layer.Path, changes); err != nil {
				return err
			}
		}
		report.Policies = append(report.Policies, entry)
	}
	return nil
}

func (policy *ProfilePolicy) mergePolicy(directory string, changes []ProfileContribution) error {
	if policy.Kind == "ordered_rules" {
		for _, change := range changes {
			policy.Values = append(policy.Values, change.Value)
		}
	}
	if policy.Name == "package.mask" || policy.Name == "package.unmask" {
		atoms, err := parseAtomList(filepath.Join(directory, policy.Name))
		if err != nil {
			return err
		}
		policy.Values = append([]string{}, applyAtomChanges(policy.Values, atoms)...)
	}
	return nil
}

// Record the actual physical line numbers, including in policy directories.
// Reject nonregular files so inspection cannot block on a FIFO or read a device.
func readProfileContributions(ctx context.Context, path string) ([]ProfileContribution, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		var result []ProfileContribution
		for _, entry := range entries {
			if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			lines, err := readProfileContributions(ctx, filepath.Join(path, entry.Name()))
			if err != nil {
				return nil, err
			}
			result = append(result, lines...)
		}
		return result, nil
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("profile policy %q is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result []ProfileContribution
	for index, raw := range strings.Split(string(data), "\n") {
		text := strings.TrimSpace(raw)
		if text != "" && !strings.HasPrefix(text, "#") {
			result = append(result, ProfileContribution{
				Value: text, Source: ProfileSource{Path: path, Line: index + 1},
			})
		}
	}
	return result, nil
}

func (report *ProfileReport) findUnsupported(ctx context.Context) error {
	supported := map[string]bool{"parent": true, "make.defaults": true, "eapi": true, "deprecated": true}
	for _, policy := range report.Policies {
		supported[policy.Name] = true
	}
	for _, layer := range report.Layers {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := os.ReadDir(layer.Path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") && !supported[entry.Name()] {
				report.Unsupported = append(report.Unsupported, filepath.Join(layer.Path, entry.Name()))
			}
		}
	}
	return nil
}
