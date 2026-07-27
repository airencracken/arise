package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/distfiles"
	"github.com/airencracken/arise/internal/executor"
	"github.com/airencracken/arise/internal/features"
	"github.com/airencracken/arise/internal/fetch"
	"github.com/airencracken/arise/internal/graph"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/phaseproto"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/rebuild"
	"github.com/airencracken/arise/internal/recoveryset"
	"github.com/airencracken/arise/internal/resolve"
	"github.com/airencracken/arise/internal/world"
)

func runInstall(args []string, dbPath, repoDir string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "install: missing package atom arguments\n")
		os.Exit(1)
	}
	runResolveAndRebuild(args, dbPath, repoDir, *updateMode, false)
}

func recoveryPackagesForActions(vdbRoot string, actions []resolve.PkgAction) []recoveryset.Package {
	seen := make(map[string]struct{})
	var packages []recoveryset.Package
	for _, action := range actions {
		if action.Atom == nil || action.InstalledVersion == "" {
			continue
		}
		path := filepath.Join(vdbRoot, action.Atom.Category, action.Atom.Package+"-"+action.InstalledVersion)
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		packages = append(packages, recoveryset.Package{VDBEntryPath: path})
	}
	return packages
}

func installWorldSelections(targets []string, cfg resolve.ResolveConfig, result *resolve.ResolveResult) []string {
	if cfg.Oneshot || cfg.OnlyDeps {
		return nil
	}
	systemPackages := make(map[string]bool)
	if cfg.SystemSet != nil {
		for _, entry := range cfg.SystemSet.Entries {
			a, err := atom.ParsePackageAtom(entry)
			if err == nil && a.Category != "virtual" {
				systemPackages[a.CP()] = true
			}
		}
	}
	selected := make(map[string]bool)
	hasNameOnlyTarget := false
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" || strings.HasPrefix(target, "@") {
			continue
		}
		a, err := atom.ParsePackageAtom(target)
		if err == nil {
			if !systemPackages[a.CP()] {
				selected[a.CP()] = true
			}
			continue
		}
		if !strings.Contains(target, "/") {
			hasNameOnlyTarget = true
		}
	}
	if hasNameOnlyTarget && result != nil {
		for _, action := range result.Install {
			if action.Atom != nil && action.Reason == "explicit target" {
				if cp := action.Atom.CP(); !systemPackages[cp] {
					selected[cp] = true
				}
			}
		}
	}
	selections := make([]string, 0, len(selected))
	for selection := range selected {
		selections = append(selections, selection)
	}
	sort.Strings(selections)
	return selections
}

func updateInstallWorld(path string, selections []string) error {
	if len(selections) == 0 {
		return nil
	}
	return world.Update(path, func(set *world.WorldSet) error {
		for _, selection := range selections {
			world.Add(set, selection)
		}
		return nil
	})
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
		slot := action.Slot
		if action.Subslot != "" && action.Subslot != action.Slot {
			slot += "/" + action.Subslot
		}
		atomText += color.Yellow(":" + slot)
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
	if portageCfg == nil {
		portageCfg = &portage.Config{MakeConf: make(map[string]string)}
	}
	var featConfig *features.Config
	if portageCfg != nil {
		if rawFeatures, ok := portageCfg.MakeConf["FEATURES"]; ok {
			featConfig = features.ParseFeatures(rawFeatures)
		}
	}

	cfg := &rebuild.RebuildConfig{
		RepoDir:       repoDir,
		DistfilesDir:  *distfilesDir,
		RootDir:       commandEnv("ROOT", "/"),
		VdbDir:        *vdbDir,
		WorkDirBase:   *workDir,
		CFLAGS:        portageCfg.CFLAGS,
		CXXFLAGS:      portageCfg.CXXFLAGS,
		LDFLAGS:       portageCfg.MakeConf["LDFLAGS"],
		MAKEOPTS:      portageCfg.MAKEOPTS,
		Arch:          portageCfg.MakeConf["ARCH"],
		Features:      featConfig,
		GentooMirrors: strings.Fields(portageCfg.MakeConf["GENTOO_MIRRORS"]),
		OnPhaseStart:  phaseStart,
		OnPhaseEnd:    phaseEnd,
		PhaseProtocol: true,
		PortageConfig: portageCfg,
		ConfigRoot:    *portageConfigRoot,
		JournalDir:    *journalDir,
		PackageDir:    *binpkgDir,
		BuildPackage:  *buildPkg || *buildPkgOnly,
		BuildOnly:     *buildPkgOnly,
	}
	if repositories, err := portage.RepositoryPolicyOrder(filepath.Join(*portageConfigRoot, "repos.conf")); err == nil {
		cfg.Repositories = repositories
	}
	if portageCfg != nil {
		cfg.PhaseLogDir = portageCfg.MakeConf["PORTAGE_LOGDIR"]
		if cfg.PhaseLogDir == "" {
			cfg.PhaseLogDir = commandRootPath("/var/log/portage")
		} else if commandEnv("ROOT", "/") != "/" && filepath.IsAbs(cfg.PhaseLogDir) {
			cfg.PhaseLogDir = commandRootPath(cfg.PhaseLogDir)
		}
		cfg.LogFilterCommand = portageCfg.MakeConf["PORTAGE_LOG_FILTER_FILE_CMD"]
		cfg.ElogClasses = strings.Fields(portageCfg.MakeConf["PORTAGE_ELOG_CLASSES"])
		cfg.ElogSinks = strings.Fields(portageCfg.MakeConf["PORTAGE_ELOG_SYSTEM"])
		cfg.ElogOutput = os.Stdout
	}
	if featConfig != nil {
		cfg.SplitLogs = featConfig.IsEnabled(features.FeatSplitLog)
		cfg.CompressLogs = featConfig.IsEnabled(features.Flag("compress-build-logs"))
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
		OnlyDepsWithRdeps:           *onlydepsWithRdeps,
		OnlyDepsWithIDeps:           *onlydepsWithIDeps,
		RootDeps:                    *rootDeps,
		EmptyTree:                   *emptytree,
		Reinstall:                   *reinstall,
		ChangedUse:                  *changedUse,
		ChangedDeps:                 *changedDeps,
		DynamicDeps:                 *dynamicDeps,
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
	rootDir := commandEnv("ROOT", "/")
	cfg.PackageSetExpander = func(setName string) ([]string, error) {
		return world.ExpandSet(setName, rootDir, *vdbDir)
	}
	for _, target := range targets {
		if target == "@preserved-rebuild" {
			cfg.Reinstall = true
			cfg.Oneshot = true
		}
	}
	// Portage's ordinary -uD @world/@system operation repairs the selected
	// installed closure as providers change. Requiring users to add Arise's
	// lower-level --complete-graph switch makes the familiar emerge invocation
	// produce a knowingly non-executable plan, so enable that repair mode for
	// deep set updates.
	if cfg.Update && cfg.Deep && targetsNeedCompleteGraph(targets) {
		cfg.CompleteGraph = true
	}
	if jsonMode {
		cfg.Quiet = true
	}
	// Fetching binary packages also selects them. Portage treats -g as -k plus
	// acquisition and -G as -K plus acquisition.
	if cfg.GetBinPkg {
		cfg.UsePkg = true
	}
	if cfg.GetBinPkgOnly {
		cfg.UsePkgOnly = true
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
		} else if err := downloadBinpkgTargets(context.Background(), cfg.BinhostURLs, targets, cfg.BinpkgDir, cfg.GetBinPkgOnly, cfg.Quiet); err != nil {
			fmt.Fprintf(os.Stderr, "--getbinpkg: %v\n", err)
			os.Exit(1)
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
	progress := startTerminalProgressMode("Calculating dependencies...", !cfg.Quiet && !jsonMode, false)
	stageStarted := time.Now()
	progress.setStatus("Loading resolver index...")
	db, err := ingest.OpenReadOnlyDB(dbPath)
	if err != nil {
		progress.stop()
		fmt.Fprintf(os.Stderr, "resolve: open db: %v\n", err)
		os.Exit(1)
	}
	openDuration := time.Since(stageStarted)

	stageStarted = time.Now()
	progress.setStatus("Building installed-state graph...")
	g, err := graph.BuildFromState(db, *vdbDir, 0)
	if err != nil {
		progress.stop()
		fmt.Fprintf(os.Stderr, "resolve: build graph: %v\n", err)
		os.Exit(1)
	}
	stateDuration := time.Since(stageStarted)

	stageStarted = time.Now()
	progress.setStatus("Constructing resolver graph...")
	rg := g.ToResolveGraph()
	graphDuration := time.Since(stageStarted)

	stageStarted = time.Now()
	progress.setStatus("Solving dependency graph...")
	resolveCtx := commandContext
	cancelResolve := func() {}
	if *resolverTimeout > 0 {
		resolveCtx, cancelResolve = context.WithTimeout(resolveCtx, *resolverTimeout)
	}
	defer cancelResolve()
	// Resolve once with source fallback to discover the complete dependency
	// closure that a remote binhost must satisfy. Downloading only the command
	// line targets misses binary dependencies and makes -g/-G non-transitive.
	if (cfg.GetBinPkg || cfg.GetBinPkgOnly) && len(cfg.BinhostURLs) != 0 {
		probeCfg := cfg
		probeCfg.GetBinPkg = false
		probeCfg.GetBinPkgOnly = false
		probeCfg.UsePkgOnly = false
		probeCfg.UsePkg = true
		probe, probeErr := resolve.ResolveContext(resolveCtx, rg, targets, probeCfg)
		if probeErr == nil && probe != nil {
			closure := make([]string, 0, len(probe.Install))
			for _, action := range probe.Install {
				if action.Atom != nil {
					closure = append(closure, action.Atom.String())
				}
			}
			if downloadErr := downloadBinpkgTargets(resolveCtx, cfg.BinhostURLs, closure, cfg.BinpkgDir, cfg.GetBinPkgOnly, cfg.Quiet); downloadErr != nil {
				err = fmt.Errorf("--getbinpkg: download dependency closure: %w", downloadErr)
			}
		} else if cfg.GetBinPkgOnly {
			err = fmt.Errorf("--getbinpkgonly: discover dependency closure: %w", probeErr)
		}
	}
	var result *resolve.ResolveResult
	if err == nil {
		result, err = resolve.ResolveContext(resolveCtx, rg, targets, cfg)
	}
	solverDuration := time.Since(stageStarted)
	if closeErr := db.Close(); closeErr != nil {
		progress.stop()
		fmt.Fprintf(os.Stderr, "resolve: close db: %v\n", closeErr)
		exitAfterRuntimeProfiles(1)
	}
	progress.stop()
	if result == nil {
		result = &resolve.ResolveResult{}
	}
	resolutionDuration := time.Since(resolutionStarted)
	stateSHA256 := ""
	stateFingerprintStarted := time.Now()
	if result.Verified && (jsonMode || *savePlan != "" || *approvePlanSHA256 != "" || *approvePlan != "" || (!cfg.Pretend && !*preflightOnly)) {
		stateSHA256, err = mutationStateSHA256(*vdbDir, *worldFile, *portageConfigRoot, result.Install)
		if err != nil {
			fmt.Fprintf(os.Stderr, "arise: fingerprint mutation state: %v\n", err)
			exitAfterRuntimeProfiles(1)
		}
	}
	stateFingerprintDuration := time.Since(stateFingerprintStarted)
	if jsonMode || *savePlan != "" {
		var encoded bytes.Buffer
		if jsonErr := writePlanJSON(&encoded, targets, cfg, result, err, planTimings{
			Total: resolutionDuration, Index: openDuration, State: stateDuration, Graph: graphDuration, Solver: solverDuration,
			StateFingerprint: stateFingerprintDuration, StateSHA256: stateSHA256,
		}); jsonErr != nil {
			fmt.Fprintf(os.Stderr, "encode JSON plan: %v\n", jsonErr)
			exitAfterRuntimeProfiles(1)
		}
		if jsonMode {
			if _, writeErr := os.Stdout.Write(encoded.Bytes()); writeErr != nil {
				fmt.Fprintf(os.Stderr, "write JSON plan: %v\n", writeErr)
				exitAfterRuntimeProfiles(1)
			}
		}
		if *savePlan != "" {
			path, saveErr := savePlanDocument(*savePlan, *planDir, encoded.Bytes())
			if saveErr != nil {
				fmt.Fprintf(os.Stderr, "save plan: %v\n", saveErr)
				exitAfterRuntimeProfiles(1)
			}
			fmt.Fprintf(os.Stderr, "Saved plan to %s\n", path)
		}
	}
	if !cfg.Quiet {
		fmt.Printf("Dependency resolution took %.3f s (backtrack: %d/%d).\n",
			resolutionDuration.Seconds(), result.BacktrackLevel, cfg.Backtrack)
		if cfg.Verbose {
			fmt.Printf("  Stages: index %.3f s, state %.3f s, graph %.3f s, solver %.3f s\n",
				openDuration.Seconds(), stateDuration.Seconds(), graphDuration.Seconds(), solverDuration.Seconds())
			fmt.Printf("  Solver: search %.3f s, direct-refresh %.3f s, complete-graph %.3f s, verification %.3f s, sort %.3f s\n",
				result.Metrics.Search.Seconds(), result.Metrics.DirectUpdateRefresh.Seconds(), result.Metrics.CompleteGraph.Seconds(), result.Metrics.Verification.Seconds(), result.Metrics.Sort.Seconds())
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
		exitAfterRuntimeProfiles(1)
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
	displayWarnings := warningsForDisplay(result.Warnings, cfg.Verbose)
	if len(displayWarnings) > 0 && !cfg.Quiet {
		fmt.Println("\nWarnings:")
		for _, warning := range displayWarnings {
			fmt.Printf("  %s\n", warning)
		}
	}

	if !cfg.Quiet {
		estimates := mergeEstimates(nil)
		if *showEstimates {
			estimates = loadMergeEstimates(*emergeLog)
		}
		fmt.Printf("\n%s\n", planHeading(result, cfg.FetchOnly))
		downloadSizes := planActionDownloadSizes(result.Install, *distfilesDir, cfg.Verbose)

		if cfg.Tree && !cfg.FetchOnly {
			fmt.Print(resolve.FormatTree(result.Install, rg))
		} else {
			for _, a := range result.Install {
				fmt.Printf("  %s %s", portageActionHeader(a, cfg.FetchOnly), colorActionAtom(a))
				if previous := portagePreviousIdentity(a); previous != "" {
					fmt.Printf(" [%s]", previous)
				}
				if estimate, ok := estimates.forAction(a); ok {
					fmt.Printf("  (estimated %s)", formatEstimate(estimate))
				}
				if use := portageUseDisplay(a); use != "" {
					fmt.Printf(" %s", use)
				}
				fmt.Printf(" %s KiB", formatInteger((downloadSizes[resolve.ActionIdentity(a)]+1023)/1024))
				fmt.Println()
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
		printActionTotals(result.Install, downloadSizes, cfg.FetchOnly)
		if *showEstimates {
			var total time.Duration
			covered := 0
			for _, action := range result.Install {
				if estimate, ok := estimates.forAction(action); ok {
					total += estimate
					covered++
				}
			}
			if covered > 0 {
				fmt.Printf("Estimated merge time: %s (%d of %d packages with history)\n", formatEstimate(total), covered, len(result.Install))
			} else {
				fmt.Println("Estimated merge time: unavailable (no matching history)")
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
				resolve.AutoUnmask(result.Conflicts, *portageConfigRoot)
				resolve.AutoAcceptLicense(result.Conflicts, *portageConfigRoot)
				resolve.AutoUseChanges(result.Conflicts, *portageConfigRoot)
				if !jsonMode {
					fmt.Printf("Auto-unmask entries written to %s/package.unmask/ and %s/package.license/\n", *portageConfigRoot, *portageConfigRoot)
					fmt.Println("Re-run with the same command to continue.")
				}
			}
			if !jsonMode {
				fmt.Println("\nCannot proceed with unresolved conflicts.")
			}
			exitAfterRuntimeProfiles(1)
		}
		// KeepGoing: with partial results, ask user if they want to proceed
		if !jsonMode && cfg.KeepGoing && len(result.Install) > 0 {
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
	if !result.Verified && (jsonMode || cfg.Pretend) {
		// --keep-going controls how much diagnostic work the resolver preserves;
		// it must never turn an unresolved, non-executable pretend plan into a
		// successful command result.
		exitAfterRuntimeProfiles(1)
	}

	if len(result.Install) == 0 && len(result.Uninstall) == 0 {
		selections := installWorldSelections(targets, cfg, result)
		if !cfg.Pretend && !*preflightOnly {
			if err := updateInstallWorld(*worldFile, selections); err != nil {
				fmt.Fprintf(os.Stderr, "arise: update world selection: %v\n", err)
				os.Exit(1)
			}
		}
		if !cfg.Quiet {
			fmt.Println("\nNothing to do.")
		}
		os.Remove(*resumeFile)
		return
	}
	if *preflightOnly {
		if err := planExecutionVerificationError(result); err != nil {
			fmt.Fprintln(os.Stderr, err)
			exitAfterRuntimeProfiles(1)
		}
		// A read-only preflight does not require mutation authorization, but when
		// the caller supplies one it must audit the same approval that execution
		// will enforce. Otherwise a stale saved plan can appear to pass preflight
		// and then fail before the real run starts.
		if *approvePlanSHA256 != "" || *approvePlan != "" {
			// Supplying approval to read-only preflight means "audit this
			// digest", not "enable mutation". The live-mutation pairing is
			// enforced only on the execution path below.
			if err := requestedPlanAuthorizationError(*approvePlanSHA256, *approvePlan, *planDir, targets, cfg, result, stateSHA256); err != nil {
				fmt.Fprintf(os.Stderr, "arise: refusing preflight: %v\n", err)
				exitAfterRuntimeProfiles(1)
			}
		}
		rebuildCfg := buildRebuildConfig(repoDir, cfg.Jobs, nil, nil)
		if filepath.Clean(rebuildCfg.RootDir) == string(filepath.Separator) {
			rebuildCfg.AllowLiveRoot = true
		}
		if err := os.MkdirAll(rebuildCfg.WorkDirBase, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "Preflight could not prepare work directory %s: %v\n", rebuildCfg.WorkDirBase, err)
			exitAfterRuntimeProfiles(1)
		}
		failures := executor.PreflightAll(result, *rebuildCfg)
		if len(result.Uninstall) > 0 {
			fmt.Fprintf(os.Stderr, "Preflight notice: %d removal action(s) require the uninstall transaction path and are not covered by package build preflight.\n", len(result.Uninstall))
		}
		if len(failures) > 0 {
			fmt.Fprintf(os.Stderr, "Preflight failed for %d of %d install actions:\n\n", len(failures), len(result.Install))
			for _, failure := range failures {
				fmt.Fprintf(os.Stderr, "  %v\n", failure)
			}
			exitAfterRuntimeProfiles(1)
		}
		fmt.Printf("Preflight passed for all %d install actions (recipe/policy only); build tools were not executed and no package state was mutated.\n", len(result.Install))
		return
	}

	// Pretend is a read-only operation and must work for unprivileged users.
	if cfg.Pretend {
		return
	}
	if err := planExecutionVerificationError(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Close the most obvious approval TOCTOU window: authorization is bound to
	// a fresh fingerprint taken after all interactive/output work and directly
	// before execution. The merge lock and journal provide the mutation-side
	// boundary; a changed package database, policy, recipe, or eclass changes
	// the canonical plan digest and fails closed here.
	if *approvePlanSHA256 != "" || *approvePlan != "" {
		currentStateSHA256, fingerprintErr := mutationStateSHA256(*vdbDir, *worldFile, *portageConfigRoot, result.Install)
		if fingerprintErr != nil {
			fmt.Fprintf(os.Stderr, "arise: refusing execution: refresh mutation-state fingerprint: %v\n", fingerprintErr)
			os.Exit(1)
		}
		if stateSHA256 != "" && currentStateSHA256 != stateSHA256 {
			fmt.Fprintln(os.Stderr, "arise: refusing execution: package state or policy changed after resolution; resolve and approve the new plan")
			os.Exit(1)
		}
		stateSHA256 = currentStateSHA256
	}
	if *approvePlanSHA256 != "" || *approvePlan != "" {
		if err := requestedPlanAuthorizationError(*approvePlanSHA256, *approvePlan, *planDir, targets, cfg, result, stateSHA256); err != nil {
			fmt.Fprintf(os.Stderr, "arise: refusing execution: %v\n", err)
			os.Exit(1)
		}
	}
	if cfg.FetchOnly {
		progress := newFetchProgress(!cfg.Quiet, os.Stdout)
		progress.setConcurrent(normalizedFetchJobs(*fetchJobs, len(result.Install)) > 1)
		if err := fetchPlanActions(commandContext, result.Install, fetch.FetchConfig{DistfilesDir: *distfilesDir, GentooMirrors: strings.Fields(portageCfg.MakeConf["GENTOO_MIRRORS"]), Progress: progress.Report}, &fetch.Fetcher{}, *fetchJobs); err != nil {
			fmt.Fprintf(os.Stderr, "arise: fetch-only failed: %v\n", err)
			os.Exit(1)
		}
		if !cfg.Quiet {
			fmt.Println("All source artifacts are present and Manifest-verified.")
		}
		return
	}
	{
		// Package transactions remain dependency-ordered by the executor, while
		// the package's build system follows explicit --jobs or configured
		// MAKEOPTS. Serializing every compiler invocation made large canaries look
		// stalled and diverged from the approved Portage configuration.
		executionProgress := startTerminalProgressMode("Executing package transaction...", !cfg.Quiet && !jsonMode, cfg.Jobs <= 1)
		rebuildCfg := buildRebuildConfig(repoDir, cfg.Jobs, nil, nil)
		rebuildCfg.Fetcher = &fetch.Fetcher{}
		fetchProgress := newFetchProgress(!cfg.Quiet, os.Stdout)
		// Fetch and package events share one terminal owner. Even a serial
		// package job can have concurrent fetch workers, so disable the separate
		// carriage-return display and route complete lines through that owner.
		fetchProgress.setConcurrent(true)
		fetchProgress.line = executionProgress.message
		rebuildCfg.FetchProgress = fetchProgress.Report
		if filepath.Clean(rebuildCfg.RootDir) == string(filepath.Separator) {
			rebuildCfg.AllowLiveRoot = true
		}
		lockedStateValidation := func() error {
			lockedStateSHA256, err := mutationStateSHA256(*vdbDir, *worldFile, *portageConfigRoot, result.Install)
			if err != nil {
				return err
			}
			if lockedStateSHA256 != stateSHA256 {
				return fmt.Errorf("package state or policy changed before the operation lock; resolve and approve the new plan")
			}
			return nil
		}
		compatLog, logErr := openPortageMergeLog(*emergeLog)
		if logErr != nil {
			fmt.Fprintf(os.Stderr, "arise: open Portage-compatible merge log: %v\n", logErr)
			os.Exit(1)
		}
		execCtx, cancelExecution := context.WithCancel(commandContext)
		defer cancelExecution()
		executionEstimates := mergeEstimates(nil)
		if *showEstimates {
			executionEstimates = loadMergeEstimates(*emergeLog)
		}
		var completedActions atomic.Int64
		initialStatus := fmt.Sprintf(">>> Jobs: 0 of %d complete", len(result.Install))
		if load := currentLoadAverages(); load != "" {
			initialStatus += "    Load avg: " + load
		}
		executionProgress.setStatus(initialStatus)
		var prepareMutation func(context.Context) error
		var publishedRecoverySet string
		if filepath.Clean(rebuildCfg.RootDir) == string(filepath.Separator) {
			recoveryPackages := recoveryPackagesForActions(*vdbDir, result.Install)
			if len(recoveryPackages) > 0 {
				recoverySetID, idErr := recoveryset.NewID()
				if idErr != nil {
					fmt.Fprintf(os.Stderr, "arise: initialize recovery set: %v\n", idErr)
					os.Exit(1)
				}
				planSHA256 := canonicalPlanSHA256(targets, cfg, result, stateSHA256)
				prepareMutation = func(ctx context.Context) error {
					configurationFingerprint, err := binpkg.FingerprintConfiguration(*portageConfigRoot)
					if err != nil {
						return fmt.Errorf("fingerprint Portage configuration: %w", err)
					}
					var sourceInputs []binpkg.FingerprintInput
					for _, action := range result.Install {
						if action.Atom == nil || action.RepositoryPath == "" {
							continue
						}
						sourceInputs = append(sourceInputs,
							binpkg.FingerprintInput{
								Label: "package:" + action.Repository + ":" + action.Atom.CP(),
								Path:  filepath.Join(action.RepositoryPath, action.Atom.Category, action.Atom.Package),
							},
							binpkg.FingerprintInput{
								Label: "eclasses:" + action.Repository,
								Path:  filepath.Join(action.RepositoryPath, "eclass"),
							},
						)
					}
					repositoryFingerprint, err := binpkg.FingerprintSelectedSourceClosure(sourceInputs)
					if err != nil {
						return fmt.Errorf("fingerprint selected repository sources: %w", err)
					}
					path, err := recoveryset.Publish(ctx, recoveryset.Request{
						Directory: filepath.Join(*binpkgDir, ".arise-recovery"),
						SetID:     recoverySetID, OperationID: recoverySetID, PlanSHA256: planSHA256,
						RootDir: rebuildCfg.RootDir, Packages: recoveryPackages,
						ConfigurationFingerprint: configurationFingerprint,
						RepositoryFingerprint:    repositoryFingerprint,
					})
					if err != nil {
						return err
					}
					publishedRecoverySet = path
					executionProgress.message(fmt.Sprintf(">>> Published complete pre-update recovery set %s", path))
					return nil
				}
			}
		}
		executionErr := executor.Execute(execCtx, result, executor.Config{
			Rebuild: *rebuildCfg, ResumePath: *resumeFile, Jobs: cfg.Jobs, LoadAverage: cfg.LoadAverage, TmpdirRequireFreeGB: *jobsTmpdirRequireFreeGB, ValidateLocked: lockedStateValidation, PrepareMutation: prepareMutation,
			OnSpaceWait: func(path string, available, required uint64) {
				executionProgress.message(fmt.Sprintf(">>> %s has insufficient free space; package parallelism reduced (free: %s, required: %s)", path, formatSize(int64(available)), formatSize(int64(required))))
			},
			OnActionStart: func(index, total int, action resolve.PkgAction) {
				message := fmt.Sprintf("Building package (%d of %d) %s", index, total, colorActionAtom(action))
				if *showEstimates {
					if estimate, ok := executionEstimates.forAction(action); ok {
						message += " (estimated " + formatEstimate(estimate) + ")"
					}
				}
				if executionProgress.enabled {
					executionProgress.setLabel(message)
				} else {
					executionProgress.message(">>> " + message)
				}
				if compatLog.event(false, index, total, action) != nil {
					cancelExecution()
				}
			},
			OnActionInstall: func(index, total int, action resolve.PkgAction) {
				executionProgress.message(fmt.Sprintf(">>> Installing into image (%d of %d) %s", index, total, colorActionAtom(action)))
			},
			OnActionStage: func(index, total int, action resolve.PkgAction, stage string) {
				executionProgress.clearProgress()
				labels := map[string]string{
					"validate": "Validating package image",
					"merge":    "Preparing package merge",
					"sync":     "Syncing package contents",
					"commit":   "Committing package transaction",
					"finalize": "Finalizing package",
				}
				if label := labels[stage]; label != "" {
					executionProgress.message(fmt.Sprintf(">>> %s (%d of %d) %s", label, index, total, colorActionAtom(action)))
				}
			},
			OnActionProgress: func(index, total int, action resolve.PkgAction, stage string, current, stageTotal int) {
				if stage == "merge" && stageTotal > 0 {
					executionProgress.setProgress(formatInstallProgress(index, total, action, current, stageTotal), current, stageTotal)
				}
			},
			OnActionNotice: func(index, total int, action resolve.PkgAction, class, message string) {
				prefix := " *"
				if class != "" && class != "INFO" {
					prefix += " " + strings.ToLower(class) + ":"
				}
				for _, line := range strings.Split(message, "\n") {
					if strings.TrimSpace(line) != "" {
						executionProgress.message(prefix + " " + strings.TrimSpace(line))
					}
				}
			},
			OnActionComplete: func(index, total int, action resolve.PkgAction) {
				executionProgress.clearProgress()
				executionProgress.message(fmt.Sprintf(">>> Completed package (%d of %d) %s", index, total, colorActionAtom(action)))
				completed := completedActions.Add(1)
				status := fmt.Sprintf(">>> Jobs: %d of %d complete", completed, total)
				if load := currentLoadAverages(); load != "" {
					status += "    Load avg: " + load
				}
				executionProgress.setStatus(status)
				if compatLog.event(true, index, total, action) != nil {
					cancelExecution()
				}
			},
		})
		executionProgress.stop()
		if logErr := compatLog.close(); executionErr == nil && logErr != nil {
			executionErr = fmt.Errorf("Portage-compatible merge log: %w", logErr)
		}
		if publishedRecoverySet != "" {
			if statusErr := markRecoverySetOutcome(publishedRecoverySet, executionErr); statusErr != nil {
				executionProgress.message(" * warning: recovery set remains conservatively active: " + statusErr.Error())
			}
		}
		if executionErr != nil {
			if errors.Is(executionErr, context.Canceled) {
				printExecutionInterrupted(os.Stderr)
				os.Exit(130)
			}
			printExecutionError(os.Stderr, executionErr)
			os.Exit(1)
		}
		selections := installWorldSelections(targets, cfg, result)
		if err := updateInstallWorld(*worldFile, selections); err != nil {
			fmt.Fprintf(os.Stderr, "arise: packages committed but world selection failed: %v\n", err)
			os.Exit(1)
		}
		if !cfg.Quiet {
			printPostTransactionSummary(os.Stdout, rebuildCfg.RootDir, rebuildCfg.VdbDir, repoDir, rebuildCfg.PortageConfig)
		}
		if !cfg.Quiet {
			fmt.Println("All package transactions committed successfully.")
		}
		return
	}

}

func downloadBinpkgTargets(ctx context.Context, binhosts, targets []string, destination string, required, quiet bool) error {
	for _, target := range targets {
		parsed, parseErr := atom.Parse(target)
		if parseErr != nil || parsed.Category == "" || parsed.Package == "" {
			continue
		}
		var failures []error
		acquired := false
		for _, host := range binhosts {
			downloaded, err := binpkg.DownloadFromBinhost(ctx, host, []string{target}, destination)
			if err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", host, err))
				continue
			}
			if len(downloaded) != 0 {
				acquired = true
				if !quiet {
					fmt.Printf("Downloaded %d binary package from %s\n", len(downloaded), host)
				}
				break
			}
		}
		if required && !acquired {
			return fmt.Errorf("no configured binhost supplied %s: %w", target, errors.Join(failures...))
		}
	}
	return nil
}

func markRecoverySetOutcome(path string, operationErr error) error {
	if operationErr != nil {
		return recoveryset.MarkStatus(path, recoveryset.StatusFailed, operationErr.Error())
	}
	return recoveryset.MarkStatus(path, recoveryset.StatusPendingVerification, "")
}

func formatInstallProgress(index, total int, action resolve.PkgAction, current, stageTotal int) string {
	return fmt.Sprintf(
		">>> Installing package contents: %d/%d entries (%.0f%%) (%d of %d) %s",
		current,
		stageTotal,
		100*float64(current)/float64(stageTotal),
		index,
		total,
		colorActionAtom(action),
	)
}

func targetsNeedCompleteGraph(targets []string) bool {
	for _, target := range targets {
		if target == "@world" || target == "@system" {
			return true
		}
	}
	return false
}

func currentLoadAverages() string {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return ""
	}
	return strings.Join(fields[:3], ", ")
}

func portageActionHeader(action resolve.PkgAction, fetchOnly bool) string {
	if fetchOnly {
		return "[" + color.Green("fetch") + "]"
	}
	kind := "ebuild"
	if action.MergeType == "binary" {
		kind = "binary"
	}
	coloredKind := color.Green(kind)
	if kind == "binary" {
		coloredKind = color.Magenta(kind)
	}
	attributes := []string{" ", " ", " ", " ", " ", " ", " "}
	switch action.Action {
	case "install":
		attributes[1] = color.Green("N")
	case "reinstall":
		attributes[2] = color.Yellow("R")
	case "update":
		if action.InstalledVersion != "" && action.Atom != nil && action.Atom.Version != nil {
			installed, installedErr := atom.ParseVersion(action.InstalledVersion)
			if installedErr == nil && action.Atom.Version.Compare(installed) < 0 {
				attributes[5] = color.Blue("D")
			} else {
				attributes[4] = color.Cyan("U")
			}
		} else {
			attributes[4] = color.Cyan("U")
		}
	}
	return fmt.Sprintf("[%s %s]", coloredKind, strings.Join(attributes, ""))
}

func portagePreviousIdentity(action resolve.PkgAction) string {
	if action.InstalledVersion == "" {
		return ""
	}
	identity := action.InstalledVersion
	if action.InstalledSlot != "" {
		identity += ":" + action.InstalledSlot
		if action.InstalledSubslot != "" && action.InstalledSubslot != action.InstalledSlot {
			identity += "/" + action.InstalledSubslot
		}
	}
	if action.InstalledRepository != "" {
		identity += "::" + action.InstalledRepository
	}
	return identity
}

func portageUseDisplay(action resolve.PkgAction) string {
	type useBucket struct{ enabled, disabled, removed []string }
	currentIUse := make(map[string]bool)
	for _, raw := range strings.Fields(action.IUse) {
		flag := strings.TrimLeft(raw, "+-")
		if flag != "" {
			currentIUse[flag] = true
		}
	}
	all := make(map[string]bool, len(currentIUse)+len(action.InstalledIUseFlags))
	for flag := range currentIUse {
		all[flag] = true
	}
	for flag := range action.InstalledIUseFlags {
		all[flag] = true
	}
	flags := make([]string, 0, len(all))
	for flag := range all {
		flags = append(flags, flag)
	}
	sort.Strings(flags)
	hidden := make(map[string]bool, len(action.UseExpandHidden))
	for _, name := range action.UseExpandHidden {
		hidden[name] = true
	}
	buckets := map[string]*useBucket{"USE": {}}
	groupFlag := func(flag string) (string, string) {
		for _, name := range action.UseExpand {
			prefix := strings.ToLower(name) + "_"
			if strings.HasPrefix(flag, prefix) {
				return name, strings.TrimPrefix(flag, prefix)
			}
		}
		return "USE", flag
	}
	for _, flag := range flags {
		group, displayFlag := groupFlag(flag)
		if hidden[group] {
			continue
		}
		bucket := buckets[group]
		if bucket == nil {
			bucket = &useBucket{}
			buckets[group] = bucket
		}
		currentDomain := currentIUse[flag]
		oldDomain := action.InstalledIUseFlags[flag]
		current := action.UseFlags[flag]
		old := action.InstalledUseFlags[flag]
		forced := action.ForcedUseFlags[flag] || action.MaskedUseFlags[flag]
		if !currentDomain {
			marker := color.Yellow("-"+displayFlag) + "%"
			if old {
				marker += "*"
			}
			bucket.removed = append(bucket.removed, "("+marker+")")
			continue
		}
		var marker string
		if current {
			switch {
			case action.InstalledVersion == "" || oldDomain && old:
				marker = color.Red(displayFlag)
			case !oldDomain:
				marker = color.Yellow(displayFlag) + "%*"
			default:
				marker = color.Green(displayFlag) + "*"
			}
		} else {
			switch {
			case action.InstalledVersion == "" || oldDomain && !old:
				marker = color.Blue("-" + displayFlag)
			case !oldDomain:
				marker = color.Yellow("-" + displayFlag)
				if !forced {
					marker += "%"
				}
			default:
				marker = color.Green("-"+displayFlag) + "*"
			}
		}
		if forced {
			marker = "(" + marker + ")"
		}
		if current {
			bucket.enabled = append(bucket.enabled, marker)
		} else {
			bucket.disabled = append(bucket.disabled, marker)
		}
	}
	groups := make([]string, 0, len(buckets))
	for name := range buckets {
		if name != "USE" {
			groups = append(groups, name)
		}
	}
	sort.Strings(groups)
	groups = append([]string{"USE"}, groups...)
	var displays []string
	for _, name := range groups {
		bucket := buckets[name]
		values := append(append(append([]string(nil), bucket.enabled...), bucket.disabled...), bucket.removed...)
		if len(values) != 0 {
			displays = append(displays, name+`="`+strings.Join(values, " ")+`"`)
		}
	}
	return strings.Join(displays, " ")
}

func printExecutionInterrupted(w io.Writer) {
	fmt.Fprintln(w, "arise: interrupted by user")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  Completed package progress has been preserved.")
	fmt.Fprintln(w, "  Rerun preflight to calculate a continuation plan.")
	fmt.Fprintln(w)
}

func warningsForDisplay(warnings []string, verbose bool) []string {
	visible := make([]string, 0, len(warnings))
	seen := make(map[string]bool, len(warnings))
	for _, warning := range warnings {
		if !verbose && strings.HasPrefix(warning, "circular dependency: ") {
			continue
		}
		if seen[warning] {
			continue
		}
		seen[warning] = true
		visible = append(visible, warning)
	}
	return visible
}

func printExecutionError(w io.Writer, err error) {
	fmt.Fprintln(w, "arise: package transaction failed")
	fmt.Fprintln(w)
	const maxDetails = 10
	details := make([]string, 0, maxDetails)
	durableLog := ""
	for err != nil {
		message := err.Error()
		cause := errors.Unwrap(err)
		if cause != nil {
			if at := strings.Index(message, cause.Error()); at >= 0 {
				message = message[:at] + message[at+len(cause.Error()):]
			}
		}
		if start := strings.LastIndex(message, "(durable log: "); start >= 0 {
			if end := strings.Index(message[start:], ")"); end >= 0 {
				durableLog = strings.TrimSpace(message[start+len("(durable log:") : start+end])
				message = message[:start] + message[start+end+1:]
			}
		}
		message = strings.Trim(message, ":; \t\r\n")
		lines := strings.Split(message, "\n")
		selected := executionDiagnosticLines(lines)
		for _, rawLine := range selected {
			line := strings.TrimSpace(rawLine)
			if line == "" || len(details) >= maxDetails {
				continue
			}
			if len(line) > 160 {
				line = line[:157] + "..."
			}
			details = append(details, line)
		}
		err = cause
	}
	for _, detail := range details {
		fmt.Fprintf(w, "  %s\n", detail)
	}
	if durableLog != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  Log file:")
		fmt.Fprintf(w, "    '%s'\n", durableLog)
	}
	fmt.Fprintln(w)
}

func executionDiagnosticLines(lines []string) []string {
	if len(lines) <= 4 {
		return lines
	}
	signals := []string{
		"error:", " error ", "failed", "failure", "cannot ", "can't ",
		"permission denied", "undefined reference", "no rule to make target",
		"not found", "no such file", "fatal:", "segmentation fault",
	}
	selected := make(map[int]bool)
	for index, line := range lines {
		lower := " " + strings.ToLower(line) + " "
		for _, signal := range signals {
			if !strings.Contains(lower, signal) {
				continue
			}
			for contextIndex := max(0, index-1); contextIndex <= min(len(lines)-1, index+1); contextIndex++ {
				selected[contextIndex] = true
			}
			break
		}
	}
	if len(selected) == 0 {
		return append([]string{lines[0]}, lines[len(lines)-3:]...)
	}
	result := make([]string, 0, min(6, len(selected)))
	for index, line := range lines {
		if selected[index] {
			result = append(result, line)
			if len(result) == 6 {
				break
			}
		}
	}
	return result
}

func executionActionLabel(action resolve.PkgAction) string {
	if action.Atom == nil {
		return "<nil>"
	}
	label := action.Atom.CP()
	if action.Atom.Version != nil && action.Atom.Version.Raw != "" {
		label += "-" + action.Atom.Version.Raw
	}
	if action.Repository != "" {
		label += "::" + action.Repository
	}
	return label
}

func planHeading(result *resolve.ResolveResult, fetchOnly bool) string {
	if fetchOnly {
		word := "packages"
		if len(result.Install) == 1 {
			word = "package"
		}
		return fmt.Sprintf("Fetch plan (%d %s, %d conflicts, backtrack %d):",
			len(result.Install), word, len(result.Conflicts), result.BacktrackLevel)
	}
	return fmt.Sprintf("Proposed actions (%d install, %d uninstall, %d conflicts, backtrack %d):",
		len(result.Install), len(result.Uninstall), len(result.Conflicts), result.BacktrackLevel)
}

func displayedActionLabel(action string, fetchOnly bool) string {
	if fetchOnly {
		return "[fetch]"
	}
	return actionLabel(action)
}

func planExecutionVerificationError(result *resolve.ResolveResult) error {
	if result == nil {
		return fmt.Errorf("arise: refusing execution: no resolved plan")
	}
	if !result.Verified {
		status := result.Verification
		if status == "" {
			status = resolve.VerificationIncomplete
		}
		return fmt.Errorf("arise: refusing execution: plan did not pass whole-state verification (%s)", status)
	}
	return nil
}

func fetchPlanActions(ctx context.Context, actions []resolve.PkgAction, baseConfig fetch.FetchConfig, fetcher *fetch.Fetcher, jobs int) error {
	if fetcher == nil {
		return fmt.Errorf("fetch-only: nil fetcher")
	}
	jobs = normalizedFetchJobs(jobs, len(actions))
	errs := make([]error, len(actions))
	indices := make(chan int)
	var workers sync.WaitGroup
	for worker := 0; worker < jobs; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range indices {
				errs[index] = fetchPlanAction(ctx, actions[index], baseConfig, fetcher)
			}
		}()
	}
	for index := range actions {
		select {
		case <-ctx.Done():
			close(indices)
			workers.Wait()
			return ctx.Err()
		case indices <- index:
		}
	}
	close(indices)
	workers.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func normalizedFetchJobs(jobs, actions int) int {
	if jobs < 1 {
		jobs = 1
	}
	if actions > 0 && jobs > actions {
		return actions
	}
	return jobs
}

func fetchPlanAction(ctx context.Context, action resolve.PkgAction, baseConfig fetch.FetchConfig, fetcher *fetch.Fetcher) error {
	if action.Atom == nil || action.RepositoryPath == "" {
		return fmt.Errorf("fetch-only: source action lacks package or repository identity")
	}
	if action.MergeType == "binary" {
		return fmt.Errorf("fetch-only: binary acquisition for %s is not implemented", action.Atom)
	}
	if strings.TrimSpace(action.SrcURI) == "" {
		return nil
	}
	manifest := filepath.Join(action.RepositoryPath, action.Atom.Category, action.Atom.Package, "Manifest")
	mirrorGroups, err := fetch.LoadMirrorGroups(filepath.Join(action.RepositoryPath, "profiles", "thirdpartymirrors"))
	if err != nil {
		return fmt.Errorf("%s: %w", action.Atom, err)
	}
	config := baseConfig
	config.MirrorGroups = mirrorGroups
	restrict, err := phaseproto.EvaluatePolicyExpression(action.Restrict, action.UseFlags)
	if err != nil {
		return fmt.Errorf("%s: evaluate RESTRICT for fetch: %w", action.Atom, err)
	}
	for _, name := range restrict {
		switch name {
		case "mirror":
			config.RestrictMirrors = true
		case "primaryuri":
			config.PrimaryURI = true
		}
	}
	if _, err := fetcher.AcquireManifest(ctx, manifest, action.SrcURI, action.UseFlags, config); err != nil {
		return fmt.Errorf("%s: %w", action.Atom, err)
	}
	return nil
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

func planActionDownloadSizes(actions []resolve.PkgAction, distdir string, verbose bool) map[string]int64 {
	sizes := make(map[string]int64, len(actions))
	for _, action := range actions {
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
		sizes[resolve.ActionIdentity(action)] = size
	}
	return sizes
}

func printActionTotals(actions []resolve.PkgAction, downloadSizes map[string]int64, fetchOnly bool) {
	counts := make(map[string]int)
	var downloadBytes int64
	for _, action := range actions {
		counts[action.Action]++
		downloadBytes += downloadSizes[resolve.ActionIdentity(action)]
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
	if fetchOnly {
		fmt.Print(" to fetch")
	} else if len(details) > 0 {
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
