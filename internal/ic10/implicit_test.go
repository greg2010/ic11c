package ic10_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
)

// implicitReach is one instruction and the registers it reaches without naming
// them, spelled as `register:direction` pairs in the order the table holds them.
type implicitReach struct {
	mnemonic string
	uses     []string
}

// implicitReaches is every instruction that reaches a register no operand
// of its can name. jal replaces ra unconditionally; the rest of the al
// family assign it only where they branch, so the prior value survives and
// is read-write. push and pop move sp and read it in the same instruction;
// peek reads it and leaves it alone. bapzal and bnazal are included even
// though the backend refuses to emit them for an unrelated parse defect.
var implicitReaches = []implicitReach{
	{mnemonic: "jal", uses: []string{"ra:write"}},
	{mnemonic: "bltzal", uses: []string{"ra:readwrite"}},
	{mnemonic: "bgezal", uses: []string{"ra:readwrite"}},
	{mnemonic: "blezal", uses: []string{"ra:readwrite"}},
	{mnemonic: "bgtzal", uses: []string{"ra:readwrite"}},
	{mnemonic: "beqal", uses: []string{"ra:readwrite"}},
	{mnemonic: "bneal", uses: []string{"ra:readwrite"}},
	{mnemonic: "bdseal", uses: []string{"ra:readwrite"}},
	{mnemonic: "bdnsal", uses: []string{"ra:readwrite"}},
	{mnemonic: "bltal", uses: []string{"ra:readwrite"}},
	{mnemonic: "bgtal", uses: []string{"ra:readwrite"}},
	{mnemonic: "bleal", uses: []string{"ra:readwrite"}},
	{mnemonic: "bgeal", uses: []string{"ra:readwrite"}},
	{mnemonic: "bapal", uses: []string{"ra:readwrite"}},
	{mnemonic: "bnaal", uses: []string{"ra:readwrite"}},
	{mnemonic: "beqzal", uses: []string{"ra:readwrite"}},
	{mnemonic: "bnezal", uses: []string{"ra:readwrite"}},
	{mnemonic: "bapzal", uses: []string{"ra:readwrite"}},
	{mnemonic: "bnazal", uses: []string{"ra:readwrite"}},
	{mnemonic: "push", uses: []string{"sp:readwrite"}},
	{mnemonic: "pop", uses: []string{"sp:readwrite"}},
	{mnemonic: "peek", uses: []string{"sp:read"}},
}

// TestImplicitReachesAreTheWholeOfWhatTheTableRecords holds every named
// instruction to what the table says and holds the table to naming no others.
func TestImplicitReachesAreTheWholeOfWhatTheTableRecords(t *testing.T) {
	named := make(map[string][]string, len(implicitReaches))
	for _, want := range implicitReaches {
		named[want.mnemonic] = want.uses
	}
	for _, instruction := range ic10.Instructions {
		want, isNamed := named[instruction.Mnemonic]
		got := spellImplicit(instruction)
		switch {
		case !isNamed && len(got) > 0:
			t.Errorf("%s reaches %v, and this names no register for it", instruction.Mnemonic, got)
		case isNamed && !slices.Equal(got, want):
			t.Errorf("%s reaches %v, want %v", instruction.Mnemonic, got, want)
		}
		delete(named, instruction.Mnemonic)
	}
	for mnemonic := range named {
		t.Errorf("the machine has no %s, which this names", mnemonic)
	}
}

// TestWriteIndexAnswersNoneForAnImplicitWrite covers what a -1 from
// WriteIndex means for a linking form: all nineteen assign ra unnamed, so
// a caller treating -1 as "defines nothing" loses the assignment on every
// one. pop and peek are excluded — their own operand is the assignment
// WriteIndex already names.
func TestWriteIndexAnswersNoneForAnImplicitWrite(t *testing.T) {
	links := 0
	for _, instruction := range ic10.Instructions {
		if !instruction.WritesImplicitly(ic10.RegRA) {
			continue
		}
		links++
		index, err := instruction.WriteIndex()
		if err != nil {
			t.Errorf("%s: WriteIndex: %v", instruction.Mnemonic, err)
			continue
		}
		if index != -1 {
			t.Errorf("%s assigns operand %d, and this expects the assignment to be unnamed", instruction.Mnemonic, index)
		}
	}
	if want := 19; links != want {
		t.Errorf("%d instructions assign ra unnamed, want %d", links, want)
	}
}

// TestWritesImplicitlyReadsTheDirection covers the one thing the accessor adds
// over the field: a register the instruction only reads is not one it assigns.
func TestWritesImplicitlyReadsTheDirection(t *testing.T) {
	tests := []struct {
		mnemonic string
		register ic10.Register
		want     bool
	}{
		{mnemonic: "jal", register: ic10.RegRA, want: true},
		{mnemonic: "jal", register: ic10.RegSP},
		{mnemonic: "bgeal", register: ic10.RegRA, want: true},
		{mnemonic: "push", register: ic10.RegSP, want: true},
		{mnemonic: "pop", register: ic10.RegSP, want: true},
		{mnemonic: "peek", register: ic10.RegSP},
		{mnemonic: "peek", register: ic10.RegRA},
		{mnemonic: "j", register: ic10.RegRA},
		{mnemonic: "add", register: ic10.RegRA},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s %s", tt.mnemonic, tt.register), func(t *testing.T) {
			instruction, known := ic10.LookupInstruction(tt.mnemonic)
			if !known {
				t.Fatalf("the machine has no %s", tt.mnemonic)
			}
			if got := instruction.WritesImplicitly(tt.register); got != tt.want {
				t.Errorf("%s assigns %s unnamed = %v, want %v", tt.mnemonic, tt.register, got, tt.want)
			}
		})
	}
}

// spellImplicit renders what an instruction reaches without naming it, in the
// order the table holds it.
func spellImplicit(instruction ic10.Instruction) []string {
	uses := make([]string, 0, len(instruction.Implicit))
	for _, use := range instruction.Implicit {
		uses = append(uses, fmt.Sprintf("%s:%s", use.Register, use.Direction))
	}
	return uses
}

// TestLinksReturnCoversTheTable holds LinksReturn to a naming convention
// derived independently of the table it wraps: a jump or branch mnemonic
// ending in al writes ra and returns, every other form leaves it alone. An
// extraction bug that stopped recording the write would agree with itself
// everywhere except here. bapzal and bnazal are included even though
// unemittable, since this expectation has no reason to consult that table.
func TestLinksReturnCoversTheTable(t *testing.T) {
	for _, instruction := range ic10.Instructions {
		name := instruction.Mnemonic
		transfers := strings.HasPrefix(name, "j") || strings.HasPrefix(name, "b")
		want := transfers && strings.HasSuffix(name, "al")
		if got := ic10.LinksReturn(instruction.Opcode); got != want {
			t.Errorf("LinksReturn(%s) = %v, want %v", name, got, want)
		}
	}
}

// TestLinksReturnAnswersNoForAnOpcodeOutsideTheTable covers the answer a caller
// gets for an opcode the table does not describe, which is what a program built
// outside this package can hold.
func TestLinksReturnAnswersNoForAnOpcodeOutsideTheTable(t *testing.T) {
	if ic10.LinksReturn(ic10.Opcode(len(ic10.Instructions))) {
		t.Error("an opcode past the table links a return")
	}
}
