package pythoncleaner

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNativeModuleFromPathAcceptsOnlyImportableExtensions(t *testing.T) {
	tests := []struct {
		path   string
		target string
		module string
		ok     bool
	}{
		{"/usr/lib64/python3.14/site-packages/Crypto/Cipher/_AES.cpython-314-x86_64-linux-gnu.so", "python3_14", "Crypto.Cipher._AES", true},
		{"/usr/lib/python3.14/site-packages/pkg/__init__.abi3.so", "python3_14", "pkg", true},
		{"/usr/lib/python3.14/site-packages/_speedups.so", "python3_14", "_speedups", true},
		{"/usr/lib/python3.14/site-packages/pkg/data-file.so", "", "", false},
		{"/usr/lib/python3.14/site-packages/pkg/module.py", "", "", false},
		{"usr/lib/python3.14/site-packages/pkg/mod.so", "", "", false},
		{"/tmp/python3.14/site-packages/pkg/mod.so", "", "", false},
	}
	for _, test := range tests {
		target, module, ok := nativeModuleFromPath(test.path)
		if target != test.target || module != test.module || ok != test.ok {
			t.Errorf("%q = %q %q %v", test.path, target, module, ok)
		}
	}
}

func TestBuildRuntimeProbesUsesVDBOwnershipPolicyAndRepairedSet(t *testing.T) {
	vdbRoot := filepath.Join(t.TempDir(), "vdb")
	writePythonPackage(t, vdbRoot, "dev-python/Foo-1", "0", "", "",
		strings.Join([]string{
			"obj /usr/lib64/python3.14/site-packages/foo/_native.cpython-314-x86_64-linux-gnu.so hash 1",
			"obj /usr/lib64/python3.13/site-packages/foo/_old.so hash 1",
			"obj /usr/lib64/python3.14/site-packages/foo/pure.py hash 1",
			"obj /usr/lib64/python3.14/site-packages/foo/_native.cpython-314-x86_64-linux-gnu.so hash 1",
		}, "\n"), "")
	writePythonPackage(t, vdbRoot, "dev-python/Other-1", "0", "", "",
		"obj /usr/lib64/python3.14/site-packages/other/_native.so hash 1\n", "")
	root := t.TempDir()
	probes, err := BuildRuntimeProbes(vdbRoot, root, []string{"python3_14"}, []string{"dev-python/Foo:0"})
	if err != nil {
		t.Fatal(err)
	}
	want := []RuntimeProbe{{
		CPV: "dev-python/Foo-1", Target: "python3_14",
		Interpreter: filepath.Join(root, "usr/bin/python3.14"),
		Module:      "foo._native",
		Evidence:    "/usr/lib64/python3.14/site-packages/foo/_native.cpython-314-x86_64-linux-gnu.so",
	}}
	if !reflect.DeepEqual(probes, want) {
		t.Fatalf("probes = %#v, want %#v", probes, want)
	}
	if _, err := BuildRuntimeProbes(vdbRoot, root, []string{"python3_14"}, []string{"../../escape"}); err == nil {
		t.Fatal("invalid repaired target accepted")
	}
}

func TestRunRuntimeProbesReportsFailuresAndBoundsOutput(t *testing.T) {
	root := t.TempDir()
	ok := filepath.Join(root, "ok")
	fail := filepath.Join(root, "fail")
	if err := os.WriteFile(ok, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fail, []byte("#!/bin/sh\ni=0\nwhile [ \"$i\" -lt 5000 ]; do printf x; i=$((i+1)); done\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	probes := []RuntimeProbe{
		{Interpreter: ok, Module: "sys"},
		{Interpreter: fail, Module: "broken"},
	}
	failures := RunRuntimeProbes(context.Background(), probes, time.Second)
	if len(failures) != 1 || failures[0].Probe.Module != "broken" ||
		len(failures[0].Detail) > 4099 || !strings.HasSuffix(failures[0].Detail, "...") {
		t.Fatalf("failures = %#v", failures)
	}
}

func TestRunRuntimeProbesEnforcesTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "slow")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	failures := RunRuntimeProbes(context.Background(), []RuntimeProbe{{
		Interpreter: path, Module: "slow",
	}}, 20*time.Millisecond)
	if len(failures) != 1 || failures[0].Detail != "probe timed out" {
		t.Fatalf("failures = %#v", failures)
	}
}
