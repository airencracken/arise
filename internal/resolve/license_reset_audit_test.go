package resolve

import "testing"

func TestLicenseResetClearsPriorDecisions(t *testing.T) {
	for _, license := range []string{"MIT", "EULA"} {
		for _, rules := range [][]string{{license, "-*"}, {"*", license, "-*"}, {"@EULA", "-*"}} {
			if LicenseExpressionAccepted(license, rules, nil) {
				t.Fatalf("%s accepted after reset: %v", license, rules)
			}
		}
	}
	if !LicenseExpressionAccepted("MIT", []string{"-MIT", "-*", "*"}, nil) {
		t.Fatal("reset retained a stale exclusion")
	}
}
