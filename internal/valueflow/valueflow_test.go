package valueflow

import (
	"slices"
	"testing"

	"tinygo.org/x/go-llvm"
)

// scratch builds an empty module with a builder positioned in a function taking
// two integers, a double and a pointer, which is enough shape for every case
// here to name an operand of each kind.
func scratch(t *testing.T) (llvm.Module, llvm.Builder, []llvm.Value) {
	t.Helper()
	ctx := llvm.NewContext()
	m := ctx.NewModule("scratch")
	builder := ctx.NewBuilder()
	t.Cleanup(func() {
		builder.Dispose()
		m.Dispose()
		ctx.Dispose()
	})
	i64, f64 := ctx.Int64Type(), ctx.DoubleType()
	ptr := llvm.PointerType(i64, 0)
	fn := llvm.AddFunction(m, "scratch", llvm.FunctionType(ctx.VoidType(), []llvm.Type{i64, i64, f64, ptr}, false))
	builder.SetInsertPointAtEnd(llvm.AddBasicBlock(fn, "entry"))
	return m, builder, fn.Params()
}

func TestMemoryObject(t *testing.T) {
	m, b, params := scratch(t)
	i64 := m.Context().Int64Type()
	index, p := params[0], params[3]

	local := b.CreateAlloca(i64, "local")
	global := llvm.AddGlobal(m, llvm.ArrayType(i64, 4), "global")
	element := b.CreateInBoundsGEP(i64, local, []llvm.Value{index}, "")
	nested := b.CreateInBoundsGEP(i64, b.CreateInBoundsGEP(i64, global, []llvm.Value{index}, ""), []llvm.Value{index}, "")
	// A constant index into a global folds as it is built, leaving a constant
	// expression where a subscript into a local leaves an instruction.
	folded := b.CreateInBoundsGEP(i64, global, []llvm.Value{llvm.ConstInt(i64, 2, false)}, "")

	cases := []struct {
		name string
		ptr  llvm.Value
		want llvm.Value
	}{
		{name: "an alloca is its own object", ptr: local, want: local},
		{name: "a global is its own object", ptr: global, want: global},
		{name: "a subscript keeps the object it started from", ptr: element, want: local},
		{name: "nested address arithmetic keeps it too", ptr: nested, want: global},
		{name: "a subscript that folded to a constant expression keeps it", ptr: folded, want: global},
		// A nil answer is not "no object": it is every object at once, which is
		// how [run.load] and [run.store] read it. What a caller loses by reading
		// it the other way is covered where each analysis's rules live.
		{name: "a pointer parameter is unplaceable", ptr: p},
		{name: "a loaded pointer is unplaceable", ptr: b.CreateLoad(llvm.PointerType(i64, 0), local, "")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MemoryObject(tc.ptr); got != tc.want {
				t.Errorf("MemoryObject(%v) = %v, want %v", tc.ptr, got, tc.want)
			}
		})
	}
}

// namedRules answers from the names a case gives its instructions, so that a
// case states which shapes stop the property and which carry it without
// reaching for either stage's real rules.
func namedRules(stops, carries map[string]bool) Rules {
	return Rules{
		Stops:   func(in llvm.Value) bool { return stops[in.Name()] },
		Carries: func(in llvm.Value) bool { return carries[in.Name()] },
	}
}

// TestRunPropagates covers the paths the property travels along, each with
// the smallest module that has one. Rules are supplied per case rather than
// taken from either stage, so a change to an analysis's own rules does not
// move a case here.
func TestRunPropagates(t *testing.T) {
	tests := []struct {
		name string
		// build returns the module, the seed, the rules, and the values the walk
		// must reach, named.
		build func(t *testing.T) (llvm.Module, Seed, Rules, []string)
	}{
		{
			name: "an operand carries into the result",
			build: func(t *testing.T) (llvm.Module, Seed, Rules, []string) {
				t.Helper()
				m, b, params := scratch(t)
				x, y := params[0], params[1]
				sum := b.CreateAdd(x, y, "sum")
				_ = b.CreateAdd(sum, sum, "twice")
				b.CreateRetVoid()
				carries := map[string]bool{"sum": true, "twice": true}
				return m, Seed{Values: map[llvm.Value]bool{x: true}}, namedRules(nil, carries), []string{"sum", "twice"}
			},
		},
		{
			name: "an instruction that stops holds nothing and hands on nothing",
			build: func(t *testing.T) (llvm.Module, Seed, Rules, []string) {
				t.Helper()
				m, b, params := scratch(t)
				x, y := params[0], params[1]
				stopped := b.CreateAdd(x, y, "stopped")
				_ = b.CreateAdd(stopped, y, "after")
				b.CreateRetVoid()
				carries := map[string]bool{"stopped": true, "after": true}
				return m, Seed{Values: map[llvm.Value]bool{x: true}},
					namedRules(map[string]bool{"stopped": true}, carries), nil
			},
		},
		{
			name: "an instruction that neither stops nor carries holds it outright",
			build: func(t *testing.T) (llvm.Module, Seed, Rules, []string) {
				t.Helper()
				m, b, params := scratch(t)
				x := params[0]
				_ = b.CreateAdd(x, x, "opening")
				b.CreateRetVoid()
				return m, Seed{}, namedRules(nil, nil), []string{"opening"}
			},
		},
		{
			name: "a store into an object reaches every load out of it",
			build: func(t *testing.T) (llvm.Module, Seed, Rules, []string) {
				t.Helper()
				m, b, params := scratch(t)
				i64 := m.Context().Int64Type()
				x := params[0]
				slot := b.CreateAlloca(i64, "slot")
				other := b.CreateAlloca(i64, "other")
				b.CreateStore(x, slot)
				_ = b.CreateLoad(i64, slot, "read")
				_ = b.CreateLoad(i64, other, "elsewhere")
				b.CreateRetVoid()
				carries := map[string]bool{"read": true, "elsewhere": true}
				// An address is not a value the property travels in, which is
				// what both stages' own rules say of a pointer result.
				stops := map[string]bool{"slot": true, "other": true}
				return m, Seed{Values: map[llvm.Value]bool{x: true}}, namedRules(stops, carries), []string{"read"}
			},
		},
		{
			name: "a store through a pointer with no object reaches every load",
			build: func(t *testing.T) (llvm.Module, Seed, Rules, []string) {
				t.Helper()
				m, b, params := scratch(t)
				i64 := m.Context().Int64Type()
				x, p := params[0], params[3]
				untouched := b.CreateAlloca(i64, "untouched")
				b.CreateStore(x, p)
				_ = b.CreateLoad(i64, untouched, "read")
				b.CreateRetVoid()
				carries := map[string]bool{"read": true}
				stops := map[string]bool{"untouched": true}
				return m, Seed{Values: map[llvm.Value]bool{x: true}}, namedRules(stops, carries), []string{"read"}
			},
		},
		{
			name: "an argument reaches the parameter it binds and the result comes back",
			build: func(t *testing.T) (llvm.Module, Seed, Rules, []string) {
				t.Helper()
				m, b, params := scratch(t)
				ctx := m.Context()
				i64 := ctx.Int64Type()
				calleeType := llvm.FunctionType(i64, []llvm.Type{i64}, false)
				callee := llvm.AddFunction(m, "callee", calleeType)
				inner := ctx.NewBuilder()
				t.Cleanup(inner.Dispose)
				inner.SetInsertPointAtEnd(llvm.AddBasicBlock(callee, "entry"))
				bound := inner.CreateAdd(callee.Param(0), callee.Param(0), "bound")
				inner.CreateRet(bound)

				_ = b.CreateCall(calleeType, callee, []llvm.Value{params[0]}, "call")
				b.CreateRetVoid()
				carries := map[string]bool{"bound": true, "call": true}
				return m, Seed{Values: map[llvm.Value]bool{params[0]: true}}, namedRules(nil, carries),
					[]string{"bound", "call"}
			},
		},
		{
			name: "a result a definition hands back is still refused by the rules",
			build: func(t *testing.T) (llvm.Module, Seed, Rules, []string) {
				t.Helper()
				m, b, params := scratch(t)
				ctx := m.Context()
				i64 := ctx.Int64Type()
				calleeType := llvm.FunctionType(i64, []llvm.Type{i64}, false)
				callee := llvm.AddFunction(m, "callee", calleeType)
				inner := ctx.NewBuilder()
				t.Cleanup(inner.Dispose)
				inner.SetInsertPointAtEnd(llvm.AddBasicBlock(callee, "entry"))
				inner.CreateRet(inner.CreateAdd(callee.Param(0), callee.Param(0), "bound"))

				_ = b.CreateCall(calleeType, callee, []llvm.Value{params[0]}, "stopped")
				b.CreateRetVoid()
				carries := map[string]bool{"bound": true, "stopped": true}
				return m, Seed{Values: map[llvm.Value]bool{params[0]: true}},
					namedRules(map[string]bool{"stopped": true}, carries), []string{"bound"}
			},
		},
		{
			name: "a pointer handed to a declaration reaches every load",
			build: func(t *testing.T) (llvm.Module, Seed, Rules, []string) {
				t.Helper()
				m, b, _ := scratch(t)
				ctx := m.Context()
				i64 := ctx.Int64Type()
				handed := b.CreateAlloca(i64, "handed")
				untouched := b.CreateAlloca(i64, "untouched")
				writesType := llvm.FunctionType(ctx.VoidType(), []llvm.Type{llvm.PointerType(i64, 0)}, false)
				writes := llvm.AddFunction(m, "writes", writesType)
				_ = b.CreateCall(writesType, writes, []llvm.Value{handed}, "")
				_ = b.CreateLoad(i64, untouched, "read")
				b.CreateRetVoid()
				carries := map[string]bool{"read": true}
				stops := map[string]bool{"handed": true, "untouched": true}
				// The object handed over is not the object read. There is no body
				// to say which one the call wrote through, so the answer has to be
				// both.
				return m, Seed{}, namedRules(stops, carries), []string{"read"}
			},
		},
		{
			name: "an instruction with no arm of its own may have written through its pointer",
			build: func(t *testing.T) (llvm.Module, Seed, Rules, []string) {
				t.Helper()
				m, b, params := scratch(t)
				i64 := m.Context().Int64Type()
				x := params[0]
				handed := b.CreateAlloca(i64, "handed")
				untouched := b.CreateAlloca(i64, "untouched")
				written := b.CreateAtomicRMW(llvm.AtomicRMWBinOpXchg, handed, x, llvm.AtomicOrderingMonotonic, false)
				written.SetName("written")
				_ = b.CreateLoad(i64, untouched, "read")
				b.CreateRetVoid()
				carries := map[string]bool{"written": true, "read": true}
				stops := map[string]bool{"handed": true, "untouched": true}
				// The walk has no arm for an atomicrmw and so cannot say which
				// object it wrote, which leaves every object as the answer. The
				// object read is not the object written, and it is reached all
				// the same.
				return m, Seed{Values: map[llvm.Value]bool{x: true}}, namedRules(stops, carries),
					[]string{"written", "read"}
			},
		},
		{
			name: "the pointer arithmetic the walk knows writes nothing leaves memory alone",
			build: func(t *testing.T) (llvm.Module, Seed, Rules, []string) {
				t.Helper()
				m, b, params := scratch(t)
				i64 := m.Context().Int64Type()
				index := params[0]
				object := b.CreateAlloca(i64, "object")
				element := b.CreateInBoundsGEP(i64, object, []llvm.Value{index}, "element")
				_ = b.CreatePtrToInt(element, i64, "addr")
				_ = b.CreateICmp(llvm.IntEQ, element, object, "same")
				_ = b.CreateLoad(i64, object, "read")
				b.CreateRetVoid()
				carries := map[string]bool{"addr": true, "same": true, "read": true}
				stops := map[string]bool{"object": true, "element": true}
				// Nothing is seeded, so a load that answers here is one the walk
				// took an address expression for a write.
				return m, Seed{}, namedRules(stops, carries), nil
			},
		},
		{
			name: "a call to a declaration is decided by the rules alone",
			build: func(t *testing.T) (llvm.Module, Seed, Rules, []string) {
				t.Helper()
				m, b, _ := scratch(t)
				i64 := m.Context().Int64Type()
				opaqueType := llvm.FunctionType(i64, nil, false)
				opaque := llvm.AddFunction(m, "opaque", opaqueType)
				_ = b.CreateCall(opaqueType, opaque, nil, "outright")
				b.CreateRetVoid()
				return m, Seed{}, namedRules(nil, nil), []string{"outright"}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, seed, rules, want := tt.build(t)
			marked := Run(m, rules, seed)
			named := map[string]bool{}
			for v := range marked {
				if name := v.Name(); name != "" {
					named[name] = true
				}
			}
			for _, name := range want {
				if !named[name] {
					t.Errorf("the walk did not reach %q; it reached %v", name, sortedNames(named))
				}
			}
			for name := range named {
				if !contains(want, name) {
					t.Errorf("the walk reached %q, which no case expects; it reached %v", name, sortedNames(named))
				}
			}
		})
	}
}

func contains(names []string, want string) bool {
	return slices.Contains(names, want)
}

func sortedNames(named map[string]bool) []string {
	names := make([]string, 0, len(named))
	for name := range named {
		names = append(names, name)
	}
	return names
}

// laterSweeps builds the smallest module the walk needs more than one
// sweep for: the call is walked before the definition it names, so what the
// callee hands back reaches the call site only a sweep later. It returns
// the fadd the cases below move the rules about, and the call that reaches it.
func laterSweeps(t *testing.T) (llvm.Module, llvm.Value, llvm.Value) {
	t.Helper()
	ctx := llvm.NewContext()
	m := ctx.NewModule("sweeps")
	builder := ctx.NewBuilder()
	t.Cleanup(func() {
		builder.Dispose()
		m.Dispose()
		ctx.Dispose()
	})
	f64 := ctx.DoubleType()
	sourceType := llvm.FunctionType(f64, nil, false)
	source := llvm.AddFunction(m, "source", sourceType)

	handsBackType := llvm.FunctionType(f64, nil, false)
	main := llvm.AddFunction(m, "main", llvm.FunctionType(ctx.VoidType(), nil, false))
	handsBack := llvm.AddFunction(m, "handsBack", handsBackType)

	builder.SetInsertPointAtEnd(llvm.AddBasicBlock(main, "entry"))
	call := builder.CreateCall(handsBackType, handsBack, nil, "call")
	after := builder.CreateFAdd(call, llvm.ConstFloat(f64, 1), "after")
	builder.CreateRetVoid()

	builder.SetInsertPointAtEnd(llvm.AddBasicBlock(handsBack, "entry"))
	builder.CreateRet(builder.CreateCall(sourceType, source, nil, "opening"))
	return m, call, after
}

// moving answers one named instruction differently after the first asking, which
// is the shape of a rule holding a budget or a cache: what the first asking
// spends the second cannot.
type moving struct {
	name        string
	first, next bool
	asked       map[llvm.Value]int
}

func movingRule(name string, first, next bool) *moving {
	return &moving{name: name, first: first, next: next, asked: make(map[llvm.Value]int)}
}

func (r *moving) answer(in llvm.Value) bool {
	if in.Name() != r.name {
		return false
	}
	r.asked[in]++
	if r.asked[in] == 1 {
		return r.first
	}
	return r.next
}

// moved reports whether the walk asked often enough for the answer to have
// changed, which is what the cases below rest on.
func (r *moving) moved() bool {
	for _, count := range r.asked {
		if count > 1 {
			return true
		}
	}
	return false
}

// TestRunSettlesUnderARuleThatMoves covers the walk against a rule
// whose answer changes between askings, unlike the compiler's own. The
// sweeps still run out, settling on whatever the moving answer asked
// for in whichever direction it moved.
func TestRunSettlesUnderARuleThatMoves(t *testing.T) {
	tests := []struct {
		name string
		// rules wraps the moving rule into the pair, so that a case says which of
		// the two moves.
		rules func(*moving) Rules
		// first and next are what the moving rule answers.
		first, next bool
		// marked is whether the fadd holds the property once the sweeps settle.
		marked bool
	}{
		{
			// The first asking marks it outright, and the answer that arrives
			// afterwards would only have marked it conditionally. Nothing is ever
			// unmarked, so what the earlier answer settled stands.
			name: "a rule that starts carrying late keeps the mark it made",
			rules: func(r *moving) Rules {
				return Rules{Stops: never, Carries: r.answer}
			},
			first: false, next: true, marked: true,
		},
		{
			// The mark the first asking withholds is one no operand had reached
			// anyway, so the answer that arrives afterwards is the one that
			// decides, a sweep late. That is conservatism arriving late and
			// nothing worse.
			name: "a rule that stops stopping marks a sweep late",
			rules: func(r *moving) Rules {
				return Rules{Stops: r.answer, Carries: carriesTheFAdd}
			},
			first: true, next: false, marked: true,
		},
		{
			// The other way round, which is what the answer costs. By the sweep
			// the operand is marked the rule has moved to stopping, so the mark
			// the first answer would have propagated is never made and the sweeps
			// settle on the smaller set without saying so.
			name: "a rule that starts stopping late withholds a mark the first answer would have made",
			rules: func(r *moving) Rules {
				return Rules{Stops: r.answer, Carries: carriesTheFAdd}
			},
			first: false, next: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, call, after := laterSweeps(t)
			rule := movingRule("after", tt.first, tt.next)
			marked := Run(m, tt.rules(rule), Seed{})
			if !rule.moved() {
				t.Fatal("the walk asked the moving rule once, so its answer never moved")
			}
			if !marked[call] {
				t.Error("the walk did not reach the call, so nothing had reached the instruction under test")
			}
			if got := marked[after]; got != tt.marked {
				t.Errorf("the fadd is marked = %v, want %v; the walk reached %v", got, tt.marked, sortedNames(namesOf(marked)))
			}
		})
	}
}

// TestRunSettlesUnderARuleThatFlips is the same claim with nothing left of
// purity at all: the answer changes on every asking rather than once. The sweeps
// still run out, because the only thing one changes is the growth of a bounded
// set, and a run that did not would hang this test rather than fail it.
func TestRunSettlesUnderARuleThatFlips(t *testing.T) {
	m, call, _ := laterSweeps(t)
	answer := false
	flip := func(in llvm.Value) bool {
		if in.Name() != "after" {
			return false
		}
		answer = !answer
		return answer
	}
	marked := Run(m, Rules{Stops: flip, Carries: flip}, Seed{})
	if !marked[call] {
		t.Errorf("the walk did not reach the call; it reached %v", sortedNames(namesOf(marked)))
	}
}

// never and carriesTheFAdd are the halves a case does not move. Nothing stops,
// and only the fadd carries, which is what leaves the call into the declaration
// neither stopped nor carried and so a source of the property.
func never(llvm.Value) bool { return false }

func carriesTheFAdd(in llvm.Value) bool { return in.Name() == "after" }

func namesOf(marked map[llvm.Value]bool) map[string]bool {
	named := map[string]bool{}
	for v := range marked {
		if name := v.Name(); name != "" {
			named[name] = true
		}
	}
	return named
}
