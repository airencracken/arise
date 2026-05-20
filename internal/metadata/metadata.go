package metadata

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"strings"

	"github.com/airencracken/arise/internal/atom"
)

// PackageMetadata holds the parsed metadata for a single Gentoo package.
type PackageMetadata struct {
	Category       string
	Package        string
	Version        string
	DEPEND         string
	RDEPEND        string
	BDEPEND        string
	IDEPEND        string
	PDEPEND        string
	SRC_URI        string
	RESTRICT       string
	PROPERTIES     string
	SLOT           string
	Subslot        string
	KEYWORDS       string
	IUSE           string
	LICENSE        string
	REQUIRED_USE   string
	EAPI           string
	DEFINED_PHASES string
	DESCRIPTION    string
	HOMEPAGE       string
	INHERITED      string
	_md5_          string
	_mtime_        string
	Unknown        map[string]string
}

// Key returns the canonical category/package key (CP).
func (m *PackageMetadata) Key() string {
	return m.Category + "/" + m.Package
}

// DependAtoms returns DEPEND split into individual atom strings.
func (m *PackageMetadata) DependAtoms() []string {
	return splitAtomList(m.DEPEND)
}

// RDependAtoms returns RDEPEND split into individual atom strings.
func (m *PackageMetadata) RDependAtoms() []string {
	return splitAtomList(m.RDEPEND)
}

// BDependAtoms returns BDEPEND split into individual atom strings.
func (m *PackageMetadata) BDependAtoms() []string {
	return splitAtomList(m.BDEPEND)
}

// IDependAtoms returns IDEPEND split into individual atom strings.
func (m *PackageMetadata) IDependAtoms() []string {
	return splitAtomList(m.IDEPEND)
}

// PDependAtoms returns PDEPEND split into individual atom strings.
func (m *PackageMetadata) PDependAtoms() []string {
	return splitAtomList(m.PDEPEND)
}

// DependAtomsParsed returns parsed DEPEND atoms and any parse errors.
func (m *PackageMetadata) DependAtomsParsed() ([]*atom.Atom, []error) {
	return parseAtoms(m.DEPEND)
}

// RDependAtomsParsed returns parsed RDEPEND atoms and any parse errors.
func (m *PackageMetadata) RDependAtomsParsed() ([]*atom.Atom, []error) {
	return parseAtoms(m.RDEPEND)
}

// BDependAtomsParsed returns parsed BDEPEND atoms and any parse errors.
func (m *PackageMetadata) BDependAtomsParsed() ([]*atom.Atom, []error) {
	return parseAtoms(m.BDEPEND)
}

// IDependAtomsParsed returns parsed IDEPEND atoms and any parse errors.
func (m *PackageMetadata) IDependAtomsParsed() ([]*atom.Atom, []error) {
	return parseAtoms(m.IDEPEND)
}

// PDependAtomsParsed returns parsed PDEPEND atoms and any parse errors.
func (m *PackageMetadata) PDependAtomsParsed() ([]*atom.Atom, []error) {
	return parseAtoms(m.PDEPEND)
}

// ParseCacheEntry parses a Gentoo cache entry (key=value format) from raw bytes.
// cpv is the category/package-version string (e.g. "sys-apps/portage-3.0.51").
func ParseCacheEntry(cpv string, data []byte) (*PackageMetadata, error) {
	if cpv == "" {
		return nil, fmt.Errorf("metadata: empty cpv")
	}

	cat, pkg, ver, err := parseCPV(cpv)
	if err != nil {
		return nil, err
	}

	m := &PackageMetadata{
		Category: cat,
		Package:  pkg,
		Version:  ver,
		Unknown:  make(map[string]string),
	}

	// Normalize CRLF to LF.
	normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	normalized = bytes.ReplaceAll(normalized, []byte("\r"), []byte("\n"))
	lines := splitLines(normalized)
	var currentKey string

	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		leadingWS := len(line) != len(trimmed)

		if trimmed == "" {
			continue
		}

		if leadingWS && currentKey != "" {
			appendValue(m, currentKey, " "+trimmed)
			continue
		}

		idx := strings.IndexByte(trimmed, '=')
		if idx < 0 {
			if currentKey != "" {
				appendValue(m, currentKey, " "+trimmed)
			}
			continue
		}

		key := trimmed[:idx]
		value := trimmed[idx+1:]
		currentKey = key

		m.setField(key, value)
		m.Unknown[key] = value
	}

	return m, nil
}

func (m *PackageMetadata) setField(key, value string) {
	switch key {
	case "DEPEND":
		m.DEPEND = value
	case "RDEPEND":
		m.RDEPEND = value
	case "BDEPEND":
		m.BDEPEND = value
	case "IDEPEND":
		m.IDEPEND = value
	case "PDEPEND":
		m.PDEPEND = value
	case "SRC_URI":
		m.SRC_URI = value
	case "RESTRICT":
		m.RESTRICT = value
	case "PROPERTIES":
		m.PROPERTIES = value
	case "SLOT":
		slot, subslot := parseSlot(value)
		m.SLOT = slot
		m.Subslot = subslot
	case "KEYWORDS":
		m.KEYWORDS = value
	case "IUSE":
		m.IUSE = value
	case "LICENSE":
		m.LICENSE = value
	case "REQUIRED_USE":
		m.REQUIRED_USE = value
	case "EAPI":
		m.EAPI = value
	case "DEFINED_PHASES":
		m.DEFINED_PHASES = value
	case "DESCRIPTION":
		m.DESCRIPTION = value
	case "HOMEPAGE":
		m.HOMEPAGE = value
	case "INHERITED":
		m.INHERITED = value
	case "_md5_":
		m._md5_ = value
	case "_mtime_":
		m._mtime_ = value
	}
}

func appendValue(m *PackageMetadata, key, value string) {
	switch key {
	case "DEPEND":
		m.DEPEND += value
	case "RDEPEND":
		m.RDEPEND += value
	case "BDEPEND":
		m.BDEPEND += value
	case "IDEPEND":
		m.IDEPEND += value
	case "PDEPEND":
		m.PDEPEND += value
	default:
		m.Unknown[key] = m.Unknown[key] + value
	}
}

func parseSlot(raw string) (slot, subslot string) {
	if idx := strings.IndexByte(raw, '/'); idx >= 0 {
		slot = raw[:idx]
		subslot = raw[idx+1:]
	} else {
		slot = raw
	}
	return slot, subslot
}

func parseCPV(cpv string) (cat, pkg, ver string, err error) {
	slash := strings.IndexByte(cpv, '/')
	if slash <= 0 {
		return "", "", "", fmt.Errorf("metadata: invalid cpv %q: missing category/package separator", cpv)
	}
	cat = cpv[:slash]
	rest := cpv[slash+1:]
	if rest == "" {
		return "", "", "", fmt.Errorf("metadata: invalid cpv %q: empty package name", cpv)
	}

	dash := strings.IndexByte(rest, '-')
	if dash < 0 {
		pkg = rest
		ver = ""
	} else if dash == 0 {
		return "", "", "", fmt.Errorf("metadata: invalid cpv %q: dash after slash", cpv)
	} else {
		pkg = rest[:dash]
		ver = rest[dash+1:]
	}

	if cat == "" || pkg == "" {
		return "", "", "", fmt.Errorf("metadata: invalid cpv %q", cpv)
	}

	return cat, pkg, ver, nil
}

func splitLines(data []byte) []string {
	return strings.Split(string(data), "\n")
}

// ValidateBytes validates cache entry data. Returns an error if the data
// contains null bytes or does not end with a newline.
func ValidateBytes(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if bytes.Contains(data, []byte{0}) {
		return fmt.Errorf("metadata: data contains null bytes")
	}
	if data[len(data)-1] != '\n' {
		return fmt.Errorf("metadata: data does not end with newline")
	}
	return nil
}

func splitAtomList(s string) []string {
	result := strings.Fields(s)
	if len(result) == 0 {
		return nil
	}
	return result
}

func parseAtoms(s string) ([]*atom.Atom, []error) {
	parts := splitAtomList(s)
	var atoms []*atom.Atom
	var errs []error

	for _, p := range parts {
		a, err := atom.Parse(p)
		if err != nil {
			errs = append(errs, err)
		} else {
			atoms = append(atoms, a)
		}
	}

	return atoms, errs
}

func init() {
	gob.Register(&PackageMetadata{})
}
