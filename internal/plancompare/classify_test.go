package plancompare

import (
	"math/rand"
	"reflect"
	"slices"
	"testing"
)

func statePackage(cp, version string) StatePackage {
	return StatePackage{CP: cp, Version: version, Slot: "0", Repository: "gentoo"}
}

func TestClassifyFinalStatesContract(t *testing.T) {
	base := []StatePackage{statePackage("sys-libs/zlib", "1.3.1")}
	for _, test := range []struct {
		name                string
		arise, portage      StateAssessment
		policy              ClassificationPolicy
		wantClass, wantDiff string
		equivalent          bool
	}{
		{
			name:      "equivalent valid",
			arise:     StateAssessment{Validated: true, Valid: true, Packages: base},
			portage:   StateAssessment{Validated: true, Valid: true, Packages: base},
			wantClass: ClassEquivalentValid, equivalent: true,
		},
		{
			name:      "valid optional divergence",
			arise:     StateAssessment{Validated: true, Valid: true, Packages: append(append([]StatePackage(nil), base...), statePackage("app-misc/extra", "1"))},
			portage:   StateAssessment{Validated: true, Valid: true, Packages: base},
			wantClass: ClassValidDivergence, wantDiff: "optional",
		},
		{
			name:      "policy equivalent version",
			arise:     StateAssessment{Validated: true, Valid: true, Packages: []StatePackage{statePackage("sys-libs/zlib", "1.3.1")}},
			portage:   StateAssessment{Validated: true, Valid: true, Packages: []StatePackage{statePackage("sys-libs/zlib", "1.3.2")}},
			policy:    ClassificationPolicy{PolicyEquivalentIdentities: []string{"sys-libs/zlib:0"}},
			wantClass: ClassValidDivergence, wantDiff: "policy-equivalent",
		},
		{
			name:      "arise valid",
			arise:     StateAssessment{Validated: true, Valid: true, Packages: base},
			portage:   StateAssessment{Validated: true, Valid: false},
			policy:    ClassificationPolicy{RequiredIdentities: []string{"sys-libs/zlib:0"}},
			wantClass: ClassAriseValidPortageInvalid, wantDiff: "required",
		},
		{
			name:      "portage valid",
			arise:     StateAssessment{Validated: true, Valid: false},
			portage:   StateAssessment{Validated: true, Valid: true, Packages: base},
			wantClass: ClassAriseInvalidPortageValid,
		},
		{
			name:      "both invalid",
			arise:     StateAssessment{Validated: true, Valid: false},
			portage:   StateAssessment{Validated: true, Valid: false},
			wantClass: ClassBothInvalid,
		},
		{
			name:      "missing evidence",
			arise:     StateAssessment{Validated: true, Valid: true, Packages: base},
			portage:   StateAssessment{Packages: base},
			wantClass: ClassInconclusive,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := ClassifyFinalStates(test.arise, test.portage, test.policy, nil)
			if err != nil {
				t.Fatal(err)
			}
			if result.Class != test.wantClass || result.Equivalent != test.equivalent {
				t.Fatalf("classification = %#v", result)
			}
			if test.wantDiff != "" && (len(result.Differences) != 1 || result.Differences[0].Classification != test.wantDiff) {
				t.Fatalf("differences = %#v", result.Differences)
			}
		})
	}
}

func TestClassifyFinalStatesPreservesActionDifferencesAsDiagnostics(t *testing.T) {
	state := StateAssessment{Validated: true, Valid: true, Packages: []StatePackage{statePackage("cat/pkg", "1")}}
	diagnostics := []Difference{{Identity: "cat/tool:0", Kind: "only-arise"}}
	result, err := ClassifyFinalStates(state, state, ClassificationPolicy{}, diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Equivalent || result.Class != ClassEquivalentValid ||
		!reflect.DeepEqual(result.ActionDiagnostics, diagnostics) {
		t.Fatalf("action diagnostics affected validity: %#v", result)
	}
}

func TestClassifyFinalStatesMutationAndAdversarialInputs(t *testing.T) {
	valid := StateAssessment{Validated: true, Valid: true, Packages: []StatePackage{statePackage("cat/pkg", "1")}}
	for name, mutate := range map[string]func(StateAssessment) StateAssessment{
		"duplicate": func(state StateAssessment) StateAssessment {
			state.Packages = append(state.Packages, state.Packages[0])
			return state
		},
		"missing cp": func(state StateAssessment) StateAssessment {
			state.Packages[0].CP = ""
			return state
		},
		"missing slot": func(state StateAssessment) StateAssessment {
			state.Packages[0].Slot = ""
			return state
		},
		"version in cp": func(state StateAssessment) StateAssessment {
			state.Packages[0].CP = "cat/pkg-1"
			return state
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := StateAssessment{Validated: valid.Validated, Valid: valid.Valid, Packages: append([]StatePackage(nil), valid.Packages...)}
			if _, err := ClassifyFinalStates(mutate(changed), valid, ClassificationPolicy{}, nil); err == nil {
				t.Fatal("malformed final state accepted")
			}
		})
	}
	if _, err := ClassifyFinalStates(valid, valid, ClassificationPolicy{
		RequiredIdentities: []string{"cat/pkg:0"}, OptionalIdentities: []string{"cat/pkg:0"},
	}, nil); err == nil {
		t.Fatal("contradictory classification policy accepted")
	}
}

func TestClassifyFinalStatesIsDeterministicAndDoesNotMutateInputs(t *testing.T) {
	arise := StateAssessment{Validated: true, Valid: true, Packages: []StatePackage{
		{CP: "cat/b", Version: "2", Slot: "0", EffectiveUse: map[string]bool{"z": true, "a": false}},
		statePackage("cat/a", "1"),
	}}
	portage := StateAssessment{Validated: true, Valid: true, Packages: []StatePackage{
		statePackage("cat/a", "2"),
		{CP: "cat/b", Version: "2", Slot: "0", EffectiveUse: map[string]bool{"z": false, "a": false}},
	}}
	beforeArise := cloneStateAssessment(arise)
	beforePortage := cloneStateAssessment(portage)
	first, err := ClassifyFinalStates(arise, portage, ClassificationPolicy{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for iteration := 0; iteration < 50; iteration++ {
		random := rand.New(rand.NewSource(int64(iteration + 1)))
		permutedArise := cloneStateAssessment(arise)
		permutedPortage := cloneStateAssessment(portage)
		random.Shuffle(len(permutedArise.Packages), func(i, j int) {
			permutedArise.Packages[i], permutedArise.Packages[j] = permutedArise.Packages[j], permutedArise.Packages[i]
		})
		random.Shuffle(len(permutedPortage.Packages), func(i, j int) {
			permutedPortage.Packages[i], permutedPortage.Packages[j] = permutedPortage.Packages[j], permutedPortage.Packages[i]
		})
		next, err := ClassifyFinalStates(permutedArise, permutedPortage, ClassificationPolicy{}, nil)
		if err != nil || !reflect.DeepEqual(first, next) {
			t.Fatalf("nondeterministic result at %d: %#v, %v", iteration, next, err)
		}
	}
	if !reflect.DeepEqual(arise, beforeArise) || !reflect.DeepEqual(portage, beforePortage) {
		t.Fatal("classifier mutated caller state")
	}
	if len(first.Differences) != 2 || first.Differences[0].Identity != "cat/a:0" ||
		!slices.Equal(first.Differences[1].UseMismatch, []string{"z"}) {
		t.Fatalf("canonical differences = %#v", first.Differences)
	}
}

func TestClassifyFinalStatesBoundsDifferences(t *testing.T) {
	arise := StateAssessment{Validated: true, Valid: true}
	for index := 0; index < MaxStateDifferences+17; index++ {
		arise.Packages = append(arise.Packages, StatePackage{
			CP: "cat/package-x" + fixedWidth(index), Version: "1", Slot: "0",
		})
	}
	result, err := ClassifyFinalStates(arise, StateAssessment{Validated: true, Valid: true}, ClassificationPolicy{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || result.OmittedDifferences != 17 || len(result.Differences) != MaxStateDifferences {
		t.Fatalf("bounded result = %#v", result)
	}
}

func TestClassifyFinalStatesBoundsActionDiagnostics(t *testing.T) {
	diagnostics := make([]Difference, MaxActionDiagnostics+9)
	for index := range diagnostics {
		diagnostics[index] = Difference{Identity: "cat/package-" + fixedWidth(index) + ":0", Kind: "only-arise"}
	}
	state := StateAssessment{Validated: true, Valid: true}
	result, err := ClassifyFinalStates(state, state, ClassificationPolicy{}, diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ActionDiagnosticsTruncated || result.OmittedActionDiagnostics != 9 ||
		len(result.ActionDiagnostics) != MaxActionDiagnostics {
		t.Fatalf("bounded action diagnostics = %#v", result)
	}
}

func fixedWidth(value int) string {
	const digits = "000000"
	raw := []byte(digits)
	for index := len(raw) - 1; index >= 0 && value > 0; index-- {
		raw[index] = byte('0' + value%10)
		value /= 10
	}
	return string(raw)
}

func cloneStateAssessment(source StateAssessment) StateAssessment {
	cloned := source
	cloned.Packages = make([]StatePackage, len(source.Packages))
	for index, pkg := range source.Packages {
		cloned.Packages[index] = pkg
		if pkg.EffectiveUse != nil {
			cloned.Packages[index].EffectiveUse = make(map[string]bool, len(pkg.EffectiveUse))
			for flag, enabled := range pkg.EffectiveUse {
				cloned.Packages[index].EffectiveUse[flag] = enabled
			}
		}
	}
	return cloned
}
