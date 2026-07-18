// Package packagestate produces deterministic, lossless state snapshots for
// differential testing and diagnostics.
package packagestate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/vdb"
	"github.com/dgraph-io/badger/v4"
)

const Schema = 2

type AvailableRecord struct {
	CPV         string `json:"cpv"`
	Repository  string `json:"repository"`
	Path        string `json:"repository_path,omitempty"`
	Slot        string `json:"slot"`
	Subslot     string `json:"subslot,omitempty"`
	EAPI        string `json:"eapi"`
	Priority    int    `json:"repository_priority,omitempty"`
	DEPEND      string `json:"depend,omitempty"`
	RDEPEND     string `json:"rdepend,omitempty"`
	BDEPEND     string `json:"bdepend,omitempty"`
	IDEPEND     string `json:"idepend,omitempty"`
	PDEPEND     string `json:"pdepend,omitempty"`
	IUSE        string `json:"iuse,omitempty"`
	KEYWORDS    string `json:"keywords,omitempty"`
	LICENSE     string `json:"license,omitempty"`
	REQUIREDUSE string `json:"required_use,omitempty"`
	SRCURI      string `json:"src_uri,omitempty"`
}

// Decode validates a captured snapshot before it is admitted to a replay.
func Decode(reader io.Reader) (Snapshot, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var snapshot Snapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("state: decode: %w", err)
	}
	if snapshot.Schema != Schema {
		return Snapshot{}, fmt.Errorf("state: unsupported schema %d (want %d)", snapshot.Schema, Schema)
	}
	want := snapshot.Fingerprint
	got, err := snapshot.digest()
	if err != nil {
		return Snapshot{}, err
	}
	if want == "" || want != got {
		return Snapshot{}, fmt.Errorf("state: fingerprint mismatch: got %q want %q", want, got)
	}
	return snapshot, nil
}

// AvailableMetadata reconstructs the immutable repository view used by graph
// construction. The returned records do not alias the snapshot.
func (snapshot Snapshot) AvailableMetadata() ([]*metadata.PackageMetadata, error) {
	result := make([]*metadata.PackageMetadata, 0, len(snapshot.Available))
	for _, record := range snapshot.Available {
		category, pkg, version, err := metadata.ParseCPV(record.CPV)
		if err != nil || version == "" {
			return nil, fmt.Errorf("state: invalid available CPV %q", record.CPV)
		}
		result = append(result, &metadata.PackageMetadata{
			Repository: record.Repository, RepositoryPath: record.Path,
			RepositoryPriority: record.Priority, Category: category, Package: pkg, Version: version,
			SLOT: record.Slot, Subslot: record.Subslot, EAPI: record.EAPI,
			DEPEND: record.DEPEND, RDEPEND: record.RDEPEND, BDEPEND: record.BDEPEND,
			IDEPEND: record.IDEPEND, PDEPEND: record.PDEPEND, IUSE: record.IUSE,
			KEYWORDS: record.KEYWORDS, LICENSE: record.LICENSE,
			REQUIRED_USE: record.REQUIREDUSE, SRC_URI: record.SRCURI,
		})
	}
	return result, nil
}

// InstalledPackages reconstructs the immutable VDB view used by graph
// construction. CONTENTS is intentionally excluded because it does not affect
// dependency resolution and can expose host paths.
func (snapshot Snapshot) InstalledPackages() ([]vdb.Package, error) {
	result := make([]vdb.Package, 0, len(snapshot.Installed))
	for _, record := range snapshot.Installed {
		category, pkg, version, err := metadata.ParseCPV(record.CPV)
		if err != nil || version == "" {
			return nil, fmt.Errorf("state: invalid installed CPV %q", record.CPV)
		}
		result = append(result, vdb.Package{
			Category: category, Package: pkg, Version: version, Repository: record.Repository,
			Slot: record.Slot, Subslot: record.Subslot, EAPI: record.EAPI,
			Use: append([]string(nil), record.USE...), IUse: append([]string(nil), record.IUSE...),
			Depend: record.DEPEND, RDepend: record.RDEPEND, BDepend: record.BDEPEND,
			IDepend: record.IDEPEND, PDepend: record.PDEPEND,
			BuildTime: record.BuildTime, BuildID: record.BuildID, Counter: record.Counter,
		})
	}
	return result, nil
}

type InstalledRecord struct {
	CPV        string   `json:"cpv"`
	Repository string   `json:"repository,omitempty"`
	Slot       string   `json:"slot"`
	Subslot    string   `json:"subslot,omitempty"`
	EAPI       string   `json:"eapi"`
	USE        []string `json:"use,omitempty"`
	IUSE       []string `json:"iuse,omitempty"`
	DEPEND     string   `json:"depend,omitempty"`
	RDEPEND    string   `json:"rdepend,omitempty"`
	BDEPEND    string   `json:"bdepend,omitempty"`
	IDEPEND    string   `json:"idepend,omitempty"`
	PDEPEND    string   `json:"pdepend,omitempty"`
	BuildTime  int64    `json:"build_time,omitempty"`
	BuildID    string   `json:"build_id,omitempty"`
	Counter    int64    `json:"counter,omitempty"`
}

type Snapshot struct {
	Schema      int               `json:"schema"`
	Fingerprint string            `json:"fingerprint,omitempty"`
	Available   []AvailableRecord `json:"available"`
	Installed   []InstalledRecord `json:"installed"`
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
			Priority: m.RepositoryPriority, DEPEND: m.DEPEND, RDEPEND: m.RDEPEND,
			BDEPEND: m.BDEPEND, IDEPEND: m.IDEPEND, PDEPEND: m.PDEPEND,
			IUSE: m.IUSE, KEYWORDS: m.KEYWORDS, LICENSE: m.LICENSE,
			REQUIREDUSE: m.REQUIRED_USE, SRCURI: m.SRC_URI,
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
			USE: append([]string(nil), p.Use...), IUSE: append([]string(nil), p.IUse...),
			DEPEND: p.Depend, RDEPEND: p.RDepend, BDEPEND: p.BDepend,
			IDEPEND: p.IDepend, PDEPEND: p.PDepend, BuildTime: p.BuildTime,
			BuildID: p.BuildID, Counter: p.Counter,
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
	fingerprint, err := snapshot.digest()
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.Fingerprint = fingerprint
	return snapshot, nil
}

// Portable returns a copy whose repository paths no longer expose or depend on
// host filesystem layout. Repository names remain intact because ::repository
// atoms require them for a faithful resolver replay.
func (snapshot Snapshot) Portable() (Snapshot, error) {
	snapshot.Fingerprint = ""
	for i := range snapshot.Available {
		path := strings.TrimSpace(snapshot.Available[i].Path)
		if path == "" {
			continue
		}
		name := snapshot.Available[i].Repository
		if name == "" {
			name = filepath.Base(path)
		}
		snapshot.Available[i].Path = filepath.ToSlash(filepath.Join("repositories", name))
	}
	fingerprint, err := snapshot.digest()
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.Fingerprint = fingerprint
	return snapshot, nil
}

func (snapshot Snapshot) digest() (string, error) {
	snapshot.Fingerprint = ""
	data, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("state: fingerprint: %w", err)
	}
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
