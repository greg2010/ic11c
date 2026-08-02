package vm

import (
	"strconv"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
)

var parseCases = []instructionCase{
	{
		name: "alias takes effect when its line runs", op: ic10.OpAlias,
		source:        "alias counter r1\nmove counter 7",
		wantRegisters: map[ic10.Register]float64{1: 7},
	},
	{
		name: "an alias to a device pin addresses that pin", op: ic10.OpAlias,
		devices:       []Device{pump(101, 5)},
		source:        "alias inlet d0\nl r0 inlet Setting",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		// define is registered while compiling, so it is visible to lines above
		// the one that declares it. An alias is not.
		name: "define is visible before its own line", op: ic10.OpDefine,
		source:        "move r0 limit\ndefine limit 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "a duplicate define is a compile error", op: ic10.OpDefine,
		source:           "define x 1\ndefine x 2",
		wantCompileError: &Fault{Type: ExcExtraDefine, Line: 1},
	},
	{
		// label is deprecated and still works. Its operands are the other way
		// round from alias.
		name: "label names a device pin", op: ic10.OpLabel,
		devices:       []Device{pump(101, 5)},
		source:        "label d0 inlet\nl r0 inlet Setting",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "a hex literal is preprocessed to decimal", op: ic10.OpMove,
		source:        "move r0 $FF\nmove r1 $ff_ff",
		wantRegisters: map[ic10.Register]float64{0: 255, 1: 65535},
	},
	{
		name: "a binary literal is preprocessed to decimal", op: ic10.OpMove,
		source:        "move r0 %1010\nmove r1 %1111_0000",
		wantRegisters: map[ic10.Register]float64{0: 10, 1: 240},
	},
	{
		name: "a packed string literal is preprocessed to decimal", op: ic10.OpMove,
		source:        `move r0 STR("abc")`,
		wantRegisters: map[ic10.Register]float64{0: 0x616263},
	},
	{
		name: "a comment is stripped before tokenising", op: ic10.OpMove,
		source:        "move r0 5 # set the limit\n# a whole line of comment",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "a wrong operand count is a compile error", op: ic10.OpAdd,
		source:           "add r0 1",
		wantCompileError: &Fault{Type: ExcIncorrectArgumentCount, Line: 0},
	},
	{
		// The token count is checked against 3 and the target is then read from
		// position 3, so three operands are an arity error.
		name: "brapz cannot be assembled with three operands", op: ic10.OpBrapz,
		source:           "brapz r0 1 2",
		wantCompileError: &Fault{Type: ExcIncorrectArgumentCount, Line: 0},
	},
	{
		// Two operands pass the count check and then run off the end of the
		// line, which the chip reports as an unknown error.
		name: "brapz cannot be assembled with two operands either", op: ic10.OpBrapz,
		source:           "brapz r0 1",
		wantCompileError: &Fault{Type: ExcUnknown, Line: 0},
	},
	{
		name: "brnaz cannot be assembled", op: ic10.OpBrnaz,
		source:           "brnaz r0 1",
		wantCompileError: &Fault{Type: ExcUnknown, Line: 0},
	},
	{
		name: "bapzal cannot be assembled", op: ic10.OpBapzal,
		source:           "bapzal r0 1",
		wantCompileError: &Fault{Type: ExcUnknown, Line: 0},
	},
	{
		name: "bnazal cannot be assembled", op: ic10.OpBnazal,
		source:           "bnazal r0 1",
		wantCompileError: &Fault{Type: ExcUnknown, Line: 0},
	},
	{
		// A register name that is nothing but the prefix letter runs the operand
		// scanner off the end of the string.
		name: "a bare register prefix is a compile error with no useful type", op: ic10.OpMove,
		source:           "move r 1",
		wantCompileError: &Fault{Type: ExcUnknown, Line: 0},
	},
	{
		// An impossible register compiles cleanly and faults at run time, which
		// is what makes operand validation the compiler's responsibility.
		name: "an out of range destination register faults at run time", op: ic10.OpMove,
		source:    "move r99 1",
		wantFault: &Fault{Type: ExcOutOfRegisterBounds, Line: 0},
	},
	{
		// The same text as a source operand takes a path with no bounds check
		// after the loop, so it lands on the general clause instead.
		name: "an out of range source register is an unknown error", op: ic10.OpMove,
		source:    "move r0 r99",
		wantFault: &Fault{Type: ExcUnknown, Line: 0},
	},
	{
		name: "an indirect register reference reads its index from a register", op: ic10.OpMove,
		registers:     map[ic10.Register]float64{0: 3},
		source:        "move rr0 7",
		wantRegisters: map[ic10.Register]float64{3: 7},
	},
	{
		// Indirect referencing bounds against the whole file, so it reaches sp
		// and ra. Published documentation says it cannot.
		name: "an indirect register reference can reach sp", op: ic10.OpMove,
		registers:     map[ic10.Register]float64{0: 16},
		source:        "move rr0 7",
		wantRegisters: map[ic10.Register]float64{ic10.RegSP: 7},
	},
	{
		name: "an indirect index outside the file faults", op: ic10.OpMove,
		registers: map[ic10.Register]float64{0: 99},
		source:    "move rr0 7",
		wantFault: &Fault{Type: ExcOutOfRegisterBounds, Line: 0},
	},
	{
		// The first hop indexes the register file before checking, so an
		// impossible starting index is an unknown error where an impossible
		// resolved index is a register bounds fault.
		name: "an indirect reference from an impossible index is an unknown error", op: ic10.OpMove,
		source:    "move rr99 7",
		wantFault: &Fault{Type: ExcUnknown, Line: 0},
	},
	{
		// sp and ra are ordinary registers with no protection.
		name: "sp and ra are writable by name", op: ic10.OpMove,
		source:        "move sp 5\nmove ra 6",
		wantRegisters: map[ic10.Register]float64{ic10.RegSP: 5, ic10.RegRA: 6},
	},
	{
		name: "yield ends the tick and resumes at the following line", op: ic10.OpYield,
		source:        "yield\nadd r0 r0 1",
		ticks:         2,
		wantRegisters: map[ic10.Register]float64{0: 1},
	},
	{
		// sleep returns -index where yield returns -index-1, so on line zero the
		// negative test that ends the tick fails and the whole budget goes.
		name: "sleep on line zero consumes the whole budget", op: ic10.OpSleep,
		source:       "sleep 1\nadd r0 r0 1",
		budget:       8,
		wantExecuted: new(8),
		wantPC:       new(0),
	},
	{
		name: "hcf destroys the chip rather than trapping", op: ic10.OpHcf,
		source:    "hcf",
		wantFault: &Fault{Type: ExcChipCatchingFire, Line: 0},
	},
}

// TestUnknownMnemonicIsACompileError covers the one compile check that has no
// opcode of its own.
func TestUnknownMnemonicIsACompileError(t *testing.T) {
	m := NewMachine()
	err := m.Load(t.Context(), "frobnicate r0")
	checkFault(t, "compile error", err, &Fault{Type: ExcUnrecognisedInstruction, Line: 0})
	if m.CompileError() == nil {
		t.Error("CompileError is nil after a failed Load")
	}
}

// TestDuplicateLabelIsACompileError covers the label check, which is not an
// instruction.
func TestDuplicateLabelIsACompileError(t *testing.T) {
	m := NewMachine()
	err := m.Load(t.Context(), "start:\nadd r0 r0 1\nstart:")
	checkFault(t, "compile error", err, &Fault{Type: ExcJumpTagDuplicate, Line: 2})
}

// TestCompileErrorBlocksExecution separates a compile error, which stops the
// chip entirely, from a run time fault, which is retried every tick.
func TestCompileErrorBlocksExecution(t *testing.T) {
	m := NewMachine()
	if err := m.Load(t.Context(), "add r0 r0 1\nadd r0 1"); err == nil {
		t.Fatal("Load accepted a line with the wrong operand count")
	}
	executed, err := m.Tick(t.Context(), InstructionsPerTick)
	if executed != 0 {
		t.Errorf("executed = %d, want 0 while a compile error is set", executed)
	}
	checkFault(t, "tick", err, &Fault{Type: ExcIncorrectArgumentCount, Line: 1})
	if got := m.Register(0); got != 0 {
		t.Errorf("r0 = %v, want 0: no line should have run", got)
	}
}

// TestCompileErrorKeepsTheLinesBeforeIt records that compilation stops at the
// offending line rather than discarding the whole program.
func TestCompileErrorKeepsTheLinesBeforeIt(t *testing.T) {
	m := NewMachine()
	if err := m.Load(t.Context(), "move r0 1\nmove r1 2\nadd r0 1"); err == nil {
		t.Fatal("Load accepted a line with the wrong operand count")
	}
	if got, want := m.LineCount(), 2; got != want {
		t.Errorf("LineCount = %d, want %d", got, want)
	}
}

// TestHashPreprocessorMatchesTheNameHash pins HASH("...") to the hash the rest
// of the package uses, so the two cannot drift.
func TestHashPreprocessorMatchesTheNameHash(t *testing.T) {
	m := NewMachine()
	if err := m.Load(t.Context(), `move r0 HASH("StructureActiveVent")`); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := m.Tick(t.Context(), InstructionsPerTick); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got, want := m.Register(0), float64(hashName("StructureActiveVent")); got != want {
		t.Errorf("r0 = %v, want %v", got, want)
	}
}

// TestNumberParsingFollowsTheGamesStyle covers the places the chip's number
// syntax is narrower or wider than Go's.
func TestNumberParsingFollowsTheGamesStyle(t *testing.T) {
	cases := []struct {
		text  string
		value float64
		ok    bool
	}{
		{text: "42", value: 42, ok: true},
		{text: "-42", value: -42, ok: true},
		{text: "42-", value: -42, ok: true},
		{text: "1.5", value: 1.5, ok: true},
		{text: ".5", value: 0.5, ok: true},
		{text: "1,000", value: 1000, ok: true},
		// The game's number style does not allow an exponent, so this is not a
		// number to the chip's assembler.
		{text: "1e5"},
		{text: "0x10"},
		{text: "Inf"},
		{text: "NaN"},
		{text: ""},
		{text: "1.2.3"},
	}
	for _, c := range cases {
		got, ok := tryParseDouble(c.text)
		if ok != c.ok {
			t.Errorf("tryParseDouble(%q) ok = %v, want %v", c.text, ok, c.ok)
			continue
		}
		if ok && got != c.value {
			t.Errorf("tryParseDouble(%q) = %v, want %v", c.text, got, c.value)
		}
	}
}

// TestPackAscii6RoundTrips covers the STR("...") preprocessor form and its
// inverse.
func TestPackAscii6RoundTrips(t *testing.T) {
	cases := []struct {
		text string
		want string
		err  ExceptionType
	}{
		{text: "a", want: "a"},
		{text: "abcdef", want: "abcdef"},
		{text: "abcdefg", err: ExcInvalidStringLength},
		{text: "", err: ExcInvalidStringNull},
		{text: "é", err: ExcInvalidStringNonASCII},
	}
	for _, c := range cases {
		packed, err := packASCII6(c.text, 0)
		if c.err != ExcNone {
			checkFault(t, "packASCII6("+strconv.Quote(c.text)+")", err, &Fault{Type: c.err, Line: 0})
			continue
		}
		if err != nil {
			t.Errorf("packASCII6(%q): %v", c.text, err)
			continue
		}
		if got := unpackASCII6(packed, false); got != c.want {
			t.Errorf("unpackASCII6(packASCII6(%q)) = %q, want %q", c.text, got, c.want)
		}
	}
}
