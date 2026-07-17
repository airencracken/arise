// Package packagestate produces deterministic, lossless state snapshots for
// differential testing and diagnostics.
package packagestate

import (
	"fmt"
	"sort"

	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/vdb"
	"github.com/dgraph-io/badger/v4"
)

const Schema = 1

type AvailableRecord struct {
	CPV        string `json:"cpv"`
	Repository string `json:"repository"`
	Path       string `json:"repository_path,omitempty"`
	Slot       string `json:"slot"`
	Subslot    string `json:"subslot,omitempty"`
	EAPI       string `json:"eapi"`
}

type InstalledRecord struct {
	CPV        string `json:"cpv"`
	Repository string `json:"repository,omitempty"`
	Slot       string `json:"slot"`
	Subslot    string `json:"subslot,omitempty"`
	EAPI       string `json:"eapi"`
}

type Snapshot struct {
	Schema    int               `json:"schema"`
	Available []AvailableRecord `json:"available"`
	Installed []InstalledRecord `json:"installed"`
}

// Capture reads immutable repository and filesystem-sovereign installed state.
func Capture(db *badger.DB, vdbPath string) (Snapshot, error) {
	snapshot := Snapshot{Schema: Schema, Available: []AvailableRecord{}, Installed: []InstalledRecord{}}
	return capture(db, vdbPath, snapshot)
}

func capture(db *badger.DB, vdbPath string, snapshot Snapshot) (Snapshot, error) {
	err := ingest.QueryRange(db, "pkg:", func(m *metadata.PackageMetadata) error {
		snapshot.Available = append(snapshot.Available, AvailableRecord{
			CPV: m.CPV(), Repository: m.Repository, Path: m.RepositoryPath,
			Slot: m.SLOT, Subslot: m.Subslot, EAPI: m.EAPI,
		})
		return nil
	})
	if err != nil {
		return Snapshot{}, fmt.Errorf("state: read available packages: %w", err)
	}
	installed, err := vdb.Scan(vdbPath)
	if err != nil {
		return Snapshot{}, fmt.Errorf("state: read installed packages: %w", err)
	}
	for _, p := range installed {
		snapshot.Installed = append(snapshot.Installed, InstalledRecord{
			CPV: p.CPV(), Repository: p.Repository, Slot: p.Slot, Subslot: p.Subslot, EAPI: p.EAPI,
		})
	}
	sort.Slice(snapshot.Available, func(i, j int) bool {
		a, b := snapshot.Available[i], snapshot.Available[j]
		if a.CPV != b.CPV {
			return a.CPV < b.CPV
		}
		if a.Repository != b.Repository {
			return a.Repository < b.Repository
		}
		return a.Path < b.Path
	})
	sort.Slice(snapshot.Installed, func(i, j int) bool {
		a, b := snapshot.Installed[i], snapshot.Installed[j]
		if a.CPV != b.CPV {
			return a.CPV < b.CPV
		}
		return a.Repository < b.Repository
	})
	return snapshot, nil
}
