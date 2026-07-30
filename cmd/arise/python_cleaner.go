package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/graph"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/pythoncleaner"
	"github.com/airencracken/arise/internal/resolve"
	"github.com/airencracken/arise/internal/world"
)

type pythonCleanerOptions struct {
	Check   bool
	Pretend bool
	Fix     bool
	Resume  bool
}

func parsePythonCleanerOptions(args []string) (pythonCleanerOptions, error) {
	var options pythonCleanerOptions
	for _, arg := range args {
		switch arg {
		case "--check":
			options.Check = true
		case "-p", "--pretend", "--dry-run":
			options.Pretend = true
		case "--fix":
			options.Fix = true
		case "--resume":
			options.Resume = true
		case "-h", "--help":
			return options, errPythonCleanerHelp
		default:
			return options, fmt.Errorf("unknown option %q", arg)
		}
	}
	if countTrue(options.Check, options.Pretend, options.Fix, options.Resume) != 1 {
		return options, fmt.Errorf("require exactly one of --check, --pretend, --fix, or --resume")
	}
	return options, nil
}

var errPythonCleanerHelp = fmt.Errorf("help requested")

func runPythonCleaner(args []string) int {
	options, err := parsePythonCleanerOptions(args)
	if err != nil {
		printPythonCleanerUsage(os.Stderr)
		if err == errPythonCleanerHelp {
			return 0
		}
		fmt.Fprintf(os.Stderr, "python-cleaner: %v\n", err)
		return 2
	}
	if (options.Fix || options.Resume) && *jsonOutput {
		fmt.Fprintln(os.Stderr, "python-cleaner: structured output is not yet available for staged mutation")
		return 2
	}
	preferencePath := commandRootPath("/etc/python-exec/python-exec.conf")
	report, plan, err := loadPythonCleanerState(preferencePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "python-cleaner: %v\n", err)
		return 1
	}
	if *jsonOutput {
		document := struct {
			Schema    int                  `json:"schema"`
			Operation string               `json:"operation"`
			Complete  bool                 `json:"complete"`
			Report    pythoncleaner.Report `json:"report"`
			Plan      pythoncleaner.Plan   `json:"plan"`
		}{Schema: 1, Operation: "python-cleaner", Complete: true, Report: report, Plan: plan}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(document); err != nil {
			fmt.Fprintf(os.Stderr, "python-cleaner: encode report: %v\n", err)
			return 1
		}
		return 0
	}
	printPythonCleanerReport(os.Stdout, report, plan)
	if options.Check {
		if pythonCleanerNeedsWork(report) {
			return 1
		}
		return 0
	}
	if options.Fix || options.Resume {
		return runPythonCleanerFix(options, preferencePath, report, plan)
	}
	cohorts := pythonCleanerRepairCohorts(plan)
	unavailable := pythonCleanerUnavailable(plan)
	if len(cohorts) == 0 && len(unavailable) == 0 {
		fmt.Fprintln(os.Stdout, "No bootstrap or consumer rebuild is required.")
		return 0
	}
	*pretend = true
	resolveCfg := pythonCleanerResolveConfig()
	for index, cohort := range cohorts {
		fmt.Fprintf(os.Stdout, "Checking repair cohort %d of %d: %s\n", index+1, len(cohorts), strings.Join(cohort, ", "))
		runResolve(cohort, *dbPath, *repoPath, resolveCfg)
	}
	if len(unavailable) != 0 {
		removals, err := pythonCleanerUnavailableRemovals(unavailable)
		if err != nil {
			fmt.Fprintf(os.Stderr, "python-cleaner: unavailable package recovery: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stdout, "Validated unavailable-removal cohort: %s\n", strings.Join(removals, ", "))
	}
	return 0
}

func loadPythonCleanerState(preferencePath string) (pythoncleaner.Report, pythoncleaner.Plan, error) {
	cfg, err := portage.LoadEffectiveConfig(*portageConfigRoot)
	if err != nil {
		return pythoncleaner.Report{}, pythoncleaner.Plan{}, fmt.Errorf("load Portage configuration: %w", err)
	}
	preference, err := pythoncleaner.ParsePreference(preferencePath)
	if err != nil {
		return pythoncleaner.Report{}, pythoncleaner.Plan{}, fmt.Errorf("read python-exec preference: %w", err)
	}
	policy := pythoncleaner.Policy{
		Targets:      strings.Fields(cfg.MakeConf["PYTHON_TARGETS"]),
		SingleTarget: cfg.MakeConf["PYTHON_SINGLE_TARGET"], Preference: preference,
	}
	report, err := pythoncleaner.Check(*vdbDir, commandEnv("ROOT", "/"), policy)
	if err != nil {
		return pythoncleaner.Report{}, pythoncleaner.Plan{}, err
	}
	repositories := maintainRepositoryRoots(*repoPath, *portageConfigRoot)
	plan := pythoncleaner.BuildPlanWithTargets(report, func(consumer pythoncleaner.Consumer) (string, bool) {
		return pythonConsumerRepairTarget(consumer, report.Policy.Targets, repositories)
	})
	return report, plan, nil
}

func runPythonCleanerFix(options pythonCleanerOptions, preferencePath string, report pythoncleaner.Report, plan pythoncleaner.Plan) int {
	companionPath := *resumeFile + ".python-cleaner"
	state := pythoncleaner.NewResumeState(report.Policy, pythoncleaner.ResumeStageValidate, nil, nil)
	useExecutorResume := false
	if options.Resume {
		saved, err := pythoncleaner.LoadResume(companionPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "python-cleaner: load resume context: %v\n", err)
			return 1
		}
		if !samePythonRepairPolicy(saved.Policy, report.Policy) {
			fmt.Fprintln(os.Stderr, "python-cleaner: effective Python policy changed since interruption; generate a fresh repair plan")
			return 1
		}
		state = saved
		if _, err := os.Stat(*resumeFile); err == nil {
			if saved.Stage != pythoncleaner.ResumeStageCohort {
				if err := resolve.RemoveCompletedResume(*resumeFile); err != nil {
					fmt.Fprintf(os.Stderr, "python-cleaner: executor progress exists outside a package cohort: %v\n", err)
					return 1
				}
			} else {
				useExecutorResume = true
			}
		} else if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "python-cleaner: inspect executor resume state: %v\n", err)
			return 1
		}
	}
	completed := cloneStringCohorts(state.CompletedCohorts)
	cohorts := pythonCleanerRepairCohorts(plan)
	current := []string(nil)
	if useExecutorResume {
		current = append([]string(nil), state.CurrentTargets...)
	} else if len(cohorts) != 0 {
		current = cohorts[0]
	}
	for iteration := 0; len(current) != 0; iteration++ {
		if iteration >= 256 {
			fmt.Fprintln(os.Stderr, "python-cleaner: repair exceeded the bounded cohort limit")
			return 1
		}
		state = pythoncleaner.NewResumeState(report.Policy, pythoncleaner.ResumeStageCohort, current, completed)
		if err := pythoncleaner.SaveResume(companionPath, state); err != nil {
			fmt.Fprintf(os.Stderr, "python-cleaner: save resume context: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stdout, "Applying Python repair cohort: %s\n", strings.Join(current, ", "))
		cfg := pythonCleanerResolveConfig(false)
		cfg.Resume = useExecutorResume
		runResolve(current, *dbPath, *repoPath, cfg)
		useExecutorResume = false
		completed = append(completed, append([]string(nil), current...))
		before := strings.Join(current, "\x00")
		var err error
		report, plan, err = loadPythonCleanerState(preferencePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "python-cleaner: post-cohort inventory failed: %v\n", err)
			return 1
		}
		if !samePythonRepairPolicy(state.Policy, report.Policy) {
			fmt.Fprintln(os.Stderr, "python-cleaner: effective Python policy changed during repair")
			return 1
		}
		cohorts = pythonCleanerRepairCohorts(plan)
		if len(cohorts) == 0 {
			current = nil
			break
		}
		current = cohorts[0]
		if strings.Join(current, "\x00") == before {
			state = pythoncleaner.NewResumeState(report.Policy, pythoncleaner.ResumeStageCohort, current, completed)
			_ = pythoncleaner.SaveResume(companionPath, state)
			fmt.Fprintln(os.Stderr, "python-cleaner: cohort made no independently observable repair progress")
			return 1
		}
	}
	state = pythoncleaner.NewResumeState(report.Policy, pythoncleaner.ResumeStageValidate, nil, completed)
	if err := pythoncleaner.SaveResume(companionPath, state); err != nil {
		fmt.Fprintf(os.Stderr, "python-cleaner: save validation checkpoint: %v\n", err)
		return 1
	}
	if unavailable := pythonCleanerUnavailable(plan); len(unavailable) != 0 {
		removals, err := pythonCleanerUnavailableRemovals(unavailable)
		if err != nil {
			fmt.Fprintf(os.Stderr, "python-cleaner: unavailable package recovery: %v\n", err)
			return 1
		}
		state = pythoncleaner.NewResumeState(report.Policy, pythoncleaner.ResumeStageUnavailable, removals, completed)
		if err := pythoncleaner.SaveResume(companionPath, state); err != nil {
			fmt.Fprintf(os.Stderr, "python-cleaner: save unavailable-removal checkpoint: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stdout, "Removing unavailable orphaned Python consumers: %s\n", strings.Join(removals, ", "))
		runUninstall(removals, *dbPath, *repoPath)
		report, plan, err = loadPythonCleanerState(preferencePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "python-cleaner: post-removal inventory failed: %v\n", err)
			return 1
		}
		if remaining := pythonCleanerUnavailable(plan); len(remaining) != 0 {
			fmt.Fprintf(os.Stderr, "python-cleaner: unavailable package removal made no observable progress: %s\n", strings.Join(remaining, ", "))
			return 1
		}
	}
	repairedTargets := flattenStringCohorts(completed)
	probes, err := pythoncleaner.BuildRuntimeProbes(*vdbDir, commandEnv("ROOT", "/"), report.Policy.Targets, repairedTargets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "python-cleaner: build runtime probes: %v\n", err)
		return 1
	}
	probes = append(pythoncleaner.InterpreterSmokeProbes(commandEnv("ROOT", "/"), report.Policy.Targets), probes...)
	if failures := pythoncleaner.RunRuntimeProbes(context.Background(), probes, 30*time.Second); len(failures) != 0 {
		for _, failure := range failures {
			fmt.Fprintf(os.Stderr, "python-cleaner: runtime probe failed for %s using %s: %s\n",
				failure.Probe.Module, failure.Probe.Interpreter, failure.Detail)
		}
		return 1
	}
	state = pythoncleaner.NewResumeState(report.Policy, pythoncleaner.ResumeStagePreference, nil, completed)
	if err := pythoncleaner.SaveResume(companionPath, state); err != nil {
		fmt.Fprintf(os.Stderr, "python-cleaner: save preference checkpoint: %v\n", err)
		return 1
	}
	target, err := pythoncleaner.PreferredPolicyTarget(report.Policy)
	if err != nil {
		fmt.Fprintf(os.Stderr, "python-cleaner: %v\n", err)
		return 1
	}
	if err := pythoncleaner.PublishPreference(preferencePath, target); err != nil {
		fmt.Fprintf(os.Stderr, "python-cleaner: publish python-exec preference: %v\n", err)
		return 1
	}
	preference, err := pythoncleaner.ParsePreference(preferencePath)
	if err != nil || len(preference) == 0 || preference[0] != target {
		fmt.Fprintf(os.Stderr, "python-cleaner: verify published python-exec preference: %v\n", err)
		return 1
	}
	finalReport, finalPlan, err := loadPythonCleanerState(preferencePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "python-cleaner: final-state inventory failed: %v\n", err)
		return 1
	}
	if !samePythonRepairPolicy(report.Policy, finalReport.Policy) ||
		len(pythonCleanerRepairCohorts(finalPlan)) != 0 ||
		len(pythonCleanerUnavailable(finalPlan)) != 0 {
		fmt.Fprintln(os.Stderr, "python-cleaner: final-state validation found unresolved Python package repair")
		return 1
	}
	if err := pythoncleaner.RemoveResume(companionPath); err != nil {
		fmt.Fprintf(os.Stderr, "python-cleaner: remove completed resume context: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stdout, "Python package repair and runtime validation completed; python-exec preference published.")
	return 0
}

func pythonCleanerUnavailableRemovals(unavailable []string) ([]string, error) {
	db, err := ingest.OpenReadOnlyDB(*dbPath)
	if err != nil {
		return nil, fmt.Errorf("open metadata: %w", err)
	}
	defer db.Close()
	state, err := graph.BuildFromState(db, *vdbDir, 0)
	if err != nil {
		return nil, fmt.Errorf("build installed-state graph: %w", err)
	}
	resolveGraph := state.ToResolveGraph()
	worldState, err := world.LoadWorld(*worldFile)
	if err != nil {
		return nil, fmt.Errorf("load world set: %w", err)
	}
	depclean, err := resolve.Depclean(resolveGraph, &resolve.WorldSet{Entries: worldState.Atoms})
	if err != nil {
		return nil, fmt.Errorf("classify orphaned packages: %w", err)
	}
	return pythonCleanerSelectUnavailableRemovals(unavailable, depclean)
}

func pythonCleanerSelectUnavailableRemovals(unavailable []string, depclean []resolve.PkgAction) ([]string, error) {
	orphaned := make(map[string]resolve.PkgAction, len(depclean))
	for _, action := range depclean {
		if action.Atom != nil {
			orphaned[action.Atom.CPV()] = action
		}
	}
	removals := make([]string, 0, len(unavailable))
	for _, cpv := range unavailable {
		action, ok := orphaned[cpv]
		if !ok || action.Atom == nil {
			return nil, fmt.Errorf("%s is unavailable but not independently classified as orphaned; select a replacement or removal policy", cpv)
		}
		removals = append(removals, "="+action.Atom.CPV())
	}
	sort.Strings(removals)
	return removals, nil
}

func pythonCleanerResolveConfig(pretendMode ...bool) resolve.ResolveConfig {
	cfg := resolveFlagsToConfig(true, false)
	cfg.Reinstall, cfg.ExplicitReinstall = true, true
	cfg.Oneshot = true
	cfg.Pretend = len(pretendMode) == 0 || pretendMode[0]
	// Complete-graph updates are intentionally excluded. Recovery cohorts must
	// not silently expand into unrelated reverse-dependent or world repairs.
	cfg.CompleteGraph = false
	return cfg
}

func samePythonRepairPolicy(left, right pythoncleaner.Policy) bool {
	return reflect.DeepEqual(left.Targets, right.Targets) && left.SingleTarget == right.SingleTarget
}

func pythonCleanerUnavailable(plan pythoncleaner.Plan) []string {
	for _, stage := range plan.Stages {
		if stage.Name == "unavailable-consumers" {
			return append([]string(nil), stage.Targets...)
		}
	}
	return nil
}

func cloneStringCohorts(cohorts [][]string) [][]string {
	result := make([][]string, len(cohorts))
	for index := range cohorts {
		result[index] = append([]string(nil), cohorts[index]...)
	}
	return result
}

func flattenStringCohorts(cohorts [][]string) []string {
	var result []string
	for _, cohort := range cohorts {
		result = append(result, cohort...)
	}
	return result
}

func pythonConsumerRepairTarget(consumer pythoncleaner.Consumer, policyTargets, repositories []string) (string, bool) {
	if targetsIntersect(consumer.SupportedTargets, policyTargets) &&
		pythonExactEbuildAvailable(consumer.CPV, repositories) {
		return "=" + consumer.CPV, true
	}
	if pythonConsumerAvailable(consumer.Atom, repositories) {
		return consumer.Atom, true
	}
	return "", false
}

func targetsIntersect(left, right []string) bool {
	for _, candidate := range left {
		if containsString(right, candidate) {
			return true
		}
	}
	return false
}

func pythonExactEbuildAvailable(cpv string, repositories []string) bool {
	parsed, err := atom.Parse("=" + cpv)
	if err != nil || parsed.Category == "" || parsed.Package == "" || parsed.Version == nil {
		return false
	}
	name := parsed.Package + "-" + parsed.Version.Raw + ".ebuild"
	for _, repository := range repositories {
		info, err := os.Stat(filepath.Join(repository, parsed.Category, parsed.Package, name))
		if err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

func pythonConsumerAvailable(target string, repositories []string) bool {
	parsed, err := atom.Parse(target)
	if err != nil || parsed.Category == "" || parsed.Package == "" {
		return false
	}
	for _, repository := range repositories {
		directory := filepath.Join(repository, parsed.Category, parsed.Package)
		entries, err := os.ReadDir(directory)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".ebuild") {
				return true
			}
		}
	}
	return false
}

func pythonCleanerRepairCohorts(plan pythoncleaner.Plan) [][]string {
	var cohorts [][]string
	for _, stage := range plan.Stages {
		if stage.Name != "bootstrap-interpreters" && stage.Name != "repair-cohort" {
			continue
		}
		if len(stage.Targets) != 0 {
			cohorts = append(cohorts, append([]string(nil), stage.Targets...))
		}
	}
	return cohorts
}

func printPythonCleanerUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Usage: arise python-cleaner --check|--pretend|--fix|--resume")
}

func printPythonCleanerReport(writer io.Writer, report pythoncleaner.Report, plan pythoncleaner.Plan) {
	fmt.Fprintf(writer, "Python policy targets: %s", strings.Join(report.Policy.Targets, ", "))
	if report.Policy.SingleTarget != "" {
		fmt.Fprintf(writer, "; single target: %s", report.Policy.SingleTarget)
	}
	fmt.Fprintln(writer)
	fmt.Fprintf(writer, "python-exec preference: %s\n", strings.Join(report.Policy.Preference, ", "))
	fmt.Fprintf(writer, "Installed interpreters: %d; missing policy targets: %d\n",
		len(report.Interpreters), len(report.Missing))
	for _, consumer := range report.Consumers {
		fmt.Fprintf(writer, "  rebuild %s as %s\n", consumer.CPV, consumer.Atom)
		for _, reason := range consumer.Reasons {
			fmt.Fprintf(writer, "    %s [%s]: %s\n", reason.Kind, reason.Target, reason.Evidence)
		}
	}
	for _, removal := range report.Removals {
		status := "blocked"
		if removal.Safe {
			status = "eligible after validation"
		}
		fmt.Fprintf(writer, "  remove %s: %s", removal.Interpreter.CPV, status)
		if len(removal.Blockers) != 0 {
			fmt.Fprintf(writer, " (%s)", strings.Join(removal.Blockers, ", "))
		}
		fmt.Fprintln(writer)
	}
	fmt.Fprintf(writer, "Unowned obsolete site-packages: %d", len(report.Orphans))
	if report.OmittedOrphans != 0 {
		fmt.Fprintf(writer, " (%d additional omitted)", report.OmittedOrphans)
	}
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Ordered Python repair plan:")
	for index, stage := range plan.Stages {
		fmt.Fprintf(writer, "  %d. %s", index+1, stage.Name)
		if len(stage.Targets) != 0 {
			fmt.Fprintf(writer, ": %s", strings.Join(stage.Targets, ", "))
		}
		fmt.Fprintln(writer)
	}
}

func pythonCleanerNeedsWork(report pythoncleaner.Report) bool {
	if len(report.Missing) != 0 || len(report.Consumers) != 0 || len(report.Orphans) != 0 || report.OmittedOrphans != 0 {
		return true
	}
	if len(report.Policy.Preference) == 0 || !containsString(report.Policy.Targets, report.Policy.Preference[0]) {
		return true
	}
	for _, removal := range report.Removals {
		if removal.Safe {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
