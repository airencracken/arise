// Package repositoryquery provides low-latency, read-only queries over the
// indexed repository snapshot using the same package policy inputs as the
// resolver.
package repositoryquery

import (
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/portage"
	"github.com/dgraph-io/badger/v4"
)

// BestBinary returns the highest binary-package index entry satisfying query
// and current package mask and keyword policy.
func BestBinary(index *binpkg.PackagesIndex, cfg *portage.Config, query string) (binpkg.PackageIndexEntry, error) {
	rule, err := atom.ParsePackageAtom(query)
	if err != nil {
		return nil, fmt.Errorf("repository query: parse atom: %w", err)
	}
	arch := runtimeArch()
	if cfg != nil && cfg.MakeConf["ARCH"] != "" {
		arch = cfg.MakeConf["ARCH"]
	}
	type candidate struct {
		entry   binpkg.PackageIndexEntry
		version *atom.Version
	}
	var candidates []candidate
	for _, entry := range index.Packages {
		cpv := entry["CPV"]
		parsed, parseErr := atom.Parse(cpv)
		if parseErr != nil || parsed.Version == nil || parsed.Key() != rule.Key() {
			continue
		}
		slot, repository := entry["SLOT"], entry["repository"]
		if repository == "" {
			repository = entry["REPOSITORY"]
		}
		if !portage.PackageAtomMatches(queryWithoutUse(*rule), cpv, slot, repository) {
			continue
		}
		if cfg != nil {
			if cfg.PackageMaskStatus(cpv, slot, repository).Masked {
				continue
			}
			if keywords := entry["KEYWORDS"]; keywords != "" &&
				!cfg.KeywordAcceptedFor(cpv, slot, repository, keywords, arch) {
				continue
			}
		}
		candidates = append(candidates, candidate{entry: entry, version: parsed.Version})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if comparison := candidates[i].version.Compare(candidates[j].version); comparison != 0 {
			return comparison < 0
		}
		return candidates[i].entry["BUILD_ID"] < candidates[j].entry["BUILD_ID"]
	})
	if len(candidates) == 0 {
		return nil, nil
	}
	return candidates[len(candidates)-1].entry, nil
}

// Visible returns all visible repository records satisfying query, ordered
// from lowest to highest Gentoo version and then repository priority.
func Visible(db *badger.DB, cfg *portage.Config, query string) ([]*metadata.PackageMetadata, error) {
	rule, err := atom.ParsePackageAtom(query)
	if err != nil {
		return nil, fmt.Errorf("repository query: parse atom: %w", err)
	}
	records, err := ingest.QueryVersions(db, rule.Key())
	if err != nil {
		return nil, err
	}
	arch := runtimeArch()
	if cfg != nil && cfg.MakeConf["ARCH"] != "" {
		arch = cfg.MakeConf["ARCH"]
	}
	var result []*metadata.PackageMetadata
	for _, record := range records {
		slot := record.SLOT
		if record.Subslot != "" {
			slot += "/" + record.Subslot
		}
		if !portage.PackageAtomMatches(queryWithoutUse(*rule), record.CPV(), slot, record.Repository) {
			continue
		}
		if cfg != nil {
			if cfg.PackageMaskStatus(record.CPV(), slot, record.Repository).Masked {
				continue
			}
			if !cfg.KeywordAcceptedFor(record.CPV(), slot, record.Repository, record.KEYWORDS, arch) {
				continue
			}
		}
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool {
		left, leftErr := atom.ParseVersion(result[i].Version)
		right, rightErr := atom.ParseVersion(result[j].Version)
		if leftErr == nil && rightErr == nil {
			if comparison := left.Compare(right); comparison != 0 {
				return comparison < 0
			}
		}
		if result[i].RepositoryPriority != result[j].RepositoryPriority {
			return result[i].RepositoryPriority < result[j].RepositoryPriority
		}
		if result[i].OverlayIndex != result[j].OverlayIndex {
			return result[i].OverlayIndex < result[j].OverlayIndex
		}
		return result[i].Repository < result[j].Repository
	})
	return result, nil
}

// BestVisible returns the highest visible record satisfying query.
func BestVisible(db *badger.DB, cfg *portage.Config, query string) (*metadata.PackageMetadata, error) {
	records, err := Visible(db, cfg, query)
	if err != nil || len(records) == 0 {
		return nil, err
	}
	return records[len(records)-1], nil
}

// BestMatching returns the highest indexed repository record satisfying query
// without applying mask or keyword policy. Exact diagnostic queries use this
// to inspect candidates whose invisibility is itself the fact under review.
func BestMatching(db *badger.DB, query string) (*metadata.PackageMetadata, error) {
	rule, err := atom.ParsePackageAtom(query)
	if err != nil {
		return nil, fmt.Errorf("repository query: parse atom: %w", err)
	}
	records, err := ingest.QueryVersions(db, rule.Key())
	if err != nil {
		return nil, err
	}
	var result []*metadata.PackageMetadata
	for _, record := range records {
		slot := record.SLOT
		if record.Subslot != "" {
			slot += "/" + record.Subslot
		}
		if portage.PackageAtomMatches(queryWithoutUse(*rule), record.CPV(), slot, record.Repository) {
			result = append(result, record)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		left, leftErr := atom.ParseVersion(result[i].Version)
		right, rightErr := atom.ParseVersion(result[j].Version)
		if leftErr == nil && rightErr == nil {
			if comparison := left.Compare(right); comparison != 0 {
				return comparison < 0
			}
		}
		if result[i].RepositoryPriority != result[j].RepositoryPriority {
			return result[i].RepositoryPriority < result[j].RepositoryPriority
		}
		if result[i].OverlayIndex != result[j].OverlayIndex {
			return result[i].OverlayIndex < result[j].OverlayIndex
		}
		return result[i].Repository < result[j].Repository
	})
	if len(result) == 0 {
		return nil, nil
	}
	return result[len(result)-1], nil
}

// AllBestVisible returns one preferred visible record for every indexed CP.
func AllBestVisible(db *badger.DB, cfg *portage.Config) ([]*metadata.PackageMetadata, error) {
	keys := make(map[string]bool)
	if err := ingest.QueryAll(db, func(record *metadata.PackageMetadata) error {
		keys[record.Key()] = true
		return nil
	}); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(keys))
	for key := range keys {
		names = append(names, key)
	}
	sort.Strings(names)
	result := make([]*metadata.PackageMetadata, 0, len(names))
	for _, key := range names {
		best, err := BestVisible(db, cfg, key)
		if err != nil {
			return nil, err
		}
		if best != nil {
			result = append(result, best)
		}
	}
	return result, nil
}

func queryWithoutUse(rule atom.Atom) string {
	rule.UseFlags = nil
	return rule.String()
}

func runtimeArch() string {
	switch runtime.GOARCH {
	case "386":
		return "x86"
	case "arm":
		return "arm"
	case "arm64":
		return "arm64"
	case "ppc":
		return "ppc"
	case "ppc64":
		return "ppc64"
	case "riscv64":
		return "riscv"
	case "s390x":
		return "s390"
	default:
		return strings.TrimSpace(runtime.GOARCH)
	}
}
