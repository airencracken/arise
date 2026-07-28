package fsrollback

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Record struct {
	Schema         int              `json:"schema"`
	Provider       ProviderKind     `json:"provider"`
	OperationID    string           `json:"operation_id"`
	PlanSHA256     string           `json:"plan_sha256"`
	Created        time.Time        `json:"created"`
	Activation     ActivationMethod `json:"activation"`
	Coverage       Coverage         `json:"coverage"`
	Snapshots      []Snapshot       `json:"snapshots"`
	CreationOrder  []string         `json:"creation_order"`
	BootRequired   bool             `json:"boot_required"`
	RetentionClass string           `json:"retention_class"`
	Digest         string           `json:"digest"`
}

func (r Record) Validate() error {
	if r.Schema != RecordSchema {
		return fmt.Errorf("unsupported rollback record schema %d", r.Schema)
	}
	if !validProvider(r.Provider) {
		return fmt.Errorf("unsupported provider %q", r.Provider)
	}
	if strings.TrimSpace(r.OperationID) == "" {
		return fmt.Errorf("operation identity is required")
	}
	if len(r.PlanSHA256) != sha256.Size*2 {
		return fmt.Errorf("plan digest must be SHA-256")
	}
	if _, err := hex.DecodeString(r.PlanSHA256); err != nil {
		return fmt.Errorf("plan digest must be SHA-256: %w", err)
	}
	if r.Created.IsZero() {
		return fmt.Errorf("creation time is required")
	}
	if !r.Coverage.Eligible() {
		return fmt.Errorf("rollback coverage is incomplete")
	}
	if len(r.Snapshots) != len(r.Coverage.Boundaries) || len(r.CreationOrder) != len(r.Snapshots) {
		return fmt.Errorf("snapshot set does not match covered boundaries")
	}
	byBoundary := make(map[string]Snapshot, len(r.Snapshots))
	for _, snapshot := range r.Snapshots {
		if err := snapshot.Validate(); err != nil {
			return err
		}
		if _, exists := byBoundary[snapshot.BoundaryPath]; exists {
			return fmt.Errorf("duplicate snapshot boundary %q", snapshot.BoundaryPath)
		}
		byBoundary[snapshot.BoundaryPath] = snapshot
	}
	seenOrder := make(map[string]bool)
	for _, boundary := range r.CreationOrder {
		if _, exists := byBoundary[boundary]; !exists {
			return fmt.Errorf("creation order references unknown boundary %q", boundary)
		}
		if seenOrder[boundary] {
			return fmt.Errorf("creation order repeats boundary %q", boundary)
		}
		seenOrder[boundary] = true
	}
	expected, err := r.expectedDigest()
	if err != nil {
		return err
	}
	if r.Digest == "" || r.Digest != expected {
		return fmt.Errorf("rollback record digest mismatch")
	}
	return nil
}

func (r *Record) Seal() error {
	if r.Schema == 0 {
		r.Schema = RecordSchema
	}
	r.Digest = ""
	digest, err := r.expectedDigest()
	if err != nil {
		return err
	}
	r.Digest = digest
	return r.Validate()
}

func (r Record) expectedDigest() (string, error) {
	r.Digest = ""
	data, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("encode rollback record: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func Encode(record Record) ([]byte, error) {
	if err := record.Validate(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode rollback record: %w", err)
	}
	return append(data, '\n'), nil
}

func Decode(data []byte) (Record, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record Record
	if err := decoder.Decode(&record); err != nil {
		return Record{}, fmt.Errorf("decode rollback record: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Record{}, fmt.Errorf("decode rollback record: trailing data")
		}
		return Record{}, fmt.Errorf("decode rollback record trailing data: %w", err)
	}
	if err := record.Validate(); err != nil {
		return Record{}, err
	}
	return record, nil
}

// Publish atomically replaces path and syncs both the record and its parent
// directory. It intentionally refuses symlink targets.
func Publish(path string, record Record) error {
	data, err := Encode(record)
	if err != nil {
		return err
	}
	path, err = cleanAbsolute(path)
	if err != nil {
		return fmt.Errorf("rollback record path: %w", err)
	}
	if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("rollback record path must not be a symlink")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("inspect rollback record: %w", statErr)
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create rollback record directory: %w", err)
	}
	temp, err := os.CreateTemp(parent, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return fmt.Errorf("create rollback record: %w", err)
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("mode rollback record: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write rollback record: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync rollback record: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close rollback record: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("publish rollback record: %w", err)
	}
	committed = true
	directory, err := os.Open(parent)
	if err != nil {
		return fmt.Errorf("open rollback record directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync rollback record directory: %w", err)
	}
	return nil
}
