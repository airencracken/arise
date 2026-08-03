package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/nameindex"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/search"
	"github.com/airencracken/gentooling"
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

	db, err := ingest.OpenReadOnlyDB(dbPath)
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
		Maintainer:  *searchMaintainer,
		Orphaned:    *searchOrphaned,
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
		VDBPath:  *vdbDir,
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

	var useExpand, useExpandHidden []string
	arch := ""
	if effective, configErr := searchEffectiveConfig(); configErr == nil {
		useExpand = append(useExpand, effective.UseExpand...)
		useExpand = append(useExpand, effective.UseExpandImplicit...)
		useExpandHidden = append(useExpandHidden, effective.UseExpandHidden...)
		arch = effective.Variables["ARCH"]
	}

	overlays := make(map[int]search.SearchResult)
	for _, r := range results {
		marker := color.Green("*")
		if r.Installed {
			marker = "[" + color.InstalledMarker("I") + "]"
			if searchUpgradeAvailable(r) {
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
			fmt.Printf("     %s ", color.Green("Available versions:"))
			lastSlot := ""
			for _, v := range r.VersionInfo {
				if v.Slot != "" && v.Slot != "0" && v.Slot != lastSlot {
					fmt.Printf("  %s", color.BoldRed("("+v.Slot+")"))
					lastSlot = v.Slot
				}
				label := availableVersionDisplay(v, arch, r.InstalledVersions)
				fmt.Printf(" %s%s%s", label, color.Cyan(propertiesSuffix(v.Properties)), color.Red(restrictionSuffix(v.Restrict)))
			}
			if r.IUSE != "" {
				fmt.Printf(" %s", declaredUseDisplay(r.IUSE, useExpand, useExpandHidden))
			}
			fmt.Println()
		} else {
			versions := r.AllVersions
			if len(versions) == 0 && r.Version != "" {
				versions = []string{r.Version}
			}
			fmt.Printf("     %s  %s", color.Green("Available versions:"), strings.Join(versions, " "))
			if r.IUSE != "" {
				fmt.Printf(" %s", declaredUseDisplay(r.IUSE, useExpand, useExpandHidden))
			}
			fmt.Println()
		}
		if len(r.InstalledVersions) > 0 {
			fmt.Printf("     %s  ", color.Green("Installed versions:"))
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
					fmt.Print(color.BoldMagenta(time.Unix(installed.BuildTime, 0).Format("(15:04:05 01/02/06)")))
				}
				if len(installed.EnabledUSE)+len(installed.DisabledUSE) > 0 {
					fmt.Printf(" %s", installedUseDisplay(installed, useExpand, useExpandHidden))
				}
			}
			fmt.Println()
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

func searchEffectiveConfig() (gentooling.EffectiveConfig, error) {
	paths := gentooling.DefaultSystemPaths("/")
	paths.ConfigRoot = *portageConfigRoot
	paths.ActiveProfile = filepath.Join(*portageConfigRoot, "make.profile")
	if _, err := os.Lstat(paths.ActiveProfile); os.IsNotExist(err) {
		paths.ActiveProfile = ""
	} else if err != nil {
		return gentooling.EffectiveConfig{}, err
	}
	repositories, err := portage.RepositoryPolicyOrder(filepath.Join(*portageConfigRoot, "repos.conf"))
	if err != nil {
		return gentooling.EffectiveConfig{}, err
	}
	for _, repository := range repositories {
		if repository.Name != "" && repository.Location != "" {
			paths.Repositories = append(paths.Repositories, gentooling.RepositoryPath{
				Name: repository.Name, Path: repository.Location,
			})
		}
	}
	return gentooling.ReadEffectiveConfig(context.Background(), paths, gentooling.ConfigOptions{Environment: os.Environ()})
}

func installedUseDisplay(installed search.InstalledVersion, expandGroups, hiddenGroups []string) string {
	hidden := make(map[string]bool, len(hiddenGroups))
	for _, group := range hiddenGroups {
		hidden[strings.ToUpper(strings.TrimSpace(group))] = true
	}
	type useGroup struct {
		name   string
		prefix string
		flags  []string
	}
	groups := make([]useGroup, 0, len(expandGroups))
	seen := make(map[string]bool, len(expandGroups))
	for _, raw := range expandGroups {
		name := strings.ToUpper(strings.TrimSpace(raw))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		groups = append(groups, useGroup{name: name, prefix: strings.ToLower(name) + "_"})
	}
	var ordinary []string
	add := func(flag string, enabled bool) {
		name := strings.TrimPrefix(flag, "-")
		rendered := name
		if !enabled {
			rendered = "-" + name
		}
		for index := range groups {
			if strings.HasPrefix(name, groups[index].prefix) {
				if !hidden[groups[index].name] {
					value := strings.TrimPrefix(name, groups[index].prefix)
					if !enabled {
						value = "-" + value
					}
					groups[index].flags = append(groups[index].flags, value)
				}
				return
			}
		}
		ordinary = append(ordinary, rendered)
	}
	for _, flag := range installed.EnabledUSE {
		add(flag, true)
	}
	for _, flag := range installed.DisabledUSE {
		add(flag, false)
	}

	parts := make([]string, 0, len(groups)+1)
	if len(ordinary) > 0 {
		parts = append(parts, colorInstalledUseFlags(ordinary))
	}
	for _, group := range groups {
		if len(group.flags) > 0 {
			parts = append(parts, group.name+`="`+colorInstalledUseFlags(group.flags)+`"`)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, " ") + ")"
}

func declaredUseDisplay(iuse string, expandGroups, hiddenGroups []string) string {
	hidden := make(map[string]bool, len(hiddenGroups))
	for _, group := range hiddenGroups {
		hidden[strings.ToUpper(strings.TrimSpace(group))] = true
	}
	type useGroup struct {
		name   string
		prefix string
		flags  []string
	}
	groups := make([]useGroup, 0, len(expandGroups))
	seen := make(map[string]bool, len(expandGroups))
	for _, raw := range expandGroups {
		name := strings.ToUpper(strings.TrimSpace(raw))
		if name == "" || seen[name] || hidden[name] {
			continue
		}
		seen[name] = true
		groups = append(groups, useGroup{name: name, prefix: strings.ToLower(name) + "_"})
	}
	ordinary := make([]string, 0, len(strings.Fields(iuse)))
	for _, flag := range strings.Fields(iuse) {
		marker := ""
		name := flag
		if strings.HasPrefix(name, "+") || strings.HasPrefix(name, "-") {
			marker, name = name[:1], name[1:]
		}
		grouped := false
		for index := range groups {
			if strings.HasPrefix(name, groups[index].prefix) {
				groups[index].flags = append(groups[index].flags, marker+strings.TrimPrefix(name, groups[index].prefix))
				grouped = true
				break
			}
		}
		if !grouped {
			ordinary = append(ordinary, flag)
		}
	}
	parts := append([]string(nil), ordinary...)
	for _, group := range groups {
		if len(group.flags) > 0 {
			parts = append(parts, group.name+`="`+strings.Join(group.flags, " ")+`"`)
		}
	}
	return color.BoldYellow("{") + strings.Join(parts, " ") + color.BoldYellow("}")
}

func colorInstalledUseFlags(flags []string) string {
	rendered := make([]string, 0, len(flags))
	for _, flag := range flags {
		if strings.HasPrefix(flag, "-") {
			rendered = append(rendered, color.BoldBlue(flag))
		} else {
			rendered = append(rendered, color.BoldRed(flag))
		}
	}
	return strings.Join(rendered, " ")
}

func searchExitCode(matches int) int {
	if matches == 0 {
		return 1
	}
	return 0
}

func canUseNameIndex(query string) bool {
	return *searchOnlyNames && !strings.ContainsAny(query, "*?[") && !*searchRegex && !*searchDesc &&
		!*searchInstalled && *searchCategory == "" && *searchName == "" &&
		*searchSlot == "" && *searchUse == "" && *searchKeywords == "" &&
		*searchLicense == "" && *searchMaintainer == "" && !*searchOrphaned &&
		!*searchStable && !*searchTesting &&
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

func propertiesSuffix(properties string) string {
	tags := []struct{ word, tag string }{{"interactive", "i"}, {"live", "l"}, {"set", "s"}}
	var suffix strings.Builder
	for _, item := range tags {
		if strings.Contains(properties, item.word) {
			suffix.WriteString(item.tag)
		}
	}
	if suffix.Len() == 0 {
		return ""
	}
	return "*" + suffix.String()
}

func availableVersionDisplay(version search.VersionInfo, arch string, installed []search.InstalledVersion) string {
	decorateInstalled := func(rendered string) string {
		for _, current := range installed {
			if current.Version == version.Version && normalizedSearchSlot(current.Slot) == normalizedSearchSlot(version.Slot) {
				return color.Reverse(rendered)
			}
		}
		return rendered
	}
	if arch == "" {
		switch {
		case version.Masked:
			return color.Red("**" + version.Version)
		case version.Stable:
			return decorateInstalled(color.Green(version.Version))
		case version.Testing:
			return decorateInstalled(color.Yellow("~" + version.Version))
		default:
			return version.Version
		}
	}
	stable, testing, anyTesting := false, false, false
	keywords := strings.Fields(version.Keywords)
	for _, keyword := range keywords {
		switch keyword {
		case arch:
			stable = true
		case "~" + arch:
			testing = true
		}
		if strings.HasPrefix(keyword, "~") {
			anyTesting = true
		}
	}
	switch {
	case stable:
		return decorateInstalled(color.Green(version.Version))
	case testing:
		return decorateInstalled(color.Yellow("~" + version.Version))
	case len(keywords) == 0:
		return color.Red("**" + version.Version)
	case anyTesting:
		return color.Red("~*" + version.Version)
	default:
		return color.Red("*" + version.Version)
	}
}

func normalizedSearchSlot(slot string) string {
	if slot == "" {
		return "0"
	}
	return slot
}

func searchUpgradeAvailable(result search.SearchResult) bool {
	installedBySlot := make(map[string]string, len(result.InstalledVersions))
	for _, installed := range result.InstalledVersions {
		slot := installed.Slot
		if slot == "" {
			slot = "0"
		}
		current := installedBySlot[slot]
		if current == "" || compareSearchVersions(installed.Version, current) > 0 {
			installedBySlot[slot] = installed.Version
		}
	}
	for _, available := range result.VersionInfo {
		if available.Masked || !available.Stable {
			continue
		}
		slot := available.Slot
		if slot == "" {
			slot = "0"
		}
		installed := installedBySlot[slot]
		if installed == "" && len(installedBySlot) == 0 {
			installed = result.InstalledVer
		}
		if installed != "" && compareSearchVersions(available.Version, installed) > 0 {
			return true
		}
	}
	return false
}

func compareSearchVersions(left, right string) int {
	leftVersion, _ := atom.ParseVersion(left)
	rightVersion, _ := atom.ParseVersion(right)
	if leftVersion == nil || rightVersion == nil {
		return 0
	}
	return leftVersion.Compare(rightVersion)
}
