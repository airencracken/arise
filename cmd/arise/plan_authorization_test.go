package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/resolve"
)

func TestHashStatePathMatchesCanonicalEntryStream(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "value"), []byte("payload"), 0o640); err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	if err := hashStatePath(hash, "fixture", root); err != nil {
		t.Fatal(err)
	}

	expected := sha256.New()
	for _, entry := range []struct {
		relative string
		path     string
	}{
		{".", root},
		{"nested", filepath.Join(root, "nested")},
		{filepath.Join("nested", "value"), filepath.Join(root, "nested", "value")},
	} {
		info, err := os.Lstat(entry.path)
		if err != nil {
			t.Fatal(err)
		}
		expected.Write([]byte("fixture\x00" + filepath.ToSlash(entry.relative) + "\x00" +
			strconv.FormatUint(uint64(info.Mode()), 10) + "\x00"))
		if info.Mode().IsRegular() {
			expected.Write([]byte("payload"))
		}
	}
	if got, want := hex.EncodeToString(hash.Sum(nil)), hex.EncodeToString(expected.Sum(nil)); got != want {
		t.Fatalf("fingerprint = %s, want canonical stream %s", got, want)
	}
}

func TestHashStatePathPropagatesWriterFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 256*1024), 0o600); err != nil {
		t.Fatal(err)
	}
	err := hashStatePath(failingFingerprintWriter{remaining: 64}, "fixture", path)
	if err == nil || !strings.Contains(err.Error(), "injected fingerprint write failure") {
		t.Fatalf("writer failure = %v", err)
	}
}

type failingFingerprintWriter struct {
	remaining int
}

func (w failingFingerprintWriter) Write(data []byte) (int, error) {
	if len(data) <= w.remaining {
		return len(data), nil
	}
	return 0, fmt.Errorf("injected fingerprint write failure")
}

func TestCanonicalPlanSHA256IgnoresTimingAndMapOrder(t *testing.T) {
	first := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{{
		Atom: planTestAtom(t, "media-sound/apulse-0.1.14"), Action: "install", Slot: "0", Repository: "gentoo",
		UseFlags: map[string]bool{"test": false, "abi_x86_64": true},
	}}}
	second := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified, Install: []resolve.PkgAction{{
		Atom: planTestAtom(t, "media-sound/apulse-0.1.14"), Action: "install", Slot: "0", Repository: "gentoo",
		UseFlags: map[string]bool{"abi_x86_64": true, "test": false},
	}}, Metrics: resolve.ResolveMetrics{Search: 12345}}
	cfg := resolve.DefaultResolveConfig()
	left := canonicalPlanSHA256([]string{"media-sound/apulse"}, cfg, first, strings.Repeat("a", 64))
	right := canonicalPlanSHA256([]string{"media-sound/apulse"}, cfg, second, strings.Repeat("a", 64))
	if left != right {
		t.Fatalf("equivalent plans have different authorization digests: %s != %s", left, right)
	}
	changedOptions := cfg
	changedOptions.ExplicitReinstall = true
	if changed := canonicalPlanSHA256([]string{"media-sound/apulse"}, changedOptions, second, strings.Repeat("a", 64)); changed == left {
		t.Fatal("explicit reinstall semantics did not invalidate plan authorization")
	}
	second.Install[0].UseFlags["test"] = true
	if changed := canonicalPlanSHA256([]string{"media-sound/apulse"}, cfg, second, strings.Repeat("a", 64)); changed == left {
		t.Fatal("USE mutation did not invalidate plan authorization")
	}
}

func TestMutationStateSHA256DetectsPolicyAndVDBChanges(t *testing.T) {
	base := t.TempDir()
	vdb := filepath.Join(base, "vdb")
	config := filepath.Join(base, "etc-portage")
	repo := filepath.Join(base, "repo")
	world := filepath.Join(base, "world")
	for _, directory := range []string{filepath.Join(vdb, "cat", "pkg-1"), config, filepath.Join(repo, "media-sound", "apulse"), filepath.Join(repo, "eclass")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(vdb, "cat", "pkg-1", "SLOT"):                           "0\n",
		filepath.Join(config, "make.conf"):                                   "USE=\"-test\"\n",
		filepath.Join(repo, "media-sound", "apulse", "apulse-0.1.14.ebuild"): "EAPI=8\n",
		filepath.Join(repo, "eclass", "cmake.eclass"):                        "EXPORT_FUNCTIONS src_configure\n",
		world: "app-shells/bash\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	action := resolve.PkgAction{Atom: planTestAtom(t, "media-sound/apulse-0.1.14"), Repository: "gentoo", RepositoryPath: repo}
	first, err := mutationStateSHA256(vdb, world, config, []resolve.PkgAction{action})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "make.conf"), []byte("USE=\"test\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := mutationStateSHA256(vdb, world, config, []resolve.PkgAction{action})
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("policy mutation did not change state fingerprint")
	}
}

func TestValidatePlanAuthorizationRequiresExactDigest(t *testing.T) {
	digest := strings.Repeat("a", 64)
	if err := validatePlanAuthorization("", digest); err == nil {
		t.Fatal("missing digest accepted")
	}
	if err := validatePlanAuthorization(strings.Repeat("b", 64), digest); err == nil {
		t.Fatal("mismatched digest accepted")
	}
	if err := validatePlanAuthorization(digest, digest); err != nil {
		t.Fatal(err)
	}
}

func TestRequestedPlanAuthorizationRejectsStaleSavedPlanDuringPreflight(t *testing.T) {
	directory := t.TempDir()
	document := []byte(`{"complete":true,"operation":"install","plan_sha256":"` + strings.Repeat("a", 64) + `","resolution":{"verified":true,"verification":"verified"}}`)
	if _, err := savePlanDocument("stale", directory, document); err != nil {
		t.Fatal(err)
	}
	result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified}
	err := requestedPlanAuthorizationError("", "stale", directory, []string{"@world"}, resolve.DefaultResolveConfig(), result, strings.Repeat("b", 64))
	if err == nil || !strings.Contains(err.Error(), "does not match current verified plan") {
		t.Fatalf("stale preflight authorization error = %v", err)
	}
}

func TestRequestedPlanAuthorizationAcceptsMatchingReadOnlyAudit(t *testing.T) {
	directory := t.TempDir()
	result := &resolve.ResolveResult{Verified: true, Verification: resolve.VerificationVerified}
	cfg := resolve.DefaultResolveConfig()
	state := strings.Repeat("b", 64)
	digest := canonicalPlanSHA256([]string{"@world"}, cfg, result, state)
	document := []byte(`{"complete":true,"operation":"update","plan_sha256":"` + digest + `","resolution":{"verified":true,"verification":"verified"}}`)
	if _, err := savePlanDocument("matching", directory, document); err != nil {
		t.Fatal(err)
	}
	if err := requestedPlanAuthorizationError("", "matching", directory, []string{"@world"}, cfg, result, state); err != nil {
		t.Fatalf("matching read-only approval rejected: %v", err)
	}
}

func TestSavedPlanNameAndPathResolveToDigest(t *testing.T) {
	directory := t.TempDir()
	digest := strings.Repeat("a", 64)
	document := []byte(`{"complete":true,"resolution":{"verified":true,"verification":"verified"},"plan_sha256":"` + digest + `"}`)
	path, err := savePlanDocument("weekly-upgrade", directory, document)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(directory, "weekly-upgrade.json") {
		t.Fatalf("saved path=%s", path)
	}
	for _, reference := range []string{"weekly-upgrade", path} {
		got, err := approvedPlanDigest("", reference, directory)
		if err != nil || got != digest {
			t.Fatalf("approvedPlanDigest(%q)=%q, %v", reference, got, err)
		}
	}
	if _, err := approvedPlanDigest(digest, "weekly-upgrade", directory); err == nil {
		t.Fatal("simultaneous digest and saved-plan approval accepted")
	}
}

func TestSavedPlanNameContainingVersionDotGetsJSONSuffix(t *testing.T) {
	directory := t.TempDir()
	path, err := resolvePlanPath("libsoup-slot-2.4-upgrade", directory)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(directory, "libsoup-slot-2.4-upgrade.json")
	if path != want {
		t.Fatalf("resolved path=%q, want %q", path, want)
	}
}

func TestApprovedPlanRejectsIncompleteDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "incomplete.json")
	if err := os.WriteFile(path, []byte(`{"complete":false,"plan_sha256":"`+strings.Repeat("a", 64)+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := approvedPlanDigest("", path, t.TempDir()); err == nil {
		t.Fatal("incomplete saved plan accepted")
	}
}

func TestDescribeApprovedPlanDifferenceNamesOneshot(t *testing.T) {
	directory := t.TempDir()
	document := []byte(`{"complete":true,"operation":"install","plan_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","resolution":{"verified":true,"verification":"verified"},"options":{"backtrack":20}}`)
	if _, err := savePlanDocument("option-test", directory, document); err != nil {
		t.Fatal(err)
	}
	detail := describeApprovedPlanDifference("option-test", directory, resolve.ResolveConfig{Backtrack: 20, Oneshot: true})
	if !strings.Contains(detail, "oneshot (saved=false, current=true)") {
		t.Fatalf("difference = %q", detail)
	}
}
