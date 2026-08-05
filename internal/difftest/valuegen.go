package difftest

import (
	"slices"
	"strconv"

	"github.com/greg2010/ic11c/internal/ic10"
)

// Mnemonic groups the value generator draws from. Each group shares an operand
// shape, so one emitter covers the whole group.
var (
	unaryOps       = []string{"move", "sqrt", "trunc", "ceil", "floor", "abs", "snan", "snanz"}
	binaryOps      = []string{"add", "sub", "mul"}
	divisorOps     = []string{"div", "mod"}
	compareOps     = []string{"slt", "sgt", "sle", "sge", "seq", "sne"}
	compareZeroOps = []string{"sltz", "sgtz", "slez", "sgez", "seqz", "snez"}
	longBinaryOps  = []string{"and", "or", "xor", "nor"}
	// sla is byte-identical to sll despite its name, so the compiler backend
	// must never select it; the chip still runs it, which a sweep checks.
	shiftOps   = []string{"srl", "sra", "sll", "sla"}
	branch1Ops = []string{"bltz", "bgtz", "blez", "bgez", "beqz", "bnez", "bnan",
		"bltzal", "bgtzal", "blezal", "bgezal", "beqzal", "bnezal"}
	branch2Ops = []string{"beq", "bne", "blt", "bgt", "ble", "bge",
		"beqal", "bneal", "bltal", "bgtal", "bleal", "bgeal"}
	jumpOps         = []string{"j", "jal"}
	deviceSetOps    = []string{"sdse", "sdns"}
	deviceBranchOps = []string{"bdse", "bdns", "bdseal", "bdnsal"}
)

// callForm is one link form emitCall can open a call with.
type callForm struct {
	mnemonic string
	operands []string
}

// callForms are the link forms emitCall opens a call with; emitCall appends
// the target to each one's operands.
//
// Every form here is certainly taken: jal is unconditional, the conditions
// are between literals that decide them, and bdnsal branches because no pin
// carries a device. A form that fell through would leave the return reading
// ra with nothing to read.
var callForms = []callForm{
	{mnemonic: "jal"},
	{mnemonic: "beqzal", operands: []string{"0"}},
	{mnemonic: "bnezal", operands: []string{"1"}},
	{mnemonic: "bgtzal", operands: []string{"1"}},
	{mnemonic: "bltzal", operands: []string{"-1"}},
	{mnemonic: "bgezal", operands: []string{"0"}},
	{mnemonic: "blezal", operands: []string{"0"}},
	{mnemonic: "beqal", operands: []string{"1", "1"}},
	{mnemonic: "bneal", operands: []string{"1", "0"}},
	{mnemonic: "bltal", operands: []string{"0", "1"}},
	{mnemonic: "bgtal", operands: []string{"1", "0"}},
	{mnemonic: "bleal", operands: []string{"1", "1"}},
	{mnemonic: "bgeal", operands: []string{"1", "1"}},
	{mnemonic: "bdnsal", operands: []string{"d0"}},
	{mnemonic: "bapal", operands: []string{"1", "1", "0"}},
	{mnemonic: "bnaal", operands: []string{"1", "0", "0"}},
}

// valueBlock is one shape a fault-free program can be assembled from, and how
// often it is chosen relative to the others. A weight must be at least one;
// zero silently drops any mnemonic only that shape reaches, which
// TestEveryValueBlockCanBeChosen guards against.
type valueBlock struct {
	weight int
	emit   func(g *generator)
}

// valueBlocks are the shapes a fault-free program is assembled from, with the
// weight each is chosen with. Arithmetic dominates because it is the largest
// part of the instruction set. The emitters for the second group are in
// chipshapes.go.
var valueBlocks = []valueBlock{
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
	{weight: 7, emit: (*generator).emitContention},
	{weight: 7, emit: (*generator).emitCall},
	{weight: 4, emit: (*generator).emitLoop},
	{weight: 2, emit: (*generator).emitYield},

	{weight: 6, emit: (*generator).emitChipUnary},
	{weight: 6, emit: (*generator).emitExtreme},
	{weight: 5, emit: (*generator).emitTranscendental},
	{weight: 6, emit: (*generator).emitInterpolate},
	{weight: 8, emit: (*generator).emitBitfield},
	{weight: 6, emit: (*generator).emitRotate},
	{weight: 8, emit: (*generator).emitApproximateSelect},
	{weight: 6, emit: (*generator).emitApproximateBranch},
	{weight: 6, emit: (*generator).emitRelativeBranch},
	{weight: 3, emit: (*generator).emitDeviceLabel},
}

// ValueProgram returns a program meant to run to its own end without
// faulting, which TestValueProgramsRunToTheEnd holds the chip to.
//
// It terminates by construction: every branch a block emits goes forward
// except a bounded counter loop and a call whose return lands past the body
// it just left. It also fits on a chip: emission stops once the lines
// written, the labels still pending and one more block would not fit in
// maxProgramLines.
func ValueProgram(seed uint64) Program {
	g := newGenerator(seed)
	g.emitDeclarations()

	total := 0
	for _, block := range valueBlocks {
		total += block.weight
	}
	for range 6 + g.rng.IntN(20) {
		if len(g.lines)+len(g.pending)+maxBlockLines > maxProgramLines {
			break
		}
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

// emitDivisor keeps the divisor a literal. A register divisor would be legal
// for the chip, which answers an infinity or a NaN rather than faulting, but
// unpredictable to the generator: that result could reach a later bitwise
// operand as a fault this generator is not trying to produce.
func (g *generator) emitDivisor() {
	dividend, divisor := g.value(), g.nonZeroLiteral()
	g.emit(pick(g.rng, divisorOps), g.destination(false), dividend, divisor)
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
// device on it. Nothing is connected to any pin, so what these answer is a
// property of the chip alone and needs no device to be laid out first.
func (g *generator) emitDeviceProbe() {
	pin := "d" + strconv.Itoa(g.rng.IntN(6))
	if g.rng.IntN(2) == 0 {
		g.emit(pick(g.rng, deviceSetOps), g.destination(true), pin)
		return
	}
	g.emit(pick(g.rng, deviceBranchOps), pin, g.newLabel())
}

// emitStack sets the stack pointer immediately before using it, so a branch
// skipping an earlier push cannot leave a later pop out of range. Each seed
// spans the operation's whole legal range, one instruction short of where
// the fault generator raises on purpose.
func (g *generator) emitStack() {
	switch g.rng.IntN(3) {
	case 0:
		g.emit("move", "sp", strconv.Itoa(g.stackPointer(0, ic10.NumMemorySlots-1)))
		g.emit("push", g.value())
	case 1:
		g.emit("move", "sp", strconv.Itoa(g.stackPointer(1, ic10.NumMemorySlots)))
		g.emit("pop", g.destination(false))
	default:
		g.emit("move", "sp", strconv.Itoa(g.stackPointer(1, ic10.NumMemorySlots)))
		g.emit("peek", g.destination(false))
	}
}

// stackPointer picks a stack pointer in low..high, drawing the two ends of
// that range half the time and a uniform value the rest. The bias is the
// point: a uniform draw over 512 slots reaches an exact end only by luck.
func (g *generator) stackPointer(low, high int) int {
	if g.rng.IntN(2) == 0 {
		return pick(g.rng, []int{low, low + 1, high - 1, high})
	}
	return low + g.rng.IntN(high-low+1)
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

const (
	// contentionSpan is how far a data address strays from the stack pointer,
	// either side. Small enough that the two ends collide often.
	contentionSpan = 6
	// How many operations one block emits. The floor is what makes the stack
	// pointer travel: too few and the block re-seeds almost as often as it
	// moves, which is the shape it exists to get away from.
	minContentionOps = 7
	maxContentionOps = 12
)

// emitContention drives the stack and the data region at the same slots: one
// 512 slot array is shared between them, so a push and a poke a few slots
// apart write to the same place. The pointer is seeded once per block, rather
// than per operation, so it can wander far enough to meet an address the
// block also pokes; each operation is then guarded against the generator's
// own tracked pointer, which lets the seed start anywhere, including near
// either end of the array.
func (g *generator) emitContention() {
	base := g.stackPointer(0, ic10.NumMemorySlots-1)
	g.emit("move", "sp", strconv.Itoa(base))

	sp := base
	for range minContentionOps + g.rng.IntN(maxContentionOps-minContentionOps+1) {
		switch roll := g.rng.IntN(8); {
		case roll <= 2 && sp < ic10.NumMemorySlots:
			g.emit("push", g.value())
			sp++
		case roll == 3 && sp > 0:
			g.emit("pop", g.destination(false))
			sp--
		case roll == 4 && sp > 0:
			g.emit("peek", g.destination(false))
		case roll == 5:
			address, value := g.nearby(sp), g.value()
			g.emit("poke", address, value)
		case roll == 6:
			address, value := g.nearby(sp), g.value()
			g.emit("put", "db", address, value)
		default:
			g.emit("get", g.destination(false), "db", g.nearby(sp))
		}
	}
}

// nearby is a data region address within a span of the stack pointer, which is
// how a poke and a push come to contend for one slot. It is clamped into the
// array, so a pointer near an end contends there rather than off the end.
func (g *generator) nearby(sp int) string {
	address := sp - contentionSpan + g.rng.IntN(2*contentionSpan+1)
	return strconv.Itoa(min(max(address, 0), ic10.NumMemorySlots-1))
}

// emitCall completes the call mechanism: it opens with a form that certainly
// writes ra, runs a body that cannot branch, fault, or write ra, and returns
// by jumping back through ra to the line past the body, so the call
// instruction is never reached a second time.
func (g *generator) emitCall() {
	body, after := g.localLabel(), g.localLabel()

	form := pick(g.rng, callForms)
	g.emit(form.mnemonic, slices.Concat(form.operands, []string{body})...)
	g.emit("j", after)

	g.defineLocalLabel(body)
	for range 1 + g.rng.IntN(3) {
		pick(g.rng, safeBlocks)(g)
	}
	if g.rng.IntN(3) == 0 {
		g.emitContention()
	}
	g.emit("j", "ra")

	g.defineLocalLabel(after)
}

// emitLoop is the one shape that branches backward on a condition. The trip
// count is a literal in a register no other emitter writes, so the loop always
// terminates.
func (g *generator) emitLoop() {
	g.emit("move", "r"+strconv.Itoa(loopRegister), strconv.Itoa(1+g.rng.IntN(4)))
	name := g.localLabel()
	g.defineLocalLabel(name)
	for range 1 + g.rng.IntN(2) {
		g.emitBinary()
	}
	g.emit("sub", "r"+strconv.Itoa(loopRegister), "r"+strconv.Itoa(loopRegister), "1")
	g.emit("bgtz", "r"+strconv.Itoa(loopRegister), name)
}

func (g *generator) emitYield() { g.emit("yield") }
