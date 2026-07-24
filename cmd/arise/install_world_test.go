package main

import (
	"testing"

	"github.com/airencracken/arise/internal/resolve"
	"github.com/airencracken/arise/internal/world"
)

func TestInstallWorldSelections(t *testing.T) {
	perf := planTestAtom(t, "dev-util/perf-6.19.12")
	tests := []struct {
		name    string
		targets []string
		cfg     resolve.ResolveConfig
		result  *resolve.ResolveResult
		want    []string
	}{
		{name: "category package", targets: []string{"dev-util/perf"}, want: []string{"dev-util/perf"}},
		{name: "exact atom", targets: []string{"=dev-util/perf-6.19.12"}, want: []string{"dev-util/perf"}},
		{name: "name only", targets: []string{"perf"}, result: &resolve.ResolveResult{Install: []resolve.PkgAction{{Atom: perf, Reason: "explicit target"}}}, want: []string{"dev-util/perf"}},
		{name: "oneshot", targets: []string{"dev-util/perf"}, cfg: resolve.ResolveConfig{Oneshot: true}},
		{name: "package set", targets: []string{"@preserved-rebuild"}, result: &resolve.ResolveResult{Install: []resolve.PkgAction{{Atom: perf, Reason: "explicit target"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := installWorldSelections(test.targets, test.cfg, test.result)
			if len(got) != len(test.want) {
				t.Fatalf("selections=%v, want %v", got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("selections=%v, want %v", got, test.want)
				}
			}
		})
	}
}

func TestUpdateInstallWorldIsAtomicAndIdempotent(t *testing.T) {
	path := t.TempDir() + "/world"
	if err := updateInstallWorld(path, []string{"dev-util/perf", "dev-util/perf"}); err != nil {
		t.Fatal(err)
	}
	if err := updateInstallWorld(path, []string{"dev-util/perf"}); err != nil {
		t.Fatal(err)
	}
	set, err := world.LoadWorld(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Atoms) != 1 || set.Atoms[0] != "dev-util/perf" {
		t.Fatalf("world=%v", set.Atoms)
	}
}
