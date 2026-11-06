package planadapter

import (
	"testing"

	"github.com/airencracken/arise/internal/planvalidate"
	"github.com/airencracken/arise/internal/vdb"
)

func TestInstalledStatePreservesUseAndBuiltABIConstraints(t *testing.T) {
	packages := []vdb.Package{
		{Category: "dev-libs", Package: "codec", Version: "1", Slot: "0", Subslot: "2", Repository: "gentoo", EAPI: "8", IUse: []string{"+feature"}, Use: []string{"feature"}, RequiredUse: "feature", RDepend: "sys-libs/runtime:0/1="},
		{Category: "sys-libs", Package: "runtime", Version: "1", Slot: "0", Subslot: "1", Repository: "gentoo", EAPI: "8"},
	}
	validate := func() bool {
		fixture := planvalidate.Fixture{Schema: planvalidate.SchemaVersion, Request: planvalidate.Request{Operation: "install", Targets: []string{"dev-libs/codec[feature]"}}, Installed: InstalledState(packages)}
		return planvalidate.ValidateFinalState(fixture, planvalidate.Plan{Schema: planvalidate.SchemaVersion}).Valid
	}
	if !validate() {
		t.Fatal("fresh VDB metadata did not satisfy its installed USE and ABI bindings")
	}
	packages[0].Use = nil
	if validate() {
		t.Fatal("disabled installed USE was lost in translation")
	}
	packages[0].Use = []string{"feature"}
	packages[1].Subslot = "2"
	if validate() {
		t.Fatal("installed subslot dependency was replaced with current repository semantics")
	}
}
