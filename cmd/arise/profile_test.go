package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/airencracken/arise/internal/color"
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

func profileOutputFixture() portage.ProfileReport {
	root := "/repo/profiles"
	return portage.ProfileReport{
		ActivePath: root + "/desktop",
		Layers: []portage.ProfileReportLayer{
			{Path: root + "/base"}, {Path: root + "/desktop", Parents: []string{"../base"}},
		},
		Defaults: []portage.ProfileDefault{
			{Name: "CFLAGS", Value: "-O2 -pipe", Contributions: []portage.ProfileContribution{
				{Value: "-O1", Source: portage.ProfileSource{Path: root + "/base/make.defaults", Line: 10}},
				{Value: "-O2 -pipe", Source: portage.ProfileSource{Path: root + "/desktop/make.defaults", Line: 4}},
			}},
			{Name: "USE", Value: "ssl -debug"},
		},
		Policies: []portage.ProfilePolicy{
			{Name: "use.force", Kind: "effective", Values: []string{"ssl"}},
			{Name: "use.mask", Kind: "effective", Values: []string{"debug"}},
			{Name: "packages", Kind: "effective", Values: []string{"sys-apps/base"}},
			{Name: "package.use", Kind: "ordered_rules", Values: []string{"cat/short ssl -debug", "category/longer-package X alsa"}},
		},
		Unsupported: []string{root + "/desktop/profile.bashrc"},
	}
}

func TestProfileReportAlignsDefaultsRulesAndSources(t *testing.T) {
	previous := color.UseColor
	color.UseColor = false
	t.Cleanup(func() { color.UseColor = previous })
	var output bytes.Buffer
	writeProfileReport(&output, profileOutputFixture(), true)
	text := output.String()
	for _, want := range []string{"VARIABLE", "VALUE", "PACKAGE", "FLAGS", "SOURCE", "CONTRIBUTION"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing column heading %s:\n%s", want, text)
		}
	}
	valueColumn := -1
	flagColumn := -1
	sourceColumn := -1
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "  CFLAGS ") || strings.HasPrefix(line, "  USE ") {
			column := strings.IndexByte(line, '"')
			if valueColumn >= 0 && column != valueColumn {
				t.Errorf("default value columns differ: %d and %d", valueColumn, column)
			}
			valueColumn = column
		}
		if strings.HasPrefix(line, "  cat/short ") || strings.HasPrefix(line, "  category/longer-package ") {
			column := strings.Index(line, "ssl")
			if column < 0 {
				column = strings.Index(line, "X alsa")
			}
			if flagColumn >= 0 && column != flagColumn {
				t.Errorf("package flag columns differ: %d and %d", flagColumn, column)
			}
			flagColumn = column
		}
		if strings.HasPrefix(line, "    base/make.defaults:") || strings.HasPrefix(line, "    desktop/make.defaults:") {
			column := strings.IndexByte(line, '"')
			if sourceColumn >= 0 && column != sourceColumn {
				t.Errorf("contribution columns differ: %d and %d", sourceColumn, column)
			}
			sourceColumn = column
		}
	}
	if valueColumn < 0 || flagColumn < 0 || sourceColumn < 0 {
		t.Fatalf("missing aligned rows:\n%s", text)
	}
	if !strings.Contains(text, "Root") || !strings.Contains(text, "/repo/profiles") || !strings.Contains(text, "[selected]") {
		t.Fatalf("missing profile path context:\n%s", text)
	}
}

func TestProfileReportColorPreservesTextAndAlignment(t *testing.T) {
	previous := color.UseColor
	t.Cleanup(func() { color.UseColor = previous })
	var plain, colored bytes.Buffer
	color.UseColor = false
	writeProfileReport(&plain, profileOutputFixture(), true)
	color.UseColor = true
	writeProfileReport(&colored, profileOutputFixture(), true)
	if !strings.Contains(colored.String(), "\x1b[") {
		t.Fatal("profile report is not colorized")
	}
	if got := stripANSIForProgressTest(colored.String()); got != plain.String() {
		t.Fatalf("color changes text or column alignment:\n%s\n%s", got, &plain)
	}
	for _, want := range []string{color.Green("ssl"), color.Red("-debug"), color.Cyan("CFLAGS"), color.Blue("base/make.defaults:10")} {
		if !strings.Contains(colored.String(), want) {
			t.Errorf("missing semantic color %q", want)
		}
	}
}

func TestProfileReportWrapsValuesAtNarrowWidths(t *testing.T) {
	previous := color.UseColor
	t.Cleanup(func() { color.UseColor = previous })
	report := profileOutputFixture()
	report.Defaults[1].Value = "ssl alsa -debug pulseaudio wayland X vulkan opengl"
	for _, width := range []int{40, 60, 100} {
		color.UseColor = false
		var plain, colored bytes.Buffer
		writeProfileReportWidth(&plain, report, true, width)
		color.UseColor = true
		writeProfileReportWidth(&colored, report, true, width)
		if stripANSIForProgressTest(colored.String()) != plain.String() {
			t.Fatalf("color changed wrapping at width %d", width)
		}
		lines := strings.Split(plain.String(), "\n")
		for index, line := range lines {
			if strings.HasPrefix(line, "  USE ") {
				if utf8.RuneCountInString(line) > width {
					t.Errorf("value exceeds width %d: %q", width, line)
				}
				column := strings.IndexByte(line, '"')
				if width == 40 && (index+1 >= len(lines) || !strings.HasPrefix(lines[index+1], strings.Repeat(" ", column)+"wayland")) {
					t.Errorf("wrapped value does not align with its column: %q", plain.String())
				}
			}
		}
		for _, token := range []string{"pulseaudio", "wayland", "vulkan", "opengl", "desktop/make.defaults:4"} {
			if !strings.Contains(plain.String(), token) {
				t.Errorf("width %d lost or split token %q", width, token)
			}
		}
	}
}

func TestWrapProfileTextPreservesValuesAndUnbrokenTokens(t *testing.T) {
	for _, text := range []string{"", "\"\"", "\"ssl  -debug X\"", "alpha beta gamma delta", "café naïve", "/long/path/with/no/whitespace", "   many   spaces   "} {
		for width := 1; width <= 100; width++ {
			lines := wrapProfileText(text, width)
			if got := strings.Join(lines, " "); got != text {
				t.Fatalf("width %d: wrapping changed %q to %q", width, text, got)
			}
			for _, line := range lines {
				if utf8.RuneCountInString(line) > width && strings.Contains(strings.TrimSpace(line), " ") {
					t.Fatalf("width %d: line exceeded available space without wrapping: %q", width, line)
				}
			}
		}
	}
}

func TestProfileReportPreservesLongNamesAndCrossRepositoryPaths(t *testing.T) {
	previous := color.UseColor
	color.UseColor = false
	t.Cleanup(func() { color.UseColor = previous })
	report := profileOutputFixture()
	longName := "LONG_PROFILE_VARIABLE_NAME_THAT_EXCEEDS_THE_COLUMN"
	longPath := "/different-repository/profiles/very-long-parent-name/make.defaults"
	report.Defaults = append(report.Defaults, portage.ProfileDefault{
		Name: longName, Value: "\"quoted\" café  value\nwith newline",
		Contributions: []portage.ProfileContribution{{Value: "external value", Source: portage.ProfileSource{Path: longPath, Line: 1000}}},
	})
	var output bytes.Buffer
	writeProfileReportWidth(&output, report, true, 40)
	for _, want := range []string{longName, longPath + ":1000", "\\\"quoted\\\"", "café", "\\nwith", "newline\"", "external value"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("long or escaped content lost %q:\n%s", want, &output)
		}
	}
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatal("plain report contains ANSI colors")
	}
}

func TestProfileShowJSONIsIdenticalWithColorAndExplanation(t *testing.T) {
	profileCommandFixture(t)
	previous := color.UseColor
	t.Cleanup(func() { color.UseColor = previous })
	var plain, colored, stderr bytes.Buffer
	color.UseColor = false
	if code := runProfileWithWriters([]string{"show", "--json"}, &plain, &stderr); code != 0 {
		t.Fatalf("plain JSON: code %d, %s", code, &stderr)
	}
	color.UseColor = true
	if code := runProfileWithWriters([]string{"show", "--json", "--explain"}, &colored, &stderr); code != 0 {
		t.Fatalf("colored JSON: code %d, %s", code, &stderr)
	}
	if !json.Valid(colored.Bytes()) || colored.String() != plain.String() || strings.Contains(colored.String(), "\x1b[") {
		t.Fatal("color or explanation changed machine output")
	}
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
		for _, want := range []string{"Scope    profile", "Inheritance (evaluation order)", "VARIABLE  VALUE", "USE       \"ssl\"", "sys-apps/base", "ordered rules; evaluated per package"} {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("%v: missing %q in %s", test.args, want, &stdout)
			}
		}
		if got := strings.Contains(stdout.String(), "make.defaults:2"); got != (len(test.args) == 2) {
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
	if code := runProfileWithWriters([]string{"show", "--json=false"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "make.defaults:2") {
		t.Fatalf("global explain: code %d, stdout %q, stderr %q", code, &stdout, &stderr)
	}
	if code := runProfileWithWriters([]string{"show"}, failingProfileWriter{}, &stderr); code != 1 || !strings.Contains(stderr.String(), "fixture write failure") {
		t.Fatalf("write failure: code %d, stderr %q", code, &stderr)
	}
}
