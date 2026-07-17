package main

import (
	"encoding/json"
	"io"
	"sort"
	"time"

	"github.com/airencracken/arise/internal/resolve"
)

type jsonPlan struct {
	Schema     int                      `json:"schema"`
	Operation  string                   `json:"operation"`
	Targets    []string                 `json:"targets"`
	Complete   bool                     `json:"complete"`
	Resolution jsonResolution           `json:"resolution"`
	Actions    []jsonAction             `json:"actions"`
	Uninstall  []jsonAction             `json:"uninstall,omitempty"`
	Conflicts  []string                 `json:"conflicts"`
	Details    []resolve.ConflictDetail `json:"conflict_details,omitempty"`
	Warnings   []string                 `json:"warnings"`
	Error      string                   `json:"error,omitempty"`
}

type jsonResolution struct {
	Verified        bool   `json:"verified"`
	Verification    string `json:"verification"`
	DurationNS      int64  `json:"duration_ns"`
	BacktrackUsed   int    `json:"backtrack_used"`
	BacktrackLimit  int    `json:"backtrack_limit"`
	IndexNS         int64  `json:"index_ns"`
	StateNS         int64  `json:"state_ns"`
	GraphNS         int64  `json:"graph_ns"`
	SolverNS        int64  `json:"solver_ns"`
	SearchNS        int64  `json:"search_ns"`
	CompleteGraphNS int64  `json:"complete_graph_ns"`
	VerificationNS  int64  `json:"verification_ns"`
	SortNS          int64  `json:"sort_ns"`
}

type jsonAction struct {
	Action      string   `json:"action"`
	CPV         string   `json:"cpv"`
	Slot        string   `json:"slot,omitempty"`
	Subslot     string   `json:"subslot,omitempty"`
	Repository  string   `json:"repository,omitempty"`
	MergeType   string   `json:"merge_type,omitempty"`
	BinaryPath  string   `json:"binary_path,omitempty"`
	Reason      string   `json:"reason,omitempty"`
	Domain      string   `json:"domain"`
	UseEnabled  []string `json:"use_enabled,omitempty"`
	UseDisabled []string `json:"use_disabled,omitempty"`
}

type planTimings struct {
	Total, Index, State, Graph, Solver time.Duration
}

func writePlanJSON(w io.Writer, targets []string, cfg resolve.ResolveConfig, result *resolve.ResolveResult, resolveErr error, timings planTimings) error {
	if result == nil {
		result = &resolve.ResolveResult{}
	}
	operation := "install"
	if cfg.Update {
		operation = "update"
	}
	document := jsonPlan{
		Schema: 1, Operation: operation, Targets: append([]string(nil), targets...),
		Complete: resolveErr == nil && result.Verified && len(result.Conflicts) == 0,
		Resolution: jsonResolution{
			Verified: result.Verified, Verification: result.Verification,
			DurationNS: timings.Total.Nanoseconds(), BacktrackUsed: result.BacktrackLevel, BacktrackLimit: cfg.Backtrack,
			IndexNS: timings.Index.Nanoseconds(), StateNS: timings.State.Nanoseconds(), GraphNS: timings.Graph.Nanoseconds(), SolverNS: timings.Solver.Nanoseconds(),
			SearchNS: result.Metrics.Search.Nanoseconds(), CompleteGraphNS: result.Metrics.CompleteGraph.Nanoseconds(),
			VerificationNS: result.Metrics.Verification.Nanoseconds(), SortNS: result.Metrics.Sort.Nanoseconds(),
		},
		Actions: jsonActions(result.Install), Uninstall: jsonActions(result.Uninstall),
		Conflicts: append([]string(nil), result.Conflicts...), Details: append([]resolve.ConflictDetail(nil), result.ConflictDetails...),
		Warnings: append([]string(nil), result.Warnings...),
	}
	if document.Actions == nil {
		document.Actions = []jsonAction{}
	}
	if document.Conflicts == nil {
		document.Conflicts = []string{}
	}
	if document.Warnings == nil {
		document.Warnings = []string{}
	}
	if resolveErr != nil {
		document.Error = resolveErr.Error()
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(document)
}

func jsonActions(actions []resolve.PkgAction) []jsonAction {
	result := make([]jsonAction, 0, len(actions))
	for _, action := range actions {
		if action.Atom == nil {
			continue
		}
		cpv := action.Atom.CP()
		if action.Atom.Version != nil {
			cpv += "-" + action.Atom.Version.Raw
		}
		domain := action.Domain
		if domain == "" {
			domain = resolve.DomainROOT
		}
		item := jsonAction{Action: action.Action, CPV: cpv, Slot: action.Slot, Subslot: action.Subslot, Repository: action.Repository, MergeType: action.MergeType, BinaryPath: action.BinaryPath, Reason: action.Reason, Domain: string(domain)}
		for flag, enabled := range action.UseFlags {
			if enabled {
				item.UseEnabled = append(item.UseEnabled, flag)
			} else {
				item.UseDisabled = append(item.UseDisabled, flag)
			}
		}
		sort.Strings(item.UseEnabled)
		sort.Strings(item.UseDisabled)
		result = append(result, item)
	}
	return result
}
