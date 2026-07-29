package planvalidate

import (
	"encoding/json"
	"fmt"
	"sort"
)

const (
	MaxDecisionRecords = 4096
	MaxDecisionBytes   = 1 << 20
)

func validateDecisionLedger(plan Plan, violations *[]Violation) {
	actionIDs := make(map[string]Action, len(plan.Actions))
	for _, action := range plan.Actions {
		if action.ID != "" {
			actionIDs[action.ID] = action
		}
	}
	seenRecords := make(map[string]bool, len(plan.Decisions.Records))
	linkedActions := make(map[string]bool, len(plan.Actions))
	encodedBytes := 0
	previousRank, previousID := -1, ""
	for _, record := range plan.Decisions.Records {
		encoded, _ := json.Marshal(record)
		encodedBytes += len(encoded)
		if record.ID == "" {
			*violations = append(*violations, violation("missing-decision-id", record.CPV, "", "", "decision record has no stable identity"))
		} else if seenRecords[record.ID] {
			*violations = append(*violations, violation("duplicate-decision-id", record.CPV, record.ID, "", "decision ledger contains duplicate candidate identity"))
		}
		seenRecords[record.ID] = true
		rank := decisionRank(record.Outcome)
		if rank < 0 {
			*violations = append(*violations, violation("invalid-decision-outcome", record.CPV, record.Outcome, "", "decision outcome is unsupported"))
		}
		if previousRank > rank || previousRank == rank && previousID > record.ID {
			*violations = append(*violations, violation("noncanonical-decision-order", record.CPV, record.ID, "", "decision records are not canonically ordered"))
		}
		previousRank, previousID = rank, record.ID
		if record.State != "available" && record.State != "installed" {
			*violations = append(*violations, violation("invalid-decision-state", record.CPV, record.State, "", "decision candidate state is unsupported"))
		}
		if len(record.Reasons) == 0 || !sort.StringsAreSorted(record.Reasons) {
			*violations = append(*violations, violation("invalid-decision-reasons", record.CPV, record.ID, "", "decision reasons must be nonempty and sorted"))
		}
		if !sort.StringsAreSorted(record.Requirements) {
			*violations = append(*violations, violation("invalid-decision-requirements", record.CPV, record.ID, "", "decision requirements must be sorted"))
		}
		if record.Outcome == "selected" {
			action, exists := actionIDs[record.ActionID]
			if !exists {
				*violations = append(*violations, violation("selected-decision-without-action", record.CPV, record.ActionID, "", "selected decision does not link to an executable action"))
			} else if action.Package.CPV != record.CPV || action.Package.Slot != record.Slot ||
				action.Package.Repository != record.Repository {
				*violations = append(*violations, violation("decision-action-mismatch", record.CPV, record.ActionID, "", "selected decision identity differs from its executable action"))
			} else {
				linkedActions[record.ActionID] = true
			}
		} else if record.ActionID != "" {
			*violations = append(*violations, violation("nonselected-decision-with-action", record.CPV, record.ActionID, "", "non-selected decision links to an executable action"))
		}
	}
	for id, action := range actionIDs {
		if !linkedActions[id] {
			*violations = append(*violations, violation("action-without-selected-decision", action.Package.CPV, id, "", "executable action has no selected decision record"))
		}
	}
	if len(plan.Decisions.Records) > MaxDecisionRecords || encodedBytes > MaxDecisionBytes {
		*violations = append(*violations, violation("unbounded-decision-ledger", "", fmt.Sprintf("%d records/%d bytes", len(plan.Decisions.Records), encodedBytes), "", "decision ledger exceeds its independent bounds"))
	}
	if plan.Decisions.EncodedBytes != encodedBytes {
		*violations = append(*violations, violation("decision-byte-count-mismatch", "", fmt.Sprint(plan.Decisions.EncodedBytes), "", "decision ledger encoded byte count is not reproducible"))
	}
	if plan.Decisions.Truncated != (plan.Decisions.OmittedRecords > 0) ||
		plan.Decisions.OmittedRecords < 0 {
		*violations = append(*violations, violation("invalid-decision-truncation", "", fmt.Sprint(plan.Decisions.OmittedRecords), "", "decision truncation metadata is inconsistent"))
	}
}

func decisionRank(outcome string) int {
	switch outcome {
	case "selected":
		return 0
	case "retained":
		return 1
	case "rejected":
		return 2
	case "skipped":
		return 3
	default:
		return -1
	}
}
