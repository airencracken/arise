package rebuild

import (
	stdbzip2 "compress/bzip2"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/depstring"
	"github.com/airencracken/arise/internal/distfiles"
	"github.com/airencracken/arise/internal/ebuild"
	envupdate "github.com/airencracken/arise/internal/env"
	"github.com/airencracken/arise/internal/features"
	"github.com/airencracken/arise/internal/fetch"
	"github.com/airencracken/arise/internal/installedquery"
	"github.com/airencracken/arise/internal/merge"
	"github.com/airencracken/arise/internal/phase"
	"github.com/airencracken/arise/internal/phaseproto"
	"github.com/airencracken/arise/internal/portage"
	shlex "github.com/anmitsu/go-shlex"
)

// RebuildConfig holds the configuration for rebuilding packages.
type RebuildConfig struct {
	RepoDir                       string
	DistfilesDir                  string
	SourceURI                     string // resolved metadata, including eclass-derived SRC_URI
	BinaryPackagePath             string
	PackageDir                    string
	BinaryPackageRequireSignature bool
	BinaryPackageTrustedKeyring   string
	BuildPackage                  bool
	BuildOnly                     bool
	RootDir                       string
	// SysrootDir supplies target build dependencies. It defaults to RootDir.
	// BrootDir supplies build-host dependencies. It defaults to RootDir.
	// Keeping these distinct is required for cross-root/disposable image builds.
	SysrootDir    string
	BrootDir      string
	VdbDir        string
	WorkDirBase   string
	CFLAGS        string
	CXXFLAGS      string
	LDFLAGS       string
	MAKEOPTS      string
	Arch          string
	Features      *features.Config
	UseFlags      map[string]bool
	Fetcher       *fetch.Fetcher
	FetchProgress func(fetch.Progress)
	GentooMirrors []string
	// PhaseProtocol selects the versioned, eclass-aware Bash execution ABI.
	// Host installation remains gated by the caller's transaction boundary.
	PhaseProtocol        bool
	Repositories         []portage.RepoEntry
	Repository           string
	SelectedSlot         string // exact resolver-selected slot/subslot metadata
	SelectedIUSE         string // exact resolver-selected repository IUSE metadata
	PortageConfig        *portage.Config
	ConfigRoot           string
	PhaseLogDir          string
	JournalDir           string
	AllowLiveRoot        bool // set only by the state-bound production mutation gate
	AllowLiveReplacement bool // exact same-version canary replacement
	AllowLiveUpgrade     bool // one exact old-version replacement canary
	VDBLockHeld          bool // serial executor owns the operation-wide VDB lock
	CommitLock           sync.Locker
	CallbackLock         sync.Locker
	// PostCommitContext drives lifecycle hooks after the payload/VDB transaction
	// is durable. Parallel executors set this to their outer context so a sibling
	// build failure cannot interrupt pkg_postrm/pkg_postinst mid-transaction.
	PostCommitContext context.Context
	// OnTransactionCommit runs while CommitLock is still held after the package
	// journal commits. A non-nil argument is a committed-state lifecycle error.
	OnTransactionCommit func(error) error
	SplitLogs           bool
	CompressLogs        bool
	LogFilterCommand    string
	ElogClasses         []string
	ElogSinks           []string
	ElogOutput          io.Writer
	HasVersion          map[string]bool

	OnPhaseStart func(phase string)
	OnPhaseEnd   func(phase string, err error)
	OnStage      func(stage string)
	OnProgress   func(stage string, current, total int)
	OnNotice     func(class, message string)
	OnError      func(pkg string, err error)
}

func ensureWorkDirectory(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("rebuild: work directory must be an absolute path")
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("rebuild: create work directory %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("rebuild: inspect work directory %s: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("rebuild: work path is not a directory: %s", path)
	}
	return nil
}

func (c *RebuildConfig) fetcher() *fetch.Fetcher {
	if c.Fetcher == nil {
		c.Fetcher = &fetch.Fetcher{}
	}
	return c.Fetcher
}

func (c *RebuildConfig) dependencyRoots() (string, string) {
	sysroot, broot := c.SysrootDir, c.BrootDir
	if sysroot == "" {
		sysroot = c.RootDir
	}
	if broot == "" {
		broot = c.RootDir
	}
	return sysroot, broot
}

// phaseBroot converts the filesystem location used to resolve BROOT
// dependencies into Portage's ebuild-visible prefix value.  For a native
// build, Portage exports an empty BROOT (not "/"); exposing "/" makes eclasses
// construct paths such as //usr/bin/python, which CMake treats as network
// paths when DESTDIR is active.
func phaseBroot(broot string) string {
	if filepath.Clean(broot) == "/" {
		return ""
	}
	return broot
}

func runPhaseWorker(ctx context.Context, request phaseproto.Request, cfg *RebuildConfig, options phaseproto.WorkerOptions) ([]phaseproto.Event, error) {
	helper, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("phase runtime query helper: %w", err)
	}
	helper, err = filepath.Abs(helper)
	if err != nil {
		return nil, fmt.Errorf("phase runtime query helper path: %w", err)
	}
	rootVDB := cfg.VdbDir
	_, broot := cfg.dependencyRoots()
	brootVDB := rootVDB
	if filepath.Clean(broot) != filepath.Clean(cfg.RootDir) {
		brootVDB = filepath.Join(broot, "var", "db", "pkg")
	}
	request.QueryHelper = helper
	request.QueryRootVDB = rootVDB
	request.QueryBrootVDB = brootVDB
	return phaseproto.RunBashWorkerWithOptions(ctx, request, options)
}

func (c *RebuildConfig) firePhaseStart(phase string) {
	if c.CallbackLock != nil {
		c.CallbackLock.Lock()
		defer c.CallbackLock.Unlock()
	}
	if c.OnPhaseStart != nil {
		c.OnPhaseStart(phase)
	}
}

func (c *RebuildConfig) fireProgress(stage string, current, total int) {
	if c.CallbackLock != nil {
		c.CallbackLock.Lock()
		defer c.CallbackLock.Unlock()
	}
	if c.OnProgress != nil {
		c.OnProgress(stage, current, total)
	}
}

func (c *RebuildConfig) fireNotice(class, message string) {
	if c.CallbackLock != nil {
		c.CallbackLock.Lock()
		defer c.CallbackLock.Unlock()
	}
	if c.OnNotice != nil {
		c.OnNotice(class, message)
	}
}

func (c *RebuildConfig) firePhaseEnd(phase string, err error) {
	if c.CallbackLock != nil {
		c.CallbackLock.Lock()
		defer c.CallbackLock.Unlock()
	}
	if c.OnPhaseEnd != nil {
		c.OnPhaseEnd(phase, err)
	}
}

func (c *RebuildConfig) fireStage(stage string) {
	if c.CallbackLock != nil {
		c.CallbackLock.Lock()
		defer c.CallbackLock.Unlock()
	}
	if c.OnStage != nil {
		c.OnStage(stage)
	}
}

func (c *RebuildConfig) fireError(pkg string, err error) {
	if c.CallbackLock != nil {
		c.CallbackLock.Lock()
		defer c.CallbackLock.Unlock()
	}
	if c.OnError != nil {
		c.OnError(pkg, err)
	}
}

// RebuildPackage rebuilds a single package from its atom string.
func RebuildPackage(ctx context.Context, atomStr string, cfg *RebuildConfig) (err error) {
	if cfg == nil {
		return fmt.Errorf("rebuild: configuration is required")
	}
	root, rootErr := filepath.Abs(cfg.RootDir)
	if rootErr != nil {
		return fmt.Errorf("rebuild: resolve ROOT: %w", rootErr)
	}
	if filepath.Clean(root) == string(filepath.Separator) && !cfg.AllowLiveRoot {
		return fmt.Errorf("rebuild: refusing live ROOT without state-bound mutation authorization")
	}
	a, err := atom.Parse(atomStr)
	if err != nil {
		return fmt.Errorf("rebuild: invalid package name %q: %w", atomStr, err)
	}

	if a.Version == nil || a.Version.Raw == "" {
		return fmt.Errorf("rebuild: package %q is missing a version number", atomStr)
	}

	ver := a.Version.Raw
	cat := a.Category
	pkg := a.Package

	ebuildFile, err := findEbuild(cfg.RepoDir, cat, pkg, ver)
	if err != nil {
		return fmt.Errorf("rebuild: could not find build recipe for %s/%s-%s: %w", cat, pkg, ver, err)
	}

	eb, err := ebuild.ParseEbuild(ebuildFile)
	if err != nil {
		return fmt.Errorf("rebuild: could not read build recipe %s: %w", ebuildFile, err)
	}

	vars := eb.Vars()
	srcURI := strings.TrimSpace(cfg.SourceURI)
	if srcURI == "" {
		srcURI = strings.Trim(strings.TrimSpace(vars["SRC_URI"]), "\"'")
	}
	var verified distfiles.VerifiedSet

	if err := ensureWorkDirectory(cfg.WorkDirBase); err != nil {
		return err
	}
	workDir, err := os.MkdirTemp(cfg.WorkDirBase, cat+"-"+pkg+"-"+ver+"-*")
	if err != nil {
		return fmt.Errorf("rebuild: could not create temporary build directory: %w", err)
	}
	defer func() {
		failClean := cfg.Features != nil && cfg.Features.IsEnabled(features.FeatFailClean)
		if err == nil || failClean {
			os.RemoveAll(workDir)
		}
	}()

	destDir, err := os.MkdirTemp(cfg.WorkDirBase, cat+"-"+pkg+"-"+ver+"-dest-*")
	if err != nil {
		return fmt.Errorf("rebuild: could not create temporary install directory: %w", err)
	}
	defer os.RemoveAll(destDir)

	if srcURI != "" {
		mirrorGroups, mirrorErr := fetch.LoadMirrorGroups(filepath.Join(cfg.RepoDir, "profiles", "thirdpartymirrors"))
		if mirrorErr != nil {
			return fmt.Errorf("rebuild: load mirror policy: %w", mirrorErr)
		}
		fetchCfg := fetch.FetchConfig{DistfilesDir: cfg.DistfilesDir, GentooMirrors: cfg.GentooMirrors, MirrorGroups: mirrorGroups, Progress: cfg.FetchProgress}
		fetchUse := useFlagsWithArch(cfg.UseFlags, cfg.Arch)
		restrict, policyErr := phaseproto.EvaluatePolicyExpression(cleanEbuildValue(eb.Vars()["RESTRICT"]), fetchUse)
		if policyErr != nil {
			return fmt.Errorf("rebuild: evaluate RESTRICT for fetch: %w", policyErr)
		}
		manualOnly := false
		for _, name := range restrict {
			switch name {
			case "mirror":
				fetchCfg.RestrictMirrors = true
			case "primaryuri":
				fetchCfg.PrimaryURI = true
			case "fetch":
				manualOnly = true
			}
		}
		fetchCfg.ManualOnly = manualOnly
		var err error
		verified, err = cfg.fetcher().AcquireManifest(ctx, filepath.Join(filepath.Dir(ebuildFile), "Manifest"), srcURI, fetchUse, fetchCfg)
		if err != nil {
			var manual *fetch.ManualFetchRequiredError
			_, customNofetch := eb.RawPhases["pkg_nofetch"]
			if cfg.PhaseProtocol && (errors.As(err, &manual) || customNofetch) {
				if nofetchErr := runPackageNofetch(ctx, atomStr, eb, ebuildFile, workDir, destDir, cfg); nofetchErr != nil {
					err = fmt.Errorf("%w; pkg_nofetch: %v", err, nofetchErr)
				}
			}
			cfg.fireError(atomStr, fmt.Errorf("rebuild: failed to fetch source files: %w", err))
			return fmt.Errorf("rebuild: could not acquire verified source files: %w", err)
		}
	}

	if cfg.PhaseProtocol {
		if err := rebuildWithPhaseProtocol(ctx, atomStr, eb, ebuildFile, workDir, destDir, verified, cfg); err != nil {
			return err
		}
		return nil
	}

	phaseCfg := phase.PhaseConfig{
		DESTDIR:     destDir,
		WorkDir:     workDir,
		Sourcedir:   workDir,
		DistDir:     cfg.DistfilesDir,
		Distfiles:   artifactNames(verified.Artifacts),
		CFLAGS:      cfg.CFLAGS,
		CXXFLAGS:    cfg.CXXFLAGS,
		LDFLAGS:     cfg.LDFLAGS,
		MAKEOPTS:    cfg.MAKEOPTS,
		PN:          pkg,
		PV:          ver,
		CATEGORY:    cat,
		EBUILD_PATH: ebuildFile,
		Features:    cfg.Features,
	}

	runner, err := phase.NewRunner(phaseCfg)
	if err != nil {
		cfg.fireError(atomStr, fmt.Errorf("rebuild: failed to initialize build environment: %w", err))
		return fmt.Errorf("rebuild: could not set up build environment: %w", err)
	}

	// Filter to relevant build phases
	var buildPhases []string
	for _, ph := range eb.RawPhaseOrder {
		switch ph {
		case "src_unpack", "src_prepare", "src_configure", "src_compile", "src_install":
			buildPhases = append(buildPhases, ph)
		}
	}

	for _, ph := range buildPhases {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		cfg.firePhaseStart(ph)

		err := runner.Run(ctx, ph)
		cfg.firePhaseEnd(ph, err)

		if err != nil {
			cfg.fireError(atomStr, fmt.Errorf("rebuild: build phase %s failed: %w", ph, err))
			return fmt.Errorf("rebuild: build step %s failed: %w", ph, err)
		}
	}

	mergeCfg := merge.MergeConfig{
		RootDir:  cfg.RootDir,
		VdbDir:   cfg.VdbDir,
		Category: cat,
		Package:  pkg,
		Version:  ver,
	}

	if err := merge.Merge(ctx, destDir, mergeCfg); err != nil {
		cfg.fireError(atomStr, fmt.Errorf("rebuild: failed to install built files to system: %w", err))
		return fmt.Errorf("rebuild: could not install files to system: %w", err)
	}

	if cfg.Features != nil && cfg.Features.IsEnabled(features.FeatBuildPkg) {
		pkgDir := os.Getenv("PKGDIR")
		if pkgDir == "" {
			pkgDir = "/var/cache/binpkgs"
		}
		if _, bpkgErr := exec.LookPath("bzip2"); bpkgErr == nil {
			_, _ = binpkg.Create(ctx, mergeCfg.VdbPath(), cfg.RootDir, pkgDir)
		}
	}

	return nil
}

// PreflightPackage validates the immutable recipe, execution policy and
// controlled directory contract without starting a worker or mutating ROOT.
func PreflightPackage(atomStr string, cfg *RebuildConfig) error {
	if cfg == nil {
		return fmt.Errorf("rebuild: configuration is required")
	}
	a, err := atom.Parse(atomStr)
	if err != nil || a.Version == nil || a.Version.Raw == "" {
		return fmt.Errorf("rebuild: preflight requires an exact package version: %w", err)
	}
	for label, path := range map[string]string{
		"repository": cfg.RepoDir, "ROOT": cfg.RootDir, "VDB": cfg.VdbDir,
		"work": cfg.WorkDirBase, "log": cfg.PhaseLogDir, "journal": cfg.JournalDir,
	} {
		if path == "" || !filepath.IsAbs(path) {
			return fmt.Errorf("rebuild: preflight %s path must be absolute", label)
		}
	}
	if err := ensureWorkDirectory(cfg.WorkDirBase); err != nil {
		return err
	}
	preflightDir, err := os.MkdirTemp(cfg.WorkDirBase, "preflight-")
	if err != nil {
		return fmt.Errorf("rebuild: create preflight workspace: %w", err)
	}
	defer os.RemoveAll(preflightDir)
	preflightWork := filepath.Join(preflightDir, "work")
	preflightSource := filepath.Join(preflightDir, "source")
	preflightImage := filepath.Join(preflightDir, "image")
	preflightTemp := filepath.Join(preflightDir, "temp")
	preflightHome := filepath.Join(preflightDir, "home")
	for _, directory := range []string{preflightWork, preflightImage, preflightTemp, preflightHome} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("rebuild: create preflight directory: %w", err)
		}
	}
	ebuildFile, err := findEbuild(cfg.RepoDir, a.Category, a.Package, a.Version.Raw)
	if err != nil {
		return fmt.Errorf("rebuild: preflight ebuild: %w", err)
	}
	eb, err := ebuild.ParseEbuild(ebuildFile)
	if err != nil {
		return fmt.Errorf("rebuild: preflight parse ebuild: %w", err)
	}
	if _, err := phaseproto.DefaultPhases(eb.EAPI); err != nil {
		return err
	}
	if err := phaseproto.ValidateElogSinks(cfg.ElogSinks); err != nil {
		return err
	}
	repository := cfg.Repository
	if repository == "" {
		return fmt.Errorf("rebuild: preflight repository name is required")
	}
	repositories := append([]portage.RepoEntry(nil), cfg.Repositories...)
	if len(repositories) == 0 {
		repositories = []portage.RepoEntry{{Name: repository, Location: cfg.RepoDir}}
	}
	p := a.Package + "-" + a.Version.Raw
	request := phaseproto.Request{Protocol: phaseproto.Version, ID: "preflight", Command: "run_phase", Phase: "pkg_setup", EAPI: eb.EAPI, Ebuild: ebuildFile, Env: map[string]string{"USE": enabledUseWithArch(cfg.UseFlags, cfg.Arch)}}
	request.InstallQAChecks, err = installQAChecks(cfg.RepoDir)
	if err != nil {
		return fmt.Errorf("rebuild: preflight install QA checks: %w", err)
	}
	_, preflightBroot := cfg.dependencyRoots()
	request.HasVersion, err = preflightHasVersionQueries(ebuildFile, eb, repositories, repository, cfg.VdbDir, filepath.Join(preflightBroot, "var", "db", "pkg"), cfg.HasVersion, request.InstallQAChecks...)
	if err != nil {
		return fmt.Errorf("rebuild: preflight has_version queries: %w", err)
	}
	request.BestVersion = preflightBestVersions(cfg.VdbDir, filepath.Join(preflightBroot, "var", "db", "pkg"))
	sysroot, broot := cfg.dependencyRoots()
	request, err = phaseproto.ApplyPackagePolicy(request, phaseproto.PackagePolicy{
		Configuration: cfg.PortageConfig, Repositories: repositories, Repository: repository,
		ConfigRoot: cfg.ConfigRoot, CPV: a.Category + "/" + p, Category: a.Category, PN: a.Package, P: p, PR: "r0", Slot: selectedEbuildSlot(cfg, eb),
		WorkDir: preflightWork, SourceDir: preflightSource, ImageDir: preflightImage,
		RootDir: cfg.RootDir, SysrootDir: sysroot, BrootDir: phaseBroot(broot), TempDir: preflightTemp, HomeDir: preflightHome,
		Restrict: cleanEbuildValue(eb.Vars()["RESTRICT"]), Properties: cleanEbuildValue(eb.Vars()["PROPERTIES"]), Use: cfg.UseFlags,
	})
	if err != nil {
		return fmt.Errorf("rebuild: preflight phase policy: %w", err)
	}
	if request.Policy.Sandbox {
		if _, err := exec.LookPath("sandbox"); err != nil {
			return fmt.Errorf("rebuild: preflight Portage sandbox: %w", err)
		}
	}
	if cfg.AllowLiveRoot {
		// The initial live lane permits only packages whose sourced ebuild/eclass
		// closure defines no package lifecycle hooks. Their defaults are no-ops,
		// leaving the image and VDB as the complete mutable write set captured by
		// the journal. General lifecycle write capture remains a broader gate.
		rejectLifecycle := func(label string, discovery phaseproto.Request, relevant, allowed map[string]bool) error {
			discovery.ID = "live-lifecycle-preflight"
			discovery.Command, discovery.Phase = "discover_phases", ""
			events, err := runPhaseWorker(context.Background(), discovery, cfg, phaseproto.WorkerOptions{Isolation: phaseproto.IsolationPortage})
			if err != nil {
				var detail []string
				for _, event := range events {
					if event.Kind == "log" || event.Kind == "elog" {
						detail = append(detail, event.Message)
					}
				}
				if len(detail) != 0 {
					return fmt.Errorf("rebuild: discover %s live lifecycle: %w: %s", label, err, strings.Join(detail, "\n"))
				}
				return fmt.Errorf("rebuild: discover %s live lifecycle: %w", label, err)
			}
			for _, event := range events {
				if event.Kind == "phase" && relevant[event.Message] {
					if allowed[event.Message] {
						continue
					}
					return fmt.Errorf("rebuild: live canary forbids %s custom lifecycle phase %s", label, event.Message)
				}
			}
			return nil
		}
		// Portage permits custom pkg_setup and pkg_preinst with its phase-specific
		// free/sandbox policy and does not transactionally capture arbitrary writes
		// from them. The default Arise lane mirrors that behavior. Syscall-level
		// lifecycle capture is an optional, USE-gated strengthening feature and
		// must never be a prerequisite for Portage-compatible execution.
		if cfg.AllowLiveUpgrade {
			replaced, err := findInstalledReplacement(cfg.VdbDir, a.Category, a.Package, a.Version.Raw, selectedEbuildSlot(cfg, eb))
			if err != nil {
				return fmt.Errorf("rebuild: preflight installed replacement: %w", err)
			}
			oldEbuilds, err := filepath.Glob(filepath.Join(replaced, "*.ebuild"))
			if err != nil || len(oldEbuilds) != 1 {
				return fmt.Errorf("rebuild: preflight old ebuild: expected one stored ebuild, found %d", len(oldEbuilds))
			}
			old, err := ebuild.ParseEbuild(oldEbuilds[0])
			if err != nil {
				return fmt.Errorf("rebuild: preflight old ebuild: %w", err)
			}
			oldRequest := request
			oldRequest.Ebuild, oldRequest.EAPI = oldEbuilds[0], old.EAPI
			oldRequest.Environment, err = materializeInstalledEnvironment(replaced, preflightDir)
			if err != nil {
				return fmt.Errorf("rebuild: preflight old environment: %w", err)
			}
			allowed := make(map[string]bool)
			// Replacement removal hooks follow Portage's committed-state
			// compatibility semantics; failures are reported after commit.
			allowed["pkg_prerm"], allowed["pkg_postrm"] = true, true
			if inheritsEclass(old, "python-any-r1") || inheritsEclass(old, "python-single-r1") || inheritsEclass(old, "python-r1") {
				allowed["pkg_setup"] = true
			}
			for _, phaseName := range []string{"pkg_preinst", "pkg_postinst", "pkg_prerm", "pkg_postrm"} {
				if advisoryLifecycleOnly(oldEbuilds[0], phaseName) || lifecycleSafeWithExplicitROOT(oldEbuilds[0], phaseName) {
					allowed[phaseName] = true
				}
			}
			for _, phaseName := range []string{"pkg_pretend", "pkg_setup", "pkg_preinst", "pkg_postinst", "pkg_prerm", "pkg_postrm"} {
				if lifecycleOnlyDisabledUseGuards(oldEbuilds[0], phaseName, cfg.UseFlags) {
					allowed[phaseName] = true
				}
			}
			if inheritsEclass(old, "xdg") && !hasXDGPayloadInContents(filepath.Join(replaced, "CONTENTS")) {
				allowed["pkg_preinst"], allowed["pkg_postinst"], allowed["pkg_postrm"] = true, true, true
			}
			if inheritsEclass(old, "xorg-3") && cleanEbuildValue(old.Vars()["FONT"]) == "" {
				// xorg-3 exports these hooks for every consumer, but their bodies
				// perform live writes only for ebuilds that set FONT. The selected
				// ebuild and eclass sources are covered by the approved state hash.
				allowed["pkg_postinst"], allowed["pkg_postrm"] = true, true
			}
			oldRelevant := map[string]bool{"pkg_prerm": true, "pkg_postrm": true}
			if err := rejectLifecycle("old", oldRequest, oldRelevant, allowed); err != nil {
				return err
			}
		}
	}
	// Portage evaluates pkg_pretend after resolution and before any build or
	// package mutation. It receives the selected package environment but no
	// state handoff from pkg_setup, which has not run yet.
	pretend := request
	pretend.ID = "preflight-pkg-pretend"
	pretend.Command, pretend.Phase = "run_phase", "pkg_pretend"
	pretend = applyPortageLifecyclePolicy(pretend, "pkg_pretend")
	cfg.firePhaseStart("pkg_pretend")
	events, pretendErr := runPhaseWorker(context.Background(), pretend, cfg, phaseproto.WorkerOptions{Isolation: phaseproto.IsolationPortage})
	for _, event := range events {
		if event.Kind == "elog" && strings.TrimSpace(event.Message) != "" {
			cfg.fireNotice(event.Stream, strings.TrimSpace(event.Message))
		}
	}
	cfg.firePhaseEnd("pkg_pretend", pretendErr)
	if pretendErr != nil {
		diagnostics := phaseFailureDiagnostics(events, 20)
		if len(diagnostics) != 0 {
			return fmt.Errorf("rebuild: pkg_pretend: %w: %s", pretendErr, strings.Join(diagnostics, "\n"))
		}
		return fmt.Errorf("rebuild: pkg_pretend: %w", pretendErr)
	}
	return nil
}

func materializeInstalledEnvironment(vdbPath, directory string) (string, error) {
	input, err := os.Open(filepath.Join(vdbPath, "environment.bz2"))
	if err != nil {
		return "", err
	}
	defer input.Close()
	path := filepath.Join(directory, "installed-environment")
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(output, stdbzip2.NewReader(input))
	closeErr := output.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return path, nil
}

func inheritsEclass(eb *ebuild.Ebuild, name string) bool {
	for _, inherited := range eb.Inherit {
		if inherited == name {
			return true
		}
	}
	return false
}

var xdgPayloadPrefixes = []string{"usr/share/applications/", "usr/share/icons/", "usr/share/mime/"}

func hasXDGPayloadInContents(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		relative := strings.TrimPrefix(fields[1], "/")
		for _, prefix := range xdgPayloadPrefixes {
			if strings.HasPrefix(relative, prefix) {
				return true
			}
		}
	}
	return false
}

func advisoryLifecycleOnly(path, phase string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	lines := strings.Split(string(data), "\n")
	start := -1
	header := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(phase) + `\s*\(\s*\)\s*\{\s*$`)
	for index, line := range lines {
		if header.MatchString(line) {
			start = index + 1
			break
		}
	}
	if start < 0 {
		return false
	}
	seen := false
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "}" {
			return seen
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		allowed := false
		for _, helper := range []string{"einfo", "elog", "ewarn", "eerror", "eqawarn", "debug-print", "debug-print-function"} {
			if trimmed == helper || strings.HasPrefix(trimmed, helper+" ") {
				allowed = true
				seen = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	return false
}

func lifecycleOnlyDisabledUseGuards(path, phase string, useFlags map[string]bool) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	lines := strings.Split(string(data), "\n")
	header := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(phase) + `\s*\(\s*\)\s*\{\s*$`)
	guard := regexp.MustCompile(`^(!\s+)?use\s+([A-Za-z0-9_+@-]+)\s+&&\s+.+$`)
	start := -1
	for index, line := range lines {
		if header.MatchString(line) {
			start = index + 1
			break
		}
	}
	if start < 0 {
		return false
	}
	seen := false
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "}" {
			return seen
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		match := guard.FindStringSubmatch(trimmed)
		if match == nil {
			return false
		}
		enabled := useFlags[match[2]]
		if (match[1] == "" && enabled) || (match[1] != "" && !enabled) {
			return false
		}
		seen = true
	}
	return false
}

func lifecycleSafeWithExplicitROOT(path, phase string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	lines := strings.Split(string(data), "\n")
	header := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(phase) + `\s*\(\s*\)\s*\{\s*$`)
	start := -1
	for index, line := range lines {
		if header.MatchString(line) {
			start = index + 1
			break
		}
	}
	if start < 0 {
		return false
	}
	ignoredDepth, controlDepth, seen := 0, 0, false
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "}" {
			return seen && ignoredDepth == 0 && controlDepth == 0
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if ignoredDepth > 0 {
			if strings.HasPrefix(trimmed, "if ") {
				ignoredDepth++
			} else if trimmed == "fi" {
				ignoredDepth--
			}
			seen = true
			continue
		}
		if strings.HasPrefix(trimmed, `if [[ -z ${ROOT}`) {
			ignoredDepth = 1
			seen = true
			continue
		}
		if strings.HasPrefix(trimmed, "if use ") && strings.HasSuffix(trimmed, "; then") {
			controlDepth++
			continue
		}
		if trimmed == "fi" && controlDepth > 0 {
			controlDepth--
			continue
		}
		allowed := false
		for _, helper := range []string{"einfo", "elog", "ewarn", "eerror", "eqawarn", "debug-print", "debug-print-function"} {
			if trimmed == helper || strings.HasPrefix(trimmed, helper+" ") {
				allowed, seen = true, true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	return false
}

func enabledUse(flags map[string]bool) string {
	var names []string
	for name, enabled := range flags {
		if enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, " ")
}

func enabledUseWithArch(flags map[string]bool, arch string) string {
	names := useFlagsWithArch(flags, arch)
	ordered := make([]string, 0, len(names))
	for name, enabled := range names {
		if enabled {
			ordered = append(ordered, name)
		}
	}
	sort.Strings(ordered)
	return strings.Join(ordered, " ")
}

func useFlagsWithArch(flags map[string]bool, arch string) map[string]bool {
	result := make(map[string]bool, len(flags)+1)
	for name, enabled := range flags {
		result[name] = enabled
	}
	if arch = strings.TrimSpace(arch); arch != "" {
		result[arch] = true
	}
	return result
}

func phaseRequestEnvironment(cfg *RebuildConfig, use, artifacts string) map[string]string {
	useFlags := make(map[string]bool)
	for _, name := range strings.Fields(use) {
		useFlags[name] = true
	}
	result := map[string]string{"USE": enabledUseWithArch(useFlags, cfg.Arch), "A": artifacts}
	// ApplyPackagePolicy composes make.conf, package.env, and command overrides
	// when effective Portage configuration is available. Reinjecting global
	// flags as request overrides here would incorrectly outrank package.env.
	if cfg.PortageConfig == nil {
		result["CFLAGS"] = cfg.CFLAGS
		result["CXXFLAGS"] = cfg.CXXFLAGS
		result["LDFLAGS"] = cfg.LDFLAGS
		result["MAKEOPTS"] = cfg.MAKEOPTS
		result["ARCH"] = cfg.Arch
	}
	return result
}

// runPackageNofetch emits the package's manual acquisition instructions after
// a RESTRICT=fetch cache miss. It deliberately runs before every build and ROOT
// mutation, with the same package policy used by the normal phase protocol.
func runPackageNofetch(ctx context.Context, atomStr string, eb *ebuild.Ebuild, ebuildFile, workDir, destDir string, cfg *RebuildConfig) error {
	a, err := atom.Parse(atomStr)
	if err != nil || a.Version == nil {
		return fmt.Errorf("package identity: %w", err)
	}
	cat, pn, pvr := a.Category, a.Package, a.Version.Raw
	pv, pr := pvr, "r0"
	if a.Version.Revision >= 0 {
		pr = fmt.Sprintf("r%d", a.Version.Revision)
		pv = strings.TrimSuffix(pvr, "-"+pr)
	}
	p, pf := pn+"-"+pv, pn+"-"+pvr
	repository := cfg.Repository
	if repository == "" {
		repository = "selected"
	}
	repositories := append([]portage.RepoEntry(nil), cfg.Repositories...)
	if len(repositories) == 0 {
		repositories = []portage.RepoEntry{{Name: repository, Location: cfg.RepoDir}}
	}
	tempDir, homeDir := filepath.Join(workDir, "nofetch-temp"), filepath.Join(workDir, "nofetch-home")
	for _, directory := range []string{workDir, destDir, tempDir, homeDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", directory, err)
		}
	}
	request := phaseproto.Request{
		Protocol: phaseproto.Version, ID: strings.NewReplacer("/", "-", ".", "-").Replace(cat + "-" + pf + "-pkg_nofetch"),
		Command: "run_phase", Phase: "pkg_nofetch", EAPI: eb.EAPI, Ebuild: ebuildFile,
		Env: phaseRequestEnvironment(cfg, enabledUse(cfg.UseFlags), ""),
	}
	sysroot, broot := cfg.dependencyRoots()
	request, err = phaseproto.ApplyPackagePolicy(request, phaseproto.PackagePolicy{
		Configuration: cfg.PortageConfig, Repositories: repositories, Repository: repository,
		ConfigRoot: cfg.ConfigRoot, CPV: cat + "/" + pf, Category: cat, PN: pn, P: p, PR: pr,
		Slot: selectedEbuildSlot(cfg, eb), WorkDir: workDir, BuildDir: workDir,
		SourceDir: filepath.Join(workDir, p), ImageDir: destDir, RootDir: cfg.RootDir,
		SysrootDir: sysroot, BrootDir: phaseBroot(broot), TempDir: tempDir, HomeDir: homeDir,
		Restrict: cleanEbuildValue(eb.Vars()["RESTRICT"]), Properties: cleanEbuildValue(eb.Vars()["PROPERTIES"]), Use: cfg.UseFlags,
	})
	if err != nil {
		return fmt.Errorf("phase policy: %w", err)
	}
	cfg.firePhaseStart("pkg_nofetch")
	events, phaseErr := runPhaseWorker(ctx, request, cfg, phaseproto.WorkerOptions{Isolation: phaseproto.IsolationPortage})
	for _, event := range events {
		if event.Kind == "elog" && strings.TrimSpace(event.Message) != "" {
			cfg.fireNotice(event.Stream, strings.TrimSpace(event.Message))
		}
	}
	cfg.firePhaseEnd("pkg_nofetch", phaseErr)
	if phaseErr != nil {
		diagnostics := phaseFailureDiagnostics(events, 20)
		if len(diagnostics) != 0 {
			return fmt.Errorf("%w: %s", phaseErr, strings.Join(diagnostics, "\n"))
		}
	}
	return phaseErr
}

func rebuildWithPhaseProtocol(ctx context.Context, atomStr string, eb *ebuild.Ebuild, ebuildFile, workDir, destDir string, verified distfiles.VerifiedSet, cfg *RebuildConfig) (returnErr error) {
	a, err := atom.Parse(atomStr)
	if err != nil || a.Version == nil {
		return fmt.Errorf("rebuild: protocol package identity: %w", err)
	}
	cat, pn, pvr := a.Category, a.Package, a.Version.Raw
	pv := pvr
	pr := "r0"
	if a.Version.Revision >= 0 {
		pr = fmt.Sprintf("r%d", a.Version.Revision)
		pv = strings.TrimSuffix(pvr, "-"+pr)
	}
	p := pn + "-" + pv
	pf := pn + "-" + pvr
	repository := cfg.Repository
	repositories := append([]portage.RepoEntry(nil), cfg.Repositories...)
	if repository == "" {
		repository = "selected"
	}
	if len(repositories) == 0 {
		repositories = []portage.RepoEntry{{Name: repository, Location: cfg.RepoDir}}
	}
	sourceDir := filepath.Join(workDir, p)
	// Portage creates WORKDIR before src_unpack but does not pre-create S.
	// Custom unpack phases commonly rename an extracted tree to ${S}; creating
	// it here would turn that rename into an unintended nested directory.
	for _, directory := range []string{destDir, filepath.Join(workDir, "temp"), filepath.Join(workDir, "home")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("rebuild: protocol directory %s: %w", directory, err)
		}
	}
	var packageLog *phaseproto.PackageLog
	if err := phaseproto.ValidateElogSinks(cfg.ElogSinks); err != nil {
		return fmt.Errorf("rebuild: %w", err)
	}
	if cfg.PhaseLogDir != "" {
		var filterCommand []string
		if strings.TrimSpace(cfg.LogFilterCommand) != "" {
			filterCommand, err = shlex.Split(cfg.LogFilterCommand, true)
			if err != nil {
				return fmt.Errorf("rebuild: parse PORTAGE_LOG_FILTER_FILE_CMD: %w", err)
			}
			if len(filterCommand) == 0 {
				return fmt.Errorf("rebuild: PORTAGE_LOG_FILTER_FILE_CMD is empty")
			}
		}
		packageLog, err = phaseproto.NewPackageLog(phaseproto.PackageLogOptions{Root: cfg.PhaseLogDir, TempDir: filepath.Join(workDir, "temp"), Category: cat, PF: pf, Split: cfg.SplitLogs, FilterCommand: filterCommand})
		if err != nil {
			return fmt.Errorf("rebuild: reserve durable phase log: %w", err)
		}
		defer func() {
			if finalizeErr := packageLog.Finalize(cfg.CompressLogs); finalizeErr != nil {
				if returnErr != nil {
					returnErr = fmt.Errorf("%v; rebuild: finalize durable phase log %s: %w", returnErr, packageLog.Path(), finalizeErr)
				} else {
					returnErr = fmt.Errorf("rebuild: finalize durable phase log %s: %w", packageLog.Path(), finalizeErr)
				}
			}
		}()
	}
	use := make([]string, 0, len(cfg.UseFlags))
	for name, enabled := range cfg.UseFlags {
		if enabled {
			use = append(use, name)
		}
	}
	sort.Strings(use)
	artifacts := artifactNames(verified.Artifacts)
	requestEnv := phaseRequestEnvironment(cfg, strings.Join(use, " "), strings.Join(artifacts, " "))
	base := phaseproto.Request{
		Protocol: phaseproto.Version, ID: "policy-preflight", Command: "run_phase", Phase: "pkg_setup", EAPI: eb.EAPI, Ebuild: ebuildFile,
		Env: requestEnv,
	}
	base.InstallQAChecks, err = installQAChecks(cfg.RepoDir)
	if err != nil {
		return fmt.Errorf("rebuild: install QA checks: %w", err)
	}
	_, queryBroot := cfg.dependencyRoots()
	base.HasVersion, err = preflightHasVersionQueries(ebuildFile, eb, repositories, repository, cfg.VdbDir, filepath.Join(queryBroot, "var", "db", "pkg"), cfg.HasVersion, base.InstallQAChecks...)
	if err != nil {
		return fmt.Errorf("rebuild: preflight has_version queries: %w", err)
	}
	base.BestVersion = preflightBestVersions(cfg.VdbDir, filepath.Join(queryBroot, "var", "db", "pkg"))
	if len(verified.Artifacts) != 0 {
		base.Distfiles = &verified
	}
	sysroot, broot := cfg.dependencyRoots()
	base, err = phaseproto.ApplyPackagePolicy(base, phaseproto.PackagePolicy{
		Configuration: cfg.PortageConfig, Repositories: repositories, Repository: repository,
		ConfigRoot: cfg.ConfigRoot, CPV: cat + "/" + pf, Category: cat, PN: pn, P: p, PR: pr,
		Slot: selectedEbuildSlot(cfg, eb), WorkDir: workDir, BuildDir: workDir, SourceDir: sourceDir, ImageDir: destDir,
		RootDir: cfg.RootDir, SysrootDir: sysroot, BrootDir: phaseBroot(broot),
		TempDir: filepath.Join(workDir, "temp"), HomeDir: filepath.Join(workDir, "home"),
		Restrict: cleanEbuildValue(eb.Vars()["RESTRICT"]), Properties: cleanEbuildValue(eb.Vars()["PROPERTIES"]), Use: cfg.UseFlags,
	})
	if packageLog != nil {
		base.LogFile = packageLog.Path()
	}
	if err != nil {
		return fmt.Errorf("rebuild: phase protocol policy: %w", err)
	}
	if base.Env["DEFAULT_ABI"] == "" && cfg.Arch == "amd64" {
		base.Env["DEFAULT_ABI"] = "amd64"
	}
	var packageEvents []phaseproto.Event
	runWithContext := func(phaseCtx context.Context, phaseName string) error {
		if strings.HasPrefix(phaseName, "pkg_") && lifecycleOnlyDisabledUseGuards(ebuildFile, phaseName, cfg.UseFlags) {
			return nil
		}
		request := base
		request.ID = strings.NewReplacer("/", "-", ".", "-").Replace(cat + "-" + pf + "-" + phaseName)
		request.Command, request.Phase = "run_phase", phaseName
		request = applyPortageLifecyclePolicy(request, phaseName)
		cfg.firePhaseStart(phaseName)
		events, phaseErr := runPhaseWorker(phaseCtx, request, cfg, phaseproto.WorkerOptions{Isolation: phaseproto.IsolationPortage, DurableLog: packageLog})
		packageEvents = append(packageEvents, events...)
		for _, event := range events {
			if event.Kind == "elog" && strings.TrimSpace(event.Message) != "" {
				cfg.fireNotice(event.Stream, strings.TrimSpace(event.Message))
			}
		}
		cfg.firePhaseEnd(phaseName, phaseErr)
		if phaseErr != nil {
			var logs []string
			for _, event := range events {
				if event.Kind == "log" && event.Message != "" {
					logs = append(logs, event.Message)
				}
			}
			if len(logs) > 20 {
				logs = logs[len(logs)-20:]
			}
			if len(logs) != 0 {
				return fmt.Errorf("rebuild: phase protocol %s: %w: %s", phaseName, phaseErr, strings.Join(logs, "\n"))
			}
			return fmt.Errorf("rebuild: phase protocol %s: %w", phaseName, phaseErr)
		}
		return nil
	}
	run := func(phaseName string) error { return runWithContext(ctx, phaseName) }
	postCommitCtx := ctx
	if cfg.PostCommitContext != nil {
		postCommitCtx = cfg.PostCommitContext
	}
	runPostCommit := func(phaseName string) error { return runWithContext(postCommitCtx, phaseName) }
	buildPhases := protocolBuildPhases(base.Policy)
	filteredPhases := buildPhases[:0]
	for _, phaseName := range buildPhases {
		if strings.HasPrefix(phaseName, "pkg_") && lifecycleOnlyDisabledUseGuards(ebuildFile, phaseName, cfg.UseFlags) {
			continue
		}
		filteredPhases = append(filteredPhases, phaseName)
	}
	buildPhases = filteredPhases
	var replacedVDB string
	var runOld func(string) error
	if cfg.AllowLiveUpgrade {
		replacedVDB, err = findInstalledReplacement(cfg.VdbDir, cat, pn, pvr, selectedEbuildSlot(cfg, eb))
		if err != nil {
			return fmt.Errorf("rebuild: select installed replacement lifecycle: %w", err)
		}
		oldEbuilds, globErr := filepath.Glob(filepath.Join(replacedVDB, "*.ebuild"))
		if globErr != nil || len(oldEbuilds) != 1 {
			return fmt.Errorf("rebuild: installed replacement lifecycle ebuild: expected one, found %d", len(oldEbuilds))
		}
		old, parseErr := ebuild.ParseEbuild(oldEbuilds[0])
		if parseErr != nil {
			return fmt.Errorf("rebuild: installed replacement lifecycle ebuild: %w", parseErr)
		}
		environment, environmentErr := materializeInstalledEnvironment(replacedVDB, workDir)
		if environmentErr != nil {
			return fmt.Errorf("rebuild: installed replacement lifecycle environment: %w", environmentErr)
		}
		oldBase := base
		oldBase.Ebuild, oldBase.EAPI, oldBase.Environment = oldEbuilds[0], old.EAPI, environment
		oldBase.ImageDir = ""
		runOld = func(phaseName string) error {
			request := oldBase
			request.ID = strings.NewReplacer("/", "-", ".", "-").Replace(cat + "-" + pf + "-old-" + phaseName)
			request.Command, request.Phase = "run_phase", phaseName
			request = applyPortageLifecyclePolicy(request, phaseName)
			cfg.firePhaseStart(phaseName)
			phaseCtx := ctx
			if phaseName == "pkg_postrm" {
				phaseCtx = postCommitCtx
			}
			events, phaseErr := runPhaseWorker(phaseCtx, request, cfg, phaseproto.WorkerOptions{Isolation: phaseproto.IsolationPortage, DurableLog: packageLog})
			packageEvents = append(packageEvents, events...)
			for _, event := range events {
				if event.Kind == "elog" && strings.TrimSpace(event.Message) != "" {
					cfg.fireNotice(event.Stream, strings.TrimSpace(event.Message))
				}
			}
			cfg.firePhaseEnd(phaseName, phaseErr)
			if phaseErr != nil {
				return fmt.Errorf("old %s: %w", phaseName, phaseErr)
			}
			return nil
		}
	}
	phaseEnvironment := filepath.Join(workDir, "phase.environment")
	for _, phaseName := range buildPhases {
		request := base
		request.ID = strings.NewReplacer("/", "-", ".", "-").Replace(cat + "-" + pf + "-" + phaseName)
		request.Command, request.Phase = "run_phase", phaseName
		request.EmitMetadata = true
		if _, statErr := os.Stat(phaseEnvironment); statErr == nil {
			request.EnvironmentOverlay = phaseEnvironment
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("rebuild: inspect saved phase environment: %w", statErr)
		}
		request.SaveEnvironment = phaseEnvironment
		request = applyPortageLifecyclePolicy(request, phaseName)
		cfg.firePhaseStart(phaseName)
		// Live package execution uses Portage's sandbox model. Each phase owns a
		// worker so phase-specific policy and userpriv credentials cannot leak
		// across setup, build, install, and lifecycle boundaries.
		events, phaseErr := runPhaseWorker(ctx, request, cfg, phaseproto.WorkerOptions{Isolation: phaseproto.IsolationPortage, DurableLog: packageLog})
		packageEvents = append(packageEvents, events...)
		cfg.firePhaseEnd(phaseName, phaseErr)
		if phaseErr != nil {
			logs := phaseFailureDiagnostics(events, 20)
			if len(logs) != 0 {
				return fmt.Errorf("rebuild: phase protocol %s: %w: %s", phaseName, phaseErr, strings.Join(logs, "\n"))
			}
			return fmt.Errorf("rebuild: phase protocol %s: %w", phaseName, phaseErr)
		}
	}
	base.EnvironmentOverlay = phaseEnvironment
	if executionFeatureEnabled(base.Policy.Features, "fixlafiles") {
		fixed, warnings, fixErr := fixLaFiles(destDir)
		if fixErr != nil {
			return fmt.Errorf("rebuild: fix .la files: %w", fixErr)
		}
		if fixed != 0 {
			cfg.fireNotice("INFO", fmt.Sprintf("Fixed %d libtool archive files.", fixed))
		}
		for _, warning := range warnings {
			cfg.fireNotice("QA", warning.Error())
		}
	}
	vdbMetadata := protocolVDBMetadata(eb, ebuildFile, cat, pf, cfg.SelectedIUSE, cfg.UseFlags, base, packageEvents)
	if err := expandBuiltSlotOperators(vdbMetadata, cfg); err != nil {
		return fmt.Errorf("rebuild: expand built slot operators: %w", err)
	}
	environment := protocolEnvironmentSnapshot(base)
	if cfg.BuildPackage {
		cfg.fireStage("package")
		if _, err := publishBuiltGPKG(ctx, cfg, cat, pf, destDir, vdbMetadata, environment); err != nil {
			return err
		}
	}
	if cfg.BuildOnly {
		return nil
	}
	// Portage runs pkg_preinst unsandboxed with host IPC/network/PID access
	// after src_install has produced the image and before any payload is copied
	// into ROOT. It cannot share the build worker's isolation policy.
	if err := run("pkg_preinst"); err != nil {
		return err
	}
	if cfg.CommitLock != nil {
		cfg.CommitLock.Lock()
		defer cfg.CommitLock.Unlock()
	}
	journalDir := cfg.JournalDir
	if journalDir == "" {
		journalDir = filepath.Join(workDir, "journal")
	}
	mergeCfg := merge.MergeConfig{
		RootDir: cfg.RootDir, VdbDir: cfg.VdbDir, Category: cat, Package: pn, Version: pvr,
		JournalDir:           journalDir,
		AllowLiveRoot:        cfg.AllowLiveRoot,
		AllowLiveReplacement: cfg.AllowLiveReplacement,
		VDBLockHeld:          cfg.VDBLockHeld,
		VDBMetadata:          vdbMetadata,
		Environment:          environment,
		OnStage:              cfg.fireStage,
		OnProgress:           cfg.fireProgress,
		// Portage runs pkg_postinst after payload/VDB commit and retains the
		// installed package if the hook fails. Arise journals its own merge first,
		// then preserves the same committed-state lifecycle semantics.
		AfterCommit: func() error {
			postinstErr := runPostCommit("pkg_postinst")
			refreshLiveInfoIndex(cfg, destDir, "/usr/bin/install-info")
			envErr := refreshLiveEnvironment(cfg)
			return errors.Join(postinstErr, envErr)
		},
	}
	if runOld != nil {
		mergeCfg.BeforeReplacementRemoval = func() error {
			if hookErr := runOld("pkg_prerm"); hookErr != nil {
				// Portage reports removal-hook failures but continues safely
				// unmerging the replaced instance. They do not make the newly
				// committed replacement a failed package job.
				cfg.fireNotice("WARN", hookErr.Error())
			}
			return nil
		}
		mergeCfg.AfterReplacementRemoval = func() error {
			if hookErr := runOld("pkg_postrm"); hookErr != nil {
				cfg.fireNotice("WARN", hookErr.Error())
			}
			return nil
		}
		mergeCfg.AfterCommit = func() error {
			postinstErr := runPostCommit("pkg_postinst")
			refreshLiveInfoIndex(cfg, destDir, "/usr/bin/install-info")
			envErr := refreshLiveEnvironment(cfg)
			return errors.Join(postinstErr, envErr)
		}
	}
	if cfg.Features != nil && cfg.Features.IsEnabled(features.FeatPreserveLibs) {
		mergeCfg.PreserveLibs = true
	}
	if cfg.AllowLiveUpgrade {
		mergeCfg.ReplacedVDBPath = replacedVDB
	}
	if cfg.PortageConfig != nil {
		mergeCfg.ConfigProtect = strings.Fields(cfg.PortageConfig.MakeConf["CONFIG_PROTECT"])
		mergeCfg.ConfigProtectMask = strings.Fields(cfg.PortageConfig.MakeConf["CONFIG_PROTECT_MASK"])
		if features.ParseFeatures(cfg.PortageConfig.MakeConf["FEATURES"]).IsEnabled(features.FeatPreserveLibs) {
			mergeCfg.PreserveLibs = true
		}
	}
	mergeErr := merge.Merge(ctx, destDir, mergeCfg)
	committed := mergeErr == nil
	if !committed {
		var postCommit *merge.PostCommitError
		committed = errors.As(mergeErr, &postCommit)
	}
	if committed && cfg.OnTransactionCommit != nil {
		if callbackErr := cfg.OnTransactionCommit(mergeErr); callbackErr != nil {
			return &merge.PostCommitError{Err: fmt.Errorf("record committed transaction: %w", callbackErr)}
		}
	}
	if mergeErr != nil {
		return fmt.Errorf("rebuild: protocol merge: %w", mergeErr)
	}
	if len(cfg.ElogSinks) != 0 {
		if _, err := phaseproto.DeliverElog(packageEvents, phaseproto.ElogOptions{LogDir: cfg.PhaseLogDir, Category: cat, PF: p, Classes: cfg.ElogClasses, Sinks: cfg.ElogSinks, Output: cfg.ElogOutput}); err != nil {
			return fmt.Errorf("rebuild: elog delivery: %w", err)
		}
	}
	return nil
}

var infoIndexNames = []string{"dir", "dir.Z", "dir.gz", "dir.bz2", "dir.lzma", "dir.lz", "dir.xz", "dir.zst", "dir.info", "dir.info.Z", "dir.info.gz", "dir.info.bz2", "dir.info.lzma", "dir.info.lz", "dir.info.xz", "dir.info.zst"}

type infoIndexResult struct {
	Processed int
	Errors    []error
}

func refreshLiveInfoIndex(cfg *RebuildConfig, imageDir, installInfo string) {
	result := regenerateLiveInfoIndexReport(cfg.RootDir, imageDir, cfg.AllowLiveRoot, installInfo)
	if result.Processed == 0 && len(result.Errors) == 0 {
		return
	}
	cfg.fireNotice("INFO", "Regenerating GNU info directory index...")
	cfg.fireNotice("INFO", fmt.Sprintf("Processed %d info files; %d errors.", result.Processed, len(result.Errors)))
	for _, err := range result.Errors {
		cfg.fireNotice("WARN", err.Error())
	}
}

func refreshLiveEnvironment(cfg *RebuildConfig) error {
	result, err := envupdate.UpdateRoot(cfg.RootDir, "", true)
	if err != nil {
		return fmt.Errorf("post-merge env-update: %w", err)
	}
	cfg.fireNotice("INFO", fmt.Sprintf("Regenerated environment from %d env.d files.", result.EnvironmentFiles))
	if result.LdconfigRan {
		cfg.fireNotice("INFO", "Regenerated dynamic linker cache with ldconfig -X.")
	}
	return nil
}

func installQAChecks(repoDir string) ([]string, error) {
	directories := []string{
		"/usr/local/lib/install-qa-check.d",
		"/usr/lib/install-qa-check.d",
		filepath.Join(repoDir, "metadata", "install-qa-check.d"),
		"/usr/lib/portage/install-qa-check.d",
	}
	// Modern Portage installs its shell helpers below an implementation-specific
	// pythonX.Y directory. The unversioned path is retained for older layouts.
	versioned, globErr := filepath.Glob("/usr/lib/portage/python*/install-qa-check.d")
	if globErr != nil {
		return nil, fmt.Errorf("discover versioned Portage install QA checks: %w", globErr)
	}
	sort.Strings(versioned)
	directories = append(directories, versioned...)
	selected := make(map[string]string)
	for _, directory := range directories {
		entries, err := os.ReadDir(directory)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", directory, err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if _, exists := selected[entry.Name()]; !exists {
				selected[entry.Name()] = filepath.Join(directory, entry.Name())
			}
		}
	}
	names := make([]string, 0, len(selected))
	for name := range selected {
		names = append(names, name)
	}
	sort.Strings(names)
	checks := make([]string, 0, len(names))
	for _, name := range names {
		checks = append(checks, selected[name])
	}
	return checks, nil
}

func regenerateLiveInfoIndex(rootDir, imageDir string, live bool, installInfo string) error {
	return errors.Join(regenerateLiveInfoIndexReport(rootDir, imageDir, live, installInfo).Errors...)
}

func regenerateLiveInfoIndexReport(rootDir, imageDir string, live bool, installInfo string) infoIndexResult {
	var result infoIndexResult
	if !live {
		return result
	}
	stagedDir := filepath.Join(imageDir, "usr", "share", "info")
	stagedEntries, err := os.ReadDir(stagedDir)
	if os.IsNotExist(err) {
		return result
	}
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("refresh Info index: inspect staged manuals: %w", err))
		return result
	}
	hasManual := false
	for _, entry := range stagedEntries {
		if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") && !slices.Contains(infoIndexNames, entry.Name()) {
			hasManual = true
			break
		}
	}
	if !hasManual {
		return result
	}
	if _, err := os.Stat(installInfo); err != nil {
		if os.IsNotExist(err) {
			return result
		}
		result.Errors = append(result.Errors, fmt.Errorf("refresh Info index: inspect install-info: %w", err))
		return result
	}
	infoDir := filepath.Join(rootDir, "usr", "share", "info")
	entries, err := os.ReadDir(infoDir)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("refresh Info index: read %s: %w", infoDir, err))
		return result
	}
	for _, name := range infoIndexNames {
		if err := os.Remove(filepath.Join(infoDir, name)); err != nil && !os.IsNotExist(err) {
			result.Errors = append(result.Errors, fmt.Errorf("refresh Info index: remove old %s: %w", name, err))
			return result
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	index := filepath.Join(infoDir, "dir")
	for _, entry := range entries {
		// Compression can intentionally leave compatibility symlinks such as
		// bashref.info -> bash.info dangling beside bash.info.gz. The real
		// compressed manual is sufficient to populate the index; never pass
		// symlinks or other special entries to install-info.
		if !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".") || slices.Contains(infoIndexNames, entry.Name()) {
			continue
		}
		manual := filepath.Join(infoDir, entry.Name())
		result.Processed++
		if output, err := exec.Command(installInfo, "--dir-file="+index, manual).CombinedOutput(); err != nil && !strings.Contains(strings.ToLower(string(output)), "no info dir entry in") {
			result.Errors = append(result.Errors, fmt.Errorf("install-info: %s for %s", strings.TrimSpace(string(output)), manual))
		}
	}
	return result
}

var staticHasVersionQuery = regexp.MustCompile(`\bhas_version[[:space:]]+(?:-([bdr])[[:space:]]+)?(?:"([^"$[:space:]]+)"|'([^'$[:space:]]+)'|([^"'$[:space:];]+))`)
var expandedHasVersionQuery = regexp.MustCompile(`\bhas_version[[:space:]]+(?:-([bdr])[[:space:]]+)?"([^"]*\$[^"]*)"`)

func preflightHasVersionQueries(ebuildFile string, eb *ebuild.Ebuild, repositories []portage.RepoEntry, repository, rootVDB, brootVDB string, configured map[string]bool, additionalFiles ...string) (map[string]bool, error) {
	result := make(map[string]bool, len(configured))
	for query, answer := range configured {
		result[query] = answer
	}
	files := append([]string{ebuildFile}, additionalFiles...)
	eclassDirs, err := portage.EclassLookupDirectories(repositories, repository)
	if err != nil {
		return nil, err
	}
	initialInherits := append([]string(nil), eb.Inherit...)
	ebuildData, err := os.ReadFile(ebuildFile)
	if err != nil {
		return nil, err
	}
	// Runtime Bash decides whether conditional inherits execute. Preflight
	// nevertheless needs a conservative superset so an eclass selected by a
	// USE/version conditional cannot introduce an unseen has_version query.
	initialInherits = append(initialInherits, staticInheritedEclasses(string(ebuildData))...)
	eclassFiles, err := inheritedEclassClosure(initialInherits, eclassDirs)
	if err != nil {
		return nil, err
	}
	files = append(files, eclassFiles...)
	inheritedEclasses := make(map[string]bool, len(initialInherits)+len(eclassFiles))
	for _, name := range initialInherits {
		inheritedEclasses[name] = true
	}
	for _, path := range eclassFiles {
		inheritedEclasses[strings.TrimSuffix(filepath.Base(path), ".eclass")] = true
	}
	queries := make(map[string]string)
	derived := derivedEbuildIdentityVariables(ebuildFile)
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		for _, match := range staticHasVersionQuery.FindAllStringSubmatch(string(data), -1) {
			query := match[2]
			if query == "" {
				query = match[3]
			}
			if query == "" {
				query = match[4]
			}
			queries[query] = match[1]
		}
		for _, match := range expandedHasVersionQuery.FindAllStringSubmatch(string(data), -1) {
			complete := true
			query := os.Expand(match[2], func(name string) string {
				value, exists := eb.Vars()[name]
				if !exists {
					value, exists = derived[name]
				}
				if !exists {
					complete = false
					return ""
				}
				return cleanEbuildValue(value)
			})
			if complete && query != "" && !strings.ContainsAny(query, "$ \t\r\n") {
				queries[query] = match[1]
			}
		}
	}
	for query, domain := range queries {
		if _, exists := result[query]; exists {
			continue
		}
		vdb := rootVDB
		if domain == "b" {
			vdb = brootVDB
		}
		setHasVersionAnswer(result, domain, query, installedAtomMatch(vdb, query))
	}
	// vala.eclass constructs its build-host probe from the installed API slot
	// and VALA_USE_DEPEND inside a loop, so it cannot appear as a static quoted
	// atom. Expand that finite input set during preflight instead of granting the
	// worker general VDB access.
	if inheritedEclasses["vala"] {
		valaUse := strings.Fields(cleanEbuildValue(eb.Vars()["VALA_USE_DEPEND"]))
		var useParts []string
		for _, flag := range valaUse {
			switch flag {
			case "vapigen":
				useParts = append(useParts, "vapigen(+)")
			case "valadoc":
				useParts = append(useParts, "valadoc(-)")
			default:
				useParts = append(useParts, flag)
			}
		}
		entries, _ := os.ReadDir(filepath.Join(brootVDB, "dev-lang"))
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "vala-") {
				continue
			}
			slotData, err := os.ReadFile(filepath.Join(brootVDB, "dev-lang", entry.Name(), "SLOT"))
			if err != nil {
				continue
			}
			slot := strings.SplitN(strings.TrimSpace(string(slotData)), "/", 2)[0]
			for _, suffix := range []string{"", "[" + strings.Join(useParts, ",") + "]"} {
				if suffix == "[]" {
					continue
				}
				query := "dev-lang/vala:" + slot + suffix
				setHasVersionAnswer(result, "b", query, installedAtomMatch(brootVDB, query))
			}
		}
	}
	pythonEclass := false
	for inherited := range inheritedEclasses {
		// distutils-r1 is the normal public entry point for Python packages and
		// inherits python-r1/python-any-r1 transitively.
		if strings.HasPrefix(inherited, "python-") || inherited == "distutils-r1" {
			pythonEclass = true
			break
		}
	}
	if pythonEclass {
		// python-any-r1 probes every compatible implementation in preference
		// order, including implementations that are not installed.  Snapshot
		// both the positive and negative answers from the finite declarations;
		// collecting only installed slots leaves the first absent interpreter as
		// an un-preflighted query in the phase worker.
		pythonReqUses := make(map[string]bool)
		if value := cleanEbuildValue(eb.Vars()["PYTHON_REQ_USE"]); value != "" {
			pythonReqUses[value] = true
		}
		var pythonSources []string
		for _, path := range files {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil, readErr
			}
			source := string(data)
			pythonSources = append(pythonSources, source)
			for _, match := range pythonReqUseRE.FindAllStringSubmatch(source, -1) {
				if value := strings.TrimSpace(match[1]); value != "" {
					pythonReqUses[value] = true
				}
			}
		}
		for _, source := range pythonSources {
			for _, query := range dynamicPythonQueries(source) {
				setHasVersionAnswer(result, "b", query, installedAtomMatch(brootVDB, query))
				for pythonReqUse := range pythonReqUses {
					qualified := query + "[" + pythonReqUse + "]"
					setHasVersionAnswer(result, "b", qualified, installedAtomMatch(brootVDB, qualified))
				}
			}
		}
		// python_check_deps commonly calls python_has_version with a package
		// atom qualified by the per-implementation PYTHON_USEDEP variables.
		// Those variables exist only inside _python_run_check_deps, so ordinary
		// shell-variable expansion cannot discover the concrete worker queries.
		// Materialize the finite PYTHON_COMPAT set here for arbitrary atoms.
		var implementations []string
		for _, query := range dynamicPythonQueries(strings.Join(pythonSources, "\n")) {
			implementations = append(implementations, strings.TrimPrefix(query, "dev-lang/python:"))
		}
		for _, source := range pythonSources {
			for _, query := range dynamicPythonUseDepQueries(source, implementations) {
				setHasVersionAnswer(result, "b", query, installedAtomMatch(brootVDB, query))
			}
		}
		entries, _ := os.ReadDir(filepath.Join(brootVDB, "dev-lang"))
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "python-") {
				continue
			}
			installed, parseErr := atom.Parse("dev-lang/" + entry.Name())
			if parseErr != nil || installed.Package != "python" {
				continue
			}
			slotData, err := os.ReadFile(filepath.Join(brootVDB, "dev-lang", entry.Name(), "SLOT"))
			if err != nil {
				continue
			}
			slot := strings.SplitN(strings.TrimSpace(string(slotData)), "/", 2)[0]
			query := "dev-lang/python:" + slot
			setHasVersionAnswer(result, "b", query, installedAtomMatch(brootVDB, query))
		}
	}
	for _, path := range eclassFiles {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		switch filepath.Base(path) {
		case "llvm.eclass":
			for _, slot := range shellArrayValues(string(data), "_LLVM_KNOWN_SLOTS") {
				query := "llvm-core/llvm:" + slot
				setHasVersionAnswer(result, "b", query, installedAtomMatch(brootVDB, query))
			}
		case "rust.eclass":
			useSuffix := ""
			if value := cleanEbuildValue(eb.Vars()["RUST_REQ_USE"]); value != "" {
				useSuffix = "[" + value + "]"
			}
			for _, slot := range shellArrayValues(string(data), "_RUST_SLOTS_ORDERED") {
				for _, cp := range []string{"dev-lang/rust", "dev-lang/rust-bin"} {
					query := cp + ":" + slot + useSuffix
					setHasVersionAnswer(result, "b", query, installedAtomMatch(brootVDB, query))
				}
			}
		}
	}
	if inheritedEclasses["autotools"] {
		for _, path := range files {
			if filepath.Base(path) != "autotools.eclass" {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			for _, query := range dynamicAutotoolsQueries(string(data)) {
				setHasVersionAnswer(result, "b", query, installedAtomMatch(brootVDB, query))
			}
		}
	}
	return result, nil
}

func setHasVersionAnswer(result map[string]bool, domain, query string, answer bool) {
	if domain == "" || domain == "d" {
		domain = "r"
	}
	result[domain+"\t"+query] = answer
	// Retain the old key for API compatibility. The worker always prefers the
	// domain-qualified entry, which prevents ROOT/BROOT collisions.
	result[query] = answer
}

func derivedEbuildIdentityVariables(ebuildFile string) map[string]string {
	stem := strings.TrimSuffix(filepath.Base(ebuildFile), ".ebuild")
	category := filepath.Base(filepath.Dir(filepath.Dir(ebuildFile)))
	parsed, err := atom.Parse(category + "/" + stem)
	if err != nil {
		return nil
	}
	pn, pv, pvr, pr := parsed.Package, "", "", "r0"
	if parsed.Version != nil {
		pv = parsed.Version.Raw
		pvr = pv
		if index := strings.LastIndex(pv, "-r"); index >= 0 {
			if _, err := strconv.Atoi(pv[index+2:]); err == nil {
				pr = pv[index+1:]
				pv = pv[:index]
			}
		}
		if pr != "r0" {
			pvr = pv + "-" + pr
		}
	}
	p := pn
	if pv != "" {
		p += "-" + pv
	}
	pf := p
	if pr != "r0" {
		pf += "-" + pr
	}
	return map[string]string{
		"CATEGORY": category, "PN": pn, "PV": pv, "PR": pr,
		"P": p, "PVR": pvr, "PF": pf,
	}
}

func inheritedEclassClosure(initial, directories []string) ([]string, error) {
	seen := make(map[string]bool)
	var result []string
	queue := append([]string(nil), initial...)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		var path string
		for _, directory := range directories {
			candidate := filepath.Join(directory, name+".eclass")
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
				break
			}
		}
		if path == "" {
			continue
		}
		result = append(result, path)
		parsed, err := ebuild.ParseEbuild(path)
		if err != nil {
			return nil, fmt.Errorf("parse inherited eclass %s: %w", path, err)
		}
		inherited := append([]string(nil), parsed.Inherit...)
		// The metadata parser deliberately skips commands while inside shell
		// conditionals. Real eclasses can place a later, unconditional inherit
		// after complex case/if preambles, so conservatively union static inherit
		// lines to keep the transitive eclass closure complete.
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		inherited = append(inherited, staticInheritedEclasses(string(data))...)
		queue = append(queue, inherited...)
	}
	return result, nil
}

var staticInheritLine = regexp.MustCompile(`(?m)^[[:space:]]*inherit[[:space:]]+([^#\r\n]+)`)
var staticEclassName = regexp.MustCompile(`^[A-Za-z0-9+_.-]+$`)

func staticInheritedEclasses(source string) []string {
	var result []string
	for _, match := range staticInheritLine.FindAllStringSubmatch(source, -1) {
		for _, name := range strings.Fields(match[1]) {
			if staticEclassName.MatchString(name) {
				result = append(result, name)
			}
		}
	}
	return result
}

var pythonImplementationRE = regexp.MustCompile(`\bpython3_([0-9]+)(?:t)?\b`)
var pythonImplementationRangeRE = regexp.MustCompile(`\bpython3_\{([0-9]+)\.\.([0-9]+)\}(?:t)?`)
var pythonReqUseRE = regexp.MustCompile(`(?m)^[[:space:]]*PYTHON_REQ_USE[[:space:]]*=[[:space:]]*["']([^"']*)["']`)
var pythonUseDepQueryRE = regexp.MustCompile(`\b(?:python_)?has_version[[:space:]]+(?:-[bdr][[:space:]]+)?["']([^"']*\$\{PYTHON_(?:SINGLE_)?USEDEP\}[^"']*)["']`)

func dynamicPythonQueries(source string) []string {
	minors := make(map[int]bool)
	for _, match := range pythonImplementationRE.FindAllStringSubmatch(source, -1) {
		if minor, err := strconv.Atoi(match[1]); err == nil {
			minors[minor] = true
		}
	}
	for _, match := range pythonImplementationRangeRE.FindAllStringSubmatch(source, -1) {
		start, startErr := strconv.Atoi(match[1])
		end, endErr := strconv.Atoi(match[2])
		if startErr != nil || endErr != nil || start > end || end-start > 100 {
			continue
		}
		for minor := start; minor <= end; minor++ {
			minors[minor] = true
		}
	}
	result := make([]string, 0, len(minors))
	for minor := range minors {
		result = append(result, fmt.Sprintf("dev-lang/python:3.%d", minor))
	}
	sort.Strings(result)
	return result
}

func dynamicPythonUseDepQueries(source string, implementations []string) []string {
	seen := make(map[string]bool)
	for _, match := range pythonUseDepQueryRE.FindAllStringSubmatch(source, -1) {
		for _, implementation := range implementations {
			impl := "python" + strings.ReplaceAll(implementation, ".", "_")
			query := strings.ReplaceAll(match[1], "${PYTHON_USEDEP}", "python_targets_"+impl+"(-)")
			query = strings.ReplaceAll(query, "${PYTHON_SINGLE_USEDEP}", "python_single_target_"+impl+"(-)")
			if query != "" && !strings.Contains(query, "${") {
				seen[query] = true
			}
		}
	}
	result := make([]string, 0, len(seen))
	for query := range seen {
		result = append(result, query)
	}
	sort.Strings(result)
	return result
}

func shellArrayValues(source, name string) []string {
	expression := regexp.MustCompile(`(?s)\b` + regexp.QuoteMeta(name) + `\s*=\s*\((.*?)\)`)
	match := expression.FindStringSubmatch(source)
	if match == nil {
		return nil
	}
	seen := make(map[string]bool)
	braceRange := regexp.MustCompile(`^\{([0-9]+)\.\.([0-9]+)\}$`)
	for _, field := range strings.Fields(match[1]) {
		field = strings.Trim(field, `"'`)
		if parts := braceRange.FindStringSubmatch(field); parts != nil {
			start, startErr := strconv.Atoi(parts[1])
			end, endErr := strconv.Atoi(parts[2])
			if startErr != nil || endErr != nil || absInt(start-end) > 100 {
				continue
			}
			step := 1
			if start > end {
				step = -1
			}
			for value := start; ; value += step {
				seen[strconv.Itoa(value)] = true
				if value == end {
					break
				}
			}
			continue
		}
		if field != "" && !strings.ContainsAny(field, "$(){}") {
			seen[field] = true
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func dynamicAutotoolsQueries(source string) []string {
	var result []string
	for _, definition := range []struct {
		name, atom string
	}{
		{name: "_LATEST_AUTOMAKE", atom: "dev-build/automake"},
		{name: "_LATEST_AUTOCONF", atom: "dev-build/autoconf"},
	} {
		expression := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(definition.name) + `\s*=\s*\(([^)]*)\)`)
		match := expression.FindStringSubmatch(source)
		if match == nil {
			continue
		}
		for _, entry := range strings.Fields(match[1]) {
			entry = strings.Trim(entry, `"'`)
			parts := strings.SplitN(entry, ":", 2)
			version := parts[0]
			if len(parts) == 2 {
				version = parts[1]
			}
			if version != "" {
				result = append(result, "="+definition.atom+"-"+version+"*")
			}
		}
	}
	sort.Strings(result)
	return result
}

func installedAtomMatch(vdbDir, query string) bool {
	matched, err := installedquery.Match(vdbDir, query, nil)
	return err == nil && matched
}

func preflightBestVersions(rootVDB, brootVDB string) map[string]string {
	result := make(map[string]string)
	for domain, vdbDir := range map[string]string{"r": rootVDB, "b": brootVDB} {
		categories, err := os.ReadDir(vdbDir)
		if err != nil {
			continue
		}
		for _, category := range categories {
			if !category.IsDir() {
				continue
			}
			packages, _ := os.ReadDir(filepath.Join(vdbDir, category.Name()))
			for _, pkg := range packages {
				if !pkg.IsDir() {
					continue
				}
				candidate, err := atom.Parse(category.Name() + "/" + pkg.Name())
				if err != nil || candidate.Version == nil {
					continue
				}
				key := domain + "\t" + candidate.CP()
				current, _ := atom.Parse(result[key])
				if current == nil || current.Version == nil || candidate.Version.Compare(current.Version) > 0 {
					result[key] = category.Name() + "/" + pkg.Name()
				}
			}
		}
	}
	return result
}

func findInstalledReplacement(vdbDir, category, packageName, newVersion, slot string) (string, error) {
	directory := filepath.Join(vdbDir, category)
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		installedAtom, parseErr := atom.Parse(category + "/" + entry.Name())
		if parseErr != nil || installedAtom.Package != packageName || installedAtom.Version == nil || installedAtom.Version.Raw == newVersion {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		installedSlot, err := os.ReadFile(filepath.Join(path, "SLOT"))
		if err != nil {
			return "", err
		}
		if strings.SplitN(strings.TrimSpace(string(installedSlot)), "/", 2)[0] == strings.SplitN(slot, "/", 2)[0] {
			matches = append(matches, path)
		}
	}
	sort.Strings(matches)
	if len(matches) != 1 {
		return "", fmt.Errorf("expected exactly one installed same-slot predecessor, found %d", len(matches))
	}
	return matches[0], nil
}

func protocolEnvironmentSnapshot(request phaseproto.Request) []byte {
	values := make(map[string]string, len(request.Env)+32)
	for name, value := range request.Env {
		values[name] = value
	}
	identity := map[string]string{
		"CATEGORY": request.Package.Category, "PN": request.Package.PN, "PV": request.Package.PV,
		"PR": request.Package.PR, "P": request.Package.P, "PVR": request.Package.PVR,
		"PF": request.Package.PF, "SLOT": request.Package.Slot, "PORTAGE_REPO_NAME": request.Package.Repository,
		"EAPI": request.EAPI, "ROOT": request.RootDir, "SYSROOT": request.SysrootDir, "BROOT": request.BrootDir,
		"EPREFIX": "", "EROOT": request.RootDir, "ESYSROOT": request.SysrootDir,
		"WORKDIR": request.WorkDir, "S": request.SourceDir, "D": request.ImageDir, "ED": request.ImageDir,
		"T": request.TempDir, "TMPDIR": request.TempDir, "TMP": request.TempDir, "TEMP": request.TempDir,
		"HOME": request.HomeDir, "PORTAGE_BUILDDIR": request.BuildDir,
		"PORTAGE_CONFIGROOT": request.ConfigRoot, "PORTAGE_LOG_FILE": request.LogFile,
		"FILESDIR": filepath.Join(filepath.Dir(request.Ebuild), "files"),
	}
	for name, value := range identity {
		values[name] = value
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	var snapshot strings.Builder
	for _, name := range names {
		snapshot.WriteString("export ")
		snapshot.WriteString(name)
		snapshot.WriteString("='")
		snapshot.WriteString(strings.ReplaceAll(values[name], "'", "'\\''"))
		snapshot.WriteString("'\n")
	}
	return []byte(snapshot.String())
}

func protocolVDBMetadata(eb *ebuild.Ebuild, ebuildFile, category, pf, selectedIUSE string, selectedUse map[string]bool, request phaseproto.Request, events []phaseproto.Event) map[string]string {
	vars := eb.Vars()
	metadata := map[string]string{
		"CATEGORY": category, "PF": pf, "EAPI": eb.EAPI,
		"SLOT":                request.Package.Slot,
		"repository":          request.Package.Repository,
		"ARISE_PHASE_ENV_ABI": portage.PhaseEnvironmentABI,
		// The resolver action owns the package-local effective USE domain.
		// request.Env also carries global/package.env execution settings and must
		// never be serialized wholesale into the installed VDB USE record.
		"USE": enabledUse(selectedUse),
	}
	for _, name := range []string{"DEPEND", "RDEPEND", "BDEPEND", "IDEPEND", "PDEPEND", "IUSE", "REQUIRED_USE", "LICENSE", "PROPERTIES", "RESTRICT", "DEFINED_PHASES", "INHERITED"} {
		metadata[name] = strings.Trim(vars[name], "\"'")
	}
	for _, event := range events {
		if event.Kind == "metadata" {
			metadata[event.Class] = event.Message
		}
	}
	if strings.TrimSpace(selectedIUSE) != "" {
		metadata["IUSE"] = selectedIUSE
	}
	if data, err := os.ReadFile(ebuildFile); err == nil {
		metadata[pf+".ebuild"] = string(data)
	}
	return metadata
}

func expandBuiltSlotOperators(metadata map[string]string, cfg *RebuildConfig) error {
	if cfg == nil {
		return fmt.Errorf("missing rebuild configuration")
	}
	sysroot, broot := cfg.dependencyRoots()
	rootVDB := cfg.VdbDir
	brootVDB := rootVDB
	if filepath.Clean(broot) != filepath.Clean(cfg.RootDir) {
		brootVDB = filepath.Join(broot, "var", "db", "pkg")
	}
	sysrootVDB := rootVDB
	if filepath.Clean(sysroot) != filepath.Clean(cfg.RootDir) {
		sysrootVDB = filepath.Join(sysroot, "var", "db", "pkg")
	}
	for _, field := range []string{"DEPEND", "RDEPEND", "BDEPEND", "IDEPEND", "PDEPEND"} {
		raw := strings.TrimSpace(metadata[field])
		if raw == "" {
			continue
		}
		vdbDir := rootVDB
		switch field {
		case "DEPEND":
			vdbDir = sysrootVDB
		case "BDEPEND":
			vdbDir = brootVDB
		}
		expanded, err := expandBuiltSlotOperatorsInDependency(raw, vdbDir, cfg.UseFlags)
		if err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
		metadata[field] = expanded
	}
	return nil
}

func expandBuiltSlotOperatorsInDependency(raw, vdbDir string, callerUse map[string]bool) (string, error) {
	tree, err := depstring.Parse(raw)
	if err != nil {
		return "", err
	}
	var walk func(depstring.DepNode) error
	walk = func(current depstring.DepNode) error {
		switch node := current.(type) {
		case *depstring.AtomDep:
			dependency, err := atom.ParsePackageAtom(node.Atom)
			if err != nil {
				return err
			}
			if dependency.SlotOp != atom.SlotOpEq || dependency.Subslot != "" {
				return nil
			}
			cpv, err := installedquery.Best(vdbDir, node.Atom, callerUse)
			if err != nil {
				return err
			}
			if cpv == "" {
				return nil
			}
			installed, err := atom.Parse(cpv)
			if err != nil {
				return err
			}
			slotData, err := os.ReadFile(filepath.Join(vdbDir, installed.Category, installed.Package+"-"+installed.Version.Raw, "SLOT"))
			if err != nil {
				return err
			}
			parts := strings.SplitN(strings.TrimSpace(string(slotData)), "/", 2)
			dependency.Slot = parts[0]
			dependency.Subslot = parts[0]
			if len(parts) == 2 && parts[1] != "" {
				dependency.Subslot = parts[1]
			}
			node.Atom = dependency.String()
		case *depstring.AllOfGroup:
			for _, child := range node.Children {
				if err := walk(child); err != nil {
					return err
				}
			}
		case *depstring.AnyOfGroup:
			for _, child := range node.Children {
				if err := walk(child); err != nil {
					return err
				}
			}
		case *depstring.UseConditional:
			for _, child := range node.Children {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(tree); err != nil {
		return "", err
	}
	return tree.String(), nil
}

func cleanEbuildValue(value string) string {
	return strings.Trim(strings.TrimSpace(value), "\"'")
}

func effectiveEbuildSlot(eb *ebuild.Ebuild) string {
	slot := cleanEbuildValue(eb.Vars()["SLOT"])
	if slot == "" {
		return "0"
	}
	return slot
}

func phaseFailureDiagnostics(events []phaseproto.Event, limit int) []string {
	if limit <= 0 {
		return nil
	}
	var logs []string
	lastPhase := ""
	for _, event := range events {
		if event.Kind == "phase" && strings.TrimSpace(event.Message) != "" {
			lastPhase = strings.TrimSpace(event.Message)
		}
		if (event.Kind == "log" || event.Kind == "elog") && strings.TrimSpace(event.Message) != "" {
			logs = append(logs, event.Message)
		}
	}
	causal := []string{
		"error:", " error ", "failed", "failure", "cannot ", "can't ",
		"permission denied", "undefined reference", "no rule to make target",
		"not found", "no such file", "no space left on device", "disk quota exceeded", "unrecognized option", "unknown option",
		"assertion", "fatal:", "segmentation fault", "aborted",
	}
	selected := make(map[int]bool)
	for index, line := range logs {
		lower := " " + strings.ToLower(line) + " "
		matched := false
		for _, signal := range causal {
			if strings.Contains(lower, signal) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		for contextIndex := max(0, index-2); contextIndex <= min(len(logs)-1, index+2); contextIndex++ {
			selected[contextIndex] = true
		}
	}
	if len(selected) == 0 {
		for index := len(logs) - 1; index >= 0; index-- {
			message := strings.TrimSpace(logs[index])
			lower := strings.ToLower(message)
			if message == "" ||
				strings.Contains(lower, "entering directory") ||
				strings.Contains(lower, "leaving directory") {
				continue
			}
			return []string{message}
		}
		message := "phase returned non-zero status without an explicit error diagnostic"
		if lastPhase != "" {
			message = "phase " + lastPhase + " returned non-zero status without an explicit error diagnostic"
		}
		return []string{message}
	}
	result := make([]string, 0, min(limit, len(selected)))
	for index, line := range logs {
		if selected[index] {
			result = append(result, line)
			if len(result) == limit {
				break
			}
		}
	}
	return result
}

// applyPortageLifecyclePolicy mirrors doebuild.py's _unsandboxed_phases,
// _ipc_phases and _global_pid_phases for the installed-package lifecycle
// phases Arise currently executes. pkg_pretend is run during preflight;
// pkg_setup is the first independently executed build-side phase.
func applyPortageLifecyclePolicy(request phaseproto.Request, phaseName string) phaseproto.Request {
	request.Policy.DropPrivileges = false
	switch phaseName {
	case "pkg_setup", "pkg_pretend":
		request.Policy.Sandbox = false
		request.Policy.NetworkSandbox = false
		request.Policy.IPCSandbox = false
		// Portage retains pid-sandbox for setup/pretend; they are not in
		// doebuild.py's _global_pid_phases.
		return request
	case "pkg_preinst", "pkg_postinst", "pkg_prerm", "pkg_postrm", "pkg_config":
	case "src_unpack", "src_prepare", "src_configure", "src_compile", "src_test":
		if request.Policy.UserPriv {
			request.Policy.DropPrivileges = true
			// Portage's usersandbox feature controls whether its sandbox is
			// retained for phases that already execute as the portage user.
			request.Policy.Sandbox = request.Policy.UserSandbox
		}
		return request
	default:
		return request
	}
	environment := make(map[string]string, len(request.Env)+1)
	for name, value := range request.Env {
		environment[name] = value
	}
	root := filepath.Clean(request.RootDir)
	if existing := environment["SANDBOX_WRITE"]; existing != "" {
		environment["SANDBOX_WRITE"] = existing + ":" + root
	} else {
		environment["SANDBOX_WRITE"] = root
	}
	request.Env = environment
	// Portage's doebuild._unsandboxed_phases runs setup/pretend and installed
	// lifecycle phases with free=True. SANDBOX_WRITE=/ is not equivalent:
	// the LD_PRELOAD sandbox can still reject capability and other xattr
	// operations required by helpers such as fcaps.eclass.
	request.Policy.Sandbox = false
	request.Policy.NetworkSandbox = false
	request.Policy.IPCSandbox = false
	request.Policy.PIDSandbox = false
	return request
}

func selectedEbuildSlot(cfg *RebuildConfig, eb *ebuild.Ebuild) string {
	if cfg != nil && strings.TrimSpace(cfg.SelectedSlot) != "" {
		return strings.TrimSpace(cfg.SelectedSlot)
	}
	return effectiveEbuildSlot(eb)
}

func protocolBuildPhases(policy phaseproto.ExecutionPolicy) []string {
	phases := []string{"pkg_setup", "src_unpack", "src_prepare", "src_configure", "src_compile"}
	if !policy.Configured || policy.Tests {
		phases = append(phases, "src_test")
	}
	return append(phases, "src_install")
}

func executionFeatureEnabled(features []string, requested string) bool {
	enabled := false
	for _, token := range features {
		name := strings.TrimPrefix(token, "-")
		if name == requested {
			enabled = !strings.HasPrefix(token, "-")
		}
	}
	return enabled
}

func artifactNames(artifacts []distfiles.Artifact) []string {
	names := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		names = append(names, artifact.Name)
	}
	return names
}

// RebuildPackages rebuilds a list of packages, continuing on errors and
// collecting them.
func RebuildPackages(ctx context.Context, atoms []string, cfg *RebuildConfig) error {
	var errs []error

	for _, a := range atoms {
		select {
		case <-ctx.Done():
			errs = append(errs, ctx.Err())
			return joinErrors(errs)
		default:
		}

		if err := RebuildPackage(ctx, a, cfg); err != nil {
			cfg.fireError(a, err)
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return joinErrors(errs)
	}

	return nil
}

// RebuildPackagesParallel rebuilds packages using a worker pool for parallelism.
func RebuildPackagesParallel(ctx context.Context, atoms []string, cfg *RebuildConfig, jobs int) error {
	if jobs <= 0 {
		jobs = 1
	}
	if len(atoms) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Every worker shares the fetcher's own concurrency-safe inflight and file
	// lock coordination. Initialize it before copying/using the configuration.
	cfg.fetcher()
	if cfg.CallbackLock == nil {
		cfg.CallbackLock = &sync.Mutex{}
	}

	atomCh := make(chan string)
	errCh := make(chan error, len(atoms))

	var wg sync.WaitGroup
	for i := 0; i < jobs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for atom := range atomCh {
				select {
				case <-ctx.Done():
					return
				default:
				}
				if err := RebuildPackage(ctx, atom, cfg); err != nil {
					cfg.fireError(atom, err)
					errCh <- err
				}
			}
		}()
	}

	go func() {
		for _, a := range atoms {
			select {
			case <-ctx.Done():
				close(atomCh)
				return
			case atomCh <- a:
			}
		}
		close(atomCh)
	}()

	wg.Wait()
	close(errCh)

	var errs []error
	for e := range errCh {
		errs = append(errs, e)
	}
	if len(errs) > 0 {
		return joinErrors(errs)
	}
	return ctx.Err()
}

// WaitForLoad drops below the given threshold before returning.
// Reads /proc/loadavg on Linux. On other systems, returns immediately.
func WaitForLoad(maxLoad float64) error {
	return WaitForLoadContext(context.Background(), maxLoad)
}

func WaitForLoadContext(ctx context.Context, maxLoad float64) error {
	if maxLoad <= 0 {
		return nil
	}
	return waitForLoad(ctx, maxLoad)
}

// LoadControlContext is a context that carries a load-average threshold.
type LoadControlContext struct {
	context.Context
	MaxLoad float64
}

// WithLoadControl wraps a context with load-average backpressure.
func WithLoadControl(ctx context.Context, maxLoad float64) context.Context {
	if maxLoad <= 0 {
		return ctx
	}
	return &LoadControlContext{
		Context: ctx,
		MaxLoad: maxLoad,
	}
}

// LoadControlFromContext extracts the LoadControlContext from a context, if
// present.
func LoadControlFromContext(ctx context.Context) *LoadControlContext {
	if lc, ok := ctx.(*LoadControlContext); ok {
		return lc
	}
	return nil
}

// Wait checks the load-average from the context and pauses if necessary.
// Call this before each unit of work in a worker pool.
func (lc *LoadControlContext) Wait() error {
	return WaitForLoadContext(lc.Context, lc.MaxLoad)
}

func findEbuild(repoDir, category, pkgName, version string) (string, error) {
	catDir := filepath.Join(repoDir, category, pkgName)
	path := filepath.Join(catDir, pkgName+"-"+version+".ebuild")
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no exact build recipe found at %s", path)
		}
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("build recipe is not a regular file: %s", path)
	}
	return path, nil
}

func resolveURIs(uris []string, vars map[string]string) []string {
	var resolved []string
	for _, uri := range uris {
		r := uri
		for k, v := range vars {
			r = strings.ReplaceAll(r, "${"+k+"}", v)
		}
		resolved = append(resolved, r)
	}
	return resolved
}

func joinErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return fmt.Errorf("rebuild: %d package(s) failed to build:\n%s", len(errs), strings.Join(msgs, "\n"))
}
