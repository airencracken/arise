package fetch

import (
	"strings"
	"testing"
)

func TestParseMirrorGroupsPreservesOrder(t *testing.T) {
	groups, err := ParseMirrorGroups(strings.NewReader("gnu https://one/gnu https://two/gnu # fallback\nsourceforge https://sf/\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(groups["gnu"], " "); got != "https://one/gnu https://two/gnu" {
		t.Fatalf("gnu = %q", got)
	}
}

func TestExpandMirrorSource(t *testing.T) {
	cfg := FetchConfig{GentooMirrors: []string{"https://one/distfiles/", "https://two/distfiles"}, MirrorGroups: map[string][]string{"gnu": {"https://gnu.example"}}}
	gentoo, err := expandMirrorSource("mirror://gentoo/path/source.tar", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(gentoo, " "); got != "https://one/distfiles/path/source.tar https://two/distfiles/path/source.tar" {
		t.Fatalf("gentoo = %q", got)
	}
	gnu, err := expandMirrorSource("mirror://gnu/project/source.tar", cfg)
	if err != nil || len(gnu) != 1 || gnu[0] != "https://gnu.example/project/source.tar" {
		t.Fatalf("gnu = %#v, %v", gnu, err)
	}
}

func TestExpandMirrorSourceRejectsUnknownAndUnsupportedGroups(t *testing.T) {
	if _, err := expandMirrorSource("mirror://unknown/source.tar", FetchConfig{}); err == nil {
		t.Fatal("unknown mirror group accepted")
	}
	if _, err := expandMirrorSource("mirror://gnu/source.tar", FetchConfig{MirrorGroups: map[string][]string{"gnu": {"ftp://gnu"}}}); err == nil {
		t.Fatal("unsupported endpoint accepted")
	}
}
