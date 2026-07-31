package main

import (
	"regexp"
	"testing"
)

func TestReleaseVersion(t *testing.T) {
	if got, want := version, "0.0.14"; got != want {
		t.Fatalf("version = %q, want %q", got, want)
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(version) {
		t.Fatalf("version %q is not a stable semantic version", version)
	}
	if got, want := versionLine(), "arise 0.0.14"; got != want {
		t.Fatalf("version line = %q, want %q", got, want)
	}
}
