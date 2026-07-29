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
	for _, target := range targets {
		if strings.HasPrefix(target, "@") {
			// Set semantics require a frozen expansion record, which is a
			// separate adapter contract. Do not pretend a set is a package atom.
			return nil, nil
		}
	}
	operation := "install"
	if cfg.Update {
		operation = "update"
	}
	fixture, plan, err := planadapter.Freeze(graph, result, planadapter.Options{
		Operation: operation, Targets: targets,
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

func (audit *independentPlanAudit) validate() planvalidate.ValidationResult {
	if audit == nil {
		return planvalidate.ValidationResult{Valid: true, Violations: []planvalidate.Violation{}}
	}
	return planvalidate.ValidatePlanImpact(audit.fixture, audit.plan)
}

func reportIndependentPlanAudit(writer io.Writer, stage string, result planvalidate.ValidationResult) {
	if result.Valid {
		return
	}
	fmt.Fprintf(writer, "arise: independent plan validation audit failed at %s (%d violation(s), %d omitted); execution remains audit-only\n",
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
