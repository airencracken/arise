// Package plandiff compares frozen, independently validatable package plans.
package plandiff

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/planvalidate"
)

const SchemaVersion = 1

type Change struct {
	Identity string               `json:"identity"`
	Kind     string               `json:"kind"`
	Before   *planvalidate.Action `json:"before,omitempty"`
	After    *planvalidate.Action `json:"after,omitempty"`
	Fields   []string             `json:"fields,omitempty"`
}

type Diff struct {
	Schema  int             `json:"schema"`
	Context []ContextChange `json:"context"`
	Changes []Change        `json:"changes"`
}

type ContextChange struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

func Compare(before, after planvalidate.Plan) (Diff, error) {
	if before.Schema != planvalidate.SchemaVersion || after.Schema != planvalidate.SchemaVersion {
		return Diff{}, fmt.Errorf("plan diff: both plans must use schema %d", planvalidate.SchemaVersion)
	}
	left, err := indexActions(before.Actions)
	if err != nil {
		return Diff{}, err
	}
	right, err := indexActions(after.Actions)
	if err != nil {
		return Diff{}, err
	}
	identities := make(map[string]bool, len(left)+len(right))
	for identity := range left {
		identities[identity] = true
	}
	for identity := range right {
		identities[identity] = true
	}
	ordered := make([]string, 0, len(identities))
	for identity := range identities {
		ordered = append(ordered, identity)
	}
	sort.Strings(ordered)
	diff := Diff{Schema: SchemaVersion, Context: []ContextChange{}, Changes: []Change{}}
	for _, identity := range ordered {
		beforeAction, hadBefore := left[identity]
		afterAction, hasAfter := right[identity]
		switch {
		case !hadBefore:
			action := afterAction
			diff.Changes = append(diff.Changes, Change{Identity: identity, Kind: "added", After: &action})
		case !hasAfter:
			action := beforeAction
			diff.Changes = append(diff.Changes, Change{Identity: identity, Kind: "removed", Before: &action})
		default:
			fields := changedFields(beforeAction, afterAction)
			if len(fields) != 0 {
				leftAction, rightAction := beforeAction, afterAction
				diff.Changes = append(diff.Changes, Change{Identity: identity, Kind: "changed", Before: &leftAction, After: &rightAction, Fields: fields})
			}
		}
	}
	return diff, nil
}

func actionIdentity(action planvalidate.Action) string {
	if strings.TrimSpace(action.ID) != "" {
		return action.ID
	}
	return strings.Join([]string{action.Kind, action.Package.CPV, action.Package.Slot, action.Package.Repository}, "|")
}

func indexActions(actions []planvalidate.Action) (map[string]planvalidate.Action, error) {
	result := make(map[string]planvalidate.Action, len(actions))
	for _, action := range actions {
		identity := actionIdentity(action)
		if _, exists := result[identity]; exists {
			return nil, fmt.Errorf("plan diff: duplicate action identity %q", identity)
		}
		result[identity] = action
	}
	return result, nil
}

func changedFields(before, after planvalidate.Action) []string {
	var fields []string
	checks := []struct {
		name        string
		left, right any
	}{
		{"kind", before.Kind, after.Kind}, {"package", before.Package, after.Package},
		{"replaces", before.Replaces, after.Replaces}, {"prerequisites", before.Prerequisites, after.Prerequisites},
	}
	for _, check := range checks {
		left, _ := json.Marshal(check.left)
		right, _ := json.Marshal(check.right)
		if string(left) != string(right) {
			fields = append(fields, check.name)
		}
	}
	return fields
}
