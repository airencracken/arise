package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/airencracken/arise/internal/resolve"
)

type canonicalPlanAuthorization struct {
	Version     int          `json:"version"`
	StateSHA256 string       `json:"state_sha256"`
	Operation   string       `json:"operation"`
	Targets     []string     `json:"targets"`
	Options     planOptions  `json:"options"`
	Actions     []jsonAction `json:"actions"`
	Uninstall   []jsonAction `json:"uninstall"`
}

type planOptions struct {
	Update        bool    `json:"update"`
	Deep          bool    `json:"deep"`
	CompleteGraph bool    `json:"complete_graph"`
	NewUse        bool    `json:"newuse"`
	ChangedUse    bool    `json:"changed_use"`
	ChangedDeps   bool    `json:"changed_deps"`
	EmptyTree     bool    `json:"empty_tree"`
	Reinstall     bool    `json:"reinstall"`
	OnlyDeps      bool    `json:"only_deps"`
	NoDeps        bool    `json:"no_deps"`
	Oneshot       bool    `json:"oneshot"`
	WithBdeps     string  `json:"with_bdeps"`
	RootDeps      string  `json:"root_deps"`
	Backtrack     int     `json:"backtrack"`
	Jobs          int     `json:"jobs"`
	LoadAverage   float64 `json:"load_average"`
}

func optionsForPlan(cfg resolve.ResolveConfig) planOptions {
	return planOptions{Update: cfg.Update, Deep: cfg.Deep, CompleteGraph: cfg.CompleteGraph, NewUse: cfg.NewUse,
		ChangedUse: cfg.ChangedUse, ChangedDeps: cfg.ChangedDeps, EmptyTree: cfg.EmptyTree, Reinstall: cfg.Reinstall,
		OnlyDeps: cfg.OnlyDeps, NoDeps: cfg.NoDeps, Oneshot: cfg.Oneshot, WithBdeps: cfg.WithBdeps,
		RootDeps: cfg.RootDeps, Backtrack: cfg.Backtrack, Jobs: cfg.Jobs, LoadAverage: cfg.LoadAverage}
}

type canonicalWorldMutation struct {
	Version     int    `json:"version"`
	Operation   string `json:"operation"`
	Atom        string `json:"atom"`
	StateSHA256 string `json:"state_sha256"`
}

func worldMutationPlanSHA256(operation, atomText, stateSHA256 string) string {
	encoded, err := json.Marshal(canonicalWorldMutation{Version: 1, Operation: operation, Atom: atomText, StateSHA256: stateSHA256})
	if err != nil {
		panic(fmt.Sprintf("canonical world mutation encoding: %v", err))
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func worldStateSHA256(path string) (string, error) {
	hash := sha256.New()
	if err := hashStatePath(hash, "world", path); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func canonicalPlanSHA256(targets []string, cfg resolve.ResolveConfig, result *resolve.ResolveResult, stateSHA256 string) string {
	operation := "install"
	if cfg.Update {
		operation = "update"
	}
	document := canonicalPlanAuthorization{
		Version: 1, StateSHA256: stateSHA256, Operation: operation,
		Targets: append([]string(nil), targets...),
		Options: optionsForPlan(cfg),
		Actions: jsonActions(result.Install), Uninstall: jsonActions(result.Uninstall),
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		panic(fmt.Sprintf("canonical plan encoding: %v", err))
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

// mutationStateSHA256 binds authorization to package state and user policy.
// Selected repository package/eclass sources are included without embedding
// host paths. Resolution is rerun before execution, so unrelated repository
// files do not invalidate an otherwise identical approved plan.
func mutationStateSHA256(vdbDir, worldPath, configRoot string, actions []resolve.PkgAction) (string, error) {
	hash := sha256.New()
	// Command-facing Portage variables are part of the effective policy even
	// though they are not files. Bind their presence and exact value so an
	// approval produced with a temporary FEATURES/USE override cannot authorize
	// a differently configured execution.
	for _, name := range []string{"FEATURES", "USE", "ACCEPT_KEYWORDS", "ACCEPT_LICENSE", "ARCH", "CHOST", "CBUILD", "CTARGET", "CFLAGS", "CXXFLAGS", "CPPFLAGS", "FFLAGS", "FCFLAGS", "LDFLAGS", "MAKEOPTS", "CC", "CXX", "CPP", "AR", "AS", "LD", "NM", "OBJCOPY", "OBJDUMP", "RANLIB", "READELF", "STRIP"} {
		value, present := os.LookupEnv(name)
		if _, err := io.WriteString(hash, "command-env\x00"+name+"\x00"+strconv.FormatBool(present)+"\x00"+value+"\x00"); err != nil {
			return "", err
		}
	}
	seen := make(map[string]bool)
	inputs := []struct{ label, path string }{
		{"vdb", vdbDir}, {"world", worldPath}, {"portage-config", configRoot},
	}
	for _, action := range actions {
		if action.Atom == nil || action.RepositoryPath == "" {
			continue
		}
		packageDir := filepath.Join(action.RepositoryPath, action.Atom.Category, action.Atom.Package)
		inputs = append(inputs,
			struct{ label, path string }{"package:" + action.Repository + ":" + action.Atom.CP(), packageDir},
			struct{ label, path string }{"eclasses:" + action.Repository, filepath.Join(action.RepositoryPath, "eclass")},
		)
	}
	for _, input := range inputs {
		identity := input.label + "\x00" + filepath.Clean(input.path)
		if seen[identity] {
			continue
		}
		seen[identity] = true
		if err := hashStatePath(hash, input.label, input.path); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func hashStatePath(dst io.Writer, label, path string) error {
	root := filepath.Clean(path)
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		_, err = io.WriteString(dst, label+"\x00missing\x00")
		return err
	}
	if err != nil {
		return fmt.Errorf("fingerprint %s: %w", label, err)
	}
	writeEntry := func(relative, current string, info os.FileInfo) error {
		if _, err := io.WriteString(dst, label+"\x00"+filepath.ToSlash(relative)+"\x00"+strconv.FormatUint(uint64(info.Mode()), 10)+"\x00"); err != nil {
			return err
		}
		switch {
		case info.Mode().IsRegular():
			file, err := os.Open(current)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(dst, file)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			return closeErr
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(current)
			if err != nil {
				return err
			}
			_, err = io.WriteString(dst, target)
			return err
		default:
			return nil
		}
	}
	if !info.IsDir() {
		return writeEntry(".", root, info)
	}
	var paths []string
	if err := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Name() == ".lock" || strings.HasSuffix(entry.Name(), ".lock") {
			return nil
		}
		paths = append(paths, current)
		return nil
	}); err != nil {
		return fmt.Errorf("fingerprint %s: %w", label, err)
	}
	sort.Strings(paths)
	for _, current := range paths {
		entryInfo, err := os.Lstat(current)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if err := writeEntry(relative, current, entryInfo); err != nil {
			return fmt.Errorf("fingerprint %s entry %s: %w", label, relative, err)
		}
	}
	return nil
}

func validatePlanAuthorization(approved, actual string) error {
	approved = strings.ToLower(strings.TrimSpace(approved))
	if approved == "" {
		return fmt.Errorf("execution requires --approve-plan or --approve-plan-sha256")
	}
	if len(approved) != sha256.Size*2 {
		return fmt.Errorf("approved plan SHA-256 must contain 64 hexadecimal characters")
	}
	if _, err := hex.DecodeString(approved); err != nil {
		return fmt.Errorf("approved plan SHA-256 is invalid: %w", err)
	}
	if approved != actual {
		return fmt.Errorf("approved plan SHA-256 %s does not match current verified plan %s", approved, actual)
	}
	return nil
}

func requestedPlanAuthorizationError(legacyDigest, reference, directory string, targets []string, cfg resolve.ResolveConfig, result *resolve.ResolveResult, stateSHA256 string) error {
	approvedDigest, err := approvedPlanDigest(legacyDigest, reference, directory)
	if err != nil {
		return err
	}
	actualDigest := canonicalPlanSHA256(targets, cfg, result, stateSHA256)
	if err := validatePlanAuthorization(approvedDigest, actualDigest); err != nil {
		if detail := describeApprovedPlanDifference(reference, directory, cfg); detail != "" {
			return fmt.Errorf("%w; %s", err, detail)
		}
		return err
	}
	return nil
}

func approvedPlanDigest(legacyDigest, reference, directory string) (string, error) {
	legacyDigest, reference = strings.TrimSpace(legacyDigest), strings.TrimSpace(reference)
	if legacyDigest != "" && reference != "" {
		return "", fmt.Errorf("use only one of --approve-plan-sha256 and --approve-plan")
	}
	if reference == "" {
		return legacyDigest, nil
	}
	path, err := resolvePlanPath(reference, directory)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read approved plan %s: %w", path, err)
	}
	var document struct {
		Complete   bool   `json:"complete"`
		Operation  string `json:"operation"`
		PlanSHA256 string `json:"plan_sha256"`
		Resolution struct {
			Verified     bool   `json:"verified"`
			Verification string `json:"verification"`
		} `json:"resolution"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return "", fmt.Errorf("decode approved plan %s: %w", path, err)
	}
	verifiedOperation := document.Operation == "install" || document.Operation == "update" || document.Operation == "uninstall"
	if !document.Complete || (verifiedOperation && (!document.Resolution.Verified || document.Resolution.Verification != resolve.VerificationVerified)) {
		return "", fmt.Errorf("approved plan %s is not complete and verified", path)
	}
	if strings.TrimSpace(document.PlanSHA256) == "" {
		return "", fmt.Errorf("approved plan %s has no plan_sha256", path)
	}
	return document.PlanSHA256, nil
}

func describeApprovedPlanDifference(reference, directory string, current resolve.ResolveConfig) string {
	if strings.TrimSpace(reference) == "" {
		return ""
	}
	path, err := resolvePlanPath(reference, directory)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var saved struct {
		Options planOptions `json:"options"`
	}
	if json.Unmarshal(data, &saved) != nil {
		return ""
	}
	want := optionsForPlan(current)
	var differences []string
	boolOption := func(name string, before, after bool) {
		if before != after {
			differences = append(differences, fmt.Sprintf("%s (saved=%t, current=%t)", name, before, after))
		}
	}
	boolOption("update", saved.Options.Update, want.Update)
	boolOption("deep", saved.Options.Deep, want.Deep)
	boolOption("complete-graph", saved.Options.CompleteGraph, want.CompleteGraph)
	boolOption("newuse", saved.Options.NewUse, want.NewUse)
	boolOption("changed-use", saved.Options.ChangedUse, want.ChangedUse)
	boolOption("changed-deps", saved.Options.ChangedDeps, want.ChangedDeps)
	boolOption("emptytree", saved.Options.EmptyTree, want.EmptyTree)
	boolOption("reinstall", saved.Options.Reinstall, want.Reinstall)
	boolOption("onlydeps", saved.Options.OnlyDeps, want.OnlyDeps)
	boolOption("nodeps", saved.Options.NoDeps, want.NoDeps)
	boolOption("oneshot", saved.Options.Oneshot, want.Oneshot)
	if saved.Options.WithBdeps != want.WithBdeps {
		differences = append(differences, fmt.Sprintf("with-bdeps (saved=%q, current=%q)", saved.Options.WithBdeps, want.WithBdeps))
	}
	if saved.Options.RootDeps != want.RootDeps {
		differences = append(differences, fmt.Sprintf("root-deps (saved=%q, current=%q)", saved.Options.RootDeps, want.RootDeps))
	}
	if len(differences) == 0 {
		return ""
	}
	return "authorization-bound options differ: " + strings.Join(differences, ", ")
}

func resolvePlanPath(reference, directory string) (string, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return "", fmt.Errorf("plan reference is empty")
	}
	if filepath.IsAbs(reference) || strings.ContainsRune(reference, filepath.Separator) {
		return filepath.Clean(reference), nil
	}
	if reference == "." || reference == ".." || strings.ContainsAny(reference, "\\\x00") {
		return "", fmt.Errorf("invalid plan name %q", reference)
	}
	// Bare references are logical plan names, not filenames with arbitrary
	// extensions. Dots are useful in package/version-derived names, so only an
	// explicit .json suffix suppresses the default extension.
	if !strings.EqualFold(filepath.Ext(reference), ".json") {
		reference += ".json"
	}
	return filepath.Join(directory, reference), nil
}

func savePlanDocument(reference, directory string, data []byte) (string, error) {
	path, err := resolvePlanPath(reference, directory)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	temporary, err := os.CreateTemp(dir, ".plan-*")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return "", err
	}
	directoryHandle, err := os.Open(dir)
	if err != nil {
		return "", err
	}
	if err := directoryHandle.Sync(); err != nil {
		directoryHandle.Close()
		return "", err
	}
	return path, directoryHandle.Close()
}
