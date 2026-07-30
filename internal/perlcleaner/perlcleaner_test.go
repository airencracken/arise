package perlcleaner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestModeContracts(t *testing.T) {
	tests := []struct {
		name string
		got  Mode
		want Mode
	}{
		{"modules", ModulesMode(), Mode{Modules: true, Leftovers: true}},
		{"allmodules", AllModulesMode(), Mode{Modules: true, AllModules: true, Leftovers: true}},
		{"libperl", LibPerlMode(), Mode{LibPerl: true, Leftovers: true}},
		{"all", AllMode(), Mode{Modules: true, LibPerl: true, Preclean: true, Leftovers: true}},
		{"reallyall", ReallyAllMode(), Mode{Modules: true, AllModules: true, LibPerl: true, Preclean: true, Leftovers: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("mode = %#v, want %#v", test.got, test.want)
			}
		})
	}
}

func TestFinalValidationDropsForceAndPrecleanButRetainsHealthChecks(t *testing.T) {
	got := FinalValidationMode(ReallyAllMode())
	want := Mode{Modules: true, LibPerl: true, Leftovers: true}
	if got != want {
		t.Fatalf("validation mode = %#v, want %#v", got, want)
	}
}

func TestCheckDistinguishesEveryRebuildMode(t *testing.T) {
	vdbRoot := perlFixture(t)
	tests := []struct {
		name string
		mode Mode
		cpvs []string
	}{
		{"modules", ModulesMode(), []string{"dev-perl/Old-1"}},
		{"allmodules", AllModulesMode(), []string{"dev-perl/Current-1", "dev-perl/Old-1"}},
		{"libperl", LibPerlMode(), []string{"app-misc/OldLink-1"}},
		{"all", AllMode(), []string{"app-misc/OldLink-1", "dev-perl/Old-1"}},
		{"reallyall", ReallyAllMode(), []string{"app-misc/CurrentLink-1", "app-misc/OldLink-1", "dev-perl/Current-1", "dev-perl/Old-1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, err := Check(vdbRoot, test.mode)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, action := range report.Actions {
				got = append(got, action.CPV)
				if action.Atom == "" || len(action.Reasons) == 0 {
					t.Fatalf("incomplete action: %#v", action)
				}
			}
			if !reflect.DeepEqual(got, test.cpvs) {
				t.Fatalf("CPVs = %v, want %v; report=%#v", got, test.cpvs, report)
			}
			if report.ABI.Version != "5.42" || report.ABI.Arch != "x86_64-linux" ||
				!reflect.DeepEqual(report.ABI.LibPerlSONames, []string{"libperl.so.5.42"}) {
				t.Fatalf("ABI = %#v", report.ABI)
			}
		})
	}
}

func TestEmptyReportJSONUsesArraysNotNull(t *testing.T) {
	report, err := Check(perlFixture(t), ModulesMode())
	if err != nil {
		t.Fatal(err)
	}
	// Make the fixture healthy for this contract assertion.
	report.Actions = []Action{}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"perl_core":null`, `"virtuals":null`, `"actions":null`} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("report violates array schema: %s", data)
		}
	}
}

func TestCheckCombinesReasonsOnceAndExcludesPerl(t *testing.T) {
	vdbRoot := perlFixture(t)
	writePackage(t, vdbRoot, "dev-perl/Both-1", "0", strings.Join([]string{
		"obj /usr/lib64/perl5/vendor_perl/5.40/Both.pm digest 1",
	}, "\n"), "X86_64;/usr/lib64/perl5/vendor_perl/5.40/auto/Both.so;;;libperl.so.5.40,libc.so.6;x86_64\n")
	report, err := Check(vdbRoot, AllMode())
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range report.Actions {
		if action.CPV == "dev-lang/perl-5.42.2" {
			t.Fatal("Perl itself must be excluded")
		}
		if action.CPV == "dev-perl/Both-1" {
			kinds := []string{action.Reasons[0].Kind, action.Reasons[1].Kind}
			if !reflect.DeepEqual(kinds, []string{"libperl", "stale-module"}) {
				t.Fatalf("combined reasons = %#v", action.Reasons)
			}
			return
		}
	}
	t.Fatal("combined stale package missing")
}

func TestAllPrecleanAndPerlCoreVirtualTargets(t *testing.T) {
	vdbRoot := perlFixture(t)
	writePackage(t, vdbRoot, "perl-core/Getopt-Long-2.58", "0",
		"obj /usr/lib64/perl5/vendor_perl/5.40/Getopt/Long.pm digest 1\n", "")
	writePackage(t, vdbRoot, "virtual/perl-Getopt-Long-2.58", "0", "", "")
	writePackage(t, vdbRoot, "virtual/perl-Carp-1.50", "0", "", "")
	report, err := Check(vdbRoot, AllMode())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Preclean.PerlCore, []string{"perl-core/Getopt-Long"}) ||
		!reflect.DeepEqual(report.Preclean.Virtuals, []string{"virtual/perl-Carp", "virtual/perl-Getopt-Long"}) {
		t.Fatalf("preclean = %#v", report.Preclean)
	}
	want := []string{
		"app-misc/OldLink:0", "dev-perl/Old:0", "perl-core/Getopt-Long:0",
		"virtual/perl-Carp", "virtual/perl-Getopt-Long",
	}
	if got := Targets(report); !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestCheckRejectsMissingOrAmbiguousABI(t *testing.T) {
	root := t.TempDir()
	if _, err := Check(root, ModulesMode()); err == nil {
		t.Fatal("missing VDB accepted")
	}
	vdbRoot := filepath.Join(root, "vdb")
	writePackage(t, vdbRoot, "dev-lang/perl-5.42.2", "0/5.42",
		"obj /usr/lib64/perl5/5.42/x86_64-linux/Foo.pm digest 1\n",
		"X86_64;/usr/lib64/libperl.so.5.42.2;libperl.so.5.42;;;x86_64\n")
	writePackage(t, vdbRoot, "dev-lang/perl-5.40.1", "0/5.40",
		"obj /usr/lib64/perl5/5.40/x86_64-linux/Foo.pm digest 1\n",
		"X86_64;/usr/lib64/libperl.so.5.40.1;libperl.so.5.40;;;x86_64\n")
	if _, err := Check(vdbRoot, ModulesMode()); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("ambiguous ABI error = %v", err)
	}
}

func TestCheckRejectsMalformedNeededMetadata(t *testing.T) {
	vdbRoot := perlFixture(t)
	path := filepath.Join(vdbRoot, "app-misc", "OldLink-1", "NEEDED.ELF.2")
	if err := os.WriteFile(path, []byte("malformed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(vdbRoot, LibPerlMode()); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("malformed linkage error = %v", err)
	}
}

func TestStaleModulePathAdversarialBoundaries(t *testing.T) {
	abi := ABI{Version: "5.42", Arch: "x86_64-linux"}
	tests := []struct {
		path  string
		stale bool
	}{
		{"/usr/lib64/perl5/5.42/Foo.pm", false},
		{"/usr/lib64/perl5/5.42/x86_64-linux/Foo.so", false},
		{"/usr/lib64/perl5/5.42/aarch64-linux/Foo.so", true},
		{"/usr/lib64/perl5/5.42/sgmlspl-specs/skel.pl", false},
		{"/usr/lib64/perl5/vendor_perl/5.40/Foo.pm", true},
		{"/usr/lib64/perl5/15.420/Foo.pm", true},
		{"/usr/share/doc/perl5/5.40/Foo.pm", true},
	}
	for _, test := range tests {
		if got := staleModulePath(test.path, abi); got != test.stale {
			t.Errorf("staleModulePath(%q) = %v, want %v", test.path, got, test.stale)
		}
	}
}

func perlFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "vdb")
	writePackage(t, root, "dev-lang/perl-5.42.2", "0/5.42",
		"obj /usr/lib64/perl5/5.42/x86_64-linux/Config.pm digest 1\n",
		"X86_64;/usr/lib64/libperl.so.5.42.2;libperl.so.5.42;;libc.so.6;x86_64\n")
	writePackage(t, root, "dev-perl/Current-1", "0",
		"obj /usr/lib64/perl5/vendor_perl/5.42/Current.pm digest 1\n", "")
	writePackage(t, root, "dev-perl/Old-1", "0",
		"obj /usr/lib64/perl5/vendor_perl/5.40/Old.pm digest 1\n", "")
	writePackage(t, root, "app-misc/CurrentLink-1", "0",
		"obj /usr/bin/current digest 1\n",
		"X86_64;/usr/bin/current;;;libperl.so.5.42,libc.so.6;x86_64\n")
	writePackage(t, root, "app-misc/OldLink-1", "0",
		"obj /usr/bin/old digest 1\n",
		"X86_64;/usr/bin/old;;;libperl.so.5.40,libc.so.6;x86_64\n")
	writePackage(t, root, "app-misc/Unrelated-1", "0",
		"obj /usr/bin/unrelated digest 1\n",
		"X86_64;/usr/bin/unrelated;;;libc.so.6;x86_64\n")
	return root
}

func writePackage(t *testing.T, root, cpv, slot, contents, needed string) {
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
