package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/fetch"
)

func TestConcurrentFetchProgressUsesCompleteLines(t *testing.T) {
	var output bytes.Buffer
	progress := newFetchProgress(true, &output)
	progress.terminal = true
	progress.setConcurrent(true)
	for _, event := range []fetch.Progress{
		{Stage: fetch.ProgressDownload, Artifact: "first.tar.xz", Source: "https://example/first.tar.xz", Downloaded: 5, Total: 10},
		{Stage: fetch.ProgressChecking, Artifact: "second.tar.xz"},
		{Stage: fetch.ProgressVerifying, Artifact: "first.tar.xz"},
		{Stage: fetch.ProgressComplete, Artifact: "first.tar.xz"},
	} {
		progress.Report(event)
	}
	got := output.String()
	if strings.Contains(got, "\r") {
		t.Fatalf("concurrent output contains carriage return: %q", got)
	}
	for _, want := range []string{
		">>> Downloading https://example/first.tar.xz\n",
		">>> Checking second.tar.xz\n",
		">>> Verifying first.tar.xz against Manifest\n",
		">>> Fetched and verified first.tar.xz\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("concurrent output %q lacks %q", got, want)
		}
	}
}
