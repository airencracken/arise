// Package depstring parses Gentoo Portage dependency strings (DEPEND,
// RDEPEND, BDEPEND, IDEPEND, PDEPEND) into structured dependency trees.
package depstring

import (
	"fmt"
	"strconv"
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

// AtMostOneOfGroup represents ?? ( node node ... ) — zero or one child may
// be satisfied.
type AtMostOneOfGroup struct {
	Children []DepNode
}

func (g *AtMostOneOfGroup) String() string {
	if len(g.Children) == 0 {
		return "?? ( )"
	}
	return "?? ( " + joinNodes(g.Children) + " )"
}

func (g *AtMostOneOfGroup) Atoms() []string {
	return collectAtoms(g.Children)
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

// Block represents !atom — a weak blocker. The packages may temporarily
// coexist during a transaction, but not in the final installed state.
type Block struct {
	Atom string
}

func (b *Block) String() string { return "!" + b.Atom }
func (b *Block) Atoms() []string {
	return []string{b.Atom}
}

// WeakBlock is the legacy type name for !!atom, Portage's strong blocker. A
// transaction must remove the blocked package before merging the blocker.
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
	Atom           string
	Condition      string
	AnyOfCondition string // condition enclosing the group, excluding option-local conditions
	AnyOfGroup     bool
	AnyOfID        int // unique non-zero identifier within one CollectMeta call
	AnyOfOption    int // one-based conjunction alternative within AnyOfID
	Block          bool
	WeakBlock      bool
}

func CollectMeta(node DepNode) []AtomMeta {
	var result []AtomMeta
	VisitMeta(node, func(meta AtomMeta) {
		result = append(result, meta)
	})
	return result
}

// VisitMeta walks dependency atoms in the same deterministic order and with
// the same annotations as CollectMeta without constructing an intermediate
// result slice.
func VisitMeta(node DepNode, visit func(AtomMeta)) {
	if visit == nil {
		return
	}
	nextID := 0
	visitMeta(node, "", false, 0, 0, "", &nextID, make(map[int]int), visit)
}

// ValidatePackageDependencies rejects syntax that the shared parser accepts
// only for REQUIRED_USE. Package dependency fields support conjunction,
// any-of groups and USE conditionals, but not ^^/?? cardinality operators.
func ValidatePackageDependencies(node DepNode) error {
	return ValidatePackageDependenciesEAPI(node, "")
}

func ValidatePackageDependenciesEAPI(node DepNode, rawEAPI string) error {
	if node == nil {
		return nil
	}
	eapi, eapiErr := strconv.Atoi(rawEAPI)
	validateAtom := func(raw string) error {
		parsed, err := atom.ParsePackageAtom(raw)
		if err != nil {
			return err
		}
		if eapiErr == nil {
			if eapi < 1 && parsed.Slot != "" {
				return fmt.Errorf("slot dependencies require EAPI 1 or newer")
			}
			if eapi < 5 && parsed.SlotOp != atom.SlotOpNone {
				return fmt.Errorf("slot operators require EAPI 5 or newer")
			}
			if eapi <= 9 && parsed.Repo != "" {
				return fmt.Errorf("repository-qualified atoms are not valid in EAPI %d package dependencies", eapi)
			}
		}
		return nil
	}
	var validate func(DepNode) error
	validate = func(current DepNode) error {
		switch n := current.(type) {
		case *AtomDep:
			return validateAtom(n.Atom)
		case *Block:
			return validateAtom(n.Atom)
		case *WeakBlock:
			if eapiErr == nil && eapi < 2 {
				return fmt.Errorf("strong blockers require EAPI 2 or newer")
			}
			return validateAtom(n.Atom)
		case *AllOfGroup:
			if !n.Implicit && len(n.Children) == 0 {
				return fmt.Errorf("empty package dependency group")
			}
			for _, child := range n.Children {
				if err := validate(child); err != nil {
					return err
				}
			}
			return nil
		case *AnyOfGroup:
			if len(n.Children) == 0 {
				return fmt.Errorf("empty any-of package dependency group")
			}
			for _, child := range n.Children {
				if err := validate(child); err != nil {
					return err
				}
			}
			return nil
		case *UseConditional:
			if len(n.Children) == 0 {
				return fmt.Errorf("empty USE-conditional package dependency group")
			}
			for _, child := range n.Children {
				if err := validate(child); err != nil {
					return err
				}
			}
			return nil
		case *XorOfGroup:
			return fmt.Errorf("^^ is valid in REQUIRED_USE, not package dependency fields")
		case *AtMostOneOfGroup:
			return fmt.Errorf("?? is valid in REQUIRED_USE, not package dependency fields")
		default:
			return fmt.Errorf("unsupported package dependency node %T", current)
		}
	}
	return validate(node)
}

func visitMeta(node DepNode, condition string, anyOf bool, anyOfID, anyOfOption int, anyOfCondition string, nextID *int, nextOption map[int]int, visit func(AtomMeta)) {
	if node == nil {
		return
	}
	switch n := node.(type) {
	case *AtomDep:
		visit(AtomMeta{Atom: n.Atom, Condition: condition, AnyOfCondition: anyOfCondition, AnyOfGroup: anyOf, AnyOfID: anyOfID, AnyOfOption: anyOfOption})
	case *Block:
		visit(AtomMeta{Atom: n.Atom, Condition: condition, AnyOfCondition: anyOfCondition, AnyOfGroup: anyOf, AnyOfID: anyOfID, AnyOfOption: anyOfOption, Block: true})
	case *WeakBlock:
		visit(AtomMeta{Atom: n.Atom, Condition: condition, AnyOfCondition: anyOfCondition, AnyOfGroup: anyOf, AnyOfID: anyOfID, AnyOfOption: anyOfOption, WeakBlock: true})
	case *AllOfGroup:
		for _, child := range n.Children {
			visitMeta(child, condition, anyOf, anyOfID, anyOfOption, anyOfCondition, nextID, nextOption, visit)
		}
	case *AnyOfGroup:
		groupID := anyOfID
		if !anyOf {
			*nextID++
			groupID = *nextID
			anyOfCondition = condition
		}
		for _, child := range n.Children {
			nextOption[groupID]++
			visitMeta(child, condition, true, groupID, nextOption[groupID], anyOfCondition, nextID, nextOption, visit)
		}
	case *XorOfGroup:
		for _, child := range n.Children {
			visitMeta(child, condition, anyOf, anyOfID, anyOfOption, anyOfCondition, nextID, nextOption, visit)
		}
	case *AtMostOneOfGroup:
		for _, child := range n.Children {
			visitMeta(child, condition, anyOf, anyOfID, anyOfOption, anyOfCondition, nextID, nextOption, visit)
		}
	case *UseConditional:
		nextCond := n.Flag
		if condition != "" {
			nextCond = condition + "," + n.Flag
		}
		for _, child := range n.Children {
			visitMeta(child, nextCond, anyOf, anyOfID, anyOfOption, anyOfCondition, nextID, nextOption, visit)
		}
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
	case tok == "??":
		return p.parseAtMostOneOfGroup()
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

func (p *parser) parseAtMostOneOfGroup() (DepNode, error) {
	p.skipWS()
	if p.pos >= len(p.input) || p.input[p.pos] != '(' {
		return nil, fmt.Errorf("expected '(' after '??' at position %d", p.pos)
	}
	p.pos++ // consume '('
	children, err := p.parseChildren()
	if err != nil {
		return nil, err
	}
	return &AtMostOneOfGroup{Children: children}, nil
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
	case *AtMostOneOfGroup:
		satisfiedCount := 0
		for _, child := range n.Children {
			sat, _ := Satisfy(child, installed, useFlags)
			if sat {
				satisfiedCount++
			}
		}
		return satisfiedCount <= 1, nil
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
