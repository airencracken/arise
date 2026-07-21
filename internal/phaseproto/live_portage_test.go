//go:build live_portage

package phaseproto

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/distfiles"
)

func readEnvironmentSnapshot(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		name, value, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("malformed environment snapshot line %q", line)
		}
		result[name] = value
	}
	return result, nil
}

func normalizedExecutionEnvironment(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for name, value := range values {
		switch name {
		case "PORTAGE_BUILDDIR", "WORKDIR", "S", "T", "HOME", "D", "ED", "ROOT", "EROOT", "SYSROOT", "ESYSROOT", "BROOT", "PORTAGE_CONFIGROOT":
			if value == "" {
				result[name] = ""
			} else {
				result[name] = "<absolute>"
			}
		default:
			result[name] = value
		}
	}
	return result
}

type imageEntry struct {
	Path, Type, Content string
	Mode                os.FileMode
}

func snapshotImageTree(root string) ([]imageEntry, error) {
	var result []imageEntry
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		item := imageEntry{Path: filepath.ToSlash(relative), Mode: info.Mode().Perm()}
		switch {
		case info.Mode().IsDir():
			item.Type = "directory"
		case info.Mode()&os.ModeSymlink != 0:
			item.Type = "symlink"
			item.Content, err = os.Readlink(path)
		case info.Mode().IsRegular():
			item.Type = "file"
			var raw []byte
			raw, err = os.ReadFile(path)
			if len(raw) >= 4 && bytes.Equal(raw[:4], []byte{0x7f, 'E', 'L', 'F'}) {
				item.Content = "<ELF>"
			} else {
				item.Content = string(raw)
			}
		default:
			return fmt.Errorf("unsupported image object %s (%s)", relative, info.Mode())
		}
		if err != nil {
			return err
		}
		result = append(result, item)
		return nil
	})
	return result, err
}

func TestLivePortageEnvironmentSnapshot(t *testing.T) {
	ebuildCommand, err := exec.LookPath("ebuild")
	if err != nil {
		t.Skip("Portage ebuild command unavailable")
	}
	for _, eapi := range []string{"7", "8"} {
		t.Run("EAPI-"+eapi, func(t *testing.T) {
			compareLivePortageEnvironment(t, ebuildCommand, eapi)
		})
	}
}

func compareLivePortageEnvironment(t *testing.T, ebuildCommand, eapi string) {
	t.Helper()
	directory := t.TempDir()
	repository := filepath.Join(directory, "repo")
	packageDir := filepath.Join(repository, "app-misc", "arise-env-probe")
	for _, path := range []string{packageDir, filepath.Join(repository, "profiles"), filepath.Join(repository, "metadata"), filepath.Join(directory, "distfiles"), filepath.Join(directory, "packages")} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repository, "profiles", "repo_name"), []byte("arise-env-probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "metadata", "layout.conf"), []byte("masters = gentoo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(packageDir, "arise-env-probe-1.ebuild")
	names := []string{"EAPI", "CATEGORY", "PN", "PV", "PR", "P", "PVR", "PF", "SLOT", "PORTAGE_REPO_NAME", "EBUILD_PHASE", "EBUILD_PHASE_FUNC", "PORTAGE_BUILDDIR", "WORKDIR", "S", "T", "HOME", "D", "ED", "ROOT", "EROOT", "SYSROOT", "ESYSROOT", "BROOT", "PORTAGE_CONFIGROOT"}
	sort.Strings(names)
	var body strings.Builder
	fmt.Fprintf(&body, "EAPI=%s\nSLOT=0\nS=${WORKDIR}\nsrc_compile() {\n", eapi)
	for _, name := range names {
		fmt.Fprintf(&body, "  printf '%%s=%%s\\n' %s \"${%s-}\"\n", name, name)
	}
	body.WriteString("} > \"${PORTAGE_BUILDDIR}/environment.snapshot\"\n")
	if err := os.WriteFile(ebuild, []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	portageTmp := filepath.Join(directory, "portage-tmp")
	if err := os.MkdirAll(portageTmp, 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(ebuildCommand, "--skip-manifest", ebuild, "compile")
	currentUser, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	currentGroup, err := user.LookupGroupId(currentUser.Gid)
	if err != nil {
		t.Fatal(err)
	}
	command.Env = append(os.Environ(),
		"PORTAGE_TMPDIR="+portageTmp, "DISTDIR="+filepath.Join(directory, "distfiles"), "PKGDIR="+filepath.Join(directory, "packages"),
		"PORTAGE_USERNAME="+currentUser.Username, "PORTAGE_GRPNAME="+currentGroup.Name,
		"FEATURES=-sandbox -usersandbox -userpriv -network-sandbox -pid-sandbox -ipc-sandbox -mount-sandbox",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Portage environment probe: %v\n%s", err, output)
	}
	matches, err := filepath.Glob(filepath.Join(portageTmp, "portage", "app-misc", "arise-env-probe-1", "environment.snapshot"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("Portage snapshot paths = %#v, error=%v", matches, err)
	}
	portageEnvironment, err := readEnvironmentSnapshot(matches[0])
	if err != nil {
		t.Fatal(err)
	}

	ariseBuild := filepath.Join(directory, "arise-build")
	for _, path := range []string{ariseBuild, filepath.Join(ariseBuild, "temp"), filepath.Join(ariseBuild, "home"), filepath.Join(ariseBuild, "image")} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	request := Request{
		Protocol: Version, ID: "environment-differential", Command: "run_phase", Phase: "src_compile", EAPI: eapi, Ebuild: ebuild,
		WorkDir: ariseBuild, BuildDir: ariseBuild, SourceDir: ariseBuild, ImageDir: filepath.Join(ariseBuild, "image"),
		TempDir: filepath.Join(ariseBuild, "temp"), HomeDir: filepath.Join(ariseBuild, "home"), ConfigRoot: "/",
		Package: PackageIdentity{Category: "app-misc", PN: "arise-env-probe", PV: "1", PR: "r0", P: "arise-env-probe-1", PVR: "1", PF: "arise-env-probe-1", Slot: "0", Repository: "arise-env-probe"},
	}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("Arise environment probe: %v; events=%#v", err, events)
	}
	ariseEnvironment, err := readEnvironmentSnapshot(filepath.Join(ariseBuild, "environment.snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := normalizedExecutionEnvironment(ariseEnvironment), normalizedExecutionEnvironment(portageEnvironment); !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized environment differs\nArise:  %#v\nPortage: %#v", got, want)
	}
}

func TestLivePortageInstallHelperImageTree(t *testing.T) {
	ebuildCommand, err := exec.LookPath("ebuild")
	if err != nil {
		t.Skip("Portage ebuild command unavailable")
	}
	for _, eapi := range []string{"7", "8"} {
		t.Run("EAPI-"+eapi, func(t *testing.T) {
			compareLivePortageHelperImage(t, ebuildCommand, eapi)
		})
	}
}

func compareLivePortageHelperImage(t *testing.T, ebuildCommand, eapi string) {
	t.Helper()
	directory := t.TempDir()
	repository := filepath.Join(directory, "repo")
	packageDir := filepath.Join(repository, "app-misc", "arise-helper-probe")
	for _, path := range []string{packageDir, filepath.Join(repository, "profiles"), filepath.Join(repository, "metadata"), filepath.Join(directory, "distfiles"), filepath.Join(directory, "packages")} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repository, "profiles", "repo_name"), []byte("arise-helper-probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "metadata", "layout.conf"), []byte("masters = gentoo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(packageDir, "arise-helper-probe-1.ebuild")
	body := fmt.Sprintf(`EAPI=%s
SLOT=0
S=${WORKDIR}
src_install() {
  nonfatal false
  [[ $? == 1 ]] || die "nonfatal false status mismatch"
  printf tool > tool
  printf data > data
  printf header > header.h
  printf library > libprobe.so
  printf manual > probe.1
  for i in {1..64}; do printf 'documentation line\n'; done > guide.txt
  printf translation > fr.mo
  into /opt/probe
  dobin tool
  newbin tool renamed
  insinto /etc/probe
  insopts -m0600
  doins data
  newins data renamed.conf
  doheader header.h
  dolib.so libprobe.so
  newlib.so libprobe.so librenamed.so
  diropts -m0700
  dodir /var/lib/probe
  dodir /var/lib/empty
  insinto /var/lib/probe
  doins data
  doinitd tool
  newconfd data probe
  doenvd data
  docinto html
  newdoc data README.probe
  docinto /
  dodoc guide.txt
  doman probe.1
  newman probe.1 renamed.5
  doinfo data
  domo fr.mo
  docompress -x /usr/share/info
  dosym ../../opt/probe/bin/tool /usr/bin/probe-link
  fperms 0750 /opt/probe/bin/renamed
}
`, eapi)
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
		"FEATURES=-sandbox -usersandbox -userpriv -network-sandbox -pid-sandbox -ipc-sandbox -mount-sandbox -strip",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Portage helper probe: %v\n%s", err, output)
	}
	portageImages, err := filepath.Glob(filepath.Join(portageTmp, "portage", "app-misc", "arise-helper-probe-1", "image"))
	if err != nil || len(portageImages) != 1 {
		t.Fatalf("Portage image paths = %#v, error=%v", portageImages, err)
	}

	ariseBuild := filepath.Join(directory, "arise-build")
	ariseImage := filepath.Join(ariseBuild, "image")
	for _, path := range []string{ariseBuild, ariseImage, filepath.Join(ariseBuild, "temp"), filepath.Join(ariseBuild, "home")} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	request := Request{
		Protocol: Version, ID: "helper-image-differential", Command: "run_phase", Phase: "src_install", EAPI: eapi, Ebuild: ebuild,
		WorkDir: ariseBuild, BuildDir: ariseBuild, SourceDir: ariseBuild, ImageDir: ariseImage, TempDir: filepath.Join(ariseBuild, "temp"), HomeDir: filepath.Join(ariseBuild, "home"),
		Package: PackageIdentity{Category: "app-misc", PN: "arise-helper-probe", PV: "1", PR: "r0", P: "arise-helper-probe-1", PVR: "1", PF: "arise-helper-probe-1", Slot: "0", Repository: "arise-helper-probe"},
		Env:     map[string]string{"ABI": "amd64", "PORTAGE_COMPRESS": "bzip2"},
	}
	if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
		t.Fatalf("Arise helper probe: %v; events=%#v", err, events)
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
		if len(ariseTree) != len(portageTree) {
			t.Fatalf("normalized image tree length differs: Arise=%d Portage=%d\nArise: %#v\nPortage: %#v", len(ariseTree), len(portageTree), ariseTree, portageTree)
		}
		for index := range ariseTree {
			if ariseTree[index] != portageTree[index] {
				t.Fatalf("normalized image entry %d differs\nArise:  %#v\nPortage: %#v", index, ariseTree[index], portageTree[index])
			}
		}
		t.Fatal("normalized image trees differ without a differing entry")
	}
}

func signalPackageIdentity() PackageIdentity {
	return PackageIdentity{
		Category: "net-im", PN: "signal-desktop-bin", PV: "8.18.0", PR: "r0",
		P: "signal-desktop-bin-8.18.0", PVR: "8.18.0", PF: "signal-desktop-bin-8.18.0",
		Slot: "0", Repository: "gentoo",
	}
}

func TestLiveSignalEclassPhaseDiscovery(t *testing.T) {
	ebuild := os.Getenv("ARISE_LIVE_SIGNAL_EBUILD")
	if ebuild == "" {
		t.Skip("ARISE_LIVE_SIGNAL_EBUILD is required")
	}
	repository := os.Getenv("ARISE_LIVE_GENTOO_REPO")
	if repository == "" {
		repository = "/var/db/repos/gentoo"
	}
	request := Request{
		Protocol: Version, ID: "live-signal-discovery", Command: "discover_phases", EAPI: "8", Ebuild: ebuild,
		EclassDirs: []string{filepath.Join(repository, "eclass")},
		Package:    signalPackageIdentity(), Env: map[string]string{"USE": ""},
	}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("Signal phase discovery: %v; events=%#v", err, events)
	}
	var phases []string
	for _, event := range events {
		if event.Kind == "phase" {
			phases = append(phases, event.Message)
		}
	}
	want := []string{"src_unpack", "src_prepare", "src_install", "pkg_preinst", "pkg_postinst", "pkg_postrm"}
	if !reflect.DeepEqual(phases, want) {
		t.Fatalf("Signal phases = %v, want %v", phases, want)
	}
}

func TestLiveApulseEclassPhaseDiscovery(t *testing.T) {
	repository := os.Getenv("ARISE_LIVE_GENTOO_REPO")
	if repository == "" {
		repository = "/var/db/repos/gentoo"
	}
	ebuild := filepath.Join(repository, "media-sound", "apulse", "apulse-0.1.14.ebuild")
	if _, err := os.Stat(ebuild); err != nil {
		t.Skipf("apulse ebuild unavailable: %v", err)
	}
	request := Request{
		Protocol: Version, ID: "live-apulse-discovery", Command: "discover_phases", EAPI: "8", Ebuild: ebuild,
		EclassDirs: []string{filepath.Join(repository, "eclass")},
		Package:    PackageIdentity{Category: "media-sound", PN: "apulse", PV: "0.1.14", PR: "r0", P: "apulse-0.1.14", PVR: "0.1.14", PF: "apulse-0.1.14", Slot: "0", Repository: "gentoo"},
		Env:        map[string]string{"USE": "abi_x86_64", "ABI": "amd64", "DEFAULT_ABI": "amd64", "CHOST": "x86_64-pc-linux-gnu"},
	}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("apulse phase discovery: %v; events=%#v", err, events)
	}
	var phases []string
	for _, event := range events {
		if event.Kind == "phase" {
			phases = append(phases, event.Message)
		}
	}
	want := []string{"src_prepare", "src_configure", "src_compile", "src_test", "src_install"}
	if !reflect.DeepEqual(phases, want) {
		t.Fatalf("apulse phases = %v, want %v", phases, want)
	}
}

func TestLiveSignalPrepareAndInstallRehearsal(t *testing.T) {
	ebuild := os.Getenv("ARISE_LIVE_SIGNAL_EBUILD")
	if ebuild == "" {
		t.Skip("ARISE_LIVE_SIGNAL_EBUILD is required")
	}
	repository := os.Getenv("ARISE_LIVE_GENTOO_REPO")
	if repository == "" {
		repository = "/var/db/repos/gentoo"
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "source")
	image := filepath.Join(directory, "image")
	temporary := filepath.Join(directory, "temp")
	paths := []string{
		filepath.Join(source, "opt", "Signal"),
		filepath.Join(source, "usr", "share", "applications"),
		filepath.Join(source, "usr", "share", "icons", "hicolor"),
		filepath.Join(source, "usr", "share", "doc", "signal-desktop"),
		image, temporary,
	}
	for _, path := range paths {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	binary, err := os.ReadFile("/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"signal-desktop", "chrome-sandbox", "chrome_crashpad_handler"} {
		if err := os.WriteFile(filepath.Join(source, "opt", "Signal", name), binary, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "usr", "share", "applications", "signal-desktop.desktop"), []byte("[Desktop Entry]\nExec=/opt/Signal/signal-desktop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "usr", "share", "icons", "hicolor", "signal.png"), []byte("icon"), 0o644); err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte("Signal changes\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "usr", "share", "doc", "signal-desktop", "changelog.gz"), compressed.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	base := Request{
		Protocol: Version, EAPI: "8", Ebuild: ebuild,
		EclassDirs: []string{filepath.Join(repository, "eclass")}, WorkDir: source,
		SourceDir: source, ImageDir: image, TempDir: temporary,
		Package: signalPackageIdentity(), Env: map[string]string{"USE": "", "A": ""},
	}
	for _, phase := range []string{"src_prepare", "src_install"} {
		request := base
		request.ID, request.Command, request.Phase = "live-signal-"+phase, "run_phase", phase
		events, err := RunBashWorker(context.Background(), request)
		if err != nil {
			t.Fatalf("Signal %s: %v; events=%#v", phase, err, events)
		}
	}
	desktop, err := os.ReadFile(filepath.Join(image, "usr", "share", "applications", "signal.desktop"))
	if err != nil || !bytes.Contains(desktop, []byte("Exec=signal-desktop")) {
		t.Fatalf("installed desktop file = %q, error=%v", desktop, err)
	}
	if target, err := os.Readlink(filepath.Join(image, "usr", "bin", "signal-desktop")); err != nil || target != "../../opt/Signal/signal-desktop" {
		t.Fatalf("Signal launcher symlink = %q, error=%v", target, err)
	}
}

func TestLiveSignalVerifiedDistfileImage(t *testing.T) {
	ebuild := os.Getenv("ARISE_LIVE_SIGNAL_EBUILD")
	distfile := os.Getenv("ARISE_LIVE_SIGNAL_DISTFILE")
	if ebuild == "" || distfile == "" {
		t.Skip("ARISE_LIVE_SIGNAL_EBUILD and ARISE_LIVE_SIGNAL_DISTFILE are required")
	}
	repository := os.Getenv("ARISE_LIVE_GENTOO_REPO")
	if repository == "" {
		repository = "/var/db/repos/gentoo"
	}
	manifest, err := os.Open(filepath.Join(filepath.Dir(ebuild), "Manifest"))
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := distfiles.Plan(manifest, "https://updates.signal.org/desktop/apt/pool/s/signal-desktop/signal-desktop_8.18.0_amd64.deb", nil)
	manifest.Close()
	if err != nil || len(artifacts) != 1 {
		t.Fatalf("Signal Manifest plan = %#v, error=%v", artifacts, err)
	}
	if filepath.Base(distfile) != artifacts[0].Name {
		t.Fatalf("Signal distfile name = %q, want %q", filepath.Base(distfile), artifacts[0].Name)
	}
	directory := t.TempDir()
	work := filepath.Join(directory, "work")
	image := filepath.Join(directory, "image")
	temporary := filepath.Join(directory, "temp")
	for _, path := range []string{work, image, temporary} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	base := Request{
		Protocol: Version, EAPI: "8", Ebuild: ebuild,
		EclassDirs: []string{filepath.Join(repository, "eclass")}, WorkDir: work,
		SourceDir: work, ImageDir: image, TempDir: temporary,
		Distfiles: &distfiles.VerifiedSet{Directory: filepath.Dir(distfile), Artifacts: artifacts},
		Package:   signalPackageIdentity(), Env: map[string]string{"USE": "", "A": artifacts[0].Name},
	}
	for _, phase := range []string{"src_unpack", "src_prepare", "src_install"} {
		request := base
		request.ID, request.Command, request.Phase = "live-signal-distfile-"+phase, "run_phase", phase
		events, err := RunBashWorker(context.Background(), request)
		if err != nil {
			t.Fatalf("Signal distfile %s: %v; events=%#v", phase, err, events)
		}
	}
	if _, err := os.Stat(filepath.Join(image, "opt", "Signal", "signal-desktop")); err != nil {
		t.Fatalf("Signal executable missing from image: %v", err)
	}
	if target, err := os.Readlink(filepath.Join(image, "usr", "bin", "signal-desktop")); err != nil || target != "../../opt/Signal/signal-desktop" {
		t.Fatalf("Signal image launcher = %q, error=%v", target, err)
	}
}
