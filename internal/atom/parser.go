package atom

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type parser struct {
	input string
	pos   int
}

func (p *parser) peek() byte {
	if p.pos >= len(p.input) {
		return 0
	}
	return p.input[p.pos]
}

func (p *parser) advance() byte {
	if p.pos >= len(p.input) {
		return 0
	}
	b := p.input[p.pos]
	p.pos++
	return b
}

func (p *parser) parse() (*Atom, error) {
	a := &Atom{}

	op, n := parseOp(p.input)
	if op != OpNone {
		a.Op = op
		p.pos += n
	}

	if p.peek() == '!' {
		p.advance()
	}

	cp, err := p.parseCP()
	if err != nil {
		return nil, err
	}
	a.Category = cp.cat
	a.Package = cp.pkg

	if p.peek() == 0 {
		return a, nil
	}

	// 4. optional version (prefixed by '-')
	if p.peek() == '-' {
		p.advance()
		ver, err := p.parseVersion()
		if err != nil {
			return nil, fmt.Errorf("version: %w", err)
		}
		a.Version = ver
	}

	if p.peek() == 0 {
		return a, nil
	}

	// 5. optional slot or repo
	if p.peek() == ':' {
		if p.pos+1 < len(p.input) && p.input[p.pos+1] == ':' {
			// ::repo
			p.pos += 2
			a.Repo = p.parseRepo()
		} else {
			p.advance()
			if err := p.parseSlot(a); err != nil {
				return nil, fmt.Errorf("slot: %w", err)
			}
		}
	}

	if p.peek() == 0 {
		return a, nil
	}

	// 6. optional repo (::repo) — reached if slot consumed the first :
	if p.peek() == ':' {
		if p.pos+1 < len(p.input) && p.input[p.pos+1] == ':' {
			p.pos += 2
			a.Repo = p.parseRepo()
		}
	}

	if p.peek() == 0 {
		return a, nil
	}

	// 7. optional USE flags [flag,flag]
	if p.peek() == '[' {
		p.advance()
		flags, err := p.parseUseFlags()
		if err != nil {
			return nil, fmt.Errorf("use flags: %w", err)
		}
		a.UseFlags = flags
	}

	return a, nil
}

type cp struct {
	cat string
	pkg string
}

func (p *parser) parseCP() (*cp, error) {
	start := p.pos

	catStart := p.pos
	for p.pos < len(p.input) {
		b := p.input[p.pos]
		if b == '/' {
			break
		}
		if !isAtomChar(b) && b != '-' && b != '+' {
			return nil, fmt.Errorf("invalid character %q in category at position %d", b, p.pos+1)
		}
		p.pos++
	}
	if p.pos == start || p.pos >= len(p.input) || p.input[p.pos] != '/' {
		return nil, fmt.Errorf("expected category/package form")
	}
	cat := p.input[catStart:p.pos]
	if cat == "" {
		return nil, fmt.Errorf("empty category")
	}

	p.pos++ // skip '/'

	pkgStart := p.pos
	for p.pos < len(p.input) {
		b := p.input[p.pos]
		if b == '-' || b == ':' || b == '[' || b == 0 {
			break
		}
		if !isAtomChar(b) {
			return nil, fmt.Errorf("invalid character %q in package name at position %d", b, p.pos+1)
		}
		p.pos++
	}
	pkg := p.input[pkgStart:p.pos]
	if pkg == "" {
		return nil, fmt.Errorf("empty package name")
	}

	return &cp{cat: cat, pkg: pkg}, nil
}

func (p *parser) parseVersion() (*Version, error) {
	start := p.pos
	for p.pos < len(p.input) {
		b := p.input[p.pos]
		if b == ':' || b == '[' || b == 0 {
			break
		}
		p.pos++
	}
	raw := p.input[start:p.pos]
	if raw == "" {
		return nil, fmt.Errorf("empty version after '-'")
	}

	return parseVersionString(raw)
}

func parseVersionString(raw string) (*Version, error) {
	v := &Version{Raw: raw, Revision: -1}

	remain := raw

	for len(remain) > 0 && unicode.IsDigit(rune(remain[0])) {
		end := 0
		for end < len(remain) && unicode.IsDigit(rune(remain[end])) {
			end++
		}
		n, err := strconv.Atoi(remain[:end])
		if err != nil {
			return nil, fmt.Errorf("invalid number %q: %w", remain[:end], err)
		}
		v.Numbers = append(v.Numbers, n)
		remain = remain[end:]

		if len(remain) > 0 && remain[0] == '.' {
			remain = remain[1:]
		} else {
			break
		}
	}

	if len(v.Numbers) == 0 && len(remain) > 0 && remain[0] != '_' && remain[0] != '-' {
		return nil, fmt.Errorf("no numeric components in version %q", raw)
	}

	if len(remain) > 0 && unicode.IsLetter(rune(remain[0])) && remain[0] != 'p' {
		end := 0
		for end < len(remain) && unicode.IsLetter(rune(remain[end])) {
			end++
		}
		v.Letter = remain[:end]
		remain = remain[end:]
	}

	for len(remain) > 0 {
		if remain[0] == '_' {
			end := 1
			for end < len(remain) && (unicode.IsLetter(rune(remain[end])) || unicode.IsDigit(rune(remain[end]))) {
				end++
			}
			token := remain[:end]

			// check for known suffix types: _alpha, _beta, _pre, _rc, _p
			suffixType := ""
			numStart := -1
			if strings.HasPrefix(token, "_alpha") {
				suffixType = "_alpha"
			} else if strings.HasPrefix(token, "_beta") {
				suffixType = "_beta"
			} else if strings.HasPrefix(token, "_pre") {
				suffixType = "_pre"
			} else if strings.HasPrefix(token, "_rc") {
				suffixType = "_rc"
			} else if strings.HasPrefix(token, "_p") {
				suffixType = "_p"
			}

			if suffixType != "" {
				numStart = len(suffixType)
			}

			if suffixType != "" && numStart > 0 && numStart < len(token) {
				// has a numeric suffix
				numStr := token[numStart:]
				n, err := strconv.Atoi(numStr)
				if err == nil {
					v.Suffixes = append(v.Suffixes, suffixType)
					v.Suffixes = append(v.Suffixes, numStr)
					// _p with number is revision
					if suffixType == "_p" {
						v.Revision = n
					}
					remain = remain[end:]
					continue
				}
			}

			// no numeric part, just the suffix keyword
			if suffixType != "" {
				v.Suffixes = append(v.Suffixes, suffixType)
				remain = remain[len(suffixType):]
				continue
			}

			// unknown suffix token
			v.Suffixes = append(v.Suffixes, token)
			remain = remain[end:]
		} else if remain[0] == '-' {
			if len(remain) > 2 && remain[1] == 'r' {
				rest := remain[2:]
				revEnd := 0
				for revEnd < len(rest) && unicode.IsDigit(rune(rest[revEnd])) {
					revEnd++
				}
				if revEnd > 0 {
					rev, err := strconv.Atoi(rest[:revEnd])
					if err != nil {
						return nil, fmt.Errorf("invalid revision: %w", err)
					}
					v.Revision = rev
					remain = remain[2+revEnd:]
					continue
				}
			}
			return nil, fmt.Errorf("unexpected '%c' in version %q", remain[0], raw)
		} else {
			break
		}
	}

	return v, nil
}

func (p *parser) parseSlot(a *Atom) error {
	// Check for slot operator first: := or :*
	b := p.peek()
	if b == '=' {
		a.SlotOp = SlotOpEq
		p.advance()
		// If next is '[' or end, we have just := operator
		if p.peek() == 0 || p.peek() == '[' {
			return nil
		}
	} else if b == '*' {
		a.SlotOp = SlotOpStar
		p.advance()
		if p.peek() == 0 || p.peek() == '[' {
			return nil
		}
	}

	// Parse slot value
	start := p.pos
	for p.pos < len(p.input) {
		b := p.input[p.pos]
		if b == '/' || b == '[' || b == 0 {
			break
		}
		if b == ':' {
			if p.pos+1 < len(p.input) && p.input[p.pos+1] == ':' {
				break
			}
		}
		if b == '=' {
			a.SlotOp = SlotOpEq
			a.Slot = p.input[start:p.pos]
			p.pos++
			break
		}
		if b == '*' {
			a.SlotOp = SlotOpStar
			a.Slot = p.input[start:p.pos]
			p.pos++
			break
		}
		p.pos++
	}
	if a.Slot == "" && p.pos > start {
		a.Slot = p.input[start:p.pos]
	}

	// Parse subslot
	if p.peek() == '/' {
		p.advance() // skip '/'
		start = p.pos
		for p.pos < len(p.input) {
			b := p.input[p.pos]
			if b == '[' || b == 0 || b == ':' {
				break
			}
			if b == '=' {
				a.SlotOp = SlotOpEq
				a.Subslot = p.input[start:p.pos]
				p.pos++
				break
			}
			if b == '*' {
				a.SlotOp = SlotOpStar
				a.Subslot = p.input[start:p.pos]
				p.pos++
				break
			}
			p.pos++
		}
		if a.Subslot == "" && p.pos > start {
			a.Subslot = p.input[start:p.pos]
		}
	}

	return nil
}

func (p *parser) parseRepo() string {
	start := p.pos
	for p.pos < len(p.input) {
		b := p.input[p.pos]
		if b == '[' || b == 0 || !isAtomChar(b) {
			break
		}
		p.pos++
	}
	return p.input[start:p.pos]
}

func (p *parser) parseUseFlags() ([]UseFlag, error) {
	var flags []UseFlag
	for p.pos < len(p.input) {
		b := p.input[p.pos]
		if b == ']' {
			p.advance()
			return flags, nil
		}
		if b == ',' {
			p.advance()
			continue
		}
		flag, err := p.parseUseFlag()
		if err != nil {
			return nil, err
		}
		flags = append(flags, flag)
	}
	return flags, nil
}

func (p *parser) parseUseFlag() (UseFlag, error) {
	enabled := true
	if p.peek() == '-' {
		enabled = false
		p.advance()
	}

	start := p.pos
	for p.pos < len(p.input) {
		b := p.input[p.pos]
		if b == ',' || b == ']' || b == 0 {
			break
		}
		if !isAtomChar(b) && b != '-' && b != '_' && b != '@' {
			return UseFlag{}, fmt.Errorf("invalid character %q in use flag at position %d", b, p.pos+1)
		}
		p.pos++
	}
	name := p.input[start:p.pos]
	if name == "" {
		return UseFlag{}, fmt.Errorf("empty use flag")
	}
	return UseFlag{Name: name, Enabled: enabled}, nil
}

func parseOp(input string) (Op, int) {
	if len(input) == 0 {
		return OpNone, 0
	}
	ops := []struct {
		s string
		o Op
	}{
		{"<=", OpLessEq},
		{">=", OpGtEq},
		{"<", OpLess},
		{">", OpGt},
		{"~", OpTilde},
		{"=", OpEq},
	}
	for _, o := range ops {
		if strings.HasPrefix(input, o.s) {
			return o.o, len(o.s)
		}
	}
	return OpNone, 0
}

func isAtomChar(b byte) bool {
	return unicode.IsLetter(rune(b)) || unicode.IsDigit(rune(b)) || b == '_'
}
