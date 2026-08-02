package difftest

import (
	"strconv"

	"github.com/greg2010/ic11c/internal/vm"
)

// logicTypePool is the device properties the unset pin recipes name. Both are
// long standing members of the game's LogicTypes, so neither depends on which
// game build a harness extracted its tables from.
var logicTypePool = []string{"Setting", "On"}

// safeBlocks are the value generator shapes a fault program pads with. None of
// them branches, so nothing can skip the faulting line, and none of them can
// fault, so the recipe is the only fault in the program.
var safeBlocks = []func(g *generator){
	(*generator).emitUnary,
	(*generator).emitBinary,
	(*generator).emitCompare,
	(*generator).emitLong,
	(*generator).emitShift,
	(*generator).emitSelect,
}

// faultRecipe is one deliberate fault.
//
// want is the exception the recipe exists to raise. It is not what the
// comparison checks — that is the harness's business — but a recipe that stops
// raising it has stopped testing anything, which the generator tests assert.
type faultRecipe struct {
	name string
	want vm.ExceptionType
	emit func(g *generator)
}

// faultRecipes are the fault constructs both implementations agree about, so
// that a mismatch is a finding rather than a known difference in vocabulary.
// divergentFaults records the ones deliberately left out.
var faultRecipes = []faultRecipe{
	{
		name: "pop-at-zero",
		want: vm.ExcStackUnderFlow,
		emit: func(g *generator) {
			g.emit("move", "sp", "0")
			g.emit("pop", g.destination(false))
		},
	},
	{
		name: "pop-below-zero",
		want: vm.ExcStackUnderFlow,
		emit: func(g *generator) {
			g.emit("move", "sp", strconv.Itoa(-1-g.rng.IntN(100)))
			g.emit("pop", g.destination(false))
		},
	},
	{
		name: "peek-at-zero",
		want: vm.ExcStackUnderFlow,
		emit: func(g *generator) {
			g.emit("move", "sp", "0")
			g.emit("peek", g.destination(false))
		},
	},
	{
		name: "push-below-zero",
		want: vm.ExcStackUnderFlow,
		emit: func(g *generator) {
			g.emit("move", "sp", strconv.Itoa(-1-g.rng.IntN(100)))
			g.emit("push", g.value())
		},
	},
	{
		name: "push-past-the-array",
		want: vm.ExcStackOverFlow,
		emit: func(g *generator) {
			g.emit("move", "sp", strconv.Itoa(512+g.rng.IntN(100)))
			g.emit("push", g.value())
		},
	},
	{
		name: "pop-past-the-array",
		want: vm.ExcStackOverFlow,
		emit: func(g *generator) {
			g.emit("move", "sp", strconv.Itoa(513+g.rng.IntN(100)))
			g.emit("pop", g.destination(false))
		},
	},
	{
		name: "peek-past-the-array",
		want: vm.ExcStackOverFlow,
		emit: func(g *generator) {
			g.emit("move", "sp", strconv.Itoa(514+g.rng.IntN(100)))
			g.emit("peek", g.destination(false))
		},
	},
	{
		name: "put-below-zero",
		want: vm.ExcStackUnderFlow,
		emit: func(g *generator) {
			g.emit("put", "db", strconv.Itoa(-1-g.rng.IntN(100)), g.value())
		},
	},
	{
		name: "put-past-the-array",
		want: vm.ExcStackOverFlow,
		emit: func(g *generator) {
			g.emit("put", "db", strconv.Itoa(512+g.rng.IntN(100)), g.value())
		},
	},
	{
		name: "load-from-an-unset-pin",
		want: vm.ExcDeviceNotFound,
		emit: func(g *generator) {
			g.emit("l", g.destination(false), "d"+strconv.Itoa(g.rng.IntN(6)), pick(g.rng, logicTypePool))
		},
	},
	{
		name: "store-to-an-unset-pin",
		want: vm.ExcDeviceNotFound,
		emit: func(g *generator) {
			g.emit("s", "d"+strconv.Itoa(g.rng.IntN(6)), pick(g.rng, logicTypePool), g.value())
		},
	},
	{
		name: "indirect-destination-past-the-file",
		want: vm.ExcOutOfRegisterBounds,
		emit: func(g *generator) {
			g.emit("move", "r"+strconv.Itoa(scratchRegister), strconv.Itoa(18+g.rng.IntN(200)))
			g.emit("move", "rr"+strconv.Itoa(scratchRegister), g.value())
		},
	},
	{
		name: "indirect-source-past-the-file",
		want: vm.ExcOutOfRegisterBounds,
		emit: func(g *generator) {
			g.emit("move", "r"+strconv.Itoa(scratchRegister), strconv.Itoa(18+g.rng.IntN(200)))
			g.emit("move", g.destination(false), "rr"+strconv.Itoa(scratchRegister))
		},
	},
	{
		name: "infinite-bitwise-operand",
		want: vm.ExcShiftOverflow,
		emit: func(g *generator) {
			g.emit("div", "r"+strconv.Itoa(scratchRegister), "1", "0")
			g.emit("and", g.destination(true), "r"+strconv.Itoa(scratchRegister), "1")
		},
	},
}

// faultRecipeNames names every fault construct the generator can produce.
func faultRecipeNames() []string {
	names := make([]string, len(faultRecipes))
	for i, recipe := range faultRecipes {
		names[i] = recipe.name
	}
	return names
}

// FaultProgram returns a program built to fault, for comparison on error type
// and faulting line.
//
// The faulting construct is preceded and followed by lines that cannot fault
// and cannot branch, so the fault always happens, always on the same line, and
// the lines after it never run. The same seed always produces the same program.
func FaultProgram(seed uint64) Program {
	g := newGenerator(seed)
	recipe := faultRecipes[g.rng.IntN(len(faultRecipes))]
	buildFaultProgram(g, recipe)
	return g.program(seed, KindFault, recipe.name)
}

// buildFaultProgram pads the recipe with lines that cannot fault, and returns
// the half-open line range the recipe itself occupies. The fault must land
// inside that range.
func buildFaultProgram(g *generator, recipe faultRecipe) (start, end int) {
	for range g.rng.IntN(6) {
		pick(g.rng, safeBlocks)(g)
	}
	start = len(g.lines)
	recipe.emit(g)
	end = len(g.lines)
	for range 1 + g.rng.IntN(4) {
		pick(g.rng, safeBlocks)(g)
	}
	return start, end
}
