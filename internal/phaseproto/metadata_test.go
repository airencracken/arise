package phaseproto

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/metadata"
)

func TestMetadataWorkerEvaluatesEclassesWithoutRunningPhases(t *testing.T) {
	directory := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("base.eclass", `SLOT=3/7
KEYWORDS=amd64
IUSE="base"
BDEPEND="dev-lang/go"
RDEPEND="dev-libs/base"
RESTRICT=mirror
PROPERTIES=live
`)
	write("child.eclass", `inherit base
IUSE="child"
RDEPEND="dev-libs/child"
child_src_compile() { die "compile must not run"; }
EXPORT_FUNCTIONS src_compile
`)
	write("pkg-1.ebuild", `EAPI=8
inherit child base
IUSE="app"
RDEPEND="dev-libs/app"
BDEPEND+=" app-arch/unzip"
RESTRICT=fetch
PROPERTIES=interactive
DESCRIPTION="package ${PN} ${PV}"
SRC_URI="https://example.invalid/${P}.tar.gz"
[[ ${EBUILD_PHASE} == depend ]] || die "wrong metadata phase"
pkg_setup() { die "setup must not run"; }
`)
	request := Request{
		Protocol: Version, ID: "metadata-test", Command: "evaluate_metadata", EAPI: "8",
		Ebuild: filepath.Join(directory, "pkg-1.ebuild"), EclassDirs: []string{directory},
		Package: PackageIdentity{Category: "cat", PN: "pkg", PV: "1", PR: "r0", P: "pkg-1", PVR: "1", PF: "pkg-1", Slot: "0", Repository: "overlay"},
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("%v; events=%#v", err, events)
	}
	result, err := evaluatedMetadata(&metadata.PackageMetadata{Category: "cat", Package: "pkg", Version: "1", EAPI: "8", Repository: "overlay"}, events)
	if err != nil {
		t.Fatal(err)
	}
	for label, pair := range map[string][2]string{
		"slot": {result.SLOT, "3"}, "subslot": {result.Subslot, "7"}, "keywords": {result.KEYWORDS, "amd64"},
		"description": {result.DESCRIPTION, "package pkg 1"}, "source": {result.SRC_URI, "https://example.invalid/pkg-1.tar.gz"},
		"phases": {result.DEFINED_PHASES, "setup compile"}, "inherited": {result.INHERITED, "base child"},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %q, want %q", label, pair[0], pair[1])
		}
	}
	for label, pair := range map[string][2]string{
		"runtime dependencies": {result.RDEPEND, "dev-libs/app dev-libs/base dev-libs/child"},
		"build dependencies":   {result.BDEPEND, "app-arch/unzip dev-lang/go"},
		"use flags":            {result.IUSE, "app base child"},
		"restrictions":         {result.RESTRICT, "fetch mirror"},
		"properties":           {result.PROPERTIES, "interactive live"},
	} {
		got, want := strings.Fields(pair[0]), strings.Fields(pair[1])
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s = %v, want %v", label, got, want)
		}
	}
	for _, event := range events {
		if event.Kind == "phase" {
			t.Fatalf("metadata evaluation ran a phase: %#v", event)
		}
	}
}

func TestMetadataWorkerDoesNotHideBrokenInheritance(t *testing.T) {
	for _, body := range []string{
		"inherit missing\nSLOT=0\n",
		"inherit recursive\nSLOT=0\n",
	} {
		t.Run(strings.Fields(body)[1], func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "pkg-1.ebuild")
			if err := os.WriteFile(path, []byte("EAPI=8\n"+body), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "recursive.eclass"), []byte("inherit recursive\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			request := Request{Protocol: Version, ID: "broken", Command: "evaluate_metadata", EAPI: "8", Ebuild: path, EclassDirs: []string{directory}}
			events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
			if err == nil {
				t.Fatalf("broken inheritance succeeded: %#v", events)
			}
			for _, event := range events {
				if event.Kind == "metadata" {
					t.Fatalf("broken inheritance produced metadata: %#v", event)
				}
			}
		})
	}
}

func TestEvaluatedMetadataRejectsMissingRequiredFields(t *testing.T) {
	source := &metadata.PackageMetadata{Category: "cat", Package: "pkg", Version: "1", EAPI: "8"}
	for _, events := range [][]Event{
		{{Kind: "metadata", Class: "EAPI", Message: "8"}},
		{{Kind: "metadata", Class: "EAPI", Message: "7"}, {Kind: "metadata", Class: "SLOT", Message: "0"}},
	} {
		if got, err := evaluatedMetadata(source, events); err == nil || got != nil {
			t.Fatalf("incomplete metadata accepted: %#v (%v)", got, err)
		}
	}
}

func TestEvaluateMetadataHonorsCancellationBeforeWorkerLaunch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := EvaluateMetadata(ctx, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled evaluation: %v", err)
	}
}
