package difftest

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/oracle"
)

// adapterTimeout bounds one adapter run so a non-terminating program fails the
// test instead of the suite.
const adapterTimeout = 30 * time.Second

// TestRunMatchesTheHarnessModel pins the adapter against expectations that were
// read off the ic10emu harness, so a change to either the tick loop or the
// error mapping shows up without the harness being present.
//
// The endless loop case is the instruction counting decision: a bare label line
// retires an instruction of its own on both sides, which is why three
// instructions go round the loop and the budget lands on 33,333 rather than
// 50,000.
func TestRunMatchesTheHarnessModel(t *testing.T) {
	tests := []struct {
		name          string
		source        string
		initial       oracle.State
		maxInstr      uint64
		wantRegisters map[int]float64
		wantStack     map[int]float64
		wantStatus    string
		wantErrorType string
		wantErrorLine uint32
		wantInstr     uint64
		wantTicks     uint64
	}{
		{
			name: "arithmetic and poke get db round trip",
			source: "move r0 7\nadd r1 r0 5\nmul r2 r1 3\npoke 100 r2\n" +
				"get r3 db 100\nsub r4 r3 r2\npush 42\npop r5",
			wantRegisters: map[int]float64{0: 7, 1: 12, 2: 36, 3: 36, 4: 0, 5: 42},
			wantStack:     map[int]float64{0: 42, 100: 36},
			wantStatus:    StatusEnded,
			wantInstr:     8,
			wantTicks:     1,
		},
		{
			name:   "initial state is honoured after the load resets sp",
			source: "get r0 db 5\nadd r1 r0 r2",
			initial: oracle.State{
				Registers: [oracle.RegisterCount]float64{2: 2.5, 16: 9},
				Stack:     [ic10.NumMemorySlots]float64{5: 1.5},
			},
			wantRegisters: map[int]float64{0: 1.5, 1: 4, 2: 2.5, 16: 9},
			wantStack:     map[int]float64{5: 1.5},
			wantStatus:    StatusEnded,
			wantInstr:     2,
			wantTicks:     1,
		},
		{
			name:       "clr db zeroes the whole array",
			source:     "poke 3 99\npoke 400 1\nclr db",
			wantStatus: StatusEnded,
			wantInstr:  3,
			wantTicks:  1,
		},
		{
			name:          "pop below zero leaves sp at -1 and faults",
			source:        "pop r0",
			wantRegisters: map[int]float64{16: -1},
			wantStatus:    StatusError,
			wantErrorType: "MemoryError.StackUnderflow",
			wantErrorLine: 0,
			wantInstr:     1,
			wantTicks:     1,
		},
		{
			name:          "a full tick of instructions stays in one tick",
			source:        strings.Repeat("add r0 r0 1\n", 127) + "add r0 r0 1",
			wantRegisters: map[int]float64{0: 128},
			wantStatus:    StatusEnded,
			wantInstr:     128,
			wantTicks:     1,
		},
		{
			name:          "overrunning the tick budget continues next tick",
			source:        strings.Repeat("add r0 r0 1\n", 199) + "add r0 r0 1",
			wantRegisters: map[int]float64{0: 200},
			wantStatus:    StatusEnded,
			wantInstr:     200,
			wantTicks:     2,
		},
		{
			name:          "yield ends the tick early",
			source:        "add r0 r0 1\nyield\nadd r0 r0 1",
			wantRegisters: map[int]float64{0: 2},
			wantStatus:    StatusEnded,
			wantInstr:     3,
			wantTicks:     2,
		},
		{
			name:          "indirect register reference resolves through r0",
			source:        "move r0 3\nmove rr0 42\nmove r1 r3",
			wantRegisters: map[int]float64{0: 3, 1: 42, 3: 42},
			wantStatus:    StatusEnded,
			wantInstr:     3,
			wantTicks:     1,
		},
		{
			name:          "a bare label costs an instruction, so an endless loop retires three per pass",
			source:        "loop:\nadd r0 r0 1\nj loop",
			wantRegisters: map[int]float64{0: 33_333},
			wantStatus:    StatusBudgetExhausted,
			wantInstr:     100_000,
			wantTicks:     782,
		},
		{
			name:          "a compile error runs nothing",
			source:        "add r0 1",
			wantStatus:    StatusCompileError,
			wantErrorType: "ic11c.IncorrectArgumentCount",
			wantErrorLine: 0,
		},
		{
			name:          "hcf destroys the chip",
			source:        "move r0 1\nhcf",
			wantRegisters: map[int]float64{0: 1},
			wantStatus:    StatusFire,
			wantInstr:     2,
			wantTicks:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), adapterTimeout)
			defer cancel()
			got, err := Run(ctx, tt.source, tt.initial, tt.maxInstr)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q (error %q on line %d)",
					got.Status, tt.wantStatus, got.ErrorType, got.ErrorLine)
			}
			if got.ErrorType != tt.wantErrorType {
				t.Errorf("error type = %q, want %q", got.ErrorType, tt.wantErrorType)
			}
			if tt.wantErrorType != "" && got.ErrorLine != tt.wantErrorLine {
				t.Errorf("error line = %d, want %d", got.ErrorLine, tt.wantErrorLine)
			}
			if got.Instructions != tt.wantInstr {
				t.Errorf("instructions = %d, want %d", got.Instructions, tt.wantInstr)
			}
			if got.Ticks != tt.wantTicks {
				t.Errorf("ticks = %d, want %d", got.Ticks, tt.wantTicks)
			}
			checkState(t, "register", tt.wantRegisters, got.Final.Registers[:])
			checkState(t, "slot", tt.wantStack, got.Final.Stack[:])
		})
	}
}

// TestModIsNotFloorModulus records what the transliterated implementation does
// with a negative divisor, which is neither the floor modulus the target
// documentation attributes to the game nor Rust's rem_euclid. It is a truncated
// remainder with the divisor added back once when the remainder is negative,
// which coincides with floor modulus only for a positive divisor. Both harnesses
// compute the same thing, so there is nothing to register.
func TestModIsNotFloorModulus(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   float64
	}{
		{name: "positive over positive", source: "mod r0 7 3", want: 1},
		{name: "negative over positive", source: "mod r0 -7 3", want: 2},
		{name: "positive over negative", source: "mod r0 7 -3", want: 1},
		{name: "negative over negative", source: "mod r0 -7 -3", want: -4},
		{name: "negative zero remainder keeps its sign", source: "mod r0 -0.5 -0.25", want: math.Copysign(0, -1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), adapterTimeout)
			defer cancel()
			got, err := Run(ctx, tt.source, oracle.State{}, 0)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if math.Float64bits(got.Final.Registers[0]) != math.Float64bits(tt.want) {
				t.Errorf("%s = %v (0x%016x), want %v (0x%016x)", tt.source,
					got.Final.Registers[0], math.Float64bits(got.Final.Registers[0]),
					tt.want, math.Float64bits(tt.want))
			}
		})
	}
}

func TestRunReportsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, "loop:\nj loop", oracle.State{}, 0); err == nil {
		t.Errorf("Run with a cancelled context returned no error")
	}
}

func checkState(tb testing.TB, kind string, want map[int]float64, got []float64) {
	tb.Helper()
	for i, value := range got {
		expected := want[i]
		if math.Float64bits(value) != math.Float64bits(expected) {
			tb.Errorf("%s %d = %v (0x%016x), want %v (0x%016x)",
				kind, i, value, math.Float64bits(value), expected, math.Float64bits(expected))
		}
	}
}
