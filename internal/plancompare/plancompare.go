// Package plancompare normalizes pretend plans from Arise and Portage.
package plancompare

import (
	"bufio"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/atom"
)

type ariseJSONPlan struct {
	Schema  int `json:"schema"`
	Actions []struct {
		Action      string   `json:"action"`
		CPV         string   `json:"cpv"`
		Slot        string   `json:"slot"`
		Subslot     string   `json:"subslot"`
		Repository  string   `json:"repository"`
		MergeType   string   `json:"merge_type"`
		UseEnabled  []string `json:"use_enabled"`
		UseDisabled []string `json:"use_disabled"`
	} `json:"actions"`
}

type Action struct {
	CP           string              `json:"cp"`
	Version      string              `json:"version"`
	Slot         string              `json:"slot,omitempty"`
	Subslot      string              `json:"subslot,omitempty"`
	Repository   string              `json:"repository,omitempty"`
	Kind         string              `json:"kind"`
	MergeType    string              `json:"merge_type,omitempty"`
	Use          map[string][]string `json:"use,omitempty"`
	EffectiveUse map[string]bool     `json:"effective_use,omitempty"`
}

func (a Action) Identity() string { return a.CP + ":" + a.Slot }
func (a Action) CPV() string {
	if a.Version == "" {
		return a.CP
	}
	return a.CP + "-" + a.Version
}

type Difference struct {
	Identity    string   `json:"identity"`
	Kind        string   `json:"kind"`
	Arise       *Action  `json:"arise,omitempty"`
	Emerge      *Action  `json:"emerge,omitempty"`
	UseMismatch []string `json:"use_mismatch,omitempty"`
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
var emergeLineRE = regexp.MustCompile(`^\[(ebuild|binary)([^]]*)\]\s+(\S+)(.*)$`)
var ariseLineRE = regexp.MustCompile(`^\[(install|update|reinstall)\]\s+(\S+)`)
var useGroupRE = regexp.MustCompile(`([A-Z][A-Z0-9_]*)="([^"]*)"`)

func ParseArise(output string) ([]Action, error) {
	return parseLines(output, func(line string) (Action, bool, error) {
		match := ariseLineRE.FindStringSubmatch(strings.TrimSpace(ansiRE.ReplaceAllString(line, "")))
		if match == nil {
			return Action{}, false, nil
		}
		action, err := parseAtom(match[2])
		action.Kind = match[1]
		return action, true, err
	})
}

// ParseAriseJSON consumes Arise's versioned plan format. The comparator uses
// this instead of presentation-oriented terminal output so color and wording
// changes cannot silently invalidate correctness comparisons.
func ParseAriseJSON(output string) ([]Action, error) {
	var plan ariseJSONPlan
	if err := json.Unmarshal([]byte(output), &plan); err != nil {
		return nil, fmt.Errorf("parse Arise JSON plan: %w", err)
	}
	if plan.Schema != 1 {
		return nil, fmt.Errorf("unsupported Arise JSON plan schema %d", plan.Schema)
	}
	result := make([]Action, 0, len(plan.Actions))
	for _, item := range plan.Actions {
		action, err := parseAtom(item.CPV)
		if err != nil {
			return nil, err
		}
		action.Kind = item.Action
		action.MergeType = item.MergeType
		if item.Slot != "" {
			action.Slot = item.Slot
		}
		action.Subslot = item.Subslot
		action.Repository = item.Repository
		if len(item.UseEnabled)+len(item.UseDisabled) > 0 {
			action.EffectiveUse = make(map[string]bool, len(item.UseEnabled)+len(item.UseDisabled))
			values := append([]string(nil), item.UseEnabled...)
			for _, flag := range item.UseEnabled {
				action.EffectiveUse[flag] = true
			}
			for _, flag := range item.UseDisabled {
				values = append(values, "-"+flag)
				action.EffectiveUse[flag] = false
			}
			sort.Strings(values)
			action.Use = map[string][]string{"USE": values}
		}
		result = append(result, action)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Identity() < result[j].Identity() })
	return result, nil
}

func ParseEmerge(output string) ([]Action, error) {
	return parseLines(output, func(line string) (Action, bool, error) {
		match := emergeLineRE.FindStringSubmatch(strings.TrimSpace(ansiRE.ReplaceAllString(line, "")))
		if match == nil {
			return Action{}, false, nil
		}
		action, err := parseAtom(match[3])
		if err != nil {
			return Action{}, true, err
		}
		action.MergeType = "source"
		if match[1] == "binary" {
			action.MergeType = "binary"
		}
		flags := match[2]
		switch {
		case strings.Contains(flags, "U"):
			action.Kind = "update"
		case strings.Contains(flags, "R"):
			action.Kind = "reinstall"
		default:
			action.Kind = "install"
		}
		action.Use, action.EffectiveUse = parseUseGroups(match[4])
		return action, true, nil
	})
}

func parseLines(output string, parse func(string) (Action, bool, error)) ([]Action, error) {
	var result []Action
	scanner := bufio.NewScanner(strings.NewReader(output))
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 4*1024*1024)
	for scanner.Scan() {
		action, ok, err := parse(scanner.Text())
		if err != nil {
			return nil, err
		}
		if ok {
			result = append(result, action)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan plan: %w", err)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Identity() < result[j].Identity() })
	return result, nil
}

func parseAtom(raw string) (Action, error) {
	a, err := atom.Parse(strings.TrimPrefix(raw, "="))
	if err != nil {
		return Action{}, fmt.Errorf("parse plan atom %q: %w", raw, err)
	}
	result := Action{CP: a.CP(), Slot: a.Slot, Subslot: a.Subslot, Repository: a.Repo}
	if result.Slot == "" {
		// Portage suppresses the conventional :0 slot in pretend output while
		// Arise renders it explicitly.
		result.Slot = "0"
	}
	if a.Version != nil {
		result.Version = a.Version.Raw
	}
	return result, nil
}

func parseUseGroups(text string) (map[string][]string, map[string]bool) {
	result := make(map[string][]string)
	effective := make(map[string]bool)
	for _, match := range useGroupRE.FindAllStringSubmatch(text, -1) {
		values := strings.Fields(match[2])
		for i := range values {
			values[i] = normalizeUseToken(values[i])
			name, enabled := canonicalUseFlag(match[1], values[i])
			if name != "" {
				effective[name] = enabled
			}
		}
		result[match[1]] = values
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, effective
}

func normalizeUseToken(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "(") && strings.HasSuffix(value, ")") {
		value = strings.TrimSuffix(strings.TrimPrefix(value, "("), ")")
	}
	return strings.TrimRight(value, "%*")
}

func canonicalUseFlag(group, value string) (string, bool) {
	enabled := !strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	if value == "" {
		return "", enabled
	}
	if group == "USE" {
		return value, enabled
	}
	return strings.ToLower(group) + "_" + value, enabled
}

func useMismatches(arise, emerge Action) []string {
	if len(emerge.EffectiveUse) == 0 {
		return nil
	}
	var result []string
	for flag, expected := range emerge.EffectiveUse {
		actual, present := arise.EffectiveUse[flag]
		if !present || actual != expected {
			result = append(result, flag)
		}
	}
	sort.Strings(result)
	return result
}

func Compare(arise, emerge []Action) []Difference {
	a := index(arise)
	e := index(emerge)
	keys := make(map[string]bool, len(a)+len(e))
	for key := range a {
		keys[key] = true
	}
	for key := range e {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	var differences []Difference
	for _, key := range ordered {
		av, aok := a[key]
		ev, eok := e[key]
		switch {
		case !aok:
			differences = append(differences, Difference{Identity: key, Kind: "only-emerge", Emerge: &ev})
		case !eok:
			differences = append(differences, Difference{Identity: key, Kind: "only-arise", Arise: &av})
		case av.Version != ev.Version:
			differences = append(differences, Difference{Identity: key, Kind: "version", Arise: &av, Emerge: &ev})
		case av.Repository != ev.Repository:
			differences = append(differences, Difference{Identity: key, Kind: "location", Arise: &av, Emerge: &ev})
		case av.Kind != ev.Kind:
			differences = append(differences, Difference{Identity: key, Kind: "action", Arise: &av, Emerge: &ev})
		case av.MergeType != "" && ev.MergeType != "" && av.MergeType != ev.MergeType:
			differences = append(differences, Difference{Identity: key, Kind: "merge-type", Arise: &av, Emerge: &ev})
		default:
			if mismatches := useMismatches(av, ev); len(mismatches) > 0 {
				differences = append(differences, Difference{Identity: key, Kind: "use", Arise: &av, Emerge: &ev, UseMismatch: mismatches})
			}
		}
	}
	return differences
}

func index(actions []Action) map[string]Action {
	result := make(map[string]Action, len(actions))
	for _, action := range actions {
		result[action.Identity()] = action
	}
	return result
}
