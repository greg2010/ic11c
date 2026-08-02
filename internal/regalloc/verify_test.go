package regalloc

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/mir"
)

// The checker in this file is what makes the allocation tests worth anything. A
// rewritten function that is merely well formed proves nothing: every operand
// could name the wrong register and still typecheck. So each use is tracked
// back to the definitions that can reach it, before allocation over virtual
// registers and after allocation over physical registers and spill slots, and
// the two sets are required to be equal.
//
// A reload and a spill store are treated as copies rather than as definitions,
// which is what lets a definition's identity survive a trip through memory.
//
// The analysis is a may-analysis, so it assumes every use is reached by a real
// definition on every path. A test function that reads a value defined on only
// one arm of a branch would report a difference that means nothing.

type locKind uint8

const (
	locVirt locKind = iota
	locPhys
	locSlot
)

type loc struct {
	kind locKind
	id   int
}

func (l loc) String() string {
	switch l.kind {
	case locVirt:
		return mir.VirtReg{ID: uint32(l.id)}.String()
	case locPhys:
		return ic10.Register(l.id).String()
	case locSlot:
	}
	return fmt.Sprintf("slot%d", l.id)
}

// siteSet is a set of definition sites. The nil key is the value a location
// holds on entry to the function, which is unknown but is unknown identically
// before and after allocation.
type siteSet map[*mir.Instr]bool

type state map[loc]siteSet

func (s state) clone() state {
	out := make(state, len(s))
	for l, sites := range s {
		copied := make(siteSet, len(sites))
		for site := range sites {
			copied[site] = true
		}
		out[l] = copied
	}
	return out
}

func (s state) mergeFrom(other state) {
	for l, sites := range other {
		target, ok := s[l]
		if !ok {
			target = make(siteSet, len(sites))
			s[l] = target
		}
		for site := range sites {
			target[site] = true
		}
	}
}

func statesEqual(a, b state) bool {
	if len(a) != len(b) {
		return false
	}
	for l, sites := range a {
		if !setsEqual(sites, b[l]) {
			return false
		}
	}
	return true
}

func setsEqual(a, b siteSet) bool {
	if len(a) != len(b) {
		return false
	}
	for site := range a {
		if !b[site] {
			return false
		}
	}
	return true
}

// effect is what one instruction does to the tracked locations.
type effect struct {
	dst    loc
	hasDst bool
	src    loc
	isCopy bool
}

func locOf(arg mir.Operand) (loc, bool) {
	switch a := arg.(type) {
	case mir.VirtReg:
		return loc{kind: locVirt, id: int(a.ID)}, true
	case mir.PhysReg:
		return loc{kind: locPhys, id: int(a.Reg)}, true
	default:
		return loc{}, false
	}
}

func virtualEffect(t *testing.T, instr *mir.Instr) effect {
	t.Helper()
	def, err := defIndex(instr)
	if err != nil {
		t.Fatalf("defIndex(%s): %v", instr, err)
	}
	if def < 0 || def >= len(instr.Args) {
		return effect{}
	}
	l, ok := locOf(instr.Args[def])
	if !ok {
		return effect{}
	}
	return effect{dst: l, hasDst: true}
}

// physicalEffect recognises the two spill shapes the allocator emits and treats
// them as copies, so that a value poked to a slot and reloaded from it keeps
// the identity of the instruction that computed it.
func physicalEffect(t *testing.T, instr *mir.Instr) effect {
	t.Helper()
	if instr.Op == ic10.OpGet {
		dst, dstOK := instr.Args[0].(mir.PhysReg)
		dev, devOK := instr.Args[1].(mir.Device)
		addr, addrOK := instr.Args[2].(mir.Imm)
		if dstOK && devOK && addrOK && dev.Kind == mir.DeviceBase {
			return effect{
				dst:    loc{kind: locPhys, id: int(dst.Reg)},
				hasDst: true,
				src:    loc{kind: locSlot, id: int(addr.Value)},
				isCopy: true,
			}
		}
	}
	if instr.Op == ic10.OpPoke {
		addr, addrOK := instr.Args[0].(mir.Imm)
		src, srcOK := instr.Args[1].(mir.PhysReg)
		if addrOK && srcOK {
			return effect{
				dst:    loc{kind: locSlot, id: int(addr.Value)},
				hasDst: true,
				src:    loc{kind: locPhys, id: int(src.Reg)},
				isCopy: true,
			}
		}
	}
	return virtualEffect(t, instr)
}

type effectFn func(*testing.T, *mir.Instr) effect

// universe collects every location the analysis must seed, so that a join of
// two paths where only one writes a location still reports the entry value.
func universe(t *testing.T, fn *mir.Func, eff effectFn) []loc {
	t.Helper()
	seen := make(map[loc]bool)
	for _, instr := range fn.AllInstrs() {
		for _, arg := range instr.Args {
			if l, ok := locOf(arg); ok {
				seen[l] = true
			}
		}
		e := eff(t, instr)
		if e.hasDst {
			seen[e.dst] = true
			if e.isCopy {
				seen[e.src] = true
			}
		}
	}
	locs := make([]loc, 0, len(seen))
	for l := range seen {
		locs = append(locs, l)
	}
	slices.SortFunc(locs, func(a, b loc) int {
		if n := int(a.kind) - int(b.kind); n != 0 {
			return n
		}
		return a.id - b.id
	})
	return locs
}

func applyEffect(st state, e effect, instr *mir.Instr) {
	if !e.hasDst {
		return
	}
	if e.isCopy {
		sites := make(siteSet, len(st[e.src]))
		for site := range st[e.src] {
			sites[site] = true
		}
		st[e.dst] = sites
		return
	}
	st[e.dst] = siteSet{instr: true}
}

// walkReachingDefs runs the analysis to a fixed point and then replays every
// block, calling visit at the point just before each instruction executes.
func walkReachingDefs(t *testing.T, fn *mir.Func, eff effectFn, visit func(*mir.Instr, state)) {
	t.Helper()

	index := make(map[*mir.Block]int, len(fn.Blocks))
	for i, block := range fn.Blocks {
		index[block] = i
	}
	preds := make([][]int, len(fn.Blocks))
	for i, block := range fn.Blocks {
		for _, succ := range block.Succs {
			preds[index[succ]] = append(preds[index[succ]], i)
		}
	}

	entry := make(state)
	for _, l := range universe(t, fn, eff) {
		entry[l] = siteSet{nil: true}
	}

	in := make([]state, len(fn.Blocks))
	out := make([]state, len(fn.Blocks))
	for i := range fn.Blocks {
		in[i] = make(state)
		out[i] = make(state)
	}

	run := func(i int, st state, observe func(*mir.Instr, state)) state {
		st = st.clone()
		for _, instr := range fn.Blocks[i].Instrs {
			if observe != nil {
				observe(instr, st)
			}
			applyEffect(st, eff(t, instr), instr)
		}
		return st
	}

	for changed := true; changed; {
		changed = false
		for i := range fn.Blocks {
			next := make(state)
			if i == 0 {
				next.mergeFrom(entry)
			}
			for _, p := range preds[i] {
				next.mergeFrom(out[p])
			}
			in[i] = next
			result := run(i, next, nil)
			if !statesEqual(out[i], result) {
				out[i] = result
				changed = true
			}
		}
	}

	for i := range fn.Blocks {
		run(i, in[i], visit)
	}
}

// useOperands lists the operand positions of instr that name a register and are
// read rather than written.
func useOperands(t *testing.T, instr *mir.Instr) []int {
	t.Helper()
	def, err := defIndex(instr)
	if err != nil {
		t.Fatalf("defIndex(%s): %v", instr, err)
	}
	var indices []int
	for j, arg := range instr.Args {
		if j == def {
			continue
		}
		if _, ok := locOf(arg); ok {
			indices = append(indices, j)
		}
	}
	return indices
}

func siteNames(names map[*mir.Instr]string, sites siteSet) string {
	rendered := make([]string, 0, len(sites))
	for site := range sites {
		if site == nil {
			rendered = append(rendered, "entry")
			continue
		}
		rendered = append(rendered, names[site])
	}
	slices.Sort(rendered)
	return "{" + strings.Join(rendered, ", ") + "}"
}

func instrNames(fn *mir.Func) map[*mir.Instr]string {
	names := make(map[*mir.Instr]string)
	for b, block := range fn.Blocks {
		for i, instr := range block.Instrs {
			names[instr] = fmt.Sprintf("%s[%d.%d] %s", block.Label, b, i, instr)
		}
	}
	return names
}

// checkMeaningPreserved allocates fn and reports every use whose set of
// reaching definitions changed. It must be handed a function still in virtual
// form.
func checkMeaningPreserved(t *testing.T, fn *mir.Func, cfg Config) Result {
	t.Helper()

	names := instrNames(fn)
	uses := make(map[*mir.Instr][]int)
	for _, instr := range fn.AllInstrs() {
		if indices := useOperands(t, instr); len(indices) > 0 {
			uses[instr] = indices
		}
	}

	want := make(map[*mir.Instr]map[int]siteSet, len(uses))
	walkReachingDefs(t, fn, virtualEffect, func(instr *mir.Instr, st state) {
		indices, ok := uses[instr]
		if !ok {
			return
		}
		at := make(map[int]siteSet, len(indices))
		for _, j := range indices {
			l, _ := locOf(instr.Args[j])
			sites := make(siteSet, len(st[l]))
			for site := range st[l] {
				sites[site] = true
			}
			at[j] = sites
		}
		want[instr] = at
	})

	res, err := Allocate(fn, cfg)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if form := fn.RegForm(); form != mir.RegFormPhysical && form != mir.RegFormEmpty {
		t.Errorf("RegForm after allocation = %v, want physical", form)
	}

	seen := make(map[*mir.Instr]bool, len(uses))
	walkReachingDefs(t, fn, physicalEffect, func(instr *mir.Instr, st state) {
		indices, ok := uses[instr]
		if !ok {
			return
		}
		seen[instr] = true
		for _, j := range indices {
			l, ok := locOf(instr.Args[j])
			if !ok {
				t.Errorf("%s: operand %d is %s, want a physical register", names[instr], j, instr.Args[j])
				continue
			}
			if !setsEqual(want[instr][j], st[l]) {
				t.Errorf("%s: operand %d now reads %s, whose reaching definitions are %s, want %s",
					names[instr], j, l, siteNames(names, st[l]), siteNames(names, want[instr][j]))
			}
		}
	})
	for instr := range uses {
		if !seen[instr] {
			t.Errorf("%s disappeared from the function", names[instr])
		}
	}
	return res
}
