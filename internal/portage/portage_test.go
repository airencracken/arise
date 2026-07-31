package portage

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestParseReposConfExactLocation(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "repos.conf")
	content := "[gentoo]\nlocation = /var/db/repos/gentoo\nsync-type = git\nsync-uri = https://example.test/gentoo.git\n"
	if err := os.WriteFile(conf, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if got := ParseReposConf(conf, "/var/db/repos/gentoo"); got != "https://example.test/gentoo.git" {
		t.Fatalf("ParseReposConf() = %q", got)
	}
}

func TestParseReposConfMatchesRepositoryNameAfterLocationMigration(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "repos.conf")
	content := "[gentoo]\nlocation = /usr/portage\nsync-type = git\nsync-uri = https://example.test/gentoo.git\n"
	if err := os.WriteFile(conf, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if got := ParseReposConf(conf, "/var/db/repos/gentoo"); got != "https://example.test/gentoo.git" {
		t.Fatalf("ParseReposConf() = %q, want migrated gentoo URI", got)
	}
}

func TestReadReposConfParsesIndependentGitDepthsIncludingFullHistory(t *testing.T) {
	conf := filepath.Join(t.TempDir(), "repos.conf")
	content := "[gentoo]\nlocation = /var/db/repos/gentoo\nsync-type = git\nsync-uri = https://example.test/gentoo.git\nclone-depth = 5\nsync-depth = 0\n"
	if err := os.WriteFile(conf, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := ReadReposConf(conf)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].CloneDepth == nil || *entries[0].CloneDepth != 5 ||
		entries[0].SyncDepth == nil || *entries[0].SyncDepth != 0 {
		t.Fatalf("repository depths = %#v", entries)
	}
}

func TestReadReposConfRejectsInvalidGitDepth(t *testing.T) {
	for _, value := range []string{"-1", "all", "1.5"} {
		t.Run(value, func(t *testing.T) {
			conf := filepath.Join(t.TempDir(), "repos.conf")
			if err := os.WriteFile(conf, []byte("[gentoo]\nclone-depth = "+value+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadReposConf(conf); err == nil || !strings.Contains(err.Error(), "clone-depth") {
				t.Fatalf("invalid depth error = %v", err)
			}
		})
	}
}

func TestRepositoryPolicyOrderMastersBeforeChildren(t *testing.T) {
	root := t.TempDir()
	master := filepath.Join(root, "master")
	child := filepath.Join(root, "child")
	for _, repository := range []string{master, child} {
		if err := os.MkdirAll(filepath.Join(repository, "metadata"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(child, "metadata", "layout.conf"), []byte("masters = master\n"), 0644); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(root, "repos.conf")
	content := fmt.Sprintf("[child]\nlocation = %s\n[master]\nlocation = %s\n", child, master)
	if err := os.WriteFile(conf, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	order, err := RepositoryPolicyOrder(conf)
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0].Name != "master" || order[1].Name != "child" {
		t.Fatalf("repository order = %#v", order)
	}
}

func TestRepositoryPolicyOrderRejectsCycle(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(root, name, "metadata"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "a", "metadata", "layout.conf"), []byte("masters = b\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b", "metadata", "layout.conf"), []byte("masters = a\n"), 0644); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(root, "repos.conf")
	content := fmt.Sprintf("[a]\nlocation = %s\n[b]\nlocation = %s\n", filepath.Join(root, "a"), filepath.Join(root, "b"))
	if err := os.WriteFile(conf, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := RepositoryPolicyOrder(conf); err == nil {
		t.Fatal("expected repository master cycle error")
	}
}

func TestLoadProfileMaskStackIncludesRepositoryOrder(t *testing.T) {
	root := t.TempDir()
	master := filepath.Join(root, "master")
	child := filepath.Join(root, "child")
	profile := filepath.Join(root, "active")
	for _, directory := range []string{filepath.Join(master, "profiles"), filepath.Join(child, "profiles"), profile} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(master, "profiles", "package.mask"), []byte("dev-libs/old\n"), 0644)
	os.WriteFile(filepath.Join(child, "profiles", "package.mask"), []byte("-dev-libs/old\ndev-libs/child\n"), 0644)
	os.WriteFile(filepath.Join(profile, "package.mask"), []byte("dev-libs/profile\n"), 0644)
	masks, _, err := loadProfileMaskStack([]string{master, child}, []string{profile})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"dev-libs/child", "dev-libs/profile"}
	if !reflect.DeepEqual(masks, want) {
		t.Fatalf("repository/profile masks = %v, want %v", masks, want)
	}
}

func TestParseMakeConf_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "make.conf")
	content := `# Comment line
CFLAGS="-O2 -pipe -march=native"
MAKEOPTS="-j8"
USE="X ssl -qt5"
ACCEPT_KEYWORDS="~amd64"
ACCEPT_LICENSE="* -@EULA"
FEATURES="ccache parallel-install"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParseMakeConf(path)
	if err != nil {
		t.Fatalf("ParseMakeConf: %v", err)
	}

	if m["CFLAGS"] != "-O2 -pipe -march=native" {
		t.Errorf("CFLAGS = %q", m["CFLAGS"])
	}
	if m["MAKEOPTS"] != "-j8" {
		t.Errorf("MAKEOPTS = %q", m["MAKEOPTS"])
	}
	if m["USE"] != "X ssl -qt5" {
		t.Errorf("USE = %q", m["USE"])
	}
	if m["ACCEPT_KEYWORDS"] != "~amd64" {
		t.Errorf("ACCEPT_KEYWORDS = %q", m["ACCEPT_KEYWORDS"])
	}
	if m["ACCEPT_LICENSE"] != "* -@EULA" {
		t.Errorf("ACCEPT_LICENSE = %q", m["ACCEPT_LICENSE"])
	}
	if m["FEATURES"] != "ccache parallel-install" {
		t.Errorf("FEATURES = %q", m["FEATURES"])
	}
}

func TestParseMakeConf_LineContinuation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "make.conf")
	content := `CFLAGS="-O2 \
-pipe \
-march=native"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParseMakeConf(path)
	if err != nil {
		t.Fatalf("ParseMakeConf: %v", err)
	}

	want := "-O2 -pipe -march=native"
	if m["CFLAGS"] != want {
		t.Errorf("CFLAGS = %q, want %q", m["CFLAGS"], want)
	}
}

func TestParseMakeConf_MultiLineVariableContinuation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "make.conf")
	content := `EMERGE_DEFAULT_OPTS="--jobs=4 \
--load-average=8 \
--keep-going \
--verbose"
MAKEOPTS="-j8"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParseMakeConf(path)
	if err != nil {
		t.Fatalf("ParseMakeConf: %v", err)
	}

	wantOpts := "--jobs=4 --load-average=8 --keep-going --verbose"
	if m["EMERGE_DEFAULT_OPTS"] != wantOpts {
		t.Errorf("EMERGE_DEFAULT_OPTS = %q, want %q", m["EMERGE_DEFAULT_OPTS"], wantOpts)
	}
	if m["MAKEOPTS"] != "-j8" {
		t.Errorf("MAKEOPTS = %q", m["MAKEOPTS"])
	}
}

func TestParseMakeConf_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "make.conf")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParseMakeConf(path)
	if err != nil {
		t.Fatalf("ParseMakeConf: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map, got %d entries", len(m))
	}
}

func TestParseMakeConf_CommentOnlyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "make.conf")
	content := "# only comments\n# nothing here\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParseMakeConf(path)
	if err != nil {
		t.Fatalf("ParseMakeConf: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map, got %d entries", len(m))
	}
}

func TestParseMakeConf_MissingFile(t *testing.T) {
	m, err := ParseMakeConf("/nonexistent/make.conf")
	if err != nil {
		t.Errorf("ParseMakeConf should not error on missing file: %v", err)
	}
	if m != nil {
		t.Errorf("expected nil map for missing file, got %v", m)
	}
}

func TestParseMakeConf_UnquotedValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "make.conf")
	content := "MAKEOPTS=-j8\nPORTAGE_TMPDIR=/var/tmp\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParseMakeConf(path)
	if err != nil {
		t.Fatalf("ParseMakeConf: %v", err)
	}
	if m["MAKEOPTS"] != "-j8" {
		t.Errorf("MAKEOPTS = %q", m["MAKEOPTS"])
	}
	if m["PORTAGE_TMPDIR"] != "/var/tmp" {
		t.Errorf("PORTAGE_TMPDIR = %q", m["PORTAGE_TMPDIR"])
	}
}

func TestResolveMakeConfRefs_Basic(t *testing.T) {
	m := map[string]string{
		"CFLAGS":   "-O2 -pipe -march=native",
		"CXXFLAGS": "${CFLAGS}",
	}

	ResolveMakeConfRefs(m)

	if m["CXXFLAGS"] != "-O2 -pipe -march=native" {
		t.Errorf("CXXFLAGS = %q, want %q", m["CXXFLAGS"], "-O2 -pipe -march=native")
	}
}

func TestResolveMakeConfRefs_MultiLevel(t *testing.T) {
	m := map[string]string{
		"BASE":  "-O2",
		"FLAGS": "${BASE} -pipe",
		"FINAL": "${FLAGS} -march=native",
	}

	ResolveMakeConfRefs(m)

	if m["FLAGS"] != "-O2 -pipe" {
		t.Errorf("FLAGS = %q, want %q", m["FLAGS"], "-O2 -pipe")
	}
	if m["FINAL"] != "-O2 -pipe -march=native" {
		t.Errorf("FINAL = %q, want %q", m["FINAL"], "-O2 -pipe -march=native")
	}
}

func TestResolveMakeConfRefs_PartialReference(t *testing.T) {
	m := map[string]string{
		"ARCH":   "x86_64",
		"CFLAGS": "-march=${ARCH} -pipe",
	}

	ResolveMakeConfRefs(m)

	if m["CFLAGS"] != "-march=x86_64 -pipe" {
		t.Errorf("CFLAGS = %q", m["CFLAGS"])
	}
}

func TestResolveMakeConfRefs_MultipleRefs(t *testing.T) {
	m := map[string]string{
		"OPT":    "-O2",
		"ARCH":   "native",
		"CFLAGS": "${OPT} -march=${ARCH} -pipe",
	}

	ResolveMakeConfRefs(m)

	if m["CFLAGS"] != "-O2 -march=native -pipe" {
		t.Errorf("CFLAGS = %q", m["CFLAGS"])
	}
}

func TestResolveMakeConfRefs_SelfReference(t *testing.T) {
	m := map[string]string{
		"CFLAGS": "${CFLAGS} -extra",
	}

	ResolveMakeConfRefs(m)

	if m["CFLAGS"] != " -extra" {
		t.Errorf("CFLAGS = %q, want %q", m["CFLAGS"], " -extra")
	}
}

func TestResolveMakeConfRefs_Cycle(t *testing.T) {
	m := map[string]string{
		"A": "${B}",
		"B": "${A}",
	}

	ResolveMakeConfRefs(m)

	if m["A"] != "" || m["B"] != "" {
		t.Errorf("cycle should resolve to empty: A=%q, B=%q", m["A"], m["B"])
	}
}

func TestResolveMakeConfRefs_Nil(t *testing.T) {
	ResolveMakeConfRefs(nil)
}

func TestResolveMakeConfRefs_DeepNesting(t *testing.T) {
	m := make(map[string]string)
	for i := 1; i <= 20; i++ {
		key := keyFor(i)
		nextKey := keyFor(i + 1)
		if i == 20 {
			m[key] = "final-value"
		} else {
			m[key] = "${" + nextKey + "}"
		}
	}

	ResolveMakeConfRefs(m)

	for i := 1; i <= 20; i++ {
		if m[keyFor(i)] != "final-value" {
			t.Errorf("%s = %q, want \"final-value\"", keyFor(i), m[keyFor(i)])
		}
	}
}

func keyFor(i int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz"
	var name string
	n := i - 1
	for {
		name = string(alphabet[n%26]) + name
		n = n/26 - 1
		if n < 0 {
			break
		}
	}
	return "VAR_" + strings.ToUpper(name)
}

func TestSplitShWords_Basic(t *testing.T) {
	words := splitShWords("X ssl -qt5")
	expected := []string{"X", "ssl", "-qt5"}
	if !reflect.DeepEqual(words, expected) {
		t.Errorf("splitShWords = %v, want %v", words, expected)
	}
}

func TestSplitShWords_NegativeFlag(t *testing.T) {
	words := splitShWords("X ssl -qt5 -X")
	expected := []string{"X", "ssl", "-qt5", "-X"}
	if !reflect.DeepEqual(words, expected) {
		t.Errorf("splitShWords = %v, want %v", words, expected)
	}
}

func TestSplitShWords_DoubleQuoted(t *testing.T) {
	words := splitShWords(`"hello world" foo`)
	expected := []string{"hello world", "foo"}
	if !reflect.DeepEqual(words, expected) {
		t.Errorf("splitShWords = %v, want %v", words, expected)
	}
}

func TestSplitShWords_SingleQuoted(t *testing.T) {
	words := splitShWords(`'hello world' foo`)
	expected := []string{"hello world", "foo"}
	if !reflect.DeepEqual(words, expected) {
		t.Errorf("splitShWords = %v, want %v", words, expected)
	}
}

func TestSplitShWords_Empty(t *testing.T) {
	words := splitShWords("")
	if words != nil {
		t.Errorf("splitShWords empty string: expected nil, got %v", words)
	}

	words = splitShWords("   ")
	if words != nil {
		t.Errorf("splitShWords whitespace: expected nil, got %v", words)
	}
}

func TestSplitShWords_OnlyNegatedFlags(t *testing.T) {
	words := splitShWords("-X -ssl -qt5")
	expected := []string{"-X", "-ssl", "-qt5"}
	if !reflect.DeepEqual(words, expected) {
		t.Errorf("splitShWords = %v, want %v", words, expected)
	}
}

func TestSplitShWords_Star(t *testing.T) {
	words := splitShWords("*")
	expected := []string{"*"}
	if !reflect.DeepEqual(words, expected) {
		t.Errorf("splitShWords = %v, want %v", words, expected)
	}
}

func TestSplitShWords_EULASlash(t *testing.T) {
	words := splitShWords("* -@EULA")
	expected := []string{"*", "-@EULA"}
	if !reflect.DeepEqual(words, expected) {
		t.Errorf("splitShWords = %v, want %v", words, expected)
	}
}

func TestParseAtomConfig_Basic(t *testing.T) {
	tests := []struct {
		line      string
		atom, val string
	}{
		{"dev-lang/python ssl sqlite", "dev-lang/python", "ssl sqlite"},
		{"app-editors/vim -X", "app-editors/vim", "-X"},
		{"*/* python_targets_python3_11", "*/*", "python_targets_python3_11"},
		{">=dev-lang/python-3.11 ssl", ">=dev-lang/python-3.11", "ssl"},
		{"=app-editors/vim-9.0 **", "=app-editors/vim-9.0", "**"},
		{"   dev-lang/python   ssl  ", "dev-lang/python", "ssl"},
		{"dev-lang/python", "dev-lang/python", ""},
		{"sys-devel/gcc-12", "sys-devel/gcc-12", ""},
		{"", "", ""},
		{"   ", "", ""},
		{">=dev-util/nvidia-cuda-toolkit-11 NVIDIA-r2", ">=dev-util/nvidia-cuda-toolkit-11", "NVIDIA-r2"},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			atom, val := parseAtomConfig(tt.line)
			if atom != tt.atom || val != tt.val {
				t.Errorf("parseAtomConfig(%q) = (%q, %q), want (%q, %q)",
					tt.line, atom, val, tt.atom, tt.val)
			}
		})
	}
}

func TestParsePackageUse_SimpleAtom(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.use")
	content := ">=dev-lang/python-3.11 ssl sqlite berkdb\napp-editors/vim -X\n*/* python_targets_python3_11\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParsePackageUse(path)
	if err != nil {
		t.Fatalf("ParsePackageUse: %v", err)
	}

	want := map[string][]string{
		">=dev-lang/python-3.11": {"ssl", "sqlite", "berkdb"},
		"app-editors/vim":        {"-X"},
		"*/*":                    {"python_targets_python3_11"},
	}

	if !reflect.DeepEqual(m, want) {
		t.Errorf("ParsePackageUse =\n  got:  %v\n  want: %v", m, want)
	}
}

func TestPackageUseForMatchesOrderedFullAtoms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.use")
	content := strings.Join([]string{
		"*/* global",
		"www-client/firefox -global cp",
		">=www-client/firefox-150 versioned",
		"<www-client/firefox-150 old",
		"www-client/firefox:rapid slot",
		"www-client/firefox::gentoo repo",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	rules, err := ParsePackageUseRules(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{PackageUseRules: rules}
	got := cfg.PackageUseFor("www-client/firefox-152.0.6", "rapid", "gentoo")
	want := []string{"global", "-global", "cp", "versioned", "slot", "repo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matching package.use = %v, want %v", got, want)
	}

	got = cfg.PackageUseFor("www-client/firefox-140.0", "esr", "local")
	want = []string{"global", "-global", "cp", "old"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("older package.use = %v, want %v", got, want)
	}
}

func TestPackageProfilePolicyMatchesAtomsAndRemovalSyntax(t *testing.T) {
	cfg := &Config{PackageUseForceRules: []PackageUseRule{
		{Atom: "www-client/firefox", Flags: []string{"inherited", "retained"}},
		{Atom: ">=www-client/firefox-150", Flags: []string{"-inherited", "new"}},
	}}
	if got, want := cfg.PackageUseForceFor("www-client/firefox-152.0", "rapid", "gentoo"), []string{"retained", "new"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("new policy = %v, want %v", got, want)
	}
	if got, want := cfg.PackageUseForceFor("www-client/firefox-140.0", "esr", "gentoo"), []string{"inherited", "retained"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("old policy = %v, want %v", got, want)
	}
}

func TestPackageMaskStatusUsesFullCandidateIdentity(t *testing.T) {
	cfg := &Config{
		PackageMask: []string{
			">=www-client/firefox-150:rapid::gentoo",
		},
		PackageUnmask: []string{
			"=www-client/firefox-152.0.6:rapid::gentoo",
		},
	}
	if got := cfg.PackageMaskStatus("www-client/firefox-152.0.5", "rapid", "gentoo"); !got.Masked || got.Atom == "" {
		t.Fatalf("masked status = %+v", got)
	}
	if got := cfg.PackageMaskStatus("www-client/firefox-152.0.6", "rapid", "gentoo"); got.Masked || got.Source != "package.unmask" {
		t.Fatalf("unmasked status = %+v", got)
	}
	if got := cfg.PackageMaskStatus("www-client/firefox-152.0.5", "esr", "gentoo"); got.Masked {
		t.Fatalf("wrong-slot status = %+v", got)
	}
	if got := cfg.PackageMaskStatus("www-client/firefox-152.0.5", "rapid", "local"); got.Masked {
		t.Fatalf("wrong-repository status = %+v", got)
	}
}

func TestEffectiveUseForLayerPrecedence(t *testing.T) {
	cfg := &Config{
		USE:                  []string{"global", "-disabled", "masked"},
		PackageUseRules:      []PackageUseRule{{Atom: ">=app-editors/vim-9", Flags: []string{"disabled", "-global", "local"}}},
		UseForce:             []string{"global"},
		UseMask:              []string{"masked"},
		PackageUseForceRules: []PackageUseRule{{Atom: "app-editors/vim", Flags: []string{"pkgforced"}}},
		PackageUseMaskRules:  []PackageUseRule{{Atom: "app-editors/vim", Flags: []string{"local"}}},
	}
	got := cfg.EffectiveUseFor("app-editors/vim-9.1", "0", "gentoo")
	want := map[string]bool{
		"global": true, "disabled": true, "masked": false,
		"local": false, "pkgforced": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effective USE = %v, want %v", got, want)
	}
}

func TestPackageAtomMatchesSubslot(t *testing.T) {
	if !PackageAtomMatches("dev-cpp/eigen:3/5.0", "dev-cpp/eigen-5.0.1", "3/5.0", "gentoo") {
		t.Fatal("matching subslot was rejected")
	}
	if PackageAtomMatches("dev-cpp/eigen:3/5.0", "dev-cpp/eigen-3.4.0-r3", "3/3.4", "gentoo") {
		t.Fatal("subslot-qualified rule matched a different subslot")
	}
}

func TestPackageAtomMatchesRejectsUnversionedCandidateWithoutPanic(t *testing.T) {
	if !PackageAtomMatches("virtual/libc", "virtual/libc", "", "") {
		t.Fatal("unversioned package rule did not match an unversioned synthetic candidate")
	}
	if PackageAtomMatches(">=virtual/libc-1", "virtual/libc", "", "") {
		t.Fatal("versioned package rule matched an unversioned synthetic candidate")
	}
}

func TestEffectiveUseForPackagePolicyCanRemoveGlobalMask(t *testing.T) {
	cfg := &Config{
		UseStableMask: []string{"future-target", "still-masked"},
		PackageUseMaskRules: []PackageUseRule{{
			Atom: "dev-lang/python-exec", Flags: []string{"-future-target"},
		}},
		PackageUseForceRules: []PackageUseRule{{
			Atom: "dev-lang/python-exec", Flags: []string{"future-target", "still-masked"},
		}},
	}
	got := cfg.EffectiveUseForStability("dev-lang/python-exec-2.4.10", "2", "gentoo", true)
	if !got["future-target"] {
		t.Fatalf("package mask removal did not expose forced flag: %v", got)
	}
	if got["still-masked"] {
		t.Fatalf("global mask did not override package force: %v", got)
	}
}

func TestExplicitUseOverrideExcludesProfileDefaults(t *testing.T) {
	cfg := &Config{USE: []string{"-debug"}}
	if cfg.ExplicitUseOverride("dev-libs/libnl-3.12.0", "3", "gentoo", "debug", true) {
		t.Fatal("merged profile default was treated as an explicit override")
	}
	cfg.UserUSE = []string{"-debug"}
	if !cfg.ExplicitUseOverride("dev-libs/libnl-3.12.0", "3", "gentoo", "debug", true) {
		t.Fatal("user make.conf override was ignored")
	}
}

func TestParsePackageUse_NegativeFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.use")
	content := "app-editors/vim -X\nsys-devel/gcc -fortran -objc\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParsePackageUse(path)
	if err != nil {
		t.Fatalf("ParsePackageUse: %v", err)
	}

	if flags := m["app-editors/vim"]; len(flags) != 1 || flags[0] != "-X" {
		t.Errorf("app-editors/vim flags = %v, want [-X]", flags)
	}
	if flags := m["sys-devel/gcc"]; len(flags) != 2 {
		t.Errorf("sys-devel/gcc flags = %v, want 2 flags", flags)
	}
}

func TestParsePackageUse_MultipleLinesSameAtom(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.use")
	content := "dev-lang/python ssl\nsys-devel/gcc fortran\ndev-lang/python sqlite\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParsePackageUse(path)
	if err != nil {
		t.Fatalf("ParsePackageUse: %v", err)
	}

	pythonFlags := m["dev-lang/python"]
	if len(pythonFlags) != 2 {
		t.Errorf("dev-lang/python flags = %v, want 2 flags", pythonFlags)
	}
}

func TestParsePackageUse_DirectoryOfFiles(t *testing.T) {
	dir := t.TempDir()
	pkgUseDir := filepath.Join(dir, "package.use")
	if err := os.MkdirAll(pkgUseDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(pkgUseDir, "python"), []byte("dev-lang/python ssl sqlite\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgUseDir, "vim"), []byte("app-editors/vim -X\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParsePackageUse(pkgUseDir)
	if err != nil {
		t.Fatalf("ParsePackageUse directory: %v", err)
	}

	if len(m) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(m), m)
	}
	if !reflect.DeepEqual(m["dev-lang/python"], []string{"ssl", "sqlite"}) {
		t.Errorf("python flags = %v", m["dev-lang/python"])
	}
	if !reflect.DeepEqual(m["app-editors/vim"], []string{"-X"}) {
		t.Errorf("vim flags = %v", m["app-editors/vim"])
	}
}

func TestParsePackageUse_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.use")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParsePackageUse(path)
	if err != nil {
		t.Fatalf("ParsePackageUse: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map, got %d entries", len(m))
	}
}

func TestParsePackageUse_CommentOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.use")
	content := "# comment\n# another comment\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParsePackageUse(path)
	if err != nil {
		t.Fatalf("ParsePackageUse: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map, got %d entries", len(m))
	}
}

func TestParsePackageUse_MissingFile(t *testing.T) {
	m, err := ParsePackageUse("/nonexistent/package.use")
	if err != nil {
		t.Fatalf("ParsePackageUse should not error on missing: %v", err)
	}
	if m != nil {
		t.Errorf("expected nil map, got %v", m)
	}
}

func TestParsePackageAcceptKeywords_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.accept_keywords")
	content := "dev-lang/python ~amd64\n=app-editors/vim-9.0 **\n*/* ~amd64\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParsePackageAcceptKeywords(path)
	if err != nil {
		t.Fatalf("ParsePackageAcceptKeywords: %v", err)
	}

	want := map[string]string{
		"dev-lang/python":      "~amd64",
		"=app-editors/vim-9.0": "**",
		"*/*":                  "~amd64",
	}

	if !reflect.DeepEqual(m, want) {
		t.Errorf("ParsePackageAcceptKeywords =\n  got:  %v\n  want: %v", m, want)
	}
}

func TestParsePackageAcceptKeywords_Directory(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "package.accept_keywords")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(pkgDir, "01"), []byte("dev-lang/python ~amd64\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "02"), []byte("=app-editors/vim-9.0 **\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParsePackageAcceptKeywords(pkgDir)
	if err != nil {
		t.Fatalf("ParsePackageAcceptKeywords directory: %v", err)
	}

	if len(m) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(m))
	}
	if m["dev-lang/python"] != "~amd64" {
		t.Errorf("python keyword = %q", m["dev-lang/python"])
	}
}

func TestPackageAcceptKeywordsForPreservesFullAtomOrder(t *testing.T) {
	cfg := &Config{PackageAcceptKeywordRules: []PackageUseRule{
		{Atom: "dev-lang/python", Flags: []string{"~amd64"}},
		{Atom: ">=dev-lang/python-3.13", Flags: []string{"-~amd64"}},
		{Atom: "=dev-lang/python-3.13.2::gentoo", Flags: []string{"~amd64"}},
	}}
	got := cfg.PackageAcceptKeywordsFor("dev-lang/python-3.13.2", "0", "gentoo", "amd64")
	want := []string{"~amd64", "-~amd64", "~amd64"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PackageAcceptKeywordsFor() = %v, want %v", got, want)
	}
}

func TestPackageAcceptKeywordsForEmptyRuleAcceptsHostTesting(t *testing.T) {
	cfg := &Config{PackageAcceptKeywordRules: []PackageUseRule{{Atom: "dev-lang/python"}}}
	got := cfg.PackageAcceptKeywordsFor("dev-lang/python-3.13", "0", "gentoo", "amd64")
	if !reflect.DeepEqual(got, []string{"~amd64"}) {
		t.Fatalf("PackageAcceptKeywordsFor() = %v", got)
	}
}

func TestEffectiveAcceptedKeywordsForAppliesOrderedGlobalAndPackagePolicy(t *testing.T) {
	cfg := &Config{
		ACCEPT_KEYWORDS: []string{"~amd64", "-amd64"},
		PackageAcceptKeywordRules: []PackageUseRule{
			{Atom: "dev-lang/python", Flags: []string{"amd64", "-~amd64"}},
			{Atom: ">=dev-lang/python-3.13", Flags: []string{"~amd64"}},
		},
	}
	got := cfg.EffectiveAcceptedKeywordsFor("dev-lang/python-3.13", "0", "gentoo", "amd64")
	if want := []string{"amd64", "~amd64"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("EffectiveAcceptedKeywordsFor() = %v, want %v", got, want)
	}
}

func TestEffectiveAcceptedKeywordsForRejectsAdversarialNonmatchingRule(t *testing.T) {
	cfg := &Config{
		PackageAcceptKeywordRules: []PackageUseRule{
			{Atom: "=dev-lang/python-3.13", Flags: []string{"~amd64"}},
			{Atom: "../../etc/passwd", Flags: []string{"**"}},
		},
	}
	got := cfg.EffectiveAcceptedKeywordsFor("dev-lang/python-3.12", "0", "gentoo", "amd64")
	if want := []string{"amd64"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("nonmatching policy changed accepted keywords: got %v, want %v", got, want)
	}
}

func TestParsePackageLicense_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.license")
	content := "dev-lang/oracle-jdk-bin Oracle-BCLA-JavaSE\n>=dev-util/nvidia-cuda-toolkit-11 NVIDIA-r2\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParsePackageLicense(path)
	if err != nil {
		t.Fatalf("ParsePackageLicense: %v", err)
	}

	want := map[string]string{
		"dev-lang/oracle-jdk-bin":           "Oracle-BCLA-JavaSE",
		">=dev-util/nvidia-cuda-toolkit-11": "NVIDIA-r2",
	}

	if !reflect.DeepEqual(m, want) {
		t.Errorf("ParsePackageLicense =\n  got:  %v\n  want: %v", m, want)
	}
}

func TestParsePackageLicense_MissingFile(t *testing.T) {
	m, err := ParsePackageLicense("/nonexistent/package.license")
	if err != nil {
		t.Fatalf("ParsePackageLicense should not error on missing: %v", err)
	}
	if m != nil {
		t.Errorf("expected nil map, got %v", m)
	}
}

func TestPackageLicensesForPreservesFullAtomOrder(t *testing.T) {
	cfg := &Config{PackageLicenseRules: []PackageUseRule{
		{Atom: "dev-lang/oracle-jdk-bin", Flags: []string{"Oracle-BCLA-JavaSE"}},
		{Atom: ">=dev-lang/oracle-jdk-bin-22", Flags: []string{"-Oracle-BCLA-JavaSE"}},
		{Atom: "=dev-lang/oracle-jdk-bin-22::gentoo", Flags: []string{"Oracle-No-Fee-Terms-and-Conditions"}},
	}}
	got := cfg.PackageLicensesFor("dev-lang/oracle-jdk-bin-22", "0", "gentoo")
	want := []string{"Oracle-BCLA-JavaSE", "-Oracle-BCLA-JavaSE", "Oracle-No-Fee-Terms-and-Conditions"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PackageLicensesFor() = %v, want %v", got, want)
	}
}

func TestLicenseGroupsStackAndExpand(t *testing.T) {
	master := t.TempDir()
	overlay := t.TempDir()
	for _, root := range []string{master, overlay} {
		if err := os.MkdirAll(filepath.Join(root, "profiles"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(master, "profiles", "license_groups"), []byte("FREE MIT BSD\nREDIST @FREE firmware\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overlay, "profiles", "license_groups"), []byte("FREE -BSD Apache-2.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	groups, err := ParseLicenseGroups([]string{master, overlay})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := groups["FREE"], []string{"MIT", "Apache-2.0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("FREE = %v, want %v", got, want)
	}
	if got, want := ExpandLicenseGroups([]string{"-*", "@REDIST", "-@FREE"}, groups),
		[]string{"-*", "MIT", "Apache-2.0", "firmware", "-MIT", "-Apache-2.0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expanded groups = %v, want %v", got, want)
	}
}

func TestLicenseGroupsCycleRemainsExplicit(t *testing.T) {
	groups := map[string][]string{"A": {"@B"}, "B": {"@A", "MIT"}}
	got := ExpandLicenseGroups([]string{"@A"}, groups)
	if !reflect.DeepEqual(got, []string{"@A", "MIT"}) {
		t.Fatalf("cycle expansion = %v", got)
	}
}

func TestParsePackageMask_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.mask")
	content := ">=dev-lang/python-3.12\n<app-editors/vim-9\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	atoms, err := ParsePackageMask(path)
	if err != nil {
		t.Fatalf("ParsePackageMask: %v", err)
	}

	want := []string{">=dev-lang/python-3.12", "<app-editors/vim-9"}
	if !reflect.DeepEqual(atoms, want) {
		t.Errorf("ParsePackageMask = %v, want %v", atoms, want)
	}
}

func TestParsePackageMask_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.mask")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	atoms, err := ParsePackageMask(path)
	if err != nil {
		t.Fatalf("ParsePackageMask: %v", err)
	}
	if atoms != nil {
		t.Errorf("expected nil for empty file, got %v", atoms)
	}
}

func TestParsePackageMaskRulesRetainsGLEP84Reason(t *testing.T) {
	path := filepath.Join(t.TempDir(), "package.mask")
	content := "# Dev Example <dev@example.test> (2026-07-17)\n# Broken ABI. Bug #123.\n>=dev-libs/foo-2\n=dev-libs/foo-3\n\n# Different reason.\ndev-libs/bar\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	rules, err := ParsePackageMaskRules(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 3 {
		t.Fatalf("rules = %#v", rules)
	}
	want := "Dev Example <dev@example.test> (2026-07-17) Broken ABI. Bug #123."
	if rules[0].Reason != want || rules[1].Reason != want || rules[0].Source != path {
		t.Fatalf("GLEP 84 rules = %#v", rules)
	}
	if rules[2].Reason != "Different reason." {
		t.Fatalf("bar reason = %q", rules[2].Reason)
	}
}

func TestPackageMaskStatusIncludesReasonAndSource(t *testing.T) {
	cfg := &Config{PackageMask: []string{">=dev-libs/foo-2"}, PackageMaskRules: []PackageMaskRule{{Atom: ">=dev-libs/foo-2", Source: "/repo/profiles/package.mask", Reason: "Broken ABI."}}}
	status := cfg.PackageMaskStatus("dev-libs/foo-3", "0", "gentoo")
	if !status.Masked || status.Reason != "Broken ABI." || status.Source != "/repo/profiles/package.mask" {
		t.Fatalf("status = %#v", status)
	}
}

func TestParsePackageMask_Directory(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "package.mask")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(pkgDir, "01"), []byte(">=dev-lang/python-3.12\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "02"), []byte("# comment\n<app-editors/vim-9\n"), 0644); err != nil {
		t.Fatal(err)
	}

	atoms, err := ParsePackageMask(pkgDir)
	if err != nil {
		t.Fatalf("ParsePackageMask directory: %v", err)
	}

	if len(atoms) != 2 {
		t.Fatalf("expected 2 atoms, got %d: %v", len(atoms), atoms)
	}
}

func TestParsePackageUnmask_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.unmask")
	content := ">=dev-lang/python-3.12\n<app-editors/vim-9\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	atoms, err := ParsePackageUnmask(path)
	if err != nil {
		t.Fatalf("ParsePackageUnmask: %v", err)
	}

	want := []string{">=dev-lang/python-3.12", "<app-editors/vim-9"}
	if !reflect.DeepEqual(atoms, want) {
		t.Errorf("ParsePackageUnmask = %v, want %v", atoms, want)
	}
}

func TestParsePackageUnmask_Comments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.unmask")
	content := "# Header comment\n>=dev-lang/python-3.12\n# Middle comment\n<app-editors/vim-9\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	atoms, err := ParsePackageUnmask(path)
	if err != nil {
		t.Fatalf("ParsePackageUnmask: %v", err)
	}

	if len(atoms) != 2 {
		t.Fatalf("expected 2 atoms, got %d: %v", len(atoms), atoms)
	}
}

func TestParsePackageEnv_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.env")
	content := "dev-lang/python python3.env\napp-editors/vim vim-no-x.env\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParsePackageEnv(path)
	if err != nil {
		t.Fatalf("ParsePackageEnv: %v", err)
	}

	want := map[string]string{
		"dev-lang/python": "python3.env",
		"app-editors/vim": "vim-no-x.env",
	}

	if !reflect.DeepEqual(m, want) {
		t.Errorf("ParsePackageEnv =\n  got:  %v\n  want: %v", m, want)
	}
}

func TestParsePackageEnv_MissingFile(t *testing.T) {
	m, err := ParsePackageEnv("/nonexistent/package.env")
	if err != nil {
		t.Fatalf("ParsePackageEnv should not error on missing: %v", err)
	}
	if m != nil {
		t.Errorf("expected nil map, got %v", m)
	}
}

func TestPackageEnvironmentForOrderedFullAtomRules(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "env"), 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"common":  "USE=\"ssl test\"\nFEATURES=\"sandbox\"\nCFLAGS=\"-O2\"\n",
		"python":  "USE=\"-test ensurepip\"\nFEATURES=\"userpriv\"\nCFLAGS=\"${CFLAGS} -pipe\"\n",
		"testing": "ACCEPT_KEYWORDS=\"~amd64\"\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, "env", name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &Config{ConfigRoot: root, PackageEnvRules: []PackageUseRule{
		{Atom: "*/*", Flags: []string{"common"}},
		{Atom: "dev-lang/python", Flags: []string{"python"}},
		{Atom: ">=dev-lang/python-3.13::gentoo", Flags: []string{"testing"}},
	}}
	got, err := cfg.PackageEnvironmentFor("dev-lang/python-3.13.2", "3.13", "gentoo")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"USE": "ssl -test ensurepip", "FEATURES": "sandbox userpriv",
		"CFLAGS": "-O2 -pipe", "ACCEPT_KEYWORDS": "~amd64",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PackageEnvironmentFor() = %#v, want %#v", got, want)
	}
}

func TestPackageEnvironmentForRejectsTraversal(t *testing.T) {
	cfg := &Config{ConfigRoot: t.TempDir(), PackageEnvRules: []PackageUseRule{{Atom: "*/*", Flags: []string{"../make.conf"}}}}
	if _, err := cfg.PackageEnvironmentFor("dev-lang/python-3.13", "3.13", "gentoo"); err == nil {
		t.Fatal("expected package.env traversal to fail")
	}
}

func TestReadConfigFile_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.conf")
	content := "# comment\nline1\n\nline2\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	lines, err := ReadConfigFile(path)
	if err != nil {
		t.Fatalf("ReadConfigFile: %v", err)
	}

	want := []string{"line1", "line2"}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("ReadConfigFile = %v, want %v", lines, want)
	}
}

func TestReadConfigFile_Directory(t *testing.T) {
	dir := t.TempDir()
	confDir := filepath.Join(dir, "config.d")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(confDir, "a.conf"), []byte("a1\na2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "b.conf"), []byte("b1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	lines, err := ReadConfigFile(confDir)
	if err != nil {
		t.Fatalf("ReadConfigFile: %v", err)
	}

	sort.Strings(lines)
	want := []string{"a1", "a2", "b1"}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("ReadConfigFile = %v, want %v", lines, want)
	}
}

func TestReadConfigFile_DirectorySkipsHidden(t *testing.T) {
	dir := t.TempDir()
	confDir := filepath.Join(dir, "config.d")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(confDir, "visible"), []byte("visible\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confDir, ".hidden"), []byte("hidden\n"), 0644); err != nil {
		t.Fatal(err)
	}

	lines, err := ReadConfigFile(confDir)
	if err != nil {
		t.Fatalf("ReadConfigFile: %v", err)
	}

	if len(lines) != 1 || lines[0] != "visible" {
		t.Errorf("ReadConfigFile = %v, want [visible]", lines)
	}
}

func TestReadConfigFile_DirectorySkipsSubdirs(t *testing.T) {
	dir := t.TempDir()
	confDir := filepath.Join(dir, "config.d")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(confDir, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(confDir, "file"), []byte("file\n"), 0644); err != nil {
		t.Fatal(err)
	}

	lines, err := ReadConfigFile(confDir)
	if err != nil {
		t.Fatalf("ReadConfigFile: %v", err)
	}

	if len(lines) != 1 || lines[0] != "file" {
		t.Errorf("ReadConfigFile = %v, want [file]", lines)
	}
}

func TestReadConfigFile_MissingPath(t *testing.T) {
	lines, err := ReadConfigFile("/nonexistent/path")
	if err != nil {
		t.Fatalf("ReadConfigFile should not error on missing: %v", err)
	}
	if lines != nil {
		t.Errorf("expected nil for missing path, got %v", lines)
	}
}

func TestReadConfigFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.conf")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	lines, err := ReadConfigFile(path)
	if err != nil {
		t.Fatalf("ReadConfigFile: %v", err)
	}
	if lines != nil {
		t.Errorf("expected nil for empty file, got %v", lines)
	}
}

func TestLoadConfig_MissingConfigRoot(t *testing.T) {
	cfg, err := LoadConfig("/nonexistent/portage/root")
	if err != nil {
		t.Fatalf("LoadConfig should not error on missing root: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if len(cfg.MakeConf) != 0 {
		t.Errorf("expected empty MakeConf, got %v", cfg.MakeConf)
	}
	if cfg.USE != nil {
		t.Errorf("expected nil USE, got %v", cfg.USE)
	}
}

func TestLoadConfig_FullConfig(t *testing.T) {
	dir := t.TempDir()

	makeConfContent := `CFLAGS="-O2 -pipe"
CXXFLAGS="${CFLAGS}"
MAKEOPTS="-j8"
USE="X ssl -qt5"
ACCEPT_KEYWORDS="~amd64"
ACCEPT_LICENSE="* -@EULA"
FEATURES="ccache parallel-install"
`
	if err := os.WriteFile(filepath.Join(dir, "make.conf"), []byte(makeConfContent), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "package.use"), []byte("dev-lang/python ssl sqlite\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.accept_keywords"), []byte("dev-lang/python ~amd64\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.license"), []byte("dev-lang/oracle-jdk-bin Oracle-BCLA-JavaSE\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.mask"), []byte(">=dev-lang/python-3.12\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.unmask"), []byte("<app-editors/vim-9\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.env"), []byte("dev-lang/python python3.env\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.CFLAGS != "-O2 -pipe" {
		t.Errorf("CFLAGS = %q", cfg.CFLAGS)
	}
	if cfg.CXXFLAGS != "-O2 -pipe" {
		t.Errorf("CXXFLAGS = %q, want resolved from CFLAGS", cfg.CXXFLAGS)
	}
	if cfg.MAKEOPTS != "-j8" {
		t.Errorf("MAKEOPTS = %q", cfg.MAKEOPTS)
	}

	wantUse := []string{"X", "ssl", "-qt5"}
	if !reflect.DeepEqual(cfg.USE, wantUse) {
		t.Errorf("USE = %v, want %v", cfg.USE, wantUse)
	}

	wantKW := []string{"~amd64"}
	if !reflect.DeepEqual(cfg.ACCEPT_KEYWORDS, wantKW) {
		t.Errorf("ACCEPT_KEYWORDS = %v", cfg.ACCEPT_KEYWORDS)
	}

	wantLic := []string{"*", "-@EULA"}
	if !reflect.DeepEqual(cfg.ACCEPT_LICENSE, wantLic) {
		t.Errorf("ACCEPT_LICENSE = %v", cfg.ACCEPT_LICENSE)
	}

	wantFeat := []string{"ccache", "parallel-install"}
	if !reflect.DeepEqual(cfg.FEATURES, wantFeat) {
		t.Errorf("FEATURES = %v", cfg.FEATURES)
	}

	wantPkgUse := map[string][]string{
		"dev-lang/python": {"ssl", "sqlite"},
	}
	if !reflect.DeepEqual(cfg.PackageUse, wantPkgUse) {
		t.Errorf("PackageUse = %v", cfg.PackageUse)
	}

	if cfg.PackageAcceptKeywords["dev-lang/python"] != "~amd64" {
		t.Errorf("PackageAcceptKeywords = %v", cfg.PackageAcceptKeywords)
	}

	if cfg.PackageLicense["dev-lang/oracle-jdk-bin"] != "Oracle-BCLA-JavaSE" {
		t.Errorf("PackageLicense = %v", cfg.PackageLicense)
	}

	wantMask := []string{">=dev-lang/python-3.12"}
	if !reflect.DeepEqual(cfg.PackageMask, wantMask) {
		t.Errorf("PackageMask = %v", cfg.PackageMask)
	}

	wantUnmask := []string{"<app-editors/vim-9"}
	if !reflect.DeepEqual(cfg.PackageUnmask, wantUnmask) {
		t.Errorf("PackageUnmask = %v", cfg.PackageUnmask)
	}

	if cfg.PackageEnv["dev-lang/python"] != "python3.env" {
		t.Errorf("PackageEnv = %v", cfg.PackageEnv)
	}
}

func TestLoadConfig_NoMakeConf(t *testing.T) {
	dir := t.TempDir()

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig without make.conf: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if len(cfg.MakeConf) != 0 {
		t.Errorf("expected empty MakeConf, got %d entries", len(cfg.MakeConf))
	}
}

func TestLoadConfig_OnlyMakeConf(t *testing.T) {
	dir := t.TempDir()
	content := `USE="pulseaudio alsa"
MAKEOPTS="-j4"
`
	if err := os.WriteFile(filepath.Join(dir, "make.conf"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	wantUse := []string{"pulseaudio", "alsa"}
	if !reflect.DeepEqual(cfg.USE, wantUse) {
		t.Errorf("USE = %v", cfg.USE)
	}
	if cfg.MAKEOPTS != "-j4" {
		t.Errorf("MAKEOPTS = %q", cfg.MAKEOPTS)
	}
}

func TestLoadConfig_DirectoryBasedPackageFiles(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "make.conf"), []byte("USE=\"alsa\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	files := map[string]map[string]string{
		"package.use":              {"10-base": "dev-lang/python ssl\n", "20-local": "dev-lang/python -ssl sqlite\n"},
		"package.accept_keywords":  {"python": "dev-lang/python ~amd64\n"},
		"package.license":          {"python": "dev-lang/python PSF-2\n"},
		"package.mask":             {"python": ">=dev-lang/python-3.12\n"},
		"package.unmask":           {"python": "=dev-lang/python-3.12\n"},
		"profile/package.provided": {"python": "dev-lang/python-3.11\n"},
		"package.env":              {"python": "dev-lang/python python.conf\n"},
	}
	for family, entries := range files {
		path := filepath.Join(dir, family)
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
		for name, content := range entries {
			if err := os.WriteFile(filepath.Join(path, name), []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if !reflect.DeepEqual(cfg.PackageUse["dev-lang/python"], []string{"ssl", "-ssl", "sqlite"}) {
		t.Errorf("PackageUse python = %v", cfg.PackageUse["dev-lang/python"])
	}
	if got := cfg.PackageUseRules; !reflect.DeepEqual(got, []PackageUseRule{
		{Atom: "dev-lang/python", Flags: []string{"ssl"}},
		{Atom: "dev-lang/python", Flags: []string{"-ssl", "sqlite"}},
	}) {
		t.Errorf("ordered PackageUseRules = %#v", got)
	}
	if cfg.PackageAcceptKeywords["dev-lang/python"] != "~amd64" || cfg.PackageLicense["dev-lang/python"] != "PSF-2" {
		t.Errorf("keyword/license directories not loaded: %#v %#v", cfg.PackageAcceptKeywords, cfg.PackageLicense)
	}
	if !reflect.DeepEqual(cfg.PackageMask, []string{">=dev-lang/python-3.12"}) {
		t.Errorf("PackageMask = %v", cfg.PackageMask)
	}
	if !reflect.DeepEqual(cfg.PackageUnmask, []string{"=dev-lang/python-3.12"}) ||
		!reflect.DeepEqual(cfg.PackageProvided, []string{"dev-lang/python-3.11"}) ||
		cfg.PackageEnv["dev-lang/python"] != "python.conf" {
		t.Errorf("atom/env directories not loaded: unmask=%v provided=%v env=%v", cfg.PackageUnmask, cfg.PackageProvided, cfg.PackageEnv)
	}
}

func TestAdversarial_HugeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.use")

	var sb strings.Builder
	for i := 0; i < 50000; i++ {
		sb.WriteString("sys-apps/pkg")
		sb.WriteString(formatInt(i))
		sb.WriteString(" flag_a flag_b flag_c\n")
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParsePackageUse(path)
	if err != nil {
		t.Fatalf("ParsePackageUse huge file: %v", err)
	}
	if len(m) != 50000 {
		t.Errorf("expected 50000 entries, got %d", len(m))
	}
}

func formatInt(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	n := i
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func TestAdversarial_NullBytesInConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.use")
	content := "dev-lang/python\x00 ssl sqlite\napp-editors/vim -X\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ParsePackageUse(path)
	if err != nil {
		t.Fatalf("ParsePackageUse with null bytes should not crash: %v", err)
	}
}

func TestAdversarial_NullBytesInMakeConf(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "make.conf")
	content := "USE=\"\x00\"\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ParseMakeConf(path)
	if err != nil {
		t.Fatalf("ParseMakeConf with null bytes should not crash: %v", err)
	}
}

func TestAdversarial_DeeplyNestedShellExpansions(t *testing.T) {
	m := make(map[string]string)
	const depth = 120
	m["ROOT"] = "final-value"
	prev := "ROOT"
	for i := 1; i < depth; i++ {
		key := keyFor(i)
		m[key] = "${" + prev + "}"
		prev = key
	}

	ResolveMakeConfRefs(m)

	for i := 1; i < depth; i++ {
		if m[keyFor(i)] != "final-value" {
			t.Errorf("depth %d: %s = %q, want \"final-value\"", i, keyFor(i), m[keyFor(i)])
			break
		}
	}
}

func TestAdversarial_VeryLongLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.use")

	longFlag := strings.Repeat("x", 100000)
	content := "dev-lang/python " + longFlag + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParsePackageUse(path)
	if err != nil {
		t.Errorf("ParsePackageUse long line: %v", err)
	}
	if len(m) != 1 {
		t.Errorf("expected 1 entry, got %d", len(m))
	}
}

func TestMutation_ByteFlipMakeConf(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "make.conf")
	valid := `CFLAGS="-O2 -pipe"
MAKEOPTS="-j8"
USE="X ssl"
`
	if err := os.WriteFile(path, []byte(valid), 0644); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < len(valid); i++ {
		mutated := []byte(valid)
		mutated[i] ^= 0xFF
		if err := os.WriteFile(path, mutated, 0644); err != nil {
			t.Fatal(err)
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("ParseMakeConf panicked on byte-flip at position %d: %v", i, r)
				}
			}()
			_, _ = ParseMakeConf(path)
		}()
	}
}

func TestMutation_ByteFlipPackageUseConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.use")
	valid := "dev-lang/python ssl sqlite\napp-editors/vim -X\n"
	if err := os.WriteFile(path, []byte(valid), 0644); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < len(valid); i++ {
		mutated := []byte(valid)
		mutated[i] ^= 0xFF
		if err := os.WriteFile(path, mutated, 0644); err != nil {
			t.Fatal(err)
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("ParsePackageUse panicked on byte-flip at position %d: %v", i, r)
				}
			}()
			_, _ = ParsePackageUse(path)
		}()
	}
}

func TestMutation_ByteFlipReadConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.d")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}

	valid := "line1\nline2\n"
	filePath := filepath.Join(path, "test")
	if err := os.WriteFile(filePath, []byte(valid), 0644); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < len(valid); i++ {
		mutated := []byte(valid)
		mutated[i] ^= 0xFF
		if err := os.WriteFile(filePath, mutated, 0644); err != nil {
			t.Fatal(err)
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("ReadConfigFile panicked on byte-flip at position %d: %v", i, r)
				}
			}()
			_, _ = ReadConfigFile(path)
		}()
	}
}

func TestProperty_ParseAtomConfigIdempotent(t *testing.T) {
	lines := []string{
		"dev-lang/python ssl sqlite",
		"app-editors/vim -X",
		"*/* python_targets_python3_11",
		">=sys-devel/gcc-12.2.0 fortran",
	}

	for _, line := range lines {
		atom, val := parseAtomConfig(line)
		if atom+val == "" && line != "" {
			t.Errorf("parseAtomConfig(%q) dropped data", line)
		}
		if atomTag, valTag := parseAtomConfig(line); atomTag != atom || valTag != val {
			t.Errorf("parseAtomConfig(%q) not idempotent: first=(%q,%q) second=(%q,%q)",
				line, atom, val, atomTag, valTag)
		}
	}
}

func TestProperty_ResolveMakeConfRefs_Idempotent(t *testing.T) {
	m := map[string]string{
		"CFLAGS":   "-O2 -pipe",
		"CXXFLAGS": "${CFLAGS}",
		"OPTIMIZE": "${CXXFLAGS} -ftree-vectorize",
	}

	ResolveMakeConfRefs(m)

	v1 := m["OPTIMIZE"]
	ResolveMakeConfRefs(m)
	v2 := m["OPTIMIZE"]

	if v1 != v2 {
		t.Errorf("ResolveMakeConfRefs not idempotent: %q vs %q", v1, v2)
	}
}

func TestProperty_ReadConfigFile_DeterministicOrder(t *testing.T) {
	dir := t.TempDir()
	confDir := filepath.Join(dir, "config.d")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatal(err)
	}

	files := []string{"c.conf", "a.conf", "b.conf"}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(confDir, name), []byte(name+"-content\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	first, err := ReadConfigFile(confDir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReadConfigFile(confDir)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Errorf("ReadConfigFile not deterministic: first=%v, second=%v", first, second)
	}
}

func TestPropertyConfigurationReductionDeterministic(t *testing.T) {
	wantUse := map[string]bool{"ssl": true, "test": false, "ensurepip": true}
	wantKeywords := []string{"~amd64", "-~amd64", "~amd64"}
	wantLicenses := []string{"-*", "PSF-2"}
	for seed := int64(0); seed < 100; seed++ {
		rng := rand.New(rand.NewSource(seed))
		makeConf := make(map[string]string)
		entries := [][2]string{{"ARCH", "amd64"}, {"USE", "ssl test"}, {"ACCEPT_LICENSE", "-*"}}
		rng.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
		for _, entry := range entries {
			makeConf[entry[0]] = entry[1]
		}
		cfg := &Config{
			MakeConf: makeConf, USE: []string{"ssl", "test"}, UseStableMask: []string{"test"},
			PackageUseStableForceRules: []PackageUseRule{{Atom: "dev-lang/python", Flags: []string{"ensurepip"}}},
			PackageAcceptKeywordRules: []PackageUseRule{
				{Atom: "dev-lang/python", Flags: []string{"~amd64"}},
				{Atom: ">=dev-lang/python-3.13", Flags: []string{"-~amd64"}},
				{Atom: "=dev-lang/python-3.13.2", Flags: []string{"~amd64"}},
			},
			PackageLicenseRules: []PackageUseRule{{Atom: "dev-lang/python", Flags: []string{"-*", "PSF-2"}}},
		}
		if got := cfg.EffectiveUseForStability("dev-lang/python-3.13.2", "3.13", "gentoo", true); !reflect.DeepEqual(got, wantUse) {
			t.Fatalf("seed %d effective USE = %#v", seed, got)
		}
		if got := cfg.PackageAcceptKeywordsFor("dev-lang/python-3.13.2", "3.13", "gentoo", "amd64"); !reflect.DeepEqual(got, wantKeywords) {
			t.Fatalf("seed %d keywords = %#v", seed, got)
		}
		if got := cfg.PackageLicensesFor("dev-lang/python-3.13.2", "3.13", "gentoo"); !reflect.DeepEqual(got, wantLicenses) {
			t.Fatalf("seed %d licenses = %#v", seed, got)
		}
	}
}

func TestAtomicity_ConfigRoundtrip(t *testing.T) {
	dir := t.TempDir()

	makeConfContent := `CFLAGS="-O2 -pipe -march=native"
CXXFLAGS="${CFLAGS}"
MAKEOPTS="-j8"
USE="X ssl -qt5"
ACCEPT_KEYWORDS="~amd64"
`
	if err := os.WriteFile(filepath.Join(dir, "make.conf"), []byte(makeConfContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.CFLAGS != "-O2 -pipe -march=native" {
		t.Errorf("CFLAGS = %q", cfg.CFLAGS)
	}
	if cfg.CXXFLAGS != "-O2 -pipe -march=native" {
		t.Errorf("CXXFLAGS = %q", cfg.CXXFLAGS)
	}
	if cfg.MAKEOPTS != "-j8" {
		t.Errorf("MAKEOPTS = %q", cfg.MAKEOPTS)
	}

	wantUse := []string{"X", "ssl", "-qt5"}
	if !reflect.DeepEqual(cfg.USE, wantUse) {
		t.Errorf("USE = %v, want %v", cfg.USE, wantUse)
	}

	wantKW := []string{"~amd64"}
	if !reflect.DeepEqual(cfg.ACCEPT_KEYWORDS, wantKW) {
		t.Errorf("ACCEPT_KEYWORDS = %v, want %v", cfg.ACCEPT_KEYWORDS, wantKW)
	}
}

func TestUnquote(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{`"hello"`, "hello"},
		{`'hello'`, "hello"},
		{`no quotes`, "no quotes"},
		{`"hello world"`, "hello world"},
		{`'hello world'`, "hello world"},
		{``, ""},
		{`"`, `"`},
		{`'`, `'`},
		{`"--jobs=4 --load-average=8"`, "--jobs=4 --load-average=8"},
	}

	for _, tt := range tests {
		got := unquote(tt.input)
		if got != tt.want {
			t.Errorf("unquote(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseMakeConf_BackslashContinuation_Complex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "make.conf")
	content := `EMERGE_DEFAULT_OPTS="\
--jobs=4 \
--load-average=8 \
--keep-going \
"
USE="X \
ssl \
-qt5"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParseMakeConf(path)
	if err != nil {
		t.Fatalf("ParseMakeConf: %v", err)
	}

	wantOpts := "--jobs=4 --load-average=8 --keep-going "
	if m["EMERGE_DEFAULT_OPTS"] != wantOpts {
		t.Errorf("EMERGE_DEFAULT_OPTS = %q, want %q", m["EMERGE_DEFAULT_OPTS"], wantOpts)
	}

	wantUse := "X ssl -qt5"
	if m["USE"] != wantUse {
		t.Errorf("USE = %q, want %q", m["USE"], wantUse)
	}
}

func TestParseMakeConf_EscapedBackslash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "make.conf")
	content := `VAR="value with \\ backslash"
OTHER="normal"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParseMakeConf(path)
	if err != nil {
		t.Fatalf("ParseMakeConf: %v", err)
	}

	if m["OTHER"] != "normal" {
		t.Errorf("OTHER = %q, want 'normal'", m["OTHER"])
	}
}

func TestResolveMakeConfRefs_UnknownVar(t *testing.T) {
	m := map[string]string{
		"CFLAGS": "${UNKNOWN} -O2",
	}

	ResolveMakeConfRefs(m)

	if m["CFLAGS"] != "${UNKNOWN} -O2" {
		t.Errorf("CFLAGS = %q, want %q", m["CFLAGS"], "${UNKNOWN} -O2")
	}
}

func TestParseBinhostConfig(t *testing.T) {
	cfg := &Config{
		MakeConf: map[string]string{
			"PORTAGE_BINHOST": "https://example.com/binpkgs/ https://mirror.example.com/binpkgs/",
		},
	}

	urls := ParseBinhostConfig(cfg)
	if len(urls) != 2 {
		t.Fatalf("expected 2 URLs, got %d: %v", len(urls), urls)
	}
	if urls[0] != "https://example.com/binpkgs/" {
		t.Errorf("urls[0] = %q", urls[0])
	}
	if urls[1] != "https://mirror.example.com/binpkgs/" {
		t.Errorf("urls[1] = %q", urls[1])
	}
}

func TestParseBinhostConfig_Empty(t *testing.T) {
	cfg := &Config{
		MakeConf: map[string]string{},
	}
	urls := ParseBinhostConfig(cfg)
	if urls != nil {
		t.Errorf("expected nil, got %v", urls)
	}
}

func TestParseBinhostConfig_NilConfig(t *testing.T) {
	urls := ParseBinhostConfig(nil)
	if urls != nil {
		t.Errorf("expected nil, got %v", urls)
	}
}

func TestParseBinhostConfig_SingleURL(t *testing.T) {
	cfg := &Config{
		MakeConf: map[string]string{
			"PORTAGE_BINHOST": "https://binhost.example.com/packages",
		},
	}
	urls := ParseBinhostConfig(cfg)
	if len(urls) != 1 {
		t.Fatalf("expected 1 URL, got %d", len(urls))
	}
	if urls[0] != "https://binhost.example.com/packages" {
		t.Errorf("url = %q", urls[0])
	}
}

func TestParseBinhostConfig_MissingKey(t *testing.T) {
	cfg := &Config{
		MakeConf: map[string]string{
			"USE": "foo bar",
		},
	}
	urls := ParseBinhostConfig(cfg)
	if urls != nil {
		t.Errorf("expected nil, got %v", urls)
	}
}

func TestParsePackageProvided_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.provided")
	content := "dev-lang/python-3.11\napp-editors/vim-9.0\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	atoms, err := ParsePackageProvided(path)
	if err != nil {
		t.Fatalf("ParsePackageProvided: %v", err)
	}

	want := []string{"dev-lang/python-3.11", "app-editors/vim-9.0"}
	if !reflect.DeepEqual(atoms, want) {
		t.Errorf("ParsePackageProvided = %v, want %v", atoms, want)
	}
}

func TestParsePackageProvided_Comments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.provided")
	content := "# external packages\ndev-lang/python-3.11\n# another\napp-editors/vim-9.0\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	atoms, err := ParsePackageProvided(path)
	if err != nil {
		t.Fatalf("ParsePackageProvided: %v", err)
	}

	if len(atoms) != 2 {
		t.Fatalf("expected 2 atoms, got %d: %v", len(atoms), atoms)
	}
}

func TestParsePackageProvided_MissingFile(t *testing.T) {
	atoms, err := ParsePackageProvided("/nonexistent/package.provided")
	if err != nil {
		t.Fatalf("ParsePackageProvided should not error on missing: %v", err)
	}
	if atoms != nil {
		t.Errorf("expected nil for missing file, got %v", atoms)
	}
}

func TestParsePackageProvided_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.provided")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	atoms, err := ParsePackageProvided(path)
	if err != nil {
		t.Fatalf("ParsePackageProvided: %v", err)
	}
	if atoms != nil {
		t.Errorf("expected nil for empty file, got %v", atoms)
	}
}

func TestLoadConfig_WithPackageProvided(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "make.conf"), []byte("USE=\"alsa\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	profDir := filepath.Join(dir, "profile")
	if err := os.MkdirAll(profDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profDir, "package.provided"), []byte("dev-lang/python-3.11\napp-editors/vim-9.0\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	want := []string{"dev-lang/python-3.11", "app-editors/vim-9.0"}
	if !reflect.DeepEqual(cfg.PackageProvided, want) {
		t.Errorf("PackageProvided = %v, want %v", cfg.PackageProvided, want)
	}
}

func TestLoadEffectiveConfigMergesActiveProfileAndUserConfig(t *testing.T) {
	root := t.TempDir()
	profiles := filepath.Join(root, "profiles")
	base := filepath.Join(profiles, "base")
	leaf := filepath.Join(profiles, "leaf")
	for _, dir := range []string{base, leaf} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "make.defaults"), []byte("USE=\"profile ssl\"\nUSE_ORDER=\"env:pkg:conf:defaults:pkginternal:repo:env.d\"\nUSE_EXPAND=\"ABI_X86 L10N\"\nUSE_EXPAND_HIDDEN=\"ABI_X86\"\nCFLAGS=\"-O1\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "use.force"), []byte("forced\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "use.mask"), []byte("masked\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "package.use.force"), []byte("app-editors/vim python\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "parent"), []byte("../base\n"), 0644); err != nil {
		t.Fatal(err)
	}
	configRoot := filepath.Join(root, "etc-portage")
	if err := os.MkdirAll(configRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(leaf, filepath.Join(configRoot, "make.profile")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configRoot, "make.conf"), []byte("CFLAGS=\"-O2\"\nUSE=\"user\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadEffectiveConfig(configRoot)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProfilePath != leaf || cfg.CFLAGS != "-O2" || !reflect.DeepEqual(cfg.USE, []string{"profile", "ssl", "user", "forced", "-masked"}) {
		t.Fatalf("effective config = %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.UseForce, []string{"forced"}) || !reflect.DeepEqual(cfg.UseMask, []string{"masked"}) {
		t.Fatalf("profile USE policy = %+v/%+v", cfg.UseForce, cfg.UseMask)
	}
	if !reflect.DeepEqual(cfg.PackageUseForce["app-editors/vim"], []string{"python"}) {
		t.Fatalf("package force = %+v", cfg.PackageUseForce)
	}
	if !reflect.DeepEqual(cfg.UseExpand, []string{"ABI_X86", "L10N"}) || !reflect.DeepEqual(cfg.UseExpandHidden, []string{"ABI_X86"}) {
		t.Fatalf("USE_EXPAND = %v hidden=%v", cfg.UseExpand, cfg.UseExpandHidden)
	}
}

func TestEffectiveGlobalsIncludeGentooMirrors(t *testing.T) {
	if !effectiveGlobalVariables["GENTOO_MIRRORS"] {
		t.Fatal("effective configuration drops the make.globals Gentoo mirror fallback")
	}
}

func TestApplyCommandEnvironmentIsAllowlistedAndOrdered(t *testing.T) {
	cfg := &Config{
		MakeConf:      map[string]string{"USE": "base old", "FEATURES": "sandbox test", "ACCEPT_LICENSE": "@FREE"},
		UseForce:      []string{"forced"},
		UseMask:       []string{"masked"},
		LicenseGroups: map[string][]string{},
	}
	cfg.populateAccessors()
	cfg.ApplyCommandEnvironment([]string{
		"USE=-old temporary masked",
		"FEATURES=-test network-sandbox",
		"ACCEPT_LICENSE=-@FREE @BINARY-REDISTRIBUTABLE",
		"MAKEOPTS=-j3",
		"ROOT=/target",
		"ARBITRARY_SECRET=must-not-enter-config",
	})

	if got := strings.Join(cfg.USE, " "); got != "base -old temporary -masked forced" {
		t.Fatalf("USE = %q", got)
	}
	if got := strings.Join(cfg.FEATURES, " "); got != "sandbox -test network-sandbox" {
		t.Fatalf("FEATURES = %q", got)
	}
	if got := strings.Join(cfg.ACCEPT_LICENSE, " "); got != "-@FREE @BINARY-REDISTRIBUTABLE" {
		t.Fatalf("ACCEPT_LICENSE = %q", got)
	}
	if cfg.MAKEOPTS != "-j3" || cfg.MakeConf["ROOT"] != "/target" {
		t.Fatalf("command controls not applied: %#v", cfg.MakeConf)
	}
	if _, exists := cfg.MakeConf["ARBITRARY_SECRET"]; exists {
		t.Fatal("non-allowlisted environment variable entered configuration")
	}
}

func TestPackageExecutionEnvironmentLayerPrecedence(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "env"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "env", "package.conf"), []byte("USE=\"-base package\"\nFEATURES=\"-test package-feature\"\nCFLAGS=\"-O3\"\nPACKAGE_ONLY=yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		ConfigRoot: root,
		MakeConf: map[string]string{
			"USE": "base old", "FEATURES": "sandbox test", "CFLAGS": "-O2", "MAKEOPTS": "-j2",
			"ABI": "amd64", "DEFAULT_ABI": "amd64", "MULTILIB_ABIS": "amd64 x86",
			"CHOST_amd64": "x86_64-pc-linux-gnu", "CFLAGS_amd64": "-m64",
		},
		PackageEnvRules: []PackageUseRule{{Atom: "dev-lang/python", Flags: []string{"package.conf"}}},
		LicenseGroups:   map[string][]string{},
	}
	cfg.populateAccessors()
	cfg.ApplyCommandEnvironment([]string{"USE=-old command", "FEATURES=-test command-feature", "CFLAGS=-O0", "IGNORED_SECRET=no"})

	got, err := cfg.PackageExecutionEnvironmentFor("dev-lang/python-3.13", "3.13", "gentoo", map[string]string{"MAKEOPTS": "-j8", "REQUEST_ONLY": "yes"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"USE": "-base -old package command", "FEATURES": "sandbox -test package-feature command-feature",
		"CFLAGS": "-O0", "MAKEOPTS": "-j8", "PACKAGE_ONLY": "yes", "REQUEST_ONLY": "yes",
		"ABI": "amd64", "DEFAULT_ABI": "amd64", "MULTILIB_ABIS": "amd64 x86",
		"CHOST_amd64": "x86_64-pc-linux-gnu", "CFLAGS_amd64": "-m64",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("execution environment = %#v, want %#v", got, want)
	}
}

func TestPackageExecutionEnvironmentMaterializesUseExpandVariables(t *testing.T) {
	cfg := &Config{
		MakeConf: map[string]string{
			"USE":        "llvm_targets_AArch64 llvm_targets_X86 -llvm_targets_ARM abi_x86_64",
			"USE_EXPAND": "LLVM_TARGETS ABI_X86",
		},
		UseExpand:     []string{"LLVM_TARGETS", "ABI_X86"},
		LicenseGroups: map[string][]string{},
	}

	got, err := cfg.PackageExecutionEnvironmentFor(
		"llvm-core/llvm-22.1.8",
		"22/22.1",
		"gentoo",
		map[string]string{
			"USE": "llvm_targets_AArch64 llvm_targets_X86 -llvm_targets_ARM abi_x86_64",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got["LLVM_TARGETS"] != "AArch64 X86" {
		t.Fatalf("LLVM_TARGETS = %q, want %q", got["LLVM_TARGETS"], "AArch64 X86")
	}
	if got["ABI_X86"] != "64" {
		t.Fatalf("ABI_X86 = %q, want %q", got["ABI_X86"], "64")
	}
}

func TestCommandEnvironmentAcceptsActiveUseExpandVariables(t *testing.T) {
	cfg := &Config{
		MakeConf: map[string]string{
			"USE":          "llvm_targets_AArch64 abi_x86_32",
			"USE_EXPAND":   "LLVM_TARGETS ABI_X86",
			"LLVM_TARGETS": "AArch64",
			"ABI_X86":      "32",
		},
		UseExpand: []string{"LLVM_TARGETS", "ABI_X86"},
		USE:       []string{"llvm_targets_AArch64", "abi_x86_32"},
	}

	cfg.ApplyCommandEnvironment([]string{"LLVM_TARGETS=X86", "ABI_X86=64", "UNRELATED=value"})
	if cfg.MakeConf["LLVM_TARGETS"] != "X86" || cfg.MakeConf["ABI_X86"] != "64" {
		t.Fatalf("USE_EXPAND command overrides were dropped: %#v", cfg.MakeConf)
	}
	if _, ok := cfg.MakeConf["UNRELATED"]; ok {
		t.Fatal("unrelated command environment variable was accepted")
	}
	use := strings.Join(cfg.USE, " ")
	for _, want := range []string{"llvm_targets_X86", "abi_x86_64"} {
		if !strings.Contains(" "+use+" ", " "+want+" ") {
			t.Fatalf("effective USE does not contain %s: %q", want, use)
		}
	}
	for _, stale := range []string{"llvm_targets_AArch64", "abi_x86_32"} {
		if strings.Contains(" "+use+" ", " "+stale+" ") {
			t.Fatalf("effective USE retained overridden %s: %q", stale, use)
		}
	}
}

func TestPackageExecutionEnvironmentIncludesBuildToolchainVariables(t *testing.T) {
	cfg := &Config{MakeConf: map[string]string{
		"BUILD_CC":        "build-gcc",
		"BUILD_CXX":       "build-g++",
		"BUILD_CFLAGS":    "-O1",
		"PKG_CONFIG":      "pkgconf",
		"RUSTFLAGS":       "-C target-cpu=native",
		"UNRELATED_VALUE": "drop-me",
	}}

	got, err := cfg.PackageExecutionEnvironmentFor("cat/pkg-1", "0", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"BUILD_CC": "build-gcc", "BUILD_CXX": "build-g++", "BUILD_CFLAGS": "-O1",
		"PKG_CONFIG": "pkgconf", "RUSTFLAGS": "-C target-cpu=native",
	} {
		if got[name] != want {
			t.Fatalf("%s = %q, want %q", name, got[name], want)
		}
	}
	if _, ok := got["UNRELATED_VALUE"]; ok {
		t.Fatal("unrelated make.conf value leaked into package environment")
	}
}

func TestLoadEffectiveConfigWithEnvironmentWithoutProfile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "make.conf"), []byte("CFLAGS=-O2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadEffectiveConfigWithEnvironment(root, []string{"CFLAGS=-O0", "ARCH=test-arch", "MAKEOPTS="})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CFLAGS != "-O0" || cfg.MakeConf["ARCH"] != "test-arch" || cfg.MAKEOPTS != "" {
		t.Fatalf("effective command environment not retained: %#v", cfg.MakeConf)
	}
}

func TestLoadEffectiveConfigStacksRepositoryProfileAndUserMasks(t *testing.T) {
	root := t.TempDir()
	profiles := filepath.Join(root, "repo", "profiles")
	base := filepath.Join(profiles, "base")
	leaf := filepath.Join(profiles, "leaf")
	for _, directory := range []string{base, leaf} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(profiles, "package.mask"), []byte("=cat/pkg-1\n=cat/pkg-2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "package.mask"), []byte("-=cat/pkg-1\n=cat/pkg-3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "parent"), []byte("../base\n"), 0644); err != nil {
		t.Fatal(err)
	}
	configRoot := filepath.Join(root, "etc-portage")
	if err := os.MkdirAll(configRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(leaf, filepath.Join(configRoot, "make.profile")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configRoot, "package.mask"), []byte("=cat/pkg-4\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configRoot, "package.unmask"), []byte("=cat/pkg-2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadEffectiveConfig(configRoot)
	if err != nil {
		t.Fatal(err)
	}
	wantMasks := []string{"=cat/pkg-2", "=cat/pkg-3", "=cat/pkg-4"}
	if !reflect.DeepEqual(cfg.PackageMask, wantMasks) {
		t.Fatalf("PackageMask = %v, want %v", cfg.PackageMask, wantMasks)
	}
	if status := cfg.PackageMaskStatus("cat/pkg-2", "0", "gentoo"); status.Masked {
		t.Fatalf("user unmask did not override profile mask: %+v", status)
	}
}

func TestAppendUseExpandIncludesImplicitPlatformGroups(t *testing.T) {
	values := map[string]string{
		"KERNEL": "linux",
		"ELIBC":  "glibc",
	}
	got := appendUseExpand(nil, []string{"KERNEL", "ELIBC"}, values)
	want := []string{"kernel_linux", "elibc_glibc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("implicit USE_EXPAND flags = %v, want %v", got, want)
	}
}
