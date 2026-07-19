package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/resolve"
)

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

func TestValidatePlanAuthorizationRequiresBothControlsAndExactDigest(t *testing.T) {
	digest := strings.Repeat("a", 64)
	if err := validatePlanAuthorization(false, "", digest); err != nil {
		t.Fatal(err)
	}
	if err := validatePlanAuthorization(true, "", digest); err == nil {
		t.Fatal("missing digest accepted")
	}
	if err := validatePlanAuthorization(false, digest, digest); err == nil {
		t.Fatal("digest without canary flag accepted")
	}
	if err := validatePlanAuthorization(true, strings.Repeat("b", 64), digest); err == nil {
		t.Fatal("mismatched digest accepted")
	}
	if err := validatePlanAuthorization(true, digest, digest); err != nil {
		t.Fatal(err)
	}
}
