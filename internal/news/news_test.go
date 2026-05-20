package news

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func writeNewsItem(t *testing.T, baseDir, name, content string) string {
	t.Helper()
	dir := filepath.Join(baseDir, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("create news dir %s: %v", name, err)
	}
	filePath := filepath.Join(dir, name+".en.txt")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("write news file %s: %v", name, err)
	}
	return filePath
}

func TestReadNewsBasic(t *testing.T) {
	dir := t.TempDir()

	content := `Title: MySQL 8.0 Default Authentication
Author: mysql-team@gentoo.org
Date: 2021-04-05
News-Item-Format: 2.0
Display-If-Installed: dev-db/mysql

Starting with the 8.0 series, MySQL changed the default authentication plugin.
Please update your configuration accordingly.
`

	writeNewsItem(t, dir, "2021-04-05-mysql-auth-change", content)

	items, err := ReadNews(dir)
	if err != nil {
		t.Fatalf("ReadNews: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("expected 1 news item, got %d", len(items))
	}

	item := items[0]
	if item.Title != "MySQL 8.0 Default Authentication" {
		t.Errorf("Title: got %q", item.Title)
	}
	if item.Author != "mysql-team@gentoo.org" {
		t.Errorf("Author: got %q", item.Author)
	}
	if item.Date != "2021-04-05" {
		t.Errorf("Date: got %q", item.Date)
	}
	if item.NewsItemFormat != "2.0" {
		t.Errorf("NewsItemFormat: got %q", item.NewsItemFormat)
	}
	if item.DisplayIfInstall != "dev-db/mysql" {
		t.Errorf("DisplayIfInstall: got %q", item.DisplayIfInstall)
	}
	if !strings.Contains(item.Body, "Starting with the 8.0 series") {
		t.Errorf("Body missing expected text: %q", item.Body)
	}
	if !strings.Contains(item.Body, "Please update your configuration") {
		t.Errorf("Body missing expected text: %q", item.Body)
	}
}

func TestReadNewsEmptyDir(t *testing.T) {
	dir := t.TempDir()
	items, err := ReadNews(dir)
	if err != nil {
		t.Fatalf("ReadNews empty dir: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

func TestReadNewsMissingDir(t *testing.T) {
	items, err := ReadNews("/nonexistent/news")
	if err != nil {
		t.Fatalf("ReadNews missing dir: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

func TestReadNewsMultipleItems(t *testing.T) {
	dir := t.TempDir()

	writeNewsItem(t, dir, "2021-01-01-first-news", `Title: First News
Author: dev1@gentoo.org
Date: 2021-01-01
News-Item-Format: 2.0

First news body.
`)

	writeNewsItem(t, dir, "2022-06-15-second-news", `Title: Second News
Author: dev2@gentoo.org
Date: 2022-06-15
News-Item-Format: 2.0

Second news body.
`)

	items, err := ReadNews(dir)
	if err != nil {
		t.Fatalf("ReadNews: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	titles := []string{items[0].Title, items[1].Title}
	sort.Strings(titles)
	if titles[0] != "First News" || titles[1] != "Second News" {
		t.Errorf("unexpected titles: %v", titles)
	}
}

func TestReadNewsSkipsNonDateDirs(t *testing.T) {
	dir := t.TempDir()

	writeNewsItem(t, dir, "2021-01-01-valid", `Title: Valid
Author: dev@gentoo.org
Date: 2021-01-01
News-Item-Format: 2.0

Body.
`)

	notNewsDir := filepath.Join(dir, "README")
	if err := os.MkdirAll(notNewsDir, 0755); err != nil {
		t.Fatalf("create README dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(notNewsDir, "README.txt"), []byte("readme content"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	items, err := ReadNews(dir)
	if err != nil {
		t.Fatalf("ReadNews: %v", err)
	}

	if len(items) != 1 {
		t.Errorf("expected 1 item (skipping non-date dir), got %d", len(items))
	}
}

func TestReadNewsMissingHeaders(t *testing.T) {
	dir := t.TempDir()

	writeNewsItem(t, dir, "2021-01-01-minimal", `Title: Just Title

Body without many headers.
`)

	items, err := ReadNews(dir)
	if err != nil {
		t.Fatalf("ReadNews: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	if items[0].Title != "Just Title" {
		t.Errorf("Title: got %q", items[0].Title)
	}
	if items[0].Author != "" {
		t.Errorf("Author: got %q, want empty", items[0].Author)
	}
	if items[0].Date != "" {
		t.Errorf("Date: got %q, want empty", items[0].Date)
	}
}

func TestReadNewsWithRevision(t *testing.T) {
	dir := t.TempDir()

	writeNewsItem(t, dir, "2021-01-01-with-rev", `Title: Has Revision
Author: dev@gentoo.org
Date: 2021-01-01
Revision: 3
News-Item-Format: 2.0

Body.
`)

	items, err := ReadNews(dir)
	if err != nil {
		t.Fatalf("ReadNews: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	if items[0].Revision != 3 {
		t.Errorf("Revision: got %d, want 3", items[0].Revision)
	}
}

func TestReadUnreadNews(t *testing.T) {
	dir := t.TempDir()
	markerDir := t.TempDir()

	writeNewsItem(t, dir, "2021-01-01-first", `Title: First
Author: dev@gentoo.org
Date: 2021-01-01
News-Item-Format: 2.0

First body.
`)

	writeNewsItem(t, dir, "2021-02-01-second", `Title: Second
Author: dev@gentoo.org
Date: 2021-02-01
News-Item-Format: 2.0

Second body.
`)

	items, err := ReadUnreadNews(dir, markerDir)
	if err != nil {
		t.Fatalf("ReadUnreadNews: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 unread items, got %d", len(items))
	}
}

func TestMarkReadAndRequery(t *testing.T) {
	dir := t.TempDir()
	markerDir := t.TempDir()

	writeNewsItem(t, dir, "2021-01-01-foo", `Title: Foo News
Author: dev@gentoo.org
Date: 2021-01-01
News-Item-Format: 2.0

Foo body.
`)

	writeNewsItem(t, dir, "2021-02-01-bar", `Title: Bar News
Author: dev@gentoo.org
Date: 2021-02-01
News-Item-Format: 2.0

Bar body.
`)

	allItems, err := ReadNews(dir)
	if err != nil {
		t.Fatalf("ReadNews: %v", err)
	}

	if len(allItems) != 2 {
		t.Fatalf("expected 2 items, got %d", len(allItems))
	}

	if err := MarkRead(markerDir, allItems[0]); err != nil {
		t.Fatalf("MarkRead first: %v", err)
	}

	unread, err := ReadUnreadNews(dir, markerDir)
	if err != nil {
		t.Fatalf("ReadUnreadNews after mark: %v", err)
	}

	if len(unread) != 1 {
		t.Fatalf("expected 1 unread after marking, got %d", len(unread))
	}
	if unread[0].Title != "Bar News" {
		t.Errorf("unread item: got %q", unread[0].Title)
	}
}

func TestMarkReadIdempotent(t *testing.T) {
	dir := t.TempDir()
	markerDir := t.TempDir()

	writeNewsItem(t, dir, "2021-01-01-test", `Title: Test
Author: dev@gentoo.org
Date: 2021-01-01
News-Item-Format: 2.0

Test body.
`)

	allItems, err := ReadNews(dir)
	if err != nil {
		t.Fatalf("ReadNews: %v", err)
	}

	if err := MarkRead(markerDir, allItems[0]); err != nil {
		t.Fatalf("MarkRead first: %v", err)
	}
	if err := MarkRead(markerDir, allItems[0]); err != nil {
		t.Fatalf("MarkRead second (idempotent): %v", err)
	}

	unread, err := ReadUnreadNews(dir, markerDir)
	if err != nil {
		t.Fatalf("ReadUnreadNews: %v", err)
	}
	if len(unread) != 0 {
		t.Errorf("expected 0 unread, got %d", len(unread))
	}
}

func TestReadNewsForPackage(t *testing.T) {
	dir := t.TempDir()

	writeNewsItem(t, dir, "2021-01-01-mysql", `Title: MySQL News
Author: dev@gentoo.org
Date: 2021-01-01
News-Item-Format: 2.0
Display-If-Installed: dev-db/mysql

MySQL body.
`)

	writeNewsItem(t, dir, "2021-02-01-general", `Title: General News
Author: dev@gentoo.org
Date: 2021-02-01
News-Item-Format: 2.0

General body.
`)

	writeNewsItem(t, dir, "2021-03-01-postgres", `Title: PostgreSQL News
Author: dev@gentoo.org
Date: 2021-03-01
News-Item-Format: 2.0
Display-If-Installed: dev-db/postgresql

PostgreSQL body.
`)

	items, err := ReadNewsForPackage(dir, "dev-db/mysql")
	if err != nil {
		t.Fatalf("ReadNewsForPackage: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items (mysql+general), got %d", len(items))
	}

	titles := []string{items[0].Title, items[1].Title}
	sort.Strings(titles)
	if titles[0] != "General News" || titles[1] != "MySQL News" {
		t.Errorf("unexpected titles: %v", titles)
	}
}

func TestReadNewsForPackageNoMatch(t *testing.T) {
	dir := t.TempDir()

	writeNewsItem(t, dir, "2021-01-01-mysql", `Title: MySQL News
Author: dev@gentoo.org
Date: 2021-01-01
News-Item-Format: 2.0
Display-If-Installed: dev-db/mysql

MySQL body.
`)

	items, err := ReadNewsForPackage(dir, "sys-apps/foo")
	if err != nil {
		t.Fatalf("ReadNewsForPackage: %v", err)
	}

	if len(items) != 0 {
		t.Errorf("expected 0 items for non-matching package, got %d", len(items))
	}
}

func TestNewsItemString(t *testing.T) {
	item := NewsItem{
		Title:    "Test News",
		Date:     "2021-01-01",
		Revision: 2,
	}

	s := item.String()
	if !strings.Contains(s, "Test News") {
		t.Errorf("String missing title: %s", s)
	}
	if !strings.Contains(s, "rev 2") {
		t.Errorf("String missing revision: %s", s)
	}
}

func TestMatchesConstraint(t *testing.T) {
	tests := []struct {
		installed  string
		constraint string
		want       bool
	}{
		{"dev-db/mysql", "dev-db/mysql", true},
		{"dev-db/mysql", "dev-db/my", true},
		{"dev-db/mysql", "dev-db/mariadb", false},
		{"dev-db/mysql", "sys-apps/foo", false},
		{"dev-db/mysql-8.0.25", "dev-db/mysql", true},
		{"", "dev-db/mysql", false},
		{"dev-db/mysql", "", false},
		{"singleword", "singleword", false},
	}

	for _, tc := range tests {
		got := matchesConstraint(tc.installed, tc.constraint)
		if got != tc.want {
			t.Errorf("matchesConstraint(%q, %q): got %v, want %v",
				tc.installed, tc.constraint, got, tc.want)
		}
	}
}

func TestReadUnreadMissingMarkerDir(t *testing.T) {
	dir := t.TempDir()

	writeNewsItem(t, dir, "2021-01-01-test", `Title: Test
Author: dev@gentoo.org
Date: 2021-01-01
News-Item-Format: 2.0

Body.
`)

	unread, err := ReadUnreadNews(dir, "/nonexistent/markers")
	if err != nil {
		t.Fatalf("ReadUnreadNews with missing marker dir: %v", err)
	}

	if len(unread) != 1 {
		t.Errorf("expected 1 unread item, got %d", len(unread))
	}
}

func TestParseNewsFileCrLfHeaders(t *testing.T) {
	dir := t.TempDir()

	content := "Title: Test\r\nAuthor: dev@gentoo.org\r\nDate: 2021-01-01\r\n\r\nBody text.\r\n"

	writeNewsItem(t, dir, "2021-01-01-crlf", content)

	items, err := ReadNews(dir)
	if err != nil {
		t.Fatalf("ReadNews with CRLF: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Title != "Test" {
		t.Errorf("Title: got %q", items[0].Title)
	}
	if items[0].Author != "dev@gentoo.org" {
		t.Errorf("Author: got %q", items[0].Author)
	}
}

func TestUnreadNewsMarkAll(t *testing.T) {
	dir := t.TempDir()
	markerDir := t.TempDir()

	for i := 0; i < 10; i++ {
		name := generateNewsDate(i + 1)
		writeNewsItem(t, dir, name, "Title: Item "+string(rune('A'+i))+"\nAuthor: dev@gentoo.org\nDate: 2021-01-"+dateDay(i+1)+"\nNews-Item-Format: 2.0\n\nBody.\n")
	}

	all, err := ReadNews(dir)
	if err != nil {
		t.Fatalf("ReadNews: %v", err)
	}

	for _, item := range all {
		if err := MarkRead(markerDir, item); err != nil {
			t.Fatalf("MarkRead: %v", err)
		}
	}

	unread, err := ReadUnreadNews(dir, markerDir)
	if err != nil {
		t.Fatalf("ReadUnreadNews: %v", err)
	}

	if len(unread) != 0 {
		t.Errorf("expected 0 unread after marking all, got %d", len(unread))
	}
}

func generateNewsDate(idx int) string {
	day := idx * 2
	if day > 28 {
		day = 28
	}
	return dateDay(day)
}

func dateDay(day int) string {
	if day < 10 {
		return "0" + string(rune('0'+day))
	}
	return string(rune('0'+day/10)) + string(rune('0'+day%10))
}

func TestReadNewsSkipsFilesNotDirs(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("not a directory"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	items, err := ReadNews(dir)
	if err != nil {
		t.Fatalf("ReadNews: %v", err)
	}

	if len(items) != 0 {
		t.Errorf("expected 0 items when only files present, got %d", len(items))
	}
}

func TestReadNewsMissingTextFile(t *testing.T) {
	dir := t.TempDir()

	newsDir := filepath.Join(dir, "2021-01-01-empty")
	if err := os.MkdirAll(newsDir, 0755); err != nil {
		t.Fatalf("create empty news dir: %v", err)
	}

	items, err := ReadNews(dir)
	if err != nil {
		t.Fatalf("ReadNews: %v", err)
	}

	if len(items) != 0 {
		t.Errorf("expected 0 items for dir without .txt file, got %d", len(items))
	}
}

func TestReadNewsBadRevision(t *testing.T) {
	dir := t.TempDir()

	writeNewsItem(t, dir, "2021-01-01-badrev", `Title: Bad Revision
Author: dev@gentoo.org
Date: 2021-01-01
Revision: not-a-number
News-Item-Format: 2.0

Body.
`)

	items, err := ReadNews(dir)
	if err != nil {
		t.Fatalf("ReadNews: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	if items[0].Revision != 0 {
		t.Errorf("Revision: got %d, want 0 (bad value default)", items[0].Revision)
	}
}

func TestParseNewsTrimsBody(t *testing.T) {
	dir := t.TempDir()

	writeNewsItem(t, dir, "2021-01-01-trim", "Title: Test\nAuthor: dev@gentoo.org\nDate: 2021-01-01\nNews-Item-Format: 2.0\n\n  Body with surrounding whitespace.  \n\n")

	items, err := ReadNews(dir)
	if err != nil {
		t.Fatalf("ReadNews: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	if items[0].Body != "Body with surrounding whitespace." {
		t.Errorf("Body: got %q", items[0].Body)
	}
}
