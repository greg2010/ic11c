package isel

import (
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/source"
	"tinygo.org/x/go-llvm"
)

// TestPhiBecomesCopiesOnTheEdge covers the ordinary case, where each
// predecessor has one exit and the copies can be appended to it.
func TestPhiBecomesCopiesOnTheEdge(t *testing.T) {
	bd := newBuilder(t)
	left, right, end := bd.block("left"), bd.block("right"), bd.block("end")
	x := bd.opaque("x")
	bd.b.CreateCondBr(bd.b.CreateICmp(llvm.IntSGT, x, bd.konst(0), "cmp"), left, right)

	bd.b.SetInsertPointAtEnd(left)
	bd.b.CreateBr(end)
	bd.b.SetInsertPointAtEnd(right)
	bd.b.CreateBr(end)

	bd.b.SetInsertPointAtEnd(end)
	phi := bd.b.CreatePHI(bd.i64, "merged")
	phi.AddIncoming([]llvm.Value{bd.konst(1), bd.konst(2)}, []llvm.BasicBlock{left, right})
	bd.keep(phi)
	bd.b.CreateRetVoid()

	result, err := Select(t.Context(), bd.m, Options{File: "test.c"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	fn := result.Program.Funcs[0]
	for _, block := range fn.Blocks {
		if strings.Contains(block.Label, ".to.") {
			t.Errorf("an edge with a single-exit predecessor was split: %v", render(fn))
		}
	}
	got := render(fn)
	if !contains(got, "move vr1 1") || !contains(got, "move vr1 2") {
		t.Errorf("selected %v, want a copy of each incoming value into one register", got)
	}
}

// TestPhiSwapIsSequenced is the case a naive lowering gets wrong. Two phis that
// exchange a pair of registers are a parallel assignment: emitting the copies
// in the order the phis were written leaves both registers holding the same
// value.
func TestPhiSwapIsSequenced(t *testing.T) {
	bd := newBuilder(t)
	head, latch, end := bd.block("head"), bd.block("latch"), bd.block("end")
	bd.b.CreateBr(head)

	bd.b.SetInsertPointAtEnd(head)
	a := bd.b.CreatePHI(bd.i64, "a")
	b := bd.b.CreatePHI(bd.i64, "b")
	bd.b.CreateCondBr(bd.b.CreateICmp(llvm.IntSGT, a, bd.konst(0), "cmp"), latch, end)

	bd.b.SetInsertPointAtEnd(latch)
	bd.b.CreateBr(head)

	// The back edge swaps them, which is what makes ordering load bearing.
	entry := bd.fn.EntryBasicBlock()
	a.AddIncoming([]llvm.Value{bd.konst(1), b}, []llvm.BasicBlock{entry, latch})
	b.AddIncoming([]llvm.Value{bd.konst(2), a}, []llvm.BasicBlock{entry, latch})

	bd.b.SetInsertPointAtEnd(end)
	bd.keep(a)
	bd.keep(b)
	bd.b.CreateRetVoid()

	got := selectFunc(t, bd)

	moves := make([]string, 0, 4)
	for _, line := range got {
		if strings.HasPrefix(line, "move ") {
			moves = append(moves, line)
		}
	}
	// The two entry copies plus the three the swap costs: one into a fresh
	// register, then the two exchanges.
	if len(moves) != 5 {
		t.Fatalf("selected %d copies, want 5 (two on entry and three for the swap): %v", len(moves), got)
	}
	if !swapIsCorrect(moves[2:]) {
		t.Errorf("the swap copies do not exchange the two registers: %v", moves[2:])
	}
}

// swapIsCorrect replays the emitted copies over a model of the register file
// and checks the two values ended up exchanged, which is stronger than
// asserting a particular instruction order.
func swapIsCorrect(moves []string) bool {
	regs := map[string]string{"vr0": "a", "vr1": "b"}
	for _, move := range moves {
		fields := strings.Fields(move)
		if len(fields) != 3 {
			return false
		}
		regs[fields[1]] = regs[fields[2]]
	}
	return regs["vr0"] == "b" && regs["vr1"] == "a"
}

// TestPhiCopyRegistersFollowLayoutOrder pins the emitted program as a function
// of the input alone: allocation orders intervals that start together by
// register number, so cycle-breaking registers handed out in map order assemble
// identical input to different text. Go randomises map iteration per range.
func TestPhiCopyRegistersFollowLayoutOrder(t *testing.T) {
	const runs = 32
	latches := []string{"main.c0", "main.c1", "main.c2", "main.c3"}

	m := parseIR(t, swapCycleIR)
	var want []string
	for run := range runs {
		result, err := Select(t.Context(), m, Options{File: "test.c"})
		if err != nil {
			t.Fatalf("Select: %v\n%s", err, swapCycleIR)
		}
		fn := result.Program.Funcs[0]

		ids := cycleTemporaries(t, fn, latches)
		for i := 1; i < len(ids); i++ {
			if ids[i] <= ids[i-1] {
				t.Fatalf("the cycle-breaking registers for %v are %v; the predecessor laid out first has to take the lower number", latches, ids)
			}
		}

		lines := render(fn)
		if run == 0 {
			want = lines
			continue
		}
		if !slices.Equal(lines, want) {
			t.Fatalf("run %d selected\n%s\nwhere the first run selected\n%s",
				run, strings.Join(lines, "\n"), strings.Join(want, "\n"))
		}
	}
}

// cycleTemporaries reports the register each named block's copy cycle was broken
// through. The first copy in the block is the move into the temporary, since a
// cycle has no destination free to be written first.
func cycleTemporaries(t *testing.T, fn *mir.Func, labels []string) []uint32 {
	t.Helper()
	byLabel := make(map[string]*mir.Block, len(fn.Blocks))
	for _, block := range fn.Blocks {
		byLabel[block.Label] = block
	}
	ids := make([]uint32, 0, len(labels))
	for _, label := range labels {
		block, laid := byLabel[label]
		if !laid || len(block.Instrs) == 0 {
			t.Fatalf("no block %s carries the copies its edge asked for", label)
		}
		first := block.Instrs[0]
		reg, temporary := first.Args[0].(mir.VirtReg)
		if first.Op != isa.OpMove || !temporary {
			t.Fatalf("%s begins with %q, want the move that breaks the copy cycle", label, first)
		}
		ids = append(ids, reg.ID)
	}
	return ids
}

// swapCycleIR is a loop whose two phis exchange their values, re-entered from
// four separate blocks. Every one of the four carries the same pair of copies,
// which is a cycle, so each needs a register of its own to break it.
const swapCycleIR = `define void @main() {
entry:
  %tag = alloca i64
  %out = alloca i64
  br label %loop

loop:
  %a = phi i64 [ 0, %entry ], [ %b, %c0 ], [ %b, %c1 ], [ %b, %c2 ], [ %b, %c3 ]
  %b = phi i64 [ 1, %entry ], [ %a, %c0 ], [ %a, %c1 ], [ %a, %c2 ], [ %a, %c3 ]
  %k = load i64, ptr %tag
  switch i64 %k, label %done [ i64 0, label %c0
                               i64 1, label %c1
                               i64 2, label %c2
                               i64 3, label %c3 ]

c0:
  br label %loop

c1:
  br label %loop

c2:
  br label %loop

c3:
  br label %loop

done:
  store i64 %a, ptr %out
  ret void
}
`

// TestCriticalEdgeIsSplit checks the copies for a phi do not run on the way to
// a successor that did not ask for them.
func TestCriticalEdgeIsSplit(t *testing.T) {
	bd := newBuilder(t)
	merge, other := bd.block("merge"), bd.block("other")
	slot := bd.b.CreateAlloca(bd.i64, "x")
	x := bd.b.CreateLoad(bd.i64, slot, "x")
	bd.b.CreateCondBr(bd.b.CreateICmp(llvm.IntSGT, x, bd.konst(0), "cmp"), merge, other)

	bd.b.SetInsertPointAtEnd(other)
	bd.b.CreateBr(merge)

	bd.b.SetInsertPointAtEnd(merge)
	phi := bd.b.CreatePHI(bd.i64, "merged")
	phi.AddIncoming([]llvm.Value{bd.konst(1), bd.konst(2)},
		[]llvm.BasicBlock{bd.fn.EntryBasicBlock(), other})
	bd.keep(phi)
	bd.b.CreateRetVoid()

	result, err := Select(t.Context(), bd.m, Options{File: "test.c"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	fn := result.Program.Funcs[0]

	var edge *mir.Block
	for _, block := range fn.Blocks {
		if strings.Contains(block.Label, ".to.") {
			edge = block
		}
	}
	if edge == nil {
		t.Fatalf("the critical edge was not split: %v", render(fn))
	}
	if !contains(render(&mir.Func{Blocks: []*mir.Block{edge}}), "move vr1 1") {
		t.Errorf("the edge block does not carry the copy: %v", render(fn))
	}
	// The block that branches must not carry it, or the copy would also run on
	// the way to the other successor.
	if contains(render(&mir.Func{Blocks: []*mir.Block{fn.Blocks[0]}}), "move vr1 1") {
		t.Errorf("the copy stayed in the branching block: %v", render(fn))
	}
}

// TestSequenceOrdersParallelCopies exercises the sequencer directly, where a
// cycle can be built without the surrounding control flow that would produce
// one.
func TestSequenceOrdersParallelCopies(t *testing.T) {
	reg := func(id uint32) mir.VirtReg { return mir.VirtReg{ID: id} }

	cases := []struct {
		name  string
		moves []copyMove
		// want is the final contents of each destination, keyed by the value
		// each register started with.
		want map[uint32]string
	}{
		{
			name:  "independent copies keep their values",
			moves: []copyMove{{dst: reg(2), src: reg(0)}, {dst: reg(3), src: reg(1)}},
			want:  map[uint32]string{2: "v0", 3: "v1"},
		},
		{
			name:  "one source read by two destinations",
			moves: []copyMove{{dst: reg(1), src: reg(0)}, {dst: reg(2), src: reg(0)}},
			want:  map[uint32]string{1: "v0", 2: "v0"},
		},
		{
			name:  "a chain is ordered so the far end is written first",
			moves: []copyMove{{dst: reg(0), src: reg(1)}, {dst: reg(1), src: reg(2)}},
			want:  map[uint32]string{0: "v1", 1: "v2"},
		},
		{
			name:  "a two-element cycle needs a spare register",
			moves: []copyMove{{dst: reg(0), src: reg(1)}, {dst: reg(1), src: reg(0)}},
			want:  map[uint32]string{0: "v1", 1: "v0"},
		},
		{
			name: "a three-element cycle rotates",
			moves: []copyMove{
				{dst: reg(0), src: reg(1)},
				{dst: reg(1), src: reg(2)},
				{dst: reg(2), src: reg(0)},
			},
			want: map[uint32]string{0: "v1", 1: "v2", 2: "v0"},
		},
		{
			name:  "a literal is written after every read of the register it lands in",
			moves: []copyMove{{dst: reg(1), src: reg(0)}, {dst: reg(0), src: mir.Imm{Value: 7}}},
			want:  map[uint32]string{0: "7", 1: "v0"},
		},
		{
			name:  "a copy to itself is dropped",
			moves: []copyMove{{dst: reg(0), src: reg(0)}},
			want:  map[uint32]string{0: "v0"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &selector{fn: mir.NewFunc("test", source.Position{})}
			// Hand out registers past every one the case names, so a temporary
			// is distinguishable from a participant.
			for range 8 {
				s.fn.NewVirtReg()
			}
			got := s.sequence(tc.moves)
			state := replay(got)
			for id, want := range tc.want {
				if state[id] != want {
					t.Errorf("register vr%d holds %q, want %q; copies were %v", id, state[id], want, formatMoves(got))
				}
			}
		})
	}
}

// replay runs the sequenced copies over a model register file, which checks the
// effect rather than a particular instruction order.
func replay(moves []copyMove) map[uint32]string {
	state := make(map[uint32]string)
	value := func(operand mir.Operand) string {
		switch operand := operand.(type) {
		case mir.VirtReg:
			if held, ok := state[operand.ID]; ok {
				return held
			}
			return "spare"
		case mir.Imm:
			return "7"
		default:
			return "?"
		}
	}
	for id := range uint32(8) {
		state[id] = "v" + string(rune('0'+id))
	}
	for _, move := range moves {
		state[move.dst.ID] = value(move.src)
	}
	return state
}

func formatMoves(moves []copyMove) []string {
	out := make([]string, len(moves))
	for i, move := range moves {
		out[i] = move.dst.String() + " <- " + move.src.String()
	}
	return out
}

// TestPhiCopiesOncePerPredecessor covers the phi a switch with two labels on one
// arm produces: LLVM gives it one entry per edge, so a second copy reaches the
// sequencer as a second demand on the destination, which the cycle breaker reads.
func TestPhiCopiesOncePerPredecessor(t *testing.T) {
	m := parseIR(t, `
define void @main() {
entry:
  %slot = alloca i64
  %t = load i64, ptr %slot
  switch i64 %t, label %other [
    i64 1, label %end
    i64 2, label %end
  ]

other:
  br label %end

end:
  %y = phi i64 [ 5, %entry ], [ 5, %entry ], [ 9, %other ]
  store i64 %y, ptr %slot
  ret void
}
`)
	result, err := Select(t.Context(), m, Options{File: "test.c"})
	if err != nil {
		t.Fatalf("Select: %v\n%s", err, m.String())
	}
	lines := render(result.Program.Funcs[0])

	copies := 0
	for _, line := range lines {
		if strings.HasSuffix(line, " 5") && strings.HasPrefix(line, "move ") {
			copies++
		}
	}
	if copies != 1 {
		t.Errorf("the value reaching the merge from the switch was copied %d times, want 1:\n%s",
			copies, strings.Join(lines, "\n"))
	}
}
