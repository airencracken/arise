package search

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/world"
	"github.com/dgraph-io/badger/v4"
)

var errStopIter = errors.New("stop")

type SearchResult struct {
	Category    string
	Package     string
	Version     string
	Slot        string
	Subslot     string
	Description string
	Homepage    string
	Keywords    string
	IUSE        string
	License     string
	Maintainers []string
	Orphaned    bool
	Installed   bool
	Stable      bool
	Testing     bool

	AllVersions        []string           `json:",omitempty"`
	IsMasked           bool               `json:",omitempty"`
	IsOverflow         bool               `json:",omitempty"`
	DependsOn          []string           `json:",omitempty"`
	RequiredBy         []string           `json:",omitempty"`
	BestVersion        string             `json:",omitempty"`
	BestVisibleVersion string             `json:",omitempty"`
	InstalledVer       string             `json:",omitempty"`
	VersionInfo        []VersionInfo      `json:",omitempty"`
	Repository         string             `json:",omitempty"`
	RepoPath           string             `json:",omitempty"`
	OverlayIndex       int                `json:",omitempty"`
	InstalledVersions  []InstalledVersion `json:",omitempty"`
}

type VersionInfo struct {
	Version    string
	Slot       string
	Stable     bool
	Testing    bool
	Masked     bool
	Restrict   string
	Properties string
}

type InstalledVersion struct {
	Version     string
	Slot        string
	Repository  string
	BuildTime   int64
	EnabledUSE  []string
	DisabledUSE []string
	Restrict    string
}

type SortField int

const (
	SortByCategory SortField = iota
	SortByPackage
	SortByVersion
	SortBySlot
)

type SearchConfig struct {
	Query       string
	Regex       bool
	Category    string
	Name        string
	Description bool
	Slot        string
	Use         string
	Keywords    string
	License     string
	Maintainer  string
	Orphaned    bool
	Installed   bool
	Stable      bool
	Testing     bool
	Exact       bool
	Limit       int
	Sort        SortField
	Compact     bool
	Versions    bool
	VDBPath     string
	RepoPath    string

	Format    string
	Print     []string
	JSON      bool
	Brief     bool
	OnlyNames bool
	CountOnly bool

	And bool
	Not string

	World  bool
	System bool

	DependsOn  string
	RequiredBy string

	HasUse     string
	HasVersion string

	Care       bool
	Overflow   bool
	Masked     bool
	Duplicates bool

	Dump bool
}

type rxPair struct {
	catRx     *regexp.Regexp
	pkgRx     *regexp.Regexp
	descRx    *regexp.Regexp
	slotRx    *regexp.Regexp
	kwRx      *regexp.Regexp
	licRx     *regexp.Regexp
	notRx     *regexp.Regexp
	dependsRx *regexp.Regexp
}

type collectedEntry struct {
	m         *metadata.PackageMetadata
	nameMatch bool
	descMatch bool
}

func compileRegex(cfg SearchConfig) (*rxPair, error) {
	var rp rxPair
	var err error

	cf := func(pat string, target **regexp.Regexp) {
		if err != nil || pat == "" {
			return
		}
		if cfg.Regex {
			*target, err = regexp.Compile(pat)
		} else {
			*target, err = regexp.Compile(`(?i)` + regexp.QuoteMeta(pat))
		}
	}

	if cfg.Category != "" {
		cf(cfg.Category, &rp.catRx)
	}
	if cfg.Name != "" {
		cf(cfg.Name, &rp.pkgRx)
	}
	if cfg.Query != "" && cfg.Description {
		cf(cfg.Query, &rp.descRx)
	}
	if cfg.Slot != "" {
		cf(cfg.Slot, &rp.slotRx)
	}
	if cfg.Keywords != "" {
		cf(cfg.Keywords, &rp.kwRx)
	}
	if cfg.License != "" {
		cf(cfg.License, &rp.licRx)
	}
	if cfg.Not != "" {
		cf(cfg.Not, &rp.notRx)
	}
	if cfg.DependsOn != "" {
		cf(cfg.DependsOn, &rp.dependsRx)
	}

	return &rp, err
}

func Search(db *badger.DB, cfg SearchConfig) ([]SearchResult, error) {
	vdbPath := cfg.VDBPath
	if vdbPath == "" {
		vdbPath = "/var/db/pkg"
	}
	var installed map[string][]InstalledVersion
	// Package-name-only output cannot expose installation metadata. Avoid a VDB
	// scan unless installation state is itself part of the requested filter.
	if !cfg.OnlyNames || cfg.Installed {
		installed = loadInstalledIndex(vdbPath)
	}

	if cfg.RequiredBy != "" {
		return searchRequiredBy(db, cfg, installed)
	}

	rx, err := compileRegex(cfg)
	if err != nil {
		return nil, err
	}

	queryLower := strings.ToLower(cfg.Query)
	queryGlob := hasGlobMeta(cfg.Query)
	if queryGlob {
		if _, err := path.Match(queryLower, ""); err != nil {
			return nil, fmt.Errorf("invalid search glob %q: %w", cfg.Query, err)
		}
	}
	parsedUse := parseUseFilter(cfg.Use)
	queryTokens := parseQueryTokens(cfg.Query, cfg.And)

	var ws *world.WorldSet
	var ss *world.WorldSet
	if cfg.World {
		ws = loadWorldSet()
	}
	if cfg.System {
		ss = loadSystemSet(cfg)
	}

	var allEntries []collectedEntry
	var maintainerPattern *regexp.Regexp
	if cfg.Maintainer != "" && cfg.Regex {
		maintainerPattern, err = regexp.Compile(cfg.Maintainer)
		if err != nil {
			return nil, fmt.Errorf("invalid maintainer regex %q: %w", cfg.Maintainer, err)
		}
	}

	collect := func(m *metadata.PackageMetadata) error {
		res := toSearchResult(m, installed[m.Key()])

		if cfg.Orphaned && !m.MaintainerNeeded {
			return nil
		}
		if cfg.Maintainer != "" {
			matched := false
			for _, email := range m.Maintainers {
				if maintainerPattern != nil {
					matched = maintainerPattern.MatchString(email)
				} else {
					matched = strings.EqualFold(email, cfg.Maintainer) ||
						strings.Contains(strings.ToLower(email), strings.ToLower(cfg.Maintainer))
				}
				if matched {
					break
				}
			}
			if !matched {
				return nil
			}
		}
		if !matchesSearchResult(res, cfg, rx, queryLower, parsedUse) {
			return nil
		}

		if cfg.HasUse != "" && !hasUseFlag(m.IUSE, cfg.HasUse) {
			return nil
		}

		if cfg.HasVersion != "" && !matchVersionGlob(m.Version, cfg.HasVersion) {
			return nil
		}

		if cfg.Query != "" {
			nameMatch := false
			descMatch := false

			if queryGlob {
				nameMatch = matchPackageGlob(queryLower, strings.ToLower(m.Category), strings.ToLower(m.Package))
			} else if cfg.And {
				nameMatch = matchAllTokens(strings.ToLower(m.Category), strings.ToLower(m.Package), queryTokens, cfg.Exact)
				descMatch = matchAllTokens(strings.ToLower(m.DESCRIPTION), "", queryTokens, cfg.Exact)
			} else if !cfg.Exact {
				nameMatch = strings.Contains(strings.ToLower(m.Category), queryLower) ||
					strings.Contains(strings.ToLower(m.Package), queryLower) ||
					strings.Contains(strings.ToLower(m.Category+"/"+m.Package), queryLower)
				descMatch = cfg.Description && strings.Contains(strings.ToLower(m.DESCRIPTION), queryLower)
			} else {
				nameMatch = strings.EqualFold(m.Category, cfg.Query) ||
					strings.EqualFold(m.Package, cfg.Query) || strings.EqualFold(m.Category+"/"+m.Package, cfg.Query)
				descMatch = cfg.Description && strings.EqualFold(m.DESCRIPTION, cfg.Query)
			}

			if !nameMatch && !descMatch {
				return nil
			}
			allEntries = append(allEntries, collectedEntry{m: m, nameMatch: nameMatch, descMatch: descMatch})
			return nil
		}

		allEntries = append(allEntries, collectedEntry{m: m, nameMatch: true, descMatch: false})
		return nil
	}

	if cfg.Query != "" && !cfg.Description && !cfg.And {
		var candidates []string
		err = ingest.QueryKeys(db, "pkg:", func(cp string) error {
			parts := strings.SplitN(cp, "/", 2)
			if len(parts) != 2 {
				return nil
			}
			category, pkg := strings.ToLower(parts[0]), strings.ToLower(parts[1])
			matches := false
			if queryGlob {
				matches = matchPackageGlob(queryLower, category, pkg)
			} else if cfg.And {
				matches = matchAllTokens(category, pkg, queryTokens, cfg.Exact)
			} else if cfg.Exact {
				matches = strings.EqualFold(parts[0], cfg.Query) || strings.EqualFold(parts[1], cfg.Query) || strings.EqualFold(cp, cfg.Query)
			} else {
				matches = strings.Contains(category, queryLower) || strings.Contains(pkg, queryLower) || strings.Contains(category+"/"+pkg, queryLower)
			}
			if matches {
				candidates = append(candidates, cp)
			}
			return nil
		})
		if err == nil {
			for _, cp := range candidates {
				m, queryErr := ingest.Query(db, cp)
				if queryErr != nil {
					err = queryErr
					break
				}
				if m != nil {
					err = collect(m)
					if err != nil {
						break
					}
				}
			}
		}
	} else {
		err = ingest.QueryRange(db, "pkg:", collect)
	}
	if err != nil {
		return nil, err
	}

	results := mergeCollectedResults(allEntries, cfg, installed)

	if cfg.World && ws != nil {
		results = filterByWorldSet(results, ws)
	}

	if cfg.System && ss != nil {
		results = filterByWorldSet(results, ss)
	}

	if cfg.DependsOn != "" {
		results = filterByDependsOn(db, results, cfg.DependsOn)
	}

	if cfg.Not != "" {
		results = filterByNot(results, rx, queryLower, cfg)
	}

	if cfg.Installed {
		results = postFilter(results, cfg)
	}

	results = applyStatusFilters(results, cfg)

	if cfg.Versions || cfg.Duplicates {
		results = expandAllVersions(results, cfg, installed)
	}

	sortResults(results, cfg.Sort)

	if cfg.Limit > 0 && len(results) > cfg.Limit {
		results = results[:cfg.Limit]
	}

	if results == nil {
		results = []SearchResult{}
	}

	populateDepFields(db, results)

	return results, nil
}

func hasGlobMeta(query string) bool {
	return strings.ContainsAny(query, "*?[")
}

func matchPackageGlob(pattern, category, pkg string) bool {
	if strings.Contains(pattern, "/") {
		matched, _ := path.Match(pattern, category+"/"+pkg)
		return matched
	}
	categoryMatch, _ := path.Match(pattern, category)
	packageMatch, _ := path.Match(pattern, pkg)
	return categoryMatch || packageMatch
}

func searchRequiredBy(db *badger.DB, cfg SearchConfig, installed map[string][]InstalledVersion) ([]SearchResult, error) {
	var target *metadata.PackageMetadata
	err := ingest.QueryRange(db, "pkg:", func(m *metadata.PackageMetadata) error {
		targetName := strings.ToLower(cfg.RequiredBy)
		if strings.Contains(strings.ToLower(m.Category), targetName) ||
			strings.Contains(strings.ToLower(m.Package), targetName) ||
			strings.EqualFold(m.Category+"/"+m.Package, cfg.RequiredBy) {
			target = m
			return errStopIter
		}
		return nil
	})
	if err != nil && err != errStopIter {
		return nil, err
	}
	if target == nil {
		return nil, nil
	}

	depAtoms := target.DependAtoms()
	rdepAtoms := target.RDependAtoms()
	allDeps := uniqueStrings(append(depAtoms, rdepAtoms...))

	var results []SearchResult
	for _, depStr := range allDeps {
		depStr = strings.TrimSpace(depStr)
		if depStr == "" {
			continue
		}
		cp := extractCP(depStr)
		if cp == "" {
			continue
		}
		m, qErr := ingest.Query(db, cp)
		if qErr != nil || m == nil {
			continue
		}
		res := toSearchResult(m, installed[m.Key()])
		results = append(results, res)
	}

	sortResults(results, cfg.Sort)
	if cfg.Limit > 0 && len(results) > cfg.Limit {
		results = results[:cfg.Limit]
	}
	if results == nil {
		results = []SearchResult{}
	}
	return results, nil
}

func parseQueryTokens(query string, and bool) []string {
	if query == "" || !and {
		return nil
	}
	return strings.Fields(query)
}

func matchAllTokens(category, pkg string, tokens []string, exact bool) bool {
	if len(tokens) == 0 {
		return true
	}
	for _, token := range tokens {
		tokenLower := strings.ToLower(token)
		match := false
		if exact {
			if strings.EqualFold(category, token) || strings.EqualFold(pkg, token) {
				match = true
			}
		} else {
			if strings.Contains(category, tokenLower) || strings.Contains(pkg, tokenLower) {
				match = true
			}
		}
		if !match {
			return false
		}
	}
	return true
}

func matchesSearchResult(r SearchResult, cfg SearchConfig, rx *rxPair, queryLower string, parsedUse []useFilter) bool {
	if cfg.Category != "" {
		if rx.catRx != nil && !rx.catRx.MatchString(r.Category) {
			return false
		}
	}
	if cfg.Name != "" {
		if rx.pkgRx != nil && !rx.pkgRx.MatchString(r.Package) {
			return false
		}
	}
	if cfg.Slot != "" {
		if rx.slotRx != nil && !rx.slotRx.MatchString(r.Slot) {
			return false
		}
	}
	if cfg.Keywords != "" {
		if rx.kwRx != nil && !rx.kwRx.MatchString(r.Keywords) {
			return false
		}
	}
	if cfg.License != "" {
		if rx.licRx != nil && !rx.licRx.MatchString(r.License) {
			return false
		}
	}
	if cfg.Stable && !r.Stable {
		return false
	}
	if cfg.Testing && !r.Testing {
		return false
	}
	if cfg.Use != "" && !matchUseFilter(r.IUSE, parsedUse) {
		return false
	}
	return true
}

type useFilter struct {
	name     string
	required bool
}

func parseUseFilter(useStr string) []useFilter {
	var filters []useFilter
	for _, token := range strings.Fields(useStr) {
		if token == "" {
			continue
		}
		if strings.HasPrefix(token, "-") {
			filters = append(filters, useFilter{name: token[1:], required: false})
		} else if strings.HasPrefix(token, "+") {
			filters = append(filters, useFilter{name: token[1:], required: true})
		} else {
			filters = append(filters, useFilter{name: token, required: true})
		}
	}
	return filters
}

func matchUseFilter(iuse string, filters []useFilter) bool {
	if len(filters) == 0 {
		return true
	}
	flags := make(map[string]bool)
	for _, f := range strings.Fields(iuse) {
		if f == "" {
			continue
		}
		flags[f] = true
	}
	for _, f := range filters {
		if f.required && !flags[f.name] {
			return false
		}
		if !f.required && flags[f.name] {
			return false
		}
	}
	return true
}

func hasUseFlag(iuse, flag string) bool {
	for _, f := range strings.Fields(iuse) {
		if f == flag || f == "+"+flag || f == "-"+flag {
			return true
		}
	}
	return false
}

func matchVersionGlob(version, pattern string) bool {
	modified := "^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$"
	rx, err := regexp.Compile(modified)
	if err != nil {
		return false
	}
	return rx.MatchString(version)
}

func postFilter(results []SearchResult, cfg SearchConfig) []SearchResult {
	if cfg.Installed {
		var filtered []SearchResult
		for _, r := range results {
			if r.Installed {
				filtered = append(filtered, r)
			}
		}
		return filtered
	}
	return results
}

func mergeCollectedResults(entries []collectedEntry, cfg SearchConfig, installed map[string][]InstalledVersion) []SearchResult {
	seen := make(map[string]bool)
	var results []SearchResult
	for _, e := range entries {
		key := e.m.Category + "/" + e.m.Package
		if seen[key] {
			continue
		}
		seen[key] = true
		r := toSearchResult(e.m, installed[e.m.Key()])
		results = append(results, r)
	}
	return results
}

func filterByWorldSet(results []SearchResult, ws *world.WorldSet) []SearchResult {
	if ws == nil || len(ws.Atoms) == 0 {
		return results
	}
	var filtered []SearchResult
	for _, r := range results {
		if ws.Contains(r.Category + "/" + r.Package) {
			filtered = append(filtered, r)
		} else {
			matchesAny := false
			for _, a := range ws.Atoms {
				if strings.Contains(a, r.Category+"/"+r.Package) {
					matchesAny = true
					break
				}
			}
			if matchesAny {
				filtered = append(filtered, r)
			}
		}
	}
	return filtered
}

func filterByDependsOn(db *badger.DB, results []SearchResult, dependsOn string) []SearchResult {
	matchingCPs := make(map[string]bool)

	_ = ingest.QueryRange(db, "pkg:", func(m *metadata.PackageMetadata) error {
		depStrs := append(m.DependAtoms(), m.RDependAtoms()...)
		for _, dep := range depStrs {
			if atomMatches(dep, dependsOn) {
				matchingCPs[m.Category+"/"+m.Package] = true
				return nil
			}
		}
		return nil
	})

	var filtered []SearchResult
	for _, r := range results {
		key := r.Category + "/" + r.Package
		if matchingCPs[key] {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func atomMatches(depAtomStr, target string) bool {
	cp := extractCP(depAtomStr)
	if cp == "" {
		return false
	}
	targetLower := strings.ToLower(target)
	return strings.Contains(strings.ToLower(cp), targetLower) ||
		strings.Contains(strings.ToLower(depAtomStr), targetLower)
}

func extractCP(atomStr string) string {
	atomStr = strings.TrimSpace(atomStr)
	for _, prefix := range []string{">=", "<=", "=", ">", "<", "~", "!", "*"} {
		for strings.HasPrefix(atomStr, prefix) && len(atomStr) > len(prefix) {
			atomStr = atomStr[len(prefix):]
		}
	}
	colonIdx := strings.Index(atomStr, ":")
	if colonIdx >= 0 {
		atomStr = atomStr[:colonIdx]
	}
	bracketIdx := strings.Index(atomStr, "[")
	if bracketIdx >= 0 {
		atomStr = atomStr[:bracketIdx]
	}
	slashIdx := strings.Index(atomStr, "/")
	if slashIdx < 0 {
		return ""
	}
	dashIdx := strings.Index(atomStr[slashIdx+1:], "-")
	if dashIdx >= 0 {
		return atomStr[:slashIdx+1+dashIdx]
	}
	return atomStr
}

func filterByNot(results []SearchResult, rx *rxPair, queryLower string, cfg SearchConfig) []SearchResult {
	if cfg.Not == "" {
		return results
	}
	var filtered []SearchResult
	for _, r := range results {
		if rx.notRx != nil && rx.notRx.MatchString(r.Category) {
			continue
		}
		if rx.notRx != nil && rx.notRx.MatchString(r.Package) {
			continue
		}
		if rx.notRx != nil && rx.notRx.MatchString(r.Description) {
			continue
		}
		notLower := strings.ToLower(cfg.Not)
		if strings.Contains(strings.ToLower(r.Category), notLower) {
			continue
		}
		if strings.Contains(strings.ToLower(r.Package), notLower) {
			continue
		}
		if strings.Contains(strings.ToLower(r.Description), notLower) {
			continue
		}
		filtered = append(filtered, r)
	}
	return filtered
}

func applyStatusFilters(results []SearchResult, cfg SearchConfig) []SearchResult {
	if !cfg.Care && !cfg.Masked && !cfg.Overflow {
		return results
	}
	var filtered []SearchResult
	for _, r := range results {
		if cfg.Masked && !r.IsMasked {
			continue
		}
		if cfg.Overflow && !r.IsOverflow {
			continue
		}
		if cfg.Care {
			if !r.IsMasked && !r.IsOverflow {
				continue
			}
		}
		filtered = append(filtered, r)
	}
	return filtered
}

func expandAllVersions(results []SearchResult, cfg SearchConfig, installed map[string][]InstalledVersion) []SearchResult {
	if cfg.RepoPath == "" {
		return results
	}

	var expanded []SearchResult
	for _, r := range results {
		repoRoot := r.RepoPath
		if repoRoot == "" {
			repoRoot = cfg.RepoPath
		}
		cpDir := filepath.Join(repoRoot, r.Category, r.Package)
		ebuilds, err := filepath.Glob(filepath.Join(cpDir, r.Package+"-*.ebuild"))
		if err != nil || len(ebuilds) == 0 {
			expanded = append(expanded, r)
			continue
		}
		if cfg.Versions && !cfg.Duplicates {
			var versions []string
			var versionInfo []VersionInfo
			var bestMetadata *metadata.PackageMetadata
			for _, eb := range ebuilds {
				ver := versionFromEbuild(eb, r.Package)
				if ver != "" {
					versions = append(versions, ver)
					repoRoot := r.RepoPath
					if repoRoot == "" {
						repoRoot = cfg.RepoPath
					}
					cachePath := filepath.Join(repoRoot, "metadata", "md5-cache", r.Category, r.Package+"-"+ver)
					if data, readErr := os.ReadFile(cachePath); readErr == nil {
						if m, parseErr := metadata.ParseCacheEntry(r.Category+"/"+r.Package+"-"+ver, data); parseErr == nil {
							status := toSearchResult(m, installed[m.Key()])
							versionInfo = append(versionInfo, VersionInfo{Version: ver, Slot: m.SLOT, Stable: status.Stable, Testing: status.Testing, Masked: status.IsMasked, Restrict: m.RESTRICT, Properties: m.PROPERTIES})
							if bestMetadata == nil || compareVersionStrings(ver, bestMetadata.Version) > 0 {
								bestMetadata = m
							}
						}
					}
				}
			}
			sort.Slice(versions, func(i, j int) bool {
				a, _ := atom.ParseVersion(versions[i])
				b, _ := atom.ParseVersion(versions[j])
				if a == nil || b == nil {
					return versions[i] < versions[j]
				}
				return a.Compare(b) < 0
			})
			r.AllVersions = versions
			sort.Slice(versionInfo, func(i, j int) bool { return compareVersionStrings(versionInfo[i].Version, versionInfo[j].Version) < 0 })
			r.VersionInfo = versionInfo
			for i := len(versionInfo) - 1; i >= 0; i-- {
				if !versionInfo[i].Masked && versionInfo[i].Stable {
					r.BestVisibleVersion = versionInfo[i].Version
					break
				}
			}
			if bestMetadata != nil {
				r.Description = bestMetadata.DESCRIPTION
				r.Homepage = bestMetadata.HOMEPAGE
				r.IUSE = bestMetadata.IUSE
				r.Keywords = bestMetadata.KEYWORDS
			}
			if len(versions) > 0 {
				r.BestVersion = bestVersionString(versions)
			}
			expanded = append(expanded, r)
		} else {
			for _, eb := range ebuilds {
				ver := versionFromEbuild(eb, r.Package)
				if ver == "" {
					continue
				}
				newR := r
				newR.Version = ver
				newR.BestVersion = ver
				expanded = append(expanded, newR)
			}
		}
	}
	return expanded
}

func compareVersionStrings(a, b string) int {
	av, _ := atom.ParseVersion(a)
	bv, _ := atom.ParseVersion(b)
	if av == nil || bv == nil {
		return strings.Compare(a, b)
	}
	return av.Compare(bv)
}

func versionFromEbuild(ebuildPath, pkg string) string {
	base := filepath.Base(ebuildPath)
	name := strings.TrimSuffix(base, ".ebuild")
	if strings.HasPrefix(name, pkg+"-") {
		return name[len(pkg)+1:]
	}
	return ""
}

func bestVersionString(versions []string) string {
	if len(versions) == 0 {
		return ""
	}
	best := versions[0]
	bestV, _ := atom.ParseVersion(best)
	for _, v := range versions[1:] {
		pv, _ := atom.ParseVersion(v)
		if bestV == nil && pv != nil {
			best, bestV = v, pv
			continue
		}
		if bestV != nil && pv != nil && pv.Compare(bestV) > 0 {
			best, bestV = v, pv
		}
	}
	return best
}

func populateDepFields(db *badger.DB, results []SearchResult) {
	if len(results) == 0 {
		return
	}
	for i := range results {
		r := &results[i]
		m, err := ingest.Query(db, r.Category+"/"+r.Package)
		if err != nil || m == nil {
			continue
		}
		var deps []string
		for _, d := range m.DependAtoms() {
			cp := extractCP(d)
			if cp != "" {
				deps = append(deps, cp)
			}
		}
		for _, d := range m.RDependAtoms() {
			cp := extractCP(d)
			if cp != "" {
				deps = append(deps, cp)
			}
		}
		r.DependsOn = uniqueStrings(deps)
	}

	cpToIndex := make(map[string][]int)
	for i, r := range results {
		key := r.Category + "/" + r.Package
		cpToIndex[key] = append(cpToIndex[key], i)
	}

	for i := range results {
		var revDeps []string
		for j, r2 := range results {
			if i == j {
				continue
			}
			for _, dep := range r2.DependsOn {
				if dep == results[i].Category+"/"+results[i].Package {
					revDeps = append(revDeps, r2.Category+"/"+r2.Package)
				}
			}
		}
		results[i].RequiredBy = uniqueStrings(revDeps)
	}
}

func toSearchResult(m *metadata.PackageMetadata, installedVersions []InstalledVersion) SearchResult {
	installed := len(installedVersions) > 0
	installedVer := ""
	for _, installedVersion := range installedVersions {
		if installedVer == "" || compareVersionStrings(installedVersion.Version, installedVer) > 0 {
			installedVer = installedVersion.Version
		}
	}

	stable := false
	testing := false
	allTesting := true
	hasKeywords := false
	keywords := strings.Fields(m.KEYWORDS)
	for _, kw := range keywords {
		kw = strings.TrimSpace(kw)
		if kw == "" || kw == "-*" {
			continue
		}
		hasKeywords = true
		if strings.HasPrefix(kw, "~") {
			testing = true
		} else {
			stable = true
			allTesting = false
		}
	}

	isMasked := !hasKeywords || (!stable && !testing) || m.KEYWORDS == ""
	isOverflow := hasKeywords && allTesting && !stable

	return SearchResult{
		Category:          m.Category,
		Package:           m.Package,
		Version:           m.Version,
		Slot:              m.SLOT,
		Subslot:           m.Subslot,
		Description:       m.DESCRIPTION,
		Homepage:          m.HOMEPAGE,
		Keywords:          m.KEYWORDS,
		IUSE:              m.IUSE,
		License:           m.LICENSE,
		Maintainers:       append([]string(nil), m.Maintainers...),
		Orphaned:          m.MaintainerNeeded,
		Installed:         installed,
		Stable:            stable,
		Testing:           testing,
		IsMasked:          isMasked,
		IsOverflow:        isOverflow,
		BestVersion:       m.Version,
		InstalledVer:      installedVer,
		InstalledVersions: installedVersions,
		Repository:        m.Repository,
		RepoPath:          m.RepositoryPath,
		OverlayIndex:      m.OverlayIndex,
	}
}

func loadInstalledVersions(vdbPath, category, pkg string) []InstalledVersion {
	return loadInstalledIndex(vdbPath)[category+"/"+pkg]
}

func loadInstalledIndex(vdbPath string) map[string][]InstalledVersion {
	installed := make(map[string][]InstalledVersion)
	categories, err := os.ReadDir(vdbPath)
	if err != nil {
		return installed
	}

	for _, category := range categories {
		if !category.IsDir() {
			continue
		}
		categoryPath := filepath.Join(vdbPath, category.Name())
		entries, err := os.ReadDir(categoryPath)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			cpv, err := atom.Parse(category.Name() + "/" + entry.Name())
			if err == nil && cpv.Version != nil {
				key := cpv.Category + "/" + cpv.Package
				if version, ok := readInstalledVersion(filepath.Join(categoryPath, entry.Name()), cpv.Version.Raw); ok {
					installed[key] = append(installed[key], version)
				}
				continue
			}

			// Retain support for the older category/package/package-version
			// layout used by fixtures and some external VDB producers.
			legacyDir := filepath.Join(categoryPath, entry.Name())
			legacyVersions, err := os.ReadDir(legacyDir)
			if err != nil {
				continue
			}
			foundLegacyVersion := false
			for _, legacyVersion := range legacyVersions {
				if !legacyVersion.IsDir() {
					continue
				}
				cpv, err := atom.Parse(category.Name() + "/" + legacyVersion.Name())
				if err != nil || cpv.Version == nil || cpv.Package != entry.Name() {
					continue
				}
				key := cpv.Category + "/" + cpv.Package
				if version, ok := readInstalledVersion(filepath.Join(legacyDir, legacyVersion.Name()), cpv.Version.Raw); ok {
					installed[key] = append(installed[key], version)
					foundLegacyVersion = true
				}
			}
			if !foundLegacyVersion {
				installed[category.Name()+"/"+entry.Name()] = []InstalledVersion{{}}
			}
		}
	}

	for key := range installed {
		versions := installed[key]
		sort.Slice(versions, func(i, j int) bool {
			return compareVersionStrings(versions[i].Version, versions[j].Version) < 0
		})
		installed[key] = versions
	}
	return installed
}

func readInstalledVersion(dir, version string) (InstalledVersion, bool) {
	if atomVersion, _ := atom.ParseVersion(version); atomVersion == nil {
		return InstalledVersion{}, false
	}
	read := func(name string) string {
		data, _ := os.ReadFile(filepath.Join(dir, name))
		return strings.TrimSpace(string(data))
	}
	enabledSet := make(map[string]bool)
	for _, flag := range strings.Fields(read("USE")) {
		enabledSet[flag] = true
	}
	var enabled, disabled []string
	for _, raw := range strings.Fields(read("IUSE")) {
		flag := strings.TrimLeft(raw, "+-")
		if enabledSet[flag] {
			enabled = append(enabled, flag)
		} else {
			disabled = append(disabled, "-"+flag)
		}
	}
	buildTime, _ := strconv.ParseInt(read("BUILD_TIME"), 10, 64)
	return InstalledVersion{
		Version: version, Slot: read("SLOT"), Repository: read("repository"),
		BuildTime: buildTime, EnabledUSE: enabled, DisabledUSE: disabled,
		Restrict: read("RESTRICT"),
	}, true
}

func findInstalledVersion(vdbCPPath, pkg string) string {
	entries, err := os.ReadDir(vdbCPPath)
	if err != nil {
		return ""
	}
	best := ""
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, pkg+"-") {
			ver := name[len(pkg)+1:]
			if ver != "" && (best == "" || compareVersionStrings(ver, best) > 0) {
				best = ver
			}
		}
	}
	return best
}

func sortResults(results []SearchResult, s SortField) {
	sort.Slice(results, func(i, j int) bool {
		switch s {
		case SortByVersion:
			a, _ := atom.ParseVersion(results[i].Version)
			b, _ := atom.ParseVersion(results[j].Version)
			if a != nil && b != nil {
				cmp := a.Compare(b)
				if cmp != 0 {
					return cmp > 0
				}
			}
		case SortBySlot:
			if results[i].Slot != results[j].Slot {
				return results[i].Slot < results[j].Slot
			}
		}
		if results[i].Category != results[j].Category {
			return results[i].Category < results[j].Category
		}
		if results[i].Package != results[j].Package {
			return results[i].Package < results[j].Package
		}
		vi, _ := atom.ParseVersion(results[i].Version)
		vj, _ := atom.ParseVersion(results[j].Version)
		if vi != nil && vj != nil {
			return vi.Compare(vj) > 0
		}
		return results[i].Version > results[j].Version
	})
}

func matchField(field, query, queryLower string, exact bool) bool {
	if query == "" {
		return true
	}
	if exact {
		return strings.EqualFold(field, query)
	}
	return strings.Contains(strings.ToLower(field), queryLower)
}

func loadWorldSet() *world.WorldSet {
	ws, err := world.LoadWorld("/var/lib/portage/world")
	if err != nil {
		return nil
	}
	return ws
}

func loadSystemSet(cfg SearchConfig) *world.WorldSet {
	ws, err := world.LoadSystem("/etc/portage/profile/packages")
	if err != nil {
		ws, err = world.LoadSystem("/var/lib/portage/world_sets")
		if err != nil {
			return nil
		}
	}
	return ws
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, s := range in {
		if s == "" {
			continue
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
