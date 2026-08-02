package regalloc

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
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
	b.emit(blk, ic10.OpS, mir.NewDeviceBase(), mir.LogicType{Value: 0}, v)
}

// limited reserves every register outside the first allocatable ones and the
// scratch registers that follow them, so a test can pin the size of the
// register file it allocates against.
//
// The file a function is placed in is allocatable+scratch wide, and narrows to
// allocatable once it spills: scratch is held back only for a function that
// needs somewhere to reload into. A test that wants a spill therefore has to
// exceed allocatable+scratch.
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
			if j < len(info.Operands) && info.Operands[j].Accepts(ic10.OperandDevice) && reg.Reg >= ic10.NumGeneralRegisters {
				t.Errorf("%s: operand %d holds a device reference in %s, which indirect referencing cannot reach", instr, j, reg.Reg)
			}
		}
	}
}

// allowedRegisters is every register the output may name: what the config left
// allocatable, the scratch registers, and whatever the input already used.
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
	b.emit(blk, ic10.OpMove, b.v("a"), imm(1))
	b.emit(blk, ic10.OpMove, b.v("b"), imm(2))
	b.emit(blk, ic10.OpMove, b.v("c"), imm(3))
	b.emit(blk, ic10.OpAdd, b.v("d"), b.v("a"), b.v("b"))
	b.emit(blk, ic10.OpAdd, b.v("d"), b.v("d"), b.v("c"))
	sink(b, blk, b.v("d"))
	return b
}

// pinnedRegister is straightLine with one instruction naming r3 outright, which
// is the shape instruction selection hands over for a call: an argument or a
// result register the input pins and carries no live range for.
func pinnedRegister(t *testing.T) *builder {
	t.Helper()
	b := newBuilder(t, "pinned")
	blk := b.block("entry")
	b.emit(blk, ic10.OpMove, mir.PhysReg{Reg: 3}, imm(1))
	b.emit(blk, ic10.OpMove, b.v("a"), mir.PhysReg{Reg: 3})
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

	b.emit(entry, ic10.OpMove, b.v("i"), imm(0))
	b.emit(entry, ic10.OpMove, b.v("limit"), imm(10))
	b.emit(entry, ic10.OpMove, b.v("acc"), imm(0))
	b.emit(entry, ic10.OpJ, mir.Label{Name: "body"})

	b.emit(body, ic10.OpAdd, b.v("acc"), b.v("acc"), b.v("i"))
	b.emit(body, ic10.OpAdd, b.v("i"), b.v("i"), imm(1))
	b.emit(body, ic10.OpBlt, b.v("i"), b.v("limit"), mir.Label{Name: "body"})

	sink(b, done, b.v("acc"))
	return b
}

// liveAcrossIntervening keeps a value live over a block that redefines a
// different one, which is the shape a naive interval that stops at the last
// textual use gets wrong.
func liveAcrossIntervening(t *testing.T) *builder {
	t.Helper()
	b := newBuilder(t, "intervening")
	entry, mid, exit := b.block("entry"), b.block("mid"), b.block("exit")
	entry.AddSucc(mid)
	mid.AddSucc(exit)

	b.emit(entry, ic10.OpMove, b.v("kept"), imm(1))
	b.emit(entry, ic10.OpMove, b.v("other"), imm(2))
	b.emit(entry, ic10.OpJ, mir.Label{Name: "mid"})

	b.emit(mid, ic10.OpAdd, b.v("other"), b.v("other"), b.v("other"))
	b.emit(mid, ic10.OpJ, mir.Label{Name: "exit"})

	b.emit(exit, ic10.OpAdd, b.v("sum"), b.v("kept"), b.v("other"))
	sink(b, exit, b.v("sum"))
	return b
}

// holeAcrossBlock leaves a value live in the first and third blocks and dead in
// the second, which layout order puts between them.
func holeAcrossBlock(t *testing.T) *builder {
	t.Helper()
	b := newBuilder(t, "hole")
	entry, skip, use, join := b.block("entry"), b.block("skip"), b.block("use"), b.block("join")
	entry.AddSucc(skip)
	entry.AddSucc(use)
	skip.AddSucc(join)
	use.AddSucc(join)

	b.emit(entry, ic10.OpMove, b.v("held"), imm(1))
	b.emit(entry, ic10.OpMove, b.v("out"), imm(0))
	b.emit(entry, ic10.OpBlt, imm(0), imm(1), mir.Label{Name: "use"})

	b.emit(skip, ic10.OpMove, b.v("out"), imm(7))
	b.emit(skip, ic10.OpJ, mir.Label{Name: "join"})

	b.emit(use, ic10.OpAdd, b.v("out"), b.v("held"), imm(2))
	b.emit(use, ic10.OpJ, mir.Label{Name: "join"})

	sink(b, join, b.v("out"))
	return b
}

// valuesInsideAHole puts two whole lifetimes inside another value's hole, so
// that all three share one register.
func valuesInsideAHole(t *testing.T) *builder {
	t.Helper()
	b := newBuilder(t, "insidehole")
	entry, skip, use, join := b.block("entry"), b.block("skip"), b.block("use"), b.block("join")
	entry.AddSucc(skip)
	entry.AddSucc(use)
	skip.AddSucc(join)
	use.AddSucc(join)

	b.emit(entry, ic10.OpMove, b.v("held"), imm(1))
	b.emit(entry, ic10.OpBlt, imm(0), imm(1), mir.Label{Name: "use"})

	b.emit(skip, ic10.OpMove, b.v("tmp"), imm(5))
	b.emit(skip, ic10.OpMove, b.v("out"), b.v("tmp"))
	b.emit(skip, ic10.OpJ, mir.Label{Name: "join"})

	b.emit(use, ic10.OpAdd, b.v("out"), b.v("held"), imm(2))
	b.emit(use, ic10.OpJ, mir.Label{Name: "join"})

	sink(b, join, b.v("out"))
	return b
}

// crowd adds n values live across the whole function, defined ahead of the
// entry block and read at the end of the last.
//
// It is how a small fixture is pushed past its register file. The file cannot
// be narrower than the scratch set, and a fixture holding two or three values
// would otherwise fit in every configuration a spill is expressible in.
func crowd(t *testing.T, b *builder, n int) *builder {
	t.Helper()
	entry, last := b.fn.Blocks[0], b.fn.Blocks[len(b.fn.Blocks)-1]
	defs := make([]*mir.Instr, 0, n)
	for i := range n {
		defs = append(defs, b.instr(ic10.OpMove, b.v("crowd"+strconv.Itoa(i)), imm(float64(i))))
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
		b.emit(blk, ic10.OpMove, b.v("v"+strconv.Itoa(i)), imm(float64(i)))
	}
	for i := range n {
		sink(b, blk, b.v("v"+strconv.Itoa(i)))
	}
	return b
}

func copyThenBothUsed(t *testing.T) *builder {
	t.Helper()
	b := newBuilder(t, "interfere")
	blk := b.block("entry")
	b.emit(blk, ic10.OpMove, b.v("src"), imm(1))
	b.emit(blk, ic10.OpMove, b.v("copy"), b.v("src"))
	b.emit(blk, ic10.OpAdd, b.v("sum"), b.v("src"), b.v("copy"))
	sink(b, blk, b.v("sum"))
	return b
}

// TestAllocatePreservesMeaning is the property the rest of the suite rests on:
// a rewritten function may look well formed and still read the wrong register.
// Each case is checked by comparing, for every use, the set of definitions that
// can reach it before and after allocation.
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
// file. The step at the threshold is scratch+1 rather than one, because the
// first spill is also what gives the scratch set up.
func TestAllocateRegisterPressure(t *testing.T) {
	const allocatable, scratch = 4, 2
	const file = allocatable + scratch
	tests := []struct {
		name       string
		live       int
		wantSpills int
	}{
		{name: "fewer values than registers", live: file - 1, wantSpills: 0},
		{name: "exactly as many values as registers", live: file, wantSpills: 0},
		{name: "one value past the register file", live: file + 1, wantSpills: file + 1 - allocatable},
		{name: "two values past the register file", live: file + 2, wantSpills: file + 2 - allocatable},
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
			if got := spillCode(b.fn); (got > 0) != (tt.wantSpills > 0) {
				t.Errorf("%d spill instructions were emitted with %d spilled registers", got, tt.wantSpills)
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
// package assumes of it: wide enough for the most register sources any
// instruction reads, inside the range a device reference resolves in, and a
// fresh slice per call.
func TestDefaultScratch(t *testing.T) {
	scratch := DefaultScratch()

	// Three register-capable sources is what select, clamp, lerp, lbns, sbn and
	// sbs read, and they are the widest instruction selection emits. A wider one
	// is reported as a shortfall by planInstr rather than miscompiled, which is
	// what makes the number a size choice and not a correctness one.
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
// sits. Scratch exists to reload a spilled operand into, so a function that
// spills nothing needs none of it, and reserving it unconditionally would start
// spilling three values early — each costing a poke and a get db per touch —
// on a function that fits in the file exactly.
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
		// One value past the file spills, and holding scratch back for it
		// costs the three registers scratch occupies as well.
		{live: ic10.NumRegisters + 1, wantSpills: ic10.NumRegisters + 1 - file},
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

// TestAllocateDoesNotShareARegisterAcrossInterference guards the case a
// coalescing allocator gets wrong: a copy whose source is read again afterwards
// keeps both values alive, so they cannot share.
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
// the register file, so that x and y reach memory at all.
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
			b.emit(blk, ic10.OpMove, b.v("held"), imm(0))
			if tt.overlap {
				b.emit(blk, ic10.OpMove, b.v("x"), imm(1))
				b.emit(blk, ic10.OpMove, b.v("y"), imm(2))
				sink(b, blk, b.v("x"))
				sink(b, blk, b.v("y"))
			} else {
				b.emit(blk, ic10.OpMove, b.v("x"), imm(1))
				sink(b, blk, b.v("x"))
				b.emit(blk, ic10.OpMove, b.v("y"), imm(2))
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

// TestAllocateSpillsLongColdOverShortHot pins the heuristic. The value read on
// every line of the loop stays in a register; the one computed early and read
// once at the far end goes to memory, because a spill costs an instruction per
// touch and buys back a register for the whole span.
func TestAllocateSpillsLongColdOverShortHot(t *testing.T) {
	b := newBuilder(t, "heuristic")
	blk := b.block("entry")
	// Three of them, so the function is over the register file by more than the
	// scratch set the first spill also gives up.
	colds := []string{"cold0", "cold1", "cold2"}
	for i, name := range colds {
		b.emit(blk, ic10.OpMove, b.v(name), imm(float64(i)))
	}
	b.emit(blk, ic10.OpMove, b.v("hot"), imm(2))
	for range 3 {
		b.emit(blk, ic10.OpAdd, b.v("hot"), b.v("hot"), b.v("hot"))
	}
	b.emit(blk, ic10.OpMove, b.v("late"), imm(3))
	b.emit(blk, ic10.OpAdd, b.v("late"), b.v("late"), b.v("hot"))
	b.emit(blk, ic10.OpMove, b.v("sum"), b.v("late"))
	for _, name := range colds {
		b.emit(blk, ic10.OpAdd, b.v("sum"), b.v("sum"), b.v(name))
	}
	sink(b, blk, b.v("sum"))

	cfg := limited(t, 2, 2, 0)
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
// instruction selection that pinned a register carries no live range for it, so
// the allocator has to hold it back for the whole function.
func TestAllocateReservesPhysicalRegistersAlreadyNamed(t *testing.T) {
	b := newBuilder(t, "preassigned")
	blk := b.block("entry")
	b.emit(blk, ic10.OpMove, b.v("a"), mir.PhysReg{Reg: 3})
	b.emit(blk, ic10.OpMove, b.v("b"), imm(2))
	b.emit(blk, ic10.OpAdd, b.v("c"), b.v("a"), b.v("b"))
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
// machine imposes on a register: a register holding a device reference resolves
// within r0 through r15, so sp and ra are out even when nothing reserves them.
//
// No selection pattern builds such an operand today — every device operand
// internal/isel emits is a pin or db — so the case assembles the machine IR by
// hand.
func TestAllocateKeepsDeviceReferencesInRange(t *testing.T) {
	b := newBuilder(t, "device")
	blk := b.block("entry")
	b.emit(blk, ic10.OpMove, b.v("first"), imm(1))
	b.emit(blk, ic10.OpMove, b.v("second"), imm(2))
	b.emit(blk, ic10.OpMove, b.v("pin"), imm(3))
	b.emit(blk, ic10.OpL, b.v("read"), b.v("pin"), mir.LogicType{Value: 0})
	sink(b, blk, b.v("read"))
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
}

// spillCode counts the reloads and spill stores a function holds.
func spillCode(fn *mir.Func) int {
	count := 0
	for _, instr := range fn.AllInstrs() {
		if instr.Op == ic10.OpGet || instr.Op == ic10.OpPoke {
			count++
		}
	}
	return count
}

// TestAllocateAttributesSpillCode checks that inserted instructions carry the
// position and the inline chain of the instruction they serve. Either one
// missing makes the byte attribution report charge the bytes to the wrong
// construct: a spill with no position claims bytes nobody wrote, and a spill
// with no chain charges an inlined body's cost to the function it landed in
// rather than to the call that spliced it there.
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
			case ic10.OpGet:
				for j := i + 1; j < len(block.Instrs); j++ {
					if original[block.Instrs[j]] {
						neighbour = block.Instrs[j]
						break
					}
				}
			case ic10.OpPoke:
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
	b.emit(blk, ic10.OpMove, b.v("x"), imm(1))
	b.emit(blk, ic10.OpAdd, b.v("y"), b.v("x"), b.v("x"))
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
		if instr.Op == ic10.OpGet {
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
			cfg:         Config{Scratch: []ic10.Register{0}, SpillSlotBase: ic10.NumMemorySlots},
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
			wantMention: "no scratch register is configured",
		},
		{
			name:        "one scratch register for two spilled operands",
			build:       straightLine,
			cfg:         limited(t, 0, 1, 0),
			wantMention: "only 1 scratch registers are configured",
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
			before := b.fn.RegForm()
			res, err := Allocate(b.fn, tt.cfg)
			if err == nil {
				t.Fatalf("Allocate succeeded with %+v, want a rejection", res)
			}
			if !strings.Contains(err.Error(), tt.wantMention) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantMention)
			}
			if got := b.fn.RegForm(); got != before {
				t.Errorf("RegForm after a failed allocation = %v, want %v: the function must be left alone", got, before)
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
