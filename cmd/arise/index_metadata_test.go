package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/graph"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/resolve"
)

func TestIndexMetadataHelper(t *testing.T) {
	root := os.Getenv("ARISE_TEST_METADATA_ROOT")
	if root == "" {
		return
	}
	*portageConfigRoot = filepath.Join(root, "etc", "portage")
	runIndex(filepath.Join(root, "db"), filepath.Join(root, "gentoo"))
}

func TestIndexEvaluatesUncachedOverlayAndPreservesPreviousOnFailure(t *testing.T) {
	if _, err := exec.LookPath("sandbox"); err != nil {
		t.Skip("Portage sandbox is unavailable")
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "vdb"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, content string) {
		t.Helper()
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("gentoo/profiles/repo_name", "gentoo\n")
	write("overlay/profiles/repo_name", "comfyware\n")
	write("overlay/metadata/layout.conf", "masters = gentoo\n")
	write("etc/portage/repos.conf/test.conf", fmt.Sprintf("[gentoo]\nlocation = %s/gentoo\n[comfyware]\nlocation = %s/overlay\n", root, root))
	for _, name := range []string{"first", "second"} {
		write("gentoo/metadata/md5-cache/dev-libs/"+name+"-1", "EAPI=8\nSLOT=0\nKEYWORDS=amd64\n")
	}
	eclass := "gentoo/eclass/dependencies.eclass"
	write(eclass, "SLOT=0\nKEYWORDS=amd64\nRDEPEND=dev-libs/first\n")
	write("overlay/www-apps/imvault/imvault-1.ebuild", `EAPI=8
inherit dependencies
RDEPEND=""
DESCRIPTION="an uncached overlay package"
src_install() { die "index must not execute src_install"; }
`)
	refresh := func() (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestIndexMetadataHelper$")
		cmd.Env = append(os.Environ(), "ARISE_TEST_METADATA_ROOT="+root)
		output, err := cmd.CombinedOutput()
		return string(output), err
	}
	assertPlan := func(dependency string) {
		t.Helper()
		db, err := ingest.OpenReadOnlyDB(filepath.Join(root, "db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		g, err := graph.BuildFromState(db, filepath.Join(root, "vdb"), 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"imvault", "www-apps/imvault"} {
			result, err := resolve.Resolve(g.ToResolveGraph(), []string{target}, resolve.DefaultResolveConfig())
			if err != nil {
				t.Fatalf("resolve %s: %v", target, err)
			}
			var packages []string
			for _, action := range result.Install {
				packages = append(packages, action.Atom.CP())
			}
			slices.Sort(packages)
			if want := []string{dependency, "www-apps/imvault"}; !slices.Equal(packages, want) || !result.Verified {
				t.Fatalf("plan for %s = %v (verified %v), want %v", target, packages, result.Verified, want)
			}
		}
	}
	if output, err := refresh(); err != nil {
		t.Fatalf("index uncached overlay: %v\n%s", err, output)
	}
	assertPlan("dev-libs/first")
	// A changed inherited eclass must be evaluated again, even when the ebuild
	// itself and the previous database generation are unchanged.
	write(eclass, "SLOT=0\nKEYWORDS=amd64\nRDEPEND=dev-libs/second\n")
	if output, err := refresh(); err != nil {
		t.Fatalf("refresh changed eclass: %v\n%s", err, output)
	}
	assertPlan("dev-libs/second")
	active, err := os.Readlink(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	write(eclass, "die 'metadata exploded'\n")
	output, err := refresh()
	if err == nil || !strings.Contains(output, "metadata exploded") || !strings.Contains(output, "previous index preserved") {
		t.Fatalf("failed refresh = %v\n%s", err, output)
	}
	if after, err := os.Readlink(filepath.Join(root, "db")); err != nil || after != active {
		t.Fatalf("failed refresh replaced active snapshot: %q -> %q (%v)", active, after, err)
	}
	assertPlan("dev-libs/second")
}
