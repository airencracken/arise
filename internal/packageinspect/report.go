package packageinspect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/depstring"
	"github.com/airencracken/gentooling"
)

const Schema = "arise.package-inspect.v1"

type Report struct {
	Schema      string                                    `json:"schema"`
	Query       string                                    `json:"query"`
	Consistency string                                    `json:"consistency"`
	Installed   []Installed                               `json:"installed"`
	Candidates  []Candidate                               `json:"candidates"`
	RequiredBy  []string                                  `json:"required_by"`
	Modules     []gentooling.InstalledKernelModulePackage `json:"kernel_modules"`
	Diagnostics []Diagnostic                              `json:"diagnostics"`
}

// MarshalJSON keeps Arise's public report contract independent of the Go
// field spelling used by Gentooling's library models.
func (report Report) MarshalJSON() ([]byte, error) {
	type reportAlias Report
	raw, err := json.Marshal(reportAlias(report))
	if err != nil {
		return nil, err
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(snakeCaseJSON(document)); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(output.Bytes(), []byte("\n")), nil
}

type Installed struct {
	Package         gentooling.PackageID          `json:"package"`
	EAPI            string                        `json:"eapi"`
	EnabledUse      []string                      `json:"enabled_use"`
	DeclaredUse     []gentooling.UseDeclaration   `json:"declared_use"`
	Dependencies    gentooling.DependencyMetadata `json:"dependencies"`
	DependencyAtoms []string                      `json:"dependency_atoms"`
}

type Candidate struct {
	Package            gentooling.PackageID            `json:"package"`
	EAPI               string                          `json:"eapi"`
	Keywords           []string                        `json:"keywords"`
	RequiredUse        string                          `json:"required_use,omitempty"`
	Dependencies       gentooling.DependencyMetadata   `json:"dependencies"`
	DependencyAtoms    []string                        `json:"dependency_atoms"`
	Visibility         gentooling.VisibilityResult     `json:"visibility"`
	Use                gentooling.UseEvaluation        `json:"use"`
	KernelRequirements gentooling.KernelRequirementSet `json:"kernel_requirements"`
}

type Diagnostic struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Package string `json:"package,omitempty"`
	Message string `json:"message"`
}

type Options struct {
	Query        string
	Repositories []gentooling.Repository
	TargetKernel string
}

func Build(ctx context.Context, snapshot gentooling.SystemSnapshot, options Options) (Report, error) {
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	if strings.TrimSpace(options.Query) == "" {
		return Report{}, fmt.Errorf("inspect: package query is empty")
	}
	query, err := gentooling.ParseAtom(options.Query)
	if err != nil {
		return Report{}, fmt.Errorf("inspect: parse package query: %w", err)
	}
	if len(options.Repositories) == 0 {
		options.Repositories = snapshot.Repositories
	}
	report := Report{
		Schema: Schema, Query: options.Query,
		Consistency: consistencyName(snapshot.Consistency),
		Installed:   []Installed{}, Candidates: []Candidate{}, RequiredBy: []string{},
		Modules: []gentooling.InstalledKernelModulePackage{}, Diagnostics: []Diagnostic{},
	}
	report.Diagnostics = append(report.Diagnostics, diagnostics(snapshot.Installed.Issues)...)
	report.Diagnostics = append(report.Diagnostics, diagnostics(snapshot.Candidates.Issues)...)

	for _, installed := range snapshot.Installed.Packages {
		matched, matchErr := query.Matches(installed.ID, installedUseState(installed))
		if matchErr != nil {
			return Report{}, fmt.Errorf("inspect: match installed package %s: %w", installed.ID.CPV(), matchErr)
		}
		if matched {
			report.Installed = append(report.Installed, Installed{
				Package: installed.ID, EAPI: installed.EAPI,
				EnabledUse: clone(installed.EnabledUse), DeclaredUse: append([]gentooling.UseDeclaration(nil), installed.DeclaredUse...),
				Dependencies: installed.Dependencies, DependencyAtoms: dependencyAtoms(installed.Dependencies, &report.Diagnostics, installed.ID.CPV()),
			})
		}
	}
	for _, candidate := range snapshot.Candidates.Candidates {
		matched, matchErr := query.Matches(candidate.ID, gentooling.UseState{})
		if matchErr != nil {
			return Report{}, fmt.Errorf("inspect: match repository candidate %s: %w", candidate.ID.CPV(), matchErr)
		}
		if !matched {
			continue
		}
		evaluation, evaluationErr := snapshot.EvaluateCandidate(ctx, candidate.ID)
		if evaluationErr != nil {
			return Report{}, fmt.Errorf("inspect: evaluate %s: %w", candidate.ID.CPV(), evaluationErr)
		}
		requirements, requirementErr := gentooling.ReadKernelRequirements(ctx, candidate, options.Repositories,
			gentooling.KernelRequirementOptions{Integrity: gentooling.AllowPartial})
		if requirementErr != nil {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Code: "kernel_requirements_unavailable", Package: candidate.ID.CPV(), Message: requirementErr.Error(),
			})
			requirements = gentooling.KernelRequirementSet{Package: candidate.ID}
		}
		report.Candidates = append(report.Candidates, Candidate{
			Package: candidate.ID, EAPI: candidate.EAPI, Keywords: clone(candidate.Keywords),
			RequiredUse: candidate.RequiredUse, Dependencies: candidate.Dependencies,
			DependencyAtoms: dependencyAtoms(candidate.Dependencies, &report.Diagnostics, candidate.ID.CPV()),
			Visibility:      evaluation.Visibility, Use: evaluation.Use, KernelRequirements: requirements,
		})
	}
	report.RequiredBy = reverseDependencies(snapshot.Installed.Packages, report.Installed, report.Candidates, &report.Diagnostics)
	modules := gentooling.ClassifyInstalledKernelModules(snapshot.Installed, options.TargetKernel)
	report.Diagnostics = append(report.Diagnostics, diagnostics(modules.Issues)...)
	for _, module := range modules.Packages {
		for _, installed := range report.Installed {
			if sameID(module.Package, installed.Package) {
				report.Modules = append(report.Modules, module)
				break
			}
		}
	}
	sortReport(&report)
	if len(report.Installed) == 0 && len(report.Candidates) == 0 {
		return report, fmt.Errorf("%w: %s", gentooling.ErrCandidateNotFound, options.Query)
	}
	return report, nil
}

func installedUseState(pkg gentooling.InstalledPackage) gentooling.UseState {
	state := gentooling.UseState{Declared: make(map[string]bool), Enabled: make(map[string]bool)}
	for _, declaration := range pkg.DeclaredUse {
		state.Declared[declaration.Name] = true
	}
	for _, flag := range pkg.EnabledUse {
		state.Enabled[flag] = true
	}
	return state
}

func dependencyAtoms(metadata gentooling.DependencyMetadata, issues *[]Diagnostic, owner string) []string {
	var result []string
	for _, field := range []struct {
		name, value string
	}{
		{"DEPEND", metadata.Depend}, {"RDEPEND", metadata.RDepend}, {"BDEPEND", metadata.BDepend},
		{"IDEPEND", metadata.IDepend}, {"PDEPEND", metadata.PDepend},
	} {
		if strings.TrimSpace(field.value) == "" {
			continue
		}
		node, err := depstring.Parse(field.value)
		if err != nil {
			*issues = append(*issues, Diagnostic{Code: "dependency_parse", Package: owner, Message: field.name + ": " + err.Error()})
			continue
		}
		result = append(result, node.Atoms()...)
	}
	return unique(result)
}

func reverseDependencies(packages []gentooling.InstalledPackage, installed []Installed, candidates []Candidate, issues *[]Diagnostic) []string {
	targets := make([]gentooling.PackageID, 0, len(installed)+len(candidates))
	for _, item := range installed {
		targets = append(targets, item.Package)
	}
	for _, item := range candidates {
		targets = append(targets, item.Package)
	}
	var result []string
	skippedAtoms := 0
	for _, pkg := range packages {
		atoms := dependencyAtoms(pkg.Dependencies, issues, pkg.ID.CPV())
		found := false
		for _, raw := range atoms {
			parsed, err := gentooling.ParseAtom(strings.TrimLeft(raw, "!"))
			if err != nil {
				skippedAtoms++
				continue
			}
			for _, target := range targets {
				matched, matchErr := parsed.Matches(target, gentooling.UseState{})
				if matchErr == nil && matched {
					result = append(result, pkg.ID.CPV())
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}
	if skippedAtoms != 0 {
		*issues = append(*issues, Diagnostic{
			Code:    "reverse_dependency_incomplete",
			Message: fmt.Sprintf("%d installed dependency atoms contained unresolved metadata expansions or unsupported legacy syntax", skippedAtoms),
		})
	}
	return unique(result)
}

func diagnostics(issues []gentooling.Issue) []Diagnostic {
	result := make([]Diagnostic, 0, len(issues))
	for _, issue := range issues {
		item := Diagnostic{Code: string(issue.Code), Path: issue.Path, Message: issue.Message}
		if issue.Package != nil {
			item.Package = issue.Package.CPV()
		}
		result = append(result, item)
	}
	return result
}

func consistencyName(value gentooling.SnapshotConsistency) string {
	if value == gentooling.StabilizedLockless {
		return "stabilized-lockless"
	}
	return "locked-and-stabilized"
}

func sortReport(report *Report) {
	sort.Slice(report.Installed, func(i, j int) bool { return report.Installed[i].Package.CPV() < report.Installed[j].Package.CPV() })
	sort.Slice(report.Candidates, func(i, j int) bool { return report.Candidates[i].Package.CPV() < report.Candidates[j].Package.CPV() })
	sort.Strings(report.RequiredBy)
	sort.Slice(report.Diagnostics, func(i, j int) bool {
		left := report.Diagnostics[i].Code + "\x00" + report.Diagnostics[i].Path + "\x00" + report.Diagnostics[i].Package
		right := report.Diagnostics[j].Code + "\x00" + report.Diagnostics[j].Path + "\x00" + report.Diagnostics[j].Package
		return left < right
	})
}

func sameID(left, right gentooling.PackageID) bool {
	return left.Category == right.Category && left.Name == right.Name && left.Version == right.Version &&
		(left.Repository == "" || right.Repository == "" || left.Repository == right.Repository)
}

func clone(values []string) []string {
	return append([]string(nil), values...)
}

func unique(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func IsNotFound(err error) bool {
	return errors.Is(err, gentooling.ErrCandidateNotFound)
}

func snakeCaseJSON(value any) any {
	switch typed := value.(type) {
	case []any:
		for index := range typed {
			typed[index] = snakeCaseJSON(typed[index])
		}
		return typed
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[snakeCase(key)] = snakeCaseJSON(item)
		}
		return result
	default:
		return value
	}
}

func snakeCase(value string) string {
	var result strings.Builder
	for index, character := range value {
		if character >= 'A' && character <= 'Z' {
			nextLower := index+1 < len(value) && value[index+1] >= 'a' && value[index+1] <= 'z'
			previousLower := index > 0 && value[index-1] >= 'a' && value[index-1] <= 'z'
			if result.Len() != 0 && (previousLower || nextLower) {
				result.WriteByte('_')
			}
			result.WriteByte(byte(character - 'A' + 'a'))
			continue
		}
		result.WriteRune(character)
	}
	return result.String()
}
