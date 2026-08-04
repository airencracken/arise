// Package resolvertrace creates bounded, privacy-reviewed resolver diagnostics.
package resolvertrace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/bugreport"
	"github.com/airencracken/arise/internal/resolve"
)

const (
	SchemaVersion    = 1
	MaxDocumentBytes = 2 << 20
	MaxMessages      = 256
	MaxTraceRecords  = 4096
)

type Trace struct {
	Schema       int                         `json:"schema"`
	Targets      []string                    `json:"targets"`
	Verified     bool                        `json:"verified"`
	Verification string                      `json:"verification"`
	Backtracks   []resolve.BacktrackDecision `json:"backtracks"`
	Branches     []resolve.BranchEvaluation  `json:"branches"`
	Candidates   resolve.DecisionLedger      `json:"candidates"`
	Conflicts    []string                    `json:"conflicts"`
	Warnings     []string                    `json:"warnings"`
	Truncated    bool                        `json:"truncated"`
}

func New(targets []string, result *resolve.ResolveResult, redactor *bugreport.Redactor) Trace {
	if redactor == nil {
		redactor = bugreport.NewRedactor()
	}
	if result == nil {
		result = &resolve.ResolveResult{}
	}
	trace := Trace{
		Schema: SchemaVersion, Targets: cleanStrings(targets, redactor),
		Verified: result.Verified, Verification: redactor.String(result.Verification),
		Backtracks: redactBacktracks(result.DecisionHistory, redactor),
		Branches:   redactBranches(result.BranchEvaluations, redactor),
		Candidates: redactCandidates(result.DecisionLedger, redactor),
	}
	trace.Conflicts, trace.Truncated = boundedStrings(result.Conflicts, redactor, trace.Truncated)
	trace.Warnings, trace.Truncated = boundedStrings(result.Warnings, redactor, trace.Truncated)
	if len(trace.Backtracks) > MaxTraceRecords {
		trace.Backtracks, trace.Truncated = trace.Backtracks[:MaxTraceRecords], true
	}
	if len(trace.Branches) > MaxTraceRecords {
		trace.Branches, trace.Truncated = trace.Branches[:MaxTraceRecords], true
	}
	return trace
}

// Sanitize applies the privacy boundary again to a decoded trace. Importers
// must not assume that a schema-valid document was originally redacted.
func Sanitize(source Trace, redactor *bugreport.Redactor) Trace {
	if redactor == nil {
		redactor = bugreport.NewRedactor()
	}
	trace := Trace{
		Schema: SchemaVersion, Targets: cleanStrings(source.Targets, redactor),
		Verified: source.Verified, Verification: redactor.String(source.Verification),
		Backtracks: redactBacktracks(source.Backtracks, redactor), Branches: redactBranches(source.Branches, redactor),
		Candidates: redactCandidates(source.Candidates, redactor), Truncated: source.Truncated,
	}
	trace.Conflicts, trace.Truncated = boundedStrings(source.Conflicts, redactor, trace.Truncated)
	trace.Warnings, trace.Truncated = boundedStrings(source.Warnings, redactor, trace.Truncated)
	return trace
}

func Encode(w io.Writer, trace Trace) error {
	if trace.Schema != SchemaVersion {
		return fmt.Errorf("resolver trace: unsupported schema %d", trace.Schema)
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(trace); err != nil {
		return fmt.Errorf("resolver trace: encode: %w", err)
	}
	if buffer.Len() > MaxDocumentBytes {
		return fmt.Errorf("resolver trace: document exceeds %d bytes", MaxDocumentBytes)
	}
	_, err := io.Copy(w, &buffer)
	return err
}

func Decode(r io.Reader) (Trace, error) {
	limited := io.LimitReader(r, MaxDocumentBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return Trace{}, fmt.Errorf("resolver trace: read: %w", err)
	}
	if len(data) > MaxDocumentBytes {
		return Trace{}, fmt.Errorf("resolver trace: document exceeds %d bytes", MaxDocumentBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var trace Trace
	if err := decoder.Decode(&trace); err != nil {
		return Trace{}, fmt.Errorf("resolver trace: decode: %w", err)
	}
	if err := ensureEOF(decoder); err != nil {
		return Trace{}, err
	}
	if trace.Schema != SchemaVersion {
		return Trace{}, fmt.Errorf("resolver trace: unsupported schema %d", trace.Schema)
	}
	if err := validate(trace); err != nil {
		return Trace{}, err
	}
	return trace, nil
}

func validate(trace Trace) error {
	if len(trace.Conflicts) > MaxMessages || len(trace.Warnings) > MaxMessages {
		return fmt.Errorf("resolver trace: message count exceeds %d", MaxMessages)
	}
	if len(trace.Backtracks) > MaxTraceRecords || len(trace.Branches) > MaxTraceRecords {
		return fmt.Errorf("resolver trace: decision count exceeds %d", MaxTraceRecords)
	}
	for _, branch := range trace.Branches {
		if len(branch.Conflicts) > MaxMessages {
			return fmt.Errorf("resolver trace: branch conflict count exceeds %d", MaxMessages)
		}
	}
	if len(trace.Candidates.Records) > resolve.MaxDecisionRecords || trace.Candidates.OmittedRecords < 0 || trace.Candidates.EncodedBytes < 0 {
		return fmt.Errorf("resolver trace: invalid candidate ledger bounds")
	}
	return nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("resolver trace: trailing JSON value")
		}
		return fmt.Errorf("resolver trace: trailing data: %w", err)
	}
	return nil
}

func cleanStrings(values []string, redactor *bugreport.Redactor) []string {
	result := redactor.Strings(values)
	if result == nil {
		return []string{}
	}
	return result
}

func boundedStrings(values []string, redactor *bugreport.Redactor, truncated bool) ([]string, bool) {
	if len(values) > MaxMessages {
		values, truncated = values[:MaxMessages], true
	}
	return cleanStrings(values, redactor), truncated
}

func redactBacktracks(values []resolve.BacktrackDecision, redactor *bugreport.Redactor) []resolve.BacktrackDecision {
	result := append([]resolve.BacktrackDecision(nil), values...)
	for index := range result {
		result[index].Kind = redactor.String(result[index].Kind)
		result[index].Key = redactor.String(result[index].Key)
		result[index].From = redactor.String(result[index].From)
		result[index].To = redactor.String(result[index].To)
	}
	if result == nil {
		return []resolve.BacktrackDecision{}
	}
	return result
}

func redactBranches(values []resolve.BranchEvaluation, redactor *bugreport.Redactor) []resolve.BranchEvaluation {
	result := append([]resolve.BranchEvaluation(nil), values...)
	for index := range result {
		result[index].DecisionKey = redactor.String(result[index].DecisionKey)
		result[index].Option = redactor.String(result[index].Option)
		result[index].Outcome = redactor.String(result[index].Outcome)
		result[index].Conflicts, _ = boundedStrings(result[index].Conflicts, redactor, false)
	}
	if result == nil {
		return []resolve.BranchEvaluation{}
	}
	return result
}

func redactCandidates(source resolve.DecisionLedger, redactor *bugreport.Redactor) resolve.DecisionLedger {
	records := append([]resolve.CandidateDecision(nil), source.Records...)
	encodedBytes := 0
	for index := range records {
		record := &records[index]
		record.ID, record.Outcome, record.State = redactor.String(record.ID), redactor.String(record.Outcome), redactor.String(record.State)
		record.CPV, record.Slot, record.Subslot = redactor.String(record.CPV), redactor.String(record.Slot), redactor.String(record.Subslot)
		record.Repository, record.ActionID = redactor.String(record.Repository), redactor.String(record.ActionID)
		record.Reasons, record.Requirements = cleanStrings(record.Reasons, redactor), cleanStrings(record.Requirements, redactor)
		encoded, _ := json.Marshal(record)
		encodedBytes += len(encoded)
	}
	sort.SliceStable(records, func(i, j int) bool { return strings.Compare(records[i].ID, records[j].ID) < 0 })
	return resolve.DecisionLedger{Records: records, Truncated: source.Truncated, OmittedRecords: source.OmittedRecords, EncodedBytes: encodedBytes}
}
