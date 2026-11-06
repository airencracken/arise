package phaseproto

import (
	"bytes"
	"fmt"
	"strings"
)

const SavedLifecyclePrefix = "# ARISE_LIFECYCLE_PHASES"

// ParseSavedEnvironmentHeader reads only the framed metadata produced by the
// worker's actual function table. Function bodies and quoted package values
// cannot define lifecycle hooks by merely containing declaration-like text.
func ParseSavedEnvironmentHeader(state []byte) ([]byte, map[string]bool, error) {
	if !bytes.HasPrefix(state, []byte(SavedEnvironmentMarker+"\n")) {
		return nil, nil, nil
	}
	lines := bytes.SplitN(state, []byte("\n"), 3)
	if len(lines) != 3 {
		return nil, nil, fmt.Errorf("phase environment: incomplete V3 header")
	}
	line := string(lines[1])
	if line != SavedLifecyclePrefix && !strings.HasPrefix(line, SavedLifecyclePrefix+" ") {
		return nil, nil, fmt.Errorf("phase environment: missing lifecycle metadata")
	}
	phases := make(map[string]bool)
	for _, phase := range strings.Fields(strings.TrimPrefix(line, SavedLifecyclePrefix)) {
		switch phase {
		case "pkg_pretend", "pkg_setup", "pkg_preinst", "pkg_postinst", "pkg_prerm", "pkg_postrm", "pkg_config", "pkg_info", "pkg_nofetch":
		default:
			return nil, nil, fmt.Errorf("phase environment: invalid lifecycle phase %q", phase)
		}
		if phases[phase] {
			return nil, nil, fmt.Errorf("phase environment: duplicate lifecycle phase %q", phase)
		}
		phases[phase] = true
	}
	return state[:len(lines[0])+len(lines[1])+2], phases, nil
}
