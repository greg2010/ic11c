package difftest

import (
	"math"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/chiptest"
)

func TestMain(m *testing.M) { chiptest.Main(m) }

// runCase is one program and the machine state the chip must produce for it.
// Registers and memory slots are given by index; an index absent from a map
// holds +0, so each expectation is complete rather than a subset of state.
type runCase struct {
	name             string
	source           string
	initialRegisters map[int]float64
	initialStack     map[int]float64
	wantRegisters    map[int]float64
	wantStack        map[int]float64
	wantStop         chip.StopReason
	wantFault        chip.ExceptionType
	wantFaultLine    int
	wantTicks        int
}

// TestRunReportsTheWholeMachine holds the chip's tick loop and the state it
// reports to programs whose every answer is worked out by hand, pinning the
// exact state at the two array ends a sampled corpus only reaches by chance.
func TestRunReportsTheWholeMachine(t *testing.T) {
	tests := []runCase{
		{
			name: "arithmetic and poke get db round trip",
			source: "move r0 7\nadd r1 r0 5\nmul r2 r1 3\npoke 100 r2\n" +
				"get r3 db 100\nsub r4 r3 r2\npush 42\npop r5",
			wantRegisters: map[int]float64{0: 7, 1: 12, 2: 36, 3: 36, 4: 0, 5: 42},
			wantStack:     map[int]float64{0: 42, 100: 36},
			wantStop:      chip.StopEnded,
			wantTicks:     1,
		},
		{
			// sp is seeded to 9 and stays there: the load resets it, so seeding
			// before the load would answer 0 here.
			name:             "initial state is honoured after the load resets sp",
			source:           "get r0 db 5\nadd r1 r0 r2",
			initialRegisters: map[int]float64{2: 2.5, 16: 9},
			initialStack:     map[int]float64{5: 1.5},
			wantRegisters:    map[int]float64{0: 1.5, 1: 4, 2: 2.5, 16: 9},
			wantStack:        map[int]float64{5: 1.5},
			wantStop:         chip.StopEnded,
			wantTicks:        1,
		},
		{
			name:      "clr db zeroes the whole array",
			source:    "poke 3 99\npoke 400 1\nclr db",
			wantStop:  chip.StopEnded,
			wantTicks: 1,
		},
		{
			name:          "pop below zero leaves sp at -1 and faults",
			source:        "pop r0",
			wantRegisters: map[int]float64{16: -1},
			wantStop:      chip.StopFaulted,
			wantFault:     chip.ExcStackUnderFlow,
			wantTicks:     1,
		},
		{
			name:          "a push into the last slot leaves the pointer one past the array",
			source:        "move sp 511\npush 1",
			wantRegisters: map[int]float64{16: 512},
			wantStack:     map[int]float64{511: 1},
			wantStop:      chip.StopEnded,
			wantTicks:     1,
		},
		{
			name:          "a pop with the pointer one past the array reads the last slot",
			source:        "move sp 512\npop r0",
			initialStack:  map[int]float64{511: 7},
			wantRegisters: map[int]float64{0: 7, 16: 511},
			wantStack:     map[int]float64{511: 7},
			wantStop:      chip.StopEnded,
			wantTicks:     1,
		},
		{
			name:          "a peek with the pointer one past the array leaves it there",
			source:        "move sp 512\npeek r0",
			initialStack:  map[int]float64{511: 7},
			wantRegisters: map[int]float64{0: 7, 16: 512},
			wantStack:     map[int]float64{511: 7},
			wantStop:      chip.StopEnded,
			wantTicks:     1,
		},
		{
			name:          "a push into the first slot and the pop that reads it back",
			source:        "move sp 0\npush 3\nmove sp 1\npop r0",
			wantRegisters: map[int]float64{0: 3, 16: 0},
			wantStack:     map[int]float64{0: 3},
			wantStop:      chip.StopEnded,
			wantTicks:     1,
		},
		{
			// poke addresses the slot the push just filled, which is the
			// collision the shared array makes possible and the one a corpus is
			// generated to find.
			name:          "the stack and the data region meet in the last slot",
			source:        "move sp 511\npush 1\npoke 511 2\npeek r0",
			wantRegisters: map[int]float64{0: 2, 16: 512},
			wantStack:     map[int]float64{511: 2},
			wantStop:      chip.StopEnded,
			wantTicks:     1,
		},
		{
			name:          "a full tick of instructions stays in one tick",
			source:        strings.Repeat("add r0 r0 1\n", 127) + "add r0 r0 1",
			wantRegisters: map[int]float64{0: 128},
			wantStop:      chip.StopEnded,
			wantTicks:     1,
		},
		{
			// 200 lines is a program the editor would truncate, which is what
			// makes it the shortest way to reach a second tick.
			name:          "overrunning the tick budget continues next tick",
			source:        strings.Repeat("add r0 r0 1\n", 199) + "add r0 r0 1",
			wantRegisters: map[int]float64{0: 200},
			wantStop:      chip.StopEnded,
			wantTicks:     2,
		},
		{
			name:          "yield ends the tick early",
			source:        "add r0 r0 1\nyield\nadd r0 r0 1",
			wantRegisters: map[int]float64{0: 2},
			wantStop:      chip.StopEnded,
			wantTicks:     2,
		},
		{
			name:          "indirect register reference resolves through r0",
			source:        "move r0 3\nmove rr0 42\nmove r1 r3",
			wantRegisters: map[int]float64{0: 3, 1: 42, 3: 42},
			wantStop:      chip.StopEnded,
			wantTicks:     1,
		},
		{
			name:          "a compile error runs nothing",
			source:        "add r0 1",
			wantStop:      chip.StopCompileError,
			wantFault:     chip.ExcIncorrectArgumentCount,
			wantFaultLine: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, harness := chiptest.Harness(t)
			got, err := Run(ctx, harness, Program{
				Source:  tt.source,
				Initial: stateFrom(tt.initialRegisters, tt.initialStack),
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got.Stop != tt.wantStop {
				t.Errorf("stopped %q, want %q (compile %v, fault %v)",
					got.Stop, tt.wantStop, got.CompileError, got.Fault)
			}
			checkFault(t, got, tt.wantFault, tt.wantFaultLine)
			if got.Ticks != tt.wantTicks {
				t.Errorf("ticks = %d, want %d", got.Ticks, tt.wantTicks)
			}
			checkState(t, "register", tt.wantRegisters, got.Registers[:])
			checkState(t, "slot", tt.wantStack, got.Stack[:])
		})
	}
}

// TestCompileErrorNamesTheLineItStoppedOn pins where a refusal is reported: a
// run reporting the first line whatever it refused would look right on every
// one-line program and on nothing else.
func TestCompileErrorNamesTheLineItStoppedOn(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		wantLine int
	}{
		{name: "on the first line", source: "add r0 1"},
		{name: "past the first line", source: "move r0 1\nmove r1 2\nadd r2 1", wantLine: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, harness := chiptest.Harness(t)
			got, err := Run(ctx, harness, Program{Source: tt.source})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got.Stop != chip.StopCompileError {
				t.Errorf("stopped %q, want %q", got.Stop, chip.StopCompileError)
			}
			checkFault(t, got, chip.ExcIncorrectArgumentCount, tt.wantLine)
			if got.Ticks != 0 {
				t.Errorf("a program that did not assemble entered %d ticks", got.Ticks)
			}
		})
	}
}

// endlessLoop never finishes, so a run of it stops only when the tick budget
// does.
const endlessLoop = "loop:\nadd r0 r0 1\nj loop"

// TestARunThatNeverEndsStopsOnTheTickBudget holds the wall a non-terminating
// program hits, which is what keeps one generated by accident from wedging a
// sweep rather than failing it. What r0 reaches is not asserted, since that
// rests on a per-instruction cost this package does not claim to know.
func TestARunThatNeverEndsStopsOnTheTickBudget(t *testing.T) {
	ctx, harness := chiptest.Harness(t)
	got, err := Run(ctx, harness, Program{Source: endlessLoop})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Stop != chip.StopTickBudget {
		t.Errorf("stopped %q, want %q", got.Stop, chip.StopTickBudget)
	}
	if got.Ticks != chip.TicksPerRun {
		t.Errorf("ticks = %d, want the whole budget of %d", got.Ticks, chip.TicksPerRun)
	}
}

// stateFrom builds a machine state from the sparse maps a case carries.
func stateFrom(registers, stack map[int]float64) chip.State {
	var s chip.State
	for i, value := range registers {
		s.Registers[i] = value
	}
	for i, value := range stack {
		s.Stack[i] = value
	}
	return s
}

// checkState compares a whole array against the sparse map a case states: an
// unnamed index must hold +0. Comparison is by bits, since -0 compares equal
// to +0 and that difference is one this package exists to see.
func checkState(tb testing.TB, what string, want map[int]float64, got []float64) {
	tb.Helper()
	for i, value := range got {
		expected := want[i]
		if math.Float64bits(value) == math.Float64bits(expected) {
			continue
		}
		tb.Errorf("%s %d = %v (%#016x), want %v (%#016x)",
			what, i, value, math.Float64bits(value), expected, math.Float64bits(expected))
	}
	for i := range want {
		if i < 0 || i >= len(got) {
			tb.Errorf("the case names %s %d, which is outside the %d the machine has", what, i, len(got))
		}
	}
}

// checkFault holds an observation to the one fault a case names, in whichever
// of the two separate fields the chip recorded it in — reading only one would
// pass a run that raised the right error in the wrong half of the machine.
func checkFault(tb testing.TB, got chip.Observation, wantType chip.ExceptionType, wantLine int) {
	tb.Helper()
	fault := got.Fault
	if fault.Type == chip.ExcNone {
		fault = got.CompileError
	}
	if wantType == chip.ExcNone {
		if fault.Type != chip.ExcNone {
			tb.Errorf("the run raised %v, want no error", fault)
		}
		return
	}
	if fault.Type != wantType || fault.Line != wantLine {
		tb.Errorf("error = %v, want %v at line %d", fault, wantType, wantLine)
	}
}
