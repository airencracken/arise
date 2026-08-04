package resolve

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/atom"
)

const (
	DecisionSelected = "selected"
	DecisionRetained = "retained"
	DecisionRejected = "rejected"
	DecisionSkipped  = "skipped"
)

func (r *resolver) buildDecisionLedger(installs, removals []PkgAction) DecisionLedger {
	records := make(map[string]CandidateDecision)
	selectedCandidates := make(map[string]bool, len(installs))
	removed := make(map[string]PkgAction, len(removals))
	for _, action := range installs {
		if action.Atom == nil {
			continue
		}
		key := candidateDecisionKey(action.Atom.CPV(), action.Slot, action.Repository, "available")
		selectedCandidates[candidatePackageKey(action.Atom.CPV(), action.Slot, action.Repository)] = true
		requirements := r.requirementsForCandidate(action.Atom.CP(), action.Slot)
		records[key] = CandidateDecision{
			ID: key, Outcome: DecisionSelected, State: "available",
			CPV: action.Atom.CPV(), Slot: action.Slot, Subslot: action.Subslot,
			Repository: action.Repository, ActionID: ActionIdentity(action),
			Reasons:      nonemptySortedUnique([]string{action.Reason}),
			Requirements: requirements,
		}
	}
	for _, action := range removals {
		if action.Atom == nil {
			continue
		}
		key := candidateDecisionKey(action.Atom.CPV(), action.Slot, action.Repository, "installed")
		removed[key] = action
		records[key] = CandidateDecision{
			ID: key, Outcome: DecisionSelected, State: "installed",
			CPV: action.Atom.CPV(), Slot: action.Slot, Subslot: action.Subslot,
			Repository: action.Repository, ActionID: ActionIdentity(action),
			Reasons: nonemptySortedUnique([]string{action.Reason}),
		}
	}

	candidateKeys := make(map[string]bool, len(r.constraints))
	for key := range r.constraints {
		candidateKeys[key] = true
	}
	// A retained installed package can block an update without receiving a
	// committed constraint of its own. Preserve its rejected alternatives in
	// the ledger so warning explanations have resolver-owned evidence.
	for _, diagnostic := range r.warningDiagnostics {
		node := r.graph.Packages[diagnostic.Blocker]
		if node == nil {
			continue
		}
		for _, version := range node.Versions {
			if version != nil && version.Slot != "" {
				candidateKeys[diagnostic.Blocker+"|"+version.Slot] = true
			}
		}
	}
	keys := make([]string, 0, len(candidateKeys))
	for key := range candidateKeys {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, constraintKey := range keys {
		cp, slot, ok := strings.Cut(constraintKey, "|")
		if !ok {
			continue
		}
		node := r.graph.Packages[cp]
		if node == nil {
			continue
		}
		requirements := make([]string, 0, len(r.constraints[constraintKey]))
		for _, constraint := range r.constraints[constraintKey] {
			if constraint != nil {
				requirements = append(requirements, constraint.String())
			}
		}
		requirements = nonemptySortedUnique(requirements)
		for _, version := range node.Versions {
			if version == nil || version.Version == nil || version.Slot != slot ||
				(!version.Available && !version.Installed) {
				continue
			}
			state := "available"
			if version.Installed {
				state = "installed"
			}
			cpv := cp + "-" + version.Version.Raw
			if selectedCandidates[candidatePackageKey(cpv, version.Slot, version.Repository)] {
				continue
			}
			key := candidateDecisionKey(cpv, version.Slot, version.Repository, state)
			if _, exists := records[key]; exists {
				continue
			}
			reasons, outcome := r.candidateDecisionOutcome(node, version, requirements, selectedReplacesSlot(installs, cp, version.Slot))
			if version.Installed {
				installedKey := candidateDecisionKey(cpv, version.Slot, version.Repository, "installed")
				if _, uninstalling := removed[installedKey]; !uninstalling &&
					!selectedReplacesSlot(installs, cp, version.Slot) {
					outcome = DecisionRetained
					reasons = []string{"retained in final state"}
				}
			}
			records[key] = CandidateDecision{
				ID: key, Outcome: outcome, State: state, CPV: cpv,
				Slot: version.Slot, Subslot: version.Subslot, Repository: version.Repository,
				Reasons: nonemptySortedUnique(reasons), Requirements: requirements,
			}
		}
	}

	ordered := make([]CandidateDecision, 0, len(records))
	for _, record := range records {
		ordered = append(ordered, record)
	}
	sort.Slice(ordered, func(i, j int) bool {
		left, right := decisionOutcomeRank(ordered[i].Outcome), decisionOutcomeRank(ordered[j].Outcome)
		if left != right {
			return left < right
		}
		return ordered[i].ID < ordered[j].ID
	})
	return boundDecisionLedger(ordered)
}

func (r *resolver) requirementsForCandidate(cp, slot string) []string {
	var requirements []string
	for _, constraint := range r.constraints[cp+"|"+slot] {
		if constraint != nil {
			requirements = append(requirements, constraint.String())
		}
	}
	return nonemptySortedUnique(requirements)
}

func (r *resolver) candidateDecisionOutcome(node *PkgNode, version *VersionInfo, requirements []string, selectedInSlot bool) ([]string, string) {
	if node != nil && node.Atom != nil && r.onlyDepsTargets[node.Atom.CP()] {
		return []string{"omitted by onlydeps partial-plan mode"}, DecisionSkipped
	}
	if status := r.versionMaskStatus(node, version); status.Masked {
		return []string{"masked by " + status.Source + " atom " + status.Atom}, DecisionRejected
	}
	if !version.Installed && !r.versionKeywordAccepted(node, version) {
		return []string{r.keywordRejectionReason(node, version)}, DecisionRejected
	}
	flags := r.candidateUseFlags(node, version)
	for _, raw := range requirements {
		constraint, err := atom.Parse(raw)
		if err != nil || !versionAtomMatches(node.Atom, constraint, version, flags) {
			return []string{"does not satisfy committed requirement " + raw}, DecisionRejected
		}
	}
	if !selectedInSlot {
		return []string{"viable candidate was not committed to the plan"}, DecisionSkipped
	}
	return []string{"lower committed preference"}, DecisionSkipped
}

func (r *resolver) keywordRejectionReason(node *PkgNode, version *VersionInfo) string {
	keywords := ""
	if version != nil {
		keywords = version.Keywords
	}
	reason := fmt.Sprintf("keywords not accepted: candidate KEYWORDS=%q", strings.TrimSpace(keywords))
	if r.portageConfig == nil || node == nil || node.Atom == nil || version == nil || version.Version == nil {
		return reason
	}
	arch := r.portageConfig.MakeConf["ARCH"]
	if arch == "" {
		arch = gentooRuntimeArch(runtime.GOARCH)
	}
	cpv := node.Atom.CP() + "-" + version.Version.Raw
	accepted := r.portageConfig.EffectiveAcceptedKeywordsFor(cpv, policySlot(version), version.Repository, arch)
	return fmt.Sprintf("%s; effective ACCEPT_KEYWORDS=%q", reason, strings.Join(accepted, " "))
}

func selectedReplacesSlot(actions []PkgAction, cp, slot string) bool {
	for _, action := range actions {
		if action.Atom != nil && action.Atom.CP() == cp && action.Slot == slot {
			return true
		}
	}
	return false
}

func candidateDecisionKey(cpv, slot, repository, state string) string {
	return strings.Join([]string{cpv, slot, repository, state}, "|")
}

func candidatePackageKey(cpv, slot, repository string) string {
	return strings.Join([]string{cpv, slot, repository}, "|")
}

func decisionOutcomeRank(outcome string) int {
	switch outcome {
	case DecisionSelected:
		return 0
	case DecisionRetained:
		return 1
	case DecisionRejected:
		return 2
	case DecisionSkipped:
		return 3
	default:
		return 4
	}
}

func nonemptySortedUnique(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	if result == nil {
		result = []string{}
	}
	return result
}

func boundDecisionLedger(records []CandidateDecision) DecisionLedger {
	ledger := DecisionLedger{Records: make([]CandidateDecision, 0, len(records))}
	for _, record := range records {
		encoded, _ := json.Marshal(record)
		size := len(encoded)
		if len(ledger.Records) == MaxDecisionRecords || ledger.EncodedBytes+size > MaxDecisionBytes {
			ledger.Truncated = true
			ledger.OmittedRecords++
			continue
		}
		ledger.Records = append(ledger.Records, record)
		ledger.EncodedBytes += size
	}
	return ledger
}
