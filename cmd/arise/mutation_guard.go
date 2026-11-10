package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/depstring"
	"github.com/airencracken/arise/internal/planadapter"
	"github.com/airencracken/arise/internal/planvalidate"
	"github.com/airencracken/arise/internal/resolve"
	"github.com/airencracken/arise/internal/vdb"
)

// packageMutationGuard is called only while the executor holds the operation
// VDB lock. The mutex also protects progress callbacks from parallel workers.
type packageMutationGuard struct {
	mu                            sync.Mutex
	vdbDir, worldPath, configRoot string
	actions                       []resolve.PkgAction
	snapshot                      mutationStateSnapshot
	audit                         *independentPlanAudit
	completed                     map[string]bool
	installed                     []planvalidate.Package
	checked                       bool
	output                        io.Writer
	onRevalidate                  func()
}

func (guard *packageMutationGuard) validate() error {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	current, err := captureMutationState(guard.vdbDir, guard.worldPath, guard.configRoot, guard.actions)
	if err != nil {
		return err
	}
	if current.policy != guard.snapshot.policy {
		return &planChangeError{reason: "Package settings or selected sources changed while this package was building."}
	}
	// World bookkeeping may be updated by another install after it releases
	// the VDB lock. It neither changes the frozen actions nor grants new ones.
	if guard.checked && current.vdb == guard.snapshot.vdb {
		guard.snapshot = current
		return nil
	}
	if guard.audit == nil {
		return &planChangeError{reason: "Arise cannot verify the current package state because the approved plan evidence is unavailable."}
	}
	pending := pendingMutationAudit(guard.audit, guard.completed)
	pending.fixture.Installed, err = guard.installedState(current)
	if err != nil {
		return err
	}
	previous := guard.installed
	if previous == nil {
		previous = guard.audit.fixture.Installed
	}
	protected, err := protectedMutationPackages(pending.plan, previous, pending.fixture.Installed)
	if err != nil {
		return err
	}
	for _, cp := range changedMutationPackages(guard.snapshot.vdbEntries, current.vdbEntries) {
		if protected[cp] {
			return &planChangeError{reason: fmt.Sprintf("Installed package %s, which the remaining plan depends on, changed while this package was building.", cp), resume: true}
		}
	}
	validation := pending.validate()
	if !validation.Valid {
		if guard.output != nil {
			reportIndependentPlanAudit(guard.output, "locked-pre-mutation", validation)
		}
		return &planChangeError{reason: "Concurrent package changes invalidate the remaining plan: required dependencies no longer match.", resume: true}
	}
	// Non-cooperating writers or policy changes must not be adopted merely
	// because they happened while the independent validation was running.
	after, err := captureMutationState(guard.vdbDir, guard.worldPath, guard.configRoot, guard.actions)
	if err != nil {
		return err
	}
	if after.policy != current.policy || after.vdb != current.vdb {
		return &planChangeError{reason: "Package state or settings changed while Arise was checking the remaining plan.", resume: true}
	}
	externalChange := current.vdb != guard.snapshot.vdb
	guard.snapshot, guard.installed, guard.checked = after, pending.fixture.Installed, true
	if externalChange && guard.onRevalidate != nil {
		guard.onRevalidate()
	}
	return nil
}

func (guard *packageMutationGuard) record() error {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	current, err := captureMutationState(guard.vdbDir, guard.worldPath, guard.configRoot, guard.actions)
	if err != nil {
		return fmt.Errorf("record committed package state: %w", err)
	}
	installed, err := guard.installedState(current)
	if err != nil {
		return fmt.Errorf("record committed installed state: %w", err)
	}
	guard.snapshot, guard.installed = current, installed
	return nil
}

func (guard *packageMutationGuard) installedState(current mutationStateSnapshot) ([]planvalidate.Package, error) {
	root := guard.vdbDir
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	packages, issues, err := vdb.ScanWithIssues(context.Background(), root, false)
	if err != nil {
		return nil, fmt.Errorf("read installed state under operation lock: %w", err)
	}
	for _, issue := range issues {
		relative, pathErr := filepath.Rel(root, issue.Path)
		if issue.Package != nil {
			relative = issue.Package.Category + "/" + issue.Package.Name + "-" + issue.Package.Version
		}
		if pathErr != nil || mutationEntryChanged(guard.snapshot.vdbEntries, current.vdbEntries, filepath.ToSlash(relative)) {
			return nil, fmt.Errorf("changed installed metadata cannot be revalidated: %w", issue)
		}
	}
	return planadapter.InstalledState(packages), nil
}

func (guard *packageMutationGuard) complete(action resolve.PkgAction) {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.completed == nil {
		guard.completed = make(map[string]bool)
	}
	guard.completed[resolve.ActionIdentity(action)] = true
}

func pendingMutationAudit(original *independentPlanAudit, completed map[string]bool) *independentPlanAudit {
	pending := &independentPlanAudit{fixture: original.fixture, plan: original.plan}
	pending.plan.Actions = nil
	for _, action := range original.plan.Actions {
		if completed[action.ID] {
			continue
		}
		prerequisites := action.Prerequisites
		action.Prerequisites = nil
		for _, prerequisite := range prerequisites {
			if !completed[prerequisite] {
				action.Prerequisites = append(action.Prerequisites, prerequisite)
			}
		}
		pending.plan.Actions = append(pending.plan.Actions, action)
	}
	pending.plan.Decisions.Records = nil
	pending.plan.Decisions.EncodedBytes = 0
	for _, record := range original.plan.Decisions.Records {
		if record.Outcome == "selected" && completed[record.ActionID] {
			continue
		}
		pending.plan.Decisions.Records = append(pending.plan.Decisions.Records, record)
		encoded, _ := json.Marshal(record)
		pending.plan.Decisions.EncodedBytes += len(encoded)
	}
	return pending
}

func changedMutationPackages(previous, current map[string]string) []string {
	changed := make(map[string]bool)
	check := func(path string) {
		if previous[path] == current[path] {
			return
		}
		parts := strings.SplitN(path, "/", 3)
		if len(parts) < 2 {
			return
		}
		parsed, err := atom.ParsePackageAtom("=" + parts[0] + "/" + parts[1])
		if err == nil && parsed.Version != nil {
			changed[parsed.CP()] = true
		}
	}
	for path := range previous {
		check(path)
	}
	for path := range current {
		check(path)
	}
	result := make([]string, 0, len(changed))
	for cp := range changed {
		result = append(result, cp)
	}
	sort.Strings(result)
	return result
}

// Protect the inputs of images that may already be built, including runtime
// dependencies of their tools. New reverse consumers are checked independently
// against the complete pending plan, rather than being assumed unrelated.
func mutationEntryChanged(previous, current map[string]string, path string) bool {
	if path == ".." || strings.HasPrefix(path, "../") {
		return true
	}
	changed := func(entry string) bool {
		return (path == "." || entry == path || strings.HasPrefix(entry, path+"/")) && previous[entry] != current[entry]
	}
	for entry := range previous {
		if changed(entry) {
			return true
		}
	}
	for entry := range current {
		if changed(entry) {
			return true
		}
	}
	return false
}

func protectedMutationPackages(plan planvalidate.Plan, previous, current []planvalidate.Package) (map[string]bool, error) {
	protected := make(map[string]bool)
	var queue []string
	add := func(cp string) {
		if !protected[cp] {
			protected[cp] = true
			queue = append(queue, cp)
		}
	}
	dependencies := func(pkg planvalidate.Package, building bool) error {
		for class, expression := range pkg.Dependencies {
			if building && pkg.MergeType == "binary" && (class == "DEPEND" || class == "BDEPEND") {
				continue
			}
			if !building && class != "RDEPEND" && class != "PDEPEND" {
				continue
			}
			if strings.TrimSpace(expression) == "" {
				continue
			}
			tree, err := depstring.Parse(expression)
			if err != nil {
				return fmt.Errorf("inspect %s dependencies of %s: %w", class, pkg.CPV, err)
			}
			for _, meta := range depstring.CollectMeta(tree) {
				if meta.Block || meta.WeakBlock {
					continue
				}
				enabled := true
				for _, condition := range strings.Split(meta.Condition, ",") {
					condition = strings.TrimSpace(condition)
					if condition == "" {
						continue
					}
					if pkg.Use[strings.TrimPrefix(condition, "!")] == strings.HasPrefix(condition, "!") {
						enabled = false
						break
					}
				}
				if !enabled {
					continue
				}
				parsed, err := atom.ParsePackageAtom(meta.Atom)
				if err != nil {
					return err
				}
				add(parsed.CP())
			}
		}
		return nil
	}
	for _, action := range plan.Actions {
		parsed, err := atom.ParsePackageAtom("=" + action.Package.CPV)
		if err != nil {
			return nil, err
		}
		add(parsed.CP())
		if action.Kind == planvalidate.ActionInstall {
			if err := dependencies(action.Package, true); err != nil {
				return nil, err
			}
		}
	}
	byCP := make(map[string][]planvalidate.Package)
	for _, packages := range [][]planvalidate.Package{previous, current} {
		for _, pkg := range packages {
			parsed, err := atom.ParsePackageAtom("=" + pkg.CPV)
			if err != nil {
				return nil, err
			}
			byCP[parsed.CP()] = append(byCP[parsed.CP()], pkg)
		}
	}
	for len(queue) != 0 {
		cp := queue[0]
		queue = queue[1:]
		for _, pkg := range byCP[cp] {
			if err := dependencies(pkg, false); err != nil {
				return nil, err
			}
		}
	}
	return protected, nil
}
