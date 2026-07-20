package resolve

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
)

// ResolverFixture is a portable, sanitized resolver input. It intentionally
// contains no repository, configuration-root, VDB, or user filesystem paths.
type ResolverFixture struct {
	Name         string              `json:"name"`
	Targets      []string            `json:"targets"`
	World        []string            `json:"world,omitempty"`
	Config       FixtureConfig       `json:"config"`
	Versions     []FixtureVersion    `json:"versions"`
	Providers    map[string][]string `json:"providers,omitempty"`
	Expectations FixtureExpectations `json:"expectations,omitempty"`
}

type FixtureExpectations struct {
	Arise, Portage FixturePlanExpectation
}

type FixturePlanExpectation struct {
	Actions  []string `json:"actions,omitempty"`
	Verified bool     `json:"verified"`
	Partial  bool     `json:"partial,omitempty"`
}

type FixtureConfig struct {
	Update, Deep, NewUse, CompleteGraph bool `json:",omitempty"`
	Backtrack                           int  `json:"backtrack,omitempty"`
}

type FixtureVersion struct {
	CP, Version, Slot, Subslot, Repository string          `json:",omitempty"`
	Installed, Available                   bool            `json:",omitempty"`
	Use, InstalledUse, InstalledIUse       map[string]bool `json:",omitempty"`
	IUse                                   string          `json:",omitempty"`
	Depend, Rdepend, Bdepend               string          `json:",omitempty"`
	Idepend, Pdepend                       string          `json:",omitempty"`
	InstalledDepend, InstalledRdepend      string          `json:",omitempty"`
	InstalledBdepend, InstalledIdepend     string          `json:",omitempty"`
	InstalledPdepend, EAPI, InstalledEAPI  string          `json:",omitempty"`
	Keywords, RequiredUse                  string          `json:",omitempty"`
	DependencyMetadataKnown                bool            `json:"dependency_metadata_known,omitempty"`
	RepositoryPriority                     int             `json:"repository_priority,omitempty"`
}

func DecodeResolverFixture(reader io.Reader) (*ResolverFixture, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var fixture ResolverFixture
	if err := decoder.Decode(&fixture); err != nil {
		return nil, fmt.Errorf("resolver fixture: %w", err)
	}
	if fixture.Name == "" || len(fixture.Targets) == 0 || len(fixture.Versions) == 0 {
		return nil, fmt.Errorf("resolver fixture: incomplete fixture")
	}
	return &fixture, nil
}

func (f *ResolverFixture) Graph() (*DepGraph, error) {
	graph := NewDepGraph()
	for _, record := range f.Versions {
		if record.CP == "" || record.Version == "" || (!record.Installed && !record.Available) {
			return nil, fmt.Errorf("resolver fixture %q: incomplete version record", f.Name)
		}
		version := graph.AddVersionFromRepository(record.CP, record.Version, record.Slot, record.Subslot, record.Installed, record.Use, record.Keywords, record.Repository)
		version.Available = record.Available
		version.Installed = record.Installed
		version.Depend, version.Rdepend, version.Bdepend = record.Depend, record.Rdepend, record.Bdepend
		version.Idepend, version.Pdepend = record.Idepend, record.Pdepend
		version.InstalledDepend, version.InstalledRdepend = record.InstalledDepend, record.InstalledRdepend
		version.InstalledBdepend, version.InstalledIdepend = record.InstalledBdepend, record.InstalledIdepend
		version.InstalledPdepend, version.EAPI, version.InstalledEAPI = record.InstalledPdepend, record.EAPI, record.InstalledEAPI
		version.UseFlags, version.InstalledUseFlags, version.InstalledIUseFlags = record.Use, record.InstalledUse, record.InstalledIUse
		version.IUse = record.IUse
		version.RequiredUse = record.RequiredUse
		version.DependencyMetadataKnown = record.DependencyMetadataKnown
		version.RepositoryPriority = record.RepositoryPriority
	}
	for virtual, providers := range f.Providers {
		for _, provider := range providers {
			graph.AddProvider(virtual, provider)
		}
	}
	return graph, nil
}

func EncodeResolverFixture(writer io.Writer, fixture *ResolverFixture) error {
	if fixture == nil {
		return fmt.Errorf("resolver fixture: nil fixture")
	}
	normalized := *fixture
	normalized.Targets, normalized.World = append([]string(nil), fixture.Targets...), append([]string(nil), fixture.World...)
	sort.Strings(normalized.Targets)
	sort.Strings(normalized.World)
	normalized.Versions = append([]FixtureVersion(nil), fixture.Versions...)
	sort.SliceStable(normalized.Versions, func(i, j int) bool {
		a, b := normalized.Versions[i], normalized.Versions[j]
		return a.CP+"\x00"+a.Version+"\x00"+a.Slot+"\x00"+a.Repository < b.CP+"\x00"+b.Version+"\x00"+b.Slot+"\x00"+b.Repository
	})
	normalized.Providers = make(map[string][]string, len(fixture.Providers))
	for virtual, providers := range fixture.Providers {
		normalized.Providers[virtual] = append([]string(nil), providers...)
		sort.Strings(normalized.Providers[virtual])
	}
	normalized.Expectations.Arise.Actions = append([]string(nil), fixture.Expectations.Arise.Actions...)
	normalized.Expectations.Portage.Actions = append([]string(nil), fixture.Expectations.Portage.Actions...)
	sort.Strings(normalized.Expectations.Arise.Actions)
	sort.Strings(normalized.Expectations.Portage.Actions)
	encoded, err := json.MarshalIndent(&normalized, "", "  ")
	if err != nil {
		return fmt.Errorf("resolver fixture: encode: %w", err)
	}
	if strings.Contains(string(encoded), "\\u0000") || strings.Contains(string(encoded), `: "/`) || strings.Contains(string(encoded), "/home/") || strings.Contains(string(encoded), "/etc/portage/") {
		return fmt.Errorf("resolver fixture: private host path or control data rejected")
	}
	if _, err := writer.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("resolver fixture: write: %w", err)
	}
	return nil
}

// ReduceResolverFixture deterministically retains only the requested CPs and
// provider relationships. Callers derive keep from the captured root-cause
// closure, then re-run normalized expectations to prove the reduction valid.
func ReduceResolverFixture(fixture *ResolverFixture, keep []string) (*ResolverFixture, error) {
	if fixture == nil {
		return nil, fmt.Errorf("resolver fixture: nil fixture")
	}
	keep = append([]string(nil), keep...)
	sort.Strings(keep)
	keep = slices.Compact(keep)
	kept := make(map[string]bool, len(keep))
	for _, cp := range keep {
		kept[cp] = true
	}
	result := *fixture
	result.Versions = nil
	result.Providers = make(map[string][]string)
	for _, version := range fixture.Versions {
		if kept[version.CP] {
			result.Versions = append(result.Versions, version)
		}
	}
	for virtual, providers := range fixture.Providers {
		if !kept[virtual] {
			continue
		}
		for _, provider := range providers {
			if kept[provider] {
				result.Providers[virtual] = append(result.Providers[virtual], provider)
			}
		}
	}
	if len(result.Versions) == 0 {
		return nil, fmt.Errorf("resolver fixture: reduction removed every version")
	}
	return &result, nil
}

func (f *ResolverFixture) ResolveConfig() ResolveConfig {
	config := DefaultResolveConfig()
	config.Update, config.Deep = f.Config.Update, f.Config.Deep
	config.NewUse, config.CompleteGraph = f.Config.NewUse, f.Config.CompleteGraph
	if f.Config.Backtrack > 0 {
		config.Backtrack = f.Config.Backtrack
	}
	if len(f.World) != 0 {
		config.WorldSet = &WorldSet{Entries: append([]string(nil), f.World...)}
	}
	return config
}
