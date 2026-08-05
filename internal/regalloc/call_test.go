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

// rendered lists a function's instructions in the order the emitter lays them
// out, which is the order a save sequence has to be read in.
func rendered(fn *mir.Func) []string {
	var lines []string
	for _, instr := range fn.AllInstrs() {
		lines = append(lines, instr.String())
	}
	return lines
}

// TestAllocateSavesLiveValuesAroundACall covers the caller's side of the
// convention: what is live across the call is pushed before it and popped after
// it, in mirrored order, and what dies at it costs nothing.
func TestAllocateSavesLiveValuesAroundACall(t *testing.T) {
	cases := []struct {
		name string
		// call is the mnemonic saveSequence splits the block at.
		call      string
		build     func(b *builder, blk *mir.Block)
		wantSaves int
	}{
		{
			name: "a value read after the call is saved",
			call: "jal",
			build: func(b *builder, blk *mir.Block) {
				b.emit(blk, isa.OpMove, b.v("live"), imm(7))
				b.emit(blk, isa.OpJal, mir.Label{Name: "callee"})
				sink(b, blk, b.v("live"))
			},
			wantSaves: 1,
		},
		{
			name: "a value the call is the last reader of is not saved",
			call: "jal",
			build: func(b *builder, blk *mir.Block) {
				b.emit(blk, isa.OpMove, b.v("arg"), imm(7))
				b.emit(blk, isa.OpMove, mir.PhysReg{Reg: 0}, b.v("arg"))
				b.emit(blk, isa.OpJal, mir.Label{Name: "callee"})
			},
			wantSaves: 0,
		},
		{
			name: "a value defined by the call is not saved around it",
			call: "jal",
			build: func(b *builder, blk *mir.Block) {
				b.emit(blk, isa.OpJal, mir.Label{Name: "callee"})
				b.emit(blk, isa.OpMove, b.v("result"), mir.PhysReg{Reg: 0})
				sink(b, blk, b.v("result"))
			},
			wantSaves: 0,
		},
		{
			name: "two values live across the call are both saved",
			call: "jal",
			build: func(b *builder, blk *mir.Block) {
				b.emit(blk, isa.OpMove, b.v("a"), imm(1))
				b.emit(blk, isa.OpMove, b.v("b"), imm(2))
				b.emit(blk, isa.OpJal, mir.Label{Name: "callee"})
				b.emit(blk, isa.OpAdd, b.v("c"), b.v("a"), b.v("b"))
				sink(b, blk, b.v("c"))
			},
			wantSaves: 2,
		},
		{
			name: "a two operand conditional call saves what is live across it",
			call: "bgezal",
			build: func(b *builder, blk *mir.Block) {
				b.emit(blk, isa.OpMove, b.v("live"), imm(7))
				b.emit(blk, isa.OpBgezal, imm(1), mir.Label{Name: "callee"})
				sink(b, blk, b.v("live"))
			},
			wantSaves: 1,
		},
		{
			name: "a conditional call reading a value is still the last reader of it",
			call: "beqal",
			build: func(b *builder, blk *mir.Block) {
				b.emit(blk, isa.OpMove, b.v("arg"), imm(7))
				b.emit(blk, isa.OpBeqal, b.v("arg"), imm(7), mir.Label{Name: "callee"})
			},
			wantSaves: 0,
		},
		{
			name: "a four operand conditional call saves both live values",
			call: "bapal",
			build: func(b *builder, blk *mir.Block) {
				b.emit(blk, isa.OpMove, b.v("a"), imm(1))
				b.emit(blk, isa.OpMove, b.v("b"), imm(2))
				b.emit(blk, isa.OpBapal, imm(1), imm(1), imm(0), mir.Label{Name: "callee"})
				b.emit(blk, isa.OpAdd, b.v("c"), b.v("a"), b.v("b"))
				sink(b, blk, b.v("c"))
			},
			wantSaves: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newBuilder(t, "caller")
			blk := b.block("caller.entry")
			tc.build(b, blk)

			cfg := Config{
				Reserved: []ic10.Register{ic10.RegSP, ic10.RegRA},
				Scratch:  []ic10.Register{13, 14, 15},
			}
			checkMeaningPreserved(t, b.fn, cfg)

			lines := rendered(b.fn)
			pushes, pops := saveSequence(lines, tc.call)
			if len(pushes) != tc.wantSaves {
				t.Errorf("saved %v around the call, want %d registers:\n%s", pushes, tc.wantSaves, strings.Join(lines, "\n"))
			}
			// pop decrements sp before its bounds check and does not roll the
			// decrement back, so a missing pop walks sp rather than answering
			// wrongly.
			if len(pops) != len(pushes) {
				t.Errorf("%d pushes against %d pops leaves sp walking:\n%s", len(pushes), len(pops), strings.Join(lines, "\n"))
			}
			for i, push := range pushes {
				if pop := pops[len(pops)-1-i]; pop != push {
					t.Errorf("push %d saved %s and the mirroring pop restored %s:\n%s", i, push, pop, strings.Join(lines, "\n"))
				}
			}
		})
	}
}

// TestValuesSurviveTheCallsAroundThem covers the shapes the table above leaves
// out, where the saves are not one mirrored run of pushes and pops in a single
// block. Two pushes of the same wrong register are the right count and the
// right pairing, so only [checkMeaningPreserved] can tell these rows apart.
func TestValuesSurviveTheCallsAroundThem(t *testing.T) {
	convention := []ic10.Register{ic10.RegSP, ic10.RegRA}

	tests := []struct {
		name  string
		build func(t *testing.T) *builder
		cfg   Config
	}{
		{
			name: "two calls in sequence over one value",
			build: func(t *testing.T) *builder {
				t.Helper()
				b := newBuilder(t, "twice")
				blk := b.block("twice.entry")
				b.emit(blk, isa.OpMove, b.v("held"), imm(7))
				b.emit(blk, isa.OpJal, mir.Label{Name: "callee"})
				b.emit(blk, isa.OpAdd, b.v("mid"), b.v("held"), imm(1))
				b.emit(blk, isa.OpJal, mir.Label{Name: "callee"})
				b.emit(blk, isa.OpAdd, b.v("out"), b.v("held"), b.v("mid"))
				sink(b, blk, b.v("out"))
				return b
			},
			cfg: Config{Reserved: convention, Scratch: []ic10.Register{13, 14, 15}},
		},
		{
			name: "a call on one arm of a branch that rejoins",
			build: func(t *testing.T) *builder {
				t.Helper()
				b := newBuilder(t, "arm")
				entry, call, skip, join := b.block("arm.entry"), b.block("arm.call"), b.block("arm.skip"), b.block("arm.join")
				entry.AddSucc(call)
				entry.AddSucc(skip)
				call.AddSucc(join)
				skip.AddSucc(join)

				b.emit(entry, isa.OpMove, b.v("held"), imm(3))
				b.emit(entry, isa.OpMove, b.v("other"), imm(4))
				b.emit(entry, isa.OpBlt, imm(0), imm(1), mir.Label{Name: "arm.skip"})

				b.emit(call, isa.OpJal, mir.Label{Name: "callee"})
				b.emit(call, isa.OpJ, mir.Label{Name: "arm.join"})

				b.emit(skip, isa.OpJ, mir.Label{Name: "arm.join"})

				b.emit(join, isa.OpAdd, b.v("out"), b.v("held"), b.v("other"))
				sink(b, join, b.v("out"))
				return b
			},
			cfg: Config{Reserved: convention, Scratch: []ic10.Register{13, 14, 15}},
		},
		{
			name: "a call inside a loop body",
			build: func(t *testing.T) *builder {
				t.Helper()
				b := newBuilder(t, "spin")
				entry, body, done := b.block("spin.entry"), b.block("spin.body"), b.block("spin.done")
				entry.AddSucc(body)
				body.AddSucc(body)
				body.AddSucc(done)

				b.emit(entry, isa.OpMove, b.v("i"), imm(0))
				b.emit(entry, isa.OpMove, b.v("limit"), imm(10))
				b.emit(entry, isa.OpMove, b.v("acc"), imm(0))
				b.emit(entry, isa.OpJ, mir.Label{Name: "spin.body"})

				b.emit(body, isa.OpJal, mir.Label{Name: "callee"})
				b.emit(body, isa.OpAdd, b.v("acc"), b.v("acc"), b.v("i"))
				b.emit(body, isa.OpAdd, b.v("i"), b.v("i"), imm(1))
				b.emit(body, isa.OpBlt, b.v("i"), b.v("limit"), mir.Label{Name: "spin.body"})

				sink(b, done, b.v("acc"))
				return b
			},
			cfg: Config{Reserved: convention, Scratch: []ic10.Register{13, 14, 15}},
		},
		{
			name: "a call in a function narrow enough to spill",
			build: func(t *testing.T) *builder {
				t.Helper()
				b := newBuilder(t, "narrow")
				blk := b.block("narrow.entry")
				for i := range 4 {
					b.emit(blk, isa.OpMove, b.v("v"+strconv.Itoa(i)), imm(float64(i)))
				}
				b.emit(blk, isa.OpJal, mir.Label{Name: "callee"})
				b.emit(blk, isa.OpAdd, b.v("s0"), b.v("v0"), b.v("v1"))
				b.emit(blk, isa.OpAdd, b.v("s1"), b.v("v2"), b.v("v3"))
				b.emit(blk, isa.OpAdd, b.v("out"), b.v("s0"), b.v("s1"))
				sink(b, blk, b.v("out"))
				return b
			},
			cfg: limited(t, 2, 2, 0),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := tt.build(t)
			checkMeaningPreserved(t, b.fn, tt.cfg)
		})
	}
}

// saveSequence reads the registers pushed before the call and popped after it.
// call is the mnemonic the block makes its one call with.
func saveSequence(lines []string, call string) (pushes, pops []string) {
	seen := false
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, call+" "):
			seen = true
		case strings.HasPrefix(line, "push ") && !seen:
			pushes = append(pushes, strings.TrimPrefix(line, "push "))
		case strings.HasPrefix(line, "pop ") && seen:
			pops = append(pops, strings.TrimPrefix(line, "pop "))
		}
	}
	return pushes, pops
}

// TestAllocateSavesNothingWithoutACall keeps the convention out of a program
// that has none. A frame is built around every mnemonic that writes ra, so a
// set that over-reached to ordinary branches would pay two lines and a stack
// cell per live value at every one of them.
func TestAllocateSavesNothingWithoutACall(t *testing.T) {
	tests := []struct {
		name  string
		build func(b *builder, blk *mir.Block)
	}{
		{
			name:  "straight line code",
			build: func(*builder, *mir.Block) {},
		},
		{
			name: "a conditional branch is not a call",
			build: func(b *builder, blk *mir.Block) {
				b.emit(blk, isa.OpBeq, b.v("a"), imm(1), mir.Label{Name: "leaf.entry"})
				blk.AddSucc(blk)
			},
		},
		{
			name: "an unconditional jump is not a call",
			build: func(b *builder, blk *mir.Block) {
				b.emit(blk, isa.OpJ, mir.Label{Name: "leaf.entry"})
				blk.AddSucc(blk)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBuilder(t, "leaf")
			blk := b.block("leaf.entry")
			b.emit(blk, isa.OpMove, b.v("a"), imm(1))
			b.emit(blk, isa.OpAdd, b.v("c"), b.v("a"), imm(2))
			sink(b, blk, b.v("c"))
			tt.build(b, blk)

			if _, err := Allocate(b.fn, Config{Scratch: []ic10.Register{13, 14, 15}}); err != nil {
				t.Fatalf("Allocate: %v", err)
			}
			for _, line := range rendered(b.fn) {
				if strings.HasPrefix(line, "push ") || strings.HasPrefix(line, "pop ") {
					t.Errorf("a call-free function emitted %q", line)
				}
			}
		})
	}
}

func TestSetStackBase(t *testing.T) {
	cases := []struct {
		name string
		// blocks is how many blocks the entry function gets; zero leaves
		// nowhere to put the instruction.
		blocks int
		base   int
		want   string
		// wantDiag is whether the rejection is the programmer's problem, and so
		// carries a source position rather than arriving as an internal error.
		wantDiag bool
	}{
		{name: "above the data region", blocks: 1, base: 12, want: "move sp 12"},
		{name: "a data region using no slots", blocks: 1, base: 0, want: "move sp 0"},
		{name: "no block to set it in", blocks: 0, base: 0},
		// push writes at sp and advances afterwards, so the last slot of the
		// array is a stack of exactly one value rather than none.
		{
			name:   "one slot of headroom",
			blocks: 1,
			base:   ic10.NumMemorySlots - 1,
			want:   "move sp " + strconv.Itoa(ic10.NumMemorySlots-1),
		},
		// A full array leaves the stack nowhere to start and is a source the
		// programmer can shorten. A base outside the array is a caller that
		// computed one, and no line of the source is answerable for it.
		{name: "no headroom for a frame", blocks: 1, base: ic10.NumMemorySlots, wantDiag: true},
		{name: "past the array", blocks: 1, base: ic10.NumMemorySlots + 1},
		{name: "below zero", blocks: 1, base: -1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newBuilder(t, "main")
			for i := range tc.blocks {
				blk := b.block("main.entry")
				if i == 0 {
					b.emit(blk, isa.OpMove, mir.PhysReg{Reg: 0}, imm(1))
				}
			}

			err := SetStackBase(b.fn, tc.base)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("SetStackBase accepted a base of %d", tc.base)
				}
				if _, got := source.DiagnosticsIn(err); got != tc.wantDiag {
					t.Errorf("a base of %d gave %q, source diagnostic = %v, want %v", tc.base, err, got, tc.wantDiag)
				}
				return
			}
			if err != nil {
				t.Fatalf("SetStackBase: %v", err)
			}
			lines := rendered(b.fn)
			if len(lines) == 0 || lines[0] != tc.want {
				t.Errorf("the program starts %v, want %q first: nothing may push before sp is set", lines, tc.want)
			}
		})
	}
}

func TestSetStackBaseRejectsANilFunction(t *testing.T) {
	if err := SetStackBase(nil, 0); err == nil {
		t.Fatalf("SetStackBase accepted a nil function")
	}
}

// TestAllocateReservesTheStackRegisters is the wiring the calling convention
// turns on: sp and ra hold values in a program with no calls and must hold none
// in a program with them, since jal writes one and every push moves the other.
func TestAllocateReservesTheStackRegisters(t *testing.T) {
	build := func(t *testing.T) *mir.Func {
		t.Helper()
		b := newBuilder(t, "wide")
		blk := b.block("wide.entry")
		// More live at once than the general file holds once scratch is held
		// back, so allocation reaches for whatever is left.
		names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o"}
		for i, name := range names {
			b.emit(blk, isa.OpMove, b.v(name), imm(float64(i)))
		}
		for _, name := range names {
			sink(b, blk, b.v(name))
		}
		return b.fn
	}

	free := build(t)
	if _, err := Allocate(free, Config{Scratch: []ic10.Register{13, 14, 15}}); err != nil {
		t.Fatalf("Allocate without the convention: %v", err)
	}
	if !usesRegister(free, ic10.RegSP) && !usesRegister(free, ic10.RegRA) {
		t.Errorf("neither sp nor ra held a value in a program with no calls, so both were wasted:\n%s",
			strings.Join(rendered(free), "\n"))
	}

	reserved := build(t)
	cfg := Config{Reserved: []ic10.Register{ic10.RegSP, ic10.RegRA}, Scratch: []ic10.Register{13, 14, 15}}
	if _, err := Allocate(reserved, cfg); err != nil {
		t.Fatalf("Allocate with the convention: %v", err)
	}
	for _, reg := range []ic10.Register{ic10.RegSP, ic10.RegRA} {
		if usesRegister(reserved, reg) {
			t.Errorf("%s held a value while the calling convention was in use:\n%s",
				reg, strings.Join(rendered(reserved), "\n"))
		}
	}
}

// TestAllocateRefusesAConfigLeavingTheStackRegistersFree pairs the two halves
// of the calling convention inside the package. No operand of a call or a push
// names sp or ra, so a calling function carries nothing keeping allocation off
// them, and a configuration reserving neither faults on a push at a negative sp.
func TestAllocateRefusesAConfigLeavingTheStackRegistersFree(t *testing.T) {
	for _, info := range linkOpcodes(t) {
		t.Run(info.Mnemonic, func(t *testing.T) {
			checkStackRegistersReserved(t, info)
		})
	}
}

func checkStackRegistersReserved(t *testing.T, call ic10.Instruction) {
	t.Helper()
	cases := []struct {
		name string
		// prologue saves ra the way instruction selection does, which pins the
		// register through the input rather than through Config.
		prologue bool
		reserved []ic10.Register
		wantErr  bool
	}{
		{name: "neither reserved", wantErr: true},
		{name: "only sp reserved", reserved: []ic10.Register{ic10.RegSP}, wantErr: true},
		{name: "only ra reserved", reserved: []ic10.Register{ic10.RegRA}, wantErr: true},
		{name: "both reserved", reserved: []ic10.Register{ic10.RegSP, ic10.RegRA}},
		{
			// The input pinning ra is as good as reserving it: preassigned
			// withholds a named register for the whole function.
			name:     "sp reserved and ra saved by the prologue",
			prologue: true,
			reserved: []ic10.Register{ic10.RegSP},
		},
		{name: "ra saved by the prologue and sp left free", prologue: true, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newBuilder(t, "caller")
			blk := b.block("caller.entry")
			if tc.prologue {
				b.emit(blk, isa.OpPush, mir.PhysReg{Reg: ic10.RegRA})
			}
			b.emit(blk, isa.OpMove, b.v("live"), imm(7))
			emitLink(b, blk, call, "callee")
			sink(b, blk, b.v("live"))

			_, err := Allocate(b.fn, Config{Reserved: tc.reserved, Scratch: []ic10.Register{13, 14, 15}})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Allocate accepted a calling function with %v reserved:\n%s",
						tc.reserved, strings.Join(rendered(b.fn), "\n"))
				}
				return
			}
			if err != nil {
				t.Fatalf("Allocate: %v", err)
			}
		})
	}
}

// wantEmittableLinkOpcodes is the nineteen forms that write ra, less bapzal and
// bnazal, which ic10 marks uncompilable in this build. Pinning the count is
// what makes it a gate: a run over whatever the table happens to hold passes as
// readily on one opcode as on seventeen.
const wantEmittableLinkOpcodes = 17

// linkOpcodes is every mnemonic that writes ra, comes back, and may appear in a
// program, in instruction table order. The unemittable forms are left out
// because mir.NewInstr refuses to build one at all.
func linkOpcodes(t *testing.T) []ic10.Instruction {
	t.Helper()
	var ops []ic10.Instruction
	for _, info := range ic10.Instructions {
		if _, refused := ic10.Unemittable(info.Opcode); refused {
			continue
		}
		if ic10.LinksReturn(info.Opcode) {
			ops = append(ops, info)
		}
	}
	if len(ops) != wantEmittableLinkOpcodes {
		t.Fatalf("the instruction table names %d emittable opcodes that write ra, want %d", len(ops), wantEmittableLinkOpcodes)
	}
	return ops
}

// TestCallOpcodesMatchTheInstructionTable holds [callOpcodes] to the machine,
// in both directions. It is a separate assertion from the ones that list serves
// so that a wrong list fails as a wrong list rather than as a silently narrower
// suite.
func TestCallOpcodesMatchTheInstructionTable(t *testing.T) {
	fromTable := make(map[ic10.Opcode]bool)
	for _, info := range ic10.Instructions {
		if ic10.LinksReturn(info.Opcode) {
			fromTable[info.Opcode] = true
		}
	}
	if len(fromTable) != len(callOpcodes) {
		t.Errorf("the instruction table names %d opcodes that write ra and the oracle names %d", len(fromTable), len(callOpcodes))
	}
	for op := range fromTable {
		if !callOpcodes[op] {
			t.Errorf("%s writes ra and the oracle does not read it as a call", op)
		}
	}
	for op := range callOpcodes {
		if !fromTable[op] {
			t.Errorf("the oracle reads %s as a call and the instruction table does not say it writes ra", op)
		}
	}
}

// emitLink appends a call spelled with the given link form. Every one takes its
// target last, so the positions before it get a constant each accepts.
func emitLink(b *builder, blk *mir.Block, call ic10.Instruction, target string) {
	b.t.Helper()
	args := make([]mir.Operand, len(call.Operands))
	for i, position := range call.Operands[:len(args)-1] {
		if position.Accepts(ic10.OperandDevice) {
			args[i] = mir.NewDeviceBase()
			continue
		}
		args[i] = imm(1)
	}
	args[len(args)-1] = mir.Label{Name: target}
	b.emit(blk, call.Opcode, args...)
}

func usesRegister(fn *mir.Func, reg ic10.Register) bool {
	for _, instr := range fn.AllInstrs() {
		for _, arg := range instr.Args {
			if phys, ok := arg.(mir.PhysReg); ok && phys.Reg == reg {
				return true
			}
		}
	}
	return false
}

// TestAllocateSavesAcrossBlocks covers a value the call does not read and the
// block does not end at, so the save has to come from the intervals rather than
// from what the call names.
func TestAllocateSavesAcrossBlocks(t *testing.T) {
	b := newBuilder(t, "spanning")
	entry := b.block("spanning.entry")
	tail := b.fn.NewBlock("spanning.tail", source.Position{File: "t.mc", Line: 9})
	entry.AddSucc(tail)

	b.emit(entry, isa.OpMove, b.v("held"), imm(3))
	b.emit(entry, isa.OpJal, mir.Label{Name: "callee"})
	b.emit(tail, isa.OpAdd, b.v("sum"), b.v("held"), imm(1))
	sink(b, tail, b.v("sum"))

	cfg := Config{Reserved: []ic10.Register{ic10.RegSP, ic10.RegRA}, Scratch: []ic10.Register{13, 14, 15}}
	if _, err := Allocate(b.fn, cfg); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	lines := rendered(b.fn)
	pushes, pops := saveSequence(lines, "jal")
	if len(pushes) != 1 || len(pops) != 1 {
		t.Errorf("a value live over the successor edge was saved by %v and restored by %v, want one of each:\n%s",
			pushes, pops, strings.Join(lines, "\n"))
	}
}

// TestStackOpcodesMatchTheInstructionTable holds the pair [stackDepths] walks
// the stack by to the machine. Only membership is checked: both carry
// DirectionReadWrite over sp, which says the register is read and assigned and
// nothing about the sign, so the signs stay where the walk spells them.
func TestStackOpcodesMatchTheInstructionTable(t *testing.T) {
	want := []ic10.Opcode{isa.OpPush, isa.OpPop}

	var movers []ic10.Opcode
	for _, info := range ic10.Instructions {
		if info.WritesImplicitly(ic10.RegSP) {
			movers = append(movers, info.Opcode)
		}
	}
	if len(movers) != len(want) {
		t.Errorf("the instruction table names %v as moving sp, and the depth walk knows %v", movers, want)
	}
	for _, op := range want {
		if !slices.Contains(movers, op) {
			t.Errorf("%s no longer writes sp implicitly in the instruction table", op)
		}
	}
	for _, op := range movers {
		if !slices.Contains(want, op) {
			t.Errorf("%s moves sp and the depth walk steps over it", op)
		}
	}
}
