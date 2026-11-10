package phaseproto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDestinationSelectionCreatesDirectoriesWithoutChangingExistingModes(t *testing.T) {
	request := phaseStateRequest(t, `src_install() {
	diropts -m0700
	for helper in into insinto exeinto; do
		"$helper" "/opt/$helper"
		[[ -d $ED/opt/$helper ]] || die 'destination selection did not create its directory'
		[[ $(stat -c %a "$ED/opt/$helper") == 755 ]] || die 'destination selection used diropts'
		chmod 750 "$ED/opt/$helper" || die
		"$helper" "/opt/$helper"
		[[ $(stat -c %a "$ED/opt/$helper") == 750 ]] || die 'destination selection changed an existing mode'
	done
	printf payload > input
	insinto /opt/insinto
	doins input
	newins input renamed
	[[ $(stat -c %a "$ED/opt/insinto") == 750 ]] || die 'file installation changed the existing directory mode'
}`)
	if err := os.MkdirAll(request.SourceDir, 0755); err != nil {
		t.Fatal(err)
	}
	runPhaseStateSequence(t, request, true, "src_install")
}

func TestLibraryHelpersUseProfileABILayout(t *testing.T) {
	for _, test := range []struct {
		name, abi, libdir, want string
	}{
		{"amd64", "amd64", "lib64", "lib64"},
		{"x86 modern layout", "x86", "lib", "lib"},
		{"x86 symlink layout", "x86", "lib32", "lib32"},
		{"x32", "x32", "libx32", "libx32"},
		{"arm64", "arm64", "lib64", "lib64"},
		{"mips n32", "n32", "lib32", "lib32"},
		{"riscv nested layout", "lp64d", "lib64/lp64d", "lib64/lp64d"},
		{"custom profile layout", "x86", "custom/lib", "custom/lib"},
		{"missing ABI directory", "amd64", "", "lib"},
		{"missing ABI", "", "lib64", "lib"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := phaseStateRequest(t, `src_configure() { econf --prefix=/opt/probe; }
src_install() {
	printf '%s' "$(get_libdir)" > "$T/libdir"
	printf archive > archive.a
	printf shared > shared.so
	into /opt/probe
	dolib.a archive.a
	newlib.a archive.a renamed.a
	dolib.so shared.so
	newlib.so shared.so renamed.so
}`)
			request.Env = map[string]string{"ABI": test.abi, "DEFAULT_ABI": "amd64", "LIBDIR_" + test.abi: test.libdir}
			if err := os.MkdirAll(request.SourceDir, 0755); err != nil {
				t.Fatal(err)
			}
			configure := "#!/bin/sh\nif [ \"$1\" = --help ]; then exit 0; fi\nprintf '%s\\n' \"$@\" > \"$T/configure.args\"\n"
			if err := os.WriteFile(filepath.Join(request.SourceDir, "configure"), []byte(configure), 0755); err != nil {
				t.Fatal(err)
			}
			runPhaseStateSequence(t, request, true, "src_configure", "src_install")
			observed, err := os.ReadFile(filepath.Join(request.TempDir, "libdir"))
			if err != nil || string(observed) != test.want {
				t.Fatalf("get_libdir = %q, %v; want %q", observed, err, test.want)
			}
			arguments, err := os.ReadFile(filepath.Join(request.TempDir, "configure.args"))
			if err != nil || !strings.Contains(string(arguments), "--libdir=/opt/probe/"+test.want+"\n") {
				t.Fatalf("configure library directory differs: %v\n%s", err, arguments)
			}
			for name, want := range map[string]string{"archive.a": "archive", "renamed.a": "archive", "shared.so": "shared", "renamed.so": "shared"} {
				installed, err := os.ReadFile(filepath.Join(request.ImageDir, "opt/probe", test.want, name))
				if err != nil || string(installed) != want {
					t.Fatalf("installed %s = %q, %v; want %q", name, installed, err, want)
				}
			}
		})
	}
}
