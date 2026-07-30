package perlcleaner

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func FuzzPerlModulePathsDeterministic(f *testing.F) {
	for _, seed := range []string{
		"obj /usr/lib64/perl5/5.42/Foo.pm digest 1\n",
		"sym /usr/lib/perl5/vendor_perl/5.40/Foo.pm -> target digest 1\n",
		"\x00\nobj\nobj ../../escape digest 1\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, contents string) {
		first := perlModulePaths(contents)
		second := perlModulePaths(contents)
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("non-deterministic parse: %v != %v", first, second)
		}
		for _, path := range first {
			if !isPerlModulePath(path) {
				t.Fatalf("accepted non-module path %q", path)
			}
		}
	})
}

func FuzzNeededRecordsFailClosed(f *testing.F) {
	for _, seed := range []string{
		"X86_64;/usr/bin/foo;;;libperl.so.5.42,libc.so.6;x86_64\n",
		"malformed\n",
		"\x00;;;;;\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data string) {
		path := filepath.Join(t.TempDir(), "NEEDED.ELF.2")
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		first, firstErr := neededRecords(path)
		second, secondErr := neededRecords(path)
		if (firstErr == nil) != (secondErr == nil) || !reflect.DeepEqual(first, second) {
			t.Fatalf("non-deterministic parse: %#v/%v != %#v/%v", first, firstErr, second, secondErr)
		}
	})
}
