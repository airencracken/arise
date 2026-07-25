// Package perf runs correctness-gated, same-snapshot command comparisons.
package perf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/airencracken/arise/internal/plancompare"
)

type Command struct {
	Tool        string   `json:"tool"`
	Path        string   `json:"path"`
	Args        []string `json:"args,omitempty"`
	Env         []string `json:"env,omitempty"`
	VersionArgs []string `json:"version_args,omitempty"`
	CachePaths  []string `json:"cache_paths,omitempty"`
	ResetPaths  []string `json:"reset_paths,omitempty"`
}

type Case struct {
	Name              string   `json:"name"`
	Arise             Command  `json:"arise"`
	Reference         Command  `json:"reference"`
	AriseValidate     *Command `json:"arise_validate,omitempty"`
	ReferenceValidate *Command `json:"reference_validate,omitempty"`
	Normalize         string   `json:"normalize"`
	MinSpeedup        *float64 `json:"min_speedup,omitempty"`
	// ReportOnly records performance without allowing it to fail the workload.
	// Correctness mismatches always remain fatal.
	ReportOnly bool `json:"report_only,omitempty"`
	// ColdCache syncs filesystems and drops the Linux page cache immediately
	// before every measured command. It requires an explicit root invocation.
	ColdCache bool `json:"cold_cache,omitempty"`
}

type Workload struct {
	Name    string `json:"name"`
	Warmups int    `json:"warmups"`
	Runs    int    `json:"runs"`
	Cases   []Case `json:"cases"`
}

type Sample struct {
	WallNS              int64 `json:"wall_ns"`
	UserNS              int64 `json:"user_ns"`
	SystemNS            int64 `json:"system_ns"`
	MaxRSSBytes         int64 `json:"max_rss_bytes"`
	PeakTreeRSSBytes    int64 `json:"peak_tree_rss_bytes,omitempty"`
	PeakTreePSSBytes    int64 `json:"peak_tree_pss_bytes,omitempty"`
	PeakTreeUSSBytes    int64 `json:"peak_tree_uss_bytes,omitempty"`
	MemorySampleCount   int64 `json:"memory_sample_count,omitempty"`
	MemorySampleEveryNS int64 `json:"memory_sample_every_ns,omitempty"`
	InputBlocks         int64 `json:"input_blocks"`
	OutputBlocks        int64 `json:"output_blocks"`
}

type Result struct {
	Name                  string   `json:"name"`
	Normalize             string   `json:"normalize"`
	Equivalent            bool     `json:"equivalent"`
	PerformancePass       bool     `json:"performance_pass"`
	PerformanceEnforced   bool     `json:"performance_enforced"`
	ColdCache             bool     `json:"cold_cache,omitempty"`
	MinSpeedup            float64  `json:"min_speedup"`
	AriseExitCode         int      `json:"arise_exit_code"`
	ReferenceTool         string   `json:"reference_tool"`
	ReferenceExitCode     int      `json:"reference_exit_code"`
	AriseOutputSHA256     string   `json:"arise_output_sha256"`
	ReferenceOutputSHA256 string   `json:"reference_output_sha256"`
	Arise                 []Sample `json:"arise_samples"`
	Reference             []Sample `json:"reference_samples"`
	AriseMedianNS         int64    `json:"arise_median_ns"`
	ReferenceMedianNS     int64    `json:"reference_median_ns"`
	AriseP95NS            int64    `json:"arise_p95_ns"`
	ReferenceP95NS        int64    `json:"reference_p95_ns"`
	Speedup               float64  `json:"speedup"`
	AriseCacheBytes       int64    `json:"arise_cache_bytes,omitempty"`
	ReferenceCacheBytes   int64    `json:"reference_cache_bytes,omitempty"`
}

type Report struct {
	Schema        int                 `json:"schema"`
	Created       time.Time           `json:"created"`
	Snapshot      string              `json:"snapshot"`
	Workload      string              `json:"workload"`
	Host          map[string]string   `json:"host"`
	Tools         map[string]ToolInfo `json:"tools"`
	AllEquivalent bool                `json:"all_equivalent"`
	AllPassed     bool                `json:"all_passed"`
	Results       []Result            `json:"results"`
}

type ToolInfo struct {
	Path    string `json:"path"`
	Version string `json:"version,omitempty"`
}

type commandResult struct {
	sample Sample
	stdout []byte
	exit   int
}

func LoadWorkload(path string) (Workload, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Workload{}, fmt.Errorf("read workload: %w", err)
	}
	var w Workload
	if err := json.Unmarshal(data, &w); err != nil {
		return Workload{}, fmt.Errorf("parse workload: %w", err)
	}
	if w.Runs <= 0 {
		w.Runs = 5
	}
	if w.Warmups < 0 {
		return Workload{}, fmt.Errorf("warmups must not be negative")
	}
	for i, c := range w.Cases {
		if c.Name == "" || c.Arise.Path == "" || c.Reference.Path == "" {
			return Workload{}, fmt.Errorf("case %d requires name, arise.path, and reference.path", i)
		}
		if (c.AriseValidate == nil) != (c.ReferenceValidate == nil) {
			return Workload{}, fmt.Errorf("case %q must provide both validation commands", c.Name)
		}
		switch c.Normalize {
		case "exact", "sorted-lines", "package-names", "search-package-names", "package-plan", "exit-code":
		default:
			return Workload{}, fmt.Errorf("case %q has unsupported normalization %q", c.Name, c.Normalize)
		}
	}
	return w, nil
}

func Run(ctx context.Context, workload Workload, snapshot string) (Report, error) {
	if strings.TrimSpace(snapshot) == "" {
		return Report{}, fmt.Errorf("snapshot identity is required")
	}
	report := Report{
		Schema: 1, Created: time.Now().UTC(), Snapshot: snapshot,
		Workload: workload.Name, AllEquivalent: true, AllPassed: true,
		Host:  map[string]string{"goos": runtime.GOOS, "goarch": runtime.GOARCH, "go_version": runtime.Version()},
		Tools: map[string]ToolInfo{},
	}
	for _, c := range workload.Cases {
		recordTool(report.Tools, c.Arise)
		recordTool(report.Tools, c.Reference)
		result, err := runCase(ctx, c, workload.Warmups, workload.Runs)
		if err != nil {
			return Report{}, fmt.Errorf("case %s: %w", c.Name, err)
		}
		if !result.Equivalent {
			report.AllEquivalent = false
		}
		if !result.Equivalent || (result.PerformanceEnforced && !result.PerformancePass) {
			report.AllPassed = false
		}
		report.Results = append(report.Results, result)
	}
	return report, nil
}

func runCase(ctx context.Context, c Case, warmups, runs int) (Result, error) {
	for i := 0; i < warmups; i++ {
		if _, err := executePrepared(ctx, c.Arise, c.ColdCache); err != nil {
			return Result{}, err
		}
		if _, err := executePrepared(ctx, c.Reference, c.ColdCache); err != nil {
			return Result{}, err
		}
	}
	var ariseRuns, referenceRuns []commandResult
	for i := 0; i < runs; i++ {
		var a, p commandResult
		var err error
		if i%2 == 0 {
			a, err = executePrepared(ctx, c.Arise, c.ColdCache)
			if err == nil {
				p, err = executePrepared(ctx, c.Reference, c.ColdCache)
			}
		} else {
			p, err = executePrepared(ctx, c.Reference, c.ColdCache)
			if err == nil {
				a, err = executePrepared(ctx, c.Arise, c.ColdCache)
			}
		}
		if err != nil {
			return Result{}, err
		}
		ariseRuns = append(ariseRuns, a)
		referenceRuns = append(referenceRuns, p)
	}
	aRaw, pRaw := ariseRuns[0].stdout, referenceRuns[0].stdout
	aOut := normalize(aRaw, c.Normalize)
	pOut := normalize(pRaw, c.Normalize)
	aExit, pExit := ariseRuns[0].exit, referenceRuns[0].exit
	if c.AriseValidate != nil {
		aValidation, err := execute(ctx, *c.AriseValidate)
		if err != nil {
			return Result{}, fmt.Errorf("validate arise result: %w", err)
		}
		pValidation, err := execute(ctx, *c.ReferenceValidate)
		if err != nil {
			return Result{}, fmt.Errorf("validate reference result: %w", err)
		}
		aOut = normalize(aValidation.stdout, c.Normalize)
		pOut = normalize(pValidation.stdout, c.Normalize)
		aRaw, pRaw = aValidation.stdout, pValidation.stdout
		aExit, pExit = aValidation.exit, pValidation.exit
	}
	r := Result{Name: c.Name, Normalize: c.Normalize, ColdCache: c.ColdCache, AriseExitCode: ariseRuns[0].exit, ReferenceTool: c.Reference.Tool, ReferenceExitCode: referenceRuns[0].exit}
	var sizeErr error
	if r.AriseCacheBytes, sizeErr = cacheSize(c.Arise.CachePaths); sizeErr != nil {
		return Result{}, fmt.Errorf("measure arise cache: %w", sizeErr)
	}
	if r.ReferenceCacheBytes, sizeErr = cacheSize(c.Reference.CachePaths); sizeErr != nil {
		return Result{}, fmt.Errorf("measure %s cache: %w", c.Reference.Tool, sizeErr)
	}
	r.AriseOutputSHA256 = digest(aOut)
	r.ReferenceOutputSHA256 = digest(pOut)
	exitEquivalent := aExit == pExit || c.Normalize == "package-plan"
	r.Equivalent = exitEquivalent && outputsEquivalent(aRaw, pRaw, aOut, pOut, c.Normalize)
	if c.AriseValidate == nil {
		for i := range ariseRuns {
			if ariseRuns[i].exit != r.AriseExitCode || !bytes.Equal(normalize(ariseRuns[i].stdout, c.Normalize), aOut) {
				r.Equivalent = false
			}
		}
		for i := range referenceRuns {
			if referenceRuns[i].exit != r.ReferenceExitCode || !bytes.Equal(normalize(referenceRuns[i].stdout, c.Normalize), pOut) {
				r.Equivalent = false
			}
		}
	}
	for _, run := range ariseRuns {
		r.Arise = append(r.Arise, run.sample)
	}
	for _, run := range referenceRuns {
		r.Reference = append(r.Reference, run.sample)
	}
	r.AriseMedianNS, r.AriseP95NS = stats(r.Arise)
	r.ReferenceMedianNS, r.ReferenceP95NS = stats(r.Reference)
	if r.AriseMedianNS > 0 {
		r.Speedup = float64(r.ReferenceMedianNS) / float64(r.AriseMedianNS)
	}
	r.MinSpeedup = 1.0
	if c.MinSpeedup != nil {
		r.MinSpeedup = *c.MinSpeedup
	}
	r.PerformancePass = r.Speedup >= r.MinSpeedup
	r.PerformanceEnforced = !c.ReportOnly
	return r, nil
}

func executePrepared(ctx context.Context, spec Command, coldCache bool) (commandResult, error) {
	if coldCache {
		if os.Geteuid() != 0 {
			return commandResult{}, fmt.Errorf("cold-cache benchmark requires root")
		}
		syscall.Sync()
		if err := os.WriteFile("/proc/sys/vm/drop_caches", []byte("3\n"), 0o644); err != nil {
			return commandResult{}, fmt.Errorf("drop Linux page cache: %w", err)
		}
	}
	return execute(ctx, spec)
}

func outputsEquivalent(ariseRaw, referenceRaw, ariseNormalized, referenceNormalized []byte, mode string) bool {
	if mode != "package-plan" {
		return bytes.Equal(ariseNormalized, referenceNormalized)
	}
	arisePlan, err := plancompare.ParseAriseJSON(string(ariseRaw))
	if err != nil {
		return false
	}
	referencePlan, err := plancompare.ParseEmerge(string(referenceRaw))
	return err == nil && len(plancompare.Compare(arisePlan, referencePlan)) == 0
}

func cacheSize(paths []string) (int64, error) {
	var total int64
	for _, root := range paths {
		err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				total += info.Size()
			}
			return nil
		})
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func recordTool(tools map[string]ToolInfo, spec Command) {
	name := spec.Tool
	if name == "" {
		name = spec.Path
	}
	if _, exists := tools[name]; exists {
		return
	}
	path, err := exec.LookPath(spec.Path)
	if err != nil {
		path = spec.Path
	}
	info := ToolInfo{Path: path}
	if len(spec.VersionArgs) > 0 {
		cmd := exec.Command(spec.Path, spec.VersionArgs...)
		out, _ := cmd.CombinedOutput()
		info.Version = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	}
	tools[name] = info
}

func execute(ctx context.Context, spec Command) (commandResult, error) {
	for _, path := range spec.ResetPaths {
		clean := filepath.Clean(path)
		temp := filepath.Clean(os.TempDir())
		if clean == temp || !strings.HasPrefix(clean, temp+string(os.PathSeparator)) {
			return commandResult{}, fmt.Errorf("refusing to reset non-temporary path %q", path)
		}
		if err := os.RemoveAll(clean); err != nil {
			return commandResult{}, fmt.Errorf("reset benchmark path %s: %w", clean, err)
		}
	}
	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
	cmd.Env = append(os.Environ(), spec.Env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	started := time.Now()
	if err := cmd.Start(); err != nil {
		return commandResult{}, fmt.Errorf("execute %s: %w", spec.Path, err)
	}
	const memoryInterval = 10 * time.Millisecond
	stopMemory := make(chan struct{})
	memoryDone := make(chan processTreeMemory, 1)
	go sampleProcessTreeMemory(cmd.Process.Pid, memoryInterval, stopMemory, memoryDone)
	err := cmd.Wait()
	close(stopMemory)
	memory := <-memoryDone
	result := commandResult{stdout: stdout.Bytes(), sample: Sample{WallNS: time.Since(started).Nanoseconds()}}
	result.sample.PeakTreeRSSBytes = memory.RSSBytes
	result.sample.PeakTreePSSBytes = memory.PSSBytes
	result.sample.PeakTreeUSSBytes = memory.USSBytes
	result.sample.MemorySampleCount = memory.Samples
	result.sample.MemorySampleEveryNS = int64(memoryInterval)
	if cmd.ProcessState != nil {
		result.sample.UserNS = cmd.ProcessState.UserTime().Nanoseconds()
		result.sample.SystemNS = cmd.ProcessState.SystemTime().Nanoseconds()
		if usage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			result.sample.MaxRSSBytes = usage.Maxrss * 1024
			result.sample.InputBlocks = usage.Inblock
			result.sample.OutputBlocks = usage.Oublock
		}
	}
	if err == nil {
		return result, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.exit = exitErr.ExitCode()
		return result, nil
	}
	return commandResult{}, fmt.Errorf("execute %s: %w (%s)", spec.Path, err, strings.TrimSpace(stderr.String()))
}

type processTreeMemory struct {
	RSSBytes int64
	PSSBytes int64
	USSBytes int64
	Samples  int64
}

func sampleProcessTreeMemory(rootPID int, interval time.Duration, stop <-chan struct{}, done chan<- processTreeMemory) {
	var peak processTreeMemory
	sample := func() {
		current := readProcessTreeMemory(rootPID)
		peak.RSSBytes = max(peak.RSSBytes, current.RSSBytes)
		peak.PSSBytes = max(peak.PSSBytes, current.PSSBytes)
		peak.USSBytes = max(peak.USSBytes, current.USSBytes)
		peak.Samples++
	}
	sample()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			sample()
		case <-stop:
			sample()
			done <- peak
			return
		}
	}
}

func readProcessTreeMemory(rootPID int) processTreeMemory {
	var result processTreeMemory
	queue := []int{rootPID}
	seen := make(map[int]bool)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if pid <= 0 || seen[pid] {
			continue
		}
		seen[pid] = true
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/smaps_rollup", pid))
		if err == nil {
			memory := parseSmapsRollup(data)
			result.RSSBytes += memory.RSSBytes
			result.PSSBytes += memory.PSSBytes
			result.USSBytes += memory.USSBytes
		}
		children, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
		if err != nil {
			continue
		}
		for _, field := range strings.Fields(string(children)) {
			child, parseErr := strconv.Atoi(field)
			if parseErr == nil {
				queue = append(queue, child)
			}
		}
	}
	return result
}

func parseSmapsRollup(data []byte) processTreeMemory {
	var result processTreeMemory
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		bytes := value * 1024
		switch fields[0] {
		case "Rss:":
			result.RSSBytes += bytes
		case "Pss:":
			result.PSSBytes += bytes
		case "Private_Clean:", "Private_Dirty:", "Private_Hugetlb:":
			result.USSBytes += bytes
		}
	}
	return result
}

func normalize(data []byte, mode string) []byte {
	s := strings.TrimSpace(string(data))
	if mode == "exit-code" {
		return nil
	}
	if mode == "sorted-lines" {
		lines := strings.Split(s, "\n")
		for i := range lines {
			lines[i] = strings.TrimSpace(lines[i])
		}
		sort.Strings(lines)
		s = strings.Join(lines, "\n")
	}
	if mode == "package-names" {
		lines := strings.Split(s, "\n")
		for i := range lines {
			line := strings.TrimSpace(lines[i])
			if slash := strings.LastIndexByte(line, '/'); slash >= 0 {
				line = line[slash+1:]
			}
			lines[i] = line
		}
		sort.Strings(lines)
		s = strings.Join(lines, "\n")
	}
	if mode == "search-package-names" {
		var names []string
		for _, raw := range strings.Split(s, "\n") {
			line := strings.TrimSpace(strings.ReplaceAll(raw, "\b", ""))
			fields := strings.Fields(line)
			candidate := ""
			switch {
			case len(fields) >= 2 && fields[0] == "*" && strings.Contains(fields[1], "/"):
				candidate = fields[1]
			case len(fields) == 1:
				candidate = fields[0]
			}
			if candidate == "" || candidate == "Searching..." || strings.HasPrefix(candidate, "[") {
				continue
			}
			if slash := strings.LastIndexByte(candidate, '/'); slash >= 0 {
				candidate = candidate[slash+1:]
			}
			names = append(names, candidate)
		}
		sort.Strings(names)
		s = strings.Join(names, "\n")
	}
	if mode == "package-plan" {
		var actions []plancompare.Action
		var err error
		if strings.HasPrefix(s, "{") {
			actions, err = plancompare.ParseAriseJSON(s)
		} else {
			actions, err = plancompare.ParseEmerge(s)
		}
		if err != nil {
			return []byte("package-plan parse error: " + err.Error())
		}
		normalized, err := json.Marshal(actions)
		if err != nil {
			return []byte("package-plan encode error: " + err.Error())
		}
		return normalized
	}
	return []byte(s)
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func stats(samples []Sample) (median, p95 int64) {
	values := make([]int64, len(samples))
	for i, s := range samples {
		values[i] = s.WallNS
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	if len(values) == 0 {
		return 0, 0
	}
	median = values[len(values)/2]
	p95 = values[(95*len(values)-1)/100]
	return median, p95
}
