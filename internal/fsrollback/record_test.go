package fsrollback

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type contractProvider struct{}

func (contractProvider) Kind() ProviderKind { return ProviderBtrfs }
func (contractProvider) Probe(context.Context, []Mount) ([]Capability, error) {
	return nil, nil
}
func (contractProvider) Create(context.Context, Capability, string) (Snapshot, error) {
	return Snapshot{}, nil
}
func (contractProvider) Delete(context.Context, Snapshot) error   { return nil }
func (contractProvider) Rollback(context.Context, Snapshot) error { return nil }

var _ Provider = contractProvider{}

func validRecord(t *testing.T) Record {
	t.Helper()
	plan := sha256.Sum256([]byte("approved plan"))
	record := Record{
		Schema:      RecordSchema,
		Provider:    ProviderBtrfs,
		OperationID: "operation-1",
		PlanSHA256:  hex.EncodeToString(plan[:]),
		Created:     time.Unix(1_700_000_000, 0).UTC(),
		Activation:  ActivationReboot,
		Coverage: Coverage{
			Required: []string{"/", "/var/db/pkg"},
			Boundaries: []Mount{
				{Path: "/", Source: "/dev/root", Filesystem: "btrfs", StableID: "root-id"},
				{Path: "/var", Source: "/dev/var", Filesystem: "btrfs", StableID: "var-id"},
			},
		},
		Snapshots: []Snapshot{
			{BoundaryPath: "/", StableID: "root-id", SnapshotID: "root/pre-operation-1"},
			{BoundaryPath: "/var", StableID: "var-id", SnapshotID: "var/pre-operation-1"},
		},
		CreationOrder:  []string{"/", "/var"},
		BootRequired:   true,
		RetentionClass: "pending-verification",
	}
	if err := record.Seal(); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestRecordSchemaRoundTripAndDigest(t *testing.T) {
	record := validRecord(t)
	data, err := Encode(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, record) {
		t.Fatalf("round trip changed record:\n got=%+v\nwant=%+v", decoded, record)
	}
	decoded.Snapshots[0].SnapshotID = "attacker-controlled"
	if err := decoded.Validate(); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("tampered record error = %v", err)
	}
}

func TestMutationRecordDigestBindsEveryAuthorizationField(t *testing.T) {
	base := validRecord(t)
	mutations := map[string]func(*Record){
		"provider":         func(r *Record) { r.Provider = ProviderLVM },
		"operation":        func(r *Record) { r.OperationID = "operation-2" },
		"plan":             func(r *Record) { r.PlanSHA256 = strings.Repeat("a", sha256.Size*2) },
		"creation time":    func(r *Record) { r.Created = r.Created.Add(time.Second) },
		"activation":       func(r *Record) { r.Activation = ActivationOffline },
		"boundary":         func(r *Record) { r.Coverage.Boundaries[0].StableID = "changed" },
		"snapshot":         func(r *Record) { r.Snapshots[0].SnapshotID = "changed" },
		"creation order":   func(r *Record) { r.CreationOrder[0], r.CreationOrder[1] = r.CreationOrder[1], r.CreationOrder[0] },
		"boot requirement": func(r *Record) { r.BootRequired = !r.BootRequired },
		"retention":        func(r *Record) { r.RetentionClass = "verified" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Coverage.Required = append([]string(nil), base.Coverage.Required...)
			candidate.Coverage.Boundaries = append([]Mount(nil), base.Coverage.Boundaries...)
			candidate.Snapshots = append([]Snapshot(nil), base.Snapshots...)
			candidate.CreationOrder = append([]string(nil), base.CreationOrder...)
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("authorization-field mutation retained a valid digest")
			}
		})
	}
}

func TestSchemaValidationRejectsUnknownAndPartialRecords(t *testing.T) {
	record := validRecord(t)
	data, err := Encode(record)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	object["unexpected"] = true
	unknown, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(unknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error = %v", err)
	}

	record.Schema++
	if err := record.Seal(); err == nil || !strings.Contains(err.Error(), "unsupported rollback record schema") {
		t.Fatalf("future-schema error = %v", err)
	}

	record = validRecord(t)
	record.Coverage.Excluded = []Mount{{Path: "/boot", Source: "/dev/boot", Filesystem: "vfat", StableID: "boot-id"}}
	if err := record.Seal(); err == nil || !strings.Contains(err.Error(), "coverage is incomplete") {
		t.Fatalf("excluded-boundary error = %v", err)
	}
}

func TestPublishIsAtomicAndPrivate(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "records", "operation.json")
	first := validRecord(t)
	if err := Publish(path, first); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("record mode = %04o", got)
	}
	second := validRecord(t)
	second.OperationID = "operation-2"
	if err := second.Seal(); err != nil {
		t.Fatal(err)
	}
	if err := Publish(path, second); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.OperationID != "operation-2" {
		t.Fatalf("published operation = %q", decoded.OperationID)
	}
	temps, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".operation.json.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temps) != 0 {
		t.Fatalf("temporary records remain: %v", temps)
	}
}

func TestAtomicityInvalidReplacementLeavesExistingRecordUnchanged(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "operation.json")
	record := validRecord(t)
	if err := Publish(path, record); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	invalid := record
	invalid.Digest = strings.Repeat("0", sha256.Size*2)
	if err := Publish(path, invalid); err == nil {
		t.Fatal("invalid replacement was published")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("failed publication changed the existing record")
	}
}

func TestAdversarialPublishRejectsSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	victim := filepath.Join(root, "victim")
	if err := os.WriteFile(victim, []byte("do not replace"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "record.json")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	if err := Publish(link, validRecord(t)); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink publication error = %v", err)
	}
	data, err := os.ReadFile(victim)
	if err != nil || string(data) != "do not replace" {
		t.Fatalf("victim changed: %q, %v", data, err)
	}
}

func TestProviderCapabilityFailsClosed(t *testing.T) {
	base := Capability{
		Provider:   ProviderLVM,
		Mount:      Mount{Path: "/", Source: "/dev/vg/root", Filesystem: "ext4", StableID: "lv-uuid"},
		Activation: ActivationOffline,
		Capacity:   Capacity{AvailableBytes: 100, RequiredBytes: 50, Evidence: "lvs-json-digest"},
	}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	base.Capacity.RequiredBytes = 101
	if err := base.Validate(); err == nil || !strings.Contains(err.Error(), "insufficient") {
		t.Fatalf("capacity error = %v", err)
	}
	base.Capacity.RequiredBytes = 50
	base.Activation = "magic"
	if err := base.Validate(); err == nil || !strings.Contains(err.Error(), "activation") {
		t.Fatalf("activation error = %v", err)
	}
}
