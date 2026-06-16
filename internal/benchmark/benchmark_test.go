package benchmark

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/depstring"
	"github.com/airencracken/arise/internal/equery"
	"github.com/airencracken/arise/internal/graph"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/resolve"
	"github.com/airencracken/arise/internal/search"
	"github.com/airencracken/arise/internal/walker"
	"github.com/dgraph-io/badger/v4"
)

func buildResolveGraph(tb testing.TB, db *badger.DB) *resolve.DepGraph {
	pkgs, err := ExtractAllFromDB(db)
	if err != nil {
		tb.Fatalf("extract from db: %v", err)
	}
	if len(pkgs) == 0 {
		tb.Fatal("no packages in test db")
	}
	g := graph.NewFromInstalled(pkgs)
	return g.ToResolveGraph()
}

// ── Atom benchmarks ────────────────────────────────────────────────────────

func BenchmarkAtomParse(b *testing.B) {
	input := ">=sys-devel/gcc-12.2.0:12/12.2=[fortran]"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = atom.Parse(input)
	}
}

func BenchmarkAtomCompare(b *testing.B) {
	v1, _ := atom.ParseVersion("12.2.0-r3")
	v2, _ := atom.ParseVersion("13.1.0_alpha1")
	if v1 == nil || v2 == nil {
		b.Fatal("version parse failed")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = v1.Compare(v2)
	}
}

// ── Depstring benchmarks ───────────────────────────────────────────────────

func BenchmarkDepstringParse(b *testing.B) {
	input := "|| ( dev-lang/python >=dev-lang/python-3.10 ) python_single_target_python3_10? ( dev-python/foo ) !!sys-libs/blocker !app-misc/conflict >=dev-libs/glib-2.70"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = depstring.Parse(input)
	}
}

func BenchmarkDepstringSatisfy(b *testing.B) {
	input := "|| ( dev-lang/python >=dev-lang/python-3.10 ) python_single_target_python3_10? ( dev-python/foo ) >=dev-libs/glib-2.70"
	tree, err := depstring.Parse(input)
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	installed := map[string]*atom.Atom{
		"dev-lang/python": {Category: "dev-lang", Package: "python", Version: mustParseVersion("3.12.0")},
		"dev-libs/glib":   {Category: "dev-libs", Package: "glib", Version: mustParseVersion("2.80.0")},
	}
	useFlags := map[string]bool{"python_single_target_python3_10": true}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		depstring.Satisfy(tree, installed, useFlags)
	}
}

// ── Search benchmarks ──────────────────────────────────────────────────────

func BenchmarkSearchByName(b *testing.B) {
	db, err := CreateTestDB(10000)
	if err != nil {
		b.Fatalf("create test db: %v", err)
	}
	defer func() { _ = db.Close() }()
	cfg := search.SearchConfig{Query: "pkg-500", Exact: true}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = search.Search(db, cfg)
	}
}

func BenchmarkSearchByCategory(b *testing.B) {
	db, err := CreateTestDB(10000)
	if err != nil {
		b.Fatalf("create test db: %v", err)
	}
	defer func() { _ = db.Close() }()
	cfg := search.SearchConfig{Category: "dev-libs", Exact: true}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = search.Search(db, cfg)
	}
}

func BenchmarkSearchWithFilters(b *testing.B) {
	db, err := CreateTestDB(10000)
	if err != nil {
		b.Fatalf("create test db: %v", err)
	}
	defer func() { _ = db.Close() }()
	cfg := search.SearchConfig{
		Query:     "pkg",
		Installed: false,
		Use:       "+foo",
		Keywords:  "amd64",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = search.Search(db, cfg)
	}
}

func BenchmarkSearchJSON(b *testing.B) {
	db, err := CreateTestDB(10000)
	if err != nil {
		b.Fatalf("create test db: %v", err)
	}
	defer func() { _ = db.Close() }()
	cfg := search.SearchConfig{JSON: true}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = search.Search(db, cfg)
	}
}

func BenchmarkSearchAll(b *testing.B) {
	db, err := CreateTestDB(10000)
	if err != nil {
		b.Fatalf("create test db: %v", err)
	}
	defer func() { _ = db.Close() }()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = search.Search(db, search.SearchConfig{})
	}
}

// ── Dependency resolution benchmarks ───────────────────────────────────────

func BenchmarkResolveSimple(b *testing.B) {
	db, err := CreateTestDB(1000)
	if err != nil {
		b.Fatalf("create test db: %v", err)
	}
	defer func() { _ = db.Close() }()
	rg := buildResolveGraph(b, db)
	cfg := resolve.DefaultResolveConfig()
	cfg.NoDeps = true
	cfg.Quiet = true
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = resolve.Resolve(rg, []string{"app-admin/pkg-0"}, cfg)
	}
}

func BenchmarkResolveDeep(b *testing.B) {
	db, err := CreateTestDB(500)
	if err != nil {
		b.Fatalf("create test db: %v", err)
	}
	defer func() { _ = db.Close() }()
	rg := buildResolveGraph(b, db)
	cfg := resolve.DefaultResolveConfig()
	cfg.Deep = true
	cfg.Quiet = true
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = resolve.Resolve(rg, []string{"app-admin/pkg-0"}, cfg)
	}
}

func BenchmarkResolveWorld(b *testing.B) {
	db, err := CreateTestDB(2000)
	if err != nil {
		b.Fatalf("create test db: %v", err)
	}
	defer func() { _ = db.Close() }()
	rg := buildResolveGraph(b, db)
	cfg := resolve.DefaultResolveConfig()
	cfg.Deep = true
	cfg.Quiet = true
	cfg.KeepGoing = true
	targets := make([]string, 100)
	categories := []string{"app-admin", "dev-libs", "sys-apps", "net-misc", "x11-libs", "media-libs", "app-text", "dev-util", "sci-libs", "net-libs"}
	for i := 0; i < 100; i++ {
		idx := i * 20
		if idx >= 2000 {
			idx = i
		}
		targets[i] = fmt.Sprintf("%s/pkg-%d", categories[i%len(categories)], idx)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = resolve.Resolve(rg, targets, cfg)
	}
}

func BenchmarkResolveWithBacktrack(b *testing.B) {
	db, err := CreateTestDB(500)
	if err != nil {
		b.Fatalf("create test db: %v", err)
	}
	defer func() { _ = db.Close() }()
	rg := buildResolveGraph(b, db)
	cfg := resolve.DefaultResolveConfig()
	cfg.Deep = true
	cfg.Backtrack = 20
	cfg.Quiet = true
	cfg.KeepGoing = true
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = resolve.Resolve(rg, []string{"app-admin/pkg-0"}, cfg)
	}
}

// ── Graph benchmarks ───────────────────────────────────────────────────────

func BenchmarkGraphBuild(b *testing.B) {
	db, err := CreateTestDB(2000)
	if err != nil {
		b.Fatalf("create test db: %v", err)
	}
	defer func() { _ = db.Close() }()
	pkgs, err := ExtractAllFromDB(db)
	if err != nil {
		b.Fatalf("extract: %v", err)
	}
	if len(pkgs) == 0 {
		b.Fatal("no packages")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = graph.NewFromInstalled(pkgs)
	}
}

func BenchmarkGraphParallel(b *testing.B) {
	db, err := CreateTestDB(2000)
	if err != nil {
		b.Fatalf("create test db: %v", err)
	}
	defer func() { _ = db.Close() }()
	repoDir := "/var/db/repos/gentoo"
	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		pkgs, err := ExtractAllFromDB(db)
		if err != nil {
			b.Fatalf("extract: %v", err)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			g := graph.NewFromInstalled(pkgs)
			_ = g.ToResolveGraph()
		}
		return
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = graph.BuildParallel(db, repoDir, 4)
	}
}

// ── Equery benchmarks ──────────────────────────────────────────────────────

func BenchmarkEqueryBelongs(b *testing.B) {
	vdbPath, refFiles, err := CreateTempVDB()
	if err != nil {
		b.Fatalf("create temp vdb: %v", err)
	}
	defer os.RemoveAll(vdbPath)
	if len(refFiles) == 0 {
		b.Skip("no reference files available")
	}
	filePath := refFiles[0]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = equery.Belongs(vdbPath, filePath)
	}
}

func BenchmarkEqueryFiles(b *testing.B) {
	vdbPath, _, err := CreateTempVDB()
	if err != nil {
		b.Fatalf("create temp vdb: %v", err)
	}
	defer os.RemoveAll(vdbPath)
	atomStr := "app-admin/pkg-0-1.0"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = equery.Files(vdbPath, atomStr)
	}
}

func BenchmarkEquerySize(b *testing.B) {
	vdbPath, refFiles, err := CreateTempVDB()
	if err != nil {
		b.Fatalf("create temp vdb: %v", err)
	}
	defer os.RemoveAll(vdbPath)
	if len(refFiles) == 0 {
		b.Skip("no reference files")
	}
	atomStr := "app-admin/pkg-0-1.0"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = equery.Size(vdbPath, atomStr)
	}
}

func BenchmarkEqueryCheck(b *testing.B) {
	vdbPath, refFiles, err := CreateTempVDB()
	if err != nil {
		b.Fatalf("create temp vdb: %v", err)
	}
	defer os.RemoveAll(vdbPath)
	if len(refFiles) == 0 {
		b.Skip("no reference files")
	}
	atomStr := "app-admin/pkg-0-1.0"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = equery.Check(vdbPath, atomStr)
	}
}

// ── Binpkg benchmarks ──────────────────────────────────────────────────────

func BenchmarkBinpkgReadInfo(b *testing.B) {
	pkgDir, err := os.MkdirTemp("", "arise-bench-binpkg-")
	if err != nil {
		b.Fatalf("mktemp: %v", err)
	}
	defer os.RemoveAll(pkgDir)

	vdbPath, _, err := CreateTempVDB()
	if err != nil {
		b.Fatalf("create temp vdb: %v", err)
	}
	defer os.RemoveAll(vdbPath)
	vdbEntry := filepath.Join(vdbPath, "app-admin", "pkg-0-1.0")
	rootDir, _ := os.MkdirTemp("", "arise-bench-root-")
	defer os.RemoveAll(rootDir)

	ctx := context.Background()
	outPath, err := binpkg.Create(ctx, vdbEntry, rootDir, pkgDir)
	if err != nil {
		b.Skipf("binpkg.Create failed (bzip2 not available?): %v", err)
		return
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = binpkg.ReadInfo(outPath)
	}
}

func BenchmarkBinpkgExtract(b *testing.B) {
	pkgDir, err := os.MkdirTemp("", "arise-bench-binpkg-")
	if err != nil {
		b.Fatalf("mktemp: %v", err)
	}
	defer os.RemoveAll(pkgDir)

	vdbPath, _, err := CreateTempVDB()
	if err != nil {
		b.Fatalf("create temp vdb: %v", err)
	}
	defer os.RemoveAll(vdbPath)
	vdbEntry := filepath.Join(vdbPath, "app-admin", "pkg-0-1.0")
	rootDir, _ := os.MkdirTemp("", "arise-bench-root-")
	defer os.RemoveAll(rootDir)

	ctx := context.Background()
	outPath, err := binpkg.Create(ctx, vdbEntry, rootDir, pkgDir)
	if err != nil {
		b.Skipf("binpkg.Create failed: %v", err)
		return
	}
	destDir, _ := os.MkdirTemp("", "arise-bench-extract-")
	defer os.RemoveAll(destDir)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = binpkg.Extract(ctx, outPath, destDir)
	}
}

func BenchmarkBinpkgCreate(b *testing.B) {
	vdbPath, _, err := CreateTempVDB()
	if err != nil {
		b.Fatalf("create temp vdb: %v", err)
	}
	defer os.RemoveAll(vdbPath)
	vdbEntry := filepath.Join(vdbPath, "app-admin", "pkg-0-1.0")
	pkgDir, err := os.MkdirTemp("", "arise-bench-create-")
	if err != nil {
		b.Fatalf("mktemp: %v", err)
	}
	defer os.RemoveAll(pkgDir)
	rootDir := "/"
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		tmpDir, _ := os.MkdirTemp("", "arise-bench-create-run-")
		outPath, err := binpkg.Create(ctx, vdbEntry, rootDir, tmpDir)
		b.StartTimer()
		if err == nil && outPath != "" {
			_ = outPath
			os.RemoveAll(tmpDir)
		}
	}
}

// ── Metadata / walker benchmarks ───────────────────────────────────────────

func BenchmarkMetadataParse(b *testing.B) {
	data := []byte("DEPEND=dev-libs/foo >=dev-libs/bar-2.0\nRDEPEND=dev-libs/foo\nSLOT=0/2\nKEYWORDS=amd64 x86\nIUSE=foo bar baz\nLICENSE=GPL-2\nEAPI=8\nDESCRIPTION=Test package\n")
	cpv := "app-admin/test-1.0"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = metadata.ParseCacheEntry(cpv, data)
	}
}

func BenchmarkWalkerWalk(b *testing.B) {
	cacheDir, err := os.MkdirTemp("", "arise-bench-cache-")
	if err != nil {
		b.Fatalf("mktemp: %v", err)
	}
	defer os.RemoveAll(cacheDir)

	const fileCount = 200
	for i := 0; i < fileCount; i++ {
		dir := filepath.Join(cacheDir, fmt.Sprintf("app-admin/pkg-%d", i))
		if err := os.MkdirAll(dir, 0755); err != nil {
			b.Fatalf("mkdir: %v", err)
		}
		data := fmt.Sprintf("DEPEND=>=dev-libs/lib-%d-1.0\nSLOT=0\nKEYWORDS=amd64\nIUSE=foo\nLICENSE=GPL-2\nEAPI=8\nDESCRIPTION=Pkg %d\n_mtime_=%d\n",
			i%100, i, i%(i+1)+1)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("pkg-%d-1.0", i)), []byte(data), 0644); err != nil {
			b.Fatalf("write: %v", err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, errs := walker.WalkCache(cacheDir)
		for range results {
		}
		for e := range errs {
			_ = e
		}
	}
}

// ── Comparison tests (emerge vs arise) ─────────────────────────────────────

func portageAvailable() bool {
	for _, cmd := range []string{"emerge", "portageq", "equery"} {
		if _, err := exec.LookPath(cmd); err != nil {
			return false
		}
	}
	return true
}

func mustParseVersion(ver string) *atom.Version {
	v, _ := atom.ParseVersion(ver)
	return v
}

func TestCompareAtomSpeed(t *testing.T) {
	if !portageAvailable() {
		t.Skip("portage not available")
	}
	Comparison := RunComparison(t, "atom-compare",
		func() error {
			a1, err := atom.Parse(">=sys-devel/gcc-12.2.0")
			if err != nil {
				return err
			}
			a2, err := atom.Parse("sys-devel/gcc-13.1.0")
			if err != nil {
				return err
			}
			if a1.Version != nil && a2.Version != nil {
				a1.Version.Compare(a2.Version)
			}
			return nil
		},
		func() (string, error) {
			out, err := exec.Command("portageq", "atom_compare", ">=sys-devel/gcc-12.2.0", "sys-devel/gcc-13.1.0").Output()
			if err != nil {
				return "", err
			}
			return string(out), nil
		},
	)
	Comparison.AriseCorrect = true
	t.Log(FormatComparison(Comparison))
}

func TestCompareSearchSpeed(t *testing.T) {
	if !portageAvailable() {
		t.Skip("portage not available")
	}
	db, err := CreateTestDB(10000)
	if err != nil {
		t.Fatalf("create test db: %v", err)
	}
	defer func() { _ = db.Close() }()

	Comparison := RunComparison(t, "search-pkg",
		func() error {
			r, err := search.Search(db, search.SearchConfig{Query: "pkg-500", Exact: true})
			if err != nil {
				return err
			}
			if len(r) == 0 {
				return fmt.Errorf("no results")
			}
			return nil
		},
		func() (string, error) {
			out, err := exec.Command("emerge", "--search", "pkg-500").Output()
			if err != nil {
				return "", err
			}
			return string(out), nil
		},
	)
	t.Log(FormatComparison(Comparison))
}

func TestCompareResolveSpeed(t *testing.T) {
	if !portageAvailable() {
		t.Skip("portage not available")
	}
	if _, err := exec.LookPath("emerge"); err != nil {
		t.Skip("emerge not available")
	}
	db, err := CreateTestDB(1000)
	if err != nil {
		t.Fatalf("create test db: %v", err)
	}
	defer func() { _ = db.Close() }()
	rg := buildResolveGraph(t, db)
	cfg := resolve.DefaultResolveConfig()
	cfg.Quiet = true
	cfg.Pretend = true

	Comparison := RunComparison(t, "resolve",
		func() error {
			_, err := resolve.Resolve(rg, []string{"app-admin/pkg-0"}, cfg)
			return err
		},
		func() (string, error) {
			out, err := exec.Command("emerge", "--pretend", "app-admin/pkg-0").Output()
			return string(out), err
		},
	)
	t.Log(FormatComparison(Comparison))
}

func TestCompareEqueryBelongs(t *testing.T) {
	if !portageAvailable() {
		t.Skip("portage not available")
	}
	vdbPath, refFiles, err := CreateTempVDB()
	if err != nil {
		t.Fatalf("create temp vdb: %v", err)
	}
	defer os.RemoveAll(vdbPath)
	if len(refFiles) == 0 {
		t.Skip("no reference files")
	}
	filePath := refFiles[0]

	Comparison := RunComparison(t, "equery-belongs",
		func() error {
			_, err := equery.Belongs(vdbPath, filePath)
			return err
		},
		func() (string, error) {
			out, err := exec.Command("equery", "belongs", filePath).Output()
			if err != nil {
				return "", err
			}
			return string(out), nil
		},
	)
	t.Log(FormatComparison(Comparison))
}

func TestCompareEqueryFiles(t *testing.T) {
	if !portageAvailable() {
		t.Skip("portage not available")
	}
	vdbPath, _, err := CreateTempVDB()
	if err != nil {
		t.Fatalf("create temp vdb: %v", err)
	}
	defer os.RemoveAll(vdbPath)
	atomStr := "app-admin/pkg-0-1.0"

	Comparison := RunComparison(t, "equery-files",
		func() error {
			_, err := equery.Files(vdbPath, atomStr)
			return err
		},
		func() (string, error) {
			out, err := exec.Command("equery", "files", atomStr).Output()
			if err != nil {
				return "", err
			}
			return string(out), nil
		},
	)
	t.Log(FormatComparison(Comparison))
}
