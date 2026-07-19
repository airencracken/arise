//go:build live_portage

package phaseproto

import (
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/airencracken/arise/internal/distfiles"
)

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
