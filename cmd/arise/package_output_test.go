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
	if !strings.Contains(got, "\x1b[32mebuild\x1b[0m") || !strings.Contains(got, "\x1b[36mU\x1b[0m") {
		t.Fatalf("colored upgrade header=%q", got)
	}
	action.Action = "reinstall"
	if got = portageActionHeader(action, false); !strings.Contains(got, "\x1b[33mR\x1b[0m") {
		t.Fatalf("colored reinstall header=%q", got)
	}
	action.Action, action.MergeType = "install", "binary"
	if got = portageActionHeader(action, false); !strings.Contains(got, "\x1b[35mbinary\x1b[0m") || !strings.Contains(got, "\x1b[32mN\x1b[0m") {
		t.Fatalf("colored binary install header=%q", got)
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
		"\x1b[32mX\x1b[0m*",
		"\x1b[33mnewflag\x1b[0m%*",
		"\x1b[34m-alsa\x1b[0m",
		"(\x1b[33m-removed\x1b[0m%*)",
		"\x1b[33msse\x1b[0m%*",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("colored USE display %q does not contain %q", got, want)
		}
	}
	action.InstalledVersion = ""
	got = portageUseDisplay(action)
	if !strings.Contains(got, "\x1b[31mX\x1b[0m") || !strings.Contains(got, "\x1b[34m-alsa\x1b[0m") {
		t.Fatalf("new-package USE colors=%q", got)
	}
}
