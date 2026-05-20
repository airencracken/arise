//go:build linux

package rebuild

import (
	"os"
	"strconv"
	"strings"
	"time"
)

func waitForLoad(maxLoad float64) error {
	for {
		load, err := readLoadAvg1()
		if err != nil {
			return err
		}
		if load <= maxLoad {
			return nil
		}
		time.Sleep(1 * time.Second)
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
