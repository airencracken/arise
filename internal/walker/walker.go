package walker

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/airencracken/arise/internal/ebuild"
	"github.com/airencracken/arise/internal/metadata"
)

const errBufSize = 128

// WalkCache walks the md5-cache tree rooted at root using runtime.NumCPU()
// parallel workers. Successful parses are sent on the results channel; non-fatal
// file/parse errors are sent on the errs channel. Both channels are closed when
// the walk finishes. Callers must drain both channels concurrently (e.g. via
// select) to avoid deadlock.
func WalkCache(root string) (<-chan *metadata.PackageMetadata, <-chan error) {
	return WalkCacheDir(root, 0)
}

// WalkCacheRoots combines metadata-cache walks from multiple repositories.
func WalkCacheRoots(roots []string) (<-chan *metadata.PackageMetadata, <-chan error) {
	results := make(chan *metadata.PackageMetadata)
	errs := make(chan error, errBufSize)
	var wg sync.WaitGroup
	for overlayIndex, root := range roots {
		root := root
		overlayIndex := overlayIndex
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			continue
		}
		wg.Add(2)
		repositoryPath := filepath.Dir(filepath.Dir(root))
		repository := filepath.Base(repositoryPath)
		if data, err := os.ReadFile(filepath.Join(repositoryPath, "profiles", "repo_name")); err == nil && strings.TrimSpace(string(data)) != "" {
			repository = strings.TrimSpace(string(data))
		}
		masters := readRepositoryMasters(repositoryPath)
		rootResults, rootErrs := walkCacheDir(root, 0, repository, repositoryPath, overlayIndex)
		go func() {
			defer wg.Done()
			for result := range rootResults {
				result.RepositoryMasters = append([]string(nil), masters...)
				result.RepositoryPriority = overlayIndex
				results <- result
			}
		}()
		go func() {
			defer wg.Done()
			for err := range rootErrs {
				errs <- err
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
		close(errs)
	}()
	return results, errs
}

// WalkUncachedEbuildRoots discovers ebuilds that have no md5-cache record.
// Static metadata is deliberately marked incomplete; it is suitable for
// discovery/state parity but not authoritative execution decisions.
func WalkUncachedEbuildRoots(cacheRoots []string) (<-chan *metadata.PackageMetadata, <-chan error) {
	results := make(chan *metadata.PackageMetadata)
	errs := make(chan error, errBufSize)
	go func() {
		defer close(results)
		defer close(errs)
		for priority, cacheRoot := range cacheRoots {
			repo := filepath.Dir(filepath.Dir(cacheRoot))
			repoName := filepath.Base(repo)
			if data, err := os.ReadFile(filepath.Join(repo, "profiles", "repo_name")); err == nil && strings.TrimSpace(string(data)) != "" {
				repoName = strings.TrimSpace(string(data))
			}
			var paths []string
			_ = filepath.WalkDir(repo, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() {
					if path != repo && (d.Name() == "metadata" || d.Name() == "profiles" || d.Name() == "eclass" || strings.HasPrefix(d.Name(), ".")) {
						return filepath.SkipDir
					}
					return nil
				}
				if strings.HasSuffix(d.Name(), ".ebuild") {
					rel, relErr := filepath.Rel(repo, path)
					parts := strings.Split(filepath.ToSlash(rel), "/")
					if relErr == nil && len(parts) == 3 && parts[1] != "files" && strings.HasPrefix(parts[2], parts[1]+"-") {
						paths = append(paths, path)
					}
				}
				return nil
			})
			sort.Strings(paths)
			for _, path := range paths {
				category := filepath.Base(filepath.Dir(filepath.Dir(path)))
				pf := strings.TrimSuffix(filepath.Base(path), ".ebuild")
				if _, err := os.Stat(filepath.Join(cacheRoot, category, pf)); err == nil {
					continue
				}
				parsed, err := ebuild.ParseEbuild(path)
				if err != nil {
					errs <- err
					continue
				}
				var cache strings.Builder
				keys := make([]string, 0, len(parsed.Variables))
				for key := range parsed.Variables {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					cache.WriteString(key)
					cache.WriteByte('=')
					cache.WriteString(parsed.Variables[key])
					cache.WriteByte('\n')
				}
				m, err := metadata.ParseCacheEntry(category+"/"+pf, []byte(cache.String()))
				if err != nil {
					errs <- err
					continue
				}
				m.Repository, m.RepositoryPath, m.RepositoryPriority, m.OverlayIndex = repoName, repo, priority, priority
				m.RepositoryMasters = readRepositoryMasters(repo)
				if m.Unknown == nil {
					m.Unknown = map[string]string{}
				}
				m.Unknown["_arise_incomplete_metadata_"] = "true"
				results <- m
			}
		}
	}()
	return results, errs
}

// MergeWalks combines two walker pairs while preserving backpressure.
func MergeWalks(aResults, bResults <-chan *metadata.PackageMetadata, aErrs, bErrs <-chan error) (<-chan *metadata.PackageMetadata, <-chan error) {
	results := make(chan *metadata.PackageMetadata)
	errs := make(chan error, errBufSize)
	var wg sync.WaitGroup
	for _, input := range []<-chan *metadata.PackageMetadata{aResults, bResults} {
		wg.Add(1)
		go func(ch <-chan *metadata.PackageMetadata) {
			defer wg.Done()
			for value := range ch {
				results <- value
			}
		}(input)
	}
	var ewg sync.WaitGroup
	for _, input := range []<-chan error{aErrs, bErrs} {
		ewg.Add(1)
		go func(ch <-chan error) {
			defer ewg.Done()
			for value := range ch {
				errs <- value
			}
		}(input)
	}
	go func() { wg.Wait(); close(results) }()
	go func() { ewg.Wait(); close(errs) }()
	return results, errs
}

func readRepositoryMasters(repositoryPath string) []string {
	data, err := os.ReadFile(filepath.Join(repositoryPath, "metadata", "layout.conf"))
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(key) == "masters" {
			return strings.Fields(value)
		}
	}
	return nil
}

// WalkCacheDir is like WalkCache but accepts an explicit number of worker
// goroutines. Values <= 0 default to runtime.NumCPU().
func WalkCacheDir(root string, workers int) (<-chan *metadata.PackageMetadata, <-chan error) {
	return walkCacheDir(root, workers, "", "", 0)
}

func walkCacheDir(root string, workers int, repository, repositoryPath string, overlayIndex int) (<-chan *metadata.PackageMetadata, <-chan error) {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	results := make(chan *metadata.PackageMetadata)
	errs := make(chan error, errBufSize)
	paths := make(chan string)

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for path := range paths {
				data, err := os.ReadFile(path)
				if err != nil {
					errs <- err
					continue
				}

				rel, err := filepath.Rel(root, path)
				if err != nil {
					errs <- err
					continue
				}

				pkg, err := metadata.ParseCacheEntry(rel, data)
				if err != nil {
					errs <- err
					continue
				}
				pkg.Repository = repository
				pkg.RepositoryPath = repositoryPath
				pkg.OverlayIndex = overlayIndex
				results <- pkg
			}
		}()
	}

	go func() {
		defer close(paths)
		if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				errs <- err
				return nil
			}
			if d.IsDir() || !d.Type().IsRegular() {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "skel.ebuild" || name == "skel.metadata.xml" || name == "header.txt" || name == ".mailmap" {
				return nil
			}
			paths <- path
			return nil
		}); err != nil {
			errs <- err
		}
	}()

	go func() {
		wg.Wait()
		close(results)
		close(errs)
	}()

	return results, errs
}
