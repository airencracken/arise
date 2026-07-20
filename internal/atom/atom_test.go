package atom

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse_Basic(t *testing.T) {
	tests := []struct {
		input string
		want  *Atom
	}{
		{
			input: "sys-devel/gcc",
			want:  &Atom{Category: "sys-devel", Package: "gcc"},
		},
		{
			input: "net-im/signal-desktop-bin",
			want:  &Atom{Category: "net-im", Package: "signal-desktop-bin"},
		},
		{
			input: "=net-im/signal-desktop-bin-7.61.0",
			want:  &Atom{Op: OpEq, Category: "net-im", Package: "signal-desktop-bin", Version: &Version{Raw: "7.61.0", Numbers: []int{7, 61, 0}, Revision: -1}},
		},
		{
			input: "dev-go/go-git:5",
			want:  &Atom{Category: "dev-go", Package: "go-git", Slot: "5"},
		},
		{
			input: "sys-devel/gcc-12.2.0",
			want:  &Atom{Category: "sys-devel", Package: "gcc", Version: &Version{Raw: "12.2.0", Numbers: []int{12, 2, 0}, Revision: -1}},
		},
		{
			input: ">=sys-devel/gcc-12.2.0",
			want:  &Atom{Op: OpGtEq, Category: "sys-devel", Package: "gcc", Version: &Version{Raw: "12.2.0", Numbers: []int{12, 2, 0}, Revision: -1}},
		},
		{
			input: "=sys-devel/gcc-12.2.0",
			want:  &Atom{Op: OpEq, Category: "sys-devel", Package: "gcc", Version: &Version{Raw: "12.2.0", Numbers: []int{12, 2, 0}, Revision: -1}},
		},
		{
			input: "=sys-devel/gcc-12.2.0*",
			want:  &Atom{Op: OpEqGlob, Category: "sys-devel", Package: "gcc", Version: &Version{Raw: "12.2.0*", Numbers: []int{12, 2, 0}, Revision: -1}},
		},
		{
			input: "~sys-devel/gcc-12.2.0",
			want:  &Atom{Op: OpTilde, Category: "sys-devel", Package: "gcc", Version: &Version{Raw: "12.2.0", Numbers: []int{12, 2, 0}, Revision: -1}},
		},
		{
			input: "sys-devel/gcc:12",
			want:  &Atom{Category: "sys-devel", Package: "gcc", Slot: "12"},
		},
		{
			input: "sys-devel/gcc:12/12.2",
			want:  &Atom{Category: "sys-devel", Package: "gcc", Slot: "12", Subslot: "12.2"},
		},
		{
			input: "sys-devel/gcc:=",
			want:  &Atom{Category: "sys-devel", Package: "gcc", SlotOp: SlotOpEq},
		},
		{
			input: "sys-devel/gcc:*",
			want:  &Atom{Category: "sys-devel", Package: "gcc", SlotOp: SlotOpStar},
		},
		{
			input: "sys-devel/gcc::gentoo",
			want:  &Atom{Category: "sys-devel", Package: "gcc", Repo: "gentoo"},
		},
		{
			input: "sys-devel/gcc[fortran]",
			want:  &Atom{Category: "sys-devel", Package: "gcc", UseFlags: []UseFlag{{Name: "fortran", Enabled: true}}},
		},
		{
			input: "sys-devel/gcc[fortran,-doc]",
			want:  &Atom{Category: "sys-devel", Package: "gcc", UseFlags: []UseFlag{{Name: "fortran", Enabled: true}, {Name: "doc", Enabled: false}}},
		},
		{
			input: ">=sys-devel/gcc-12.2.0:12/12.2=[fortran]",
			want: &Atom{
				Op: OpGtEq, Category: "sys-devel", Package: "gcc",
				Version: &Version{Raw: "12.2.0", Numbers: []int{12, 2, 0}, Revision: -1},
				Slot:    "12", Subslot: "12.2",
				SlotOp:   SlotOpEq,
				UseFlags: []UseFlag{{Name: "fortran", Enabled: true}},
			},
		},
		{
			input: "dev-lang/python-3.11.5-r1",
			want:  &Atom{Category: "dev-lang", Package: "python", Version: &Version{Raw: "3.11.5-r1", Numbers: []int{3, 11, 5}, Revision: 1}},
		},
		{
			input: "dev-lang/python-3.12.0_alpha1",
			want: &Atom{
				Category: "dev-lang", Package: "python",
				Version: &Version{Raw: "3.12.0_alpha1", Numbers: []int{3, 12, 0}, Suffixes: []string{"_alpha", "1"}, Revision: -1},
			},
		},
		{
			input: "dev-lang/python-3.12.0_rc1",
			want: &Atom{
				Category: "dev-lang", Package: "python",
				Version: &Version{Raw: "3.12.0_rc1", Numbers: []int{3, 12, 0}, Suffixes: []string{"_rc", "1"}, Revision: -1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.input, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse(%q) =\n  got:  %+v\n  want: %+v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseEqualGlobAtom(t *testing.T) {
	a, err := Parse("=dev-python/bitarray-3*")
	if err != nil {
		t.Fatal(err)
	}
	if a.Op != OpEqGlob || a.Version == nil || a.Version.Raw != "3*" {
		t.Fatalf("parsed atom = %+v", a)
	}
	if got := a.String(); got != "=dev-python/bitarray-3*" {
		t.Fatalf("round trip = %q", got)
	}
}

func TestParsePackageNameWithNumericWordSuffix(t *testing.T) {
	a, err := Parse("media-fonts/font-adobe-100dpi")
	if err != nil {
		t.Fatal(err)
	}
	if a.Package != "font-adobe-100dpi" || a.Version != nil {
		t.Fatalf("numeric package-name suffix misparsed: %+v", a)
	}
}

func TestParse_VersionSuffixes(t *testing.T) {
	tests := []struct {
		input   string
		numbers []int
		suffix  []string
		rev     int
	}{
		{"1.0", []int{1, 0}, nil, -1},
		{"1.0_alpha", []int{1, 0}, []string{"_alpha"}, -1},
		{"1.0_alpha1", []int{1, 0}, []string{"_alpha", "1"}, -1},
		{"1.0_beta2", []int{1, 0}, []string{"_beta", "2"}, -1},
		{"1.0_pre3", []int{1, 0}, []string{"_pre", "3"}, -1},
		{"1.0_rc4", []int{1, 0}, []string{"_rc", "4"}, -1},
		{"1.0_p5", []int{1, 0}, []string{"_p", "5"}, -1},
		{"1.0-r1", []int{1, 0}, nil, 1},
		{"1.0_alpha1_p2", []int{1, 0}, []string{"_alpha", "1", "_p", "2"}, -1},
		{"1.0p", []int{1, 0}, nil, -1},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			v, err := parseVersionString(tt.input)
			if err != nil {
				t.Fatalf("parseVersionString(%q) error: %v", tt.input, err)
			}
			if !reflect.DeepEqual(v.Numbers, tt.numbers) {
				t.Errorf("numbers: got %v, want %v", v.Numbers, tt.numbers)
			}
			if !reflect.DeepEqual(v.Suffixes, tt.suffix) {
				t.Errorf("suffixes: got %v, want %v", v.Suffixes, tt.suffix)
			}
			if v.Revision != tt.rev {
				t.Errorf("revision: got %d, want %d", v.Revision, tt.rev)
			}
		})
	}
}

func TestComparePreservesTrailingNumericComponents(t *testing.T) {
	short, err := parseVersionString("1.0")
	if err != nil {
		t.Fatal(err)
	}
	long, err := parseVersionString("1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := short.Compare(long); got != -1 {
		t.Fatalf("1.0 Compare 1.0.0 = %d, want -1", got)
	}
	if got := long.Compare(short); got != 1 {
		t.Fatalf("1.0.0 Compare 1.0 = %d, want 1", got)
	}
}

func TestComparePreservesLeadingZeroComponentPrecision(t *testing.T) {
	tests := []struct {
		left, right string
		want        int
	}{
		{"1.01", "1.1", -1},
		{"1.001", "1.01", -1},
		{"1.010", "1.01", 0},
		{"1.1", "1.01", 1},
		{"1.0", "1.00", 0},
	}
	for _, tt := range tests {
		t.Run(tt.left+"_vs_"+tt.right, func(t *testing.T) {
			left, err := ParseVersion(tt.left)
			if err != nil {
				t.Fatal(err)
			}
			right, err := ParseVersion(tt.right)
			if err != nil {
				t.Fatal(err)
			}
			if got := left.Compare(right); got != tt.want {
				t.Fatalf("Compare(%q, %q) = %d, want %d", tt.left, tt.right, got, tt.want)
			}
		})
	}
}

func TestCompareNormalizesImplicitSuffixAndRevisionZero(t *testing.T) {
	for _, pair := range [][2]string{
		{"1.0", "1.0-r0"},
		{"1.0_alpha", "1.0_alpha0"},
		{"1.0_p", "1.0_p0"},
		{"1.0_alpha0", "1.0_alpha00"},
		{"1.0_alpha1", "1.0_alpha01"},
	} {
		left, err := ParseVersion(pair[0])
		if err != nil {
			t.Fatal(err)
		}
		right, err := ParseVersion(pair[1])
		if err != nil {
			t.Fatal(err)
		}
		if got := left.Compare(right); got != 0 {
			t.Errorf("Compare(%q, %q) = %d, want 0", pair[0], pair[1], got)
		}
	}
}

func TestParseConditionalUseDependenciesWithDefaults(t *testing.T) {
	parsed, err := Parse(">=sys-apps/dbus-1.5[abi_x86_32(-)?,abi_x86_64(+)?,!test=]")
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.String(); got != ">=sys-apps/dbus-1.5[abi_x86_32(-)?,abi_x86_64(+)?,!test=]" {
		t.Fatalf("round trip = %q", got)
	}
	if len(parsed.UseFlags) != 3 || !parsed.UseFlags[0].Conditional || parsed.UseFlags[0].Default == nil || *parsed.UseFlags[0].Default {
		t.Fatalf("parsed USE dependencies = %+v", parsed.UseFlags)
	}
}

func TestParse_VersionComparison(t *testing.T) {
	tests := []struct {
		a, b string
		cmp  int
	}{
		{"1.0", "1.0", 0},
		{"1.0", "2.0", -1},
		{"2.0", "1.0", 1},
		{"1.2", "1.2.0", -1},
		{"1.2.3", "1.2.3", 0},
		{"1.0_alpha", "1.0", -1},
		{"1.0_beta", "1.0", -1},
		{"1.0_pre", "1.0", -1},
		{"1.0_rc", "1.0", -1},
		{"1.0_alpha", "1.0_beta", -1},
		{"1.0_beta", "1.0_pre", -1},
		{"1.0_pre", "1.0_rc", -1},
		{"1.0_rc", "1.0", -1},
		{"1.0", "1.0-r1", -1},
		{"1.0-r1", "1.0-r2", -1},
		{"1.0-r2", "1.0-r1", 1},
		{"1.0_p1", "1.0_p2", -1},
		{"1.0-r0", "1.0-r1", -1},
	}

	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			va, err := parseVersionString(tt.a)
			if err != nil {
				t.Fatalf("parseVersionString(%q): %v", tt.a, err)
			}
			vb, err := parseVersionString(tt.b)
			if err != nil {
				t.Fatalf("parseVersionString(%q): %v", tt.b, err)
			}
			got := va.Compare(vb)
			if got != tt.cmp {
				t.Errorf("%s.Compare(%s) = %d, want %d", tt.a, tt.b, got, tt.cmp)
			}
		})
	}
}

func TestPatchSuffixDoesNotReplacePackageRevision(t *testing.T) {
	plain, err := ParseVersion("5.3_p9")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := ParseVersion("5.3_p9-r2")
	if err != nil {
		t.Fatal(err)
	}
	if plain.Revision != -1 || r2.Revision != 2 || plain.Compare(r2) >= 0 {
		t.Fatalf("plain=%+v r2=%+v compare=%d", plain, r2, plain.Compare(r2))
	}
}

func TestAtom_String_Roundtrip(t *testing.T) {
	atoms := []string{
		"sys-devel/gcc",
		">=sys-devel/gcc-12.2.0",
		"=sys-devel/gcc-12.2.0-r1",
		"~sys-devel/gcc-12.2.0",
		"sys-devel/gcc:12",
		"sys-devel/gcc:12/12.2",
		"sys-devel/gcc:=",
		"sys-devel/gcc:*",
		"sys-devel/gcc::gentoo",
		"sys-devel/gcc[fortran]",
		"sys-devel/gcc[fortran,-doc]",
		">=sys-devel/gcc-12.2.0:12/12.2=[fortran]",
		"dev-lang/python-3.12.0_alpha1",
		"dev-lang/python-3.12.0_rc1",
		"virtual/rust-1.75.0",
	}

	for _, a := range atoms {
		t.Run(a, func(t *testing.T) {
			parsed, err := Parse(a)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", a, err)
			}
			got := parsed.String()
			if got != a {
				t.Errorf("roundtrip:\n  input: %q\n  got:   %q", a, got)
			}
		})
	}
}

func TestAtom_CPV(t *testing.T) {
	a, _ := Parse(">=sys-devel/gcc-12.2.0-r1:12/12.2=[fortran]")
	got := a.CPV()
	want := "sys-devel/gcc-12.2.0-r1"
	if got != want {
		t.Errorf("CPV() = %q, want %q", got, want)
	}

	b, _ := Parse("virtual/rust")
	if got := b.CPV(); got != "virtual/rust" {
		t.Errorf("CPV() = %q, want %q", got, "virtual/rust")
	}
}

func TestAtom_CP(t *testing.T) {
	a, _ := Parse(">=sys-devel/gcc-12.2.0-r1:12/12.2=[fortran]")
	if got := a.CP(); got != "sys-devel/gcc" {
		t.Errorf("CP() = %q, want %q", got, "sys-devel/gcc")
	}
}

func TestParse_Errors(t *testing.T) {
	tests := []string{
		"",
		"gcc",
		"/gcc",
		"sys-devel/",
		"sys-devel/gcc-",
		"sys-devel/gcc-!invalid",
		"!sys-devel/gcc",
		"!!sys-devel/gcc",
		"sys-devel/gcc::",
		"sys-devel/gcc[]",
		"sys-devel/gcc[foo,]",
		"sys-devel/gcc[,foo]",
		"sys-devel/gcc[foo,,bar]",
		"sys-devel/gcc[foo",
		"sys-devel/gcc[foo]trailing",
		"sys-devel/gcc::gentoo:trailing",
		"sys-devel/gcc:*:",
		"sys-devel/gcc:=:",
		"sys-devel/gcc\x00trailing",
		"sys-devel/gcc\n",
		"sys-devel/gcc:0/*",
		"sys-devel/gcc:0/=",
		"sys-devel/gcc-1.0_foo",
		"sys-devel/gcc-1.0_alpha1x",
		">=sys-devel/gcc",
		"~sys-devel/gcc-1*",
		">=sys-devel/gcc-1*",
		"sys-devel/gcc:",
		"sys-devel/gcc:/1",
		"sys-devel/gcc:bad slot",
		"sys-devel/gcc:.slot",
		"sys-devel/gcc::repo.name",
		"sys-devel/gcc:0*",
		"sys-devel/gcc:0/1*",
		"+sys/pkg",
		"sys/+pkg",
		"sys-devel/gcc[-doc?]",
		"sys-devel/gcc[-doc=]",
		"sys-devel/gcc[doc,doc]",
		"sys-devel/gcc[doc(+),-doc]",
	}
	for _, input := range tests {
		t.Run("error_"+input, func(t *testing.T) {
			_, err := Parse(input)
			if err == nil {
				t.Errorf("Parse(%q) expected error, got nil", input)
			}
		})
	}
}

func TestAtomPortageBoundaryCharacters(t *testing.T) {
	for _, input := range []string{
		"sys.apps/gcc",
		"sys+apps/gcc",
		"sys_apps/gcc",
		"sys-apps/gcc:slot.name/sub-slot+abi=::gentoo-overlay",
		"sys-apps/gcc::_repo",
	} {
		t.Run(input, func(t *testing.T) {
			parsed, err := Parse(input)
			if err != nil {
				t.Fatalf("Parse(%q): %v", input, err)
			}
			if parsed.String() != input {
				t.Fatalf("roundtrip = %q", parsed.String())
			}
		})
	}
}

// TestParsePortageAtomCorpus ports the non-wildcard syntax cases from
// portage.tests.dep.test_atom and test_isvalidatom. Unversioned CPVs are
// intentionally tested at the depstring boundary instead: Parse also serves
// repository and VDB identity parsing, where those CPVs are valid inputs.
func TestParsePortageAtomCorpus(t *testing.T) {
	valid := []string{
		"sys-apps/portage", "=sys-apps/portage-2.1", "=sys-apps/portage-2.1*",
		">=sys-apps/portage-2.1", "<=sys-apps/portage-2.1", ">sys-apps/portage-2.1",
		"<sys-apps/portage-2.1", "~sys-apps/portage-2.1", "sys-apps/portage:foo",
		"=sys-apps/portage-2.2*:foo[bar?,!baz?,!doc=,build=]",
		"=sys-apps/portage-2.2*:foo[bar,-baz,doc?,!build?]",
		"=foo/bar--baz-1-r1", "=foo/bar-baz--1-r1", "=foo/bar-baz---1-r1",
		"=foo/bar-baz---1", "games-strategy/ufo2000", "~games-strategy/ufo2000-0.1",
		"=media-libs/x264-20060810", "foo/b", "app-text/7plus", "foo/666",
		"=dev-libs/poppler-qt3-0.11*", "sys-apps/portage::repo_123-name",
		"=sys-apps/portage-2.1::repo", "=sys-apps/portage-2.1*::repo",
		"sys-apps/portage:foo::repo", "virtual/ffmpeg:0/53", "virtual/ffmpeg:0/53=",
		"virtual/ffmpeg:=", "virtual/ffmpeg:0=", "virtual/ffmpeg:*", "virtual/ffmpeg:0",
		"dev-libs/A[a(+),b(-)=,!c(+)=,d(-)?,!e(+)?,-f(-)]",
	}
	for _, input := range valid {
		t.Run("valid_"+input, func(t *testing.T) {
			parsed, err := Parse(input)
			if err != nil {
				t.Fatalf("Portage-valid atom rejected: %v", err)
			}
			if reparsed, err := Parse(parsed.String()); err != nil || reparsed.String() != parsed.String() {
				t.Fatalf("round trip failed: parsed=%q reparsed=%v err=%v", parsed, reparsed, err)
			}
		})
	}

	invalid := []string{
		"", "cat/pkg\n", "cat/Ҙ", "cat/pkg:/slot", "+cat/pkg", "-cat/pkg", ".cat/pkg",
		"cat/+pkg", "cat/-pkg", "cat/pkg:+slot", "cat/pkg:-slot", "cat/pkg:.slot",
		"cat/pkg[a!]", "cat/pkg[!a]", "cat/pkg[!a!]", "cat/pkg[!a-]", "cat/pkg[-a=]",
		"cat/pkg[-a?]", "cat/pkg[-a!]", "cat/pkg[=a]", "cat/pkg[=a=]", "cat/pkg[=a?]",
		"cat/pkg[=a!]", "cat/pkg[=a-]", "cat/pkg[?a]", "cat/pkg[?a=]", "cat/pkg[?a?]",
		"cat/pkg[?a!]", "cat/pkg[?a-]", "sys-apps/portage[doc]:0", "cat/pkg[a()]",
		"cat/pkg[a(]", "cat/pkg[a)]", "cat/pkg[a(,b]", "cat/pkg[a),b]", "cat/pkg[a(*)]",
		"cat/pkg[a(+-)]", "cat/pkg[(+)a]", "cat/pkg[a=(+)]", "cat/pkg[!(+)a=]",
		"cat/pkg[!a=(+)]", "cat/pkg[a?(+)]", "cat/pkg[!a?(+)]", "cat/pkg[!(+)a?]",
		"cat/pkg[-(+)a]", "cat/pkg[a(+),-a]", "cat/pkg[a(-),-a]", "cat/pkg[-a,a(+)]",
		"cat/pkg[-a,a(-)]", "cat/pkg[-a(+),a(-)]", "cat/pkg[-a(-),a(+)]",
		"sys-apps/portage[doc]::repo_name", "sys-apps/portage:0[doc]::repo_name",
		"sys-apps/portage[doc]:0::repo_name", ">~category/foo-1.0",
		"<~category/foo-1.0", "###cat/foo-1.0", "~sys-apps/portage", "portage", "=portage",
		">=portage-2.1", "null/portage*:0", ">=null/portage", ">null/portage", "=null/portage*",
		"=null/portage", "~null/portage", "<=null/portage", "<null/portage",
		"=foo/bar-1-r1-1-r1", "foo/-z-1", "=foo/bar-123-1", "=foo/bar-123-1*",
		"=foo/bar-123-1-r1", "=foo/bar-123-1-r1*", "=foo/bar-baz-1--r1", "sys-apps/portage-2.1:::repo",
		"sys-apps/portage-2.1:::repo[foo]", "virtual/ffmpeg:0/53*", "virtual/ffmpeg:0*",
	}
	for _, input := range invalid {
		t.Run("invalid_"+input, func(t *testing.T) {
			if parsed, err := Parse(input); err == nil {
				t.Fatalf("Portage-invalid atom accepted as %q", parsed)
			}
		})
	}
}

func TestParsePackageAtomSeparatesConstraintsFromCPVIdentities(t *testing.T) {
	identity, err := Parse("sys-apps/portage-3.0.81")
	if err != nil || identity.Version == nil {
		t.Fatalf("internal CPV identity rejected: atom=%v err=%v", identity, err)
	}
	if _, err := ParsePackageAtom("sys-apps/portage-3.0.81"); err == nil {
		t.Fatal("bare CPV accepted as a package constraint")
	}
	if _, err := ParsePackageAtom("=sys-apps/portage-3.0.81"); err != nil {
		t.Fatalf("operator-qualified package atom rejected: %v", err)
	}
}

func FuzzAtomRoundTrip(f *testing.F) {
	for _, seed := range []string{
		"sys-apps/portage",
		"=sys-apps/portage-3.0.51-r1:0/0::gentoo[doc,-test]",
		"~dev-lang/python-3.13.5-r2:3.13[tcltk(+)]",
		"dev-libs/boost:0/1.88=",
		"virtual/libcrypt:*",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		parsed, err := Parse(input)
		if err != nil {
			return
		}
		rendered := parsed.String()
		reparsed, err := Parse(rendered)
		if err != nil {
			t.Fatalf("Parse(%q) succeeded but reparsing %q failed: %v", input, rendered, err)
		}
		if got := reparsed.String(); got != rendered {
			t.Fatalf("atom rendering is not idempotent: first %q, second %q", rendered, got)
		}
	})
}

func FuzzVersionCompareInvariants(f *testing.F) {
	for _, seed := range [][2]string{
		{"1.0", "1.0-r1"},
		{"1.0_alpha1", "1.0_beta1"},
		{"1.0", "1.0.0"},
		{"9999", "15.3.0_p1"},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, left, right string) {
		a, err := ParseVersion(left)
		if err != nil {
			return
		}
		b, err := ParseVersion(right)
		if err != nil {
			return
		}
		ab := a.Compare(b)
		ba := b.Compare(a)
		if (ab < 0 && ba != 1) || (ab > 0 && ba != -1) || (ab == 0 && ba != 0) {
			t.Fatalf("comparison is not antisymmetric: %q vs %q = %d, reverse = %d", left, right, ab, ba)
		}
	})
}

func TestParse_Adversarial(t *testing.T) {
	inputs := []string{
		"\x00\x00\x00",
		strings.Repeat("a", 10000),
		"\xff\xfe\xfd",
		"//////",
		":::",
		"[[[[",
		"---+---",
		"sys-devel/gcc-12.2.0\x00hack",
		">=<sys-devel/gcc-12.2.0",
		">==sys-devel/gcc-12.2.0",
		"sys-devel/gcc-[]",
		"sys-devel/gcc[",
		"sys-devel/gcc[---",
		"sys-devel/gcc[\x00\x00\x00]",
		"sys-devel/gcc:12:12/12.2",
	}

	for _, input := range inputs {
		t.Run("adversarial", func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Parse(%q) panicked: %v", input, r)
				}
			}()
			_, _ = Parse(input)
		})
	}
}

func TestParse_Mutation(t *testing.T) {
	valid := ">=sys-devel/gcc-12.2.0:12/12.2=[fortran]"
	for i := 0; i < len(valid); i++ {
		mutated := []byte(valid)
		mutated[i] ^= 0xFF
		t.Run("mutation", func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Parse(%q) panicked on byte-flip at position %d: %v", string(mutated), i, r)
				}
			}()
			_, _ = Parse(string(mutated))
		})
	}
}

func TestAtom_Key(t *testing.T) {
	a, _ := Parse(">=sys-devel/gcc-12.2.0")
	if a.Key() != "sys-devel/gcc" {
		t.Errorf("Key() = %q, want sys-devel/gcc", a.Key())
	}
}

func TestAtom_String_NoVersion(t *testing.T) {
	a := &Atom{Category: "sys-devel", Package: "gcc"}
	if a.String() != "sys-devel/gcc" {
		t.Errorf("String() = %q, want sys-devel/gcc", a.String())
	}
}

func TestIsPositiveSuffix(t *testing.T) {
	if !isPositiveSuffix("_p") {
		t.Error("_p should be positive")
	}
	if isPositiveSuffix("_alpha") {
		t.Error("_alpha should not be positive")
	}
	if isPositiveSuffix("_p1") {
		t.Error("_p1 should not be positive (is _p + number, not a suffix)")
	}
	if isPositiveSuffix("_alpha1") {
		t.Error("_alpha1 should not be positive")
	}
	if isPositiveSuffix("") {
		t.Error("empty string should not be positive")
	}
}

func TestVersion_String(t *testing.T) {
	v := Version{Raw: "1.2.3-r1", Numbers: []int{1, 2, 3}, Revision: 1}
	if got := v.String(); got != "1.2.3-r1" {
		t.Errorf("Version.String() = %q, want \"1.2.3-r1\"", got)
	}

	v2 := Version{Raw: "", Numbers: nil, Revision: -1}
	if got := v2.String(); got != "" {
		t.Errorf("Version.String() empty = %q, want \"\"", got)
	}
}

func TestCompare_NilReceiver(t *testing.T) {
	var a *Version
	b := &Version{Raw: "1.0", Numbers: []int{1, 0}, Revision: -1}

	if got := a.Compare(b); got != -1 {
		t.Errorf("nil.Compare(non-nil) = %d, want -1", got)
	}
	if got := b.Compare(a); got != 1 {
		t.Errorf("non-nil.Compare(nil) = %d, want 1", got)
	}
	if got := a.Compare(nil); got != 0 {
		t.Errorf("nil.Compare(nil) = %d, want 0", got)
	}

	var c *Version
	if got := c.Compare(c); got != 0 {
		t.Errorf("nil.Compare(nil) on same nil = %d, want 0", got)
	}
}

func TestProperty_VersionCompareAntisymmetry(t *testing.T) {
	versions := []string{
		"1.0", "2.0", "1.2.3", "1.2.3a", "1.2.3b",
		"1.0_alpha", "1.0_beta", "1.0_pre", "1.0_rc", "1.0_p",
		"1.0-r1", "1.0-r2", "0", "9999", "1.2.3_p4-r5",
		"2.3.4a-r1", "3.4.5_alpha1", "4.5.6_beta2-r3",
	}
	parsed := make([]*Version, len(versions))
	for i, v := range versions {
		a, _ := Parse("cat/pkg-" + v)
		parsed[i] = a.Version
	}

	for i := range parsed {
		for j := range parsed {
			cmp := parsed[i].Compare(parsed[j])
			rev := parsed[j].Compare(parsed[i])
			if cmp == 0 && rev != 0 {
				t.Errorf("Compare(%s,%s)=0 but reverse=%d", versions[i], versions[j], rev)
			}
			if cmp > 0 && rev != -1 {
				t.Errorf("Compare(%s,%s)=%d but reverse expected -1, got %d", versions[i], versions[j], cmp, rev)
			}
			if cmp < 0 && rev != 1 {
				t.Errorf("Compare(%s,%s)=%d but reverse expected 1, got %d", versions[i], versions[j], cmp, rev)
			}
		}
	}
}

func TestProperty_VersionCompareTransitivity(t *testing.T) {
	versions := []string{
		"1.0", "1.1", "2.0", "1.0-r1", "1.0-r2",
		"1.0_alpha", "1.0_beta", "1.0_pre", "1.0_rc",
		"1.0a", "1.0b", "1.0c", "3.0", "4.0", "5.0",
		"1_p1", "1_p2", "1.0_p3",
	}
	parsed := make([]*Version, len(versions))
	for i, v := range versions {
		a, _ := Parse("cat/pkg-" + v)
		parsed[i] = a.Version
	}

	for i := range parsed {
		for j := range parsed {
			if parsed[i].Compare(parsed[j]) <= 0 {
				continue
			}
			for k := range parsed {
				if parsed[j].Compare(parsed[k]) <= 0 {
					continue
				}
				if parsed[i].Compare(parsed[k]) <= 0 {
					t.Errorf("transitivity violation: %s > %s && %s > %s but %s not > %s",
						versions[i], versions[j], versions[j], versions[k], versions[i], versions[k])
				}
			}
		}
	}
}

func TestVersionComparisonMatchesPortageArbitraryPrecisionCases(t *testing.T) {
	tests := []struct {
		newer, older string
	}{
		{"999999999999999999999999999999", "999999999999999999999999999998"},
		{"1.001000000000000000002", "1.001000000000000000001"},
		{"1_alpha999999999999999999999", "1_alpha999999999999999999998"},
		{"1-r999999999999999999999", "1-r999999999999999999998"},
	}
	for _, test := range tests {
		newer, err := ParseVersion(test.newer)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", test.newer, err)
		}
		older, err := ParseVersion(test.older)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", test.older, err)
		}
		if newer.Compare(older) <= 0 || older.Compare(newer) >= 0 {
			t.Fatalf("comparison %s > %s failed", test.newer, test.older)
		}
	}
	left, _ := ParseVersion("01")
	right, _ := ParseVersion("1")
	if left.Compare(right) != 0 {
		t.Fatal("leading zero in first component changed numeric value")
	}
}

func TestVersionComparisonPortageCorpus(t *testing.T) {
	greater := [][2]string{
		{"6.0", "5.0"}, {"5.0", "5"}, {"1.0-r1", "1.0-r0"}, {"1.0-r1", "1.0"},
		{"999999999999999999999999999999", "999999999999999999999999999998"},
		{"1.0.0", "1.0"}, {"1.0.0", "1.0b"}, {"1b", "1"}, {"1b_p1", "1_p1"},
		{"1.1b", "1.1"}, {"12.2.5", "12.2b"},
	}
	less := [][2]string{
		{"4.0", "5.0"}, {"5", "5.0"}, {"1.0_pre2", "1.0_p2"}, {"1.0_alpha2", "1.0_p2"},
		{"1.0_alpha1", "1.0_beta1"}, {"1.0_beta3", "1.0_rc3"},
		{"1.001000000000000000001", "1.001000000000000000002"},
		{"1.00100000000", "1.0010000000000000001"}, {"1.01", "1.1"},
		{"1.0-r0", "1.0-r1"}, {"1.0", "1.0-r1"}, {"1.0", "1.0.0"},
		{"1.0b", "1.0.0"}, {"1_p1", "1b_p1"}, {"1", "1b"}, {"1.1", "1.1b"},
		{"12.2b", "12.2.5"},
	}
	equal := [][2]string{{"4.0", "4.0"}, {"1.0", "1.0"}, {"1.0-r0", "1.0"}, {"01", "1"}}
	compare := func(pair [2]string) int {
		left, err := ParseVersion(pair[0])
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", pair[0], err)
		}
		right, err := ParseVersion(pair[1])
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", pair[1], err)
		}
		return left.Compare(right)
	}
	for _, pair := range greater {
		if got := compare(pair); got <= 0 {
			t.Errorf("Compare(%q, %q) = %d, want > 0", pair[0], pair[1], got)
		}
	}
	for _, pair := range less {
		if got := compare(pair); got >= 0 {
			t.Errorf("Compare(%q, %q) = %d, want < 0", pair[0], pair[1], got)
		}
	}
	for _, pair := range equal {
		if got := compare(pair); got != 0 {
			t.Errorf("Compare(%q, %q) = %d, want 0", pair[0], pair[1], got)
		}
	}
}

func TestProperty_AtomRoundTrip(t *testing.T) {
	atoms := []string{
		"sys-apps/portage",
		"=sys-apps/portage-3.0.51",
		">=dev-lang/python-3.11",
		"~sys-libs/glibc-2.37-r3",
		"<media-libs/libpng-1.6",
		"dev-libs/openssl:0",
		"=sys-devel/gcc-13.2.0:13",
		"app-editors/vim:0/1",
		"=dev-libs/boost-1.84.0:0/1.84.0=",
		"virtual/libcrypt:=",
		"=virtual/rust-1.84.0*",
		"~dev-lang/ruby-3.3:3.3[ssl]",
		"dev-libs/libfoo[-bar,baz]",
		"dev-lang/rust::gentoo",
		"null/nothing",
	}

	for _, input := range atoms {
		t.Run(input, func(t *testing.T) {
			parsed, err := Parse(input)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", input, err)
			}
			rendered := parsed.String()
			reparsed, err := Parse(rendered)
			if err != nil {
				t.Fatalf("re-Parse(%q) error: %v", rendered, err)
			}

			if parsed.Category != reparsed.Category {
				t.Errorf("Category: %q != %q", parsed.Category, reparsed.Category)
			}
			if parsed.Package != reparsed.Package {
				t.Errorf("Package: %q != %q", parsed.Package, reparsed.Package)
			}
			if parsed.Op != reparsed.Op {
				t.Errorf("Op: %q != %q", parsed.Op, reparsed.Op)
			}
			if parsed.Slot != reparsed.Slot {
				t.Errorf("Slot: %q != %q", parsed.Slot, reparsed.Slot)
			}
			if parsed.Subslot != reparsed.Subslot {
				t.Errorf("Subslot: %q != %q", parsed.Subslot, reparsed.Subslot)
			}
			if parsed.Repo != reparsed.Repo {
				t.Errorf("Repo: %q != %q", parsed.Repo, reparsed.Repo)
			}
		})
	}
}

func TestProperty_AtomStringIdempotency(t *testing.T) {
	inputs := []string{
		"=sys-apps/portage-3.0.51",
		">=dev-lang/python-3.11",
		"dev-libs/openssl:0",
		"app-editors/vim:0/1",
	}

	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			a, err := Parse(input)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			s1 := a.String()
			s2 := a.String()
			if s1 != s2 {
				t.Errorf("String() not idempotent: %q != %q", s1, s2)
			}
		})
	}
}
