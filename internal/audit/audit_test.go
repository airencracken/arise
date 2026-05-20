package audit

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestFindPythonVersionsInPath(t *testing.T) {
	tests := []struct {
		path string
		want []string
	}{
		{"/usr/lib/python3.11/site-packages/foo.py", []string{"3.11"}},
		{"/usr/lib64/python3.10/site-packages/bar/__init__.py", []string{"3.10"}},
		{"/usr/lib/python3.9/distutils/core.py", []string{"3.9"}},
		{"/usr/lib64/python3.12/site-packages/baz.so", []string{"3.12"}},
		{"/usr/bin/python3", nil},
		{"/etc/portage/make.conf", nil},
		{"", nil},
		{"/usr/share/doc/python-3.11/README", nil},
		{"/usr/lib64/", nil},
		{"/usr/lib/python3.13/site-packages/pkg/module.py", []string{"3.13"}},
		{"python3.11", nil},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := FindPythonVersionsInPath(tt.path)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FindPythonVersionsInPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestFindPythonVersionsInPath_MultipleVersions(t *testing.T) {
	path := "/usr/lib/python3.10/site-packages/old + /usr/lib/python3.11/site-packages/new"
	got := FindPythonVersionsInPath(path)
	want := []string{"3.10", "3.11"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FindPythonVersionsInPath(...) = %v, want %v", got, want)
	}
}

func TestFindPythonVersionsInPath_Deduplicates(t *testing.T) {
	path := "/usr/lib/python3.11/site-packages/a //usr/lib/python3.11/site-packages/b"
	got := FindPythonVersionsInPath(path)
	want := []string{"3.11"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FindPythonVersionsInPath(...) = %v, want %v", got, want)
	}
}

func TestFindPythonVersionsInPath_Lib64(t *testing.T) {
	got := FindPythonVersionsInPath("/usr/lib64/python3.11/site-packages/pkg")
	if len(got) != 1 || got[0] != "3.11" {
		t.Errorf("FindPythonVersionsInPath(...) = %v, want [3.11]", got)
	}
}

func makeVDBFlat(dir string, packages map[string]string) error {
	for pkg, contents := range packages {
		pkgDir := filepath.Join(dir, pkg)
		if err := os.MkdirAll(pkgDir, 0755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(pkgDir, "CONTENTS"), []byte(contents), 0644); err != nil {
			return err
		}
	}
	return nil
}

func makeVDBNested(dir string, packages map[string]map[string]string) error {
	for category, pkgs := range packages {
		catDir := filepath.Join(dir, category)
		for pkg, contents := range pkgs {
			pkgDir := filepath.Join(catDir, pkg)
			if err := os.MkdirAll(pkgDir, 0755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(pkgDir, "CONTENTS"), []byte(contents), 0644); err != nil {
				return err
			}
		}
	}
	return nil
}

func TestAuditPython_WithPythonReferences(t *testing.T) {
	dir := t.TempDir()

	packages := map[string]string{
		"python-3.9.18": `obj /usr/bin/python3.9 a1b2c3 1234567890
obj /usr/lib/python3.9/site-packages/setuptools/__init__.py a1b2c3 1234567890
obj /usr/lib64/python3.9/lib-dynload/math.so a1b2c3 1234567890`,
		"python-3.11.5": `obj /usr/bin/python3.11 a1b2c3 1234567890
obj /usr/lib/python3.11/site-packages/pip/__init__.py a1b2c3 1234567890`,
		"vim-9.0": `obj /usr/bin/vim a1b2c3 1234567890
obj /usr/share/vim/vim90/doc/help.txt a1b2c3 1234567890`,
		"numpy-1.24.0": `obj /usr/lib/python3.11/site-packages/numpy/__init__.py a1b2c3 1234567890
obj /usr/lib64/python3.11/site-packages/numpy/core/_multiarray_umath.so a1b2c3 1234567890`,
		"requests-2.28.0": `obj /usr/lib/python3.9/site-packages/requests/__init__.py a1b2c3 1234567890
obj /usr/lib/python3.10/site-packages/requests/__init__.py a1b2c3 1234567890
obj /usr/lib/python3.11/site-packages/requests/__init__.py a1b2c3 1234567890`,
	}
	if err := makeVDBFlat(dir, packages); err != nil {
		t.Fatalf("makeVDBFlat: %v", err)
	}

	results, err := AuditPython(dir)
	if err != nil {
		t.Fatalf("AuditPython: %v", err)
	}

	resultMap := make(map[string]VdbAuditResult)
	for _, r := range results {
		key := filepath.Base(r.PackagePath)
		resultMap[key] = r
	}

	if _, ok := resultMap["vim-9.0"]; ok {
		t.Error("vim should not appear in results (no python references)")
	}

	if r, ok := resultMap["python-3.9.18"]; ok {
		if len(r.AffectedContents) < 2 {
			t.Errorf("python-3.9.18: expected at least 2 affected contents, got %d", len(r.AffectedContents))
		}
	} else {
		t.Error("python-3.9.18 not found in results")
	}

	if r, ok := resultMap["requests-2.28.0"]; ok {
		if len(r.AffectedContents) != 3 {
			t.Errorf("requests: expected 3 affected contents, got %d", len(r.AffectedContents))
		}
	} else {
		t.Error("requests-2.28.0 not found in results")
	}

	for _, r := range results {
		if r.PackagePath == "" {
			t.Error("result has empty PackagePath")
		}
		if len(r.AffectedContents) == 0 {
			t.Error("result has empty AffectedContents")
		}
	}
}

func TestAuditPython_NoPythonReferences(t *testing.T) {
	dir := t.TempDir()

	packages := map[string]string{
		"gcc-13.2.0": `obj /usr/bin/gcc a1b2c3 1234567890
obj /usr/lib/gcc/x86_64-pc-linux-gnu/13/libgcc.a a1b2c3 1234567890`,
		"bash-5.1": `obj /bin/bash a1b2c3 1234567890
obj /usr/share/doc/bash-5.1/README a1b2c3 1234567890`,
	}
	if err := makeVDBFlat(dir, packages); err != nil {
		t.Fatalf("makeVDBFlat: %v", err)
	}

	results, err := AuditPython(dir)
	if err != nil {
		t.Fatalf("AuditPython: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d: %+v", len(results), results)
	}
}

func TestAuditPython_EmptyVDB(t *testing.T) {
	dir := t.TempDir()
	results, err := AuditPython(dir)
	if err != nil {
		t.Fatalf("AuditPython: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestAuditPython_MissingVDB(t *testing.T) {
	_, err := AuditPython("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Error("expected error for nonexistent path, got nil")
	}
}

func TestAuditPython_SortedResults(t *testing.T) {
	dir := t.TempDir()

	packages := map[string]string{
		"zzz-pkg-1.0": `obj /usr/lib/python3.9/site-packages/z.py a1b2c3 1234567890`,
		"aaa-pkg-1.0": `obj /usr/lib/python3.11/site-packages/a.py a1b2c3 1234567890`,
	}
	if err := makeVDBFlat(dir, packages); err != nil {
		t.Fatalf("makeVDBFlat: %v", err)
	}

	results, err := AuditPython(dir)
	if err != nil {
		t.Fatalf("AuditPython: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if !strings.Contains(results[0].PackagePath, "aaa-pkg") {
		t.Errorf("first result should be aaa-pkg, got %s", results[0].PackagePath)
	}
	if !strings.Contains(results[1].PackagePath, "zzz-pkg") {
		t.Errorf("second result should be zzz-pkg, got %s", results[1].PackagePath)
	}
}

func TestAuditPython_MultipleVersionsPerPackage(t *testing.T) {
	dir := t.TempDir()

	packages := map[string]string{
		"legacy-1.0": `obj /usr/lib/python3.9/site-packages/legacy.py a1b2c3 1234567890
obj /usr/lib/python3.10/site-packages/legacy.py a1b2c3 1234567890
obj /usr/lib64/python3.11/site-packages/legacy.py a1b2c3 1234567890`,
	}
	if err := makeVDBFlat(dir, packages); err != nil {
		t.Fatalf("makeVDBFlat: %v", err)
	}

	results, err := AuditPython(dir)
	if err != nil {
		t.Fatalf("AuditPython: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if len(r.AffectedContents) != 3 {
		t.Errorf("expected 3 affected contents, got %d", len(r.AffectedContents))
	}

	sort.Strings(r.OldVersions)
	if len(r.OldVersions) == 0 {
		t.Error("expected old versions, got none")
	}
	expectedVersions := []string{"3.10", "3.11", "3.9"}
	for _, ev := range expectedVersions {
		found := false
		for _, v := range r.OldVersions {
			if v == ev {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected version %s in old versions, got %v", ev, r.OldVersions)
		}
	}
}

func TestAuditPython_AdversarialInput(t *testing.T) {
	dir := t.TempDir()

	packages := map[string]string{
		"giant-1.0":  strings.Repeat("obj /usr/lib/python3.11/site-packages/x.py a1b2c3 0\n", 10000),
		"binary-pkg": "obj /usr/lib/python3.10/site-packages/bad.py a1b2c3 0\n",
	}
	if err := makeVDBFlat(dir, packages); err != nil {
		t.Fatalf("makeVDBFlat: %v", err)
	}

	results, err := AuditPython(dir)
	if err != nil {
		t.Fatalf("AuditPython should not error on adversarial input: %v", err)
	}
	if len(results) < 2 {
		t.Errorf("expected at least 2 results, got %d", len(results))
	}
	for _, r := range results {
		if len(r.AffectedContents) == 0 {
			t.Error("result has empty AffectedContents despite having python references")
		}
	}
}

func TestAuditPython_DirAndSymEntries(t *testing.T) {
	dir := t.TempDir()

	packages := map[string]string{
		"pkg-1.0": `dir /usr/lib/python3.11/site-packages/pkg
obj /usr/lib/python3.11/site-packages/pkg/__init__.py a1 0
sym /usr/lib/python3.11/site-packages/pkg/link.so -> /usr/lib64/python3.11/site-packages/pkg/real.so a2 0`,
	}
	if err := makeVDBFlat(dir, packages); err != nil {
		t.Fatalf("makeVDBFlat: %v", err)
	}

	results, err := AuditPython(dir)
	if err != nil {
		t.Fatalf("AuditPython: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(results[0].AffectedContents) != 3 {
		t.Errorf("expected 3 affected entries (dir, obj, sym), got %d: %v",
			len(results[0].AffectedContents), results[0].AffectedContents)
	}
}

func TestAuditPython_NestedVDB(t *testing.T) {
	dir := t.TempDir()

	packages := map[string]map[string]string{
		"dev-lang": {
			"python-3.9.18": `obj /usr/lib/python3.9/site-packages/setuptools/__init__.py a1b2c3 1234567890`,
			"python-3.11.5": `obj /usr/lib/python3.11/site-packages/pip/__init__.py a1b2c3 1234567890`,
		},
		"app-shells": {
			"bash-5.1": `obj /bin/bash a1b2c3 1234567890`,
		},
		"dev-python": {
			"requests-2.28.0": `obj /usr/lib/python3.9/site-packages/requests/__init__.py a1b2c3 1234567890
obj /usr/lib/python3.11/site-packages/requests/__init__.py a1b2c3 1234567890`,
		},
	}
	if err := makeVDBNested(dir, packages); err != nil {
		t.Fatalf("makeVDBNested: %v", err)
	}

	results, err := AuditPython(dir)
	if err != nil {
		t.Fatalf("AuditPython: %v", err)
	}

	if len(results) < 3 {
		t.Errorf("expected at least 3 results, got %d", len(results))
	}

	for _, r := range results {
		base := filepath.Base(r.PackagePath)
		if base == "bash-5.1" {
			t.Error("bash should not appear in python audit results")
		}
	}
}

func TestFindPythonVersionsInPath_Property(t *testing.T) {
	for _, input := range []string{
		"",
		"/usr/bin/python3",
		"/etc/portage/make.conf",
		"/usr/share/doc/python-3.11/README",
		"\x00\x00\x00",
		strings.Repeat("/usr/lib/python3.11/site-packages/x", 1000),
	} {
		result := FindPythonVersionsInPath(input)
		if result == nil {
			continue
		}
		for _, v := range result {
			if !strings.Contains(v, ".") {
				t.Errorf("FindPythonVersionsInPath returned version without dot: %q from input %q", v, input)
			}
		}
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"3.9", "3.11", -1},
		{"3.11", "3.11", 0},
		{"3.12", "3.11", 1},
		{"3.11", "3.9", 1},
		{"3.10", "3.11", -1},
		{"4.0", "3.11", 1},
		{"3", "3.0", 0},
		{"3.0", "3", 0},
		{"3.10.1", "3.10", 1},
		{"3.10", "3.10.1", -1},
		{"notaversion", "3.11", 0},
		{"3.11", "notaversion", 0},
		{"abc", "xyz", 0},
	}

	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			got := compareVersions(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestFindPerlVersionsInPath(t *testing.T) {
	tests := []struct {
		path string
		want []string
	}{
		{"/usr/lib/perl5/vendor_perl/5.36.0/File/Path.pm", []string{"5.36.0"}},
		{"/usr/lib/perl5/site_perl/5.38.2/x86_64-linux/auto/List/Util/Util.so", []string{"5.38.2"}},
		{"/usr/lib/perl5/5.40.0/strict.pm", []string{"5.40.0"}},
		{"/usr/lib64/perl5/vendor_perl/5.38.2/XML/Parser.pm", []string{"5.38.2"}},
		{"/usr/bin/perl", nil},
		{"/etc/perl/CPAN/Config.pm", nil},
		{"", nil},
		{"/usr/lib/perl5/5.38.0/unicore/lib/Perl/Alnum.pl", []string{"5.38.0"}},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := FindPerlVersionsInPath(tt.path)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FindPerlVersionsInPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestFindPerlVersionsInPath_MultipleVersions(t *testing.T) {
	path := "/usr/lib/perl5/vendor_perl/5.36.0/old.pm /usr/lib/perl5/vendor_perl/5.40.0/new.pm"
	got := FindPerlVersionsInPath(path)
	want := []string{"5.36.0", "5.40.0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FindPerlVersionsInPath(...) = %v, want %v", got, want)
	}
}

func TestAuditPerl_WithPerlReferences(t *testing.T) {
	dir := t.TempDir()

	packages := map[string]string{
		"perl-5.36.0": `obj /usr/bin/perl5.36.0 a1b2c3 1234567890
obj /usr/lib/perl5/vendor_perl/5.36.0/File/Path.pm a1b2c3 1234567890
obj /usr/lib64/perl5/vendor_perl/5.36.0/auto/List/Util/Util.so a1b2c3 1234567890`,
		"perl-XML-Parser-2.46": `obj /usr/lib/perl5/vendor_perl/5.36.0/XML/Parser.pm a1b2c3 1234567890
obj /usr/lib/perl5/vendor_perl/5.36.0/x86_64-linux/auto/XML/Parser/Expat/Expat.so a1b2c3 1234567890`,
		"vim-9.0": `obj /usr/bin/vim a1b2c3 1234567890
obj /usr/share/vim/vim90/doc/help.txt a1b2c3 1234567890`,
		"perl-IO-1.52": `obj /usr/lib/perl5/site_perl/5.38.2/IO.pm a1b2c3 1234567890`,
	}
	if err := makeVDBFlat(dir, packages); err != nil {
		t.Fatalf("makeVDBFlat: %v", err)
	}

	results, err := AuditPerl(dir)
	if err != nil {
		t.Fatalf("AuditPerl: %v", err)
	}

	resultMap := make(map[string]VdbAuditResult)
	for _, r := range results {
		key := filepath.Base(r.PackagePath)
		resultMap[key] = r
	}

	if _, ok := resultMap["vim-9.0"]; ok {
		t.Error("vim should not appear in perl audit results")
	}

	if r, ok := resultMap["perl-XML-Parser-2.46"]; ok {
		if len(r.AffectedContents) != 2 {
			t.Errorf("expected 2 affected contents, got %d", len(r.AffectedContents))
		}
	} else {
		t.Error("perl-XML-Parser-2.46 not found in results")
	}

	if r, ok := resultMap["perl-IO-1.52"]; ok {
		if len(r.AffectedContents) != 1 {
			t.Errorf("expected 1 affected content, got %d", len(r.AffectedContents))
		}
	} else {
		t.Error("perl-IO-1.52 not found in results")
	}

	for _, r := range results {
		if r.AuditType != "perl" {
			t.Errorf("expected audit type 'perl', got %q", r.AuditType)
		}
	}
}

func TestAuditPerl_NoPerlReferences(t *testing.T) {
	dir := t.TempDir()

	packages := map[string]string{
		"gcc-13.2.0": `obj /usr/bin/gcc a1b2c3 1234567890
obj /usr/lib/gcc/x86_64-pc-linux-gnu/13/libgcc.a a1b2c3 1234567890`,
	}
	if err := makeVDBFlat(dir, packages); err != nil {
		t.Fatalf("makeVDBFlat: %v", err)
	}

	results, err := AuditPerl(dir)
	if err != nil {
		t.Fatalf("AuditPerl: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestAuditPerl_EmptyVDB(t *testing.T) {
	dir := t.TempDir()
	results, err := AuditPerl(dir)
	if err != nil {
		t.Fatalf("AuditPerl: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestAuditPerl_NestedVDB(t *testing.T) {
	dir := t.TempDir()

	packages := map[string]map[string]string{
		"dev-perl": {
			"XML-Parser-2.46": `obj /usr/lib/perl5/vendor_perl/5.38.2/XML/Parser.pm a1b2c3 1234567890`,
		},
		"perl-core": {
			"File-Path-2.18": `obj /usr/lib/perl5/vendor_perl/5.38.0/File/Path.pm a1b2c3 1234567890`,
		},
	}
	if err := makeVDBNested(dir, packages); err != nil {
		t.Fatalf("makeVDBNested: %v", err)
	}

	results, err := AuditPerl(dir)
	if err != nil {
		t.Fatalf("AuditPerl: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestAuditPerl_Adversarial(t *testing.T) {
	dir := t.TempDir()

	packages := map[string]string{
		"giant-perl-pkg": strings.Repeat("obj /usr/lib/perl5/vendor_perl/5.38.2/x.pm a1 0\n", 10000),
		"corrupt-pkg":    "garbage line\n\x00\x00\x00\nobj /usr/lib/perl5/5.36.0/ok.pm a1 0",
	}
	if err := makeVDBFlat(dir, packages); err != nil {
		t.Fatalf("makeVDBFlat: %v", err)
	}

	results, err := AuditPerl(dir)
	if err != nil {
		t.Fatalf("AuditPerl should not error: %v", err)
	}
	if len(results) < 2 {
		t.Errorf("expected at least 2 results, got %d", len(results))
	}
}

func TestAuditPerl_SortedResults(t *testing.T) {
	dir := t.TempDir()

	packages := map[string]string{
		"zzz-perl-pkg-1.0": `obj /usr/lib/perl5/vendor_perl/5.36.0/z.pm a1 0`,
		"aaa-perl-pkg-1.0": `obj /usr/lib/perl5/vendor_perl/5.38.0/a.pm a1 0`,
	}
	if err := makeVDBFlat(dir, packages); err != nil {
		t.Fatalf("makeVDBFlat: %v", err)
	}

	results, err := AuditPerl(dir)
	if err != nil {
		t.Fatalf("AuditPerl: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if !strings.Contains(results[0].PackagePath, "aaa-perl-pkg") {
		t.Errorf("first result should be aaa-perl-pkg, got %s", results[0].PackagePath)
	}
}

func TestParsePerlRevision(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"5.038002", "5.38.2"},
		{"5.036000", "5.36.0"},
		{"5.040000", "5.40.0"},
		{"5.034001", "5.34.1"},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := parsePerlRevision(tt.raw)
			if err != nil {
				t.Fatalf("parsePerlRevision(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Errorf("parsePerlRevision(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestRebuild_EmptyPackages(t *testing.T) {
	t.Skip("rebuild now lives in internal/rebuild package")
}
