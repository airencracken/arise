// Package vdb reads Portage's installed-package database without conflating it
// with available repository metadata.
package vdb

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/airencracken/arise/internal/metadata"
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
	categories, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("vdb: read root: %w", err)
	}
	var packages []Package
	for _, category := range categories {
		if !category.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, category.Name()))
		if err != nil {
			return nil, fmt.Errorf("vdb: read category %s: %w", category.Name(), err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			cat, pn, version, err := metadata.ParseCPV(category.Name() + "/" + entry.Name())
			if err != nil || version == "" {
				continue
			}
			dir := filepath.Join(root, category.Name(), entry.Name())
			// A directory name is not an installed package. Interrupted merges may
			// leave an empty or partial VDB directory before journal rollback. Only
			// records with the minimum committed Portage metadata are authoritative.
			valid := true
			for _, required := range []string{"CONTENTS", "EAPI", "SLOT", "repository"} {
				if info, statErr := os.Stat(filepath.Join(dir, required)); statErr != nil || !info.Mode().IsRegular() {
					valid = false
					break
				}
			}
			if !valid {
				continue
			}
			read := func(name string) string {
				data, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					return ""
				}
				return strings.TrimSpace(string(data))
			}
			slot, subslot := splitSlot(read("SLOT"))
			packages = append(packages, Package{
				Category: cat, Package: pn, Version: version, Slot: slot, Subslot: subslot,
				Repository: read("repository"), Use: strings.Fields(read("USE")), IUse: strings.Fields(read("IUSE")),
				Depend: read("DEPEND"), RDepend: read("RDEPEND"), BDepend: read("BDEPEND"),
				IDepend: read("IDEPEND"), PDepend: read("PDEPEND"), EAPI: read("EAPI"),
				BuildTime: parseInt(read("BUILD_TIME")), BuildID: read("BUILD_ID"),
				PhaseEnvABI: read("ARISE_PHASE_ENV_ABI"), Counter: parseInt(read("COUNTER")),
				Contents: read("CONTENTS"),
			})
		}
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].CPV() < packages[j].CPV() })
	return packages, nil
}

func splitSlot(value string) (string, string) {
	if before, after, ok := strings.Cut(value, "/"); ok {
		return before, after
	}
	return value, ""
}

func parseInt(value string) int64 {
	n, _ := strconv.ParseInt(value, 10, 64)
	return n
}
