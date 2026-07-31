// Package planadapter freezes resolver inputs and outputs into the independent
// plan-validation contract. It contains translation only; validation remains
// in planvalidate and does not call resolver selection helpers.
package planadapter

import (
	"encoding/json"
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
	PartialMode        string
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
				selectedAction, selectedForPlan := selected[actionKey(cpv(cp, version), version.Slot, version.Repository)]
				if !selectedForPlan {
					continue
				}
				action := &selectedAction
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
			Package: pkg, Replaces: replacedCPV(graph, action),
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
	plan.Decisions = freezeDecisionLedger(result.DecisionLedger)
	if len(plan.Decisions.Records) == 0 && len(plan.Actions) != 0 {
		plan.Decisions = selectedActionLedger(plan.Actions, result)
	}

	fixture := planvalidate.Fixture{
		Schema: planvalidate.SchemaVersion,
		Request: planvalidate.Request{
			Operation:       opts.Operation,
			Targets:         append([]string(nil), opts.Targets...),
			OriginalTargets: append([]string(nil), opts.OriginalTargets...),
			PartialMode:     opts.PartialMode,
		},
		Installed: installed,
		Available: available,
		Policy:    opts.Policy,
	}
	if opts.DomainsAliasToRoot {
		fixture.DomainsAliasToRoot = true
	}
	return fixture, plan, nil
}

func selectedActionLedger(actions []planvalidate.Action, result *resolve.ResolveResult) planvalidate.DecisionLedger {
	reasons := make(map[string]string, len(result.Install)+len(result.Uninstall))
	for _, action := range append(append([]resolve.PkgAction(nil), result.Install...), result.Uninstall...) {
		reasons[resolve.ActionIdentity(action)] = action.Reason
	}
	ledger := planvalidate.DecisionLedger{Records: make([]planvalidate.DecisionRecord, 0, len(actions))}
	for _, action := range actions {
		state := "available"
		if action.Kind == planvalidate.ActionRemove {
			state = "installed"
		}
		reason := strings.TrimSpace(reasons[action.ID])
		if reason == "" {
			reason = "selected executable action"
		}
		record := planvalidate.DecisionRecord{
			ID:      strings.Join([]string{action.Package.CPV, action.Package.Slot, action.Package.Repository, state}, "|"),
			Outcome: "selected", State: state, CPV: action.Package.CPV,
			Slot: action.Package.Slot, Subslot: action.Package.Subslot,
			Repository: action.Package.Repository, ActionID: action.ID,
			Reasons: []string{reason},
		}
		encoded, _ := json.Marshal(record)
		ledger.EncodedBytes += len(encoded)
		ledger.Records = append(ledger.Records, record)
	}
	sort.Slice(ledger.Records, func(i, j int) bool { return ledger.Records[i].ID < ledger.Records[j].ID })
	return ledger
}

func freezeDecisionLedger(source resolve.DecisionLedger) planvalidate.DecisionLedger {
	records := make([]planvalidate.DecisionRecord, len(source.Records))
	for index, record := range source.Records {
		records[index] = planvalidate.DecisionRecord{
			ID: record.ID, Outcome: record.Outcome, State: record.State,
			CPV: record.CPV, Slot: record.Slot, Subslot: record.Subslot,
			Repository: record.Repository, ActionID: record.ActionID,
			Reasons:      append([]string(nil), record.Reasons...),
			Requirements: append([]string(nil), record.Requirements...),
		}
	}
	if records == nil {
		records = []planvalidate.DecisionRecord{}
	}
	return planvalidate.DecisionLedger{
		Records: records, Truncated: source.Truncated,
		OmittedRecords: source.OmittedRecords, EncodedBytes: source.EncodedBytes,
	}
}

func replacedCPV(graph *resolve.DepGraph, action resolve.PkgAction) string {
	if action.Atom == nil {
		return ""
	}
	if action.InstalledVersion != "" {
		return action.Atom.CP() + "-" + action.InstalledVersion
	}
	if action.Action != "update" && action.Action != "reinstall" {
		return ""
	}
	node := graph.Packages[action.Atom.CP()]
	if node == nil {
		return ""
	}
	for _, version := range node.Versions {
		if version == nil || !version.Installed || version.Version == nil ||
			action.Slot != "" && version.Slot != action.Slot {
			continue
		}
		return action.Atom.CP() + "-" + version.Version.Raw
	}
	return ""
}

func packageFromVersion(cp string, version *resolve.VersionInfo, installed bool, action *resolve.PkgAction, policy func(string, string, string) planvalidate.PackagePolicy) planvalidate.Package {
	use := version.UseFlags
	iuse := parseIUse(version.IUse)
	dependencies := map[string]string{
		"DEPEND": version.Depend, "RDEPEND": version.Rdepend,
		"BDEPEND": version.Bdepend, "IDEPEND": version.Idepend, "PDEPEND": version.Pdepend,
	}
	eapi := version.EAPI
	requiredUse := version.RequiredUse
	authority := planvalidate.AuthorityEvaluated
	if installed {
		use = version.InstalledUseFlags
		iuse = version.InstalledIUseFlags
		dependencies = map[string]string{
			"DEPEND": version.InstalledDepend, "RDEPEND": version.InstalledRdepend,
			"BDEPEND": version.InstalledBdepend, "IDEPEND": version.InstalledIdepend, "PDEPEND": version.InstalledPdepend,
		}
		eapi = version.InstalledEAPI
		requiredUse = version.InstalledRequiredUse
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
		Dependencies: dependencies, RequiredUse: requiredUse,
		EAPI: eapi, Keywords: strings.Fields(version.Keywords), License: version.License,
	}
	// Package policy is consumed only by validateActionPolicy, so freeze it
	// only for selected install actions. Evaluating every package.mask,
	// package.accept_keywords, and package.license rule for every repository
	// candidate turns an independent audit into O(candidates * policy-rules)
	// work without adding validation coverage.
	if policy != nil && action != nil {
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
