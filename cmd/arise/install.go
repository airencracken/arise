package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/features"
	"github.com/airencracken/arise/internal/graph"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/rebuild"
	"github.com/airencracken/arise/internal/resolve"
)

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

func buildRebuildConfig(repoDir string, jobs int, phaseStart func(string), phaseEnd func(string, error)) *rebuild.RebuildConfig {
	portageCfg, _ := portage.LoadConfig(*portageConfigRoot)
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
	if *pretend {
		fmt.Println("(pretend mode: no actions will be performed)")
	}

	// Parse PORTAGE_BINHOST from portage config
	portageCfg, _ := portage.LoadConfig(*portageConfigRoot)
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
				resolve.AutoUnmask(result.Conflicts, *portageConfigRoot)
				resolve.AutoAcceptLicense(result.Conflicts, *portageConfigRoot)
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

	// Save resume state for --resume support
	if err := resolve.SaveResume(*resumeFile, result); err != nil && !*quiet {
		fmt.Fprintf(os.Stderr, "resume: save: %v\n", err)
	}

	if cfg.Pretend || cfg.FetchOnly {
		return
	}
}
