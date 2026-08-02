package ic10

// Instructions the backend must never select, and why.
//
// These are behavioural defects and hazards of the shipped build, not
// properties of the instruction set, so no extraction from the assembly can
// express them. Each reason below is sourced from the decompiled
// implementation in Assets.Scripts.Objects.Electrical.ProgrammableChip for the
// assembly named by ManifestID, because the reason exists nowhere a reader of
// this repository can check it.
//
// The chip validates almost nothing at compile time, so an instruction that
// faults does so once per tick, indefinitely, with no diagnostic beyond an
// error line number. Keeping these out of the selection tables is the only
// place the mistake can be caught.
var unemittableOps = map[Opcode]string{
	// Uncompilable. The parser checks the operand count against 3 and then
	// reads array[3], the fourth operand: three operands raise an argument
	// count error, two raise an index-out-of-range surfaced as an unknown
	// error. There is no operand count that assembles. These are also
	// relative forms, so they would be unusable even if the arity were fixed.
	OpBrapz:  "uncompilable in this build: the operand count check and the operand read disagree, so no operand count assembles",
	OpBrnaz:  "uncompilable in this build: the operand count check and the operand read disagree, so no operand count assembles",
	OpBapzal: "uncompilable in this build: the operand count check and the operand read disagree, so no operand count assembles",
	OpBnazal: "uncompilable in this build: the operand count check and the operand read disagree, so no operand count assembles",

	// sla and sll share one case and construct the same operation class, which
	// performs a plain left shift with zero fill. The in-game help text
	// claiming sla sign-fills is wrong. Emit sll, which is the honest name for
	// what both do.
	OpSla: "byte-identical to sll, which is the honest name for a zero-fill left shift; emit sll",

	// Relative branches encode a line offset rather than a target line. Any
	// pass that inserts or removes a line silently retargets them, and the
	// corruption is invisible until the wrong line runs. Absolute forms cost
	// the same and survive every pass.
	OpBrdse: relativeBranch,
	OpBrdns: relativeBranch,
	OpBrltz: relativeBranch,
	OpBrgez: relativeBranch,
	OpBrlez: relativeBranch,
	OpBrgtz: relativeBranch,
	OpBreq:  relativeBranch,
	OpBrne:  relativeBranch,
	OpBrap:  relativeBranch,
	OpBrna:  relativeBranch,
	OpBrlt:  relativeBranch,
	OpBrgt:  relativeBranch,
	OpBrle:  relativeBranch,
	OpBrge:  relativeBranch,
	OpBreqz: relativeBranch,
	OpBrnez: relativeBranch,
	OpBrnan: relativeBranch,
	OpJr:    relativeBranch,

	// hcf is not a trap or an abort. It destroys the chip and starts a fire,
	// so it is never a legitimate lowering of anything, including unreachable
	// code or a failed assertion.
	OpHcf: "destroys the chip and starts a fire; it is not a trap",
}

// relativeBranch is the shared reason for every line-offset encoded form.
const relativeBranch = "encodes a line offset, which any pass that changes the line count silently corrupts; use the absolute form"

// Instructions that are correct on every line but the program's first, and why.
//
// This is a weaker condition than unemittableOps and a differently timed one.
// The instruction itself is sound, so refusing to construct it would refuse a
// working program; what is wrong is one placement, and no stage that builds an
// instruction knows which line it will occupy. The check belongs to whichever
// stage holds the final layout, which is where mir.Program.CheckPlacement runs
// it.
var firstLineHazards = map[Opcode]string{
	// sleep re-enters itself each tick until its duration elapses, signalling
	// re-entry by returning the negation of the line it sits on. On line 0 that
	// is -0, which fails the negative test that ends the tick, so the chip
	// re-runs the sleep until the 128 instruction budget is gone and does it
	// again every tick for the whole duration. On any other line the negation
	// is negative and the tick ends as intended.
	OpSleep: "on line 0 it returns -0, which fails the negative test that ends the tick, so instead of yielding it re-enters itself until the whole 128 instruction budget is gone, every tick until its duration elapses",
}

// FirstLineHazard reports whether op must not occupy program line 0, along with
// the reason it must not.
//
// Line 0 is the only position that carries one: it is where the chip starts,
// and the hazard is about what the instruction returns to a tick loop that has
// not yet advanced. An instruction listed here is ordinary everywhere else,
// which is what separates it from [Unemittable].
func FirstLineHazard(op Opcode) (reason string, ok bool) {
	reason, ok = firstLineHazards[op]
	return reason, ok
}

// Unemittable reports whether the backend must never select op, along with the
// reason it must not. Instruction selection consults this so an unemittable
// form cannot reach the output no matter which pattern matched.
//
// This is a stronger condition than Instruction.Deprecated: a deprecated
// instruction assembles and works, whereas these either cannot assemble,
// silently miscompile, or destroy the chip.
func Unemittable(op Opcode) (reason string, ok bool) {
	reason, ok = unemittableOps[op]
	return reason, ok
}
