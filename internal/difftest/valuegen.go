package difftest

import "strconv"

// Mnemonic groups the value generator draws from. Each group shares an operand
// shape, so one emitter covers the whole group.
var (
	unaryOps       = []string{"move", "sqrt", "trunc", "ceil", "floor", "abs", "snan", "snanz"}
	binaryOps      = []string{"add", "sub", "mul"}
	compareOps     = []string{"slt", "sgt", "sle", "sge", "seq", "sne"}
	compareZeroOps = []string{"sltz", "sgtz", "slez", "sgez", "seqz", "snez"}
	longBinaryOps  = []string{"and", "or", "xor", "nor"}
	shiftOps       = []string{"srl", "sra", "sll"}
	branch1Ops     = []string{"bltz", "bgtz", "blez", "bgez", "beqz", "bnez", "bnan",
		"bltzal", "bgtzal", "blezal", "bgezal", "beqzal", "bnezal"}
	branch2Ops = []string{"beq", "bne", "blt", "bgt", "ble", "bge",
		"beqal", "bneal", "bltal", "bgtal", "bleal", "bgeal"}
	jumpOps         = []string{"j", "jal"}
	deviceSetOps    = []string{"sdse", "sdns"}
	deviceBranchOps = []string{"bdse", "bdns", "bdseal", "bdnsal"}
)

// valueBlocks are the shapes a fault-free program is assembled from, with the
// weight each is chosen with. Arithmetic dominates because it is where a
// bit-exact comparison has the most to say.
var valueBlocks = []struct {
	weight int
	emit   func(g *generator)
}{
	{weight: 10, emit: (*generator).emitUnary},
	{weight: 10, emit: (*generator).emitBinary},
	{weight: 6, emit: (*generator).emitDivisor},
	{weight: 10, emit: (*generator).emitCompare},
	{weight: 10, emit: (*generator).emitLong},
	{weight: 8, emit: (*generator).emitShift},
	{weight: 4, emit: (*generator).emitSelect},
	{weight: 7, emit: (*generator).emitBranch},
	{weight: 4, emit: (*generator).emitJump},
	{weight: 5, emit: (*generator).emitDeviceProbe},
	{weight: 7, emit: (*generator).emitStack},
	{weight: 7, emit: (*generator).emitMemory},
	{weight: 4, emit: (*generator).emitLoop},
	{weight: 2, emit: (*generator).emitYield},
}

// ValueProgram returns a terminating program that is meant to run to the end
// without faulting, for comparison on final machine state.
//
// It terminates by construction: every branch goes forward, and the one
// backward branch shape is a counter loop with a literal trip count whose
// counter register nothing else writes.
//
// The same seed always produces the same program and the same starting state.
func ValueProgram(seed uint64) Program {
	g := newGenerator(seed)
	g.emitDeclarations()

	total := 0
	for _, block := range valueBlocks {
		total += block.weight
	}
	for range 6 + g.rng.IntN(20) {
		roll := g.rng.IntN(total)
		for _, block := range valueBlocks {
			roll -= block.weight
			if roll < 0 {
				block.emit(g)
				break
			}
		}
		// Defining a reserved label soon after the branch that reserved it
		// keeps a forward branch from skipping most of the program.
		if len(g.pending) > 0 && g.rng.IntN(3) != 0 {
			g.defineLabel()
		}
	}
	g.flushLabels()
	return g.program(seed, KindValue, "")
}

// emitDeclarations writes the compile time define and run time alias forms.
// Aliases come first because an alias only takes effect when its line runs, so
// a reference above it resolves to nothing.
func (g *generator) emitDeclarations() {
	for range g.rng.IntN(3) {
		name := "a" + strconv.Itoa(len(g.aliases))
		g.emit("alias", name, "r"+strconv.Itoa(g.rng.IntN(generalRegisters)))
		g.aliases = append(g.aliases, name)
	}
	for range g.rng.IntN(3) {
		name := "v" + strconv.Itoa(len(g.defines))
		g.emit("define", name, pick(g.rng, literalPool))
		g.defines = append(g.defines, name)
	}
}

// Every emitter chooses its operands before its destination. Taking the
// destination first would mark it as a safe bitwise operand while it still
// holds whatever the previous instruction left there, and the operand choice
// could then pick it.
func (g *generator) emitUnary() {
	source := g.value()
	g.emit(pick(g.rng, unaryOps), g.destination(false), source)
}

func (g *generator) emitBinary() {
	a, b := g.value(), g.value()
	g.emit(pick(g.rng, binaryOps), g.destination(false), a, b)
}

// emitDivisor keeps the divisor a literal. A register divisor would be fine for
// the chip, which returns an infinity or a NaN rather than faulting, but it
// would make the result unpredictable to the generator, and an infinity reaching
// a later bitwise operand is a fault neither generator is trying to produce.
//
// mod additionally takes a finite dividend. Go's math.Mod returns a NaN of its
// own for a NaN or infinite dividend where the game propagates the operand's,
// and the comparison is on bit patterns.
func (g *generator) emitDivisor() {
	if g.rng.IntN(2) == 0 {
		dividend, divisor := g.boundedValue(), g.nonZeroLiteral()
		g.emit("mod", g.destination(false), dividend, divisor)
		return
	}
	dividend, divisor := g.value(), g.nonZeroLiteral()
	g.emit("div", g.destination(false), dividend, divisor)
}

func (g *generator) emitCompare() {
	if g.rng.IntN(2) == 0 {
		source := g.value()
		g.emit(pick(g.rng, compareZeroOps), g.destination(true), source)
		return
	}
	a, b := g.value(), g.value()
	g.emit(pick(g.rng, compareOps), g.destination(true), a, b)
}

func (g *generator) emitLong() {
	if g.rng.IntN(5) == 0 {
		source := g.boundedValue()
		g.emit("not", g.destination(true), source)
		return
	}
	a, b := g.boundedValue(), g.boundedValue()
	g.emit(pick(g.rng, longBinaryOps), g.destination(true), a, b)
}

func (g *generator) emitShift() {
	value, distance := g.boundedValue(), pick(g.rng, shiftDistancePool)
	g.emit(pick(g.rng, shiftOps), g.destination(true), value, distance)
}

func (g *generator) emitSelect() {
	condition, ifTrue, ifFalse := g.value(), g.value(), g.value()
	g.emit("select", g.destination(false), condition, ifTrue, ifFalse)
}

func (g *generator) emitBranch() {
	target := g.newLabel()
	if g.rng.IntN(2) == 0 {
		g.emit(pick(g.rng, branch1Ops), g.value(), target)
		return
	}
	g.emit(pick(g.rng, branch2Ops), g.value(), g.value(), target)
}

func (g *generator) emitJump() {
	g.emit(pick(g.rng, jumpOps), g.newLabel())
}

// emitDeviceProbe uses the instructions that only ask whether a pin has a
// device on it. Every pin is empty on both sides, so the answer is the same
// without either side needing a device model.
func (g *generator) emitDeviceProbe() {
	pin := "d" + strconv.Itoa(g.rng.IntN(6))
	if g.rng.IntN(2) == 0 {
		g.emit(pick(g.rng, deviceSetOps), g.destination(true), pin)
		return
	}
	g.emit(pick(g.rng, deviceBranchOps), pin, g.newLabel())
}

// emitStack sets the stack pointer immediately before using it, so that a
// branch skipping an earlier push cannot leave a later pop out of range.
func (g *generator) emitStack() {
	switch g.rng.IntN(3) {
	case 0:
		g.emit("move", "sp", strconv.Itoa(g.rng.IntN(500)))
		g.emit("push", g.value())
	case 1:
		g.emit("move", "sp", strconv.Itoa(1+g.rng.IntN(500)))
		g.emit("pop", g.destination(false))
	default:
		g.emit("move", "sp", strconv.Itoa(1+g.rng.IntN(500)))
		g.emit("peek", g.destination(false))
	}
}

func (g *generator) emitMemory() {
	switch g.rng.IntN(8) {
	case 0:
		g.emit("clr", "db")
	case 1, 2:
		address, value := g.address(), g.value()
		g.emit("put", "db", address, value)
	case 3, 4, 5:
		address, value := g.address(), g.value()
		g.emit("poke", address, value)
	default:
		g.emit("get", g.destination(false), "db", g.address())
	}
}

// emitLoop is the only backward branch shape. The trip count is a literal in a
// register no other emitter writes, so the loop always terminates.
func (g *generator) emitLoop() {
	g.emit("move", "r"+strconv.Itoa(loopRegister), strconv.Itoa(1+g.rng.IntN(4)))
	name := "t" + strconv.Itoa(g.labelSeq)
	g.labelSeq++
	g.raw(name + ":")
	g.clearBounded()
	for range 1 + g.rng.IntN(2) {
		g.emitBinary()
	}
	g.emit("sub", "r"+strconv.Itoa(loopRegister), "r"+strconv.Itoa(loopRegister), "1")
	g.emit("bgtz", "r"+strconv.Itoa(loopRegister), name)
}

func (g *generator) emitYield() { g.emit("yield") }
