package pythoncleaner

import (
	"reflect"
	"testing"
)

func TestBuildPlanEnforcesTransitionOrdering(t *testing.T) {
	report := Report{
		Policy:  Policy{Targets: []string{"python3_14"}, Preference: []string{"python3_13", "python3_14"}},
		Missing: []string{"python3_14"},
		Consumers: []Consumer{
			{CPV: "dev-python/Z-1", Atom: "dev-python/Z:0"},
			{CPV: "dev-python/A-1", Atom: "dev-python/A:0"},
		},
		Removals: []Removal{
			{Interpreter: Interpreter{CPV: "dev-lang/python-3.10.20"}, Safe: true},
			{Interpreter: Interpreter{CPV: "dev-lang/python-3.13.13"}, Blockers: []string{"python-exec preference"}},
		},
	}
	plan := BuildPlan(report)
	var names []string
	for _, stage := range plan.Stages {
		names = append(names, stage.Name)
	}
	if !reflect.DeepEqual(names, []string{
		"bootstrap-interpreters", "repair-cohort", "repair-cohort", "validate-runtime",
		"switch-preference", "remove-obsolete-interpreters",
	}) {
		t.Fatalf("stage order = %v", names)
	}
	if !reflect.DeepEqual(plan.Stages[0].Targets, []string{"dev-lang/python:3.14"}) ||
		!reflect.DeepEqual(plan.Stages[1].Targets, []string{"dev-python/A:0"}) ||
		!reflect.DeepEqual(plan.Stages[2].Targets, []string{"dev-python/Z:0"}) ||
		!reflect.DeepEqual(plan.Stages[5].Targets, []string{"=dev-lang/python-3.10.20"}) {
		t.Fatalf("plan = %#v", plan)
	}
	if got := RebuildTargets(plan); !reflect.DeepEqual(got, []string{
		"dev-lang/python:3.14", "dev-python/A:0", "dev-python/Z:0",
	}) {
		t.Fatalf("rebuild targets = %v", got)
	}
}

func TestBuildPlanPinsExactInstalledVersionPerCohort(t *testing.T) {
	report := Report{
		Policy: Policy{Targets: []string{"python3_14"}, Preference: []string{"python3_14"}},
		Consumers: []Consumer{
			{CPV: "dev-python/A-1", Atom: "dev-python/A:0"},
			{CPV: "dev-python/B-2", Atom: "dev-python/B:0"},
		},
	}
	plan := BuildPlanWithTargets(report, func(consumer Consumer) (string, bool) {
		return "=" + consumer.CPV, true
	})
	if got := RebuildTargets(plan); !reflect.DeepEqual(got, []string{
		"=dev-python/A-1", "=dev-python/B-2",
	}) {
		t.Fatalf("rebuild targets = %v", got)
	}
	for _, stage := range plan.Stages {
		if stage.Name == "repair-cohort" && len(stage.Targets) != 1 {
			t.Fatalf("cohort is not isolated: %#v", stage)
		}
	}
}

func TestBuildPlanGroupsDependencyConnectedRepairIsland(t *testing.T) {
	report := Report{
		Policy: Policy{Targets: []string{"python3_14"}, Preference: []string{"python3_14"}},
		Consumers: []Consumer{
			{CPV: "dev-python/ecdsa-1", Atom: "dev-python/ecdsa:0", Dependencies: []string{"dev-python/gmpy2", "dev-python/six"}},
			{CPV: "dev-python/gmpy2-2", Atom: "dev-python/gmpy2:2"},
			{CPV: "dev-python/independent-1", Atom: "dev-python/independent:0"},
			{CPV: "dev-python/six-1", Atom: "dev-python/six:0"},
		},
	}
	plan := BuildPlanWithTargets(report, func(consumer Consumer) (string, bool) {
		return "=" + consumer.CPV, true
	})
	var cohorts [][]string
	for _, stage := range plan.Stages {
		if stage.Name == "repair-cohort" {
			cohorts = append(cohorts, stage.Targets)
		}
	}
	want := [][]string{
		{"=dev-python/ecdsa-1", "=dev-python/gmpy2-2", "=dev-python/six-1"},
		{"=dev-python/independent-1"},
	}
	if !reflect.DeepEqual(cohorts, want) {
		t.Fatalf("cohorts = %v, want %v", cohorts, want)
	}
}

func TestDependencyCohortsAreDeterministicAcrossCycles(t *testing.T) {
	consumers := map[string]Consumer{
		"dev-python/A": {Atom: "dev-python/A:0", Dependencies: []string{"dev-python/B"}},
		"dev-python/B": {Atom: "dev-python/B:0", Dependencies: []string{"dev-python/A"}},
	}
	want := [][]string{{"dev-python/A", "dev-python/B"}}
	for range 20 {
		if got := dependencyCohorts(consumers); !reflect.DeepEqual(got, want) {
			t.Fatalf("cohorts = %v", got)
		}
	}
}

func TestBuildPlanNeverRemovesBlockedInterpreter(t *testing.T) {
	report := Report{
		Policy: Policy{Targets: []string{"python3_14"}, Preference: []string{"python3_14"}},
		Removals: []Removal{{
			Interpreter: Interpreter{CPV: "dev-lang/python-3.13.13"}, Safe: false,
			Blockers: []string{"dev-python/Foo"},
		}},
	}
	plan := BuildPlan(report)
	for _, stage := range plan.Stages {
		if stage.Name == "remove-obsolete-interpreters" {
			t.Fatalf("blocked removal entered plan: %#v", stage)
		}
	}
}

func TestBuildPlanSeparatesUnavailableConsumersFromResolverTargets(t *testing.T) {
	report := Report{
		Policy: Policy{Targets: []string{"python3_14"}, Preference: []string{"python3_14"}},
		Consumers: []Consumer{
			{CPV: "dev-python/Available-1", Atom: "dev-python/Available:0"},
			{CPV: "dev-python/Gone-1", Atom: "dev-python/Gone:0"},
		},
	}
	plan := BuildPlanWithAvailability(report, func(atom string) bool {
		return atom == "dev-python/Available:0"
	})
	if !reflect.DeepEqual(RebuildTargets(plan), []string{"dev-python/Available:0"}) {
		t.Fatalf("rebuild targets = %v", RebuildTargets(plan))
	}
	var unavailable []string
	for _, stage := range plan.Stages {
		if stage.Name == "unavailable-consumers" {
			unavailable = stage.Targets
		}
	}
	if !reflect.DeepEqual(unavailable, []string{"dev-python/Gone-1"}) {
		t.Fatalf("unavailable = %v", unavailable)
	}
}
