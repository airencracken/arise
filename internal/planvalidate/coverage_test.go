package planvalidate

import (
	"reflect"
	"testing"
)

func TestSemanticFeaturesAreCanonicalAndPackageCountIndependent(t *testing.T) {
	fixture := Fixture{
		Schema:  SchemaVersion,
		Request: Request{Operation: "update", Targets: []string{"app-misc/client"}},
		Installed: []Package{pkg("app-misc/client-1", map[string]string{
			"RDEPEND": "feature? ( || ( dev-libs/provider:0/1= !dev-libs/blocked ) )",
		})},
		DomainsAliasToRoot: true,
	}
	plan := Plan{Schema: SchemaVersion, Actions: []Action{{
		ID: "client", Kind: ActionInstall,
		Package: pkg("app-misc/client-2", nil), Replaces: "app-misc/client-1",
		Prerequisites: []string{"provider"},
	}}}
	got := SemanticFeatures(fixture, plan)
	want := []string{
		"action:install", "action:replacement",
		"dependency-class:rdepend", "dependency-domains:root-aliased",
		"dependency:any-of", "dependency:blocker", "dependency:built-slot-operator",
		"dependency:use-conditional", "operation:update", "transaction:prerequisites",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("semantic coverage = %v, want %v", got, want)
	}
	fixture.Installed = append(fixture.Installed, pkg("app-misc/unrelated-1", nil))
	if next := SemanticFeatures(fixture, plan); !reflect.DeepEqual(next, want) {
		t.Fatalf("package count changed semantic coverage: %v", next)
	}
}
