package rebuild

import "testing"

func TestRewriteLaFileMatchesPortageVectors(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"dependency_libs=' /usr/lib64/liba.la /usr/lib64/libb.la -lc'\n", "dependency_libs=' -L/usr/lib64 -la -lb -lc'\n"},
		{"dependency_libs=' /usr/lib64/liba.la -pthread /usr/lib64/libb.la -lc'\ninherited_linker_flags=''\n", "dependency_libs=' -L/usr/lib64 -la -lb -lc'\ninherited_linker_flags=' -pthread'\n"},
		{"dependency_libs=' /usr/lib64/liba.la -R/usr/lib64 /usr/lib64/libb.la -lc'\n", "dependency_libs=' -R/usr/lib64 -L/usr/lib64 -la -lb -lc'\n"},
		{"dependency_libs=' -L/usr/X11R6/lib'\n", "dependency_libs=' -L/usr/lib'\n"},
		{"dependency_libs=' -L/usr/lib64/pkgconfig/../..'\n", "dependency_libs=' -L/usr'\n"},
	} {
		got, changed, err := rewriteLaFile([]byte(test.input))
		if err != nil || !changed || string(got) != test.want {
			t.Errorf("rewrite(%q)=%q changed=%v err=%v want %q", test.input, got, changed, err, test.want)
		}
	}
}

func TestRewriteLaFileRejectsMalformedInput(t *testing.T) {
	for _, input := range []string{"", "dependency_libs=' -lc'\ndependency_libs=' -lm'\n", "dependency_libs=' /-lstdc++'\n"} {
		if _, _, err := rewriteLaFile([]byte(input)); err == nil {
			t.Errorf("malformed input accepted: %q", input)
		}
	}
}
