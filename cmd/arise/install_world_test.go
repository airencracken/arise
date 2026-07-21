package main

import (
	"testing"

	"github.com/airencracken/arise/internal/resolve"
)

func TestLiveMutationWorldJournalGate(t *testing.T) {
	tests := []struct {
		name    string
		targets []string
		cfg     resolve.ResolveConfig
		want    bool
	}{
		{name: "world update preserves membership", targets: []string{"@world"}, want: false},
		{name: "explicit atom needs journal", targets: []string{"dev-lang/go"}, want: true},
		{name: "multiple explicit atoms need journal", targets: []string{"dev-lang/go", "dev-vcs/git"}, want: true},
		{name: "oneshot atom skips world", targets: []string{"dev-lang/go"}, cfg: resolve.ResolveConfig{Oneshot: true}, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := liveMutationNeedsWorldJournal(test.targets, test.cfg); got != test.want {
				t.Fatalf("liveMutationNeedsWorldJournal(%v) = %v, want %v", test.targets, got, test.want)
			}
		})
	}
}
