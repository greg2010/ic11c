package difftest

import (
	"maps"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"

	"github.com/greg2010/ic11c/internal/oracle"
)

// Register conventions every generated program keeps to, so that a program
// cannot clobber the registers its own scaffolding depends on.
const (
	// generalRegisters is r0 through r13, the only destinations a generated
	// instruction writes.
	generalRegisters = 14
	// scratchRegister carries the out of range index a fault recipe builds.
	scratchRegister = 14
	// loopRegister counts a bounded loop down.
	loopRegister = 15
)

// rngStream keeps the two halves of the PCG seed from being equal, which the
// generator would otherwise pass for every program.
const rngStream = 0x9e3779b97f4a7c15

// literalPool is the numeric operands a generated instruction draws from.
//
// Plain decimal only: ic10emu's number parser rejects exponent notation, so
// "1e30" is a compile error there and a value here. Every entry is finite and
// well inside the range the bitwise instructions range-check, which is what
// makes a literal always safe as a bitwise operand.
var literalPool = []string{
	"0", "1", "-1", "2", "-2", "3", "-3", "7", "-7", "10", "-10",
	"255", "-256", "65535", "4294967295", "-4294967296",
	"8388607", "4503599627370496", "-4503599627370496",
	"9007199254740991", "-9007199254740991",
	"1000000", "-1000000",
	"0.5", "-0.5", "0.25", "1.5", "-2.5", "3.14159",
	"0.0009765625", "-0.001", "123.456", "-123.456", "1.0000001",
}

// initialPool seeds the registers a program starts from. Every value is finite
// and inside the bitwise range check, so the generator can treat every general
// register as a safe bitwise operand before the program writes anything.
var initialPool = []float64{
	0, 1, -1, 2, -3, 0.5, -0.25, 255, -4096, 65536,
	4503599627370496, -4503599627370496, 9007199254740991, -9007199254740991,
	1e9, -1e9, 3.141592653589793, -2.718281828459045,
	math.SmallestNonzeroFloat64, math.Copysign(0, -1),
}

// shiftDistancePool spans the whole low six bits the chip masks a distance
// down to, plus the values outside it that prove the masking.
var shiftDistancePool = []string{
	"0", "1", "7", "31", "32", "52", "53", "54", "63", "64", "65", "-1", "100",
}

func newRNG(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, seed^rngStream)) }

// generator accumulates the lines of one program.
//
// bounded tracks which general registers provably hold a value the bitwise and
// shift instructions accept, so those instructions can take a register operand
// without risking a fault the program was not meant to raise. It is cleared at
// every label, because a label is a join the generator cannot reason across.
type generator struct {
	rng       *rand.Rand
	lines     []string
	mnemonics map[string]bool
	bounded   [generalRegisters]bool
	// pending are labels a branch already targets and no line has defined yet.
	pending  []string
	labelSeq int
	defines  []string
	aliases  []string
	initial  oracle.State
}

func newGenerator(seed uint64) *generator {
	g := &generator{rng: newRNG(seed), mnemonics: make(map[string]bool)}
	for i := range generalRegisters {
		g.bounded[i] = true
		g.initial.Registers[i] = pick(g.rng, initialPool)
	}
	return g
}

func pick[T any](rng *rand.Rand, values []T) T { return values[rng.IntN(len(values))] }

// emit appends one instruction and records the mnemonic for coverage.
func (g *generator) emit(mnemonic string, operands ...string) {
	g.mnemonics[mnemonic] = true
	if len(operands) == 0 {
		g.lines = append(g.lines, mnemonic)
		return
	}
	g.lines = append(g.lines, mnemonic+" "+strings.Join(operands, " "))
}

// raw appends a line that is not an instruction, such as a bare label.
func (g *generator) raw(text string) { g.lines = append(g.lines, text) }

// newLabel reserves a label name and queues it for definition. Every reserved
// label is defined before the program ends, so a branch always has a target.
func (g *generator) newLabel() string {
	name := "t" + strconv.Itoa(g.labelSeq)
	g.labelSeq++
	g.pending = append(g.pending, name)
	return name
}

// defineLabel writes out the oldest reserved label, if there is one.
func (g *generator) defineLabel() bool {
	if len(g.pending) == 0 {
		return false
	}
	name := g.pending[0]
	g.pending = g.pending[1:]
	g.raw(name + ":")
	g.clearBounded()
	return true
}

func (g *generator) flushLabels() {
	for g.defineLabel() {
	}
}

func (g *generator) clearBounded() {
	for i := range g.bounded {
		g.bounded[i] = false
	}
}

// destination returns a general register to write, and records what the write
// does to its bitwise safety.
func (g *generator) destination(staysBounded bool) string {
	index := g.rng.IntN(generalRegisters)
	g.bounded[index] = staysBounded
	return "r" + strconv.Itoa(index)
}

// value returns any operand a `num` position accepts. The result may be
// infinite or NaN at run time, which every instruction the generators emit
// tolerates except the bitwise and shift family; those take boundedValue.
func (g *generator) value() string {
	switch n := g.rng.IntN(10); {
	case n < 4:
		return pick(g.rng, literalPool)
	case n < 8:
		return "r" + strconv.Itoa(g.rng.IntN(generalRegisters))
	case n == 8 && len(g.defines) > 0:
		return pick(g.rng, g.defines)
	case n == 9 && len(g.aliases) > 0:
		return pick(g.rng, g.aliases)
	default:
		return pick(g.rng, literalPool)
	}
}

// boundedValue returns an operand the bitwise and shift instructions accept
// without faulting: a literal, a compile time define, or a register the
// generator knows holds a value inside the range they check.
func (g *generator) boundedValue() string {
	var safe []string
	for i := range generalRegisters {
		if g.bounded[i] {
			safe = append(safe, "r"+strconv.Itoa(i))
		}
	}
	safe = append(safe, g.defines...)
	if len(safe) == 0 || g.rng.IntN(2) == 0 {
		return pick(g.rng, literalPool)
	}
	return pick(g.rng, safe)
}

// nonZeroLiteral is a divisor. Keeping it off zero keeps an infinity out of the
// value generator: the chip returns one rather than faulting, but it would then
// travel through registers the generator can no longer predict, and reaching a
// bitwise operand it is a fault the value generator is not trying to produce.
func (g *generator) nonZeroLiteral() string {
	for {
		if literal := pick(g.rng, literalPool); literal != "0" {
			return literal
		}
	}
}

// address is a memory slot every implementation agrees is in range.
func (g *generator) address() string { return strconv.Itoa(g.rng.IntN(512)) }

func (g *generator) program(seed uint64, kind, recipe string) Program {
	return Program{
		Seed:      seed,
		Kind:      kind,
		Recipe:    recipe,
		Source:    strings.Join(g.lines, "\n"),
		Initial:   g.initial,
		Mnemonics: slices.Sorted(maps.Keys(g.mnemonics)),
	}
}
