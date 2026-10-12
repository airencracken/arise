package resolve

import (
	"testing"

	"github.com/airencracken/arise/internal/portage"
)

func TestNewUseFixedIUSEChangesRespectEnabledState(t *testing.T) {
	for _, test := range []struct {
		name                        string
		oldDeclared, newDeclared    bool
		oldEnabled, newEnabled      bool
		masked, forced, wantRebuild bool
	}{
		{"removed disabled masked flag", true, false, false, false, true, false, false},
		{"removed disabled forced flag", true, false, false, false, false, true, false},
		{"removed enabled masked flag", true, false, true, false, true, false, true},
		{"added disabled masked flag", false, true, false, false, true, false, false},
		{"added enabled forced flag", false, true, false, true, false, true, true},
		{"removed ordinary disabled flag", true, false, false, false, false, false, true},
		{"added ordinary disabled flag", false, true, false, false, false, false, true},
		{"retained flag changes value", true, true, true, false, true, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := makeGraph()
			old := pkg(g, "sys-apps/example", "1", "0", "0", true, nil)
			old.InstalledIUseFlags, old.InstalledUseFlags = map[string]bool{}, map[string]bool{}
			next := pkg(g, "sys-apps/example", "2", "0", "0", false, map[string]bool{})
			if test.oldDeclared {
				old.InstalledIUseFlags["feature"] = true
				old.InstalledUseFlags["feature"] = test.oldEnabled
			}
			if test.newDeclared {
				next.UseFlags["feature"] = test.newEnabled
			}
			policy := &portage.Config{}
			if test.masked {
				policy.UseMask = []string{"feature"}
			}
			if test.forced {
				policy.UseForce = []string{"feature"}
			}
			r := &resolver{portageConfig: policy}
			if changed := r.newUseChanged(g.Packages["sys-apps/example"], old, next, next.UseFlags); changed != test.wantRebuild {
				t.Fatalf("rebuild=%t, want %t", changed, test.wantRebuild)
			}
		})
	}
}

func TestCompleteGraphConsumerUpgradeUsesUpdateLifecycle(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/provider", "1", "0", "1", true, nil)
	pkg(g, "dev-libs/provider", "2", "0", "2", false, nil)
	old := pkg(g, "app-misc/consumer", "1", "0", "0", true, nil)
	old.EAPI, old.InstalledEAPI, old.DependencyMetadataKnown = "8", "8", true
	old.InstalledRdepend = "dev-libs/provider:0/1="
	next := pkg(g, "app-misc/consumer", "2", "0", "0", false, nil)
	next.EAPI, next.DependencyMetadataKnown, next.Rdepend = "8", true, "dev-libs/provider:="
	cfg := DefaultResolveConfig()
	cfg.Update, cfg.CompleteGraph = true, true
	result, err := Resolve(g, []string{"dev-libs/provider"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || !hasAction(result.Install, "app-misc/consumer-2", "update") {
		t.Fatalf("consumer upgrade did not use replacement lifecycle: actions=%#v conflicts=%v", result.Install, result.Conflicts)
	}
}
