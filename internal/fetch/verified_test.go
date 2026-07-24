package fetch

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/distfiles"
)

func TestAcquireManualOnlyUsesVerifiedCacheWithoutNetwork(t *testing.T) {
	payload := []byte("manually downloaded")
	artifact := testArtifact(payload, "https://invalid.example/source.tar")
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, artifact.Name), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("manual-only acquisition attempted network access")
		return nil, nil
	})}
	set, err := (&Fetcher{Client: client}).Acquire(context.Background(), []distfiles.Artifact{artifact}, FetchConfig{DistfilesDir: directory, ManualOnly: true})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if len(set.Artifacts) != 1 || set.Artifacts[0].Name != artifact.Name {
		t.Fatalf("verified set = %#v", set)
	}
}

func TestAcquireManualOnlyReturnsTypedMissingArtifactWithoutNetwork(t *testing.T) {
	artifact := testArtifact([]byte("missing"), "https://invalid.example/source.tar")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("manual-only acquisition attempted network access")
		return nil, nil
	})}
	_, err := (&Fetcher{Client: client}).Acquire(context.Background(), []distfiles.Artifact{artifact}, FetchConfig{DistfilesDir: t.TempDir(), ManualOnly: true})
	var required *ManualFetchRequiredError
	if !errors.As(err, &required) || required.Artifact != artifact.Name {
		t.Fatalf("Acquire error = %v, want ManualFetchRequiredError for %s", err, artifact.Name)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func testArtifact(content []byte, sources ...string) distfiles.Artifact {
	digest := sha512.Sum512(content)
	return distfiles.Artifact{Name: "source.tar", Size: int64(len(content)), Digests: map[string]string{"SHA512": hex.EncodeToString(digest[:])}, Sources: sources}
}

func TestAcquireDownloadsVerifiesAndReusesDISTDIR(t *testing.T) {
	content := []byte("artifact")
	var requests atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(content)), Header: make(http.Header)}, nil
	})}
	fetcher := &Fetcher{Client: client}
	directory := t.TempDir()
	artifact := testArtifact(content, "https://one/source.tar")
	for range 2 {
		set, err := fetcher.Acquire(context.Background(), []distfiles.Artifact{artifact}, FetchConfig{DistfilesDir: directory})
		if err != nil {
			t.Fatal(err)
		}
		if len(set.Paths()) != 1 {
			t.Fatalf("verified set = %#v", set)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestAcquireReportsDownloadVerificationAndCacheProgress(t *testing.T) {
	content := []byte("progress artifact")
	artifact := testArtifact(content, "https://one/source.tar")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(content)), Header: make(http.Header)}, nil
	})}
	var events []Progress
	config := FetchConfig{DistfilesDir: t.TempDir(), Progress: func(progress Progress) { events = append(events, progress) }}
	fetcher := &Fetcher{Client: client}
	if _, err := fetcher.Acquire(context.Background(), []distfiles.Artifact{artifact}, config); err != nil {
		t.Fatal(err)
	}
	var stages []ProgressStage
	for _, event := range events {
		stages = append(stages, event.Stage)
	}
	for _, required := range []ProgressStage{ProgressChecking, ProgressDownload, ProgressVerifying, ProgressComplete} {
		if !slices.Contains(stages, required) {
			t.Errorf("progress stages %v omit %s", stages, required)
		}
	}
	events = nil
	if _, err := fetcher.Acquire(context.Background(), []distfiles.Artifact{artifact}, config); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Stage != ProgressCached {
		t.Fatalf("cached progress = %#v", events)
	}
}

func TestAcquireInvalidatesOperationCacheWhenDistfileChanges(t *testing.T) {
	content := []byte("immutable artifact")
	artifact := testArtifact(content)
	directory := t.TempDir()
	path := filepath.Join(directory, artifact.Name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	fetcher := &Fetcher{}
	if _, err := fetcher.Acquire(context.Background(), []distfiles.Artifact{artifact}, FetchConfig{DistfilesDir: directory}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fetcher.Acquire(context.Background(), []distfiles.Artifact{artifact}, FetchConfig{DistfilesDir: directory}); err == nil {
		t.Fatal("changed distfile reused stale verified cache entry")
	}
}

func TestAcquireManifestIsSharedPreparationEntryPoint(t *testing.T) {
	content := []byte("planned")
	artifact := testArtifact(content, "https://one/source.tar")
	manifestPath := filepath.Join(t.TempDir(), "Manifest")
	manifest := "DIST source.tar 7 SHA512 " + artifact.Digests["SHA512"] + "\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(content)), Header: make(http.Header)}, nil
	})}
	set, err := (&Fetcher{Client: client}).AcquireManifest(context.Background(), manifestPath, "https://one/source.tar", nil, FetchConfig{DistfilesDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Artifacts) != 1 || set.Artifacts[0].Name != "source.tar" {
		t.Fatalf("set = %#v", set)
	}
}

func TestAcquireFallsBackAcrossMirrorEndpointsInOrder(t *testing.T) {
	content := []byte("mirror")
	artifact := testArtifact(content, "mirror://gentoo/source.tar")
	var requested []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requested = append(requested, request.URL.String())
		if request.URL.Host == "first.example" {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("unavailable")), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(content)), Header: make(http.Header)}, nil
	})}
	_, err := (&Fetcher{Client: client}).Acquire(context.Background(), []distfiles.Artifact{artifact}, FetchConfig{
		DistfilesDir:  t.TempDir(),
		GentooMirrors: []string{"https://first.example/distfiles", "https://second.example/distfiles"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://first.example/distfiles/e9/source.tar", "https://second.example/distfiles/e9/source.tar"}
	if strings.Join(requested, " ") != strings.Join(want, " ") {
		t.Fatalf("requests = %#v, want %#v", requested, want)
	}
}

func TestAcquireUsesGentooMirrorBeforeOrdinaryUpstream(t *testing.T) {
	content := []byte("verified rhash archive")
	artifact := testArtifact(content, "https://downloads.sourceforge.net/rhash/source.tar")
	var requested []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requested = append(requested, request.URL.String())
		if request.URL.Host == "distfiles.gentoo.org" {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(content)), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("wrong-sized SourceForge response")), Header: make(http.Header)}, nil
	})}
	_, err := (&Fetcher{Client: client}).Acquire(context.Background(), []distfiles.Artifact{artifact}, FetchConfig{
		DistfilesDir: t.TempDir(), GentooMirrors: []string{"http://distfiles.gentoo.org"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"http://distfiles.gentoo.org/distfiles/e9/source.tar"}
	if !slices.Equal(requested, want) {
		t.Fatalf("requests = %#v, want %#v", requested, want)
	}
}

func TestAcquireFallsBackFromBadGentooMirrorToUpstream(t *testing.T) {
	content := []byte("verified upstream archive")
	artifact := testArtifact(content, "https://upstream.example/source.tar")
	var requested []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requested = append(requested, request.URL.String())
		body := []byte("wrong size")
		if request.URL.Host == "upstream.example" {
			body = content
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})}
	_, err := (&Fetcher{Client: client}).Acquire(context.Background(), []distfiles.Artifact{artifact}, FetchConfig{
		DistfilesDir: t.TempDir(), GentooMirrors: []string{"https://mirror.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://mirror.example/distfiles/e9/source.tar", "https://mirror.example/distfiles/source.tar", "https://upstream.example/source.tar"}
	if !slices.Equal(requested, want) {
		t.Fatalf("requests = %#v, want %#v", requested, want)
	}
}

func TestAcquireHonorsMirrorRestrictionAndPrimaryURI(t *testing.T) {
	content := []byte("verified")
	for _, test := range []struct {
		name string
		cfg  FetchConfig
		want []string
	}{
		{name: "mirror restricted", cfg: FetchConfig{RestrictMirrors: true}, want: []string{"https://upstream.example/source.tar"}},
		{name: "primary URI", cfg: FetchConfig{PrimaryURI: true}, want: []string{"https://upstream.example/source.tar"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			artifact := testArtifact(content, "https://upstream.example/source.tar")
			var requested []string
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				requested = append(requested, request.URL.String())
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(content)), Header: make(http.Header)}, nil
			})}
			test.cfg.DistfilesDir = t.TempDir()
			test.cfg.GentooMirrors = []string{"https://mirror.example"}
			if _, err := (&Fetcher{Client: client}).Acquire(context.Background(), []distfiles.Artifact{artifact}, test.cfg); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(requested, test.want) {
				t.Fatalf("requests = %#v, want %#v", requested, test.want)
			}
		})
	}
}

func TestAcquireRejectsCorruptDownloadsAndPreservesExistingFile(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "source.tar")
	if err := os.WriteFile(destination, []byte("old-corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader([]byte("new-corrupt"))), Header: make(http.Header)}, nil
	})}
	_, err := (&Fetcher{Client: client}).Acquire(context.Background(), []distfiles.Artifact{testArtifact([]byte("wanted"), "https://one/source.tar")}, FetchConfig{DistfilesDir: directory})
	if err == nil {
		t.Fatal("corrupt download accepted")
	}
	got, readErr := os.ReadFile(destination)
	if readErr != nil || string(got) != "old-corrupt" {
		t.Fatalf("existing file changed to %q, %v", got, readErr)
	}
}

func TestAcquireDeduplicatesConcurrentRequests(t *testing.T) {
	content := []byte("shared")
	var requests atomic.Int32
	release := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		<-release
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(content)), Header: make(http.Header)}, nil
	})}
	fetcher := &Fetcher{Client: client}
	artifact := testArtifact(content, "https://one/source.tar")
	var wait sync.WaitGroup
	errors := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := fetcher.Acquire(context.Background(), []distfiles.Artifact{artifact}, FetchConfig{DistfilesDir: t.TempDir()})
			errors <- err
		}()
	}
	close(release)
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	// Different DISTDIRs are different cache identities and must not coalesce.
	if requests.Load() != 2 {
		t.Fatalf("requests across distinct DISTDIRs = %d, want 2", requests.Load())
	}
}

func TestAcquireDeduplicatesSameDISTDIR(t *testing.T) {
	content := []byte("shared")
	var requests atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(content)), Header: make(http.Header)}, nil
	})}
	fetcher := &Fetcher{Client: client}
	directory := t.TempDir()
	artifact := testArtifact(content, "https://one/source.tar")
	var wait sync.WaitGroup
	errors := make(chan error, 8)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := fetcher.Acquire(context.Background(), []distfiles.Artifact{artifact}, FetchConfig{DistfilesDir: directory})
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestAcquireCoordinatesIndependentFetchers(t *testing.T) {
	content := []byte("cross-process-lock")
	artifact := testArtifact(content, "https://one/source.tar")
	directory := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	var requests atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		if requests.Add(1) == 1 {
			close(started)
		}
		<-release
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(content)), Header: make(http.Header)}, nil
	})}
	errors := make(chan error, 2)
	for index := range 2 {
		fetcher := &Fetcher{Client: client}
		go func(index int) {
			if index == 1 {
				<-started
			}
			_, err := fetcher.Acquire(context.Background(), []distfiles.Artifact{artifact}, FetchConfig{DistfilesDir: directory})
			errors <- err
		}(index)
	}
	<-started
	close(release)
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("independent fetcher requests = %d, want 1", requests.Load())
	}
}

func TestArtifactLockWaitIsContextCancellable(t *testing.T) {
	directory := t.TempDir()
	unlock, err := acquireArtifactLock(context.Background(), directory, "source.tar")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := acquireArtifactLock(ctx, directory, "source.tar"); !errors.Is(err, context.Canceled) {
		t.Fatalf("lock wait error = %v, want context cancellation", err)
	}
}

func TestArtifactLockCoordinatesSubprocesses(t *testing.T) {
	directory := t.TempDir()
	unlock, err := acquireArtifactLock(context.Background(), directory, "source.tar")
	if err != nil {
		t.Fatal(err)
	}
	blocked := exec.Command(os.Args[0], "-test.run=^TestArtifactLockSubprocessHelper$")
	blocked.Env = append(os.Environ(), "ARISE_FETCH_LOCK_DIR="+directory, "ARISE_FETCH_LOCK_EXPECT=blocked")
	if output, err := blocked.CombinedOutput(); err != nil {
		t.Fatalf("blocked subprocess: %v: %s", err, output)
	}
	unlock()
	acquired := exec.Command(os.Args[0], "-test.run=^TestArtifactLockSubprocessHelper$")
	acquired.Env = append(os.Environ(), "ARISE_FETCH_LOCK_DIR="+directory, "ARISE_FETCH_LOCK_EXPECT=acquired")
	if output, err := acquired.CombinedOutput(); err != nil {
		t.Fatalf("released subprocess: %v: %s", err, output)
	}
}

func TestArtifactLockSubprocessHelper(t *testing.T) {
	directory := os.Getenv("ARISE_FETCH_LOCK_DIR")
	if directory == "" {
		t.Skip("helper")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	unlock, err := acquireArtifactLock(ctx, directory, "source.tar")
	expect := os.Getenv("ARISE_FETCH_LOCK_EXPECT")
	if expect == "blocked" {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("blocked lock error = %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}
