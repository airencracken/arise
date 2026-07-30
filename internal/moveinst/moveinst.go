// Package moveinst checks and applies Gentoo repository package move updates
// to the installed package database.
package moveinst

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/depstring"
	"github.com/airencracken/arise/internal/journal"
	"github.com/airencracken/arise/internal/vdb"
)

var dependencyKeys = []string{"BDEPEND", "DEPEND", "IDEPEND", "PDEPEND", "RDEPEND"}
var writeMetadata = writeAtomic

type Repository struct {
	Name   string
	Root   string
	Master bool
}

type Command struct {
	Kind, Package, Destination, OldSlot, NewSlot, Repository, Source string
}

type Issue struct {
	CPV     string `json:"cpv"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type Action struct {
	CPV       string            `json:"cpv"`
	ResultCPV string            `json:"result_cpv"`
	From      string            `json:"from"`
	To        string            `json:"to"`
	Files     map[string]string `json:"files"`
	Reasons   []string          `json:"reasons"`
}

type Report struct {
	Issues  []Issue  `json:"issues"`
	Actions []Action `json:"actions"`
}

type ApplyConfig struct {
	RootDir, VDBRoot, JournalDir string
}

func Check(vdbRoot string, repositories []Repository) (Report, error) {
	commands, master, err := loadCommands(repositories)
	if err != nil {
		return Report{}, err
	}
	packages, err := vdb.Scan(vdbRoot)
	if err != nil {
		return Report{}, err
	}
	var report Report
	for _, pkg := range packages {
		rules := commands[pkg.Repository]
		if _, configured := commands[pkg.Repository]; !configured {
			rules = commands[master]
		}
		action, changed, err := checkPackage(vdbRoot, pkg, rules)
		if err != nil {
			return Report{}, err
		}
		if !changed {
			continue
		}
		if action.From != action.To {
			if target, statErr := os.Stat(action.To); statErr == nil && target.IsDir() {
				sourceBuild, sourceErr := os.ReadFile(filepath.Join(action.From, "BUILD_TIME"))
				targetBuild, targetErr := os.ReadFile(filepath.Join(action.To, "BUILD_TIME"))
				if sourceErr == nil && targetErr == nil && strings.TrimSpace(string(sourceBuild)) != "" &&
					strings.TrimSpace(string(sourceBuild)) == strings.TrimSpace(string(targetBuild)) {
					delete(action.Files, "CATEGORY")
					delete(action.Files, "PF")
					action.To, action.ResultCPV = action.From, action.CPV
					action.Reasons = metadataReasons(action)
					if len(action.Files) == 0 {
						continue
					}
				} else {
					return Report{}, fmt.Errorf("moveinst: target VDB entry already exists: %s", action.To)
				}
			} else if statErr != nil && !os.IsNotExist(statErr) {
				return Report{}, statErr
			}
		}
		report.Actions = append(report.Actions, action)
		for _, reason := range action.Reasons {
			report.Issues = append(report.Issues, Issue{CPV: pkg.CPV(), Kind: reasonKind(reason), Message: reason})
		}
	}
	sort.Slice(report.Actions, func(i, j int) bool { return report.Actions[i].CPV < report.Actions[j].CPV })
	sort.Slice(report.Issues, func(i, j int) bool {
		if report.Issues[i].CPV == report.Issues[j].CPV {
			return report.Issues[i].Message < report.Issues[j].Message
		}
		return report.Issues[i].CPV < report.Issues[j].CPV
	})
	return report, nil
}

func metadataReasons(action Action) []string {
	var reasons []string
	for _, reason := range action.Reasons {
		if !strings.Contains(reason, " moved to ") {
			reasons = append(reasons, reason)
		}
	}
	for _, key := range dependencyKeys {
		if _, exists := action.Files[key]; exists {
			reasons = append(reasons, fmt.Sprintf("'%s' has outdated metadata", action.CPV))
			break
		}
	}
	return reasons
}

func checkPackage(vdbRoot string, pkg vdb.Package, commands []Command) (Action, bool, error) {
	category, packageName, slot := pkg.Category, pkg.Package, pkg.Slot
	metadata := map[string]string{
		"BDEPEND": pkg.BDepend, "DEPEND": pkg.Depend, "IDEPEND": pkg.IDepend,
		"PDEPEND": pkg.PDepend, "RDEPEND": pkg.RDepend,
	}
	var reasons []string
	for _, command := range commands {
		switch command.Kind {
		case "move":
			if category+"/"+packageName == command.Package {
				target, err := atom.ParsePackageAtom(command.Destination)
				if err != nil {
					return Action{}, false, fmt.Errorf("moveinst: invalid move target %q: %w", command.Destination, err)
				}
				category, packageName = target.Category, target.Package
				reasons = append(reasons, fmt.Sprintf("'%s' moved to '%s'", pkg.CPV(), command.Destination))
			}
		case "slotmove":
			if category+"/"+packageName == command.Package && slot == command.OldSlot {
				slot = command.NewSlot
				reasons = append(reasons, fmt.Sprintf("'%s' slot moved from '%s' to '%s'", pkg.CPV(), command.OldSlot, command.NewSlot))
			}
		}
		for _, key := range dependencyKeys {
			updated, err := updateDependency(metadata[key], command)
			if err != nil {
				return Action{}, false, fmt.Errorf("moveinst: update %s for %s: %w", key, pkg.CPV(), err)
			}
			metadata[key] = updated
		}
	}
	files := map[string]string{}
	metadataChanged := false
	original := map[string]string{
		"BDEPEND": pkg.BDepend, "DEPEND": pkg.Depend, "IDEPEND": pkg.IDepend,
		"PDEPEND": pkg.PDepend, "RDEPEND": pkg.RDepend,
	}
	for _, key := range dependencyKeys {
		if metadata[key] != original[key] {
			files[key] = metadata[key] + "\n"
			metadataChanged = true
		}
	}
	if slot != pkg.Slot {
		slotValue := slot
		if pkg.Subslot != "" {
			slotValue += "/" + pkg.Subslot
		}
		files["SLOT"] = slotValue + "\n"
	}
	resultCPV := category + "/" + packageName + "-" + pkg.Version
	from := filepath.Join(vdbRoot, pkg.Category, pkg.Package+"-"+pkg.Version)
	to := filepath.Join(vdbRoot, category, packageName+"-"+pkg.Version)
	if from != to {
		files["CATEGORY"] = category + "\n"
		files["PF"] = packageName + "-" + pkg.Version + "\n"
	}
	if from == to && len(files) == 0 {
		return Action{}, false, nil
	}
	if len(reasons) == 0 {
		reasons = []string{fmt.Sprintf("'%s' has outdated metadata", pkg.CPV())}
	} else if metadataChanged {
		reasons = append(reasons, fmt.Sprintf("'%s' has outdated metadata", pkg.CPV()))
	}
	return Action{CPV: pkg.CPV(), ResultCPV: resultCPV, From: from, To: to, Files: files, Reasons: reasons}, true, nil
}

func updateDependency(value string, command Command) (string, error) {
	if strings.TrimSpace(value) == "" {
		return value, nil
	}
	tree, err := depstring.Parse(value)
	if err != nil {
		return "", err
	}
	if !updateNode(tree, command) {
		return value, nil
	}
	return tree.String(), nil
}

func updateNode(node depstring.DepNode, command Command) bool {
	updateAtom := func(raw string) (string, bool) {
		parsed, err := atom.Parse(raw)
		if err != nil {
			return raw, false
		}
		original := parsed.String()
		switch command.Kind {
		case "move":
			if parsed.CP() == command.Package {
				target, targetErr := atom.ParsePackageAtom(command.Destination)
				if targetErr == nil {
					parsed.Category, parsed.Package = target.Category, target.Package
				}
			}
		case "slotmove":
			if parsed.CP() == command.Package && parsed.Slot == command.OldSlot {
				parsed.Slot = command.NewSlot
			}
		}
		updated := parsed.String()
		return updated, updated != original
	}
	changed := false
	switch value := node.(type) {
	case *depstring.AtomDep:
		value.Atom, changed = updateAtom(value.Atom)
	case *depstring.Block:
		value.Atom, changed = updateAtom(value.Atom)
	case *depstring.WeakBlock:
		value.Atom, changed = updateAtom(value.Atom)
	case *depstring.AllOfGroup:
		for _, child := range value.Children {
			changed = updateNode(child, command) || changed
		}
	case *depstring.AnyOfGroup:
		for _, child := range value.Children {
			changed = updateNode(child, command) || changed
		}
	case *depstring.XorOfGroup:
		for _, child := range value.Children {
			changed = updateNode(child, command) || changed
		}
	case *depstring.AtMostOneOfGroup:
		for _, child := range value.Children {
			changed = updateNode(child, command) || changed
		}
	case *depstring.UseConditional:
		for _, child := range value.Children {
			changed = updateNode(child, command) || changed
		}
	}
	return changed
}

func loadCommands(repositories []Repository) (map[string][]Command, string, error) {
	result := map[string][]Command{}
	master := ""
	for _, repository := range repositories {
		if repository.Master {
			master = repository.Name
		}
		updatesRoot := filepath.Join(repository.Root, "profiles", "updates")
		entries, err := os.ReadDir(updatesRoot)
		if os.IsNotExist(err) {
			result[repository.Name] = nil
			continue
		}
		if err != nil {
			return nil, "", err
		}
		var names []string
		for _, entry := range entries {
			if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
				names = append(names, entry.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			file, err := os.Open(filepath.Join(updatesRoot, name))
			if err != nil {
				return nil, "", err
			}
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				line := strings.TrimSpace(strings.SplitN(scanner.Text(), "#", 2)[0])
				if line == "" {
					continue
				}
				fields := strings.Fields(line)
				command := Command{Repository: repository.Name, Source: name}
				switch {
				case len(fields) == 3 && fields[0] == "move":
					command.Kind, command.Package, command.Destination = fields[0], fields[1], fields[2]
				case len(fields) == 4 && fields[0] == "slotmove":
					command.Kind, command.Package, command.OldSlot, command.NewSlot = fields[0], fields[1], fields[2], fields[3]
				default:
					_ = file.Close()
					return nil, "", fmt.Errorf("moveinst: invalid update command %q in %s", line, name)
				}
				if _, err := atom.ParsePackageAtom(command.Package); err != nil {
					_ = file.Close()
					return nil, "", fmt.Errorf("moveinst: invalid package %q in %s", command.Package, name)
				}
				result[repository.Name] = append(result[repository.Name], command)
			}
			scanErr := scanner.Err()
			closeErr := file.Close()
			if scanErr != nil {
				return nil, "", scanErr
			}
			if closeErr != nil {
				return nil, "", closeErr
			}
		}
	}
	return result, master, nil
}

func reasonKind(reason string) string {
	if strings.Contains(reason, "slot moved") {
		return "slotmove"
	}
	if strings.Contains(reason, "moved to") {
		return "move"
	}
	return "metadata"
}

func Apply(actions []Action, config ApplyConfig) (returnErr error) {
	if config.RootDir == "" || config.VDBRoot == "" || config.JournalDir == "" {
		return fmt.Errorf("moveinst: incomplete apply configuration")
	}
	for _, action := range actions {
		if err := validateAction(config.VDBRoot, action); err != nil {
			return err
		}
	}
	var operation *journal.Journal
	var err error
	if filepath.Clean(config.RootDir) == string(filepath.Separator) {
		operation, err = journal.BeginLiveRoot(config.JournalDir)
	} else {
		operation, err = journal.Begin(config.JournalDir, config.RootDir)
	}
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			returnErr = errors.Join(returnErr, operation.Rollback())
		}
	}()
	for _, action := range actions {
		if action.From != action.To {
			if err := operation.CaptureTree(action.From); err != nil {
				return err
			}
			if err := operation.CaptureAbsentTree(action.To); err != nil {
				return err
			}
			continue
		}
		var paths []string
		for name := range action.Files {
			paths = append(paths, filepath.Join(action.From, name))
		}
		sort.Strings(paths)
		if err := operation.CaptureBatch(paths); err != nil {
			return err
		}
	}
	for _, action := range actions {
		target := action.From
		if action.From != action.To {
			if err := os.MkdirAll(filepath.Dir(action.To), 0o755); err != nil {
				return err
			}
			if err := os.Rename(action.From, action.To); err != nil {
				return err
			}
			target = action.To
		}
		names := make([]string, 0, len(action.Files))
		for name := range action.Files {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			value := action.Files[name]
			if err := writeMetadata(filepath.Join(target, name), []byte(value)); err != nil {
				return err
			}
		}
	}
	if err := operation.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func validateAction(vdbRoot string, action Action) error {
	validPackagePath := func(path string) bool {
		relative, err := filepath.Rel(vdbRoot, path)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return false
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		return len(parts) == 2 && parts[0] != "" && parts[1] != "" && parts[0] != "." && parts[1] != "."
	}
	if !validPackagePath(action.From) || !validPackagePath(action.To) {
		return fmt.Errorf("moveinst: action %s has path outside direct VDB package entries", action.CPV)
	}
	allowed := map[string]bool{
		"BDEPEND": true, "DEPEND": true, "IDEPEND": true, "PDEPEND": true, "RDEPEND": true,
		"SLOT": true, "CATEGORY": true, "PF": true,
	}
	for name := range action.Files {
		if !allowed[name] {
			return fmt.Errorf("moveinst: action %s has unsafe metadata name %q", action.CPV, name)
		}
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	mode := os.FileMode(0o644)
	uid, gid := -1, -1
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			uid, gid = int(stat.Uid), int(stat.Gid)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if uid >= 0 {
		if err := file.Chown(uid, gid); err != nil {
			_ = file.Close()
			return err
		}
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	err = directory.Sync()
	if closeErr := directory.Close(); err == nil {
		err = closeErr
	}
	return err
}
