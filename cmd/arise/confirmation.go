package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

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

func loadResumeTargets(path string, skipFirst, readOnly bool) ([]string, error) {
	if skipFirst && !readOnly {
		if err := resolve.SkipFirstResume(path); err != nil {
			return nil, err
		}
	}
	remaining, err := resolve.LoadResume(path)
	if err != nil {
		return nil, err
	}
	if skipFirst && readOnly && len(remaining) > 0 {
		remaining = remaining[1:]
	}
	return remaining, nil
}
