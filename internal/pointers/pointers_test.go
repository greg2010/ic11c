package pointers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/source"
	"tinygo.org/x/go-llvm"
)

// sourceFile is the name diagnostics are expected to carry. Debug locations
// hold a line and a column but no file, so the verifier restores it from
// Options and the cases check that it did.
const sourceFile = "pointer.c"

// accessLine is the line every case attaches to the load or store under test,
// so a diagnostic can be checked against a position rather than only a message.
const accessLine = 7

// dwarfLangC99 is LLVMDWARFSourceLanguageC99. go-llvm's DwarfLang constants
// carry raw DWARF values, which the LLVMDWARFSourceLanguage enum does not
// share, so the enum value is spelled out here.
const dwarfLangC99 = llvm.DwarfLang(11)

// fixture is one module under construction, holding the pieces every case
// needs. All of it is disposed when the running test returns.
type fixture struct {
	m     llvm.Module
	b     llvm.Builder
	i64   llvm.Type
	ptr   llvm.Type
	scope llvm.Metadata

	// di stays alive so a case can give a second function a debug scope of its
	// own, which LLVM requires of every instruction's location. finalize closes
	// it, and every case calls that before the module is read.
	di   *llvm.DIBuilder
	file llvm.Metadata
	sig  llvm.Metadata

	// fn is a function taking one i1, so a case can branch without inventing a
	// condition, and returning void.
	fn    llvm.Value
	cond  llvm.Value
	entry llvm.BasicBlock
}

func newFixture(t *testing.T, name string) *fixture {
	t.Helper()
	ctx := llvm.NewContext()
	m := ctx.NewModule(name)
	b := ctx.NewBuilder()
	version := ctx.MDNode([]llvm.Metadata{
		llvm.ConstInt(ctx.Int32Type(), 2, false).ConstantAsMetadata(),
		ctx.MDString("Debug Info Version"),
		llvm.ConstInt(ctx.Int32Type(), 3, false).ConstantAsMetadata(),
	})
	m.AddNamedMetadataOperand("llvm.module.flags", version)

	di := llvm.NewDIBuilder(m)
	t.Cleanup(func() {
		di.Destroy()
		b.Dispose()
		m.Dispose()
		ctx.Dispose()
	})
	di.CreateCompileUnit(llvm.DICompileUnit{
		Language: dwarfLangC99,
		File:     sourceFile,
		Dir:      ".",
		Producer: "ic11c",
	})
	file := di.CreateFile(sourceFile, ".")
	intType := di.CreateBasicType(llvm.DIBasicType{Name: "int", SizeInBits: 64, Encoding: llvm.DW_ATE_signed})
	sig := di.CreateSubroutineType(llvm.DISubroutineType{File: file, Parameters: []llvm.Metadata{intType}})

	fnType := llvm.FunctionType(ctx.VoidType(), []llvm.Type{ctx.Int1Type()}, false)
	fn := llvm.AddFunction(m, "main", fnType)
	scope := di.CreateFunction(file, llvm.DIFunction{
		Name:         "main",
		LinkageName:  "main",
		File:         file,
		Line:         1,
		Type:         sig,
		IsDefinition: true,
		ScopeLine:    1,
	})
	fn.SetSubprogram(scope)

	f := &fixture{
		m:     m,
		b:     b,
		i64:   ctx.Int64Type(),
		ptr:   llvm.PointerType(ctx.Int64Type(), 0),
		scope: scope,
		di:    di,
		file:  file,
		sig:   sig,
		fn:    fn,
		cond:  fn.Param(0),
		entry: llvm.AddBasicBlock(fn, "entry"),
	}
	b.SetInsertPointAtEnd(f.entry)
	f.at(1)
	return f
}

// at moves the debug location the next instructions carry to line.
func (f *fixture) at(line uint) {
	f.b.SetCurrentDebugLocation(line, 1, f.scope, llvm.Metadata{})
}

// finalize closes the debug info, which has to happen before the module is read
// or LLVM's own verifier finds unresolved temporary metadata.
func (f *fixture) finalize() { f.di.Finalize() }

// callee adds a second function taking one pointer and reading through it at
// [accessLine], with a debug scope of its own. The builder is left in its entry
// block with no terminator, so a case can add the recursive call the shape it
// is testing needs before ending the block and going back to [fixture.entry].
func (f *fixture) callee(name string) llvm.Value {
	fnType := llvm.FunctionType(f.m.Context().VoidType(), []llvm.Type{f.ptr}, false)
	fn := llvm.AddFunction(f.m, name, fnType)
	scope := f.di.CreateFunction(f.file, llvm.DIFunction{
		Name:         name,
		LinkageName:  name,
		File:         f.file,
		Line:         accessLine,
		Type:         f.sig,
		IsDefinition: true,
		ScopeLine:    accessLine,
	})
	fn.SetSubprogram(scope)

	f.b.SetInsertPointAtEnd(llvm.AddBasicBlock(fn, "entry"))
	f.b.SetCurrentDebugLocation(accessLine, 1, scope, llvm.Metadata{})
	f.b.CreateLoad(f.i64, fn.Param(0), "value")
	return fn
}

// setter adds a second function taking one pointer and writing value
// through it, with a debug scope of its own, and leaves the builder back
// in the fixture's entry block. It is [fixture.callee]'s counterpart: what
// a callee writes into an object is invisible from the caller's own instructions.
func (f *fixture) setter(name string, value llvm.Value) llvm.Value {
	fnType := llvm.FunctionType(f.m.Context().VoidType(), []llvm.Type{f.ptr}, false)
	fn := llvm.AddFunction(f.m, name, fnType)
	scope := f.di.CreateFunction(f.file, llvm.DIFunction{
		Name:         name,
		LinkageName:  name,
		File:         f.file,
		Line:         accessLine,
		Type:         f.sig,
		IsDefinition: true,
		ScopeLine:    accessLine,
	})
	fn.SetSubprogram(scope)

	f.b.SetInsertPointAtEnd(llvm.AddBasicBlock(fn, "entry"))
	f.b.SetCurrentDebugLocation(accessLine, 1, scope, llvm.Metadata{})
	f.b.CreateStore(value, fn.Param(0))
	f.b.CreateRetVoid()

	f.b.SetInsertPointAtEnd(f.entry)
	return fn
}

// wideCallee adds a second function of params pointer parameters, with a debug
// scope of its own, and leaves the builder in its entry block with no
// terminator. It is [fixture.callee] widened, for the cases that need a
// parameter no call site passes an argument for.
func (f *fixture) wideCallee(name string, params int) llvm.Value {
	types := make([]llvm.Type, params)
	for i := range types {
		types[i] = f.ptr
	}
	fn := llvm.AddFunction(f.m, name, llvm.FunctionType(f.m.Context().VoidType(), types, false))
	scope := f.di.CreateFunction(f.file, llvm.DIFunction{
		Name:         name,
		LinkageName:  name,
		File:         f.file,
		Line:         accessLine,
		Type:         f.sig,
		IsDefinition: true,
		ScopeLine:    accessLine,
	})
	fn.SetSubprogram(scope)

	f.b.SetInsertPointAtEnd(llvm.AddBasicBlock(fn, "entry"))
	f.b.SetCurrentDebugLocation(accessLine, 1, scope, llvm.Metadata{})
	return fn
}

// shortCall calls fn through a signature of its own carrying one type per
// argument, which is how a call passing fewer arguments than the callee defines
// is spelled: a call site holds its own function type and LLVM does not hold it
// against the callee's.
func (f *fixture) shortCall(fn llvm.Value, args ...llvm.Value) llvm.Value {
	types := make([]llvm.Type, len(args))
	for i := range types {
		types[i] = f.ptr
	}
	return f.b.CreateCall(llvm.FunctionType(f.m.Context().VoidType(), types, false), fn, args, "")
}

// globalPointer adds a global holding one pointer, initialized to target.
func (f *fixture) globalPointer(name string, target llvm.Value) llvm.Value {
	g := llvm.AddGlobal(f.m, f.ptr, name)
	g.SetInitializer(target)
	return g
}

func (f *fixture) global(name string) llvm.Value {
	g := llvm.AddGlobal(f.m, f.i64, name)
	g.SetInitializer(llvm.ConstInt(f.i64, 0, false))
	return g
}

// diverge branches on the fixture's condition and returns the two arms and the
// block they join at, each already ending in the branch that gets there.
func (f *fixture) diverge() (left, right, join llvm.BasicBlock) {
	left = llvm.AddBasicBlock(f.fn, "left")
	right = llvm.AddBasicBlock(f.fn, "right")
	join = llvm.AddBasicBlock(f.fn, "join")

	f.b.CreateCondBr(f.cond, left, right)
	f.b.SetInsertPointAtEnd(left)
	f.b.CreateBr(join)
	f.b.SetInsertPointAtEnd(right)
	f.b.CreateBr(join)
	f.b.SetInsertPointAtEnd(join)
	return left, right, join
}

// TestCheck builds each pointer shape directly rather than waiting for the
// optimizer to produce one, because what the verifier has to reject is defined
// by the backend's addressing and not by whichever shapes today's pipeline
// happens to emit.
func TestCheck(t *testing.T) {
	cases := []struct {
		name  string
		build func(f *fixture)
		// want is every fragment the diagnostic must carry. Empty means the
		// module is expected to pass.
		want []string
	}{
		{
			name: "a load and a store against their own objects",
			build: func(f *fixture) {
				slot := f.b.CreateAlloca(f.i64, "local")
				g := f.global("counter")
				f.at(accessLine)
				value := f.b.CreateLoad(f.i64, g, "value")
				f.b.CreateStore(value, slot)
				f.b.CreateRetVoid()
			},
		},
		{
			name: "an element of a global array",
			build: func(f *fixture) {
				arrType := llvm.ArrayType(f.i64, 4)
				table := llvm.AddGlobal(f.m, arrType, "table")
				table.SetInitializer(llvm.ConstNull(arrType))
				indices := []llvm.Value{llvm.ConstInt(f.i64, 0, false), llvm.ConstInt(f.i64, 2, false)}
				f.at(accessLine)
				elem := f.b.CreateInBoundsGEP(arrType, table, indices, "elem")
				f.b.CreateLoad(f.i64, elem, "value")
				f.b.CreateRetVoid()
			},
		},
		{
			name: "a phi whose arms are the same object",
			build: func(f *fixture) {
				slot := f.b.CreateAlloca(f.i64, "local")
				left, right, _ := f.diverge()
				merged := f.b.CreatePHI(f.ptr, "merged")
				merged.AddIncoming([]llvm.Value{slot, slot}, []llvm.BasicBlock{left, right})
				f.at(accessLine)
				f.b.CreateStore(llvm.ConstInt(f.i64, 1, false), merged)
				f.b.CreateRetVoid()
			},
		},
		{
			name: "a loop carried phi over one object",
			build: func(f *fixture) {
				slot := f.b.CreateAlloca(f.i64, "local")
				body := llvm.AddBasicBlock(f.fn, "body")
				exit := llvm.AddBasicBlock(f.fn, "exit")
				f.b.CreateBr(body)

				f.b.SetInsertPointAtEnd(body)
				carried := f.b.CreatePHI(f.ptr, "carried")
				f.at(accessLine)
				f.b.CreateStore(llvm.ConstInt(f.i64, 1, false), carried)
				f.b.CreateCondBr(f.cond, body, exit)
				carried.AddIncoming([]llvm.Value{slot, carried}, []llvm.BasicBlock{f.entry, body})

				f.b.SetInsertPointAtEnd(exit)
				f.b.CreateRetVoid()
			},
		},
		{
			name: "a phi merging two locals",
			build: func(f *fixture) {
				first := f.b.CreateAlloca(f.i64, "x")
				second := f.b.CreateAlloca(f.i64, "y")
				left, right, _ := f.diverge()
				merged := f.b.CreatePHI(f.ptr, "merged")
				merged.AddIncoming([]llvm.Value{first, second}, []llvm.BasicBlock{left, right})
				f.at(accessLine)
				f.b.CreateStore(llvm.ConstInt(f.i64, 1, false), merged)
				f.b.CreateRetVoid()
			},
			want: []string{"writes through a pointer", "merges x and y", "one named object"},
		},
		{
			name: "a select between two globals",
			build: func(f *fixture) {
				first := f.global("left")
				second := f.global("right")
				f.at(accessLine)
				chosen := f.b.CreateSelect(f.cond, first, second, "chosen")
				f.b.CreateLoad(f.i64, chosen, "value")
				f.b.CreateRetVoid()
			},
			want: []string{"reads through a pointer", "chooses between left and right"},
		},
		{
			name: "a select behind address arithmetic",
			build: func(f *fixture) {
				arrType := llvm.ArrayType(f.i64, 4)
				first := llvm.AddGlobal(f.m, arrType, "left")
				first.SetInitializer(llvm.ConstNull(arrType))
				second := llvm.AddGlobal(f.m, arrType, "right")
				second.SetInitializer(llvm.ConstNull(arrType))
				indices := []llvm.Value{llvm.ConstInt(f.i64, 0, false), llvm.ConstInt(f.i64, 1, false)}
				f.at(accessLine)
				chosen := f.b.CreateSelect(f.cond, first, second, "chosen")
				elem := f.b.CreateInBoundsGEP(arrType, chosen, indices, "elem")
				f.b.CreateLoad(f.i64, elem, "value")
				f.b.CreateRetVoid()
			},
			want: []string{"chooses between left and right"},
		},
		{
			// One arm reaches a value the other already resolved: the walk
			// marks a value once per access rather than once per path, so the
			// shared step contributes its object only to whichever arm reached
			// it first. The merge is still of two objects and must still be refused.
			name: "a merge whose arms share a step",
			build: func(f *fixture) {
				first := f.b.CreateAlloca(f.i64, "x")
				second := f.b.CreateAlloca(f.i64, "y")
				zero := []llvm.Value{llvm.ConstInt(f.i64, 0, false)}
				f.at(accessLine)
				toFirst := f.b.CreateInBoundsGEP(f.i64, first, zero, "tofirst")
				toSecond := f.b.CreateInBoundsGEP(f.i64, second, zero, "tosecond")
				inner := f.b.CreateSelect(f.cond, toFirst, toSecond, "inner")
				outer := f.b.CreateSelect(f.cond, toFirst, inner, "outer")
				f.b.CreateLoad(f.i64, outer, "value")
				f.b.CreateRetVoid()
			},
			want: []string{"reads through a pointer", "chooses between x and y"},
		},
		{
			name: "a pointer built out of an integer",
			build: func(f *fixture) {
				f.at(accessLine)
				address := f.b.CreateIntToPtr(llvm.ConstInt(f.i64, 64, false), f.ptr, "address")
				f.b.CreateLoad(f.i64, address, "value")
				f.b.CreateRetVoid()
			},
			want: []string{"does not name a local or a global"},
		},
		{
			// The shape every pointer has before SROA runs, which is what the
			// flag that skips the optimizer leaves behind.
			name: "a pointer held in a local of its own",
			build: func(f *fixture) {
				target := f.b.CreateAlloca(f.i64, "x")
				held := f.b.CreateAlloca(f.ptr, "p")
				f.b.CreateStore(target, held)
				f.at(accessLine)
				f.b.CreateLoad(f.i64, f.b.CreateLoad(f.ptr, held, "p.value"), "value")
				f.b.CreateRetVoid()
			},
		},
		{
			// 'p' takes part in a second store, at the position that says what
			// is written rather than where. Reading every store a pointer
			// appears in as a write into it puts 'p' in the list of what 'p'
			// holds, and the program is refused for naming two objects.
			name: "a pointer that is itself written into another pointer",
			build: func(f *fixture) {
				target := f.b.CreateAlloca(f.i64, "x")
				held := f.b.CreateAlloca(f.ptr, "p")
				indirect := f.b.CreateAlloca(f.ptr, "q")
				f.b.CreateStore(target, held)
				f.b.CreateStore(held, indirect)
				f.at(accessLine)
				f.b.CreateLoad(f.i64, f.b.CreateLoad(f.ptr, held, "p.value"), "value")
				f.b.CreateRetVoid()
			},
		},
		{
			name: "a pointer held in a local assigned two objects",
			build: func(f *fixture) {
				first := f.b.CreateAlloca(f.i64, "x")
				second := f.b.CreateAlloca(f.i64, "y")
				held := f.b.CreateAlloca(f.ptr, "p")
				f.b.CreateStore(first, held)
				f.b.CreateStore(second, held)
				f.at(accessLine)
				f.b.CreateLoad(f.i64, f.b.CreateLoad(f.ptr, held, "p.value"), "value")
				f.b.CreateRetVoid()
			},
			want: []string{"reads through a pointer", "is assigned x and y"},
		},
		{
			name: "a pointer held in a local nothing wrote",
			build: func(f *fixture) {
				held := f.b.CreateAlloca(f.ptr, "p")
				f.at(accessLine)
				f.b.CreateLoad(f.i64, f.b.CreateLoad(f.ptr, held, "p.value"), "value")
				f.b.CreateRetVoid()
			},
			want: []string{"does not name a local or a global"},
		},
		{
			name: "an element of an array of pointers",
			build: func(f *fixture) {
				target := f.global("counter")
				table := f.b.CreateAlloca(llvm.ArrayType(f.ptr, 2), "table")
				f.b.CreateStore(target, f.b.CreateInBoundsGEP(f.ptr, table, []llvm.Value{llvm.ConstInt(f.i64, 1, false)}, ""))
				f.at(accessLine)
				held := f.b.CreateLoad(f.ptr, f.b.CreateInBoundsGEP(f.ptr, table, []llvm.Value{llvm.ConstInt(f.i64, 0, false)}, ""), "held")
				f.b.CreateLoad(f.i64, held, "value")
				f.b.CreateRetVoid()
			},
		},
		{
			// A subscript at a constant index into a global folds to a
			// constant expression rather than an instruction, on the store as
			// well as on the load.
			name: "an element of a global array of pointers",
			build: func(f *fixture) {
				target := f.global("counter")
				arrType := llvm.ArrayType(f.ptr, 2)
				table := llvm.AddGlobal(f.m, arrType, "table")
				table.SetInitializer(llvm.ConstNull(arrType))
				at := []llvm.Value{llvm.ConstInt(f.i64, 0, false), llvm.ConstInt(f.i64, 1, false)}
				f.at(accessLine)
				f.b.CreateStore(target, f.b.CreateInBoundsGEP(arrType, table, at, ""))
				held := f.b.CreateLoad(f.ptr, f.b.CreateInBoundsGEP(arrType, table, at, ""), "held")
				f.b.CreateLoad(f.i64, held, "value")
				f.b.CreateRetVoid()
			},
		},
		{
			// An index position says nothing about what the pointer holds, and
			// a constant expression is the one address arithmetic that can put
			// a pointer there — LLVM checks a getelementptr instruction's
			// indices but not a constant expression's, which this case exploits.
			name: "a pointer reached as the index of a constant expression",
			build: func(f *fixture) {
				target := f.global("target")
				holder := f.globalPointer("holder", target)
				other := f.global("other")
				f.at(accessLine)
				f.b.CreateStore(llvm.ConstInt(f.i64, 1, false), f.b.CreateGEP(f.i64, other, []llvm.Value{holder}, ""))
				f.b.CreateLoad(f.i64, f.b.CreateLoad(f.ptr, holder, "held"), "value")
				f.b.CreateRetVoid()
			},
		},
		{
			// The shape a pointer walking an array of pointers takes once the
			// optimizer has run: the store's own pointer operand is the phi
			// carrying the walk, so the object it writes into is named nowhere
			// the store itself can be seen from.
			name: "a pointer assigned through a phi as well as directly",
			build: func(f *fixture) {
				first := f.b.CreateAlloca(f.i64, "x")
				second := f.b.CreateAlloca(f.i64, "y")
				held := f.b.CreateAlloca(f.ptr, "p")
				f.b.CreateStore(first, held)
				left, right, _ := f.diverge()
				merged := f.b.CreatePHI(f.ptr, "merged")
				merged.AddIncoming([]llvm.Value{held, held}, []llvm.BasicBlock{left, right})
				f.b.CreateStore(second, merged)
				f.at(accessLine)
				f.b.CreateLoad(f.i64, f.b.CreateLoad(f.ptr, held, "p.value"), "value")
				f.b.CreateRetVoid()
			},
			want: []string{"reads through a pointer", "is assigned x and y"},
		},
		{
			name: "a pointer assigned through a select as well as directly",
			build: func(f *fixture) {
				first := f.b.CreateAlloca(f.i64, "x")
				second := f.b.CreateAlloca(f.i64, "y")
				held := f.b.CreateAlloca(f.ptr, "p")
				f.b.CreateStore(first, held)
				chosen := f.b.CreateSelect(f.cond, held, held, "chosen")
				f.b.CreateStore(second, chosen)
				f.at(accessLine)
				f.b.CreateLoad(f.i64, f.b.CreateLoad(f.ptr, held, "p.value"), "value")
				f.b.CreateRetVoid()
			},
			want: []string{"reads through a pointer", "is assigned x and y"},
		},
		{
			// The walk over the writers has to terminate on the phi a loop
			// carries back into itself, which is the same phi the shape above
			// reaches the store through.
			name: "a pointer assigned through a loop carried phi",
			build: func(f *fixture) {
				first := f.b.CreateAlloca(f.i64, "x")
				second := f.b.CreateAlloca(f.i64, "y")
				held := f.b.CreateAlloca(f.ptr, "p")
				f.b.CreateStore(first, held)
				body := llvm.AddBasicBlock(f.fn, "body")
				exit := llvm.AddBasicBlock(f.fn, "exit")
				f.b.CreateBr(body)

				f.b.SetInsertPointAtEnd(body)
				carried := f.b.CreatePHI(f.ptr, "carried")
				f.b.CreateStore(second, carried)
				f.b.CreateCondBr(f.cond, body, exit)
				carried.AddIncoming([]llvm.Value{held, carried}, []llvm.BasicBlock{f.entry, body})

				f.b.SetInsertPointAtEnd(exit)
				f.at(accessLine)
				f.b.CreateLoad(f.i64, f.b.CreateLoad(f.ptr, held, "p.value"), "value")
				f.b.CreateRetVoid()
			},
			want: []string{"reads through a pointer", "is assigned x and y"},
		},
		{
			// The other side of the same walk: a merge is not a second object
			// by itself, so a pointer every writer gives the same object is
			// still one the backend addresses.
			name: "a pointer assigned one object through a phi and directly",
			build: func(f *fixture) {
				target := f.b.CreateAlloca(f.i64, "x")
				held := f.b.CreateAlloca(f.ptr, "p")
				f.b.CreateStore(target, held)
				left, right, _ := f.diverge()
				merged := f.b.CreatePHI(f.ptr, "merged")
				merged.AddIncoming([]llvm.Value{held, held}, []llvm.BasicBlock{left, right})
				f.b.CreateStore(target, merged)
				f.at(accessLine)
				f.b.CreateLoad(f.i64, f.b.CreateLoad(f.ptr, held, "p.value"), "value")
				f.b.CreateRetVoid()
			},
		},
		{
			name: "a pointer parameter every call site agrees about",
			build: func(f *fixture) {
				target := f.global("counter")
				callee := f.callee("readThrough")
				f.b.CreateRetVoid()

				f.b.SetInsertPointAtEnd(f.entry)
				f.at(accessLine)
				f.b.CreateCall(callee.GlobalValueType(), callee, []llvm.Value{target}, "")
				f.b.CreateCall(callee.GlobalValueType(), callee, []llvm.Value{target}, "")
				f.b.CreateRetVoid()
			},
		},
		{
			name: "a pointer parameter two call sites disagree about",
			build: func(f *fixture) {
				first := f.global("left")
				second := f.global("right")
				callee := f.callee("readThrough")
				f.b.CreateRetVoid()

				f.b.SetInsertPointAtEnd(f.entry)
				f.at(accessLine)
				f.b.CreateCall(callee.GlobalValueType(), callee, []llvm.Value{first}, "")
				f.b.CreateCall(callee.GlobalValueType(), callee, []llvm.Value{second}, "")
				f.b.CreateRetVoid()
			},
			want: []string{"reads through a pointer", "is passed left and right"},
		},
		{
			// Nothing calls it, so no argument says what the parameter holds
			// and there is no object behind the access. Treating no call site
			// as no disagreement would let this one shape through with
			// nothing to resolve against.
			name: "a pointer parameter of a function nothing calls",
			build: func(f *fixture) {
				f.callee("readThrough")
				f.b.CreateRetVoid()

				f.b.SetInsertPointAtEnd(f.entry)
				f.at(accessLine)
				f.b.CreateRetVoid()
			},
			want: []string{"reads through a pointer", "does not name a local or a global"},
		},
		{
			// What a callee writes through a pointer parameter is a write into
			// the caller's object, and nothing at the call site says so.
			// Reading only the caller's own stores leaves the pointer resolving
			// to whichever object those named.
			name: "a pointer a callee assigns through a parameter",
			build: func(f *fixture) {
				first := f.global("left")
				second := f.global("right")
				held := f.b.CreateAlloca(f.ptr, "p")
				f.b.CreateStore(first, held)
				assign := f.setter("assign", second)
				f.at(accessLine)
				f.b.CreateCall(assign.GlobalValueType(), assign, []llvm.Value{held}, "")
				f.b.CreateLoad(f.i64, f.b.CreateLoad(f.ptr, held, "p.value"), "value")
				f.b.CreateRetVoid()
			},
			want: []string{"reads through a pointer", "is assigned left and right"},
		},
		{
			// The other side of the same walk: a callee writing the object the
			// caller already wrote is not a second object.
			name: "a pointer a callee assigns the object it already held",
			build: func(f *fixture) {
				target := f.global("counter")
				held := f.b.CreateAlloca(f.ptr, "p")
				f.b.CreateStore(target, held)
				assign := f.setter("assign", target)
				f.at(accessLine)
				f.b.CreateCall(assign.GlobalValueType(), assign, []llvm.Value{held}, "")
				f.b.CreateLoad(f.i64, f.b.CreateLoad(f.ptr, held, "p.value"), "value")
				f.b.CreateRetVoid()
			},
		},
		{
			// A callee that only reads through the parameter writes nothing
			// into the object, so it contributes no second assignment.
			name: "a pointer a callee only reads through",
			build: func(f *fixture) {
				target := f.global("counter")
				held := f.b.CreateAlloca(f.ptr, "p")
				f.b.CreateStore(target, held)
				reader := f.callee("readThrough")
				f.b.CreateRetVoid()

				f.b.SetInsertPointAtEnd(f.entry)
				f.at(accessLine)
				f.b.CreateCall(reader.GlobalValueType(), reader, []llvm.Value{held}, "")
				f.b.CreateLoad(f.i64, f.b.CreateLoad(f.ptr, held, "p.value"), "value")
				f.b.CreateRetVoid()
			},
		},
		{
			// A callee with no body is one whose stores cannot be read, so
			// what the object holds afterwards is not decided here.
			name: "a pointer passed to a function with no body",
			build: func(f *fixture) {
				target := f.global("counter")
				held := f.b.CreateAlloca(f.ptr, "p")
				f.b.CreateStore(target, held)
				opaque := llvm.AddFunction(f.m, "__ic_opaque",
					llvm.FunctionType(f.m.Context().VoidType(), []llvm.Type{f.ptr}, false))
				f.at(accessLine)
				f.b.CreateCall(opaque.GlobalValueType(), opaque, []llvm.Value{held}, "")
				f.b.CreateLoad(f.i64, f.b.CreateLoad(f.ptr, held, "p.value"), "value")
				f.b.CreateRetVoid()
			},
			want: []string{"reads through a pointer", "does not name a local or a global"},
		},
		{
			// A global pointer's initializer is the only assignment a program
			// that never writes it makes. The restriction is over what the
			// backend can address, and an object named by an initializer is
			// addressed like one named by a store.
			name: "a global pointer with a static initializer",
			build: func(f *fixture) {
				arrType := llvm.ArrayType(f.i64, 4)
				table := llvm.AddGlobal(f.m, arrType, "table")
				table.SetInitializer(llvm.ConstNull(arrType))
				held := f.globalPointer("p", table)
				f.at(accessLine)
				f.b.CreateLoad(f.i64, f.b.CreateLoad(f.ptr, held, "p.value"), "value")
				f.b.CreateRetVoid()
			},
		},
		{
			name: "a global pointer initialized to one object and assigned another",
			build: func(f *fixture) {
				first := f.global("left")
				second := f.global("right")
				held := f.globalPointer("p", first)
				f.b.CreateStore(second, held)
				f.at(accessLine)
				f.b.CreateLoad(f.i64, f.b.CreateLoad(f.ptr, held, "p.value"), "value")
				f.b.CreateRetVoid()
			},
			want: []string{"reads through a pointer", "is assigned left and right"},
		},
		{
			// A null initializer designates no object, so it is not an
			// assignment and does not hide the store that follows it. Every
			// global LLVM defines carries one, so counting it would reject
			// every global pointer a program assigns at run time.
			name: "a global pointer left null and assigned at run time",
			build: func(f *fixture) {
				target := f.global("counter")
				held := f.globalPointer("p", llvm.ConstNull(f.ptr))
				f.b.CreateStore(target, held)
				f.at(accessLine)
				f.b.CreateLoad(f.i64, f.b.CreateLoad(f.ptr, held, "p.value"), "value")
				f.b.CreateRetVoid()
			},
		},
		{
			name: "a global pointer left null that nothing assigns",
			build: func(f *fixture) {
				held := f.globalPointer("p", llvm.ConstNull(f.ptr))
				f.at(accessLine)
				f.b.CreateLoad(f.i64, f.b.CreateLoad(f.ptr, held, "p.value"), "value")
				f.b.CreateRetVoid()
			},
			want: []string{"does not name a local or a global"},
		},
		{
			// A recursive function stepping its own pointer parameter along is
			// the shape microc.md admits, and the recursive call must not count
			// as a second object.
			name: "a recursive function passing its own pointer parameter on",
			build: func(f *fixture) {
				table := f.global("table")
				callee := f.callee("walk")
				stepped := f.b.CreateInBoundsGEP(f.i64, callee.Param(0), []llvm.Value{llvm.ConstInt(f.i64, 1, false)}, "")
				f.b.CreateCall(callee.GlobalValueType(), callee, []llvm.Value{stepped}, "")
				f.b.CreateRetVoid()

				f.b.SetInsertPointAtEnd(f.entry)
				f.at(accessLine)
				f.b.CreateCall(callee.GlobalValueType(), callee, []llvm.Value{table}, "")
				f.b.CreateRetVoid()
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "pointers")
			tc.build(f)
			f.finalize()
			if err := llvm.VerifyModule(f.m, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("the case built a module LLVM rejects: %v\n%s", err, f.m.String())
			}

			err := Check(context.Background(), f.m, Options{File: sourceFile})
			if len(tc.want) == 0 {
				if err != nil {
					t.Fatalf("a module whose pointers all resolve was rejected: %v\n%s", err, f.m.String())
				}
				return
			}
			if err == nil {
				t.Fatalf("the verifier accepted an unresolvable pointer:\n%s", f.m.String())
			}

			var diags source.DiagnosticList
			if !errors.As(err, &diags) {
				t.Fatalf("error is not a source.DiagnosticList: %T %v", err, err)
			}
			if len(diags) != 1 {
				t.Fatalf("diagnostic count = %d, want 1: %s", len(diags), diags.String())
			}
			if got := diags[0].Pos.Line; got != accessLine {
				t.Errorf("diagnostic line = %d, want %d: %s", got, accessLine, diags[0].Error())
			}
			if got := diags[0].Pos.File; got != sourceFile {
				t.Errorf("diagnostic file = %q, want %q", got, sourceFile)
			}
			for _, want := range tc.want {
				if !strings.Contains(diags[0].Msg, want) {
					t.Errorf("diagnostic does not mention %q: %s", want, diags[0].Msg)
				}
			}
		})
	}
}

// TestCheckReportsEveryAccess checks that one bad pointer does not hide the
// next, since a program reaching this restriction usually reaches it more than
// once and fixing them one build at a time is the experience worth avoiding.
func TestCheckReportsEveryAccess(t *testing.T) {
	f := newFixture(t, "several")
	first := f.b.CreateAlloca(f.i64, "x")
	second := f.b.CreateAlloca(f.i64, "y")
	left, right, _ := f.diverge()
	merged := f.b.CreatePHI(f.ptr, "merged")
	merged.AddIncoming([]llvm.Value{first, second}, []llvm.BasicBlock{left, right})

	f.at(accessLine)
	f.b.CreateStore(llvm.ConstInt(f.i64, 1, false), merged)
	f.at(accessLine + 3)
	f.b.CreateLoad(f.i64, merged, "value")
	f.b.CreateRetVoid()
	f.finalize()

	err := Check(context.Background(), f.m, Options{File: sourceFile})
	var diags source.DiagnosticList
	if !errors.As(err, &diags) {
		t.Fatalf("error is not a source.DiagnosticList: %T %v", err, err)
	}
	if len(diags) != 2 {
		t.Fatalf("diagnostic count = %d, want 2: %s", len(diags), diags.String())
	}
	for i, want := range []int{accessLine, accessLine + 3} {
		if got := diags[i].Pos.Line; got != want {
			t.Errorf("diagnostic %d is at line %d, want %d: %s", i, got, want, diags[i].Error())
		}
	}
}

// TestCheckOrdersDiagnosticsByPosition covers a module whose functions
// are laid out in an order the source lines do not follow: the walk goes
// function by module order, and the optimizer is free to lay a function
// out anywhere. Reporting in walk order would print line 9 before line 7.
func TestCheckOrdersDiagnosticsByPosition(t *testing.T) {
	f := newFixture(t, "ordering")
	// The entry point comes first in the module and holds the later line.
	first := f.b.CreateAlloca(f.i64, "x")
	second := f.b.CreateAlloca(f.i64, "y")
	chosen := f.b.CreateSelect(f.cond, first, second, "chosen")
	f.at(accessLine + 2)
	f.b.CreateLoad(f.i64, chosen, "value")
	f.b.CreateRetVoid()

	// A function no call site reaches, whose parameter therefore names no
	// object, laid out after the entry point and holding the earlier line.
	sink := f.wideCallee("sink", 1)
	f.b.CreateLoad(f.i64, sink.Param(0), "held")
	f.b.CreateRetVoid()
	f.finalize()

	if err := llvm.VerifyModule(f.m, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("the case built a module LLVM rejects: %v\n%s", err, f.m.String())
	}
	err := Check(context.Background(), f.m, Options{File: sourceFile})
	var diags source.DiagnosticList
	if !errors.As(err, &diags) {
		t.Fatalf("error is not a source.DiagnosticList: %T %v", err, err)
	}
	if len(diags) != 2 {
		t.Fatalf("diagnostic count = %d, want 2: %s", len(diags), diags.String())
	}
	for i, want := range []int{accessLine, accessLine + 2} {
		if got := diags[i].Pos.Line; got != want {
			t.Errorf("diagnostic %d is at line %d, want %d: %s", i, got, want, diags[i].Error())
		}
	}
}

// TestCheckFallsBackToTheFunctionForAnUnlocatedAccess covers the
// positions this package is most likely to meet, since it runs after
// optimization: a pointer phi or select the optimizer formed carries
// either no debug location or line 0, both rendering as a bare dash.
func TestCheckFallsBackToTheFunctionForAnUnlocatedAccess(t *testing.T) {
	cases := []struct {
		name string
		// access writes through merged with a location of the kind the
		// optimizer leaves behind.
		access func(f *fixture, merged llvm.Value)
	}{
		{
			name: "an access carrying no debug location",
			access: func(f *fixture, merged llvm.Value) {
				f.at(accessLine)
				store := f.b.CreateStore(llvm.ConstInt(f.i64, 1, false), merged)
				store.InstructionSetDebugLoc(llvm.Metadata{})
			},
		},
		{
			name: "an access whose debug location names line 0",
			access: func(f *fixture, merged llvm.Value) {
				f.at(0)
				f.b.CreateStore(llvm.ConstInt(f.i64, 1, false), merged)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "unlocated")
			first := f.b.CreateAlloca(f.i64, "x")
			second := f.b.CreateAlloca(f.i64, "y")
			left, right, _ := f.diverge()
			merged := f.b.CreatePHI(f.ptr, "merged")
			merged.AddIncoming([]llvm.Value{first, second}, []llvm.BasicBlock{left, right})

			tc.access(f, merged)
			f.at(accessLine)
			f.b.CreateRetVoid()
			f.finalize()

			err := Check(context.Background(), f.m, Options{File: sourceFile})
			var diags source.DiagnosticList
			if !errors.As(err, &diags) {
				t.Fatalf("error is not a source.DiagnosticList: %T %v", err, err)
			}
			if len(diags) != 1 {
				t.Fatalf("diagnostic count = %d, want 1: %s", len(diags), diags.String())
			}
			if !diags[0].Pos.IsValid() {
				t.Fatalf("the diagnostic names no place to open: %s", diags[0].Error())
			}
			if got := diags[0].Pos.File; got != sourceFile {
				t.Errorf("diagnostic file = %q, want %q", got, sourceFile)
			}
			// Line 1 is where the fixture opens the function, and so the first
			// line it still carries a location for.
			if got := diags[0].Pos.Line; got != 1 {
				t.Errorf("diagnostic line = %d, want the function's own line 1: %s", got, diags[0].Error())
			}
		})
	}
}

// TestCheckSkipsDeclarations checks that the device intrinsics, which are
// bodiless declarations, are not walked as if they had code.
func TestCheckSkipsDeclarations(t *testing.T) {
	f := newFixture(t, "declarations")
	llvm.AddFunction(f.m, "__ic_load", llvm.FunctionType(f.i64, []llvm.Type{f.i64, f.i64}, false))
	f.b.CreateRetVoid()
	f.finalize()

	if err := Check(context.Background(), f.m, Options{File: sourceFile}); err != nil {
		t.Fatalf("a module holding an intrinsic declaration was rejected: %v", err)
	}
}

// TestCheckReadsNoOperandPastACallsArguments covers the two walks that
// index a call's operands by a position in the callee's parameter list.
// A call may pass fewer arguments than the callee defines parameters
// for, so a parameter past the last argument has no operand to read.
func TestCheckReadsNoOperandPastACallsArguments(t *testing.T) {
	t.Run("a parameter no call site passes an argument for", func(t *testing.T) {
		f := newFixture(t, "past-args")
		sink := f.wideCallee("sink", 3)
		f.b.CreateLoad(f.i64, sink.Param(2), "value")
		f.b.CreateRetVoid()

		f.b.SetInsertPointAtEnd(f.entry)
		f.at(1)
		f.shortCall(sink, f.b.CreateAlloca(f.i64, "obj"))
		f.b.CreateRetVoid()
		f.finalize()

		if err := llvm.VerifyModule(f.m, llvm.ReturnStatusAction); err != nil {
			t.Fatalf("the case built a module LLVM rejects: %v\n%s", err, f.m.String())
		}
		err := Check(context.Background(), f.m, Options{File: sourceFile})
		var diags source.DiagnosticList
		if !errors.As(err, &diags) {
			t.Fatalf("error is not a source.DiagnosticList: %T %v", err, err)
		}
		if len(diags) != 1 {
			t.Fatalf("diagnostic count = %d, want 1: %s", len(diags), diags.String())
		}
		if !strings.Contains(diags[0].Msg, unresolved) {
			t.Errorf("diagnostic does not mention %q: %s", unresolved, diags[0].Msg)
		}
	})

	t.Run("a parameter at the position the callee operand occupies", func(t *testing.T) {
		// The short call passes two arguments, so the callee sits at
		// operand two and the parameter being resolved is the third. A
		// bound that admits that position reads the called function as an
		// argument, which resolves to neither a local nor a global.
		f := newFixture(t, "arg-at-the-bound")
		sink := f.wideCallee("sink", 3)
		f.b.CreateLoad(f.i64, sink.Param(2), "value")
		f.b.CreateRetVoid()

		f.b.SetInsertPointAtEnd(f.entry)
		f.at(1)
		obj := f.b.CreateAlloca(f.i64, "obj")
		f.shortCall(sink, obj, obj, obj)
		f.shortCall(sink, obj, obj)
		f.b.CreateRetVoid()
		f.finalize()

		if err := llvm.VerifyModule(f.m, llvm.ReturnStatusAction); err != nil {
			t.Fatalf("the case built a module LLVM rejects: %v\n%s", err, f.m.String())
		}
		if err := Check(context.Background(), f.m, Options{File: sourceFile}); err != nil {
			t.Fatalf("a module whose pointers all resolve was rejected: %v\n%s", err, f.m.String())
		}
	})

	t.Run("the function is passed as an argument elsewhere", func(t *testing.T) {
		// A call that mentions the function without calling it is in its
		// use list too, and its operands are another call's arguments.
		// Reading the parameter's position out of one would gather an
		// object this parameter never holds.
		f := newFixture(t, "callee-elsewhere")
		sink := f.wideCallee("sink", 1)
		f.b.CreateLoad(f.i64, sink.Param(0), "value")
		f.b.CreateRetVoid()
		other := f.wideCallee("other", 2)
		f.b.CreateRetVoid()

		f.b.SetInsertPointAtEnd(f.entry)
		f.at(1)
		obj := f.b.CreateAlloca(f.i64, "obj")
		f.shortCall(sink, obj)
		f.shortCall(other, f.global("target"), sink)
		f.b.CreateRetVoid()
		f.finalize()

		if err := llvm.VerifyModule(f.m, llvm.ReturnStatusAction); err != nil {
			t.Fatalf("the case built a module LLVM rejects: %v\n%s", err, f.m.String())
		}
		if err := Check(context.Background(), f.m, Options{File: sourceFile}); err != nil {
			t.Fatalf("a module whose pointers all resolve was rejected: %v\n%s", err, f.m.String())
		}
	})

	t.Run("an object handed to a callee with parameters past it", func(t *testing.T) {
		f := newFixture(t, "past-args-store")
		holder := f.globalPointer("holder", f.global("target"))
		sink := f.wideCallee("sink", 3)
		f.b.CreateRetVoid()

		f.b.SetInsertPointAtEnd(f.entry)
		f.at(1)
		f.shortCall(sink, holder)
		f.at(accessLine)
		f.b.CreateLoad(f.i64, f.b.CreateLoad(f.ptr, holder, "held"), "value")
		f.b.CreateRetVoid()
		f.finalize()

		if err := llvm.VerifyModule(f.m, llvm.ReturnStatusAction); err != nil {
			t.Fatalf("the case built a module LLVM rejects: %v\n%s", err, f.m.String())
		}
		if err := Check(context.Background(), f.m, Options{File: sourceFile}); err != nil {
			t.Fatalf("a module whose pointers all resolve was rejected: %v\n%s", err, f.m.String())
		}
	})
}

func TestCheckRejects(t *testing.T) {
	cases := []struct {
		name      string
		nilModule bool
		cancel    bool
		want      string
	}{
		{name: "a nil module", nilModule: true, want: "nil module"},
		{name: "a cancelled context", cancel: true, want: "context canceled"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m llvm.Module
			if !tc.nilModule {
				f := newFixture(t, "reject")
				f.b.CreateRetVoid()
				f.finalize()
				m = f.m
			}

			ctx := context.Background()
			if tc.cancel {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}

			err := Check(ctx, m, Options{File: sourceFile})
			if err == nil {
				t.Fatalf("Module accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not mention %q: %v", tc.want, err)
			}
		})
	}
}
