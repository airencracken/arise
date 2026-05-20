package phase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/airencracken/arise/internal/features"
)

type PhaseConfig struct {
	WorkDir           string
	Sourcedir         string
	DESTDIR           string
	CFLAGS            string
	CXXFLAGS          string
	LDFLAGS           string
	MAKEOPTS          string
	Arch              string
	PN                string
	PV                string
	CATEGORY          string
	EBUILD_PATH       string
	PortageConfigRoot string
	Features          *features.Config
}

type Runner struct {
	cfg PhaseConfig
}

func NewRunner(cfg PhaseConfig) (*Runner, error) {
	if cfg.WorkDir == "" {
		return nil, fmt.Errorf("phase: WorkDir is required")
	}
	if cfg.Sourcedir == "" {
		return nil, fmt.Errorf("phase: Sourcedir is required")
	}
	if cfg.DESTDIR == "" {
		return nil, fmt.Errorf("phase: DESTDIR is required")
	}
	return &Runner{cfg: cfg}, nil
}

func (r *Runner) Run(ctx context.Context, phase string) error {
	return r.RunPhase(ctx, phase)
}

func (r *Runner) RunPhase(ctx context.Context, phase string) error {
	switch phase {
	case "src_unpack":
		return r.unpack(ctx)
	case "src_prepare":
		return nil
	case "src_configure":
		return r.configure(ctx)
	case "src_compile":
		return r.compile(ctx)
	case "src_install":
		return r.install(ctx)
	default:
		return nil
	}
}

func (r *Runner) RunPhases(ctx context.Context, phases []string) error {
	for _, ph := range phases {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := r.RunPhase(ctx, ph); err != nil {
			return fmt.Errorf("phase %s: %w", ph, err)
		}
	}
	return nil
}

func (r *Runner) unpack(ctx context.Context) error {
	srcDir := r.cfg.Sourcedir
	workDir := r.cfg.WorkDir

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}

	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		switch ext {
		case ".gz", ".xz", ".bz2", ".tgz":
			archivePath := filepath.Join(srcDir, e.Name())
			cmd := exec.CommandContext(ctx, "tar", "xf", archivePath, "-C", workDir)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			r.applyFeatures(cmd)
			if err := cmd.Run(); err != nil {
				return cmdError(err)
			}
			return nil
		}
	}
	return nil
}

func (r *Runner) configure(ctx context.Context) error {
	srcDir := r.cfg.Sourcedir
	configurePath := filepath.Join(srcDir, "configure")
	if _, err := os.Stat(configurePath); os.IsNotExist(err) {
		return nil
	}
	cmd := exec.CommandContext(ctx, "./configure",
		"--prefix=/usr",
		"--build="+r.cfg.Arch+"-pc-linux-gnu",
		"--host="+r.cfg.Arch+"-pc-linux-gnu",
	)
	cmd.Dir = srcDir
	cmd.Env = r.buildEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	r.applyFeatures(cmd)
	if err := cmd.Run(); err != nil {
		return cmdError(err)
	}
	return nil
}

func (r *Runner) compile(ctx context.Context) error {
	srcDir := r.cfg.Sourcedir
	if _, err := os.Stat(filepath.Join(srcDir, "Makefile")); os.IsNotExist(err) {
		return nil
	}
	makeBinary := "make"
	if runtime.GOOS != "linux" {
		makeBinary = "gmake"
	}
	args := append([]string{"-C", srcDir}, r.makeOpts()...)
	cmd := exec.CommandContext(ctx, makeBinary, args...)
	cmd.Env = r.buildEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	r.applyFeatures(cmd)
	if err := cmd.Run(); err != nil {
		return cmdError(err)
	}
	return nil
}

func (r *Runner) install(ctx context.Context) error {
	srcDir := r.cfg.Sourcedir
	if err := os.MkdirAll(r.cfg.DESTDIR, 0755); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(srcDir, "Makefile")); os.IsNotExist(err) {
		return nil
	}
	makeBinary := "make"
	if runtime.GOOS != "linux" {
		makeBinary = "gmake"
	}
	args := append([]string{"-C", srcDir, "install"}, r.installOpts()...)
	cmd := exec.CommandContext(ctx, makeBinary, args...)
	cmd.Env = r.buildEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	r.applyFeatures(cmd)
	if err := cmd.Run(); err != nil {
		return cmdError(err)
	}
	return nil
}

func (r *Runner) buildEnv() []string {
	env := os.Environ()

	arch := r.cfg.Arch
	if arch == "" {
		arch = "amd64"
	}

	setenv := func(k, v string) {
		prefix := k + "="
		for i, e := range env {
			if strings.HasPrefix(e, prefix) {
				env[i] = prefix + v
				return
			}
		}
		env = append(env, prefix+v)
	}

	setenv("ARCH", arch)
	setenv("DESTDIR", r.cfg.DESTDIR)
	setenv("ED", r.cfg.DESTDIR)
	setenv("EROOT", r.cfg.DESTDIR)
	if r.cfg.CFLAGS != "" {
		setenv("CFLAGS", r.cfg.CFLAGS)
	}
	if r.cfg.CXXFLAGS != "" {
		setenv("CXXFLAGS", r.cfg.CXXFLAGS)
	}
	if r.cfg.LDFLAGS != "" {
		setenv("LDFLAGS", r.cfg.LDFLAGS)
	}
	if r.cfg.MAKEOPTS != "" {
		setenv("MAKEOPTS", r.cfg.MAKEOPTS)
	}

	return env
}

func (r *Runner) applyFeatures(cmd *exec.Cmd) {
	if r.cfg.Features != nil {
		r.cfg.Features.ApplyToEnv(cmd)
	}
}

func (r *Runner) makeOpts() []string {
	raw := strings.TrimSpace(r.cfg.MAKEOPTS)
	if raw == "" {
		return nil
	}
	return strings.Fields(raw)
}

func (r *Runner) installOpts() []string {
	return []string{"DESTDIR=" + r.cfg.DESTDIR}
}

func cmdError(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			return fmt.Errorf("command exited with status %d", status.ExitStatus())
		}
		return fmt.Errorf("command failed: %w", exitErr)
	}
	return err
}
