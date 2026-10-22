package portage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func profileComparisonFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	config, base, leaf := profileInspectionFixture(t)
	target := filepath.Join(filepath.Dir(leaf), "alternate")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProfileFixture(t, filepath.Join(base, "make.defaults"), "ARCH=amd64\nCFLAGS=\"-O1\"\nUSE=\"ssl old\"\n")
	writeProfileFixture(t, filepath.Join(leaf, "make.defaults"), "CFLAGS=\"-O2\"\nUSE=\"-old\"\nEMPTY=\"\"\n")
	writeProfileFixture(t, filepath.Join(target, "parent"), "../base\n")
	writeProfileFixture(t, filepath.Join(target, "make.defaults"), "CFLAGS=\"-O3\"\nUSE=\"-ssl new\"\nADDED=\"\"\n")
	writeProfileFixture(t, filepath.Join(leaf, "packages"), "*sys-apps/old\n")
	writeProfileFixture(t, filepath.Join(target, "packages"), "*sys-apps/new\n")
	writeProfileFixture(t, filepath.Join(leaf, "use.mask"), "masked\n")
	writeProfileFixture(t, filepath.Join(target, "use.force"), "forced\n")
	return config, base, leaf, target
}

func TestCompareProfilesResolvesSelectorsAndPreservesActiveSelection(t *testing.T) {
	config, _, leaf, target := profileComparisonFixture(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("USE", "ignored-environment")
	writeProfileFixture(t, filepath.Join(config, "make.conf"), "USE=\"ignored-user\"\n")
	for _, selector := range []string{"gentoo:alternate", "alternate", target} {
		diff, err := CompareProfiles(t.Context(), config, "active", selector)
		if err != nil {
			t.Fatal(err)
		}
		if diff.Schema != "arise.profile-diff.v1" || diff.Scope != "profile" || diff.Before.Path != leaf || diff.After.Path != target {
			t.Fatalf("diff identity = %+v", diff)
		}
		if len(diff.Defaults) != 4 || len(diff.Policies) != 3 {
			t.Fatalf("diff changed unexpected defaults or policy: %+v", diff)
		}
		changes := make(map[string]ProfileDefaultChange)
		for _, change := range diff.Defaults {
			changes[change.Name] = change
		}
		if changes["ADDED"].Before != nil || changes["ADDED"].After == nil || changes["ADDED"].After.Value != "" {
			t.Fatalf("unset versus empty lost: %+v", changes["ADDED"])
		}
		if changes["EMPTY"].After != nil || changes["EMPTY"].Before == nil || changes["EMPTY"].Before.Value != "" {
			t.Fatalf("empty versus unset lost: %+v", changes["EMPTY"])
		}
		if changes["USE"].Kind != "tokens" || changes["USE"].After.Value != "-ssl old new" || len(changes["CFLAGS"].After.Contributions) != 2 {
			t.Fatalf("defaults or provenance: %+v", changes)
		}
		current, err := os.Readlink(filepath.Join(config, "make.profile"))
		if err != nil || current != leaf {
			t.Fatalf("active selection changed: %q, %v", current, err)
		}
		entries, err := os.ReadDir(tmp)
		if err != nil || len(entries) != 0 {
			t.Fatalf("temporary adapter left files behind: %v, %v", entries, err)
		}
	}
	// An explicit pair works even when there is no active selection.
	if err := os.Remove(filepath.Join(config, "make.profile")); err != nil {
		t.Fatal(err)
	}
	if _, err := CompareProfiles(t.Context(), config, "gentoo:leaf", "gentoo:alternate"); err != nil {
		t.Fatalf("explicit pair depended on active profile: %v", err)
	}
}

func TestCompareProfilesPreservesRuleOrderButIgnoresSetAndWhitespaceOrder(t *testing.T) {
	before := ProfileReport{
		Defaults: []ProfileDefault{{Name: "USE", Value: "ssl -debug"}},
		Policies: []ProfilePolicy{
			{Name: "use.mask", Kind: "effective", Values: []string{"a", "b"}},
			{Name: "package.use", Kind: "ordered_rules", Values: []string{"cat/pkg ssl", "cat/pkg -ssl"}},
		},
	}
	after := before
	after.Defaults = []ProfileDefault{{Name: "USE", Value: "-debug ssl"}}
	after.Policies = []ProfilePolicy{
		{Name: "use.mask", Kind: "effective", Values: []string{"b", "a", "a"}},
		{Name: "package.use", Kind: "ordered_rules", Values: []string{"cat/pkg\tssl", "cat/pkg   -ssl"}},
	}
	if diff := compareProfileReports(before, after); len(diff.Defaults) != 0 || len(diff.Policies) != 0 {
		t.Fatalf("cosmetic changes reported: %+v", diff)
	}
	after.Policies[1].Values = []string{"cat/pkg -ssl", "cat/pkg ssl"}
	diff := compareProfileReports(before, after)
	if len(diff.Policies) != 1 || diff.Policies[0].Name != "package.use" {
		t.Fatalf("rule reordering was missed: %+v", diff)
	}
	if !reflect.DeepEqual(diff.Policies[0].Before, []string{"cat/pkg ssl", "cat/pkg -ssl"}) || !reflect.DeepEqual(diff.Policies[0].After, []string{"cat/pkg -ssl", "cat/pkg ssl"}) {
		t.Fatalf("rule sequence lost: %+v", diff.Policies)
	}
}

func TestCompareProfilesDetectsOrderSensitiveLicenseChanges(t *testing.T) {
	before := ProfileReport{Defaults: []ProfileDefault{{Name: "ACCEPT_LICENSE", Value: "* -GPL-2"}}}
	after := ProfileReport{Defaults: []ProfileDefault{{Name: "ACCEPT_LICENSE", Value: "-GPL-2 *"}}}
	diff := compareProfileReports(before, after)
	if len(diff.Defaults) != 1 || diff.Defaults[0].Kind != "value" {
		t.Fatalf("order-sensitive license expression was hidden: %+v", diff.Defaults)
	}
}

func TestCompareProfilesEqualityAndReverseComparison(t *testing.T) {
	config, _, _, _ := profileComparisonFixture(t)
	same, err := CompareProfiles(t.Context(), config, "active", "active")
	if err != nil || len(same.Defaults) != 0 || len(same.Policies) != 0 || !reflect.DeepEqual(same.Before, same.After) {
		t.Fatalf("equal profiles differ: %+v, %v", same, err)
	}
	forward, err := CompareProfiles(t.Context(), config, "active", "gentoo:alternate")
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := CompareProfiles(t.Context(), config, "gentoo:alternate", "active")
	if err != nil {
		t.Fatal(err)
	}
	if len(forward.Defaults) != len(reverse.Defaults) || len(forward.Policies) != len(reverse.Policies) {
		t.Fatal("reversing comparison changed the set of differences")
	}
	for index, change := range forward.Defaults {
		if reverse.Defaults[index].Name != change.Name || !reflect.DeepEqual(reverse.Defaults[index].Before, change.After) || !reflect.DeepEqual(reverse.Defaults[index].After, change.Before) {
			t.Fatalf("reverse default change lost symmetry: %+v", reverse.Defaults[index])
		}
	}
	for index, change := range forward.Policies {
		if reverse.Policies[index].Name != change.Name || !reflect.DeepEqual(reverse.Policies[index].Before, change.After) || !reflect.DeepEqual(reverse.Policies[index].After, change.Before) {
			t.Fatalf("reverse policy change lost symmetry: %+v", reverse.Policies[index])
		}
	}
}

func TestCompareProfilesRejectsInvalidTargetsAndCleansTemporaryLinks(t *testing.T) {
	for _, selector := range []string{"missing:alternate", "gentoo:../leaf", "gentoo:/absolute", "gentoo:missing", "gentoo:", "../leaf", "/tmp/not-a-profile"} {
		config, _, _, _ := profileComparisonFixture(t)
		if _, err := CompareProfiles(t.Context(), config, "active", selector); err == nil {
			t.Errorf("accepted invalid target %q", selector)
		}
	}
	config, _, _, target := profileComparisonFixture(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	writeProfileFixture(t, filepath.Join(target, "parent"), "../alternate\n")
	if _, err := CompareProfiles(t.Context(), config, "active", "gentoo:alternate"); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("invalid candidate graph accepted: %v", err)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed inspection leaked adapter: %v, %v", entries, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := CompareProfiles(ctx, config, "active", "gentoo:alternate"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestProfileSelectorsRejectSymlinkEscapesAndAmbiguousNames(t *testing.T) {
	config, _, leaf, _ := profileComparisonFixture(t)
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(filepath.Dir(leaf), "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := CompareProfiles(t.Context(), config, "active", "gentoo:escape/child"); err == nil {
		t.Fatal("accepted a candidate outside the repository through an ancestor symlink")
	}
	overlay := t.TempDir()
	if err := os.MkdirAll(filepath.Join(overlay, "profiles", "alternate"), 0o755); err != nil {
		t.Fatal(err)
	}
	repository := filepath.Dir(filepath.Dir(leaf))
	writeProfileFixture(t, filepath.Join(config, "repos.conf"), "[gentoo]\nlocation = "+repository+"\n[overlay]\nlocation = "+overlay+"\n")
	if _, err := CompareProfiles(t.Context(), config, "active", "alternate"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous target = %v", err)
	}
	if _, err := CompareProfiles(t.Context(), config, "active", "overlay:alternate"); err != nil {
		t.Fatalf("qualified target failed: %v", err)
	}
}

func TestListProfilesFiltersArchitectureAndRetainsRepositoryMetadata(t *testing.T) {
	config, _, leaf, target := profileComparisonFixture(t)
	root := filepath.Dir(leaf)
	writeProfileFixture(t, filepath.Join(root, "profiles.desc"), "# advertised\namd64 leaf stable\namd64 alternate custom-status # comment\narm64 alternate exp\n")
	writeProfileFixture(t, filepath.Join(target, "deprecated"), "leaf\nChoose another profile.\n")
	listed, err := ListProfiles(t.Context(), config, false)
	if err != nil {
		t.Fatal(err)
	}
	if listed.Schema != "arise.profile-catalog.v1" || listed.Arch != "amd64" || len(listed.Profiles) != 2 {
		t.Fatalf("catalog = %+v", listed)
	}
	if entry := listed.Profiles[0]; entry.Selector != "gentoo:alternate" || !entry.Deprecated || entry.Status != "custom-status" || entry.Source.Line != 3 {
		t.Fatalf("candidate metadata = %+v", entry)
	}
	if !listed.Profiles[1].Selected || listed.Profiles[1].Path != leaf {
		t.Fatalf("active profile not marked: %+v", listed)
	}
	listed, err = ListProfiles(t.Context(), config, true)
	if err != nil || len(listed.Profiles) != 3 || !listed.All {
		t.Fatalf("all architectures = %+v, %v", listed, err)
	}
	writeProfileFixture(t, filepath.Join(root, "profiles.desc"), "amd64 ../escape stable\n")
	if _, err := ListProfiles(t.Context(), config, false); err == nil {
		t.Fatal("catalog accepted path traversal")
	}
	writeProfileFixture(t, filepath.Join(root, "profiles.desc"), "amd64 leaf\n")
	if _, err := ListProfiles(t.Context(), config, false); err == nil {
		t.Fatal("catalog accepted malformed description")
	}
}
