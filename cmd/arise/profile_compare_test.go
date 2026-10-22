package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/portage"
)

func profileCompareCommandFixture(t *testing.T) string {
	t.Helper()
	config := profileCommandFixture(t)
	selected, err := os.Readlink(filepath.Join(config, "make.profile"))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(filepath.Dir(selected), "alternate")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{
		filepath.Join(selected, "make.defaults"):               "ARCH=amd64\nUSE=ssl\n",
		filepath.Join(target, "make.defaults"):                 "ARCH=amd64\nUSE=\"ssl X\"\n",
		filepath.Join(target, "package.use"):                   "cat/pkg ssl -debug\n",
		filepath.Join(filepath.Dir(selected), "profiles.desc"): "amd64 test stable\namd64 alternate dev\narm64 alternate exp\n",
	} {
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return config
}

func TestProfileListAndDiffCommands(t *testing.T) {
	profileCompareCommandFixture(t)
	var stdout, stderr bytes.Buffer
	if code := runProfileWithWriters([]string{"list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("catalog code %d: %s", code, &stderr)
	}
	var catalog portage.ProfileCatalog
	if err := json.Unmarshal(stdout.Bytes(), &catalog); err != nil || len(catalog.Profiles) != 2 || catalog.Arch != "amd64" {
		t.Fatalf("catalog = %+v: %v", catalog, err)
	}
	for _, args := range [][]string{
		{"diff", "--json", "gentoo:alternate"},
		{"diff", "--json", "active", "gentoo:alternate"},
		{"diff", "--json", "gentoo:test", "gentoo:alternate"},
	} {
		stdout.Reset()
		if code := runProfileWithWriters(args, &stdout, &stderr); code != 0 {
			t.Fatalf("%v code %d: %s", args, code, &stderr)
		}
		var diff portage.ProfileDiff
		decoder := json.NewDecoder(&stdout)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&diff); err != nil || diff.Schema != "arise.profile-diff.v1" || len(diff.Defaults) != 1 || diff.Defaults[0].Name != "USE" {
			t.Fatalf("diff = %+v: %v", diff, err)
		}
	}
	stdout.Reset()
	if code := runProfileWithWriters([]string{"diff", "--explain", "gentoo:alternate"}, &stdout, &stderr); code != 0 {
		t.Fatalf("explanation code %d: %s", code, &stderr)
	}
	for _, want := range []string{"Before", "After", "+  X", "cat/pkg ssl -debug", "make.defaults:2", "SOURCE"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("diff explanation missing %q:\n%s", want, &stdout)
		}
	}
}

func TestProfileCompareCommandsRejectInvalidArgumentsAndReportFailures(t *testing.T) {
	profileCompareCommandFixture(t)
	for _, args := range [][]string{{"diff"}, {"diff", "a", "b", "c"}, {"diff", "--unknown"}, {"list", "unexpected"}, {"list", "--unknown"}} {
		var stdout, stderr bytes.Buffer
		if code := runProfileWithWriters(args, &stdout, &stderr); code != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("%v code %d, stdout %q, stderr %q", args, code, &stdout, &stderr)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := runProfileWithWriters([]string{"diff", "--json", "gentoo:missing"}, &stdout, &stderr); code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "after profile") {
		t.Fatalf("failed comparison: code %d, stdout %q, stderr %q", code, &stdout, &stderr)
	}
	for _, command := range []string{"list", "diff"} {
		stdout.Reset()
		stderr.Reset()
		if code := runProfileWithWriters([]string{command, "--help"}, &stdout, &stderr); code != 0 || !strings.Contains(stderr.String(), "arise profile "+command) {
			t.Fatalf("%s help: code %d, %s", command, code, &stderr)
		}
	}
}

func TestProfileComparisonColorsAndMachineOutput(t *testing.T) {
	profileCompareCommandFixture(t)
	previousColor, previousJSON := color.UseColor, *jsonOutput
	t.Cleanup(func() { color.UseColor, *jsonOutput = previousColor, previousJSON })
	for _, args := range [][]string{{"list"}, {"diff", "gentoo:alternate"}, {"diff", "--explain", "gentoo:alternate"}} {
		var plain, colored, stderr bytes.Buffer
		color.UseColor = false
		if code := runProfileWithWriters(args, &plain, &stderr); code != 0 {
			t.Fatalf("plain code %d: %s", code, &stderr)
		}
		color.UseColor = true
		if code := runProfileWithWriters(args, &colored, &stderr); code != 0 {
			t.Fatalf("colored code %d: %s", code, &stderr)
		}
		if !strings.Contains(colored.String(), "\x1b[") || stripANSIForProgressTest(colored.String()) != plain.String() {
			t.Fatalf("%v color changed comparison text or columns", args)
		}
		*jsonOutput = true
		colored.Reset()
		if code := runProfileWithWriters(args, &colored, &stderr); code != 0 || !json.Valid(colored.Bytes()) || strings.Contains(colored.String(), "\x1b[") {
			t.Fatalf("global JSON code %d: %s", code, &stderr)
		}
		*jsonOutput = false
	}
	var stderr bytes.Buffer
	if code := runProfileWithWriters([]string{"diff", "gentoo:alternate"}, failingProfileWriter{}, &stderr); code != 1 || !strings.Contains(stderr.String(), "write") {
		t.Fatalf("diff write failure: code %d, %s", code, &stderr)
	}
}

func TestProfileDiffShowsReorderedRulesAndOmitsUnchangedContext(t *testing.T) {
	var output bytes.Buffer
	writer := profileTextWriter{writer: &output, width: 100}
	before := []string{"cat/shared one", "cat/pkg ssl", "cat/pkg -ssl", "cat/tail two"}
	after := []string{"cat/shared one", "cat/pkg -ssl", "cat/pkg ssl", "cat/tail two"}
	previous := color.UseColor
	color.UseColor = false
	t.Cleanup(func() { color.UseColor = previous })
	writer.changedValues(before, after, true)
	if strings.Contains(output.String(), "cat/shared") || strings.Contains(output.String(), "cat/tail") {
		t.Fatalf("unchanged context was repeated: %s", &output)
	}
	var removed, added []string
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.HasPrefix(line, "  -  ") {
			removed = append(removed, strings.TrimPrefix(line, "  -  "))
		}
		if strings.HasPrefix(line, "  +  ") {
			added = append(added, strings.TrimPrefix(line, "  +  "))
		}
	}
	if !reflect.DeepEqual(removed, before[1:3]) || !reflect.DeepEqual(added, after[1:3]) {
		t.Fatalf("rule reordering disappeared: %s", &output)
	}
}
