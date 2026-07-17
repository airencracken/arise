package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/ebuild"
	"github.com/airencracken/arise/internal/env"
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
	ws, err := world.LoadWorld(worldPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: loading world: %v\n", color.Red("deselect"), err)
		os.Exit(1)
	}

	if !ws.Contains(atomStr) {
		fmt.Printf("%s: %s is not in the world set\n", color.Yellow("deselect"), atomStr)
		return
	}

	ws.Deselect(atomStr)

	if err := ws.Save(worldPath); err != nil {
		fmt.Fprintf(os.Stderr, "%s: saving world: %v\n", color.Red("deselect"), err)
		os.Exit(1)
	}

	fmt.Printf("%s: removed %s from world set\n", color.Green("deselect"), color.Bold(atomStr))
}

func runInfo() {
	fmt.Println("arise 0.1.1")
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
