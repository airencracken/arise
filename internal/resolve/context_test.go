package resolve

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/portage"
)

func TestResolveContextCancelledReturnsStructuredIncompleteResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := ResolveContext(ctx, NewDepGraph(), []string{"@world"}, DefaultResolveConfig())
	if err != nil {
		t.Fatalf("ResolveContext: %v", err)
	}
	if result == nil || result.Verified || result.Verification != VerificationIncomplete {
		t.Fatalf("result = %#v, want non-executable incomplete result", result)
	}
	if result.Incomplete == nil {
		t.Fatal("missing structured incomplete cause")
	}
	if result.Incomplete.Kind != "cancelled" || result.Incomplete.Phase != "target-expansion" {
		t.Fatalf("incomplete cause = %#v", result.Incomplete)
	}
	if result.Incomplete.DecisionsUsed != 0 || result.Incomplete.BacktracksUsed != 0 {
		t.Fatalf("unexpected search usage: %#v", result.Incomplete)
	}
	if len(result.Install) != 0 || len(result.Uninstall) != 0 {
		t.Fatalf("incomplete result exposed executable actions: %#v", result)
	}
}

type checkpointDeadlineContext struct {
	calls atomic.Int64
	limit int64
}

func (c *checkpointDeadlineContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *checkpointDeadlineContext) Done() <-chan struct{}       { return nil }
func (c *checkpointDeadlineContext) Value(any) any               { return nil }
func (c *checkpointDeadlineContext) Err() error {
	if c.calls.Add(1) >= c.limit {
		return context.DeadlineExceeded
	}
	return nil
}

func TestResolveContextBoundsCandidateCancellationLatency(t *testing.T) {
	graph := NewDepGraph()
	for version := 1; version <= 1000; version++ {
		graph.AddVersion("cat/pkg", fmt.Sprintf("%d", version), "0", "0", false, nil, "")
	}
	ctx := &checkpointDeadlineContext{limit: 50}
	result, err := ResolveContext(ctx, graph, []string{"cat/pkg"}, DefaultResolveConfig())
	if err != nil {
		t.Fatal(err)
	}
	if result.Incomplete == nil || result.Incomplete.Kind != "timeout" || result.Incomplete.Phase != "candidate-search" {
		t.Fatalf("result = %#v", result)
	}
	if result.Metrics.CandidateEvaluations >= 1000 {
		t.Fatalf("cancellation scanned all candidates: %#v", result.Metrics)
	}
}

func TestCandidateLookupCacheHitsAndInvalidatesOnUseOverride(t *testing.T) {
	graph := NewDepGraph()
	graph.AddVersion("cat/pkg", "1", "0", "0", false, nil, "")
	constraint, err := atom.Parse("cat/pkg")
	if err != nil {
		t.Fatal(err)
	}
	r := &resolver{ctx: context.Background(), graph: graph, config: DefaultResolveConfig(), useOverrides: make(map[string]map[string]bool), baseUseCache: make(map[string]map[string]bool), maskCache: make(map[string]portage.MaskStatus), keywordCache: make(map[string]bool), candidateCache: make(map[string]candidateCacheEntry)}
	node := graph.Packages["cat/pkg"]
	first := r.findMatchingVersion(node, constraint)
	second := r.findMatchingVersion(node, constraint)
	if first == nil || first != second || r.metrics.CandidateCacheHits != 1 || r.metrics.CandidateCacheMisses != 1 {
		t.Fatalf("cache result=%p/%p metrics=%#v", first, second, r.metrics)
	}
	r.setUseOverride("cat/pkg-1:0", "feature", true)
	if third := r.findMatchingVersion(node, constraint); third != first || r.metrics.CandidateCacheMisses != 2 {
		t.Fatalf("override did not invalidate cache: third=%p metrics=%#v", third, r.metrics)
	}
}

func TestResolveContextDeadlineReportsTimeout(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	result, err := ResolveContext(ctx, NewDepGraph(), nil, DefaultResolveConfig())
	if err != nil {
		t.Fatalf("ResolveContext: %v", err)
	}
	if result.Incomplete == nil || result.Incomplete.Kind != "timeout" {
		t.Fatalf("incomplete cause = %#v, want timeout", result.Incomplete)
	}
}
