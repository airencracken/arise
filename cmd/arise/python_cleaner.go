package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/pythoncleaner"
	"github.com/airencracken/arise/internal/resolve"
)

type pythonCleanerOptions struct {
	Check   bool
	Pretend bool
	Fix     bool
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
		case "-h", "--help":
			return options, errPythonCleanerHelp
		default:
			return options, fmt.Errorf("unknown option %q", arg)
		}
	}
	if countTrue(options.Check, options.Pretend, options.Fix) != 1 {
		return options, fmt.Errorf("require exactly one of --check, --pretend, or --fix")
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
	if options.Fix {
		fmt.Fprintln(os.Stderr, "python-cleaner: --fix remains gated until runtime import probes and preference publication are implemented")
		return 1
	}
	cfg, err := portage.LoadEffectiveConfig(*portageConfigRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "python-cleaner: load Portage configuration: %v\n", err)
		return 1
	}
	preferencePath := commandRootPath("/etc/python-exec/python-exec.conf")
	preference, err := pythoncleaner.ParsePreference(preferencePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "python-cleaner: read python-exec preference: %v\n", err)
		return 1
	}
	policy := pythoncleaner.Policy{
		Targets:      strings.Fields(cfg.MakeConf["PYTHON_TARGETS"]),
		SingleTarget: cfg.MakeConf["PYTHON_SINGLE_TARGET"],
		Preference:   preference,
	}
	report, err := pythoncleaner.Check(*vdbDir, commandEnv("ROOT", "/"), policy)
	if err != nil {
		fmt.Fprintf(os.Stderr, "python-cleaner: %v\n", err)
		return 1
	}
	repositories := maintainRepositoryRoots(*repoPath, *portageConfigRoot)
	plan := pythoncleaner.BuildPlanWithTargets(report, func(consumer pythoncleaner.Consumer) (string, bool) {
		return pythonConsumerRepairTarget(consumer, report.Policy.Targets, repositories)
	})
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
	cohorts := pythonCleanerRepairCohorts(plan)
	if len(cohorts) == 0 {
		fmt.Fprintln(os.Stdout, "No bootstrap or consumer rebuild is required.")
		return 0
	}
	*pretend = true
	resolveCfg := pythonCleanerResolveConfig()
	for index, cohort := range cohorts {
		fmt.Fprintf(os.Stdout, "Checking repair cohort %d of %d: %s\n", index+1, len(cohorts), strings.Join(cohort, ", "))
		runResolve(cohort, *dbPath, *repoPath, resolveCfg)
	}
	return 0
}

func pythonCleanerResolveConfig() resolve.ResolveConfig {
	cfg := resolveFlagsToConfig(true, false)
	cfg.Reinstall, cfg.ExplicitReinstall = true, true
	cfg.Oneshot, cfg.Pretend = true, true
	// Complete-graph updates are intentionally excluded. Recovery cohorts must
	// not silently expand into unrelated reverse-dependent or world repairs.
	cfg.CompleteGraph = false
	return cfg
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
	fmt.Fprintln(writer, "Usage: arise python-cleaner --check|--pretend|--fix")
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
