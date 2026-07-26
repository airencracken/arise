package phaseproto

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/distfiles"
)

func TestRunWorkerCommandStreamsValidatedEvents(t *testing.T) {
	request := Request{ID: "stream-test"}
	command := exec.CommandContext(context.Background(), "bash", "-c", `printf '%s\n' '{"protocol":1,"id":"stream-test","sequence":0,"kind":"phase","message":"src_install"}'; sleep 3; printf '%s\n' '{"protocol":1,"id":"stream-test","sequence":1,"kind":"result","exit_code":0}'`)
	observed := make(chan Event, 2)
	done := make(chan error, 1)
	go func() {
		_, err := runWorkerCommandWithEvents(command, request, func(event Event) { observed <- event })
		done <- err
	}()
	select {
	case event := <-observed:
		if event.Kind != "phase" || event.Message != "src_install" {
			t.Fatalf("first event=%+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("phase event was buffered until worker exit")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestBashWorkerImageAndArchiveHelperABI(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source")
	image := filepath.Join(directory, "image")
	temporary := filepath.Join(directory, "temp")
	for _, path := range []string{filepath.Join(source, "payload"), filepath.Join(source, "docs"), image, temporary} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "payload", "program"), []byte("program"), 0o644); err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte("changes\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "docs", "changes.gz"), compressed.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := `EAPI=8
S="${WORKDIR}"
src_prepare() { default; unpack docs/changes.gz || die; }
src_install() {
  insinto /opt/example
  doins payload/program changes
  fperms +x /opt/example/program
  dosym ../../opt/example/program /usr/bin/example
  dosym -r /usr/lib/python-exec/python-exec2 /usr/bin/meson-format-array
  exeinto /usr/lib/python-exec/python3.14
  newexe - gpep517 <<-EOF
	#!/usr/bin/python
	print("gpep517")
	EOF
}
`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"src_prepare", "src_install"} {
		request := Request{Protocol: Version, ID: "helpers-" + phase, Command: "run_phase", Phase: phase, EAPI: "8", Ebuild: ebuild, WorkDir: source, SourceDir: source, ImageDir: image, TempDir: temporary, UserPatchDirs: []string{filepath.Join(directory, "missing-patches")}}
		// Missing patch directories are ignored by the worker's explicit list;
		// policy construction filters them before production requests.
		request.UserPatchDirs = nil
		events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
		if err != nil {
			t.Fatalf("%s: %v; events=%#v", phase, err, events)
		}
	}
	program := filepath.Join(image, "opt", "example", "program")
	info, err := os.Stat(program)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("program mode = %o, want executable", info.Mode().Perm())
	}
	data, err := os.ReadFile(filepath.Join(image, "opt", "example", "changes"))
	if err != nil || string(data) != "changes\n" {
		t.Fatalf("installed changes = %q, error=%v", data, err)
	}
	target, err := os.Readlink(filepath.Join(image, "usr", "bin", "example"))
	if err != nil || target != "../../opt/example/program" {
		t.Fatalf("installed symlink = %q, error=%v", target, err)
	}
	pythonWrapper, err := os.Readlink(filepath.Join(image, "usr", "bin", "meson-format-array"))
	if err != nil || pythonWrapper != "../lib/python-exec/python-exec2" {
		t.Fatalf("relative Python wrapper = %q, error=%v", pythonWrapper, err)
	}
	if _, err := os.Lstat(filepath.Join(image, "usr", "lib", "python-exec", "python-exec2")); !os.IsNotExist(err) {
		t.Fatalf("dosym -r created its target in the image: %v", err)
	}
	generated, err := os.ReadFile(filepath.Join(image, "usr", "lib", "python-exec", "python3.14", "gpep517"))
	if err != nil || string(generated) != "#!/usr/bin/python\nprint(\"gpep517\")\n" {
		t.Fatalf("newexe stdin output = %q, error=%v", generated, err)
	}
}

func TestBashWorkerVersionCutAndSeparatorReplacement(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "libsoup-2.74.3-r1.ebuild")
	content := `EAPI=7
src_unpack() {
  printf '%s\n' "$(ver_cut 1 2.74.3)" "$(ver_cut 1-2 2.74.3)" "$(ver_rs 1- . 2.74.3-r1)" > "${T}/versions"
}
`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(directory, "temp")
	for _, path := range []string{temporary, filepath.Join(directory, "work"), filepath.Join(directory, "source"), filepath.Join(directory, "image")} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	request := Request{Protocol: Version, ID: "version-helpers", Command: "run_phase", Phase: "src_unpack", EAPI: "7", Ebuild: ebuild, WorkDir: filepath.Join(directory, "work"), SourceDir: filepath.Join(directory, "source"), ImageDir: filepath.Join(directory, "image"), TempDir: temporary}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("version helpers: %v; events=%#v", err, events)
	}
	data, err := os.ReadFile(filepath.Join(temporary, "versions"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "2\n2.74\n2.74.3.r.1\n"; got != want {
		t.Fatalf("version helper output=%q, want %q", got, want)
	}
}

func TestBashWorkerCoreInstallHelperFamily(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source")
	image := filepath.Join(directory, "image")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"tool": "tool", "data": "data", "header.h": "header", "libdemo.a": "archive", "libdemo.so": "shared", "tool.1": "manual", "service": "service", "config": "config", "environment": "environment"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := `EAPI=8
src_install() {
  into /opt/demo
  dobin tool
  newbin tool renamed
  dosbin tool
  newsbin tool renamed-admin
  exeinto /usr/libexec/demo
  exeopts -m0710
  doexe tool
  newexe tool renamed-helper
  insinto /etc/demo
  insopts -m0600
  doins data
  newins data renamed.conf
  doheader header.h
  newheader header.h renamed.hpp
  dolib.a libdemo.a
  dolib.so libdemo.so
  newlib.a libdemo.a librenamed.a
  newlib.so libdemo.so librenamed.so
  diropts -m0700
  dodir /var/lib/demo
  keepdir /var/lib/demo/empty
  doinitd service
  newinitd service renamed-service
  doconfd config
  newconfd config renamed-config
  doenvd environment
  newenvd environment 99demo
  docinto html
  newdoc data README.demo
  doman tool.1
  newman tool.1 renamed.5
  doinfo data
  fowners "${UID}:$(id -g)" /etc/demo/data
}`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "install-family", Command: "run_phase", Phase: "src_install", EAPI: "8", Ebuild: ebuild, WorkDir: source, SourceDir: source, ImageDir: image, Package: PackageIdentity{Category: "app-misc", PN: "pkg", PV: "1", PR: "r0", P: "pkg-1", PVR: "1", PF: "pkg-1", Slot: "0", Repository: "test"}, Env: map[string]string{"ABI": "amd64"}}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("install helper family: %v; events=%#v", err, events)
	}
	wantModes := map[string]os.FileMode{
		"opt/demo/bin/tool": 0o755, "opt/demo/bin/renamed": 0o755,
		"opt/demo/sbin/tool": 0o755, "opt/demo/sbin/renamed-admin": 0o755,
		"usr/libexec/demo/tool": 0o710, "usr/libexec/demo/renamed-helper": 0o710,
		"etc/demo/data": 0o600, "etc/demo/renamed.conf": 0o600,
		"usr/include/header.h": 0o644, "usr/include/renamed.hpp": 0o644,
		"opt/demo/lib64/libdemo.a": 0o644, "opt/demo/lib64/libdemo.so": 0o755,
		"opt/demo/lib64/librenamed.a": 0o644, "opt/demo/lib64/librenamed.so": 0o755,
		"etc/init.d/service": 0o755, "etc/init.d/renamed-service": 0o755,
		"etc/conf.d/config": 0o644, "etc/conf.d/renamed-config": 0o644,
		"etc/env.d/environment": 0o644, "etc/env.d/99demo": 0o644,
		"usr/share/doc/pkg-1/html/README.demo": 0o644,
		"usr/share/man/man1/tool.1":            0o644, "usr/share/man/man5/renamed.5": 0o644,
		"usr/share/info/data": 0o644,
	}
	for relative, wantMode := range wantModes {
		info, err := os.Stat(filepath.Join(image, relative))
		if err != nil || infoMode(info) != wantMode {
			t.Errorf("%s mode = %v, error=%v; want %v", relative, infoMode(info), err, wantMode)
		}
	}
	if _, err := os.Stat(filepath.Join(image, "var/lib/demo/empty/.keep_app-misc_pkg_0")); err != nil {
		t.Fatalf("keepdir marker: %v", err)
	}
	for _, relative := range []string{"var/lib/demo", "var/lib/demo/empty"} {
		info, err := os.Stat(filepath.Join(image, relative))
		if err != nil || infoMode(info) != 0o700 {
			t.Errorf("%s directory mode = %v, error=%v", relative, infoMode(info), err)
		}
	}
	for _, relative := range []string{"etc/conf.d", "etc/env.d", "usr/include"} {
		info, err := os.Stat(filepath.Join(image, relative))
		if err != nil || infoMode(info) != 0o755 {
			t.Errorf("%s fixed directory mode = %v, error=%v", relative, infoMode(info), err)
		}
	}
	ownedInfo, err := os.Stat(filepath.Join(image, "etc/demo/data"))
	if err != nil {
		t.Fatal(err)
	}
	ownedStat := ownedInfo.Sys().(*syscall.Stat_t)
	if int(ownedStat.Uid) != os.Getuid() || int(ownedStat.Gid) != os.Getgid() {
		t.Fatalf("fowners owner = %d:%d, want %d:%d", ownedStat.Uid, ownedStat.Gid, os.Getuid(), os.Getgid())
	}
}

func infoMode(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode().Perm()
}

func TestBashWorkerOpenRCHelpersAcceptGeneratedStandardInput(t *testing.T) {
	directory := t.TempDir()
	image := filepath.Join(directory, "image")
	ebuild := filepath.Join(directory, "postgresql-18.4.ebuild")
	content := `EAPI=8
IUSE="+server"
src_install() {
	if use server; then
		printf 'PGDATA=/var/lib/postgresql/18/data\n' | newconfd - postgresql-18
		printf '#!/sbin/openrc-run\ncommand=/usr/bin/postgres18\n' | newinitd - postgresql-18
	fi
}`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Protocol: Version, ID: "openrc-generated-stdin", Command: "run_phase", Phase: "src_install",
		EAPI: "8", Ebuild: ebuild, WorkDir: directory, SourceDir: directory, ImageDir: image,
		Package: PackageIdentity{Category: "dev-db", PN: "postgresql", PV: "18.4", PR: "r0", P: "postgresql-18.4", PVR: "18.4", PF: "postgresql-18.4", Slot: "18", Repository: "test"},
		Env:     map[string]string{"USE": "server"},
	}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("generated OpenRC helpers: %v; events=%#v", err, events)
	}
	for relative, want := range map[string]string{
		"etc/conf.d/postgresql-18": "PGDATA=/var/lib/postgresql/18/data\n",
		"etc/init.d/postgresql-18": "#!/sbin/openrc-run\ncommand=/usr/bin/postgres18\n",
	} {
		got, readErr := os.ReadFile(filepath.Join(image, relative))
		if readErr != nil {
			t.Fatalf("read %s: %v", relative, readErr)
		}
		if string(got) != want {
			t.Fatalf("%s=%q want=%q", relative, got, want)
		}
	}
	if info, err := os.Stat(filepath.Join(image, "etc/init.d/postgresql-18")); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("init script mode=%v error=%v", infoMode(info), err)
	}
}

func TestBashWorkerInstallHelperFailureCannotBeMaskedByLaterCommand(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := "EAPI=8\nsrc_install() { newinitd missing-source service; :; }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Protocol: Version, ID: "install-helper-failure", Command: "run_phase", Phase: "src_install",
		EAPI: "8", Ebuild: ebuild, WorkDir: directory, SourceDir: directory,
		ImageDir: filepath.Join(directory, "image"),
	}
	events, err := RunBashWorker(context.Background(), request)
	if err == nil {
		t.Fatalf("missing helper source was masked by later success; events=%#v", events)
	}
}

func TestBashWorkerInstallHelpersRejectImageEscape(t *testing.T) {
	for name, command := range map[string]string{
		"directory":   "dodir /safe/../../../escaped",
		"permissions": "fperms 0644 /safe/../../../escaped",
		"symlink":     "dosym target /safe/../../../escaped",
		"hardlink":    "dohard /safe/source /safe/escaped",
		"documents":   "docinto ../../escaped",
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			image := filepath.Join(directory, "image")
			if err := os.MkdirAll(image, 0o755); err != nil {
				t.Fatal(err)
			}
			ebuild := filepath.Join(directory, "pkg-1.ebuild")
			if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_install() { "+command+"; }\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			request := Request{Protocol: Version, ID: "install-escape-" + name, Command: "run_phase", Phase: "src_install", EAPI: "8", Ebuild: ebuild, WorkDir: directory, SourceDir: directory, ImageDir: image}
			if _, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err == nil {
				t.Fatal("image destination traversal was accepted")
			}
			if _, err := os.Stat(filepath.Join(directory, "escaped")); !os.IsNotExist(err) {
				t.Fatalf("image helper escaped D: %v", err)
			}
		})
	}
}

func TestBashWorkerInstallHelpersRejectInvalidArguments(t *testing.T) {
	commands := map[string]string{
		"diropts-empty":   "diropts",
		"libopts-empty":   "libopts",
		"newheader-name":  "newheader data ../escaped.h",
		"newlib-name":     "newlib.so data ../escaped.so",
		"newman-section":  "newman data invalid-name",
		"newinfo-missing": "newinfo data renamed.info",
		"dohard-banned":   "dohard /source /destination",
		"dohtml-banned":   "dohtml data",
		"dosed-banned":    "dosed data",
		"dolib-banned":    "dolib data",
	}
	for name, command := range commands {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.WriteFile(filepath.Join(directory, "data"), []byte("data"), 0o644); err != nil {
				t.Fatal(err)
			}
			ebuild := filepath.Join(directory, "pkg-1.ebuild")
			if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_install() { "+command+"; }\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			request := Request{Protocol: Version, ID: "install-invalid-" + name, Command: "run_phase", Phase: "src_install", EAPI: "8", Ebuild: ebuild, WorkDir: directory, SourceDir: directory, ImageDir: filepath.Join(directory, "image")}
			if _, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err == nil {
				t.Fatalf("invalid helper call %q succeeded", command)
			}
		})
	}
}

func TestBashWorkerNewinsAcceptsStandardInput(t *testing.T) {
	directory := t.TempDir()
	image := filepath.Join(directory, "image")
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := "EAPI=8\nsrc_install() { newins - generated.conf < <(printf '%s' generated); }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "newins-stdin", Command: "run_phase", Phase: "src_install", EAPI: "8", Ebuild: ebuild, WorkDir: directory, SourceDir: directory, ImageDir: image}
	if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
		t.Fatalf("newins stdin: %v; events=%#v", err, events)
	}
	installed, err := os.ReadFile(filepath.Join(image, "generated.conf"))
	if err != nil || string(installed) != "generated" {
		t.Fatalf("installed stdin = %q, error=%v", installed, err)
	}
}

func TestBashWorkerEAPI8PrunesOnlyUnclaimedEmptyImageDirectories(t *testing.T) {
	for _, eapi := range []string{"7", "8"} {
		t.Run("EAPI-"+eapi, func(t *testing.T) {
			directory := t.TempDir()
			image := filepath.Join(directory, "image")
			if err := os.MkdirAll(image, 0o755); err != nil {
				t.Fatal(err)
			}
			ebuild := filepath.Join(directory, "pkg-1.ebuild")
			content := "EAPI=" + eapi + "\nsrc_install() { dodir /var/lib/empty; keepdir /var/lib/kept; }\n"
			if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			request := Request{Protocol: Version, ID: "empty-image-" + eapi, Command: "run_phase", Phase: "src_install", EAPI: eapi, Ebuild: ebuild, WorkDir: directory, SourceDir: directory, ImageDir: image, Package: PackageIdentity{Category: "app-misc", PN: "pkg", PV: "1", PR: "r0", P: "pkg-1", PVR: "1", PF: "pkg-1", Slot: "0", Repository: "test"}}
			if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
				t.Fatalf("empty image cleanup: %v; events=%#v", err, events)
			}
			_, emptyErr := os.Stat(filepath.Join(image, "var/lib/empty"))
			if eapi == "7" && emptyErr != nil {
				t.Fatalf("EAPI 7 empty directory was removed: %v", emptyErr)
			}
			if eapi == "8" && !os.IsNotExist(emptyErr) {
				t.Fatalf("EAPI 8 empty directory remains: %v", emptyErr)
			}
			if _, err := os.Stat(filepath.Join(image, "var/lib/kept/.keep_app-misc_pkg_0")); err != nil {
				t.Fatalf("keepdir marker removed: %v", err)
			}
		})
	}
}

func TestBashWorkerTranslationAndCompressionHelpers(t *testing.T) {
	directory := t.TempDir()
	source, image := filepath.Join(directory, "source"), filepath.Join(directory, "image")
	if err := os.MkdirAll(filepath.Join(source, "html"), 0o755); err != nil {
		t.Fatal(err)
	}
	large := bytes.Repeat([]byte("documentation line\n"), 32)
	for name, content := range map[string][]byte{"guide.txt": large, "probe.1": large, "manual.info": large, "html/index.html": large, "fr.mo": []byte("translation")} {
		path := filepath.Join(source, name)
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := `EAPI=8
src_install() {
  dodoc guide.txt
  docinto html
  newdoc html/index.html index.html
  doman probe.1
  doinfo manual.info
  domo fr.mo
  docompress -x /usr/share/info
}`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "translation-compression", Command: "run_phase", Phase: "src_install", EAPI: "8", Ebuild: ebuild, WorkDir: source, SourceDir: source, ImageDir: image, Package: PackageIdentity{Category: "app-misc", PN: "pkg", PV: "1", PR: "r0", P: "pkg-1", PVR: "1", PF: "pkg-1", Slot: "0", Repository: "test"}}
	if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
		t.Fatalf("translation/compression helpers: %v; events=%#v", err, events)
	}
	for _, relative := range []string{"usr/share/doc/pkg-1/guide.txt.gz", "usr/share/man/man1/probe.1.gz"} {
		file, err := os.Open(filepath.Join(image, relative))
		if err != nil {
			t.Fatal(err)
		}
		reader, err := gzip.NewReader(file)
		if err != nil {
			file.Close()
			t.Fatal(err)
		}
		decompressed, err := io.ReadAll(reader)
		reader.Close()
		file.Close()
		if err != nil || !bytes.Equal(decompressed, large) {
			t.Fatalf("compressed %s does not round trip: %v", relative, err)
		}
	}
	for _, relative := range []string{"usr/share/doc/pkg-1/html/index.html", "usr/share/info/manual.info"} {
		if raw, err := os.ReadFile(filepath.Join(image, relative)); err != nil || !bytes.Equal(raw, large) {
			t.Fatalf("excluded compression path %s = %d bytes, %v", relative, len(raw), err)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(image, "usr/share/locale/fr/LC_MESSAGES/pkg.mo")); err != nil || string(raw) != "translation" {
		t.Fatalf("domo output = %q, %v", raw, err)
	}
}

func TestBashWorkerRemovesGeneratedInfoDirectoryIndexes(t *testing.T) {
	directory := t.TempDir()
	source, image := filepath.Join(directory, "source"), filepath.Join(directory, "image")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := `EAPI=8
src_install() {
  dodir /usr/share/info
  for name in dir dir.gz dir.bz2 dir.xz dir.zst dir.info dir.info.gz; do
    printf generated > "${ED}/usr/share/info/${name}"
  done
  printf manual > "${ED}/usr/share/info/manual.info"
}`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "info-index-cleanup", Command: "run_phase", Phase: "src_install", EAPI: "8", Ebuild: ebuild, WorkDir: source, SourceDir: source, ImageDir: image, Package: PackageIdentity{Category: "app-misc", PN: "pkg", PV: "1", PR: "r0", P: "pkg-1", PVR: "1", PF: "pkg-1", Slot: "0", Repository: "test"}}
	if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
		t.Fatalf("Info index cleanup: %v; events=%#v", err, events)
	}
	for _, name := range []string{"dir", "dir.gz", "dir.bz2", "dir.xz", "dir.zst", "dir.info", "dir.info.gz"} {
		if _, err := os.Lstat(filepath.Join(image, "usr/share/info", name)); !os.IsNotExist(err) {
			t.Fatalf("generated Info index %s remains: %v", name, err)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(image, "usr/share/info/manual.info")); err != nil || string(raw) != "manual" {
		t.Fatalf("real Info manual was removed: %q, %v", raw, err)
	}
}

func TestBashWorkerRunsInstallQAChecksBeforeFinalization(t *testing.T) {
	directory := t.TempDir()
	source, image := filepath.Join(directory, "source"), filepath.Join(directory, "image")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_install() { dodir /usr/share/probe; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	check := filepath.Join(directory, "60probe")
	if err := os.WriteFile(check, []byte("[[ -d ${ED%/}/usr/share/probe ]] || die missing-image\neqatag -v probe.present /usr/share/probe\n:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "install-qa", Command: "run_phase", Phase: "src_install", EAPI: "8", Ebuild: ebuild, WorkDir: source, SourceDir: source, ImageDir: image, InstallQAChecks: []string{check}, Package: PackageIdentity{Category: "app-misc", PN: "pkg", PV: "1", PR: "r0", P: "pkg-1", PVR: "1", PF: "pkg-1", Slot: "0", Repository: "test"}}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("install QA check: %v; events=%#v", err, events)
	}
	found := false
	for _, event := range events {
		if event.Kind == "elog" && event.Class == "QA" && strings.Contains(event.Message, "probe.present") {
			found = true
		}
	}
	if !found {
		t.Fatalf("QA event missing: %#v", events)
	}
}

func TestBashWorkerStripQueueHonorsExclusions(t *testing.T) {
	if _, err := exec.LookPath("strip"); err != nil {
		t.Skip("strip is unavailable")
	}
	if _, err := exec.LookPath("readelf"); err != nil {
		t.Skip("readelf is unavailable")
	}
	directory := t.TempDir()
	source, image := filepath.Join(directory, "source"), filepath.Join(directory, "image")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "probe"), original, 0o755); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := `EAPI=8
src_install() {
  newexe probe stripped
  newexe probe unstripped
  dostrip -x /usr/bin/unstripped
}`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "strip-queue", Command: "run_phase", Phase: "src_install", EAPI: "8", Ebuild: ebuild, WorkDir: source, SourceDir: source, ImageDir: image, Policy: ExecutionPolicy{Configured: true, Strip: true}}
	if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
		t.Fatalf("strip helpers: %v; events=%#v", err, events)
	}
	stripped, err := os.ReadFile(filepath.Join(image, "usr/bin/stripped"))
	if err != nil {
		t.Fatal(err)
	}
	unstripped, err := os.ReadFile(filepath.Join(image, "usr/bin/unstripped"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(unstripped, original) {
		t.Fatal("dostrip exclusion did not preserve the original executable")
	}
	if bytes.Equal(stripped, unstripped) {
		t.Fatal("dostrip did not transform the queued executable")
	}
}

func TestBashWorkerHonorsSourcedRestrictStrip(t *testing.T) {
	if _, err := exec.LookPath("strip"); err != nil {
		t.Skip("strip is unavailable")
	}
	directory := t.TempDir()
	source, image := filepath.Join(directory, "source"), filepath.Join(directory, "image")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "probe"), original, 0o755); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := "EAPI=8\nRESTRICT=\"binchecks strip\"\nsrc_install() { newexe probe probe; }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "runtime-restrict-strip", Command: "run_phase", Phase: "src_install", EAPI: "8", Ebuild: ebuild, WorkDir: source, SourceDir: source, ImageDir: image, Policy: ExecutionPolicy{Configured: true, Strip: true}}
	if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
		t.Fatalf("runtime RESTRICT=strip: %v; events=%#v", err, events)
	}
	installed, err := os.ReadFile(filepath.Join(image, "usr/bin/probe"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(installed, original) {
		t.Fatal("effective sourced RESTRICT=strip did not preserve executable")
	}
}

func TestBashWorkerConfiguredZstdCompression(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd is unavailable")
	}
	directory := t.TempDir()
	source, image := filepath.Join(directory, "source"), filepath.Join(directory, "image")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	original := bytes.Repeat([]byte("zstd is great\n"), 64)
	if err := os.WriteFile(filepath.Join(source, "README"), original, 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_install() { dodoc README; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "zstd-compression", Command: "run_phase", Phase: "src_install", EAPI: "8", Ebuild: ebuild, WorkDir: source, SourceDir: source, ImageDir: image, Package: PackageIdentity{Category: "app-misc", PN: "pkg", PV: "1", PR: "r0", P: "pkg-1", PVR: "1", PF: "pkg-1", Slot: "0", Repository: "test"}, Env: map[string]string{"PORTAGE_COMPRESS": "zstd"}}
	if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
		t.Fatalf("zstd compression: %v; events=%#v", err, events)
	}
	compressed := filepath.Join(image, "usr/share/doc/pkg-1/README.zst")
	command := exec.Command("zstd", "-q", "-d", "-c", compressed)
	decompressed, err := command.Output()
	if err != nil || !bytes.Equal(decompressed, original) {
		t.Fatalf("zstd round trip = %d bytes, error=%v", len(decompressed), err)
	}
}

func TestBashWorkerDodocRecursiveOption(t *testing.T) {
	directory := t.TempDir()
	source, image := filepath.Join(directory, "source"), filepath.Join(directory, "image")
	if err := os.MkdirAll(filepath.Join(source, "doc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "doc", "guide.txt"), []byte("guide\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_install() { dodoc -r doc README.md; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "dodoc-recursive", Command: "run_phase", Phase: "src_install", EAPI: "8", Ebuild: ebuild, WorkDir: source, SourceDir: source, ImageDir: image, Package: PackageIdentity{Category: "app-misc", PN: "pkg", PV: "1", PR: "r0", P: "pkg-1", PVR: "1", PF: "pkg-1", Slot: "0", Repository: "test"}}
	if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
		t.Fatalf("dodoc -r: %v; events=%#v", err, events)
	}
	for _, path := range []string{filepath.Join(image, "usr/share/doc/pkg-1/doc/guide.txt"), filepath.Join(image, "usr/share/doc/pkg-1/README.md")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("recursive documentation %s: %v", path, err)
		}
	}
}

func TestBashWorkerUnpackUsesWorkDirAndLifecycleDefaultsAreNoOps(t *testing.T) {
	directory := t.TempDir()
	work := filepath.Join(directory, "work")
	source := filepath.Join(work, "pkg-1")
	image := filepath.Join(directory, "image")
	for _, path := range []string{work, image} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_unpack() { [[ $PWD == $WORKDIR ]] || return 41; mkdir -p \"$S\"; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := Request{Protocol: Version, EAPI: "8", Ebuild: ebuild, WorkDir: work, SourceDir: source, ImageDir: image}
	for _, phase := range []string{"src_unpack", "pkg_setup", "pkg_preinst", "pkg_postinst", "pkg_prerm", "pkg_postrm"} {
		request := base
		request.ID, request.Command, request.Phase = "directory-"+phase, "run_phase", phase
		if events, err := RunBashWorker(context.Background(), request); err != nil {
			t.Fatalf("%s: %v; events=%#v", phase, err, events)
		}
	}
}

func TestBashWorkerTarUnpackDoesNotPreserveArchiveOwner(t *testing.T) {
	if !strings.Contains(bashWorker, `tar --no-same-owner -xJf "$source"`) {
		t.Fatal("tar.xz unpack does not disable archive owner restoration")
	}
	if !strings.Contains(bashWorker, `tar --no-same-owner --zstd -xf -`) {
		t.Fatal("Debian zstd payload unpack does not disable archive owner restoration")
	}
}

func TestBashWorkerUnpackSkipsUnsupportedDistfileSuffix(t *testing.T) {
	directory := t.TempDir()
	work := filepath.Join(directory, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "fix.patch"), []byte("not an archive\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_unpack() { unpack ./fix.patch; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "unpack-skip-patch", Command: "run_phase", Phase: "src_unpack", EAPI: "8", Ebuild: ebuild, WorkDir: work, SourceDir: filepath.Join(work, "pkg-1")}
	if events, err := RunBashWorker(context.Background(), request); err != nil {
		t.Fatalf("unsupported suffix was not skipped: %v; events=%#v", err, events)
	}
}

func TestBashWorkerAcceptsDisabledUseGuardAtPhaseTail(t *testing.T) {
	directory := t.TempDir()
	work := filepath.Join(directory, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	source := `EAPI=8
src_unpack() {
	if false; then
		return 42
	else
		:
		use verify-sig && die "disabled guard executed"
	fi
}
`
	if err := os.WriteFile(ebuild, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "disabled-use-tail", Command: "run_phase", Phase: "src_unpack", EAPI: "8", Ebuild: ebuild, WorkDir: work, SourceDir: filepath.Join(work, "pkg-1")}
	if events, err := RunBashWorker(context.Background(), request); err != nil {
		t.Fatalf("disabled terminal USE guard failed phase: %v; events=%#v", err, events)
	}
}

func TestBashWorkerMatchesPortageByIgnoringOrdinaryPhaseReturn(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	source := "EAPI=8\npython_configure_all() { return 1; }\nsrc_configure() { local ret=0; python_configure_all || ret=$?; return $ret; }\n"
	if err := os.WriteFile(ebuild, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "portage-phase-return", Command: "run_phase", Phase: "src_configure", EAPI: "8", Ebuild: ebuild, WorkDir: directory, SourceDir: directory}
	if events, err := RunBashWorker(context.Background(), request); err != nil {
		t.Fatalf("ordinary propagated phase return was treated as fatal: %v; events=%#v", err, events)
	}
}

func TestBashWorkerRealRootPrefixContract(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	source := `EAPI=8
pkg_setup() {
	[[ -z ${EPREFIX} ]] || die "EPREFIX=${EPREFIX}"
	[[ ${ROOT} == / ]] || die "ROOT=${ROOT}"
	[[ ${EROOT} == / ]] || die "EROOT=${EROOT}"
	[[ -z ${BROOT} ]] || die "BROOT=${BROOT}"
}
`
	if err := os.WriteFile(ebuild, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "real-root-prefix", Command: "run_phase", Phase: "pkg_setup", EAPI: "8", Ebuild: ebuild, WorkDir: directory, RootDir: "/", SysrootDir: "/"}
	if events, err := RunBashWorker(context.Background(), request); err != nil {
		t.Fatalf("real-root prefix contract failed: %v; events=%#v", err, events)
	}
}

func TestBashWorkerEapplyDirectoryUsesSortedPatchAndDiffFiles(t *testing.T) {
	directory := t.TempDir()
	work, source, image := filepath.Join(directory, "work"), filepath.Join(directory, "source"), filepath.Join(directory, "image")
	patches := filepath.Join(work, "patches")
	for _, path := range []string{work, source, image, patches} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "value"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := "--- a/value\n+++ b/value\n@@ -1 +1 @@\n-one\n+two\n"
	second := "--- a/value\n+++ b/value\n@@ -1 +1 @@\n-two\n+three\n"
	if err := os.WriteFile(filepath.Join(patches, "01.patch"), []byte(first), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(patches, "02.diff"), []byte(second), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(patches, "README"), []byte("ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_prepare() { eapply \"$WORKDIR/patches\"; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "eapply-directory", Command: "run_phase", Phase: "src_prepare", EAPI: "8", Ebuild: ebuild, WorkDir: work, SourceDir: source, ImageDir: image}
	if events, err := RunBashWorker(context.Background(), request); err != nil {
		t.Fatalf("eapply directory: %v; events=%#v", err, events)
	}
	content, err := os.ReadFile(filepath.Join(source, "value"))
	if err != nil || string(content) != "three\n" {
		t.Fatalf("patched content=%q err=%v", content, err)
	}
}

func TestBashWorkerEscapesJSONControlCharacters(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_prepare() { printf 'tab\\tcarriage\\rcontrol\\001slash\\\\quote\\\"\\n'; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "json-controls", Command: "run_phase", Phase: "src_prepare", EAPI: "8", Ebuild: ebuild, WorkDir: directory, SourceDir: directory, ImageDir: filepath.Join(directory, "image")}
	events, err := RunBashWorker(context.Background(), request)
	if err != nil {
		t.Fatalf("control-character output: %v; events=%#v", err, events)
	}
	want := "tab\tcarriage\rcontrol\x01slash\\quote\""
	for _, event := range events {
		if event.Kind == "log" {
			if event.Message != want {
				t.Fatalf("message=%q want=%q", event.Message, want)
			}
			return
		}
	}
	t.Fatal("missing log event")
}

func TestBashWorkerEconfSuppliesGentooInstallDirectories(t *testing.T) {
	directory := t.TempDir()
	configure := filepath.Join(directory, "configure")
	if err := os.WriteFile(configure, []byte(`#!/bin/sh
if [ "$1" = --help ]; then
	cat <<EOF
  --disable-dependency-tracking
  --disable-silent-rules
  --enable-shared
  --enable-static
  --datarootdir=DIR
  --docdir=DIR
  --htmldir=DIR
  --with-sysroot=DIR
EOF
	exit 0
fi
printf '%s\n' "$@" > "$T/configure.args"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_configure() { econf --enable-example; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(directory, "temp")
	if err := os.MkdirAll(temp, 0o755); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "econf-layout", Command: "run_phase", Phase: "src_configure", EAPI: "8", Ebuild: ebuild, WorkDir: directory, SourceDir: directory, ImageDir: filepath.Join(directory, "image"), TempDir: temp, Env: map[string]string{"DEFAULT_ABI": "amd64", "CHOST": "x86_64-pc-linux-gnu"}}
	if events, err := RunBashWorker(context.Background(), request); err != nil {
		t.Fatalf("econf: %v; events=%#v", err, events)
	}
	data, err := os.ReadFile(filepath.Join(temp, "configure.args"))
	if err != nil {
		t.Fatal(err)
	}
	arguments := string(data)
	for _, want := range []string{"--prefix=/usr\n", "--libdir=/usr/lib64\n", "--sysconfdir=/etc\n", "--localstatedir=/var/lib\n", "--disable-dependency-tracking\n", "--disable-silent-rules\n", "--disable-static\n", "--datarootdir=/usr/share\n", "--docdir=/usr/share/doc/package\n", "--htmldir=/usr/share/doc/package/html\n", "--with-sysroot=/\n", "--build=x86_64-pc-linux-gnu\n", "--host=x86_64-pc-linux-gnu\n", "--enable-example\n"} {
		if !strings.Contains(arguments, want) {
			t.Fatalf("configure args missing %q:\n%s", want, arguments)
		}
	}
}

func TestBashWorkerEconfDerivesLibdirFromEbuildPrefix(t *testing.T) {
	directory := t.TempDir()
	configure := filepath.Join(directory, "configure")
	if err := os.WriteFile(configure, []byte(`#!/bin/sh
if [ "$1" = --help ]; then
	exit 0
fi
printf '%s\n' "$@" > "$T/configure.args"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "postgresql-18.4.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_configure() { econf --prefix=/usr/lib64/postgresql-18; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(directory, "temp")
	if err := os.MkdirAll(temp, 0o755); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Protocol: Version, ID: "econf-custom-prefix", Command: "run_phase", Phase: "src_configure",
		EAPI: "8", Ebuild: ebuild, WorkDir: directory, SourceDir: directory,
		ImageDir: filepath.Join(directory, "image"), TempDir: temp,
		Env: map[string]string{"DEFAULT_ABI": "amd64", "CHOST": "x86_64-pc-linux-gnu"},
	}
	if events, err := RunBashWorker(context.Background(), request); err != nil {
		t.Fatalf("econf custom prefix: %v; events=%#v", err, events)
	}
	data, err := os.ReadFile(filepath.Join(temp, "configure.args"))
	if err != nil {
		t.Fatal(err)
	}
	arguments := string(data)
	if !strings.Contains(arguments, "--libdir=/usr/lib64/postgresql-18/lib64\n") {
		t.Fatalf("custom-prefix libdir was not derived:\n%s", arguments)
	}
}

func TestBashWorkerEconfDisablesDependencyTrackingBeforeEbuildWorkaround(t *testing.T) {
	directory := t.TempDir()
	configure := filepath.Join(directory, "configure")
	if err := os.WriteFile(configure, []byte(`#!/bin/sh
if [ "$1" = --help ]; then
	echo '  --disable-dependency-tracking'
	exit 0
fi
for arg; do
	[ "$arg" = --disable-dependency-tracking ] && exit 0
done
mkdir qt zbarcam
`), 0o755); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_configure() { econf; mkdir qt zbarcam || die; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Protocol: Version, ID: "econf-dependency-tracking", Command: "run_phase",
		Phase: "src_configure", EAPI: "8", Ebuild: ebuild, WorkDir: directory,
		SourceDir: directory, ImageDir: filepath.Join(directory, "image"),
		Env: map[string]string{"DEFAULT_ABI": "amd64", "CHOST": "x86_64-pc-linux-gnu"},
	}
	if events, err := RunBashWorker(context.Background(), request); err != nil {
		t.Fatalf("econf dependency-tracking parity: %v; events=%#v", err, events)
	}
}

func TestBashWorkerPhaseBatchPreservesEclassStateAndDieIsTerminal(t *testing.T) {
	directory := t.TempDir()
	work, source, image := filepath.Join(directory, "work"), filepath.Join(directory, "source"), filepath.Join(directory, "image")
	for _, path := range []string{work, source, image} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte(`EAPI=8
src_prepare() { PREPARED=yes; }
src_configure() { [[ ${PREPARED-} == yes ]] || die "prepare state lost"; }
src_compile() { printf compiled > "${T}/compiled"; }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "phase-batch", Command: "run_phases", Phases: []string{"src_prepare", "src_configure", "src_compile"}, EAPI: "8", Ebuild: ebuild, WorkDir: work, SourceDir: source, ImageDir: image, TempDir: directory}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("stateful phase batch: %v; events=%#v", err, events)
	}
	if _, err := os.Stat(filepath.Join(directory, "compiled")); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_prepare() { die fatal; printf leaked > \"${T}/leaked\"; }\nsrc_configure() { printf later > \"${T}/later\"; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request.ID = "phase-batch-die"
	if _, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err == nil {
		t.Fatal("die returned successful batch")
	}
	for _, name := range []string{"leaked", "later"} {
		if _, err := os.Stat(filepath.Join(directory, name)); !os.IsNotExist(err) {
			t.Fatalf("%s executed after die: %v", name, err)
		}
	}
}

func TestBashWorkerPreservesSafeEbuildSourceDirectory(t *testing.T) {
	directory := t.TempDir()
	work := filepath.Join(directory, "work")
	if err := os.MkdirAll(filepath.Join(work, "Upstream-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := "EAPI=8\nS=\"${WORKDIR}/Upstream-1\"\nsrc_prepare() { [[ $PWD == \"$S\" ]] || return 42; }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "custom-s", Command: "run_phase", Phase: "src_prepare", EAPI: "8", Ebuild: ebuild, WorkDir: work, SourceDir: filepath.Join(work, "pkg-1")}
	if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
		t.Fatalf("custom S rejected: %v; events=%#v", err, events)
	}
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nS=/outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request.ID = "escaping-s"
	if _, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err == nil {
		t.Fatal("S outside WORKDIR was accepted")
	}
}

func TestBashWorkerCreatesSourceDirectoryAfterSourceLessUnpack(t *testing.T) {
	directory := t.TempDir()
	work := filepath.Join(directory, "work")
	source := filepath.Join(work, "source-less-1")
	ebuild := filepath.Join(directory, "source-less-1.ebuild")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ebuild, []byte("EAPI=8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Protocol: Version, ID: "source-less-unpack", Command: "run_phase", Phase: "src_unpack",
		EAPI: "8", Ebuild: ebuild, WorkDir: work, SourceDir: source,
	}
	if _, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
		t.Fatalf("source-less src_unpack: %v", err)
	}
	if info, err := os.Stat(source); err != nil {
		t.Fatalf("source directory was not created after src_unpack: %v", err)
	} else if !info.IsDir() {
		t.Fatalf("source path is not a directory: %s", source)
	}
}

func TestBashWorkerHasUsesExactArgumentMembership(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := `EAPI=8
src_compile() {
  has 8 7 8 || return 31
  ! has 8 7 80 || return 32
  ! has 8 || return 33
}`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "has-contract", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild, WorkDir: directory, SourceDir: directory}
	if events, err := RunBashWorker(context.Background(), request); err != nil {
		t.Fatalf("has contract: %v; events=%#v", err, events)
	}
}

func TestBashWorkerBestVersionUsesPreflightedDomainSnapshot(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := `EAPI=8
src_compile() {
  [[ $(best_version -b dev-python/gpep517) == dev-python/gpep517-19 ]] || return 31
  [[ $(best_version dev-python/gpep517) == dev-python/gpep517-18 ]] || return 32
  [[ -z $(best_version -b dev-python/missing) ]] || return 33
}`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "best-version-contract", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild, WorkDir: directory, SourceDir: directory,
		BestVersion: map[string]string{"b\tdev-python/gpep517": "dev-python/gpep517-19", "r\tdev-python/gpep517": "dev-python/gpep517-18"}}
	if events, err := RunBashWorker(context.Background(), request); err != nil {
		t.Fatalf("best_version contract: %v; events=%#v", err, events)
	}
}

func TestBashWorkerFallsBackToConstrainedRuntimeVersionHelper(t *testing.T) {
	tmp := t.TempDir()
	ebuild := filepath.Join(tmp, "dynamic-1.ebuild")
	source := `EAPI=8
pkg_setup() {
	local query=dev-libs/dynamic
	has_version "$query" || die "dynamic has_version failed"
	[[ $(best_version -b "$query") == dev-libs/dynamic-2 ]] || die "dynamic best_version failed"
}`
	if err := os.WriteFile(ebuild, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(tmp, "query-helper")
	script := `#!/bin/sh
test "$1" = __phase-query || exit 2
case "$2" in
  has-version) printf '1\n' ;;
  best-version) printf 'dev-libs/dynamic-2\n' ;;
  *) exit 2 ;;
esac
`
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Protocol: Version, ID: "dynamic-query", Command: "run_phase", Phase: "pkg_setup",
		EAPI: "8", Ebuild: ebuild, Env: map[string]string{},
		QueryHelper: helper, QueryRootVDB: tmp, QueryBrootVDB: tmp,
	}
	events, err := RunBashWorker(context.Background(), request)
	if err != nil {
		t.Fatalf("runtime query fallback: %v; events=%#v", err, events)
	}
}

func TestBashWorkerRejectsInvalidRuntimeVersionHelperAnswer(t *testing.T) {
	tmp := t.TempDir()
	ebuild := filepath.Join(tmp, "dynamic-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\npkg_setup() { has_version dev-libs/dynamic || die \"query failed\"; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(tmp, "query-helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf 'maybe\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Protocol: Version, ID: "invalid-query", Command: "run_phase", Phase: "pkg_setup",
		EAPI: "8", Ebuild: ebuild, Env: map[string]string{},
		QueryHelper: helper, QueryRootVDB: tmp, QueryBrootVDB: tmp,
	}
	events, err := RunBashWorker(context.Background(), request)
	if err == nil {
		t.Fatalf("invalid helper answer succeeded: %#v", events)
	}
}

func TestBashWorkerUseHelperPrimitives(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := `EAPI=8
IUSE="+ssl -test examples"
src_compile() {
  in_iuse ssl || return 31
  in_iuse test || return 32
  ! in_iuse missing || return 33
  [[ $(usev ssl) == ssl ]] || return 34
  [[ $(usev ssl custom) == custom ]] || return 35
  ! usev test >/dev/null || return 36
  [[ $(use_with ssl crypto openssl) == --with-crypto=openssl ]] || return 37
  [[ $(use_with test tests) == --without-tests ]] || return 38
  [[ $(use_enable examples demos) == --enable-demos ]] || return 39
  [[ $(use_enable test tests) == --disable-tests ]] || return 40
  use '!test' || return 41
  ! use '!ssl' || return 42
}`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "use-primitives", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild, WorkDir: directory, SourceDir: directory, Env: map[string]string{"USE": "ssl examples"}}
	if events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
		t.Fatalf("USE helper primitives: %v; events=%#v", err, events)
	}
}

func TestBashWorkerFind0ConsumesNullTerminatedRoots(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "config.log"), []byte("log"), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := `EAPI=8
src_compile() {
  found=$(printf '%s\0' "${WORKDIR}" | find0 -type f -name config.log -print)
  [[ ${found} == "${WORKDIR}/config.log" ]] || return 31
}`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "find0-contract", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild, WorkDir: directory, SourceDir: directory}
	if events, err := RunBashWorker(context.Background(), request); err != nil {
		t.Fatalf("find0 contract: %v; events=%#v", err, events)
	}
}

func TestBashWorkerNonfatalContainsCommandFailureButNotDie(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := `EAPI=8
expected_failure() { printf expected; return 23; }
src_compile() {
  nonfatal expected_failure
  [[ $? == 23 ]] || return 31
  [[ ${PORTAGE_NONFATAL-unset} == unset ]] || return 33
  printf 'survived\n'
}`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "nonfatal-primitives", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild, WorkDir: directory, SourceDir: directory}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("nonfatal primitives: %v; events=%#v", err, events)
	}
	var output strings.Builder
	for _, event := range events {
		if event.Kind == "log" {
			output.WriteString(event.Message)
		}
	}
	if got := output.String(); !strings.Contains(got, "expected") || !strings.Contains(got, "survived") {
		t.Fatalf("nonfatal output = %q", got)
	}
	content = `EAPI=8
src_compile() { nonfatal die terminal; printf leaked > "${T}/leaked"; }`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request.ID = "nonfatal-die"
	request.TempDir = directory
	if _, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err == nil {
		t.Fatal("nonfatal contained die")
	}
	if _, err := os.Stat(filepath.Join(directory, "leaked")); !os.IsNotExist(err) {
		t.Fatalf("execution continued after nonfatal die: %v", err)
	}
}

func TestRequestValidation(t *testing.T) {
	request := Request{Protocol: Version, ID: "pkg-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: "/repo/cat/pkg/pkg-1.ebuild"}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	request.Protocol++
	if err := request.Validate(); err == nil {
		t.Fatal("future protocol version accepted")
	}
}

func TestRequestRejectsUnsupportedEAPIAndRelativeEclassDirectory(t *testing.T) {
	request := Request{Protocol: Version, ID: "pkg-1", Command: "run_phase", Phase: "src_compile", EAPI: "10", Ebuild: "/repo/pkg.ebuild"}
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported EAPI") {
		t.Fatalf("EAPI error = %v", err)
	}
	request.EAPI = "8"
	request.EclassDirs = []string{"relative/eclass"}
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("eclass directory error = %v", err)
	}
}

func TestRequestRejectsRelativeExecutionDirectories(t *testing.T) {
	base := Request{Protocol: Version, ID: "pkg-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: "/repo/pkg.ebuild"}
	for name, mutate := range map[string]func(*Request){
		"root":      func(r *Request) { r.RootDir = "root" },
		"sysroot":   func(r *Request) { r.SysrootDir = "sysroot" },
		"broot":     func(r *Request) { r.BrootDir = "broot" },
		"temporary": func(r *Request) { r.TempDir = "tmp" },
		"home":      func(r *Request) { r.HomeDir = "home" },
	} {
		t.Run(name, func(t *testing.T) {
			request := base
			mutate(&request)
			if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "must be absolute") {
				t.Fatalf("relative %s error = %v", name, err)
			}
		})
	}
}

func TestDefaultPhasesDeclareSupportedEAPIs(t *testing.T) {
	for _, eapi := range []string{"7", "8", "9"} {
		phases, err := DefaultPhases(eapi)
		if err != nil || len(phases) != 11 || phases[2] != "src_prepare" {
			t.Fatalf("EAPI %s defaults = %#v, %v", eapi, phases, err)
		}
	}
	if _, err := DefaultPhases("6"); err == nil {
		t.Fatal("unsupported EAPI defaults accepted")
	}
}

func TestEAPI9EnvironmentAndHelpers(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := `EAPI=9
src_compile() {
	[[ -n ${ROOT} ]] || die "ROOT shell variable missing"
	! env | grep -q '^ROOT=' || die "ROOT leaked to external environment"
	false | true
	! pipestatus || die "pipestatus missed pipeline failure"
	true | true
	pipestatus || die "pipestatus rejected successful pipeline"
	! domo missing.mo || die "EAPI 9 domo was not banned"
}
`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "eapi9-helpers", Command: "run_phase", Phase: "src_compile", EAPI: "9", Ebuild: ebuild, RootDir: directory}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("EAPI 9 worker: %v; events=%#v", err, events)
	}
}

func TestEAPI8AssertChecksCompletePipeline(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := `EAPI=8
src_unpack() {
	printf source | grep -q source
	assert -n "successful pipeline rejected"
}
`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "eapi8-assert-success", Command: "run_phase", Phase: "src_unpack", EAPI: "8", Ebuild: ebuild, RootDir: directory}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("EAPI 8 assert rejected successful pipeline: %v; events=%#v", err, events)
	}
}

func TestEAPI8AssertRejectsAnyPipelineFailure(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := `EAPI=8
src_unpack() {
	false | true
	assert -n "pipeline failure"
}
`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "eapi8-assert-failure", Command: "run_phase", Phase: "src_unpack", EAPI: "8", Ebuild: ebuild, RootDir: directory}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err == nil {
		t.Fatalf("EAPI 8 assert accepted failed pipeline; events=%#v", events)
	}
	found := false
	for _, event := range events {
		if strings.Contains(event.Message, "pipeline failure") {
			found = true
		}
	}
	if !found {
		t.Fatalf("assert failure message missing: events=%#v", events)
	}
}

func TestInstalledEnvironmentSuppliesLifecycleAndCannotReplaceTypedRoot(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	environment := filepath.Join(directory, "environment")
	root := filepath.Join(directory, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ebuild, []byte("EAPI=8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content := `EAPI=8
ROOT=/stale/installed/root
pkg_postrm() { [[ ${ROOT} == "${EXPECTED_ROOT}" ]] || die "stale ROOT escaped"; echo installed-environment; }
`
	if err := os.WriteFile(environment, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "installed-environment", Command: "run_phase", Phase: "pkg_postrm", EAPI: "8", Ebuild: ebuild, Environment: environment, RootDir: root, Env: map[string]string{"EXPECTED_ROOT": root}}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("installed environment worker: %v; events=%#v", err, events)
	}
	found := false
	for _, event := range events {
		found = found || event.Kind == "log" && event.Message == "installed-environment"
	}
	if !found {
		t.Fatalf("installed lifecycle did not run: %#v", events)
	}
}

func TestSupportedEAPIDefaultAndLifecycleMatrix(t *testing.T) {
	for _, eapi := range []string{"7", "8", "9"} {
		t.Run("EAPI-"+eapi, func(t *testing.T) {
			directory := t.TempDir()
			work := filepath.Join(directory, "work")
			source := filepath.Join(work, "pkg-1")
			image := filepath.Join(directory, "image")
			for _, path := range []string{work, source, image} {
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			ebuild := filepath.Join(directory, "pkg-1.ebuild")
			if err := os.WriteFile(ebuild, []byte("EAPI="+eapi+"\nA=\nPF=pkg-1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			phases, err := DefaultPhases(eapi)
			if err != nil {
				t.Fatal(err)
			}
			for _, phase := range phases {
				request := Request{
					Protocol:  Version,
					ID:        "matrix-" + eapi + "-" + phase,
					Command:   "run_phase",
					Phase:     phase,
					EAPI:      eapi,
					Ebuild:    ebuild,
					WorkDir:   work,
					SourceDir: source,
					ImageDir:  image,
				}
				events, runErr := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
				if runErr != nil {
					t.Fatalf("%s: %v; events=%#v", phase, runErr, events)
				}
				if len(events) != 2 || events[0].Kind != "phase" || events[0].Message != phase || events[1].Kind != "result" || events[1].ExitCode == nil || *events[1].ExitCode != 0 {
					t.Fatalf("%s events = %#v", phase, events)
				}
			}
		})
	}
}

func TestDecoderRequiresOrderedTerminalResult(t *testing.T) {
	stream := strings.NewReader("" +
		`{"protocol":1,"id":"pkg-1","sequence":0,"kind":"phase","message":"src_compile"}` + "\n" +
		`{"protocol":1,"id":"pkg-1","sequence":1,"kind":"log","stream":"stdout","message":"building"}` + "\n" +
		`{"protocol":1,"id":"pkg-1","sequence":2,"kind":"result","exit_code":0}` + "\n")
	decoder := NewDecoder(stream, "pkg-1")
	for range 3 {
		if _, err := decoder.Next(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := decoder.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("post-result decode = %v, want EOF", err)
	}
}

func TestDecoderRejectsCrossTalkAndSequenceGaps(t *testing.T) {
	for _, input := range []string{
		`{"protocol":1,"id":"other","sequence":0,"kind":"log"}`,
		`{"protocol":1,"id":"pkg-1","sequence":2,"kind":"log"}`,
		`{"protocol":1,"id":"pkg-1","sequence":0,"kind":"result"}`,
		`{"protocol":1,"id":"pkg-1","sequence":0,"kind":"unknown"}`,
	} {
		if _, err := NewDecoder(strings.NewReader(input), "pkg-1").Next(); err == nil {
			t.Fatalf("invalid event accepted: %s", input)
		}
	}
}

func TestWorkerProtocolRequiresExactlyOneTerminalResult(t *testing.T) {
	request := Request{Protocol: Version, ID: "terminal-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: "/tmp/pkg.ebuild"}
	event := func(sequence int, kind string, exit string) string {
		field := ""
		if exit != "" {
			field = `,"exit_code":` + exit
		}
		return fmt.Sprintf(`{"protocol":1,"id":"terminal-1","sequence":%d,"kind":"%s"%s}`, sequence, kind, field)
	}
	tests := []struct {
		name   string
		output string
	}{
		{name: "missing result", output: event(0, "phase", "")},
		{name: "duplicate result", output: event(0, "result", "0") + "\n" + event(1, "result", "0")},
		{name: "trailing garbage", output: event(0, "result", "0") + "\nnot-json"},
		{name: "status mismatch", output: event(0, "result", "7")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command := exec.CommandContext(context.Background(), "bash", "-c", "printf '%s' \"$PAYLOAD\"")
			request.Env = map[string]string{"PAYLOAD": tt.output}
			if events, err := runWorkerCommand(command, request); err == nil {
				t.Fatalf("invalid terminal stream accepted: %#v", events)
			}
		})
	}
}

func TestBashWorkerHandshakeAndOrderedLogs(t *testing.T) {
	ebuild := filepath.Join(t.TempDir(), "pkg-1.ebuild")
	content := "EAPI=8\nsrc_compile() { printf 'hello from phase\\n'; }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "cat-pkg-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild}
	command := exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker)
	events, err := runWorkerCommand(command, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Kind != "phase" || events[1].Kind != "log" || events[1].Message != "hello from phase" || events[2].Kind != "result" || events[2].ExitCode == nil || *events[2].ExitCode != 0 {
		t.Fatalf("worker events = %#v", events)
	}
}

func TestBashWorkerEmitsExpandedVDBMetadataOnRequest(t *testing.T) {
	ebuild := filepath.Join(t.TempDir(), "pkg-1.ebuild")
	eclassDir := filepath.Join(filepath.Dir(ebuild), "eclass")
	if err := os.Mkdir(eclassDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(eclassDir, "sample.eclass"), []byte("sample_src_prepare() { :; }\nEXPORT_FUNCTIONS src_prepare\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content := "EAPI=8\ninherit sample\nLLVM_MAJOR=19\nDEPEND=\"~llvm-core/llvm-${LLVM_MAJOR}:${LLVM_MAJOR}\"\nRDEPEND=\"${DEPEND}\"\nsrc_compile() { :; }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "metadata-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild, EclassDirs: []string{eclassDir}, EmitMetadata: true}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("%v; events=%#v", err, events)
	}
	metadata := make(map[string]string)
	for _, event := range events {
		if event.Kind == "metadata" {
			metadata[event.Class] = event.Message
		}
	}
	want := "~llvm-core/llvm-19:19"
	if metadata["DEPEND"] != want || metadata["RDEPEND"] != want {
		t.Fatalf("expanded metadata = %#v, want DEPEND/RDEPEND %q", metadata, want)
	}
	if metadata["INHERITED"] != "sample" || metadata["DEFINED_PHASES"] != "src_prepare src_compile" {
		t.Fatalf("derived metadata = %#v", metadata)
	}
}

func TestBashWorkerEmitsTypedElogClasses(t *testing.T) {
	ebuild := filepath.Join(t.TempDir(), "pkg-1.ebuild")
	content := "EAPI=8\nsrc_compile() { einfo info; elog log; ewarn warn; eerror error; eqawarn qa; }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "elog-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatal(err)
	}
	var classes []string
	for _, event := range events {
		if event.Kind == "elog" {
			classes = append(classes, event.Class+":"+event.Message)
		}
	}
	want := []string{"INFO:info", "LOG:log", "WARN:warn", "ERROR:error", "QA:qa"}
	if !reflect.DeepEqual(classes, want) {
		t.Fatalf("elog classes = %#v, want %#v; events=%#v", classes, want, events)
	}
}

func TestEveryDeclaredPhaseFailurePreservesDurableLog(t *testing.T) {
	for _, eapi := range []string{"7", "8"} {
		phases, err := DefaultPhases(eapi)
		if err != nil {
			t.Fatal(err)
		}
		for _, phaseName := range phases {
			t.Run("EAPI-"+eapi+"-"+phaseName, func(t *testing.T) {
				directory := t.TempDir()
				ebuild := filepath.Join(directory, "pkg-1.ebuild")
				content := fmt.Sprintf("EAPI=%s\n%s() { printf 'before failure in %s\\n'; die 'declared phase failure'; }\n", eapi, phaseName, phaseName)
				if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
				request := Request{Protocol: Version, ID: "failure-" + eapi + "-" + phaseName, Command: "run_phase", Phase: phaseName, EAPI: eapi, Ebuild: ebuild, WorkDir: directory, SourceDir: directory, ImageDir: filepath.Join(directory, "image"), TempDir: filepath.Join(directory, "temp")}
				for _, path := range []string{request.ImageDir, request.TempDir} {
					if err := os.MkdirAll(path, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				events, runErr := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
				if runErr == nil {
					t.Fatal("failing phase returned success")
				}
				manager, err := NewPackageLog(PackageLogOptions{Root: filepath.Join(directory, "logs"), TempDir: request.TempDir, Category: "cat", PF: "pkg-1"})
				if err != nil {
					t.Fatal(err)
				}
				_, persistErr := persistWorkerEvents(request, events, runErr, WorkerOptions{DurableLog: manager, FinalizeLog: true})
				if persistErr == nil || !strings.Contains(persistErr.Error(), manager.Path()) {
					t.Fatalf("persist error = %v", persistErr)
				}
				contentBytes, err := os.ReadFile(manager.Path())
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(contentBytes), "before failure in "+phaseName) || !strings.Contains(string(contentBytes), "exit_code=1") || !strings.Contains(string(contentBytes), "terminal-error") {
					t.Fatalf("durable failure log = %s", contentBytes)
				}
			})
		}
	}
}

func TestBashWorkerAcceptsNestedTrailingConditionalGuard(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := "EAPI=8\noptional_docs() { use doc && printf docs; }\nsrc_install() { optional_docs; }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "nested-guard", Command: "run_phase", Phase: "src_install", EAPI: "8", Ebuild: ebuild, WorkDir: directory, SourceDir: directory, ImageDir: filepath.Join(directory, "image"), TempDir: filepath.Join(directory, "temp")}
	for _, path := range []string{request.ImageDir, request.TempDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
		t.Fatalf("nested trailing guard failed: %v", err)
	}
}

func TestBashWorkerAcceptsNestedDisabledUseEarlyReturn(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := "EAPI=8\nrestore_config() { use savedconfig || return; printf restored; }\nsrc_prepare() { printf prepared; restore_config config; }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "nested-early-return", Command: "run_phase", Phase: "src_prepare", EAPI: "8", Ebuild: ebuild, WorkDir: directory, SourceDir: directory, ImageDir: filepath.Join(directory, "image"), TempDir: filepath.Join(directory, "temp")}
	for _, path := range []string{request.ImageDir, request.TempDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
		t.Fatalf("nested disabled-USE early return failed: %v", err)
	}
}

func TestBashWorkerCancellationKillsProcessGroup(t *testing.T) {
	directory := t.TempDir()
	pidFile := filepath.Join(directory, "child.pid")
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := "EAPI=8\nsrc_compile() { ( trap '' TERM; printf '%s\\n' \"$BASHPID\" > \"$PID_FILE\"; while :; do sleep 1; done ) \u0026 wait; }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	request := Request{Protocol: Version, ID: "cancel-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild, Env: map[string]string{"PID_FILE": pidFile}}
	started := time.Now()
	events, err := runWorkerCommandWithCancelGrace(exec.CommandContext(ctx, "bash", "--noprofile", "--norc", "-c", bashWorker), request, 100*time.Millisecond)
	if err == nil {
		t.Fatal("cancelled worker returned success")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("cancellation error = %v", err)
	}
	if len(events) == 0 || events[len(events)-1].Kind != "signal" || events[len(events)-1].Stream != "control" || !strings.Contains(events[len(events)-1].Message, "process group terminated") {
		t.Fatalf("cancellation events = %#v", events)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("worker cancellation took %s", elapsed)
	}
	rawPID, readErr := os.ReadFile(pidFile)
	if readErr != nil {
		t.Fatalf("child did not record PID: %v", readErr)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(rawPID)))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	deadline := time.Now().Add(time.Second)
	for {
		probeErr := syscall.Kill(pid, 0)
		if probeErr == syscall.ESRCH {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker descendant %d survived cancellation (probe error %v)", pid, probeErr)
		}
		time.Sleep(10 * time.Millisecond)
	}

	logRoot := filepath.Join(directory, "logs")
	manager, managerErr := NewPackageLog(PackageLogOptions{
		Root: logRoot, TempDir: filepath.Join(directory, "log-temp"), Category: "cat", PF: "pkg-1",
		Now: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
	})
	if managerErr != nil {
		t.Fatal(managerErr)
	}
	if _, persistErr := persistWorkerEvents(request, events, err, WorkerOptions{DurableLog: manager}); persistErr == nil || !strings.Contains(persistErr.Error(), manager.Path()) {
		t.Fatalf("persist cancellation error = %v", persistErr)
	}
	logContent, readErr := os.ReadFile(manager.Path())
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(logContent), `kind="signal" stream="control"`) || !strings.Contains(string(logContent), `kind="terminal-error" stream="stderr"`) || !strings.Contains(string(logContent), "cancelled") {
		t.Fatalf("durable cancellation log = %s", logContent)
	}
}

func TestBashWorkerReceivesControlledRootAndScratchContract(t *testing.T) {
	directory := t.TempDir()
	dirs := make(map[string]string)
	for _, name := range []string{"root", "sysroot", "broot", "temp", "home"} {
		dirs[name] = filepath.Join(directory, name)
		if err := os.MkdirAll(dirs[name], 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ebuild := filepath.Join(directory, "pkg.ebuild")
	content := "EAPI=8\nsrc_compile() { printf '%s|%s|%s|%s|%s|%s|%s|%s\\n' \"$ROOT\" \"$SYSROOT\" \"$BROOT\" \"$T\" \"$TMPDIR\" \"$TMP\" \"$TEMP\" \"$HOME\"; }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "roots-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild, RootDir: dirs["root"], SysrootDir: dirs["sysroot"], BrootDir: dirs["broot"], TempDir: dirs["temp"], HomeDir: dirs["home"]}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{dirs["root"], dirs["sysroot"], dirs["broot"], dirs["temp"], dirs["temp"], dirs["temp"], dirs["temp"], dirs["home"]}, "|")
	if len(events) != 3 || events[1].Message != want {
		t.Fatalf("controlled environment = %#v, want %q", events, want)
	}
}

func TestBashWorkerReceivesPortagePhaseAndBuildEnvironment(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := `EAPI=8
src_compile() {
  printf '%s|%s|%s|%s\n' "$EBUILD_PHASE" "$EBUILD_PHASE_FUNC" "$PORTAGE_BUILDDIR" "$PORTAGE_CONFIGROOT"
}`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	buildDir := filepath.Join(directory, "build")
	configRoot := filepath.Join(directory, "config")
	request := Request{
		Protocol: Version, ID: "portage-env", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild,
		WorkDir: directory, SourceDir: directory, BuildDir: buildDir, ConfigRoot: configRoot,
	}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("phase environment: %v; events=%#v", err, events)
	}
	want := "compile|src_compile|" + buildDir + "|" + configRoot
	found := false
	for _, event := range events {
		if event.Kind == "log" && event.Message == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("phase environment missing %q: %#v", want, events)
	}
}

func TestBashWorkerSourcesNestedEclassesAndExportedPhase(t *testing.T) {
	directory := t.TempDir()
	eclassDir := filepath.Join(directory, "eclass")
	if err := os.MkdirAll(eclassDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(eclassDir, "base.eclass"), []byte("BASE_VALUE=base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	derived := "inherit base\nderived_src_compile() { printf '%s-%s\\n' \"$BASE_VALUE\" derived; }\nEXPORT_FUNCTIONS src_compile\n"
	if err := os.WriteFile(filepath.Join(eclassDir, "derived.eclass"), []byte(derived), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\ninherit derived\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "inherit-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild, EclassDirs: []string{eclassDir}}
	command := exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker)
	events, err := runWorkerCommand(command, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[1].Message != "base-derived" {
		t.Fatalf("events = %#v", events)
	}
}

func TestBashWorkerDiscoversEbuildAndExportedEclassPhases(t *testing.T) {
	directory := t.TempDir()
	eclassDir := filepath.Join(directory, "eclass")
	if err := os.MkdirAll(eclassDir, 0o755); err != nil {
		t.Fatal(err)
	}
	eclass := "tool_src_compile() { :; }\nEXPORT_FUNCTIONS src_compile\n"
	if err := os.WriteFile(filepath.Join(eclassDir, "tool.eclass"), []byte(eclass), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\ninherit tool\nsrc_install() { :; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "discover-1", Command: "discover_phases", EAPI: "8", Ebuild: ebuild, EclassDirs: []string{eclassDir}}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatal(err)
	}
	var phases []string
	for _, event := range events {
		if event.Kind == "phase" {
			phases = append(phases, event.Message)
		}
	}
	if got := strings.Join(phases, " "); got != "src_compile src_install" {
		t.Fatalf("discovered phases = %q; events = %#v", got, events)
	}
}

func TestBashWorkerInstalledEnvironmentCannotReplaceSandboxPolicy(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(directory, "environment")
	content := "export EAPI=8\n" +
		"export SANDBOX_WRITE=/stale/original/build\n" +
		"export LD_PRELOAD=/stale/libsandbox.so\n" +
		"pkg_postrm() { printf '%s|%s\\n' \"$SANDBOX_WRITE\" \"$LD_PRELOAD\"; }\n"
	if err := os.WriteFile(installed, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Protocol: Version, ID: "installed-sandbox-policy", Command: "run_phase", Phase: "pkg_postrm",
		EAPI: "8", Ebuild: ebuild, Environment: installed,
		WorkDir: directory, TempDir: directory,
		Env: map[string]string{"SANDBOX_WRITE": directory},
	}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("%v; events=%#v", err, events)
	}
	if len(events) != 3 || events[1].Message != directory+"|" {
		t.Fatalf("sandbox policy was replaced by installed environment: %#v", events)
	}
}

func TestBashWorkerRejectsEAPIMismatchAfterSource(t *testing.T) {
	ebuild := filepath.Join(t.TempDir(), "pkg.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=9\nsrc_compile() { :; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "eapi-mismatch", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err == nil || len(events) != 2 || events[0].Kind != "log" || !strings.Contains(events[0].Message, "does not match") {
		t.Fatalf("events = %#v, error = %v", events, err)
	}
}

func TestDefaultSrcPrepareAppliesUserPatches(t *testing.T) {
	directory := t.TempDir()
	workDir := filepath.Join(directory, "work")
	patchDir := filepath.Join(directory, "patches")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(patchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	message := filepath.Join(workDir, "message.txt")
	if err := os.WriteFile(message, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patch := "--- a/message.txt\n+++ b/message.txt\n@@ -1 +1 @@\n-old\n+new\n"
	if err := os.WriteFile(filepath.Join(patchDir, "001-message.patch"), []byte(patch), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "prepare-1", Command: "run_phase", Phase: "src_prepare", EAPI: "8", Ebuild: ebuild, WorkDir: workDir, UserPatchDirs: []string{patchDir}}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("events = %#v: %v", events, err)
	}
	content, err := os.ReadFile(message)
	if err != nil || string(content) != "new\n" {
		t.Fatalf("message = %q, %v", content, err)
	}
}

func TestEapplyUserMoreSpecificBasenameOverridesGeneric(t *testing.T) {
	directory := t.TempDir()
	workDir := filepath.Join(directory, "work")
	generic := filepath.Join(directory, "generic")
	specific := filepath.Join(directory, "specific")
	for _, path := range []string{workDir, generic, specific} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	message := filepath.Join(workDir, "message.txt")
	if err := os.WriteFile(message, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	genericPatch := "--- a/message.txt\n+++ b/message.txt\n@@ -1 +1 @@\n-old\n+generic\n"
	specificPatch := "--- a/message.txt\n+++ b/message.txt\n@@ -1 +1 @@\n-old\n+specific\n"
	if err := os.WriteFile(filepath.Join(generic, "same.patch"), []byte(genericPatch), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specific, "same.patch"), []byte(specificPatch), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "prepare-override", Command: "run_phase", Phase: "src_prepare", EAPI: "8", Ebuild: ebuild, WorkDir: workDir, UserPatchDirs: []string{generic, specific}}
	if _, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(message)
	if err != nil || string(content) != "specific\n" {
		t.Fatalf("message = %q, %v", content, err)
	}
}

func TestWorkerDoesNotEnableNounset(t *testing.T) {
	ebuild := filepath.Join(t.TempDir(), "pkg.ebuild")
	content := "EAPI=8\nsrc_compile() { printf '<%s>\\n' \"$OPTIONAL_FLAG\"; }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "unset-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil || len(events) != 3 || events[1].Message != "<>" {
		t.Fatalf("events = %#v, error = %v", events, err)
	}
}

func TestDefaultConfigureCompileTestInstallPipeline(t *testing.T) {
	directory := t.TempDir()
	sourceDir := filepath.Join(directory, "source")
	imageDir := filepath.Join(directory, "image")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configure := "#!/bin/sh\nprintf 'configured\\n' > configured\n"
	if err := os.WriteFile(filepath.Join(sourceDir, "configure"), []byte(configure), 0o755); err != nil {
		t.Fatal(err)
	}
	makefile := "all:\n\tprintf 'built\\n' > built\ncheck:\n\ttest -f built\ninstall:\n\tmkdir -p $(DESTDIR)/usr/bin\n\tcp built $(DESTDIR)/usr/bin/built\n"
	if err := os.WriteFile(filepath.Join(sourceDir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "README"), []byte("docs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nPF=pkg-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"src_configure", "src_compile", "src_test", "src_install"} {
		request := Request{Protocol: Version, ID: "default-" + phase, Command: "run_phase", Phase: phase, EAPI: "8", Ebuild: ebuild, WorkDir: directory, SourceDir: sourceDir, ImageDir: imageDir}
		events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
		if err != nil {
			t.Fatalf("%s events = %#v: %v", phase, events, err)
		}
	}
	for _, path := range []string{filepath.Join(sourceDir, "configured"), filepath.Join(sourceDir, "built"), filepath.Join(imageDir, "usr/bin/built"), filepath.Join(imageDir, "usr/share/doc/pkg-1/README")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected output %s: %v", path, err)
		}
	}
}

func TestBashWorkerFailsForMissingAndCircularEclass(t *testing.T) {
	for _, fixture := range []struct {
		name, ebuild string
		files        map[string]string
	}{
		{name: "missing", ebuild: "EAPI=8\ninherit absent\n"},
		{name: "circular", ebuild: "EAPI=8\ninherit a\n", files: map[string]string{"a.eclass": "inherit b\n", "b.eclass": "inherit a\n"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			directory := t.TempDir()
			eclassDir := filepath.Join(directory, "eclass")
			if err := os.MkdirAll(eclassDir, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, content := range fixture.files {
				if err := os.WriteFile(filepath.Join(eclassDir, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			ebuild := filepath.Join(directory, "pkg.ebuild")
			if err := os.WriteFile(ebuild, []byte(fixture.ebuild), 0o644); err != nil {
				t.Fatal(err)
			}
			request := Request{Protocol: Version, ID: fixture.name, Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild, EclassDirs: []string{eclassDir}}
			events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
			if err == nil || len(events) < 2 || events[len(events)-1].ExitCode == nil || *events[len(events)-1].ExitCode == 0 {
				t.Fatalf("events = %#v, error = %v", events, err)
			}
		})
	}
}

func TestPortageSandboxWorkerIsDefault(t *testing.T) {
	ebuild := filepath.Join(t.TempDir(), "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_compile() { :; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "isolated-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild}
	events, err := RunBashWorker(context.Background(), request)
	if err != nil {
		if strings.Contains(err.Error(), "Portage sandbox is required") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	if len(events) < 2 || events[len(events)-1].Kind != "result" {
		t.Fatalf("sandbox worker returned incomplete events: %#v", events)
	}
}

func TestWorkerHostDirectoryNeverInheritsInvocationDirectory(t *testing.T) {
	t.Run("work directory", func(t *testing.T) {
		request := Request{
			Ebuild:  "/var/db/repos/gentoo/app-misc/pkg/pkg-1.ebuild",
			WorkDir: "/var/tmp/portage/app-misc/pkg-1/work",
		}
		if got, want := workerHostDirectory(request), request.WorkDir; got != want {
			t.Fatalf("worker directory = %q, want %q", got, want)
		}
	})
	t.Run("metadata fallback", func(t *testing.T) {
		request := Request{Ebuild: "/var/db/repos/gentoo/app-misc/pkg/pkg-1.ebuild"}
		if got, want := workerHostDirectory(request), filepath.Dir(request.Ebuild); got != want {
			t.Fatalf("worker directory = %q, want %q", got, want)
		}
	})
}

func TestPortageWorkerStartsOutsideInaccessibleCallerDirectory(t *testing.T) {
	if os.Getenv("ARISE_INACCESSIBLE_CWD_HELPER") == "1" {
		work := os.Getenv("ARISE_TEST_WORKDIR")
		ebuild := filepath.Join(work, "pkg-1.ebuild")
		request := Request{
			Protocol: Version, ID: "inaccessible-cwd", Command: "run_phase",
			Phase: "src_unpack", EAPI: "8", Ebuild: ebuild, WorkDir: work,
			Policy: ExecutionPolicy{Configured: true, Sandbox: true, DropPrivileges: true},
		}
		events, err := RunBashWorkerWithOptions(context.Background(), request, WorkerOptions{Isolation: IsolationPortage})
		if err != nil {
			t.Fatal(err)
		}
		if len(events) < 2 || events[len(events)-1].Kind != "result" {
			t.Fatalf("worker returned incomplete events: %#v", events)
		}
		return
	}
	if os.Geteuid() != 0 {
		t.Skip("userpriv regression requires root")
	}
	if _, err := exec.LookPath("sandbox"); err != nil {
		t.Skip("Portage sandbox is unavailable")
	}
	if _, err := user.Lookup("portage"); err != nil {
		t.Skip("portage account is unavailable")
	}

	base := t.TempDir()
	caller := filepath.Join(base, "caller")
	work := filepath.Join(base, "work")
	if err := os.Mkdir(caller, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(work, "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_unpack() { :; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(os.Args[0], "-test.run=^TestPortageWorkerStartsOutsideInaccessibleCallerDirectory$")
	command.Dir = caller
	command.Env = append(os.Environ(),
		"ARISE_INACCESSIBLE_CWD_HELPER=1",
		"ARISE_TEST_WORKDIR="+work,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("worker inherited inaccessible caller directory: %v\n%s", err, output)
	}
}

func TestBubblewrapEnhancedModeNeverFallsBack(t *testing.T) {
	ebuild := filepath.Join(t.TempDir(), "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_compile() { :; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "enhanced-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild}
	command, _, commandErr := isolatedBashCommand(context.Background(), request, false)
	if commandErr == nil && command.Dir != "/" {
		t.Fatalf("bubblewrap launcher directory = %q, want /", command.Dir)
	}
	events, err := RunBashWorkerWithOptions(context.Background(), request, WorkerOptions{Isolation: IsolationBubblewrap})
	if err == nil && (len(events) < 2 || events[len(events)-1].Kind != "result") {
		t.Fatalf("enhanced worker returned incomplete events: %#v", events)
	}
	if err != nil && !strings.Contains(err.Error(), "phase isolation") {
		t.Fatalf("enhanced isolation failure was not explicit: %v", err)
	}
}

func TestNamespaceSelectionDegradesIndependently(t *testing.T) {
	specs := namespaceSpecs(NamespaceOptions{Network: true, IPC: true, Mount: true, PID: true})
	enabled, warnings := selectNamespaces(specs, func(arguments []string) error {
		argument := strings.Join(arguments, " ")
		if argument == "--net" || argument == "--pid --fork" {
			return errors.New("operation not permitted")
		}
		return nil
	})
	if got, want := strings.Join(enabled, " "), "--ipc --mount"; got != want {
		t.Fatalf("enabled namespaces = %q, want %q", got, want)
	}
	if len(warnings) != 2 || !strings.Contains(warnings[0], "network") || !strings.Contains(warnings[1], "PID") {
		t.Fatalf("namespace warnings = %#v", warnings)
	}
}

func TestNamespaceSelectionNeverRequestsUserNamespace(t *testing.T) {
	for _, spec := range namespaceSpecs(NamespaceOptions{Network: true, IPC: true, Mount: true, PID: true}) {
		if strings.Contains(strings.Join(spec.arguments, " "), "user") {
			t.Fatalf("Portage-compatible backend requested a user namespace: %#v", spec)
		}
	}
}

func TestNamespaceCommandWrapsUnsandboxedWorkerAsOuterExecutable(t *testing.T) {
	executable, arguments := namespaceCommand("/usr/bin/unshare", []string{"--net"}, "/bin/bash", []string{"--noprofile", "worker"})
	if executable != "/usr/bin/unshare" {
		t.Fatalf("executable=%q", executable)
	}
	if got, want := strings.Join(arguments, " "), "--net -- /bin/bash --noprofile worker"; got != want {
		t.Fatalf("arguments=%q want %q", got, want)
	}
}

func TestNamespaceCommandWrapsSandboxedWorkerAsOuterExecutable(t *testing.T) {
	executable, arguments := namespaceCommand("/usr/bin/unshare", []string{"--ipc", "--mount"}, "/usr/bin/sandbox", []string{"/bin/bash", "worker"})
	if executable != "/usr/bin/unshare" {
		t.Fatalf("executable=%q", executable)
	}
	if got, want := strings.Join(arguments, " "), "--ipc --mount -- /usr/bin/sandbox /bin/bash worker"; got != want {
		t.Fatalf("arguments=%q want %q", got, want)
	}
}

func TestPhaseEnvironmentOverlayCarriesStateAcrossWorkers(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := "EAPI=8\npkg_setup() { PHASE_VALUE=from-setup; PHASE_ARRAY=(one two); SANDBOX_WRITE=/package-only; }\nsrc_prepare() { [[ $PHASE_VALUE == from-setup && ${PHASE_ARRAY[1]} == two ]]; }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(directory, "phase.environment")
	base := Request{Protocol: Version, EAPI: "8", Ebuild: ebuild, WorkDir: directory, SourceDir: directory, ImageDir: filepath.Join(directory, "image"), TempDir: directory, Policy: ExecutionPolicy{Configured: true, Sandbox: true}}
	setup := base
	setup.ID, setup.Command, setup.Phase, setup.SaveEnvironment = "overlay-setup", "run_phase", "pkg_setup", overlay
	setup.Policy.Sandbox = false
	if _, err := RunBashWorkerWithOptions(context.Background(), setup, WorkerOptions{Isolation: IsolationPortage}); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(overlay)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "SANDBOX_WRITE") || strings.Contains(string(saved), "LD_PRELOAD") {
		t.Fatalf("phase overlay persisted package-manager isolation controls: %s", saved)
	}
	prepare := base
	prepare.ID, prepare.Command, prepare.Phase, prepare.EnvironmentOverlay = "overlay-prepare", "run_phase", "src_prepare", overlay
	if _, err := RunBashWorkerWithOptions(context.Background(), prepare, WorkerOptions{Isolation: IsolationPortage}); err != nil {
		t.Fatal(err)
	}
}

func TestPortageWorkerGracefullyDegradesUnavailableNamespaces(t *testing.T) {
	ebuild := filepath.Join(t.TempDir(), "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_compile() { printf 'ran\\n'; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "namespace-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild}
	var diagnostics strings.Builder
	events, err := RunBashWorkerWithOptions(context.Background(), request, WorkerOptions{
		Isolation:   IsolationPortage,
		Namespaces:  NamespaceOptions{Network: true, IPC: true, Mount: true, PID: true},
		Diagnostics: &diagnostics,
	})
	if err != nil {
		if strings.Contains(err.Error(), "Portage sandbox is required") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	if len(events) != 3 || events[1].Message != "ran" || events[2].ExitCode == nil || *events[2].ExitCode != 0 {
		t.Fatalf("namespace worker events = %#v; diagnostics = %q", events, diagnostics.String())
	}
}

func TestFilesystemSandboxHidesUnboundHostFiles(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	secret := filepath.Join(directory, "host-secret")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	content := "EAPI=8\nsrc_compile() { if test -e \"$HOST_SECRET\"; then echo visible; else echo hidden; fi; }\n"
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "fs-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild, Env: map[string]string{"HOST_SECRET": secret}}
	command, isolatedRequest, err := isolatedBashCommand(context.Background(), request, false)
	if err != nil {
		t.Skipf("filesystem isolation unavailable: %v", err)
	}
	events, err := runWorkerCommand(command, isolatedRequest)
	if err != nil {
		t.Fatalf("filesystem-isolated worker: %v", err)
	}
	if len(events) != 3 || events[1].Message != "hidden" {
		t.Fatalf("host file visibility events = %#v", events)
	}
}

func TestFilesystemSandboxBindsPackageFilesReadOnly(t *testing.T) {
	directory := t.TempDir()
	files := filepath.Join(directory, "files")
	if err := os.MkdirAll(files, 0o755); err != nil {
		t.Fatal(err)
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	command, isolated, err := isolatedBashCommand(context.Background(), Request{Protocol: Version, ID: "filesdir-bind", Command: "run_phase", Phase: "src_prepare", EAPI: "8", Ebuild: ebuild}, false)
	if err != nil {
		t.Skipf("filesystem isolation unavailable: %v", err)
	}
	arguments := strings.Join(command.Args, "\x00")
	want := "--ro-bind\x00" + files + "\x00/run/arise/files"
	if !strings.Contains(arguments, want) {
		t.Fatalf("FILESDIR binding missing from %#v", command.Args)
	}
	if isolated.Ebuild != "/run/arise/ebuild" {
		t.Fatalf("isolated ebuild = %q", isolated.Ebuild)
	}
}

func TestBubblewrapMakesRootReadOnlyAndLeavesImageWritable(t *testing.T) {
	directory := t.TempDir()
	root := filepath.Join(directory, "root")
	image := filepath.Join(directory, "image")
	work := filepath.Join(directory, "work")
	for _, path := range []string{root, image, work} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := `EAPI=8
pkg_preinst() {
	printf image > "${ED}/image-marker"
	printf escaped > "${ROOT}/root-marker" || return 125
}
`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Protocol: Version, ID: "readonly-root-1", Command: "run_phase", Phase: "pkg_preinst", EAPI: "8", Ebuild: ebuild,
		RootDir: root, ImageDir: image, WorkDir: work, BuildDir: work, SourceDir: work, TempDir: work, HomeDir: work,
	}
	events, workerErr := RunBashWorkerWithOptions(context.Background(), request, WorkerOptions{Isolation: IsolationBubblewrap})
	if workerErr != nil && len(events) == 0 && strings.Contains(workerErr.Error(), "worker did not start") {
		t.Skipf("bubblewrap unavailable in test environment: %v", workerErr)
	}
	if workerErr == nil {
		t.Fatalf("ROOT write unexpectedly succeeded: %#v", events)
	}
	if _, err := os.Stat(filepath.Join(image, "image-marker")); err != nil {
		t.Fatalf("writable image marker: %v; worker error=%v events=%#v", err, workerErr, events)
	}
	if _, err := os.Stat(filepath.Join(root, "root-marker")); !os.IsNotExist(err) {
		t.Fatalf("read-only ROOT was modified: %v", err)
	}
}

func TestBashWorkerRejectsReservedEnvironment(t *testing.T) {
	for _, name := range []string{"BASH_ENV", "ROOT", "SYSROOT", "BROOT", "HOME", "TMPDIR", "PORTAGE_LOG_FILE", "CATEGORY", "PF", "SLOT", "PORTAGE_REPO_NAME"} {
		request := Request{Protocol: Version, ID: "pkg-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: "/tmp/pkg.ebuild", Env: map[string]string{name: "/tmp/inject"}}
		if _, err := RunBashWorker(context.Background(), request); err == nil {
			t.Fatalf("reserved environment %s accepted", name)
		}
	}
}

func TestRequestRequiresAbsolutePortageLogFile(t *testing.T) {
	request := Request{Protocol: Version, ID: "log-path", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: "/tmp/pkg.ebuild", LogFile: "relative.log"}
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "PORTAGE_LOG_FILE") {
		t.Fatalf("Validate error = %v", err)
	}
}

func TestWorkerEnvironmentIsMinimalAndExplicit(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	content := `EAPI=8
src_compile() {
  printf 'leak=%s custom=%s path=%s locale=%s id=%s phase=%s\n' \
    "${HOST_ENV_LEAK-unset}" "${EXPLICIT_VALUE-unset}" "$PATH" "$LC_ALL" "$ARISE_ID" "$ARISE_PHASE"
}
`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Protocol: Version,
		ID:       "environment-1",
		Command:  "run_phase",
		Phase:    "src_compile",
		EAPI:     "8",
		Ebuild:   ebuild,
		Env:      map[string]string{"EXPLICIT_VALUE": "present"},
	}
	command := exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker)
	command.Env = append(os.Environ(), "HOST_ENV_LEAK=poison")
	events, err := runWorkerCommand(command, request)
	if err != nil {
		t.Fatalf("minimal environment worker: %v; events=%#v", err, events)
	}
	if len(events) != 3 {
		t.Fatalf("events = %#v, want phase, log, result", events)
	}
	want := "leak=unset custom=present path=/usr/sbin:/usr/bin:/sbin:/bin locale=C id=environment-1 phase=src_compile"
	if events[1].Kind != "log" || events[1].Message != want {
		t.Fatalf("environment log = %#v, want %q", events[1], want)
	}
}

func TestNamedExecutablePreservesSandboxArgumentsAndPortageTitle(t *testing.T) {
	executable, arguments := namedExecutable("[sci-misc/llama-cpp-0_pre9888] sandbox", "/usr/bin/sandbox", []string{"/bin/bash", "--noprofile"})
	if executable != "/bin/bash" {
		t.Fatalf("named executable = %q", executable)
	}
	want := []string{"-c", `printf -v process_name '%b' "$1"; exec -a "$process_name" "$2" "${@:3}"`, "arise-exec-a", `[sci-misc/llama-cpp-0_pre9888]\x20sandbox`, "/usr/bin/sandbox", "/bin/bash", "--noprofile"}
	if !reflect.DeepEqual(arguments, want) {
		t.Fatalf("named arguments = %#v, want %#v", arguments, want)
	}
	if strings.Contains(strings.Join(arguments, " "), " sandbox ") {
		t.Fatalf("outer launcher exposes a duplicate genlop sandbox match: %#v", arguments)
	}
}

func TestNamedExecutableDecodesPortageTitleOnlyAtExec(t *testing.T) {
	executable, arguments := namedExecutable(
		"[dev-libs/example-1.0] sandbox",
		"/bin/bash",
		[]string{"-c", `printf '%s' "$0"`},
	)
	output, err := exec.Command(executable, arguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("named executable failed: %v: %s", err, output)
	}
	if got, want := string(output), "[dev-libs/example-1.0] sandbox"; got != want {
		t.Fatalf("executed argv[0] = %q, want %q", got, want)
	}
}

func TestWorkerExposesProtocolOwnedPackageIdentity(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "python-3.13.7-r2.ebuild")
	content := `EAPI=8
src_compile() {
  printf '%s|%s|%s|%s|%s|%s|%s|%s|%s\n' "$CATEGORY" "$PN" "$PV" "$PR" "$P" "$PVR" "$PF" "$SLOT" "$PORTAGE_REPO_NAME"
}
`
	if err := os.WriteFile(ebuild, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Protocol: Version, ID: "identity-1", Command: "run_phase", Phase: "src_compile", EAPI: "8", Ebuild: ebuild,
		Package: PackageIdentity{Category: "dev-lang", PN: "python", PV: "3.13.7", PR: "r2", P: "python-3.13.7", PVR: "3.13.7-r2", PF: "python-3.13.7-r2", Slot: "3.13/3.13", Repository: "gentoo"},
	}
	events, err := runWorkerCommand(exec.CommandContext(context.Background(), "bash", "--noprofile", "--norc", "-c", bashWorker), request)
	if err != nil {
		t.Fatalf("identity worker: %v; events=%#v", err, events)
	}
	want := "dev-lang|python|3.13.7|r2|python-3.13.7|3.13.7-r2|python-3.13.7-r2|3.13/3.13|gentoo"
	if len(events) != 3 || events[1].Message != want {
		t.Fatalf("identity events = %#v, want log %q", events, want)
	}
}

func TestWorkerAcceptsOnlyVerifiedDistfileSet(t *testing.T) {
	directory := t.TempDir()
	ebuild := filepath.Join(directory, "pkg-1.ebuild")
	if err := os.WriteFile(ebuild, []byte("EAPI=8\nsrc_unpack() { printf '%s\\n' \"$DISTDIR\"; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content := []byte("source")
	digest := sha512.Sum512(content)
	artifact := distfiles.Artifact{Name: "source.tar", Size: int64(len(content)), Digests: map[string]string{"SHA512": hex.EncodeToString(digest[:])}}
	path := filepath.Join(directory, artifact.Name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{Protocol: Version, ID: "verified-1", Command: "run_phase", Phase: "src_unpack", EAPI: "8", Ebuild: ebuild, Distfiles: &distfiles.VerifiedSet{Directory: directory, Artifacts: []distfiles.Artifact{artifact}}}
	events, err := RunBashWorker(context.Background(), request)
	if err != nil {
		if strings.Contains(err.Error(), "Portage sandbox is required") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	if len(events) != 3 || events[1].Message != directory {
		t.Fatalf("events = %#v", events)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RunBashWorker(context.Background(), request); err == nil || !strings.Contains(err.Error(), "refusing unverified distfile") {
		t.Fatalf("corrupt verified set error = %v", err)
	}
}
