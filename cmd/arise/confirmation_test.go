package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/resolve"
)

func TestInstallAskRequiresAffirmativeAnswer(t *testing.T) {
	for _, ask := range []bool{false, true} {
		for _, input := range []string{"", "no\n", "yes\n", "yesterday\n"} {
			var output strings.Builder
			got := confirmInstall(strings.NewReader(input), &output, ask)
			if want := !ask || input == "yes\n"; got != want {
				t.Fatalf("ask=%t input=%q accepted=%t", ask, input, got)
			}
			if ask != (output.Len() > 0) {
				t.Fatal("prompt did not respect ask option")
			}
		}
	}
}

func TestNoopReadOnlyPlanPreservesResume(t *testing.T) {
	for _, flags := range [][2]bool{{true, false}, {false, true}, {true, true}, {false, false}} {
		path := filepath.Join(t.TempDir(), "resume")
		if err := os.WriteFile(path, []byte("saved plan"), 0600); err != nil {
			t.Fatal(err)
		}
		removeNoopResume(path, flags[0], flags[1])
		_, err := os.Stat(path)
		if readonly := flags[0] || flags[1]; readonly && err != nil || !readonly && !os.IsNotExist(err) {
			t.Fatalf("flags=%v resume status=%v", flags, err)
		}
	}
}

func TestResumeSkipFirstPlanningPreservesCheckpoint(t *testing.T) {
	for _, skip := range []bool{true, false} {
		t.Run(map[bool]string{true: "skip first", false: "all remaining"}[skip], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "resume")
			first, _ := atom.Parse("cat/first-1")
			second, _ := atom.Parse("cat/second-1")
			if err := resolve.SaveResume(path, &resolve.ResolveResult{Install: []resolve.PkgAction{{Atom: first}, {Atom: second}}}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got, favorites, err := loadResumeRequest(path, skip)
			if want := map[bool]int{true: 1, false: 2}[skip]; err != nil || len(got) != want || got[len(got)-1] != "=cat/second-1" || len(favorites) != 0 {
				t.Fatalf("resume request: %v %v %v", got, favorites, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) {
				t.Fatal("planning modified resume state before confirmation and preflight")
			}
		})
	}
}
