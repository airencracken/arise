//go:build linux

package rebuild

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"
)

func waitForLoad(ctx context.Context, maxLoad float64) error {
	for {
		load, err := readLoadAvg1()
		if err != nil {
			return err
		}
		if load <= maxLoad {
			return nil
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func readLoadAvg1() (float64, error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return 0, nil
	}
	return strconv.ParseFloat(fields[0], 64)
}
