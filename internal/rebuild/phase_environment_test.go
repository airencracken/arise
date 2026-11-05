package rebuild

import (
	"compress/bzip2"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/phaseproto"
	"github.com/airencracken/arise/internal/portage"
)

func TestSourcePhaseStateSurvivesInstallAndRemoval(t *testing.T) {
	if _, err := exec.LookPath("sandbox"); err != nil {
		t.Skip("Portage sandbox is not installed")
	}
	base := t.TempDir()
	repo, root := filepath.Join(base, "repo"), filepath.Join(base, "root")
	packageDir := filepath.Join(repo, "app-misc", "state-probe")
	for _, directory := range []string{packageDir, filepath.Join(repo, "eclass"), filepath.Join(root, "var/db/pkg"), filepath.Join(base, "work"), filepath.Join(base, "distfiles")} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	const content = `EAPI=8
SLOT=0
pkg_setup() { PHASE_VALUE=setup; }
src_unpack() {
	S="${WORKDIR}/upstream-1"
	mkdir -p "$S" || die
	printf 'phase-state payload\n' > "$S/payload"
	phase_helper() { printf '%s' "$PHASE_VALUE"; }
}
src_prepare() { [[ $(phase_helper) == setup ]] || die 'lost setup state'; PHASE_VALUE=prepare; default; }
src_install() {
	[[ $(phase_helper) == prepare ]] || die 'lost prepare state'
	insinto /usr/share/state-probe; doins payload
	unset -f pkg_info
	pkg_config() { phase_helper > "$ROOT/config-state"; }
	pkg_postinst() { [[ $(phase_helper) == preinst ]] || die 'lost preinst state'; PHASE_VALUE=postinst; }
}
pkg_info() { die 'removed hook was resurrected'; }
pkg_preinst() { [[ $(phase_helper) == prepare ]] || die 'lost build state'; PHASE_VALUE=preinst; }
pkg_postinst() {
	use disabled && die 'inactive original hook ran'
}
pkg_prerm() { [[ $(phase_helper) == postinst ]] || die 'lost installed state'; PHASE_VALUE=prerm; }
pkg_postrm() { [[ $(phase_helper) == prerm ]] || die 'lost prerm state'; printf '%s' "$PHASE_VALUE" > "$ROOT/removal-state"; }
`
	if err := os.WriteFile(filepath.Join(packageDir, "state-probe-1.ebuild"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := RebuildConfig{
		RepoDir: repo, RootDir: root, VdbDir: filepath.Join(root, "var/db/pkg"),
		WorkDirBase: filepath.Join(base, "work"), DistfilesDir: filepath.Join(base, "distfiles"),
		PhaseLogDir: filepath.Join(base, "logs"), JournalDir: filepath.Join(base, "journals"),
		PhaseProtocol: true, Repository: "test", Repositories: []portage.RepoEntry{{Name: "test", Location: repo}},
	}
	if err := RebuildPackage(context.Background(), "app-misc/state-probe-1", &cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "usr/share/state-probe/payload"))
	if err != nil || string(data) != "phase-state payload\n" {
		t.Fatalf("source payload = %q, %v", data, err)
	}
	// Installed lifecycle must work after the build tree and current ebuilds
	// disappear, using the actual final environment and its helper functions.
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	lifecycle, err := OpenInstalledLifecycle(filepath.Join(cfg.VdbDir, "app-misc/state-probe-1"), &cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer lifecycle.Close()
	if lifecycle.HasPhase("pkg_info") || !lifecycle.HasPhase("pkg_config") {
		t.Fatal("installed snapshot lost a dynamically added hook or resurrected a removed one")
	}
	if err := lifecycle.Run(context.Background(), "pkg_config"); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(root, "config-state"))
	if err != nil || string(data) != "postinst" {
		t.Fatalf("dynamic hook lost installed state: %q, %v", data, err)
	}
	for _, phase := range []string{"pkg_prerm", "pkg_postrm"} {
		if err := lifecycle.Run(context.Background(), phase); err != nil {
			t.Fatal(err)
		}
	}
	data, err = os.ReadFile(filepath.Join(root, "removal-state"))
	if err != nil || string(data) != "prerm" {
		t.Fatalf("removal state = %q, %v", data, err)
	}
}

func TestInstalledSnapshotExcludesWorkerIsolationControls(t *testing.T) {
	request := phaseproto.Request{Env: map[string]string{"CFLAGS": "-O2", "SANDBOX_WRITE": "/old/build", "LD_PRELOAD": "/old/sandbox.so"}}
	state, err := savedProtocolEnvironment(request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(state), "SANDBOX_WRITE") || strings.Contains(string(state), "LD_PRELOAD") || !strings.Contains(string(state), "export CFLAGS='-O2'") {
		t.Fatalf("installed state leaked worker controls or lost build flags: %s", state)
	}
}

func TestLifecyclePolicyExplicitlyDisablesDefaultSandbox(t *testing.T) {
	for _, phase := range []string{"pkg_setup", "pkg_pretend", "pkg_preinst", "pkg_postinst", "pkg_prerm", "pkg_postrm", "pkg_config"} {
		request := applyPortageLifecyclePolicy(phaseproto.Request{RootDir: "/target"}, phase)
		if !request.Policy.Configured || request.Policy.Sandbox {
			t.Errorf("%s falls back to an unintended default sandbox: %+v", phase, request.Policy)
		}
	}
}

func TestSaveInstalledProtocolEnvironmentIsAtomic(t *testing.T) {
	vdb := t.TempDir()
	overlay := filepath.Join(t.TempDir(), "phase.environment")
	if err := os.WriteFile(overlay, []byte("export PHASE_VALUE=final\n"), 0644); err != nil {
		t.Fatal(err)
	}
	request := phaseproto.Request{EnvironmentOverlay: overlay}
	if err := saveInstalledProtocolEnvironment(vdb, request); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(vdb, "environment.bz2")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := io.ReadAll(bzip2.NewReader(file))
	file.Close()
	if err != nil || !strings.Contains(string(state), "PHASE_VALUE=final") {
		t.Fatalf("saved environment = %q, %v", state, err)
	}
	if err := os.Remove(overlay); err != nil {
		t.Fatal(err)
	}
	if err := saveInstalledProtocolEnvironment(vdb, request); err == nil {
		t.Fatal("missing final state was silently replaced with initial configuration")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("failed update damaged the committed environment: %v", err)
	}
	temporary, err := filepath.Glob(filepath.Join(vdb, ".environment.bz2.tmp-*"))
	if err != nil || len(temporary) != 0 {
		t.Fatalf("environment temporary files = %v, %v", temporary, err)
	}
}
