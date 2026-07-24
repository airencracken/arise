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
	"syscall"

	"github.com/airencracken/arise/internal/distfiles"
)

type acquireCall struct {
	done chan struct{}
	err  error
}

type verifiedFile struct {
	size    int64
	modTime int64
	inode   uint64
	device  uint64
}

// ManualFetchRequiredError reports a Manifest artifact which is unavailable
// while RESTRICT=fetch forbids automatic acquisition.
type ManualFetchRequiredError struct {
	Artifact string
	Cause    error
}

func (e *ManualFetchRequiredError) Error() string {
	return fmt.Sprintf("fetch: %s requires manual acquisition: %v", e.Artifact, e.Cause)
}

func (e *ManualFetchRequiredError) Unwrap() error { return e.Cause }

// Fetcher owns process-wide acquisition coordination. Callers sharing a
// Fetcher never download the same Manifest identity concurrently.
type Fetcher struct {
	Client *http.Client

	mu       sync.Mutex
	inflight map[string]*acquireCall
	verified map[string]verifiedFile
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
	if stamp, ok := f.verified[key]; ok {
		f.mu.Unlock()
		if current, err := verifiedFileStamp(filepath.Join(directory, artifact.Name)); err == nil && current == stamp {
			reportProgress(cfg, Progress{Stage: ProgressCached, Artifact: artifact.Name, Downloaded: artifact.Size, Total: artifact.Size})
			return nil
		}
		f.mu.Lock()
		delete(f.verified, key)
	}
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
	if call.err == nil {
		if stamp, stampErr := verifiedFileStamp(filepath.Join(directory, artifact.Name)); stampErr == nil {
			if f.verified == nil {
				f.verified = make(map[string]verifiedFile)
			}
			f.verified[key] = stamp
		}
	}
	delete(f.inflight, key)
	close(call.done)
	f.mu.Unlock()
	return call.err
}

func verifiedFileStamp(path string) (verifiedFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return verifiedFile{}, err
	}
	stamp := verifiedFile{size: info.Size(), modTime: info.ModTime().UnixNano()}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		stamp.inode, stamp.device = stat.Ino, uint64(stat.Dev)
	}
	return stamp, nil
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
	verifyErr := distfiles.Verify(destination, artifact)
	if verifyErr == nil {
		reportProgress(cfg, Progress{Stage: ProgressCached, Artifact: artifact.Name, Downloaded: artifact.Size, Total: artifact.Size})
		return nil
	}
	if cfg.ManualOnly {
		return &ManualFetchRequiredError{Artifact: artifact.Name, Cause: verifyErr}
	}
	if len(artifact.Sources) == 0 {
		return fmt.Errorf("fetch: %s is not verified and has no source URI", artifact.Name)
	}
	var failures []error
	automaticMirrors := automaticGentooSources(artifact.Name, cfg)
	if cfg.RestrictMirrors {
		automaticMirrors = nil
	}
	var endpoints []string
	if !cfg.PrimaryURI {
		endpoints = append(endpoints, automaticMirrors...)
	}
	for _, source := range artifact.Sources {
		expanded, err := expandMirrorSource(source, cfg)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		endpoints = append(endpoints, expanded...)
	}
	if cfg.PrimaryURI {
		endpoints = append(endpoints, automaticMirrors...)
	}
	seen := make(map[string]bool)
	for _, endpoint := range endpoints {
		if seen[endpoint] {
			continue
		}
		seen[endpoint] = true
		if err := f.downloadVerified(ctx, endpoint, destination, artifact, cfg); err != nil {
			failures = append(failures, err)
			continue
		}
		return nil
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
