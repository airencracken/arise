package color

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

var UseColor = true

func init() {
	if os.Getenv("NO_COLOR") != "" {
		UseColor = false
	}
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		UseColor = false
	}
}

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiCyan   = "\033[36m"
)

func Green(s string) string {
	if s == "" {
		return s
	}
	if !UseColor {
		return s
	}
	return fmt.Sprintf("%s%s%s", ansiGreen, s, ansiReset)
}

func Red(s string) string {
	if s == "" {
		return s
	}
	if !UseColor {
		return s
	}
	return fmt.Sprintf("%s%s%s", ansiRed, s, ansiReset)
}

func Yellow(s string) string {
	if s == "" {
		return s
	}
	if !UseColor {
		return s
	}
	return fmt.Sprintf("%s%s%s", ansiYellow, s, ansiReset)
}

func Cyan(s string) string {
	if s == "" {
		return s
	}
	if !UseColor {
		return s
	}
	return fmt.Sprintf("%s%s%s", ansiCyan, s, ansiReset)
}

func Bold(s string) string {
	if s == "" {
		return s
	}
	if !UseColor {
		return s
	}
	return fmt.Sprintf("%s%s%s", ansiBold, s, ansiReset)
}
