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
	SelectedSlot         string // exact resolver-selected slot/subslot metadata
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
	OnError      func(pkg string, err error)
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

func (c *RebuildConfig) firePhaseStart(phase string) {
	if c.CallbackLock != nil {
		c.CallbackLock.Lock()
		defer c.CallbackLock.Unlock()
	}
	if c.OnPhaseStart != nil {
		c.OnPhaseStart(phase)
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
		restrict, policyErr := phaseproto.EvaluatePolicyExpression(cleanEbuildValue(eb.Vars()["RESTRICT"]), cfg.UseFlags)
		if policyErr != nil {
			return fmt.Errorf("rebuild: evaluate RESTRICT for fetch: %w", policyErr)
		}
		for _, name := range restrict {
			switch name {
			case "mirror":
				fetchCfg.RestrictMirrors = true
			case "primaryuri":
				fetchCfg.PrimaryURI = true
			}
		}
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
	for _, directory := range []string{preflightWork, preflightSource, preflightImage, preflightTemp, preflightHome} {
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
	request := phaseproto.Request{Protocol: phaseproto.Version, ID: "preflight", Command: "run_phase", Phase: "pkg_setup", EAPI: eb.EAPI, Ebuild: ebuildFile, Env: map[string]string{"USE": enabledUse(cfg.UseFlags)}}
	_, preflightBroot := cfg.dependencyRoots()
	request.HasVersion, err = preflightHasVersionQueries(ebuildFile, eb, repositories, repository, cfg.VdbDir, filepath.Join(preflightBroot, "var", "db", "pkg"), cfg.HasVersion)
	if err != nil {
		return fmt.Errorf("rebuild: preflight has_version queries: %w", err)
	}
	sysroot, broot := cfg.dependencyRoots()
	request, err = phaseproto.ApplyPackagePolicy(request, phaseproto.PackagePolicy{
		Configuration: cfg.PortageConfig, Repositories: repositories, Repository: repository,
		ConfigRoot: cfg.ConfigRoot, CPV: a.Category + "/" + p, Category: a.Category, PN: a.Package, P: p, PR: "r0", Slot: selectedEbuildSlot(cfg, eb),
		WorkDir: preflightWork, SourceDir: preflightSource, ImageDir: preflightImage,
		RootDir: cfg.RootDir, SysrootDir: sysroot, BrootDir: broot, TempDir: preflightTemp, HomeDir: preflightHome,
		Restrict: cleanEbuildValue(eb.Vars()["RESTRICT"]), Properties: cleanEbuildValue(eb.Vars()["PROPERTIES"]), Use: cfg.UseFlags,
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
		if _, err := exec.LookPath("bwrap"); err != nil {
			return fmt.Errorf("rebuild: live lifecycle isolation requires bubblewrap: %w", err)
		}
		// The initial live lane permits only packages whose sourced ebuild/eclass
		// closure defines no package lifecycle hooks. Their defaults are no-ops,
		// leaving the image and VDB as the complete mutable write set captured by
		// the journal. General lifecycle write capture remains a broader gate.
		rejectLifecycle := func(label string, discovery phaseproto.Request, relevant, allowed map[string]bool) error {
			discovery.ID = "live-lifecycle-preflight"
			discovery.Command, discovery.Phase = "discover_phases", ""
			events, err := phaseproto.RunBashWorkerWithOptions(context.Background(), discovery, phaseproto.WorkerOptions{Isolation: phaseproto.IsolationBubblewrap})
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
		newAllowed := make(map[string]bool)
		// Live setup and preinstall run with ROOT mounted read-only while the
		// work tree and staged image remain writable. They therefore cannot
		// escape the journal by changing the host filesystem.
		newAllowed["pkg_setup"], newAllowed["pkg_preinst"], newAllowed["pkg_postinst"] = true, true, true
		if inheritsEclass(eb, "python-any-r1") || inheritsEclass(eb, "python-single-r1") || inheritsEclass(eb, "python-r1") {
			// These eclasses export pkg_setup solely to select and validate the
			// build-time interpreter before compilation; they do not mutate ROOT.
			newAllowed["pkg_setup"] = true
		}
		for _, phaseName := range []string{"pkg_preinst", "pkg_postinst", "pkg_prerm", "pkg_postrm"} {
			if advisoryLifecycleOnly(ebuildFile, phaseName) || lifecycleSafeWithExplicitROOT(ebuildFile, phaseName) {
				newAllowed[phaseName] = true
			}
		}
		for _, phaseName := range []string{"pkg_pretend", "pkg_setup", "pkg_preinst", "pkg_postinst", "pkg_prerm", "pkg_postrm"} {
			if lifecycleOnlyDisabledUseGuards(ebuildFile, phaseName, cfg.UseFlags) {
				newAllowed[phaseName] = true
			}
		}
		if inheritsEclass(eb, "xdg") {
			// Provisional only: the completed image is rejected before merge if
			// it contains payload that could make these hooks write live caches.
			newAllowed["pkg_preinst"], newAllowed["pkg_postinst"], newAllowed["pkg_postrm"] = true, true, true
		}
		newRelevant := map[string]bool{"pkg_setup": true, "pkg_preinst": true, "pkg_postinst": true}
		if err := rejectLifecycle("new", request, newRelevant, newAllowed); err != nil {
			return err
		}
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

func hasXDGPayloadInImage(image string) bool {
	for _, prefix := range xdgPayloadPrefixes {
		found := false
		path := filepath.Join(image, filepath.FromSlash(strings.TrimSuffix(prefix, "/")))
		_ = filepath.WalkDir(path, func(_ string, entry os.DirEntry, err error) error {
			if err == nil && !entry.IsDir() {
				found = true
				return filepath.SkipAll
			}
			return nil
		})
		if found {
			return true
		}
	}
	return false
}

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

func phaseRequestEnvironment(cfg *RebuildConfig, use, artifacts string) map[string]string {
	result := map[string]string{"USE": use, "A": artifacts}
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
	requestEnv := phaseRequestEnvironment(cfg, strings.Join(use, " "), strings.Join(artifacts, " "))
	base := phaseproto.Request{
		Protocol: phaseproto.Version, ID: "policy-preflight", Command: "run_phase", Phase: "pkg_setup", EAPI: eb.EAPI, Ebuild: ebuildFile,
		Env: requestEnv,
	}
	_, queryBroot := cfg.dependencyRoots()
	base.HasVersion, err = preflightHasVersionQueries(ebuildFile, eb, repositories, repository, cfg.VdbDir, filepath.Join(queryBroot, "var", "db", "pkg"), cfg.HasVersion)
	if err != nil {
		return fmt.Errorf("rebuild: preflight has_version queries: %w", err)
	}
	if len(verified.Artifacts) != 0 {
		base.Distfiles = &verified
	}
	sysroot, broot := cfg.dependencyRoots()
	base, err = phaseproto.ApplyPackagePolicy(base, phaseproto.PackagePolicy{
		Configuration: cfg.PortageConfig, Repositories: repositories, Repository: repository,
		ConfigRoot: cfg.ConfigRoot, CPV: cat + "/" + p, Category: cat, PN: pn, P: p, PR: "r0",
		Slot: selectedEbuildSlot(cfg, eb), WorkDir: workDir, BuildDir: workDir, SourceDir: sourceDir, ImageDir: destDir,
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
	if base.Env["DEFAULT_ABI"] == "" && cfg.Arch == "amd64" {
		base.Env["DEFAULT_ABI"] = "amd64"
	}
	var packageEvents []phaseproto.Event
	run := func(phaseName string) error {
		if strings.HasPrefix(phaseName, "pkg_") && lifecycleOnlyDisabledUseGuards(ebuildFile, phaseName, cfg.UseFlags) {
			return nil
		}
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
		replacedVDB, err = findInstalledReplacement(cfg.VdbDir, cat, pn, version, selectedEbuildSlot(cfg, eb))
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
			request.ID = strings.NewReplacer("/", "-", ".", "-").Replace(cat + "-" + p + "-old-" + phaseName)
			request.Command, request.Phase = "run_phase", phaseName
			cfg.firePhaseStart(phaseName)
			events, phaseErr := phaseproto.RunBashWorkerWithOptions(ctx, request, phaseproto.WorkerOptions{Isolation: phaseproto.IsolationPortage, DurableLog: packageLog})
			packageEvents = append(packageEvents, events...)
			cfg.firePhaseEnd(phaseName, phaseErr)
			if phaseErr != nil {
				return fmt.Errorf("old %s: %w", phaseName, phaseErr)
			}
			return nil
		}
	}
	batch := base
	batch.ID = strings.NewReplacer("/", "-", ".", "-").Replace(cat + "-" + p + "-build")
	batch.Command, batch.Phase, batch.Phases = "run_phases", "", append([]string(nil), buildPhases...)
	batch.EmitMetadata = true
	for _, phaseName := range buildPhases {
		cfg.firePhaseStart(phaseName)
	}
	buildIsolation := phaseproto.IsolationPortage
	if cfg.AllowLiveRoot {
		buildIsolation = phaseproto.IsolationBubblewrap
	}
	batchEvents, batchErr := phaseproto.RunBashWorkerWithOptions(ctx, batch, phaseproto.WorkerOptions{Isolation: buildIsolation, DurableLog: packageLog})
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
	if cfg.CommitLock != nil {
		cfg.CommitLock.Lock()
		defer cfg.CommitLock.Unlock()
	}
	if cfg.AllowLiveRoot && inheritsEclass(eb, "xdg") && hasXDGPayloadInImage(destDir) {
		return fmt.Errorf("rebuild: live canary forbids xdg lifecycle cache writes for an image containing desktop, icon, or MIME payload")
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
		VDBMetadata:          protocolVDBMetadata(eb, ebuildFile, cat, p, base, batchEvents),
		Environment:          protocolEnvironmentSnapshot(base),
		// Portage runs pkg_postinst after payload/VDB commit and retains the
		// installed package if the hook fails. Arise journals its own merge first,
		// then preserves the same committed-state lifecycle semantics.
		AfterCommit: func() error { return run("pkg_postinst") },
	}
	var oldLifecycleErrors []error
	if runOld != nil {
		mergeCfg.BeforeReplacementRemoval = func() error {
			if hookErr := runOld("pkg_prerm"); hookErr != nil {
				oldLifecycleErrors = append(oldLifecycleErrors, hookErr)
			}
			return nil
		}
		mergeCfg.AfterReplacementRemoval = func() error {
			if hookErr := runOld("pkg_postrm"); hookErr != nil {
				oldLifecycleErrors = append(oldLifecycleErrors, hookErr)
			}
			return nil
		}
		mergeCfg.AfterCommit = func() error {
			postinstErr := run("pkg_postinst")
			return errors.Join(append(oldLifecycleErrors, postinstErr)...)
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

var staticHasVersionQuery = regexp.MustCompile(`\bhas_version[[:space:]]+(?:-([bdr])[[:space:]]+)?(?:"([^"$[:space:]]+)"|'([^'$[:space:]]+)'|([^"'$[:space:];]+))`)

func preflightHasVersionQueries(ebuildFile string, eb *ebuild.Ebuild, repositories []portage.RepoEntry, repository, rootVDB, brootVDB string, configured map[string]bool) (map[string]bool, error) {
	result := make(map[string]bool, len(configured))
	for query, answer := range configured {
		result[query] = answer
	}
	files := []string{ebuildFile}
	eclassDirs, err := portage.EclassLookupDirectories(repositories, repository)
	if err != nil {
		return nil, err
	}
	for _, name := range eb.Inherit {
		for _, directory := range eclassDirs {
			candidate := filepath.Join(directory, name+".eclass")
			if _, err := os.Stat(candidate); err == nil {
				files = append(files, candidate)
				break
			}
		}
	}
	queries := make(map[string]string)
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
	}
	for query, domain := range queries {
		if _, exists := result[query]; exists {
			continue
		}
		vdb := rootVDB
		if domain == "b" {
			vdb = brootVDB
		}
		result[query] = installedAtomMatch(vdb, query)
	}
	// vala.eclass constructs its build-host probe from the installed API slot
	// and VALA_USE_DEPEND inside a loop, so it cannot appear as a static quoted
	// atom. Expand that finite input set during preflight instead of granting the
	// worker general VDB access.
	if slices.Contains(eb.Inherit, "vala") {
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
				result[query] = installedAtomMatch(brootVDB, query)
			}
		}
	}
	pythonEclass := false
	for _, inherited := range eb.Inherit {
		if strings.HasPrefix(inherited, "python-") {
			pythonEclass = true
			break
		}
	}
	if pythonEclass {
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
			result[query] = installedAtomMatch(brootVDB, query)
		}
	}
	if slices.Contains(eb.Inherit, "autotools") {
		for _, path := range files {
			if filepath.Base(path) != "autotools.eclass" {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			for _, query := range dynamicAutotoolsQueries(string(data)) {
				result[query] = installedAtomMatch(brootVDB, query)
			}
		}
	}
	return result, nil
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
	categories, err := os.ReadDir(vdbDir)
	if err != nil {
		return false
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
			path := filepath.Join(vdbDir, category.Name(), pkg.Name())
			slot, _ := os.ReadFile(filepath.Join(path, "SLOT"))
			repo, _ := os.ReadFile(filepath.Join(path, "repository"))
			if portage.PackageAtomMatches(query, category.Name()+"/"+pkg.Name(), strings.TrimSpace(string(slot)), strings.TrimSpace(string(repo))) {
				return true
			}
		}
	}
	return false
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

func protocolVDBMetadata(eb *ebuild.Ebuild, ebuildFile, category, pf string, request phaseproto.Request, events []phaseproto.Event) map[string]string {
	vars := eb.Vars()
	metadata := map[string]string{
		"CATEGORY": category, "PF": pf, "EAPI": eb.EAPI,
		"SLOT":       request.Package.Slot,
		"repository": request.Package.Repository,
		"USE":        request.Env["USE"],
	}
	for _, name := range []string{"DEPEND", "RDEPEND", "BDEPEND", "IDEPEND", "PDEPEND", "IUSE", "REQUIRED_USE", "LICENSE", "PROPERTIES", "RESTRICT", "DEFINED_PHASES", "INHERITED"} {
		metadata[name] = strings.Trim(vars[name], "\"'")
	}
	for _, event := range events {
		if event.Kind == "metadata" {
			metadata[event.Class] = event.Message
		}
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
