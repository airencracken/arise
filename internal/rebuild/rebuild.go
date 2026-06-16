package rebuild

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/ebuild"
	"github.com/airencracken/arise/internal/features"
	"github.com/airencracken/arise/internal/fetch"
	"github.com/airencracken/arise/internal/merge"
	"github.com/airencracken/arise/internal/phase"
)

// RebuildConfig holds the configuration for rebuilding packages.
type RebuildConfig struct {
	RepoDir      string
	DistfilesDir string
	RootDir      string
	VdbDir       string
	WorkDirBase  string
	CFLAGS       string
	CXXFLAGS     string
	LDFLAGS      string
	MAKEOPTS     string
	Arch         string
	Features     *features.Config

	OnPhaseStart func(phase string)
	OnPhaseEnd   func(phase string, err error)
	OnError      func(pkg string, err error)

	mu sync.Mutex
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
	resolvedURIs := resolveURIs(eb.SourceURIList(), vars)

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

	if len(resolvedURIs) > 0 {
		fetchCfg := fetch.FetchConfig{
			Destination:  workDir,
			DistfilesDir: cfg.DistfilesDir,
		}
		if _, err := fetch.Fetch(ctx, resolvedURIs, fetchCfg); err != nil {
			cfg.fireError(atomStr, fmt.Errorf("rebuild: failed to fetch source files: %w", err))
			return fmt.Errorf("rebuild: could not download source files: %w", err)
		}
	}

	phaseCfg := phase.PhaseConfig{
		DESTDIR:          destDir,
		WorkDir:          workDir,
		Sourcedir:        workDir,
		CFLAGS:           cfg.CFLAGS,
		CXXFLAGS:         cfg.CXXFLAGS,
		LDFLAGS:          cfg.LDFLAGS,
		MAKEOPTS:         cfg.MAKEOPTS,
		PN:               pkg,
		PV:               ver,
		CATEGORY:         cat,
		EBUILD_PATH:      ebuildFile,
		Features:         cfg.Features,
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
	entries, err := os.ReadDir(catDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no build recipe directory found at %s", catDir)
		}
		return "", err
	}

	prefix := pkgName + "-" + version
	var matches []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".ebuild") {
			matches = append(matches, name)
		}
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("no build recipe found for %s-%s in %s", pkgName, version, catDir)
	}

	if len(matches) > 1 {
		return matches[0], nil
	}

	return filepath.Join(catDir, matches[0]), nil
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
