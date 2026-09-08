package main

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/resolve"
)

func prepareUninstallTargets(vdb string, targets []string) ([]*atom.Atom, []string, error) {
	var atoms []*atom.Atom
	var paths []string
	for _, cpv := range targets {
		parsed, err := atom.Parse(cpv)
		if err != nil || parsed.Version == nil {
			return nil, nil, fmt.Errorf("invalid resolved installed package %q", cpv)
		}
		path := filepath.Join(vdb, parsed.Category, parsed.Package+"-"+parsed.Version.Raw)
		if err := validateUninstallVDB(path); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", cpv, err)
		}
		atoms = append(atoms, parsed)
		paths = append(paths, path)
	}
	if err := validateELFRemovalOrder(vdb, targets); err != nil {
		return nil, nil, fmt.Errorf("refusing removal: %w", err)
	}
	return atoms, paths, nil
}

func confirmUninstall(input io.Reader, output io.Writer, targets []string) bool {
	fmt.Fprintln(output, "Packages to remove:")
	for _, target := range targets {
		fmt.Fprintf(output, "  %s\n", target)
	}
	return confirmOperation(input, output, "Proceed with removal? [y/N] ")
}

func removePreexistingUninstallConflicts(result, baseline *resolve.ResolveResult) {
	if result == nil || baseline == nil || len(result.Conflicts) == 0 {
		return
	}
	preexisting := make(map[string]bool, len(baseline.Conflicts))
	for _, conflict := range baseline.Conflicts {
		preexisting[conflict] = true
	}
	novel := result.Conflicts[:0]
	for _, conflict := range result.Conflicts {
		if !preexisting[conflict] {
			novel = append(novel, conflict)
		}
	}
	result.Conflicts = novel
	if len(novel) != 0 || result.Incomplete != nil {
		return
	}
	result.Verified = true
	result.Verification = resolve.VerificationVerified
	if len(baseline.Conflicts) != 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%d pre-existing verification conflict(s) unchanged by removal", len(baseline.Conflicts)))
	}
}
