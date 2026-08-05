package irgen

import (
	"github.com/greg2010/ic11c/internal/sema"
	"tinygo.org/x/go-llvm"
)

// definition is one function this stage emits as its own LLVM function: the source function, the
// value it lowered to, and the debug scope every instruction in it is attributed to. A scope apiece
// is not a choice — LLVM requires an instruction's !dbg location scoped to the subprogram of the
// function holding it, so a shared scope would fail its verifier.
type definition struct {
	fn    *sema.Func
	value llvm.Value
	scope llvm.Metadata
}

// outOfLine reports whether a function is compiled as a real call rather than spliced into every
// site that names it. Recursion is the case with no alternative, since a body cannot be inlined into
// itself; everything else is inlined unless [Options.OutOfLineCallSites] raises the bar. A function
// taking a dev parameter is never outlined: the argument is substituted per site, so a shared body has nowhere to put it.
func (g *generator) outOfLine(fn *sema.Func, sites map[*sema.Func]int) bool {
	if fn.Recursive {
		return true
	}
	// A threshold is a preference where recursion is a necessity, so a function
	// it picks whose signature cannot be formed is inlined rather than
	// reported. A dev parameter is that case, and is why no count reaches such
	// a function.
	return g.outlineSites > 0 && sites[fn] >= g.outlineSites && g.lowerable(fn)
}

// lowerable reports whether a signature exists for fn, leaving the diagnostic
// to [generator.signature] for the caller that has no alternative to one.
func (g *generator) lowerable(fn *sema.Func) bool {
	if fn.Result.Kind() != sema.Void {
		if _, ok := g.llvmType(fn.Result); !ok {
			return false
		}
	}
	for _, param := range fn.Params {
		if _, ok := g.llvmType(param.Type); !ok {
			return false
		}
	}
	return true
}

// callSites counts the call expressions naming each function. A site is syntactic: a call inside a
// body spliced into three places is one site here and three in the emitted program.
func callSites(prog *sema.Program) map[*sema.Func]int {
	sites := make(map[*sema.Func]int, len(prog.Funcs))
	for _, fn := range prog.Calls {
		sites[fn]++
	}
	return sites
}

// declareFunctions creates the entry point and every out-of-line definition before any body is
// generated, so a call can name a function whose body comes later, which mutual recursion always
// does. Only a function main can reach gets a definition, and only the entry point is external:
// nothing links against a MicroC program, so the whole call graph is in front of the interprocedural passes.
func (g *generator) declareFunctions() {
	g.declare(g.prog.Main, llvm.FunctionType(g.result.Context.VoidType(), nil, false))
	reachable := reachableFrom(g.prog.Main)
	var sites map[*sema.Func]int
	if g.outlineSites > 0 {
		sites = callSites(g.prog)
	}
	for _, fn := range g.prog.Funcs {
		if fn == g.prog.Main || !reachable[fn] || !g.outOfLine(fn, sites) || fn.Decl == nil || fn.Decl.Body == nil {
			continue
		}
		signature, ok := g.signature(fn)
		if !ok {
			continue
		}
		def := g.declare(fn, signature)
		def.value.SetLinkage(llvm.InternalLinkage)
		// noinline keeps the choice between a call and an expansion this
		// stage's: the optimizer's inliner weighs bytes on a conventional
		// machine, not bytes of IC10 text.
		def.value.AddFunctionAttr(g.noInline)
		g.outlined[fn] = def.value
	}
}

// reachableFrom collects the functions calls can reach from entry.
func reachableFrom(entry *sema.Func) map[*sema.Func]bool {
	reached := map[*sema.Func]bool{entry: true}
	queue := []*sema.Func{entry}
	for len(queue) > 0 {
		fn := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, callee := range fn.Callees {
			if reached[callee] {
				continue
			}
			reached[callee] = true
			queue = append(queue, callee)
		}
	}
	return reached
}

// declare creates one function and the debug scope its instructions are attributed to. no-builtins
// withholds LLVM's library-call recognition, since there is no C library on the chip: a pass that
// turns a run of zeroing stores into a memset call emits something instruction selection would have
// to reject. It says nothing about the calls this stage emits itself, which are the machine's own instructions.
func (g *generator) declare(fn *sema.Func, signature llvm.Type) definition {
	value := llvm.AddFunction(g.result.Module, fn.Name, signature)
	value.AddFunctionAttr(g.noBuiltins)
	scope := g.di.CreateFunction(g.file, llvm.DIFunction{
		Name:         fn.Name,
		LinkageName:  fn.Name,
		File:         g.file,
		Line:         fn.Pos.Line,
		Type:         g.subroutine,
		IsDefinition: true,
		ScopeLine:    fn.Pos.Line,
		Optimized:    true,
	})
	value.SetSubprogram(scope)
	def := definition{fn: fn, value: value, scope: scope}
	g.definitions = append(g.definitions, def)
	return def
}

// signature gives the LLVM function type a real call goes through, reporting a
// parameter or result the machine has no representation for.
func (g *generator) signature(fn *sema.Func) (llvm.Type, bool) {
	result := g.result.Context.VoidType()
	if fn.Result.Kind() != sema.Void {
		typ, ok := g.llvmType(fn.Result)
		if !ok {
			g.errorf(fn.Pos, "'%s' returns %s, which is not lowered", fn.Name, fn.Result)
			return llvm.Type{}, false
		}
		result = typ
	}
	params := make([]llvm.Type, len(fn.Params))
	for i, param := range fn.Params {
		typ, ok := g.llvmType(param.Type)
		if !ok {
			g.errorf(param.Pos, "the parameter '%s' has type %s, which is not lowered", param.Name, param.Type)
			return llvm.Type{}, false
		}
		params[i] = typ
	}
	return llvm.FunctionType(result, params, false), true
}

// define generates one function body. The entry point additionally carries the
// global initializers, which run before any statement of it.
func (g *generator) define(def definition) {
	g.fn, g.scope = def.value, def.scope
	g.entryPoint = def.fn == g.prog.Main
	g.locals = make(map[*sema.Symbol]llvm.Value)
	g.devices = make(map[*sema.Symbol]sema.Device)
	g.frames, g.breaks, g.continues, g.inlining = nil, nil, nil, nil

	entry := llvm.AddBasicBlock(g.fn, "entry")
	g.builder.SetInsertPointAtEnd(entry)
	g.allocaBuilder.SetInsertPointAtEnd(entry)
	g.terminated = false
	g.setLoc(def.fn.Pos)

	if def.fn == g.prog.Main {
		g.globals()
	}
	retSlot := g.bindParams(def)

	epilogue := g.newBlock("return")
	g.frames = append(g.frames, frame{retSlot: retSlot, retBlock: epilogue})
	g.stmt(def.fn.Decl.Body)
	g.br(epilogue)
	g.tail(epilogue)
	g.setLoc(def.fn.Decl.Body.Rbrace)
	if retSlot.IsNil() {
		g.builder.CreateRetVoid()
		return
	}
	g.builder.CreateRet(g.builder.CreateLoad(retSlot.AllocatedType(), retSlot, ""))
}

// bindParams stores each incoming argument into the parameter's own storage and reserves the slot a
// return writes through, returning that slot or a nil value for a void result. A parameter goes
// through memory rather than an SSA value because that is the form mem2reg promotes, and because a
// parameter is assignable in C.
func (g *generator) bindParams(def definition) llvm.Value {
	for i, param := range def.fn.Params {
		slot, ok := g.storageOf(param)
		if !ok {
			g.errorf(param.Pos, "the parameter '%s' has type %s, which is not lowered", param.Name, param.Type)
			continue
		}
		g.builder.CreateStore(def.value.Param(i), slot)
	}
	if def.fn.Result.Kind() == sema.Void {
		return llvm.Value{}
	}
	result, ok := g.llvmType(def.fn.Result)
	if !ok {
		g.errorf(def.fn.Pos, "'%s' returns %s, which is not lowered", def.fn.Name, def.fn.Result)
		return llvm.Value{}
	}
	return g.alloca(result, def.fn.Name+".result")
}

// llvmType gives the LLVM type an object of MicroC type t is stored as. long long, bool and double
// are all an LLVM double, one memory slot whatever the front end called it. A pointer is an opaque
// pointer the backend resolves to a slot index, and an array is a flat LLVM array carrying the one
// element stride a getelementptr divides out. dev has none: a device is resolved at assembly and
// never occupies storage. The recursion here needs no budget of its own, unlike the statement and
// expression walks: one level is one [ast.ArrayType] node, so it is never deeper than the declaration that wrote it.
func (g *generator) llvmType(t *sema.Type) (llvm.Type, bool) {
	switch t.Kind() {
	case sema.Int, sema.Bool:
		return g.f64, true
	case sema.Double:
		return g.f64, true
	case sema.Pointer:
		return g.ptr, true
	case sema.Array:
		elem, ok := g.llvmType(t.Elem())
		if !ok {
			return llvm.Type{}, false
		}
		return llvm.ArrayType(elem, int(t.Len())), true
	case sema.Invalid, sema.Dev, sema.Void:
	}
	return llvm.Type{}, false
}
