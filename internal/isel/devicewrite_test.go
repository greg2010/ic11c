package isel

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/ic10"
)

// assertWrote holds the last write to one property against a value, comparing
// bit patterns so the two zeroes stay apart. A device write is the one surface
// that separates -0.0 from the 0.0 C names, and a case written as `== 0` passes
// on a compiler that writes the other one.
func assertWrote(t *testing.T, events []chip.Write, pin int, property ic10.LogicType, want float64, assembly string) {
	t.Helper()
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.Pin != pin || event.Property != int(property) || event.Slot != chip.NoSlot {
			continue
		}
		if math.Float64bits(event.Value) != math.Float64bits(want) {
			t.Errorf("the program wrote d%d %v = %v, want %v\n%s", pin, property, event.Value, want, assembly)
		}
		return
	}
	t.Errorf("the program made no write to d%d %v; it wrote %s\n%s", pin, property, describeWrites(events), assembly)
}

func describeWrites(events []chip.Write) string {
	if len(events) == 0 {
		return "nothing"
	}
	written := make([]string, len(events))
	for i, event := range events {
		written[i] = describeWrite(event)
	}
	return strings.Join(written, ", ")
}

// describeWrite renders one write for a failure message. The value carries its
// bit pattern because the two zeroes and a NaN spell themselves the same way.
func describeWrite(w chip.Write) string {
	slot := ""
	if w.Slot != chip.NoSlot {
		slot = " slot " + strconv.Itoa(w.Slot)
	}
	return fmt.Sprintf("d%d%s property %d = %v (%016x)",
		w.Pin, slot, w.Property, w.Value, math.Float64bits(w.Value))
}
