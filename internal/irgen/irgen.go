// Package irgen lowers a checked MicroC program to LLVM IR.
//
// long long, bool and double all become LLVM double, the machine's only register type, so every
// identity LLVM applies is a float identity: x - x is not zero, x / x is not one, and an ordered
// comparison and its apparent negation are not complements.
package irgen

import (
	"context"
	"errors"
	"fmt"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/sema"
	"github.com/greg2010/ic11c/internal/source"
	"tinygo.org/x/go-llvm"
)

// dataLayout describes the machine to the mid-level optimizer. The load-bearing field is n64, the
// native integer width: without it InstCombine narrows a proven-small value (a loop counter, a
// switch tag) to a width the backend cannot select, since the register file holds nothing
// narrower than a whole double.
var dataLayout = fmt.Sprintf("e-p:%[1]d:%[1]d-i%[1]d:%[1]d-n%[1]d", ic10.SlotBits)

// maxInlineDepth bounds nested inlining. A recursive function is compiled out
// of line rather than inlined, so hitting this means a call chain deep enough
// that the expansion would not fit the byte budget anyway.
const maxInlineDepth = 32

// maxDescent bounds this stage's own recursion, counted in levels on the stack. It is not
// [ast.MaxNestingDepth]: an inlined call descends inside the caller's own recursion, so a chained
// inline can put several functions' depths on one stack at once. The value keeps Go's goroutine
// stack several doublings under its ceiling; TestTheDescentBudgetStandsUnderTheStackCeiling checks it.
const maxDescent = 50_000

// maxLowered bounds the same recursion counted over the whole program rather than the stack, since
// depth alone does not bound size: a call inlined at every level of a chain multiplies the work
// generated there. Nothing downstream catches an oversized module before the allocator does, so
// this is refused here. TestTheCorpusIsFarUnderTheLoweringBudget checks it against the fixtures.
const maxLowered = 200_000

// Options configures generation.
type Options struct {
	// ModuleName names the LLVM module. It defaults to the source file name.
	ModuleName string
	// Dir is the directory recorded in the debug compile unit. It is never
	// opened, and defaults to ".".
	Dir string
	// OutOfLineCallSites gives a real definition to any function named by at least this many call
	// sites, on top of the recursive ones that always get one. Zero is the shipped rule: recursion
	// and nothing else. No threshold may reach a function taking a dev parameter, since a device
	// position needs a literal at the call site; TestCorpusMeasurements in cmd/ic11c checks that.
	OutOfLineCallSites int
}

// Result owns the LLVM state Generate produced.
//
// Context and Module are borrowed, not given: Dispose releases both, and every
// value read out of the module is invalid afterwards.
type Result struct {
	Context llvm.Context
	Module  llvm.Module
	// InlineSites names the function spliced in at each inlined call, keyed by
	// the call expression's line and column. A debug location survives the
	// optimizer carrying those two fields and no name, so the name a size report
	// puts against an inline site is looked up here.
	InlineSites map[source.LineCol]string
}

// Dispose releases the module and its context. It is safe to call twice.
func (r *Result) Dispose() {
	if r == nil {
		return
	}
	if !r.Module.IsNil() {
		r.Module.Dispose()
		r.Module = llvm.Module{}
	}
	if !r.Context.IsNil() {
		r.Context.Dispose()
		r.Context = llvm.Context{}
	}
}

// Generate lowers prog to an LLVM module holding main and one definition per function compiled out of
// line, with every other call inlined. A construct outside the lowered subset, a module failing LLVM's
// own verifier, or a tree exceeding [maxDescent] is reported as a [source.DiagnosticList] rather than
// aborting. prog must have analyzed without diagnostics; on error the LLVM state is released before returning.
func Generate(ctx context.Context, prog *sema.Program, opts Options) (*Result, error) {
	return generateWithin(ctx, prog, opts, budgets{descent: maxDescent, lowered: maxLowered})
}

// budgets are the two bounds this stage keeps over its own recursion: how deep
// it may descend, and how many levels it may descend in all.
type budgets struct {
	descent int
	lowered int
}

// generateWithin is [Generate] with those bounds given rather than shipped,
// which is how each refusal is held to what it reports without a program big
// enough to reach the real one.
func generateWithin(ctx context.Context, prog *sema.Program, opts Options, limits budgets) (*Result, error) {
	if prog == nil {
		return nil, errors.New("irgen: nil program")
	}
	if prog.Main == nil || prog.Main.Decl == nil || prog.Main.Decl.Body == nil {
		return nil, errors.New("irgen: program defines no main body")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("irgen: %w", err)
	}

	name := opts.ModuleName
	if name == "" && prog.File != nil {
		name = prog.File.Name
	}
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}

	g := newGenerator(ctx, prog, name, dir, opts, limits)
	err := g.generate()
	// LLVM requires a DIBuilder be finalized before it is destroyed, and ends the process where it
	// was not, so both happen here rather than at the end of a lowering a diagnostic can leave early.
	g.di.Finalize()
	g.di.Destroy()
	g.builder.Dispose()
	g.allocaBuilder.Dispose()
	if err != nil {
		g.result.Dispose()
		return nil, err
	}
	return g.result, nil
}

// generate lowers the whole program. The LLVM state is left alive either way, so the module text
// can go into a diagnostic and the caller decides what to release.
func (g *generator) generate() error {
	g.run()
	g.diags.Sort()
	if err := g.diags.Err(); err != nil {
		return err
	}
	if err := llvm.VerifyModule(g.result.Module, llvm.ReturnStatusAction); err != nil {
		return fmt.Errorf("irgen: generated module does not verify: %w\n%s", err, g.result.Module.String())
	}
	return nil
}

// frame is one function body in progress: the definition being generated, or a
// call inlined into it. A return stores through retSlot and branches to
// retBlock, which is the only way out of either.
type frame struct {
	// retSlot holds the value a return writes. It is nil for a void result.
	retSlot llvm.Value
	// retBlock is where control resumes: the definition's epilogue, or the
	// continuation after an inlined call site.
	retBlock llvm.BasicBlock
}

// generator carries the state of one lowering. ctx is held rather than threaded through the mutual
// recursion; it is checked once per statement, which bounds cancellation latency by one statement.
type generator struct {
	ctx    context.Context
	prog   *sema.Program
	result *Result
	diags  source.DiagnosticList

	builder llvm.Builder
	// allocaBuilder inserts at the top of the entry block. Every alloca lands
	// there so that mem2reg can promote it; one created inside a loop body
	// would be a fresh object per iteration and would not promote.
	allocaBuilder llvm.Builder
	di            *llvm.DIBuilder
	file          llvm.Metadata
	subroutine    llvm.Metadata
	scope         llvm.Metadata
	// inlinedAt is the call site the code being generated was spliced in at,
	// nil in a definition's own body. It nests, so a callee inlined into a
	// callee carries the whole chain.
	inlinedAt  llvm.Metadata
	noInline   llvm.Attribute
	noBuiltins llvm.Attribute
	// pure marks a declaration that computes a result and does nothing else,
	// which is what lets a dead one be deleted. The declaration stays opaque
	// either way: LLVM learns what the call does not do, never what it does.
	pure []llvm.Attribute
	// faulting is pure without speculatable, for a declaration whose instruction can refuse its
	// operands: withholding speculatable is the only way to tell LLVM the operation must not run
	// where the source did not run it. A dead call still gets deleted along with its fault, which is
	// the direction to be wrong in under a 128 line budget. TestAFaultingCallNothingReadsIsDeleted checks it.
	faulting []llvm.Attribute

	i1  llvm.Type
	i64 llvm.Type
	f64 llvm.Type
	ptr llvm.Type
	// outlineSites is [Options.OutOfLineCallSites].
	outlineSites int

	fn llvm.Value
	// entryPoint reports that the definition being generated is main, which is
	// the one definition with a single activation: every other is a function
	// compiled out of line, which is to say a recursive one.
	entryPoint bool

	// terminated reports whether the block being built already ends in a
	// terminator, which is what makes code after a return or a break safe to
	// keep generating and discard.
	terminated bool

	// descent and lowered track the two bounds in limits. refused reports that one already came
	// back, so only the first refusal past a bound is reported.
	descent int
	lowered int
	limits  budgets
	refused bool

	// symbols indexes every symbol by the declaration that introduced it.
	// Analysis exposes a local only through the uses that name it, so a local
	// nothing reads has no entry — and needs no storage, though its initializer
	// still runs for its effects.
	symbols map[ast.Node]*sema.Symbol
	// globalStorage holds the module global every file-scope object lives in.
	globalStorage map[*sema.Symbol]llvm.Value
	// locals holds the storage of the definition being generated. It is reset
	// per definition rather than kept per symbol, because a function inlined
	// into two definitions needs an alloca in each: an alloca of one function
	// referenced from another is not a module LLVM will verify.
	locals map[*sema.Symbol]llvm.Value
	// devices binds each dev parameter of a call being inlined to the device
	// the site named. A device is a compile-time operand, so a parameter of one
	// is resolved by substitution rather than by a value in a register, which is
	// why only a function that inlines may take one.
	devices    map[*sema.Symbol]sema.Device
	intrinsics map[string]llvm.Value
	// outlined maps every function compiled out of line to its definition, and
	// is what a call site consults to choose between a call and an expansion.
	outlined    map[*sema.Func]llvm.Value
	definitions []definition
	frames      []frame
	breaks      []llvm.BasicBlock
	continues   []llvm.BasicBlock
	inlining    []*sema.Func
}

func declSymbols(prog *sema.Program) map[ast.Node]*sema.Symbol {
	symbols := make(map[ast.Node]*sema.Symbol)
	record := func(sym *sema.Symbol) {
		if sym != nil && sym.Decl != nil {
			symbols[sym.Decl] = sym
		}
	}
	for _, sym := range prog.Globals {
		record(sym)
	}
	for _, fn := range prog.Funcs {
		for _, param := range fn.Params {
			record(param)
		}
	}
	for _, sym := range prog.Uses {
		record(sym)
	}
	return symbols
}

func newGenerator(ctx context.Context, prog *sema.Program, name, dir string, opts Options, limits budgets) *generator {
	llvmCtx := llvm.NewContext()
	mod := llvmCtx.NewModule(name)
	mod.SetDataLayout(dataLayout)
	g := &generator{
		ctx:           ctx,
		prog:          prog,
		limits:        limits,
		result:        &Result{Context: llvmCtx, Module: mod},
		builder:       llvmCtx.NewBuilder(),
		allocaBuilder: llvmCtx.NewBuilder(),
		i1:            llvmCtx.Int1Type(),
		i64:           llvmCtx.Int64Type(),
		f64:           llvmCtx.DoubleType(),
		ptr:           llvm.PointerType(llvmCtx.Int64Type(), 0),
		outlineSites:  opts.OutOfLineCallSites,
		symbols:       declSymbols(prog),
		globalStorage: make(map[*sema.Symbol]llvm.Value),
		locals:        make(map[*sema.Symbol]llvm.Value),
		devices:       make(map[*sema.Symbol]sema.Device),
		intrinsics:    make(map[string]llvm.Value),
		outlined:      make(map[*sema.Func]llvm.Value),
		noInline:      llvmCtx.CreateEnumAttribute(llvm.AttributeKindID("noinline"), 0),
		noBuiltins:    llvmCtx.CreateStringAttribute("no-builtins", ""),
	}
	for _, name := range []string{"nounwind", "willreturn", "memory"} {
		// memory takes a bitmask of the locations the function may touch, and
		// zero is none of them, which is what makes the rest of these count.
		g.faulting = append(g.faulting, llvmCtx.CreateEnumAttribute(llvm.AttributeKindID(name), 0))
	}
	g.pure = append(append([]llvm.Attribute{}, g.faulting...),
		llvmCtx.CreateEnumAttribute(llvm.AttributeKindID("speculatable"), 0))
	g.result.InlineSites = make(map[source.LineCol]string)
	g.di = g.declareDebugInfo(name, dir)
	return g
}

func (g *generator) run() {
	g.declareFunctions()
	for _, def := range g.definitions {
		if err := g.ctx.Err(); err != nil {
			g.errorf(def.fn.Pos, "lowering was cancelled: %v", err)
			return
		}
		g.define(def)
	}
}

func (g *generator) errorf(pos source.Position, format string, args ...any) {
	g.diags.Addf(pos, format, args...)
}

// descend charges one level of the recursion this stage descends the tree with and reports whether
// the budget covered it. [generator.ascend] gives the level back, so what is counted is the depth on
// the stack. It must be charged at every point the recursion can turn, or the bound stops covering it.
// Refusing hands back a value of the right type and lets generation carry on to the next diagnostic.
func (g *generator) descend(pos source.Position) bool {
	if g.descent >= g.limits.descent {
		return g.refuse(pos, "nested too deeply; lowering descends at most %d levels, and a call is lowered "+
			"by generating the callee's body inside the caller's own descent, so an inlined chain spends "+
			"the levels of every function in it at once", g.limits.descent)
	}
	if g.lowered >= g.limits.lowered {
		return g.refuse(pos, "this program lowers to more than the %d constructs the compiler will generate; "+
			"a call is lowered by generating the callee's body at the site, so a function calling two others "+
			"that each do the same doubles what is generated at every level of the chain", g.limits.lowered)
	}
	g.lowered++
	g.descent++
	return true
}

// refuse reports one of the two bounds coming back and answers false, so that
// the caller hands back a value of the right type and generation unwinds. Only
// the first refusal is reported: everything under it is the same sentence about
// the same program.
func (g *generator) refuse(pos source.Position, format string, args ...any) bool {
	if !g.refused {
		g.refused = true
		g.errorf(pos, format, args...)
	}
	return false
}

func (g *generator) ascend() { g.descent-- }
