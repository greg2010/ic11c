// Package parser builds a MicroC syntax tree from source text.
//
// Parsing is recursive descent with precedence climbing for binary operators,
// following C's precedence and associativity. It collects diagnostics rather
// than stopping at the first error and resynchronizes at statement and
// declaration boundaries, so one mistake does not cascade into a page of noise.
//
// The parser accepts what is syntactically MicroC and records it. It performs
// no type checking, no name resolution, and no constant evaluation. It does
// reject the C constructs MicroC excludes wherever they are syntactically
// recognizable, because "unexpected token" is a poor thing to say about
// "struct".
package parser

import (
	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/lexer"
	"github.com/greg2010/ic11c/internal/source"
)

// maxDiagnostics caps how many problems the parser itself reports. Past this
// point the output is noise rather than information. Lexical diagnostics are
// not counted against it: they are already in the list when parsing begins, and
// letting them spend the budget would silence the parser on exactly the files
// that need it most.
const maxDiagnostics = 64

// excludedMsg explains each C keyword MicroC does not accept. Naming the
// construct is the whole point: a parser that answers "struct" with "unexpected
// token" has told the programmer nothing.
var excludedMsg = map[lexer.Kind]string{
	lexer.Auto:     "the 'auto' storage class is not supported in MicroC",
	lexer.Char:     "the 'char' type specifier is not supported in MicroC; a character literal is a long long",
	lexer.Enum:     "enums are not supported in MicroC",
	lexer.Extern:   "the 'extern' storage class is not supported in MicroC",
	lexer.Float:    "the 'float' type specifier is not supported in MicroC; every register and memory slot holds one whole double, so there is no 32-bit type for it to name — write 'double'",
	lexer.Goto:     "goto is not supported in MicroC",
	lexer.Inline:   "the 'inline' specifier is not supported in MicroC; functions are inlined by the compiler",
	lexer.Int:      "the 'int' type specifier is not supported in MicroC; C's int is 32 bits and every value here is exact to 53 — write 'long long', which C guarantees at least 64 bits everywhere",
	lexer.Register: "the 'register' storage class is not supported in MicroC",
	lexer.Restrict: "the 'restrict' qualifier is not supported in MicroC",
	lexer.Short:    "the 'short' type specifier is not supported in MicroC",
	lexer.Signed:   "the 'signed' type specifier is not supported in MicroC",
	lexer.Sizeof:   "sizeof is not supported in MicroC",
	lexer.Static:   "the 'static' storage class is not supported in MicroC",
	lexer.Struct:   "structs are not supported in MicroC",
	lexer.Typedef:  "typedef is not supported in MicroC",
	lexer.Union:    "unions are not supported in MicroC",
	lexer.Unsigned: "the 'unsigned' type specifier is not supported in MicroC",
	lexer.Volatile: "the 'volatile' qualifier is not supported in MicroC",
}

// Parse parses src as a MicroC translation unit. file names the source in
// positions and diagnostics and is never opened.
//
// The returned file is never nil and holds every declaration that parsed;
// source the parser could not read appears as a Bad node so later positions
// stay meaningful. Diagnostics combine lexical and syntactic errors ordered by
// source position, and an empty list means the file parsed cleanly. A caller
// wanting a single error can use [source.DiagnosticList.Err].
//
// A parse that ran past [maxDiagnostics] closes the list with a note saying so,
// carrying the position of the last diagnostic shown, since that is the point
// beyond which the report says nothing.
func Parse(file, src string) (*ast.File, source.DiagnosticList) {
	p := newParser(file, src)
	f := p.parseFile()
	p.diags.Sort()
	if p.truncated {
		p.diags.Addf(p.diags[len(p.diags)-1].Pos, "too many errors")
	}
	return f, p.diags
}

type parser struct {
	file  string
	toks  []lexer.Token
	pos   int
	diags source.DiagnosticList

	// lastErrOffset suppresses a second complaint about the same token, which
	// is how a failed sub-parse would otherwise be reported twice on the way
	// back up.
	lastErrOffset int
	// reported counts the diagnostics this parser added, which is what
	// maxDiagnostics bounds. truncated says the cap suppressed at least one,
	// and so implies the list already holds maxDiagnostics of them.
	reported  int
	truncated bool
}

func newParser(file, src string) *parser {
	l := lexer.New(file, src)
	var toks []lexer.Token
	for {
		t := l.Next()
		toks = append(toks, t)
		if t.Kind == lexer.EOF {
			break
		}
	}
	return &parser{
		file:          file,
		toks:          toks,
		diags:         l.Diagnostics(),
		lastErrOffset: -1,
	}
}

func (p *parser) tok() lexer.Token { return p.toks[p.pos] }

func (p *parser) kind() lexer.Kind { return p.toks[p.pos].Kind }

func (p *parser) at(k lexer.Kind) bool { return p.toks[p.pos].Kind == k }

// peek returns the token n positions ahead, clamped to the trailing EOF.
func (p *parser) peek(n int) lexer.Token {
	if p.pos+n >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[p.pos+n]
}

func (p *parser) next() {
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
}

// accept consumes the current token when it has kind k.
func (p *parser) accept(k lexer.Kind) bool {
	if !p.at(k) {
		return false
	}
	p.next()
	return true
}

// expect consumes the current token when it has kind k. On a mismatch it
// reports and leaves the token in place, so the caller can keep parsing against
// whatever is actually there rather than losing it.
func (p *parser) expect(k lexer.Kind) (lexer.Token, bool) {
	t := p.tok()
	if t.Kind != k {
		p.errorf(t.Pos, "expected %s, found %s", k, t.Describe())
		return t, false
	}
	p.next()
	return t, true
}

// expectSemi consumes a statement or declaration terminator, naming the comma
// operator when that is what the programmer reached for.
//
// A missing terminator is reported where it belonged rather than at the token
// that follows, which may be blank lines below and reads as a mistake on a line
// that is written correctly.
func (p *parser) expectSemi() {
	if p.accept(lexer.Semicolon) {
		return
	}
	if p.at(lexer.Comma) {
		p.errorf(p.tok().Pos, "the comma operator is not supported in MicroC")
		return
	}
	p.errorf(p.endOfPrev(), "expected %s, found %s", lexer.Semicolon, p.tok().Describe())
}

// endOfPrev gives the position just past the last consumed token. No token
// spans a line break, so advancing the column by the spelling's length stays on
// the line the token was written on.
func (p *parser) endOfPrev() source.Position {
	if p.pos == 0 {
		return p.tok().Pos
	}
	prev := p.toks[p.pos-1]
	end := prev.Pos
	end.Offset += len(prev.Text)
	end.Column += len(prev.Text)
	return end
}

func (p *parser) errorf(pos source.Position, format string, args ...any) {
	if pos.Offset == p.lastErrOffset {
		return
	}
	p.lastErrOffset = pos.Offset
	if p.reported >= maxDiagnostics {
		p.truncated = true
		return
	}
	p.reported++
	p.diags.Addf(pos, format, args...)
}

// reportExcluded reports the current token when it is a C keyword MicroC
// excludes. It does not consume: the caller resynchronizes, which lets
// "unsigned int x;" recover onto the 'int' and parse the rest.
//
// It must run against the token that would begin the type, not the one that
// begins the declaration, or a leading 'const' hides the tailored message
// behind "expected a type".
func (p *parser) reportExcluded() bool {
	msg, ok := excludedMsg[p.kind()]
	if !ok {
		return false
	}
	p.errorf(p.tok().Pos, "%s", msg)
	return true
}

// parseSpecifiers consumes the 'const' and 'constexpr' specifiers a declaration
// may lead with. C23 admits them in either order and admits both together, so
// this reads a sequence rather than a fixed pair.
//
// A repeat is folded into the flag rather than reported: C says a qualifier
// written twice means what it means once.
func (p *parser) parseSpecifiers() (isConst, isConstexpr bool) {
	for {
		// Default is the rule: any other token ends the specifier sequence and
		// begins the type.
		//exhaustive:ignore
		switch p.kind() {
		case lexer.Const:
			isConst = true
		case lexer.Constexpr:
			isConstexpr = true
		default:
			return isConst, isConstexpr
		}
		p.next()
	}
}

// rejectTrailingSpecifier reports a 'const' or 'constexpr' written after the
// type, which C admits and MicroC does not. It consumes the keyword, so the
// declaration it qualifies still parses and costs one diagnostic apiece.
func (p *parser) rejectTrailingSpecifier() {
	for {
		var what string
		switch {
		case p.at(lexer.Const):
			what = "const"
		case p.at(lexer.Constexpr):
			what = "constexpr"
		default:
			return
		}
		p.errorf(p.tok().Pos, "%s must precede the type in MicroC", what)
		p.next()
	}
}

func isOpenBracket(k lexer.Kind) bool {
	return k == lexer.Lparen || k == lexer.Lbrack || k == lexer.Lbrace
}

func isCloseBracket(k lexer.Kind) bool {
	return k == lexer.Rparen || k == lexer.Rbrack || k == lexer.Rbrace
}

// skipBracketGroup consumes a balanced group when the scan point sits on its
// opening bracket of kind open, so that rejecting a construct like "sizeof(int)"
// or a whole switch body costs one diagnostic rather than one per token inside.
func (p *parser) skipBracketGroup(open lexer.Kind) {
	if !p.at(open) {
		return
	}
	depth := 0
	for !p.at(lexer.EOF) {
		k := p.kind()
		if isOpenBracket(k) {
			depth++
		} else if isCloseBracket(k) && depth > 0 {
			depth--
		}
		p.next()
		if depth == 0 {
			return
		}
	}
}

// skipToCloseParen advances to the ')' that closes the enclosing list, or to a
// ';' that shows the list was never closed, and leaves that token in place for
// the caller. A rejected parameter then costs one diagnostic rather than one
// per remaining token.
func (p *parser) skipToCloseParen() {
	depth := 0
	for !p.at(lexer.EOF) {
		k := p.kind()
		if depth == 0 && (k == lexer.Rparen || k == lexer.Semicolon) {
			return
		}
		if isOpenBracket(k) {
			depth++
		} else if isCloseBracket(k) && depth > 0 {
			depth--
		}
		p.next()
	}
}

// scalarKinds maps the one-word type keywords MicroC accepts to their tree
// form. The integer type is two words and is read by [parser.parseLongLong].
var scalarKinds = map[lexer.Kind]ast.ScalarKind{
	lexer.Bool:   ast.Bool,
	lexer.Double: ast.Double,
	lexer.Dev:    ast.Dev,
	lexer.Void:   ast.Void,
}

// startsTypeName reports whether a token begins a type, which is what makes a
// cast distinguishable from a parenthesized expression. MicroC has no typedefs,
// so the keyword decides it.
func startsTypeName(k lexer.Kind) bool {
	if k == lexer.Long {
		return true
	}
	_, ok := scalarKinds[k]
	return ok
}

func startsDecl(k lexer.Kind) bool {
	return k == lexer.Const || k == lexer.Constexpr || startsTypeName(k)
}

var stmtStarters = map[lexer.Kind]bool{
	lexer.Lbrace:   true,
	lexer.If:       true,
	lexer.While:    true,
	lexer.Do:       true,
	lexer.For:      true,
	lexer.Switch:   true,
	lexer.Break:    true,
	lexer.Continue: true,
	lexer.Return:   true,
}

func startsStmt(k lexer.Kind) bool { return stmtStarters[k] || startsDecl(k) }

// syncDecl skips to the next plausible declaration boundary. It always consumes
// at least one token unless already at EOF.
func (p *parser) syncDecl() {
	p.sync(startsDecl, false)
}

// syncStmt skips to the next plausible statement boundary. It stops before a
// closing brace that is not its own, so the enclosing block still sees its
// terminator.
func (p *parser) syncStmt() {
	p.sync(startsStmt, true)
}

// sync skips forward until starts accepts an unnested token, a terminator
// closes the construct, or the input runs out. Nesting is counted over all
// three bracket pairs, so a broken function body, initializer list, or
// parameter list is stepped over rather than reentered.
func (p *parser) sync(starts func(lexer.Kind) bool, stopAtRbrace bool) {
	start := p.pos
	depth := 0
	for !p.at(lexer.EOF) {
		k := p.kind()
		if depth == 0 && p.pos > start {
			if starts(k) {
				return
			}
			if stopAtRbrace && k == lexer.Rbrace {
				return
			}
		}
		p.next()
		if isOpenBracket(k) {
			depth++
			continue
		}
		if isCloseBracket(k) {
			if depth > 0 {
				depth--
			}
			if depth == 0 && k == lexer.Rbrace {
				// A tagged type or a brace initializer ends "};", and leaving
				// the semicolon behind would cost a second diagnostic.
				p.accept(lexer.Semicolon)
				return
			}
			continue
		}
		if k == lexer.Semicolon && depth == 0 {
			return
		}
	}
}

func (p *parser) parseFile() *ast.File {
	f := &ast.File{Name: p.file, Start: p.tok().Pos}
	for !p.at(lexer.EOF) {
		before := p.pos
		f.Decls = append(f.Decls, p.parseDecl(false))
		if p.pos == before {
			p.next()
		}
	}
	return f
}

// parseDecl parses one declaration. inBlock marks a declaration appearing as a
// statement, where a function definition is not allowed.
func (p *parser) parseDecl(inBlock bool) ast.Decl {
	from := p.tok().Pos
	isConst, isConstexpr := p.parseSpecifiers()
	if p.reportExcluded() {
		return p.badDecl(from, inBlock)
	}
	base, ok := p.parseTypeSpec()
	if !ok {
		p.errorf(p.tok().Pos, "expected a type, found %s", p.tok().Describe())
		return p.badDecl(from, inBlock)
	}

	d, ok := p.parseDeclarator(base)
	if !ok {
		return p.badDecl(from, inBlock)
	}
	if d.isFunc {
		return p.parseFuncRest(from, isConst, isConstexpr, inBlock, d)
	}

	decl := &ast.VarDecl{
		DeclPos:   from,
		Const:     isConst,
		Constexpr: isConstexpr,
		Type:      d.typ,
		Name:      d.name,
		NamePos:   d.namePos,
	}
	if p.accept(lexer.Assign) {
		decl.Init = p.parseInitializer()
	}
	if p.at(lexer.Comma) {
		p.errorf(p.tok().Pos, "MicroC declares one variable per declaration")
		if inBlock {
			p.syncStmt()
		} else {
			p.syncDecl()
		}
		return decl
	}
	p.expectSemi()
	return decl
}

func (p *parser) parseFuncRest(from source.Position, isConst, isConstexpr, inBlock bool, d declarator) ast.Decl {
	if isConst {
		p.errorf(from, "const is not valid on a function")
	}
	if isConstexpr {
		p.errorf(from, "constexpr is not valid on a function; MicroC has no constexpr function")
	}
	fn := &ast.FuncDecl{
		DeclPos: from,
		Result:  d.typ,
		Name:    d.name,
		NamePos: d.namePos,
		Params:  d.params,
	}
	if !p.at(lexer.Lbrace) {
		p.expectSemi()
		return fn
	}
	if inBlock {
		p.errorf(from, "nested function definitions are not supported in MicroC")
	}
	fn.Body = p.parseBlock()
	return fn
}

func (p *parser) badDecl(from source.Position, inBlock bool) ast.Decl {
	if inBlock {
		p.syncStmt()
	} else {
		p.syncDecl()
	}
	return &ast.BadDecl{From: from}
}

// parseTypeSpec parses a built-in type keyword. It neither consumes nor reports
// when the current token cannot begin a type, leaving the caller to say what it
// was looking for.
func (p *parser) parseTypeSpec() (ast.Type, bool) {
	t := p.tok()
	if t.Kind == lexer.Long {
		return p.parseLongLong(), true
	}
	k, ok := scalarKinds[t.Kind]
	if !ok {
		return nil, false
	}
	p.next()
	return &ast.ScalarType{TypePos: t.Pos, Kind: k}, true
}

// parseLongLong reads the integer type, which is spelled with both words and
// with nothing after them.
//
// C's long is 32 bits on some implementations and long long is at least 64 on
// every one, so naming the wider type is what makes every value MicroC holds
// exact in the C type a program is read as. A spelling this refuses is still
// consumed and answered with the integer type, so one mistake costs one
// diagnostic and the declaration around it still parses.
func (p *parser) parseLongLong() ast.Type {
	from := p.tok().Pos
	p.next()
	if !p.accept(lexer.Long) {
		p.errorf(from, "MicroC's integer type is 'long long'; 'long' alone is 32 bits on some C implementations, which is narrower than the values this machine holds")
	} else if p.at(lexer.Int) {
		p.errorf(p.tok().Pos, "MicroC writes the integer type as 'long long', without the trailing 'int'")
		p.next()
	}
	return &ast.ScalarType{TypePos: from, Kind: ast.Int}
}

func (p *parser) parsePointers(base ast.Type) ast.Type {
	for p.at(lexer.Mul) {
		star := p.tok().Pos
		p.next()
		p.rejectTrailingSpecifier()
		base = &ast.PointerType{Star: star, Elem: base}
	}
	return base
}

// declarator is the part of a declaration that follows the type keyword: the
// pointer stars, the name, and either an array suffix or a parameter list.
type declarator struct {
	typ     ast.Type
	name    string
	namePos source.Position
	isFunc  bool
	params  []*ast.Param
}

func (p *parser) parseDeclarator(base ast.Type) (declarator, bool) {
	typ := p.parsePointers(base)
	if p.at(lexer.Lparen) {
		p.errorf(p.tok().Pos, "function pointers are not supported in MicroC")
		return declarator{}, false
	}
	p.rejectTrailingSpecifier()
	nameTok, ok := p.expect(lexer.Ident)
	if !ok {
		return declarator{}, false
	}
	d := declarator{name: nameTok.Text, namePos: nameTok.Pos}
	if p.at(lexer.Lparen) {
		d.typ = typ
		d.isFunc = true
		d.params = p.parseParams()
		return d, true
	}
	d.typ = p.parseArraySuffix(typ, true)
	return d, true
}

// parseArraySuffix wraps typ in an ArrayType when a subscript follows.
// requireSize rejects an omitted bound, which only a parameter may write, where
// the array decays to a pointer. A second subscript is rejected: arrays are
// one-dimensional.
func (p *parser) parseArraySuffix(typ ast.Type, requireSize bool) ast.Type {
	if !p.at(lexer.Lbrack) {
		return typ
	}
	lbrack := p.tok().Pos
	p.next()
	var size ast.Expr
	if !p.at(lexer.Rbrack) {
		size = p.parseExpr()
	} else if requireSize {
		p.errorf(lbrack, "an array bound is required outside a parameter list")
	}
	p.expect(lexer.Rbrack)

	if p.at(lexer.Lbrack) {
		p.errorf(p.tok().Pos, "multi-dimensional arrays are not supported in MicroC; index a flat array")
		for p.at(lexer.Lbrack) {
			p.skipBracketGroup(lexer.Lbrack)
		}
	}
	return &ast.ArrayType{Lbrack: lbrack, Elem: typ, Size: size}
}

// parseParams parses a parameter list. "(void)" and "()" both yield no
// parameters.
func (p *parser) parseParams() []*ast.Param {
	p.expect(lexer.Lparen)
	if p.at(lexer.Void) && p.peek(1).Kind == lexer.Rparen {
		p.next()
		p.next()
		return nil
	}
	if p.accept(lexer.Rparen) {
		return nil
	}
	var params []*ast.Param
	for {
		if p.at(lexer.Ellipsis) {
			p.errorf(p.tok().Pos, "variadic parameters are not supported in MicroC")
			p.next()
			break
		}
		prm, ok := p.parseParam()
		if !ok {
			p.skipToCloseParen()
			break
		}
		params = append(params, prm)
		if !p.accept(lexer.Comma) {
			break
		}
	}
	p.expect(lexer.Rparen)
	return params
}

func (p *parser) parseParam() (*ast.Param, bool) {
	from := p.tok().Pos
	isConst, isConstexpr := p.parseSpecifiers()
	if isConstexpr {
		// C admits no storage class but 'register' on a parameter, and a
		// parameter names whatever the call site passed, which is not a constant.
		p.errorf(from, "constexpr is not valid on a parameter")
	}
	if p.reportExcluded() {
		return nil, false
	}
	base, ok := p.parseTypeSpec()
	if !ok {
		p.errorf(p.tok().Pos, "expected a parameter type, found %s", p.tok().Describe())
		return nil, false
	}
	typ := p.parsePointers(base)
	if p.at(lexer.Lparen) {
		p.errorf(p.tok().Pos, "function pointers are not supported in MicroC")
		return nil, false
	}
	p.rejectTrailingSpecifier()
	prm := &ast.Param{ParamPos: from, Const: isConst}
	if p.at(lexer.Ident) {
		prm.Name = p.tok().Text
		prm.NamePos = p.tok().Pos
		p.next()
	}
	prm.Type = p.parseArraySuffix(typ, false)
	return prm, true
}

func (p *parser) parseInitializer() ast.Expr {
	if !p.at(lexer.Lbrace) {
		return p.parseExpr()
	}
	lit := &ast.InitListExpr{Lbrace: p.tok().Pos}
	p.next()
	for !p.at(lexer.Rbrace) && !p.at(lexer.EOF) {
		before := p.pos
		lit.Elems = append(lit.Elems, p.parseInitElem())
		if p.pos == before {
			p.next()
		}
		if !p.accept(lexer.Comma) {
			break
		}
	}
	p.expect(lexer.Rbrace)
	return lit
}

// parseInitElem parses one element of a brace initializer. A nested brace has
// no valid target, since arrays are one-dimensional and structs are excluded.
func (p *parser) parseInitElem() ast.Expr {
	if !p.at(lexer.Lbrace) {
		return p.parseExpr()
	}
	from := p.tok().Pos
	p.errorf(from, "nested brace initializers are not supported in MicroC; arrays are one-dimensional")
	p.skipBracketGroup(lexer.Lbrace)
	return &ast.BadExpr{From: from}
}

func (p *parser) parseBlock() *ast.BlockStmt {
	lbrace, _ := p.expect(lexer.Lbrace)
	block := &ast.BlockStmt{Lbrace: lbrace.Pos}
	for !p.at(lexer.Rbrace) && !p.at(lexer.EOF) {
		before := p.pos
		block.Stmts = append(block.Stmts, p.parseStmt())
		if p.pos == before {
			p.next()
		}
	}
	rbrace, _ := p.expect(lexer.Rbrace)
	block.Rbrace = rbrace.Pos
	return block
}

func (p *parser) parseStmt() ast.Stmt {
	from := p.tok().Pos
	if p.reportExcluded() {
		return p.badStmt(from)
	}

	// Default is the rule: a token that begins no statement keyword begins
	// either a declaration or an expression statement.
	//exhaustive:ignore
	switch k := p.kind(); k {
	case lexer.Lbrace:
		return p.parseBlock()
	case lexer.Semicolon:
		p.next()
		return &ast.EmptyStmt{Semi: from}
	case lexer.If:
		return p.parseIf()
	case lexer.While:
		return p.parseWhile()
	case lexer.Do:
		return p.parseDo()
	case lexer.For:
		return p.parseFor()
	case lexer.Switch:
		return p.parseSwitch()
	case lexer.Break:
		p.next()
		p.expectSemi()
		return &ast.BreakStmt{BreakPos: from}
	case lexer.Continue:
		p.next()
		p.expectSemi()
		return &ast.ContinueStmt{ContinuePos: from}
	case lexer.Return:
		return p.parseReturn()
	case lexer.Case, lexer.Default:
		p.errorf(from, "%s is only valid inside a switch", k)
		return p.badStmt(from)
	default:
		if startsDecl(k) {
			if s, ok := p.parseDecl(true).(ast.Stmt); ok {
				return s
			}
			return &ast.BadStmt{From: from}
		}
		return p.parseExprStmt()
	}
}

func (p *parser) badStmt(from source.Position) ast.Stmt {
	p.syncStmt()
	return &ast.BadStmt{From: from}
}

func (p *parser) parseExprStmt() ast.Stmt {
	x := p.parseExpr()
	p.expectSemi()
	return &ast.ExprStmt{X: x}
}

// parseParenExpr parses the parenthesized head of a control statement: the
// condition of if, while, and do, and the tag of switch. The grammar spells the
// two alike. That a switch tag is not a condition, and so does not convert, is
// checked where the types are known.
func (p *parser) parseParenExpr() ast.Expr {
	p.expect(lexer.Lparen)
	x := p.parseExpr()
	p.expect(lexer.Rparen)
	return x
}

func (p *parser) parseIf() ast.Stmt {
	s := &ast.IfStmt{IfPos: p.tok().Pos}
	p.next()
	s.Cond = p.parseParenExpr()
	s.Then = p.parseStmt()
	if p.accept(lexer.Else) {
		s.Else = p.parseStmt()
	}
	return s
}

func (p *parser) parseWhile() ast.Stmt {
	s := &ast.WhileStmt{WhilePos: p.tok().Pos}
	p.next()
	s.Cond = p.parseParenExpr()
	s.Body = p.parseStmt()
	return s
}

func (p *parser) parseDo() ast.Stmt {
	s := &ast.DoStmt{DoPos: p.tok().Pos}
	p.next()
	s.Body = p.parseStmt()
	if _, ok := p.expect(lexer.While); !ok {
		return s
	}
	s.Cond = p.parseParenExpr()
	p.expectSemi()
	return s
}

func (p *parser) parseFor() ast.Stmt {
	s := &ast.ForStmt{ForPos: p.tok().Pos}
	p.next()
	p.expect(lexer.Lparen)

	switch {
	case p.accept(lexer.Semicolon):
	case startsDecl(p.kind()):
		decl := p.parseDecl(true)
		if init, ok := decl.(ast.Stmt); ok {
			s.Init = init
		}
	default:
		s.Init = p.parseExprStmt()
	}

	if !p.at(lexer.Semicolon) {
		s.Cond = p.parseExpr()
	}
	p.expect(lexer.Semicolon)

	if !p.at(lexer.Rparen) {
		s.Post = p.parseExpr()
	}
	p.expect(lexer.Rparen)

	s.Body = p.parseStmt()
	return s
}

func (p *parser) parseSwitch() ast.Stmt {
	s := &ast.SwitchStmt{SwitchPos: p.tok().Pos}
	p.next()
	s.Tag = p.parseParenExpr()

	lbrace, ok := p.expect(lexer.Lbrace)
	s.Lbrace = lbrace.Pos
	if !ok {
		return s
	}
	var current *ast.CaseClause
	for !p.at(lexer.Rbrace) && !p.at(lexer.EOF) {
		before := p.pos
		if p.at(lexer.Case) || p.at(lexer.Default) {
			current = p.parseCaseHeader()
			s.Cases = append(s.Cases, current)
			continue
		}
		stmt := p.parseStmt()
		if current == nil {
			p.errorf(stmt.Pos(), "a statement in a switch body must follow a case or default label")
		} else {
			current.Body = append(current.Body, stmt)
		}
		if p.pos == before {
			p.next()
		}
	}
	p.expect(lexer.Rbrace)
	return s
}

// parseCaseHeader parses a case or default label. A case value is parsed as a
// conditional expression, which is C's grammar for it; that it evaluates to a
// constant and does not repeat an earlier label is checked by semantic
// analysis, which is where constant evaluation lives.
func (p *parser) parseCaseHeader() *ast.CaseClause {
	c := &ast.CaseClause{CasePos: p.tok().Pos}
	isCase := p.at(lexer.Case)
	p.next()
	if isCase {
		c.Value = p.parseConditionalExpr()
	}
	p.expect(lexer.Colon)
	return c
}

func (p *parser) parseReturn() ast.Stmt {
	s := &ast.ReturnStmt{ReturnPos: p.tok().Pos}
	p.next()
	if !p.at(lexer.Semicolon) {
		s.Result = p.parseExpr()
	}
	p.expectSemi()
	return s
}
