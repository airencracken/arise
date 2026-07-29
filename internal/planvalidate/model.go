// Package planvalidate independently applies package plans to frozen installed
// state and validates the result without invoking resolver policy.
package planvalidate

import "sort"

const SchemaVersion = 1

const MaxViolationRecords = 256

type MetadataAuthority string

const (
	AuthorityVDB       MetadataAuthority = "vdb"
	AuthorityMD5Cache  MetadataAuthority = "md5-cache"
	AuthorityEvaluated MetadataAuthority = "evaluated"
)

type Fixture struct {
	Schema    int       `json:"schema"`
	Request   Request   `json:"request"`
	Installed []Package `json:"installed"`
	Available []Package `json:"available"`
}

type Request struct {
	Operation string   `json:"operation"`
	Targets   []string `json:"targets"`
}

type Package struct {
	CPV          string            `json:"cpv"`
	Slot         string            `json:"slot"`
	Subslot      string            `json:"subslot,omitempty"`
	Repository   string            `json:"repository"`
	Authority    MetadataAuthority `json:"metadata_authority"`
	Use          map[string]bool   `json:"use,omitempty"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
}

type Plan struct {
	Schema  int      `json:"schema"`
	Actions []Action `json:"actions"`
}

type Action struct {
	Kind     string  `json:"kind"`
	Package  Package `json:"package"`
	Replaces string  `json:"replaces,omitempty"`
}

type State struct {
	Packages []Package `json:"packages"`
}

type Violation struct {
	Kind        string `json:"kind"`
	Package     string `json:"package,omitempty"`
	Requirement string `json:"requirement,omitempty"`
	RequiredBy  string `json:"required_by,omitempty"`
	Message     string `json:"message"`
}

type ApplicationResult struct {
	State             State       `json:"state"`
	Violations        []Violation `json:"violations"`
	Truncated         bool        `json:"truncated"`
	OmittedViolations int         `json:"omitted_violations"`
}

type ValidationResult struct {
	Valid             bool        `json:"valid"`
	Violations        []Violation `json:"violations"`
	Truncated         bool        `json:"truncated"`
	OmittedViolations int         `json:"omitted_violations"`
}

func packageIdentity(pkg Package) string {
	return pkg.CPV + ":" + pkg.Slot + "::" + pkg.Repository
}

func clonePackage(pkg Package) Package {
	cloned := pkg
	if pkg.Use != nil {
		cloned.Use = make(map[string]bool, len(pkg.Use))
		for name, enabled := range pkg.Use {
			cloned.Use[name] = enabled
		}
	}
	if pkg.Dependencies != nil {
		cloned.Dependencies = make(map[string]string, len(pkg.Dependencies))
		for class, expression := range pkg.Dependencies {
			cloned.Dependencies[class] = expression
		}
	}
	return cloned
}

func canonicalState(packages []Package) State {
	result := make([]Package, len(packages))
	for i := range packages {
		result[i] = clonePackage(packages[i])
	}
	sort.Slice(result, func(i, j int) bool {
		return packageIdentity(result[i]) < packageIdentity(result[j])
	})
	if result == nil {
		result = []Package{}
	}
	return State{Packages: result}
}
