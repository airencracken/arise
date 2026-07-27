package recoveryset

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishedSetStartsActiveAndStatusTransitionsAtomically(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	vdb := createInstalledFixture(t, base, root, "sys-devel", "first", "1", "usr/bin/first")
	request := validRequest(base, root, []Package{{VDBEntryPath: vdb}})
	path, err := Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	status, err := ReadStatus(path)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != StatusActive || status.SetID != request.SetID {
		t.Fatalf("initial status = %+v", status)
	}
	if err := MarkStatus(path, StatusPendingVerification, ""); err != nil {
		t.Fatal(err)
	}
	status, err = ReadStatus(path)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != StatusPendingVerification {
		t.Fatalf("updated status = %+v", status)
	}
	temporaries, err := filepath.Glob(filepath.Join(path, ".status.json.tmp-*"))
	if err != nil || len(temporaries) != 0 {
		t.Fatalf("status update retained temporaries %v: %v", temporaries, err)
	}
}

func TestStatusSchemaRejectsContradictoryFailureReasons(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	vdb := createInstalledFixture(t, base, root, "sys-devel", "first", "1", "usr/bin/first")
	request := validRequest(base, root, []Package{{VDBEntryPath: vdb}})
	path, err := Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := MarkStatus(path, StatusFailed, ""); err == nil {
		t.Fatal("MarkStatus() accepted failed status without a reason")
	}
	if err := MarkStatus(path, StatusVerified, "failure"); err == nil {
		t.Fatal("MarkStatus() accepted failure reason for verified status")
	}
	if err := MarkStatus(path, StatusName("unknown"), ""); err == nil {
		t.Fatal("MarkStatus() accepted an unknown state")
	}
}

func TestPruneVerifiedPreservesEveryNonCollectibleState(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	vdb := createInstalledFixture(t, base, root, "sys-devel", "first", "1", "usr/bin/first")
	statuses := map[string]StatusName{
		"active": StatusActive, "pending": StatusPendingVerification,
		"failed": StatusFailed, "rollback": StatusPendingRollback,
		"verified-old": StatusVerified, "verified-new": StatusVerified,
	}
	for id, state := range statuses {
		request := validRequest(base, root, []Package{{VDBEntryPath: vdb}})
		request.SetID, request.OperationID = id, "operation-"+id
		path, err := Publish(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		reason := ""
		if state == StatusFailed {
			reason = "injected operation failure"
		}
		if state == StatusVerified {
			if err := MarkStatus(path, StatusPendingVerification, ""); err != nil {
				t.Fatal(err)
			}
			current, err := ReadStatus(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifyAfterReboot(path, current.CaptureBootID+"-next"); err != nil {
				t.Fatal(err)
			}
		} else if err := MarkStatus(path, state, reason); err != nil {
			t.Fatal(err)
		}
	}
	result, err := PruneVerified(filepath.Join(base, "recovery"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 1 || !strings.HasPrefix(result.Removed[0], "verified-") {
		t.Fatalf("prune removed = %v", result.Removed)
	}
	for _, protected := range []string{"active", "pending", "failed", "rollback"} {
		if _, err := os.Stat(filepath.Join(base, "recovery", "sets", protected)); err != nil {
			t.Fatalf("protected set %s was removed: %v", protected, err)
		}
	}
	verifiedRemaining := 0
	for _, id := range []string{"verified-old", "verified-new"} {
		if _, err := os.Stat(filepath.Join(base, "recovery", "sets", id)); err == nil {
			verifiedRemaining++
		}
	}
	if verifiedRemaining != 1 {
		t.Fatalf("verified sets remaining = %d, want 1", verifiedRemaining)
	}
}

func TestPrunePreservesSetsWithMissingOrMalformedStatus(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	vdb := createInstalledFixture(t, base, root, "sys-devel", "first", "1", "usr/bin/first")
	request := validRequest(base, root, []Package{{VDBEntryPath: vdb}})
	path, err := Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "status.json"), []byte("{malformed"), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := PruneVerified(request.Directory, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 0 {
		t.Fatalf("pruned malformed-status set: %v", result.Removed)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("malformed-status set was not preserved: %v", err)
	}
}

func TestPruneRejectsNegativeRetention(t *testing.T) {
	if _, err := PruneVerified(t.TempDir(), -1); err == nil {
		t.Fatal("PruneVerified() accepted negative retention")
	}
}

func TestPruneCollectsOnlyObjectsUnreferencedByEveryRemainingSet(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	vdb := createInstalledFixture(t, base, root, "sys-devel", "first", "1", "usr/bin/first")
	request := validRequest(base, root, []Package{{VDBEntryPath: vdb}})
	firstPath, err := Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.SetID, request.OperationID = "set-2", "operation-2"
	secondPath, err := Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	markVerifiedForTest(t, firstPath)
	if err := MarkStatus(secondPath, StatusActive, ""); err != nil {
		t.Fatal(err)
	}
	result, err := PruneVerified(request.Directory, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 1 || len(result.RemovedObjects) != 0 {
		t.Fatalf("first prune = %+v", result)
	}
	markVerifiedForTest(t, secondPath)
	result, err = PruneVerified(request.Directory, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 1 || len(result.RemovedObjects) != 1 {
		t.Fatalf("second prune = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(request.Directory, "objects", "sha256", result.RemovedObjects[0])); !os.IsNotExist(err) {
		t.Fatalf("unreferenced object remains: %v", err)
	}
}

func markVerifiedForTest(t *testing.T, path string) {
	t.Helper()
	if err := MarkStatus(path, StatusPendingVerification, ""); err != nil {
		t.Fatal(err)
	}
	status, err := ReadStatus(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyAfterReboot(path, status.CaptureBootID+"-next"); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyAfterRebootRequiresPendingStateAndDifferentBoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	vdb := createInstalledFixture(t, base, root, "sys-devel", "first", "1", "usr/bin/first")
	path, err := Publish(context.Background(), validRequest(base, root, []Package{{VDBEntryPath: vdb}}))
	if err != nil {
		t.Fatal(err)
	}
	status, err := ReadStatus(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyAfterReboot(path, status.CaptureBootID+"-next"); err == nil {
		t.Fatal("VerifyAfterReboot() accepted an active set")
	}
	if err := MarkStatus(path, StatusPendingVerification, ""); err != nil {
		t.Fatal(err)
	}
	if err := VerifyAfterReboot(path, status.CaptureBootID); err == nil {
		t.Fatal("VerifyAfterReboot() accepted the capture boot")
	}
	if err := VerifyAfterReboot(path, status.CaptureBootID+"-next"); err != nil {
		t.Fatal(err)
	}
	verified, err := ReadStatus(path)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Status != StatusVerified || verified.VerifiedBootID == verified.CaptureBootID {
		t.Fatalf("verified status = %+v", verified)
	}
	if err := MarkStatus(path, StatusVerified, ""); err == nil {
		t.Fatal("MarkStatus() bypassed reboot verification")
	}
}
