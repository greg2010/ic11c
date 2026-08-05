package chip

import (
	"math"
	"strconv"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
)

// TestPropertyReadsBackWhatAProgramLeft covers the read a caller checking
// what a program left behind needs: a trace carries only what changed, so a
// property seeded and never written, or written back to its existing value,
// appears in no trace and can be read back nowhere else.
func TestPropertyReadsBackWhatAProgramLeft(t *testing.T) {
	ctx, fixtures := liveFixtures(t)

	setting := logicType(t, "Setting")
	occupied := slotType(t, "Occupied")

	if err := fixtures.AddDevice(ctx, 0); err != nil {
		t.Fatalf("add a fixture device: %v", err)
	}
	if err := fixtures.SetProperty(ctx, 0, setting, 100); err != nil {
		t.Fatalf("seed Setting: %v", err)
	}
	if err := fixtures.SetSlotProperty(ctx, 0, 1, occupied, 2); err != nil {
		t.Fatalf("seed a slot: %v", err)
	}

	// Seeded and never written, so the trace cannot answer for either.
	got, err := fixtures.Property(ctx, Pin(0), setting)
	if err != nil {
		t.Fatalf("read Setting back: %v", err)
	}
	if got != 100 {
		t.Errorf("the seeded property reads back as %v, want 100", got)
	}
	slot, err := fixtures.SlotProperty(ctx, Pin(0), 1, occupied)
	if err != nil {
		t.Fatalf("read the slot back: %v", err)
	}
	if slot != 2 {
		t.Errorf("the seeded slot property reads back as %v, want 2", slot)
	}

	if err := fixtures.Load(ctx, "s d0 "+strconv.Itoa(int(setting))+" 250"); err != nil {
		t.Fatalf("load: %v", err)
	}
	segment, err := fixtures.Step(ctx, InstructionsPerTick)
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	if segment.Stop != StopEnded {
		t.Fatalf("stop = %q, error %s", segment.Stop, segment.Fault)
	}
	got, err = fixtures.Property(ctx, Pin(0), setting)
	if err != nil {
		t.Fatalf("read Setting back: %v", err)
	}
	if got != 250 {
		t.Errorf("the written property reads back as %v, want 250", got)
	}
}

// TestPropertyReachesTheHousing covers the one target that is not a pin. A
// program writing to db changes state the state block reports rather than any
// device's, so the housing is the one place a write lands that no trace holds.
func TestPropertyReachesTheHousing(t *testing.T) {
	ctx, harness := liveHarness(t)

	setting := logicType(t, "Setting")
	if err := harness.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := harness.Load(ctx, "s db "+strconv.Itoa(int(setting))+" 42"); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := harness.Step(ctx, InstructionsPerTick); err != nil {
		t.Fatalf("step: %v", err)
	}
	got, err := harness.Property(ctx, Housing, setting)
	if err != nil {
		t.Fatalf("read the housing's Setting: %v", err)
	}
	if got != 42 {
		t.Errorf("the housing's Setting is %v, want 42", got)
	}
}

// TestPropertyCarriesTheValuesADecimalWouldLose holds the read-back verbs to the
// same protocol the state block is held to. A getter that copied a struct or a
// rendering that dropped a sign would answer a number nobody wrote.
func TestPropertyCarriesTheValuesADecimalWouldLose(t *testing.T) {
	ctx, fixtures := liveFixtures(t)

	setting := logicType(t, "Setting")
	if err := fixtures.AddDevice(ctx, 0); err != nil {
		t.Fatalf("add a fixture device: %v", err)
	}
	tests := []struct {
		name  string
		value float64
	}{
		{name: "negative zero", value: math.Copysign(0, -1)},
		{name: "a nan carrying a payload", value: math.Float64frombits(0x7ff8000000dead01)},
		{name: "negative infinity", value: math.Inf(-1)},
		{name: "a value one ulp under pi", value: math.Nextafter(math.Pi, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := fixtures.SetProperty(ctx, 0, setting, tt.value); err != nil {
				t.Fatalf("seed Setting: %v", err)
			}
			got, err := fixtures.Property(ctx, Pin(0), setting)
			if err != nil {
				t.Fatalf("read Setting back: %v", err)
			}
			if bits, want := math.Float64bits(got), math.Float64bits(tt.value); bits != want {
				t.Errorf("read back %016x, want %016x", bits, want)
			}
		})
	}
}

// TestSetReagentSeedsWhatLoadReagentReads covers the reagent surface a
// traced program reaches: a program names a reagent with the hash of its
// short type name, which is what __ic_hash computes, and the seed names it
// with that same name, hashed the same way — the two agree by construction.
func TestSetReagentSeedsWhatLoadReagentReads(t *testing.T) {
	ctx, fixtures := liveFixtures(t)

	if err := fixtures.AddDevice(ctx, 0); err != nil {
		t.Fatalf("add a fixture device: %v", err)
	}

	tests := []struct {
		name  string
		mode  string
		value float64
	}{
		{name: "what the machine holds", mode: "Contents", value: 12.5},
		{name: "what the recipe asks of it", mode: "Required", value: 30},
		{name: "what one unit costs", mode: "Recipe", value: 2.25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, ok := ic10.LookupReagentMode(tt.mode)
			if !ok {
				t.Fatalf("the tables name no reagent mode %q", tt.mode)
			}
			if err := fixtures.SetReagent(ctx, 0, info.Value, "Copper", tt.value); err != nil {
				t.Fatalf("seed the reagent: %v", err)
			}
			// The hash is spelled as the number a compiled program carries,
			// which is what the emitted literal is, so nothing here can seed
			// one reagent and read another.
			source := "lr r0 d0 " + strconv.Itoa(int(info.Value)) + " " + strconv.Itoa(ic10.HashName("Copper"))
			if err := fixtures.Load(ctx, source); err != nil {
				t.Fatalf("load %q: %v", source, err)
			}
			segment, err := fixtures.Step(ctx, InstructionsPerTick)
			if err != nil {
				t.Fatalf("step: %v", err)
			}
			if segment.Stop != StopEnded {
				t.Fatalf("%q stopped %s, error %s, compile %s",
					source, segment.Stop, segment.Fault, segment.CompileError)
			}
			if segment.Registers[0] != tt.value {
				t.Errorf("lr under %s answered %v, want %v", tt.mode, segment.Registers[0], tt.value)
			}
		})
	}
}

// TestSetReagentRefusesWhatItCannotSeed covers the two refusals, both of which
// would otherwise be a seed that lands nowhere and reads back as a reagent the
// device never held.
func TestSetReagentRefusesWhatItCannotSeed(t *testing.T) {
	ctx, fixtures := liveFixtures(t)

	if err := fixtures.AddDevice(ctx, 0); err != nil {
		t.Fatalf("add a fixture device: %v", err)
	}
	total, ok := ic10.LookupReagentMode("TotalContents")
	if !ok {
		t.Fatal("the tables name no TotalContents reagent mode")
	}
	if err := fixtures.SetReagent(ctx, 0, total.Value, "Copper", 1); err == nil {
		t.Error("TotalContents was seeded directly, so a total need not add up to its parts")
	}

	contents, ok := ic10.LookupReagentMode("Contents")
	if !ok {
		t.Fatal("the tables name no Contents reagent mode")
	}
	if err := fixtures.SetReagent(ctx, 0, contents.Value, "NoSuchReagent", 1); err == nil {
		t.Error("a reagent the game does not carry was seeded onto the device")
	}
}
