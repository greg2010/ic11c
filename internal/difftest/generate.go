package difftest

import (
	"maps"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/emit"
	"github.com/greg2010/ic11c/internal/ic10"
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

// What a ProgrammableChip holds, from the decompiled game. A corpus that
// outgrew one of them would go on running cleanly while it had stopped being a
// corpus of programs the machine could hold.
const (
	maxProgramLines = emit.MaxLines
	maxProgramBytes = emit.MaxBytes
	maxLineLength   = emit.MaxLineLength
)

// separatorBytes is what the game's editor charges between two lines.
// InputSourceCode.UpdateFileSize increments its counter twice per charged
// line and reads no newline sequence at all, so this is the game's own pair
// of increments rather than a platform separator's width.
const separatorBytes = 2

// sourceBytes is the size the game's editor charges for source, which is not
// its length: UpdateFileSize adds a separator only between two lines that
// both hold text, so trailing blank lines are free, and a line wider than
// maxLineLength is charged at the cut, since InputSourceCode.Paste runs each
// line through AsciiString.ParseLine before it reaches the grid.
func sourceBytes(source string) int {
	lines := strings.Split(strings.TrimSuffix(source, "\n"), "\n")
	last := 0
	for i, line := range lines {
		if line != "" {
			last = i
		}
	}
	total := 0
	for i, line := range lines {
		total += min(len(line), maxLineLength)
		if i < last {
			total += separatorBytes
		}
	}
	return total
}

// maxBlockLines is the most lines one valueBlocks emitter appends in a single
// call, counting a label it reserved and has yet to flush. [ValueProgram]
// keeps this much room free before starting a block; TestNoBlockOutgrowsItsHeadroom
// holds the number to what the emitters actually do.
const maxBlockLines = 21

// rngStream keeps the two halves of the PCG seed from being equal, which the
// generator would otherwise pass for every program.
const rngStream = 0x9e3779b97f4a7c15

// literalPool is the numeric operands a generated instruction draws from,
// spelled as plain decimal since the game parses an operand with
// NumberStyles.Number, which allows no exponent.
//
// Every entry is safely inside the ±2^63 bound the bitwise instructions fault
// outside of. The pool also reaches past 2^53, where DoubleToLong's modular
// reduction turns over: TestPoolsReachTheConversionBoundary holds it to
// exercising that boundary rather than stopping short of it.
var literalPool = []string{
	"0", "1", "-1", "2", "-2", "3", "-3", "7", "-7", "10", "-10",
	"255", "-256", "65535", "4294967295", "-4294967296",
	"8388607", "4503599627370496", "-4503599627370496",
	"9007199254740991", "-9007199254740991",
	"9007199254740992", "-9007199254740992",
	"13510798882111488", "-13510798882111488",
	"4611686018427387904", "-4611686018427387904",
	"1000000", "-1000000",
	"0.5", "-0.5", "0.25", "1.5", "-2.5", "3.14159",
	"0.0009765625", "-0.001", "123.456", "-123.456", "1.0000001",
}

// initialPool seeds the registers a program starts from. It answers to the
// same bounds as literalPool, so every general register is a safe bitwise
// operand before the program writes anything.
var initialPool = []float64{
	0, 1, -1, 2, -3, 0.5, -0.25, 255, -4096, 65536,
	4503599627370496, -4503599627370496, 9007199254740991, -9007199254740991,
	9007199254740992, -9007199254740992, 13510798882111488, -13510798882111488,
	4611686018427387904, -4611686018427387904,
	1e9, -1e9, 3.141592653589793, -2.718281828459045,
	math.SmallestNonzeroFloat64, math.Copysign(0, -1),
}

// shiftDistancePool samples the low six bits the chip masks a distance down
// to — both ends, the values either side of 53, and out-of-range distances
// that prove the masking — rather than enumerating the whole range.
var shiftDistancePool = []string{
	"0", "1", "7", "31", "32", "52", "53", "54", "63", "64", "65", "-1", "100",
}

func newRNG(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, seed^rngStream)) }

// generator accumulates the lines of one program.
type generator struct {
	rng       *rand.Rand
	lines     []string
	mnemonics map[string]bool
	// bounded tracks which general registers provably hold a value the
	// bitwise and shift instructions accept without faulting. It is cleared
	// at every label, since a label is a join the generator cannot reason
	// across.
	bounded [generalRegisters]bool
	// pending are labels a branch already targets and no line has defined yet.
	pending  []string
	labelSeq int
	defines  []string
	aliases  []string
	// pinSeq numbers the names label binds, so no two bind the same one.
	pinSeq  int
	initial chip.State
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

// localLabel reserves a label name and leaves it out of the pending queue, so
// the only lines that can target it are the ones the caller emits around it.
// That is what lets a block define its own labels without an earlier forward
// branch landing in the middle of it.
func (g *generator) localLabel() string {
	name := "t" + strconv.Itoa(g.labelSeq)
	g.labelSeq++
	return name
}

// defineLocalLabel writes a reserved local label out where it stands.
func (g *generator) defineLocalLabel(name string) {
	g.raw(name + ":")
	g.clearBounded()
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

// nonZeroLiteral is a divisor. Zero is excluded because the chip answers an
// infinity rather than faulting, which would then reach a bitwise operand as
// a fault this generator is not trying to produce.
func (g *generator) nonZeroLiteral() string {
	for {
		if literal := pick(g.rng, literalPool); literal != "0" {
			return literal
		}
	}
}

// address is a memory slot inside the chip's array, so a data access naming one
// reaches a slot rather than the bounds check either end of it stands on.
func (g *generator) address() string { return strconv.Itoa(g.rng.IntN(ic10.NumMemorySlots)) }

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
