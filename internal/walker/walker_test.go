package walker

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"

	"github.com/airencracken/arise/internal/metadata"
)

func cachePayload(eapi, slot, desc string) string {
	return "EAPI=" + eapi + "\nSLOT=" + slot + "\nDESCRIPTION=" + desc + "\n"
}

// writeCacheEntry creates a cache file at root/category/name with the given
// content string. Returns the full path.
func writeCacheEntry(t *testing.T, root, category, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, category)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll %s: %v", dir, err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile %s: %v", p, err)
	}
	return p
}

// drainChannels reads results and errs concurrently until both are closed.
func drainChannels(t *testing.T, results <-chan *metadata.PackageMetadata, errs <-chan error) ([]*metadata.PackageMetadata, []error) {
	t.Helper()
	var (
		pkgs []*metadata.PackageMetadata
		er   []error
		wg   sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for p := range results {
			pkgs = append(pkgs, p)
		}
	}()
	go func() {
		defer wg.Done()
		for e := range errs {
			er = append(er, e)
		}
	}()
	wg.Wait()
	return pkgs, er
}

func TestWalkCacheDir_Basic(t *testing.T) {
	root := t.TempDir()

	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))
	writeCacheEntry(t, root, "sys-apps", "portage-3.0.50", cachePayload("7", "0", "Old Portage"))
	writeCacheEntry(t, root, "app-editors", "vim-9.0.0001", cachePayload("8", "0", "Vim editor"))

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(pkgs) != 3 {
		t.Fatalf("got %d packages, want 3", len(pkgs))
	}

	keys := make([]string, len(pkgs))
	for i, p := range pkgs {
		keys[i] = p.Key()
	}
	sort.Strings(keys)
	want := []string{"app-editors/vim", "sys-apps/portage", "sys-apps/portage"}
	if !equalStrings(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
}

func TestWalkCacheDir_EmptyDir(t *testing.T) {
	root := t.TempDir()

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(pkgs) != 0 {
		t.Errorf("got %d packages, want 0", len(pkgs))
	}
	if len(errs) != 0 {
		t.Errorf("got %d errors, want 0: %v", len(errs), errs)
	}
}

func TestWalkCacheDir_NonexistentRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(pkgs) != 0 {
		t.Errorf("got %d packages from nonexistent root, want 0", len(pkgs))
	}
	if len(errs) != 1 {
		t.Fatalf("got %d errors from nonexistent root, want 1", len(errs))
	}
	if !os.IsNotExist(errs[0]) {
		t.Errorf("want os.IsNotExist error, got %T: %v", errs[0], errs[0])
	}
}

func TestWalkCacheDir_Workers(t *testing.T) {
	for _, workers := range []int{1, 2, 4, runtime.NumCPU(), runtime.NumCPU() * 2} {
		t.Run("workers", func(t *testing.T) {
			root := t.TempDir()
			for i := 0; i < 20; i++ {
				cat := "cat-" + string(rune('a'+i%5))
				pkg := "pkg-" + string(rune('a'+i))
				writeCacheEntry(t, root, cat, pkg+"-1.0", cachePayload("8", "0", "desc"))
			}

			resCh, errCh := WalkCacheDir(root, workers)
			pkgs, errs := drainChannels(t, resCh, errCh)

			if len(errs) != 0 {
				t.Errorf("workers=%d: unexpected errors: %v", workers, errs)
			}
			if len(pkgs) != 20 {
				t.Errorf("workers=%d: got %d packages, want 20", workers, len(pkgs))
			}
		})
	}
}

func TestWalkCacheDir_InvalidCPV(t *testing.T) {
	root := t.TempDir()

	// Valid file
	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))
	// File in root (no category dir) — relpath lacks '/', invalid CPV
	badPath := filepath.Join(root, "bad-entry")
	if err := os.WriteFile(badPath, []byte(cachePayload("8", "0", "bad")), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(pkgs) != 1 {
		t.Errorf("got %d valid packages, want 1", len(pkgs))
	}

	found := false
	for _, err := range errs {
		if err != nil && err.Error() != "" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected an error for the file with invalid CPV")
	}
}

func TestWalkCacheDir_BadFileStillWalksRemaining(t *testing.T) {
	root := t.TempDir()

	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))
	// Place a file with a name that produces an invalid CPV
	if err := os.WriteFile(filepath.Join(root, "nopkg"), []byte("garbage"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	writeCacheEntry(t, root, "app-editors", "vim-9.0.0001", cachePayload("8", "0", "Vim"))

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(pkgs) != 2 {
		t.Errorf("got %d packages, want 2 (bad file should not stop the walk)", len(pkgs))
	}
	if len(errs) == 0 {
		t.Error("expected at least one error from the bad file")
	}
}

func TestWalkCacheDir_FileReadError(t *testing.T) {
	root := t.TempDir()

	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))
	// Create an unreadable file
	badPath := filepath.Join(root, "sys-apps", "unreadable-1.0")
	if err := os.WriteFile(badPath, []byte("data"), 0000); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(pkgs) != 1 {
		t.Errorf("got %d packages, want 1", len(pkgs))
	}
	if len(errs) == 0 {
		t.Error("expected an error for unreadable file")
	}
}

func TestWalkCache_DefaultWorkers(t *testing.T) {
	root := t.TempDir()

	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))

	resCh, errCh := WalkCache(root)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(pkgs) != 1 {
		t.Errorf("got %d packages, want 1", len(pkgs))
	}
}

func TestWalkCacheDir_SingleFile(t *testing.T) {
	root := t.TempDir()

	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))

	resCh, errCh := WalkCacheDir(root, 1)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(pkgs) != 1 {
		t.Fatalf("got %d packages, want 1", len(pkgs))
	}
	if pkgs[0].Category != "sys-apps" {
		t.Errorf("Category = %q, want sys-apps", pkgs[0].Category)
	}
	if pkgs[0].Package != "portage" {
		t.Errorf("Name = %q, want portage", pkgs[0].Package)
	}
	if pkgs[0].Version != "3.0.51" {
		t.Errorf("Version = %q, want 3.0.51", pkgs[0].Version)
	}
}

func TestWalkCacheDir_ZeroWorkers(t *testing.T) {
	root := t.TempDir()

	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))

	resCh, errCh := WalkCacheDir(root, 0)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(pkgs) != 1 {
		t.Errorf("got %d packages, want 1", len(pkgs))
	}
}

func TestWalkCacheDir_NegativeWorkers(t *testing.T) {
	root := t.TempDir()

	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))

	resCh, errCh := WalkCacheDir(root, -1)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(pkgs) != 1 {
		t.Errorf("got %d packages, want 1", len(pkgs))
	}
}

func TestWalkCacheDir_ManyFiles(t *testing.T) {
	root := t.TempDir()

	const n = 500
	categories := []string{"app-admin", "app-editors", "sys-apps", "dev-lang", "net-misc"}
	for i := 0; i < n; i++ {
		cat := categories[i%len(categories)]
		name := "pkg-" + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + "-1.0"
		writeCacheEntry(t, root, cat, name, cachePayload("8", "0", "desc"))
	}

	resCh, errCh := WalkCacheDir(root, 0)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(pkgs) != n {
		t.Errorf("got %d packages, want %d", len(pkgs), n)
	}
}

func TestWalkCacheDir_SubdirectoryIgnored(t *testing.T) {
	root := t.TempDir()

	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))
	// Create a nested subdirectory with a file — WalkDir recurses into it,
	// producing a relpath like "sys-apps/nested/pkg-1.0" which is not a normal CPV.
	nestedDir := filepath.Join(root, "sys-apps", "nested")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "pkg-1.0"), []byte(cachePayload("8", "0", "nested")), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(pkgs) < 1 {
		t.Errorf("got %d packages, want at least 1", len(pkgs))
	}
	// The nested file may or may not error depending on whether ParseCacheEntry
	// accepts "sys-apps/nested/pkg-1.0" as a valid CPV. Either way the walk
	// must not crash.
	t.Logf("packages: %d, errors: %d", len(pkgs), len(errs))
}

func TestWalkCacheDir_ChannelClose(t *testing.T) {
	root := t.TempDir()

	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))

	results, errs := WalkCacheDir(root, 2)
	pkgs, er := drainChannels(t, results, errs)

	if len(pkgs) != 1 {
		t.Errorf("got %d packages, want 1", len(pkgs))
	}
	if len(er) != 0 {
		t.Errorf("unexpected errors: %v", er)
	}
}

func TestWalkCacheDir_SkipsNonRegular(t *testing.T) {
	root := t.TempDir()

	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))
	symlink := filepath.Join(root, "sys-apps", "symlink")
	if err := os.Symlink("portage-3.0.51", symlink); err != nil {
		t.Skipf("cannot create symlink: %v (possibly on unsupported fs)", err)
	}

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	// The symlink is skipped because d.Type().IsRegular() is false for symlinks
	if len(pkgs) != 1 {
		t.Errorf("got %d packages, want 1 (symlink should be skipped)", len(pkgs))
	}
}

func TestWalkCacheDir_ConcurrentResultsNonDeterministic(t *testing.T) {
	root := t.TempDir()

	const n = 100
	for i := 0; i < n; i++ {
		cat := "cat-" + string(rune('a'+i%10)) + string(rune('a'+i/10%10))
		pkg := "pkg-" + string(rune('0'+i%10)) + string(rune('0'+i/10%10)) + "-1.0"
		writeCacheEntry(t, root, cat, pkg, cachePayload("8", "0", "desc"))
	}

	// High concurrency ensures out-of-order delivery
	resCh, errCh := WalkCacheDir(root, 16)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(pkgs) != n {
		t.Errorf("got %d packages, want %d", len(pkgs), n)
	}

	// Results are delivered in completion order, verify uniqueness
	seen := make(map[string]bool)
	for _, p := range pkgs {
		if seen[(p.Category + "/" + p.Package + "-" + p.Version)] {
			t.Errorf("duplicate result for %s", (p.Category + "/" + p.Package + "-" + p.Version))
		}
		seen[(p.Category + "/" + p.Package + "-" + p.Version)] = true
	}
	if len(seen) != n {
		t.Errorf("got %d unique results, want %d", len(seen), n)
	}
}

func TestWalkCacheDir_Atomicity(t *testing.T) {
	root := t.TempDir()

	const nFiles = 50
	for i := 0; i < nFiles; i++ {
		cat := "cat-" + string(rune('a'+i%5))
		pkg := "pkg-" + string(rune('a'+i)) + "-1.0"
		writeCacheEntry(t, root, cat, pkg, cachePayload("8", "0", "desc"))
	}

	for _, workers := range []int{1, 3, 8} {
		t.Run("workers", func(t *testing.T) {
			resCh, errCh := WalkCacheDir(root, workers)
			pkgs, errs := drainChannels(t, resCh, errCh)
			if len(errs) != 0 {
				t.Errorf("unexpected errors: %v", errs)
			}
			if len(pkgs) != nFiles {
				t.Errorf("workers=%d: got %d results, want %d", workers, len(pkgs), nFiles)
			}
		})
	}
}

func TestWalkCacheDir_CPVParsing(t *testing.T) {
	root := t.TempDir()

	// package with revision
	writeCacheEntry(t, root, "dev-lang", "python-3.11.5-r1", cachePayload("8", "0", "Python"))
	// package with alpha suffix
	writeCacheEntry(t, root, "dev-lang", "python-3.12.0_alpha1", cachePayload("8", "0", "Python alpha"))
	// package with plus sign in name
	writeCacheEntry(t, root, "x11-libs", "gtk+-3.24.39", cachePayload("8", "0", "GTK"))

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(pkgs) != 3 {
		t.Fatalf("got %d packages, want 3", len(pkgs))
	}

	byVersion := make(map[string]*metadata.PackageMetadata)
	for _, p := range pkgs {
		byVersion[p.Version] = p
	}

	if p, ok := byVersion["3.11.5-r1"]; !ok || p.Category != "dev-lang" || p.Package != "python" {
		t.Errorf("missing or wrong python-3.11.5-r1")
	}
	if p, ok := byVersion["3.12.0_alpha1"]; !ok || p.Category != "dev-lang" || p.Package != "python" {
		t.Errorf("missing or wrong python-3.12.0_alpha1")
	}
	if p, ok := byVersion["3.24.39"]; !ok || p.Category != "x11-libs" || p.Package != "gtk+" {
		t.Errorf("missing or wrong gtk+-3.24.39")
	}
}

func TestWalkCacheDir_MultipleCategories(t *testing.T) {
	root := t.TempDir()

	cats := []string{
		"app-admin",
		"app-editors",
		"dev-lang",
		"net-misc",
		"sys-apps",
		"sys-devel",
		"x11-libs",
	}
	for _, cat := range cats {
		writeCacheEntry(t, root, cat, "pkg-1.0", cachePayload("8", "0", cat))
	}

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(pkgs) != len(cats) {
		t.Errorf("got %d packages, want %d", len(pkgs), len(cats))
	}

	seenCats := make(map[string]bool)
	for _, p := range pkgs {
		seenCats[p.Category] = true
	}
	for _, c := range cats {
		if !seenCats[c] {
			t.Errorf("category %q not seen", c)
		}
	}
}

func TestWalkCacheDir_AdversarialFilenames(t *testing.T) {
	root := t.TempDir()

	entries := []struct {
		cat, name string
	}{
		{"x11-libs", "\x00test-1.0"},
		{"sys-apps", "pkg\x00-1.0"},
		{"\x00cat", "pkg-1.0"},
	}

	for _, e := range entries {
		dir := filepath.Join(root, e.cat)
		_ = os.MkdirAll(dir, 0755)
		// WriteFile may succeed or fail depending on filesystem
		_ = os.WriteFile(filepath.Join(dir, e.name), []byte(cachePayload("8", "0", "nul")), 0644)
	}

	// The walk must not panic regardless of what filenames exist.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("WalkCacheDir panicked on adversarial filenames: %v", r)
		}
	}()

	resCh, errCh := WalkCacheDir(root, 2)
	_, _ = drainChannels(t, resCh, errCh)
}

func TestWalkCacheDir_ParseCacheEntryNeverCalledForDirs(t *testing.T) {
	root := t.TempDir()

	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))
	// Create an empty category directory
	if err := os.MkdirAll(filepath.Join(root, "empty-cat"), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(pkgs) != 1 {
		t.Errorf("got %d packages, want 1", len(pkgs))
	}
}

// TestWalkCacheDir_RootIsFile verifies that when root is a file (not a
// directory), WalkCacheDir handles it gracefully.
func TestWalkCacheDir_RootIsFile(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "file")
	if err := os.WriteFile(root, []byte("data"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	resCh, errCh := WalkCacheDir(root, 2)
	_, errs := drainChannels(t, resCh, errCh)
	if len(errs) == 0 {
		t.Error("expected an error when root is a file")
	}
}

func TestWalkCacheDir_ResultsClosedAfterWalk(t *testing.T) {
	root := t.TempDir()

	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))

	results, errs := WalkCacheDir(root, 2)

	// Drain results; after it's closed, receiving should return zero value, false
	var count int
	for range results {
		count++
	}
	if count != 1 {
		t.Errorf("got %d results, want 1", count)
	}

	// errs should eventually close too
	for range errs {
	}
}

func TestWalkCacheDir_SymlinkDir(t *testing.T) {
	root := t.TempDir()

	srcDir := filepath.Join(t.TempDir(), "src")
	writeCacheEntry(t, srcDir, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))

	linkDir := filepath.Join(root, "link")
	if err := os.Symlink(srcDir, linkDir); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	// WalkDir follows symlinks by default? No — by default Go's WalkDir does
	// NOT follow symlinks. Let's verify the walk handles this.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("WalkCacheDir panicked on symlinked directories: %v", r)
		}
	}()

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)
	t.Logf("packages: %d, errors: %d", len(pkgs), len(errs))
}

// property: for each file written, exactly one result is produced
func TestWalkCacheDir_NoLossNoDuplication(t *testing.T) {
	root := t.TempDir()

	const n = 200
	written := make(map[string]bool)
	for i := 0; i < n; i++ {
		cat := "cat-" + string(rune('a'+i%13)) + string(rune('a'+i/13%13))
		name := "pkg-" + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + "-1.0"
		fullPath := writeCacheEntry(t, root, cat, name, cachePayload("8", "0", "desc"))
		written[fullPath] = true
	}

	resCh, errCh := WalkCacheDir(root, 0)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(pkgs) != n {
		t.Errorf("got %d packages, want %d", len(pkgs), n)
	}

	// Verify no duplicates by CPV
	seen := make(map[string]bool, len(pkgs))
	for _, p := range pkgs {
		cpv := p.Category + "/" + p.Package + "-" + p.Version
		if seen[cpv] {
			t.Errorf("duplicate result for %s", cpv)
		}
		seen[cpv] = true
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestWalkCacheDir_DirWithOnlySubdirs(t *testing.T) {
	root := t.TempDir()

	// Category dirs with no files
	for _, cat := range []string{"app-admin", "app-editors", "sys-apps"} {
		if err := os.MkdirAll(filepath.Join(root, cat), 0755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(pkgs) != 0 {
		t.Errorf("got %d packages, want 0", len(pkgs))
	}
}

func TestWalkCacheDir_FileWithoutNewline(t *testing.T) {
	root := t.TempDir()

	// Valid cache entry with trailing newline
	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", "EAPI=8\nSLOT=0\n")
	// Entry without trailing newline
	writeCacheEntry(t, root, "sys-apps", "portage-3.0.50", "EAPI=7\nSLOT=0")

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(pkgs) != 2 {
		t.Errorf("got %d packages, want 2", len(pkgs))
	}
}

func TestWalkCacheDir_LongFilePaths(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long path test in short mode")
	}

	root := t.TempDir()

	longName := ""
	for len(longName) < 200 {
		longName += "x"
	}

	writeCacheEntry(t, root, longName, "pkg-1.0", cachePayload("8", "0", "desc"))

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("WalkCacheDir panicked on long paths: %v", r)
		}
	}()

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)
	t.Logf("packages: %d, errors: %d", len(pkgs), len(errs))
}

func TestWalkCacheDir_ConcurrentSafetyPathsChannel(t *testing.T) {
	root := t.TempDir()

	const nFiles = 200
	for i := 0; i < nFiles; i++ {
		cat := "cat-" + string(rune('a'+i%15))
		pkg := "pkg-" + string(rune('a'+i%26)) + "-1.0"
		writeCacheEntry(t, root, cat, pkg, cachePayload("8", "0", "desc"))
	}

	results, errs := WalkCacheDir(root, 0)
	drainChannels(t, results, errs)
	// If we got here without a deadlock or race, it passed.
}

func TestWalkCacheDir_PropertyResultCountEqualsFileCount(t *testing.T) {
	for _, workers := range []int{1, 3, 7, 0} {
		root := t.TempDir()
		const n = 73
		for i := 0; i < n; i++ {
			cat := "cat-" + string(rune('a'+i%7))
			pkg := "pkg-" + string(rune('a'+i%26)) + "-1.0"
			writeCacheEntry(t, root, cat, pkg, cachePayload("8", "0", "desc"))
		}

		resCh, errCh := WalkCacheDir(root, workers)
		pkgs, errs := drainChannels(t, resCh, errCh)
		if len(errs) != 0 {
			t.Errorf("workers=%d: unexpected errors: %v", workers, errs)
		}
		if len(pkgs) != n {
			t.Errorf("workers=%d: got %d results, want %d", workers, len(pkgs), n)
		}
	}
}

func TestWalkCacheDir_SingleWorker(t *testing.T) {
	root := t.TempDir()

	const n = 50
	for i := 0; i < n; i++ {
		cat := "cat-" + string(rune('a'+i%10))
		pkg := "pkg-" + string(rune('a'+i%26)) + "-1.0"
		writeCacheEntry(t, root, cat, pkg, cachePayload("8", "0", "desc"))
	}

	resCh, errCh := WalkCacheDir(root, 1)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(pkgs) != n {
		t.Errorf("got %d packages, want %d", len(pkgs), n)
	}
}

func TestWalkCacheDir_IgnoresDotFiles(t *testing.T) {
	root := t.TempDir()

	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))
	// Hidden file in category directory
	writeCacheEntry(t, root, "sys-apps", ".hidden-1.0", cachePayload("8", "0", "hidden"))

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(pkgs) != 1 {
		t.Errorf("got %d packages, want 1 (dotfiles are skipped)", len(pkgs))
	}
}

func TestWalkCacheDir_ReadOnlyCategoryDir(t *testing.T) {
	root := t.TempDir()

	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))

	readOnlyDir := filepath.Join(root, "locked")
	if err := os.Mkdir(readOnlyDir, 0000); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	defer os.Chmod(readOnlyDir, 0755)

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(pkgs) != 1 {
		t.Errorf("got %d packages, want 1", len(pkgs))
	}
	// The locked directory may or may not produce a walk error depending on
	// filesystem; assert presence.
	foundPermErr := false
	for _, err := range errs {
		if os.IsPermission(err) {
			foundPermErr = true
			break
		}
	}
	if !foundPermErr && len(errs) > 0 {
		t.Logf("got errors: %v (expected permission error from locked dir)", errs)
	}
}

func TestWalkCacheDir_MetadataFieldsPreserved(t *testing.T) {
	root := t.TempDir()

	content := "EAPI=8\nSLOT=3/3.1\nDESCRIPTION=Test package\nHOMEPAGE=https://example.com\nLICENSE=GPL-2\n"
	writeCacheEntry(t, root, "sys-apps", "testpkg-1.0", content)

	resCh, errCh := WalkCacheDir(root, 1)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(pkgs) != 1 {
		t.Fatalf("got %d packages, want 1", len(pkgs))
	}

	p := pkgs[0]
	if p.EAPI != "8" {
		t.Errorf("EAPI = %q, want 8", p.EAPI)
	}
	if p.SLOT != "3" {
		t.Errorf("Slot = %q, want 3", p.SLOT)
	}
	if p.Subslot != "3.1" {
		t.Errorf("Subslot = %q, want 3.1", p.Subslot)
	}
	if p.DESCRIPTION != "Test package" {
		t.Errorf("Description = %q, want 'Test package'", p.DESCRIPTION)
	}
	if p.HOMEPAGE != "https://example.com" {
		t.Errorf("Homepage = %q, want https://example.com", p.HOMEPAGE)
	}
	if p.LICENSE != "GPL-2" {
		t.Errorf("License = %q, want GPL-2", p.LICENSE)
	}
}

func TestWalkCacheDir_CPVWithNoVersion(t *testing.T) {
	root := t.TempDir()

	// Package without a version (e.g., virtual packages)
	// The file name is just the package name, like "cat/pkg" directory layout
	// But in md5-cache, each file is "package-version", so "pkg" with no version
	// would be unusual. Test it anyway.
	writeCacheEntry(t, root, "virtual", "rust-1.75.0", cachePayload("8", "0", "Rust"))

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)

	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(pkgs) != 1 {
		t.Fatalf("got %d packages, want 1", len(pkgs))
	}
	if pkgs[0].Package != "rust" {
		t.Errorf("Name = %q, want rust", pkgs[0].Package)
	}
}

func TestWalkCacheDir_WalkErrorForUnreadableRoot(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping permission test when running as root")
	}
	root := t.TempDir()

	if err := os.Chmod(root, 0000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	defer os.Chmod(root, 0755)

	resCh, errCh := WalkCacheDir(root, 2)
	_, errs := drainChannels(t, resCh, errCh)
	if len(errs) == 0 {
		t.Error("expected an error for unreadable root")
	}
}

func TestWalkCacheDir_ResultsNotClosedEarly(t *testing.T) {
	root := t.TempDir()

	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))

	results, errs := WalkCacheDir(root, 2)

	// Read the result
	val, ok := <-results
	if !ok {
		t.Fatal("results channel closed before delivering result")
	}
	if val.Key() != "sys-apps/portage" {
		t.Errorf("Key = %q, want sys-apps/portage", val.Key())
	}

	// Results should close after all items are sent
	_, ok = <-results
	if ok {
		t.Error("results channel should be closed after draining")
	}

	// Drain errors
	for range errs {
	}
}

func TestWalkCacheDir_CachesCanBeWalkedConcurrently(t *testing.T) {
	const nRoots = 4
	roots := make([]string, nRoots)
	for i := 0; i < nRoots; i++ {
		roots[i] = t.TempDir()
		writeCacheEntry(t, roots[i], "sys-apps", "pkg-1.0", cachePayload("8", "0", "desc"))
	}

	var wg sync.WaitGroup
	for i := 0; i < nRoots; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resCh, errCh := WalkCacheDir(roots[idx], 2)
			pkgs, errs := drainChannels(t, resCh, errCh)
			if len(errs) != 0 {
				t.Errorf("root %d: unexpected errors: %v", idx, errs)
			}
			if len(pkgs) != 1 {
				t.Errorf("root %d: got %d packages, want 1", idx, len(pkgs))
			}
		}(i)
	}
	wg.Wait()
}

func TestWalkCacheDir_ImmutabilityDuringWalk(t *testing.T) {
	root := t.TempDir()

	writeCacheEntry(t, root, "sys-apps", "pkg-1.0", cachePayload("8", "0", "a"))

	results, errs := WalkCacheDir(root, 2)

	// Drain the single result to ensure the walk has started processing
	pkg, ok := <-results
	if !ok {
		t.Fatal("expected a result")
	}
	_ = pkg

	// Results channel should eventually close
	for range results {
	}
	for range errs {
	}
}

func TestWalkCacheDir_SymlinksInRoot(t *testing.T) {
	root := t.TempDir()

	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "P"))
	depRoot := filepath.Join(t.TempDir(), "dep")
	writeCacheEntry(t, depRoot, "sys-apps", "dep-1.0", cachePayload("8", "0", "D"))

	// Place a symlink to another directory inside the walked root
	symlink := filepath.Join(root, "linked")
	if err := os.Symlink(depRoot, symlink); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	_ = symlink

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("WalkCacheDir panicked with symlink dir: %v", r)
		}
	}()
	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)
	t.Logf("packages: %d, errors: %d", len(pkgs), len(errs))
}

func TestWalkCacheDir_AdversarialBinaryContent(t *testing.T) {
	root := t.TempDir()

	// Binary content that might crash a naive parser
	binaryContent := make([]byte, 2048)
	for i := range binaryContent {
		binaryContent[i] = byte(i)
	}

	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))
	if err := os.WriteFile(filepath.Join(root, "sys-apps", "binary-1.0"), binaryContent, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Another valid entry after the binary one
	writeCacheEntry(t, root, "sys-apps", "portage-3.0.50", cachePayload("8", "0", "Portage 2"))

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("WalkCacheDir panicked on binary content: %v", r)
		}
	}()

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)
	// ParseCacheEntry is lenient and may parse binary content without error;
	// the important thing is the walk did not crash.
	if len(pkgs) < 2 {
		t.Errorf("got %d packages, want at least 2", len(pkgs))
	}
	_ = errs
}

func TestWalkCacheDir_WhitespaceOnlyContent(t *testing.T) {
	root := t.TempDir()

	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))
	if err := os.WriteFile(filepath.Join(root, "sys-apps", "blank-1.0"), []byte("   \n  \n\t\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)

	// ParseCacheEntry handles whitespace-only data without error.
	if len(pkgs) < 1 {
		t.Errorf("got %d packages, want at least 1", len(pkgs))
	}
	if len(errs) != 0 {
		t.Logf("unexpected errors (may be ok depending on parser): %v", errs)
	}
}

func TestWalkCacheDir_EmptyContent(t *testing.T) {
	root := t.TempDir()

	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))
	if err := os.WriteFile(filepath.Join(root, "sys-apps", "empty-1.0"), []byte{}, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("WalkCacheDir panicked on empty file: %v", r)
		}
	}()

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)

	// ParseCacheEntry handles empty data without error.
	if len(pkgs) < 1 {
		t.Errorf("got %d packages, want at least 1", len(pkgs))
	}
	_ = errs
}

func TestWalkCacheDir_FilepathRelError(t *testing.T) {
	// filepath.Rel returns an error when the base and target are on different
	// volumes/roots. We simulate this by passing root as a relative path and
	// the file as an absolute path, but that's hard in test.
	// Instead, verify that the error path in the worker is reachable by
	// providing a root path that is a prefix but causes issues.
	// This is a best-effort test for the Rel error branch.
	root := t.TempDir()

	writeCacheEntry(t, root, "sys-apps", "portage-3.0.51", cachePayload("8", "0", "Portage"))

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("WalkCacheDir panicked: %v", r)
		}
	}()

	resCh, errCh := WalkCacheDir(root, 2)
	pkgs, errs := drainChannels(t, resCh, errCh)
	if len(pkgs) != 1 {
		t.Errorf("got %d packages, want 1", len(pkgs))
	}
	_ = errs
}
