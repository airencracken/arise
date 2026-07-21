//go:build !linux

package rebuild

import "context"

func waitForLoad(ctx context.Context, maxLoad float64) error {
	return nil
}
