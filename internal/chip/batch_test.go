package chip

import (
	"math"
	"strconv"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
)

// batchOrderPrefab is the structure hash both devices in the test below carry,
// so that nothing but the fold order decides which of them answers.
const batchOrderPrefab = 1234

// TestBatchFoldWalksDevicesSortedByName settles what decides fold order: the
// game sorts the housing's cable devices by display name before folding
// backwards, so Minimum/Maximum answer for whichever device the walk reaches
// first and Sum adds in an order that is not commutative. The three cases
// check both that name order (not cable order) decides, and that swapping
// which name gets which value flips the answer. NaN vs a number makes the
// difference maximal, since the fold's `!(v >= x)` guard fails for NaN on
// either side.
func TestBatchFoldWalksDevicesSortedByName(t *testing.T) {
	ctx, fixtures := liveFixtures(t)

	setting := logicType(t, "Setting")
	source := "lb r0 " + strconv.Itoa(batchOrderPrefab) + " " + strconv.Itoa(int(setting)) + " Minimum\n" +
		"lb r1 " + strconv.Itoa(batchOrderPrefab) + " " + strconv.Itoa(int(setting)) + " Maximum"

	type device struct {
		pin   int
		name  string
		value float64
	}
	tests := []struct {
		name    string
		cable   []device
		wantNaN bool
	}{
		{
			name:    "nan sorts first",
			cable:   []device{{pin: 0, name: "a", value: math.NaN()}, {pin: 1, name: "b", value: 5}},
			wantNaN: true,
		},
		{
			// The same two names, added to the cable the other way round.
			name:    "nan sorts first, added last",
			cable:   []device{{pin: 1, name: "b", value: 5}, {pin: 0, name: "a", value: math.NaN()}},
			wantNaN: true,
		},
		{
			// The cable order of the first case with the names exchanged, which
			// is the case that separates a name sort from a cable order.
			name:    "nan sorts last",
			cable:   []device{{pin: 0, name: "b", value: math.NaN()}, {pin: 1, name: "a", value: 5}},
			wantNaN: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := fixtures.Reset(ctx); err != nil {
				t.Fatalf("reset: %v", err)
			}
			var world Seeding
			for _, d := range tt.cable {
				world.AddDevice(d.pin)
				world.DisplayName(d.pin, d.name)
				world.Hashes(d.pin, batchOrderPrefab, 0)
				world.Property(d.pin, setting, d.value)
			}
			if err := fixtures.SeedWorld(ctx, &world); err != nil {
				t.Fatalf("lay the cable out: %v", err)
			}
			if err := fixtures.Load(ctx, source); err != nil {
				t.Fatalf("load: %v", err)
			}
			segment, err := fixtures.Step(ctx, InstructionsPerTick)
			if err != nil {
				t.Fatalf("step: %v", err)
			}
			if segment.Stop != StopEnded {
				t.Fatalf("stop = %q, fault %s", segment.Stop, segment.Fault)
			}
			for register, mode := range map[int]string{0: "Minimum", 1: "Maximum"} {
				got := segment.Registers[register]
				if math.IsNaN(got) != tt.wantNaN {
					t.Errorf("%s answered %v, want NaN=%t", mode, got, tt.wantNaN)
				}
				if !tt.wantNaN && got != 5 {
					t.Errorf("%s answered %v, want the 5 the other device holds", mode, got)
				}
			}
		})
	}
}

// TestStateReadsZeroWhenTheGameWouldNotSyncIt covers the branch every device
// state property reads through: the game answers zero for an unsynchronised
// interactable regardless of its stored value, so a harness ignoring the sync
// flag would only ever answer one of the game's two configurations. The
// housing is the target since it's the one device a faithful process has
// without building one.
func TestStateReadsZeroWhenTheGameWouldNotSyncIt(t *testing.T) {
	ctx, harness := liveHarness(t)

	on := logicType(t, "On")
	for _, tt := range []struct {
		name   string
		synced bool
		want   float64
	}{
		{name: "synced", synced: true, want: 1},
		{name: "not synced", synced: false, want: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := harness.Reset(ctx); err != nil {
				t.Fatalf("reset: %v", err)
			}
			if err := harness.SetFlag(ctx, Housing, "HasOnOffState", true); err != nil {
				t.Fatalf("publish the housing's On state: %v", err)
			}
			if err := harness.SetState(ctx, Housing, on, 1); err != nil {
				t.Fatalf("seed the housing's On state: %v", err)
			}
			if err := harness.SetSynced(ctx, Housing, on, tt.synced); err != nil {
				t.Fatalf("set whether the state is synchronised: %v", err)
			}
			got, err := harness.Property(ctx, Housing, on)
			if err != nil {
				t.Fatalf("read the housing's On: %v", err)
			}
			if got != tt.want {
				t.Errorf("On reads %v, want %v", got, tt.want)
			}
		})
	}
}

// TestLimitsMatchTheConstantsCopiedFromThem holds the two machine sizes this
// tree spells out (segment size, pin count) to the ones the game declares.
// tools/chipgen's digest catches a game update that moves either in the
// compiled slice, but not a stale Go constant mirroring it — this test is what
// catches that.
func TestLimitsMatchTheConstantsCopiedFromThem(t *testing.T) {
	ctx, harness := liveHarness(t)

	got, err := harness.Limits(ctx)
	if err != nil {
		t.Fatalf("read the chip's machine limits: %v", err)
	}
	if got.InstructionsPerTick != InstructionsPerTick {
		t.Errorf("the game spends %d instructions a tick and InstructionsPerTick is %d",
			got.InstructionsPerTick, InstructionsPerTick)
	}
	if got.DevicePins != ic10.NumDevicePins {
		t.Errorf("the housing has %d pins and ic10.NumDevicePins is %d",
			got.DevicePins, ic10.NumDevicePins)
	}
}
