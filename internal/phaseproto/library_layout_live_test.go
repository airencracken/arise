//go:build live_portage

package phaseproto

import (
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/airencracken/arise/internal/portage"
)

func TestLiveProfileLibraryLayoutLinks32BitConsumer(t *testing.T) {
	if _, err := exec.LookPath("cc"); err != nil {
		if os.Getenv("ARISE_REQUIRE_PHASE_CONTRACT") == "1" {
			t.Fatal("required C compiler unavailable")
		}
		t.Skip("C compiler unavailable")
	}
	for _, libdir := range []string{"lib", "lib32"} {
		t.Run(libdir, func(t *testing.T) {
			request := phaseStateRequest(t, `src_install() {
	into /usr
	dolib.so "$WORKDIR/libarise_layout_probe.so"
}`)
			request.Env = map[string]string{"ABI": "x86", "LIBDIR_x86": libdir}
			if err := os.MkdirAll(request.SourceDir, 0755); err != nil {
				t.Fatal(err)
			}
			librarySource := filepath.Join(request.WorkDir, "library.c")
			consumerSource := filepath.Join(request.WorkDir, "consumer.c")
			for path, source := range map[string]string{
				librarySource:  "int layout_probe(void) { return 7; }\n",
				consumerSource: "extern int layout_probe(void); int main(void) { return layout_probe() - 7; }\n",
			} {
				if err := os.WriteFile(path, []byte(source), 0644); err != nil {
					t.Fatal(err)
				}
			}
			// This needs no installed 32-bit libc. The consumer can only find
			// this uniquely named library through the profile's image directory.
			library := filepath.Join(request.WorkDir, "libarise_layout_probe.so")
			if output, err := exec.Command("cc", "-m32", "-fPIC", "-shared", "-nostdlib", librarySource, "-o", library).CombinedOutput(); err != nil {
				t.Fatalf("32-bit library fixture: %v\n%s", err, output)
			}
			runPhaseStateSequence(t, request, true, "src_install")
			consumer := filepath.Join(request.TempDir, "consumer")
			if output, err := exec.Command("cc", "-m32", "-nostdlib", "-Wl,-e,main", consumerSource,
				"-L"+filepath.Join(request.ImageDir, "usr", libdir), "-larise_layout_probe", "-o", consumer).CombinedOutput(); err != nil {
				t.Fatalf("consumer cannot link through profile library directory: %v\n%s", err, output)
			}
			binary, err := elf.Open(consumer)
			if err != nil {
				t.Fatal(err)
			}
			defer binary.Close()
			libraries, err := binary.ImportedLibraries()
			if err != nil || binary.Class != elf.ELFCLASS32 || len(libraries) != 1 || libraries[0] != "libarise_layout_probe.so" {
				t.Fatalf("consumer class=%v, libraries=%v, error=%v", binary.Class, libraries, err)
			}
		})
	}
}

func TestLiveMultilibProfileLayout(t *testing.T) {
	const eclasses = "/var/db/repos/gentoo/eclass"
	if _, err := os.Stat(filepath.Join(eclasses, "multilib.eclass")); err != nil {
		if os.Getenv("ARISE_REQUIRE_PHASE_CONTRACT") == "1" {
			t.Fatal("required Gentoo multilib eclass unavailable")
		}
		t.Skip("Gentoo multilib eclass unavailable")
	}
	for _, symlink := range []string{"no", "yes"} {
		t.Run("SYMLINK_LIB="+symlink, func(t *testing.T) {
			want := "lib"
			if symlink == "yes" {
				want = "lib32"
			}
			configuration := &portage.Config{MakeConf: map[string]string{
				"ABI": "amd64", "DEFAULT_ABI": "amd64", "MULTILIB_ABIS": "amd64 x86",
				"CHOST": "x86_64-pc-linux-gnu", "LIBDIR_amd64": "lib64", "LIBDIR_x86": want,
				"SYMLINK_LIB": symlink,
			}}
			request := phaseStateRequest(t, `inherit multilib
pkg_setup() { multilib_env "$CHOST"; }
src_install() {
	local ABI=x86
	[[ $(get_abi_LIBDIR) == "$EXPECTED_LIBDIR" ]] || die 'multilib_env lost the profile layout'
	printf startup > crti.o
	insinto "/usr/$(get_libdir)"
	doins crti.o
}`)
			var err error
			request.Env, err = configuration.PackageExecutionEnvironmentFor("sys-libs/glibc-2.43-r4", "2.2", "gentoo", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Env["EXPECTED_LIBDIR"] = want
			request.EclassDirs = []string{eclasses}
			if err := os.MkdirAll(request.SourceDir, 0755); err != nil {
				t.Fatal(err)
			}
			runPhaseStateSequence(t, request, true, "pkg_setup", "src_install")
			data, err := os.ReadFile(filepath.Join(request.ImageDir, "usr", want, "crti.o"))
			if err != nil || string(data) != "startup" {
				t.Fatalf("installed startup object = %q, %v", data, err)
			}
		})
	}
}
