package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/search"
)

func runSearch(args []string, dbPath string) {
	query := ""
	if len(args) > 0 {
		query = args[0]
	}

	db, err := ingest.OpenDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "search: open db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	printFields := []string{}
	if *searchPrint != "" {
		printFields = strings.Fields(*searchPrint)
	}

	cfg := search.SearchConfig{
		Query:       query,
		Regex:       *searchRegex,
		Category:    *searchCategory,
		Name:        *searchName,
		Description: *searchDesc,
		Slot:        *searchSlot,
		Use:         *searchUse,
		Keywords:    *searchKeywords,
		License:     *searchLicense,
		Installed:   *searchInstalled,
		Stable:      *searchStable,
		Testing:     *searchTesting,
		Exact:       *searchExact,
		Sort:        parseSortField(*searchSort),
		Compact:     *searchCompact,

		Versions:  *searchVersions,
		Format:    *searchFormat,
		Print:     printFields,
		JSON:      *searchJSON,
		Brief:     *searchBrief,
		OnlyNames: *searchOnlyNames,
		CountOnly: *searchCountOnly,

		And: *searchAnd,
		Not: *searchNot,

		World:  *searchWorld,
		System: *searchSystem,

		DependsOn:  *searchDependsOn,
		RequiredBy: *searchRequiredBy,

		HasUse:     *searchHasUse,
		HasVersion: *searchHasVersion,

		Care:       *searchCare,
		Overflow:   *searchOverflow,
		Masked:     *searchMasked,
		Duplicates: *searchDuplicates,

		Dump:     *searchDump,
		RepoPath: *repoPath,
	}

	results, err := search.Search(db, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "search: %v\n", err)
		os.Exit(1)
	}

	if cfg.JSON {
		out, jErr := search.JSONOutput(results)
		if jErr != nil {
			fmt.Fprintf(os.Stderr, "search: json: %v\n", jErr)
			os.Exit(1)
		}
		fmt.Print(out)
		return
	}

	if cfg.CountOnly {
		fmt.Printf("%d\n", len(results))
		return
	}

	if cfg.OnlyNames {
		for _, r := range results {
			fmt.Println(r.Package)
		}
		return
	}

	if cfg.Brief {
		for _, r := range results {
			fmt.Println(search.BriefResult(r))
		}
		return
	}

	if cfg.Format != "" {
		for _, r := range results {
			fmt.Println(search.FormatResult(r, cfg.Format))
		}
		return
	}

	if len(cfg.Print) > 0 {
		for _, r := range results {
			fmt.Println(search.PrintResult(r, cfg.Print))
		}
		return
	}

	if cfg.Dump {
		for _, r := range results {
			fmt.Print(search.DumpResult(r))
			fmt.Println()
		}
		return
	}

	if len(results) == 0 {
		fmt.Println("No packages found.")
		return
	}

	for _, r := range results {
		cp := r.Category + "/" + r.Package
		ver := ""
		if r.Version != "" {
			ver = r.Version
		}
		desc := ""
		if r.Description != "" {
			desc = fmt.Sprintf("%q", r.Description)
		}
		kw := r.Keywords

		if r.Installed {
			fmt.Printf("%s %s [%s] %s %s\n",
				color.Green("[I]"), color.Bold(cp),
				color.Green(ver), desc, kw)
		} else {
			fmt.Printf("  %s [%s] %s\n",
				color.Bold(cp), ver, desc)
		}
	}
}
