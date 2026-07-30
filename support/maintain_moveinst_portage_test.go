//go:build live_portage

package support

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestMaintainMoveInstMatchesEmaintInDisposableRoots(t *testing.T) {
	if _, err := exec.LookPath("emaint"); err != nil {
		t.Skip("emaint is not installed")
	}
	base := t.TempDir()
	repoRoot := filepath.Join(base, "repo")
	configBase := filepath.Join(base, "config")
	for _, directory := range []string{
		filepath.Join(repoRoot, "profiles", "updates"),
		filepath.Join(repoRoot, "metadata"),
		filepath.Join(configBase, "etc", "portage"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(repoRoot, "profiles", "repo_name"), "gentoo\n")
	write(filepath.Join(repoRoot, "profiles", "updates", "1Q-2026"),
		"move old-cat/old-pkg new-cat/new-pkg\nslotmove dev-libs/lib 0 1\n")
	write(filepath.Join(repoRoot, "metadata", "layout.conf"), "repo-name = gentoo\n")
	write(filepath.Join(configBase, "etc", "portage", "repos.conf"),
		"[DEFAULT]\nmain-repo = gentoo\n[gentoo]\nlocation = "+repoRoot+"\n")

	roots := []string{filepath.Join(base, "emaint-root"), filepath.Join(base, "arise-root")}
	for _, root := range roots {
		writeDifferentialVDB(t, filepath.Join(root, "var", "db", "pkg"))
	}

	emaint := exec.Command("emaint", "--fix", "moveinst")
	emaint.Env = append(os.Environ(), "ROOT="+roots[0]+"/", "PORTAGE_CONFIGROOT="+configBase+"/")
	if output, err := emaint.CombinedOutput(); err != nil {
		t.Fatalf("emaint moveinst: %v\n%s", err, output)
	}

	binary := filepath.Join(base, "arise")
	build := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-o", binary, "../cmd/arise")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build released CLI: %v\n%s", err, output)
	}
	arise := exec.Command(binary, "maintain", "moveinst", "--fix")
	arise.Env = append(os.Environ(),
		"ROOT="+roots[1]+"/",
		"PORTAGE_CONFIGROOT="+configBase+"/",
		"PORTDIR="+repoRoot,
		"PORTAGE_TMPDIR="+filepath.Join(roots[1], "var", "tmp"),
	)
	if output, err := arise.CombinedOutput(); err != nil {
		t.Fatalf("arise moveinst: %v\n%s", err, output)
	}

	emaintVDB := readTree(t, filepath.Join(roots[0], "var", "db", "pkg"))
	ariseVDB := readTree(t, filepath.Join(roots[1], "var", "db", "pkg"))
	if !reflect.DeepEqual(ariseVDB, emaintVDB) {
		t.Fatalf("VDB differential:\nemaint=%s\narise=%s", formatTree(emaintVDB), formatTree(ariseVDB))
	}
}

func writeDifferentialVDB(t *testing.T, root string) {
	t.Helper()
	writePackage := func(cpv, slot, depend, rdepend string) {
		parts := strings.SplitN(cpv, "/", 2)
		path := filepath.Join(root, parts[0], parts[1])
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		files := map[string]string{
			"CONTENTS": "", "EAPI": "8\n", "SLOT": slot + "\n", "repository": "gentoo\n",
			"BUILD_TIME": "100\n", "BDEPEND": "\n", "DEPEND": depend + "\n", "IDEPEND": "\n",
			"PDEPEND": "\n", "RDEPEND": rdepend + "\n",
		}
		for name, value := range files {
			if err := os.WriteFile(filepath.Join(path, name), []byte(value), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	writePackage("old-cat/old-pkg-2", "0", "", "dev-libs/lib:0 old-cat/old-pkg")
	writePackage("app-misc/consumer-1", "0", "flag? ( >=old-cat/old-pkg-2 )", "!dev-libs/lib:0")
	writePackage("dev-libs/lib-3", "0/sub", "", "")
}

func readTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	result := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = bytes.TrimSpace(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func formatTree(tree map[string][]byte) string {
	names := make([]string, 0, len(tree))
	for name := range tree {
		names = append(names, name)
	}
	sort.Strings(names)
	var output strings.Builder
	for _, name := range names {
		output.WriteString("\n")
		output.WriteString(name)
		output.WriteString("=")
		output.WriteString(string(tree[name]))
	}
	return output.String()
}
