package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/resolve"
)

func confirmInstall(input io.Reader, output io.Writer, ask bool) bool {
	if !ask {
		return true
	}
	return confirmOperation(input, output, "Proceed with this plan? [y/N] ")
}

func confirmOperation(input io.Reader, output io.Writer, prompt string) bool {
	fmt.Fprint(output, prompt)
	scanner := bufio.NewScanner(input)
	if !scanner.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes"
}

func removeNoopResume(path string, pretend, preflight bool) {
	if !pretend && !preflight {
		_ = os.Remove(path)
	}
}

func loadResumeRequest(path string, skipFirst bool) ([]string, []string, error) {
	// Planning must preserve the checkpoint through confirmation, approval and
	// preflight failures. The executor saves the selected remaining plan only
	// when it is ready to start execution.
	remaining, selections, err := resolve.LoadResumeWithWorldSelections(path)
	if err != nil {
		return nil, nil, err
	}
	if skipFirst && len(remaining) > 0 {
		remaining = remaining[1:]
	}
	for index, target := range remaining {
		if _, err := atom.ParsePackageAtom(target); err == nil {
			continue
		}
		// Executor action atoms and older checkpoints use bare CPVs. Resolver
		// command-line requests require an explicit operator for versions.
		parsed, err := atom.Parse(target)
		if err != nil || parsed.Version == nil || parsed.Op != "" {
			return nil, nil, fmt.Errorf("resume: invalid saved package atom %q", target)
		}
		remaining[index] = "=" + target
	}
	return remaining, selections, nil
}

func finishEmptyResume(path, worldPath string, cfg resolve.ResolveConfig, preflight bool, selections []string) error {
	if *approvePlanSHA256 != "" || *approvePlan != "" {
		return fmt.Errorf("resume: completed checkpoint has no package plan to approve; use a separately reviewed world operation")
	}
	if !cfg.Pretend && !preflight {
		if !confirmInstall(os.Stdin, os.Stdout, cfg.Ask) {
			fmt.Println("Aborted.")
			return nil
		}
		if err := updateInstallWorld(worldPath, selections); err != nil {
			return fmt.Errorf("resume: update world selection: %w", err)
		}
	}
	fmt.Println("Nothing to resume.")
	removeNoopResume(path, cfg.Pretend, preflight)
	return nil
}
