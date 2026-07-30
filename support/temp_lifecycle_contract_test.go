package support

import (
	"os"
	"strings"
	"testing"
)

func TestBenchmarkCannotCreateUnboundedTopLevelTemporaryObjects(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("../internal/benchmark/benchmark_test.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, forbidden := range []string{
		`os.MkdirTemp("", "arise-bench-`,
		`arise-bench-create-run-`,
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("benchmark retains unbounded temporary-object pattern %q", forbidden)
		}
	}
	for _, required := range []string{
		"pkgDir := b.TempDir()",
		"destDir := b.TempDir()",
		"cacheDir := b.TempDir()",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("benchmark omits testing-owned cleanup pattern %q", required)
		}
	}
}

func TestShellTemporaryCreatorsInstallCleanupTraps(t *testing.T) {
	t.Parallel()

	contracts := map[string]string{
		"../internal/phaseproto/worker.sh":    "trap cleanup_worker_temporaries EXIT",
		"../scripts/build-vendor-artifact.sh": "trap cleanup EXIT",
		"../scripts/test-vendor-artifact.sh":  "trap cleanup EXIT",
		"check-docs.sh":                       "trap cleanup EXIT",
		"perf/profile-p3-world.sh":            "trap cleanup EXIT",
		"perf/profile-p3-matrix.sh":           "trap cleanup EXIT",
	}
	for path, cleanupTrap := range contracts {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source := string(data)
		if !strings.Contains(source, "mktemp") {
			t.Fatalf("%s no longer exercises the temporary creator contract", path)
		}
		if !strings.Contains(source, cleanupTrap) {
			t.Errorf("%s creates temporary objects without an EXIT cleanup trap", path)
		}
	}
}
