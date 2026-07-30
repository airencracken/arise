package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/installedquery"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/packagequery"
)

func runInstalled(args []string, dbPath, vdbPath string) int {
	if len(args) > 0 {
		switch args[0] {
		case "--match":
			return runInstalledMatch(args[1:], vdbPath)
		case "--has":
			return runInstalledHas(args[1:], vdbPath)
		case "--best":
			return runInstalledBest(args[1:], vdbPath)
		case "--contents":
			return runInstalledContents(args[1:], vdbPath)
		case "--owner":
			return runInstalledOwner(args[1:], vdbPath)
		case "--uses":
			return runInstalledUses(args[1:], dbPath, vdbPath)
		case "--size":
			return runInstalledSize(args[1:], vdbPath)
		case "--check":
			return runInstalledCheck(args[1:], vdbPath)
		}
	}
	withVersions := false
	nul := false
	quiet := true
	quietRequested := false
	prefixEqual := false
	selector := ""
	for _, arg := range args {
		switch arg {
		case "--versions", "--cpv":
			withVersions = true
		case "--null", "-0":
			nul = true
			quietRequested = true
		case "-=":
			prefixEqual = true
			withVersions = true
		case "-q":
			quietRequested = true
		case "-a":
			selector = "all"
			withVersions = true
			quietRequested = true
		case "all", "repo", "no-repo", "buildtime", "no-buildtime":
			selector = arg
			withVersions = true
		default:
			fmt.Fprintf(os.Stderr, "installed: unknown option %s\n", arg)
			return 2
		}
	}
	if selector != "" && !quietRequested {
		quiet = false
	}
	separator := "\n"
	if nul {
		separator = "\x00"
	}
	out := bufio.NewWriterSize(os.Stdout, 64*1024)
	defer out.Flush()
	if selector == "" {
		if err := writeInstalled(out, vdbPath, withVersions, prefixEqual, separator); err != nil {
			fmt.Fprintf(os.Stderr, "installed: %v\n", err)
			return 1
		}
		return 0
	}
	records, err := scanInstalled(vdbPath, selector)
	if err != nil {
		fmt.Fprintf(os.Stderr, "installed: %v\n", err)
		return 1
	}
	if !quiet {
		fmt.Fprintln(out, installedHeading(selector))
		fmt.Fprintln(out)
	}
	printed := 0
	for _, record := range records {
		if !installedSelectorMatches(record, selector) {
			continue
		}
		atom := record.CP
		if withVersions {
			atom = record.CPV
		}
		if prefixEqual {
			atom = "=" + atom
		}
		_, _ = out.WriteString(atom)
		_, _ = out.WriteString(separator)
		printed++
	}
	if !quiet && printed == 0 {
		fmt.Fprintln(out, "none")
	}
	return 0
}

func runInstalledMatch(args []string, vdbPath string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "installed --match: require exactly one package atom")
		return 2
	}
	matches, err := installedquery.Matches(vdbPath, args[0], nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "installed --match: %v\n", err)
		return 2
	}
	for _, cpv := range matches {
		fmt.Println(cpv)
	}
	if len(matches) == 0 {
		return 1
	}
	return 0
}

func runInstalledHas(args []string, vdbPath string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "installed --has: require exactly one package atom")
		return 2
	}
	matched, err := installedquery.Match(vdbPath, args[0], nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "installed --has: %v\n", err)
		return 2
	}
	if !matched {
		return 1
	}
	return 0
}

func runInstalledBest(args []string, vdbPath string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "installed --best: require at least one package atom")
		return 2
	}
	found := false
	for _, query := range args {
		best, err := installedquery.Best(vdbPath, query, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "installed --best: %v\n", err)
			return 2
		}
		if best != "" {
			fmt.Println(best)
			found = true
		}
	}
	if !found {
		return 1
	}
	return 0
}

func runInstalledContents(args []string, vdbPath string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "installed --contents: require exactly one package atom")
		return 2
	}
	entries, err := packagequery.Contents(vdbPath, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "installed --contents: %v\n", err)
		return 1
	}
	for _, entry := range entries {
		fmt.Println(entry.Path)
	}
	return 0
}

func runInstalledOwner(args []string, vdbPath string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "installed --owner: require at least one path or basename")
		return 2
	}
	owners, err := packagequery.Owners(vdbPath, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "installed --owner: %v\n", err)
		return 1
	}
	for _, owner := range owners {
		fmt.Printf("%s: %s\n", owner.Package, owner.Path)
	}
	if len(owners) == 0 {
		return 1
	}
	return 0
}

func runInstalledUses(args []string, dbPath, vdbPath string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "installed --uses: require exactly one package atom")
		return 2
	}
	db, err := ingest.OpenReadOnlyDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "installed --uses: open db: %v\n", err)
		return 1
	}
	defer db.Close()
	iuse, active, err := packagequery.Uses(db, vdbPath, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "installed --uses: %v\n", err)
		return 1
	}
	if iuse != "" {
		fmt.Printf("IUSE: %s\n", iuse)
	}
	fmt.Printf("Active: %s\n", active)
	return 0
}

func runInstalledSize(args []string, vdbPath string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "installed --size: require exactly one package atom")
		return 2
	}
	size, err := packagequery.Size(vdbPath, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "installed --size: %v\n", err)
		return 1
	}
	fmt.Println(formatSize(size))
	return 0
}

func runInstalledCheck(args []string, vdbPath string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "installed --check: require exactly one package atom")
		return 2
	}
	mismatches, err := packagequery.Check(vdbPath, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "installed --check: %v\n", err)
		return 1
	}
	if len(mismatches) == 0 {
		fmt.Println("OK")
		return 0
	}
	for _, mismatch := range mismatches {
		fmt.Println(mismatch)
	}
	return 1
}

// writeInstalled streams the common unfiltered query directly from the VDB.
// Avoiding an intermediate record slice matters for this startup-bound command.
func writeInstalled(out *bufio.Writer, vdbPath string, withVersions, prefixEqual bool, separator string) error {
	categories, err := os.ReadDir(vdbPath)
	if err != nil {
		return err
	}
	for _, category := range categories {
		if !category.IsDir() {
			continue
		}
		packages, readErr := os.ReadDir(filepath.Join(vdbPath, category.Name()))
		if readErr != nil {
			continue
		}
		for _, pkg := range packages {
			if !pkg.IsDir() {
				continue
			}
			cat, name, version, parseErr := metadata.ParseCPV(category.Name() + "/" + pkg.Name())
			if parseErr != nil || version == "" {
				continue
			}
			if prefixEqual {
				_ = out.WriteByte('=')
			}
			_, _ = out.WriteString(cat)
			_ = out.WriteByte('/')
			_, _ = out.WriteString(name)
			if withVersions {
				_ = out.WriteByte('-')
				_, _ = out.WriteString(version)
			}
			_, _ = out.WriteString(separator)
		}
	}
	return nil
}

func installedAtoms(vdbPath string, withVersions bool) ([]string, error) {
	records, err := scanInstalled(vdbPath, "")
	if err != nil {
		return nil, err
	}
	atoms := make([]string, 0, len(records))
	for _, record := range records {
		if withVersions {
			atoms = append(atoms, record.CPV)
		} else {
			atoms = append(atoms, record.CP)
		}
	}
	atoms = uniqueSorted(atoms)
	return atoms, nil
}

type installedRecord struct {
	CP           string
	CPV          string
	HasRepo      bool
	HasBuildTime bool
}

func scanInstalled(vdbPath, selector string) ([]installedRecord, error) {
	categories, err := os.ReadDir(vdbPath)
	if err != nil {
		return nil, err
	}
	var records []installedRecord
	for _, category := range categories {
		if !category.IsDir() {
			continue
		}
		packages, readErr := os.ReadDir(filepath.Join(vdbPath, category.Name()))
		if readErr != nil {
			continue
		}
		for _, pkg := range packages {
			if !pkg.IsDir() {
				continue
			}
			categoryName, packageName, version, parseErr := metadata.ParseCPV(category.Name() + "/" + pkg.Name())
			if parseErr != nil || version == "" {
				continue
			}
			dir := filepath.Join(vdbPath, category.Name(), pkg.Name())
			record := installedRecord{CP: categoryName + "/" + packageName, CPV: categoryName + "/" + packageName + "-" + version}
			if selector == "repo" || selector == "no-repo" {
				if data, readErr := os.ReadFile(filepath.Join(dir, "repository")); readErr == nil && strings.TrimSpace(string(data)) != "" {
					record.HasRepo = true
				}
			}
			if selector == "buildtime" || selector == "no-buildtime" {
				if data, readErr := os.ReadFile(filepath.Join(dir, "BUILD_TIME")); readErr == nil && strings.TrimSpace(string(data)) != "" {
					record.HasBuildTime = true
				}
			}
			records = append(records, record)
		}
	}
	return records, nil
}

func installedSelectorMatches(record installedRecord, selector string) bool {
	switch selector {
	case "repo":
		return record.HasRepo
	case "no-repo":
		return !record.HasRepo
	case "buildtime":
		return record.HasBuildTime
	case "no-buildtime":
		return !record.HasBuildTime
	default:
		return true
	}
}

func installedHeading(selector string) string {
	switch selector {
	case "repo":
		return "The following package versions are installed with repository information:"
	case "no-repo":
		return "The following package versions are installed without repository information:"
	case "buildtime":
		return "The following package versions are installed with build-time information:"
	case "no-buildtime":
		return "The following package versions are installed without build-time information:"
	default:
		return "The following package versions are installed:"
	}
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
