package tsparse

import (
	"strings"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/lexer"
	"github.com/greg2010/ic11c/internal/tsnode"
	ts "github.com/tree-sitter/go-tree-sitter"
)

// exprConverters dispatches on the kind of an expression node. It is assigned
// in init because every converter in it reaches back through [converter.expr],
// which reads the table.
var exprConverters map[tsnode.Kind]func(*converter, *ts.Node) ast.Expr

func init() {
	exprConverters = map[tsnode.Kind]func(*converter, *ts.Node) ast.Expr{
		tsnode.KindAssignmentExpression:    (*converter).assignExpr,
		tsnode.KindBinaryExpression:        (*converter).binaryExpr,
		tsnode.KindCallExpression:          (*converter).callExpr,
		tsnode.KindCastExpression:          (*converter).castExpr,
		tsnode.KindCharLiteral:             (*converter).charLit,
		tsnode.KindConditionalExpression:   (*converter).condExpr,
		tsnode.KindFalse:                   (*converter).falseLit,
		tsnode.KindIdentifier:              (*converter).identExpr,
		tsnode.KindNumberLiteral:           (*converter).numberLit,
		tsnode.KindParenthesizedExpression: (*converter).parenExpr,
		tsnode.KindPointerExpression:       (*converter).pointerExpr,
		tsnode.KindStringLiteral:           (*converter).stringLit,
		tsnode.KindSubscriptExpression:     (*converter).subscriptExpr,
		tsnode.KindTrue:                    (*converter).trueLit,
		tsnode.KindUnaryExpression:         (*converter).unaryExpr,
		tsnode.KindUpdateExpression:        (*converter).updateExpr,
	}
}

// binaryOps maps each infix operator the grammar admits to the operator the
// tree records. Its keys are held to the grammar's own list of alternatives, so
// an operator C gains is a build failure rather than a silently dropped one.
var binaryOps = map[tsnode.Kind]ast.BinaryOp{
	tsnode.KindAmp:      ast.BitAnd,
	tsnode.KindAmpAmp:   ast.LogicalAnd,
	tsnode.KindBangEq:   ast.Ne,
	tsnode.KindCaret:    ast.BitXor,
	tsnode.KindEqEq:     ast.Eq,
	tsnode.KindGt:       ast.Gt,
	tsnode.KindGtEq:     ast.Ge,
	tsnode.KindGtGt:     ast.Shr,
	tsnode.KindLt:       ast.Lt,
	tsnode.KindLtEq:     ast.Le,
	tsnode.KindLtLt:     ast.Shl,
	tsnode.KindMinus:    ast.Sub,
	tsnode.KindPercent:  ast.Mod,
	tsnode.KindPipe:     ast.BitOr,
	tsnode.KindPipePipe: ast.LogicalOr,
	tsnode.KindPlus:     ast.Add,
	tsnode.KindSlash:    ast.Div,
	tsnode.KindStar:     ast.Mul,
}

// assignOps maps each assignment operator the grammar admits to the operator
// the tree records.
var assignOps = map[tsnode.Kind]ast.AssignOp{
	tsnode.KindAmpEq:     ast.AndAssign,
	tsnode.KindCaretEq:   ast.XorAssign,
	tsnode.KindEq:        ast.Assign,
	tsnode.KindGtGtEq:    ast.ShrAssign,
	tsnode.KindLtLtEq:    ast.ShlAssign,
	tsnode.KindMinusEq:   ast.SubAssign,
	tsnode.KindPercentEq: ast.ModAssign,
	tsnode.KindPipeEq:    ast.OrAssign,
	tsnode.KindPlusEq:    ast.AddAssign,
	tsnode.KindSlashEq:   ast.DivAssign,
	tsnode.KindStarEq:    ast.MulAssign,
}

// unaryOps maps each prefix operator that is not an increment to the operator
// the tree records.
var unaryOps = map[tsnode.Kind]ast.UnaryOp{
	tsnode.KindBang:  ast.LogicalNot,
	tsnode.KindMinus: ast.Neg,
	tsnode.KindPlus:  ast.Plus,
	tsnode.KindTilde: ast.BitNot,
}

// pointerOps maps the two operators the grammar spells with a pointer
// expression rather than a unary one.
var pointerOps = map[tsnode.Kind]ast.UnaryOp{
	tsnode.KindAmp:  ast.AddrOf,
	tsnode.KindStar: ast.Deref,
}

// incDecOps maps each increment operator to the operator the tree records.
var incDecOps = map[tsnode.Kind]ast.IncDecOp{
	tsnode.KindPlusPlus:   ast.Inc,
	tsnode.KindMinusMinus: ast.Dec,
}

// expr converts one expression. A node the grammar could not read, one built
// out of part of a token, or one spelling a construct MicroC excludes, becomes
// a [ast.BadExpr] so the enclosing statement still has a shape.
func (c *converter) expr(n *ts.Node) ast.Expr {
	if bad(n) || c.partOfToken(n) {
		return &ast.BadExpr{From: c.start(n)}
	}
	convert, known := exprConverters[tsnode.Kind(n.Kind())]
	if !known {
		c.refuse(n)
		return &ast.BadExpr{From: c.start(n)}
	}
	return convert(c, n)
}

// operand converts the expression filling one slot of n. A slot the source
// left empty stands at the position of the construct that wanted it, which is
// the only place a reader can be sent when what is missing has no position of
// its own.
func (c *converter) operand(n *ts.Node, slot tsnode.Field) ast.Expr {
	if filled := field(n, slot); filled != nil {
		return c.expr(filled)
	}
	return &ast.BadExpr{From: c.start(n)}
}

// bound builds an expression out of what fills one slot of n, moving an
// assignment the grammar bound inside the operator back around it: C's left
// operand of '=' admits forms the grammar's narrower one does not, so
// "-a = 1" arrives as "-(a = 1)". A parenthesized operand is left alone.
func (c *converter) bound(n *ts.Node, slot tsnode.Field, build func(ast.Expr) ast.Expr) ast.Expr {
	inner := field(n, slot)
	if inner == nil {
		return build(&ast.BadExpr{From: c.start(n)})
	}
	x := c.expr(inner)
	assign, rotated := x.(*ast.AssignExpr)
	if !rotated || tsnode.Kind(inner.Kind()) == tsnode.KindParenthesizedExpression {
		return build(x)
	}
	assign.Target = build(assign.Target)
	return assign
}

func (c *converter) parenExpr(n *ts.Node) ast.Expr {
	for _, ch := range c.children(n) {
		// Default is the rule: tsnode.Kind is the grammar's whole alphabet, and
		// what a construct is written with is a handful of it.
		//exhaustive:ignore
		switch ch.kind {
		case tsnode.KindLparen, tsnode.KindRparen:
		case tsnode.KindCompoundStatement:
			c.errorf(c.start(ch.node), "%s", statementExprMsg)
			return &ast.BadExpr{From: c.start(n)}
		default:
			return c.expr(ch.node)
		}
	}
	c.errorf(c.start(n), "expected an expression between the parentheses")
	return &ast.BadExpr{From: c.start(n)}
}

// operatorOf gives the node spelling the operator an expression applies, and
// says whether the source wrote one. An operator covering part of a token is
// not one the source has: [converter.reportRelexedTokens] has already
// reported it, and this just keeps an expression from being made of the pieces.
func (c *converter) operatorOf(n *ts.Node) (*ts.Node, bool) {
	op := field(n, tsnode.FieldOperator)
	if op == nil || c.partOfToken(op) {
		return nil, false
	}
	return op, true
}

func (c *converter) binaryExpr(n *ts.Node) ast.Expr {
	op, written := c.operatorOf(n)
	if !written {
		return &ast.BadExpr{From: c.start(n)}
	}
	kind, known := binaryOps[tsnode.Kind(op.Kind())]
	if !known {
		c.refuse(op)
		return &ast.BadExpr{From: c.start(n)}
	}
	opPos, x := c.start(op), c.operand(n, tsnode.FieldLeft)
	return c.bound(n, tsnode.FieldRight, func(y ast.Expr) ast.Expr {
		return &ast.BinaryExpr{OpPos: opPos, Op: kind, X: x, Y: y}
	})
}

func (c *converter) assignExpr(n *ts.Node) ast.Expr {
	op, written := c.operatorOf(n)
	if !written {
		return &ast.BadExpr{From: c.start(n)}
	}
	kind, known := assignOps[tsnode.Kind(op.Kind())]
	if !known {
		c.refuse(op)
		return &ast.BadExpr{From: c.start(n)}
	}
	return &ast.AssignExpr{
		OpPos:  c.start(op),
		Op:     kind,
		Target: c.operand(n, tsnode.FieldLeft),
		Value:  c.operand(n, tsnode.FieldRight),
	}
}

func (c *converter) unaryExpr(n *ts.Node) ast.Expr {
	return c.prefixExpr(n, unaryOps)
}

func (c *converter) pointerExpr(n *ts.Node) ast.Expr {
	return c.prefixExpr(n, pointerOps)
}

func (c *converter) prefixExpr(n *ts.Node, ops map[tsnode.Kind]ast.UnaryOp) ast.Expr {
	op, kind, written := c.prefixOp(n, ops)
	if !written {
		return &ast.BadExpr{From: c.start(n)}
	}
	opPos := c.start(op)
	return c.bound(n, tsnode.FieldArgument, func(x ast.Expr) ast.Expr {
		return &ast.UnaryExpr{OpPos: opPos, Op: kind, X: x}
	})
}

// prefixOp gives the operator node one prefix expression applies and the
// operator it spells, and says whether the node writes one this converter
// reads. An operator C gains is refused rather than dropped.
func (c *converter) prefixOp(n *ts.Node, ops map[tsnode.Kind]ast.UnaryOp) (*ts.Node, ast.UnaryOp, bool) {
	op, written := c.operatorOf(n)
	if !written {
		return nil, 0, false
	}
	kind, known := ops[tsnode.Kind(op.Kind())]
	if !known {
		c.refuse(op)
		return nil, 0, false
	}
	return op, kind, true
}

// prefixForms names, for each expression the grammar writes a prefix operator
// with, the operators that form admits. It is what [converter.applied] descends
// through, and its keys are the two kinds whose operator table
// [TestOperatorTablesCoverTheGrammar] holds to the grammar.
var prefixForms = map[tsnode.Kind]map[tsnode.Kind]ast.UnaryOp{
	tsnode.KindPointerExpression: pointerOps,
	tsnode.KindUnaryExpression:   unaryOps,
}

// applied builds an expression out of n, with build applied to what a postfix
// operator written behind n really applies to. C binds postfix tighter than
// every prefix operator; the grammar gives them equal precedence, so "-a++"
// arrives as the increment of "-a" and is rebuilt around the increment here.
func (c *converter) applied(n *ts.Node, build func(ast.Expr) ast.Expr) ast.Expr {
	if sign, digits, signed := c.signedNumber(n); signed {
		return &ast.UnaryExpr{OpPos: c.start(n), Op: sign, X: build(c.numberAt(n, digits))}
	}
	if op, kind, arg, postfix := c.postfixUpdate(n); postfix {
		opPos := c.start(op)
		return c.applied(arg, func(x ast.Expr) ast.Expr {
			return build(&ast.IncDecExpr{OpPos: opPos, Op: kind, Postfix: true, X: x})
		})
	}
	ops, prefix := prefixForms[tsnode.Kind(n.Kind())]
	if !prefix {
		return build(c.expr(n))
	}
	op, kind, written := c.prefixOp(n, ops)
	arg := field(n, tsnode.FieldArgument)
	if !written || arg == nil {
		return &ast.BadExpr{From: c.start(n)}
	}
	return &ast.UnaryExpr{OpPos: c.start(op), Op: kind, X: c.applied(arg, build)}
}

// postfixUpdate gives the operator, its meaning and the operand of a postfix
// increment or decrement, and says whether n is one whose operator spells a
// form MicroC has. An operator it does not is left to [converter.updateExpr]
// to refuse.
func (c *converter) postfixUpdate(n *ts.Node) (op *ts.Node, kind ast.IncDecOp, arg *ts.Node, postfix bool) {
	if tsnode.Kind(n.Kind()) != tsnode.KindUpdateExpression {
		return nil, 0, nil, false
	}
	op, arg = field(n, tsnode.FieldOperator), field(n, tsnode.FieldArgument)
	if op == nil || arg == nil || op.StartByte() < arg.StartByte() {
		return nil, 0, nil, false
	}
	kind, known := incDecOps[tsnode.Kind(op.Kind())]
	if !known {
		return nil, 0, nil, false
	}
	return op, kind, arg, true
}

// updateExpr converts an increment or a decrement, which the grammar spells one
// way round and binds the other way round from C in each of its two forms. A
// prefix operator wants what [converter.bound] moves outwards and a postfix one
// what [converter.applied] moves inwards.
func (c *converter) updateExpr(n *ts.Node) ast.Expr {
	op, written := c.operatorOf(n)
	arg := field(n, tsnode.FieldArgument)
	if !written || arg == nil {
		return &ast.BadExpr{From: c.start(n)}
	}
	kind, known := incDecOps[tsnode.Kind(op.Kind())]
	if !known {
		c.refuse(op)
		return &ast.BadExpr{From: c.start(n)}
	}
	opPos := c.start(op)
	if op.StartByte() < arg.StartByte() {
		return c.bound(n, tsnode.FieldArgument, func(x ast.Expr) ast.Expr {
			return &ast.IncDecExpr{OpPos: opPos, Op: kind, X: x}
		})
	}
	return c.applied(arg, func(x ast.Expr) ast.Expr {
		return &ast.IncDecExpr{OpPos: opPos, Op: kind, Postfix: true, X: x}
	})
}

func (c *converter) condExpr(n *ts.Node) ast.Expr {
	question, marked := c.anonymous(n, tsnode.KindQuestion)
	then := field(n, tsnode.FieldConsequence)
	if !marked || then == nil {
		c.errorf(c.start(n), "a conditional expression needs a value for each branch")
		return &ast.BadExpr{From: c.start(n)}
	}
	cond, consequence := c.operand(n, tsnode.FieldCondition), c.expr(then)
	return c.bound(n, tsnode.FieldAlternative, func(alt ast.Expr) ast.Expr {
		return &ast.CondExpr{Question: question, Cond: cond, Then: consequence, Else: alt}
	})
}

func (c *converter) subscriptExpr(n *ts.Node) ast.Expr {
	lbrack, marked := c.anonymous(n, tsnode.KindLbrack)
	if !marked {
		return &ast.BadExpr{From: c.start(n)}
	}
	return &ast.IndexExpr{
		Lbrack: lbrack,
		X:      c.operand(n, tsnode.FieldArgument),
		Index:  c.operand(n, tsnode.FieldIndex),
	}
}

func (c *converter) callExpr(n *ts.Node) ast.Expr {
	args := field(n, tsnode.FieldArguments)
	if args == nil {
		return &ast.BadExpr{From: c.start(n)}
	}
	call := &ast.CallExpr{Lparen: c.start(args), Fun: c.operand(n, tsnode.FieldFunction)}
	for _, ch := range c.children(args) {
		// Default is the rule: tsnode.Kind is the grammar's whole alphabet, and
		// what a construct is written with is a handful of it.
		//exhaustive:ignore
		switch ch.kind {
		case tsnode.KindLparen, tsnode.KindRparen, tsnode.KindComma:
		case tsnode.KindCompoundStatement:
			c.errorf(c.start(ch.node), "%s", statementExprMsg)
			call.Args = append(call.Args, &ast.BadExpr{From: c.start(ch.node)})
		default:
			call.Args = append(call.Args, c.expr(ch.node))
		}
	}
	return call
}

func (c *converter) castExpr(n *ts.Node) ast.Expr {
	lparen, marked := c.anonymous(n, tsnode.KindLparen)
	descriptor := field(n, tsnode.FieldType)
	if !marked || descriptor == nil {
		return &ast.BadExpr{From: c.start(n)}
	}
	typ, ok := c.castType(descriptor)
	if !ok {
		return &ast.BadExpr{From: lparen}
	}
	return c.bound(n, tsnode.FieldValue, func(x ast.Expr) ast.Expr {
		return &ast.CastExpr{Lparen: lparen, Type: typ, X: x}
	})
}

func (c *converter) identExpr(n *ts.Node) ast.Expr {
	name, named := c.nameExpr(n)
	if !named {
		return &ast.BadExpr{From: c.start(n)}
	}
	return name
}

// nameExpr builds the name an identifier node denotes, and says whether the
// node spells one. It answers with the expression rather than a [ast.BadExpr]
// because the declarator readback in misread.go needs to know that the source
// was not read at all, which a Bad node standing in for it would hide.
func (c *converter) nameExpr(n *ts.Node) (ast.Expr, bool) {
	name, named := c.identifier(n)
	if !named {
		return nil, false
	}
	return &ast.Ident{NamePos: c.start(n), Name: name}, true
}

func (c *converter) trueLit(n *ts.Node) ast.Expr {
	return &ast.BoolLit{ValuePos: c.start(n), Value: true}
}

func (c *converter) falseLit(n *ts.Node) ast.Expr {
	return &ast.BoolLit{ValuePos: c.start(n), Value: false}
}

// numberLit converts a numeric literal, which the grammar spells with its sign
// attached where one is written without a space. The sign is split back off
// since it is an operator applied to the literal rather than part of it:
// 9223372036854775808 has no negation of its own.
func (c *converter) numberLit(n *ts.Node) ast.Expr {
	op, digits, signed := c.signedNumber(n)
	if !signed {
		return c.numberAt(n, n.StartByte())
	}
	return &ast.UnaryExpr{OpPos: c.start(n), Op: op, X: c.numberAt(n, digits)}
}

// signedNumber gives the operator a numeric literal is written with, the offset
// of the digits behind it, and whether a sign is written at all. A node that is
// no numeric literal carries none.
func (c *converter) signedNumber(n *ts.Node) (op ast.UnaryOp, digits uint, signed bool) {
	if tsnode.Kind(n.Kind()) != tsnode.KindNumberLiteral {
		return 0, 0, false
	}
	text := c.text(n)
	switch {
	case strings.HasPrefix(text, "-"):
		return ast.Neg, n.StartByte() + 1, true
	case strings.HasPrefix(text, "+"):
		return ast.Plus, n.StartByte() + 1, true
	}
	return 0, 0, false
}

// numberAt builds the literal the lexer scanned at an offset. The value is the
// lexer's rather than one this package decodes, so a spelling the two front
// ends read differently is impossible by construction.
func (c *converter) numberAt(n *ts.Node, offset uint) ast.Expr {
	pos := c.pos(offset)
	switch tok, scanned := c.token(offset); {
	case scanned && tok.Kind == lexer.IntLit:
		return &ast.IntLit{ValuePos: pos, Value: tok.Int, Hex: isHexLiteral(tok.Text)}
	case scanned && tok.Kind == lexer.FloatLit:
		return &ast.FloatLit{ValuePos: pos, Value: tok.Float}
	default:
		c.errorf(pos, "%q is not a number MicroC accepts", c.text(n))
		return &ast.BadExpr{From: pos}
	}
}

func (c *converter) charLit(n *ts.Node) ast.Expr {
	tok, scanned := c.token(n.StartByte())
	if !scanned || tok.Kind != lexer.CharLit {
		c.errorf(c.start(n), "%s is not a character literal MicroC accepts", c.text(n))
		return &ast.BadExpr{From: c.start(n)}
	}
	return &ast.CharLit{ValuePos: c.start(n), Value: tok.Int}
}

func (c *converter) stringLit(n *ts.Node) ast.Expr {
	tok, scanned := c.token(n.StartByte())
	if !scanned || tok.Kind != lexer.StringLit {
		c.errorf(c.start(n), "%s is not a string literal MicroC accepts", c.text(n))
		return &ast.BadExpr{From: c.start(n)}
	}
	return &ast.StringLit{ValuePos: c.start(n), Value: tok.Str}
}

// identifier answers the spelling of an identifier node, refusing one the
// language reserves. The grammar has no MicroC keywords, so "long long dev;"
// reaches here as a declaration of something named dev.
func (c *converter) identifier(n *ts.Node) (string, bool) {
	text := c.text(n)
	tok, scanned := c.token(n.StartByte())
	if !scanned || tok.Kind != lexer.Ident || tok.Text != text {
		c.errorf(c.start(n), "expected an identifier, found '%s'", text)
		return "", false
	}
	return text, true
}

// isHexLiteral reports whether an integer literal token was written in
// hexadecimal, which decides the type C gives the constant.
func isHexLiteral(text string) bool {
	return len(text) > 1 && text[0] == '0' && text[1]|0x20 == 'x'
}

// initializer converts what a declaration assigns, which is either an
// expression or a brace initializer.
func (c *converter) initializer(n *ts.Node) ast.Expr {
	if tsnode.Kind(n.Kind()) != tsnode.KindInitializerList {
		return c.expr(n)
	}
	lit := &ast.InitListExpr{Lbrace: c.start(n)}
	for _, ch := range c.children(n) {
		// Default is the rule: tsnode.Kind is the grammar's whole alphabet, and
		// what a construct is written with is a handful of it.
		//exhaustive:ignore
		switch ch.kind {
		case tsnode.KindLbrace, tsnode.KindRbrace, tsnode.KindComma:
		case tsnode.KindInitializerList:
			c.errorf(c.start(ch.node), "nested brace initializers are not supported in MicroC; arrays are one-dimensional")
			lit.Elems = append(lit.Elems, &ast.BadExpr{From: c.start(ch.node)})
		default:
			lit.Elems = append(lit.Elems, c.expr(ch.node))
		}
	}
	return lit
}
