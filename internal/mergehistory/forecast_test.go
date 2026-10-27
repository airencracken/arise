package mergehistory

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

func TestForecastParallelismDependenciesAndActiveTasks(t *testing.T) {
	tasks := []Task{{ID: "a", Expected: 10 * time.Second, Known: true}, {ID: "b", Expected: 20 * time.Second, Known: true}, {ID: "c", Prerequisites: []string{"a", "b"}, Expected: 5 * time.Second, Known: true}}
	for _, test := range []struct {
		jobs              int
		active            map[string]time.Duration
		done              map[string]bool
		wall, work, chain time.Duration
	}{{1, nil, nil, 35, 35, 25}, {2, nil, nil, 25, 35, 25}, {99, nil, nil, 25, 35, 25}, {2, map[string]time.Duration{"a": 5 * time.Second, "b": 5 * time.Second}, nil, 20, 25, 20}, {2, map[string]time.Duration{"b": 5 * time.Second}, map[string]bool{"a": true}, 20, 20, 20}} {
		forecast, err := ForecastPlan(tasks, test.jobs, test.active, test.done)
		if err != nil || forecast.Elapsed != test.wall*time.Second || forecast.Work != test.work*time.Second || forecast.CriticalPath != test.chain*time.Second {
			t.Fatalf("forecast=%+v, %v; test=%+v", forecast, err, test)
		}
	}
}

func TestForecastMissingAndOverdueHistory(t *testing.T) {
	tasks := []Task{{ID: "known", Expected: 10 * time.Second, Known: true}, {ID: "unknown", Prerequisites: []string{"known"}}}
	forecast, err := ForecastPlan(tasks, 1, map[string]time.Duration{"known": 20 * time.Second}, nil)
	if err != nil || forecast.Known != 1 || forecast.Total != 2 || forecast.Overdue != 1 || forecast.Elapsed < 0 || forecast.Work < 0 {
		t.Fatalf("bad partial forecast: %+v %v", forecast, err)
	}
}

func TestForecastRejectsInvalidGraphs(t *testing.T) {
	for _, tasks := range [][]Task{{{ID: ""}}, {{ID: "a"}, {ID: "a"}}, {{ID: "a", Expected: -1}}, {{ID: "a", Prerequisites: []string{"missing"}}}, {{ID: "a", Prerequisites: []string{"a"}}}, {{ID: "a"}, {ID: "b", Prerequisites: []string{"a", "a"}}}, {{ID: "a", Prerequisites: []string{"b"}}, {ID: "b", Prerequisites: []string{"a"}}}} {
		if _, err := ForecastPlan(tasks, 1, nil, nil); err == nil {
			t.Fatalf("invalid graph accepted: %+v", tasks)
		}
	}
	if _, err := ForecastPlan([]Task{{ID: "a", Known: true, Expected: 10}}, 1, map[string]time.Duration{"a": -1}, nil); err == nil {
		t.Fatal("negative elapsed accepted")
	}
}

func TestForecastBoundsOnGeneratedDAGs(t *testing.T) {
	random := rand.New(rand.NewSource(42))
	for iteration := 0; iteration < 200; iteration++ {
		tasks := make([]Task, 20)
		for i := range tasks {
			tasks[i] = Task{ID: string(rune('a' + i)), Expected: time.Duration(1+random.Intn(100)) * time.Second, Known: true}
			for before := 0; before < i; before++ {
				if random.Intn(5) == 0 {
					tasks[i].Prerequisites = append(tasks[i].Prerequisites, tasks[before].ID)
				}
			}
		}
		for jobs := 1; jobs <= 5; jobs++ {
			forecast, err := ForecastPlan(tasks, jobs, nil, nil)
			if err != nil || forecast.Elapsed < forecast.CriticalPath || forecast.Elapsed > forecast.Work || jobs == 1 && forecast.Elapsed != forecast.Work {
				t.Fatalf("invalid scheduling bounds: %+v, %v", forecast, err)
			}
		}
	}
	// Huge user job counts are bounded by actual work; addition cannot wrap.
	forecast, err := ForecastPlan([]Task{{ID: "a", Expected: time.Duration(math.MaxInt64), Known: true}, {ID: "b", Expected: 1, Known: true}}, math.MaxInt, nil, nil)
	if err != nil || forecast.Work < 0 || forecast.Elapsed < 0 {
		t.Fatalf("overflow: %+v %v", forecast, err)
	}
}
