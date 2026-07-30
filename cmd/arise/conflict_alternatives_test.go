package main

import (
	"context"
	"slices"
	"testing"

	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/resolve"
)

func TestValidateConflictAlternativesReResolvesAndIndependentlyValidates(t *testing.T) {
	graph := resolve.NewDepGraph()
	consumer := graph.AddVersion("x11-misc/redshift", "1", "0", "0", false, map[string]bool{"gtk": true}, "amd64")
	consumer.Rdepend = "gtk? ( dev-libs/libdbusmenu[gtk3] )"
	library := graph.AddVersion("dev-libs/libdbusmenu", "1", "0", "0", false, map[string]bool{"gtk3": false}, "amd64")
	consumer.IUse = "gtk"
	library.IUse = "gtk3"
	for _, version := range []*resolve.VersionInfo{consumer, library} {
		version.Repository = "gentoo"
		version.EAPI = "8"
		version.DependencyMetadataKnown = true
	}

	cfg := resolve.DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{MakeConf: map[string]string{"ARCH": "amd64"}, ACCEPT_KEYWORDS: []string{"amd64"}}
	result, err := resolve.Resolve(graph, []string{"x11-misc/redshift"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	validateConflictAlternatives(context.Background(), graph, []string{"x11-misc/redshift"}, cfg, result)

	if len(result.ConflictDetails) != 1 || len(result.ConflictDetails[0].Alternatives) != 2 {
		t.Fatalf("validated alternatives = %#v", result.ConflictDetails)
	}
	for _, alternative := range result.ConflictDetails[0].Alternatives {
		if !alternative.Validated || alternative.Command == "" {
			t.Fatalf("unvalidated advice escaped: %#v", alternative)
		}
	}
}

func TestConfigWithPackageUseDoesNotMutateSource(t *testing.T) {
	source := &portage.Config{PackageUseRules: []portage.PackageUseRule{{
		Atom: "app-misc/existing", Flags: []string{"feature"},
	}}}
	cloned := configWithPackageUse(source, "dev-libs/library", []string{"gtk3"})
	cloned.PackageUseRules[0].Atom = "changed"

	if source.PackageUseRules[0].Atom != "app-misc/existing" {
		t.Fatalf("source policy was mutated: %#v", source.PackageUseRules)
	}
	if got := cloned.PackageUseRules[1]; got.Atom != "dev-libs/library" || !slices.Equal(got.Flags, []string{"gtk3"}) {
		t.Fatalf("appended policy = %#v", got)
	}
}

func TestExplicitRemovalCandidateRejectsDependencyAndMalformedTargets(t *testing.T) {
	world := &resolve.WorldSet{Entries: []string{"app-misc/world-package", "not an atom"}}
	if !explicitRemovalCandidate([]string{"=x11-misc/redshift-1"}, world, "x11-misc/redshift") {
		t.Fatal("exact direct target was not recognized")
	}
	if !explicitRemovalCandidate(nil, world, "app-misc/world-package") {
		t.Fatal("world target was not recognized")
	}
	if explicitRemovalCandidate([]string{"not an atom"}, world, "dev-libs/transitive") {
		t.Fatal("transitive package was offered as a removal target")
	}
}

func TestTargetsWithoutPackageIsAtomicAndSlotAware(t *testing.T) {
	source := []string{"=x11-misc/redshift-1", "dev-python/pyxdg", "@world"}
	got := targetsWithoutPackage(source, "x11-misc/redshift")
	if !slices.Equal(got, []string{"dev-python/pyxdg", "@world"}) {
		t.Fatalf("filtered targets = %v", got)
	}
	if source[0] != "=x11-misc/redshift-1" {
		t.Fatalf("source targets mutated: %v", source)
	}
}

func TestExactInstalledRemovalRejectsAmbiguousSlots(t *testing.T) {
	graph := resolve.NewDepGraph()
	first := graph.AddVersionFromRepository("dev-libs/library", "1", "1", "1", true, nil, "amd64", "gentoo")
	first.Installed = true
	if removal, ok := exactInstalledRemoval(graph, "dev-libs/library"); !ok ||
		removal.Atom.String() != "=dev-libs/library-1" || removal.Slot != "1" {
		t.Fatalf("exact removal = %#v, %t", removal, ok)
	}
	second := graph.AddVersionFromRepository("dev-libs/library", "2", "2", "2", true, nil, "amd64", "gentoo")
	second.Installed = true
	if _, ok := exactInstalledRemoval(graph, "dev-libs/library"); ok {
		t.Fatal("ambiguous slotted removal was accepted")
	}
}
