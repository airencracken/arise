//go:build live_portage

package perlcleaner

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/atom"
)

func TestDisposableRootEveryPerlCleanerModeDifferential(t *testing.T) {
	for _, command := range []string{"bwrap", "perl-cleaner", "perl"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("%s is not installed", command)
		}
	}
	root := t.TempDir()
	vdbRoot := filepath.Join(root, "var/db/pkg")
	writeDifferentialPackage(t, vdbRoot, "dev-lang/perl-5.42.2", "0/5.42",
		"obj /usr/lib64/perl5/5.42/x86_64-linux/Config.pm digest 1\n",
		"X86_64;/usr/lib64/libperl.so.5.42.2;libperl.so.5.42;;libc.so.6;x86_64\n")
	writeDifferentialPackage(t, vdbRoot, "dev-perl/Current-1", "0",
		"obj /usr/lib64/perl5/vendor_perl/5.42/Current.pm digest 1\n", "")
	writeDifferentialPackage(t, vdbRoot, "dev-perl/Old-1", "0",
		"obj /usr/lib64/perl5/vendor_perl/5.40/Old.pm digest 1\n", "")
	writeDifferentialPackage(t, vdbRoot, "app-misc/CurrentLink-1", "0",
		"obj /usr/bin/current-perl-consumer digest 1\n",
		"X86_64;/usr/bin/current-perl-consumer;;;libperl.so.5.42,libc.so.6;x86_64\n")
	writeDifferentialPackage(t, vdbRoot, "app-misc/OldLink-1", "0",
		"obj /usr/bin/old-perl-consumer digest 1\n",
		"X86_64;/usr/bin/old-perl-consumer;;;libperl.so.5.40,libc.so.6;x86_64\n")
	writeDifferentialPackage(t, vdbRoot, "perl-core/Getopt-Long-2.58", "0",
		"obj /usr/lib64/perl5/vendor_perl/5.40/Getopt/Long.pm digest 1\n", "")
	writeDifferentialPackage(t, vdbRoot, "virtual/perl-Getopt-Long-2.58", "0", "", "")
	writeDifferentialStubs(t, root)
	perl5 := filepath.Join(root, "usr/lib64/perl5")
	if err := os.MkdirAll(filepath.Join(perl5, "vendor_perl", "5.40"), 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		mode Mode
		arg  string
	}{
		{"modules", ModulesMode(), "--modules"},
		{"allmodules", AllModulesMode(), "--allmodules"},
		{"libperl", LibPerlMode(), "--libperl"},
		{"all", AllMode(), "--all"},
		{"reallyall", ReallyAllMode(), "--reallyall"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, err := Check(vdbRoot, test.mode)
			if err != nil {
				t.Fatal(err)
			}
			arise := actionCPs(report)
			reference, output := runDisposablePerlCleaner(t, root, test.arg)
			if strings.Join(arise, "\n") != strings.Join(reference, "\n") {
				t.Fatalf("rebuild set differs\nArise: %v\nperl-cleaner: %v\n%s", arise, reference, output)
			}
			if test.mode.Preclean {
				if !reflect.DeepEqual(report.Preclean.PerlCore, []string{"perl-core/Getopt-Long"}) ||
					!reflect.DeepEqual(report.Preclean.Virtuals, []string{"virtual/perl-Getopt-Long"}) {
					t.Fatalf("preclean = %#v", report.Preclean)
				}
				if !strings.Contains(output, "perl-core/Getopt-Long") ||
					!strings.Contains(output, "virtual/perl-Getopt-Long") {
					t.Fatalf("reference preclean evidence missing:\n%s", output)
				}
			}
		})
	}
}

func TestDisposableRootLeftoverClassificationDifferential(t *testing.T) {
	for _, command := range []string{"bwrap", "perl-cleaner", "perl"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("%s is not installed", command)
		}
	}
	root := t.TempDir()
	vdbRoot := filepath.Join(root, "var/db/pkg")
	writeDifferentialPackage(t, vdbRoot, "dev-lang/perl-5.42.2", "0/5.42",
		"obj /usr/lib64/perl5/5.42/x86_64-linux/Config.pm digest 1\n",
		"X86_64;/usr/lib64/libperl.so.5.42.2;libperl.so.5.42;;libc.so.6;x86_64\n")
	writeDifferentialStubs(t, root)
	files := map[string]string{
		"usr/lib64/perl5/vendor_perl/5.40/features.ph": "known",
		"usr/lib64/perl5/vendor_perl/5.40/Custom.pm":   "unknown",
		"usr/lib64/perl5/vendor_perl/5.42/Current.pm":  "current",
	}
	for relative, data := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	report, err := Check(vdbRoot, ModulesMode())
	if err != nil {
		t.Fatal(err)
	}
	leftovers, err := FindLeftovers(root, report.ABI)
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 2 || leftovers[0].Known || !leftovers[1].Known {
		t.Fatalf("Arise leftovers = %#v", leftovers)
	}
	_, output := runDisposablePerlCleaner(t, root, "--modules")
	for _, fragment := range []string{
		"/usr/lib64/perl5/vendor_perl/5.40/Custom.pm",
		"/usr/lib64/perl5/vendor_perl/5.40/features.ph",
		"known, can be deleted",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("reference output missing %q:\n%s", fragment, output)
		}
	}
	if strings.Contains(output, "/usr/lib64/perl5/vendor_perl/5.42/Current.pm") {
		t.Fatalf("reference classified current file as leftover:\n%s", output)
	}
}

func runDisposablePerlCleaner(t *testing.T, root, mode string) ([]string, string) {
	t.Helper()
	path := filepath.Join(root, "bin") + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	command := exec.Command("bwrap",
		"--ro-bind", "/", "/",
		"--bind", filepath.Join(root, "var/db/pkg"), "/var/db/pkg",
		"--bind", filepath.Join(root, "usr/lib64/perl5"), "/usr/lib64/perl5",
		"--proc", "/proc", "--dev", "/dev",
		"--setenv", "PATH", path,
		"/usr/sbin/perl-cleaner", mode, "--pretend", "--verbose", "--verbose",
		"--dont-delete-leftovers", "--package-manager-command", "/bin/true",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("perl-cleaner %s: %v\n%s", mode, err, output)
	}
	pattern := regexp.MustCompile(`Adding to list:\s+(\S+)`)
	var result []string
	for _, match := range pattern.FindAllStringSubmatch(string(output), -1) {
		parsed, parseErr := atom.Parse(match[1])
		if parseErr != nil {
			t.Fatalf("parse reference atom %q: %v", match[1], parseErr)
		}
		result = append(result, parsed.CP())
	}
	sort.Strings(result)
	return compact(result), string(output)
}

func actionCPs(report Report) []string {
	var result []string
	for _, action := range report.Actions {
		parsed, err := atom.Parse(action.Atom)
		if err != nil {
			panic(err)
		}
		result = append(result, parsed.CP())
	}
	sort.Strings(result)
	return compact(result)
}

func writeDifferentialStubs(t *testing.T, root string) {
	t.Helper()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	scripts := map[string]string{
		"qlist": "#!/bin/bash\n" +
			"printf '%s\\n' perl-core/Getopt-Long virtual/perl-Getopt-Long\n" +
			"exit 0\n",
		"emerge": "#!/bin/bash\n" +
			"printf 'stub emerge %s\\n' \"$*\"\n" +
			"exit 0\n",
		"scanelf": "#!/bin/bash\n" +
			"if [[ \"$1\" == '-qBS' ]]; then\n" +
			"  printf '%s\\n' 'libperl.so.5.42 provider'\n" +
			"  exit 0\n" +
			"fi\n" +
			"input=''\n" +
			"while IFS= read -r line; do\n" +
			"  input+=\"$line\"\n" +
			"done\n" +
			"if [[ \"$input\" == *old-perl-consumer* ]]; then\n" +
			"  printf '%s\\n' 'libperl.so.5.40'\n" +
			"elif [[ \"$input\" == *current-perl-consumer* ]]; then\n" +
			"  printf '%s\\n' 'libperl.so.5.42'\n" +
			"fi\n" +
			"exit 0\n",
	}
	for name, data := range scripts {
		path := filepath.Join(bin, name)
		if err := os.WriteFile(path, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func writeDifferentialPackage(t *testing.T, root, cpv, slot, contents, needed string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(cpv))
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"CONTENTS": contents, "EAPI": "8\n", "SLOT": slot + "\n", "repository": "gentoo\n",
	} {
		if err := os.WriteFile(filepath.Join(path, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if needed != "" {
		if err := os.WriteFile(filepath.Join(path, "NEEDED.ELF.2"), []byte(needed), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
