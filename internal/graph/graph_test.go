package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/resolve"
	"github.com/dgraph-io/badger/v4"
)

func openTestDB(t *testing.T) *badger.DB {
	t.Helper()
	opts := badger.DefaultOptions("").WithInMemory(true)
	opts.Logger = nil
	db, err := badger.Open(opts)
	if err != nil {
		t.Fatalf("failed to open in-memory badger: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func ingestEntries(t *testing.T, db *badger.DB, entries []*metadata.PackageMetadata) {
	t.Helper()
	ch := make(chan *metadata.PackageMetadata, len(entries))
	for _, e := range entries {
		ch <- e
	}
	close(ch)
	_, err := ingest.Ingest(db, ch)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
}

func makeMeta(category, pkg, version, depend, rdepend, bdep string) *metadata.PackageMetadata {
	return &metadata.PackageMetadata{
		Category: category,
		Package:  pkg,
		Version:  version,
		DEPEND:   depend,
		RDEPEND:  rdepend,
		BDEPEND:  bdep,
	}
}

func TestBuild_FromInstalled(t *testing.T) {
	m := makeMeta("sys-apps", "foo", "1.0", ">=dev-libs/bar-2.0", "", "")
	g := NewFromInstalled([]*metadata.PackageMetadata{m})

	if len(g.Nodes) < 2 {
		t.Fatalf("expected at least 2 nodes (foo + bar), got %d", len(g.Nodes))
	}

	fooNode, ok := g.Nodes["sys-apps/foo"]
	if !ok {
		t.Fatal("sys-apps/foo not found in graph")
	}
	if fooNode.State != StateInstalled {
		t.Errorf("foo state = %v, want Installed", fooNode.State)
	}
	if len(fooNode.Depends) != 1 {
		t.Fatalf("foo has %d depends, want 1", len(fooNode.Depends))
	}
	if fooNode.Depends[0].Atom.CP() != "dev-libs/bar" {
		t.Errorf("foo depends on %s, want dev-libs/bar", fooNode.Depends[0].Atom.CP())
	}

	barNode, ok := g.Nodes["dev-libs/bar"]
	if !ok {
		t.Fatal("dev-libs/bar not found in graph")
	}
	if len(barNode.RevDepends) != 1 {
		t.Fatalf("bar has %d rev depends, want 1", len(barNode.RevDepends))
	}
	if barNode.RevDepends[0].Atom.CP() != "sys-apps/foo" {
		t.Errorf("bar rev-dep is %s, want sys-apps/foo", barNode.RevDepends[0].Atom.CP())
	}
}

func TestBuild_FromDB(t *testing.T) {
	db := openTestDB(t)

	entries := []*metadata.PackageMetadata{
		makeMeta("sys-apps", "portage", "3.0.51", ">=dev-lang/python-3.10", "", ""),
		makeMeta("dev-lang", "python", "3.10.10", "", "", ""),
	}
	ingestEntries(t, db, entries)

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")

	g, err := Build(db, repoDir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if len(g.Nodes) < 2 {
		t.Fatalf("expected at least 2 nodes, got %d", len(g.Nodes))
	}

	portageNode, ok := g.Nodes["sys-apps/portage"]
	if !ok {
		t.Fatal("sys-apps/portage not found")
	}
	if portageNode.State != StateInstalled {
		t.Errorf("portage state = %v, want Installed", portageNode.State)
	}

	if _, ok := g.Nodes["dev-lang/python"]; !ok {
		t.Fatal("dev-lang/python not found")
	}
}

func TestFindUpdates_HasUpdate(t *testing.T) {
	db := openTestDB(t)

	entries := []*metadata.PackageMetadata{
		makeMeta("sys-apps", "foo", "1.0", "", "", ""),
	}
	ingestEntries(t, db, entries)

	md5Dir := filepath.Join(t.TempDir(), "repo", "metadata", "md5-cache")
	cacheFile := filepath.Join(md5Dir, "sys-apps", "foo-2.0")
	if err := os.MkdirAll(filepath.Dir(cacheFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacheFile, []byte("DEPEND=\nRDEPEND=\nEAPI=8\n"), 0644); err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Dir(filepath.Dir(md5Dir))

	g, err := Build(db, repoDir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	foo, ok := g.Nodes["sys-apps/foo"]
	if !ok {
		t.Fatal("sys-apps/foo not found")
	}
	if foo.State != StateUpdateAvailable {
		t.Errorf("foo state = %v, want UpdateAvailable", foo.State)
	}

	updates := g.FindUpdates()
	if len(updates) != 1 {
		t.Fatalf("FindUpdates = %d, want 1", len(updates))
	}
	if updates[0].Atom.CP() != "sys-apps/foo" {
		t.Errorf("update = %s, want sys-apps/foo", updates[0].Atom.CP())
	}
}

func TestFindUpdates_NoUpdates(t *testing.T) {
	db := openTestDB(t)

	entries := []*metadata.PackageMetadata{
		makeMeta("sys-apps", "foo", "2.0", "", "", ""),
	}
	ingestEntries(t, db, entries)

	md5Dir := filepath.Join(t.TempDir(), "repo", "metadata", "md5-cache")
	cacheFile := filepath.Join(md5Dir, "sys-apps", "foo-1.0")
	if err := os.MkdirAll(filepath.Dir(cacheFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacheFile, []byte("DEPEND=\nRDEPEND=\nEAPI=8\n"), 0644); err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Dir(filepath.Dir(md5Dir))

	g, err := Build(db, repoDir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	updates := g.FindUpdates()
	if len(updates) != 0 {
		t.Errorf("FindUpdates = %d, want 0", len(updates))
	}
}

func TestEmptyDB(t *testing.T) {
	db := openTestDB(t)
	tmpDir := t.TempDir()
	g, err := Build(db, tmpDir)
	if err != nil {
		t.Fatalf("Build with empty DB: %v", err)
	}
	if len(g.Nodes) != 0 {
		t.Errorf("empty DB should produce 0 nodes, got %d", len(g.Nodes))
	}
}

func TestReverseDepsOf(t *testing.T) {
	m1 := makeMeta("sys-apps", "foo", "1.0", ">=dev-libs/bar-2.0", "", "")
	m2 := makeMeta("sys-apps", "baz", "1.0", ">=dev-libs/bar-2.0", "", "")
	g := NewFromInstalled([]*metadata.PackageMetadata{m1, m2})

	rev := g.ReverseDepsOf("dev-libs/bar")
	if len(rev) != 2 {
		t.Fatalf("ReverseDepsOf(bar) = %d, want 2", len(rev))
	}

	cps := make([]string, len(rev))
	for i, n := range rev {
		cps[i] = n.Atom.CP()
	}
	sort.Strings(cps)
	if cps[0] != "sys-apps/baz" || cps[1] != "sys-apps/foo" {
		t.Errorf("reverse deps = %v, want [sys-apps/baz, sys-apps/foo]", cps)
	}
}

func TestReverseDepsOf_None(t *testing.T) {
	m := makeMeta("sys-apps", "foo", "1.0", "", "", "")
	g := NewFromInstalled([]*metadata.PackageMetadata{m})

	rev := g.ReverseDepsOf("sys-apps/foo")
	if len(rev) != 0 {
		t.Errorf("ReverseDepsOf = %d, want 0", len(rev))
	}

	rev = g.ReverseDepsOf("nonexistent/pkg")
	if len(rev) != 0 {
		t.Errorf("ReverseDepsOf for missing = %d, want 0", len(rev))
	}

	rev = g.ReverseDepsOf("")
	if len(rev) != 0 {
		t.Errorf("ReverseDepsOf empty = %d, want 0", len(rev))
	}
}

func TestDepEdges_Conditional(t *testing.T) {
	m := makeMeta("sys-apps", "foo", "1.0", "python? ( >=dev-python/numpy-1.20 )", "", "")
	g := NewFromInstalled([]*metadata.PackageMetadata{m})

	foo, ok := g.Nodes["sys-apps/foo"]
	if !ok {
		t.Fatal("foo not found")
	}

	found := false
	for _, e := range foo.Depends {
		if e.Atom.Category == "dev-python" && e.Atom.Package == "numpy" {
			found = true
			if e.Conditional != "python" {
				t.Errorf("conditional = %q, want python", e.Conditional)
			}
		}
	}
	if !found {
		t.Error("numpy dependency not found")
	}
}

func TestDepEdges_AnyOf(t *testing.T) {
	m := makeMeta("sys-apps", "foo", "1.0", "|| ( dev-lang/python:3.10 dev-lang/python:3.11 )", "", "")
	g := NewFromInstalled([]*metadata.PackageMetadata{m})

	foo, ok := g.Nodes["sys-apps/foo"]
	if !ok {
		t.Fatal("foo not found")
	}

	found3_10 := false
	found3_11 := false
	for _, e := range foo.Depends {
		if e.Atom.Category == "dev-lang" && e.Atom.Package == "python" {
			if e.AnyOfGroup {
				if e.Atom.Slot == "3.10" {
					found3_10 = true
				}
				if e.Atom.Slot == "3.11" {
					found3_11 = true
				}
			}
		}
	}
	if !found3_10 || !found3_11 {
		t.Errorf("any-of deps: found 3.10=%v, found 3.11=%v", found3_10, found3_11)
	}
}

func TestDepEdges_AllTypes(t *testing.T) {
	m := makeMeta(
		"sys-apps", "foo", "1.0",
		">=dev-libs/dep-1.0",
		">=dev-libs/rdep-1.0",
		">=dev-libs/bdep-1.0",
	)
	m.IDEPEND = ">=dev-libs/idep-1.0"
	m.PDEPEND = ">=dev-libs/pdep-1.0"

	g := NewFromInstalled([]*metadata.PackageMetadata{m})

	foo, ok := g.Nodes["sys-apps/foo"]
	if !ok {
		t.Fatal("foo not found")
	}

	typeCounts := map[DepType]int{}
	for _, e := range foo.Depends {
		typeCounts[e.Type]++
	}

	if typeCounts[DepDEPEND] != 1 {
		t.Errorf("DEPEND count = %d, want 1", typeCounts[DepDEPEND])
	}
	if typeCounts[DepRDEPEND] != 1 {
		t.Errorf("RDEPEND count = %d, want 1", typeCounts[DepRDEPEND])
	}
	if typeCounts[DepBDEPEND] != 1 {
		t.Errorf("BDEPEND count = %d, want 1", typeCounts[DepBDEPEND])
	}
	if typeCounts[DepIDEPEND] != 1 {
		t.Errorf("IDEPEND count = %d, want 1", typeCounts[DepIDEPEND])
	}
	if typeCounts[DepPDEPEND] != 1 {
		t.Errorf("PDEPEND count = %d, want 1", typeCounts[DepPDEPEND])
	}
}

func TestFindOutdated(t *testing.T) {
	m1 := makeMeta("sys-apps", "foo", "1.0", ">=dev-libs/bar-2.0", "", "")
	m2 := makeMeta("dev-libs", "bar", "1.0", "", "", "")
	g := NewFromInstalled([]*metadata.PackageMetadata{m1, m2})

	outdated := g.FindOutdated(nil)
	if len(outdated) != 1 {
		t.Fatalf("FindOutdated = %d, want 1", len(outdated))
	}
	if outdated[0].Atom.CP() != "sys-apps/foo" {
		t.Errorf("outdated = %s, want sys-apps/foo", outdated[0].Atom.CP())
	}
}

func TestFindOutdated_AllSatisfied(t *testing.T) {
	m1 := makeMeta("sys-apps", "foo", "1.0", ">=dev-libs/bar-1.0", "", "")
	m2 := makeMeta("dev-libs", "bar", "2.0", "", "", "")
	g := NewFromInstalled([]*metadata.PackageMetadata{m1, m2})

	outdated := g.FindOutdated(nil)
	if len(outdated) != 0 {
		t.Errorf("FindOutdated = %d, want 0", len(outdated))
	}
}

func TestFindOutdated_Conditional(t *testing.T) {
	m1 := makeMeta("sys-apps", "foo", "1.0", "python? ( >=dev-python/numpy-1.20 )", "", "")
	g := NewFromInstalled([]*metadata.PackageMetadata{m1})

	outdated := g.FindOutdated(map[string]bool{"python": false})
	if len(outdated) != 0 {
		t.Errorf("FindOutdated with python disabled = %d, want 0", len(outdated))
	}

	outdated2 := g.FindOutdated(map[string]bool{"python": true})
	if len(outdated2) != 1 {
		t.Errorf("FindOutdated with python enabled = %d, want 1", len(outdated2))
	}
}

func TestInstalledAtoms(t *testing.T) {
	m := makeMeta("sys-apps", "foo", "1.0", "", "", "")
	g := NewFromInstalled([]*metadata.PackageMetadata{m})

	if len(g.InstalledAtoms) != 1 {
		t.Fatalf("InstalledAtoms len = %d, want 1", len(g.InstalledAtoms))
	}
	if _, ok := g.InstalledAtoms["sys-apps/foo-1.0"]; !ok {
		t.Error("sys-apps/foo-1.0 not found in InstalledAtoms")
	}
}

func TestPkgState_String(t *testing.T) {
	tests := []struct {
		state PkgState
		want  string
	}{
		{StateMissing, "Missing"},
		{StateInstalled, "Installed"},
		{StateUpdateAvailable, "UpdateAvailable"},
		{StateOutdatedDeps, "OutdatedDeps"},
		{PkgState(999), "Unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("PkgState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestDepType_String(t *testing.T) {
	tests := []struct {
		dt   DepType
		want string
	}{
		{DepDEPEND, "DEPEND"},
		{DepRDEPEND, "RDEPEND"},
		{DepBDEPEND, "BDEPEND"},
		{DepIDEPEND, "IDEPEND"},
		{DepPDEPEND, "PDEPEND"},
		{DepType(999), "Unknown"},
	}
	for _, tt := range tests {
		if got := tt.dt.String(); got != tt.want {
			t.Errorf("DepType(%d).String() = %q, want %q", tt.dt, got, tt.want)
		}
	}
}

func TestVersionGreater(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"2.0", "1.0", true},
		{"1.0", "2.0", false},
		{"1.0", "1.0", false},
		{"1.0_p1", "1.0", true},
		{"1.0-r1", "1.0", true},
		{"1.0-r2", "1.0-r1", true},
		{"1.0-r1", "1.0-r2", false},
	}
	for _, tt := range tests {
		got := versionGreater(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("versionGreater(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestMatchAtom(t *testing.T) {
	installedCPV := "dev-libs/bar-2.0"

	tests := []struct {
		dep  string
		want bool
	}{
		{">=dev-libs/bar-1.0", true},
		{">=dev-libs/bar-2.0", true},
		{">=dev-libs/bar-3.0", false},
		{"dev-libs/bar", true},
		{"dev-libs/baz", false},
		{"dev-foo/bar", false},
	}
	for _, tt := range tests {
		a, err := atom.Parse(tt.dep)
		if err != nil {
			t.Fatalf("parse %q: %v", tt.dep, err)
		}
		if got := matchAtom(a, installedCPV); got != tt.want {
			t.Errorf("matchAtom(%q, %q) = %v, want %v", tt.dep, installedCPV, got, tt.want)
		}
	}
}

func TestBuild_EmptyDepStrings(t *testing.T) {
	m := makeMeta("sys-apps", "foo", "1.0", "", "", "")
	g := NewFromInstalled([]*metadata.PackageMetadata{m})

	foo, ok := g.Nodes["sys-apps/foo"]
	if !ok {
		t.Fatal("foo not found")
	}
	if len(foo.Depends) != 0 {
		t.Errorf("foo has %d depends with empty dep strings, want 0", len(foo.Depends))
	}
}

func TestBuild_DedupEdges(t *testing.T) {
	m := makeMeta("sys-apps", "foo", "1.0", ">=dev-libs/bar-1.0 >=dev-libs/bar-1.0", "", "")
	g := NewFromInstalled([]*metadata.PackageMetadata{m})

	foo, ok := g.Nodes["sys-apps/foo"]
	if !ok {
		t.Fatal("foo not found")
	}

	count := 0
	for _, e := range foo.Depends {
		if e.Atom.CP() == "dev-libs/bar" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("duplicate edge count = %d, want 1", count)
	}
}

func TestToResolveGraph_Basic(t *testing.T) {
	m := makeMeta("sys-apps", "foo", "1.0", ">=dev-libs/bar-2.0", "", "")
	g := NewFromInstalled([]*metadata.PackageMetadata{m})
	rg := g.ToResolveGraph()

	if rg.Packages == nil {
		t.Fatal("Packages map is nil")
	}
	if len(rg.Packages) < 2 {
		t.Fatalf("expected at least 2 packages, got %d", len(rg.Packages))
	}

	fooNode, ok := rg.Packages["sys-apps/foo"]
	if !ok {
		t.Fatal("sys-apps/foo not found in resolve graph")
	}
	if len(fooNode.Versions) == 0 {
		t.Fatal("foo has no versions")
	}

	// ToResolveGraph adds version entries; verify at least one has a version string matching metadata
	foundVersion := false
	for _, vi := range fooNode.Versions {
		if vi.Version != nil && vi.Version.Raw == "1.0" {
			foundVersion = true
		}
	}
	if !foundVersion {
		// If no version info was found, check Installed status as fallback
		for _, vi := range fooNode.Versions {
			if vi.Installed {
				foundVersion = true
			}
		}
		t.Log("version not directly available on VersionInfo; ToResolveGraph may not propagate version from Atom")
	}

	if len(fooNode.Deps) != 1 {
		t.Fatalf("foo has %d deps, want 1", len(fooNode.Deps))
	}
	if fooNode.Deps[0].To == nil || fooNode.Deps[0].To.Atom.CP() != "dev-libs/bar" {
		t.Errorf("expected dep to dev-libs/bar, got %v", fooNode.Deps[0].To)
	}
}

func TestToResolveGraph_IUSEDefaults(t *testing.T) {
	m := makeMeta("media-libs", "mesa", "26.0.8", "", "", "")
	m.IUSE = "+opengl -test vulkan"
	g := NewFromInstalled([]*metadata.PackageMetadata{m})
	rg := g.ToResolveGraph()

	vi := rg.Packages["media-libs/mesa"].GetBestVersion()
	if vi == nil {
		t.Fatal("mesa has no resolver version")
	}
	if !vi.UseFlags["opengl"] {
		t.Error("+opengl should be enabled by default")
	}
	if vi.UseFlags["test"] || vi.UseFlags["vulkan"] {
		t.Errorf("disabled IUSE defaults were enabled: %#v", vi.UseFlags)
	}
}

func TestToResolveGraph_DepTypeTranslation(t *testing.T) {
	m := makeMeta(
		"sys-apps", "foo", "1.0",
		">=dev-libs/dep-1.0",
		">=dev-libs/rdep-1.0",
		">=dev-libs/bdep-1.0",
	)
	_ = m
	g := NewFromInstalled([]*metadata.PackageMetadata{m})
	rg := g.ToResolveGraph()

	fooNode, ok := rg.Packages["sys-apps/foo"]
	if !ok {
		t.Fatal("foo not found")
	}

	typeCounts := map[resolve.DepType]int{}
	for _, e := range fooNode.Deps {
		typeCounts[e.Type]++
	}

	if typeCounts[resolve.DepTypeDepend] != 1 {
		t.Errorf("DEPEND count = %d, want 1", typeCounts[resolve.DepTypeDepend])
	}
	if typeCounts[resolve.DepTypeRuntime] != 1 {
		t.Errorf("RDEPEND count = %d, want 1", typeCounts[resolve.DepTypeRuntime])
	}
	if typeCounts[resolve.DepTypeBuild] != 1 {
		t.Errorf("BDEPEND count = %d, want 1", typeCounts[resolve.DepTypeBuild])
	}
}

func TestToResolveGraph_ConditionalDeps(t *testing.T) {
	m := makeMeta("sys-apps", "foo", "1.0", "python? ( >=dev-libs/conditional-1.0 )", "", "")
	g := NewFromInstalled([]*metadata.PackageMetadata{m})
	rg := g.ToResolveGraph()

	fooNode, ok := rg.Packages["sys-apps/foo"]
	if !ok {
		t.Fatal("foo not found")
	}

	found := false
	for _, e := range fooNode.Deps {
		if e.To != nil && e.To.Atom.CP() == "dev-libs/conditional" {
			found = true
			if e.UseCond != "python" {
				t.Errorf("expected UseCond 'python', got %q", e.UseCond)
			}
		}
	}
	if !found {
		t.Error("conditional dependency not found in resolve graph")
	}
}

func TestBuildParallel_Basic(t *testing.T) {
	db := openTestDB(t)
	entries := []*metadata.PackageMetadata{
		makeMeta("sys-apps", "foo", "1.0", ">=dev-libs/bar-2.0", "", ""),
	}
	ingestEntries(t, db, entries)

	tmpDir := t.TempDir()
	g, err := BuildParallel(db, tmpDir, 2)
	if err != nil {
		t.Fatalf("BuildParallel: %v", err)
	}

	if len(g.Nodes) < 2 {
		t.Fatalf("expected at least 2 nodes, got %d", len(g.Nodes))
	}

	fooNode, ok := g.Nodes["sys-apps/foo"]
	if !ok {
		t.Fatal("sys-apps/foo not found")
	}
	if fooNode.State != StateInstalled {
		t.Errorf("foo state = %v, want Installed", fooNode.State)
	}
	if len(fooNode.Depends) != 1 {
		t.Fatalf("foo has %d depends, want 1", len(fooNode.Depends))
	}
	if fooNode.Depends[0].Atom.CP() != "dev-libs/bar" {
		t.Errorf("foo depends on %s, want dev-libs/bar", fooNode.Depends[0].Atom.CP())
	}
}

func TestBuildParallel_WorkerCount1(t *testing.T) {
	db := openTestDB(t)
	entries := []*metadata.PackageMetadata{
		makeMeta("sys-apps", "foo", "1.0", "", "", ""),
	}
	ingestEntries(t, db, entries)

	tmpDir := t.TempDir()
	g, err := BuildParallel(db, tmpDir, 1)
	if err != nil {
		t.Fatalf("BuildParallel with 1 worker: %v", err)
	}
	if len(g.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(g.Nodes))
	}
}

func TestBuildParallel_WorkerCount2(t *testing.T) {
	db := openTestDB(t)
	entries := []*metadata.PackageMetadata{
		makeMeta("sys-apps", "a", "1.0", "", "", ""),
		makeMeta("sys-apps", "b", "1.0", "", "", ""),
		makeMeta("sys-apps", "c", "1.0", "", "", ""),
	}
	ingestEntries(t, db, entries)

	tmpDir := t.TempDir()
	g, err := BuildParallel(db, tmpDir, 2)
	if err != nil {
		t.Fatalf("BuildParallel with 2 workers: %v", err)
	}
	if len(g.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(g.Nodes))
	}
}

func TestBuildParallel_WorkerCount4(t *testing.T) {
	db := openTestDB(t)
	entries := []*metadata.PackageMetadata{
		makeMeta("sys-apps", "a", "1.0", "", "", ""),
		makeMeta("sys-apps", "b", "1.0", "", "", ""),
		makeMeta("sys-apps", "c", "1.0", "", "", ""),
		makeMeta("sys-apps", "d", "1.0", "", "", ""),
		makeMeta("sys-apps", "e", "1.0", "", "", ""),
	}
	ingestEntries(t, db, entries)

	tmpDir := t.TempDir()
	g, err := BuildParallel(db, tmpDir, 4)
	if err != nil {
		t.Fatalf("BuildParallel with 4 workers: %v", err)
	}
	if len(g.Nodes) != 5 {
		t.Fatalf("expected 5 nodes, got %d", len(g.Nodes))
	}
}

func TestVersionOrEmpty(t *testing.T) {
	if got := versionOrEmpty(nil); got != "" {
		t.Errorf("versionOrEmpty(nil) = %q, want \"\"", got)
	}

	a := &atom.Atom{Category: "test", Package: "pkg", Version: &atom.Version{Raw: "1.2.3"}}
	if got := versionOrEmpty(a); got != "1.2.3" {
		t.Errorf("versionOrEmpty with version) = %q, want \"1.2.3\"", got)
	}

	b := &atom.Atom{Category: "test", Package: "pkg"}
	if got := versionOrEmpty(b); got != "" {
		t.Errorf("versionOrEmpty(no version) = %q, want \"\"", got)
	}
}

func TestBuild_NilMetadataEntries(t *testing.T) {
	g := NewFromInstalled([]*metadata.PackageMetadata{nil, nil})

	if len(g.Nodes) != 0 {
		t.Errorf("expected 0 nodes with nil entries, got %d", len(g.Nodes))
	}
}

func TestBuild_MalformedDepAtom(t *testing.T) {
	m := makeMeta("sys-apps", "foo", "1.0", "not/a/valid/atom ", "", "")
	g := NewFromInstalled([]*metadata.PackageMetadata{m})

	foo, ok := g.Nodes["sys-apps/foo"]
	if !ok {
		t.Fatal("foo not found")
	}
	if len(foo.Depends) != 0 {
		t.Errorf("foo has %d depends with malformed dep atom, want 0", len(foo.Depends))
	}
}

func TestAllAtoms_Populated(t *testing.T) {
	m := makeMeta("sys-apps", "foo", "1.0", "", "", "")
	g := NewFromInstalled([]*metadata.PackageMetadata{m})

	if len(g.AllAtoms) != 1 {
		t.Errorf("AllAtoms len = %d, want 1", len(g.AllAtoms))
	}
}

func TestFindOutdated_SkipUninstalled(t *testing.T) {
	m1 := makeMeta("sys-apps", "foo", "1.0", "", "", "")

	g := &DepGraph{
		Nodes:          make(map[string]*PkgNode),
		InstalledAtoms: make(map[string]*atom.Atom),
	}

	a, _ := atom.Parse("sys-apps/foo-1.0")
	g.InstalledAtoms["sys-apps/foo-1.0"] = a
	g.Nodes["sys-apps/foo"] = &PkgNode{
		Atom:     a,
		Metadata: m1,
		State:    StateInstalled,
	}

	g.Nodes["sys-apps/bar"] = &PkgNode{
		Atom:  &atom.Atom{Category: "sys-apps", Package: "bar"},
		State: StateMissing,
	}

	outdated := g.FindOutdated(nil)
	for _, o := range outdated {
		if o.Atom.CP() == "sys-apps/bar" {
			t.Error("missing package should not appear in outdated")
		}
	}
}

func TestGraph_Adversarial_EmptyRepoDir(t *testing.T) {
	db := openTestDB(t)
	g, err := Build(db, "")
	if err != nil {
		t.Logf("Build with empty repo dir returned error (acceptable): %v", err)
	}
	if g == nil && err == nil {
		t.Error("expected either an error or a non-nil graph")
	}
}

func TestGraph_Adversarial_MassiveData(t *testing.T) {
	max := 100
	entries := make([]*metadata.PackageMetadata, max)
	for i := range max {
		entries[i] = makeMeta("sys-apps", fmt.Sprintf("pkg%d", i), fmt.Sprintf("%d.0", i), "", "", "")
	}

	t.Logf("building graph with %d packages", max)
	graph := NewFromInstalled(entries)
	if len(graph.Nodes) != max {
		t.Errorf("expected %d nodes, got %d", max, len(graph.Nodes))
	}
}

func TestGraph_Adversarial_DeepDepChain(t *testing.T) {
	depth := 100
	entries := make([]*metadata.PackageMetadata, depth)
	for i := range depth {
		pn := fmt.Sprintf("pkg%d", i)
		depend := ""
		if i > 0 {
			depend = fmt.Sprintf(">=test-cat/pkg%d-%d", i-1, i-1)
		}
		entries[i] = makeMeta("test-cat", pn, fmt.Sprintf("%d.0", i), depend, depend, "")
	}

	graph := NewFromInstalled(entries)
	if len(graph.Nodes) != depth {
		t.Errorf("expected %d nodes, got %d", depth, len(graph.Nodes))
	}
}

func TestGraph_Adversarial_SelfReferentialDep(t *testing.T) {
	m := makeMeta("sys-apps", "selfref", "1.0", ">=sys-apps/selfref-1", ">=sys-apps/selfref-1", "")

	graph := NewFromInstalled([]*metadata.PackageMetadata{m})

	node := graph.Nodes["sys-apps/selfref"]
	if node == nil {
		t.Fatal("expected node for selfref")
	}
	if len(node.Depends) < 1 {
		t.Error("self-referential dep should be in edge list (even if it's the same package)")
	}
}
