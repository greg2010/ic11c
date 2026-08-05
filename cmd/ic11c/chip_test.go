package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/chiptest"
	"github.com/greg2010/ic11c/internal/ic10"
)

func TestMain(m *testing.M) {
	if err := locateCorpus(); err != nil {
		fmt.Fprintf(os.Stderr, "ic11c tests: %v\n", err)
		os.Exit(1)
	}
	chiptest.Main(m)
}

// maxChipTicks bounds a run here. It is far more ticks than any corpus program
// takes to leave its own instructions, and it is what ends a test on a program
// that loops forever instead of the package deadline.
const maxChipTicks = 1024

// chipRun is the chip this test was handed and what the last segment left on
// it. It is a handle rather than a machine: every value it answers comes off
// the snapshot the last segment read back, and every write goes out as a
// command.
type chipRun struct {
	ctx      context.Context
	harness  *chip.FixtureHarness
	assembly string
	last     chip.Segment
}

// device is one pin on a housing, which is what the seeding and read-back
// helpers name a device by. It carries the process rather than the run, so a
// stimulus handed only a harness can name a pin on it.
type device struct {
	ctx     context.Context
	harness *chip.FixtureHarness
	pin     int
}

// pinOn names one pin of a harness a stimulus was handed. The context is the
// shared process's rather than the test's: the process outlives every test,
// so a command cancelled part way through would desync the stream for the
// whole binary rather than for one run.
func pinOn(t *testing.T, h *chip.FixtureHarness, pin int) *device {
	t.Helper()
	return &device{ctx: context.Background(), harness: h, pin: pin}
}

// newChipRun takes the shared permissive chip, reset and ready. The clock is
// armed here rather than at load, so a caller wanting a different one can
// set it and have it hold; the default of one second a reading is what lets
// a sleep elapse instead of re-entering itself forever.
func newChipRun(t *testing.T) *chipRun {
	t.Helper()
	ctx, harness := chiptest.Fixtures(t)
	if err := harness.SetClock(ctx, 0, 1); err != nil {
		t.Fatalf("arm the clock: %v", err)
	}
	return &chipRun{ctx: ctx, harness: harness}
}

// populate puts recording devices on pins d0 upward.
func (r *chipRun) populate(t *testing.T, count int) {
	t.Helper()
	if err := r.harness.AddDevices(r.ctx, count); err != nil {
		t.Fatalf("lay out a housing of %d pins: %v", count, err)
	}
}

// device names one pin on this run.
func (r *chipRun) device(pin int) *device {
	return &device{ctx: r.ctx, harness: r.harness, pin: pin}
}

// load compiles assembly onto the chip. It runs after the devices go on,
// because loading does not disturb them and a reset would; it leaves the
// clock alone for the same reason.
func (r *chipRun) load(t *testing.T, assembly string) {
	t.Helper()
	if err := r.harness.Load(r.ctx, assembly); err != nil {
		t.Fatalf("the emitted assembly does not load:\n%s\n%v", assembly, err)
	}
	r.assembly = assembly
	state, err := r.harness.State(r.ctx)
	if err != nil {
		t.Fatalf("read the chip's state: %v", err)
	}
	if state.CompileError.Type != chip.ExcNone {
		t.Fatalf("the chip refused the emitted assembly: %s\n%s", state.CompileError, assembly)
	}
	r.last = chip.Segment{Snapshot: state}
}

// step retires up to budget instructions and keeps what the chip answered.
func (r *chipRun) step(t *testing.T, budget int) chip.Segment {
	t.Helper()
	segment, err := r.harness.Step(r.ctx, budget)
	if err != nil {
		t.Fatalf("run the chip: %v\n%s", err, r.assembly)
	}
	r.last = segment
	return segment
}

// pc is the line the next segment would run, and lineCount how many compiled.
func (r *chipRun) pc() int        { return r.last.Address }
func (r *chipRun) lineCount() int { return r.last.LineCount }

// running reports whether the program counter is still inside the program,
// which is what a run ends by leaving.
func (r *chipRun) running() bool { return r.pc() >= 0 && r.pc() < r.lineCount() }

// register reads one register off the last segment's snapshot.
func (r *chipRun) register(reg ic10.Register) float64 { return r.last.Registers[reg] }

// setRegister writes one register, whatever the chip already holds there.
func (r *chipRun) setRegister(t *testing.T, reg ic10.Register, value float64) {
	t.Helper()
	if err := r.harness.SetRegister(r.ctx, reg, value); err != nil {
		t.Fatalf("write r%d: %v", reg, err)
	}
	r.last.Registers[reg] = value
}

// setMemory fills the chip's whole slot array, which is what a case standing in
// for state a previous program left behind needs.
func (r *chipRun) setMemory(t *testing.T, value float64) {
	t.Helper()
	if err := r.harness.FillStack(r.ctx, value); err != nil {
		t.Fatalf("seed the slot array: %v", err)
	}
}

// faulted fails the test when the last segment stopped on a chip fault, which
// is never what these programs are meant to do.
func (r *chipRun) faulted(t *testing.T) {
	t.Helper()
	if r.last.Stop == chip.StopFaulted {
		t.Fatalf("the chip faulted at line %d: %s\n%s", r.last.Fault.Line, r.last.Fault, r.assembly)
	}
}

// devicePair is the housing most cases want: one device to read from on d0 and
// one to write to on d1.
func devicePair(t *testing.T) (*chipRun, *device, *device) {
	t.Helper()
	run := newChipRun(t)
	run.populate(t, 2)
	return run, run.device(0), run.device(1)
}

// seeding collects world changes for one device so that a stimulus laying a
// whole roster out pays one exchange rather than one per property. Every
// property is named the way a program names it and resolved against the
// generated tables here, so a stimulus naming one property cannot seed another.
type seeding struct {
	device *device
	world  chip.Seeding
}

// seedDevice builds one batch of world changes and sends it. Building and
// sending are one call rather than two: a batch nobody sent is a device left
// answering zero everywhere, which reads downstream as a program whose
// branches all decided the same thing rather than as a mistake in the stimulus.
func seedDevice(t *testing.T, d *device, build func(*seeding)) {
	t.Helper()
	s := &seeding{device: d}
	build(s)
	if err := d.harness.SeedWorld(d.ctx, &s.world); err != nil {
		t.Fatalf("seed the world on d%d: %v", d.pin, err)
	}
}

func (s *seeding) logic(t *testing.T, name string, value float64) {
	t.Helper()
	s.world.Property(s.device.pin, logicType(t, name), value)
}

// slot names a slot property the way the source does, so a slot type whose bare
// name the logic family took is written here with the prefix a program needs.
func (s *seeding) slot(t *testing.T, name string, slot int, value float64) {
	t.Helper()
	info, ok := ic10.LookupLogicSlotType(strings.TrimPrefix(name, ic10.SlotTypePrefix))
	if !ok {
		t.Fatalf("the instruction tables name no slot type %q", name)
	}
	s.world.SlotProperty(s.device.pin, slot, info.Value, value)
}

// reagent names the reagent by the short type name the game hashes it by, which
// is what a program names it with through __ic_hash, so the world and the
// program reach the same reagent without either restating the hash.
func (s *seeding) reagent(t *testing.T, mode, reagent string, value float64) {
	t.Helper()
	info, ok := ic10.LookupReagentMode(mode)
	if !ok {
		t.Fatalf("the instruction tables name no reagent mode %q", mode)
	}
	s.world.Reagent(s.device.pin, info.Value, reagent, value)
}

func (s *seeding) hashes(prefab, name int) { s.world.Hashes(s.device.pin, prefab, name) }

// setLogic seeds one property of one device, which is the world rather than
// anything the program did and so leaves no trace behind.
func setLogic(t *testing.T, d *device, name string, value float64) {
	t.Helper()
	seedDevice(t, d, func(s *seeding) { s.logic(t, name, value) })
}

// setPrefab gives a device the prefab hash a batch instruction selects on,
// and setHashes that plus the name hash the filtered forms narrow with. A
// device the filtered forms must reach needs one setHashes naming both:
// setPrefab alone leaves the name at zero, which no filtered batch selects.
func setPrefab(t *testing.T, d *device, prefab string) {
	t.Helper()
	setHashes(t, d, ic10.HashName(prefab), 0)
}

func setHashes(t *testing.T, d *device, prefab, name int) {
	t.Helper()
	seedDevice(t, d, func(s *seeding) { s.hashes(prefab, name) })
}

// logicValue reads back one property of one device, through the getter the
// chip's own load instruction reads it with.
func logicValue(t *testing.T, d *device, name string) float64 {
	t.Helper()
	value, err := d.harness.Property(d.ctx, chip.Pin(d.pin), logicType(t, name))
	if err != nil {
		t.Fatalf("read %s off d%d: %v", name, d.pin, err)
	}
	return value
}

// housedChip puts assembly on a chip whose housing is already populated.
func housedChip(t *testing.T, assembly string, housing *chipRun) *chipRun {
	t.Helper()
	housing.load(t, assembly)
	return housing
}

// runTicks advances the chip, failing on a fault or on the program ending.
func runTicks(t *testing.T, housing *chipRun, ticks int, assembly string) {
	t.Helper()
	for tick := range ticks {
		if !housing.running() {
			t.Fatalf("the program left its own instructions after %d ticks, so it runs once and stops:\n%s",
				tick, assembly)
		}
		housing.step(t, chip.InstructionsPerTick)
		housing.faulted(t)
	}
}

// runToEnd runs until the program counter leaves the program, which is how the
// chip ends a run.
func runToEnd(t *testing.T, housing *chipRun, assembly string) {
	t.Helper()
	for tick := 0; housing.running(); tick++ {
		if tick > maxChipTicks {
			t.Fatalf("the program never left its own instructions:\n%s", assembly)
		}
		housing.step(t, chip.InstructionsPerTick)
		housing.faulted(t)
	}
}
