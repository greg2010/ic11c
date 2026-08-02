package sema

import (
	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/source"
)

// A pointer is an index into the 512-slot space and must trace at compile time
// to exactly one object, because the backend has no way to load through a base
// it cannot name. This file implements the half of that rule the source can
// show: an expression that visibly merges two objects.
//
// The authoritative check runs after optimization, which can manufacture merges
// the source never wrote. Nothing here should be read as the last line of
// defense: where provenance cannot be determined, analysis stays quiet and
// leaves the verdict to the verifier.

// objectOf reports which object a pointer expression designates. The second
// result is false when the expression traces to no single object analysis can
// name, which is not a rejection.
func (c *checker) objectOf(x ast.Expr) (object, bool) {
	switch x := x.(type) {
	case *ast.Ident:
		sym := c.prog.Uses[x]
		if sym == nil {
			return object{}, false
		}
		if sym.objSet {
			return sym.obj, true
		}
		return object{sym: sym}, true
	case *ast.UnaryExpr:
		if x.Op == ast.AddrOf {
			return c.storageOf(x.X)
		}
		return object{}, false
	case *ast.BinaryExpr:
		// Arithmetic within one array keeps the object it started in.
		if x.Op != ast.Add && x.Op != ast.Sub {
			return object{}, false
		}
		if c.prog.Types[x.X].Kind() == Pointer || c.prog.Types[x.X].Kind() == Array {
			return c.objectOf(x.X)
		}
		return c.objectOf(x.Y)
	case *ast.AssignExpr:
		return c.objectOf(x.Target)
	case *ast.CallExpr:
		// A returned pointer designates one object per call site. Which one it
		// is takes interprocedural analysis, which the post-optimization
		// verifier has and this pass does not.
		return object{call: x}, true
	default:
		return object{}, false
	}
}

// storageOf reports which object an lvalue occupies, which is what taking its
// address designates.
func (c *checker) storageOf(x ast.Expr) (object, bool) {
	switch x := x.(type) {
	case *ast.Ident:
		sym := c.prog.Uses[x]
		if sym == nil {
			return object{}, false
		}
		return object{sym: sym}, true
	case *ast.IndexExpr:
		return c.objectOf(x.X)
	case *ast.UnaryExpr:
		if x.Op == ast.Deref {
			return c.objectOf(x.X)
		}
		return object{}, false
	default:
		return object{}, false
	}
}

// sameObject rejects an expression that merges two objects into one pointer.
func (c *checker) sameObject(a, b ast.Expr, pos source.Position, what string) {
	if c.prog.Types[a].Kind() == Invalid || c.prog.Types[b].Kind() == Invalid {
		return
	}
	oa, okA := c.objectOf(a)
	ob, okB := c.objectOf(b)
	if !okA || !okB || oa == ob {
		return
	}
	c.errorf(pos, "%s designate different objects; a pointer expression must trace to exactly one object", what)
}

// recordPointerInit remembers which object a pointer variable's initializer
// designated, so a later assignment naming a different one can be rejected.
func (c *checker) recordPointerInit(sym *Symbol, init ast.Expr) {
	if sym.Type.Kind() != Pointer || sym.objSet {
		return
	}
	if obj, ok := c.objectOf(init); ok {
		sym.obj, sym.objSet = obj, true
	}
}

// trackPointerAssign rejects assigning a pointer variable an object other than
// the one it already designates, which is the merge a later phase could not
// resolve.
func (c *checker) trackPointerAssign(target, value ast.Expr) {
	id, ok := target.(*ast.Ident)
	if !ok {
		return
	}
	sym := c.prog.Uses[id]
	if sym == nil || sym.Type.Kind() != Pointer {
		return
	}
	obj, ok := c.objectOf(value)
	if !ok {
		return
	}
	if !sym.objSet {
		sym.obj, sym.objSet = obj, true
		return
	}
	if sym.obj != obj {
		c.errorf(id.NamePos, "'%s' is assigned a pointer to a different object; a pointer must trace to exactly one object", id.Name)
	}
}
