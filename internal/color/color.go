package color

import (
	"fmt"
	"os"
	"strings"

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
	ansiReset          = "\033[0m"
	ansiBold           = "\033[1m"
	ansiRed            = "\033[31m"
	ansiGreen          = "\033[32m"
	ansiYellow         = "\033[33m"
	ansiCyan           = "\033[36m"
	ansiBlue           = "\033[34m"
	ansiMagenta        = "\033[35m"
	ansiReverse        = "\033[7m"
	ansiBlueBackground = "\033[44m"
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

func BoldGreen(s string) string        { return styled(s, ansiBold, ansiGreen) }
func BoldRed(s string) string          { return styled(s, ansiBold, ansiRed) }
func BoldYellow(s string) string       { return styled(s, ansiBold, ansiYellow) }
func BoldCyan(s string) string         { return styled(s, ansiBold, ansiCyan) }
func BoldBlue(s string) string         { return styled(s, ansiBold, ansiBlue) }
func BoldMagenta(s string) string      { return styled(s, ansiBold, ansiMagenta) }
func ReverseBoldCyan(s string) string  { return styled(s, ansiBold, ansiReverse, ansiCyan) }
func ReverseBoldBlue(s string) string  { return styled(s, ansiBold, ansiReverse, ansiBlue) }
func InstalledVersion(s string) string { return styled(s, ansiBlueBackground) }

func styled(s string, codes ...string) string {
	if s == "" || !UseColor {
		return s
	}
	return strings.Join(codes, "") + s + ansiReset
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

func Blue(s string) string { return styled(s, ansiBlue) }

func Magenta(s string) string { return styled(s, ansiMagenta) }

func Bold(s string) string {
	if s == "" {
		return s
	}
	if !UseColor {
		return s
	}
	return fmt.Sprintf("%s%s%s", ansiBold, s, ansiReset)
}
