package phaseproto

import (
	"testing"
)

func TestSavedEnvironmentHeaderDoesNotInspectPackageText(t *testing.T) {
	header := SavedEnvironmentMarker + "\n" + SavedLifecyclePrefix + " pkg_prerm\n"
	body := "PACKAGE_TEXT='pkg_info ()'\npackage_helper() {\ncat <<'END'\npkg_config ()\nEND\n}\n"
	got, phases, err := ParseSavedEnvironmentHeader([]byte(header + body))
	if err != nil || string(got) != header || len(phases) != 1 || !phases["pkg_prerm"] {
		t.Fatalf("header = %q, phases = %v, error = %v", got, phases, err)
	}
}

func TestSavedEnvironmentHeaderRejectsMalformedMetadata(t *testing.T) {
	for _, suffix := range []string{
		"",
		"export VALUE=initial\n",
		SavedLifecyclePrefix + " pkg_unknown\n",
		SavedLifecyclePrefix + " pkg_info pkg_info\n",
		"# invalid metadata\n# ARISE_PHASE_STATE_V2\n",
	} {
		state := []byte(SavedEnvironmentMarker + "\n" + suffix)
		if _, _, err := ParseSavedEnvironmentHeader(state); err == nil {
			t.Fatalf("accepted malformed metadata %q", suffix)
		}
		if HasCompleteSavedEnvironment(state) {
			t.Fatalf("invalid current metadata became a complete or legacy snapshot: %q", suffix)
		}
	}
}
