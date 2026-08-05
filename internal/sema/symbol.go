package sema

import (
	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/source"
)

// SymbolKind distinguishes what a declared name denotes.
type SymbolKind uint8

const (
	// GlobalVar is a variable declared at file scope. It keeps its value
	// across ticks.
	GlobalVar SymbolKind = iota
	LocalVar
	ParamVar
	FuncName
)

var symbolKindNames = [...]string{
	GlobalVar: "global",
	LocalVar:  "local",
	ParamVar:  "parameter",
	FuncName:  "function",
}

func (k SymbolKind) String() string { return source.EnumName(symbolKindNames[:], int(k), "SymbolKind") }

// Symbol is one declared name. Every [ast.Ident] that resolved appears in
// [Program.Uses] mapped to the Symbol it named.
type Symbol struct {
	Name string
	Kind SymbolKind
	// Type is the declared type, with an array parameter already decayed to a
	// pointer. It is Invalid for a function, whose signature is on Func.
	Type *Type
	Pos  source.Position
	// Decl is the [ast.VarDecl], [ast.Param], or [ast.FuncDecl] that declared
	// the name.
	Decl ast.Node
	// Func is the function a FuncName symbol denotes, and nil otherwise.
	Func *Func
	// Constexpr reports that the declaration wrote constexpr, which is what
	// admits the object into a constant expression. It implies the type is
	// const-qualified.
	Constexpr bool
	// Value is the folded value of a constexpr scalar initialized with a
	// constant expression, and nil otherwise.
	Value *Value
	// Device is the pin a dev object names, and nil for every other symbol and
	// for a dev parameter, whose pin the call site supplies.
	Device *Device
	// Addressed reports that the source took the address of the object, which
	// is what moves a local out of a register and into the data region. It is
	// complete only after the whole file has been checked, since the address of
	// a local can be taken above the line that declares it.
	Addressed bool

	// obj records which object a pointer variable designates, so that a second
	// assignment naming a different object can be rejected.
	obj    object
	objSet bool

	// hashName and hashKnown record the string an object's initializer hashed,
	// if any — what makes a batch operand naming it checkable. hashVaries
	// records a later write, after which the value at a use is undecided; all
	// three are final only once the file is fully checked.
	hashName   string
	hashKnown  bool
	hashVaries bool
}

// InDataRegion reports whether the object occupies one of the 512 memory
// slots rather than a register. The entry prologue zeroes every slot before
// anything else runs, so an object here reads as zero before its first
// write; a register has nothing that zeroes it.
func (s *Symbol) InDataRegion() bool {
	switch s.Kind {
	case GlobalVar:
		return true
	case LocalVar, ParamVar:
		return s.Addressed || s.Type.Kind() == Array
	case FuncName:
		return false
	}
	return false
}

// Func is one function, declared by a prototype, a definition, or both.
type Func struct {
	Name   string
	Result *Type
	Params []*Symbol
	// Pos is where the name was first written, whether by a prototype or by
	// the definition.
	Pos source.Position
	// Decl is the definition. It is nil for a function only a prototype
	// declared, which is an error at any call site.
	Decl *ast.FuncDecl
	// Callees holds every function this one calls, without repeats, in the
	// order the calls were first seen. Intrinsics are not calls and do not
	// appear.
	Callees []*Func
	// Recursive reports whether the function can reach itself through Callees.
	// A recursive function needs a real frame; the rest can be inlined.
	Recursive bool

	callees map[*Func]bool
	called  bool
}

// object identifies the storage a pointer expression designates. Exactly one
// field is set: a named variable, parameter, or array, or the call whose result
// no source-level analysis can trace further.
type object struct {
	sym  *Symbol
	call *ast.CallExpr
}

// scopes holds the name lookup levels one analysis has open, outermost
// first: the universe, file scope, a function body, and a block for each one
// nested inside it. A name resolves through one map lookup rather than a
// walk outward through levels, keeping lookup cost independent of depth.
type scopes struct {
	// bound holds every symbol a name is currently bound to, innermost last. A
	// name nothing declared is absent rather than present and empty.
	bound map[string][]binding
	// opened holds the names each open level bound, in the order it bound them,
	// so closing a level unbinds exactly what it added.
	opened [][]string
}

// binding is one symbol a name is bound to, and the level that bound it. The
// level is what tells a redeclaration in the innermost scope from a
// declaration that shadows an outer one.
type binding struct {
	sym   *Symbol
	depth int
}

// newScopes binds predeclared in the universe and opens file scope inside
// it. Neither is ever closed, and they are two levels rather than one: a
// declaration taking a predeclared name shadows it rather than colliding,
// which leaves the reserved-name rule the only thing that refuses one.
func newScopes(predeclared []*Symbol) *scopes {
	s := &scopes{bound: make(map[string][]binding), opened: make([][]string, 1)}
	for _, sym := range predeclared {
		s.insert(sym)
	}
	s.push()
	return s
}

// push opens a level and pop closes it. Every caller pairs them.
func (s *scopes) push() { s.opened = append(s.opened, nil) }

func (s *scopes) pop() {
	last := len(s.opened) - 1
	for _, name := range s.opened[last] {
		// The level being closed is the innermost, so its bindings are the last
		// ones for their names whatever order they were added in.
		rest := s.bound[name][:len(s.bound[name])-1]
		if len(rest) == 0 {
			delete(s.bound, name)
			continue
		}
		s.bound[name] = rest
	}
	s.opened = s.opened[:last]
}

// lookup finds name in the innermost level that bound it, so an inner
// declaration shadows an outer one.
func (s *scopes) lookup(name string) *Symbol {
	if b, declared := s.innermost(name); declared {
		return b.sym
	}
	return nil
}

// lookupLocal finds name bound in the innermost open level alone, which is what
// a redeclaration conflict is about.
func (s *scopes) lookupLocal(name string) *Symbol {
	if b, declared := s.innermost(name); declared && b.depth == len(s.opened)-1 {
		return b.sym
	}
	return nil
}

func (s *scopes) innermost(name string) (binding, bool) {
	bound := s.bound[name]
	if len(bound) == 0 {
		return binding{}, false
	}
	return bound[len(bound)-1], true
}

func (s *scopes) insert(sym *Symbol) {
	depth := len(s.opened) - 1
	s.bound[sym.Name] = append(s.bound[sym.Name], binding{sym: sym, depth: depth})
	s.opened[depth] = append(s.opened[depth], sym.Name)
}

// declare adds sym to the current scope, reporting a name already declared
// there. The first declaration keeps the name, so later uses resolve to
// something rather than cascading into "undeclared".
func (c *checker) declare(sym *Symbol) {
	if prev := c.scope.lookupLocal(sym.Name); prev != nil {
		c.errorf(sym.Pos, "'%s' is already declared at %s", sym.Name, prev.Pos)
		return
	}
	c.scope.insert(sym)
}
