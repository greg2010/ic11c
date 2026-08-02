package difftest

import "github.com/greg2010/ic11c/internal/oracle"

// Divergence is a disagreement between internal/vm and a harness that the
// oracle divergence registry does not cover.
//
// Each entry is why some construct is kept out of the generators. The registry
// is closed by construction, so an uncovered mismatch fails the suite; a
// generator that produced one of these would fail on every run without telling
// anyone anything new. Registering the entry, or fixing the harness, is what
// lets the construct back in.
//
// This is a record, not a second registry: nothing here excuses anything. It
// exists so that a run can say what it is not comparing.
type Divergence struct {
	Harness oracle.Harness
	// Summary names the construct.
	Summary string
	// Source is the smallest program that shows the difference.
	Source string
	// Ours is what internal/vm reports.
	Ours string
	// Theirs is what the harness reports.
	Theirs string
}

// unregisteredDivergences is everything the generators work around.
//
// What is left here after a finding has been judged is what the divergence
// registry cannot express. Its triggers are exact source tokens, so it can name
// a mnemonic or a constant but not a construct: a literal in exponent notation,
// an indirect operand of unbounded prefix depth, a duplicated name, an operand
// out of range. An entry for one of those would have to trigger on a mnemonic
// the generators emit constantly, or on nothing at all, and would then excuse
// far more than it describes.
//
// They are recorded rather than dropped, because each one is a finding in its
// own right.
var unregisteredDivergences = []Divergence{
	{
		Harness: oracle.IC10Emu,
		Summary: "a number in exponent notation does not parse",
		Source:  "move r0 1e30",
		Ours:    "1e30",
		Theirs:  "a compile error, and then a nop that still costs an instruction",
	},
	{
		Harness: oracle.IC10Emu,
		Summary: "poke reports an out of range address differently from push and put",
		Source:  "poke 600 1",
		Ours:    "MemoryError.StackOverflow, the chip's own stack fault types",
		Theirs:  "StackIndexOutOfRange, which it uses for both directions",
	},
	{
		Harness: oracle.IC10Emu,
		Summary: "get reports an out of range address differently from put",
		Source:  "get r0 db 600",
		Ours:    "ic11c.Unknown, because the game's get does not catch the memory fault and the tick loop reports it as a host exception",
		Theirs:  "MemoryError.StackOverflow, the same type its put reports",
	},
	{
		Harness: oracle.IC10Emu,
		Summary: "a negative indirect register index does not fault",
		Source:  "move r0 -1\nmove rr0 1",
		Ours:    "RegisterIndexOutOfRange",
		Theirs:  "no fault; the write lands on r0",
	},
	{
		Harness: oracle.IC10Emu,
		Summary: "a device index past the pins does not fault",
		Source:  "move r0 7\nsdse r1 dr0",
		Ours:    "ic11c.Unknown, from the game indexing the pin array before validating",
		Theirs:  "no fault; the result is zero",
	},
	{
		Harness: oracle.IC10Emu,
		Summary: "a shift distance out of int range has its own error variant",
		Source:  "div r0 1 0\nsll r1 1 r0",
		Ours:    "ShiftOverflowI64, because one chip exception covers both widths",
		Theirs:  "ShiftOverflowI32, a separate variant for the narrower conversion",
	},
	{
		Harness: oracle.IC10Emu,
		Summary: "a negative jump target is an endless loop rather than the end of the program",
		Source:  "j -5",
		Ours:    "the program ends, because a line number outside the program is how a run stops and a negative one is outside it",
		Theirs:  "the program loops until the instruction budget runs out",
	},
	{
		Harness: oracle.IC10Emu,
		Summary: "a duplicate define is a run time fault rather than a compile error",
		Source:  "define v0 1\ndefine v0 2",
		Ours:    "compile_error DuplicateDefine, with no instruction retired",
		Theirs:  "a run time DuplicateDefine on the second line, with two instructions retired",
	},
	{
		Harness: oracle.IC10Emu,
		Summary: "a duplicate label does not stop the program",
		Source:  "t0:\nt0:",
		Ours:    "compile_error DuplicateLabel, with no instruction retired",
		Theirs:  "a compile error the program then runs past, retiring both lines as nops",
	},
	{
		Harness: oracle.IC10Emu,
		Summary: "a wrong operand count is a run time fault rather than a compile error",
		Source:  "add r0 1",
		Ours:    "compile_error ic11c.IncorrectArgumentCount, with no instruction retired",
		Theirs:  "a run time TooFewOperands, with the line retired",
	},
	{
		Harness: oracle.NPM,
		Summary: "negative zero does not survive a register",
		Source:  "mul r0 -1 0\nmove r1 r0",
		Ours:    "both registers hold -0",
		Theirs:  "r0 holds -0 and r1 holds +0; the literal -0 is also read as +0",
	},
}
