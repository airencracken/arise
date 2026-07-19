package rebuild

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/distfiles"
	"github.com/airencracken/arise/internal/ebuild"
	"github.com/airencracken/arise/internal/features"
	"github.com/airencracken/arise/internal/fetch"
	"github.com/airencracken/arise/internal/merge"
	"github.com/airencracken/arise/internal/phase"
	"github.com/airencracken/arise/internal/phaseproto"
	"github.com/airencracken/arise/internal/portage"
	shlex "github.com/anmitsu/go-shlex"
)

// RebuildConfig holds the configuration for rebuilding packages.
type RebuildConfig struct {
	RepoDir      string
	DistfilesDir string
	SourceURI    string // resolved metadata, including eclass-derived SRC_URI
	RootDir      string
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
	GentooMirrors []string
	// PhaseProtocol selects the versioned, eclass-aware Bash execution ABI.
	// Host installation remains gated by the caller's transaction boundary.
	PhaseProtocol        bool
	Repositories         []portage.RepoEntry
	Repository           string
	PortageConfig        *portage.Config
	ConfigRoot           string
	PhaseLogDir          string
	JournalDir           string
	AllowLiveRoot        bool // set only by the state-bound production mutation gate
	AllowLiveReplacement bool // exact same-version canary replacement
	AllowLiveUpgrade     bool // one exact old-version replacement canary
	VDBLockHeld          bool // serial executor owns the operation-wide VDB lock
	SplitLogs            bool
	CompressLogs         bool
	LogFilterCommand     string
	ElogClasses          []string
	ElogSinks            []string
	ElogOutput           io.Writer
	HasVersion           map[string]bool

	OnPhaseStart func(phase string)
	OnPhaseEnd   func(phase string, err error)
	OnError      func(pkg string, err error)

	mu sync.Mutex
}

func (c *RebuildConfig) fetcher() *fetch.Fetcher {
	c.mu.Lock()
	defer c.mu.Unlock()
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

func (c *RebuildConfig) firePhaseStart(phase string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.OnPhaseStart != nil {
		c.OnPhaseStart(phase)
	}
}

func (c *RebuildConfig) firePhaseEnd(phase string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.OnPhaseEnd != nil {
		c.OnPhaseEnd(phase, err)
	}
}

func (c *RebuildConfig) fireError(pkg string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
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
		fetchCfg := fetch.FetchConfig{DistfilesDir: cfg.DistfilesDir, GentooMirrors: cfg.GentooMirrors, MirrorGroups: mirrorGroups}
		var err error
		verified, err = cfg.fetcher().AcquireManifest(ctx, filepath.Join(filepath.Dir(ebuildFile), "Manifest"), srcURI, cfg.UseFlags, fetchCfg)
		if err != nil {
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
	request := phaseproto.Request{Protocol: phaseproto.Version, ID: "preflight", Command: "run_phase", Phase: "pkg_setup", EAPI: eb.EAPI, Ebuild: ebuildFile, Env: map[string]string{"USE": enabledUse(cfg.UseFlags)}}
	sysroot, broot := cfg.dependencyRoots()
	request, err = phaseproto.ApplyPackagePolicy(request, phaseproto.PackagePolicy{
		Configuration: cfg.PortageConfig, Repositories: repositories, Repository: repository,
		ConfigRoot: cfg.ConfigRoot, CPV: a.Category + "/" + p, Category: a.Category, PN: a.Package, P: p, PR: "r0", Slot: effectiveEbuildSlot(eb),
		WorkDir: filepath.Join(cfg.WorkDirBase, "preflight-work"), SourceDir: filepath.Join(cfg.WorkDirBase, "preflight-source"), ImageDir: filepath.Join(cfg.WorkDirBase, "preflight-image"),
		RootDir: cfg.RootDir, SysrootDir: sysroot, BrootDir: broot, TempDir: filepath.Join(cfg.WorkDirBase, "preflight-temp"), HomeDir: filepath.Join(cfg.WorkDirBase, "preflight-home"),
		LogFile: filepath.Join(cfg.PhaseLogDir, "preflight.log"), Restrict: cleanEbuildValue(eb.Vars()["RESTRICT"]), Properties: cleanEbuildValue(eb.Vars()["PROPERTIES"]), Use: cfg.UseFlags,
	})
	if err != nil {
		return fmt.Errorf("rebuild: preflight phase policy: %w", err)
	}
	if request.Policy.UserPriv {
		return fmt.Errorf("rebuild: preflight userpriv is unsupported by the worker")
	}
	if !request.Policy.Fetch && strings.TrimSpace(eb.Vars()["SRC_URI"]) != "" {
		return fmt.Errorf("rebuild: preflight RESTRICT=fetch/pkg_nofetch is unsupported")
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
		rejectLifecycle := func(label string, discovery phaseproto.Request, allowed map[string]bool) error {
			discovery.ID = "live-lifecycle-preflight"
			discovery.Command, discovery.Phase = "discover_phases", ""
			events, err := phaseproto.RunBashWorkerWithOptions(context.Background(), discovery, phaseproto.WorkerOptions{Isolation: phaseproto.IsolationPortage})
			if err != nil {
				return fmt.Errorf("rebuild: discover %s live lifecycle: %w", label, err)
			}
			for _, event := range events {
				if event.Kind == "phase" && strings.HasPrefix(event.Message, "pkg_") {
					if allowed[event.Message] {
						continue
					}
					return fmt.Errorf("rebuild: live canary forbids %s custom lifecycle phase %s", label, event.Message)
				}
			}
			return nil
		}
		if err := rejectLifecycle("new", request, nil); err != nil {
			return err
		}
		if cfg.AllowLiveUpgrade {
			replaced, err := findInstalledReplacement(cfg.VdbDir, a.Category, a.Package, a.Version.Raw, effectiveEbuildSlot(eb))
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
			allowed := make(map[string]bool)
			if inheritsEclass(old, "xorg-3") && cleanEbuildValue(old.Vars()["FONT"]) == "" {
				// xorg-3 exports these hooks for every consumer, but their bodies
				// perform live writes only for ebuilds that set FONT. The selected
				// ebuild and eclass sources are covered by the approved state hash.
				allowed["pkg_postinst"], allowed["pkg_postrm"] = true, true
			}
			if err := rejectLifecycle("old", oldRequest, allowed); err != nil {
				return err
			}
		}
	}
	return nil
}

func inheritsEclass(eb *ebuild.Ebuild, name string) bool {
	for _, inherited := range eb.Inherit {
		if inherited == name {
			return true
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

func rebuildWithPhaseProtocol(ctx context.Context, atomStr string, eb *ebuild.Ebuild, ebuildFile, workDir, destDir string, verified distfiles.VerifiedSet, cfg *RebuildConfig) (returnErr error) {
	a, err := atom.Parse(atomStr)
	if err != nil || a.Version == nil {
		return fmt.Errorf("rebuild: protocol package identity: %w", err)
	}
	cat, pn, version := a.Category, a.Package, a.Version.Raw
	p := pn + "-" + version
	repository := cfg.Repository
	repositories := append([]portage.RepoEntry(nil), cfg.Repositories...)
	if repository == "" {
		repository = "selected"
	}
	if len(repositories) == 0 {
		repositories = []portage.RepoEntry{{Name: repository, Location: cfg.RepoDir}}
	}
	sourceDir := filepath.Join(workDir, p)
	for _, directory := range []string{sourceDir, destDir, filepath.Join(workDir, "temp"), filepath.Join(workDir, "home")} {
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
		packageLog, err = phaseproto.NewPackageLog(phaseproto.PackageLogOptions{Root: cfg.PhaseLogDir, TempDir: filepath.Join(workDir, "temp"), Category: cat, PF: p, Split: cfg.SplitLogs, FilterCommand: filterCommand})
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
	base := phaseproto.Request{
		Protocol: phaseproto.Version, ID: "policy-preflight", Command: "run_phase", Phase: "pkg_setup", EAPI: eb.EAPI, Ebuild: ebuildFile,
		Env: map[string]string{
			"USE": strings.Join(use, " "), "A": strings.Join(artifacts, " "),
			"CFLAGS": cfg.CFLAGS, "CXXFLAGS": cfg.CXXFLAGS, "LDFLAGS": cfg.LDFLAGS,
			"MAKEOPTS": cfg.MAKEOPTS, "ARCH": cfg.Arch,
		},
	}
	if cfg.PortageConfig != nil {
		for _, name := range []string{"CHOST", "CBUILD", "CTARGET", "ABI", "DEFAULT_ABI", "MULTILIB_ABIS"} {
			if value := cfg.PortageConfig.MakeConf[name]; value != "" {
				base.Env[name] = value
			}
		}
	}
	if base.Env["DEFAULT_ABI"] == "" && cfg.Arch == "amd64" {
		base.Env["DEFAULT_ABI"] = "amd64"
	}
	base.HasVersion = cfg.HasVersion
	if len(verified.Artifacts) != 0 {
		base.Distfiles = &verified
	}
	sysroot, broot := cfg.dependencyRoots()
	base, err = phaseproto.ApplyPackagePolicy(base, phaseproto.PackagePolicy{
		Configuration: cfg.PortageConfig, Repositories: repositories, Repository: repository,
		ConfigRoot: cfg.ConfigRoot, CPV: cat + "/" + p, Category: cat, PN: pn, P: p, PR: "r0",
		Slot: effectiveEbuildSlot(eb), WorkDir: workDir, SourceDir: sourceDir, ImageDir: destDir,
		RootDir: cfg.RootDir, SysrootDir: sysroot, BrootDir: broot,
		TempDir: filepath.Join(workDir, "temp"), HomeDir: filepath.Join(workDir, "home"),
		Restrict: cleanEbuildValue(eb.Vars()["RESTRICT"]), Properties: cleanEbuildValue(eb.Vars()["PROPERTIES"]), Use: cfg.UseFlags,
	})
	if packageLog != nil {
		base.LogFile = packageLog.Path()
	}
	if err != nil {
		return fmt.Errorf("rebuild: phase protocol policy: %w", err)
	}
	var packageEvents []phaseproto.Event
	run := func(phaseName string) error {
		request := base
		request.ID = strings.NewReplacer("/", "-", ".", "-").Replace(cat + "-" + p + "-" + phaseName)
		request.Command, request.Phase = "run_phase", phaseName
		cfg.firePhaseStart(phaseName)
		events, phaseErr := phaseproto.RunBashWorkerWithOptions(ctx, request, phaseproto.WorkerOptions{Isolation: phaseproto.IsolationPortage, DurableLog: packageLog})
		packageEvents = append(packageEvents, events...)
		cfg.firePhaseEnd(phaseName, phaseErr)
		if phaseErr != nil {
			var logs []string
			for _, event := range events {
				if event.Kind == "log" && event.Message != "" {
					logs = append(logs, event.Message)
				}
			}
			if len(logs) != 0 {
				return fmt.Errorf("rebuild: phase protocol %s: %w: %s", phaseName, phaseErr, strings.Join(logs, "\n"))
			}
			return fmt.Errorf("rebuild: phase protocol %s: %w", phaseName, phaseErr)
		}
		return nil
	}
	buildPhases := protocolBuildPhases(base.Policy)
	batch := base
	batch.ID = strings.NewReplacer("/", "-", ".", "-").Replace(cat + "-" + p + "-build")
	batch.Command, batch.Phase, batch.Phases = "run_phases", "", append([]string(nil), buildPhases...)
	for _, phaseName := range buildPhases {
		cfg.firePhaseStart(phaseName)
	}
	batchEvents, batchErr := phaseproto.RunBashWorkerWithOptions(ctx, batch, phaseproto.WorkerOptions{Isolation: phaseproto.IsolationPortage, DurableLog: packageLog})
	packageEvents = append(packageEvents, batchEvents...)
	for _, phaseName := range buildPhases {
		cfg.firePhaseEnd(phaseName, batchErr)
	}
	if batchErr != nil {
		var logs []string
		for _, event := range batchEvents {
			if event.Kind == "log" && event.Message != "" {
				logs = append(logs, event.Message)
			}
		}
		if len(logs) != 0 {
			return fmt.Errorf("rebuild: phase protocol build sequence: %w: %s", batchErr, strings.Join(logs, "\n"))
		}
		return fmt.Errorf("rebuild: phase protocol build sequence: %w", batchErr)
	}
	journalDir := cfg.JournalDir
	if journalDir == "" {
		journalDir = filepath.Join(workDir, "journal")
	}
	mergeCfg := merge.MergeConfig{
		RootDir: cfg.RootDir, VdbDir: cfg.VdbDir, Category: cat, Package: pn, Version: version,
		JournalDir:           journalDir,
		AllowLiveRoot:        cfg.AllowLiveRoot,
		AllowLiveReplacement: cfg.AllowLiveReplacement,
		VDBLockHeld:          cfg.VDBLockHeld,
		VDBMetadata:          protocolVDBMetadata(eb, ebuildFile, cat, p, base),
		Environment:          protocolEnvironmentSnapshot(base),
		// The terminal install lifecycle must succeed before the filesystem/VDB
		// transaction is committed. Live ROOT is still gated because arbitrary
		// pkg_postinst writes need write-set capture in addition to this ordering.
		BeforeCommit: func() error { return run("pkg_postinst") },
	}
	if cfg.AllowLiveUpgrade {
		replaced, err := findInstalledReplacement(cfg.VdbDir, cat, pn, version, effectiveEbuildSlot(eb))
		if err != nil {
			return fmt.Errorf("rebuild: select installed replacement: %w", err)
		}
		mergeCfg.ReplacedVDBPath = replaced
	}
	if cfg.PortageConfig != nil {
		mergeCfg.ConfigProtect = strings.Fields(cfg.PortageConfig.MakeConf["CONFIG_PROTECT"])
		mergeCfg.ConfigProtectMask = strings.Fields(cfg.PortageConfig.MakeConf["CONFIG_PROTECT_MASK"])
	}
	if err := merge.Merge(ctx, destDir, mergeCfg); err != nil {
		return fmt.Errorf("rebuild: protocol merge: %w", err)
	}
	if len(cfg.ElogSinks) != 0 {
		if _, err := phaseproto.DeliverElog(packageEvents, phaseproto.ElogOptions{LogDir: cfg.PhaseLogDir, Category: cat, PF: p, Classes: cfg.ElogClasses, Sinks: cfg.ElogSinks, Output: cfg.ElogOutput}); err != nil {
			return fmt.Errorf("rebuild: elog delivery: %w", err)
		}
	}
	return nil
}

func findInstalledReplacement(vdbDir, category, packageName, newVersion, slot string) (string, error) {
	directory := filepath.Join(vdbDir, category)
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), packageName+"-") || entry.Name() == packageName+"-"+newVersion {
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
	values := make(map[string]string, len(request.Env)+16)
	for name, value := range request.Env {
		values[name] = value
	}
	identity := map[string]string{
		"CATEGORY": request.Package.Category, "PN": request.Package.PN, "PV": request.Package.PV,
		"PR": request.Package.PR, "P": request.Package.P, "PVR": request.Package.PVR,
		"PF": request.Package.PF, "SLOT": request.Package.Slot, "PORTAGE_REPO_NAME": request.Package.Repository,
		"EAPI": request.EAPI, "ROOT": request.RootDir, "SYSROOT": request.SysrootDir, "BROOT": request.BrootDir,
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

func protocolVDBMetadata(eb *ebuild.Ebuild, ebuildFile, category, pf string, request phaseproto.Request) map[string]string {
	vars := eb.Vars()
	metadata := map[string]string{
		"CATEGORY": category, "PF": pf, "EAPI": eb.EAPI,
		"SLOT":       effectiveEbuildSlot(eb),
		"repository": request.Package.Repository,
		"USE":        request.Env["USE"],
	}
	for _, name := range []string{"DEPEND", "RDEPEND", "BDEPEND", "IDEPEND", "PDEPEND", "IUSE", "REQUIRED_USE", "LICENSE", "PROPERTIES", "RESTRICT", "DEFINED_PHASES", "INHERITED"} {
		metadata[name] = strings.Trim(vars[name], "\"'")
	}
	if data, err := os.ReadFile(ebuildFile); err == nil {
		metadata[pf+".ebuild"] = string(data)
	}
	return metadata
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

func protocolBuildPhases(policy phaseproto.ExecutionPolicy) []string {
	phases := []string{"pkg_setup", "src_unpack", "src_prepare", "src_configure", "src_compile"}
	if !policy.Configured || policy.Tests {
		phases = append(phases, "src_test")
	}
	return append(phases, "src_install", "pkg_preinst")
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
	if maxLoad <= 0 {
		return nil
	}
	return waitForLoad(maxLoad)
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
	return WaitForLoad(lc.MaxLoad)
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
