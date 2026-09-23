//go:build live_portage

package phaseproto

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/portage"
)

// This differential uses the same eclass families as Comfyware. Portage only
// evaluates metadata in a disposable repository; no package phases are run.
func TestLiveNativeMetadataMatchesPortage(t *testing.T) {
	if _, err := exec.LookPath("egencache"); err != nil {
		t.Skip("egencache is unavailable")
	}
	gentoo := "/var/db/repos/gentoo"
	if _, err := os.Stat(filepath.Join(gentoo, "eclass", "go-module.eclass")); err != nil {
		t.Skip("Gentoo eclasses are unavailable")
	}
	root := t.TempDir()
	repository := filepath.Join(root, "overlay")
	write := func(path, content string) {
		t.Helper()
		path = filepath.Join(repository, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("profiles/repo_name", "metadata-probe\n")
	write("profiles/categories", "acct-group\nacct-user\nwww-apps\n")
	write("metadata/layout.conf", "masters = gentoo\ncache-formats = md5-dict\nuse-manifests = false\n")
	fixtures := map[string]string{
		"www-apps/probe-1": `inherit go-module systemd
SLOT=0
KEYWORDS="~amd64 ~arm64"
LICENSE=MIT
SRC_URI="https://example.invalid/${P}.tar.gz"
IUSE=feature
RDEPEND="acct-user/probe feature? ( dev-libs/example )"
BDEPEND+=" >=dev-lang/go-1.26 acct-user/probe"
src_compile() { die 'must not run'; }
`,
		"www-apps/probe-9999": `inherit git-r3 go-module systemd
SLOT=0
LICENSE=MIT
PROPERTIES=live
EGIT_REPO_URI="https://example.invalid/probe.git"
BDEPEND+=" >=dev-lang/go-1.26"
`,
		"acct-group/probe-0": "inherit acct-group\nACCT_GROUP_ID=-1\n",
		"acct-user/probe-0":  "inherit acct-user\nACCT_USER_ID=-1\nACCT_USER_GROUPS=( probe )\nacct-user_add_deps\n",
	}
	for cpv, body := range fixtures {
		category, pn, pvr, err := metadata.ParseCPV(cpv)
		if err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(category, pn, pn+"-"+pvr+".ebuild"), "EAPI=8\n"+body)
	}
	cache := filepath.Join(root, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	currentUser, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	currentGroup, err := user.LookupGroupId(currentUser.Gid)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	repos := fmt.Sprintf("[DEFAULT]\nmain-repo = gentoo\n[gentoo]\nlocation = %s\n[metadata-probe]\nlocation = %s\n", gentoo, repository)
	cmd := exec.CommandContext(ctx, "egencache", "--config-root", root, "--repositories-configuration", repos, "--cache-dir", cache, "--repo=metadata-probe", "--update")
	cmd.Env = append(os.Environ(), "PORTAGE_USERNAME="+currentUser.Username, "PORTAGE_GRPNAME="+currentGroup.Name)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Portage reference: %v\n%s", err, output)
	} else {
		t.Logf("Portage reference output: %s", output)
	}
	entries := []portage.RepoEntry{{Name: "gentoo", Location: gentoo}, {Name: "metadata-probe", Location: repository, Masters: []string{"gentoo"}}}
	for cpv := range fixtures {
		t.Run(cpv, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(repository, "metadata", "md5-cache", cpv))
			if err != nil {
				t.Fatal(err)
			}
			want, err := metadata.ParseCacheEntry(cpv, data)
			if err != nil {
				t.Fatal(err)
			}
			source := &metadata.PackageMetadata{Category: want.Category, Package: want.Package, Version: want.Version, EAPI: "8", Repository: "metadata-probe", RepositoryPath: repository}
			got, err := EvaluateMetadata(ctx, source, entries)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"EAPI", "SLOT", "KEYWORDS", "LICENSE", "DESCRIPTION", "HOMEPAGE", "SRC_URI", "DEPEND", "BDEPEND", "RDEPEND", "IDEPEND", "PDEPEND", "IUSE", "REQUIRED_USE", "PROPERTIES", "RESTRICT", "DEFINED_PHASES"} {
				actual, expected := strings.Fields(got.Unknown[key]), strings.Fields(want.Unknown[key])
				// DEFINED_PHASES is a set; the worker emits phase execution order.
				if key == "DEFINED_PHASES" {
					slices.Sort(actual)
					slices.Sort(expected)
				}
				if !slices.Equal(actual, expected) {
					t.Errorf("%s: Arise %q, Portage %q", key, actual, expected)
				}
			}
		})
	}
}
