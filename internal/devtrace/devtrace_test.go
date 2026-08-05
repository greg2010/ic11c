package devtrace

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/chiptest"
	"github.com/greg2010/ic11c/internal/ic10"
)

// TestRunRecordsEveryWriteInOrder covers the surface a trace is: the writes,
// in the order they happened, on the pin they landed on.
func TestRunRecordsEveryWriteInOrder(t *testing.T) {
	assembly := strings.Join([]string{
		"s d0 Setting 1",
		"ss d1 2 Quantity 3",
		"s d0 On 1",
		"yield",
		"s d0 Setting 4",
		"yield",
		"j 4",
	}, "\n")

	trace := traceProgram(t, assembly, 2, RunOptions{Name: "recording", Segments: 3})

	want := []chip.Write{
		write(0, int(logicType(t, "Setting")), chip.NoSlot, 1),
		write(1, int(slotType(t, "Quantity")), 2, 3),
		write(0, int(logicType(t, "On")), chip.NoSlot, 1),
		write(0, int(logicType(t, "Setting")), chip.NoSlot, 4),
	}
	if len(trace.Events) != len(want) {
		t.Fatalf("traced %d writes, want %d:\n%v", len(trace.Events), len(want), trace.Events)
	}
	for i, event := range want {
		if !sameWrite(trace.Events[i], event, sameBits) {
			t.Errorf("write %d is %s, want %s", i, formatWrite(trace.Events[i]), formatWrite(event))
		}
	}
	if trace.Segments != 3 {
		t.Errorf("ran %d segments, want 3", trace.Segments)
	}
}

// TestRunRecordsOnlyWhatChanged covers the chip's own rule: a store reads the
// current value first and is skipped when it would not change it, so a store of
// the value already there leaves nothing a device could observe.
func TestRunRecordsOnlyWhatChanged(t *testing.T) {
	assembly := strings.Join([]string{
		"s d0 Setting 7",
		"s d0 Setting 7",
		"s d0 Setting 8",
		"yield",
		"j 3",
	}, "\n")

	trace := traceProgram(t, assembly, 1, RunOptions{Name: "unchanged", Segments: 2})

	if len(trace.Events) != 2 {
		t.Fatalf("traced %d writes, want the repeated store left out:\n%v", len(trace.Events), trace.Events)
	}
	for i, want := range []float64{7, 8} {
		if trace.Events[i].Value != want {
			t.Errorf("write %d is %v, want %v", i, trace.Events[i].Value, want)
		}
	}
}

// TestSeedingIsNotRecorded keeps the world out of the trace. A reading a
// fixture is driven with is the environment, not something the program did, and
// a trace holding both could not tell them apart.
func TestSeedingIsNotRecorded(t *testing.T) {
	ctx, h := chiptest.Fixtures(t)
	temperature, quantity := logicType(t, "Temperature"), slotType(t, "Quantity")

	// An empty program, so that the run resets the chip and lays the world out
	// and nothing the program did can be what a write records.
	trace := Run(ctx, t, h, "yield", RunOptions{Name: "seeded", Segments: 1, World: func(t *testing.T, h *chip.FixtureHarness) {
		t.Helper()
		pins(1)(t, h)
		if err := h.SetProperty(ctx, 0, temperature, 300); err != nil {
			t.Fatalf("seed Temperature: %v", err)
		}
		if err := h.SetSlotProperty(ctx, 0, 0, quantity, 4); err != nil {
			t.Fatalf("seed a slot: %v", err)
		}
	}})

	if len(trace.Events) != 0 {
		t.Errorf("seeding a world recorded %d writes, want none: %v", len(trace.Events), trace.Events)
	}
	// Read back through the getter rather than through the trace, so that a
	// seed which recorded nothing because it landed nowhere fails here.
	got, err := h.Property(ctx, chip.Pin(0), temperature)
	if err != nil {
		t.Fatalf("read Temperature back: %v", err)
	}
	if got != 300 {
		t.Errorf("the seeded reading is %v, want 300", got)
	}
	slot, err := h.SlotProperty(ctx, chip.Pin(0), 0, quantity)
	if err != nil {
		t.Fatalf("read the slot back: %v", err)
	}
	if slot != 4 {
		t.Errorf("the seeded slot reading is %v, want 4", slot)
	}
}

// TestStimulusRunsBeforeEachSegment covers the mechanism that makes a world
// which changes during a run comparable: the change lands at a segment
// boundary, which is a place in the source rather than a place in the emitted
// program.
func TestStimulusRunsBeforeEachSegment(t *testing.T) {
	assembly := strings.Join([]string{
		"l r0 d0 Setting",
		"s d1 Setting r0",
		"yield",
		"j 0",
	}, "\n")

	setting := logicType(t, "Setting")
	trace := traceProgram(t, assembly, 2, RunOptions{
		Name:     "stimulated",
		Segments: 4,
		Stimulus: func(t *testing.T, h *chip.FixtureHarness, segment int) {
			t.Helper()
			if err := h.SetProperty(t.Context(), 0, setting, float64(segment*10+1)); err != nil {
				t.Fatalf("segment %d: seed Setting: %v", segment, err)
			}
		},
	})

	want := []float64{1, 11, 21, 31}
	if len(trace.Events) != len(want) {
		t.Fatalf("traced %d writes, want %d:\n%v", len(trace.Events), len(want), trace.Events)
	}
	for i, value := range want {
		if trace.Events[i].Value != value {
			t.Errorf("segment %d published %v, want %v", i, trace.Events[i].Value, value)
		}
	}
}

// TestBatchWriteRecordsEveryDeviceItReached covers the one instruction whose
// single line is several writes. A batch store names no pin, so the trace is
// where the devices it reached become visible at all.
func TestBatchWriteRecordsEveryDeviceItReached(t *testing.T) {
	const prefab = -1860064656
	if want := ic10.HashName("StructureWallLight"); want != prefab {
		t.Fatalf("the program selects prefab %d and StructureWallLight hashes to %d", prefab, want)
	}

	trace := traceProgram(t, "sb -1860064656 On 1\nyield\nj 0", 3, RunOptions{
		Name: "batch", Segments: 1,
		World: func(t *testing.T, h *chip.FixtureHarness) {
			t.Helper()
			pins(3)(t, h)
			// d1 is left without a prefab, so a batch reaching it is a batch
			// that selected on nothing.
			for _, pin := range []int{0, 2} {
				if err := h.SetHashes(t.Context(), pin, prefab, 0); err != nil {
					t.Fatalf("give d%d a prefab: %v", pin, err)
				}
			}
		},
	})

	if len(trace.Events) != 2 {
		t.Fatalf("a batch store over two matching devices traced %d writes:\n%v", len(trace.Events), trace.Events)
	}
	// The chip walks the network back to front, so the higher pin is written
	// first; what matters here is that both were written and neither was the
	// device left without a prefab.
	for _, event := range trace.Events {
		if event.Pin != 0 && event.Pin != 2 {
			t.Errorf("a batch store reached d%d, which carries no matching prefab", event.Pin)
		}
	}
}

// TestNamedBatchNarrowsOnTheNameHash covers the other half of batch selection,
// which a fixture reaching for one named device depends on.
func TestNamedBatchNarrowsOnTheNameHash(t *testing.T) {
	const prefab, name = 1947944864, -601214782
	if got := ic10.HashName("StructureFurnace"); got != prefab {
		t.Fatalf("the program selects prefab %d and StructureFurnace hashes to %d", prefab, got)
	}
	if got := ic10.HashName("north"); got != name {
		t.Fatalf("the program narrows on name %d and north hashes to %d", name, got)
	}

	trace := traceProgram(t, "sbn 1947944864 -601214782 Setting 5\nyield\nj 0", 2, RunOptions{
		Name: "named", Segments: 1,
		World: func(t *testing.T, h *chip.FixtureHarness) {
			t.Helper()
			pins(2)(t, h)
			// Both carry the prefab, so what narrows the selection to one of
			// them is the name and nothing else.
			for pin := range 2 {
				named := 0
				if pin == 1 {
					named = name
				}
				if err := h.SetHashes(t.Context(), pin, prefab, named); err != nil {
					t.Fatalf("give d%d its hashes: %v", pin, err)
				}
			}
		},
	})
	if len(trace.Events) != 1 || trace.Events[0].Pin != 1 {
		t.Fatalf("a name filtered batch store reached %v, want d1 alone", trace.Events)
	}
}

// TestRunReportsHowTheRunEnded covers each way a run stops, since that is the
// part of a trace that is not a write. The segment count is asserted beside
// the stop because [Diff] compares both, and the segment a run stopped in
// counts however it stopped.
func TestRunReportsHowTheRunEnded(t *testing.T) {
	tests := []struct {
		name     string
		assembly string
		pins     int
		segments int
		want     Stop
		ran      int
	}{
		{
			name:     "a control loop outliving its segments",
			assembly: "s d0 Setting 1\nyield\nj 0",
			pins:     1,
			segments: 3,
			want:     Stop{Reason: StopSegments},
			ran:      3,
		},
		{
			name:     "a program running off its own end",
			assembly: "s d0 Setting 1",
			pins:     1,
			segments: 4,
			want:     Stop{Reason: StopEnded},
			ran:      1,
		},
		{
			name:     "a store to an empty pin",
			assembly: "s d5 Setting 1\nyield\nj 0",
			pins:     1,
			segments: 4,
			want:     Stop{Reason: StopFaulted, Fault: chip.ExcDeviceNotFound, Line: 0},
			ran:      1,
		},
		{
			name:     "a fault on a later turn of the loop",
			assembly: "add r0 r0 1\ns d0 Setting r0\nblt r0 3 4\ns d5 Setting 1\nyield\nj 0",
			pins:     1,
			segments: 8,
			want:     Stop{Reason: StopFaulted, Fault: chip.ExcDeviceNotFound, Line: 3},
			ran:      3,
		},
		{
			name:     "a loop with no yield in it",
			assembly: "add r0 r0 1\nj 0",
			pins:     1,
			segments: 4,
			want:     Stop{Reason: StopBudget},
			ran:      1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trace := traceProgram(t, tt.assembly, tt.pins, RunOptions{Name: tt.name, Segments: tt.segments})
			if trace.Stop != tt.want {
				t.Errorf("the run ended with %+v, want %+v", trace.Stop, tt.want)
			}
			if trace.Segments != tt.ran {
				t.Errorf("the run turned %d segments, want %d", trace.Segments, tt.ran)
			}
		})
	}
}

// TestSleepSuspendsForTheSameSegmentsWhateverTheProgramCosts is the reason a
// sleeping fixture is comparable at all.
//
// `sleep` re-enters itself once per segment until its duration elapses against
// the clock, so two builds of one program suspend for the same number of
// segments however many instructions they retire — except on line 0, where
// _SLEEP_Operation returns -0 instead of a suspending address, so the sleep
// spins instead. Every case here keeps the sleep off line 0; that exception is
// covered by the negative control below instead.
func TestSleepSuspendsForTheSameSegmentsWhateverTheProgramCosts(t *testing.T) {
	tests := []struct {
		name    string
		padding string
	}{
		{name: "a bare sleep"},
		{name: "the same sleep behind more instructions", padding: strings.Repeat("add r2 r2 1\n", 3)},
		{name: "and behind more still", padding: strings.Repeat("add r2 r2 1\n", 9)},
	}

	// Line 0 counts r1 up, so the value stored differs every turn; the padding
	// counts r2 instead, so it cannot also change what the program computed.
	const loop = "add r1 r1 1\n"
	const rest = "sleep 4\ns d0 Setting r1\nyield\nj 0"

	var first Trace
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trace := traceProgram(t, loop+tt.padding+rest, 1,
				RunOptions{Name: tt.name, Segments: 12})
			t.Logf("%s traced %d writes over %d segments: %v",
				tt.name, len(trace.Events), trace.Segments, trace.Stop)

			// Two writes rather than one, so a program that came back from its
			// sleep once and then stopped fails here. With a sleep of 4 and a
			// clock advancing one per reading, 12 segments is room for two.
			if len(trace.Events) < 2 {
				t.Fatalf("the program made %d writes, so it never came back from its sleep twice",
					len(trace.Events))
			}
			if i == 0 {
				first = trace
				return
			}
			// Diff compares the written values and the segment count, both of
			// which move when padding changes when the program comes back.
			if err := Diff(first, trace); err != nil {
				t.Errorf("padding the program changed what a device saw: %v", err)
			}
		})
	}
}

// TestPaddingChangesTheTraceWhenTheSleepIsOnLineZero is the negative control
// for the test above: it puts the same sleep on line 0, where it spins rather
// than suspends, and holds the padded and unpadded traces apart.
func TestPaddingChangesTheTraceWhenTheSleepIsOnLineZero(t *testing.T) {
	const rest = "sleep 4\nadd r1 r1 1\ns d0 Setting r1\nyield\nj 0"

	onLineZero := traceProgram(t, rest, 1,
		RunOptions{Name: "the sleep on line 0", Segments: 12})
	padded := traceProgram(t, "add r1 r1 1\n"+rest, 1,
		RunOptions{Name: "the same sleep on line 1", Segments: 12})

	t.Logf("on line 0 the program made %d writes over %d segments; on line 1 it made %d over %d",
		len(onLineZero.Events), onLineZero.Segments, len(padded.Events), padded.Segments)

	if len(onLineZero.Events) <= len(padded.Events) {
		t.Errorf("a sleep on line 0 made %d writes and one on line 1 made %d; the first does not suspend, so it should come back far more often",
			len(onLineZero.Events), len(padded.Events))
	}
	if err := Diff(onLineZero, padded); err == nil {
		t.Error("moving a sleep off line 0 left the trace unchanged, so nothing here would notice the exception being lost")
	}
}

// TestDiff covers what counts as a disagreement and what does not, and how a
// disagreement classifies itself as [*Difference] rather than only as a
// sentence.
func TestDiff(t *testing.T) {
	setting := func(device int, value float64) chip.Write {
		return write(device, int(logicType(t, "Setting")), chip.NoSlot, value)
	}
	// chipRun stamps a hand-built trace as a chip run that used up its segments.
	// Diff refuses a trace that says neither, so a row saying nothing about
	// either is saying it came off one machine and ran to its own bound, which
	// is what every row below but the last two mean.
	chipRun := func(name string, events ...chip.Write) Trace {
		return Trace{Name: name, Events: events, Stop: Stop{Reason: StopSegments},
			producer: producerChip}
	}
	segments := func(t Trace, n int) Trace { t.Segments = n; return t }
	ending := func(t Trace, stop Stop) Trace { t.Stop = stop; return t }

	tests := []struct {
		name string
		a, b Trace
		// want is a fragment of the difference, or empty when the two agree.
		want string
		// onAWrite is what the difference has to classify itself as.
		onAWrite bool
		// refused marks a row Diff turns away rather than compares. Such a row
		// is not a difference between two runs and carries no classification,
		// because there was nothing well formed enough to classify.
		refused bool
	}{
		{
			name: "the same run twice",
			a:    segments(chipRun("left", setting(0, 1), setting(1, 2)), 4),
			b:    segments(chipRun("right", setting(0, 1), setting(1, 2)), 4),
		},
		{
			name: "the same NaN from one machine twice",
			a:    chipRun("left", setting(0, math.NaN())),
			b:    chipRun("right", setting(0, math.NaN())),
		},
		{
			name:     "a different value",
			a:        chipRun("left", setting(0, 1)),
			b:        chipRun("right", setting(0, 2)),
			want:     "write 0 differs",
			onAWrite: true,
		},
		{
			name:     "the same value on a different pin",
			a:        chipRun("left", setting(0, 1)),
			b:        chipRun("right", setting(1, 1)),
			want:     "write 0 differs",
			onAWrite: true,
		},
		{
			name:     "the two zeroes, which the chip's own division tells apart",
			a:        chipRun("left", setting(0, 0)),
			b:        chipRun("right", setting(0, math.Copysign(0, -1))),
			want:     "write 0 differs",
			onAWrite: true,
		},
		{
			name: "one run agreeing as far as it goes and then stopping",
			a:    chipRun("left", setting(0, 1), setting(0, 2)),
			b:    chipRun("right", setting(0, 1)),
			want: "made 2 writes and right made 1",
		},
		{
			name: "the same writes over a different number of loop turns",
			a:    segments(chipRun("left", setting(0, 1)), 4),
			b:    segments(chipRun("right", setting(0, 1)), 5),
			want: "one control loop turned more often than the other",
		},
		{
			name: "one run ending and the other still going",
			a:    ending(chipRun("left"), Stop{Reason: StopEnded}),
			b:    chipRun("right"),
			want: "ended because",
		},
		{
			name: "two different faults",
			a:    ending(chipRun("left"), Stop{Reason: StopFaulted, Fault: chip.ExcDeviceNotFound}),
			b:    ending(chipRun("right"), Stop{Reason: StopFaulted, Fault: chip.ExcStackOverFlow}),
			want: "ended because",
		},
		{
			name: "one fault reached from two different lines",
			a:    ending(chipRun("left"), Stop{Reason: StopFaulted, Fault: chip.ExcDeviceNotFound, Line: 3}),
			b:    ending(chipRun("right"), Stop{Reason: StopFaulted, Fault: chip.ExcDeviceNotFound, Line: 41}),
		},
		{
			// The two below are why a zero value is not a reading. A trace that
			// went unstamped would otherwise be compared bit for bit against
			// another one, which says both came off one machine, and two of them
			// would agree about an ending neither ever reached.
			name:    "a trace that says nothing about what ran it",
			a:       ending(Trace{Name: "left"}, Stop{Reason: StopEnded}),
			b:       ending(chipRun("right"), Stop{Reason: StopEnded}),
			want:    "says nothing about what ran it",
			refused: true,
		},
		{
			name:    "a trace that says nothing about how its run ended",
			a:       ending(chipRun("left"), Stop{}),
			b:       chipRun("right"),
			want:    "says nothing about how its run ended",
			refused: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Diff(tt.a, tt.b)
			switch {
			case tt.want == "" && err != nil:
				t.Errorf("reported a difference between two runs a device could not tell apart: %v", err)
				return
			case tt.want != "" && err == nil:
				t.Errorf("reported no difference, want one mentioning %q", tt.want)
				return
			case tt.want != "" && !strings.Contains(err.Error(), tt.want):
				t.Errorf("the difference is %q, want it to mention %q", err, tt.want)
			}
			if err == nil {
				return
			}
			var difference *Difference
			if tt.refused {
				if errors.As(err, &difference) {
					t.Errorf("a trace Diff cannot read came back classified as %v, which a reduction would keep hold of as though two runs had disagreed", difference)
				}
				return
			}
			if !errors.As(err, &difference) {
				t.Fatalf("the difference is a %T, which carries no classification for a reduction to keep hold of", err)
			}
			if difference.OnAWrite != tt.onAWrite {
				t.Errorf("the difference reports OnAWrite as %t, want %t: %v", difference.OnAWrite, tt.onAWrite, err)
			}
		})
	}
}

// TestDiffNamesWhichNaNEachRunWrote covers the one difference whose two sides
// share a spelling: a NaN renders as "NaN" however it was made, so the report
// has to carry the payloads instead.
func TestDiffNamesWhichNaNEachRunWrote(t *testing.T) {
	health := func(bits uint64) chip.Write {
		return write(3, int(slotType(t, "Health")), 0, math.Float64frombits(bits))
	}
	const ours, theirs = 0x7ff8000000000001, 0xfff8000000000000

	err := Diff(
		Trace{Name: "left", Events: []chip.Write{health(ours)},
			Stop: Stop{Reason: StopSegments}, producer: producerChip},
		Trace{Name: "right", Events: []chip.Write{health(theirs)},
			Stop: Stop{Reason: StopSegments}, producer: producerChip})
	if err == nil {
		t.Fatalf("two runs that wrote different NaNs are reported as agreeing")
	}
	for _, want := range []string{"0x7ff8000000000001", "0xfff8000000000000"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the difference is %q, want it to name the payload %s", err, want)
		}
	}
}

// TestDiffComparesNaNPayloadsOnlyWithinOneMachine covers what each pair of
// producers can settle: two chip runs compare a NaN's payload, a chip run and
// a native run do not, and the zeroes and infinities compare either way.
func TestDiffComparesNaNPayloadsOnlyWithinOneMachine(t *testing.T) {
	setting := int(logicType(t, "Setting"))
	bits := func(v uint64) float64 { return math.Float64frombits(v) }
	const (
		goNaN      = 0x7ff8000000000001
		x86NaN     = 0xfff8000000000000
		payloadNaN = 0x7ff8000000000042
	)

	tests := []struct {
		name string
		// producer is what made the second trace; the first is always a chip run.
		producer producer
		a, b     float64
		want     bool
	}{
		{name: "two chip runs writing one NaN", producer: producerChip, a: bits(goNaN), b: bits(goNaN)},
		{name: "two chip runs writing NaNs of different payloads", producer: producerChip, a: bits(goNaN), b: bits(payloadNaN), want: true},
		{name: "two chip runs writing the two zeroes", producer: producerChip, a: 0, b: math.Copysign(0, -1), want: true},
		{name: "two chip runs writing the two infinities", producer: producerChip, a: math.Inf(1), b: math.Inf(-1), want: true},

		{name: "a chip run and a native run writing one NaN", producer: producerNative, a: bits(goNaN), b: bits(goNaN)},
		{name: "a chip run and a native run writing each machine's own NaN", producer: producerNative, a: bits(goNaN), b: bits(x86NaN)},
		{name: "a chip run and a native run writing NaNs of different payloads", producer: producerNative, a: bits(payloadNaN), b: bits(goNaN)},
		{name: "a chip run writing a NaN where a native run wrote a number", producer: producerNative, a: bits(goNaN), b: 1, want: true},
		{name: "a chip run writing a number where a native run wrote a NaN", producer: producerNative, a: 1, b: bits(goNaN), want: true},
		{name: "a chip run and a native run writing the two zeroes", producer: producerNative, a: 0, b: math.Copysign(0, -1), want: true},
		{name: "a chip run and a native run writing the two infinities", producer: producerNative, a: math.Inf(1), b: math.Inf(-1), want: true},
		{name: "a chip run and a native run writing an infinity where the other wrote a NaN", producer: producerNative, a: math.Inf(1), b: bits(goNaN), want: true},
		{name: "a chip run and a native run writing one number", producer: producerNative, a: 293.15, b: 293.15},
		{name: "a chip run and a native run writing two numbers", producer: producerNative, a: 293.15, b: 293.16, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Diff(
				Trace{Name: "left", Events: []chip.Write{write(0, setting, chip.NoSlot, tt.a)},
					Stop: Stop{Reason: StopSegments}, producer: producerChip},
				Trace{Name: "right", Events: []chip.Write{write(0, setting, chip.NoSlot, tt.b)},
					Stop: Stop{Reason: StopSegments}, producer: tt.producer})
			if got := err != nil; got != tt.want {
				t.Errorf("Diff reported a difference: %t, want %t: %v", got, tt.want, err)
			}
		})
	}
}

// TestProducersSayWhichMachineRanTheProgram holds the two producers to
// stamping traces differently, which is what [Diff] reads to decide how far two
// of them can be held to each other. A producer that stamped nothing would
// leave every comparison reading as one machine against itself.
func TestProducersSayWhichMachineRanTheProgram(t *testing.T) {
	compiled := traceProgram(t, "s d0 Setting 1\nyield\nj 0", 1, RunOptions{Name: "chip", Segments: 1})
	if compiled.producer != producerChip {
		t.Errorf("Run produced a %v trace, want %v", compiled.producer, producerChip)
	}
	const src = `const dev out = d0;

void main(void) {
    while (true) {
        __ic_store(out, Setting, 1.0);
        __ic_yield();
    }
}`
	ctx, h := chiptest.Fixtures(t)
	native := RunNative(ctx, t, h, microcFile(t, "producer.c", src),
		RunOptions{Name: nativeName, Segments: 1, World: pins(1)})
	if native.producer != producerNative {
		t.Errorf("RunNative produced a %v trace, want %v", native.producer, producerNative)
	}
}

func TestEveryEnumValueIsNamed(t *testing.T) {
	for p := producerUnset; p <= producerNative; p++ {
		if s := p.String(); strings.Contains(s, "producer(") {
			t.Errorf("producer %d has no name", p)
		}
	}
	if s := (producerNative + 1).String(); !strings.Contains(s, "producer(") {
		t.Errorf("producer(%d) is named %q, so the loop above stops short of the last producer", producerNative+1, s)
	}
	for r := stopUnset; r <= StopBudget; r++ {
		if s := r.String(); strings.Contains(s, "StopReason(") {
			t.Errorf("stop reason %d has no name", r)
		}
	}
	if s := (StopBudget + 1).String(); !strings.Contains(s, "StopReason(") {
		t.Errorf("StopReason(%d) is named %q, so the loop above stops short of the last reason", StopBudget+1, s)
	}
}

// TestFormatWrite covers the rendering a failure is read through, including the
// property ordinal the generated tables do not name — an unnamed property in a
// trace is a finding, and a diagnostic that hid it would be the wrong place to
// lose it.
func TestFormatWrite(t *testing.T) {
	tests := []struct {
		name  string
		event chip.Write
		want  string
	}{
		{
			name:  "a logic write",
			event: write(1, int(logicType(t, "Setting")), chip.NoSlot, 293.15),
			want:  "d1 Setting = 293.15",
		},
		{
			name:  "a slot write",
			event: write(0, int(slotType(t, "Quantity")), 2, 4),
			want:  "d0 slot 2 Quantity = 4",
		},
		{
			name:  "a property the tables do not name",
			event: write(0, 60000, chip.NoSlot, 1),
			want:  "d0 LogicType(60000) = 1",
		},
		{
			name:  "a slot property the tables do not name",
			event: write(0, 250, 1, 1),
			want:  "d0 slot 1 LogicSlotType(250) = 1",
		},
		{
			name:  "the NaN Go's own arithmetic answers with",
			event: write(3, int(logicType(t, "Setting")), chip.NoSlot, math.NaN()),
			want:  "d3 Setting = NaN(0x7ff8000000000001)",
		},
		{
			name:  "the NaN an x86 invalid operation answers with, which is a different value under the same spelling",
			event: write(3, int(logicType(t, "Setting")), chip.NoSlot, math.Float64frombits(0xfff8000000000000)),
			want:  "d3 Setting = NaN(0xfff8000000000000)",
		},
		{
			name:  "a NaN carrying a payload of its own",
			event: write(3, int(slotType(t, "Health")), 0, math.Float64frombits(0x7ff8000000000042)),
			want:  "d3 slot 0 Health = NaN(0x7ff8000000000042)",
		},
		{
			name:  "a negative zero, which is not a NaN and is still not the value beside it",
			event: write(0, int(logicType(t, "Setting")), chip.NoSlot, math.Copysign(0, -1)),
			want:  "d0 Setting = -0",
		},
		{
			name:  "an infinity",
			event: write(0, int(logicType(t, "Setting")), chip.NoSlot, math.Inf(-1)),
			want:  "d0 Setting = -Inf",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatWrite(tt.event); got != tt.want {
				t.Errorf("rendered %q, want %q", got, tt.want)
			}
		})
	}
}
