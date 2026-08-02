package ast

import "strconv"

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

func (k ScalarKind) String() string { return enumName(scalarKindNames[:], uint8(k), "ScalarKind") }

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

func (op UnaryOp) String() string { return enumName(unaryOpNames[:], uint8(op), "UnaryOp") }

// IncDecOp distinguishes ++ from --. Whether it applies before or after the
// operand is recorded on [IncDecExpr].
type IncDecOp uint8

const (
	Inc IncDecOp = iota
	Dec
)

var incDecOpNames = [...]string{Inc: "++", Dec: "--"}

func (op IncDecOp) String() string { return enumName(incDecOpNames[:], uint8(op), "IncDecOp") }

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

func (op BinaryOp) String() string { return enumName(binaryOpNames[:], uint8(op), "BinaryOp") }

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

func (op AssignOp) String() string { return enumName(assignOpNames[:], uint8(op), "AssignOp") }

func enumName(names []string, v uint8, typeName string) string {
	if int(v) < len(names) && names[v] != "" {
		return names[v]
	}
	return typeName + "(" + strconv.Itoa(int(v)) + ")"
}
