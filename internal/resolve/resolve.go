// Package resolve provides the core dependency resolverfor the arise project
// manager. It implements a backtracking dependency resolution algorithm
// equivalent to emerge's --backtrack functionality.
package resolve

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/depstring"
	"github.com/airencracken/arise/internal/portage"
)

// ResolveConfig holds configuration parameters for the dependency resolver.
type ResolveConfig struct {
	Backtrack                    int    // max backtrack levels (default 10)
	Deep                         bool   // -D, consider full dependency tree
	CompleteGraph                bool   // rebuild reverse deps when packages change
	NewUse                       bool   // -N, rebuild when USE flags changed
	Update                       bool   // -u, update packages
	Oneshot                      bool   // -1, install without adding to world
	NoDeps                       bool   // skip dependency resolution
	OnlyDeps                     bool   // only install dependencies, not target
	EmptyTree                    bool   // -e, rebuild entire tree as if empty
	Reinstall                    bool   // force reinstall of already-installed packages
	ChangedUse                   bool   // reinstall when USE flags changed
	ChangedDeps                  bool   // reinstall when DEPENDs changed
	KeepGoing                    bool   // continue on errors
	Exclude                      []string // packages to exclude
	FetchOnly                    bool   // only fetch sources
	BuildPkgOnly                 bool   // build binary packages without installing
	Pretend                      bool   // -p, dry run
	Ask                          bool   // -a, prompt before proceeding
	Quiet                        bool   // -q, minimal output
	Verbose                      bool   // -v, verbose output
	Tree                         bool   // -t, display dependency tree
	Resume                       bool   // resume last operation
	SkipFirst                    bool   // skip first package in resume
	AutoUnmaskWrite              bool   // --autounmask-write, write package.unmask entries
	UnsortedDisplay              bool   // --unordered-display, don't sort results
	Jobs                         int    // -j, parallel jobs
	LoadAverage                  float64 // --load-average
	IgnoreBuiltSlotOperatorDeps  string // --ignore-built-slot-operator-deps
	WithBdeps                    string // --with-bdeps (y/n)
	WithBdepsAuto                bool   // --with-bdeps-auto
	BinpkgRespectUse             bool   // --binpkg-respect-use
	UsePkg                       bool   // -k, use binary packages
	UsePkgOnly                   bool   // -K, only use binary packages
	BuildPkg                     bool   // -b, build binary packages
	NoReplace                    bool   // --noreplace, skip if already installed
	BinpkgDir                    string // directory for binary packages
	BinhostURLs                  []string // remote binhost URLs
	GetBinPkg                    bool   // --getbinpkg
	GetBinPkgOnly                bool   // --getbinpkgonly
	PortageConfig                *portage.Config
}

func (c *ResolveConfig) Defaults() {
	if c.Backtrack <= 0 {
		c.Backtrack = 10
	}
}

// ResolveResult holds the result of a dependency resolution.
type ResolveResult struct {
	Install        []PkgAction // packages to install/update
	Uninstall      []PkgAction // packages to remove (blocks)
	Conflicts      []string    // list of unresolvable conflicts
	BacktrackLevel int         // how many backtrack levels were used
}

// PkgAction describes a single package action (install, update, uninstall, etc.)
type PkgAction struct {
	Atom     *atom.Atom // the package
	Action   string     // "install", "update", "reinstall", "uninstall"
	Reason   string     // why (e.g. "dependency of @world", "blocked by ...")
	Slot     string     // the slot of the package to install
	Subslot  string     // the subslot
	Unsorted bool       // if true, exclude from topological sort
}

// PkgNode represents a package in the dependency graph.
type PkgNode struct {
	Atom      *atom.Atom
	Installed bool
	Versions  map[string]*VersionInfo // version string -> version info
	Slots     map[string][]*VersionInfo // slot -> versions in that slot
	Deps      []*DepEdge              // dependency edges
	RevDeps   []*DepEdge              // reverse dependency edges
}

// VersionInfo holds per-version data for a package.
type VersionInfo struct {
	Package     *PkgNode
	Version     *atom.Version
	Slot        string
	Subslot     string
	UseFlags    map[string]bool
	Installed   bool
	DepStr      string    // raw dependency string (combined)
	Depend      string    // DEPEND value
	Rdepend     string    // RDEPEND value
	Keywords    string    // ebuild keywords (e.g. "amd64 ~x86")
	RequiredUse string    // REQUIRED_USE constraint
	License     string    // LICENSE value
}

// DepEdge represents a dependency relationship between packages.
type DepEdge struct {
	From     *PkgNode
	To       *PkgNode
	Type     DepType
	DepAtom  *atom.Atom  // the atom as specified in the dep string
	UseCond  string      // USE flag condition (empty = always)
	Block    bool        // is this a blocker?
	AnyOf    []*DepAtom  // if non-nil, this is an any-of group
}

// DepType classifies a dependency.
type DepType int

const (
	_ DepType = iota
	DepTypeBuild       // BDEPEND
	DepTypeRuntime     // RDEPEND
	DepTypeDepend      // DEPEND
	DepTypePost        // PDEPEND
	DepTypeInstall     // IDEPEND
)

// DepAtom holds a dependency atom and metadata.
type DepAtom struct {
	Atom    *atom.Atom
	UseCond string
	Block   bool
	AnyOf   []*DepAtom
}

// WorldSet represents the set of explicitly requested packages.
type WorldSet struct {
	Entries []string
}

// DepGraph is the dependency graph used by the resolver.
type DepGraph struct {
	Packages    map[string]*PkgNode // CP -> node
	Providers   map[string]string   // provider CP -> virtual CP (which virtual this package provides)
	ProvidersOf map[string][]string // virtual CP -> provider CPs that satisfy the virtual
}

// NewDepGraph creates a new empty dependency graph.
func NewDepGraph() *DepGraph {
	return &DepGraph{
		Packages:    make(map[string]*PkgNode),
		Providers:   make(map[string]string),
		ProvidersOf: make(map[string][]string),
	}
}

// AddPackage adds a package node to the graph.
func (g *DepGraph) AddPackage(cp string) *PkgNode {
	if n, ok := g.Packages[cp]; ok {
		return n
	}
	n := &PkgNode{
		Atom:     mustParseAtom(cp),
		Versions: make(map[string]*VersionInfo),
		Slots:    make(map[string][]*VersionInfo),
	}
	g.Packages[cp] = n
	return n
}

// AddProvider records that providerCP provides virtualCP.
func (g *DepGraph) AddProvider(virtualCP, providerCP string) {
	g.Providers[providerCP] = virtualCP
	g.ProvidersOf[virtualCP] = append(g.ProvidersOf[virtualCP], providerCP)
}

// AddVersion adds a version to an existing package in the graph.
// If a version with the same key already exists, it merges metadata
// (e.g. installed flag and DEPEND strings) rather than overwriting.
func (g *DepGraph) AddVersion(cp, version, slot, subslot string, installed bool, useFlags map[string]bool, keywords string) *VersionInfo {
	n := g.AddPackage(cp)
	key := version
	if existing, ok := n.Versions[key]; ok {
		if installed {
			existing.Installed = true
			n.Installed = true
			if existing.Version != nil {
				n.Atom.Version = existing.Version
				n.Atom.Slot = slot
				n.Atom.Subslot = subslot
			}
		}
		return existing
	}
	v, err := atom.Parse(cp + "-" + version)
	if err != nil {
		v, _ = atom.Parse(cp)
	}
	var ver *atom.Version
	if v != nil {
		ver = v.Version
	}
	vi := &VersionInfo{
		Package:   n,
		Version:   ver,
		Slot:      slot,
		Subslot:   subslot,
		UseFlags:  useFlags,
		Installed: installed,
		Keywords:  keywords,
	}
	n.Versions[key] = vi
	n.Slots[slot] = append(n.Slots[slot], vi)
	if installed {
		n.Installed = true
		if n.Atom != nil && ver != nil {
			n.Atom.Version = ver
			n.Atom.Slot = slot
			n.Atom.Subslot = subslot
		}
	}
	return vi
}

// AddDep adds a dependency edge between packages.
func (g *DepGraph) AddDep(fromCP, toCP, depAtomStr string, depType DepType, useCond string, block bool) {
	from := g.AddPackage(fromCP)
	to := g.AddPackage(toCP)
	depAtom, err := atom.Parse(depAtomStr)
	if err != nil {
		// best-effort parse
		depAtom = &atom.Atom{Category: to.Atom.Category, Package: to.Atom.Package}
	}
	edge := &DepEdge{
		From:    from,
		To:      to,
		Type:    depType,
		DepAtom: depAtom,
		UseCond: useCond,
		Block:   block,
	}
	from.Deps = append(from.Deps, edge)
	to.RevDeps = append(to.RevDeps, edge)
}

// AddAnyOfDep adds an any-of (||) dependency edge.
func (g *DepGraph) AddAnyOfDep(fromCP string, depType DepType, options []*DepAtom) {
	from := g.AddPackage(fromCP)
	edge := &DepEdge{
		From:  from,
		Type:  depType,
		AnyOf: options,
	}
	from.Deps = append(from.Deps, edge)
	for _, opt := range options {
		if opt.Atom != nil {
			to := g.AddPackage(opt.Atom.CP())
			to.RevDeps = append(to.RevDeps, edge)
		}
	}
}

// GetInstalledVersion returns the installed version info for a package, if any.
func (n *PkgNode) GetInstalledVersion() *VersionInfo {
	for _, vi := range n.Versions {
		if vi != nil && vi.Installed {
			return vi
		}
	}
	return nil
}

// GetBestVersion returns the highest available version for a package.
func (n *PkgNode) GetBestVersion() *VersionInfo {
	var best *VersionInfo
	for _, vi := range n.Versions {
		if vi == nil {
			continue
		}
		if best == nil || (vi.Version != nil && best.Version != nil && vi.Version.Compare(best.Version) > 0) {
			best = vi
		}
	}
	return best
}

// GetVersion returns the version matching the given string.
func (n *PkgNode) GetVersion(vStr string) *VersionInfo {
	return n.Versions[vStr]
}

// FindMatchingVersion finds a version that satisfies the given constraint atom.
func (n *PkgNode) FindMatchingVersion(constraint *atom.Atom) *VersionInfo {
	// find best version that matches
	var best *VersionInfo
	for _, vi := range n.Versions {
		if vi == nil {
			continue
		}
		if !atomMatches(n.Atom, constraint, vi.Slot, vi.Subslot, vi.UseFlags, vi.Version) {
			continue
		}
		if best == nil || (vi.Version != nil && best.Version != nil && vi.Version.Compare(best.Version) > 0) {
			best = vi
		}
	}
	return best
}

// DefaultResolveConfig returns a ResolveConfig with sensible defaults.
func DefaultResolveConfig() ResolveConfig {
	return ResolveConfig{
		Backtrack:  10,
		Deep:       true,
		Update:     false,
		Jobs:       1,
		KeepGoing:  false,
		WithBdeps:  "n",
	}
}

// Resolve performs dependency resolution on the given graph for the specified
// targets. It implements the full backtracking algorithm equivalent to
// emerge's --backtrack functionality.
func Resolve(g *DepGraph, targets []string, config ResolveConfig) (*ResolveResult, error) {
	if g == nil {
		return nil, fmt.Errorf("resolve: nil dependency graph")
	}

	r := &resolver{
		graph:    g,
		config:   config,
		installed: make(map[string]*PkgAction),
		toInstall: make(map[string]*PkgAction),
		toUninstall: make(map[string]*PkgAction),
		conflicts: []string{},
		seenDeps: make(map[string]bool),
		portageConfig: config.PortageConfig,
	}

	if config.Backtrack <= 0 {
		config.Backtrack = 10
	}
	r.backtrackRemaining = config.Backtrack

	if config.EmptyTree {
		for _, pkg := range g.Packages {
			for _, vi := range pkg.Versions {
				vi.Installed = false
			}
			pkg.Installed = false
		}
	}

	// step 1: parse and expand targets
	targetAtoms, err := r.expandTargets(targets)
	if err != nil {
		if config.KeepGoing {
			return r.buildResult()
		}
		return nil, fmt.Errorf("resolve: expand targets: %w", err)
	}
	r.targetAtoms = targetAtoms

	if len(targetAtoms) == 0 {
		return r.buildResult()
	}

	if config.NoDeps {
		for _, target := range targetAtoms {
			cp := target.CP()
			node := g.Packages[cp]
			if node == nil && !config.KeepGoing {
				return nil, fmt.Errorf("resolve: unknown package: %s", cp)
			}
			if node != nil {
				vi := r.findMatchingVersion(node, target)
				if vi == nil {
					if !config.KeepGoing {
						return nil, fmt.Errorf("resolve: no matching version for %s", target)
					}
					continue
				}
				action := "install"
				reason := "explicit (nodeps)"
				if node.Installed && !config.Reinstall {
					action = "update"
				}
				if config.Oneshot {
					reason = "explicit (oneshot, nodeps)"
				}
				r.toInstall[cp+"-"+vi.Version.Raw] = &PkgAction{
					Atom:   node.Atom,
					Action: action,
					Reason: reason,
				}
			}
		}
		return r.buildResult()
	}

	targetCPs := make(map[string]bool)
	for _, t := range targetAtoms {
		targetCPs[t.CP()] = true
	}

	// step 2-6: build the install plan with backtracking
	err = r.resolveTargets(targetAtoms)
	if err != nil {
		if config.KeepGoing {
			return r.buildResult()
		}
		return nil, fmt.Errorf("resolve: %w", err)
	}

	if config.OnlyDeps {
		for a := range r.toInstall {
			if targetCPs[a] {
				delete(r.toInstall, a)
			}
		}
	}

	if len(config.Exclude) > 0 {
		excludeSet := make(map[string]bool)
		for _, ex := range config.Exclude {
			excludeSet[ex] = true
		}
		for a := range r.toInstall {
			if excludeSet[a] {
				delete(r.toInstall, a)
			}
		}
	}

	// step 5: CompleteGraph — rebuild reverse deps when packages change
	if config.CompleteGraph {
		r.processCompleteGraph()
	}

	// step 6: topologically sort install actions
	install := SortByDeps(mapToSlice(r.toInstall), g)

	return &ResolveResult{
		Install:        install,
		Uninstall:      mapToSlice(r.toUninstall),
		Conflicts:      r.conflicts,
		BacktrackLevel: config.Backtrack - r.backtrackRemaining,
	}, nil
}

type resolver struct {
	graph              *DepGraph
	config             ResolveConfig
	worldSet           *WorldSet
	targetAtoms        []*atom.Atom
	installed          map[string]*PkgAction // CPV -> action
	toInstall          map[string]*PkgAction // CPV -> action
	toUninstall        map[string]*PkgAction // CPV -> action
	conflicts          []string
	seenDeps           map[string]bool
	backtrackRemaining int
	anyOfChoices       []anyOfDecision
	portageConfig      *portage.Config
}

type anyOfDecision struct {
	depKey    string // fromCP + "->" + index in deps
	chosen    int    // index of chosen option
	options   int    // total options
}

func (r *resolver) expandTargets(targets []string) ([]*atom.Atom, error) {
	var atoms []*atom.Atom

	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}

		// handle @world expansion
		if target == "@world" {
			if r.worldSet != nil {
				for _, entry := range r.worldSet.Entries {
					a, err := atom.Parse(entry)
					if err != nil {
						if r.config.KeepGoing {
							r.conflicts = append(r.conflicts, fmt.Sprintf("bad world entry %q: %v", entry, err))
							continue
						}
						return nil, fmt.Errorf("parse world entry %q: %w", entry, err)
					}
					atoms = append(atoms, a)
				}
			}
			continue
		}

		// parse atom
		a, err := atom.Parse(target)
		if err != nil {
			if r.config.KeepGoing {
				r.conflicts = append(r.conflicts, fmt.Sprintf("bad target %q: %v", target, err))
				continue
			}
			return nil, fmt.Errorf("parse target %q: %w", target, err)
		}
		atoms = append(atoms, a)
	}

	return atoms, nil
}

func (r *resolver) resolveTargets(targetAtoms []*atom.Atom) error {
	for _, target := range targetAtoms {
		if err := r.planPackage(target, "world target", 0); err != nil {
			return err
		}
	}
	return nil
}

func (r *resolver) planPackage(target *atom.Atom, reason string, depth int) error {
	if depth > 100 {
		return fmt.Errorf("maximum dependency depth exceeded for %s", target.CP())
	}

	cp := target.CP()

	// check package mask
	if r.isPackageMasked(cp) {
		msg := fmt.Sprintf("package masked: %s", cp)
		r.conflicts = append(r.conflicts, msg)
		if r.config.KeepGoing {
			return nil
		}
		return fmt.Errorf("resolve: %s", msg)
	}

	node := r.graph.Packages[cp]
	resolvedFromProvider := false
	if node == nil {
		// Check for virtual providers
		if providers, ok := r.graph.ProvidersOf[cp]; ok {
			for _, providerCP := range providers {
				node = r.graph.Packages[providerCP]
				if node != nil {
					r.graph.Providers[providerCP] = cp
					resolvedFromProvider = true
					break
				}
			}
		}
	}
	if node == nil {
		msg := fmt.Sprintf("unknown package: %s", cp)
		r.conflicts = append(r.conflicts, msg)
		if r.config.KeepGoing {
			return nil
		}
		return fmt.Errorf("resolve: %s", msg)
	}

	// find best version matching the target constraint
	var vi *VersionInfo
	if resolvedFromProvider {
		vi = node.GetBestVersion()
	} else {
		vi = r.findMatchingVersion(node, target)
	}
	if vi == nil {
		msg := fmt.Sprintf("no version of %s matches constraint %s", cp, target.String())
		r.conflicts = append(r.conflicts, msg)
		if r.config.KeepGoing {
			return nil
		}
		return fmt.Errorf("resolve: %s", msg)
	}

	// Check REQUIRED_USE
	if vi.RequiredUse != "" {
		if err := CheckRequiredUse(vi.RequiredUse, vi.UseFlags); err != nil {
			msg := fmt.Sprintf("REQUIRED_USE violation for %s: %v", cp, err)
			r.conflicts = append(r.conflicts, msg)
			if r.config.KeepGoing {
				return nil
			}
			return fmt.Errorf("resolve: %s", msg)
		}
	}

	// Check license
	if vi.License != "" {
		var acceptLicenses []string
		if r.portageConfig != nil {
			acceptLicenses = r.portageConfig.ACCEPT_LICENSE
		}
		if !LicenseAccepted(vi.License, acceptLicenses) {
			msg := fmt.Sprintf("license %s not accepted for %s", vi.License, cp)
			r.conflicts = append(r.conflicts, msg)
			if r.config.KeepGoing {
				return nil
			}
			return fmt.Errorf("resolve: %s", msg)
		}
	}

	// check if already installed and satisfies constraints
	installed := node.GetInstalledVersion()

	// check package.provided — treat as already installed
	if r.isPackageProvided(cp) {
		if !r.config.Update && !r.config.Reinstall {
			if installed != nil && !r.config.NoDeps && r.config.Deep {
				return r.processDeps(node, target.String(), depth+1)
			}
			return nil
		}
	}

	if installed != nil {
		if atomMatches(node.Atom, target, installed.Slot, installed.Subslot, installed.UseFlags, installed.Version) {
			// --noreplace: skip if exact same version already installed
			if r.config.NoReplace && vi != nil && installed.Version != nil && vi.Version != nil &&
				vi.Version.Raw == installed.Version.Raw {
				if r.config.Deep {
					return r.processDeps(node, target.String(), depth+1)
				}
				return nil
			}

			// already installed and satisfies constraint: decide if we need to update
			needInstall := false
			if r.config.Update && vi != nil && vi.Version != nil && installed.Version != nil && vi.Version.Compare(installed.Version) > 0 {
				needInstall = true
			}
			if r.config.NewUse {
				needInstall = true // reinstall to pick up new USE
			}
			if r.config.ChangedUse && useFlagsChanged(installed.UseFlags, vi.UseFlags) {
				needInstall = true
			}
			if r.config.ChangedDeps && depsChanged(installed, vi) {
				needInstall = true
			}

			if !needInstall && !r.config.Reinstall {
				// satisfied as-is; process deps if deep
				if r.config.Deep {
					return r.processDeps(node, target.String(), depth+1)
				}
				return nil
			}
		}
	}

	// mark for install
	cpv := cp
	action := "install"
	if vi.Version != nil && vi.Version.Raw != "" {
		cpv = cp + "-" + vi.Version.Raw
	}
	if installed != nil {
		if r.config.Update && vi.Version != nil && installed.Version != nil && vi.Version.Compare(installed.Version) > 0 {
			action = "update"
		} else if r.config.NewUse {
			action = "reinstall"
			if useFlagsChanged(installed.UseFlags, vi.UseFlags) {
				action = "update"
			}
		} else if r.config.ChangedUse {
			if useFlagsChanged(installed.UseFlags, vi.UseFlags) {
				action = "update"
			}
		} else if r.config.ChangedDeps && depsChanged(installed, vi) {
			action = "reinstall"
		} else if vi.Version != nil && installed.Version != nil && vi.Version.Compare(installed.Version) > 0 {
			action = "update"
		}
	}

	if _, exists := r.toInstall[cpv]; !exists {
		resolvedAtom := bestVersionAtom(node.Atom, vi)
		if resolvedAtom == nil {
			resolvedAtom = target
		}
		r.toInstall[cpv] = &PkgAction{
			Atom:   resolvedAtom,
			Action: action,
			Reason: reason,
			Slot:   vi.Slot,
			Subslot: vi.Subslot,
		}
	}

	// check for slot operator rebuild triggers
	if target.SlotOp == atom.SlotOpEq && installed != nil && vi != nil {
		if installed.Subslot != vi.Subslot {
			// subslot changed, this triggers rebuild of dependents
			// marked for later processing in CompleteGraph step
		}
	}

	// process dependencies
	if r.config.Deep || action != "install" {
		return r.processDeps(node, target.String(), depth+1)
	}

	return nil
}

func (r *resolver) processDeps(node *PkgNode, parent string, depth int) error {
	depKey := node.Atom.CP()
	if r.seenDeps[depKey] {
		return nil
	}
	r.seenDeps[depKey] = true

	for i, edge := range node.Deps {
		// handle any-of groups
		if len(edge.AnyOf) > 0 {
			if err := r.processAnyOf(node, edge, i, depth); err != nil {
				return err
			}
			continue
		}

		if err := r.processEdge(edge, depth); err != nil {
			return err
		}
	}

	return nil
}

func (r *resolver) processEdge(edge *DepEdge, depth int) error {
	// handle USE conditionals
	if edge.UseCond != "" {
		if !r.useFlagEnabled(edge.From, edge.UseCond) {
			return nil
		}
	}

	// handle BDEPEND auto-detection
	if edge.Type == DepTypeBuild {
		if r.config.WithBdeps == "n" && !r.config.WithBdepsAuto {
			return nil
		}
		if r.config.WithBdeps == "auto" || r.config.WithBdepsAuto {
			if edge.To != nil {
				installed := edge.To.GetInstalledVersion()
				if installed != nil {
					return nil
				}
				if r.config.UsePkg || r.config.UsePkgOnly {
					binPkgDir := r.config.BinpkgDir
					if binPkgDir == "" {
						binPkgDir = "/var/cache/binpkgs"
					}
					pkgName := edge.To.Atom.CP()
					binPkg, _ := binpkg.FindPackageMatchingUse(binPkgDir, pkgName, nil, r.config.BinpkgRespectUse)
					if binPkg != nil {
						return nil
					}
				}
			} else {
				return nil
			}
		}
	}

	// handle blockers
	if edge.Block {
		return r.processBlock(edge)
	}

	// normal dependency
	depAtom := edge.DepAtom
	if depAtom == nil {
		return nil
	}

	toNode := edge.To
	if toNode == nil {
		cp := depAtom.CP()
		toNode = r.graph.Packages[cp]
	}
	if toNode == nil {
		// Check for virtual providers
		cp := depAtom.CP()
		if providers, ok := r.graph.ProvidersOf[cp]; ok {
			for _, pcp := range providers {
				toNode = r.graph.Packages[pcp]
				if toNode != nil {
					r.graph.Providers[pcp] = cp
					break
				}
			}
		}
	}
	if toNode == nil {
		// package might be satisfied by a different provider
		if depAtom.Slot != "" {
			// search by slot
			for _, node := range r.graph.Packages {
				vi := r.findMatchingVersion(node, depAtom)
				if vi != nil {
					toNode = node
					break
				}
			}
		}
		if toNode == nil {
			msg := fmt.Sprintf("unsatisfied dependency: %s (dep of %s)", depAtom.String(), edge.From.Atom.CP())
			r.conflicts = append(r.conflicts, msg)
			if r.config.KeepGoing {
				return nil
			}
			return fmt.Errorf("resolve: %s", msg)
		}
	}

	// check if installed version satisfies the dep
	installed := toNode.GetInstalledVersion()
	if installed != nil && atomMatches(toNode.Atom, depAtom, installed.Slot, installed.Subslot, installed.UseFlags, installed.Version) {
		// dep satisfied by installed package
		// check for slot operator rebuilds
		ignoreSlotOps := r.config.IgnoreBuiltSlotOperatorDeps == "y"
		if edge.DepAtom != nil && edge.DepAtom.SlotOp == atom.SlotOpEq {
			best := toNode.GetBestVersion()
			if best != nil && installed.Subslot != best.Subslot {
				if ignoreSlotOps {
					// Update dep without triggering dependent rebuilds
					reason := fmt.Sprintf("dependency of %s", edge.From.Atom.CP())
					return r.planPackage(bestVersionAtom(toNode.Atom, best), reason, depth)
				}
				reason := fmt.Sprintf("slot operator rebuild of %s (dep of %s)", toNode.Atom.CP(), edge.From.Atom.CP())
				return r.planPackage(bestVersionAtom(toNode.Atom, best), reason, depth)
			}
		}
		// satisfied; recurse if deep
		if r.config.Deep {
			r.processDeps(toNode, depAtom.String(), depth+1)
		}
		return nil
	}

	// find best version that satisfies dep
	best := r.findMatchingVersion(toNode, depAtom)

	if best == nil {
		// Check for virtual providers
		cp := depAtom.CP()
		if providers, ok := r.graph.ProvidersOf[cp]; ok {
			for _, pcp := range providers {
				pNode := r.graph.Packages[pcp]
				if pNode != nil {
					r.graph.Providers[pcp] = cp
					toNode = pNode
					best = toNode.GetBestVersion()
					if best != nil {
						break
					}
				}
			}
		}
	}

	if best == nil {
		msg := fmt.Sprintf("no version of %s satisfies constraint %s (dep of %s)", toNode.Atom.CP(), depAtom.String(), edge.From.Atom.CP())
		r.conflicts = append(r.conflicts, msg)
		if r.config.KeepGoing {
			return nil
		}
		return fmt.Errorf("resolve: %s", msg)
	}

	// install dependency
	reason := fmt.Sprintf("dependency of %s", edge.From.Atom.CP())
	bestAt := bestVersionAtom(toNode.Atom, best)
	return r.planPackage(bestAt, reason, depth)
}

func (r *resolver) processAnyOf(node *PkgNode, edge *DepEdge, edgeIdx int, depth int) error {
	options := edge.AnyOf
	decisionKey := fmt.Sprintf("%s->%d", node.Atom.CP(), edgeIdx)

	// try each option, preferring already-installed
	type candidate struct {
		idx      int
		depAtom  *DepAtom
		installed bool
	}
	var candidates []candidate

	for i, opt := range options {
		if opt.Atom == nil {
			continue
		}
		if opt.Block {
			continue // blocks handled separately
		}
		if opt.UseCond != "" && !r.useFlagEnabled(node, opt.UseCond) {
			continue
		}

		toNode := r.graph.Packages[opt.Atom.CP()]
		if toNode == nil {
			continue
		}
		inst := toNode.GetInstalledVersion()
		satisfied := inst != nil && atomMatches(toNode.Atom, opt.Atom, inst.Slot, inst.Subslot, inst.UseFlags, inst.Version)

		candidates = append(candidates, candidate{
			idx:      i,
			depAtom:  opt,
			installed: satisfied,
		})
	}

	if len(candidates) == 0 {
		var opts []string
		for _, o := range options {
			if o.Atom != nil {
				opts = append(opts, o.Atom.String())
			}
		}
		msg := fmt.Sprintf("no satisfiable option in any-of group (||) for dep of %s: %s", node.Atom.CP(), strings.Join(opts, ", "))
		r.conflicts = append(r.conflicts, msg)
		if r.config.KeepGoing {
			return nil
		}
		return fmt.Errorf("resolve: %s", msg)
	}

	// sort: installed first, then by version
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].installed != candidates[j].installed {
			return candidates[i].installed
		}
		// prefer lower version matching first (cheapest to install)
		vi := r.graph.Packages[candidates[i].depAtom.Atom.CP()].GetBestVersion()
		vj := r.graph.Packages[candidates[j].depAtom.Atom.CP()].GetBestVersion()
		if vi != nil && vj != nil && vi.Version != nil && vj.Version != nil {
			return vi.Version.Compare(vj.Version) < 0
		}
		return i < j
	})

	// try the first candidate
	chosen := candidates[0]
	r.anyOfChoices = append(r.anyOfChoices, anyOfDecision{
		depKey:  decisionKey,
		chosen:  chosen.idx,
		options: len(options),
	})

	if chosen.installed {
		toNode := r.graph.Packages[chosen.depAtom.Atom.CP()]
		if r.config.Deep {
			return r.processDeps(toNode, chosen.depAtom.Atom.String(), depth)
		}
		return nil
	}

	// not installed; plan the installation
	reason := fmt.Sprintf("any-of dependency of %s", node.Atom.CP())
	toNode := r.graph.Packages[chosen.depAtom.Atom.CP()]
	best := r.findMatchingVersion(toNode, chosen.depAtom.Atom)
	if best == nil {
		msg := fmt.Sprintf("no satisfiable version in any-of for %s", chosen.depAtom.Atom.CP())
		r.conflicts = append(r.conflicts, msg)
		if r.config.KeepGoing {
			return nil
		}
		return fmt.Errorf("resolve: %s", msg)
	}
	return r.planPackage(bestVersionAtom(toNode.Atom, best), reason, depth)
}

func (r *resolver) processBlock(edge *DepEdge) error {
	toNode := edge.To
	if toNode == nil && edge.DepAtom != nil {
		toNode = r.graph.Packages[edge.DepAtom.CP()]
	}
	if toNode == nil {
		return nil // nothing to block
	}

	installed := toNode.GetInstalledVersion()
	if installed == nil {
		return nil // not installed, nothing to block
	}

	cp := toNode.Atom.CP()

	// check if the blocked package is a world/target package
	isWorldPkg := false
	if r.worldSet != nil {
		for _, e := range r.worldSet.Entries {
			if strings.TrimSpace(e) == cp {
				isWorldPkg = true
				break
			}
		}
	}
	for _, t := range r.targetAtoms {
		if t.CP() == cp {
			isWorldPkg = true
			break
		}
	}
	// check if it's in our install list
	for _, a := range r.toInstall {
		if a.Atom != nil && a.Atom.CP() == cp {
			isWorldPkg = true
			break
		}
	}

	blocker := edge.From.Atom.CP()

	if isWorldPkg {
		msg := fmt.Sprintf("%s (%s) blocks %s which is in world set", blocker, edge.DepAtom.String(), cp)
		r.conflicts = append(r.conflicts, msg)
		if r.config.KeepGoing {
			return nil
		}
		return fmt.Errorf("resolve: %s", msg)
	}

	// uninstall the blocked package
	cpv := cp
	if installed.Version != nil && installed.Version.Raw != "" {
		cpv = cp + "-" + installed.Version.Raw
	}
	if _, exists := r.toUninstall[cpv]; !exists {
		r.toUninstall[cpv] = &PkgAction{
			Atom:   toNode.Atom,
			Action: "uninstall",
			Reason: fmt.Sprintf("blocked by %s", blocker),
		}
	}

	return nil
}

func (r *resolver) processCompleteGraph() {
	// scan all packages being installed and rebuild reverse deps
	// whose subslot operators trigger a rebuild

	ignoreSlotOps := r.config.IgnoreBuiltSlotOperatorDeps == "y"

	processed := make(map[string]bool)

	for {
		found := false
		for _, a := range r.toInstall {
			cp := a.Atom.CP()
			if processed[cp] {
				continue
			}
			processed[cp] = true

			node := r.graph.Packages[cp]
			if node == nil {
				continue
			}

			installed := node.GetInstalledVersion()
			newVI := r.findMatchingVersion(node, a.Atom)
			if installed == nil || newVI == nil {
				continue
			}
			if installed.Subslot == "" || newVI.Subslot == "" || installed.Subslot == newVI.Subslot {
				continue
			}

			// subslot changed — rebuild all reverse deps with slot operators
			if ignoreSlotOps {
				continue
			}

			for _, edge := range node.RevDeps {
				if edge.DepAtom == nil {
					continue
				}
				if edge.DepAtom.SlotOp != atom.SlotOpEq {
					continue
				}
				dependent := edge.From
				if dependent == nil {
					continue
				}
				cpv := dependent.Atom.CP()
				if _, exists := r.toInstall[cpv]; exists {
					continue // already being rebuilt
				}
				reason := fmt.Sprintf("slot operator rebuild (subslot change in %s)", cp)
				dVI := dependent.GetBestVersion()
				if dVI == nil {
					continue
				}
				depAtom := bestVersionAtom(dependent.Atom, dVI)
				cpv = depAtom.CP()
				if depAtom.Version != nil && depAtom.Version.Raw != "" {
					cpv = cpv + "-" + depAtom.Version.Raw
				}
				r.toInstall[cpv] = &PkgAction{
					Atom:   depAtom,
					Action: "reinstall",
					Reason: reason,
					Slot:   dVI.Slot,
				}
				found = true
			}
		}
		if !found {
			break
		}
	}
}

func (r *resolver) useFlagEnabled(node *PkgNode, flag string) bool {
	// check portage config USE flags (highest priority)
	if r.portageConfig != nil {
		useFlags := r.getUseFlags(node.Atom.CP())
		if enabled, ok := useFlags[flag]; ok {
			return enabled
		}
	}

	// check target atom's USE flags
	for _, t := range r.targetAtoms {
		if t.CP() == node.Atom.CP() {
			for _, f := range t.UseFlags {
				if f.Name == flag {
					return f.Enabled
				}
			}
		}
	}

	// check installed version's USE flags
	installed := node.GetInstalledVersion()
	if installed != nil {
		if enabled, ok := installed.UseFlags[flag]; ok {
			return enabled
		}
	}

	// check best (non-installed) version's USE flags
	best := node.GetBestVersion()
	if best != nil && best != installed {
		if enabled, ok := best.UseFlags[flag]; ok {
			return enabled
		}
	}

	// default: if USE flag is not explicitly set, treat as disabled
	// to match Portage behavior where use-like deps are conditional
	return false
}

// SortByDeps topologically sorts the install actions so that dependencies
// are installed before their dependents.
func SortByDeps(actions []PkgAction, g *DepGraph) []PkgAction {
	if len(actions) <= 1 {
		return actions
	}

	// build a map for quick lookup
	planMap := make(map[string]int) // CP -> index in actions
	for i, a := range actions {
		if a.Atom != nil {
			planMap[a.Atom.CP()] = i
		}
	}

	// compute in-degrees based on dependency relationships
	inDegree := make([]int, len(actions))
	depMap := make(map[int][]int) // action index -> list of dep action indices

	for i, a := range actions {
		if a.Atom == nil {
			continue
		}
		node := g.Packages[a.Atom.CP()]
		if node == nil {
			continue
		}
		for _, edge := range node.Deps {
			if edge.Block {
				continue
			}
			// handle any-of
			if len(edge.AnyOf) > 0 {
				for _, opt := range edge.AnyOf {
					if opt.Atom != nil {
						if depIdx, ok := planMap[opt.Atom.CP()]; ok {
							depMap[depIdx] = append(depMap[depIdx], i)
							inDegree[i]++
						}
					}
				}
				continue
			}
			if edge.To != nil {
				if depIdx, ok := planMap[edge.To.Atom.CP()]; ok {
					depMap[depIdx] = append(depMap[depIdx], i)
					inDegree[i]++
				}
			}
		}
	}

	// Kahn's algorithm for topological sort
	var sorted []PkgAction
	var queue []int

	for i := 0; i < len(actions); i++ {
		if inDegree[i] == 0 {
			queue = append(queue, i)
		}
	}

	for len(queue) > 0 {
		idx := queue[0]
		queue = queue[1:]
		sorted = append(sorted, actions[idx])

		for _, dependent := range depMap[idx] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}

	// if we couldn't sort all (cycle), append remaining in original order
	if len(sorted) < len(actions) {
		seen := make(map[int]bool)
		for _, a := range sorted {
			for i, oa := range actions {
				if a.Atom != nil && oa.Atom != nil && a.Atom.CP() == oa.Atom.CP() {
					seen[i] = true
					break
				}
			}
		}
		for i, a := range actions {
			if !seen[i] {
				sorted = append(sorted, a)
			}
		}
	}

	return sorted
}

// atomMatches checks whether an installed version of a package satisfies a
// dependency atom constraint. The category and package must match; the version,
// slot, subslot, and USE flags are checked according to their respective
// operators in the constraint.
// pkgVersion is the optional version of the package being checked.
func atomMatches(installed *atom.Atom, constraint *atom.Atom, slot, subslot string, useFlags map[string]bool, pkgVersion *atom.Version) bool {
	if installed == nil || constraint == nil {
		return false
	}

	if installed.Category != constraint.Category || installed.Package != constraint.Package {
		return false
	}

	// version constraint check
	if constraint.Version != nil && constraint.Version.Raw != "" && pkgVersion != nil {
		cmp := pkgVersion.Compare(constraint.Version)
		switch constraint.Op {
		case atom.OpNone:
			// no operator means any version is acceptable in a dependency context
		case atom.OpEq:
			if cmp != 0 {
				return false
			}
		case atom.OpGtEq:
			if cmp < 0 {
				return false
			}
		case atom.OpGt:
			if cmp <= 0 {
				return false
			}
		case atom.OpLessEq:
			if cmp > 0 {
				return false
			}
		case atom.OpLess:
			if cmp >= 0 {
				return false
			}
		case atom.OpTilde:
			if !tildeMatch(pkgVersion, constraint.Version) {
				return false
			}
		case atom.OpEqGlob:
			if !globMatch(pkgVersion, constraint.Version) {
				return false
			}
		}
	}

	// slot constraint
	if constraint.Slot != "" && constraint.SlotOp != atom.SlotOpStar {
		if slot != constraint.Slot {
			return false
		}
	}

	// slot operator * means any slot — always satisfied
	// slot operator = means slot must match, subslot change triggers rebuild
	// (subslot equality not required for satisfaction, rebuild handled externally)

	// USE flag constraints
	for _, f := range constraint.UseFlags {
		enabled, ok := useFlags[f.Name]
		if !ok {
			// flag not present in this version
			if f.Enabled {
				return false // required flag not set
			}
			continue // disabled flag not present is OK
		}
		if enabled != f.Enabled {
			return false
		}
	}

	return true
}

// tildeMatch checks if version v satisfies the ~ operator constraint c.
// ~x.y.z matches x.y.z through x.y.* (same prefix of all numbers in c).
func tildeMatch(v, c *atom.Version) bool {
	if v == nil || c == nil || len(v.Numbers) < len(c.Numbers) {
		return false
	}
	for i := 0; i < len(c.Numbers); i++ {
		if v.Numbers[i] != c.Numbers[i] {
			return false
		}
	}
	return true
}

// globMatch checks if version v satisfies the =* operator constraint c.
// =*x.y.* matches x.y.z for any z.
func globMatch(v, c *atom.Version) bool {
	if v == nil || c == nil || len(v.Numbers) < len(c.Numbers) {
		return false
	}
	for i := 0; i < len(c.Numbers); i++ {
		if v.Numbers[i] != c.Numbers[i] {
			return false
		}
	}
	return true
}

func mustParseAtom(s string) *atom.Atom {
	a, err := atom.Parse(s)
	if err != nil {
		// best-effort: create minimal atom from the CP
		parts := strings.SplitN(s, "/", 2)
		if len(parts) == 2 {
			return &atom.Atom{Category: parts[0], Package: parts[1]}
		}
		return &atom.Atom{Package: s}
	}
	return a
}

func bestVersionAtom(base *atom.Atom, vi *VersionInfo) *atom.Atom {
	if base == nil {
		return nil
	}
	a := &atom.Atom{
		Category: base.Category,
		Package:  base.Package,
		Slot:     vi.Slot,
		Subslot:  vi.Subslot,
	}
	if vi.Version != nil {
		a.Version = vi.Version
	}
	return a
}

func useFlagsChanged(old, new map[string]bool) bool {
	if len(old) != len(new) {
		return true
	}
	for k, v := range old {
		if nv, ok := new[k]; !ok || nv != v {
			return true
		}
	}
	return false
}

func (r *resolver) getUseFlags(cp string) map[string]bool {
	flags := make(map[string]bool)
	if r.portageConfig == nil {
		return flags
	}
	for _, f := range r.portageConfig.USE {
		if strings.HasPrefix(f, "-") {
			flags[f[1:]] = false
		} else {
			flags[f] = true
		}
	}
	if pkgFlags, ok := r.portageConfig.PackageUse[cp]; ok {
		for _, f := range pkgFlags {
			if strings.HasPrefix(f, "-") {
				flags[f[1:]] = false
			} else {
				flags[f] = true
			}
		}
	}
	return flags
}

func (r *resolver) isPackageMasked(cp string) bool {
	if r.portageConfig == nil {
		return false
	}

	for _, entry := range r.portageConfig.PackageUnmask {
		a, err := atom.Parse(entry)
		if err != nil {
			continue
		}
		if a.CP() == cp {
			return false
		}
	}

	for _, entry := range r.portageConfig.PackageMask {
		a, err := atom.Parse(entry)
		if err != nil {
			continue
		}
		if a.CP() == cp {
			return true
		}
	}

	return false
}

func (r *resolver) isPackageProvided(cp string) bool {
	if r.portageConfig == nil {
		return false
	}
	for _, entry := range r.portageConfig.PackageProvided {
		a, err := atom.Parse(entry)
		if err != nil {
			continue
		}
		if a.CP() == cp {
			return true
		}
	}
	return false
}

func (r *resolver) keywordAccepted(keywords string) bool {
	if r.portageConfig == nil {
		return true
	}

	if keywords == "" {
		return true
	}

	for _, ak := range r.portageConfig.ACCEPT_KEYWORDS {
		if ak == "**" {
			return true
		}
	}

	for _, kw := range strings.Fields(keywords) {
		if strings.HasPrefix(kw, "-") {
			continue
		}
		for _, ak := range r.portageConfig.ACCEPT_KEYWORDS {
			if ak == kw {
				return true
			}
			if strings.HasPrefix(ak, "~") && ak[1:] == kw {
				return true
			}
		}
	}

	return false
}

func (r *resolver) findMatchingVersion(node *PkgNode, constraint *atom.Atom) *VersionInfo {
	var best *VersionInfo
	for _, vi := range node.Versions {
		if vi == nil {
			continue
		}
		if !atomMatches(node.Atom, constraint, vi.Slot, vi.Subslot, vi.UseFlags, vi.Version) {
			continue
		}
		if !r.keywordAccepted(vi.Keywords) {
			continue
		}
		if best == nil || (vi.Version != nil && best.Version != nil && vi.Version.Compare(best.Version) > 0) {
			best = vi
		}
	}
	return best
}

func (r *resolver) buildResult() (*ResolveResult, error) {
	install := mapToSlice(r.toInstall)
	if !r.config.UnsortedDisplay {
		install = SortByDeps(install, r.graph)
	}
	return &ResolveResult{
		Install:        install,
		Uninstall:      mapToSlice(r.toUninstall),
		Conflicts:      r.conflicts,
		BacktrackLevel: r.config.Backtrack - r.backtrackRemaining,
	}, nil
}

func mapToSlice(m map[string]*PkgAction) []PkgAction {
	result := make([]PkgAction, 0, len(m))
	for _, a := range m {
		result = append(result, *a)
	}
	return result
}

// Depclean finds packages that can be safely removed because they are not
// in the world set or the dependency tree of world packages.
func Depclean(g *DepGraph, worldSet *WorldSet) ([]PkgAction, error) {
	if g == nil {
		return nil, fmt.Errorf("depclean: nil graph")
	}

	keepers := make(map[string]bool)

	if worldSet != nil {
		for _, entry := range worldSet.Entries {
			a, err := atom.Parse(entry)
			if err != nil {
				continue
			}
			cp := a.CP()
			keepers[cp] = true
		}
	}

	queue := make([]string, 0, len(keepers))
	for cp := range keepers {
		queue = append(queue, cp)
	}

	for len(queue) > 0 {
		cp := queue[0]
		queue = queue[1:]

		node, ok := g.Packages[cp]
		if !ok {
			continue
		}

		for _, edge := range node.Deps {
			if edge.Block {
				continue
			}

			if len(edge.AnyOf) > 0 {
				for _, opt := range edge.AnyOf {
					if opt.Atom != nil {
						depCP := opt.Atom.CP()
						if !keepers[depCP] {
							keepers[depCP] = true
							queue = append(queue, depCP)
						}
					}
				}
				continue
			}

			if edge.To == nil {
				continue
			}

			depCP := edge.To.Atom.CP()
			if !keepers[depCP] {
				keepers[depCP] = true
				queue = append(queue, depCP)
			}
		}
	}

	var removals []PkgAction
	for cp, node := range g.Packages {
		if keepers[cp] {
			continue
		}
		if !node.Installed {
			continue
		}
		installed := node.GetInstalledVersion()
		if installed == nil {
			continue
		}
		removals = append(removals, PkgAction{
			Atom:    bestVersionAtom(node.Atom, installed),
			Action:  "uninstall",
			Reason:  "orphaned dependency",
			Slot:    installed.Slot,
			Subslot: installed.Subslot,
		})
	}

	return removals, nil
}

// Prune finds old versions of installed packages that can be removed,
// keeping only the newest version in each slot.
func Prune(g *DepGraph) ([]PkgAction, error) {
	if g == nil {
		return nil, fmt.Errorf("prune: nil graph")
	}

	var removals []PkgAction

	for cp, node := range g.Packages {
		slotVersions := make(map[string][]*VersionInfo)
		for _, vi := range node.Versions {
			if vi == nil || !vi.Installed {
				continue
			}
			slotVersions[vi.Slot] = append(slotVersions[vi.Slot], vi)
		}

		for slot, versions := range slotVersions {
			if len(versions) <= 1 {
				continue
			}

			newest := versions[0]
			for _, vi := range versions[1:] {
				if vi.Version != nil && newest.Version != nil && vi.Version.Compare(newest.Version) > 0 {
					newest = vi
				}
			}

			for _, vi := range versions {
				if vi == newest {
					continue
				}
				removals = append(removals, PkgAction{
					Atom:    bestVersionAtom(node.Atom, vi),
					Action:  "uninstall",
					Reason:  fmt.Sprintf("pruning old version, keeping %s-%s", cp, newest.Version.Raw),
					Slot:    slot,
					Subslot: vi.Subslot,
				})
			}
		}
	}

	return removals, nil
}

// CheckRequiredUse validates that the given USE flags satisfy a REQUIRED_USE
// constraint string. REQUIRED_USE uses the same syntax as depstrings
// (|| for any-of, use? conditionals). Returns nil if valid, or an error
// describing what failed.
func CheckRequiredUse(requiredUse string, useFlags map[string]bool) error {
	if requiredUse == "" {
		return nil
	}

	node, err := depstring.Parse(requiredUse)
	if err != nil {
		return fmt.Errorf("parse REQUIRED_USE: %w", err)
	}
	if node == nil {
		return nil
	}

	return checkRequiredUseNode(node, useFlags)
}

func checkRequiredUseNode(node depstring.DepNode, useFlags map[string]bool) error {
	switch n := node.(type) {
	case *depstring.AtomDep:
		enabled, ok := useFlags[n.Atom]
		if !ok {
			return fmt.Errorf("required USE flag %q is not set", n.Atom)
		}
		if !enabled {
			return fmt.Errorf("required USE flag %q is disabled", n.Atom)
		}
		return nil

	case *depstring.AllOfGroup:
		for _, child := range n.Children {
			if err := checkRequiredUseNode(child, useFlags); err != nil {
				return err
			}
		}
		return nil

	case *depstring.AnyOfGroup:
		for _, child := range n.Children {
			if checkRequiredUseNode(child, useFlags) == nil {
				return nil
			}
		}
		return fmt.Errorf("none of the options in any-of group are satisfied")

	case *depstring.XorOfGroup:
		satisfied := 0
		for _, child := range n.Children {
			if checkRequiredUseNode(child, useFlags) == nil {
				satisfied++
			}
		}
		if satisfied != 1 {
			return fmt.Errorf("exactly-one-of group requires exactly 1 match, got %d", satisfied)
		}
		return nil

	case *depstring.UseConditional:
		flag := n.Flag
		negate := false
		if strings.HasPrefix(flag, "!") {
			negate = true
			flag = flag[1:]
		}
		enabled, ok := useFlags[flag]
		if !ok {
			enabled = false
		}
		if negate {
			enabled = !enabled
		}
		if !enabled {
			return nil
		}
		for _, child := range n.Children {
			if err := checkRequiredUseNode(child, useFlags); err != nil {
				return err
			}
		}
		return nil

	case *depstring.Block, *depstring.WeakBlock:
		return nil

	default:
		return nil
	}
}

// LicenseAccepted checks if the given license string is accepted according to
// the ACCEPT_LICENSE configuration. Rules:
//   - * means all licenses accepted
//   - -* means reject all (unless explicitly listed)
//   - @EULA group means EULA licenses
//   - -@EULA means reject EULA licenses
//   - Individual license names are added to the accept set
func LicenseAccepted(license string, acceptLicenses []string) bool {
	if len(acceptLicenses) == 0 {
		return true
	}

	acceptAll := false
	eulaAllowed := false
	acceptSet := make(map[string]bool)
	rejectSet := make(map[string]bool)

	for _, al := range acceptLicenses {
		switch {
		case al == "*":
			acceptAll = true
		case al == "-*":
			acceptAll = false
		case al == "@EULA":
			eulaAllowed = true
		case al == "-@EULA":
			eulaAllowed = false
		case strings.HasPrefix(al, "-"):
			rejectSet[al[1:]] = true
		default:
			acceptSet[al] = true
		}
	}

	licenses := strings.Fields(license)
	for _, lic := range licenses {
		lic = strings.TrimSpace(lic)
		if lic == "" {
			continue
		}

		if rejectSet[lic] {
			return false
		}

		if acceptSet[lic] {
			return true
		}

		isEula := strings.Contains(strings.ToUpper(lic), "EULA")

		if isEula && !eulaAllowed {
			return false
		}

		if isEula && eulaAllowed {
			return true
		}
	}

	if acceptAll {
		return true
	}

	return false
}

// ---------------------------------------------------------------------------
// Resume support (--resume / --skipfirst)
// ---------------------------------------------------------------------------

// ResumePackage represents a single package entry in the resume file.
type ResumePackage struct {
	CPV       string `json:"cpv"`
	Atom      string `json:"atom"`
	Completed bool   `json:"completed"`
}

// ResumeState represents the full resume file contents.
type ResumeState struct {
	Packages []ResumePackage `json:"packages"`
}

// ResumePath is the default filesystem location for the resume file.
const ResumePath = "/var/tmp/arise/resume"

// SaveResume saves the current resolution state for --resume support.
func SaveResume(path string, result *ResolveResult) error {
	if result == nil {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("save resume: %w", err)
	}
	state := ResumeState{
		Packages: make([]ResumePackage, 0, len(result.Install)),
	}
	for _, a := range result.Install {
		cpv := a.Atom.CP()
		if a.Atom.Version != nil && a.Atom.Version.Raw != "" {
			cpv += "-" + a.Atom.Version.Raw
		} else if a.Slot != "" {
			cpv += ":" + a.Slot
		}
		state.Packages = append(state.Packages, ResumePackage{
			CPV:       cpv,
			Atom:      a.Atom.String(),
			Completed: false,
		})
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("save resume: %w", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(state); err != nil {
		return fmt.Errorf("save resume: %w", err)
	}
	return nil
}

// LoadResume loads a previous resume state.
func LoadResume(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("load resume: %w", err)
	}
	defer f.Close()
	var state ResumeState
	if err := json.NewDecoder(f).Decode(&state); err != nil {
		return nil, fmt.Errorf("load resume: %w", err)
	}
	var result []string
	for _, p := range state.Packages {
		if !p.Completed {
			result = append(result, p.Atom)
		}
	}
	return result, nil
}

// MarkResumeComplete marks a package as completed in the resume file.
func MarkResumeComplete(path string, completedAtom string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("mark resume: %w", err)
	}
	defer f.Close()
	var state ResumeState
	if err := json.NewDecoder(f).Decode(&state); err != nil {
		return fmt.Errorf("mark resume: %w", err)
	}
	for i := range state.Packages {
		if state.Packages[i].Atom == completedAtom {
			state.Packages[i].Completed = true
			break
		}
	}
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("mark resume: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("mark resume: %w", err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(state); err != nil {
		return fmt.Errorf("mark resume: %w", err)
	}
	return nil
}

// SkipFirstResume removes the first uncompleted entry from the resume file.
func SkipFirstResume(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("skip first resume: %w", err)
	}
	defer f.Close()
	var state ResumeState
	if err := json.NewDecoder(f).Decode(&state); err != nil {
		return fmt.Errorf("skip first resume: %w", err)
	}
	for i := range state.Packages {
		if !state.Packages[i].Completed {
			state.Packages[i].Completed = true
			break
		}
	}
	// Rewrite
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("skip first resume: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("skip first resume: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Changed deps detection (--changed-deps)
// ---------------------------------------------------------------------------

// depsChanged checks if the DEPEND and RDEPEND strings differ between
// the installed version and the available version.
func depsChanged(installedVer *VersionInfo, availableVer *VersionInfo) bool {
	if installedVer == nil || availableVer == nil {
		return false
	}
	if installedVer.Depend != availableVer.Depend {
		return true
	}
	if installedVer.Rdepend != availableVer.Rdepend {
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Tree display (--tree)
// ---------------------------------------------------------------------------

// FormatTree formats the dependency tree for display when --tree is set.
func FormatTree(actions []PkgAction, graph *DepGraph) string {
	if len(actions) == 0 {
		return ""
	}
	roots := findRoots(actions, graph)
	seen := make(map[string]bool)
	var buf strings.Builder
	for _, root := range roots {
		formatTreeRecurse(&buf, root, graph, 0, seen)
	}
	return buf.String()
}

func findRoots(actions []PkgAction, graph *DepGraph) []*PkgNode {
	// a root is a package in the install list that is not a dep of any other
	// package in the install list
	inPlan := make(map[string]bool)
	for _, a := range actions {
		if a.Atom != nil {
			inPlan[a.Atom.CP()] = true
		}
	}
	isDep := make(map[string]bool)
	for _, a := range actions {
		if a.Atom == nil {
			continue
		}
		node := graph.Packages[a.Atom.CP()]
		if node == nil {
			continue
		}
		for _, edge := range node.Deps {
			if edge.Block {
				continue
			}
			if edge.To != nil && inPlan[edge.To.Atom.CP()] {
				isDep[edge.To.Atom.CP()] = true
			}
			for _, opt := range edge.AnyOf {
				if opt.Atom != nil && inPlan[opt.Atom.CP()] {
					isDep[opt.Atom.CP()] = true
				}
			}
		}
	}
	var roots []*PkgNode
	for _, a := range actions {
		if a.Atom != nil && !isDep[a.Atom.CP()] {
			node := graph.Packages[a.Atom.CP()]
			if node != nil {
				roots = append(roots, node)
			}
		}
	}
	if len(roots) == 0 && len(actions) > 0 {
		// fallback: use the first action's node
		node := graph.Packages[actions[0].Atom.CP()]
		if node != nil {
			roots = append(roots, node)
		}
	}
	return roots
}

func formatTreeRecurse(buf *strings.Builder, node *PkgNode, graph *DepGraph, depth int, seen map[string]bool) {
	cp := node.Atom.CP()
	if seen[cp] {
		return
	}
	seen[cp] = true

	indent := ""
	if depth > 0 {
		indent = strings.Repeat(" ", depth*2)
	}

	actionLabel := "[ebuild   N    ]"
	installed := node.GetInstalledVersion()
	if installed != nil {
		actionLabel = "[ebuild   R    ]"
		// Check if there's an update
		best := node.GetBestVersion()
		if best != nil && installed.Version != nil && best.Version != nil &&
			best.Version.Compare(installed.Version) > 0 {
			actionLabel = "[ebuild     U  ]"
		}
	}

	versionStr := ""
	best := node.GetBestVersion()
	if best != nil && best.Version != nil {
		versionStr = "-" + best.Version.Raw
	}

	buf.WriteString(fmt.Sprintf("%s%-15s %s%s\n", indent, actionLabel, cp, versionStr))

	for _, edge := range node.Deps {
		if edge.Block {
			continue
		}
		if len(edge.AnyOf) > 0 {
			for _, opt := range edge.AnyOf {
				if opt.Atom != nil {
					depNode := graph.Packages[opt.Atom.CP()]
					if depNode != nil {
						formatTreeRecurse(buf, depNode, graph, depth+1, seen)
					}
				}
			}
			continue
		}
		if edge.To != nil {
			formatTreeRecurse(buf, edge.To, graph, depth+1, seen)
		}
	}
}

// ---------------------------------------------------------------------------
// Auto-unmask / auto-accept-license (--autounmask-write)
// ---------------------------------------------------------------------------

// AutoUnmask generates package.unmask entries for masked packages that blocked
// resolution. It writes one file per package under portageConfigRoot/package.unmask/.
func AutoUnmask(conflicts []string, portageConfigRoot string) error {
	unmaskDir := filepath.Join(portageConfigRoot, "package.unmask")
	written := make(map[string]bool)
	for _, c := range conflicts {
		if !strings.HasPrefix(c, "package masked: ") {
			continue
		}
		cp := strings.TrimPrefix(c, "package masked: ")
		cp = strings.TrimSpace(cp)
		if cp == "" || written[cp] {
			continue
		}
		written[cp] = true

		if err := os.MkdirAll(unmaskDir, 0755); err != nil {
			return fmt.Errorf("auto-unmask: %w", err)
		}
		fileName := strings.ReplaceAll(cp, "/", "_")
		path := filepath.Join(unmaskDir, fileName)
		if err := os.WriteFile(path, []byte(cp+"\n"), 0644); err != nil {
			return fmt.Errorf("auto-unmask: write %s: %w", path, err)
		}
	}
	return nil
}

// AutoAcceptLicense generates package.license entries for unaccepted licenses
// that blocked resolution. It writes one file per package under
// portageConfigRoot/package.license/.
func AutoAcceptLicense(conflicts []string, portageConfigRoot string) error {
	licenseDir := filepath.Join(portageConfigRoot, "package.license")
	written := make(map[string]bool)
	for _, c := range conflicts {
		// Conflict format: "license <LICENSE> not accepted for <cp>"
		rest, ok := strings.CutPrefix(c, "license ")
		if !ok {
			continue
		}
		idx := strings.Index(rest, " not accepted for ")
		if idx < 0 {
			continue
		}
		licenseName := rest[:idx]
		cp := rest[idx+len(" not accepted for "):]

		cp = strings.TrimSpace(cp)
		licenseName = strings.TrimSpace(licenseName)
		if cp == "" || licenseName == "" || written[cp] {
			continue
		}
		written[cp] = true

		if err := os.MkdirAll(licenseDir, 0755); err != nil {
			return fmt.Errorf("auto-accept-license: %w", err)
		}
		fileName := strings.ReplaceAll(cp, "/", "_")
		path := filepath.Join(licenseDir, fileName)
		if err := os.WriteFile(path, []byte(cp+" "+licenseName+"\n"), 0644); err != nil {
			return fmt.Errorf("auto-accept-license: write %s: %w", path, err)
		}
	}
	return nil
}
