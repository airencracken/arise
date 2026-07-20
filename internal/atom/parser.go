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
		return nil, fmt.Errorf("blocker prefix belongs to a dependency expression, not an atom")
	}

	cp, err := p.parseCP()
	if err != nil {
		return nil, err
	}
	a.Category = cp.cat
	a.Package = cp.pkg

	if p.peek() == 0 {
		if a.Op != OpNone {
			return nil, fmt.Errorf("operator %q requires a version", a.Op)
		}
		if strings.HasSuffix(a.Package, "-") {
			return nil, fmt.Errorf("package name must not end with '-' without a version")
		}
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
		if a.Op == OpEq && strings.HasSuffix(ver.Raw, "*") {
			a.Op = OpEqGlob
		} else if strings.HasSuffix(ver.Raw, "*") {
			return nil, fmt.Errorf("version wildcard requires the = operator")
		}
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
			if a.Repo == "" {
				return nil, fmt.Errorf("empty repository")
			}
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
			if a.Repo == "" {
				return nil, fmt.Errorf("empty repository")
			}
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
	if p.peek() != 0 {
		return nil, fmt.Errorf("unexpected trailing input %q at position %d", p.input[p.pos:], p.pos+1)
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
		if !isCategoryChar(b) {
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
	if !isAtomChar(cat[0]) {
		return nil, fmt.Errorf("category must begin with an ASCII letter, digit, or underscore")
	}

	p.pos++ // skip '/'

	pkgStart := p.pos
	for p.pos < len(p.input) {
		b := p.input[p.pos]
		if b == ':' || b == '[' || b == 0 {
			break
		}
		if b == '-' && p.pos+1 < len(p.input) && p.input[p.pos+1] >= '0' && p.input[p.pos+1] <= '9' {
			end := p.pos + 1
			for end < len(p.input) && p.input[end] != ':' && p.input[end] != '[' {
				end++
			}
			if _, err := parseVersionString(p.input[p.pos+1 : end]); err == nil {
				break
			}
		}
		if !isAtomChar(b) && b != '-' && b != '+' {
			return nil, fmt.Errorf("invalid character %q in package name at position %d", b, p.pos+1)
		}
		p.pos++
	}
	pkg := p.input[pkgStart:p.pos]
	if pkg == "" {
		return nil, fmt.Errorf("empty package name")
	}
	if !isAtomChar(pkg[0]) {
		return nil, fmt.Errorf("package name must begin with an ASCII letter, digit, or underscore")
	}
	// A package component may contain (and, in a CPV, even end with) hyphens,
	// but no hyphen-delimited suffix may itself be a valid version. Otherwise
	// the package/version boundary is ambiguous (for example bar-123-1).
	for i := 0; i < len(pkg); i++ {
		if pkg[i] != '-' || i+1 == len(pkg) {
			continue
		}
		if _, err := parseVersionString(pkg[i+1:]); err == nil {
			return nil, fmt.Errorf("package name has version-like suffix %q", pkg[i+1:])
		}
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
			if !isDigitString(remain[:end]) {
				return nil, fmt.Errorf("invalid number %q: %w", remain[:end], err)
			}
			n = int(^uint(0) >> 1)
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

	if len(remain) > 0 && remain[0] >= 'a' && remain[0] <= 'z' {
		v.Letter = remain[:1]
		remain = remain[1:]
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
				if isDigitString(numStr) {
					v.Suffixes = append(v.Suffixes, suffixType)
					v.Suffixes = append(v.Suffixes, numStr)
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

			return nil, fmt.Errorf("unknown version suffix %q in version %q", token, raw)
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
						if !isDigitString(rest[:revEnd]) {
							return nil, fmt.Errorf("invalid revision: %w", err)
						}
						rev = int(^uint(0) >> 1)
						v.revisionRaw = rest[:revEnd]
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
	if remain != "" && remain != "*" {
		return nil, fmt.Errorf("unexpected %q in version %q", remain, raw)
	}

	return v, nil
}

func (p *parser) parseSlot(a *Atom) error {
	// Check for slot operator first: := or :*
	b := p.peek()
	if b == '=' {
		a.SlotOp = SlotOpEq
		p.advance()
		// A leading operator is the complete slot dependency. A named slot
		// places its operator after the slot/subslot instead.
		if p.peek() == 0 || p.peek() == '[' || (p.peek() == ':' && p.pos+1 < len(p.input) && p.input[p.pos+1] == ':') {
			return nil
		}
		return fmt.Errorf("unexpected input after standalone := operator")
	} else if b == '*' {
		a.SlotOp = SlotOpStar
		p.advance()
		if p.peek() == 0 || p.peek() == '[' || (p.peek() == ':' && p.pos+1 < len(p.input) && p.input[p.pos+1] == ':') {
			return nil
		}
		return fmt.Errorf("unexpected input after standalone :* operator")
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
			return fmt.Errorf("the * slot operator cannot follow a named slot")
		}
		p.pos++
	}
	if a.Slot == "" && p.pos > start {
		a.Slot = p.input[start:p.pos]
	}
	if a.Slot == "" {
		return fmt.Errorf("empty slot")
	}
	if err := validateSlotPart("slot", a.Slot); err != nil {
		return err
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
				if p.pos == start {
					return fmt.Errorf("empty subslot before '=' operator")
				}
				a.SlotOp = SlotOpEq
				a.Subslot = p.input[start:p.pos]
				p.pos++
				break
			}
			if b == '*' {
				return fmt.Errorf("the * slot operator cannot follow a named subslot")
			}
			p.pos++
		}
		if a.Subslot == "" && p.pos > start {
			a.Subslot = p.input[start:p.pos]
		}
		if a.Subslot == "" {
			return fmt.Errorf("empty subslot")
		}
		if err := validateSlotPart("subslot", a.Subslot); err != nil {
			return err
		}
	}

	return nil
}

func (p *parser) parseRepo() string {
	start := p.pos
	for p.pos < len(p.input) {
		b := p.input[p.pos]
		if b == '[' || b == 0 || !isRepositoryChar(b) {
			break
		}
		p.pos++
	}
	repository := p.input[start:p.pos]
	if repository != "" && !isAtomChar(repository[0]) {
		return ""
	}
	return repository
}

func (p *parser) parseUseFlags() ([]UseFlag, error) {
	var flags []UseFlag
	seen := make(map[string]bool)
	expectFlag := true
	for p.pos < len(p.input) {
		b := p.input[p.pos]
		if b == ']' {
			if expectFlag {
				return nil, fmt.Errorf("empty USE dependency")
			}
			p.advance()
			return flags, nil
		}
		if b == ',' {
			if expectFlag {
				return nil, fmt.Errorf("empty USE dependency")
			}
			p.advance()
			expectFlag = true
			continue
		}
		flag, err := p.parseUseFlag()
		if err != nil {
			return nil, err
		}
		if seen[flag.Name] {
			return nil, fmt.Errorf("duplicate USE dependency %q", flag.Name)
		}
		seen[flag.Name] = true
		flags = append(flags, flag)
		expectFlag = false
	}
	return nil, fmt.Errorf("unterminated USE dependency list")
}

func (p *parser) parseUseFlag() (UseFlag, error) {
	start := p.pos
	for p.pos < len(p.input) {
		b := p.input[p.pos]
		if b == ',' || b == ']' || b == 0 {
			break
		}
		p.pos++
	}
	raw := p.input[start:p.pos]
	if raw == "" {
		return UseFlag{}, fmt.Errorf("empty use flag")
	}
	flag := UseFlag{Enabled: true}
	if strings.HasPrefix(raw, "!") {
		flag.Negated = true
		raw = raw[1:]
	} else if strings.HasPrefix(raw, "-") {
		flag.Enabled = false
		raw = raw[1:]
	}
	if strings.HasSuffix(raw, "?") {
		flag.Conditional = true
		raw = strings.TrimSuffix(raw, "?")
	} else if strings.HasSuffix(raw, "=") {
		flag.Equal = true
		raw = strings.TrimSuffix(raw, "=")
	}
	if strings.HasSuffix(raw, "(+)") || strings.HasSuffix(raw, "(-)") {
		value := strings.HasSuffix(raw, "(+)")
		flag.Default = &value
		raw = raw[:len(raw)-3]
	}
	if raw == "" {
		return UseFlag{}, fmt.Errorf("empty use flag")
	}
	for i := 0; i < len(raw); i++ {
		if !isAtomChar(raw[i]) && raw[i] != '-' && raw[i] != '_' && raw[i] != '@' {
			return UseFlag{}, fmt.Errorf("invalid character %q in use flag at position %d", raw[i], start+i+1)
		}
	}
	if flag.Negated && !flag.Conditional && !flag.Equal {
		return UseFlag{}, fmt.Errorf("negated USE dependency %q requires ? or =", raw)
	}
	if !flag.Enabled && (flag.Conditional || flag.Equal) {
		return UseFlag{}, fmt.Errorf("disabled USE dependency %q cannot use ? or =", raw)
	}
	flag.Name = raw
	return flag, nil
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
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
}

func isCategoryChar(b byte) bool {
	return isAtomChar(b) || b == '+' || b == '-' || b == '.'
}

func isSlotChar(b byte) bool {
	return isCategoryChar(b)
}

func isRepositoryChar(b byte) bool {
	return isAtomChar(b) || b == '-'
}

func validateSlotPart(label, value string) error {
	if value == "" || !isAtomChar(value[0]) {
		return fmt.Errorf("%s must begin with an ASCII letter, digit, or underscore", label)
	}
	for i := 1; i < len(value); i++ {
		if !isSlotChar(value[i]) {
			return fmt.Errorf("invalid character %q in %s", value[i], label)
		}
	}
	return nil
}
