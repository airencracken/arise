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
		if len(policy.AcceptedKeywords) != 0 && !keywordAccepted(pkg.Keywords, policy.AcceptedKeywords) {
			*violations = append(*violations, violation(
				"keyword-policy-violation", pkg.CPV, strings.Join(pkg.Keywords, " "), "",
				"install target has no keyword accepted by frozen policy",
			))
		}
		if len(policy.AcceptedLicenses) != 0 {
			node, err := depstring.Parse(pkg.License)
			if err != nil {
				*violations = append(*violations, violation(
					"invalid-license-expression", pkg.CPV, pkg.License, "", err.Error(),
				))
			} else if !licenseNodeAccepted(node, pkg.Use, acceptedLicenseSet(policy.AcceptedLicenses)) {
				*violations = append(*violations, violation(
					"license-policy-violation", pkg.CPV, pkg.License, "",
					"install target license is not accepted by frozen policy",
				))
			}
		}
	}
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
