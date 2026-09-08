package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/graph"
	"github.com/airencracken/arise/internal/ingest"
	"github.com/airencracken/arise/internal/installedquery"
	"github.com/airencracken/arise/internal/merge"
	"github.com/airencracken/arise/internal/oplock"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/preserved"
	"github.com/airencracken/arise/internal/rebuild"
	"github.com/airencracken/arise/internal/recoveryset"
	"github.com/airencracken/arise/internal/resolve"
)

func runUninstall(args []string, dbPath, repoDir string) {
	resolved, err := resolveUninstallTargets(*vdbDir, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: %v\n", err)
		os.Exit(1)
	}
	atoms, vdbPaths, err := prepareUninstallTargets(*vdbDir, resolved)
	if err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: %v\n", err)
		os.Exit(1)
	}
	db, err := ingest.OpenReadOnlyDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: open metadata: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()
	g, err := graph.BuildFromState(db, *vdbDir, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: build state graph: %v\n", err)
		os.Exit(1)
	}
	// Removal verifies the installed state that will remain. Repository
	// candidates cannot satisfy a removal unless they are also planned for
	// installation, and their unexpanded cache metadata can contaminate a
	// slotted installed CP with dependencies from a different version.
	for _, node := range g.Nodes {
		node.AvailableVersions = nil
		node.Depends = nil
		node.RevDepends = nil
	}
	portageConfig, configErr := portage.LoadEffectiveConfig(*portageConfigRoot)
	if configErr != nil {
		fmt.Fprintf(os.Stderr, "uninstall: load configuration: %v\n", configErr)
		os.Exit(1)
	}
	removals := make([]resolve.PkgAction, 0, len(atoms))
	for _, a := range atoms {
		removals = append(removals, resolve.PkgAction{Atom: a, Action: "uninstall", Domain: resolve.DomainROOT})
	}
	resolveGraph := g.ToResolveGraph()
	resolveCfg := resolve.ResolveConfig{PortageConfig: portageConfig, Backtrack: *backtrackVal}
	baseline, baselineErr := resolve.VerifyTransaction(resolveGraph, nil, nil, resolveCfg)
	result, err := resolve.VerifyTransaction(resolveGraph, nil, removals, resolveCfg)
	if baselineErr == nil {
		removePreexistingUninstallConflicts(result, baseline)
	}
	if err != nil || result == nil || !result.Verified || len(result.Conflicts) != 0 {
		var conflicts []string
		if result != nil {
			conflicts = result.Conflicts
		}
		fmt.Fprintf(os.Stderr, "uninstall: whole-state verification failed: %v conflicts=%v\n", err, conflicts)
		os.Exit(1)
	}
	stateSHA256, err := mutationStateSHA256(*vdbDir, *worldFile, *portageConfigRoot, result.Uninstall)
	if err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: fingerprint state: %v\n", err)
		os.Exit(1)
	}
	planSHA256 := canonicalPlanSHA256(args, resolve.ResolveConfig{Backtrack: *backtrackVal}, result, stateSHA256)
	if *pretend {
		if *jsonOutput || *savePlan != "" {
			var encoded bytes.Buffer
			if err := writePlanJSON(&encoded, args, resolve.ResolveConfig{Backtrack: *backtrackVal}, result, nil, planTimings{StateSHA256: stateSHA256, Operation: "uninstall"}); err != nil {
				fmt.Fprintf(os.Stderr, "uninstall: encode plan: %v\n", err)
				os.Exit(1)
			}
			if *jsonOutput {
				_, _ = os.Stdout.Write(encoded.Bytes())
			}
			if *savePlan != "" {
				path, err := savePlanDocument(*savePlan, *planDir, encoded.Bytes())
				if err != nil {
					fmt.Fprintf(os.Stderr, "uninstall: save plan: %v\n", err)
					os.Exit(1)
				}
				fmt.Fprintf(os.Stderr, "Saved plan to %s\n", path)
			}
		}
		if !*jsonOutput {
			fmt.Printf("Proposed uninstall (%d packages):\n", len(vdbPaths))
			for _, path := range vdbPaths {
				fmt.Printf("  %s\n", path)
			}
			fmt.Printf("Plan SHA-256: %s\n", planSHA256)
		}
		return
	}
	if strings.TrimSpace(*approvePlanSHA256) != "" || strings.TrimSpace(*approvePlan) != "" {
		approvedDigest, err := approvedPlanDigest(*approvePlanSHA256, *approvePlan, *planDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "uninstall: refusing mutation: %v\n", err)
			os.Exit(1)
		}
		if err := validatePlanAuthorization(approvedDigest, planSHA256); err != nil {
			fmt.Fprintf(os.Stderr, "uninstall: refusing mutation: %v\n", err)
			os.Exit(1)
		}
	}
	if *ask && !confirmUninstall(os.Stdin, os.Stdout, resolved) {
		fmt.Println("Aborted.")
		return
	}
	lock, err := oplock.TryAcquireVDB(*vdbDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: acquire operation VDB lock: %v\n", err)
		os.Exit(1)
	}
	defer lock.Release()
	locked, err := mutationStateSHA256(*vdbDir, *worldFile, *portageConfigRoot, result.Uninstall)
	if err != nil || locked != stateSHA256 {
		fmt.Fprintln(os.Stderr, "uninstall: package state or policy changed after approval")
		os.Exit(1)
	}
	recoverySetID, err := recoveryset.NewID()
	if err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: initialize recovery set: %v\n", err)
		os.Exit(1)
	}
	configurationFingerprint, err := binpkg.FingerprintConfiguration(*portageConfigRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: fingerprint Portage configuration: %v\n", err)
		os.Exit(1)
	}
	repositoryFingerprint, err := binpkg.FingerprintRepositoryIdentity(repoDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: fingerprint repository identity: %v\n", err)
		os.Exit(1)
	}
	recoveryPath, err := publishUninstallRecoverySet(context.Background(), vdbPaths, recoveryset.Request{
		Directory: filepath.Join(*binpkgDir, ".arise-recovery"),
		SetID:     recoverySetID, OperationID: recoverySetID, PlanSHA256: planSHA256,
		RootDir:                  commandEnv("ROOT", "/"),
		ConfigurationFingerprint: configurationFingerprint,
		RepositoryFingerprint:    repositoryFingerprint,
	}, recoveryset.Publish)
	if err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: publish pre-removal recovery set: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Published complete pre-removal recovery set %s\n", recoveryPath)
	for index, path := range vdbPaths {
		rebuildCfg := buildRebuildConfig(repoDir, 0, nil, nil)
		rebuildCfg.AllowLiveRoot = true
		lifecycle, err := rebuild.OpenInstalledLifecycle(path, rebuildCfg)
		if err != nil {
			if statusErr := markRecoverySetOutcome(recoveryPath, err); statusErr != nil {
				fmt.Fprintf(os.Stderr, "uninstall: recovery set remains active: %v\n", statusErr)
			}
			fmt.Fprintf(os.Stderr, "uninstall: prepare installed lifecycle for %s: %v\n", atoms[index], err)
			os.Exit(1)
		}
		closed := false
		var lifecycleErrors []error
		cfg := merge.UnmergeConfig{RootDir: commandEnv("ROOT", "/"), VDBDir: *vdbDir, PackagePath: path, JournalDir: *journalDir, AllowLiveRoot: true, VDBLockHeld: true,
			BeforeRemoval: func() error {
				if hookErr := lifecycle.Run(context.Background(), "pkg_prerm"); hookErr != nil {
					lifecycleErrors = append(lifecycleErrors, hookErr)
				}
				return nil
			},
			AfterRemoval: func() error {
				if hookErr := lifecycle.Run(context.Background(), "pkg_postrm"); hookErr != nil {
					lifecycleErrors = append(lifecycleErrors, hookErr)
				}
				return nil
			},
			AfterCommit: func() error {
				closeErr := lifecycle.Close()
				closed = true
				return errors.Join(append(lifecycleErrors, closeErr)...)
			},
		}
		if err := merge.UnmergeWithConfig(context.Background(), cfg); err != nil {
			if !closed {
				_ = lifecycle.Close()
				closed = true
			}
			var committed *merge.PostCommitError
			if errors.As(err, &committed) {
				if statusErr := markRecoverySetOutcome(recoveryPath, committed); statusErr != nil {
					fmt.Fprintf(os.Stderr, "uninstall: recovery set remains active: %v\n", statusErr)
				}
				fmt.Fprintf(os.Stderr, "Removed %s with a committed package journal, but lifecycle finalization failed: %v\n", atoms[index], committed)
				os.Exit(1)
			}
			if statusErr := markRecoverySetOutcome(recoveryPath, err); statusErr != nil {
				fmt.Fprintf(os.Stderr, "uninstall: recovery set remains active: %v\n", statusErr)
			}
			fmt.Fprintf(os.Stderr, "uninstall: after %d/%d committed removals: %v\n", index, len(vdbPaths), err)
			os.Exit(1)
		}
		fmt.Printf("Removed %s with a committed package journal.\n", atoms[index])
	}
	if err := markRecoverySetOutcome(recoveryPath, nil); err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: recovery set remains active pending explicit verification: %v\n", err)
	}
}

// Resolve against installed state only. A package selects all matching
// versions; a short name must identify one category/package unambiguously.
func resolveUninstallTargets(vdbPath string, targets []string) ([]string, error) {
	if len(targets) == 0 {
		return nil, fmt.Errorf("require at least one installed package name or atom")
	}
	var result []string
	seen := make(map[string]bool)
	for _, target := range targets {
		query := target
		short := !strings.Contains(strings.SplitN(target, ":", 2)[0], "/")
		if short {
			name := strings.TrimLeft(target, "<>=~")
			query = strings.TrimSuffix(target, name) + "arise/" + name
		}
		parsed, err := atom.Parse(query)
		if err != nil {
			return nil, fmt.Errorf("invalid package %q: %w", target, err)
		}
		if parsed.Version != nil && parsed.Op == atom.OpNone {
			parsed.Op = atom.OpEq
		}
		categories := []string{parsed.Category}
		if short {
			entries, err := os.ReadDir(vdbPath)
			if err != nil {
				return nil, fmt.Errorf("read installed packages: %w", err)
			}
			categories = nil
			for _, entry := range entries {
				if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
					categories = append(categories, entry.Name())
				}
			}
		}
		var matches, identities []string
		for _, category := range categories {
			parsed.Category = category
			found, err := installedquery.Matches(vdbPath, parsed.String(), nil)
			if err != nil {
				return nil, fmt.Errorf("match %q: %w", target, err)
			}
			if len(found) != 0 {
				identities = append(identities, parsed.CP())
				matches = append(matches, found...)
			}
		}
		if len(identities) > 1 {
			return nil, fmt.Errorf("ambiguous package %q; specify one of: %s", target, strings.Join(identities, ", "))
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no installed package matches %q", target)
		}
		for _, cpv := range matches {
			if !seen[cpv] {
				result = append(result, cpv)
				seen[cpv] = true
			}
		}
	}
	return result, nil
}

type recoverySetPublisher func(context.Context, recoveryset.Request) (string, error)

func publishUninstallRecoverySet(ctx context.Context, vdbPaths []string, request recoveryset.Request, publish recoverySetPublisher) (string, error) {
	if publish == nil {
		return "", fmt.Errorf("uninstall: recovery-set publisher is required")
	}
	request.Packages = make([]recoveryset.Package, 0, len(vdbPaths))
	for _, path := range vdbPaths {
		request.Packages = append(request.Packages, recoveryset.Package{VDBEntryPath: path})
	}
	return publish(ctx, request)
}

func validateUninstallVDB(vdbPath string) error {
	if _, err := os.Stat(filepath.Join(vdbPath, "CONTENTS")); err != nil {
		return fmt.Errorf("installed VDB entry: %w", err)
	}
	storedEbuilds, _ := filepath.Glob(filepath.Join(vdbPath, "*.ebuild"))
	if len(storedEbuilds) != 1 {
		return fmt.Errorf("expected exactly one stored ebuild")
	}
	stored, err := os.ReadFile(storedEbuilds[0])
	if err != nil {
		return fmt.Errorf("read stored ebuild: %w", err)
	}
	for _, phase := range []string{"pkg_prerm", "pkg_postrm"} {
		if removalHookDeclared(string(stored), phase) && !lifecycleNoopWithLiveRoot(string(stored), phase) {
			return fmt.Errorf("certified lane forbids custom %s", phase)
		}
	}
	return nil
}

func validateELFRemovalOrder(vdbDir string, cpvs []string) error {
	positions := make(map[string]int, len(cpvs))
	for index, cpv := range cpvs {
		if _, duplicate := positions[cpv]; duplicate {
			return fmt.Errorf("duplicate package %s", cpv)
		}
		positions[cpv] = index
	}
	for providerIndex, cpv := range cpvs {
		consumers, err := preserved.ReverseELFConsumers(vdbDir, cpv)
		if err != nil {
			return fmt.Errorf("reverse ELF verification for %s: %w", cpv, err)
		}
		for _, consumer := range consumers {
			consumerIndex, included := positions[consumer]
			if !included {
				return fmt.Errorf("%s supplies shared libraries required by installed package %s outside the removal plan", cpv, consumer)
			}
			if consumerIndex >= providerIndex {
				return fmt.Errorf("unsafe order: consumer %s must precede provider %s", consumer, cpv)
			}
		}
	}
	return nil
}

func unsupportedRemovalMessage(command string) string {
	return fmt.Sprintf("arise: %s execution is experimental and unavailable; rerun with --pretend (live removal remains gated on the P6 journal and reverse-dependency safety)", command)
}
