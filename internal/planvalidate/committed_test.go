package planvalidate

import "testing"

func TestPredictCommittedStateBindsSelectedProviderSubslot(t *testing.T) {
	provider := pkg("dev-libs/provider-2", nil)
	provider.Slot, provider.Subslot = "0", "2"
	consumer := pkg("app-misc/consumer-1", map[string]string{
		"RDEPEND": "feature? ( dev-libs/provider:= )",
	})
	consumer.Use, consumer.IUse = map[string]bool{"feature": true}, map[string]bool{"feature": true}
	input := State{Packages: []Package{consumer, provider}}

	predicted, err := PredictCommittedState(input)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for _, candidate := range predicted.Packages {
		if candidate.CPV == consumer.CPV {
			got = candidate.Dependencies["RDEPEND"]
		}
	}
	if got != "feature? ( dev-libs/provider:0/2= )" {
		t.Fatalf("predicted binding = %q", got)
	}
	if input.Packages[0].Dependencies["RDEPEND"] != "feature? ( dev-libs/provider:= )" {
		t.Fatal("prediction mutated its input")
	}
}

func TestPredictCommittedStateSelectsBestProviderAndFailsClosedOnTie(t *testing.T) {
	first := pkg("dev-libs/provider-1", nil)
	first.Slot, first.Subslot = "0", "1"
	second := pkg("dev-libs/provider-2", nil)
	second.Slot, second.Subslot = "1", "2"
	consumer := pkg("app-misc/consumer-1", map[string]string{"RDEPEND": "dev-libs/provider:="})
	consumer.Authority = AuthorityEvaluated
	predicted, err := PredictCommittedState(State{Packages: []Package{consumer, first, second}})
	if err != nil {
		t.Fatal(err)
	}
	if got := predicted.Packages[0].Dependencies["RDEPEND"]; got != "dev-libs/provider:1/2=" {
		t.Fatalf("best provider binding = %q", got)
	}
	tied := second
	tied.CPV, tied.Repository = first.CPV, "other"
	if _, err := PredictCommittedState(State{Packages: []Package{consumer, first, tied}}); err == nil {
		t.Fatal("tied best built-slot provider accepted")
	}
}

func TestPredictCommittedStatePreservesLegacyVDBExpressionsButRejectsEvaluatedOnes(t *testing.T) {
	legacy := pkg("app-misc/legacy-1", map[string]string{"RDEPEND": "${DEPEND}"})
	legacy.Authority = AuthorityVDB
	predicted, err := PredictCommittedState(State{Packages: []Package{legacy}})
	if err != nil || predicted.Packages[0].Dependencies["RDEPEND"] != "${DEPEND}" {
		t.Fatalf("legacy VDB expression = %#v, %v", predicted, err)
	}
	legacy.Authority = AuthorityEvaluated
	if _, err := PredictCommittedState(State{Packages: []Package{legacy}}); err == nil {
		t.Fatal("malformed evaluated dependency expression accepted")
	}
}

func TestValidateCommittedStateDetectsIdentityUseDependencyAndAtomicityMutations(t *testing.T) {
	base := pkg("app-misc/consumer-1", map[string]string{"RDEPEND": "dev-libs/provider:0/2="})
	base.Subslot = "1"
	base.Use, base.IUse = map[string]bool{"feature": true}, map[string]bool{"feature": true}
	predicted := State{Packages: []Package{base}}
	if result := ValidateCommittedState(predicted, predicted); !result.Valid {
		t.Fatalf("identical committed state rejected: %#v", result)
	}
	emptyMetadata := cloneJSON(t, predicted)
	emptyMetadata.Packages[0].Dependencies["DEPEND"] = "  "
	if result := ValidateCommittedState(predicted, emptyMetadata); !result.Valid {
		t.Fatalf("empty dependency metadata representation rejected: %#v", result)
	}
	tests := []struct {
		name string
		edit func(*State)
		kind string
	}{
		{"missing", func(state *State) { state.Packages = nil }, "missing-actual-package"},
		{"unexpected", func(state *State) {
			extra := base
			extra.CPV = "app-misc/extra-1"
			state.Packages = append(state.Packages, extra)
		}, "unexpected-actual-package"},
		{"version", func(state *State) { state.Packages[0].CPV = "app-misc/consumer-2" }, "actual-package-identity-mismatch"},
		{"subslot", func(state *State) { state.Packages[0].Subslot = "2" }, "actual-package-identity-mismatch"},
		{"repository", func(state *State) { state.Packages[0].Repository = "other" }, "actual-package-identity-mismatch"},
		{"use", func(state *State) { state.Packages[0].Use["feature"] = false }, "actual-use-mismatch"},
		{"binding", func(state *State) { state.Packages[0].Dependencies["RDEPEND"] = "dev-libs/provider:0/1=" }, "actual-dependency-mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := cloneJSON(t, predicted)
			test.edit(&actual)
			result := ValidateCommittedState(predicted, actual)
			if result.Valid || !hasViolation(result, test.kind) {
				t.Fatalf("mutation accepted: %#v", result)
			}
			if predicted.Packages[0].CPV != base.CPV ||
				predicted.Packages[0].Dependencies["RDEPEND"] != base.Dependencies["RDEPEND"] {
				t.Fatal("comparison mutated predicted state")
			}
		})
	}
}
