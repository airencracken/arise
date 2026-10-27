package resolve

import (
	"reflect"
	"testing"
)

func TestBlockerRemovalTracksEveryRequestingPackage(t *testing.T) {
	g := makeGraph()
	old := pkg(g, "cat/old", "1", "0", "0", true, nil)
	old.InstalledEAPI, old.DependencyMetadataKnown = "8", true
	for _, cp := range []string{"cat/first", "cat/second"} {
		parent := pkg(g, cp, "1", "0", "0", false, nil)
		parent.Available, parent.DependencyMetadataKnown, parent.EAPI = true, true, "8"
		parent.Rdepend = "!cat/old"
	}
	result, err := Resolve(g, []string{"cat/first", "cat/second"}, DefaultResolveConfig())
	if err != nil || result == nil || !result.Verified {
		t.Fatalf("resolution: %#v, %v", result, err)
	}
	if len(result.Uninstall) != 1 || !reflect.DeepEqual(result.Uninstall[0].RemovalBefore, []string{"cat/first", "cat/second"}) {
		t.Fatalf("lost blocker requesters: %#v", result.Uninstall)
	}
}
