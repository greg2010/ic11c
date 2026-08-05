// Package frames measures the call frames of a program against the
// memory slots the data region leaves them: 512 slots shared, objects
// from 0 up, frames from the top down — every slot a global takes is
// one no activation can have. It reads machine IR after register allocation.
package frames

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/source"
)

// Options configures [Measure].
type Options struct {
	// StackBase is the first memory slot a call frame may take: the far side of
	// the data region and the spill slots, and the value regalloc.SetStackBase
	// put in sp. Everything from it to ic10.NumMemorySlots is what the frames
	// have, and a push at the far end faults rather than wrapping.
	StackBase int
}

// Measure reports every recursion the program's remaining memory
// slots have to absorb, and how many activations of it they hold. The
// verdict is a warning unless the arithmetic alone decides the
// outcome — too few slots for even the first activation is an error.
func Measure(ctx context.Context, prog *mir.Program, opts Options) (source.DiagnosticList, error) {
	if prog == nil || len(prog.Funcs) == 0 {
		return nil, errors.New("frames: program has no functions to measure frames of")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("frames: %w", err)
	}
	if opts.StackBase < 0 || opts.StackBase > ic10.NumMemorySlots {
		return nil, fmt.Errorf("frames: a stack base of %d is outside the %d slot array", opts.StackBase, ic10.NumMemorySlots)
	}

	g, err := newFrameGraph(prog)
	if err != nil {
		return nil, err
	}
	left := ic10.NumMemorySlots - opts.StackBase
	var diags source.DiagnosticList
	for _, cycle := range g.cycles() {
		diags = append(diags, g.report(cycle, left))
	}
	if g.funcs[0].below.amount > left {
		diags = append(diags, g.reportDepth(left))
	}
	// The two reports are gathered by call graph structure rather than in
	// reading order, and the depth is one diagnostic appended after every cycle
	// whatever line each was written on.
	diags.Sort()
	return diags, nil
}

// frameSite is one call in a function: the callee, and the slots the calling
// activation holds while that callee runs.
type frameSite struct {
	callee int
	held   int
}

// frameFunc is what one function contributes to the stack: prologue
// is the return address save, sites carry that plus the registers
// allocation pushed around one call. The rest is what
// [frameGraph.reach] works out once the components are known.
type frameFunc struct {
	fn        *mir.Func
	prologue  int
	sites     []frameSite
	component int
	reachable bool
	// entered is the slots the chain from the entry point holds when the
	// function is entered, over calls that are not themselves recursive.
	// Unseen where no such chain reaches the function, or where a
	// recursion also reaches it too (an unresolved depth).
	entered widest
	// below is the deepest the stack goes from the function's own entry
	// along chains that enter no recursion, unseen where every chain out
	// of the function enters one. deep says the measurement covers the
	// whole of what the function reaches.
	below widest
	deep  bool
}

type frameGraph struct {
	funcs []frameFunc
	// recursive marks each component that can re-enter itself, which is a
	// component of more than one function or a single one that calls itself. It
	// is the one fact here that is a component's rather than a function's.
	recursive []bool
}

// widest folds candidate amounts into their maximum and reports
// whether every one was the same. Where choices differ, the maximum
// is a bound, not the answer; a candidate that was itself such a
// bound carries that through via exact.
type widest struct {
	amount int
	exact  bool
	// seen separates an amount of zero from no candidate at all, which is what
	// distinguishes a chain holding nothing from a recursion no static chain
	// reaches.
	seen bool
}

func (w *widest) add(amount int, exact bool) {
	switch {
	case !w.seen:
		w.amount, w.exact, w.seen = amount, exact, true
	case amount == w.amount:
		w.exact = w.exact && exact
	default:
		w.amount = max(w.amount, amount)
		w.exact = false
	}
}

func newFrameGraph(prog *mir.Program) (*frameGraph, error) {
	// Every push a frame holds is counted by the physical register it names,
	// so a function still holding virtual ones is one whose pushes this walk
	// does not see — each frame comes out smaller than it is, understating
	// the depth reported. The check is here rather than assumed.
	for _, fn := range prog.Funcs {
		switch fn.RegForm() {
		case mir.RegFormVirtual, mir.RegFormMixed:
			return nil, fmt.Errorf("frames: '%s' still names virtual registers, so its frame is measured before register allocation placed the pushes that make it up", fn.Name)
		case mir.RegFormEmpty, mir.RegFormPhysical:
		}
	}

	byLabel := make(map[string]int, len(prog.Funcs))
	for i, fn := range prog.Funcs {
		for _, block := range fn.Blocks {
			byLabel[block.Label] = i
		}
	}

	// Both walks below start from prog.Funcs[0], which [mir.Program] states
	// is the function control reaches at line 0. It is reached by no jal and
	// so saves no return address; a first function that saves one is not the
	// entry the frame walk starts from.
	entrySaves, err := prologueSlots(prog.Funcs[0])
	if err != nil {
		return nil, fmt.Errorf("frames: %s: %w", prog.Funcs[0].Name, err)
	}
	if entrySaves != 0 {
		return nil, fmt.Errorf("frames: the first function '%s' saves a return address, so it is not the entry point the frame walk starts from", prog.Funcs[0].Name)
	}

	g := &frameGraph{funcs: make([]frameFunc, len(prog.Funcs))}
	for i, fn := range prog.Funcs {
		prologue, err := prologueSlots(fn)
		if err != nil {
			return nil, fmt.Errorf("frames: %s: %w", fn.Name, err)
		}
		info := frameFunc{fn: fn, prologue: prologue}
		for _, block := range fn.Blocks {
			for at, instr := range block.Instrs {
				if !ic10.LinksReturn(instr.Op) {
					continue
				}
				callee, ok := calleeOf(instr, byLabel)
				if !ok {
					return nil, fmt.Errorf("frames: the call at %s in %s names no function this program defines", instr.Pos, fn.Name)
				}
				saved, err := pushesBefore(block.Instrs[:at])
				if err != nil {
					return nil, fmt.Errorf("frames: %s: %w", fn.Name, err)
				}
				info.sites = append(info.sites, frameSite{
					callee: callee,
					held:   info.prologue + saved,
				})
			}
		}
		g.funcs[i] = info
	}
	g.components()
	g.reach()
	return g, nil
}

// stackMove is what an instruction does to the stack pointer, which is what
// decides whether the walks below may step over it.
type stackMove uint8

const (
	// stackSame leaves sp where it was. peek is one of these: it reads sp to
	// name the cell it copies out and moves nothing, so it holds no slot.
	stackSame stackMove = iota
	stackGrows
	stackShrinks
)

// stackMoveOf says what op does to sp, and fails for an opcode it
// cannot say it of. Membership comes from the target's instruction
// table; direction is spelled per opcode here, since the table's
// DirectionReadWrite does not distinguish push from pop.
func stackMoveOf(op ic10.Opcode) (stackMove, error) {
	info, known := op.Instruction()
	if !known {
		return stackSame, fmt.Errorf("opcode %v is not in the instruction table", op)
	}
	if !info.WritesImplicitly(ic10.RegSP) {
		return stackSame, nil
	}
	// Only the sp movers reach here, and the table does not order them.
	//exhaustive:ignore
	switch op {
	case isa.OpPush:
		return stackGrows, nil
	case isa.OpPop:
		return stackShrinks, nil
	}
	return stackSame, fmt.Errorf("%s moves sp and this walk cannot say which way", op)
}

// prologueSlots counts the return address save: a function reached
// through jal saves ra once at entry, a leaf keeps it in the
// register and saves nothing, and the entry point is reached by no
// jal and saves nothing either.
func prologueSlots(fn *mir.Func) (int, error) {
	if len(fn.Blocks) == 0 {
		return 0, nil
	}
	for _, instr := range fn.Blocks[0].Instrs {
		move, err := stackMoveOf(instr.Op)
		if err != nil {
			return 0, err
		}
		if move == stackGrows && isRegister(instr, ic10.RegRA) {
			return 1, nil
		}
	}
	return 0, nil
}

// pushesBefore counts the registers allocation pushed around the
// call that follows the given run of instructions, bounded by every
// sp-moving instruction rather than by what this compiler emits
// today. The return address save is excluded by register: ra is never allocated.
func pushesBefore(before []*mir.Instr) (int, error) {
	saves := 0
	for i := len(before) - 1; i >= 0; i-- {
		instr := before[i]
		move, err := stackMoveOf(instr.Op)
		if err != nil {
			return 0, err
		}
		if move == stackShrinks || ic10.LinksReturn(instr.Op) {
			return saves, nil
		}
		if move != stackGrows {
			continue
		}
		if isRegister(instr, ic10.RegRA) {
			return saves, nil
		}
		saves++
	}
	return saves, nil
}

func isRegister(instr *mir.Instr, reg ic10.Register) bool {
	if len(instr.Args) == 0 {
		return false
	}
	phys, ok := instr.Args[0].(mir.PhysReg)
	return ok && phys.Reg == reg
}

// calleeOf names the function a call transfers to. The target is
// found by operand kind, not position, since link forms disagree:
// jal takes it first, conditional forms take comparison operands
// first. A call naming a line number rather than a label is refused.
func calleeOf(instr *mir.Instr, byLabel map[string]int) (int, bool) {
	for _, arg := range instr.Args {
		label, isLabel := arg.(mir.Label)
		if !isLabel {
			continue
		}
		callee, defined := byLabel[label.Name]
		return callee, defined
	}
	return 0, false
}

// components runs Tarjan's algorithm over the call graph and marks the
// components that can re-enter themselves. It is the same question
// internal/isel answers over the LLVM module, asked again here because a
// component is also what the depth is counted around.
func (g *frameGraph) components() {
	g.recursive = make([]bool, 0, len(g.funcs))
	indices := make([]int, len(g.funcs))
	low := make([]int, len(g.funcs))
	onStack := make([]bool, len(g.funcs))
	for i := range g.funcs {
		indices[i] = -1
		g.funcs[i].component = -1
	}
	next := 0
	var stack []int

	var visit func(int)
	visit = func(v int) {
		indices[v], low[v] = next, next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, site := range g.funcs[v].sites {
			switch w := site.callee; {
			case indices[w] < 0:
				visit(w)
				low[v] = min(low[v], low[w])
			case onStack[w]:
				low[v] = min(low[v], indices[w])
			}
		}
		if low[v] != indices[v] {
			return
		}
		id := len(g.recursive)
		size := 0
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[w] = false
			g.funcs[w].component = id
			size++
			if w == v {
				break
			}
		}
		selfCall := false
		for _, site := range g.funcs[v].sites {
			selfCall = selfCall || site.callee == v
		}
		g.recursive = append(g.recursive, size > 1 || selfCall)
	}

	for i := range g.funcs {
		if indices[i] < 0 {
			visit(i)
		}
	}
}

// reach fills in what the chain above and below each function holds.
// Both walks stay outside the recursive components: what a recursion
// holds is the depth being reported, not a constant to add. The backward
// walk measures past a call into a recursion, dropping only what bracket covers.
func (g *frameGraph) reach() {
	stack := []int{0}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if g.funcs[v].reachable {
			continue
		}
		g.funcs[v].reachable = true
		for _, site := range g.funcs[v].sites {
			stack = append(stack, site.callee)
		}
	}

	// The subgraph of functions outside a recursive component is acyclic, so
	// one pass in each direction settles both walks. entered runs forward from
	// the entry point and below runs backward from the leaves.
	order := g.topological()
	// A function a recursion calls into has no settled entered amount,
	// however many recursion-free chains also reach it. Left unmarked,
	// the static chain alone would settle it and [frameGraph.bracket]
	// would report a recursion below as if nothing were above it.
	undecided := make([]bool, len(g.funcs))
	for i := range g.funcs {
		if !g.funcs[i].reachable || !g.isRecursive(i) {
			continue
		}
		for _, site := range g.funcs[i].sites {
			undecided[site.callee] = true
		}
	}
	if !g.isRecursive(0) {
		// The entry point is reached at line 0 along no call, so the one chain
		// into it holds nothing and there is no choice behind the amount.
		g.funcs[0].entered.add(0, true)
	}
	// Callers precede callees in order, so every caller has had its say by the
	// time a function is reached and the mark carries the whole way down. A
	// caller the entry point never reaches has no say at all: it builds no
	// frame, so what it holds settles nothing and leaves nothing undecided.
	for _, v := range order {
		if !g.funcs[v].reachable {
			continue
		}
		if undecided[v] {
			g.funcs[v].entered = widest{}
		}
		for _, site := range g.funcs[v].sites {
			if g.isRecursive(site.callee) {
				continue
			}
			if !g.funcs[v].entered.seen {
				undecided[site.callee] = true
				continue
			}
			g.funcs[site.callee].entered.add(g.funcs[v].entered.amount+site.held, g.funcs[v].entered.exact)
		}
	}
	for i := len(order) - 1; i >= 0; i-- {
		v := order[i]
		var reached widest
		deep, entersRecursion := true, false
		for _, site := range g.funcs[v].sites {
			if g.isRecursive(site.callee) {
				deep, entersRecursion = false, true
				continue
			}
			deep = deep && g.funcs[site.callee].deep
			if !g.funcs[site.callee].below.seen {
				continue
			}
			reached.add(site.held+g.funcs[site.callee].below.amount, g.funcs[site.callee].below.exact)
		}
		// The prologue is a candidate of its own, not a floor under the
		// others: a function returning without making any call reaches no
		// further than its own return address. It belongs to the
		// recursion's report instead where a call enters one.
		if !entersRecursion {
			reached.add(g.funcs[v].prologue, true)
		}
		g.funcs[v].below, g.funcs[v].deep = reached, deep
	}
}

// topological orders the functions outside the recursive components so that
// every caller precedes its callees. A function inside one is omitted: it has
// no acyclic position, and both walks stop at its component's edge.
func (g *frameGraph) topological() []int {
	order := make([]int, 0, len(g.funcs))
	placed := make([]bool, len(g.funcs))
	var visit func(int)
	visit = func(v int) {
		if placed[v] || g.isRecursive(v) {
			return
		}
		placed[v] = true
		for _, site := range g.funcs[v].sites {
			visit(site.callee)
		}
		order = append(order, v)
	}
	for i := range g.funcs {
		visit(i)
	}
	slices.Reverse(order)
	return order
}

func (g *frameGraph) isRecursive(v int) bool { return g.recursive[g.funcs[v].component] }

// cycles lists the recursive components the entry point reaches, each as the
// functions in it in program order. A component nothing reaches is left out:
// its frames are never built, so there is no depth to state.
func (g *frameGraph) cycles() [][]int {
	// Component ids are Tarjan's completion order rather than program order, so
	// where records the position each component took in the answer and the
	// program order the walk runs in is what fixes it.
	where := make([]int, len(g.recursive))
	for i := range where {
		where[i] = -1
	}
	var cycles [][]int
	for i := range g.funcs {
		if !g.isRecursive(i) || !g.funcs[i].reachable {
			continue
		}
		id := g.funcs[i].component
		if where[id] < 0 {
			where[id] = len(cycles)
			cycles = append(cycles, nil)
		}
		cycles[where[id]] = append(cycles[where[id]], i)
	}
	return cycles
}

// report states one recursion against the slots left for frames.
func (g *frameGraph) report(cycle []int, left int) source.Diagnostic {
	step := g.step(cycle)
	pos := g.funcs[cycle[0]].fn.Pos
	subject := g.cycleSubject(cycle)

	spent, tail, end := g.bracket(cycle)
	if end != bracketStatic {
		return source.Diagnostic{Pos: pos, Severity: source.Warning, Msg: fmt.Sprintf(
			"%s, and how deep it goes is not decided at compile time; each activation holds %s of the %s left above the data region, and %s, so no activation count holds for it — bound the depth in the source, or rewrite the recursion as a loop",
			subject, source.Plural(step.amount, "slot"), source.Plural(left, "slot"), end)}
	}

	if step.amount <= 0 {
		// A recursion whose activations hold nothing never reaches the end of
		// the array, so there is no activation count to state. What rules it
		// out is a prologue elsewhere, not the quotient below.
		return source.Diagnostic{Pos: pos, Severity: source.Warning, Msg: fmt.Sprintf(
			"%s, and how deep it goes is not decided at compile time; an activation of it holds none of the %s left above the data region, so no activation count bounds it — bound the depth in the source, or rewrite the recursion as a loop",
			subject, source.Plural(left, "slot"))}
	}

	room := left - spent.amount - tail.amount
	if room < 0 {
		return source.Diagnostic{Pos: pos, Msg: fmt.Sprintf(
			"%s, and the data region leaves no room for even one activation: %s remain above it, the call into it already holds %s, and an activation holds %s more — shorten an array, drop a global, or rewrite the recursion as a loop",
			subject, source.Plural(left, "slot"), source.Plural(spent.amount, "slot"), source.Plural(tail.amount, "slot"))}
	}
	// One more than the quotient: the activations above the deepest are each
	// blocked at a call and pay step, and the deepest pays tail instead.
	depth := room/step.amount + 1
	// All four amounts the count is divided out of, not just the two an
	// activation is described by: stating only step and slots left invites
	// the reader to multiply them back, overshooting by whatever the calls
	// into the recursion and its deepest activation hold.
	arithmetic := fmt.Sprintf("of the %s left above the data region the calls into it hold %s and its deepest activation holds %s, and every activation above that one holds %s",
		source.Plural(left, "slot"), source.Plural(spent.amount, "slot"), source.Plural(tail.amount, "slot"), source.Plural(step.amount, "slot"))
	if varying := varyingAmounts(step, spent, tail); varying != "" {
		// Every part of the arithmetic that had a choice was measured by its most
		// expensive answer, so a run that goes a cheaper way reaches further than
		// this and none reaches less.
		return source.Diagnostic{Pos: pos, Severity: source.Warning, Msg: fmt.Sprintf(
			"%s, and how deep it goes is not decided at compile time; %s do not all hold the same, so the count is measured over the largest: %s, so there is room for at least %s — bound the depth in the source, or rewrite the recursion as a loop",
			subject, varying, arithmetic, source.Plural(depth, "activation"))}
	}
	return source.Diagnostic{Pos: pos, Severity: source.Warning, Msg: fmt.Sprintf(
		"%s, and how deep it goes is not decided at compile time; %s, so there is room for %s and the next one faults on a push — bound the depth in the source, or rewrite the recursion as a loop",
		subject, arithmetic, source.Plural(depth, "activation"))}
}

// reportDepth states the deepest chain of ordinary calls the
// program can make, against the slots left for the frames. It is an
// error where recursion reports are warnings: every activation on
// the chain is entered by a call the program holds, so it faults whatever the data says.
func (g *frameGraph) reportDepth(left int) source.Diagnostic {
	chain := g.deepestChain()
	return source.Diagnostic{Pos: g.funcs[0].fn.Pos, Msg: fmt.Sprintf(
		"the calls nest deep enough to hold %s at once and only %s are left above the data region, so the chain %s faults on a push before it returns — shorten an array, drop a global, or hold fewer values across the calls on that chain",
		source.Plural(g.funcs[0].below.amount, "slot"), source.Plural(left, "slot"), strings.Join(chain, " to "))}
}

// deepestChain names the functions from the entry point down to the
// deepest activation the calls reach, following the site that
// attained the maximum at each step. A callee with no measurement of
// its own is passed over, since an unseen amount would read as zero.
func (g *frameGraph) deepestChain() []string {
	chain := []string{g.funcs[0].fn.Name}
	for v := g.deepestSite(0); v >= 0; v = g.deepestSite(v) {
		chain = append(chain, g.funcs[v].fn.Name)
	}
	return chain
}

// deepestSite is the callee of the site that attained v's own measurement, or
// -1 where no site did.
func (g *frameGraph) deepestSite(v int) int {
	for _, site := range g.funcs[v].sites {
		below := g.funcs[site.callee].below
		if below.seen && site.held+below.amount == g.funcs[v].below.amount {
			return site.callee
		}
	}
	return -1
}

// varyingAmounts names the parts of the arithmetic measured over
// the largest of differing choices, empty where all three had one
// answer. All three are maxima: reading only the cycle's step would
// understate the capacity of a recursion whose chains or tail vary.
func varyingAmounts(step, spent, tail widest) string {
	var parts []string
	if !step.exact {
		parts = append(parts, "the members of the cycle")
	}
	if !spent.exact {
		parts = append(parts, "the calls that reach it")
	}
	if !tail.exact {
		parts = append(parts, "the ways it bottoms out")
	}
	return joinPhrases(parts)
}

// joinPhrases writes a list as the subject of one sentence, and an empty list
// as no subject at all.
func joinPhrases(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// step is the slots one activation of a cycle holds while the call
// that continues the cycle runs, measured by the most expensive
// activation where members differ. Whether the amount is positive is
// [frameGraph.report]'s to establish, since it divides by it.
func (g *frameGraph) step(cycle []int) widest {
	var held widest
	for _, v := range cycle {
		for _, site := range g.funcs[v].sites {
			if g.funcs[site.callee].component != g.funcs[v].component {
				continue
			}
			held.add(site.held, true)
		}
	}
	return held
}

// bracketEnd names the end of the stack around a recursion whose own
// depth is not decided, empty where both ends are constants. The two
// failures are opposites, so which end failed is carried out of
// [frameGraph.bracket] rather than re-derived by the message.
type bracketEnd string

const (
	bracketStatic bracketEnd = ""
	bracketAbove  bracketEnd = "it is entered from inside another recursion whose own depth is not decided either"
	bracketBelow  bracketEnd = "it calls into another recursion whose own depth is not decided either"
)

// bracket is what the stack holds around the recursion: spent by
// the chain that reached it, tail by the deepest activation
// returning instead of recursing again. A call crossing a recursion
// carries its own undecided depth, failing whichever end crosses it.
func (g *frameGraph) bracket(cycle []int) (spent, tail widest, end bracketEnd) {
	component := g.funcs[cycle[0]].component
	for i := range g.funcs {
		// A function of the component itself calls in through the cycle rather
		// than into it, and one nothing reaches builds no frame to hold.
		if g.funcs[i].component == component || !g.funcs[i].reachable {
			continue
		}
		for _, site := range g.funcs[i].sites {
			if g.funcs[site.callee].component != component {
				continue
			}
			// entered is filled in only along the chains outside every
			// recursion, so a reachable caller without one is a caller inside a
			// recursive component or below one, and either way the stack above
			// this recursion holds a depth of that one.
			if !g.funcs[i].entered.seen {
				return widest{}, widest{}, bracketAbove
			}
			spent.add(g.funcs[i].entered.amount+site.held, g.funcs[i].entered.exact)
		}
	}
	// A component the loop found no caller for holds the entry point:
	// reachability is rooted there, so every other member arrives via a
	// caller outside the component. The entry itself is reached at line
	// 0 along no call, so the stack above it holds nothing.
	if !spent.seen {
		spent.add(0, true)
	}
	for _, v := range cycle {
		var reached widest
		reached.add(g.funcs[v].prologue, true)
		for _, site := range g.funcs[v].sites {
			if g.funcs[site.callee].component == component {
				continue
			}
			if !g.funcs[site.callee].deep {
				return widest{}, widest{}, bracketBelow
			}
			reached.add(site.held+g.funcs[site.callee].below.amount, g.funcs[site.callee].below.exact)
		}
		// Which member the recursion bottoms out in is the data's choice, so a
		// cycle whose members reach different depths below themselves is
		// measured by the deepest of them and holds no more than that.
		tail.add(reached.amount, reached.exact)
	}
	return spent, tail, bracketStatic
}

// cycleSubject names the functions a recursion runs through, as the subject of
// the sentence that reports it.
func (g *frameGraph) cycleSubject(cycle []int) string {
	names := make([]string, len(cycle))
	for i, v := range cycle {
		names[i] = "'" + g.funcs[v].fn.Name + "'"
	}
	switch len(names) {
	case 1:
		return joinPhrases(names) + " can reach itself through a call"
	case 2:
		return joinPhrases(names) + " can reach each other through a call"
	default:
		return joinPhrases(names) + " can reach one another through a call"
	}
}
