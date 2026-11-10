package rebuild

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/diagnostic"
	"github.com/airencracken/arise/internal/portage"
)

func TestSourcePhaseFailureCarriesActionableDiagnostic(t *testing.T) {
	if _, err := exec.LookPath("sandbox"); err != nil {
		t.Skip("Portage sandbox is not installed")
	}
	base := t.TempDir()
	repo, root := filepath.Join(base, "repo"), filepath.Join(base, "root")
	packageDir := filepath.Join(repo, "app-misc", "failure-test")
	for _, path := range []string{packageDir, filepath.Join(repo, "eclass"), root} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	body := `EAPI=8
S="${WORKDIR}/${P}"
src_unpack() { mkdir -p "${S}"; }
src_configure() { printf 'configure: error: C compiler cannot create executables\n'; die 'configure failed'; }
`
	if err := os.WriteFile(filepath.Join(packageDir, "failure-test-1.ebuild"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := RebuildConfig{
		RepoDir: repo, RootDir: root, VdbDir: filepath.Join(root, "var/db/pkg"),
		DistfilesDir: filepath.Join(base, "distfiles"), WorkDirBase: filepath.Join(base, "work"),
		PhaseProtocol: true, Repository: "test", Repositories: []portage.RepoEntry{{Name: "test", Location: repo}},
		PhaseLogDir: filepath.Join(base, "logs"), JournalDir: filepath.Join(base, "journals"),
	}
	err := RebuildPackage(context.Background(), "app-misc/failure-test-1", &cfg)
	var failure *PhaseError
	if !errors.As(err, &failure) || failure.Phase != "src_configure" {
		t.Fatalf("source failure lost structured phase context: %v", err)
	}
	message, ok := diagnostic.Lookup(fmt.Errorf("outer operation: %w", err))
	if !ok || !strings.Contains(message.Summary, "C compiler could not link a test program") ||
		!strings.Contains(message.Summary, "app-misc/failure-test-1") || len(message.NextSteps) == 0 ||
		!strings.Contains(message.NextSteps[0], "config.log") {
		t.Fatalf("source failure diagnostic = %#v", message)
	}
	log, readErr := os.ReadFile(message.LogFile)
	if readErr != nil || !strings.Contains(string(log), "C compiler cannot create executables") {
		t.Fatalf("diagnostic did not preserve the actual build log: %q, %v", log, readErr)
	}
}
