package tsparse

import (
	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/tsnode"
	ts "github.com/tree-sitter/go-tree-sitter"
)

// The C grammar reads "a * b;" as a pointer declaration wherever a names a
// type; MicroC has no typedef, so a name outside [scalarTypes] means the
// statement is a product instead, read back through each declarator form's
// expression counterpart. A form with none is where the expression ended.

// misreadDeclaration reports whether a declaration is one the grammar built
// out of an expression: the type has to be the first thing it writes and
// spell a name outside [scalarTypes] — a qualifier or attribute in front
// means it is an ordinary declaration instead.
func (c *converter) misreadDeclaration(n *ts.Node) bool {
	children := c.children(n)
	if len(children) == 0 {
		return false
	}
	name := children[0].node
	if tsnode.Kind(name.Kind()) == tsnode.KindSizedTypeSpecifier {
		words := c.children(name)
		if len(words) == 0 || words[0].kind != tsnode.KindTypeIdentifier {
			return false
		}
		name = words[0].node
	} else if tsnode.Kind(name.Kind()) != tsnode.KindTypeIdentifier {
		return false
	}
	_, isType := scalarTypes[c.text(name)]
	return !isType
}

// misreadStmt reads a misread declaration back as the expression statement it
// is. Source that cannot be read as one has already been reported on, and
// becomes a [ast.BadStmt] so the statements after it keep their positions.
func (c *converter) misreadStmt(n *ts.Node) ast.Stmt {
	x, read := c.misreadExpr(n)
	if !read {
		return &ast.BadStmt{From: c.start(n)}
	}
	return &ast.ExprStmt{X: x}
}

// misreadCallStmt reads a macro type specifier back as the call it was written
// as. Source that cannot be read as one has already been reported on, and
// becomes a [ast.BadStmt] so the statements after it keep their positions.
func (c *converter) misreadCallStmt(n *ts.Node) ast.Stmt {
	x, read := c.misreadCall(n)
	if !read {
		return &ast.BadStmt{From: c.start(n)}
	}
	return &ast.ExprStmt{X: x}
}

// misreadCall reads a macro type specifier back as the call it was written
// as, and says whether the source is one: a name applied to something the
// grammar read as a type, which MicroC reads as a call whose one argument the
// language reads as a value.
func (c *converter) misreadCall(n *ts.Node) (ast.Expr, bool) {
	name, descriptor := field(n, tsnode.FieldName), field(n, tsnode.FieldType)
	lparen, marked := c.anonymous(n, tsnode.KindLparen)
	if name == nil || descriptor == nil || !marked {
		return nil, false
	}
	fun, named := c.nameExpr(name)
	arg, read := c.argumentExpr(descriptor)
	if !named || !read {
		return nil, false
	}
	return &ast.CallExpr{Lparen: lparen, Fun: fun, Args: []ast.Expr{arg}}, true
}

// misreadExpr reads the whole declaration back. The declarators the grammar
// separated with commas are one expression apiece, which is the operator MicroC
// excludes rather than a list.
func (c *converter) misreadExpr(n *ts.Node) (ast.Expr, bool) {
	var typ, declarator *ts.Node
	for _, ch := range c.children(n) {
		switch {
		case ch.field == tsnode.FieldType && typ == nil:
			typ = ch.node
		case ch.field == tsnode.FieldDeclarator && declarator == nil:
			declarator = ch.node
		case ch.kind == tsnode.KindSemi:
		case ch.kind == tsnode.KindComma:
			c.errorf(c.start(ch.node), "%s", commaOperatorMsg)
			return nil, false
		default:
			c.endedBefore(ch.node)
			return nil, false
		}
	}
	if typ == nil || declarator == nil {
		return nil, false
	}
	// The grammar reads "a long long b" as one type specifier, having taken the
	// name the statement is for the front of the type behind it. The statement
	// is that name and ended in front of the type.
	if tsnode.Kind(typ.Kind()) == tsnode.KindSizedTypeSpecifier {
		if words := c.children(typ); len(words) > 1 {
			c.endedBefore(words[1].node)
		}
		return nil, false
	}
	return c.multiplied(typ, declarator)
}

// multiplied builds the product the source was written as: the type is the left
// operand, and the star the grammar read as the declarator's own is the
// operator.
func (c *converter) multiplied(typ, n *ts.Node) (ast.Expr, bool) {
	if bad(n) {
		return nil, false
	}
	if tsnode.Kind(n.Kind()) == tsnode.KindInitDeclarator {
		return c.assigned(typ, n)
	}
	if tsnode.Kind(n.Kind()) != tsnode.KindPointerDeclarator {
		// Two complete expressions with nothing joining them, so the first of
		// them is the whole statement and the second is past its end.
		c.endedBefore(n)
		return nil, false
	}
	star, marked := c.anonymous(n, tsnode.KindStar)
	if !marked || !c.misreadParts(n) {
		return nil, false
	}
	inner := field(n, tsnode.FieldDeclarator)
	if inner == nil {
		return nil, false
	}
	x, named := c.nameExpr(typ)
	y, read := c.declaratorExpr(inner)
	if !named || !read {
		return nil, false
	}
	return &ast.BinaryExpr{OpPos: star, Op: ast.Mul, X: x, Y: y}, true
}

// assigned reads an initialized declarator back as the assignment it was
// written as. The whole product is what is assigned to, because '=' binds
// looser than '*'.
func (c *converter) assigned(typ, n *ts.Node) (ast.Expr, bool) {
	eq, marked := c.anonymous(n, tsnode.KindEq)
	target, value := field(n, tsnode.FieldDeclarator), field(n, tsnode.FieldValue)
	if !marked || target == nil || value == nil {
		return nil, false
	}
	// A brace initializer is what a declaration takes and an assignment does
	// not, so the statement ended at the brace.
	if tsnode.Kind(value.Kind()) == tsnode.KindInitializerList {
		c.endedBefore(value)
		return nil, false
	}
	x, read := c.multiplied(typ, target)
	if !read {
		return nil, false
	}
	return &ast.AssignExpr{OpPos: eq, Op: ast.Assign, Target: x, Value: c.expr(value)}, true
}

// misreadForms reads one layer of a declarator back as the expression the
// grammar built it out of. A form with no counterpart has no entry. It is
// assigned in init because every reader in it reaches back through
// [converter.declaratorExpr], which reads the table.
var misreadForms map[tsnode.Kind]func(*converter, *ts.Node) (ast.Expr, bool)

func init() {
	misreadForms = map[tsnode.Kind]func(*converter, *ts.Node) (ast.Expr, bool){
		tsnode.KindArrayDeclarator:    (*converter).indexExpr,
		tsnode.KindFunctionDeclarator: (*converter).calledExpr,
		tsnode.KindIdentifier:         (*converter).nameExpr,
		tsnode.KindPointerDeclarator:  (*converter).derefExpr,
	}
}

func (c *converter) declaratorExpr(n *ts.Node) (ast.Expr, bool) {
	if bad(n) {
		return nil, false
	}
	read, known := misreadForms[tsnode.Kind(n.Kind())]
	if !known {
		c.endedBefore(n)
		return nil, false
	}
	return read(c, n)
}

func (c *converter) derefExpr(n *ts.Node) (ast.Expr, bool) {
	star, marked := c.anonymous(n, tsnode.KindStar)
	if !marked {
		return nil, false
	}
	x, read := c.wrappedExpr(n)
	if !read {
		return nil, false
	}
	return &ast.UnaryExpr{OpPos: star, Op: ast.Deref, X: x}, true
}

func (c *converter) indexExpr(n *ts.Node) (ast.Expr, bool) {
	lbrack, marked := c.anonymous(n, tsnode.KindLbrack)
	if !marked {
		return nil, false
	}
	size := field(n, tsnode.FieldSize)
	if size == nil {
		// A declaration writes the empty subscript to mean an array whose bound
		// the initializer decides, and a subscript expression always indexes by
		// something.
		if rbrack, closed := c.anonymous(n, tsnode.KindRbrack); closed {
			c.errorf(rbrack, "expected an expression, found ']'")
		}
		return nil, false
	}
	x, read := c.wrappedExpr(n)
	if !read {
		return nil, false
	}
	return &ast.IndexExpr{Lbrack: lbrack, X: x, Index: c.expr(size)}, true
}

func (c *converter) calledExpr(n *ts.Node) (ast.Expr, bool) {
	params := field(n, tsnode.FieldParameters)
	if params == nil {
		return nil, false
	}
	args, read := c.arguments(params)
	x, named := c.wrappedExpr(n)
	if !read || !named {
		return nil, false
	}
	return &ast.CallExpr{Lparen: c.start(params), Fun: x, Args: args}, true
}

// wrappedExpr reads the declarator one layer wraps, having refused whatever
// else that layer holds.
func (c *converter) wrappedExpr(n *ts.Node) (ast.Expr, bool) {
	if !c.misreadParts(n) {
		return nil, false
	}
	wrapped := field(n, tsnode.FieldDeclarator)
	if wrapped == nil {
		return nil, false
	}
	return c.declaratorExpr(wrapped)
}

// misreadParts refuses whatever hangs off one layer of a declarator that the
// expression it is being read back as has no room for, and says whether the
// layer held nothing else. A qualifier is the one thing C admits beside them,
// and it says how a declaration is stored rather than what a value is.
func (c *converter) misreadParts(n *ts.Node) bool {
	for _, ch := range c.children(n) {
		if declaratorFields[ch.field] || declaratorTokens[ch.kind] {
			continue
		}
		c.endedBefore(ch.node)
		return false
	}
	return true
}

// arguments reads a parameter list back as the argument list it was written
// as. Every argument arrives as a parameter declaration, since a list holding
// one thing the grammar can read as a parameter is a parameter list whole.
func (c *converter) arguments(n *ts.Node) ([]ast.Expr, bool) {
	var args []ast.Expr
	for _, ch := range c.children(n) {
		var arg ast.Expr
		read := false
		// Default is the rule: tsnode.Kind is the grammar's whole alphabet, and
		// what a construct is written with is a handful of it.
		//exhaustive:ignore
		switch ch.kind {
		case tsnode.KindLparen, tsnode.KindRparen, tsnode.KindComma:
			continue
		case tsnode.KindIdentifier:
			// The old-style parameter list spells a bare name, which is what a
			// call spells an argument with.
			arg, read = c.nameExpr(ch.node)
		case tsnode.KindParameterDeclaration:
			arg, read = c.argumentExpr(ch.node)
		default:
			c.notAnArgument(ch.node)
		}
		if !read {
			return nil, false
		}
		args = append(args, arg)
	}
	return args, true
}

// argumentExpr reads a parameter declaration or a type descriptor back as the
// argument it was written as. A parameter is a type and a name, and an
// argument is a value; what an abstract declarator wraps the name in is what
// an index or a call is written with.
func (c *converter) argumentExpr(n *ts.Node) (ast.Expr, bool) {
	var typ, declarator *ts.Node
	for _, ch := range c.children(n) {
		switch {
		case ch.field == tsnode.FieldType && typ == nil:
			typ = ch.node
		case ch.field == tsnode.FieldDeclarator && declarator == nil:
			declarator = ch.node
		default:
			c.notAnArgument(ch.node)
			return nil, false
		}
	}
	if typ == nil || tsnode.Kind(typ.Kind()) != tsnode.KindTypeIdentifier {
		c.notAnArgument(n)
		return nil, false
	}
	if declarator == nil {
		return c.nameExpr(typ)
	}
	return c.appliedExpr(typ, declarator)
}

// appliedExpr reads back what a parameter's declarator does to the name the
// grammar took for its type. A form with no counterpart is one no expression
// writes, so the source was never the argument it is being read as.
func (c *converter) appliedExpr(typ, n *ts.Node) (ast.Expr, bool) {
	if bad(n) {
		return nil, false
	}
	// Default is the rule: a declarator has more forms than an expression has
	// counterparts for.
	//exhaustive:ignore
	switch tsnode.Kind(n.Kind()) {
	case tsnode.KindAbstractArrayDeclarator:
		return c.appliedIndexExpr(typ, n)
	case tsnode.KindAbstractFunctionDeclarator:
		return c.appliedCallExpr(typ, n)
	case tsnode.KindPointerDeclarator:
		// A star between the type and a name of its own is the multiplication
		// joining the two, exactly as it is one declarator out.
		return c.multiplied(typ, n)
	}
	c.notAnArgument(n)
	return nil, false
}

func (c *converter) appliedIndexExpr(typ, n *ts.Node) (ast.Expr, bool) {
	lbrack, marked := c.anonymous(n, tsnode.KindLbrack)
	if !marked {
		return nil, false
	}
	size := field(n, tsnode.FieldSize)
	if size == nil {
		// A parameter writes the empty subscript to mean an array whose bound
		// the caller decides, and a subscript expression always indexes by
		// something.
		if rbrack, closed := c.anonymous(n, tsnode.KindRbrack); closed {
			c.errorf(rbrack, "expected an expression, found ']'")
		}
		return nil, false
	}
	x, read := c.appliedTo(typ, n)
	if !read {
		return nil, false
	}
	return &ast.IndexExpr{Lbrack: lbrack, X: x, Index: c.expr(size)}, true
}

func (c *converter) appliedCallExpr(typ, n *ts.Node) (ast.Expr, bool) {
	params := field(n, tsnode.FieldParameters)
	if params == nil {
		return nil, false
	}
	args, read := c.arguments(params)
	x, named := c.appliedTo(typ, n)
	if !read || !named {
		return nil, false
	}
	return &ast.CallExpr{Lparen: c.start(params), Fun: x, Args: args}, true
}

// appliedTo reads what one layer of a parameter's declarator applies to, having
// refused whatever else that layer holds. An abstract declarator names nothing,
// so the layer at the bottom applies to the type itself.
func (c *converter) appliedTo(typ, n *ts.Node) (ast.Expr, bool) {
	if !c.misreadParts(n) {
		return nil, false
	}
	if inner := field(n, tsnode.FieldDeclarator); inner != nil {
		return c.appliedExpr(typ, inner)
	}
	return c.nameExpr(typ)
}

// endedBefore reports the terminator a statement was owed in front of what
// the source wrote instead. What stands there belongs to a declaration, which
// this statement is not, so the sentence names where the expression ended.
func (c *converter) endedBefore(n *ts.Node) {
	if c.unreadableAt(n.StartByte()) {
		return
	}
	c.errorf(c.endOfPrevToken(n.StartByte()), "expected ';', found %s", c.describeAt(n.StartByte()))
}

// notAnArgument reports what stands in an argument list that no expression
// denotes a value with.
func (c *converter) notAnArgument(n *ts.Node) {
	c.errorf(c.start(n), "expected an expression, found %s", c.describeAt(n.StartByte()))
}
