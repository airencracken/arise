package main

import (
	"context"
	"fmt"
	"os"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/distfiles"
	"github.com/airencracken/arise/internal/features"
	"github.com/airencracken/arise/internal/graph"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/rebuild"
	"github.com/airencracken/arise/internal/resolve"
	"github.com/airencracken/arise/internal/world"
)

func runInstall(args []string, dbPath, repoDir string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "install: missing package atom arguments\n")
		os.Exit(1)
	}
	runResolveAndRebuild(args, dbPath, repoDir, false, false)
}

func colorActionAtom(action resolve.PkgAction) string {
	if action.Atom == nil {
		return ""
	}
	cp := action.Atom.Category + "/" + action.Atom.Package
	version := ""
	if action.Atom.Version != nil {
		version = "-" + action.Atom.Version.Raw
	}
	atomText := cp + version
	switch action.Action {
	case "install":
		atomText = color.BoldGreen(atomText)
	case "update":
		atomText = color.BoldCyan(atomText)
	case "reinstall":
		atomText = color.BoldYellow(atomText)
	default:
		atomText = color.Bold(atomText)
	}
	if action.Slot != "" {
		atomText += color.Yellow(":" + action.Slot)
	}
	if action.Repository != "" {
		atomText += color.Magenta("::" + action.Repository)
	}
	return atomText
}

func runResolveAndRebuild(targets []string, dbPath, repoDir string, update bool, deep bool) {
	cfg := resolveFlagsToConfig(update, deep)
	runResolve(targets, dbPath, repoDir, cfg)
}

func buildRebuildConfig(repoDir string, jobs int, phaseStart func(string), phaseEnd func(string, error)) *rebuild.RebuildConfig {
	portageCfg, _ := portage.LoadEffectiveConfig(*portageConfigRoot)
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

	cfg := &rebuild.RebuildConfig{
		RepoDir:      repoDir,
		DistfilesDir: *distfilesDir,
		RootDir:      "/",
		VdbDir:       *vdbDir,
		WorkDirBase:  *workDir,
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

func runRebuild(ctx context.Context, atoms []string, cfg *rebuild.RebuildConfig, jobs int, loadAvg float64) error {
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
		Backtrack:                   *backtrackVal,
		Deep:                        deepParam || *deep,
		CompleteGraph:               *completeGraph,
		NewUse:                      *newuse,
		Update:                      update,
		Oneshot:                     *oneshot,
		NoDeps:                      *nodeps,
		OnlyDeps:                    *onlydeps,
		EmptyTree:                   *emptytree,
		Reinstall:                   *reinstall,
		ChangedUse:                  *changedUse,
		ChangedDeps:                 *changedDeps,
		KeepGoing:                   *keepGoing,
		FetchOnly:                   *fetchOnly,
		BuildPkgOnly:                *buildPkgOnly,
		BuildPkg:                    *buildPkg,
		UsePkg:                      *usePkg,
		UsePkgOnly:                  *usePkgOnly,
		Pretend:                     *pretend,
		Ask:                         *ask,
		Quiet:                       *quiet,
		Verbose:                     *verbose,
		Tree:                        *tree,
		Resume:                      *resume,
		SkipFirst:                   *skipFirst,
		UnsortedDisplay:             *unorderedDisp,
		AutoUnmaskWrite:             *autoUnmaskW,
		Jobs:                        *jobsVal,
		LoadAverage:                 *loadAverage,
		WithBdeps:                   *withBdeps,
		WithBdepsAuto:               *withBdeps == "auto",
		BinpkgRespectUse:            *binpkgRespectUse,
		IgnoreBuiltSlotOperatorDeps: *ignoreBuiltSlotOps,
		BinpkgDir:                   *binpkgDir,
		GetBinPkg:                   *getbinpkg,
		GetBinPkgOnly:               *getbinpkgOnly,
		NoReplace:                   *noreplace,
	}
}

func runResolve(targets []string, dbPath, repoDir string, cfg resolve.ResolveConfig) {
	jsonMode := *jsonOutput
	if jsonMode {
		cfg.Quiet = true
	}
	if *pretend && !jsonMode {
		fmt.Println("(pretend mode: no actions will be performed)")
	}

	// Parse PORTAGE_BINHOST from portage config
	portageCfg, configErr := portage.LoadEffectiveConfig(*portageConfigRoot)
	if configErr != nil {
		fmt.Fprintf(os.Stderr, "resolve: load effective Portage configuration: %v\n", configErr)
		os.Exit(1)
	}
	if portageCfg != nil {
		if cfg.PortageConfig == nil {
			cfg.PortageConfig = portageCfg
		}
		binhostURLs := portage.ParseBinhostConfig(portageCfg)
		if len(binhostURLs) > 0 {
			cfg.BinhostURLs = binhostURLs
		}
		cfg.SystemSet = &resolve.WorldSet{Entries: append([]string(nil), portageCfg.SystemSet...)}
	}
	for _, target := range targets {
		if target != "@world" {
			continue
		}
		worldState, err := world.LoadWorld(*worldFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolve: load world set: %v\n", err)
			os.Exit(1)
		}
		cfg.WorldSet = &resolve.WorldSet{Entries: worldState.Atoms}
		break
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
				} else if !cfg.Quiet {
					fmt.Printf("Downloaded %d binary packages from %s\n", len(downloaded), url)
				}
			}
		}
	}

	// --resume: load remaining packages from previous interrupted operation
	if cfg.Resume {
		if cfg.SkipFirst {
			if err := resolve.SkipFirstResume(*resumeFile); err != nil {
				fmt.Fprintf(os.Stderr, "resume: skipfirst: %v\n", err)
			}
		}
		remaining, err := resolve.LoadResume(*resumeFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "resume: load: %v\n", err)
			os.Exit(1)
		}
		if len(remaining) == 0 {
			fmt.Println("Nothing to resume.")
			os.Remove(*resumeFile)
			return
		}
		targets = remaining
		if !cfg.Quiet {
			fmt.Printf("Resuming %d packages...\n", len(targets))
		}
	}

	if !cfg.Quiet {
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

	resolutionStarted := time.Now()
	progress := startTerminalProgress("Calculating dependencies...", !cfg.Quiet && !jsonMode)
	stageStarted := time.Now()
	db, err := ingest.OpenReadOnlyDB(dbPath)
	if err != nil {
		progress.stop()
		fmt.Fprintf(os.Stderr, "resolve: open db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()
	openDuration := time.Since(stageStarted)

	stageStarted = time.Now()
	g, err := graph.BuildFromState(db, *vdbDir, 0)
	if err != nil {
		progress.stop()
		fmt.Fprintf(os.Stderr, "resolve: build graph: %v\n", err)
		os.Exit(1)
	}
	stateDuration := time.Since(stageStarted)

	stageStarted = time.Now()
	rg := g.ToResolveGraph()
	graphDuration := time.Since(stageStarted)

	stageStarted = time.Now()
	var cpuProfile *os.File
	if path := os.Getenv("ARISE_CPU_PROFILE"); path != "" {
		cpuProfile, err = os.Create(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolve: create CPU profile: %v\n", err)
		} else if err = pprof.StartCPUProfile(cpuProfile); err != nil {
			fmt.Fprintf(os.Stderr, "resolve: start CPU profile: %v\n", err)
			cpuProfile.Close()
			cpuProfile = nil
		}
	}
	result, err := resolve.Resolve(rg, targets, cfg)
	if cpuProfile != nil {
		pprof.StopCPUProfile()
		cpuProfile.Close()
	}
	solverDuration := time.Since(stageStarted)
	progress.stop()
	if result == nil {
		result = &resolve.ResolveResult{}
	}
	resolutionDuration := time.Since(resolutionStarted)
	if jsonMode {
		if jsonErr := writePlanJSON(os.Stdout, targets, cfg, result, err, planTimings{
			Total: resolutionDuration, Index: openDuration, State: stateDuration, Graph: graphDuration, Solver: solverDuration,
		}); jsonErr != nil {
			fmt.Fprintf(os.Stderr, "encode JSON plan: %v\n", jsonErr)
			os.Exit(1)
		}
	}
	if !cfg.Quiet {
		fmt.Printf("Dependency resolution took %.3f s (backtrack: %d/%d).\n",
			resolutionDuration.Seconds(), result.BacktrackLevel, cfg.Backtrack)
		if cfg.Verbose {
			fmt.Printf("  Stages: index %.3f s, state %.3f s, graph %.3f s, solver %.3f s\n",
				openDuration.Seconds(), stateDuration.Seconds(), graphDuration.Seconds(), solverDuration.Seconds())
			fmt.Printf("  Solver: search %.3f s, complete-graph %.3f s, verification %.3f s, sort %.3f s\n",
				result.Metrics.Search.Seconds(), result.Metrics.CompleteGraph.Seconds(), result.Metrics.Verification.Seconds(), result.Metrics.Sort.Seconds())
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)

		// --autounmask-write: generate unmask/license entries on failure
		if cfg.AutoUnmaskWrite {
			if *autoUnmaskW {
				if err := resolve.AutoUnmask(result.Conflicts, *portageConfigRoot); err != nil {
					fmt.Fprintf(os.Stderr, "auto-unmask: %v\n", err)
				} else {
					fmt.Printf("Auto-unmask entries written to %s/package.unmask/\n", *portageConfigRoot)
				}
				if err := resolve.AutoAcceptLicense(result.Conflicts, *portageConfigRoot); err != nil {
					fmt.Fprintf(os.Stderr, "autounmask-write: %v\n", err)
				} else {
					fmt.Printf("Auto-accept-license entries written to %s/package.license/\n", *portageConfigRoot)
				}
				if err := resolve.AutoUseChanges(result.Conflicts, *portageConfigRoot); err != nil {
					fmt.Fprintf(os.Stderr, "autounmask-write USE: %v\n", err)
				}
			}
		}
		os.Exit(1)
	}

	if len(result.Conflicts) > 0 && !cfg.Quiet {
		fmt.Println("\nConflicts:")
		for _, c := range result.Conflicts {
			fmt.Printf("  %s\n", c)
			if cfg.Verbose {
				for _, detail := range result.ConflictDetails {
					if detail.Message != c {
						continue
					}
					for _, requirement := range detail.Requirements {
						if requirement.Reason != "" {
							fmt.Printf("    %s — %s\n", requirement.Atom, requirement.Reason)
						} else {
							fmt.Printf("    %s\n", requirement.Atom)
						}
					}
					for _, candidate := range detail.Candidates {
						visibility := ""
						if !candidate.Visible && candidate.Visibility != "" {
							visibility = "; " + candidate.Visibility
						}
						fmt.Printf("    candidate %s (%s%s): satisfies [%s], rejects [%s]\n",
							candidate.CPV, candidate.State, visibility,
							strings.Join(candidate.Satisfies, ", "), strings.Join(candidate.Rejects, ", "))
					}
				}
			}
		}
	}
	if len(result.Warnings) > 0 && !cfg.Quiet {
		fmt.Println("\nWarnings:")
		for _, warning := range result.Warnings {
			fmt.Printf("  %s\n", warning)
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
				fmt.Printf("  %s %s\n", colorIcon(a.Action, actionLabel), colorActionAtom(a))
				if cfg.Verbose {
					if a.Reason != "" {
						fmt.Printf("           reason: %s\n", a.Reason)
					}
					enabled, disabled := sortedUseFlags(a.UseFlags)
					if len(enabled) > 0 || len(disabled) > 0 {
						fmt.Printf("           USE: %s\n", strings.Join(append(enabled, disabled...), " "))
					}
				}
			}
			for _, a := range result.Uninstall {
				fmt.Printf("  [%s] %s\n", color.Red(a.Action), a.Atom)
			}
		}
		printActionTotals(result.Install, *distfilesDir, cfg.Verbose)
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
				resolve.AutoUnmask(result.Conflicts, *portageConfigRoot)
				resolve.AutoAcceptLicense(result.Conflicts, *portageConfigRoot)
				resolve.AutoUseChanges(result.Conflicts, *portageConfigRoot)
				fmt.Printf("Auto-unmask entries written to %s/package.unmask/ and %s/package.license/\n", *portageConfigRoot, *portageConfigRoot)
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
		os.Remove(*resumeFile)
		return
	}

	// Pretend is a read-only operation and must work for unprivileged users.
	if cfg.Pretend {
		return
	}

	// Save resume state for --resume support
	if err := resolve.SaveResume(*resumeFile, result); err != nil && !*quiet {
		fmt.Fprintf(os.Stderr, "resume: save: %v\n", err)
	}

	if cfg.FetchOnly {
		return
	}
}

func sortedUseFlags(flags map[string]bool) ([]string, []string) {
	var enabled, disabled []string
	for flag, value := range flags {
		if value {
			enabled = append(enabled, "+"+flag)
		} else {
			disabled = append(disabled, "-"+flag)
		}
	}
	sort.Strings(enabled)
	sort.Strings(disabled)
	return enabled, disabled
}

func printActionTotals(actions []resolve.PkgAction, distdir string, verbose bool) {
	counts := make(map[string]int)
	var downloadBytes int64
	for _, action := range actions {
		counts[action.Action]++
		if action.Atom == nil {
			continue
		}
		size, err := distfiles.ManifestDownloadSize(action.RepositoryPath, action.Atom.Category, action.Atom.Package, action.SrcURI, distdir, action.UseFlags)
		if err != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "totals: %s: %v\n", action.Atom.CP(), err)
			}
			continue
		}
		downloadBytes += size
	}
	packageWord := "packages"
	if len(actions) == 1 {
		packageWord = "package"
	}
	var details []string
	for _, entry := range []struct{ action, label string }{{"install", "new"}, {"update", "upgrade"}, {"reinstall", "reinstall"}} {
		if count := counts[entry.action]; count > 0 {
			details = append(details, fmt.Sprintf("%d %s", count, entry.label))
		}
	}
	fmt.Printf("Total: %d %s", len(actions), packageWord)
	if len(details) > 0 {
		fmt.Printf(" (%s)", strings.Join(details, ", "))
	}
	fmt.Printf(", Size of downloads: %s KiB\n", formatInteger((downloadBytes+1023)/1024))
}

func formatInteger(value int64) string {
	raw := strconv.FormatInt(value, 10)
	for index := len(raw) - 3; index > 0; index -= 3 {
		raw = raw[:index] + "," + raw[index:]
	}
	return raw
}
