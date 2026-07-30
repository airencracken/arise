package pythoncleaner

import (
	"sort"
	"strings"
)

type Stage struct {
	Name    string   `json:"name"`
	Targets []string `json:"targets"`
	Checks  []string `json:"checks"`
}

type Plan struct {
	Stages []Stage `json:"stages"`
}

func BuildPlan(report Report) Plan {
	return BuildPlanWithTargets(report, func(consumer Consumer) (string, bool) {
		return consumer.Atom, true
	})
}

func BuildPlanWithAvailability(report Report, available func(string) bool) Plan {
	return BuildPlanWithTargets(report, func(consumer Consumer) (string, bool) {
		if available == nil || available(consumer.Atom) {
			return consumer.Atom, true
		}
		return "", false
	})
}

// BuildPlanWithTargets constructs isolated repair cohorts. resolveTarget may
// pin an installed CPV when its ebuild remains available, or select a
// replacement atom when recovery must cross a removed historical version.
func BuildPlanWithTargets(report Report, resolveTarget func(Consumer) (string, bool)) Plan {
	plan := Plan{Stages: []Stage{}}
	if len(report.Missing) != 0 {
		var targets []string
		for _, target := range report.Missing {
			if slot := targetSlot(target); slot != "" {
				targets = append(targets, "dev-lang/python:"+slot)
			}
		}
		sort.Strings(targets)
		plan.Stages = append(plan.Stages, Stage{
			Name: "bootstrap-interpreters", Targets: targets,
			Checks: []string{"installed interpreter slots satisfy effective PYTHON_TARGETS"},
		})
	}
	if len(report.Consumers) != 0 {
		var unavailable []string
		consumers := append([]Consumer(nil), report.Consumers...)
		sort.Slice(consumers, func(i, j int) bool { return consumers[i].CPV < consumers[j].CPV })
		targets := map[string]string{}
		availableConsumers := map[string]Consumer{}
		for _, consumer := range consumers {
			target, ok := resolveTarget(consumer)
			if !ok || strings.TrimSpace(target) == "" {
				unavailable = append(unavailable, consumer.CPV)
				continue
			}
			cp := consumerCP(consumer)
			targets[cp] = target
			availableConsumers[cp] = consumer
		}
		for _, cohort := range dependencyCohorts(availableConsumers) {
			var cohortTargets []string
			for _, cp := range cohort {
				cohortTargets = append(cohortTargets, targets[cp])
			}
			sort.Strings(cohortTargets)
			plan.Stages = append(plan.Stages, Stage{
				Name: "repair-cohort", Targets: cohortTargets,
				Checks: []string{
					"rebuilt packages own no obsolete Python paths",
					"rebuilt ELF objects require no obsolete libpython SONAME",
					"owned scripts use a policy-supported interpreter",
					"whole installed state remains independently valid",
				},
			})
		}
		if len(unavailable) != 0 {
			sort.Strings(unavailable)
			plan.Stages = append(plan.Stages, Stage{
				Name: "unavailable-consumers", Targets: unavailable,
				Checks: []string{"operator selects a package replacement or independently verified removal"},
			})
		}
	}
	plan.Stages = append(plan.Stages, Stage{
		Name: "validate-runtime", Targets: []string{},
		Checks: []string{
			"import probes pass for rebuilt extension modules",
			"python-exec resolves the preferred policy interpreter",
			"VDB ownership and filesystem orphan scan agree",
		},
	})
	if len(report.Policy.Preference) == 0 || !contains(report.Policy.Targets, report.Policy.Preference[0]) {
		plan.Stages = append(plan.Stages, Stage{
			Name: "switch-preference", Targets: []string{},
			Checks: []string{"python-exec preference begins with a policy-supported interpreter"},
		})
	}
	var removals []string
	for _, removal := range report.Removals {
		if removal.Safe {
			removals = append(removals, "="+removal.Interpreter.CPV)
		}
	}
	sort.Strings(removals)
	if len(removals) != 0 {
		plan.Stages = append(plan.Stages, Stage{
			Name: "remove-obsolete-interpreters", Targets: removals,
			Checks: []string{
				"no package, shebang, python-exec preference, or ELF linkage references removed targets",
				"whole-state validation passes after removal",
			},
		})
	}
	return plan
}

func dependencyCohorts(consumers map[string]Consumer) [][]string {
	adjacent := make(map[string]map[string]bool, len(consumers))
	for cp := range consumers {
		adjacent[cp] = map[string]bool{}
	}
	for cp, consumer := range consumers {
		for _, dependency := range consumer.Dependencies {
			if _, exists := consumers[dependency]; !exists || dependency == cp {
				continue
			}
			adjacent[cp][dependency] = true
			adjacent[dependency][cp] = true
		}
	}
	var roots []string
	for cp := range consumers {
		roots = append(roots, cp)
	}
	sort.Strings(roots)
	visited := map[string]bool{}
	var cohorts [][]string
	for _, root := range roots {
		if visited[root] {
			continue
		}
		visited[root] = true
		pending := []string{root}
		var cohort []string
		for len(pending) != 0 {
			cp := pending[0]
			pending = pending[1:]
			cohort = append(cohort, cp)
			var neighbors []string
			for neighbor := range adjacent[cp] {
				neighbors = append(neighbors, neighbor)
			}
			sort.Strings(neighbors)
			for _, neighbor := range neighbors {
				if !visited[neighbor] {
					visited[neighbor] = true
					pending = append(pending, neighbor)
				}
			}
		}
		sort.Strings(cohort)
		cohorts = append(cohorts, cohort)
	}
	return cohorts
}

func consumerCP(consumer Consumer) string {
	if before, _, ok := strings.Cut(consumer.Atom, ":"); ok {
		return before
	}
	return consumer.Atom
}

func RebuildTargets(plan Plan) []string {
	var targets []string
	for _, stage := range plan.Stages {
		if stage.Name == "bootstrap-interpreters" || stage.Name == "repair-cohort" {
			targets = append(targets, stage.Targets...)
		}
	}
	sort.Strings(targets)
	return uniqueStrings(targets)
}

func targetSlot(target string) string {
	if !strings.HasPrefix(target, "python") {
		return ""
	}
	return strings.ReplaceAll(strings.TrimPrefix(target, "python"), "_", ".")
}
