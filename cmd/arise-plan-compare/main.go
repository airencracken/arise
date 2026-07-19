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
)

type report struct {
	AriseCount      int                      `json:"arise_count"`
	EmergeCount     int                      `json:"emerge_count"`
	AriseVerified   bool                     `json:"arise_verified"`
	PortageResolved bool                     `json:"portage_resolved"`
	ComparisonClass string                   `json:"comparison_class"`
	Equivalent      bool                     `json:"equivalent"`
	Differences     []plancompare.Difference `json:"differences,omitempty"`
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
	flag.Parse()
	if *withBdeps != "auto" && *withBdeps != "y" && *withBdeps != "n" {
		fatal(fmt.Errorf("--with-bdeps must be auto, y, or n"))
	}

	ariseArgs := []string{"--json", "--pretend", fmt.Sprintf("--backtrack=%d", *backtrack)}
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
	comparisonClass := classifyComparison(ariseVerified, portageResolved, len(differences) == 0)
	r := report{AriseCount: len(arisePlan), EmergeCount: len(emergePlan), AriseVerified: ariseVerified, PortageResolved: portageResolved, ComparisonClass: comparisonClass, Equivalent: comparisonClass == "equivalent-verified", Differences: differences}
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(r); err != nil {
			fatal(err)
		}
	} else {
		fmt.Printf("Arise actions: %d (verified: %t)\nPortage actions: %d (resolved: %t)\nClass: %s\nDifferences: %d\n", r.AriseCount, r.AriseVerified, r.EmergeCount, r.PortageResolved, r.ComparisonClass, len(differences))
		for _, difference := range differences {
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
	}
	if !r.Equivalent {
		os.Exit(1)
	}
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

func classifyComparison(ariseVerified, portageResolved, sameActions bool) string {
	switch {
	case ariseVerified && portageResolved && sameActions:
		return "equivalent-verified"
	case ariseVerified && !portageResolved:
		return "verified-repair-vs-unresolved-partial"
	case !ariseVerified && portageResolved:
		return "arise-unverified-vs-portage-resolved"
	default:
		return "non-equivalent"
	}
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
