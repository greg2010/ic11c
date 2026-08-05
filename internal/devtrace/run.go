package devtrace

import (
	"context"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
)

// Stimulus prepares the world before segment n runs, n counting from zero.
// [Run] and [RunNative] apply it at the same segment on both builds, which is
// what makes a world that changes mid-run comparable at all.
type Stimulus func(t *testing.T, h *chip.FixtureHarness, segment int)

// segmentBudget bounds one segment: instructions run per call to the chip's
// Execute, not a tick count. It sits well above any working control-loop turn,
// so reaching it means the loop has no yield rather than that the turn was
// long; [Run] reports that case as [StopBudget].
const segmentBudget = 1 << 16

// randomSeed pins the generator `rand` draws from. The game's own source is
// unseeded, so nothing can match it; what matters here is only that two builds
// of one program draw the same sequence.
const randomSeed = 0x1c11c

// clockStep is how far one reading advances the sleep clock. A second per
// reading is what lets a sleep elapse instead of re-entering itself forever.
const clockStep = 1

// Both entry points are pinned to *chip.FixtureHarness rather than an
// interface: it embeds *chip.Harness and so would also satisfy a faithful
// harness's interface, letting a non-recording process in silently. Pinning
// the concrete type makes that a compile error instead.
var (
	_ func(context.Context, *testing.T, *chip.FixtureHarness, string, RunOptions) Trace = Run
	_ func(context.Context, *testing.T, *chip.FixtureHarness, string, RunOptions) Trace = RunNative
)

// World lays out the devices a run happens among, on a harness [Run] has just
// reset. It must run after the reset: a reset discards every device on the
// harness, so a world laid out earlier would not survive to the program.
type World func(t *testing.T, h *chip.FixtureHarness)

// RunOptions is what a run needs beyond its program and its world.
type RunOptions struct {
	// Name identifies the run in a difference reported by [Diff].
	Name string
	// Segments is how many turns of the control loop to run. It must be at
	// least one: a run of no segments retires no instruction, and two of them
	// are indistinguishable however different the programs behind them.
	Segments int
	// World, when set, lays out the devices the program runs among.
	World World
	// Stimulus, when set, prepares the world before each segment.
	Stimulus Stimulus
}

// Run loads assembly onto a fresh chip in h and records what it writes.
//
// h is reset here and is the caller's for the length of the run; a second run
// started on it before this one returns would take both runs' writes. A chip
// fault is part of the returned trace, but anything that is not a chip verdict
// — an unreachable harness, assembly the chip refuses — fails the test instead,
// since there is then no run to compare.
func Run(ctx context.Context, t *testing.T, h *chip.FixtureHarness, assembly string, opts RunOptions) Trace {
	t.Helper()
	checkSegments(t, opts)
	if err := h.Reset(ctx); err != nil {
		t.Fatalf("%s: resetting the chip: %v", opts.Name, err)
	}
	if opts.World != nil {
		opts.World(t, h)
	}
	if err := h.SetClock(ctx, 0, clockStep); err != nil {
		t.Fatalf("%s: arming the clock: %v", opts.Name, err)
	}
	if err := h.SetRandomSeed(ctx, randomSeed); err != nil {
		t.Fatalf("%s: arming the generator: %v", opts.Name, err)
	}
	if err := h.Load(ctx, assembly); err != nil {
		t.Fatalf("%s: loading the emitted assembly:\n%s\n%v", opts.Name, assembly, err)
	}

	trace := Trace{Name: opts.Name, producer: producerChip}
	stoppedEarly := false
	for segment := range opts.Segments {
		if opts.Stimulus != nil {
			opts.Stimulus(t, h, segment)
		}
		got, err := h.Step(ctx, segmentBudget)
		if err != nil {
			t.Fatalf("%s: segment %d: %v\n%s", opts.Name, segment, err, assembly)
		}
		trace.Segments++
		stop, done := stopFor(t, got, opts.Name, assembly)
		if done {
			trace.Stop, stoppedEarly = stop, true
			break
		}
	}
	// Set on the one path that observed it: the loop ran every segment it was
	// given and the program was still going. Setting it before the loop would
	// carry it away untouched from a run of no segments, and setting it on an
	// unset reason afterwards would put it over whatever a segment reported.
	if !stoppedEarly {
		trace.Stop = Stop{Reason: StopSegments}
	}

	writes, err := h.Trace(ctx)
	if err != nil {
		t.Fatalf("%s: reading the trace: %v", opts.Name, err)
	}
	trace.Events = writes
	return trace
}

// checkSegments refuses a run bounded at nothing: a run of no segments writes
// nothing, and two such runs compare equal whatever their programs do.
func checkSegments(t *testing.T, opts RunOptions) {
	t.Helper()
	if opts.Segments < 1 {
		t.Fatalf("%s: a run of %d segments retires nothing, and two such runs are alike whatever their programs do",
			opts.Name, opts.Segments)
	}
}

// stopFor reads one segment's ending, reporting whether the run is over.
// Assembly the chip will not compile fails the test rather than ending the
// trace: a refused program is a defect in whichever build emitted it, not an
// ending the other build could be held to.
func stopFor(t *testing.T, got chip.Segment, name, assembly string) (Stop, bool) {
	t.Helper()
	switch got.Stop {
	case chip.StopCompileError:
		t.Fatalf("%s: the chip refused the emitted assembly: %s\n%s", name, got.CompileError, assembly)
	case chip.StopFaulted:
		return Stop{Reason: StopFaulted, Fault: got.Fault.Type, Line: got.Fault.Line}, true
	case chip.StopEnded:
		return Stop{Reason: StopEnded}, true
	case chip.StopBudget:
		return Stop{Reason: StopBudget}, true
	case chip.StopSuspended:
		return Stop{}, false
	case chip.StopTickBudget:
		// A whole run's ending; Step drives one call to Execute and never
		// counts ticks, so a segment cannot end this way.
	}
	t.Fatalf("%s: a segment ended %q, which is not an ending one segment has", name, got.Stop)
	return Stop{}, true
}
