package pythoncleaner

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/vdb"
)

func TestCheckBuildsPolicyInterpreterConsumerAndRemovalInventory(t *testing.T) {
	vdbRoot := pythonFixture(t)
	report, err := Check(vdbRoot, t.TempDir(), Policy{
		Targets: []string{"python3_14"}, SingleTarget: "python3_14",
		Preference: []string{"python3_13", "python3_14", "python3_12"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Policy.Preference, []string{"python3_13", "python3_14", "python3_12"}) {
		t.Fatalf("preference order lost: %v", report.Policy.Preference)
	}
	if len(report.Interpreters) != 4 || len(report.Missing) != 0 {
		t.Fatalf("interpreters=%#v missing=%v", report.Interpreters, report.Missing)
	}
	if len(report.Consumers) != 1 {
		t.Fatalf("consumers = %#v", report.Consumers)
	}
	consumer := report.Consumers[0]
	if consumer.CPV != "dev-python/Old-1" || consumer.Atom != "dev-python/Old:0" {
		t.Fatalf("consumer = %#v", consumer)
	}
	kinds := make([]string, 0, len(consumer.Reasons))
	for _, reason := range consumer.Reasons {
		kinds = append(kinds, reason.Kind)
	}
	if !reflect.DeepEqual(kinds, []string{"stale-libpython", "stale-python-path", "targets-outside-policy"}) {
		t.Fatalf("reasons = %#v", consumer.Reasons)
	}
	removals := removalMap(report.Removals)
	if removals["python3_13"].Safe ||
		!reflect.DeepEqual(removals["python3_13"].Blockers, []string{"dev-python/Old", "python-exec preference"}) {
		t.Fatalf("3.13 removal = %#v", removals["python3_13"])
	}
	if removals["python3_12"].Safe || !reflect.DeepEqual(removals["python3_12"].Blockers, []string{"python-exec preference"}) {
		t.Fatalf("3.12 removal = %#v", removals["python3_12"])
	}
	if !removals["python3_10"].Safe {
		t.Fatalf("unreferenced 3.10 removal blocked: %#v", removals["python3_10"])
	}
}

func TestCheckReportsMissingPolicyInterpreter(t *testing.T) {
	report, err := Check(pythonFixture(t), t.TempDir(), Policy{Targets: []string{"python3_15"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Missing, []string{"python3_15"}) {
		t.Fatalf("missing = %v", report.Missing)
	}
}

func TestCheckRejectsEmptyPolicyAndMalformedLinkage(t *testing.T) {
	vdbRoot := pythonFixture(t)
	if _, err := Check(vdbRoot, t.TempDir(), Policy{}); err == nil {
		t.Fatal("empty policy accepted")
	}
	path := filepath.Join(vdbRoot, "dev-python", "Old-1", "NEEDED.ELF.2")
	if err := os.WriteFile(path, []byte("malformed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(vdbRoot, t.TempDir(), Policy{Targets: []string{"python3_14"}}); err == nil ||
		!strings.Contains(err.Error(), "malformed") {
		t.Fatalf("malformed linkage error = %v", err)
	}
}

func TestCheckFindsRootConfinedStaleShebang(t *testing.T) {
	vdbRoot := pythonFixture(t)
	root := t.TempDir()
	script := filepath.Join(root, "usr/bin/tool")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/usr/bin/env -S python3.13 -I\nprint('ok')\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePythonPackage(t, vdbRoot, "app-misc/Tool-1", "0", "", "",
		"obj /usr/bin/tool digest 1\n", "")
	report, err := Check(vdbRoot, root, Policy{Targets: []string{"python3_14"}})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, consumer := range report.Consumers {
		if consumer.CPV != "app-misc/Tool-1" {
			continue
		}
		for _, reason := range consumer.Reasons {
			found = found || reason.Kind == "stale-shebang" && reason.Target == "python3_13"
		}
	}
	if !found {
		t.Fatalf("stale shebang missing: %#v", report.Consumers)
	}
}

func TestShebangScannerDoesNotFollowSymlinksOrEscapeRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("#!/usr/bin/python3.13\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "usr/bin/tool")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	got, err := staleShebangs(root, "obj /usr/bin/tool digest 1\nobj /../../escape digest 1\n", []string{"python3_14"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("unsafe shebang evidence = %#v", got)
	}
}

func TestUnavailableShebangProbeIsEvidenceAndBlocksRemoval(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read mode-000 fixture")
	}
	vdbRoot := pythonFixture(t)
	root := t.TempDir()
	path := filepath.Join(root, "usr/sbin/tool")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/usr/bin/python3.13\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	writePythonPackage(t, vdbRoot, "app-misc/Unreadable-1", "0", "", "",
		"obj /usr/sbin/tool digest 1\n", "")
	report, err := Check(vdbRoot, root, Policy{Targets: []string{"python3_14"}})
	if err != nil {
		t.Fatal(err)
	}
	var evidence bool
	for _, consumer := range report.Consumers {
		for _, reason := range consumer.Reasons {
			evidence = evidence || reason.Kind == "shebang-probe-unavailable"
		}
	}
	if !evidence {
		t.Fatalf("unavailable probe missing: %#v", report.Consumers)
	}
	for _, removal := range report.Removals {
		if removal.Safe {
			t.Fatalf("removal remained safe with unknown shebang: %#v", removal)
		}
	}
}

func TestKnownELFIsNeverTreatedAsUnavailableShebangProbe(t *testing.T) {
	vdbRoot := pythonFixture(t)
	root := t.TempDir()
	path := filepath.Join(root, "usr/bin/native")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("ELF"), 0o000); err != nil {
		t.Fatal(err)
	}
	writePythonPackage(t, vdbRoot, "app-misc/Native-1", "0", "", "",
		"obj /usr/bin/native digest 1\n",
		"X86_64;/usr/bin/native;;;libc.so.6;x86_64\n")
	report, err := Check(vdbRoot, root, Policy{Targets: []string{"python3_14"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, consumer := range report.Consumers {
		if consumer.CPV == "app-misc/Native-1" {
			t.Fatalf("native ELF became shebang consumer: %#v", consumer)
		}
	}
}

func TestCheckFindsOnlyUnownedFilesInObsoleteSitePackages(t *testing.T) {
	vdbRoot := pythonFixture(t)
	root := t.TempDir()
	owned := filepath.Join(root, "usr/lib64/python3.13/site-packages/owned.py")
	orphan := filepath.Join(root, "usr/lib64/python3.13/site-packages/orphan.py")
	current := filepath.Join(root, "usr/lib64/python3.14/site-packages/current-orphan.py")
	for _, path := range []string{owned, orphan, current} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writePythonPackage(t, vdbRoot, "dev-python/Owner-1", "0", "", "",
		"obj /usr/lib64/python3.13/site-packages/owned.py digest 1\n", "")
	report, err := Check(vdbRoot, root, Policy{Targets: []string{"python3_14"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Orphans, []Orphan{{Path: orphan, Target: "python3_13"}}) {
		t.Fatalf("orphans = %#v", report.Orphans)
	}
}

func TestOrphanReportIsBoundedAndCountsOmissions(t *testing.T) {
	root := t.TempDir()
	tree := filepath.Join(root, "usr/lib/python3.13/site-packages")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.py", "b.py", "c.py"} {
		if err := os.WriteFile(filepath.Join(tree, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, omitted, err := findOrphans(root, nil, []string{"python3_14"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || omitted != 1 {
		t.Fatalf("orphans=%#v omitted=%d", got, omitted)
	}
}

func TestOrphanScanIgnoresGeneratedBytecode(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "usr/lib/python3.13/site-packages/__pycache__")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "generated.cpython-313.pyc"), []byte("bytecode"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, omitted, err := findOrphans(root, nil, []string{"python3_14"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || omitted != 0 {
		t.Fatalf("generated bytecode reported: %#v omitted=%d", got, omitted)
	}
}

func TestParsePreferencePreservesOrderDisablesAndComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "python-exec.conf")
	data := "# preference\npython3.13\npython3.14\n-python3.12\npython3.13\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ParsePreference(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"python3_13", "python3_14"}) {
		t.Fatalf("preference = %v", got)
	}
}

func TestTargetNormalizationAndEvidenceParsing(t *testing.T) {
	if got := normalizedTargets([]string{"python3.14", "python_targets_python3_13", "-python3_12", "bad"}); !reflect.DeepEqual(got, []string{
		"python3_12", "python3_13", "python3_14",
	}) {
		t.Fatalf("targets = %v", got)
	}
	contents := "obj /usr/lib64/python3.13/site-packages/a.py x 1\nsym /usr/lib/python3.13/b.so -> x\n"
	if got := pythonVersionsInContents(contents); !reflect.DeepEqual(got, []string{"3.13"}) {
		t.Fatalf("versions = %v", got)
	}
}

func TestPackageDependencyCPsParsesAllDependencyClasses(t *testing.T) {
	pkg := vdb.Package{
		Depend:  "build? ( dev-python/build )",
		RDepend: "dev-python/runtime >=dev-python/versioned-2",
		BDepend: "|| ( dev-python/host-a dev-python/host-b )",
		IDepend: "dev-python/install",
		PDepend: "dev-python/post",
	}
	got, err := packageDependencyCPs(pkg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"dev-python/build", "dev-python/host-a", "dev-python/host-b",
		"dev-python/install", "dev-python/post", "dev-python/runtime",
		"dev-python/versioned",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dependencies = %v, want %v", got, want)
	}
	pkg.RDepend = "broken? ("
	if _, err := packageDependencyCPs(pkg); err == nil {
		t.Fatal("malformed installed dependency accepted")
	}
}

func removalMap(removals []Removal) map[string]Removal {
	result := map[string]Removal{}
	for _, removal := range removals {
		result[removal.Interpreter.Target] = removal
	}
	return result
}

func pythonFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "vdb")
	for _, version := range []string{"3.10.20", "3.12.9", "3.13.13", "3.14.6"} {
		parts := strings.Split(version, ".")
		slot := parts[0] + "." + parts[1]
		writePythonPackage(t, root, "dev-lang/python-"+version, slot, "", "", "", "")
	}
	writePythonPackage(t, root, "dev-python/Current-1", "0",
		"python_targets_python3_14", "python_targets_python3_13 python_targets_python3_14",
		"obj /usr/lib64/python3.14/site-packages/current.py digest 1\n", "")
	writePythonPackage(t, root, "dev-python/Old-1", "0",
		"python_targets_python3_13", "python_targets_python3_13 python_targets_python3_14",
		"obj /usr/lib64/python3.13/site-packages/old.py digest 1\n",
		"X86_64;/usr/lib64/python3.13/site-packages/old.so;;;libpython3.13.so.1.0,libc.so.6;x86_64\n")
	return root
}

func writePythonPackage(t *testing.T, root, cpv, slot, use, iuse, contents, needed string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(cpv))
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"CONTENTS": contents, "EAPI": "8\n", "SLOT": slot + "\n", "repository": "gentoo\n",
		"USE": use + "\n", "IUSE": iuse + "\n",
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
