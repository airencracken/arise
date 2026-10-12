package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/plancompare"
)

func TestFailedCommandCannotCertifyValidPartialPlan(t *testing.T) {
	for _, class := range []string{plancompare.ClassEquivalentValid, plancompare.ClassValidDivergence, plancompare.ClassAriseValidPortageInvalid} {
		for _, resolved := range [][2]bool{{true, false}, {false, true}, {false, false}, {true, true}} {
			comparison := plancompare.ClassifiedComparison{Class: class, Equivalent: class == plancompare.ClassEquivalentValid}
			got := requireResolvedCommands(comparison, resolved[0], resolved[1])
			if resolved[0] && resolved[1] {
				if got.Class != class {
					t.Fatalf("resolved comparison changed: %#v", got)
				}
			} else if got.Class != plancompare.ClassInconclusive || got.Equivalent || classificationAccepted(got.Class) {
				t.Fatalf("failed command accepted: %#v", got)
			}
		}
	}
}

func TestPlanCommandErrorRetainsLaunchAndExitDiagnostics(t *testing.T) {
	missing := run(filepath.Join(t.TempDir(), "missing-tool"), nil)
	if err := planCommandError("Arise", missing, io.EOF); !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "command failed") {
		t.Fatalf("launch failure hidden by parse error: %v", err)
	}
	script := filepath.Join(t.TempDir(), "probe")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'package database unavailable' >&2\nexit 7\n"), 0755); err != nil {
		t.Fatal(err)
	}
	result := run(script, nil)
	if err := planCommandError("emerge", result, io.EOF); !errors.Is(err, result.err) || !strings.Contains(err.Error(), "package database unavailable") || !strings.Contains(err.Error(), "exit status 7") {
		t.Fatalf("failed command diagnostics lost: %v", err)
	}
	if err := planCommandError("Arise", commandResult{}, io.EOF); !errors.Is(err, io.EOF) {
		t.Fatalf("successful command parse failure lost: %v", err)
	}
}

func TestWithoutNewsPreservesCallerFeatures(t *testing.T) {
	input := []string{"PATH=/bin", "FEATURES=test sandbox", "USE=ssl"}
	want := []string{"PATH=/bin", "FEATURES=test sandbox -news", "USE=ssl"}
	if got := withoutNews(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("withoutNews() = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(input, []string{"PATH=/bin", "FEATURES=test sandbox", "USE=ssl"}) {
		t.Fatalf("withoutNews mutated caller environment: %v", input)
	}
}

func TestComparisonClassification(t *testing.T) {
	for _, test := range []struct {
		arise, portage, same bool
		want                 string
	}{
		{true, true, true, plancompare.ClassEquivalentValid},
		{true, false, false, plancompare.ClassInconclusive},
		{false, true, false, plancompare.ClassInconclusive},
		{true, true, false, plancompare.ClassInconclusive},
	} {
		var differences []plancompare.Difference
		if !test.same {
			differences = []plancompare.Difference{{Identity: "cat/pkg:0", Kind: "version"}}
		}
		got, err := classifyPlans(test.arise, test.portage, differences, "", "", "")
		if err != nil || got.Class != test.want {
			t.Fatalf("classifyPlans(%t,%t,%t)=%#v,%v want=%q", test.arise, test.portage, test.same, got, err, test.want)
		}
	}
}

func TestClassifyPlansRequiresFinalStateEvidenceForDifferentActions(t *testing.T) {
	diagnostics := []plancompare.Difference{{Identity: "cat/pkg:0", Kind: "only-arise"}}
	result, err := classifyPlans(true, true, diagnostics, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Class != plancompare.ClassInconclusive || result.Equivalent {
		t.Fatalf("difference without state evidence = %#v", result)
	}

	dir := t.TempDir()
	arisePath := filepath.Join(dir, "arise.json")
	portagePath := filepath.Join(dir, "portage.json")
	document := `{
		"schema": 1,
		"fixture": {
			"schema": 1,
			"request": {"operation": "install", "targets": []},
			"installed": [{"cpv":"cat/pkg-1","slot":"0","repository":"gentoo","metadata_authority":"vdb"}],
			"available": []
		},
		"plan": {
			"schema": 1,
			"actions": [],
			"decisions": {"records":[],"truncated":false,"omitted_records":0,"encoded_bytes":0}
		}
	}`
	if err := os.WriteFile(arisePath, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(portagePath, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = classifyPlans(true, true, diagnostics, arisePath, portagePath, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Class != plancompare.ClassEquivalentValid || !result.Equivalent ||
		!reflect.DeepEqual(result.ActionDiagnostics, diagnostics) {
		t.Fatalf("validated equal final states = %#v", result)
	}
}

func TestClassifyPlansRejectsOneSidedStateEvidence(t *testing.T) {
	if _, err := classifyPlans(true, true, nil, "arise.json", "", ""); err == nil {
		t.Fatal("one-sided state evidence accepted")
	}
}

func TestClassificationAcceptanceContract(t *testing.T) {
	for class, want := range map[string]bool{
		plancompare.ClassEquivalentValid:          true,
		plancompare.ClassValidDivergence:          true,
		plancompare.ClassAriseValidPortageInvalid: true,
		plancompare.ClassAriseInvalidPortageValid: false,
		plancompare.ClassBothInvalid:              false,
		plancompare.ClassInconclusive:             false,
	} {
		if got := classificationAccepted(class); got != want {
			t.Fatalf("classificationAccepted(%q) = %t, want %t", class, got, want)
		}
	}
}

func TestParseAriseVerifiedRequiresCompleteVerifiedResult(t *testing.T) {
	if !parseAriseVerified(`{"complete":true,"resolution":{"verified":true,"verification":"verified"}}`) {
		t.Fatal("complete verified result rejected")
	}
	if parseAriseVerified(`{"complete":false,"resolution":{"verified":true,"verification":"verified"}}`) {
		t.Fatal("incomplete result accepted")
	}
}

func TestUnresolvedPortageDiagnostics(t *testing.T) {
	for _, diagnostic := range []string{"resulting in a slot conflict", "impossible to satisfy simultaneously", "4 unsatisfied blockers"} {
		if !looksUnresolved(diagnostic) {
			t.Fatalf("unresolved diagnostic not recognized: %q", diagnostic)
		}
	}
	if looksUnresolved("ordinary warning") {
		t.Fatal("ordinary warning classified as unresolved")
	}
}

func TestWithoutNewsAddsMissingFeatures(t *testing.T) {
	want := []string{"PATH=/bin", "FEATURES=-news"}
	if got := withoutNews([]string{"PATH=/bin"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("withoutNews() = %v, want %v", got, want)
	}
}

func TestComparisonUsesExecutableOperationRoutes(t *testing.T) {
	for _, operation := range []string{"install", "update", "uninstall", "unknown"} {
		args, err := ariseOperationArgs(operation, "@world")
		if operation == "install" || operation == "update" {
			if err != nil {
				t.Fatal(err)
			}
			first := "install"
			if operation == "update" {
				first = "--update"
			}
			if len(args) != 2 || args[0] != first || args[1] != "@world" {
				t.Fatalf("invalid %s route: %v", operation, args)
			}
		} else if err == nil {
			t.Fatalf("unsupported comparison operation %s accepted", operation)
		}
	}
}
