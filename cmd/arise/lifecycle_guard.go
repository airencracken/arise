package main

import (
	"regexp"
	"strings"
)

var removalHookName = regexp.MustCompile(`\bpkg_(prerm|postrm)\b`)
var liveRootEmptyGuard = regexp.MustCompile(`^if \[\[ -z \$\{ROOT\}( && -[efd] \$\{EPREFIX\}/[A-Za-z0-9_./+-]+)? \]\] ?; then$`)
var guardedSimpleCommand = regexp.MustCompile(`^[A-Za-z0-9_./+-]+(?:[ \t]+[A-Za-z0-9_./+-]+)*$`)

// Only a small, structural subset is certified as unreachable on ROOT=/.
// Reject unfamiliar shell syntax rather than trying to interpret Bash here.
func lifecycleNoopWithLiveRoot(ebuild, phase string) bool {
	if removalHookCount(ebuild, phase) != 1 {
		return false
	}
	lines := strings.Split(ebuild, "\n")
	start := -1
	for index, line := range lines {
		if strings.TrimSpace(line) == phase+"() {" {
			if start != -1 {
				return false
			}
			start = index + 1
		}
	}
	if start < 0 {
		return false
	}
	guarded, guards := false, 0
	for _, line := range lines[start:] {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch line {
		case "}":
			return guards > 0 && !guarded
		case "fi":
			if !guarded {
				return false
			}
			guarded = false
		default:
			if liveRootEmptyGuard.MatchString(line) {
				if guarded {
					return false
				}
				guarded = true
				guards++
				continue
			}
			if !guarded || !guardedSimpleCommand.MatchString(line) {
				return false
			}
			switch strings.Fields(line)[0] {
			case "if", "then", "else", "elif", "fi", "case", "esac", "for", "while", "until", "do", "done", "function":
				return false
			}
		}
	}
	return false
}

func removalHookDeclared(ebuild, phase string) bool { return removalHookCount(ebuild, phase) != 0 }

func removalHookCount(ebuild, phase string) int {
	count := 0
	for _, line := range strings.Split(ebuild, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, name := range removalHookName.FindAllString(line, -1) {
			if name == phase {
				count++
			}
		}
	}
	return count
}
