package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/airencracken/arise/internal/planadapter"
	"github.com/airencracken/arise/internal/planvalidate"
	"github.com/airencracken/arise/internal/resolve"
)

type independentPlanAudit struct {
	fixture planvalidate.Fixture
	plan    planvalidate.Plan
}

func prepareIndependentPlanAudit(graph *resolve.DepGraph, result *resolve.ResolveResult, targets []string, cfg resolve.ResolveConfig) (*independentPlanAudit, error) {
	if graph == nil || result == nil || !result.Verified || cfg.NoDeps || cfg.OnlyDeps {
		return nil, nil
	}
	expandedTargets, err := expandIndependentAuditTargets(targets, cfg)
	if err != nil {
		return nil, err
	}
	operation := "install"
	if cfg.Update {
		operation = "update"
	}
	policy, packagePolicy := freezeIndependentPolicy(cfg)
	fixture, plan, err := planadapter.Freeze(graph, result, planadapter.Options{
		Operation: operation, Targets: expandedTargets, OriginalTargets: targets,
		Policy: policy, PackagePolicy: packagePolicy,
		// Current production scheduling uses the host root for all three
		// domains. Cross-root execution must provide independent snapshots
		// instead of enabling this alias.
		DomainsAliasToRoot: true,
	})
	if err != nil {
		return nil, err
	}
	return &independentPlanAudit{fixture: fixture, plan: plan}, nil
}

func freezeIndependentPolicy(cfg resolve.ResolveConfig) (planvalidate.Policy, func(string, string, string) planvalidate.PackagePolicy) {
	portageConfig := cfg.PortageConfig
	if portageConfig == nil {
		return planvalidate.Policy{SupportedEAPIs: []string{"7", "8", "9"}}, nil
	}
	licenseGroups := make(map[string][]string, len(portageConfig.LicenseGroups))
	for name, members := range portageConfig.LicenseGroups {
		licenseGroups[name] = append([]string(nil), members...)
	}
	policy := planvalidate.Policy{
		AcceptedKeywords: append([]string(nil), portageConfig.ACCEPT_KEYWORDS...),
		AcceptedLicenses: append([]string(nil), portageConfig.ACCEPT_LICENSE...),
		SupportedEAPIs:   []string{"7", "8", "9"},
		LicenseGroups:    licenseGroups,
	}
	arch := portageConfig.MakeConf["ARCH"]
	return policy, func(cpv, slot, repository string) planvalidate.PackagePolicy {
		mask := portageConfig.PackageMaskStatus(cpv, slot, repository)
		return planvalidate.PackagePolicy{
			BaseKeyword: arch,
			KeywordChanges: append([]string(nil),
				portageConfig.PackageAcceptKeywordsFor(cpv, slot, repository, arch)...),
			LicenseChanges: append([]string(nil),
				portageConfig.PackageLicensesFor(cpv, slot, repository)...),
			Masked: mask.Masked, MaskAtom: mask.Atom, MaskSource: mask.Source,
		}
	}
}

func expandIndependentAuditTargets(targets []string, cfg resolve.ResolveConfig) ([]string, error) {
	var expanded []string
	seen := make(map[string]bool)
	appendEntries := func(entries []string) {
		for _, entry := range entries {
			entry = strings.TrimSpace(entry)
			if entry != "" && !seen[entry] {
				seen[entry] = true
				expanded = append(expanded, entry)
			}
		}
	}
	for _, target := range targets {
		switch target {
		case "@world":
			if cfg.SystemSet != nil {
				appendEntries(cfg.SystemSet.Entries)
			}
			if cfg.WorldSet != nil {
				appendEntries(cfg.WorldSet.Entries)
			}
		case "@system":
			if cfg.SystemSet != nil {
				appendEntries(cfg.SystemSet.Entries)
			}
		default:
			if strings.HasPrefix(target, "@") {
				if cfg.PackageSetExpander == nil {
					return nil, fmt.Errorf("independent plan audit: package set %q has no expander", target)
				}
				entries, err := cfg.PackageSetExpander(target)
				if err != nil {
					return nil, fmt.Errorf("independent plan audit: expand %q: %w", target, err)
				}
				appendEntries(entries)
			} else {
				appendEntries([]string{target})
			}
		}
	}
	return expanded, nil
}

func (audit *independentPlanAudit) validate() planvalidate.ValidationResult {
	if audit == nil {
		return planvalidate.ValidationResult{Valid: true, Violations: []planvalidate.Violation{}}
	}
	return planvalidate.ValidatePlanImpact(audit.fixture, audit.plan)
}

func enforceIndependentPlanAudit(writer io.Writer, stage string, audit *independentPlanAudit) error {
	if audit == nil {
		return nil
	}
	result := audit.validate()
	reportIndependentPlanAudit(writer, stage, result)
	if result.Valid {
		return nil
	}
	return fmt.Errorf("independent plan validation rejected the %s executable plan", stage)
}

func reportIndependentPlanAudit(writer io.Writer, stage string, result planvalidate.ValidationResult) {
	if result.Valid {
		return
	}
	fmt.Fprintf(writer, "arise: independent plan validation failed at %s (%d violation(s), %d omitted); refusing package-state mutation\n",
		stage, len(result.Violations), result.OmittedViolations)
	for index, violation := range result.Violations {
		if index == 5 {
			fmt.Fprintln(writer, "  additional violations omitted from human audit output")
			break
		}
		fmt.Fprintf(writer, "  %s: %s", violation.Kind, violation.Message)
		if violation.Package != "" {
			fmt.Fprintf(writer, " [%s]", violation.Package)
		}
		if violation.Requirement != "" {
			fmt.Fprintf(writer, " requires %s", violation.Requirement)
		}
		fmt.Fprintln(writer)
	}
}
