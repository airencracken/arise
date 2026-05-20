package integration

import "testing"

func TestPortageAvailable(t *testing.T) {
	_ = PortageAvailable()
}

func TestRequirePortage(t *testing.T) {
	if !PortageAvailable() {
		t.Log("portage not available, as expected on this host")
	}
}

func TestAllComparisons(t *testing.T) {
	if !PortageAvailable() {
		t.Skip("portage not available")
	}
	RunAll(t)
}

func TestAnalyzeBrokenState(t *testing.T) {
	if !PortageAvailable() {
		t.Skip("portage not available")
	}
	states := AnalyzeBrokenState(t)
	t.Logf("Found %d potentially broken packages", len(states))
	for _, s := range states {
		t.Logf("  %s: %s (suggestion: %s)", s.Package, s.Issue, s.Suggestion)
	}
}
