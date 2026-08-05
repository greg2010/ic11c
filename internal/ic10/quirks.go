package ic10

import (
	"math"

	"github.com/greg2010/ic11c/internal/isa"
)

// Instructions the backend must never select, and why: behavioural defects
// of the shipped build, not properties of the instruction set, so no
// extraction can express them. An instruction that faults does so once per
// tick, indefinitely, with only an error line number as diagnostic.
var unemittableOps = map[Opcode]string{
	// Uncompilable: the parser requires exactly two operands (array.Length==3)
	// then reads array[3], the third operand, which never exists. Three
	// operands trip the count check instead; no arity assembles.
	isa.OpBrapz:  "uncompilable in this build: the operand count check and the operand read disagree, so no operand count assembles",
	isa.OpBrnaz:  "uncompilable in this build: the operand count check and the operand read disagree, so no operand count assembles",
	isa.OpBapzal: "uncompilable in this build: the operand count check and the operand read disagree, so no operand count assembles",
	isa.OpBnazal: "uncompilable in this build: the operand count check and the operand read disagree, so no operand count assembles",

	// sla and sll share one case, constructing the same operation: a plain
	// left shift with zero fill via C#'s <<. The in-game help text claims sla
	// sign-fills; it does not. Emit sll, the honest name for what both do.
	isa.OpSla: "byte-identical to sll, which is the honest name for a zero-fill left shift; emit sll",

	// Relative forms encode a line offset, not a target line: any pass that
	// inserts or removes a line silently retargets them, invisibly until the
	// wrong line runs. Absolute forms cost the same and survive every pass.
	isa.OpBrdse: relativeBranch,
	isa.OpBrdns: relativeBranch,
	isa.OpBrltz: relativeBranch,
	isa.OpBrgez: relativeBranch,
	isa.OpBrlez: relativeBranch,
	isa.OpBrgtz: relativeBranch,
	isa.OpBreq:  relativeBranch,
	isa.OpBrne:  relativeBranch,
	isa.OpBrap:  relativeBranch,
	isa.OpBrna:  relativeBranch,
	isa.OpBrlt:  relativeBranch,
	isa.OpBrgt:  relativeBranch,
	isa.OpBrle:  relativeBranch,
	isa.OpBrge:  relativeBranch,
	isa.OpBreqz: relativeBranch,
	isa.OpBrnez: relativeBranch,
	isa.OpBrnan: relativeBranch,
	isa.OpJr:    relativeBranch,

	// hcf is not a trap or an abort. It destroys the chip and starts a fire,
	// so it is never a legitimate lowering of anything, including unreachable
	// code or a failed assertion.
	isa.OpHcf: "destroys the chip and starts a fire; it is not a trap",
}

// relativeBranch is the shared reason for every line-offset encoded form.
const relativeBranch = "encodes a line offset, which any pass that changes the line count silently corrupts; use the absolute form"

// Instructions that are correct on every line but the program's first, and
// why. Weaker than unemittableOps: the instruction itself is sound, so only
// its placement is wrong, and no stage building it yet knows which line it
// will occupy.
var firstLineHazards = map[Opcode]string{
	// sleep re-enters by returning -index; on any other line that is negative
	// and the tick ends. On line 0, -index is int 0 (no negative zero), so
	// the loop condition (index >= 0) stays true and it spins for the whole
	// 128-instruction budget instead of yielding, every tick.
	isa.OpSleep: "on line 0 it returns -0, which fails the negative test that ends the tick, so instead of yielding it re-enters itself until the whole 128 instruction budget is gone, every tick until its duration elapses",
}

// FirstLineHazard reports whether op must not occupy program line 0, and
// the reason. Line 0 is the only position that carries one, since the
// hazard is in what the instruction returns to a tick loop that has not
// yet advanced; elsewhere the instruction is ordinary, unlike [Unemittable].
func FirstLineHazard(op Opcode) (reason string, ok bool) {
	reason, ok = firstLineHazards[op]
	return reason, ok
}

// Unemittable reports whether the backend must never select op, and the
// reason. Stronger than Instruction.Deprecated: a deprecated instruction
// assembles and works, these cannot assemble, silently miscompile, or
// destroy the chip.
func Unemittable(op Opcode) (reason string, ok bool) {
	reason, ok = unemittableOps[op]
	return reason, ok
}

// UnreadableValue is a value a register holds that no operand literal can
// name, with the arithmetic that puts it there instead: one instruction of
// the form `Op dst Left Right`, costing one line. Left and Right are
// themselves readable literals.
type UnreadableValue struct {
	// Reason says what the chip does with every literal spelling of the value,
	// which is the part a reader of this repository cannot check.
	Reason      string
	Op          Opcode
	Left, Right float64
}

// Unreadable reports whether value is one the chip's operand parser cannot
// reproduce, with arithmetic that computes it instead. The parser is
// Mono's double.TryParse under NumberStyles.Number; two values survive
// none of the constant table, the internal enums, or that parse.
func Unreadable(value float64) (UnreadableValue, bool) {
	switch {
	case math.IsNaN(value):
		// The constant table's "nan" parses, but DoubleValueVariable reports a
		// NaN _Value as no value at all, so any spelling raises IncorrectVariable
		// every tick the line runs.
		return UnreadableValue{
			Reason: "the operand parser reports a NaN as no value at all, so an instruction reading one raises IncorrectVariable every tick",
			Op:     isa.OpDiv, Left: 0, Right: 0,
		}, true
	case value == 0 && math.Signbit(value):
		// double.TryParse reads every negative-zero spelling ("-0", "-0.0",
		// "-.0") and any underflowing magnitude as +0.0 — the only place the
		// chip conflates the two zeros it keeps distinct everywhere else (max
		// answers -0 for a tie; dividing by one keeps the infinity's sign).
		return UnreadableValue{
			Reason: "the operand parser reads every spelling of a negative zero, and every magnitude that underflows to one, as +0.0",
			Op:     isa.OpMul, Left: 0, Right: -1,
		}, true
	}
	return UnreadableValue{}, false
}
