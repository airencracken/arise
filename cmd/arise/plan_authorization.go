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
	Update, Deep, CompleteGraph, NewUse, ChangedUse, ChangedDeps bool
	EmptyTree, Reinstall, OnlyDeps, NoDeps, Oneshot              bool
	WithBdeps, RootDeps                                          string
	Backtrack                                                    int
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
		Options: planOptions{
			Update: cfg.Update, Deep: cfg.Deep, CompleteGraph: cfg.CompleteGraph,
			NewUse: cfg.NewUse, ChangedUse: cfg.ChangedUse, ChangedDeps: cfg.ChangedDeps,
			EmptyTree: cfg.EmptyTree, Reinstall: cfg.Reinstall, OnlyDeps: cfg.OnlyDeps,
			NoDeps: cfg.NoDeps, Oneshot: cfg.Oneshot, WithBdeps: cfg.WithBdeps,
			RootDeps: cfg.RootDeps, Backtrack: cfg.Backtrack,
		},
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

func validatePlanAuthorization(experimental bool, approved, actual string) error {
	approved = strings.ToLower(strings.TrimSpace(approved))
	if !experimental && approved == "" {
		return nil
	}
	if !experimental {
		return fmt.Errorf("--approve-plan-sha256 requires --experimental-live-mutation")
	}
	if approved == "" {
		return fmt.Errorf("--experimental-live-mutation requires --approve-plan-sha256")
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
