package planadapter

import (
	"testing"

	"github.com/airencracken/arise/internal/vdb"
)

func TestStateFromVDBNormalizesEffectiveUseAndDependencies(t *testing.T) {
	state := StateFromVDB([]vdb.Package{{
		Category: "app-misc", Package: "consumer", Version: "1",
		Slot: "0", Subslot: "1", Repository: "test", EAPI: "8",
		IUse: []string{"+feature", "-debug"}, Use: []string{"feature"},
		RDepend:     "dev-libs/provider:0/2=",
		RequiredUse: "feature? ( !debug )",
	}})
	if len(state.Packages) != 1 {
		t.Fatalf("VDB state = %#v", state)
	}
	pkg := state.Packages[0]
	if !pkg.Use["feature"] || pkg.Use["debug"] || !pkg.IUse["feature"] || !pkg.IUse["debug"] ||
		pkg.Dependencies["RDEPEND"] != "dev-libs/provider:0/2=" {
		t.Fatalf("normalized VDB package = %#v", pkg)
	}
	if pkg.RequiredUse != "feature? ( !debug )" {
		t.Fatalf("normalized REQUIRED_USE = %q", pkg.RequiredUse)
	}
}
