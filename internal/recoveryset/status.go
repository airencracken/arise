package recoveryset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

const StatusSchema = 1

type StatusName string

const (
	StatusActive              StatusName = "active"
	StatusPendingVerification StatusName = "pending-verification"
	StatusVerified            StatusName = "verified"
	StatusFailed              StatusName = "failed"
	StatusPendingRollback     StatusName = "pending-rollback"
)

type Status struct {
	Schema         int        `json:"schema"`
	SetID          string     `json:"set_id"`
	Status         StatusName `json:"status"`
	UpdatedAt      time.Time  `json:"updated_at"`
	FailureReason  string     `json:"failure_reason,omitempty"`
	CaptureBootID  string     `json:"capture_boot_id,omitempty"`
	VerifiedBootID string     `json:"verified_boot_id,omitempty"`
}

type PruneResult struct {
	Removed        []string
	Preserved      []string
	RemovedObjects []string
}

func MarkStatus(setPath string, status StatusName, failureReason string) error {
	if status == StatusVerified {
		return fmt.Errorf("recovery set: verified status requires post-reboot verification")
	}
	return markStatus(setPath, status, failureReason, "", "")
}

func markStatus(setPath string, status StatusName, failureReason, captureBootID, verifiedBootID string) error {
	if err := validateStatusName(status); err != nil {
		return err
	}
	manifest, err := Read(setPath)
	if err != nil {
		return err
	}
	if status == StatusFailed && failureReason == "" {
		return fmt.Errorf("recovery set: failed status requires a reason")
	}
	if status != StatusFailed && failureReason != "" {
		return fmt.Errorf("recovery set: failure reason is only valid for failed status")
	}
	lock, err := acquireStoreLock(filepath.Dir(filepath.Dir(filepath.Clean(setPath))))
	if err != nil {
		return err
	}
	defer lock.close()
	record := Status{
		Schema: StatusSchema, SetID: manifest.SetID, Status: status,
		UpdatedAt: time.Now().UTC(), FailureReason: failureReason,
		CaptureBootID: captureBootID, VerifiedBootID: verifiedBootID,
	}
	if current, readErr := readStatusFile(setPath); readErr == nil && record.CaptureBootID == "" {
		record.CaptureBootID = current.CaptureBootID
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("recovery set: encode status: %w", err)
	}
	path := filepath.Join(setPath, "status.json")
	temporary, err := os.CreateTemp(setPath, ".status.json.tmp-")
	if err != nil {
		return fmt.Errorf("recovery set: create status temporary: %w", err)
	}
	temporaryPath := temporary.Name()
	published := false
	defer func() {
		if !published {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("recovery set: write status: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("recovery set: sync status: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("recovery set: close status: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("recovery set: publish status: %w", err)
	}
	published = true
	if err := syncDirectory(setPath); err != nil {
		return fmt.Errorf("recovery set: sync status directory: %w", err)
	}
	return nil
}

func CurrentBootID() (string, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", fmt.Errorf("recovery set: read current boot ID: %w", err)
	}
	bootID := string(bytes.TrimSpace(data))
	if bootID == "" || len(bootID) > 128 {
		return "", fmt.Errorf("recovery set: invalid current boot ID")
	}
	return bootID, nil
}

func VerifyAfterReboot(setPath, currentBootID string) error {
	status, err := ReadStatus(setPath)
	if err != nil {
		return err
	}
	if status.Status != StatusPendingVerification {
		return fmt.Errorf("recovery set: post-reboot verification requires pending-verification status")
	}
	if status.CaptureBootID == "" {
		return fmt.Errorf("recovery set: capture boot ID is unavailable")
	}
	currentBootID = string(bytes.TrimSpace([]byte(currentBootID)))
	if currentBootID == "" || currentBootID == status.CaptureBootID {
		return fmt.Errorf("recovery set: verification requires a successful subsequent boot")
	}
	return markStatus(setPath, StatusVerified, "", status.CaptureBootID, currentBootID)
}

func readStatusFile(setPath string) (*Status, error) {
	data, err := os.ReadFile(filepath.Join(setPath, "status.json"))
	if err != nil {
		return nil, err
	}
	var status Status
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

func ReadStatus(setPath string) (*Status, error) {
	data, err := os.ReadFile(filepath.Join(setPath, "status.json"))
	if err != nil {
		return nil, fmt.Errorf("recovery set: read status: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var status Status
	if err := decoder.Decode(&status); err != nil {
		return nil, fmt.Errorf("recovery set: decode status: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("recovery set: status has trailing JSON")
	}
	if status.Schema != StatusSchema || status.SetID == "" || status.UpdatedAt.IsZero() {
		return nil, fmt.Errorf("recovery set: invalid status schema")
	}
	if err := validateStatusName(status.Status); err != nil {
		return nil, err
	}
	if status.Status == StatusFailed && status.FailureReason == "" {
		return nil, fmt.Errorf("recovery set: failed status has no reason")
	}
	if status.Status != StatusFailed && status.FailureReason != "" {
		return nil, fmt.Errorf("recovery set: non-failed status has a failure reason")
	}
	if status.Status == StatusVerified {
		if status.CaptureBootID == "" || status.VerifiedBootID == "" || status.CaptureBootID == status.VerifiedBootID {
			return nil, fmt.Errorf("recovery set: verified status lacks subsequent-boot evidence")
		}
	} else if status.VerifiedBootID != "" {
		return nil, fmt.Errorf("recovery set: non-verified status has verification boot evidence")
	}
	manifest, err := Read(setPath)
	if err != nil {
		return nil, err
	}
	if status.SetID != manifest.SetID {
		return nil, fmt.Errorf("recovery set: status set ID mismatch")
	}
	return &status, nil
}

func PruneVerified(directory string, keep int) (PruneResult, error) {
	if keep < 0 {
		return PruneResult{}, fmt.Errorf("recovery set: retained verified count must not be negative")
	}
	lock, err := acquireStoreLock(directory)
	if err != nil {
		return PruneResult{}, err
	}
	defer lock.close()
	setsDir := filepath.Join(directory, "sets")
	entries, err := os.ReadDir(setsDir)
	if os.IsNotExist(err) {
		return PruneResult{}, nil
	}
	if err != nil {
		return PruneResult{}, fmt.Errorf("recovery set: list sets: %w", err)
	}
	type candidate struct {
		id      string
		path    string
		updated time.Time
	}
	var candidates []candidate
	result := PruneResult{}
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) > 0 && entry.Name()[0] == '.' {
			continue
		}
		path := filepath.Join(setsDir, entry.Name())
		status, err := ReadStatus(path)
		if err != nil || status.Status != StatusVerified {
			result.Preserved = append(result.Preserved, entry.Name())
			continue
		}
		candidates = append(candidates, candidate{id: entry.Name(), path: path, updated: status.UpdatedAt})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].updated.Equal(candidates[j].updated) {
			return candidates[i].id > candidates[j].id
		}
		return candidates[i].updated.After(candidates[j].updated)
	})
	for index, item := range candidates {
		if index < keep {
			result.Preserved = append(result.Preserved, item.id)
			continue
		}
		if err := os.RemoveAll(item.path); err != nil {
			return result, fmt.Errorf("recovery set: prune verified set %s: %w", item.id, err)
		}
		result.Removed = append(result.Removed, item.id)
	}
	sort.Strings(result.Preserved)
	sort.Strings(result.Removed)
	if len(result.Removed) > 0 {
		if err := syncDirectory(setsDir); err != nil {
			return result, fmt.Errorf("recovery set: sync pruned sets directory: %w", err)
		}
	}
	removedObjects, err := pruneUnreferencedObjects(directory, setsDir)
	if err != nil {
		return result, err
	}
	result.RemovedObjects = removedObjects
	return result, nil
}

func pruneUnreferencedObjects(directory, setsDir string) ([]string, error) {
	entries, err := os.ReadDir(setsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("recovery set: relist sets for object collection: %w", err)
	}
	referenced := make(map[string]struct{})
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) > 0 && entry.Name()[0] == '.' {
			continue
		}
		manifest, err := Read(filepath.Join(setsDir, entry.Name()))
		if err != nil {
			// An unreadable preserved set may still reference any object. Fail
			// closed by skipping object collection entirely.
			return nil, nil
		}
		for _, artifact := range manifest.Artifacts {
			referenced[artifact.SHA256] = struct{}{}
		}
	}
	objectsDir := filepath.Join(directory, "objects", "sha256")
	objects, err := os.ReadDir(objectsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("recovery set: list content objects: %w", err)
	}
	var removed []string
	for _, object := range objects {
		if object.IsDir() {
			continue
		}
		if _, exists := referenced[object.Name()]; exists {
			continue
		}
		if err := os.Remove(filepath.Join(objectsDir, object.Name())); err != nil {
			return removed, fmt.Errorf("recovery set: remove unreferenced object %s: %w", object.Name(), err)
		}
		removed = append(removed, object.Name())
	}
	sort.Strings(removed)
	if len(removed) > 0 {
		if err := syncDirectory(objectsDir); err != nil {
			return removed, fmt.Errorf("recovery set: sync collected object directory: %w", err)
		}
	}
	return removed, nil
}

type storeLock struct {
	file *os.File
}

func acquireStoreLock(directory string) (*storeLock, error) {
	if directory == "" {
		return nil, fmt.Errorf("recovery set: store directory is required")
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		return nil, fmt.Errorf("recovery set: create store directory for lock: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(directory, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("recovery set: open store lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("recovery set: acquire store lock: %w", err)
	}
	return &storeLock{file: file}, nil
}

func (lock *storeLock) close() {
	if lock == nil || lock.file == nil {
		return
	}
	_ = syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	_ = lock.file.Close()
}

func validateStatusName(status StatusName) error {
	switch status {
	case StatusActive, StatusPendingVerification, StatusVerified, StatusFailed, StatusPendingRollback:
		return nil
	default:
		return fmt.Errorf("recovery set: invalid status %q", status)
	}
}
