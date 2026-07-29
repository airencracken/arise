// Package planadapter freezes resolver inputs and outputs into the independent
// plan-validation contract. It contains translation only; validation remains
// in planvalidate and does not call resolver selection helpers.
package planadapter

import (
	"fmt"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/planvalidate"
	"github.com/airencracken/arise/internal/resolve"
)

type Options struct {
	Operation          string
	Targets            []string
	OriginalTargets    []string
	Policy             planvalidate.Policy
	PackagePolicy      func(cpv, slot, repository string) planvalidate.PackagePolicy
	DomainsAliasToRoot bool
}

func Freeze(graph *resolve.DepGraph, result *resolve.ResolveResult, opts Options) (planvalidate.Fixture, planvalidate.Plan, error) {
	if graph == nil || result == nil {
		return planvalidate.Fixture{}, planvalidate.Plan{}, fmt.Errorf("plan adapter: graph and result are required")
	}
	if opts.Operation == "" {
		opts.Operation = "install"
	}
	selected := make(map[string]resolve.PkgAction)
	for _, action := range result.Install {
		if action.Atom == nil {
			return planvalidate.Fixture{}, planvalidate.Plan{}, fmt.Errorf("plan adapter: install action has nil atom")
		}
		selected[actionKey(action.Atom.CPV(), action.Slot, action.Repository)] = action
	}

	var installed, available []planvalidate.Package
	for _, cp := range sortedPackageKeys(graph) {
		node := graph.Packages[cp]
		for _, version := range sortedVersions(cp, node) {
			if version.Installed {
				installed = append(installed, packageFromVersion(cp, version, true, nil, opts.PackagePolicy))
			}
			if version.Available {
				var action *resolve.PkgAction
				if selectedAction, ok := selected[actionKey(cpv(cp, version), version.Slot, version.Repository)]; ok {
					action = &selectedAction
				}
				available = append(available, packageFromVersion(cp, version, false, action, opts.PackagePolicy))
			}
		}
	}

	plan := planvalidate.Plan{Schema: planvalidate.SchemaVersion}
	for _, action := range result.Install {
		version := findVersion(graph, action)
		if version == nil {
			return planvalidate.Fixture{}, planvalidate.Plan{}, fmt.Errorf("plan adapter: selected package %s is absent from frozen graph", action.Atom)
		}
		pkg := packageFromVersion(action.Atom.CP(), version, false, &action, opts.PackagePolicy)
		plan.Actions = append(plan.Actions, planvalidate.Action{
			ID: resolve.ActionIdentity(action), Kind: planvalidate.ActionInstall,
			Package: pkg, Replaces: replacedCPV(action),
			Prerequisites: append([]string(nil), action.Prerequisites...),
		})
	}
	for _, action := range result.Uninstall {
		version := findInstalledVersion(graph, action)
		if version == nil {
			return planvalidate.Fixture{}, planvalidate.Plan{}, fmt.Errorf("plan adapter: removal package %s is absent from frozen installed graph", action.Atom)
		}
		plan.Actions = append(plan.Actions, planvalidate.Action{
			ID:      resolve.ActionIdentity(action),
			Kind:    planvalidate.ActionRemove,
			Package: packageFromVersion(action.Atom.CP(), version, true, nil, opts.PackagePolicy),
		})
	}

	fixture := planvalidate.Fixture{
		Schema: planvalidate.SchemaVersion,
		Request: planvalidate.Request{
			Operation:       opts.Operation,
			Targets:         append([]string(nil), opts.Targets...),
			OriginalTargets: append([]string(nil), opts.OriginalTargets...),
		},
		Installed: installed,
		Available: available,
		Policy:    opts.Policy,
	}
	if opts.DomainsAliasToRoot {
		final := planvalidate.ApplyPlan(installed, plan).State.Packages
		fixture.Domains = map[string][]planvalidate.Package{
			planvalidate.DomainSysroot: final,
			planvalidate.DomainBroot:   final,
		}
	}
	return fixture, plan, nil
}

func replacedCPV(action resolve.PkgAction) string {
	if action.Atom == nil || action.InstalledVersion == "" {
		return ""
	}
	return action.Atom.CP() + "-" + action.InstalledVersion
}

func packageFromVersion(cp string, version *resolve.VersionInfo, installed bool, action *resolve.PkgAction, policy func(string, string, string) planvalidate.PackagePolicy) planvalidate.Package {
	use := version.UseFlags
	iuse := parseIUse(version.IUse)
	dependencies := map[string]string{
		"DEPEND": version.Depend, "RDEPEND": version.Rdepend,
		"BDEPEND": version.Bdepend, "IDEPEND": version.Idepend, "PDEPEND": version.Pdepend,
	}
	eapi := version.EAPI
	authority := planvalidate.AuthorityEvaluated
	if installed {
		use = version.InstalledUseFlags
		iuse = version.InstalledIUseFlags
		dependencies = map[string]string{
			"DEPEND": version.InstalledDepend, "RDEPEND": version.InstalledRdepend,
			"BDEPEND": version.InstalledBdepend, "IDEPEND": version.InstalledIdepend, "PDEPEND": version.InstalledPdepend,
		}
		eapi = version.InstalledEAPI
		authority = planvalidate.AuthorityVDB
	} else if !version.DependencyMetadataKnown {
		authority = ""
	}
	if action != nil {
		use = action.UseFlags
	}
	pkg := planvalidate.Package{
		CPV: cpv(cp, version), Slot: version.Slot, Subslot: version.Subslot,
		Repository: version.Repository, Authority: authority,
		Use: cloneBoolMap(use), IUse: cloneBoolMap(iuse),
		Dependencies: dependencies, RequiredUse: version.RequiredUse,
		EAPI: eapi, Keywords: strings.Fields(version.Keywords), License: version.License,
	}
	if policy != nil {
		pkg.Policy = policy(pkg.CPV, pkg.Slot, pkg.Repository)
		pkg.Masked = pkg.Policy.Masked
	}
	return pkg
}

func findVersion(graph *resolve.DepGraph, action resolve.PkgAction) *resolve.VersionInfo {
	if action.Atom == nil {
		return nil
	}
	node := graph.Packages[action.Atom.CP()]
	if node == nil {
		return nil
	}
	for _, version := range node.Versions {
		if action.Atom.Version != nil && version.Version.Raw != action.Atom.Version.Raw {
			continue
		}
		if action.Slot != "" && version.Slot != action.Slot ||
			action.Repository != "" && version.Repository != action.Repository {
			continue
		}
		return version
	}
	return nil
}

func findInstalledVersion(graph *resolve.DepGraph, action resolve.PkgAction) *resolve.VersionInfo {
	version := findVersion(graph, action)
	if version != nil && version.Installed {
		return version
	}
	return nil
}

func sortedPackageKeys(graph *resolve.DepGraph) []string {
	keys := make([]string, 0, len(graph.Packages))
	for cp := range graph.Packages {
		keys = append(keys, cp)
	}
	sort.Strings(keys)
	return keys
}

func sortedVersions(cp string, node *resolve.PkgNode) []*resolve.VersionInfo {
	versions := make([]*resolve.VersionInfo, 0, len(node.Versions))
	for _, version := range node.Versions {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool {
		return actionKey(cpv(cp, versions[i]), versions[i].Slot, versions[i].Repository) <
			actionKey(cpv(cp, versions[j]), versions[j].Slot, versions[j].Repository)
	})
	return versions
}

func cpv(cp string, version *resolve.VersionInfo) string {
	if version == nil || version.Version == nil || version.Version.Raw == "" {
		return cp
	}
	return cp + "-" + version.Version.Raw
}

func actionKey(cpv, slot, repository string) string {
	return cpv + "\x00" + slot + "\x00" + repository
}

func parseIUse(raw string) map[string]bool {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	result := make(map[string]bool)
	for _, flag := range strings.Fields(raw) {
		result[strings.TrimLeft(flag, "+-")] = true
	}
	return result
}

func cloneBoolMap(source map[string]bool) map[string]bool {
	if source == nil {
		return nil
	}
	result := make(map[string]bool, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
