// Package vdb reads Portage's installed-package database without conflating it
// with available repository metadata.
package vdb

import (
	"context"
	"strings"

	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/gentooling"
)

// Package is the package-manager state retained for one installed CPV.
type Package struct {
	Category    string
	Package     string
	Version     string
	Slot        string
	Subslot     string
	Repository  string
	Use         []string
	IUse        []string
	Depend      string
	RDepend     string
	BDepend     string
	IDepend     string
	PDepend     string
	BuildTime   int64
	BuildID     string
	PhaseEnvABI string
	Counter     int64
	EAPI        string
	Contents    string
}

func (p Package) CP() string  { return p.Category + "/" + p.Package }
func (p Package) CPV() string { return p.CP() + "-" + p.Version }

// Metadata converts installed state into the common dependency metadata shape
// without implying that the record came from an available repository.
func (p Package) Metadata() *metadata.PackageMetadata {
	return &metadata.PackageMetadata{
		Repository: p.Repository, Category: p.Category, Package: p.Package, Version: p.Version,
		SLOT: p.Slot, Subslot: p.Subslot, IUSE: strings.Join(p.IUse, " "), EAPI: p.EAPI,
		DEPEND: p.Depend, RDEPEND: p.RDepend, BDEPEND: p.BDepend, IDEPEND: p.IDepend, PDEPEND: p.PDepend,
	}
}

// Scan returns every valid installed CPV in deterministic CPV order.
func Scan(root string) ([]Package, error) {
	packages, _, err := ScanWithIssues(context.Background(), root, true)
	return packages, err
}

// ScanResolverState returns installed package identity and dependency policy
// without retaining CONTENTS payloads. It still requires a regular CONTENTS
// file as part of the minimum committed VDB record.
func ScanResolverState(root string) ([]Package, error) {
	packages, _, err := ScanWithIssues(context.Background(), root, false)
	return packages, err
}

// ScanWithIssues retains Gentooling's typed evidence diagnostics for callers
// that need to distinguish interrupted merges from corrupt or unreadable VDB
// records. Resolver scans intentionally use partial mode because uncommitted
// records must not enter package state.
func ScanWithIssues(ctx context.Context, root string, includeContents bool) ([]Package, []gentooling.Issue, error) {
	inventory, err := gentooling.ReadInstalled(ctx, gentooling.SystemPaths{VDB: root}, gentooling.InstalledOptions{
		Integrity:       gentooling.AllowPartial,
		IncludeContents: includeContents,
	})
	if err != nil {
		return nil, nil, err
	}
	packages := make([]Package, 0, len(inventory.Packages))
	for _, installed := range inventory.Packages {
		packages = append(packages, Package{
			Category: installed.ID.Category, Package: installed.ID.Name, Version: installed.ID.Version,
			Slot: installed.ID.Slot, Subslot: installed.ID.Subslot, Repository: installed.ID.Repository,
			Use: append([]string(nil), installed.EnabledUse...), IUse: append([]string(nil), installed.DeclaredUse...),
			Depend: installed.Dependencies.Depend, RDepend: installed.Dependencies.RDepend,
			BDepend: installed.Dependencies.BDepend, IDepend: installed.Dependencies.IDepend,
			PDepend: installed.Dependencies.PDepend, EAPI: installed.EAPI,
			BuildTime: installed.Build.Time, BuildID: installed.Build.ID,
			PhaseEnvABI: installed.Build.PhaseEnvABI, Counter: installed.Build.Counter,
			Contents: installed.Contents,
		})
	}
	return packages, inventory.Issues, nil
}
