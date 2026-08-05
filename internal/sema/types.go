package sema

import (
	"strconv"

	"github.com/greg2010/ic11c/internal/source"
)

// Kind classifies a MicroC type.
type Kind uint8

const (
	// Invalid is the type of an expression whose type could not be
	// determined. Every operation on it yields Invalid again and reports
	// nothing, so one root cause costs one diagnostic.
	Invalid Kind = iota
	// Int is the integral scalar type, exact to 2^53 at runtime.
	Int
	// Bool holds 0 or 1 and is distinct from Int for type checking.
	Bool
	// Double is the fractional scalar type. It is the machine's own value
	// type, and what every device read and every math intrinsic answers with.
	Double
	// Dev names a device pin. It has no runtime representation: the chip needs
	// a literal in a device position, so every dev resolves at compile time.
	Dev
	// Void is a function result type only.
	Void
	// Pointer is an index into the 512-slot space. It always designates an
	// object; there is no null pointer.
	Pointer
	// Array is one-dimensional with a constant bound.
	Array
)

var kindNames = [...]string{
	Invalid: "invalid",
	Int:     "long long",
	Bool:    "bool",
	Double:  "double",
	Dev:     "dev",
	Void:    "void",
	Pointer: "pointer",
	Array:   "array",
}

func (k Kind) String() string { return source.EnumName(kindNames[:], int(k), "Kind") }

// Type is a MicroC type. It is immutable once built and compared with
// [Type.Equal] rather than by pointer, since analysis builds the same type
// more than once. const is part of the type: "const long long *p" qualifies
// the pointee while leaving p assignable, and only the type carries that.
type Type struct {
	kind  Kind
	konst bool
	elem  *Type
	size  int64
	// spelling is how the source that produced this type wrote it. It is not
	// part of type identity: two types differing only here are one type.
	spelling string
}

// The predeclared scalar types. Their fields are unexported, so the values are
// safe to share.
var (
	IntType    = &Type{kind: Int}
	BoolType   = &Type{kind: Bool}
	DoubleType = &Type{kind: Double}
	DevType    = &Type{kind: Dev}
	VoidType   = &Type{kind: Void}

	constIntType    = &Type{kind: Int, konst: true}
	constBoolType   = &Type{kind: Bool, konst: true}
	constDoubleType = &Type{kind: Double, konst: true}
	constDevType    = &Type{kind: Dev, konst: true}
	invalidType     = &Type{kind: Invalid}
)

// PointerTo returns the type of a pointer to elem.
func PointerTo(elem *Type) *Type { return &Type{kind: Pointer, elem: elem} }

// spelled returns t written the way spelling writes it, or t unchanged where
// spelling is empty or is already t's own name. The result is [Type.Equal] to t.
func spelled(t *Type, spelling string) *Type {
	if t == nil || spelling == "" || spelling == kindNames[t.kind] {
		return t
	}
	u := *t
	u.spelling = spelling
	return &u
}

// intAs gives the integer type for a message that has no written specifier of
// its own, spelled the way the first of ts to reach it does, or the file's own.
func (c *checker) intAs(ts ...*Type) *Type {
	for _, t := range ts {
		if spelling, names := intSpelling(t); names {
			return spelled(IntType, spelling)
		}
	}
	return c.intType
}

// intSpelling reports how t writes the integer type, looking through a pointer
// or an array to the element that carries it, and false where t does not reach
// it at all. The empty spelling is the canonical one.
func intSpelling(t *Type) (string, bool) {
	for t != nil {
		switch t.kind {
		case Int:
			return t.spelling, true
		case Pointer, Array:
			t = t.elem
		case Invalid, Bool, Double, Dev, Void:
			return "", false
		}
	}
	return "", false
}

// ArrayOf returns the type of an array of n elements of elem. n is the bound
// the declaration wrote, which the caller has already checked is a positive
// constant expression.
func ArrayOf(elem *Type, n int64) *Type { return &Type{kind: Array, elem: elem, size: n} }

// Kind reports what t is. The nil Type is Invalid, so a caller holding the type
// of an expression analysis rejected needs no nil check.
func (t *Type) Kind() Kind {
	if t == nil {
		return Invalid
	}
	return t.kind
}

// Elem returns the pointee of a pointer or the element type of an array, and
// nil for every other type.
func (t *Type) Elem() *Type {
	if t == nil {
		return nil
	}
	return t.elem
}

// Len returns the bound of an array type and 0 for every other type.
func (t *Type) Len() int64 {
	if t == nil {
		return 0
	}
	return t.size
}

// IsConst reports whether t itself is const-qualified. It says nothing about
// the pointee of a pointer type: "const long long *" is not const, its element is.
func (t *Type) IsConst() bool { return t != nil && t.konst }

// Equal reports whether t and u are the same type, qualifiers included.
func (t *Type) Equal(u *Type) bool {
	if t == nil || u == nil {
		return t == u
	}
	if t.kind != u.kind || t.konst != u.konst || t.size != u.size {
		return false
	}
	if t.elem == nil || u.elem == nil {
		return t.elem == u.elem
	}
	return t.elem.Equal(u.elem)
}

// String renders t the way the source would write it.
func (t *Type) String() string {
	if t == nil {
		return kindNames[Invalid]
	}
	// Default is the rule: a kind with no elem and no bound renders as its own
	// name, so a new one reads correctly without being named here.
	//exhaustive:ignore
	switch t.kind {
	case Pointer:
		return t.elem.String() + " *"
	case Array:
		return t.elem.String() + "[" + strconv.FormatInt(t.size, 10) + "]"
	default:
		name := t.spelling
		if name == "" {
			name = t.kind.String()
		}
		if t.konst {
			return "const " + name
		}
		return name
	}
}

// qualified returns t with the const qualifier applied, sharing the predeclared
// values where it can.
func qualified(t *Type, konst bool) *Type {
	if !konst || t == nil || t.konst {
		return t
	}
	if t.spelling != "" {
		u := *t
		u.konst = true
		return &u
	}
	// Default is the rule: the named kinds only spare an allocation, and a kind
	// with no predeclared const value is qualified by copying it.
	//exhaustive:ignore
	switch t.kind {
	case Int:
		return constIntType
	case Bool:
		return constBoolType
	case Double:
		return constDoubleType
	case Dev:
		return constDevType
	default:
		return &Type{kind: t.kind, konst: true, elem: t.elem, size: t.size}
	}
}

// unqual drops a top-level const qualifier, which is what reading an object
// yields: the value is a copy and nothing about it is const.
func unqual(t *Type) *Type {
	if t == nil || !t.konst {
		return t
	}
	if t.spelling != "" {
		u := *t
		u.konst = false
		return &u
	}
	// Default is the rule: the named kinds only spare an allocation, and a kind
	// with no predeclared value drops the qualifier by copying it.
	//exhaustive:ignore
	switch t.kind {
	case Int:
		return IntType
	case Bool:
		return BoolType
	case Double:
		return DoubleType
	case Dev:
		return DevType
	default:
		return &Type{kind: t.kind, elem: t.elem, size: t.size}
	}
}

// decay converts an array type to a pointer to its first element, which is what
// an array name used as a value denotes.
func decay(t *Type) *Type {
	if t.Kind() != Array {
		return t
	}
	return PointerTo(t.elem)
}

// isConstObject reports whether a declaration of type t declares an object that
// cannot be written after it is created, which is what makes an initializer
// mandatory. An array of const elements qualifies; a pointer to const does not,
// since the pointer itself stays assignable.
func isConstObject(t *Type) bool {
	// Default is the rule: every type but an array carries the qualifier that
	// decides this on itself.
	//exhaustive:ignore
	switch t.Kind() {
	case Array:
		return t.Elem().IsConst()
	default:
		return t.IsConst()
	}
}

// isScalar reports whether t is a type an expression may compute and a variable
// may hold in a register. dev is not one: it resolves at compile time and never
// occupies storage.
func isScalar(t *Type) bool {
	switch unqual(t).Kind() {
	case Int, Bool, Double:
		return true
	case Invalid, Dev, Void, Pointer, Array:
	}
	return false
}

// isIntegral reports whether t is one of the integer types, which is what an
// integer constant expression computes in. bool is one: C counts it among the
// integer types, and a bool constant folds to 0 or 1 the same way.
func isIntegral(t *Type) bool {
	switch unqual(t).Kind() {
	case Int, Bool:
		return true
	case Invalid, Double, Dev, Void, Pointer, Array:
	}
	return false
}

// isArithmetic reports whether t is a type the arithmetic operators take.
func isArithmetic(t *Type) bool {
	switch unqual(decay(t)).Kind() {
	case Int, Double:
		return true
	case Invalid, Bool, Dev, Void, Pointer, Array:
	}
	return false
}

// assignableTo reports whether a value of type src may initialize, be
// assigned to, be passed as, or be returned as dst.
func assignableTo(dst, src *Type) bool {
	d := unqual(dst)
	s := unqual(decay(src))
	switch d.Kind() {
	case Int, Bool:
		return s.Kind() == Int || s.Kind() == Bool
	case Double:
		return s.Kind() == Double || s.Kind() == Int || s.Kind() == Bool
	case Dev:
		return s.Kind() == Dev
	case Pointer:
		return s.Kind() == Pointer && compatiblePointee(d.Elem(), s.Elem())
	case Invalid, Void, Array:
	}
	return false
}

// arithType gives the type a binary arithmetic or relational operator
// computes in, and Invalid for an operand pair that does not meet. long long
// widens to double where the other operand is one — the one place an
// operator converts anything — since the widening is exact and loses nothing.
// An integer result is the left operand's own type, so a message about it names
// the integer type the way that operand was written.
func arithType(lhs, rhs *Type) *Type {
	left, right := unqual(decay(lhs)), unqual(decay(rhs))
	l, r := left.Kind(), right.Kind()
	switch {
	case l == Double && (r == Double || r == Int):
		return DoubleType
	case l == Int && r == Double:
		return DoubleType
	case l == Int && r == Int:
		return left
	default:
		return invalidType
	}
}

// compatiblePointee reports whether a pointer to src may be assigned to a
// pointer to dst. The pointee types must agree, and the assignment may add
// const but never drop it.
func compatiblePointee(dst, src *Type) bool {
	if src.IsConst() && !dst.IsConst() {
		return false
	}
	if dst.Kind() != src.Kind() {
		return false
	}
	switch dst.Kind() {
	case Int, Bool, Double:
		return true
	case Pointer:
		return compatiblePointee(dst.Elem(), src.Elem())
	case Invalid, Dev, Void, Array:
	}
	return false
}
