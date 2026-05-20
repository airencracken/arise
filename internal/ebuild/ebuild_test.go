package ebuild

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func TestParseValidEbuild(t *testing.T) {
	content := `# Copyright 1999-2024 Gentoo Authors
# Distributed under the terms of the GNU General Public License v2

EAPI=8

PYTHON_COMPAT=( python3_{10..13} )
DISTUTILS_USE_PEP517=setuptools
inherit distutils-r1

DESCRIPTION="A Python package"
HOMEPAGE="https://example.com"
SRC_URI="https://example.com/${P}.tar.gz"

LICENSE="MIT"
SLOT="0"
KEYWORDS="~amd64"

DEPEND="
    >=dev-lang/python-3.10
    dev-libs/libfoo
"
RDEPEND="${DEPEND}"
`
	path := writeTemp(t, "test-1.0.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	if eb.EAPI != "8" {
		t.Errorf("EAPI: got %q, want %q", eb.EAPI, "8")
	}

	if len(eb.Inherit) != 1 || eb.Inherit[0] != "distutils-r1" {
		t.Errorf("Inherit: got %v, want [distutils-r1]", eb.Inherit)
	}

	if eb.FilePath != path {
		t.Errorf("FilePath: got %q, want %q", eb.FilePath, path)
	}

	if v := eb.Variables["DESCRIPTION"]; v != `"A Python package"` {
		t.Errorf("DESCRIPTION: got %q", v)
	}
	if v := eb.Variables["HOMEPAGE"]; v != `"https://example.com"` {
		t.Errorf("HOMEPAGE: got %q", v)
	}
	if v := eb.Variables["LICENSE"]; v != `"MIT"` {
		t.Errorf("LICENSE: got %q", v)
	}
	if v := eb.Variables["SLOT"]; v != `"0"` {
		t.Errorf("SLOT: got %q", v)
	}
	if v := eb.Variables["KEYWORDS"]; v != `"~amd64"` {
		t.Errorf("KEYWORDS: got %q", v)
	}
	if v := eb.Variables["RDEPEND"]; v != `"${DEPEND}"` {
		t.Errorf("RDEPEND: got %q", v)
	}
}

func TestMultilineVariables(t *testing.T) {
	content := `EAPI=8

SRC_URI="https://example.com/${P}.tar.gz \
    https://example.com/${PN}-patches.tar.bz2"

DEPEND="\
    >=dev-libs/foo-1.0 \
    dev-libs/bar \
"
`
	path := writeTemp(t, "test-1.0.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	vars := eb.Vars()
	src := vars["SRC_URI"]
	if !strings.Contains(src, "test-1.0.0.tar.gz") {
		t.Errorf("SRC_URI should contain ${P} substitution, got: %s", src)
	}
	if !strings.Contains(src, "test-patches") {
		t.Errorf("SRC_URI should contain ${PN} substitution, got: %s", src)
	}
}

func TestPhaseFunctionExtraction(t *testing.T) {
	content := `EAPI=8

DESCRIPTION="test"

src_unpack() {
    default
    echo "unpacking"
}

src_compile() {
    emake
}

src_install() {
    emake DESTDIR="${D}" install
}
`
	path := writeTemp(t, "test-1.0.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	if len(eb.RawPhases) != 3 {
		t.Errorf("expected 3 phase functions, got %d", len(eb.RawPhases))
	}

	unpack, ok := eb.RawPhases["src_unpack"]
	if !ok {
		t.Error("missing src_unpack")
	} else if !strings.Contains(unpack, "default") || !strings.Contains(unpack, "unpacking") {
		t.Errorf("src_unpack body: %s", unpack)
	}

	compile, ok := eb.RawPhases["src_compile"]
	if !ok {
		t.Error("missing src_compile")
	} else if !strings.Contains(compile, "emake") {
		t.Errorf("src_compile body: %s", compile)
	}

	install, ok := eb.RawPhases["src_install"]
	if !ok {
		t.Error("missing src_install")
	} else if !strings.Contains(install, "DESTDIR") {
		t.Errorf("src_install body: %s", install)
	}
}

func TestPhaseFunctionMultiLineHeader(t *testing.T) {
	content := `EAPI=8

DESCRIPTION="test"

src_unpack()
{
    default
}

src_compile()   {
    emake
}
`
	path := writeTemp(t, "test-1.0.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	if len(eb.RawPhases) != 2 {
		t.Errorf("expected 2 phase functions, got %d", len(eb.RawPhases))
	}
	if _, ok := eb.RawPhases["src_unpack"]; !ok {
		t.Error("missing src_unpack with { on next line")
	}
}

func TestVariableResolutionPN_PV_PVR_P(t *testing.T) {
	content := `EAPI=8
DESCRIPTION="${P} is ${PN} at version ${PV} revision ${PVR}"
SRC_URI="mirror://sourceforge/${PN}/${P}.tar.gz"
HOMEPAGE="https://gitlab.com/${PN}"
`
	path := writeTemp(t, "testpkg-2.5.1-r3.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	vars := eb.Vars()

	if desc := vars["DESCRIPTION"]; !strings.Contains(desc, "testpkg-2.5.1") {
		t.Errorf("DESCRIPTION missing P substitution: %s", desc)
	}
	if desc := vars["DESCRIPTION"]; !strings.Contains(desc, "testpkg") {
		t.Errorf("DESCRIPTION missing PN substitution: %s", desc)
	}
	if desc := vars["DESCRIPTION"]; !strings.Contains(desc, "2.5.1") {
		t.Errorf("DESCRIPTION missing PV substitution: %s", desc)
	}
	if desc := vars["DESCRIPTION"]; !strings.Contains(desc, "2.5.1-r3") {
		t.Errorf("DESCRIPTION missing PVR substitution: %s", desc)
	}

	if src := vars["SRC_URI"]; !strings.Contains(src, "testpkg-2.5.1.tar.gz") {
		t.Errorf("SRC_URI missing P substitution: %s", src)
	}
}

func TestVariableResolutionNoVersion(t *testing.T) {
	content := `EAPI=8
DESCRIPTION="${P} is the package"
`
	path := writeTemp(t, "nover.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	vars := eb.Vars()
	if desc := vars["DESCRIPTION"]; !strings.Contains(desc, "nover") {
		t.Errorf("DESCRIPTION for no-version package: got %s", desc)
	}
}

func TestVariableResolutionDollarOnly(t *testing.T) {
	content := `EAPI=8
DESCRIPTION="$PN is a package version $PV"
`
	path := writeTemp(t, "foo-3.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	vars := eb.Vars()
	desc := vars["DESCRIPTION"]
	if !strings.Contains(desc, "foo") {
		t.Errorf("DESCRIPTION missing bare $PN substitution: %s", desc)
	}
	if !strings.Contains(desc, "3.0") {
		t.Errorf("DESCRIPTION missing bare $PV substitution: %s", desc)
	}
}

func TestSourceURIList(t *testing.T) {
	content := `EAPI=8
SRC_URI="https://example.com/${P}.tar.gz https://example.com/${PN}-docs.tar.bz2"
`
	path := writeTemp(t, "foo-1.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	uris := eb.SourceURIList()
	if len(uris) != 2 {
		t.Fatalf("expected 2 URIs, got %d: %v", len(uris), uris)
	}
	if uris[0] != "https://example.com/foo-1.0.tar.gz" {
		t.Errorf("URI[0]: got %q", uris[0])
	}
	if uris[1] != "https://example.com/foo-docs.tar.bz2" {
		t.Errorf("URI[1]: got %q", uris[1])
	}
}

func TestSourceURIListArrowSyntax(t *testing.T) {
	content := `EAPI=8
SRC_URI="https://example.com/${P}.tar.gz -> ${P}-renamed.tar.gz"
`
	path := writeTemp(t, "foo-1.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	uris := eb.SourceURIList()
	if len(uris) != 1 {
		t.Fatalf("expected 1 URI, got %d: %v", len(uris), uris)
	}
	if strings.Contains(uris[0], "->") {
		t.Errorf("URI still contains arrow: %q", uris[0])
	}
	if !strings.Contains(uris[0], "foo-1.0.tar.gz") {
		t.Errorf("URI got: %q", uris[0])
	}
}

func TestSourceURIListConditionals(t *testing.T) {
	content := `EAPI=8
SRC_URI="https://example.com/${P}.tar.gz python_single_target_python3_10? ( https://example.com/py310.patch )"
`
	path := writeTemp(t, "foo-1.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	uris := eb.SourceURIList()
	if len(uris) != 2 {
		t.Errorf("expected 2 URIs after filtering, got %d: %v", len(uris), uris)
	}
}

func TestSourceURIListEmpty(t *testing.T) {
	content := `EAPI=8
DESCRIPTION="test"
`
	path := writeTemp(t, "foo-1.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	uris := eb.SourceURIList()
	if len(uris) != 0 {
		t.Errorf("expected 0 URIs, got %d", len(uris))
	}
}

func TestEmptyEbuild(t *testing.T) {
	path := writeTemp(t, "empty-1.0.ebuild", "")

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	if eb.EAPI != "" {
		t.Errorf("expected empty EAPI, got %q", eb.EAPI)
	}
	if len(eb.Inherit) != 0 {
		t.Errorf("expected empty Inherit, got %v", eb.Inherit)
	}
	if len(eb.Variables) != 0 {
		t.Errorf("expected empty Variables, got %v", eb.Variables)
	}
	if len(eb.RawPhases) != 0 {
		t.Errorf("expected empty RawPhases, got %v", eb.RawPhases)
	}
}

func TestCommentOnlyEbuild(t *testing.T) {
	content := `# This is a comment
# Another comment line
#
# More comments
`
	path := writeTemp(t, "comments-1.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	if len(eb.Variables) != 0 {
		t.Errorf("expected no variables, got %v", eb.Variables)
	}
}

func TestMissingEAPI(t *testing.T) {
	content := `DESCRIPTION="No EAPI set"
SRC_URI="https://example.com/${P}.tar.gz"
`
	path := writeTemp(t, "test-1.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	if eb.EAPI != "" {
		t.Errorf("expected empty EAPI, got %q", eb.EAPI)
	}
	if v := eb.Variables["DESCRIPTION"]; v != `"No EAPI set"` {
		t.Errorf("DESCRIPTION: got %q", v)
	}
}

func TestMissingFunctionClose(t *testing.T) {
	content := `EAPI=8

src_compile() {
    emake
    do_something

# file ends without closing src_compile
`
	path := writeTemp(t, "test-1.0.ebuild", content)

	_, err := ParseEbuild(path)
	if err == nil {
		t.Error("expected error for unclosed function, got nil")
	}
}

func TestNullBytesInContent(t *testing.T) {
	content := "EAPI=8\nDESCRIPTION=\"test\x00with null\"\n"
	path := writeTemp(t, "test-1.0.ebuild", content)

	_, err := ParseEbuild(path)
	if err == nil {
		t.Error("expected error for null bytes")
	}
}

func TestNullBytesHeader(t *testing.T) {
	content := "\x00EAPI=8\n"
	path := writeTemp(t, "test-1.0.ebuild", content)

	_, err := ParseEbuild(path)
	if err == nil {
		t.Error("expected error for null bytes at start")
	}
}

func TestNonExistentFile(t *testing.T) {
	_, err := ParseEbuild("/nonexistent/path/test-1.0.ebuild")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestInheritMultipleEclasses(t *testing.T) {
	content := `EAPI=8
inherit toolchain-funcs flag-o-matic multilib
DESCRIPTION="test"
`
	path := writeTemp(t, "test-1.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	if len(eb.Inherit) != 3 {
		t.Fatalf("expected 3 inherits, got %d: %v", len(eb.Inherit), eb.Inherit)
	}
	expected := []string{"toolchain-funcs", "flag-o-matic", "multilib"}
	for i, want := range expected {
		if eb.Inherit[i] != want {
			t.Errorf("Inherit[%d]: got %q, want %q", i, eb.Inherit[i], want)
		}
	}
}

func TestInheritMultipleLines(t *testing.T) {
	content := `EAPI=8
inherit foo
inherit bar baz
DESCRIPTION="test"
`
	path := writeTemp(t, "test-1.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	if len(eb.Inherit) != 3 {
		t.Fatalf("expected 3 inherits across lines, got %d: %v", len(eb.Inherit), eb.Inherit)
	}
}

func TestConditionalVariableSkipped(t *testing.T) {
	content := `EAPI=8

DESCRIPTION="global desc"

if use foo; then
    SRC_URI="https://example.com/foo.tar.gz"
fi

HOMEPAGE="https://example.com"
`
	path := writeTemp(t, "test-1.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	if _, ok := eb.Variables["SRC_URI"]; ok {
		t.Error("SRC_URI inside conditional should not be extracted")
	}
	if v := eb.Variables["DESCRIPTION"]; v != `"global desc"` {
		t.Errorf("DESCRIPTION: got %q", v)
	}
	if v := eb.Variables["HOMEPAGE"]; v != `"https://example.com"` {
		t.Errorf("HOMEPAGE: got %q", v)
	}
}

func TestExportVariable(t *testing.T) {
	content := `EAPI=8
export FOO=bar
export BAZ
`
	path := writeTemp(t, "test-1.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	if v := eb.Variables["FOO"]; v != "bar" {
		t.Errorf("FOO: got %q, want %q", v, "bar")
	}
	if v, ok := eb.Variables["BAZ"]; !ok || v != "" {
		t.Errorf("BAZ: got %q (ok=%v), want empty string", v, ok)
	}
}

func TestLocalVariable(t *testing.T) {
	content := `EAPI=8
local MYVAR=somevalue
local PLAIN
`
	path := writeTemp(t, "test-1.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	if v := eb.Variables["MYVAR"]; v != "somevalue" {
		t.Errorf("MYVAR: got %q, want %q", v, "somevalue")
	}
	if v, ok := eb.Variables["PLAIN"]; !ok || v != "" {
		t.Errorf("PLAIN: got %q (ok=%v), want empty string", v, ok)
	}
}

func TestArrayVariableAssignment(t *testing.T) {
	content := `EAPI=8
PYTHON_COMPAT=( python3_{10..13} )
MY_ARRAY=( a b c )
`
	path := writeTemp(t, "test-1.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	if v := eb.Variables["PYTHON_COMPAT"]; v != `( python3_{10..13} )` {
		t.Errorf("PYTHON_COMPAT: got %q", v)
	}
	if v := eb.Variables["MY_ARRAY"]; v != `( a b c )` {
		t.Errorf("MY_ARRAY: got %q", v)
	}
}

func TestPhaseOrderDefault(t *testing.T) {
	content := `EAPI=8
DESCRIPTION="test"
`
	path := writeTemp(t, "test-1.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	if len(eb.RawPhaseOrder) != len(defaultPhaseOrder) {
		t.Errorf("PhaseOrder length: got %d, want %d", len(eb.RawPhaseOrder), len(defaultPhaseOrder))
	}
	for i, p := range defaultPhaseOrder {
		if eb.RawPhaseOrder[i] != p {
			t.Errorf("PhaseOrder[%d]: got %q, want %q", i, eb.RawPhaseOrder[i], p)
		}
	}
}

func TestAllKnownVariablesExtracted(t *testing.T) {
	content := `EAPI=8
SRC_URI="mirror://gnu/${P}.tar.gz"
DEPEND=">=dev-libs/libfoo-2.0"
RDEPEND="dev-libs/libfoo"
BDEPEND="sys-devel/make"
IDEPEND="virtual/libc"
PDEPEND="app-misc/foo"
SLOT="0"
DESCRIPTION="A test ebuild"
HOMEPAGE="https://example.com"
LICENSE="GPL-2"
KEYWORDS="amd64 x86"
IUSE="X gtk"
REQUIRED_USE="^^ ( X gtk )"
RESTRICT="test"
PROPERTIES="live"
`
	path := writeTemp(t, "test-1.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	expectedVars := []string{
		"SRC_URI", "DEPEND", "RDEPEND", "BDEPEND", "IDEPEND", "PDEPEND",
		"SLOT", "DESCRIPTION", "HOMEPAGE", "LICENSE", "KEYWORDS", "IUSE",
		"REQUIRED_USE", "RESTRICT", "PROPERTIES",
	}
	for _, key := range expectedVars {
		if _, ok := eb.Variables[key]; !ok {
			t.Errorf("missing variable %q in parsed output", key)
		}
	}
}

func TestAdversarialHugeEbuild(t *testing.T) {
	var b strings.Builder
	b.WriteString("EAPI=8\n")
	for i := 0; i < 10000; i++ {
		b.WriteString("MYVAR_")
		b.WriteString(strings.Repeat("x", 50))
		fmt.Fprintf(&b, "%d", i)
		b.WriteString("=value_")
		b.WriteString(strings.Repeat("y", 50))
		b.WriteString("\n")
	}
	path := writeTemp(t, "huge-1.0.ebuild", b.String())

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild on huge file: %v", err)
	}
	if len(eb.Variables) < 10000 {
		t.Errorf("expected at least 10000 variables, got %d", len(eb.Variables))
	}
}

func TestAdversarialDeeplyNestedBraces(t *testing.T) {
	depth := 2000
	open := strings.Repeat("{", depth)
	closer := strings.Repeat("}", depth)

	content := "EAPI=8\nsrc_test() " + open + "\necho test\n" + closer + "\n"
	path := writeTemp(t, "nested-1.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild on deeply nested braces: %v", err)
	}
	if _, ok := eb.RawPhases["src_test"]; !ok {
		t.Error("expected src_test phase to be extracted")
	}
}

func TestAdversarialUnbalancedBraces(t *testing.T) {
	content := `EAPI=8
src_compile() {
    if true; then
        echo "{"
        echo "}"
    fi
# missing closing } for function
`
	path := writeTemp(t, "unbalanced-1.0.ebuild", content)

	_, err := ParseEbuild(path)
	if err == nil {
		t.Error("expected error for unbalanced braces")
	}
}

func TestAdversarialEmptyFunction(t *testing.T) {
	content := `EAPI=8
src_unpack() { }
src_compile() { :; }
`
	path := writeTemp(t, "emptyfunc-1.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild on empty functions: %v", err)
	}
	if len(eb.RawPhases) != 2 {
		t.Errorf("expected 2 phase functions, got %d", len(eb.RawPhases))
	}
}

func TestAdversarialVeryLongLine(t *testing.T) {
	longValue := strings.Repeat("x", 500000)
	content := "EAPI=8\nDESCRIPTION=\"" + longValue + "\"\n"
	path := writeTemp(t, "longline-1.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild on very long line: %v", err)
	}
	if v := eb.Variables["DESCRIPTION"]; len(v) < 500000 {
		t.Errorf("DESCRIPTION truncated: got %d chars", len(v))
	}
}

func TestAdversarialBinaryGarbageContent(t *testing.T) {
	content := make([]byte, 4096)
	for i := range content {
		content[i] = byte(i % 256)
	}
	for i := range content {
		if content[i] == 0 {
			content[i] = 0x41
		}
	}
	content[0] = '#'
	content[1] = ' '
	path := writeTemp(t, "binary-1.0.ebuild", string(content))

	eb, err := ParseEbuild(path)
	_ = eb
	_ = err
}

func TestAdversarialManyContinuations(t *testing.T) {
	var b strings.Builder
	b.WriteString("EAPI=8\n")
	b.WriteString("DESCRIPTION=\\\n")
	for i := 0; i < 5000; i++ {
		b.WriteString("  part")
		b.WriteString(strings.Repeat("x", 100))
		b.WriteString(" \\\n")
	}
	b.WriteString("  finalpart\n")
	path := writeTemp(t, "wide-1.0.ebuild", b.String())

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild on many continuation lines: %v", err)
	}
	if v := eb.Variables["DESCRIPTION"]; !strings.Contains(v, "finalpart") {
		t.Errorf("DESCRIPTION should contain finalpart: got %d chars", len(v))
	}
}

func TestMutationByteFlip(t *testing.T) {
	valid := `EAPI=8
DESCRIPTION="A valid ebuild"
SRC_URI="https://example.com/${P}.tar.gz"
SLOT="0"
KEYWORDS="~amd64"
LICENSE="MIT"
`
	for i := 0; i < len(valid); i++ {
		mutated := []byte(valid)
		mutated[i] = mutated[i] ^ 0xFF

		path := writeTemp(t, "mutated-1.0.ebuild", string(mutated))

		_, err := ParseEbuild(path)
		_ = err
	}
}

func TestMutationRandomSingleByte(t *testing.T) {
	valid := `EAPI=8
DESCRIPTION="A valid ebuild"
SRC_URI="https://example.com/${P}.tar.gz"
LICENSE="MIT"
`
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 100; i++ {
		mutated := []byte(valid)
		pos := rng.Intn(len(mutated))
		mutated[pos] = byte(rng.Intn(256))

		path := writeTemp(t, "mutated-1.0.ebuild", string(mutated))

		_, err := ParseEbuild(path)
		_ = err
	}
}

func TestMutationInsertRandomBytes(t *testing.T) {
	valid := `EAPI=8
DESCRIPTION="A valid ebuild"
SRC_URI="https://example.com/${P}.tar.gz"
`
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 50; i++ {
		mutated := []byte(valid)
		pos := rng.Intn(len(mutated))
		insertLen := rng.Intn(20) + 1
		insert := make([]byte, insertLen)
		for j := range insert {
			insert[j] = byte(rng.Intn(256))
		}
		mutated = append(mutated[:pos], append(insert, mutated[pos:]...)...)

		path := writeTemp(t, "mutated-1.0.ebuild", string(mutated))

		_, err := ParseEbuild(path)
		_ = err
	}
}

func TestRealWorldInheritMultipleEclasses(t *testing.T) {
	content := `# Copyright 1999-2024 Gentoo Authors
# Distributed under the terms of the GNU General Public License v2

EAPI=8

PYTHON_COMPAT=( python3_{10..13} )
DISTUTILS_USE_PEP517=setuptools
inherit distutils-r1 pypi

DESCRIPTION="A Python package"
HOMEPAGE="https://pypi.org/project/example/"
SRC_URI="https://example.com/${P}.tar.gz"

LICENSE="MIT"
SLOT="0"
KEYWORDS="~amd64 ~x86"

BDEPEND="
    dev-python/setuptools[${PYTHON_USEDEP}]
"
RDEPEND="
    dev-python/requests[${PYTHON_USEDEP}]
"
DEPEND="${RDEPEND}"
`
	path := writeTemp(t, "example-2.0.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	if eb.EAPI != "8" {
		t.Errorf("EAPI: got %q, want 8", eb.EAPI)
	}
	if len(eb.Inherit) != 2 {
		t.Errorf("expected 2 inherits, got %d: %v", len(eb.Inherit), eb.Inherit)
	}
	if eb.Inherit[0] != "distutils-r1" {
		t.Errorf("Inherit[0]: got %q", eb.Inherit[0])
	}
	if eb.Inherit[1] != "pypi" {
		t.Errorf("Inherit[1]: got %q", eb.Inherit[1])
	}

	vars := eb.Vars()
	if v := vars["BDEPEND"]; !strings.Contains(v, "setuptools") {
		t.Errorf("BDEPEND: got %q", v)
	}
	if v := vars["RDEPEND"]; !strings.Contains(v, "requests") {
		t.Errorf("RDEPEND: got %q", v)
	}
}

func TestNestedFunctionInsideConditionalSkipped(t *testing.T) {
	content := `EAPI=8

if use extra; then
    src_compile() {
        emake extra
    }
fi

src_install() {
    default
}
`
	path := writeTemp(t, "test-1.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	if _, ok := eb.RawPhases["src_compile"]; ok {
		t.Error("src_compile inside conditional should not be captured")
	}
	if _, ok := eb.RawPhases["src_install"]; !ok {
		t.Error("src_install should be captured")
	}
}

func TestEapiLowerCaseExtracted(t *testing.T) {
	content := `eapi=7
DESCRIPTION="test"
`
	path := writeTemp(t, "test-1.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	if eb.EAPI != "7" {
		t.Errorf("EAPI: got %q, want 7", eb.EAPI)
	}
}

func TestFileEndingWithBackslash(t *testing.T) {
	content := `EAPI=8
DESCRIPTION=\
A test description ending with backslash at eof\
`
	path := writeTemp(t, "test-1.0.ebuild", content)

	eb, err := ParseEbuild(path)
	if err != nil {
		t.Fatalf("ParseEbuild: %v", err)
	}

	if v := eb.Variables["DESCRIPTION"]; !strings.Contains(v, "A test description") {
		t.Errorf("DESCRIPTION: got %q", v)
	}
}
