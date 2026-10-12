package binpkg

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/airencracken/arise/internal/atom"
)

// FetchBinhostIndex reads remote planning metadata without downloading package
// payloads or writing the local package cache.
func FetchBinhostIndex(ctx context.Context, host string) (*PackagesIndex, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(host, "/")+"/Packages", nil)
	if err != nil {
		return nil, err
	}
	response, err := (&http.Client{Timeout: 120 * time.Second}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("binpkg: download Packages index: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("binpkg: Packages index returned HTTP %d", response.StatusCode)
	}
	return ParsePackagesIndex(response.Body)
}

// PackageInfoFromIndex accepts an entry from ParsePackagesIndex. Paths identify
// the future local archive; payload verification remains an acquisition step.
func PackageInfoFromIndex(entry PackageIndexEntry, destination string) (*BinPkgInfo, error) {
	parsed, err := atom.Parse(entry["CPV"])
	if err != nil || parsed.Version == nil {
		return nil, fmt.Errorf("binpkg: invalid advertised CPV %q", entry["CPV"])
	}
	path := entry["PATH"]
	if path == "" || path == "." || filepath.IsAbs(path) || filepath.ToSlash(filepath.Clean(path)) != path || path == ".." || strings.HasPrefix(path, "../") {
		return nil, fmt.Errorf("binpkg: unsafe advertised PATH %q", path)
	}
	size, err := strconv.ParseInt(entry["SIZE"], 10, 64)
	if err != nil || size <= 0 || (entry["SHA512"] == "" && entry["BLAKE2B"] == "") {
		return nil, fmt.Errorf("binpkg: advertised %s lacks usable size or digest", entry["CPV"])
	}
	if entry["SLOT"] == "" || entry["EAPI"] == "" {
		return nil, fmt.Errorf("binpkg: advertised %s lacks SLOT or EAPI", entry["CPV"])
	}
	slot, subslot := parseSlot(entry["SLOT"])
	if subslot == "" {
		subslot = slot
	}
	buildTime, _ := strconv.ParseInt(entry["BUILD_TIME"], 10, 64)
	return &BinPkgInfo{
		Category: parsed.Category, Package: parsed.Package, Version: parsed.Version.Raw,
		Slot: slot, Subslot: subslot, EAPI: entry["EAPI"], Use: entry["USE"], IUse: entry["IUSE"],
		Repository: entry["repository"], CHOST: entry["CHOST"], ABI: entry["ABI"],
		BuildID: entry["BUILD_ID"], BuildTime: buildTime, Size: size,
		DefinedPhases: entry["DEFINED_PHASES"],
		Path:          filepath.Join(destination, filepath.FromSlash(path)),
	}, nil
}

// BinaryLifecycleError names required hooks the binary executor cannot run.
func BinaryLifecycleError(phases string) error {
	for _, phase := range strings.Fields(phases) {
		switch strings.TrimPrefix(phase, "pkg_") {
		case "pretend", "setup", "preinst", "postinst", "prerm", "postrm":
			return fmt.Errorf("binary package requires unsupported lifecycle phase %s; rebuild from source", phase)
		}
	}
	return nil
}

// AcquireIndexedPackage downloads the exact instance selected during planning.
// It preserves size, digest, container and atomic publication checks.
func AcquireIndexedPackage(ctx context.Context, host, destination string, entry PackageIndexEntry) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := PackageInfoFromIndex(entry, destination); err != nil {
		return "", err
	}
	return downloadIndexedPackage(ctx, &http.Client{Timeout: 120 * time.Second}, host, destination, entry)
}
