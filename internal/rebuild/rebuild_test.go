package rebuild

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/airencracken/arise/internal/portage"
)

func TestFindEbuild(t *testing.T) {
	tmp := t.TempDir()
	pkgDir := filepath.Join(tmp, "sys-apps", "hello")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}

	files := []string{
		"hello-1.0.ebuild",
		"hello-1.1.ebuild",
		"hello-2.0.ebuild",
		"metadata.xml",
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(pkgDir, f), []byte("EAPI=8"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("exact version match", func(t *testing.T) {
		path, err := findEbuild(tmp, "sys-apps", "hello", "1.0")
		if err != nil {
			t.Fatalf("findEbuild: %v", err)
		}
		if filepath.Base(path) != "hello-1.0.ebuild" {
			t.Errorf("got %q, want hello-1.0.ebuild", filepath.Base(path))
		}
	})

	t.Run("missing version", func(t *testing.T) {
		_, err := findEbuild(tmp, "sys-apps", "hello", "9.9.9")
		if err == nil {
			t.Error("expected error for missing version, got nil")
		}
	})

	t.Run("missing package", func(t *testing.T) {
		_, err := findEbuild(tmp, "sys-apps", "nonexistent", "1.0")
		if err == nil {
			t.Error("expected error for missing package, got nil")
		}
	})
}

func TestFindEbuild_EmptyDir(t *testing.T) {
	tmp := t.TempDir()
	pkgDir := filepath.Join(tmp, "cat", "pkg")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}

	_, err := findEbuild(tmp, "cat", "pkg", "1.0")
	if err == nil {
		t.Error("expected error for empty directory, got nil")
	}
}

func TestResolveURIs(t *testing.T) {
	vars := map[string]string{
		"PN":   "hello",
		"PV":   "1.0",
		"P":    "hello-1.0",
		"MY_P": "Hello-v1.0",
	}

	uris := []string{
		"https://example.com/${P}.tar.gz",
		"mirror://gentoo/${MY_P}.xz",
	}

	resolved := resolveURIs(uris, vars)
	if len(resolved) != 2 {
		t.Fatalf("expected 2 URIs, got %d", len(resolved))
	}
	if resolved[0] != "https://example.com/hello-1.0.tar.gz" {
		t.Errorf("URI[0] = %q, want %q", resolved[0], "https://example.com/hello-1.0.tar.gz")
	}
	if resolved[1] != "mirror://gentoo/Hello-v1.0.xz" {
		t.Errorf("URI[1] = %q, want %q", resolved[1], "mirror://gentoo/Hello-v1.0.xz")
	}
}

func TestResolveURIs_Empty(t *testing.T) {
	resolved := resolveURIs(nil, nil)
	if len(resolved) != 0 {
		t.Errorf("expected 0 URIs, got %d", len(resolved))
	}
}

func TestRebuildPackage_MissingEbuild(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	cfg := RebuildConfig{
		RepoDir:      tmp,
		DistfilesDir: filepath.Join(tmp, "distfiles"),
		RootDir:      filepath.Join(tmp, "root"),
		VdbDir:       filepath.Join(tmp, "vdb"),
		WorkDirBase:  filepath.Join(tmp, "work"),
	}

	for _, d := range []string{cfg.DistfilesDir, cfg.RootDir, cfg.VdbDir, cfg.WorkDirBase} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	err := RebuildPackage(ctx, "nonexistent/pkg-1.0", &cfg)
	if err == nil {
		t.Error("expected error for missing ebuild, got nil")
	}
}

func TestRebuildPackage_InvalidAtom(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	cfg := RebuildConfig{
		WorkDirBase: filepath.Join(tmp, "work"),
	}
	if err := os.MkdirAll(cfg.WorkDirBase, 0755); err != nil {
		t.Fatal(err)
	}

	invalidAtoms := []string{
		"",
		"not-an-atom",
		"missing-category/",
		"/missing-package-1.0",
	}

	for _, a := range invalidAtoms {
		t.Run("atom="+a, func(t *testing.T) {
			err := RebuildPackage(ctx, a, &cfg)
			if err == nil {
				t.Errorf("expected error for invalid atom %q, got nil", a)
			}
		})
	}
}

func TestRebuildPackage_NoVersion(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	cfg := RebuildConfig{
		WorkDirBase: filepath.Join(tmp, "work"),
	}
	if err := os.MkdirAll(cfg.WorkDirBase, 0755); err != nil {
		t.Fatal(err)
	}

	err := RebuildPackage(ctx, "sys-apps/hello", &cfg)
	if err == nil {
		t.Error("expected error for atom without version, got nil")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("error should mention version: %v", err)
	}
}

func TestRebuildPackage_ContextCancellation(t *testing.T) {
	tmp := t.TempDir()

	pkgDir := filepath.Join(tmp, "sys-apps", "hello")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "hello-1.0.ebuild"), []byte("EAPI=8"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := RebuildConfig{
		RepoDir:      tmp,
		DistfilesDir: filepath.Join(tmp, "distfiles"),
		RootDir:      filepath.Join(tmp, "root"),
		VdbDir:       filepath.Join(tmp, "vdb"),
		WorkDirBase:  filepath.Join(tmp, "work"),
	}

	for _, d := range []string{cfg.DistfilesDir, cfg.RootDir, cfg.VdbDir, cfg.WorkDirBase} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := RebuildPackage(ctx, "sys-apps/hello-1.0", &cfg)
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
}

func TestRebuildPackages(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	pkgDir := filepath.Join(tmp, "sys-apps", "hello")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"hello-1.0.ebuild", "hello-1.1.ebuild"} {
		if err := os.WriteFile(filepath.Join(pkgDir, f), []byte("EAPI=8"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := RebuildConfig{
		RepoDir:      tmp,
		DistfilesDir: filepath.Join(tmp, "distfiles"),
		RootDir:      filepath.Join(tmp, "root"),
		VdbDir:       filepath.Join(tmp, "vdb"),
		WorkDirBase:  filepath.Join(tmp, "work"),
	}

	for _, d := range []string{cfg.DistfilesDir, cfg.RootDir, cfg.VdbDir, cfg.WorkDirBase} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	atoms := []string{
		"sys-apps/hello-1.0",
		"sys-apps/hello-1.1",
		"sys-apps/nonexistent-1.0",
	}

	err := RebuildPackages(ctx, atoms, &cfg)
	if err == nil {
		t.Error("expected error due to nonexistent package, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention nonexistent: %v", err)
	}
}

func TestRebuildPackages_Empty(t *testing.T) {
	err := RebuildPackages(context.Background(), nil, &RebuildConfig{})
	if err != nil {
		t.Errorf("expected nil for empty atoms, got %v", err)
	}
}

func TestRebuildPackagesParallel_Basic(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	pkgDir := filepath.Join(tmp, "sys-apps", "hello")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"hello-1.0.ebuild", "hello-1.1.ebuild"} {
		if err := os.WriteFile(filepath.Join(pkgDir, f), []byte("EAPI=8"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := RebuildConfig{
		RepoDir:      tmp,
		DistfilesDir: filepath.Join(tmp, "distfiles"),
		RootDir:      filepath.Join(tmp, "root"),
		VdbDir:       filepath.Join(tmp, "vdb"),
		WorkDirBase:  filepath.Join(tmp, "work"),
	}

	for _, d := range []string{cfg.DistfilesDir, cfg.RootDir, cfg.VdbDir, cfg.WorkDirBase} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	runParallelTest := func(t *testing.T, jobs int) {
		atoms := []string{
			"sys-apps/hello-1.0",
			"sys-apps/hello-1.1",
			"sys-apps/nonexistent-1.0",
		}

		err := RebuildPackagesParallel(ctx, atoms, &cfg, jobs)
		if err == nil {
			t.Error("expected error due to nonexistent package, got nil")
		}
		if !strings.Contains(err.Error(), "nonexistent") {
			t.Errorf("error should mention nonexistent: %v", err)
		}
	}

	t.Run("workers=2", func(t *testing.T) { runParallelTest(t, 2) })
	t.Run("workers=4", func(t *testing.T) { runParallelTest(t, 4) })
	t.Run("workers=8", func(t *testing.T) { runParallelTest(t, 8) })
}

func TestRebuildPackagesParallel_Empty(t *testing.T) {
	err := RebuildPackagesParallel(context.Background(), nil, &RebuildConfig{}, 4)
	if err != nil {
		t.Errorf("expected nil for empty atoms, got %v", err)
	}
	err = RebuildPackagesParallel(context.Background(), []string{}, &RebuildConfig{}, 4)
	if err != nil {
		t.Errorf("expected nil for empty atoms, got %v", err)
	}
}

func TestRebuildPackagesParallel_ContextCancellation(t *testing.T) {
	tmp := t.TempDir()

	pkgDir := filepath.Join(tmp, "sys-apps", "hello")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "hello-1.0.ebuild"), []byte("EAPI=8"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := RebuildConfig{
		RepoDir:      tmp,
		DistfilesDir: filepath.Join(tmp, "distfiles"),
		RootDir:      filepath.Join(tmp, "root"),
		VdbDir:       filepath.Join(tmp, "vdb"),
		WorkDirBase:  filepath.Join(tmp, "work"),
	}

	for _, d := range []string{cfg.DistfilesDir, cfg.RootDir, cfg.VdbDir, cfg.WorkDirBase} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	atoms := []string{"sys-apps/hello-1.0", "sys-apps/hello-1.1"}
	err := RebuildPackagesParallel(ctx, atoms, &cfg, 4)
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
}

func TestWaitForLoad(t *testing.T) {
	// maxLoad <= 0 should return immediately
	if err := WaitForLoad(0); err != nil {
		t.Errorf("WaitForLoad(0) should not error: %v", err)
	}
	if err := WaitForLoad(-1); err != nil {
		t.Errorf("WaitForLoad(-1) should not error: %v", err)
	}
}

func TestWaitForLoad_WithHighThreshold(t *testing.T) {
	// With a very high threshold, should pass immediately on a normal system
	err := WaitForLoad(9999.0)
	if err != nil {
		t.Errorf("WaitForLoad(9999) should not error on normal system: %v", err)
	}
}

func TestWithLoadControl(t *testing.T) {
	ctx := WithLoadControl(context.Background(), 0)
	if ctx == nil {
		t.Fatal("expected non-nil context")
	}

	// maxLoad <= 0 should return original context, not LoadControlContext
	lc := LoadControlFromContext(ctx)
	if lc != nil {
		t.Error("expected nil LoadControlContext for maxLoad <= 0")
	}

	ctx2 := WithLoadControl(context.Background(), 2.5)
	lc2 := LoadControlFromContext(ctx2)
	if lc2 == nil {
		t.Fatal("expected non-nil LoadControlContext for maxLoad > 0")
	}
	if lc2.MaxLoad != 2.5 {
		t.Errorf("expected MaxLoad=2.5, got %f", lc2.MaxLoad)
	}
}

func TestLoadControlContext_Wait(t *testing.T) {
	ctx := WithLoadControl(context.Background(), 9999.0)
	lc := LoadControlFromContext(ctx)
	if lc == nil {
		t.Fatal("expected non-nil LoadControlContext")
	}
	if err := lc.Wait(); err != nil {
		t.Errorf("Wait() with high threshold should not error: %v", err)
	}
}

func TestRebuildPackagesParallel_ContinuesOnError(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	repoDir := filepath.Join(tmp, "repo")
	vdbDir := filepath.Join(tmp, "vdb")
	rootDir := filepath.Join(tmp, "root")
	distDir := filepath.Join(tmp, "distfiles")
	workDir := filepath.Join(tmp, "work")

	for _, d := range []string{repoDir, vdbDir, rootDir, distDir, workDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	goodDir := filepath.Join(repoDir, "app-good", "good")
	if err := os.MkdirAll(goodDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goodDir, "good-1.0.ebuild"), []byte("EAPI=8\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var erroredPkgs []string
	var mu sync.Mutex
	cfg := RebuildConfig{
		RepoDir:      repoDir,
		DistfilesDir: distDir,
		RootDir:      rootDir,
		VdbDir:       vdbDir,
		WorkDirBase:  workDir,
		OnError: func(pkg string, err error) {
			mu.Lock()
			erroredPkgs = append(erroredPkgs, pkg)
			mu.Unlock()
		},
	}

	atoms := []string{
		"app-bad/bad-1.0",
		"app-good/good-1.0",
		"app-bad/missing-1.0",
	}

	err := RebuildPackagesParallel(ctx, atoms, &cfg, 4)
	if err == nil {
		t.Error("expected error from RebuildPackagesParallel with failing atoms")
	}

	mu.Lock()
	count := len(erroredPkgs)
	mu.Unlock()
	if count < 2 {
		t.Errorf("expected at least 2 errored packages, got %d: %v", count, erroredPkgs)
	}

	contentsPath := filepath.Join(vdbDir, "app-good", "good-1.0", "CONTENTS")
	if _, err := os.Stat(contentsPath); err != nil {
		t.Errorf("CONTENTS for good package should exist: %v", err)
	}
}

func TestRebuildPackagesParallel_SingleWorker(t *testing.T) {
	tmp := t.TempDir()

	pkgDir := filepath.Join(tmp, "sys-apps", "hello")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "hello-1.0.ebuild"), []byte("EAPI=8"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := RebuildConfig{
		RepoDir:      tmp,
		DistfilesDir: filepath.Join(tmp, "distfiles"),
		RootDir:      filepath.Join(tmp, "root"),
		VdbDir:       filepath.Join(tmp, "vdb"),
		WorkDirBase:  filepath.Join(tmp, "work"),
	}

	for _, d := range []string{cfg.DistfilesDir, cfg.RootDir, cfg.VdbDir, cfg.WorkDirBase} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	atoms := []string{"sys-apps/hello-1.0"}
	err := RebuildPackagesParallel(context.Background(), atoms, &cfg, 1)
	if err != nil {
		t.Errorf("RebuildPackagesParallel with 1 worker failed: %v", err)
	}
}

func TestProgressCallbacks(t *testing.T) {
	tmp := t.TempDir()

	pkgDir := filepath.Join(tmp, "sys-apps", "hello")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "hello-1.0.ebuild"), []byte("EAPI=8"), 0644); err != nil {
		t.Fatal(err)
	}

	var phaseStarts []string
	var phaseEnds []string
	var errors []string

	cfg := RebuildConfig{
		RepoDir:      tmp,
		DistfilesDir: filepath.Join(tmp, "distfiles"),
		RootDir:      filepath.Join(tmp, "root"),
		VdbDir:       filepath.Join(tmp, "vdb"),
		WorkDirBase:  filepath.Join(tmp, "work"),

		OnPhaseStart: func(phase string) {
			phaseStarts = append(phaseStarts, phase)
		},
		OnPhaseEnd: func(phase string, err error) {
			phaseEnds = append(phaseEnds, phase)
		},
		OnError: func(pkg string, err error) {
			errors = append(errors, pkg)
		},
	}

	for _, d := range []string{cfg.DistfilesDir, cfg.RootDir, cfg.VdbDir, cfg.WorkDirBase} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	err := RebuildPackage(context.Background(), "sys-apps/hello-1.0", &cfg)
	if err != nil {
		t.Fatalf("RebuildPackage: %v", err)
	}

	if len(phaseStarts) == 0 {
		t.Error("OnPhaseStart was never called")
	}
	if len(phaseEnds) == 0 {
		t.Error("OnPhaseEnd was never called")
	}

	for i, start := range phaseStarts {
		if i < len(phaseEnds) && phaseEnds[i] != start {
			t.Errorf("phase start/end mismatch: start=%s end=%s", start, phaseEnds[i])
		}
	}
}

func TestRebuildPackage_EndToEnd(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	repoDir := filepath.Join(tmp, "repo")
	distDir := filepath.Join(tmp, "distfiles")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(tmp, "vdb")
	workDirBase := filepath.Join(tmp, "work")

	for _, d := range []string{repoDir, distDir, rootDir, vdbDir, workDirBase} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	ebuildDir := filepath.Join(repoDir, "app-misc", "gmtest")
	if err := os.MkdirAll(ebuildDir, 0755); err != nil {
		t.Fatal(err)
	}

	// No SRC_URI to avoid fetch issues; this tests the orchestration
	ebuildContent := `EAPI=8
DESCRIPTION="GM test package"
`
	if err := os.WriteFile(filepath.Join(ebuildDir, "gmtest-1.0.ebuild"), []byte(ebuildContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := RebuildConfig{
		RepoDir:      repoDir,
		DistfilesDir: distDir,
		RootDir:      rootDir,
		VdbDir:       vdbDir,
		WorkDirBase:  workDirBase,
		CFLAGS:       "-O2 -pipe",
		CXXFLAGS:     "-O2 -pipe",
		MAKEOPTS:     "-j4",
		Arch:         "amd64",

		OnPhaseStart: func(phase string) {},
		OnPhaseEnd:   func(phase string, err error) {},
		OnError:      func(pkg string, err error) {},
	}

	err := RebuildPackage(ctx, "app-misc/gmtest-1.0", &cfg)
	if err != nil {
		t.Fatalf("RebuildPackage failed: %v", err)
	}

	contentsPath := filepath.Join(vdbDir, "app-misc", "gmtest-1.0", "CONTENTS")
	if _, err := os.Stat(contentsPath); err != nil {
		t.Errorf("CONTENTS file not found: %v", err)
	}
}

func TestRebuildPackagePhaseProtocolIntoDisposableRoot(t *testing.T) {
	if _, err := exec.LookPath("sandbox"); err != nil {
		t.Skip("Portage sandbox is not installed")
	}
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	root := filepath.Join(tmp, "root")
	vdb := filepath.Join(root, "var", "db", "pkg")
	work := filepath.Join(tmp, "work")
	dist := filepath.Join(tmp, "distfiles")
	packageDir := filepath.Join(repo, "app-misc", "protocol-test")
	for _, directory := range []string{filepath.Join(repo, "eclass"), packageDir, root, vdb, work, dist} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ebuildContent := `EAPI=8
S="${WORKDIR}/${P}"
src_unpack() { mkdir -p "${S}"; printf 'protocol image\n' > "${S}/payload"; }
src_install() { insinto /usr/share/protocol-test; doins payload; }
pkg_postinst() { printf 'postinst\n' > "${ROOT}/postinst-marker"; }
`
	if err := os.WriteFile(filepath.Join(packageDir, "protocol-test-1.ebuild"), []byte(ebuildContent), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := RebuildConfig{
		RepoDir: repo, DistfilesDir: dist, RootDir: root, VdbDir: vdb, WorkDirBase: work,
		PhaseProtocol: true, Repository: "test", Repositories: []portage.RepoEntry{{Name: "test", Location: repo}},
	}
	if err := RebuildPackage(context.Background(), "app-misc/protocol-test-1", &cfg); err != nil {
		t.Fatalf("protocol rebuild: %v", err)
	}
	for _, path := range []string{
		filepath.Join(root, "usr", "share", "protocol-test", "payload"),
		filepath.Join(root, "postinst-marker"),
		filepath.Join(vdb, "app-misc", "protocol-test-1", "CONTENTS"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected disposable-root result %s: %v", path, err)
		}
	}
}

func TestRebuildPackageUsesVerifiedCachedDISTDIR(t *testing.T) {
	tmp := t.TempDir()
	repoDir := filepath.Join(tmp, "repo")
	distDir := filepath.Join(tmp, "distfiles")
	workDir := filepath.Join(tmp, "work")
	rootDir := filepath.Join(tmp, "root")
	vdbDir := filepath.Join(tmp, "vdb")
	packageDir := filepath.Join(repoDir, "app-misc", "cached")
	for _, directory := range []string{distDir, workDir, rootDir, vdbDir, packageDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	content := archive.Bytes()
	digest := sha512.Sum512(content)
	if err := os.WriteFile(filepath.Join(distDir, "source.tar.gz"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := "DIST source.tar.gz " + fmt.Sprint(len(content)) + " SHA512 " + hex.EncodeToString(digest[:]) + "\n"
	if err := os.WriteFile(filepath.Join(packageDir, "Manifest"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := "EAPI=8\nSRC_URI=\"https://invalid.example/source.tar.gz\"\n"
	if err := os.WriteFile(filepath.Join(packageDir, "cached-1.ebuild"), []byte(ebuild), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := RebuildConfig{RepoDir: repoDir, DistfilesDir: distDir, RootDir: rootDir, VdbDir: vdbDir, WorkDirBase: workDir}
	if err := RebuildPackage(context.Background(), "app-misc/cached-1", &cfg); err != nil {
		t.Fatal(err)
	}
}

func TestRebuildPackageRefusesSourceWithoutManifest(t *testing.T) {
	tmp := t.TempDir()
	packageDir := filepath.Join(tmp, "repo", "app-misc", "missing")
	for _, directory := range []string{packageDir, filepath.Join(tmp, "dist"), filepath.Join(tmp, "work")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(packageDir, "missing-1.ebuild"), []byte("EAPI=8\nSRC_URI=\"https://invalid.example/source.tar\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := RebuildConfig{RepoDir: filepath.Join(tmp, "repo"), DistfilesDir: filepath.Join(tmp, "dist"), RootDir: filepath.Join(tmp, "root"), VdbDir: filepath.Join(tmp, "vdb"), WorkDirBase: filepath.Join(tmp, "work")}
	err := RebuildPackage(context.Background(), "app-misc/missing-1", &cfg)
	if err == nil || !strings.Contains(err.Error(), "open Manifest") {
		t.Fatalf("error = %v", err)
	}
}

func TestRebuildPackages_ContinuesOnError(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	repoDir := filepath.Join(tmp, "repo")
	vdbDir := filepath.Join(tmp, "vdb")
	rootDir := filepath.Join(tmp, "root")
	distDir := filepath.Join(tmp, "distfiles")
	workDir := filepath.Join(tmp, "work")

	for _, d := range []string{repoDir, vdbDir, rootDir, distDir, workDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	// Good package
	goodDir := filepath.Join(repoDir, "app-good", "good")
	if err := os.MkdirAll(goodDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goodDir, "good-1.0.ebuild"), []byte("EAPI=8\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var erroredPkgs []string
	cfg := RebuildConfig{
		RepoDir:      repoDir,
		DistfilesDir: distDir,
		RootDir:      rootDir,
		VdbDir:       vdbDir,
		WorkDirBase:  workDir,
		OnError: func(pkg string, err error) {
			erroredPkgs = append(erroredPkgs, pkg)
		},
	}

	atoms := []string{
		"app-bad/bad-1.0",
		"app-good/good-1.0",
		"app-bad/missing-1.0",
	}

	err := RebuildPackages(ctx, atoms, &cfg)
	if err == nil {
		t.Error("expected error from RebuildPackages with failing atoms")
	}

	if len(erroredPkgs) < 2 {
		t.Errorf("expected at least 2 errored packages, got %d: %v", len(erroredPkgs), erroredPkgs)
	}

	// The good package should have been merged
	contentsPath := filepath.Join(vdbDir, "app-good", "good-1.0", "CONTENTS")
	if _, err := os.Stat(contentsPath); err != nil {
		t.Errorf("CONTENTS for good package should exist: %v", err)
	}
}

func TestRebuildPackage_AdversarialAtom(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	cfg := RebuildConfig{
		WorkDirBase: filepath.Join(tmp, "work"),
	}
	if err := os.MkdirAll(cfg.WorkDirBase, 0755); err != nil {
		t.Fatal(err)
	}

	adversarial := []string{
		strings.Repeat("a", 10000) + "/pkg-1.0",
		"../../etc/passwd-1.0",
		"\x00/pkg-1.0",
		"cat/pkg-99999999999999999999",
	}

	for _, a := range adversarial {
		err := RebuildPackage(ctx, a, &cfg)
		_ = err
	}
}

func createTestTar(t *testing.T, tarPath string, files map[string]string) {
	t.Helper()

	fh, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fh.Close() }()

	gw := gzip.NewWriter(fh)
	defer func() { _ = gw.Close() }()
	tw := tar.NewWriter(gw)
	defer func() { _ = tw.Close() }()

	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0644,
			Size: int64(len(content)),
		}
		if strings.Contains(name, "usr/bin") {
			hdr.Mode = 0755
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
}

func mkdirAll(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
}
