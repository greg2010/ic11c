package difftest

import (
	"context"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/chiptest"
	"github.com/greg2010/ic11c/internal/ic10"
)

// returnLine is the one line shape that reads ra back.
const returnLine = "j ra"

// traceLimit bounds one trace: every instruction the chip would retire before
// [chip.TicksPerRun] runs out, so a program this stops is one the chip would
// not finish either.
const traceLimit = chip.TicksPerRun * chip.InstructionsPerTick

// step is one instruction a run retired: the line it ran, and the two scaffolding
// registers as they stood on the way in.
type step struct {
	line int
	sp   float64
	ra   float64
}

// trace runs a program one instruction at a time on the chip and reports the
// steps it took, since single stepping is the only way to see which lines a
// run actually reached. A step is recorded on the way in, so the first step
// is the seeded state rather than a reply from [chip.Harness.Step].
func trace(ctx context.Context, tb testing.TB, harness *chip.Harness, p Program) []step {
	tb.Helper()
	if err := harness.Reset(ctx); err != nil {
		tb.Fatalf("%s: reset: %v", p, err)
	}
	if err := harness.Load(ctx, p.Source); err != nil {
		tb.Fatalf("%s: load: %v\n%s", p, err, p.Source)
	}
	if err := harness.Seed(ctx, p.Initial); err != nil {
		tb.Fatalf("%s: seed: %v\n%s", p, err, p.Source)
	}

	current := step{
		line: 0,
		sp:   p.Initial.Registers[ic10.RegSP],
		ra:   p.Initial.Registers[ic10.RegRA],
	}
	var steps []step
	for len(steps) < traceLimit {
		steps = append(steps, current)
		segment, err := harness.Step(ctx, 1)
		if err != nil {
			tb.Fatalf("%s: line %d: %v\n%s", p, current.line, err, p.Source)
		}
		switch segment.Stop {
		case chip.StopEnded:
			return steps
		case chip.StopCompileError:
			tb.Fatalf("%s: did not assemble: %v\n%s", p, segment.CompileError, p.Source)
		case chip.StopFaulted:
			tb.Fatalf("%s: faulted with %v\n%s", p, segment.Fault, p.Source)
		case chip.StopBudget, chip.StopSuspended:
			// A one instruction budget spends itself every step, and a yield
			// ends one too. Both continue the trace.
		case chip.StopTickBudget:
			tb.Fatalf("%s: a single step ended %q, which is a whole run's ending\n%s",
				p, segment.Stop, p.Source)
		}
		current = step{
			line: segment.Address,
			sp:   segment.Registers[ic10.RegSP],
			ra:   segment.Registers[ic10.RegRA],
		}
	}
	tb.Fatalf("%s: still running after %d instructions\n%s", p, traceLimit, p.Source)
	return nil
}

// TestValueProgramsReturnThroughRa holds the corpus to exercising both halves
// of the call mechanism: not just writing ra, which every link form does, but
// reading it back through an executed trace.
func TestValueProgramsReturnThroughRa(t *testing.T) {
	// Measured at 45% over this sample; the floor only catches the shape
	// disappearing, not ordinary rate drift from a weight change.
	const floor = 0.35

	ctx, harness := chiptest.Harness(t)
	returned := 0
	for seed := range uint64(corpusSample) {
		if returnsThroughRa(ctx, t, harness, ValueProgram(seed)) {
			returned++
		}
	}

	fraction := float64(returned) / float64(corpusSample)
	t.Logf("%d of %d value programs (%.1f%%) return through ra", returned, corpusSample, 100*fraction)
	if fraction < floor {
		t.Errorf("%.1f%% of value programs return through ra, want at least %.1f%%; the corpus is "+
			"back to writing the return address and never reading it",
			100*fraction, 100*floor)
	}
}

// returnsThroughRa reports whether a run jumped back through the return address,
// and fails if a return executed without doing so.
func returnsThroughRa(ctx context.Context, tb testing.TB, harness *chip.Harness, p Program) bool {
	tb.Helper()
	lines := strings.Split(p.Source, "\n")
	steps := trace(ctx, tb, harness, p)

	found := false
	for i := range max(len(steps)-1, 0) {
		if lines[steps[i].line] != returnLine {
			continue
		}
		target, next := int(steps[i].ra), steps[i+1].line
		switch {
		case next != target:
			tb.Errorf("%s: %q on line %d with ra=%v, and the run went to line %d\n%s",
				p, returnLine, steps[i].line, steps[i].ra, next, p.Source)
		case target >= steps[i].line:
			tb.Errorf("%s: the return on line %d went forward to line %d, which is what falling "+
				"through would also have done\n%s", p, steps[i].line, target, p.Source)
		default:
			found = true
		}
	}
	return found
}

// TestValueProgramsContendForSharedSlots holds the corpus to exercising the
// one 512 slot array both the data region and the call stack write to: that
// the stack pointer travels across several operations, and that the slots it
// reaches are ones the data region instructions write too.
func TestValueProgramsContendForSharedSlots(t *testing.T) {
	// Measured at 43% contending and 28% wandering over this sample.
	const (
		contendingFloor = 0.30
		wanderingFloor  = 0.25
		// wanderSpread is how far the pointer must travel between two writes to
		// it before the stack counts as having grown rather than been re-seeded.
		// A seed per operation pins the spread at zero.
		wanderSpread = 3
	)

	ctx, harness := chiptest.Harness(t)
	checkContention(ctx, t, harness, contendingFloor, wanderingFloor, wanderSpread)
}

func checkContention(ctx context.Context, t *testing.T, harness *chip.Harness, contendingFloor, wanderingFloor float64, wanderSpread int) {
	t.Helper()
	contending, wandering := 0, 0
	for seed := range uint64(corpusSample) {
		p := ValueProgram(seed)
		stack, data, spread := touchedSlots(ctx, t, harness, p)
		if spread >= wanderSpread {
			wandering++
		}
		for slot := range stack {
			if data[slot] {
				contending++
				break
			}
		}
	}

	contendingFraction := float64(contending) / float64(corpusSample)
	wanderingFraction := float64(wandering) / float64(corpusSample)
	t.Logf("%d of %d value programs (%.1f%%) write one slot from both ends; %d (%.1f%%) move the "+
		"stack pointer at least %d slots between two writes to it",
		contending, corpusSample, 100*contendingFraction,
		wandering, 100*wanderingFraction, wanderSpread)

	if contendingFraction < contendingFloor {
		t.Errorf("%.1f%% of value programs write one slot from both ends, want at least %.1f%%; "+
			"the two ends of the array have stopped meeting",
			100*contendingFraction, 100*contendingFloor)
	}
	if wanderingFraction < wanderingFloor {
		t.Errorf("%.1f%% of value programs move the stack pointer at least %d slots between two "+
			"writes to it, want at least %.1f%%; the stack is being re-seeded rather than grown",
			100*wanderingFraction, wanderSpread, 100*wanderingFloor)
	}
}

// TestValueProgramsReachBothEndsOfTheArray holds the corpus to the two slots
// the chip's own bounds checks stand on: a push at 511 and a pop at 1, each
// one instruction from the fault the fault corpus raises deliberately.
func TestValueProgramsReachBothEndsOfTheArray(t *testing.T) {
	// nearEnd is how far a contention block's addresses stray from the stack
	// pointer, so a block seeded at an end reaches inside it.
	const nearEnd = contentionSpan

	ctx, harness := chiptest.Harness(t)
	checkArrayEnds(ctx, t, harness, nearEnd)
}

func checkArrayEnds(ctx context.Context, t *testing.T, harness *chip.Harness, nearEnd int) {
	t.Helper()
	lowest, highest := math.MaxInt, math.MinInt
	contendedLow, contendedHigh := false, false
	for seed := range uint64(corpusSample) {
		stack, data, _ := touchedSlots(ctx, t, harness, ValueProgram(seed))
		for slot := range stack {
			lowest, highest = min(lowest, slot), max(highest, slot)
			if !data[slot] {
				continue
			}
			contendedLow = contendedLow || slot < nearEnd
			contendedHigh = contendedHigh || slot >= ic10.NumMemorySlots-nearEnd
		}
	}

	t.Logf("stack slots span %d..%d of 0..%d; contention within %d of the bottom: %t, of the top: %t",
		lowest, highest, ic10.NumMemorySlots-1, nearEnd, contendedLow, contendedHigh)

	if lowest != 0 {
		t.Errorf("the lowest slot any value program reaches through the stack pointer is %d, want 0; "+
			"the bottom of the array is never written or read", lowest)
	}
	if highest != ic10.NumMemorySlots-1 {
		t.Errorf("the highest slot any value program reaches through the stack pointer is %d, want %d; "+
			"the top of the array is never written or read, so the legal top push is never made",
			highest, ic10.NumMemorySlots-1)
	}
	if !contendedLow {
		t.Errorf("no value program writes a slot below %d from both ends, so the two ends of the "+
			"array only ever meet away from the bottom", nearEnd)
	}
	if !contendedHigh {
		t.Errorf("no value program writes a slot at or above %d from both ends, so the two ends of "+
			"the array only ever meet away from the top", ic10.NumMemorySlots-nearEnd)
	}
}

// touchedSlots reports the array slots a run reached through the stack pointer
// and the slots it reached by address, and how far the pointer travelled between
// two writes to it.
func touchedSlots(ctx context.Context, tb testing.TB, harness *chip.Harness, p Program) (stack, data map[int]bool, spread int) {
	tb.Helper()
	lines := strings.Split(p.Source, "\n")
	stack, data = make(map[int]bool), make(map[int]bool)

	lo, hi := math.MaxInt, math.MinInt
	for _, s := range trace(ctx, tb, harness, p) {
		fields := strings.Fields(lines[s.line])
		pointer := int(math.RoundToEven(s.sp))
		switch {
		case fields[0] == "move" && fields[1] == "sp":
			lo, hi = math.MaxInt, math.MinInt
			continue
		case fields[0] == "push":
			stack[pointer] = true
		case fields[0] == "pop", fields[0] == "peek":
			stack[pointer-1] = true
		case fields[0] == "poke":
			data[slotOperand(tb, p, fields[1])] = true
			continue
		case fields[0] == "put":
			data[slotOperand(tb, p, fields[2])] = true
			continue
		case fields[0] == "get":
			data[slotOperand(tb, p, fields[3])] = true
			continue
		default:
			continue
		}
		lo, hi = min(lo, pointer), max(hi, pointer)
		spread = max(spread, hi-lo)
	}
	return stack, data, spread
}

// slotOperand reads an address operand. Every generator writes these as decimal
// literals, and a register or a define would make the slot unknowable from the
// source, so one that is not a literal fails rather than going uncounted.
func slotOperand(tb testing.TB, p Program, operand string) int {
	tb.Helper()
	slot, err := strconv.Atoi(operand)
	if err != nil {
		tb.Fatalf("%s: address operand %q is not a literal, so the slot it names cannot be "+
			"counted\n%s", p, operand, p.Source)
	}
	return slot
}
