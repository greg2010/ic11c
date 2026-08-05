package difftest

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/chiptest"
	"github.com/greg2010/ic11c/internal/ic10"
)

// corpusSample is how many programs the generator-only tests draw. It is large
// enough for every weighted shape to appear and small enough to stay in the
// ordinary suite's budget.
const corpusSample = 2000

// sample generates one program of each kind for every seed in a fixed range.
func sample(tb testing.TB, n int) []Program {
	tb.Helper()
	programs := make([]Program, 0, 2*n)
	for seed := range uint64(n) {
		programs = append(programs, ValueProgram(seed), FaultProgram(seed))
	}
	return programs
}

func TestGenerationIsReproducible(t *testing.T) {
	tests := []struct {
		name     string
		generate func(uint64) Program
	}{
		{name: "value", generate: ValueProgram},
		{name: "fault", generate: FaultProgram},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runReproducible(t, tt.generate)
		})
	}
}

func runReproducible(t *testing.T, generate func(uint64) Program) {
	t.Helper()
	for _, seed := range []uint64{0, 1, 42, 1 << 40, ^uint64(0)} {
		first, second := generate(seed), generate(seed)
		if first.Source != second.Source {
			t.Errorf("seed %d: source differs between runs\nfirst:\n%s\nsecond:\n%s",
				seed, first.Source, second.Source)
		}
		if first.Initial != second.Initial {
			t.Errorf("seed %d: initial state differs between runs", seed)
		}
		if first.Recipe != second.Recipe {
			t.Errorf("seed %d: recipe = %q then %q", seed, first.Recipe, second.Recipe)
		}
	}
}

// TestDistinctSeedsGiveDistinctPrograms is the only check on the seed
// reaching the generator at full width: a seed folded into buckets — a stray
// mask, a modulus, a hash truncated to fit — would still pass the coverage
// tests, since every mnemonic and recipe stays reachable through the fold.
func TestDistinctSeedsGiveDistinctPrograms(t *testing.T) {
	seen := make(map[string]uint64)
	for _, p := range sample(t, corpusSample) {
		key := p.Kind + "\x00" + p.Source
		if other, ok := seen[key]; ok && other != p.Seed {
			t.Errorf("seeds %d and %d produce the same %s program", other, p.Seed, p.Kind)
		}
		seen[key] = p.Seed
	}
}

// TestValueProgramsRunToTheEnd holds the value generator to its claim: a
// program that faults still assembles and runs, so nothing else would notice
// the corpus quietly losing coverage past the faulting line.
func TestValueProgramsRunToTheEnd(t *testing.T) {
	ctx, harness := chiptest.Harness(t)
	for seed := range uint64(corpusSample) {
		p := ValueProgram(seed)
		got, err := Run(ctx, harness, p)
		if err != nil {
			t.Fatalf("%s: %v\n%s", p, err, p.Source)
		}
		if got.Stop != chip.StopEnded {
			t.Fatalf("%s: stopped %q (compile %v, fault %v), want %q\n%s",
				p, got.Stop, got.CompileError, got.Fault, chip.StopEnded, p.Source)
		}
	}
}

// TestSourceBytes pins what the game's editor spends its budget on, which is
// not the length of the string it was given; every figure here is arithmetic
// done by hand.
func TestSourceBytes(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   int
	}{
		{name: "one line is its own text", source: "move r0 1", want: 9},
		{name: "a terminating newline ends the last line", source: "move r0 1\n", want: 9},
		{name: "a separator costs two, not one", source: "ab\ncd\nef", want: 6 + 2*2},
		{name: "a blank line between two lines of text pays its separator", source: "ab\n\ncd", want: 4 + 2*2},
		{name: "trailing blank lines cost nothing at all", source: "ab\n\n\n", want: 2},
		{name: "a program of nothing but blank lines", source: "\n\n\n", want: 0},
		{
			// The paste cuts the line on the way in, so the rest never
			// reaches the editor's grid.
			name:   "a line the paste would cut is charged at the cut",
			source: strings.Repeat("x", maxLineLength+10) + "\nab",
			want:   maxLineLength + 2 + 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sourceBytes(tt.source); got != tt.want {
				t.Errorf("sourceBytes = %d, want %d", got, tt.want)
			}
		})
	}
}

// widest records the largest measurement taken over a corpus and the program that produced it.
type widest struct {
	value   int
	program Program
}

func (w *widest) observe(value int, p Program) {
	if value > w.value {
		w.value, w.program = value, p
	}
}

// TestGeneratedProgramsFitTheChip holds the corpus to programs a chip could
// hold. Nothing about running a program enforces the editor's limits, so
// nothing else here would notice the generator growing past what a player
// could type in.
func TestGeneratedProgramsFitTheChip(t *testing.T) {
	// Wider than corpusSample: a line count is a tail property, and the
	// widest program in 2,000 seeds sits inside the limit while the widest in
	// 20,000 does not.
	const seeds = 20_000

	var lines, bytes, longestLine widest
	for _, p := range sample(t, seeds) {
		source := strings.Split(p.Source, "\n")
		lines.observe(len(source), p)
		// The editor's own count, not the string's; see sourceBytes.
		bytes.observe(sourceBytes(p.Source), p)
		for _, line := range source {
			longestLine.observe(len(line), p)
		}
	}

	tests := []struct {
		name  string
		got   widest
		limit int
	}{
		{name: "lines", got: lines, limit: maxProgramLines},
		{name: "bytes", got: bytes, limit: maxProgramBytes},
		{name: "characters in one line", got: longestLine, limit: maxLineLength},
	}
	for _, tt := range tests {
		t.Logf("widest %s: %d of %d", tt.name, tt.got.value, tt.limit)
		if tt.got.value > tt.limit {
			t.Errorf("%s generates %d %s and a chip holds %d\n%s",
				tt.got.program, tt.got.value, tt.name, tt.limit, tt.got.program.Source)
		}
	}
}

// TestNoBlockOutgrowsItsHeadroom holds maxProgramLines to being reachable:
// ValueProgram stops emitting blocks once fewer than maxBlockLines remain, so
// an emitter that grew past that bound would overrun the limit from just
// under it.
func TestNoBlockOutgrowsItsHeadroom(t *testing.T) {
	var widestBlock int
	for seed := range uint64(corpusSample) {
		for _, block := range valueBlocks {
			g := newGenerator(seed)
			block.emit(g)
			widestBlock = max(widestBlock, len(g.lines)+len(g.pending))
		}
	}
	t.Logf("widest block: %d lines of %d", widestBlock, maxBlockLines)
	if widestBlock > maxBlockLines {
		t.Errorf("one block emits %d lines and ValueProgram keeps %d free, so a program can "+
			"overrun the %d line limit from just under it", widestBlock, maxBlockLines, maxProgramLines)
	}
}

// TestEveryValueBlockCanBeChosen holds the block table to every row being
// reachable. A row written without a weight is chosen never, and nothing
// else would notice unless that shape were the last source of a mnemonic.
func TestEveryValueBlockCanBeChosen(t *testing.T) {
	for i, block := range valueBlocks {
		if block.emit == nil {
			t.Errorf("value block %d emits nothing", i)
			continue
		}
		if block.weight < 1 {
			t.Errorf("value block %d has weight %d, so it is never chosen and whatever it alone "+
				"emits is out of the corpus", i, block.weight)
		}
	}
}

// TestSourceHasNoTrailingNewline pins the one representation choice a source
// string can carry silently. The chip splits on "\n" and keeps a trailing one
// as a final empty line that retires an instruction of its own, so a program
// carrying one is not the program it looks like.
func TestSourceHasNoTrailingNewline(t *testing.T) {
	for _, p := range sample(t, 200) {
		if strings.HasSuffix(p.Source, "\n") {
			t.Fatalf("%s ends in a newline", p)
		}
		if p.Source == "" {
			t.Fatalf("%s is empty", p)
		}
	}
}

// TestExcludedMnemonicsAreNeverEmitted is the exclusion policy's only
// enforcement: nothing stops an emitter naming an excluded mnemonic except
// this.
func TestExcludedMnemonicsAreNeverEmitted(t *testing.T) {
	for _, p := range sample(t, corpusSample/2) {
		for _, mnemonic := range p.Mnemonics {
			if reason, ok := Excluded(mnemonic); ok {
				t.Fatalf("%s emits excluded mnemonic %q (%s)\n%s", p, mnemonic, reason, p.Source)
			}
		}
		for line := range strings.SplitSeq(p.Source, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			if _, ok := Excluded(fields[0]); ok {
				t.Fatalf("%s has a line starting with excluded mnemonic %q\n%s", p, fields[0], p.Source)
			}
		}
	}
}

// TestExclusionsPartitionTheInstructionSet holds the coverage claim to account:
// every mnemonic is either something a generator emits or something excluded
// with a reason, and nothing is both.
func TestExclusionsPartitionTheInstructionSet(t *testing.T) {
	generated := make(map[string]bool)
	for _, mnemonic := range GeneratedMnemonics() {
		generated[mnemonic] = true
	}
	for _, instruction := range ic10.Instructions {
		reason, isExcluded := Excluded(instruction.Mnemonic)
		switch {
		case isExcluded && generated[instruction.Mnemonic]:
			t.Errorf("%s is both generated and excluded", instruction.Mnemonic)
		case isExcluded && reason == "":
			t.Errorf("%s is excluded with no reason", instruction.Mnemonic)
		case !isExcluded && !generated[instruction.Mnemonic]:
			t.Errorf("%s is neither generated nor excluded", instruction.Mnemonic)
		}
	}
	for mnemonic := range excluded {
		if _, ok := ic10.LookupInstruction(mnemonic); !ok {
			t.Errorf("excluded mnemonic %q is not in the instruction set", mnemonic)
		}
	}
}

// TestCorpusReachesTheGeneratedMnemonics keeps the declared set honest in both
// directions: a mnemonic listed but no longer emitted is as much a defect as
// one emitted but not listed.
func TestCorpusReachesTheGeneratedMnemonics(t *testing.T) {
	const drawn = corpusSample / 2
	coverage := make(Coverage)
	for _, p := range sample(t, drawn) {
		coverage.Add(p)
	}
	declared := make(map[string]bool)
	for _, mnemonic := range GeneratedMnemonics() {
		declared[mnemonic] = true
	}
	for _, mnemonic := range coverage.Reached() {
		if !declared[mnemonic] {
			t.Errorf("corpus reaches %q, which the exclusion list keeps out", mnemonic)
		}
	}
	for mnemonic := range declared {
		if coverage[mnemonic] == 0 {
			t.Errorf("%q is excluded by no reason the list states, and a %d program "+
				"corpus never reaches it, so nothing emits it", mnemonic, drawn)
		}
	}
	t.Log(coverage.Report())
}

const (
	// conversionTurnover is the modulus DoubleToLong reduces by and the bit
	// LongToDouble sign-extends from: exactly this value arrives as 0, and a
	// bit set here comes back negative.
	conversionTurnover float64 = 1 << 53
	// faultBound is the magnitude the bitwise instructions fault strictly
	// outside of. It is not the boundary the pools have to reach; that is
	// conversionTurnover.
	faultBound float64 = 1 << 63
)

// TestPoolsReachTheConversionBoundary holds the pools to conversionTurnover,
// not faultBound: they must also hold a value past it with a non-zero
// residue, since every multiple of 2^53 alone reduces to 0 too.
func TestPoolsReachTheConversionBoundary(t *testing.T) {
	literals := make([]float64, len(literalPool))
	for i, literal := range literalPool {
		value, err := strconv.ParseFloat(literal, 64)
		if err != nil {
			t.Fatalf("literalPool holds %q, which is not a number: %v", literal, err)
		}
		literals[i] = value
	}

	tests := []struct {
		name string
		pool []float64
	}{
		{name: "literalPool", pool: literals},
		{name: "initialPool", pool: initialPool},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reachesTurnover, leavesAResidue bool
			for _, value := range tt.pool {
				magnitude := math.Abs(value)
				switch {
				case magnitude > faultBound:
					t.Errorf("%v is outside ±2^63, so a bitwise instruction taking it faults and "+
						"the generators can no longer treat every pool entry as a safe operand", value)
				case magnitude == conversionTurnover:
					reachesTurnover = true
				case magnitude > conversionTurnover && math.Mod(magnitude, conversionTurnover) != 0:
					leavesAResidue = true
				}
			}
			if !reachesTurnover {
				t.Errorf("nothing here is ±2^53 exactly, so no generated bitwise operand reaches " +
					"the value DoubleToLong reduces to 0")
			}
			if !leavesAResidue {
				t.Errorf("nothing here is past 2^53 with a non-zero remainder, so every generated " +
					"bitwise operand either converts unchanged or converts to 0, and a conversion " +
					"that simply gave up above the boundary would answer the same")
			}
		})
	}
}

// TestGeneratedNamesAreNotReserved guards the one silent failure mode in a
// generated name: a label or define colliding with a name the chip already
// resolves is not rejected, it is shadowed, and every line referring to it then
// faults forever.
func TestGeneratedNamesAreNotReserved(t *testing.T) {
	for _, p := range sample(t, 200) {
		for line := range strings.SplitSeq(p.Source, "\n") {
			fields := strings.Fields(line)
			var name string
			switch {
			case len(fields) == 1 && strings.HasSuffix(fields[0], ":"):
				name = strings.TrimSuffix(fields[0], ":")
			case len(fields) == 3 && (fields[0] == "define" || fields[0] == "alias"):
				name = fields[1]
			default:
				continue
			}
			if ic10.IsReservedWord(name) {
				t.Errorf("%s declares reserved name %q", p, name)
			}
		}
	}
}
