// Package depstring parses Gentoo Portage dependency strings (DEPEND,
// RDEPEND, BDEPEND, IDEPEND, PDEPEND) into structured dependency trees.
package depstring

import (
	"fmt"
	"strings"

	"github.com/airencracken/arise/internal/atom"
)

// DepNode is the interface implemented by all dependency tree node types.
type DepNode interface {
	String() string
	Atoms() []string
}

// AtomDep is a single atom dependency string with operator, category, package,
// version, and optional slot information.
type AtomDep struct {
	Atom string
}

func (a *AtomDep) String() string { return a.Atom }
func (a *AtomDep) Atoms() []string {
	return []string{a.Atom}
}

// AllOfGroup is a group where all children must be satisfied. When explicit
// (non-implicit top-level), it is represented as ( child child ... ) in the
// depstring.
type AllOfGroup struct {
	Children []DepNode
	Implicit bool
}

func (g *AllOfGroup) String() string {
	if g.Implicit {
		return joinNodes(g.Children)
	}
	if len(g.Children) == 0 {
		return "( )"
	}
	return "( " + joinNodes(g.Children) + " )"
}

func (g *AllOfGroup) Atoms() []string {
	return collectAtoms(g.Children)
}

// AnyOfGroup represents || ( node node ... ) — at least one child must be
// satisfied.
type AnyOfGroup struct {
	Children []DepNode
}

func (g *AnyOfGroup) String() string {
	if len(g.Children) == 0 {
		return "|| ( )"
	}
	return "|| ( " + joinNodes(g.Children) + " )"
}

func (g *AnyOfGroup) Atoms() []string {
	return collectAtoms(g.Children)
}

// XorOfGroup represents ^^ ( node node ... ) — exactly one child must be
// satisfied.
type XorOfGroup struct {
	Children []DepNode
}

func (g *XorOfGroup) String() string {
	if len(g.Children) == 0 {
		return "^^ ( )"
	}
	return "^^ ( " + joinNodes(g.Children) + " )"
}

func (g *XorOfGroup) Atoms() []string {
	return collectAtoms(g.Children)
}

// UseConditional represents useflag? ( node node ... ) or !useflag? ( ... )
// — children are only required if the USE flag is set (or unset for negative).
type UseConditional struct {
	Flag     string // includes leading "!" for negative conditionals
	Children []DepNode
}

func (u *UseConditional) String() string {
	if len(u.Children) == 0 {
		return u.Flag + "? ( )"
	}
	return u.Flag + "? ( " + joinNodes(u.Children) + " )"
}

func (u *UseConditional) Atoms() []string {
	return collectAtoms(u.Children)
}

// Block represents !atom — a hard blocker (conflicts with the specified atom).
type Block struct {
	Atom string
}

func (b *Block) String() string { return "!" + b.Atom }
func (b *Block) Atoms() []string {
	return []string{b.Atom}
}

// WeakBlock represents !!atom — a weak blocker (conflicts if installed).
type WeakBlock struct {
	Atom string
}

func (w *WeakBlock) String() string { return "!!" + w.Atom }
func (w *WeakBlock) Atoms() []string {
	return []string{w.Atom}
}

func joinNodes(nodes []DepNode) string {
	var b strings.Builder
	for i, n := range nodes {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(n.String())
	}
	return b.String()
}

func collectAtoms(nodes []DepNode) []string {
	var result []string
	for _, n := range nodes {
		result = append(result, n.Atoms()...)
	}
	return result
}

type AtomMeta struct {
	Atom       string
	Condition  string
	AnyOfGroup bool
	AnyOfID    int // unique non-zero identifier within one CollectMeta call
	Block      bool
	WeakBlock  bool
}

func CollectMeta(node DepNode) []AtomMeta {
	nextID := 0
	return collectMeta(node, "", false, 0, &nextID)
}

func collectMeta(node DepNode, condition string, anyOf bool, anyOfID int, nextID *int) []AtomMeta {
	if node == nil {
		return nil
	}
	switch n := node.(type) {
	case *AtomDep:
		return []AtomMeta{{Atom: n.Atom, Condition: condition, AnyOfGroup: anyOf, AnyOfID: anyOfID}}
	case *Block:
		return []AtomMeta{{Atom: n.Atom, Condition: condition, AnyOfGroup: anyOf, AnyOfID: anyOfID, Block: true}}
	case *WeakBlock:
		return []AtomMeta{{Atom: n.Atom, Condition: condition, AnyOfGroup: anyOf, AnyOfID: anyOfID, WeakBlock: true}}
	case *AllOfGroup:
		var result []AtomMeta
		for _, child := range n.Children {
			result = append(result, collectMeta(child, condition, anyOf, anyOfID, nextID)...)
		}
		return result
	case *AnyOfGroup:
		*nextID++
		groupID := *nextID
		var result []AtomMeta
		for _, child := range n.Children {
			result = append(result, collectMeta(child, condition, true, groupID, nextID)...)
		}
		return result
	case *XorOfGroup:
		var result []AtomMeta
		for _, child := range n.Children {
			result = append(result, collectMeta(child, condition, anyOf, anyOfID, nextID)...)
		}
		return result
	case *UseConditional:
		nextCond := n.Flag
		if condition != "" {
			nextCond = condition + "," + n.Flag
		}
		var result []AtomMeta
		for _, child := range n.Children {
			result = append(result, collectMeta(child, nextCond, anyOf, anyOfID, nextID)...)
		}
		return result
	default:
		return nil
	}
}

// Parse parses a full dependency string into a tree. The returned DepNode is
// an implicit AllOfGroup containing the top-level children. An empty input
// returns (nil, nil).
func Parse(input string) (DepNode, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, nil
	}

	p := &parser{input: trimmed, pos: 0}
	return p.parseTop()
}

type parser struct {
	input string
	pos   int
}

func (p *parser) parseTop() (DepNode, error) {
	p.skipWS()
	if p.pos >= len(p.input) {
		return nil, nil
	}

	var children []DepNode
	for p.pos < len(p.input) {
		node, err := p.parseNode()
		if err != nil {
			return nil, err
		}
		if node != nil {
			children = append(children, node)
		}
		p.skipWS()
	}

	return &AllOfGroup{Children: children, Implicit: true}, nil
}

func (p *parser) parseNode() (DepNode, error) {
	p.skipWS()
	if p.pos >= len(p.input) {
		return nil, fmt.Errorf("unexpected end of input")
	}

	tok := p.readToken()
	if tok == "" {
		return nil, fmt.Errorf("empty token in dependency string")
	}

	switch {
	case tok == "||":
		return p.parseAnyOfGroup()
	case tok == "^^":
		return p.parseXorOfGroup()
	case tok == "(":
		return p.parseAllOfGroup()
	case tok == ")":
		return nil, fmt.Errorf("unexpected ')' at position %d", p.pos)
	case strings.HasPrefix(tok, "!!"):
		atomStr := tok[2:]
		if atomStr == "" {
			return nil, fmt.Errorf("empty atom after '!!' at position %d", p.pos)
		}
		return &WeakBlock{Atom: atomStr}, nil
	case strings.HasSuffix(tok, "?"):
		flag := tok[:len(tok)-1]
		p.skipWS()
		if p.pos >= len(p.input) || p.input[p.pos] != '(' {
			return nil, fmt.Errorf("expected '(' after use conditional %q", tok)
		}
		p.pos++ // consume '('
		children, err := p.parseChildren()
		if err != nil {
			return nil, err
		}
		if flag == "" {
			return nil, fmt.Errorf("empty USE flag name before '?'")
		}
		return &UseConditional{Flag: flag, Children: children}, nil
	case strings.HasPrefix(tok, "!"):
		atomStr := tok[1:]
		if atomStr == "" {
			return nil, fmt.Errorf("empty atom after '!' at position %d", p.pos)
		}
		return &Block{Atom: atomStr}, nil
	default:
		return &AtomDep{Atom: tok}, nil
	}
}

func (p *parser) parseAnyOfGroup() (DepNode, error) {
	p.skipWS()
	if p.pos >= len(p.input) || p.input[p.pos] != '(' {
		return nil, fmt.Errorf("expected '(' after '||' at position %d", p.pos)
	}
	p.pos++ // consume '('
	children, err := p.parseChildren()
	if err != nil {
		return nil, err
	}
	return &AnyOfGroup{Children: children}, nil
}

func (p *parser) parseXorOfGroup() (DepNode, error) {
	p.skipWS()
	if p.pos >= len(p.input) || p.input[p.pos] != '(' {
		return nil, fmt.Errorf("expected '(' after '^^' at position %d", p.pos)
	}
	p.pos++ // consume '('
	children, err := p.parseChildren()
	if err != nil {
		return nil, err
	}
	return &XorOfGroup{Children: children}, nil
}

func (p *parser) parseAllOfGroup() (DepNode, error) {
	// '(' already consumed
	children, err := p.parseChildren()
	if err != nil {
		return nil, err
	}
	return &AllOfGroup{Children: children, Implicit: false}, nil
}

func (p *parser) parseChildren() ([]DepNode, error) {
	var children []DepNode
	for {
		p.skipWS()
		if p.pos >= len(p.input) {
			return nil, fmt.Errorf("unterminated group: expected ')'")
		}
		if p.input[p.pos] == ')' {
			p.pos++ // consume ')'
			return children, nil
		}
		node, err := p.parseNode()
		if err != nil {
			return nil, err
		}
		if node != nil {
			children = append(children, node)
		}
	}
}

func (p *parser) readToken() string {
	p.skipWS()
	if p.pos >= len(p.input) {
		return ""
	}

	ch := p.input[p.pos]

	switch ch {
	case '(', ')':
		p.pos++
		return string(ch)
	case '|':
		if p.pos+1 < len(p.input) && p.input[p.pos+1] == '|' {
			p.pos += 2
			return "||"
		}
	}

	start := p.pos
	brackets := 0
	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		if ch == '[' {
			brackets++
		} else if ch == ']' && brackets > 0 {
			brackets--
		} else if brackets == 0 && (isSpace(ch) || ch == '(' || ch == ')') {
			break
		}
		p.pos++
	}
	return p.input[start:p.pos]
}

func (p *parser) skipWS() {
	for p.pos < len(p.input) && isSpace(p.input[p.pos]) {
		p.pos++
	}
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// Satisfy checks whether a dependency tree is satisfied given a set of
// installed packages (keyed by CP string like "category/package") and USE
// flags. Returns (satisfied, missingAtoms).
func Satisfy(node DepNode, installed map[string]*atom.Atom, useFlags map[string]bool) (bool, []string) {
	if node == nil {
		return true, nil
	}

	switch n := node.(type) {
	case *AtomDep:
		return checkAtomSat(n.Atom, installed)
	case *AllOfGroup:
		overall := true
		var missing []string
		for _, child := range n.Children {
			sat, miss := Satisfy(child, installed, useFlags)
			if !sat {
				overall = false
			}
			missing = append(missing, miss...)
		}
		if !overall {
			return false, missing
		}
		return true, nil
	case *AnyOfGroup:
		var allMissing [][]string
		for _, child := range n.Children {
			sat, miss := Satisfy(child, installed, useFlags)
			if sat {
				return true, nil
			}
			allMissing = append(allMissing, miss)
		}
		var flatMissing []string
		for _, m := range allMissing {
			flatMissing = append(flatMissing, m...)
		}
		return false, flatMissing
	case *XorOfGroup:
		satisfiedCount := 0
		var allMissing [][]string
		for _, child := range n.Children {
			sat, miss := Satisfy(child, installed, useFlags)
			if sat {
				satisfiedCount++
			}
			allMissing = append(allMissing, miss)
		}
		if satisfiedCount == 1 {
			return true, nil
		}
		var flatMissing []string
		for _, m := range allMissing {
			flatMissing = append(flatMissing, m...)
		}
		return false, flatMissing
	case *UseConditional:
		flag := n.Flag
		negate := false
		if strings.HasPrefix(flag, "!") {
			negate = true
			flag = flag[1:]
		}
		enabled := useFlags[flag]
		if negate {
			enabled = !enabled
		}
		if !enabled {
			return true, nil
		}
		var missing []string
		overall := true
		for _, child := range n.Children {
			sat, miss := Satisfy(child, installed, useFlags)
			if !sat {
				overall = false
			}
			missing = append(missing, miss...)
		}
		if !overall {
			return false, missing
		}
		return true, nil
	case *Block:
		parsed, err := atom.Parse(n.Atom)
		if err != nil {
			return false, []string{n.Atom}
		}
		if _, exists := installed[parsed.CP()]; exists {
			return false, nil
		}
		return true, nil
	case *WeakBlock:
		return true, nil
	default:
		return false, nil
	}
}

func checkAtomSat(raw string, installed map[string]*atom.Atom) (bool, []string) {
	parsed, err := atom.Parse(raw)
	if err != nil {
		return false, []string{raw}
	}

	cp := parsed.CP()
	inst, ok := installed[cp]
	if !ok {
		return false, []string{raw}
	}

	if parsed.Version != nil && parsed.Version.Raw != "" {
		if inst.Version == nil {
			return false, []string{raw}
		}
		cmp := inst.Version.Compare(parsed.Version)

		switch parsed.Op {
		case atom.OpNone:
			// Bare atom with version means exact match
			if cmp != 0 {
				return false, []string{raw}
			}
		case atom.OpGt:
			if cmp <= 0 {
				return false, []string{raw}
			}
		case atom.OpGtEq:
			if cmp < 0 {
				return false, []string{raw}
			}
		case atom.OpLess:
			if cmp >= 0 {
				return false, []string{raw}
			}
		case atom.OpLessEq:
			if cmp > 0 {
				return false, []string{raw}
			}
		case atom.OpEq:
			if cmp != 0 {
				return false, []string{raw}
			}
		case atom.OpEqGlob:
			if cmp > 0 {
				return false, []string{raw}
			}
		case atom.OpTilde:
			if cmp > 0 {
				return false, []string{raw}
			}
		}
	} else {
		// No version constraint
		if parsed.Op != atom.OpNone {
			if inst.Version == nil {
				return false, []string{raw}
			}
			cmp := inst.Version.Compare(parsed.Version)
			switch parsed.Op {
			case atom.OpGt:
				if cmp <= 0 {
					return false, []string{raw}
				}
			case atom.OpGtEq:
				if cmp < 0 {
					return false, []string{raw}
				}
			case atom.OpLess:
				if cmp >= 0 {
					return false, []string{raw}
				}
			case atom.OpLessEq:
				if cmp > 0 {
					return false, []string{raw}
				}
			case atom.OpEq:
				if cmp != 0 {
					return false, []string{raw}
				}
			}
		}
	}

	if parsed.Slot != "" && inst.Slot != "" && parsed.Slot != inst.Slot {
		return false, []string{raw}
	}

	return true, nil
}
