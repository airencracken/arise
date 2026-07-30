package color

import (
	"strings"
	"testing"
)

func TestPaletteReturnsIndependentStableSemanticMap(t *testing.T) {
	first := Palette()
	if first["bad"] != "\033[91m" || first["normal"] != "\033[0m" || len(first) != 10 {
		t.Fatalf("Palette = %#v", first)
	}
	first["bad"] = "changed"
	if Palette()["bad"] != "\033[91m" {
		t.Fatal("Palette returned shared mutable state")
	}
}

func TestGreen(t *testing.T) {
	UseColor = true
	got := Green("ok")
	if !strings.HasPrefix(got, "\033[32m") || !strings.HasSuffix(got, "\033[0m") {
		t.Errorf("Green(ok) = %q, expected ANSI green wrapper", got)
	}
	if !strings.Contains(got, "ok") {
		t.Errorf("Green(ok) should contain original text, got %q", got)
	}
}

func TestRed(t *testing.T) {
	UseColor = true
	got := Red("err")
	if !strings.HasPrefix(got, "\033[31m") || !strings.HasSuffix(got, "\033[0m") {
		t.Errorf("Red(err) = %q, expected ANSI red wrapper", got)
	}
}

func TestYellow(t *testing.T) {
	UseColor = true
	got := Yellow("warn")
	if !strings.HasPrefix(got, "\033[33m") || !strings.HasSuffix(got, "\033[0m") {
		t.Errorf("Yellow(warn) = %q, expected ANSI yellow wrapper", got)
	}
}

func TestCyan(t *testing.T) {
	UseColor = true
	got := Cyan("info")
	if !strings.HasPrefix(got, "\033[36m") || !strings.HasSuffix(got, "\033[0m") {
		t.Errorf("Cyan(info) = %q, expected ANSI cyan wrapper", got)
	}
}

func TestBold(t *testing.T) {
	UseColor = true
	got := Bold("bold")
	if !strings.HasPrefix(got, "\033[1m") || !strings.HasSuffix(got, "\033[0m") {
		t.Errorf("Bold(bold) = %q, expected ANSI bold wrapper", got)
	}
}

func TestColorDisabled(t *testing.T) {
	UseColor = false
	got := Green("ok")
	if got != "ok" {
		t.Errorf("Green(ok) with color disabled = %q, want plain text", got)
	}
	got = Red("err")
	if got != "err" {
		t.Errorf("Red(err) with color disabled = %q, want plain text", got)
	}
	got = Yellow("warn")
	if got != "warn" {
		t.Errorf("Yellow(warn) with color disabled = %q, want plain text", got)
	}
	got = Cyan("info")
	if got != "info" {
		t.Errorf("Cyan(info) with color disabled = %q, want plain text", got)
	}
	got = Bold("bold")
	if got != "bold" {
		t.Errorf("Bold(bold) with color disabled = %q, want plain text", got)
	}
}

func TestEmptyString(t *testing.T) {
	UseColor = true
	for _, fn := range []func(string) string{Green, Red, Yellow, Cyan, Bold} {
		got := fn("")
		if got != "" {
			t.Errorf("colored empty string = %q, want empty", got)
		}
	}
}

func TestColorEncodesProperly(t *testing.T) {
	UseColor = true
	got := Green("hello")
	if strings.Contains(got, "\033[32m") && strings.Contains(got, "\033[0m") {
		idxGreen := strings.Index(got, "\033[32m")
		idxText := strings.Index(got, "hello")
		idxReset := strings.Index(got, "\033[0m")
		if idxGreen >= idxText || idxText >= idxReset {
			t.Errorf("color code ordering wrong: %q", got)
		}
	}
}

func TestPortageSemanticPaletteMatchesNamedANSIColors(t *testing.T) {
	previous := UseColor
	UseColor = true
	t.Cleanup(func() { UseColor = previous })
	tests := []struct {
		got  string
		code string
	}{
		{PortageRed("x"), "\x1b[91m"},
		{PortageGreen("x"), "\x1b[92m"},
		{PortageYellow("x"), "\x1b[93m"},
		{PortageBlue("x"), "\x1b[94m"},
		{PortageFuchsia("x"), "\x1b[95m"},
		{PortageTurquoise("x"), "\x1b[96m"},
		{PortageDarkGreen("x"), "\x1b[32m"},
		{PortagePurple("x"), "\x1b[35m"},
		{PortageTeal("x"), "\x1b[36m"},
	}
	for _, test := range tests {
		if test.got != test.code+"x\x1b[0m" {
			t.Errorf("style = %q, want prefix %q", test.got, test.code)
		}
	}
}
