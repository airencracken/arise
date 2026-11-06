//go:build live_portage

package phaseproto

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/distfiles"
)

func TestLiveBashCompletionQADoesNotLeakIntoPreinst(t *testing.T) {
	binPath, err := exec.Command("portageq", "envvar", "PORTAGE_BIN_PATH").Output()
	if err != nil {
		if os.Getenv("ARISE_REQUIRE_PHASE_CONTRACT") == "1" {
			t.Fatalf("required Portage QA reference unavailable: %v", err)
		}
		t.Skipf("Portage QA reference unavailable: %v", err)
	}
	check := filepath.Join(strings.TrimSpace(string(binPath)), "install-qa-check.d/60bash-completion")
	for _, path := range []string{check, "/usr/share/bash-completion/bash_completion"} {
		if _, err := os.Stat(path); err != nil {
			if os.Getenv("ARISE_REQUIRE_PHASE_CONTRACT") == "1" {
				t.Fatalf("required Bash completion QA reference unavailable: %s", path)
			}
			t.Skipf("Bash completion QA reference unavailable: %s", path)
		}
	}
	request := phaseStateRequest(t, `src_unpack() { mkdir -p "$S"; }
src_install() {
	dodir /usr/share/bash-completion/completions
	printf 'complete -F _comp_complete_minimal state-probe\n' > "$ED/usr/share/bash-completion/completions/state-probe"
}
pkg_preinst() {
	! declare -F _comp_abspath >/dev/null || die 'Bash completion helpers leaked from QA'
	! shopt -q extglob || die 'Bash completion options leaked from QA'
	printf passed > "$T/observed"
}`)
	request.InstallQAChecks = []string{check}
	runPhaseStateSequence(t, request, true, "src_unpack", "src_install", "pkg_preinst")
	data, err := os.ReadFile(filepath.Join(request.TempDir, "observed"))
	if err != nil || string(data) != "passed" {
		t.Fatalf("pkg_preinst = %q, %v", data, err)
	}
}

func TestLivePhaseStateMatchesPortage(t *testing.T) {
	ebuildCommand, err := exec.LookPath("ebuild")
	if err != nil {
		if os.Getenv("ARISE_REQUIRE_PHASE_CONTRACT") == "1" {
			t.Fatal("required Portage ebuild reference unavailable")
		}
		t.Skip("Portage ebuild command unavailable")
	}
	const body = `SLOT=0
LICENSE=MIT
KEYWORDS="~amd64"
ARISE_VERSION=package-version
BASH_COMPLETION_COMPAT_DIR=package-completion
PHASE_DELETE=global
phase_removed_helper() { printf resurrected; }
phase_replaced_helper() { printf initial; }
pkg_setup() {
	PHASE_VALUE=setup
	PHASE_ARRAY=(one 'two words')
	declare -gA PHASE_MAP=([key]=$'quoted "value"\nnext line')
	declare -gi PHASE_INTEGER=17
	declare -gl PHASE_LOWER=MiXeD
	declare -gu PHASE_UPPER=MiXeD
	declare -gn PHASE_REF=PHASE_VALUE
	readonly PHASE_CONSTANT=constant
	export PHASE_EXPORTED=exported-value
	export XDG_CONFIG_HOME=contract-sentinel USER=contract-sentinel http_proxy=contract-sentinel CCACHE_DIR=contract-sentinel
	phase_helper() { printf '%s' "$PHASE_VALUE"; }
	phase_exported_helper() { printf exported-helper; }
	export -f phase_exported_helper
}
src_unpack() { S="${WORKDIR}/upstream-1"; mkdir -p "$S" || die; }
src_prepare() { PHASE_VALUE=prepare; unset PHASE_DELETE; unset -f phase_removed_helper; phase_replaced_helper() { printf replaced; }; docompress -x /usr/share/doc/state-probe-1; default; }
src_compile() { PHASE_VALUE=compile; export -n PHASE_EXPORTED; }
src_install() {
	printf '%s|%s|%s|%s|%s' "$(phase_helper)" "${PHASE_ARRAY[1]}" "$PHASE_CONSTANT" "${S##*/}" "${EBUILD_PHASE_FUNC@a}" > "$T/state"
	printf '|%s|%s|%s|%s|%s|%s|%s|%s|%s' "$ARISE_VERSION" "$BASH_COMPLETION_COMPAT_DIR" "${PHASE_MAP[key]}" "$PHASE_INTEGER" "$PHASE_LOWER" "$PHASE_UPPER" "$PHASE_REF" "${PHASE_CONSTANT@a}" "$(bash -c phase_exported_helper)" >> "$T/state"
	printf '|%s|%s|%s|%s' "${PHASE_EXPORTED@a}" "$PHASE_EXPORTED" "${PHASE_DELETE-unset}" "$(phase_replaced_helper)" >> "$T/state"
	if declare -F phase_removed_helper >/dev/null; then printf '|resurrected'; else printf '|unset'; fi >> "$T/state"
	for runtime_name in XDG_CONFIG_HOME USER http_proxy CCACHE_DIR; do
		if [[ ${!runtime_name-} == contract-sentinel ]]; then printf '|stale'; else printf '|current'; fi
	done >> "$T/state"
	insinto /usr/share/doc/state-probe-1
	newins "$T/state" state || die
	printf '%0200d' 0 > "$T/plain"
	newins "$T/plain" plain || die
}`
	for _, eapi := range []string{"7", "8", "9"} {
		t.Run("EAPI-"+eapi, func(t *testing.T) {
			directory := t.TempDir()
			repo := filepath.Join(directory, "repo")
			packageDir := filepath.Join(repo, "app-misc/state-probe")
			for _, path := range []string{packageDir, filepath.Join(repo, "profiles"), filepath.Join(repo, "metadata"), filepath.Join(directory, "portage-tmp"), filepath.Join(directory, "distfiles")} {
				if err := os.MkdirAll(path, 0755); err != nil {
					t.Fatal(err)
				}
			}
			for path, value := range map[string]string{
				filepath.Join(repo, "profiles/repo_name"):         "state-probe\n",
				filepath.Join(repo, "metadata/layout.conf"):       "masters = gentoo\n",
				filepath.Join(packageDir, "state-probe-1.ebuild"): "EAPI=" + eapi + "\n" + body + "\n",
			} {
				if err := os.WriteFile(path, []byte(value), 0644); err != nil {
					t.Fatal(err)
				}
			}
			current, err := user.Current()
			if err != nil {
				t.Fatal(err)
			}
			group, err := user.LookupGroupId(current.Gid)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, ebuildCommand, "--skip-manifest", filepath.Join(packageDir, "state-probe-1.ebuild"), "install")
			command.Env = append(os.Environ(), "PORTAGE_TMPDIR="+filepath.Join(directory, "portage-tmp"),
				"DISTDIR="+filepath.Join(directory, "distfiles"), "PORTAGE_USERNAME="+current.Username, "PORTAGE_GRPNAME="+group.Name,
				"FEATURES=-sandbox -usersandbox -userpriv -network-sandbox -pid-sandbox -ipc-sandbox -mount-sandbox")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("Portage state reference: %v\n%s", err, output)
			}
			request := phaseStateRequestForEAPI(t, eapi, body)
			runPhaseStateSequence(t, request, true, "pkg_setup", "src_unpack", "src_prepare", "src_configure", "src_compile", "src_install")
			for _, name := range []string{"state", "plain"} {
				relative := filepath.Join("usr/share/doc/state-probe-1", name)
				portage, err := os.ReadFile(filepath.Join(directory, "portage-tmp/portage/app-misc/state-probe-1/image", relative))
				if err != nil {
					t.Fatal(err)
				}
				arise, err := os.ReadFile(filepath.Join(request.ImageDir, relative))
				if err != nil || string(arise) != string(portage) {
					t.Fatalf("%s differs: Arise=%q, Portage=%q, error=%v", name, arise, portage, err)
				}
			}
		})
	}
}

func TestLiveBinutilsLibsUnpackAndPrepare(t *testing.T) {
	const repo = "/var/db/repos/gentoo"
	const cache = "/var/cache/distfiles"
	ebuild := filepath.Join(repo, "sys-libs/binutils-libs/binutils-libs-2.47.ebuild")
	if _, err := os.Stat(ebuild); err != nil {
		t.Skip("binutils-libs-2.47 ebuild is unavailable")
	}
	manifest, err := os.Open(filepath.Join(filepath.Dir(ebuild), "Manifest"))
	if err != nil {
		t.Fatal(err)
	}
	identities, err := distfiles.ParseManifest(manifest)
	manifest.Close()
	if err != nil {
		t.Fatal(err)
	}
	verified := distfiles.VerifiedSet{Directory: cache}
	for _, name := range []string{"binutils-2.47.tar.xz", "binutils-2.47-patches-1.tar.xz"} {
		if _, err := os.Stat(filepath.Join(cache, name)); err != nil {
			t.Skipf("cached distfile is unavailable: %s", name)
		}
		identity, ok := identities[name]
		if !ok {
			t.Fatalf("distfile is absent from Manifest: %s", name)
		}
		verified.Artifacts = append(verified.Artifacts, identity)
	}
	request := phaseStateRequest(t, "")
	request.Ebuild, request.EclassDirs, request.Distfiles = ebuild, []string{filepath.Join(repo, "eclass")}, &verified
	request.SourceDir = filepath.Join(request.WorkDir, "binutils-libs-2.47")
	request.Package = PackageIdentity{Category: "sys-libs", PN: "binutils-libs", PV: "2.47", PR: "r0", P: "binutils-libs-2.47", PVR: "2.47", PF: "binutils-libs-2.47", Slot: "0/2.47", Repository: "gentoo"}
	request.Env = map[string]string{"A": "binutils-2.47.tar.xz binutils-2.47-patches-1.tar.xz", "USE": "", "CHOST": "x86_64-pc-linux-gnu", "CBUILD": "x86_64-pc-linux-gnu", "ABI": "amd64", "DEFAULT_ABI": "amd64", "MULTILIB_ABIS": "amd64"}
	overlay := filepath.Join(request.TempDir, "phase.environment")
	for _, phase := range []string{"src_unpack", "src_prepare"} {
		request.ID, request.Command, request.Phase = "binutils-"+phase, "run_phase", phase
		request.SaveEnvironment = overlay
		if phase == "src_prepare" {
			request.EnvironmentOverlay = overlay
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		events, err := RunBashWorkerWithOptions(ctx, request, WorkerOptions{Isolation: IsolationPortage})
		cancel()
		if err != nil {
			var logs []string
			for _, event := range events {
				if event.Kind == "log" {
					logs = append(logs, event.Message)
				}
			}
			t.Fatalf("actual binutils-libs %s: %v\n%s", phase, err, strings.Join(logs, "\n"))
		}
	}
	if _, err := os.Stat(filepath.Join(request.WorkDir, "binutils-2.47/configure")); err != nil {
		t.Fatal(err)
	}
}
