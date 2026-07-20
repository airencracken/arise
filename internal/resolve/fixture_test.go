package resolve

import (
	"bytes"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestBrokenPythonTransitionFixtureIsPortableAndRepairable(t *testing.T) {
	file, err := os.Open("testdata/broken_python_transition.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	fixture, err := DecodeResolverFixture(file)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := fixture.Graph()
	if err != nil {
		t.Fatal(err)
	}
	config := fixture.ResolveConfig()
	result, err := Resolve(graph, fixture.Targets, config)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified {
		t.Fatalf("complete-graph transition was not repaired: %#v", result)
	}
	gotActions, wantActions := collectCPV(result.Install), append([]string(nil), fixture.Expectations.Arise.Actions...)
	sort.Strings(gotActions)
	sort.Strings(wantActions)
	if result.Verified != fixture.Expectations.Arise.Verified || !reflect.DeepEqual(gotActions, wantActions) {
		t.Fatalf("normalized Arise expectation mismatch: got=%v verified=%v want=%#v", gotActions, result.Verified, fixture.Expectations.Arise)
	}
	if !fixture.Expectations.Portage.Partial || fixture.Expectations.Portage.Verified {
		t.Fatalf("fixture lost normalized Portage partial-plan expectation: %#v", fixture.Expectations.Portage)
	}
	if result.Metrics.CandidateEvaluations == 0 || result.Metrics.VerifierPasses == 0 || result.Metrics.CompleteGraphPasses == 0 {
		t.Fatalf("missing scaling telemetry: %#v", result.Metrics)
	}
	want := map[string]bool{"dev-lang/python-3.14.4_p1": false, "app-misc/python-consumer-1": false}
	for _, action := range result.Install {
		if _, ok := want[action.Atom.CPV()]; ok {
			want[action.Atom.CPV()] = true
		}
	}
	for cpv, found := range want {
		if !found {
			t.Fatalf("repaired plan omitted %s: %#v", cpv, result.Install)
		}
	}
}

func TestResolverFixtureEncodingIsDeterministicPrivateAndReducible(t *testing.T) {
	fixture := &ResolverFixture{Name: "portable", Targets: []string{"cat/b", "cat/a"}, World: []string{"cat/b", "cat/a"}, Config: FixtureConfig{Deep: true}, Versions: []FixtureVersion{
		{CP: "cat/b", Version: "2", Slot: "0", Repository: "overlay", RepositoryPriority: 10, Available: true, IUse: "+feature"},
		{CP: "cat/a", Version: "1", Slot: "0", Repository: "gentoo", Installed: true, Available: true},
	}, Providers: map[string][]string{"virtual/x": {"cat/b", "cat/a"}}}
	var first bytes.Buffer
	if err := EncodeResolverFixture(&first, fixture); err != nil {
		t.Fatal(err)
	}
	fixture.Targets[0], fixture.Targets[1] = fixture.Targets[1], fixture.Targets[0]
	fixture.Versions[0], fixture.Versions[1] = fixture.Versions[1], fixture.Versions[0]
	fixture.Providers["virtual/x"][0], fixture.Providers["virtual/x"][1] = fixture.Providers["virtual/x"][1], fixture.Providers["virtual/x"][0]
	var second bytes.Buffer
	if err := EncodeResolverFixture(&second, fixture); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatalf("nondeterministic fixture:\n%s\n%s", first.String(), second.String())
	}
	reduced, err := ReduceResolverFixture(fixture, []string{"cat/b", "virtual/x", "cat/b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(reduced.Versions) != 1 || reduced.Versions[0].CP != "cat/b" || !reflect.DeepEqual(reduced.Providers["virtual/x"], []string{"cat/b"}) {
		t.Fatalf("reduced = %#v", reduced)
	}
	graph, err := reduced.Graph()
	if err != nil {
		t.Fatal(err)
	}
	if graph.Packages["cat/b"].GetBestVersion().RepositoryPriority != 10 {
		t.Fatalf("repository priority was lost")
	}
	if graph.Packages["cat/b"].GetBestVersion().IUse != "+feature" {
		t.Fatalf("raw IUSE was lost")
	}
	private := *fixture
	private.Name = "/home/user/private"
	if err := EncodeResolverFixture(&bytes.Buffer{}, &private); err == nil {
		t.Fatal("private host path was serialized")
	}
}

func TestBrokenPythonTransitionBacktrackBudgetIsOnlyACeiling(t *testing.T) {
	load := func(t *testing.T) *ResolverFixture {
		t.Helper()
		file, err := os.Open("testdata/broken_python_transition.json")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		fixture, err := DecodeResolverFixture(file)
		if err != nil {
			t.Fatal(err)
		}
		return fixture
	}
	var baseline []string
	used := -1
	for _, budget := range []int{20, 10000} {
		fixture := load(t)
		fixture.Config.Backtrack = budget
		graph, err := fixture.Graph()
		if err != nil {
			t.Fatal(err)
		}
		result, err := Resolve(graph, fixture.Targets, fixture.ResolveConfig())
		if err != nil {
			t.Fatal(err)
		}
		plan := collectCPV(result.Install)
		if baseline == nil {
			baseline, used = plan, result.BacktrackLevel
			continue
		}
		if !reflect.DeepEqual(plan, baseline) || result.BacktrackLevel != used {
			t.Fatalf("budget %d changed bounded search: plan=%v/%v backtracks=%d/%d", budget, plan, baseline, result.BacktrackLevel, used)
		}
	}
}

func TestResolverFixtureRejectsUnknownAndIncompleteInput(t *testing.T) {
	for _, input := range []string{
		`{"name":"fixture","targets":["cat/pkg"],"versions":[],"unknown":true}`,
		`{"name":"fixture","targets":[],"versions":[]}`,
	} {
		if _, err := DecodeResolverFixture(strings.NewReader(input)); err == nil {
			t.Fatalf("invalid fixture accepted: %s", input)
		}
	}
}
