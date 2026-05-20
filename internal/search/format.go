package search

import (
	"encoding/json"
	"fmt"
	"strings"
)

func FormatResult(r SearchResult, format string) string {
	s := format
	s = strings.ReplaceAll(s, "<category>", r.Category)
	s = strings.ReplaceAll(s, "<name>", r.Package)
	s = strings.ReplaceAll(s, "<version>", r.Version)
	s = strings.ReplaceAll(s, "<description>", r.Description)
	s = strings.ReplaceAll(s, "<homepage>", r.Homepage)
	s = strings.ReplaceAll(s, "<license>", r.License)
	s = strings.ReplaceAll(s, "<keywords>", r.Keywords)
	s = strings.ReplaceAll(s, "<slot>", r.Slot)
	s = strings.ReplaceAll(s, "<revision>", extractRevision(r.Version))

	if strings.Contains(s, "<installedversions:") {
		s = replaceInstalledVersions(s, r)
	}

	installedStr := ""
	if r.Installed {
		installedStr = "I"
	}
	s = strings.ReplaceAll(s, "<installed>", installedStr)

	maskedStr := ""
	if r.IsMasked {
		maskedStr = "M"
	}
	s = strings.ReplaceAll(s, "<masked>", maskedStr)

	return s
}

func replaceInstalledVersions(s string, r SearchResult) string {
	startTag := "<installedversions:"
	for {
		idx := strings.Index(s, startTag)
		if idx < 0 {
			break
		}
		endIdx := strings.Index(s[idx:], ">")
		if endIdx < 0 {
			break
		}
		endIdx += idx
		_ = s[idx : endIdx+1]
		label := s[idx+len(startTag) : endIdx]

		val := r.InstalledVer
		if val == "" && label == "INSTALLED" {
			val = ""
		}
		if val != "" {
			val = "[" + val + "]"
		}
		s = s[:idx] + val + s[endIdx+1:]
	}
	return s
}

func extractRevision(version string) string {
	idx := strings.LastIndex(version, "-r")
	if idx < 0 {
		return ""
	}
	return version[idx+2:]
}

func PrintResult(r SearchResult, fields []string) string {
	var parts []string
	for _, f := range fields {
		switch strings.ToLower(f) {
		case "category":
			parts = append(parts, r.Category)
		case "name", "package":
			parts = append(parts, r.Package)
		case "version":
			parts = append(parts, r.Version)
		case "slot":
			parts = append(parts, r.Slot)
		case "description":
			parts = append(parts, r.Description)
		case "homepage":
			parts = append(parts, r.Homepage)
		case "keywords":
			parts = append(parts, r.Keywords)
		case "license":
			parts = append(parts, r.License)
		case "iuse":
			parts = append(parts, r.IUSE)
		default:
			parts = append(parts, "")
		}
	}
	return strings.Join(parts, " ")
}

func JSONOutput(results []SearchResult) (string, error) {
	if results == nil {
		results = []SearchResult{}
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func BriefResult(r SearchResult) string {
	installed := ""
	if r.Installed {
		installed = "I"
	}
	slot := r.Slot
	if slot == "" {
		slot = "0"
	}
	return fmt.Sprintf("[%s] %s/%s-%s:%s", installed, r.Category, r.Package, r.Version, slot)
}

func DumpResult(r SearchResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "category=%s\n", r.Category)
	fmt.Fprintf(&b, "name=%s\n", r.Package)
	fmt.Fprintf(&b, "version=%s\n", r.Version)
	fmt.Fprintf(&b, "slot=%s\n", r.Slot)
	fmt.Fprintf(&b, "description=%s\n", r.Description)
	fmt.Fprintf(&b, "homepage=%s\n", r.Homepage)
	fmt.Fprintf(&b, "keywords=%s\n", r.Keywords)
	fmt.Fprintf(&b, "iuse=%s\n", r.IUSE)
	fmt.Fprintf(&b, "license=%s\n", r.License)
	fmt.Fprintf(&b, "installed=%v\n", r.Installed)
	fmt.Fprintf(&b, "masked=%v\n", r.IsMasked)
	if len(r.AllVersions) > 0 {
		fmt.Fprintf(&b, "versions=%s\n", strings.Join(r.AllVersions, " "))
	}
	return b.String()
}
