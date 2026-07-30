package main

import (
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/resolve"
)

func outputTestAction(t *testing.T) resolve.PkgAction {
	t.Helper()
	selected, err := atom.Parse("media-video/vlc-4.0.0")
	if err != nil {
		t.Fatal(err)
	}
	return resolve.PkgAction{
		Atom: selected, Action: "update", MergeType: "source", Slot: "0", Subslot: "12-9", Repository: "gentoo",
		IUse: "X alsa newflag cpu_flags_x86_sse", UseFlags: map[string]bool{"X": true, "alsa": false, "newflag": true, "removed": false, "cpu_flags_x86_sse": true},
		InstalledVersion: "3.0.0", InstalledSlot: "0", InstalledSubslot: "11", InstalledRepository: "gentoo",
		InstalledUseFlags:  map[string]bool{"X": false, "alsa": false, "removed": true},
		InstalledIUseFlags: map[string]bool{"X": true, "alsa": true, "removed": true},
		UseExpand:          []string{"CPU_FLAGS_X86"},
	}
}

func TestPortageActionHeaderColorsStatusFields(t *testing.T) {
	old := color.UseColor
	color.UseColor = true
	t.Cleanup(func() { color.UseColor = old })
	action := outputTestAction(t)
	got := portageActionHeader(action, false)
	if !strings.Contains(got, "\x1b[32mebuild\x1b[0m") || !strings.Contains(got, "\x1b[96mU\x1b[0m") {
		t.Fatalf("colored upgrade header=%q", got)
	}
	action.Action = "reinstall"
	if got = portageActionHeader(action, false); !strings.Contains(got, "\x1b[93mR\x1b[0m") {
		t.Fatalf("colored reinstall header=%q", got)
	}
	action.Action, action.MergeType = "install", "binary"
	if got = portageActionHeader(action, false); !strings.Contains(got, "\x1b[35mbinary\x1b[0m") || !strings.Contains(got, "\x1b[92mN\x1b[0m") {
		t.Fatalf("colored binary install header=%q", got)
	}
}

func TestColorActionAtomUsesPortageMergeRoles(t *testing.T) {
	old := color.UseColor
	color.UseColor = true
	t.Cleanup(func() { color.UseColor = old })
	action := outputTestAction(t)
	if got := colorActionAtom(action); !strings.Contains(got, "\x1b[32mmedia-video/vlc-4.0.0\x1b[0m") {
		t.Fatalf("dependency source color = %q", got)
	}
	action.Reason = "world target"
	if got := colorActionAtom(action); !strings.Contains(got, "\x1b[92mmedia-video/vlc-4.0.0\x1b[0m") {
		t.Fatalf("world source color = %q", got)
	}
	action.MergeType = "binary"
	if got := colorActionAtom(action); !strings.Contains(got, "\x1b[95mmedia-video/vlc-4.0.0\x1b[0m") {
		t.Fatalf("world binary color = %q", got)
	}
}

func TestPortageUseDisplayGroupsImplicitAndSuppressesHiddenDomains(t *testing.T) {
	action := outputTestAction(t)
	action.IUse += " abi_x86_64 elibc_glibc kernel_linux"
	action.UseFlags["abi_x86_64"] = true
	action.UseFlags["elibc_glibc"] = true
	action.UseFlags["kernel_linux"] = true
	action.UseExpandImplicit = []string{"ABI_X86", "ELIBC", "KERNEL"}
	action.UseExpandHidden = []string{"ELIBC", "KERNEL"}
	got := portageUseDisplay(action)
	if !strings.Contains(got, `ABI_X86="64%*"`) {
		t.Fatalf("implicit ABI group missing: %q", got)
	}
	for _, hidden := range []string{"elibc_glibc", "kernel_linux", "ELIBC=", "KERNEL="} {
		if strings.Contains(got, hidden) {
			t.Fatalf("hidden domain %q leaked in %q", hidden, got)
		}
	}
}

func TestPortageUseDisplayIsVerboseOnlyLikeEmerge(t *testing.T) {
	action := outputTestAction(t)
	if got := portageUseDisplayForVerbosity(action, false); got != "" {
		t.Fatalf("non-verbose USE display = %q", got)
	}
	if got := portageUseDisplayForVerbosity(action, true); got == "" {
		t.Fatal("verbose USE display is empty")
	}
}

func TestPortageActionHeaderAndPreviousIdentity(t *testing.T) {
	action := outputTestAction(t)
	if got, want := portageActionHeader(action, false), "[ebuild     U  ]"; got != want {
		t.Fatalf("header=%q want %q", got, want)
	}
	if got, want := portagePreviousIdentity(action), "3.0.0:0/11::gentoo"; got != want {
		t.Fatalf("previous=%q want %q", got, want)
	}
	action.Action = "reinstall"
	if got, want := portageActionHeader(action, false), "[ebuild   R    ]"; got != want {
		t.Fatalf("reinstall header=%q want %q", got, want)
	}
	action.Action, action.InstalledVersion = "install", ""
	if got, want := portageActionHeader(action, false), "[ebuild  N     ]"; got != want {
		t.Fatalf("install header=%q want %q", got, want)
	}
}

func TestPortageUseDisplayShowsDomainAndStateTransitions(t *testing.T) {
	action := outputTestAction(t)
	if got, want := portageUseDisplay(action), `USE="X* newflag%* -alsa (-removed%*)" CPU_FLAGS_X86="sse%*"`; got != want {
		t.Fatalf("USE=%q want %q", got, want)
	}
	action.MaskedUseFlags = map[string]bool{"alsa": true}
	if got, want := portageUseDisplay(action), `USE="X* newflag%* (-alsa) (-removed%*)" CPU_FLAGS_X86="sse%*"`; got != want {
		t.Fatalf("masked USE=%q want %q", got, want)
	}
}

func TestPortageUseDisplayColorsFlagsLikeEmerge(t *testing.T) {
	old := color.UseColor
	color.UseColor = true
	t.Cleanup(func() { color.UseColor = old })
	action := outputTestAction(t)
	got := portageUseDisplay(action)
	for _, want := range []string{
		"\x1b[92mX\x1b[0m*",
		"\x1b[93mnewflag\x1b[0m%*",
		"\x1b[94m-alsa\x1b[0m",
		"(\x1b[93m-removed\x1b[0m%*)",
		"\x1b[93msse\x1b[0m%*",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("colored USE display %q does not contain %q", got, want)
		}
	}
	action.InstalledVersion = ""
	got = portageUseDisplay(action)
	if !strings.Contains(got, "\x1b[91mX\x1b[0m") || !strings.Contains(got, "\x1b[94m-alsa\x1b[0m") {
		t.Fatalf("new-package USE colors=%q", got)
	}
}
