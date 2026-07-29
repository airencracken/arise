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

func TestPredictCommittedStateFailsClosedOnAmbiguousProvider(t *testing.T) {
	first := pkg("dev-libs/provider-1", nil)
	first.Slot, first.Subslot = "0", "1"
	second := pkg("dev-libs/provider-2", nil)
	second.Slot, second.Subslot = "1", "2"
	consumer := pkg("app-misc/consumer-1", map[string]string{"RDEPEND": "dev-libs/provider:="})
	if _, err := PredictCommittedState(State{Packages: []Package{consumer, first, second}}); err == nil {
		t.Fatal("ambiguous built slot provider accepted")
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
