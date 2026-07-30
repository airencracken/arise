// Package installedquery evaluates PMS package queries against an installed
// package database without importing Portage or mutating VDB state.
package installedquery

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/portage"
)

type Package struct {
	CPV        string
	Slot       string
	Repository string
	Use        map[string]bool
	IUse       map[string]bool
	Version    *atom.Version
}

func Match(vdbDir, query string, callerUse map[string]bool) (bool, error) {
	matches, err := Matches(vdbDir, query, callerUse)
	return len(matches) != 0, err
}

// Matches returns every installed CPV satisfying a PMS package atom in
// ascending version order. It is the shared primitive for presence, match,
// and best-version queries so their atom semantics cannot drift.
func Matches(vdbDir, query string, callerUse map[string]bool) ([]string, error) {
	rule, err := atom.ParsePackageAtom(query)
	if err != nil {
		return nil, err
	}
	packages, err := candidates(vdbDir, rule)
	if err != nil {
		return nil, err
	}
	var matches []Package
	for _, installed := range packages {
		if portage.PackageAtomMatches(queryWithoutUse(*rule), installed.CPV, installed.Slot, installed.Repository) &&
			useDependenciesMatch(rule.UseFlags, installed.Use, installed.IUse, callerUse) {
			matches = append(matches, installed)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		cmp := matches[i].Version.Compare(matches[j].Version)
		if cmp != 0 {
			return cmp < 0
		}
		return matches[i].CPV < matches[j].CPV
	})
	result := make([]string, len(matches))
	for index := range matches {
		result[index] = matches[index].CPV
	}
	return result, nil
}

func Best(vdbDir, query string, callerUse map[string]bool) (string, error) {
	matches, err := Matches(vdbDir, query, callerUse)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", nil
	}
	return matches[len(matches)-1], nil
}

func candidates(vdbDir string, rule *atom.Atom) ([]Package, error) {
	categoryDir := filepath.Join(vdbDir, rule.Category)
	entries, err := os.ReadDir(categoryDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("installed query: read category: %w", err)
	}
	var result []Package
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		cpv := rule.Category + "/" + entry.Name()
		parsed, parseErr := atom.Parse(cpv)
		if parseErr != nil || parsed.Package != rule.Package || parsed.Version == nil {
			continue
		}
		path := filepath.Join(categoryDir, entry.Name())
		result = append(result, Package{
			CPV: cpv, Version: parsed.Version,
			Slot:       strings.TrimSpace(read(path, "SLOT")),
			Repository: strings.TrimSpace(read(path, "repository")),
			Use:        words(read(path, "USE"), false),
			IUse:       words(read(path, "IUSE"), true),
		})
	}
	return result, nil
}

func read(directory, name string) string {
	data, _ := os.ReadFile(filepath.Join(directory, name))
	return string(data)
}

func words(value string, stripDefaults bool) map[string]bool {
	result := make(map[string]bool)
	for _, word := range strings.Fields(value) {
		if stripDefaults {
			word = strings.TrimPrefix(strings.TrimPrefix(word, "+"), "-")
		}
		if word != "" {
			result[word] = true
		}
	}
	return result
}

func queryWithoutUse(rule atom.Atom) string {
	rule.UseFlags = nil
	return rule.String()
}

func useDependenciesMatch(requirements []atom.UseFlag, enabled, declared, caller map[string]bool) bool {
	for _, requirement := range requirements {
		required := requirement.Enabled
		constrained := true
		if requirement.Conditional {
			trigger := caller[requirement.Name]
			if requirement.Negated {
				trigger = !trigger
			}
			constrained = trigger
			required = true
		} else if requirement.Equal {
			required = caller[requirement.Name]
			if requirement.Negated {
				required = !required
			}
		}
		if !constrained {
			continue
		}
		actual, exists := enabled[requirement.Name]
		if !declared[requirement.Name] {
			if requirement.Default == nil {
				return false
			}
			actual, exists = *requirement.Default, true
		}
		if !exists {
			actual = false
		}
		if actual != required {
			return false
		}
	}
	return true
}
