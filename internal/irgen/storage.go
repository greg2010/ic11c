package irgen

import (
	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/llvmir"
	"github.com/greg2010/ic11c/internal/sema"
	"tinygo.org/x/go-llvm"
)

// globals gives every file-scope object a module global. A const object whose
// initializer folded needs no storage: every reference to it becomes the
// constant.
func (g *generator) globals() {
	for _, sym := range g.prog.Globals {
		if sym.Value != nil {
			continue
		}
		typ, ok := g.llvmType(sym.Type)
		if !ok {
			continue
		}
		global := llvm.AddGlobal(g.result.Module, typ, sym.Name)
		global.SetInitializer(llvm.ConstNull(typ))
		global.SetLinkage(llvm.InternalLinkage)
		g.globalStorage[sym] = global
	}
	// A global initializer is a constant expression, but it is not a constant
	// initializer: chip memory survives power loss, so the value has to be
	// written at run time. It is written in declaration order, before any
	// statement of main runs.
	for _, sym := range g.prog.Globals {
		decl, ok := sym.Decl.(*ast.VarDecl)
		if !ok || decl.Init == nil {
			continue
		}
		slot, ok := g.globalStorage[sym]
		if !ok {
			continue
		}
		g.setLoc(decl.Pos())
		g.initialize(sym, slot, decl.Init)
	}
}

// initialize writes a declaration's initializer into the storage it names. A brace initializer
// supplies elements from index zero; nothing writes the elements past the last one supplied, since
// the entry prologue's clr db has already zeroed all [ic10.NumMemorySlots] slots and re-zeroing here
// would cost a poke apiece against the byte budget.
func (g *generator) initialize(sym *sema.Symbol, storage llvm.Value, init ast.Expr) {
	list, isList := init.(*ast.InitListExpr)
	if !isList {
		g.builder.CreateStore(g.value(init), storage)
		return
	}
	if sym.Type.Kind() != sema.Array {
		// Analysis admits exactly one value in a brace initializer for a scalar,
		// so anything past the first would be a second store to one address.
		if len(list.Elems) > 0 {
			g.builder.CreateStore(g.value(list.Elems[0]), storage)
		}
		return
	}
	elem, ok := g.llvmType(sym.Type.Elem())
	if !ok {
		g.errorf(list.Lbrace, "'%s' has element type %s, which is not lowered", sym.Name, sym.Type.Elem())
		return
	}
	for i, value := range list.Elems {
		g.setLoc(value.Pos())
		lowered := g.value(value)
		at := g.builder.CreateInBoundsGEP(elem, storage, []llvm.Value{g.constInt(int64(i))}, "")
		g.builder.CreateStore(lowered, at)
	}
}

// zeroUnwritten writes zero into the storage a local declaration leaves unwritten: the whole of an
// object with no initializer, and the elements past the last one a brace initializer supplied. The
// machine's entry prologue already zeroes all [ic10.NumMemorySlots] slots, but the IR must state it
// too, or LLVM treats the alloca as undef and can fold it away — including the device stores under a
// comparison against it. The stores go above the entry block's allocas rather than at the
// declaration, so a data-region local re-entered by a loop holds the last iteration's value, not zero.
func (g *generator) zeroUnwritten(sym *sema.Symbol, storage llvm.Value, init ast.Expr) {
	if sym.Kind != sema.LocalVar || !sym.InDataRegion() || !storage.IsAGlobalVariable().IsNil() {
		return
	}
	elem, count := sym.Type, int64(1)
	if sym.Type.Kind() == sema.Array {
		elem, count = sym.Type.Elem(), sym.Type.Len()
	}
	written := int64(0)
	switch init := init.(type) {
	case nil:
	case *ast.InitListExpr:
		written = int64(len(init.Elems))
	default:
		// A scalar initializer writes the whole object.
		written = count
	}
	if written >= count {
		return
	}
	typ, ok := g.llvmType(elem)
	if !ok {
		// The declaration's own type is reported by the caller that reserved
		// the storage this writes into.
		return
	}
	builder := g.afterAllocas()
	zero := llvm.ConstNull(typ)
	for i := written; i < count; i++ {
		at := storage
		if sym.Type.Kind() == sema.Array {
			at = builder.CreateInBoundsGEP(typ, storage, []llvm.Value{g.constInt(i)}, "")
		}
		builder.CreateStore(zero, at)
	}
}

// afterAllocas positions the alloca builder below the entry block's allocas, the first point an
// instruction may use one, since every alloca is created at the top of that block.
func (g *generator) afterAllocas() llvm.Builder {
	entry := g.fn.EntryBasicBlock()
	for in := range llvmir.BlockInstrs(entry) {
		if in.IsAAllocaInst().IsNil() {
			g.allocaBuilder.SetInsertPointBefore(in)
			return g.allocaBuilder
		}
	}
	g.allocaBuilder.SetInsertPointAtEnd(entry)
	return g.allocaBuilder
}

// alloca reserves storage at the top of the entry block, where mem2reg expects
// to find it.
func (g *generator) alloca(typ llvm.Type, name string) llvm.Value {
	entry := g.fn.EntryBasicBlock()
	if first := entry.FirstInstruction(); !first.IsNil() {
		g.allocaBuilder.SetInsertPointBefore(first)
	} else {
		g.allocaBuilder.SetInsertPointAtEnd(entry)
	}
	return g.allocaBuilder.CreateAlloca(typ, name)
}

// storageOf returns the storage sym lives in, creating it for a local on first use in the definition
// being generated. It reports false for a symbol that has none: a const object whose initializer
// folded to a value, or one whose type did not check. Only the entry point gives a data-region local
// a module global instead of an alloca, since a global states the machine's zeroing in its
// initializer for free where an alloca costs a store per element; every other definition is compiled
// out of line and needs storage per activation, which a shared global would deny it.
func (g *generator) storageOf(sym *sema.Symbol) (llvm.Value, bool) {
	if slot, ok := g.globalStorage[sym]; ok {
		return slot, true
	}
	switch sym.Kind {
	case sema.LocalVar, sema.ParamVar:
	case sema.GlobalVar, sema.FuncName:
		// A global with storage was found above; one that was not has none,
		// and a function name never does.
		return llvm.Value{}, false
	}
	if slot, ok := g.locals[sym]; ok {
		return slot, true
	}
	typ, ok := g.llvmType(sym.Type)
	if !ok {
		return llvm.Value{}, false
	}
	slot := g.reserve(sym, typ)
	g.locals[sym] = slot
	return slot, true
}

// reserve gives one local its storage: a module global stating the entry prologue's zero in its
// initializer, or an alloca otherwise. Only an array takes the global, since it is the object that
// costs a store per element to zero and that the optimizer cannot keep in registers past a
// non-constant subscript; a scalar gets an alloca so the optimizer can still promote it out of memory entirely.
func (g *generator) reserve(sym *sema.Symbol, typ llvm.Type) llvm.Value {
	if !g.entryPoint || sym.Type.Kind() != sema.Array {
		return g.alloca(typ, sym.Name)
	}
	global := llvm.AddGlobal(g.result.Module, typ, sym.Name)
	global.SetInitializer(llvm.ConstNull(typ))
	global.SetLinkage(llvm.InternalLinkage)
	return global
}
