//go:build live_portage

package rebuild

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/phaseproto"
	"github.com/airencracken/arise/internal/portage"
)

func TestLiveNetworkTestPhasePolicyMatchesPortage(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Portage Python interpreter is unavailable")
	}
	// Capture Portage's actual spawn policy without spawning an ebuild, changing
	// namespaces or accessing the network. The reference is installed Portage,
	// rather than a second copy of Arise's expected phase table.
	const reference = `
import importlib, json
doebuild = importlib.import_module("portage.package.ebuild.doebuild")
class Settings(dict):
    pass
doebuild.spawn = lambda cmd, settings, **kwargs: kwargs["networked"]
results = []
for features in ("network-sandbox", ""):
    for properties in ("test_network", ""):
        for phase in ("unpack", "prepare", "configure", "compile", "test", "install"):
            settings = Settings(PORTAGE_BIN_PATH="/unused", PORTAGE_PROPERTIES=properties, PORTAGE_RESTRICT="")
            settings.features = features.split()
            results.append(dict(features=features, properties=properties, phase="src_" + phase,
                networked=doebuild._doebuild_spawn(phase, settings)))
print(json.dumps(results))
`
	output, err := exec.Command("python3", "-c", reference).Output()
	if err != nil {
		t.Fatalf("Portage reference: %v", err)
	}
	var cases []struct {
		Features, Properties, Phase string
		Networked                   bool
	}
	if err := json.Unmarshal(output, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 24 {
		t.Fatalf("Portage reference returned %d cases, want 24", len(cases))
	}
	for _, test := range cases {
		policy, err := phaseproto.EvaluateExecutionPolicy(test.Features+" test", "", test.Properties, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := applyPortageLifecyclePolicy(phaseproto.Request{Policy: policy}, test.Phase)
		if got.Policy.NetworkSandbox == test.Networked {
			t.Errorf("%s FEATURES=%q PROPERTIES=%q: Arise network sandbox=%v, Portage networked=%v", test.Phase, test.Features, test.Properties, got.Policy.NetworkSandbox, test.Networked)
		}
	}
}

func TestLiveNSSNetworkTestPreflight(t *testing.T) {
	if os.Getenv("ARISE_LIVE_NSS_PREFLIGHT") != "1" {
		t.Skip("set ARISE_LIVE_NSS_PREFLIGHT=1 for the real NSS preflight")
	}
	configuration, err := portage.LoadEffectiveConfig("/etc/portage")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("/var/db/pkg/dev-libs/nss-3.112.5/USE")
	if err != nil {
		t.Fatal(err)
	}
	use := make(map[string]bool)
	for _, flag := range strings.Fields(string(data)) {
		use[flag] = true
	}
	base := t.TempDir()
	cfg := &RebuildConfig{
		RepoDir: "/var/db/repos/gentoo", Repository: "gentoo", RootDir: base, VdbDir: "/var/db/pkg",
		WorkDirBase: filepath.Join(base, "work"), PhaseLogDir: filepath.Join(base, "logs"), JournalDir: filepath.Join(base, "journal"),
		PortageConfig: configuration, UseFlags: use, Arch: "amd64", SelectedSlot: "0",
	}
	if err := PreflightPackage("dev-libs/nss-3.125", cfg); err != nil {
		t.Fatal(err)
	}
}
