package vm

import (
	"math"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
)

// instructionCases is the whole per-instruction corpus. The coverage test reads
// it to check the declared unit column against what is actually exercised, so
// every case must name the opcode it covers.
var instructionCases = concatCases(
	arithmeticCases,
	bitwiseCases,
	comparisonCases,
	branchCases,
	memoryCases,
	deviceCases,
	parseCases,
)

func concatCases(groups ...[]instructionCase) []instructionCase {
	var all []instructionCase
	for _, group := range groups {
		all = append(all, group...)
	}
	return all
}

func TestInstructions(t *testing.T) {
	seen := make(map[string]bool, len(instructionCases))
	for _, c := range instructionCases {
		if seen[c.name] {
			t.Fatalf("duplicate case name %q", c.name)
		}
		seen[c.name] = true
		t.Run(c.name, func(t *testing.T) {
			runInstructionCase(t, c)
		})
	}
}

// TestOverrunIsNotAnError covers the tick boundary. The budget is spent in the
// loop condition, so exhausting it returns normally and the next tick resumes
// at the same instruction.
func TestOverrunIsNotAnError(t *testing.T) {
	m := NewMachine()
	// Two instructions per iteration, so a full budget is 64 increments.
	if err := m.Load(t.Context(), "add r0 r0 1\nj 0"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	executed, err := m.Tick(t.Context(), InstructionsPerTick)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if executed != InstructionsPerTick {
		t.Errorf("executed = %d, want %d", executed, InstructionsPerTick)
	}
	if got, want := m.Register(0), 64.0; got != want {
		t.Errorf("r0 = %v, want %v", got, want)
	}
	if m.PC() != 0 {
		t.Errorf("PC = %d, want 0", m.PC())
	}
	if m.Fault() != nil {
		t.Errorf("Fault = %v, want none", m.Fault())
	}

	executed, err = m.Tick(t.Context(), InstructionsPerTick)
	if err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	if executed != InstructionsPerTick {
		t.Errorf("second tick executed = %d, want %d", executed, InstructionsPerTick)
	}
	if got, want := m.Register(0), 128.0; got != want {
		t.Errorf("r0 after two ticks = %v, want %v", got, want)
	}
}

// TestExactlyOneHundredAndTwentyEightInstructions pins the boundary itself: the
// last instruction of the budget runs and the one after it does not.
func TestExactlyOneHundredAndTwentyEightInstructions(t *testing.T) {
	m := NewMachine()
	if err := m.Load(t.Context(), "add r0 r0 1\nj 0"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, budget := range []int{127, 128, 129} {
		m.SetRegisters([ic10.NumRegisters]float64{})
		m.SetPC(0)
		executed, err := m.Tick(t.Context(), budget)
		if err != nil {
			t.Fatalf("Tick(%d): %v", budget, err)
		}
		if executed != budget {
			t.Errorf("budget %d: executed = %d", budget, executed)
		}
		want := float64((budget + 1) / 2)
		if got := m.Register(0); got != want {
			t.Errorf("budget %d: r0 = %v, want %v", budget, got, want)
		}
	}
}

// TestYieldAndSleepDifferOnlyInTheirReturn shows why one resumes at the next
// line and the other re-enters itself.
func TestYieldAndSleepDifferOnlyInTheirReturn(t *testing.T) {
	m := NewMachine()
	if err := m.Load(t.Context(), "add r0 r0 1\nyield\nadd r1 r1 1"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	executed, err := m.Tick(t.Context(), InstructionsPerTick)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if executed != 2 {
		t.Errorf("executed = %d, want 2: yield ends the tick", executed)
	}
	if m.PC() != 2 {
		t.Errorf("PC = %d, want 2: yield resumes at the following line", m.PC())
	}
	if _, err := m.Tick(t.Context(), InstructionsPerTick); err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	if got := m.Register(1); got != 1 {
		t.Errorf("r1 = %v, want 1", got)
	}
}

// TestSleepReEntersItselfUntilItsDurationElapses drives sleep with a clock that
// advances, which the default clock does not.
func TestSleepReEntersItselfUntilItsDurationElapses(t *testing.T) {
	m := NewMachine()
	var now float32
	m.SetClock(func() float32 { return now })
	if err := m.Load(t.Context(), "move r0 1\nsleep 2\nmove r1 1"); err != nil {
		t.Fatalf("Load: %v", err)
	}

	executed, err := m.Tick(t.Context(), InstructionsPerTick)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if executed != 2 {
		t.Errorf("executed = %d, want 2: sleep ends the tick once it is not on line zero", executed)
	}
	if m.PC() != 1 {
		t.Errorf("PC = %d, want 1: sleep re-enters itself", m.PC())
	}

	now = 1
	if _, err := m.Tick(t.Context(), InstructionsPerTick); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if m.PC() != 1 {
		t.Errorf("PC = %d, want 1: the duration has not elapsed", m.PC())
	}
	if got := m.Register(1); got != 0 {
		t.Errorf("r1 = %v, want 0: the line after sleep must not have run", got)
	}

	now = 5
	if _, err := m.Tick(t.Context(), InstructionsPerTick); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := m.Register(1); got != 1 {
		t.Errorf("r1 = %v, want 1: the duration has elapsed", got)
	}
}

// TestRuntimeFaultIsRetriedAndClears covers the difference between a fault and
// a compile error: the faulting line is retried every tick and the error state
// clears by itself once the cause goes away.
func TestRuntimeFaultIsRetriedAndClears(t *testing.T) {
	m := NewMachine()
	m.SetRegister(1, 600)
	if err := m.Load(t.Context(), "get r0 db r1\nmove r2 1"); err != nil {
		t.Fatalf("Load: %v", err)
	}

	_, err := m.Tick(t.Context(), InstructionsPerTick)
	checkFault(t, "first tick", err, &Fault{Type: ExcUnknown, Line: 0})
	if m.PC() != 0 {
		t.Errorf("PC = %d, want 0: a fault rewinds to the faulting line", m.PC())
	}
	if m.Fault() == nil {
		t.Error("Fault is nil after a faulting tick")
	}

	// The same fault repeats for as long as the cause is there.
	_, err = m.Tick(t.Context(), InstructionsPerTick)
	checkFault(t, "second tick", err, &Fault{Type: ExcUnknown, Line: 0})

	m.SetRegister(1, 3)
	m.SetMemoryAt(3, 42)
	if _, err := m.Tick(t.Context(), InstructionsPerTick); err != nil {
		t.Fatalf("third Tick: %v", err)
	}
	if m.Fault() != nil {
		t.Errorf("Fault = %v, want none once the cause has gone", m.Fault())
	}
	if got := m.Register(0); got != 42 {
		t.Errorf("r0 = %v, want 42", got)
	}
	if got := m.Register(2); got != 1 {
		t.Errorf("r2 = %v, want 1: execution continued past the retried line", got)
	}
}

// TestStateSurvivesReload records that reflashing does not zero the machine.
// Only sp, the aliases, the defines and the jump tags are reset, which is why
// nothing can be assumed zero at program start.
func TestStateSurvivesReload(t *testing.T) {
	m := NewMachine()
	if err := m.Load(t.Context(), "move r0 7\npush 9"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := m.Tick(t.Context(), InstructionsPerTick); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := m.Register(ic10.RegSP); got != 1 {
		t.Fatalf("sp = %v, want 1", got)
	}

	if err := m.Load(t.Context(), "move r1 1"); err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if got := m.Register(0); got != 7 {
		t.Errorf("r0 = %v, want 7: registers survive a reload", got)
	}
	if value, _ := m.MemoryAt(0); value != 9 {
		t.Errorf("memory[0] = %v, want 9: memory survives a reload", value)
	}
	if got := m.Register(ic10.RegSP); got != 0 {
		t.Errorf("sp = %v, want 0: sp is the one register a reload resets", got)
	}
}

// TestLongToDoubleRoundTrip covers the conversion every bitwise instruction
// inherits, including the sign extension from bit 53.
func TestLongToDoubleRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		input int64
		want  float64
	}{
		{name: "zero", input: 0, want: 0},
		{name: "one", input: 1, want: 1},
		{name: "largest positive payload", input: 1<<53 - 1, want: twoPow53 - 1},
		{name: "the sign bit alone reads as the most negative value", input: 1 << 53, want: -twoPow53},
		{name: "minus one", input: -1, want: -1},
		{name: "everything above bit 53 is discarded", input: 1 << 60, want: 0},
	}
	for _, c := range cases {
		if got := LongToDouble(c.input); !sameFloat(got, c.want) {
			t.Errorf("%s: LongToDouble(%d) = %v, want %v", c.name, c.input, got, c.want)
		}
	}
}

// TestDoubleToLongReduces covers the modulus and the unsigned mask, which is
// one bit wider than the payload LongToDouble reads back.
func TestDoubleToLongReduces(t *testing.T) {
	cases := []struct {
		name   string
		input  float64
		signed bool
		want   int64
	}{
		{name: "small signed", input: 5, signed: true, want: 5},
		{name: "negative signed", input: -1, signed: true, want: -1},
		{name: "negative unsigned fills 54 bits", input: -1, signed: false, want: 1<<54 - 1},
		{name: "the modulus discards the 2^53 bit", input: twoPow53, signed: true, want: 0},
		{name: "NaN converts to zero", input: math.NaN(), signed: true, want: 0},
		{name: "infinity reduces to NaN and then zero", input: math.Inf(1), signed: true, want: 0},
	}
	for _, c := range cases {
		if got := DoubleToLong(c.input, c.signed); got != c.want {
			t.Errorf("%s: DoubleToLong(%v, %v) = %d, want %d", c.name, c.input, c.signed, got, c.want)
		}
	}
}

// TestSameFloatDistinguishesSignedZero guards the comparison the whole corpus
// rests on. A tolerance would hide exactly the differences differential testing
// exists to find.
func TestSameFloatDistinguishesSignedZero(t *testing.T) {
	if sameFloat(0, math.Copysign(0, -1)) {
		t.Error("sameFloat treats positive and negative zero as equal")
	}
	if !sameFloat(math.NaN(), math.NaN()) {
		t.Error("sameFloat treats two NaNs as different")
	}
	if sameFloat(1, math.Nextafter(1, 2)) {
		t.Error("sameFloat applies a tolerance")
	}
}
