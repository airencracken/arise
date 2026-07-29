package planvalidate

import (
	"regexp"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/depstring"
)

var blockerFeaturePattern = regexp.MustCompile(`(?:^|[\s(])!!?[A-Za-z0-9+_.-]+/[A-Za-z0-9+_.-]+`)

// SemanticFeatures reports which correctness semantics a frozen fixture and
// plan exercise. Coverage is intentionally feature-based rather than a count
// of package names, versions, or actions.
func SemanticFeatures(fixture Fixture, plan Plan) []string {
	features := map[string]bool{"operation:" + fixture.Request.Operation: true}
	if fixture.Request.PartialMode != "" {
		features["partial-mode:"+fixture.Request.PartialMode] = true
	}
	if fixture.DomainsAliasToRoot {
		features["dependency-domains:root-aliased"] = true
	} else if len(fixture.Domains) != 0 {
		features["dependency-domains:independent"] = true
	}
	if len(fixture.Policy.AcceptedKeywords) != 0 {
		features["policy:keywords"] = true
	}
	if len(fixture.Policy.AcceptedLicenses) != 0 {
		features["policy:licenses"] = true
	}
	for _, action := range plan.Actions {
		features["action:"+action.Kind] = true
		if action.Replaces != "" {
			features["action:replacement"] = true
		}
		if len(action.Prerequisites) != 0 {
			features["transaction:prerequisites"] = true
		}
	}
	for _, pkg := range append(append([]Package(nil), fixture.Installed...), fixture.Available...) {
		if strings.TrimSpace(pkg.RequiredUse) != "" {
			features["constraint:required-use"] = true
		}
		for class, expression := range pkg.Dependencies {
			expression = strings.TrimSpace(expression)
			if expression == "" {
				continue
			}
			features["dependency-class:"+strings.ToLower(class)] = true
			if tree, err := depstring.Parse(expression); err == nil {
				for _, raw := range tree.Atoms() {
					constraint, parseErr := atom.ParsePackageAtom(strings.TrimLeft(raw, "!"))
					if parseErr == nil && constraint.SlotOp == atom.SlotOpEq {
						features["dependency:built-slot-operator"] = true
					}
					if strings.HasPrefix(raw, "!") {
						features["dependency:blocker"] = true
					}
				}
			}
			if strings.Contains(expression, "|| (") {
				features["dependency:any-of"] = true
			}
			if strings.Contains(expression, "? (") {
				features["dependency:use-conditional"] = true
			}
			if blockerFeaturePattern.MatchString(expression) {
				features["dependency:blocker"] = true
			}
		}
	}
	result := make([]string, 0, len(features))
	for feature := range features {
		result = append(result, feature)
	}
	sort.Strings(result)
	return result
}
