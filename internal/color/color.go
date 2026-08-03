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
	ansiBlack          = "\033[30m"
	ansiRed            = "\033[31m"
	ansiGreen          = "\033[32m"
	ansiYellow         = "\033[33m"
	ansiCyan           = "\033[36m"
	ansiBlue           = "\033[34m"
	ansiMagenta        = "\033[35m"
	ansiReverse        = "\033[7m"
	ansiBlueBackground = "\033[44m"
	ansiBrightRed      = "\033[91m"
	ansiBrightGreen    = "\033[92m"
	ansiBrightYellow   = "\033[93m"
	ansiBrightBlue     = "\033[94m"
	ansiBrightMagenta  = "\033[95m"
	ansiBrightCyan     = "\033[96m"
)

// Palette returns Arise's stable semantic ANSI palette. Callers can build
// alternate frontends without scraping terminal output.
func Palette() map[string]string {
	return map[string]string{
		"bad": ansiBrightRed, "bracket": ansiBrightBlue, "error": ansiBrightRed,
		"good": ansiBrightGreen, "highlight": ansiCyan, "info": ansiGreen,
		"log": ansiBrightGreen, "normal": ansiReset, "qa-warning": ansiYellow,
		"warning": ansiBrightYellow,
	}
}

func Green(s string) string {
	if s == "" {
		return s
	}
	if !UseColor {
		return s
	}
	return fmt.Sprintf("%s%s%s", ansiGreen, s, ansiReset)
}

func BoldGreen(s string) string       { return styled(s, ansiBold, ansiGreen) }
func BoldRed(s string) string         { return styled(s, ansiBold, ansiRed) }
func BoldYellow(s string) string      { return styled(s, ansiBold, ansiYellow) }
func BoldCyan(s string) string        { return styled(s, ansiBold, ansiCyan) }
func BoldBlue(s string) string        { return styled(s, ansiBold, ansiBlue) }
func BoldMagenta(s string) string     { return styled(s, ansiBold, ansiMagenta) }
func ReverseBoldCyan(s string) string { return styled(s, ansiBold, ansiReverse, ansiCyan) }
func ReverseBoldBlue(s string) string { return styled(s, ansiBold, ansiReverse, ansiBlue) }
func InstalledMarker(s string) string { return styled(s, ansiBold, ansiReverse, ansiGreen) }
func InstalledVersion(s string) string {
	return styled(s, ansiBlack, ansiBlueBackground)
}

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

// Portage's named palette distinguishes the normal ANSI colors from their
// bright variants. Keep these explicit so emerge-compatible output does not
// depend on terminal bold semantics.
func PortageRed(s string) string       { return styled(s, ansiBrightRed) }
func PortageGreen(s string) string     { return styled(s, ansiBrightGreen) }
func PortageYellow(s string) string    { return styled(s, ansiBrightYellow) }
func PortageBlue(s string) string      { return styled(s, ansiBrightBlue) }
func PortageFuchsia(s string) string   { return styled(s, ansiBrightMagenta) }
func PortageTurquoise(s string) string { return styled(s, ansiBrightCyan) }
func PortageDarkGreen(s string) string { return styled(s, ansiGreen) }
func PortagePurple(s string) string    { return styled(s, ansiMagenta) }
func PortageTeal(s string) string      { return styled(s, ansiCyan) }

func Bold(s string) string {
	if s == "" {
		return s
	}
	if !UseColor {
		return s
	}
	return fmt.Sprintf("%s%s%s", ansiBold, s, ansiReset)
}
