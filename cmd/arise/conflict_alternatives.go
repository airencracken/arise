package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/resolve"
)

// validateConflictAlternatives refuses to advertise policy advice based only
// on dependency provenance. Every retained choice must produce a verified
// resolver plan and pass the independent frozen-plan validator.
func validateConflictAlternatives(ctx context.Context, graph *resolve.DepGraph, targets []string, cfg resolve.ResolveConfig, result *resolve.ResolveResult) {
	if graph == nil || result == nil {
		return
	}
	for detailIndex := range result.ConflictDetails {
		detail := &result.ConflictDetails[detailIndex]
		validated := make([]resolve.ConflictAlternative, 0, len(detail.Alternatives))
		for _, alternative := range detail.Alternatives {
			var candidate *resolve.ResolveResult
			var candidateTargets []string
			candidateCfg := cfg
			switch alternative.Kind {
			case "package-use", "requester-use":
				candidateCfg.PortageConfig = configWithPackageUse(cfg.PortageConfig, alternative.Package, alternative.UseChanges)
				candidateTargets = append([]string(nil), targets...)
				resolved, err := resolve.ResolveContext(ctx, graph, candidateTargets, candidateCfg)
				if err != nil || resolved == nil || !resolved.Verified || len(resolved.Conflicts) != 0 {
					continue
				}
				candidate = resolved
				alternative.Command = fmt.Sprintf("%s %s", alternative.Package, strings.Join(alternative.UseChanges, " "))
			case "remove-requester":
				if !explicitRemovalCandidate(targets, cfg.WorldSet, alternative.Package) {
					continue
				}
				removal, ok := exactInstalledRemoval(graph, alternative.Package)
				if !ok {
					continue
				}
				removal.Reason = "validated conflict alternative"
				candidateTargets = targetsWithoutPackage(targets, alternative.Package)
				if len(candidateTargets) == 0 {
					continue
				}
				verified, err := resolve.ResolveContext(ctx, graph, candidateTargets, candidateCfg)
				if err != nil || verified == nil || !verified.Verified || len(verified.Conflicts) != 0 {
					continue
				}
				transaction, verifyErr := resolve.VerifyTransaction(graph, verified.Install, []resolve.PkgAction{removal}, candidateCfg)
				if verifyErr != nil || transaction == nil {
					continue
				}
				// The independent validator classifies unchanged baseline
				// breakage separately; it remains the authority for whether
				// this alternative introduces any new invalid state.
				transaction.Verified = true
				transaction.Verification = resolve.VerificationVerified
				candidate = transaction
				alternative.Command = fmt.Sprintf("arise deselect %s && arise depclean", alternative.Package)
			default:
				continue
			}
			audit, err := prepareIndependentPlanAudit(graph, candidate, candidateTargets, candidateCfg)
			if err != nil || audit == nil || !audit.validate().Valid {
				continue
			}
			alternative.Validated = true
			validated = append(validated, alternative)
		}
		detail.Alternatives = validated
	}
}

func exactInstalledRemoval(graph *resolve.DepGraph, cp string) (resolve.PkgAction, bool) {
	node := graph.Packages[cp]
	if node == nil {
		return resolve.PkgAction{}, false
	}
	var installed *resolve.VersionInfo
	for _, version := range node.Versions {
		if version == nil || !version.Installed {
			continue
		}
		if installed != nil {
			return resolve.PkgAction{}, false
		}
		installed = version
	}
	if installed == nil || installed.Version == nil {
		return resolve.PkgAction{}, false
	}
	exact, err := atom.Parse("=" + cp + "-" + installed.Version.Raw)
	if err != nil {
		return resolve.PkgAction{}, false
	}
	return resolve.PkgAction{
		Atom: exact, Action: "uninstall", Slot: installed.Slot, Subslot: installed.Subslot,
		Repository: installed.Repository, Domain: resolve.DomainROOT,
	}, true
}

func configWithPackageUse(source *portage.Config, packageAtom string, changes []string) *portage.Config {
	var cloned portage.Config
	if source != nil {
		cloned = *source
	}
	cloned.PackageUseRules = append([]portage.PackageUseRule(nil), cloned.PackageUseRules...)
	cloned.PackageUseRules = append(cloned.PackageUseRules, portage.PackageUseRule{
		Atom: packageAtom, Flags: append([]string(nil), changes...),
	})
	return &cloned
}

func explicitRemovalCandidate(targets []string, worldSet *resolve.WorldSet, cp string) bool {
	for _, target := range targets {
		parsed, err := atom.Parse(target)
		if err == nil && parsed.CP() == cp {
			return true
		}
	}
	if worldSet != nil {
		for _, entry := range worldSet.Entries {
			parsed, err := atom.Parse(entry)
			if err == nil && parsed.CP() == cp {
				return true
			}
		}
	}
	return false
}

func targetsWithoutPackage(targets []string, cp string) []string {
	filtered := make([]string, 0, len(targets))
	for _, target := range targets {
		parsed, err := atom.Parse(target)
		if err == nil && parsed.CP() == cp {
			continue
		}
		filtered = append(filtered, target)
	}
	return filtered
}
