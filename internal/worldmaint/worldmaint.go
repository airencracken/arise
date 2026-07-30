package worldmaint

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/vdb"
)

type Kind string

const (
	Invalid      Kind = "invalid"
	Duplicate    Kind = "duplicate"
	Moved        Kind = "moved"
	Redundant    Kind = "redundant"
	Unavailable  Kind = "unavailable"
	Masked       Kind = "masked"
	NotInstalled Kind = "not_installed"
)

type Issue struct {
	Entry       string `json:"entry"`
	Kind        Kind   `json:"kind"`
	Message     string `json:"message"`
	Replacement string `json:"replacement,omitempty"`
}

type Action struct {
	Action string `json:"action"`
	Entry  string `json:"entry"`
	Value  string `json:"value,omitempty"`
	Reason Kind   `json:"reason"`
}

type Report struct {
	Entries []string `json:"entries"`
	Issues  []Issue  `json:"issues"`
	Actions []Action `json:"actions"`
}

type candidate struct {
	cpv, slot, repo, keywords string
}

func Check(worldPath, vdbRoot, repoRoot string, config *portage.Config) (Report, error) {
	return CheckRepositories(worldPath, vdbRoot, []string{repoRoot}, config)
}

func CheckRepositories(worldPath, vdbRoot string, repoRoots []string, config *portage.Config) (Report, error) {
	entries, duplicates, err := readWorld(worldPath)
	if err != nil {
		return Report{}, err
	}
	installed, err := vdb.Scan(vdbRoot)
	if err != nil {
		return Report{}, err
	}
	var available []candidate
	moves := make(map[string]string)
	for _, repoRoot := range uniquePaths(repoRoots) {
		repoCandidates, err := readCandidates(repoRoot)
		if err != nil {
			return Report{}, err
		}
		available = append(available, repoCandidates...)
		repoMoves, err := readMoves(repoRoot)
		if err != nil {
			return Report{}, err
		}
		for before, after := range repoMoves {
			moves[before] = after
		}
	}

	report := Report{Entries: append([]string(nil), entries...)}
	plainEntries := make(map[string]bool)
	for _, entry := range entries {
		parsed, parseErr := atom.ParsePackageAtom(entry)
		if parseErr == nil && isPlainCP(parsed) {
			plainEntries[parsed.CP()] = true
		}
	}
	for _, duplicate := range duplicates {
		report.Issues = append(report.Issues, Issue{Entry: duplicate, Kind: Duplicate, Message: fmt.Sprintf("%q appears more than once", duplicate)})
		report.Actions = append(report.Actions, Action{Action: "deduplicate", Entry: duplicate, Reason: Duplicate})
	}
	for _, entry := range entries {
		parsed, parseErr := atom.ParsePackageAtom(entry)
		if parseErr != nil || parsed.Category == "" || parsed.Package == "" {
			message := fmt.Sprintf("%q is not a valid package atom", entry)
			if parseErr != nil {
				message += ": " + parseErr.Error()
			}
			report.Issues = append(report.Issues, Issue{Entry: entry, Kind: Invalid, Message: message})
			report.Actions = append(report.Actions, Action{Action: "remove", Entry: entry, Reason: Invalid})
			continue
		}
		if !isPlainCP(parsed) && plainEntries[parsed.CP()] {
			report.Issues = append(report.Issues, Issue{
				Entry: entry, Kind: Redundant,
				Message: fmt.Sprintf("%q is redundant because %q is already selected", entry, parsed.CP()),
			})
			report.Actions = append(report.Actions, Action{Action: "remove", Entry: entry, Reason: Redundant})
			continue
		}
		matchingAvailable := matchCandidates(parsed, available)
		matchingInstalled := matchInstalled(parsed, installed)
		if len(matchingAvailable) == 0 {
			if movedCP := moves[parsed.CP()]; movedCP != "" {
				replacement, replacementErr := replaceAtomCP(parsed, movedCP)
				if replacementErr != nil {
					return Report{}, replacementErr
				}
				report.Issues = append(report.Issues, Issue{Entry: entry, Kind: Moved, Replacement: replacement, Message: fmt.Sprintf("%q moved to %q", entry, replacement)})
				report.Actions = append(report.Actions, Action{Action: "replace", Entry: entry, Value: replacement, Reason: Moved})
			} else {
				report.Issues = append(report.Issues, Issue{Entry: entry, Kind: Unavailable, Message: fmt.Sprintf("%q has no available ebuilds", entry)})
				report.Actions = append(report.Actions, Action{Action: "remove", Entry: entry, Reason: Unavailable})
			}
			continue
		}
		if len(matchingInstalled) == 0 {
			report.Issues = append(report.Issues, Issue{Entry: entry, Kind: NotInstalled, Message: fmt.Sprintf("%q is not installed", entry)})
			report.Actions = append(report.Actions, Action{Action: "remove", Entry: entry, Reason: NotInstalled})
			continue
		}
		allMasked := true
		for _, pkg := range matchingAvailable {
			keywordAccepted := true
			if config != nil && pkg.keywords != "" && config.MakeConf["ARCH"] != "" {
				keywordAccepted = config.KeywordAcceptedFor(pkg.cpv, pkg.slot, pkg.repo, pkg.keywords, config.MakeConf["ARCH"])
			}
			if config == nil || (!config.PackageMaskStatus(pkg.cpv, pkg.slot, pkg.repo).Masked && keywordAccepted) {
				allMasked = false
				break
			}
		}
		if allMasked {
			report.Issues = append(report.Issues, Issue{Entry: entry, Kind: Masked, Message: fmt.Sprintf("%q has no visible ebuilds", entry)})
			report.Actions = append(report.Actions, Action{Action: "remove", Entry: entry, Reason: Masked})
		}
	}
	sort.SliceStable(report.Issues, func(i, j int) bool {
		if report.Issues[i].Entry == report.Issues[j].Entry {
			return report.Issues[i].Kind < report.Issues[j].Kind
		}
		return report.Issues[i].Entry < report.Issues[j].Entry
	})
	sort.SliceStable(report.Actions, func(i, j int) bool {
		if report.Actions[i].Entry == report.Actions[j].Entry {
			return report.Actions[i].Action < report.Actions[j].Action
		}
		return report.Actions[i].Entry < report.Actions[j].Entry
	})
	return report, nil
}

func uniquePaths(paths []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." || seen[path] {
			continue
		}
		seen[path] = true
		result = append(result, path)
	}
	return result
}

func Apply(entries []string, actions []Action) []string {
	values := append([]string(nil), entries...)
	for _, action := range actions {
		switch action.Action {
		case "remove":
			values = removeAll(values, action.Entry)
		case "replace":
			values = removeAll(values, action.Entry)
			values = append(values, action.Value)
		}
	}
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, entry := range values {
		if entry == "" || seen[entry] {
			continue
		}
		seen[entry] = true
		result = append(result, entry)
	}
	sort.Strings(result)
	return result
}

func readWorld(path string) ([]string, []string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	var entries, duplicates []string
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entries = append(entries, line)
		if seen[line] {
			duplicates = append(duplicates, line)
		}
		seen[line] = true
	}
	return entries, duplicates, scanner.Err()
}

func readCandidates(repoRoot string) ([]candidate, error) {
	cacheRoot := filepath.Join(repoRoot, "metadata", "md5-cache")
	repo := filepath.Base(repoRoot)
	if data, err := os.ReadFile(filepath.Join(repoRoot, "profiles", "repo_name")); err == nil {
		repo = strings.TrimSpace(string(data))
	}
	var candidates []candidate
	err := filepath.WalkDir(cacheRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(cacheRoot, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		item, err := metadata.ParseCacheEntry(filepath.ToSlash(relative), data)
		if err != nil {
			return nil
		}
		candidates = append(candidates, candidate{
			cpv: item.Category + "/" + item.Package + "-" + item.Version, slot: item.SLOT, repo: repo, keywords: item.KEYWORDS,
		})
		return nil
	})
	if os.IsNotExist(err) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(candidates))
	for _, item := range candidates {
		seen[item.cpv+"::"+item.repo] = true
	}
	entries, readErr := os.ReadDir(repoRoot)
	if readErr != nil {
		return nil, readErr
	}
	for _, category := range entries {
		if !category.IsDir() || strings.HasPrefix(category.Name(), ".") ||
			category.Name() == "metadata" || category.Name() == "profiles" || category.Name() == "eclass" {
			continue
		}
		packages, packageErr := os.ReadDir(filepath.Join(repoRoot, category.Name()))
		if packageErr != nil {
			return nil, packageErr
		}
		for _, packageEntry := range packages {
			if !packageEntry.IsDir() {
				continue
			}
			ebuilds, ebuildErr := filepath.Glob(filepath.Join(repoRoot, category.Name(), packageEntry.Name(), "*.ebuild"))
			if ebuildErr != nil {
				return nil, ebuildErr
			}
			for _, ebuildPath := range ebuilds {
				cpv := category.Name() + "/" + strings.TrimSuffix(filepath.Base(ebuildPath), ".ebuild")
				parsedCategory, parsedPackage, version, parseErr := metadata.ParseCPV(cpv)
				if parseErr != nil || parsedCategory != category.Name() || parsedPackage != packageEntry.Name() {
					continue
				}
				cpv = parsedCategory + "/" + parsedPackage + "-" + version
				key := cpv + "::" + repo
				if seen[key] {
					continue
				}
				seen[key] = true
				candidates = append(candidates, candidate{cpv: cpv, repo: repo})
			}
		}
	}
	return candidates, nil
}

func readMoves(repoRoot string) (map[string]string, error) {
	result := make(map[string]string)
	root := filepath.Join(repoRoot, "profiles", "updates")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		file, err := os.Open(filepath.Join(root, entry.Name()))
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) == 3 && fields[0] == "move" {
				result[fields[1]] = fields[2]
			}
		}
		scanErr := scanner.Err()
		closeErr := file.Close()
		if scanErr != nil {
			return nil, scanErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	return result, nil
}

func matchCandidates(rule *atom.Atom, candidates []candidate) []candidate {
	var result []candidate
	for _, item := range candidates {
		if portage.PackageAtomMatches(rule.String(), item.cpv, item.slot, item.repo) {
			result = append(result, item)
		}
	}
	return result
}

func matchInstalled(rule *atom.Atom, packages []vdb.Package) []vdb.Package {
	var result []vdb.Package
	for _, item := range packages {
		if portage.PackageAtomMatches(rule.String(), item.CPV(), item.Slot, item.Repository) {
			result = append(result, item)
		}
	}
	return result
}

func isPlainCP(value *atom.Atom) bool {
	return value != nil && value.Op == atom.OpNone && value.Version == nil && value.Slot == "" && value.Repo == "" && len(value.UseFlags) == 0
}

func replaceAtomCP(value *atom.Atom, replacement string) (string, error) {
	target, err := atom.ParsePackageAtom(replacement)
	if err != nil || !isPlainCP(target) {
		return "", fmt.Errorf("invalid package move target %q", replacement)
	}
	updated := *value
	updated.Category, updated.Package = target.Category, target.Package
	return updated.String(), nil
}

func removeAll(values []string, target string) []string {
	result := values[:0]
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}
