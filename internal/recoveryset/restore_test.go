package recoveryset

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/binpkg"
	"github.com/airencracken/arise/internal/merge"
)

func TestInspectRestoreUsesReverseCaptureOrder(t *testing.T) {
	base := t.TempDir()
	sourceRoot := filepath.Join(base, "source")
	first := createInstalledFixture(t, base, sourceRoot, "sys-devel", "first", "1", "usr/bin/first")
	second := createInstalledFixture(t, base, sourceRoot, "app-misc", "second", "2", "usr/bin/second")
	publish := validRequest(base, sourceRoot, []Package{{VDBEntryPath: first}, {VDBEntryPath: second}})
	setPath, err := Publish(context.Background(), publish)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := InspectRestore(RestoreRequest{
		SetPath: setPath, CurrentConfiguration: publish.ConfigurationFingerprint,
		CurrentRepository: publish.RepositoryFingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Drift) != 0 || plan.DriftApprovalSHA256 != "" {
		t.Fatalf("matching restore reported drift: %+v", plan)
	}
	if len(plan.Artifacts) != 2 || plan.Artifacts[0].CaptureOrder != 1 || plan.Artifacts[1].CaptureOrder != 0 {
		t.Fatalf("restore order = %+v", plan.Artifacts)
	}
}

func TestRestoreRequiresExactDriftApproval(t *testing.T) {
	base := t.TempDir()
	sourceRoot := filepath.Join(base, "source")
	vdb := createInstalledFixture(t, base, sourceRoot, "sys-devel", "first", "1", "usr/bin/first")
	publish := validRequest(base, sourceRoot, []Package{{VDBEntryPath: vdb}})
	setPath, err := Publish(context.Background(), publish)
	if err != nil {
		t.Fatal(err)
	}
	request := RestoreRequest{
		SetPath: setPath, RootDir: filepath.Join(base, "restore"),
		VDBDir: filepath.Join(base, "restore-vdb"), JournalDir: filepath.Join(base, "journal"),
		CurrentConfiguration: binpkg.InputFingerprint{
			Scope: "portage-configuration", SHA256: strings.Repeat("d", 64), Complete: true,
		},
		CurrentRepository: publish.RepositoryFingerprint,
		Merge: func(context.Context, string, merge.MergeConfig) error {
			t.Fatal("merge started without drift approval")
			return nil
		},
	}
	plan, err := InspectRestore(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Drift) != 1 || len(plan.DriftApprovalSHA256) != 64 {
		t.Fatalf("drift plan = %+v", plan)
	}
	if err := Restore(context.Background(), request); err == nil || !strings.Contains(err.Error(), "requires approval") {
		t.Fatalf("Restore() unapproved drift error = %v", err)
	}
	request.ApprovedDriftSHA256 = strings.Repeat("0", 64)
	if err := Restore(context.Background(), request); err == nil || !strings.Contains(err.Error(), "requires approval") {
		t.Fatalf("Restore() wrong approval error = %v", err)
	}
}

func TestRestoreMergesCompleteSetAndRebuildsVDB(t *testing.T) {
	base := t.TempDir()
	sourceRoot := filepath.Join(base, "source")
	first := createInstalledFixture(t, base, sourceRoot, "sys-devel", "first", "1", "usr/bin/first")
	second := createInstalledFixture(t, base, sourceRoot, "app-misc", "second", "2", "usr/bin/second")
	publish := validRequest(base, sourceRoot, []Package{{VDBEntryPath: first}, {VDBEntryPath: second}})
	setPath, err := Publish(context.Background(), publish)
	if err != nil {
		t.Fatal(err)
	}
	restoreRoot := filepath.Join(base, "restore")
	request := RestoreRequest{
		SetPath: setPath, RootDir: restoreRoot,
		VDBDir: filepath.Join(restoreRoot, "var/db/pkg"), JournalDir: filepath.Join(base, "journal"),
		CurrentConfiguration: publish.ConfigurationFingerprint,
		CurrentRepository:    publish.RepositoryFingerprint,
	}
	if err := Restore(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(restoreRoot, "usr/bin/first"),
		filepath.Join(restoreRoot, "usr/bin/second"),
		filepath.Join(request.VDBDir, "sys-devel/first-1", "CONTENTS"),
		filepath.Join(request.VDBDir, "app-misc/second-2", "EAPI"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("restored path %s: %v", path, err)
		}
	}
	status, err := ReadStatus(setPath)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != StatusPendingVerification {
		t.Fatalf("restore status = %+v", status)
	}
}

func TestRestoreFailureStopsAtBoundaryAndRemainsPendingRollback(t *testing.T) {
	base := t.TempDir()
	sourceRoot := filepath.Join(base, "source")
	first := createInstalledFixture(t, base, sourceRoot, "sys-devel", "first", "1", "usr/bin/first")
	second := createInstalledFixture(t, base, sourceRoot, "app-misc", "second", "2", "usr/bin/second")
	publish := validRequest(base, sourceRoot, []Package{{VDBEntryPath: first}, {VDBEntryPath: second}})
	setPath, err := Publish(context.Background(), publish)
	if err != nil {
		t.Fatal(err)
	}
	var restored []string
	injected := errors.New("injected merge failure")
	request := RestoreRequest{
		SetPath: setPath, RootDir: filepath.Join(base, "restore"),
		VDBDir: filepath.Join(base, "restore-vdb"), JournalDir: filepath.Join(base, "journal"),
		CurrentConfiguration: publish.ConfigurationFingerprint,
		CurrentRepository:    publish.RepositoryFingerprint,
		Merge: func(_ context.Context, _ string, cfg merge.MergeConfig) error {
			restored = append(restored, cfg.Category+"/"+cfg.Package+"-"+cfg.Version)
			if len(restored) == 2 {
				return injected
			}
			return nil
		},
	}
	if err := Restore(context.Background(), request); !errors.Is(err, injected) {
		t.Fatalf("Restore() error = %v", err)
	}
	if len(restored) != 2 || restored[0] != "app-misc/second-2" || restored[1] != "sys-devel/first-1" {
		t.Fatalf("restore boundaries = %v", restored)
	}
	status, err := ReadStatus(setPath)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != StatusPendingRollback {
		t.Fatalf("failed restore status = %+v", status)
	}
}

func TestRestoreFaultInjectionAtEveryMergeBoundary(t *testing.T) {
	for failAt := 1; failAt <= 3; failAt++ {
		t.Run(fmt.Sprintf("merge-%d", failAt), func(t *testing.T) {
			base := t.TempDir()
			sourceRoot := filepath.Join(base, "source")
			var packages []Package
			for index, name := range []string{"first", "second", "third"} {
				vdb := createInstalledFixture(t, base, sourceRoot, "app-misc", name, fmt.Sprint(index+1), "usr/bin/"+name)
				packages = append(packages, Package{VDBEntryPath: vdb})
			}
			publish := validRequest(base, sourceRoot, packages)
			setPath, err := Publish(context.Background(), publish)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			injected := errors.New("injected merge boundary failure")
			err = Restore(context.Background(), RestoreRequest{
				SetPath: setPath, RootDir: filepath.Join(base, "restore"),
				VDBDir: filepath.Join(base, "restore-vdb"), JournalDir: filepath.Join(base, "journal"),
				CurrentConfiguration: publish.ConfigurationFingerprint,
				CurrentRepository:    publish.RepositoryFingerprint,
				Merge: func(context.Context, string, merge.MergeConfig) error {
					calls++
					if calls == failAt {
						return injected
					}
					return nil
				},
			})
			if !errors.Is(err, injected) || calls != failAt {
				t.Fatalf("Restore() calls=%d error=%v", calls, err)
			}
			status, err := ReadStatus(setPath)
			if err != nil {
				t.Fatal(err)
			}
			if status.Status != StatusPendingRollback {
				t.Fatalf("status after boundary failure = %+v", status)
			}
		})
	}
}

func TestRestoreRejectsApprovalWhenThereIsNoDrift(t *testing.T) {
	base := t.TempDir()
	sourceRoot := filepath.Join(base, "source")
	vdb := createInstalledFixture(t, base, sourceRoot, "sys-devel", "first", "1", "usr/bin/first")
	publish := validRequest(base, sourceRoot, []Package{{VDBEntryPath: vdb}})
	setPath, err := Publish(context.Background(), publish)
	if err != nil {
		t.Fatal(err)
	}
	request := RestoreRequest{
		SetPath: setPath, RootDir: filepath.Join(base, "restore"),
		VDBDir: filepath.Join(base, "restore-vdb"), JournalDir: filepath.Join(base, "journal"),
		CurrentConfiguration: publish.ConfigurationFingerprint,
		CurrentRepository:    publish.RepositoryFingerprint,
		ApprovedDriftSHA256:  strings.Repeat("a", 64),
	}
	if err := Restore(context.Background(), request); err == nil || !strings.Contains(err.Error(), "inputs match") {
		t.Fatalf("Restore() unnecessary approval error = %v", err)
	}
}
