package isel

import (
	"fmt"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/ic10"
)

// foldedShift writes the distance out, so the constant fold in IR generation
// answers and the emitted program holds no shift. The shifted value is a prefab
// name hash because that is the only constant the front end cannot evaluate: it
// is resolved during generation, which puts it in front of the fold.
const foldedShift = `const dev out = d1;

void main(void) {
    while (true) {
        __ic_store(out, Setting, (double)(__ic_hash("%s") %s %d));
        __ic_yield();
    }
}`

// machineShift is the same shift with the distance read from a device, which
// keeps every fold off it and leaves the chip's own instruction to answer.
const machineShift = `const dev in = d0;
const dev out = d1;

void main(void) {
    while (true) {
        long long d = (long long)__ic_load(in, Setting);
        __ic_store(out, Setting, (double)(__ic_hash("%s") %s d));
        __ic_yield();
    }
}`

// TestFoldedShiftAnswersWhatTheMachineWould holds the constant shift fold to the
// chip: each row is compiled twice, once so the fold answers and once with the
// distance read from a device so it cannot, and the two are held to the same
// bits. The fold declines at 64, where Go counts past the width and the chip does not.
func TestFoldedShiftAnswersWhatTheMachineWould(t *testing.T) {
	cases := []struct {
		name   string
		prefab string
		op     string
		// distance is the shift count, and is also the reading the machine form
		// is given.
		distance int
		// folded says the emitted program holds no shift instruction, because
		// the fold answered for it.
		folded bool
	}{
		{name: "a right shift just below the old window", prefab: "Pump", op: ">>", distance: 51, folded: true},
		{name: "a right shift at the old window's edge", prefab: "Pump", op: ">>", distance: 52, folded: true},
		{name: "a right shift inside the widened window", prefab: "Pump", op: ">>", distance: 55, folded: true},
		{name: "a right shift at the last distance the fold takes", prefab: "Pump", op: ">>", distance: 63, folded: true},
		{name: "a negative hash shifted inside the widened window", prefab: "StructureWallLight", op: ">>", distance: 55, folded: true},
		{name: "a negative hash shifted to nothing but its sign", prefab: "StructureWallLight", op: ">>", distance: 63, folded: true},
		{name: "a left shift the window will not carry an answer for", prefab: "Pump", op: "<<", distance: 55},
		{name: "a left shift of a negative hash", prefab: "StructureWallLight", op: "<<", distance: 55},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			folded := compileSource(t, fmt.Sprintf(foldedShift, tc.prefab, tc.op, tc.distance))
			if held := holdsAShift(folded); held == tc.folded {
				t.Fatalf("the emitted program holds a shift = %v, want %v\n%s", held, !tc.folded, folded)
			}
			machine := compileSource(t, fmt.Sprintf(machineShift, tc.prefab, tc.op))
			if !holdsAShift(machine) {
				t.Fatalf("the machine form holds no shift, so it answers nothing the fold could be held to\n%s", machine)
			}

			want := lastWrite(t, runWorld(t, machine, func(t *testing.T, w *world) {
				w.set(t, 0, logicType(t, "Setting"), float64(tc.distance))
			}, 1), 1, logicType(t, "Setting"), machine)
			events := runWorld(t, folded, func(*testing.T, *world) {}, 1)
			assertWrote(t, events, 1, logicType(t, "Setting"), want, folded)
		})
	}
}

// holdsAShift separates a program the fold answered for from one it declined.
func holdsAShift(assembly string) bool {
	for line := range strings.SplitSeq(assembly, "\n") {
		switch mnemonic, _, _ := strings.Cut(strings.TrimSpace(line), " "); mnemonic {
		case "sra", "srl", "sla", "sll":
			return true
		}
	}
	return false
}

// lastWrite is the value a run left on one property, for a case whose expected
// answer is another run rather than a number written here.
func lastWrite(t *testing.T, events []chip.Write, pin int, property ic10.LogicType, assembly string) float64 {
	t.Helper()
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.Pin == pin && event.Property == int(property) && event.Slot == chip.NoSlot {
			return event.Value
		}
	}
	t.Fatalf("the program made no write to d%d %v; it wrote %s\n%s", pin, property, describeWrites(events), assembly)
	return 0
}
