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

type Request struct {
	Protocol      int                    `json:"protocol"`
	ID            string                 `json:"id"`
	Command       string                 `json:"command"`
	Phase         string                 `json:"phase"`
	EAPI          string                 `json:"eapi"`
	Ebuild        string                 `json:"ebuild"`
	Env           map[string]string      `json:"env"`
	EclassDirs    []string               `json:"eclass_dirs,omitempty"`
	UserPatchDirs []string               `json:"user_patch_dirs,omitempty"`
	WorkDir       string                 `json:"work_dir,omitempty"`
	SourceDir     string                 `json:"source_dir,omitempty"`
	ImageDir      string                 `json:"image_dir,omitempty"`
	RootDir       string                 `json:"root_dir,omitempty"`
	SysrootDir    string                 `json:"sysroot_dir,omitempty"`
	BrootDir      string                 `json:"broot_dir,omitempty"`
	TempDir       string                 `json:"temp_dir,omitempty"`
	HomeDir       string                 `json:"home_dir,omitempty"`
	Distfiles     *distfiles.VerifiedSet `json:"-"`
}

type Event struct {
	Protocol int    `json:"protocol"`
	ID       string `json:"id"`
	Sequence uint64 `json:"sequence"`
	Kind     string `json:"kind"` // phase, log, qa, result
	Stream   string `json:"stream,omitempty"`
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
	if r.Command != "run_phase" && r.Command != "discover_phases" {
		return fmt.Errorf("phase protocol: unsupported command %q", r.Command)
	}
	if (r.Command == "run_phase" && r.Phase == "") || (r.Command == "discover_phases" && r.Phase != "") {
		return fmt.Errorf("phase protocol: invalid phase for command %s", r.Command)
	}
	if !safeToken.MatchString(r.ID) || (r.Phase != "" && !safeToken.MatchString(r.Phase)) || !safeToken.MatchString(r.EAPI) {
		return fmt.Errorf("phase protocol: unsafe request token")
	}
	if !filepath.IsAbs(r.Ebuild) {
		return fmt.Errorf("phase protocol: ebuild path must be absolute")
	}
	if r.EAPI != "7" && r.EAPI != "8" {
		return fmt.Errorf("phase protocol: unsupported EAPI %q (supported: 7, 8)", r.EAPI)
	}
	for _, directory := range r.EclassDirs {
		if !filepath.IsAbs(directory) {
			return fmt.Errorf("phase protocol: eclass directory must be absolute")
		}
	}
	if r.WorkDir != "" && !filepath.IsAbs(r.WorkDir) {
		return fmt.Errorf("phase protocol: work directory must be absolute")
	}
	for label, directory := range map[string]string{
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
	if len(r.UserPatchDirs) != 0 && r.WorkDir == "" {
		return fmt.Errorf("phase protocol: user patches require a work directory")
	}
	return nil
}

func DefaultPhases(eapi string) ([]string, error) {
	if eapi != "7" && eapi != "8" {
		return nil, fmt.Errorf("phase protocol: unsupported EAPI %q (supported: 7, 8)", eapi)
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
	if err := request.Validate(); err != nil {
		return nil, err
	}
	prepared, err := prepareVerifiedDistfiles(request)
	if err != nil {
		return nil, err
	}
	request = prepared
	switch options.Isolation {
	case "", IsolationPortage:
		sandbox, err := exec.LookPath("sandbox")
		if err != nil {
			return nil, fmt.Errorf("phase isolation: Portage sandbox is required: %w", err)
		}
		arguments := []string{"/bin/bash", "--noprofile", "--norc", "-c", bashWorker}
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
		command := exec.CommandContext(ctx, sandbox, arguments...)
		return runWorkerCommand(command, request)
	case IsolationBubblewrap:
		command, isolatedRequest, err := isolatedBashCommand(ctx, request, true)
		if err != nil {
			return nil, err
		}
		return runWorkerCommand(command, isolatedRequest)
	default:
		return nil, fmt.Errorf("phase isolation: unknown mode %q", options.Isolation)
	}
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
	if request.WorkDir != "" {
		insert := len(args) - 6
		args = append(args[:insert], append([]string{"--bind", request.WorkDir, "/run/arise/work"}, args[insert:]...)...)
		request.WorkDir = "/run/arise/work"
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
		if *binding.source == "" || *binding.source == "/" {
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
	if isolateNetwork {
		args = append(args[:7], append([]string{"--unshare-net"}, args[7:]...)...)
	}
	command := exec.CommandContext(ctx, bwrap, args...)
	request.Ebuild = "/run/arise/ebuild"
	return command, request, nil
}

func runWorkerCommand(command *exec.Cmd, request Request) ([]Event, error) {
	command.Env = []string{
		"PATH=/usr/bin:/bin", "LC_ALL=C", "ARISE_ID=" + request.ID,
		"ARISE_COMMAND=" + request.Command, "ARISE_PHASE=" + request.Phase, "ARISE_EAPI=" + request.EAPI, "ARISE_EBUILD=" + request.Ebuild,
	}
	if len(request.EclassDirs) != 0 {
		command.Env = append(command.Env, "ARISE_ECLASS_DIRS="+strings.Join(request.EclassDirs, "\n"))
	}
	if len(request.UserPatchDirs) != 0 {
		command.Env = append(command.Env, "ARISE_USER_PATCH_DIRS="+strings.Join(request.UserPatchDirs, "\n"))
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
	names := make([]string, 0, len(request.Env))
	for name := range request.Env {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := request.Env[name]
		if !safeToken.MatchString(name) || strings.HasPrefix(name, "ARISE_") || name == "BASH_ENV" || name == "ENV" || name == "SHELLOPTS" || name == "PATH" || name == "WORKDIR" || name == "T" || name == "S" || name == "D" || name == "ED" || name == "ROOT" || name == "SYSROOT" || name == "BROOT" || name == "HOME" || name == "TMPDIR" || name == "TMP" || name == "TEMP" {
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
	if runErr != nil {
		if len(events) == 0 {
			return nil, fmt.Errorf("phase isolation: worker did not start: %w; stderr: %s", runErr, strings.TrimSpace(stderr.String()))
		}
		return events, fmt.Errorf("phase worker: %w", runErr)
	}
	return events, nil
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
		return Event{}, io.EOF
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
