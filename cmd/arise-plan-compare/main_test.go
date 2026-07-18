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

func TestWithoutNewsAddsMissingFeatures(t *testing.T) {
	want := []string{"PATH=/bin", "FEATURES=-news"}
	if got := withoutNews([]string{"PATH=/bin"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("withoutNews() = %v, want %v", got, want)
	}
}
