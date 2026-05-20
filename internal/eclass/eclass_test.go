package eclass

import (
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/ebuild"
)

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func makeEclassDir(t *testing.T, base string) string {
	t.Helper()
	dir := filepath.Join(base, "eclass")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir eclass dir: %v", err)
	}
	return dir
}

func TestLoadSimpleEclass(t *testing.T) {
	repoDir := t.TempDir()
	eclassDir := makeEclassDir(t, repoDir)

	content := `# Copyright 2024 Gentoo Authors
EXPORT_FUNCTIONS="flag-o-matic_src_compile flag-o-matic_src_install"

flag-o-matic_src_compile() {
    emake
}

flag-o-matic_src_install() {
    emake DESTDIR="${D}" install
}
`
	writeTemp(t, eclassDir, "flag-o-matic.eclass", content)

	info, err := LoadEclassByName("flag-o-matic", repoDir)
	if err != nil {
		t.Fatalf("LoadEclassByName: %v", err)
	}

	if info.Name != "flag-o-matic" {
		t.Errorf("Name: got %q, want flag-o-matic", info.Name)
	}

	if v := info.Variables["EXPORT_FUNCTIONS"]; v != `"flag-o-matic_src_compile flag-o-matic_src_install"` {
		t.Errorf("EXPORT_FUNCTIONS: got %q", v)
	}

	if _, ok := info.Functions["flag-o-matic_src_compile"]; !ok {
		t.Error("missing flag-o-matic_src_compile function")
	}
	if _, ok := info.Functions["flag-o-matic_src_install"]; !ok {
		t.Error("missing flag-o-matic_src_install function")
	}
}

func TestParseExportFunctions(t *testing.T) {
	names := parseExportFunctions(`"src_compile src_install pkg_setup"`)
	if len(names) != 3 {
		t.Errorf("expected 3 names, got %d: %v", len(names), names)
	}
	expected := []string{"src_compile", "src_install", "pkg_setup"}
	for i, want := range expected {
		if names[i] != want {
			t.Errorf("export[%d]: got %q, want %q", i, names[i], want)
		}
	}
}

func TestParseExportFunctionsEmpty(t *testing.T) {
	names := parseExportFunctions("")
	if len(names) != 0 {
		t.Errorf("expected 0 names, got %d", len(names))
	}

	names = parseExportFunctions(`""`)
	if len(names) != 0 {
		t.Errorf("expected 0 names for empty string, got %d", len(names))
	}
}

func TestExpandInheritSingleEclass(t *testing.T) {
	repoDir := t.TempDir()
	eclassDir := makeEclassDir(t, repoDir)

	content := `# Copyright 2024 Gentoo Authors
EXPORT_FUNCTIONS="toolchain-funcs_src_compile toolchain-funcs_src_install"

MYFLAGS="-O2"

toolchain-funcs_src_compile() {
    emake
}

toolchain-funcs_src_install() {
    emake DESTDIR="${D}" install
}
`
	writeTemp(t, eclassDir, "toolchain-funcs.eclass", content)

	variables, functions, err := ExpandInherit([]string{"toolchain-funcs"}, repoDir)
	if err != nil {
		t.Fatalf("ExpandInherit: %v", err)
	}

	if v := variables["MYFLAGS"]; v != `"-O2"` {
		t.Errorf("MYFLAGS: got %q, want %q", v, `"-O2"`)
	}
	if _, ok := functions["toolchain-funcs_src_compile"]; !ok {
		t.Error("missing toolchain-funcs_src_compile in expanded functions")
	}
	if _, ok := functions["toolchain-funcs_src_install"]; !ok {
		t.Error("missing toolchain-funcs_src_install in expanded functions")
	}
}

func TestExpandInheritMultipleEclasses(t *testing.T) {
	repoDir := t.TempDir()
	eclassDir := makeEclassDir(t, repoDir)

	writeTemp(t, eclassDir, "foo.eclass", `
EXPORT_FUNCTIONS="foo_src_configure"

FOO_VAR="foo"

foo_src_configure() {
    econf
}
`)
	writeTemp(t, eclassDir, "bar.eclass", `
EXPORT_FUNCTIONS="bar_src_compile"

BAR_VAR="bar"

bar_src_compile() {
    emake
}
`)

	variables, functions, err := ExpandInherit([]string{"foo", "bar"}, repoDir)
	if err != nil {
		t.Fatalf("ExpandInherit: %v", err)
	}

	if v := variables["FOO_VAR"]; v != `"foo"` {
		t.Errorf("FOO_VAR: got %q, want %q", v, `"foo"`)
	}
	if v := variables["BAR_VAR"]; v != `"bar"` {
		t.Errorf("BAR_VAR: got %q, want %q", v, `"bar"`)
	}
	if _, ok := functions["foo_src_configure"]; !ok {
		t.Error("missing foo_src_configure")
	}
	if _, ok := functions["bar_src_compile"]; !ok {
		t.Error("missing bar_src_compile")
	}
}

func TestExpandInheritNestedEclasses(t *testing.T) {
	repoDir := t.TempDir()
	eclassDir := makeEclassDir(t, repoDir)

	writeTemp(t, eclassDir, "base.eclass", `
EXPORT_FUNCTIONS="base_src_unpack"

BASE_VAR="base-value"

base_src_unpack() {
    default
}
`)
	writeTemp(t, eclassDir, "derived.eclass", `
EXPORT_FUNCTIONS="derived_src_compile"
inherit base

DERIVED_VAR="derived-value"

derived_src_compile() {
    emake
}
`)

	variables, functions, err := ExpandInherit([]string{"derived"}, repoDir)
	if err != nil {
		t.Fatalf("ExpandInherit: %v", err)
	}

	if v := variables["BASE_VAR"]; v != `"base-value"` {
		t.Errorf("BASE_VAR: got %q", v)
	}
	if v := variables["DERIVED_VAR"]; v != `"derived-value"` {
		t.Errorf("DERIVED_VAR: got %q", v)
	}
	if _, ok := functions["base_src_unpack"]; !ok {
		t.Error("missing base_src_unpack from base eclass")
	}
	if _, ok := functions["derived_src_compile"]; !ok {
		t.Error("missing derived_src_compile from derived eclass")
	}
}

func TestCircularInheritDetection(t *testing.T) {
	repoDir := t.TempDir()
	eclassDir := makeEclassDir(t, repoDir)

	writeTemp(t, eclassDir, "a.eclass", `
EXPORT_FUNCTIONS="a_src_unpack"
inherit b

a_src_unpack() {
    default
}
`)
	writeTemp(t, eclassDir, "b.eclass", `
EXPORT_FUNCTIONS="b_src_compile"
inherit a

b_src_compile() {
    emake
}
`)

	_, _, err := ExpandInherit([]string{"a"}, repoDir)
	if err == nil {
		t.Error("expected error for circular inherit")
	}
	if err != nil && !strings.Contains(err.Error(), "circular") {
		t.Errorf("expected circular error, got: %v", err)
	}
}

func TestMissingEclass(t *testing.T) {
	repoDir := t.TempDir()
	makeEclassDir(t, repoDir)

	_, _, err := ExpandInherit([]string{"nonexistent"}, repoDir)
	if err == nil {
		t.Error("expected error for missing eclass")
	}
}

func TestEmptyEclass(t *testing.T) {
	repoDir := t.TempDir()
	eclassDir := makeEclassDir(t, repoDir)

	writeTemp(t, eclassDir, "empty.eclass", ``)

	info, err := LoadEclassByName("empty", repoDir)
	if err != nil {
		t.Fatalf("LoadEclassByName: %v", err)
	}

	if len(info.Variables) != 0 {
		t.Errorf("expected no variables, got %v", info.Variables)
	}
	if len(info.Functions) != 0 {
		t.Errorf("expected no functions, got %v", info.Functions)
	}

	variables, functions, err := ExpandInherit([]string{"empty"}, repoDir)
	if err != nil {
		t.Fatalf("ExpandInherit: %v", err)
	}
	if len(variables) != 0 {
		t.Errorf("expected no expanded variables, got %v", variables)
	}
	if len(functions) != 0 {
		t.Errorf("expected no expanded functions, got %v", functions)
	}
}

func TestVariableOverrideOrder(t *testing.T) {
	repoDir := t.TempDir()
	eclassDir := makeEclassDir(t, repoDir)

	writeTemp(t, eclassDir, "base.eclass", `
MYVAR="base"
inherit_level="base"
`)
	writeTemp(t, eclassDir, "override.eclass", `
inherit base
MYVAR="override"
`)

	variables, _, err := ExpandInherit([]string{"override"}, repoDir)
	if err != nil {
		t.Fatalf("ExpandInherit: %v", err)
	}

	if v := variables["MYVAR"]; v != `"override"` {
		t.Errorf("MYVAR: got %q, want %q (later eclass should override)", v, `"override"`)
	}
}

func TestEclassWithEAPI(t *testing.T) {
	repoDir := t.TempDir()
	eclassDir := makeEclassDir(t, repoDir)

	writeTemp(t, eclassDir, "eapi-gated.eclass", `
# @SUPPORTED_EAPIS: 7 8
EAPI=8

EXPORT_FUNCTIONS="eapi-gated_src_install"

eapi-gated_src_install() {
    default
}
`)

	info, err := LoadEclassByName("eapi-gated", repoDir)
	if err != nil {
		t.Fatalf("LoadEclassByName: %v", err)
	}

	if info.EAPI != "8" {
		t.Errorf("EAPI: got %q, want 8", info.EAPI)
	}
}

func TestEclassWithNestedFunctions(t *testing.T) {
	repoDir := t.TempDir()
	eclassDir := makeEclassDir(t, repoDir)

	content := `EXPORT_FUNCTIONS="myeclass_src_compile"

helper_function() {
    echo "helper"
}

myeclass_src_compile() {
    if use someflag; then
        helper_function
    fi
    emake
}
`
	writeTemp(t, eclassDir, "nested.eclass", content)

	info, err := LoadEclassByName("nested", repoDir)
	if err != nil {
		t.Fatalf("LoadEclassByName: %v", err)
	}

	if _, ok := info.Functions["helper_function"]; !ok {
		t.Error("missing helper_function")
	}
	if _, ok := info.Functions["myeclass_src_compile"]; !ok {
		t.Error("missing myeclass_src_compile")
	}
}

func TestEclassCommentOnly(t *testing.T) {
	repoDir := t.TempDir()
	eclassDir := makeEclassDir(t, repoDir)

	writeTemp(t, eclassDir, "comments.eclass", `# just a comment
# another comment
`)

	info, err := LoadEclassByName("comments", repoDir)
	if err != nil {
		t.Fatalf("LoadEclassByName: %v", err)
	}

	if len(info.Variables) != 0 {
		t.Errorf("expected no variables, got %v", info.Variables)
	}
}

func TestAdversarialDeepNestedFunctions(t *testing.T) {
	repoDir := t.TempDir()
	eclassDir := makeEclassDir(t, repoDir)

	depth := 100
	var b strings.Builder
	b.WriteString("EXPORT_FUNCTIONS=\"deep\"\n")
	b.WriteString("deep() {\n")
	for i := 0; i < depth; i++ {
		b.WriteString("if true; then\n")
	}
	b.WriteString("echo deep\n")
	for i := 0; i < depth; i++ {
		b.WriteString("fi\n")
	}
	b.WriteString("}\n")

	writeTemp(t, eclassDir, "deep.eclass", b.String())

	info, err := LoadEclassByName("deep", repoDir)
	if err != nil {
		t.Fatalf("LoadEclassByName: %v", err)
	}

	if _, ok := info.Functions["deep"]; !ok {
		t.Error("missing deep function")
	}
}

func TestAdversarialVeryLongLine(t *testing.T) {
	repoDir := t.TempDir()
	eclassDir := makeEclassDir(t, repoDir)

	longValue := strings.Repeat("x", 100000)
	content := "MYLONGVAR=\"" + longValue + "\"\n"
	writeTemp(t, eclassDir, "long.eclass", content)

	info, err := LoadEclassByName("long", repoDir)
	if err != nil {
		t.Fatalf("LoadEclassByName: %v", err)
	}

	if v := info.Variables["MYLONGVAR"]; len(v) < 100000 {
		t.Errorf("MYLONGVAR truncated: got %d chars", len(v))
	}
}

func TestAdversarialBinaryGarbage(t *testing.T) {
	repoDir := t.TempDir()
	eclassDir := makeEclassDir(t, repoDir)

	garbage := make([]byte, 4096)
	rng := rand.New(rand.NewSource(42))
	for i := range garbage {
		garbage[i] = byte(rng.Intn(256))
		if garbage[i] == 0 {
			garbage[i] = 0x41
		}
	}

	writeTemp(t, eclassDir, "garbage.eclass", string(garbage))

	info, err := LoadEclassByName("garbage", repoDir)
	_ = info
	_ = err
}

func TestMutationByteFlip(t *testing.T) {
	repoDir := t.TempDir()
	eclassDir := makeEclassDir(t, repoDir)

	valid := `EXPORT_FUNCTIONS="myclass_src_compile"

myclass_src_compile() {
    emake
}
`
	for i := 0; i < len(valid); i++ {
		mutated := []byte(valid)
		mutated[i] = mutated[i] ^ 0xFF

		writeTemp(t, eclassDir, "mutated.eclass", string(mutated))

		info, err := LoadEclassByName("mutated", repoDir)
		_ = info
		_ = err
	}
}

func TestEclassFunctionsNotExportedIfNotListed(t *testing.T) {
	repoDir := t.TempDir()
	eclassDir := makeEclassDir(t, repoDir)

	content := `EXPORT_FUNCTIONS="myeclass_src_compile"

internal_helper() {
    echo "do not export me"
}

myeclass_src_compile() {
    internal_helper
    emake
}
`
	writeTemp(t, eclassDir, "selective.eclass", content)

	_, functions, err := ExpandInherit([]string{"selective"}, repoDir)
	if err != nil {
		t.Fatalf("ExpandInherit: %v", err)
	}

	if _, ok := functions["internal_helper"]; ok {
		t.Error("internal_helper should not be exported")
	}
	if _, ok := functions["myeclass_src_compile"]; !ok {
		t.Error("myeclass_src_compile should be exported")
	}
}

func TestEbuildEclassIntegration(t *testing.T) {
	repoDir := t.TempDir()
	eclassDir := makeEclassDir(t, repoDir)

	writeTemp(t, eclassDir, "myeclass.eclass", `
EXPORT_FUNCTIONS="myeclass_src_compile myeclass_src_install"

MYVAR="eclass-value"

myeclass_src_compile() {
    emake
}

myeclass_src_install() {
    emake DESTDIR="${D}" install
}
`)

	ebuildContent := `EAPI=8
inherit myeclass

DESCRIPTION="test package"
`
	ebuildPath := filepath.Join(t.TempDir(), "test-1.0.ebuild")
	if err := os.WriteFile(ebuildPath, []byte(ebuildContent), 0644); err != nil {
		t.Fatalf("write ebuild: %v", err)
	}

	eb, err := ebuild.ParseEbuild(ebuildPath)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	if len(eb.Inherit) != 1 || eb.Inherit[0] != "myeclass" {
		t.Fatalf("expected inherit myeclass, got %v", eb.Inherit)
	}

	variables, functions, err := ExpandInherit(eb.Inherit, repoDir)
	if err != nil {
		t.Fatalf("ExpandInherit: %v", err)
	}

	if v := variables["MYVAR"]; v != `"eclass-value"` {
		t.Errorf("MYVAR: got %q", v)
	}

	if len(functions) < 2 {
		t.Fatalf("expected at least 2 exported functions, got %d", len(functions))
	}

	functionNames := make([]string, 0, len(functions))
	for name := range functions {
		functionNames = append(functionNames, name)
	}
	sort.Strings(functionNames)
	if functionNames[0] != "myeclass_src_compile" || functionNames[1] != "myeclass_src_install" {
		t.Errorf("unexpected function names: %v", functionNames)
	}
}

func TestAdversarialManyContinuations(t *testing.T) {
	repoDir := t.TempDir()
	eclassDir := makeEclassDir(t, repoDir)

	var b strings.Builder
	b.WriteString("EXPORT_FUNCTIONS=\"src_compile\"\n")
	b.WriteString("DESCRIPTION=\\\n")
	for i := 0; i < 1000; i++ {
		b.WriteString("  part")
		b.WriteString(strings.Repeat("x", 50))
		b.WriteString(" \\\n")
	}
	b.WriteString("  finalpart\n")
	b.WriteString("fn_src_compile() { emake; }\n")

	writeTemp(t, eclassDir, "wide.eclass", b.String())

	info, err := LoadEclassByName("wide", repoDir)
	if err != nil {
		t.Fatalf("LoadEclassByName: %v", err)
	}

	if v := info.Variables["DESCRIPTION"]; !strings.Contains(v, "finalpart") {
		t.Errorf("DESCRIPTION should contain finalpart: got %d chars", len(v))
	}
}

func TestSelfReferentialInherit(t *testing.T) {
	repoDir := t.TempDir()
	eclassDir := makeEclassDir(t, repoDir)

	writeTemp(t, eclassDir, "self.eclass", `
EXPORT_FUNCTIONS="self_src_compile"
inherit self

self_src_compile() {
    emake
}
`)

	_, _, err := ExpandInherit([]string{"self"}, repoDir)
	if err == nil {
		t.Error("expected error for self-referential inherit")
	}
}
