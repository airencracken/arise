package walker

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/airencracken/arise/internal/metadata"
	"github.com/airencracken/arise/internal/phaseproto"
	"github.com/airencracken/arise/internal/portage"
)

// WalkEvaluatedEbuildRoots fills gaps in repository caches using the native
// Bash worker. Discovery-only records never become authoritative by relabeling:
// every uncached ebuild is sourced together with its inherited eclasses.
func WalkEvaluatedEbuildRoots(ctx context.Context, cacheRoots []string, repositories []portage.RepoEntry) (<-chan *metadata.PackageMetadata, <-chan error) {
	// Do not trust a potentially stale external Portage cache: evaluate the
	// current ebuild and eclass contents on each explicit index refresh.
	input, inputErrors := WalkUncachedEbuildRootsWithPortageCache(cacheRoots, "")
	results := make(chan *metadata.PackageMetadata)
	errs := make(chan error, errBufSize)
	go func() {
		defer close(results)
		defer close(errs)
		for input != nil || inputErrors != nil {
			select {
			case record, ok := <-input:
				if !ok {
					input = nil
					continue
				}
				if ctx.Err() != nil {
					continue // Drain the discovery walker before returning.
				}
				evaluated, err := phaseproto.EvaluateMetadata(ctx, record, repositories)
				if err != nil {
					errs <- err
					continue
				}
				select {
				case results <- evaluated:
				case <-ctx.Done():
				}
			case err, ok := <-inputErrors:
				if !ok {
					inputErrors = nil
					continue
				}
				errs <- err
			}
		}
		if err := ctx.Err(); err != nil {
			errs <- err
		}
	}()
	return results, errs
}

// RepositoryEntries includes --repo even when it has no repos.conf entry.
func RepositoryEntries(cacheRoots []string, configured []portage.RepoEntry) []portage.RepoEntry {
	entries := append([]portage.RepoEntry(nil), configured...)
	for i := range entries {
		entries[i].Masters = readRepositoryMasters(entries[i].Location)
	}
	for _, root := range cacheRoots {
		directory := filepath.Dir(filepath.Dir(root))
		found := false
		for _, entry := range entries {
			if filepath.Clean(entry.Location) == filepath.Clean(directory) {
				found = true
				break
			}
		}
		if !found {
			entries = append(entries, portage.RepoEntry{Name: repositoryName(directory), Location: directory, Masters: readRepositoryMasters(directory)})
		}
	}
	return entries
}

func repositoryName(directory string) string {
	if data, err := os.ReadFile(filepath.Join(directory, "profiles", "repo_name")); err == nil && strings.TrimSpace(string(data)) != "" {
		return strings.TrimSpace(string(data))
	}
	return filepath.Base(directory)
}
