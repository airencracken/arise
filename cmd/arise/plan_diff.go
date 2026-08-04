package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/plandiff"
	"github.com/airencracken/arise/internal/planvalidate"
)

const maxSavedPlanBytes = 16 << 20

func runPlanDiff(args []string) int {
	options := flag.NewFlagSet("plan-diff", flag.ContinueOnError)
	options.SetOutput(os.Stderr)
	jsonOutput := options.Bool("json", false, "emit the versioned plan-diff document")
	if err := options.Parse(args); err != nil {
		return 2
	}
	if options.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "plan-diff: require BEFORE and AFTER saved plan paths")
		return 2
	}
	diff, err := compareSavedPlanFiles(options.Arg(0), options.Arg(1))
	if err != nil {
		fmt.Fprintf(os.Stderr, "plan-diff: %v\n", err)
		return 1
	}
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(diff); err != nil {
			fmt.Fprintf(os.Stderr, "plan-diff: encode: %v\n", err)
			return 1
		}
		return 0
	}
	if len(diff.Changes) == 0 {
		fmt.Println("Plans have identical package actions.")
	}
	for _, change := range diff.Context {
		fmt.Printf("context changed %s: %s -> %s\n", change.Field, change.Before, change.After)
	}
	for _, change := range diff.Changes {
		if len(change.Fields) == 0 {
			fmt.Printf("%s %s\n", change.Kind, change.Identity)
			continue
		}
		fmt.Printf("%s %s (%s)\n", change.Kind, change.Identity, strings.Join(change.Fields, ", "))
	}
	return 0
}

func compareSavedPlanFiles(beforePath, afterPath string) (plandiff.Diff, error) {
	before, err := readSavedPlan(beforePath)
	if err != nil {
		return plandiff.Diff{}, fmt.Errorf("read before plan: %w", err)
	}
	after, err := readSavedPlan(afterPath)
	if err != nil {
		return plandiff.Diff{}, fmt.Errorf("read after plan: %w", err)
	}
	beforePlan, err := validationPlanForDiff(before)
	if err != nil {
		return plandiff.Diff{}, fmt.Errorf("before plan: %w", err)
	}
	afterPlan, err := validationPlanForDiff(after)
	if err != nil {
		return plandiff.Diff{}, fmt.Errorf("after plan: %w", err)
	}
	diff, err := plandiff.Compare(beforePlan, afterPlan)
	if err != nil {
		return plandiff.Diff{}, err
	}
	diff.Context = savedPlanContextChanges(before, after)
	return diff, nil
}

func savedPlanContextChanges(before, after jsonPlan) []plandiff.ContextChange {
	encodeTargets := func(targets []string) string {
		encoded, _ := json.Marshal(targets)
		return string(encoded)
	}
	values := []struct{ field, before, after string }{
		{"operation", before.Operation, after.Operation},
		{"targets", encodeTargets(before.Targets), encodeTargets(after.Targets)},
		{"complete", strconv.FormatBool(before.Complete), strconv.FormatBool(after.Complete)},
		{"state_sha256", before.StateSHA256, after.StateSHA256},
		{"plan_sha256", before.PlanSHA256, after.PlanSHA256},
	}
	changes := []plandiff.ContextChange{}
	for _, value := range values {
		if value.before != value.after {
			changes = append(changes, plandiff.ContextChange{Field: value.field, Before: value.before, After: value.after})
		}
	}
	return changes
}

func readSavedPlan(path string) (jsonPlan, error) {
	file, err := os.Open(path)
	if err != nil {
		return jsonPlan{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSavedPlanBytes+1))
	if err != nil {
		return jsonPlan{}, err
	}
	if len(data) > maxSavedPlanBytes {
		return jsonPlan{}, fmt.Errorf("document exceeds %d bytes", maxSavedPlanBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document jsonPlan
	if err := decoder.Decode(&document); err != nil {
		return jsonPlan{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return jsonPlan{}, fmt.Errorf("unexpected trailing JSON value")
		}
		return jsonPlan{}, err
	}
	if document.Schema != 1 {
		return jsonPlan{}, fmt.Errorf("unsupported plan schema %d", document.Schema)
	}
	if strings.TrimSpace(document.Operation) == "" || document.Targets == nil || document.Actions == nil || document.Conflicts == nil || document.Warnings == nil {
		return jsonPlan{}, fmt.Errorf("incomplete saved plan document")
	}
	return document, nil
}

func validationPlanForDiff(document jsonPlan) (planvalidate.Plan, error) {
	plan := planvalidate.Plan{Schema: planvalidate.SchemaVersion, Actions: []planvalidate.Action{}}
	appendAction := func(item jsonAction) error {
		parsed, err := atom.Parse("=" + item.CPV)
		if err != nil || parsed.Version == nil {
			return fmt.Errorf("invalid action CPV %q", item.CPV)
		}
		if item.Action == "" || item.Domain == "" {
			return fmt.Errorf("action %s lacks action or domain", item.CPV)
		}
		cp := parsed.CP()
		identity := strings.Join([]string{cp, item.Slot, item.Repository, item.Domain}, "|")
		use := make(map[string]bool, len(item.UseEnabled)+len(item.UseDisabled))
		for _, flag := range item.UseEnabled {
			use[flag] = true
		}
		for _, flag := range item.UseDisabled {
			use[flag] = false
		}
		plan.Actions = append(plan.Actions, planvalidate.Action{
			ID: identity, Kind: item.Action, Prerequisites: append([]string(nil), item.Prerequisites...),
			Package: planvalidate.Package{CPV: item.CPV, Slot: item.Slot, Subslot: item.Subslot, Repository: item.Repository, Authority: planvalidate.AuthorityEvaluated, Use: use},
		})
		return nil
	}
	for _, item := range document.Actions {
		if err := appendAction(item); err != nil {
			return planvalidate.Plan{}, err
		}
	}
	for _, item := range document.Uninstall {
		if err := appendAction(item); err != nil {
			return planvalidate.Plan{}, err
		}
	}
	return plan, nil
}
