package tsparse

import (
	"cmp"
	"slices"

	"github.com/greg2010/ic11c/internal/lexer"
	"github.com/greg2010/ic11c/internal/tsnode"
	ts "github.com/tree-sitter/go-tree-sitter"
)

// Some expressions MicroC has are ones the C grammar will not build: a cast,
// since MicroC's closed type-name set decides what C decides with a symbol
// table, and an assignment to a target outside the grammar's left operand
// list. Both are recovered by wrapping the source in parentheses and reparsing.

// span is what one rewrite covers: the two bytes its parentheses go between, in
// the source as written.
type span struct{ open, closed int }

// rewrittenExprs gives the parentheses to write around every expression the
// grammar could not build, and records the ones it answered with in wrapped
// so a span already covered is skipped, which is what ends the rounds. The
// walk is a cursor rather than a recursion, ahead of the depth check.
func (c *converter) rewrittenExprs(root *ts.Node, wrapped map[span]bool) []inserted {
	var marks []inserted
	cursor := root.Walk()
	defer cursor.Close()
	for {
		if open, closed, misread := c.misbuilt(cursor.Node()); misread && !wrapped[span{open, closed}] {
			wrapped[span{open, closed}] = true
			marks = append(marks, inserted{at: open, ch: '('}, inserted{at: closed, ch: ')'})
		}
		if cursor.GotoFirstChild() {
			continue
		}
		for !cursor.GotoNextSibling() {
			if !cursor.GotoParent() {
				return marks
			}
		}
	}
}

// misbuilt gives the bytes a pair of parentheses goes between, in the source as
// written, and says whether the node stands where the grammar could not build
// the expression MicroC has.
func (c *converter) misbuilt(n *ts.Node) (open, closed int, misread bool) {
	// Default is the rule: a handful of kinds out of the grammar's whole
	// alphabet.
	//exhaustive:ignore
	switch tsnode.Kind(n.Kind()) {
	case tsnode.KindCastExpression:
		return c.misreadCast(n)
	case tsnode.KindUpdateExpression:
		return c.misboundUpdate(n)
	case tsnode.KindNumberLiteral, tsnode.KindCharLiteral, tsnode.KindStringLiteral, tsnode.KindTrue, tsnode.KindFalse:
		return c.misboundLiteral(n)
	}
	return 0, 0, false
}

// misreadCast gives the byte a cast's opening parenthesis begins at and the byte
// just past its closing one, and says whether the node is a cast the grammar
// built out of an expression MicroC has.
func (c *converter) misreadCast(n *ts.Node) (open, closed int, misread bool) {
	descriptor := field(n, tsnode.FieldType)
	if descriptor == nil || !c.misreadDescriptor(descriptor) {
		return 0, 0, false
	}
	var lparen, rparen *ts.Node
	for _, ch := range c.children(n) {
		if ch.kind == tsnode.KindLparen && lparen == nil {
			lparen = ch.node
		}
		if ch.kind == tsnode.KindRparen && rparen == nil {
			rparen = ch.node
		}
	}
	if lparen == nil || rparen == nil {
		return 0, 0, false
	}
	return c.sourceOffset(lparen.StartByte()), c.sourceOffset(rparen.EndByte()), true
}

// misreadDescriptor reports whether a cast's type descriptor is source MicroC
// reads as an expression instead: a name outside MicroC's closed type set,
// applied to nothing or to something spelled the way a postfix expression is
// (a subscript or a call).
func (c *converter) misreadDescriptor(descriptor *ts.Node) bool {
	children := c.children(descriptor)
	if len(children) == 0 || len(children) > 2 {
		return false
	}
	name := children[0]
	if name.field != tsnode.FieldType || name.kind != tsnode.KindTypeIdentifier {
		return false
	}
	if _, isType := scalarTypes[c.text(name.node)]; isType {
		return false
	}
	if len(children) == 1 {
		return true
	}
	return children[1].field == tsnode.FieldDeclarator && c.postfixDeclarator(children[1].node)
}

// postfixDeclarator reports whether an abstract declarator is written the way
// a postfix expression is: a subscript or an argument list applied to another
// such, with parentheses admitted anywhere around them. The descent is a loop
// rather than a recursion because it runs in front of the depth check.
func (c *converter) postfixDeclarator(n *ts.Node) bool {
	for n != nil {
		// Default is the rule: three declarator forms out of the grammar's
		// whole alphabet read as an expression.
		//exhaustive:ignore
		switch tsnode.Kind(n.Kind()) {
		case tsnode.KindAbstractArrayDeclarator, tsnode.KindAbstractFunctionDeclarator:
			n = field(n, tsnode.FieldDeclarator)
		case tsnode.KindAbstractParenthesizedDeclarator:
			inner := c.parenthesized(n)
			if inner == nil {
				return false
			}
			n = inner
		default:
			return false
		}
	}
	return true
}

// parenthesized gives the declarator a parenthesized abstract declarator holds,
// or nil where the grammar left it holding none.
func (c *converter) parenthesized(n *ts.Node) *ts.Node {
	for _, ch := range c.children(n) {
		if ch.kind != tsnode.KindLparen && ch.kind != tsnode.KindRparen {
			return ch.node
		}
	}
	return nil
}

// misboundUpdate gives the bytes a postfix increment spans, and says whether
// an assignment is written to it. The node has to be one the grammar read
// whole: recovery can build an increment out of source that is not one, and
// the parentheses that program needs go elsewhere.
func (c *converter) misboundUpdate(n *ts.Node) (open, closed int, misread bool) {
	op, arg := field(n, tsnode.FieldOperator), field(n, tsnode.FieldArgument)
	if op == nil || arg == nil || op.StartByte() < arg.StartByte() {
		return 0, 0, false
	}
	if n.HasError() {
		return 0, 0, false
	}
	return c.assignedTo(n)
}

// misboundLiteral gives the bytes a literal spans, and says whether an
// assignment is written to it. A literal is a postfix-expression and so a
// unary-expression, which is what C assigns to; the grammar's left operand
// list leaves literals out with the increments.
func (c *converter) misboundLiteral(n *ts.Node) (open, closed int, misread bool) {
	return c.assignedTo(n)
}

// assignedTo gives the bytes an operand spans, and says whether the source
// writes an assignment to that operand. The token behind it has to be an
// assignment operator, and the source in front has to leave an operand
// position open — otherwise the source juxtaposed two expressions instead.
func (c *converter) assignedTo(n *ts.Node) (open, closed int, misread bool) {
	if !assigns(c.nextToken(n.EndByte()).Kind) {
		return 0, 0, false
	}
	start := c.sourceOffset(n.StartByte())
	if !c.opensAnOperand(start) {
		return 0, 0, false
	}
	return start, c.sourceOffset(n.EndByte()), true
}

// opensAnOperand reports whether the source in front of a byte offset leaves
// an operand position open, which is what makes parentheses written there
// group rather than apply. It reads the lexer's tokens backwards from the
// offset rather than the tree, since the tree is what the question changes.
func (c *converter) opensAnOperand(off int) bool {
	return !c.endsAValueBefore(c.tokenIndex(off))
}

// endsAValueBefore reports whether the source in front of a token ends
// something that stands for a value. An increment and a ')' do not say on
// their own which they are — a ')' closes a call, a group, a control head, or
// a cast — so the walk climbs leftwards through openers, deciding outside in.
func (c *converter) endsAValueBefore(i int) bool {
	var openers []int
	value := false
	for i > 0 {
		prev := c.ordered[i-1]
		if prev.Kind == lexer.Inc || prev.Kind == lexer.Dec {
			i--
			continue
		}
		if prev.Kind == lexer.Rparen {
			opener, opened := c.matchingLparen(prev.Pos.Offset)
			if !opened {
				value = true
				break
			}
			openers = append(openers, opener)
			i = opener
			continue
		}
		value = endsAnExpression(prev.Kind)
		break
	}
	for k := len(openers) - 1; k >= 0 && !value; k-- {
		opener := openers[k]
		if opener > 0 && controlHeads[c.ordered[opener-1].Kind] {
			continue
		}
		value = opener+1 >= len(c.ordered) || !typeWords[c.ordered[opener+1].Kind]
	}
	return value
}

// matchingLparen gives the index in [converter.ordered] of the '(' that the ')'
// written at a byte offset closes, and says whether the source writes one.
func (c *converter) matchingLparen(off int) (int, bool) {
	depth := 0
	for i := c.tokenIndex(off); i >= 0; i-- {
		// Default is the rule: a source is written with far more than the
		// parentheses that bound its groups.
		//exhaustive:ignore
		switch c.ordered[i].Kind {
		case lexer.Rparen:
			depth++
		case lexer.Lparen:
			if depth--; depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

// tokenIndex gives the index in [converter.ordered] of the token beginning at a
// byte offset into the source as written, or of the first token past it where
// the offset is inside one. The token in front is therefore always the one at
// the index behind.
func (c *converter) tokenIndex(off int) int {
	i, _ := slices.BinarySearchFunc(c.ordered, off, func(t lexer.Token, at int) int {
		return cmp.Compare(t.Pos.Offset, at)
	})
	return i
}

// controlHeads are the words whose parenthesized head governs the statement
// behind it rather than standing for a value of its own.
var controlHeads = map[lexer.Kind]bool{
	lexer.For:    true,
	lexer.If:     true,
	lexer.Switch: true,
	lexer.While:  true,
}

// endsAnExpression reports whether a token is one an expression can end with,
// which is what makes an expression written behind it a juxtaposition. A ')'
// and an increment also qualify but are answered elsewhere, since neither says
// on its own what it is. See [converter.opensAnOperand].
func endsAnExpression(kind lexer.Kind) bool {
	// Default is the rule: an expression ends with a handful of the lexer's
	// tokens and everything else opens or joins one.
	//exhaustive:ignore
	switch kind {
	case lexer.Ident, lexer.IntLit, lexer.FloatLit, lexer.CharLit, lexer.StringLit,
		lexer.True, lexer.False, lexer.Rbrack:
		return true
	}
	return false
}

// assigns reports whether a token is one of the operators an assignment is
// written with.
func assigns(kind lexer.Kind) bool {
	// Default is the rule: an assignment is written with eleven of the lexer's
	// tokens and everything else is not one.
	//exhaustive:ignore
	switch kind {
	case lexer.Assign, lexer.AddAssign, lexer.SubAssign, lexer.MulAssign,
		lexer.QuoAssign, lexer.RemAssign, lexer.AndAssign, lexer.OrAssign,
		lexer.XorAssign, lexer.ShlAssign, lexer.ShrAssign:
		return true
	}
	return false
}
