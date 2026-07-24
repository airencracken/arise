package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/ebuild"
	"github.com/airencracken/arise/internal/env"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/oplock"
	"github.com/airencracken/arise/internal/portage"
	"github.com/airencracken/arise/internal/world"
)

func runDispatchConf() {
	etcDir := "/etc"
	entries, err := os.ReadDir(etcDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dispatch-conf: reading %s: %v\n", etcDir, err)
		os.Exit(1)
	}

	var pending []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "._cfg") {
			pending = append(pending, filepath.Join(etcDir, name))
		}
	}

	if len(pending) == 0 {
		fmt.Println("No pending config file updates.")
		return
	}

	fmt.Printf("Found %d pending config file updates:\n", len(pending))
	for _, p := range pending {
		base := strings.TrimPrefix(filepath.Base(p), "._cfg0000_")
		if base == filepath.Base(p) {
			base = strings.TrimPrefix(base, "._cfg")
			base = strings.TrimLeft(base, "0123456789_")
		}
		fmt.Printf("  %s -> %s\n", p, filepath.Join(etcDir, base))
	}
}

func runEnvUpdate() {
	envDir := "/etc/env.d"
	outputDir := "/etc"

	if err := env.UpdateEnv(envDir, outputDir); err != nil {
		fmt.Fprintf(os.Stderr, "env-update: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("env-update: regenerated /etc/profile.env and /etc/csh.env")
}

func runLdConfig() {
	fmt.Println("Running ldconfig...")
	if err := env.RunLdConfig("/"); err != nil {
		fmt.Fprintf(os.Stderr, "ldconfig: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("ldconfig complete.")
}

func runConfig(args []string, repoDir string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "config: missing package atom argument\n")
		os.Exit(1)
	}

	a, err := atom.Parse(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: parsing atom %q: %v\n", args[0], err)
		os.Exit(1)
	}

	fmt.Printf("pkg_config for %s\n", a.String())
	if a.Version == nil || a.Version.Raw == "" {
		fmt.Fprintf(os.Stderr, "config: atom must include a version\n")
		os.Exit(1)
	}

	ebuildFile := filepath.Join(repoDir, a.Category, a.Package, a.Package+"-"+a.Version.Raw+".ebuild")
	if _, statErr := os.Stat(ebuildFile); os.IsNotExist(statErr) {
		fmt.Fprintf(os.Stderr, "config: ebuild not found: %s\n", ebuildFile)
		os.Exit(1)
	}

	eb, err := ebuild.ParseEbuild(ebuildFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: parse ebuild: %v\n", err)
		os.Exit(1)
	}

	if _, ok := eb.RawPhases["pkg_config"]; !ok {
		fmt.Printf("No pkg_config phase defined for %s\n", a.String())
		return
	}

	fmt.Println("pkg_config phase found; native execution pending")
}

func runDeselect(atomStr string) {
	worldPath := *worldFile
	current, err := world.LoadWorld(worldPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "deselect: read world: %v\n", err)
		os.Exit(1)
	}
	if !current.Contains(atomStr) {
		fmt.Printf("%s: %s is not in the world set\n", color.Yellow("deselect"), atomStr)
		return
	}
	stateSHA256, err := worldStateSHA256(worldPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "deselect: fingerprint world: %v\n", err)
		os.Exit(1)
	}
	planSHA256 := worldMutationPlanSHA256("deselect", atomStr, stateSHA256)
	if *pretend {
		document := map[string]any{"schema": 1, "operation": "deselect", "atom": atomStr, "complete": true, "state_sha256": stateSHA256, "plan_sha256": planSHA256}
		if err := emitSavableMutationPlan("deselect", document); err != nil {
			fmt.Fprintf(os.Stderr, "deselect: %v\n", err)
			os.Exit(1)
		}
		if !*jsonOutput {
			fmt.Printf("Would remove %s from world set.\nPlan SHA-256: %s\n", atomStr, planSHA256)
		}
		return
	}
	if !*experimentalLiveMutation || (strings.TrimSpace(*approvePlanSHA256) == "" && strings.TrimSpace(*approvePlan) == "") {
		fmt.Fprintln(os.Stderr, "deselect: refusing mutation: require --experimental-live-mutation and --approve-plan or --approve-plan-sha256")
		os.Exit(1)
	}
	approvedDigest, err := approvedPlanDigest(*approvePlanSHA256, *approvePlan, *planDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "deselect: refusing mutation: %v\n", err)
		os.Exit(1)
	}
	if err := validatePlanAuthorization(*experimentalLiveMutation, approvedDigest, planSHA256); err != nil {
		fmt.Fprintf(os.Stderr, "deselect: refusing mutation: %v\n", err)
		os.Exit(1)
	}
	found := false
	err = world.Update(worldPath, func(ws *world.WorldSet) error {
		lockedStateSHA256, err := worldStateSHA256(worldPath)
		if err != nil {
			return err
		}
		if lockedStateSHA256 != stateSHA256 {
			return fmt.Errorf("world changed after approval; generate a new plan")
		}
		if !ws.Contains(atomStr) {
			return nil
		}
		found = true
		ws.Deselect(atomStr)
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: updating world: %v\n", color.Red("deselect"), err)
		os.Exit(1)
	}
	if !found {
		fmt.Printf("%s: %s is not in the world set\n", color.Yellow("deselect"), atomStr)
		return
	}

	fmt.Printf("%s: removed %s from world set\n", color.Green("deselect"), color.Bold(atomStr))
}

// runSelect implements the state-only equivalent of emerge --noreplace for an
// already installed package: no build or merge occurs, and only world changes.
func runSelect(atomStr string) {
	a, err := atom.Parse(atomStr)
	if err != nil || a.Category == "" || a.Package == "" {
		fmt.Fprintf(os.Stderr, "select: invalid package atom %q\n", atomStr)
		os.Exit(1)
	}
	worldAtom := a.CP()
	installed, err := installedVDBForCP(*vdbDir, a.Category, a.Package)
	if err != nil || len(installed) == 0 {
		fmt.Fprintf(os.Stderr, "select: %s is not installed\n", worldAtom)
		os.Exit(1)
	}
	current, err := world.LoadWorld(*worldFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "select: read world: %v\n", err)
		os.Exit(1)
	}
	if current.Contains(worldAtom) {
		fmt.Printf("select: %s is already in the world set\n", worldAtom)
		return
	}
	stateSHA256, err := mutationStateSHA256(*vdbDir, *worldFile, *portageConfigRoot, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "select: fingerprint state: %v\n", err)
		os.Exit(1)
	}
	planSHA256 := worldMutationPlanSHA256("select", worldAtom, stateSHA256)
	if *pretend {
		document := map[string]any{"schema": 1, "operation": "select", "atom": worldAtom, "complete": true, "installed_vdb": installed, "state_sha256": stateSHA256, "plan_sha256": planSHA256}
		if err := emitSavableMutationPlan("select", document); err != nil {
			fmt.Fprintf(os.Stderr, "select: %v\n", err)
			os.Exit(1)
		}
		if !*jsonOutput {
			fmt.Printf("Would add %s to world without rebuilding it.\nPlan SHA-256: %s\n", worldAtom, planSHA256)
		}
		return
	}
	if !*experimentalLiveMutation || (strings.TrimSpace(*approvePlanSHA256) == "" && strings.TrimSpace(*approvePlan) == "") {
		fmt.Fprintln(os.Stderr, "select: refusing mutation: require --experimental-live-mutation and --approve-plan or --approve-plan-sha256")
		os.Exit(1)
	}
	approvedDigest, err := approvedPlanDigest(*approvePlanSHA256, *approvePlan, *planDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "select: refusing mutation: %v\n", err)
		os.Exit(1)
	}
	if err := validatePlanAuthorization(true, approvedDigest, planSHA256); err != nil {
		fmt.Fprintf(os.Stderr, "select: refusing mutation: %v\n", err)
		os.Exit(1)
	}
	vdbLock, err := oplock.TryAcquireVDB(*vdbDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "select: acquire VDB lock: %v\n", err)
		os.Exit(1)
	}
	defer vdbLock.Release()
	err = world.Update(*worldFile, func(ws *world.WorldSet) error {
		lockedState, err := mutationStateSHA256(*vdbDir, *worldFile, *portageConfigRoot, nil)
		if err != nil {
			return err
		}
		if lockedState != stateSHA256 {
			return fmt.Errorf("package/world state changed after approval; generate a new plan")
		}
		world.Add(ws, worldAtom)
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "select: update world: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("select: added %s to world without rebuilding it\n", worldAtom)
}

func emitSavableMutationPlan(command string, document any) error {
	if !*jsonOutput && strings.TrimSpace(*savePlan) == "" {
		return nil
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return fmt.Errorf("encode plan: %w", err)
	}
	if *jsonOutput {
		if _, err := os.Stdout.Write(encoded.Bytes()); err != nil {
			return fmt.Errorf("write plan: %w", err)
		}
	}
	if strings.TrimSpace(*savePlan) != "" {
		path, err := savePlanDocument(*savePlan, *planDir, encoded.Bytes())
		if err != nil {
			return fmt.Errorf("save plan: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Saved %s plan to %s\n", command, path)
	}
	return nil
}

// installedVDBForCP returns only VDB entries whose parsed category/package is
// an exact match. A glob such as "bind-*" also matches "bind-tools-*", which
// is unsafe when deciding whether a package may be selected into world.
func installedVDBForCP(vdbDir, category, packageName string) ([]string, error) {
	categoryDir := filepath.Join(vdbDir, category)
	entries, err := os.ReadDir(categoryDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var matches []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		parsedCategory, parsedPackage, version, err := metadata.ParseCPV(category + "/" + entry.Name())
		if err != nil || version == "" || parsedCategory != category || parsedPackage != packageName {
			continue
		}
		matches = append(matches, filepath.Join(categoryDir, entry.Name()))
	}
	sort.Strings(matches)
	return matches, nil
}

func runInfo() {
	fmt.Println("arise 0.0.1")
	fmt.Printf("Go version: %s\n", runtime.Version())
	fmt.Printf("OS: %s\n", runtime.GOOS)
	fmt.Printf("Arch: %s\n", runtime.GOARCH)
	fmt.Println()

	cfg, err := portage.LoadEffectiveConfig(*portageConfigRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "info: loading portage config: %v\n", err)
	}

	if cfg != nil {
		if cfg.CFLAGS != "" {
			fmt.Printf("CFLAGS: %s\n", cfg.CFLAGS)
		}
		if cfg.CXXFLAGS != "" {
			fmt.Printf("CXXFLAGS: %s\n", cfg.CXXFLAGS)
		}
		if cfg.MAKEOPTS != "" {
			fmt.Printf("MAKEOPTS: %s\n", cfg.MAKEOPTS)
		}
		if len(cfg.ACCEPT_KEYWORDS) > 0 {
			fmt.Printf("ACCEPT_KEYWORDS: %s\n", strings.Join(cfg.ACCEPT_KEYWORDS, " "))
		}
		if len(cfg.FEATURES) > 0 {
			fmt.Printf("FEATURES: %s\n", strings.Join(cfg.FEATURES, " "))
		}
		if val, ok := cfg.MakeConf["CHOST"]; ok && val != "" {
			fmt.Printf("CHOST: %s\n", val)
		}
		if len(cfg.USE) > 0 {
			displayUse := cfg.USE
			if len(displayUse) > 50 && !*verbose {
				displayUse = displayUse[:50]
				fmt.Printf("USE: %s ... (%d total)\n", strings.Join(displayUse, " "), len(cfg.USE))
			} else {
				fmt.Printf("USE: %s\n", strings.Join(displayUse, " "))
			}
		}
		if *verbose && len(cfg.ProfileParents) > 0 {
			fmt.Printf("Profile stack: %s\n", strings.Join(cfg.ProfileParents, " "))
		}
	}

	fmt.Println()

	profilePath := filepath.Join(*portageConfigRoot, "make.profile")
	if target, err := os.Readlink(profilePath); err == nil {
		fmt.Printf("Profile: %s\n", target)
	} else {
		fmt.Printf("Profile: %s (unable to read link: %v)\n", profilePath, err)
	}

	repoPath := "/var/db/repos/gentoo"
	if info, err := os.Stat(repoPath); err == nil {
		fmt.Printf("Repository: %s (present, last modified: %s)\n", repoPath, info.ModTime().Format("Mon Jan 2 15:04:05 MST 2006"))
	} else {
		fmt.Printf("Repository: %s (not present)\n", repoPath)
	}
}
