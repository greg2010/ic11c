package chip

import (
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
)

func TestStopReason(t *testing.T) {
	tests := []struct {
		name      string
		snapshot  Snapshot
		exhausted bool
		want      StopReason
	}{
		{
			name:     "a compile error outranks everything",
			snapshot: Snapshot{CompileError: Fault{Type: ExcUnrecognisedInstruction}, Fault: Fault{Type: ExcUnknown}},
			want:     StopCompileError,
		},
		{
			name:     "a fault parks inside the program",
			snapshot: Snapshot{Fault: Fault{Type: ExcStackUnderFlow, Line: 1}, Address: 1, LineCount: 2},
			want:     StopFaulted,
		},
		{
			name:     "the counter leaving the program ends it",
			snapshot: Snapshot{Address: 2, LineCount: 2},
			want:     StopEnded,
		},
		{
			name:     "and so does a negative address, which nothing clamps here",
			snapshot: Snapshot{Address: -1, LineCount: 2},
			want:     StopEnded,
		},
		{
			name:      "a program still inside itself with the budget spent",
			snapshot:  Snapshot{Address: 0, LineCount: 2},
			exhausted: true,
			want:      StopBudget,
		},
		{
			name:     "and one still inside itself without it",
			snapshot: Snapshot{Address: 0, LineCount: 2},
			want:     StopSuspended,
		},
		{
			name:      "a fault is a fault whatever the budget did",
			snapshot:  Snapshot{Fault: Fault{Type: ExcStackUnderFlow}, Address: 0, LineCount: 2},
			exhausted: true,
			want:      StopFaulted,
		},
		{
			name:     "an empty program has ended before it starts",
			snapshot: Snapshot{Address: 0, LineCount: 0},
			want:     StopEnded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stopReason(tt.snapshot, tt.exhausted); got != tt.want {
				t.Errorf("stopReason = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestObservation drives the reading of a whole run's ending, which is where
// the harness's word and the state block have to agree.
func TestObservation(t *testing.T) {
	inside := Snapshot{Address: 1, LineCount: 4}
	ended := Snapshot{Address: 4, LineCount: 4}
	faulted := Snapshot{Fault: Fault{Type: ExcStackUnderFlow, Line: 1}, Address: 1, LineCount: 4}
	refused := Snapshot{CompileError: Fault{Type: ExcUnrecognisedInstruction, Line: 1}, Address: 0, LineCount: 1}

	tests := []struct {
		name      string
		reason    StopReason
		ticks     int
		snapshot  Snapshot
		want      Observation
		wantError string
	}{
		{
			name: "a program that ran off its end", reason: StopEnded, ticks: 1, snapshot: ended,
			want: Observation{Snapshot: ended, Ticks: 1, Stop: StopEnded},
		},
		{
			name: "a program that faulted", reason: StopFaulted, ticks: 3, snapshot: faulted,
			want: Observation{Snapshot: faulted, Ticks: 3, Stop: StopFaulted},
		},
		{
			name: "a program that did not compile reports no ticks", reason: StopCompileError, ticks: 1, snapshot: refused,
			want: Observation{Snapshot: refused, Stop: StopCompileError},
		},
		{
			name:   "a yield on the last tick is the run reaching its limit",
			reason: StopSuspended, ticks: TicksPerRun, snapshot: inside,
			want: Observation{Snapshot: inside, Ticks: TicksPerRun, Stop: StopTickBudget},
		},
		{
			name:   "and so is a spent budget on it",
			reason: StopBudget, ticks: TicksPerRun, snapshot: inside,
			want: Observation{Snapshot: inside, Ticks: TicksPerRun, Stop: StopTickBudget},
		},
		{
			name:   "a run still inside its program with ticks left is refused",
			reason: StopSuspended, ticks: TicksPerRun - 1, snapshot: inside,
			wantError: "has ticks left to spend",
		},
		{
			name:   "an ending the state block contradicts is refused",
			reason: StopEnded, ticks: 1, snapshot: inside,
			wantError: `ended a run "ended" and the state it left reads as "suspended"`,
		},
		{
			name:   "a fault the state block does not carry is refused",
			reason: StopFaulted, ticks: 1, snapshot: ended,
			wantError: `reads as "ended"`,
		},
		{
			name:   "a yield the state block reads as a fault is refused",
			reason: StopSuspended, ticks: TicksPerRun, snapshot: faulted,
			wantError: `reads as "faulted"`,
		},
		{
			name:   "a compile error the state block does not carry is refused",
			reason: StopCompileError, ticks: 1, snapshot: ended,
			wantError: `reads as "ended"`,
		},
		{
			name:   "the run's own ending is not one a tick can have",
			reason: StopTickBudget, ticks: TicksPerRun, snapshot: inside,
			wantError: `ended a run "tick_budget"`,
		},
		{
			name: "no tick at all is refused", reason: StopEnded, ticks: 0, snapshot: ended,
			wantError: "ran 0 of at most",
		},
		{
			name:   "more ticks than the run asked for is refused",
			reason: StopEnded, ticks: TicksPerRun + 1, snapshot: ended,
			wantError: "of at most 1024 ticks",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := observation(tt.reason, tt.ticks, tt.snapshot)
			if tt.wantError != "" {
				if err == nil {
					t.Fatalf("observation(%q, %d, ...) = %+v, want an error mentioning %q",
						tt.reason, tt.ticks, got, tt.wantError)
				}
				if !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("observation error = %q, want it to mention %q", err, tt.wantError)
				}
				if !errors.Is(err, ErrUnavailable) {
					t.Errorf("observation error = %q, want it to wrap ErrUnavailable", err)
				}
				if got != (Observation{}) {
					t.Errorf("observation returned %+v alongside an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("observation(%q, %d, ...): %v", tt.reason, tt.ticks, err)
			}
			if got != tt.want {
				t.Errorf("observation(%q, %d, ...) = %+v, want %+v", tt.reason, tt.ticks, got, tt.want)
			}
		})
	}
}

// TestSeedCommands pins what a seed skips. A reset leaves +0.0 everywhere, so a
// field skipped on comparing equal to zero would drop a -0.0 the caller asked
// for and leave nothing to notice.
func TestSeedCommands(t *testing.T) {
	negativeZero := math.Copysign(0, -1)

	tests := []struct {
		name  string
		state func(*State)
		want  []string
	}{
		{
			name:  "a state the chip already holds needs nothing",
			state: func(*State) {},
		},
		{
			name:  "a register",
			state: func(s *State) { s.Registers[2] = 7 },
			want:  []string{"reg 2 0x401c000000000000"},
		},
		{
			name:  "a negative zero register is seeded",
			state: func(s *State) { s.Registers[0] = negativeZero },
			want:  []string{"reg 0 0x8000000000000000"},
		},
		{
			name:  "a negative zero stack slot is seeded",
			state: func(s *State) { s.Stack[11] = negativeZero },
			want:  []string{"stack 11 0x8000000000000000"},
		},
		{
			name:  "a negative zero in the stack pointer is seeded like any other register",
			state: func(s *State) { s.Registers[16] = negativeZero },
			want:  []string{"reg 16 0x8000000000000000"},
		},
		{
			name: "registers come before slots, because a slot address is not a register index",
			state: func(s *State) {
				s.Registers[1] = -1
				s.Stack[0] = 1
			},
			want: []string{"reg 1 0xbff0000000000000", "stack 0 0x3ff0000000000000"},
		},
		{
			name: "a nan keeps its payload",
			state: func(s *State) {
				s.Registers[3] = math.Float64frombits(0x7ff8000000dead01)
			},
			want: []string{"reg 3 0x7ff8000000dead01"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var state State
			tt.state(&state)
			if got := seedCommands(state); !slices.Equal(got, tt.want) {
				t.Errorf("seedCommands = %v, want %v", got, tt.want)
			}
		})
	}
}
