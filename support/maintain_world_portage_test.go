//go:build live_portage

package support

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMaintainWorldFixMatchesEmaintInDisposableRoots(t *testing.T) {
	if _, err := exec.LookPath("emaint"); err != nil {
		t.Skip("emaint is not installed")
	}
	repoRoot := "/var/db/repos/gentoo"
	if _, err := os.Stat(filepath.Join(repoRoot, "profiles", "repo_name")); err != nil {
		t.Skipf("Gentoo repository unavailable: %v", err)
	}

	buildRoot := t.TempDir()
	binary := filepath.Join(buildRoot, "arise")
	build := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-o", binary, "../cmd/arise")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build release CLI: %v\n%s", err, output)
	}

	initial := []byte("app-misc/definitely-not-a-real-package\nsys-apps/portage\n")
	roots := []string{filepath.Join(t.TempDir(), "emaint"), filepath.Join(t.TempDir(), "arise")}
	for _, root := range roots {
		if err := os.MkdirAll(filepath.Join(root, "var", "lib", "portage"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, "var", "db", "pkg"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "var", "lib", "portage", "world"), initial, 0o640); err != nil {
			t.Fatal(err)
		}
	}

	emaint := exec.Command("emaint", "--fix", "world")
	emaint.Env = append(os.Environ(), "ROOT="+roots[0]+"/", "PORTAGE_CONFIGROOT=/")
	if output, err := emaint.CombinedOutput(); err != nil {
		t.Fatalf("emaint disposable-root repair: %v\n%s", err, output)
	}

	arise := exec.Command(binary, "maintain", "world", "--fix")
	arise.Env = append(os.Environ(),
		"ROOT="+roots[1]+"/",
		"PORTAGE_CONFIGROOT=/",
		"PORTDIR="+repoRoot,
	)
	if output, err := arise.CombinedOutput(); err != nil {
		t.Fatalf("arise disposable-root repair: %v\n%s", err, output)
	}

	emaintWorld, err := os.ReadFile(filepath.Join(roots[0], "var", "lib", "portage", "world"))
	if err != nil {
		t.Fatal(err)
	}
	ariseWorld, err := os.ReadFile(filepath.Join(roots[1], "var", "lib", "portage", "world"))
	if err != nil {
		t.Fatal(err)
	}
	if string(ariseWorld) != string(emaintWorld) {
		t.Fatalf("world repair differs:\nemaint: %q\narise:  %q", emaintWorld, ariseWorld)
	}
}
