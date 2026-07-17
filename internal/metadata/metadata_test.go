package metadata

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/atom"
)

func validCacheEntry() []byte {
	return []byte(`DEFINED_PHASES=compile configure install postinst postrm preinst prepare pretend setup test unpack
DEPEND=dev-lang/python sys-devel/gcc
RDEPEND=dev-lang/python
BDEPEND=sys-devel/gcc
IDEPEND=
PDEPEND=
SRC_URI= https://example.com/foo-1.0.tar.gz
RESTRICT=test
PROPERTIES=
SLOT=3.11/3.11
KEYWORDS=amd64 arm64 x86
IUSE=bzip2 ncurses +ssl -test
LICENSE= GPL-2
REQUIRED_USE=
EAPI=8
DESCRIPTION=Gentoo package manager
HOMEPAGE=https://wiki.gentoo.org/
INHERITED=multilib toolchain-funcs
_md5_=abcdef1234567890abcdef1234567890
_eclasses_=
_mtime_=1700000000
`)
}

func TestParseCacheEntry_Basic(t *testing.T) {
	cpv := "sys-apps/portage-3.0.51"
	m, err := ParseCacheEntry(cpv, validCacheEntry())
	if err != nil {
		t.Fatalf("ParseCacheEntry unexpected error: %v", err)
	}

	if m.Category != "sys-apps" {
		t.Errorf("Category = %q, want %q", m.Category, "sys-apps")
	}
	if m.Package != "portage" {
		t.Errorf("Package = %q, want %q", m.Package, "portage")
	}
	if m.Version != "3.0.51" {
		t.Errorf("Version = %q, want %q", m.Version, "3.0.51")
	}

	if m.DEPEND != "dev-lang/python sys-devel/gcc" {
		t.Errorf("DEPEND = %q", m.DEPEND)
	}
	if m.RDEPEND != "dev-lang/python" {
		t.Errorf("RDEPEND = %q", m.RDEPEND)
	}
	if m.BDEPEND != "sys-devel/gcc" {
		t.Errorf("BDEPEND = %q", m.BDEPEND)
	}
	if m.IDEPEND != "" {
		t.Errorf("IDEPEND = %q, want empty", m.IDEPEND)
	}
	if m.PDEPEND != "" {
		t.Errorf("PDEPEND = %q, want empty", m.PDEPEND)
	}
	if m.SRC_URI != " https://example.com/foo-1.0.tar.gz" {
		t.Errorf("SRC_URI = %q", m.SRC_URI)
	}
	if m.RESTRICT != "test" {
		t.Errorf("RESTRICT = %q, want %q", m.RESTRICT, "test")
	}
	if m.SLOT != "3.11" {
		t.Errorf("SLOT = %q, want %q", m.SLOT, "3.11")
	}
	if m.Subslot != "3.11" {
		t.Errorf("Subslot = %q, want %q", m.Subslot, "3.11")
	}
	if m.KEYWORDS != "amd64 arm64 x86" {
		t.Errorf("KEYWORDS = %q", m.KEYWORDS)
	}
	if m.IUSE != "bzip2 ncurses +ssl -test" {
		t.Errorf("IUSE = %q", m.IUSE)
	}
	if m.LICENSE != " GPL-2" {
		t.Errorf("LICENSE = %q", m.LICENSE)
	}
	if m.EAPI != "8" {
		t.Errorf("EAPI = %q, want %q", m.EAPI, "8")
	}
	if m.DEFINED_PHASES != "compile configure install postinst postrm preinst prepare pretend setup test unpack" {
		t.Errorf("DEFINED_PHASES = %q", m.DEFINED_PHASES)
	}
	if m.DESCRIPTION != "Gentoo package manager" {
		t.Errorf("DESCRIPTION = %q", m.DESCRIPTION)
	}
	if m.HOMEPAGE != "https://wiki.gentoo.org/" {
		t.Errorf("HOMEPAGE = %q", m.HOMEPAGE)
	}
	if m.INHERITED != "multilib toolchain-funcs" {
		t.Errorf("INHERITED = %q", m.INHERITED)
	}
	if m._md5_ != "abcdef1234567890abcdef1234567890" {
		t.Errorf("_md5_ = %q", m._md5_)
	}
	if m._mtime_ != "1700000000" {
		t.Errorf("_mtime_ = %q", m._mtime_)
	}

	if len(m.Unknown) == 0 {
		t.Error("Unknown map should not be empty")
	}
	if m.Unknown["EAPI"] != "8" {
		t.Errorf("Unknown[EAPI] = %q", m.Unknown["EAPI"])
	}
}

func TestFingerprintDeterministicMapOrder(t *testing.T) {
	a := &PackageMetadata{Category: "app-editors", Package: "vim", Unknown: map[string]string{"B": "2", "A": "1"}}
	b := &PackageMetadata{Category: "app-editors", Package: "vim", Unknown: map[string]string{"A": "1", "B": "2"}}
	aDigest, err := Fingerprint(a)
	if err != nil {
		t.Fatal(err)
	}
	bDigest, err := Fingerprint(b)
	if err != nil {
		t.Fatal(err)
	}
	if aDigest != bDigest {
		t.Fatal("map insertion order changed metadata fingerprint")
	}
	b.Version = "9.1"
	bDigest, err = Fingerprint(b)
	if err != nil {
		t.Fatal(err)
	}
	if aDigest == bDigest {
		t.Fatal("metadata change did not change fingerprint")
	}
}

func TestParseCacheEntry_EmptyFields(t *testing.T) {
	data := []byte("DESCRIPTION=\nEAPI=0\n")
	m, err := ParseCacheEntry("cat/pkg-1", data)
	if err != nil {
		t.Fatalf("ParseCacheEntry error: %v", err)
	}
	if m.DESCRIPTION != "" {
		t.Errorf("DESCRIPTION = %q, want empty", m.DESCRIPTION)
	}
	if m.EAPI != "0" {
		t.Errorf("EAPI = %q, want 0", m.EAPI)
	}
}

func TestParseCacheEntry_SubslotParsing(t *testing.T) {
	tests := []struct {
		slotRaw string
		slot    string
		subslot string
	}{
		{"0", "0", ""},
		{"0/", "0", ""},
		{"0/1", "0", "1"},
		{"12/3.2", "12", "3.2"},
		{"0/0=", "0", "0="},
	}
	for _, tt := range tests {
		t.Run(tt.slotRaw, func(t *testing.T) {
			data := []byte("SLOT=" + tt.slotRaw + "\n")
			m, err := ParseCacheEntry("cat/pkg-1", data)
			if err != nil {
				t.Fatalf("ParseCacheEntry error: %v", err)
			}
			if m.SLOT != tt.slot {
				t.Errorf("SLOT = %q, want %q", m.SLOT, tt.slot)
			}
			if m.Subslot != tt.subslot {
				t.Errorf("Subslot = %q, want %q", m.Subslot, tt.subslot)
			}
		})
	}
}

func TestParseCacheEntry_CPVParsing(t *testing.T) {
	tests := []struct {
		cpv string
		cat string
		pkg string
		ver string
	}{
		{"sys-devel/gcc-12.2.0", "sys-devel", "gcc", "12.2.0"},
		{"dev-lang/python-3.12.0_alpha1", "dev-lang", "python", "3.12.0_alpha1"},
		{"virtual/rust-1.75.0", "virtual", "rust", "1.75.0"},
		{"x11-libs/gtk+-3.24.39", "x11-libs", "gtk+", "3.24.39"},
		{"cat/pkg-1.2.3-r4", "cat", "pkg", "1.2.3-r4"},
		{"www-client/firefox-l10n-152.0.6", "www-client", "firefox-l10n", "152.0.6"},
		{"dev-go/go-git-5.19.1", "dev-go", "go-git", "5.19.1"},
		{"media-fonts/font-adobe-100dpi-1.0.4", "media-fonts", "font-adobe-100dpi", "1.0.4"},
		{"sys-apps/intel-sa-00075-tools-1.0.0", "sys-apps", "intel-sa-00075-tools", "1.0.0"},
		{"cat/pkg", "cat", "pkg", ""},
	}

	for _, tt := range tests {
		t.Run(tt.cpv, func(t *testing.T) {
			data := []byte("EAPI=8\n")
			m, err := ParseCacheEntry(tt.cpv, data)
			if err != nil {
				t.Fatalf("ParseCacheEntry error: %v", err)
			}
			if m.Category != tt.cat {
				t.Errorf("Category = %q, want %q", m.Category, tt.cat)
			}
			if m.Package != tt.pkg {
				t.Errorf("Package = %q, want %q", m.Package, tt.pkg)
			}
			if m.Version != tt.ver {
				t.Errorf("Version = %q, want %q", m.Version, tt.ver)
			}
		})
	}
}

func TestParseCacheEntry_UnknownFields(t *testing.T) {
	data := []byte("EAPI=8\nCUSTOM_FIELD=hello\nANOTHER=world\n")
	m, err := ParseCacheEntry("cat/pkg-1", data)
	if err != nil {
		t.Fatalf("ParseCacheEntry error: %v", err)
	}
	if m.Unknown["CUSTOM_FIELD"] != "hello" {
		t.Errorf("Unknown[CUSTOM_FIELD] = %q", m.Unknown["CUSTOM_FIELD"])
	}
	if m.Unknown["ANOTHER"] != "world" {
		t.Errorf("Unknown[ANOTHER] = %q", m.Unknown["ANOTHER"])
	}
	if len(m.Unknown) != 3 {
		t.Errorf("Unknown length = %d, want 3", len(m.Unknown))
	}
}

func TestDependAtoms(t *testing.T) {
	data := []byte("DEPEND=dev-lang/python >=sys-devel/gcc-12.2.0 virtual/rust\n")
	m, err := ParseCacheEntry("cat/pkg-1", data)
	if err != nil {
		t.Fatalf("ParseCacheEntry error: %v", err)
	}

	atoms := m.DependAtoms()
	if len(atoms) != 3 {
		t.Fatalf("DependAtoms length = %d, want 3", len(atoms))
	}
	if atoms[0] != "dev-lang/python" {
		t.Errorf("atoms[0] = %q", atoms[0])
	}
	if atoms[1] != ">=sys-devel/gcc-12.2.0" {
		t.Errorf("atoms[1] = %q", atoms[1])
	}
	if atoms[2] != "virtual/rust" {
		t.Errorf("atoms[2] = %q", atoms[2])
	}
}

func TestDependAtoms_Empty(t *testing.T) {
	m := &PackageMetadata{}
	if m.DependAtoms() != nil {
		t.Error("DependAtoms on empty DEPEND should return nil")
	}
	if m.RDependAtoms() != nil {
		t.Error("RDependAtoms on empty RDEPEND should return nil")
	}
}

func TestDependAtoms_Multiline(t *testing.T) {
	data := []byte("DEPEND=a/b c/d e/f\n")
	m, err := ParseCacheEntry("cat/pkg-1", data)
	if err != nil {
		t.Fatalf("ParseCacheEntry error: %v", err)
	}
	atoms := m.DependAtoms()
	if len(atoms) != 3 {
		t.Fatalf("DependAtoms length = %d, want 3", len(atoms))
	}
	if atoms[0] != "a/b" {
		t.Errorf("atoms[0] = %q", atoms[0])
	}
	if atoms[1] != "c/d" {
		t.Errorf("atoms[1] = %q", atoms[1])
	}
	if atoms[2] != "e/f" {
		t.Errorf("atoms[2] = %q", atoms[2])
	}
}

func TestDependAtomsParsed(t *testing.T) {
	data := []byte("DEPEND=dev-lang/python >=sys-devel/gcc-12.2.0\n")
	m, err := ParseCacheEntry("cat/pkg-1", data)
	if err != nil {
		t.Fatalf("ParseCacheEntry error: %v", err)
	}

	atoms, errs := m.DependAtomsParsed()
	if len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(atoms) != 2 {
		t.Fatalf("DependAtomsParsed length = %d, want 2", len(atoms))
	}
	if atoms[0].Category != "dev-lang" || atoms[0].Package != "python" {
		t.Errorf("atoms[0] = %v", atoms[0])
	}
	if atoms[1].Category != "sys-devel" || atoms[1].Package != "gcc" {
		t.Errorf("atoms[1] = %v", atoms[1])
	}
}

func TestDependAtomsParsed_WithErrors(t *testing.T) {
	data := []byte("DEPEND=valid/pkg invalid!!!\n")
	m, err := ParseCacheEntry("cat/pkg-1", data)
	if err != nil {
		t.Fatalf("ParseCacheEntry error: %v", err)
	}

	atoms, errs := m.DependAtomsParsed()
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	if len(atoms) != 1 {
		t.Fatalf("expected 1 parsed atom, got %d", len(atoms))
	}
	if atoms[0].Category != "valid" || atoms[0].Package != "pkg" {
		t.Errorf("atoms[0] = %v, want valid/pkg", atoms[0])
	}
}

func TestAllDependHelpers(t *testing.T) {
	data := []byte("RDEPEND=a/b\nBDEPEND=c/d\nIDEPEND=e/f\nPDEPEND=g/h\n")
	m, err := ParseCacheEntry("cat/pkg-1", data)
	if err != nil {
		t.Fatalf("ParseCacheEntry error: %v", err)
	}

	for _, fn := range []struct {
		name string
		fn   func() []string
		want string
	}{
		{"RDependAtoms", m.RDependAtoms, "a/b"},
		{"BDependAtoms", m.BDependAtoms, "c/d"},
		{"IDependAtoms", m.IDependAtoms, "e/f"},
		{"PDependAtoms", m.PDependAtoms, "g/h"},
	} {
		atoms := fn.fn()
		if len(atoms) != 1 || atoms[0] != fn.want {
			t.Errorf("%s = %v, want [%q]", fn.name, atoms, fn.want)
		}
	}

	// Exercise parsed variants to ensure they don't panic
	for _, fn := range []func() ([]*atom.Atom, []error){
		m.RDependAtomsParsed,
		m.BDependAtomsParsed,
		m.IDependAtomsParsed,
		m.PDependAtomsParsed,
	} {
		_, _ = fn()
	}
}

func TestParseCacheEntry_RepeatedKeys(t *testing.T) {
	data := []byte("EAPI=8\nEAPI=7\nEAPI=6\n")
	m, err := ParseCacheEntry("cat/pkg-1", data)
	if err != nil {
		t.Fatalf("ParseCacheEntry error: %v", err)
	}
	if m.EAPI != "6" {
		t.Errorf("EAPI = %q, want 6 (last occurrence wins for named field)", m.EAPI)
	}
	if m.Unknown["EAPI"] != "6" {
		t.Errorf("Unknown[EAPI] = %q, want 6", m.Unknown["EAPI"])
	}
}

func TestParseCacheEntry_CRLF(t *testing.T) {
	data := []byte("EAPI=8\r\nDESCRIPTION=hello\r\n")
	m, err := ParseCacheEntry("cat/pkg-1", data)
	if err != nil {
		t.Fatalf("ParseCacheEntry error: %v", err)
	}
	if m.EAPI != "8" {
		t.Errorf("EAPI = %q", m.EAPI)
	}
	if m.DESCRIPTION != "hello" {
		t.Errorf("DESCRIPTION = %q", m.DESCRIPTION)
	}
}

func TestParseCacheEntry_Empty(t *testing.T) {
	_, err := ParseCacheEntry("cat/pkg-1", []byte{})
	if err != nil {
		t.Fatalf("ParseCacheEntry with empty data should not error: %v", err)
	}
}

func TestParseCacheEntry_EmptyCPV(t *testing.T) {
	_, err := ParseCacheEntry("", []byte("EAPI=8\n"))
	if err == nil {
		t.Error("expected error for empty cpv")
	}
}

func TestParseCacheEntry_InvalidCPV(t *testing.T) {
	inputs := []string{
		"gcc",
		"/pkg",
		"cat/",
		"cat/-1",
	}
	for _, cpv := range inputs {
		t.Run(cpv, func(t *testing.T) {
			_, err := ParseCacheEntry(cpv, []byte("EAPI=8\n"))
			if err == nil {
				t.Errorf("expected error for cpv %q", cpv)
			}
		})
	}
}

func TestParseCacheEntry_NoEquals(t *testing.T) {
	data := []byte("EAPI8\nJUST_A_LINE\n\n=value\n\nkey=\nv==2\n")
	m, err := ParseCacheEntry("cat/pkg-1", data)
	if err != nil {
		t.Fatalf("ParseCacheEntry error: %v", err)
	}
	if m.Unknown["EAPI8"] != "" {
		t.Error("line without = should be skipped")
	}
	if m.Unknown["key"] != "" {
		t.Errorf("key= should yield empty value, got %q", m.Unknown["key"])
	}
	// Lines without '=' should not appear in Unknown
	if _, ok := m.Unknown["JUST_A_LINE"]; ok {
		t.Error("JUST_A_LINE without = should not be in Unknown")
	}
	// First = splits key from value
	if m.Unknown["v"] != "=2" {
		t.Errorf("Unknown[v] = %q, want =2", m.Unknown["v"])
	}
}

func TestParseCacheEntry_NullBytes(t *testing.T) {
	data := []byte("EAPI=8\nDEPEND=a/b\x00c/d\n")
	m, err := ParseCacheEntry("cat/pkg-1", data)
	if err != nil {
		t.Fatalf("ParseCacheEntry error: %v", err)
	}
	if m.EAPI != "8" {
		t.Errorf("EAPI = %q", m.EAPI)
	}
}

func TestParseCacheEntry_BinaryData(t *testing.T) {
	data := make([]byte, 256)
	for i := range data {
		data[i] = byte(i)
	}
	m, err := ParseCacheEntry("cat/pkg-1", data)
	if err != nil {
		t.Fatalf("ParseCacheEntry should handle binary data gracefully: %v", err)
	}
	_ = m
}

func TestParseCacheEntry_OnlyNewlines(t *testing.T) {
	data := []byte("\n\n\n\n\n")
	m, err := ParseCacheEntry("cat/pkg-1", data)
	if err != nil {
		t.Fatalf("ParseCacheEntry error: %v", err)
	}
	if len(m.Unknown) != 0 {
		t.Errorf("Unknown should be empty, got %d entries", len(m.Unknown))
	}
}

func TestParseCacheEntry_TabsInKeys(t *testing.T) {
	data := []byte("EAPI\t=8\n")
	m, err := ParseCacheEntry("cat/pkg-1", data)
	if err != nil {
		t.Fatalf("ParseCacheEntry error: %v", err)
	}
	if m.EAPI != "" {
		t.Errorf("EAPI should not be set from tab-containing key, got %q", m.EAPI)
	}
	if v, ok := m.Unknown["EAPI\t"]; !ok || v != "8" {
		t.Errorf("Unknown[\"EAPI\\t\"] should be \"8\", got %q (ok=%v)", v, ok)
	}
}

func TestParseCacheEntry_ValuesWithEquals(t *testing.T) {
	data := []byte("DESCRIPTION=a=b=c\n")
	m, err := ParseCacheEntry("cat/pkg-1", data)
	if err != nil {
		t.Fatalf("ParseCacheEntry error: %v", err)
	}
	if m.DESCRIPTION != "a=b=c" {
		t.Errorf("DESCRIPTION = %q, want a=b=c", m.DESCRIPTION)
	}
}

func TestParseCacheEntry_TrailingNewline(t *testing.T) {
	data := []byte("EAPI=8")
	m, err := ParseCacheEntry("cat/pkg-1", data)
	if err != nil {
		t.Fatalf("ParseCacheEntry error: %v", err)
	}
	if m.EAPI != "8" {
		t.Errorf("EAPI = %q, want 8", m.EAPI)
	}
}

func TestParseCacheEntry_AtomFieldsCaseSensitivity(t *testing.T) {
	data := []byte("depend=foo/bar\nDEPEND=baz/qux\n")
	m, err := ParseCacheEntry("cat/pkg-1", data)
	if err != nil {
		t.Fatalf("ParseCacheEntry error: %v", err)
	}
	if m.DEPEND != "baz/qux" {
		t.Errorf("DEPEND = %q, want baz/qux", m.DEPEND)
	}
	if m.Unknown["depend"] != "foo/bar" {
		t.Errorf("Unknown[depend] = %q", m.Unknown["depend"])
	}
}

func TestParseCacheEntry_VeryLongLine(t *testing.T) {
	longValue := strings.Repeat("x", 100000)
	data := []byte("DESCRIPTION=" + longValue + "\n")
	m, err := ParseCacheEntry("cat/pkg-1", data)
	if err != nil {
		t.Fatalf("ParseCacheEntry error: %v", err)
	}
	if m.DESCRIPTION != longValue {
		t.Errorf("DESCRIPTION length = %d, want %d", len(m.DESCRIPTION), len(longValue))
	}
}

func TestParseCacheEntry_Mutation(t *testing.T) {
	valid := validCacheEntry()
	for i := 0; i < len(valid); i++ {
		mutated := make([]byte, len(valid))
		copy(mutated, valid)
		mutated[i] ^= 0xFF

		t.Run(fmt.Sprintf("byte_%d", i), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("ParseCacheEntry panicked on byte-flip at position %d: %v", i, r)
				}
			}()
			_, _ = ParseCacheEntry("cat/pkg-1", mutated)
		})
	}
}

func TestParseCacheEntry_Adversarial(t *testing.T) {
	inputs := []struct {
		name string
		data []byte
	}{
		{"nulls", bytes.Repeat([]byte{0}, 1000)},
		{"all_equals", bytes.Repeat([]byte("="), 1000)},
		{"mixed_binary", bytes.Repeat([]byte{0xFF, 0xFE, 0xFD}, 100)},
		{"high_codepoints", []byte(strings.Repeat("\U0010FFFF", 100))},
		{"line_start_equals", []byte("=value\n=value2\n")},
		{"empty_key", []byte("=value\nEAPI=8\n")},
	}

	for _, tt := range inputs {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("ParseCacheEntry panicked on %q: %v", tt.name, r)
				}
			}()
			_, _ = ParseCacheEntry("cat/pkg-1", tt.data)
		})
	}
}

func TestValidateBytes(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{"valid", []byte("EAPI=8\n"), false},
		{"empty", []byte{}, false},
		{"no_newline", []byte("EAPI=8"), true},
		{"with_null", []byte("EAPI=8\x00\n"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBytes(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateBytes() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestSplitAtomList(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"   ", nil},
		{"a/b", []string{"a/b"}},
		{"a/b c/d", []string{"a/b", "c/d"}},
		{"  a/b   c/d  ", []string{"a/b", "c/d"}},
		{"a/b\nc/d\ne/f", []string{"a/b", "c/d", "e/f"}},
		{"\t\ta/b\tc/d\t\t", []string{"a/b", "c/d"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitAtomList(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitAtomList(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseCacheEntry_WhitespaceOnlyData(t *testing.T) {
	data := []byte("   \n  \n\t\n")
	m, err := ParseCacheEntry("cat/pkg-1", data)
	if err != nil {
		t.Fatalf("ParseCacheEntry error: %v", err)
	}
	if len(m.Unknown) != 0 {
		t.Errorf("Unknown should be empty, got %d entries", len(m.Unknown))
	}
}

func TestPackageMetadata_UnknownIncludesAll(t *testing.T) {
	m, err := ParseCacheEntry("cat/pkg-1", validCacheEntry())
	if err != nil {
		t.Fatalf("ParseCacheEntry error: %v", err)
	}

	for _, key := range []string{
		"DEPEND", "RDEPEND", "BDEPEND", "IDEPEND", "PDEPEND",
		"SRC_URI", "RESTRICT", "PROPERTIES", "SLOT", "KEYWORDS",
		"IUSE", "LICENSE", "REQUIRED_USE", "EAPI", "DEFINED_PHASES",
		"DESCRIPTION", "HOMEPAGE", "INHERITED", "_md5_", "_mtime_",
	} {
		if _, ok := m.Unknown[key]; !ok {
			t.Errorf("Unknown missing key %q", key)
		}
	}
}

func TestParseCacheEntryPreservesFutureEAPIAndFields(t *testing.T) {
	data := []byte("EAPI=9999\nSLOT=0/9999\nDEPEND=dev-libs/libfuture\nFUTURE_DEPEND=sys-libs/new-abi\nFUTURE_MODE=parallel\n")
	m, err := ParseCacheEntry("dev-util/future-tool-1.0", data)
	if err != nil {
		t.Fatal(err)
	}
	if m.EAPI != "9999" || m.SLOT != "0" || m.Subslot != "9999" || m.DEPEND != "dev-libs/libfuture" {
		t.Fatalf("known metadata was not preserved: %+v", m)
	}
	for key, want := range map[string]string{"FUTURE_DEPEND": "sys-libs/new-abi", "FUTURE_MODE": "parallel"} {
		if got := m.Unknown[key]; got != want {
			t.Fatalf("future field %s = %q, want %q", key, got, want)
		}
	}
}
