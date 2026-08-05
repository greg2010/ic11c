package devtrace

import (
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/chiptest"
	"github.com/greg2010/ic11c/internal/ic10"
)

func TestMain(m *testing.M) { chiptest.Main(m) }

// pins lays out a world of recording devices on d0 upward.
func pins(count int) World {
	return func(t *testing.T, h *chip.FixtureHarness) {
		t.Helper()
		if err := h.AddDevices(t.Context(), count); err != nil {
			t.Fatalf("lay out %d pins: %v", count, err)
		}
	}
}

// traceProgram traces one program in a world of count pins.
func traceProgram(t *testing.T, assembly string, count int, opts RunOptions) Trace {
	t.Helper()
	ctx, h := chiptest.Fixtures(t)
	if opts.World == nil {
		opts.World = pins(count)
	}
	return Run(ctx, t, h, assembly, opts)
}

// logicType resolves a property name against the generated tables, so that a
// test naming one property cannot seed another.
func logicType(t *testing.T, name string) ic10.LogicType {
	t.Helper()
	info, ok := ic10.LookupLogicType(name)
	if !ok {
		t.Fatalf("the instruction tables name no logic type %q", name)
	}
	return info.Value
}

// slotType resolves a slot property name against the generated tables.
func slotType(t *testing.T, name string) ic10.LogicSlotType {
	t.Helper()
	info, ok := ic10.LookupLogicSlotType(name)
	if !ok {
		t.Fatalf("the instruction tables name no slot type %q", name)
	}
	return info.Value
}

// write builds one expected write.
func write(pin, property, slot int, value float64) chip.Write {
	return chip.Write{Pin: pin, Property: property, Slot: slot, Value: value}
}
