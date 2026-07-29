// arise-plan-compare runs equivalent pretend plans and explains their action-set differences.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/airencracken/arise/internal/plancompare"
	"github.com/airencracken/arise/internal/planvalidate"
)

type report struct {
	AriseCount                 int                           `json:"arise_count"`
	EmergeCount                int                           `json:"emerge_count"`
	AriseVerified              bool                          `json:"arise_verified"`
	PortageResolved            bool                          `json:"portage_resolved"`
	ComparisonClass            string                        `json:"comparison_class"`
	Accepted                   bool                          `json:"accepted"`
	Equivalent                 bool                          `json:"equivalent"`
	Differences                []plancompare.Difference      `json:"differences,omitempty"`
	ActionDiagnosticsTruncated bool                          `json:"action_diagnostics_truncated"`
	OmittedActionDiagnostics   int                           `json:"omitted_action_diagnostics"`
	StateDifferences           []plancompare.StateDifference `json:"state_differences,omitempty"`
	StateComparisonTruncated   bool                          `json:"state_comparison_truncated"`
	OmittedStateDifferences    int                           `json:"omitted_state_differences"`
}

type commandResult struct {
	stdout string
	stderr string
	err    error
}

func main() {
	arisePath := flag.String("arise", "arise", "Arise executable")
	emergePath := flag.String("emerge", "emerge", "emerge executable")
	ariseDB := flag.String("arise-db", "", "Arise metadata database path")
	ariseRepo := flag.String("arise-repo", "", "Arise repository path")
	target := flag.String("target", "@world", "package atom or set")
	operation := flag.String("operation", "update", "Arise operation")
	completeGraph := flag.Bool("complete-graph", true, "enable complete-graph resolution")
	deep := flag.Bool("deep", false, "enable deep dependency traversal")
	newUse := flag.Bool("newuse", false, "rebuild when USE configuration changed")
	withBdeps := flag.String("with-bdeps", "auto", "build dependency mode: y, n, or auto")
	backtrack := flag.Int("backtrack", 20, "backtrack limit for both resolvers")
	jsonOutput := flag.Bool("json", false, "emit JSON")
	ariseStatePath := flag.String("arise-state", "", "versioned frozen Arise fixture and plan for independent final-state validation")
	portageStatePath := flag.String("portage-state", "", "versioned frozen Portage fixture and plan for independent final-state validation")
	policyPath := flag.String("classification-policy", "", "versioned required/optional/policy-equivalent classification policy")
	captureDir := flag.String("capture-dir", "", "write deterministic frozen comparison documents to this directory")
	flag.Parse()
	if *withBdeps != "auto" && *withBdeps != "y" && *withBdeps != "n" {
		fatal(fmt.Errorf("--with-bdeps must be auto, y, or n"))
	}

	ariseArgs := []string{"--json", "--include-validation-fixture", "--pretend", fmt.Sprintf("--backtrack=%d", *backtrack)}
	if *ariseDB != "" {
		ariseArgs = append(ariseArgs, "--db", *ariseDB)
	}
	if *ariseRepo != "" {
		ariseArgs = append(ariseArgs, "--repo", *ariseRepo)
	}
	if *completeGraph {
		ariseArgs = append(ariseArgs, "--complete-graph")
	}
	if *deep {
		ariseArgs = append(ariseArgs, "--deep")
	}
	if *newUse {
		ariseArgs = append(ariseArgs, "--newuse")
	}
	if *withBdeps != "auto" {
		ariseArgs = append(ariseArgs, "--with-bdeps="+*withBdeps)
	}
	ariseArgs = append(ariseArgs, *operation, *target)
	// Verbose output is required for Portage to retain slot, repository and USE
	// information in each action line.
	emergeArgs := []string{"--pretend", "--verbose", "--color=n", fmt.Sprintf("--backtrack=%d", *backtrack)}
	if *operation == "update" {
		emergeArgs = append(emergeArgs, "--update")
	}
	if *completeGraph {
		emergeArgs = append(emergeArgs, "--complete-graph=y")
	}
	if *deep {
		emergeArgs = append(emergeArgs, "--deep")
	}
	if *newUse {
		emergeArgs = append(emergeArgs, "--newuse")
	}
	if *withBdeps != "auto" {
		emergeArgs = append(emergeArgs, "--with-bdeps="+*withBdeps)
	}
	emergeArgs = append(emergeArgs, *target)

	ariseResult := run(*arisePath, ariseArgs)
	emergeResult := run(*emergePath, emergeArgs)
	arisePlan, err := plancompare.ParseAriseJSON(ariseResult.stdout)
	if err != nil {
		fatal(err)
	}
	emergePlan, err := plancompare.ParseEmerge(emergeResult.stdout)
	if err != nil {
		fatal(err)
	}
	if len(arisePlan) == 0 && ariseResult.err != nil {
		fatal(fmt.Errorf("Arise produced no parseable actions: %w", ariseResult.err))
	}
	if len(emergePlan) == 0 && emergeResult.err != nil && !looksLikeEmergePlan(emergeResult.stdout) {
		fatal(fmt.Errorf("emerge produced no parseable actions: %w", emergeResult.err))
	}

	differences := plancompare.Compare(arisePlan, emergePlan)
	ariseVerified := parseAriseVerified(ariseResult.stdout)
	portageResolved := emergeResult.err == nil && !looksUnresolved(emergeResult.stderr)
	fixture, ariseValidationPlan, captured, captureErr := plancompare.ParseAriseValidation(ariseResult.stdout)
	if captureErr != nil {
		fatal(captureErr)
	}
	var portageValidationPlan planvalidate.Plan
	var portageValidationFixture planvalidate.Fixture
	var classified plancompare.ClassifiedComparison
	if *ariseStatePath == "" && *portageStatePath == "" && captured {
		ariseAssessment := plancompare.AssessmentFromValidation(
			planvalidate.ValidatePlanImpact(fixture, ariseValidationPlan),
			planvalidate.ApplyPlan(fixture.Installed, ariseValidationPlan).State,
		)
		portageAssessment, externalFixture, externalPlan, externalErr := plancompare.AssessmentFromExternalActions(fixture, emergePlan)
		if externalErr != nil {
			fatal(externalErr)
		}
		portageValidationPlan = externalPlan
		portageValidationFixture = externalFixture
		policy := plancompare.ClassificationPolicyForRequest(fixture.Request, ariseAssessment, portageAssessment)
		classified, err = plancompare.ClassifyFinalStates(ariseAssessment, portageAssessment, policy, differences)
		if err == nil && *captureDir != "" {
			err = writeComparisonCapture(*captureDir, *target, *operation, classified,
				plancompare.CaptureDocument(fixture, ariseValidationPlan),
				plancompare.CaptureDocument(portageValidationFixture, portageValidationPlan), policy)
		}
	} else {
		classified, err = classifyPlans(ariseVerified, portageResolved, differences, *ariseStatePath, *portageStatePath, *policyPath)
	}
	if err != nil {
		fatal(err)
	}
	r := report{
		AriseCount: len(arisePlan), EmergeCount: len(emergePlan),
		AriseVerified: ariseVerified, PortageResolved: portageResolved,
		ComparisonClass: classified.Class, Equivalent: classified.Equivalent,
		Accepted:    classificationAccepted(classified.Class),
		Differences: classified.ActionDiagnostics, StateDifferences: classified.Differences,
		ActionDiagnosticsTruncated: classified.ActionDiagnosticsTruncated,
		OmittedActionDiagnostics:   classified.OmittedActionDiagnostics,
		StateComparisonTruncated:   classified.Truncated,
		OmittedStateDifferences:    classified.OmittedDifferences,
	}
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(r); err != nil {
			fatal(err)
		}
	} else {
		fmt.Printf("Arise actions: %d (verified: %t)\nPortage actions: %d (resolved: %t)\nClass: %s\nAccepted: %t\nAction diagnostics: %d\nFinal-state differences: %d\n",
			r.AriseCount, r.AriseVerified, r.EmergeCount, r.PortageResolved,
			r.ComparisonClass, r.Accepted, len(r.Differences), len(r.StateDifferences))
		for _, difference := range r.Differences {
			fmt.Printf("  %-12s %s", difference.Kind, difference.Identity)
			if difference.Arise != nil {
				fmt.Printf("  arise=%s (%s)", difference.Arise.CPV(), difference.Arise.Kind)
			}
			if difference.Emerge != nil {
				fmt.Printf("  emerge=%s (%s)", difference.Emerge.CPV(), difference.Emerge.Kind)
			}
			if len(difference.UseMismatch) > 0 {
				fmt.Printf("  flags=%s", strings.Join(difference.UseMismatch, ","))
			}
			fmt.Println()
		}
		if r.OmittedActionDiagnostics > 0 {
			fmt.Printf("  %d additional action diagnostics omitted by bounds\n", r.OmittedActionDiagnostics)
		}
		for _, difference := range r.StateDifferences {
			fmt.Printf("  final-state %-12s %-24s class=%s\n",
				difference.Kind, difference.Identity, difference.Classification)
		}
	}
	if !r.Accepted {
		os.Exit(1)
	}
}

func classificationAccepted(class string) bool {
	switch class {
	case plancompare.ClassEquivalentValid, plancompare.ClassValidDivergence,
		plancompare.ClassAriseValidPortageInvalid:
		return true
	default:
		return false
	}
}

func classifyPlans(ariseVerified, portageResolved bool, actionDifferences []plancompare.Difference, ariseStatePath, portageStatePath, policyPath string) (plancompare.ClassifiedComparison, error) {
	if (ariseStatePath == "") != (portageStatePath == "") {
		return plancompare.ClassifiedComparison{}, fmt.Errorf("--arise-state and --portage-state must be provided together")
	}
	if ariseStatePath == "" {
		if ariseVerified && portageResolved && len(actionDifferences) == 0 {
			return plancompare.ClassifiedComparison{
				Class: plancompare.ClassEquivalentValid, Equivalent: true,
				Differences:       []plancompare.StateDifference{},
				ActionDiagnostics: append([]plancompare.Difference(nil), actionDifferences...),
			}, nil
		}
		return plancompare.ClassifiedComparison{
			Class: plancompare.ClassInconclusive, Differences: []plancompare.StateDifference{},
			ActionDiagnostics: append([]plancompare.Difference(nil), actionDifferences...),
		}, nil
	}
	ariseState, err := readStateAssessment(ariseStatePath)
	if err != nil {
		return plancompare.ClassifiedComparison{}, err
	}
	portageState, err := readStateAssessment(portageStatePath)
	if err != nil {
		return plancompare.ClassifiedComparison{}, err
	}
	var policy plancompare.ClassificationPolicy
	if policyPath != "" {
		file, err := os.Open(policyPath)
		if err != nil {
			return plancompare.ClassifiedComparison{}, fmt.Errorf("open classification policy: %w", err)
		}
		defer file.Close()
		policy, err = plancompare.DecodePolicyDocument(file)
		if err != nil {
			return plancompare.ClassifiedComparison{}, err
		}
	}
	return plancompare.ClassifyFinalStates(ariseState, portageState, policy, actionDifferences)
}

func readStateAssessment(path string) (plancompare.StateAssessment, error) {
	file, err := os.Open(path)
	if err != nil {
		return plancompare.StateAssessment{}, fmt.Errorf("open final-state assessment %s: %w", path, err)
	}
	defer file.Close()
	state, err := plancompare.DecodeStateDocument(file)
	if err != nil {
		return plancompare.StateAssessment{}, fmt.Errorf("%s: %w", path, err)
	}
	return state, nil
}

func looksLikeEmergePlan(output string) bool {
	return strings.Contains(output, "These are the packages that would be merged") ||
		strings.Contains(output, "Total: 0 packages") ||
		strings.Contains(output, "following update(s) have been skipped")
}

func run(path string, args []string) commandResult {
	cmd := exec.Command(path, args...)
	if filepath.Base(path) == "emerge" {
		cmd.Env = withoutNews(os.Environ())
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return commandResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func parseAriseVerified(output string) bool {
	var envelope struct {
		Complete   bool `json:"complete"`
		Resolution struct {
			Verified     bool   `json:"verified"`
			Verification string `json:"verification"`
		} `json:"resolution"`
	}
	return json.Unmarshal([]byte(output), &envelope) == nil && envelope.Complete && envelope.Resolution.Verified && envelope.Resolution.Verification == "verified"
}

func looksUnresolved(stderr string) bool {
	return strings.Contains(stderr, "resulting in a slot conflict") || strings.Contains(stderr, "impossible to satisfy simultaneously") || strings.Contains(stderr, "unsatisfied")
}

func withoutNews(environment []string) []string {
	result := append([]string(nil), environment...)
	for i, entry := range result {
		if strings.HasPrefix(entry, "FEATURES=") {
			result[i] = entry + " -news"
			return result
		}
	}
	return append(result, "FEATURES=-news")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "plan comparison:", err)
	os.Exit(2)
}
