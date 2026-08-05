package isel

import (
	"context"
	"slices"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/chiptest"
	"github.com/greg2010/ic11c/internal/devtrace"
	"github.com/greg2010/ic11c/internal/emit"
	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/irgen"
	"github.com/greg2010/ic11c/internal/llvmopt"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/peephole"
	"github.com/greg2010/ic11c/internal/regalloc"
	"github.com/greg2010/ic11c/internal/sema"
	"github.com/greg2010/ic11c/internal/source"
	"github.com/greg2010/ic11c/internal/tsparse"
)

// sourceFile is the name compiled programs carry, so a diagnostic can be read
// against a position as well as a message.
const sourceFile = "source.c"

// selectSource runs the front end and the optimizer over src in the order
// cmd/ic11c uses, spelled out because the command is not a library. Selection's
// error is returned rather than failing the test, which is what lets a case
// whose subject is a refusal read the diagnostic; every earlier stage fails.
func selectSource(t *testing.T, src string) (*Result, error) {
	t.Helper()
	file, diags, err := tsparse.Parse(sourceFile, src)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if diags.HasErrors() {
		t.Fatalf("parsing:\n%s", diags.String())
	}
	analyzed, diags, err := sema.Analyze(t.Context(), file, sema.Shipped{})
	if err != nil {
		t.Fatalf("analyzing: %v", err)
	}
	if diags.HasErrors() {
		t.Fatalf("analyzing:\n%s", diags.String())
	}
	module, err := irgen.Generate(t.Context(), analyzed, irgen.Options{ModuleName: sourceFile})
	if err != nil {
		t.Fatalf("generating IR: %v", err)
	}
	t.Cleanup(module.Dispose)
	if err := llvmopt.Run(t.Context(), module.Module, llvmopt.Options{}); err != nil {
		t.Fatalf("optimizing: %v", err)
	}
	return Select(t.Context(), module.Module, Options{
		File:        sourceFile,
		Lines:       source.NewLineMap(src),
		InlineSites: module.InlineSites,
	})
}

// compileSource runs the whole pipeline over src and answers the assembly a chip
// loads, which is what a case about the machine's own behaviour needs and no
// single stage produces.
func compileSource(t *testing.T, src string) string {
	t.Helper()
	selected, err := selectSource(t, src)
	if err != nil {
		t.Fatalf("selecting instructions: %v", err)
	}
	cfg := regalloc.Config{Scratch: regalloc.DefaultScratch(), SpillSlotBase: selected.DataSlots}
	if selected.CallingConvention {
		cfg.Reserved = []ic10.Register{ic10.RegSP, ic10.RegRA}
	}
	for _, fn := range selected.Program.Funcs {
		if form := fn.RegForm(); form == mir.RegFormPhysical || form == mir.RegFormEmpty {
			continue
		}
		allocated, allocErr := regalloc.Allocate(fn, cfg)
		if allocErr != nil {
			t.Fatalf("allocating registers for %s: %v", fn.Name, allocErr)
		}
		cfg.SpillSlotBase += allocated.SpillSlots
	}
	if selected.CallingConvention {
		if err := regalloc.SetStackBase(selected.Program.Funcs[0], cfg.SpillSlotBase); err != nil {
			t.Fatalf("setting the stack base: %v", err)
		}
	}
	peephole.Run(selected.Program)
	if err := selected.Program.CheckPlacement(); err != nil {
		t.Fatalf("placement: %v", err)
	}
	out, err := emit.Emit(selected.Program, emit.Options{})
	if err != nil {
		t.Fatalf("emitting: %v", err)
	}
	return out.Text
}

// logicType resolves a device property by the name the machine table gives it.
// The table is generated from the game's own assembly, so a case that spelled an
// ordinal would go stale silently.
func logicType(t *testing.T, name string) ic10.LogicType {
	t.Helper()
	for _, info := range ic10.LogicTypes {
		if info.Name == name {
			return ic10.LogicType(info.Value)
		}
	}
	t.Fatalf("the machine has no logic type named %q", name)
	return 0
}

// logicSlotType resolves a slot property by the name the machine table gives
// it, for the reason [logicType] is not a constant either.
func logicSlotType(t *testing.T, name string) ic10.LogicSlotType {
	t.Helper()
	for _, info := range ic10.LogicSlotTypes {
		if info.Name == name {
			return ic10.LogicSlotType(info.Value)
		}
	}
	t.Fatalf("the machine has no slot type named %q", name)
	return 0
}

// unwritten is what every output property is seeded with. The chip skips a store
// that would not change the value already there, so seeding a value no case
// expects is what keeps "wrote the right answer" and "wrote nothing at all" two
// outcomes rather than one.
const unwritten = -987654321.0

// runWorld loads assembly onto a chip surrounded by devices and answers every
// write it made, failing the test for a run the chip refused. Every program here
// is one C runs to completion, so a chip that stops has already disagreed.
func runWorld(t *testing.T, assembly string, prepare worldSetup, segments int) []chip.Write {
	t.Helper()
	trace := traceWorld(t, assembly, prepare, segments)
	if trace.Stop.Reason == devtrace.StopFaulted {
		t.Fatalf("the chip stopped: %s\n%s", trace.Stop, assembly)
	}
	return trace.Events
}

// traceWorld is [runWorld] for a case whose subject is the chip's own verdict,
// which it hands back rather than failing on.
func traceWorld(t *testing.T, assembly string, prepare worldSetup, segments int) devtrace.Trace {
	t.Helper()
	ctx, harness := chiptest.Fixtures(t)
	return devtrace.Run(ctx, t, harness, assembly, devtrace.RunOptions{
		Name:     "chip",
		Segments: segments,
		World: func(t *testing.T, h *chip.FixtureHarness) {
			t.Helper()
			for pin := range tracedPins {
				if err := h.AddDevice(ctx, pin); err != nil {
					t.Fatalf("put a device on d%d: %v", pin, err)
				}
				if err := h.SetProperties(ctx, pin, everyLogicType(), unwritten); err != nil {
					t.Fatalf("seed the properties of d%d: %v", pin, err)
				}
			}
			prepare(t, &world{ctx: ctx, harness: h})
		},
	})
}

// tracedPins is how many pins a traced case runs among.
const tracedPins = 3

// everyLogicType is the machine's whole property roster, which is what a traced
// device is seeded across.
func everyLogicType() []ic10.LogicType {
	properties := make([]ic10.LogicType, len(ic10.LogicTypes))
	for i, info := range ic10.LogicTypes {
		properties[i] = info.Value
	}
	return properties
}

// world is the housing a case seeds before its program runs.
type world struct {
	ctx     context.Context
	harness *chip.FixtureHarness
}

// worldSetup seeds one case's readings.
type worldSetup func(t *testing.T, w *world)

// set seeds one logic property of one pin.
func (w *world) set(t *testing.T, pin int, property ic10.LogicType, value float64) {
	t.Helper()
	if err := w.harness.SetProperty(w.ctx, pin, property, value); err != nil {
		t.Fatalf("seed property %v on d%d: %v", property, pin, err)
	}
}

// setSlot seeds one slot property of one pin.
func (w *world) setSlot(t *testing.T, pin, slot int, property ic10.LogicSlotType, value float64) {
	t.Helper()
	if err := w.harness.SetSlotProperty(w.ctx, pin, slot, property, value); err != nil {
		t.Fatalf("seed slot %d property %v on d%d: %v", slot, property, pin, err)
	}
}

// setHashes gives a pin the prefab and name hashes a batch form selects on.
func (w *world) setHashes(t *testing.T, pin, prefab, name int) {
	t.Helper()
	if err := w.harness.SetHashes(w.ctx, pin, prefab, name); err != nil {
		t.Fatalf("give d%d its hashes: %v", pin, err)
	}
}

// wroteProperty reports whether the program wrote one property of one device,
// which is all a case about a batch the program never named needs: the value is
// the one it meant to write and the pin is not.
func wroteProperty(events []chip.Write, pin int, property ic10.LogicType) bool {
	return slices.ContainsFunc(events, func(event chip.Write) bool {
		return event.Pin == pin && event.Property == int(property) && event.Slot == chip.NoSlot
	})
}
