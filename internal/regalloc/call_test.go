package regalloc

import (
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/source"
)

// rendered lists a function's instructions the way the emitter would lay them
// out, which is what a save sequence has to be read in.
func rendered(fn *mir.Func) []string {
	var lines []string
	for _, instr := range fn.AllInstrs() {
		lines = append(lines, instr.String())
	}
	return lines
}

// TestAllocateSavesLiveValuesAroundACall covers the whole of the caller's side
// of the convention: what is live across the call is pushed before it and
// popped after it, in mirrored order, and what dies at it costs nothing.
//
// A missing pop is not a wrong answer but a stack pointer that walks: pop
// decrements sp before its bounds check and does not roll the decrement back,
// so the pairing is what the test is really about.
func TestAllocateSavesLiveValuesAroundACall(t *testing.T) {
	cases := []struct {
		name string
		// build lays out one block around a single call.
		build func(b *builder, blk *mir.Block)
		// wantSaves is the number of registers saved around the call.
		wantSaves int
	}{
		{
			name: "a value read after the call is saved",
			build: func(b *builder, blk *mir.Block) {
				b.emit(blk, ic10.OpMove, b.v("live"), imm(7))
				b.emit(blk, ic10.OpJal, mir.Label{Name: "callee"})
				sink(b, blk, b.v("live"))
			},
			wantSaves: 1,
		},
		{
			name: "a value the call is the last reader of is not saved",
			build: func(b *builder, blk *mir.Block) {
				b.emit(blk, ic10.OpMove, b.v("arg"), imm(7))
				b.emit(blk, ic10.OpMove, mir.PhysReg{Reg: 0}, b.v("arg"))
				b.emit(blk, ic10.OpJal, mir.Label{Name: "callee"})
			},
			wantSaves: 0,
		},
		{
			name: "a value defined by the call is not saved around it",
			build: func(b *builder, blk *mir.Block) {
				b.emit(blk, ic10.OpJal, mir.Label{Name: "callee"})
				b.emit(blk, ic10.OpMove, b.v("result"), mir.PhysReg{Reg: 0})
				sink(b, blk, b.v("result"))
			},
			wantSaves: 0,
		},
		{
			name: "two values live across the call are both saved",
			build: func(b *builder, blk *mir.Block) {
				b.emit(blk, ic10.OpMove, b.v("a"), imm(1))
				b.emit(blk, ic10.OpMove, b.v("b"), imm(2))
				b.emit(blk, ic10.OpJal, mir.Label{Name: "callee"})
				b.emit(blk, ic10.OpAdd, b.v("c"), b.v("a"), b.v("b"))
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
			if _, err := Allocate(b.fn, cfg); err != nil {
				t.Fatalf("Allocate: %v", err)
			}

			lines := rendered(b.fn)
			pushes, pops := saveSequence(lines)
			if len(pushes) != tc.wantSaves {
				t.Errorf("saved %v around the call, want %d registers:\n%s", pushes, tc.wantSaves, strings.Join(lines, "\n"))
			}
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

// saveSequence reads the registers pushed before the call and popped after it.
func saveSequence(lines []string) (pushes, pops []string) {
	seen := false
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "jal "):
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
// that has none: an inlined program pays nothing for the machinery.
func TestAllocateSavesNothingWithoutACall(t *testing.T) {
	b := newBuilder(t, "leaf")
	blk := b.block("leaf.entry")
	b.emit(blk, ic10.OpMove, b.v("a"), imm(1))
	b.emit(blk, ic10.OpAdd, b.v("c"), b.v("a"), imm(2))
	sink(b, blk, b.v("c"))

	if _, err := Allocate(b.fn, Config{Scratch: []ic10.Register{13, 14, 15}}); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	for _, line := range rendered(b.fn) {
		if strings.HasPrefix(line, "push ") || strings.HasPrefix(line, "pop ") {
			t.Errorf("a call-free function emitted %q", line)
		}
	}
}

func TestSetStackBase(t *testing.T) {
	cases := []struct {
		name string
		// blocks is how many blocks the entry function gets; zero is a function
		// with nowhere to put the instruction.
		blocks int
		base   int
		want   string
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
		{name: "no headroom for a frame", blocks: 1, base: ic10.NumMemorySlots},
		{name: "below zero", blocks: 1, base: -1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newBuilder(t, "main")
			for i := range tc.blocks {
				blk := b.block("main.entry")
				if i == 0 {
					b.emit(blk, ic10.OpMove, mir.PhysReg{Reg: 0}, imm(1))
				}
			}

			err := SetStackBase(b.fn, tc.base)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("SetStackBase accepted a base of %d", tc.base)
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
// turns on: sp and ra hold values in a program with no calls and must hold
// none in a program with them, since jal writes one and every push moves the
// other.
func TestAllocateReservesTheStackRegisters(t *testing.T) {
	build := func(t *testing.T) *mir.Func {
		t.Helper()
		b := newBuilder(t, "wide")
		blk := b.block("wide.entry")
		// More simultaneously live values than the general file holds once the
		// scratch registers are held back, so allocation reaches for whatever
		// is left.
		names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o"}
		for i, name := range names {
			b.emit(blk, ic10.OpMove, b.v(name), imm(float64(i)))
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
// block does not end at: liveness carries it over the successor edge, so the
// save has to come from the intervals rather than from what the call names.
func TestAllocateSavesAcrossBlocks(t *testing.T) {
	b := newBuilder(t, "spanning")
	entry := b.block("spanning.entry")
	tail := b.fn.NewBlock("spanning.tail", source.Position{File: "t.mc", Line: 9})
	entry.AddSucc(tail)

	b.emit(entry, ic10.OpMove, b.v("held"), imm(3))
	b.emit(entry, ic10.OpJal, mir.Label{Name: "callee"})
	b.emit(tail, ic10.OpAdd, b.v("sum"), b.v("held"), imm(1))
	sink(b, tail, b.v("sum"))

	cfg := Config{Reserved: []ic10.Register{ic10.RegSP, ic10.RegRA}, Scratch: []ic10.Register{13, 14, 15}}
	if _, err := Allocate(b.fn, cfg); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	lines := rendered(b.fn)
	pushes, pops := saveSequence(lines)
	if len(pushes) != 1 || len(pops) != 1 {
		t.Errorf("a value live over the successor edge was saved by %v and restored by %v, want one of each:\n%s",
			pushes, pops, strings.Join(lines, "\n"))
	}
}
