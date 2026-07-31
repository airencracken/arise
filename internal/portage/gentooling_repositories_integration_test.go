package portage

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/gentooling"
)

func TestGentoolingRootAwareRepositoryConsumerContract(t *testing.T) {
	root := t.TempDir()
	paths := gentooling.DefaultSystemPaths(root)
	if err := os.MkdirAll(filepath.Dir(paths.ReposConf), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ReposConf, []byte(`[gentoo]
location = /var/db/repos/gentoo
sync-type = git
sync-uri = https://example.test/gentoo.git
`), 0o644); err != nil {
		t.Fatal(err)
	}
	repositories, err := gentooling.ReadRepositories(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "var", "db", "repos", "gentoo")
	if len(repositories) != 1 || repositories[0].Location != want {
		t.Fatalf("root-aware repositories = %+v, want location %q", repositories, want)
	}
}

func TestGentoolingSnapshotConsistencyModesAreExplicit(t *testing.T) {
	if gentooling.LockedAndStabilized == gentooling.StabilizedLockless {
		t.Fatal("locked and lockless snapshot modes collapsed")
	}
}

func TestGentoolingRepositoryCandidateConsumerContract(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "metadata", "md5-cache", "sys-kernel")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "gentoo-sources-6.12.1"), []byte(
		"EAPI=8\nSLOT=6.12/6.12.1\nKEYWORDS=amd64 ~arm64\nIUSE=+experimental -debug\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	inventory, err := gentooling.ReadRepositoryCandidates(context.Background(), []gentooling.Repository{{
		Name: "gentoo", Location: root,
	}}, gentooling.CandidateOptions{Integrity: gentooling.RequireComplete})
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Candidates) != 1 {
		t.Fatalf("candidate inventory = %+v", inventory)
	}
	candidate := inventory.Candidates[0]
	if candidate.ID.CPV() != "sys-kernel/gentoo-sources-6.12.1" ||
		candidate.ID.Slot != "6.12" || candidate.ID.Subslot != "6.12.1" ||
		!reflect.DeepEqual(candidate.Keywords, []string{"amd64", "~arm64"}) ||
		!reflect.DeepEqual(candidate.DeclaredUse, []gentooling.UseDeclaration{
			{Name: "experimental", Default: gentooling.UseDefaultEnabled},
			{Name: "debug", Default: gentooling.UseDefaultDisabled},
		}) {
		t.Fatalf("candidate = %+v", candidate)
	}
}

func TestGentoolingCanonicalMultilineGlobalsConsumerContract(t *testing.T) {
	globals := filepath.Join(t.TempDir(), "make.globals")
	if err := os.WriteFile(globals, []byte(`FEATURES="
	sandbox
	userpriv
	usersandbox
"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	config, err := gentooling.ReadEffectiveConfig(context.Background(), gentooling.SystemPaths{
		MakeGlobals: globals,
	}, gentooling.ConfigOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(config.Variables["FEATURES"]); !reflect.DeepEqual(got, []string{"sandbox", "userpriv", "usersandbox"}) {
		t.Fatalf("FEATURES = %q (%v)", config.Variables["FEATURES"], got)
	}
}
