package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/nameindex"
	"github.com/airencracken/arise/internal/search"
)

func runSearch(args []string, dbPath string) int {
	query := ""
	if len(args) > 0 {
		query = args[0]
	}
	if canUseNameIndex(query) {
		if names, err := nameindex.Search(nameindex.Path(dbPath), query, *searchExact); err == nil {
			for _, cp := range names {
				parts := strings.SplitN(cp, "/", 2)
				fmt.Println(parts[1])
			}
			return searchExitCode(len(names))
		}
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
	defaultOutput := !cfg.JSON && !cfg.Brief && !cfg.OnlyNames && !cfg.CountOnly && cfg.Format == "" && len(cfg.Print) == 0 && !cfg.Dump
	if defaultOutput {
		cfg.Versions = true
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
		return searchExitCode(len(results))
	}

	if cfg.CountOnly {
		fmt.Printf("%d\n", len(results))
		return searchExitCode(len(results))
	}

	if cfg.OnlyNames {
		for _, r := range results {
			fmt.Println(r.Package)
		}
		return searchExitCode(len(results))
	}

	if cfg.Brief {
		for _, r := range results {
			fmt.Println(search.BriefResult(r))
		}
		return searchExitCode(len(results))
	}

	if cfg.Format != "" {
		for _, r := range results {
			fmt.Println(search.FormatResult(r, cfg.Format))
		}
		return searchExitCode(len(results))
	}

	if len(cfg.Print) > 0 {
		for _, r := range results {
			fmt.Println(search.PrintResult(r, cfg.Print))
		}
		return searchExitCode(len(results))
	}

	if cfg.Dump {
		for _, r := range results {
			fmt.Print(search.DumpResult(r))
			fmt.Println()
		}
		return searchExitCode(len(results))
	}

	if len(results) == 0 {
		fmt.Println("No packages found.")
		return searchExitCode(len(results))
	}

	overlays := make(map[int]search.SearchResult)
	for _, r := range results {
		marker := color.Green("*")
		if r.Installed {
			marker = color.Green("[I]")
			if searchUpgradeAvailable(r.InstalledVer, r.BestVersion) {
				marker = "[" + color.ReverseBoldCyan("U") + "]"
			}
		}
		fmt.Printf("%s %s/%s", marker, r.Category, color.Bold(r.Package))
		if r.OverlayIndex > 0 {
			fmt.Printf(" %s", color.Cyan(fmt.Sprintf("[%d]", r.OverlayIndex)))
			overlays[r.OverlayIndex] = r
		}
		fmt.Println()
		if len(r.VersionInfo) > 0 {
			fmt.Printf("     %s", color.Green("Available versions:"))
			lastSlot := ""
			for _, v := range r.VersionInfo {
				if v.Slot != "" && v.Slot != "0" && v.Slot != lastSlot {
					fmt.Printf("  %s", color.BoldRed("("+v.Slot+")"))
					lastSlot = v.Slot
				}
				label := v.Version
				switch {
				case v.Masked:
					label = color.Red("!" + label)
				case v.Stable:
					label = color.Green(label)
				case v.Testing:
					label = color.Yellow("~" + label)
				}
				fmt.Printf(" %s%s", label, color.Red(restrictionSuffix(v.Restrict)))
			}
			fmt.Println()
		} else {
			versions := r.AllVersions
			if len(versions) == 0 && r.Version != "" {
				versions = []string{r.Version}
			}
			fmt.Printf("     %s %s\n", color.Green("Available versions:"), strings.Join(versions, " "))
		}
		if len(r.InstalledVersions) > 0 {
			fmt.Printf("     %s ", color.Green("Installed versions:"))
			for i, installed := range r.InstalledVersions {
				if i > 0 {
					fmt.Printf("\n                          ")
				}
				fmt.Print(color.InstalledVersion(installed.Version))
				if installed.Slot != "" && installed.Slot != "0" {
					fmt.Print(color.BoldRed("(" + installed.Slot + ")"))
				}
				fmt.Print(color.Red(restrictionSuffix(installed.Restrict)))
				if installed.BuildTime > 0 {
					fmt.Print(color.BoldMagenta(time.Unix(installed.BuildTime, 0).Format("(03:04:05 PM 01/02/2006)")))
				}
				if len(installed.EnabledUSE)+len(installed.DisabledUSE) > 0 {
					fmt.Print("(")
					printed := 0
					for _, flag := range installed.EnabledUSE {
						if printed > 0 {
							fmt.Print(" ")
						}
						fmt.Print(color.BoldRed(flag))
						printed++
					}
					for _, flag := range installed.DisabledUSE {
						if printed > 0 {
							fmt.Print(" ")
						}
						fmt.Print(color.BoldBlue(flag))
						printed++
					}
					fmt.Print(")")
				}
			}
			fmt.Println()
		}
		if r.IUSE != "" {
			fmt.Printf("       %s%s%s\n", color.BoldYellow("{"), r.IUSE, color.BoldYellow("}"))
		}
		if r.Homepage != "" {
			fmt.Printf("     %s            %s\n", color.Green("Homepage:"), r.Homepage)
		}
		if r.Description != "" {
			fmt.Printf("     %s         %s\n", color.Green("Description:"), r.Description)
		}
		fmt.Println()
	}
	if len(overlays) > 0 {
		keys := make([]int, 0, len(overlays))
		for key := range overlays {
			keys = append(keys, key)
		}
		sort.Ints(keys)
		for _, key := range keys {
			r := overlays[key]
			fmt.Printf("%s %q %s\n", color.Cyan(fmt.Sprintf("[%d]", key)), r.Repository, r.RepoPath)
		}
		fmt.Println()
	}
	fmt.Printf("Found %d match", len(results))
	if len(results) != 1 {
		fmt.Print("es")
	}
	fmt.Println()
	return searchExitCode(len(results))
}

func searchExitCode(matches int) int {
	if matches == 0 {
		return 1
	}
	return 0
}

func canUseNameIndex(query string) bool {
	return *searchOnlyNames && !*searchRegex && !*searchDesc &&
		!*searchInstalled && *searchCategory == "" && *searchName == "" &&
		*searchSlot == "" && *searchUse == "" && *searchKeywords == "" &&
		*searchLicense == "" && !*searchStable && !*searchTesting &&
		*searchSort == "" && !*searchVersions && *searchFormat == "" &&
		*searchPrint == "" && !*searchJSON && !*searchBrief && !*searchCountOnly &&
		!*searchAnd && *searchNot == "" && !*searchWorld && !*searchSystem &&
		*searchDependsOn == "" && *searchRequiredBy == "" && *searchHasUse == "" &&
		*searchHasVersion == "" && !*searchCare && !*searchOverflow &&
		!*searchMasked && !*searchDuplicates && !*searchDump
}

func restrictionSuffix(restrict string) string {
	tags := []struct{ word, tag string }{{"fetch", "f"}, {"mirror", "m"}, {"primaryuri", "p"}, {"binchecks", "b"}, {"strip", "s"}, {"test", "t"}, {"userpriv", "u"}, {"installsources", "i"}, {"bindist", "d"}, {"parallel", "P"}}
	var suffix strings.Builder
	for _, item := range tags {
		if strings.Contains(restrict, item.word) {
			suffix.WriteString(item.tag)
		}
	}
	if suffix.Len() == 0 {
		return ""
	}
	return "^" + suffix.String()
}

func searchUpgradeAvailable(installed, available string) bool {
	if installed == "" || available == "" {
		return false
	}
	iv, _ := atom.ParseVersion(installed)
	av, _ := atom.ParseVersion(available)
	return iv != nil && av != nil && av.Compare(iv) > 0
}
