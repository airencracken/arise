package fsrollback

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func fixtureMounts() []Mount {
	return []Mount{
		{Path: "/", Source: "/dev/mapper/root", Filesystem: "btrfs", StableID: "root-uuid"},
		{Path: "/boot", Source: "/dev/nvme0n1p1", Filesystem: "vfat", StableID: "boot-uuid"},
		{Path: "/var", Source: "tank/var", Filesystem: "zfs", StableID: "var-guid"},
		{Path: "/var/db/repos", Source: "/dev/mapper/repos", Filesystem: "ext4", StableID: "repos-uuid"},
	}
}

func TestCoverageEnumeratesNestedBoundariesAndExclusions(t *testing.T) {
	coverage, err := EvaluateCoverage(fixtureMounts(), []string{"/", "/var/db/pkg", "/etc/portage"}, func(m Mount) bool {
		return m.Filesystem == "btrfs" || m.Filesystem == "zfs"
	})
	if err != nil {
		t.Fatal(err)
	}
	if coverage.Eligible() {
		t.Fatal("coverage with unsupported nested mounts was eligible")
	}
	if got, want := mountPaths(coverage.Boundaries), []string{"/", "/var"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("boundaries = %v, want %v", got, want)
	}
	if got, want := mountPaths(coverage.Excluded), []string{"/boot", "/var/db/repos"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("excluded = %v, want %v", got, want)
	}
}

func TestCoverageUsesLongestContainingBoundary(t *testing.T) {
	coverage, err := EvaluateCoverage(fixtureMounts(), []string{"/var/db/pkg"}, func(m Mount) bool {
		return m.Filesystem == "zfs"
	})
	if err != nil {
		t.Fatal(err)
	}
	if !coverage.Eligible() {
		t.Fatalf("coverage unexpectedly ineligible: %+v", coverage)
	}
	if got := mountPaths(coverage.Boundaries); !reflect.DeepEqual(got, []string{"/var"}) {
		t.Fatalf("boundaries = %v", got)
	}
}

func TestPropertyCoverageIsIndependentOfMountAndRequirementOrder(t *testing.T) {
	mounts := fixtureMounts()
	required := []string{"/etc/portage", "/var/db/pkg", "/"}
	want, err := EvaluateCoverage(mounts, required, func(Mount) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	random := rand.New(rand.NewSource(42))
	for iteration := 0; iteration < 100; iteration++ {
		shuffledMounts := append([]Mount(nil), mounts...)
		shuffledRequired := append([]string(nil), required...)
		random.Shuffle(len(shuffledMounts), func(i, j int) { shuffledMounts[i], shuffledMounts[j] = shuffledMounts[j], shuffledMounts[i] })
		random.Shuffle(len(shuffledRequired), func(i, j int) { shuffledRequired[i], shuffledRequired[j] = shuffledRequired[j], shuffledRequired[i] })
		got, err := EvaluateCoverage(shuffledMounts, shuffledRequired, func(Mount) bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d changed coverage:\n got=%+v\nwant=%+v", iteration, got, want)
		}
	}
}

func TestAdversarialCoverageRejectsMalformedTopology(t *testing.T) {
	cases := []struct {
		name     string
		mounts   []Mount
		required []string
		contains string
	}{
		{"empty topology", nil, []string{"/"}, "empty"},
		{"relative required", fixtureMounts(), []string{"var/db/pkg"}, "absolute"},
		{"relative mount", []Mount{{Path: "var", Source: "x", Filesystem: "btrfs", StableID: "id"}}, []string{"/"}, "absolute"},
		{"missing identity", []Mount{{Path: "/", Source: "x", Filesystem: "btrfs"}}, []string{"/"}, "stable identity"},
		{"duplicate normalized path", []Mount{
			{Path: "/var", Source: "a", Filesystem: "btrfs", StableID: "a"},
			{Path: "/var/.", Source: "b", Filesystem: "btrfs", StableID: "b"},
		}, []string{"/var"}, "duplicate"},
		{"prefix confusion", []Mount{{Path: "/var", Source: "a", Filesystem: "btrfs", StableID: "a"}}, []string{"/variant"}, "no mount"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := EvaluateCoverage(tc.mounts, tc.required, func(Mount) bool { return true })
			if err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("error = %v, want substring %q", err, tc.contains)
			}
		})
	}
}

func mountPaths(mounts []Mount) []string {
	result := make([]string, len(mounts))
	for i, mount := range mounts {
		result[i] = mount.Path
	}
	return result
}
