package chip

import (
	"math"
	"testing"
)

// TestASleepOnLineZeroSpinsTheBudget checks the game's own arithmetic on the
// sign of an address, read off the chip rather than a model of it: a sleep on
// line 0 returns -0, which is not negative, so it does not suspend and instead
// spins for the rest of the budget. tools/chipgen edits this tick loop, so the
// behaviour isn't free for being correct by construction.
func TestASleepOnLineZeroSpinsTheBudget(t *testing.T) {
	ctx, harness := liveHarness(t)

	tests := []struct {
		name    string
		source  string
		wantIP  int
		want    StopReason
		spinsOn bool
	}{
		{
			name:    "on line 0 it does not suspend and spends the whole budget",
			source:  "sleep 1\nadd r0 r0 1",
			wantIP:  0,
			want:    StopBudget,
			spinsOn: true,
		},
		{
			name:   "on line 1 it suspends with the budget still in hand",
			source: "add r0 r0 1\nsleep 1",
			wantIP: 1,
			want:   StopSuspended,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := harness.SetClock(ctx, 0, 0); err != nil {
				t.Fatalf("pin the clock: %v", err)
			}
			if err := harness.Reset(ctx); err != nil {
				t.Fatalf("reset: %v", err)
			}
			if err := harness.Load(ctx, tt.source); err != nil {
				t.Fatalf("load: %v", err)
			}
			segment, err := harness.Step(ctx, InstructionsPerTick)
			if err != nil {
				t.Fatalf("step: %v", err)
			}
			t.Logf("%q under a budget of %d stopped %s at line %d of %d",
				tt.source, InstructionsPerTick, segment.Stop, segment.Address, segment.LineCount)
			if segment.Stop != tt.want {
				t.Errorf("stop = %q, want %q", segment.Stop, tt.want)
			}
			if segment.Address != tt.wantIP {
				t.Errorf("ip = %d, want %d", segment.Address, tt.wantIP)
			}
			// The two endings leave the same address, so the address alone
			// cannot tell them apart and asserting it is not enough.
			if tt.spinsOn && segment.Registers[0] != 0 {
				t.Errorf("r0 = %v, so the sleep let the line after it run", segment.Registers[0])
			}
		})
	}
}

// TestSetAddressSelectsTheLineToRun covers the verb a caller driving the chip as
// a dispatcher needs: the line is chosen per call rather than reached.
func TestSetAddressSelectsTheLineToRun(t *testing.T) {
	ctx, harness := liveHarness(t)

	// One line per register, so which line ran is read off which register moved.
	const source = "move r0 1\nmove r1 2\nmove r2 3"
	if err := harness.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := harness.Load(ctx, source); err != nil {
		t.Fatalf("load: %v", err)
	}

	// Backwards, so that reaching a line the way the program would is not what
	// puts the value there.
	for line := 2; line >= 0; line-- {
		if err := harness.SetAddress(ctx, line); err != nil {
			t.Fatalf("select line %d: %v", line, err)
		}
		segment, err := harness.Step(ctx, 1)
		if err != nil {
			t.Fatalf("run line %d: %v", line, err)
		}
		if got, want := segment.Registers[line], float64(line+1); got != want {
			t.Errorf("running line %d left r%d = %v, want %v", line, line, got, want)
		}
	}
}

// TestSetAddressRefusesALineOutsideTheProgram covers the refusal. An address
// past the program is one the next run reads as the program having ended, so a
// caller given it would measure a stop nothing caused.
func TestSetAddressRefusesALineOutsideTheProgram(t *testing.T) {
	ctx, harness := liveHarness(t)

	if err := harness.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := harness.Load(ctx, "move r0 1\nmove r1 2"); err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, line := range []int{-1, 2, 99} {
		if err := harness.SetAddress(ctx, line); err == nil {
			t.Errorf("the harness accepted line %d of a two line program", line)
		}
	}
}

// TestSetRegistersWritesEveryValue covers what separates it from a seed: a zero
// has to land. A caller writing arguments before each of many calls would
// otherwise run one against what the last call left behind.
func TestSetRegistersWritesEveryValue(t *testing.T) {
	ctx, harness := liveHarness(t)

	if err := harness.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := harness.Load(ctx, "move r15 r0"); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := harness.SetRegisters(ctx, 1, 2, 3); err != nil {
		t.Fatalf("write the registers: %v", err)
	}
	// Zero over a register the chip holds something in is the case a seed skips.
	if err := harness.SetRegisters(ctx, 0, math.Copysign(0, -1)); err != nil {
		t.Fatalf("write zeroes over them: %v", err)
	}
	// Read r0 back through the chip's own copy rather than through the state
	// block alone, so a write that answered ok while landing nowhere fails here.
	if err := harness.SetAddress(ctx, 0); err != nil {
		t.Fatalf("select the copy: %v", err)
	}
	segment, err := harness.Step(ctx, 1)
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	for _, read := range []struct {
		where string
		got   float64
		want  uint64
	}{
		{"r0 in the state block", segment.Registers[0], 0},
		{"r15, which the chip copied from r0", segment.Registers[15], 0},
		{"r1 in the state block", segment.Registers[1], 0x8000000000000000},
		{"r2, which no later write named", segment.Registers[2], math.Float64bits(3)},
	} {
		if bits := math.Float64bits(read.got); bits != read.want {
			t.Errorf("%s = %016x, want %016x", read.where, bits, read.want)
		}
	}
}
