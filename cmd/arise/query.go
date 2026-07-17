package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/metadata"
)

func runQuery(cmdArgs []string, dbPath string) {
	allVersions := false
	if len(cmdArgs) > 0 && cmdArgs[0] == "--versions" {
		allVersions = true
		cmdArgs = cmdArgs[1:]
	}
	if len(cmdArgs) == 0 {
		fmt.Fprintf(os.Stderr, "query: missing package atom argument\n")
		os.Exit(1)
	}
	a, err := atom.Parse(cmdArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "query: parsing atom %q: %v\n", cmdArgs[0], err)
		os.Exit(1)
	}

	db, err := ingest.OpenReadOnlyDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query: open db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if allVersions {
		records, err := ingest.QueryVersions(db, a.Key())
		if err != nil {
			fmt.Fprintf(os.Stderr, "query: %v\n", err)
			os.Exit(1)
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
		return
	}
	m, err := ingest.Query(db, a.Key())
	if err != nil {
		fmt.Fprintf(os.Stderr, "query: %v\n", err)
		os.Exit(1)
	}
	if m == nil {
		fmt.Printf("package %s not found\n", a.Key())
		return
	}
	_ = metadata.PackageMetadata{}
	fmt.Printf("package: %s/%s-%s\n", m.Category, m.Package, m.Version)
	fmt.Printf("  description: %s\n", m.DESCRIPTION)
	fmt.Printf("  homepage:    %s\n", m.HOMEPAGE)
	fmt.Printf("  license:     %s\n", m.LICENSE)
}
