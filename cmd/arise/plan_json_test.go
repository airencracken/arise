package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/resolve"
)

func planTestAtom(t *testing.T, raw string) *atom.Atom {
	t.Helper()
	a, err := atom.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestWritePlanJSONIsVersionedDeterministicAndComplete(t *testing.T) {
	result := &resolve.ResolveResult{
		Install: []resolve.PkgAction{{
			Atom: planTestAtom(t, "dev-libs/example-2"), Action: "update", Reason: "world target",
			Slot: "0", Subslot: "2", Repository: "gentoo",
			UseFlags: map[string]bool{"zeta": true, "alpha": true, "debug": false},
		}},
		BacktrackLevel: 1,
		Warnings:       []string{"example warning"},
		Verified:       true,
		Verification:   resolve.VerificationVerified,
	}
	cfg := resolve.DefaultResolveConfig()
	cfg.Update = true
	cfg.Backtrack = 20
	var first, second bytes.Buffer
	timings := planTimings{Total: 2 * time.Second, Solver: time.Second}
	if err := writePlanJSON(&first, []string{"@world"}, cfg, result, nil, timings); err != nil {
		t.Fatal(err)
	}
	if err := writePlanJSON(&second, []string{"@world"}, cfg, result, nil, timings); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatal("JSON plan output is nondeterministic")
	}
	var document jsonPlan
	if err := json.Unmarshal(first.Bytes(), &document); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, first.String())
	}
	if document.Schema != 1 || !document.Complete || !document.Resolution.Verified || document.Resolution.Verification != resolve.VerificationVerified || document.Operation != "update" || document.Resolution.DurationNS != int64(2*time.Second) {
		t.Fatalf("document header = %#v", document)
	}
	if len(document.Actions) != 1 || document.Actions[0].Domain != string(resolve.DomainROOT) || !reflect.DeepEqual(document.Actions[0].UseEnabled, []string{"alpha", "zeta"}) || !reflect.DeepEqual(document.Actions[0].UseDisabled, []string{"debug"}) {
		t.Fatalf("action = %#v", document.Actions)
	}
}

func TestWritePlanJSONPreservesConflictedPartialPlan(t *testing.T) {
	result := &resolve.ResolveResult{
		Conflicts:       []string{"slot conflict"},
		ConflictDetails: []resolve.ConflictDetail{{Kind: "slot-conflict", Package: "dev-libs/example", Slot: "0", Message: "slot conflict"}},
	}
	var output bytes.Buffer
	if err := writePlanJSON(&output, []string{"dev-libs/example"}, resolve.DefaultResolveConfig(), result, errors.New("resolve failed"), planTimings{}); err != nil {
		t.Fatal(err)
	}
	var document jsonPlan
	if err := json.Unmarshal(output.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Complete || document.Error != "resolve failed" || len(document.Conflicts) != 1 || len(document.Details) != 1 || document.Actions == nil {
		t.Fatalf("partial plan = %#v", document)
	}
}

func TestSortedUseFlags(t *testing.T) {
	enabled, disabled := sortedUseFlags(map[string]bool{"z": true, "a": true, "m": false})
	if !reflect.DeepEqual(enabled, []string{"+a", "+z"}) || !reflect.DeepEqual(disabled, []string{"-m"}) {
		t.Fatalf("enabled=%v disabled=%v", enabled, disabled)
	}
}
