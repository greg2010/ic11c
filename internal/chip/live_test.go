package chip

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
)

// logicType and slotType resolve a property by the name the game gives it, so
// that a test naming one property cannot seed another.
func logicType(t *testing.T, name string) ic10.LogicType {
	t.Helper()
	info, ok := ic10.LookupLogicType(name)
	if !ok {
		t.Fatalf("no logic type is named %q", name)
	}
	return info.Value
}

func slotType(t *testing.T, name string) ic10.LogicSlotType {
	t.Helper()
	info, ok := ic10.LookupLogicSlotType(name)
	if !ok {
		t.Fatalf("no slot type is named %q", name)
	}
	return info.Value
}

// buildCommand is what builds a chip and runs these tests. [Start] does not name
// it: it is a library, and a caller outside this tree would be told to run
// something it has no target for.
const buildCommand = "`task test` builds a chip and runs these tests"

func liveHarness(t *testing.T) (context.Context, *Harness) {
	t.Helper()
	options := liveOptions(t)
	ctx := t.Context()
	harness, err := Start(ctx, options)
	if err != nil {
		t.Fatalf("start the chip: %v; %s", err, buildCommand)
	}
	t.Cleanup(func() {
		if err := harness.Close(); err != nil {
			t.Errorf("close the chip: %v", err)
		}
	})
	return ctx, harness
}

func liveFixtures(t *testing.T) (context.Context, *FixtureHarness) {
	t.Helper()
	options := liveOptions(t)
	ctx := t.Context()
	harness, err := StartFixtures(ctx, options)
	if err != nil {
		t.Fatalf("start a permissive chip: %v; %s", err, buildCommand)
	}
	t.Cleanup(func() {
		if err := harness.Close(); err != nil {
			t.Errorf("close the chip: %v", err)
		}
	})
	return ctx, harness
}

// liveOptions gates every test in this file behind [EnvEnabled]: the one
// legitimate skip is "was a run against the chip asked for". Anything after
// that point is a failure, not a skip — a run that didn't happen must never
// read as one that found nothing.
func liveOptions(t *testing.T) Options {
	t.Helper()
	if !Enabled() {
		t.Skipf("skipping: %s is not set; `task test` runs this", EnvEnabled)
	}
	options, err := EnvOptions()
	if err != nil {
		t.Fatalf("configure the chip: %v", err)
	}
	options.Log = t.Logf
	return options
}

func TestRunDrivesAProgramToItsStop(t *testing.T) {
	ctx, harness := liveHarness(t)

	tests := []struct {
		name     string
		source   string
		wantStop StopReason
		check    func(t *testing.T, got Observation)
	}{
		{
			name:     "a program that runs off its end",
			source:   "add r0 r0 1\nadd r0 r0 2",
			wantStop: StopEnded,
			check: func(t *testing.T, got Observation) {
				t.Helper()
				if got.Registers[0] != 3 || got.Address != 2 || got.LineCount != 2 || got.Ticks != 1 {
					t.Errorf("r0/ip/lines/ticks = %v/%d/%d/%d, want 3/2/2/1",
						got.Registers[0], got.Address, got.LineCount, got.Ticks)
				}
			},
		},
		{
			name:     "a trailing newline is a line",
			source:   "yield\n",
			wantStop: StopEnded,
			check: func(t *testing.T, got Observation) {
				t.Helper()
				if got.LineCount != 2 {
					t.Errorf("lines = %d, want 2", got.LineCount)
				}
			},
		},
		{
			name:     "a program that faults",
			source:   "move sp 0\npop r0",
			wantStop: StopFaulted,
			check: func(t *testing.T, got Observation) {
				t.Helper()
				want := Fault{Type: ExcStackUnderFlow, Line: 1}
				if got.Fault != want {
					t.Errorf("fault = %v, want %v", got.Fault, want)
				}
				if got.HousingError != 1 {
					t.Errorf("housing error state = %d, want 1", got.HousingError)
				}
			},
		},
		{
			name:     "a program that does not compile",
			source:   "add r0 r0 1\nnosuchinstruction",
			wantStop: StopCompileError,
			check: func(t *testing.T, got Observation) {
				t.Helper()
				if got.CompileError.Type != ExcUnrecognisedInstruction || got.CompileError.Line != 1 {
					t.Errorf("compile error = %v, want UnrecognisedInstruction at line 1", got.CompileError)
				}
				if got.Ticks != 0 {
					t.Errorf("ticks = %d, want 0", got.Ticks)
				}
			},
		},
		{
			name:     "a program that never ends",
			source:   "j 0",
			wantStop: StopTickBudget,
			check: func(t *testing.T, got Observation) {
				t.Helper()
				if got.Ticks != TicksPerRun {
					t.Errorf("ticks = %d, want %d", got.Ticks, TicksPerRun)
				}
			},
		},
		{
			name:     "a yielding program spends its ticks and keeps going",
			source:   "add r0 r0 1\nyield\nj 0",
			wantStop: StopTickBudget,
			check: func(t *testing.T, got Observation) {
				t.Helper()
				if got.Registers[0] != TicksPerRun {
					t.Errorf("r0 = %v, want one increment per tick (%d)", got.Registers[0], TicksPerRun)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := harness.Run(ctx, Request{Source: tt.source})
			if err != nil {
				t.Fatalf("run %q: %v", tt.source, err)
			}
			t.Logf("%q stopped %s after %d ticks at line %d of %d, error %s, compile %s",
				tt.source, got.Stop, got.Ticks, got.Address, got.LineCount, got.Fault, got.CompileError)
			if got.Stop != tt.wantStop {
				t.Fatalf("stop = %q, want %q", got.Stop, tt.wantStop)
			}
			tt.check(t, got)
		})
	}
}

// TestRunAgreesWithPerTickDriving holds the tick loop the harness runs to the
// one this package used to drive one round trip at a time. Since the loop
// moved across the protocol, everything a run leaves is compared, not just the
// ending. The programs chosen make a tick boundary visible — loops that
// straddle the budget, a yield adding a tick per turn, a fault reached after
// many — since a program fitting in one tick compares the same either way.
func TestRunAgreesWithPerTickDriving(t *testing.T) {
	ctx, harness := liveHarness(t)

	sources := []string{
		"add r0 r0 1",
		"",
		"j 0",
		"add r0 r0 1\nyield\nj 0",
		"sleep 1",
		"add r0 r0 1\nsleep 1",
		"add r0 r0 1\nyield",
		"add r0 r0 1\nnosuchinstruction",
		"j 0\nnosuchinstruction",
		"pop r0",
		"add r0 r0 1\nblt r0 5000 0\nmove sp 0\npop r1",
	}
	// Turn counts either side of the tick budget, so that the comparison covers
	// a boundary falling inside a loop turn as well as between two.
	for _, turns := range []int{63, 64, 65, 127, 128, 129, 4096, 70000} {
		count := "add r0 r0 1\nblt r0 " + strconv.Itoa(turns) + " 0"
		sources = append(sources, count, count+"\nmove sp 0\npop r1",
			"add r0 r0 1\nyield\nblt r0 "+strconv.Itoa(turns)+" 0")
	}

	for _, source := range sources {
		t.Run(source, func(t *testing.T) {
			want := perTickRun(ctx, t, harness, source)
			got, err := harness.Run(ctx, Request{Source: source})
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			t.Logf("%s after %d ticks", got.Stop, got.Ticks)
			if got.Stop != want.Stop || got.Ticks != want.Ticks {
				t.Errorf("stop/ticks = %q/%d driven per tick, %q/%d in one exchange",
					want.Stop, want.Ticks, got.Stop, got.Ticks)
			}
			// The scalars are named rather than compared as a struct: a struct
			// comparison holds the registers to float equality, which reports a
			// pair of NaNs as a difference and a +0.0 against a -0.0 as none.
			if got.Address != want.Address || got.LineCount != want.LineCount {
				t.Errorf("ip/lines = %d/%d driven per tick, %d/%d in one exchange",
					want.Address, want.LineCount, got.Address, got.LineCount)
			}
			if got.Fault != want.Fault || got.CompileError != want.CompileError ||
				got.HousingError != want.HousingError {
				t.Errorf("err/cerr/housing = %v/%v/%d driven per tick, %v/%v/%d in one exchange",
					want.Fault, want.CompileError, want.HousingError,
					got.Fault, got.CompileError, got.HousingError)
			}
			for i := range got.Registers {
				if bits, wantBits := math.Float64bits(got.Registers[i]), math.Float64bits(want.Registers[i]); bits != wantBits {
					t.Errorf("r%d = %016x driven per tick, %016x in one exchange", i, wantBits, bits)
				}
			}
			for i := range got.Stack {
				if bits, wantBits := math.Float64bits(got.Stack[i]), math.Float64bits(want.Stack[i]); bits != wantBits {
					t.Errorf("stack[%d] = %016x driven per tick, %016x in one exchange", i, wantBits, bits)
				}
			}
		})
	}
}

// perTickRun drives a program to its stop from here, a round trip per tick,
// and concludes the run's ending the way [Harness.Run] does from the harness's
// one reply.
func perTickRun(ctx context.Context, t *testing.T, h *Harness, source string) Observation {
	t.Helper()
	if err := h.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := h.Load(ctx, source); err != nil {
		t.Fatalf("load: %v", err)
	}
	var last Segment
	for ticks := 1; ticks <= TicksPerRun; ticks++ {
		segment, err := h.Step(ctx, InstructionsPerTick)
		if err != nil {
			t.Fatalf("step %d: %v", ticks, err)
		}
		last = segment
		switch segment.Stop {
		case StopCompileError:
			return Observation{Snapshot: segment.Snapshot, Stop: StopCompileError}
		case StopFaulted, StopEnded:
			return Observation{Snapshot: segment.Snapshot, Ticks: ticks, Stop: segment.Stop}
		case StopSuspended, StopBudget:
			// The game follows a yield and a spent budget with another tick.
		case StopTickBudget:
			t.Fatalf("a segment ended %q, which is a whole run's ending", segment.Stop)
		}
	}
	return Observation{Snapshot: last.Snapshot, Ticks: TicksPerRun, Stop: StopTickBudget}
}

// TestValuesSurviveTheProtocol seeds the values a decimal protocol could not
// carry and reads each of them back twice: once through the state block, and
// once through the chip's own copy of it into another field. A seeding verb that
// answers ok while landing nowhere passes the first read and fails the second.
func TestValuesSurviveTheProtocol(t *testing.T) {
	ctx, harness := liveHarness(t)

	tests := []struct {
		name  string
		value float64
	}{
		{name: "negative zero", value: math.Copysign(0, -1)},
		{name: "a nan carrying a payload", value: math.Float64frombits(0x7ff8000000dead01)},
		{name: "a nan with the sign bit set", value: math.Float64frombits(0xfff8000000dead01)},
		{name: "negative infinity", value: math.Inf(-1)},
		{name: "the smallest denormal", value: math.SmallestNonzeroFloat64},
		{name: "a value one ulp under pi", value: math.Nextafter(math.Pi, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// r5 is copied into r6, and the slot at 7 is read out with peek,
			// which reads Stack[sp-1]. Neither read is the command that wrote.
			const source = "move r6 r5\nmove sp 8\npeek r8"
			var initial State
			initial.Registers[5] = tt.value
			initial.Stack[7] = tt.value

			got, err := harness.Run(ctx, Request{Source: source, Initial: initial})
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			want := math.Float64bits(tt.value)
			t.Logf("seeded %016x; state r5=%016x stack[7]=%016x; chip copied r6=%016x peeked r8=%016x",
				want, math.Float64bits(got.Registers[5]), math.Float64bits(got.Stack[7]),
				math.Float64bits(got.Registers[6]), math.Float64bits(got.Registers[8]))

			for _, read := range []struct {
				where string
				got   float64
			}{
				{"r5 in the state block", got.Registers[5]},
				{"stack[7] in the state block", got.Stack[7]},
				{"r6, which the chip copied from r5", got.Registers[6]},
				{"r8, which the chip peeked out of stack[7]", got.Registers[8]},
			} {
				if bits := math.Float64bits(read.got); bits != want {
					t.Errorf("%s = %016x, want %016x", read.where, bits, want)
				}
			}
		})
	}
}

// TestTheChipComputesTheValuesToo checks that a value the chip produces itself
// survives the state block, not only one this driver put there. A protocol that
// carried a seeded -0.0 and lost a computed one would pass the test above.
func TestTheChipComputesTheValuesToo(t *testing.T) {
	ctx, harness := liveHarness(t)

	tests := []struct {
		name   string
		source string
		want   uint64
	}{
		{name: "a negative zero", source: "mul r0 0 -1", want: 0x8000000000000000},
		{name: "a nan", source: "div r0 0 0", want: 0xfff8000000000000},
		{name: "an infinity", source: "div r0 1 0", want: 0x7ff0000000000000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := harness.Run(ctx, Request{Source: tt.source})
			if err != nil {
				t.Fatalf("run %q: %v", tt.source, err)
			}
			t.Logf("%q left r0 = %016x", tt.source, math.Float64bits(got.Registers[0]))
			if bits := math.Float64bits(got.Registers[0]); bits != tt.want {
				t.Errorf("%q left r0 = %016x, want %016x", tt.source, bits, tt.want)
			}
		})
	}
}

// TestStepTellsAYieldFromAnOverrun is what per-segment stepping is for. The chip
// destroys the distinction - a yield and a spent budget leave the same address,
// the same error state and the same line count - so it is read from the reply
// and never inferred from the state.
func TestStepTellsAYieldFromAnOverrun(t *testing.T) {
	ctx, harness := liveHarness(t)

	tests := []struct {
		name   string
		source string
		budget int
		// clockStep is how far one reading advances the sleep clock. Zero is a
		// clock two readings cannot tell apart, so a sleep under it never expires.
		clockStep float64
		want      StopReason
	}{
		{name: "a yield suspends", source: "yield\nadd r0 r0 1", budget: 128, want: StopSuspended},
		{
			// The yield leaves the address one past itself, and past the program
			// as well, so there is nothing left to come back to.
			name:   "a yield on the last line ends the program instead",
			source: "add r0 r0 1\nyield",
			budget: 128,
			want:   StopEnded,
		},
		{name: "a loop with no yield overruns", source: "add r0 r0 1\nj 0", budget: 4, want: StopBudget},
		{
			name:   "a sleep on any line but the first suspends",
			source: "add r0 r0 1\nsleep 1",
			budget: 128,
			want:   StopSuspended,
		},
		{
			// _SLEEP_Operation returns -index, so on line 0 it returns -0, which
			// is not negative and does not suspend. The instruction re-enters
			// itself for the rest of the budget.
			name:   "a sleep on line 0 spins the budget instead",
			source: "sleep 1",
			budget: 4,
			want:   StopBudget,
		},
		{name: "a program that ends", source: "add r0 r0 1", budget: 128, want: StopEnded},
		{name: "a program that faults", source: "move sp 0\npop r0", budget: 128, want: StopFaulted},
		{name: "a program that does not compile", source: "nosuchinstruction", budget: 128, want: StopCompileError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := harness.SetClock(ctx, 0, tt.clockStep); err != nil {
				t.Fatalf("set the clock: %v", err)
			}
			if err := harness.Reset(ctx); err != nil {
				t.Fatalf("reset: %v", err)
			}
			if err := harness.Load(ctx, tt.source); err != nil {
				t.Fatalf("load: %v", err)
			}
			segment, err := harness.Step(ctx, tt.budget)
			if err != nil {
				t.Fatalf("step: %v", err)
			}
			t.Logf("%q under a budget of %d stopped %s at line %d of %d",
				tt.source, tt.budget, segment.Stop, segment.Address, segment.LineCount)
			if segment.Stop != tt.want {
				t.Errorf("stop = %q, want %q", segment.Stop, tt.want)
			}
		})
	}
}

// segmentCap bounds a stepping loop in these tests. It is far more segments than
// any of them needs and is here so that a chip that stops making progress ends
// the test rather than the deadline.
const segmentCap = 32

// TestTheClockRunsASleepDown checks the two ends of the clock a caller arms: a
// step of zero is a sleep that never expires, and a step that covers the
// duration is one that does.
func TestTheClockRunsASleepDown(t *testing.T) {
	ctx, harness := liveHarness(t)

	// The sleep is on line 1 because one on line 0 does not suspend at all.
	const source = "add r0 r0 1\nsleep 2\nadd r1 r1 1"

	tests := []struct {
		name     string
		step     float64
		wantStop StopReason
	}{
		{name: "a clock that does not advance never expires the sleep", step: 0, wantStop: StopSuspended},
		{name: "one that does expires it", step: 1, wantStop: StopEnded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := harness.SetClock(ctx, 0, tt.step); err != nil {
				t.Fatalf("set the clock: %v", err)
			}
			if err := harness.Reset(ctx); err != nil {
				t.Fatalf("reset: %v", err)
			}
			if err := harness.Load(ctx, source); err != nil {
				t.Fatalf("load: %v", err)
			}
			var segment Segment
			segments := 0
			for range segmentCap {
				var err error
				segment, err = harness.Step(ctx, InstructionsPerTick)
				if err != nil {
					t.Fatalf("step: %v", err)
				}
				segments++
				if segment.Stop != StopSuspended {
					break
				}
			}
			t.Logf("a step of %v left the program %s after %d segments, r1 = %v",
				tt.step, segment.Stop, segments, segment.Registers[1])
			if segment.Stop != tt.wantStop {
				t.Fatalf("stop = %q after %d segments, want %q", segment.Stop, segments, tt.wantStop)
			}
			if tt.wantStop == StopEnded && segments < 2 {
				t.Errorf("the sleep ended in %d segment(s), so it never suspended", segments)
			}
		})
	}
}

// TestTheRandomSeedRepeats checks that the sequence rand draws from is a
// function of the seed. Nothing can reproduce the game's own unseeded generator;
// what a caller needs is that two runs of one program draw the same numbers.
func TestTheRandomSeedRepeats(t *testing.T) {
	ctx, harness := liveHarness(t)

	const source = "rand r0\nrand r1\nrand r2"
	draw := func(t *testing.T, seed int) [3]float64 {
		t.Helper()
		if err := harness.SetRandomSeed(ctx, seed); err != nil {
			t.Fatalf("arm the generator: %v", err)
		}
		got, err := harness.Run(ctx, Request{Source: source})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if got.Stop != StopEnded {
			t.Fatalf("stop = %q, want %q", got.Stop, StopEnded)
		}
		return [3]float64{got.Registers[0], got.Registers[1], got.Registers[2]}
	}

	first := draw(t, 7)
	again := draw(t, 7)
	other := draw(t, 8)
	t.Logf("seed 7 drew %v, then %v; seed 8 drew %v", first, again, other)

	if first != again {
		t.Errorf("one seed drew %v and then %v", first, again)
	}
	if first == other {
		t.Errorf("two seeds drew the same sequence %v", first)
	}
	for i, value := range first {
		if !(value >= 0 && value < 1) {
			t.Errorf("draw %d = %v, which is outside [0, 1)", i, value)
		}
	}
}

// TestAFaithfulDeviceRefusesWhatAFixtureAnswers is the barrier between the two
// kinds of process, driven from both sides.
func TestAFaithfulDeviceRefusesWhatAFixtureAnswers(t *testing.T) {
	const readSetting = "l r0 d0 Setting"

	t.Run("a faithful process has nothing on the pin", func(t *testing.T) {
		ctx, harness := liveHarness(t)
		got, err := harness.Run(ctx, Request{Source: readSetting})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		t.Logf("a faithful chip answered %q with %s", readSetting, got.Fault)
		if got.Stop != StopFaulted || got.Fault.Type != ExcDeviceNotFound {
			t.Errorf("stop/fault = %q/%v, want %q/DeviceNotFound", got.Stop, got.Fault, StopFaulted)
		}
	})

	t.Run("a permissive process answers it", func(t *testing.T) {
		ctx, fixtures := liveFixtures(t)
		if err := fixtures.AddDevice(ctx, 0); err != nil {
			t.Fatalf("add a fixture device: %v", err)
		}
		if err := fixtures.SetProperty(ctx, 0, logicType(t, "Setting"), math.Pi); err != nil {
			t.Fatalf("seed Setting: %v", err)
		}
		if err := fixtures.Load(ctx, readSetting); err != nil {
			t.Fatalf("load: %v", err)
		}
		segment, err := fixtures.Step(ctx, InstructionsPerTick)
		if err != nil {
			t.Fatalf("step: %v", err)
		}
		t.Logf("a permissive chip answered %q with r0 = %016x, stopping %s",
			readSetting, math.Float64bits(segment.Registers[0]), segment.Stop)
		if segment.Stop != StopEnded {
			t.Fatalf("stop = %q, want %q", segment.Stop, StopEnded)
		}
		if segment.Registers[0] != math.Pi {
			t.Errorf("r0 = %v, want %v", segment.Registers[0], math.Pi)
		}
	})

	t.Run("and refuses the verb when the process was not started for it", func(t *testing.T) {
		ctx, harness := liveHarness(t)
		// The wrap is unreachable from outside this package; what it establishes
		// here is that the harness process refuses the verb on its own, so the Go
		// type is the second barrier and not the only one.
		wrapped := &FixtureHarness{chipProcess: harness}
		err := wrapped.AddDevice(ctx, 0)
		if err == nil {
			t.Fatal("a faithful process built a fixture device")
		}
		t.Logf("a faithful process answered a fixture verb with: %v", err)
	})
}

// TestAPermissiveProcessRecordsWhatAProgramWrote drives the surface a device
// trace needs: seeded reads, recorded writes, slots, and a batch selection.
func TestAPermissiveProcessRecordsWhatAProgramWrote(t *testing.T) {
	ctx, fixtures := liveFixtures(t)

	const prefab = -1234
	setting := logicType(t, "Setting")
	occupied := slotType(t, "Occupied")
	// Reads the seeded property, writes it back doubled, writes a slot property,
	// and reads the same device through the batch form the prefab hash selects.
	// The properties are spelled as ordinals, which the operand forms take where
	// they take a name, so that nothing here can name one thing and seed another.
	source := fmt.Sprintf("l r0 d0 %d\nadd r1 r0 r0\ns d0 %d r1\nss d0 0 %d 5\nlb r2 %d %d 0",
		setting, setting, occupied, prefab, setting)

	if err := fixtures.AddDevice(ctx, 0); err != nil {
		t.Fatalf("add a fixture device: %v", err)
	}
	if err := fixtures.SetHashes(ctx, 0, prefab, 0); err != nil {
		t.Fatalf("set the hashes: %v", err)
	}
	if err := fixtures.SetProperty(ctx, 0, setting, 100); err != nil {
		t.Fatalf("seed Setting: %v", err)
	}
	// Seeded to something other than what the program writes. A store reads the
	// current value and skips the write when it would not change it, so seeding
	// the value the program writes would record nothing and read as a trace that
	// lost it.
	if err := fixtures.SetSlotProperty(ctx, 0, 0, occupied, 1); err != nil {
		t.Fatalf("seed a slot: %v", err)
	}
	if err := fixtures.Load(ctx, source); err != nil {
		t.Fatalf("load: %v", err)
	}
	segment, err := fixtures.Step(ctx, InstructionsPerTick)
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	if segment.Stop != StopEnded {
		t.Fatalf("stop = %q at line %d, error %s, compile %s",
			segment.Stop, segment.Address, segment.Fault, segment.CompileError)
	}

	writes, err := fixtures.Trace(ctx)
	if err != nil {
		t.Fatalf("read the trace: %v", err)
	}
	t.Logf("r0=%v r1=%v r2=%v, trace %+v", segment.Registers[0], segment.Registers[1], segment.Registers[2], writes)

	if segment.Registers[0] != 100 {
		t.Errorf("the seeded property read back as %v, want 100", segment.Registers[0])
	}
	if segment.Registers[2] != 200 {
		t.Errorf("the batch read answered %v, want the 200 the program had just written", segment.Registers[2])
	}
	want := []Write{
		{Pin: 0, Slot: NoSlot, Property: int(setting), Value: 200},
		{Pin: 0, Slot: 0, Property: int(occupied), Value: 5},
	}
	if len(writes) != len(want) {
		t.Fatalf("the trace carried %d writes, want %d", len(writes), len(want))
	}
	for i, got := range writes {
		if got != want[i] {
			t.Errorf("write %d = %+v, want %+v", i, got, want[i])
		}
	}
}

// TestTraceIsEmptyUntilAProgramWrites is the negative control for the trace: a
// seed is the world changing rather than the program writing, so it must record
// nothing.
func TestTraceIsEmptyUntilAProgramWrites(t *testing.T) {
	ctx, fixtures := liveFixtures(t)

	if err := fixtures.AddDevice(ctx, 0); err != nil {
		t.Fatalf("add a fixture device: %v", err)
	}
	if err := fixtures.SetProperty(ctx, 0, logicType(t, "Setting"), 1); err != nil {
		t.Fatalf("seed Setting: %v", err)
	}
	writes, err := fixtures.Trace(ctx)
	if err != nil {
		t.Fatalf("read the trace: %v", err)
	}
	if len(writes) != 0 {
		t.Errorf("seeding recorded %+v", writes)
	}
}
