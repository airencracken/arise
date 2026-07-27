package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/oplock"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/world"
	"github.com/airencracken/arise/internal/worldmaint"
)

type maintainWorldDocument struct {
	Schema        int                  `json:"schema"`
	Operation     string               `json:"operation"`
	Complete      bool                 `json:"complete"`
	StateSHA256   string               `json:"state_sha256"`
	PlanSHA256    string               `json:"plan_sha256"`
	World         string               `json:"world"`
	Entries       []string             `json:"entries"`
	ResultEntries []string             `json:"result_entries"`
	Issues        []worldmaint.Issue   `json:"issues"`
	Actions       []worldmaint.Action  `json:"actions"`
	Summary       maintainWorldSummary `json:"summary"`
}

type maintainWorldSummary struct {
	Entries int `json:"entries"`
	Issues  int `json:"issues"`
	Actions int `json:"actions"`
}

func runMaintain(args []string) int {
	if len(args) == 0 || args[0] != "world" {
		fmt.Fprintln(os.Stderr, "maintain: expected `world --check` or `world --fix`")
		return 2
	}
	check, fix := false, false
	for _, arg := range args[1:] {
		switch arg {
		case "--check":
			check = true
		case "--fix":
			fix = true
		case "-h", "--help":
			fmt.Println("Usage: arise [global options] maintain world --check|--fix")
			return 0
		default:
			fmt.Fprintf(os.Stderr, "maintain world: unknown option %q\n", arg)
			return 2
		}
	}
	if check == fix {
		fmt.Fprintln(os.Stderr, "maintain world: require exactly one of --check or --fix")
		return 2
	}
	cfg, err := portage.LoadEffectiveConfig(*portageConfigRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "maintain world: load Portage configuration: %v\n", err)
		return 1
	}
	repositories := maintainRepositoryRoots(*repoPath, *portageConfigRoot)
	report, err := worldmaint.CheckRepositories(*worldFile, *vdbDir, repositories, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "maintain world: %v\n", err)
		return 1
	}
	state, err := maintainWorldStateSHA256(*worldFile, *vdbDir, *portageConfigRoot, repositories)
	if err != nil {
		fmt.Fprintf(os.Stderr, "maintain world: fingerprint state: %v\n", err)
		return 1
	}
	document := newMaintainWorldDocument(report, state)

	if check {
		if *jsonOutput {
			if err := writeMaintainWorldJSON(os.Stdout, document); err != nil {
				fmt.Fprintf(os.Stderr, "maintain world: %v\n", err)
				return 1
			}
		} else {
			printMaintainWorldReport(os.Stdout, report)
		}
		if len(report.Issues) != 0 {
			return 1
		}
		return 0
	}

	if *jsonOutput || strings.TrimSpace(*savePlan) != "" || *pretend {
		if err := emitSavableMutationPlan("maintain-world", document); err != nil {
			fmt.Fprintf(os.Stderr, "maintain world: %v\n", err)
			return 1
		}
		if !*jsonOutput {
			printMaintainWorldPlan(os.Stdout, document)
		}
		if *pretend {
			return 0
		}
	}
	if len(report.Actions) == 0 {
		if !*jsonOutput {
			fmt.Fprintln(os.Stdout, "World file is clean.")
		}
		return 0
	}
	if strings.TrimSpace(*approvePlanSHA256) != "" || strings.TrimSpace(*approvePlan) != "" {
		approved, err := approvedPlanDigest(*approvePlanSHA256, *approvePlan, *planDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "maintain world: refusing repair: %v\n", err)
			return 1
		}
		if err := validatePlanAuthorization(approved, document.PlanSHA256); err != nil {
			fmt.Fprintf(os.Stderr, "maintain world: refusing repair: %v\n", err)
			fmt.Fprintf(os.Stderr, "Generate a reviewable plan with --pretend --save-plan NAME.\n")
			return 1
		}
	}
	lock, err := oplock.TryAcquirePath(*worldFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "maintain world: acquire world lock: %v\n", err)
		return 1
	}
	defer lock.Release()
	lockedState, err := maintainWorldStateSHA256(*worldFile, *vdbDir, *portageConfigRoot, repositories)
	if err != nil {
		fmt.Fprintf(os.Stderr, "maintain world: revalidate state: %v\n", err)
		return 1
	}
	if lockedState != document.StateSHA256 {
		fmt.Fprintln(os.Stderr, "maintain world: state changed after planning; generate a new repair plan")
		return 1
	}
	set, err := world.LoadWorld(*worldFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "maintain world: reload world: %v\n", err)
		return 1
	}
	set.Atoms = worldmaint.Apply(report.Entries, report.Actions)
	if err := set.Save(*worldFile); err != nil {
		fmt.Fprintf(os.Stderr, "maintain world: commit repair: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "Repaired world file: %d action(s) applied.\n", len(report.Actions))
	return 0
}

func newMaintainWorldDocument(report worldmaint.Report, state string) maintainWorldDocument {
	document := maintainWorldDocument{
		Schema: 1, Operation: "maintain-world", Complete: true, StateSHA256: state,
		World: *worldFile, Entries: append([]string(nil), report.Entries...),
		ResultEntries: worldmaint.Apply(report.Entries, report.Actions),
		Issues:        append([]worldmaint.Issue(nil), report.Issues...),
		Actions:       append([]worldmaint.Action(nil), report.Actions...),
		Summary:       maintainWorldSummary{Entries: len(report.Entries), Issues: len(report.Issues), Actions: len(report.Actions)},
	}
	document.PlanSHA256 = maintainWorldPlanSHA256(document)
	return document
}

func maintainWorldPlanSHA256(document maintainWorldDocument) string {
	document.PlanSHA256 = ""
	encoded, err := json.Marshal(document)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func maintainWorldStateSHA256(worldPath, vdbRoot, configRoot string, repoRoots []string) (string, error) {
	hash := sha256.New()
	inputs := []struct{ label, path string }{
		{"world", worldPath},
		{"vdb", vdbRoot},
		{"portage-config", configRoot},
	}
	for _, repoRoot := range repoRoots {
		label := "repository:" + repoRoot
		inputs = append(inputs,
			struct{ label, path string }{label + ":cache", repoRoot + "/metadata/md5-cache"},
			struct{ label, path string }{label + ":profiles", repoRoot + "/profiles"},
		)
	}
	for _, input := range inputs {
		if err := hashStatePath(hash, input.label, input.path); err != nil {
			return "", err
		}
	}
	for _, repoRoot := range repoRoots {
		var ebuilds []string
		err := filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				switch entry.Name() {
				case ".git", "metadata", "profiles", "eclass", "distfiles":
					if path != repoRoot {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if strings.HasSuffix(entry.Name(), ".ebuild") {
				ebuilds = append(ebuilds, path)
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		sort.Strings(ebuilds)
		for _, ebuild := range ebuilds {
			relative, err := filepath.Rel(repoRoot, ebuild)
			if err != nil {
				return "", err
			}
			if err := hashStatePath(hash, "repository-ebuild:"+repoRoot+":"+filepath.ToSlash(relative), ebuild); err != nil {
				return "", err
			}
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func maintainRepositoryRoots(primary, configRoot string) []string {
	roots := []string{primary}
	seen := map[string]bool{primary: true}
	repositories, err := portage.ReadReposConf(configRoot + "/repos.conf")
	if err != nil {
		return roots
	}
	for _, repository := range repositories {
		root := strings.TrimSpace(repository.Location)
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		roots = append(roots, root)
	}
	return roots
}

func writeMaintainWorldJSON(writer io.Writer, document maintainWorldDocument) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(document)
}

func printMaintainWorldReport(writer io.Writer, report worldmaint.Report) {
	if len(report.Issues) == 0 {
		fmt.Fprintln(writer, "World file is clean.")
		return
	}
	fmt.Fprintf(writer, "World file has %d issue(s):\n", len(report.Issues))
	for _, issue := range report.Issues {
		fmt.Fprintf(writer, "  [%s] %s\n", issue.Kind, issue.Message)
	}
}

func printMaintainWorldPlan(writer io.Writer, document maintainWorldDocument) {
	fmt.Fprintf(writer, "World repair plan (%d action(s)):\n", len(document.Actions))
	actions := append([]worldmaint.Action(nil), document.Actions...)
	sort.SliceStable(actions, func(i, j int) bool { return actions[i].Entry < actions[j].Entry })
	for _, action := range actions {
		if action.Action == "replace" {
			fmt.Fprintf(writer, "  replace %s with %s (%s)\n", action.Entry, action.Value, action.Reason)
		} else {
			fmt.Fprintf(writer, "  %s %s (%s)\n", action.Action, action.Entry, action.Reason)
		}
	}
	fmt.Fprintf(writer, "Plan SHA-256: %s\n", document.PlanSHA256)
}
