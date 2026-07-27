package main

import (
	"reflect"
	"testing"
)

func TestWarningsForDisplayHidesCircularDependenciesUnlessVerbose(t *testing.T) {
	warnings := []string{
		"circular dependency: cat/a-1 -> cat/b-1 -> cat/a-1",
		"selected cat/old-1 uses deprecated EAPI 7",
		"selected cat/old-1 uses deprecated EAPI 7",
	}

	want := []string{"selected cat/old-1 uses deprecated EAPI 7"}
	if got := warningsForDisplay(warnings, false); !reflect.DeepEqual(got, want) {
		t.Fatalf("non-verbose warnings = %#v, want %#v", got, want)
	}
	verboseWant := warnings[:2]
	if got := warningsForDisplay(warnings, true); !reflect.DeepEqual(got, verboseWant) {
		t.Fatalf("verbose warnings = %#v, want %#v", got, verboseWant)
	}
}
