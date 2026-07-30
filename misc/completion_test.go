package misc_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandCompletion(t *testing.T) {
	completion, err := filepath.Abs("arise-completion.bash")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		words []string
		want  []string
	}{
		{name: "command", words: []string{"arise", "mai"}, want: []string{"maintain"}},
		{name: "target", words: []string{"arise", "maintain", ""}, want: []string{"world"}},
		{name: "mode", words: []string{"arise", "maintain", "world", "--"}, want: []string{"--check", "--fix"}},
		{name: "filtered mode", words: []string{"arise", "maintain", "world", "--f"}, want: []string{"--fix"}},
		{name: "perl cleaner command", words: []string{"arise", "perl-c"}, want: []string{"perl-cleaner"}},
		{name: "perl cleaner modes", words: []string{"arise", "perl-cleaner", "--all"}, want: []string{"--allmodules", "--all"}},
		{name: "python cleaner command", words: []string{"arise", "python-c"}, want: []string{"python-cleaner"}},
		{name: "python cleaner modes", words: []string{"arise", "python-cleaner", "--"}, want: []string{"--check", "--pretend", "--fix", "--resume"}},
		{name: "removed equery list", words: []string{"arise", "equery", "l"}, want: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{completion}, test.words...)
			command := exec.Command("bash", append([]string{"-c", completionHarness, "completion-test"}, args...)...)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("bash completion failed: %v\n%s", err, output)
			}
			got := strings.Fields(string(output))
			if strings.Join(got, "\n") != strings.Join(test.want, "\n") {
				t.Fatalf("completion = %q, want %q", got, test.want)
			}
		})
	}
}

const completionHarness = `
_init_completion() {
    words=("${COMP_WORDS[@]}")
    cword=$COMP_CWORD
    cur=${COMP_WORDS[COMP_CWORD]}
    if (( COMP_CWORD > 0 )); then
        prev=${COMP_WORDS[COMP_CWORD-1]}
    else
        prev=
    fi
}
completion_file=$1
shift
COMP_WORDS=("$@")
COMP_CWORD=$((${#COMP_WORDS[@]} - 1))
source "$completion_file" || exit 1
_arise || exit 1
printf '%s\n' "${COMPREPLY[@]}"
`
