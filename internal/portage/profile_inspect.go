package portage

import (
	"context"
	"path/filepath"

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

// InspectProfile adapts explicit Arise repository discovery to the shared
// evaluator. The versioned CLI document remains an Arise presentation contract.
func InspectProfile(ctx context.Context, configRoot string) (ProfileReport, error) {
	paths, err := profileInspectionPaths(configRoot)
	if err != nil {
		return ProfileReport{}, err
	}
	inspection, err := gentooling.InspectProfile(ctx, paths, "active")
	if err != nil {
		return ProfileReport{}, err
	}
	report := ProfileReport{
		Schema: "arise.profile.v1", Scope: "profile", ActivePath: inspection.ActivePath,
		Layers: []ProfileReportLayer{}, Defaults: []ProfileDefault{}, Policies: []ProfilePolicy{},
		Unsupported: append([]string{}, inspection.Unsupported...),
	}
	for _, layer := range inspection.Layers {
		report.Layers = append(report.Layers, ProfileReportLayer{Path: layer.Path, Parents: append([]string{}, layer.Parents...)})
	}
	for _, value := range inspection.Defaults {
		report.Defaults = append(report.Defaults, profileDefaultDocument(value))
	}
	for _, policy := range inspection.Policies {
		report.Policies = append(report.Policies, ProfilePolicy{
			Name: policy.Name, Kind: policy.Kind, Values: append([]string{}, policy.Values...),
			Contributions: profileContributionDocuments(policy.Contributions),
		})
	}
	return report, nil
}

func profileInspectionPaths(configRoot string) (gentooling.SystemPaths, error) {
	repositories, err := EffectiveRepositoryPolicyOrder(configRoot)
	if err != nil {
		return gentooling.SystemPaths{}, err
	}
	paths := gentooling.SystemPaths{ActiveProfile: filepath.Join(configRoot, "make.profile")}
	for _, repository := range repositories {
		if repository.Name != "" && repository.Location != "" {
			paths.Repositories = append(paths.Repositories, gentooling.RepositoryPath{Name: repository.Name, Path: repository.Location})
		}
	}
	return paths, nil
}

func profileDefaultDocument(value gentooling.ProfileDefault) ProfileDefault {
	return ProfileDefault{Name: value.Name, Value: value.Value, Contributions: profileContributionDocuments(value.Contributions)}
}

func profileContributionDocuments(contributions []gentooling.ProfileContribution) []ProfileContribution {
	result := make([]ProfileContribution, 0, len(contributions))
	for _, value := range contributions {
		result = append(result, ProfileContribution{Value: value.Value, Source: ProfileSource{Path: value.Source.Path, Line: value.Source.Line}})
	}
	return result
}
