package sema

import (
	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/source"
)

func (c *checker) decl(d ast.Decl) {
	switch d := d.(type) {
	case *ast.VarDecl:
		c.prog.Globals = append(c.prog.Globals, c.varDecl(d, GlobalVar))
	case *ast.FuncDecl:
		c.funcDecl(d)
	default:
		// A BadDecl is source the parser already reported.
	}
}

// varDecl declares one variable and checks its initializer. The name enters
// scope before the initializer is checked, since a name is in scope from its
// declarator onward.
func (c *checker) varDecl(d *ast.VarDecl, kind SymbolKind) *Symbol {
	c.checkReserved(d.NamePos, d.Name)
	// constexpr carries the const: an object whose value the program names in a
	// constant expression is one nothing may assign to.
	typ := c.resolveType(d.Type, d.Const || d.Constexpr)
	if typ.Kind() == Void {
		c.errorf(d.DeclPos, "a variable may not have type void; void is a function result type only")
		typ = invalidType
	}
	sym := &Symbol{Name: d.Name, Kind: kind, Type: typ, Pos: d.NamePos, Decl: d, Constexpr: d.Constexpr}
	c.declare(sym)

	if unqual(typ).Kind() == Dev {
		c.deviceDecl(d, sym)
		return sym
	}
	c.rejectPrefabAttr(d, typ)
	if d.Init == nil {
		if isConstObject(typ) {
			specifier := "const"
			if d.Constexpr {
				specifier = "constexpr"
			}
			c.errorf(d.NamePos, "the %s object '%s' requires an initializer", specifier, d.Name)
		}
		return sym
	}
	c.initializer(sym, d.Init, kind == GlobalVar)
	return sym
}

// deviceDecl checks a dev declaration, which names a pin rather than
// declaring storage. It is always const: the chip resolves a device position
// when the line is assembled, so what the name stands for is fixed once and
// never reassigned — writing const or constexpr is required, not assumed.
func (c *checker) deviceDecl(d *ast.VarDecl, sym *Symbol) {
	if !d.Const && !d.Constexpr {
		c.errorf(d.DeclPos, "'%s' has type dev, which names a device pin the chip resolves at assembly time; declare it const", d.Name)
	}
	if d.Init == nil {
		c.errorf(d.NamePos, "'%s' has type dev and requires an initializer naming a device", d.Name)
		return
	}
	device, ok := c.deviceExpr(d.Init, "the initializer of '"+d.Name+"'")
	if !ok {
		return
	}
	sym.Device = &device
	c.declarePrefab(d.Prefab, device)
}

// initializer checks a variable's initializer against its type. A global's
// must be a constant expression, since chip memory holds a number rather
// than a computation; so must a constexpr object's. A local's is otherwise
// any expression, though a brace initializer's elements are always constant.
func (c *checker) initializer(sym *Symbol, init ast.Expr, global bool) {
	if list, ok := init.(*ast.InitListExpr); ok {
		c.initList(sym, list)
		return
	}
	t := c.expr(init)
	if sym.Type.Kind() == Array {
		c.errorf(init.Pos(), "an array requires a brace initializer")
		return
	}
	assignable := c.checkAssignable(sym.Type, t, init, "the initializer of '"+sym.Name+"'")
	c.recordPointerInit(sym, init)
	c.recordHashedName(sym, init)

	if !global && !sym.Constexpr {
		return
	}
	what := "a global initializer"
	if sym.Constexpr {
		what = "the initializer of a constexpr object"
	}
	// A type the object does not admit is already reported, and the integer
	// mode would name the same double a second time.
	mode := arithmeticConst
	if assignable {
		mode = initConstMode(sym, sym.Type)
	}
	if v, ok := c.requireConst(init, mode, what); ok {
		c.recordConstexprObject(sym, v)
	}
}

// recordConstexprObject remembers a constexpr scalar's value, which is what
// lets it appear in a constant expression — MicroC has neither enum nor a
// preprocessor, so this is the only way to name a constant. A plain const
// object is deliberately excluded: C reads its value at run time.
func (c *checker) recordConstexprObject(sym *Symbol, v Value) {
	if !sym.Constexpr || !isScalar(sym.Type) {
		return
	}
	sym.Value = &v
}

// initList checks a brace initializer against the object it initializes.
// Every type but an array takes the scalar rule: a dev is resolved at its
// declaration and never reaches here, and an array is the one shape that
// takes more than one value.
func (c *checker) initList(sym *Symbol, list *ast.InitListExpr) {
	//exhaustive:ignore
	switch sym.Type.Kind() {
	case Array:
		c.initArray(sym, list)
	case Invalid:
		// The declaration is already reported. The elements are still typed, so
		// a mistake written inside one is not hidden by it.
		for _, e := range list.Elems {
			c.expr(e)
		}
	default:
		c.initScalar(sym, list)
	}
}

// initArray checks the brace initializer of an array, which supplies elements
// from index zero. Supplying fewer than the bound is allowed: an array lives in
// the data region, which the entry prologue has already zeroed.
func (c *checker) initArray(sym *Symbol, list *ast.InitListExpr) {
	if n := int64(len(list.Elems)); n > sym.Type.Len() {
		c.errorf(list.Elems[sym.Type.Len()].Pos(),
			"the brace initializer supplies %d elements for an array of %d", n, sym.Type.Len())
	}
	for _, e := range list.Elems {
		c.initElem(sym, sym.Type.Elem(), e)
	}
}

// initScalar checks the brace initializer of a scalar, which supplies exactly
// one value. An empty one is refused rather than treated as no initializer:
// definite assignment reads a declaration's initializer as a write, and this
// is exactly the uninitialized-register case that analysis exists to reject.
func (c *checker) initScalar(sym *Symbol, list *ast.InitListExpr) {
	if len(list.Elems) != 1 {
		c.errorf(list.Lbrace, "a brace initializer for '%s' must supply exactly one value, found %d",
			sym.Name, len(list.Elems))
		for _, e := range list.Elems {
			c.initElem(sym, sym.Type, e)
		}
		return
	}
	if v, ok := c.initElem(sym, sym.Type, list.Elems[0]); ok {
		c.recordConstexprObject(sym, v)
	}
}

// initElem checks one element of a brace initializer and folds it, since every
// element is a constant expression whether the object is a global or a local.
// An element whose type the object does not admit is not folded as well, so one
// mistake costs one diagnostic.
func (c *checker) initElem(sym *Symbol, want *Type, e ast.Expr) (Value, bool) {
	t := c.expr(e)
	if !c.checkAssignable(want, t, e, "an element of the brace initializer") {
		return Value{}, false
	}
	return c.requireConst(e, initConstMode(sym, want), "an element of a brace initializer")
}

// funcDecl declares a function and, when the declaration is a definition,
// checks its body. The name is declared before the body is checked, so a
// function can call itself.
func (c *checker) funcDecl(d *ast.FuncDecl) {
	c.checkReserved(d.NamePos, d.Name)
	result := c.resolveType(d.Result, false)
	// Default is the rule: a result reaches the caller in a register, which
	// every type but these two has a form for.
	//exhaustive:ignore
	switch result.Kind() {
	case Array:
		c.errorf(d.DeclPos, "a function may not return an array")
		result = invalidType
	case Dev:
		c.errorf(d.DeclPos, "a function may not return a dev; a device is named where it is used, and a result reaches the caller in a register the chip does not read as one")
		result = invalidType
	default:
	}
	params := c.params(d)

	fn := c.funcs[d.Name]
	if fn == nil {
		fn = &Func{Name: d.Name, Result: result, Params: params, Pos: d.NamePos, callees: make(map[*Func]bool)}
		c.funcs[d.Name] = fn
		c.prog.Funcs = append(c.prog.Funcs, fn)
		c.declare(&Symbol{Name: d.Name, Kind: FuncName, Type: invalidType, Pos: d.NamePos, Decl: d, Func: fn})
	} else {
		c.checkAgrees(fn, d, result, params)
	}

	if d.Body == nil {
		return
	}
	if fn.Decl != nil {
		c.errorf(d.NamePos, "'%s' is already defined at %s", d.Name, fn.Decl.NamePos)
		return
	}
	fn.Decl = d
	// The definition's signature is the one the body is checked against,
	// whatever an earlier prototype said, so a disagreement between them costs
	// one diagnostic rather than one per return statement.
	fn.Result = result
	fn.Params = params
	c.body(fn, d)
}

// checkAgrees reports a redeclaration that does not match the one in force. A
// prototype exists so a call may precede the definition, which only works if
// the two say the same thing.
func (c *checker) checkAgrees(fn *Func, d *ast.FuncDecl, result *Type, params []*Symbol) {
	if !fn.Result.Equal(result) {
		c.errorf(d.DeclPos, "'%s' was declared at %s returning %s, not %s", d.Name, fn.Pos, fn.Result, result)
		return
	}
	if len(fn.Params) != len(params) {
		c.errorf(d.NamePos, "'%s' was declared at %s with %s, not %d",
			d.Name, fn.Pos, source.Plural(len(fn.Params), "parameter"), len(params))
		return
	}
	for i, prev := range fn.Params {
		if !prev.Type.Equal(params[i].Type) {
			c.errorf(params[i].Pos, "parameter %d of '%s' was declared at %s as %s, not %s",
				i+1, d.Name, fn.Pos, prev.Type, params[i].Type)
		}
	}
}

// params resolves a parameter list. A parameter may go unnamed only in a
// prototype, where there is no body to name it from.
func (c *checker) params(d *ast.FuncDecl) []*Symbol {
	params := make([]*Symbol, 0, len(d.Params))
	seen := make(map[string]source.Position, len(d.Params))
	for _, p := range d.Params {
		typ := c.resolveParamType(p.Type, p.Const)
		if typ.Kind() == Void {
			c.errorf(p.ParamPos, "a parameter may not have type void; write '()' or '(void)' for no parameters")
			typ = invalidType
		}
		pos := p.NamePos
		if !pos.IsValid() {
			pos = p.ParamPos
		}
		if p.Name == "" && d.Body != nil {
			c.errorf(pos, "a parameter of '%s' must be named in a definition", d.Name)
		}
		if p.Name != "" {
			c.checkReserved(pos, p.Name)
			if prev, dup := seen[p.Name]; dup {
				c.errorf(pos, "'%s' is already declared at %s", p.Name, prev)
			}
			seen[p.Name] = pos
		}
		sym := &Symbol{Name: p.Name, Kind: ParamVar, Type: typ, Pos: pos, Decl: p}
		// A pointer parameter designates whatever the caller passed, which is
		// one object; assigning it a different one is what the restriction
		// forbids.
		if typ.Kind() == Pointer {
			sym.obj, sym.objSet = object{sym: sym}, true
		}
		params = append(params, sym)
	}
	return params
}

// body checks a function definition. Parameters share the body's scope, so a
// local redeclaring one conflicts.
func (c *checker) body(fn *Func, d *ast.FuncDecl) {
	prevFn, prevLoops, prevSwitches := c.fn, c.loops, c.switches
	c.fn, c.loops, c.switches = fn, 0, 0
	c.scope.push()
	for _, p := range fn.Params {
		if p.Name != "" {
			c.declare(p)
		}
	}
	for _, s := range d.Body.Stmts {
		c.stmt(s)
	}
	c.scope.pop()
	c.fn, c.loops, c.switches = prevFn, prevLoops, prevSwitches

	if fn.Result.Kind() != Void && fn.Result.Kind() != Invalid && !c.terminatesList(d.Body.Stmts) {
		c.errorf(d.Body.Rbrace, "control reaches the end of '%s', which must return %s", fn.Name, fn.Result)
	}
}

// resolveType turns a syntactic type into a semantic one. konst is the const
// the declaration wrote, which precedes the type and so qualifies the base:
// "const long long *p" is a pointer to const long long, and p itself stays assignable.
func (c *checker) resolveType(t ast.Type, konst bool) *Type {
	switch t := t.(type) {
	case *ast.ScalarType:
		switch t.Kind {
		case ast.Int:
			return qualified(IntType, konst)
		case ast.Bool:
			return qualified(BoolType, konst)
		case ast.Double:
			return qualified(DoubleType, konst)
		case ast.Dev:
			return qualified(DevType, konst)
		case ast.Void:
			return VoidType
		default:
			return invalidType
		}
	case *ast.PointerType:
		elem := c.resolveType(t.Elem, konst)
		if elem.Kind() == Void {
			c.errorf(t.Star, "a pointer to void is not supported in MicroC; void is a function result type only")
			return invalidType
		}
		if elem.Kind() == Dev {
			c.errorf(t.Star, "a pointer to dev is not supported in MicroC; a device is resolved when the line is assembled and occupies no memory slot")
			return invalidType
		}
		if elem.Kind() == Invalid {
			return invalidType
		}
		return PointerTo(elem)
	case *ast.ArrayType:
		return c.arrayType(t, konst)
	default:
		return invalidType
	}
}

// arrayElem resolves an array's element type, refusing the two types no
// array may hold and reporting false where it did. Neither rule depends on
// where the array was written: a parameter's array decays to a pointer, but
// a pointer to either refused type is refused too.
func (c *checker) arrayElem(t *ast.ArrayType, konst bool) (*Type, bool) {
	elem := c.resolveType(t.Elem, konst)
	if elem.Kind() == Void {
		c.errorf(t.Lbrack, "an array element may not have type void")
		return invalidType, false
	}
	if elem.Kind() == Dev {
		c.errorf(t.Lbrack, "an array element may not have type dev; a device occupies no memory slot")
		return invalidType, false
	}
	return elem, true
}

func (c *checker) arrayType(t *ast.ArrayType, konst bool) *Type {
	elem, ok := c.arrayElem(t, konst)
	if !ok {
		return invalidType
	}
	if t.Size == nil {
		// An omitted bound outside a parameter list is a parse error the
		// parser already named.
		return invalidType
	}
	n, bounded := c.arrayBound(t.Size)
	if !bounded || elem.Kind() == Invalid {
		return invalidType
	}
	return ArrayOf(elem, n)
}

func (c *checker) arrayBound(size ast.Expr) (int64, bool) {
	t := c.expr(size)
	if unqual(t).Kind() != Int && t.Kind() != Invalid {
		c.errorf(size.Pos(), "an array bound must be a long long, found %s", t)
		return 0, false
	}
	v, ok := c.requireConst(size, integerConst, "an array bound")
	if !ok {
		return 0, false
	}
	if v.Int < 1 {
		c.errorf(size.Pos(), "an array bound must be positive, found %d", v.Int)
		return 0, false
	}
	// Refused here rather than left to instruction selection: a bound this
	// rejects is one no program can lay out regardless, and the type is
	// walked element by element downstream, so a bound in the billions would
	// stop the compiler answering instead of stopping the program compiling.
	if v.Int > ic10.NumMemorySlots {
		c.errorf(size.Pos(), "an array bound must be at most %d, the whole data region, found %d; "+
			"what a program can afford is less, since the same slots hold every other object and the call stack",
			ic10.NumMemorySlots, v.Int)
		return 0, false
	}
	return v.Int, true
}

// resolveParamType resolves a parameter's type, where an array decays to a
// pointer whether or not it wrote a bound.
func (c *checker) resolveParamType(t ast.Type, konst bool) *Type {
	at, isArray := t.(*ast.ArrayType)
	if !isArray {
		return c.resolveType(t, konst)
	}
	elem, ok := c.arrayElem(at, konst)
	if !ok {
		return invalidType
	}
	if at.Size != nil {
		c.arrayBound(at.Size)
	}
	if elem.Kind() == Invalid {
		return invalidType
	}
	return PointerTo(elem)
}

// checkDefined reports a function a call reached but no definition supplies.
// There is no linker: every function called lives in this file or nowhere. The
// entry point is exempt because checkEntryPoint requires its body whether or
// not anything calls it.
func (c *checker) checkDefined() {
	for _, fn := range c.prog.Funcs {
		if fn == c.prog.Main {
			continue
		}
		if fn.Decl == nil && fn.called {
			c.errorf(fn.Pos, "'%s' is called but never defined; MicroC has no linker", fn.Name)
		}
	}
}

// checkEntryPoint reports the program's entry point, which every other function
// is reachable from. A program defines main: a prototype alone leaves execution
// nowhere to begin, and no call has to reach main for that to be a mistake.
func (c *checker) checkEntryPoint() {
	fn := c.funcs[EntryFunction]
	if fn == nil {
		c.errorf(c.prog.File.Start, "the program does not define 'void main(void)', where execution begins")
		return
	}
	c.prog.Main = fn
	if fn.Result.Kind() != Void || len(fn.Params) != 0 {
		c.errorf(fn.Pos, "'main' must be declared 'void main(void)'")
	}
	if fn.Decl == nil {
		c.errorf(fn.Pos, "'main' is declared but never defined; execution begins in its body")
	}
}
