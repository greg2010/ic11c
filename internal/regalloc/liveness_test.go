package regalloc

import (
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/mir"
)

// TestDefIndex pins which operand each emitted instruction writes. The store
// forms are why the answer has to come from the target's table: they spell
// their source exactly like a destination, and nothing in the spelling of an
// operand separates the two.
func TestDefIndex(t *testing.T) {
	tests := []struct {
		name string
		op   ic10.Opcode
		args []mir.Operand
		want int
	}{
		{name: "move writes its first operand", op: isa.OpMove, args: []mir.Operand{mir.VirtReg{ID: 0}, imm(1)}, want: 0},
		{name: "add writes its first operand", op: isa.OpAdd, args: []mir.Operand{mir.VirtReg{ID: 0}, imm(1), imm(2)}, want: 0},
		{name: "l writes its first operand", op: isa.OpL, args: []mir.Operand{mir.VirtReg{ID: 0}, mir.NewDeviceBase(), mir.LogicType{}}, want: 0},
		{name: "get writes its first operand", op: isa.OpGet, args: []mir.Operand{mir.VirtReg{ID: 0}, mir.NewDeviceBase(), imm(4)}, want: 0},
		{name: "pop writes its first operand", op: isa.OpPop, args: []mir.Operand{mir.VirtReg{ID: 0}}, want: 0},
		{name: "peek writes its first operand", op: isa.OpPeek, args: []mir.Operand{mir.VirtReg{ID: 0}}, want: 0},
		{name: "rand writes its first operand", op: isa.OpRand, args: []mir.Operand{mir.VirtReg{ID: 0}}, want: 0},
		{name: "s reads its last operand", op: isa.OpS, args: []mir.Operand{mir.NewDeviceBase(), mir.LogicType{}, mir.VirtReg{ID: 0}}, want: -1},
		{name: "ss reads its last operand", op: isa.OpSs, args: []mir.Operand{mir.NewDeviceBase(), imm(0), mir.LogicSlotType{}, mir.VirtReg{ID: 0}}, want: -1},
		{name: "sd reads its last operand", op: isa.OpSd, args: []mir.Operand{mir.VirtReg{ID: 0}, mir.LogicType{}, mir.VirtReg{ID: 1}}, want: -1},
		{name: "poke reads both operands", op: isa.OpPoke, args: []mir.Operand{imm(0), mir.VirtReg{ID: 0}}, want: -1},
		{name: "push reads its operand", op: isa.OpPush, args: []mir.Operand{mir.VirtReg{ID: 0}}, want: -1},
		{name: "beq reads every operand", op: isa.OpBeq, args: []mir.Operand{mir.VirtReg{ID: 0}, mir.VirtReg{ID: 1}, mir.Label{Name: "x"}}, want: -1},
		{name: "j reads its operand", op: isa.OpJ, args: []mir.Operand{mir.Label{Name: "x"}}, want: -1},
		{name: "yield has no operands", op: isa.OpYield, want: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBuilder(t, "def")
			instr, err := mir.NewInstr(tt.op, b.pos(), tt.args...)
			if err != nil {
				t.Fatalf("NewInstr(%v): %v", tt.op, err)
			}
			got, err := defIndex(instr)
			if err != nil {
				t.Fatalf("defIndex(%s): %v", instr, err)
			}
			if got != tt.want {
				t.Errorf("defIndex(%s) = %d, want %d", instr, got, tt.want)
			}
		})
	}
}

// TestDeviceConstrained pins which operand positions restrict the register a
// value may be given. Both kinds are asked because neither implies the other:
// sd names a reference id and admits no device pin, rmap the reverse, so a
// check reading one kind alone answers one of those the way it answers an add.
func TestDeviceConstrained(t *testing.T) {
	tests := []struct {
		name  string
		op    ic10.Opcode
		args  []mir.Operand
		index int
		want  bool
	}{
		{name: "l device position", op: isa.OpL, args: []mir.Operand{mir.VirtReg{ID: 0}, mir.VirtReg{ID: 1}, mir.LogicType{}}, index: 1, want: true},
		{name: "l destination", op: isa.OpL, args: []mir.Operand{mir.VirtReg{ID: 0}, mir.VirtReg{ID: 1}, mir.LogicType{}}, index: 0, want: false},
		{name: "sd reference id position", op: isa.OpSd, args: []mir.Operand{mir.VirtReg{ID: 0}, mir.LogicType{}, mir.VirtReg{ID: 1}}, index: 0, want: true},
		{name: "rmap device position accepting no reference id", op: isa.OpRmap, args: []mir.Operand{mir.VirtReg{ID: 0}, mir.NewDeviceBase(), imm(1)}, index: 1, want: true},
		{name: "add operand", op: isa.OpAdd, args: []mir.Operand{mir.VirtReg{ID: 0}, mir.VirtReg{ID: 1}, imm(1)}, index: 1, want: false},
		{name: "index past the operand list", op: isa.OpAdd, args: []mir.Operand{mir.VirtReg{ID: 0}, mir.VirtReg{ID: 1}, imm(1)}, index: 9, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBuilder(t, "constrained")
			instr, err := mir.NewInstr(tt.op, b.pos(), tt.args...)
			if err != nil {
				t.Fatalf("NewInstr(%v): %v", tt.op, err)
			}
			if got := deviceConstrained(instr, tt.index); got != tt.want {
				t.Errorf("deviceConstrained(%s, %d) = %v, want %v", instr, tt.index, got, tt.want)
			}
		})
	}
}

func liveSet(t *testing.T, b *builder, set map[mir.VirtReg]bool) []string {
	t.Helper()
	names := make([]string, 0, len(set))
	for v := range set {
		for name, vr := range b.vr {
			if vr == v {
				names = append(names, name)
			}
		}
	}
	slices.Sort(names)
	return names
}

// TestLivenessAcrossBackEdge is the case a single pass over layout order gets
// wrong: a value read at the top of every iteration is live out of the latch,
// which only a fixed point reports.
func TestLivenessAcrossBackEdge(t *testing.T) {
	b := loopAcrossBackEdge(t)
	live, err := computeLiveness(b.fn)
	if err != nil {
		t.Fatalf("computeLiveness: %v", err)
	}

	tests := []struct {
		name  string
		block int
		set   func(int) map[mir.VirtReg]bool
		want  []string
	}{
		{name: "entry live in", block: 0, set: func(i int) map[mir.VirtReg]bool { return live.in[i] }, want: nil},
		{name: "entry live out", block: 0, set: func(i int) map[mir.VirtReg]bool { return live.out[i] }, want: []string{"acc", "i", "limit"}},
		{name: "body live in", block: 1, set: func(i int) map[mir.VirtReg]bool { return live.in[i] }, want: []string{"acc", "i", "limit"}},
		{name: "body live out", block: 1, set: func(i int) map[mir.VirtReg]bool { return live.out[i] }, want: []string{"acc", "i", "limit"}},
		{name: "done live in", block: 2, set: func(i int) map[mir.VirtReg]bool { return live.in[i] }, want: []string{"acc"}},
		{name: "done live out", block: 2, set: func(i int) map[mir.VirtReg]bool { return live.out[i] }, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := liveSet(t, b, tt.set(tt.block))
			if !slices.Equal(got, tt.want) {
				t.Errorf("%s = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func intervalOf(t *testing.T, intervals []*interval, v mir.VirtReg) *interval {
	t.Helper()
	for _, iv := range intervals {
		if iv.vreg == v {
			return iv
		}
	}
	t.Fatalf("no interval for %s", v)
	return nil
}

// TestBuildIntervalsKeepsHoles is the interval-hole policy stated as a test.
// Widening the value to cover the block it is dead in would make it interfere
// with everything defined there for no reason.
func TestBuildIntervalsKeepsHoles(t *testing.T) {
	b := holeAcrossBlock(t)
	nums, err := number(b.fn)
	if err != nil {
		t.Fatalf("number: %v", err)
	}
	intervals, _, err := buildIntervals(b.fn, nums)
	if err != nil {
		t.Fatalf("buildIntervals: %v", err)
	}

	held := intervalOf(t, intervals, b.vr["held"])
	if len(held.ranges) != 2 {
		t.Fatalf("held has ranges %v, want two with a hole over the skip block", held.ranges)
	}
	// The skip block occupies instruction indices 3 and 4.
	for _, point := range []int{usePoint(3), defPoint(3), usePoint(4), defPoint(4)} {
		if held.covers(point) {
			t.Errorf("held covers point %d, which is inside the block it is dead in", point)
		}
	}
	if !held.covers(defPoint(0)) {
		t.Errorf("held does not cover its own definition at %d", defPoint(0))
	}
	if !held.covers(usePoint(5)) {
		t.Errorf("held does not cover its use at %d", usePoint(5))
	}
	if got, want := held.touches, 2; got != want {
		t.Errorf("held touches = %d, want %d", got, want)
	}
}

// TestBuildIntervalsSplitsAtADeadDefinition covers a non-SSA input: a value
// written twice with nothing reading the first result holds no register in
// between.
func TestBuildIntervalsSplitsAtADeadDefinition(t *testing.T) {
	b := newBuilder(t, "deaddef")
	blk := b.block("entry")
	b.emit(blk, isa.OpMove, b.v("x"), imm(1))
	b.emit(blk, isa.OpMove, b.v("filler"), imm(2))
	sink(b, blk, b.v("filler"))
	b.emit(blk, isa.OpMove, b.v("x"), imm(3))
	sink(b, blk, b.v("x"))

	nums, err := number(b.fn)
	if err != nil {
		t.Fatalf("number: %v", err)
	}
	intervals, _, err := buildIntervals(b.fn, nums)
	if err != nil {
		t.Fatalf("buildIntervals: %v", err)
	}

	x := intervalOf(t, intervals, b.vr["x"])
	if len(x.ranges) != 2 {
		t.Fatalf("x has ranges %v, want the dead first definition split off", x.ranges)
	}
	if x.covers(defPoint(1)) || x.covers(defPoint(2)) {
		t.Errorf("x with ranges %v holds a register while its first value is dead", x.ranges)
	}
	if got, want := x.touches, 3; got != want {
		t.Errorf("x touches = %d, want %d", got, want)
	}
}

// TestBuildIntervalsSharesARegisterWithinAHole is the payoff for keeping holes:
// three values fit in one register only because two live entirely inside the
// third's hole. Filling it would make them interfere and the function spill.
func TestBuildIntervalsSharesARegisterWithinAHole(t *testing.T) {
	b := valuesInsideAHole(t)
	cfg := limited(t, 1, 1, 0)
	allowed := allowedRegisters(b.fn, cfg)
	res := checkMeaningPreserved(t, b.fn, cfg)
	assertWellFormed(t, b.fn, allowed)

	if len(res.spilled) != 0 {
		t.Errorf("spilled %v, want one register to suffice once the hole is kept", res.spilled)
	}
	for _, name := range []string{"held", "tmp", "out"} {
		if reg, ok := res.assigned[b.vr[name]]; !ok || reg != 0 {
			t.Errorf("%s got %v (assigned %v), want r0", name, reg, ok)
		}
	}
}

// TestAllocateRejectsMalformedInput covers the shapes mir.Program.Validate
// would also reject: this pass runs over whatever selection handed it, and
// reading direction off an unknown opcode is not possible.
func TestAllocateRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name        string
		build       func(*testing.T) *mir.Func
		wantMention string
	}{
		{
			name: "opcode outside the instruction table",
			build: func(t *testing.T) *mir.Func {
				t.Helper()
				b := newBuilder(t, "unknown")
				blk := b.block("entry")
				blk.Append(&mir.Instr{Op: ic10.Opcode(len(ic10.Instructions) + 100), Args: []mir.Operand{b.v("x")}, Pos: b.pos()})
				return b.fn
			},
			wantMention: "not in the instruction table",
		},
		{
			// NaN materialisation runs between Validate and here and rebuilds
			// the instruction list of every block holding one, so the list
			// allocation walks is not the one validation saw.
			name: "nil instruction",
			build: func(t *testing.T) *mir.Func {
				t.Helper()
				b := newBuilder(t, "nilinstr")
				blk := b.block("entry")
				b.emit(blk, isa.OpMove, b.v("x"), imm(1))
				blk.Append(nil)
				return b.fn
			},
			wantMention: "nil instruction",
		},
		{
			name: "successor outside the function",
			build: func(t *testing.T) *mir.Func {
				t.Helper()
				b := newBuilder(t, "stray")
				blk := b.block("entry")
				b.emit(blk, isa.OpMove, b.v("x"), imm(1))
				other := newBuilder(t, "other")
				blk.AddSucc(other.block("elsewhere"))
				return b.fn
			},
			wantMention: "outside the function",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := tt.build(t)
			if _, err := Allocate(fn, Config{Scratch: []ic10.Register{0}}); err == nil {
				t.Fatalf("Allocate succeeded, want a rejection")
			} else if !strings.Contains(err.Error(), tt.wantMention) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantMention)
			}
		})
	}
}

// TestComputeLivenessRejectsANilInstruction covers the pass directly: Allocate
// is not the only way in, and a nil reaching the operand walk panics.
func TestComputeLivenessRejectsANilInstruction(t *testing.T) {
	b := newBuilder(t, "nilinstr")
	blk := b.block("entry")
	blk.Append(nil)
	if _, err := computeLiveness(b.fn); err == nil {
		t.Fatalf("computeLiveness succeeded on a block holding a nil instruction, want a rejection")
	}
}

func TestNumberRejectsNilBlock(t *testing.T) {
	b := newBuilder(t, "nilblock")
	b.fn.Blocks = append(b.fn.Blocks, nil)
	if _, err := number(b.fn); err == nil {
		t.Fatalf("number succeeded on a function with a nil block, want a rejection")
	}
}
