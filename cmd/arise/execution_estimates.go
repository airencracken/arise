package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/airencracken/arise/internal/mergehistory"
	"github.com/airencracken/arise/internal/resolve"
)

func estimateTasks(actions []resolve.PkgAction, estimates mergeEstimates) []mergehistory.Task {
	tasks := make([]mergehistory.Task, 0, len(actions))
	for _, action := range actions {
		expected, known := estimates.forAction(action)
		tasks = append(tasks, mergehistory.Task{ID: resolve.ActionIdentity(action), Prerequisites: append([]string(nil), action.Prerequisites...), Expected: expected, Known: known})
	}
	return tasks
}

func writePlanEstimates(writer io.Writer, actions []resolve.PkgAction, jobs int, estimates mergeEstimates) {
	forecast, err := mergehistory.ForecastPlan(estimateTasks(actions, estimates), jobs, nil, nil)
	if err != nil {
		fmt.Fprintf(writer, "Time estimate unavailable: %v\n", err)
		return
	}
	if estimates.err != nil {
		fmt.Fprintf(writer, "Time estimate unavailable: %v\n", estimates.err)
		return
	}
	if forecast.Known == 0 {
		fmt.Fprintln(writer, "Time estimate unavailable: no matching history.")
		return
	}
	label := "Historical estimate"
	if forecast.Known < forecast.Total {
		label = "Partial historical estimate"
	}
	fmt.Fprintf(writer, "%s: ~%s elapsed at %d job(s); %s package work\n", label, formatEstimate(forecast.Elapsed), max(jobs, 1), formatEstimate(forecast.Work))
	fmt.Fprintf(writer, "  History: %d/%d packages; dependency chain %s. Load and temporary-space limits may reduce concurrency.\n", forecast.Known, forecast.Total, formatEstimate(forecast.CriticalPath))
	fmt.Fprintln(writer, "  Based on past merges; build settings and removal time are not recorded in this estimate.")
}

type executionEstimate struct {
	mu          sync.Mutex
	tasks       []mergehistory.Task
	jobs, total int
	started     time.Time
	active      map[string]time.Time
	completed   map[string]bool
}

func newExecutionEstimate(actions []resolve.PkgAction, total, jobs int, estimates mergeEstimates, now time.Time) *executionEstimate {
	return &executionEstimate{tasks: estimateTasks(actions, estimates), jobs: jobs, total: total, started: now, active: make(map[string]time.Time), completed: make(map[string]bool)}
}

func (estimate *executionEstimate) start(action resolve.PkgAction, now time.Time) {
	if estimate == nil {
		return
	}
	estimate.mu.Lock()
	defer estimate.mu.Unlock()
	estimate.active[resolve.ActionIdentity(action)] = now
}

func (estimate *executionEstimate) complete(action resolve.PkgAction) {
	if estimate == nil {
		return
	}
	estimate.mu.Lock()
	defer estimate.mu.Unlock()
	id := resolve.ActionIdentity(action)
	delete(estimate.active, id)
	estimate.completed[id] = true
}

func (estimate *executionEstimate) status(now time.Time) string {
	estimate.mu.Lock()
	defer estimate.mu.Unlock()
	active := make(map[string]time.Duration, len(estimate.active))
	for id, started := range estimate.active {
		active[id] = max(now.Sub(started), 0)
	}
	forecast, err := mergehistory.ForecastPlan(estimate.tasks, estimate.jobs, active, estimate.completed)
	parts := []string{fmt.Sprintf("Jobs: %d/%d complete", len(estimate.completed), estimate.total), fmt.Sprintf("%d running", len(estimate.active)), "elapsed " + formatEstimate(now.Sub(estimate.started))}
	switch {
	case err != nil || forecast.Known == 0 && forecast.Total > 0:
		parts = append(parts, "ETA unavailable")
	case forecast.Overdue > 0:
		parts = append(parts, fmt.Sprintf("ETA unknown (%d over estimate)", forecast.Overdue))
	case forecast.Total == 0:
		parts = append(parts, "finishing")
	default:
		eta := "~" + formatEstimate(forecast.Elapsed) + " left"
		if forecast.Known < forecast.Total {
			eta += " (partial)"
		}
		parts = append(parts, eta, "finish ~"+now.Add(forecast.Elapsed).Format("15:04"))
	}
	if forecast.Total > 0 {
		parts = append(parts, fmt.Sprintf("history %d/%d", forecast.Known, forecast.Total))
	}
	return strings.Join(parts, " | ")
}

// Only interactive terminals receive periodic updates. Redirected logs get
// status on real start/complete events and never carriage-return animations.
func watchExecutionEstimate(progress *terminalProgress, estimate *executionEstimate) func() {
	if estimate == nil || progress == nil || !progress.terminal {
		return func() {}
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				progress.setStatus(estimate.status(time.Now()))
			case <-stop:
				return
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop) }); <-done }
}
