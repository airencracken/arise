package fetch

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"golang.org/x/crypto/blake2b"
)

func automaticGentooSources(name string, cfg FetchConfig) []string {
	seen := make(map[string]bool)
	var bases []string
	for _, base := range cfg.GentooMirrors {
		base = strings.TrimRight(strings.TrimSpace(base), "/")
		if base == "" {
			continue
		}
		if !strings.HasSuffix(base, "/distfiles") {
			base += "/distfiles"
		}
		parsed, err := url.Parse(base)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || seen[base] {
			continue
		}
		seen[base] = true
		bases = append(bases, base)
	}
	// Gentoo's current distfile layout is `filename-hash BLAKE2B 8`,
	// meaning the first byte of the BLAKE2b-512 filename digest is a
	// directory component. Keep the historical flat path as a fallback for
	// independently operated mirrors that have not adopted this layout.
	digest := blake2b.Sum512([]byte(name))
	result := make([]string, 0, len(bases)*2)
	for _, base := range bases {
		result = append(result, fmt.Sprintf("%s/%02x/%s", base, digest[0], name))
	}
	for _, base := range bases {
		result = append(result, base+"/"+name)
	}
	return result
}

func LoadMirrorGroups(path string) (map[string][]string, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return map[string][]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetch: open thirdpartymirrors: %w", err)
	}
	groups, parseErr := ParseMirrorGroups(file)
	closeErr := file.Close()
	if parseErr != nil {
		return nil, parseErr
	}
	if closeErr != nil {
		return nil, fmt.Errorf("fetch: close thirdpartymirrors: %w", closeErr)
	}
	return groups, nil
}

// ParseMirrorGroups reads Portage profiles/thirdpartymirrors format while
// preserving group and URI order.
func ParseMirrorGroups(reader io.Reader) (map[string][]string, error) {
	groups := make(map[string][]string)
	scanner := bufio.NewScanner(reader)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(strings.SplitN(scanner.Text(), "#", 2)[0])
		if text == "" {
			continue
		}
		fields := strings.Fields(text)
		if len(fields) < 2 {
			return nil, fmt.Errorf("fetch: thirdpartymirrors line %d has no URI", line)
		}
		groups[fields[0]] = append(groups[fields[0]], fields[1:]...)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("fetch: read thirdpartymirrors: %w", err)
	}
	return groups, nil
}

func expandMirrorSource(source string, cfg FetchConfig) ([]string, error) {
	parsed, err := url.Parse(source)
	if err != nil {
		return nil, fmt.Errorf("fetch: invalid source URI %q: %w", source, err)
	}
	if parsed.Scheme != "mirror" {
		return []string{source}, nil
	}
	group := parsed.Host
	if group == "" {
		return nil, fmt.Errorf("fetch: mirror URI %q has no group", source)
	}
	bases := cfg.MirrorGroups[group]
	if group == "gentoo" && len(cfg.GentooMirrors) != 0 {
		bases = cfg.GentooMirrors
	}
	if len(bases) == 0 {
		return nil, fmt.Errorf("fetch: mirror group %q has no configured endpoints", group)
	}
	relative := strings.TrimPrefix(parsed.EscapedPath(), "/")
	if relative == "" {
		return nil, fmt.Errorf("fetch: mirror URI %q has no path", source)
	}
	result := make([]string, 0, len(bases))
	for _, base := range bases {
		base = strings.TrimSpace(base)
		if base == "" {
			continue
		}
		candidate := strings.TrimRight(base, "/") + "/" + relative
		endpoint, err := url.Parse(candidate)
		if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
			return nil, fmt.Errorf("fetch: mirror group %q has unsupported endpoint %q", group, base)
		}
		result = append(result, candidate)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("fetch: mirror group %q has no usable endpoints", group)
	}
	return result, nil
}
