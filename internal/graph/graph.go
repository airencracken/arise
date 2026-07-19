package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/depstring"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/resolve"
	"github.com/airencracken/arise/internal/resolversnapshot"
	"github.com/airencracken/arise/internal/vdb"
	"github.com/dgraph-io/badger/v4"
)

type PkgState int

const (
	StateMissing         PkgState = iota
	StateInstalled       PkgState = iota
	StateUpdateAvailable PkgState = iota
	StateOutdatedDeps    PkgState = iota
)

func (s PkgState) String() string {
	switch s {
	case StateMissing:
		return "Missing"
	case StateInstalled:
		return "Installed"
	case StateUpdateAvailable:
		return "UpdateAvailable"
	case StateOutdatedDeps:
		return "OutdatedDeps"
	default:
		return "Unknown"
	}
}

type DepType int

const (
	DepDEPEND  DepType = iota
	DepRDEPEND DepType = iota
	DepBDEPEND DepType = iota
	DepIDEPEND DepType = iota
	DepPDEPEND DepType = iota
)

func (d DepType) String() string {
	switch d {
	case DepDEPEND:
		return "DEPEND"
	case DepRDEPEND:
		return "RDEPEND"
	case DepBDEPEND:
		return "BDEPEND"
	case DepIDEPEND:
		return "IDEPEND"
	case DepPDEPEND:
		return "PDEPEND"
	default:
		return "Unknown"
	}
}

type DepEdge struct {
	Atom        *atom.Atom
	Type        DepType
	Conditional string
	AnyOfGroup  bool
}

type PkgNode struct {
	Atom              *atom.Atom
	Metadata          *metadata.PackageMetadata
	AvailableVersions []*metadata.PackageMetadata
	InstalledVersions []vdb.Package
	Depends           []DepEdge
	RevDepends        []DepEdge
	State             PkgState
}

type DepGraph struct {
	Nodes          map[string]*PkgNode
	InstalledAtoms map[string]*atom.Atom
	AllAtoms       map[string]*atom.Atom
}

func Build(db *badger.DB, repoDir string) (*DepGraph, error) {
	g := &DepGraph{
		Nodes:          make(map[string]*PkgNode),
		InstalledAtoms: make(map[string]*atom.Atom),
		AllAtoms:       make(map[string]*atom.Atom),
	}

	installed := make(map[string]*metadata.PackageMetadata)
	err := ingest.QueryRange(db, "pkg:", func(m *metadata.PackageMetadata) error {
		cp := m.Key()
		installed[cp] = m
		a, err := atom.Parse(cp + "-" + m.Version)
		if err == nil {
			g.InstalledAtoms[m.Category+"/"+m.Package+"-"+m.Version] = a
			g.AllAtoms[m.Category+"/"+m.Package+"-"+m.Version] = a
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("graph: could not read installed package data: %w", err)
	}

	available := make(map[string]*metadata.PackageMetadata)
	md5CacheDir := filepath.Join(repoDir, "metadata", "md5-cache")
	if info, statErr := os.Stat(md5CacheDir); statErr == nil && info.IsDir() {
		results, errs := walkCache(md5CacheDir)
		checkResults := true
		checkErrs := true
		for checkResults || checkErrs {
			select {
			case m, ok := <-results:
				if !ok {
					checkResults = false
					break
				}
				if m != nil {
					cp := m.Key()
					if existing, exists := available[cp]; !exists || versionGreater(m.Version, existing.Version) {
						available[cp] = m
					}
					a, parseErr := atom.Parse(cp + "-" + m.Version)
					if parseErr == nil {
						g.AllAtoms[cp+"-"+m.Version] = a
					}
				}
			case e, ok := <-errs:
				if !ok {
					checkErrs = false
					break
				}
				if e != nil {
					_ = e
				}
			}
		}
	}

	for cp, m := range installed {
		node := g.getOrCreate(cp, m)
		node.State = StateInstalled

		if avail, ok := available[cp]; ok {
			if versionGreater(avail.Version, m.Version) {
				node.State = StateUpdateAvailable
			}
		}

		g.addEdgesFromMeta(node, m)
	}

	for cp, m := range available {
		if _, isInstalled := installed[cp]; !isInstalled {
			node := g.getOrCreate(cp, m)
			if node.State == StateMissing {
				node.State = StateMissing
			}
			g.addEdgesFromMeta(node, m)
		}
	}

	g.computeRevDeps()

	return g, nil
}

func NewFromInstalled(installed []*metadata.PackageMetadata) *DepGraph {
	g := &DepGraph{
		Nodes:          make(map[string]*PkgNode),
		InstalledAtoms: make(map[string]*atom.Atom),
		AllAtoms:       make(map[string]*atom.Atom),
	}

	for _, m := range installed {
		if m == nil {
			continue
		}
		cp := m.Key()
		a, err := atom.Parse(cp + "-" + m.Version)
		if err == nil {
			g.InstalledAtoms[m.Category+"/"+m.Package+"-"+m.Version] = a
			g.AllAtoms[m.Category+"/"+m.Package+"-"+m.Version] = a
		}
		node := g.getOrCreate(cp, m)
		node.State = StateInstalled
		g.addEdgesFromMeta(node, m)
	}

	g.computeRevDeps()
	return g
}

func (g *DepGraph) getOrCreate(cp string, m *metadata.PackageMetadata) *PkgNode {
	if node, ok := g.Nodes[cp]; ok {
		if m != nil && node.Metadata == nil {
			node.Metadata = m
		}
		return node
	}

	a, err := atom.Parse(cp)
	if err != nil {
		a = &atom.Atom{Category: m.Category, Package: m.Package}
	}

	node := &PkgNode{
		Atom:     a,
		Metadata: m,
		State:    StateMissing,
	}
	g.Nodes[cp] = node
	return node
}

func (g *DepGraph) addEdgesFromMeta(node *PkgNode, m *metadata.PackageMetadata) {
	deps := []struct {
		raw  string
		kind DepType
	}{
		{m.DEPEND, DepDEPEND},
		{m.RDEPEND, DepRDEPEND},
		{m.BDEPEND, DepBDEPEND},
		{m.IDEPEND, DepIDEPEND},
		{m.PDEPEND, DepPDEPEND},
	}

	type edgePair struct {
		meta depstring.AtomMeta
		kind DepType
	}

	var edges []edgePair
	for _, d := range deps {
		if d.raw == "" {
			continue
		}
		depNode, err := depstring.Parse(d.raw)
		if err != nil || depNode == nil {
			continue
		}
		for _, meta := range depstring.CollectMeta(depNode) {
			if meta.Block || meta.WeakBlock {
				continue
			}
			edges = append(edges, edgePair{meta: meta, kind: d.kind})
		}
	}

	seen := make(map[string]bool)
	for _, e := range edges {
		parsed, err := atom.Parse(e.meta.Atom)
		if err != nil {
			continue
		}
		cp := parsed.CP()
		key := parsed.String() + "|" + e.kind.String()
		if seen[key] {
			continue
		}
		seen[key] = true

		edge := DepEdge{
			Atom:        parsed,
			Type:        e.kind,
			Conditional: e.meta.Condition,
			AnyOfGroup:  e.meta.AnyOfGroup,
		}
		node.Depends = append(node.Depends, edge)

		target := g.getOrCreate(cp, nil)
		target.Atom = parsed
	}
}

func (g *DepGraph) computeRevDeps() {
	for _, node := range g.Nodes {
		node.RevDepends = nil
	}

	for _, node := range g.Nodes {
		for _, edge := range node.Depends {
			cp := edge.Atom.CP()
			if target, ok := g.Nodes[cp]; ok {
				revEdge := DepEdge{
					Atom:        node.Atom,
					Type:        edge.Type,
					Conditional: edge.Conditional,
					AnyOfGroup:  edge.AnyOfGroup,
				}
				target.RevDepends = append(target.RevDepends, revEdge)
			}
		}
	}
}

func (g *DepGraph) ReverseDepsOf(cp string) []*PkgNode {
	if cp == "" {
		return nil
	}

	node, ok := g.Nodes[cp]
	if !ok {
		return nil
	}

	var result []*PkgNode
	for _, edge := range node.RevDepends {
		revCP := edge.Atom.CP()
		if revNode, exists := g.Nodes[revCP]; exists {
			result = append(result, revNode)
		}
	}
	return result
}

func (g *DepGraph) FindUpdates() []*PkgNode {
	var result []*PkgNode
	for _, node := range g.Nodes {
		if node.State == StateUpdateAvailable {
			result = append(result, node)
		}
	}
	return result
}

func (g *DepGraph) FindOutdated(useFlags map[string]bool) []*PkgNode {
	var result []*PkgNode
	for _, node := range g.Nodes {
		if node.State != StateInstalled && node.State != StateUpdateAvailable {
			continue
		}
		installed := make(map[string]bool)
		for cpv := range g.InstalledAtoms {
			installed[cpv] = true
		}

		allSatisfied := true
		for _, edge := range node.Depends {
			if edge.Conditional != "" {
				if !isConditionMet(edge.Conditional, useFlags) {
					continue
				}
			}
			edgeAtom := edge.Atom
			matched := false
			for cpv := range g.InstalledAtoms {
				if matchAtom(edgeAtom, cpv) {
					matched = true
					break
				}
			}
			if !matched {
				allSatisfied = false
				break
			}
		}
		if !allSatisfied {
			result = append(result, node)
		}
	}
	return result
}

func isConditionMet(condition string, useFlags map[string]bool) bool {
	for _, flag := range splitConditions(condition) {
		flag = trimSpace(flag)
		if flag == "" {
			continue
		}
		negate := false
		flagName := flag
		if len(flag) > 0 && flag[0] == '!' {
			negate = true
			flagName = flag[1:]
		}
		enabled := useFlags[flagName]
		if !negate && !enabled {
			return false
		}
		if negate && enabled {
			return false
		}
	}
	return true
}

func splitConditions(s string) []string {
	var parts []string
	current := ""
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			parts = append(parts, current)
			current = ""
		} else {
			current += string(s[i])
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

func matchAtom(a *atom.Atom, cpv string) bool {
	if a == nil {
		return false
	}
	parsed, err := atom.Parse(cpv)
	if err != nil {
		return false
	}
	if a.Category != parsed.Category || a.Package != parsed.Package {
		return false
	}
	if a.Version == nil || a.Version.Raw == "" {
		return true
	}
	return a.Version.Compare(parsed.Version) <= 0
}

func versionGreater(a, b string) bool {
	va, err := atom.Parse("cat/pkg-" + a)
	if err != nil || va.Version == nil {
		return false
	}
	vb, err2 := atom.Parse("cat/pkg-" + b)
	if err2 != nil || vb.Version == nil {
		return false
	}
	return va.Version.Compare(vb.Version) > 0
}

func walkCache(root string) (<-chan *metadata.PackageMetadata, <-chan error) {
	results := make(chan *metadata.PackageMetadata, 256)
	errs := make(chan error, 256)

	go func() {
		defer close(results)
		defer close(errs)

		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				errs <- err
				return nil
			}
			if d.IsDir() {
				return nil
			}

			data, readErr := os.ReadFile(path)
			if readErr != nil {
				errs <- readErr
				return nil
			}

			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				errs <- relErr
				return nil
			}

			m, parseErr := metadata.ParseCacheEntry(rel, data)
			if parseErr != nil {
				errs <- parseErr
				return nil
			}

			results <- m
			return nil
		})

		if err != nil {
			errs <- err
		}
	}()

	return results, errs
}

func (g *DepGraph) ToResolveGraph() *resolve.DepGraph {
	rg := resolve.NewDepGraph()

	for cp, node := range g.Nodes {
		available := node.AvailableVersions
		// Older synthetic graphs stored repository metadata only in Metadata.
		// A real installed-only node also has Metadata, but it came from VDB and
		// must never be promoted into a repository-available version.
		if len(available) == 0 && len(node.InstalledVersions) == 0 && node.Metadata != nil {
			available = []*metadata.PackageMetadata{node.Metadata}
		}
		for _, m := range available {
			vi := rg.AddVersionFromRepository(cp, m.Version, m.SLOT, m.Subslot, false, iuseDefaults(m.IUSE), m.KEYWORDS, m.Repository)
			vi.RepositoryPriority = repositoryPriority(m)
			vi.Available = !m.EAPIBanned
			vi.EAPIDeprecated = m.EAPIDeprecated
			vi.Depend, vi.Rdepend = m.DEPEND, m.RDEPEND
			vi.Bdepend, vi.Idepend, vi.Pdepend = m.BDEPEND, m.IDEPEND, m.PDEPEND
			vi.RequiredUse, vi.License = m.REQUIRED_USE, m.LICENSE
			vi.RepositoryPath, vi.SrcURI, vi.EAPI = m.RepositoryPath, m.SRC_URI, m.EAPI
			vi.DependencyMetadataKnown = true
		}
		for _, installed := range node.InstalledVersions {
			use := make(map[string]bool)
			iuse := make(map[string]bool)
			for _, flag := range installed.IUse {
				name := strings.TrimLeft(flag, "+-")
				use[name] = false
				iuse[name] = true
			}
			for _, flag := range installed.Use {
				use[flag] = true
			}
			vi := rg.AddVersionFromRepository(cp, installed.Version, installed.Slot, installed.Subslot, true, use, "", installed.Repository)
			vi.InstalledIUseFlags = iuse
			vi.InstalledEAPI = installed.EAPI
			if vi.EAPI == "" {
				vi.EAPI = installed.EAPI
			}
			vi.InstalledDepend, vi.InstalledRdepend = installed.Depend, installed.RDepend
			vi.InstalledBdepend, vi.InstalledIdepend, vi.InstalledPdepend = installed.BDepend, installed.IDepend, installed.PDepend
			vi.DependencyMetadataKnown = true
			if vi.Depend == "" && vi.Rdepend == "" && vi.Bdepend == "" && vi.Idepend == "" && vi.Pdepend == "" {
				vi.Depend, vi.Rdepend = installed.Depend, installed.RDepend
				vi.Bdepend, vi.Idepend, vi.Pdepend = installed.BDepend, installed.IDepend, installed.PDepend
			}
		}

		if node.Atom != nil {
			slot := node.Atom.Slot
			subslot := node.Atom.Subslot
			verStr := ""
			if node.Atom.Version != nil {
				verStr = node.Atom.Version.Raw
			}
			installed := len(node.InstalledVersions) > 0
			if _, exists := rg.Packages[cp]; !exists {
				rg.AddVersion(cp, verStr, slot, subslot, installed, nil, "")
			} else if node.Metadata == nil {
				rg.AddVersion(cp, verStr, slot, subslot, installed, nil, "")
			}
		}

		for _, edge := range node.Depends {
			depAtomStr := edge.Atom.String()
			var depType resolve.DepType
			switch edge.Type {
			case DepDEPEND:
				depType = resolve.DepTypeDepend
			case DepRDEPEND:
				depType = resolve.DepTypeRuntime
			case DepBDEPEND:
				depType = resolve.DepTypeBuild
			case DepIDEPEND:
				depType = resolve.DepTypeInstall
			case DepPDEPEND:
				depType = resolve.DepTypePost
			default:
				depType = resolve.DepTypeDepend
			}

			targetCP := edge.Atom.CP()
			block := edge.Atom.Op == "!" || edge.Atom.Op == "!!"

			rg.AddDep(cp, targetCP, depAtomStr, depType, edge.Conditional, block)
		}
	}

	return rg
}

// iuseDefaults converts the profile-independent defaults encoded in IUSE into
// the state expected by the resolver. A leading '+' enables a flag by default;
// a leading '-' (or no prefix) leaves it disabled until USE/package.use
// overrides it.
func iuseDefaults(iuse string) map[string]bool {
	flags := make(map[string]bool)
	for _, raw := range strings.Fields(iuse) {
		enabled := strings.HasPrefix(raw, "+")
		name := strings.TrimLeft(raw, "+-")
		if name != "" {
			flags[name] = enabled
		}
	}
	return flags
}

func versionOrEmpty(a *atom.Atom) string {
	if a == nil || a.Version == nil {
		return ""
	}
	return a.Version.Raw
}

func BuildParallel(db *badger.DB, repoDir string, workers int) (*DepGraph, error) {
	if workers <= 0 {
		workers = 8
	}

	g := &DepGraph{
		Nodes:          make(map[string]*PkgNode),
		InstalledAtoms: make(map[string]*atom.Atom),
		AllAtoms:       make(map[string]*atom.Atom),
	}

	var installed []*metadata.PackageMetadata
	prefix := []byte("pkg:")

	err := db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			val, err := item.ValueCopy(nil)
			if err != nil {
				continue
			}
			m, err := ingest.DecodeEntry(val)
			if err != nil {
				continue
			}
			installed = append(installed, m)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("graph: could not scan installed packages: %w", err)
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	errs := make(chan error, len(installed))

	for _, m := range installed {
		wg.Add(1)
		go func(m *metadata.PackageMetadata) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			cp := m.Key()
			ver := m.Version
			if ver == "" {
				return
			}

			a, parseErr := atom.Parse(cp + "-" + ver)
			if parseErr != nil {
				return
			}

			mu.Lock()
			node := g.getOrCreate(cp, m)
			node.State = StateInstalled
			if node.Atom == nil || node.Atom.Version == nil {
				node.Atom = a
			} else {
				existingVer := node.Atom.Version
				availVer := a.Version
				if availVer != nil && existingVer != nil && availVer.Compare(existingVer) > 0 {
					node.Atom = a
				}
			}
			g.InstalledAtoms[cp+"-"+ver] = a
			mu.Unlock()

			mu.Lock()
			g.addEdgesFromMeta(node, m)
			mu.Unlock()
		}(m)
	}

	wg.Wait()
	close(errs)

	for e := range errs {
		if err == nil {
			err = e
		}
	}

	md5CacheDir := filepath.Join(repoDir, "metadata", "md5-cache")
	if info, statErr := os.Stat(md5CacheDir); statErr == nil && info.IsDir() {
		results, walkErrs := walkCache(md5CacheDir)
		available := make(map[string]*metadata.PackageMetadata)

		done := make(chan struct{})
		go func() {
			for {
				select {
				case m, ok := <-results:
					if !ok {
						results = nil
					} else if m != nil {
						cp := m.Key()
						mu.Lock()
						if existing, exists := available[cp]; !exists || versionGreater(m.Version, existing.Version) {
							available[cp] = m
						}
						mu.Unlock()
					}
				case _, ok := <-walkErrs:
					if !ok {
						walkErrs = nil
					}
				}
				if results == nil && walkErrs == nil {
					break
				}
			}
			close(done)
		}()
		<-done

		for cp, m := range available {
			mu.Lock()
			if _, isInstalled := g.Nodes[cp]; !isInstalled {
				node := g.getOrCreate(cp, m)
				if node.State == StateMissing {
					node.State = StateMissing
				}
				g.addEdgesFromMeta(node, m)
			}
			mu.Unlock()
		}
	}

	g.computeRevDeps()
	return g, err
}

// BuildFromState constructs a graph from distinct available-repository and
// installed-VDB snapshots. Loading both snapshots overlaps because neither is
// allowed to mutate during resolution.
func BuildFromState(db *badger.DB, vdbPath string, workers int) (*DepGraph, error) {
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	type availableResult struct {
		packages map[string][]*metadata.PackageMetadata
		err      error
	}
	type installedResult struct {
		packages []vdb.Package
		err      error
	}
	availableCh := make(chan availableResult, 1)
	installedCh := make(chan installedResult, 1)
	go func() {
		selected := make(map[string][]*metadata.PackageMetadata)
		records, snapshotErr := resolversnapshot.Read(db.Opts().Dir)
		if snapshotErr == nil {
			for _, m := range records {
				selected[m.Key()] = append(selected[m.Key()], m)
			}
			availableCh <- availableResult{packages: selected}
			return
		}
		if !os.IsNotExist(snapshotErr) {
			availableCh <- availableResult{err: snapshotErr}
			return
		}
		err := ingest.QueryRangeParallel(db, "pkg:", workers, func(m *metadata.PackageMetadata) error {
			if !m.Complete() {
				return nil
			}
			cp := m.Key()
			selected[cp] = append(selected[cp], m)
			return nil
		})
		availableCh <- availableResult{packages: selected, err: err}
	}()
	go func() {
		packages, err := vdb.Scan(vdbPath)
		installedCh <- installedResult{packages: packages, err: err}
	}()

	available := <-availableCh
	installed := <-installedCh
	if available.err != nil {
		return nil, fmt.Errorf("graph: read available package index: %w", available.err)
	}
	if installed.err != nil {
		return nil, fmt.Errorf("graph: read installed package database: %w", installed.err)
	}

	g := &DepGraph{Nodes: make(map[string]*PkgNode), InstalledAtoms: make(map[string]*atom.Atom), AllAtoms: make(map[string]*atom.Atom)}
	for cp, versions := range available.packages {
		var selected *metadata.PackageMetadata
		for _, m := range versions {
			if selected == nil || versionGreater(m.Version, selected.Version) ||
				(m.Version == selected.Version && repositoryPriority(m) > repositoryPriority(selected)) {
				selected = m
			}
			if a, err := atom.Parse(cp + "-" + m.Version); err == nil {
				g.AllAtoms[cp+"-"+m.Version] = a
			}
		}
		node := g.getOrCreate(cp, selected)
		node.AvailableVersions = append(node.AvailableVersions, versions...)
		g.addEdgesFromMeta(node, selected)
	}
	for _, installedPackage := range installed.packages {
		cp := installedPackage.CP()
		installedMetadata := installedPackage.Metadata()
		node := g.Nodes[cp]
		if node == nil {
			node = g.getOrCreate(cp, installedMetadata)
			g.addEdgesFromMeta(node, installedMetadata)
		}
		node.State = StateInstalled
		node.InstalledVersions = append(node.InstalledVersions, installedPackage)
		for _, availableMetadata := range available.packages[cp] {
			if versionGreater(availableMetadata.Version, installedPackage.Version) {
				node.State = StateUpdateAvailable
				break
			}
		}
		if a, err := atom.Parse(installedPackage.CPV()); err == nil {
			g.InstalledAtoms[installedPackage.CPV()] = a
			g.AllAtoms[installedPackage.CPV()] = a
		}
	}
	g.computeRevDeps()
	return g, nil
}

func repositoryPriority(m *metadata.PackageMetadata) int {
	if m == nil {
		return 0
	}
	if m.RepositoryPriority != 0 {
		return m.RepositoryPriority
	}
	return m.OverlayIndex
}
