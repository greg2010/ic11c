package sema

import (
	"strconv"
	"strings"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/source"
)

// reservedPrefix marks the names the language keeps for intrinsics. A program
// may not declare one, and a call to an unknown one is a misspelling rather
// than a missing function.
const reservedPrefix = "__ic_"

func isReservedName(name string) bool { return strings.HasPrefix(name, reservedPrefix) }

// OperandKind classifies one intrinsic parameter, which decides both what the
// argument may be written as and what analysis must resolve it to.
type OperandKind uint8

const (
	// OperandValue is an ordinary long long expression, evaluated at runtime. Prefab
	// and name hashes are these: the game derives them from a string, and they
	// are integers wherever they are written.
	OperandValue OperandKind = iota
	// OperandDouble is an ordinary double expression, evaluated at runtime. An
	// long long argument widens to reach one.
	OperandDouble
	// OperandSlot is a slot index, which must be an integer constant
	// expression, the form an array bound and a case label take.
	OperandSlot
	// OperandDevice is a device pin, written as db, d0 through d5, or a dev
	// object or parameter.
	OperandDevice
	// OperandLogicType is a logic type name from the generated tables.
	OperandLogicType
	// OperandSlotType is a slot property name from the generated tables.
	OperandSlotType
	// OperandBatchMode is a batch mode name from the generated tables.
	OperandBatchMode
	// OperandReagentMode is a reagent mode name from the generated tables.
	OperandReagentMode
	// OperandString is a string literal, which only __ic_hash accepts and
	// which analysis replaces with its CRC-32.
	OperandString
)

// operandKindNames renders each kind the way a diagnostic names it. No two
// kinds share a rendering, so a message quoting one says which parameter it
// came from.
var operandKindNames = [...]string{
	OperandValue:       "long long value",
	OperandDouble:      "double value",
	OperandSlot:        "slot index",
	OperandDevice:      "device",
	OperandLogicType:   "logic type",
	OperandSlotType:    "slot type",
	OperandBatchMode:   "batch mode",
	OperandReagentMode: "reagent mode",
	OperandString:      "string literal",
}

func (k OperandKind) String() string {
	if int(k) < len(operandKindNames) && operandKindNames[k] != "" {
		return operandKindNames[k]
	}
	return "OperandKind(" + strconv.Itoa(int(k)) + ")"
}

// Intrinsic is the signature of one intrinsic. Intrinsics are ordinary
// identifiers to the grammar and are recognized here by name.
type Intrinsic struct {
	Name   string
	Result *Type
	Params []OperandKind
	// Pure reports that the instruction computes a result and does nothing
	// else, so a call whose result nothing reads may be deleted. Every
	// intrinsic MicroC exposes is impure or unresolved by default: a device
	// access is observable, a yield is timing, and rand answers differently
	// each time.
	Pure bool
}

// Operand is one resolved intrinsic argument.
type Operand struct {
	Kind OperandKind
	// Name is the identifier the source wrote for a named operand, and empty
	// for a value.
	Name string
	// Value is the resolved encoding: the [Device.Code] of a device; the table
	// encoding of a logic type, slot type, batch mode, or reagent mode; the
	// constant slot index; or the CRC-32 of a string literal. It is meaningful
	// only when Resolved is set.
	Value int64
	// Resolved reports whether Value holds the operand's compile-time value.
	// It is false for a runtime argument and for one analysis rejected.
	Resolved bool
	// DeviceParam is the dev parameter a device operand named, and nil
	// otherwise. Which pin it stands for is decided by the call site the body
	// is spliced into, so IR generation resolves it.
	DeviceParam *Symbol
}

// IntrinsicCall records how one intrinsic call resolved. Args is parallel to
// the call's argument list, or shorter when the arity was wrong.
type IntrinsicCall struct {
	Intrinsic *Intrinsic
	Args      []Operand
	// ResultMayBeNonFinite reports that the instruction this call becomes can
	// put a NaN or an infinity in a register. MicroC has neither and IR
	// generation types every long long as an integer, so the optimizer reasons
	// about a value this produces under an assumption the machine does not
	// honour, and every operation and comparison it reaches has to be protected
	// from that. See [nonFiniteResultIntrinsics].
	ResultMayBeNonFinite bool
}

// nonFiniteResultIntrinsics names the intrinsics whose machine instruction can
// answer outside the finite doubles given operands that are inside them, and
// the batch mode operand that has to select it where one does.
//
// The property tracked is non-finiteness rather than NaN alone, because the two
// travel together. Every identity the optimizer applies to an operation the
// machine computes in a register — an integer difference of a value with
// itself, a product with zero, a quotient of a value by itself, and the ordered
// comparisons — is unsound for an infinity for exactly the reason it is unsound
// for a NaN, and an infinity is one arithmetic step from a NaN whichever way it
// arrived.
//
// A batch read matching no device answers Average with NaN and Maximum with
// negative infinity; Sum and Minimum answer zero, so those two alone are
// finite. The rest are partial for one of three reasons:
//
//   - outside a domain of finite values: sqrt of a negative, log of a negative
//     or of zero, asin or acos past 1, and pow of a negative base under a
//     fractional exponent;
//   - at an infinity: sin, cos and tan, which have no limit there, and lerp,
//     whose infinite endpoint less its other endpoint, or times a zero weight,
//     is a NaN;
//   - past what a double holds: exp and pow overflow to an infinity from finite
//     operands.
//
// Every intrinsic absent from this table answers a finite double for every
// finite double the machine holds — atan and atan2 have bounded ranges, min,
// max and clamp answer with one of their operands, sgn answers 0, 1 or -1, rand
// draws between 0 and 1, and the rounding family maps a finite value to a
// finite one — or faults instead, which is what a read of an unconnected pin
// does. A value that is already non-finite reaches those through the taint walk
// rather than through this table.
//
// batchModeArg is the index of the operand naming the aggregate, or -1 for an
// intrinsic whose result does not depend on one.
var nonFiniteResultIntrinsics = map[string]struct{ batchModeArg int }{
	"__ic_load_batch":            {batchModeArg: 2},
	"__ic_load_batch_named":      {batchModeArg: 3},
	"__ic_load_batch_slot":       {batchModeArg: 3},
	"__ic_load_batch_named_slot": {batchModeArg: 4},

	"__ic_sqrt": {batchModeArg: -1},
	"__ic_log":  {batchModeArg: -1},
	"__ic_asin": {batchModeArg: -1},
	"__ic_acos": {batchModeArg: -1},
	"__ic_pow":  {batchModeArg: -1},
	"__ic_exp":  {batchModeArg: -1},
	"__ic_sin":  {batchModeArg: -1},
	"__ic_cos":  {batchModeArg: -1},
	"__ic_tan":  {batchModeArg: -1},
	"__ic_lerp": {batchModeArg: -1},
}

// nonFiniteBatchModes are the aggregates a batch read with no matching device
// answers outside the finite doubles: Average with NaN, Maximum with negative
// infinity.
var nonFiniteBatchModes = map[string]bool{"Average": true, "Maximum": true}

// The guarded ordered comparisons. IR generation emits a call to one of these
// in place of an ordered comparison a value marked by
// [nonFiniteResultIntrinsics] can reach, and instruction selection turns it
// back into the machine's own
// compare-and-branch. Standing between the two is what withholds the comparison
// from the optimizer, which would otherwise rewrite the predicate into its
// negation with the branch targets swapped — sound over integers and a
// miscompilation over doubles, where an ordered comparison and its negation are
// both false for a NaN.
//
// The names live here because this package owns the reserved prefix. No source
// line contains one: a program declaring the name is rejected for using the
// prefix, and a call to it is an unrecognized intrinsic.
const (
	CompareLT = reservedPrefix + "cmp_lt"
	CompareLE = reservedPrefix + "cmp_le"
	CompareGT = reservedPrefix + "cmp_gt"
	CompareGE = reservedPrefix + "cmp_ge"
)

// resultMayBeNonFinite answers [IntrinsicCall.ResultMayBeNonFinite] for one
// resolved call.
func resultMayBeNonFinite(call *IntrinsicCall) bool {
	source, known := nonFiniteResultIntrinsics[call.Intrinsic.Name]
	if !known {
		return false
	}
	if source.batchModeArg < 0 {
		return true
	}
	if source.batchModeArg >= len(call.Args) {
		// The arity was wrong, which is already reported. Assuming the mode
		// that can produce one keeps this from being the thing that decides
		// whether a broken call compiles.
		return true
	}
	return nonFiniteBatchModes[call.Args[source.batchModeArg].Name]
}

func def(name string, result *Type, params ...OperandKind) *Intrinsic {
	return &Intrinsic{Name: name, Result: result, Params: params}
}

// Intrinsics is every intrinsic MicroC defines.
//
// The entries are shared and must not be modified. It is exported for the check
// that the C prelude in internal/ic10 declares a prototype for each of them and
// for no other name; that header is written as literal text by a generator
// which cannot read this table, so the two are otherwise unrelated. Analysis
// resolves a call through an index built from it rather than through the slice.
var Intrinsics = buildIntrinsics()

// intrinsics indexes [Intrinsics] by name.
var intrinsics = indexIntrinsics(Intrinsics)

func buildIntrinsics() []*Intrinsic {
	list := []*Intrinsic{
		def("__ic_load", DoubleType, OperandDevice, OperandLogicType),
		def("__ic_store", VoidType, OperandDevice, OperandLogicType, OperandDouble),
		def("__ic_load_slot", DoubleType, OperandDevice, OperandSlot, OperandSlotType),
		def("__ic_store_slot", VoidType, OperandDevice, OperandSlot, OperandSlotType, OperandDouble),
		def("__ic_load_batch", DoubleType, OperandValue, OperandLogicType, OperandBatchMode),
		def("__ic_store_batch", VoidType, OperandValue, OperandLogicType, OperandDouble),
		def("__ic_load_batch_named", DoubleType, OperandValue, OperandValue, OperandLogicType, OperandBatchMode),
		def("__ic_store_batch_named", VoidType, OperandValue, OperandValue, OperandLogicType, OperandDouble),
		def("__ic_load_batch_slot", DoubleType, OperandValue, OperandSlot, OperandSlotType, OperandBatchMode),
		def("__ic_store_batch_slot", VoidType, OperandValue, OperandSlot, OperandSlotType, OperandDouble),
		def("__ic_load_batch_named_slot", DoubleType, OperandValue, OperandValue, OperandSlot, OperandSlotType, OperandBatchMode),
		def("__ic_load_reagent", DoubleType, OperandDevice, OperandReagentMode, OperandValue),
		def("__ic_device_present", BoolType, OperandDevice),
		def("__ic_hash", IntType, OperandString),

		def("__ic_yield", VoidType),
		def("__ic_sleep", VoidType, OperandDouble),

		def("__ic_isnan", BoolType, OperandDouble),
		def("__ic_rand", DoubleType),
	}
	for _, name := range []string{
		"__ic_sqrt", "__ic_abs", "__ic_sgn", "__ic_round", "__ic_ceil",
		"__ic_floor", "__ic_log", "__ic_exp", "__ic_sin", "__ic_cos", "__ic_tan",
		"__ic_asin", "__ic_acos", "__ic_atan",
	} {
		list = append(list, def(name, DoubleType, OperandDouble))
	}
	// The toward-zero rounding is a function of its argument and touches
	// nothing, so a dead one may be deleted and two of the same argument may be
	// merged. IR generation reads it as the barrier that keeps the optimizer's
	// idea of an integer's range off the arithmetic a non-finite value reaches,
	// and every such reading would cost an instruction of its own without this.
	trunc := def("__ic_trunc", DoubleType, OperandDouble)
	trunc.Pure = true
	list = append(list, trunc)
	for _, name := range []string{"__ic_min", "__ic_max", "__ic_pow", "__ic_atan2"} {
		list = append(list, def(name, DoubleType, OperandDouble, OperandDouble))
	}
	for _, name := range []string{"__ic_clamp", "__ic_lerp"} {
		list = append(list, def(name, DoubleType, OperandDouble, OperandDouble, OperandDouble))
	}

	return list
}

func indexIntrinsics(list []*Intrinsic) map[string]*Intrinsic {
	byName := make(map[string]*Intrinsic, len(list))
	for _, in := range list {
		byName[in.Name] = in
	}
	return byName
}

// intrinsicCall checks a call to a recognized intrinsic and records how each
// argument resolved.
func (c *checker) intrinsicCall(x *ast.CallExpr, in *Intrinsic) *Type {
	if len(x.Args) != len(in.Params) {
		c.errorf(x.Lparen, "%s expects %s, found %d", in.Name, source.Plural(len(in.Params), "argument"), len(x.Args))
	}
	call := &IntrinsicCall{Intrinsic: in}
	for i, arg := range x.Args {
		if i >= len(in.Params) {
			c.expr(arg)
			continue
		}
		call.Args = append(call.Args, c.operand(in, i, arg))
	}
	call.ResultMayBeNonFinite = resultMayBeNonFinite(call)
	c.prog.Intrinsics[x] = call
	return in.Result
}

// operand checks one argument against the parameter kind at index i.
//
// A table name is not looked up in scope: a logic type carries meaning only
// here, and a variable of the same name is a different thing. A device is the
// one named operand that can also be a declaration, since dev is a type, and a
// bare pin spelling still wins over a variable of the same name.
func (c *checker) operand(in *Intrinsic, i int, arg ast.Expr) Operand {
	kind := in.Params[i]
	switch kind {
	case OperandValue:
		t := c.expr(arg)
		c.assign(IntType, t, arg, "an argument to "+in.Name)
		return Operand{Kind: kind}

	case OperandDouble:
		t := c.expr(arg)
		c.assign(DoubleType, t, arg, "an argument to "+in.Name)
		return Operand{Kind: kind}

	case OperandSlot:
		t := c.expr(arg)
		c.assign(IntType, t, arg, "an argument to "+in.Name)
		v, ok := c.requireConst(arg, integerConst, "the slot index of "+in.Name)
		// A device's slots are indexed from zero. The chip resolves the operand
		// when the line is assembled and checks nothing, so a negative index
		// reaches the device at run time and faults there, once per tick, with
		// no diagnostic beyond a line number.
		if ok && v.Int < 0 {
			c.errorf(arg.Pos(), "the slot index of %s is %d, and a device's slots are numbered from 0", in.Name, v.Int)
			return Operand{Kind: kind}
		}
		return Operand{Kind: kind, Value: v.Int, Resolved: ok}

	case OperandString:
		lit, ok := arg.(*ast.StringLit)
		if !ok {
			c.errorf(arg.Pos(), "%s takes a string literal", in.Name)
			return Operand{Kind: kind}
		}
		return Operand{Kind: kind, Name: lit.Value, Value: hashString(lit.Value), Resolved: true}

	case OperandDevice:
		device, param, ok := c.resolveDevice(arg, "the device argument of "+in.Name)
		switch {
		case !ok:
			return Operand{Kind: kind}
		case param != nil:
			return Operand{Kind: kind, Name: param.Name, DeviceParam: param}
		default:
			return Operand{Kind: kind, Name: device.String(), Value: device.Code(), Resolved: true}
		}

	case OperandLogicType, OperandSlotType, OperandBatchMode, OperandReagentMode:
		return c.namedOperand(in, kind, arg)

	default:
		return Operand{Kind: kind}
	}
}

func (c *checker) namedOperand(in *Intrinsic, kind OperandKind, arg ast.Expr) Operand {
	id, ok := arg.(*ast.Ident)
	if !ok {
		c.errorf(arg.Pos(), "the %s argument of %s must be written as a name", kind, in.Name)
		return Operand{Kind: kind}
	}
	var member Member
	switch kind {
	case OperandLogicType:
		member, ok = c.tables.LogicType(id.Name)
	case OperandSlotType:
		member, ok = c.tables.LogicSlotType(id.Name)
	case OperandBatchMode:
		member, ok = c.tables.BatchMode(id.Name)
	case OperandReagentMode:
		member, ok = c.tables.ReagentMode(id.Name)
	case OperandValue, OperandDouble, OperandSlot, OperandDevice, OperandString:
		// No table names these, and the caller routes only the four above here.
		return Operand{Kind: kind, Name: id.Name}
	}
	if !ok {
		c.errorf(id.NamePos, "'%s' is not a %s", id.Name, kind)
		return Operand{Kind: kind, Name: id.Name}
	}
	if member.Deprecated {
		// Not an error: the chip resolves a retired member exactly like any
		// other, so refusing would reject a program that works today. What the
		// programmer cannot see without being told is that the game has moved
		// the property elsewhere and may stop populating this one.
		c.warnf(id.NamePos, "'%s' is a %s the game marks deprecated; it still resolves on the chip and this program is emitted unchanged, but a later game build may stop maintaining it — check the device's current properties for the one that replaced it", id.Name, kind)
	}
	return Operand{Kind: kind, Name: id.Name, Value: int64(member.Value), Resolved: true}
}
