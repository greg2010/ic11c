package sema

import (
	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/source"
)

// expr checks x and records its type.
func (c *checker) expr(x ast.Expr) *Type {
	t := c.exprType(x)
	c.prog.Types[x] = t
	c.diagnoseFold(x)
	return t
}

func (c *checker) exprType(x ast.Expr) *Type {
	switch x := x.(type) {
	case *ast.Ident:
		return c.ident(x)
	case *ast.IntLit, *ast.CharLit:
		return IntType
	case *ast.FloatLit:
		return DoubleType
	case *ast.BoolLit:
		return BoolType
	case *ast.StringLit:
		c.errorf(x.ValuePos, "a string literal is valid only as the argument of __ic_hash")
		return invalidType
	case *ast.UnaryExpr:
		return c.unary(x)
	case *ast.IncDecExpr:
		return c.incDec(x)
	case *ast.BinaryExpr:
		return c.binary(x)
	case *ast.AssignExpr:
		return c.assignExpr(x)
	case *ast.CondExpr:
		return c.cond(x)
	case *ast.IndexExpr:
		return c.index(x)
	case *ast.CallExpr:
		return c.call(x)
	case *ast.CastExpr:
		return c.cast(x)
	case *ast.InitListExpr:
		c.errorf(x.Lbrace, "a brace initializer is valid only as a variable's initializer")
		for _, e := range x.Elems {
			c.expr(e)
		}
		return invalidType
	default:
		// A BadExpr is source the parser already reported.
		return invalidType
	}
}

func (c *checker) ident(x *ast.Ident) *Type {
	sym := c.scope.lookup(x.Name)
	if sym == nil {
		switch {
		case intrinsics[x.Name] != nil:
			c.errorf(x.NamePos, "the intrinsic '%s' may only be called", x.Name)
		case isReservedName(x.Name):
			c.errorf(x.NamePos, "'%s' is not an intrinsic, and names beginning '%s' are reserved for them", x.Name, reservedPrefix)
		case isDevicePinSpelling(x.Name):
			// The spelling resolves without scope wherever a device belongs, so
			// nothing declares it and nothing may. Saying it is undeclared names
			// the one repair the language refuses.
			c.errorf(x.NamePos, "'%s' names a device pin rather than a value, and no cast turns one into a number; "+
				"a device stands only in a device position, which the chip resolves when the line is assembled", x.Name)
		default:
			c.errorf(x.NamePos, "undeclared name '%s'", x.Name)
		}
		return invalidType
	}
	c.prog.Uses[x] = sym
	if sym.Kind == FuncName {
		c.errorf(x.NamePos, "'%s' is a function, and a function name is not a value in MicroC", x.Name)
		return invalidType
	}
	return sym.Type
}

func (c *checker) unary(x *ast.UnaryExpr) *Type {
	switch x.Op {
	case ast.Plus, ast.Neg:
		t := unqual(decay(c.expr(x.X)))
		if !isArithmetic(t) {
			if t.Kind() != Invalid {
				c.errorf(x.X.Pos(), "the operand of unary '%s' must be a long long or a double, found %s", x.Op, t)
			}
			return invalidType
		}
		return t
	case ast.BitNot:
		t := c.expr(x.X)
		if !c.requireInt(t, x.X, "the operand of unary '"+x.Op.String()+"'") {
			return invalidType
		}
		return IntType
	case ast.LogicalNot:
		t := c.expr(x.X)
		c.condition(x.X, t, "the operand of '!'")
		return BoolType
	case ast.AddrOf:
		return c.addrOf(x)
	case ast.Deref:
		return c.deref(x)
	default:
		return invalidType
	}
}

// deref checks unary '*'. Where it stands directly over a '&', '*&E' reads
// E without ever forming an address, so the one-past-the-end index
// [checker.checkIndexBound] allows under '&' is taken away again here.
func (c *checker) deref(x *ast.UnaryExpr) *Type {
	outer := c.dereferenced
	if x == c.addressed {
		c.dereferenced = nil
	} else {
		c.dereferenced = x.X
	}
	t := decay(c.expr(x.X))
	c.dereferenced = outer
	if t.Kind() == Invalid {
		return invalidType
	}
	if t.Kind() != Pointer {
		c.errorf(x.OpPos, "the operand of unary '*' must be a pointer, found %s", t)
		return invalidType
	}
	c.checkDerefSlot(x)
	return t.Elem()
}

func (c *checker) addrOf(x *ast.UnaryExpr) *Type {
	outer := c.addressed
	if x == c.dereferenced {
		c.addressed = nil
	} else {
		c.addressed = x.X
	}
	t := c.expr(x.X)
	c.addressed = outer
	if t.Kind() == Invalid {
		return invalidType
	}
	if t.Kind() == Array {
		c.errorf(x.OpPos, "the address of an array is not expressible in MicroC; an array name already decays to a pointer")
		return invalidType
	}
	if t.Kind() == Dev {
		c.errorf(x.OpPos, "the address of a dev is not expressible in MicroC; a device is resolved when the line is assembled and occupies no memory slot")
		return invalidType
	}
	if !isLvalue(x.X) {
		c.errorf(x.OpPos, "the operand of unary '&' must name an object")
		return invalidType
	}
	// Only a name moves an object into the data region: '&p[i]' is the
	// arithmetic 'p + i' and '&*p' is p, both of which read the pointer
	// rather than address it.
	if id, named := x.X.(*ast.Ident); named {
		if sym := c.prog.Uses[id]; sym != nil {
			if sym.Constexpr {
				c.errorf(x.OpPos, "the address of '%s' is not expressible in MicroC; a constexpr object occupies no storage, since every reference to it becomes its value", id.Name)
				return invalidType
			}
			sym.Addressed = true
		}
	}
	return PointerTo(t)
}

func (c *checker) incDec(x *ast.IncDecExpr) *Type {
	t := c.expr(x.X)
	if t.Kind() == Invalid {
		return invalidType
	}
	if !c.assignTarget(x.X, t, "the operand of '"+x.Op.String()+"'") {
		return invalidType
	}
	c.noteHashOverwritten(x.X)
	switch unqual(t).Kind() {
	case Int, Double, Pointer:
		return unqual(t)
	case Invalid, Bool, Dev, Void, Array:
	}
	c.errorf(x.OpPos, "the operand of '%s' must be a long long, a double, or a pointer, found %s", x.Op, t)
	return invalidType
}

func (c *checker) binary(x *ast.BinaryExpr) *Type {
	switch x.Op {
	case ast.Add, ast.Sub:
		return c.additive(x)
	case ast.Mul, ast.Div:
		return c.arithmetic(x, c.expr(x.X), c.expr(x.Y))
	case ast.Mod, ast.Shl, ast.Shr, ast.BitAnd, ast.BitOr, ast.BitXor:
		lhs := c.expr(x.X)
		rhs := c.expr(x.Y)
		okL := c.requireInt(lhs, x.X, "the left operand of '"+x.Op.String()+"'")
		okR := c.requireInt(rhs, x.Y, "the right operand of '"+x.Op.String()+"'")
		if !okL || !okR {
			return invalidType
		}
		return IntType
	case ast.Eq, ast.Ne, ast.Lt, ast.Le, ast.Gt, ast.Ge:
		return c.comparison(x)
	case ast.LogicalAnd, ast.LogicalOr:
		lhs := c.expr(x.X)
		rhs := c.expr(x.Y)
		c.condition(x.X, lhs, "the left operand of '"+x.Op.String()+"'")
		c.condition(x.Y, rhs, "the right operand of '"+x.Op.String()+"'")
		return BoolType
	default:
		return invalidType
	}
}

// additive checks '+' and '-', the two operators a pointer takes. Pointer
// arithmetic stays within one object; subtracting two pointers into different
// objects is exactly the merge the restriction forbids.
func (c *checker) additive(x *ast.BinaryExpr) *Type {
	lhs := decay(c.expr(x.X))
	rhs := decay(c.expr(x.Y))
	if lhs.Kind() == Invalid || rhs.Kind() == Invalid {
		return invalidType
	}
	lp, rp := lhs.Kind() == Pointer, rhs.Kind() == Pointer
	switch {
	case !lp && !rp:
		return c.arithmetic(x, lhs, rhs)
	case lp && !rp:
		if !c.requireInt(rhs, x.Y, "the right operand of '"+x.Op.String()+"'") {
			return invalidType
		}
		c.checkPointerStep(x)
		return lhs
	case !lp && rp && x.Op == ast.Add:
		if !c.requireInt(lhs, x.X, "the left operand of '+'") {
			return invalidType
		}
		c.checkPointerStep(x)
		return rhs
	case lp && rp && x.Op == ast.Sub:
		c.sameObject(x.X, x.Y, x.OpPos, "the operands of '-'")
		return IntType
	default:
		c.errorf(x.OpPos, "'%s' does not apply to %s and %s", x.Op, lhs, rhs)
		return invalidType
	}
}

// arithmetic checks the operands of an operator that computes a number, and
// records the widening a long long operand needs where the other is a double.
func (c *checker) arithmetic(x *ast.BinaryExpr, lhs, rhs *Type) *Type {
	result := arithType(lhs, rhs)
	if result.Kind() == Invalid {
		// Every pair of arithmetic operands meets, so a pair that does not has
		// an operand that is not one, and naming it is more use than naming the
		// pair.
		op := x.Op.String()
		c.requireArith(lhs, x.X, "the left operand of '"+op+"'")
		c.requireArith(rhs, x.Y, "the right operand of '"+op+"'")
		return invalidType
	}
	if result.Kind() == Double {
		c.widen(x.X, lhs)
		c.widen(x.Y, rhs)
	}
	return result
}

// requireArith reports an operand that is not a number. An operand analysis
// already rejected stays quiet, so one mistake costs one diagnostic.
func (c *checker) requireArith(t *Type, x ast.Expr, what string) {
	if isArithmetic(t) || unqual(decay(t)).Kind() == Invalid {
		return
	}
	c.errorf(x.Pos(), "%s must be a long long or a double, found %s", what, t)
}

// widen records the conversion a long long operand needs to meet a double one. It is
// the one conversion an operator performs, and it loses nothing: a long long is
// exact to 2^53 and so is the double it becomes.
func (c *checker) widen(x ast.Expr, t *Type) {
	if unqual(decay(t)).Kind() == Int {
		c.prog.Conversions[x] = DoubleType
	}
}

func (c *checker) comparison(x *ast.BinaryExpr) *Type {
	lhs := unqual(decay(c.expr(x.X)))
	rhs := unqual(decay(c.expr(x.Y)))
	if lhs.Kind() == Invalid || rhs.Kind() == Invalid {
		return BoolType
	}
	ordered := x.Op != ast.Eq && x.Op != ast.Ne
	switch {
	case lhs.Kind() == Pointer && rhs.Kind() == Pointer:
		c.sameObject(x.X, x.Y, x.OpPos, "the operands of '"+x.Op.String()+"'")
	case arithType(lhs, rhs).Kind() == Double:
		c.widen(x.X, lhs)
		c.widen(x.Y, rhs)
	case lhs.Kind() == Int && rhs.Kind() == Int:
	case lhs.Kind() == Bool && rhs.Kind() == Bool && !ordered:
	case lhs.Kind() == Bool && rhs.Kind() == Bool:
		c.errorf(x.OpPos, "'%s' does not order bool values; compare them with '==' or '!='", x.Op)
	case lhs.Kind() == Pointer || rhs.Kind() == Pointer:
		c.errorf(x.OpPos, "cannot compare %s with %s", lhs, rhs)
	case lhs.Kind() == Dev || rhs.Kind() == Dev:
		c.errorf(x.OpPos, "cannot compare %s with %s; a dev names a device pin rather than a value, and no cast turns one into a number", lhs, rhs)
	default:
		c.errorf(x.OpPos, "cannot compare %s with %s; long long, bool, and double are distinct types, so convert with a cast", lhs, rhs)
	}
	return BoolType
}

func (c *checker) assignExpr(x *ast.AssignExpr) *Type {
	target := c.expr(x.Target)
	value := c.expr(x.Value)
	if !c.assignTarget(x.Target, target, "the target of '"+x.Op.String()+"'") {
		return invalidType
	}
	c.noteHashOverwritten(x.Target)
	if x.Op == ast.Assign {
		c.checkAssignable(target, value, x.Value, "an assignment")
		c.trackPointerAssign(x.Target, x.Value)
		return unqual(target)
	}
	return c.compoundAssign(x, target, value)
}

// compoundOnlyInt names the compound assignments whose operator takes ints
// alone, which are exactly the bitwise, shift, and remainder forms.
var compoundOnlyInt = map[ast.AssignOp]bool{
	ast.ModAssign: true, ast.ShlAssign: true, ast.ShrAssign: true,
	ast.AndAssign: true, ast.OrAssign: true, ast.XorAssign: true,
}

// compoundAssign checks "t op= v", which means "t = t op v" with t evaluated
// once. Only '+' and '-' apply to a pointer target. The result is the
// target's own type: a double target takes a widening long long right
// operand, but a long long target never takes a double one implicitly.
func (c *checker) compoundAssign(x *ast.AssignExpr, target, value *Type) *Type {
	op := x.Op.String()
	if unqual(decay(target)).Kind() == Pointer {
		if x.Op != ast.AddAssign && x.Op != ast.SubAssign {
			c.errorf(x.OpPos, "'%s' does not apply to a pointer", op)
			return invalidType
		}
		if !c.requireInt(value, x.Value, "the right operand of '"+op+"'") {
			return invalidType
		}
		return unqual(target)
	}
	if compoundOnlyInt[x.Op] || unqual(target).Kind() != Double {
		okT := c.requireInt(target, x.Target, "the target of '"+op+"'")
		okV := c.requireInt(value, x.Value, "the right operand of '"+op+"'")
		if !okT || !okV {
			return invalidType
		}
		return IntType
	}
	if !isArithmetic(value) {
		if value.Kind() != Invalid {
			c.errorf(x.Value.Pos(), "the right operand of '%s' must be a long long or a double, found %s", op, value)
		}
		return invalidType
	}
	c.widen(x.Value, value)
	return DoubleType
}

func (c *checker) cond(x *ast.CondExpr) *Type {
	c.condition(x.Cond, c.expr(x.Cond), "the condition of '?:'")
	then := decay(c.expr(x.Then))
	els := decay(c.expr(x.Else))
	if then.Kind() == Invalid || els.Kind() == Invalid {
		return invalidType
	}
	// A pointer arm is held to the same equality the others are, qualifiers
	// included: the result takes one arm's type, and there is no destination
	// type to make an added const safe the way an assignment's is.
	if !unqual(then).Equal(unqual(els)) {
		c.errorf(x.Question, "the arms of '?:' must have the same type, found %s and %s", then, els)
		return invalidType
	}
	if then.Kind() == Pointer {
		c.sameObject(x.Then, x.Else, x.Question, "the arms of '?:'")
	}
	return unqual(then)
}

func (c *checker) index(x *ast.IndexExpr) *Type {
	base := decay(c.expr(x.X))
	idx := c.expr(x.Index)
	c.requireInt(idx, x.Index, "an array index")
	if base.Kind() == Invalid {
		return invalidType
	}
	if base.Kind() != Pointer {
		c.errorf(x.Lbrack, "cannot index %s; only an array or a pointer may be indexed", base)
		return invalidType
	}
	c.checkIndexBound(x)
	return base.Elem()
}

func (c *checker) call(x *ast.CallExpr) *Type {
	id, ok := x.Fun.(*ast.Ident)
	if !ok {
		c.expr(x.Fun)
		c.errorf(x.Lparen, "only a named function may be called; MicroC has no function pointers")
		c.checkArgs(x)
		return invalidType
	}
	if in := intrinsics[id.Name]; in != nil {
		return c.intrinsicCall(x, in)
	}
	sym := c.scope.lookup(id.Name)
	if sym == nil {
		if isReservedName(id.Name) {
			c.errorf(id.NamePos, "'%s' is not an intrinsic, and names beginning '%s' are reserved for them", id.Name, reservedPrefix)
		} else {
			c.errorf(id.NamePos, "undeclared name '%s'", id.Name)
		}
		c.checkArgs(x)
		return invalidType
	}
	c.prog.Uses[id] = sym
	if sym.Kind != FuncName {
		c.errorf(id.NamePos, "'%s' is a %s, not a function", id.Name, sym.Kind)
		c.checkArgs(x)
		return invalidType
	}

	fn := sym.Func
	if len(x.Args) != len(fn.Params) {
		c.errorf(x.Lparen, "'%s' expects %s, found %d", fn.Name, source.Plural(len(fn.Params), "argument"), len(x.Args))
	}
	for i, arg := range x.Args {
		// A dev argument names a pin rather than computing a value, so it is
		// read the way an intrinsic's device operand is.
		if i < len(fn.Params) && unqual(fn.Params[i].Type).Kind() == Dev {
			c.resolveDevice(arg, "an argument to '"+fn.Name+"'")
			continue
		}
		t := c.expr(arg)
		if i < len(fn.Params) {
			c.checkAssignable(fn.Params[i].Type, t, arg, "an argument to '"+fn.Name+"'")
		}
	}
	fn.called = true
	c.prog.Calls[x] = fn
	c.addEdge(fn)
	return fn.Result
}

// checkArgs types the arguments of a call that has no callee to check them
// against, so a mistake inside one is still reported.
func (c *checker) checkArgs(x *ast.CallExpr) {
	for _, arg := range x.Args {
		c.expr(arg)
	}
}

func (c *checker) addEdge(callee *Func) {
	if c.fn == nil || c.fn.callees[callee] {
		return
	}
	c.fn.callees[callee] = true
	c.fn.Callees = append(c.fn.Callees, callee)
}

func (c *checker) cast(x *ast.CastExpr) *Type {
	target := c.resolveType(x.Type, false)
	src := decay(c.expr(x.X))
	switch target.Kind() {
	case Int, Bool, Double:
	case Invalid, Dev, Void, Pointer, Array:
		// A cast to void, to dev, or to a pointer is a parse error the parser
		// named.
		return invalidType
	}
	switch unqual(src).Kind() {
	case Invalid:
		return target
	case Int, Bool, Double:
		c.recordConversion(target, src, x.X)
		return target
	case Dev, Void, Pointer, Array:
	}
	c.errorf(x.Lparen, "cannot convert %s to %s; a cast targets long long, bool, or double", src, target)
	return target
}

// checkAssignable reports a value of type src used where dst is wanted,
// records the conversion where one happens, and says whether the value may
// be used there. It is [assignableTo] with a diagnostic behind it: an
// operand analysis already rejected answers false and says nothing more.
func (c *checker) checkAssignable(dst, src *Type, x ast.Expr, what string) bool {
	if dst.Kind() == Invalid || src.Kind() == Invalid {
		return false
	}
	if !assignableTo(dst, src) {
		c.errorf(x.Pos(), "cannot use %s as %s in %s", src, dst, what)
		return false
	}
	c.recordConversion(dst, src, x)
	return true
}

// recordConversion notes a conversion between the scalar types. A conversion to
// bool normalizes the value to 0 or 1, one to double widens, and one to long long
// truncates toward zero; bool to long long alone does nothing, since a bool already
// holds 0 or 1.
func (c *checker) recordConversion(dst, src *Type, x ast.Expr) {
	d, s := unqual(dst), unqual(src)
	switch d.Kind() {
	case Int, Bool, Double:
		if d.Kind() != s.Kind() {
			c.prog.Conversions[x] = d
		}
	case Invalid, Dev, Void, Pointer, Array:
	}
}

// condition checks an expression used for its truth: the controlling clause
// of if, while, do, for, and '?:', and the operands of '!', '&&', and '||'.
// A long long converts to bool there, but a double does not: every chip
// value is fractional, so a bare test against zero is rarely what was meant.
func (c *checker) condition(x ast.Expr, t *Type, what string) {
	switch unqual(decay(t)).Kind() {
	case Invalid, Bool:
	case Int:
		c.prog.Conversions[x] = BoolType
	case Double:
		c.errorf(x.Pos(), "%s must be a long long or a bool, found %s; compare the value rather than testing it against zero", what, t)
	case Dev, Void, Pointer, Array:
		c.errorf(x.Pos(), "%s must be a long long or a bool, found %s", what, t)
	}
}

func (c *checker) requireInt(t *Type, x ast.Expr, what string) bool {
	switch unqual(decay(t)).Kind() {
	case Invalid:
		return false
	case Int:
		return true
	case Bool, Double, Dev, Void, Pointer, Array:
	}
	c.errorf(x.Pos(), "%s must be a long long, found %s", what, t)
	return false
}

// assignTarget reports whether x may be written through. An array name, a
// const object, and anything that does not name an object are all rejected.
func (c *checker) assignTarget(x ast.Expr, t *Type, what string) bool {
	if !isLvalue(x) {
		c.errorf(x.Pos(), "%s must name an object", what)
		return false
	}
	if t.Kind() == Array {
		c.errorf(x.Pos(), "an array may not be assigned")
		return false
	}
	if unqual(t).Kind() == Dev {
		c.errorf(x.Pos(), "a dev names a device pin the chip resolves when the line is assembled, so it may not be assigned")
		return false
	}
	if t.IsConst() {
		if id, ok := x.(*ast.Ident); ok {
			c.errorf(x.Pos(), "'%s' is const and may not be assigned", id.Name)
		} else {
			c.errorf(x.Pos(), "the object is const and may not be assigned")
		}
		return false
	}
	return true
}

// isLvalue reports whether x designates an object rather than a value. Only a
// name, a subscript, and a dereference do.
func isLvalue(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.Ident:
		return true
	case *ast.IndexExpr:
		return true
	case *ast.UnaryExpr:
		return x.Op == ast.Deref
	default:
		return false
	}
}
