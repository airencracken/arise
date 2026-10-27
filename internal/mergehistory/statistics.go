package mergehistory

import (
	"math"
	"path/filepath"
	"sort"
	"time"
)

type Statistics struct {
	Kind          string    `json:"kind"`
	Package       string    `json:"package"`
	Repository    string    `json:"repository,omitempty"`
	Root          string    `json:"root,omitempty"`
	Completed     int       `json:"completed"`
	Incomplete    int       `json:"incomplete"`
	Samples       int       `json:"samples"`
	TotalSeconds  int64     `json:"total_seconds"`
	MedianSeconds float64   `json:"median_seconds"`
	MeanSeconds   float64   `json:"mean_seconds"`
	P90Seconds    int64     `json:"p90_seconds"`
	Last          time.Time `json:"last"`
}

func Summarize(events []Event) []Statistics {
	type group struct {
		stats  Statistics
		values []int64
	}
	groups := make(map[string]*group)
	for _, event := range events {
		key := event.Kind + "\x00" + event.Package + "\x00" + event.Repository + "\x00" + event.Root
		g := groups[key]
		if g == nil {
			g = &group{stats: Statistics{Kind: event.Kind, Package: event.Package, Repository: event.Repository, Root: event.Root}}
			groups[key] = g
		}
		if stamp := EventTime(event); stamp.After(g.stats.Last) {
			g.stats.Last = stamp
		}
		if event.Status != "completed" {
			g.stats.Incomplete++
			continue
		}
		g.stats.Completed++
		if event.DurationSeconds == nil {
			continue
		}
		value := *event.DurationSeconds
		if value < 0 {
			continue
		}
		g.values = append(g.values, value)
		g.stats.TotalSeconds = addSeconds(g.stats.TotalSeconds, value)
	}
	result := make([]Statistics, 0, len(groups))
	for _, g := range groups {
		sort.Slice(g.values, func(i, j int) bool { return g.values[i] < g.values[j] })
		g.stats.Samples = len(g.values)
		if len(g.values) > 0 {
			g.stats.MedianSeconds = median(g.values)
			g.stats.MeanSeconds = float64(g.stats.TotalSeconds) / float64(len(g.values))
			g.stats.P90Seconds = g.values[(len(g.values)*9+9)/10-1]
		}
		result = append(result, g.stats)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].TotalSeconds != result[j].TotalSeconds {
			return result[i].TotalSeconds > result[j].TotalSeconds
		}
		if result[i].Package != result[j].Package {
			return result[i].Package < result[j].Package
		}
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		if result[i].Repository != result[j].Repository {
			return result[i].Repository < result[j].Repository
		}
		return result[i].Root < result[j].Root
	})
	return result
}

func median(values []int64) float64 {
	n := len(values)
	if n%2 == 1 {
		return float64(values[n/2])
	}
	return float64(values[n/2-1]) + float64(values[n/2]-values[n/2-1])/2
}

func addSeconds(left, right int64) int64 {
	if right > math.MaxInt64-left {
		return math.MaxInt64
	}
	return left + right
}

type Prediction struct {
	Seconds float64 `json:"seconds"`
	Samples int     `json:"samples"`
	Basis   string  `json:"basis"`
}

// Predict prefers the exact CPV, then the same package. Portage's timing log
// lacks USE, toolchain and machine identity, so these are historical estimates,
// not claims of build-configuration compatibility. Unknown roots/repos match
// only when the log itself omitted them.
func Predict(events []Event, cp, cpv, repository, root string) (Prediction, bool) {
	var exact, fallback []int64
	for _, event := range events {
		if event.Kind != "merge" || event.Status != "completed" || event.Package != cp || event.DurationSeconds == nil {
			continue
		}
		if repository != "" && event.Repository != "" && repository != event.Repository {
			continue
		}
		if root != "" && event.Root != "" && filepath.Clean(root) != filepath.Clean(event.Root) {
			continue
		}
		seconds := *event.DurationSeconds
		if seconds <= 0 || seconds >= 7*24*60*60 {
			continue
		}
		fallback = append(fallback, seconds)
		if event.CPV == cpv {
			exact = append(exact, seconds)
		}
	}
	values, basis := exact, "same version"
	if len(values) == 0 {
		values, basis = fallback, "same package"
	}
	if len(values) == 0 {
		return Prediction{}, false
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return Prediction{Seconds: median(values), Samples: len(values), Basis: basis}, true
}
