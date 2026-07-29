package planvalidate

import (
	"fmt"
	"testing"
	"time"

	"github.com/airencracken/arise/internal/atom"
)

func TestStateMatchesLargeFinalStatePerformanceGuard(t *testing.T) {
	if testing.Short() {
		t.Skip("performance guard")
	}
	const packageCount = 10_000
	packages := make([]Package, 0, packageCount)
	for index := 0; index < packageCount; index++ {
		packages = append(packages, Package{
			CPV:        fmt.Sprintf("app-bench/package%05d-1", index),
			Slot:       "0",
			Repository: "gentoo",
		})
	}
	constraint, err := atom.ParsePackageAtom("app-bench/package09999")
	if err != nil {
		t.Fatal(err)
	}
	state := newPackageState(packages)

	start := time.Now()
	for iteration := 0; iteration < 1_000; iteration++ {
		if !state.matches(constraint, nil) {
			t.Fatal("matching package disappeared")
		}
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("1,000 large final-state lookups took %s; major validation regression", elapsed)
	}
}
