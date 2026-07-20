// Package resolve provides the core dependency resolverfor the arise project
// manager. It implements a backtracking dependency resolution algorithm
// equivalent to emerge's --backtrack functionality.
package resolve

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/depstring"
	"github.com/airencracken/arise/internal/portage"
)

// ResolveConfig holds configuration parameters for the dependency resolver.
type ResolveConfig struct {
	Backtrack                   int      // max backtrack levels (default 10)
	Deep                        bool     // -D, consider full dependency tree
	CompleteGraph               bool     // rebuild reverse deps when packages change
	NewUse                      bool     // -N, rebuild when USE flags changed
	Update                      bool     // -u, update packages
	Oneshot                     bool     // -1, install without adding to world
	NoDeps                      bool     // skip dependency resolution
	OnlyDeps                    bool     // only install dependencies, not target
	OnlyDepsWithRdeps           string   // --onlydeps-with-rdeps (y/n, empty is default)
	OnlyDepsWithIDeps           string   // --onlydeps-with-ideps (y/n, empty is default)
	RootDeps                    string   // --root-deps=True|rdeps
	EmptyTree                   bool     // -e, rebuild entire tree as if empty
	Reinstall                   bool     // force reinstall of already-installed packages
	ChangedUse                  bool     // reinstall when USE flags changed
	ChangedDeps                 bool     // reinstall when DEPENDs changed
	DynamicDeps                 bool     // use current ebuild deps for installed packages when available
	KeepGoing                   bool     // continue on errors
	Exclude                     []string // packages to exclude
	FetchOnly                   bool     // only fetch sources
	BuildPkgOnly                bool     // build binary packages without installing
	Pretend                     bool     // -p, dry run
	Ask                         bool     // -a, prompt before proceeding
	Quiet                       bool     // -q, minimal output
	Verbose                     bool     // -v, verbose output
	Tree                        bool     // -t, display dependency tree
	Resume                      bool     // resume last operation
	SkipFirst                   bool     // skip first package in resume
	AutoUnmaskWrite             bool     // --autounmask-write, write package.unmask entries
	UnsortedDisplay             bool     // --unordered-display, don't sort results
	Jobs                        int      // -j, parallel jobs
	LoadAverage                 float64  // --load-average
	IgnoreBuiltSlotOperatorDeps string   // --ignore-built-slot-operator-deps
	WithBdeps                   string   // --with-bdeps (y/n)
	WithBdepsAuto               bool     // --with-bdeps-auto
	BinpkgRespectUse            bool     // --binpkg-respect-use
	UsePkg                      bool     // -k, use binary packages
	UsePkgOnly                  bool     // -K, only use binary packages
	BuildPkg                    bool     // -b, build binary packages
	NoReplace                   bool     // --noreplace, skip if already installed
	BinpkgDir                   string   // directory for binary packages
	BinhostURLs                 []string // remote binhost URLs
	GetBinPkg                   bool     // --getbinpkg
	GetBinPkgOnly               bool     // --getbinpkgonly
	PortageConfig               *portage.Config
	WorldSet                    *WorldSet
	SystemSet                   *WorldSet
	// InstalledByDomain supplies immutable installed-state views for cross-root
	// verification. Missing domains retain the historical single-graph view.
	// Planned actions are overlaid from the transaction graph in every domain;
	// action placement becomes explicit when the cross-root scheduler lands.
	InstalledByDomain map[DependencyDomain]*DepGraph
	// exactBacktrackBudget is set by ResolveContext for replay attempts. It
	// distinguishes an exhausted zero budget from the public zero value, which
	// retains the historical default of ten backtracks.
	exactBacktrackBudget bool
}

func (c *ResolveConfig) Defaults() {
	if c.Backtrack <= 0 {
		c.Backtrack = 10
	}
}

// ResolveResult holds the result of a dependency resolution.
type ResolveResult struct {
	Install           []PkgAction // packages to install/update
	Uninstall         []PkgAction // packages to remove (blocks)
	Conflicts         []string    // list of unresolvable conflicts
	Warnings          []string    // non-fatal state requiring user attention
	BacktrackLevel    int         // how many backtrack levels were used
	DecisionHistory   []BacktrackDecision
	BranchEvaluations []BranchEvaluation
	Metrics           ResolveMetrics
	ConflictDetails   []ConflictDetail
	Verified          bool             // final installed-state overlay passed whole-state verification
	Verification      string           // verified, failed, skipped-nodeps, or incomplete
	Incomplete        *IncompleteCause `json:"incomplete,omitempty"`
	retryChoices      []replayDecision
}

// IncompleteCause explains why resolution stopped before it could produce an
// executable, whole-state-verified plan.
type IncompleteCause struct {
	Kind           string        `json:"kind"`
	Phase          string        `json:"phase"`
	Elapsed        time.Duration `json:"elapsed"`
	DecisionsUsed  int           `json:"decisions_used"`
	BacktracksUsed int           `json:"backtracks_used"`
	Message        string        `json:"message"`
}

// BacktrackDecision records one rejected preference and the deterministic
// alternative selected for exploration. Entries commit and roll back with
// speculative resolver transactions, keeping the ledger equal to budget used.
type BacktrackDecision struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
	From string `json:"from"`
	To   string `json:"to"`
}

// BranchEvaluation is a deterministic trace row for one concurrently explored
// replay alternative. These rows are suitable for tables, timelines and graph
// visualizations without parsing human diagnostics.
type BranchEvaluation struct {
	DecisionKey   string   `json:"decision_key"`
	Option        string   `json:"option"`
	Outcome       string   `json:"outcome"`
	BacktrackUsed int      `json:"backtrack_used"`
	Conflicts     []string `json:"conflicts,omitempty"`
}

const (
	VerificationVerified      = "verified"
	VerificationFailed        = "failed"
	VerificationSkippedNoDeps = "skipped-nodeps"
	VerificationIncomplete    = "incomplete"
)

type ConflictDetail struct {
	Kind         string                `json:"kind"`
	Package      string                `json:"package"`
	Slot         string                `json:"slot,omitempty"`
	Message      string                `json:"message"`
	Requirements []ConflictRequirement `json:"requirements,omitempty"`
	Candidates   []ConflictCandidate   `json:"candidates,omitempty"`
}

type ConflictRequirement struct {
	Atom   string `json:"atom"`
	Reason string `json:"reason,omitempty"`
}

type ConflictCandidate struct {
	CPV        string   `json:"cpv"`
	State      string   `json:"state"` // installed or available
	Repository string   `json:"repository,omitempty"`
	Visible    bool     `json:"visible"`
	Visibility string   `json:"visibility,omitempty"`
	Satisfies  []string `json:"satisfies,omitempty"`
	Rejects    []string `json:"rejects,omitempty"`
}

func cloneConflictDetails(src []ConflictDetail) []ConflictDetail {
	dst := append([]ConflictDetail(nil), src...)
	for i := range dst {
		dst[i].Requirements = append([]ConflictRequirement(nil), src[i].Requirements...)
		dst[i].Candidates = append([]ConflictCandidate(nil), src[i].Candidates...)
	}
	return dst
}

type ResolveMetrics struct {
	Search, DirectUpdateRefresh, CompleteGraph, Verification, Sort time.Duration
	CompleteGraphPasses, CandidateEvaluations                      uint64
	ReplayBranches, VerifierPasses, VerifierRepairs                uint64
	UndoLogOperations, CancellationChecks                          uint64
	Allocations, AllocatedBytes                                    uint64
	CandidateCacheHits, CandidateCacheMisses                       uint64
}

// PkgAction describes a single package action (install, update, uninstall, etc.)
type PkgAction struct {
	Atom           *atom.Atom // the package
	Action         string     // "install", "update", "reinstall", "uninstall"
	Reason         string     // why (e.g. "dependency of @world", "blocked by ...")
	Slot           string     // the slot of the package to install
	Subslot        string     // the subslot
	Repository     string     // selected repository
	RepositoryPath string
	SrcURI         string
	UseFlags       map[string]bool
	MergeType      string // source or binary
	BinaryPath     string
	Unsorted       bool             // if true, exclude from topological sort
	Domain         DependencyDomain // filesystem domain receiving/removing this package
}

// PkgNode represents a package in the dependency graph.
type PkgNode struct {
	Atom      *atom.Atom
	Installed bool
	Versions  map[string]*VersionInfo   // version string -> version info
	Slots     map[string][]*VersionInfo // slot -> versions in that slot
	Deps      []*DepEdge                // dependency edges
	RevDeps   []*DepEdge                // reverse dependency edges
}

// VersionInfo holds per-version data for a package.
type VersionInfo struct {
	Package                 *PkgNode
	Version                 *atom.Version
	Slot                    string
	Subslot                 string
	UseFlags                map[string]bool
	IUse                    string
	InstalledUseFlags       map[string]bool
	InstalledIUseFlags      map[string]bool
	Installed               bool
	Available               bool   // version exists in a configured repository
	DepStr                  string // raw dependency string (combined)
	Depend                  string // DEPEND value
	Rdepend                 string // RDEPEND value
	Bdepend                 string // BDEPEND value
	Idepend                 string // IDEPEND value
	Pdepend                 string // PDEPEND value
	InstalledDepend         string
	InstalledRdepend        string
	InstalledBdepend        string
	InstalledIdepend        string
	InstalledPdepend        string
	DependencyMetadataKnown bool // empty dependency strings are authoritative, not a package-edge fallback
	InstalledEAPI           string
	Keywords                string // ebuild keywords (e.g. "amd64 ~x86")
	RequiredUse             string // REQUIRED_USE constraint
	License                 string // LICENSE value
	Repository              string // repository selected for this version
	RepositoryPriority      int
	RepositoryPath          string
	EAPIDeprecated          bool
	SrcURI                  string
	EAPI                    string
}

// DepEdge represents a dependency relationship between packages.
type DepEdge struct {
	From        *PkgNode
	To          *PkgNode
	Type        DepType
	Domain      DependencyDomain // filesystem root in which this dependency is satisfied
	EAPI        string           // selected parent EAPI governing dependency semantics
	DepAtom     *atom.Atom       // the atom as specified in the dep string
	UseCond     string           // USE flag condition (empty = always)
	Block       bool             // is this a blocker?
	StrongBlock bool             // !!atom: blocked package must be removed before merge
	AnyOf       []*DepAtom       // if non-nil, this is an any-of group
	AnyOfGroups [][]*DepAtom     // conjunction members for each alternative
	UseFlags    map[string]bool  // effective USE state of the selected parent version
}

// DependencyDomain identifies the Portage root associated with a dependency
// class. Keeping this on graph edges prevents native build/install tools from
// being conflated with target or runtime dependencies as cross-root support is
// expanded.
type DependencyDomain string

const (
	DomainBROOT   DependencyDomain = "BROOT"
	DomainSYSROOT DependencyDomain = "SYSROOT"
	DomainROOT    DependencyDomain = "ROOT"
)

// DepType classifies a dependency.
type DepType int

const (
	_              DepType = iota
	DepTypeBuild           // BDEPEND
	DepTypeRuntime         // RDEPEND
	DepTypeDepend          // DEPEND
	DepTypePost            // PDEPEND
	DepTypeInstall         // IDEPEND
)

func dependencyDomain(depType DepType) DependencyDomain {
	switch depType {
	case DepTypeBuild, DepTypeInstall:
		return DomainBROOT
	case DepTypeDepend:
		return DomainSYSROOT
	default:
		return DomainROOT
	}
}

func dependencyDomainForEAPI(depType DepType, rawEAPI string) DependencyDomain {
	if depType != DepTypeDepend {
		return dependencyDomain(depType)
	}
	eapi, err := strconv.Atoi(rawEAPI)
	if err == nil && eapi < 7 {
		// Before EAPI 7 there is no BDEPEND/SYSROOT split. DEPEND is
		// satisfied in Portage's running build root (our BROOT domain).
		return DomainBROOT
	}
	return DomainSYSROOT
}

// DepAtom holds a dependency atom and metadata.
type DepAtom struct {
	Atom        *atom.Atom
	UseCond     string
	Block       bool
	StrongBlock bool
	AnyOf       []*DepAtom
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
	return g.AddVersionFromRepository(cp, version, slot, subslot, installed, useFlags, keywords, "")
}

// AddVersionFromRepository retains repository identity for repo-qualified
// package policy and later visibility decisions.
func (g *DepGraph) AddVersionFromRepository(cp, version, slot, subslot string, installed bool, useFlags map[string]bool, keywords, repository string) *VersionInfo {
	n := g.AddPackage(cp)
	key := versionRepositoryKey(version, repository)
	if existing, ok := n.Versions[key]; ok {
		if !installed {
			existing.Available = true
		}
		if installed {
			existing.Installed = true
			existing.InstalledUseFlags = useFlags
			if repository != "" {
				existing.Repository = repository
			}
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
		Package:    n,
		Version:    ver,
		Slot:       slot,
		Subslot:    subslot,
		UseFlags:   useFlags,
		Installed:  installed,
		Available:  !installed,
		Keywords:   keywords,
		Repository: repository,
	}
	if installed {
		vi.InstalledUseFlags = useFlags
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

func versionRepositoryKey(version, repository string) string {
	if repository == "" {
		return version
	}
	return version + "\x00" + repository
}

func versionActionKey(cp string, version *VersionInfo) string {
	if version == nil || version.Version == nil {
		return cp
	}
	return cp + "-" + versionRepositoryKey(version.Version.Raw, version.Repository)
}

func domainActionKey(key string, domain DependencyDomain) string {
	if domain == "" || domain == DomainROOT {
		return key
	}
	return string(domain) + "\x00" + key
}

func normalizedActionDomain(domain DependencyDomain) DependencyDomain {
	if domain == "" {
		return DomainROOT
	}
	return domain
}

func actionVersionKey(action *PkgAction) string {
	if action == nil || action.Atom == nil || action.Atom.Version == nil {
		return ""
	}
	return domainActionKey(action.Atom.CP()+"-"+versionRepositoryKey(action.Atom.Version.Raw, action.Repository), action.Domain)
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
		Domain:  dependencyDomain(depType),
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
		From:   from,
		Type:   depType,
		Domain: dependencyDomain(depType),
		AnyOf:  options,
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
	var best *VersionInfo
	for _, vi := range n.Versions {
		if vi != nil && vi.Installed {
			if best == nil || (vi.Version != nil && (best.Version == nil || vi.Version.Compare(best.Version) > 0)) {
				best = vi
			}
		}
	}
	return best
}

// GetInstalledVersionForSlot returns the installed instance replaced by a
// selected candidate. Package-wide lookup is wrong for slotted packages: a new
// kernel/Python/compiler slot must not be classified against an unrelated,
// numerically newer installed slot.
func (n *PkgNode) GetInstalledVersionForSlot(slot string) *VersionInfo {
	var best *VersionInfo
	for _, vi := range n.Versions {
		if vi == nil || !vi.Installed || vi.Slot != slot {
			continue
		}
		if best == nil || (vi.Version != nil && (best.Version == nil || vi.Version.Compare(best.Version) > 0)) {
			best = vi
		}
	}
	return best
}

func matchingInstalledVersion(node *PkgNode, constraint *atom.Atom) *VersionInfo {
	if node == nil {
		return nil
	}
	var best *VersionInfo
	for _, vi := range node.Versions {
		if vi == nil || !vi.Installed || !versionAtomMatches(node.Atom, constraint, vi, installedFlags(vi)) {
			continue
		}
		if best == nil || (vi.Version != nil && (best.Version == nil || vi.Version.Compare(best.Version) > 0)) {
			best = vi
		}
	}
	return best
}

func (r *resolver) matchingInstalledVersionInDomain(node *PkgNode, constraint *atom.Atom, domain DependencyDomain) *VersionInfo {
	graph := r.graph
	if configured := r.config.InstalledByDomain[normalizedActionDomain(domain)]; configured != nil {
		graph = configured
	}
	if node == nil || node.Atom == nil {
		return nil
	}
	return matchingInstalledVersion(graph.Packages[node.Atom.CP()], constraint)
}

func (r *resolver) effectiveDomain(domain DependencyDomain) DependencyDomain {
	domain = normalizedActionDomain(domain)
	if domain != DomainROOT && r.config.InstalledByDomain[domain] == nil {
		return DomainROOT
	}
	return domain
}

// GetBestVersion returns the highest available version for a package.
func (n *PkgNode) GetBestVersion() *VersionInfo {
	var best *VersionInfo
	for _, vi := range n.Versions {
		if vi == nil || !vi.Available {
			continue
		}
		if betterVersionCandidate(vi, best) {
			best = vi
		}
	}
	return best
}

// GetVersion returns the version matching the given string.
func (n *PkgNode) GetVersion(vStr string) *VersionInfo {
	var best *VersionInfo
	for _, vi := range n.Versions {
		if vi == nil || vi.Version == nil || vi.Version.Raw != vStr {
			continue
		}
		if betterRepositoryCandidate(vi, best) {
			best = vi
		}
	}
	return best
}

func (n *PkgNode) GetVersionFromRepository(vStr, repository string) *VersionInfo {
	return n.Versions[versionRepositoryKey(vStr, repository)]
}

func betterRepositoryCandidate(candidate, current *VersionInfo) bool {
	if candidate == nil {
		return false
	}
	if current == nil {
		return true
	}
	if candidate.RepositoryPriority != current.RepositoryPriority {
		return candidate.RepositoryPriority > current.RepositoryPriority
	}
	return candidate.Repository < current.Repository
}

func betterVersionCandidate(candidate, current *VersionInfo) bool {
	if candidate == nil {
		return false
	}
	if current == nil || current.Version == nil {
		return true
	}
	if candidate.Version == nil {
		return false
	}
	if comparison := candidate.Version.Compare(current.Version); comparison != 0 {
		return comparison > 0
	}
	return betterRepositoryCandidate(candidate, current)
}

// FindMatchingVersion finds a version that satisfies the given constraint atom.
func (n *PkgNode) FindMatchingVersion(constraint *atom.Atom) *VersionInfo {
	// find best version that matches
	var best *VersionInfo
	for _, vi := range n.Versions {
		if vi == nil {
			continue
		}
		if !versionAtomMatches(n.Atom, constraint, vi, vi.UseFlags) {
			continue
		}
		if betterVersionCandidate(vi, best) {
			best = vi
		}
	}
	return best
}

// DefaultResolveConfig returns a ResolveConfig with sensible defaults.
func DefaultResolveConfig() ResolveConfig {
	return ResolveConfig{
		Backtrack:     10,
		Deep:          true,
		Update:        false,
		Jobs:          1,
		KeepGoing:     false,
		WithBdeps:     "auto",
		WithBdepsAuto: true,
		DynamicDeps:   true,
	}
}

// Resolve performs dependency resolution on the given graph for the specified
// targets. It implements the full backtracking algorithm equivalent to
// emerge's --backtrack functionality.
func Resolve(g *DepGraph, targets []string, config ResolveConfig) (*ResolveResult, error) {
	return ResolveContext(context.Background(), g, targets, config)
}

// ResolveContext performs dependency resolution and cooperatively stops when
// ctx is cancelled. Cancellation returns an incomplete result, not a partial
// executable plan.
func ResolveContext(ctx context.Context, g *DepGraph, targets []string, config ResolveConfig) (*ResolveResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	started := time.Now()
	limit := config.Backtrack
	if limit <= 0 {
		limit = 10
	}
	overrides := make(map[string]int)
	chargedDecisions := make(map[string]bool)
	visitedReplayStates := make(map[string]bool)
	var history []BacktrackDecision
	remaining := limit
	for {
		visitedReplayStates[replayStateKey(overrides)] = true
		attemptConfig := config
		attemptConfig.Backtrack = remaining
		attemptConfig.exactBacktrackBudget = true
		result, err := resolveAttempt(ctx, g, targets, attemptConfig, overrides, chargedDecisions, started)
		if result == nil {
			return result, err
		}
		if result.Incomplete != nil {
			return result, nil
		}
		remaining -= result.BacktrackLevel
		history = append(history, result.DecisionHistory...)
		for _, charged := range result.DecisionHistory {
			chargedDecisions[backtrackDecisionKey(charged.Kind, charged.Key, charged.From, charged.To)] = true
		}
		if err == nil && (result.Verified || len(result.Conflicts) == 0) {
			result.BacktrackLevel = limit - remaining
			result.DecisionHistory = append([]BacktrackDecision(nil), history...)
			return result, nil
		}
		if hasTerminalTargetConflict(result.Conflicts) {
			// A missing direct set member cannot be repaired by changing an any-of,
			// provider, or transitive version choice. Preserve the complete first-pass
			// diagnostics instead of replaying the whole graph until the budget expires.
			result.BacktrackLevel = limit - remaining
			result.DecisionHistory = append([]BacktrackDecision(nil), history...)
			return result, nil
		}
		if !config.CompleteGraph && terminalVerificationFailure(result) {
			// Verification has already proved the planned installed state invalid.
			// Candidate replay cannot schedule the reverse-dependency repairs that
			// are intentionally gated by --complete-graph, so return the explained,
			// non-executable result instead of exploring unrelated preferences.
			result.BacktrackLevel = limit - remaining
			result.DecisionHistory = append([]BacktrackDecision(nil), history...)
			return result, nil
		}
		if remaining <= 0 {
			result.BacktrackLevel = limit - remaining
			result.DecisionHistory = append([]BacktrackDecision(nil), history...)
			if err != nil {
				return result, err
			}
			return result, nil
		}
		decision, next, replayPrefix, nextOverrides, ok := nextUnvisitedReplayState(result.retryChoices, visitedReplayStates)
		if !ok {
			result.BacktrackLevel = limit - remaining
			result.DecisionHistory = append([]BacktrackDecision(nil), history...)
			if err != nil {
				return result, err
			}
			return result, nil
		}
		if config.Jobs > 1 {
			if speculative, chosen, used, evaluations, ok := speculateReplayAlternatives(ctx, g, targets, config, replayPrefix, chargedDecisions, decision, remaining, started); ok {
				history = append(history, BacktrackDecision{
					Kind: "conflict-rewind", Key: decision.depKey,
					From: decision.label(decision.chosen), To: decision.label(chosen),
				})
				history = append(history, speculative.DecisionHistory...)
				remaining -= used
				speculative.BacktrackLevel = limit - remaining
				speculative.DecisionHistory = append([]BacktrackDecision(nil), history...)
				speculative.BranchEvaluations = append(evaluations, speculative.BranchEvaluations...)
				return speculative, nil
			}
		}
		overrides = nextOverrides
		remaining--
		history = append(history, BacktrackDecision{
			Kind: "conflict-rewind", Key: decision.depKey,
			From: decision.label(decision.chosen), To: decision.label(next),
		})
	}
}

func terminalVerificationFailure(result *ResolveResult) bool {
	if result == nil || len(result.Conflicts) == 0 || len(result.ConflictDetails) == 0 {
		return false
	}
	for _, detail := range result.ConflictDetails {
		if detail.Kind != "post-solve-verification" {
			return false
		}
	}
	return true
}

func hasTerminalTargetConflict(conflicts []string) bool {
	for _, conflict := range conflicts {
		if strings.Contains(conflict, "(world target)") || strings.Contains(conflict, "(system target)") {
			return true
		}
	}
	return false
}

type speculativeReplayResult struct {
	option int
	result *ResolveResult
	err    error
}

func speculateReplayAlternatives(ctx context.Context, g *DepGraph, targets []string, config ResolveConfig, overrides map[string]int, chargedDecisions map[string]bool, decision replayDecision, budget int, started time.Time) (*ResolveResult, int, int, []BranchEvaluation, bool) {
	var alternatives []int
	found := false
	for _, option := range decision.order {
		if option == decision.chosen {
			found = true
			continue
		}
		if found {
			alternatives = append(alternatives, option)
		}
	}
	if len(alternatives) < 2 || budget <= 1 {
		return nil, 0, 0, nil, false
	}
	workers := config.Jobs
	if workers > len(alternatives) {
		workers = len(alternatives)
	}
	jobs := make(chan int)
	results := make(chan speculativeReplayResult, len(alternatives))
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for option := range jobs {
				branchOverrides := make(map[string]int, len(overrides)+1)
				for key, value := range overrides {
					branchOverrides[key] = value
				}
				branchOverrides[decision.depKey] = option
				branchConfig := config
				branchConfig.Backtrack = budget - 1
				result, err := resolveAttempt(ctx, g, targets, branchConfig, branchOverrides, chargedDecisions, started)
				results <- speculativeReplayResult{option: option, result: result, err: err}
			}
		}()
	}
	go func() {
		for _, option := range alternatives {
			jobs <- option
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	byOption := make(map[int]speculativeReplayResult, len(alternatives))
	for result := range results {
		byOption[result.option] = result
	}
	evaluations := make([]BranchEvaluation, 0, len(alternatives))
	for _, option := range alternatives {
		candidate := byOption[option]
		evaluation := BranchEvaluation{DecisionKey: decision.depKey, Option: decision.label(option), Outcome: "error"}
		if candidate.result != nil {
			evaluation.BacktrackUsed = candidate.result.BacktrackLevel
			evaluation.Conflicts = append([]string(nil), candidate.result.Conflicts...)
			if candidate.result.Verified && candidate.err == nil {
				evaluation.Outcome = "verified"
			} else if candidate.err == nil {
				evaluation.Outcome = "conflict"
			}
		}
		evaluations = append(evaluations, evaluation)
	}
	for index, option := range alternatives {
		candidate := byOption[option]
		used := index + 1
		if candidate.result != nil {
			used += candidate.result.BacktrackLevel
		}
		if candidate.err == nil && candidate.result != nil && candidate.result.Verified && used <= budget {
			candidate.result.Metrics.ReplayBranches += uint64(len(alternatives))
			return candidate.result, option, used, evaluations, true
		}
	}
	return nil, 0, 0, evaluations, false
}

func resolveAttempt(ctx context.Context, g *DepGraph, targets []string, config ResolveConfig, choiceOverrides map[string]int, chargedDecisions map[string]bool, started time.Time) (*ResolveResult, error) {
	if g == nil {
		return nil, fmt.Errorf("resolve: no dependency graph provided (internal error)")
	}
	if config.Backtrack <= 0 && !config.exactBacktrackBudget {
		config.Backtrack = 10
	}

	r := &resolver{
		ctx:                   ctx,
		started:               started,
		phase:                 "target-expansion",
		graph:                 g,
		config:                config,
		installed:             make(map[string]*PkgAction),
		toInstall:             make(map[string]*PkgAction),
		actionOwners:          make(map[string]map[string]bool),
		rootActionKeys:        make(map[string]bool),
		toUninstall:           make(map[string]*PkgAction),
		conflicts:             []string{},
		seenDeps:              make(map[string]bool),
		activeDeps:            make(map[string]int),
		cycleSeen:             make(map[string]bool),
		selectedCPs:           make(map[string]bool),
		explicitTargets:       make(map[string]bool),
		onlyDepsTargets:       make(map[string]bool),
		constraints:           make(map[string][]*atom.Atom),
		constraintCauses:      make(map[string][]ConflictRequirement),
		useOverrides:          make(map[string]map[string]bool),
		useChangeSeen:         make(map[string]bool),
		baseUseByVersion:      make(map[*VersionInfo]map[string]bool),
		effectiveNodeUseCache: make(map[string]map[string]bool),
		maskCache:             make(map[string]portage.MaskStatus),
		keywordCache:          make(map[string]bool),
		candidateCache:        make(map[string]candidateCacheEntry),
		portageConfig:         config.PortageConfig,
		choiceOverrides:       choiceOverrides,
		chargedDecisions:      chargedDecisions,
		worldSet:              config.WorldSet,
		systemSet:             config.SystemSet,
	}
	var allocationStart runtime.MemStats
	runtime.ReadMemStats(&allocationStart)
	r.allocationStartCount, r.allocationStartBytes = allocationStart.Mallocs, allocationStart.TotalAlloc
	for _, target := range targets {
		if target == "@world" || target == "@system" {
			r.setScoped = true
		}
	}

	r.backtrackRemaining = config.Backtrack
	if err := r.checkContext(); err != nil {
		return r.incompleteResult(err), nil
	}

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
		return nil, fmt.Errorf("resolve: failed to determine which packages to install: %w", err)
	}
	r.targetAtoms = targetAtoms
	if config.OnlyDeps {
		for _, target := range targetAtoms {
			r.onlyDepsTargets[target.CP()] = true
		}
	}

	if len(targetAtoms) == 0 {
		return r.buildResult()
	}

	if config.NoDeps {
		for _, target := range targetAtoms {
			cp := target.CP()
			node := g.Packages[cp]
			if node == nil && !config.KeepGoing {
				return nil, fmt.Errorf("resolve: package %s is not available in the repository", cp)
			}
			if node != nil {
				vi := r.findMatchingVersion(node, target)
				if vi == nil {
					if !config.KeepGoing {
						return nil, fmt.Errorf("resolve: no installable version found for %s", target)
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
				mergeType, binaryPath, selectErr := r.selectMergeType(node, vi)
				if selectErr != nil {
					if !config.KeepGoing {
						return nil, fmt.Errorf("resolve: %w", selectErr)
					}
					r.conflicts = append(r.conflicts, selectErr.Error())
					continue
				}
				r.setInstall(versionActionKey(cp, vi), &PkgAction{
					Atom:   bestVersionAtom(node.Atom, vi),
					Action: action,
					Reason: reason,
					Slot:   vi.Slot, Subslot: vi.Subslot, Repository: vi.Repository,
					MergeType: mergeType, BinaryPath: binaryPath,
				})
			}
		}
		return r.buildResult()
	}

	targetCPs := make(map[string]bool)
	for _, t := range targetAtoms {
		targetCPs[t.CP()] = true
	}

	// step 2-6: build the install plan with backtracking
	phaseStarted := time.Now()
	r.phase = "candidate-search"
	err = r.resolveTargets(targetAtoms)
	r.metrics.Search = time.Since(phaseStarted)
	if err != nil {
		if ctx.Err() != nil {
			return r.incompleteResult(ctx.Err()), nil
		}
		if config.KeepGoing {
			return r.buildResult()
		}
		result, _ := r.buildResult()
		return result, fmt.Errorf("resolve: dependency resolution failed: %w", err)
	}
	if !config.EmptyTree && config.Deep && (config.Update || config.NewUse || config.ChangedUse || config.ChangedDeps) {
		r.phase = "direct-update-refresh"
		phaseStarted = time.Now()
		if err := r.refreshCommittedDirectUpdates(); err != nil {
			r.metrics.DirectUpdateRefresh = time.Since(phaseStarted)
			if ctx.Err() != nil {
				return r.incompleteResult(ctx.Err()), nil
			}
			if !config.KeepGoing {
				return nil, fmt.Errorf("resolve: refresh committed dependency updates: %w", err)
			}
			r.conflicts = append(r.conflicts, err.Error())
		}
		r.metrics.DirectUpdateRefresh = time.Since(phaseStarted)
	}
	if config.OnlyDeps {
		for key, action := range r.toInstall {
			if action != nil && action.Atom != nil && targetCPs[action.Atom.CP()] {
				r.deleteInstall(key)
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
				r.deleteInstall(a)
			}
		}
	}

	// step 5: CompleteGraph — rebuild reverse deps when packages change
	if config.CompleteGraph {
		r.phase = "complete-graph"
		phaseStarted = time.Now()
		r.processCompleteGraph()
		r.metrics.CompleteGraph = time.Since(phaseStarted)
		if ctx.Err() != nil {
			return r.incompleteResult(ctx.Err()), nil
		}
	}

	// A candidate search is not a proof that the resulting installed state is
	// coherent. Validate the overlaid transaction before it can be executed.
	phaseStarted = time.Now()
	r.phase = "verification"
	r.verifyPlannedState()
	r.metrics.Verification = time.Since(phaseStarted)
	if ctx.Err() != nil {
		return r.incompleteResult(ctx.Err()), nil
	}

	// step 6: topologically sort install actions
	phaseStarted = time.Now()
	r.phase = "plan-ordering"
	install := r.sortPlannedActions(mapToSlice(r.toInstall))
	r.validatePlanOrder(install)
	r.metrics.Sort = time.Since(phaseStarted)
	if ctx.Err() != nil {
		return r.incompleteResult(ctx.Err()), nil
	}
	r.snapshotAllocations()

	return &ResolveResult{
		Install:         install,
		Uninstall:       mapToSlice(r.toUninstall),
		Conflicts:       r.conflicts,
		Warnings:        r.warnings,
		BacktrackLevel:  config.Backtrack - r.backtrackRemaining,
		DecisionHistory: append([]BacktrackDecision(nil), r.decisionHistory...),
		Metrics:         r.metrics,
		ConflictDetails: r.conflictDetails,
		Verified:        len(r.conflicts) == 0,
		Verification: func() string {
			if len(r.conflicts) == 0 {
				return VerificationVerified
			}
			return VerificationFailed
		}(),
		retryChoices: append([]replayDecision(nil), r.replayChoices...),
	}, nil
}

func (r *resolver) checkContext() error {
	r.metrics.CancellationChecks++
	if r.ctx == nil {
		return nil
	}
	return r.ctx.Err()
}

func (r *resolver) snapshotAllocations() {
	if r.allocationStartCount == 0 && r.allocationStartBytes == 0 {
		return
	}
	var current runtime.MemStats
	runtime.ReadMemStats(&current)
	if current.Mallocs >= r.allocationStartCount {
		r.metrics.Allocations = current.Mallocs - r.allocationStartCount
	}
	if current.TotalAlloc >= r.allocationStartBytes {
		r.metrics.AllocatedBytes = current.TotalAlloc - r.allocationStartBytes
	}
}

func (r *resolver) incompleteResult(cause error) *ResolveResult {
	r.snapshotAllocations()
	backtracks := r.config.Backtrack - r.backtrackRemaining
	if backtracks < 0 {
		backtracks = 0
	}
	kind := "cancelled"
	if cause == context.DeadlineExceeded {
		kind = "timeout"
	}
	return &ResolveResult{
		Conflicts:       []string{fmt.Sprintf("resolver %s during %s: %v", kind, r.phase, cause)},
		Warnings:        append([]string(nil), r.warnings...),
		BacktrackLevel:  backtracks,
		DecisionHistory: append([]BacktrackDecision(nil), r.decisionHistory...),
		Metrics:         r.metrics,
		ConflictDetails: append([]ConflictDetail(nil), r.conflictDetails...),
		Verified:        false,
		Verification:    VerificationIncomplete,
		Incomplete: &IncompleteCause{Kind: kind, Phase: r.phase, Elapsed: time.Since(r.started),
			DecisionsUsed: len(r.decisionHistory), BacktracksUsed: backtracks, Message: cause.Error()},
	}
}

// VerifyTransaction validates an already constructed install/removal overlay.
// Removal-oriented commands use this entry point so they receive the same
// whole-state dependency, blocker, slot, USE, provider, and root-domain checks
// as ordinary resolution before a transaction can become executable.
func VerifyTransaction(g *DepGraph, installs, removals []PkgAction, config ResolveConfig) (*ResolveResult, error) {
	if g == nil {
		return nil, fmt.Errorf("resolve: no dependency graph provided (internal error)")
	}
	r := &resolver{
		graph:            g,
		config:           config,
		installed:        make(map[string]*PkgAction),
		toInstall:        make(map[string]*PkgAction),
		actionOwners:     make(map[string]map[string]bool),
		rootActionKeys:   make(map[string]bool),
		toUninstall:      make(map[string]*PkgAction),
		seenDeps:         make(map[string]bool),
		activeDeps:       make(map[string]int),
		cycleSeen:        make(map[string]bool),
		selectedCPs:      make(map[string]bool),
		explicitTargets:  make(map[string]bool),
		constraints:      make(map[string][]*atom.Atom),
		constraintCauses: make(map[string][]ConflictRequirement),
		useOverrides:     make(map[string]map[string]bool),
		useChangeSeen:    make(map[string]bool),
		maskCache:        make(map[string]portage.MaskStatus),
		keywordCache:     make(map[string]bool),
		portageConfig:    config.PortageConfig,
		worldSet:         config.WorldSet,
		systemSet:        config.SystemSet,
		strictWholeState: true,
	}
	for i := range installs {
		action := installs[i]
		if action.Atom == nil {
			return nil, fmt.Errorf("resolve: install action %d has no atom", i)
		}
		r.toInstall[actionVersionKey(&action)] = &action
	}
	for i := range removals {
		if removals[i].Atom == nil {
			return nil, fmt.Errorf("resolve: removal action %d has no atom", i)
		}
		if err := completeRemovalIdentity(g, &removals[i]); err != nil {
			return nil, fmt.Errorf("resolve: removal action %d: %w", i, err)
		}
		r.toUninstall[actionVersionKey(&removals[i])] = &removals[i]
	}
	started := time.Now()
	r.verifyPlannedState()
	r.metrics.Verification = time.Since(started)
	return &ResolveResult{
		Install: installs, Uninstall: removals,
		Conflicts: r.conflicts, Warnings: r.warnings,
		ConflictDetails: r.conflictDetails, Metrics: r.metrics,
		Verified: len(r.conflicts) == 0,
		Verification: func() string {
			if len(r.conflicts) == 0 {
				return VerificationVerified
			}
			return VerificationFailed
		}(),
	}, nil
}

func completeRemovalIdentity(g *DepGraph, action *PkgAction) error {
	if g == nil || action == nil || action.Atom == nil || action.Atom.Version == nil {
		return fmt.Errorf("exact installed package identity is required")
	}
	node := g.Packages[action.Atom.CP()]
	if node == nil {
		return fmt.Errorf("package %s is not installed", action.Atom.CP())
	}
	var matches []*VersionInfo
	for _, vi := range node.Versions {
		if vi == nil || !vi.Installed || vi.Version == nil || vi.Version.Raw != action.Atom.Version.Raw {
			continue
		}
		if action.Repository != "" && vi.Repository != action.Repository {
			continue
		}
		if action.Slot != "" && vi.Slot != action.Slot {
			continue
		}
		matches = append(matches, vi)
	}
	if len(matches) != 1 {
		return fmt.Errorf("exact removal %s matched %d installed repository/slot identities", action.Atom, len(matches))
	}
	action.Repository = matches[0].Repository
	action.Slot = matches[0].Slot
	action.Subslot = matches[0].Subslot
	return nil
}

type resolver struct {
	ctx                   context.Context
	started               time.Time
	phase                 string
	allocationStartCount  uint64
	allocationStartBytes  uint64
	graph                 *DepGraph
	config                ResolveConfig
	worldSet              *WorldSet
	systemSet             *WorldSet
	targetAtoms           []*atom.Atom
	installed             map[string]*PkgAction      // CPV -> action
	toInstall             map[string]*PkgAction      // CPV -> action
	toUninstall           map[string]*PkgAction      // CPV -> action
	actionOwners          map[string]map[string]bool // action key -> parent version dependency keys
	rootActionKeys        map[string]bool            // exact actions selected at target depth
	conflicts             []string
	warnings              []string
	seenDeps              map[string]bool
	activeDeps            map[string]int
	dependencyPath        []string
	cycleSeen             map[string]bool
	selectedCPs           map[string]bool // final target/dependency closure
	explicitTargets       map[string]bool // atoms named directly, excluding expanded sets
	onlyDepsTargets       map[string]bool // argument packages receiving --onlydeps policy
	backtrackRemaining    int
	decisionHistory       []BacktrackDecision
	choiceOverrides       map[string]int
	chargedDecisions      map[string]bool
	replayChoices         []replayDecision
	constraints           map[string][]*atom.Atom // accumulated requirements by CP|slot
	constraintCauses      map[string][]ConflictRequirement
	conflictDetails       []ConflictDetail
	useOverrides          map[string]map[string]bool
	useChangeSeen         map[string]bool
	baseUseByVersion      map[*VersionInfo]map[string]bool
	baseUseCache          map[string]map[string]bool // retained for fixture/source compatibility; superseded by baseUseByVersion
	versionKeyCache       map[*VersionInfo]string
	effectiveNodeUseCache map[string]map[string]bool
	maskCache             map[string]portage.MaskStatus
	keywordCache          map[string]bool
	candidateCache        map[string]candidateCacheEntry
	useOverrideGeneration uint64
	pendingConstraint     *atom.Atom // unpinned dependency behind an internally pinned candidate
	pendingReason         string
	pendingDomain         DependencyDomain
	portageConfig         *portage.Config
	metrics               ResolveMetrics
	transactions          []*resolverTransaction
	setScoped             bool // @world/@system excludes unrelated installed orphans
	strictWholeState      bool // explicit transaction verification cannot downgrade breakage to depclean advice
}

func (r *resolver) consumeBacktrack(kind, key, from, to string) error {
	decisionKey := backtrackDecisionKey(kind, key, from, to)
	if r.chargedDecisions[decisionKey] {
		return nil
	}
	if r.backtrackRemaining <= 0 {
		return fmt.Errorf("backtrack limit exhausted while revising %s from %s to %s", key, from, to)
	}
	r.backtrackRemaining--
	r.decisionHistory = append(r.decisionHistory, BacktrackDecision{Kind: kind, Key: key, From: from, To: to})
	return nil
}

func backtrackDecisionKey(kind, key, from, to string) string {
	return kind + "\x00" + key + "\x00" + from + "\x00" + to
}

type replayDecision struct {
	kind   string
	depKey string
	chosen int
	order  []int
	labels map[int]string
}

func (d replayDecision) label(option int) string {
	if label := d.labels[option]; label != "" {
		return label
	}
	return fmt.Sprintf("alternative %d", option+1)
}

func nextReplayChoice(decisions []replayDecision) (replayDecision, int, bool) {
	for i := len(decisions) - 1; i >= 0; i-- {
		decision := decisions[i]
		for position, option := range decision.order {
			if option == decision.chosen && position+1 < len(decision.order) {
				return decision, decision.order[position+1], true
			}
		}
	}
	return replayDecision{}, 0, false
}

// nextUnvisitedReplayState advances the deepest decision that has an
// unexplored alternative. Overrides below that decision are discarded: they
// belong to the branch being rewound and must not leak into its sibling.
func nextUnvisitedReplayState(decisions []replayDecision, visited map[string]bool) (replayDecision, int, map[string]int, map[string]int, bool) {
	for i := len(decisions) - 1; i >= 0; i-- {
		decision := decisions[i]
		position := slices.Index(decision.order, decision.chosen)
		if position < 0 {
			continue
		}
		prefix := make(map[string]int, i)
		for _, ancestor := range decisions[:i] {
			prefix[ancestor.depKey] = ancestor.chosen
		}
		for _, option := range decision.order[position+1:] {
			candidate := cloneChoiceOverrides(prefix)
			candidate[decision.depKey] = option
			if !visited[replayStateKey(candidate)] {
				return decision, option, prefix, candidate, true
			}
		}
	}
	return replayDecision{}, 0, nil, nil, false
}

func cloneChoiceOverrides(src map[string]int) map[string]int {
	dst := make(map[string]int, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func replayStateKey(overrides map[string]int) string {
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var state strings.Builder
	for _, key := range keys {
		state.WriteString(strconv.Itoa(len(key)))
		state.WriteByte(':')
		state.WriteString(key)
		state.WriteByte('=')
		state.WriteString(strconv.Itoa(overrides[key]))
		state.WriteByte(';')
	}
	return state.String()
}

func (r *resolver) recordReplayChoice(decision replayDecision) {
	for index := len(r.replayChoices) - 1; index >= 0; index-- {
		existing := r.replayChoices[index]
		if existing.depKey == decision.depKey && existing.chosen == decision.chosen {
			return
		}
	}
	r.replayChoices = append(r.replayChoices, decision)
}

type actionUndo struct {
	value  *PkgAction
	exists bool
}
type boolUndo struct {
	value  bool
	exists bool
}
type constraintsUndo struct {
	value  []*atom.Atom
	exists bool
}
type constraintCausesUndo struct {
	value  []ConflictRequirement
	exists bool
}
type useOverrideUndo struct {
	value  map[string]bool
	exists bool
}
type stringSetUndo struct {
	value  map[string]bool
	exists bool
}

// resolverTransaction records only keys changed by a speculative branch.
// Nested transactions log mutations in every active parent, so committing an
// inner branch still leaves the outer branch fully reversible.
type resolverTransaction struct {
	install            map[string]actionUndo
	uninstall          map[string]actionUndo
	seenDeps           map[string]boolUndo
	selectedCPs        map[string]boolUndo
	constraints        map[string]constraintsUndo
	constraintCauses   map[string]constraintCausesUndo
	useOverrides       map[string]useOverrideUndo
	useChangeSeen      map[string]boolUndo
	actionOwners       map[string]stringSetUndo
	conflictsLen       int
	warningsLen        int
	replayChoicesLen   int
	pendingConstraint  *atom.Atom
	pendingReason      string
	conflictDetailsLen int
	backtrackRemaining int
	decisionHistoryLen int
}

func cloneBoolMap(src map[string]bool) map[string]bool {
	dst := make(map[string]bool, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func (r *resolver) beginTransaction() *resolverTransaction {
	tx := &resolverTransaction{
		install: make(map[string]actionUndo), uninstall: make(map[string]actionUndo),
		seenDeps: make(map[string]boolUndo), selectedCPs: make(map[string]boolUndo), constraints: make(map[string]constraintsUndo),
		constraintCauses: make(map[string]constraintCausesUndo),
		useOverrides:     make(map[string]useOverrideUndo), useChangeSeen: make(map[string]boolUndo),
		actionOwners: make(map[string]stringSetUndo),
		conflictsLen: len(r.conflicts), warningsLen: len(r.warnings), replayChoicesLen: len(r.replayChoices),
		pendingConstraint: r.pendingConstraint, pendingReason: r.pendingReason,
		conflictDetailsLen: len(r.conflictDetails), backtrackRemaining: r.backtrackRemaining,
		decisionHistoryLen: len(r.decisionHistory),
	}
	r.transactions = append(r.transactions, tx)
	return tx
}

func (r *resolver) commitTransaction(tx *resolverTransaction) {
	if len(r.transactions) == 0 || r.transactions[len(r.transactions)-1] != tx {
		panic("resolve: transaction commit out of order")
	}
	r.transactions = r.transactions[:len(r.transactions)-1]
}

func (r *resolver) rollbackTransaction(tx *resolverTransaction) {
	if len(r.transactions) == 0 || r.transactions[len(r.transactions)-1] != tx {
		panic("resolve: transaction rollback out of order")
	}
	r.transactions = r.transactions[:len(r.transactions)-1]
	r.metrics.UndoLogOperations += uint64(len(tx.install) + len(tx.uninstall) + len(tx.seenDeps) + len(tx.selectedCPs) + len(tx.constraints) + len(tx.constraintCauses) + len(tx.useOverrides) + len(tx.useChangeSeen) + len(tx.actionOwners))
	for key, undo := range tx.install {
		if undo.exists {
			r.toInstall[key] = undo.value
		} else {
			delete(r.toInstall, key)
		}
	}
	for key, undo := range tx.uninstall {
		if undo.exists {
			r.toUninstall[key] = undo.value
		} else {
			delete(r.toUninstall, key)
		}
	}
	for key, undo := range tx.seenDeps {
		if undo.exists {
			r.seenDeps[key] = undo.value
		} else {
			delete(r.seenDeps, key)
		}
	}
	for key, undo := range tx.selectedCPs {
		if undo.exists {
			r.selectedCPs[key] = undo.value
		} else {
			delete(r.selectedCPs, key)
		}
	}
	for key, undo := range tx.constraints {
		if undo.exists {
			r.constraints[key] = undo.value
		} else {
			delete(r.constraints, key)
		}
	}
	for key, undo := range tx.constraintCauses {
		if undo.exists {
			r.constraintCauses[key] = undo.value
		} else {
			delete(r.constraintCauses, key)
		}
	}
	for key, undo := range tx.useOverrides {
		if undo.exists {
			r.useOverrides[key] = undo.value
		} else {
			delete(r.useOverrides, key)
		}
	}
	if len(tx.useOverrides) != 0 {
		r.useOverrideGeneration++
	}
	for key, undo := range tx.useChangeSeen {
		if undo.exists {
			r.useChangeSeen[key] = undo.value
		} else {
			delete(r.useChangeSeen, key)
		}
	}
	for key, undo := range tx.actionOwners {
		if undo.exists {
			r.actionOwners[key] = undo.value
		} else {
			delete(r.actionOwners, key)
		}
	}
	r.conflicts = r.conflicts[:tx.conflictsLen]
	r.warnings = r.warnings[:tx.warningsLen]
	r.replayChoices = r.replayChoices[:tx.replayChoicesLen]
	r.pendingConstraint = tx.pendingConstraint
	r.pendingReason = tx.pendingReason
	r.conflictDetails = r.conflictDetails[:tx.conflictDetailsLen]
	r.backtrackRemaining = tx.backtrackRemaining
	r.decisionHistory = r.decisionHistory[:tx.decisionHistoryLen]
}

func (r *resolver) setInstall(key string, value *PkgAction) {
	for _, tx := range r.transactions {
		if _, logged := tx.install[key]; !logged {
			old, exists := r.toInstall[key]
			tx.install[key] = actionUndo{value: old, exists: exists}
		}
	}
	r.toInstall[key] = value
	if len(r.dependencyPath) > 0 {
		r.addActionOwner(key, r.dependencyPath[len(r.dependencyPath)-1])
	}
}

func (r *resolver) deleteInstall(key string) {
	for _, tx := range r.transactions {
		if _, logged := tx.install[key]; !logged {
			old, exists := r.toInstall[key]
			tx.install[key] = actionUndo{value: old, exists: exists}
		}
	}
	delete(r.toInstall, key)
	r.setActionOwners(key, nil)
}

func (r *resolver) setActionOwners(key string, owners map[string]bool) {
	if r.actionOwners == nil {
		r.actionOwners = make(map[string]map[string]bool)
	}
	for _, tx := range r.transactions {
		if _, logged := tx.actionOwners[key]; !logged {
			old, exists := r.actionOwners[key]
			tx.actionOwners[key] = stringSetUndo{value: cloneBoolMap(old), exists: exists}
		}
	}
	if len(owners) == 0 {
		delete(r.actionOwners, key)
		return
	}
	r.actionOwners[key] = owners
}

func (r *resolver) addActionOwner(key, owner string) {
	owners := cloneBoolMap(r.actionOwners[key])
	owners[owner] = true
	r.setActionOwners(key, owners)
}

func (r *resolver) setUninstall(key string, value *PkgAction) {
	for _, tx := range r.transactions {
		if _, logged := tx.uninstall[key]; !logged {
			old, exists := r.toUninstall[key]
			tx.uninstall[key] = actionUndo{value: old, exists: exists}
		}
	}
	r.toUninstall[key] = value
}

func (r *resolver) setSeenDep(key string, value bool) {
	for _, tx := range r.transactions {
		if _, logged := tx.seenDeps[key]; !logged {
			old, exists := r.seenDeps[key]
			tx.seenDeps[key] = boolUndo{value: old, exists: exists}
		}
	}
	r.seenDeps[key] = value
}

func (r *resolver) setSelectedCP(key string) {
	for _, tx := range r.transactions {
		if _, logged := tx.selectedCPs[key]; !logged {
			old, exists := r.selectedCPs[key]
			tx.selectedCPs[key] = boolUndo{value: old, exists: exists}
		}
	}
	r.selectedCPs[key] = true
}

func (r *resolver) deleteSeenDep(key string) {
	for _, tx := range r.transactions {
		if _, logged := tx.seenDeps[key]; !logged {
			old, exists := r.seenDeps[key]
			tx.seenDeps[key] = boolUndo{value: old, exists: exists}
		}
	}
	delete(r.seenDeps, key)
}

func (r *resolver) setConstraints(key string, value []*atom.Atom) {
	for _, tx := range r.transactions {
		if _, logged := tx.constraints[key]; !logged {
			old, exists := r.constraints[key]
			tx.constraints[key] = constraintsUndo{value: append([]*atom.Atom(nil), old...), exists: exists}
		}
	}
	r.constraints[key] = value
}

func (r *resolver) setConstraintCauses(key string, value []ConflictRequirement) {
	for _, tx := range r.transactions {
		if _, logged := tx.constraintCauses[key]; !logged {
			old, exists := r.constraintCauses[key]
			tx.constraintCauses[key] = constraintCausesUndo{value: append([]ConflictRequirement(nil), old...), exists: exists}
		}
	}
	r.constraintCauses[key] = value
}

func (r *resolver) setUseOverride(key, flag string, value bool) {
	for _, tx := range r.transactions {
		if _, logged := tx.useOverrides[key]; !logged {
			old, exists := r.useOverrides[key]
			tx.useOverrides[key] = useOverrideUndo{value: cloneBoolMap(old), exists: exists}
		}
	}
	if r.useOverrides[key] == nil {
		r.useOverrides[key] = make(map[string]bool)
	}
	r.useOverrides[key][flag] = value
	r.useOverrideGeneration++
}

func (r *resolver) setUseChangeSeen(key string, value bool) {
	for _, tx := range r.transactions {
		if _, logged := tx.useChangeSeen[key]; !logged {
			old, exists := r.useChangeSeen[key]
			tx.useChangeSeen[key] = boolUndo{value: old, exists: exists}
		}
	}
	r.useChangeSeen[key] = value
}

func (r *resolver) expandTargets(targets []string) ([]*atom.Atom, error) {
	var atoms []*atom.Atom
	appendSet := func(set *WorldSet, label string) error {
		if set == nil {
			return nil
		}
		for _, entry := range set.Entries {
			a, err := atom.ParsePackageAtom(entry)
			if err != nil {
				if r.config.KeepGoing {
					r.conflicts = append(r.conflicts, fmt.Sprintf("bad %s entry %q: %v", label, entry, err))
					continue
				}
				return fmt.Errorf("resolve: could not parse %s entry %q: %w", label, entry, err)
			}
			atoms = append(atoms, a)
		}
		return nil
	}

	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}

		// handle @world expansion
		if target == "@world" {
			// Portage's @world is the union of the selected profile's @system
			// set and the user world file.
			if err := appendSet(r.systemSet, "system"); err != nil {
				return nil, err
			}
			if err := appendSet(r.worldSet, "world"); err != nil {
				return nil, err
			}
			continue
		}
		if target == "@system" {
			if err := appendSet(r.systemSet, "system"); err != nil {
				return nil, err
			}
			continue
		}

		// parse atom
		a, err := atom.ParsePackageAtom(target)
		if err != nil {
			if !strings.Contains(target, "/") {
				matches := r.findPackagesByName(target)
				switch len(matches) {
				case 0:
					// no match, proceed with original error
				case 1:
					a, err = atom.Parse(matches[0])
					if err != nil {
						if r.config.KeepGoing {
							r.conflicts = append(r.conflicts, fmt.Sprintf("bad target %q: %v", target, err))
							continue
						}
						return nil, fmt.Errorf("parse target %q: %w", target, err)
					}
					atoms = append(atoms, a)
					r.explicitTargets[a.CP()] = true
					continue
				default:
					names := strings.Join(matches, ", ")
					msg := fmt.Sprintf("ambiguous package name %q matches %d packages: [%s]", target, len(matches), names)
					r.conflicts = append(r.conflicts, msg)
					if r.config.KeepGoing {
						continue
					}
					return nil, fmt.Errorf("resolve: %s", msg)
				}
			}
			if r.config.KeepGoing {
				r.conflicts = append(r.conflicts, fmt.Sprintf("bad target %q: %v", target, err))
				continue
			}
			return nil, fmt.Errorf("resolve: could not parse package specification %q: %w", target, err)
		}
		atoms = append(atoms, a)
		r.explicitTargets[a.CP()] = true
	}

	return atoms, nil
}

func (r *resolver) findPackagesByName(name string) []string {
	var matches []string
	for cp := range r.graph.Packages {
		parts := strings.SplitN(cp, "/", 2)
		if len(parts) == 2 && parts[1] == name {
			matches = append(matches, cp)
		}
	}
	sort.Strings(matches)
	return matches
}

// refreshCommittedDirectUpdates closes the narrow gap where an optional child
// update is rolled back with a speculative alternative even though its exact
// installed parent version remains in the committed traversal. It is a single
// pass over transactional seenDeps, not a graph-wide or fixed-point rescan.
func (r *resolver) refreshCommittedDirectUpdates() error {
	type selectedVersion struct {
		node *PkgNode
		vi   *VersionInfo
	}
	versions := make(map[string]selectedVersion)
	for _, node := range r.graph.Packages {
		if node == nil || node.Atom == nil {
			continue
		}
		for _, vi := range node.Versions {
			if vi != nil {
				versions[dependencyVersionKey(node.Atom.CP(), vi.Version, vi.Slot, vi.Repository)] = selectedVersion{node: node, vi: vi}
			}
		}
	}
	keys := make([]string, 0, len(r.seenDeps))
	for key, seen := range r.seenDeps {
		if seen {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := r.checkContext(); err != nil {
			return err
		}
		entry := versions[key]
		if entry.vi == nil {
			continue
		}
		selected := entry.vi
		if selected.Installed {
			if replacement, _ := r.scheduledReplacement(entry.node, selected, DomainROOT); replacement != nil {
				selected = replacement
			}
		}
		edges, err := r.dependenciesForVersion(entry.node, selected)
		if err != nil {
			return err
		}
		for _, edge := range edges {
			if len(edge.AnyOf) > 0 || edge.Block {
				continue
			}
			if err := r.processEdge(edge, 1); err != nil {
				return err
			}
		}
	}
	return nil
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
	if err := r.checkContext(); err != nil {
		return err
	}
	if depth > 100 {
		return fmt.Errorf("resolve: dependency chain is too deep for %s — there may be a circular dependency", target.CP())
	}

	cp := target.CP()

	node := r.graph.Packages[cp]
	resolvedFromProvider := false
	if node == nil {
		// Check for virtual providers
		if providers, ok := r.graph.ProvidersOf[cp]; ok {
			for _, providerCP := range providers {
				node = r.graph.Packages[providerCP]
				if node != nil {
					resolvedFromProvider = true
					break
				}
			}
		}
	}
	if node == nil {
		msg := fmt.Sprintf("package %s could not be found in the repository", cp)
		r.conflicts = append(r.conflicts, msg)
		if r.config.KeepGoing {
			return nil
		}
		return fmt.Errorf("%s", msg)
	}
	r.setSelectedCP(node.Atom.CP())

	// find best version matching the target constraint
	var vi *VersionInfo
	matchTarget := target
	if resolvedFromProvider {
		matchTarget = node.Atom
	}
	vi = r.findMatchingVersion(node, matchTarget)
	if vi == nil {
		installed := matchingInstalledVersion(node, matchTarget)
		if installed != nil && versionAtomMatches(node.Atom, matchTarget, installed, installedFlags(installed)) {
			masked := r.matchingMaskStatuses(node, matchTarget)
			if len(masked) > 0 {
				r.warnings = append(r.warnings, fmt.Sprintf("installed package %s-%s is masked (%s)", cp, installed.Version.Raw, strings.Join(masked, "; ")))
				if r.config.Deep {
					return r.processDeps(node, installed, target.String(), depth+1, DomainROOT)
				}
				return nil
			}
			if !r.explicitTargets[cp] {
				r.warnings = append(r.warnings, fmt.Sprintf("retaining installed package %s-%s because no matching repository candidate is currently installable", cp, installed.Version.Raw))
				if r.config.Deep {
					return r.processDeps(node, installed, target.String(), depth+1, DomainROOT)
				}
				return nil
			}
		}
		msg := fmt.Sprintf("no installable version of %s satisfies the version constraint %s (%s)", cp, target.String(), reason)
		if masked := r.matchingMaskStatuses(node, matchTarget); len(masked) > 0 {
			msg = fmt.Sprintf("package masked: all matching versions of %s are masked (%s)", cp, strings.Join(masked, "; "))
		}
		r.conflicts = append(r.conflicts, msg)
		if r.config.KeepGoing {
			return nil
		}
		return fmt.Errorf("%s", msg)
	}
	if vi.Installed && !vi.Available {
		if !r.explicitTargets[cp] {
			r.warnings = append(r.warnings, fmt.Sprintf("retaining installed package %s-%s because no matching repository candidate is currently installable", cp, vi.Version.Raw))
			if r.config.Deep {
				return r.processDeps(node, vi, target.String(), depth+1, DomainROOT)
			}
			return nil
		}
	}
	if !resolvedFromProvider {
		if r.constraints == nil {
			r.constraints = make(map[string][]*atom.Atom)
		}
		constraintKey := cp + "|" + vi.Slot
		existingVersion := ""
		for _, action := range r.toInstall {
			if action.Atom != nil && action.Atom.CP() == cp && action.Slot == vi.Slot && action.Atom.Version != nil {
				existingVersion = action.Atom.Version.Raw
				break
			}
		}
		constraint := target
		if r.pendingConstraint != nil && r.pendingConstraint.CP() == cp {
			constraint = r.pendingConstraint
		}
		r.setConstraints(constraintKey, append(r.constraints[constraintKey], constraint))
		r.setConstraintCauses(constraintKey, append(r.constraintCauses[constraintKey], ConflictRequirement{
			Atom: constraint.String(), Reason: r.pendingReason,
		}))
		versionChoices := r.versionsSatisfyingAll(node, vi.Slot, r.constraints[constraintKey])
		if len(versionChoices) > 0 {
			constrained := versionChoices[0]
			decisionKey := "version:" + cp + ":" + vi.Slot
			var replayVersions []*VersionInfo
			installedInSlot := node.GetInstalledVersionForSlot(vi.Slot)
			explicitNewerRoot := depth == 0 && r.explicitTargets[cp] && installedInSlot != nil &&
				vi.Version != nil && installedInSlot.Version != nil && vi.Version.Compare(installedInSlot.Version) > 0
			for _, candidate := range versionChoices {
				if candidate.Available {
					if explicitNewerRoot && candidate.Version != nil && installedInSlot.Version != nil &&
						candidate.Version.Compare(installedInSlot.Version) <= 0 && candidate.Slot == installedInSlot.Slot {
						continue
					}
					replayVersions = append(replayVersions, candidate)
				}
			}
			if forced, ok := r.choiceOverrides[decisionKey]; ok && forced >= 0 && forced < len(replayVersions) {
				constrained = replayVersions[forced]
			}
			if existingVersion != "" && constrained.Version != nil && existingVersion != constrained.Version.Raw {
				if err := r.consumeBacktrack("version", cp+":"+vi.Slot, existingVersion, constrained.Version.Raw); err != nil {
					msg := err.Error()
					r.conflicts = append(r.conflicts, msg)
					return fmt.Errorf("%s", msg)
				}
			}
			vi = constrained
			if len(replayVersions) > 1 && depth > 0 {
				chosen := 0
				labels := make(map[int]string, len(replayVersions))
				order := make([]int, len(replayVersions))
				for index, candidate := range replayVersions {
					order[index] = index
					labels[index] = cp + "-" + candidate.Version.Raw
					if candidate == constrained {
						chosen = index
					}
				}
				r.recordReplayChoice(replayDecision{kind: "version", depKey: decisionKey, chosen: chosen, order: order, labels: labels})
			}
		} else {
			msg := fmt.Sprintf("slot conflict: no single version of %s:%s satisfies all accumulated constraints", cp, vi.Slot)
			r.conflicts = append(r.conflicts, msg)
			r.conflictDetails = append(r.conflictDetails, ConflictDetail{
				Kind: "slot-conflict", Package: cp, Slot: vi.Slot, Message: msg,
				Requirements: append([]ConflictRequirement(nil), r.constraintCauses[constraintKey]...),
				Candidates:   r.slotConflictCandidates(node, vi.Slot, r.constraints[constraintKey]),
			})
			if !r.config.KeepGoing {
				return fmt.Errorf("%s", msg)
			}
		}
	}

	if err := validateVersionMetadataEAPI(vi); err != nil {
		msg := fmt.Sprintf("invalid EAPI metadata for %s: %v", cp, err)
		r.conflicts = append(r.conflicts, msg)
		if r.config.KeepGoing {
			return nil
		}
		return fmt.Errorf("%s", msg)
	}

	// Check REQUIRED_USE
	if vi.RequiredUse != "" {
		if err := CheckRequiredUse(vi.RequiredUse, r.candidateUseFlags(node, vi)); err != nil {
			msg := fmt.Sprintf("REQUIRED_USE constraint not satisfied for %s: %v", cp, err)
			r.conflicts = append(r.conflicts, msg)
			if r.config.KeepGoing {
				return nil
			}
			return fmt.Errorf("%s", msg)
		}
	}

	// Check license
	if vi.License != "" {
		var acceptLicenses []string
		if r.portageConfig != nil {
			acceptLicenses = append([]string(nil), r.portageConfig.ACCEPT_LICENSE...)
			cpv := cp
			if vi.Version != nil {
				cpv += "-" + vi.Version.Raw
			}
			acceptLicenses = append(acceptLicenses,
				r.portageConfig.PackageLicensesFor(cpv, policySlot(vi), vi.Repository)...)
			acceptLicenses = portage.ExpandLicenseGroups(acceptLicenses, r.portageConfig.LicenseGroups)
		}
		if !LicenseExpressionAccepted(vi.License, acceptLicenses, r.candidateUseFlags(node, vi)) {
			msg := fmt.Sprintf("license %s not accepted for %s", vi.License, cp)
			r.conflicts = append(r.conflicts, msg)
			if r.config.KeepGoing {
				return nil
			}
			return fmt.Errorf("%s", msg)
		}
	}

	// check if already installed and satisfies constraints
	installed := node.GetInstalledVersionForSlot(vi.Slot)
	// Portage upgrades an explicitly named package even without --update.
	// --update controls set members and traversal into dependencies; it is not
	// required for `emerge category/package` to select a newer visible CPV.
	allowUpdate := (r.config.Update || (depth == 0 && r.explicitTargets[cp])) && (depth == 0 || r.config.Deep)

	// check package.provided — treat as already installed
	if r.isPackageProvided(target) {
		if !r.config.Update && !r.config.Reinstall {
			if installed != nil && !r.config.NoDeps && r.config.Deep {
				return r.processDeps(node, installed, target.String(), depth+1, DomainROOT)
			}
			return nil
		}
	}

	if installed != nil {
		if versionAtomMatches(node.Atom, target, installed, installedFlags(installed)) {
			// --noreplace: skip if exact same version already installed
			if r.config.NoReplace && vi != nil && installed.Version != nil && vi.Version != nil &&
				vi.Version.Raw == installed.Version.Raw {
				if r.config.Deep {
					return r.processDeps(node, installed, target.String(), depth+1, DomainROOT)
				}
				return nil
			}

			// already installed and satisfies constraint: decide if we need to update
			needInstall := false
			if allowUpdate && vi != nil && vi.Version != nil && installed.Version != nil && vi.Version.Compare(installed.Version) > 0 {
				needInstall = true
			}
			if r.config.NewUse && newUseChanged(installed, vi, r.candidateUseFlags(node, vi)) {
				needInstall = true
			}
			if r.config.ChangedUse && effectiveUseChanged(installedFlags(installed), r.candidateUseFlags(node, vi)) {
				needInstall = true
			}
			if r.config.ChangedDeps && depsChanged(installed, vi) {
				needInstall = true
			}

			if !needInstall && !r.config.Reinstall {
				// satisfied as-is; process deps if deep
				if r.config.Deep {
					return r.processDeps(node, installed, target.String(), depth+1, DomainROOT)
				}
				return nil
			}
		}
	}

	// mark for install
	action := "install"
	if installed != nil {
		if vi.Version != nil && installed.Version != nil && vi.Version.Compare(installed.Version) == 0 {
			action = "reinstall"
		} else if allowUpdate && vi.Version != nil && installed.Version != nil && vi.Version.Compare(installed.Version) > 0 {
			action = "update"
		} else if r.config.NewUse && newUseChanged(installed, vi, r.candidateUseFlags(node, vi)) {
			action = "reinstall"
		} else if r.config.ChangedUse {
			if effectiveUseChanged(installedFlags(installed), r.candidateUseFlags(node, vi)) {
				action = "reinstall"
			}
		} else if r.config.ChangedDeps && depsChanged(installed, vi) {
			action = "reinstall"
		} else if vi.Version != nil && installed.Version != nil && vi.Version.Compare(installed.Version) > 0 {
			action = "update"
		}
	}

	// Exactly one candidate may occupy a CP/slot in the final plan. A later,
	// tighter constraint replaces an earlier choice after intersection above.
	mergeType, binaryPath, mergeErr := r.selectMergeType(node, vi)
	if mergeErr != nil {
		r.conflicts = append(r.conflicts, mergeErr.Error())
		if !r.config.KeepGoing {
			return mergeErr
		}
	}
	actionDomain := r.pendingDomain
	if actionDomain == "" {
		actionDomain = DomainROOT
	}
	actionKey := domainActionKey(versionActionKey(cp, vi), actionDomain)
	r.retractSupersededParent(cp, dependencyVersionKey(cp, vi.Version, vi.Slot, vi.Repository))
	for key, existingAction := range r.toInstall {
		if existingAction.Atom != nil && existingAction.Atom.CP() == cp && existingAction.Slot == vi.Slot && normalizedActionDomain(existingAction.Domain) == actionDomain && key != actionKey {
			r.deleteInstall(key)
		}
	}
	if _, exists := r.toInstall[actionKey]; !exists {
		resolvedAtom := bestVersionAtom(node.Atom, vi)
		if resolvedAtom == nil {
			resolvedAtom = target
		}
		r.setInstall(actionKey, &PkgAction{
			Atom:           resolvedAtom,
			Action:         action,
			Reason:         reason,
			Slot:           vi.Slot,
			Subslot:        vi.Subslot,
			Repository:     vi.Repository,
			RepositoryPath: vi.RepositoryPath,
			SrcURI:         vi.SrcURI,
			UseFlags:       r.candidateUseFlags(node, vi),
			MergeType:      mergeType,
			BinaryPath:     binaryPath,
			Domain:         actionDomain,
		})
		if vi.EAPIDeprecated {
			warning := fmt.Sprintf("selected %s from %s uses deprecated EAPI %s", resolvedAtom, vi.Repository, vi.EAPI)
			seen := false
			for _, existing := range r.warnings {
				if existing == warning {
					seen = true
					break
				}
			}
			if !seen {
				r.warnings = append(r.warnings, warning)
			}
		}
	} else if len(r.dependencyPath) > 0 {
		r.addActionOwner(actionKey, r.dependencyPath[len(r.dependencyPath)-1])
	}
	if depth == 0 {
		r.rootActionKeys[actionKey] = true
	}

	// check for slot operator rebuild triggers
	if target.SlotOp == atom.SlotOpEq && installed != nil && vi != nil {
		if installed.Subslot != vi.Subslot {
			// subslot changed, this triggers rebuild of dependents
			// marked for later processing in CompleteGraph step
		}
	}

	// A newly selected package always needs its direct dependency graph.
	// --deep controls traversal through packages already satisfied by the
	// installed state; it must not be forced for an ordinary install.
	return r.processDeps(node, vi, target.String(), depth+1, DomainROOT)
}

func dependencyVersionKey(cp string, version *atom.Version, slot, repository string) string {
	key := cp
	if version != nil {
		key += "-" + version.Raw + ":" + slot + "::" + repository
	}
	return key
}

func policySlot(vi *VersionInfo) string {
	if vi == nil || vi.Subslot == "" {
		if vi == nil {
			return ""
		}
		return vi.Slot
	}
	return vi.Slot + "/" + vi.Subslot
}

func actionDependencyVersionKey(action *PkgAction) string {
	if action == nil || action.Atom == nil {
		return ""
	}
	return dependencyVersionKey(action.Atom.CP(), action.Atom.Version, action.Slot, action.Repository)
}

// retractSupersededParent removes provenance contributed by older versions of
// a selected CP. Actions with another owner or a root target remain. Removing
// a newly orphaned action recursively retracts dependencies introduced solely
// by that action's version.
func (r *resolver) retractSupersededParent(cp, keepOwner string) {
	queue := []string{}
	removeOwner := func(owner string) {
		for key, action := range r.toInstall {
			owners := r.actionOwners[key]
			if !owners[owner] {
				continue
			}
			updated := cloneBoolMap(owners)
			delete(updated, owner)
			r.setActionOwners(key, updated)
			if len(updated) == 0 && !r.rootActionKeys[key] {
				queue = append(queue, actionDependencyVersionKey(action))
				r.deleteInstall(key)
			}
		}
		r.deleteSeenDep(owner)
	}
	var oldOwners []string
	for _, owners := range r.actionOwners {
		for owner := range owners {
			if owner != keepOwner && strings.HasPrefix(owner, cp+"-") {
				oldOwners = append(oldOwners, owner)
			}
		}
	}
	sort.Strings(oldOwners)
	oldOwners = slices.Compact(oldOwners)
	for _, owner := range oldOwners {
		removeOwner(owner)
	}
	for len(queue) > 0 {
		owner := queue[0]
		queue = queue[1:]
		if owner != "" {
			removeOwner(owner)
		}
	}
}

func (r *resolver) selectMergeType(node *PkgNode, version *VersionInfo) (string, string, error) {
	if !r.config.UsePkg && !r.config.UsePkgOnly {
		return "source", "", nil
	}
	if node == nil || node.Atom == nil || version == nil || version.Version == nil {
		return "source", "", fmt.Errorf("binary package selection lacks a concrete candidate")
	}
	directory := r.config.BinpkgDir
	if directory == "" {
		directory = "/var/cache/binpkgs"
	}
	exact := "=" + node.Atom.CP() + "-" + version.Version.Raw
	binary, err := binpkg.FindPackageMatchingUse(directory, exact, r.candidateUseFlags(node, version), r.config.BinpkgRespectUse)
	if err != nil && r.config.UsePkgOnly {
		return "", "", fmt.Errorf("binary-only: inspect %s: %w", exact, err)
	}
	if binary != nil && (version.Slot == "" || binary.Slot == "" || binary.Slot == version.Slot) {
		return "binary", binary.Path, nil
	}
	if r.config.UsePkgOnly {
		return "", "", fmt.Errorf("binary-only: no usable binary package for %s", exact)
	}
	return "source", "", nil
}

func (r *resolver) processDeps(node *PkgNode, vi *VersionInfo, parent string, depth int, domain DependencyDomain) error {
	if r.activeDeps == nil {
		r.activeDeps = make(map[string]int)
	}
	if r.cycleSeen == nil {
		r.cycleSeen = make(map[string]bool)
	}
	// Dependency traversal describes the final overlaid state, not an
	// intermediate installed state. A parent may be reached through a second
	// path after its replacement was already scheduled; in that case expanding
	// the installed VDB metadata would retain dependencies which the replacement
	// no longer has.
	if vi != nil && vi.Installed {
		if replacement, _ := r.scheduledReplacement(node, vi, domain); replacement != nil && replacement != vi {
			vi = replacement
		}
	}
	depKey := node.Atom.CP()
	if vi != nil && vi.Version != nil {
		depKey += "-" + vi.Version.Raw + ":" + vi.Slot + "::" + vi.Repository
	}
	if start, active := r.activeDeps[depKey]; active {
		cycle := append([]string(nil), r.dependencyPath[start:]...)
		cycle = append(cycle, depKey)
		message := "circular dependency: " + strings.Join(cycle, " -> ")
		if !r.cycleSeen[message] {
			r.cycleSeen[message] = true
			r.warnings = append(r.warnings, message)
		}
		return nil
	}
	if r.seenDeps[depKey] {
		return nil
	}
	r.setSeenDep(depKey, true)
	r.activeDeps[depKey] = len(r.dependencyPath)
	r.dependencyPath = append(r.dependencyPath, depKey)
	defer func() {
		delete(r.activeDeps, depKey)
		r.dependencyPath = r.dependencyPath[:len(r.dependencyPath)-1]
	}()

	edges, err := r.dependenciesForVersion(node, vi)
	if err != nil {
		return fmt.Errorf("parse dependencies for %s: %w", depKey, err)
	}
	for i, edge := range edges {
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

// dependenciesForVersion constructs edges from the metadata belonging to the
// selected version. Package-level edges are only a compatibility fallback for
// synthetic/test graphs; using them for repository packages can accidentally
// resolve the dependencies of a different (often live) version.
func (r *resolver) dependenciesForVersion(node *PkgNode, vi *VersionInfo) ([]*DepEdge, error) {
	if vi == nil || (!vi.DependencyMetadataKnown && vi.Depend == "" && vi.Rdepend == "" && vi.Bdepend == "" && vi.Idepend == "" && vi.Pdepend == "" &&
		vi.InstalledDepend == "" && vi.InstalledRdepend == "" && vi.InstalledBdepend == "" && vi.InstalledIdepend == "" && vi.InstalledPdepend == "") {
		return node.Deps, nil
	}
	chosenEAPI := vi.EAPI
	deps := []struct {
		raw string
		typ DepType
	}{
		{vi.Depend, DepTypeDepend}, {vi.Rdepend, DepTypeRuntime},
		{vi.Bdepend, DepTypeBuild}, {vi.Idepend, DepTypeInstall},
		{vi.Pdepend, DepTypePost},
	}
	flags := r.candidateUseFlags(node, vi)
	scheduledVersion := r.packageVersionScheduled(node, vi)
	scheduledMergeType := r.mergeTypeForVersion(node, vi)
	useInstalledMetadata := vi.Installed && !scheduledVersion && (!r.config.DynamicDeps || !vi.Available)
	if useInstalledMetadata {
		flags = installedFlags(vi)
	}
	if useInstalledMetadata &&
		(vi.InstalledDepend != "" || vi.InstalledRdepend != "" || vi.InstalledBdepend != "" || vi.InstalledIdepend != "" || vi.InstalledPdepend != "") {
		deps = []struct {
			raw string
			typ DepType
		}{
			{vi.InstalledDepend, DepTypeDepend}, {vi.InstalledRdepend, DepTypeRuntime},
			{vi.InstalledBdepend, DepTypeBuild}, {vi.InstalledIdepend, DepTypeInstall},
			{vi.InstalledPdepend, DepTypePost},
		}
		if vi.InstalledEAPI != "" {
			chosenEAPI = vi.InstalledEAPI
		}
	}
	validation := &VersionInfo{EAPI: chosenEAPI}
	for _, dependency := range deps {
		switch dependency.typ {
		case DepTypeDepend:
			validation.Depend = dependency.raw
		case DepTypeRuntime:
			validation.Rdepend = dependency.raw
		case DepTypeBuild:
			validation.Bdepend = dependency.raw
		case DepTypeInstall:
			validation.Idepend = dependency.raw
		case DepTypePost:
			validation.Pdepend = dependency.raw
		}
	}
	if eapi, err := strconv.Atoi(chosenEAPI); err == nil {
		filtered := deps[:0]
		for _, dependency := range deps {
			// Metadata variables introduced by a later EAPI are not part of the
			// package's dependency contract. Portage ignores them rather than
			// interpreting or rejecting their contents.
			if dependency.typ == DepTypeBuild && eapi < 7 || dependency.typ == DepTypeInstall && eapi < 8 {
				continue
			}
			filtered = append(filtered, dependency)
		}
		deps = filtered
	}
	if r.config.BuildPkgOnly && !r.config.Deep && scheduledMergeType != "binary" {
		filtered := deps[:0]
		for _, dependency := range deps {
			if dependency.typ == DepTypeDepend || dependency.typ == DepTypeBuild {
				filtered = append(filtered, dependency)
			}
		}
		deps = filtered
	}
	if r.onlyDepsTargets[node.Atom.CP()] && r.config.OnlyDepsWithRdeps == "n" {
		filtered := deps[:0]
		for _, dependency := range deps {
			if dependency.typ == DepTypeRuntime || dependency.typ == DepTypePost ||
				(dependency.typ == DepTypeInstall && r.config.OnlyDepsWithIDeps != "y") {
				continue
			}
			filtered = append(filtered, dependency)
		}
		deps = filtered
	}
	rootDeps := strings.ToLower(r.config.RootDeps)
	if rootDeps == "rdeps" {
		// Before EAPI 7, DEPEND belongs to the build host. Portage's rdeps
		// mode deliberately omits it for an alternate ROOT. EAPI 7 split
		// host tools into BDEPEND, making DEPEND a target-root requirement.
		if eapi, err := strconv.Atoi(chosenEAPI); err == nil && eapi < 7 {
			filtered := deps[:0]
			for _, dependency := range deps {
				if dependency.typ != DepTypeDepend {
					filtered = append(filtered, dependency)
				}
			}
			deps = filtered
		}
	} else if rootDeps == "true" {
		// Portage additionally treats build/install dependencies as RDEPEND
		// in this mode, satisfying them in ROOT as well as their native
		// BROOT/SYSROOT domain.
		original := append([]struct {
			raw string
			typ DepType
		}(nil), deps...)
		for _, dependency := range original {
			switch dependency.typ {
			case DepTypeDepend, DepTypeBuild, DepTypeInstall:
				deps = append(deps, struct {
					raw string
					typ DepType
				}{dependency.raw, DepTypeRuntime})
			}
		}
	}
	if vi.Installed && !scheduledVersion {
		bdepsMode := r.effectiveBdepsMode()
		filtered := deps[:0]
		for _, dependency := range deps {
			switch dependency.typ {
			case DepTypeRuntime, DepTypePost:
				filtered = append(filtered, dependency)
			case DepTypeDepend, DepTypeBuild:
				if bdepsMode != "n" {
					filtered = append(filtered, dependency)
				}
			case DepTypeInstall:
				// IDEPEND is required for pkg_preinst/pkg_postinst while the
				// package is merged, not as part of a retained runtime closure.
			}
		}
		deps = filtered
	} else if scheduledMergeType == "binary" && r.effectiveBdepsMode() != "y" {
		filtered := deps[:0]
		for _, dependency := range deps {
			if dependency.typ != DepTypeDepend && dependency.typ != DepTypeBuild {
				filtered = append(filtered, dependency)
			}
		}
		deps = filtered
	}
	var edges []*DepEdge
	for _, d := range deps {
		if d.raw == "" {
			continue
		}
		root, err := depstring.Parse(d.raw)
		if err != nil {
			return nil, err
		}
		if err := depstring.ValidatePackageDependenciesEAPI(root, chosenEAPI); err != nil {
			return nil, fmt.Errorf("%s dependency class %d: %w", node.Atom.CP(), d.typ, err)
		}
		groups := make(map[int]*DepEdge)
		groupOptions := make(map[int]map[int][]*DepAtom)
		for _, meta := range depstring.CollectMeta(root) {
			a, err := atom.Parse(meta.Atom)
			if err != nil {
				return nil, err
			}
			if err := validateUseDependencyEAPI(a, chosenEAPI); err != nil {
				return nil, fmt.Errorf("%s dependency %s: %w", node.Atom.CP(), meta.Atom, err)
			}
			if meta.AnyOfID != 0 {
				group := groups[meta.AnyOfID]
				if group == nil {
					group = &DepEdge{From: node, Type: d.typ, Domain: dependencyDomainForEAPI(d.typ, chosenEAPI), EAPI: chosenEAPI, UseCond: meta.AnyOfCondition, UseFlags: flags}
					groups[meta.AnyOfID] = group
					edges = append(edges, group)
				}
				optionCondition := strings.TrimPrefix(strings.TrimPrefix(meta.Condition, meta.AnyOfCondition), ",")
				group.AnyOf = append(group.AnyOf, &DepAtom{Atom: a, UseCond: optionCondition, Block: meta.Block || meta.WeakBlock, StrongBlock: meta.WeakBlock})
				if groupOptions[meta.AnyOfID] == nil {
					groupOptions[meta.AnyOfID] = make(map[int][]*DepAtom)
				}
				option := &DepAtom{Atom: a, UseCond: optionCondition, Block: meta.Block || meta.WeakBlock, StrongBlock: meta.WeakBlock}
				groupOptions[meta.AnyOfID][meta.AnyOfOption] = append(groupOptions[meta.AnyOfID][meta.AnyOfOption], option)
				continue
			}
			edges = append(edges, &DepEdge{
				From: node, To: r.graph.Packages[a.CP()], Type: d.typ, Domain: dependencyDomainForEAPI(d.typ, chosenEAPI),
				EAPI: chosenEAPI, DepAtom: a, UseCond: meta.Condition,
				Block: meta.Block || meta.WeakBlock, StrongBlock: meta.WeakBlock, UseFlags: flags,
			})
		}
		for groupID, group := range groups {
			indices := make([]int, 0, len(groupOptions[groupID]))
			for option := range groupOptions[groupID] {
				indices = append(indices, option)
			}
			sort.Ints(indices)
			for _, option := range indices {
				group.AnyOfGroups = append(group.AnyOfGroups, groupOptions[groupID][option])
			}
		}
	}
	return edges, nil
}

func (r *resolver) effectiveBdepsMode() string {
	mode := r.config.WithBdeps
	if mode == "" && r.config.WithBdepsAuto {
		mode = "auto"
	}
	if mode == "auto" {
		if r.config.UsePkg || r.config.UsePkgOnly || r.config.GetBinPkg || r.config.GetBinPkgOnly {
			return "n"
		}
		return "y"
	}
	return mode
}

func validateUseDependencyEAPI(dependency *atom.Atom, rawEAPI string) error {
	if dependency == nil || len(dependency.UseFlags) == 0 || rawEAPI == "" {
		return nil
	}
	eapi, err := strconv.Atoi(rawEAPI)
	if err != nil {
		return nil
	}
	if eapi < 2 {
		return fmt.Errorf("USE dependencies require EAPI 2 or newer (package uses EAPI %d)", eapi)
	}
	if eapi < 4 {
		for _, flag := range dependency.UseFlags {
			if flag.Default != nil {
				return fmt.Errorf("USE dependency defaults require EAPI 4 or newer (package uses EAPI %d)", eapi)
			}
		}
	}
	return nil
}

func validateVersionMetadataEAPI(version *VersionInfo) error {
	if version == nil || version.EAPI == "" {
		return nil
	}
	eapi, err := strconv.Atoi(version.EAPI)
	if err != nil {
		return nil
	}
	if eapi < 1 {
		for _, flag := range strings.Fields(version.IUse) {
			if strings.HasPrefix(flag, "+") || strings.HasPrefix(flag, "-") {
				return fmt.Errorf("IUSE defaults require EAPI 1 or newer")
			}
		}
	}
	if eapi < 4 && strings.TrimSpace(version.RequiredUse) != "" {
		return fmt.Errorf("REQUIRED_USE requires EAPI 4 or newer")
	}
	return nil
}

func (r *resolver) processEdge(edge *DepEdge, depth int) error {
	if err := r.checkContext(); err != nil {
		return err
	}
	// handle USE conditionals
	if edge.UseCond != "" {
		flags := r.effectiveNodeUseFlags(edge.From)
		if edge.UseFlags != nil {
			flags = edge.UseFlags
		}
		if !conditionsEnabled(flags, edge.UseCond) {
			return nil
		}
	}

	// handle BDEPEND auto-detection
	if edge.Type == DepTypeBuild {
		buildingParent := r.packageScheduled(edge.From)
		bdepsMode := r.effectiveBdepsMode()
		if !buildingParent && bdepsMode == "n" {
			return nil
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
	parentFlags := r.effectiveNodeUseFlags(edge.From)
	if edge.UseFlags != nil {
		parentFlags = edge.UseFlags
	}
	depAtom = resolveUseDependencies(depAtom, parentFlags)

	toNode := edge.To
	if toNode == nil {
		cp := depAtom.CP()
		toNode = r.graph.Packages[cp]
	}
	if toNode == nil {
		if len(r.graph.ProvidersOf[depAtom.CP()]) > 0 {
			return r.processProviderDependency(edge.From, depAtom, depth, edge.Domain)
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
			msg := fmt.Sprintf("dependency %s required by %s could not be satisfied", depAtom.String(), edge.From.Atom.CP())
			r.conflicts = append(r.conflicts, msg)
			if r.config.KeepGoing {
				return nil
			}
			return fmt.Errorf("%s", msg)
		}
	}

	// check if installed version satisfies the dep
	installed := r.matchingInstalledVersionInDomain(toNode, depAtom, edge.Domain)
	if installed != nil {
		if replacement, replaces := r.scheduledReplacement(toNode, installed, edge.Domain); replaces {
			if versionAtomMatches(toNode.Atom, depAtom, replacement, r.candidateUseFlags(toNode, replacement)) {
				if r.config.Deep || edge.Type == DepTypeBuild {
					return r.processDeps(toNode, replacement, depAtom.String(), depth+1, edge.Domain)
				}
				return nil
			}
			// The installed instance is absent from the hypothetical final state.
			// Continue candidate resolution rather than traversing superseded VDB
			// metadata that happens to satisfy this edge.
			installed = nil
		}
	}
	if installed != nil {
		// dep satisfied by installed package
		// check for slot operator rebuilds
		ignoreSlotOps := r.config.IgnoreBuiltSlotOperatorDeps == "y"
		if edge.DepAtom != nil && edge.DepAtom.SlotOp == atom.SlotOpEq {
			// Slot-operator rebuilds must use the same visibility, keyword, atom,
			// repository and USE-aware selector as ordinary dependency planning.
			// GetBestVersion includes raw masked/live records and can invent an
			// uninstallable rebuild target.
			best := r.findMatchingVersion(toNode, depAtom)
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
		if r.config.Deep {
			// Deep traversal applies update and rebuild policy throughout the
			// selected closure, not only to explicit set members. In particular,
			// --newuse must rebuild an otherwise satisfied dependency when its
			// declared/effective USE state changed.
			best := r.findMatchingVersion(toNode, depAtom)
			if best != nil && best.Version != nil && installed.Version != nil {
				newer := r.config.Update && best.Version.Compare(installed.Version) > 0
				rebuild := r.config.NewUse && newUseChanged(installed, best, r.candidateUseFlags(toNode, best))
				rebuild = rebuild || (r.config.ChangedUse && effectiveUseChanged(installedFlags(installed), r.candidateUseFlags(toNode, best)))
				rebuild = rebuild || (r.config.ChangedDeps && depsChanged(installed, best))
				if newer || rebuild {
					reason := fmt.Sprintf("dependency of %s", edge.From.Atom.CP())
					return r.planDependencyInDomain(bestVersionAtom(toNode.Atom, best), depAtom, reason, depth, edge.Domain)
				}
			}
			return r.processDeps(toNode, installed, depAtom.String(), depth+1, edge.Domain)
		}
		return nil
	}

	// find best version that satisfies dep
	best := r.findMatchingVersion(toNode, depAtom)
	if best == nil {
		best = r.findMatchingVersionWithUseChanges(toNode, depAtom)
	}

	if best == nil && len(r.graph.ProvidersOf[depAtom.CP()]) > 0 {
		return r.processProviderDependency(edge.From, depAtom, depth, edge.Domain)
	}

	if best == nil {
		msg := fmt.Sprintf("no installable version of %s satisfies constraint %s (required by %s)", toNode.Atom.CP(), depAtom.String(), edge.From.Atom.CP())
		r.conflicts = append(r.conflicts, msg)
		if r.config.KeepGoing {
			return nil
		}
		return fmt.Errorf("%s", msg)
	}

	// install dependency
	reason := fmt.Sprintf("dependency of %s", edge.From.Atom.CP())
	bestAt := versionedDependencyAtom(toNode, depAtom, best)
	return r.planDependencyInDomain(bestAt, depAtom, reason, depth, edge.Domain)
}

func (r *resolver) scheduledReplacement(node *PkgNode, installed *VersionInfo, domain DependencyDomain) (*VersionInfo, bool) {
	if node == nil || node.Atom == nil || installed == nil {
		return nil, false
	}
	domain = r.effectiveDomain(domain)
	for _, action := range r.toInstall {
		if action == nil || action.Atom == nil || action.Atom.CP() != node.Atom.CP() ||
			action.Slot != installed.Slot || normalizedActionDomain(action.Domain) != domain {
			continue
		}
		for _, vi := range node.Versions {
			if vi != nil && vi.Version != nil && action.Atom.Version != nil &&
				vi.Version.Raw == action.Atom.Version.Raw && vi.Repository == action.Repository {
				return vi, true
			}
		}
		return nil, true
	}
	return nil, false
}

func (r *resolver) processProviderDependency(parent *PkgNode, depAtom *atom.Atom, depth int, domain DependencyDomain) error {
	type providerCandidate struct {
		idx        int
		node       *PkgNode
		constraint *atom.Atom
		installed  *VersionInfo
		best       *VersionInfo
	}
	var candidates []providerCandidate
	for _, providerCP := range r.graph.ProvidersOf[depAtom.CP()] {
		node := r.graph.Packages[providerCP]
		if node == nil {
			continue
		}
		constraint := providerConstraint(depAtom, node)
		installed := r.matchingInstalledVersionInDomain(node, constraint, domain)
		best := r.findMatchingVersion(node, constraint)
		if installed != nil || best != nil {
			candidates = append(candidates, providerCandidate{node: node, constraint: constraint, installed: installed, best: best})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if (candidates[i].installed != nil) != (candidates[j].installed != nil) {
			return candidates[i].installed != nil
		}
		return candidates[i].node.Atom.CP() < candidates[j].node.Atom.CP()
	})

	parentCP := "requested target"
	if parent != nil && parent.Atom != nil {
		parentCP = parent.Atom.CP()
	}
	if len(candidates) == 0 {
		msg := fmt.Sprintf("no provider of %s satisfies constraint %s (required by %s)", depAtom.CP(), depAtom, parentCP)
		r.conflicts = append(r.conflicts, msg)
		if r.config.KeepGoing {
			return nil
		}
		return fmt.Errorf("%s", msg)
	}
	decisionKey := "provider:" + parentCP + "->" + depAtom.CP()
	order := make([]int, len(candidates))
	labels := make(map[int]string, len(candidates))
	for index := range candidates {
		candidates[index].idx = index
		order[index] = index
		labels[index] = candidates[index].node.Atom.CP()
	}
	if forced, ok := r.choiceOverrides[decisionKey]; ok && forced >= 0 && forced < len(candidates) {
		candidates = candidates[forced:]
	}

	var failures []string
	var failureDetails []ConflictDetail
	for index, candidate := range candidates {
		tx := r.beginTransaction()
		var err error
		if candidate.installed != nil {
			if r.config.Deep {
				err = r.processDeps(candidate.node, candidate.installed, depAtom.String(), depth+1, domain)
			}
		} else {
			reason := fmt.Sprintf("provider of %s (dependency of %s)", depAtom.CP(), parentCP)
			selected := versionedConstraintAtom(candidate.constraint, candidate.best)
			err = r.planDependencyInDomain(selected, candidate.constraint, reason, depth, domain)
		}
		if err == nil && len(r.conflicts) == tx.conflictsLen {
			r.replayChoices = append(r.replayChoices, replayDecision{
				kind: "provider", depKey: decisionKey, chosen: candidate.idx,
				order: append([]int(nil), order...), labels: labels,
			})
			r.commitTransaction(tx)
			return nil
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", candidate.node.Atom.CP(), err))
		} else {
			failures = append(failures, fmt.Sprintf("%s: %s", candidate.node.Atom.CP(), strings.Join(r.conflicts[tx.conflictsLen:], "; ")))
		}
		failureDetails = append(failureDetails, cloneConflictDetails(r.conflictDetails[tx.conflictDetailsLen:])...)
		r.rollbackTransaction(tx)
		if index+1 < len(candidates) {
			if err := r.consumeBacktrack("provider", decisionKey, candidate.node.Atom.CP(), candidates[index+1].node.Atom.CP()); err != nil {
				break
			}
		}
	}

	msg := fmt.Sprintf("no provider of %s produced a valid plan: %s", depAtom.CP(), strings.Join(failures, "; "))
	r.conflicts = append(r.conflicts, msg)
	r.conflictDetails = append(r.conflictDetails, failureDetails...)
	if r.config.KeepGoing {
		return nil
	}
	return fmt.Errorf("%s", msg)
}

func (r *resolver) packageScheduled(node *PkgNode) bool {
	if node == nil || node.Atom == nil {
		return false
	}
	cp := node.Atom.CP()
	for _, vi := range node.Versions {
		if vi != nil && vi.Version != nil {
			if action := r.toInstall[versionActionKey(cp, vi)]; action != nil {
				return true
			}
		}
	}
	if action := r.toInstall[cp]; action != nil {
		if action.Atom != nil && action.Atom.CP() == cp {
			return true
		}
	}
	return false
}

func (r *resolver) packageVersionScheduled(node *PkgNode, vi *VersionInfo) bool {
	for _, domain := range []DependencyDomain{DomainROOT, DomainSYSROOT, DomainBROOT} {
		if r.packageVersionScheduledInDomain(node, vi, domain) {
			return true
		}
	}
	return false
}

func (r *resolver) packageVersionScheduledInDomain(node *PkgNode, vi *VersionInfo, domain DependencyDomain) bool {
	if node == nil || node.Atom == nil || vi == nil || vi.Version == nil {
		return false
	}
	action := r.toInstall[domainActionKey(versionActionKey(node.Atom.CP(), vi), domain)]
	return action != nil && action.Atom != nil && action.Atom.CP() == node.Atom.CP() &&
		(action.Slot == "" || action.Slot == vi.Slot)
}

func (r *resolver) mergeTypeForVersion(node *PkgNode, vi *VersionInfo) string {
	if node == nil || node.Atom == nil || vi == nil {
		return ""
	}
	action := r.toInstall[versionActionKey(node.Atom.CP(), vi)]
	if action == nil {
		return ""
	}
	return action.MergeType
}

func (r *resolver) processAnyOf(node *PkgNode, edge *DepEdge, edgeIdx int, depth int) error {
	if edge.UseCond != "" {
		flags := r.effectiveNodeUseFlags(node)
		if edge.UseFlags != nil {
			flags = edge.UseFlags
		}
		if !conditionsEnabled(flags, edge.UseCond) {
			return nil
		}
	}
	options := edge.AnyOfGroups
	if len(options) == 0 {
		for _, option := range edge.AnyOf {
			options = append(options, []*DepAtom{option})
		}
	}
	decisionKey := fmt.Sprintf("%s->%d", node.Atom.CP(), edgeIdx)

	type member struct {
		depAtom        *DepAtom
		installedVI    *VersionInfo
		best           *VersionInfo
		needsUseChange bool
	}
	// Try each conjunction alternative, preferring one whose every active
	// member is already installed.
	type candidate struct {
		idx       int
		members   []member
		installed bool
	}
	var candidates []candidate
	activeOptions := 0

	for i, option := range options {
		optionFlags := r.effectiveNodeUseFlags(node)
		if edge.UseFlags != nil {
			optionFlags = edge.UseFlags
		}
		candidate := candidate{idx: i, installed: true}
		feasible := true
		for _, opt := range option {
			if opt == nil || opt.Atom == nil || opt.Block {
				continue
			}
			if opt.UseCond != "" && opt.UseCond != edge.UseCond && !conditionsEnabled(optionFlags, opt.UseCond) {
				continue
			}
			resolvedOpt := *opt
			resolvedOpt.Atom = resolveUseDependencies(opt.Atom, optionFlags)
			toNode := r.graph.Packages[resolvedOpt.Atom.CP()]
			if toNode == nil {
				feasible = false
				break
			}
			inst := r.matchingInstalledVersionInDomain(toNode, resolvedOpt.Atom, edge.Domain)
			best := r.findMatchingVersion(toNode, resolvedOpt.Atom)
			needsUseChange := false
			if inst == nil && best == nil {
				best, _ = r.matchingVersionUseChanges(toNode, resolvedOpt.Atom)
				needsUseChange = best != nil
			}
			if inst == nil && best == nil {
				feasible = false
				break
			}
			constraintVersion := best
			if constraintVersion == nil {
				constraintVersion = inst
			}
			if constraintVersion != nil {
				constraintKey := resolvedOpt.Atom.CP() + "|" + constraintVersion.Slot
				if existing := r.constraints[constraintKey]; len(existing) > 0 {
					combined := append(append([]*atom.Atom(nil), existing...), resolvedOpt.Atom)
					if len(r.versionsSatisfyingAll(toNode, constraintVersion.Slot, combined)) == 0 {
						feasible = false
						break
					}
				}
			}
			candidate.installed = candidate.installed && inst != nil
			candidate.members = append(candidate.members, member{depAtom: &resolvedOpt, installedVI: inst, best: best, needsUseChange: needsUseChange})
		}
		if len(candidate.members) == 0 {
			continue
		}
		activeOptions++
		if feasible {
			candidates = append(candidates, candidate)
		}
	}

	if len(candidates) == 0 {
		if activeOptions == 0 && !anyOfRequiresActiveOption(edge.EAPI) {
			return nil
		}
		var opts []string
		for _, option := range options {
			var members []string
			for _, o := range option {
				if o.Atom != nil {
					members = append(members, o.Atom.String())
				}
			}
			opts = append(opts, "("+strings.Join(members, " ")+")")
		}
		msg := fmt.Sprintf("none of the alternative dependencies required by %s could be satisfied: %s", node.Atom.CP(), strings.Join(opts, ", "))
		r.conflicts = append(r.conflicts, msg)
		if r.config.KeepGoing {
			return nil
		}
		return fmt.Errorf("%s", msg)
	}

	// sort: installed first, then by version
	sort.Slice(candidates, func(i, j int) bool {
		singletons := len(candidates[i].members) == 1 && len(candidates[j].members) == 1
		if singletons && candidates[i].installed != candidates[j].installed {
			return candidates[i].installed
		}
		// Multi-atom alternatives encode an implementation tuple. Eclasses order
		// these deliberately (for example newest configured Python first), so a
		// stale fully-installed tuple must not outrank the declared preference.
		return candidates[i].idx < candidates[j].idx
	})
	order := make([]int, 0, len(candidates))
	labels := make(map[int]string, len(candidates))
	for _, candidate := range candidates {
		order = append(order, candidate.idx)
		var atoms []string
		for _, member := range candidate.members {
			atoms = append(atoms, member.depAtom.Atom.String())
		}
		labels[candidate.idx] = strings.Join(atoms, " + ")
	}
	if forced, ok := r.choiceOverrides[decisionKey]; ok {
		start := -1
		for index, candidate := range candidates {
			if candidate.idx == forced {
				start = index
				break
			}
		}
		if start >= 0 {
			candidates = candidates[start:]
		}
	}

	var failures []string
	var failureDetails []ConflictDetail
	for candidateIndex, chosen := range candidates {
		tx := r.beginTransaction()
		r.replayChoices = append(r.replayChoices, replayDecision{
			kind:   "any-of",
			depKey: decisionKey,
			chosen: chosen.idx,
			order:  append([]int(nil), order...),
			labels: labels,
		})

		var err error
		for _, chosenMember := range chosen.members {
			toNode := r.graph.Packages[chosenMember.depAtom.Atom.CP()]
			if chosenMember.installedVI != nil {
				if r.config.Deep {
					best := chosenMember.best
					installed := chosenMember.installedVI
					if best != nil && best.Version != nil && installed.Version != nil {
						newer := r.config.Update && best.Version.Compare(installed.Version) > 0
						rebuild := r.config.NewUse && newUseChanged(installed, best, r.candidateUseFlags(toNode, best))
						rebuild = rebuild || (r.config.ChangedUse && effectiveUseChanged(installedFlags(installed), r.candidateUseFlags(toNode, best)))
						rebuild = rebuild || (r.config.ChangedDeps && depsChanged(installed, best))
						if newer || rebuild {
							reason := fmt.Sprintf("any-of dependency of %s", node.Atom.CP())
							err = r.planDependencyInDomain(versionedConstraintAtom(chosenMember.depAtom.Atom, best), chosenMember.depAtom.Atom, reason, depth, edge.Domain)
						} else {
							err = r.processDeps(toNode, installed, chosenMember.depAtom.Atom.String(), depth, edge.Domain)
						}
					} else {
						err = r.processDeps(toNode, installed, chosenMember.depAtom.Atom.String(), depth, edge.Domain)
					}
				}
			} else {
				if chosenMember.needsUseChange {
					chosenMember.best = r.findMatchingVersionWithUseChanges(toNode, chosenMember.depAtom.Atom)
				}
				if chosenMember.best == nil {
					err = fmt.Errorf("no installable version found for any-of dependency %s", chosenMember.depAtom.Atom.CP())
				} else {
					reason := fmt.Sprintf("any-of dependency of %s", node.Atom.CP())
					err = r.planDependencyInDomain(versionedConstraintAtom(chosenMember.depAtom.Atom, chosenMember.best), chosenMember.depAtom.Atom, reason, depth, edge.Domain)
				}
			}
			if err != nil {
				break
			}
		}

		// KeepGoing may turn a branch error into a recorded conflict. A branch
		// that added conflicts is still not a successful alternative.
		policyOnly := true
		for _, conflict := range r.conflicts[tx.conflictsLen:] {
			policyOnly = policyOnly && persistentRepairConflict(conflict)
		}
		if err == nil && policyOnly {
			r.commitTransaction(tx)
			return nil
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("alternative %d: %v", chosen.idx+1, err))
		} else {
			failures = append(failures, fmt.Sprintf("alternative %d: %s", chosen.idx+1, strings.Join(r.conflicts[tx.conflictsLen:], "; ")))
		}
		failureDetails = append(failureDetails, cloneConflictDetails(r.conflictDetails[tx.conflictDetailsLen:])...)
		r.rollbackTransaction(tx)

		if candidateIndex+1 < len(candidates) {
			if err := r.consumeBacktrack("any-of", decisionKey, fmt.Sprintf("alternative %d", chosen.idx+1), fmt.Sprintf("alternative %d", candidates[candidateIndex+1].idx+1)); err != nil {
				break
			}
		}
	}

	msg := fmt.Sprintf("none of the alternative dependencies required by %s produced a valid plan: %s", node.Atom.CP(), strings.Join(failures, "; "))
	r.conflicts = append(r.conflicts, msg)
	r.conflictDetails = append(r.conflictDetails, failureDetails...)
	if r.config.KeepGoing {
		return nil
	}
	return fmt.Errorf("%s", msg)
}

func edgeAnyOfGroups(edge *DepEdge) [][]*DepAtom {
	if edge == nil {
		return nil
	}
	if len(edge.AnyOfGroups) > 0 {
		return edge.AnyOfGroups
	}
	groups := make([][]*DepAtom, 0, len(edge.AnyOf))
	for _, option := range edge.AnyOf {
		groups = append(groups, []*DepAtom{option})
	}
	return groups
}

func (r *resolver) planDependency(selected, constraint *atom.Atom, reason string, depth int) error {
	return r.planDependencyInDomain(selected, constraint, reason, depth, DomainROOT)
}

func (r *resolver) planDependencyInDomain(selected, constraint *atom.Atom, reason string, depth int, domain DependencyDomain) error {
	previous := r.pendingConstraint
	previousReason := r.pendingReason
	previousDomain := r.pendingDomain
	r.pendingConstraint = constraint
	r.pendingReason = reason
	r.pendingDomain = r.effectiveDomain(domain)
	err := r.planPackage(selected, reason, depth)
	r.pendingConstraint = previous
	r.pendingReason = previousReason
	r.pendingDomain = previousDomain
	return err
}

func (r *resolver) processBlock(edge *DepEdge) error {
	toNode := edge.To
	if toNode == nil && edge.DepAtom != nil {
		toNode = r.graph.Packages[edge.DepAtom.CP()]
	}
	if toNode == nil {
		return nil // nothing to block
	}
	installedNode := toNode
	blockDomain := r.effectiveDomain(edge.Domain)
	if graph := r.config.InstalledByDomain[blockDomain]; graph != nil {
		installedNode = graph.Packages[toNode.Atom.CP()]
		if installedNode == nil {
			return nil
		}
	}

	var blocked []*VersionInfo
	for _, installed := range installedNode.Versions {
		if installed == nil || !installed.Installed {
			continue
		}
		if replacement, replaces := r.scheduledReplacement(toNode, installed, edge.Domain); replaces && replacement != nil {
			if edge.DepAtom == nil || !versionAtomMatches(toNode.Atom, edge.DepAtom, replacement, r.candidateUseFlags(toNode, replacement)) {
				continue
			}
		}
		if edge.DepAtom == nil || versionAtomMatches(installedNode.Atom, edge.DepAtom, installed, installedFlags(installed)) {
			blocked = append(blocked, installed)
		}
	}
	sort.Slice(blocked, func(i, j int) bool {
		if blocked[i].Slot != blocked[j].Slot {
			return blocked[i].Slot < blocked[j].Slot
		}
		return blocked[i].Version.Compare(blocked[j].Version) < 0
	})
	if len(blocked) == 0 {
		return nil
	}
	// A blocker against the installed version can be resolved by a coordinated
	// upgrade when the selected replacement no longer matches the blocker.
	if r.config.Update && edge.DepAtom != nil && len(blocked) == 1 {
		installed := blocked[0]
		// PkgNode.Atom may carry the installed identity for legacy graph
		// consumers. Replacement selection needs an unconstrained CP atom;
		// otherwise the old slot/subslot accidentally excludes its successor.
		packageConstraint := &atom.Atom{Category: toNode.Atom.Category, Package: toNode.Atom.Package}
		best := r.findMatchingVersion(toNode, packageConstraint)
		if best != nil && best.Version != nil && installed.Version != nil && best.Version.Compare(installed.Version) > 0 &&
			!versionAtomMatches(toNode.Atom, edge.DepAtom, best, r.candidateUseFlags(toNode, best)) {
			reason := fmt.Sprintf("blocker replacement required by %s", edge.From.Atom.CP())
			return r.planPackage(bestVersionAtom(toNode.Atom, best), reason, 0)
		}
	}

	cp := toNode.Atom.CP()

	// check if the blocked package is a world/target package
	isWorldPkg := false
	if blockDomain != DomainROOT {
		isWorldPkg = false
	} else {
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
	}

	blocker := edge.From.Atom.CP()

	if isWorldPkg {
		msg := fmt.Sprintf("%s (%s) prevents %s from being installed, but %s is in the world set", blocker, edge.DepAtom.String(), cp, cp)
		r.conflicts = append(r.conflicts, msg)
		if r.config.KeepGoing {
			return nil
		}
		return fmt.Errorf("%s", msg)
	}

	// Uninstall every installed instance matched by the blocker atom. Parallel
	// slots not matched by a version/slot-qualified blocker remain in the final
	// state and may continue satisfying other dependencies.
	for _, installed := range blocked {
		cpv := cp
		if installed.Version != nil && installed.Version.Raw != "" {
			cpv = cp + "-" + versionRepositoryKey(installed.Version.Raw, installed.Repository)
		}
		key := domainActionKey(cpv, blockDomain)
		if _, exists := r.toUninstall[key]; !exists {
			strength := "weak"
			if edge.StrongBlock {
				strength = "strong; remove before merge"
			}
			r.setUninstall(key, &PkgAction{
				Atom:   bestVersionAtom(installedNode.Atom, installed),
				Action: "uninstall",
				Reason: fmt.Sprintf("%s blocker from %s", strength, blocker),
				Slot:   installed.Slot, Subslot: installed.Subslot, Repository: installed.Repository,
				Domain: blockDomain,
			})
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
		if r.checkContext() != nil {
			return
		}
		r.metrics.CompleteGraphPasses++
		found := false
		installKeys := make([]string, 0, len(r.toInstall))
		for key := range r.toInstall {
			installKeys = append(installKeys, key)
		}
		sort.Strings(installKeys)
		for _, key := range installKeys {
			if r.checkContext() != nil {
				return
			}
			a := r.toInstall[key]
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
				if dependent == nil || !dependent.Installed || dependent.GetInstalledVersion() == nil || (r.setScoped && !r.selectedCPs[dependent.Atom.CP()]) {
					continue
				}
				cpv := dependent.Atom.CP()
				if r.packageScheduled(dependent) {
					continue // already being rebuilt
				}
				reason := fmt.Sprintf("slot operator rebuild (subslot change in %s)", cp)
				installedDependent := dependent.GetInstalledVersion()
				constraint := *dependent.Atom
				constraint.Slot = installedDependent.Slot
				dVI := r.findMatchingVersion(dependent, &constraint)
				if dVI == nil || !dVI.Available {
					continue
				}
				depAtom := bestVersionAtom(dependent.Atom, dVI)
				cpv = depAtom.CP()
				if depAtom.Version != nil && depAtom.Version.Raw != "" {
					cpv = cpv + "-" + depAtom.Version.Raw
				}
				r.setInstall(versionActionKey(dependent.Atom.CP(), dVI), &PkgAction{
					Atom: depAtom, Action: "reinstall", Reason: reason,
					Slot: dVI.Slot, Subslot: dVI.Subslot, Repository: dVI.Repository,
					RepositoryPath: dVI.RepositoryPath, SrcURI: dVI.SrcURI,
					UseFlags: r.candidateUseFlags(dependent, dVI),
				})
				found = true
			}
		}
		if !found {
			break
		}
	}
}

func (r *resolver) verifyPlannedState() {
	// Conflicts produced by the primary solve are authoritative. During a
	// repair pass, however, only user-policy requirements may be promoted into
	// this set; other newly produced conflicts belong to that intermediate plan.
	persistent := append([]string(nil), r.conflicts...)
	persistentSeen := make(map[string]bool, len(persistent))
	baseDetailsLen := len(r.conflictDetails)
	for _, conflict := range persistent {
		persistentSeen[conflict] = true
	}
	for attempt := 0; attempt < 100; attempt++ {
		if r.checkContext() != nil {
			return
		}
		r.metrics.VerifierPasses++
		r.conflicts = append(r.conflicts[:0], persistent...)
		r.conflictDetails = r.conflictDetails[:baseDetailsLen]
		if !r.verifyPlannedStatePass() {
			return
		}
		r.metrics.VerifierRepairs++
		// Verification findings describe the current intermediate plan and are
		// recomputed after a repair. Policy changes discovered while resolving
		// that repair (USE, masks, licenses, etc.) remain requirements of every
		// later plan and must not be truncated with transient verifier output.
		for _, conflict := range r.conflicts[len(persistent):] {
			if !persistentRepairConflict(conflict) || persistentSeen[conflict] {
				continue
			}
			persistentSeen[conflict] = true
			persistent = append(persistent, conflict)
		}
	}
	r.conflicts = append(persistent, "post-solve verification: complete-graph repair did not converge")
	r.conflictDetails = append(r.conflictDetails[:baseDetailsLen], ConflictDetail{Kind: "post-solve-verification", Message: "post-solve verification: complete-graph repair did not converge"})
}

// Only requirements that need user policy changes survive verification repair
// passes. Solver and verifier conflicts describe an intermediate candidate and
// must be recomputed after the repair changes that candidate.
func persistentRepairConflict(conflict string) bool {
	return strings.HasPrefix(conflict, "USE changes are necessary to proceed:") ||
		strings.HasPrefix(conflict, "package masked:") ||
		strings.HasPrefix(conflict, "license ")
}

func (r *resolver) verifyPlannedStatePass() bool {
	changed := make(map[string]bool)
	removed := make(map[string]bool)
	removedNames := make(map[string]bool)
	for _, action := range r.toInstall {
		if action.Atom != nil {
			changed[action.Atom.CP()] = true
		}
	}
	for _, action := range r.toUninstall {
		if action.Atom != nil {
			removed[actionVersionKey(action)] = true
			removedNames[action.Atom.CP()] = true
		}
	}
	affectedNames := make(map[string]bool, len(changed)+len(removedNames))
	for cp := range changed {
		affectedNames[cp] = true
		if provided := r.graph.Providers[cp]; provided != "" {
			affectedNames[provided] = true
		}
	}
	for cp := range removedNames {
		affectedNames[cp] = true
		if provided := r.graph.Providers[cp]; provided != "" {
			affectedNames[provided] = true
		}
	}
	seen := make(map[string]bool)
	repairAdded := false
	addConflictKey := func(key, message string) bool {
		if !seen[key] {
			seen[key] = true
			r.conflicts = append(r.conflicts, message)
			return true
		}
		return false
	}
	addConflict := func(message string) {
		if addConflictKey(message, message) {
			r.conflictDetails = append(r.conflictDetails, ConflictDetail{Kind: "post-solve-verification", Message: message})
		}
	}
	addIssue := func(node *PkgNode, vi *VersionInfo, parentChanging bool, message string) {
		if r.config.CompleteGraph && !parentChanging {
			added, err := r.scheduleVerificationRebuild(node, vi, message)
			if err != nil {
				addConflict(fmt.Sprintf("%s; repair failed: %v", message, err))
				return
			}
			if added {
				repairAdded = true
				return
			}
		}
		if !r.strictWholeState && !parentChanging && node != nil && node.Atom != nil && node.GetBestVersion() == nil {
			cp := node.Atom.CP()
			diagnostic := fmt.Sprintf("installed-only package %s has no available ebuild and cannot retain a valid dependency graph (%s); it is a depclean candidate", cp, strings.TrimPrefix(message, "post-solve verification: "))
			depKey := cp
			if vi != nil && vi.Version != nil {
				depKey += "-" + vi.Version.Raw + ":" + vi.Slot + "::" + vi.Repository
			}
			if !r.seenDeps[depKey] {
				// An orphan outside the selected world/system dependency closure is
				// depclean information, not a reason to block an unrelated update.
				warningPrefix := "installed-only package " + cp + " "
				found := false
				for _, warning := range r.warnings {
					found = found || strings.HasPrefix(warning, warningPrefix)
				}
				if !found {
					r.warnings = append(r.warnings, diagnostic)
				}
				return
			}
			message := "post-solve verification: " + diagnostic
			if addConflictKey("installed-only:"+cp, message) {
				r.conflictDetails = append(r.conflictDetails, ConflictDetail{Kind: "post-solve-verification", Package: cp, Message: message})
			}
			return
		}
		if addConflictKey(message, message) {
			packageName := ""
			if node != nil && node.Atom != nil {
				packageName = node.Atom.CP()
			}
			r.conflictDetails = append(r.conflictDetails, ConflictDetail{Kind: "post-solve-verification", Package: packageName, Message: message})
		}
	}
	repairDependency := func(dep *atom.Atom, parentCP string) bool {
		if r.config.EmptyTree || !r.config.CompleteGraph || dep == nil {
			return false
		}
		node := r.graph.Packages[dep.CP()]
		best := r.findMatchingVersion(node, dep)
		if best == nil {
			best = r.findMatchingVersionWithUseChanges(node, dep)
		}
		if best == nil {
			return false
		}
		selected := versionedDependencyAtom(node, dep, best)
		if err := r.planDependency(selected, dep, "complete-graph dependency repair required by "+parentCP, 1); err != nil {
			addConflict(fmt.Sprintf("post-solve verification: repair %s required by %s failed: %v", dep.String(), parentCP, err))
			return false
		}
		repairAdded = true
		return true
	}

	// Repository-only packages outside the transaction cannot affect the final
	// installed state. Excluding them here is important: a typical Gentoo tree
	// has tens of thousands of CPs, while the installed set is usually around a
	// thousand, and complete-graph repair may run several verification passes.
	packageCPs := make([]string, 0, len(r.toInstall)+len(r.graph.Packages)/32)
	packageScan := 0
	for cp, node := range r.graph.Packages {
		packageScan++
		if packageScan%256 == 0 && r.checkContext() != nil {
			return false
		}
		if !changed[cp] && (node == nil || !node.Installed) {
			continue
		}
		packageCPs = append(packageCPs, cp)
	}
	sort.Strings(packageCPs)
	for _, cp := range packageCPs {
		if r.checkContext() != nil {
			return false
		}
		node := r.graph.Packages[cp]
		if r.setScoped && r.config.CompleteGraph && !changed[cp] && !r.selectedCPs[cp] && node != nil && node.GetBestVersion() != nil {
			// Portage completes the selected @world/@system graph, not every
			// installed package on the machine. Available-but-unselected orphans
			// are depclean territory and must not be rebuilt into the transaction.
			continue
		}
		versions := r.finalVersions(node, removed)
		if len(versions) == 0 {
			continue
		}
		for _, vi := range versions {
			if r.checkContext() != nil {
				return false
			}
			parentChanging := r.packageVersionScheduled(node, vi)
			versionKey := dependencyVersionKey(cp, vi.Version, vi.Slot, vi.Repository)
			if r.setScoped && r.config.CompleteGraph && !parentChanging {
				if !r.seenDeps[versionKey] {
					// Selection is version/slot-specific. Updating one Python, LLVM,
					// Ruby, or similar slot must not pull unrelated installed slots
					// into complete-graph repair merely because they share a CP.
					continue
				}
			}
			if !parentChanging && !versionDependenciesMention(vi, affectedNames) {
				continue
			}
			edges, err := r.dependenciesForVersion(node, vi)
			if err != nil {
				addConflict(fmt.Sprintf("post-solve verification: parse dependencies for %s: %v", cp, err))
				continue
			}
			for _, edge := range edges {
				if r.checkContext() != nil {
					return false
				}
				// Build/install-time dependencies of retained packages need not remain
				// installed. They are mandatory for packages in this transaction.
				if !parentChanging && (edge.Type == DepTypeBuild || edge.Type == DepTypeDepend || edge.Type == DepTypeInstall) {
					continue
				}
				flags := edge.UseFlags
				if flags == nil {
					if parentChanging {
						flags = r.candidateUseFlags(node, vi)
					} else {
						flags = installedFlags(vi)
					}
				}
				if edge.UseCond != "" && !conditionsEnabled(flags, edge.UseCond) {
					continue
				}
				if len(edge.AnyOf) > 0 {
					active, satisfied := 0, false
					for _, option := range edgeAnyOfGroups(edge) {
						optionActive, optionSatisfied := false, true
						for _, member := range option {
							if member.UseCond != "" && !conditionsEnabled(flags, member.UseCond) {
								continue
							}
							optionActive = true
							if !r.finalAtomSatisfiedInDomain(resolveUseDependencies(member.Atom, flags), removed, edge.Domain) {
								optionSatisfied = false
							}
						}
						if optionActive {
							active++
						}
						if optionActive && optionSatisfied {
							satisfied = true
							break
						}
					}
					if (active > 0 || anyOfRequiresActiveOption(edge.EAPI)) && !satisfied && (parentChanging || r.anyOptionChanged(edge.AnyOf, changed)) {
						addIssue(node, vi, parentChanging, fmt.Sprintf("post-solve verification: no alternative dependency of %s is satisfied", cp))
					}
					continue
				}
				dep := resolveUseDependencies(edge.DepAtom, flags)
				if dep == nil {
					continue
				}
				if !parentChanging && !changed[dep.CP()] && !removedNames[dep.CP()] {
					continue
				}
				if edge.Block {
					if r.finalAtomSatisfiedInDomain(dep, removed, edge.Domain) {
						addIssue(node, vi, parentChanging, fmt.Sprintf("post-solve verification: %s remains blocked by %s", dep.CP(), cp))
					}
					continue
				}
				if !r.finalAtomSatisfiedInDomain(dep, removed, edge.Domain) {
					if parentChanging && repairDependency(dep, cp) {
						continue
					}
					addIssue(node, vi, parentChanging, fmt.Sprintf("post-solve verification: %s required by %s is not satisfied", dep.String(), cp))
				}
			}
		}
	}
	return repairAdded
}

func (r *resolver) scheduleVerificationRebuild(node *PkgNode, installed *VersionInfo, cause string) (bool, error) {
	if node == nil || node.Atom == nil || installed == nil {
		return false, nil
	}
	target := *node.Atom
	target.Version = nil
	target.Op = atom.OpNone
	target.Slot = installed.Slot
	target.Subslot = ""
	best := r.findMatchingVersion(node, &target)
	if best == nil || best.Version == nil || !best.Available {
		return false, nil
	}
	actionKey := versionActionKey(node.Atom.CP(), best)
	if _, exists := r.toInstall[actionKey]; exists {
		return false, nil
	}
	// The replacement is built with the current effective configuration. Old
	// installed USE belongs only to the installed instance while it remains in
	// the graph; carrying it onto a replacement invents package.use changes and
	// is especially damaging during Python/Ruby target transitions.
	action := "reinstall"
	if installed.Version != nil && best.Version.Compare(installed.Version) > 0 {
		action = "update"
	}
	resolved := bestVersionAtom(node.Atom, best)
	r.setInstall(actionKey, &PkgAction{
		Atom: resolved, Action: action,
		Reason: "complete-graph repair: " + strings.TrimPrefix(cause, "post-solve verification: "),
		Slot:   best.Slot, Subslot: best.Subslot, Repository: best.Repository,
		RepositoryPath: best.RepositoryPath, SrcURI: best.SrcURI,
		UseFlags: r.candidateUseFlags(node, best),
	})
	if err := r.processDeps(node, best, resolved.String(), 1, DomainROOT); err != nil {
		return true, err
	}
	return true, nil
}

func versionDependenciesMention(vi *VersionInfo, names map[string]bool) bool {
	if vi == nil || len(names) == 0 {
		return false
	}
	raw := strings.Join([]string{
		vi.InstalledDepend, vi.InstalledRdepend, vi.InstalledBdepend, vi.InstalledIdepend, vi.InstalledPdepend,
		vi.Depend, vi.Rdepend, vi.Bdepend, vi.Idepend, vi.Pdepend,
	}, " ")
	for name := range names {
		if strings.Contains(raw, name) {
			return true
		}
	}
	return false
}

func (r *resolver) finalVersions(node *PkgNode, removed map[string]bool) []*VersionInfo {
	if node == nil {
		return nil
	}
	bySlot := make(map[string]*VersionInfo)
	for _, vi := range node.Versions {
		r.metrics.CandidateEvaluations++
		if vi != nil && vi.Installed && !removed[versionActionKey(node.Atom.CP(), vi)] {
			bySlot[vi.Slot] = vi
		}
	}
	for _, vi := range node.Versions {
		r.metrics.CandidateEvaluations++
		if vi == nil || vi.Version == nil {
			continue
		}
		action := r.toInstall[versionActionKey(node.Atom.CP(), vi)]
		if action != nil && (action.Slot == "" || vi.Slot == action.Slot) {
			bySlot[vi.Slot] = vi
		}
	}
	slots := make([]string, 0, len(bySlot))
	for slot := range bySlot {
		slots = append(slots, slot)
	}
	sort.Strings(slots)
	result := make([]*VersionInfo, 0, len(slots))
	for _, slot := range slots {
		result = append(result, bySlot[slot])
	}
	return result
}

func (r *resolver) finalAtomSatisfied(dep *atom.Atom, removed map[string]bool) bool {
	return r.finalAtomSatisfiedInDomain(dep, removed, DomainROOT)
}

func (r *resolver) finalAtomSatisfiedInDomain(dep *atom.Atom, removed map[string]bool, domain DependencyDomain) bool {
	if dep == nil {
		return false
	}
	domain = r.effectiveDomain(domain)
	installedGraph := r.graph
	if configured := r.config.InstalledByDomain[domain]; configured != nil {
		installedGraph = configured
	}
	node := installedGraph.Packages[dep.CP()]
	if node != nil {
		for _, vi := range r.finalVersionsInDomain(node, domainRemovals(removed, domain), installedGraph == r.graph, domain) {
			flags := installedFlags(vi)
			if installedGraph == r.graph && r.packageVersionScheduled(node, vi) {
				flags = r.candidateUseFlags(node, vi)
			}
			if versionAtomMatches(node.Atom, dep, vi, flags) {
				return true
			}
		}
	}
	for _, providerCP := range installedGraph.ProvidersOf[dep.CP()] {
		provider := installedGraph.Packages[providerCP]
		providerDep := providerConstraint(dep, provider)
		for _, vi := range r.finalVersionsInDomain(provider, domainRemovals(removed, domain), installedGraph == r.graph, domain) {
			flags := installedFlags(vi)
			if installedGraph == r.graph && r.packageVersionScheduled(provider, vi) {
				flags = r.candidateUseFlags(provider, vi)
			}
			if versionAtomMatches(provider.Atom, providerDep, vi, flags) {
				return true
			}
		}
	}
	// Planned actions are stored independently by domain and overlay only the
	// corresponding immutable installed view.
	if installedGraph != r.graph {
		transactionNode := r.graph.Packages[dep.CP()]
		if transactionNode == nil {
			return false
		}
		for _, vi := range transactionNode.Versions {
			if !r.packageVersionScheduledInDomain(transactionNode, vi, domain) {
				continue
			}
			if versionAtomMatches(transactionNode.Atom, dep, vi, r.candidateUseFlags(transactionNode, vi)) {
				return true
			}
		}
	}
	return false
}

func domainRemovals(removed map[string]bool, domain DependencyDomain) map[string]bool {
	domain = normalizedActionDomain(domain)
	result := make(map[string]bool)
	prefix := string(domain) + "\x00"
	for key := range removed {
		if domain == DomainROOT {
			// ROOT action keys may contain the version/repository separator. Only
			// explicit non-ROOT domain prefixes exclude an action from ROOT.
			if !strings.HasPrefix(key, string(DomainBROOT)+"\x00") &&
				!strings.HasPrefix(key, string(DomainSYSROOT)+"\x00") {
				result[key] = true
			}
			continue
		}
		if strings.HasPrefix(key, prefix) {
			result[strings.TrimPrefix(key, prefix)] = true
		}
	}
	return result
}

func (r *resolver) finalVersionsInDomain(node *PkgNode, removed map[string]bool, overlayPlanned bool, domain DependencyDomain) []*VersionInfo {
	if node == nil {
		return nil
	}
	bySlot := make(map[string]*VersionInfo)
	for _, vi := range node.Versions {
		if vi != nil && vi.Installed && !removed[versionActionKey(node.Atom.CP(), vi)] {
			bySlot[vi.Slot] = vi
		}
	}
	if overlayPlanned {
		for _, vi := range node.Versions {
			if vi != nil && r.packageVersionScheduledInDomain(node, vi, domain) {
				bySlot[vi.Slot] = vi
			}
		}
	}
	result := make([]*VersionInfo, 0, len(bySlot))
	for _, vi := range bySlot {
		result = append(result, vi)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Slot != result[j].Slot {
			return result[i].Slot < result[j].Slot
		}
		return result[i].Version.Compare(result[j].Version) < 0
	})
	return result
}

// providerConstraint applies a virtual's version, slot and USE requirements to
// a concrete provider. PROVIDE uses the provider package's version; changing
// only the CP lets the ordinary atom matcher enforce all remaining semantics.
func providerConstraint(dep *atom.Atom, provider *PkgNode) *atom.Atom {
	if dep == nil || provider == nil || provider.Atom == nil {
		return dep
	}
	translated := *dep
	translated.Category = provider.Atom.Category
	translated.Package = provider.Atom.Package
	translated.UseFlags = append([]atom.UseFlag(nil), dep.UseFlags...)
	return &translated
}

func (r *resolver) anyOptionChanged(options []*DepAtom, changed map[string]bool) bool {
	for _, option := range options {
		if option != nil && option.Atom != nil && changed[option.Atom.CP()] {
			return true
		}
	}
	return false
}

func (r *resolver) useFlagEnabled(node *PkgNode, flag string) bool {
	return r.effectiveNodeUseFlags(node)[flag]
}

// conditionsEnabled evaluates the comma-separated conjunction emitted by
// depstring.CollectMeta for nested USE conditionals. A leading ! negates one
// condition, matching EAPI dependency syntax.
func conditionsEnabled(flags map[string]bool, condition string) bool {
	for _, raw := range strings.Split(condition, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		negated := strings.HasPrefix(raw, "!")
		name := strings.TrimPrefix(raw, "!")
		enabled := flags[name]
		if negated {
			enabled = !enabled
		}
		if !enabled {
			return false
		}
	}
	return true
}

func anyOfRequiresActiveOption(rawEAPI string) bool {
	eapi, err := strconv.Atoi(rawEAPI)
	return err == nil && eapi >= 7
}

func (r *resolver) effectiveNodeUseFlags(node *PkgNode) map[string]bool {
	flags := make(map[string]bool)
	if node == nil {
		return flags
	}
	cacheKey := node.Atom.CP()
	if r.effectiveNodeUseCache == nil {
		r.effectiveNodeUseCache = make(map[string]map[string]bool)
	}
	if cached, found := r.effectiveNodeUseCache[cacheKey]; found {
		return cached
	}
	best := node.GetBestVersion()
	if best != nil {
		for name, enabled := range best.UseFlags {
			flags[name] = enabled
		}
	}
	if r.portageConfig != nil {
		cpv, slot, repository := node.Atom.CP(), "", ""
		if best != nil {
			slot, repository = best.Slot, best.Repository
			if best.Version != nil {
				cpv += "-" + best.Version.Raw
			}
		}
		for name, enabled := range r.getUseFlagsFor(cpv, slot, repository) {
			flags[name] = enabled
		}
	}
	for _, target := range r.targetAtoms {
		if target.CP() == node.Atom.CP() {
			for _, flag := range target.UseFlags {
				flags[flag.Name] = flag.Enabled
			}
		}
	}
	r.effectiveNodeUseCache[cacheKey] = flags
	return flags
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
	emitted := make([]bool, len(actions))

	for i := 0; i < len(actions); i++ {
		if inDegree[i] == 0 {
			queue = append(queue, i)
		}
	}

	for len(queue) > 0 {
		idx := queue[0]
		queue = queue[1:]
		if emitted[idx] {
			continue
		}
		emitted[idx] = true
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
		for i, a := range actions {
			if !emitted[i] {
				emitted[i] = true
				sorted = append(sorted, a)
			}
		}
	}

	return sorted
}

// sortPlannedActions orders the transaction using dependency metadata from the
// exact selected version. Package-level graph edges are only a compatibility
// fallback and are not authoritative for a real repository plan.
func (r *resolver) sortPlannedActions(actions []PkgAction) []PkgAction {
	if len(actions) <= 1 {
		return actions
	}
	out := r.plannedOrderGraph(actions)
	componentOf, components := stronglyConnectedPlanComponents(out, len(actions))
	componentOut := make(map[int][]int)
	componentInDegree := make([]int, len(components))
	seenComponentEdge := make(map[[2]int]bool)
	for before, afters := range out {
		for _, after := range afters {
			from, to := componentOf[before], componentOf[after]
			pair := [2]int{from, to}
			if from == to || seenComponentEdge[pair] {
				continue
			}
			seenComponentEdge[pair] = true
			componentOut[from] = append(componentOut[from], to)
			componentInDegree[to]++
		}
	}
	componentKey := make([]int, len(components))
	for component := range components {
		sort.Ints(components[component])
		componentKey[component] = components[component][0]
	}
	queue := make([]int, 0, len(components))
	for component, degree := range componentInDegree {
		if degree == 0 {
			queue = append(queue, component)
		}
	}
	sort.Slice(queue, func(i, j int) bool { return componentKey[queue[i]] < componentKey[queue[j]] })
	result := make([]PkgAction, 0, len(actions))
	for len(queue) > 0 {
		component := queue[0]
		queue = queue[1:]
		for _, actionIndex := range components[component] {
			result = append(result, actions[actionIndex])
		}
		for _, next := range componentOut[component] {
			componentInDegree[next]--
			if componentInDegree[next] == 0 {
				queue = append(queue, next)
				sort.Slice(queue, func(i, j int) bool { return componentKey[queue[i]] < componentKey[queue[j]] })
			}
		}
	}
	return result
}

func (r *resolver) plannedOrderGraph(actions []PkgAction) map[int][]int {
	index := make(map[string][]int, len(actions))
	for i := range actions {
		if actions[i].Atom != nil {
			index[actions[i].Atom.CP()] = append(index[actions[i].Atom.CP()], i)
		}
	}
	out := make(map[int][]int)
	seen := make(map[[2]int]bool)
	add := func(before, after int) {
		pair := [2]int{before, after}
		if before == after || seen[pair] {
			return
		}
		seen[pair] = true
		out[before] = append(out[before], after)
	}
	for parentIndex := range actions {
		action := &actions[parentIndex]
		vi, node := r.versionForAction(action)
		if vi == nil || node == nil {
			continue
		}
		edges, err := r.dependenciesForVersion(node, vi)
		if err != nil {
			continue
		}
		for _, edge := range edges {
			parentFlags := edge.UseFlags
			if parentFlags == nil {
				parentFlags = action.UseFlags
			}
			if edge.Block || (edge.UseCond != "" && !conditionsEnabled(parentFlags, edge.UseCond)) {
				continue
			}
			var dependencies []*atom.Atom
			if len(edge.AnyOf) > 0 {
				for _, option := range edge.AnyOf {
					if option != nil && option.Atom != nil && (option.UseCond == "" || conditionsEnabled(parentFlags, option.UseCond)) {
						dependencies = append(dependencies, option.Atom)
					}
				}
			} else if edge.DepAtom != nil {
				dependencies = append(dependencies, edge.DepAtom)
			}
			for _, dependency := range dependencies {
				for _, depIndex := range plannedDependencyIndices(actions, index, dependency, r.effectiveDomain(edge.Domain)) {
					if edge.Type == DepTypePost {
						add(parentIndex, depIndex)
					} else {
						add(depIndex, parentIndex)
					}
				}
			}
		}
	}
	return out
}

func stronglyConnectedPlanComponents(out map[int][]int, count int) ([]int, [][]int) {
	indices := make([]int, count)
	lowLink := make([]int, count)
	onStack := make([]bool, count)
	for i := range indices {
		indices[i] = -1
	}
	stack := make([]int, 0, count)
	nextIndex := 0
	componentOf := make([]int, count)
	var components [][]int
	var visit func(int)
	visit = func(node int) {
		indices[node], lowLink[node] = nextIndex, nextIndex
		nextIndex++
		stack = append(stack, node)
		onStack[node] = true
		for _, next := range out[node] {
			if indices[next] == -1 {
				visit(next)
				if lowLink[next] < lowLink[node] {
					lowLink[node] = lowLink[next]
				}
			} else if onStack[next] && indices[next] < lowLink[node] {
				lowLink[node] = indices[next]
			}
		}
		if lowLink[node] != indices[node] {
			return
		}
		component := len(components)
		var members []int
		for {
			last := len(stack) - 1
			member := stack[last]
			stack = stack[:last]
			onStack[member] = false
			componentOf[member] = component
			members = append(members, member)
			if member == node {
				break
			}
		}
		components = append(components, members)
	}
	for node := 0; node < count; node++ {
		if indices[node] == -1 {
			visit(node)
		}
	}
	return componentOf, components
}

func (r *resolver) versionForAction(action *PkgAction) (*VersionInfo, *PkgNode) {
	if action == nil || action.Atom == nil {
		return nil, nil
	}
	node := r.graph.Packages[action.Atom.CP()]
	if node == nil {
		return nil, nil
	}
	for _, vi := range node.Versions {
		if vi == nil || vi.Version == nil || action.Atom.Version == nil {
			continue
		}
		if vi.Version.Raw == action.Atom.Version.Raw && (action.Slot == "" || vi.Slot == action.Slot) &&
			(action.Repository == "" || vi.Repository == action.Repository) {
			return vi, node
		}
	}
	return nil, node
}

func plannedDependencyIndices(actions []PkgAction, index map[string][]int, dependency *atom.Atom, domain DependencyDomain) []int {
	if dependency == nil {
		return nil
	}
	var result []int
	for _, candidate := range index[dependency.CP()] {
		action := &actions[candidate]
		if action.Atom == nil {
			continue
		}
		if dependency.Repo != "" && action.Repository != dependency.Repo {
			continue
		}
		if normalizedActionDomain(action.Domain) != normalizedActionDomain(domain) {
			continue
		}
		if atomMatches(action.Atom, dependency, action.Slot, action.Subslot, action.UseFlags, action.Atom.Version) {
			result = append(result, candidate)
		}
	}
	return result
}

func (r *resolver) validatePlanOrder(actions []PkgAction) {
	componentOf, _ := stronglyConnectedPlanComponents(r.plannedOrderGraph(actions), len(actions))
	positions := make(map[string][]int, len(actions))
	for i := range actions {
		if actions[i].Atom != nil {
			positions[actions[i].Atom.CP()] = append(positions[actions[i].Atom.CP()], i)
		}
	}
	for parentIndex := range actions {
		vi, node := r.versionForAction(&actions[parentIndex])
		if vi == nil || node == nil {
			continue
		}
		edges, err := r.dependenciesForVersion(node, vi)
		if err != nil {
			continue
		}
		for _, edge := range edges {
			parentFlags := edge.UseFlags
			if parentFlags == nil {
				parentFlags = actions[parentIndex].UseFlags
			}
			if edge.Block || (edge.UseCond != "" && !conditionsEnabled(parentFlags, edge.UseCond)) {
				continue
			}
			var dependencies []*atom.Atom
			if len(edge.AnyOf) > 0 {
				for _, option := range edge.AnyOf {
					if option != nil && option.Atom != nil && (option.UseCond == "" || conditionsEnabled(parentFlags, option.UseCond)) {
						dependencies = append(dependencies, option.Atom)
					}
				}
			} else if edge.DepAtom != nil {
				dependencies = append(dependencies, edge.DepAtom)
			}
			for _, dependency := range dependencies {
				for _, depIndex := range plannedDependencyIndices(actions, positions, dependency, r.effectiveDomain(edge.Domain)) {
					if componentOf[depIndex] == componentOf[parentIndex] {
						continue
					}
					invalid := edge.Type == DepTypePost && depIndex < parentIndex
					invalid = invalid || (edge.Type != DepTypePost && depIndex > parentIndex)
					if invalid {
						r.conflicts = append(r.conflicts, fmt.Sprintf("plan ordering: dependency class %d package %s is scheduled on the wrong side of %s", edge.Type, dependency.CP(), node.Atom.CP()))
					}
				}
			}
		}
	}
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
	if constraint.Version != nil && constraint.Version.Raw != "" {
		if pkgVersion == nil {
			return false
		}
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
	if constraint.Subslot != "" {
		effectiveSubslot := subslot
		if effectiveSubslot == "" {
			effectiveSubslot = slot
		}
		if effectiveSubslot != constraint.Subslot {
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
			if f.Default != nil {
				enabled, ok = *f.Default, true
			}
		}
		if !ok {
			// Without an explicit (+)/(-) default, a flag absent from the
			// candidate's IUSE cannot satisfy either polarity of a USE dep.
			return false
		}
		if enabled != f.Enabled {
			return false
		}
	}

	return true
}

func versionAtomMatches(packageAtom, constraint *atom.Atom, version *VersionInfo, useFlags map[string]bool) bool {
	if version == nil || constraint == nil {
		return false
	}
	if constraint.Repo != "" && version.Repository != constraint.Repo {
		return false
	}
	return atomMatches(packageAtom, constraint, version.Slot, version.Subslot, useFlags, version.Version)
}

func resolveUseDependencies(input *atom.Atom, parentUse map[string]bool) *atom.Atom {
	if input == nil || len(input.UseFlags) == 0 {
		return input
	}
	resolved := *input
	resolved.UseFlags = make([]atom.UseFlag, 0, len(input.UseFlags))
	for _, flag := range input.UseFlags {
		if flag.Conditional {
			parentEnabled := parentUse[flag.Name]
			applies := parentEnabled
			if flag.Negated {
				applies = !parentEnabled
			}
			if !applies {
				continue
			}
			flag.Enabled = !flag.Negated
			flag.Conditional, flag.Negated = false, false
		}
		if flag.Equal {
			flag.Enabled = parentUse[flag.Name]
			if flag.Negated {
				flag.Enabled = !flag.Enabled
			}
			flag.Equal, flag.Negated = false, false
		}
		resolved.UseFlags = append(resolved.UseFlags, flag)
	}
	return &resolved
}

// tildeMatch checks if version v satisfies the ~ operator constraint c.
// Gentoo's ~ operator ignores only the package revision: ~pkg-1.2 matches
// pkg-1.2 and pkg-1.2-rN, but it must not admit pkg-1.2.1.
func tildeMatch(v, c *atom.Version) bool {
	if v == nil || c == nil {
		return false
	}
	vBase := *v
	cBase := *c
	vBase.Revision = -1
	cBase.Revision = -1
	return vBase.Compare(&cBase) == 0
}

// globMatch implements Portage's =* normalized literal-prefix rule. Matching
// stops on a version-part boundary, so 1* matches 1.0 but not 10, and textual
// component distinctions such as 1.2 versus 1.02 remain significant.
func globMatch(v, c *atom.Version) bool {
	if v == nil || c == nil {
		return false
	}
	candidate := normalizeGlobVersion(v.Raw)
	prefix := strings.TrimSuffix(normalizeGlobVersion(c.Raw), "*")
	if prefix == "" || !strings.HasPrefix(candidate, prefix) {
		return false
	}
	if len(candidate) == len(prefix) {
		return true
	}
	next := candidate[len(prefix)]
	last := prefix[len(prefix)-1]
	return next == '.' || next == '_' || next == '-' || isASCIIDigit(last) != isASCIIDigit(next)
}

func normalizeGlobVersion(raw string) string {
	star := strings.HasSuffix(raw, "*")
	base := strings.TrimSuffix(raw, "*")
	i := 0
	for i < len(base) && base[i] == '0' {
		i++
	}
	base = base[i:]
	if base == "" || !isASCIIDigit(base[0]) {
		base = "0" + base
	}
	if star {
		base += "*"
	}
	return base
}

func isASCIIDigit(value byte) bool {
	return value >= '0' && value <= '9'
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

// versionedConstraintAtom pins a selected version without discarding the slot,
// repository or USE requirements that caused it to be selected.
func versionedConstraintAtom(constraint *atom.Atom, vi *VersionInfo) *atom.Atom {
	if constraint == nil {
		return bestVersionAtom(nil, vi)
	}
	a := *constraint
	a.Op = atom.OpEq
	a.Version = vi.Version
	a.Slot = vi.Slot
	a.Subslot = vi.Subslot
	a.UseFlags = append([]atom.UseFlag(nil), constraint.UseFlags...)
	return &a
}

func versionedDependencyAtom(node *PkgNode, constraint *atom.Atom, vi *VersionInfo) *atom.Atom {
	if node != nil && node.Atom != nil && constraint != nil && node.Atom.CP() != constraint.CP() {
		a := bestVersionAtom(node.Atom, vi)
		a.UseFlags = append([]atom.UseFlag(nil), constraint.UseFlags...)
		return a
	}
	return versionedConstraintAtom(constraint, vi)
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

// newUseChanged compares effective values over the union of the old and new
// declared IUSE domains. VDB USE also contains implicit profile flags (ARCH,
// USE_EXPAND_IMPLICIT, and similar values) that are valid dependency state but
// are not IUSE additions/removals and therefore must not cause --newuse rebuilds.
func newUseChanged(installed, candidate *VersionInfo, candidateFlags map[string]bool) bool {
	if installed == nil || candidate == nil {
		return useFlagsChanged(installedFlags(installed), candidateFlags)
	}
	oldDomain := installed.InstalledIUseFlags
	if oldDomain == nil {
		// Synthetic and legacy snapshots predate the separate IUSE domain. Their
		// installed map intentionally retains the old test/fixture semantics.
		oldDomain = installedFlags(installed)
	}
	newDomain := candidate.UseFlags
	oldFlags := installedFlags(installed)
	for flag := range oldDomain {
		if _, exists := newDomain[flag]; !exists || oldFlags[flag] != candidateFlags[flag] {
			return true
		}
	}
	for flag := range newDomain {
		if _, exists := oldDomain[flag]; !exists || oldFlags[flag] != candidateFlags[flag] {
			return true
		}
	}
	return false
}

func effectiveUseChanged(old, new map[string]bool) bool {
	for flag, enabled := range old {
		if enabled && !new[flag] {
			return true
		}
	}
	for flag, enabled := range new {
		if enabled && !old[flag] {
			return true
		}
	}
	return false
}

func installedFlags(version *VersionInfo) map[string]bool {
	if version == nil {
		return nil
	}
	if version.InstalledUseFlags != nil {
		return version.InstalledUseFlags
	}
	return version.UseFlags
}

func (r *resolver) getUseFlags(cp string) map[string]bool {
	return r.getUseFlagsFor(cp, "", "")
}

func (r *resolver) getUseFlagsFor(cpv, slot, repo string) map[string]bool {
	if r.portageConfig == nil {
		return make(map[string]bool)
	}
	return r.portageConfig.EffectiveUseFor(cpv, slot, repo)
}

func (r *resolver) isPackageProvided(requirement *atom.Atom) bool {
	if r.portageConfig == nil || requirement == nil {
		return false
	}
	for _, entry := range r.portageConfig.PackageProvided {
		provided, err := atom.Parse(entry)
		if err != nil {
			continue
		}
		if provided.CP() != requirement.CP() || provided.Version == nil {
			continue
		}
		cpv := provided.CP() + "-" + provided.Version.Raw
		if portage.PackageAtomMatches(requirement.String(), cpv, provided.Slot, provided.Repo) {
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
		return false
	}

	for _, ak := range r.portageConfig.ACCEPT_KEYWORDS {
		if ak == "**" {
			return true
		}
	}

	arch := r.portageConfig.MakeConf["ARCH"]
	if arch == "" {
		arch = gentooRuntimeArch(runtime.GOARCH)
	}

	for _, kw := range strings.Fields(keywords) {
		if strings.HasPrefix(kw, "-") {
			continue
		}
		if kw == arch {
			return true
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

func (r *resolver) versionKeywordAccepted(node *PkgNode, vi *VersionInfo) bool {
	if r.keywordCache == nil {
		r.keywordCache = make(map[string]bool)
	}
	cacheKey := r.versionKey(node, vi)
	if accepted, found := r.keywordCache[cacheKey]; found {
		return accepted
	}
	accepted := r.versionKeywordAcceptedUncached(node, vi)
	r.keywordCache[cacheKey] = accepted
	return accepted
}

func (r *resolver) versionKeywordAcceptedUncached(node *PkgNode, vi *VersionInfo) bool {
	if r.portageConfig == nil || node == nil || vi == nil || vi.Version == nil {
		return r.keywordAccepted(vi.Keywords)
	}
	cpv := node.Atom.CP() + "-" + vi.Version.Raw
	arch := r.portageConfig.MakeConf["ARCH"]
	if arch == "" {
		arch = gentooRuntimeArch(runtime.GOARCH)
	}
	return r.portageConfig.KeywordAcceptedFor(cpv, policySlot(vi), vi.Repository, vi.Keywords, arch)
}

func applyKeywordChanges(previous, changes []string) []string {
	state := append([]string(nil), previous...)
	for _, change := range changes {
		if change == "-*" {
			state = nil
			continue
		}
		remove := strings.TrimPrefix(change, "-")
		filtered := state[:0]
		for _, current := range state {
			if current != remove {
				filtered = append(filtered, current)
			}
		}
		state = filtered
		if !strings.HasPrefix(change, "-") {
			state = append(state, change)
		}
	}
	return state
}

func keywordSetAccepts(keywords string, accepted []string, arch string) bool {
	for _, allow := range accepted {
		if allow == "**" {
			return true
		}
		for _, keyword := range strings.Fields(keywords) {
			if strings.HasPrefix(keyword, "-") {
				continue
			}
			if allow == keyword || allow == "*" && !strings.HasPrefix(keyword, "~") ||
				allow == "~*" && strings.HasPrefix(keyword, "~") ||
				allow == arch && keyword == arch {
				return true
			}
		}
	}
	return false
}

func gentooRuntimeArch(goarch string) string {
	if goarch == "386" {
		return "x86"
	}
	return goarch
}

type candidateCacheEntry struct {
	version  *VersionInfo
	versions []*VersionInfo
}

func (r *resolver) findMatchingVersion(node *PkgNode, constraint *atom.Atom) *VersionInfo {
	if node == nil || constraint == nil {
		return nil
	}
	cacheKey := "one\x00" + node.Atom.CP() + "\x00" + constraint.String() + "\x00" + strconv.FormatUint(r.useOverrideGeneration, 10)
	if r.candidateCache == nil {
		r.candidateCache = make(map[string]candidateCacheEntry)
	}
	if cached, found := r.candidateCache[cacheKey]; found {
		r.metrics.CandidateCacheHits++
		return cached.version
	}
	r.metrics.CandidateCacheMisses++
	var best *VersionInfo
	for _, vi := range node.Versions {
		if r.checkContext() != nil {
			return nil
		}
		if vi == nil {
			continue
		}
		if !versionAtomMatches(node.Atom, constraint, vi, r.candidateUseFlags(node, vi)) {
			continue
		}
		if r.versionMaskStatus(node, vi).Masked {
			continue
		}
		if !vi.Installed && !r.versionKeywordAccepted(node, vi) {
			continue
		}
		if betterVersionCandidate(vi, best) {
			best = vi
		}
	}
	r.candidateCache[cacheKey] = candidateCacheEntry{version: best}
	return best
}

func (r *resolver) findVersionSatisfyingAll(node *PkgNode, slot string, constraints []*atom.Atom) *VersionInfo {
	choices := r.versionsSatisfyingAll(node, slot, constraints)
	if len(choices) == 0 {
		return nil
	}
	return choices[0]
}

func (r *resolver) versionsSatisfyingAll(node *PkgNode, slot string, constraints []*atom.Atom) []*VersionInfo {
	if node == nil {
		return nil
	}
	keyParts := []string{"all", node.Atom.CP(), slot, strconv.FormatUint(r.useOverrideGeneration, 10)}
	for _, constraint := range constraints {
		keyParts = append(keyParts, constraint.String())
	}
	cacheKey := strings.Join(keyParts, "\x00")
	if r.candidateCache == nil {
		r.candidateCache = make(map[string]candidateCacheEntry)
	}
	if cached, found := r.candidateCache[cacheKey]; found {
		r.metrics.CandidateCacheHits++
		return cached.versions
	}
	r.metrics.CandidateCacheMisses++
	var result []*VersionInfo
	for _, vi := range node.Versions {
		if r.checkContext() != nil {
			return nil
		}
		if vi == nil || vi.Slot != slot || (!vi.Installed && !vi.Available) || r.versionMaskStatus(node, vi).Masked {
			continue
		}
		if !vi.Installed && !r.versionKeywordAccepted(node, vi) {
			continue
		}
		matches := true
		for _, constraint := range constraints {
			if !versionAtomMatches(node.Atom, constraint, vi, r.candidateUseFlags(node, vi)) {
				matches = false
				break
			}
		}
		if matches {
			result = append(result, vi)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return betterVersionCandidate(result[i], result[j]) })
	r.candidateCache[cacheKey] = candidateCacheEntry{versions: result}
	return result
}

func (r *resolver) slotConflictCandidates(node *PkgNode, slot string, constraints []*atom.Atom) []ConflictCandidate {
	if node == nil || node.Atom == nil {
		return nil
	}
	var result []ConflictCandidate
	appendState := func(vi *VersionInfo, state string, flags map[string]bool, visible bool, visibility string) {
		cpv := node.Atom.CP()
		if vi.Version != nil {
			cpv += "-" + vi.Version.Raw
		}
		candidate := ConflictCandidate{CPV: cpv, State: state, Repository: vi.Repository, Visible: visible, Visibility: visibility}
		for _, constraint := range constraints {
			if versionAtomMatches(node.Atom, constraint, vi, flags) {
				candidate.Satisfies = append(candidate.Satisfies, constraint.String())
			} else {
				candidate.Rejects = append(candidate.Rejects, constraint.String())
			}
		}
		result = append(result, candidate)
	}
	for _, vi := range node.Versions {
		if vi == nil || vi.Slot != slot {
			continue
		}
		if vi.Installed {
			appendState(vi, "installed", installedFlags(vi), true, "")
		}
		if vi.Available {
			visible, why := true, ""
			if status := r.versionMaskStatus(node, vi); status.Masked {
				visible, why = false, "masked by "+status.Source
			} else if !r.versionKeywordAccepted(node, vi) {
				visible, why = false, "keywords not accepted"
			}
			appendState(vi, "available", r.candidateUseFlags(node, vi), visible, why)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CPV != result[j].CPV {
			return result[i].CPV < result[j].CPV
		}
		return result[i].State < result[j].State
	})
	return result
}

func (r *resolver) candidateUseFlags(node *PkgNode, vi *VersionInfo) map[string]bool {
	if vi == nil {
		return map[string]bool{}
	}
	if node == nil {
		return cloneBoolMap(vi.UseFlags)
	}
	if r.baseUseByVersion == nil {
		r.baseUseByVersion = make(map[*VersionInfo]map[string]bool)
	}
	base, found := r.baseUseByVersion[vi]
	if !found {
		base = cloneBoolMap(vi.UseFlags)
		cpv := node.Atom.CP()
		if vi.Version != nil {
			cpv += "-" + vi.Version.Raw
		}
		if r.portageConfig != nil {
			arch := r.portageConfig.MakeConf["ARCH"]
			if arch == "" {
				arch = gentooRuntimeArch(runtime.GOARCH)
			}
			stable := false
			for _, keyword := range strings.Fields(vi.Keywords) {
				if keyword == arch {
					stable = true
					break
				}
			}
			for name, enabled := range r.portageConfig.EffectiveUseForStability(cpv, policySlot(vi), vi.Repository, stable) {
				// Repository versions carry the declared IUSE as keys. Project
				// configuration onto that package-local domain instead of leaking
				// every global USE/USE_EXPAND flag into actions and dependency
				// evaluation. A nil map is retained for synthetic compatibility
				// graphs whose conditional flags are not backed by metadata.
				if vi.UseFlags == nil {
					base[name] = enabled
				} else if _, declared := vi.UseFlags[name]; declared || implicitUseExpandFlag(r.portageConfig, name) {
					base[name] = enabled
				}
			}
		}
		r.baseUseByVersion[vi] = base
	}
	key := r.versionKey(node, vi)
	overrides := r.useOverrides[key]
	if len(overrides) == 0 {
		// Base candidate USE maps are immutable after construction. Returning the
		// shared map avoids cloning the complete IUSE domain for every read-only
		// constraint check; callers that apply a hypothetical override clone below.
		return base
	}
	flags := cloneBoolMap(base)
	for name, enabled := range overrides {
		flags[name] = enabled
	}
	return flags
}

func implicitUseExpandFlag(config *portage.Config, flag string) bool {
	if config == nil {
		return false
	}
	for _, variable := range config.UseExpandImplicit {
		prefix := strings.ToLower(strings.TrimSpace(variable)) + "_"
		if prefix != "_" && strings.HasPrefix(flag, prefix) && len(flag) > len(prefix) {
			return true
		}
	}
	return false
}

func versionKey(node *PkgNode, vi *VersionInfo) string {
	if node == nil || node.Atom == nil || vi == nil {
		return ""
	}
	key := node.Atom.CP() + "|" + vi.Slot
	if vi.Version != nil {
		key += "|" + vi.Version.Raw
	}
	key += "|" + vi.Repository
	return key
}

func (r *resolver) versionKey(node *PkgNode, vi *VersionInfo) string {
	if vi == nil {
		return ""
	}
	if key, found := r.versionKeyCache[vi]; found {
		return key
	}
	if r.versionKeyCache == nil {
		r.versionKeyCache = make(map[*VersionInfo]string)
	}
	key := versionKey(node, vi)
	r.versionKeyCache[vi] = key
	return key
}

func policyContainsFlag(flags []string, name string) bool {
	for _, flag := range flags {
		if strings.TrimPrefix(flag, "-") == name {
			return true
		}
	}
	return false
}

// findMatchingVersionWithUseChanges keeps a visible candidate in the
// hypothetical plan when its only mismatch is a mutable dependency USE flag.
// The plan remains blocked by an actionable conflict until the user accepts
// the package.use change.
func (r *resolver) findMatchingVersionWithUseChanges(node *PkgNode, constraint *atom.Atom) *VersionInfo {
	best, bestChanges := r.matchingVersionUseChanges(node, constraint)
	if best == nil {
		return nil
	}
	key := r.versionKey(node, best)
	var rendered []string
	for flag, enabled := range bestChanges {
		r.setUseOverride(key, flag, enabled)
		if enabled {
			rendered = append(rendered, flag)
		} else {
			rendered = append(rendered, "-"+flag)
		}
	}
	depKey := node.Atom.CP()
	if best.Version != nil {
		depKey += "-" + best.Version.Raw + ":" + best.Slot + "::" + best.Repository
	}
	r.deleteSeenDep(depKey)
	if best.Version != nil {
		actionKey := versionActionKey(node.Atom.CP(), best)
		if existing := r.toInstall[actionKey]; existing != nil {
			updated := *existing
			updated.UseFlags = r.candidateUseFlags(node, best)
			r.setInstall(actionKey, &updated)
		}
	}
	sort.Strings(rendered)
	changeKey := node.Atom.CP() + ":" + strings.Join(rendered, ",")
	if !r.useChangeSeen[changeKey] {
		r.setUseChangeSeen(changeKey, true)
		r.conflicts = append(r.conflicts, fmt.Sprintf("USE changes are necessary to proceed: %s %s", node.Atom.CP(), strings.Join(rendered, " ")))
	}
	return best
}

// matchingVersionUseChanges previews a mutable USE repair without changing
// resolver state. Any-of selection uses it to compare alternatives before the
// chosen branch transaction commits its package.use requirement.
func (r *resolver) matchingVersionUseChanges(node *PkgNode, constraint *atom.Atom) (*VersionInfo, map[string]bool) {
	if node == nil || constraint == nil || len(constraint.UseFlags) == 0 {
		return nil, nil
	}
	withoutUse := *constraint
	withoutUse.UseFlags = nil
	var best *VersionInfo
	var bestChanges map[string]bool
	for _, vi := range node.Versions {
		if vi == nil || !vi.Available || !versionAtomMatches(node.Atom, &withoutUse, vi, r.candidateUseFlags(node, vi)) ||
			r.versionMaskStatus(node, vi).Masked || !r.versionKeywordAccepted(node, vi) {
			continue
		}
		current := r.candidateUseFlags(node, vi)
		changes := make(map[string]bool)
		mutable := true
		cpv := node.Atom.CP()
		if vi.Version != nil {
			cpv += "-" + vi.Version.Raw
		}
		for _, required := range constraint.UseFlags {
			if current[required.Name] == required.Enabled {
				continue
			}
			if _, exists := vi.UseFlags[required.Name]; !exists {
				mutable = false
				break
			}
			if r.portageConfig != nil {
				masked := append([]string(nil), r.portageConfig.UseMask...)
				masked = append(masked, r.portageConfig.PackageUseMaskFor(cpv, policySlot(vi), vi.Repository)...)
				forced := append([]string(nil), r.portageConfig.UseForce...)
				forced = append(forced, r.portageConfig.PackageUseForceFor(cpv, policySlot(vi), vi.Repository)...)
				if (required.Enabled && policyContainsFlag(masked, required.Name)) || (!required.Enabled && policyContainsFlag(forced, required.Name)) {
					mutable = false
					break
				}
			}
			changes[required.Name] = required.Enabled
		}
		if !mutable || len(changes) == 0 {
			continue
		}
		if betterVersionCandidate(vi, best) {
			best, bestChanges = vi, changes
		}
	}
	if best == nil {
		return nil, nil
	}
	return best, bestChanges
}

func (r *resolver) versionMaskStatus(node *PkgNode, vi *VersionInfo) portage.MaskStatus {
	if r.portageConfig == nil || node == nil || vi == nil || vi.Version == nil {
		return portage.MaskStatus{}
	}
	if r.maskCache == nil {
		r.maskCache = make(map[string]portage.MaskStatus)
	}
	key := r.versionKey(node, vi)
	if status, found := r.maskCache[key]; found {
		return status
	}
	status := r.portageConfig.PackageMaskStatus(node.Atom.CP()+"-"+vi.Version.Raw, policySlot(vi), vi.Repository)
	r.maskCache[key] = status
	return status
}

func (r *resolver) matchingMaskStatuses(node *PkgNode, constraint *atom.Atom) []string {
	var result []string
	for _, vi := range node.Versions {
		if vi == nil || !versionAtomMatches(node.Atom, constraint, vi, r.candidateUseFlags(node, vi)) {
			continue
		}
		status := r.versionMaskStatus(node, vi)
		if status.Masked && vi.Version != nil {
			result = append(result, node.Atom.CP()+"-"+vi.Version.Raw+" by "+status.Source+" atom "+status.Atom)
		}
	}
	sort.Strings(result)
	return result
}

func (r *resolver) buildResult() (*ResolveResult, error) {
	r.snapshotAllocations()
	install := mapToSlice(r.toInstall)
	if !r.config.UnsortedDisplay {
		install = SortByDeps(install, r.graph)
	}
	verification := VerificationIncomplete
	if r.config.NoDeps {
		verification = VerificationSkippedNoDeps
	}
	return &ResolveResult{
		Install:         install,
		Uninstall:       mapToSlice(r.toUninstall),
		Conflicts:       r.conflicts,
		Warnings:        r.warnings,
		DecisionHistory: append([]BacktrackDecision(nil), r.decisionHistory...),
		BacktrackLevel:  r.config.Backtrack - r.backtrackRemaining,
		Metrics:         r.metrics,
		ConflictDetails: r.conflictDetails,
		Verification:    verification,
		retryChoices:    append([]replayDecision(nil), r.replayChoices...),
	}, nil
}

func mapToSlice(m map[string]*PkgAction) []PkgAction {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	selected := make(map[string]*PkgAction, len(keys))
	order := make([]string, 0, len(keys))
	for _, key := range keys {
		action := m[key]
		identity := actionIdentity(action)
		if existing, found := selected[identity]; found {
			if existing.Repository == "" && action.Repository != "" {
				selected[identity] = action
			}
			continue
		}
		selected[identity] = action
		order = append(order, identity)
	}
	result := make([]PkgAction, 0, len(order))
	for _, identity := range order {
		result = append(result, *selected[identity])
	}
	return result
}

func actionIdentity(action *PkgAction) string {
	if action == nil || action.Atom == nil {
		return "<nil>"
	}
	version := ""
	if action.Atom.Version != nil {
		version = action.Atom.Version.Raw
	}
	slot := action.Slot
	if slot == "" {
		slot = action.Atom.Slot
	}
	return strings.Join([]string{action.Action, action.Atom.CP(), version, slot, string(normalizedActionDomain(action.Domain))}, "|")
}

// Depclean finds packages that can be safely removed because they are not
// in the world set or the dependency tree of world packages.
func Depclean(g *DepGraph, worldSet *WorldSet) ([]PkgAction, error) {
	if g == nil {
		return nil, fmt.Errorf("resolve: no package list provided for dependency cleanup (internal error)")
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
		return nil, fmt.Errorf("resolve: no package list provided for pruning (internal error)")
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
		return fmt.Errorf("resolve: invalid REQUIRED_USE constraint in package: %w", err)
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
			return fmt.Errorf("resolve: this package requires the USE flag %q to be enabled", n.Atom)
		}
		if !enabled {
			return fmt.Errorf("resolve: this package requires the USE flag %q to be enabled, but it is currently disabled", n.Atom)
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
		return fmt.Errorf("resolve: none of the alternative dependencies could be satisfied — at least one must be installed")

	case *depstring.XorOfGroup:
		satisfied := 0
		for _, child := range n.Children {
			if checkRequiredUseNode(child, useFlags) == nil {
				satisfied++
			}
		}
		if satisfied != 1 {
			return fmt.Errorf("resolve: exactly one of the alternative dependencies must be selected, but %d were found", satisfied)
		}
		return nil

	case *depstring.AtMostOneOfGroup:
		satisfied := 0
		for _, child := range n.Children {
			if checkRequiredUseNode(child, useFlags) == nil {
				satisfied++
			}
		}
		if satisfied > 1 {
			return fmt.Errorf("resolve: at most one of the alternative USE requirements may be selected, but %d were found", satisfied)
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
	explicit := make(map[string]bool)
	for _, change := range acceptLicenses {
		switch {
		case change == "*":
			acceptAll = true
		case change == "-*":
			acceptAll = false
		case change == "@EULA":
			eulaAllowed = true
		case change == "-@EULA":
			eulaAllowed = false
		case strings.HasPrefix(change, "-"):
			explicit[strings.TrimPrefix(change, "-")] = false
		default:
			explicit[change] = true
		}
	}
	for _, candidate := range strings.Fields(license) {
		if accepted, found := explicit[candidate]; found {
			if accepted {
				return true
			}
			continue
		}
		if strings.Contains(strings.ToUpper(candidate), "EULA") {
			if eulaAllowed {
				return true
			}
			continue
		}
		if acceptAll {
			return true
		}
	}
	return false
}

// LicenseExpressionAccepted evaluates the boolean structure of LICENSE.
// Whitespace is conjunction, || groups are alternatives, and USE conditionals
// include their body only when active for the selected package version.
func LicenseExpressionAccepted(expression string, acceptLicenses []string, useFlags map[string]bool) bool {
	if strings.TrimSpace(expression) == "" {
		return true
	}
	node, err := depstring.Parse(expression)
	if err != nil {
		return false
	}
	var accepted func(depstring.DepNode) bool
	accepted = func(current depstring.DepNode) bool {
		switch n := current.(type) {
		case *depstring.AtomDep:
			return LicenseAccepted(n.Atom, acceptLicenses)
		case *depstring.AllOfGroup:
			for _, child := range n.Children {
				if !accepted(child) {
					return false
				}
			}
			return true
		case *depstring.AnyOfGroup:
			for _, child := range n.Children {
				if accepted(child) {
					return true
				}
			}
			return false
		case *depstring.UseConditional:
			flag := strings.TrimPrefix(n.Flag, "!")
			enabled := useFlags[flag]
			if strings.HasPrefix(n.Flag, "!") {
				enabled = !enabled
			}
			if !enabled {
				return true
			}
			for _, child := range n.Children {
				if !accepted(child) {
					return false
				}
			}
			return true
		default:
			return false
		}
	}
	return accepted(node)
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
		return fmt.Errorf("resolve: could not save build progress for --resume: %w", err)
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
		return fmt.Errorf("resolve: could not save build progress for --resume: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil { /* Best effort */
		}
	}()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(state); err != nil {
		return fmt.Errorf("resolve: could not save build progress for --resume: %w", err)
	}
	return nil
}

// LoadResume loads a previous resume state.
func LoadResume(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("resolve: could not load saved build progress: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil { /* Best effort */
		}
	}()
	var state ResumeState
	if err := json.NewDecoder(f).Decode(&state); err != nil {
		return nil, fmt.Errorf("resolve: could not load saved build progress: %w", err)
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
		return fmt.Errorf("resolve: could not update build progress record: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil { /* Best effort */
		}
	}()
	var state ResumeState
	if err := json.NewDecoder(f).Decode(&state); err != nil {
		return fmt.Errorf("resolve: could not update build progress record: %w", err)
	}
	for i := range state.Packages {
		if state.Packages[i].Atom == completedAtom {
			state.Packages[i].Completed = true
			break
		}
	}
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("resolve: could not update build progress record: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("resolve: could not update build progress record: %w", err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(state); err != nil {
		return fmt.Errorf("resolve: could not update build progress record: %w", err)
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
		return fmt.Errorf("resolve: could not skip first item in saved build list: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil { /* Best effort */
		}
	}()
	var state ResumeState
	if err := json.NewDecoder(f).Decode(&state); err != nil {
		return fmt.Errorf("resolve: could not skip first item in saved build list: %w", err)
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
		return fmt.Errorf("resolve: could not skip first item in saved build list: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("resolve: could not skip first item in saved build list: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Changed deps detection (--changed-deps)
// ---------------------------------------------------------------------------

// depsChanged compares the dependency metadata recorded at install time with
// the selected repository candidate across every EAPI dependency class.
func depsChanged(installedVer *VersionInfo, availableVer *VersionInfo) bool {
	if installedVer == nil || availableVer == nil {
		return false
	}
	installed := []string{
		installedVer.InstalledDepend, installedVer.InstalledRdepend,
		installedVer.InstalledBdepend, installedVer.InstalledIdepend, installedVer.InstalledPdepend,
	}
	if installed[0] == "" && installed[1] == "" && installed[2] == "" && installed[3] == "" && installed[4] == "" {
		installed = []string{installedVer.Depend, installedVer.Rdepend, installedVer.Bdepend, installedVer.Idepend, installedVer.Pdepend}
	}
	available := []string{availableVer.Depend, availableVer.Rdepend, availableVer.Bdepend, availableVer.Idepend, availableVer.Pdepend}
	return !slices.Equal(installed, available)
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
	plan := make(map[string]PkgAction, len(actions))
	for _, action := range actions {
		if action.Atom != nil {
			plan[action.Atom.CP()] = action
		}
	}
	seen := make(map[string]bool)
	var buf strings.Builder
	for _, root := range roots {
		formatTreeRecurse(&buf, root, graph, plan, 0, seen)
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

func formatTreeRecurse(buf *strings.Builder, node *PkgNode, graph *DepGraph, plan map[string]PkgAction, depth int, seen map[string]bool) {
	cp := node.Atom.CP()
	action, planned := plan[cp]
	if seen[cp] || !planned {
		return
	}
	seen[cp] = true

	indent := ""
	if depth > 0 {
		indent = strings.Repeat(" ", depth*2)
	}

	actionLabel := "[ebuild  N     ]"
	switch action.Action {
	case "reinstall":
		actionLabel = "[ebuild   R    ]"
	case "update":
		actionLabel = "[ebuild     U  ]"
	}
	atomString := cp
	if action.Atom != nil {
		atomString = action.Atom.String()
	}
	buf.WriteString(fmt.Sprintf("%s%-15s %s\n", indent, actionLabel, atomString))

	for _, edge := range node.Deps {
		if edge.Block {
			continue
		}
		if len(edge.AnyOf) > 0 {
			for _, opt := range edge.AnyOf {
				if opt.Atom != nil {
					depNode := graph.Packages[opt.Atom.CP()]
					if depNode != nil {
						formatTreeRecurse(buf, depNode, graph, plan, depth+1, seen)
					}
				}
			}
			continue
		}
		if edge.To != nil {
			formatTreeRecurse(buf, edge.To, graph, plan, depth+1, seen)
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
			return fmt.Errorf("resolve: could not automatically unmask package: %w", err)
		}
		fileName := strings.ReplaceAll(cp, "/", "_")
		path := filepath.Join(unmaskDir, fileName)
		if err := os.WriteFile(path, []byte(cp+"\n"), 0644); err != nil {
			return fmt.Errorf("resolve: could not write auto-unmask entry to %s: %w", path, err)
		}
	}
	return nil
}

// AutoUseChanges writes dependency-required USE adjustments in Portage's
// package.use format. Directory-style configuration is preferred for a new
// path; an existing regular package.use file is appended without replacing
// user configuration.
func AutoUseChanges(conflicts []string, portageConfigRoot string) error {
	changes := make(map[string]map[string]bool)
	const prefix = "USE changes are necessary to proceed: "
	for _, conflict := range conflicts {
		rest, ok := strings.CutPrefix(conflict, prefix)
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 2 || !strings.Contains(fields[0], "/") {
			continue
		}
		if changes[fields[0]] == nil {
			changes[fields[0]] = make(map[string]bool)
		}
		for _, flag := range fields[1:] {
			changes[fields[0]][flag] = true
		}
	}
	if len(changes) == 0 {
		return nil
	}
	packageUse := filepath.Join(portageConfigRoot, "package.use")
	outputPath := packageUse
	info, err := os.Stat(packageUse)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(packageUse, 0755); err != nil {
			return fmt.Errorf("resolve: create package.use directory: %w", err)
		}
		outputPath = filepath.Join(packageUse, "arise")
	} else if err != nil {
		return fmt.Errorf("resolve: inspect package.use: %w", err)
	} else if info.IsDir() {
		outputPath = filepath.Join(packageUse, "arise")
	}

	var cps []string
	for cp := range changes {
		cps = append(cps, cp)
	}
	sort.Strings(cps)
	var lines []string
	for _, cp := range cps {
		var flags []string
		for flag := range changes[cp] {
			flags = append(flags, flag)
		}
		sort.Strings(flags)
		lines = append(lines, cp+" "+strings.Join(flags, " "))
	}
	existing, _ := os.ReadFile(outputPath)
	var additions []string
	text := string(existing)
	for _, line := range lines {
		if !strings.Contains("\n"+text+"\n", "\n"+line+"\n") {
			additions = append(additions, line)
		}
	}
	if len(additions) == 0 {
		return nil
	}
	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("resolve: write package.use changes: %w", err)
	}
	defer file.Close()
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		if _, err := file.WriteString("\n"); err != nil {
			return err
		}
	}
	_, err = file.WriteString(strings.Join(additions, "\n") + "\n")
	return err
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
			return fmt.Errorf("resolve: could not automatically accept license: %w", err)
		}
		fileName := strings.ReplaceAll(cp, "/", "_")
		path := filepath.Join(licenseDir, fileName)
		if err := os.WriteFile(path, []byte(cp+" "+licenseName+"\n"), 0644); err != nil {
			return fmt.Errorf("resolve: could not write auto-accept-license entry to %s: %w", path, err)
		}
	}
	return nil
}
