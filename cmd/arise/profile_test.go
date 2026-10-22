package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/portage"
)

func profileCommandFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	config, repo := filepath.Join(root, "etc", "portage"), filepath.Join(root, "repo")
	profile := filepath.Join(repo, "profiles", "test")
	for _, directory := range []string{config, profile} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string]string{
		filepath.Join(config, "repos.conf"):     "[gentoo]\nlocation = " + repo + "\n",
		filepath.Join(profile, "make.defaults"): "# fixture\nUSE=\"ssl\"\n",
		filepath.Join(profile, "packages"):      "*sys-apps/base\n",
	} {
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(profile, filepath.Join(config, "make.profile")); err != nil {
		t.Fatal(err)
	}
	previous := *portageConfigRoot
	*portageConfigRoot = config
	t.Cleanup(func() { *portageConfigRoot = previous })
	return config
}

func TestProfileShowTextExplanationAndJSON(t *testing.T) {
	profileCommandFixture(t)
	for _, test := range []struct {
		args []string
		json bool
	}{
		{[]string{"show"}, false}, {[]string{"show", "--explain"}, false},
		{[]string{"show", "--json"}, true}, {[]string{"show", "--json", "--explain"}, true},
	} {
		var stdout, stderr bytes.Buffer
		if code := runProfileWithWriters(test.args, &stdout, &stderr); code != 0 {
			t.Fatalf("%v: code %d, error %s", test.args, code, stderr.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("unexpected stderr: %s", &stderr)
		}
		if test.json {
			var report portage.ProfileReport
			decoder := json.NewDecoder(&stdout)
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&report); err != nil {
				t.Fatal(err)
			}
			if report.Schema != "arise.profile.v1" || report.Scope != "profile" || report.Defaults[0].Contributions[0].Source.Line != 2 {
				t.Fatalf("JSON contract = %+v", report)
			}
			continue
		}
		for _, want := range []string{"Scope: profile", "Inheritance (evaluation order)", "USE=\"ssl\"", "sys-apps/base", "ordered rules; evaluated per package"} {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("%v: missing %q in %s", test.args, want, &stdout)
			}
		}
		if got := strings.Contains(stdout.String(), "make.defaults:2:"); got != (len(test.args) == 2) {
			t.Fatalf("explanation output = %s", &stdout)
		}
	}
}

func TestProfileShowRejectsArgumentsBeforeReadingState(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"show", "extra"}, {"show", "--unknown"}, {"show", "--json=garbage"}, {"show", "--explain", "extra"}} {
		var stdout, stderr bytes.Buffer
		if code := runProfileWithWriters(args, &stdout, &stderr); code != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("%v: invalid arguments: code %d, stdout %q, stderr %q", args, code, &stdout, &stderr)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := runProfileWithWriters([]string{"show", "--help"}, &stdout, &stderr); code != 0 || !strings.Contains(stderr.String(), "--explain") {
		t.Fatalf("show help: code %d, stderr %q", code, &stderr)
	}
}

func TestProfileShowReadFailureDoesNotEmitJSON(t *testing.T) {
	config := profileCommandFixture(t)
	if err := os.Remove(filepath.Join(config, "make.profile")); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runProfileWithWriters([]string{"show", "--json"}, &stdout, &stderr); code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "active profile") {
		t.Fatalf("missing profile: code %d, stdout %q, stderr %q", code, &stdout, &stderr)
	}
}

type failingProfileWriter struct{}

func (failingProfileWriter) Write([]byte) (int, error) {
	return 0, errors.New("fixture write failure")
}

func TestProfileShowGlobalOptionsAndWriteFailure(t *testing.T) {
	profileCommandFixture(t)
	previousJSON, previousExplain := *jsonOutput, *explainOutput
	t.Cleanup(func() { *jsonOutput, *explainOutput = previousJSON, previousExplain })
	*jsonOutput, *explainOutput = true, true
	var stdout, stderr bytes.Buffer
	if code := runProfileWithWriters([]string{"show"}, &stdout, &stderr); code != 0 || !json.Valid(stdout.Bytes()) {
		t.Fatalf("global JSON: code %d, stdout %q, stderr %q", code, &stdout, &stderr)
	}
	stdout.Reset()
	if code := runProfileWithWriters([]string{"show", "--json=false"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "make.defaults:2:") {
		t.Fatalf("global explain: code %d, stdout %q, stderr %q", code, &stdout, &stderr)
	}
	if code := runProfileWithWriters([]string{"show"}, failingProfileWriter{}, &stderr); code != 1 || !strings.Contains(stderr.String(), "fixture write failure") {
		t.Fatalf("write failure: code %d, stderr %q", code, &stderr)
	}
}
