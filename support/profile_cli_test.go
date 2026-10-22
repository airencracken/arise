package support

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/portage"
)

func TestReleasedCLIProfileShowHonorsConfigRootAndOptions(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "arise")
	build := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-o", binary, "../cmd/arise")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	configBase := filepath.Join(root, "config-root")
	config := filepath.Join(configBase, "etc", "portage")
	repo := filepath.Join(root, "repo")
	profile := filepath.Join(repo, "profiles", "selected")
	for _, directory := range []string{config, profile} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(config, "repos.conf"):     "[gentoo]\nlocation = " + repo + "\n",
		filepath.Join(config, "make.conf"):      "USE=\"local\"\n",
		filepath.Join(profile, "make.defaults"): "# fixture\nUSE=\"profile\"\n",
	}
	for path, data := range files {
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(profile, filepath.Join(config, "make.profile")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PORTAGE_CONFIGROOT", configBase)
	t.Setenv("USE", "command-environment")
	for _, args := range [][]string{
		{"profile", "show", "--json"},
		{"--json", "profile", "show"},
		{"--color=y", "profile", "show", "--json"},
		{"--portage-config-root", config, "profile", "show", "--json", "--explain"},
	} {
		command := exec.Command(binary, args...)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		if err != nil || stderr.Len() != 0 {
			t.Fatalf("%v: %v, stderr: %s", args, err, &stderr)
		}
		var report portage.ProfileReport
		decoder := json.NewDecoder(bytes.NewReader(output))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&report); err != nil {
			t.Fatal(err)
		}
		if report.Schema != "arise.profile.v1" || report.ActivePath != profile || len(report.Defaults) != 1 || report.Defaults[0].Value != "profile" || report.Defaults[0].Contributions[0].Source.Line != 2 {
			t.Fatalf("CLI report = %+v", report)
		}
	}
	for _, args := range [][]string{{"profile", "--help"}, {"profile", "show", "--help"}} {
		if output, err := exec.Command(binary, args...).CombinedOutput(); err != nil || !strings.Contains(string(output), "arise profile show") {
			t.Fatalf("%v: %v\n%s", args, err, output)
		}
	}
	var plainText []byte
	for _, mode := range []string{"n", "y", "auto"} {
		command := exec.Command(binary, "--color="+mode, "profile", "show", "--explain")
		output, err := command.Output()
		if err != nil {
			t.Fatalf("color %s: %v", mode, err)
		}
		colored := bytes.Contains(output, []byte("\x1b["))
		if colored != (mode == "y") {
			t.Fatalf("color %s: ANSI presence = %t", mode, colored)
		}
		if mode == "n" {
			plainText = output
		}
		if mode == "auto" && !bytes.Equal(output, plainText) {
			t.Fatal("automatic color changed redirected report text")
		}
	}
	for path, want := range files {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("inspection modified %s: %v, %q", path, err, got)
		}
	}
}

func TestProfilePublishedSchemaContract(t *testing.T) {
	data, err := os.ReadFile("../docs/schemas/profile-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Draft      string                     `json:"$schema"`
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Draft != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("schema draft = %q", schema.Draft)
	}
	required := make(map[string]bool)
	for _, name := range schema.Required {
		required[name] = true
	}
	for _, name := range []string{"schema", "scope", "active_path", "layers", "defaults", "policies", "unsupported_files"} {
		if !required[name] || schema.Properties[name] == nil {
			t.Errorf("schema omits required field %s", name)
		}
	}
	var identity struct {
		Const string `json:"const"`
	}
	if err := json.Unmarshal(schema.Properties["schema"], &identity); err != nil || identity.Const != "arise.profile.v1" {
		t.Fatalf("schema identity: %+v, %v", identity, err)
	}
}
