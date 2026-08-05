package regalloc

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/source"
)

type builder struct {
	t    *testing.T
	fn   *mir.Func
	vr   map[string]mir.VirtReg
	line int
}

func newBuilder(t *testing.T, name string) *builder {
	t.Helper()
	return &builder{t: t, fn: mir.NewFunc(name, source.Position{File: "t.mc", Line: 1, Column: 1}), vr: make(map[string]mir.VirtReg)}
}

// v names a virtual register, creating it the first time it is mentioned.
func (b *builder) v(name string) mir.VirtReg {
	if v, ok := b.vr[name]; ok {
		return v
	}
	v := b.fn.NewVirtReg()
	b.vr[name] = v
	return v
}

func (b *builder) block(label string) *mir.Block {
	b.t.Helper()
	return b.fn.NewBlock(label, b.pos())
}

func (b *builder) pos() source.Position {
	b.line++
	return source.Position{File: "t.mc", Offset: b.line, Line: b.line, Column: 1}
}

func (b *builder) instr(op ic10.Opcode, args ...mir.Operand) *mir.Instr {
	b.t.Helper()
	instr, err := mir.NewInstr(op, b.pos(), args...)
	if err != nil {
		b.t.Fatalf("NewInstr(%v): %v", op, err)
	}
	return instr
}

func (b *builder) emit(blk *mir.Block, op ic10.Opcode, args ...mir.Operand) {
	b.t.Helper()
	blk.Append(b.instr(op, args...))
}

func imm(v float64) mir.Imm { return mir.Imm{Value: v} }

func sink(b *builder, blk *mir.Block, v mir.VirtReg) {
	b.t.Helper()
	b.emit(blk, isa.OpS, mir.NewDeviceBase(), mir.LogicType{Value: 0}, v)
}

// limited reserves every register past the first allocatable ones and the
// scratch registers after them, so a test can pin the size of the file it
// allocates against. The file is allocatable+scratch wide and narrows, once the
// function spills at all, by what its widest line reads out of memory.
func limited(t *testing.T, allocatable, scratch, base int) Config {
	t.Helper()
	if allocatable+scratch > ic10.NumGeneralRegisters {
		t.Fatalf("limited(%d, %d): more than the %d general registers", allocatable, scratch, ic10.NumGeneralRegisters)
	}
	cfg := Config{SpillSlotBase: base}
	for r := range ic10.Register(ic10.NumRegisters) {
		switch {
		case int(r) < allocatable:
		case int(r) < allocatable+scratch:
			cfg.Scratch = append(cfg.Scratch, r)
		default:
			cfg.Reserved = append(cfg.Reserved, r)
		}
	}
	return cfg
}

func assertWellFormed(t *testing.T, fn *mir.Func, allowed map[ic10.Register]bool) {
	t.Helper()
	prog := &mir.Program{Funcs: []*mir.Func{fn}}
	if err := prog.Validate(); err != nil {
		t.Fatalf("Validate after allocation: %v", err)
	}
	for _, instr := range fn.AllInstrs() {
		info, ok := instr.Op.Instruction()
		if !ok {
			t.Fatalf("%s: opcode is not in the instruction table", instr)
		}
		for j, arg := range instr.Args {
			reg, ok := arg.(mir.PhysReg)
			if !ok {
				if _, virtual := arg.(mir.VirtReg); virtual {
					t.Errorf("%s: operand %d is still virtual", instr, j)
				}
				continue
			}
			if !allowed[reg.Reg] {
				t.Errorf("%s: operand %d is %s, which is reserved", instr, j, reg.Reg)
			}
			// Read off the operand table rather than through the allocator's own
			// deviceConstrained, which would make the gate agree with whatever
			// that decided. Both kinds are asked for the reason
			// [TestDeviceConstrained] gives.
			if j < len(info.Operands) && reg.Reg >= ic10.NumGeneralRegisters &&
				(info.Operands[j].Accepts(ic10.OperandDevice) || info.Operands[j].Accepts(ic10.OperandRefID)) {
				t.Errorf("%s: operand %d holds a device reference in %s, which the chip cannot resolve there", instr, j, reg.Reg)
			}
		}
	}
}

func allowedRegisters(fn *mir.Func, cfg Config) map[ic10.Register]bool {
	allowed := make(map[ic10.Register]bool)
	for _, r := range cfg.allocatable(preassigned(fn)) {
		allowed[r] = true
	}
	for _, r := range cfg.Scratch {
		allowed[r] = true
	}
	for r := range preassigned(fn) {
		allowed[r] = true
	}
	return allowed
}

func straightLine(t *testing.T) *builder {
	t.Helper()
	b := newBuilder(t, "straight")
	blk := b.block("entry")
	b.emit(blk, isa.OpMove, b.v("a"), imm(1))
	b.emit(blk, isa.OpMove, b.v("b"), imm(2))
	b.emit(blk, isa.OpMove, b.v("c"), imm(3))
	b.emit(blk, isa.OpAdd, b.v("d"), b.v("a"), b.v("b"))
	b.emit(blk, isa.OpAdd, b.v("d"), b.v("d"), b.v("c"))
	sink(b, blk, b.v("d"))
	return b
}

// pinnedRegister names r3 outright, which is the shape selection hands over for
// a call: a register the input pins and carries no live range for.
func pinnedRegister(t *testing.T) *builder {
	t.Helper()
	b := newBuilder(t, "pinned")
	blk := b.block("entry")
	b.emit(blk, isa.OpMove, mir.PhysReg{Reg: 3}, imm(1))
	b.emit(blk, isa.OpMove, b.v("a"), mir.PhysReg{Reg: 3})
	sink(b, blk, b.v("a"))
	return b
}

func loopAcrossBackEdge(t *testing.T) *builder {
	t.Helper()
	b := newBuilder(t, "loop")
	entry, body, done := b.block("entry"), b.block("body"), b.block("done")
	entry.AddSucc(body)
	body.AddSucc(body)
	body.AddSucc(done)

	b.emit(entry, isa.OpMove, b.v("i"), imm(0))
	b.emit(entry, isa.OpMove, b.v("limit"), imm(10))
	b.emit(entry, isa.OpMove, b.v("acc"), imm(0))
	b.emit(entry, isa.OpJ, mir.Label{Name: "body"})

	b.emit(body, isa.OpAdd, b.v("acc"), b.v("acc"), b.v("i"))
	b.emit(body, isa.OpAdd, b.v("i"), b.v("i"), imm(1))
	b.emit(body, isa.OpBlt, b.v("i"), b.v("limit"), mir.Label{Name: "body"})

	sink(b, done, b.v("acc"))
	return b
}

// liveAcrossIntervening is the shape an interval stopping at the last textual
// use gets wrong.
func liveAcrossIntervening(t *testing.T) *builder {
	t.Helper()
	b := newBuilder(t, "intervening")
	entry, mid, exit := b.block("entry"), b.block("mid"), b.block("exit")
	entry.AddSucc(mid)
	mid.AddSucc(exit)

	b.emit(entry, isa.OpMove, b.v("kept"), imm(1))
	b.emit(entry, isa.OpMove, b.v("other"), imm(2))
	b.emit(entry, isa.OpJ, mir.Label{Name: "mid"})

	b.emit(mid, isa.OpAdd, b.v("other"), b.v("other"), b.v("other"))
	b.emit(mid, isa.OpJ, mir.Label{Name: "exit"})

	b.emit(exit, isa.OpAdd, b.v("sum"), b.v("kept"), b.v("other"))
	sink(b, exit, b.v("sum"))
	return b
}

func holeAcrossBlock(t *testing.T) *builder {
	t.Helper()
	b := newBuilder(t, "hole")
	entry, skip, use, join := b.block("entry"), b.block("skip"), b.block("use"), b.block("join")
	entry.AddSucc(skip)
	entry.AddSucc(use)
	skip.AddSucc(join)
	use.AddSucc(join)

	b.emit(entry, isa.OpMove, b.v("held"), imm(1))
	b.emit(entry, isa.OpMove, b.v("out"), imm(0))
	b.emit(entry, isa.OpBlt, imm(0), imm(1), mir.Label{Name: "use"})

	b.emit(skip, isa.OpMove, b.v("out"), imm(7))
	b.emit(skip, isa.OpJ, mir.Label{Name: "join"})

	b.emit(use, isa.OpAdd, b.v("out"), b.v("held"), imm(2))
	b.emit(use, isa.OpJ, mir.Label{Name: "join"})

	sink(b, join, b.v("out"))
	return b
}

func valuesInsideAHole(t *testing.T) *builder {
	t.Helper()
	b := newBuilder(t, "insidehole")
	entry, skip, use, join := b.block("entry"), b.block("skip"), b.block("use"), b.block("join")
	entry.AddSucc(skip)
	entry.AddSucc(use)
	skip.AddSucc(join)
	use.AddSucc(join)

	b.emit(entry, isa.OpMove, b.v("held"), imm(1))
	b.emit(entry, isa.OpBlt, imm(0), imm(1), mir.Label{Name: "use"})

	b.emit(skip, isa.OpMove, b.v("tmp"), imm(5))
	b.emit(skip, isa.OpMove, b.v("out"), b.v("tmp"))
	b.emit(skip, isa.OpJ, mir.Label{Name: "join"})

	b.emit(use, isa.OpAdd, b.v("out"), b.v("held"), imm(2))
	b.emit(use, isa.OpJ, mir.Label{Name: "join"})

	sink(b, join, b.v("out"))
	return b
}

// crowd adds n values live across the whole function, which is how a small
// fixture is pushed past its register file: the file cannot be narrower than
// the scratch set, so a two or three value fixture fits in every configuration
// a spill is expressible in.
func crowd(t *testing.T, b *builder, n int) *builder {
	t.Helper()
	entry, last := b.fn.Blocks[0], b.fn.Blocks[len(b.fn.Blocks)-1]
	defs := make([]*mir.Instr, 0, n)
	for i := range n {
		defs = append(defs, b.instr(isa.OpMove, b.v("crowd"+strconv.Itoa(i)), imm(float64(i))))
	}
	// The reads go on before the definitions go in front, so that a fixture of
	// one block ends up with the two around its own code rather than inside it.
	for i := range n {
		sink(b, last, b.v("crowd"+strconv.Itoa(i)))
	}
	entry.Instrs = append(defs, entry.Instrs...)
	return b
}

// pressure builds a function holding n values live at once: every one is
// defined before any is read, so the whole set interferes.
func pressure(t *testing.T, n int) *builder {
	t.Helper()
	b := newBuilder(t, "pressure")
	blk := b.block("entry")
	for i := range n {
		b.emit(blk, isa.OpMove, b.v("v"+strconv.Itoa(i)), imm(float64(i)))
	}
	for i := range n {
		sink(b, blk, b.v("v"+strconv.Itoa(i)))
	}
	return b
}

// twoSpilledInOneLine reads two cold values on one line, the one shape needing
// a second scratch register: the machine reads every operand before it writes
// anything, so both reloads have to be in registers at once.
func twoSpilledInOneLine(t *testing.T) *builder {
	t.Helper()
	b := newBuilder(t, "twospilled")
	blk := b.block("entry")
	b.emit(blk, isa.OpMove, b.v("cold0"), imm(1))
	b.emit(blk, isa.OpMove, b.v("cold1"), imm(2))
	b.emit(blk, isa.OpMove, b.v("hot"), imm(3))
	for range 4 {
		b.emit(blk, isa.OpAdd, b.v("hot"), b.v("hot"), b.v("hot"))
	}
	b.emit(blk, isa.OpAdd, b.v("out"), b.v("cold0"), b.v("cold1"))
	b.emit(blk, isa.OpAdd, b.v("out"), b.v("out"), b.v("hot"))
	sink(b, blk, b.v("out"))
	return b
}

func copyThenBothUsed(t *testing.T) *builder {
	t.Helper()
	b := newBuilder(t, "interfere")
	blk := b.block("entry")
	b.emit(blk, isa.OpMove, b.v("src"), imm(1))
	b.emit(blk, isa.OpMove, b.v("copy"), b.v("src"))
	b.emit(blk, isa.OpAdd, b.v("sum"), b.v("src"), b.v("copy"))
	sink(b, blk, b.v("sum"))
	return b
}

// TestAllocatePreservesMeaning is the property the rest of the suite rests on:
// a rewritten function may look well formed and still read the wrong register,
// so every use is held to the definitions that reached it before allocation.
func TestAllocatePreservesMeaning(t *testing.T) {
	crowded := func(build func(*testing.T) *builder, n int) func(*testing.T) *builder {
		return func(t *testing.T) *builder { return crowd(t, build(t), n) }
	}
	tests := []struct {
		name      string
		build     func(*testing.T) *builder
		cfg       Config
		wantSpill bool
	}{
		{name: "straight line under capacity", build: straightLine, cfg: limited(t, 8, 2, 0)},
		{name: "straight line at capacity", build: straightLine, cfg: limited(t, 1, 2, 0)},
		{name: "straight line over capacity", build: crowded(straightLine, 2), cfg: limited(t, 2, 2, 0), wantSpill: true},
		{name: "straight line with nothing allocatable", build: straightLine, cfg: limited(t, 0, 2, 0), wantSpill: true},
		{name: "loop across a back edge", build: loopAcrossBackEdge, cfg: limited(t, 8, 2, 0)},
		{name: "loop across a back edge over capacity", build: crowded(loopAcrossBackEdge, 2), cfg: limited(t, 2, 2, 0), wantSpill: true},
		{name: "loop across a back edge spilling everything", build: loopAcrossBackEdge, cfg: limited(t, 0, 2, 0), wantSpill: true},
		{name: "live across an intervening definition", build: liveAcrossIntervening, cfg: limited(t, 8, 1, 0)},
		{name: "live across an intervening definition over capacity", build: crowded(liveAcrossIntervening, 2), cfg: limited(t, 1, 2, 0), wantSpill: true},
		{name: "hole across a block", build: holeAcrossBlock, cfg: limited(t, 8, 1, 0)},
		{name: "hole across a block over capacity", build: crowded(holeAcrossBlock, 2), cfg: limited(t, 1, 2, 0), wantSpill: true},
		{name: "values inside a hole", build: valuesInsideAHole, cfg: limited(t, 1, 2, 0)},
		{name: "values inside a hole over capacity", build: crowded(valuesInsideAHole, 3), cfg: limited(t, 1, 2, 0), wantSpill: true},
		{name: "values inside a hole spilling everything", build: crowded(valuesInsideAHole, 2), cfg: limited(t, 0, 2, 0), wantSpill: true},
		{name: "copy whose source stays live", build: copyThenBothUsed, cfg: limited(t, 8, 1, 0)},
		{name: "copy whose source stays live over capacity", build: crowded(copyThenBothUsed, 2), cfg: limited(t, 1, 2, 0), wantSpill: true},
		{name: "spill slots offset past globals", build: crowded(straightLine, 2), cfg: limited(t, 2, 2, 64), wantSpill: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := tt.build(t)
			allowed := allowedRegisters(b.fn, tt.cfg)
			res := checkMeaningPreserved(t, b.fn, tt.cfg)
			assertWellFormed(t, b.fn, allowed)
			if spilled := len(res.spilled) > 0; spilled != tt.wantSpill {
				t.Errorf("spilled %v, want the case to exercise spilling = %v", res.spilled, tt.wantSpill)
			}
			for v, slot := range res.spilled {
				if slot < tt.cfg.SpillSlotBase || slot >= tt.cfg.SpillSlotBase+res.SpillSlots {
					t.Errorf("%s spilled to slot %d, outside [%d, %d)", v, slot, tt.cfg.SpillSlotBase, tt.cfg.SpillSlotBase+res.SpillSlots)
				}
			}
		})
	}
}

// TestAllocateRegisterPressure pins the cost of each value past the register
// file. The step at the threshold is two rather than one: the first spill also
// gives up the one scratch register a one-value-per-line fixture needs.
func TestAllocateRegisterPressure(t *testing.T) {
	const allocatable, scratch = 4, 2
	const file = allocatable + scratch
	// No line of the fixture names two values, so one register serves every
	// reload and the rest of the scratch set stays allocatable.
	const kept = file - 1
	tests := []struct {
		name       string
		live       int
		wantSpills int
	}{
		{name: "fewer values than registers", live: file - 1, wantSpills: 0},
		{name: "exactly as many values as registers", live: file, wantSpills: 0},
		{name: "one value past the register file", live: file + 1, wantSpills: file + 1 - kept},
		{name: "two values past the register file", live: file + 2, wantSpills: file + 2 - kept},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := pressure(t, tt.live)
			cfg := limited(t, allocatable, scratch, 0)
			res, err := Allocate(b.fn, cfg)
			if err != nil {
				t.Fatalf("Allocate: %v", err)
			}
			if got := len(res.spilled); got != tt.wantSpills {
				t.Errorf("spilled %d of %d values (%v), want %d", got, tt.live, res.spilled, tt.wantSpills)
			}
			// Every value is live at once, so nothing shares a slot.
			if res.SpillSlots != tt.wantSpills {
				t.Errorf("SpillSlots = %d, want %d", res.SpillSlots, tt.wantSpills)
			}
			// Every value is written once and read once, so a spilled one costs a
			// poke and a get db. Counting rather than asking whether any were
			// emitted is what makes a double reload visible.
			if got, want := spillCode(b.fn), 2*tt.wantSpills; got != want {
				t.Errorf("%d spill instructions were emitted for %d spilled values, want %d", got, tt.wantSpills, want)
			}
			// The same reason no two share a slot: no two may share a register.
			seen := make(map[ic10.Register]mir.VirtReg)
			for v, reg := range res.assigned {
				if other, dup := seen[reg]; dup {
					t.Errorf("%s and %s both got %s while both are live", other, v, reg)
				}
				seen[reg] = v
			}
		})
	}
}

// TestDefaultScratch holds the shipped scratch set to what the rest of the
// package assumes: wide enough for the most register sources any instruction
// reads, inside the range a device reference resolves in, a fresh slice per
// call.
func TestDefaultScratch(t *testing.T) {
	scratch := DefaultScratch()

	// Three register sources is what select, clamp, lerp, lbns, sbn and sbs
	// read, the widest selection emits. A wider one is reported as a shortfall
	// rather than miscompiled, which makes the number a size choice.
	if want := 3; len(scratch) != want {
		t.Errorf("DefaultScratch() = %v, want %d registers", scratch, want)
	}
	if distinct := slices.Compact(slices.Sorted(slices.Values(scratch))); len(distinct) != len(scratch) {
		t.Errorf("DefaultScratch() = %v, which names a register twice and so serves fewer operands than it looks", scratch)
	}
	for _, r := range scratch {
		if r >= ic10.NumGeneralRegisters {
			t.Errorf("DefaultScratch() holds %s, outside the r0-r15 a device reference resolves in", r)
		}
	}
	// checkPinned's disjointness argument is that the calling convention passes
	// arguments in r0 upward while scratch takes the top of the file. A set
	// that stopped being the top would leave that argument describing nothing.
	want := []ic10.Register{
		ic10.NumGeneralRegisters - 3, ic10.NumGeneralRegisters - 2, ic10.NumGeneralRegisters - 1,
	}
	if !slices.Equal(slices.Sorted(slices.Values(scratch)), want) {
		t.Errorf("DefaultScratch() = %v, want the top of the general file %v; allocation reaches the highest numbered registers last, and holding back any others would withhold one it would have used first", scratch, want)
	}
	if err := (Config{Scratch: scratch}).validate(); err != nil {
		t.Errorf("the shipped scratch set is not a configuration Allocate accepts: %v", err)
	}

	scratch[0] = ic10.Register(ic10.NumGeneralRegisters - 1)
	if again := DefaultScratch(); slices.Equal(again, scratch) {
		t.Errorf("DefaultScratch() hands out one shared slice, so a caller reordering it changes every later call")
	}
}

// TestAllocateHoldsScratchBackOnlyForASpill pins where the spill threshold
// sits. Scratch exists to reload into, so reserving it unconditionally would
// spill three values early on a function that fits in the file exactly.
func TestAllocateHoldsScratchBackOnlyForASpill(t *testing.T) {
	scratch := DefaultScratch()
	file := ic10.NumRegisters - len(scratch)
	tests := []struct {
		live       int
		wantSpills int
	}{
		{live: 1, wantSpills: 0},
		{live: len(scratch), wantSpills: 0},
		{live: file, wantSpills: 0},
		{live: file + 1, wantSpills: 0},
		{live: ic10.NumRegisters - 1, wantSpills: 0},
		{live: ic10.NumRegisters, wantSpills: 0},
		// The reload costs one register of the scratch set rather than all
		// three: no line of this fixture names two values.
		{live: ic10.NumRegisters + 1, wantSpills: ic10.NumRegisters + 1 - (ic10.NumRegisters - 1)},
	}
	for _, tt := range tests {
		t.Run(source.Plural(tt.live, "live value"), func(t *testing.T) {
			b := pressure(t, tt.live)
			cfg := Config{Scratch: DefaultScratch()}
			allowed := allowedRegisters(b.fn, cfg)
			res := checkMeaningPreserved(t, b.fn, cfg)
			assertWellFormed(t, b.fn, allowed)
			if got := len(res.spilled); got != tt.wantSpills {
				t.Errorf("spilled %d of %d values (%v), want %d", got, tt.live, res.spilled, tt.wantSpills)
			}
			if tt.wantSpills > 0 {
				return
			}
			if got := spillCode(b.fn); got != 0 {
				t.Errorf("%d spill instructions were emitted for a function that fits in the register file", got)
			}
		})
	}
}

// scratchInUse counts the configured scratch registers that ended up holding an
// ordinary value rather than being held back for reloads.
func scratchInUse(res Result, cfg Config) int {
	taken := make(map[ic10.Register]bool, len(res.assigned))
	for _, reg := range res.assigned {
		taken[reg] = true
	}
	inUse := 0
	for _, r := range cfg.Scratch {
		if taken[r] {
			inUse++
		}
	}
	return inUse
}

// TestAllocateHoldsBackOnlyTheScratchAnInstructionNeeds pins the reservation to
// one register per distinct spilled operand one line reads, which does not grow
// with how much the function spilled. Holding back the whole configured set
// instead would spill two more values, each costing a poke and a get db a touch.
func TestAllocateHoldsBackOnlyTheScratchAnInstructionNeeds(t *testing.T) {
	tests := []struct {
		name             string
		build            func(t *testing.T) *builder
		cfg              Config
		wantSpills       int
		wantScratchInUse int
	}{
		{
			// pressure writes and reads each value on its own line, so no
			// instruction ever names two spilled values.
			name:             "one value per line gives up one register",
			build:            func(t *testing.T) *builder { return pressure(t, ic10.NumRegisters+1) },
			cfg:              Config{Scratch: DefaultScratch()},
			wantSpills:       ic10.NumRegisters + 1 - (ic10.NumRegisters - 1),
			wantScratchInUse: len(DefaultScratch()) - 1,
		},
		{
			// The first placement sends one of the two cold values to memory and
			// asks for one register. Holding that one back sends the other after
			// it, and the line that reads both then wants two.
			name:             "a line reading two spilled values gives up two",
			build:            twoSpilledInOneLine,
			cfg:              limited(t, 0, 2, 0),
			wantSpills:       4,
			wantScratchInUse: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := tt.build(t)
			allowed := allowedRegisters(b.fn, tt.cfg)
			res := checkMeaningPreserved(t, b.fn, tt.cfg)
			assertWellFormed(t, b.fn, allowed)

			if got := len(res.spilled); got != tt.wantSpills {
				t.Errorf("spilled %d values (%v), want %d", got, res.spilled, tt.wantSpills)
			}
			if got := scratchInUse(res, tt.cfg); got != tt.wantScratchInUse {
				t.Errorf("%d of the %d scratch registers hold a value, want %d", got, len(tt.cfg.Scratch), tt.wantScratchInUse)
			}
		})
	}
}

// TestScratchDemand covers what one instruction asks of the scratch set. The
// two walks have to agree exactly: an understated demand is planned against a
// prefix that does not hold what it reaches for, and an overstated one takes a
// register off the ordinary values of the whole function.
func TestScratchDemand(t *testing.T) {
	tests := []struct {
		name    string
		build   func(t *testing.T) *builder
		spilled []string
		want    int
	}{
		{
			name: "a function with nothing in memory",
			build: func(t *testing.T) *builder {
				t.Helper()
				b := newBuilder(t, "none")
				blk := b.block("entry")
				b.emit(blk, isa.OpMove, b.v("x"), imm(1))
				b.emit(blk, isa.OpAdd, b.v("y"), b.v("x"), imm(2))
				sink(b, blk, b.v("y"))
				return b
			},
			want: 0,
		},
		{
			// The result goes to memory and every source is in a register, so
			// nothing is reloaded and the store still needs somewhere to be
			// computed before it is poked out.
			name: "a line writing a spilled value and reading none",
			build: func(t *testing.T) *builder {
				t.Helper()
				b := newBuilder(t, "writeonly")
				blk := b.block("entry")
				b.emit(blk, isa.OpMove, b.v("x"), imm(1))
				b.emit(blk, isa.OpAdd, b.v("y"), b.v("x"), imm(2))
				sink(b, blk, b.v("y"))
				return b
			},
			spilled: []string{"y"},
			want:    1,
		},
		{
			// The destination takes back the register the source borrowed, so
			// the two together want one rather than two.
			name: "a line writing a spilled value and reading one",
			build: func(t *testing.T) *builder {
				t.Helper()
				b := newBuilder(t, "both")
				blk := b.block("entry")
				b.emit(blk, isa.OpMove, b.v("x"), imm(1))
				b.emit(blk, isa.OpAdd, b.v("y"), b.v("x"), imm(2))
				sink(b, blk, b.v("y"))
				return b
			},
			spilled: []string{"x", "y"},
			want:    1,
		},
		{
			name: "a line reading two spilled values",
			build: func(t *testing.T) *builder {
				t.Helper()
				b := newBuilder(t, "two")
				blk := b.block("entry")
				b.emit(blk, isa.OpMove, b.v("x"), imm(1))
				b.emit(blk, isa.OpMove, b.v("y"), imm(2))
				b.emit(blk, isa.OpAdd, b.v("z"), b.v("x"), b.v("y"))
				sink(b, blk, b.v("z"))
				return b
			},
			spilled: []string{"x", "y"},
			want:    2,
		},
		{
			// One reload serves both positions, so the same value named twice
			// is one register and not two.
			name: "a line reading one spilled value twice",
			build: func(t *testing.T) *builder {
				t.Helper()
				b := newBuilder(t, "twice")
				blk := b.block("entry")
				b.emit(blk, isa.OpMove, b.v("x"), imm(1))
				b.emit(blk, isa.OpAdd, b.v("z"), b.v("x"), b.v("x"))
				sink(b, blk, b.v("z"))
				return b
			},
			spilled: []string{"x"},
			want:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := tt.build(t)
			inMemory := make(map[mir.VirtReg]bool, len(tt.spilled))
			for _, name := range tt.spilled {
				v, named := b.vr[name]
				if !named {
					t.Fatalf("the function has no value named %s", name)
				}
				inMemory[v] = true
			}
			got, err := scratchDemand(b.fn, func(v mir.VirtReg) bool { return inMemory[v] })
			if err != nil {
				t.Fatalf("scratchDemand: %v", err)
			}
			if got != tt.want {
				t.Errorf("scratchDemand = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestAllocateSpillsADefinitionNothingReads is what makes the spilled
// destination above a case rather than a precaution. A definition with no
// reader is live for one point and touched once, so its own line reads nothing
// out of memory — the one shape whose whole demand comes from the destination.
func TestAllocateSpillsADefinitionNothingReads(t *testing.T) {
	tests := []struct {
		name string
		// third is the last operand of the line defining dead. A register there
		// makes x dearer to spill than dead; a literal ties the two, which is
		// where the comparison decides rather than agreeing with the other side.
		third mir.Operand
	}{
		{name: "the value holding the register is dearer", third: nil},
		{name: "the value holding the register costs the same", third: imm(0)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBuilder(t, "defonly")
			blk := b.block("entry")
			third := tt.third
			if third == nil {
				third = b.v("x")
			}
			b.emit(blk, isa.OpMove, b.v("x"), imm(1))
			b.emit(blk, isa.OpSelect, b.v("dead"), b.v("x"), b.v("x"), third)
			sink(b, blk, b.v("x"))

			cfg := limited(t, 0, 1, 0)
			intervals, constrained, err := buildIntervals(b.fn, mustNumber(t, b.fn))
			if err != nil {
				t.Fatalf("buildIntervals: %v", err)
			}
			_, spilled, err := scan(intervals, constrained, cfg.allocatable(nil), cfg)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if len(spilled) != 1 || spilled[0].vreg != b.vr["dead"] {
				t.Fatalf("the placement sent %v to memory, want dead alone", spilled)
			}
			if got, err := scratchDemand(b.fn, inMemory(spilled)); err != nil || got != 1 {
				t.Fatalf("scratchDemand = %d, %v, want 1 and no error: the definition's own line reads nothing out of memory", got, err)
			}

			allowed := allowedRegisters(b.fn, cfg)
			res, err := Allocate(b.fn, cfg)
			if err != nil {
				t.Fatalf("Allocate: %v", err)
			}
			assertWellFormed(t, b.fn, allowed)
			if _, inMemory := res.spilled[b.vr["dead"]]; !inMemory {
				t.Errorf("dead was given a register, want the slot the placement chose for it")
			}
		})
	}
}

func mustNumber(t *testing.T, fn *mir.Func) numbering {
	t.Helper()
	nums, err := number(fn)
	if err != nil {
		t.Fatalf("number: %v", err)
	}
	return nums
}

// TestAllocateDoesNotShareARegisterAcrossInterference guards the case a
// coalescing allocator gets wrong: a copy whose source is read again keeps both
// values alive, so they cannot share.
func TestAllocateDoesNotShareARegisterAcrossInterference(t *testing.T) {
	b := copyThenBothUsed(t)
	res, err := Allocate(b.fn, limited(t, 8, 1, 0))
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	src, ok := res.assigned[b.vr["src"]]
	if !ok {
		t.Fatalf("src was spilled, want a register")
	}
	copied, ok := res.assigned[b.vr["copy"]]
	if !ok {
		t.Fatalf("copy was spilled, want a register")
	}
	if src == copied {
		t.Errorf("src and copy both got %s, but both are live at the add", src)
	}
}

// TestAllocateSpillSlotReuse covers what keeps the boundary between the data
// region and the call frames low. held is there only to put the function over
// the file, so that x and y reach memory at all.
func TestAllocateSpillSlotReuse(t *testing.T) {
	tests := []struct {
		name       string
		overlap    bool
		wantSlots  int
		wantShared bool
	}{
		{name: "disjoint lifetimes share a slot", overlap: false, wantSlots: 2, wantShared: true},
		{name: "overlapping lifetimes need their own", overlap: true, wantSlots: 3, wantShared: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBuilder(t, "slots")
			blk := b.block("entry")
			b.emit(blk, isa.OpMove, b.v("held"), imm(0))
			if tt.overlap {
				b.emit(blk, isa.OpMove, b.v("x"), imm(1))
				b.emit(blk, isa.OpMove, b.v("y"), imm(2))
				sink(b, blk, b.v("x"))
				sink(b, blk, b.v("y"))
			} else {
				b.emit(blk, isa.OpMove, b.v("x"), imm(1))
				sink(b, blk, b.v("x"))
				b.emit(blk, isa.OpMove, b.v("y"), imm(2))
				sink(b, blk, b.v("y"))
			}
			sink(b, blk, b.v("held"))

			cfg := limited(t, 0, 1, 12)
			allowed := allowedRegisters(b.fn, cfg)
			res := checkMeaningPreserved(t, b.fn, cfg)
			assertWellFormed(t, b.fn, allowed)
			if res.SpillSlots != tt.wantSlots {
				t.Errorf("SpillSlots = %d, want %d (assignments %v)", res.SpillSlots, tt.wantSlots, res.spilled)
			}
			if got := res.spilled[b.vr["held"]]; got != 12 {
				t.Errorf("held spilled to slot %d, want the base slot 12", got)
			}
			if shared := res.spilled[b.vr["x"]] == res.spilled[b.vr["y"]]; shared != tt.wantShared {
				t.Errorf("x is at slot %d and y at %d, want them shared = %v", res.spilled[b.vr["x"]], res.spilled[b.vr["y"]], tt.wantShared)
			}
		})
	}
}

// TestAssignSlotsSeparatesOverlappingValuesInAnyOrder holds slot assignment to
// its invariant without borrowing the caller's ordering. The walk prunes an
// occupant once its interval ends, which is exact only while the points arrive
// in non-decreasing order and drops a live one as soon as they do not.
func TestAssignSlotsSeparatesOverlappingValuesInAnyOrder(t *testing.T) {
	const base = 7
	// early and late are disjoint, so they may share; middle overlaps early
	// and must not.
	early := &interval{vreg: mir.VirtReg{ID: 1}, ranges: []liveRange{{from: 0, to: 10}}}
	late := &interval{vreg: mir.VirtReg{ID: 2}, ranges: []liveRange{{from: 20, to: 30}}}
	middle := &interval{vreg: mir.VirtReg{ID: 3}, ranges: []liveRange{{from: 5, to: 15}}}

	tests := []struct {
		name    string
		spilled []*interval
	}{
		{name: "ordered by first live point", spilled: []*interval{early, middle, late}},
		{name: "the overlapping value arrives last", spilled: []*interval{early, late, middle}},
		{name: "reversed", spilled: []*interval{late, middle, early}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slots, count := assignSlots(tt.spilled, base)
			if len(slots) != len(tt.spilled) {
				t.Fatalf("assignSlots placed %d of %d intervals", len(slots), len(tt.spilled))
			}
			for i, a := range tt.spilled {
				if slot := slots[a.vreg]; slot < base || slot-base >= count {
					t.Errorf("%s holds slot %d, outside the %d slots from %d the count reserves", a.vreg, slot, count, base)
				}
				for _, b := range tt.spilled[i+1:] {
					if a.intersects(b) && slots[a.vreg] == slots[b.vreg] {
						t.Errorf("%s and %s are live at once and both hold slot %d", a.vreg, b.vreg, slots[a.vreg])
					}
				}
			}
			if want := 2; count != want {
				t.Errorf("the assignment reserved %d slots, want %d", count, want)
			}
		})
	}
}

// TestAllocateSlotsOfSuccessiveFunctionsAreDisjoint holds Result.SpillSlots to
// what a driver allocating a whole program reads it for. Allocation is per
// function, so only stacking each base on the last one's count keeps a callee
// out of its caller's slots — which is what [clobbered] assumes of them.
func TestAllocateSlotsOfSuccessiveFunctionsAreDisjoint(t *testing.T) {
	const base = 12
	first, second := crowd(t, straightLine(t), 2), crowd(t, pressure(t, 4), 2)

	firstRes, err := Allocate(first.fn, limited(t, 1, 2, base))
	if err != nil {
		t.Fatalf("Allocate the first function: %v", err)
	}
	secondRes, err := Allocate(second.fn, limited(t, 1, 2, base+firstRes.SpillSlots))
	if err != nil {
		t.Fatalf("Allocate the second function: %v", err)
	}
	if firstRes.SpillSlots == 0 || secondRes.SpillSlots == 0 {
		t.Fatalf("the functions took %d and %d slots, so the case proves nothing", firstRes.SpillSlots, secondRes.SpillSlots)
	}

	taken := make(map[int]string, len(firstRes.spilled))
	for v, slot := range firstRes.spilled {
		taken[slot] = v.String()
	}
	for v, slot := range secondRes.spilled {
		if other, clash := taken[slot]; clash {
			t.Errorf("%s of the second function and %s of the first both hold slot %d", v, other, slot)
		}
		if slot < base+firstRes.SpillSlots {
			t.Errorf("%s of the second function holds slot %d, below the %d its base was set to", v, slot, base+firstRes.SpillSlots)
		}
	}
}

// TestAllocateSpillsLongColdOverShortHot pins the heuristic. The value read on
// every line stays in a register and the one read once at the far end goes to
// memory: a spill costs an instruction per touch and buys back a whole span.
func TestAllocateSpillsLongColdOverShortHot(t *testing.T) {
	b := newBuilder(t, "heuristic")
	blk := b.block("entry")
	// Three, so that what is left of the file once the hot value and the two
	// around it have one cannot hold a cold value either, and the ranking rather
	// than the count decides which go.
	colds := []string{"cold0", "cold1", "cold2"}
	for i, name := range colds {
		b.emit(blk, isa.OpMove, b.v(name), imm(float64(i)))
	}
	b.emit(blk, isa.OpMove, b.v("hot"), imm(2))
	for range 3 {
		b.emit(blk, isa.OpAdd, b.v("hot"), b.v("hot"), b.v("hot"))
	}
	b.emit(blk, isa.OpMove, b.v("late"), imm(3))
	b.emit(blk, isa.OpAdd, b.v("late"), b.v("late"), b.v("hot"))
	b.emit(blk, isa.OpMove, b.v("sum"), b.v("late"))
	for _, name := range colds {
		b.emit(blk, isa.OpAdd, b.v("sum"), b.v("sum"), b.v(name))
	}
	sink(b, blk, b.v("sum"))

	cfg := limited(t, 1, 2, 0)
	allowed := allowedRegisters(b.fn, cfg)
	res := checkMeaningPreserved(t, b.fn, cfg)
	assertWellFormed(t, b.fn, allowed)

	for _, name := range colds {
		if _, spilled := res.spilled[b.vr[name]]; !spilled {
			t.Errorf("%s stayed in %s, want it spilled as a long cold interval", name, res.assigned[b.vr[name]])
		}
	}
	for _, name := range []string{"hot", "late"} {
		if slot, spilled := res.spilled[b.vr[name]]; spilled {
			t.Errorf("%s was spilled to slot %d, want it kept in a register", name, slot)
		}
	}
}

// TestSpillScoreWeighsTouchesAgainstSpan pins the ranking rather than the one
// placement above. A ranking reading the cost alone agrees with that placement,
// where both candidates live the same stretch, and sends the wrong one to
// memory as soon as they do not.
func TestSpillScoreWeighsTouchesAgainstSpan(t *testing.T) {
	iv := func(touches int, ranges ...liveRange) *interval {
		return &interval{vreg: mir.VirtReg{ID: 1}, ranges: ranges, touches: touches}
	}
	tests := []struct {
		name            string
		cheaper, dearer *interval
	}{
		{
			name:    "a long cold interval beats a short hot one it is touched more than",
			cheaper: iv(4, liveRange{from: 0, to: 40}),
			dearer:  iv(2, liveRange{from: 0, to: 2}),
		},
		{
			name:    "over the same span the fewer touches are cheaper",
			cheaper: iv(2, liveRange{from: 0, to: 10}),
			dearer:  iv(4, liveRange{from: 0, to: 10}),
		},
		{
			name:    "for the same touches the longer span is cheaper",
			cheaper: iv(2, liveRange{from: 0, to: 20}),
			dearer:  iv(2, liveRange{from: 0, to: 4}),
		},
		{
			// A hole holds no register, so it buys nothing back. Measuring the
			// distance from the first point to the last instead would rank the
			// value with the hole as the wider of the two.
			name:    "a hole in the interval is not span",
			cheaper: iv(2, liveRange{from: 0, to: 10}),
			dearer:  iv(2, liveRange{from: 0, to: 2}, liveRange{from: 30, to: 32}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if cheaper, dearer := spillScore(tt.cheaper), spillScore(tt.dearer); cheaper >= dearer {
				t.Errorf("spillScore ranked the intervals %v and %v, want the first strictly lower", cheaper, dearer)
			}
		})
	}
}

// TestCheapestVictimLeavesATieWhereItIs pins which register an eviction takes
// when two cost the same to free. Config.allocatable hands them out
// cheapest-to-render first, so taking the later candidate spends bytes against
// the 4096 byte budget for nothing.
func TestCheapestVictimLeavesATieWhereItIs(t *testing.T) {
	tied := func(id uint32) *interval {
		return &interval{vreg: mir.VirtReg{ID: id}, ranges: []liveRange{{from: 0, to: 10}}, touches: 5}
	}
	candidates := []ic10.Register{3, 7, 9}
	occupied := map[ic10.Register][]*interval{3: {tied(1)}, 7: {tied(2)}, 9: {tied(3)}}

	reg, score, ok := cheapestVictim(candidates, occupied)
	if !ok {
		t.Fatal("cheapestVictim found no occupied register among three")
	}
	if reg != candidates[0] {
		t.Errorf("the eviction took %s, want %s: equally priced registers leave the earliest candidate in place", reg, candidates[0])
	}
	if want := spillScore(tied(1)); score != want {
		t.Errorf("the victim scored %v, want %v", score, want)
	}
}

// TestAllocateRespectsReservation covers what Config.Reserved withholds. The
// scratch set is not part of it: a function that spills nothing is placed in
// the whole file, so scratch appears in the first pass's candidates.
func TestAllocateRespectsReservation(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		wantFree []ic10.Register
	}{
		{
			name:     "calling convention in use keeps sp and ra",
			cfg:      Config{Reserved: []ic10.Register{ic10.RegSP, ic10.RegRA}, Scratch: []ic10.Register{0, 1}},
			wantFree: []ic10.Register{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
		},
		{
			name:     "no calling convention frees sp and ra",
			cfg:      Config{Scratch: []ic10.Register{0}},
			wantFree: []ic10.Register{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, ic10.RegSP, ic10.RegRA},
		},
		{
			name:     "an arbitrary reservation set",
			cfg:      Config{Reserved: []ic10.Register{3, 4, 5, ic10.RegRA}, Scratch: []ic10.Register{6}},
			wantFree: []ic10.Register{0, 1, 2, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, ic10.RegSP},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			free := tt.cfg.allocatable(nil)
			sorted := slices.Clone(free)
			slices.Sort(sorted)
			if !slices.Equal(sorted, tt.wantFree) {
				t.Fatalf("allocatable = %v, want %v", sorted, tt.wantFree)
			}
			// Byte budget: a register whose name renders in two characters is
			// handed out before one that renders in three.
			for i := 1; i < len(free); i++ {
				if len(free[i-1].String()) > len(free[i].String()) {
					t.Errorf("allocatable order %v puts %s before %s, want the shorter name first", free, free[i-1], free[i])
				}
			}

			b := straightLine(t)
			allowed := allowedRegisters(b.fn, tt.cfg)
			checkMeaningPreserved(t, b.fn, tt.cfg)
			assertWellFormed(t, b.fn, allowed)

			banned := make(map[ic10.Register]bool)
			for _, r := range tt.cfg.Reserved {
				banned[r] = true
			}
			for _, r := range tt.cfg.Scratch {
				banned[r] = true
			}
			for _, instr := range b.fn.AllInstrs() {
				for j, arg := range instr.Args {
					reg, ok := arg.(mir.PhysReg)
					if ok && banned[reg.Reg] && !slices.Contains(tt.cfg.Scratch, reg.Reg) {
						t.Errorf("%s: operand %d is the reserved %s", instr, j, reg.Reg)
					}
				}
			}
		})
	}
}

// TestAllocateReservesPhysicalRegistersAlreadyNamed covers input in mixed form:
// a register selection pinned carries no live range, so the allocator has to
// hold it back for the whole function.
func TestAllocateReservesPhysicalRegistersAlreadyNamed(t *testing.T) {
	b := newBuilder(t, "preassigned")
	blk := b.block("entry")
	b.emit(blk, isa.OpMove, b.v("a"), mir.PhysReg{Reg: 3})
	b.emit(blk, isa.OpMove, b.v("b"), imm(2))
	b.emit(blk, isa.OpAdd, b.v("c"), b.v("a"), b.v("b"))
	sink(b, blk, b.v("c"))

	cfg := Config{Scratch: []ic10.Register{15}}
	res, err := Allocate(b.fn, cfg)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	for v, reg := range res.assigned {
		if reg == 3 {
			t.Errorf("%s was given r3, which the input already names", v)
		}
	}
}

// TestAllocateKeepsDeviceReferencesInRange covers the one range restriction the
// machine imposes: a register holding a device reference resolves within r0
// through r15, so sp and ra are out even when nothing reserves them. No
// selection pattern builds such an operand, so the cases build the IR by hand.
func TestAllocateKeepsDeviceReferencesInRange(t *testing.T) {
	tests := []struct {
		name string
		// use reads the device through the virtual register named pin, which the
		// shared body has already defined. The configuration leaves r5, sp and
		// ra allocatable and hands the shorter names out first, so an
		// unconstrained value in the reference position lands in sp.
		use func(b *builder, blk *mir.Block)
	}{
		{
			name: "a position that also accepts a device pin",
			use: func(b *builder, blk *mir.Block) {
				b.emit(blk, isa.OpL, b.v("read"), b.v("pin"), mir.LogicType{Value: 0})
				sink(b, blk, b.v("read"))
			},
		},
		{
			name: "a reference id position that accepts no device pin",
			use: func(b *builder, blk *mir.Block) {
				b.emit(blk, isa.OpMove, b.v("written"), imm(4))
				b.emit(blk, isa.OpSd, b.v("pin"), mir.LogicType{Value: 0}, b.v("written"))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBuilder(t, "device")
			blk := b.block("entry")
			b.emit(blk, isa.OpMove, b.v("first"), imm(1))
			b.emit(blk, isa.OpMove, b.v("second"), imm(2))
			b.emit(blk, isa.OpMove, b.v("pin"), imm(3))
			tt.use(b, blk)
			sink(b, blk, b.v("first"))
			sink(b, blk, b.v("second"))

			cfg := Config{Scratch: []ic10.Register{0}}
			for r := ic10.Register(1); r < ic10.NumGeneralRegisters; r++ {
				if r != 5 {
					cfg.Reserved = append(cfg.Reserved, r)
				}
			}
			allowed := allowedRegisters(b.fn, cfg)
			res := checkMeaningPreserved(t, b.fn, cfg)
			assertWellFormed(t, b.fn, allowed)

			if reg, ok := res.assigned[b.vr["pin"]]; ok && reg >= ic10.NumGeneralRegisters {
				t.Errorf("pin holds a device reference in %s, which the chip cannot resolve there", reg)
			}
			if len(res.assigned) == 0 {
				t.Fatalf("nothing was assigned a register, so the case proves nothing")
			}
		})
	}
}

// spillCode counts the reloads and spill stores a function holds.
func spillCode(fn *mir.Func) int {
	count := 0
	for _, instr := range fn.AllInstrs() {
		if instr.Op == isa.OpGet || instr.Op == isa.OpPoke {
			count++
		}
	}
	return count
}

// TestAllocateAttributesSpillCode checks that inserted instructions carry the
// position and the inline chain of the instruction they serve. Without the
// position the byte report claims bytes nobody wrote; without the chain it
// charges an inlined body's cost to the function it landed in.
func TestAllocateAttributesSpillCode(t *testing.T) {
	b := straightLine(t)
	site := []source.InlineSite{{Pos: source.Position{File: "t.mc", Line: 4, Column: 2}, Callee: "helper"}}
	original := make(map[*mir.Instr]bool)
	for _, instr := range b.fn.AllInstrs() {
		instr.Inline = site
		original[instr] = true
	}

	if _, err := Allocate(b.fn, limited(t, 0, 2, 0)); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if spillCode(b.fn) == 0 {
		t.Fatalf("no spill code was inserted, so the case proves nothing")
	}

	inserted := 0
	for _, block := range b.fn.Blocks {
		for i, instr := range block.Instrs {
			if original[instr] {
				continue
			}
			inserted++
			if !instr.Pos.IsValid() {
				t.Errorf("%s has no source position", instr)
				continue
			}
			var neighbour *mir.Instr
			// Default is the rule: only a spill and a reload are inserted, and
			// no other opcode has a neighbour to borrow a position from.
			//exhaustive:ignore
			switch instr.Op {
			case isa.OpGet:
				for j := i + 1; j < len(block.Instrs); j++ {
					if original[block.Instrs[j]] {
						neighbour = block.Instrs[j]
						break
					}
				}
			case isa.OpPoke:
				for j := i - 1; j >= 0; j-- {
					if original[block.Instrs[j]] {
						neighbour = block.Instrs[j]
						break
					}
				}
			default:
				t.Errorf("%s was inserted but is neither a reload nor a spill store", instr)
				continue
			}
			if neighbour == nil {
				t.Errorf("%s has no instruction it could be attributed to", instr)
				continue
			}
			if instr.Pos != neighbour.Pos {
				t.Errorf("%s is at %s, want the position of %s at %s", instr, instr.Pos, neighbour, neighbour.Pos)
			}
			if !slices.Equal(instr.Inline, neighbour.Inline) {
				t.Errorf("%s carries the inline chain %v, want %v from %s", instr, instr.Inline, neighbour.Inline, neighbour)
			}
		}
	}
	if inserted == 0 {
		t.Errorf("no instruction was inserted, so the case proves nothing")
	}
}

// TestAllocateReloadsASpilledValueOncePerInstruction is a byte saving: an
// instruction naming the same spilled value twice needs one get db, not two,
// and one scratch register rather than two.
func TestAllocateReloadsASpilledValueOncePerInstruction(t *testing.T) {
	b := newBuilder(t, "doubleuse")
	blk := b.block("entry")
	b.emit(blk, isa.OpMove, b.v("x"), imm(1))
	b.emit(blk, isa.OpAdd, b.v("y"), b.v("x"), b.v("x"))
	sink(b, blk, b.v("y"))
	sink(b, blk, b.v("x"))

	cfg := limited(t, 0, 1, 0)
	allowed := allowedRegisters(b.fn, cfg)
	res := checkMeaningPreserved(t, b.fn, cfg)
	assertWellFormed(t, b.fn, allowed)

	if _, spilled := res.spilled[b.vr["x"]]; !spilled {
		t.Fatalf("x was not spilled, so the case proves nothing")
	}
	reloads := 0
	for _, instr := range b.fn.Blocks[0].Instrs {
		if instr.Op == isa.OpGet {
			reloads++
		}
	}
	// One reload for each of the three instructions that read a spilled value.
	if want := 3; reloads != want {
		t.Errorf("emitted %d reloads, want %d", reloads, want)
	}
}

func TestAllocateErrors(t *testing.T) {
	tests := []struct {
		name        string
		build       func(*testing.T) *builder
		cfg         Config
		wantMention string
	}{
		{
			name:        "scratch outside the indirect range",
			build:       straightLine,
			cfg:         Config{Scratch: []ic10.Register{ic10.RegSP}},
			wantMention: "outside r0-r15",
		},
		{
			name:        "scratch is also reserved",
			build:       straightLine,
			cfg:         Config{Reserved: []ic10.Register{2}, Scratch: []ic10.Register{2}},
			wantMention: "already reserved",
		},
		{
			name:        "scratch is a register the input already names",
			build:       pinnedRegister,
			cfg:         Config{Scratch: []ic10.Register{3}},
			wantMention: "already named by an instruction",
		},
		{
			name:        "reserved register outside the file",
			build:       straightLine,
			cfg:         Config{Reserved: []ic10.Register{ic10.NumRegisters}},
			wantMention: "outside the register file",
		},
		{
			name:        "spill base past the data region",
			build:       straightLine,
			cfg:         Config{Scratch: []ic10.Register{0}, SpillSlotBase: ic10.NumMemorySlots + 1},
			wantMention: "data region",
		},
		{
			name:        "negative spill base",
			build:       straightLine,
			cfg:         Config{Scratch: []ic10.Register{0}, SpillSlotBase: -1},
			wantMention: "data region",
		},
		{
			name:        "spilling needed with no scratch",
			build:       straightLine,
			cfg:         limited(t, 1, 0, 0),
			wantMention: "Config.Scratch is empty",
		},
		{
			name:        "one scratch register for two spilled operands",
			build:       straightLine,
			cfg:         limited(t, 0, 1, 0),
			wantMention: "2 distinct spilled operands and the configuration holds back 1 scratch register",
		},
		{
			name:        "spill slots past the end of memory",
			build:       straightLine,
			cfg:         limited(t, 0, 2, ic10.NumMemorySlots-1),
			wantMention: "past the",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := tt.build(t)
			// The whole rendering rather than the register form: a form that did
			// not move says only that no operand was rewritten, and cannot see an
			// instruction inserted around one.
			before := rendered(b.fn)
			res, err := Allocate(b.fn, tt.cfg)
			if err == nil {
				t.Fatalf("Allocate succeeded with %+v, want a rejection", res)
			}
			if !strings.Contains(err.Error(), tt.wantMention) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantMention)
			}
			if after := rendered(b.fn); !slices.Equal(after, before) {
				t.Errorf("a failed allocation left\n%s\nwant the function it was given\n%s",
					strings.Join(after, "\n"), strings.Join(before, "\n"))
			}
		})
	}
}

// TestAllocateAcceptsAFullDataRegionThatSpillsNothing covers the boundary
// between a data region that is full and one that has overflowed. A program
// taking every slot and spilling nothing runs, so what refuses the function
// that does want a slot has to be the count rather than the base.
func TestAllocateAcceptsAFullDataRegionThatSpillsNothing(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name: "a full region and no spill",
			cfg:  Config{Scratch: []ic10.Register{13, 14, 15}, SpillSlotBase: ic10.NumMemorySlots},
		},
		{
			name:    "a full region and one spilled value",
			cfg:     limited(t, 0, 2, ic10.NumMemorySlots),
			wantErr: "reach past the",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := straightLine(t)
			res, err := Allocate(b.fn, tt.cfg)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Allocate succeeded with %+v, want a rejection", res)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to mention %q", err, tt.wantErr)
				}
				// The programmer wrote the array and the expression, so this
				// reaches the front end's diagnostic output rather than
				// arriving as a failure of the compiler.
				if _, ok := source.DiagnosticsIn(err); !ok {
					t.Errorf("error = %q, want a source diagnostic", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Allocate: %v", err)
			}
			if res.SpillSlots != 0 {
				t.Errorf("allocation took %d spill slots from a full data region, want 0", res.SpillSlots)
			}
		})
	}
}

// TestAllocateReportsAShortfallAgainstASourceLine holds every rejection the
// program can cause to naming a line the programmer can open. Both rows are one
// expression holding more values at once than the file has room for, and a
// diagnostic with no position renders as a bare dash with nowhere to act on it.
func TestAllocateReportsAShortfallAgainstASourceLine(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "one scratch register for two spilled operands", cfg: limited(t, 0, 1, 0)},
		{name: "spill slots past the end of memory", cfg: limited(t, 0, 2, ic10.NumMemorySlots-1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := straightLine(t)
			res, err := Allocate(b.fn, tt.cfg)
			if err == nil {
				t.Fatalf("Allocate succeeded with %+v, want a rejection", res)
			}
			diags, ok := source.DiagnosticsIn(err)
			if !ok {
				t.Fatalf("error = %q, want a source diagnostic", err)
			}
			for _, d := range diags {
				if !d.Pos.IsValid() {
					t.Errorf("the rejection carries no source position: %s", d.Error())
				}
			}
		})
	}
}

func TestAllocateRejectsNilFunc(t *testing.T) {
	if _, err := Allocate(nil, Config{}); err == nil {
		t.Fatalf("Allocate(nil) succeeded, want a rejection")
	}
}

func TestAllocateEmptyFunc(t *testing.T) {
	fn := mir.NewFunc("empty", source.Position{File: "t.mc", Line: 1, Column: 1})
	fn.NewBlock("entry", source.Position{File: "t.mc", Line: 1, Column: 1})
	res, err := Allocate(fn, Config{Scratch: []ic10.Register{0}})
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if res.SpillSlots != 0 || len(res.assigned) != 0 || len(res.spilled) != 0 {
		t.Errorf("Allocate on an empty function = %+v, want nothing allocated", res)
	}
	if form := fn.RegForm(); form != mir.RegFormEmpty {
		t.Errorf("RegForm = %v, want empty", form)
	}
}
