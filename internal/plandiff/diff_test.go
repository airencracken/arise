package plandiff

import (
	"reflect"
	"testing"

	"github.com/airencracken/arise/internal/planvalidate"
)

func action(id, cpv string, use map[string]bool) planvalidate.Action {
	return planvalidate.Action{ID: id, Kind: "install", Package: planvalidate.Package{CPV: cpv, Slot: "0", Repository: "gentoo", Use: use}}
}

func TestCompareReportsDeterministicAddedRemovedAndChangedActions(t *testing.T) {
	before := planvalidate.Plan{Schema: 1, Actions: []planvalidate.Action{
		action("b", "cat/removed-1", nil), action("c", "cat/changed-1", map[string]bool{"old": true}),
	}}
	after := planvalidate.Plan{Schema: 1, Actions: []planvalidate.Action{
		action("c", "cat/changed-2", map[string]bool{"new": true}), action("a", "cat/added-1", nil),
	}}
	diff, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := []string{diff.Changes[0].Identity, diff.Changes[1].Identity, diff.Changes[2].Identity}, []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("change order = %v, want %v", got, want)
	}
	if diff.Changes[0].Kind != "added" || diff.Changes[1].Kind != "removed" || diff.Changes[2].Kind != "changed" {
		t.Fatalf("change kinds = %#v", diff.Changes)
	}
	if !reflect.DeepEqual(diff.Changes[2].Fields, []string{"package"}) {
		t.Fatalf("changed fields = %v", diff.Changes[2].Fields)
	}
}

func TestCompareDoesNotMutatePlansAndOmitsEquivalentActions(t *testing.T) {
	item := action("same", "cat/pkg-1", map[string]bool{"flag": true})
	before := planvalidate.Plan{Schema: 1, Actions: []planvalidate.Action{item}}
	after := planvalidate.Plan{Schema: 1, Actions: []planvalidate.Action{item}}
	diff, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Changes) != 0 {
		t.Fatalf("equivalent plans differ: %#v", diff)
	}
	if !before.Actions[0].Package.Use["flag"] {
		t.Fatal("input plan was mutated")
	}
}

func TestCompareRejectsUnsupportedSchemasAndDuplicateIdentities(t *testing.T) {
	valid := planvalidate.Plan{Schema: 1}
	if _, err := Compare(planvalidate.Plan{Schema: 2}, valid); err == nil {
		t.Fatal("unsupported schema accepted")
	}
	duplicate := planvalidate.Plan{Schema: 1, Actions: []planvalidate.Action{action("same", "cat/a-1", nil), action("same", "cat/b-1", nil)}}
	if _, err := Compare(duplicate, valid); err == nil {
		t.Fatal("duplicate action identity accepted")
	}
}
