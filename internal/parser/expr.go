package parser

import (
	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/lexer"
)

// binaryPrec gives C's binding strength for each infix operator; a higher
// number binds tighter and zero means the token is not an infix operator. Every
// level is left associative, so precedence climbing recurses at prec+1.
var binaryPrec = map[lexer.Kind]int{
	lexer.Lor:  1,
	lexer.Land: 2,
	lexer.Or:   3,
	lexer.Xor:  4,
	lexer.And:  5,
	lexer.Eq:   6,
	lexer.Neq:  6,
	lexer.Lt:   7,
	lexer.Leq:  7,
	lexer.Gt:   7,
	lexer.Geq:  7,
	lexer.Shl:  8,
	lexer.Shr:  8,
	lexer.Add:  9,
	lexer.Sub:  9,
	lexer.Mul:  10,
	lexer.Quo:  10,
	lexer.Rem:  10,
}

var binaryOps = map[lexer.Kind]ast.BinaryOp{
	lexer.Lor:  ast.LogicalOr,
	lexer.Land: ast.LogicalAnd,
	lexer.Or:   ast.BitOr,
	lexer.Xor:  ast.BitXor,
	lexer.And:  ast.BitAnd,
	lexer.Eq:   ast.Eq,
	lexer.Neq:  ast.Ne,
	lexer.Lt:   ast.Lt,
	lexer.Leq:  ast.Le,
	lexer.Gt:   ast.Gt,
	lexer.Geq:  ast.Ge,
	lexer.Shl:  ast.Shl,
	lexer.Shr:  ast.Shr,
	lexer.Add:  ast.Add,
	lexer.Sub:  ast.Sub,
	lexer.Mul:  ast.Mul,
	lexer.Quo:  ast.Div,
	lexer.Rem:  ast.Mod,
}

var assignOps = map[lexer.Kind]ast.AssignOp{
	lexer.Assign:    ast.Assign,
	lexer.AddAssign: ast.AddAssign,
	lexer.SubAssign: ast.SubAssign,
	lexer.MulAssign: ast.MulAssign,
	lexer.QuoAssign: ast.DivAssign,
	lexer.RemAssign: ast.ModAssign,
	lexer.ShlAssign: ast.ShlAssign,
	lexer.ShrAssign: ast.ShrAssign,
	lexer.AndAssign: ast.AndAssign,
	lexer.OrAssign:  ast.OrAssign,
	lexer.XorAssign: ast.XorAssign,
}

var unaryOps = map[lexer.Kind]ast.UnaryOp{
	lexer.Add:   ast.Plus,
	lexer.Sub:   ast.Neg,
	lexer.Not:   ast.LogicalNot,
	lexer.Tilde: ast.BitNot,
	lexer.And:   ast.AddrOf,
	lexer.Mul:   ast.Deref,
}

// parseExpr parses a full expression. MicroC has no comma operator, so this is
// exactly an assignment expression.
func (p *parser) parseExpr() ast.Expr {
	target := p.parseConditionalExpr()
	op, ok := assignOps[p.kind()]
	if !ok {
		return target
	}
	opPos := p.tok().Pos
	p.next()
	return &ast.AssignExpr{OpPos: opPos, Op: op, Target: target, Value: p.parseExpr()}
}

// parseConditionalExpr parses a ternary conditional, which is right
// associative: its else branch is itself a conditional expression.
func (p *parser) parseConditionalExpr() ast.Expr {
	cond := p.parseBinaryExpr(1)
	if !p.at(lexer.Question) {
		return cond
	}
	e := &ast.CondExpr{Question: p.tok().Pos, Cond: cond}
	p.next()
	e.Then = p.parseExpr()
	p.expect(lexer.Colon)
	e.Else = p.parseConditionalExpr()
	return e
}

func (p *parser) parseBinaryExpr(minPrec int) ast.Expr {
	x := p.parseUnaryExpr()
	for {
		prec, ok := binaryPrec[p.kind()]
		if !ok || prec < minPrec {
			return x
		}
		op := binaryOps[p.kind()]
		opPos := p.tok().Pos
		p.next()
		x = &ast.BinaryExpr{OpPos: opPos, Op: op, X: x, Y: p.parseBinaryExpr(prec + 1)}
	}
}

func (p *parser) parseUnaryExpr() ast.Expr {
	from := p.tok().Pos
	if p.reportExcluded() {
		p.next()
		p.skipBracketGroup(lexer.Lparen)
		return &ast.BadExpr{From: from}
	}

	if p.at(lexer.Inc) || p.at(lexer.Dec) {
		op := ast.Inc
		if p.at(lexer.Dec) {
			op = ast.Dec
		}
		p.next()
		return &ast.IncDecExpr{OpPos: from, Op: op, X: p.parseUnaryExpr()}
	}

	// MicroC has no typedefs, so a type keyword after '(' is unambiguously a
	// cast rather than a parenthesized expression.
	if p.at(lexer.Lparen) && startsTypeName(p.peek(1).Kind) {
		return p.parseCast()
	}

	if op, ok := unaryOps[p.kind()]; ok {
		p.next()
		return &ast.UnaryExpr{OpPos: from, Op: op, X: p.parseUnaryExpr()}
	}

	return p.parsePostfixExpr()
}

// castTargetMsg explains each type keyword a cast may not name. A cast to void
// discards a value an expression statement already discards, and a device pin is
// a compile-time name rather than a value a number could become.
var castTargetMsg = map[ast.ScalarKind]string{
	ast.Void: "a cast to void is not supported in MicroC",
	ast.Dev:  "a cast to dev is not supported in MicroC; a device is named, not computed",
}

// parseCast parses a cast, whose target must be int, bool, or double. Each does
// real work on this machine: a cast to int truncates toward zero, a cast to bool
// normalizes to 0 or 1, and a cast to double is the widening an assignment would
// have done implicitly. A cast to a pointer would be an identity on the slot
// index while hiding the object a pointer traces to.
func (p *parser) parseCast() ast.Expr {
	e := &ast.CastExpr{Lparen: p.tok().Pos}
	p.next()
	typePos := p.tok().Pos
	base, ok := p.parseTypeSpec()
	if !ok {
		p.errorf(p.tok().Pos, "expected a type, found %s", p.tok().Describe())
		return &ast.BadExpr{From: e.Lparen}
	}
	if scalar, isScalar := base.(*ast.ScalarType); isScalar {
		if msg, refused := castTargetMsg[scalar.Kind]; refused {
			p.errorf(typePos, "%s", msg)
		}
	}
	if p.at(lexer.Mul) {
		p.errorf(p.tok().Pos, "a cast to a pointer type is not supported in MicroC")
		for p.at(lexer.Mul) {
			p.next()
		}
	}
	e.Type = base
	p.expect(lexer.Rparen)
	e.X = p.parseUnaryExpr()
	return e
}

func (p *parser) parsePostfixExpr() ast.Expr {
	x := p.parsePrimaryExpr()
	for {
		// Default is the rule: a token that continues no postfix form ends the
		// expression.
		//exhaustive:ignore
		switch k := p.kind(); k {
		case lexer.Lbrack:
			e := &ast.IndexExpr{Lbrack: p.tok().Pos, X: x}
			p.next()
			e.Index = p.parseExpr()
			p.expect(lexer.Rbrack)
			x = e
		case lexer.Lparen:
			x = p.parseCallExpr(x)
		case lexer.Inc, lexer.Dec:
			op := ast.Inc
			if k == lexer.Dec {
				op = ast.Dec
			}
			x = &ast.IncDecExpr{OpPos: p.tok().Pos, Op: op, Postfix: true, X: x}
			p.next()
		case lexer.Period, lexer.Arrow:
			p.errorf(p.tok().Pos, "member access is not supported in MicroC; structs and unions are excluded")
			p.next()
			p.accept(lexer.Ident)
			x = &ast.BadExpr{From: x.Pos()}
		default:
			return x
		}
	}
}

// parseCallExpr parses an argument list. Unlike a brace initializer, it admits
// no trailing comma: the grammar gives the list no optional one, and silently
// accepting it would let a deleted last argument compile.
func (p *parser) parseCallExpr(fun ast.Expr) ast.Expr {
	e := &ast.CallExpr{Lparen: p.tok().Pos, Fun: fun}
	p.next()
	for !p.at(lexer.Rparen) && !p.at(lexer.EOF) {
		before := p.pos
		e.Args = append(e.Args, p.parseExpr())
		if p.pos == before {
			p.next()
		}
		comma := p.tok().Pos
		if !p.accept(lexer.Comma) {
			break
		}
		if p.at(lexer.Rparen) {
			p.errorf(comma, "a trailing comma is not valid in an argument list")
			break
		}
	}
	p.expect(lexer.Rparen)
	return e
}

// parsePrimaryExpr parses an identifier, a literal, or a parenthesized
// expression. Parentheses leave no node behind: grouping is already encoded by
// the shape of the tree.
func (p *parser) parsePrimaryExpr() ast.Expr {
	t := p.tok()
	if t.Kind == lexer.Lparen {
		p.next()
		x := p.parseExpr()
		p.expect(lexer.Rparen)
		return x
	}
	if e, ok := literalExpr(t); ok {
		p.next()
		return e
	}
	p.errorf(t.Pos, "expected an expression, found %s", t.Describe())
	return &ast.BadExpr{From: t.Pos}
}

// literalExpr builds the leaf node for an identifier or literal token.
func literalExpr(t lexer.Token) (ast.Expr, bool) {
	// Default is the rule: a token that is no leaf reports false, and the
	// caller names what it expected instead.
	//exhaustive:ignore
	switch t.Kind {
	case lexer.Ident:
		return &ast.Ident{NamePos: t.Pos, Name: t.Text}, true
	case lexer.IntLit:
		return &ast.IntLit{ValuePos: t.Pos, Value: t.Int, Hex: isHexLiteral(t.Text)}, true
	case lexer.FloatLit:
		return &ast.FloatLit{ValuePos: t.Pos, Value: t.Float}, true
	case lexer.CharLit:
		return &ast.CharLit{ValuePos: t.Pos, Value: t.Int}, true
	case lexer.True:
		return &ast.BoolLit{ValuePos: t.Pos, Value: true}, true
	case lexer.False:
		return &ast.BoolLit{ValuePos: t.Pos, Value: false}, true
	case lexer.StringLit:
		return &ast.StringLit{ValuePos: t.Pos, Value: t.Str}, true
	default:
		return nil, false
	}
}

// isHexLiteral reports whether an integer literal token was written in
// hexadecimal. The lexer rejects octal, so the two spellings the language has
// are told apart by the prefix alone.
func isHexLiteral(text string) bool {
	return len(text) > 1 && text[0] == '0' && text[1]|0x20 == 'x'
}
