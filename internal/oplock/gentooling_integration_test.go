package oplock

import (
	"context"
	"errors"
	"testing"

	"github.com/airencracken/gentooling"
)

func TestGentoolingAndAriseSharePortageLockNamespace(t *testing.T) {
	for _, path := range []string{
		"/var/db/pkg",
		"/var/lib/portage/world",
		"/var/cache/edb/mtimedb",
		"alternate/root/var/db/pkg/",
	} {
		if arise, library := PortageLockPath(path), gentooling.PortageStateLockPath(path); arise != library {
			t.Fatalf("lock path for %q: Arise %q, Gentooling %q", path, arise, library)
		}
	}
}

func TestGentoolingSnapshotConsumerCancellationContract(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := gentooling.ReadSystemSnapshot(ctx, gentooling.SystemPaths{}, gentooling.SnapshotOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("snapshot cancellation = %v", err)
	}
}
