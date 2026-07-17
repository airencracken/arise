package plancompare

import "testing"

func TestParseAndComparePlans(t *testing.T) {
	arise, err := ParseArise(`  [update] dev-python/editables-0.6:0::gentoo
  [install] dev-python/new-1.0:1/1.2::guru`)
	if err != nil {
		t.Fatal(err)
	}
	emerge, err := ParseEmerge(`[ebuild     U  ] dev-python/editables-0.5:0/0::gentoo [0.4] USE="test" PYTHON_TARGETS="python3_14* -python3_13%"
[ebuild  N     ] dev-python/other-2.0:0::gentoo`)
	if err != nil {
		t.Fatal(err)
	}
	if len(emerge) != 2 || emerge[0].Use["PYTHON_TARGETS"][0] != "python3_14" || !emerge[0].EffectiveUse["python_targets_python3_14"] || emerge[0].EffectiveUse["python_targets_python3_13"] {
		t.Fatalf("emerge parse = %#v", emerge)
	}
	diff := Compare(arise, emerge)
	if len(diff) != 3 || diff[0].Kind != "version" || diff[1].Kind != "only-arise" || diff[2].Kind != "only-emerge" {
		t.Fatalf("differences = %#v", diff)
	}
}

func TestParseEmergeCanonicalizesUseExpandAndMarkers(t *testing.T) {
	plan, err := ParseEmerge(`[ebuild U ] media-libs/mesa-26.0.8::gentoo USE="(opengl) -test" ABI_X86="(64) -32 (-x32)" LLVM_SLOT="22%* -21*"`)
	if err != nil || len(plan) != 1 {
		t.Fatalf("plan=%#v err=%v", plan, err)
	}
	want := map[string]bool{"opengl": true, "test": false, "abi_x86_64": true, "abi_x86_32": false, "abi_x86_x32": false, "llvm_slot_22": true, "llvm_slot_21": false}
	for flag, enabled := range want {
		if got, ok := plan[0].EffectiveUse[flag]; !ok || got != enabled {
			t.Errorf("%s = %v, present %v; want %v", flag, got, ok, enabled)
		}
	}
}

func TestCompareReportsUseMismatchOnPortageReportedDomain(t *testing.T) {
	arise := []Action{{CP: "cat/pkg", Version: "1", Slot: "0", Kind: "install", EffectiveUse: map[string]bool{"feature": false, "hidden_extra": true}}}
	emerge := []Action{{CP: "cat/pkg", Version: "1", Slot: "0", Kind: "install", EffectiveUse: map[string]bool{"feature": true}}}
	diff := Compare(arise, emerge)
	if len(diff) != 1 || diff[0].Kind != "use" || len(diff[0].UseMismatch) != 1 || diff[0].UseMismatch[0] != "feature" {
		t.Fatalf("differences = %#v", diff)
	}
}

func TestParseAriseStripsColor(t *testing.T) {
	plan, err := ParseArise("  \x1b[32m[reinstall]\x1b[0m dev-libs/glib-2.0:2::gentoo")
	if err != nil || len(plan) != 1 || plan[0].Kind != "reinstall" {
		t.Fatalf("plan=%#v err=%v", plan, err)
	}
}

func TestParseAriseJSON(t *testing.T) {
	plan, err := ParseAriseJSON(`{"schema":1,"actions":[{"action":"update","cpv":"dev-libs/glib-2.84.0","slot":"2","subslot":"2.84","repository":"gentoo","merge_type":"binary","use_enabled":["dbus"],"use_disabled":["test"]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].CP != "dev-libs/glib" || plan[0].Version != "2.84.0" || plan[0].Slot != "2" || plan[0].Subslot != "2.84" || plan[0].Repository != "gentoo" || plan[0].Kind != "update" || plan[0].MergeType != "binary" {
		t.Fatalf("plan = %#v", plan)
	}
	if got := plan[0].Use["USE"]; len(got) != 2 || got[0] != "-test" || got[1] != "dbus" {
		t.Fatalf("USE = %#v", got)
	}
}

func TestCompareReportsMergeTypeMismatch(t *testing.T) {
	arise := []Action{{CP: "cat/pkg", Version: "1", Slot: "0", Repository: "gentoo", Kind: "install", MergeType: "source"}}
	emerge, err := ParseEmerge(`[binary  N     ] cat/pkg-1::gentoo`)
	if err != nil {
		t.Fatal(err)
	}
	diff := Compare(arise, emerge)
	if len(diff) != 1 || diff[0].Kind != "merge-type" {
		t.Fatalf("differences = %#v", diff)
	}
}

func TestParseAriseJSONRejectsUnknownSchema(t *testing.T) {
	if _, err := ParseAriseJSON(`{"schema":2,"actions":[]}`); err == nil {
		t.Fatal("expected unsupported schema error")
	}
}
