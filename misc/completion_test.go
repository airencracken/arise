package misc_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
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
		{name: "installed inspection modes", words: []string{"arise", "installed", "--o"}, want: []string{"--owner"}},
		{name: "query visible mode", words: []string{"arise", "query", "--best"}, want: []string{"--best-visible"}},
		{name: "info repository modes", words: []string{"arise", "info", "--repo"}, want: []string{"--repositories", "--repo-path", "--repository-config"}},
		{name: "inspect command", words: []string{"arise", "inspe"}, want: []string{"inspect"}},
		{name: "inspect modes", words: []string{"arise", "inspect", "--"}, want: []string{"--json", "--strict", "--locked", "--target-kernel="}},
		{name: "news read all", words: []string{"arise", "news", "read", "a"}, want: []string{"all"}},
		{name: "doctor modes", words: []string{"arise", "doctor", "package"}, want: []string{"package-use", "package-policy"}},
		{name: "plan diff modes", words: []string{"arise", "plan-diff", "--"}, want: []string{"--json"}},
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

func TestCompletionCommandsMatchCLIRegistry(t *testing.T) {
	help, err := os.ReadFile("../cmd/arise/help.go")
	if err != nil {
		t.Fatal(err)
	}
	completion, err := os.ReadFile("arise-completion.bash")
	if err != nil {
		t.Fatal(err)
	}
	entryPattern := regexp.MustCompile(`(?m)^\s*"([^"]+)":\s+\{`)
	var registered []string
	for _, match := range entryPattern.FindAllStringSubmatch(string(help), -1) {
		registered = append(registered, match[1])
	}
	commandPattern := regexp.MustCompile(`local commands="([^"]+)"`)
	match := commandPattern.FindStringSubmatch(string(completion))
	if len(match) != 2 {
		t.Fatal("completion command registry not found")
	}
	completed := strings.Fields(match[1])
	sort.Strings(registered)
	sort.Strings(completed)
	if strings.Join(registered, "\n") != strings.Join(completed, "\n") {
		t.Fatalf("completion commands = %v, CLI commands = %v", completed, registered)
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
