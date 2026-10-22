package portage

import (
	"context"

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

func ListProfiles(ctx context.Context, configRoot string, all bool) (ProfileCatalog, error) {
	paths, err := profileInspectionPaths(configRoot)
	if err != nil {
		return ProfileCatalog{}, err
	}
	catalog, err := gentooling.ListProfiles(ctx, paths, gentooling.ProfileListOptions{AllArchitectures: all})
	if err != nil {
		return ProfileCatalog{}, err
	}
	result := ProfileCatalog{
		Schema: "arise.profile-catalog.v1", ActivePath: catalog.ActivePath, Arch: catalog.Architecture,
		All: catalog.AllArchitectures, Profiles: []ProfileCandidate{},
	}
	for _, candidate := range catalog.Profiles {
		result.Profiles = append(result.Profiles, ProfileCandidate{
			Selector: candidate.Selector, Path: candidate.Path, Arch: candidate.Architecture, Status: candidate.Status,
			Deprecated: candidate.Deprecated, Selected: candidate.Selected,
			Source: ProfileSource{Path: candidate.Source.Path, Line: candidate.Source.Line},
		})
	}
	return result, nil
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
	comparison, err := gentooling.CompareProfiles(ctx, paths, before, after)
	if err != nil {
		return ProfileDiff{}, err
	}
	result := ProfileDiff{
		Schema: "arise.profile-diff.v1", Scope: "profile",
		Before: profileDiffSideDocument(comparison.Before), After: profileDiffSideDocument(comparison.After),
		Defaults: []ProfileDefaultChange{}, Policies: []ProfilePolicyChange{},
	}
	for _, change := range comparison.Defaults {
		document := ProfileDefaultChange{Name: change.Name, Kind: change.Kind}
		if change.Before != nil {
			value := profileDefaultDocument(*change.Before)
			document.Before = &value
		}
		if change.After != nil {
			value := profileDefaultDocument(*change.After)
			document.After = &value
		}
		result.Defaults = append(result.Defaults, document)
	}
	for _, change := range comparison.Policies {
		result.Policies = append(result.Policies, ProfilePolicyChange{
			Name: change.Name, Kind: change.Kind, Before: append([]string{}, change.Before...), After: append([]string{}, change.After...),
			BeforeSources: profileContributionDocuments(change.BeforeSources), AfterSources: profileContributionDocuments(change.AfterSources),
		})
	}
	return result, nil
}

func profileDiffSideDocument(side gentooling.ProfileComparisonSide) ProfileDiffSide {
	return ProfileDiffSide{
		Path: side.Path, Arch: side.Architecture, Layers: append([]string{}, side.Layers...),
		Unsupported: append([]string{}, side.Unsupported...),
	}
}
