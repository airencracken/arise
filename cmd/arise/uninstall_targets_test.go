package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/ingest"
)

func TestResolveUninstallTargets(t *testing.T) {
	vdb := t.TempDir()
	for cpv, slot := range map[string]string{"dev-util/codex-1": "0", "dev-util/codex-2": "1", "app-misc/other-1": "0"} {
		dir := filepath.Join(vdb, cpv)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		for name, content := range map[string]string{"SLOT": slot, "repository": "gentoo", "CONTENTS": "", "IUSE": "", "USE": ""} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tt := range []struct {
		name       string
		args, want []string
	}{
		{"qualified", []string{"dev-util/codex"}, []string{"dev-util/codex-1", "dev-util/codex-2"}},
		{"short", []string{"codex"}, []string{"dev-util/codex-1", "dev-util/codex-2"}},
		{"slot", []string{"codex:1"}, []string{"dev-util/codex-2"}},
		{"exact legacy", []string{"dev-util/codex-1"}, []string{"dev-util/codex-1"}},
		{"operator", []string{">=dev-util/codex-2"}, []string{"dev-util/codex-2"}},
		{"short operator", []string{"=codex-1"}, []string{"dev-util/codex-1"}},
		{"repository", []string{"codex::gentoo"}, []string{"dev-util/codex-1", "dev-util/codex-2"}},
		{"overlap", []string{"codex", "dev-util/codex-1"}, []string{"dev-util/codex-1", "dev-util/codex-2"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveUninstallTargets(vdb, tt.args)
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, %v; want %v", got, err, tt.want)
			}
		})
	}
	for _, input := range []string{"missing", "codex::missing", "codex:9", "../codex", "!dev-util/codex", "codex; echo bad", "", "codex\x00"} {
		t.Run("adversarial/"+input, func(t *testing.T) {
			got, err := resolveUninstallTargets(vdb, []string{"codex", input})
			if err == nil || got != nil {
				t.Fatalf("invalid request returned partial removal plan: %v, %v", got, err)
			}
		})
	}
	if _, err := resolveUninstallTargets(vdb, nil); err == nil {
		t.Fatal("empty request accepted")
	}
	if err := os.MkdirAll(filepath.Join(vdb, "app-misc/codex-3"), 0755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveUninstallTargets(vdb, []string{"codex"})
	if got != nil || err == nil || !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "dev-util/codex") || !strings.Contains(err.Error(), "app-misc/codex") {
		t.Fatalf("ambiguous name: %v, %v", got, err)
	}
}

func TestUninstallAliasRoutes(t *testing.T) {
	for _, name := range []string{"uninstall", "remove", "unmerge"} {
		args := normalizeEmergeArgs([]string{"arise", name, "-p", "codex"})
		want := []string{"arise", "-p", name, "codex"}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("%s options: %v", name, args)
		}
		command, targets := selectCommand(args[2:])
		if command != name || !reflect.DeepEqual(targets, []string{"codex"}) {
			t.Fatalf("%s route: %s %v", name, command, targets)
		}
	}
}

func TestUninstallShortNamePretendJSONContract(t *testing.T) {
	if root := os.Getenv("ARISE_UNINSTALL_QUERY_TEST_ROOT"); root != "" {
		*vdbDir = filepath.Join(root, "vdb")
		*worldFile = filepath.Join(root, "world")
		*portageConfigRoot = filepath.Join(root, "config")
		*pretend, *jsonOutput = true, true
		if os.Getenv("ARISE_UNINSTALL_QUERY_TEST_ASK") == "1" {
			*pretend, *jsonOutput, *ask = false, false, true
		}
		runUninstall([]string{"codex"}, filepath.Join(root, "db"), filepath.Join(root, "repo"))
		os.Exit(0)
	}
	root := t.TempDir()
	dir := writeConfigTarget(t, filepath.Join(root, "vdb"), "dev-util", "codex-1", "0", "gentoo")
	if err := os.WriteFile(filepath.Join(dir, "codex-1.ebuild"), []byte("EAPI=8\nSLOT=0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	db, err := ingest.OpenDB(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestUninstallShortNamePretendJSONContract$")
	command.Env = append(os.Environ(), "ARISE_UNINSTALL_QUERY_TEST_ROOT="+root)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("pretend uninstall: %v\n%s", err, output)
	}
	var plan jsonPlan
	if err := json.Unmarshal(output, &plan); err != nil {
		t.Fatalf("invalid plan JSON: %v\n%s", err, output)
	}
	if plan.Schema != 1 || plan.Operation != "uninstall" || !plan.Resolution.Verified || len(plan.Uninstall) != 1 || plan.Uninstall[0].CPV != "dev-util/codex-1" || len(plan.PlanSHA256) != 64 {
		t.Fatalf("invalid removal contract: %#v", plan)
	}
	if _, err := os.Stat(filepath.Join(dir, "CONTENTS")); err != nil {
		t.Fatalf("pretend changed VDB: %v", err)
	}
}

func TestConfirmUninstallOnlyExplicitAffirmativeAnswers(t *testing.T) {
	for _, input := range []string{"y\n", "YES\n", " yes \n", "no\n", "yesterday\n", "\n", ""} {
		var output strings.Builder
		got := confirmUninstall(strings.NewReader(input), &output, []string{"dev-util/codex-1"})
		want := input == "y\n" || input == "YES\n" || input == " yes \n"
		if got != want || !strings.Contains(output.String(), "dev-util/codex-1") {
			t.Fatalf("answer %q: accepted=%t", input, got)
		}
	}
}

func TestUninstallAskDeclineDoesNotMutate(t *testing.T) {
	root := t.TempDir()
	dir := writeConfigTarget(t, filepath.Join(root, "vdb"), "dev-util", "codex-1", "0", "gentoo")
	if err := os.WriteFile(filepath.Join(dir, "codex-1.ebuild"), []byte("EAPI=8\nSLOT=0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	db, err := ingest.OpenDB(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestUninstallShortNamePretendJSONContract$")
	command.Env = append(os.Environ(), "ARISE_UNINSTALL_QUERY_TEST_ROOT="+root, "ARISE_UNINSTALL_QUERY_TEST_ASK=1")
	command.Stdin = strings.NewReader("no\n")
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Aborted.") || !strings.Contains(string(output), "dev-util/codex-1") {
		t.Fatalf("declined removal: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(dir, "CONTENTS")); err != nil {
		t.Fatal("declined removal changed VDB")
	}
}
