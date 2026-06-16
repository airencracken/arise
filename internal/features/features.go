package features

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Flag string

const (
	FeatCcache          Flag = "ccache"
	FeatDistcc          Flag = "distcc"
	FeatBuildPkg        Flag = "buildpkg"
	FeatParallelInstall Flag = "parallel-install"
	FeatPreserveLibs    Flag = "preserve-libs"
	FeatStrict          Flag = "strict"
	FeatNoStrip         Flag = "nostrip"
	FeatTest            Flag = "test"
	FeatSplitLog        Flag = "split-log"
	FeatCompressDebug   Flag = "compressdebug"
	FeatUserPriv        Flag = "userpriv"
	FeatUserSandbox     Flag = "usersandbox"
	FeatSandbox         Flag = "sandbox"
	FeatNetworkSandbox  Flag = "network-sandbox"
	FeatPidSandbox      Flag = "pid-sandbox"
	FeatIpcSandbox      Flag = "ipc-sandbox"
	FeatFakeroot        Flag = "fakeroot"
	FeatCollisionProtect Flag = "collision-protect"
	FeatProtectOwned    Flag = "protect-owned"
	FeatConfigProtect   Flag = "config-protect"
	FeatFailClean       Flag = "fail-clean"
	FeatGetBinPkg       Flag = "getbinpkg"
)

type Config struct {
	Enabled  map[Flag]bool
	Disabled bool // set to true for "-*" which disables all features

	// SplitLog state
	splitLogDir  string
	splitLogCmds map[*exec.Cmd]struct{}
}

// ParseFeatures parses the FEATURES string from make.conf into a Config.
// The string is space-separated tokens. Tokens prefixed with "-" disable
// a feature. The special token "-*" disables all features.
func ParseFeatures(features string) *Config {
	cfg := &Config{
		Enabled: make(map[Flag]bool),
	}

	if features == "" {
		return cfg
	}

	tokens := strings.Fields(features)
	for _, tok := range tokens {
		if tok == "-*" {
			cfg.Disabled = true
			cfg.Enabled = make(map[Flag]bool)
			continue
		}
		if strings.HasPrefix(tok, "-") {
			name := Flag(tok[1:])
			cfg.Enabled[name] = false
		} else {
			cfg.Enabled[Flag(tok)] = true
		}
	}

	return cfg
}

// IsEnabled checks if a feature is enabled.
func (c *Config) IsEnabled(f Flag) bool {
	if v, ok := c.Enabled[f]; ok {
		return v
	}
	if c.Disabled {
		return false
	}
	return false
}

// ApplyToEnv applies feature-specific environment variables for a build
// command. It modifies cmd.Env and may also set up cmd.Stdout/Stderr for
// features like split-log.
func (c *Config) ApplyToEnv(cmd *exec.Cmd) {
	if c.IsEnabled(FeatCcache) {
		c.applyCcache(cmd)
	}
	if c.IsEnabled(FeatDistcc) {
		c.applyDistcc(cmd)
	}
	if c.IsEnabled(FeatNoStrip) {
		c.applyNoStrip(cmd)
	}
	if c.IsEnabled(FeatSplitLog) {
		c.applySplitLog(cmd)
	}
	if c.IsEnabled(FeatUserPriv) {
		c.applyUserPriv(cmd)
	}
	if c.IsEnabled(FeatUserSandbox) {
		log.Printf("features: usersandbox is not implemented (stub)")
	}
	if c.IsEnabled(FeatSandbox) {
		log.Printf("features: sandbox is not implemented (stub)")
	}
	if c.IsEnabled(FeatNetworkSandbox) {
		log.Printf("features: network-sandbox is not implemented (stub)")
	}
	if c.IsEnabled(FeatPidSandbox) {
		log.Printf("features: pid-sandbox is not implemented (stub)")
	}
	if c.IsEnabled(FeatIpcSandbox) {
		log.Printf("features: ipc-sandbox is not implemented (stub)")
	}
	if c.IsEnabled(FeatFakeroot) {
		log.Printf("features: fakeroot is not implemented (stub)")
	}
}

// applyCcache prefixes PATH with the ccache directory and sets CCACHE_DIR.
func (c *Config) applyCcache(cmd *exec.Cmd) {
	ccacheDir := "/usr/lib/ccache/bin"
	if d := os.Getenv("CCACHE_DIR"); d != "" {
		c.setenv(cmd, "CCACHE_DIR", d)
	} else {
		c.setenv(cmd, "CCACHE_DIR", "/var/tmp/ccache")
	}
	c.prependPath(cmd, ccacheDir)
}

// applyDistcc sets DISTCC_HOSTS and prefixes PATH with the distcc directory.
func (c *Config) applyDistcc(cmd *exec.Cmd) {
	if hosts := os.Getenv("DISTCC_HOSTS"); hosts != "" {
		c.setenv(cmd, "DISTCC_HOSTS", hosts)
	}
	distccDir := "/usr/lib/distcc/bin"
	c.prependPath(cmd, distccDir)
}

// applyNoStrip sets NOSTRIP=1 and DONTSTRIP=1 in the build environment.
func (c *Config) applyNoStrip(cmd *exec.Cmd) {
	c.setenv(cmd, "NOSTRIP", "1")
	c.setenv(cmd, "DONTSTRIP", "1")
}

// applySplitLog redirects stdout and stderr to separate log files.
func (c *Config) applySplitLog(cmd *exec.Cmd) {
	if c.splitLogDir == "" {
		c.splitLogDir = os.TempDir()
	}
	if c.splitLogCmds == nil {
		c.splitLogCmds = make(map[*exec.Cmd]struct{})
	}

	name := filepath.Base(cmd.Path)
	if len(cmd.Args) > 1 {
		name = fmt.Sprintf("%s-%s", name, strings.ReplaceAll(filepath.Base(cmd.Args[0]), "/", "_"))
	}

	outPath := filepath.Join(c.splitLogDir, name+".out")
	errPath := filepath.Join(c.splitLogDir, name+".err")

	outF, outErr := os.Create(outPath)
	errF, errErr := os.Create(errPath)
	if outErr == nil && errErr == nil {
		cmd.Stdout = outF
		cmd.Stderr = errF
		// Record for cleanup
		c.splitLogCmds[cmd] = struct{}{}
	} else {
		if outF != nil {
			if cerr := outF.Close(); cerr != nil { /* Best effort */ }
		}
		if errF != nil {
			if cerr := errF.Close(); cerr != nil { /* Best effort */ }
		}
	}
}

// CloseSplitLogs closes any open split-log file handles. Call after the
// command has completed.
func (c *Config) CloseSplitLogs() {
	if c.splitLogCmds == nil {
		return
	}
	for cmd := range c.splitLogCmds {
		if wc, ok := cmd.Stdout.(io.Closer); ok {
			if cerr := wc.Close(); cerr != nil { /* Best effort */ }
		}
		if wc, ok := cmd.Stderr.(io.Closer); ok {
			if cerr := wc.Close(); cerr != nil { /* Best effort */ }
		}
	}
	c.splitLogCmds = make(map[*exec.Cmd]struct{})
}

// applyUserPriv sets up command credentials to drop privileges.
// On Linux, sets SysProcAttr with credential settings.
func (c *Config) applyUserPriv(cmd *exec.Cmd) {
	applyUserPrivPlatform(cmd)
}

// setenv sets or replaces an environment variable in cmd.Env.
func (c *Config) setenv(cmd *exec.Cmd, key, val string) {
	prefix := key + "="
	for i, e := range cmd.Env {
		if strings.HasPrefix(e, prefix) {
			cmd.Env[i] = prefix + val
			return
		}
	}
	cmd.Env = append(cmd.Env, prefix+val)
}

// getenv reads a value from cmd.Env, falling back to OS env if cmd.Env is nil.
func (c *Config) getenv(cmd *exec.Cmd, key string) string {
	prefix := key + "="
	if cmd.Env != nil {
		for _, e := range cmd.Env {
			if strings.HasPrefix(e, prefix) {
				return e[len(prefix):]
			}
		}
	}
	return os.Getenv(key)
}

// prependPath prepends a directory to the PATH variable in cmd.Env.
func (c *Config) prependPath(cmd *exec.Cmd, dir string) {
	existing := c.getenv(cmd, "PATH")
	if existing == "" {
		c.setenv(cmd, "PATH", dir)
	} else {
		c.setenv(cmd, "PATH", dir+string(os.PathListSeparator)+existing)
	}
}

// SetSplitLogDir sets the directory for split-log output files.
func (c *Config) SetSplitLogDir(dir string) {
	c.splitLogDir = dir
}
