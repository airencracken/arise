// Package humansize formats byte counts for user-facing diagnostics.
package humansize

import "fmt"

const unit = uint64(1024)

// Bytes formats an unsigned byte count with IEC binary units.
func Bytes(size uint64) string {
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := unit, 0
	for n := size / unit; n >= unit && exp < len("KMGTPE")-1; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}
