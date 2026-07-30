package diagnostic

import (
	"bytes"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/color"
)

func TestRenderProducesCompilerStyleCaretSpan(t *testing.T) {
	previous := color.UseColor
	color.UseColor = false
	t.Cleanup(func() { color.UseColor = previous })
	var output bytes.Buffer
	Render(&output, SourceSpan{
		Summary: "candidate is blocked",
		Source:  "<dev-python/docutils-0.23[python_targets_python3_14(-)]",
		Start:   26, End: 55, Annotation: "required by dev-python/sphinx",
	})
	got := output.String()
	if !strings.Contains(got, "  candidate is blocked\n") ||
		!strings.Contains(got, "\n    <dev-python/docutils") ||
		!strings.Contains(got, strings.Repeat("^", 29)+" required by dev-python/sphinx") {
		t.Fatalf("diagnostic = %q", got)
	}
}

func TestRenderColorizesTheSourceSpanAndCarets(t *testing.T) {
	previous := color.UseColor
	color.UseColor = true
	t.Cleanup(func() { color.UseColor = previous })
	var output bytes.Buffer
	Render(&output, SourceSpan{Source: "<cat/pkg-2[flag]", Start: 0, End: 17})
	got := output.String()
	if strings.Count(got, "\x1b[") < 4 {
		t.Fatalf("source and carets were not both colorized: %q", got)
	}
	if !strings.Contains(got, color.PortageYellow("<cat/pkg-2[flag]")) {
		t.Fatalf("source constraint was not colorized as one span: %q", got)
	}
}

func TestRenderBoundsAdversarialSpansAndControlCharacters(t *testing.T) {
	previous := color.UseColor
	color.UseColor = false
	t.Cleanup(func() { color.UseColor = previous })
	for _, span := range []SourceSpan{
		{Summary: "bad\nsummary", Source: "abc\ninjected", Start: -100, End: 1000},
		{Summary: "bad", Source: "", Start: 50, End: -2},
		{Summary: "bad", Source: "abc", Start: 2, End: 1, Annotation: "x\ty"},
	} {
		var output bytes.Buffer
		Render(&output, span)
		if strings.Count(output.String(), "\n") != 3 {
			t.Fatalf("unexpected line count for %#v: %q", span, output.String())
		}
	}
}

func FuzzRenderNeverPanicsOrEmitsInjectedLines(f *testing.F) {
	f.Add("summary", "source", 0, 3, "annotation")
	f.Add("bad\nsummary", "x\r\ny", -100, 1000, "note\tvalue")
	f.Fuzz(func(t *testing.T, summary, source string, start, end int, annotation string) {
		var output bytes.Buffer
		Render(&output, SourceSpan{
			Summary: summary, Source: source, Start: start, End: end, Annotation: annotation,
		})
		if strings.Count(output.String(), "\n") > 3 {
			t.Fatalf("diagnostic emitted injected lines: %q", output.String())
		}
	})
}
