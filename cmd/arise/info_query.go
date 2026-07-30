package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/configprotect"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/preserved"
)

func runInfoQuery(args []string) int {
	if len(args) == 0 {
		runInfo()
		return 0
	}
	switch args[0] {
	case "--value":
		return runInfoValues(args[1:])
	case "--repositories":
		return runInfoRepositories(args[1:])
	case "--repo-path":
		return runInfoRepoPaths(args[1:])
	case "--repository-config":
		return runInfoRepositoryConfig(args[1:])
	case "--masters":
		return runInfoMasters(args[1:])
	case "--eclasses":
		return runInfoEclasses(args[1:])
	case "--eclass-path":
		return runInfoAssetPaths("eclass", args[1:])
	case "--license-path":
		return runInfoAssetPaths("license", args[1:])
	case "--preserved-libs":
		return runInfoPreservedLibs(args[1:])
	case "--is-protected":
		return runInfoIsProtected(args[1:])
	case "--filter-protected":
		return runInfoFilterProtected(args[1:])
	case "--colors":
		return runInfoColors(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "info: unknown inspection option %q\n", args[0])
		return 2
	}
}

func runInfoColors(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "info --colors: does not accept arguments")
		return 2
	}
	palette := color.Palette()
	names := make([]string, 0, len(palette))
	for name := range palette {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Printf("%s=%q\n", name, palette[name])
	}
	return 0
}

func configProtectionPolicy() (string, []string, []string, int) {
	cfg, code := loadInfoConfig()
	if code != 0 {
		return "", nil, nil, code
	}
	root := commandEnv("ROOT", "/")
	return root, strings.Fields(cfg.MakeConf["CONFIG_PROTECT"]), strings.Fields(cfg.MakeConf["CONFIG_PROTECT_MASK"]), 0
}

func runInfoIsProtected(paths []string) int {
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "info --is-protected: require at least one absolute path")
		return 2
	}
	root, protect, mask, code := configProtectionPolicy()
	if code != 0 {
		return code
	}
	found := false
	for _, path := range paths {
		protected, err := configprotect.Protected(root, path, protect, mask)
		if err != nil {
			fmt.Fprintf(os.Stderr, "info --is-protected: %v\n", err)
			return 2
		}
		if protected {
			fmt.Println(path)
			found = true
		}
	}
	if !found {
		return 1
	}
	return 0
}

func runInfoFilterProtected(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "info --filter-protected: reads newline-delimited paths from stdin")
		return 2
	}
	root, protect, mask, code := configProtectionPolicy()
	if code != 0 {
		return code
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		path := scanner.Text()
		protected, err := configprotect.Protected(root, path, protect, mask)
		if err != nil {
			fmt.Fprintf(os.Stderr, "info --filter-protected: %v\n", err)
			return 2
		}
		if protected {
			fmt.Println(path)
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "info --filter-protected: %v\n", err)
		return 1
	}
	return 0
}

func loadInfoConfig() (*portage.Config, int) {
	cfg, err := portage.LoadEffectiveConfig(*portageConfigRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "info: loading portage config: %v\n", err)
		return nil, 1
	}
	return cfg, 0
}

func loadInfoRepositories() ([]portage.RepoEntry, int) {
	entries, err := portage.RepositoryPolicyOrder(filepath.Join(*portageConfigRoot, "repos.conf"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "info: loading repositories: %v\n", err)
		return nil, 1
	}
	return entries, 0
}

func runInfoValues(names []string) int {
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "info --value: require at least one variable")
		return 2
	}
	cfg, code := loadInfoConfig()
	if code != 0 {
		return code
	}
	values := make(map[string]string, len(cfg.MakeConf)+8)
	for name, value := range cfg.MakeConf {
		values[name] = value
	}
	values["DISTDIR"] = *distfilesDir
	values["PKGDIR"] = *binpkgDir
	values["PORTDIR"] = *repoPath
	values["PORTAGE_CONFIGROOT"] = filepath.Dir(filepath.Dir(*portageConfigRoot))
	values["ROOT"] = commandEnv("ROOT", "/")
	values["EROOT"] = commandEnv("ROOT", "/")
	values["VDB_PATH"] = *vdbDir
	found := false
	for _, pattern := range names {
		if strings.HasSuffix(pattern, "*") {
			prefix := strings.TrimSuffix(pattern, "*")
			var matches []string
			for name := range values {
				if strings.HasPrefix(name, prefix) {
					matches = append(matches, name)
				}
			}
			sort.Strings(matches)
			for _, name := range matches {
				fmt.Printf("%s=%s\n", name, values[name])
				found = true
			}
			continue
		}
		if value, ok := values[pattern]; ok {
			if len(names) == 1 {
				fmt.Println(value)
			} else {
				fmt.Printf("%s=%s\n", pattern, value)
			}
			found = true
		}
	}
	if !found {
		return 1
	}
	return 0
}

func runInfoRepositories(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "info --repositories: does not accept arguments")
		return 2
	}
	entries, code := loadInfoRepositories()
	if code != 0 {
		return code
	}
	for _, entry := range entries {
		fmt.Println(entry.Name)
	}
	return 0
}

func repositoryMap() (map[string]portage.RepoEntry, int) {
	entries, code := loadInfoRepositories()
	if code != 0 {
		return nil, code
	}
	result := make(map[string]portage.RepoEntry, len(entries))
	for _, entry := range entries {
		result[entry.Name] = entry
	}
	return result, 0
}

func runInfoRepoPaths(names []string) int {
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "info --repo-path: require at least one repository")
		return 2
	}
	repositories, code := repositoryMap()
	if code != 0 {
		return code
	}
	for _, name := range names {
		entry, ok := repositories[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "info --repo-path: unknown repository %q\n", name)
			return 1
		}
		fmt.Println(entry.Location)
	}
	return 0
}

func runInfoRepositoryConfig(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "info --repository-config: does not accept arguments")
		return 2
	}
	entries, code := loadInfoRepositories()
	if code != 0 {
		return code
	}
	for _, entry := range entries {
		fmt.Printf("[%s]\nlocation = %s\n", entry.Name, entry.Location)
		if entry.SyncType != "" {
			fmt.Printf("sync-type = %s\n", entry.SyncType)
		}
		if entry.SyncURI != "" {
			fmt.Printf("sync-uri = %s\n", entry.SyncURI)
		}
		if len(entry.Masters) > 0 {
			fmt.Printf("masters = %s\n", strings.Join(entry.Masters, " "))
		}
	}
	return 0
}

func runInfoMasters(names []string) int {
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "info --masters: require at least one repository")
		return 2
	}
	repositories, code := repositoryMap()
	if code != 0 {
		return code
	}
	for _, name := range names {
		entry, ok := repositories[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "info --masters: unknown repository %q\n", name)
			return 1
		}
		fmt.Println(strings.Join(entry.Masters, " "))
	}
	return 0
}

func runInfoEclasses(names []string) int {
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "info --eclasses: require at least one repository")
		return 2
	}
	entries, code := loadInfoRepositories()
	if code != 0 {
		return code
	}
	seen := make(map[string]bool)
	for _, name := range names {
		directories, err := portage.EclassLookupDirectories(entries, name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "info --eclasses: %v\n", err)
			return 1
		}
		for _, directory := range directories {
			files, err := os.ReadDir(directory)
			if err != nil {
				continue
			}
			for _, file := range files {
				if !file.IsDir() && strings.HasSuffix(file.Name(), ".eclass") {
					seen[strings.TrimSuffix(file.Name(), ".eclass")] = true
				}
			}
		}
	}
	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	sort.Strings(result)
	fmt.Println(strings.Join(result, " "))
	return 0
}

func runInfoAssetPaths(kind string, args []string) int {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "info --%s-path: require a repository and at least one %s\n", kind, kind)
		return 2
	}
	repositories, code := repositoryMap()
	if code != 0 {
		return code
	}
	entry, ok := repositories[args[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "info --%s-path: unknown repository %q\n", kind, args[0])
		return 1
	}
	for _, name := range args[1:] {
		var candidates []string
		if kind == "eclass" {
			directories, err := portage.EclassLookupDirectories(mapRepositoryValues(repositories), entry.Name)
			if err != nil {
				fmt.Fprintf(os.Stderr, "info --eclass-path: %v\n", err)
				return 1
			}
			for _, directory := range directories {
				candidates = append(candidates, filepath.Join(directory, name+".eclass"))
			}
		} else {
			candidates = []string{filepath.Join(entry.Location, "licenses", name)}
		}
		found := false
		for _, candidate := range candidates {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				fmt.Println(candidate)
				found = true
				break
			}
		}
		if !found {
			return 1
		}
	}
	return 0
}

func mapRepositoryValues(repositories map[string]portage.RepoEntry) []portage.RepoEntry {
	result := make([]portage.RepoEntry, 0, len(repositories))
	for _, entry := range repositories {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func runInfoPreservedLibs(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "info --preserved-libs: does not accept arguments")
		return 2
	}
	libraries, err := preserved.ScanPreservedLibs(commandEnv("ROOT", "/"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "info --preserved-libs: %v\n", err)
		return 1
	}
	for _, library := range libraries {
		fmt.Printf("%s: %s\n", library.OwningPkg, library.Path)
	}
	if len(libraries) == 0 {
		return 1
	}
	return 0
}
