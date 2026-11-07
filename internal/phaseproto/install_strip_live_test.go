//go:build live_portage

package phaseproto

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveInstallStripMatchesPortage(t *testing.T) {
	ebuildCommand, err := exec.LookPath("ebuild")
	if err != nil {
		if os.Getenv("ARISE_REQUIRE_PHASE_CONTRACT") == "1" {
			t.Fatal("required Portage ebuild reference unavailable")
		}
		t.Skip("Portage ebuild command unavailable")
	}
	fixtures := installStripFixtures(t)
	directory := t.TempDir()
	repo := filepath.Join(directory, "repo")
	packageDir := filepath.Join(repo, "app-misc/state-probe")
	for _, path := range []string{packageDir, filepath.Join(repo, "profiles"), filepath.Join(repo, "metadata"), filepath.Join(directory, "portage-tmp"), filepath.Join(directory, "distfiles")} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	body := fmt.Sprintf(`SLOT=0
LICENSE=MIT
KEYWORDS="~amd64"
S=${WORKDIR}
src_install() {
	insinto /usr/lib/probe
	doins %q/{executable,shared.so,member.o,regular.a,thin.a} || die
	newins %q/member.o excluded.ko || die
	dostrip -x /usr/lib/probe/excluded.ko
}`, fixtures, fixtures)
	ebuild := filepath.Join(packageDir, "state-probe-1.ebuild")
	for path, content := range map[string]string{
		filepath.Join(repo, "profiles/repo_name"):   "strip-probe\n",
		filepath.Join(repo, "metadata/layout.conf"): "masters = gentoo\n",
		ebuild: "EAPI=8\n" + body + "\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
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
	command := exec.CommandContext(ctx, ebuildCommand, "--skip-manifest", ebuild, "install")
	command.Env = append(os.Environ(), "PORTAGE_TMPDIR="+filepath.Join(directory, "portage-tmp"),
		"DISTDIR="+filepath.Join(directory, "distfiles"), "PORTAGE_USERNAME="+current.Username, "PORTAGE_GRPNAME="+group.Name,
		"FEATURES=binpkg-dostrip -splitdebug -installsources -compressdebug -sandbox -usersandbox -userpriv -network-sandbox -pid-sandbox -ipc-sandbox -mount-sandbox")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Portage strip reference: %v\n%s", err, output)
	}
	request := phaseStateRequest(t, body)
	request.SourceDir = request.WorkDir
	request.Policy = ExecutionPolicy{Configured: true, Strip: true}
	runPhaseStateSequence(t, request, true, "src_install")
	for _, name := range []string{"executable", "shared.so", "member.o", "regular.a", "thin.a", "excluded.ko"} {
		relative := filepath.Join("usr/lib/probe", name)
		portage := filepath.Join(directory, "portage-tmp/portage/app-misc/state-probe-1/image", relative)
		arise := filepath.Join(request.ImageDir, relative)
		if name == "regular.a" {
			// Archive headers can contain the strip time. Compare the object
			// payload and symbols, including locals, rather than those timestamps.
			for _, arguments := range [][]string{{"ar", "p"}, {"nm", "-a"}} {
				want, err := exec.Command(arguments[0], arguments[1], portage).CombinedOutput()
				if err != nil {
					t.Fatalf("Portage %s: %v\n%s", arguments[0], err, want)
				}
				got, err := exec.Command(arguments[0], arguments[1], arise).CombinedOutput()
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("archive %s differs from Portage: %v", arguments[0], err)
				}
			}
			continue
		}
		if name == "thin.a" || name == "excluded.ko" {
			want, err := os.ReadFile(portage)
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(arise)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("%s differs from Portage: error=%v", name, err)
			}
			continue
		}
		for _, path := range []string{portage, arise} {
			sections, err := exec.Command("readelf", "-S", path).CombinedOutput()
			if err != nil || strings.Contains(string(sections), ".debug_info") {
				t.Fatalf("%s still has debug sections: %v\n%s", path, err, sections)
			}
		}
	}
}

func TestLiveExecutableQASupportsVerboseHelper(t *testing.T) {
	binPath, err := exec.Command("portageq", "envvar", "PORTAGE_BIN_PATH").Output()
	if err != nil {
		if os.Getenv("ARISE_REQUIRE_PHASE_CONTRACT") == "1" {
			t.Fatalf("required Portage QA reference unavailable: %v", err)
		}
		t.Skipf("Portage QA reference unavailable: %v", err)
	}
	check := filepath.Join(strings.TrimSpace(string(binPath)), "install-qa-check.d/10executable-issues")
	if _, err := os.Stat(check); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"cc", "scanelf"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("ARISE_REQUIRE_PHASE_CONTRACT") == "1" {
				t.Fatalf("required executable QA fixture tool unavailable: %s", tool)
			}
			t.Skipf("%s is unavailable", tool)
		}
	}
	request := phaseStateRequest(t, `src_install() {
	insinto /usr/lib/probe
	doins "$WORKDIR/wx.o" || die
}`)
	if err := os.MkdirAll(request.SourceDir, 0755); err != nil {
		t.Fatal(err)
	}
	assembly := filepath.Join(request.WorkDir, "wx.S")
	if err := os.WriteFile(assembly, []byte(".text\n.byte 0\n.section .note.GNU-stack,\"x\",@progbits\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("cc", "-c", assembly, "-o", filepath.Join(request.WorkDir, "wx.o")).CombinedOutput(); err != nil {
		t.Fatalf("executable stack fixture: %v\n%s", err, output)
	}
	target, err := exec.Command("cc", "-dumpmachine").Output()
	if err != nil {
		t.Fatal(err)
	}
	request.InstallQAChecks = []string{check}
	request.Env = map[string]string{"CHOST": strings.TrimSpace(string(target)), "PORTAGE_QUIET": "0"}
	request.ID, request.Command, request.Phase = "executable-qa", "run_phase", "src_install"
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("advisory executable QA terminated the phase: %v; events=%+v", err, events)
	}
	warned := false
	for _, event := range events {
		if strings.Contains(event.Message, "command not found") || strings.Contains(event.Message, "failed to run") {
			t.Fatalf("executable QA helper failed: %+v", event)
		}
		if event.Kind == "elog" && event.Class == "QA" && strings.Contains(event.Message, "writable and executable sections") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("executable stack fixture did not exercise the QA warning branch: %+v", events)
	}
}
