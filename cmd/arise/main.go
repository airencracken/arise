package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/audit"
	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/ebuild"
	"github.com/airencracken/arise/internal/equery"
	"github.com/airencracken/arise/internal/env"
	"github.com/airencracken/arise/internal/features"
	"github.com/airencracken/arise/internal/graph"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/news"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/preserved"
	"github.com/airencracken/arise/internal/rebuild"
	"github.com/airencracken/arise/internal/resolve"
	"github.com/airencracken/arise/internal/search"
	"github.com/airencracken/arise/internal/sync"
	"github.com/airencracken/arise/internal/walker"
	"github.com/airencracken/arise/internal/world"
)

var (
	dbPath       = flag.String("db", "/var/lib/arise/data", "database path")
	repoPath     = flag.String("repo", "/var/db/repos/gentoo", "repository path")
	repoURL      = flag.String("repo-url", "", "remote repository URL for sync")

	// Package resolution flags
	oneshot      = flag.Bool("oneshot", false, "-1, install without adding to world set")
	nodeps       = flag.Bool("nodeps", false, "-O, skip dependency resolution")
	onlydeps     = flag.Bool("onlydeps", false, "-o, only install dependencies")
	emptytree    = flag.Bool("emptytree", false, "-e, rebuild entire tree as if empty")
	reinstall    = flag.Bool("reinstall", false, "force reinstall of already-installed packages")
	changedUse   = flag.Bool("changed-use", false, "reinstall when USE flags changed")
	changedDeps  = flag.Bool("changed-deps", false, "reinstall when DEPENDs changed")
	newuse       = flag.Bool("newuse", false, "-N, rebuild when USE flags changed")
	keepGoing    = flag.Bool("keep-going", false, "continue on errors")
	deep         = flag.Bool("deep", false, "-D, consider full dependency tree")
	completeGraph = flag.Bool("complete-graph", false, "rebuild reverse deps when packages change")
	backtrackVal = flag.Int("backtrack", 10, "--backtrack=INT, max backtrack levels")
	jobsVal      = flag.Int("jobs", 0, "-j, parallel jobs")
	loadAverage  = flag.Float64("load-average", 0, "--load-average=LOAD")
	pretend      = flag.Bool("pretend", false, "-p, dry run")
	ask          = flag.Bool("ask", false, "-a, prompt before proceeding")
	quiet        = flag.Bool("quiet", false, "-q, minimal output")
	verbose      = flag.Bool("verbose", false, "-v, verbose output")
	tree         = flag.Bool("tree", false, "-t, display dependency tree")
	resume       = flag.Bool("resume", false, "--resume, resume last operation")
	skipFirst    = flag.Bool("skipfirst", false, "--skipfirst, skip first package in resume")
	unorderedDisp = flag.Bool("unordered-display", false, "--unordered-display, don't sort results")
	autoUnmaskW  = flag.Bool("autounmask-write", false, "--autounmask-write, write package.unmask entries")
	withBdeps    = flag.String("with-bdeps", "n", "--with-bdeps=y|n|auto")
	buildPkg     = flag.Bool("buildpkg", false, "-b, build binary packages")
	buildPkgOnly = flag.Bool("buildpkgonly", false, "-B, only build binary packages")
	usePkg       = flag.Bool("usepkg", false, "-k, use binary packages")
	usePkgOnly   = flag.Bool("usepkgonly", false, "-K, only use binary packages")
	fetchOnly    = flag.Bool("fetchonly", false, "-f, only fetch sources")
	noreplace    = flag.Bool("noreplace", false, "--noreplace, skip packages with exact same version installed")
	colors       = flag.String("color", "y", "--color=y|n, enable or disable color output")
	deselectArg  = flag.String("deselect", "", "--deselect, remove atom from world set")
	binpkgRespectUse = flag.Bool("binpkg-respect-use", false, "--binpkg-respect-use, respect USE flags when searching binary packages")
	ignoreBuiltSlotOps = flag.String("ignore-built-slot-operator-deps", "n", "--ignore-built-slot-operator-deps=y|n")
	getbinpkg    = flag.Bool("getbinpkg", false, "-g, fetch binary packages from remote binhost")
	getbinpkgOnly = flag.Bool("getbinpkgonly", false, "-G, only use binary packages from remote binhost")

	searchNameOnly   = flag.Bool("name-only", false, "--name-only, search only package/category names")
	searchDesc       = flag.Bool("desc", false, "--desc, search descriptions")
	searchInstalled  = flag.Bool("search-installed", false, "--installed, only show installed packages")
	searchExact      = flag.Bool("exact", false, "--exact, exact match instead of substring")
	searchRegex      = flag.Bool("regex", false, "--regex, use regex patterns")
	searchCategory   = flag.String("search-category", "", "--category, filter by category")
	searchName       = flag.String("search-name", "", "--name, filter by package name")
	searchSlot       = flag.String("search-slot", "", "--slot, filter by SLOT")
	searchUse        = flag.String("search-use", "", "--use, USE flag filter (+flag, -flag)")
	searchKeywords   = flag.String("search-keywords", "", "--keywords, filter by KEYWORDS")
	searchLicense    = flag.String("search-license", "", "--license, filter by LICENSE")
	searchStable     = flag.Bool("search-stable", false, "--stable, only stable-keyworded packages")
	searchTesting    = flag.Bool("search-testing", false, "--testing, only testing-keyworded packages")
	searchSort       = flag.String("search-sort", "", "--sort, sort by: category, name, version, slot")
	searchCompact    = flag.Bool("compact", false, "--compact, compact output")

	searchVersions   = flag.Bool("search-versions", false, "--versions, show all versions of matching packages")
	searchFormat     = flag.String("search-format", "", "--format, custom format string")
	searchPrint      = flag.String("search-print", "", "--print, specific fields to print (space-separated)")
	searchJSON       = flag.Bool("search-json", false, "--json, JSON output")
	searchBrief      = flag.Bool("search-brief", false, "--brief, one-line minimal output")
	searchOnlyNames  = flag.Bool("search-only-names", false, "--only-names, just package names")
	searchCountOnly  = flag.Bool("search-count-only", false, "--count-only, just print count")
	searchAnd        = flag.Bool("search-and", false, "--and, combine multiple queries with AND")
	searchNot        = flag.String("search-not", "", "--not, exclude packages matching this")
	searchWorld      = flag.Bool("search-world", false, "--world, only packages in @world")
	searchSystem     = flag.Bool("search-system", false, "--system, only packages in @system")
	searchDependsOn  = flag.String("search-depends-on", "", "--depends-on, packages that depend on this")
	searchRequiredBy = flag.String("search-required-by", "", "--required-by, packages that this depends on")
	searchHasUse     = flag.String("search-has-use", "", "--has-use, packages with this USE flag")
	searchHasVersion = flag.String("search-has-version", "", "--has-version, packages with version matching glob")
	searchCare       = flag.Bool("search-care", false, "--care, show packages needing attention")
	searchOverflow   = flag.Bool("search-overflow", false, "--overflow, only packages with masked keywords")
	searchMasked     = flag.Bool("search-masked", false, "--masked, only masked packages")
	searchDuplicates = flag.Bool("search-duplicates", false, "--duplicates, show multiple versions of same package")
	searchDump       = flag.Bool("search-dump", false, "--dump, dump as eix-compatible format")
)

func main() {
	flag.Parse()

	if *colors == "n" || *colors == "no" || *colors == "false" || *colors == "0" {
		color.UseColor = false
	}

	args := flag.Args()
	if len(args) == 0 && *deselectArg == "" {
		fmt.Fprintf(os.Stderr, "Usage: arise [flags] <command> [args...]\n")
		fmt.Fprintf(os.Stderr, "Commands: sync, index, install, update, uninstall, query, search, info, audit, dispatch-conf, quickpkg, depclean, prune, env-update, ldconfig, config, news, deselect, preserved-rebuild, revdep-rebuild, equery, bench\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	if *deselectArg != "" {
		runDeselect(*deselectArg)
		return
	}

	cmd := args[0]
	cmdArgs := args[1:]

	switch cmd {
	case "sync":
		if *repoURL == "" {
			fmt.Fprintf(os.Stderr, "sync: -repo-url is required\n")
			os.Exit(1)
		}
		cfg := sync.SyncConfig{
			RepoURL:   *repoURL,
			TargetDir: *repoPath,
		}
		if err := sync.Sync(context.Background(), cfg); err != nil {
			fmt.Fprintf(os.Stderr, "sync: %v\n", err)
			os.Exit(1)
		}
	case "index":
		results, errs := walker.WalkCache(*repoPath)
		db, err := ingest.OpenDB(*dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "index: open db: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()

		var parseErrors int
		go func() {
			for e := range errs {
				parseErrors++
				if *verbose {
					fmt.Fprintf(os.Stderr, "index: %v\n", e)
				}
			}
		}()

		count, err := ingest.Ingest(db, results)
		if err != nil {
			fmt.Fprintf(os.Stderr, "index: ingest: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("index: ingested %d packages", count)
		if parseErrors > 0 {
			fmt.Printf(" (%d non-fatal parse errors, use -v to see)", parseErrors)
		}
		fmt.Println()
	case "query":
		if len(cmdArgs) == 0 {
			fmt.Fprintf(os.Stderr, "query: missing package atom argument\n")
			os.Exit(1)
		}
		a, err := atom.Parse(cmdArgs[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "query: parsing atom %q: %v\n", cmdArgs[0], err)
			os.Exit(1)
		}

		db, err := ingest.OpenDB(*dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "query: open db: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()

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
	case "install":
		runInstall(cmdArgs, *dbPath, *repoPath)
	case "update":
		if len(cmdArgs) == 0 {
			cmdArgs = []string{"@world"}
		}
		runResolveAndRebuild(cmdArgs, *dbPath, *repoPath, true, true)
	case "uninstall":
		runUninstall(cmdArgs, *dbPath, *repoPath)
	case "audit":
		runAudit(cmdArgs, *repoPath)
	case "dispatch-conf":
		runDispatchConf()
	case "quickpkg":
		runQuickPkg(cmdArgs)
	case "depclean":
		runDepclean(*dbPath, *repoPath)
	case "prune":
		runPrune(*dbPath, *repoPath)
	case "search":
		runSearch(cmdArgs, *dbPath)
	case "info":
		runInfo()
	case "preserved-rebuild":
		runPreservedRebuild()
	case "revdep-rebuild":
		runRevdepRebuild()
	case "env-update":
		runEnvUpdate()
	case "ldconfig":
		runLdConfig()
	case "config":
		runConfig(cmdArgs, *repoPath)
	case "news":
		runNews(cmdArgs)
	case "deselect":
		if len(cmdArgs) == 0 {
			fmt.Fprintf(os.Stderr, "deselect: missing atom argument\n")
			os.Exit(1)
		}
		runDeselect(cmdArgs[0])
	case "equery":
		runEquery(cmdArgs, *dbPath, *repoPath, "/var/db/pkg")
	case "bench":
		runBench()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		fmt.Fprintf(os.Stderr, "Usage: arise [flags] <command> [args...]\n")
		os.Exit(1)
	}
}

func runInstall(args []string, dbPath, repoDir string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "install: missing package atom arguments\n")
		os.Exit(1)
	}
	runResolveAndRebuild(args, dbPath, repoDir, false, true)
}

func runResolveAndRebuild(targets []string, dbPath, repoDir string, update bool, deep bool) {
	cfg := resolveFlagsToConfig(update, deep)
	runResolve(targets, dbPath, repoDir, cfg)
}

func buildRebuildConfig(repoDir string, jobs int, phaseStart func(string), phaseEnd func(string, error)) rebuild.RebuildConfig {
	portageCfg, _ := portage.LoadConfig("/etc/portage")
	var featConfig *features.Config
	if portageCfg != nil {
		if rawFeatures, ok := portageCfg.MakeConf["FEATURES"]; ok {
			featConfig = features.ParseFeatures(rawFeatures)
		}
	}

	makeOpts := fmt.Sprintf("-j%d", jobs)
	if jobs <= 0 {
		makeOpts = os.Getenv("MAKEOPTS")
	}

	cfg := rebuild.RebuildConfig{
		RepoDir:      repoDir,
		DistfilesDir: "/var/cache/distfiles",
		RootDir:      "/",
		VdbDir:       "/var/db/pkg",
		WorkDirBase:  "/var/tmp/arise",
		CFLAGS:       os.Getenv("CFLAGS"),
		CXXFLAGS:     os.Getenv("CXXFLAGS"),
		LDFLAGS:      os.Getenv("LDFLAGS"),
		MAKEOPTS:     makeOpts,
		Arch:         os.Getenv("ARCH"),
		Features:     featConfig,
		OnPhaseStart: phaseStart,
		OnPhaseEnd:   phaseEnd,
	}
	if portageCfg != nil {
		if cfg.CFLAGS == "" {
			cfg.CFLAGS = portageCfg.CFLAGS
		}
		if cfg.CXXFLAGS == "" {
			cfg.CXXFLAGS = portageCfg.CXXFLAGS
		}
		if cfg.MAKEOPTS == "" {
			cfg.MAKEOPTS = portageCfg.MAKEOPTS
		}
	}
	return cfg
}

func runRebuild(ctx context.Context, atoms []string, cfg rebuild.RebuildConfig, jobs int, loadAvg float64) error {
	buildCtx := ctx
	if loadAvg > 0 {
		buildCtx = rebuild.WithLoadControl(ctx, loadAvg)
	}
	if jobs > 1 {
		return rebuild.RebuildPackagesParallel(buildCtx, atoms, cfg, jobs)
	}
	return rebuild.RebuildPackages(buildCtx, atoms, cfg)
}

func resolveFlagsToConfig(update, deepParam bool) resolve.ResolveConfig {
	return resolve.ResolveConfig{
		Backtrack:                    *backtrackVal,
		Deep:                         deepParam || *deep,
		CompleteGraph:                *completeGraph,
		NewUse:                       *newuse,
		Update:                       update,
		Oneshot:                      *oneshot,
		NoDeps:                       *nodeps,
		OnlyDeps:                     *onlydeps,
		EmptyTree:                    *emptytree,
		Reinstall:                    *reinstall,
		ChangedUse:                   *changedUse,
		ChangedDeps:                  *changedDeps,
		KeepGoing:                    *keepGoing,
		FetchOnly:                    *fetchOnly,
		BuildPkgOnly:                 *buildPkgOnly,
		BuildPkg:                     *buildPkg,
		UsePkg:                       *usePkg,
		UsePkgOnly:                   *usePkgOnly,
		Pretend:                      *pretend,
		Ask:                          *ask,
		Quiet:                        *quiet,
		Verbose:                      *verbose,
		Tree:                         *tree,
		Resume:                       *resume,
		SkipFirst:                    *skipFirst,
		UnsortedDisplay:              *unorderedDisp,
		AutoUnmaskWrite:              *autoUnmaskW,
		Jobs:                         *jobsVal,
		LoadAverage:                  *loadAverage,
		WithBdeps:                    *withBdeps,
		WithBdepsAuto:                *withBdeps == "auto",
		BinpkgRespectUse:             *binpkgRespectUse,
		IgnoreBuiltSlotOperatorDeps:  *ignoreBuiltSlotOps,
		BinpkgDir:                    "/var/cache/binpkgs",
		GetBinPkg:                    *getbinpkg,
		GetBinPkgOnly:                *getbinpkgOnly,
		NoReplace:                    *noreplace,
	}
}

func runResolve(targets []string, dbPath, repoDir string, cfg resolve.ResolveConfig) {
	if *pretend {
		fmt.Println("(pretend mode: no actions will be performed)")
	}

	// Parse PORTAGE_BINHOST from portage config
	portageCfg, _ := portage.LoadConfig("/etc/portage")
	if portageCfg != nil {
		if cfg.PortageConfig == nil {
			cfg.PortageConfig = portageCfg
		}
		binhostURLs := portage.ParseBinhostConfig(portageCfg)
		if len(binhostURLs) > 0 {
			cfg.BinhostURLs = binhostURLs
		}
	}

	// --getbinpkg / --getbinpkgonly: download from binhost
	if cfg.GetBinPkg || cfg.GetBinPkgOnly {
		if len(cfg.BinhostURLs) == 0 {
			fmt.Fprintf(os.Stderr, "--getbinpkg: no PORTAGE_BINHOST configured in make.conf\n")
			if cfg.GetBinPkgOnly {
				os.Exit(1)
			}
		} else {
			for _, url := range cfg.BinhostURLs {
				downloaded, err := binpkg.DownloadFromBinhost(context.Background(), url, targets, cfg.BinpkgDir)
				if err != nil {
					fmt.Fprintf(os.Stderr, "--getbinpkg: download from %s: %v\n", url, err)
					if cfg.GetBinPkgOnly {
						os.Exit(1)
					}
				} else if !*quiet {
					fmt.Printf("Downloaded %d binary packages from %s\n", len(downloaded), url)
				}
			}
		}
	}

	// --resume: load remaining packages from previous interrupted operation
	if cfg.Resume {
		if cfg.SkipFirst {
			if err := resolve.SkipFirstResume(resolve.ResumePath); err != nil {
				fmt.Fprintf(os.Stderr, "resume: skipfirst: %v\n", err)
			}
		}
		remaining, err := resolve.LoadResume(resolve.ResumePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "resume: load: %v\n", err)
			os.Exit(1)
		}
		if len(remaining) == 0 {
			fmt.Println("Nothing to resume.")
			os.Remove(resolve.ResumePath)
			return
		}
		targets = remaining
		if !*quiet {
			fmt.Printf("Resuming %d packages...\n", len(targets))
		}
	}

	if !*quiet {
		flags := []string{}
		if cfg.Deep {
			flags = append(flags, "-D")
		}
		if cfg.Update {
			flags = append(flags, "-u")
		}
		if cfg.Oneshot {
			flags = append(flags, "-1")
		}
		if cfg.NoDeps {
			flags = append(flags, "-O")
		}
		if cfg.OnlyDeps {
			flags = append(flags, "-o")
		}
		if cfg.EmptyTree {
			flags = append(flags, "-e")
		}
		if cfg.NewUse {
			flags = append(flags, "-N")
		}
		if cfg.Reinstall {
			flags = append(flags, "--reinstall")
		}
		if cfg.Resume {
			flags = append(flags, "--resume")
		}
		if cfg.KeepGoing {
			flags = append(flags, "--keep-going")
		}
		flagStr := ""
		if len(flags) > 0 {
			flagStr = " [" + strings.Join(flags, " ") + "]"
		}
		fmt.Printf("Resolving dependencies for: %s%s\n", strings.Join(targets, ", "), flagStr)
	}

	db, err := ingest.OpenDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve: open db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	g, err := graph.BuildParallel(db, repoDir, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve: build graph: %v\n", err)
		os.Exit(1)
	}

	rg := g.ToResolveGraph()

	result, err := resolve.Resolve(rg, targets, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve: %v\n", err)

		// --autounmask-write: generate unmask/license entries on failure
		if cfg.AutoUnmaskWrite {
			portageConfigRoot := "/etc/portage"
			if result != nil && len(result.Conflicts) > 0 {
				if err := resolve.AutoUnmask(result.Conflicts, portageConfigRoot); err != nil {
					fmt.Fprintf(os.Stderr, "autounmask-write: %v\n", err)
				} else {
					fmt.Println("Auto-unmask entries written to /etc/portage/package.unmask/")
				}
				if err := resolve.AutoAcceptLicense(result.Conflicts, portageConfigRoot); err != nil {
					fmt.Fprintf(os.Stderr, "autounmask-write: %v\n", err)
				} else {
					fmt.Println("Auto-accept-license entries written to /etc/portage/package.license/")
				}
			}
		}
		os.Exit(1)
	}

	if len(result.Conflicts) > 0 && !cfg.Quiet {
		fmt.Println("\nConflicts:")
		for _, c := range result.Conflicts {
			fmt.Printf("  %s\n", c)
		}
	}

	if !cfg.Quiet {
		fmt.Printf("\nProposed actions (%d install, %d uninstall, %d conflicts, backtrack %d):\n",
			len(result.Install), len(result.Uninstall), len(result.Conflicts), result.BacktrackLevel)

		if cfg.Tree {
			fmt.Print(resolve.FormatTree(result.Install, rg))
		} else {
			for _, a := range result.Install {
				actionLabel := actionLabel(a.Action)
				fmt.Printf("  %s %s\n", colorIcon(a.Action, actionLabel), a.Atom)
			}
			for _, a := range result.Uninstall {
				fmt.Printf("  [%s] %s\n", color.Red(a.Action), a.Atom)
			}
		}
	}

	if len(result.Conflicts) > 0 {
		if cfg.Quiet {
			for _, c := range result.Conflicts {
				fmt.Fprintf(os.Stderr, "conflict: %s\n", c)
			}
		}
		if !cfg.KeepGoing {
			// --autounmask-write: generate entries before exiting
			if cfg.AutoUnmaskWrite {
				portageConfigRoot := "/etc/portage"
				resolve.AutoUnmask(result.Conflicts, portageConfigRoot)
				resolve.AutoAcceptLicense(result.Conflicts, portageConfigRoot)
				fmt.Println("Auto-unmask entries written to /etc/portage/package.unmask/ and /etc/portage/package.license/")
				fmt.Println("Re-run with the same command to continue.")
			}
			fmt.Println("\nCannot proceed with unresolved conflicts.")
			os.Exit(1)
		}
		// KeepGoing: with partial results, ask user if they want to proceed
		if cfg.KeepGoing && len(result.Install) > 0 {
			if !cfg.Ask {
				fmt.Println("\nProceeding with partial results (--keep-going).")
			} else if cfg.Ask {
				fmt.Print("\nProceed with partial results? [y/N] ")
				var response string
				fmt.Scanln(&response)
				if !strings.HasPrefix(strings.ToLower(response), "y") {
					fmt.Println("Aborted.")
					return
				}
			}
		}
	}

	if len(result.Install) == 0 && len(result.Uninstall) == 0 {
		if !cfg.Quiet {
			fmt.Println("\nNothing to do.")
		}
		os.Remove(resolve.ResumePath)
		return
	}

	// Save resume state for --resume support
	if err := resolve.SaveResume(resolve.ResumePath, result); err != nil && !*quiet {
		fmt.Fprintf(os.Stderr, "resume: save: %v\n", err)
	}

	if cfg.Pretend || cfg.FetchOnly {
		return
	}
}

func runUninstall(args []string, dbPath, repoDir string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "uninstall: missing package atom arguments\n")
		os.Exit(1)
	}

	for _, arg := range args {
		a, err := atom.Parse(arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "uninstall: parsing atom %q: %v\n", arg, err)
			continue
		}

		vdbPath := filepath.Join("/var/db/pkg", a.Category, a.Package)
		if a.Version != nil && a.Version.Raw != "" {
			vdbPath = vdbPath + "-" + a.Version.Raw
		}

		fmt.Printf("Uninstall: %s\n", vdbPath)
	}
}

func runDispatchConf() {
	etcDir := "/etc"
	entries, err := os.ReadDir(etcDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dispatch-conf: reading %s: %v\n", etcDir, err)
		os.Exit(1)
	}

	var pending []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "._cfg") {
			pending = append(pending, filepath.Join(etcDir, name))
		}
	}

	if len(pending) == 0 {
		fmt.Println("No pending config file updates.")
		return
	}

	fmt.Printf("Found %d pending config file updates:\n", len(pending))
	for _, p := range pending {
		base := strings.TrimPrefix(filepath.Base(p), "._cfg0000_")
		if base == filepath.Base(p) {
			base = strings.TrimPrefix(base, "._cfg")
			base = strings.TrimLeft(base, "0123456789_")
		}
		fmt.Printf("  %s -> %s\n", p, filepath.Join(etcDir, base))
	}
}

func runAudit(args []string, repoDir string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "audit: expected subcommand: python or perl\n")
		fmt.Fprintf(os.Stderr, "Usage: arise audit <python|perl> [--fix] [--pretend] [--jobs N]\n")
		os.Exit(1)
	}

	auditType := args[0]
	auditArgs := args[1:]

	fix := false
	pretend := false
	jobs := 0

	for i := 0; i < len(auditArgs); i++ {
		switch auditArgs[i] {
		case "--fix":
			fix = true
		case "--pretend":
			pretend = true
		case "--jobs":
			if i+1 < len(auditArgs) {
				fmt.Sscanf(auditArgs[i+1], "%d", &jobs)
				i++
			}
		}
	}

	vdbPath := "/var/db/pkg"

	var results []audit.VdbAuditResult
	var err error

	switch auditType {
	case "python":
		results, err = audit.AuditPython(vdbPath)
	case "perl":
		results, err = audit.AuditPerl(vdbPath)
	default:
		fmt.Fprintf(os.Stderr, "audit: unknown subcommand %q; expected python or perl\n", auditType)
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "audit %s: %v\n", auditType, err)
		os.Exit(1)
	}

	if len(results) == 0 {
		fmt.Printf("No outdated %s paths found.\n", auditType)
		return
	}

	var packages []string
	for _, r := range results {
		fmt.Printf("\nPackage: %s\n", r.PackagePath)
		fmt.Printf("  Old versions: %v\n", r.OldVersions)
		for _, c := range r.AffectedContents {
			fmt.Printf("    %s\n", c)
		}
		if fix {
			atoms := vdbPathToAtoms(r.PackagePath)
			packages = append(packages, atoms...)
		}
	}

	if fix {
		fmt.Printf("\nRebuilding %d packages...\n", len(packages))
		cfg := buildRebuildConfig(repoDir, jobs, func(phase string) {
			fmt.Printf("  [%s]\n", phase)
		}, func(phase string, err error) {
			if err != nil {
				fmt.Printf("  [%s] FAILED: %v\n", phase, err)
			}
		})
		if pretend {
			fmt.Println("(pretend mode: would rebuild these packages)")
			return
		}
		loadAvg := *loadAverage
		if err := runRebuild(context.Background(), packages, cfg, jobs, loadAvg); err != nil {
			fmt.Fprintf(os.Stderr, "rebuild: %v\n", err)
			os.Exit(1)
		}
	}
}

func vdbPathToAtoms(vdbPath string) []string {
	base := filepath.Base(vdbPath)
	parent := filepath.Base(filepath.Dir(vdbPath))
	var atoms []string
	if strings.Contains(base, "-") {
		atoms = append(atoms, "="+parent+"/"+base)
	}
	return atoms
}

func runQuickPkg(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "quickpkg: missing package atom argument\n")
		fmt.Fprintf(os.Stderr, "Usage: arise quickpkg <atom>\n")
		os.Exit(1)
	}

	a, err := atom.Parse(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "quickpkg: parsing atom %q: %v\n", args[0], err)
		os.Exit(1)
	}

	vdbPath := filepath.Join("/var/db/pkg", a.Category, a.Package)
	if a.Version != nil && a.Version.Raw != "" {
		vdbPath = vdbPath + "-" + a.Version.Raw
	}

	ctx := context.Background()
	outPath, err := binpkg.Create(ctx, vdbPath, "/", "/var/cache/binpkgs")
	if err != nil {
		fmt.Fprintf(os.Stderr, "quickpkg: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(outPath)
}

func runDepclean(dbPath, repoDir string) {
	if *pretend {
		fmt.Println("(pretend mode: no actions will be performed)")
	}

	db, err := ingest.OpenDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "depclean: open db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	g, err := graph.BuildParallel(db, repoDir, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "depclean: build graph: %v\n", err)
		os.Exit(1)
	}

	rg := g.ToResolveGraph()

	w, err := world.LoadWorld("/var/lib/portage/world")
	if err != nil {
		fmt.Fprintf(os.Stderr, "depclean: load world: %v\n", err)
		os.Exit(1)
	}

	ws := &resolve.WorldSet{Entries: w.Atoms}

	removals, err := resolve.Depclean(rg, ws)
	if err != nil {
		fmt.Fprintf(os.Stderr, "depclean: %v\n", err)
		os.Exit(1)
	}

	if len(removals) == 0 {
		fmt.Println("\nNothing to remove.")
		return
	}

	fmt.Printf("\nProposed removals (%d packages):\n", len(removals))
	for _, r := range removals {
		fmt.Printf("  [%s] %s  (reason: %s)\n", r.Action, r.Atom, r.Reason)
	}

	if *pretend {
		return
	}

	if *ask {
		fmt.Print("\nWould you like to remove these packages? [y/N] ")
		var response string
		fmt.Scanln(&response)
		if !strings.HasPrefix(strings.ToLower(response), "y") {
			fmt.Println("Aborted.")
			return
		}
	}
}

func runPrune(dbPath, repoDir string) {
	if *pretend {
		fmt.Println("(pretend mode: no actions will be performed)")
	}

	db, err := ingest.OpenDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "prune: open db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	g, err := graph.BuildParallel(db, repoDir, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "prune: build graph: %v\n", err)
		os.Exit(1)
	}

	rg := g.ToResolveGraph()

	removals, err := resolve.Prune(rg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "prune: %v\n", err)
		os.Exit(1)
	}

	if len(removals) == 0 {
		fmt.Println("\nNothing to prune.")
		return
	}

	fmt.Printf("\nProposed removals (%d packages):\n", len(removals))
	for _, r := range removals {
		fmt.Printf("  [%s] %s  (reason: %s)\n", r.Action, r.Atom, r.Reason)
	}

	if *pretend {
		return
	}

	if *ask {
		fmt.Print("\nWould you like to remove these packages? [y/N] ")
		var response string
		fmt.Scanln(&response)
		if !strings.HasPrefix(strings.ToLower(response), "y") {
			fmt.Println("Aborted.")
			return
		}
	}
}

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

func runInfo() {
	fmt.Println("arise 0.1.0-dev")
	fmt.Printf("Go version: %s\n", runtime.Version())
	fmt.Printf("OS: %s\n", runtime.GOOS)
	fmt.Printf("Arch: %s\n", runtime.GOARCH)
	fmt.Println()

	portageConfigRoot := "/etc/portage"
	cfg, err := portage.LoadConfig(portageConfigRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "info: loading portage config: %v\n", err)
	}

	if cfg != nil {
		if cfg.CFLAGS != "" {
			fmt.Printf("CFLAGS: %s\n", cfg.CFLAGS)
		}
		if cfg.CXXFLAGS != "" {
			fmt.Printf("CXXFLAGS: %s\n", cfg.CXXFLAGS)
		}
		if cfg.MAKEOPTS != "" {
			fmt.Printf("MAKEOPTS: %s\n", cfg.MAKEOPTS)
		}
		if len(cfg.ACCEPT_KEYWORDS) > 0 {
			fmt.Printf("ACCEPT_KEYWORDS: %s\n", strings.Join(cfg.ACCEPT_KEYWORDS, " "))
		}
		if len(cfg.FEATURES) > 0 {
			fmt.Printf("FEATURES: %s\n", strings.Join(cfg.FEATURES, " "))
		}
		if val, ok := cfg.MakeConf["CHOST"]; ok && val != "" {
			fmt.Printf("CHOST: %s\n", val)
		}
		if len(cfg.USE) > 0 {
			displayUse := cfg.USE
			if len(displayUse) > 50 {
				displayUse = displayUse[:50]
				fmt.Printf("USE: %s ... (%d total)\n", strings.Join(displayUse, " "), len(cfg.USE))
			} else {
				fmt.Printf("USE: %s\n", strings.Join(displayUse, " "))
			}
		}
	}

	fmt.Println()

	profilePath := filepath.Join(portageConfigRoot, "make.profile")
	if target, err := os.Readlink(profilePath); err == nil {
		fmt.Printf("Profile: %s\n", target)
	} else {
		fmt.Printf("Profile: %s (unable to read link: %v)\n", profilePath, err)
	}

	repoPath := "/var/db/repos/gentoo"
	if info, err := os.Stat(repoPath); err == nil {
		fmt.Printf("Repository: %s (present, last modified: %s)\n", repoPath, info.ModTime().Format("Mon Jan 2 15:04:05 MST 2006"))
	} else {
		fmt.Printf("Repository: %s (not present)\n", repoPath)
	}
}

func runEnvUpdate() {
	envDir := "/etc/env.d"
	outputDir := "/etc"

	if err := env.UpdateEnv(envDir, outputDir); err != nil {
		fmt.Fprintf(os.Stderr, "env-update: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("env-update: regenerated /etc/profile.env and /etc/csh.env")
}

func runLdConfig() {
	fmt.Println("Running ldconfig...")
	if err := env.RunLdConfig("/"); err != nil {
		fmt.Fprintf(os.Stderr, "ldconfig: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("ldconfig complete.")
}

func runConfig(args []string, repoDir string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "config: missing package atom argument\n")
		os.Exit(1)
	}

	a, err := atom.Parse(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: parsing atom %q: %v\n", args[0], err)
		os.Exit(1)
	}

	fmt.Printf("pkg_config for %s\n", a.String())
	if a.Version == nil || a.Version.Raw == "" {
		fmt.Fprintf(os.Stderr, "config: atom must include a version\n")
		os.Exit(1)
	}

	ebuildFile := filepath.Join(repoDir, a.Category, a.Package, a.Package+"-"+a.Version.Raw+".ebuild")
	if _, statErr := os.Stat(ebuildFile); os.IsNotExist(statErr) {
		fmt.Fprintf(os.Stderr, "config: ebuild not found: %s\n", ebuildFile)
		os.Exit(1)
	}

	eb, err := ebuild.ParseEbuild(ebuildFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: parse ebuild: %v\n", err)
		os.Exit(1)
	}

	if _, ok := eb.RawPhases["pkg_config"]; !ok {
		fmt.Printf("No pkg_config phase defined for %s\n", a.String())
		return
	}

	fmt.Println("pkg_config phase found; native execution pending")
}

func runNews(args []string) {
	newsDir := filepath.Join(*repoPath, "metadata", "news")
	markerDir := "/var/lib/gentoo/news"
	subCmd := "list"
	if len(args) > 0 {
		subCmd = args[0]
		args = args[1:]
	}

	switch subCmd {
	case "list":
		items, err := news.ReadNews(newsDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "news: %v\n", err)
			os.Exit(1)
		}

		if len(items) == 0 {
			fmt.Println("No news items found.")
			return
		}

		for _, item := range items {
			readStatus := " "
			unread, err := news.ReadUnreadNews(newsDir, markerDir)
			if err == nil {
				isRead := true
				for _, u := range unread {
					if u.Path == item.Path {
						isRead = false
						break
					}
				}
				if !isRead {
					readStatus = "N"
				}
			}
			fmt.Printf("[%s] %s\n", readStatus, item)
		}
	case "read":
		if len(args) < 1 {
			fmt.Fprintf(os.Stderr, "news read: missing news item specifier\n")
			os.Exit(1)
		}

		items, err := news.ReadNews(newsDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "news: list: %v\n", err)
			os.Exit(1)
		}

		// "all" marks everything as read
		if args[0] == "all" {
			for _, item := range items {
				if err := news.MarkRead(markerDir, item); err != nil {
					fmt.Fprintf(os.Stderr, "news: mark read: %v\n", err)
					os.Exit(1)
				}
			}
			fmt.Printf("Marked %d news items as read.\n", len(items))
			return
		}

		fmt.Fprintf(os.Stderr, "news read: specify 'all' to mark all as read, or use item path\n")
		os.Exit(1)
	case "display":
		if len(args) < 1 {
			fmt.Fprintf(os.Stderr, "news display: missing news item count or specifier\n")
			os.Exit(1)
		}

		items, err := news.ReadUnreadNews(newsDir, markerDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "news: %v\n", err)
			os.Exit(1)
		}

		count := 1
		fmt.Sscanf(args[0], "%d", &count)
		if count > len(items) {
			count = len(items)
		}

		for i := 0; i < count; i++ {
			item := items[i]
			fmt.Printf("\n  Title: %s\n", item.Title)
			fmt.Printf("  Author: %s\n", item.Author)
			fmt.Printf("  Date: %s\n", item.Date)
			fmt.Printf("  Format: %s\n", item.NewsItemFormat)
			fmt.Printf("\n%s\n", item.Body)
		}

		if len(items) > 0 {
			for _, item := range items[:count] {
				if err := news.MarkRead(markerDir, item); err != nil {
					fmt.Fprintf(os.Stderr, "news: mark read: %v\n", err)
				}
			}
		}
	default:
		fmt.Fprintf(os.Stderr, "news: unknown subcommand %q\n", subCmd)
		fmt.Fprintf(os.Stderr, "Usage: arise news [list|read|display]\n")
		os.Exit(1)
	}
}

func parseSortField(s string) search.SortField {
	switch strings.ToLower(s) {
	case "version":
		return search.SortByVersion
	case "slot":
		return search.SortBySlot
	case "name", "package":
		return search.SortByPackage
	default:
		return search.SortByCategory
	}
}

func actionLabel(action string) string {
	switch action {
	case "install":
		return "install"
	case "update":
		return "update"
	case "reinstall":
		return "reinstall"
	case "uninstall":
		return "uninstall"
	default:
		return action
	}
}

func colorIcon(action, label string) string {
	switch action {
	case "install":
		return "[" + color.Green(label) + "]"
	case "update":
		return "[" + color.Cyan(label) + "]"
	case "reinstall":
		return "[" + color.Yellow(label) + "]"
	case "uninstall":
		return "[" + color.Red(label) + "]"
	default:
		return "[" + label + "]"
	}
}

func runPreservedRebuild() {
	if *pretend {
		fmt.Println("(pretend mode: no actions will be performed)")
	}

	fmt.Println("Scanning for preserved-rebuild packages...")

	packages, err := preserved.RebuildNeeded("/", "/var/db/pkg")
	if err != nil {
		fmt.Fprintf(os.Stderr, "preserved-rebuild: %v\n", err)
		os.Exit(1)
	}

	if len(packages) == 0 {
		fmt.Println("No packages need to be rebuilt (no broken preserved links).")
		return
	}

	fmt.Printf("\nThe following %d package(s) need to be rebuilt:\n", len(packages))
	for _, pkg := range packages {
		fmt.Printf("  %s\n", pkg)
	}

	if *pretend {
		return
	}

	if *ask {
		fmt.Print("\nWould you like to rebuild these packages? [y/N] ")
		var response string
		fmt.Scanln(&response)
		if !strings.HasPrefix(strings.ToLower(response), "y") {
			fmt.Println("Aborted.")
			return
		}
	}

	jobs := *jobsVal
	if jobs <= 0 {
		jobs = 1
	}

	cfg := buildRebuildConfig(*repoPath, jobs, func(phase string) {
		fmt.Printf("  [%s]\n", phase)
	}, func(phase string, err error) {
		if err != nil {
			fmt.Printf("  [%s] FAILED: %v\n", phase, err)
		}
	})

	loadAvg := *loadAverage
	if err := runRebuild(context.Background(), packages, cfg, jobs, loadAvg); err != nil {
		fmt.Fprintf(os.Stderr, "preserved-rebuild: %v\n", err)
		os.Exit(1)
	}
}

func runRevdepRebuild() {
	if *pretend {
		fmt.Println("(pretend mode: no actions will be performed)")
	}

	fmt.Println("Scanning for broken reverse dependencies...")

	packages, err := preserved.RevdepRebuild("/", "/var/db/pkg")
	if err != nil {
		fmt.Fprintf(os.Stderr, "revdep-rebuild: %v\n", err)
		os.Exit(1)
	}

	if len(packages) == 0 {
		fmt.Println("No packages with broken dependencies found.")
		return
	}

	fmt.Printf("\nThe following %d package(s) have broken dependencies:\n", len(packages))
	for _, pkg := range packages {
		fmt.Printf("  %s\n", pkg)
	}

	if *pretend {
		return
	}

	if *ask {
		fmt.Print("\nWould you like to rebuild these packages? [y/N] ")
		var response string
		fmt.Scanln(&response)
		if !strings.HasPrefix(strings.ToLower(response), "y") {
			fmt.Println("Aborted.")
			return
		}
	}

	jobs := *jobsVal
	if jobs <= 0 {
		jobs = 1
	}

	cfg := buildRebuildConfig(*repoPath, jobs, func(phase string) {
		fmt.Printf("  [%s]\n", phase)
	}, func(phase string, err error) {
		if err != nil {
			fmt.Printf("  [%s] FAILED: %v\n", phase, err)
		}
	})

	loadAvg := *loadAverage
	if err := runRebuild(context.Background(), packages, cfg, jobs, loadAvg); err != nil {
		fmt.Fprintf(os.Stderr, "revdep-rebuild: %v\n", err)
		os.Exit(1)
	}
}

func runDeselect(atomStr string) {
	worldPath := "/var/lib/portage/world"
	ws, err := world.LoadWorld(worldPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: loading world: %v\n", color.Red("deselect"), err)
		os.Exit(1)
	}

	if !ws.Contains(atomStr) {
		fmt.Printf("%s: %s is not in the world set\n", color.Yellow("deselect"), atomStr)
		return
	}

	ws.Deselect(atomStr)

	if err := ws.Save(worldPath); err != nil {
		fmt.Fprintf(os.Stderr, "%s: saving world: %v\n", color.Red("deselect"), err)
		os.Exit(1)
	}

	fmt.Printf("%s: removed %s from world set\n", color.Green("deselect"), color.Bold(atomStr))
}

func runEquery(args []string, dbPath, repoDir, vdbPath string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "equery: expected subcommand: belongs, files, uses, size, check, which, list\n")
		os.Exit(1)
	}
	subcmd := args[0]
	subArgs := args[1:]

	var arg string
	if len(subArgs) > 0 {
		arg = subArgs[0]
	}

	switch subcmd {
	case "belongs":
		if arg == "" {
			fmt.Fprintf(os.Stderr, "equery belongs: missing file path argument\n")
			os.Exit(1)
		}
		pkg, err := equery.Belongs(vdbPath, arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "equery belongs: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(pkg)

	case "files":
		if arg == "" {
			fmt.Fprintf(os.Stderr, "equery files: missing atom argument\n")
			os.Exit(1)
		}
		files, err := equery.Files(vdbPath, arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "equery files: %v\n", err)
			os.Exit(1)
		}
		for _, f := range files {
			fmt.Println(f)
		}

	case "uses":
		if arg == "" {
			fmt.Fprintf(os.Stderr, "equery uses: missing atom argument\n")
			os.Exit(1)
		}
		db, err := ingest.OpenDB(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "equery uses: open db: %v\n", err)
			os.Exit(1)
		}
		iuse, active, err := equery.Uses(db, vdbPath, arg)
		db.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "equery uses: %v\n", err)
			os.Exit(1)
		}
		if iuse != "" {
			fmt.Printf("IUSE: %s\n", iuse)
		}
		fmt.Printf("Active: %s\n", active)

	case "size":
		if arg == "" {
			fmt.Fprintf(os.Stderr, "equery size: missing atom argument\n")
			os.Exit(1)
		}
		size, err := equery.Size(vdbPath, arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "equery size: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(formatSize(size))

	case "check":
		if arg == "" {
			fmt.Fprintf(os.Stderr, "equery check: missing atom argument\n")
			os.Exit(1)
		}
		mismatches, err := equery.Check(vdbPath, arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "equery check: %v\n", err)
			os.Exit(1)
		}
		if len(mismatches) == 0 {
			fmt.Println("OK")
		} else {
			for _, m := range mismatches {
				fmt.Println(m)
			}
		}

	case "which":
		if arg == "" {
			fmt.Fprintf(os.Stderr, "equery which: missing atom argument\n")
			os.Exit(1)
		}
		path, err := equery.Which(repoDir, arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "equery which: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(path)

	case "list":
		packages, err := equery.List(vdbPath, arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "equery list: %v\n", err)
			os.Exit(1)
		}
		for _, p := range packages {
			fmt.Println(p)
		}

	default:
		fmt.Fprintf(os.Stderr, "equery: unknown subcommand %q\n", subcmd)
		fmt.Fprintf(os.Stderr, "Expected: belongs, files, uses, size, check, which, list\n")
		os.Exit(1)
	}
}

func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}