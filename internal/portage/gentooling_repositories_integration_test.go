package portage

import (
	"context"
	"os"
	"path/filepath"
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
