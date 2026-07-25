package main

import (
	"reflect"
	"testing"

	"github.com/airencracken/arise/internal/portage"
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
