package features

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestParseFeatures_Basic(t *testing.T) {
	cfg := ParseFeatures("ccache distcc buildpkg")
	if !cfg.IsEnabled(FeatCcache) {
		t.Error("expected ccache to be enabled")
	}
	if !cfg.IsEnabled(FeatDistcc) {
		t.Error("expected distcc to be enabled")
	}
	if !cfg.IsEnabled(FeatBuildPkg) {
		t.Error("expected buildpkg to be enabled")
	}
	if cfg.IsEnabled(FeatNoStrip) {
		t.Error("expected nostrip to be disabled")
	}
}

func TestParseFeatures_Empty(t *testing.T) {
	cfg := ParseFeatures("")
	if cfg.IsEnabled(FeatCcache) {
		t.Error("expected ccache to be disabled for empty string")
	}
	if cfg.Disabled {
		t.Error("Disabled should not be set for empty string")
	}
}

func TestParseFeatures_DisableAll(t *testing.T) {
	cfg := ParseFeatures("-*")
	if !cfg.Disabled {
		t.Error("expected Disabled to be true")
	}
	if cfg.IsEnabled(FeatCcache) {
		t.Error("expected ccache to be disabled after -*")
	}
	if cfg.IsEnabled(FeatBuildPkg) {
		t.Error("expected buildpkg to be disabled after -*")
	}
}

func TestParseFeatures_DisableAllThenEnable(t *testing.T) {
	cfg := ParseFeatures("-* ccache distcc")
	if !cfg.Disabled {
		t.Error("expected Disabled to be true")
	}
	if !cfg.IsEnabled(FeatCcache) {
		t.Error("expected ccache to be enabled after -* ccache")
	}
	if !cfg.IsEnabled(FeatDistcc) {
		t.Error("expected distcc to be enabled after -* distcc")
	}
	if cfg.IsEnabled(FeatBuildPkg) {
		t.Error("expected buildpkg to be disabled")
	}
}

func TestParseFeatures_DisableAllAtEnd(t *testing.T) {
	cfg := ParseFeatures("ccache distcc -*")
	if !cfg.Disabled {
		t.Error("expected Disabled to be true")
	}
	if cfg.IsEnabled(FeatCcache) {
		t.Error("expected ccache to be disabled (cleared by trailing -*)")
	}
}

func TestParseFeatures_Negation(t *testing.T) {
	cfg := ParseFeatures("ccache distcc buildpkg -distcc")
	if !cfg.IsEnabled(FeatCcache) {
		t.Error("expected ccache to be enabled (explicitly set)")
	}
	if cfg.IsEnabled(FeatDistcc) {
		t.Error("expected distcc to be disabled by negation")
	}
	if !cfg.IsEnabled(FeatBuildPkg) {
		t.Error("expected buildpkg to be enabled (explicitly set)")
	}
}

func TestParseFeatures_ExplicitEnable(t *testing.T) {
	cfg := ParseFeatures("ccache distcc")
	if !cfg.IsEnabled(FeatCcache) {
		t.Error("expected ccache to be enabled")
	}
	if !cfg.IsEnabled(FeatDistcc) {
		t.Error("expected distcc to be enabled")
	}
}

func TestParseFeatures_UnknownFlag(t *testing.T) {
	cfg := ParseFeatures("ccache custom-feature")
	if !cfg.IsEnabled(FeatCcache) {
		t.Error("expected ccache to be enabled")
	}
	if !cfg.IsEnabled(Flag("custom-feature")) {
		t.Error("expected custom-feature to be enabled (extensible)")
	}
}

func TestIsEnabled_ImplicitDefault(t *testing.T) {
	cfg := ParseFeatures("")
	for _, f := range []Flag{FeatCcache, FeatDistcc, FeatBuildPkg, FeatNoStrip, FeatStrict} {
		if cfg.IsEnabled(f) {
			t.Errorf("expected %s to be disabled by default", f)
		}
	}
}

func TestIsEnabled_DisabledAll(t *testing.T) {
	cfg := ParseFeatures("-* buildpkg")
	if !cfg.IsEnabled(FeatBuildPkg) {
		t.Error("expected buildpkg to be enabled")
	}
	if cfg.IsEnabled(FeatCcache) {
		t.Error("expected ccache to be disabled when not in the list")
	}
}

func TestApplyToEnv_Ccache(t *testing.T) {
	cfg := ParseFeatures("ccache")
	cmd := exec.Command("true")
	cmd.Env = []string{"PATH=/usr/bin", "HOME=/tmp"}

	cfg.ApplyToEnv(cmd)

	hasCcachDir := false
	hasCcachPath := false
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "CCACHE_DIR=") {
			hasCcachDir = true
		}
		if strings.HasPrefix(e, "PATH=") && strings.Contains(e, "/usr/lib/ccache/bin") {
			hasCcachPath = true
		}
	}
	if !hasCcachDir {
		t.Error("expected CCACHE_DIR to be set")
	}
	if !hasCcachPath {
		t.Error("expected PATH to contain /usr/lib/ccache/bin")
	}
}

func TestApplyToEnv_Distcc(t *testing.T) {
	_ = os.Setenv("DISTCC_HOSTS", "localhost/4")
	defer func() { _ = os.Unsetenv("DISTCC_HOSTS") }()

	cfg := ParseFeatures("distcc")
	cmd := exec.Command("true")
	cmd.Env = []string{"PATH=/usr/bin"}

	cfg.ApplyToEnv(cmd)

	hasDistccHosts := false
	hasDistccPath := false
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "DISTCC_HOSTS=") {
			hasDistccHosts = true
		}
		if strings.HasPrefix(e, "PATH=") && strings.Contains(e, "/usr/lib/distcc/bin") {
			hasDistccPath = true
		}
	}
	if !hasDistccHosts {
		t.Error("expected DISTCC_HOSTS to be set")
	}
	if !hasDistccPath {
		t.Error("expected PATH to contain /usr/lib/distcc/bin")
	}
}

func TestApplyToEnv_NoStrip(t *testing.T) {
	cfg := ParseFeatures("nostrip")
	cmd := exec.Command("true")
	cmd.Env = os.Environ()

	cfg.ApplyToEnv(cmd)

	hasNoStrip := false
	hasDontStrip := false
	for _, e := range cmd.Env {
		if e == "NOSTRIP=1" {
			hasNoStrip = true
		}
		if e == "DONTSTRIP=1" {
			hasDontStrip = true
		}
	}
	if !hasNoStrip {
		t.Error("expected NOSTRIP=1")
	}
	if !hasDontStrip {
		t.Error("expected DONTSTRIP=1")
	}
}

func TestApplyToEnv_MultipleFeatures(t *testing.T) {
	cfg := ParseFeatures("ccache nostrip")
	cmd := exec.Command("true")
	cmd.Env = []string{"PATH=/usr/bin"}

	cfg.ApplyToEnv(cmd)

	hasCcachDir := false
	hasNoStrip := false
	hasDontStrip := false
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "CCACHE_DIR=") {
			hasCcachDir = true
		}
		if e == "NOSTRIP=1" {
			hasNoStrip = true
		}
		if e == "DONTSTRIP=1" {
			hasDontStrip = true
		}
	}
	if !hasCcachDir {
		t.Error("expected CCACHE_DIR to be set")
	}
	if !hasNoStrip {
		t.Error("expected NOSTRIP=1")
	}
	if !hasDontStrip {
		t.Error("expected DONTSTRIP=1")
	}
}

func TestApplyToEnv_NoFeatures(t *testing.T) {
	cfg := ParseFeatures("")
	cmd := exec.Command("true")
	cmd.Env = []string{"PATH=/usr/bin"}

	cfg.ApplyToEnv(cmd)

	if len(cmd.Env) != 1 {
		t.Errorf("expected 1 env var, got %d: %v", len(cmd.Env), cmd.Env)
	}
}

func TestApplyToEnv_SplitLog(t *testing.T) {
	cfg := ParseFeatures("split-log")
	tmp := t.TempDir()
	cfg.SetSplitLogDir(tmp)

	cmd := exec.Command("echo", "hello")
	cmd.Env = os.Environ()

	cfg.ApplyToEnv(cmd)

	if cmd.Stdout == nil {
		t.Error("expected Stdout to be set for split-log")
	}
	if cmd.Stderr == nil {
		t.Error("expected Stderr to be set for split-log")
	}

	// Clean up
	cfg.CloseSplitLogs()
}

func TestApplyToEnv_UserPriv_Linux(t *testing.T) {
	cfg := ParseFeatures("userpriv")
	cmd := exec.Command("true")
	cmd.Env = os.Environ()

	cfg.ApplyToEnv(cmd)

	// UserPriv sets SysProcAttr on Linux; may or may not have credentials
	// depending on whether the portage user exists
	if cmd.SysProcAttr != nil {
		// Just verify nothing crashed
	}
}

func TestApplyToEnv_SandboxStubs(t *testing.T) {
	// These should not crash
	for _, feat := range []string{"usersandbox", "sandbox", "network-sandbox", "pid-sandbox", "ipc-sandbox", "fakeroot"} {
		t.Run(feat, func(t *testing.T) {
			cfg := ParseFeatures(feat)
			cmd := exec.Command("true")
			cmd.Env = os.Environ()
			cfg.ApplyToEnv(cmd)
		})
	}
}

func TestParseFeatures_AdversarialInput(t *testing.T) {
	inputs := []string{
		strings.Repeat("a", 10000),
		"",
		"   ",
		"ccache\tdistcc\nbuildpkg",
		"-ccache --extra-flag",
		"---ccache",
		"-* -* -*",
		"ccache -ccache ccache -ccache",
	}
	for _, in := range inputs {
		cfg := ParseFeatures(in)
		_ = cfg
	}
}

func TestParseFeatures_FeatureConstantsExist(t *testing.T) {
	flags := []Flag{
		FeatCcache, FeatDistcc, FeatBuildPkg, FeatParallelInstall,
		FeatPreserveLibs, FeatStrict, FeatNoStrip, FeatTest,
		FeatSplitLog, FeatCompressDebug, FeatUserPriv,
		FeatUserSandbox, FeatSandbox, FeatNetworkSandbox,
		FeatPidSandbox, FeatIpcSandbox, FeatFakeroot,
		FeatCollisionProtect, FeatProtectOwned, FeatConfigProtect,
		FeatFailClean, FeatGetBinPkg,
	}
	for _, f := range flags {
		if string(f) == "" {
			t.Errorf("empty Flag constant")
		}
	}
}

func TestSetenv(t *testing.T) {
	cfg := &Config{}
	cmd := exec.Command("true")
	cmd.Env = []string{"FOO=bar"}

	cfg.setenv(cmd, "FOO", "baz")
	cfg.setenv(cmd, "NEW", "val")

	hasFoo := false
	hasNew := false
	for _, e := range cmd.Env {
		if e == "FOO=baz" {
			hasFoo = true
		}
		if e == "NEW=val" {
			hasNew = true
		}
	}
	if !hasFoo {
		t.Error("expected FOO=baz")
	}
	if !hasNew {
		t.Error("expected NEW=val")
	}
}

func TestSetSplitLogDir(t *testing.T) {
	cfg := &Config{}
	cfg.SetSplitLogDir("/var/log/portage")
	if cfg.splitLogDir != "/var/log/portage" {
		t.Errorf("expected splitLogDir to be /var/log/portage, got %s", cfg.splitLogDir)
	}
}
