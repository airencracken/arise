// Package diagnostic renders compiler-style, source-span diagnostics.
package diagnostic

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/airencracken/arise/internal/color"
)

type SourceSpan struct {
	Summary    string
	Source     string
	Start      int
	End        int
	Annotation string
}

func Render(writer io.Writer, span SourceSpan) {
	source := singleLine(span.Source)
	start, end := boundedSpan(source, span.Start, span.End)
	if summary := singleLine(span.Summary); summary != "" {
		fmt.Fprintf(writer, "  %s\n", summary)
	}
	fmt.Fprintf(writer, "    %s%s%s\n", source[:start], color.PortageYellow(source[start:end]), source[end:])
	carets := color.PortageYellow(strings.Repeat("^", max(1, end-start)))
	fmt.Fprintf(writer, "    %s%s", strings.Repeat(" ", start), carets)
	if annotation := singleLine(span.Annotation); annotation != "" {
		fmt.Fprintf(writer, " %s", annotation)
	}
	fmt.Fprintln(writer)
}

func boundedSpan(source string, start, end int) (int, int) {
	if start < 0 {
		start = 0
	}
	if start > len(source) {
		start = len(source)
	}
	if end <= start {
		end = start + 1
	}
	if end > len(source) {
		end = len(source)
	}
	if end < start {
		end = start
	}
	return start, end
}

func singleLine(value string) string {
	return strings.Map(func(character rune) rune {
		if character == '\t' {
			return ' '
		}
		if unicode.IsControl(character) {
			return ' '
		}
		return character
	}, value)
}
