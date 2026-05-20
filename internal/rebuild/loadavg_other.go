//go:build !linux

package rebuild

func waitForLoad(maxLoad float64) error {
	return nil
}
