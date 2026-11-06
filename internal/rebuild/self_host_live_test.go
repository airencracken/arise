//go:build live_portage

package rebuild

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/portage"
)

// TestLiveAriseSelfHost builds the real, pinned release ebuild through Arise's
// rebuild driver and installs it only into a disposable root. Release automation
// supplies its staged overlay and verified distfiles, before publication.
func TestLiveAriseSelfHost(t *testing.T) {
	ebuild := os.Getenv("ARISE_SELF_EBUILD")
	distfiles := os.Getenv("ARISE_SELF_DISTFILES")
	if ebuild == "" || distfiles == "" {
		t.Skip("ARISE_SELF_EBUILD and ARISE_SELF_DISTFILES are required")
	}
	if !filepath.IsAbs(ebuild) || !filepath.IsAbs(distfiles) {
		t.Fatal("self-host inputs must be absolute")
	}
	pf := strings.TrimSuffix(filepath.Base(ebuild), ".ebuild")
	if !strings.HasPrefix(pf, "arise-") || !strings.HasSuffix(ebuild, ".ebuild") {
		t.Fatal("expected the real Arise source ebuild")
	}
	repo := filepath.Dir(filepath.Dir(filepath.Dir(ebuild)))
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.MkdirAll(filepath.Join(root, "var/db/pkg"), 0755); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	cfg := RebuildConfig{
		RepoDir: repo, RootDir: root, BrootDir: "/", VdbDir: filepath.Join(root, "var/db/pkg"),
		DistfilesDir: distfiles, WorkDirBase: filepath.Join(base, "work"), Arch: "amd64", MAKEOPTS: "-j2",
		PhaseLogDir: filepath.Join(base, "logs"), JournalDir: filepath.Join(base, "journals"),
		PhaseProtocol: true, Repository: "arise-overlay",
		UseFlags: map[string]bool{"test": true}, PortageConfig: &portage.Config{MakeConf: map[string]string{"FEATURES": "sandbox test"}},
		Repositories: []portage.RepoEntry{{Name: "arise-overlay", Location: repo, Masters: []string{"gentoo"}}, {Name: "gentoo", Location: "/var/db/repos/gentoo"}},
		OnPhaseStart: func(phase string) { seen[phase] = true; t.Logf("self-host %s", phase) },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	if err := RebuildPackage(ctx, "sys-apps/"+pf, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"pkg_setup", "src_unpack", "src_prepare", "src_configure", "src_compile", "src_test", "src_install", "pkg_preinst", "pkg_postinst"} {
		if !seen[phase] {
			t.Fatalf("self-host did not execute %s", phase)
		}
	}
	binary := filepath.Join(root, "usr/bin/arise")
	version, err := exec.CommandContext(ctx, binary, "--version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(version)) != "arise "+strings.TrimPrefix(pf, "arise-") {
		t.Fatalf("self-host binary = %q, %v", version, err)
	}
	for _, relative := range []string{"usr/share/bash-completion/completions/arise", "var/db/pkg/sys-apps/" + pf + "/environment.bz2"} {
		if _, err := os.Stat(filepath.Join(root, relative)); err != nil {
			t.Fatal(err)
		}
	}
}
