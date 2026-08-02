package oracle

import (
	"slices"
	"strings"
)

// Authority names the side of a divergence that matches the Stationeers depot manifest named by
// ic10.ManifestID.
type Authority string

const (
	// AuthorityGame means our implementation, transliterated from the decompiled game, is right
	// and the harness is wrong.
	AuthorityGame Authority = "game"
	// AuthorityUnknown means neither side has been checked against the game. Entries with this
	// authority are standing caveats, not established facts.
	AuthorityUnknown Authority = "unknown"
)

// Field names one comparable part of a Result.
type Field string

const (
	FieldRegisters Field = "registers"
	FieldStack     Field = "stack"
	// FieldInstructionPointer is where the chip stopped. For a run cut off by the instruction
	// budget it is the only field that says where the two implementations parted company.
	FieldInstructionPointer Field = "instruction_pointer"
	FieldStatus             Field = "status"
	FieldErrorType          Field = "error_type"
	FieldErrorLine          Field = "error_line"
	FieldInstructions       Field = "instructions"
	FieldTicks              Field = "ticks"
)

// AllFields returns every comparable field, in comparison order.
func AllFields() []Field {
	return []Field{
		FieldRegisters, FieldStack, FieldInstructionPointer, FieldStatus,
		FieldErrorType, FieldErrorLine, FieldInstructions, FieldTicks,
	}
}

// Divergence is one documented disagreement between a harness and the game.
//
// The registry is an allowlist. Compare excuses a mismatch only when some entry covers both the
// harness and the mismatched field, and either has no triggers or has one present in the source.
// Anything else is reported as unexplained and should fail the test that found it.
//
// Fields is deliberately generous for value-level entries: a wrong constant or a silently
// skipped instruction changes control flow, so it can move any part of the result. Only harness
// limitations whose blast radius is provably bounded — an error vocabulary, a missing tick model
// — get a narrow field list, and those are where the allowlist does real work.
type Divergence struct {
	ID string
	// Harnesses are the emulators this entry describes.
	Harnesses []Harness
	Summary   string
	// Observed is what the harness does.
	Observed string
	// Correct is what the game build named by ic10.ManifestID does.
	Correct string
	// Reason is why the harness behaves that way, as far as it has been established.
	Reason    string
	Authority Authority
	// Triggers are the source tokens whose presence makes this divergence reachable. An empty
	// list means the entry applies to every program. A trigger makes the divergence possible
	// rather than certain: many entries bite only for particular operand values.
	Triggers []string
	// Advisory marks an entry that explains nothing. It is attached to a report as context but
	// never excuses a mismatch, so a program it triggers on still has to agree.
	Advisory bool
	// Fields are the parts of a Result this entry may explain. Ignored when Advisory is set.
	Fields []Field
}

// registry is the full set of known divergences. Adding an entry is a claim about the game, so
// each one records how it was established; removing one is a claim that a harness was fixed.
var registry = []Divergence{
	{
		ID:        "ic10emu/const-deg2rad-rad2deg",
		Harnesses: []Harness{IC10Emu},
		Summary:   "deg2rad and rad2deg do not resolve",
		Observed:  "the referencing line faults with UnknownIdentifier",
		Correct:   "0.01745329238474369 and 57.295780181884766, float literals widened to double",
		Reason: "a grammar arm ordering bug: the device and register arms are tried first, so any " +
			"constant whose name begins with d or r is unreachable",
		Authority: AuthorityGame,
		Triggers:  []string{"deg2rad", "rad2deg"},
		Fields:    AllFields(),
	},
	{
		ID:        "ic10emu/const-tau-rgas",
		Harnesses: []Harness{IC10Emu},
		Summary:   "tau and rgas do not resolve",
		Observed:  "the referencing line faults with UnknownIdentifier",
		Correct:   "6.283185307179586 and 8.3144598",
		Reason:    "both are absent from the emulator's constants table",
		Authority: AuthorityGame,
		Triggers:  []string{"tau", "rgas"},
		Fields:    AllFields(),
	},
	{
		ID:        "ic10emu/const-epsilon",
		Harnesses: []Harness{IC10Emu},
		Summary:   "epsilon is the machine epsilon rather than the smallest subnormal",
		Observed:  "2.220446049250313e-16, which is f64::EPSILON",
		Correct:   "double.Epsilon, about 4.94e-324, the smallest positive subnormal",
		Reason:    "the emulator reads the name as a comparison tolerance; the game does not",
		Authority: AuthorityGame,
		Triggers:  []string{"epsilon"},
		Fields:    AllFields(),
	},
	{
		ID:        "ic10emu/nan-minmax",
		Harnesses: []Harness{IC10Emu},
		Summary:   "max and min swallow NaN",
		Observed:  "the non-NaN operand is returned, whichever position the NaN is in",
		Correct:   "NaN propagates",
		Reason:    "Rust's f64::max and f64::min are defined to ignore a NaN operand",
		Authority: AuthorityGame,
		Triggers:  []string{"max", "min"},
		Fields:    AllFields(),
	},
	{
		ID:        "ic10emu/empty-batch-aggregate",
		Harnesses: []Harness{IC10Emu},
		Summary:   "a batch read matching no device answers the wrong value under Sum and Minimum",
		Observed:  "-0 for Sum and positive infinity for Minimum",
		Correct:   "0 for both; Average and Maximum already agree, at NaN and negative infinity",
		Reason: "the fold is seeded with the identity element of the operation rather than with " +
			"the value the game hands back when the batch list is empty",
		Authority: AuthorityGame,
		Triggers:  []string{"Sum", "Minimum"},
		Fields:    AllFields(),
	},
	{
		ID:        "ic10emu/approximate-tolerance-floor",
		Harnesses: []Harness{IC10Emu},
		Summary:   "the approximate comparisons floor their tolerance far higher",
		Observed: "the floor is f64::EPSILON*8, about 1.78e-15, so sapz r0 0.000000000000001 0 " +
			"answers 1",
		Correct: "the floor is the literal 1.1210387714598537e-44 that _SAP_Operation and its " +
			"relatives share, which answers 0 for the same program",
		Reason: "the emulator reads the floor as a machine-epsilon guard rather than as the " +
			"constant the game hard-codes",
		Authority: AuthorityGame,
		Triggers: []string{
			"sap", "sna", "sapz", "snaz",
			"bap", "bna", "bapal", "bnaal", "bapz", "bnaz", "bapzal", "bnazal",
			"brap", "brna", "brapz", "brnaz",
		},
		Fields: AllFields(),
	},
	{
		ID:        "ic10emu/bapz-inverted",
		Harnesses: []Harness{IC10Emu},
		Summary:   "bapz and bapzal branch on the wrong side of the test",
		Observed: "the taken and not-taken arms are swapped: bapz 1000 0 3 branches and " +
			"bapz 0 0 3 falls through",
		Correct:   "the branch is taken when the operand is approximately zero, and not otherwise",
		Reason:    "an upstream defect; the sibling bnaz is not affected, so it is these two arms alone",
		Authority: AuthorityGame,
		Triggers:  []string{"bapz", "bapzal"},
		Fields:    AllFields(),
	},
	{
		ID:        "ic10emu/round-midpoint",
		Harnesses: []Harness{IC10Emu},
		Summary:   "round takes a midpoint away from zero",
		Observed:  "2.5 rounds to 3 and -2.5 to -3",
		Correct:   "banker's rounding, so 2.5 rounds to 2 and -2.5 to -2",
		Reason:    "Rust's f64::round is defined to round a half away from zero; C#'s Math.Round is not",
		Authority: AuthorityGame,
		Triggers:  []string{"round"},
		Fields:    AllFields(),
	},
	{
		ID:        "ic10emu/unimplemented-instructions",
		Harnesses: []Harness{IC10Emu},
		Summary:   "ext, ins, rol, ror, lerp, clamp, pow, sgn, bdnvl and bdnvs are not implemented",
		Observed: "the line fails to parse, is reported in CompileErrors, and then executes as a " +
			"nop that still costs an instruction",
		Correct: "all ten are implemented",
		Reason: "never added to the emulator's instruction set; the list is what its opcode enum " +
			"is missing, not what a run happened to trip over",
		Authority: AuthorityGame,
		Triggers: []string{
			"ext", "ins", "rol", "ror", "lerp", "clamp", "pow", "sgn", "bdnvl", "bdnvs",
		},
		Fields: AllFields(),
	},
	{
		ID:        "ic10emu/getd-putd-db",
		Harnesses: []Harness{IC10Emu},
		Summary:   "getd and putd reject db",
		Observed:  "IncorrectOperandType, because db is not accepted where a ReferenceId is expected",
		Correct:   "both address the housing, which forwards to the socketed chip's memory",
		Reason: "our db-forwarding patch covers get, put and clr only; the deprecated d-suffixed " +
			"forms take a different operand path",
		Authority: AuthorityGame,
		Triggers:  []string{"getd", "putd"},
		Fields:    AllFields(),
	},
	{
		ID:        "ic10emu/sleep",
		Harnesses: []Harness{IC10Emu},
		Summary:   "sleep is wall-clock, not tick-based",
		Observed: "the harness stops the run at the sleep and reports status \"sleep\" rather than " +
			"blocking on OffsetDateTime::now for real",
		Correct: "sleep re-enters itself once per tick until the duration elapses; on line 0 it " +
			"returns -0, fails the negative test that ends the tick, and burns the full budget",
		Reason:    "the emulator has no tick clock to schedule against",
		Authority: AuthorityGame,
		Triggers:  []string{"sleep"},
		Fields:    AllFields(),
	},
	{
		ID:        "ic10emu/hcf",
		Harnesses: []Harness{IC10Emu},
		Summary:   "hcf ends the program instead of destroying the chip",
		Observed:  "status \"ended\" with the chip intact",
		Correct:   "the chip is destroyed and a fire starts",
		Reason:    "the emulator has no damage model for the housing",
		Authority: AuthorityGame,
		Triggers:  []string{"hcf"},
		Fields:    AllFields(),
	},
	{
		ID:        "npm/error-model",
		Harnesses: []Harness{NPM},
		Summary:   "error classification is not comparable",
		Observed: "five error codes in total (TYPE_ERROR, RUNTIME_ERROR, FATAL_ERROR, " +
			"ARGUMENT_ERROR and one more), and parse failures surface as runtime errors part way " +
			"through a run rather than blocking execution",
		Correct: "distinct exception types per fault, and a compile error blocks the program " +
			"entirely until the source is edited",
		Reason:    "the package models errors for an editor, not for a faithful chip",
		Authority: AuthorityGame,
		Fields:    []Field{FieldStatus, FieldErrorType, FieldErrorLine},
	},
	{
		ID:        "npm/no-tick-model",
		Harnesses: []Harness{NPM},
		Summary:   "there is no tick model",
		Observed:  "Ticks is always zero and yield is an empty function",
		Correct:   "128 instructions per tick, with yield ending the tick early",
		Reason:    "the package runs programs to completion with no scheduler",
		Authority: AuthorityGame,
		Fields:    []Field{FieldTicks},
	},
	{
		ID:        "npm/instruction-count",
		Harnesses: []Harness{NPM},
		Summary:   "one instruction too many is reported",
		Observed:  "the step that terminates the program is counted as retired",
		Correct:   "only retired instructions count",
		Reason:    "an artefact of the package's step loop, which this harness cannot see past",
		Authority: AuthorityGame,
		Fields:    []Field{FieldInstructions},
	},
	{
		ID:        "npm/const-rgas",
		Harnesses: []Harness{NPM},
		Summary:   "rgas carries the CODATA 2018 value",
		Observed:  "8.31446261815324",
		Correct:   "8.3144598",
		Reason:    "the package uses the current physical constant; the game froze an older one",
		Authority: AuthorityGame,
		Triggers:  []string{"rgas"},
		Fields:    AllFields(),
	},
	{
		ID:        "npm/const-ninf",
		Harnesses: []Harness{NPM},
		Summary:   "ninf has the wrong sign",
		Observed:  "+Inf",
		Correct:   "-Inf",
		Reason:    "not established; the constants table appears to map both infinities to +Inf",
		Authority: AuthorityGame,
		Triggers:  []string{"ninf"},
		Fields:    AllFields(),
	},
	{
		ID:        "npm/const-nan",
		Harnesses: []Harness{NPM},
		Summary:   "nan is not accepted as an operand",
		Observed:  "TYPE_ERROR on the referencing line",
		Correct:   "a NaN literal",
		Reason:    "the operand validator does not list nan among the constants",
		Authority: AuthorityGame,
		Triggers:  []string{"nan"},
		Fields:    AllFields(),
	},
	{
		ID:        "npm/sp-ra",
		Harnesses: []Harness{NPM},
		Summary:   "sp and ra are not usable as registers",
		Observed: "a write faults with TYPE_ERROR and lands on r0 instead; a read yields the " +
			"register's own index, 16 or 17, rather than its contents",
		Correct:   "both are ordinary registers, freely readable and writable",
		Reason:    "the package resolves the two names to their index as a literal rather than to a register",
		Authority: AuthorityGame,
		Triggers:  []string{"sp", "ra"},
		Fields:    AllFields(),
	},
	{
		ID:        "npm/link-register",
		Harnesses: []Harness{NPM},
		Summary:   "the link forms do not write ra",
		Observed:  "ra is left at its previous value when the branch is taken",
		Correct:   "ra holds the line after the branch, which is what makes jal and the al forms callable",
		Reason:    "the package implements the link forms as plain branches",
		Authority: AuthorityGame,
		Triggers: []string{
			"jal", "bltzal", "bgezal", "blezal", "bgtzal", "beqal", "bneal", "bdseal", "bdnsal",
			"bltal", "bgtal", "bleal", "bgeal", "bapal", "bnaal", "beqzal", "bnezal", "bapzal",
			"bnazal",
		},
		Fields: AllFields(),
	},
	{
		ID:        "npm/device-presence-tests",
		Harnesses: []Harness{NPM},
		Summary:   "the device presence tests answer correctly and then fault",
		Observed: "the right value is stored or the right branch taken, and the program then stops " +
			"with TYPE_ERROR device_pin_not_connected",
		Correct:   "an unconnected pin is what these instructions exist to report, so the run continues",
		Reason:    "the package validates the pin operand after the instruction has already used it",
		Authority: AuthorityGame,
		Triggers: []string{
			"sdse", "sdns", "bdse", "bdns", "brdse", "brdns", "bdseal", "bdnsal",
		},
		Fields: AllFields(),
	},
	{
		ID:        "npm/define-zero",
		Harnesses: []Harness{NPM},
		Summary:   "a define of zero is rejected",
		Observed:  "TYPE_ERROR value_must_be_number on the define, and then on every line reading it",
		Correct:   "zero is an ordinary compile time constant",
		Reason:    "the package tests the value for truthiness where it means to test it for a number",
		Authority: AuthorityGame,
		Triggers:  []string{"define"},
		Fields:    AllFields(),
	},
	{
		ID:        "npm/unimplemented-instructions",
		Harnesses: []Harness{NPM},
		Summary:   "rol, ror and clamp are not implemented",
		Observed:  "unknown_instruction on the referencing line",
		Correct:   "all three are implemented",
		Reason:    "never added to the package's instruction set",
		Authority: AuthorityGame,
		Triggers:  []string{"rol", "ror", "clamp"},
		Fields:    AllFields(),
	},
	{
		ID:        "npm/getd-putd-db",
		Harnesses: []Harness{NPM},
		Summary:   "getd and putd reject db",
		Observed:  "invalid_argument_device_id followed by device_not_found",
		Correct:   "both address the housing, which forwards to the socketed chip's memory",
		Reason:    "the sandbox housing exposes no device id that these forms accept",
		Authority: AuthorityGame,
		Triggers:  []string{"getd", "putd"},
		Fields:    AllFields(),
	},
	{
		ID:        "npm/sleep",
		Harnesses: []Harness{NPM},
		Summary:   "sleep blocks the harness",
		Observed: "a whole-second sleep never resolves inside a single step, so the harness " +
			"abandons the run and reports status \"harness_timeout\"; sub-second sleeps really " +
			"do sleep",
		Correct:   "sleep re-enters itself once per tick until the duration elapses",
		Reason:    "the package sleeps on wall-clock time and has no tick clock",
		Authority: AuthorityGame,
		Triggers:  []string{"sleep"},
		Fields:    AllFields(),
	},
	{
		ID:        "npm/hcf",
		Harnesses: []Harness{NPM},
		Summary:   "hcf is a runtime error instead of destroying the chip",
		Observed:  "RUNTIME_ERROR with the chip intact",
		Correct:   "the chip is destroyed and a fire starts",
		Reason:    "the package has no damage model for the housing",
		Authority: AuthorityGame,
		Triggers:  []string{"hcf"},
		Fields:    AllFields(),
	},
	{
		ID:        "game-data-vintage",
		Harnesses: []Harness{IC10Emu, NPM},
		Summary:   "logic type and slot type tables come from a different game build than ours",
		Observed:  "enum names and integer values from a 2024 game-data extract",
		Correct:   "the tables extracted from the manifest ic10.ManifestID names",
		Reason: "neither harness regenerates its data against our build, so any comparison " +
			"involving a LogicType, LogicSlotType, batch method or reagent mode may disagree for " +
			"reasons that are not implementation bugs on either side. Advisory rather than " +
			"excusing: no specific value is known to differ, and silently forgiving every logic " +
			"instruction would gut the allowlist",
		Authority: AuthorityUnknown,
		Advisory:  true,
		Triggers: []string{
			"l", "lb", "lbn", "lbns", "lbs", "lr", "ls", "ld",
			"s", "sb", "sbn", "sbs", "ss", "sd", "getd", "putd", "rmap",
		},
	},
}

// Registry returns every known divergence.
func Registry() []Divergence {
	return slices.Clone(registry)
}

// lookup returns the entry with the given ID.
func lookup(id string) (Divergence, bool) {
	for _, d := range registry {
		if d.ID == id {
			return d, true
		}
	}
	return Divergence{}, false
}

// Reachable returns the entries for h that source could hit: those with no triggers, plus those
// with a trigger token present in the program.
func Reachable(h Harness, source string) []Divergence {
	present := sourceTokens(source)
	var out []Divergence
	for _, d := range registry {
		if !slices.Contains(d.Harnesses, h) {
			continue
		}
		if len(d.Triggers) == 0 {
			out = append(out, d)
			continue
		}
		for _, trigger := range d.Triggers {
			if present[trigger] {
				out = append(out, d)
				break
			}
		}
	}
	return out
}

// sourceTokens splits IC10 source into the bare words a trigger can match. Comments are dropped;
// device-with-connection operands such as d0:1 contribute both halves.
func sourceTokens(source string) map[string]bool {
	out := make(map[string]bool)
	for line := range strings.SplitSeq(source, "\n") {
		if cut := strings.IndexByte(line, '#'); cut >= 0 {
			line = line[:cut]
		}
		for field := range strings.FieldsSeq(line) {
			for part := range strings.SplitSeq(field, ":") {
				if part != "" {
					out[part] = true
				}
			}
		}
	}
	return out
}
