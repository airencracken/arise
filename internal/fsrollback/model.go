// Package fsrollback defines the fail-closed boundary for experimental
// filesystem snapshot providers. It does not execute provider commands.
package fsrollback

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const RecordSchema = 1

type ProviderKind string

const (
	ProviderBtrfs   ProviderKind = "btrfs"
	ProviderZFS     ProviderKind = "zfs"
	ProviderLVM     ProviderKind = "lvm"
	ProviderOverlay ProviderKind = "overlayfs"
	ProviderFUSE    ProviderKind = "fuse-overlayfs"
)

type ActivationMethod string

const (
	ActivationOnline  ActivationMethod = "online"
	ActivationOffline ActivationMethod = "offline"
	ActivationReboot  ActivationMethod = "reboot"
)

type Mount struct {
	Path       string `json:"path"`
	Source     string `json:"source"`
	Filesystem string `json:"filesystem"`
	StableID   string `json:"stable_id"`
}

type Capability struct {
	Provider   ProviderKind     `json:"provider"`
	Mount      Mount            `json:"mount"`
	Activation ActivationMethod `json:"activation"`
	Capacity   Capacity         `json:"capacity"`
}

type Capacity struct {
	AvailableBytes uint64 `json:"available_bytes"`
	RequiredBytes  uint64 `json:"required_bytes"`
	Evidence       string `json:"evidence"`
}

func (c Capability) Validate() error {
	if !validProvider(c.Provider) {
		return fmt.Errorf("unsupported provider %q", c.Provider)
	}
	if err := validateMount(c.Mount); err != nil {
		return err
	}
	switch c.Activation {
	case ActivationOnline, ActivationOffline, ActivationReboot:
	default:
		return fmt.Errorf("unsupported activation method %q", c.Activation)
	}
	if strings.TrimSpace(c.Capacity.Evidence) == "" {
		return errors.New("capacity evidence is required")
	}
	if c.Capacity.AvailableBytes < c.Capacity.RequiredBytes {
		return fmt.Errorf("insufficient snapshot capacity: available=%d required=%d", c.Capacity.AvailableBytes, c.Capacity.RequiredBytes)
	}
	return nil
}

func validProvider(kind ProviderKind) bool {
	switch kind {
	case ProviderBtrfs, ProviderZFS, ProviderLVM, ProviderOverlay, ProviderFUSE:
		return true
	default:
		return false
	}
}

// Provider is deliberately narrower than a command runner. Implementations
// must return structured identities that the caller can bind to a durable
// operation record before authorizing mutation.
type Provider interface {
	Kind() ProviderKind
	Probe(context.Context, []Mount) ([]Capability, error)
	Create(context.Context, Capability, string) (Snapshot, error)
	Delete(context.Context, Snapshot) error
	Rollback(context.Context, Snapshot) error
}

type Snapshot struct {
	BoundaryPath string `json:"boundary_path"`
	StableID     string `json:"stable_id"`
	SnapshotID   string `json:"snapshot_id"`
}

func (s Snapshot) Validate() error {
	if strings.TrimSpace(s.BoundaryPath) == "" {
		return errors.New("snapshot boundary path is required")
	}
	if strings.TrimSpace(s.StableID) == "" {
		return errors.New("snapshot stable identity is required")
	}
	if strings.TrimSpace(s.SnapshotID) == "" {
		return errors.New("snapshot identity is required")
	}
	return nil
}
