package pythoncleaner

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func FuzzPythonMetadataParsersAreDeterministic(f *testing.F) {
	for _, seed := range []string{
		"obj /usr/lib64/python3.14/site-packages/a.py digest 1\n",
		"X86_64;/usr/bin/a;;;libpython3.13.so.1.0,libc.so.6;x86_64\n",
		"malformed\x00data\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data string) {
		first := pythonVersionsInContents(data)
		second := pythonVersionsInContents(data)
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("path parser is non-deterministic: %v != %v", first, second)
		}
		path := filepath.Join(t.TempDir(), "NEEDED.ELF.2")
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		firstLinks, firstErr := linkedPythonVersions(path)
		secondLinks, secondErr := linkedPythonVersions(path)
		if (firstErr == nil) != (secondErr == nil) || !reflect.DeepEqual(firstLinks, secondLinks) {
			t.Fatalf("link parser is non-deterministic: %v/%v != %v/%v", firstLinks, firstErr, secondLinks, secondErr)
		}
	})
}
