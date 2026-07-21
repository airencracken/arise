//go:build live_portage

package phaseproto

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"testing"

	mergepkg "github.com/airencracken/arise/internal/merge"
)

type buildSystemFixture struct {
	name, ebuild  string
	tools         []string
	smokeArgs     []string
	wantOutput    string
	configProtect bool
	files         map[string]struct {
		content string
		mode    os.FileMode
	}
}

func TestLivePortageRepresentativeBuildSystems(t *testing.T) {
	for _, fixture := range representativeBuildSystems() {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			for _, eapi := range []string{"7", "8"} {
				t.Run("EAPI-"+eapi, func(t *testing.T) {
					compareRepresentativeBuildSystem(t, fixture, eapi)
				})
			}
		})
	}
}

func representativeBuildSystems() []buildSystemFixture {
	commonC := `#include <stdio.h>
int main(void) { puts("arise-build-system"); return 0; }
`
	return []buildSystemFixture{
		{
			name: "trivial",
			ebuild: `src_unpack() { mkdir -p "${S}"; cp -R "${FILESDIR}/project/." "${S}/"; }
src_install() { dobin arise-build-system; }
`,
			wantOutput: "arise-build-system\n",
			files: map[string]struct {
				content string
				mode    os.FileMode
			}{"arise-build-system": {content: "#!/bin/sh\nprintf 'arise-build-system\\n'\n", mode: 0o755}},
		},
		{
			name:  "autotools-defaults",
			tools: []string{"gcc", "make"}, wantOutput: "arise-build-system\n",
			ebuild: `src_unpack() { mkdir -p "${S}"; cp -R "${FILESDIR}/project/." "${S}/"; }
`,
			files: map[string]struct {
				content string
				mode    os.FileMode
			}{
				"hello.c": {content: commonC, mode: 0o644},
				"configure": {content: `#!/bin/sh
cat > Makefile <<'EOF'
all:
	gcc -O2 -s -o arise-build-system hello.c
check: all
	./arise-build-system | grep -q arise-build-system
install: all
	install -D -m0755 arise-build-system "$(DESTDIR)/usr/bin/arise-build-system"
EOF
`, mode: 0o755},
			},
		},
		{
			name:  "cmake",
			tools: []string{"gcc", "cmake"}, wantOutput: "arise-build-system\n",
			ebuild: `src_unpack() { mkdir -p "${S}"; cp -R "${FILESDIR}/project/." "${S}/"; }
src_configure() { cmake -S "${S}" -B "${WORKDIR}/cmake-build" -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX=/usr; }
src_compile() { cmake --build "${WORKDIR}/cmake-build" --parallel 1; }
src_test() { "${WORKDIR}/cmake-build/arise-build-system" | grep -q arise-build-system; }
src_install() { DESTDIR="${D}" cmake --install "${WORKDIR}/cmake-build"; }
`,
			files: map[string]struct {
				content string
				mode    os.FileMode
			}{
				"hello.c": {content: commonC, mode: 0o644},
				"CMakeLists.txt": {content: `cmake_minimum_required(VERSION 3.20)
project(arise_build_system C)
add_executable(arise-build-system hello.c)
install(TARGETS arise-build-system RUNTIME DESTINATION bin)
`, mode: 0o644},
			},
		},
		{
			name:  "meson",
			tools: []string{"gcc", "meson", "ninja"}, wantOutput: "arise-build-system\n",
			ebuild: `src_unpack() { mkdir -p "${S}"; cp -R "${FILESDIR}/project/." "${S}/"; }
src_configure() { meson setup "${WORKDIR}/meson-build" "${S}" --buildtype=release --prefix=/usr; }
src_compile() { meson compile -C "${WORKDIR}/meson-build" -j 1; }
src_test() { meson test -C "${WORKDIR}/meson-build" --print-errorlogs; }
src_install() { DESTDIR="${D}" meson install -C "${WORKDIR}/meson-build" --no-rebuild; }
`,
			files: map[string]struct {
				content string
				mode    os.FileMode
			}{
				"hello.c": {content: commonC, mode: 0o644},
				"meson.build": {content: `project('arise-build-system', 'c')
exe = executable('arise-build-system', 'hello.c', install: true)
test('smoke', exe)
`, mode: 0o644},
			},
		},
		{
			name:  "go",
			tools: []string{"go"}, wantOutput: "arise-build-system\n",
			ebuild: `src_unpack() { mkdir -p "${S}"; cp -R "${FILESDIR}/project/." "${S}/"; }
src_compile() { GO111MODULE=off go build -trimpath -ldflags='-s -w' -o arise-build-system main.go; }
src_test() { ./arise-build-system | grep -q arise-build-system; }
src_install() { dobin arise-build-system; }
`,
			files: map[string]struct {
				content string
				mode    os.FileMode
			}{"main.go": {content: "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"arise-build-system\") }\n", mode: 0o644}},
		},
		{
			name:  "python",
			tools: []string{"python3"}, wantOutput: "arise-build-system\n",
			ebuild: `src_unpack() { mkdir -p "${S}"; cp -R "${FILESDIR}/project/." "${S}/"; }
src_compile() { python3 -m py_compile arise_probe.py; }
src_test() { python3 arise-build-system | grep -q arise-build-system; }
src_install() {
  dobin arise-build-system
  insinto /usr/lib/python3.13/site-packages
  doins arise_probe.py
}
`,
			files: map[string]struct {
				content string
				mode    os.FileMode
			}{
				"arise_probe.py":     {content: "MESSAGE = 'arise-build-system'\n", mode: 0o644},
				"arise-build-system": {content: "#!/usr/bin/python3\nprint('arise-build-system')\n", mode: 0o755},
			},
		},
		{
			name:  "rust",
			tools: []string{"rustc"}, wantOutput: "arise-build-system\n",
			ebuild: `src_unpack() { mkdir -p "${S}"; cp -R "${FILESDIR}/project/." "${S}/"; }
src_compile() { rustc -C opt-level=2 -C strip=symbols -o arise-build-system main.rs; }
src_test() { ./arise-build-system | grep -q arise-build-system; }
src_install() { dobin arise-build-system; }
`,
			files: map[string]struct {
				content string
				mode    os.FileMode
			}{"main.rs": {content: "fn main() { println!(\"arise-build-system\"); }\n", mode: 0o644}},
		},
		{
			name:      "binary-only",
			smokeArgs: []string{"arise-build-system"}, wantOutput: "arise-build-system\n",
			ebuild: `src_unpack() { mkdir -p "${S}"; }
src_install() { newbin /bin/echo arise-build-system; }
`,
			files: map[string]struct {
				content string
				mode    os.FileMode
			}{},
		},
		{
			name:  "kernel-module",
			tools: []string{"gcc"},
			ebuild: `src_unpack() { mkdir -p "${S}"; cp -R "${FILESDIR}/project/." "${S}/"; }
src_compile() { gcc -O2 -c -o arise.ko module.c; }
src_install() { insinto /lib/modules/6.6.0-arise/extra; doins arise.ko; }
`,
			files: map[string]struct {
				content string
				mode    os.FileMode
			}{"module.c": {content: "int arise_module_probe(void) { return 42; }\n", mode: 0o644}},
		},
		{
			name:          "config-protected",
			configProtect: true,
			ebuild: `src_unpack() { mkdir -p "${S}"; cp -R "${FILESDIR}/project/." "${S}/"; }
src_install() { insinto /etc; newins arise.conf arise.conf; }
`,
			files: map[string]struct {
				content string
				mode    os.FileMode
			}{"arise.conf": {content: "setting=package\n", mode: 0o644}},
		},
	}
}

func compareRepresentativeBuildSystem(t *testing.T, fixture buildSystemFixture, eapi string) {
	t.Helper()
	for _, tool := range fixture.tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is unavailable", tool)
		}
	}
	ebuildCommand, err := exec.LookPath("ebuild")
	if err != nil {
		t.Skip("Portage ebuild command unavailable")
	}
	directory := t.TempDir()
	repository := filepath.Join(directory, "repo")
	packageDir := filepath.Join(repository, "app-misc", "arise-build-system")
	projectDir := filepath.Join(packageDir, "files", "project")
	for _, path := range []string{projectDir, filepath.Join(repository, "profiles"), filepath.Join(repository, "metadata"), filepath.Join(directory, "distfiles"), filepath.Join(directory, "packages")} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repository, "profiles", "repo_name"), []byte("arise-build-system\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "metadata", "layout.conf"), []byte("masters = gentoo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, file := range fixture.files {
		if err := os.WriteFile(filepath.Join(projectDir, name), []byte(file.content), file.mode); err != nil {
			t.Fatal(err)
		}
	}
	ebuild := filepath.Join(packageDir, "arise-build-system-1.ebuild")
	body := fmt.Sprintf("EAPI=%s\nSLOT=0\nS=${WORKDIR}/source\n%s", eapi, fixture.ebuild)
	if err := os.WriteFile(ebuild, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	portageTmp := filepath.Join(directory, "portage-tmp")
	if err := os.MkdirAll(portageTmp, 0o755); err != nil {
		t.Fatal(err)
	}
	currentUser, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	currentGroup, err := user.LookupGroupId(currentUser.Gid)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(ebuildCommand, "--skip-manifest", ebuild, "install")
	command.Env = append(os.Environ(),
		"PORTAGE_TMPDIR="+portageTmp, "DISTDIR="+filepath.Join(directory, "distfiles"), "PKGDIR="+filepath.Join(directory, "packages"),
		"PORTAGE_USERNAME="+currentUser.Username, "PORTAGE_GRPNAME="+currentGroup.Name,
		"PORTAGE_INST_UID="+currentUser.Uid, "PORTAGE_INST_GID="+currentUser.Gid,
		"FEATURES=-sandbox -usersandbox -userpriv -network-sandbox -pid-sandbox -ipc-sandbox -mount-sandbox -strip test",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Portage %s probe: %v\n%s", fixture.name, err, output)
	}
	portageImages, err := filepath.Glob(filepath.Join(portageTmp, "portage", "app-misc", "arise-build-system-1", "image"))
	if err != nil || len(portageImages) != 1 {
		t.Fatalf("Portage image paths = %#v, error=%v", portageImages, err)
	}

	ariseBuild := filepath.Join(directory, "arise-build")
	ariseSource, ariseImage := filepath.Join(ariseBuild, "source"), filepath.Join(ariseBuild, "image")
	for _, path := range []string{ariseBuild, ariseImage, filepath.Join(ariseBuild, "temp"), filepath.Join(ariseBuild, "home")} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	request := Request{
		Protocol: Version, ID: "build-system-" + fixture.name, Command: "run_phases",
		Phases: []string{"src_unpack", "src_prepare", "src_configure", "src_compile", "src_test", "src_install"}, EAPI: eapi, Ebuild: ebuild,
		WorkDir: ariseBuild, BuildDir: ariseBuild, SourceDir: ariseSource, ImageDir: ariseImage,
		TempDir: filepath.Join(ariseBuild, "temp"), HomeDir: filepath.Join(ariseBuild, "home"),
		Package: PackageIdentity{Category: "app-misc", PN: "arise-build-system", PV: "1", PR: "r0", P: "arise-build-system-1", PVR: "1", PF: "arise-build-system-1", Slot: "0", Repository: "arise-build-system"},
		Env:     map[string]string{"CC": "gcc", "MAKEOPTS": "-j1"}, Policy: ExecutionPolicy{Configured: true, Tests: true},
	}
	if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
		t.Fatalf("Arise %s probe: %v; events=%#v", fixture.name, err, events)
	}
	portageTree, err := snapshotImageTree(portageImages[0])
	if err != nil {
		t.Fatal(err)
	}
	ariseTree, err := snapshotImageTree(ariseImage)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ariseTree, portageTree) {
		t.Fatalf("%s normalized image differs\nArise: %#v\nPortage: %#v", fixture.name, ariseTree, portageTree)
	}
	if fixture.configProtect {
		compareConfigProtectedMerge(t, ebuildCommand, command.Env, ebuild, ariseImage, directory)
	}
	if fixture.wantOutput == "" {
		return
	}
	for _, binary := range []string{filepath.Join(ariseImage, "usr/bin/arise-build-system"), filepath.Join(portageImages[0], "usr/bin/arise-build-system")} {
		output, err := exec.Command(binary, fixture.smokeArgs...).CombinedOutput()
		if err != nil || string(output) != fixture.wantOutput {
			t.Fatalf("%s smoke output = %q, error=%v", binary, output, err)
		}
	}
}

func compareConfigProtectedMerge(t *testing.T, ebuildCommand string, baseEnv []string, ebuild, ariseImage, directory string) {
	t.Helper()
	portageRoot, ariseRoot := filepath.Join(directory, "portage-root"), filepath.Join(directory, "arise-root")
	for _, root := range []string{portageRoot, ariseRoot} {
		if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "etc", "arise.conf"), []byte("setting=local\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command(ebuildCommand, "--skip-manifest", ebuild, "merge")
	command.Env = append(baseEnv,
		"ROOT="+portageRoot+string(os.PathSeparator),
		"EROOT="+portageRoot+string(os.PathSeparator),
		"CONFIG_PROTECT=/etc",
		"CONFIG_PROTECT_MASK=",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Portage config-protected merge: %v\n%s", err, output)
	}
	if err := mergepkg.Merge(context.Background(), ariseImage, mergepkg.MergeConfig{
		RootDir: ariseRoot, VdbDir: filepath.Join(ariseRoot, "var", "db", "pkg"),
		Category: "app-misc", Package: "arise-build-system", Version: "1",
		JournalDir: filepath.Join(directory, "journals"), ConfigProtect: []string{"/etc"},
	}); err != nil {
		t.Fatalf("Arise config-protected merge: %v", err)
	}
	for _, name := range []string{"arise.conf", "._cfg0000_arise.conf"} {
		portageData, portageErr := os.ReadFile(filepath.Join(portageRoot, "etc", name))
		ariseData, ariseErr := os.ReadFile(filepath.Join(ariseRoot, "etc", name))
		if portageErr != nil || ariseErr != nil || !reflect.DeepEqual(ariseData, portageData) {
			t.Fatalf("protected %s: Arise=%q (%v), Portage=%q (%v)", name, ariseData, ariseErr, portageData, portageErr)
		}
	}
}
