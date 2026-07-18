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
	AriseCount  int                      `json:"arise_count"`
	EmergeCount int                      `json:"emerge_count"`
	Equivalent  bool                     `json:"equivalent"`
	Differences []plancompare.Difference `json:"differences,omitempty"`
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

	ariseOutput, ariseRunErr := run(*arisePath, ariseArgs)
	emergeOutput, emergeRunErr := run(*emergePath, emergeArgs)
	arisePlan, err := plancompare.ParseAriseJSON(ariseOutput)
	if err != nil {
		fatal(err)
	}
	emergePlan, err := plancompare.ParseEmerge(emergeOutput)
	if err != nil {
		fatal(err)
	}
	if len(arisePlan) == 0 && ariseRunErr != nil {
		fatal(fmt.Errorf("Arise produced no parseable actions: %w", ariseRunErr))
	}
	if len(emergePlan) == 0 && emergeRunErr != nil && !looksLikeEmergePlan(emergeOutput) {
		fatal(fmt.Errorf("emerge produced no parseable actions: %w", emergeRunErr))
	}

	differences := plancompare.Compare(arisePlan, emergePlan)
	r := report{AriseCount: len(arisePlan), EmergeCount: len(emergePlan), Equivalent: len(differences) == 0, Differences: differences}
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(r); err != nil {
			fatal(err)
		}
	} else {
		fmt.Printf("Arise actions: %d\nPortage actions: %d\nDifferences: %d\n", r.AriseCount, r.EmergeCount, len(differences))
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

func run(path string, args []string) (string, error) {
	cmd := exec.Command(path, args...)
	if filepath.Base(path) == "emerge" {
		cmd.Env = withoutNews(os.Environ())
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	// Plans with conflicts commonly exit non-zero. Preserve stderr only when
	// stdout has no plan, so Portage news/config warnings cannot pollute parsing.
	if stdout.Len() == 0 {
		stdout.Write(stderr.Bytes())
	}
	return stdout.String(), err
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
