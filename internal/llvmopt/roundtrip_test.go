package llvmopt

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/llvmir"
	"tinygo.org/x/go-llvm"
)

// supportedLLVMMajors are the libLLVM major versions the compiler is built
// and tested against. The go-llvm build tag only steers include and link
// paths, so the linked version is asserted here rather than assumed.
var supportedLLVMMajors = []string{"21", "22"}

// dwarfLangC99 is LLVMDWARFSourceLanguageC99. go-llvm's DwarfLang constants
// carry raw DWARF values, which the LLVMDWARFSourceLanguage enum does not
// share, so the enum value is spelled out here.
const dwarfLangC99 = llvm.DwarfLang(11)

// module bundles the LLVM state a case builds into. All of it is owned by the
// running subtest and disposed when the subtest returns.
type module struct {
	ctx     llvm.Context
	m       llvm.Module
	builder llvm.Builder
	i64     llvm.Type
}

// TestOptPipeline checks the architectural assumptions the compiler rests
// on: modules of the shapes irgen produces build, verify, survive the Oz
// pipeline in process, and can be read back off the optimized instructions.
// Debug locations are load-bearing, since backend diagnostics need the line.
func TestOptPipeline(t *testing.T) {
	t.Logf("linked LLVM version: %s", llvm.Version)
	major, _, _ := strings.Cut(llvm.Version, ".")
	if !slices.Contains(supportedLLVMMajors, major) {
		t.Fatalf("linked LLVM major version %q is outside the supported set %v (llvm.Version = %q)",
			major, supportedLLVMMajors, llvm.Version)
	}

	cases := []struct {
		name  string
		build func(mod *module)
		check func(t *testing.T, m llvm.Module)
	}{
		{"const_ret_i64", buildConstRet, checkConstRet},
		{"arith_nsw", buildArithNSW, checkArithNSW},
		{"loop_phi_alloca", buildLoopPhiAlloca, checkLoopPhiAlloca},
		{"gep_array_i64", buildGEPArray, checkGEPArray},
		{"extern_call", buildExternCall, checkExternCall},
		{"debug_info", buildDebugInfo, checkDebugInfo},
		{"const_fold", buildConstFold, checkConstFold},
		{"dead_code", buildDeadCode, checkDeadCode},
		{"conditional_over_two_objects", buildConditionalStores, checkConditionalStores},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := llvm.NewContext()
			defer ctx.Dispose()
			m := ctx.NewModule(tc.name)
			defer m.Dispose()
			builder := ctx.NewBuilder()
			defer builder.Dispose()

			mod := &module{ctx: ctx, m: m, builder: builder, i64: ctx.Int64Type()}
			tc.build(mod)
			generated := m.String()

			// Run verifies the module either side of the pipeline, so a case
			// that built something malformed and a pipeline that broke
			// something both arrive here as an error.
			if err := Run(context.Background(), m, Options{}); err != nil {
				t.Fatalf("%v\n--- module ---\n%s", err, generated)
			}

			tc.check(t, m)
			if t.Failed() {
				t.Logf("--- generated module ---\n%s\n--- optimized module ---\n%s", generated, m.String())
			}
		})
	}
}

// namedFunction returns the definition of fn, failing when the optimizer
// removed it or left it a bare declaration.
func namedFunction(t *testing.T, m llvm.Module, name string) llvm.Value {
	t.Helper()
	fn := m.NamedFunction(name)
	if fn.IsNil() {
		t.Fatalf("function %q is missing from the optimized module", name)
	}
	if fn.FirstBasicBlock().IsNil() {
		t.Fatalf("function %q lost its body", name)
	}
	return fn
}

// instructions returns every instruction in fn in block order.
func instructions(fn llvm.Value) []llvm.Value {
	return slices.Collect(llvmir.FuncInstrs(fn))
}

// opcodes counts instructions by opcode across fn.
func opcodes(fn llvm.Value) map[llvm.Opcode]int {
	counts := make(map[llvm.Opcode]int)
	for _, inst := range instructions(fn) {
		counts[inst.InstructionOpcode()]++
	}
	return counts
}

func buildConstRet(mod *module) {
	fn := llvm.AddFunction(mod.m, "ret_const", llvm.FunctionType(mod.i64, nil, false))
	mod.builder.SetInsertPointAtEnd(llvm.AddBasicBlock(fn, "entry"))
	mod.builder.CreateRet(llvm.ConstInt(mod.i64, 42, false))
}

func checkConstRet(t *testing.T, m llvm.Module) {
	fn := namedFunction(t, m, "ret_const")
	ret := fn.FirstBasicBlock().FirstInstruction()
	if got := ret.InstructionOpcode(); got != llvm.Ret {
		t.Fatalf("first instruction opcode = %v, want Ret", got)
	}
	val := ret.Operand(0)
	if val.IsAConstantInt().IsNil() {
		t.Fatalf("return value is not a constant integer: %s", val.String())
	}
	if got := val.SExtValue(); got != 42 {
		t.Errorf("returned constant = %d, want 42", got)
	}
}

func buildArithNSW(mod *module) {
	fnType := llvm.FunctionType(mod.i64, []llvm.Type{mod.i64, mod.i64}, false)
	fn := llvm.AddFunction(mod.m, "arith", fnType)
	x, y := fn.Param(0), fn.Param(1)
	mod.builder.SetInsertPointAtEnd(llvm.AddBasicBlock(fn, "entry"))

	add := mod.builder.CreateNSWAdd(x, y, "add")
	sub := mod.builder.CreateNSWSub(add, llvm.ConstInt(mod.i64, 7, false), "sub")
	mod.builder.CreateRet(mod.builder.CreateNSWMul(sub, y, "mul"))
}

// checkArithNSW asserts nsw threads through the pipeline, not that every
// instruction keeps it: reassociation drops the flag wherever it cannot
// prove the rewritten form overflow-free, leaving it only on the multiply
// here. It is read off the printed instruction; the bindings expose no accessor.
func checkArithNSW(t *testing.T, m llvm.Module) {
	fn := namedFunction(t, m, "arith")

	arithmetic, flagged := 0, 0
	for _, inst := range instructions(fn) {
		if op := inst.InstructionOpcode(); op != llvm.Add && op != llvm.Sub && op != llvm.Mul {
			continue
		}
		arithmetic++
		if strings.Contains(inst.String(), " nsw ") {
			flagged++
		}
	}
	if arithmetic == 0 {
		t.Fatalf("no integer arithmetic survived %s", DefaultPipeline)
	}
	if flagged == 0 {
		t.Errorf("nsw was stripped from all %d arithmetic instruction(s)", arithmetic)
	}
}

// buildLoopPhiAlloca produces the shape SROA and mem2reg are expected to
// transform: an accumulator in an alloca updated across a counted loop that
// already carries an induction phi.
func buildLoopPhiAlloca(mod *module) {
	fn := llvm.AddFunction(mod.m, "sum_to", llvm.FunctionType(mod.i64, []llvm.Type{mod.i64}, false))
	n := fn.Param(0)

	entry := llvm.AddBasicBlock(fn, "entry")
	header := llvm.AddBasicBlock(fn, "header")
	body := llvm.AddBasicBlock(fn, "body")
	exit := llvm.AddBasicBlock(fn, "exit")

	b := mod.builder
	zero := llvm.ConstInt(mod.i64, 0, false)

	b.SetInsertPointAtEnd(entry)
	acc := b.CreateAlloca(mod.i64, "acc")
	b.CreateStore(zero, acc)
	b.CreateBr(header)

	b.SetInsertPointAtEnd(header)
	iv := b.CreatePHI(mod.i64, "iv")
	b.CreateCondBr(b.CreateICmp(llvm.IntSLT, iv, n, "cmp"), body, exit)

	b.SetInsertPointAtEnd(body)
	cur := b.CreateLoad(mod.i64, acc, "cur")
	b.CreateStore(b.CreateNSWAdd(cur, iv, "next"), acc)
	ivNext := b.CreateNSWAdd(iv, llvm.ConstInt(mod.i64, 1, false), "iv.next")
	b.CreateBr(header)
	iv.AddIncoming([]llvm.Value{zero, ivNext}, []llvm.BasicBlock{entry, body})

	b.SetInsertPointAtEnd(exit)
	b.CreateRet(b.CreateLoad(mod.i64, acc, "result"))
}

func checkLoopPhiAlloca(t *testing.T, m llvm.Module) {
	fn := namedFunction(t, m, "sum_to")
	counts := opcodes(fn)
	if counts[llvm.Alloca] != 0 {
		t.Errorf("%d alloca(s) survived %s; the accumulator was not promoted to a register",
			counts[llvm.Alloca], DefaultPipeline)
	}
	if counts[llvm.Load] != 0 {
		t.Errorf("%d load(s) survived %s; the accumulator was not promoted to a register",
			counts[llvm.Load], DefaultPipeline)
	}
}

func buildGEPArray(mod *module) {
	arrType := llvm.ArrayType(mod.i64, 8)
	table := llvm.AddGlobal(mod.m, arrType, "table")
	table.SetInitializer(llvm.ConstNull(arrType))

	fn := llvm.AddFunction(mod.m, "table_get", llvm.FunctionType(mod.i64, []llvm.Type{mod.i64}, false))
	mod.builder.SetInsertPointAtEnd(llvm.AddBasicBlock(fn, "entry"))
	indices := []llvm.Value{llvm.ConstInt(mod.i64, 0, false), fn.Param(0)}
	elem := mod.builder.CreateInBoundsGEP(arrType, table, indices, "elem")
	mod.builder.CreateRet(mod.builder.CreateLoad(mod.i64, elem, "value"))
}

func checkGEPArray(t *testing.T, m llvm.Module) {
	fn := namedFunction(t, m, "table_get")
	counts := opcodes(fn)
	if counts[llvm.GetElementPtr] != 1 {
		t.Errorf("getelementptr count = %d, want 1", counts[llvm.GetElementPtr])
	}
	if counts[llvm.Load] != 1 {
		t.Errorf("load count = %d, want 1", counts[llvm.Load])
	}
	for _, inst := range instructions(fn) {
		if inst.InstructionOpcode() != llvm.Load {
			continue
		}
		if got := inst.Type(); got.TypeKind() != llvm.IntegerTypeKind || got.IntTypeWidth() != 64 {
			t.Errorf("loaded type = %s, want i64", got.String())
		}
	}
}

// buildExternCall models a device intrinsic: an attribute-free declaration the
// optimizer must treat as opaque and may not delete.
func buildExternCall(mod *module) {
	intrinsicType := llvm.FunctionType(mod.i64, []llvm.Type{mod.i64}, false)
	intrinsic := llvm.AddFunction(mod.m, "ic10_load", intrinsicType)

	fn := llvm.AddFunction(mod.m, "read_device", llvm.FunctionType(mod.i64, nil, false))
	mod.builder.SetInsertPointAtEnd(llvm.AddBasicBlock(fn, "entry"))
	args := []llvm.Value{llvm.ConstInt(mod.i64, 3, false)}
	mod.builder.CreateRet(mod.builder.CreateCall(intrinsicType, intrinsic, args, "value"))
}

func checkExternCall(t *testing.T, m llvm.Module) {
	fn := namedFunction(t, m, "read_device")

	var calls []llvm.Value
	for _, inst := range instructions(fn) {
		if inst.InstructionOpcode() == llvm.Call {
			calls = append(calls, inst)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("call count = %d, want 1; an attribute-free callee must not be removed", len(calls))
	}

	call := calls[0]
	if got := call.CalledValue().Name(); got != "ic10_load" {
		t.Errorf("callee = %q, want %q", got, "ic10_load")
	}
	arg := call.Operand(0)
	if arg.IsAConstantInt().IsNil() {
		t.Fatalf("call argument is not a constant integer: %s", arg.String())
	}
	if got := arg.SExtValue(); got != 3 {
		t.Errorf("call argument = %d, want 3", got)
	}
}

// dbgLines are the source lines buildDebugInfo attaches, in instruction order.
var dbgLines = []uint{2, 3}

// buildDebugInfo attaches a subprogram and !dbg line locations, the threading
// the compiler depends on to attribute backend errors to source lines.
func buildDebugInfo(mod *module) {
	// LLVM discards debug info that is not declared at version 3.
	version := mod.ctx.MDNode([]llvm.Metadata{
		llvm.ConstInt(mod.ctx.Int32Type(), 2, false).ConstantAsMetadata(),
		mod.ctx.MDString("Debug Info Version"),
		llvm.ConstInt(mod.ctx.Int32Type(), 3, false).ConstantAsMetadata(),
	})
	mod.m.AddNamedMetadataOperand("llvm.module.flags", version)

	di := llvm.NewDIBuilder(mod.m)
	defer di.Destroy()

	di.CreateCompileUnit(llvm.DICompileUnit{
		Language:  dwarfLangC99,
		File:      "debug.c",
		Dir:       "/ic11c",
		Producer:  "ic11c",
		Optimized: true,
	})
	file := di.CreateFile("debug.c", "/ic11c")
	intType := di.CreateBasicType(llvm.DIBasicType{
		Name:       "int",
		SizeInBits: 64,
		Encoding:   llvm.DW_ATE_signed,
	})
	sigType := di.CreateSubroutineType(llvm.DISubroutineType{
		File:       file,
		Parameters: []llvm.Metadata{intType, intType},
	})

	fn := llvm.AddFunction(mod.m, "dbg_fn", llvm.FunctionType(mod.i64, []llvm.Type{mod.i64}, false))
	scope := di.CreateFunction(file, llvm.DIFunction{
		Name:         "dbg_fn",
		LinkageName:  "dbg_fn",
		File:         file,
		Line:         1,
		Type:         sigType,
		IsDefinition: true,
		ScopeLine:    1,
		Optimized:    true,
	})
	fn.SetSubprogram(scope)

	b := mod.builder
	b.SetInsertPointAtEnd(llvm.AddBasicBlock(fn, "entry"))
	b.SetCurrentDebugLocation(dbgLines[0], 3, scope, llvm.Metadata{})
	add := b.CreateNSWAdd(fn.Param(0), llvm.ConstInt(mod.i64, 1, false), "add")
	b.SetCurrentDebugLocation(dbgLines[1], 3, scope, llvm.Metadata{})
	b.CreateRet(add)

	di.Finalize()
}

func checkDebugInfo(t *testing.T, m llvm.Module) {
	fn := namedFunction(t, m, "dbg_fn")

	var lines []uint
	for _, inst := range instructions(fn) {
		loc := inst.InstructionDebugLoc()
		if loc.IsNil() {
			t.Errorf("instruction lost its debug location: %s", inst.String())
			continue
		}
		lines = append(lines, loc.LocationLine())
	}
	if !slices.Equal(lines, dbgLines) {
		t.Errorf("debug lines = %v, want %v", lines, dbgLines)
	}
}

// buildConstFold chains arithmetic over literals, which is what a MicroC
// program built from const objects lowers to once every reference to one has
// become its value.
func buildConstFold(mod *module) {
	fn := llvm.AddFunction(mod.m, "fold", llvm.FunctionType(mod.i64, nil, false))
	mod.builder.SetInsertPointAtEnd(llvm.AddBasicBlock(fn, "entry"))

	sum := mod.builder.CreateNSWAdd(llvm.ConstInt(mod.i64, 6, false), llvm.ConstInt(mod.i64, 7, false), "sum")
	product := mod.builder.CreateNSWMul(sum, llvm.ConstInt(mod.i64, 3, false), "product")
	mod.builder.CreateRet(mod.builder.CreateNSWSub(product, llvm.ConstInt(mod.i64, 9, false), "diff"))
}

func checkConstFold(t *testing.T, m llvm.Module) {
	fn := namedFunction(t, m, "fold")
	counts := opcodes(fn)
	for _, op := range []llvm.Opcode{llvm.Add, llvm.Sub, llvm.Mul} {
		if counts[op] != 0 {
			t.Errorf("%d %v instruction(s) survived; the expression is entirely literal", counts[op], op)
		}
	}
	val := fn.FirstBasicBlock().FirstInstruction().Operand(0)
	if val.IsAConstantInt().IsNil() {
		t.Fatalf("return value is not a constant integer: %s", val.String())
	}
	if got := val.SExtValue(); got != 30 {
		t.Errorf("folded constant = %d, want 30", got)
	}
}

// buildDeadCode produces both shapes the optimizer has to remove: a computation
// nothing reads, and a block no edge can reach because its guard is false for
// every input.
func buildDeadCode(mod *module) {
	fn := llvm.AddFunction(mod.m, "dead", llvm.FunctionType(mod.i64, []llvm.Type{mod.i64}, false))
	n := fn.Param(0)

	entry := llvm.AddBasicBlock(fn, "entry")
	never := llvm.AddBasicBlock(fn, "never")
	live := llvm.AddBasicBlock(fn, "live")

	b := mod.builder
	b.SetInsertPointAtEnd(entry)
	b.CreateNSWMul(n, n, "unused")
	b.CreateCondBr(b.CreateICmp(llvm.IntSLT, n, n, "impossible"), never, live)

	b.SetInsertPointAtEnd(never)
	b.CreateRet(llvm.ConstInt(mod.i64, 999, false))

	b.SetInsertPointAtEnd(live)
	b.CreateRet(n)
}

func checkDeadCode(t *testing.T, m llvm.Module) {
	fn := namedFunction(t, m, "dead")
	counts := opcodes(fn)
	if counts[llvm.Mul] != 0 {
		t.Errorf("%d multiply instruction(s) survived, and nothing reads the product", counts[llvm.Mul])
	}
	if counts[llvm.ICmp] != 0 {
		t.Errorf("%d comparison(s) survived, and the predicate is false for every input", counts[llvm.ICmp])
	}
	if counts[llvm.Ret] != 1 {
		t.Errorf("return count = %d, want 1; the unreachable arm was not removed", counts[llvm.Ret])
	}
}

// buildConditionalStores is the shape a sunk pointer select would come out
// of: two objects written on opposite branch arms with the same value, so
// the pointer operand is the only difference between the stores. The
// globals are external, since an internal one nothing reads gets deleted first.
func buildConditionalStores(mod *module) {
	arrType := llvm.ArrayType(mod.i64, 4)
	first := llvm.AddGlobal(mod.m, arrType, "obj_a")
	first.SetInitializer(llvm.ConstNull(arrType))
	second := llvm.AddGlobal(mod.m, arrType, "obj_b")
	second.SetInitializer(llvm.ConstNull(arrType))

	fnType := llvm.FunctionType(mod.ctx.VoidType(), []llvm.Type{mod.ctx.Int1Type(), mod.i64}, false)
	fn := llvm.AddFunction(mod.m, "pick", fnType)
	cond, value := fn.Param(0), fn.Param(1)

	entry := llvm.AddBasicBlock(fn, "entry")
	useFirst := llvm.AddBasicBlock(fn, "use.first")
	useSecond := llvm.AddBasicBlock(fn, "use.second")
	join := llvm.AddBasicBlock(fn, "join")

	b := mod.builder
	indices := []llvm.Value{llvm.ConstInt(mod.i64, 0, false), llvm.ConstInt(mod.i64, 1, false)}

	b.SetInsertPointAtEnd(entry)
	b.CreateCondBr(cond, useFirst, useSecond)

	b.SetInsertPointAtEnd(useFirst)
	b.CreateStore(value, b.CreateInBoundsGEP(arrType, first, indices, "slot.a"))
	b.CreateBr(join)

	b.SetInsertPointAtEnd(useSecond)
	b.CreateStore(value, b.CreateInBoundsGEP(arrType, second, indices, "slot.b"))
	b.CreateBr(join)

	b.SetInsertPointAtEnd(join)
	b.CreateRetVoid()
}

// checkConditionalStores holds one shape the pipeline leaves alone; it
// is not evidence the optimizer declines to merge pointers — it does,
// and [internal/pointers] is what catches the result. A pipeline change
// that starts sinking these fails this case.
func checkConditionalStores(t *testing.T, m llvm.Module) {
	fn := namedFunction(t, m, "pick")

	stores := 0
	for _, inst := range instructions(fn) {
		// Default is the rule: this counts stores and refuses pointer merges,
		// and every other instruction is beside the question.
		//exhaustive:ignore
		switch inst.InstructionOpcode() {
		case llvm.Store:
			stores++
		case llvm.PHI, llvm.Select:
			if inst.Type().TypeKind() == llvm.PointerTypeKind {
				t.Errorf("the pipeline manufactured a pointer merge: %s", inst.String())
			}
		default:
		}
	}
	if stores != 2 {
		t.Errorf("store count = %d, want 2; the case no longer asks the question it was written to ask", stores)
	}
}
