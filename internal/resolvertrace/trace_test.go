package resolvertrace

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/bugreport"
	"github.com/airencracken/arise/internal/resolve"
)

func TestTraceRedactsEveryFreeFormResolverField(t *testing.T) {
	private := "/home/alice/private"
	secret := "token=hunter2"
	result := &resolve.ResolveResult{
		Verification: private, Conflicts: []string{private}, Warnings: []string{secret},
		DecisionHistory:   []resolve.BacktrackDecision{{Kind: private, Key: secret, From: private, To: secret}},
		BranchEvaluations: []resolve.BranchEvaluation{{DecisionKey: private, Option: secret, Outcome: private, Conflicts: []string{secret}}},
		DecisionLedger: resolve.DecisionLedger{Records: []resolve.CandidateDecision{{
			ID: private, CPV: "cat/pkg-1", Reasons: []string{secret}, Requirements: []string{private},
		}}},
	}
	trace := New([]string{private}, result, bugreport.NewRedactor(private))
	var output bytes.Buffer
	if err := Encode(&output, trace); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), private) || strings.Contains(output.String(), "hunter2") {
		t.Fatalf("resolver trace leaked private input: %s", output.String())
	}
	if !strings.Contains(output.String(), "[REDACTED]") {
		t.Fatalf("resolver trace did not record redaction: %s", output.String())
	}
}

func TestTraceEncodingIsDeterministicAndStrictlyDecodable(t *testing.T) {
	result := &resolve.ResolveResult{DecisionLedger: resolve.DecisionLedger{Records: []resolve.CandidateDecision{
		{ID: "z", CPV: "cat/z-1"}, {ID: "a", CPV: "cat/a-1"},
	}}}
	trace := New([]string{"@world"}, result, bugreport.NewRedactor("unrelated-private-value"))
	var first, second bytes.Buffer
	if err := Encode(&first, trace); err != nil {
		t.Fatal(err)
	}
	if err := Encode(&second, trace); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatal("resolver trace encoding is nondeterministic")
	}
	decoded, err := Decode(&first)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Candidates.Records) != 2 || decoded.Candidates.Records[0].ID != "a" {
		t.Fatalf("candidate order = %#v", decoded.Candidates.Records)
	}
}

func TestTraceBoundsMessagesAndRejectsHostileDocuments(t *testing.T) {
	messages := make([]string, MaxMessages+10)
	backtracks := make([]resolve.BacktrackDecision, MaxTraceRecords+1)
	branches := make([]resolve.BranchEvaluation, MaxTraceRecords+1)
	trace := New(nil, &resolve.ResolveResult{Warnings: messages, DecisionHistory: backtracks, BranchEvaluations: branches}, bugreport.NewRedactor("unrelated-private-value"))
	if len(trace.Warnings) != MaxMessages || !trace.Truncated {
		t.Fatalf("message bound = %d, truncated=%v", len(trace.Warnings), trace.Truncated)
	}
	if len(trace.Backtracks) != MaxTraceRecords || len(trace.Branches) != MaxTraceRecords {
		t.Fatalf("record bounds = %d backtracks, %d branches", len(trace.Backtracks), len(trace.Branches))
	}
	for name, input := range map[string]string{
		"unknown field":  `{"schema":1,"targets":[],"verified":false,"verification":"","backtracks":[],"branches":[],"candidates":{"records":[],"truncated":false,"omitted_records":0,"encoded_bytes":0},"conflicts":[],"warnings":[],"truncated":false,"surprise":true}`,
		"wrong schema":   `{"schema":2}`,
		"trailing value": `{"schema":1} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(input)); err == nil {
				t.Fatal("hostile document accepted")
			}
		})
	}
}

func TestDecodeRejectsSemanticBoundViolations(t *testing.T) {
	base := Trace{Schema: SchemaVersion, Targets: []string{}, Backtracks: []resolve.BacktrackDecision{}, Branches: []resolve.BranchEvaluation{}, Candidates: resolve.DecisionLedger{Records: []resolve.CandidateDecision{}}, Conflicts: []string{}, Warnings: []string{}}
	tests := map[string]Trace{
		"too many messages":          base,
		"negative ledger count":      base,
		"oversized branch conflicts": base,
	}
	tooMany := tests["too many messages"]
	tooMany.Warnings = make([]string, MaxMessages+1)
	tests["too many messages"] = tooMany
	negative := tests["negative ledger count"]
	negative.Candidates.OmittedRecords = -1
	tests["negative ledger count"] = negative
	branch := tests["oversized branch conflicts"]
	branch.Branches = []resolve.BranchEvaluation{{Conflicts: make([]string, MaxMessages+1)}}
	tests["oversized branch conflicts"] = branch
	for name, trace := range tests {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(trace)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(bytes.NewReader(encoded)); err == nil {
				t.Fatal("semantic bound violation accepted")
			}
		})
	}
}

func TestSanitizeRedactsSchemaValidImportedTrace(t *testing.T) {
	private := "/home/alice/private"
	source := Trace{Schema: SchemaVersion, Targets: []string{private}, Backtracks: []resolve.BacktrackDecision{}, Branches: []resolve.BranchEvaluation{}, Candidates: resolve.DecisionLedger{Records: []resolve.CandidateDecision{}}, Conflicts: []string{"token=hunter2"}, Warnings: []string{}}
	sanitized := Sanitize(source, bugreport.NewRedactor("/home/alice"))
	encoded, err := json.Marshal(sanitized)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), private) || strings.Contains(string(encoded), "hunter2") {
		t.Fatalf("imported trace leaked private data: %s", encoded)
	}
}
