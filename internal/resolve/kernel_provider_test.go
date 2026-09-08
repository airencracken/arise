package resolve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/distfiles"
	"github.com/airencracken/arise/internal/portage"
)

func TestResolve_KernelProviderUpgrade(t *testing.T) {
	for _, targets := range [][]string{{"virtual/dist-kernel"}, {"virtual/dist-kernel", "sys-kernel/gentoo-kernel-bin"}, {"sys-kernel/gentoo-kernel-bin", "virtual/dist-kernel"}} {
		t.Run(strings.Join(targets, ","), func(t *testing.T) {
			g := makeGraph()
			old := pkg(g, "virtual/dist-kernel", "6.18.41", "0", "6.18.41", true, nil)
			old.Rdepend = "|| ( =sys-kernel/gentoo-kernel-6.18.41 =sys-kernel/gentoo-kernel-bin-6.18.41 )"
			next := pkg(g, "virtual/dist-kernel", "6.18.43", "0", "6.18.43", false, nil)
			next.Rdepend = "|| ( =sys-kernel/gentoo-kernel-6.18.43 =sys-kernel/gentoo-kernel-bin-6.18.43 )"
			pkg(g, "sys-kernel/gentoo-kernel", "6.18.43", "6.18.43", "6.18.43", false, nil)
			pkg(g, "sys-kernel/gentoo-kernel-bin", "6.18.41", "6.18.41", "6.18.41", true, nil)
			pkg(g, "sys-kernel/gentoo-kernel-bin", "6.18.43", "6.18.43", "6.18.43", false, nil)
			cfg := DefaultResolveConfig()
			cfg.Update, cfg.Deep, cfg.NewUse = true, true, true
			result, err := Resolve(g, targets, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if actionForCP(result.Install, "sys-kernel/gentoo-kernel") || !actionForCP(result.Install, "sys-kernel/gentoo-kernel-bin") {
				t.Fatalf("changed kernel provider: %v", result.Install)
			}
		})
	}
}

// Provider affinity must not make an unusable branch mandatory or leak its
// partial plan when the resolver falls back to the declared alternative.
func TestResolve_KernelProviderFallbackAtomicity(t *testing.T) {
	for _, mode := range []string{"unavailable", "broken", "other-domain", "not-installed"} {
		t.Run(mode, func(t *testing.T) {
			g := makeGraph()
			parent := pkg(g, "virtual/dist-kernel", "2", "0", "2", false, nil)
			parent.Rdepend = "|| ( =sys-kernel/source-2 =sys-kernel/binary-2 )"
			pkg(g, "sys-kernel/source", "2", "2", "2", false, nil)
			pkg(g, "sys-kernel/binary", "1", "1", "1", mode != "not-installed", nil)
			if mode != "unavailable" {
				next := pkg(g, "sys-kernel/binary", "2", "2", "2", false, nil)
				if mode == "broken" {
					next.Rdepend = "app-misc/partial app-misc/missing"
					pkg(g, "app-misc/partial", "1", "0", "0", false, nil)
				}
			}
			cfg := DefaultResolveConfig()
			cfg.Update, cfg.Deep = true, true
			if mode == "other-domain" {
				cfg.InstalledByDomain = map[DependencyDomain]*DepGraph{DomainROOT: makeGraph()}
			}
			result, err := Resolve(g, []string{"virtual/dist-kernel"}, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Install) != 2 || !actionForCP(result.Install, "sys-kernel/source") || len(result.Conflicts) != 0 {
				t.Fatalf("invalid fallback plan: %#v", result)
			}
		})
	}
}

func TestResolve_KernelArchitectureDownloadContract(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "ppc64", "x86"} {
		t.Run(arch, func(t *testing.T) {
			g := makeGraph()
			vi := pkgKeywords(g, "sys-kernel/gentoo-kernel-bin", "2", "2", "2", false, map[string]bool{"initramfs": true}, arch)
			vi.SrcURI = "amd64? ( https://example/amd64.tar ) arm64? ( https://example/arm64.tar ) ppc64? ( https://example/ppc64.tar ) x86? ( https://example/x86.tar )"
			vi.RepositoryPath = t.TempDir()
			dir := filepath.Join(vi.RepositoryPath, "sys-kernel", "gentoo-kernel-bin")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "Manifest"), []byte("DIST "+arch+".tar 467005440 SHA512 aa\n"), 0644); err != nil {
				t.Fatal(err)
			}
			cfg := DefaultResolveConfig()
			cfg.PortageConfig = &portage.Config{MakeConf: map[string]string{"ARCH": arch}, UseForce: []string{arch}, USE: []string{"unrelated"}}
			result, err := Resolve(g, []string{"sys-kernel/gentoo-kernel-bin"}, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Install) != 1 {
				t.Fatalf("unexpected plan: %#v", result)
			}
			action := result.Install[0]
			size, err := distfiles.ManifestDownloadSize(action.RepositoryPath, action.Atom.Category, action.Atom.Package, action.SrcURI, "", action.UseFlags)
			if err != nil || size != 467005440 {
				t.Fatalf("architecture download size = %d, %v", size, err)
			}
			if action.UseFlags["unrelated"] || vi.UseFlags[arch] {
				t.Fatal("effective flags leaked into declared IUSE or included unrelated USE")
			}
		})
	}
}
