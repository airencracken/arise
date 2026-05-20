package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFetchConfig_Defaults(t *testing.T) {
	cfg := FetchConfig{DistfilesDir: "/var/cache/distfiles"}
	cfg.defaults()

	if cfg.Destination != "/var/cache/distfiles" {
		t.Errorf("expected Destination to fall back to DistfilesDir, got %q", cfg.Destination)
	}
}

func TestParseArrowURI(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		wantSrc  string
		wantDest string
	}{
		{
			name:     "no arrow",
			uri:      "https://example.com/pkg-1.0.tar.gz",
			wantSrc:  "https://example.com/pkg-1.0.tar.gz",
			wantDest: "",
		},
		{
			name:     "with arrow",
			uri:      "https://example.com/pkg-1.0.tar.gz -> pkg.tar.gz",
			wantSrc:  "https://example.com/pkg-1.0.tar.gz",
			wantDest: "pkg.tar.gz",
		},
		{
			name:     "arrow with extra spaces",
			uri:      "https://example.com/pkg-1.0.tar.gz   ->   pkg.tar.gz",
			wantSrc:  "https://example.com/pkg-1.0.tar.gz",
			wantDest: "pkg.tar.gz",
		},
		{
			name:     "arrow in filename but no rename",
			uri:      "https://example.com/a->b.tar.gz",
			wantSrc:  "https://example.com/a->b.tar.gz",
			wantDest: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, dest := parseArrowURI(tt.uri)
			if src != tt.wantSrc {
				t.Errorf("src = %q, want %q", src, tt.wantSrc)
			}
			if dest != tt.wantDest {
				t.Errorf("dest = %q, want %q", dest, tt.wantDest)
			}
		})
	}
}

func TestFetch_HTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := FetchConfig{Destination: dir}

	uris := []string{srv.URL + "/test.txt"}
	paths, err := Fetch(context.Background(), uris, cfg)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	if len(paths) != 1 {
		t.Fatalf("expected 1 downloaded path, got %d", len(paths))
	}

	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("file content = %q, want %q", string(data), "hello world")
	}
}

func TestFetch_ArrowRename(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("renamed content"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := FetchConfig{Destination: dir}

	uri := srv.URL + "/pkg-1.0.tar.gz -> renamed.tar.gz"
	paths, err := Fetch(context.Background(), []string{uri}, cfg)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	if len(paths) != 1 {
		t.Fatalf("expected 1 downloaded path, got %d", len(paths))
	}

	expected := filepath.Join(dir, "renamed.tar.gz")
	if paths[0] != expected {
		t.Errorf("path = %q, want %q", paths[0], expected)
	}

	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "renamed content" {
		t.Errorf("file content = %q, want %q", string(data), "renamed content")
	}
}

func TestFetch_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.Write([]byte("too late"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := FetchConfig{Destination: dir}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Fetch(ctx, []string{srv.URL + "/test.txt"}, cfg)
	if err == nil {
		t.Error("expected error with cancelled context, got nil")
	}
}

func TestFetch_InvalidURI_HTTP404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := FetchConfig{Destination: dir}

	paths, err := Fetch(context.Background(), []string{srv.URL + "/missing.txt"}, cfg)
	if err == nil {
		t.Error("expected error for 404, got nil")
	}
	if len(paths) != 0 {
		t.Errorf("expected 0 downloaded paths, got %d", len(paths))
	}
}

func TestFetch_PartialFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "good") {
			w.Write([]byte("ok"))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := FetchConfig{Destination: dir}

	uris := []string{
		srv.URL + "/good.txt",
		srv.URL + "/bad.txt",
	}
	paths, err := Fetch(context.Background(), uris, cfg)
	if err != nil {
		t.Errorf("partial failure should not return error when at least one URI succeeds: %v", err)
	}
	if len(paths) != 1 {
		t.Errorf("expected 1 downloaded path, got %d", len(paths))
	}
}

func TestFetch_MirrorStub(t *testing.T) {
	dir := t.TempDir()
	cfg := FetchConfig{Destination: dir}

	_, err := Fetch(context.Background(), []string{"mirror://gentoo/distfiles/pkg-1.0.tar.gz"}, cfg)
	if err == nil {
		t.Error("expected error for mirror URI, got nil")
	}
	if !strings.Contains(err.Error(), "mirror expansion not yet implemented") {
		t.Errorf("expected 'mirror expansion not yet implemented', got %q", err.Error())
	}
}

func TestFetch_FTPStub(t *testing.T) {
	dir := t.TempDir()
	cfg := FetchConfig{Destination: dir}

	_, err := Fetch(context.Background(), []string{"ftp://example.com/pkg.tar.gz"}, cfg)
	if err == nil {
		t.Error("expected error for FTP URI, got nil")
	}
	if !strings.Contains(err.Error(), "FTP not yet supported") {
		t.Errorf("expected FTP not supported message, got %q", err.Error())
	}
}

func TestFetch_UnsupportedScheme(t *testing.T) {
	dir := t.TempDir()
	cfg := FetchConfig{Destination: dir}

	_, err := Fetch(context.Background(), []string{"gopher://example.com/pkg"}, cfg)
	if err == nil {
		t.Error("expected error for unsupported scheme, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported URI scheme") {
		t.Errorf("expected unsupported scheme error, got %q", err.Error())
	}
}

func TestFetch_EmptyURI(t *testing.T) {
	dir := t.TempDir()
	cfg := FetchConfig{Destination: dir}

	paths, err := Fetch(context.Background(), []string{"", "  "}, cfg)
	if err != nil {
		t.Errorf("empty URIs should be skipped silently: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("expected 0 paths for empty URIs, got %d", len(paths))
	}
}

func TestFetchFile_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.Write([]byte("slow"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := &FetchConfig{Timeout: 50 * time.Millisecond}

	err := FetchFile(context.Background(), srv.URL+"/slow.txt", filepath.Join(dir, "out.txt"), cfg)
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}

func TestFetchFile_NotAValidURL(t *testing.T) {
	err := FetchFile(context.Background(), "not-a-valid-scheme://x", "/tmp/out", nil)
	if err == nil {
		t.Error("expected error for invalid scheme in FetchFile, got nil")
	}
}

func TestFetch_MultipleURIs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.URL.Path))
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := FetchConfig{Destination: dir}

	uris := []string{
		srv.URL + "/a.txt",
		srv.URL + "/b.txt",
	}
	paths, err := Fetch(context.Background(), uris, cfg)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(paths))
	}
}

func TestFetch_DestinationFallsBackToDistfilesDir(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("content"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := FetchConfig{DistfilesDir: dir}

	paths, err := Fetch(context.Background(), []string{srv.URL + "/file.txt"}, cfg)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}

	expected := filepath.Join(dir, "file.txt")
	if paths[0] != expected {
		t.Errorf("path = %q, want %q", paths[0], expected)
	}
}

func TestFetchFile_Download(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network download test in short mode")
	}

	dir := t.TempDir()
	dest := filepath.Join(dir, "test.txt")

	err := FetchFile(context.Background(), "https://httpbin.org/get", dest, nil)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) ||
			strings.Contains(err.Error(), "no such host") ||
			strings.Contains(err.Error(), "connection refused") ||
			strings.Contains(err.Error(), "i/o timeout") {
			t.Skipf("network unavailable: %v", err)
		}
		t.Fatalf("FetchFile() error = %v", err)
	}

	if _, err := os.Stat(dest); os.IsNotExist(err) {
		t.Error("downloaded file does not exist")
	}
}
