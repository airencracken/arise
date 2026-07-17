package depstring

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/airencracken/arise/internal/atom"
)

// nodesEqual performs deep structural equality between two DepNode trees.
func nodesEqual(a, b DepNode) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	switch an := a.(type) {
	case *AtomDep:
		bn, ok := b.(*AtomDep)
		return ok && an.Atom == bn.Atom
	case *AllOfGroup:
		bn, ok := b.(*AllOfGroup)
		if !ok || len(an.Children) != len(bn.Children) {
			return false
		}
		for i := range an.Children {
			if !nodesEqual(an.Children[i], bn.Children[i]) {
				return false
			}
		}
		return true
	case *AnyOfGroup:
		bn, ok := b.(*AnyOfGroup)
		if !ok || len(an.Children) != len(bn.Children) {
			return false
		}
		for i := range an.Children {
			if !nodesEqual(an.Children[i], bn.Children[i]) {
				return false
			}
		}
		return true
	case *XorOfGroup:
		bn, ok := b.(*XorOfGroup)
		if !ok || len(an.Children) != len(bn.Children) {
			return false
		}
		for i := range an.Children {
			if !nodesEqual(an.Children[i], bn.Children[i]) {
				return false
			}
		}
		return true
	case *AtMostOneOfGroup:
		bn, ok := b.(*AtMostOneOfGroup)
		if !ok || len(an.Children) != len(bn.Children) {
			return false
		}
		for i := range an.Children {
			if !nodesEqual(an.Children[i], bn.Children[i]) {
				return false
			}
		}
		return true
	case *UseConditional:
		bn, ok := b.(*UseConditional)
		if !ok || an.Flag != bn.Flag || len(an.Children) != len(bn.Children) {
			return false
		}
		for i := range an.Children {
			if !nodesEqual(an.Children[i], bn.Children[i]) {
				return false
			}
		}
		return true
	case *Block:
		bn, ok := b.(*Block)
		return ok && an.Atom == bn.Atom
	case *WeakBlock:
		bn, ok := b.(*WeakBlock)
		return ok && an.Atom == bn.Atom
	default:
		return false
	}
}

func TestBasicAtomParsing(t *testing.T) {
	tests := []struct {
		input string
		atom  string
	}{
		{"dev-lang/python", "dev-lang/python"},
		{">=dev-lang/python-3.10", ">=dev-lang/python-3.10"},
		{"=dev-lang/python-3.10-r1", "=dev-lang/python-3.10-r1"},
		{"~dev-lang/python-3.10", "~dev-lang/python-3.10"},
		{">dev-lang/python-3.9", ">dev-lang/python-3.9"},
		{"<=dev-lang/python-3.11", "<=dev-lang/python-3.11"},
		{"dev-lang/python:3.11", "dev-lang/python:3.11"},
		{"dev-lang/python:3.11/3.11", "dev-lang/python:3.11/3.11"},
		{"dev-lang/python:=", "dev-lang/python:="},
		{"dev-lang/python:*", "dev-lang/python:*"},
		{"dev-lang/python::gentoo", "dev-lang/python::gentoo"},
		{"dev-lang/python[ssl,xml]", "dev-lang/python[ssl,xml]"},
		{"dev-lang/python[-ssl,xml]", "dev-lang/python[-ssl,xml]"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			node, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.input, err)
			}
			root, ok := node.(*AllOfGroup)
			if !ok {
				t.Fatalf("expected *AllOfGroup, got %T", node)
			}
			if !root.Implicit {
				t.Fatal("root should be implicit")
			}
			if len(root.Children) != 1 {
				t.Fatalf("expected 1 child, got %d", len(root.Children))
			}
			ad, ok := root.Children[0].(*AtomDep)
			if !ok {
				t.Fatalf("expected *AtomDep, got %T", root.Children[0])
			}
			if ad.Atom != tt.atom {
				t.Errorf("expected atom %q, got %q", tt.atom, ad.Atom)
			}
		})
	}
}

func TestParseMultipleAtoms(t *testing.T) {
	input := "dev-lang/python sys-apps/gawk app-doc/doxygen"

	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	root := node.(*AllOfGroup)
	if len(root.Children) != 3 {
		t.Fatalf("expected 3 children, got %d", len(root.Children))
	}
	expected := []string{"dev-lang/python", "sys-apps/gawk", "app-doc/doxygen"}
	for i, exp := range expected {
		ad := root.Children[i].(*AtomDep)
		if ad.Atom != exp {
			t.Errorf("child %d: expected %q, got %q", i, exp, ad.Atom)
		}
	}
}

func TestParseAtMostOneOfGroupRoundTrip(t *testing.T) {
	input := "?? ( foo bar? ( baz ) )"
	node, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse(%q): %v", input, err)
	}
	root := node.(*AllOfGroup)
	if len(root.Children) != 1 {
		t.Fatalf("children = %d, want 1", len(root.Children))
	}
	group, ok := root.Children[0].(*AtMostOneOfGroup)
	if !ok {
		t.Fatalf("child type = %T, want *AtMostOneOfGroup", root.Children[0])
	}
	if got := group.String(); got != input {
		t.Fatalf("String() = %q, want %q", got, input)
	}
	if got := group.Atoms(); !reflect.DeepEqual(got, []string{"foo", "baz"}) {
		t.Fatalf("Atoms() = %#v", got)
	}
}

func TestAtMostOneOfGroupSatisfactionAndMetadata(t *testing.T) {
	node, err := Parse("?? ( dev-libs/foo dev-libs/bar )")
	if err != nil {
		t.Fatal(err)
	}
	foo, _ := atom.Parse("dev-libs/foo-1")
	bar, _ := atom.Parse("dev-libs/bar-1")
	tests := []struct {
		name      string
		installed map[string]*atom.Atom
		want      bool
	}{
		{name: "none", installed: map[string]*atom.Atom{}, want: true},
		{name: "one", installed: map[string]*atom.Atom{"dev-libs/foo": foo}, want: true},
		{name: "two", installed: map[string]*atom.Atom{"dev-libs/foo": foo, "dev-libs/bar": bar}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, _ := Satisfy(node, test.installed, nil)
			if got != test.want {
				t.Fatalf("Satisfy() = %v, want %v", got, test.want)
			}
		})
	}

	meta := CollectMeta(node)
	if len(meta) != 2 || meta[0].Atom != "dev-libs/foo" || meta[1].Atom != "dev-libs/bar" {
		t.Fatalf("CollectMeta() = %#v", meta)
	}
}

func TestParseWithWhitespace(t *testing.T) {
	tests := []string{
		"a/b\tc/d",
		"a/b\nc/d",
		"a/b\r\nc/d",
		"  a/b   c/d  ",
		"a/b\n\nc/d\n",
	}

	for _, input := range tests {
		t.Run(fmt.Sprintf("%q", input), func(t *testing.T) {
			node, err := Parse(input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			root := node.(*AllOfGroup)
			if len(root.Children) != 2 {
				t.Fatalf("expected 2 children, got %d", len(root.Children))
			}
		})
	}
}

func TestAnyOfGroup(t *testing.T) {
	input := "|| ( dev-libs/foo dev-libs/bar )"

	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	root := node.(*AllOfGroup)
	if len(root.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(root.Children))
	}

	aog, ok := root.Children[0].(*AnyOfGroup)
	if !ok {
		t.Fatalf("expected *AnyOfGroup, got %T", root.Children[0])
	}
	if len(aog.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(aog.Children))
	}

	if aog.Children[0].(*AtomDep).Atom != "dev-libs/foo" {
		t.Errorf("expected dev-libs/foo, got %s", aog.Children[0].(*AtomDep).Atom)
	}
	if aog.Children[1].(*AtomDep).Atom != "dev-libs/bar" {
		t.Errorf("expected dev-libs/bar, got %s", aog.Children[1].(*AtomDep).Atom)
	}
}

func TestAnyOfGroupWithThreeAtoms(t *testing.T) {
	input := "|| ( a/b c/d e/f )"
	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	root := node.(*AllOfGroup)
	aog := root.Children[0].(*AnyOfGroup)
	if len(aog.Children) != 3 {
		t.Fatalf("expected 3 children, got %d", len(aog.Children))
	}
}

func TestAnyOfGroupNested(t *testing.T) {
	input := "|| ( a/b || ( c/d e/f ) )"
	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	root := node.(*AllOfGroup)
	aog := root.Children[0].(*AnyOfGroup)
	if len(aog.Children) != 2 {
		t.Fatalf("expected 2 children in outer any-of, got %d", len(aog.Children))
	}
	inner, ok := aog.Children[1].(*AnyOfGroup)
	if !ok {
		t.Fatalf("expected *AnyOfGroup, got %T", aog.Children[1])
	}
	if len(inner.Children) != 2 {
		t.Fatalf("expected 2 children in inner any-of, got %d", len(inner.Children))
	}
}

func TestAllOfGroupExplicit(t *testing.T) {
	input := "( dev-libs/foo dev-libs/bar )"

	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	root := node.(*AllOfGroup)
	if len(root.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(root.Children))
	}

	aog, ok := root.Children[0].(*AllOfGroup)
	if !ok {
		t.Fatalf("expected *AllOfGroup, got %T", root.Children[0])
	}
	if aog.Implicit {
		t.Fatal("explicit group should not be implicit")
	}
	if len(aog.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(aog.Children))
	}
}

func TestAllOfGroupEmpty(t *testing.T) {
	input := "( )"
	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	root := node.(*AllOfGroup)
	aog := root.Children[0].(*AllOfGroup)
	if len(aog.Children) != 0 {
		t.Fatalf("expected 0 children, got %d", len(aog.Children))
	}
	if aog.String() != "( )" {
		t.Errorf("expected '( )', got %q", aog.String())
	}
}

func TestUseConditionalPositive(t *testing.T) {
	input := "doc? ( app-doc/doxygen )"

	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	root := node.(*AllOfGroup)
	uc, ok := root.Children[0].(*UseConditional)
	if !ok {
		t.Fatalf("expected *UseConditional, got %T", root.Children[0])
	}
	if uc.Flag != "doc" {
		t.Errorf("expected flag 'doc', got %q", uc.Flag)
	}
	if len(uc.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(uc.Children))
	}
	if uc.Children[0].(*AtomDep).Atom != "app-doc/doxygen" {
		t.Errorf("expected app-doc/doxygen, got %s", uc.Children[0].(*AtomDep).Atom)
	}
}

func TestAtomUseDefaultsDoNotSplitDependencyToken(t *testing.T) {
	input := ">=sys-apps/dbus-1.5[abi_x86_32(-)?,abi_x86_64(-)?]"
	root, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	atoms := root.Atoms()
	if len(atoms) != 1 || atoms[0] != input {
		t.Fatalf("atoms = %v, want %q", atoms, input)
	}
}

func TestUseConditionalNegative(t *testing.T) {
	input := "!other-package? ( dev-libs/optional )"

	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	root := node.(*AllOfGroup)
	uc, ok := root.Children[0].(*UseConditional)
	if !ok {
		t.Fatalf("expected *UseConditional, got %T", root.Children[0])
	}
	if uc.Flag != "!other-package" {
		t.Errorf("expected flag '!other-package', got %q", uc.Flag)
	}
	if len(uc.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(uc.Children))
	}
}

func TestUseConditionalMultipleChildren(t *testing.T) {
	input := "x? ( a/b c/d e/f )"

	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	root := node.(*AllOfGroup)
	uc := root.Children[0].(*UseConditional)
	if len(uc.Children) != 3 {
		t.Fatalf("expected 3 children, got %d", len(uc.Children))
	}
}

func TestUseConditionalNested(t *testing.T) {
	input := "a? ( b? ( c/d ) e/f )"

	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	root := node.(*AllOfGroup)
	uc := root.Children[0].(*UseConditional)
	if uc.Flag != "a" {
		t.Errorf("expected flag 'a', got %q", uc.Flag)
	}
	if len(uc.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(uc.Children))
	}
	inner, ok := uc.Children[0].(*UseConditional)
	if !ok {
		t.Fatalf("expected *UseConditional, got %T", uc.Children[0])
	}
	if inner.Flag != "b" {
		t.Errorf("expected flag 'b', got %q", inner.Flag)
	}
}

func TestHardBlocker(t *testing.T) {
	input := "!dev-lang/rust"

	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	root := node.(*AllOfGroup)
	b, ok := root.Children[0].(*Block)
	if !ok {
		t.Fatalf("expected *Block, got %T", root.Children[0])
	}
	if b.Atom != "dev-lang/rust" {
		t.Errorf("expected atom 'dev-lang/rust', got %q", b.Atom)
	}
	if b.String() != "!dev-lang/rust" {
		t.Errorf("expected '!dev-lang/rust', got %q", b.String())
	}
}

func TestHardBlockerMixedWithAtoms(t *testing.T) {
	input := ">=dev-lang/python-3.10 !dev-lang/rust sys-apps/gawk"

	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	root := node.(*AllOfGroup)
	if len(root.Children) != 3 {
		t.Fatalf("expected 3 children, got %d", len(root.Children))
	}
	_, ok1 := root.Children[0].(*AtomDep)
	_, ok2 := root.Children[1].(*Block)
	_, ok3 := root.Children[2].(*AtomDep)
	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("expected AtomDep, Block, AtomDep; got %T, %T, %T",
			root.Children[0], root.Children[1], root.Children[2])
	}
}

func TestWeakBlocker(t *testing.T) {
	input := "!!dev-lang/rust"

	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	root := node.(*AllOfGroup)
	wb, ok := root.Children[0].(*WeakBlock)
	if !ok {
		t.Fatalf("expected *WeakBlock, got %T", root.Children[0])
	}
	if wb.Atom != "dev-lang/rust" {
		t.Errorf("expected atom 'dev-lang/rust', got %q", wb.Atom)
	}
	if wb.String() != "!!dev-lang/rust" {
		t.Errorf("expected '!!dev-lang/rust', got %q", wb.String())
	}
}

func TestNestedGroupAnyOfInsideUseConditional(t *testing.T) {
	input := "python? ( || ( dev-python/foo dev-python/bar ) )"

	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	root := node.(*AllOfGroup)
	uc := root.Children[0].(*UseConditional)
	if len(uc.Children) != 1 {
		t.Fatalf("expected 1 child in use-cond, got %d", len(uc.Children))
	}
	aog, ok := uc.Children[0].(*AnyOfGroup)
	if !ok {
		t.Fatalf("expected *AnyOfGroup, got %T", uc.Children[0])
	}
	if len(aog.Children) != 2 {
		t.Fatalf("expected 2 children in any-of, got %d", len(aog.Children))
	}
}

func TestSlotSubslotConstraints(t *testing.T) {
	tests := []string{
		"dev-lang/python:3.11",
		"dev-lang/python:3.11/3.11",
		"dev-lang/python:=",
		"dev-lang/python:*",
		"dev-lang/python:3.11=",
		"dev-lang/python:3.11/3.11=",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			node, err := Parse(input)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", input, err)
			}
			root := node.(*AllOfGroup)
			ad := root.Children[0].(*AtomDep)
			if ad.Atom != input {
				t.Errorf("expected atom %q, got %q", input, ad.Atom)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	inputs := []string{
		"dev-lang/python",
		">=dev-lang/python-3.10",
		"dev-lang/python:3.11/3.11",
		"|| ( dev-libs/foo dev-libs/bar )",
		"dev-libs/foo dev-libs/bar",
		"( a/b c/d )",
		"( a/b ) c/d",
		"doc? ( app-doc/doxygen )",
		"!other-package? ( dev-libs/optional )",
		"!dev-lang/rust",
		"!!dev-lang/rust",
		"python? ( || ( dev-python/foo dev-python/bar ) )",
		"a? ( b? ( c/d ) e/f )",
		">=dev-lang/python-3.10 !dev-lang/rust sys-apps/gawk",
		"|| ( a/b || ( c/d e/f ) )",
		"x? ( a/b z? ( c/d ) ) !y/z",
	}

	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			n1, err := Parse(input)
			if err != nil {
				t.Fatalf("first Parse(%q): %v", input, err)
			}
			rendered := n1.String()
			n2, err := Parse(rendered)
			if err != nil {
				t.Fatalf("second Parse(%q) from %q: %v", input, rendered, err)
			}
			if !nodesEqual(n1, n2) {
				t.Errorf("round-trip mismatch for %q:\n  first:  %s (%T)\n  second: %s (%T)",
					input, renderTree(n1), n1, renderTree(n2), n2)
			}
		})
	}
}

func TestRoundTripComplexRealWorld(t *testing.T) {
	input := `>=dev-lang/python-3.10 || ( dev-libs/foo dev-libs/bar ) sys-apps/gawk doc? ( app-doc/doxygen ) !other-package? ( dev-libs/optional ) !dev-lang/rust`

	n1, err := Parse(input)
	if err != nil {
		t.Fatalf("first Parse: %v", err)
	}
	rendered := n1.String()
	n2, err := Parse(rendered)
	if err != nil {
		t.Fatalf("second Parse %q: %v", rendered, err)
	}
	if !nodesEqual(n1, n2) {
		t.Errorf("round-trip mismatch:\n  first:  %s\n  second: %s", renderTree(n1), renderTree(n2))
	}
}

func TestEmptyString(t *testing.T) {
	tests := []string{"", "   ", "\n\t  "}

	for _, input := range tests {
		t.Run(fmt.Sprintf("%q", input), func(t *testing.T) {
			node, err := Parse(input)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if node != nil {
				t.Errorf("expected nil node for empty input, got %T", node)
			}
		})
	}
}

func TestMalformedUnbalancedParens(t *testing.T) {
	tests := []string{
		"( a/b",
		"a/b )",
		"( a/b ( c/d )",
		"( a/b c/d",
		"|| ( a/b",
		"a? ( b/c",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			_, err := Parse(input)
			if err == nil {
				t.Errorf("expected error for %q, got nil", input)
			}
		})
	}
}

func TestMalformedGarbageAfterAnyOf(t *testing.T) {
	_, err := Parse("|| a/b")
	if err == nil {
		t.Fatal("expected error for '|| a/b' without parens")
	}
}

func TestMalformedMissingCloseParen(t *testing.T) {
	_, err := Parse("|| ( a/b c/d")
	if err == nil {
		t.Fatal("expected error for missing close paren")
	}
}

func TestMalformedUseConditionalWithoutParens(t *testing.T) {
	_, err := Parse("use? a/b")
	if err == nil {
		t.Fatal("expected error for use conditional without parens")
	}
}

func TestMalformedLonelyQuestionMark(t *testing.T) {
	_, err := Parse("? ( a/b )")
	if err == nil {
		t.Fatal("expected error for lonely '?'")
	}
}

func TestMalformedEmptyBlockAtom(t *testing.T) {
	_, err := Parse("!")
	if err == nil {
		t.Fatal("expected error for empty block")
	}
}

func TestMalformedEmptyWeakBlockAtom(t *testing.T) {
	_, err := Parse("!!")
	if err == nil {
		t.Fatal("expected error for empty weak block")
	}
}

func TestAdversarialDeeplyNestedGroups(t *testing.T) {
	depth := 100
	var b strings.Builder
	for i := 0; i < depth; i++ {
		b.WriteString("( ")
	}
	b.WriteString("a/b")
	for i := 0; i < depth; i++ {
		b.WriteString(" )")
	}

	node, err := Parse(b.String())
	if err != nil {
		t.Fatalf("deep nesting parse failed: %v", err)
	}

	cur := node.(*AllOfGroup).Children[0]
	for i := 0; i < depth; i++ {
		g, ok := cur.(*AllOfGroup)
		if !ok {
			t.Fatalf("at depth %d: expected *AllOfGroup, got %T", i, cur)
		}
		if len(g.Children) != 1 {
			t.Fatalf("at depth %d: expected 1 child, got %d", i, len(g.Children))
		}
		cur = g.Children[0]
	}
	if ad, ok := cur.(*AtomDep); !ok || ad.Atom != "a/b" {
		t.Fatalf("expected leaf AtomDep(a/b), got %T %v", cur, cur)
	}
}

func TestAdversarialLargeDepstring(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString("dev-lang/foo")
	}

	node, err := Parse(b.String())
	if err != nil {
		t.Fatalf("large depstring parse failed: %v", err)
	}
	root := node.(*AllOfGroup)
	if len(root.Children) != 5000 {
		t.Fatalf("expected 5000 children, got %d", len(root.Children))
	}
}

func TestAdversarialNullBytes(t *testing.T) {
	input := "dev-lang/foo\x00dev-lang/bar"
	node, err := Parse(input)
	if err != nil {
		t.Fatalf("null byte parse failed: %v", err)
	}
	root := node.(*AllOfGroup)
	if len(root.Children) < 1 {
		t.Fatal("expected at least 1 child")
	}
}

func TestAtomExtraction(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{
			"dev-lang/python",
			[]string{"dev-lang/python"},
		},
		{
			">=dev-lang/python-3.10 sys-apps/gawk",
			[]string{">=dev-lang/python-3.10", "sys-apps/gawk"},
		},
		{
			"|| ( dev-libs/foo dev-libs/bar )",
			[]string{"dev-libs/foo", "dev-libs/bar"},
		},
		{
			"doc? ( app-doc/doxygen )",
			[]string{"app-doc/doxygen"},
		},
		{
			"!dev-lang/rust",
			[]string{"dev-lang/rust"},
		},
		{
			"!!dev-lang/rust",
			[]string{"dev-lang/rust"},
		},
		{
			"|| ( a/b c/d ) dev-lang/python !x/y python? ( || ( p/q r/s ) )",
			[]string{"a/b", "c/d", "dev-lang/python", "x/y", "p/q", "r/s"},
		},
		{
			"a? ( b? ( c/d ) e/f ) !z/w || ( g/h i/j )",
			[]string{"c/d", "e/f", "z/w", "g/h", "i/j"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			node, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.input, err)
			}
			atoms := node.Atoms()
			if !reflect.DeepEqual(atoms, tt.expected) {
				t.Errorf("Atoms mismatch for %q:\n  got:      %v\n  expected: %v",
					tt.input, atoms, tt.expected)
			}
		})
	}
}

func TestSatisfyBasicAtom(t *testing.T) {
	installed := map[string]*atom.Atom{
		"dev-lang/python": {Category: "dev-lang", Package: "python", Version: &atom.Version{Raw: "3.11", Numbers: []int{3, 11}, Revision: -1}},
	}
	flags := map[string]bool{}

	sat, missing := Satisfy(mustParse(t, "dev-lang/python"), installed, flags)
	if !sat {
		t.Error("expected satisfied")
	}
	if len(missing) != 0 {
		t.Errorf("expected no missing, got %v", missing)
	}
}

func TestSatisfyMissingAtom(t *testing.T) {
	installed := map[string]*atom.Atom{}
	flags := map[string]bool{}

	sat, missing := Satisfy(mustParse(t, "dev-lang/python"), installed, flags)
	if sat {
		t.Error("expected not satisfied")
	}
	if len(missing) == 0 {
		t.Error("expected missing atoms")
	}
}

func TestSatisfyVersionConstraint(t *testing.T) {
	installed := map[string]*atom.Atom{
		"dev-lang/python": {Category: "dev-lang", Package: "python", Version: &atom.Version{Raw: "3.10", Numbers: []int{3, 10}, Revision: -1}},
	}

	tests := []struct {
		dep       string
		satisfied bool
	}{
		{">=dev-lang/python-3.10", true},
		{">=dev-lang/python-3.11", false},
		{">dev-lang/python-3.9", true},
		{">dev-lang/python-3.10", false},
		{"<dev-lang/python-3.11", true},
		{"<dev-lang/python-3.10", false},
		{"<=dev-lang/python-3.10", true},
		{"=dev-lang/python-3.10", true},
		{"=dev-lang/python-3.11", false},
	}

	for _, tt := range tests {
		t.Run(tt.dep, func(t *testing.T) {
			sat, _ := Satisfy(mustParse(t, tt.dep), installed, nil)
			if sat != tt.satisfied {
				t.Errorf("expected satisfied=%v, got %v", tt.satisfied, sat)
			}
		})
	}
}

func TestSatisfyAllOfGroup(t *testing.T) {
	installed := map[string]*atom.Atom{
		"dev-lang/python": {Category: "dev-lang", Package: "python"},
		"sys-apps/gawk":   {Category: "sys-apps", Package: "gawk"},
	}

	sat, _ := Satisfy(mustParse(t, "dev-lang/python sys-apps/gawk"), installed, nil)
	if !sat {
		t.Error("expected satisfied")
	}

	sat, missing := Satisfy(mustParse(t, "dev-lang/python dev-lang/rust"), installed, nil)
	if sat {
		t.Error("expected not satisfied")
	}
	if len(missing) != 1 {
		t.Errorf("expected 1 missing, got %v", missing)
	}
}

func TestSatisfyAnyOfGroup(t *testing.T) {
	installed := map[string]*atom.Atom{
		"dev-libs/bar": {Category: "dev-libs", Package: "bar"},
	}

	sat, _ := Satisfy(mustParse(t, "|| ( dev-libs/foo dev-libs/bar )"), installed, nil)
	if !sat {
		t.Error("expected satisfied (bar installed)")
	}

	installed2 := map[string]*atom.Atom{}
	sat, missing := Satisfy(mustParse(t, "|| ( dev-libs/foo dev-libs/bar )"), installed2, nil)
	if sat {
		t.Error("expected not satisfied")
	}
	if len(missing) != 2 {
		t.Errorf("expected 2 missing, got %d: %v", len(missing), missing)
	}
}

func TestSatisfyUseConditional(t *testing.T) {
	installed := map[string]*atom.Atom{
		"app-doc/doxygen": {Category: "app-doc", Package: "doxygen"},
	}

	sat, _ := Satisfy(mustParse(t, "doc? ( app-doc/doxygen )"), installed, map[string]bool{"doc": true})
	if !sat {
		t.Error("expected satisfied with doc=true and doxygen installed")
	}

	sat, _ = Satisfy(mustParse(t, "doc? ( app-doc/doxygen )"), map[string]*atom.Atom{}, map[string]bool{"doc": false})
	if !sat {
		t.Error("expected satisfied with doc=false (dep not required)")
	}

	sat, missing := Satisfy(mustParse(t, "doc? ( app-doc/doxygen )"), map[string]*atom.Atom{}, map[string]bool{"doc": true})
	if sat {
		t.Error("expected not satisfied with doc=true but doxygen not installed")
	}
	if len(missing) != 1 {
		t.Errorf("expected 1 missing, got %v", missing)
	}
}

func TestSatisfyNegativeUseConditional(t *testing.T) {
	installed := map[string]*atom.Atom{
		"x11-libs/libxcb": {Category: "x11-libs", Package: "libxcb"},
	}

	sat, _ := Satisfy(mustParse(t, "!x? ( x11-libs/libxcb )"), installed, map[string]bool{"x": false})
	if !sat {
		t.Error("expected satisfied with x=false (negative conditional active)")
	}

	sat, _ = Satisfy(mustParse(t, "!x? ( x11-libs/libxcb )"), installed, map[string]bool{"x": true})
	if !sat {
		t.Error("expected satisfied with x=true (negative conditional inactive)")
	}

	sat, missing := Satisfy(mustParse(t, "!x? ( x11-libs/libxcb )"), map[string]*atom.Atom{}, map[string]bool{"x": false})
	if sat {
		t.Error("expected not satisfied: x=false but libxcb not installed")
	}
	if len(missing) != 1 {
		t.Errorf("expected 1 missing, got %v", missing)
	}
}

func TestSatisfyHardBlocker(t *testing.T) {
	installed := map[string]*atom.Atom{
		"dev-lang/rust": {Category: "dev-lang", Package: "rust"},
	}

	sat, _ := Satisfy(mustParse(t, "!dev-lang/rust"), installed, nil)
	if sat {
		t.Error("expected NOT satisfied when blocker atom is installed")
	}

	sat, _ = Satisfy(mustParse(t, "!dev-lang/rust"), map[string]*atom.Atom{}, nil)
	if !sat {
		t.Error("expected satisfied when blocker atom is not installed")
	}
}

func TestSatisfyWeakBlocker(t *testing.T) {
	installed := map[string]*atom.Atom{
		"dev-lang/rust": {Category: "dev-lang", Package: "rust"},
	}

	sat, _ := Satisfy(mustParse(t, "!!dev-lang/rust"), installed, nil)
	if !sat {
		t.Error("weak blocker should always be satisfied")
	}
}

func TestSatisfyNestedComplex(t *testing.T) {
	installed := map[string]*atom.Atom{
		"dev-libs/foo": {Category: "dev-libs", Package: "foo"},
	}
	flags := map[string]bool{"python": true}

	sat, _ := Satisfy(mustParse(t, "python? ( || ( dev-libs/foo dev-libs/bar ) )"), installed, flags)
	if !sat {
		t.Error("expected satisfied: python=true, foo satisfies the any-of")
	}

	sat, missing := Satisfy(mustParse(t, "python? ( || ( dev-python/foo dev-python/bar ) )"), installed, flags)
	if sat {
		t.Error("expected not satisfied: python=true but neither pkg installed")
	}
	if len(missing) != 2 {
		t.Errorf("expected 2 missing, got %v", missing)
	}
}

func TestSatisfyNilNode(t *testing.T) {
	sat, missing := Satisfy(nil, nil, nil)
	if !sat {
		t.Error("nil node should be satisfied")
	}
	if len(missing) != 0 {
		t.Error("nil node should have no missing")
	}
}

func TestStringMethods(t *testing.T) {
	tests := []struct {
		node     DepNode
		expected string
	}{
		{&AtomDep{Atom: "dev-lang/python"}, "dev-lang/python"},
		{&AllOfGroup{Children: []DepNode{&AtomDep{Atom: "a"}, &AtomDep{Atom: "b"}}, Implicit: false}, "( a b )"},
		{&AllOfGroup{Children: []DepNode{&AtomDep{Atom: "a"}, &AtomDep{Atom: "b"}}, Implicit: true}, "a b"},
		{&AllOfGroup{Children: nil, Implicit: false}, "( )"},
		{&AnyOfGroup{Children: []DepNode{&AtomDep{Atom: "a"}, &AtomDep{Atom: "b"}}}, "|| ( a b )"},
		{&AnyOfGroup{Children: nil}, "|| ( )"},
		{&XorOfGroup{Children: []DepNode{&AtomDep{Atom: "a"}, &AtomDep{Atom: "b"}}}, "^^ ( a b )"},
		{&XorOfGroup{Children: nil}, "^^ ( )"},
		{&UseConditional{Flag: "doc", Children: []DepNode{&AtomDep{Atom: "app-doc/foo"}}}, "doc? ( app-doc/foo )"},
		{&UseConditional{Flag: "!other", Children: []DepNode{&AtomDep{Atom: "dev-libs/foo"}}}, "!other? ( dev-libs/foo )"},
		{&UseConditional{Flag: "doc", Children: nil}, "doc? ( )"},
		{&Block{Atom: "dev-lang/rust"}, "!dev-lang/rust"},
		{&WeakBlock{Atom: "dev-lang/rust"}, "!!dev-lang/rust"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := tt.node.String()
			if got != tt.expected {
				t.Errorf("String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestStringWhitespacePreservation(t *testing.T) {
	input := "a/b\nc/d\ne/f"
	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rendered := node.String()
	// String() uses single spaces between children, newlines are collapsed
	if strings.Count(rendered, "a/b") != 1 || strings.Count(rendered, "c/d") != 1 || strings.Count(rendered, "e/f") != 1 {
		t.Errorf("rendered %q missing expected atoms", rendered)
	}
}

func TestRealWorldPortageExamples(t *testing.T) {
	examples := []string{
		">=dev-lang/python-3.10",
		"|| ( >=dev-libs/foo-1.0 dev-libs/bar )",
		"sys-apps/gawk",
		"doc? ( app-doc/doxygen )",
		"!other-package? ( dev-libs/optional )",
		"!dev-lang/rust",
		"=dev-libs/foo-1.2.3-r4",
		"~dev-libs/foo-1.2.3",
		">=dev-libs/foo-1.0:0/1",
		"dev-libs/foo:=",
		"dev-libs/foo:*",
		"dev-libs/foo::gentoo",
		"dev-libs/foo[ssl,xml]",
		"dev-libs/foo[-ssl,xml]",
	}

	for _, ex := range examples {
		t.Run(ex, func(t *testing.T) {
			node, err := Parse(ex)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", ex, err)
			}
			if node == nil {
				t.Fatal("expected non-nil node")
			}
			if len(node.Atoms()) == 0 {
				t.Errorf("expected Atoms() >= 1 for %q, got 0", ex)
			}
		})
	}
}

func TestRealWorldComplexDepstring(t *testing.T) {
	input := `
	>=dev-lang/python-3.10
	|| (
		>=dev-libs/foo-1.0
		dev-libs/bar
	)
	sys-apps/gawk
	doc? (
		app-doc/doxygen
		app-doc/sphinx
	)
	!other-package? ( dev-libs/optional )
	!dev-lang/rust
`

	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	root := node.(*AllOfGroup)
	if len(root.Children) != 6 {
		t.Fatalf("expected 6 children, got %d", len(root.Children))
	}

	// child 0: >=dev-lang/python-3.10
	ad0 := root.Children[0].(*AtomDep)
	if ad0.Atom != ">=dev-lang/python-3.10" {
		t.Errorf("child 0: %q", ad0.Atom)
	}

	// child 1: || ( ... )
	aog := root.Children[1].(*AnyOfGroup)
	if len(aog.Children) != 2 {
		t.Errorf("any-of: expected 2 children, got %d", len(aog.Children))
	}
	if aog.Children[0].(*AtomDep).Atom != ">=dev-libs/foo-1.0" {
		t.Errorf("any-of child 0: %q", aog.Children[0].(*AtomDep).Atom)
	}
	if aog.Children[1].(*AtomDep).Atom != "dev-libs/bar" {
		t.Errorf("any-of child 1: %q", aog.Children[1].(*AtomDep).Atom)
	}

	// child 2: sys-apps/gawk
	ad2 := root.Children[2].(*AtomDep)
	if ad2.Atom != "sys-apps/gawk" {
		t.Errorf("child 2: %q", ad2.Atom)
	}

	// child 3: doc? ( ... )
	uc3 := root.Children[3].(*UseConditional)
	if uc3.Flag != "doc" {
		t.Errorf("child 3 flag: %q", uc3.Flag)
	}
	if len(uc3.Children) != 2 {
		t.Errorf("child 3 children: expected 2, got %d", len(uc3.Children))
	}

	// child 4: !other-package? ( ... )
	uc4 := root.Children[4].(*UseConditional)
	if uc4.Flag != "!other-package" {
		t.Errorf("child 4 flag: %q", uc4.Flag)
	}

	// child 5: !dev-lang/rust
	b5 := root.Children[5].(*Block)
	if b5.Atom != "dev-lang/rust" {
		t.Errorf("child 5: %q", b5.Atom)
	}
}

func TestSatisfySlotConstraint(t *testing.T) {
	installed := map[string]*atom.Atom{
		"dev-lang/python": {Category: "dev-lang", Package: "python", Slot: "3.11"},
	}

	sat, _ := Satisfy(mustParse(t, "dev-lang/python:3.11"), installed, nil)
	if !sat {
		t.Error("expected satisfied for matching slot")
	}

	sat, _ = Satisfy(mustParse(t, "dev-lang/python:3.10"), installed, nil)
	if sat {
		t.Error("expected not satisfied for mismatched slot")
	}
}

func TestAtomsOnEachNodeType(t *testing.T) {
	tests := []struct {
		name     string
		node     DepNode
		expected []string
	}{
		{"AtomDep", &AtomDep{Atom: "dev-lang/python"}, []string{"dev-lang/python"}},
		{"AllOfGroup", &AllOfGroup{Children: []DepNode{&AtomDep{Atom: "a"}, &AtomDep{Atom: "b"}}}, []string{"a", "b"}},
		{"AnyOfGroup", &AnyOfGroup{Children: []DepNode{&AtomDep{Atom: "a"}, &AtomDep{Atom: "b"}}}, []string{"a", "b"}},
		{"XorOfGroup", &XorOfGroup{Children: []DepNode{&AtomDep{Atom: "a"}, &AtomDep{Atom: "b"}}}, []string{"a", "b"}},
		{"UseConditional", &UseConditional{Flag: "x", Children: []DepNode{&AtomDep{Atom: "a"}}}, []string{"a"}},
		{"Block", &Block{Atom: "dev-lang/rust"}, []string{"dev-lang/rust"}},
		{"WeakBlock", &WeakBlock{Atom: "dev-lang/rust"}, []string{"dev-lang/rust"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.node.Atoms()
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("Atoms() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestParseXorOfGroup(t *testing.T) {
	input := "^^ ( a b )"
	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	root := node.(*AllOfGroup)
	if len(root.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(root.Children))
	}
	xog, ok := root.Children[0].(*XorOfGroup)
	if !ok {
		t.Fatalf("expected *XorOfGroup, got %T", root.Children[0])
	}
	if len(xog.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(xog.Children))
	}
	if xog.Children[0].(*AtomDep).Atom != "a" {
		t.Errorf("expected a, got %s", xog.Children[0].(*AtomDep).Atom)
	}
	if xog.Children[1].(*AtomDep).Atom != "b" {
		t.Errorf("expected b, got %s", xog.Children[1].(*AtomDep).Atom)
	}
}

func TestParseXorOfGroupNestedInsideAnyOfGroup(t *testing.T) {
	input := "|| ( a/b ^^ ( c/d e/f ) )"
	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	root := node.(*AllOfGroup)
	aog := root.Children[0].(*AnyOfGroup)
	if len(aog.Children) != 2 {
		t.Fatalf("expected 2 children in any-of, got %d", len(aog.Children))
	}
	if aog.Children[0].(*AtomDep).Atom != "a/b" {
		t.Errorf("expected a/b, got %s", aog.Children[0].(*AtomDep).Atom)
	}
	xog, ok := aog.Children[1].(*XorOfGroup)
	if !ok {
		t.Fatalf("expected *XorOfGroup nested inside AnyOfGroup, got %T", aog.Children[1])
	}
	if len(xog.Children) != 2 {
		t.Fatalf("expected 2 children in xor-of, got %d", len(xog.Children))
	}
	if xog.Children[0].(*AtomDep).Atom != "c/d" {
		t.Errorf("expected c/d, got %s", xog.Children[0].(*AtomDep).Atom)
	}
	if xog.Children[1].(*AtomDep).Atom != "e/f" {
		t.Errorf("expected e/f, got %s", xog.Children[1].(*AtomDep).Atom)
	}
}

func TestCollectMeta_ComplexDepstring(t *testing.T) {
	input := ">=dev-lang/python-3.10 sys-apps/gawk doc? ( app-doc/doxygen ) !dev-lang/rust"
	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	metas := CollectMeta(node)
	if len(metas) != 4 {
		t.Fatalf("expected 4 meta entries, got %d", len(metas))
	}

	findMeta := func(atom string) *AtomMeta {
		for i, m := range metas {
			if m.Atom == atom {
				return &metas[i]
			}
		}
		return nil
	}

	pythonMeta := findMeta(">=dev-lang/python-3.10")
	if pythonMeta == nil {
		t.Fatal("missing python meta")
	}
	if pythonMeta.AnyOfGroup {
		t.Error("python should not be in any-of group")
	}
	if pythonMeta.Condition != "" {
		t.Errorf("python condition should be empty, got %q", pythonMeta.Condition)
	}

	gawkMeta := findMeta("sys-apps/gawk")
	if gawkMeta == nil {
		t.Fatal("missing gawk meta")
	}

	doxygenMeta := findMeta("app-doc/doxygen")
	if doxygenMeta == nil {
		t.Fatal("missing doxygen meta")
	}
	if doxygenMeta.Condition != "doc" {
		t.Errorf("expected condition 'doc', got %q", doxygenMeta.Condition)
	}

	rustMeta := findMeta("dev-lang/rust")
	if rustMeta == nil {
		t.Fatal("missing rust meta")
	}
	if !rustMeta.Block {
		t.Error("rust should be a block")
	}
}

func TestCollectMeta_XorOfGroup(t *testing.T) {
	input := "^^ ( dev-libs/a dev-libs/b )"
	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	metas := CollectMeta(node)
	if len(metas) != 2 {
		t.Fatalf("expected 2 meta entries, got %d", len(metas))
	}
	if metas[0].Atom != "dev-libs/a" {
		t.Errorf("expected dev-libs/a, got %s", metas[0].Atom)
	}
	if metas[1].Atom != "dev-libs/b" {
		t.Errorf("expected dev-libs/b, got %s", metas[1].Atom)
	}
}

func TestCollectMeta_XorOfGroupNestedInUseConditional(t *testing.T) {
	input := "x? ( ^^ ( dev-libs/a dev-libs/b ) )"
	node, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	metas := CollectMeta(node)
	if len(metas) != 2 {
		t.Fatalf("expected 2 meta entries, got %d", len(metas))
	}
	if metas[0].Condition != "x" {
		t.Errorf("expected condition 'x', got %q", metas[0].Condition)
	}
	if metas[1].Condition != "x" {
		t.Errorf("expected condition 'x', got %q", metas[1].Condition)
	}
}

func TestSatisfy_XorOfGroup_NeitherSatisfied(t *testing.T) {
	installed := map[string]*atom.Atom{}
	flags := map[string]bool{}

	sat, missing := Satisfy(mustParse(t, "^^ ( a/b c/d )"), installed, flags)
	if sat {
		t.Error("expected not satisfied when neither side is installed")
	}
	if len(missing) != 2 {
		t.Errorf("expected 2 missing, got %d: %v", len(missing), missing)
	}
}

func TestSatisfy_XorOfGroup_BothSidesTrue(t *testing.T) {
	installed := map[string]*atom.Atom{
		"dev-libs/a": {Category: "dev-libs", Package: "a"},
		"dev-libs/b": {Category: "dev-libs", Package: "b"},
	}
	flags := map[string]bool{}

	sat, _ := Satisfy(mustParse(t, "^^ ( dev-libs/a dev-libs/b )"), installed, flags)
	if sat {
		t.Error("expected not satisfied when both sides are installed")
	}
}

func TestSatisfy_XorOfGroup_ExactlyOneTrue(t *testing.T) {
	installed := map[string]*atom.Atom{
		"dev-libs/a": {Category: "dev-libs", Package: "a"},
	}
	flags := map[string]bool{}

	sat, missing := Satisfy(mustParse(t, "^^ ( dev-libs/a dev-libs/b )"), installed, flags)
	if !sat {
		t.Error("expected satisfied when exactly one side is installed")
	}
	if len(missing) != 0 {
		t.Errorf("expected 0 missing, got %d: %v", len(missing), missing)
	}
}

// mustParse is a test helper that parses a depstring or panics.
func mustParse(t *testing.T, input string) DepNode {
	t.Helper()
	node, err := Parse(input)
	if err != nil {
		t.Fatalf("mustParse(%q): %v", input, err)
	}
	return node
}

// renderTree returns a human-readable tree representation for debugging.
func renderTree(node DepNode) string {
	if node == nil {
		return "<nil>"
	}

	var b strings.Builder
	renderTreeInner(&b, node, 0)
	return b.String()
}

func renderTreeInner(b *strings.Builder, node DepNode, indent int) {
	prefix := strings.Repeat("  ", indent)
	switch n := node.(type) {
	case *AtomDep:
		b.WriteString(prefix)
		b.WriteString("AtomDep(")
		b.WriteString(n.Atom)
		b.WriteString(")")
	case *AllOfGroup:
		b.WriteString(prefix)
		if n.Implicit {
			b.WriteString("AllOfGroup(implicit):\n")
		} else {
			b.WriteString("AllOfGroup:\n")
		}
		for _, c := range n.Children {
			renderTreeInner(b, c, indent+1)
		}
	case *AnyOfGroup:
		b.WriteString(prefix)
		b.WriteString("AnyOfGroup:\n")
		for _, c := range n.Children {
			renderTreeInner(b, c, indent+1)
		}
	case *XorOfGroup:
		b.WriteString(prefix)
		b.WriteString("XorOfGroup:\n")
		for _, c := range n.Children {
			renderTreeInner(b, c, indent+1)
		}
	case *UseConditional:
		b.WriteString(prefix)
		b.WriteString("UseConditional(flag=")
		b.WriteString(n.Flag)
		b.WriteString("):\n")
		for _, c := range n.Children {
			renderTreeInner(b, c, indent+1)
		}
	case *Block:
		b.WriteString(prefix)
		b.WriteString("Block(atom=")
		b.WriteString(n.Atom)
		b.WriteString(")")
	case *WeakBlock:
		b.WriteString(prefix)
		b.WriteString("WeakBlock(atom=")
		b.WriteString(n.Atom)
		b.WriteString(")")
	default:
		b.WriteString(prefix)
		fmt.Fprintf(b, "<unknown type %T>", n)
	}
	if indent > 0 {
		b.WriteByte('\n')
	}
}
