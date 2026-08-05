package regalloc

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/mir"
)

type locKind uint8

const (
	locVirt locKind = iota
	locPhys
	locSlot
	// locStack is one cell of the machine's stack, named by the depth a push
	// writes it at. The chip has a stack pointer and no frame, so nothing else
	// joins a push to the pop that undoes it: one location for the whole stack
	// would make "push a; push b; pop b; pop a" restore a from b's cell.
	locStack
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
	case locStack:
		return fmt.Sprintf("stack%d", l.id)
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
	// kills are locations the instruction leaves holding something, without
	// naming where it came from. A call is the only instruction with any:
	// without them it destroys nothing, and a save of the wrong register is
	// then invisible because the value was never taken away from the use.
	kills []loc
}

// clobbered is every register a call leaves undefined, which is all of them.
// The caller's pushed cells survive because they sit below the depth the callee
// starts pushing at; the spill slots survive only if the driver stacks bases,
// which is what [TestAllocateSlotsOfSuccessiveFunctionsAreDisjoint] holds.
var clobbered = func() []loc {
	kills := make([]loc, 0, ic10.NumRegisters)
	for r := range ic10.Register(ic10.NumRegisters) {
		kills = append(kills, loc{kind: locPhys, id: int(r)})
	}
	return kills
}()

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

// callOpcodes is every opcode that leaves a return address in ra, stated here
// rather than read out of the table so this oracle and the allocator cannot
// agree through one wrong answer: a dropped entry would make both read the call
// as straight-line code. [TestCallOpcodesMatchTheInstructionTable] holds it.
var callOpcodes = map[ic10.Opcode]bool{
	isa.OpJal:    true,
	isa.OpBltzal: true,
	isa.OpBgezal: true,
	isa.OpBlezal: true,
	isa.OpBgtzal: true,
	isa.OpBeqal:  true,
	isa.OpBneal:  true,
	isa.OpBdseal: true,
	isa.OpBdnsal: true,
	isa.OpBltal:  true,
	isa.OpBgtal:  true,
	isa.OpBleal:  true,
	isa.OpBgeal:  true,
	isa.OpBapal:  true,
	isa.OpBnaal:  true,
	isa.OpBeqzal: true,
	isa.OpBnezal: true,
	isa.OpBapzal: true,
	isa.OpBnazal: true,
}

func virtualEffect(t *testing.T, instr *mir.Instr) effect {
	t.Helper()
	if callOpcodes[instr.Op] {
		return effect{kills: clobbered}
	}
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

// stackDepths is how many values the stack holds just before each instruction,
// which is what names the cell a push writes and a pop reads. It also fails the
// two shapes that would leave a cell unnamed and so silently relate the wrong
// push to the wrong pop: a block reached at two depths, and a pop below entry.
func stackDepths(t *testing.T, fn *mir.Func) map[*mir.Instr]int {
	t.Helper()
	index := make(map[*mir.Block]int, len(fn.Blocks))
	for i, block := range fn.Blocks {
		index[block] = i
	}
	entry := make([]int, len(fn.Blocks))
	known := make([]bool, len(fn.Blocks))
	depths := make(map[*mir.Instr]int, len(fn.Blocks))

	var walk func(i int)
	walk = func(i int) {
		depth := entry[i]
		for _, instr := range fn.Blocks[i].Instrs {
			depths[instr] = depth
			// The two sp movers are named rather than read off the table, for
			// the reason [callOpcodes] is. Every other opcode leaves the depth
			// alone.
			//exhaustive:ignore
			switch instr.Op {
			case isa.OpPush:
				depth++
			case isa.OpPop:
				depth--
			}
			if depth < 0 {
				t.Fatalf("%s in %s pops below the depth the function was entered at", instr, fn.Blocks[i].Label)
			}
		}
		for _, succ := range fn.Blocks[i].Succs {
			j := index[succ]
			if known[j] {
				if entry[j] != depth {
					t.Errorf("%s is reached at stack depth %d and at %d, so no cell names what a pop in it reads", succ.Label, entry[j], depth)
				}
				continue
			}
			entry[j], known[j] = depth, true
			walk(j)
		}
	}
	// The entry block starts at zero, and a block no path reaches is seeded
	// there too so that the replay below has a depth for every instruction.
	for i := range fn.Blocks {
		if known[i] {
			continue
		}
		known[i] = true
		walk(i)
	}
	return depths
}

// physicalEffects reads the two spill shapes and the two stack shapes as copies
// rather than definitions, so a value that went to memory and came back keeps
// the identity of the instruction that computed it. depths joins a push to the
// pop that undoes it and must have been taken over fn after allocation ran.
func physicalEffects(depths map[*mir.Instr]int) effectFn {
	return func(t *testing.T, instr *mir.Instr) effect {
		t.Helper()
		if instr.Op == isa.OpGet {
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
		if instr.Op == isa.OpPoke {
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
		if instr.Op == isa.OpPush {
			if src, ok := instr.Args[0].(mir.PhysReg); ok {
				return effect{
					dst:    loc{kind: locStack, id: depths[instr]},
					hasDst: true,
					src:    loc{kind: locPhys, id: int(src.Reg)},
					isCopy: true,
				}
			}
		}
		if instr.Op == isa.OpPop {
			if dst, ok := instr.Args[0].(mir.PhysReg); ok {
				// push writes at sp and advances, so the cell a pop takes back
				// is the one below the depth it arrives at.
				return effect{
					dst:    loc{kind: locPhys, id: int(dst.Reg)},
					hasDst: true,
					src:    loc{kind: locStack, id: depths[instr] - 1},
					isCopy: true,
				}
			}
		}
		return virtualEffect(t, instr)
	}
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
		for _, l := range e.kills {
			seen[l] = true
		}
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
	for _, l := range e.kills {
		st[l] = siteSet{instr: true}
	}
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

// useOperands lists the operand positions naming a register that instr reads.
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
// reaching definitions changed. fn must still be in virtual form, and every use
// in it must be reached by a real definition on every path: the analysis is a
// may-analysis, so a value defined on one arm of a branch reports nothing.
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
	walkReachingDefs(t, fn, physicalEffects(stackDepths(t, fn)), func(instr *mir.Instr, st state) {
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
