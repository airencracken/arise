package resolve

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
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
	return g.AddVersion(cp, version, slot, subslot, installed, useFlags, "amd64")
}

func pkgKeywords(g *DepGraph, cp, version, slot, subslot string, installed bool, useFlags map[string]bool, keywords string) *VersionInfo {
	return g.AddVersion(cp, version, slot, subslot, installed, useFlags, keywords)
}

// pkgWithMeta adds a version and returns it; caller can set .RequiredUse and .License on the result.
func pkgWithMeta(g *DepGraph, cp, version, slot, subslot string, installed bool, useFlags map[string]bool, requiredUse, license string) *VersionInfo {
	vi := g.AddVersion(cp, version, slot, subslot, installed, useFlags, "amd64")
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

func actionForCP(actions []PkgAction, cp string) bool {
	for _, action := range actions {
		if action.Atom != nil && action.Atom.CP() == cp {
			return true
		}
	}
	return false
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
	if a.Reason != "explicit target" {
		t.Errorf("expected explicit target reason, got %q", a.Reason)
	}
	if len(result.Uninstall) != 0 {
		t.Errorf("expected 0 uninstalls, got %d", len(result.Uninstall))
	}
	if len(result.Conflicts) != 0 {
		t.Errorf("expected 0 conflicts, got %v", result.Conflicts)
	}
	if len(result.DecisionLedger.Records) != 1 ||
		result.DecisionLedger.Records[0].Outcome != DecisionSelected ||
		result.DecisionLedger.Records[0].ActionID != ActionIdentity(a) {
		t.Fatalf("selected action ledger = %#v", result.DecisionLedger)
	}
}

func TestResolveDecisionLedgerClassifiesCommittedCandidates(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/library", "1", "0", "0", false, nil)
	pkg(g, "dev-libs/library", "2", "0", "0", false, nil)
	result, err := Resolve(g, []string{">=dev-libs/library-1"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified {
		t.Fatalf("resolution failed: %#v", result.Conflicts)
	}
	var selected, skipped int
	for _, record := range result.DecisionLedger.Records {
		switch record.Outcome {
		case DecisionSelected:
			selected++
		case DecisionSkipped:
			skipped++
		}
	}
	if selected != 1 || skipped != 1 {
		t.Fatalf("candidate outcomes = %#v", result.DecisionLedger)
	}
}

func TestDecisionLedgerBoundsAdversarialCandidateVolume(t *testing.T) {
	records := make([]CandidateDecision, MaxDecisionRecords+100)
	for index := range records {
		records[index] = CandidateDecision{
			ID:      fmt.Sprintf("app-misc/candidate-%06d|0|gentoo|available", index),
			Outcome: DecisionSkipped, State: "available",
			CPV: fmt.Sprintf("app-misc/candidate-%06d", index), Slot: "0",
			Repository: "gentoo", Reasons: []string{"lower committed preference"},
		}
	}
	ledger := boundDecisionLedger(records)
	if !ledger.Truncated || ledger.OmittedRecords != 100 ||
		len(ledger.Records) != MaxDecisionRecords || ledger.EncodedBytes > MaxDecisionBytes {
		t.Fatalf("bounded ledger = %#v", ledger)
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

func TestResolve_ExplicitInstalledTargetSelectsAvailableUpgrade(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "8.0", "0", "0", true, nil)
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)

	result, err := Resolve(g, []string{"app-editors/vim"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Install) != 1 || result.Install[0].Action != "update" || result.Install[0].Atom.Version == nil || result.Install[0].Atom.Version.Raw != "9.0" {
		t.Fatalf("explicit target plan = %#v", result.Install)
	}
}

func TestResolve_ReinstallRejectsInstalledVersionMissingFromRepository(t *testing.T) {
	g := makeGraph()
	pkg(g, "net-libs/libsoup", "2.74.2", "2.4", "0", true, nil)
	pkg(g, "net-libs/libsoup", "2.74.3-r1", "2.4", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.Reinstall = true
	_, err := Resolve(g, []string{"=net-libs/libsoup-2.74.2"}, cfg)
	if err == nil || !strings.Contains(err.Error(), "no source ebuild is available") {
		t.Fatalf("Resolve error = %v, want unavailable source ebuild", err)
	}
}

func TestResolve_ReinstallOnlyForcesExplicitTarget(t *testing.T) {
	g := makeGraph()
	pkg(g, "llvm-core/clang", "22.1.8", "22", "22.1", true, nil)
	pkg(g, "llvm-core/clang", "22.1.8", "22", "22.1", false, nil)
	deps(g, "llvm-core/clang", "llvm-runtimes/compiler-rt:22")
	pkg(g, "llvm-runtimes/compiler-rt", "22.1.8", "22", "0", true, nil)
	pkg(g, "llvm-runtimes/compiler-rt", "22.1.8", "22", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.Reinstall = true
	cfg.Deep = true
	result, err := Resolve(g, []string{"=llvm-core/clang-22.1.8"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Install) != 1 || result.Install[0].Atom.CP() != "llvm-core/clang" || result.Install[0].Action != "reinstall" {
		t.Fatalf("forced reinstall plan = %#v, want only explicit clang target", result.Install)
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
	pkg(g, "sys-apps/coreutils", "9.0", "0", "0", false, nil)
	pkg(g, "virtual/editor", "1", "0", "0", true, nil)      // already installed
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)  // not installed
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

func TestResolvePreservesStrongBlockerOrderingRequirement(t *testing.T) {
	g := makeGraph()
	parent := pkg(g, "app-misc/replacement", "1", "0", "0", false, nil)
	parent.Rdepend = "!!app-misc/obsolete"
	pkg(g, "app-misc/obsolete", "1", "0", "0", true, nil)

	cfg := DefaultResolveConfig()
	result, err := Resolve(g, []string{"app-misc/replacement"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Uninstall) != 1 || !strings.Contains(result.Uninstall[0].Reason, "strong; remove before merge") {
		t.Fatalf("strong blocker ordering requirement lost: %#v", result.Uninstall)
	}
}

func TestResolve_VersionedBlockDoesNotMatchNewerInstalled(t *testing.T) {
	g := makeGraph()
	vi := pkg(g, "sys-apps/coreutils", "9.0", "0", "0", false, nil)
	vi.Rdepend = "!<sys-apps/util-linux-2.13"
	pkg(g, "sys-apps/util-linux", "2.41", "0", "0", true, nil)

	result, err := Resolve(g, []string{"sys-apps/coreutils"}, DefaultResolveConfig())
	if err != nil {
		t.Fatalf("newer installed package must not trigger old-version blocker: %v", err)
	}
	if len(result.Conflicts) != 0 || len(result.Uninstall) != 0 {
		t.Fatalf("unexpected blocker actions: conflicts=%v uninstall=%v", result.Conflicts, result.Uninstall)
	}
}

func TestVerifyPlannedStateRetainsUnblockedParallelSlot(t *testing.T) {
	g := makeGraph()
	parent := pkg(g, "app-misc/replacement", "1", "0", "0", false, nil)
	parent.Rdepend = "!<dev-libs/parallel-2 dev-libs/parallel:2"
	pkg(g, "dev-libs/parallel", "1", "1", "1", true, nil)
	pkg(g, "dev-libs/parallel", "2", "2", "2", true, nil)

	result, err := Resolve(g, []string{"app-misc/replacement"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("parallel slot 2 should satisfy the retained dependency: %v", result.Conflicts)
	}
	if len(result.Uninstall) != 1 || result.Uninstall[0].Atom.Version == nil || result.Uninstall[0].Atom.Version.Raw != "1" || result.Uninstall[0].Slot != "1" {
		t.Fatalf("uninstall must identify only version 1 in slot 1: %#v", result.Uninstall)
	}
}

func TestResolveBlockerRemovesEveryMatchingParallelSlot(t *testing.T) {
	g := makeGraph()
	parent := pkg(g, "app-misc/replacement", "1", "0", "0", false, nil)
	parent.Rdepend = "!dev-libs/parallel"
	pkg(g, "dev-libs/parallel", "1", "1", "1", true, nil)
	pkg(g, "dev-libs/parallel", "2", "2", "2", true, nil)

	result, err := Resolve(g, []string{"app-misc/replacement"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) != 0 || len(result.Uninstall) != 2 {
		t.Fatalf("unqualified blocker must remove both installed slots: conflicts=%v uninstall=%#v", result.Conflicts, result.Uninstall)
	}
	want := map[string]string{"1": "1", "2": "2"}
	for _, action := range result.Uninstall {
		if action.Atom == nil || action.Atom.Version == nil || want[action.Slot] != action.Atom.Version.Raw {
			t.Fatalf("uninstall lacks exact slot/version identity: %#v", action)
		}
		delete(want, action.Slot)
	}
	if len(want) != 0 {
		t.Fatalf("missing parallel-slot removals: %v", want)
	}
}

func TestResolve_VersionedBlockResolvedByCoordinatedUpgrade(t *testing.T) {
	g := makeGraph()
	common := pkg(g, "dev-libs/common", "2", "0", "0", false, nil)
	common.Rdepend = "!<dev-libs/library-2"
	pkg(g, "dev-libs/library", "1", "0", "0", true, nil)
	pkg(g, "dev-libs/library", "2", "0", "0", false, nil)
	cfg := DefaultResolveConfig()
	cfg.Update = true

	result, err := Resolve(g, []string{"dev-libs/common", "dev-libs/library"}, cfg)
	if err != nil {
		t.Fatalf("coordinated upgrade should resolve blocker: %v", err)
	}
	found := false
	for _, action := range result.Install {
		found = found || action.Atom.CP() == "dev-libs/library" && action.Atom.Version.Raw == "2"
	}
	if !found || len(result.Conflicts) != 0 {
		t.Fatalf("replacement upgrade missing: install=%v conflicts=%v", result.Install, result.Conflicts)
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

func TestSortPlannedActionsUsesSelectedVersionDependencies(t *testing.T) {
	g := makeGraph()
	parent := pkg(g, "app-misc/parent", "2", "0", "0", false, nil)
	parent.Bdepend = "dev-build/tool"
	pkg(g, "dev-build/tool", "1", "0", "0", false, nil)
	r := &resolver{graph: g}
	actions := []PkgAction{
		{Atom: mustParse("app-misc/parent-2"), Slot: "0"},
		{Atom: mustParse("dev-build/tool-1"), Slot: "0"},
	}
	sorted := r.sortPlannedActions(actions)
	if sorted[0].Atom.CP() != "dev-build/tool" || sorted[1].Atom.CP() != "app-misc/parent" {
		t.Fatalf("selected-version BDEPEND order = %v", collectCPV(sorted))
	}
	wantPrerequisite := ActionIdentity(sorted[0])
	if !reflect.DeepEqual(sorted[1].Prerequisites, []string{wantPrerequisite}) {
		t.Fatalf("parent prerequisites = %v, want %v", sorted[1].Prerequisites, wantPrerequisite)
	}
}

func TestSortPlannedActionsPlacesPdependAfterParent(t *testing.T) {
	g := makeGraph()
	parent := pkg(g, "app-misc/parent", "2", "0", "0", false, nil)
	parent.Pdepend = "app-misc/post"
	pkg(g, "app-misc/post", "1", "0", "0", false, nil)
	r := &resolver{graph: g}
	actions := []PkgAction{
		{Atom: mustParse("app-misc/post-1"), Slot: "0"},
		{Atom: mustParse("app-misc/parent-2"), Slot: "0"},
	}
	sorted := r.sortPlannedActions(actions)
	if sorted[0].Atom.CP() != "app-misc/parent" || sorted[1].Atom.CP() != "app-misc/post" {
		t.Fatalf("PDEPEND order = %v", collectCPV(sorted))
	}
}

func TestSortPlannedActionsCondensesDependencyCycleBeforeDependent(t *testing.T) {
	g := makeGraph()
	a := pkg(g, "dev-libs/a", "1", "0", "0", false, nil)
	b := pkg(g, "dev-libs/b", "1", "0", "0", false, nil)
	consumer := pkg(g, "app-misc/consumer", "1", "0", "0", false, nil)
	a.Rdepend = "dev-libs/b"
	b.Rdepend = "dev-libs/a"
	consumer.Rdepend = "dev-libs/a"
	r := &resolver{graph: g}
	actions := []PkgAction{
		{Atom: mustParse("app-misc/consumer-1"), Slot: "0"},
		{Atom: mustParse("dev-libs/a-1"), Slot: "0"},
		{Atom: mustParse("dev-libs/b-1"), Slot: "0"},
	}
	sorted := r.sortPlannedActions(actions)
	if sorted[2].Atom.CP() != "app-misc/consumer" {
		t.Fatalf("cyclic dependency component was not placed before its dependent: %v", collectCPV(sorted))
	}
	if len(sorted[0].Prerequisites) != 0 || !reflect.DeepEqual(sorted[1].Prerequisites, []string{ActionIdentity(sorted[0])}) {
		t.Fatalf("cycle component was not serialized deterministically: %v / %v", sorted[0].Prerequisites, sorted[1].Prerequisites)
	}
	if !reflect.DeepEqual(sorted[2].Prerequisites, []string{ActionIdentity(sorted[1])}) {
		t.Fatalf("consumer prerequisites = %v, want completed cycle component", sorted[2].Prerequisites)
	}
	r.validatePlanOrder(sorted)
	if len(r.conflicts) != 0 {
		t.Fatalf("unavoidable intra-component order was treated as a conflict: %v", r.conflicts)
	}
}

func TestSortPlannedActionsPreservesBuildPrerequisitesInsideRuntimeCycle(t *testing.T) {
	g := makeGraph()
	gemato := pkg(g, "app-portage/gemato", "20.12", "0", "0", false, nil)
	gpep517 := pkg(g, "dev-python/gpep517", "19", "0", "0", false, nil)
	installer := pkg(g, "dev-python/installer", "1.0.1", "0", "0", false, nil)
	portage := pkg(g, "sys-apps/portage", "3.0", "0", "0", false, nil)
	gemato.Bdepend = "dev-python/gpep517[python_targets_python3_14(-)?]"
	gpep517.Bdepend = "dev-python/installer"
	portage.Rdepend = "app-portage/gemato"
	installer.Rdepend = "sys-apps/portage"

	r := &resolver{graph: g}
	actions := []PkgAction{
		{Atom: mustParse("app-portage/gemato-20.12"), Slot: "0", UseFlags: map[string]bool{"python_targets_python3_14": true}},
		{Atom: mustParse("sys-apps/portage-3.0"), Slot: "0"},
		{Atom: mustParse("dev-python/gpep517-19"), Slot: "0", UseFlags: map[string]bool{"python_targets_python3_14": true}},
		{Atom: mustParse("dev-python/installer-1.0.1"), Slot: "0"},
	}
	sorted := r.sortPlannedActions(actions)
	positions := make(map[string]int)
	for i := range sorted {
		positions[sorted[i].Atom.CP()] = i
	}
	if positions["dev-python/installer"] >= positions["dev-python/gpep517"] ||
		positions["dev-python/gpep517"] >= positions["app-portage/gemato"] {
		t.Fatalf("build prerequisites were broken to satisfy runtime cycle: %v", collectCPV(sorted))
	}
	gematoAction := sorted[positions["app-portage/gemato"]]
	if len(gematoAction.Prerequisites) == 0 {
		t.Fatalf("gemato has no serialized prerequisite: %v", collectCPV(sorted))
	}
}

func TestSortPlannedActionsMatchesDependencySlot(t *testing.T) {
	g := makeGraph()
	parent := pkg(g, "app-misc/parent", "1", "0", "0", false, nil)
	parent.Bdepend = "dev-lang/python:3.14"
	pkg(g, "dev-lang/python", "3.13.9", "3.13", "3.13", false, nil)
	pkg(g, "dev-lang/python", "3.14.1", "3.14", "3.14", false, nil)
	r := &resolver{graph: g}
	actions := []PkgAction{
		{Atom: mustParse("app-misc/parent-1"), Slot: "0"},
		{Atom: mustParse("dev-lang/python-3.14.1"), Slot: "3.14", Subslot: "3.14"},
		{Atom: mustParse("dev-lang/python-3.13.9"), Slot: "3.13", Subslot: "3.13"},
	}
	sorted := r.sortPlannedActions(actions)
	positions := map[string]int{}
	for i, action := range sorted {
		positions[action.Atom.CP()+":"+action.Slot] = i
	}
	if positions["dev-lang/python:3.14"] >= positions["app-misc/parent:0"] {
		t.Fatalf("required Python slot ordered after parent: %v", collectCPV(sorted))
	}
}

func TestSortPlannedActionsIgnoresDisabledConditional(t *testing.T) {
	g := makeGraph()
	parent := pkg(g, "app-misc/parent", "1", "0", "0", false, map[string]bool{"docs": false})
	parent.Bdepend = "docs? ( dev-python/sphinx )"
	pkg(g, "dev-python/sphinx", "1", "0", "0", false, nil)
	r := &resolver{graph: g}
	actions := []PkgAction{
		{Atom: mustParse("app-misc/parent-1"), Slot: "0", UseFlags: map[string]bool{"docs": false}},
		{Atom: mustParse("dev-python/sphinx-1"), Slot: "0"},
	}
	sorted := r.sortPlannedActions(actions)
	if sorted[0].Atom.CP() != "app-misc/parent" {
		t.Fatalf("disabled conditional created ordering edge: %v", collectCPV(sorted))
	}
}

func TestSortPlannedActionsMatchesDependencyRepository(t *testing.T) {
	g := makeGraph()
	parent := pkg(g, "app-misc/parent", "1", "0", "0", false, nil)
	parent.Repository = "gentoo"
	parent.Bdepend = "dev-build/tool::guru"
	g.AddVersionFromRepository("dev-build/tool", "1", "0", "0", false, nil, "amd64", "gentoo")
	g.AddVersionFromRepository("dev-build/tool", "2", "0", "0", false, nil, "amd64", "guru")
	r := &resolver{graph: g}
	actions := []PkgAction{
		{Atom: mustParse("app-misc/parent-1"), Slot: "0", Repository: "gentoo"},
		{Atom: mustParse("dev-build/tool-1"), Slot: "0", Repository: "gentoo"},
		{Atom: mustParse("dev-build/tool-2"), Slot: "0", Repository: "guru"},
	}
	sorted := r.sortPlannedActions(actions)
	positions := map[string]int{}
	for i, action := range sorted {
		positions[action.Atom.CP()+"::"+action.Repository] = i
	}
	if positions["dev-build/tool::guru"] >= positions["app-misc/parent::gentoo"] {
		t.Fatalf("required repository ordered after parent: %v", collectCPV(sorted))
	}
}

func TestResolve_RepositoryConstraintRejectsInstalledOtherRepository(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-misc/parent", "1", "0", "0", false, nil)
	g.AddVersionFromRepository("dev-libs/library", "2", "0", "0", true, nil, "amd64", "gentoo")
	g.AddVersionFromRepository("dev-libs/library", "1", "0", "0", false, nil, "amd64", "guru")
	depWithType(g, "app-misc/parent", "dev-libs/library::guru", DepTypeRuntime)

	result, err := Resolve(g, []string{"app-misc/parent"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, action := range result.Install {
		if action.Atom.CP() == "dev-libs/library" {
			found = true
			if action.Repository != "guru" || action.Atom.Version == nil || action.Atom.Version.Raw != "1" {
				t.Fatalf("repository-constrained dependency = %#v", action)
			}
		}
	}
	if !found {
		t.Fatalf("installed package from wrong repository satisfied constraint: %#v", result.Install)
	}
}

func TestResolve_ExplicitRepositoryConstraintSelectsMatchingRepository(t *testing.T) {
	g := makeGraph()
	g.AddVersionFromRepository("dev-libs/library", "2", "0", "0", false, nil, "amd64", "gentoo")
	g.AddVersionFromRepository("dev-libs/library", "1", "0", "0", false, nil, "amd64", "guru")
	result, err := Resolve(g, []string{"dev-libs/library::guru"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Install) != 1 || result.Install[0].Repository != "guru" || result.Install[0].Atom.Version.Raw != "1" {
		t.Fatalf("explicit repository plan = %#v", result.Install)
	}
}

func TestResolve_IdenticalCPVRemainsSelectableFromEitherRepository(t *testing.T) {
	g := makeGraph()
	gentoo := g.AddVersionFromRepository("dev-libs/library", "1", "0", "0", false, nil, "amd64", "gentoo")
	gentoo.RepositoryPriority = 0
	guru := g.AddVersionFromRepository("dev-libs/library", "1", "0", "0", false, nil, "amd64", "guru")
	guru.RepositoryPriority = 10

	defaultResult, err := Resolve(g, []string{"dev-libs/library"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultResult.Install) != 1 || defaultResult.Install[0].Repository != "guru" {
		t.Fatalf("unqualified duplicate CPV plan = %#v", defaultResult.Install)
	}
	gentooResult, err := Resolve(g, []string{"dev-libs/library::gentoo"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(gentooResult.Install) != 1 || gentooResult.Install[0].Repository != "gentoo" {
		t.Fatalf("qualified shadowed CPV plan = %#v", gentooResult.Install)
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
	pkg(g, "dev-libs/libfoo", "1.0", "0", "0", false, nil)
	pkg(g, "dev-libs/libbar", "2.0", "0/1", "1", true, nil)
	deps(g, "dev-libs/libfoo", "dev-libs/libbar")

	// newer libbar available with changed subslot
	pkg(g, "dev-libs/libbar", "3.0", "0/2", "2", false, nil)

	// libfoo's installed VDB metadata records the built slot/subslot binding.
	setSlotOp(g, "dev-libs/libfoo", "dev-libs/libbar", atom.SlotOpEq)

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

func TestResolveConditionalUseDependenciesFromParent(t *testing.T) {
	dep := mustParse("sys-apps/dbus[abi_x86_32(-)?,abi_x86_64(-)?]")
	resolved := resolveUseDependencies(dep, map[string]bool{"abi_x86_64": true, "abi_x86_32": false})
	if len(resolved.UseFlags) != 1 || resolved.UseFlags[0].Name != "abi_x86_64" || !resolved.UseFlags[0].Enabled {
		t.Fatalf("resolved USE deps = %+v", resolved.UseFlags)
	}
	child := mustParse("sys-apps/dbus-1.16.2")
	if !atomMatchesDep(child, resolved, "0", "", map[string]bool{"abi_x86_64": true}) {
		t.Fatal("resolved ABI dependency should match enabled 64-bit child")
	}
}

func TestResolveRejectsBareCPVTargetInsteadOfIgnoringItsVersion(t *testing.T) {
	graph := makeGraph()
	pkg(graph, "app-misc/example", "1", "0", "0", false, nil)
	pkg(graph, "app-misc/example", "2", "0", "0", false, nil)
	if _, err := Resolve(graph, []string{"app-misc/example-1"}, DefaultResolveConfig()); err == nil {
		t.Fatal("bare CPV target was accepted")
	}
	result, err := Resolve(graph, []string{"=app-misc/example-1"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Install) != 1 || result.Install[0].Atom.Version.Raw != "1" {
		t.Fatalf("exact target selected %#v", result.Install)
	}
}

func TestResolveRechecksBlockersWhenVersionIsReachedInAnotherDomain(t *testing.T) {
	graph := makeGraph()
	consumer := pkg(graph, "app-misc/consumer", "1", "0", "0", false, nil)
	consumer.EAPI = "8"
	consumer.Bdepend = "dev-qt/qtbase"
	consumer.Rdepend = "dev-qt/qtbase"
	qtbase := pkg(graph, "dev-qt/qtbase", "2", "6", "2", false, nil)
	qtbase.Rdepend = "!<dev-qt/qt5compat-2:6"
	pkg(graph, "dev-qt/qt5compat", "1", "6", "1", true, nil)
	pkg(graph, "dev-qt/qt5compat", "2", "6", "2", false, nil)

	root := makeGraph()
	pkg(root, "dev-qt/qt5compat", "1", "6", "1", true, nil)
	config := DefaultResolveConfig()
	config.Update = true
	config.InstalledByDomain = map[DependencyDomain]*DepGraph{
		DomainROOT: root, DomainBROOT: makeGraph(), DomainSYSROOT: makeGraph(),
	}
	result, err := Resolve(graph, []string{"app-misc/consumer"}, config)
	if err != nil {
		t.Fatal(err)
	}
	found, removed := false, false
	for _, action := range result.Install {
		found = found || action.Atom.CP() == "dev-qt/qt5compat"
	}
	for _, action := range result.Uninstall {
		removed = removed || action.Atom.CP() == "dev-qt/qt5compat"
	}
	if !found || removed {
		t.Fatalf("ROOT blocker did not coordinate replacement: install=%#v uninstall=%#v", result.Install, result.Uninstall)
	}
}

func TestResolveUseDependencyOperatorTruthTable(t *testing.T) {
	trueValue, falseValue := true, false
	tests := []struct {
		name      string
		flag      atom.UseFlag
		parent    map[string]bool
		child     map[string]bool
		satisfied bool
	}{
		{"conditional enabled applies", atom.UseFlag{Name: "feature", Conditional: true}, map[string]bool{"feature": true}, map[string]bool{"feature": true}, true},
		{"conditional enabled rejects disabled child", atom.UseFlag{Name: "feature", Conditional: true}, map[string]bool{"feature": true}, map[string]bool{"feature": false}, false},
		{"conditional disabled does not apply", atom.UseFlag{Name: "feature", Conditional: true}, map[string]bool{"feature": false}, nil, true},
		{"negated conditional applies disabled", atom.UseFlag{Name: "feature", Conditional: true, Negated: true}, map[string]bool{"feature": false}, map[string]bool{"feature": false}, true},
		{"negated conditional rejects enabled child", atom.UseFlag{Name: "feature", Conditional: true, Negated: true}, map[string]bool{"feature": false}, map[string]bool{"feature": true}, false},
		{"negated conditional enabled does not apply", atom.UseFlag{Name: "feature", Conditional: true, Negated: true}, map[string]bool{"feature": true}, nil, true},
		{"equality enabled", atom.UseFlag{Name: "feature", Equal: true}, map[string]bool{"feature": true}, map[string]bool{"feature": true}, true},
		{"equality disabled", atom.UseFlag{Name: "feature", Equal: true}, map[string]bool{"feature": false}, map[string]bool{"feature": false}, true},
		{"negated equality enabled parent", atom.UseFlag{Name: "feature", Equal: true, Negated: true}, map[string]bool{"feature": true}, map[string]bool{"feature": false}, true},
		{"negated equality disabled parent", atom.UseFlag{Name: "feature", Equal: true, Negated: true}, map[string]bool{"feature": false}, map[string]bool{"feature": true}, true},
		{"missing child positive without default", atom.UseFlag{Name: "feature", Enabled: true}, nil, nil, false},
		{"missing child negative without default", atom.UseFlag{Name: "feature", Enabled: false}, nil, nil, false},
		{"missing child defaults enabled", atom.UseFlag{Name: "feature", Enabled: true, Default: &trueValue}, nil, nil, true},
		{"missing child defaults disabled", atom.UseFlag{Name: "feature", Enabled: false, Default: &falseValue}, nil, nil, true},
		{"missing child wrong enabled default", atom.UseFlag{Name: "feature", Enabled: false, Default: &trueValue}, nil, nil, false},
		{"missing child wrong disabled default", atom.UseFlag{Name: "feature", Enabled: true, Default: &falseValue}, nil, nil, false},
	}
	packageAtom := mustParse("dev-libs/child-1")
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			constraint := &atom.Atom{Category: "dev-libs", Package: "child", UseFlags: []atom.UseFlag{test.flag}}
			resolved := resolveUseDependencies(constraint, test.parent)
			got := atomMatches(packageAtom, resolved, "0", "0", test.child, packageAtom.Version)
			if got != test.satisfied {
				t.Fatalf("resolved=%s child=%v satisfied=%v, want %v", resolved, test.child, got, test.satisfied)
			}
		})
	}
}

func TestAtomMatchesRequiresExplicitSubslot(t *testing.T) {
	packageAtom := mustParse("dev-libs/provider-1")
	constraint := mustParse("dev-libs/provider:0/53=")
	if !atomMatches(packageAtom, constraint, "0", "53", nil, packageAtom.Version) {
		t.Fatal("matching explicit subslot rejected")
	}
	if atomMatches(packageAtom, constraint, "0", "54", nil, packageAtom.Version) {
		t.Fatal("mismatched explicit subslot accepted")
	}
	if !atomMatches(packageAtom, mustParse("dev-libs/provider:0/0="), "0", "", nil, packageAtom.Version) {
		t.Fatal("implicit subslot equal to SLOT was rejected")
	}
}

func TestAtomMatchesVersionConstraintRequiresCandidateVersion(t *testing.T) {
	if atomMatches(mustParse("dev-libs/provider"), mustParse(">=dev-libs/provider-1"), "0", "0", nil, nil) {
		t.Fatal("version constraint accepted a versionless candidate")
	}
}

func TestDependenciesForVersionEnforcesUseDependencyEAPI(t *testing.T) {
	g := makeGraph()
	node := g.AddPackage("app-misc/parent")
	r := &resolver{graph: g, baseUseCache: make(map[string]map[string]bool), useOverrides: make(map[string]map[string]bool)}
	tests := []struct {
		name, eapi, dependency string
		wantError              bool
	}{
		{"plain use dependency before EAPI 2", "1", "dev-libs/child[feature]", true},
		{"conditional use dependency in EAPI 2", "2", "dev-libs/child[feature?]", false},
		{"default before EAPI 4", "3", "dev-libs/child[feature(+)]", true},
		{"default in EAPI 4", "4", "dev-libs/child[feature(-)]", false},
		{"future EAPI remains forward compatible", "9999", "dev-libs/child[feature(+)]", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			vi := &VersionInfo{Package: node, Version: mustParse("app-misc/parent-1").Version, Available: true, Rdepend: test.dependency, EAPI: test.eapi}
			_, err := r.dependenciesForVersion(node, vi)
			if (err != nil) != test.wantError {
				t.Fatalf("error=%v, wantError=%v", err, test.wantError)
			}
		})
	}
}

func TestDependenciesForVersionIgnoresDependencyClassesUnavailableToEAPI(t *testing.T) {
	tests := []struct {
		name string
		vi   *VersionInfo
		want map[DepType]bool
	}{
		{"BDEPEND before EAPI 7", &VersionInfo{EAPI: "6", Bdepend: "not valid syntax ("}, map[DepType]bool{}},
		{"BDEPEND in EAPI 7", &VersionInfo{EAPI: "7", Bdepend: "dev-build/tool"}, map[DepType]bool{DepTypeBuild: true}},
		{"IDEPEND before EAPI 8", &VersionInfo{EAPI: "7", Idepend: "not valid syntax ("}, map[DepType]bool{}},
		{"IDEPEND in EAPI 8", &VersionInfo{EAPI: "8", Idepend: "app-admin/tool"}, map[DepType]bool{DepTypeInstall: true}},
		{"future EAPI", &VersionInfo{EAPI: "9999", Bdepend: "dev-build/tool", Idepend: "app-admin/tool"}, map[DepType]bool{DepTypeBuild: true, DepTypeInstall: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph := makeGraph()
			node := graph.AddPackage("app-misc/parent")
			test.vi.Package = node
			test.vi.Version = mustParse("app-misc/parent-1").Version
			test.vi.Available = true
			resolver := &resolver{graph: graph, toInstall: make(map[string]*PkgAction), baseUseCache: make(map[string]map[string]bool), useOverrides: make(map[string]map[string]bool)}
			edges, err := resolver.dependenciesForVersion(node, test.vi)
			if err != nil {
				t.Fatal(err)
			}
			got := make(map[DepType]bool)
			for _, edge := range edges {
				got[edge.Type] = true
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("dependency classes = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestDependencyDomainsFollowPMSRoots(t *testing.T) {
	tests := map[DepType]DependencyDomain{
		DepTypeBuild: DomainBROOT, DepTypeInstall: DomainBROOT,
		DepTypeDepend:  DomainSYSROOT,
		DepTypeRuntime: DomainROOT, DepTypePost: DomainROOT,
	}
	for dependencyType, want := range tests {
		if got := dependencyDomain(dependencyType); got != want {
			t.Errorf("dependencyDomain(%v) = %q, want %q", dependencyType, got, want)
		}
	}
}

func TestDependDomainFollowsEAPIRootSplit(t *testing.T) {
	for _, test := range []struct {
		eapi string
		want DependencyDomain
	}{
		{eapi: "6", want: DomainBROOT},
		{eapi: "7", want: DomainSYSROOT},
		{eapi: "8", want: DomainSYSROOT},
		{eapi: "9999", want: DomainSYSROOT},
	} {
		if got := dependencyDomainForEAPI(DepTypeDepend, test.eapi); got != test.want {
			t.Errorf("EAPI %s DEPEND domain = %s, want %s", test.eapi, got, test.want)
		}
	}
}

func TestResolvePlansDependInEAPISpecificDomain(t *testing.T) {
	for _, test := range []struct {
		eapi string
		want DependencyDomain
	}{
		{eapi: "6", want: DomainBROOT},
		{eapi: "7", want: DomainSYSROOT},
	} {
		t.Run("EAPI "+test.eapi, func(t *testing.T) {
			graph := makeGraph()
			application := pkg(graph, "app-misc/application", "1", "0", "0", false, nil)
			application.EAPI = test.eapi
			application.Depend = "dev-build/tool"
			pkg(graph, "dev-build/tool", "1", "0", "0", false, nil)
			config := DefaultResolveConfig()
			config.InstalledByDomain = map[DependencyDomain]*DepGraph{
				DomainROOT: makeGraph(), DomainSYSROOT: makeGraph(), DomainBROOT: makeGraph(),
			}
			result, err := Resolve(graph, []string{"app-misc/application"}, config)
			if err != nil {
				t.Fatal(err)
			}
			var domains []DependencyDomain
			for _, action := range result.Install {
				if action.Atom.CP() == "dev-build/tool" {
					domains = append(domains, action.Domain)
				}
			}
			if len(domains) != 1 || domains[0] != test.want {
				t.Fatalf("EAPI %s DEPEND actions = %#v, domains=%v, want %s", test.eapi, result.Install, domains, test.want)
			}
		})
	}
}

func TestBinaryMergeOmitsSourceOnlyDependencyClasses(t *testing.T) {
	g := makeGraph()
	node := g.AddPackage("app-misc/parent")
	vi := &VersionInfo{
		Package: node, Version: mustParse("app-misc/parent-1").Version, Available: true, EAPI: "8",
		Depend: "dev-libs/target", Rdepend: "dev-libs/runtime", Bdepend: "dev-build/native",
		Idepend: "app-admin/install-tool", Pdepend: "app-misc/post",
	}
	r := &resolver{
		graph: g, toInstall: make(map[string]*PkgAction),
		baseUseCache: make(map[string]map[string]bool), useOverrides: make(map[string]map[string]bool),
	}
	r.toInstall[versionActionKey(node.Atom.CP(), vi)] = &PkgAction{Atom: bestVersionAtom(node.Atom, vi), MergeType: "binary"}
	edges, err := r.dependenciesForVersion(node, vi)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[DepType]bool)
	for _, edge := range edges {
		got[edge.Type] = true
		if edge.Domain != dependencyDomain(edge.Type) {
			t.Errorf("edge type %v domain = %q", edge.Type, edge.Domain)
		}
	}
	if got[DepTypeDepend] || got[DepTypeBuild] {
		t.Fatalf("binary merge retained source-only dependency classes: %#v", got)
	}
	for _, want := range []DepType{DepTypeRuntime, DepTypeInstall, DepTypePost} {
		if !got[want] {
			t.Errorf("binary merge omitted dependency class %v", want)
		}
	}
}

func TestBinaryMergeExplicitWithBdepsKeepsBuildDependencyClasses(t *testing.T) {
	graph := makeGraph()
	node := graph.AddPackage("app-misc/parent")
	version := &VersionInfo{
		Package: node, Version: mustParse("app-misc/parent-1").Version, Available: true, EAPI: "8",
		Depend: "dev-libs/target", Rdepend: "dev-libs/runtime", Bdepend: "dev-build/native",
	}
	resolver := &resolver{
		graph: graph, config: ResolveConfig{UsePkg: true, WithBdeps: "y"},
		toInstall: make(map[string]*PkgAction), baseUseCache: make(map[string]map[string]bool),
		useOverrides: make(map[string]map[string]bool),
	}
	resolver.toInstall[versionActionKey(node.Atom.CP(), version)] = &PkgAction{Atom: bestVersionAtom(node.Atom, version), MergeType: "binary"}
	edges, err := resolver.dependenciesForVersion(node, version)
	if err != nil {
		t.Fatal(err)
	}
	classes := make(map[DepType]bool)
	for _, edge := range edges {
		classes[edge.Type] = true
	}
	if !classes[DepTypeDepend] || !classes[DepTypeBuild] || !classes[DepTypeRuntime] {
		t.Fatalf("explicit --with-bdeps=y binary classes = %#v", classes)
	}
}

func TestSourceAndBinaryTransactionDependencyDomains(t *testing.T) {
	tests := []struct {
		name      string
		eapi      string
		mergeType string
		withBdeps string
		want      map[string]DependencyDomain
	}{
		{
			name: "EAPI 8 source", eapi: "8", mergeType: "source", withBdeps: "auto",
			want: map[string]DependencyDomain{
				"dev-libs/target": DomainSYSROOT, "dev-build/native": DomainBROOT,
				"dev-libs/runtime": DomainROOT, "app-admin/install-tool": DomainBROOT,
				"app-misc/post": DomainROOT,
			},
		},
		{
			name: "EAPI 6 source DEPEND uses build root", eapi: "6", mergeType: "source", withBdeps: "auto",
			want: map[string]DependencyDomain{
				"dev-libs/target": DomainBROOT, "dev-libs/runtime": DomainROOT, "app-misc/post": DomainROOT,
			},
		},
		{
			name: "EAPI 8 binary automatic bdeps", eapi: "8", mergeType: "binary", withBdeps: "auto",
			want: map[string]DependencyDomain{
				"dev-libs/runtime": DomainROOT, "app-admin/install-tool": DomainBROOT, "app-misc/post": DomainROOT,
			},
		},
		{
			name: "EAPI 8 binary explicit bdeps", eapi: "8", mergeType: "binary", withBdeps: "y",
			want: map[string]DependencyDomain{
				"dev-libs/target": DomainSYSROOT, "dev-build/native": DomainBROOT,
				"dev-libs/runtime": DomainROOT, "app-admin/install-tool": DomainBROOT,
				"app-misc/post": DomainROOT,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph := makeGraph()
			node := graph.AddPackage("app-misc/parent")
			version := &VersionInfo{
				Package: node, Version: mustParse("app-misc/parent-1").Version, Available: true, EAPI: test.eapi,
				Depend: "dev-libs/target", Rdepend: "dev-libs/runtime", Pdepend: "app-misc/post",
			}
			if test.eapi != "6" {
				version.Bdepend = "dev-build/native"
				version.Idepend = "app-admin/install-tool"
			}
			resolver := &resolver{
				graph: graph, config: ResolveConfig{UsePkg: test.mergeType == "binary", WithBdeps: test.withBdeps},
				toInstall: make(map[string]*PkgAction), baseUseCache: make(map[string]map[string]bool),
				useOverrides: make(map[string]map[string]bool),
			}
			resolver.toInstall[versionActionKey(node.Atom.CP(), version)] = &PkgAction{Atom: bestVersionAtom(node.Atom, version), MergeType: test.mergeType}
			edges, err := resolver.dependenciesForVersion(node, version)
			if err != nil {
				t.Fatal(err)
			}
			got := make(map[string]DependencyDomain)
			for _, edge := range edges {
				got[edge.DepAtom.CP()] = edge.Domain
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("dependency domains = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestBuildPkgOnlyDependencyClassesFollowDeepMode(t *testing.T) {
	for _, test := range []struct {
		name string
		deep bool
	}{
		{name: "shallow"},
		{name: "deep", deep: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			graph := makeGraph()
			node := graph.AddPackage("app-misc/parent")
			version := &VersionInfo{
				Package: node, Version: mustParse("app-misc/parent-1").Version, Available: true, EAPI: "8",
				Depend: "dev-libs/target", Bdepend: "dev-build/native", Rdepend: "dev-libs/runtime",
				Idepend: "app-admin/install-tool", Pdepend: "app-misc/post",
			}
			resolver := &resolver{
				graph: graph, config: ResolveConfig{BuildPkgOnly: true, Deep: test.deep, WithBdeps: "y"},
				toInstall: make(map[string]*PkgAction), baseUseCache: make(map[string]map[string]bool),
				useOverrides: make(map[string]map[string]bool),
			}
			resolver.toInstall[versionActionKey(node.Atom.CP(), version)] = &PkgAction{Atom: bestVersionAtom(node.Atom, version), MergeType: "source"}
			edges, err := resolver.dependenciesForVersion(node, version)
			if err != nil {
				t.Fatal(err)
			}
			classes := make(map[DepType]bool)
			for _, edge := range edges {
				classes[edge.Type] = true
			}
			for _, buildClass := range []DepType{DepTypeDepend, DepTypeBuild} {
				if !classes[buildClass] {
					t.Errorf("buildpkgonly omitted build class %v", buildClass)
				}
			}
			for _, installClass := range []DepType{DepTypeRuntime, DepTypeInstall, DepTypePost} {
				if classes[installClass] != test.deep {
					t.Errorf("deep=%t install class %v present=%t", test.deep, installClass, classes[installClass])
				}
			}
		})
	}
}

func TestEffectiveBdepsModeMatchesUsepkgAutoPolicy(t *testing.T) {
	for _, test := range []struct {
		name   string
		config ResolveConfig
		want   string
	}{
		{name: "source auto", config: ResolveConfig{WithBdeps: "auto"}, want: "y"},
		{name: "usepkg auto", config: ResolveConfig{WithBdeps: "auto", UsePkg: true}, want: "n"},
		{name: "usepkgonly auto", config: ResolveConfig{WithBdepsAuto: true, UsePkgOnly: true}, want: "n"},
		{name: "explicit yes overrides usepkg", config: ResolveConfig{WithBdeps: "y", UsePkg: true}, want: "y"},
		{name: "explicit no", config: ResolveConfig{WithBdeps: "n"}, want: "n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolver := &resolver{config: test.config}
			if got := resolver.effectiveBdepsMode(); got != test.want {
				t.Fatalf("effective bdeps mode = %q, want %q", got, test.want)
			}
		})
	}
}

func TestOnlyDepsRemovesTargetAndAppliesTargetScopedDependencyClasses(t *testing.T) {
	graph := makeGraph()
	target := pkg(graph, "app-misc/target", "1", "0", "0", false, nil)
	target.EAPI = "8"
	target.Depend = "dev-build/build"
	target.Bdepend = "dev-build/native"
	target.Rdepend = "dev-libs/runtime"
	target.Pdepend = "app-misc/post"
	target.Idepend = "app-admin/install-tool"
	for _, cp := range []string{"dev-build/build", "dev-build/native", "dev-libs/runtime", "app-misc/post", "app-admin/install-tool"} {
		pkg(graph, cp, "1", "0", "0", false, nil)
	}
	config := DefaultResolveConfig()
	config.OnlyDeps = true
	config.OnlyDepsWithRdeps = "n"
	config.OnlyDepsWithIDeps = "y"
	result, err := Resolve(graph, []string{"app-misc/target"}, config)
	if err != nil {
		t.Fatal(err)
	}
	present := make(map[string]bool)
	for _, action := range result.Install {
		present[action.Atom.CP()] = true
	}
	for _, cp := range []string{"app-misc/target", "dev-libs/runtime", "app-misc/post"} {
		if present[cp] {
			t.Errorf("--onlydeps unexpectedly retained %s: %#v", cp, result.Install)
		}
	}
	for _, cp := range []string{"dev-build/build", "dev-build/native", "app-admin/install-tool"} {
		if !present[cp] {
			t.Errorf("--onlydeps omitted %s: %#v", cp, result.Install)
		}
	}
	var targetDecision *CandidateDecision
	for index := range result.DecisionLedger.Records {
		if result.DecisionLedger.Records[index].CPV == "app-misc/target-1" {
			targetDecision = &result.DecisionLedger.Records[index]
			break
		}
	}
	if targetDecision == nil || targetDecision.Outcome != DecisionSkipped ||
		!slices.Contains(targetDecision.Reasons, "omitted by onlydeps partial-plan mode") {
		t.Fatalf("--onlydeps target decision = %#v", targetDecision)
	}
}

func TestRootDepsRdepsFollowsEAPI7Boundary(t *testing.T) {
	for _, test := range []struct {
		eapi       string
		wantDepend bool
	}{
		{eapi: "6", wantDepend: false},
		{eapi: "7", wantDepend: true},
	} {
		t.Run("EAPI "+test.eapi, func(t *testing.T) {
			graph := makeGraph()
			target := pkg(graph, "app-misc/target", "1", "0", "0", false, nil)
			target.EAPI = test.eapi
			target.Depend = "dev-build/build"
			target.Rdepend = "dev-libs/runtime"
			pkg(graph, "dev-build/build", "1", "0", "0", false, nil)
			pkg(graph, "dev-libs/runtime", "1", "0", "0", false, nil)
			config := DefaultResolveConfig()
			config.RootDeps = "rdeps"
			result, err := Resolve(graph, []string{"app-misc/target"}, config)
			if err != nil {
				t.Fatal(err)
			}
			present := make(map[string]bool)
			for _, action := range result.Install {
				present[action.Atom.CP()] = true
			}
			if present["dev-build/build"] != test.wantDepend {
				t.Errorf("DEPEND present=%t, want %t: %#v", present["dev-build/build"], test.wantDepend, result.Install)
			}
			if !present["dev-libs/runtime"] {
				t.Errorf("RDEPEND omitted: %#v", result.Install)
			}
		})
	}
}

func TestRootDepsTrueDuplicatesBuildAndInstallDependenciesIntoRoot(t *testing.T) {
	graph := makeGraph()
	target := pkg(graph, "app-misc/target", "1", "0", "0", false, nil)
	target.EAPI = "8"
	target.Depend = "dev-build/target-tool"
	target.Bdepend = "dev-build/native-tool"
	target.Idepend = "app-admin/install-tool"
	for _, cp := range []string{"dev-build/target-tool", "dev-build/native-tool", "app-admin/install-tool"} {
		pkg(graph, cp, "1", "0", "0", false, nil)
	}
	config := DefaultResolveConfig()
	config.RootDeps = "True"
	config.WithBdeps = "y"
	config.InstalledByDomain = map[DependencyDomain]*DepGraph{
		DomainROOT: makeGraph(), DomainSYSROOT: makeGraph(), DomainBROOT: makeGraph(),
	}
	result, err := Resolve(graph, []string{"app-misc/target"}, config)
	if err != nil {
		t.Fatal(err)
	}
	domains := make(map[string]map[DependencyDomain]bool)
	for _, action := range result.Install {
		cp := action.Atom.CP()
		if domains[cp] == nil {
			domains[cp] = make(map[DependencyDomain]bool)
		}
		domains[cp][action.Domain] = true
	}
	for cp, native := range map[string]DependencyDomain{
		"dev-build/target-tool":  DomainSYSROOT,
		"dev-build/native-tool":  DomainBROOT,
		"app-admin/install-tool": DomainBROOT,
	} {
		if !domains[cp][native] || !domains[cp][DomainROOT] {
			t.Errorf("%s domains=%v, want %s and %s", cp, domains[cp], native, DomainROOT)
		}
	}
}

func TestRepositoryEAPIPolicyExcludesBannedAndWarnsDeprecated(t *testing.T) {
	graph := makeGraph()
	node := graph.AddPackage("app-misc/example")
	banned := graph.AddVersionFromRepository("app-misc/example", "2", "0", "0", false, nil, "amd64", "gentoo")
	banned.EAPI = "6"
	banned.Available = false
	deprecated := graph.AddVersionFromRepository("app-misc/example", "1", "0", "0", false, nil, "amd64", "gentoo")
	deprecated.EAPI = "7"
	deprecated.EAPIDeprecated = true
	result, err := Resolve(graph, []string{node.Atom.CP()}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Install) != 1 || result.Install[0].Atom.Version.Raw != "1" {
		t.Fatalf("selected actions = %#v, want deprecated but eligible EAPI 7 candidate", result.Install)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "deprecated EAPI 7") {
		t.Fatalf("warnings = %v", result.Warnings)
	}
}

func TestUsePkgOnlyRejectsMissingBinary(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-misc/parent", "1", "0", "0", false, nil)
	result, err := Resolve(g, []string{"app-misc/parent"}, ResolveConfig{
		UsePkgOnly: true, BinpkgDir: t.TempDir(),
	})
	if err == nil {
		t.Fatalf("Resolve succeeded with no binary; result=%#v", result)
	}
	if !strings.Contains(err.Error(), "binary-only") {
		t.Fatalf("error = %v, want binary-only diagnostic", err)
	}
}

func TestResolve_RetainedDependencyClassesFollowWithBdeps(t *testing.T) {
	makeFixture := func() *DepGraph {
		g := makeGraph()
		parent := pkg(g, "app-misc/parent", "1", "0", "0", true, nil)
		parent.EAPI = "8"
		parent.Depend = "dev-libs/build-library"
		parent.Bdepend = "dev-build/tool"
		parent.Rdepend = "dev-libs/runtime"
		parent.Idepend = "app-admin/install-tool"
		parent.Pdepend = "app-misc/post"
		for _, cp := range []string{"dev-libs/build-library", "dev-build/tool", "dev-libs/runtime", "app-admin/install-tool", "app-misc/post"} {
			pkg(g, cp, "1", "0", "0", false, nil)
		}
		return g
	}
	for _, test := range []struct {
		mode       string
		want, omit []string
	}{
		{"n", []string{"dev-libs/runtime", "app-misc/post"}, []string{"dev-libs/build-library", "dev-build/tool", "app-admin/install-tool"}},
		{"y", []string{"dev-libs/build-library", "dev-build/tool", "dev-libs/runtime", "app-misc/post"}, []string{"app-admin/install-tool"}},
	} {
		t.Run(test.mode, func(t *testing.T) {
			cfg := DefaultResolveConfig()
			cfg.Deep, cfg.WithBdeps, cfg.WithBdepsAuto = true, test.mode, false
			result, err := Resolve(makeFixture(), []string{"app-misc/parent"}, cfg)
			if err != nil {
				t.Fatal(err)
			}
			present := make(map[string]bool)
			for _, action := range result.Install {
				present[action.Atom.CP()] = true
			}
			for _, cp := range test.want {
				if !present[cp] {
					t.Errorf("required class package %s omitted: %v", cp, collectCPV(result.Install))
				}
			}
			for _, cp := range test.omit {
				if present[cp] {
					t.Errorf("inactive class package %s included: %v", cp, collectCPV(result.Install))
				}
			}
		})
	}
}

func TestResolveDeepUpdatePromotesSatisfiedTransitiveDependency(t *testing.T) {
	g := makeGraph()
	parent := pkg(g, "app-misc/parent", "1", "0", "0", true, nil)
	parent.Rdepend = "dev-libs/child"
	pkg(g, "dev-libs/child", "1", "0", "0", true, nil)
	pkg(g, "dev-libs/child", "2", "0", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.Deep = true
	result, err := Resolve(g, []string{"app-misc/parent"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, action := range result.Install {
		if action.Atom.CP() == "dev-libs/child" {
			found = true
			if action.Atom.Version == nil || action.Atom.Version.Raw != "2" || action.Action != "update" {
				t.Fatalf("deep child action = %#v", action)
			}
		}
	}
	if !found {
		t.Fatalf("deep update omitted transitive candidate: %v", collectCPV(result.Install))
	}
}

func TestTildeMatch(t *testing.T) {
	tests := []struct {
		v, c string
		want bool
	}{
		{"3.11.5", "3.11", false},
		{"3.11.5-r1", "3.11", false},
		{"3.12.0", "3.11", false},
		{"3.11", "3.11", true},
		{"3.11-r7", "3.11", true},
		{"3.11.0", "3.11", false},
		{"3.11_alpha1-r2", "3.11_alpha1", true},
		{"3.11_alpha2", "3.11_alpha1", false},
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
		{"1.0", "1", true},
		{"10", "1", false},
		{"1.2a", "1.2", true},
		{"1.2_alpha", "1.2", true},
		{"1.02", "1.2", false},
		{"01.2", "1.2", true},
		{"1.2-r1", "1.2-r1", true},
		{"1.2-r2", "1.2-r1", false},
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
	if result == nil {
		t.Fatal("expected a resolver result")
	}
	for _, warning := range result.Warnings {
		if strings.HasPrefix(warning, "circular dependency: ") {
			t.Fatalf("internal traversal cycle leaked as a user warning: %q", warning)
		}
	}
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
		name    string
		graphFn func() *DepGraph
		targets []string
		config  ResolveConfig
	}{
		{
			name:    "nil graph",
			graphFn: func() *DepGraph { return nil },
			targets: []string{"app-editors/vim"},
			config:  DefaultResolveConfig(),
		},
		{
			name:    "empty graph",
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

func TestKeywordAcceptedUsesStableHostArch(t *testing.T) {
	r := &resolver{portageConfig: &portage.Config{MakeConf: map[string]string{"ARCH": "amd64"}}}
	if !r.keywordAccepted("-* amd64") {
		t.Fatal("stable host keyword should be accepted without explicit ACCEPT_KEYWORDS")
	}
	if r.keywordAccepted("-* arm64") {
		t.Fatal("a different architecture should not be accepted")
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

func TestAddParsedDepCopiesMutableAtomFieldsPerEdge(t *testing.T) {
	parsed, err := atom.Parse(">=pkg/b-1.0:0/1=")
	if err != nil {
		t.Fatal(err)
	}
	g := makeGraph()
	g.AddParsedDep("pkg/a", "pkg/b", parsed, DepTypeRuntime, "ssl", false)
	g.AddParsedDep("pkg/c", "pkg/b", parsed, DepTypeBuild, "", false)
	first := g.Packages["pkg/a"].Deps[0].DepAtom
	second := g.Packages["pkg/c"].Deps[0].DepAtom
	if first == parsed || second == parsed || first == second {
		t.Fatal("parsed dependency atoms share mutable top-level storage")
	}
	first.Slot = "mutated"
	if parsed.Slot != "0" || second.Slot != "0" {
		t.Fatalf("edge mutation escaped its copy: parsed=%q second=%q", parsed.Slot, second.Slot)
	}
	if first.Version != parsed.Version || second.Version != parsed.Version {
		t.Fatal("immutable parsed version was unnecessarily duplicated")
	}
}

func TestAdversarialAddParsedDepNilFallsBackToTargetIdentity(t *testing.T) {
	g := makeGraph()
	g.AddParsedDep("pkg/a", "pkg/b", nil, DepTypeRuntime, "", false)
	edge := g.Packages["pkg/a"].Deps[0]
	if edge.DepAtom == nil || edge.DepAtom.CP() != "pkg/b" {
		t.Fatalf("nil parsed dependency fallback = %#v", edge.DepAtom)
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

func TestEffectiveUseChangedIgnoresDisabledIUSEAddition(t *testing.T) {
	old := map[string]bool{"ssl": true}
	newFlags := map[string]bool{"ssl": true, "new_disabled": false}
	if effectiveUseChanged(old, newFlags) {
		t.Fatal("disabled IUSE addition is not an effective USE change")
	}
	if !useFlagsChanged(old, newFlags) {
		t.Fatal("disabled IUSE addition must be visible to --newuse")
	}
	if !effectiveUseChanged(old, map[string]bool{"ssl": false}) {
		t.Fatal("enabled flag transition must be an effective USE change")
	}
}

func TestNewUseChangedIgnoresImplicitEffectiveUseDomain(t *testing.T) {
	installed := &VersionInfo{
		InstalledUseFlags:  map[string]bool{"split-usr": true, "gawk": true, "abi_x86_64": true, "elibc_glibc": true},
		InstalledIUseFlags: map[string]bool{"split-usr": true, "gawk": true, "busybox": true},
	}
	candidate := &VersionInfo{UseFlags: map[string]bool{"split-usr": false, "gawk": true, "busybox": false}}
	candidateFlags := map[string]bool{
		"split-usr": true, "gawk": true, "busybox": false,
		"abi_x86_64": true, "abi_x86_32": false, "elibc_glibc": true, "elibc_musl": false,
	}
	if newUseChanged(installed, candidate, candidateFlags) {
		t.Fatal("implicit effective USE flags must not trigger --newuse")
	}
	candidate.UseFlags["new-flag"] = false
	candidateFlags["new-flag"] = false
	if !newUseChanged(installed, candidate, candidateFlags) {
		t.Fatal("a disabled declared IUSE addition must trigger --newuse")
	}
}

func TestResolverNewUseIgnoresImplicitFlagsPresentOnlyInRepositoryIUSE(t *testing.T) {
	installed := &VersionInfo{
		InstalledUseFlags:  map[string]bool{"ssl": true, "abi_x86_64": true, "arch_amd64": true},
		InstalledIUseFlags: map[string]bool{"ssl": true},
	}
	candidate := &VersionInfo{
		UseFlags: map[string]bool{"ssl": false, "abi_x86_64": false, "arch_amd64": false},
	}
	r := &resolver{portageConfig: &portage.Config{UseExpandImplicit: []string{"ABI_X86", "ARCH"}}}
	flags := map[string]bool{"ssl": true, "abi_x86_64": true, "arch_amd64": true}
	a, err := atom.Parse("app-misc/example")
	if err != nil {
		t.Fatal(err)
	}
	if r.newUseChanged(&PkgNode{Atom: a}, installed, candidate, flags) {
		t.Fatal("repository implicit IUSE values must not replay an otherwise unchanged package")
	}
}

func TestResolve_NewUseAndChangedUseClassification(t *testing.T) {
	makeCase := func() *DepGraph {
		g := makeGraph()
		installed := pkg(g, "app-misc/example", "1", "0", "0", true, map[string]bool{"ssl": true, "new_disabled": false})
		installed.InstalledUseFlags = map[string]bool{"ssl": true}
		return g
	}
	changed := DefaultResolveConfig()
	changed.ChangedUse = true
	result, err := Resolve(makeCase(), []string{"app-misc/example"}, changed)
	if err != nil || len(result.Install) != 0 {
		t.Fatalf("--changed-use rebuilt for disabled IUSE addition: %v %v", result, err)
	}
	newUse := DefaultResolveConfig()
	newUse.NewUse = true
	result, err = Resolve(makeCase(), []string{"app-misc/example"}, newUse)
	if err != nil || len(result.Install) != 1 || result.Install[0].Action != "reinstall" {
		t.Fatalf("--newuse did not rebuild for IUSE addition: %v %v", result, err)
	}
}

func TestResolve_DeepNewUseRebuildsSatisfiedDependency(t *testing.T) {
	g := makeGraph()
	parent := pkg(g, "app-misc/parent", "1", "0", "0", true, nil)
	parent.Available = true
	parent.Rdepend = "app-misc/dependency"
	dependency := pkg(g, "app-misc/dependency", "1", "0", "0", true, map[string]bool{"old_target": false, "new_target": true})
	dependency.Available = true
	dependency.InstalledUseFlags = map[string]bool{"old_target": true, "new_target": false}
	dependency.InstalledIUseFlags = map[string]bool{"old_target": true, "new_target": true}

	cfg := DefaultResolveConfig()
	cfg.Deep = true
	cfg.NewUse = true
	result, err := Resolve(g, []string{"app-misc/parent"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !actionForCP(result.Install, "app-misc/dependency") {
		t.Fatalf("--deep --newuse omitted satisfied dependency rebuild: %v", result.Install)
	}
}

func TestResolve_DynamicDepsUsesCurrentMetadataWithoutRebuildingParent(t *testing.T) {
	g := makeGraph()
	parent := pkg(g, "acct-user/postmaster", "1", "0", "0", true, nil)
	parent.Available = true
	parent.InstalledRdepend = "acct-group/mail"
	parent.Rdepend = "acct-group/mail acct-user/root"
	pkg(g, "acct-group/mail", "1", "0", "0", true, nil)
	pkg(g, "acct-user/root", "1", "0", "0", false, nil)
	cfg := DefaultResolveConfig()
	cfg.Update = true

	result, err := Resolve(g, []string{"acct-user/postmaster"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var root, parentRebuild bool
	for _, action := range result.Install {
		root = root || action.Atom.CP() == "acct-user/root"
		parentRebuild = parentRebuild || action.Atom.CP() == "acct-user/postmaster"
	}
	if !root || parentRebuild {
		t.Fatalf("dynamic dependency closure = %v; want root only", result.Install)
	}

	cfg.DynamicDeps = false
	result, err = Resolve(g, []string{"acct-user/postmaster"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range result.Install {
		if action.Atom.CP() == "acct-user/root" {
			t.Fatalf("VDB dependency mode unexpectedly used current metadata: %v", result.Install)
		}
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
		graph:              g,
		config:             cfg,
		toInstall:          make(map[string]*PkgAction),
		toUninstall:        make(map[string]*PkgAction),
		conflicts:          []string{},
		seenDeps:           make(map[string]bool),
		backtrackRemaining: cfg.Backtrack,
		worldSet: &WorldSet{
			Entries: []string{"app-editors/vim", "app-shells/bash"},
		},
		systemSet: &WorldSet{Entries: []string{"sys-apps/coreutils"}},
	}
	if cfg.Backtrack <= 0 {
		r.backtrackRemaining = 10
	}

	atoms, err := r.expandTargets([]string{"@world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(atoms) != 3 {
		t.Errorf("expected 3 atoms from @world expansion, got %d", len(atoms))
	}

	cpSet := make(map[string]bool)
	for _, a := range atoms {
		cpSet[a.CP()] = true
	}
	if !cpSet["app-editors/vim"] || !cpSet["app-shells/bash"] || !cpSet["sys-apps/coreutils"] {
		t.Errorf("expected world and system packages in @world expansion, got %v", cpSet)
	}
}

func TestResolve_WorldSetEmpty(t *testing.T) {
	g := makeGraph()
	cfg := DefaultResolveConfig()

	r := &resolver{
		graph:              g,
		config:             cfg,
		toInstall:          make(map[string]*PkgAction),
		toUninstall:        make(map[string]*PkgAction),
		conflicts:          []string{},
		seenDeps:           make(map[string]bool),
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

func TestResolveReinstallsOlderArisePhaseEnvironmentABI(t *testing.T) {
	g := makeGraph()
	installed := pkg(g, "app-misc/example", "1", "0", "0", true, nil)
	installed.InstalledPhaseEnvABI = "older"

	result, err := Resolve(g, []string{"app-misc/example"}, DefaultResolveConfig())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(result.Install) != 1 || result.Install[0].Action != "reinstall" {
		t.Fatalf("environment ABI repair plan = %#v", result.Install)
	}
}

func TestResolveDoesNotTreatUnmarkedPortagePackageAsOldAriseABI(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-misc/example", "1", "0", "0", true, nil)

	result, err := Resolve(g, []string{"app-misc/example"}, DefaultResolveConfig())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(result.Install) != 0 {
		t.Fatalf("unmarked installed package unexpectedly rebuilt: %#v", result.Install)
	}
}

func TestResolveRetainsCompatiblePhaseEnvironmentABI(t *testing.T) {
	g := makeGraph()
	installed := pkg(g, "app-misc/example", "1", "0", "0", true, nil)
	installed.InstalledPhaseEnvABI = "2"

	result, err := Resolve(g, []string{"app-misc/example"}, DefaultResolveConfig())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(result.Install) != 0 {
		t.Fatalf("compatible ABI-2 artifact unexpectedly rebuilt: %#v", result.Install)
	}
}

func TestExpandTargetsPreservedRebuildUsesSetSemantics(t *testing.T) {
	g := makeGraph()
	cfg := DefaultResolveConfig()
	var expanded string
	cfg.PackageSetExpander = func(name string) ([]string, error) {
		expanded = name
		return []string{"dev-build/cmake-4.3.4"}, nil
	}
	r := &resolver{
		graph:              g,
		config:             cfg,
		toInstall:          make(map[string]*PkgAction),
		toUninstall:        make(map[string]*PkgAction),
		conflicts:          []string{},
		seenDeps:           make(map[string]bool),
		explicitTargets:    make(map[string]bool),
		backtrackRemaining: cfg.Backtrack,
	}

	atoms, err := r.expandTargets([]string{"@preserved-rebuild"})
	if err != nil {
		t.Fatalf("expand preserved-rebuild: %v", err)
	}
	if expanded != "@preserved-rebuild" || len(atoms) != 1 || atoms[0].String() != "=dev-build/cmake-4.3.4" {
		t.Fatalf("expansion=%q atoms=%v", expanded, atoms)
	}
	if !r.config.Reinstall || !r.config.Oneshot {
		t.Fatalf("preserved-rebuild flags: reinstall=%v oneshot=%v", r.config.Reinstall, r.config.Oneshot)
	}
	if !r.explicitTargets["dev-build/cmake"] {
		t.Fatal("expanded package was not marked as an explicit rebuild target")
	}
}

func TestParseGeneratedSetAtomRetainsExplicitConstraint(t *testing.T) {
	a, err := parseGeneratedSetAtom(">=dev-build/cmake-4.3")
	if err != nil {
		t.Fatalf("parse generated set atom: %v", err)
	}
	if got := a.String(); got != ">=dev-build/cmake-4.3" {
		t.Fatalf("atom=%q", got)
	}
}

func TestExpandTargetsPreservedRebuildEmpty(t *testing.T) {
	cfg := DefaultResolveConfig()
	cfg.PackageSetExpander = func(name string) ([]string, error) { return nil, nil }
	r := &resolver{
		graph:              makeGraph(),
		config:             cfg,
		toInstall:          make(map[string]*PkgAction),
		toUninstall:        make(map[string]*PkgAction),
		conflicts:          []string{},
		seenDeps:           make(map[string]bool),
		explicitTargets:    make(map[string]bool),
		backtrackRemaining: cfg.Backtrack,
	}

	atoms, err := r.expandTargets([]string{"@preserved-rebuild"})
	if err != nil {
		t.Fatalf("expand empty preserved-rebuild: %v", err)
	}
	if len(atoms) != 0 {
		t.Fatalf("empty preserved-rebuild expanded to %v", atoms)
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

func TestVersionDependenciesMentionTransactionIsSlotAware(t *testing.T) {
	vi := &VersionInfo{InstalledRdepend: `~llvm-core/llvm-19.1.7:${LLVM_MAJOR}=[debug=,${MULTILIB_USEDEP}]`}
	remove13, err := atom.Parse("=llvm-core/llvm-13.0.1")
	if err != nil {
		t.Fatal(err)
	}
	remove19, err := atom.Parse("=llvm-core/llvm-19.1.7")
	if err != nil {
		t.Fatal(err)
	}
	if versionDependenciesMentionTransaction(vi, nil, map[string]*PkgAction{"13": {Atom: remove13, Slot: "13"}}) {
		t.Fatal("LLVM 19 dependency treated as affected by LLVM 13 removal")
	}
	if !versionDependenciesMentionTransaction(vi, nil, map[string]*PkgAction{"19": {Atom: remove19, Slot: "19"}}) {
		t.Fatal("LLVM 19 dependency did not match LLVM 19 removal")
	}
	vi.InstalledRdepend = "llvm-core/llvm"
	if !versionDependenciesMentionTransaction(vi, nil, map[string]*PkgAction{"13": {Atom: remove13, Slot: "13"}}) {
		t.Fatal("unversioned dependency did not match slotted removal")
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

func benchmarkBacktrackingGraph(count int) *DepGraph {
	g := makeGraph()
	pkg(g, "app-misc/root", "1", "0", "0", false, nil)
	for i := 0; i < count; i++ {
		brokenCP := fmt.Sprintf("app-misc/broken-x%d", i)
		workingCP := fmt.Sprintf("app-misc/working-x%d", i)
		broken := pkg(g, brokenCP, "1", "0", "0", false, nil)
		broken.Rdepend = fmt.Sprintf("app-misc/missing-x%d", i)
		pkg(g, workingCP, "1", "0", "0", false, nil)
		anyOf(g, "app-misc/root", DepTypeRuntime, anyOfDep(brokenCP), anyOfDep(workingCP))
	}
	return g
}

func BenchmarkResolve_ForcedBacktracking(b *testing.B) {
	for _, count := range []int{20, 100, 1000} {
		b.Run(fmt.Sprintf("decisions-%d", count), func(b *testing.B) {
			g := benchmarkBacktrackingGraph(count)
			cfg := DefaultResolveConfig()
			cfg.Backtrack = count + 1
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := Resolve(g, []string{"app-misc/root"}, cfg)
				if err != nil {
					b.Fatal(err)
				}
				if result.BacktrackLevel != count {
					b.Fatalf("backtrack level = %d, want %d", result.BacktrackLevel, count)
				}
			}
		})
	}
}

func TestResolve_BacktrackLimitIsHardCeiling(t *testing.T) {
	g := benchmarkBacktrackingGraph(3)
	cfg := DefaultResolveConfig()
	cfg.Backtrack = 1
	cfg.KeepGoing = true
	result, err := Resolve(g, []string{"app-misc/root"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.BacktrackLevel > cfg.Backtrack {
		t.Fatalf("backtrack level %d exceeded configured ceiling %d", result.BacktrackLevel, cfg.Backtrack)
	}
}

func TestResolveEmptyTreeSkipsInstalledParentRefresh(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-misc/target", "1", "0", "0", true, nil)
	pkg(g, "app-misc/target", "1", "0", "0", false, nil)
	cfg := DefaultResolveConfig()
	cfg.EmptyTree = true
	cfg.Update = true
	cfg.NewUse = true
	result, err := Resolve(g, []string{"app-misc/target"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.Metrics.DirectUpdateRefresh != 0 {
		t.Fatalf("empty-tree ran installed-parent refresh: %s", result.Metrics.DirectUpdateRefresh)
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

func TestGetUseFlagsAppliesProfileForceAndMaskLast(t *testing.T) {
	r := &resolver{portageConfig: &portage.Config{
		USE:        []string{"masked", "-forced"},
		PackageUse: map[string][]string{"app-editors/vim": {"masked", "-forced", "local"}},
		UseForce:   []string{"forced"}, UseMask: []string{"masked"},
		PackageUseForce: map[string][]string{"app-editors/vim": {"pkgforced"}},
		PackageUseMask:  map[string][]string{"app-editors/vim": {"local"}},
	}}
	got := r.getUseFlags("app-editors/vim")
	want := map[string]bool{"masked": false, "forced": true, "local": false, "pkgforced": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effective USE = %v, want %v", got, want)
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
	if result == nil || result.Verified || result.Verification != VerificationIncomplete || len(result.Conflicts) == 0 {
		t.Errorf("expected non-executable result retaining masked-package conflict, got %#v", result)
	}
}

func TestResolve_UpdateKeepsInstalledMaskedWorldPackage(t *testing.T) {
	g := makeGraph()
	pkg(g, "net-dns/bind-tools", "9.18.0-r1", "0", "0", true, nil)
	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.PortageConfig = &portage.Config{PackageMask: []string{"net-dns/bind-tools"}}

	result, err := Resolve(g, []string{"net-dns/bind-tools"}, cfg)
	if err != nil {
		t.Fatalf("installed masked package should be retained during update: %v", err)
	}
	if len(result.Install) != 0 || len(result.Conflicts) != 0 || len(result.Warnings) != 1 {
		t.Fatalf("unexpected result: install=%v conflicts=%v warnings=%v", result.Install, result.Conflicts, result.Warnings)
	}
}

func TestResolveVersionMaskFallsBackToOlderCandidate(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)
	pkg(g, "app-editors/vim", "9.1", "0", "0", false, nil)
	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{PackageMask: []string{">=app-editors/vim-9.1"}}

	result, err := Resolve(g, []string{"app-editors/vim"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Install) != 1 || result.Install[0].Atom.Version.Raw != "9.0" {
		t.Fatalf("install = %v, want vim-9.0", collectCPV(result.Install))
	}
}

func TestResolveVersionUnmaskRestoresCandidate(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.0", "0", "0", false, nil)
	pkg(g, "app-editors/vim", "9.1", "0", "0", false, nil)
	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{
		PackageMask:   []string{">=app-editors/vim-9.1"},
		PackageUnmask: []string{"=app-editors/vim-9.1"},
	}

	result, err := Resolve(g, []string{"app-editors/vim"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Install) != 1 || result.Install[0].Atom.Version.Raw != "9.1" {
		t.Fatalf("install = %v, want vim-9.1", collectCPV(result.Install))
	}
}

func TestResolveMaskedDiagnosticIdentifiesAtomAndVersion(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-editors/vim", "9.1", "0", "0", false, nil)
	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{PackageMask: []string{"=app-editors/vim-9.1"}}

	_, err := Resolve(g, []string{"app-editors/vim"}, cfg)
	if err == nil || !strings.Contains(err.Error(), "app-editors/vim-9.1 by package.mask atom =app-editors/vim-9.1") {
		t.Fatalf("mask diagnostic = %v", err)
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

func TestCandidateUseFlagsExcludeUnrelatedGlobalFlags(t *testing.T) {
	g := makeGraph()
	vi := pkg(g, "app-misc/example", "1", "0", "0", false, map[string]bool{"feature": false, "debug": false})
	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{USE: []string{"feature", "unrelated", "-debug"}}
	r := &resolver{portageConfig: cfg.PortageConfig, baseUseCache: make(map[string]map[string]bool), useOverrides: make(map[string]map[string]bool)}
	flags := r.candidateUseFlags(g.Packages["app-misc/example"], vi)
	if !flags["feature"] || flags["debug"] {
		t.Fatalf("declared effective USE = %#v", flags)
	}
	if _, leaked := flags["unrelated"]; leaked {
		t.Fatalf("unrelated global USE leaked into package state: %#v", flags)
	}
}

func TestCandidateUseFlagsIUSEDefaultOutranksProfileButNotUser(t *testing.T) {
	g := makeGraph()
	vi := pkg(g, "dev-libs/libnl", "3.12.0", "3", "3", false, map[string]bool{"debug": true})
	node := g.Packages["dev-libs/libnl"]

	profile := &portage.Config{USE: []string{"-debug"}}
	r := &resolver{portageConfig: profile, baseUseByVersion: make(map[*VersionInfo]map[string]bool), useOverrides: make(map[string]map[string]bool)}
	if flags := r.candidateUseFlags(node, vi); !flags["debug"] {
		t.Fatalf("profile default erased IUSE=+debug: %#v", flags)
	}

	user := &portage.Config{USE: []string{"-debug"}, UserUSE: []string{"-debug"}}
	r = &resolver{portageConfig: user, baseUseByVersion: make(map[*VersionInfo]map[string]bool), useOverrides: make(map[string]map[string]bool)}
	if flags := r.candidateUseFlags(node, vi); flags["debug"] {
		t.Fatalf("explicit user -debug did not override IUSE=+debug: %#v", flags)
	}
}

func TestDeepUpdateUnqualifiedDependencyEscapesScheduledOldSlot(t *testing.T) {
	g := makeGraph()
	pkg(g, "llvm-runtimes/clang-runtime", "21.1.8", "21", "21", true, nil)
	pkg(g, "llvm-runtimes/clang-runtime", "22", "22", "22", false, nil)
	pkg(g, "llvm-core/clang", "21.1.8", "21", "21", true, nil)
	pkg(g, "llvm-core/clang-common", "21.1.8", "0", "0", true, nil)
	pkg(g, "llvm-core/clang-common", "22.1.8", "0", "0", false, nil)
	deps(g, "llvm-core/clang", "~llvm-runtimes/clang-runtime-21.1.8:21")
	deps(g, "llvm-core/clang", "llvm-core/clang-common")
	deps(g, "llvm-core/clang-common", "llvm-runtimes/clang-runtime")

	cfg := DefaultResolveConfig()
	cfg.Update, cfg.Deep = true, true
	result, err := Resolve(g, []string{"llvm-core/clang"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	found22 := false
	for _, action := range result.Install {
		found22 = found22 || action.Atom.CP() == "llvm-runtimes/clang-runtime" && action.Slot == "22"
	}
	if !found22 {
		t.Fatalf("unqualified updated dependency remained frozen at slot 21: %#v", result.Install)
	}
}

func TestNewUseIgnoresNewlyMaskedIUSEFlag(t *testing.T) {
	g := makeGraph()
	installed := pkg(g, "app-crypt/pinentry", "1", "0", "0", true, map[string]bool{"gtk": true})
	installed.InstalledIUseFlags = map[string]bool{"gtk": true}
	candidate := pkg(g, "app-crypt/pinentry", "2", "0", "0", false, map[string]bool{"gtk": true, "selinux": false})
	r := &resolver{portageConfig: &portage.Config{UseMask: []string{"selinux"}}}
	if r.newUseChanged(g.Packages["app-crypt/pinentry"], installed, candidate, candidate.UseFlags) {
		t.Fatal("new masked IUSE flag triggered --newuse")
	}
}

func TestCandidateUseFlagsIncludeImplicitUseExpandFlags(t *testing.T) {
	g := makeGraph()
	vi := pkg(g, "app-misc/example", "1", "0", "0", false, map[string]bool{"feature": false})
	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{
		USE:               []string{"abi_x86_64", "abi_x86_32", "unrelated_value"},
		UseExpandImplicit: []string{"ABI_X86"},
	}
	r := &resolver{portageConfig: cfg.PortageConfig, baseUseCache: make(map[string]map[string]bool), useOverrides: make(map[string]map[string]bool)}
	flags := r.candidateUseFlags(g.Packages["app-misc/example"], vi)
	if !flags["abi_x86_64"] || !flags["abi_x86_32"] {
		t.Fatalf("implicit USE_EXPAND flags absent: %#v", flags)
	}
	if _, leaked := flags["unrelated_value"]; leaked {
		t.Fatalf("unrelated flag leaked into package state: %#v", flags)
	}
}

func TestNormalizedImplicitUsePrefixesCanonicalizesConfigurationOnce(t *testing.T) {
	cfg := &portage.Config{UseExpandImplicit: []string{" ABI_X86 ", "", "ArCh"}}
	got := normalizedImplicitUsePrefixes(cfg)
	want := []string{"abi_x86_", "arch_"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("implicit prefixes = %#v, want %#v", got, want)
	}
}

func TestPropertyCachedImplicitUseExpansionMatchesConfigurationSemantics(t *testing.T) {
	variables := []string{"ABI_X86", " ARCH ", "ELIBC", "", "   "}
	flags := []string{"abi_x86_64", "abi_x86_", "ABI_X86_64", "arch_amd64", "elibc_glibc", "unrelated", "_hostile"}
	cfg := &portage.Config{UseExpandImplicit: variables}
	r := &resolver{portageConfig: cfg, implicitUsePrefixes: normalizedImplicitUsePrefixes(cfg)}
	for _, flag := range flags {
		want := false
		for _, variable := range variables {
			prefix := strings.ToLower(strings.TrimSpace(variable)) + "_"
			if prefix != "_" && strings.HasPrefix(flag, prefix) && len(flag) > len(prefix) {
				want = true
			}
		}
		if got := r.implicitUseExpandFlag(flag); got != want {
			t.Fatalf("implicitUseExpandFlag(%q) = %t, want %t", flag, got, want)
		}
	}
}

func TestMutationImplicitUsePrefixRequiresValueAfterSeparator(t *testing.T) {
	cfg := &portage.Config{UseExpandImplicit: []string{"ABI_X86"}}
	r := &resolver{portageConfig: cfg}
	if r.implicitUseExpandFlag("abi_x86_") {
		t.Fatal("empty implicit USE_EXPAND value was accepted")
	}
	if !r.implicitUseExpandFlag("abi_x86_64") {
		t.Fatal("valid implicit USE_EXPAND value was rejected")
	}
	if r.implicitUseExpandFlag("xabi_x86_64") {
		t.Fatal("non-prefix USE_EXPAND fragment was accepted")
	}
}

func TestCandidateUseFlagsApplyStablePolicyOnlyToStableVersion(t *testing.T) {
	g := makeGraph()
	stable := pkgKeywords(g, "dev-lang/python", "3.12", "3.12", "3.12", false,
		map[string]bool{"ssl": false, "test": true, "ensurepip": false}, "amd64")
	unstable := pkgKeywords(g, "dev-lang/python", "3.13", "3.13", "3.13", false,
		map[string]bool{"ssl": false, "test": true, "ensurepip": false}, "~amd64")
	cfg := &portage.Config{
		MakeConf:       map[string]string{"ARCH": "amd64"},
		UseStableForce: []string{"ssl"}, UseStableMask: []string{"test"},
		PackageUseStableForceRules: []portage.PackageUseRule{{Atom: "dev-lang/python", Flags: []string{"ensurepip"}}},
	}
	r := &resolver{portageConfig: cfg, baseUseCache: make(map[string]map[string]bool), useOverrides: make(map[string]map[string]bool)}
	stableFlags := r.candidateUseFlags(g.Packages["dev-lang/python"], stable)
	if !stableFlags["ssl"] || stableFlags["test"] || !stableFlags["ensurepip"] {
		t.Fatalf("stable flags = %#v", stableFlags)
	}
	unstableFlags := r.candidateUseFlags(g.Packages["dev-lang/python"], unstable)
	if unstableFlags["ssl"] || !unstableFlags["test"] || unstableFlags["ensurepip"] {
		t.Fatalf("unstable flags = %#v", unstableFlags)
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

func TestResolve_PackageAcceptKeywordsSelectsTestingUpdate(t *testing.T) {
	g := makeGraph()
	pkgKeywords(g, "dev-lang/go", "1.26.3", "0", "0", true, nil, "amd64")
	pkgKeywords(g, "dev-lang/go", "1.26.4", "0", "0", false, nil, "~amd64")
	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.PortageConfig = &portage.Config{
		MakeConf:              map[string]string{"ARCH": "amd64"},
		ACCEPT_KEYWORDS:       []string{"amd64"},
		PackageAcceptKeywords: map[string]string{"dev-lang/go": "~amd64"},
	}

	result, err := Resolve(g, []string{"dev-lang/go"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Install) != 1 || result.Install[0].Atom.Version.Raw != "1.26.4" {
		t.Fatalf("package keyword exception was ignored: %v", result.Install)
	}
}

func TestResolve_PackageAcceptKeywordsOrderedRemovalAndException(t *testing.T) {
	g := makeGraph()
	pkgKeywords(g, "dev-lang/python", "3.12", "0", "0", false, nil, "amd64")
	pkgKeywords(g, "dev-lang/python", "3.13", "0", "0", false, nil, "~amd64")
	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{
		MakeConf: map[string]string{"ARCH": "amd64"},
		PackageAcceptKeywordRules: []portage.PackageUseRule{
			{Atom: "dev-lang/python", Flags: []string{"~amd64"}},
			{Atom: ">=dev-lang/python-3.13", Flags: []string{"-~amd64"}},
		},
	}
	result, err := Resolve(g, []string{"dev-lang/python"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Install) != 1 || result.Install[0].Atom.Version.Raw != "3.12" {
		t.Fatalf("ordered keyword removal ignored: %v", result.Install)
	}
}

func TestResolve_PackageAcceptKeywordsEmptyRule(t *testing.T) {
	g := makeGraph()
	pkgKeywords(g, "dev-lang/python", "3.13", "0", "0", false, nil, "~amd64")
	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{
		MakeConf:                  map[string]string{"ARCH": "amd64"},
		PackageAcceptKeywordRules: []portage.PackageUseRule{{Atom: "dev-lang/python"}},
	}
	result, err := Resolve(g, []string{"dev-lang/python"}, cfg)
	if err != nil || len(result.Install) != 1 {
		t.Fatalf("empty package.accept_keywords rule did not accept ~amd64: result=%v err=%v", result, err)
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

func TestVirtualPackage_ProviderMustSatisfyConstraint(t *testing.T) {
	g := makeGraph()
	consumer := pkg(g, "app-shells/consumer", "1", "0", "0", false, nil)
	consumer.Rdepend = ">=virtual/rust-1.80[llvm]"
	pkg(g, "dev-lang/rust-bin", "1.70", "0", "0", false, map[string]bool{"llvm": true})
	pkg(g, "dev-lang/rust", "1.85", "0", "0", false, map[string]bool{"llvm": true})
	g.AddProvider("virtual/rust", "dev-lang/rust-bin")
	g.AddProvider("virtual/rust", "dev-lang/rust")

	result, err := Resolve(g, []string{"app-shells/consumer"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	var oldProvider, matchingProvider bool
	for _, action := range result.Install {
		oldProvider = oldProvider || action.Atom.CP() == "dev-lang/rust-bin"
		matchingProvider = matchingProvider || action.Atom.CP() == "dev-lang/rust"
	}
	if oldProvider || !matchingProvider {
		t.Fatalf("selected provider that does not satisfy virtual constraint: %v", result.Install)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("provider verification disagreed with selection: %v", result.Conflicts)
	}
}

func TestVirtualPackage_BacktracksWhenPreferredProviderDepsFail(t *testing.T) {
	g := makeGraph()
	consumer := pkg(g, "app-shells/consumer", "1", "0", "0", false, nil)
	consumer.Rdepend = "virtual/editor"
	broken := pkg(g, "app-editors/broken", "1", "0", "0", false, nil)
	broken.Rdepend = "dev-libs/missing"
	pkg(g, "app-editors/working", "1", "0", "0", false, nil)
	g.AddProvider("virtual/editor", "app-editors/broken")
	g.AddProvider("virtual/editor", "app-editors/working")

	result, err := Resolve(g, []string{"app-shells/consumer"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	var selectedBroken, selectedWorking bool
	for _, action := range result.Install {
		selectedBroken = selectedBroken || action.Atom.CP() == "app-editors/broken"
		selectedWorking = selectedWorking || action.Atom.CP() == "app-editors/working"
	}
	if selectedBroken || !selectedWorking {
		t.Fatalf("provider branch was not rolled back: %v", result.Install)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("failed provider leaked conflicts: %v", result.Conflicts)
	}
	if result.BacktrackLevel != 1 {
		t.Fatalf("backtrack level = %d, want 1", result.BacktrackLevel)
	}
	if len(result.DecisionHistory) != 1 || result.DecisionHistory[0] != (BacktrackDecision{
		Kind: "provider", Key: "provider:app-shells/consumer->virtual/editor", From: "app-editors/broken", To: "app-editors/working",
	}) {
		t.Fatalf("provider decision history = %#v", result.DecisionHistory)
	}
	var ledgerWorking bool
	for _, record := range result.DecisionLedger.Records {
		if strings.HasPrefix(record.CPV, "app-editors/broken-") {
			t.Fatalf("rolled-back provider leaked into committed decision ledger: %#v", record)
		}
		if strings.HasPrefix(record.CPV, "app-editors/working-") && record.Outcome == DecisionSelected {
			ledgerWorking = true
		}
	}
	if !ledgerWorking {
		t.Fatalf("committed provider missing from decision ledger: %#v", result.DecisionLedger)
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

func TestCheckRequiredUse_AtMostOneOf(t *testing.T) {
	tests := []struct {
		name  string
		flags map[string]bool
		valid bool
	}{
		{name: "none", flags: map[string]bool{"foo": false, "bar": false}, valid: true},
		{name: "one", flags: map[string]bool{"foo": true, "bar": false}, valid: true},
		{name: "two", flags: map[string]bool{"foo": true, "bar": true}, valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := CheckRequiredUse("?? ( foo bar )", test.flags)
			if (err == nil) != test.valid {
				t.Fatalf("CheckRequiredUse() error = %v, want valid %v", err, test.valid)
			}
		})
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

func TestLicenseAccepted_LaterRuleOverridesRemoval(t *testing.T) {
	if !LicenseAccepted("MIT", []string{"-*", "MIT", "-MIT", "MIT"}) {
		t.Error("later explicit license should override an earlier removal")
	}
	if LicenseAccepted("MIT", []string{"-*", "MIT", "-MIT"}) {
		t.Error("later removal should override an earlier explicit license")
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

func TestLicenseExpressionAccepted(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		accepted   []string
		use        map[string]bool
		want       bool
	}{
		{name: "all terms", expression: "MIT GPL-2", accepted: []string{"-*", "MIT", "GPL-2"}, want: true},
		{name: "missing required term", expression: "MIT GPL-2", accepted: []string{"-*", "MIT"}, want: false},
		{name: "alternative", expression: "|| ( MIT GPL-2 )", accepted: []string{"-*", "MIT"}, want: true},
		{name: "active conditional", expression: "ssl? ( OpenSSL ) MIT", accepted: []string{"-*", "MIT"}, use: map[string]bool{"ssl": true}, want: false},
		{name: "inactive conditional", expression: "ssl? ( OpenSSL ) MIT", accepted: []string{"-*", "MIT"}, use: map[string]bool{"ssl": false}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := LicenseExpressionAccepted(test.expression, test.accepted, test.use); got != test.want {
				t.Fatalf("LicenseExpressionAccepted() = %v, want %v", got, test.want)
			}
		})
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

func TestResolve_PackageLicenseFullAtomException(t *testing.T) {
	g := makeGraph()
	vi := pkgWithMeta(g, "dev-lang/oracle-jdk-bin", "22", "0", "0", false, nil, "", "Oracle-No-Fee-Terms-and-Conditions")
	vi.Repository = "gentoo"
	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{
		ACCEPT_LICENSE: []string{"-*"},
		PackageLicenseRules: []portage.PackageUseRule{{
			Atom: "=dev-lang/oracle-jdk-bin-22::gentoo", Flags: []string{"Oracle-No-Fee-Terms-and-Conditions"},
		}},
	}
	result, err := Resolve(g, []string{"dev-lang/oracle-jdk-bin"}, cfg)
	if err != nil || len(result.Install) != 1 {
		t.Fatalf("package.license exception failed: result=%v err=%v", result, err)
	}
}

func TestResolve_LicenseGroupAccepted(t *testing.T) {
	g := makeGraph()
	pkgWithMeta(g, "dev-libs/libfoo", "1", "0", "0", false, nil, "", "MIT")
	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{
		ACCEPT_LICENSE: []string{"-*", "@FREE"},
		LicenseGroups:  map[string][]string{"FREE": {"MIT", "BSD"}},
	}
	result, err := Resolve(g, []string{"dev-libs/libfoo"}, cfg)
	if err != nil || len(result.Install) != 1 {
		t.Fatalf("license group was not expanded: result=%v err=%v", result, err)
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
	path := filepath.Join(t.TempDir(), "resume")
	if err := MarkResumeComplete(path, "some-atom"); err != nil {
		t.Fatalf("expected no error for nonexistent file, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// SkipFirstResume on nonexistent file
// ---------------------------------------------------------------------------

func TestResume_SkipFirst_Nonexistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume")
	if err := SkipFirstResume(path); err != nil {
		t.Fatalf("expected no error for nonexistent file, got: %v", err)
	}
}

func TestResume_Schema_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resume")

	state := ResumeState{
		Packages: []ResumePackage{
			{CPV: "app-misc/foo-1.0", Atom: "app-misc/foo-1.0", Completed: false},
			{CPV: "dev-libs/bar-2.0", Atom: "dev-libs/bar-2.0", Completed: false},
		},
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, data, 0644)

	loaded, err := LoadResume(path)
	if err != nil {
		t.Fatalf("LoadResume: %v", err)
	}
	if len(loaded) != 2 {
		t.Errorf("expected 2 atoms, got %d", len(loaded))
	}
	if loaded[0] != "app-misc/foo-1.0" {
		t.Errorf("first atom = %q, want app-misc/foo-1.0", loaded[0])
	}
}

func TestResume_Schema_EmptyPackages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resume")

	os.WriteFile(path, []byte(`{"packages":[]}`), 0644)

	loaded, err := LoadResume(path)
	if err != nil {
		t.Fatalf("LoadResume: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("expected 0 atoms, got %d", len(loaded))
	}
}

func TestResume_Schema_MissingCompletedField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resume")

	os.WriteFile(path, []byte(`{"packages":[{"cpv":"foo/bar-1.0","atom":"foo/bar-1.0"}]}`), 0644)

	loaded, err := LoadResume(path)
	if err != nil {
		t.Fatalf("LoadResume: %v", err)
	}
	if len(loaded) != 1 {
		t.Errorf("expected 1 atom, got %d", len(loaded))
	}
	if loaded[0] != "foo/bar-1.0" {
		t.Errorf("atom = %q, want foo/bar-1.0", loaded[0])
	}
}

func TestResume_Schema_CorruptJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resume")

	os.WriteFile(path, []byte(`{this is not json}`), 0644)

	_, err := LoadResume(path)
	if err == nil {
		t.Error("expected error for corrupt JSON")
	}
}

func TestResume_Schema_MissingPackagesField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resume")

	os.WriteFile(path, []byte(`{"other": "data"}`), 0644)

	if _, err := LoadResume(path); err == nil {
		t.Fatal("LoadResume accepted a state without packages")
	}
}

func TestResume_SchemaRejectsAdversarialInputs(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{"trailing document", `{"packages":[]} {"packages":[]}`},
		{"unknown field", `{"packages":[],"status":"trusted"}`},
		{"empty atom", `{"packages":[{"cpv":"cat/pkg-1","atom":"","completed":false}]}`},
		{"whitespace atom", `{"packages":[{"cpv":"cat/pkg-1","atom":"  ","completed":false}]}`},
		{"duplicate atom", `{"packages":[{"atom":"cat/pkg-1"},{"atom":"cat/pkg-1"}]}`},
		{"truncated", `{"packages":[{"atom":"cat/pkg-1"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "resume")
			if err := os.WriteFile(path, []byte(test.data), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadResume(path); err == nil {
				t.Fatalf("LoadResume accepted %s", test.name)
			}
		})
	}
}

func TestResumeConcurrentCompletionsDoNotLoseUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume")
	const packages = 32
	result := &ResolveResult{Install: make([]PkgAction, 0, packages)}
	for index := 0; index < packages; index++ {
		result.Install = append(result.Install, PkgAction{
			Atom: mustParse(fmt.Sprintf("app-misc/pkg%d-1", index)),
		})
	}
	if err := SaveResume(path, result); err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	errors := make(chan error, packages)
	for _, action := range result.Install {
		wait.Add(1)
		go func(completed string) {
			defer wait.Done()
			errors <- MarkResumeComplete(path, completed)
		}(action.Atom.String())
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent completion: %v", err)
		}
	}
	remaining, err := LoadResume(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("lost concurrent updates; remaining = %v", remaining)
	}
}

func TestResumeSaveRejectsInvalidActionsWithoutReplacingState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume")
	original := &ResolveResult{Install: []PkgAction{{Atom: mustParse("app-misc/original-1")}}}
	if err := SaveResume(path, original); err != nil {
		t.Fatal(err)
	}
	if err := SaveResume(path, &ResolveResult{Install: []PkgAction{{Atom: nil}}}); err == nil {
		t.Fatal("SaveResume accepted a nil action atom")
	}
	remaining, err := LoadResume(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(remaining, []string{"app-misc/original-1"}) {
		t.Fatalf("invalid save replaced durable state: %v", remaining)
	}
}

type failingResumeFile struct {
	resumeFile
	stage  string
	closes *int
}

func (f failingResumeFile) Write(data []byte) (int, error) {
	if f.stage == "write" {
		return 0, errors.New("injected write failure")
	}
	return f.resumeFile.Write(data)
}

func (f failingResumeFile) Chmod(mode os.FileMode) error {
	if f.stage == "chmod" {
		return errors.New("injected chmod failure")
	}
	return f.resumeFile.Chmod(mode)
}

func (f failingResumeFile) Sync() error {
	if f.stage == "file sync" {
		return errors.New("injected file sync failure")
	}
	return f.resumeFile.Sync()
}

func (f failingResumeFile) Close() error {
	*f.closes++
	err := f.resumeFile.Close()
	if f.stage == "file close" {
		return errors.New("injected file close failure")
	}
	return err
}

type failingResumeDirectory struct {
	resumeDirectory
	stage  string
	closes *int
}

func (d failingResumeDirectory) Sync() error {
	if d.stage == "directory sync" {
		return errors.New("injected directory sync failure")
	}
	return d.resumeDirectory.Sync()
}

func (d failingResumeDirectory) Close() error {
	*d.closes++
	err := d.resumeDirectory.Close()
	if d.stage == "directory close" {
		return errors.New("injected directory close failure")
	}
	return err
}

func TestResumeWriteFaultsLeaveCompleteOldOrNewState(t *testing.T) {
	oldState := ResumeState{Packages: []ResumePackage{{Atom: "app-misc/old-1"}}}
	newState := ResumeState{Packages: []ResumePackage{{Atom: "app-misc/new-1"}}}
	for _, stage := range []string{"create", "chmod", "write", "file sync", "file close", "rename", "open directory", "directory sync", "directory close"} {
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "resume")
			if err := writeResumeState(path, oldState); err != nil {
				t.Fatal(err)
			}
			fileCloses, directoryCloses := 0, 0
			operations := resumeIO{
				createTemp: func(directory, pattern string) (resumeFile, error) {
					if stage == "create" {
						return nil, errors.New("injected create failure")
					}
					file, err := os.CreateTemp(directory, pattern)
					if err != nil {
						return nil, err
					}
					return failingResumeFile{resumeFile: file, stage: stage, closes: &fileCloses}, nil
				},
				rename: func(oldPath, newPath string) error {
					if stage == "rename" {
						return errors.New("injected rename failure")
					}
					return os.Rename(oldPath, newPath)
				},
				openDir: func(directory string) (resumeDirectory, error) {
					if stage == "open directory" {
						return nil, errors.New("injected directory open failure")
					}
					dir, err := os.Open(directory)
					if err != nil {
						return nil, err
					}
					return failingResumeDirectory{resumeDirectory: dir, stage: stage, closes: &directoryCloses}, nil
				},
				remove: os.Remove,
			}
			if err := writeResumeStateWithIO(path, newState, operations); err == nil {
				t.Fatalf("write succeeded through injected %s failure", stage)
			}
			remaining, err := LoadResume(path)
			if err != nil {
				t.Fatalf("fault left corrupt state: %v", err)
			}
			if !reflect.DeepEqual(remaining, []string{"app-misc/old-1"}) &&
				!reflect.DeepEqual(remaining, []string{"app-misc/new-1"}) {
				t.Fatalf("fault left mixed state: %v", remaining)
			}
			if stage != "create" && fileCloses != 1 {
				t.Fatalf("temporary file close count = %d, want 1", fileCloses)
			}
			if (stage == "directory sync" || stage == "directory close") && directoryCloses != 1 {
				t.Fatalf("directory close count = %d, want 1", directoryCloses)
			}
		})
	}
}

func TestResumeWriteFormatIsStableAndReviewable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume")
	state := ResumeState{Packages: []ResumePackage{{CPV: "app-misc/pkg-1", Atom: "app-misc/pkg-1"}}}
	if err := writeResumeState(path, state); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\n  \"packages\": [\n") || data[len(data)-1] != '\n' {
		t.Fatalf("resume JSON lost stable indentation or trailing newline:\n%s", data)
	}
}

func TestResume_Schema_SaveLoadMatch(t *testing.T) {
	g := makeGraph()
	vi := pkg(g, "app-misc/roundtrip", "1.0", "0", "0", false, nil)
	_ = vi

	result := &ResolveResult{
		Install: []PkgAction{
			{Atom: mustParse("app-misc/roundtrip-1.0"), Action: "install", Reason: "test", Slot: "0", Subslot: "0"},
		},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "resume")

	if err := SaveResume(path, result); err != nil {
		t.Fatalf("SaveResume: %v", err)
	}

	loaded, err := LoadResume(path)
	if err != nil {
		t.Fatalf("LoadResume: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 atom, got %d", len(loaded))
	}
	if loaded[0] != "app-misc/roundtrip-1.0" {
		t.Errorf("atom = %q, want app-misc/roundtrip-1.0", loaded[0])
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

func TestResolveMarksNormalPlanVerifiedAndNodepsPlanSkipped(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/libfoo", "2.0", "0", "0", false, nil)

	verified, err := Resolve(g, []string{"dev-libs/libfoo"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !verified.Verified || verified.Verification != VerificationVerified {
		t.Fatalf("normal plan verification = %t/%q", verified.Verified, verified.Verification)
	}

	cfg := DefaultResolveConfig()
	cfg.NoDeps = true
	skipped, err := Resolve(g, []string{"dev-libs/libfoo"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if skipped.Verified || skipped.Verification != VerificationSkippedNoDeps {
		t.Fatalf("nodeps plan verification = %t/%q", skipped.Verified, skipped.Verification)
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

func TestFormatTreeShowsOnlyPlannedSelectedVersions(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-misc/root", "1", "0", "0", false, nil)
	pkg(g, "dev-libs/dependency", "1", "0", "0", false, nil)
	pkg(g, "dev-libs/dependency", "9999", "0", "0", false, nil)
	pkg(g, "dev-libs/unplanned", "1", "0", "0", false, nil)
	deps(g, "app-misc/root", "dev-libs/dependency")
	deps(g, "dev-libs/dependency", "dev-libs/unplanned")
	actions := []PkgAction{
		{Atom: mustParse("app-misc/root-1"), Action: "install"},
		{Atom: mustParse("dev-libs/dependency-1"), Action: "reinstall"},
	}

	output := FormatTree(actions, g)
	if strings.Contains(output, "dependency-9999") || strings.Contains(output, "dev-libs/unplanned") {
		t.Fatalf("tree leaked repository graph outside plan:\n%s", output)
	}
	if !strings.Contains(output, "dependency-1") || !strings.Contains(output, "[ebuild   R    ]") {
		t.Fatalf("tree omitted selected action metadata:\n%s", output)
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
	pkg(g, "new-dep/cat", "1", "0", "0", false, nil)

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
	pkg(g, "foo/bar", "1", "0", "0", true, nil)
	pkg(g, "baz/qux", "1", "0", "0", true, nil)

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

func TestDepsChangedUsesInstalledMetadataAndAllClasses(t *testing.T) {
	installed := &VersionInfo{
		Depend: "repository/current", InstalledDepend: "build/old",
		InstalledRdepend: "run/same", InstalledBdepend: "build/tool-old",
		InstalledIdepend: "install/same", InstalledPdepend: "post/same",
	}
	available := &VersionInfo{
		Depend: "build/old", Rdepend: "run/same", Bdepend: "build/tool-new",
		Idepend: "install/same", Pdepend: "post/same",
	}
	if !depsChanged(installed, available) {
		t.Fatal("BDEPEND-only change was missed or repository metadata was compared as installed state")
	}
	available.Bdepend = "build/tool-old"
	if depsChanged(installed, available) {
		t.Fatal("identical installed and candidate dependency classes reported changed")
	}
}

func TestResolve_UsesDependenciesFromSelectedVersion(t *testing.T) {
	g := makeGraph()
	stable := pkgKeywords(g, "media-gfx/editor", "1.0", "0", "0", false, nil, "amd64")
	stable.Rdepend = "dev-libs/stable-dep"
	live := pkgKeywords(g, "media-gfx/editor", "9999", "0", "0", false, nil, "~amd64")
	live.Rdepend = "dev-libs/live-dep"
	pkg(g, "dev-libs/stable-dep", "1", "0", "0", false, nil)
	pkg(g, "dev-libs/live-dep", "1", "0", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{ACCEPT_KEYWORDS: []string{"amd64"}}
	result, err := Resolve(g, []string{"media-gfx/editor"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var stableFound, liveFound bool
	for _, action := range result.Install {
		stableFound = stableFound || action.Atom.CP() == "dev-libs/stable-dep"
		liveFound = liveFound || action.Atom.CP() == "dev-libs/live-dep"
	}
	if !stableFound || liveFound {
		t.Fatalf("selected stable dependencies only: stable=%v live=%v", stableFound, liveFound)
	}
}

func TestResolve_UpdateDoesNotDeepUpdateSatisfiedDependency(t *testing.T) {
	g := makeGraph()
	oldApp := pkg(g, "app-misc/application", "1", "0", "0", true, nil)
	oldApp.Rdepend = ">=dev-libs/library-1"
	newApp := pkg(g, "app-misc/application", "2", "0", "0", false, nil)
	newApp.Rdepend = ">=dev-libs/library-1"
	pkg(g, "dev-libs/library", "1", "0", "0", true, nil)
	pkg(g, "dev-libs/library", "2", "0", "0", false, nil)
	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.Deep = false

	result, err := Resolve(g, []string{"app-misc/application"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var appUpdated, libraryUpdated bool
	for _, action := range result.Install {
		appUpdated = appUpdated || action.Atom.CP() == "app-misc/application" && action.Atom.Version.Raw == "2"
		libraryUpdated = libraryUpdated || action.Atom.CP() == "dev-libs/library" && action.Atom.Version.Raw == "2"
	}
	if !appUpdated || libraryUpdated {
		t.Fatalf("non-deep update changed a satisfied dependency: %v", result.Install)
	}
}

func TestResolve_EqualGlobWithConditionalUseDependency(t *testing.T) {
	g := makeGraph()
	consumer := pkg(g, "dev-python/bitstring", "4.4.0", "0", "0", false, map[string]bool{"python_targets_python3_14": true})
	consumer.Rdepend = "=dev-python/bitarray-3*[python_targets_python3_14(-)?]"
	pkg(g, "dev-python/bitarray", "3.8.1", "0", "0", false, map[string]bool{"python_targets_python3_14": true})

	result, err := Resolve(g, []string{"dev-python/bitstring"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, action := range result.Install {
		found = found || action.Atom.CP() == "dev-python/bitarray"
	}
	if !found {
		t.Fatalf("equal-glob dependency was not selected: %v", result.Install)
	}
}

func TestResolve_DependencyUseConstraintSurvivesCandidatePinning(t *testing.T) {
	g := makeGraph()
	consumer := pkg(g, "app-misc/consumer", "2", "0", "0", false, map[string]bool{"python": true})
	consumer.Rdepend = "dev-python/library[python_targets_python3_14]"
	installed := pkg(g, "dev-python/library", "1", "0", "0", true, nil)
	installed.InstalledUseFlags = map[string]bool{"python_targets_python3_14": false}
	available := pkg(g, "dev-python/library", "1", "0", "0", false, map[string]bool{"python_targets_python3_14": true})
	available.UseFlags = map[string]bool{"python_targets_python3_14": true}

	cfg := DefaultResolveConfig()
	cfg.Update, cfg.Deep, cfg.NewUse, cfg.CompleteGraph = true, true, true, true
	result, err := Resolve(g, []string{"app-misc/consumer"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, action := range result.Install {
		found = found || action.Atom.CP() == "dev-python/library"
	}
	if !found {
		t.Fatalf("USE-incompatible installed dependency was incorrectly accepted: %v", result.Install)
	}
}

func TestResolve_DependencyUseConstraintRebuildsCoalescedInstalledCandidate(t *testing.T) {
	g := makeGraph()
	consumer := pkg(g, "app-misc/consumer", "2", "0", "0", false, nil)
	consumer.Rdepend = "dev-python/library[python_targets_python3_14]"
	library := pkg(g, "dev-python/library", "1", "0", "0", true, map[string]bool{"python_targets_python3_14": true})
	// Live ingestion coalesces an installed CPV and the identical repository CPV
	// into one record, retaining distinct installed and candidate USE domains.
	library.Available = true
	library.InstalledUseFlags = map[string]bool{"python_targets_python3_14": false}
	library.InstalledIUseFlags = map[string]bool{"python_targets_python3_14": true}
	library.UseFlags = map[string]bool{"python_targets_python3_14": true}

	result, err := Resolve(g, []string{"app-misc/consumer"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, conflict := range result.Conflicts {
		if strings.Contains(conflict, "python_targets_python3_14") {
			t.Fatalf("coalesced candidate left an unsatisfied USE dependency: %v", result.Conflicts)
		}
	}
	for _, action := range result.Install {
		if action.Atom.CP() == "dev-python/library" && action.UseFlags["python_targets_python3_14"] {
			return
		}
	}
	t.Fatalf("USE-incompatible coalesced installed dependency was retained: %v", result.Install)
}

func TestResolve_DependencyUseConstraintUpgradesToNewerCandidate(t *testing.T) {
	g := makeGraph()
	oldConsumer := pkg(g, "app-misc/consumer", "1", "0", "0", true, map[string]bool{"python_targets_python3_13": true})
	oldConsumer.InstalledUseFlags = map[string]bool{"python_targets_python3_13": true}
	consumer := pkg(g, "app-misc/consumer", "2", "0", "0", false, map[string]bool{"python_targets_python3_14": true})
	consumer.Rdepend = ">=dev-python/library-1[python_targets_python3_14(-)]"
	installed := pkg(g, "dev-python/library", "1", "0", "0", true, map[string]bool{"python_targets_python3_14": false})
	installed.InstalledUseFlags = map[string]bool{"python_targets_python3_14": false}
	available := pkg(g, "dev-python/library", "2", "0", "0", false, map[string]bool{"python_targets_python3_14": true})
	available.Available = true

	cfg := DefaultResolveConfig()
	cfg.Update, cfg.Deep, cfg.NewUse, cfg.CompleteGraph = true, true, true, true
	result, err := Resolve(g, []string{"app-misc/consumer"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range result.Install {
		if action.Atom.CP() == "dev-python/library" && action.Atom.Version.Raw == "2" && action.UseFlags["python_targets_python3_14"] {
			return
		}
	}
	t.Fatalf("newer USE-compatible dependency was not selected: install=%v conflicts=%v", result.Install, result.Conflicts)
}

func TestResolve_WarnsWhenDependencyConstraintSkipsVisibleUpdate(t *testing.T) {
	g := makeGraph()
	installed := pkg(g, "dev-python/docutils", "0.22", "0", "0", true, map[string]bool{"python_targets_python3_14": true})
	installed.InstalledUseFlags = map[string]bool{"python_targets_python3_14": true}
	pkg(g, "dev-python/docutils", "0.23", "0", "0", false, map[string]bool{"python_targets_python3_14": true})
	constraint, err := atom.Parse("<dev-python/docutils-0.23[python_targets_python3_14(-)]")
	if err != nil {
		t.Fatal(err)
	}
	r := &resolver{
		graph: g,
		constraints: map[string][]*atom.Atom{
			"dev-python/docutils|0": {constraint},
		},
		constraintCauses: map[string][]ConflictRequirement{
			"dev-python/docutils|0": {{Atom: constraint.String(), Reason: "dependency of dev-python/sphinx"}},
		},
	}
	r.warnSkippedUpdatesDueToConstraints()
	for _, warning := range r.warnings {
		if strings.Contains(warning, "skipped update dev-python/docutils-0.23") &&
			strings.Contains(warning, "<dev-python/docutils-0.23[python_targets_python3_14(-)]") {
			return
		}
	}
	t.Fatalf("missing skipped-update constraint diagnostic: warnings=%v", r.warnings)
}

func TestResolve_CompleteGraphRebuildsReverseDependencyForUseTransition(t *testing.T) {
	g := makeGraph()
	oldLibrary := pkg(g, "dev-python/library", "1", "0", "0", true, map[string]bool{"python_targets_python3_13": true})
	oldLibrary.InstalledUseFlags = map[string]bool{"python_targets_python3_13": true}
	pkg(g, "dev-python/library", "2", "0", "0", false, map[string]bool{"python_targets_python3_14": true})
	oldConsumer := pkg(g, "app-misc/consumer", "1", "0", "0", true, map[string]bool{"python_targets_python3_13": true})
	oldConsumer.InstalledUseFlags = map[string]bool{"python_targets_python3_13": true}
	oldConsumer.InstalledRdepend = "dev-python/library[python_targets_python3_13(-)]"
	newConsumer := pkg(g, "app-misc/consumer", "2", "0", "0", false, map[string]bool{"python_targets_python3_14": true})
	newConsumer.Rdepend = "dev-python/library[python_targets_python3_14(-)]"
	cfg := DefaultResolveConfig()
	cfg.Update, cfg.Deep, cfg.NewUse, cfg.CompleteGraph = true, true, true, true

	result, err := Resolve(g, []string{"dev-python/library"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, action := range result.Install {
		found[action.Atom.CP()] = true
	}
	if !found["dev-python/library"] || !found["app-misc/consumer"] || len(result.Conflicts) != 0 {
		t.Fatalf("USE transition did not rebuild reverse dependency: install=%v conflicts=%v", result.Install, result.Conflicts)
	}
}

func TestVerifyPlannedProviderOverlaysSeparateInstalledRootGraph(t *testing.T) {
	g := makeGraph()
	consumer := pkg(g, "app-misc/consumer", "2", "0", "0", false, map[string]bool{"python_targets_python3_14": true})
	consumer.Rdepend = "dev-python/library[python_targets_python3_14(-)]"
	pkg(g, "dev-python/library", "2", "0", "0", false, map[string]bool{"python_targets_python3_14": true})
	installed := makeGraph()
	oldLibrary := pkg(installed, "dev-python/library", "1", "0", "0", true, map[string]bool{"python_targets_python3_13": true})
	oldLibrary.InstalledUseFlags = map[string]bool{"python_targets_python3_13": true}
	cfg := DefaultResolveConfig()
	cfg.InstalledByDomain = map[DependencyDomain]*DepGraph{DomainROOT: installed}

	result, err := Resolve(g, []string{"app-misc/consumer"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("planned provider disappeared behind separate installed graph: %v", result.Conflicts)
	}
}

func TestResolve_ProposesMutableDependencyUseChange(t *testing.T) {
	g := makeGraph()
	consumer := pkg(g, "dev-libs/consumer", "1", "0", "0", false, nil)
	consumer.Rdepend = "dev-libs/libdbusmenu[gtk3]"
	pkg(g, "dev-libs/libdbusmenu", "1", "0", "0", false, map[string]bool{"gtk3": false})

	result, err := Resolve(g, []string{"dev-libs/consumer"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	var dependency *PkgAction
	for i := range result.Install {
		if result.Install[i].Atom.CP() == "dev-libs/libdbusmenu" {
			dependency = &result.Install[i]
		}
	}
	if dependency == nil || !dependency.UseFlags["gtk3"] {
		t.Fatalf("USE-adjusted candidate missing from plan: %v", result.Install)
	}
	if len(result.Conflicts) != 1 || !strings.Contains(result.Conflicts[0], "dev-libs/libdbusmenu gtk3") {
		t.Fatalf("missing actionable USE change: %v", result.Conflicts)
	}
}

func TestResolve_DoesNotOverrideProfileMaskedUse(t *testing.T) {
	g := makeGraph()
	consumer := pkg(g, "dev-libs/consumer", "1", "0", "0", false, nil)
	consumer.Rdepend = "dev-libs/library[feature]"
	pkg(g, "dev-libs/library", "1", "0", "0", false, map[string]bool{"feature": false})
	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{UseMask: []string{"feature"}}

	_, err := Resolve(g, []string{"dev-libs/consumer"}, cfg)
	if err == nil || !strings.Contains(err.Error(), "no installable version") {
		t.Fatalf("profile-masked USE was overridden: %v", err)
	}
}

func TestMapToSlice_DeduplicatesIdentityButPreservesSlots(t *testing.T) {
	one := &PkgAction{Atom: mustParse("app-text/docbook-xml-dtd-4.3"), Action: "install", Slot: "4.3", Repository: "gentoo"}
	two := &PkgAction{Atom: mustParse("app-text/docbook-xml-dtd-4.3"), Action: "install", Slot: "4.3"}
	otherSlot := &PkgAction{Atom: mustParse("app-text/docbook-xml-dtd-4.3"), Action: "install", Slot: "4.4", Repository: "gentoo"}
	result := mapToSlice(map[string]*PkgAction{"legacy": one, "versioned": two, "other-slot": otherSlot})
	if len(result) != 2 {
		t.Fatalf("identity deduplication produced %d actions: %v", len(result), result)
	}
}

func TestAutoUseChanges_DirectoryStyleAndIdempotent(t *testing.T) {
	root := t.TempDir()
	conflicts := []string{
		"USE changes are necessary to proceed: dev-libs/libdbusmenu gtk3 introspection",
		"USE changes are necessary to proceed: dev-libs/libdbusmenu gtk3",
	}
	if err := AutoUseChanges(conflicts, root); err != nil {
		t.Fatal(err)
	}
	if err := AutoUseChanges(conflicts, root); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "package.use", "arise"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "dev-libs/libdbusmenu gtk3 introspection\n"; got != want {
		t.Fatalf("package.use contents = %q, want %q", got, want)
	}
}

func TestResolve_PostSolveVerifierChecksAffectedRetainedPackage(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/library", "1", "0", "0", true, nil)
	pkg(g, "dev-libs/library", "2", "0", "0", false, nil)
	consumer := pkg(g, "app-misc/consumer", "1", "0", "0", true, nil)
	consumer.InstalledRdepend = "<dev-libs/library-2"
	pkg(g, "app-misc/consumer", "1", "0", "0", false, nil)
	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.DynamicDeps = false

	result, err := Resolve(g, []string{"dev-libs/library"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) == 0 || !strings.Contains(strings.Join(result.Conflicts, "\n"), "required by app-misc/consumer") {
		t.Fatalf("affected retained dependency was not verified: %v", result.Conflicts)
	}
	if result.Verified || result.Verification != VerificationFailed {
		t.Fatalf("conflicted overlay marked verified: %t/%q", result.Verified, result.Verification)
	}
	if len(result.ConflictDetails) == 0 || result.ConflictDetails[len(result.ConflictDetails)-1].Kind != "post-solve-verification" || result.ConflictDetails[len(result.ConflictDetails)-1].Package != "app-misc/consumer" {
		t.Fatalf("verifier conflict lacks structured package detail: %#v", result.ConflictDetails)
	}
}

func TestResolve_PostSolveVerifierChecksDependenciesBrokenByRemoval(t *testing.T) {
	g := makeGraph()
	replacement := pkg(g, "app-misc/replacement", "1", "0", "0", false, nil)
	replacement.Rdepend = "!dev-libs/library"
	pkg(g, "dev-libs/library", "1", "0", "0", true, nil)
	consumer := pkg(g, "app-misc/consumer", "1", "0", "0", true, nil)
	consumer.InstalledRdepend = "dev-libs/library"
	pkg(g, "app-misc/consumer", "1", "0", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.DynamicDeps = false
	result, err := Resolve(g, []string{"app-misc/replacement"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.Verified || result.Verification != VerificationFailed || !strings.Contains(strings.Join(result.Conflicts, "\n"), "dev-libs/library required by app-misc/consumer") {
		t.Fatalf("removal-broken retained dependency escaped verification: verified=%t/%q uninstall=%v conflicts=%v", result.Verified, result.Verification, result.Uninstall, result.Conflicts)
	}
}

func TestResolve_PostSolveVerifierUsesDependencyDomain(t *testing.T) {
	tests := []struct {
		name       string
		dependency func(*VersionInfo)
		domain     DependencyDomain
	}{
		{name: "BDEPEND uses BROOT", domain: DomainBROOT, dependency: func(vi *VersionInfo) { vi.Bdepend = "dev-build/tool" }},
		{name: "IDEPEND uses BROOT", domain: DomainBROOT, dependency: func(vi *VersionInfo) { vi.Idepend = "dev-build/tool" }},
		{name: "DEPEND uses SYSROOT", domain: DomainSYSROOT, dependency: func(vi *VersionInfo) { vi.Depend = "dev-build/tool" }},
		{name: "RDEPEND uses ROOT", domain: DomainROOT, dependency: func(vi *VersionInfo) { vi.Rdepend = "dev-build/tool" }},
		{name: "PDEPEND uses ROOT", domain: DomainROOT, dependency: func(vi *VersionInfo) { vi.Pdepend = "dev-build/tool" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := makeGraph()
			app := pkg(g, "app-misc/application", "1", "0", "0", false, nil)
			app.EAPI = "8"
			tt.dependency(app)
			pkg(g, "dev-build/tool", "1", "0", "0", true, nil)

			wrongDomain := makeGraph()
			cfg := DefaultResolveConfig()
			cfg.InstalledByDomain = map[DependencyDomain]*DepGraph{tt.domain: wrongDomain}
			result, err := Resolve(g, []string{"app-misc/application"}, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if result.Verified || !strings.Contains(strings.Join(result.Conflicts, "\n"), "dev-build/tool required by app-misc/application") {
				t.Fatalf("dependency installed only in another domain passed verification: verified=%t conflicts=%v", result.Verified, result.Conflicts)
			}

			correctDomain := makeGraph()
			pkg(correctDomain, "dev-build/tool", "1", "0", "0", true, nil)
			cfg.InstalledByDomain[tt.domain] = correctDomain
			result, err = Resolve(g, []string{"app-misc/application"}, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if !result.Verified || len(result.Conflicts) != 0 {
				t.Fatalf("dependency in %s failed verification: verified=%t conflicts=%v", tt.domain, result.Verified, result.Conflicts)
			}
		})
	}
}

func TestResolve_PostSolveVerifierUsesDomainForAnyOfAndBlockers(t *testing.T) {
	t.Run("any-of", func(t *testing.T) {
		g := makeGraph()
		app := pkg(g, "app-misc/application", "1", "0", "0", false, nil)
		app.EAPI = "8"
		app.Bdepend = "|| ( dev-build/one dev-build/two )"
		pkg(g, "dev-build/one", "1", "0", "0", true, nil)
		emptyBROOT := makeGraph()
		cfg := DefaultResolveConfig()
		cfg.InstalledByDomain = map[DependencyDomain]*DepGraph{DomainBROOT: emptyBROOT}
		result, err := Resolve(g, []string{"app-misc/application"}, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if result.Verified || !strings.Contains(strings.Join(result.Conflicts, "\n"), "no alternative dependency") {
			t.Fatalf("cross-domain any-of escaped verification: %#v", result)
		}
	})

	t.Run("blocker", func(t *testing.T) {
		g := makeGraph()
		app := pkg(g, "app-misc/application", "1", "0", "0", false, nil)
		app.EAPI = "8"
		app.Bdepend = "!dev-build/conflict"
		// The transaction removes the ROOT instance, but the identical BROOT CPV
		// is a separate installed object and must continue to satisfy the blocker.
		pkg(g, "dev-build/conflict", "1", "0", "0", true, nil)
		broot := makeGraph()
		pkg(broot, "dev-build/conflict", "1", "0", "0", true, nil)
		cfg := DefaultResolveConfig()
		cfg.InstalledByDomain = map[DependencyDomain]*DepGraph{DomainBROOT: broot}
		result, err := Resolve(g, []string{"app-misc/application"}, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if !result.Verified || len(result.Uninstall) != 1 || result.Uninstall[0].Domain != DomainBROOT {
			t.Fatalf("cross-domain blocker removal was not placed in BROOT: %#v", result)
		}
	})
}

func TestVerifyTransactionRemovalMatrix(t *testing.T) {
	t.Run("unreferenced removal is verified", func(t *testing.T) {
		g := makeGraph()
		pkg(g, "app-misc/orphan", "1", "0", "0", true, nil)
		result, err := VerifyTransaction(g, nil, []PkgAction{{
			Atom: mustParse("app-misc/orphan-1"), Action: "uninstall", Slot: "0",
		}}, DefaultResolveConfig())
		if err != nil {
			t.Fatal(err)
		}
		if !result.Verified || result.Verification != VerificationVerified {
			t.Fatalf("safe removal rejected: %#v", result)
		}
	})

	t.Run("reverse dependency blocks removal", func(t *testing.T) {
		g := makeGraph()
		pkg(g, "dev-libs/library", "1", "0", "0", true, nil)
		consumer := pkg(g, "app-misc/consumer", "1", "0", "0", true, nil)
		consumer.InstalledRdepend = "dev-libs/library"
		result, err := VerifyTransaction(g, nil, []PkgAction{{
			Atom: mustParse("dev-libs/library-1"), Action: "uninstall", Slot: "0",
		}}, DefaultResolveConfig())
		if err != nil {
			t.Fatal(err)
		}
		if result.Verified || result.Verification != VerificationFailed || !strings.Contains(strings.Join(result.Conflicts, "\n"), "required by app-misc/consumer") {
			t.Fatalf("dependency-breaking removal passed: %#v", result)
		}
	})

	t.Run("parallel slot retains satisfaction", func(t *testing.T) {
		g := makeGraph()
		pkg(g, "dev-libs/library", "1", "1", "1", true, nil)
		pkg(g, "dev-libs/library", "2", "2", "2", true, nil)
		consumer := pkg(g, "app-misc/consumer", "1", "0", "0", true, nil)
		consumer.InstalledRdepend = "dev-libs/library:2"
		result, err := VerifyTransaction(g, nil, []PkgAction{{
			Atom: mustParse("dev-libs/library-1"), Action: "uninstall", Slot: "1",
		}}, DefaultResolveConfig())
		if err != nil {
			t.Fatal(err)
		}
		if !result.Verified {
			t.Fatalf("unrelated slot removal rejected: %#v", result)
		}
	})

	t.Run("repository identity is completed before multi removal overlay", func(t *testing.T) {
		g := makeGraph()
		library := g.AddVersionFromRepository("dev-libs/library", "1", "0", "1", true, nil, "amd64", "gentoo")
		consumer := g.AddVersionFromRepository("app-misc/consumer", "1", "0", "0", true, nil, "amd64", "gentoo")
		consumer.InstalledRdepend = "dev-libs/library"
		_ = library
		result, err := VerifyTransaction(g, nil, []PkgAction{
			{Atom: mustParse("app-misc/consumer-1"), Action: "uninstall"},
			{Atom: mustParse("dev-libs/library-1"), Action: "uninstall"},
		}, DefaultResolveConfig())
		if err != nil {
			t.Fatal(err)
		}
		if !result.Verified || len(result.Conflicts) != 0 {
			t.Fatalf("complete repository-qualified closure rejected: %#v", result)
		}
		for _, action := range result.Uninstall {
			if action.Repository != "gentoo" || action.Slot != "0" {
				t.Fatalf("incomplete removal identity: %#v", action)
			}
		}
	})

	t.Run("nil action atom fails closed", func(t *testing.T) {
		if _, err := VerifyTransaction(makeGraph(), nil, []PkgAction{{Action: "uninstall"}}, DefaultResolveConfig()); err == nil {
			t.Fatal("invalid removal action was accepted")
		}
	})
}

func TestVerifyTransactionConstraintMutationMatrix(t *testing.T) {
	tests := []struct {
		name       string
		dependency string
		wantOK     bool
	}{
		{name: "baseline dependency", dependency: ">=dev-libs/library-1:0[-feature]", wantOK: true},
		{name: "mutated version", dependency: ">=dev-libs/library-2:0[-feature]"},
		{name: "mutated slot", dependency: ">=dev-libs/library-1:1[-feature]"},
		{name: "mutated USE", dependency: ">=dev-libs/library-1:0[feature]"},
		{name: "inverted to blocker", dependency: "!dev-libs/library"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := makeGraph()
			library := pkg(g, "dev-libs/library", "1", "0", "0", true, map[string]bool{"feature": false})
			library.InstalledIUseFlags = map[string]bool{"feature": true}
			application := pkg(g, "app-misc/application", "1", "0", "0", false, nil)
			application.EAPI = "8"
			application.Rdepend = tt.dependency
			result, err := VerifyTransaction(g, []PkgAction{{
				Atom: mustParse("app-misc/application-1"), Action: "install", Slot: "0", Domain: DomainROOT,
			}}, nil, DefaultResolveConfig())
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantOK {
				if !result.Verified || result.Verification != VerificationVerified {
					t.Fatalf("valid baseline failed verification: %#v", result)
				}
				return
			}
			if result.Verified || result.Verification != VerificationFailed {
				t.Fatalf("constraint mutation escaped verification: %#v", result)
			}
			if len(result.ConflictDetails) == 0 || result.ConflictDetails[len(result.ConflictDetails)-1].Kind != "post-solve-verification" {
				t.Fatalf("constraint mutation lacks structured verifier detail: %#v", result.ConflictDetails)
			}
		})
	}
}

func TestResolve_PlansSamePackageIndependentlyAcrossRootDomains(t *testing.T) {
	g := makeGraph()
	app := pkg(g, "app-misc/application", "1", "0", "0", false, nil)
	app.EAPI = "8"
	app.Depend = "dev-build/tool"
	app.Rdepend = "dev-build/tool"
	app.Bdepend = "dev-build/tool"
	pkg(g, "dev-build/tool", "1", "0", "0", false, nil)

	cfg := DefaultResolveConfig()
	cfg.InstalledByDomain = map[DependencyDomain]*DepGraph{
		DomainROOT: makeGraph(), DomainSYSROOT: makeGraph(), DomainBROOT: makeGraph(),
	}
	result, err := Resolve(g, []string{"app-misc/application"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified {
		t.Fatalf("three-domain plan failed verification: %v", result.Conflicts)
	}
	domains := make(map[DependencyDomain]int)
	for _, action := range result.Install {
		if action.Atom.CP() == "dev-build/tool" {
			domains[action.Domain]++
		}
	}
	if domains[DomainROOT] != 1 || domains[DomainSYSROOT] != 1 || domains[DomainBROOT] != 1 || len(domains) != 3 {
		t.Fatalf("same dependency collided across domains: actions=%#v domains=%v", result.Install, domains)
	}
}

func TestDependenciesForVersionKeepsInstalledEAPISeparateFromRepositoryCandidate(t *testing.T) {
	g := makeGraph()
	vi := g.AddVersionFromRepository("x11-misc/example", "1", "0", "0", false, nil, "amd64", "gentoo")
	vi.EAPI = "8"
	vi.Bdepend = "dev-build/new-tool"
	installed := g.AddVersionFromRepository("x11-misc/example", "1", "0", "0", true, nil, "", "gentoo")
	installed.InstalledEAPI = "6"
	installed.InstalledRdepend = "dev-libs/runtime"
	pkg(g, "dev-libs/runtime", "1", "0", "0", true, nil)
	cfg := DefaultResolveConfig()
	cfg.DynamicDeps = false
	r := &resolver{graph: g, config: cfg, toInstall: make(map[string]*PkgAction)}
	edges, err := r.dependenciesForVersion(g.Packages["x11-misc/example"], installed)
	if err != nil {
		t.Fatalf("repository EAPI/dependencies contaminated retained metadata: %v", err)
	}
	if len(edges) != 1 || edges[0].Type != DepTypeRuntime || edges[0].DepAtom.CP() != "dev-libs/runtime" {
		t.Fatalf("retained dependency view = %#v", edges)
	}
}

func TestResolve_SetRetainsInstalledVersionWithoutRepositoryCandidate(t *testing.T) {
	g := makeGraph()
	installed := pkg(g, "llvm-core/llvm", "23.0.0.9999", "23", "23.0", true, nil)
	installed.InstalledEAPI = "8"
	installed.InstalledRdepend = "sys-libs/runtime"
	pkg(g, "llvm-core/llvm", "22.1", "22", "22.1", false, nil)
	pkg(g, "sys-libs/runtime", "1", "0", "0", true, nil)
	cfg := DefaultResolveConfig()
	cfg.WorldSet = &WorldSet{Entries: []string{"=llvm-core/llvm-23.0.0.9999:23/23.0"}}

	result, err := Resolve(g, []string{"@world"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	warnings := strings.Join(result.Warnings, "\n")
	if !result.Verified || len(result.Install) != 0 ||
		!strings.Contains(warnings, "world selection llvm-core/llvm has no installable repository candidate") ||
		!strings.Contains(warnings, "arise deselect llvm-core/llvm") {
		t.Fatalf("installed live set member was not retained safely: %#v", result)
	}
}

func TestResolve_SlotOperatorRebuildUsesVisibleCandidate(t *testing.T) {
	g := makeGraph()
	parent := pkg(g, "app-misc/parent", "1", "0", "0", true, nil)
	parent.InstalledRdepend = "dev-libs/library:0="
	pkgKeywords(g, "dev-libs/library", "1", "0", "1", true, nil, "amd64")
	pkgKeywords(g, "dev-libs/library", "2", "0", "2", false, nil, "amd64")
	pkgKeywords(g, "dev-libs/library", "9999", "0", "9999", false, nil, "amd64")
	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{ACCEPT_KEYWORDS: []string{"amd64"}, PackageMask: []string{"=dev-libs/library-9999"}}

	result, err := Resolve(g, []string{"app-misc/parent"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range result.Install {
		if action.Atom.CP() == "dev-libs/library" && action.Atom.Version.Raw != "2" {
			t.Fatalf("slot-operator rebuild selected masked raw-best candidate: %#v", action)
		}
	}
}

func TestResolve_VerifierReportsUnselectedInstalledOnlyPackageAsDepcleanWarning(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/one", "1", "0", "0", true, nil)
	pkg(g, "dev-libs/one", "2", "0", "0", false, nil)
	pkg(g, "dev-libs/two", "1", "0", "0", true, nil)
	pkg(g, "dev-libs/two", "2", "0", "0", false, nil)
	consumer := pkg(g, "app-misc/removed", "1", "0", "0", true, nil)
	consumer.InstalledRdepend = "<dev-libs/one-2 <dev-libs/two-2"
	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.CompleteGraph = true

	result, err := Resolve(g, []string{"dev-libs/one", "dev-libs/two"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("unselected installed-only package blocked update: %v", result.Conflicts)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("installed-only failures were not collapsed: %v", result.Warnings)
	}
	message := result.Warnings[0]
	if !strings.Contains(message, "installed-only package app-misc/removed") || !strings.Contains(message, "depclean candidate") {
		t.Fatalf("missing actionable installed-only diagnostic: %s", message)
	}
}

func TestResolve_VerifierBlocksSelectedInstalledOnlyPackageFailure(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/library", "1", "0", "0", true, nil)
	pkg(g, "dev-libs/library", "2", "0", "0", false, nil)
	consumer := pkg(g, "app-misc/removed", "1", "0", "0", true, nil)
	consumer.InstalledRdepend = "<dev-libs/library-2"
	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.CompleteGraph = true
	cfg.WorldSet = &WorldSet{Entries: []string{"app-misc/removed", "dev-libs/library"}}

	result, err := Resolve(g, []string{"@world"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(result.Conflicts, "\n"), "installed-only package app-misc/removed") {
		t.Fatalf("selected broken installed-only package did not block plan: conflicts=%v warnings=%v", result.Conflicts, result.Warnings)
	}
}

func TestResolve_WorldCompleteGraphDoesNotRebuildUnselectedAvailableOrphan(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/library", "1", "0", "0", true, nil)
	pkg(g, "dev-libs/library", "2", "0", "0", false, nil)
	orphan := pkg(g, "app-misc/orphan", "1", "0", "0", true, nil)
	orphan.InstalledRdepend = "<dev-libs/library-2"
	pkg(g, "app-misc/orphan", "1", "0", "0", false, nil)
	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.CompleteGraph = true
	cfg.WorldSet = &WorldSet{Entries: []string{"dev-libs/library"}}

	result, err := Resolve(g, []string{"@world"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range result.Install {
		if action.Atom.CP() == "app-misc/orphan" {
			t.Fatalf("unselected orphan was pulled back into @world: %v", result.Install)
		}
	}
}

func TestResolve_WorldCompleteGraphScopesSelectionToPackageSlot(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/tool", "1", "0", "0", true, nil)
	pkg(g, "dev-libs/tool", "2", "0", "0", false, nil)
	oldSlot := pkg(g, "dev-lang/runtime", "1", "1", "0", true, nil)
	oldSlot.InstalledRdepend = "<dev-libs/tool-2"
	pkg(g, "dev-lang/runtime", "1.1", "1", "0", false, nil)
	selectedSlot := pkg(g, "dev-lang/runtime", "2", "2", "0", true, nil)
	selectedSlot.Available = true
	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.Deep = true
	cfg.CompleteGraph = true
	cfg.WorldSet = &WorldSet{Entries: []string{"dev-libs/tool", "dev-lang/runtime:2"}}

	result, err := Resolve(g, []string{"@world"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range result.Install {
		if action.Atom.CP() == "dev-lang/runtime" && action.Slot == "1" {
			t.Fatalf("selected slot pulled unrelated installed slot into repair: %v", result.Install)
		}
	}
}

func TestResolve_CompleteGraphPlanIsDeterministic(t *testing.T) {
	var baseline string
	for iteration := 0; iteration < 20; iteration++ {
		g := makeGraph()
		pkg(g, "dev-libs/one", "1", "0", "0", true, nil)
		pkg(g, "dev-libs/one", "2", "0", "0", false, nil)
		pkg(g, "dev-libs/two", "1", "0", "0", true, nil)
		pkg(g, "dev-libs/two", "2", "0", "0", false, nil)
		consumer := pkg(g, "app-misc/removed", "1", "0", "0", true, nil)
		consumer.InstalledRdepend = "<dev-libs/one-2 <dev-libs/two-2"
		cfg := DefaultResolveConfig()
		cfg.Update = true
		cfg.CompleteGraph = true
		result, err := Resolve(g, []string{"dev-libs/two", "dev-libs/one"}, cfg)
		if err != nil {
			t.Fatal(err)
		}
		parts := []string{fmt.Sprintf("backtrack=%d", result.BacktrackLevel), strings.Join(result.Conflicts, "|")}
		for _, action := range result.Install {
			parts = append(parts, action.Action+":"+action.Atom.String())
		}
		signature := strings.Join(parts, "\n")
		if iteration == 0 {
			baseline = signature
		} else if signature != baseline {
			t.Fatalf("iteration %d produced a different plan\nfirst:\n%s\ncurrent:\n%s", iteration, baseline, signature)
		}
	}
}

func TestResolve_CompleteGraphRepairsAffectedRetainedPackage(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/library", "1", "0", "0", true, nil)
	pkg(g, "dev-libs/library", "2", "0", "0", false, nil)
	installedConsumer := pkg(g, "app-misc/consumer", "1", "0", "0", true, nil)
	installedConsumer.InstalledRdepend = "<dev-libs/library-2"
	newConsumer := pkg(g, "app-misc/consumer", "2", "0", "0", false, nil)
	newConsumer.Rdepend = ">=dev-libs/library-2"
	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.CompleteGraph = true

	result, err := Resolve(g, []string{"dev-libs/library"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var consumer bool
	for _, action := range result.Install {
		consumer = consumer || action.Atom.CP() == "app-misc/consumer" && action.Atom.Version.Raw == "2"
	}
	if !consumer || len(result.Conflicts) != 0 {
		t.Fatalf("complete graph did not repair retained package: install=%v conflicts=%v", result.Install, result.Conflicts)
	}
	if !result.Verified || result.Verification != VerificationVerified {
		t.Fatalf("repaired overlay verification = %t/%q", result.Verified, result.Verification)
	}
}

func TestResolve_CompleteGraphRepairsPerlVirtualTransition(t *testing.T) {
	makeFixture := func() *DepGraph {
		g := makeGraph()
		pkg(g, "dev-lang/perl", "1", "0", "1", true, nil)
		pkg(g, "dev-lang/perl", "2", "0", "2", false, nil)
		oldVirtual := pkg(g, "virtual/perl-parent", "1", "0", "0", true, nil)
		oldVirtual.Rdepend = "=dev-lang/perl-1* dev-lang/perl:0/1="
		newVirtual := pkg(g, "virtual/perl-parent", "2", "0", "0", false, nil)
		newVirtual.Rdepend = "=dev-lang/perl-2* dev-lang/perl:0/2="
		return g
	}

	cfg := DefaultResolveConfig()
	cfg.Deep = false
	cfg.CompleteGraph = false
	result, err := Resolve(makeFixture(), []string{"dev-lang/perl"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.Verified || result.Verification != VerificationFailed {
		t.Fatalf("unrepaired Perl virtual transition passed verification: %#v", result)
	}

	cfg.CompleteGraph = true
	result, err = Resolve(makeFixture(), []string{"dev-lang/perl"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || len(result.Install) != 2 {
		t.Fatalf("complete graph did not repair Perl virtual transition: %#v", result)
	}
	versions := collectCPV(result.Install)
	seen := make(map[string]bool, len(versions))
	for _, version := range versions {
		seen[version] = true
	}
	if !seen["dev-lang/perl-2"] || !seen["virtual/perl-parent-2"] {
		t.Fatalf("repaired Perl plan = %v", versions)
	}
}

func TestResolve_CompleteGraphPreservesUseChangeFromRepair(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/library", "1", "0", "0", true, nil)
	pkg(g, "dev-libs/library", "2", "0", "0", false, nil)
	installedConsumer := pkg(g, "app-misc/consumer", "1", "0", "0", true, nil)
	installedConsumer.InstalledRdepend = "<dev-libs/library-2"
	newConsumer := pkg(g, "app-misc/consumer", "2", "0", "0", false, nil)
	newConsumer.Rdepend = "dev-libs/feature[enabled]"
	pkg(g, "dev-libs/feature", "1", "0", "0", false, map[string]bool{"enabled": false})
	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.CompleteGraph = true

	result, err := Resolve(g, []string{"dev-libs/library"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(result.Conflicts, "\n")
	if !strings.Contains(joined, "USE changes are necessary to proceed: dev-libs/feature enabled") {
		t.Fatalf("repair USE requirement was lost: %v", result.Conflicts)
	}
	var featurePlanned bool
	for _, action := range result.Install {
		if action.Atom.CP() == "dev-libs/feature" && action.UseFlags["enabled"] {
			featurePlanned = true
		}
	}
	if !featurePlanned {
		t.Fatalf("hypothetical USE-adjusted repair candidate missing: %v", result.Install)
	}
}

func TestResolve_CompleteGraphRepairUsesCurrentUse(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/library", "1", "0", "0", true, nil)
	pkg(g, "dev-libs/library", "2", "0", "0", false, nil)
	installedConsumer := pkg(g, "app-misc/consumer", "1", "0", "0", true, map[string]bool{"old_target": true})
	installedConsumer.InstalledUseFlags = map[string]bool{"old_target": true}
	installedConsumer.InstalledRdepend = "<dev-libs/library-2"
	newConsumer := pkg(g, "app-misc/consumer", "2", "0", "0", false, map[string]bool{"old_target": false, "new_target": true})
	newConsumer.Rdepend = "old_target? ( dev-libs/compat[old_target] )"
	pkg(g, "dev-libs/compat", "1", "0", "0", false, map[string]bool{"old_target": true})
	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.CompleteGraph = true

	result, err := Resolve(g, []string{"dev-libs/library"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var current, compat bool
	for _, action := range result.Install {
		if action.Atom.CP() == "app-misc/consumer" {
			current = !action.UseFlags["old_target"] && action.UseFlags["new_target"]
		}
		compat = compat || action.Atom.CP() == "dev-libs/compat"
	}
	if !current || compat {
		t.Fatalf("repair did not use current effective USE: install=%v", result.Install)
	}
}

func TestResolve_CompleteGraphDoesNotInventUseFromInstalledInstance(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/library", "1", "0", "0", true, nil)
	pkg(g, "dev-libs/library", "2", "0", "0", false, nil)
	installedConsumer := pkg(g, "app-misc/consumer", "1", "0", "0", true, map[string]bool{"old_target": true})
	installedConsumer.InstalledUseFlags = map[string]bool{"old_target": true}
	installedConsumer.InstalledRdepend = "<dev-libs/library-2"
	newConsumer := pkg(g, "app-misc/consumer", "2", "0", "0", false, map[string]bool{"old_target": false})
	newConsumer.Rdepend = "old_target? ( dev-libs/compat[old_target] )"
	pkg(g, "dev-libs/compat", "1", "0", "0", false, map[string]bool{"old_target": false})
	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.CompleteGraph = true

	result, err := Resolve(g, []string{"dev-libs/library"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(result.Conflicts, "\n")
	if strings.Contains(joined, "dev-libs/compat") {
		t.Fatalf("installed-only USE leaked into replacement dependencies: %v", result.Conflicts)
	}
	for _, action := range result.Install {
		if action.Atom.CP() == "dev-libs/compat" {
			t.Fatalf("inactive installed-only dependency was planned: %v", result.Install)
		}
	}
}

func TestResolve_SameVersionUseRepairIsReinstall(t *testing.T) {
	g := makeGraph()
	installed := pkg(g, "dev-libs/feature", "1", "0", "0", true, map[string]bool{"enabled": false})
	installed.InstalledUseFlags = map[string]bool{"enabled": false}
	pkg(g, "dev-libs/feature", "1", "0", "0", false, map[string]bool{"enabled": false})
	consumer := pkg(g, "app-misc/consumer", "1", "0", "0", false, nil)
	consumer.Rdepend = "dev-libs/feature[enabled]"

	result, err := Resolve(g, []string{"app-misc/consumer"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range result.Install {
		if action.Atom.CP() == "dev-libs/feature" {
			if action.Action != "reinstall" {
				t.Fatalf("same-version USE repair action = %q, want reinstall", action.Action)
			}
			return
		}
	}
	t.Fatalf("feature repair missing: %v", result.Install)
}

func TestInstalledVersionForSlotDoesNotCrossSlots(t *testing.T) {
	g := makeGraph()
	pkg(g, "sys-kernel/gentoo-sources", "7.0", "7.0", "", true, nil)
	want := pkg(g, "sys-kernel/gentoo-sources", "6.12", "6.12", "", true, nil)
	node := g.Packages["sys-kernel/gentoo-sources"]
	if got := node.GetInstalledVersionForSlot("6.12"); got != want {
		t.Fatalf("slot lookup = %v, want %v", got, want)
	}
	if got := node.GetInstalledVersionForSlot("7.1"); got != nil {
		t.Fatalf("uninstalled slot lookup = %v, want nil", got)
	}
}

func TestResolve_AnyOfSearchesAllInstalledSlots(t *testing.T) {
	g := makeGraph()
	parent := pkg(g, "www-client/browser", "1", "0", "0", false, nil)
	parent.Bdepend = "|| ( dev-lang/rust-bin:1.94 dev-lang/rust-bin:1.93 dev-lang/rust-bin:1.91 )"
	pkg(g, "dev-lang/rust-bin", "1.91", "1.91", "", false, nil)
	pkg(g, "dev-lang/rust-bin", "1.93", "1.93", "", true, nil)
	pkg(g, "dev-lang/rust-bin", "1.95", "1.95", "", true, nil)
	cfg := DefaultResolveConfig()
	cfg.WithBdeps = "y"

	result, err := Resolve(g, []string{"www-client/browser"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range result.Install {
		if action.Atom.CP() == "dev-lang/rust-bin" {
			t.Fatalf("installed matching Rust slot was ignored: %v", result.Install)
		}
	}
}

func TestResolve_IntersectsConstraintsToOneCandidatePerSlot(t *testing.T) {
	g := makeGraph()
	first := pkg(g, "app-misc/first", "1", "0", "0", false, nil)
	first.Rdepend = "dev-python/docutils"
	second := pkg(g, "app-misc/second", "1", "0", "0", false, nil)
	second.Rdepend = "<dev-python/docutils-0.23"
	pkg(g, "dev-python/docutils", "0.22.4", "0", "0", false, nil)
	pkg(g, "dev-python/docutils", "0.23", "0", "0", false, nil)

	result, err := Resolve(g, []string{"app-misc/first", "app-misc/second"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, action := range result.Install {
		if action.Atom.CP() == "dev-python/docutils" {
			count++
			if action.Atom.Version.Raw != "0.22.4" {
				t.Fatalf("selected incompatible candidate: %v", action.Atom)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected one docutils slot candidate, got %d: %v", count, result.Install)
	}
	if result.BacktrackLevel != 1 {
		t.Fatalf("backtrack level = %d, want 1", result.BacktrackLevel)
	}
	if len(result.DecisionHistory) != 1 || result.DecisionHistory[0] != (BacktrackDecision{
		Kind: "version", Key: "dev-python/docutils:0", From: "0.23", To: "0.22.4",
	}) {
		t.Fatalf("version decision history = %#v", result.DecisionHistory)
	}
}

func TestResolve_SlotConflictRetainsStructuredRequirements(t *testing.T) {
	g := makeGraph()
	first := pkg(g, "app-misc/first", "1", "0", "0", false, nil)
	first.Rdepend = "<dev-libs/shared-2"
	second := pkg(g, "app-misc/second", "1", "0", "0", false, nil)
	second.Rdepend = ">=dev-libs/shared-2"
	pkg(g, "dev-libs/shared", "1", "0", "0", false, nil)
	pkg(g, "dev-libs/shared", "2", "0", "0", false, nil)
	cfg := DefaultResolveConfig()
	cfg.KeepGoing = true

	result, err := Resolve(g, []string{"app-misc/first", "app-misc/second"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var detail ConflictDetail
	for _, candidate := range result.ConflictDetails {
		if candidate.Kind == "slot-conflict" {
			detail = candidate
			break
		}
	}
	if detail.Kind != "slot-conflict" || detail.Package != "dev-libs/shared" || detail.Slot != "0" || len(detail.Requirements) != 2 {
		t.Fatalf("slot detail = %#v", detail)
	}
	if detail.Requirements[0].Atom != "<dev-libs/shared-2" || !strings.Contains(detail.Requirements[0].Reason, "app-misc/first") ||
		detail.Requirements[1].Atom != ">=dev-libs/shared-2" || !strings.Contains(detail.Requirements[1].Reason, "app-misc/second") {
		t.Fatalf("requirements lost parent causes: %#v", detail.Requirements)
	}
	if len(detail.Candidates) != 2 {
		t.Fatalf("candidate explanations = %#v", detail.Candidates)
	}
	for _, candidate := range detail.Candidates {
		if candidate.State != "available" || !candidate.Visible || len(candidate.Satisfies) != 1 || len(candidate.Rejects) != 1 {
			t.Fatalf("candidate does not explain incompatible requirements: %#v", candidate)
		}
	}
}

func TestResolve_NestedAndNegatedDependencyConditions(t *testing.T) {
	g := makeGraph()
	consumer := pkg(g, "app-misc/consumer", "1", "0", "0", false, map[string]bool{
		"python": true, "test": false,
	})
	consumer.Rdepend = "python? ( !test? ( dev-python/required ) ) test? ( dev-python/skipped )"
	pkg(g, "dev-python/required", "1", "0", "0", false, nil)
	pkg(g, "dev-python/skipped", "1", "0", "0", false, nil)

	result, err := Resolve(g, []string{"app-misc/consumer"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	var required, skipped bool
	for _, action := range result.Install {
		required = required || action.Atom.CP() == "dev-python/required"
		skipped = skipped || action.Atom.CP() == "dev-python/skipped"
	}
	if !required || skipped {
		t.Fatalf("nested conditions evaluated incorrectly: %v", result.Install)
	}
}

func TestResolve_InactiveConditionalAnyOfIsNotAConflict(t *testing.T) {
	g := makeGraph()
	consumer := pkg(g, "app-misc/consumer", "1", "0", "0", false, map[string]bool{"gui": false})
	consumer.Rdepend = "gui? ( || ( app-misc/one app-misc/two ) )"
	pkgKeywords(g, "app-misc/one", "1", "0", "0", false, nil, "~amd64")

	result, err := Resolve(g, []string{"app-misc/consumer"}, DefaultResolveConfig())
	if err != nil {
		t.Fatalf("inactive any-of group caused a conflict: %v", err)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %v", result.Conflicts)
	}
}

func TestResolve_InactiveAnyOfOptionChangesInEAPI7(t *testing.T) {
	for _, test := range []struct {
		eapi         string
		wantConflict bool
	}{{eapi: "6"}, {eapi: "7", wantConflict: true}, {eapi: "8", wantConflict: true}} {
		t.Run("EAPI-"+test.eapi, func(t *testing.T) {
			graph := makeGraph()
			consumer := pkg(graph, "app-misc/consumer", "1", "0", "0", false, map[string]bool{"gui": false})
			consumer.EAPI = test.eapi
			consumer.Rdepend = "|| ( gui? ( app-misc/provider ) )"
			result, err := Resolve(graph, []string{"app-misc/consumer"}, DefaultResolveConfig())
			if test.wantConflict {
				if err == nil || len(result.Conflicts) == 0 {
					t.Fatalf("inactive any-of option passed EAPI %s: result=%#v err=%v", test.eapi, result, err)
				}
				return
			}
			if err != nil || len(result.Conflicts) != 0 || !result.Verified {
				t.Fatalf("inactive any-of option failed EAPI %s: result=%#v err=%v", test.eapi, result, err)
			}
		})
	}
}

func TestResolve_SelectedVersionPreservesAnyOfDependencies(t *testing.T) {
	g := makeGraph()
	firefox := pkg(g, "www-client/firefox", "140.12.0", "esr", "0", false, map[string]bool{"pulseaudio": true})
	firefox.Rdepend = "pulseaudio? ( || ( media-libs/libpulse >=media-sound/apulse-0.1.12-r4[sdk] ) )"
	pkg(g, "media-libs/libpulse", "17.0", "0", "0", true, nil)
	pkg(g, "media-sound/apulse", "0.1.13", "0", "0", false, map[string]bool{"sdk": false})

	result, err := Resolve(g, []string{"www-client/firefox"}, DefaultResolveConfig())
	if err != nil {
		t.Fatalf("installed any-of option should satisfy dependency: %v", err)
	}
	for _, action := range result.Install {
		if action.Atom.CP() == "media-sound/apulse" {
			t.Fatalf("unselected any-of alternative was scheduled: %v", result.Install)
		}
	}
}

func TestResolve_AnyOfSkipsUnavailableAlternative(t *testing.T) {
	g := makeGraph()
	consumer := pkg(g, "app-misc/consumer", "1", "0", "0", false, nil)
	consumer.Rdepend = "|| ( media-plugins/testing-only media-plugins/stable )"
	pkgKeywords(g, "media-plugins/testing-only", "1", "0", "0", false, nil, "~amd64")
	pkg(g, "media-plugins/stable", "1", "0", "0", false, nil)
	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{ACCEPT_KEYWORDS: []string{"amd64"}}

	result, err := Resolve(g, []string{"app-misc/consumer"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range result.Install {
		if action.Atom.CP() == "media-plugins/testing-only" {
			t.Fatalf("unavailable any-of option selected: %v", result.Install)
		}
	}
}

func TestResolve_AnyOfPlansMutableUseRepair(t *testing.T) {
	g := makeGraph()
	consumer := pkg(g, "virtual/service-manager", "1", "0", "0", false, nil)
	consumer.Rdepend = "|| ( sys-apps/s6-rc[system-init(-)] sys-process/runit[system-init(-)] )"
	pkg(g, "sys-apps/s6-rc", "1", "0", "0", false, map[string]bool{"system-init": false})
	pkg(g, "sys-process/runit", "1", "0", "0", false, map[string]bool{"system-init": false})

	result, err := Resolve(g, []string{"virtual/service-manager"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) != 1 || !strings.Contains(result.Conflicts[0], "USE changes are necessary to proceed:") {
		t.Fatalf("mutable any-of USE requirement not preserved: %v", result.Conflicts)
	}
	var repaired int
	for _, action := range result.Install {
		if (action.Atom.CP() == "sys-apps/s6-rc" || action.Atom.CP() == "sys-process/runit") && action.UseFlags["system-init"] {
			repaired++
		}
	}
	if repaired != 1 {
		t.Fatalf("planned repaired alternatives = %d, want exactly one: %v", repaired, result.Install)
	}
}

func TestResolve_NestedAnyOfPrefersInstalledOuterAlternative(t *testing.T) {
	g := makeGraph()
	consumer := pkg(g, "virtual/service-manager", "1", "0", "0", false, map[string]bool{"systemd": false, "kernel_linux": true})
	consumer.Rdepend = "!systemd? ( || ( sys-apps/openrc kernel_linux? ( || ( sys-apps/s6-rc[system-init(-)] sys-process/runit[system-init(-)] ) ) ) )"
	pkg(g, "sys-apps/openrc", "1", "0", "0", true, nil)
	pkg(g, "sys-apps/s6-rc", "1", "0", "0", false, map[string]bool{"system-init": false})
	pkg(g, "sys-process/runit", "1", "0", "0", false, map[string]bool{"system-init": false})

	result, err := Resolve(g, []string{"virtual/service-manager"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	if actionForCP(result.Install, "sys-apps/s6-rc") || actionForCP(result.Install, "sys-process/runit") {
		t.Fatalf("nested any-of ignored installed outer alternative: %v", result.Install)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("installed OpenRC should avoid USE repair: %v", result.Conflicts)
	}
}

func TestResolve_AnyOfPreservesAllOfAlternativeTuple(t *testing.T) {
	g := makeGraph()
	mesa := pkg(g, "media-libs/mesa", "1", "0", "0", false, nil)
	mesa.Bdepend = "|| ( ( dev-lang/python:3.14 dev-python/mako[python_targets_python3_14(-)] dev-python/packaging[python_targets_python3_14(-)] ) ( dev-lang/python:3.13 dev-python/mako[python_targets_python3_13(-)] dev-python/packaging[python_targets_python3_13(-)] ) )"
	pkg(g, "dev-lang/python", "3.14.1", "3.14", "0", false, nil)
	pkg(g, "dev-lang/python", "3.13.1", "3.13", "0", true, nil)
	pkg(g, "dev-python/mako", "0", "0", "0", true, map[string]bool{"python_targets_python3_14": false, "python_targets_python3_13": true})
	pkg(g, "dev-python/mako", "1", "0", "0", false, map[string]bool{"python_targets_python3_14": true, "python_targets_python3_13": false})
	pkg(g, "dev-python/packaging", "0", "0", "0", true, map[string]bool{"python_targets_python3_14": false, "python_targets_python3_13": true})
	pkg(g, "dev-python/packaging", "1", "0", "0", false, map[string]bool{"python_targets_python3_14": true, "python_targets_python3_13": false})

	result, err := Resolve(g, []string{"media-libs/mesa"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, cp := range []string{"dev-lang/python", "dev-python/mako", "dev-python/packaging"} {
		if !actionForCP(result.Install, cp) {
			t.Fatalf("tuple member %s missing from plan: %v", cp, result.Install)
		}
	}
	for _, action := range result.Install {
		if action.Atom.CP() == "dev-lang/python" && action.Slot != "3.14" {
			t.Fatalf("selected mismatched interpreter tuple: %v", result.Install)
		}
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("valid tuple produced conflicts: %v", result.Conflicts)
	}
}

func TestResolve_RetractsDependenciesOwnedOnlyBySupersededParent(t *testing.T) {
	g := makeGraph()
	consumer := pkg(g, "app-misc/consumer", "1", "0", "0", false, nil)
	consumer.Rdepend = "app-misc/parent[old]"
	installedParent := pkg(g, "app-misc/parent", "1", "0", "0", true, map[string]bool{"old": true})
	installedParent.InstalledRdepend = "app-misc/old-child"
	newParent := pkg(g, "app-misc/parent", "2", "0", "0", false, map[string]bool{"old": false})
	newParent.Rdepend = "app-misc/new-child"
	pkg(g, "app-misc/old-child", "1", "0", "0", false, nil)
	pkg(g, "app-misc/new-child", "1", "0", "0", false, nil)
	cfg := DefaultResolveConfig()
	cfg.Update = true

	result, err := Resolve(g, []string{"app-misc/consumer", "app-misc/parent"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if actionForCP(result.Install, "app-misc/old-child") {
		t.Fatalf("superseded parent dependency survived: %v", result.Install)
	}
	if !actionForCP(result.Install, "app-misc/new-child") {
		t.Fatalf("replacement dependency missing: %v", result.Install)
	}
}

func TestResolve_SupersededParentPreservesSharedDependencyOwner(t *testing.T) {
	g := makeGraph()
	consumer := pkg(g, "app-misc/consumer", "1", "0", "0", false, nil)
	consumer.Rdepend = "app-misc/parent[old]"
	other := pkg(g, "app-misc/other", "1", "0", "0", false, nil)
	other.Rdepend = "app-misc/shared"
	installedParent := pkg(g, "app-misc/parent", "1", "0", "0", true, map[string]bool{"old": true})
	installedParent.InstalledRdepend = "app-misc/shared"
	newParent := pkg(g, "app-misc/parent", "2", "0", "0", false, map[string]bool{"old": false})
	newParent.Rdepend = "app-misc/new-child"
	pkg(g, "app-misc/shared", "1", "0", "0", false, nil)
	pkg(g, "app-misc/new-child", "1", "0", "0", false, nil)
	cfg := DefaultResolveConfig()
	cfg.Update = true

	result, err := Resolve(g, []string{"app-misc/consumer", "app-misc/other", "app-misc/parent"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !actionForCP(result.Install, "app-misc/shared") {
		t.Fatalf("shared dependency was removed with one superseded owner: %v", result.Install)
	}
}

func TestResolve_DependencyTraversalUsesScheduledReplacementOverlay(t *testing.T) {
	g := makeGraph()
	installedParent := pkg(g, "app-misc/parent", "1", "0", "0", true, nil)
	installedParent.InstalledRdepend = "app-misc/old-child"
	newParent := pkg(g, "app-misc/parent", "2", "0", "0", false, nil)
	newParent.Rdepend = "app-misc/new-child"
	wrapper := pkg(g, "virtual/parent", "1", "0", "0", false, nil)
	wrapper.Rdepend = "app-misc/parent"
	pkg(g, "app-misc/old-child", "1", "0", "0", false, nil)
	pkg(g, "app-misc/new-child", "1", "0", "0", false, nil)
	cfg := DefaultResolveConfig()
	cfg.Update = true

	result, err := Resolve(g, []string{"app-misc/parent", "virtual/parent"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if actionForCP(result.Install, "app-misc/old-child") || !actionForCP(result.Install, "app-misc/new-child") {
		t.Fatalf("dependency traversal ignored scheduled replacement: %v", result.Install)
	}
}

func TestResolve_DeepUpdatePromotesInstalledAnyOfAlternative(t *testing.T) {
	g := makeGraph()
	parent := pkg(g, "app-misc/parent", "1", "0", "0", true, nil)
	parent.Available = true
	parent.Rdepend = "|| ( www-client/preferred www-client/fallback )"
	pkg(g, "www-client/preferred", "1", "0", "0", true, nil)
	pkg(g, "www-client/preferred", "2", "0", "0", false, nil)
	pkg(g, "www-client/fallback", "1", "0", "0", true, nil)
	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.Deep = true

	result, err := Resolve(g, []string{"app-misc/parent"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var promoted bool
	for _, action := range result.Install {
		promoted = promoted || action.Atom.CP() == "www-client/preferred" && action.Atom.Version != nil && action.Atom.Version.Raw == "2"
	}
	if !promoted {
		t.Fatalf("installed any-of alternative was not updated during deep traversal: %v", result.Install)
	}
}

func TestRefreshCommittedDirectUpdatesRestoresRolledBackChildUpdate(t *testing.T) {
	g := makeGraph()
	parent := pkg(g, "virtual/parent", "1", "0", "0", true, nil)
	parent.Available = true
	parent.Rdepend = "virtual/child"
	pkg(g, "virtual/child", "1", "0", "0", true, nil)
	pkg(g, "virtual/child", "2", "0", "0", false, nil)
	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.Deep = true
	r := &resolver{
		graph: g, config: cfg,
		installed: make(map[string]*PkgAction), toInstall: make(map[string]*PkgAction),
		actionOwners: make(map[string]map[string]bool), rootActionKeys: make(map[string]bool),
		toUninstall: make(map[string]*PkgAction), seenDeps: make(map[string]bool),
		activeDeps: make(map[string]int), selectedCPs: make(map[string]bool),
		explicitTargets: make(map[string]bool), constraints: make(map[string][]*atom.Atom),
		constraintCauses: make(map[string][]ConflictRequirement), useOverrides: make(map[string]map[string]bool),
		useChangeSeen: make(map[string]bool), baseUseCache: make(map[string]map[string]bool),
		maskCache: make(map[string]portage.MaskStatus), keywordCache: make(map[string]bool),
		backtrackRemaining: cfg.Backtrack,
	}
	r.seenDeps[dependencyVersionKey("virtual/parent", parent.Version, parent.Slot, parent.Repository)] = true
	if err := r.refreshCommittedDirectUpdates(); err != nil {
		t.Fatal(err)
	}
	var updated bool
	for _, action := range r.toInstall {
		updated = updated || action.Atom.CP() == "virtual/child" && action.Atom.Version != nil && action.Atom.Version.Raw == "2"
	}
	if !updated {
		t.Fatalf("committed parent did not restore direct child update: %v", r.toInstall)
	}
}

func TestResolve_AnyOfBacktracksWhenFirstAlternativeDepsFail(t *testing.T) {
	g := makeGraph()
	consumer := pkg(g, "app-misc/consumer", "1", "0", "0", false, nil)
	consumer.Rdepend = "|| ( media-plugins/broken media-plugins/working )"
	broken := pkg(g, "media-plugins/broken", "1", "0", "0", false, nil)
	broken.Rdepend = "media-libs/missing"
	pkg(g, "media-plugins/working", "1", "0", "0", false, nil)

	result, err := Resolve(g, []string{"app-misc/consumer"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	var selectedBroken, selectedWorking bool
	for _, action := range result.Install {
		selectedBroken = selectedBroken || action.Atom.CP() == "media-plugins/broken"
		selectedWorking = selectedWorking || action.Atom.CP() == "media-plugins/working"
	}
	if selectedBroken || !selectedWorking {
		t.Fatalf("any-of branch was not rolled back: %v", result.Install)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("failed branch leaked conflicts: %v", result.Conflicts)
	}
	if result.BacktrackLevel != 1 {
		t.Fatalf("backtrack level = %d, want 1", result.BacktrackLevel)
	}
	if len(result.DecisionHistory) != 1 || result.DecisionHistory[0].Kind != "any-of" ||
		result.DecisionHistory[0].From != "alternative 1" || result.DecisionHistory[0].To != "alternative 2" {
		t.Fatalf("any-of decision history = %#v", result.DecisionHistory)
	}
}

func TestResolve_AnyOfFailureRetainsStructuredBranchCauses(t *testing.T) {
	g := makeGraph()
	consumer := pkg(g, "app-misc/consumer", "1", "0", "0", false, nil)
	consumer.Rdepend = "|| ( media-plugins/first media-plugins/second )"
	first := pkg(g, "media-plugins/first", "1", "0", "0", false, nil)
	first.Rdepend = "<dev-libs/shared-2 >=dev-libs/shared-2"
	second := pkg(g, "media-plugins/second", "1", "0", "0", false, nil)
	second.Rdepend = "<dev-libs/shared-2 >=dev-libs/shared-2"
	pkg(g, "dev-libs/shared", "1", "0", "0", false, nil)
	pkg(g, "dev-libs/shared", "2", "0", "0", false, nil)

	result, err := Resolve(g, []string{"app-misc/consumer"}, DefaultResolveConfig())
	if err == nil {
		t.Fatal("expected every any-of alternative to fail")
	}
	if result == nil {
		t.Fatalf("expected incomplete result with structured causes: %v", err)
	}
	var slotConflicts int
	for _, detail := range result.ConflictDetails {
		if detail.Kind == "slot-conflict" && detail.Package == "dev-libs/shared" {
			slotConflicts++
			if len(detail.Requirements) != 2 {
				t.Fatalf("slot conflict requirements = %#v, want both competing atoms", detail.Requirements)
			}
		}
	}
	if slotConflicts != 2 {
		t.Fatalf("retained slot conflicts = %d, want one per failed alternative: %#v", slotConflicts, result.ConflictDetails)
	}
}

func TestResolve_AnyOfDoesNotReplayAlternativeIncompatibleWithCommittedConstraints(t *testing.T) {
	g := makeGraph()
	consumer := pkg(g, "app-misc/consumer", "1", "0", "0", false, nil)
	consumer.Rdepend = "=dev-libs/shared-1 || ( =dev-libs/shared-2 =dev-libs/shared-1 )"
	pkg(g, "dev-libs/shared", "1", "0", "0", false, nil)
	pkg(g, "dev-libs/shared", "2", "0", "0", false, nil)

	result, err := Resolve(g, []string{"app-misc/consumer"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || result.BacktrackLevel != 0 {
		t.Fatalf("known-incompatible any-of branch consumed search: verified=%v backtracks=%d conflicts=%v", result.Verified, result.BacktrackLevel, result.Conflicts)
	}
	for _, decision := range result.retryChoices {
		if decision.depKey == "app-misc/consumer->1" && len(decision.order) != 1 {
			t.Fatalf("known-incompatible alternative remained replayable: %#v", decision)
		}
	}
}

func TestBacktrackDecisionLedgerRollsBackWithSpeculativeTransaction(t *testing.T) {
	r := &resolver{backtrackRemaining: 2}
	tx := r.beginTransaction()
	if err := r.consumeBacktrack("any-of", "parent->0", "alternative 1", "alternative 2"); err != nil {
		t.Fatal(err)
	}
	if r.backtrackRemaining != 1 || len(r.decisionHistory) != 1 {
		t.Fatalf("speculative decision was not recorded: remaining=%d history=%v", r.backtrackRemaining, r.decisionHistory)
	}
	r.rollbackTransaction(tx)
	if r.backtrackRemaining != 2 || len(r.decisionHistory) != 0 {
		t.Fatalf("rolled-back decision leaked: remaining=%d history=%v", r.backtrackRemaining, r.decisionHistory)
	}
}

func TestConsumeBacktrackDoesNotRechargeDecisionPaidByEarlierReplay(t *testing.T) {
	decision := BacktrackDecision{Kind: "version", Key: "dev-python/docutils:0", From: "0.23", To: "0.22.4"}
	r := &resolver{
		backtrackRemaining: 1,
		chargedDecisions: map[string]bool{
			backtrackDecisionKey(decision.Kind, decision.Key, decision.From, decision.To): true,
		},
	}
	if err := r.consumeBacktrack(decision.Kind, decision.Key, decision.From, decision.To); err != nil {
		t.Fatal(err)
	}
	if r.backtrackRemaining != 1 || len(r.decisionHistory) != 0 {
		t.Fatalf("replayed decision was charged again: remaining=%d history=%v", r.backtrackRemaining, r.decisionHistory)
	}
}

func TestConsumeBacktrackDoesNotRechargeDecisionWithinAttempt(t *testing.T) {
	r := &resolver{backtrackRemaining: 2, chargedDecisions: make(map[string]bool)}
	for range 2 {
		if err := r.consumeBacktrack("version", "dev-python/docutils:0", "0.23", "0.22.4"); err != nil {
			t.Fatal(err)
		}
	}
	if r.backtrackRemaining != 1 || len(r.decisionHistory) != 1 {
		t.Fatalf("identical in-attempt repair was charged repeatedly: remaining=%d history=%v", r.backtrackRemaining, r.decisionHistory)
	}
}

func TestVerificationRepairStateKeyDetectsCandidateReplacement(t *testing.T) {
	graph := makeGraph()
	old := pkg(graph, "dev-python/docutils", "0.22.4", "0", "0", false, nil)
	newer := pkg(graph, "dev-python/docutils", "0.23", "0", "0", false, nil)
	r := &resolver{
		toInstall:   make(map[string]*PkgAction),
		toUninstall: make(map[string]*PkgAction),
		constraints: make(map[string][]*atom.Atom),
	}
	r.toInstall[versionActionKey("dev-python/docutils", newer)] = &PkgAction{Atom: bestVersionAtom(graph.Packages["dev-python/docutils"].Atom, newer), Slot: "0"}
	before := r.verificationRepairStateKey()
	delete(r.toInstall, versionActionKey("dev-python/docutils", newer))
	r.toInstall[versionActionKey("dev-python/docutils", old)] = &PkgAction{Atom: bestVersionAtom(graph.Packages["dev-python/docutils"].Atom, old), Slot: "0"}
	if after := r.verificationRepairStateKey(); after == before {
		t.Fatal("candidate replacement did not change verifier repair state")
	}
}

func TestResolve_LaterConstraintCancelsIncompatibleScheduledUpdate(t *testing.T) {
	graph := makeGraph()
	installed := pkg(graph, "dev-python/docutils", "0.22.4", "0", "0", true, nil)
	installed.Available = true
	pkg(graph, "dev-python/docutils", "0.23", "0", "0", false, nil)
	loose := pkg(graph, "app-misc/loose-consumer", "1", "0", "0", false, nil)
	loose.Rdepend = ">=dev-python/docutils-0.21"
	tight := pkg(graph, "dev-python/sphinx", "9.1.0", "0", "0", false, nil)
	tight.Rdepend = "<dev-python/docutils-0.23"

	cfg := DefaultResolveConfig()
	cfg.Update, cfg.Deep = true, true
	result, err := Resolve(graph, []string{"app-misc/loose-consumer", "dev-python/sphinx"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || len(result.Conflicts) != 0 {
		t.Fatalf("compatible installed candidate did not resolve shared constraints: %#v", result)
	}
	for _, action := range result.Install {
		if action.Atom != nil && action.Atom.CP() == "dev-python/docutils" {
			t.Fatalf("stale docutils replacement remained scheduled: %#v", action)
		}
	}
}

func TestResolveRewindsEarlierAnyOfAfterWholeStateConflict(t *testing.T) {
	graph := makeGraph()
	consumer := pkg(graph, "app-misc/consumer", "1", "0", "0", false, nil)
	consumer.Rdepend = "|| ( app-misc/implementation-a app-misc/implementation-b )"
	first := pkg(graph, "app-misc/implementation-a", "1", "0", "0", false, nil)
	first.Rdepend = "=dev-libs/shared-1"
	second := pkg(graph, "app-misc/implementation-b", "1", "0", "0", false, nil)
	second.Rdepend = "=dev-libs/shared-2"
	pkg(graph, "dev-libs/shared", "1", "0", "0", false, nil)
	pkg(graph, "dev-libs/shared", "2", "0", "0", false, nil)

	result, err := Resolve(graph, []string{"app-misc/consumer", "=dev-libs/shared-2"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || len(result.Conflicts) != 0 {
		t.Fatalf("rewound plan did not verify: conflicts=%v actions=%v", result.Conflicts, result.Install)
	}
	present := make(map[string]bool)
	for _, action := range result.Install {
		present[action.Atom.CP()] = true
	}
	if present["app-misc/implementation-a"] || !present["app-misc/implementation-b"] {
		t.Fatalf("earlier any-of decision was not rewound: %v", result.Install)
	}
	foundRewind := false
	for _, decision := range result.DecisionHistory {
		foundRewind = foundRewind || decision.Kind == "conflict-rewind" && strings.Contains(decision.From, "implementation-a") && strings.Contains(decision.To, "implementation-b")
	}
	if !foundRewind {
		t.Fatalf("conflict rewind missing from history: %#v", result.DecisionHistory)
	}
}

func TestResolveRewindsEarlierProviderAfterLaterConstraint(t *testing.T) {
	graph := makeGraph()
	consumer := pkg(graph, "app-misc/consumer", "1", "0", "0", false, nil)
	consumer.Rdepend = "virtual/implementation"
	first := pkg(graph, "app-misc/provider-a", "1", "0", "0", false, nil)
	first.Rdepend = "=dev-libs/shared-1"
	second := pkg(graph, "app-misc/provider-b", "1", "0", "0", false, nil)
	second.Rdepend = "=dev-libs/shared-2"
	graph.AddProvider("virtual/implementation", "app-misc/provider-a")
	graph.AddProvider("virtual/implementation", "app-misc/provider-b")
	pkg(graph, "dev-libs/shared", "1", "0", "0", false, nil)
	pkg(graph, "dev-libs/shared", "2", "0", "0", false, nil)

	result, err := Resolve(graph, []string{"app-misc/consumer", "=dev-libs/shared-2"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || len(result.Conflicts) != 0 {
		t.Fatalf("provider rewind did not verify: conflicts=%v actions=%v", result.Conflicts, result.Install)
	}
	present := make(map[string]bool)
	for _, action := range result.Install {
		present[action.Atom.CP()] = true
	}
	if present["app-misc/provider-a"] || !present["app-misc/provider-b"] {
		t.Fatalf("earlier provider was not rewound: %v", result.Install)
	}
	found := false
	for _, decision := range result.DecisionHistory {
		found = found || decision.Kind == "conflict-rewind" && decision.From == "app-misc/provider-a" && decision.To == "app-misc/provider-b"
	}
	if !found {
		t.Fatalf("provider rewind missing from history: %#v", result.DecisionHistory)
	}
}

func TestResolveRewindsEarlierVersionAfterLaterConstraint(t *testing.T) {
	graph := makeGraph()
	consumer := pkg(graph, "app-misc/consumer", "1", "0", "0", false, nil)
	consumer.Rdepend = "app-misc/implementation"
	older := pkg(graph, "app-misc/implementation", "1", "0", "0", false, nil)
	older.Rdepend = "=dev-libs/shared-2"
	newer := pkg(graph, "app-misc/implementation", "2", "0", "0", false, nil)
	newer.Rdepend = "=dev-libs/shared-1"
	pkg(graph, "dev-libs/shared", "1", "0", "0", false, nil)
	pkg(graph, "dev-libs/shared", "2", "0", "0", false, nil)

	result, err := Resolve(graph, []string{"app-misc/consumer", "=dev-libs/shared-2"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || len(result.Conflicts) != 0 {
		t.Fatalf("version rewind did not verify: conflicts=%v actions=%v", result.Conflicts, result.Install)
	}
	for _, action := range result.Install {
		if action.Atom.CP() == "app-misc/implementation" && action.Atom.Version.Raw != "1" {
			t.Fatalf("earlier version was not rewound: %v", result.Install)
		}
	}
	found := false
	for _, decision := range result.DecisionHistory {
		found = found || decision.Kind == "conflict-rewind" && decision.From == "app-misc/implementation-2" && decision.To == "app-misc/implementation-1"
	}
	if !found {
		t.Fatalf("version rewind missing from history: %#v", result.DecisionHistory)
	}
}

func TestResolveExplicitUpdateCannotReplayToInstalledNoop(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-misc/target", "0", "0", "0", false, nil)
	pkg(g, "app-misc/target", "1", "0", "0", true, nil)
	target := pkg(g, "app-misc/target", "2", "0", "0", false, nil)
	target.Rdepend = ">=dev-libs/library-2"
	pkg(g, "dev-libs/library", "1", "0", "0", true, nil)
	pkg(g, "dev-libs/library", "2", "0", "0", false, nil)
	consumer := pkg(g, "app-misc/consumer", "1", "0", "0", true, nil)
	consumer.InstalledRdepend = "<dev-libs/library-2"

	cfg := DefaultResolveConfig()
	cfg.Deep = false
	cfg.DynamicDeps = false
	result, err := Resolve(g, []string{"app-misc/target"}, cfg)
	if err == nil && result.Verified && len(result.Install) == 0 {
		t.Fatalf("explicit update silently replayed to installed no-op: %#v", result)
	}
	if result != nil {
		for _, decision := range result.DecisionHistory {
			if decision.Key == "version:app-misc/target:0" && (decision.To == "app-misc/target-1" || decision.To == "app-misc/target-0") {
				t.Fatalf("explicit target replayed at or below installed version: %#v", result.DecisionHistory)
			}
		}
	}
}

func TestResolveSpeculatesReplayAlternativesAndCommitsPreferenceOrder(t *testing.T) {
	graph := makeGraph()
	consumer := pkg(graph, "app-misc/consumer", "1", "0", "0", false, nil)
	consumer.Rdepend = "|| ( app-misc/first app-misc/second app-misc/third )"
	for _, cp := range []string{"app-misc/first", "app-misc/second"} {
		candidate := pkg(graph, cp, "1", "0", "0", false, nil)
		candidate.Rdepend = "=dev-libs/shared-1"
	}
	third := pkg(graph, "app-misc/third", "1", "0", "0", false, nil)
	third.Rdepend = "=dev-libs/shared-2"
	pkg(graph, "dev-libs/shared", "1", "0", "0", false, nil)
	pkg(graph, "dev-libs/shared", "2", "0", "0", false, nil)
	config := DefaultResolveConfig()
	config.Jobs = 3

	result, err := Resolve(graph, []string{"app-misc/consumer", "=dev-libs/shared-2"}, config)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified {
		t.Fatalf("speculative replay did not verify: %v", result.Conflicts)
	}
	present := make(map[string]bool)
	for _, action := range result.Install {
		present[action.Atom.CP()] = true
	}
	if present["app-misc/first"] || present["app-misc/second"] || !present["app-misc/third"] {
		t.Fatalf("wrong speculative winner: %v", result.Install)
	}
	found := false
	for _, decision := range result.DecisionHistory {
		found = found || decision.Kind == "conflict-rewind" && strings.Contains(decision.From, "first") && strings.Contains(decision.To, "third")
	}
	if !found {
		t.Fatalf("speculative rewind missing from history: %#v", result.DecisionHistory)
	}
	if len(result.BranchEvaluations) != 2 || !strings.Contains(result.BranchEvaluations[0].Option, "second") ||
		result.BranchEvaluations[0].Outcome == "verified" || !strings.Contains(result.BranchEvaluations[1].Option, "third") ||
		result.BranchEvaluations[1].Outcome != "verified" {
		t.Fatalf("branch evaluations = %#v", result.BranchEvaluations)
	}
}

func TestResolveRejectsRequiredUseCardinalityOperatorsInDependencies(t *testing.T) {
	for _, expression := range []string{
		"^^ ( app-editors/vim app-editors/nano )",
		"?? ( app-editors/vim app-editors/nano )",
		"|| ( )",
	} {
		t.Run(expression, func(t *testing.T) {
			graph := makeGraph()
			consumer := pkg(graph, "app-misc/consumer", "1", "0", "0", false, nil)
			consumer.Rdepend = expression
			pkg(graph, "app-editors/vim", "1", "0", "0", false, nil)
			pkg(graph, "app-editors/nano", "1", "0", "0", false, nil)
			result, err := Resolve(graph, []string{"app-misc/consumer"}, DefaultResolveConfig())
			if err == nil {
				t.Fatalf("invalid package dependency resolved: %#v", result)
			}
			if !strings.Contains(err.Error(), "package dependency") && !strings.Contains(err.Error(), "dependency class") {
				t.Fatalf("invalid dependency error = %v", err)
			}
		})
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

func TestResolve_PackageProvidedRespectsVersionConstraint(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-lang/python", "3.13", "3.13", "3.13", false, nil)
	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{PackageProvided: []string{"dev-lang/python-3.11"}}
	result, err := Resolve(g, []string{">=dev-lang/python-3.12"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Install) != 1 || result.Install[0].Atom.Version.Raw != "3.13" {
		t.Fatalf("old provided version satisfied newer constraint: %v", result.Install)
	}
}

func TestResolve_PackageProvidedSatisfiesCompatibleVersionConstraint(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-lang/python", "3.13", "3.13", "3.13", false, nil)
	cfg := DefaultResolveConfig()
	cfg.PortageConfig = &portage.Config{PackageProvided: []string{"dev-lang/python-3.12"}}
	result, err := Resolve(g, []string{">=dev-lang/python-3.12"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Install) != 0 {
		t.Fatalf("compatible provided version was ignored: %v", result.Install)
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

	python, _ := atom.Parse("dev-lang/python")
	vim, _ := atom.Parse("app-editors/vim")
	gcc, _ := atom.Parse("sys-devel/gcc")
	if !r.isPackageProvided(python) {
		t.Error("dev-lang/python should be provided")
	}
	if !r.isPackageProvided(vim) {
		t.Error("app-editors/vim should be provided")
	}
	if r.isPackageProvided(gcc) {
		t.Error("sys-devel/gcc should not be provided")
	}
}

func Test_isPackageProvided_NilConfig(t *testing.T) {
	r := &resolver{portageConfig: nil}
	requirement, _ := atom.Parse("dev-lang/python")
	if r.isPackageProvided(requirement) {
		t.Error("nil config should return false")
	}
}

func TestResolve_WithBdepsN(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-shells/bash", "5.0", "0", "0", true, nil)
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

func TestResolve_NewPackageAlwaysIncludesBdeps(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-shells/bash", "5.0", "0", "0", false, nil)
	pkg(g, "dev-build/make", "4.4", "0", "0", false, nil)
	depWithType(g, "app-shells/bash", ">=dev-build/make-4", DepTypeBuild)
	cfg := DefaultResolveConfig()
	cfg.WithBdeps = "n"

	result, err := Resolve(g, []string{"app-shells/bash"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, action := range result.Install {
		found = found || action.Atom.CP() == "dev-build/make"
	}
	if !found {
		t.Fatalf("BDEPEND of a package being built was omitted: %v", result.Install)
	}
}

func TestResolve_NonDeepInstalledBdepDoesNotPromoteRuntimeClosure(t *testing.T) {
	g := makeGraph()
	pkg(g, "app-misc/target", "1", "0", "0", false, nil)
	pkg(g, "dev-build/tool", "1", "0", "0", true, nil)
	pkg(g, "dev-python/helper", "1", "0", "0", true, map[string]bool{"python_targets_python3_14": false})
	pkg(g, "dev-python/helper", "2", "0", "0", false, map[string]bool{"python_targets_python3_14": true})
	depWithType(g, "app-misc/target", "dev-build/tool", DepTypeBuild)
	depWithType(g, "dev-build/tool", "dev-python/helper[python_targets_python3_14]", DepTypeRuntime)

	cfg := DefaultResolveConfig()
	cfg.Deep = false
	result, err := Resolve(g, []string{"app-misc/target"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range result.Install {
		if action.Atom.CP() == "dev-python/helper" {
			t.Fatalf("non-deep installed BDEPEND promoted current runtime metadata: %#v", result.Install)
		}
	}
	if !result.Verified {
		t.Fatalf("installed build tool did not satisfy non-deep plan: %#v", result)
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

func TestResolve_WithBdepsAutoUpdatesRetainedBuildDependencyForSourcePlan(t *testing.T) {
	makeGraph := func() *DepGraph {
		g := makeGraph()
		parent := pkg(g, "app-shells/bash", "5.0", "0", "0", true, nil)
		parent.Available = true
		parent.Bdepend = ">=dev-build/make-4"
		pkg(g, "dev-build/make", "4.4", "0", "0", true, nil)
		pkg(g, "dev-build/make", "4.5", "0", "0", false, nil)
		return g
	}
	cfg := DefaultResolveConfig()
	cfg.Update, cfg.Deep = true, true

	result, err := Resolve(makeGraph(), []string{"app-shells/bash"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !actionForCP(result.Install, "dev-build/make") {
		t.Fatalf("source auto mode omitted retained BDEPEND update: %v", result.Install)
	}

	cfg.UsePkg = true
	result, err = Resolve(makeGraph(), []string{"app-shells/bash"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if actionForCP(result.Install, "dev-build/make") {
		t.Fatalf("binary auto mode unexpectedly pulled retained BDEPEND: %v", result.Install)
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

func TestCandidateUseFlagsSharesImmutableBaseAndIsolatesOverrides(t *testing.T) {
	version, err := atom.ParseVersion("1")
	if err != nil {
		t.Fatal(err)
	}
	vi := &VersionInfo{Version: version, Slot: "0", Repository: "gentoo", UseFlags: map[string]bool{"foo": false, "bar": true}}
	node := &PkgNode{Atom: mustParseAtom("cat/pkg"), Versions: map[string]*VersionInfo{"1": vi}}
	vi.Package = node
	r := &resolver{useOverrides: make(map[string]map[string]bool)}

	base := r.candidateUseFlags(node, vi)
	if base["foo"] || !base["bar"] {
		t.Fatalf("unexpected base USE: %#v", base)
	}
	r.setUseOverride(r.versionKey(node, vi), "foo", true)
	overridden := r.candidateUseFlags(node, vi)
	if !overridden["foo"] || !overridden["bar"] {
		t.Fatalf("override not applied: %#v", overridden)
	}
	if base["foo"] || vi.UseFlags["foo"] {
		t.Fatalf("override mutated immutable base: base=%#v metadata=%#v", base, vi.UseFlags)
	}
}

func TestTerminalSetTargetConflictStopsReplay(t *testing.T) {
	if !hasTerminalTargetConflict([]string{"no installable version of cat/pkg satisfies cat/pkg (world target)"}) {
		t.Fatal("world target conflict must be terminal")
	}
	if !hasTerminalTargetConflict([]string{"no installable version of cat/pkg satisfies cat/pkg (system target)"}) {
		t.Fatal("system target conflict must be terminal")
	}
	if hasTerminalTargetConflict([]string{"post-solve verification: cat/dep required by cat/pkg is not satisfied"}) {
		t.Fatal("transitive conflict may still be replayable")
	}
}

func TestTerminalVerificationFailureRequiresOnlyStructuredVerifierCauses(t *testing.T) {
	verifiedFailure := &ResolveResult{
		Conflicts:       []string{"post-solve verification: broken"},
		ConflictDetails: []ConflictDetail{{Kind: "post-solve-verification", Message: "post-solve verification: broken"}},
	}
	if !terminalVerificationFailure(verifiedFailure) {
		t.Fatal("pure verifier failure must be terminal without complete-graph repair")
	}
	verifiedFailure.ConflictDetails = append(verifiedFailure.ConflictDetails, ConflictDetail{Kind: "slot-conflict", Message: "slot conflict"})
	if terminalVerificationFailure(verifiedFailure) {
		t.Fatal("mixed candidate and verifier failures must remain replayable")
	}
}

func TestNextReplayChoiceRetainsDeterministicFallback(t *testing.T) {
	first := replayDecision{depKey: "cat/first->0", chosen: 0, order: []int{0, 1}}
	last := replayDecision{depKey: "cat/last->0", chosen: 0, order: []int{0, 1}}
	decision, next, ok := nextReplayChoice([]replayDecision{first, last})
	if !ok || decision.depKey != last.depKey || next != 1 {
		t.Fatalf("selected %q option %d, want last deterministic fallback", decision.depKey, next)
	}
}

func TestNextUnvisitedReplayStateSkipsCycleAndRewindsParent(t *testing.T) {
	first := replayDecision{depKey: "cat/first->0", chosen: 0, order: []int{0, 1}}
	last := replayDecision{depKey: "cat/last->0", chosen: 0, order: []int{0, 1}}
	visited := map[string]bool{
		replayStateKey(map[string]int{"cat/first->0": 0, "cat/last->0": 1}): true,
	}
	decision, next, prefix, state, ok := nextUnvisitedReplayState([]replayDecision{first, last}, visited)
	if !ok || decision.depKey != first.depKey || next != 1 {
		t.Fatalf("selected %q option %d, want unvisited parent fallback", decision.depKey, next)
	}
	if len(prefix) != 0 || !reflect.DeepEqual(state, map[string]int{"cat/first->0": 1}) {
		t.Fatalf("rewound replay state leaked child overrides: prefix=%v state=%v", prefix, state)
	}
}

func TestReplayStateKeyIsMapOrderIndependent(t *testing.T) {
	left := map[string]int{"cat/a->0": 1, "cat/b->0": 2}
	right := map[string]int{"cat/b->0": 2, "cat/a->0": 1}
	if replayStateKey(left) != replayStateKey(right) {
		t.Fatalf("equivalent override maps produced different state keys: %q != %q", replayStateKey(left), replayStateKey(right))
	}
}

func TestDomainRemovalsRetainsRepositoryQualifiedRootKey(t *testing.T) {
	rootKey := "perl-core/Compress-Raw-Zlib-2.213.0\x00gentoo"
	brootKey := string(DomainBROOT) + "\x00dev-lang/perl-5.42.2\x00gentoo"
	filtered := domainRemovals(map[string]bool{rootKey: true, brootKey: true}, DomainROOT)
	if !filtered[rootKey] {
		t.Fatalf("repository-qualified ROOT removal was discarded: %#v", filtered)
	}
	if filtered[brootKey] {
		t.Fatalf("BROOT removal leaked into ROOT: %#v", filtered)
	}
}

func TestDependenciesForVersionHonorsKnownEmptyMetadata(t *testing.T) {
	g := makeGraph()
	node := g.AddPackage("dev-libs/released")
	g.AddDep("dev-libs/released", "dev-build/live-only", "dev-build/live-only", DepTypeBuild, "", false)
	version := g.AddVersion("dev-libs/released", "1", "0", "0", false, nil, "amd64")
	version.DependencyMetadataKnown = true
	r := &resolver{graph: g, useOverrides: make(map[string]map[string]bool)}
	edges, err := r.dependenciesForVersion(node, version)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 0 {
		t.Fatalf("known empty metadata inherited package-level live edges: %#v", edges)
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

func setSlotOp(g *DepGraph, fromCP, toCP string, op atom.SlotOp) {
	for _, edge := range g.Packages[fromCP].Deps {
		if edge.To != nil && edge.To.Atom.CP() == toCP {
			edge.DepAtom.SlotOp = op
			if op == atom.SlotOpEq {
				if installed := edge.To.GetInstalledVersion(); installed != nil {
					edge.DepAtom.Slot = installed.Slot
					edge.DepAtom.Subslot = installed.Subslot
				}
			}
		}
	}
}

func TestProcessCompleteGraph_SubslotChangeRebuildsSlotOpRevDeps(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/somelib", "1.0", "0/1", "1", true, nil)
	pkg(g, "dev-libs/somelib", "2.0", "0/2", "2", false, nil)
	pkg(g, "app-misc/consumer", "1.0", "0", "0", true, nil)
	pkg(g, "app-misc/consumer", "1.0", "0", "0", false, nil)

	deps(g, "app-misc/consumer", "dev-libs/somelib")
	setSlotOp(g, "app-misc/consumer", "dev-libs/somelib", atom.SlotOpEq)

	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.CompleteGraph = true

	result, err := Resolve(g, []string{"dev-libs/somelib"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	consumerRebuilt := false
	for _, a := range result.Install {
		if a.Atom.CP() == "app-misc/consumer" && a.Action == "reinstall" {
			consumerRebuilt = true
		}
	}
	if !consumerRebuilt {
		t.Error("consumer should be rebuilt when somelib subslot changes")
	}
}

func TestProcessCompleteGraph_WorldProviderRebuildsInstalledConsumerOutsideTraversal(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-lang/go", "1.26.4", "0", "1.26.4", true, nil)
	pkg(g, "dev-lang/go", "1.26.5", "0", "1.26.5", false, nil)
	consumer := pkg(g, "dev-go/gopls", "0.21.1", "0", "0", true, nil)
	pkg(g, "dev-go/gopls", "0.21.1", "0", "0", false, nil)
	consumer.EAPI = "8"
	consumer.InstalledEAPI = "8"
	consumer.DependencyMetadataKnown = true
	consumer.InstalledBdepend = ">=dev-lang/go-1.24.11:0/1.26.4="

	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.Deep = true
	cfg.CompleteGraph = true
	cfg.WorldSet = &WorldSet{Entries: []string{"dev-lang/go"}}

	result, err := Resolve(g, []string{"@world"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasAction(result.Install, "dev-go/gopls-0.21.1", "reinstall") {
		t.Fatalf("complete graph omitted installed consumer outside world traversal: %v", result.Install)
	}
}

func TestProcessCompleteGraph_RebuildsStaleInstalledSlotOperatorBinding(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-lang/go", "1.26.4", "0", "1.26.4", true, nil)
	consumer := pkg(g, "dev-util/github-cli", "2.93.0", "0", "0", true, nil)
	pkg(g, "dev-util/github-cli", "2.93.0", "0", "0", false, nil)
	consumer.EAPI = "8"
	consumer.InstalledEAPI = "8"
	consumer.DependencyMetadataKnown = true
	consumer.InstalledBdepend = ">=dev-lang/go-1.24.11:0/1.26.3="

	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.Deep = true
	cfg.CompleteGraph = true

	result, err := Resolve(g, []string{"dev-util/github-cli"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasAction(result.Install, "dev-util/github-cli-2.93.0", "reinstall") {
		t.Fatalf("stale installed := binding did not rebuild consumer: %v", result.Install)
	}
}

func TestProcessCompleteGraph_StaleBindingUpgradeUsesReplacementLifecycle(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-lang/perl", "5.42.2", "0", "5.42", true, nil)
	installed := pkgKeywords(g, "dev-perl/consumer", "1", "0", "0", true, nil, "amd64")
	candidate := pkgKeywords(g, "dev-perl/consumer", "2", "0", "0", false, nil, "amd64")
	installed.InstalledRdepend = "dev-lang/perl:0/5.40="
	installed.InstalledEAPI, installed.DependencyMetadataKnown = "8", true
	candidate.Rdepend, candidate.EAPI, candidate.DependencyMetadataKnown = "dev-lang/perl:=", "8", true

	action := staleSlotRepairAction(g.Packages["dev-perl/consumer"], installed, candidate, nil)
	if action.Action != "update" || action.Atom.CPV() != "dev-perl/consumer-2" ||
		action.InstalledVersion != "1" || action.InstalledSlot != "0" ||
		action.InstalledSubslot != "0" {
		t.Fatalf("stale binding upgrade lifecycle = %#v", action)
	}
	same := staleSlotRepairAction(g.Packages["dev-perl/consumer"], installed, installed, nil)
	if same.Action != "reinstall" {
		t.Fatalf("same-version stale repair = %#v", same)
	}
}

func TestProcessCompleteGraph_CurrentInstalledSlotOperatorBindingIsClean(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-lang/go", "1.26.4", "0", "1.26.4", true, nil)
	consumer := pkg(g, "dev-util/github-cli", "2.93.0", "0", "0", true, nil)
	pkg(g, "dev-util/github-cli", "2.93.0", "0", "0", false, nil)
	consumer.EAPI = "8"
	consumer.InstalledEAPI = "8"
	consumer.DependencyMetadataKnown = true
	consumer.InstalledBdepend = ">=dev-lang/go-1.24.11:0/1.26.4="

	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.Deep = true
	cfg.CompleteGraph = true

	result, err := Resolve(g, []string{"dev-util/github-cli"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if actionForCP(result.Install, "dev-util/github-cli") {
		t.Fatalf("current installed := binding triggered rebuild: %v", result.Install)
	}
}

func TestProcessCompleteGraph_RebuildSelectsVisibleVersion(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/somelib", "1.0", "0", "1", true, nil)
	pkg(g, "dev-libs/somelib", "2.0", "0", "2", false, nil)
	pkgKeywords(g, "app-misc/consumer", "1.0", "0", "0", true, nil, "amd64")
	pkgKeywords(g, "app-misc/consumer", "1.0", "0", "0", false, nil, "amd64")
	pkgKeywords(g, "app-misc/consumer", "9999", "0", "0", false, nil, "")
	deps(g, "app-misc/consumer", "dev-libs/somelib")
	setSlotOp(g, "app-misc/consumer", "dev-libs/somelib", atom.SlotOpEq)
	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.CompleteGraph = true
	cfg.PortageConfig = &portage.Config{MakeConf: map[string]string{"ARCH": "amd64"}, ACCEPT_KEYWORDS: []string{"amd64"}}

	result, err := Resolve(g, []string{"dev-libs/somelib"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range result.Install {
		if action.Atom.CP() == "app-misc/consumer" {
			if action.Atom.Version.Raw != "1.0" {
				t.Fatalf("slot rebuild selected invisible version: %v", action.Atom)
			}
			return
		}
	}
	t.Fatalf("consumer rebuild missing: %v", result.Install)
}

func TestProcessCompleteGraph_NoSubslotChangeNoRebuild(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/somelib", "1.0", "0/1", "1", true, nil)
	pkg(g, "dev-libs/somelib", "2.0", "0/1", "1", false, nil)
	pkg(g, "app-misc/consumer", "1.0", "0", "0", true, nil)

	deps(g, "app-misc/consumer", "dev-libs/somelib")
	setSlotOp(g, "app-misc/consumer", "dev-libs/somelib", atom.SlotOpEq)

	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.CompleteGraph = true

	result, err := Resolve(g, []string{"dev-libs/somelib"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, a := range result.Install {
		if a.Atom.CP() == "app-misc/consumer" {
			t.Error("consumer should NOT be rebuilt when subslot unchanged")
		}
	}
}

func TestProcessCompleteGraph_NoSlotOperatorNoRebuild(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/somelib", "1.0", "0/1", "1", true, nil)
	pkg(g, "dev-libs/somelib", "2.0", "0/2", "2", false, nil)
	pkg(g, "app-misc/consumer", "1.0", "0", "0", true, nil)

	deps(g, "app-misc/consumer", "dev-libs/somelib")

	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.CompleteGraph = true

	result, err := Resolve(g, []string{"dev-libs/somelib"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, a := range result.Install {
		if a.Atom.CP() == "app-misc/consumer" {
			t.Error("consumer should NOT be rebuilt without slot operator dep")
		}
	}
}

func TestProcessCompleteGraph_IgnoreFlagSkipsRebuild(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/somelib", "1.0", "0/1", "1", true, nil)
	pkg(g, "dev-libs/somelib", "2.0", "0/2", "2", false, nil)
	pkg(g, "app-misc/consumer", "1.0", "0", "0", true, nil)

	deps(g, "app-misc/consumer", "dev-libs/somelib")
	setSlotOp(g, "app-misc/consumer", "dev-libs/somelib", atom.SlotOpEq)

	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.CompleteGraph = true
	cfg.IgnoreBuiltSlotOperatorDeps = "y"

	result, err := Resolve(g, []string{"dev-libs/somelib"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, a := range result.Install {
		if a.Atom.CP() == "app-misc/consumer" {
			t.Error("consumer should NOT be rebuilt when ignore-built-slot-operator-deps=y")
		}
	}
}

func TestProcessCompleteGraph_EmptyGraphNoop(t *testing.T) {
	g := makeGraph()

	cfg := DefaultResolveConfig()
	cfg.CompleteGraph = true

	result, err := Resolve(g, []string{}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Install) != 0 {
		t.Errorf("empty graph should produce empty install list, got %d", len(result.Install))
	}
}

func TestProcessCompleteGraph_NotInstalledRevDepNoRebuild(t *testing.T) {
	g := makeGraph()
	pkg(g, "dev-libs/somelib", "1.0", "0/1", "1", true, nil)
	pkg(g, "dev-libs/somelib", "2.0", "0/2", "2", false, nil)
	pkg(g, "app-misc/consumer", "2.0", "0", "0", false, nil)

	deps(g, "app-misc/consumer", "dev-libs/somelib")
	setSlotOp(g, "app-misc/consumer", "dev-libs/somelib", atom.SlotOpEq)

	cfg := DefaultResolveConfig()
	cfg.Update = true
	cfg.CompleteGraph = true

	result, err := Resolve(g, []string{"dev-libs/somelib"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	somelibUpdated := false
	for _, a := range result.Install {
		if a.Atom.CP() == "dev-libs/somelib" {
			somelibUpdated = true
		}
	}
	if !somelibUpdated {
		t.Error("somelib should be in install list (update)")
	}
}
