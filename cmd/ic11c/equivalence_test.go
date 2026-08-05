package main

import (
	"flag"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/devtrace"
	"github.com/greg2010/ic11c/internal/ic10"
)

// unoptimizedFlag skips the optimizer, emitting what IR generation produced.
const unoptimizedFlag = "--no-optimize"

// comparisonSegments is how many turns of its control loop a fixture is driven
// for. It is far more than any fixture's world takes to come back round, so a
// program is compared over its steady state and not only over the first pass
// through its loop.
const comparisonSegments = 200

// reasonRecursiveLocal is why airlock.c has no unoptimized form to compare
// against: a recursive function's local needs a data region slot, one
// address serves every activation, and only SROA makes that local disappear.
const reasonRecursiveLocal = "a recursive function's local needs a data region slot, " +
	"one address serves every activation, and only SROA makes the local disappear"

// unoptimizedRejection is the part of the diagnostic that carries that reason.
// requireRefusalReason matches on it, so a rewording that drops the reason is a
// failure rather than a silent pass.
const unoptimizedRejection = "can reach itself through a call, and this local needs a data region slot"

// refusalMarker is the part of the diagnostic each reason turns on, which is
// what holds the reason to the refusal. A reason with no marker is a reason
// nothing checks, so requireRefusalReason refuses to pass a fixture stating one.
var refusalMarker = map[string]string{
	reasonRecursiveLocal: unoptimizedRejection,
}

// baselineFile holds what every fixture was measured to produce, and the floors
// under the comparison are derived from it.
const baselineFile = "testdata/equivalence.json"

// updateBaseline rewrites baselineFile from what this run measured.
var updateBaseline = flag.Bool("update-baseline", false,
	"rewrite "+baselineFile+" from what this run measures")

// equivalenceBaseline is one number per fixture.
var equivalenceBaseline = baseline[int]{
	file:     baselineFile,
	task:     equivalenceBaselineTask,
	update:   updateBaseline,
	excluded: func(name string) bool { _, out := unsupportedUnoptimized[name]; return out },
	missing: func(name string, writes int) []string {
		if writes < measurableWrites {
			return []string{name}
		}
		return nil
	},
}

// measurableWrites is the least production a program may be baselined at,
// and the least a recorded one is read back as: a store of the value
// already there is skipped, so an undriven fixture writes once and goes
// quiet, well under this floor.
const measurableWrites = comparisonSegments / 20

// A machine reading the write counts off a run is the right authority for them,
// which is why the baseline is regenerated rather than written by hand. That is
// not true of the two tables below: a fixture can join either of those by
// breaking, and a regeneration would adopt the breakage as the new normal.

// unsupportedUnoptimized names every fixture --no-optimize refuses outright,
// and why; such a program has no unoptimized build to compare against.
// Written by hand and asserted in both directions: a fixture that newly
// stops compiling must not join this set by failing, only by a deliberate edit.
var unsupportedUnoptimized = map[string]string{
	"airlock.c":     reasonRecursiveLocal,
	"call_ladder.c": reasonRecursiveLocal,
	"frames.c":      reasonRecursiveLocal,
}

// overEditorLimit names every fixture whose unoptimized build is larger
// than the in-game editor accepts. Such a program is still compared, since
// the interpreter imposes no line cap; written by hand, like unsupportedUnoptimized.
var overEditorLimit = map[string]bool{
	"alloy_table.c":  true,
	"bits.c":         true,
	"hab_monitor.c":  true,
	"liquid_train.c": true,
}

// liquidTrainContaminants are the substances liquid_train.c adds up at each
// tap. The pipe answers zero for a substance nothing seeded, which would leave
// the audit summing one reading and the rest of the list contributing nothing,
// so every name the program reads is driven.
var liquidTrainContaminants = []string{
	"RatioLiquidHydrochloricAcid",
	"RatioLiquidSodiumChloride",
	"RatioLiquidCarbonDioxide",
	"RatioLiquidNitrousOxide",
	"RatioLiquidPollutant",
	"RatioLiquidHydrazine",
	"RatioLiquidNitrogen",
	"RatioLiquidHydrogen",
	"RatioLiquidMethane",
	"RatioLiquidOxygen",
	"RatioPollutedWater",
}

// cycle picks one of values by segment, which is how a world that has to drive
// a program across a boundary and back is written.
func cycle(segment int, values ...float64) float64 { return values[segment%len(values)] }

// boolValue is what a device property holding a flag reads as.
func boolValue(set bool) float64 {
	if set {
		return 1
	}
	return 0
}

// setSlot seeds one slot property of one device. See [seeding.slot].
func setSlot(t *testing.T, d *device, name string, slot int, value float64) {
	t.Helper()
	seedDevice(t, d, func(s *seeding) { s.slot(t, name, slot, value) })
}

// setReagent seeds one reagent view of one device. See [seeding.reagent].
func setReagent(t *testing.T, d *device, mode, reagent string, value float64) {
	t.Helper()
	seedDevice(t, d, func(s *seeding) { s.reagent(t, mode, reagent, value) })
}

// fixtureWorld is the world one fixture is driven through, applied before
// each turn of its control loop. Every pin answers zero otherwise, so a
// fixture absent from the map is driven against the all-zero world.
var fixtureWorld = map[string]devtrace.Stimulus{
	"antenna_aim.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		if segment == 0 {
			setPrefab(t, pinOn(t, h, 4), "StructureSolarPanel")
			setPrefab(t, pinOn(t, h, 5), "StructureSolarPanel")
		}
		// A whole revolution in azimuth and a sun that rises and sets, so the
		// elevation crosses the cosine floor the air mass is guarded with and
		// the aim published to the panels moves every turn.
		setLogic(t, pinOn(t, h, 0), "SolarAngle", float64((segment*13)%360))
		setLogic(t, pinOn(t, h, 0), "Vertical", float64((segment*17)%180)-90)
	},
	"alloy_table.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		// Three alloys the recipe table holds and one it does not. The three
		// smelt from a different number of ingots each, so the figure derived
		// from the row moves as well as the row itself, and the fourth is what
		// opens the arm that finds no row.
		alloys := []int{
			ic10.HashName("ItemCopperIngot"),
			ic10.HashName("ItemSolderIngot"),
			ic10.HashName("ItemInconelIngot"),
			ic10.HashName("ItemWaterBottle"),
		}
		setLogic(t, pinOn(t, h, 0), "Setting", float64(alloys[segment%len(alloys)]))
	},
	"digit_row.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		// A reading that takes every digit count the row can show, and steps
		// past both ends of what it accepts. How deep the layout recurses is the
		// digit count, so a sweep confined to one magnitude would drive one
		// depth and the walk back down would never run more than once.
		magnitudes := []float64{0, 7, 94, 813, 6250, 99999, -12, 250000}
		setLogic(t, pinOn(t, h, 0), "Setting", magnitudes[segment%len(magnitudes)]+float64(segment%7))
	},
	"frames.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		// A sweep that crosses the alarm bound and does not repeat within the
		// 64 sample window, so the median the selection returns moves every turn
		// rather than settling on the one value an undriven gauge reads.
		setLogic(t, pinOn(t, h, 0), "Pressure", float64((segment*37)%400))
	},
	"filtration.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		unit := pinOn(t, h, 0)
		// An input the machine has no atmosphere on reads NaN, and the inlet
		// falling to nothing is the other way in to the starved arm. Both are
		// entered on their own period so neither hides the other.
		if segment%13 == 0 {
			setLogic(t, unit, "RatioPollutantInput", math.NaN())
		} else {
			setLogic(t, unit, "RatioPollutantInput", float64((segment*7)%20)/100)
		}
		setLogic(t, unit, "PressureInput", boolValue(segment%17 != 0)*float64(50+segment%40))
		// The oxygen fraction straddles the target in both directions, which is
		// what makes the integrator's correction change sign rather than
		// winding to one end of its clamp and staying there.
		setLogic(t, unit, "RatioOxygenOutput", 0.90+float64((segment*3)%17)/100)
		// Pollutant leaving on the waste side crosses the floor the stalled
		// test is written against.
		setLogic(t, unit, "RatioPollutantOutput2", float64((segment*11)%100)/100)
		setLogic(t, unit, "TemperatureOutput", 320+float64((segment*5)%40))
	},
	"alarm_panel.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		vent := pinOn(t, h, 0)
		setLogic(t, vent, "Pressure", cycle(segment, 5, 20, 20, 5))
		setLogic(t, vent, "On", cycle(segment, 1, 1, 0, 1))
		setLogic(t, vent, "Ratio", cycle(segment, 0.5, 0.9, 0.5, 0.5))
		setLogic(t, vent, "Error", cycle(segment, 0, 0, 0, 1))
		// The acknowledgement is what clears the filter bit and silences the
		// horn; without it the latch saturates on the third segment and every
		// later one publishes the same mask.
		setLogic(t, pinOn(t, h, 1), "Activate", boolValue(segment%37 == 0))
	},
	"bits.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		if segment == 0 {
			setPrefab(t, pinOn(t, h, 2), "StructureWallLight")
			setHashes(t, pinOn(t, h, 3), ic10.HashName("StructureConsoleLED5"), ic10.HashName("Airlock"))
		}
		// A packed setting whose low byte is a digit some segments and not
		// others, and whose top byte is the delimiter every third one, so both
		// the digit branch and the delimiter test change answer during the run.
		low := '0' + segment%12
		top := int(' ')
		if segment%3 == 0 {
			top = ','
		}
		packed := low | (segment%7)<<8 | (segment%5)<<16 | top<<24
		setLogic(t, pinOn(t, h, 0), "Setting", float64(packed))
	},
	"cooler_bank.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		if segment == 0 {
			// The wired sensor is on the network too, so the batch average and
			// the pin reading are taken over overlapping worlds rather than
			// unrelated ones.
			setPrefab(t, pinOn(t, h, 0), "StructureGasSensor")
			setPrefab(t, pinOn(t, h, 2), "StructureGasSensor")
			setPrefab(t, pinOn(t, h, 3), "StructureWallCooler")
			setPrefab(t, pinOn(t, h, 4), "StructureWallCooler")
		}
		setLogic(t, pinOn(t, h, 0), "Temperature", cycle(segment, 290, 310, 305, 280))
		setLogic(t, pinOn(t, h, 2), "Temperature", cycle(segment, 320, 295, 290, 315))
	},
	"hab_monitor.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		// Each section crosses its own habitability bound on a different
		// period, so the count published to the console takes every value it
		// can rather than settling on one.
		for pin := range 5 {
			section := pinOn(t, h, pin)
			setLogic(t, section, "Temperature", 280+float64((segment+pin*3)%20))
			setLogic(t, section, "Pressure", 80+float64((segment*7+pin*11)%40))
			setLogic(t, section, "RatioOxygen", 0.15+float64((segment+pin)%10)/100)
			setLogic(t, section, "RatioCarbonDioxide", float64((segment*3+pin)%12)/100)
		}
	},
	"lights_control.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		if segment == 0 {
			setPrefab(t, pinOn(t, h, 0), "StructureDiodeSlide")
			setPrefab(t, pinOn(t, h, 1), "StructureDiodeSlide")
			setPrefab(t, pinOn(t, h, 2), "StructureLightRound")
			setPrefab(t, pinOn(t, h, 3), "StructureLightLongWide")
			setPrefab(t, pinOn(t, h, 4), "StructureLightLongAngled")
		}
		// The daylight reading is the sum over the diodes, and the program
		// writes the diodes itself, so a world that left them alone would see
		// the sum it last wrote and never move. Driving them is what makes the
		// falling and rising branches both reachable.
		setLogic(t, pinOn(t, h, 0), "On", float64(segment%3))
		setLogic(t, pinOn(t, h, 1), "On", float64((segment*2)%5))
	},
	"liquid_train.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		if segment == 0 {
			// Two analysers on the feed, so a maximum over that tap is not the
			// tap's own average, and one apiece on the stages below it.
			for pin, tap := range map[int]string{2: "Feed", 3: "Feed", 4: "Middle", 5: "Tail"} {
				setHashes(t, pinOn(t, h, pin), ic10.HashName("StructureLiquidPipeAnalyzer"), ic10.HashName(tap))
			}
		}
		// Every eleventh turn the tail passes more than the stage above it,
		// opening the falling-test arm; the second feed analyser runs dirtier
		// than the first, so the spike's maximum differs from the average.
		upset := 1.0
		if segment%11 == 0 {
			upset = 30.0
		}
		for i, liquid := range liquidTrainContaminants {
			fraction := 0.004 + float64((segment*7+i*13)%20)/1000
			setLogic(t, pinOn(t, h, 2), liquid, fraction)
			setLogic(t, pinOn(t, h, 3), liquid, fraction*1.5)
			setLogic(t, pinOn(t, h, 4), liquid, fraction/3)
			setLogic(t, pinOn(t, h, 5), liquid, fraction*upset/9)
		}
	},
	"meter_scale.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		// A fractional reading, and a zero on some segments, since the lamp is
		// driven from a double reaching bool and zero is the only reading that
		// clears it.
		setLogic(t, pinOn(t, h, 0), "Pressure", float64((segment*7)%23)+float64(segment%4)/4)
	},
	"mode_select.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		// Every dial position the switch names, and one it does not, which is
		// the arm that continues the loop from inside the switch.
		setLogic(t, pinOn(t, h, 0), "Setting", float64(segment%6))
	},
	"odometer.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		// A reading of 2^53 - 1 is the largest magnitude the machine's
		// bitwise operand conversion is the identity on; its low word is
		// full, which is also what opens the rollover arm, so it and the
		// one-below case are entered separately rather than together.
		switch segment % 9 {
		case 0:
			setLogic(t, pinOn(t, h, 2), "Setting", 1<<53-1)
		case 4:
			setLogic(t, pinOn(t, h, 2), "Setting", 1<<53-2)
		default:
			setLogic(t, pinOn(t, h, 2), "Setting", float64((segment*2654435761)%(1<<32)))
		}
	},
	"pointer_walk.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		// Samples that leave the band around the middle one on some turns, so
		// the two pointers walk inward rather than stopping where they started.
		setLogic(t, pinOn(t, h, 0), "Pressure", float64((segment*29)%97))
	},
	"power_bus.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		if segment == 0 {
			// Six pins is the whole housing and this program wires four of
			// them, so the network the batches select over overlaps the pins it
			// writes. Nothing collides: the analyser properties are read and
			// the pin properties are written.
			analyser := ic10.HashName("StructureCableAnalysizer")
			setHashes(t, pinOn(t, h, 4), analyser, ic10.HashName("Power Input"))
			setHashes(t, pinOn(t, h, 3), analyser, ic10.HashName("Power Output"))
		}
		// A bank matching nothing sums to zero on both readings, so the ratio
		// is zero over zero and the program is required to hold its last good
		// value rather than publish the NaN. Nothing else asks it to hold.
		battery := pinOn(t, h, 5)
		if segment%11 == 0 {
			setPrefab(t, battery, "StructureWallLight")
		} else {
			setPrefab(t, battery, "StructureBattery")
		}
		setLogic(t, battery, "Charge", float64((segment*37)%1000))
		setLogic(t, battery, "Maximum", 1000)
		// Supply crossing draw in both directions is what moves the breaker
		// independently of the two charge bounds.
		setLogic(t, pinOn(t, h, 4), "PowerPotential", float64((segment*13)%500))
		setLogic(t, pinOn(t, h, 3), "PowerActual", float64((segment*29)%500))
	},
	"printer_reagents.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		printer := pinOn(t, h, 0)
		ores := []string{"Copper", "Iron", "Gold", "Silicon"}
		for ore, name := range ores {
			// A reagent the printer has never held reads NaN, which the
			// shortfall guards on. One ore is absent per segment and which one
			// moves, so the guard is taken for a different ore each turn rather
			// than for none or always the same.
			if segment%len(ores) == ore {
				setReagent(t, printer, "Contents", name, math.NaN())
			} else {
				setReagent(t, printer, "Contents", name, float64((segment*7+ore*13)%20))
			}
			setReagent(t, printer, "Required", name, float64((segment*3+ore*5)%25))
		}
		// The recipe view is read for the first ore alone. TotalContents is not
		// seeded: the chip sums the mixture Contents seeded, so a value put
		// there directly would be a total its own parts do not add up to.
		setReagent(t, printer, "Recipe", "Copper", float64(segment%5))
		setLogic(t, printer, "RecipeHash", float64((segment*17)%50))
	},
	"quantize.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		// Negative on some segments and carrying half a unit on others, where
		// the four roundings part company; the period is longer than the
		// run, so no sample repeats.
		setLogic(t, pinOn(t, h, 0), "Pressure", (float64((segment*37)%211)-105)/8)
	},
	"rolling_average.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		// A sweep that crosses the alarm bound in both directions, and never
		// repeats within the eight sample window, so the mean moves every turn.
		setLogic(t, pinOn(t, h, 0), "Pressure", float64((segment*37)%400))
	},
	"satellitetracking.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		if segment == 0 {
			// The master switch is the outer loop's condition. A dish already
			// holding a signal keeps the elevation search out of the first
			// pass, and that search ends by throwing the switch.
			setLogic(t, pinOn(t, h, 2), "On", 1)
			setLogic(t, pinOn(t, h, 0), "SignalStrength", 25)
			return
		}
		// The signal falling below the inner loop's bound is what opens the
		// elevation search, and the elevation moving under it is what picks
		// which way the search steps.
		setLogic(t, pinOn(t, h, 0), "SignalStrength", cycle(segment, 25, 30, 15, 22))
		setLogic(t, pinOn(t, h, 0), "Vertical", float64((segment*11)%40))
	},
	"smelter.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		if segment == 0 {
			for _, pin := range []int{0, 2} {
				setHashes(t, pinOn(t, h, pin), ic10.HashName("StructureFurnace"), ic10.HashName("north"))
			}
		}
		setSlot(t, pinOn(t, h, 0), "SlotType_Quantity", 0, float64(segment%9))
		setSlot(t, pinOn(t, h, 2), "SlotType_Quantity", 0, float64((segment*5)%11))
		// The output slot's occupancy is the whole of what the device write is
		// conditioned on.
		setSlot(t, pinOn(t, h, 0), "Occupied", 1, boolValue(segment%2 == 0))
		setLogic(t, pinOn(t, h, 0), "Temperature", 400+float64(segment%50))
	},
	"solar_tracker.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		setLogic(t, pinOn(t, h, 0), "SolarAngle", float64((segment*13)%360))
		// Past the maximum elevation on some segments, so the clamp the aim
		// runs through is asked for something to clamp.
		setLogic(t, pinOn(t, h, 0), "Vertical", float64((segment*7)%120))
	},
	"suit_racks.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		if segment == 0 {
			// Three racks, so the census counts more than one and the worst
			// helmet is a maximum over a group rather than one rack's reading.
			for _, pin := range []int{2, 3, 4} {
				setPrefab(t, pinOn(t, h, pin), "StructureSuitStorage")
			}
		}
		// The hazard line trips on its own period, which is what moves the lock
		// and the lamp the bay is written with, and the helmets wear at
		// different rates so the worst of them crosses the service bound
		// without every rack crossing it at once.
		setLogic(t, pinOn(t, h, 0), "Activate", boolValue(segment%5 == 0))
		for i, pin := range []int{2, 3, 4} {
			setSlot(t, pinOn(t, h, pin), "Damage", 0, float64((segment*7+i*11)%40)/100)
		}
	},
	"thermostat.c": func(t *testing.T, h *chip.FixtureHarness, segment int) {
		t.Helper()
		// Below the band, inside it, above it, and inside it again: the two
		// inside steps are where the hysteresis has something to hold, and they
		// are the only place a program recomputing from a default differs from
		// one that keeps its last state.
		setLogic(t, pinOn(t, h, 0), "Temperature", cycle(segment, 250, 293, 350, 293))
	},
}

// TestOptimizedAgreesWithUnoptimized is the equivalence comparison: one
// program built twice, run in one world, and compared on what a device saw.
// It cannot say that either form is right — two builds can agree and both
// be wrong — only that optimizing did not change the answer.
func TestOptimizedAgreesWithUnoptimized(t *testing.T) {
	names := corpusFixtures(t)
	// All three are keyed by fixture name and hand-written, so a stale name
	// in any of them is bookkeeping nothing else reports: the baseline's own
	// coverage check walks the recorded measurements rather than these.
	requireNamedInCorpus(t, "fixtureWorld", fixtureWorld, names)
	requireNamedInCorpus(t, "unsupportedUnoptimized", unsupportedUnoptimized, names)
	requireNamedInCorpus(t, "overEditorLimit", overEditorLimit, names)
	requireReasonsCited(t, "refusalMarker", refusalMarker, unsupportedUnoptimized)

	recorded := equivalenceBaseline.load(t)
	equivalenceBaseline.covers(t, names, recorded)

	measured := make(map[string]int, len(names))
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			compareFixture(t, name, recorded[name], measured)
		})
	}

	if *updateBaseline {
		equivalenceBaseline.record(t, names, measured)
	}
}

// compareFixture builds one fixture both ways, runs each through the
// fixture's world, and compares what a device saw. It records what it
// measured into measured, which a regeneration writes out; a fixture it
// could not measure is left out rather than filled with an invented number.
func compareFixture(t *testing.T, name string, want int, measured map[string]int) {
	t.Helper()
	build := buildUnoptimized(t, name)
	reason, unsupported := unsupportedUnoptimized[name]

	if build.rejected != unsupported {
		if unsupported {
			t.Errorf("%s is recorded as unsupported because %s, and %s now compiles it; take it out of unsupportedUnoptimized",
				name, reason, unoptimizedFlag)
		} else {
			t.Fatalf("%s refuses %s, and nothing records it as unsupported; a program that newly stops compiling is a finding rather than a fixture to drop quietly:\n%s",
				unoptimizedFlag, name, build.report)
		}
	}
	if build.rejected {
		requireRefusalReason(t, name, reason, build.report)
		t.Skipf("unsupported under %s: %s", unoptimizedFlag, reason)
	}

	switch {
	case build.overEditorLimit && !overEditorLimit[name]:
		t.Errorf("%s is over a limit unoptimized and overEditorLimit does not name it; it is still compared, because the interpreter imposes no line cap, but which fixtures need that allowance is written down rather than found in a diagnostic:\n%s",
			name, build.report)
	case !build.overEditorLimit && overEditorLimit[name]:
		t.Errorf("%s is named in overEditorLimit and its unoptimized build now fits inside every limit", name)
	case build.overEditorLimit:
		t.Logf("%s is over a limit unoptimized, which the in-game editor enforces and the interpreter does not:\n%s",
			name, build.report)
	}

	optimized := traceChip(t, compileFixture(t, name), "optimized", fixtureWorld[name])
	unoptimized := traceChip(t, build.assembly, "unoptimized", fixtureWorld[name])
	// Logged before the comparison rather than after it, so a fixture that
	// diverges still says what was run against what.
	t.Logf("%s: %d writes over %d segments, %s",
		name, len(optimized.Events), optimized.Segments, optimized.Stop)

	if *updateBaseline {
		// Held to the same evidence as a comparison, against measurableWrites
		// rather than a floor derived from the number about to be written, which
		// every run clears by construction. Both are asked before either is read,
		// so a regeneration reports every fixture it will not record.
		optimizedEvident := requireEvidence(t, optimized, measurableWrites)
		unoptimizedEvident := requireEvidence(t, unoptimized, measurableWrites)
		if optimizedEvident && unoptimizedEvident {
			measured[name] = len(optimized.Events)
		}
	} else {
		floor := writeFloor(want)
		requireEvidence(t, optimized, floor)
		requireEvidence(t, unoptimized, floor)
	}

	if err := devtrace.Diff(optimized, unoptimized); err != nil {
		t.Errorf("%s: optimizing changed what the program does: %v", name, err)
	}
}

// requireEvidence is the floor a comparison has to clear to have compared
// anything: an empty trace against an empty trace is not evidence, and
// neither is a run that stopped before the program's loop could turn. A
// regeneration reads the result to decide the run is worth recording.
func requireEvidence(t *testing.T, trace devtrace.Trace, floor int) bool {
	t.Helper()
	evident := true
	if len(trace.Events) < floor {
		t.Errorf("%s wrote to a device %d times, and its world is supposed to drive it to at least %d; a comparison over fewer has stopped establishing what it claims",
			trace.Name, len(trace.Events), floor)
		evident = false
	}
	if trace.Stop.Reason == devtrace.StopBudget {
		t.Errorf("%s reached no yield within one segment, so the two builds are no longer at the same place in the source and nothing they wrote is comparable",
			trace.Name)
		evident = false
	}
	return evident
}

// unoptimizedBuild is what --no-optimize made of one fixture.
type unoptimizedBuild struct {
	assembly string
	// rejected is a refusal: a non-zero status with no assembly beside it.
	rejected bool
	// overEditorLimit is a non-zero status naming a limit the program exceeded,
	// which the emitted text survives.
	overEditorLimit bool
	// report is the compiler's own diagnostic, which carries the reason.
	report string
}

// buildUnoptimized compiles one fixture with the optimizer off, and returns
// assembly the chip's own rules accept. A limit the in-game editor holds is
// not a failure here, since the interpreter imposes no line cap. A refusal
// is reported, not decided, here.
func buildUnoptimized(t *testing.T, name string) unoptimizedBuild {
	t.Helper()
	output, err := compileDirect(t, filepath.Join(fixtures, name), options{skipOptimizer: true})
	if err != nil {
		return unoptimizedBuild{rejected: true, report: err.Error()}
	}
	return unoptimizedBuild{
		assembly:        output.Text,
		overEditorLimit: len(output.Report.Violations) > 0,
		report:          output.Report.String(),
	}
}

// requireRefusalReason holds an exclusion to the reason it states: the
// program kept out has to still be refused for the reason recorded beside
// it, not for some later one nobody looked at. The reason is looked up, not
// assumed, so each reason is checked against its own marker.
func requireRefusalReason(t *testing.T, name, reason, report string) {
	t.Helper()
	marker, stated := refusalMarker[reason]
	if !stated {
		t.Fatalf("%s is excluded because %s, and nothing says what to look for in the diagnostic to know that still holds",
			name, reason)
	}
	if !strings.Contains(report, marker) {
		t.Errorf("%s is excluded because %s, and %s refuses it for something else:\n%s",
			name, reason, unoptimizedFlag, report)
	}
}
