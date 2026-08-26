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

	"github.com/airencracken/arise/internal/moveinst"
	"github.com/airencracken/arise/internal/oplock"
	"github.com/airencracken/arise/internal/portage"
)

type maintainMoveInstDocument struct {
	Schema      int               `json:"schema"`
	Operation   string            `json:"operation"`
	Complete    bool              `json:"complete"`
	StateSHA256 string            `json:"state_sha256"`
	PlanSHA256  string            `json:"plan_sha256"`
	VDB         string            `json:"vdb"`
	Issues      []moveinst.Issue  `json:"issues"`
	Actions     []moveinst.Action `json:"actions"`
}

var maintainMoveInstBeforeLock = func() error { return nil }

func runMaintainMoveInst(args []string) int {
	check, fix := false, false
	for _, arg := range args {
		switch arg {
		case "--check":
			check = true
		case "--fix":
			fix = true
		case "-h", "--help":
			fmt.Println("Usage: arise [global options] maintain moveinst --check|--fix")
			return 0
		default:
			fmt.Fprintf(os.Stderr, "maintain moveinst: unknown option %q\n", arg)
			return 2
		}
	}
	if check == fix {
		fmt.Fprintln(os.Stderr, "maintain moveinst: require exactly one of --check or --fix")
		return 2
	}
	repositories := maintainMoveInstRepositories(*repoPath, *portageConfigRoot)
	report, err := moveinst.Check(*vdbDir, repositories)
	if err != nil {
		fmt.Fprintf(os.Stderr, "maintain moveinst: %v\n", err)
		return 1
	}
	state, err := maintainMoveInstStateSHA256(*vdbDir, *portageConfigRoot, repositories)
	if err != nil {
		fmt.Fprintf(os.Stderr, "maintain moveinst: fingerprint state: %v\n", err)
		return 1
	}
	document := newMaintainMoveInstDocument(report, state)
	if check {
		if *jsonOutput {
			if err := writeMaintainMoveInstJSON(os.Stdout, document); err != nil {
				fmt.Fprintf(os.Stderr, "maintain moveinst: %v\n", err)
				return 1
			}
		} else {
			printMaintainMoveInstReport(os.Stdout, report)
		}
		if len(report.Issues) != 0 {
			return 1
		}
		return 0
	}
	if *jsonOutput || strings.TrimSpace(*savePlan) != "" || *pretend {
		if err := emitSavableMutationPlan("maintain-moveinst", document); err != nil {
			fmt.Fprintf(os.Stderr, "maintain moveinst: %v\n", err)
			return 1
		}
		if !*jsonOutput {
			printMaintainMoveInstPlan(os.Stdout, document)
		}
		if *pretend {
			return 0
		}
	}
	if len(report.Actions) == 0 {
		if !*jsonOutput {
			fmt.Fprintln(os.Stdout, "Installed package metadata is current.")
		}
		return 0
	}
	if strings.TrimSpace(*approvePlanSHA256) != "" || strings.TrimSpace(*approvePlan) != "" {
		approved, err := approvedPlanDigest(*approvePlanSHA256, *approvePlan, *planDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "maintain moveinst: refusing repair: %v\n", err)
			return 1
		}
		if err := validatePlanAuthorization(approved, document.PlanSHA256); err != nil {
			fmt.Fprintf(os.Stderr, "maintain moveinst: refusing repair: %v\n", err)
			return 1
		}
	}
	if err := maintainMoveInstBeforeLock(); err != nil {
		fmt.Fprintf(os.Stderr, "maintain moveinst: interrupted before repair: %v\n", err)
		return 1
	}
	lock, err := oplock.TryAcquireVDB(*vdbDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "maintain moveinst: acquire VDB lock: %v\n", err)
		return 1
	}
	defer lock.Release()
	lockedState, err := maintainMoveInstStateSHA256(*vdbDir, *portageConfigRoot, repositories)
	if err != nil {
		fmt.Fprintf(os.Stderr, "maintain moveinst: revalidate state: %v\n", err)
		return 1
	}
	if lockedState != document.StateSHA256 {
		fmt.Fprintln(os.Stderr, "maintain moveinst: state changed after planning; generate a new repair plan")
		return 1
	}
	if err := moveinst.Apply(report.Actions, moveinst.ApplyConfig{
		RootDir: commandEnv("ROOT", "/"), VDBRoot: *vdbDir, JournalDir: *journalDir,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "maintain moveinst: commit repair: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "Updated installed package metadata: %d package action(s) applied.\n", len(report.Actions))
	return 0
}

func newMaintainMoveInstDocument(report moveinst.Report, state string) maintainMoveInstDocument {
	document := maintainMoveInstDocument{
		Schema: 1, Operation: "maintain-moveinst", Complete: true, StateSHA256: state, VDB: *vdbDir,
		Issues: append([]moveinst.Issue(nil), report.Issues...), Actions: append([]moveinst.Action(nil), report.Actions...),
	}
	document.PlanSHA256 = maintainMoveInstPlanSHA256(document)
	return document
}

func maintainMoveInstPlanSHA256(document maintainMoveInstDocument) string {
	document.PlanSHA256 = ""
	encoded, err := json.Marshal(document)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func maintainMoveInstStateSHA256(vdbRoot, configRoot string, repositories []moveinst.Repository) (string, error) {
	hash := sha256.New()
	if err := hashStatePath(hash, "vdb", vdbRoot); err != nil {
		return "", err
	}
	if err := hashStatePath(hash, "repos.conf", filepath.Join(configRoot, "repos.conf")); err != nil {
		return "", err
	}
	for _, repository := range repositories {
		if err := hashStatePath(hash, "updates:"+repository.Name, filepath.Join(repository.Root, "profiles", "updates")); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func maintainMoveInstRepositories(primary, configRoot string) []moveinst.Repository {
	var result []moveinst.Repository
	seen := map[string]bool{}
	entries, err := portage.EffectiveRepositoryPolicyOrder(configRoot)
	if err == nil {
		for _, entry := range entries {
			if entry.Location == "" || seen[entry.Location] {
				continue
			}
			seen[entry.Location] = true
			result = append(result, moveinst.Repository{
				Name: entry.Name, Root: entry.Location, Master: filepath.Clean(entry.Location) == filepath.Clean(primary),
			})
		}
	}
	if !seen[primary] {
		name := filepath.Base(primary)
		if data, err := os.ReadFile(filepath.Join(primary, "profiles", "repo_name")); err == nil && strings.TrimSpace(string(data)) != "" {
			name = strings.TrimSpace(string(data))
		}
		result = append(result, moveinst.Repository{Name: name, Root: primary, Master: true})
	}
	hasMaster := false
	for _, repository := range result {
		hasMaster = hasMaster || repository.Master
	}
	if !hasMaster && len(result) != 0 {
		result[0].Master = true
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Master != result[j].Master {
			return result[i].Master
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func writeMaintainMoveInstJSON(writer io.Writer, document maintainMoveInstDocument) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(document)
}

func printMaintainMoveInstReport(writer io.Writer, report moveinst.Report) {
	if len(report.Issues) == 0 {
		fmt.Fprintln(writer, "Installed package metadata is current.")
		return
	}
	fmt.Fprintf(writer, "Installed package metadata has %d issue(s):\n", len(report.Issues))
	for _, issue := range report.Issues {
		fmt.Fprintf(writer, "  [%s] %s\n", issue.Kind, issue.Message)
	}
}

func printMaintainMoveInstPlan(writer io.Writer, document maintainMoveInstDocument) {
	fmt.Fprintf(writer, "Installed package move plan (%d package action(s)):\n", len(document.Actions))
	for _, action := range document.Actions {
		fmt.Fprintf(writer, "  %s -> %s\n", action.CPV, action.ResultCPV)
		for _, reason := range action.Reasons {
			fmt.Fprintf(writer, "    %s\n", reason)
		}
	}
	fmt.Fprintf(writer, "Plan SHA-256: %s\n", document.PlanSHA256)
}
