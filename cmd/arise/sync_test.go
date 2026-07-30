package main

import (
	"bytes"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/portage"
	internalsync "github.com/airencracken/arise/internal/sync"
)

func TestConfiguredSyncTargetsIncludesPrimaryAndAllConfiguredRepositories(t *testing.T) {
	repositories := []portage.RepoEntry{
		{Name: "local", Location: "/repos/local"},
		{Name: "overlay", Location: "/repos/overlay", SyncType: "git", SyncURI: "https://example.invalid/overlay.git"},
		{Name: "gentoo", Location: "/repos/gentoo", SyncType: "rsync", SyncURI: "rsync://example.invalid/gentoo"},
		{Name: "guru", Location: "/repos/guru", SyncType: "git", SyncURI: "https://example.invalid/guru.git"},
	}
	got := configuredSyncTargets("/repos/gentoo", "", repositories)
	want := []repositorySyncTarget{
		{Name: "gentoo", Location: "/repos/gentoo", URL: "rsync://example.invalid/gentoo", SyncType: "rsync", Primary: true},
		{Name: "guru", Location: "/repos/guru", URL: "https://example.invalid/guru.git", SyncType: "git"},
		{Name: "local", Location: "/repos/local"},
		{Name: "overlay", Location: "/repos/overlay", URL: "https://example.invalid/overlay.git", SyncType: "git"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %#v, want %#v", got, want)
	}
}

func TestConfiguredSyncTargetsOverrideAppliesOnlyToPrimary(t *testing.T) {
	repositories := []portage.RepoEntry{
		{Name: "gentoo", Location: "/repos/gentoo", SyncURI: "old-primary"},
		{Name: "overlay", Location: "/repos/overlay", SyncURI: "overlay-uri"},
		{Name: "duplicate", Location: "/repos/overlay", SyncURI: "duplicate-uri"},
	}
	got := configuredSyncTargets("/repos/gentoo/", "new-primary", repositories)
	if len(got) != 2 {
		t.Fatalf("got %d targets: %#v", len(got), got)
	}
	if got[0].URL != "new-primary" || got[1].URL != "overlay-uri" {
		t.Fatalf("override leaked across repositories: %#v", got)
	}
}

func TestSelectSyncTargetsByConfiguredName(t *testing.T) {
	targets := []repositorySyncTarget{
		{Name: "gentoo", Location: "/repos/gentoo"},
		{Name: "arise-overlay", Location: "/repos/arise"},
		{Name: "guru", Location: "/repos/guru"},
	}
	got, err := selectSyncTargets(targets, []string{"arise-overlay", "guru", "arise-overlay"})
	if err != nil {
		t.Fatal(err)
	}
	want := []repositorySyncTarget{targets[1], targets[2]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selected targets = %#v, want %#v", got, want)
	}
}

func TestSelectSyncTargetsRejectsUnknownName(t *testing.T) {
	targets := []repositorySyncTarget{{Name: "gentoo"}, {Name: "guru"}}
	_, err := selectSyncTargets(targets, []string{"missing"})
	if err == nil || !strings.Contains(err.Error(), `unknown repository "missing"`) ||
		!strings.Contains(err.Error(), "configured: gentoo, guru") {
		t.Fatalf("unknown target error = %v", err)
	}
}

func TestPrintSyncTargetReportUsesEixStylePackageChanges(t *testing.T) {
	previousColor := color.UseColor
	color.UseColor = false
	t.Cleanup(func() { color.UseColor = previousColor })

	var output bytes.Buffer
	printSyncTargetReport(&output, "arise-overlay", syncTargetReport{
		Stage: "changes", HasChanges: true,
		Changes: internalsync.ChangeSummary{Packages: []internalsync.PackageChange{
			{CP: "app-misc/new", Kind: "new", After: []string{"1"}, Description: "A new package"},
			{
				CP: "app-containers/lxc-templates", Kind: "better",
				Before:      []string{"3.0.4_p20240917", "9999"},
				After:       []string{"3.0.4_p20240917", "3.0.4_p20260719", "9999"},
				Description: "Old style template scripts for LXC",
			},
			{CP: "x11-apps/old", Kind: "removed", Before: []string{"1", "2"}, Description: "An obsolete package"},
		}},
	}, 541*time.Millisecond)

	want := "" +
		"  arise-overlay        updated      541ms\n" +
		"    [N]   >> app-misc/new (1): A new package\n" +
		"    [>]   == app-containers/lxc-templates (3.0.4_p20240917 -> 3.0.4_p20260719): Old style template scripts for LXC\n" +
		"    [D]   << x11-apps/old (2): An obsolete package\n"
	if output.String() != want {
		t.Fatalf("sync report = %q, want %q", output.String(), want)
	}
}

func TestPrintSyncTargetReportKeepsUnchangedOutputToOneLine(t *testing.T) {
	previousColor := color.UseColor
	color.UseColor = false
	t.Cleanup(func() { color.UseColor = previousColor })

	var output bytes.Buffer
	printSyncTargetReport(&output, "gentoo", syncTargetReport{Stage: "unchanged"}, 1706*time.Millisecond)
	if got, want := output.String(), "  gentoo               unchanged    1.706s\n"; got != want {
		t.Fatalf("unchanged report = %q, want %q", got, want)
	}
}

func TestPrintPackageChangesColorizesEixStyleChangeKinds(t *testing.T) {
	previousColor := color.UseColor
	color.UseColor = true
	t.Cleanup(func() { color.UseColor = previousColor })

	changes := internalsync.ChangeSummary{Packages: []internalsync.PackageChange{
		{CP: "app-misc/new", Kind: "new", After: []string{"1"}},
		{CP: "app-misc/removed", Kind: "removed", Before: []string{"1"}},
		{CP: "app-misc/upgrade", Kind: "upgrade", Before: []string{"1"}, After: []string{"2"}},
		{CP: "app-misc/better", Kind: "better", Before: []string{"1"}, After: []string{"1", "2"}},
		{CP: "app-misc/downgrade", Kind: "downgrade", Before: []string{"2"}, After: []string{"1"}},
		{CP: "app-misc/worse", Kind: "worse", Before: []string{"1", "2"}, After: []string{"2"}},
		{CP: "app-misc/changed", Kind: "changed", Before: []string{"1"}, After: []string{"1"}},
	}}
	var output bytes.Buffer
	printPackageChanges(&output, changes)
	got := output.String()

	for _, sequence := range []string{
		"[\x1b[1m\x1b[32mN\x1b[0m]",
		"[\x1b[1m\x1b[31mD\x1b[0m]",
		"[\x1b[1m\x1b[7m\x1b[36mU\x1b[0m]",
		"[\x1b[33m>\x1b[0m]",
		"[\x1b[1m\x1b[7m\x1b[34m?\x1b[0m]",
		"[\x1b[1m\x1b[31m<\x1b[0m]",
		"[\x1b[1m\x1b[33mC\x1b[0m]",
		"app-misc/\x1b[1mupgrade\x1b[0m",
		"\x1b[33m==\x1b[0m",
		"1 -> 2",
	} {
		if !strings.Contains(got, sequence) {
			t.Errorf("colored sync changes do not contain %q:\n%q", sequence, got)
		}
	}

	color.UseColor = false
	output.Reset()
	printPackageChanges(&output, changes)
	plain := output.String()
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	if stripped := ansi.ReplaceAllString(got, ""); stripped != plain {
		t.Fatalf("stripped colored output differs from plain output:\ncolored: %q\nplain:   %q", stripped, plain)
	}
}
