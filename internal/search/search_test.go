package search

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/world"
	"github.com/dgraph-io/badger/v4"
)

func openTestDB(t *testing.T) *badger.DB {
	t.Helper()
	opts := badger.DefaultOptions("").WithInMemory(true).WithLogger(nil)
	db, err := badger.Open(opts)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func ingestTestData(t *testing.T, db *badger.DB, entries []*metadata.PackageMetadata) {
	t.Helper()
	ch := make(chan *metadata.PackageMetadata, len(entries))
	for _, e := range entries {
		ch <- e
	}
	close(ch)
	if _, err := ingest.Ingest(db, ch); err != nil {
		t.Fatalf("ingest test data: %v", err)
	}
}

func seedTestDB(t *testing.T) *badger.DB {
	db := openTestDB(t)
	entries := []*metadata.PackageMetadata{
		{Category: "sys-devel", Package: "gcc", Version: "13.2.0", DESCRIPTION: "The GNU Compiler Collection", HOMEPAGE: "https://gcc.gnu.org", SLOT: "13", Subslot: "13.2", IUSE: "fortran nls openmp", KEYWORDS: "amd64 ~arm64", LICENSE: "GPL-3", DEPEND: "virtual/libc", RDEPEND: ""},
		{Category: "dev-lang", Package: "python", Version: "3.12.0", DESCRIPTION: "An interpreted, interactive, object-oriented programming language", HOMEPAGE: "https://python.org", SLOT: "3.12", IUSE: "ssl sqlite", KEYWORDS: "~amd64", LICENSE: "PSF-2", DEPEND: "dev-lang/perl", RDEPEND: ""},
		{Category: "dev-lang", Package: "rust", Version: "1.75.0", DESCRIPTION: "Systems programming language from Mozilla", HOMEPAGE: "https://rust-lang.org", SLOT: "stable", IUSE: "clippy rustfmt", KEYWORDS: "amd64 arm64", LICENSE: "MIT Apache-2.0", DEPEND: "sys-devel/gcc", RDEPEND: ""},
		{Category: "app-editors", Package: "vim", Version: "9.0.2100", DESCRIPTION: "Improved vi-style text editor", HOMEPAGE: "https://vim.org", SLOT: "0", IUSE: "X gtk perl python", KEYWORDS: "amd64", LICENSE: "vim", DEPEND: "sys-devel/gcc dev-lang/python", RDEPEND: ""},
	}
	ingestTestData(t, db, entries)
	return db
}

func seedTestDBExtended(t *testing.T) *badger.DB {
	db := openTestDB(t)
	entries := []*metadata.PackageMetadata{
		{Category: "sys-devel", Package: "gcc", Version: "13.2.0", DESCRIPTION: "The GNU Compiler Collection", HOMEPAGE: "https://gcc.gnu.org", SLOT: "13", Subslot: "13.2", IUSE: "fortran nls openmp", KEYWORDS: "amd64 ~arm64", LICENSE: "GPL-3", DEPEND: "virtual/libc", RDEPEND: ""},
		{Category: "dev-lang", Package: "python", Version: "3.12.0", DESCRIPTION: "An interpreted, interactive, object-oriented programming language", HOMEPAGE: "https://python.org", SLOT: "3.12", IUSE: "ssl sqlite", KEYWORDS: "~amd64", LICENSE: "PSF-2", DEPEND: "dev-lang/perl", RDEPEND: ""},
		{Category: "dev-lang", Package: "rust", Version: "1.75.0", DESCRIPTION: "Systems programming language from Mozilla", HOMEPAGE: "https://rust-lang.org", SLOT: "stable", IUSE: "clippy rustfmt", KEYWORDS: "amd64 arm64", LICENSE: "MIT Apache-2.0", DEPEND: "sys-devel/gcc", RDEPEND: ""},
		{Category: "app-editors", Package: "vim", Version: "9.0.2100", DESCRIPTION: "Improved vi-style text editor", HOMEPAGE: "https://vim.org", SLOT: "0", IUSE: "X gtk perl python", KEYWORDS: "amd64", LICENSE: "vim", DEPEND: "sys-devel/gcc dev-lang/python", RDEPEND: ""},
		{Category: "app-misc", Package: "foo", Version: "1.0.0", DESCRIPTION: "A masked package", HOMEPAGE: "", SLOT: "0", IUSE: "", KEYWORDS: "", LICENSE: "GPL-2", DEPEND: "", RDEPEND: ""},
		{Category: "app-misc", Package: "bar", Version: "2.0.0", DESCRIPTION: "An overflow-only package", HOMEPAGE: "", SLOT: "0", IUSE: "test", KEYWORDS: "~amd64 ~x86", LICENSE: "MIT", DEPEND: "", RDEPEND: ""},
		{Category: "dev-util", Package: "cmake", Version: "3.28.0", DESCRIPTION: "Cross-platform build system", HOMEPAGE: "https://cmake.org", SLOT: "0", IUSE: "doc ncurses", KEYWORDS: "amd64 arm64 ~x86", LICENSE: "CMake", DEPEND: "", RDEPEND: "sys-devel/gcc"},
	}
	ingestTestData(t, db, entries)
	return db
}

func createTestRepo(t *testing.T, pkgs []struct {
	cat, pkg string
	versions []string
}) string {
	t.Helper()
	repoPath := t.TempDir()
	for _, p := range pkgs {
		pkgDir := filepath.Join(repoPath, p.cat, p.pkg)
		os.MkdirAll(pkgDir, 0755)
		for _, v := range p.versions {
			ebuildPath := filepath.Join(pkgDir, p.pkg+"-"+v+".ebuild")
			if err := os.WriteFile(ebuildPath, []byte("EAPI=8\n"), 0644); err != nil {
				t.Fatalf("write ebuild: %v", err)
			}
		}
	}
	return repoPath
}

func TestSearch_PackageName(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Query: "gcc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
	if results[0].Package != "gcc" {
		t.Errorf("expected gcc, got %s", results[0].Package)
	}
}

func TestSearch_Category(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Query: "dev-lang"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results (python + rust), got %d", len(results))
	}
}

func TestSearch_FullCategoryPackage(t *testing.T) {
	db := seedTestDB(t)
	for _, cfg := range []SearchConfig{
		{Query: "dev-lang/python"},
		{Query: "DEV-LANG/PYTHON"},
		{Query: "dev-lang/python", Exact: true},
		{Query: "lang/pyth"},
	} {
		results, err := Search(db, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0].Category != "dev-lang" || results[0].Package != "python" {
			t.Fatalf("Search(%#v) = %#v, want dev-lang/python", cfg, results)
		}
	}
}

func TestSearch_PackageGlob(t *testing.T) {
	db := seedTestDB(t)
	tests := []struct {
		query string
		want  []string
	}{
		{query: "app-editors/*", want: []string{"app-editors/vim"}},
		{query: "dev-lang/*", want: []string{"dev-lang/python", "dev-lang/rust"}},
		{query: "*/vim", want: []string{"app-editors/vim"}},
		{query: "pyth?n", want: []string{"dev-lang/python"}},
		{query: "DEV-LANG/R*", want: []string{"dev-lang/rust"}},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			results, err := Search(db, SearchConfig{Query: tt.query})
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(results))
			for _, result := range results {
				got = append(got, result.Category+"/"+result.Package)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Search(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestSearch_InvalidPackageGlob(t *testing.T) {
	db := seedTestDB(t)
	if _, err := Search(db, SearchConfig{Query: "app-editors/["}); err == nil {
		t.Fatal("invalid package glob was accepted")
	}
}

func TestSearch_CategoryFilter(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Category: "app-editors"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 1 {
		t.Fatalf("expected at least 1 result, got %d", len(results))
	}
	if results[0].Package != "vim" {
		t.Errorf("expected vim, got %s", results[0].Package)
	}
}

func TestSearch_NameFilter(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Name: "python"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
	if results[0].Package != "python" {
		t.Errorf("expected python, got %s", results[0].Package)
	}
}

func TestSearch_Description(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Query: "interactive", Description: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'interactive', got %d", len(results))
	}
}

func TestSearch_Installed(t *testing.T) {
	db := seedTestDB(t)

	vdbPath := t.TempDir()
	os.MkdirAll(filepath.Join(vdbPath, "sys-devel", "gcc"), 0755)
	os.MkdirAll(filepath.Join(vdbPath, "dev-lang", "python"), 0755)

	results, err := Search(db, SearchConfig{Installed: true, VDBPath: vdbPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 installed results, got %d: %+v", len(results), results)
	}
}

func TestSearch_Exact(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Query: "vim", Exact: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for exact 'vim', got %d", len(results))
	}
}

func TestSearch_CaseInsensitive(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Query: "GCC"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'GCC', got %d", len(results))
	}
}

func TestSearch_NoResults(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Query: "zzz_nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Query: ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 4 {
		t.Errorf("expected 4 results, got %d", len(results))
	}
}

func TestSearch_Limit(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Query: "", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestSearch_SortOrder(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Query: ""})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(results); i++ {
		prev := results[i-1]
		curr := results[i]
		if prev.Category > curr.Category {
			t.Errorf("sort error at %d: %s > %s", i, prev.Category, curr.Category)
		} else if prev.Category == curr.Category && prev.Package > curr.Package {
			t.Errorf("sort error at %d package: %s > %s", i, prev.Package, curr.Package)
		}
	}
}

func TestSearch_SortByVersion(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Sort: SortByVersion})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatal("not enough results")
	}
	first := results[0].Version
	if first == "" {
		t.Error("first result has empty version")
	}
}

func TestSearch_SortBySlot(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Sort: SortBySlot})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatal("not enough results")
	}
}

func TestSearch_UseFilter(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Use: "fortran"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result with fortran USE, got %d", len(results))
	}
	if results[0].Package != "gcc" {
		t.Errorf("expected gcc, got %s", results[0].Package)
	}
}

func TestSearch_UseFilterNegative(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Use: "-fortran"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if stringsHasIuseFlag(r.IUSE, "fortran") {
			t.Errorf("%s has fortran but filter was -fortran", r.Package)
		}
	}
}

func TestSearch_RegexFilter(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Name: "^py", Regex: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for regex '^py', got %d", len(results))
	}
}

func TestSearch_KeywordsFilter(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Keywords: "~arm64"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result with ~arm64, got %d", len(results))
	}
}

func TestSearch_StableFilter(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Stable: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if !r.Stable {
			t.Errorf("%s should be stable", r.Package)
		}
	}
}

func TestSearch_TestingFilter(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Testing: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if !r.Testing {
			t.Errorf("%s should have testing keywords", r.Package)
		}
	}
}

func TestSearch_Versions(t *testing.T) {
	db := seedTestDB(t)
	repoPath := createTestRepo(t, []struct {
		cat, pkg string
		versions []string
	}{
		{cat: "app-editors", pkg: "vim", versions: []string{"9.0.2100", "9.0.2000", "8.2.3456"}},
	})

	results, err := Search(db, SearchConfig{
		Query:    "vim",
		Versions: true,
		RepoPath: repoPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if len(r.AllVersions) != 3 {
		t.Errorf("expected 3 versions, got %d: %v", len(r.AllVersions), r.AllVersions)
	}
}

func TestSearch_VersionsNoRepo(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{
		Query:    "vim",
		Versions: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestSearch_Duplicates(t *testing.T) {
	db := seedTestDB(t)
	repoPath := createTestRepo(t, []struct {
		cat, pkg string
		versions []string
	}{
		{cat: "sys-devel", pkg: "gcc", versions: []string{"13.2.0", "13.1.0", "12.3.0"}},
	})

	results, err := Search(db, SearchConfig{
		Query:      "gcc",
		Duplicates: true,
		RepoPath:   repoPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 duplicates, got %d", len(results))
	}
	seen := make(map[string]bool)
	for _, r := range results {
		key := r.Category + "/" + r.Package + "-" + r.Version
		if seen[key] {
			t.Errorf("duplicate result: %s", key)
		}
		seen[key] = true
	}
}

func TestSearch_Format(t *testing.T) {
	r := SearchResult{
		Category:    "dev-lang",
		Package:     "python",
		Version:     "3.12.0",
		Slot:        "3.12",
		Description: "An interpreted language",
		Homepage:    "https://python.org",
		License:     "PSF-2",
		Keywords:    "amd64",
		Installed:   true,
		IsMasked:    false,
	}

	result := FormatResult(r, "<category>/<name>-<version>:<slot> <description>")
	expected := "dev-lang/python-3.12.0:3.12 An interpreted language"
	if result != expected {
		t.Errorf("format mismatch:\ngot:  %q\nwant: %q", result, expected)
	}
}

func TestSearch_FormatInstalled(t *testing.T) {
	r := SearchResult{
		Category:  "app-editors",
		Package:   "vim",
		Version:   "9.0.2100",
		Installed: true,
	}

	result := FormatResult(r, "[<installed>] <category>/<name>")
	if result != "[I] app-editors/vim" {
		t.Errorf("format installed mismatch: got %q", result)
	}

	r2 := SearchResult{Category: "app-editors", Package: "vim", Version: "9.0.2100", Installed: false}
	result2 := FormatResult(r2, "[<installed>] <category>/<name>")
	if result2 != "[] app-editors/vim" {
		t.Errorf("format uninstalled mismatch: got %q", result2)
	}
}

func TestSearch_FormatMasked(t *testing.T) {
	r := SearchResult{
		Category: "app-misc",
		Package:  "foo",
		Version:  "1.0.0",
		IsMasked: true,
	}

	result := FormatResult(r, "<category>/<name>-<version> [<masked>]")
	if !strings.Contains(result, "[M]") {
		t.Errorf("expected masked marker, got %q", result)
	}
}

func TestSearch_FormatRevision(t *testing.T) {
	r := SearchResult{Version: "1.2.3-r5"}
	result := FormatResult(r, "rev=<revision>")
	if result != "rev=5" {
		t.Errorf("expected rev=5, got %q", result)
	}
}

func TestSearch_Print(t *testing.T) {
	r := SearchResult{
		Category: "dev-lang",
		Package:  "python",
		Version:  "3.12.0",
		Slot:     "3.12",
	}

	result := PrintResult(r, []string{"category", "name", "version"})
	expected := "dev-lang python 3.12.0"
	if result != expected {
		t.Errorf("print mismatch:\ngot:  %q\nwant: %q", result, expected)
	}
}

func TestSearch_JSONOutput(t *testing.T) {
	results := []SearchResult{
		{Category: "dev-lang", Package: "python", Version: "3.12.0"},
	}

	out, err := JSONOutput(results)
	if err != nil {
		t.Fatal(err)
	}

	var parsed []SearchResult
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Errorf("invalid JSON: %v\n%s", err, out)
	}
	if len(parsed) != 1 {
		t.Errorf("expected 1 result in JSON, got %d", len(parsed))
	}
	if parsed[0].Package != "python" {
		t.Errorf("expected python in JSON, got %s", parsed[0].Package)
	}
}

func TestSearch_JSONOutputNil(t *testing.T) {
	out, err := JSONOutput(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[") {
		t.Errorf("expected JSON array, got %q", out)
	}
}

func TestSearch_Brief(t *testing.T) {
	r := SearchResult{
		Category:  "dev-lang",
		Package:   "python",
		Version:   "3.12.0",
		Slot:      "3.12",
		Installed: true,
	}

	result := BriefResult(r)
	expected := "[I] dev-lang/python-3.12.0:3.12"
	if result != expected {
		t.Errorf("brief mismatch:\ngot:  %q\nwant: %q", result, expected)
	}
}

func TestSearch_BriefNoSlot(t *testing.T) {
	r := SearchResult{
		Category: "dev-lang",
		Package:  "python",
		Version:  "3.12.0",
		Slot:     "",
	}

	result := BriefResult(r)
	expected := "[] dev-lang/python-3.12.0:0"
	if result != expected {
		t.Errorf("brief no-slot mismatch: got %q", result)
	}
}

func TestSearch_And(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{
		Query: "systems programming",
		And:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	foundRust := false
	for _, r := range results {
		if r.Package == "rust" {
			foundRust = true
		}
	}
	if !foundRust {
		t.Error("expected rust to match 'systems programming' with --and")
	}
}

func TestSearch_Not(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{
		Query: "",
		Not:   "rust",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Package == "rust" {
			t.Errorf("rust should have been excluded by --not")
		}
	}
}

func TestSearch_World(t *testing.T) {
	db := seedTestDB(t)

	worldPath := filepath.Join(t.TempDir(), "world")
	os.WriteFile(worldPath, []byte("sys-devel/gcc\ndev-lang/python\n"), 0644)

	// Override the world path by setting up a test with installed packages only
	vdbPath := t.TempDir()
	os.MkdirAll(filepath.Join(vdbPath, "sys-devel", "gcc"), 0755)
	os.MkdirAll(filepath.Join(vdbPath, "dev-lang", "python"), 0755)

	results, err := Search(db, SearchConfig{Installed: true, VDBPath: vdbPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 installed, got %d", len(results))
	}

	cpFound := make(map[string]bool)
	for _, r := range results {
		cpFound[r.Category+"/"+r.Package] = true
	}
	if !cpFound["sys-devel/gcc"] || !cpFound["dev-lang/python"] {
		t.Errorf("missing expected packages: %v", cpFound)
	}
}

func TestSearch_System(t *testing.T) {
	db := seedTestDB(t)

	systemPath := filepath.Join(t.TempDir(), "packages")
	os.WriteFile(systemPath, []byte("sys-devel/gcc\n"), 0644)

	vdbPath := t.TempDir()
	os.MkdirAll(filepath.Join(vdbPath, "sys-devel", "gcc"), 0755)
	os.MkdirAll(filepath.Join(vdbPath, "dev-lang", "python"), 0755)

	results, err := Search(db, SearchConfig{Installed: true, VDBPath: vdbPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 installed, got %d", len(results))
	}

	_ = systemPath
}

func TestSearch_DependsOn(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{
		DependsOn: "virtual/libc",
	})
	if err != nil {
		t.Fatal(err)
	}
	foundGcc := false
	for _, r := range results {
		if r.Package == "gcc" {
			foundGcc = true
		}
	}
	if !foundGcc {
		t.Errorf("expected gcc to depend on virtual/libc, got %d results", len(results))
	}
}

func TestSearch_RequiredBy(t *testing.T) {
	db := openTestDB(t)
	entries := []*metadata.PackageMetadata{
		{Category: "sys-devel", Package: "gcc", Version: "13.2.0", DESCRIPTION: "GCC", SLOT: "13", IUSE: "", KEYWORDS: "amd64", LICENSE: "GPL-3", DEPEND: "virtual/libc", RDEPEND: ""},
		{Category: "virtual", Package: "libc", Version: "1", DESCRIPTION: "Virtual libc", SLOT: "0", IUSE: "", KEYWORDS: "amd64", LICENSE: "GPL-2", DEPEND: "", RDEPEND: ""},
	}
	ingestTestData(t, db, entries)

	results, err := Search(db, SearchConfig{
		RequiredBy: "sys-devel/gcc",
	})
	if err != nil {
		t.Fatal(err)
	}
	foundLibc := false
	for _, r := range results {
		if r.Package == "libc" && r.Category == "virtual" {
			foundLibc = true
		}
	}
	if !foundLibc {
		t.Errorf("expected gcc's deps to include virtual/libc, got %d results", len(results))
		for _, r := range results {
			t.Logf("  result: %s/%s", r.Category, r.Package)
		}
	}
}

func TestSearch_RequiredByNotFound(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{
		RequiredBy: "nonexistent/package",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for nonexistent package, got %d", len(results))
	}
}

func TestSearch_HasUse(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{
		HasUse: "fortran",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result with fortran IUSE, got %d", len(results))
	}
	if results[0].Package != "gcc" {
		t.Errorf("expected gcc, got %s", results[0].Package)
	}
}

func TestSearch_HasUseNotFound(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{
		HasUse: "nonexistent_flag",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearch_HasVersion(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{
		HasVersion: "13.*",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result with version 13.*, got %d", len(results))
	}
	if results[0].Package != "gcc" {
		t.Errorf("expected gcc, got %s", results[0].Package)
	}
}

func TestSearch_HasVersionExact(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{
		HasVersion: "1.75.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result with version 1.75.0, got %d", len(results))
	}
}

func TestSearch_Masked(t *testing.T) {
	db := seedTestDBExtended(t)
	results, err := Search(db, SearchConfig{Masked: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if !r.IsMasked {
			t.Errorf("%s should be masked", r.Package)
		}
	}
	if len(results) == 0 {
		t.Error("expected at least 1 masked package")
	}
}

func TestSearch_Overflow(t *testing.T) {
	db := seedTestDBExtended(t)
	results, err := Search(db, SearchConfig{Overflow: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if !r.IsOverflow {
			t.Errorf("%s should be overflow", r.Package)
		}
	}
	foundBar := false
	for _, r := range results {
		if r.Package == "bar" {
			foundBar = true
		}
	}
	if !foundBar {
		t.Errorf("expected bar in overflow results")
	}
}

func TestSearch_Care(t *testing.T) {
	db := seedTestDBExtended(t)
	results, err := Search(db, SearchConfig{Care: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if !r.IsMasked && !r.IsOverflow {
			t.Errorf("%s should need care (masked or overflow)", r.Package)
		}
	}
	if len(results) == 0 {
		t.Error("expected at least 1 care-needed package")
	}
}

func TestSearch_OnlyNames(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Query: "dev-lang"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results for only-names test, got %d", len(results))
	}
	names := make(map[string]bool)
	for _, r := range results {
		names[r.Package] = true
	}
	if !names["python"] || !names["rust"] {
		t.Errorf("missing packages: %v", names)
	}
}

func TestSearch_CountOnly(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Query: ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 4 {
		t.Errorf("expected count 4, got %d", len(results))
	}
}

func TestSearch_Dump(t *testing.T) {
	r := SearchResult{
		Category:    "dev-lang",
		Package:     "python",
		Version:     "3.12.0",
		Slot:        "3.12",
		Description: "An interpreted language",
		Homepage:    "https://python.org",
		Keywords:    "amd64",
		IUSE:        "ssl sqlite",
		License:     "PSF-2",
		Installed:   true,
		IsMasked:    false,
		AllVersions: []string{"3.12.0", "3.11.5"},
	}

	result := DumpResult(r)
	if !strings.Contains(result, "category=dev-lang") {
		t.Error("dump missing category")
	}
	if !strings.Contains(result, "name=python") {
		t.Error("dump missing name")
	}
	if !strings.Contains(result, "versions=3.12.0 3.11.5") {
		t.Error("dump missing versions")
	}
}

func TestSearch_ResultMasking(t *testing.T) {
	db := seedTestDBExtended(t)

	vdbPath := t.TempDir()

	results, err := Search(db, SearchConfig{Query: "foo", VDBPath: vdbPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].IsMasked {
		t.Error("foo (empty keywords) should be marked as masked")
	}
}

func TestSearch_ResultOverflow(t *testing.T) {
	db := seedTestDBExtended(t)

	vdbPath := t.TempDir()

	results, err := Search(db, SearchConfig{Query: "bar", VDBPath: vdbPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].IsOverflow {
		t.Error("bar (~amd64 ~x86 only) should be marked as overflow")
	}
}

func TestSearch_InstalledVersion(t *testing.T) {
	db := seedTestDB(t)

	vdbPath := t.TempDir()
	pkgDir := filepath.Join(vdbPath, "sys-devel", "gcc")
	os.MkdirAll(pkgDir, 0755)
	os.MkdirAll(filepath.Join(pkgDir, "gcc-13.2.0"), 0755)

	results, err := Search(db, SearchConfig{Query: "gcc", VDBPath: vdbPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].InstalledVer != "13.2.0" {
		t.Errorf("expected installed version 13.2.0, got %q", results[0].InstalledVer)
	}
}

func TestSearch_DependsOnField(t *testing.T) {
	db := seedTestDB(t)
	results, err := Search(db, SearchConfig{Query: "vim"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if len(r.DependsOn) == 0 {
		t.Error("vim should have DependsOn populated")
	}
	foundGcc := false
	foundPython := false
	for _, d := range r.DependsOn {
		if strings.Contains(d, "gcc") {
			foundGcc = true
		}
		if strings.Contains(d, "python") {
			foundPython = true
		}
	}
	if !foundGcc {
		t.Errorf("vim missing gcc dep, deps: %v", r.DependsOn)
	}
	if !foundPython {
		t.Errorf("vim missing python dep, deps: %v", r.DependsOn)
	}
}

func TestMatchVersionGlob(t *testing.T) {
	tests := []struct {
		version string
		pattern string
		want    bool
	}{
		{"1.2.3", "1.*", true},
		{"1.2.3", "2.*", false},
		{"3.12.0", "3.12.*", true},
		{"3.12.0", "3.11.*", false},
		{"13.2.0", "13.*", true},
		{"1.0", "*", true},
		{"9999", "9999", true},
	}

	for _, tt := range tests {
		got := matchVersionGlob(tt.version, tt.pattern)
		if got != tt.want {
			t.Errorf("matchVersionGlob(%q, %q) = %v, want %v", tt.version, tt.pattern, got, tt.want)
		}
	}
}

func TestHasUseFlag(t *testing.T) {
	tests := []struct {
		iuse string
		flag string
		want bool
	}{
		{"ssl sqlite", "ssl", true},
		{"ssl sqlite", "fortran", false},
		{"+ssl -sqlite", "ssl", true},
		{"+ssl -sqlite", "sqlite", true},
		{"", "anything", false},
	}

	for _, tt := range tests {
		got := hasUseFlag(tt.iuse, tt.flag)
		if got != tt.want {
			t.Errorf("hasUseFlag(%q, %q) = %v, want %v", tt.iuse, tt.flag, got, tt.want)
		}
	}
}

func TestExtractCP(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{">=dev-lang/python-3.12", "dev-lang/python"},
		{"sys-devel/gcc", "sys-devel/gcc"},
		{"~dev-lang/python-3.12:3.12", "dev-lang/python"},
		{"=sys-libs/glibc-2.38", "sys-libs/glibc"},
		{"", ""},
		{"no-slash", ""},
	}

	for _, tt := range tests {
		got := extractCP(tt.input)
		if got != tt.want {
			t.Errorf("extractCP(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBestVersionString(t *testing.T) {
	got := bestVersionString([]string{"1.0", "2.0", "1.5"})
	if got != "2.0" {
		t.Errorf("bestVersionString = %q, want 2.0", got)
	}

	got = bestVersionString([]string{})
	if got != "" {
		t.Errorf("bestVersionString empty = %q, want empty", got)
	}

	got = bestVersionString([]string{"13.2.0", "13.1.0", "12.3.0"})
	if got != "13.2.0" {
		t.Errorf("bestVersionString = %q, want 13.2.0", got)
	}
}

func TestPrintResult(t *testing.T) {
	r := SearchResult{
		Category:    "app-editors",
		Package:     "vim",
		Version:     "9.0.2100",
		Slot:        "0",
		Description: "Improved vi-style text editor",
		Homepage:    "https://vim.org",
		Keywords:    "amd64",
		License:     "vim",
		IUSE:        "X gtk",
	}

	tests := []struct {
		fields []string
		want   string
	}{
		{[]string{"category", "name"}, "app-editors vim"},
		{[]string{"package", "version"}, "vim 9.0.2100"},
		{[]string{"description"}, "Improved vi-style text editor"},
		{[]string{"homepage"}, "https://vim.org"},
		{[]string{"unknown_field"}, ""},
		{[]string{}, ""},
	}

	for _, tt := range tests {
		got := PrintResult(r, tt.fields)
		if got != tt.want {
			t.Errorf("PrintResult(fields=%v) = %q, want %q", tt.fields, got, tt.want)
		}
	}
}

func TestMatchField(t *testing.T) {
	if !matchField("dev-lang/python", "python", "python", false) {
		t.Error("substring match should return true")
	}
	if !matchField("dev-lang/python", "DEV-LANG/python", "dev-lang/python", false) {
		t.Error("case-insensitive substring match should return true")
	}
	if !matchField("dev-lang/python", "", "", false) {
		t.Error("empty query should return true")
	}
	if !matchField("dev-lang/python", "dev-lang/python", "dev-lang/python", true) {
		t.Error("exact match should return true")
	}
	if matchField("dev-lang/python", "python", "python", true) {
		t.Error("exact match with different string should return false")
	}
	if !matchField("dev-lang/python", "Dev-Lang/Python", "dev-lang/python", true) {
		t.Error("exact match should be case-insensitive (via EqualFold)")
	}
}

func TestFilterByWorldSet_WithWorldSet(t *testing.T) {
	results := []SearchResult{
		{Category: "sys-apps", Package: "portage", Version: "1.0"},
		{Category: "dev-lang", Package: "python", Version: "3.12"},
		{Category: "app-editors", Package: "vim", Version: "9.0"},
	}

	ws := &world.WorldSet{Atoms: []string{"sys-apps/portage", "dev-lang/python"}}

	filtered := filterByWorldSet(results, ws)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 results, got %d", len(filtered))
	}

	cps := make(map[string]bool)
	for _, r := range filtered {
		cps[r.Category+"/"+r.Package] = true
	}
	if !cps["sys-apps/portage"] {
		t.Error("missing sys-apps/portage")
	}
	if !cps["dev-lang/python"] {
		t.Error("missing dev-lang/python")
	}
	if cps["app-editors/vim"] {
		t.Error("vim should not be in filtered results")
	}
}

func TestFilterByWorldSet_EmptyWorldSet(t *testing.T) {
	results := []SearchResult{
		{Category: "sys-apps", Package: "portage", Version: "1.0"},
		{Category: "dev-lang", Package: "python", Version: "3.12"},
	}

	ws := &world.WorldSet{}

	filtered := filterByWorldSet(results, ws)
	if len(filtered) != 2 {
		t.Fatalf("expected all 2 results to survive, got %d", len(filtered))
	}

	filtered2 := filterByWorldSet(results, nil)
	if len(filtered2) != 2 {
		t.Fatalf("expected all 2 results to survive with nil world set, got %d", len(filtered2))
	}
}

func TestReplaceInstalledVersions(t *testing.T) {
	r := SearchResult{
		Category:     "dev-lang",
		Package:      "python",
		Version:      "3.12.0",
		InstalledVer: "3.12.0",
	}
	result := FormatResult(r, "<category>/<name>-<version> <installedversions:INSTALLED>")
	expected := "dev-lang/python-3.12.0 [3.12.0]"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestReplaceInstalledVersions_Empty(t *testing.T) {
	r := SearchResult{
		Category:     "dev-lang",
		Package:      "python",
		Version:      "3.12.0",
		InstalledVer: "",
	}
	result := FormatResult(r, "<category>/<name>-<version> <installedversions:INSTALLED>")
	expected := "dev-lang/python-3.12.0 "
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func stringsHasIuseFlag(iuse, flag string) bool {
	for _, f := range strings.Fields(iuse) {
		if f == flag {
			return true
		}
	}
	return false
}
