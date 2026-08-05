package ast

import "github.com/greg2010/ic11c/internal/source"

// ScalarKind names a built-in scalar type keyword. long long, bool, and double are
// distinct for type checking and share one machine representation; dev names a
// device pin and never occupies one; void exists only as a function result.
type ScalarKind uint8

const (
	Int ScalarKind = iota
	Bool
	Double
	Dev
	Void
)

var scalarKindNames = [...]string{Int: "long long", Bool: "bool", Double: "double", Dev: "dev", Void: "void"}

func (k ScalarKind) String() string {
	return source.EnumName(scalarKindNames[:], int(k), "ScalarKind")
}

// ScalarKinds lists every scalar type MicroC has, in declaration order.
//
// It exists so a caller holding the closed set of type spellings reads the
// enumeration rather than re-listing it.
func ScalarKinds() []ScalarKind {
	kinds := make([]ScalarKind, len(scalarKindNames))
	for i := range scalarKindNames {
		kinds[i] = ScalarKind(i)
	}
	return kinds
}

// UnaryOp is a prefix operator that is not an increment or decrement.
type UnaryOp uint8

const (
	Plus UnaryOp = iota
	Neg
	LogicalNot
	BitNot
	AddrOf
	Deref
)

var unaryOpNames = [...]string{
	Plus:       "+",
	Neg:        "-",
	LogicalNot: "!",
	BitNot:     "~",
	AddrOf:     "&",
	Deref:      "*",
}

func (op UnaryOp) String() string { return source.EnumName(unaryOpNames[:], int(op), "UnaryOp") }

// IncDecOp distinguishes ++ from --. Whether it applies before or after the
// operand is recorded on [IncDecExpr].
type IncDecOp uint8

const (
	Inc IncDecOp = iota
	Dec
)

var incDecOpNames = [...]string{Inc: "++", Dec: "--"}

func (op IncDecOp) String() string { return source.EnumName(incDecOpNames[:], int(op), "IncDecOp") }

// BinaryOp is an infix operator. Every MicroC binary operator is left
// associative.
type BinaryOp uint8

const (
	Add BinaryOp = iota
	Sub
	Mul
	Div
	Mod
	Shl
	Shr
	BitAnd
	BitOr
	BitXor
	Eq
	Ne
	Lt
	Le
	Gt
	Ge
	LogicalAnd
	LogicalOr
)

var binaryOpNames = [...]string{
	Add:        "+",
	Sub:        "-",
	Mul:        "*",
	Div:        "/",
	Mod:        "%",
	Shl:        "<<",
	Shr:        ">>",
	BitAnd:     "&",
	BitOr:      "|",
	BitXor:     "^",
	Eq:         "==",
	Ne:         "!=",
	Lt:         "<",
	Le:         "<=",
	Gt:         ">",
	Ge:         ">=",
	LogicalAnd: "&&",
	LogicalOr:  "||",
}

func (op BinaryOp) String() string { return source.EnumName(binaryOpNames[:], int(op), "BinaryOp") }

// AssignOp is a plain or compound assignment operator.
type AssignOp uint8

const (
	Assign AssignOp = iota
	AddAssign
	SubAssign
	MulAssign
	DivAssign
	ModAssign
	ShlAssign
	ShrAssign
	AndAssign
	OrAssign
	XorAssign
)

var assignOpNames = [...]string{
	Assign:    "=",
	AddAssign: "+=",
	SubAssign: "-=",
	MulAssign: "*=",
	DivAssign: "/=",
	ModAssign: "%=",
	ShlAssign: "<<=",
	ShrAssign: ">>=",
	AndAssign: "&=",
	OrAssign:  "|=",
	XorAssign: "^=",
}

func (op AssignOp) String() string { return source.EnumName(assignOpNames[:], int(op), "AssignOp") }
