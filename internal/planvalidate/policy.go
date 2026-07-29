package planvalidate

import (
	"strings"

	"github.com/airencracken/arise/internal/depstring"
)

func validateActionPolicy(policy Policy, actions []Action, violations *[]Violation) {
	for _, action := range actions {
		if action.Kind != ActionInstall {
			continue
		}
		pkg := action.Package
		if pkg.Masked {
			*violations = append(*violations, violation(
				"masked-package", pkg.CPV, "", "", "install target is masked by frozen policy",
			))
		}
		if len(policy.SupportedEAPIs) != 0 && !contains(policy.SupportedEAPIs, pkg.EAPI) {
			*violations = append(*violations, violation(
				"unsupported-eapi", pkg.CPV, pkg.EAPI, "", "install target EAPI is not supported by frozen policy",
			))
		}
		acceptedKeywords := append([]string(nil), policy.AcceptedKeywords...)
		if pkg.Policy.BaseKeyword != "" {
			acceptedKeywords = append([]string{pkg.Policy.BaseKeyword}, acceptedKeywords...)
		}
		acceptedKeywords = applyPolicyChanges(acceptedKeywords, pkg.Policy.KeywordChanges)
		if len(acceptedKeywords) != 0 && !keywordAccepted(pkg.Keywords, acceptedKeywords) {
			*violations = append(*violations, violation(
				"keyword-policy-violation", pkg.CPV, strings.Join(pkg.Keywords, " "), "",
				"install target has no keyword accepted by frozen policy",
			))
		}
		acceptedLicenses := applyPolicyChanges(policy.AcceptedLicenses, pkg.Policy.LicenseChanges)
		acceptedLicenses = expandLicenseGroups(acceptedLicenses, policy.LicenseGroups)
		if len(acceptedLicenses) != 0 {
			node, err := depstring.Parse(pkg.License)
			if err != nil {
				*violations = append(*violations, violation(
					"invalid-license-expression", pkg.CPV, pkg.License, "", err.Error(),
				))
			} else if !licenseNodeAccepted(node, pkg.Use, acceptedLicenseSet(acceptedLicenses)) {
				*violations = append(*violations, violation(
					"license-policy-violation", pkg.CPV, pkg.License, "",
					"install target license is not accepted by frozen policy",
				))
			}
		}
	}
}

func applyPolicyChanges(initial, changes []string) []string {
	result := append([]string(nil), initial...)
	for _, change := range changes {
		switch {
		case change == "-*":
			result = result[:0]
		case strings.HasPrefix(change, "-"):
			remove := strings.TrimPrefix(change, "-")
			filtered := result[:0]
			for _, item := range result {
				if item != remove {
					filtered = append(filtered, item)
				}
			}
			result = filtered
		default:
			if !contains(result, change) {
				result = append(result, change)
			}
		}
	}
	return result
}

func expandLicenseGroups(changes []string, groups map[string][]string) []string {
	var result []string
	for _, change := range changes {
		negative := strings.HasPrefix(change, "-")
		name := strings.TrimPrefix(change, "-")
		members, ok := groups[strings.TrimPrefix(name, "@")]
		if !ok || !strings.HasPrefix(name, "@") {
			result = append(result, change)
			continue
		}
		for _, member := range members {
			if negative {
				result = append(result, "-"+member)
			} else {
				result = append(result, member)
			}
		}
	}
	return result
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func keywordAccepted(keywords, accepted []string) bool {
	allowed := make(map[string]bool)
	acceptAll := false
	acceptStable := false
	acceptUnstable := false
	for _, item := range accepted {
		switch item {
		case "**":
			acceptAll = true
		case "*":
			acceptStable = true
		case "~*":
			acceptUnstable = true
		default:
			allowed[item] = true
		}
	}
	for _, keyword := range keywords {
		if strings.HasPrefix(keyword, "-") {
			continue
		}
		if acceptAll || allowed[keyword] {
			return true
		}
		if strings.HasPrefix(keyword, "~") {
			if acceptUnstable {
				return true
			}
		} else if acceptStable {
			return true
		}
	}
	return false
}

func acceptedLicenseSet(changes []string) map[string]bool {
	accepted := make(map[string]bool)
	acceptAll := false
	for _, change := range changes {
		switch {
		case change == "*":
			acceptAll = true
		case change == "-*":
			acceptAll = false
			clear(accepted)
		case strings.HasPrefix(change, "-"):
			accepted[strings.TrimPrefix(change, "-")] = false
		default:
			accepted[change] = true
		}
	}
	accepted["\x00*"] = acceptAll
	return accepted
}

func licenseNodeAccepted(node depstring.DepNode, use map[string]bool, accepted map[string]bool) bool {
	switch item := node.(type) {
	case nil:
		return true
	case *depstring.AtomDep:
		if decision, ok := accepted[item.Atom]; ok {
			return decision
		}
		return accepted["\x00*"]
	case *depstring.AllOfGroup:
		for _, child := range item.Children {
			if !licenseNodeAccepted(child, use, accepted) {
				return false
			}
		}
		return true
	case *depstring.AnyOfGroup:
		for _, child := range item.Children {
			if licenseNodeAccepted(child, use, accepted) {
				return true
			}
		}
		return false
	case *depstring.UseConditional:
		flag := strings.TrimPrefix(item.Flag, "!")
		active := use[flag]
		if strings.HasPrefix(item.Flag, "!") {
			active = !active
		}
		if !active {
			return true
		}
		for _, child := range item.Children {
			if !licenseNodeAccepted(child, use, accepted) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
