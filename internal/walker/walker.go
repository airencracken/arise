package walker

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"

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

// WalkCacheDir is like WalkCache but accepts an explicit number of worker
// goroutines. Values <= 0 default to runtime.NumCPU().
func WalkCacheDir(root string, workers int) (<-chan *metadata.PackageMetadata, <-chan error) {
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
