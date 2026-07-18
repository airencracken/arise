package fetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/airencracken/arise/internal/distfiles"
)

type acquireCall struct {
	done chan struct{}
	err  error
}

// Fetcher owns process-wide acquisition coordination. Callers sharing a
// Fetcher never download the same Manifest identity concurrently.
type Fetcher struct {
	Client *http.Client

	mu       sync.Mutex
	inflight map[string]*acquireCall
}

// AcquireManifest is the shared preparation entry point for fetch-only and
// normal builds. It joins enabled SRC_URI entries to Manifest identities and
// returns the verified artifact set consumed by phase execution.
func (f *Fetcher) AcquireManifest(ctx context.Context, manifestPath, srcURI string, use map[string]bool, cfg FetchConfig) (distfiles.VerifiedSet, error) {
	manifest, err := os.Open(manifestPath)
	if err != nil {
		return distfiles.VerifiedSet{}, fmt.Errorf("fetch: open Manifest: %w", err)
	}
	artifacts, planErr := distfiles.Plan(manifest, srcURI, use)
	closeErr := manifest.Close()
	if planErr != nil {
		return distfiles.VerifiedSet{}, fmt.Errorf("fetch: plan verified sources: %w", planErr)
	}
	if closeErr != nil {
		return distfiles.VerifiedSet{}, fmt.Errorf("fetch: close Manifest: %w", closeErr)
	}
	return f.Acquire(ctx, artifacts, cfg)
}

// Acquire returns only after every artifact exists in DISTDIR and passes its
// Manifest size and digest checks.
func (f *Fetcher) Acquire(ctx context.Context, artifacts []distfiles.Artifact, cfg FetchConfig) (distfiles.VerifiedSet, error) {
	cfg.defaults()
	directory := cfg.Destination
	if directory == "" {
		return distfiles.VerifiedSet{}, fmt.Errorf("fetch: DISTDIR must be specified")
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return distfiles.VerifiedSet{}, fmt.Errorf("fetch: create DISTDIR: %w", err)
	}
	for _, artifact := range artifacts {
		if err := f.acquireOne(ctx, directory, artifact, cfg); err != nil {
			return distfiles.VerifiedSet{}, err
		}
	}
	return distfiles.VerifiedSet{Directory: directory, Artifacts: append([]distfiles.Artifact(nil), artifacts...)}, nil
}

func (f *Fetcher) acquireOne(ctx context.Context, directory string, artifact distfiles.Artifact, cfg FetchConfig) error {
	key := artifactKey(directory, artifact)
	f.mu.Lock()
	if f.inflight == nil {
		f.inflight = make(map[string]*acquireCall)
	}
	if running := f.inflight[key]; running != nil {
		f.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-running.done:
			return running.err
		}
	}
	call := &acquireCall{done: make(chan struct{})}
	f.inflight[key] = call
	f.mu.Unlock()

	call.err = f.acquireLeader(ctx, directory, artifact, cfg)
	f.mu.Lock()
	delete(f.inflight, key)
	close(call.done)
	f.mu.Unlock()
	return call.err
}

func artifactKey(directory string, artifact distfiles.Artifact) string {
	algorithms := make([]string, 0, len(artifact.Digests))
	for algorithm := range artifact.Digests {
		algorithms = append(algorithms, algorithm)
	}
	sort.Strings(algorithms)
	var key strings.Builder
	key.WriteString(filepath.Join(directory, artifact.Name))
	key.WriteByte(0)
	key.WriteString(strconv.FormatInt(artifact.Size, 10))
	for _, algorithm := range algorithms {
		key.WriteByte(0)
		key.WriteString(algorithm)
		key.WriteByte('=')
		key.WriteString(artifact.Digests[algorithm])
	}
	return key.String()
}

func (f *Fetcher) acquireLeader(ctx context.Context, directory string, artifact distfiles.Artifact, cfg FetchConfig) error {
	unlock, err := acquireArtifactLock(ctx, directory, artifact.Name)
	if err != nil {
		return err
	}
	defer unlock()
	destination := filepath.Join(directory, artifact.Name)
	reportProgress(cfg, Progress{Stage: ProgressChecking, Artifact: artifact.Name, Total: artifact.Size})
	if err := distfiles.Verify(destination, artifact); err == nil {
		reportProgress(cfg, Progress{Stage: ProgressCached, Artifact: artifact.Name, Downloaded: artifact.Size, Total: artifact.Size})
		return nil
	}
	if len(artifact.Sources) == 0 {
		return fmt.Errorf("fetch: %s is not verified and has no source URI", artifact.Name)
	}
	var failures []error
	for _, source := range artifact.Sources {
		endpoints, err := expandMirrorSource(source, cfg)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, endpoint := range endpoints {
			if err := f.downloadVerified(ctx, endpoint, destination, artifact, cfg); err != nil {
				failures = append(failures, err)
				continue
			}
			return nil
		}
	}
	return fmt.Errorf("fetch: all sources failed for %s: %v", artifact.Name, failures)
}

func (f *Fetcher) downloadVerified(ctx context.Context, source, destination string, artifact distfiles.Artifact, cfg FetchConfig) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return fmt.Errorf("prepare %s: %w", source, err)
	}
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download %s: %w", source, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("download %s: HTTP %d", source, response.StatusCode)
	}
	reportProgress(cfg, Progress{Stage: ProgressDownload, Artifact: artifact.Name, Source: source, Total: artifact.Size})
	temporary, err := os.CreateTemp(filepath.Dir(destination), "."+artifact.Name+".part-*")
	if err != nil {
		return fmt.Errorf("fetch: create temporary distfile: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		temporary.Close()
		if !committed {
			os.Remove(temporaryPath)
		}
	}()
	reader := &progressReader{reader: response.Body, artifact: artifact, source: source, progress: cfg.Progress}
	if _, err := io.Copy(temporary, reader); err != nil {
		return fmt.Errorf("download %s: %w", source, err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("fetch: sync temporary distfile: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("fetch: close temporary distfile: %w", err)
	}
	reportProgress(cfg, Progress{Stage: ProgressVerifying, Artifact: artifact.Name, Source: source, Downloaded: reader.downloaded, Total: artifact.Size})
	if err := distfiles.Verify(temporaryPath, artifact); err != nil {
		return fmt.Errorf("download %s: %w", source, err)
	}
	if err := os.Chmod(temporaryPath, 0o644); err != nil {
		return fmt.Errorf("fetch: set distfile permissions: %w", err)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("fetch: commit verified distfile: %w", err)
	}
	committed = true
	reportProgress(cfg, Progress{Stage: ProgressComplete, Artifact: artifact.Name, Source: source, Downloaded: artifact.Size, Total: artifact.Size})
	return nil
}

func reportProgress(cfg FetchConfig, progress Progress) {
	if cfg.Progress != nil {
		cfg.Progress(progress)
	}
}

type progressReader struct {
	reader     io.Reader
	artifact   distfiles.Artifact
	source     string
	progress   func(Progress)
	downloaded int64
}

func (r *progressReader) Read(buffer []byte) (int, error) {
	count, err := r.reader.Read(buffer)
	r.downloaded += int64(count)
	if count != 0 && r.progress != nil {
		r.progress(Progress{Stage: ProgressDownload, Artifact: r.artifact.Name, Source: r.source, Downloaded: r.downloaded, Total: r.artifact.Size})
	}
	return count, err
}
