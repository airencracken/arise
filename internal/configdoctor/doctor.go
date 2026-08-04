// Package configdoctor performs read-only, deterministic Portage policy linting.
package configdoctor

import (
	"fmt"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/resolve"
)

const SchemaVersion = 1

type Finding struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Family   string `json:"family,omitempty"`
	Atom     string `json:"atom,omitempty"`
	Flag     string `json:"flag,omitempty"`
	Rule     int    `json:"rule"`
	Related  int    `json:"related_rule,omitempty"`
	Message  string `json:"message"`
}

type Report struct {
	Schema   int       `json:"schema"`
	Findings []Finding `json:"findings"`
}

func PackageUse(config *portage.Config, graph *resolve.DepGraph) Report {
	report := Report{Schema: SchemaVersion, Findings: []Finding{}}
	if config == nil {
		return report
	}
	packages := map[string]bool{}
	if graph != nil {
		for cp := range graph.Packages {
			packages[cp] = true
		}
	}
	type occurrence struct {
		rule    int
		enabled bool
	}
	last := make(map[string]occurrence)
	seenRules := make(map[string]int)
	for index, rule := range config.PackageUseRules {
		ruleNumber := index + 1
		parsed, err := atom.Parse(rule.Atom)
		if err != nil {
			report.Findings = append(report.Findings, Finding{Kind: "invalid-atom", Severity: "error", Family: "package.use", Atom: rule.Atom, Rule: ruleNumber, Message: err.Error()})
			continue
		}
		cp := parsed.CP()
		if graph != nil && !packages[cp] {
			report.Findings = append(report.Findings, Finding{Kind: "stale-atom", Severity: "warning", Atom: rule.Atom, Rule: ruleNumber, Message: "atom matches no installed or visible package name"})
		} else if graph != nil && !matchesPackageState(graph.Packages[cp], parsed) {
			report.Findings = append(report.Findings, Finding{Kind: "stale-version-atom", Severity: "warning", Atom: rule.Atom, Rule: ruleNumber, Message: "package exists, but the atom matches no installed or visible version"})
		}
		normalizedFlags := append([]string(nil), rule.Flags...)
		sort.Strings(normalizedFlags)
		ruleKey := rule.Atom + "\x00" + strings.Join(normalizedFlags, "\x00")
		if previous, exists := seenRules[ruleKey]; exists {
			report.Findings = append(report.Findings, Finding{Kind: "duplicate-rule", Severity: "warning", Atom: rule.Atom, Rule: ruleNumber, Related: previous, Message: "rule duplicates an earlier package.use entry"})
		} else {
			seenRules[ruleKey] = ruleNumber
		}
		within := make(map[string]bool)
		for _, raw := range rule.Flags {
			flag, enabled := normalizeFlag(raw)
			if flag == "" {
				continue
			}
			priorWithin, seenWithin := within[flag]
			if !seenWithin && graph != nil && packages[cp] && flag != "*" && !flagKnownForMatchingVersions(graph.Packages[cp], parsed, flag) {
				report.Findings = append(report.Findings, Finding{Kind: "unknown-use-flag", Severity: "warning", Atom: rule.Atom, Flag: flag, Rule: ruleNumber, Message: "flag is absent from every installed or visible version matched by this atom"})
			}
			if seenWithin && priorWithin != enabled {
				report.Findings = append(report.Findings, Finding{Kind: "contradictory-rule", Severity: "error", Atom: rule.Atom, Flag: flag, Rule: ruleNumber, Message: "rule enables and disables the same USE flag"})
			} else if seenWithin {
				report.Findings = append(report.Findings, Finding{Kind: "duplicate-flag", Severity: "notice", Atom: rule.Atom, Flag: flag, Rule: ruleNumber, Message: "rule repeats the same USE flag setting"})
			}
			within[flag] = enabled
		}
		for flag, enabled := range within {
			key := rule.Atom + "\x00" + flag
			if prior, exists := last[key]; exists && prior.enabled != enabled {
				report.Findings = append(report.Findings, Finding{Kind: "shadowed-setting", Severity: "notice", Atom: rule.Atom, Flag: flag, Rule: prior.rule, Related: ruleNumber, Message: fmt.Sprintf("later rule %d reverses this exact-atom setting", ruleNumber)})
			}
			last[key] = occurrence{rule: ruleNumber, enabled: enabled}
		}
	}
	for index := range report.Findings {
		if report.Findings[index].Family == "" {
			report.Findings[index].Family = "package.use"
		}
	}
	sortFindings(report.Findings)
	return report
}

// PackagePolicy audits non-USE package policy families whose safe common
// semantics are atom validity, applicability, and exact duplicate detection.
func PackagePolicy(config *portage.Config, graph *resolve.DepGraph) Report {
	report := Report{Schema: SchemaVersion, Findings: []Finding{}}
	if config == nil {
		return report
	}
	type rule struct {
		family, atom, value string
		number              int
	}
	var rules []rule
	appendFlagRules := func(family string, input []portage.PackageUseRule) {
		for index, item := range input {
			values := append([]string(nil), item.Flags...)
			sort.Strings(values)
			rules = append(rules, rule{family: family, atom: item.Atom, value: strings.Join(values, "\x00"), number: index + 1})
		}
	}
	appendFlagRules("package.accept_keywords", config.PackageAcceptKeywordRules)
	appendFlagRules("package.license", config.PackageLicenseRules)
	appendFlagRules("package.env", config.PackageEnvRules)
	for index, item := range config.PackageMaskRules {
		rules = append(rules, rule{family: "package.mask", atom: item.Atom, number: index + 1})
	}
	for index, item := range config.PackageUnmask {
		rules = append(rules, rule{family: "package.unmask", atom: item, number: index + 1})
	}
	seen := map[string]int{}
	for _, item := range rules {
		parsed, err := atom.Parse(item.atom)
		if err != nil {
			report.Findings = append(report.Findings, Finding{Kind: "invalid-atom", Severity: "error", Family: item.family, Atom: item.atom, Rule: item.number, Message: err.Error()})
			continue
		}
		if graph != nil {
			node := graph.Packages[parsed.CP()]
			if node == nil {
				report.Findings = append(report.Findings, Finding{Kind: "stale-atom", Severity: "warning", Family: item.family, Atom: item.atom, Rule: item.number, Message: "atom matches no installed or visible package name"})
			} else if !matchesPackageState(node, parsed) {
				report.Findings = append(report.Findings, Finding{Kind: "stale-version-atom", Severity: "warning", Family: item.family, Atom: item.atom, Rule: item.number, Message: "package exists, but the atom matches no installed or visible version"})
			}
		}
		key := item.family + "\x00" + item.atom + "\x00" + item.value
		if previous, exists := seen[key]; exists {
			report.Findings = append(report.Findings, Finding{Kind: "duplicate-rule", Severity: "warning", Family: item.family, Atom: item.atom, Rule: item.number, Related: previous, Message: "rule duplicates an earlier entry in the same policy family"})
		} else {
			seen[key] = item.number
		}
	}
	sortFindings(report.Findings)
	return report
}

func WorldTargets(entries []string, graph *resolve.DepGraph) Report {
	report := Report{Schema: SchemaVersion, Findings: []Finding{}}
	seen := map[string]int{}
	for index, raw := range entries {
		ruleNumber := index + 1
		raw = strings.TrimSpace(raw)
		if raw == "" || strings.HasPrefix(raw, "@") {
			continue
		}
		parsed, err := atom.ParsePackageAtom(raw)
		if err != nil {
			report.Findings = append(report.Findings, Finding{Kind: "invalid-atom", Severity: "error", Family: "world", Atom: raw, Rule: ruleNumber, Message: err.Error()})
			continue
		}
		if previous, exists := seen[parsed.String()]; exists {
			report.Findings = append(report.Findings, Finding{Kind: "duplicate-target", Severity: "notice", Family: "world", Atom: raw, Rule: ruleNumber, Related: previous, Message: "selected target duplicates an earlier world entry"})
		} else {
			seen[parsed.String()] = ruleNumber
		}
		if graph == nil {
			continue
		}
		node := graph.Packages[parsed.CP()]
		if node == nil {
			report.Findings = append(report.Findings, Finding{Kind: "obsolete-target", Severity: "warning", Family: "world", Atom: raw, Rule: ruleNumber, Message: "selected target has no installed or visible package name"})
		} else if !matchesPackageState(node, parsed) {
			report.Findings = append(report.Findings, Finding{Kind: "obsolete-version-target", Severity: "warning", Family: "world", Atom: raw, Rule: ruleNumber, Message: "selected target matches no installed or visible version"})
		}
	}
	sortFindings(report.Findings)
	return report
}

func All(config *portage.Config, graph *resolve.DepGraph, worldEntries []string) Report {
	report := Report{Schema: SchemaVersion, Findings: []Finding{}}
	report.Findings = append(report.Findings, PackageUse(config, graph).Findings...)
	report.Findings = append(report.Findings, PackagePolicy(config, graph).Findings...)
	report.Findings = append(report.Findings, WorldTargets(worldEntries, graph).Findings...)
	sortFindings(report.Findings)
	return report
}

func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Family != findings[j].Family {
			return findings[i].Family < findings[j].Family
		}
		if findings[i].Rule != findings[j].Rule {
			return findings[i].Rule < findings[j].Rule
		}
		if findings[i].Kind != findings[j].Kind {
			return findings[i].Kind < findings[j].Kind
		}
		return findings[i].Flag < findings[j].Flag
	})
}

func matchesPackageState(node *resolve.PkgNode, constraint *atom.Atom) bool {
	if node == nil {
		return false
	}
	for _, version := range node.Versions {
		if version != nil && (version.Available || version.Installed) && resolve.VersionMatchesConstraint(node.Atom, constraint, version) {
			return true
		}
	}
	return false
}

func flagKnownForMatchingVersions(node *resolve.PkgNode, constraint *atom.Atom, flag string) bool {
	if node == nil {
		return false
	}
	matched := false
	for _, version := range node.Versions {
		if version == nil || (!version.Available && !version.Installed) || !resolve.VersionMatchesConstraint(node.Atom, constraint, version) {
			continue
		}
		matched = true
		if _, exists := version.UseFlags[flag]; exists {
			return true
		}
		for _, token := range strings.Fields(version.IUse) {
			if strings.TrimLeft(token, "+-") == flag {
				return true
			}
		}
	}
	// A stale version constraint already has a more direct finding; avoid
	// claiming its flags are unknown when no version was available to inspect.
	return !matched
}

func normalizeFlag(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if strings.HasPrefix(raw, "-") {
		return strings.TrimPrefix(raw, "-"), false
	}
	return raw, true
}
