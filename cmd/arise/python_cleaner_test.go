package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/pythoncleaner"
)

func TestParsePythonCleanerOptions(t *testing.T) {
	tests := []struct {
		args []string
		want pythonCleanerOptions
	}{
		{[]string{"--check"}, pythonCleanerOptions{Check: true}},
		{[]string{"--pretend"}, pythonCleanerOptions{Pretend: true}},
		{[]string{"--fix"}, pythonCleanerOptions{Fix: true}},
		{[]string{"--resume"}, pythonCleanerOptions{Resume: true}},
	}
	for _, test := range tests {
		got, err := parsePythonCleanerOptions(test.args)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("%v = %#v, want %#v", test.args, got, test.want)
		}
	}
	for _, args := range [][]string{nil, {"--check", "--fix"}, {"--fix", "--resume"}, {"--unknown"}} {
		if _, err := parsePythonCleanerOptions(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestPythonConsumerAvailabilityUsesConfiguredRepositories(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "dev-python", "Available")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "Available-1.ebuild"), []byte("EAPI=8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !pythonConsumerAvailable("dev-python/Available:0", []string{root}) {
		t.Fatal("available package reported missing")
	}
	for _, target := range []string{"dev-python/Gone:0", "../../escape", "bad"} {
		if pythonConsumerAvailable(target, []string{root}) {
			t.Fatalf("unavailable target accepted: %q", target)
		}
	}
}

func TestPythonConsumerRepairTargetPinsExactThenFallsBack(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "dev-python", "Available")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "Available-1.ebuild"), []byte("EAPI=8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	consumer := pythoncleaner.Consumer{
		CPV: "dev-python/Available-1", Atom: "dev-python/Available:0",
		SupportedTargets: []string{"python3_14"},
	}
	if got, ok := pythonConsumerRepairTarget(consumer, []string{"python3_14"}, []string{root}); !ok || got != "=dev-python/Available-1" {
		t.Fatalf("exact repair target = %q, %v", got, ok)
	}
	consumer.CPV = "dev-python/Available-0"
	if got, ok := pythonConsumerRepairTarget(consumer, []string{"python3_14"}, []string{root}); !ok || got != "dev-python/Available:0" {
		t.Fatalf("fallback repair target = %q, %v", got, ok)
	}
	consumer.CPV = "dev-python/Available-1"
	consumer.SupportedTargets = []string{"python3_13"}
	if got, ok := pythonConsumerRepairTarget(consumer, []string{"python3_14"}, []string{root}); !ok || got != "dev-python/Available:0" {
		t.Fatalf("unsupported exact target did not fall back = %q, %v", got, ok)
	}
	consumer = pythoncleaner.Consumer{CPV: "dev-python/Gone-1", Atom: "dev-python/Gone:0"}
	if got, ok := pythonConsumerRepairTarget(consumer, []string{"python3_14"}, []string{root}); ok || got != "" {
		t.Fatalf("missing repair target = %q, %v", got, ok)
	}
}

func TestPythonCleanerRepairCohortsRemainAtomic(t *testing.T) {
	plan := pythoncleaner.Plan{Stages: []pythoncleaner.Stage{
		{Name: "bootstrap-interpreters", Targets: []string{"dev-lang/python:3.14"}},
		{Name: "repair-cohort", Targets: []string{"=dev-python/A-1"}},
		{Name: "unavailable-consumers", Targets: []string{"dev-python/Gone-1"}},
		{Name: "validate-runtime"},
	}}
	got := pythonCleanerRepairCohorts(plan)
	want := [][]string{{"dev-lang/python:3.14"}, {"=dev-python/A-1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cohorts = %v, want %v", got, want)
	}
	got[0][0] = "mutated"
	if plan.Stages[0].Targets[0] != "dev-lang/python:3.14" {
		t.Fatal("cohort aliases immutable plan")
	}
}

func TestPythonCleanerResolveConfigDoesNotExpandToCompleteGraph(t *testing.T) {
	cfg := pythonCleanerResolveConfig()
	if !cfg.Update || !cfg.Reinstall || !cfg.ExplicitReinstall || !cfg.Oneshot || !cfg.Pretend {
		t.Fatalf("incomplete recovery flags: %#v", cfg)
	}
	if cfg.CompleteGraph {
		t.Fatal("recovery cohort expanded to complete graph")
	}
	live := pythonCleanerResolveConfig(false)
	if live.Pretend || !live.Reinstall || !live.Oneshot {
		t.Fatalf("live recovery config = %#v", live)
	}
}

func TestPythonCleanerResumeHelpersPreserveBoundariesAndIgnorePreferenceDrift(t *testing.T) {
	left := pythoncleaner.Policy{
		Targets: []string{"python3_14"}, SingleTarget: "python3_14",
		Preference: []string{"python3_13"},
	}
	right := left
	right.Preference = []string{"python3_14"}
	if !samePythonRepairPolicy(left, right) {
		t.Fatal("preference publication treated as repair policy drift")
	}
	right.Targets = []string{"python3_15"}
	if samePythonRepairPolicy(left, right) {
		t.Fatal("target policy drift accepted")
	}
	cohorts := [][]string{{"dev-python/A"}, {"dev-python/B", "dev-python/C"}}
	cloned := cloneStringCohorts(cohorts)
	flat := flattenStringCohorts(cloned)
	if !reflect.DeepEqual(flat, []string{"dev-python/A", "dev-python/B", "dev-python/C"}) {
		t.Fatalf("flat cohorts = %v", flat)
	}
	cloned[0][0] = "mutated"
	if cohorts[0][0] != "dev-python/A" {
		t.Fatal("cloned cohorts alias source")
	}
}

func TestPythonCleanerUnavailableIsCopied(t *testing.T) {
	plan := pythoncleaner.Plan{Stages: []pythoncleaner.Stage{{
		Name: "unavailable-consumers", Targets: []string{"dev-python/Gone-1"},
	}}}
	got := pythonCleanerUnavailable(plan)
	if !reflect.DeepEqual(got, []string{"dev-python/Gone-1"}) {
		t.Fatalf("unavailable = %v", got)
	}
	got[0] = "mutated"
	if plan.Stages[0].Targets[0] != "dev-python/Gone-1" {
		t.Fatal("unavailable result aliases plan")
	}
}

func TestPythonCleanerRejectsStructuredMutationBeforeStateAccess(t *testing.T) {
	original := *jsonOutput
	*jsonOutput = true
	t.Cleanup(func() { *jsonOutput = original })
	for _, option := range []string{"--fix", "--resume"} {
		if code := runPythonCleaner([]string{option}); code != 2 {
			t.Fatalf("%s exit = %d", option, code)
		}
	}
}

func TestPythonCleanerFixPublishesPreferenceOnlyAfterRuntimeValidation(t *testing.T) {
	root, preference, companion := pythonCleanerFixFixture(t, "# keep\npython3.13\n", "#!/bin/sh\nexit 0\n")
	if code := runPythonCleaner([]string{"--fix"}); code != 0 {
		t.Fatalf("fix exit = %d", code)
	}
	data, err := os.ReadFile(preference)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "python3.14\n") || !strings.Contains(string(data), "# keep\n") {
		t.Fatalf("preference = %q", data)
	}
	if _, err := os.Stat(companion); !os.IsNotExist(err) {
		t.Fatalf("completed resume context remains under %s: %v", root, err)
	}
}

func TestPythonCleanerRuntimeFailurePreservesPreferenceAndCheckpoint(t *testing.T) {
	root, preference, companion := pythonCleanerFixFixture(t, "python3.13\n", "#!/bin/sh\necho broken >&2\nexit 7\n")
	if code := runPythonCleaner([]string{"--fix"}); code != 1 {
		t.Fatalf("fix exit = %d", code)
	}
	data, err := os.ReadFile(preference)
	if err != nil || string(data) != "python3.13\n" {
		t.Fatalf("preference changed after failed probe: %q %v", data, err)
	}
	state, err := pythoncleaner.LoadResume(companion)
	if err != nil {
		t.Fatal(err)
	}
	if state.Stage != pythoncleaner.ResumeStageValidate {
		t.Fatalf("checkpoint stage = %q", state.Stage)
	}
	if err := os.WriteFile(filepath.Join(root, "usr", "bin", "python3.14"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code := runPythonCleaner([]string{"--resume"}); code != 0 {
		t.Fatalf("resume exit = %d", code)
	}
	data, err = os.ReadFile(preference)
	if err != nil || !strings.HasPrefix(string(data), "python3.14\n") {
		t.Fatalf("resume did not publish preference: %q %v", data, err)
	}
}

func pythonCleanerFixFixture(t *testing.T, preference, interpreter string) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	configRoot := filepath.Join(root, "etc", "portage")
	vdbRoot := filepath.Join(root, "var", "db", "pkg")
	repository := filepath.Join(root, "var", "db", "repos", "gentoo")
	resumePath := filepath.Join(root, "var", "tmp", "arise", "resume")
	for _, directory := range []string{
		configRoot, vdbRoot, repository,
		filepath.Join(root, "etc", "python-exec"),
		filepath.Join(root, "usr", "bin"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(configRoot, "make.conf"), []byte(
		"PYTHON_TARGETS=\"python3_14\"\nPYTHON_SINGLE_TARGET=\"python3_14\"\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	preferencePath := filepath.Join(root, "etc", "python-exec", "python-exec.conf")
	if err := os.WriteFile(preferencePath, []byte(preference), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "usr", "bin", "python3.14"), []byte(interpreter), 0o755); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(vdbRoot, "dev-lang", "python-3.14.6")
	if err := os.MkdirAll(packagePath, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"CONTENTS": "", "EAPI": "8\n", "SLOT": "3.14\n", "repository": "gentoo\n",
	} {
		if err := os.WriteFile(filepath.Join(packagePath, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldConfigRoot, oldVDB, oldRepo, oldResume := *portageConfigRoot, *vdbDir, *repoPath, *resumeFile
	oldPretend, oldJSON := *pretend, *jsonOutput
	*portageConfigRoot, *vdbDir, *repoPath, *resumeFile = configRoot, vdbRoot, repository, resumePath
	*pretend, *jsonOutput = false, false
	t.Setenv("ROOT", root)
	t.Cleanup(func() {
		*portageConfigRoot, *vdbDir, *repoPath, *resumeFile = oldConfigRoot, oldVDB, oldRepo, oldResume
		*pretend, *jsonOutput = oldPretend, oldJSON
	})
	return root, preferencePath, resumePath + ".python-cleaner"
}

func TestPythonCleanerRouteExists(t *testing.T) {
	command, args := selectCommand([]string{"python-cleaner", "--check"})
	if command != "python-cleaner" || !reflect.DeepEqual(args, []string{"--check"}) {
		t.Fatalf("route = %q %v", command, args)
	}
}

func TestPythonCleanerNeedsWorkIncludesEverySafetyDimension(t *testing.T) {
	clean := pythoncleaner.Report{
		Policy: pythoncleaner.Policy{Targets: []string{"python3_14"}, Preference: []string{"python3_14"}},
	}
	if pythonCleanerNeedsWork(clean) {
		t.Fatal("clean report needs work")
	}
	cases := []pythoncleaner.Report{
		{Policy: clean.Policy, Missing: []string{"python3_14"}},
		{Policy: clean.Policy, Consumers: []pythoncleaner.Consumer{{CPV: "dev-python/Foo-1"}}},
		{Policy: clean.Policy, Orphans: []pythoncleaner.Orphan{{Path: "/old"}}},
		{Policy: clean.Policy, OmittedOrphans: 1},
		{Policy: pythoncleaner.Policy{Targets: []string{"python3_14"}, Preference: []string{"python3_13"}}},
		{Policy: clean.Policy, Removals: []pythoncleaner.Removal{{Safe: true}}},
	}
	for index, report := range cases {
		if !pythonCleanerNeedsWork(report) {
			t.Errorf("case %d reported clean: %#v", index, report)
		}
	}
}

func TestPrintPythonCleanerReportShowsOrderedSafetyPlan(t *testing.T) {
	report := pythoncleaner.Report{
		Policy: pythoncleaner.Policy{
			Targets: []string{"python3_14"}, SingleTarget: "python3_14",
			Preference: []string{"python3_13", "python3_14"},
		},
		Interpreters: []pythoncleaner.Interpreter{{Target: "python3_14"}},
		Consumers: []pythoncleaner.Consumer{{
			CPV: "dev-python/Foo-1", Atom: "dev-python/Foo:0",
			Reasons: []pythoncleaner.Reason{{Kind: "stale-shebang", Target: "python3_13", Evidence: "/usr/bin/foo"}},
		}},
		Removals: []pythoncleaner.Removal{{
			Interpreter: pythoncleaner.Interpreter{CPV: "dev-lang/python-3.13.13"},
			Blockers:    []string{"python-exec preference"},
		}},
	}
	plan := pythoncleaner.BuildPlan(report)
	var output bytes.Buffer
	printPythonCleanerReport(&output, report, plan)
	for _, fragment := range []string{
		"Python policy targets: python3_14",
		"python-exec preference: python3_13, python3_14",
		"stale-shebang [python3_13]: /usr/bin/foo",
		"remove dev-lang/python-3.13.13: blocked (python-exec preference)",
		"validate-runtime",
		"switch-preference",
	} {
		if !strings.Contains(output.String(), fragment) {
			t.Fatalf("output missing %q:\n%s", fragment, output.String())
		}
	}
}
