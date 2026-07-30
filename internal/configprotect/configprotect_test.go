package configprotect

import "testing"

func TestProtectedHonorsBoundariesAndMasks(t *testing.T) {
	protect := []string{"/etc", "/var/lib/app"}
	mask := []string{"/etc/env.d"}
	for _, test := range []struct {
		path string
		want bool
	}{
		{"/etc/hosts", true},
		{"/etc/env.d/00basic", false},
		{"/etcetera/file", false},
		{"/var/lib/app/state", true},
		{"/usr/bin/tool", false},
	} {
		got, err := Protected("/", test.path, protect, mask)
		if err != nil {
			t.Fatalf("Protected(%q): %v", test.path, err)
		}
		if got != test.want {
			t.Errorf("Protected(%q) = %t, want %t", test.path, got, test.want)
		}
	}
}

func TestProtectedRejectsAdversarialPathsOutsideRoot(t *testing.T) {
	for _, path := range []string{"relative", "/srv/root/../../etc/passwd", "/other/file"} {
		if _, err := Protected("/srv/root", path, []string{"/etc"}, nil); err == nil {
			t.Errorf("Protected accepted %q outside root", path)
		}
	}
}

func TestProtectedPropertyMaskAlwaysWins(t *testing.T) {
	for _, path := range []string{"/etc", "/etc/a", "/etc/a/b"} {
		got, err := Protected("/", path, []string{"/"}, []string{"/etc"})
		if err != nil {
			t.Fatal(err)
		}
		if got {
			t.Fatalf("mask did not override protection for %q", path)
		}
	}
}

func TestProtectedMutationPathBoundaryCannotBecomePrefixMatch(t *testing.T) {
	protected, err := Protected("/", "/etcetera/escape", []string{"/etc"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if protected {
		t.Fatal("path boundary mutation treated /etcetera as beneath /etc")
	}
}
