package rebuild

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"syscall"
)

// Interpret check-reqs only for the temporary directory Arise supplied.
// A requirement for /usr, /var or memory needs a different remedy.
func preflightSpaceAdvice(output []string, tempDir, workDir string) string {
	for _, line := range output {
		value, ok := strings.CutPrefix(line, "There is NOT at least ")
		if !ok {
			continue
		}
		value, ok = strings.CutSuffix(value, " disk space at \""+tempDir+"\"")
		if !ok {
			continue
		}
		number, unit, _ := strings.Cut(value, " ")
		size, err := strconv.ParseUint(number, 10, 64)
		scale := map[string]uint64{"MiB": 1, "GiB": 1024, "TiB": 1024 * 1024}[unit]
		if err != nil || scale == 0 || size == 0 || size > math.MaxUint64/scale {
			continue
		}
		required := size * scale
		const remedy = "Free space on that filesystem or choose a different one with --work-dir."
		var stat syscall.Statfs_t
		if err := syscall.Statfs(tempDir, &stat); err != nil || stat.Bsize <= 0 || uint64(stat.Bavail) > math.MaxUint64/uint64(stat.Bsize) {
			return fmt.Sprintf("The build filesystem for %q needs at least %d MiB free. %s", workDir, required, remedy)
		}
		available := uint64(stat.Bavail) * uint64(stat.Bsize) / (1024 * 1024)
		if available < required {
			return fmt.Sprintf("%q had %d MiB available of the %d MiB required (%d MiB short). %s", workDir, available, required, required-available, remedy)
		}
		return fmt.Sprintf("%q now has %d MiB available against a requirement of %d MiB. Rerun the original command with --preflight-only to recheck the requirements.", workDir, available, required)
	}
	return ""
}
