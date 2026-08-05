package llvmopt

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/corpus"
	"github.com/greg2010/ic11c/internal/irgen"
	"github.com/greg2010/ic11c/internal/llvmir"
	"github.com/greg2010/ic11c/internal/sema"
	"github.com/greg2010/ic11c/internal/source"
	"github.com/greg2010/ic11c/internal/tsparse"
	"tinygo.org/x/go-llvm"
)

// newModule builds an empty module owned by the running test.
func newModule(t *testing.T, name string) llvm.Module {
	t.Helper()
	ctx := llvm.NewContext()
	m := ctx.NewModule(name)
	t.Cleanup(func() {
		m.Dispose()
		ctx.Dispose()
	})
	return m
}

// addTrivialFunction gives m one function the pipeline can chew on, so a case
// exercises the pipeline rather than an empty module.
func addTrivialFunction(t *testing.T, m llvm.Module) {
	t.Helper()
	i64 := m.Context().Int64Type()
	fn := llvm.AddFunction(m, "trivial", llvm.FunctionType(i64, nil, false))
	builder := m.Context().NewBuilder()
	defer builder.Dispose()
	builder.SetInsertPointAtEnd(llvm.AddBasicBlock(fn, "entry"))
	builder.CreateRet(llvm.ConstInt(i64, 1, false))
}

// addBrokenFunction gives m a function LLVM's own verifier rejects: a block
// with no terminator.
func addBrokenFunction(t *testing.T, m llvm.Module) {
	t.Helper()
	fn := llvm.AddFunction(m, "broken", llvm.FunctionType(m.Context().VoidType(), nil, false))
	llvm.AddBasicBlock(fn, "entry")
}

func TestRunRejects(t *testing.T) {
	cases := []struct {
		name string
		// build populates the module. A nil build leaves it empty.
		build func(t *testing.T, m llvm.Module)
		// nilModule passes the zero module rather than a built one.
		nilModule bool
		cancel    bool
		pipeline  string
		want      string
	}{
		{
			name:      "a nil module",
			nilModule: true,
			want:      "nil module",
		},
		{
			name:   "a cancelled context",
			build:  addTrivialFunction,
			cancel: true,
			want:   "context canceled",
		},
		{
			name:     "a pipeline the pass builder cannot parse",
			build:    addTrivialFunction,
			pipeline: "no-such-pass",
			want:     "no-such-pass",
		},
		{
			name:  "a module that does not verify going in",
			build: addBrokenFunction,
			want:  "does not verify before optimization",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m llvm.Module
			if !tc.nilModule {
				m = newModule(t, "reject")
				if tc.build != nil {
					tc.build(t, m)
				}
			}

			ctx := context.Background()
			if tc.cancel {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}

			err := Run(ctx, m, Options{Pipeline: tc.pipeline})
			if err == nil {
				t.Fatalf("Run accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not mention %q: %v", tc.want, err)
			}
		})
	}
}

// addInstructions gives m a function holding exactly n instructions: n-1
// additions over its parameter and the return terminating the block. Nothing
// reads the additions, so the pipeline disposes of them in one walk and a case
// sitting at the cap does not cost what the cap is there to bound.
func addInstructions(t *testing.T, m llvm.Module, name string, n int) llvm.Value {
	t.Helper()
	if n < 1 {
		t.Fatalf("a function holds at least its terminator, not %d instructions", n)
	}
	ctx := m.Context()
	f64 := ctx.DoubleType()
	fn := llvm.AddFunction(m, name, llvm.FunctionType(ctx.VoidType(), []llvm.Type{f64}, false))
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	builder.SetInsertPointAtEnd(llvm.AddBasicBlock(fn, "entry"))
	one := llvm.ConstFloat(f64, 1)
	acc := fn.Param(0)
	for range n - 1 {
		acc = builder.CreateFAdd(acc, one, "")
	}
	builder.CreateRetVoid()
	return fn
}

// countInstructions returns what [maxInstructions] caps, computed independently
// of the guard so a case states the size it built rather than trusting it.
func countInstructions(m llvm.Module) int {
	var n int
	for range llvmir.ModuleInstrs(m) {
		n++
	}
	return n
}

// TestRunRefusesAModuleOverTheInstructionCap pins the cap to the
// instruction either side of it, which is the only pair that states where
// it is. The two-function case makes the cap a property of the module
// rather than of any one function: neither half is over it and the module is.
func TestRunRefusesAModuleOverTheInstructionCap(t *testing.T) {
	half := maxInstructions/2 + 1
	cases := []struct {
		name string
		// sizes gives the instruction count of each function in the module.
		sizes   []int
		refused bool
	}{
		{
			name:  "a module holding nothing but a return",
			sizes: []int{1},
		},
		{
			name:  "one instruction under the cap",
			sizes: []int{maxInstructions - 1},
		},
		{
			name:  "the cap itself",
			sizes: []int{maxInstructions},
		},
		{
			name:    "one instruction over the cap",
			sizes:   []int{maxInstructions + 1},
			refused: true,
		},
		{
			name:    "two functions, each of them under the cap",
			sizes:   []int{half, half},
			refused: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newModule(t, "cap")
			for i, size := range tc.sizes {
				addInstructions(t, m, "fn"+strconv.Itoa(i), size)
			}
			built := countInstructions(m)

			err := Run(context.Background(), m, Options{})
			if !tc.refused {
				if err != nil {
					t.Fatalf("Run refused a module of %d instructions: %v", built, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Run accepted a module of %d instructions, past the cap of %d", built, maxInstructions)
			}
			for _, want := range []string{strconv.Itoa(built), strconv.Itoa(maxInstructions)} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not state %s: %v", want, err)
				}
			}
		})
	}
}

// locatedFunctions gives m the debug information a source position is read out
// of, and one function per entry of sizes: the function at index i holds
// sizes[i] instructions and is declared at line i+1 of file.
func locatedFunctions(t *testing.T, m llvm.Module, file string, sizes ...int) {
	t.Helper()
	ctx := m.Context()
	// LLVM discards debug info that is not declared at version 3.
	m.AddNamedMetadataOperand("llvm.module.flags", ctx.MDNode([]llvm.Metadata{
		llvm.ConstInt(ctx.Int32Type(), 2, false).ConstantAsMetadata(),
		ctx.MDString("Debug Info Version"),
		llvm.ConstInt(ctx.Int32Type(), 3, false).ConstantAsMetadata(),
	}))

	di := llvm.NewDIBuilder(m)
	defer di.Destroy()
	const dir = "/ic11c"
	di.CreateCompileUnit(llvm.DICompileUnit{
		Language:  dwarfLangC99,
		File:      file,
		Dir:       dir,
		Producer:  "ic11c",
		Optimized: true,
	})
	scope := di.CreateFile(file, dir)
	sig := di.CreateSubroutineType(llvm.DISubroutineType{File: scope})
	for i, size := range sizes {
		name := "fn" + strconv.Itoa(i)
		fn := addInstructions(t, m, name, size)
		fn.SetSubprogram(di.CreateFunction(scope, llvm.DIFunction{
			Name:         name,
			LinkageName:  name,
			File:         scope,
			Line:         i + 1,
			Type:         sig,
			IsDefinition: true,
			ScopeLine:    i + 1,
			Optimized:    true,
		}))
	}
	di.Finalize()
}

// TestRunBlamesTheLargestFunctionForARefusal checks the position the refusal
// carries. The largest function is declared second so that a refusal reporting
// whichever function it reached first would name the wrong line.
func TestRunBlamesTheLargestFunctionForARefusal(t *testing.T) {
	const file = "oversize.c"
	m := newModule(t, "oversize")
	locatedFunctions(t, m, file, 1, maxInstructions)
	built := countInstructions(m)

	err := Run(context.Background(), m, Options{})
	if err == nil {
		t.Fatalf("Run accepted a module of %d instructions, past the cap of %d", built, maxInstructions)
	}
	diags, ok := errors.AsType[source.DiagnosticList](err)
	if !ok {
		t.Fatalf("the refusal is not a diagnostic list, so nothing renders its position: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("the refusal carries %d diagnostics, want the one naming the largest function: %v", len(diags), diags)
	}
	want := source.Position{File: file, Line: 2}
	if diags[0].Pos != want {
		t.Errorf("the refusal is positioned at %v, want %v", diags[0].Pos, want)
	}
}

// corpusModule lowers one fixture as far as the optimizer's input, which is the
// module [maxInstructions] is measured against.
func corpusModule(t *testing.T, program corpus.Program) *irgen.Result {
	t.Helper()
	name := program.Name
	file, diags, err := tsparse.Parse(name, program.Source)
	if err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}
	if diags.HasErrors() {
		t.Fatalf("parsing %s: %v", name, diags.Error())
	}
	prog, diags, err := sema.Analyze(t.Context(), file, sema.Shipped{})
	if err != nil {
		t.Fatalf("analyzing %s: %v", name, err)
	}
	if diags.HasErrors() {
		t.Fatalf("analyzing %s: %v", name, diags.Error())
	}
	res, err := irgen.Generate(t.Context(), prog, irgen.Options{ModuleName: name})
	if err != nil {
		t.Fatalf("generating IR for %s: %v", name, err)
	}
	return res
}

// TestTheCorpusIsFarUnderTheInstructionCap recomputes the headroom
// [maxInstructions] leaves over real programs, caught here rather than
// by a program refused in the field. The largest fixture emits within a
// dozen lines of the chip's 128.
func TestTheCorpusIsFarUnderTheInstructionCap(t *testing.T) {
	// minHeadroom is what the cap must keep over the largest real program:
	// two orders of magnitude, since the cap is set by what the optimizer
	// costs in memory, not by what a program is expected to reach.
	const minHeadroom = 100

	programs, err := corpus.Programs()
	if err != nil {
		t.Fatalf("loading the corpus: %v", err)
	}

	var largest int
	var largestName string
	for _, program := range programs {
		res := corpusModule(t, program)
		n := countInstructions(res.Module)
		res.Dispose()
		if n > largest {
			largest, largestName = n, program.Name
		}
	}

	t.Logf("the largest of the %d fixtures is %s at %d instructions, %d times under the cap of %d",
		len(programs), largestName, largest, maxInstructions/largest, maxInstructions)
	if largest*minHeadroom > maxInstructions {
		t.Errorf("%s reaches %d instructions and the cap is %d, which is under the %d times headroom a real program is owed",
			largestName, largest, maxInstructions, minHeadroom)
	}
}

// addCountedLoop gives m a loop over a constant trip count whose body calls an
// opaque declaration, which is a body the unroller may replicate and no other
// pass may delete.
func addCountedLoop(t *testing.T, m llvm.Module, trip int) {
	t.Helper()
	ctx := m.Context()
	i64 := ctx.Int64Type()
	sink := llvm.AddFunction(m, "sink", llvm.FunctionType(ctx.VoidType(), []llvm.Type{i64}, false))
	fn := llvm.AddFunction(m, "counted", llvm.FunctionType(ctx.VoidType(), nil, false))

	entry := llvm.AddBasicBlock(fn, "entry")
	body := llvm.AddBasicBlock(fn, "body")
	exit := llvm.AddBasicBlock(fn, "exit")

	builder := ctx.NewBuilder()
	defer builder.Dispose()

	builder.SetInsertPointAtEnd(entry)
	builder.CreateBr(body)

	builder.SetInsertPointAtEnd(body)
	i := builder.CreatePHI(i64, "i")
	builder.CreateCall(sink.GlobalValueType(), sink, []llvm.Value{i}, "")
	next := builder.CreateAdd(i, llvm.ConstInt(i64, 1, false), "next")
	more := builder.CreateICmp(llvm.IntSLT, next, llvm.ConstInt(i64, uint64(trip), false), "more")
	builder.CreateCondBr(more, body, exit)
	i.AddIncoming([]llvm.Value{llvm.ConstInt(i64, 0, false), next}, []llvm.BasicBlock{entry, body})

	builder.SetInsertPointAtEnd(exit)
	builder.CreateRetVoid()
}

// countCalls returns how many call instructions the module holds, which is the
// trip count once a loop is fully unrolled and one while it is not.
func countCalls(m llvm.Module) int {
	var calls int
	for _, in := range llvmir.ModuleInstrs(m) {
		if in.InstructionOpcode() == llvm.Call {
			calls++
		}
	}
	return calls
}

// TestRunUnrollsOnlyWhenAsked pins the deviation the shipped configuration
// takes to what it does, in both directions. The zero value has to be the
// configuration the compiler runs, or a corpus figure measured through it
// describes a pipeline nothing ships.
func TestRunUnrollsOnlyWhenAsked(t *testing.T) {
	const trip = 4
	cases := []struct {
		name      string
		unrolling bool
		// wantCalls is how many copies of the body survive.
		wantCalls int
	}{
		{name: "the shipped configuration leaves the loop rolled", wantCalls: 1},
		{name: "unrolling replicates the body per iteration", unrolling: true, wantCalls: trip},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newModule(t, "unroll")
			addCountedLoop(t, m, trip)
			if err := Run(context.Background(), m, Options{LoopUnrolling: tc.unrolling}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := countCalls(m); got != tc.wantCalls {
				t.Errorf("the body survives %d times, want %d:\n%s", got, tc.wantCalls, m.String())
			}
		})
	}
}

// TestRunDefaultsThePipeline checks that an empty Options runs the pipeline the
// compiler ships rather than nothing at all, which would make every size
// measurement taken through Run meaningless.
func TestRunDefaultsThePipeline(t *testing.T) {
	m := newModule(t, "defaulted")
	i64 := m.Context().Int64Type()

	fn := llvm.AddFunction(m, "promote", llvm.FunctionType(i64, []llvm.Type{i64}, false))
	builder := m.Context().NewBuilder()
	defer builder.Dispose()
	builder.SetInsertPointAtEnd(llvm.AddBasicBlock(fn, "entry"))
	slot := builder.CreateAlloca(i64, "slot")
	builder.CreateStore(fn.Param(0), slot)
	builder.CreateRet(builder.CreateLoad(i64, slot, "value"))

	if err := Run(context.Background(), m, Options{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for in := range llvmir.FuncInstrs(fn) {
		if op := in.InstructionOpcode(); op == llvm.Alloca || op == llvm.Load {
			t.Errorf("a %v survived the default pipeline, which therefore did not run mem2reg:\n%s", op, m.String())
		}
	}
}

// countOpcode returns how many instructions of one opcode a function holds.
func countOpcode(fn llvm.Value, op llvm.Opcode) int {
	var n int
	for in := range llvmir.FuncInstrs(fn) {
		if in.InstructionOpcode() == op {
			n++
		}
	}
	return n
}

// addDroppedArgumentCall builds the shape dead argument elimination
// leaves behind: an internal callee that never reads its second
// parameter, and a caller that computes that argument via an opaque
// call it cannot delete. The callee is noinline, so it is not removed first.
func addDroppedArgumentCall(t *testing.T, m llvm.Module) (caller llvm.Value) {
	t.Helper()
	ctx := m.Context()
	f64 := ctx.DoubleType()

	source := llvm.AddFunction(m, "source", llvm.FunctionType(f64, nil, false))
	sink := llvm.AddFunction(m, "sink", llvm.FunctionType(f64, []llvm.Type{f64}, false))
	callee := llvm.AddFunction(m, "callee", llvm.FunctionType(f64, []llvm.Type{f64, f64}, false))
	callee.SetLinkage(llvm.InternalLinkage)
	callee.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("noinline"), 0))
	caller = llvm.AddFunction(m, "caller", llvm.FunctionType(f64, nil, false))

	builder := ctx.NewBuilder()
	defer builder.Dispose()

	builder.SetInsertPointAtEnd(llvm.AddBasicBlock(callee, "entry"))
	builder.CreateRet(builder.CreateCall(sink.GlobalValueType(), sink, []llvm.Value{callee.Param(0)}, ""))

	builder.SetInsertPointAtEnd(llvm.AddBasicBlock(caller, "entry"))
	kept := builder.CreateCall(source.GlobalValueType(), source, nil, "kept")
	dropped := builder.CreateFAdd(kept, llvm.ConstFloat(f64, 1), "dropped")
	builder.CreateRet(builder.CreateCall(callee.GlobalValueType(), callee, []llvm.Value{kept, dropped}, ""))
	return caller
}

// TestRunCleansUpAfterDeadArgumentElimination pins the trailing dce to
// what it is there for: without it, the argument the caller computed for
// a dropped parameter is emitted and never read. The opaque call
// feeding it has to survive, or the case would pass on a deleted caller.
func TestRunCleansUpAfterDeadArgumentElimination(t *testing.T) {
	m := newModule(t, "deadarg")
	caller := addDroppedArgumentCall(t, m)

	if err := Run(context.Background(), m, Options{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := countOpcode(caller, llvm.FAdd); got != 0 {
		t.Errorf("the caller keeps %d computation(s) of the dropped argument:\n%s", got, m.String())
	}
	if got := countOpcode(caller, llvm.Call); got != 2 {
		t.Errorf("the caller holds %d calls, want the opaque one and the call to the callee:\n%s", got, m.String())
	}
}

// irgenLayout is the layout irgen puts on every module it builds, read off one
// rather than spelled again: two of the cases below turn on the layout, and a
// module built directly against this package carries whatever its builder gave
// it.
func irgenLayout(t *testing.T) string {
	t.Helper()
	return lowered(t, "void main(void) { }").DataLayout()
}

// lowered compiles one source as far as the optimizer's input.
func lowered(t *testing.T, src string) llvm.Module {
	t.Helper()
	file, diags, err := tsparse.Parse("test.c", src)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if diags.HasErrors() {
		t.Fatalf("parsing:\n%s", diags)
	}
	prog, diags, err := sema.Analyze(t.Context(), file, sema.Shipped{})
	if err != nil {
		t.Fatalf("analyzing: %v", err)
	}
	if diags.HasErrors() {
		t.Fatalf("analyzing:\n%s", diags)
	}
	res, err := irgen.Generate(t.Context(), prog, irgen.Options{})
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	t.Cleanup(res.Dispose)
	return res.Module
}

// TestAFaultingCallNothingReadsIsDeleted pins what irgen's attribute
// set for a faulting instruction buys: withholding speculatable keeps
// the call from running early, and LLVM's dead-call predicate then
// deletes it once unread. The design rationale lives on irgen's faulting field.
func TestAFaultingCallNothingReadsIsDeleted(t *testing.T) {
	const src = `long long g;
void main(void) {
    long long unread = g << 1;
    __ic_store(d0, Setting, g);
}`
	m := lowered(t, src)
	if before := strings.Count(m.String(), "@__ic_shl"); before < 2 {
		t.Fatalf("the module holds no call to the shift to begin with:\n%s", m.String())
	}
	if err := Run(context.Background(), m, Options{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(m.String(), "@__ic_shl") {
		t.Errorf("the shift survives the pipeline, so the attribute set does more than the comment on it claims:\n%s",
			m.String())
	}
}

// switchTableCases are case values no arithmetic reproduces. A run of them in
// arithmetic progression is folded to that arithmetic before any lookup table is
// considered, which would leave the case below passing on a module holding no
// switch at all.
var switchTableCases = []int{17, 3, 91, 5, 42, 8, 13, 77, 2, 64, 31, 9, 55, 20, 6, 88}

// addDenseSwitch gives m the one shape the transform the shipped spelling
// withholds applies to: a switch over consecutive labels, each answering a
// constant, merged through a phi.
func addDenseSwitch(t *testing.T, m llvm.Module) {
	t.Helper()
	ctx := m.Context()
	i64 := ctx.Int64Type()
	fn := llvm.AddFunction(m, "dense", llvm.FunctionType(i64, []llvm.Type{i64}, false))
	builder := ctx.NewBuilder()
	defer builder.Dispose()

	entry := llvm.AddBasicBlock(fn, "entry")
	deflt := llvm.AddBasicBlock(fn, "default")
	join := llvm.AddBasicBlock(fn, "join")
	arms := make([]llvm.BasicBlock, len(switchTableCases))
	values := make([]llvm.Value, 0, len(arms)+1)
	blocks := make([]llvm.BasicBlock, 0, len(arms)+1)

	builder.SetInsertPointAtEnd(entry)
	sw := builder.CreateSwitch(fn.Param(0), deflt, len(arms))
	for i := range arms {
		arms[i] = llvm.AddBasicBlock(fn, "arm")
		sw.AddCase(llvm.ConstInt(i64, uint64(i), false), arms[i])
		builder.SetInsertPointAtEnd(arms[i])
		builder.CreateBr(join)
		values = append(values, llvm.ConstInt(i64, uint64(switchTableCases[i]), false))
		blocks = append(blocks, arms[i])
	}
	builder.SetInsertPointAtEnd(deflt)
	builder.CreateBr(join)
	values = append(values, llvm.ConstInt(i64, 0, false))
	blocks = append(blocks, deflt)

	builder.SetInsertPointAtEnd(join)
	phi := builder.CreatePHI(i64, "r")
	phi.AddIncoming(values, blocks)
	builder.CreateRet(phi)
}

// TestTheSpellingWithholdsTheSwitchLookupTable covers why the pipeline
// stops short of default<Oz>: a lookup table costs more here than the
// branches it replaces, since get takes its address operand in a
// register.
func TestTheSpellingWithholdsTheSwitchLookupTable(t *testing.T) {
	tests := []struct {
		name     string
		pipeline string
		table    bool
	}{
		{name: "the shipped spelling", pipeline: DefaultPipeline},
		{name: "the pipeline it stops short of", pipeline: "default<Oz>", table: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModule(t, "dense")
			// The transform asks the layout whether the element type is legal, so
			// a module without one answers no for a reason that is not the
			// spelling.
			m.SetDataLayout(irgenLayout(t))
			addDenseSwitch(t, m)
			if err := Run(context.Background(), m, Options{Pipeline: tt.pipeline}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := strings.Contains(m.String(), "switch.table"); got != tt.table {
				t.Errorf("built a lookup table = %v, want %v:\n%s", got, tt.table, m.String())
			}
		})
	}
}

// addVectorizableLoop gives m a counted loop over an array, which is the shape
// both vectorizers exist for.
func addVectorizableLoop(t *testing.T, m llvm.Module) {
	t.Helper()
	ctx := m.Context()
	i64 := ctx.Int64Type()
	ptr := llvm.PointerType(i64, 0)
	fn := llvm.AddFunction(m, "bump", llvm.FunctionType(ctx.VoidType(), []llvm.Type{ptr, i64}, false))
	builder := ctx.NewBuilder()
	defer builder.Dispose()

	entry := llvm.AddBasicBlock(fn, "entry")
	body := llvm.AddBasicBlock(fn, "body")
	exit := llvm.AddBasicBlock(fn, "exit")

	builder.SetInsertPointAtEnd(entry)
	builder.CreateBr(body)

	builder.SetInsertPointAtEnd(body)
	index := builder.CreatePHI(i64, "i")
	at := builder.CreateInBoundsGEP(i64, fn.Param(0), []llvm.Value{index}, "")
	builder.CreateStore(builder.CreateAdd(builder.CreateLoad(i64, at, ""), llvm.ConstInt(i64, 1, false), ""), at)
	next := builder.CreateAdd(index, llvm.ConstInt(i64, 1, false), "next")
	builder.CreateCondBr(builder.CreateICmp(llvm.IntSLT, next, fn.Param(1), ""), body, exit)
	index.AddIncoming([]llvm.Value{llvm.ConstInt(i64, 0, false), next}, []llvm.BasicBlock{entry, body})

	builder.SetInsertPointAtEnd(exit)
	builder.CreateRetVoid()
}

// TestNoLevelVectorizesWithoutATargetMachine covers the claim that
// choosing Oz is not what withholds vectorization: with no target
// registered, the cost model reports no vector register and both
// vectorizers rewrite nothing at any level.
func TestNoLevelVectorizesWithoutATargetMachine(t *testing.T) {
	for _, pipeline := range []string{
		DefaultPipeline,
		"default<Oz>",
		"default<O3>",
		"function(loop-vectorize,slp-vectorizer)",
	} {
		t.Run(pipeline, func(t *testing.T) {
			m := newModule(t, "vector")
			m.SetDataLayout(irgenLayout(t))
			addVectorizableLoop(t, m)
			if err := Run(context.Background(), m, Options{Pipeline: pipeline}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if text := m.String(); strings.Contains(text, " x i64>") {
				t.Errorf("the pipeline built a vector, so no target machine is not what withholds one:\n%s", text)
			}
		})
	}
}
