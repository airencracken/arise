package portage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/gentooling"
)

func profileInspectionFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	config := filepath.Join(root, "etc", "portage")
	repo := filepath.Join(root, "repos", "gentoo")
	base, leaf := filepath.Join(repo, "profiles", "base"), filepath.Join(repo, "profiles", "leaf")
	for _, path := range []string{config, base, leaf} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeProfileFixture(t, filepath.Join(config, "repos.conf"), "[gentoo]\nlocation = "+repo+"\n")
	writeProfileFixture(t, filepath.Join(leaf, "parent"), "../base\n")
	if err := os.Symlink(leaf, filepath.Join(config, "make.profile")); err != nil {
		t.Fatal(err)
	}
	return config, base, leaf
}

func writeProfileFixture(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func reportDefault(t *testing.T, report ProfileReport, name string) ProfileDefault {
	t.Helper()
	for _, value := range report.Defaults {
		if value.Name == name {
			return value
		}
	}
	t.Fatalf("missing default %s", name)
	return ProfileDefault{}
}

func reportPolicy(t *testing.T, report ProfileReport, name string) ProfilePolicy {
	t.Helper()
	for _, policy := range report.Policies {
		if policy.Name == name {
			return policy
		}
	}
	t.Fatalf("missing policy %s", name)
	return ProfilePolicy{}
}

func TestInspectProfileDefaultsAndProvenance(t *testing.T) {
	config, base, leaf := profileInspectionFixture(t)
	writeProfileFixture(t, filepath.Join(base, "make.defaults"), "# defaults\nCFLAGS=\"-O1\"\nUSE=\"ssl old\"\nFEATURES=\"sandbox test\"\nUSE_EXPAND=\"ABI_X86\"\nABI_X86=\"64\"\n")
	writeProfileFixture(t, filepath.Join(leaf, "make.defaults"), "# overrides\nCFLAGS=\"-O2\"\nUSE=\"${USE} -old \\\nnew\"\nFEATURES=\"-test\"\nUSE_EXPAND=\"-ABI_X86 LLVM_TARGETS\"\nLLVM_TARGETS=\"X86\"\nCOPY=\"${CFLAGS}\"\n")
	writeProfileFixture(t, filepath.Join(config, "make.conf"), "CFLAGS=\"-O0\"\nUSE=\"user\"\n")
	t.Setenv("USE", "environment")
	t.Setenv("CFLAGS", "-O3")
	report, err := InspectProfile(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"CFLAGS": "-O2", "COPY": "-O2", "USE": "ssl -old new",
		"FEATURES": "sandbox", "USE_EXPAND": "LLVM_TARGETS", "LLVM_TARGETS": "X86",
	} {
		if got := reportDefault(t, report, name).Value; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	use := reportDefault(t, report, "USE")
	if len(use.Contributions) != 2 || use.Contributions[1].Source.Line != 3 || use.Contributions[1].Value != "${USE} -old new" {
		t.Fatalf("USE provenance: %+v", use)
	}
	if got := reportDefault(t, report, "FEATURES").Contributions[1].Source.Line; got != 5 {
		t.Fatalf("line after continuation = %d, want 5", got)
	}
	flags := reportDefault(t, report, "CFLAGS").Contributions
	if len(flags) != 2 || flags[0].Source.Path != filepath.Join(base, "make.defaults") || flags[1].Value != "-O2" {
		t.Fatalf("scalar history = %+v", flags)
	}
	if report.Schema != "arise.profile.v1" || report.Scope != "profile" || report.ActivePath != leaf || len(report.Layers) != 2 {
		t.Fatalf("report identity = %+v", report)
	}
	if !reflect.DeepEqual(report.Layers[1].Parents, []string{"../base"}) {
		t.Fatalf("parent references = %v", report.Layers)
	}
	// Repeated observations have an identical JSON contract and order.
	again, err := InspectProfile(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(again)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("report is nondeterministic")
	}
}

func TestInspectProfilePolicyRemovalsStableRulesAndUnsupportedFiles(t *testing.T) {
	config, base, leaf := profileInspectionFixture(t)
	for file, data := range map[string]string{
		"packages":         "# system\n*sys-apps/base\n*sys-apps/removed\napp-misc/non-system\n",
		"package.provided": "sys-libs/provided-1\n",
		"use.force":        "ssl\nremoved\n", "use.mask": "masked\n",
		"use.stable.force": "stable\n", "use.stable.mask": "unstable\n",
		"package.mask":   "# masked reason\n=cat/pkg-1\n=cat/pkg-2\n",
		"package.unmask": "=cat/other-1\n",
		"package.use":    "cat/pkg ssl\n", "package.use.stable.force": "cat/pkg stable\n",
		"package.use.stable.mask": "cat/pkg unstable\n",
	} {
		writeProfileFixture(t, filepath.Join(base, file), data)
	}
	for file, data := range map[string]string{
		"packages":         "-*sys-apps/removed\n*sys-apps/new\n",
		"package.provided": "-sys-libs/provided-1\nsys-libs/provided-2\n",
		"use.force":        "-removed\n", "use.mask": "-masked\nnewmask\n",
		"package.mask": "-=cat/pkg-1\n", "package.unmask": "-=cat/other-1\n",
		"package.use":    "cat/pkg -ssl\n=cat/pkg-2 ssl\n",
		"profile.bashrc": "touch should-never-run\n",
	} {
		writeProfileFixture(t, filepath.Join(leaf, file), data)
	}
	report, err := InspectProfile(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string][]string{
		"packages":         {"sys-apps/base", "sys-apps/new"},
		"package.provided": {"sys-libs/provided-2"}, "use.force": {"ssl"}, "use.mask": {"newmask"},
		"use.stable.force": {"stable"}, "use.stable.mask": {"unstable"},
		"package.mask": {"=cat/pkg-2"}, "package.unmask": {},
		"package.use":              {"cat/pkg ssl", "cat/pkg -ssl", "=cat/pkg-2 ssl"},
		"package.use.stable.force": {"cat/pkg stable"}, "package.use.stable.mask": {"cat/pkg unstable"},
	} {
		if got := reportPolicy(t, report, name).Values; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	mask := reportPolicy(t, report, "package.mask")
	if len(mask.Contributions) != 3 || mask.Contributions[0].Source.Line != 2 || mask.Contributions[2].Value != "-=cat/pkg-1" {
		t.Fatalf("mask provenance = %+v", mask)
	}
	if reportPolicy(t, report, "package.use").Kind != "ordered_rules" {
		t.Fatal("package USE is presented as an effective result")
	}
	if !reflect.DeepEqual(report.Unsupported, []string{filepath.Join(leaf, "profile.bashrc")}) {
		t.Fatalf("unsupported files = %v", report.Unsupported)
	}
}

func TestInspectProfileCrossRepositoryDiamond(t *testing.T) {
	config, base, leaf := profileInspectionFixture(t)
	overlay := filepath.Join(t.TempDir(), "custom-location")
	right := filepath.Join(overlay, "profiles", "right")
	if err := os.MkdirAll(right, 0o755); err != nil {
		t.Fatal(err)
	}
	gentoo := filepath.Dir(filepath.Dir(base))
	writeProfileFixture(t, filepath.Join(config, "repos.conf"), "[gentoo]\nlocation = "+gentoo+"\n[overlay]\nlocation = "+overlay+"\n")
	writeProfileFixture(t, filepath.Join(right, "parent"), "gentoo:base\n")
	writeProfileFixture(t, filepath.Join(leaf, "parent"), "../base\noverlay:right\n")
	writeProfileFixture(t, filepath.Join(base, "make.defaults"), "USE=\"base\"\n")
	writeProfileFixture(t, filepath.Join(right, "make.defaults"), "USE=\"right\"\n")
	writeProfileFixture(t, filepath.Join(leaf, "make.defaults"), "USE=\"leaf\"\n")
	report, err := InspectProfile(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Layers) != 3 || report.Layers[0].Path != base || report.Layers[1].Path != right || report.Layers[2].Path != leaf {
		t.Fatalf("diamond order = %+v", report.Layers)
	}
	if got := reportDefault(t, report, "USE"); got.Value != "base right leaf" || len(got.Contributions) != 3 {
		t.Fatalf("diamond contributions = %+v", got)
	}
}

func TestInspectProfileRejectsInvalidEvidence(t *testing.T) {
	for _, scenario := range []string{"missing", "cycle", "escape", "unknown-repository", "symlink-policy", "symlink-mask", "malformed-rule"} {
		t.Run(scenario, func(t *testing.T) {
			config, base, leaf := profileInspectionFixture(t)
			switch scenario {
			case "missing":
				if err := os.Remove(filepath.Join(config, "make.profile")); err != nil {
					t.Fatal(err)
				}
			case "cycle":
				writeProfileFixture(t, filepath.Join(base, "parent"), "../leaf\n")
			case "escape":
				writeProfileFixture(t, filepath.Join(leaf, "parent"), "../../outside\n")
			case "unknown-repository":
				writeProfileFixture(t, filepath.Join(leaf, "parent"), "unknown:base\n")
			case "symlink-policy":
				if err := os.Symlink(filepath.Join(base, "packages"), filepath.Join(leaf, "packages")); err != nil {
					t.Fatal(err)
				}
			case "symlink-mask":
				writeProfileFixture(t, filepath.Join(base, "package.mask"), "cat/pkg\n")
				if err := os.Symlink(filepath.Join(base, "package.mask"), filepath.Join(leaf, "package.mask")); err != nil {
					t.Fatal(err)
				}
			case "malformed-rule":
				writeProfileFixture(t, filepath.Join(leaf, "package.use"), "cat/pkg\n")
			}
			report, err := InspectProfile(t.Context(), config)
			if err == nil || report.Schema != "" {
				t.Fatalf("invalid evidence accepted: %+v, %v", report, err)
			}
			if scenario == "cycle" && !errors.Is(err, gentooling.ErrProfileCycle) {
				t.Fatalf("cycle error = %v", err)
			}
		})
	}
}

func TestInspectProfileMaskDirectoryOrderAndSources(t *testing.T) {
	config, base, leaf := profileInspectionFixture(t)
	directory := filepath.Join(base, "package.mask")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProfileFixture(t, filepath.Join(directory, "20-remove"), "-cat/pkg\ncat/final\n")
	writeProfileFixture(t, filepath.Join(directory, "10-add"), "# first\ncat/pkg\n")
	writeProfileFixture(t, filepath.Join(directory, ".ignored"), "cat/ignored\n")
	writeProfileFixture(t, filepath.Join(leaf, "package.mask"), "-cat/final\ncat/leaf\n")
	report, err := InspectProfile(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	policy := reportPolicy(t, report, "package.mask")
	if !reflect.DeepEqual(policy.Values, []string{"cat/leaf"}) || len(policy.Contributions) != 5 {
		t.Fatalf("mask directory policy = %+v", policy)
	}
	if first := policy.Contributions[0]; first.Source.Path != filepath.Join(directory, "10-add") || first.Source.Line != 2 {
		t.Fatalf("directory provenance = %+v", first)
	}
}

func TestInspectProfileCancelledAndResetDefaults(t *testing.T) {
	config, base, leaf := profileInspectionFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := InspectProfile(ctx, config); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled inspection = %v", err)
	}
	writeProfileFixture(t, filepath.Join(base, "make.defaults"), "USE=\"old ssl\"\n")
	writeProfileFixture(t, filepath.Join(leaf, "make.defaults"), "USE=\"-* fresh\"\nUSE=\"-fresh final\"\n")
	report, err := InspectProfile(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	value := reportDefault(t, report, "USE")
	if value.Value != "-fresh final" || len(value.Contributions) != 3 {
		t.Fatalf("reset and repeated assignments = %+v", value)
	}
	if strings.Contains(value.Value, "old") {
		t.Fatal("reset retained an inherited flag")
	}
}
