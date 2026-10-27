package mergehistory

import (
	"fmt"
	"math"
	"sort"
	"time"
)

type Task struct {
	ID            string
	Prerequisites []string
	Expected      time.Duration
	Known         bool
}

type Forecast struct {
	Work         time.Duration
	CriticalPath time.Duration
	Elapsed      time.Duration
	Known        int
	Total        int
	Overdue      int
}

// ForecastPlan simulates the dependency-ready queue with a bounded worker pool.
// Unknown tasks contribute no invented duration; callers must label coverage as
// partial. Active tasks retain their slots and use nonnegative remaining work.
func ForecastPlan(tasks []Task, jobs int, active map[string]time.Duration, completed map[string]bool) (Forecast, error) {
	if jobs < 1 {
		jobs = 1
	}
	jobs = min(jobs, max(len(tasks), 1))
	indexes := make(map[string]int, len(tasks))
	for i, task := range tasks {
		if task.ID == "" {
			return Forecast{}, fmt.Errorf("estimate: empty task identity")
		}
		if _, exists := indexes[task.ID]; exists {
			return Forecast{}, fmt.Errorf("estimate: duplicate task %s", task.ID)
		}
		if task.Expected < 0 {
			return Forecast{}, fmt.Errorf("estimate: negative duration for %s", task.ID)
		}
		indexes[task.ID] = i
	}
	remaining := make([]int, len(tasks))
	dependents := make([][]int, len(tasks))
	durations := make([]time.Duration, len(tasks))
	result := Forecast{}
	for i, task := range tasks {
		if !completed[task.ID] {
			result.Total++
			if task.Known {
				result.Known++
				durations[i] = task.Expected
				if elapsed, running := active[task.ID]; running {
					if elapsed < 0 {
						return Forecast{}, fmt.Errorf("estimate: negative elapsed time")
					}
					if elapsed >= task.Expected {
						result.Overdue++
						durations[i] = 0
					} else {
						durations[i] -= elapsed
					}
				}
			}
			result.Work = addDuration(result.Work, durations[i])
		}
		seen := make(map[string]bool)
		for _, id := range task.Prerequisites {
			before, ok := indexes[id]
			if !ok || seen[id] {
				return Forecast{}, fmt.Errorf("estimate: invalid prerequisite %s", id)
			}
			seen[id] = true
			remaining[i]++
			dependents[before] = append(dependents[before], i)
		}
	}
	// Topological traversal validates the whole graph, including completed tasks.
	var ready []int
	for i, count := range remaining {
		if count == 0 {
			ready = append(ready, i)
		}
	}
	order := make([]int, 0, len(tasks))
	critical := make([]time.Duration, len(tasks))
	for len(ready) > 0 {
		i := ready[0]
		ready = ready[1:]
		order = append(order, i)
		critical[i] = addDuration(critical[i], durations[i])
		result.CriticalPath = max(result.CriticalPath, critical[i])
		for _, after := range dependents[i] {
			critical[after] = max(critical[after], critical[i])
			remaining[after]--
			if remaining[after] == 0 {
				ready = append(ready, after)
			}
		}
	}
	if len(order) != len(tasks) {
		return Forecast{}, fmt.Errorf("estimate: cyclic task graph")
	}
	type runningTask struct {
		index int
		end   time.Duration
	}
	running := make([]runningTask, 0, jobs)
	done := make(map[string]bool, len(tasks))
	for id, value := range completed {
		done[id] = value
	}
	for i, task := range tasks {
		if _, ok := active[task.ID]; ok && !done[task.ID] {
			running = append(running, runningTask{i, durations[i]})
		}
	}
	jobs = max(jobs, len(running))
	started := make(map[string]bool, len(tasks))
	for _, item := range running {
		started[tasks[item.index].ID] = true
	}
	clock := time.Duration(0)
	for finished := 0; finished < result.Total; {
		for i, task := range tasks {
			if len(running) >= jobs {
				break
			}
			if done[task.ID] || started[task.ID] {
				continue
			}
			canStart := true
			for _, id := range task.Prerequisites {
				if !done[id] {
					canStart = false
					break
				}
			}
			if canStart {
				running = append(running, runningTask{i, addDuration(clock, durations[i])})
				started[task.ID] = true
			}
		}
		if len(running) == 0 {
			return Forecast{}, fmt.Errorf("estimate: no ready task")
		}
		sort.SliceStable(running, func(i, j int) bool { return running[i].end < running[j].end })
		clock = running[0].end
		for len(running) > 0 && running[0].end == clock {
			done[tasks[running[0].index].ID] = true
			running = running[1:]
			finished++
		}
	}
	result.Elapsed = clock
	return result, nil
}

func addDuration(left, right time.Duration) time.Duration {
	if right > time.Duration(math.MaxInt64)-left {
		return time.Duration(math.MaxInt64)
	}
	return left + right
}
