package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/resolve"
	"github.com/dsnet/compress/bzip2"
)

func TestInstallPlanningProcess(t *testing.T) {
	root := os.Getenv("ARISE_INSTALL_PLANNING_TEST_ROOT")
	if root == "" {
		return
	}
	*vdbDir, *worldFile = filepath.Join(root, "vdb"), filepath.Join(root, "world")
	*portageConfigRoot, *resumeFile = filepath.Join(root, "config"), filepath.Join(root, "resume")
	*workDir, *journalDir, *emergeLog = filepath.Join(root, "work"), filepath.Join(root, "journal"), filepath.Join(root, "merge.log")
	*ask = os.Getenv("ARISE_INSTALL_PLANNING_ASK") == "1"
	mode := os.Getenv("ARISE_INSTALL_PLANNING_MODE")
	*resume, *skipFirst, *updateMode = strings.HasPrefix(mode, "resume"), strings.HasPrefix(mode, "resume"), mode != "resume"
	*pretend, *oneshot = strings.HasSuffix(mode, "-pretend"), mode == "resume-oneshot"
	if mode == "nodeps-json" {
		*nodeps, *pretend, *jsonOutput = true, true, true
	}
	if strings.Contains(mode, "bad-approval") {
		*approvePlanSHA256 = strings.Repeat("f", 64)
	}
	if mode == "prepare-approval" {
		*pretend, *jsonOutput = true, true
		*planDir, *savePlan = filepath.Join(root, "plans"), "noop"
	}
	if mode == "good-approval" {
		*approvePlan = filepath.Join(root, "plans/noop.json")
	}
	runInstall([]string{"app-misc/demo"}, filepath.Join(root, "db"), filepath.Join(root, "repo"))
	os.Exit(0)
}

func TestInstallPlanningPreservesUnrelatedState(t *testing.T) {
	for _, test := range []struct {
		name, mode, input string
		ask               bool
		wantWorld         string
		favorites         bool
		completed         bool
		retainCheckpoint  bool
		wantFailure       bool
	}{
		{name: "declined resume", mode: "resume", input: "no\n", ask: true, wantWorld: "app-misc/existing\n", retainCheckpoint: true},
		{name: "declined empty update", mode: "update", input: "no\n", ask: true, wantWorld: "app-misc/existing\n", retainCheckpoint: true},
		{name: "accepted empty update", mode: "update", input: "yes\n", ask: true, wantWorld: "app-misc/demo\napp-misc/existing\n", retainCheckpoint: true},
		{name: "noninteractive empty update", mode: "update", wantWorld: "app-misc/demo\napp-misc/existing\n", retainCheckpoint: true},
		{name: "legacy resume does not promote dependencies", mode: "resume-update", input: "yes\n", ask: true, wantWorld: "app-misc/existing\n"},
		{name: "resume retains original favorite", mode: "resume-update", input: "yes\n", ask: true, wantWorld: "app-misc/demo\napp-misc/existing\n", favorites: true},
		{name: "resume oneshot suppresses favorite", mode: "resume-oneshot", input: "yes\n", ask: true, wantWorld: "app-misc/existing\n", favorites: true},
		{name: "finish world after packages committed", mode: "resume-complete", input: "yes\n", ask: true, wantWorld: "app-misc/demo\napp-misc/existing\n", favorites: true, completed: true},
		{name: "decline completed resume", mode: "resume-complete", input: "no\n", ask: true, wantWorld: "app-misc/existing\n", favorites: true, completed: true, retainCheckpoint: true},
		{name: "pretend completed resume", mode: "resume-complete-pretend", wantWorld: "app-misc/existing\n", favorites: true, completed: true, retainCheckpoint: true},
		{name: "nodeps diagnostic plan stays unverified", mode: "nodeps-json", wantWorld: "app-misc/existing\n", retainCheckpoint: true, wantFailure: true},
		{name: "empty update checks supplied approval", mode: "bad-approval", input: "yes\n", ask: true, wantWorld: "app-misc/existing\n", retainCheckpoint: true, wantFailure: true},
		{name: "completed resume rejects package approval", mode: "resume-complete-bad-approval", input: "yes\n", ask: true, wantWorld: "app-misc/existing\n", favorites: true, completed: true, retainCheckpoint: true, wantFailure: true},
		{name: "empty update accepts matching saved approval", mode: "good-approval", input: "yes\n", ask: true, wantWorld: "app-misc/demo\napp-misc/existing\n", retainCheckpoint: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.mode == "resume-update" || test.mode == "resume-oneshot" {
				if _, err := exec.LookPath("sandbox"); err != nil {
					t.Skip("Portage sandbox is unavailable")
				}
			}
			root := t.TempDir()
			for _, dir := range []string{"config/repos.conf", "repo/eclass", "repo/profiles", "repo/app-misc/demo", "vdb/app-misc/demo-1"} {
				if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
					t.Fatal(err)
				}
			}
			const ebuild = "EAPI=8\nSLOT=0\nS=\"${WORKDIR}/${P}\"\nsrc_unpack() { mkdir -p \"${S}\"; }\nsrc_install() { insinto /usr/share/planning-audit; echo rebuilt > \"${T}/demo\"; doins \"${T}/demo\"; }\n"
			for path, value := range map[string]string{"repo/profiles/repo_name": "test\n", "repo/app-misc/demo/demo-1.ebuild": ebuild, "vdb/app-misc/demo-1/demo-1.ebuild": ebuild, "config/repos.conf/test.conf": "[test]\nlocation = " + filepath.Join(root, "repo") + "\n"} {
				if err := os.WriteFile(filepath.Join(root, path), []byte(value), 0644); err != nil {
					t.Fatal(err)
				}
			}
			var environment bytes.Buffer
			compressor, err := bzip2.NewWriter(&environment, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := compressor.Write([]byte("EAPI='8'\nSLOT='0'\n")); err != nil {
				t.Fatal(err)
			}
			if err := compressor.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "vdb/app-misc/demo-1/environment.bz2"), environment.Bytes(), 0644); err != nil {
				t.Fatal(err)
			}
			for name, value := range map[string]string{"SLOT": "0", "EAPI": "8", "repository": "test", "CONTENTS": "", "USE": "", "IUSE": ""} {
				if err := os.WriteFile(filepath.Join(root, "vdb/app-misc/demo-1", name), []byte(value), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "config/make.conf"), []byte("ARCH=amd64\nACCEPT_KEYWORDS=amd64\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "world"), []byte("app-misc/existing\n"), 0644); err != nil {
				t.Fatal(err)
			}
			db, err := ingest.OpenDB(filepath.Join(root, "db"))
			if err != nil {
				t.Fatal(err)
			}
			entries := make(chan *metadata.PackageMetadata, 1)
			entries <- &metadata.PackageMetadata{Category: "app-misc", Package: "demo", Version: "1", SLOT: "0", EAPI: "8", KEYWORDS: "amd64", Repository: "test", RepositoryPath: filepath.Join(root, "repo")}
			close(entries)
			if _, err := ingest.Ingest(db, entries); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			first, _ := atom.Parse("app-misc/first-1")
			second, _ := atom.Parse("app-misc/demo-1")
			path := filepath.Join(root, "resume")
			var favorites []string
			if test.favorites {
				favorites = []string{"app-misc/demo"}
			}
			if err := resolve.SaveResumeWithWorldSelections(path, &resolve.ResolveResult{Install: []resolve.PkgAction{{Atom: first}, {Atom: second}}}, favorites); err != nil {
				t.Fatal(err)
			}
			if test.completed {
				for _, parsed := range []*atom.Atom{first, second} {
					if err := resolve.MarkResumeComplete(path, parsed.String()); err != nil {
						t.Fatal(err)
					}
				}
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if test.mode == "good-approval" {
				prepare := exec.Command(os.Args[0], "-test.run=^TestInstallPlanningProcess$")
				prepare.Env = append(os.Environ(), "ARISE_INSTALL_PLANNING_TEST_ROOT="+root, "ARISE_INSTALL_PLANNING_MODE=prepare-approval", "ROOT="+root)
				if output, err := prepare.CombinedOutput(); err != nil {
					t.Fatalf("prepare empty approval: %v\n%s", err, output)
				}
			}
			command := exec.Command(os.Args[0], "-test.run=^TestInstallPlanningProcess$")
			command.Env = append(os.Environ(), "ARISE_INSTALL_PLANNING_TEST_ROOT="+root, "ARISE_INSTALL_PLANNING_MODE="+test.mode, "ROOT="+root)
			if test.ask {
				command.Env = append(command.Env, "ARISE_INSTALL_PLANNING_ASK=1")
			}
			command.Stdin = strings.NewReader(test.input)
			output, err := command.CombinedOutput()
			if err != nil && !test.wantFailure || err == nil && test.wantFailure {
				t.Fatalf("install: %v\n%s", err, output)
			}
			if test.wantFailure && test.mode == "nodeps-json" {
				var plan jsonPlan
				if err := json.Unmarshal(output, &plan); err != nil || plan.Resolution.Verified || plan.Resolution.Verification != resolve.VerificationSkippedNoDeps {
					t.Fatalf("nodeps did not explain skipped verification: %v\n%s", err, output)
				}
			}
			if test.mode == "bad-approval" && !strings.Contains(string(output), "does not match current verified plan") {
				t.Fatalf("supplied approval ignored: %s", output)
			}
			if test.mode == "resume-complete-bad-approval" && !strings.Contains(string(output), "no package plan to approve") {
				t.Fatalf("completed resume ignored package approval: %s", output)
			}
			if test.mode == "resume-update" || test.mode == "resume-oneshot" {
				if data, err := os.ReadFile(filepath.Join(root, "usr/share/planning-audit/demo")); err != nil || string(data) != "rebuilt\n" {
					t.Errorf("pending same-version action was skipped: %q, %v", data, err)
				}
			}
			if test.input == "no\n" && !strings.Contains(string(output), "Aborted.") {
				t.Errorf("declined operation was not confirmed: %s", output)
			}
			if after, err := os.ReadFile(path); test.retainCheckpoint && (err != nil || string(after) != string(before)) {
				t.Errorf("planning changed unrelated or declined checkpoint: %q, %v", after, err)
			} else if !test.retainCheckpoint && !os.IsNotExist(err) {
				t.Errorf("completed resume remains: %q, %v", after, err)
			}
			if data, err := os.ReadFile(filepath.Join(root, "world")); err != nil || string(data) != test.wantWorld {
				t.Errorf("world = %q, %v; want %q", data, err, test.wantWorld)
			}
		})
	}
}
