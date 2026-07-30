package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/installedquery"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/packagequery"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/repositoryquery"
	"github.com/airencracken/arise/internal/virtualquery"
)

func runQuery(cmdArgs []string, dbPath string) int {
	if len(cmdArgs) > 0 && cmdArgs[0] == "--ebuild" {
		if len(cmdArgs) != 2 {
			fmt.Fprintln(os.Stderr, "query --ebuild: require exactly one package atom")
			return 2
		}
		path, err := packagequery.Which(*repoPath, cmdArgs[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "query --ebuild: %v\n", err)
			return 1
		}
		fmt.Println(path)
		return 0
	}
	if len(cmdArgs) > 0 && cmdArgs[0] == "--best-visible" {
		return runBestVisibleQuery(cmdArgs[1:], dbPath)
	}
	if len(cmdArgs) > 0 && cmdArgs[0] == "--all-best-visible" {
		if len(cmdArgs) != 1 {
			fmt.Fprintln(os.Stderr, "query --all-best-visible: does not accept package atoms")
			return 2
		}
		return runAllBestVisibleQuery(dbPath)
	}
	if len(cmdArgs) > 0 && cmdArgs[0] == "--expand-virtual" {
		if len(cmdArgs) != 2 {
			fmt.Fprintln(os.Stderr, "query --expand-virtual: require exactly one package atom")
			return 2
		}
		cfg, err := portage.LoadEffectiveConfig(*portageConfigRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "query --expand-virtual: loading portage config: %v\n", err)
			return 1
		}
		providers, err := virtualquery.Expand(cfg, cmdArgs[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "query --expand-virtual: %v\n", err)
			return 2
		}
		for _, provider := range providers {
			fmt.Println(provider)
		}
		return 0
	}
	if len(cmdArgs) > 0 && (cmdArgs[0] == "--metadata" || strings.HasPrefix(cmdArgs[0], "--metadata=")) {
		return runMetadataQuery(cmdArgs, dbPath)
	}
	allVersions := false
	if len(cmdArgs) > 0 && cmdArgs[0] == "--versions" {
		allVersions = true
		cmdArgs = cmdArgs[1:]
	}
	if len(cmdArgs) == 0 {
		fmt.Fprintf(os.Stderr, "query: missing package atom argument\n")
		return 2
	}
	a, err := atom.Parse(cmdArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "query: parsing atom %q: %v\n", cmdArgs[0], err)
		return 2
	}

	db, err := ingest.OpenReadOnlyDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query: open db: %v\n", err)
		return 1
	}
	defer db.Close()

	if allVersions {
		records, err := ingest.QueryVersions(db, a.Key())
		if err != nil {
			fmt.Fprintf(os.Stderr, "query: %v\n", err)
			return 1
		}
		sort.Slice(records, func(i, j int) bool {
			if records[i].Version != records[j].Version {
				return records[i].Version < records[j].Version
			}
			return records[i].Repository < records[j].Repository
		})
		for _, m := range records {
			if a.Version != nil && m.Version != a.Version.Raw {
				continue
			}
			fmt.Printf("%s::%s\n", m.CPV(), m.Repository)
		}
		return 0
	}
	m, err := ingest.Query(db, a.Key())
	if err != nil {
		fmt.Fprintf(os.Stderr, "query: %v\n", err)
		return 1
	}
	if m == nil {
		fmt.Printf("package %s not found\n", a.Key())
		return 1
	}
	_ = metadata.PackageMetadata{}
	fmt.Printf("package: %s/%s-%s\n", m.Category, m.Package, m.Version)
	fmt.Printf("  description: %s\n", m.DESCRIPTION)
	fmt.Printf("  homepage:    %s\n", m.HOMEPAGE)
	fmt.Printf("  license:     %s\n", m.LICENSE)
	return 0
}

func runBestVisibleQuery(atoms []string, dbPath string) int {
	queryType := "ebuild"
	filtered := atoms[:0]
	for _, argument := range atoms {
		if strings.HasPrefix(argument, "--type=") {
			queryType = strings.TrimPrefix(argument, "--type=")
			continue
		}
		filtered = append(filtered, argument)
	}
	atoms = filtered
	if len(atoms) == 0 {
		fmt.Fprintln(os.Stderr, "query --best-visible: require at least one package atom")
		return 2
	}
	if queryType == "installed" {
		found := false
		for _, query := range atoms {
			best, err := installedquery.Best(*vdbDir, query, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "query --best-visible: %v\n", err)
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
	if queryType == "binary" {
		index, err := binpkg.ReadPackagesIndex(filepath.Join(*binpkgDir, "Packages"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "query --best-visible: %v\n", err)
			return 1
		}
		cfg, err := portage.LoadEffectiveConfig(*portageConfigRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "query --best-visible: loading portage config: %v\n", err)
			return 1
		}
		found := false
		for _, query := range atoms {
			entry, err := repositoryquery.BestBinary(index, cfg, query)
			if err != nil {
				fmt.Fprintf(os.Stderr, "query --best-visible: %v\n", err)
				return 2
			}
			if entry != nil {
				fmt.Println(entry["CPV"])
				found = true
			}
		}
		if !found {
			return 1
		}
		return 0
	}
	if queryType != "ebuild" {
		fmt.Fprintf(os.Stderr, "query --best-visible: unknown type %q; expected ebuild, binary, or installed\n", queryType)
		return 2
	}
	db, err := ingest.OpenReadOnlyDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query --best-visible: open db: %v\n", err)
		return 1
	}
	defer db.Close()
	cfg, err := portage.LoadEffectiveConfig(*portageConfigRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query --best-visible: loading portage config: %v\n", err)
		return 1
	}
	found := false
	for _, query := range atoms {
		best, err := repositoryquery.BestVisible(db, cfg, query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "query --best-visible: %v\n", err)
			return 2
		}
		if best != nil {
			fmt.Println(best.CPV())
			found = true
		}
	}
	if !found {
		return 1
	}
	return 0
}

func runAllBestVisibleQuery(dbPath string) int {
	db, err := ingest.OpenReadOnlyDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query --all-best-visible: open db: %v\n", err)
		return 1
	}
	defer db.Close()
	cfg, err := portage.LoadEffectiveConfig(*portageConfigRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query --all-best-visible: loading portage config: %v\n", err)
		return 1
	}
	records, err := repositoryquery.AllBestVisible(db, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query --all-best-visible: %v\n", err)
		return 1
	}
	for _, record := range records {
		fmt.Println(record.CPV())
	}
	return 0
}

func runMetadataQuery(args []string, dbPath string) int {
	var keys []string
	switch {
	case args[0] == "--metadata":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "query --metadata: require comma-separated keys and one package atom")
			return 2
		}
		keys = splitMetadataKeys(args[1])
		args = args[2:]
	default:
		keys = splitMetadataKeys(strings.TrimPrefix(args[0], "--metadata="))
		args = args[1:]
	}
	queryType := "ebuild"
	filtered := args[:0]
	for _, argument := range args {
		if strings.HasPrefix(argument, "--type=") {
			queryType = strings.TrimPrefix(argument, "--type=")
			continue
		}
		filtered = append(filtered, argument)
	}
	args = filtered
	if len(keys) == 0 || len(args) != 1 {
		fmt.Fprintln(os.Stderr, "query --metadata: require metadata keys and exactly one package atom")
		return 2
	}
	if queryType == "installed" {
		return runInstalledMetadataQuery(keys, args[0])
	}
	if queryType == "binary" {
		return runBinaryMetadataQuery(keys, args[0])
	}
	if queryType != "ebuild" {
		fmt.Fprintf(os.Stderr, "query --metadata: unknown type %q; expected ebuild, binary, or installed\n", queryType)
		return 2
	}
	db, err := ingest.OpenReadOnlyDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query --metadata: open db: %v\n", err)
		return 1
	}
	defer db.Close()
	cfg, err := portage.LoadEffectiveConfig(*portageConfigRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query --metadata: loading portage config: %v\n", err)
		return 1
	}
	rule, parseErr := atom.ParsePackageAtom(args[0])
	if parseErr != nil {
		fmt.Fprintf(os.Stderr, "query --metadata: %v\n", parseErr)
		return 2
	}
	var record *metadata.PackageMetadata
	if rule.Version != nil {
		record, err = repositoryquery.BestMatching(db, args[0])
	} else {
		record, err = repositoryquery.BestVisible(db, cfg, args[0])
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "query --metadata: %v\n", err)
		return 2
	}
	if record == nil {
		return 1
	}
	for _, key := range keys {
		value, ok := repositoryMetadataValue(record, key)
		if !ok {
			fmt.Fprintf(os.Stderr, "query --metadata: unsupported key %q\n", key)
			return 2
		}
		fmt.Println(value)
	}
	return 0
}

func runInstalledMetadataQuery(keys []string, query string) int {
	best, err := installedquery.Best(*vdbDir, query, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query --metadata: %v\n", err)
		return 2
	}
	if best == "" {
		return 1
	}
	parsed, err := atom.Parse(best)
	if err != nil || parsed.Version == nil {
		return 1
	}
	directory := filepath.Join(*vdbDir, parsed.Category, parsed.Package+"-"+parsed.Version.Raw)
	for _, key := range keys {
		key = strings.ToUpper(key)
		if !repositoryMetadataKeySupported(key) {
			fmt.Fprintf(os.Stderr, "query --metadata: unsupported key %q\n", key)
			return 2
		}
		if key == "INHERIT" {
			key = "INHERITED"
		}
		value, err := os.ReadFile(filepath.Join(directory, key))
		if err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "query --metadata: read %s: %v\n", key, err)
			return 1
		}
		fmt.Println(strings.TrimSpace(string(value)))
	}
	return 0
}

func runBinaryMetadataQuery(keys []string, query string) int {
	index, err := binpkg.ReadPackagesIndex(filepath.Join(*binpkgDir, "Packages"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "query --metadata: %v\n", err)
		return 1
	}
	cfg, err := portage.LoadEffectiveConfig(*portageConfigRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query --metadata: loading portage config: %v\n", err)
		return 1
	}
	entry, err := repositoryquery.BestBinary(index, cfg, query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query --metadata: %v\n", err)
		return 2
	}
	if entry == nil {
		return 1
	}
	for _, key := range keys {
		key = strings.ToUpper(key)
		if !repositoryMetadataKeySupported(key) {
			fmt.Fprintf(os.Stderr, "query --metadata: unsupported key %q\n", key)
			return 2
		}
		if key == "INHERIT" {
			key = "INHERITED"
		}
		fmt.Println(entry[key])
	}
	return 0
}

func splitMetadataKeys(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
}

func repositoryMetadataValue(record *metadata.PackageMetadata, key string) (string, bool) {
	values := map[string]string{
		"BDEPEND": record.BDEPEND, "DEFINED_PHASES": record.DEFINED_PHASES,
		"DEPEND": record.DEPEND, "DESCRIPTION": record.DESCRIPTION, "EAPI": record.EAPI,
		"HOMEPAGE": record.HOMEPAGE, "IDEPEND": record.IDEPEND, "INHERIT": record.INHERITED,
		"INHERITED": record.INHERITED, "IUSE": record.IUSE, "KEYWORDS": record.KEYWORDS,
		"LICENSE": record.LICENSE, "PDEPEND": record.PDEPEND, "PROPERTIES": record.PROPERTIES,
		"RDEPEND": record.RDEPEND, "REQUIRED_USE": record.REQUIRED_USE, "RESTRICT": record.RESTRICT,
		"SLOT": record.SLOT, "SRC_URI": record.SRC_URI,
	}
	value, ok := values[strings.ToUpper(key)]
	return value, ok
}

func repositoryMetadataKeySupported(key string) bool {
	_, ok := repositoryMetadataValue(&metadata.PackageMetadata{}, key)
	return ok
}
