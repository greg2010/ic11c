package sema

import (
	"strconv"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/source"
)

// SymbolKind distinguishes what a declared name denotes. It also carries the
// storage class, since MicroC has exactly one per kind: globals and arrays live
// in the data region, locals and parameters in a frame or a register.
type SymbolKind uint8

const (
	// GlobalVar is a variable declared at file scope. It keeps its value
	// across ticks.
	GlobalVar SymbolKind = iota
	// LocalVar is a variable declared in a block.
	LocalVar
	// ParamVar is a function parameter. Parameters share the function body's
	// scope, so a local redeclaring one conflicts.
	ParamVar
	// FuncName is a function.
	FuncName
)

var symbolKindNames = [...]string{
	GlobalVar: "global",
	LocalVar:  "local",
	ParamVar:  "parameter",
	FuncName:  "function",
}

func (k SymbolKind) String() string {
	if int(k) < len(symbolKindNames) && symbolKindNames[k] != "" {
		return symbolKindNames[k]
	}
	return "SymbolKind(" + strconv.Itoa(int(k)) + ")"
}

// Symbol is one declared name. Every [ast.Ident] that resolved appears in
// [Program.Uses] mapped to the Symbol it named.
type Symbol struct {
	Name string
	Kind SymbolKind
	// Type is the declared type, with an array parameter already decayed to a
	// pointer. It is Invalid for a function, whose signature is on Func.
	Type *Type
	// Pos is where the name was written.
	Pos source.Position
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
	// constant expression, and nil otherwise. It is what makes "case kIdle:"
	// work.
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
}

// InDataRegion reports whether the object occupies one of the 512 memory slots
// rather than a register.
//
// It decides two things that have to agree: which locals definite assignment
// covers, and which storage IR generation has to write a zero into. The entry
// prologue zeroes every slot before anything else runs, so an object here reads
// as zero before its first write; a register has nothing that zeroes it.
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

// scope is one name lookup level: file scope, a function body, or a block.
type scope struct {
	parent *scope
	names  map[string]*Symbol
}

func newScope(parent *scope) *scope {
	return &scope{parent: parent, names: make(map[string]*Symbol)}
}

// lookup finds name in this scope or any enclosing one, so an inner declaration
// shadows an outer one.
func (s *scope) lookup(name string) *Symbol {
	for cur := s; cur != nil; cur = cur.parent {
		if sym, ok := cur.names[name]; ok {
			return sym
		}
	}
	return nil
}

// lookupLocal finds name declared in this scope only, which is what a
// redeclaration conflict is about.
func (s *scope) lookupLocal(name string) *Symbol { return s.names[name] }

func (s *scope) insert(sym *Symbol) { s.names[sym.Name] = sym }
