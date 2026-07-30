package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/perlcleaner"
)

func TestParsePerlCleanerModeMatrix(t *testing.T) {
	tests := []struct {
		arg  string
		mode perlcleaner.Mode
	}{
		{"--modules", perlcleaner.ModulesMode()},
		{"--allmodules", perlcleaner.AllModulesMode()},
		{"--libperl", perlcleaner.LibPerlMode()},
		{"--all", perlcleaner.AllMode()},
		{"--reallyall", perlcleaner.ReallyAllMode()},
	}
	for _, test := range tests {
		t.Run(test.arg, func(t *testing.T) {
			options, err := parsePerlCleanerOptions([]string{test.arg, "--pretend", "--dont-delete-leftovers"})
			if err != nil {
				t.Fatal(err)
			}
			if options.Mode != test.mode || !options.Pretend || options.DeleteLeftovers {
				t.Fatalf("options = %#v", options)
			}
		})
	}
}

func TestParsePerlCleanerRejectsMissingConflictingAndUnknownModes(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--modules", "--libperl"},
		{"--all", "--reallyall"},
		{"--modules", "--shell-command=bad"},
	} {
		if _, err := parsePerlCleanerOptions(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestPerlCleanerCommandRouteExists(t *testing.T) {
	command, args := selectCommand([]string{"perl-cleaner", "--reallyall", "--pretend"})
	if command != "perl-cleaner" || !reflect.DeepEqual(args, []string{"--reallyall", "--pretend"}) {
		t.Fatalf("route = %q %v", command, args)
	}
}

func TestPrintPerlCleanerReportIncludesTypedEvidence(t *testing.T) {
	report := perlcleaner.Report{
		Mode: perlcleaner.AllMode(),
		ABI:  perlcleaner.ABI{Version: "5.42", Arch: "x86_64-linux", LibPerlSONames: []string{"libperl.so.5.42"}},
		Preclean: perlcleaner.Preclean{
			PerlCore: []string{"perl-core/Getopt-Long"}, Virtuals: []string{"virtual/perl-Getopt-Long"},
		},
		Actions: []perlcleaner.Action{{
			CPV: "dev-perl/Foo-1", Atom: "dev-perl/Foo:0",
			Reasons: []perlcleaner.Reason{{Kind: "stale-module", Evidence: "/usr/lib64/perl5/5.40/Foo.pm"}},
		}},
	}
	var output bytes.Buffer
	printPerlCleanerReport(&output, report, perlCleanerOptions{DeleteLeftovers: false}, []perlcleaner.Leftover{{Path: "/old"}})
	for _, fragment := range []string{
		"Active Perl ABI: 5.42 (x86_64-linux)",
		"Pre-clean: 1 perl-core world candidate(s), 1 installed virtual(s)",
		"rebuild dev-perl/Foo-1 as dev-perl/Foo:0",
		"stale-module: /usr/lib64/perl5/5.40/Foo.pm",
		"Leftover-file cleanup: report only (1 candidate(s)).",
	} {
		if !strings.Contains(output.String(), fragment) {
			t.Fatalf("output missing %q:\n%s", fragment, output.String())
		}
	}
}

func TestDeselectPerlCoreRemovesConstrainedWorldEntriesOnly(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "world")
	data := "perl-core/Getopt-Long\n=perl-core/Test-Harness-3.50\nvirtual/perl-Getopt-Long\ndev-perl/Foo\n"
	if err := os.WriteFile(path, []byte(data), 0o640); err != nil {
		t.Fatal(err)
	}
	oldWorld := *worldFile
	*worldFile = path
	t.Cleanup(func() { *worldFile = oldWorld })
	if err := deselectPerlCore([]string{"perl-core/Getopt-Long", "perl-core/Test-Harness"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "dev-perl/Foo\nvirtual/perl-Getopt-Long\n" {
		t.Fatalf("world = %q", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("world mode = %o", info.Mode().Perm())
	}
}
