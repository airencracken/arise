package resolve

import (
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/binpkg"
)

func TestBinaryLifecycleCandidateFallsBackOrRejects(t *testing.T) {
	for _, only := range []bool{false, true} {
		g := makeGraph()
		pkg(g, "app-misc/demo", "1", "0", "0", false, nil)
		cfg := DefaultResolveConfig()
		cfg.UsePkg, cfg.UsePkgOnly, cfg.BinpkgDir = true, only, t.TempDir()
		cfg.RemoteBinpkgs = []*binpkg.BinPkgInfo{{Category: "app-misc", Package: "demo", Version: "1", Slot: "0", DefinedPhases: "postinst", Path: "/unused"}}
		result, err := Resolve(g, []string{"app-misc/demo"}, cfg)
		if only {
			if err == nil || !strings.Contains(err.Error(), "unsupported lifecycle") {
				t.Fatalf("binary-only ignored hook: %#v, %v", result, err)
			}
		} else if err != nil || !result.Verified || len(result.Install) != 1 || result.Install[0].MergeType != "source" {
			t.Fatalf("optional binary did not fall back to source: %#v, %v", result, err)
		}
	}
}
