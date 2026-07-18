package atom

import (
	"fmt"
	"strconv"
	"strings"
)

type SlotOp string

const (
	SlotOpNone SlotOp = ""
	SlotOpEq   SlotOp = "="
	SlotOpStar SlotOp = "*"
)

type Op string

const (
	OpNone   Op = ""
	OpLess   Op = "<"
	OpLessEq Op = "<="
	OpEq     Op = "="
	OpEqGlob Op = "=*"
	OpTilde  Op = "~"
	OpGtEq   Op = ">="
	OpGt     Op = ">"
)

type Version struct {
	Raw      string
	Numbers  []int
	Letter   string
	Suffixes []string
	Revision int // -1 means no revision suffix
}

type Atom struct {
	Op       Op
	Category string
	Package  string
	Version  *Version
	Slot     string
	Subslot  string
	SlotOp   SlotOp
	Repo     string
	UseFlags []UseFlag
}

type UseFlag struct {
	Name        string
	Enabled     bool
	Conditional bool  // flag? or !flag?
	Equal       bool  // flag= or !flag=
	Negated     bool  // leading ! for conditional/equality operators
	Default     *bool // (+) or (-) when the target omits the flag from IUSE
}

func (a Atom) String() string {
	var b strings.Builder

	if a.Op != OpNone {
		if a.Op == OpEqGlob {
			b.WriteByte('=')
		} else {
			b.WriteString(string(a.Op))
		}
	}
	if a.Category != "" {
		b.WriteString(a.Category)
		b.WriteByte('/')
	}
	b.WriteString(a.Package)

	if a.Version != nil && a.Version.Raw != "" {
		b.WriteByte('-')
		b.WriteString(a.Version.Raw)
	}

	if a.Slot != "" || a.SlotOp != SlotOpNone {
		b.WriteByte(':')
		if a.Slot == "" {
			b.WriteString(string(a.SlotOp))
		} else {
			b.WriteString(a.Slot)
			if a.Subslot != "" {
				b.WriteByte('/')
				b.WriteString(a.Subslot)
			}
			if a.SlotOp != SlotOpNone {
				b.WriteString(string(a.SlotOp))
			}
		}
	}

	if a.Repo != "" {
		b.WriteString("::")
		b.WriteString(a.Repo)
	}

	if len(a.UseFlags) > 0 {
		b.WriteByte('[')
		for i, f := range a.UseFlags {
			if i > 0 {
				b.WriteByte(',')
			}
			if f.Negated {
				b.WriteByte('!')
			} else if !f.Enabled && !f.Conditional && !f.Equal {
				b.WriteByte('-')
			}
			b.WriteString(f.Name)
			if f.Default != nil {
				if *f.Default {
					b.WriteString("(+)")
				} else {
					b.WriteString("(-)")
				}
			}
			if f.Conditional {
				b.WriteByte('?')
			} else if f.Equal {
				b.WriteByte('=')
			}
		}
		b.WriteByte(']')
	}

	return b.String()
}

func (a Atom) CPV() string {
	var b strings.Builder
	b.WriteString(a.Category)
	b.WriteByte('/')
	b.WriteString(a.Package)
	if a.Version != nil && a.Version.Raw != "" {
		b.WriteByte('-')
		b.WriteString(a.Version.Raw)
	}
	return b.String()
}

func (a Atom) CP() string {
	return a.Category + "/" + a.Package
}

func (a Atom) Key() string {
	return a.CP()
}

func (v *Version) Compare(other *Version) int {
	if v == nil && other == nil {
		return 0
	}
	if v == nil {
		return -1
	}
	if other == nil {
		return 1
	}

	maxN := len(v.Numbers)
	if len(other.Numbers) > maxN {
		maxN = len(other.Numbers)
	}
	for i := 0; i < maxN; i++ {
		vn := 0
		on := 0
		if i < len(v.Numbers) {
			vn = v.Numbers[i]
		}
		if i < len(other.Numbers) {
			on = other.Numbers[i]
		}
		if vn < on {
			return -1
		}
		if vn > on {
			return 1
		}
	}
	// PMS version components are significant even when an omitted component
	// would have the numeric value zero. Portage therefore orders 1.0 before
	// 1.0.0 instead of applying semver-style trailing-zero normalization.
	if len(v.Numbers) < len(other.Numbers) {
		return -1
	}
	if len(v.Numbers) > len(other.Numbers) {
		return 1
	}

	if v.Letter < other.Letter {
		return -1
	}
	if v.Letter > other.Letter {
		return 1
	}

	// suffix comparison: walk both lists, comparing token by token
	// ordering: _alpha < _beta < _pre < _rc < _p
	// when one list runs out, the next token on the longer side determines winner
	aSuf := v.Suffixes
	bSuf := other.Suffixes
	minS := len(aSuf)
	if len(bSuf) < minS {
		minS = len(bSuf)
	}
	for i := 0; i < minS; i++ {
		sa := aSuf[i]
		sb := bSuf[i]
		na, aIsNum := parseSuffixNum(sa)
		nb, bIsNum := parseSuffixNum(sb)

		if aIsNum && bIsNum {
			if na < nb {
				return -1
			}
			if na > nb {
				return 1
			}
			continue
		}

		if aIsNum != bIsNum {
			// one is a suffix type, the other is a number
			// number should come after suffix type (since it modifies it)
			if aIsNum {
				// a is number, b is suffix type. a comes after b (a > b)
				return 1
			}
			return -1
		}

		// both are suffix types (e.g. _alpha, _beta, _rc, _p)
		oa := suffixOrder(sa)
		ob := suffixOrder(sb)
		if oa < ob {
			return -1
		}
		if oa > ob {
			return 1
		}
	}

	// one list ran out; the next token on the longer side determines order
	if len(aSuf) > minS {
		// v has more suffixes
		next := aSuf[minS]
		if isNegativeSuffix(next) {
			return -1 // v is older (has _alpha/etc where other has none)
		}
		if isPositiveSuffix(next) {
			return 1 // v is newer (has _p where other has none)
		}
		// numeric token: v has extra number = v is newer
		if _, ok := parseSuffixNum(next); ok {
			return 1
		}
		return 1
	}
	if len(bSuf) > minS {
		// other has more suffixes
		next := bSuf[minS]
		if isNegativeSuffix(next) {
			return 1 // v is newer
		}
		if isPositiveSuffix(next) {
			return -1 // v is older
		}
		if _, ok := parseSuffixNum(next); ok {
			return -1
		}
		return -1
	}

	if v.Revision < other.Revision {
		return -1
	}
	if v.Revision > other.Revision {
		return 1
	}

	return 0
}

func parseSuffixNum(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

func suffixOrder(s string) int {
	switch s {
	case "_alpha":
		return 0
	case "_beta":
		return 1
	case "_pre":
		return 2
	case "_rc":
		return 3
	case "_p":
		return 4
	default:
		if len(s) > 1 && s[0] == '_' {
			return 5
		}
		return 6
	}
}

func isNegativeSuffix(s string) bool {
	switch s {
	case "_alpha", "_beta", "_pre", "_rc":
		return true
	}
	return false
}

func isPositiveSuffix(s string) bool {
	return s == "_p"
}

func (v Version) String() string {
	return v.Raw
}

func Parse(raw string) (*Atom, error) {
	if raw == "" {
		return nil, fmt.Errorf("empty atom string")
	}

	p := &parser{input: raw, pos: 0}
	atom, err := p.parse()
	if err != nil {
		return nil, fmt.Errorf("parsing atom %q: %w", raw, err)
	}
	return atom, nil
}

func ParseVersion(raw string) (*Version, error) {
	if raw == "" {
		return nil, fmt.Errorf("empty version string")
	}
	return parseVersionString(raw)
}
