// Package phaseproto defines the versioned, line-delimited JSON contract
// between Arise's Go control plane and an isolated Bash ebuild worker.
package phaseproto

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/airencracken/arise/internal/distfiles"
)

const Version = 1

type IsolationMode string

const (
	IsolationPortage    IsolationMode = "portage"
	IsolationBubblewrap IsolationMode = "bubblewrap"
)

type WorkerOptions struct {
	Isolation   IsolationMode
	Namespaces  NamespaceOptions
	Diagnostics io.Writer
	DurableLog  *PackageLog
	FinalizeLog bool
	CompressLog bool
}

// NamespaceOptions mirrors the independent namespace controls used by
// Portage. User namespaces are intentionally absent: the recovery-compatible
// backend must work when unprivileged user namespaces are disabled.
type NamespaceOptions struct {
	Network bool
	IPC     bool
	Mount   bool
	PID     bool
}

type namespaceSpec struct {
	name      string
	requested bool
	arguments []string
}

var safeToken = regexp.MustCompile(`^[A-Za-z0-9_.:+-]+$`)
var safePackageIdentity = regexp.MustCompile(`^[A-Za-z0-9_.:+/-]+$`)

type Request struct {
	Protocol      int                    `json:"protocol"`
	ID            string                 `json:"id"`
	Command       string                 `json:"command"`
	Phase         string                 `json:"phase"`
	Phases        []string               `json:"phases,omitempty"`
	EAPI          string                 `json:"eapi"`
	Ebuild        string                 `json:"ebuild"`
	Environment   string                 `json:"environment,omitempty"` // decompressed installed VDB environment
	Env           map[string]string      `json:"env"`
	EclassDirs    []string               `json:"eclass_dirs,omitempty"`
	UserPatchDirs []string               `json:"user_patch_dirs,omitempty"`
	WorkDir       string                 `json:"work_dir,omitempty"`
	BuildDir      string                 `json:"build_dir,omitempty"`
	ConfigRoot    string                 `json:"config_root,omitempty"`
	SourceDir     string                 `json:"source_dir,omitempty"`
	ImageDir      string                 `json:"image_dir,omitempty"`
	RootDir       string                 `json:"root_dir,omitempty"`
	SysrootDir    string                 `json:"sysroot_dir,omitempty"`
	BrootDir      string                 `json:"broot_dir,omitempty"`
	TempDir       string                 `json:"temp_dir,omitempty"`
	HomeDir       string                 `json:"home_dir,omitempty"`
	LogFile       string                 `json:"log_file,omitempty"`
	Package       PackageIdentity        `json:"package,omitempty"`
	Policy        ExecutionPolicy        `json:"policy,omitempty"`
	Distfiles     *distfiles.VerifiedSet `json:"-"`
	HasVersion    map[string]bool        `json:"-"`
	EmitMetadata  bool                   `json:"emit_metadata,omitempty"`
}

// PackageIdentity contains the PMS package variables owned by the execution
// protocol. Keeping them typed prevents package.env from changing the identity
// of the ebuild selected by the resolver.
type PackageIdentity struct {
	Category   string `json:"category"`
	PN         string `json:"pn"`
	PV         string `json:"pv"`
	PR         string `json:"pr"`
	P          string `json:"p"`
	PVR        string `json:"pvr"`
	PF         string `json:"pf"`
	Slot       string `json:"slot"`
	Repository string `json:"repository"`
}

type Event struct {
	Protocol int    `json:"protocol"`
	ID       string `json:"id"`
	Sequence uint64 `json:"sequence"`
	Kind     string `json:"kind"` // phase, log, qa, result
	Stream   string `json:"stream,omitempty"`
	Class    string `json:"class,omitempty"` // elog class or metadata variable name
	Message  string `json:"message,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
}

func (r Request) Validate() error {
	if r.Protocol != Version {
		return fmt.Errorf("phase protocol: unsupported request version %d", r.Protocol)
	}
	if r.ID == "" || r.EAPI == "" || r.Ebuild == "" {
		return fmt.Errorf("phase protocol: incomplete request")
	}
	if r.Command != "run_phase" && r.Command != "run_phases" && r.Command != "discover_phases" {
		return fmt.Errorf("phase protocol: unsupported command %q", r.Command)
	}
	if (r.Command == "run_phase" && (r.Phase == "" || len(r.Phases) != 0)) ||
		(r.Command == "run_phases" && (r.Phase != "" || len(r.Phases) == 0)) ||
		(r.Command == "discover_phases" && (r.Phase != "" || len(r.Phases) != 0)) {
		return fmt.Errorf("phase protocol: invalid phase for command %s", r.Command)
	}
	if !safeToken.MatchString(r.ID) || (r.Phase != "" && !safeToken.MatchString(r.Phase)) || !safeToken.MatchString(r.EAPI) {
		return fmt.Errorf("phase protocol: unsafe request token")
	}
	for _, phase := range r.Phases {
		if !safeToken.MatchString(phase) {
			return fmt.Errorf("phase protocol: unsafe phase token")
		}
	}
	if !filepath.IsAbs(r.Ebuild) {
		return fmt.Errorf("phase protocol: ebuild path must be absolute")
	}
	if r.Environment != "" && !filepath.IsAbs(r.Environment) {
		return fmt.Errorf("phase protocol: installed environment path must be absolute")
	}
	if r.EAPI != "7" && r.EAPI != "8" && r.EAPI != "9" {
		return fmt.Errorf("phase protocol: unsupported EAPI %q (supported: 7, 8, 9)", r.EAPI)
	}
	for _, directory := range r.EclassDirs {
		if !filepath.IsAbs(directory) {
			return fmt.Errorf("phase protocol: eclass directory must be absolute")
		}
	}
	for label, directory := range map[string]string{
		"work": r.WorkDir, "build": r.BuildDir, "configuration root": r.ConfigRoot,
		"source": r.SourceDir, "image": r.ImageDir, "ROOT": r.RootDir,
		"SYSROOT": r.SysrootDir, "BROOT": r.BrootDir, "temporary": r.TempDir,
		"home": r.HomeDir,
	} {
		if directory != "" && !filepath.IsAbs(directory) {
			return fmt.Errorf("phase protocol: %s directory must be absolute", label)
		}
	}
	for _, directory := range r.UserPatchDirs {
		if !filepath.IsAbs(directory) {
			return fmt.Errorf("phase protocol: user patch directory must be absolute")
		}
	}
	if r.Package != (PackageIdentity{}) {
		for label, value := range map[string]string{
			"CATEGORY": r.Package.Category, "PN": r.Package.PN, "PV": r.Package.PV,
			"PR": r.Package.PR, "P": r.Package.P, "PVR": r.Package.PVR,
			"PF": r.Package.PF, "SLOT": r.Package.Slot, "repository": r.Package.Repository,
		} {
			if value == "" || !safePackageIdentity.MatchString(value) {
				return fmt.Errorf("phase protocol: unsafe or incomplete package identity %s", label)
			}
		}
	}
	if r.LogFile != "" && !filepath.IsAbs(r.LogFile) {
		return fmt.Errorf("phase protocol: PORTAGE_LOG_FILE must be absolute")
	}
	if len(r.UserPatchDirs) != 0 && r.WorkDir == "" {
		return fmt.Errorf("phase protocol: user patches require a work directory")
	}
	return nil
}

func DefaultPhases(eapi string) ([]string, error) {
	if eapi != "7" && eapi != "8" && eapi != "9" {
		return nil, fmt.Errorf("phase protocol: unsupported EAPI %q (supported: 7, 8, 9)", eapi)
	}
	return []string{"pkg_setup", "src_unpack", "src_prepare", "src_configure", "src_compile", "src_test", "src_install", "pkg_preinst", "pkg_postinst", "pkg_prerm", "pkg_postrm"}, nil
}

// RunBashWorker starts one clean Bash process for one phase request. The
// worker's stdout is reserved exclusively for protocol events; ebuild output
// is captured and returned as ordered log events.
func RunBashWorker(ctx context.Context, request Request) ([]Event, error) {
	return RunBashWorkerWithOptions(ctx, request, WorkerOptions{Isolation: IsolationPortage})
}

func RunBashWorkerWithOptions(ctx context.Context, request Request, options WorkerOptions) ([]Event, error) {
	if options.DurableLog != nil {
		if request.LogFile != "" && request.LogFile != options.DurableLog.Path() {
			return nil, fmt.Errorf("phase log: request path %s does not match reserved log %s", request.LogFile, options.DurableLog.Path())
		}
		request.LogFile = options.DurableLog.Path()
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	prepared, err := prepareVerifiedDistfiles(request)
	if err != nil {
		return nil, err
	}
	request = prepared
	if request.Policy.Configured {
		if request.Policy.UserPriv {
			return nil, fmt.Errorf("phase policy: userpriv is enabled but credential isolation is unsupported by this worker")
		}
		options.Namespaces.Network = request.Policy.NetworkSandbox
		options.Namespaces.IPC = request.Policy.IPCSandbox
		options.Namespaces.PID = request.Policy.PIDSandbox
		options.Namespaces.Mount = request.Policy.MountSandbox
	}
	switch options.Isolation {
	case "", IsolationPortage:
		executable := "/bin/bash"
		arguments := []string{"--noprofile", "--norc", "-c", bashWorker}
		if !request.Policy.Configured || request.Policy.Sandbox {
			sandbox, err := exec.LookPath("sandbox")
			if err != nil {
				return nil, fmt.Errorf("phase isolation: Portage sandbox is required: %w", err)
			}
			executable = sandbox
			arguments = append([]string{"/bin/bash"}, arguments...)
		}
		requested := namespaceSpecs(options.Namespaces)
		if namespacesRequested(requested) {
			unshare, lookupErr := exec.LookPath("unshare")
			if lookupErr != nil {
				warnNamespace(options.Diagnostics, "namespace isolation unavailable: unshare executable not found; continuing with Portage sandbox")
			} else {
				enabled, warnings := selectNamespaces(requested, func(arguments []string) error {
					probeArguments := append(append([]string{}, arguments...), "--", "/bin/true")
					return exec.CommandContext(ctx, unshare, probeArguments...).Run()
				})
				for _, warning := range warnings {
					warnNamespace(options.Diagnostics, warning)
				}
				if len(enabled) != 0 {
					arguments = append(append([]string{unshare}, enabled...), append([]string{"--"}, arguments...)...)
				}
			}
		}
		command := exec.CommandContext(ctx, executable, arguments...)
		events, runErr := runWorkerCommand(command, request)
		return persistWorkerEvents(request, events, runErr, options)
	case IsolationBubblewrap:
		command, isolatedRequest, err := isolatedBashCommand(ctx, request, true)
		if err != nil {
			return nil, err
		}
		events, runErr := runWorkerCommand(command, isolatedRequest)
		return persistWorkerEvents(request, events, runErr, options)
	default:
		return nil, fmt.Errorf("phase isolation: unknown mode %q", options.Isolation)
	}
}

func persistWorkerEvents(request Request, events []Event, runErr error, options WorkerOptions) ([]Event, error) {
	if options.DurableLog == nil {
		return events, runErr
	}
	for _, event := range events {
		stream := event.Stream
		if event.Class != "" {
			stream = event.Class
		}
		message := event.Message
		if event.Kind == "result" && event.ExitCode != nil {
			message = fmt.Sprintf("exit_code=%d", *event.ExitCode)
		}
		if err := options.DurableLog.WriteRecord(event.Sequence, request.ID, request.Phase, event.Kind, stream, message); err != nil {
			return events, fmt.Errorf("%w (durable log: %s)", err, options.DurableLog.Path())
		}
	}
	if runErr != nil {
		sequence := uint64(1)
		if len(events) != 0 {
			sequence = events[len(events)-1].Sequence + 1
		}
		if err := options.DurableLog.WriteRecord(sequence, request.ID, request.Phase, "terminal-error", "stderr", runErr.Error()); err != nil {
			return events, fmt.Errorf("%v; %w (durable log: %s)", runErr, err, options.DurableLog.Path())
		}
	}
	if options.FinalizeLog {
		if err := options.DurableLog.Finalize(options.CompressLog); err != nil {
			return events, fmt.Errorf("%w (durable log: %s)", err, options.DurableLog.Path())
		}
	}
	if runErr != nil {
		return events, fmt.Errorf("%w (durable log: %s)", runErr, options.DurableLog.Path())
	}
	return events, nil
}

func prepareVerifiedDistfiles(request Request) (Request, error) {
	if request.Distfiles == nil {
		return request, nil
	}
	if !filepath.IsAbs(request.Distfiles.Directory) {
		return request, fmt.Errorf("phase protocol: verified DISTDIR must be absolute")
	}
	for _, artifact := range request.Distfiles.Artifacts {
		if err := distfiles.Verify(filepath.Join(request.Distfiles.Directory, artifact.Name), artifact); err != nil {
			return request, fmt.Errorf("phase protocol: refusing unverified distfile: %w", err)
		}
	}
	environment := make(map[string]string, len(request.Env)+1)
	for name, value := range request.Env {
		environment[name] = value
	}
	environment["DISTDIR"] = request.Distfiles.Directory
	request.Env = environment
	return request, nil
}

func namespaceSpecs(options NamespaceOptions) []namespaceSpec {
	return []namespaceSpec{
		{name: "network", requested: options.Network, arguments: []string{"--net"}},
		{name: "IPC", requested: options.IPC, arguments: []string{"--ipc"}},
		{name: "mount", requested: options.Mount, arguments: []string{"--mount"}},
		{name: "PID", requested: options.PID, arguments: []string{"--pid", "--fork"}},
	}
}

func namespacesRequested(specs []namespaceSpec) bool {
	for _, spec := range specs {
		if spec.requested {
			return true
		}
	}
	return false
}

func selectNamespaces(specs []namespaceSpec, probe func([]string) error) ([]string, []string) {
	var enabled, warnings []string
	for _, spec := range specs {
		if !spec.requested {
			continue
		}
		if err := probe(spec.arguments); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s namespace isolation unavailable; continuing with remaining protections: %v", spec.name, err))
			continue
		}
		enabled = append(enabled, spec.arguments...)
	}
	return enabled, warnings
}

func warnNamespace(writer io.Writer, warning string) {
	if writer != nil {
		fmt.Fprintf(writer, "arise: warning: %s\n", warning)
	}
}

func isolatedBashCommand(ctx context.Context, request Request, isolateNetwork bool) (*exec.Cmd, Request, error) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, request, fmt.Errorf("phase isolation: bubblewrap is required: %w", err)
	}
	args := []string{
		"--die-with-parent", "--new-session", "--unshare-user", "--unshare-pid",
		"--unshare-ipc", "--unshare-uts", "--unshare-cgroup-try",
		"--ro-bind", "/usr", "/usr", "--ro-bind", "/bin", "/bin",
		"--ro-bind", "/sbin", "/sbin", "--ro-bind", "/lib", "/lib",
		"--ro-bind", "/lib64", "/lib64", "--tmpfs", "/tmp", "--proc", "/proc", "--dev", "/dev",
		"--dir", "/run", "--dir", "/run/arise", "--ro-bind", request.Ebuild, "/run/arise/ebuild",
		"--", "bash", "--noprofile", "--norc", "-c", bashWorker,
	}
	bind := func(mode, source, target string) {
		insert := len(args) - 6
		args = append(args[:insert], append([]string{mode, source, target}, args[insert:]...)...)
	}
	if request.Environment != "" {
		bind("--ro-bind", request.Environment, "/run/arise/environment")
		request.Environment = "/run/arise/environment"
	}
	if request.Distfiles != nil {
		insert := len(args) - 6
		binding := []string{"--ro-bind", request.Distfiles.Directory, "/run/arise/distfiles"}
		args = append(args[:insert], append(binding, args[insert:]...)...)
		request.Env["DISTDIR"] = "/run/arise/distfiles"
	}
	request.EclassDirs = append([]string(nil), request.EclassDirs...)
	for index, directory := range request.EclassDirs {
		target := fmt.Sprintf("/run/arise/eclasses/%d", index)
		insert := len(args) - 6
		binding := []string{"--ro-bind", directory, target}
		args = append(args[:insert], append(binding, args[insert:]...)...)
		request.EclassDirs[index] = target
	}
	request.UserPatchDirs = append([]string(nil), request.UserPatchDirs...)
	for index, directory := range request.UserPatchDirs {
		target := fmt.Sprintf("/run/arise/user-patches/%d", index)
		insert := len(args) - 6
		args = append(args[:insert], append([]string{"--ro-bind", directory, target}, args[insert:]...)...)
		request.UserPatchDirs[index] = target
	}
	originalWorkDir := request.WorkDir
	if request.WorkDir != "" {
		insert := len(args) - 6
		args = append(args[:insert], append([]string{"--bind", request.WorkDir, "/run/arise/work"}, args[insert:]...)...)
		request.WorkDir = "/run/arise/work"
	}
	if request.BuildDir == originalWorkDir && originalWorkDir != "" {
		request.BuildDir = request.WorkDir
	} else if request.BuildDir != "" {
		bind("--bind", request.BuildDir, "/run/arise/build")
		request.BuildDir = "/run/arise/build"
	}
	if request.ConfigRoot != "" {
		bind("--ro-bind", request.ConfigRoot, "/run/arise/config")
		request.ConfigRoot = "/run/arise/config"
	}
	if request.SourceDir != "" {
		insert := len(args) - 6
		args = append(args[:insert], append([]string{"--bind", request.SourceDir, "/run/arise/source"}, args[insert:]...)...)
		request.SourceDir = "/run/arise/source"
	}
	if request.ImageDir != "" {
		bind("--bind", request.ImageDir, "/run/arise/image")
		request.ImageDir = "/run/arise/image"
	}
	rootBindings := []struct {
		source *string
		target string
	}{{&request.RootDir, "/run/arise/root"}, {&request.SysrootDir, "/run/arise/sysroot"}, {&request.BrootDir, "/run/arise/broot"}}
	for _, binding := range rootBindings {
		if *binding.source == "" {
			continue
		}
		bind("--ro-bind", *binding.source, binding.target)
		*binding.source = binding.target
	}
	if request.TempDir != "" {
		bind("--bind", request.TempDir, "/run/arise/temp")
		request.TempDir = "/run/arise/temp"
	}
	if request.HomeDir != "" {
		bind("--bind", request.HomeDir, "/run/arise/home")
		request.HomeDir = "/run/arise/home"
	}
	if request.LogFile != "" {
		bind("--bind", request.LogFile, "/run/arise/build.log")
		request.LogFile = "/run/arise/build.log"
	}
	if isolateNetwork {
		args = append(args[:7], append([]string{"--unshare-net"}, args[7:]...)...)
	}
	command := exec.CommandContext(ctx, bwrap, args...)
	request.Ebuild = "/run/arise/ebuild"
	return command, request, nil
}

func runWorkerCommand(command *exec.Cmd, request Request) ([]Event, error) {
	return runWorkerCommandWithCancelGrace(command, request, 2*time.Second)
}

func runWorkerCommandWithCancelGrace(command *exec.Cmd, request Request, cancelGrace time.Duration) ([]Event, error) {
	cancelled := configureProcessGroupCancellation(command, cancelGrace)
	command.Env = []string{
		"PATH=/usr/bin:/bin", "LC_ALL=C", "ARISE_ID=" + request.ID,
		"ARISE_COMMAND=" + request.Command, "ARISE_PHASE=" + request.Phase, "ARISE_EAPI=" + request.EAPI, "ARISE_EBUILD=" + request.Ebuild,
	}
	if request.Environment != "" {
		command.Env = append(command.Env, "ARISE_ENVIRONMENT="+request.Environment)
	}
	if request.EmitMetadata {
		command.Env = append(command.Env, "ARISE_EMIT_METADATA=1")
	}
	if request.Policy.Configured && request.Policy.Strip {
		command.Env = append(command.Env, "ARISE_STRIP=1")
	} else {
		command.Env = append(command.Env, "ARISE_STRIP=0")
	}
	if len(request.Phases) != 0 {
		command.Env = append(command.Env, "ARISE_PHASES="+strings.Join(request.Phases, "\n"))
	}
	if len(request.EclassDirs) != 0 {
		command.Env = append(command.Env, "ARISE_ECLASS_DIRS="+strings.Join(request.EclassDirs, "\n"))
	}
	if len(request.UserPatchDirs) != 0 {
		command.Env = append(command.Env, "ARISE_USER_PATCH_DIRS="+strings.Join(request.UserPatchDirs, "\n"))
	}
	if len(request.HasVersion) != 0 {
		queries := make([]string, 0, len(request.HasVersion))
		for query := range request.HasVersion {
			queries = append(queries, query)
		}
		sort.Strings(queries)
		var encoded strings.Builder
		for _, query := range queries {
			encoded.WriteString(query)
			if request.HasVersion[query] {
				encoded.WriteString("\t1\n")
			} else {
				encoded.WriteString("\t0\n")
			}
		}
		command.Env = append(command.Env, "ARISE_HAS_VERSION="+encoded.String())
	}
	command.Env = append(command.Env,
		"FILESDIR="+filepath.Join(filepath.Dir(request.Ebuild), "files"),
		"EPREFIX=", "EROOT="+request.RootDir, "ESYSROOT="+request.SysrootDir,
	)
	if request.BuildDir != "" {
		command.Env = append(command.Env, "PORTAGE_BUILDDIR="+request.BuildDir)
	}
	if request.ConfigRoot != "" {
		command.Env = append(command.Env, "PORTAGE_CONFIGROOT="+request.ConfigRoot)
	}
	if request.WorkDir != "" {
		command.Env = append(command.Env, "WORKDIR="+request.WorkDir)
	}
	if request.SourceDir != "" {
		command.Env = append(command.Env, "S="+request.SourceDir)
	}
	if request.ImageDir != "" {
		command.Env = append(command.Env, "D="+request.ImageDir, "ED="+request.ImageDir)
	}
	if request.RootDir != "" {
		command.Env = append(command.Env, "ROOT="+request.RootDir)
	}
	if request.SysrootDir != "" {
		command.Env = append(command.Env, "SYSROOT="+request.SysrootDir)
	}
	if request.BrootDir != "" {
		command.Env = append(command.Env, "BROOT="+request.BrootDir)
	}
	if request.TempDir != "" {
		command.Env = append(command.Env, "T="+request.TempDir, "TMPDIR="+request.TempDir, "TMP="+request.TempDir, "TEMP="+request.TempDir)
	}
	if request.HomeDir != "" {
		command.Env = append(command.Env, "HOME="+request.HomeDir)
	}
	if request.LogFile != "" {
		command.Env = append(command.Env, "PORTAGE_LOG_FILE="+request.LogFile)
	}
	if request.Package != (PackageIdentity{}) {
		command.Env = append(command.Env,
			"CATEGORY="+request.Package.Category, "PN="+request.Package.PN,
			"PV="+request.Package.PV, "PR="+request.Package.PR,
			"P="+request.Package.P, "PVR="+request.Package.PVR,
			"PF="+request.Package.PF, "SLOT="+request.Package.Slot,
			"PORTAGE_REPO_NAME="+request.Package.Repository,
		)
	}
	names := make([]string, 0, len(request.Env))
	for name := range request.Env {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := request.Env[name]
		if !safeToken.MatchString(name) || strings.HasPrefix(name, "ARISE_") || packageIdentityEnvironment[name] || name == "PORTAGE_LOG_FILE" || name == "PORTAGE_BUILDDIR" || name == "PORTAGE_CONFIGROOT" || name == "EBUILD_PHASE" || name == "EBUILD_PHASE_FUNC" || name == "BASH_ENV" || name == "ENV" || name == "SHELLOPTS" || name == "PATH" || name == "WORKDIR" || name == "T" || name == "S" || name == "D" || name == "ED" || name == "ROOT" || name == "SYSROOT" || name == "BROOT" || name == "HOME" || name == "TMPDIR" || name == "TMP" || name == "TEMP" {
			return nil, fmt.Errorf("phase protocol: unsafe environment key %q", name)
		}
		command.Env = append(command.Env, name+"="+value)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	runErr := command.Run()
	decoder := NewDecoder(&stdout, request.ID)
	var events []Event
	for {
		event, err := decoder.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("phase worker protocol: %w; stderr: %s", err, strings.TrimSpace(stderr.String()))
		}
		events = append(events, event)
	}
	if cancelled.Load() {
		sequence := uint64(1)
		if len(events) != 0 {
			sequence = events[len(events)-1].Sequence + 1
		}
		events = append(events, Event{
			Protocol: Version, ID: request.ID, Sequence: sequence,
			Kind: "signal", Stream: "control", Message: "cancellation requested; worker process group terminated",
		})
		if runErr == nil {
			return events, fmt.Errorf("phase worker: cancelled")
		}
		return events, fmt.Errorf("phase worker: cancelled: %w", runErr)
	}
	if !decoder.Finished() {
		if runErr != nil && len(events) == 0 {
			return nil, fmt.Errorf("phase isolation: worker did not start: %w; stderr: %s", runErr, strings.TrimSpace(stderr.String()))
		}
		return events, fmt.Errorf("phase worker protocol: stream ended before terminal result; stderr: %s", strings.TrimSpace(stderr.String()))
	}
	if runErr != nil {
		return events, fmt.Errorf("phase worker: %w", runErr)
	}
	if exitCode := events[len(events)-1].ExitCode; exitCode == nil || *exitCode != 0 {
		return events, fmt.Errorf("phase worker protocol: terminal exit status does not match successful process exit")
	}
	return events, nil
}

func configureProcessGroupCancellation(command *exec.Cmd, grace time.Duration) *atomic.Bool {
	cancelled := &atomic.Bool{}
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.Setpgid = true
	command.Cancel = func() error {
		cancelled.Store(true)
		if command.Process == nil {
			return nil
		}
		pid := command.Process.Pid
		err := syscall.Kill(-pid, syscall.SIGTERM)
		if err != nil && err != syscall.ESRCH {
			return err
		}
		time.AfterFunc(grace, func() {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		})
		return nil
	}
	// Bound Cmd.Wait after the context fires even when a descendant inherited
	// stdout/stderr or ignored TERM. os/exec performs its own final Process.Kill
	// after this delay; the timer above applies the same escalation to the group.
	command.WaitDelay = grace + time.Second
	return cancelled
}

//go:embed worker.sh
var bashWorker string

type Decoder struct {
	decoder  *json.Decoder
	id       string
	next     uint64
	finished bool
}

func NewDecoder(reader io.Reader, requestID string) *Decoder {
	return &Decoder{decoder: json.NewDecoder(reader), id: requestID}
}

func (d *Decoder) Next() (Event, error) {
	if d.finished {
		var trailing json.RawMessage
		err := d.decoder.Decode(&trailing)
		if err == io.EOF {
			return Event{}, io.EOF
		}
		if err != nil {
			return Event{}, fmt.Errorf("phase protocol: invalid data after terminal result: %w", err)
		}
		return Event{}, fmt.Errorf("phase protocol: event after terminal result")
	}
	var event Event
	if err := d.decoder.Decode(&event); err != nil {
		return Event{}, err
	}
	if event.Protocol != Version || event.ID != d.id {
		return Event{}, fmt.Errorf("phase protocol: event envelope mismatch")
	}
	if event.Sequence != d.next {
		return Event{}, fmt.Errorf("phase protocol: event sequence %d, want %d", event.Sequence, d.next)
	}
	d.next++
	switch event.Kind {
	case "phase", "log", "qa":
		if event.ExitCode != nil {
			return Event{}, fmt.Errorf("phase protocol: non-result event has exit status")
		}
	case "metadata":
		if event.ExitCode != nil || !safeToken.MatchString(event.Class) {
			return Event{}, fmt.Errorf("phase protocol: invalid metadata event")
		}
	case "elog":
		if event.ExitCode != nil || !map[string]bool{"INFO": true, "LOG": true, "WARN": true, "ERROR": true, "QA": true}[event.Class] {
			return Event{}, fmt.Errorf("phase protocol: invalid elog event")
		}
	case "result":
		if event.ExitCode == nil {
			return Event{}, fmt.Errorf("phase protocol: result event lacks exit status")
		}
		d.finished = true
	default:
		return Event{}, fmt.Errorf("phase protocol: unknown event kind %q", event.Kind)
	}
	return event, nil
}

func (d *Decoder) Finished() bool {
	return d.finished
}
