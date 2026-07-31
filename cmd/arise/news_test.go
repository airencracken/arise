package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/news"
)

func TestWriteNewsListNumbersItemsAndShowsUnreadState(t *testing.T) {
	items := []news.NewsItem{
		{Path: "/news/2026-01-01-first", Date: "2026-01-01", Title: "First", Revision: 2},
		{Path: "/news/2026-02-01-second", Date: "2026-02-01", Title: "Second", Revision: 1},
	}
	var output bytes.Buffer
	writeNewsList(&output, items, items[1:])
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "1 [ ] 2026-01-01: First (rev 2)") ||
		!strings.Contains(lines[1], "2 [N] 2026-02-01: Second (rev 1)") {
		t.Fatalf("news list = %q", output.String())
	}
}

func TestWriteNewsListUsesSemanticColor(t *testing.T) {
	previous := color.UseColor
	color.UseColor = true
	t.Cleanup(func() { color.UseColor = previous })
	item := news.NewsItem{Path: "/news/item", Date: "2026-01-01", Title: "Important", Revision: 1}
	var output bytes.Buffer
	writeNewsList(&output, []news.NewsItem{item}, []news.NewsItem{item})
	for _, sequence := range []string{"\x1b[36m", "\x1b[1m\x1b[33m", "\x1b[1mImportant"} {
		if !strings.Contains(output.String(), sequence) {
			t.Fatalf("color sequence %q missing from %q", sequence, output.String())
		}
	}
}

func TestSelectNewsItemByNumberPathAndBasename(t *testing.T) {
	items := []news.NewsItem{
		{Path: "/news/2026-01-01-first", Title: "First"},
		{Path: "/news/2026-02-01-second", Title: "Second"},
	}
	for _, test := range []struct {
		specifier string
		title     string
	}{
		{"2", "Second"},
		{items[0].Path, "First"},
		{filepath.Base(items[1].Path), "Second"},
	} {
		item, err := selectNewsItem(items, test.specifier)
		if err != nil {
			t.Fatal(err)
		}
		if item.Title != test.title {
			t.Fatalf("select %q = %q", test.specifier, item.Title)
		}
	}
}

func TestSelectNewsItemRejectsAdversarialSpecifiers(t *testing.T) {
	items := []news.NewsItem{{Path: "/news/one", Title: "One"}}
	for _, specifier := range []string{"0", "-1", "2", "../one", "", "1\n2"} {
		if _, err := selectNewsItem(items, specifier); err == nil {
			t.Fatalf("selectNewsItem(%q) succeeded", specifier)
		}
	}
}
