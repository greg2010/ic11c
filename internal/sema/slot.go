package sema

import (
	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/source"
)

// slot is an address in one declared object: the declaration, how many slots
// it occupies, and the constant offset into it. The data region and the call
// frames share one 512-slot array with nothing between them, so a slot past
// an object is another object or a return address, and the chip refuses neither.
type slot struct {
	sym    *Symbol
	array  bool
	length int64
	offset int64
}

// declaredSlot gives the first slot of the object an identifier names, as the
// expression naming it is typed, and nothing where the name resolved to no
// declaration. Every settled slot starts at one of these, which is what lets the
// diagnostics name a declaration rather than a type.
func (c *checker) declaredSlot(x *ast.Ident, t *Type) (slot, bool) {
	sym := c.prog.Uses[x]
	if sym == nil {
		return slot{}, false
	}
	if t.Kind() == Array {
		return slot{sym: sym, array: true, length: t.Len()}, true
	}
	// A scalar is a pointer to the first element of a length-1 array, per C.
	return slot{sym: sym, length: 1}, true
}

// addresses reports whether the offset is one of the object's addresses, which
// is 0 through its length. The last of those is the one-past-the-end pointer C
// defines: it occupies no slot, and it may be compared and stepped from.
func (s slot) addresses() bool { return s.offset >= 0 && s.offset <= s.length }

// occupied reports whether the offset is one of the object's own slots, which is
// what reading or writing through the address needs.
func (s slot) occupied() bool { return s.offset >= 0 && s.offset < s.length }

// slotResult is what one lookup answered, kept so that a chain of pointer
// arithmetic costs one lookup per operator rather than one for each operator
// enclosing it.
type slotResult struct {
	slot  slot
	known bool
}

// slotOf reports the object an expression addresses and the constant offset
// into it, and false where analysis cannot settle both. The offset may be
// one the object does not have, which is what every caller exists to
// report. x must have been type checked.
func (c *checker) slotOf(x ast.Expr) (slot, bool) {
	if got, asked := c.slots[x]; asked {
		return got.slot, got.known
	}
	s, known := c.slotNode(x)
	c.slots[x] = slotResult{slot: s, known: known}
	return s, known
}

// slotNode answers for the expressions whose address the source fixes without
// reading an object to find it. A pointer object is not one of them and neither
// is anything computed from one: '*pp', a call's result, a subscript that yields
// a pointer. Which slot each holds is the verifier's answer, not this pass's.
func (c *checker) slotNode(x ast.Expr) (slot, bool) {
	switch x := x.(type) {
	case *ast.Ident:
		// An array name used as a value is the address of its first element.
		if t := c.prog.Types[x]; t.Kind() == Array {
			return c.declaredSlot(x, t)
		}
	case *ast.UnaryExpr:
		if x.Op == ast.AddrOf {
			return c.addressSlot(x.X)
		}
	case *ast.BinaryExpr:
		if x.Op == ast.Add || x.Op == ast.Sub {
			return c.arithmeticSlot(x)
		}
	case *ast.AssignExpr:
		// The value of 'p = E' is the address E named, whatever p goes on to
		// hold. A compound assignment reads p first and answers with nothing.
		if x.Op == ast.Assign {
			return c.slotOf(x.Value)
		}
	case *ast.CondExpr:
		return c.condSlot(x)
	}
	return slot{}, false
}

// addressSlot reports the slot '&' designates. A name is the object itself at
// its first slot; a subscript is that object stepped by the index. '&*E' is E,
// a pointer the source read rather than an address it wrote, so nothing is
// settled for it.
func (c *checker) addressSlot(x ast.Expr) (slot, bool) {
	switch x := x.(type) {
	case *ast.Ident:
		// An array reaches here only in '&a', which is refused for having no
		// type to give it, so what is left occupies one slot.
		if t := c.prog.Types[x]; isScalar(t) || unqual(t).Kind() == Pointer {
			return c.declaredSlot(x, t)
		}
	case *ast.IndexExpr:
		return c.steppedSlot(x.X, x.Index, false)
	}
	return slot{}, false
}

// arithmeticSlot reports the slot '+' or '-' steps to.
func (c *checker) arithmeticSlot(x *ast.BinaryExpr) (slot, bool) {
	base, step := x.X, x.Y
	if unqual(decay(c.prog.Types[base])).Kind() != Pointer {
		// 'k + p' addresses what 'p + k' does. '-' has no such spelling: its
		// pointer operand is the left one, and a pointer on the right makes it a
		// difference rather than an address.
		if x.Op == ast.Sub {
			return slot{}, false
		}
		base, step = x.Y, x.X
	}
	return c.steppedSlot(base, step, x.Op == ast.Sub)
}

// steppedSlot reports the slot reached by stepping a constant number of slots
// from the one base addresses, back where the step is subtracted.
func (c *checker) steppedSlot(base, step ast.Expr, back bool) (slot, bool) {
	from, known := c.slotWithin(base)
	if !known {
		return slot{}, false
	}
	n, constant := c.constOperand(step)
	if !constant {
		return slot{}, false
	}
	if back {
		n = -n
	}
	from.offset += n
	return from, true
}

// condSlot reports the slot '?:' answers with, which is settled only where both
// arms address the same one. Arms that address different objects are refused by
// [checker.sameObject]; arms at different offsets in one object leave the
// address a choice the program makes at run time.
func (c *checker) condSlot(x *ast.CondExpr) (slot, bool) {
	then, known := c.slotWithin(x.Then)
	if !known {
		return slot{}, false
	}
	els, known := c.slotWithin(x.Else)
	if !known || then != els {
		return slot{}, false
	}
	return then, true
}

// slotWithin is [checker.slotOf] restricted to an address the object has. An
// expression that already stepped out was refused where it stepped, so carrying
// the offset further would name one mistake once per enclosing operator.
func (c *checker) slotWithin(x ast.Expr) (slot, bool) {
	s, known := c.slotOf(x)
	if !known || !s.addresses() {
		return slot{}, false
	}
	return s, true
}

// reportSlot names an offset the declared object does not admit, as an address
// where the expression forms one and as a slot where it reads or writes through
// one. The two differ by exactly the one-past-the-end address, which is a
// pointer and occupies nothing.
func (c *checker) reportSlot(pos source.Position, s slot, addressed bool) {
	switch {
	case addressed:
		c.errorf(pos, "an address in '%s' must be between 0 and %d, one past its last slot, found %d",
			s.sym.Name, s.length, s.offset)
	case s.length == 1:
		// A range from 0 to 0 reads as a compiler fault rather than a fact
		// about the object, so the object of one slot gets its own sentence.
		c.errorf(pos, "0 is the only slot '%s' has, found %d", s.sym.Name, s.offset)
	default:
		c.errorf(pos, "a slot of '%s' must be between 0 and %d, the last it has, found %d",
			s.sym.Name, s.length-1, s.offset)
	}
}

// checkPointerStep refuses pointer arithmetic that steps outside the object the
// pointer started in. Forming the address is what is refused, not reading
// through it: C defines an address from the first slot to one past the last, and
// this machine has none beyond either.
func (c *checker) checkPointerStep(x *ast.BinaryExpr) {
	s, known := c.slotOf(x)
	if !known || s.addresses() {
		return
	}
	c.reportSlot(x.OpPos, s, true)
}

// checkDerefSlot refuses a read or a write through a constant address the object
// has no slot at. Only the one-past-the-end address reaches the message: every
// other offset outside the object was refused where it was formed.
func (c *checker) checkDerefSlot(x *ast.UnaryExpr) {
	// '&*E' designates E without reading it, so the address one past the end
	// survives the '*' written under the '&'. See [checker.deref].
	if x == c.addressed {
		return
	}
	// '*&E' reads E and holds no address between the two, so the subscript
	// under the '&' is held by [checker.checkIndexBound] instead.
	if addr, taken := x.X.(*ast.UnaryExpr); taken && addr.Op == ast.AddrOf {
		return
	}
	s, known := c.slotWithin(x.X)
	if !known || s.occupied() {
		return
	}
	c.reportSlot(x.OpPos, s, false)
}

// checkIndexBound refuses a constant subscript that reaches outside the object
// it indexes. A constant index folds away before instruction selection has a
// literal address left to check, and neither an index the program computes nor a
// pointer object carries the numbers to hold, so both are left to the verifier.
func (c *checker) checkIndexBound(x *ast.IndexExpr) {
	base, known := c.slotWithin(x.X)
	if !known {
		return
	}
	i, constant := c.constOperand(x.Index)
	if !constant {
		return
	}
	reached := base
	reached.offset += i
	// A subscript under '&' takes one index more than a read does, since '&a[n]'
	// is the one-past-the-end pointer C defines. [checker.deref] takes the extra
	// index away again wherever a '*' cancels the '&'.
	addressed := x == c.addressed
	if addressed && reached.addresses() || !addressed && reached.occupied() {
		return
	}
	// Anywhere but the first slot of an array the index is not the slot reached,
	// and naming it would name a number the reader cannot act on.
	if base.offset != 0 || !base.array {
		c.reportSlot(x.Index.Pos(), reached, addressed)
		return
	}
	switch {
	case addressed:
		c.errorf(x.Index.Pos(), "an index under '&' must be between 0 and %d, one past the last index of '%s', found %d",
			base.length, base.sym.Name, i)
	case base.length == 1:
		c.errorf(x.Index.Pos(), "0 is the only index '%s' has, found %d", base.sym.Name, i)
	default:
		c.errorf(x.Index.Pos(), "an array index must be between 0 and %d, the last index of '%s', found %d",
			base.length-1, base.sym.Name, i)
	}
}
