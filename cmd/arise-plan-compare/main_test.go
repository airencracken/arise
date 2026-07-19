package main

import (
	"reflect"
	"testing"
)

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
		{true, true, true, "equivalent-verified"},
		{true, false, false, "verified-repair-vs-unresolved-partial"},
		{false, true, false, "arise-unverified-vs-portage-resolved"},
		{true, true, false, "non-equivalent"},
	} {
		if got := classifyComparison(test.arise, test.portage, test.same); got != test.want {
			t.Fatalf("classifyComparison(%t,%t,%t)=%q want=%q", test.arise, test.portage, test.same, got, test.want)
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
