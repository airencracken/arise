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
	"time"

	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/distfiles"
	"github.com/airencracken/arise/internal/executor"
	"github.com/airencracken/arise/internal/features"
	"github.com/airencracken/arise/internal/fetch"
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
	runResolveAndRebuild(args, dbPath, repoDir, *updateMode, false)
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
	resolveCtx := context.Background()
	cancelResolve := func() {}
	if *resolverTimeout > 0 {
		resolveCtx, cancelResolve = context.WithTimeout(resolveCtx, *resolverTimeout)
	}
	defer cancelResolve()
	result, err := resolve.ResolveContext(resolveCtx, rg, targets, cfg)
	solverDuration := time.Since(stageStarted)
	progress.stop()
	if result == nil {
		result = &resolve.ResolveResult{}
	}
	resolutionDuration := time.Since(resolutionStarted)
	stateSHA256 := ""
	stateFingerprintStarted := time.Now()
	if result.Verified && (jsonMode || *savePlan != "" || *experimentalLiveMutation || *approvePlanSHA256 != "" || *approvePlan != "") {
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

		if cfg.Tree && !cfg.FetchOnly {
			fmt.Print(resolve.FormatTree(result.Install, rg))
		} else {
			for _, a := range result.Install {
				label := displayedActionLabel(a.Action, cfg.FetchOnly)
				fmt.Printf("  %s %s", colorIcon(a.Action, label), colorActionAtom(a))
				if estimate, ok := estimates.forAction(a); ok {
					fmt.Printf("  (estimated %s)", formatEstimate(estimate))
				}
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
		printActionTotals(result.Install, *distfilesDir, cfg.Verbose, cfg.FetchOnly)
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
		fmt.Printf("Preflight passed for all %d install actions; no package state was mutated.\n", len(result.Install))
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
	if *experimentalLiveMutation || *approvePlanSHA256 != "" || *approvePlan != "" {
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
	actualPlanSHA256 := canonicalPlanSHA256(targets, cfg, result, stateSHA256)
	approvedDigest, approvalErr := approvedPlanDigest(*approvePlanSHA256, *approvePlan, *planDir)
	if approvalErr != nil {
		fmt.Fprintf(os.Stderr, "arise: refusing execution: %v\n", approvalErr)
		os.Exit(1)
	}
	if err := validatePlanAuthorization(*experimentalLiveMutation, approvedDigest, actualPlanSHA256); err != nil {
		if detail := describeApprovedPlanDifference(*approvePlan, *planDir, cfg); detail != "" {
			err = fmt.Errorf("%w; %s", err, detail)
		}
		fmt.Fprintf(os.Stderr, "arise: refusing execution: %v\n", err)
		os.Exit(1)
	}
	if cfg.FetchOnly {
		progress := newFetchProgress(!cfg.Quiet, os.Stdout)
		progress.setConcurrent(normalizedFetchJobs(*fetchJobs, len(result.Install)) > 1)
		if err := fetchPlanActions(context.Background(), result.Install, fetch.FetchConfig{DistfilesDir: *distfilesDir, GentooMirrors: strings.Fields(portageCfg.MakeConf["GENTOO_MIRRORS"]), Progress: progress.Report}, &fetch.Fetcher{}, *fetchJobs); err != nil {
			fmt.Fprintf(os.Stderr, "arise: fetch-only failed: %v\n", err)
			os.Exit(1)
		}
		if !cfg.Quiet {
			fmt.Println("All source artifacts are present and Manifest-verified.")
		}
		return
	}
	if *experimentalLiveMutation {
		if !cfg.Oneshot {
			fmt.Fprintln(os.Stderr, "arise: refusing execution: disposable executor currently requires --oneshot until world addition joins the package journal")
			os.Exit(1)
		}
		// Package transactions remain dependency-ordered by the executor, while
		// the package's build system follows explicit --jobs or configured
		// MAKEOPTS. Serializing every compiler invocation made large canaries look
		// stalled and diverged from the approved Portage configuration.
		rebuildCfg := buildRebuildConfig(repoDir, cfg.Jobs, nil, nil)
		rebuildCfg.Fetcher = &fetch.Fetcher{}
		fetchProgress := newFetchProgress(!cfg.Quiet, os.Stdout)
		fetchProgress.setConcurrent(normalizedFetchJobs(*fetchJobs, len(result.Install)) > 1)
		if !cfg.Quiet {
			fmt.Printf("Fetching source artifacts with %d concurrent job(s)...\n", normalizedFetchJobs(*fetchJobs, len(result.Install)))
		}
		if err := fetchPlanActions(context.Background(), result.Install, fetch.FetchConfig{DistfilesDir: *distfilesDir, GentooMirrors: strings.Fields(portageCfg.MakeConf["GENTOO_MIRRORS"]), Progress: fetchProgress.Report}, rebuildCfg.Fetcher, *fetchJobs); err != nil {
			fmt.Fprintf(os.Stderr, "arise: fetch-ahead failed before package mutation: %v\n", err)
			os.Exit(1)
		}
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
		execCtx, cancelExecution := context.WithCancel(context.Background())
		defer cancelExecution()
		executionEstimates := mergeEstimates(nil)
		if *showEstimates {
			executionEstimates = loadMergeEstimates(*emergeLog)
		}
		executionProgress := startTerminalProgressMode("Executing package transaction...", !cfg.Quiet && !jsonMode, cfg.Jobs <= 1)
		executionErr := executor.Execute(execCtx, result, executor.Config{
			Rebuild: *rebuildCfg, ResumePath: *resumeFile, Jobs: cfg.Jobs, LoadAverage: cfg.LoadAverage, ValidateLocked: lockedStateValidation,
			OnActionStart: func(index, total int, action resolve.PkgAction) {
				message := fmt.Sprintf("Emerging (%d of %d) %s", index, total, executionActionLabel(action))
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
			OnActionComplete: func(index, total int, action resolve.PkgAction) {
				executionProgress.message(fmt.Sprintf(">>> Completed emerge (%d of %d) %s", index, total, executionActionLabel(action)))
				if compatLog.event(true, index, total, action) != nil {
					cancelExecution()
				}
			},
		})
		executionProgress.stop()
		if logErr := compatLog.close(); executionErr == nil && logErr != nil {
			executionErr = fmt.Errorf("Portage-compatible merge log: %w", logErr)
		}
		if executionErr != nil {
			printExecutionError(os.Stderr, executionErr)
			os.Exit(1)
		}
		if !cfg.Quiet {
			fmt.Println("All package transactions committed successfully.")
		}
		return
	}

	// Resolution is production-capable, but package execution is not yet wired
	// to the journaled P4/P6 transaction path. Never report success for a plan
	// that was not executed, including fetch-only requests.
	fmt.Fprintln(os.Stderr, unsupportedExecutionMessage(cfg))
	os.Exit(1)
}

func warningsForDisplay(warnings []string, verbose bool) []string {
	if verbose {
		return warnings
	}
	visible := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		if strings.HasPrefix(warning, "circular dependency: ") {
			continue
		}
		visible = append(visible, warning)
	}
	return visible
}

func printExecutionError(w io.Writer, err error) {
	fmt.Fprintln(w, "arise: package transaction failed")
	fmt.Fprintln(w)
	const maxDetails = 6
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
		if line := strings.TrimSpace(strings.SplitN(message, "\n", 2)[0]); line != "" && len(details) < maxDetails {
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
	if _, err := fetcher.AcquireManifest(ctx, manifest, action.SrcURI, action.UseFlags, config); err != nil {
		return fmt.Errorf("%s: %w", action.Atom, err)
	}
	return nil
}

func unsupportedExecutionMessage(cfg resolve.ResolveConfig) string {
	return "arise: install/update execution is experimental and unavailable; rerun with --pretend (live mutation remains gated on the P4/P6 transaction engine)"
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

func printActionTotals(actions []resolve.PkgAction, distdir string, verbose, fetchOnly bool) {
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
