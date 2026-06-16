package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type FetchConfig struct {
	Destination  string
	DistfilesDir string
	Timeout      time.Duration
}

func (c *FetchConfig) defaults() {
	if c.Destination == "" {
		c.Destination = c.DistfilesDir
	}
	if c.Timeout == 0 {
		c.Timeout = 120 * time.Second
	}
}

// parseArrowURI splits a URI with an optional " -> destname" suffix.
// Returns "https://example.com/pkg.tar.gz", "renamed.tar.gz".
func parseArrowURI(uri string) (src, dest string) {
	const arrow = " -> "
	if idx := strings.Index(uri, arrow); idx >= 0 {
		src = strings.TrimSpace(uri[:idx])
		dest = strings.TrimSpace(uri[idx+len(arrow):])
	} else {
		src = uri
		dest = ""
	}
	return src, dest
}

// Fetch downloads a list of source URIs into cfg.Destination.
// Returns paths of successfully downloaded files.
func Fetch(ctx context.Context, uris []string, cfg FetchConfig) ([]string, error) {
	cfg.defaults()

	if cfg.Destination == "" {
		return nil, fmt.Errorf("fetch: a download location must be specified")
	}

	if err := os.MkdirAll(cfg.Destination, 0755); err != nil {
		return nil, fmt.Errorf("fetch: could not create download directory: %w", err)
	}

	var firstErr error
	var paths []string

	for _, rawURI := range uris {
		rawURI = strings.TrimSpace(rawURI)
		if rawURI == "" {
			continue
		}

		srcURI, renameDest := parseArrowURI(rawURI)

		u, err := url.Parse(srcURI)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("fetch: invalid download address %q: %w", srcURI, err)
			}
			continue
		}

		var destPath string
		if renameDest != "" {
			destPath = filepath.Join(cfg.Destination, renameDest)
		} else {
			destPath = filepath.Join(cfg.Destination, filepath.Base(u.Path))
		}

		switch u.Scheme {
		case "http", "https":
			if err := FetchFile(ctx, srcURI, destPath, &cfg); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			paths = append(paths, destPath)
		case "ftp":
			return paths, fmt.Errorf("fetch: FTP downloads are not supported yet (address: %s)", rawURI)
		case "mirror":
			return paths, fmt.Errorf("fetch: mirror:// expansion is not implemented yet (address: %s)", rawURI)
		default:
			if firstErr == nil {
				firstErr = fmt.Errorf("fetch: unsupported download protocol %q in address %s", u.Scheme, rawURI)
			}
		}
	}

	if len(paths) == 0 && firstErr != nil {
		return paths, firstErr
	}

	return paths, nil
}

// FetchFile downloads a single URI to destPath. The file is downloaded
// to a temporary file first and renamed atomically on success.
func FetchFile(ctx context.Context, uri string, destPath string, cfg *FetchConfig) error {
	timeout := 120 * time.Second
	if cfg != nil && cfg.Timeout > 0 {
		timeout = cfg.Timeout
	}

	httpClient := &http.Client{Timeout: timeout}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return fmt.Errorf("fetch: could not prepare download request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return fmt.Errorf("fetch: could not download from %s: %w", uri, err)
	}
	defer func() { if cerr := resp.Body.Close(); cerr != nil { /* Best effort */ } }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("fetch: server returned error %d when downloading %s", resp.StatusCode, uri)
	}

	tmpPath := destPath + ".part"
	fh, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("fetch: could not create temporary download file: %w", err)
	}

	if _, err := io.Copy(fh, resp.Body); err != nil {
		fh.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("fetch: download of %s failed: %w", uri, err)
	}
	if err := fh.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("fetch: could not finalize downloaded file: %w", err)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("fetch: could not save downloaded file: %w", err)
	}

	return nil
}
