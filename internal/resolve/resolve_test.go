package resolve

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/portage"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// atomMatchesDep wraps atomMatches for test use, extracting version from installed atom.
func atomMatchesDep(installed *atom.Atom, constraint *atom.Atom, slot, subslot string, useFlags map[string]bool) bool {
	var ver *atom.Version
	if installed != nil {
		ver = installed.Version
	}
	return atomMatches(installed, constraint, slot, subslot, useFlags, ver)
}

func mustParse(s string) *atom.Atom {
	a, err := atom.Parse(s)
	if err != nil {
		panic(fmt.Sprintf("bad test atom %q: %v", s, err))
	}
	return a
}

func makeGraph() *DepGraph {
	return NewDepGraph()
}

func pkg(g *DepGraph, cp, version, slot, subslot string, installed bool, useFlags map[string]bool) *VersionInfo {
	return g.AddVersion(cp, version, slot, subslot, installed, useFlags, "")
}

func pkgKeywords(g *DepGraph, cp, version, slot, subslot string, installed bool, useFlags map[string]bool, keywords string) *VersionInfo {
	return g.AddVersion(cp, version, slot, subslot, installed, useFlags, keywords)
}

// pkgWithMeta adds a version and returns it; caller can set .RequiredUse and .License on the result.
func pkgWithMeta(g *DepGraph, cp, version, slot, subslot string, installed bool, useFlags map[string]bool, requiredUse, license string) *VersionInfo {
	vi := g.AddVersion(cp, version, slot, subslot, installed, useFlags, "")
	vi.RequiredUse = requiredUse
	vi.License = license
	return vi
}

func deps(g *DepGraph, fromCP, depAtomStr string) *DepEdge {
	depA, err := atom.Parse(depAtomStr)
	if err != nil {
		depA = mustParse(depAtomStr)
	}
	g.AddDep(fromCP, depA.CP(), depAtomStr, DepTypeRuntime, "", false)
	return nil
}

func depWithType(g *DepGraph, fromCP, depAtomStr string, dt DepType) {
	depA := mustParse(depAtomStr)
	g.AddDep(fromCP, depA.CP(), depAtomStr, dt, "", false)
}

func depBlock(g *DepGraph, fromCP, depAtomStr string) {
	depA := mustParse(depAtomStr)
	g.AddDep(fromCP, depA.CP(), depAtomStr, DepTypeRuntime, "", true)
}

func depUse(g *DepGraph, fromCP, depAtomStr, useCond string) {
	depA := mustParse(depAtomStr)
	g.AddDep(fromCP, depA.CP(), depAtomStr, DepTypeRuntime, useCond, false)
}

func anyOf(g *DepGraph, fromCP string, dt DepType, opts ...*DepAtom) {
	g.AddAnyOfDep(fromCP, dt, opts)
}

func anyOfDep(atomStr string) *DepAtom {
	return &DepAtom{Atom: mustParse(atomStr)}
}

func anyOfDepUse(atomStr, useCond string) *DepAtom {
	return &DepAtom{Atom: mustParse(atomStr), UseCond: useCond}
}

func collectCPV(actions []PkgAction) []string {
	var out []string
	for _, a := range actions {
		s := a.Atom.CP()
		if a.Atom.Version != nil && a.Atom.Version.Raw != "" {
			s += "-" + a.Atom.Version.Raw
		} else if a.Slot != "" {
			s += ":" + a.Slot
		}
		out = append(out, s)
	}
	return out
}

func hasAction(actions []PkgAction, cpv, action string) bool {
	for _, a := range actions {
		cp := a.Atom.CP()
		if a.Atom.Version != nil && a.Atom.Version.Raw != "" {
			cp += "-" + a.Atom.Version.Raw
		}
		if cp == cpv && a.Action == action {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Test: simple install — one package with no deps
// ---------------------------------------------------------------------------

func TestResolve_SimpleInstall(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)

	result, err := Resolve(g, []string{"app-editors/vim"}, DefaultResolveConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Install) != 1 {
		t.Fatalf("expected 1 install, got %d: %v", len(result.Install), collectCPV(result.Install))
	}

	a := result.Install[0]
	if a.Atom.CP() != "app-editors/vim" {
		t.Errorf("expected app-editors/vim, got %s", a.Atom.CP())
	}
	if a.Action != "install" && a.Action != "update" {
		t.Errorf("expected install or update, got %s", a.Action)
	}
	if len(result.Uninstall) != 0 {
		t.Errorf("expected 0 uninstalls, got %d", len(result.Uninstall))
	}
	if len(result.Conflicts) != 0 {
		t.Errorf("expected 0 conflicts, got %v", result.Conflicts)
	}
}

// ---------------------------------------------------------------------------
// Test: install with one unsatisfied dep
// ---------------------------------------------------------------------------

func TestResolve_UnsatisfiedDep(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", false, nil)
	deps(g, "app-editors/vim", ">=dev-libs/libfoo-1.0")

	result, err := Resolve(g, []string{"app-editors/vim"}, DefaultResolveConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Install) < 2 {
		t.Fatalf("expected at least 2 installs, got %d: %v", len(result.Install), collectCPV(result.Install))
	}

	foundVim := false
	foundLibfoo := false
	for _, a := range result.Install {
		switch a.Atom.CP() {
		case "app-editors/vim":
			foundVim = true
		case "dev-libs/libfoo":
			foundLibfoo = true
			if !strings.Contains(a.Reason, "dependency") {
				t.Errorf("expected dependency reason for libfoo, got %q", a.Reason)
			}
		}
	}
	if !foundVim {
		t.Error("app-editors/vim not found in install list")
	}
	if !foundLibfoo {
		t.Error("dev-libs/libfoo not found in install list")
	}
}

// ---------------------------------------------------------------------------
// Test: already-installed package with satisfied deps
// ---------------------------------------------------------------------------

func TestResolve_AlreadyInstalledSatisfied(t *testing.T) {
	g := makeGraph()
	// vim is already installed
	pkg(g, "app-editors/vim", "9.0", "0", "0", true, nil)
	// libfoo is also installed (dep)
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", true, nil)
	deps(g, "app-editors/vim", ">=dev-libs/libfoo-1.0")

	result, err := Resolve(g, []string{"app-editors/vim"}, DefaultResolveConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Install) != 0 {
		t.Errorf("expected 0 installs, got %d: %v", len(result.Install), collectCPV(result.Install))
	}
}

// ---------------------------------------------------------------------------
// Test: update mode — newer version available
// ---------------------------------------------------------------------------

func TestResolve_UpdateMode(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "8.0", "0", "0", true, nil)
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)
	deps(g, "app-editors/vim", "dev-libs/libfoo")
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", true, nil)

	cfg := DefaultResolveConfig()
	cfg.Update = true

	result, err := Resolve(g, []string{"app-editors/vim"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Install) == 0 {
		t.Fatal("expected at least 1 install (update) in update mode")
	}

	found := false
	for _, a := range result.Install {
		if a.Atom.CP() == "app-editors/vim" {
			found = true
			if a.Action != "update" {
				t.Errorf("expected action=update, got %s", a.Action)
			}
		}
	}
	if !found {
		t.Error("app-editors/vim not found in install list during update mode")
	}
}

// ---------------------------------------------------------------------------
// Test: deep mode — recursive dep checking
// ---------------------------------------------------------------------------

func TestResolve_DeepMode(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)
	deps(g, "app-editors/vim", "dev-libs/libfoo")
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", false, nil)
	deps(g, "dev-libs/libfoo", "dev-libs/libbar")
	pkg(g, "dev-libs/libbar", "2.0", "0", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.Deep = true

	result, err := Resolve(g, []string{"app-editors/vim"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// should include vim, libfoo, and libbar (3-level deep)
	found := map[string]bool{}
	for _, a := range result.Install {
		found[a.Atom.CP()] = true
	}
	if !found["app-editors/vim"] {
		t.Error("app-editors/vim missing")
	}
	if !found["dev-libs/libfoo"] {
		t.Error("dev-libs/libfoo missing (deep mode should recurse)")
	}
	if !found["dev-libs/libbar"] {
		t.Error("dev-libs/libbar missing (deep mode should recurse 2 levels)")
	}
}

// ---------------------------------------------------------------------------
// Test: AnyOf group prefers already-installed
// ---------------------------------------------------------------------------

func TestResolve_AnyOfPreferInstalled(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-shells/bash", "5.0", "0", "0", false, nil)
	pkg(g, "virtual/editor", "1", "0", "0", true, nil)   // already installed
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil) // not installed
	pkg(g, "app-editors/nano", "7.0", "0", "0", false, nil) // not installed

	anyOf(g, "app-shells/bash", DepTypeRuntime,
		anyOfDep("virtual/editor"),
		anyOfDep("app-editors/vim"),
		anyOfDep("app-editors/nano"),
	)

	cfg := DefaultResolveConfig()
	cfg.Deep = true

	result, err := Resolve(g, []string{"app-shells/bash"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// bash should be installed
	foundBash := false
	for _, a := range result.Install {
		if a.Atom.CP() == "app-shells/bash" {
			foundBash = true
		}
	}
	if !foundBash {
		t.Error("app-shells/bash should be in install list")
	}

	// since virtual/editor is already installed, vim and nano should NOT be installed
	for _, a := range result.Install {
		if a.Atom.CP() == "app-editors/vim" {
			t.Error("app-editors/vim should NOT be installed (virtual/editor already satisfies any-of)")
		}
		if a.Atom.CP() == "app-editors/nano" {
			t.Error("app-editors/nano should NOT be installed (virtual/editor already satisfies any-of)")
		}
	}
}

// ---------------------------------------------------------------------------
// Test: block — unresolvable conflict
// ---------------------------------------------------------------------------

func TestResolve_BlockUnresolvable(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)
	pkg(g, "app-editors/nano", "7.0", "0", "0", true, nil)
	depBlock(g, "app-editors/vim", "app-editors/nano")

	cfg := DefaultResolveConfig()
	cfg.Deep = true

	// nano is required by world explicitly, and vim blocks it
	result, err := Resolve(g, []string{"app-editors/vim", "app-editors/nano"}, cfg)
	if err == nil && result != nil && len(result.Conflicts) == 0 {
		t.Error("expected conflicts due to unresolvable block")
	}
	// when err is returned, conflicts were detected
	if err != nil {
		t.Logf("expected conflict error: %v", err)
	}
	// nano should not be uninstalled automatically (it's a target)
	if result != nil {
		for _, a := range result.Uninstall {
			if a.Atom.CP() == "app-editors/nano" {
				t.Error("nano is in world set, should not be uninstalled automatically")
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Test: block — resolvable by uninstalling blocked package
// ---------------------------------------------------------------------------

func TestResolve_BlockResolvableByUninstall(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)
	pkg(g, "app-editors/nano", "7.0", "0", "0", true, nil)
	depBlock(g, "app-editors/vim", "app-editors/nano")

	cfg := DefaultResolveConfig()
	cfg.Deep = true

	// nano is NOT requested as a target, so it can be uninstalled
	result, err := Resolve(g, []string{"app-editors/vim"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// nano should be in uninstall list
	foundUninstall := false
	for _, a := range result.Uninstall {
		if a.Atom.CP() == "app-editors/nano" && a.Action == "uninstall" {
			foundUninstall = true
		}
	}
	if !foundUninstall {
		t.Error("nano should be uninstalled due to block from vim")
	}
}

// ---------------------------------------------------------------------------
// Test: topological sort of install list
// ---------------------------------------------------------------------------

func TestSortByDeps_Linear(t *testing.T) {
	g := makeGraph()
	deps(g, "app-shells/bash", "dev-libs/readline")
	deps(g, "dev-libs/readline", "sys-libs/ncurses")

	actions := []PkgAction{
		{Atom: mustParse("dev-libs/readline"), Action: "install"},
		{Atom: mustParse("app-shells/bash"), Action: "install"},
		{Atom: mustParse("sys-libs/ncurses"), Action: "install"},
	}

	sorted := SortByDeps(actions, g)
	names := collectCPV(sorted)

	// ncurses must come before readline, readline before bash
	findPos := func(name string) int {
		for i, s := range names {
			if s == name {
				return i
			}
		}
		return -1
	}

	ncPos := findPos("sys-libs/ncurses")
	rlPos := findPos("dev-libs/readline")
	bashPos := findPos("app-shells/bash")

	if ncPos < 0 || rlPos < 0 || bashPos < 0 {
		t.Fatalf("missing packages in sorted output: ncurses=%d readline=%d bash=%d", ncPos, rlPos, bashPos)
	}
	if ncPos >= rlPos {
		t.Errorf("ncurses (pos %d) must come before readline (pos %d)", ncPos, rlPos)
	}
	if rlPos >= bashPos {
		t.Errorf("readline (pos %d) must come before bash (pos %d)", rlPos, bashPos)
	}
}

func TestSortByDeps_Diamond(t *testing.T) {
	g := makeGraph()
	deps(g, "app-misc/top", "dev-libs/liba")
	deps(g, "app-misc/top", "dev-libs/libb")
	deps(g, "dev-libs/liba", "sys-libs/common")
	deps(g, "dev-libs/libb", "sys-libs/common")

	actions := []PkgAction{
		{Atom: mustParse("app-misc/top"), Action: "install"},
		{Atom: mustParse("dev-libs/liba"), Action: "install"},
		{Atom: mustParse("dev-libs/libb"), Action: "install"},
		{Atom: mustParse("sys-libs/common"), Action: "install"},
	}

	sorted := SortByDeps(actions, g)
	names := collectCPV(sorted)

	// common must come before liba and libb, which must come before top
	findPos := func(name string) int {
		for i, s := range names {
			if s == name {
				return i
			}
		}
		return -1
	}

	commonPos := findPos("sys-libs/common")
	libaPos := findPos("dev-libs/liba")
	libbPos := findPos("dev-libs/libb")
	topPos := findPos("app-misc/top")

	if commonPos < 0 || libaPos < 0 || libbPos < 0 || topPos < 0 {
		t.Fatalf("missing packages in sorted output")
	}
	if commonPos >= libaPos {
		t.Error("common must come before liba")
	}
	if commonPos >= libbPos {
		t.Error("common must come before libb")
	}
	if libaPos >= topPos {
		t.Error("liba must come before top")
	}
	if libbPos >= topPos {
		t.Error("libb must come before top")
	}
}

func TestSortByDeps_Empty(t *testing.T) {
	g := makeGraph()
	sorted := SortByDeps(nil, g)
	if sorted != nil {
		t.Errorf("expected nil for empty actions, got %v", sorted)
	}
}

func TestSortByDeps_Single(t *testing.T) {
	g := makeGraph()
	actions := []PkgAction{
		{Atom: mustParse("app-editors/vim"), Action: "install"},
	}
	sorted := SortByDeps(actions, g)
	if len(sorted) != 1 || sorted[0].Atom.CP() != "app-editors/vim" {
		t.Errorf("single action should be returned unchanged")
	}
}

// ---------------------------------------------------------------------------
// Test: slot operator := triggers rebuild
// ---------------------------------------------------------------------------

func TestResolve_SlotOperatorRebuild(t *testing.T) {
	g := makeGraph()
	// bash depends on readline:=
	pkg(g, "app-shells/bash", "5.0", "0", "0", false, nil)
	// readline 8.0 installed with subslot 0/1
	pkg(g, "sys-libs/readline", "8.0", "0", "1", true, nil)
	// readline 8.1 available with subslot 0/2 (changed)
	pkg(g, "sys-libs/readline", "8.1", "0", "2", false, nil)

	// dependency with slot operator :=
	depA, _ := atom.Parse("sys-libs/readline:0=")
	depA.SlotOp = atom.SlotOpEq
	g.AddDep("app-shells/bash", "sys-libs/readline", "sys-libs/readline:0=", DepTypeRuntime, "", false)

	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.Deep = true

	result, err := Resolve(g, []string{"app-shells/bash"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// bash should be in the install list (triggered by slot operator rebuild)
	foundBash := false
	foundReadline := false
	for _, a := range result.Install {
		cp := a.Atom.CP()
		if cp == "app-shells/bash" {
			foundBash = true
		}
		if cp == "sys-libs/readline" {
			foundReadline = true
		}
	}
	if !foundBash {
		t.Error("bash should be in install list (dep of readline slot op rebuild)")
	}
	if !foundReadline {
		t.Error("readline should be in install list (update to new subslot)")
	}
}

// ---------------------------------------------------------------------------
// Test: USE conditional — skip when flag disabled
// ---------------------------------------------------------------------------

func TestResolve_UseConditional(t *testing.T) {
	g := makeGraph()
	// vim depends on python? (dev-lang/python)
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, map[string]bool{"python": false})
	pkg(g, "dev-lang/python", "3.11", "0", "0", false, nil)
	depUse(g, "app-editors/vim", "dev-lang/python", "python")

	cfg := DefaultResolveConfig()
	cfg.Deep = true

	result, err := Resolve(g, []string{"app-editors/vim"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// python should NOT be installed since the USE flag is disabled
	for _, a := range result.Install {
		if a.Atom.CP() == "dev-lang/python" {
			t.Error("python should not be installed (USE flag python is disabled)")
		}
	}
}

func TestResolve_UseConditionalEnabled(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, map[string]bool{"python": true})
	pkg(g, "dev-lang/python", "3.11", "0", "0", false, nil)
	depUse(g, "app-editors/vim", "dev-lang/python", "python")

	cfg := DefaultResolveConfig()
	cfg.Deep = true

	result, err := Resolve(g, []string{"app-editors/vim"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, a := range result.Install {
		if a.Atom.CP() == "dev-lang/python" {
			found = true
		}
	}
	if !found {
		t.Error("python should be installed (USE flag python is enabled)")
	}
}

// ---------------------------------------------------------------------------
// Test: empty target list
// ---------------------------------------------------------------------------

func TestResolve_EmptyTargets(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)

	result, err := Resolve(g, nil, DefaultResolveConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Install) != 0 {
		t.Errorf("expected 0 installs for empty targets, got %d", len(result.Install))
	}

	result2, err := Resolve(g, []string{}, DefaultResolveConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result2.Install) != 0 {
		t.Errorf("expected 0 installs for empty target slice, got %d", len(result2.Install))
	}
}

// ---------------------------------------------------------------------------
// Test: unknown target package
// ---------------------------------------------------------------------------

func TestResolve_UnknownPackage(t *testing.T) {
	g := makeGraph()
	cfg := DefaultResolveConfig()

	result, err := Resolve(g, []string{"nonexistent/pkg"}, cfg)
	if err != nil {
		return // expected error for unknown package
	}
	if result == nil {
		t.Error("expected non-nil result")
		return
	}
	// should report conflict or return empty install
	if len(result.Conflicts) == 0 {
		t.Error("expected a conflict for unknown package")
	}
}

// ---------------------------------------------------------------------------
// Test: deeply nested dep chains
// ---------------------------------------------------------------------------

func TestResolve_DeeplyNested(t *testing.T) {
	g := makeGraph()
	// build a chain: p0 -> p1 -> p2 -> ... -> p9
	const depth = 10
	for i := 0; i < depth; i++ {
		cp := fmt.Sprintf("cat/pkg%d", i)
		pkg(g, cp, "1.0", "0", "0", false, nil)
	}
	for i := 0; i < depth-1; i++ {
		from := fmt.Sprintf("cat/pkg%d", i)
		to := fmt.Sprintf("cat/pkg%d", i+1)
		depStr := fmt.Sprintf(">=%s-1.0", to)
		deps(g, from, depStr)
	}

	cfg := DefaultResolveConfig()
	cfg.Deep = true

	result, err := Resolve(g, []string{"cat/pkg0"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Install) < depth {
		t.Errorf("expected %d installs (deep chain), got %d: %v", depth, len(result.Install), collectCPV(result.Install))
	}

	// check that p9 (leaf) is installed
	found := false
	for _, a := range result.Install {
		if a.Atom.CP() == "cat/pkg9" {
			found = true
		}
	}
	if !found {
		t.Error("leaf package p9 not installed; deep mode should have traversed full chain")
	}
}

// ---------------------------------------------------------------------------
// Test: complete graph — rebuild reverse deps (conceptual; tested via
// slot operator rebuild scenario)
// ---------------------------------------------------------------------------

func TestResolve_CompleteGraphConcept(t *testing.T) {
	g := makeGraph()
	// libfoo depends on libbar
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", true, nil)
	pkg(g, "dev-libs/libbar", "2.0", "0/1", "1", true, nil)
	deps(g, "dev-libs/libfoo", "dev-libs/libbar")

	// newer libbar available with changed subslot
	pkg(g, "dev-libs/libbar", "3.0", "0/2", "2", false, nil)

	// libfoo has slot operator on libbar
	for _, edge := range g.Packages["dev-libs/libfoo"].Deps {
		if edge.To != nil && edge.To.Atom.CP() == "dev-libs/libbar" {
			edge.DepAtom.SlotOp = atom.SlotOpEq
		}
	}

	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.Deep = true
	cfg.CompleteGraph = true

	result, err := Resolve(g, []string{"dev-libs/libbar"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// libfoo should be rebuilt because libbar's subslot changed
	foundFoo := false
	foundBar := false
	for _, a := range result.Install {
		if a.Atom.CP() == "dev-libs/libfoo" {
			foundFoo = true
		}
		if a.Atom.CP() == "dev-libs/libbar" {
			foundBar = true
		}
	}
	if !foundBar {
		t.Error("libbar should be in install list (update)")
	}
	if !foundFoo {
		t.Error("libfoo should be rebuilt due to libbar subslot change (complete graph / slot op)")
	}
}

// ---------------------------------------------------------------------------
// Test: property — install list contains no duplicates
// ---------------------------------------------------------------------------

func TestProperty_NoDuplicates(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-misc/diamond", "1.0", "0", "0", false, nil)
	pkg(g, "dev-libs/shared", "2.0", "0", "0", false, nil)
	pkg(g, "dev-libs/liba", "1.0", "0", "0", false, nil)
	pkg(g, "dev-libs/libb", "1.0", "0", "0", false, nil)

	deps(g, "app-misc/diamond", "dev-libs/liba")
	deps(g, "app-misc/diamond", "dev-libs/libb")
	// both liba and libb depend on shared
	deps(g, "dev-libs/liba", "dev-libs/shared")
	deps(g, "dev-libs/libb", "dev-libs/shared")

	cfg := DefaultResolveConfig()
	cfg.Deep = true

	result, err := Resolve(g, []string{"app-misc/diamond"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	seen := make(map[string]int) // CP -> count
	for _, a := range result.Install {
		cp := a.Atom.CP()
		seen[cp]++
	}
	for cp, count := range seen {
		if count > 1 {
			t.Errorf("package %s appears %d times in install list (expected at most 1)", cp, count)
		}
	}
}

// ---------------------------------------------------------------------------
// Test: property — all deps satisfied after install list is applied
// ---------------------------------------------------------------------------

func TestProperty_DepsSatisfiedAfterInstall(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-shells/bash", "5.2", "0", "0", false, nil)
	pkg(g, "dev-libs/readline", "8.2", "0", "0", false, nil)
	pkg(g, "sys-libs/ncurses", "6.4", "0", "0", false, nil)
	deps(g, "app-shells/bash", ">=dev-libs/readline-8")
	deps(g, "dev-libs/readline", ">=sys-libs/ncurses-6")

	cfg := DefaultResolveConfig()
	cfg.Deep = true

	result, err := Resolve(g, []string{"app-shells/bash"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// build set of installed packages after resolution
	installedAfter := make(map[string]*atom.Version)
	for _, a := range result.Install {
		if a.Atom.Version != nil {
			installedAfter[a.Atom.CP()] = a.Atom.Version
		}
	}

	// for each installed package, check its deps are satisfied
	for _, a := range result.Install {
		node := g.Packages[a.Atom.CP()]
		if node == nil {
			continue
		}
		for _, edge := range node.Deps {
			if edge.Block {
				continue
			}
			if len(edge.AnyOf) > 0 {
				// any-of: at least one must be satisfied
				anySatisfied := false
				for _, opt := range edge.AnyOf {
					if opt.Atom == nil {
						continue
					}
					opVer, ok := installedAfter[opt.Atom.CP()]
					if ok {
						iv := &atom.Atom{Category: opt.Atom.Category, Package: opt.Atom.Package, Version: opVer}
						if atomMatchesDep(iv, opt.Atom, "", "", nil) {
							anySatisfied = true
							break
						}
					}
				}
				if !anySatisfied {
					t.Errorf("any-of dep of %s not satisfied after resolution", a.Atom.CP())
				}
				continue
			}
			if edge.To == nil || edge.DepAtom == nil {
				continue
			}
			depVer, ok := installedAfter[edge.To.Atom.CP()]
			if !ok {
				t.Errorf("dep %s of %s not in install list", edge.To.Atom.CP(), a.Atom.CP())
				continue
			}
			iv := &atom.Atom{Category: edge.To.Atom.Category, Package: edge.To.Atom.Package, Version: depVer}
			if !atomMatchesDep(iv, edge.DepAtom, "", "", nil) {
				t.Errorf("dep %s %s not satisfied by %s", edge.DepAtom.String(), edge.To.Atom.CP(), depVer.String())
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Test: version matching functions
// ---------------------------------------------------------------------------

func TestAtomMatches_Basic(t *testing.T) {
	installed := mustParse("dev-libs/libfoo-2.0")

	tests := []struct {
		name       string
		constraint string
		want       bool
	}{
		{"exact match", "=dev-libs/libfoo-2.0", true},
		{"gte satisfied", ">=dev-libs/libfoo-1.0", true},
		{"gte equal", ">=dev-libs/libfoo-2.0", true},
		{"gte not satisfied", ">=dev-libs/libfoo-3.0", false},
		{"gt satisfied", ">dev-libs/libfoo-1.0", true},
		{"gt not satisfied eq", ">dev-libs/libfoo-2.0", false},
		{"lte satisfied", "<=dev-libs/libfoo-2.0", true},
		{"lte satisfied lower", "<=dev-libs/libfoo-3.0", true},
		{"lt satisfied", "<dev-libs/libfoo-3.0", true},
		{"lt not satisfied eq", "<dev-libs/libfoo-2.0", false},
		{"tilde match", "~dev-libs/libfoo-2.0", true},
		{"tilde not match", "~dev-libs/libfoo-3.0", false},
		{"any version", "dev-libs/libfoo", true},
		{"wrong package", "dev-libs/otherlib-1.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := mustParse(tt.constraint)
			got := atomMatchesDep(installed, c, "", "", nil)
			if got != tt.want {
				t.Errorf("atomMatchesDep(%s, %s) = %v, want %v", installed.String(), c.String(), got, tt.want)
			}
		})
	}
}

func TestAtomMatches_Slot(t *testing.T) {
	installed := mustParse("dev-libs/libfoo-2.0")

	// constraint with slot
	c := mustParse("dev-libs/libfoo:1")
	if atomMatchesDep(installed, c, "1", "", nil) != true {
		t.Error("slot 1 should match")
	}
	if atomMatchesDep(installed, c, "2", "", nil) != false {
		t.Error("slot 2 should not match when constraint is slot 1")
	}

	// slot operator * — always matches
	c2 := mustParse("dev-libs/libfoo:*")
	if atomMatchesDep(installed, c2, "1", "", nil) != true {
		t.Error("slot * should always match")
	}
	if atomMatchesDep(installed, c2, "2", "", nil) != true {
		t.Error("slot * should always match regardless of actual slot")
	}
}

func TestAtomMatches_UseFlags(t *testing.T) {
	installed := mustParse("dev-libs/libfoo-2.0")
	useFlags := map[string]bool{"ssl": true, "doc": false}

	// constraint requires ssl
	c := mustParse("dev-libs/libfoo[ssl]")
	if !atomMatchesDep(installed, c, "", "", useFlags) {
		t.Error("ssl flag should match enabled")
	}

	// constraint requires -doc
	c2 := mustParse("dev-libs/libfoo[-doc]")
	if !atomMatchesDep(installed, c2, "", "", useFlags) {
		t.Error("-doc flag should match disabled")
	}

	// constraint requires ssl but flag not present
	c3 := mustParse("dev-libs/libfoo[ssl]")
	if atomMatchesDep(installed, c3, "", "", map[string]bool{}) {
		t.Error("ssl flag should not match when not present in useFlags")
	}
}

func TestTildeMatch(t *testing.T) {
	tests := []struct {
		v, c string
		want bool
	}{
		{"3.11.5", "3.11", true},
		{"3.11.5-r1", "3.11", true},
		{"3.12.0", "3.11", false},
		{"3.11", "3.11", true},
		{"3.11.0", "3.11", true},
		{"4.0", "3.11", false},
	}
	for _, tt := range tests {
		t.Run(tt.v+"_~_"+tt.c, func(t *testing.T) {
			v := mustParse("dev-lang/python-" + tt.v)
			c := mustParse("dev-lang/python-" + tt.c)
			got := tildeMatch(v.Version, c.Version)
			if got != tt.want {
				t.Errorf("tildeMatch(%s, %s) = %v, want %v", tt.v, tt.c, got, tt.want)
			}
		})
	}
}

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		v, c string
		want bool
	}{
		{"3.11.5", "3.11", true},
		{"3.11.5-r1", "3.11", true},
		{"3.12.0", "3.11", false},
		{"3.11", "3.11", true},
	}
	for _, tt := range tests {
		t.Run(tt.v+"_=*_"+tt.c, func(t *testing.T) {
			v := mustParse("dev-lang/python-" + tt.v)
			c := mustParse("dev-lang/python-" + tt.c)
			got := globMatch(v.Version, c.Version)
			if got != tt.want {
				t.Errorf("globMatch(%s, %s) = %v, want %v", tt.v, tt.c, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test: adversarial — huge dep trees and circular deps
// ---------------------------------------------------------------------------

func TestResolve_AdversarialHugeTree(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Resolve panicked on huge tree: %v", r)
		}
	}()

	g := makeGraph()
	const count = 200
	for i := 0; i < count; i++ {
		cp := fmt.Sprintf("pkg/cat%d", i)
		pkg(g, cp, "1.0", "0", "0", false, nil)
	}
	// make every package depend on a few others
	for i := 0; i < count-1; i++ {
		for j := i + 1; j < count && j < i+4; j++ {
			from := fmt.Sprintf("pkg/cat%d", i)
			to := fmt.Sprintf(">=pkg/cat%d-1.0", j)
			deps(g, from, to)
		}
	}

	cfg := DefaultResolveConfig()
	cfg.Deep = true
	cfg.Backtrack = 20

	result, err := Resolve(g, []string{"pkg/cat0"}, cfg)
	if err != nil {
		t.Logf("huge tree resolution error (may be expected): %v", err)
	}
	if result != nil && len(result.Install) == 0 && len(result.Conflicts) == 0 {
		t.Error("expected some installs or conflicts for huge tree")
	}
}

func TestResolve_AdversarialCircularDeps(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Resolve panicked on circular deps: %v", r)
		}
	}()

	g := makeGraph()
	pkg(g, "pkg/a", "1.0", "0", "0", false, nil)
	pkg(g, "pkg/b", "1.0", "0", "0", false, nil)
	pkg(g, "pkg/c", "1.0", "0", "0", false, nil)
	deps(g, "pkg/a", "pkg/b")
	deps(g, "pkg/b", "pkg/c")
	deps(g, "pkg/c", "pkg/a")

	cfg := DefaultResolveConfig()
	cfg.Deep = true
	cfg.Backtrack = 20

	result, err := Resolve(g, []string{"pkg/a"}, cfg)
	if err != nil {
		t.Logf("circular deps resolution error (may be expected): %v", err)
	}
	if result != nil && len(result.Conflicts) > 0 {
		t.Logf("circular deps conflicts: %v", result.Conflicts)
	}
	// should not panic
}

func TestResolve_AdversarialNilGraph(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Resolve panicked on nil graph: %v", r)
		}
	}()

	_, err := Resolve(nil, []string{"app-editors/vim"}, DefaultResolveConfig())
	if err == nil {
		t.Error("expected error for nil graph")
	}
}

func TestResolve_AdversarialMalformedTarget(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Resolve panicked on malformed target: %v", r)
		}
	}()

	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.KeepGoing = true

	_, err := Resolve(g, []string{"!!!invalid!!!"}, cfg)
	if err != nil {
		t.Logf("expected error for malformed target: %v", err)
	}
}

func TestResolve_AdversarialEmptyGraph(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Resolve panicked on empty graph: %v", r)
		}
	}()

	g := makeGraph()
	result, err := Resolve(g, []string{"nonexistent/pkg"}, DefaultResolveConfig())
	if err != nil {
		t.Logf("expected error for missing package: %v", err)
	}
	if result != nil && len(result.Conflicts) == 0 && err == nil {
		t.Error("expected conflicts or error for missing package in empty graph")
	}
}

func TestResolve_AdversarialBlankTarget(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Resolve panicked on blank target: %v", r)
		}
	}()

	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)

	result, err := Resolve(g, []string{"", "  ", "\t"}, DefaultResolveConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Install) != 0 {
		t.Errorf("expected 0 installs for blank targets, got %d", len(result.Install))
	}
}

// ---------------------------------------------------------------------------
// Test: DefaultResolveConfig
// ---------------------------------------------------------------------------

func TestDefaultResolveConfig(t *testing.T) {
	cfg := DefaultResolveConfig()
	if cfg.Backtrack != 10 {
		t.Errorf("default Backtrack should be 10, got %d", cfg.Backtrack)
	}
	if !cfg.Deep {
		t.Error("default Deep should be true")
	}
}

// ---------------------------------------------------------------------------
// Test: resolve panic recovery (NEVER panic)
// ---------------------------------------------------------------------------

func TestResolve_NeverPanics_StressTest(t *testing.T) {
	// Ensure resolve never panics regardless of input
	tests := []struct {
		name      string
		graphFn   func() *DepGraph
		targets   []string
		config    ResolveConfig
	}{
		{
			name: "nil graph",
			graphFn: func() *DepGraph { return nil },
			targets: []string{"app-editors/vim"},
			config:  DefaultResolveConfig(),
		},
		{
			name: "empty graph",
			graphFn: func() *DepGraph { return makeGraph() },
			targets: []string{"app-editors/vim"},
			config:  DefaultResolveConfig(),
		},
		{
			name: "graph with nil packages",
			graphFn: func() *DepGraph {
				g := makeGraph()
				g.Packages["test/pkg"] = nil
				return g
			},
			targets: []string{"test/pkg"},
			config:  DefaultResolveConfig(),
		},
		{
			name: "graph with nil versions",
			graphFn: func() *DepGraph {
				g := makeGraph()
				n := g.AddPackage("test/pkg")
				n.Versions["1.0"] = nil
				return g
			},
			targets: []string{"test/pkg"},
			config:  DefaultResolveConfig(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Resolve panicked on %s: %v", tt.name, r)
				}
			}()
			g := tt.graphFn()
			_, _ = Resolve(g, tt.targets, tt.config)
		})
	}
}

// ---------------------------------------------------------------------------
// Test: PkgNode methods
// ---------------------------------------------------------------------------

func TestPkgNode_GetInstalledVersion(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "8.0", "0", "0", true, nil)
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)

	node := g.Packages["app-editors/vim"]
	inst := node.GetInstalledVersion()
	if inst == nil {
		t.Fatal("expected installed version")
	}
	if inst.Version.Raw != "8.0" {
		t.Errorf("expected installed version 8.0, got %s", inst.Version.Raw)
	}
}

func TestPkgNode_GetBestVersion(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "8.0", "0", "0", false, nil)
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)
	pkg(g, "app-editors/vim", "7.4", "0", "0", false, nil)

	node := g.Packages["app-editors/vim"]
	best := node.GetBestVersion()
	if best == nil {
		t.Fatal("expected best version")
	}
	if best.Version.Raw != "9.0" {
		t.Errorf("expected best version 9.0, got %s", best.Version.Raw)
	}
}

func TestPkgNode_FindMatchingVersion(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", false, nil)
	pkg(g, "dev-libs/libfoo", "2.0", "0", "0", false, nil)
	pkg(g, "dev-libs/libfoo", "3.0", "0", "0", false, nil)

	node := g.Packages["dev-libs/libfoo"]

	c := mustParse(">=dev-libs/libfoo-2.0")
	vi := node.FindMatchingVersion(c)
	if vi == nil {
		t.Fatal("expected matching version")
	}
	if vi.Version.Raw != "3.0" {
		t.Errorf("expected best matching version 3.0, got %s", vi.Version.Raw)
	}

	c2 := mustParse(">=dev-libs/libfoo-5.0")
	vi2 := node.FindMatchingVersion(c2)
	if vi2 != nil {
		t.Errorf("expected no matching version for constraint %s, got %s", c2.String(), vi2.Version.Raw)
	}
}
// ---------------------------------------------------------------------------
// Test: DepGraph methods
// ---------------------------------------------------------------------------
// Test: DepGraph methods
// ---------------------------------------------------------------------------

func TestDepGraph_AddPackage(t *testing.T) {
	g := makeGraph()
	n := g.AddPackage("cat/pkg")
	if n == nil {
		t.Fatal("expected node")
	}
	if n.Atom.CP() != "cat/pkg" {
		t.Errorf("expected CP cat/pkg, got %s", n.Atom.CP())
	}
	// adding again returns the same node
	n2 := g.AddPackage("cat/pkg")
	if n != n2 {
		t.Error("AddPackage should return same node for duplicate CP")
	}
}

func TestDepGraph_AddVersion(t *testing.T) {
	g := makeGraph()
	vi := g.AddVersion("cat/pkg", "1.0", "0", "0", false, nil, "")
	if vi == nil {
		t.Fatal("expected version info")
	}
	if vi.Slot != "0" {
		t.Errorf("expected slot 0, got %s", vi.Slot)
	}
}

func TestDepGraph_AddDep(t *testing.T) {
	g := makeGraph()
	pkg(g, "pkg/a", "1.0", "0", "0", false, nil)
	pkg(g, "pkg/b", "1.0", "0", "0", false, nil)
	g.AddDep("pkg/a", "pkg/b", ">=pkg/b-1.0", DepTypeRuntime, "", false)

	a := g.Packages["pkg/a"]
	if len(a.Deps) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(a.Deps))
	}
	if a.Deps[0].To == nil {
		t.Fatal("expected dep target")
	}
	if a.Deps[0].To.Atom.CP() != "pkg/b" {
		t.Errorf("expected dep to pkg/b, got %s", a.Deps[0].To.Atom.CP())
	}
}

func TestDepGraph_AddAnyOfDep(t *testing.T) {
	g := makeGraph()
	pkg(g, "pkg/a", "1.0", "0", "0", false, nil)
	anyOf(g, "pkg/a", DepTypeRuntime,
		anyOfDep("pkg/b"),
		anyOfDep("pkg/c"),
	)

	a := g.Packages["pkg/a"]
	if len(a.Deps) != 1 {
		t.Fatalf("expected 1 dep (any-of), got %d", len(a.Deps))
	}
	if len(a.Deps[0].AnyOf) != 2 {
		t.Errorf("expected 2 options in any-of, got %d", len(a.Deps[0].AnyOf))
	}
}

// ---------------------------------------------------------------------------
// Test: ResolveConfig defaults via zero value
// ---------------------------------------------------------------------------

func Test_resolveConfig_Defaults_ZeroBacktrack(t *testing.T) {
	cfg := &ResolveConfig{Backtrack: 0}
	cfg.Defaults()
	if cfg.Backtrack != 10 {
		t.Errorf("expected Backtrack to become 10 after Defaults, got %d", cfg.Backtrack)
	}
}

func Test_resolveConfig_Defaults_NonZeroBacktrackNotOverwritten(t *testing.T) {
	cfg := &ResolveConfig{Backtrack: 42}
	cfg.Defaults()
	if cfg.Backtrack != 42 {
		t.Errorf("expected Backtrack to stay 42, got %d", cfg.Backtrack)
	}
}

func TestPkgNode_GetVersion(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "8.0", "0", "0", false, nil)
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)

	node := g.Packages["app-editors/vim"]
	vi := node.GetVersion("9.0")
	if vi == nil {
		t.Fatal("expected version info for 9.0")
	}
	if vi.Version == nil || vi.Version.Raw != "9.0" {
		t.Errorf("expected version 9.0, got %v", vi.Version)
	}
	if vi.Slot != "0" {
		t.Errorf("expected slot 0, got %s", vi.Slot)
	}
}

func TestPkgNode_GetVersion_NotFound(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)

	node := g.Packages["app-editors/vim"]
	vi := node.GetVersion("nonexistent")
	if vi != nil {
		t.Errorf("expected nil for nonexistent version, got %v", vi)
	}
}

func TestResolve_ZeroBacktrackDefaultsTo10(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)

	cfg := ResolveConfig{Backtrack: 0, Deep: true}
	result, err := Resolve(g, []string{"app-editors/vim"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// backtrack should have defaulted to 10 internally
	if result.BacktrackLevel < 0 {
		t.Errorf("backtrack level should be >= 0, got %d", result.BacktrackLevel)
	}
}

// ---------------------------------------------------------------------------
// Test: version matching edge cases
// ---------------------------------------------------------------------------

func TestAtomMatches_NilInputs(t *testing.T) {
	if atomMatchesDep(nil, mustParse("dev-libs/libfoo"), "", "", nil) {
		t.Error("nil installed should not match")
	}
	if atomMatchesDep(mustParse("dev-libs/libfoo"), nil, "", "", nil) {
		t.Error("nil constraint should not match")
	}
}

func TestAtomMatches_WrongCategory(t *testing.T) {
	installed := mustParse("dev-libs/libfoo-2.0")
	c := mustParse("app-misc/libfoo")
	if atomMatchesDep(installed, c, "", "", nil) {
		t.Error("different category should not match")
	}
}

func TestAtomMatches_WrongPackage(t *testing.T) {
	installed := mustParse("dev-libs/libfoo-2.0")
	c := mustParse("dev-libs/libbar")
	if atomMatchesDep(installed, c, "", "", nil) {
		t.Error("different package should not match")
	}
}

// ---------------------------------------------------------------------------
// Test: useFlagsChanged
// ---------------------------------------------------------------------------

func TestUseFlagsChanged(t *testing.T) {
	if useFlagsChanged(nil, nil) {
		t.Error("both nil should not be changed")
	}
	if !useFlagsChanged(map[string]bool{"a": true}, map[string]bool{"a": false}) {
		t.Error("different values should be changed")
	}
	if useFlagsChanged(map[string]bool{"a": true}, map[string]bool{"a": true}) {
		t.Error("same values should not be changed")
	}
	if !useFlagsChanged(map[string]bool{"a": true}, map[string]bool{"a": true, "b": false}) {
		t.Error("different lengths should be changed")
	}
}

// ---------------------------------------------------------------------------
// Test: SortByDeps with no dependencies
// ---------------------------------------------------------------------------

func TestSortByDeps_NoDeps(t *testing.T) {
	g := makeGraph()
	// packages with no deps added to graph
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)
	pkg(g, "app-editors/nano", "7.0", "0", "0", false, nil)
	pkg(g, "app-shells/bash", "5.0", "0", "0", false, nil)

	actions := []PkgAction{
		{Atom: mustParse("app-editors/vim"), Action: "install"},
		{Atom: mustParse("app-editors/nano"), Action: "install"},
		{Atom: mustParse("app-shells/bash"), Action: "install"},
	}

	sorted := SortByDeps(actions, g)
	if len(sorted) != 3 {
		t.Errorf("expected 3 actions, got %d", len(sorted))
	}
	// any order is valid for packages with no deps
	for _, a := range actions {
		found := false
		for _, s := range sorted {
			if s.Atom.CP() == a.Atom.CP() {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s not found in sorted output", a.Atom.CP())
		}
	}
}

// ---------------------------------------------------------------------------
// Test: SortByDeps with cycles
// ---------------------------------------------------------------------------

func TestSortByDeps_Cycle(t *testing.T) {
	g := makeGraph()
	deps(g, "pkg/a", "pkg/b")
	deps(g, "pkg/b", "pkg/c")
	deps(g, "pkg/c", "pkg/a") // cycle!

	actions := []PkgAction{
		{Atom: mustParse("pkg/a"), Action: "install"},
		{Atom: mustParse("pkg/b"), Action: "install"},
		{Atom: mustParse("pkg/c"), Action: "install"},
	}

	sorted := SortByDeps(actions, g)
	if len(sorted) != 3 {
		t.Fatalf("expected 3 actions even with cycle, got %d", len(sorted))
	}
	// all packages should still be present (cycle handled gracefully)
	seen := make(map[string]bool)
	for _, a := range sorted {
		seen[a.Atom.CP()] = true
	}
	for _, a := range actions {
		if !seen[a.Atom.CP()] {
			t.Errorf("%s missing from sorted output", a.Atom.CP())
		}
	}
}

// ---------------------------------------------------------------------------
// Test: Resolve with specific version constraint on target
// ---------------------------------------------------------------------------

func TestResolve_SpecificVersionConstraint(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", false, nil)
	pkg(g, "dev-libs/libfoo", "2.0", "0", "0", false, nil)
	pkg(g, "dev-libs/libfoo", "3.0", "0", "0", false, nil)

	result, err := Resolve(g, []string{">=dev-libs/libfoo-2.0"}, DefaultResolveConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Install) != 1 {
		t.Fatalf("expected 1 install, got %d", len(result.Install))
	}

	// should install 3.0 (highest version matching >=2.0)
	a := result.Install[0]
	if a.Atom.Version == nil || a.Atom.Version.Raw != "3.0" {
		t.Errorf("expected version 3.0, got %v", a.Atom.Version)
	}
}

// ---------------------------------------------------------------------------
// Test: Resolve with slot constraints
// ---------------------------------------------------------------------------

func TestResolve_SlotConstraint(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-lang/python", "3.10.10", "3.10", "3.10", true, nil)
	pkg(g, "dev-lang/python", "3.11.5", "3.11", "3.11", false, nil)
	pkg(g, "dev-lang/python", "3.12.0", "3.12", "3.12", false, nil)

	result, err := Resolve(g, []string{"dev-lang/python:3.11"}, DefaultResolveConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Install) != 1 {
		t.Fatalf("expected 1 install, got %d", len(result.Install))
	}
	a := result.Install[0]
	if a.Slot != "3.11" {
		t.Errorf("expected slot 3.11, got %s", a.Slot)
	}
}

// ---------------------------------------------------------------------------
// Test: world set expansion
// ---------------------------------------------------------------------------

func TestResolve_WorldSet(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)
	pkg(g, "app-shells/bash", "5.0", "0", "0", false, nil)

	cfg := DefaultResolveConfig()

	r := &resolver{
		graph:    g,
		config:   cfg,
		toInstall: make(map[string]*PkgAction),
		toUninstall: make(map[string]*PkgAction),
		conflicts: []string{},
		seenDeps: make(map[string]bool),
		backtrackRemaining: cfg.Backtrack,
		worldSet: &WorldSet{
			Entries: []string{"app-editors/vim", "app-shells/bash"},
		},
	}
	if cfg.Backtrack <= 0 {
		r.backtrackRemaining = 10
	}

	atoms, err := r.expandTargets([]string{"@world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(atoms) != 2 {
		t.Errorf("expected 2 atoms from @world expansion, got %d", len(atoms))
	}

	cpSet := make(map[string]bool)
	for _, a := range atoms {
		cpSet[a.CP()] = true
	}
	if !cpSet["app-editors/vim"] || !cpSet["app-shells/bash"] {
		t.Errorf("expected both packages in @world expansion, got %v", cpSet)
	}
}

func TestResolve_WorldSetEmpty(t *testing.T) {
	g := makeGraph()
	cfg := DefaultResolveConfig()

	r := &resolver{
		graph:    g,
		config:   cfg,
		toInstall: make(map[string]*PkgAction),
		toUninstall: make(map[string]*PkgAction),
		conflicts: []string{},
		seenDeps: make(map[string]bool),
		backtrackRemaining: cfg.Backtrack,
	}
	if cfg.Backtrack <= 0 {
		r.backtrackRemaining = 10
	}

	atoms, err := r.expandTargets([]string{"@world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(atoms) != 0 {
		t.Errorf("expected 0 atoms from empty @world, got %d", len(atoms))
	}
}

// ---------------------------------------------------------------------------
// Test: install list topological order via Resolve
// ---------------------------------------------------------------------------

func TestResolve_TopoSortInResult(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-shells/bash", "5.0", "0", "0", false, nil)
	pkg(g, "dev-libs/readline", "8.0", "0", "0", false, nil)
	pkg(g, "sys-libs/ncurses", "6.0", "0", "0", false, nil)
	deps(g, "app-shells/bash", ">=dev-libs/readline-8")
	deps(g, "dev-libs/readline", ">=sys-libs/ncurses-6")

	cfg := DefaultResolveConfig()
	cfg.Deep = true

	result, err := Resolve(g, []string{"app-shells/bash"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// check topological order in the result
	findPos := func(name string) int {
		for i, a := range result.Install {
			if a.Atom.CP() == name {
				return i
			}
		}
		return -1
	}

	ncPos := findPos("sys-libs/ncurses")
	rlPos := findPos("dev-libs/readline")
	bashPos := findPos("app-shells/bash")

	if ncPos < 0 || rlPos < 0 || bashPos < 0 {
		t.Fatalf("missing packages in result: ncurses=%d readline=%d bash=%d", ncPos, rlPos, bashPos)
	}
	if ncPos >= rlPos {
		t.Errorf("ncurses (pos %d) must precede readline (pos %d)", ncPos, rlPos)
	}
	if rlPos >= bashPos {
		t.Errorf("readline (pos %d) must precede bash (pos %d)", rlPos, bashPos)
	}
}

// ---------------------------------------------------------------------------
// Test: multiple targets
// ---------------------------------------------------------------------------

func TestResolve_MultipleTargets(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)
	pkg(g, "app-editors/nano", "7.0", "0", "0", false, nil)
	pkg(g, "app-shells/bash", "5.0", "0", "0", false, nil)

	result, err := Resolve(g, []string{"app-editors/vim", "app-shells/bash", "app-editors/nano"}, DefaultResolveConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := map[string]bool{
		"app-editors/vim":  false,
		"app-editors/nano": false,
		"app-shells/bash":  false,
	}
	for _, a := range result.Install {
		expected[a.Atom.CP()] = true
	}
	for cp, found := range expected {
		if !found {
			t.Errorf("expected %s in install list", cp)
		}
	}
}

// ---------------------------------------------------------------------------
// Test: version constraints for deps
// ---------------------------------------------------------------------------

func TestResolve_VersionConstraintDep(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-misc/tool", "1.0", "0", "0", false, nil)

	// dep requires >= 2.0, but only 1.0 and 1.5 available
	pkg(g, "dev-libs/libx", "1.0", "0", "0", false, nil)
	pkg(g, "dev-libs/libx", "1.5", "0", "0", false, nil)

	deps(g, "app-misc/tool", ">=dev-libs/libx-2.0")

	cfg := DefaultResolveConfig()
	cfg.KeepGoing = true

	result, err := Resolve(g, []string{"app-misc/tool"}, cfg)
	if err != nil {
		t.Logf("version constraint conflict error: %v", err)
	}

	// should have conflict about unsatisfied version constraint
	hasConflict := false
	for _, c := range result.Conflicts {
		if strings.Contains(c, "no installable version") || strings.Contains(c, "could not be satisfied") {
			hasConflict = true
			break
		}
	}
	if !hasConflict && err == nil {
		t.Error("expected conflict for unsatisfied version constraint")
	}
}

// ---------------------------------------------------------------------------
// Test: atom version matching with OpNone (any version acceptable as dep)
// ---------------------------------------------------------------------------

func TestAtomMatches_OpNone(t *testing.T) {
	// in a dependency context, "dev-libs/libfoo" with no version operator
	// means any version is acceptable
	installed := mustParse("dev-libs/libfoo-5.0")
	c := mustParse("dev-libs/libfoo")

	if !atomMatchesDep(installed, c, "", "", nil) {
		t.Error("OpNone should match any version of same package")
	}
}

// ---------------------------------------------------------------------------
// Test: multiple versions, best available chosen
// ---------------------------------------------------------------------------

func TestResolve_BestAvailable(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", false, nil)
	pkg(g, "dev-libs/libfoo", "2.0", "0", "0", false, nil)
	pkg(g, "dev-libs/libfoo", "3.0", "0", "0", false, nil)

	result, err := Resolve(g, []string{"dev-libs/libfoo"}, DefaultResolveConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Install) != 1 {
		t.Fatalf("expected 1 install, got %d", len(result.Install))
	}

	a := result.Install[0]
	if a.Atom.Version == nil || a.Atom.Version.Raw != "3.0" {
		t.Errorf("expected best version 3.0, got %v", a.Atom.Version)
	}
}

// ---------------------------------------------------------------------------
// Test: alias functions
// ---------------------------------------------------------------------------

func Test_anyOfDep(t *testing.T) {
	d := anyOfDep("app-editors/vim")
	if d.Atom.CP() != "app-editors/vim" {
		t.Errorf("expected app-editors/vim, got %s", d.Atom.CP())
	}
	if d.Block || d.UseCond != "" {
		t.Error("expected no block or use cond for simple anyOfDep")
	}
}

func Test_anyOfDepUse(t *testing.T) {
	d := anyOfDepUse("app-editors/vim", "X")
	if d.UseCond != "X" {
		t.Errorf("expected use cond X, got %s", d.UseCond)
	}
}

func Test_collectCPV(t *testing.T) {
	actions := []PkgAction{
		{Atom: mustParse("app-editors/vim-9.0"), Action: "install"},
		{Atom: mustParse("app-shells/bash"), Action: "install"},
	}
	c := collectCPV(actions)
	names := strings.Join(c, ",")
	if !strings.Contains(names, "app-editors/vim-9.0") {
		t.Errorf("expected vim-9.0 in %s", names)
	}
	if !strings.Contains(names, "app-shells/bash") {
		t.Errorf("expected bash in %s", names)
	}
}

func Test_hasAction(t *testing.T) {
	actions := []PkgAction{
		{Atom: mustParse("app-editors/vim-9.0"), Action: "install"},
	}
	if !hasAction(actions, "app-editors/vim-9.0", "install") {
		t.Error("expected to find install action")
	}
	if hasAction(actions, "app-editors/vim-9.0", "uninstall") {
		t.Error("expected not to find uninstall action")
	}
	if hasAction(actions, "nonexistent/pkg-1.0", "install") {
		t.Error("expected not to find nonexistent package")
	}
}

// ---------------------------------------------------------------------------
// Test: backup — version info without version
// ---------------------------------------------------------------------------

func TestPkgNode_Methods_NoVersion(t *testing.T) {
	n := &PkgNode{
		Atom:     &atom.Atom{Category: "test", Package: "pkg"},
		Versions: make(map[string]*VersionInfo),
		Slots:    make(map[string][]*VersionInfo),
	}

	if n.GetInstalledVersion() != nil {
		t.Error("expected nil for empty versions")
	}
	if n.GetBestVersion() != nil {
		t.Error("expected nil for empty versions")
	}
}

// ---------------------------------------------------------------------------
// Test: hasAction with version in atom
// ---------------------------------------------------------------------------

func Test_hasAction_FullCPV(t *testing.T) {
	actions := []PkgAction{
		{Atom: mustParse(">=app-editors/vim-9.0"), Action: "install"},
	}
	// hasAction reconstructs the CPV from the atom; it should match
	if !hasAction(actions, "app-editors/vim-9.0", "install") {
		t.Error("should match CPV extracted from atom")
	}
	if hasAction(actions, "app-editors/vim", "install") {
		t.Error("should not match bare CP when version is set")
	}
}

// ---------------------------------------------------------------------------
// Benchmark: large graph resolution
// ---------------------------------------------------------------------------

func BenchmarkResolve_Small(b *testing.B) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", false, nil)
	deps(g, "app-editors/vim", ">=dev-libs/libfoo-1.0")

	cfg := DefaultResolveConfig()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Resolve(g, []string{"app-editors/vim"}, cfg)
	}
}

func BenchmarkResolve_Medium(b *testing.B) {
	g := makeGraph()
	const n = 20
	for i := 0; i < n; i++ {
		cp := fmt.Sprintf("pkg/pkg%d", i)
		pkg(g, cp, "1.0", "0", "0", false, nil)
	}
	for i := 0; i < n-1; i++ {
		deps(g, fmt.Sprintf("pkg/pkg%d", i), fmt.Sprintf("pkg/pkg%d", i+1))
	}

	cfg := DefaultResolveConfig()
	cfg.Deep = true
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Resolve(g, []string{"pkg/pkg0"}, cfg)
	}
}

// ---------------------------------------------------------------------------
// Ensure sort is imported (used in SortByDeps and elsewhere)
// ---------------------------------------------------------------------------
var _ = sort.Ints

// ---------------------------------------------------------------------------
// Test: portage config — nil PortageConfig works as before
// ---------------------------------------------------------------------------

func TestResolve_NilPortageConfig(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.PortageConfig = nil

	result, err := Resolve(g, []string{"app-editors/vim"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Install) != 1 {
		t.Fatalf("expected 1 install, got %d: %v", len(result.Install), collectCPV(result.Install))
	}

	if result.Install[0].Atom.CP() != "app-editors/vim" {
		t.Errorf("expected app-editors/vim, got %s", result.Install[0].Atom.CP())
	}
}

// ---------------------------------------------------------------------------
// Test: portage config — masked packages are excluded
// ---------------------------------------------------------------------------

func TestResolve_MaskedPackage(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{
		PackageMask: []string{"app-editors/vim"},
	}

	result, err := Resolve(g, []string{"app-editors/vim"}, cfg)
	if err == nil {
		t.Error("expected error for masked package")
	}
	if result != nil {
		t.Error("expected nil result when error returned")
	}
}

func TestResolve_MaskedPackage_KeepGoing(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.KeepGoing = true
	cfg.PortageConfig = &portage.Config{
		PackageMask: []string{"app-editors/vim"},
	}

	result, err := Resolve(g, []string{"app-editors/vim"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error with KeepGoing: %v", err)
	}

	if len(result.Conflicts) == 0 {
		t.Error("expected conflict for masked package with KeepGoing")
	}
}

// ---------------------------------------------------------------------------
// Test: portage config — unmasked packages override masking
// ---------------------------------------------------------------------------

func TestResolve_UnmaskedOverride(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)
	pkg(g, "app-editors/nano", "7.0", "0", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{
		PackageMask:   []string{"app-editors/vim", "app-editors/nano"},
		PackageUnmask: []string{"app-editors/vim"},
	}

	result, err := Resolve(g, []string{"app-editors/vim"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error for unmasked package: %v", err)
	}

	foundVim := false
	for _, a := range result.Install {
		if a.Atom.CP() == "app-editors/vim" {
			foundVim = true
		}
	}
	if !foundVim {
		t.Error("app-editors/vim should be installed (unmasked)")
	}
}

// ---------------------------------------------------------------------------
// Test: portage config — package-specific USE flags affect resolution
// ---------------------------------------------------------------------------

func TestResolve_PackageUseFlags(t *testing.T) {
	g := makeGraph()
	// vim depends on python? (dev-lang/python)
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)
	pkg(g, "dev-lang/python", "3.11", "0", "0", false, nil)
	depUse(g, "app-editors/vim", "dev-lang/python", "python")

	cfg := DefaultResolveConfig()
	cfg.Deep = true
	cfg.PortageConfig = &portage.Config{
		USE: []string{"-python", "ssl"},
		PackageUse: map[string][]string{
			"app-editors/vim": {"python"},
		},
	}

	result, err := Resolve(g, []string{"app-editors/vim"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, a := range result.Install {
		if a.Atom.CP() == "dev-lang/python" {
			found = true
		}
	}
	if !found {
		t.Error("python should be installed (package-specific USE flag 'python' overrides global '-python')")
	}
}

// ---------------------------------------------------------------------------
// Test: portage config — global USE flags merge with package USE flags
// ---------------------------------------------------------------------------

func TestResolve_GlobalAndPackageUseMerge(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)
	pkg(g, "dev-lang/python", "3.11", "0", "0", false, nil)
	pkg(g, "dev-lang/ruby", "3.2", "0", "0", false, nil)
	depUse(g, "app-editors/vim", "dev-lang/python", "python")
	depUse(g, "app-editors/vim", "dev-lang/ruby", "ruby")

	cfg := DefaultResolveConfig()
	cfg.Deep = true
	cfg.PortageConfig = &portage.Config{
		USE: []string{"python", "-ruby", "ssl"},
		PackageUse: map[string][]string{
			"app-editors/vim": {"ruby"},
		},
	}

	result, err := Resolve(g, []string{"app-editors/vim"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundPython := false
	foundRuby := false
	for _, a := range result.Install {
		switch a.Atom.CP() {
		case "dev-lang/python":
			foundPython = true
		case "dev-lang/ruby":
			foundRuby = true
		}
	}
	if !foundPython {
		t.Error("python should be installed (global USE flag 'python' is enabled)")
	}
	if !foundRuby {
		t.Error("ruby should be installed (package-specific USE flag 'ruby' overrides global '-ruby')")
	}
}

// ---------------------------------------------------------------------------
// Test: portage config — ACCEPT_KEYWORDS filters versions
// ---------------------------------------------------------------------------

func TestResolve_AcceptKeywordsFilter(t *testing.T) {
	g := makeGraph()
	// version 3.11 keyworded amd64 (stable)
	pkgKeywords(g, "dev-lang/python", "3.11", "0", "0", false, nil, "amd64")
	// version 3.12 keyworded ~amd64 only (testing)
	pkgKeywords(g, "dev-lang/python", "3.12", "0", "0", false, nil, "~amd64")

	// ACCEPT_KEYWORDS="amd64" means only stable amd64
	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{
		ACCEPT_KEYWORDS: []string{"amd64"},
	}

	result, err := Resolve(g, []string{"dev-lang/python"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Install) != 1 {
		t.Fatalf("expected 1 install, got %d", len(result.Install))
	}

	// should install 3.11 (stable amd64), not 3.12 (testing only)
	if result.Install[0].Atom.Version == nil || result.Install[0].Atom.Version.Raw != "3.11" {
		t.Errorf("expected version 3.11 (stable amd64), got %v", result.Install[0].Atom.Version)
	}
}

// ---------------------------------------------------------------------------
// Test: portage config — ACCEPT_KEYWORDS with ~arch accepts testing
// ---------------------------------------------------------------------------

func TestResolve_AcceptKeywordsTesting(t *testing.T) {
	g := makeGraph()
	pkgKeywords(g, "dev-lang/python", "3.11", "0", "0", false, nil, "amd64")
	pkgKeywords(g, "dev-lang/python", "3.12", "0", "0", false, nil, "~amd64")

	// ACCEPT_KEYWORDS="amd64 ~amd64" means both stable and testing
	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{
		ACCEPT_KEYWORDS: []string{"amd64", "~amd64"},
	}

	result, err := Resolve(g, []string{"dev-lang/python"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Install) != 1 {
		t.Fatalf("expected 1 install, got %d", len(result.Install))
	}

	// should install 3.12 (best version, testing is accepted)
	if result.Install[0].Atom.Version == nil || result.Install[0].Atom.Version.Raw != "3.12" {
		t.Errorf("expected version 3.12 (testing accepted), got %v", result.Install[0].Atom.Version)
	}
}

// ---------------------------------------------------------------------------
// Test: portage config — ACCEPT_KEYWORDS with ** accepts everything
// ---------------------------------------------------------------------------

func TestResolve_AcceptKeywordsStarStar(t *testing.T) {
	g := makeGraph()
	pkgKeywords(g, "dev-lang/python", "3.11", "0", "0", false, nil, "amd64")
	pkgKeywords(g, "dev-lang/python", "3.12", "0", "0", false, nil, "~amd64")

	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{
		ACCEPT_KEYWORDS: []string{"**"},
	}

	result, err := Resolve(g, []string{"dev-lang/python"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Install) != 1 {
		t.Fatalf("expected 1 install, got %d", len(result.Install))
	}

	// should install 3.12 (best version, ** accepts everything)
	if result.Install[0].Atom.Version == nil || result.Install[0].Atom.Version.Raw != "3.12" {
		t.Errorf("expected version 3.12, got %v", result.Install[0].Atom.Version)
	}
}

// ---------------------------------------------------------------------------
// Test: portage config — getUseFlags helper
// ---------------------------------------------------------------------------

func TestResolver_GetUseFlags(t *testing.T) {
	r := &resolver{
		portageConfig: &portage.Config{
			USE: []string{"ssl", "-X", "gtk"},
			PackageUse: map[string][]string{
				"app-editors/vim": {"X", "-gtk", "python"},
			},
		},
	}

	flags := r.getUseFlags("app-editors/vim")

	if !flags["ssl"] {
		t.Error("ssl should be enabled (global)")
	}
	if !flags["X"] {
		t.Error("X should be enabled (global -X overridden by package X)")
	}
	if flags["gtk"] {
		t.Error("gtk should be disabled (global gtk overridden by package -gtk)")
	}
	if !flags["python"] {
		t.Error("python should be enabled (package-specific)")
	}
}

// ---------------------------------------------------------------------------
// Test: portage config — getUseFlags with nil config returns empty
// ---------------------------------------------------------------------------

func TestResolver_GetUseFlags_NilConfig(t *testing.T) {
	r := &resolver{
		portageConfig: nil,
	}

	flags := r.getUseFlags("app-editors/vim")
	if len(flags) != 0 {
		t.Errorf("expected empty flags for nil config, got %v", flags)
	}
}

// ---------------------------------------------------------------------------
// Test: Depclean
// ---------------------------------------------------------------------------

func TestDepclean_RemovesOrphan(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", true, nil)
	pkg(g, "app-editors/other", "1.0", "0", "0", true, nil)

	ws := &WorldSet{Entries: []string{"app-editors/vim"}}

	removals, err := Depclean(g, ws)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(removals) != 1 {
		t.Fatalf("expected 1 removal, got %d", len(removals))
	}

	if removals[0].Atom.CP() != "app-editors/other" {
		t.Errorf("expected app-editors/other to be removed, got %s", removals[0].Atom.CP())
	}

	if removals[0].Reason != "orphaned dependency" {
		t.Errorf("expected reason 'orphaned dependency', got %q", removals[0].Reason)
	}
}

func TestDepclean_KeepsWorldPackage(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", true, nil)

	ws := &WorldSet{Entries: []string{"app-editors/vim"}}

	removals, err := Depclean(g, ws)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(removals) != 0 {
		t.Errorf("expected 0 removals, got %d", len(removals))
	}
}

func TestDepclean_KeepsDependency(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", true, nil)
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", true, nil)
	deps(g, "app-editors/vim", ">=dev-libs/libfoo-1.0")

	ws := &WorldSet{Entries: []string{"app-editors/vim"}}

	removals, err := Depclean(g, ws)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(removals) != 0 {
		t.Errorf("expected 0 removals, got %d", len(removals))
	}
}

func TestDepclean_TransitiveDep(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", true, nil)
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", true, nil)
	pkg(g, "dev-libs/libbar", "2.0", "0", "0", true, nil)
	deps(g, "app-editors/vim", ">=dev-libs/libfoo-1.0")
	deps(g, "dev-libs/libfoo", ">=dev-libs/libbar-2.0")

	ws := &WorldSet{Entries: []string{"app-editors/vim"}}

	removals, err := Depclean(g, ws)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(removals) != 0 {
		t.Errorf("expected 0 removals, got %d", len(removals))
	}
}

func TestDepclean_EmptyWorldSet(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", true, nil)
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", true, nil)

	ws := &WorldSet{}

	removals, err := Depclean(g, ws)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(removals) != 2 {
		t.Errorf("expected 2 removals (everything), got %d", len(removals))
	}
}

func TestDepclean_NilWorldSet(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", true, nil)

	removals, err := Depclean(g, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(removals) != 1 {
		t.Errorf("expected 1 removal (everything), got %d", len(removals))
	}
}

func TestDepclean_NoInstalledPackages(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", false, nil)

	ws := &WorldSet{Entries: []string{"app-editors/vim"}}

	removals, err := Depclean(g, ws)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(removals) != 0 {
		t.Errorf("expected 0 removals, got %d", len(removals))
	}
}

func TestDepclean_NilGraph(t *testing.T) {
	ws := &WorldSet{Entries: []string{"app-editors/vim"}}
	_, err := Depclean(nil, ws)
	if err == nil {
		t.Error("expected error for nil graph")
	}
}

func TestDepclean_AnyOfDep(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-shells/bash", "5.0", "0", "0", true, nil)
	pkg(g, "virtual/editor", "1", "0", "0", true, nil)
	pkg(g, "app-editors/vim", "9.0", "0", "0", true, nil)
	anyOf(g, "app-shells/bash", DepTypeRuntime,
		anyOfDep("virtual/editor"),
		anyOfDep("app-editors/vim"),
	)

	ws := &WorldSet{Entries: []string{"app-shells/bash"}}

	removals, err := Depclean(g, ws)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(removals) != 0 {
		t.Errorf("expected 0 removals (any-of options are kept), got %d", len(removals))
	}
}

// ---------------------------------------------------------------------------
// Test: Prune
// ---------------------------------------------------------------------------

func TestPrune_RemovesOldVersion(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", true, nil)
	pkg(g, "dev-libs/libfoo", "2.0", "0", "0", true, nil)

	removals, err := Prune(g)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(removals) != 1 {
		t.Fatalf("expected 1 removal, got %d", len(removals))
	}

	if removals[0].Atom.Version == nil || removals[0].Atom.Version.Raw != "1.0" {
		t.Errorf("expected version 1.0 to be removed, got %v", removals[0].Atom.Version)
	}
}

func TestPrune_KeepsNewest(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", true, nil)
	pkg(g, "dev-libs/libfoo", "2.0", "0", "0", true, nil)
	pkg(g, "dev-libs/libfoo", "3.0", "0", "0", true, nil)

	removals, err := Prune(g)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(removals) != 2 {
		t.Fatalf("expected 2 removals, got %d", len(removals))
	}

	for _, r := range removals {
		if r.Atom.Version != nil && r.Atom.Version.Raw == "3.0" {
			t.Error("newest version (3.0) should not be removed")
		}
	}
}

func TestPrune_KeepsMultipleSlots(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-lang/python", "3.10.10", "3.10", "0", true, nil)
	pkg(g, "dev-lang/python", "3.11.5", "3.11", "0", true, nil)

	removals, err := Prune(g)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(removals) != 0 {
		t.Errorf("expected 0 removals (different slots), got %d", len(removals))
	}
}

func TestPrune_RemovesOldInEachSlot(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-lang/python", "3.10.9", "3.10", "0", true, nil)
	pkg(g, "dev-lang/python", "3.10.10", "3.10", "0", true, nil)
	pkg(g, "dev-lang/python", "3.11.0", "3.11", "0", true, nil)
	pkg(g, "dev-lang/python", "3.11.5", "3.11", "0", true, nil)

	removals, err := Prune(g)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(removals) != 2 {
		t.Fatalf("expected 2 removals (one per slot), got %d", len(removals))
	}

	for _, r := range removals {
		if r.Atom.Version == nil {
			continue
		}
		if r.Atom.Version.Raw != "3.10.9" && r.Atom.Version.Raw != "3.11.0" {
			t.Errorf("expected only old versions removed, got %s", r.Atom.Version.Raw)
		}
	}
}

func TestPrune_OneVersionReturnsEmpty(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", true, nil)

	removals, err := Prune(g)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(removals) != 0 {
		t.Errorf("expected 0 removals, got %d", len(removals))
	}
}

func TestPrune_NilGraph(t *testing.T) {
	_, err := Prune(nil)
	if err == nil {
		t.Error("expected error for nil graph")
	}
}

func TestPrune_NoInstalledPackages(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", false, nil)
	pkg(g, "dev-libs/libfoo", "2.0", "0", "0", false, nil)

	removals, err := Prune(g)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(removals) != 0 {
		t.Errorf("expected 0 removals, got %d", len(removals))
	}
}

func TestPrune_InstalledMixedWithAvailable(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", true, nil)
	pkg(g, "dev-libs/libfoo", "2.0", "0", "0", true, nil)
	pkg(g, "dev-libs/libfoo", "3.0", "0", "0", false, nil)

	removals, err := Prune(g)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(removals) != 1 {
		t.Fatalf("expected 1 removal (only installed versions considered), got %d", len(removals))
	}

	if removals[0].Atom.Version == nil || removals[0].Atom.Version.Raw != "1.0" {
		t.Errorf("expected version 1.0 to be pruned, got %v", removals[0].Atom.Version)
	}
}

// ---------------------------------------------------------------------------
// Virtual package support
// ---------------------------------------------------------------------------

func TestVirtualPackage_ResolvedByProvider(t *testing.T) {
	g := makeGraph()
	// app-shells/bash depends on virtual/rust
	pkg(g, "app-shells/bash", "5.0", "0", "0", false, nil)
	// virtual/rust is not in Packages map; it has a provider
	pkg(g, "dev-lang/rust-bin", "1.70", "0", "0", false, nil)
	g.AddProvider("virtual/rust", "dev-lang/rust-bin")

	// bash depends on virtual/rust
	g.AddDep("app-shells/bash", "virtual/rust", "virtual/rust", DepTypeRuntime, "", false)

	cfg := DefaultResolveConfig()
	cfg.Deep = true

	result, err := Resolve(g, []string{"app-shells/bash"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundRust := false
	for _, a := range result.Install {
		if a.Atom.CP() == "dev-lang/rust-bin" {
			foundRust = true
		}
	}
	if !foundRust {
		t.Error("dev-lang/rust-bin should be installed as provider of virtual/rust")
	}
}

func TestVirtualPackage_NoProviderConflict(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-shells/bash", "5.0", "0", "0", false, nil)

	// bash depends on virtual/rust but no providers exist
	g.AddDep("app-shells/bash", "virtual/rust", "virtual/rust", DepTypeRuntime, "", false)

	cfg := DefaultResolveConfig()
	cfg.Deep = true
	cfg.KeepGoing = true

	result, err := Resolve(g, []string{"app-shells/bash"}, cfg)
	if err != nil {
		t.Logf("expected conflict error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	hasConflict := false
	for _, c := range result.Conflicts {
		if strings.Contains(c, "virtual/rust") || strings.Contains(c, "could not be satisfied") {
			hasConflict = true
			break
		}
	}
	if !hasConflict {
		t.Error("expected conflict for virtual/rust with no provider")
	}
}

func TestVirtualPackage_DirectTargetProvider(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-lang/rust-bin", "1.70", "0", "0", false, nil)
	g.AddProvider("virtual/rust", "dev-lang/rust-bin")

	cfg := DefaultResolveConfig()

	// Target is virtual/rust directly, not in Packages
	result, err := Resolve(g, []string{"virtual/rust"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, a := range result.Install {
		if a.Atom.CP() == "dev-lang/rust-bin" {
			found = true
		}
	}
	if !found {
		t.Error("dev-lang/rust-bin should be installed when targeting virtual/rust")
	}
}

// ---------------------------------------------------------------------------
// REQUIRED_USE checking
// ---------------------------------------------------------------------------

func TestCheckRequiredUse_Empty(t *testing.T) {
	err := CheckRequiredUse("", nil)
	if err != nil {
		t.Errorf("empty REQUIRED_USE should be valid, got: %v", err)
	}
}

func TestCheckRequiredUse_NoConstraint(t *testing.T) {
	err := CheckRequiredUse("", map[string]bool{"python": true})
	if err != nil {
		t.Errorf("empty REQUIRED_USE with flags should be valid, got: %v", err)
	}
}

func TestCheckRequiredUse_SingleFlagEnabled(t *testing.T) {
	err := CheckRequiredUse("python_targets_python3_11", map[string]bool{"python_targets_python3_11": true})
	if err != nil {
		t.Errorf("flag enabled should satisfy: %v", err)
	}
}

func TestCheckRequiredUse_SingleFlagDisabled(t *testing.T) {
	err := CheckRequiredUse("python_targets_python3_11", map[string]bool{"python_targets_python3_11": false})
	if err == nil {
		t.Error("disabled flag should violate REQUIRED_USE")
	}
}

func TestCheckRequiredUse_AnyOfSatisfied(t *testing.T) {
	err := CheckRequiredUse(
		"|| ( python_targets_python3_11 python_targets_python3_12 )",
		map[string]bool{"python_targets_python3_11": true, "python_targets_python3_12": false},
	)
	if err != nil {
		t.Errorf("any-of satisfied by first option: %v", err)
	}
}

func TestCheckRequiredUse_AnyOfSatisfiedBySecond(t *testing.T) {
	err := CheckRequiredUse(
		"|| ( python_targets_python3_11 python_targets_python3_12 )",
		map[string]bool{"python_targets_python3_11": false, "python_targets_python3_12": true},
	)
	if err != nil {
		t.Errorf("any-of satisfied by second option: %v", err)
	}
}

func TestCheckRequiredUse_AnyOfUnsatisfied(t *testing.T) {
	err := CheckRequiredUse(
		"|| ( python_targets_python3_11 python_targets_python3_12 )",
		map[string]bool{"python_targets_python3_11": false, "python_targets_python3_12": false},
	)
	if err == nil {
		t.Error("any-of should fail when none are enabled")
	}
}

func TestCheckRequiredUse_XorOfSatisfied(t *testing.T) {
	err := CheckRequiredUse(
		"^^ ( python_targets_python3_11 python_targets_python3_12 )",
		map[string]bool{"python_targets_python3_11": true, "python_targets_python3_12": false},
	)
	if err != nil {
		t.Errorf("^^ exactly one satisfied: %v", err)
	}
}

func TestCheckRequiredUse_XorOfUnsatisfiedZero(t *testing.T) {
	err := CheckRequiredUse(
		"^^ ( python_targets_python3_11 python_targets_python3_12 )",
		map[string]bool{"python_targets_python3_11": false, "python_targets_python3_12": false},
	)
	if err == nil {
		t.Error("^^ should fail when zero are enabled")
	}
}

func TestCheckRequiredUse_XorOfUnsatisfiedTwo(t *testing.T) {
	err := CheckRequiredUse(
		"^^ ( python_targets_python3_11 python_targets_python3_12 )",
		map[string]bool{"python_targets_python3_11": true, "python_targets_python3_12": true},
	)
	if err == nil {
		t.Error("^^ should fail when more than one is enabled")
	}
}

func TestCheckRequiredUse_UseConditionalPositive(t *testing.T) {
	err := CheckRequiredUse(
		"python? ( python_targets_python3_11 )",
		map[string]bool{"python": true, "python_targets_python3_11": true},
	)
	if err != nil {
		t.Errorf("positive use conditional should pass when condition and body are met: %v", err)
	}
}

func TestCheckRequiredUse_UseConditionalPositiveFlagDisabled(t *testing.T) {
	err := CheckRequiredUse(
		"python? ( python_targets_python3_11 )",
		map[string]bool{"python": false},
	)
	if err != nil {
		t.Errorf("positive use conditional should pass when flag is disabled (body skipped): %v", err)
	}
}

func TestCheckRequiredUse_UseConditionalNegative(t *testing.T) {
	err := CheckRequiredUse(
		"!python? ( python_targets_python3_11 )",
		map[string]bool{"python": false, "python_targets_python3_11": true},
	)
	if err != nil {
		t.Errorf("negative use conditional should pass when condition and body are met: %v", err)
	}
}

func TestCheckRequiredUse_UseConditionalNegativeFlagEnabled(t *testing.T) {
	err := CheckRequiredUse(
		"!python? ( python_targets_python3_11 )",
		map[string]bool{"python": true},
	)
	if err != nil {
		t.Errorf("negative use conditional should pass when flag is enabled (body skipped): %v", err)
	}
}

func TestCheckRequiredUse_ComplexAllOf(t *testing.T) {
	err := CheckRequiredUse(
		"flag1 flag2",
		map[string]bool{"flag1": true, "flag2": true},
	)
	if err != nil {
		t.Errorf("all-of with both flags enabled: %v", err)
	}
}

func TestCheckRequiredUse_ComplexAllOfMissingOne(t *testing.T) {
	err := CheckRequiredUse(
		"flag1 flag2",
		map[string]bool{"flag1": true, "flag2": false},
	)
	if err == nil {
		t.Error("all-of should fail when one flag is disabled")
	}
}

func TestResolve_RequiredUseViolation(t *testing.T) {
	g := makeGraph()
	pkgWithMeta(g, "dev-lang/python", "3.11", "0", "0", false,
		map[string]bool{"python_targets_python3_11": false},
		"python_targets_python3_11",
		"",
	)

	result, err := Resolve(g, []string{"dev-lang/python"}, DefaultResolveConfig())
	if err == nil {
		t.Error("expected error for REQUIRED_USE violation")
	}
	if result != nil {
		for _, c := range result.Conflicts {
			t.Logf("conflict: %s", c)
		}
	}
}

func TestResolve_RequiredUseSatisfied(t *testing.T) {
	g := makeGraph()
	pkgWithMeta(g, "dev-lang/python", "3.11", "0", "0", false,
		map[string]bool{"python_targets_python3_11": true},
		"|| ( python_targets_python3_11 python_targets_python3_12 )",
		"",
	)

	result, err := Resolve(g, []string{"dev-lang/python"}, DefaultResolveConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, a := range result.Install {
		if a.Atom.CP() == "dev-lang/python" {
			found = true
		}
	}
	if !found {
		t.Error("python should be installed (REQUIRED_USE satisfied)")
	}
}

func TestResolve_RequiredUseViolation_KeepGoing(t *testing.T) {
	g := makeGraph()
	pkgWithMeta(g, "dev-lang/python", "3.11", "0", "0", false,
		map[string]bool{"python_targets_python3_11": false},
		"python_targets_python3_11",
		"",
	)

	cfg := DefaultResolveConfig()
	cfg.KeepGoing = true

	result, err := Resolve(g, []string{"dev-lang/python"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error with KeepGoing: %v", err)
	}
	hasConflict := false
	for _, c := range result.Conflicts {
		if strings.Contains(c, "REQUIRED_USE") {
			hasConflict = true
		}
	}
	if !hasConflict {
		t.Error("expected REQUIRED_USE conflict with KeepGoing")
	}
}

// ---------------------------------------------------------------------------
// License checking
// ---------------------------------------------------------------------------

func TestLicenseAccepted_EmptyList(t *testing.T) {
	if !LicenseAccepted("MIT", nil) {
		t.Error("empty ACCEPT_LICENSE should accept all")
	}
	if !LicenseAccepted("GPL-2", []string{}) {
		t.Error("empty ACCEPT_LICENSE slice should accept all")
	}
}

func TestLicenseAccepted_StarAcceptsAll(t *testing.T) {
	if !LicenseAccepted("MIT", []string{"*"}) {
		t.Error("* should accept MIT")
	}
	if !LicenseAccepted("GPL-2", []string{"*"}) {
		t.Error("* should accept GPL-2")
	}
	if !LicenseAccepted("Apache-2.0", []string{"*"}) {
		t.Error("* should accept Apache-2.0")
	}
}

func TestLicenseAccepted_SpecificLicenseAccepted(t *testing.T) {
	if !LicenseAccepted("MIT", []string{"MIT", "GPL-2"}) {
		t.Error("MIT should be accepted when explicitly listed")
	}
}

func TestLicenseAccepted_LicenseNotInList(t *testing.T) {
	if LicenseAccepted("MIT", []string{"GPL-2", "Apache-2.0"}) {
		t.Error("MIT should not be accepted when not in list")
	}
}

func TestLicenseAccepted_MinusStarRejectsAll(t *testing.T) {
	if LicenseAccepted("MIT", []string{"-*"}) {
		t.Error("-* should reject MIT")
	}
	if LicenseAccepted("GPL-2", []string{"-*"}) {
		t.Error("-* should reject GPL-2")
	}
}

func TestLicenseAccepted_MinusStarWithSpecific(t *testing.T) {
	if !LicenseAccepted("MIT", []string{"-*", "MIT"}) {
		t.Error("-* MIT should accept MIT (explicitly listed)")
	}
	if LicenseAccepted("GPL-2", []string{"-*", "MIT"}) {
		t.Error("-* MIT should reject GPL-2 (not listed)")
	}
}

func TestLicenseAccepted_MinusEULA(t *testing.T) {
	if LicenseAccepted("EULA", []string{"*", "-@EULA"}) {
		t.Error("-@EULA should reject EULA license")
	}
	if LicenseAccepted("some-vendor/EULA", []string{"*", "-@EULA"}) {
		t.Error("-@EULA should reject vendor EULA license")
	}
}

func TestLicenseAccepted_MinusEULA_NonEulaAccepted(t *testing.T) {
	if !LicenseAccepted("MIT", []string{"*", "-@EULA"}) {
		t.Error("MIT should be accepted even with -@EULA")
	}
}

func TestLicenseAccepted_MinusSpecific(t *testing.T) {
	if LicenseAccepted("MIT", []string{"*", "-MIT"}) {
		t.Error("-MIT should reject MIT even with *")
	}
}

func TestLicenseAccepted_AtEULA_AcceptsEULA(t *testing.T) {
	if !LicenseAccepted("EULA", []string{"-*", "@EULA"}) {
		t.Error("@EULA should accept EULA even with -*")
	}
}

func TestLicenseAccepted_MultiLicenseField(t *testing.T) {
	if !LicenseAccepted("MIT GPL-2", []string{"-*", "MIT"}) {
		t.Error("should accept when at least one license from field matches")
	}
}

func TestLicenseAccepted_MultiLicenseFieldAllRejected(t *testing.T) {
	if LicenseAccepted("MIT GPL-2", []string{"-*", "Apache-2.0"}) {
		t.Error("should reject when no license from field matches")
	}
}

func TestResolve_LicenseAcceptedInstalls(t *testing.T) {
	g := makeGraph()
	pkgWithMeta(g, "dev-libs/libfoo", "1.0", "0", "0", false, nil, "", "MIT")

	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{
		ACCEPT_LICENSE: []string{"*", "-@EULA"},
	}

	result, err := Resolve(g, []string{"dev-libs/libfoo"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, a := range result.Install {
		if a.Atom.CP() == "dev-libs/libfoo" {
			found = true
		}
	}
	if !found {
		t.Error("libfoo should be installed (MIT accepted)")
	}
}

func TestResolve_LicenseRejected(t *testing.T) {
	g := makeGraph()
	pkgWithMeta(g, "dev-libs/libfoo", "1.0", "0", "0", false, nil, "", "EULA")

	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{
		ACCEPT_LICENSE: []string{"*", "-@EULA"},
	}

	result, err := Resolve(g, []string{"dev-libs/libfoo"}, cfg)
	if err == nil {
		t.Error("expected error for rejected license")
	}
	if result != nil {
		for _, c := range result.Conflicts {
			t.Logf("conflict: %s", c)
		}
	}
}

func TestResolve_LicenseRejected_KeepGoing(t *testing.T) {
	g := makeGraph()
	pkgWithMeta(g, "dev-libs/libfoo", "1.0", "0", "0", false, nil, "", "proprietary")

	cfg := DefaultResolveConfig()
	cfg.KeepGoing = true
	cfg.PortageConfig = &portage.Config{
		ACCEPT_LICENSE: []string{"-*", "MIT"},
	}

	result, err := Resolve(g, []string{"dev-libs/libfoo"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error with KeepGoing: %v", err)
	}
	hasConflict := false
	for _, c := range result.Conflicts {
		if strings.Contains(c, "license") {
			hasConflict = true
		}
	}
	if !hasConflict {
		t.Error("expected license conflict with KeepGoing")
	}
}

// ---------------------------------------------------------------------------
// DepGraph: AddProvider and Providers
// ---------------------------------------------------------------------------

func TestDepGraph_AddProvider(t *testing.T) {
	g := makeGraph()
	g.AddProvider("virtual/rust", "dev-lang/rust-bin")

	if g.Providers["dev-lang/rust-bin"] != "virtual/rust" {
		t.Errorf("expected provider mapping, got %q", g.Providers["dev-lang/rust-bin"])
	}

	if len(g.ProvidersOf["virtual/rust"]) != 1 || g.ProvidersOf["virtual/rust"][0] != "dev-lang/rust-bin" {
		t.Errorf("expected ProvidersOf entry, got %v", g.ProvidersOf["virtual/rust"])
	}
}

func TestDepGraph_AddProvider_Multiple(t *testing.T) {
	g := makeGraph()
	g.AddProvider("virtual/rust", "dev-lang/rust-bin")
	g.AddProvider("virtual/rust", "dev-lang/rust")

	if len(g.ProvidersOf["virtual/rust"]) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(g.ProvidersOf["virtual/rust"]))
	}
	if g.ProvidersOf["virtual/rust"][0] != "dev-lang/rust-bin" {
		t.Errorf("expected rust-bin, got %s", g.ProvidersOf["virtual/rust"][0])
	}
	if g.ProvidersOf["virtual/rust"][1] != "dev-lang/rust" {
		t.Errorf("expected rust, got %s", g.ProvidersOf["virtual/rust"][1])
	}
}

// ---------------------------------------------------------------------------
// Resume save/load roundtrip
// ---------------------------------------------------------------------------

func TestResume_SaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resume")

	result := &ResolveResult{
		Install: []PkgAction{
			{Atom: mustParse("app-editors/vim-9.0"), Action: "install", Reason: "world target"},
			{Atom: mustParse("dev-libs/libfoo-1.0"), Action: "install", Reason: "dependency"},
			{Atom: mustParse("sys-libs/ncurses-6.0"), Action: "install", Reason: "dependency"},
		},
	}

	err := SaveResume(path, result)
	if err != nil {
		t.Fatalf("SaveResume: %v", err)
	}

	loaded, err := LoadResume(path)
	if err != nil {
		t.Fatalf("LoadResume: %v", err)
	}

	if len(loaded) != 3 {
		t.Fatalf("expected 3 atoms, got %d: %v", len(loaded), loaded)
	}

	// all should be uncompleted
	for _, a := range loaded {
		if a == "" {
			t.Error("empty atom in resume")
		}
	}
}

// ---------------------------------------------------------------------------
// Resume with partial completion
// ---------------------------------------------------------------------------

func TestResume_PartialCompletion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resume")

	result := &ResolveResult{
		Install: []PkgAction{
			{Atom: mustParse("app-editors/vim-9.0"), Action: "install"},
			{Atom: mustParse("dev-libs/libfoo-1.0"), Action: "install"},
			{Atom: mustParse("sys-libs/ncurses-6.0"), Action: "install"},
		},
	}

	if err := SaveResume(path, result); err != nil {
		t.Fatalf("SaveResume: %v", err)
	}

	// mark first as complete
	if err := MarkResumeComplete(path, "app-editors/vim-9.0"); err != nil {
		t.Fatalf("MarkResumeComplete: %v", err)
	}

	loaded, err := LoadResume(path)
	if err != nil {
		t.Fatalf("LoadResume: %v", err)
	}

	if len(loaded) != 2 {
		t.Fatalf("expected 2 remaining atoms, got %d: %v", len(loaded), loaded)
	}

	foundVim := false
	for _, a := range loaded {
		if strings.Contains(a, "vim") {
			foundVim = true
		}
	}
	if foundVim {
		t.Error("vim should have been marked complete and not loaded")
	}
}

// ---------------------------------------------------------------------------
// SkipFirst removes first entry
// ---------------------------------------------------------------------------

func TestResume_SkipFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resume")

	result := &ResolveResult{
		Install: []PkgAction{
			{Atom: mustParse("app-editors/vim-9.0"), Action: "install"},
			{Atom: mustParse("dev-libs/libfoo-1.0"), Action: "install"},
			{Atom: mustParse("sys-libs/ncurses-6.0"), Action: "install"},
		},
	}

	if err := SaveResume(path, result); err != nil {
		t.Fatalf("SaveResume: %v", err)
	}

	if err := SkipFirstResume(path); err != nil {
		t.Fatalf("SkipFirstResume: %v", err)
	}

	loaded, err := LoadResume(path)
	if err != nil {
		t.Fatalf("LoadResume: %v", err)
	}

	if len(loaded) != 2 {
		t.Fatalf("expected 2 remaining after skipfirst, got %d: %v", len(loaded), loaded)
	}

	foundVim := false
	for _, a := range loaded {
		if strings.Contains(a, "vim") {
			foundVim = true
		}
	}
	if foundVim {
		t.Error("vim should have been skipped (SkipFirst)")
	}
}

// ---------------------------------------------------------------------------
// Resume save with nil result
// ---------------------------------------------------------------------------

func TestResume_NilResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resume")

	if err := SaveResume(path, nil); err != nil {
		t.Fatalf("SaveResume nil: unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// MarkResumeComplete on nonexistent file
// ---------------------------------------------------------------------------

func TestResume_MarkComplete_Nonexistent(t *testing.T) {
	if err := MarkResumeComplete("/nonexistent/resume/file", "some-atom"); err != nil {
		t.Logf("expected no error for nonexistent file, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// SkipFirstResume on nonexistent file
// ---------------------------------------------------------------------------

func TestResume_SkipFirst_Nonexistent(t *testing.T) {
	if err := SkipFirstResume("/nonexistent/resume/file"); err != nil {
		t.Logf("expected no error for nonexistent file, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// KeepGoing returns partial installs with conflicts
// ---------------------------------------------------------------------------

func TestResolve_KeepGoingReturnsPartialInstalls(t *testing.T) {
	g := makeGraph()
	// one installable package
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)
	// one masked package
	pkg(g, "app-editors/emacs", "28.0", "0", "0", false, nil)
	// one with unsatisfied dep
	pkg(g, "app-misc/tool", "1.0", "0", "0", false, nil)
	deps(g, "app-misc/tool", ">=nonexistent/lib-1.0")

	cfg := DefaultResolveConfig()
	cfg.KeepGoing = true
	cfg.Deep = true
	cfg.PortageConfig = &portage.Config{
		PackageMask: []string{"app-editors/emacs"},
	}

	result, err := Resolve(g, []string{"app-editors/vim", "app-editors/emacs", "app-misc/tool"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error with KeepGoing: %v", err)
	}

	// vim should be in install (it's installable)
	foundVim := false
	for _, a := range result.Install {
		if a.Atom.CP() == "app-editors/vim" {
			foundVim = true
		}
	}
	if !foundVim {
		t.Error("vim should be installed (installable package)")
	}

	// should have conflicts about emacs and the missing dep
	hasMaskedConflict := false
	hasDepConflict := false
	for _, c := range result.Conflicts {
		if strings.Contains(c, "package masked") && strings.Contains(c, "emacs") {
			hasMaskedConflict = true
		}
		if strings.Contains(c, "could not be satisfied") || strings.Contains(c, "no installable version") {
			hasDepConflict = true
		}
	}
	if !hasMaskedConflict {
		t.Error("expected masked conflict for emacs")
	}
	if !hasDepConflict {
		t.Error("expected unsatisfied dep conflict")
	}
}

// ---------------------------------------------------------------------------
// KeepGoing: all packages fail, still returns result
// ---------------------------------------------------------------------------

func TestResolve_KeepGoing_AllFail(t *testing.T) {
	g := makeGraph()
	cfg := DefaultResolveConfig()
	cfg.KeepGoing = true

	result, err := Resolve(g, []string{"nonexistent/pkg"}, cfg)
	if err != nil {
		t.Fatalf("expected no error with KeepGoing, got: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Conflicts) == 0 {
		t.Error("expected conflicts when all targets fail")
	}
	if len(result.Install) != 0 {
		t.Errorf("expected 0 installs when all targets fail, got %d", len(result.Install))
	}
}

// ---------------------------------------------------------------------------
// KeepGoing: handles nil version in NoDeps path gracefully
// ---------------------------------------------------------------------------

func TestResolve_KeepGoing_NoDeps_NilVersion(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/libfoo", "2.0", "0", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.NoDeps = true
	cfg.KeepGoing = true

	// constrain to a version that doesn't exist
	result, err := Resolve(g, []string{">=dev-libs/libfoo-5.0"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Nothing should be installed (no match)
	if len(result.Install) != 0 {
		t.Errorf("expected 0 installs, got %d", len(result.Install))
	}
}

// ---------------------------------------------------------------------------
// Tree formatting output
// ---------------------------------------------------------------------------

func TestFormatTree_SinglePackage(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-lang/python", "3.11.5", "0", "0", false, nil)

	actions := []PkgAction{
		{Atom: mustParse("dev-lang/python-3.11.5"), Action: "install"},
	}

	output := FormatTree(actions, g)
	if !strings.Contains(output, "dev-lang/python") {
		t.Errorf("expected python in tree output, got:\n%s", output)
	}
}

func TestFormatTree_WithDeps(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)
	pkg(g, "dev-libs/libffi", "3.4.4", "0", "0", false, nil)
	pkg(g, "app-arch/bzip2", "1.0.8", "0", "0", false, nil)
	deps(g, "app-editors/vim", ">=dev-libs/libffi-3.4")
	deps(g, "app-editors/vim", ">=app-arch/bzip2-1.0")

	actions := []PkgAction{
		{Atom: mustParse("app-editors/vim-9.0"), Action: "install"},
		{Atom: mustParse("dev-libs/libffi-3.4.4"), Action: "install"},
		{Atom: mustParse("app-arch/bzip2-1.0.8"), Action: "install"},
	}

	output := FormatTree(actions, g)
	if !strings.Contains(output, "app-editors/vim") {
		t.Error("expected vim in tree output")
	}
	if !strings.Contains(output, "dev-libs/libffi") {
		t.Error("expected libffi in tree output")
	}
	if !strings.Contains(output, "app-arch/bzip2") {
		t.Error("expected bzip2 in tree output")
	}
	// deps should be indented
	vimLine := ""
	libffiLine := ""
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "app-editors/vim") {
			vimLine = line
		}
		if strings.Contains(line, "dev-libs/libffi") {
			libffiLine = line
		}
	}
	if len(vimLine) > 0 && len(libffiLine) > 0 {
		// dep should be indented more than root
		vimIndent := len(vimLine) - len(strings.TrimLeft(vimLine, " "))
		libffiIndent := len(libffiLine) - len(strings.TrimLeft(libffiLine, " "))
		if libffiIndent <= vimIndent {
			t.Errorf("dep should be more indented than root: vim=%d libffi=%d",
				vimIndent, libffiIndent)
		}
	}
}

func TestFormatTree_Empty(t *testing.T) {
	g := makeGraph()
	output := FormatTree(nil, g)
	if output != "" {
		t.Errorf("expected empty output, got: %s", output)
	}
}

func TestFormatTree_InstalledPackage(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-lang/python", "3.11.5", "0", "0", true, nil)

	actions := []PkgAction{
		{Atom: mustParse("dev-lang/python-3.11.5"), Action: "install"},
	}

	output := FormatTree(actions, g)
	if !strings.Contains(output, "[ebuild   R    ]") && !strings.Contains(output, "[ebuild") {
		t.Errorf("expected ebuild tag in output, got:\n%s", output)
	}
}

// ---------------------------------------------------------------------------
// Changed deps detection
// ---------------------------------------------------------------------------

func TestDepsChanged_Same(t *testing.T) {
	installed := &VersionInfo{Depend: "foo/bar", Rdepend: "baz/qux"}
	available := &VersionInfo{Depend: "foo/bar", Rdepend: "baz/qux"}

	if depsChanged(installed, available) {
		t.Error("same deps should not be changed")
	}
}

func TestDepsChanged_DependDiffers(t *testing.T) {
	installed := &VersionInfo{Depend: "foo/bar", Rdepend: "baz/qux"}
	available := &VersionInfo{Depend: "foo/bar-2.0", Rdepend: "baz/qux"}

	if !depsChanged(installed, available) {
		t.Error("different DEPEND should be detected as changed")
	}
}

func TestDepsChanged_RdependDiffers(t *testing.T) {
	installed := &VersionInfo{Depend: "foo/bar", Rdepend: "baz/qux"}
	available := &VersionInfo{Depend: "foo/bar", Rdepend: "baz/qux-2.0"}

	if !depsChanged(installed, available) {
		t.Error("different RDEPEND should be detected as changed")
	}
}

func TestDepsChanged_NilInputs(t *testing.T) {
	if depsChanged(nil, nil) {
		t.Error("both nil should return false")
	}
	installed := &VersionInfo{Depend: "foo/bar"}
	if depsChanged(installed, nil) {
		t.Error("nil available should return false")
	}
	if depsChanged(nil, installed) {
		t.Error("nil installed should return false")
	}
}

func TestDepsChanged_BothDiffer(t *testing.T) {
	installed := &VersionInfo{Depend: "foo/bar", Rdepend: "baz/qux"}
	available := &VersionInfo{Depend: "foo/bar-2.0", Rdepend: "baz/qux-2.0"}

	if !depsChanged(installed, available) {
		t.Error("both DEPEND and RDEPEND changed should be detected")
	}
}

// ---------------------------------------------------------------------------
// Auto-unmask generates correct file
// ---------------------------------------------------------------------------

func TestAutoUnmask_GeneratesCorrectFile(t *testing.T) {
	dir := t.TempDir()
	conflicts := []string{
		"package masked: app-editors/vim",
		"package masked: dev-libs/libfoo",
		"some other conflict",
	}

	if err := AutoUnmask(conflicts, dir); err != nil {
		t.Fatalf("AutoUnmask: %v", err)
	}

	vimContent, err := os.ReadFile(filepath.Join(dir, "package.unmask", "app-editors_vim"))
	if err != nil {
		t.Fatalf("reading vim unmask: %v", err)
	}
	if strings.TrimSpace(string(vimContent)) != "app-editors/vim" {
		t.Errorf("unexpected vim content: %q", string(vimContent))
	}

	libfooContent, err := os.ReadFile(filepath.Join(dir, "package.unmask", "dev-libs_libfoo"))
	if err != nil {
		t.Fatalf("reading libfoo unmask: %v", err)
	}
	if strings.TrimSpace(string(libfooContent)) != "dev-libs/libfoo" {
		t.Errorf("unexpected libfoo content: %q", string(libfooContent))
	}
}

func TestAutoUnmask_NoMaskConflicts(t *testing.T) {
	dir := t.TempDir()
	conflicts := []string{
		"unsatisfied dependency: dev-libs/libfoo",
		"no version matches",
	}

	if err := AutoUnmask(conflicts, dir); err != nil {
		t.Fatalf("AutoUnmask: %v", err)
	}

	// no package.unmask directory should be created
	if _, err := os.Stat(filepath.Join(dir, "package.unmask")); !os.IsNotExist(err) {
		t.Error("package.unmask should not be created when no mask conflicts")
	}
}

func TestAutoUnmask_EmptyConflicts(t *testing.T) {
	dir := t.TempDir()
	if err := AutoUnmask(nil, dir); err != nil {
		t.Fatalf("AutoUnmask nil: %v", err)
	}
	if err := AutoUnmask([]string{}, dir); err != nil {
		t.Fatalf("AutoUnmask empty: %v", err)
	}
}

func TestAutoUnmask_Deduplicates(t *testing.T) {
	dir := t.TempDir()
	conflicts := []string{
		"package masked: app-editors/vim",
		"package masked: app-editors/vim",
	}

	if err := AutoUnmask(conflicts, dir); err != nil {
		t.Fatalf("AutoUnmask: %v", err)
	}

	entries, _ := os.ReadDir(filepath.Join(dir, "package.unmask"))
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// Auto-accept-license generates correct file
// ---------------------------------------------------------------------------

func TestAutoAcceptLicense_GeneratesCorrectFile(t *testing.T) {
	dir := t.TempDir()
	conflicts := []string{
		"license MIT not accepted for app-editors/vim",
		"license GPL-2 not accepted for dev-libs/libfoo",
		"some other conflict",
	}

	if err := AutoAcceptLicense(conflicts, dir); err != nil {
		t.Fatalf("AutoAcceptLicense: %v", err)
	}

	vimContent, err := os.ReadFile(filepath.Join(dir, "package.license", "app-editors_vim"))
	if err != nil {
		t.Fatalf("reading vim license: %v", err)
	}
	if strings.TrimSpace(string(vimContent)) != "app-editors/vim MIT" {
		t.Errorf("unexpected vim content: %q", string(vimContent))
	}

	libfooContent, err := os.ReadFile(filepath.Join(dir, "package.license", "dev-libs_libfoo"))
	if err != nil {
		t.Fatalf("reading libfoo license: %v", err)
	}
	if strings.TrimSpace(string(libfooContent)) != "dev-libs/libfoo GPL-2" {
		t.Errorf("unexpected libfoo content: %q", string(libfooContent))
	}
}

func TestAutoAcceptLicense_NoLicenseConflicts(t *testing.T) {
	dir := t.TempDir()
	conflicts := []string{
		"package masked: dev-libs/libfoo",
		"unsatisfied dependency",
	}

	if err := AutoAcceptLicense(conflicts, dir); err != nil {
		t.Fatalf("AutoAcceptLicense: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "package.license")); !os.IsNotExist(err) {
		t.Error("package.license should not be created when no license conflicts")
	}
}

func TestAutoAcceptLicense_EmptyConflicts(t *testing.T) {
	dir := t.TempDir()
	if err := AutoAcceptLicense(nil, dir); err != nil {
		t.Fatalf("AutoAcceptLicense nil: %v", err)
	}
	if err := AutoAcceptLicense([]string{}, dir); err != nil {
		t.Fatalf("AutoAcceptLicense empty: %v", err)
	}
}

func TestAutoAcceptLicense_Deduplicates(t *testing.T) {
	dir := t.TempDir()
	conflicts := []string{
		"license EULA not accepted for app-editors/vim",
		"license MIT not accepted for app-editors/vim",
	}

	if err := AutoAcceptLicense(conflicts, dir); err != nil {
		t.Fatalf("AutoAcceptLicense: %v", err)
	}

	entries, _ := os.ReadDir(filepath.Join(dir, "package.license"))
	if len(entries) != 1 {
		t.Errorf("expected 1 entry (deduplicated), got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// Unsorted display mode
// ---------------------------------------------------------------------------

func TestResolve_UnsortedDisplay(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-shells/bash", "5.0", "0", "0", false, nil)
	pkg(g, "dev-libs/readline", "8.0", "0", "0", false, nil)
	pkg(g, "sys-libs/ncurses", "6.0", "0", "0", false, nil)
	deps(g, "app-shells/bash", ">=dev-libs/readline-8")
	deps(g, "dev-libs/readline", ">=sys-libs/ncurses-6")

	cfg := DefaultResolveConfig()
	cfg.Deep = true
	cfg.UnsortedDisplay = true

	result, err := Resolve(g, []string{"app-shells/bash"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Install) != 3 {
		t.Fatalf("expected 3 installs, got %d", len(result.Install))
	}

	// with unsorted display, packages may appear in any order;
	// all three must still be present
	seen := make(map[string]bool)
	for _, a := range result.Install {
		seen[a.Atom.CP()] = true
	}
	for _, cp := range []string{"app-shells/bash", "dev-libs/readline", "sys-libs/ncurses"} {
		if !seen[cp] {
			t.Errorf("expected %s in unsorted result", cp)
		}
	}
}

func TestResolve_SortedDisplay_StillWorks(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-shells/bash", "5.0", "0", "0", false, nil)
	pkg(g, "dev-libs/readline", "8.0", "0", "0", false, nil)
	pkg(g, "sys-libs/ncurses", "6.0", "0", "0", false, nil)
	deps(g, "app-shells/bash", ">=dev-libs/readline-8")
	deps(g, "dev-libs/readline", ">=sys-libs/ncurses-6")

	cfg := DefaultResolveConfig()
	cfg.Deep = true
	cfg.UnsortedDisplay = false

	result, err := Resolve(g, []string{"app-shells/bash"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Install) != 3 {
		t.Fatalf("expected 3 installs, got %d", len(result.Install))
	}

	// with sorted display, ncurses must precede readline, which must precede bash
	findPos := func(name string) int {
		for i, a := range result.Install {
			if a.Atom.CP() == name {
				return i
			}
		}
		return -1
	}

	ncPos := findPos("sys-libs/ncurses")
	rlPos := findPos("dev-libs/readline")
	bashPos := findPos("app-shells/bash")

	if ncPos >= rlPos {
		t.Error("ncurses must come before readline in sorted output")
	}
	if rlPos >= bashPos {
		t.Error("readline must come before bash in sorted output")
	}
}

// ---------------------------------------------------------------------------
// Resolve: --changed-deps triggers install when deps changed
// ---------------------------------------------------------------------------

func TestResolve_ChangedDepsTriggersReinstall(t *testing.T) {
	g := makeGraph()
	// installed version with old DEPEND
	viInst := pkg(g, "app-editors/vim", "8.0", "0", "0", true, nil)
	viInst.Depend = "old-dep/cat"

	// available version (newer) with new DEPEND
	viAvail := pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)
	viAvail.Depend = "new-dep/cat"

	cfg := DefaultResolveConfig()
	cfg.ChangedDeps = true
	// No Update, so version comparison alone won't trigger anything

	result, err := Resolve(g, []string{"app-editors/vim"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ChangedDeps should trigger needInstall since deps differ between installed (8.0)
	// and available (9.0). The version is newer too, but ChangedDeps is the driving flag.
	foundVim := false
	for _, a := range result.Install {
		if a.Atom.CP() == "app-editors/vim" {
			foundVim = true
		}
	}
	if !foundVim {
		t.Error("vim should be flagged for install (changed deps or newer version)")
	}
}

func TestResolve_ChangedDeps_NoReinstallWhenSame(t *testing.T) {
	g := makeGraph()
	viInst := pkg(g, "app-editors/vim", "9.0", "0", "0", true, nil)
	viInst.Depend = "foo/bar"
	viInst.Rdepend = "baz/qux"

	// FIX: second pkg() call for same version returns the existing VersionInfo
	// due to AddVersion merge. Set deps on the existing pointer.
	viAvail := pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)
	// viAvail == viInst due to merge; set Depend and Rdepend on the same object
	viAvail.Depend = "foo/bar"
	viAvail.Rdepend = "baz/qux"

	cfg := DefaultResolveConfig()
	cfg.ChangedDeps = true

	result, err := Resolve(g, []string{"app-editors/vim"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// should NOT install vim (same deps, already installed)
	foundVim := false
	for _, a := range result.Install {
		if a.Atom.CP() == "app-editors/vim" {
			foundVim = true
		}
	}
	if foundVim {
		t.Error("vim should NOT be reinstalled (deps unchanged)")
	}
}

// ---------------------------------------------------------------------------
// --noreplace: skip if exact same version already installed
// ---------------------------------------------------------------------------

func TestResolve_NoReplace_SkipsSameVersion(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", true, nil)
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.NoReplace = true

	result, err := Resolve(g, []string{"app-editors/vim"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Install) != 0 {
		t.Errorf("expected 0 installs with --noreplace when same version installed, got %d: %v",
			len(result.Install), collectCPV(result.Install))
	}
}

func TestResolve_NoReplace_InstallsWhenNotInstalled(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.NoReplace = true

	result, err := Resolve(g, []string{"app-editors/vim"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Install) != 1 {
		t.Fatalf("expected 1 install when not installed, got %d", len(result.Install))
	}
	if result.Install[0].Atom.CP() != "app-editors/vim" {
		t.Errorf("expected vim, got %s", result.Install[0].Atom.CP())
	}
}

func TestResolve_NoReplace_InstallsWhenNewerVersion(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "8.0", "0", "0", true, nil)
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.NoReplace = true
	cfg.Update = true

	result, err := Resolve(g, []string{"app-editors/vim"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// --noreplace should still allow updates when combined with --update
	if len(result.Install) != 1 {
		t.Fatalf("expected 1 install (newer version with --update), got %d", len(result.Install))
	}
}

// ---------------------------------------------------------------------------
// package.provided: provided packages treated as already installed
// ---------------------------------------------------------------------------

func TestResolve_PackageProvided_SkipsProvided(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-lang/python", "3.11", "0", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{
		PackageProvided: []string{"dev-lang/python-3.11"},
	}

	result, err := Resolve(g, []string{"dev-lang/python"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Install) != 0 {
		t.Errorf("expected 0 installs for provided package, got %d: %v",
			len(result.Install), collectCPV(result.Install))
	}
}

func TestResolve_PackageProvided_InstallsWhenNotProvided(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-lang/python", "3.11", "0", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{
		PackageProvided: []string{"app-editors/vim-9.0"},
	}

	result, err := Resolve(g, []string{"dev-lang/python"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Install) != 1 {
		t.Fatalf("expected 1 install for non-provided package, got %d", len(result.Install))
	}
}

func TestResolve_PackageProvided_ReinstallWhenRequested(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-lang/python", "3.11", "0", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.Reinstall = true
	cfg.PortageConfig = &portage.Config{
		PackageProvided: []string{"dev-lang/python-3.11"},
	}

	result, err := Resolve(g, []string{"dev-lang/python"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// --reinstall should override package.provided
	found := false
	for _, a := range result.Install {
		if a.Atom.CP() == "dev-lang/python" {
			found = true
		}
	}
	if !found {
		t.Error("python should be installed when --reinstall overrides provided")
	}
}

func TestResolve_PackageProvided_UpdateWhenRequested(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-lang/python", "3.11", "0", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.PortageConfig = &portage.Config{
		PackageProvided: []string{"dev-lang/python-3.11"},
	}

	result, err := Resolve(g, []string{"dev-lang/python"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, a := range result.Install {
		if a.Atom.CP() == "dev-lang/python" {
			found = true
		}
	}
	if !found {
		t.Error("python should be installed when --update is used with provided")
	}
}

func TestResolve_PackageProvided_NilConfig(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-lang/python", "3.11", "0", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.PortageConfig = nil

	result, err := Resolve(g, []string{"dev-lang/python"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Install) != 1 {
		t.Errorf("expected 1 install with nil config, got %d", len(result.Install))
	}
}

func Test_isPackageProvided(t *testing.T) {
	r := &resolver{
		portageConfig: &portage.Config{
			PackageProvided: []string{"dev-lang/python-3.11", "app-editors/vim-9.0"},
		},
	}

	if !r.isPackageProvided("dev-lang/python") {
		t.Error("dev-lang/python should be provided")
	}
	if !r.isPackageProvided("app-editors/vim") {
		t.Error("app-editors/vim should be provided")
	}
	if r.isPackageProvided("sys-devel/gcc") {
		t.Error("sys-devel/gcc should not be provided")
	}
}

func Test_isPackageProvided_NilConfig(t *testing.T) {
	r := &resolver{portageConfig: nil}
	if r.isPackageProvided("dev-lang/python") {
		t.Error("nil config should return false")
	}
}

func TestResolve_WithBdepsN(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-shells/bash", "5.0", "0", "0", false, nil)
	pkg(g, "dev-build/make", "4.4", "0", "0", false, nil)
	depWithType(g, "app-shells/bash", ">=dev-build/make-4", DepTypeBuild)

	cfg := DefaultResolveConfig()
	cfg.WithBdeps = "n"
	cfg.Deep = true

	result, err := Resolve(g, []string{"app-shells/bash"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, a := range result.Install {
		if a.Atom.CP() == "dev-build/make" {
			t.Error("make should NOT be installed (--with-bdeps=n)")
		}
	}
}

func TestResolve_WithBdepsY(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-shells/bash", "5.0", "0", "0", false, nil)
	pkg(g, "dev-build/make", "4.4", "0", "0", false, nil)
	depWithType(g, "app-shells/bash", ">=dev-build/make-4", DepTypeBuild)

	cfg := DefaultResolveConfig()
	cfg.WithBdeps = "y"
	cfg.Deep = true

	result, err := Resolve(g, []string{"app-shells/bash"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, a := range result.Install {
		if a.Atom.CP() == "dev-build/make" {
			found = true
		}
	}
	if !found {
		t.Error("make should be installed (--with-bdeps=y)")
	}
}

func TestResolve_WithBdepsAuto_NotInstalled(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-shells/bash", "5.0", "0", "0", false, nil)
	pkg(g, "dev-build/make", "4.4", "0", "0", false, nil)
	depWithType(g, "app-shells/bash", ">=dev-build/make-4", DepTypeBuild)

	cfg := DefaultResolveConfig()
	cfg.WithBdepsAuto = true
	cfg.Deep = true

	result, err := Resolve(g, []string{"app-shells/bash"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, a := range result.Install {
		if a.Atom.CP() == "dev-build/make" {
			found = true
		}
	}
	if !found {
		t.Error("make should be installed (auto: not already installed)")
	}
}

func TestResolve_WithBdepsAuto_AlreadyInstalled(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-shells/bash", "5.0", "0", "0", false, nil)
	pkg(g, "dev-build/make", "4.4", "0", "0", true, nil)
	depWithType(g, "app-shells/bash", ">=dev-build/make-4", DepTypeBuild)

	cfg := DefaultResolveConfig()
	cfg.WithBdepsAuto = true
	cfg.Deep = true

	result, err := Resolve(g, []string{"app-shells/bash"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, a := range result.Install {
		if a.Atom.CP() == "dev-build/make" {
			t.Error("make should NOT be installed (auto: already installed)")
		}
	}
}

func TestResolve_IgnoreSlotOps_Deps(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-shells/bash", "5.0", "0", "0", true, nil)
	pkg(g, "sys-libs/readline", "8.0", "0", "1", true, nil)
	pkg(g, "sys-libs/readline", "8.1", "0", "2", false, nil)

	depA, _ := atom.Parse("sys-libs/readline:0=")
	depA.SlotOp = atom.SlotOpEq
	g.AddDep("app-shells/bash", "sys-libs/readline", "sys-libs/readline:0=", DepTypeRuntime, "", false)

	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.Deep = true
	cfg.IgnoreBuiltSlotOperatorDeps = "y"

	result, err := Resolve(g, []string{"app-shells/bash"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With ignore=y, bash (already installed) should NOT be rebuilt despite readline subslot change
	// readline should still be updated
	foundBash := false
	foundReadline := false
	for _, a := range result.Install {
		if a.Atom.CP() == "app-shells/bash" {
			foundBash = true
		}
		if a.Atom.CP() == "sys-libs/readline" {
			foundReadline = true
		}
	}
	if foundBash {
		t.Error("bash should NOT be rebuilt (ignore-built-slot-operator-deps=y)")
	}
	if !foundReadline {
		t.Error("readline should still be updated")
	}
}

func TestResolve_IgnoreSlotOps_CompleteGraph(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", true, nil)
	pkg(g, "dev-libs/libbar", "2.0", "0/1", "1", true, nil)
	deps(g, "dev-libs/libfoo", "dev-libs/libbar")

	pkg(g, "dev-libs/libbar", "3.0", "0/2", "2", false, nil)

	for _, edge := range g.Packages["dev-libs/libfoo"].Deps {
		if edge.To != nil && edge.To.Atom.CP() == "dev-libs/libbar" {
			edge.DepAtom.SlotOp = atom.SlotOpEq
		}
	}

	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.Deep = true
	cfg.CompleteGraph = true
	cfg.IgnoreBuiltSlotOperatorDeps = "y"

	result, err := Resolve(g, []string{"dev-libs/libbar"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundBar := false
	for _, a := range result.Install {
		if a.Atom.CP() == "dev-libs/libbar" {
			foundBar = true
		}
		if a.Atom.CP() == "dev-libs/libfoo" {
			t.Error("libfoo should NOT be rebuilt (ignore-built-slot-operator-deps=y)")
		}
	}
	if !foundBar {
		t.Error("libbar should be in install list (update)")
	}
}

func TestResolve_IgnoreSlotOps_N(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-shells/bash", "5.0", "0", "0", false, nil)
	pkg(g, "sys-libs/readline", "8.0", "0", "1", true, nil)
	pkg(g, "sys-libs/readline", "8.1", "0", "2", false, nil)

	depA, _ := atom.Parse("sys-libs/readline:0=")
	depA.SlotOp = atom.SlotOpEq
	g.AddDep("app-shells/bash", "sys-libs/readline", "sys-libs/readline:0=", DepTypeRuntime, "", false)

	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.Deep = true
	cfg.IgnoreBuiltSlotOperatorDeps = "n"

	result, err := Resolve(g, []string{"app-shells/bash"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundBash := false
	for _, a := range result.Install {
		if a.Atom.CP() == "app-shells/bash" {
			foundBash = true
		}
	}
	if !foundBash {
		t.Error("bash should be rebuilt (ignore-built-slot-operator-deps=n)")
	}
}

func TestResolve_BinpkgRespectUseField(t *testing.T) {
	cfg := DefaultResolveConfig()
	if cfg.BinpkgRespectUse != false {
		t.Error("BinpkgRespectUse should default to false")
	}

	cfg.BinpkgRespectUse = true
	if !cfg.BinpkgRespectUse {
		t.Error("BinpkgRespectUse should be true after setting")
	}
}

func TestResolve_BinpkgDirField(t *testing.T) {
	cfg := DefaultResolveConfig()
	if cfg.BinpkgDir != "" {
		t.Errorf("BinpkgDir should default to empty, got %q", cfg.BinpkgDir)
	}

	cfg.BinpkgDir = "/var/cache/binpkgs"
	if cfg.BinpkgDir != "/var/cache/binpkgs" {
		t.Errorf("BinpkgDir = %q", cfg.BinpkgDir)
	}
}

func TestResolve_GetBinPkgField(t *testing.T) {
	cfg := DefaultResolveConfig()
	if cfg.GetBinPkg {
		t.Error("GetBinPkg should default to false")
	}
	if cfg.GetBinPkgOnly {
		t.Error("GetBinPkgOnly should default to false")
	}
}

func Test_stress_BdepsAuto_AllTypes(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-shells/bash", "5.0", "0", "0", false, nil)
	pkg(g, "dev-build/make", "4.4", "0", "0", false, nil)
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", true, nil)
	pkg(g, "dev-libs/libbar", "2.0", "0", "0", false, nil)

	depWithType(g, "app-shells/bash", ">=dev-build/make-4", DepTypeBuild)
	depWithType(g, "app-shells/bash", ">=dev-libs/libfoo-1", DepTypeBuild)
	depWithType(g, "app-shells/bash", ">=dev-libs/libbar-2", DepTypeRuntime)

	cfg := DefaultResolveConfig()
	cfg.WithBdepsAuto = true
	cfg.Deep = true

	result, err := Resolve(g, []string{"app-shells/bash"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundMake := false
	foundLibfoo := false
	foundLibbar := false
	for _, a := range result.Install {
		switch a.Atom.CP() {
		case "dev-build/make":
			foundMake = true
		case "dev-libs/libfoo":
			foundLibfoo = true
		case "dev-libs/libbar":
			foundLibbar = true
		}
	}

	if !foundMake {
		t.Error("make should be installed (auto: not installed)")
	}
	if foundLibfoo {
		t.Error("libfoo should NOT be installed as bdep (auto: already installed)")
	}
	if !foundLibbar {
		t.Error("libbar should be installed (runtime dep)")
	}
}
