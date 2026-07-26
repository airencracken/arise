package rebuild

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/ebuild"
	"github.com/airencracken/arise/internal/phaseproto"
	"github.com/airencracken/arise/internal/portage"
)

// InstalledLifecycle executes package hooks from the environment persisted in
// an installed VDB entry. It deliberately does not re-source current eclasses.
type InstalledLifecycle struct {
	request phaseproto.Request
	log     *phaseproto.PackageLog
	workDir string
	cfg     *RebuildConfig
	phases  map[string]bool
}

func OpenInstalledLifecycle(vdbPath string, cfg *RebuildConfig) (*InstalledLifecycle, error) {
	if cfg == nil || !filepath.IsAbs(vdbPath) || cfg.WorkDirBase == "" || cfg.PhaseLogDir == "" {
		return nil, fmt.Errorf("installed lifecycle: absolute VDB, work and log paths are required")
	}
	storedEbuilds, err := filepath.Glob(filepath.Join(vdbPath, "*.ebuild"))
	if err != nil || len(storedEbuilds) != 1 {
		return nil, fmt.Errorf("installed lifecycle: expected one stored ebuild, found %d", len(storedEbuilds))
	}
	category := filepath.Base(filepath.Dir(vdbPath))
	pf := strings.TrimSuffix(filepath.Base(storedEbuilds[0]), ".ebuild")
	a, err := atom.Parse(category + "/" + pf)
	if err != nil || a.Version == nil {
		return nil, fmt.Errorf("installed lifecycle: package identity: %w", err)
	}
	eb, err := ebuild.ParseEbuild(storedEbuilds[0])
	if err != nil {
		return nil, fmt.Errorf("installed lifecycle: stored ebuild: %w", err)
	}
	if err := ensureWorkDirectory(cfg.WorkDirBase); err != nil {
		return nil, fmt.Errorf("installed lifecycle: %w", err)
	}
	workDir, err := os.MkdirTemp(cfg.WorkDirBase, category+"-"+a.Package+"-remove-")
	if err != nil {
		return nil, fmt.Errorf("installed lifecycle: work directory: %w", err)
	}
	fail := func(cause error) (*InstalledLifecycle, error) {
		_ = os.RemoveAll(workDir)
		return nil, cause
	}
	for _, directory := range []string{filepath.Join(workDir, "work"), filepath.Join(workDir, "temp"), filepath.Join(workDir, "home")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fail(err)
		}
	}
	environment, err := materializeInstalledEnvironment(vdbPath, workDir)
	if err != nil {
		return fail(fmt.Errorf("installed lifecycle: environment: %w", err))
	}
	repository := strings.TrimSpace(readInstalledValue(vdbPath, "repository"))
	if repository == "" {
		repository = "installed"
	}
	slot := strings.TrimSpace(readInstalledValue(vdbPath, "SLOT"))
	if slot == "" {
		slot = "0"
	}
	repositories := append([]portage.RepoEntry(nil), cfg.Repositories...)
	if len(repositories) == 0 {
		repositories = []portage.RepoEntry{{Name: repository, Location: cfg.RepoDir}}
	}
	request := phaseproto.Request{Protocol: phaseproto.Version, ID: "installed-lifecycle", Command: "run_phase", Phase: "pkg_prerm", EAPI: eb.EAPI, Ebuild: storedEbuilds[0], Environment: environment,
		Env: map[string]string{"USE": strings.TrimSpace(readInstalledValue(vdbPath, "USE"))}, WorkDir: filepath.Join(workDir, "work"), BuildDir: filepath.Join(workDir, "work"),
		RootDir: cfg.RootDir, SysrootDir: cfg.SysrootDir, BrootDir: phaseBroot(cfg.BrootDir), TempDir: filepath.Join(workDir, "temp"), HomeDir: filepath.Join(workDir, "home")}
	request, err = phaseproto.ApplyPackagePolicy(request, phaseproto.PackagePolicy{Configuration: cfg.PortageConfig, Repositories: repositories, Repository: repository,
		ConfigRoot: cfg.ConfigRoot, CPV: category + "/" + pf, Category: category, PN: a.Package, P: pf, PR: "r0", Slot: slot,
		WorkDir: request.WorkDir, BuildDir: request.BuildDir, RootDir: cfg.RootDir, SysrootDir: cfg.SysrootDir, BrootDir: phaseBroot(cfg.BrootDir), TempDir: request.TempDir, HomeDir: request.HomeDir})
	if err != nil {
		return fail(fmt.Errorf("installed lifecycle: policy: %w", err))
	}
	request.HasVersion, err = preflightHasVersionQueries(environment, eb, repositories, repository, cfg.VdbDir, cfg.VdbDir, cfg.HasVersion)
	if err != nil {
		return fail(fmt.Errorf("installed lifecycle: has_version queries: %w", err))
	}
	request.BestVersion = preflightBestVersions(cfg.VdbDir, cfg.VdbDir)
	log, err := phaseproto.NewPackageLog(phaseproto.PackageLogOptions{Root: cfg.PhaseLogDir, TempDir: request.TempDir, Category: category, PF: pf, Split: cfg.SplitLogs})
	if err != nil {
		return fail(fmt.Errorf("installed lifecycle: log: %w", err))
	}
	request.LogFile = log.Path()
	definedPhases := strings.Fields(readInstalledValue(vdbPath, "DEFINED_PHASES"))
	phases := make(map[string]bool, len(definedPhases))
	for _, phase := range definedPhases {
		phases[phase] = true
	}
	// Old or synthetic VDB records may predate DEFINED_PHASES. Direct
	// definitions in the stored ebuild remain a safe compatibility fallback.
	if len(phases) == 0 {
		for phase := range eb.RawPhases {
			phases[phase] = true
		}
	}
	return &InstalledLifecycle{request: request, log: log, workDir: workDir, cfg: cfg, phases: phases}, nil
}

func (l *InstalledLifecycle) HasPhase(phase string) bool {
	return l != nil && l.phases[phase]
}

func (l *InstalledLifecycle) Run(ctx context.Context, phase string) error {
	request := l.request
	request.ID = strings.NewReplacer("/", "-", ".", "-").Replace(request.Package.Category + "-" + request.Package.PF + "-" + phase)
	request.Phase = phase
	request = applyPortageLifecyclePolicy(request, phase)
	l.cfg.firePhaseStart(phase)
	events, err := runPhaseWorker(ctx, request, l.cfg, phaseproto.WorkerOptions{Isolation: phaseproto.IsolationPortage, DurableLog: l.log})
	l.cfg.firePhaseEnd(phase, err)
	if err != nil {
		var details []string
		for _, event := range events {
			if event.Kind == "log" && event.Message != "" {
				details = append(details, event.Message)
			}
		}
		return fmt.Errorf("installed lifecycle %s: %w%s", phase, err, lifecycleDetails(details))
	}
	return nil
}

func lifecycleDetails(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return ": " + strings.Join(lines, "\n")
}

func (l *InstalledLifecycle) Close() error {
	logErr := l.log.Finalize(l.cfg.CompressLogs)
	removeErr := os.RemoveAll(l.workDir)
	if logErr != nil {
		return logErr
	}
	return removeErr
}

func readInstalledValue(vdbPath, name string) string {
	data, _ := os.ReadFile(filepath.Join(vdbPath, name))
	return string(data)
}
