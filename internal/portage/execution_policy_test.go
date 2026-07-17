package portage

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestEclassLookupDirectoriesChildBeforeMasters(t *testing.T) {
	entries := []RepoEntry{
		{Name: "gentoo", Location: "/repos/gentoo"},
		{Name: "middle", Location: "/repos/middle", Masters: []string{"gentoo"}},
		{Name: "overlay", Location: "/repos/overlay", Masters: []string{"middle"}},
	}
	got, err := EclassLookupDirectories(entries, "overlay")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/repos/overlay/eclass", "/repos/middle/eclass", "/repos/gentoo/eclass"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("directories = %#v, want %#v", got, want)
	}
}

func TestEclassLookupDirectoriesRejectsBrokenGraph(t *testing.T) {
	if _, err := EclassLookupDirectories([]RepoEntry{{Name: "overlay", Location: "/overlay", Masters: []string{"missing"}}}, "overlay"); err == nil {
		t.Fatal("missing master accepted")
	}
	entries := []RepoEntry{{Name: "a", Location: "/a", Masters: []string{"b"}}, {Name: "b", Location: "/b", Masters: []string{"a"}}}
	if _, err := EclassLookupDirectories(entries, "a"); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle error = %v", err)
	}
}

func TestUserPatchDirectoriesMatchPortageSpecificity(t *testing.T) {
	got := UserPatchDirectories("/config", "cat", "pkg", "pkg-1", "r2", "3/4")
	want := []string{
		filepath.Join("/config/etc/portage/patches/cat/pkg"), filepath.Join("/config/etc/portage/patches/cat/pkg:3"),
		filepath.Join("/config/etc/portage/patches/cat/pkg-1"), filepath.Join("/config/etc/portage/patches/cat/pkg-1:3"),
		filepath.Join("/config/etc/portage/patches/cat/pkg-1-r2"), filepath.Join("/config/etc/portage/patches/cat/pkg-1-r2:3"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("patch directories = %#v, want %#v", got, want)
	}
}
