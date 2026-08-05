package isel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/chiptest"
	"github.com/greg2010/ic11c/internal/emit"
	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/llvmir"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/regalloc"
	"github.com/greg2010/ic11c/internal/source"
	"tinygo.org/x/go-llvm"
)

func TestMain(m *testing.M) { chiptest.Main(m) }

// builder is the LLVM state a case builds its module in. Everything in it is
// owned by the running subtest and disposed when the subtest returns.
type builder struct {
	ctx llvm.Context
	m   llvm.Module
	b   llvm.Builder
	fn  llvm.Value
	i64 llvm.Type
}

// newBuilder starts a module holding one void main with an entry block, which
// is the shape IR generation produces.
func newBuilder(t *testing.T) *builder {
	t.Helper()
	ctx := llvm.NewContext()
	m := ctx.NewModule("test")
	b := ctx.NewBuilder()
	t.Cleanup(func() {
		b.Dispose()
		m.Dispose()
		ctx.Dispose()
	})
	fn := llvm.AddFunction(m, "main", llvm.FunctionType(ctx.VoidType(), nil, false))
	bb := llvm.AddBasicBlock(fn, "entry")
	b.SetInsertPointAtEnd(bb)
	return &builder{ctx: ctx, m: m, b: b, fn: fn, i64: ctx.Int64Type()}
}

func (bd *builder) block(name string) llvm.BasicBlock {
	return llvm.AddBasicBlock(bd.fn, name)
}

func (bd *builder) konst(v int64) llvm.Value {
	return llvm.ConstInt(bd.i64, uint64(v), true)
}

// keep forces a value to be observed, so that a pattern under test is not the
// only thing holding it and selection has to materialise it.
func (bd *builder) keep(v llvm.Value) {
	slot := bd.b.CreateAlloca(bd.i64, "keep")
	bd.b.CreateStore(v, slot)
}

// opaque produces a value the IR builder cannot fold, which is what an
// arithmetic case needs: the builder folds an operation on two literals into a
// literal, and there is then no instruction to select.
func (bd *builder) opaque(name string) llvm.Value {
	slot := bd.b.CreateAlloca(bd.i64, name)
	return bd.b.CreateLoad(bd.i64, slot, name)
}

// selectFunc runs selection over the module and returns the machine
// instructions of its one function, rendered as text.
func selectFunc(t *testing.T, bd *builder) []string {
	t.Helper()
	result, err := Select(t.Context(), bd.m, Options{File: "test.c"})
	if err != nil {
		t.Fatalf("Select: %v\n--- module ---\n%s", err, bd.m.String())
	}
	return render(result.Program.Funcs[0])
}

// selectProgram runs selection over IR text and returns the whole program, one
// function per entry in emission order. It is what a case with more than one
// function in it needs, where selectFunc renders only the first.
func selectProgram(t *testing.T, text string) *Result {
	t.Helper()
	m := parseIR(t, text)
	result, err := Select(t.Context(), m, Options{File: "test.c"})
	if err != nil {
		t.Fatalf("Select: %v\n%s", err, text)
	}
	return result
}

func render(fn *mir.Func) []string {
	var lines []string
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			lines = append(lines, instr.String())
		}
	}
	return lines
}

func mnemonics(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i], _, _ = strings.Cut(line, " ")
	}
	return out
}

func contains(lines []string, want string) bool {
	return slices.Contains(lines, want)
}

func containsSequence(got, want []string) bool {
	for start := 0; start+len(want) <= len(got); start++ {
		matched := true
		for i, w := range want {
			if got[start+i] != w {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// TestSelectDataRegionLayout checks the slot arithmetic every array access rests
// on: objects laid out consecutively, one slot per element, and a constant
// subscript as a literal slot rather than an address computed at run time.
func TestSelectDataRegionLayout(t *testing.T) {
	bd := newBuilder(t)
	table := llvm.ArrayType(bd.i64, 4)
	first := llvm.AddGlobal(bd.m, table, "first")
	first.SetInitializer(llvm.ConstNull(table))
	second := llvm.AddGlobal(bd.m, bd.i64, "second")
	second.SetInitializer(llvm.ConstNull(bd.i64))
	local := bd.b.CreateAlloca(table, "local")

	index := bd.opaque("i")
	bd.b.CreateStore(bd.konst(7),
		bd.b.CreateInBoundsGEP(bd.i64, first, []llvm.Value{bd.konst(3)}, "last"))
	bd.b.CreateStore(bd.konst(8), second)
	bd.b.CreateStore(bd.konst(9),
		bd.b.CreateInBoundsGEP(bd.i64, local, []llvm.Value{index}, "computed"))
	bd.b.CreateRetVoid()

	result, err := Select(t.Context(), bd.m, Options{File: "test.c"})
	if err != nil {
		t.Fatalf("Select: %v\n%s", err, bd.m.String())
	}
	got := render(result.Program.Funcs[0])

	// first takes slots 0 through 3, second slot 4, and the local array 5
	// through 8. The scratch alloca opaque left behind takes slot 9.
	for _, want := range []string{"poke 3 7", "poke 4 8", "add vr1 5 vr0", "poke vr1 9"} {
		if !contains(got, want) {
			t.Errorf("selected %v, want a %q", got, want)
		}
	}
	if result.DataSlots != 10 {
		t.Errorf("DataSlots = %d, want 10: two arrays of four, one scalar, and one scratch slot", result.DataSlots)
	}
}

func TestSelectRejects(t *testing.T) {
	cases := []struct {
		name  string
		build func(bd *builder)
		want  string
	}{
		{
			// Every object is one slot or an array of them, so an offset that
			// is not a whole number of slots would read part of a double.
			name: "an address that is not a whole number of slots",
			build: func(bd *builder) {
				table := llvm.AddGlobal(bd.m, llvm.ArrayType(bd.i64, 4), "table")
				table.SetInitializer(llvm.ConstNull(llvm.ArrayType(bd.i64, 4)))
				elem := bd.b.CreateInBoundsGEP(bd.ctx.Int8Type(), table, []llvm.Value{bd.opaque("i")}, "elem")
				bd.keep(bd.b.CreateLoad(bd.i64, elem, "v"))
				bd.b.CreateRetVoid()
			},
			want: "whole memory slots",
		},
		{
			name: "a pointer no object and no computation produced",
			build: func(bd *builder) {
				bd.keep(bd.b.CreateLoad(bd.i64, llvm.ConstNull(llvm.PointerType(bd.i64, 0)), "v"))
				bd.b.CreateRetVoid()
			},
			want: "does not resolve to a memory slot",
		},
		{
			name: "a call that is not a machine intrinsic",
			build: func(bd *builder) {
				fnType := llvm.FunctionType(bd.i64, []llvm.Type{bd.i64}, false)
				callee := llvm.AddFunction(bd.m, "helper", fnType)
				bd.keep(bd.b.CreateCall(fnType, callee, []llvm.Value{bd.konst(1)}, "v"))
				bd.b.CreateRetVoid()
			},
			want: "is not selected",
		},
		{
			// A batch mode is int32 in the game, so a wider value wraps onto a
			// mode meaning something else — 256 becomes Average.
			name: "an operand wider than the enum behind it",
			build: func(bd *builder) {
				fnType := llvm.FunctionType(bd.i64, []llvm.Type{bd.i64, bd.i64, bd.i64}, false)
				callee := llvm.AddFunction(bd.m, "__ic_load_batch", fnType)
				args := []llvm.Value{bd.konst(1), bd.konst(0), bd.konst(1 << 32)}
				bd.keep(bd.b.CreateCall(fnType, callee, args, "v"))
				bd.b.CreateRetVoid()
			},
			want: "outside the 0 to 2147483647 range",
		},
		{
			name: "a logic type wider than the sixteen bits behind it",
			build: func(bd *builder) {
				fnType := llvm.FunctionType(bd.i64, []llvm.Type{bd.i64, bd.i64}, false)
				callee := llvm.AddFunction(bd.m, "__ic_load", fnType)
				args := []llvm.Value{bd.konst(0), bd.konst(1 << 16)}
				bd.keep(bd.b.CreateCall(fnType, callee, args, "v"))
				bd.b.CreateRetVoid()
			},
			want: "outside the 0 to 65535 range",
		},
		{
			// Every argument register is withheld from the calling function's
			// allocation throughout, so the count is capped below the file size.
			name: "more parameters than a call passes",
			build: func(bd *builder) {
				params := make([]llvm.Type, maxCallArgs+1)
				for i := range params {
					params[i] = bd.i64
				}
				fn := llvm.AddFunction(bd.m, "wide", llvm.FunctionType(bd.ctx.VoidType(), params, false))
				bd.b.SetInsertPointAtEnd(llvm.AddBasicBlock(fn, "entry"))
				bd.b.CreateRetVoid()
				bd.b.SetInsertPointAtEnd(bd.fn.EntryBasicBlock())
				bd.b.CreateRetVoid()
			},
			want: "at most",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bd := newBuilder(t)
			tc.build(bd)

			_, err := Select(t.Context(), bd.m, Options{File: "test.c"})
			if err == nil {
				t.Fatalf("Select accepted a module it cannot lower:\n%s", bd.m.String())
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not mention %q: %v", tc.want, err)
			}
			assertNamesSourceRatherThanIR(t, err)
		})
	}
}

// TestSelectReportsAWideParameterListOnce covers the cascade a call to an
// over-wide function used to produce. The bound belongs on the definition, since
// removing a parameter is what the message asks for.
func TestSelectReportsAWideParameterListOnce(t *testing.T) {
	bd := newBuilder(t)
	params := make([]llvm.Type, maxCallArgs+1)
	args := make([]llvm.Value, len(params))
	for i := range params {
		params[i] = bd.i64
		args[i] = bd.konst(int64(i))
	}
	fnType := llvm.FunctionType(bd.ctx.VoidType(), params, false)
	callee := llvm.AddFunction(bd.m, "wide", fnType)
	bd.b.SetInsertPointAtEnd(llvm.AddBasicBlock(callee, "entry"))
	bd.b.CreateRetVoid()

	bd.b.SetInsertPointAtEnd(bd.fn.EntryBasicBlock())
	bd.b.CreateCall(fnType, callee, args, "")
	bd.b.CreateRetVoid()

	_, err := Select(t.Context(), bd.m, Options{File: "test.c"})
	var diags source.DiagnosticList
	if !errors.As(err, &diags) {
		t.Fatalf("Select = %v, want a diagnostic list\n%s", err, bd.m.String())
	}
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics for one over-wide function, want 1:\n%s", len(diags), diags)
	}
	if !strings.Contains(diags[0].Msg, "'wide' takes") {
		t.Errorf("the diagnostic does not name the definition:\n%s", diags[0].Msg)
	}
}

// TestSelectCarriesSourcePositions checks the thread from IR debug locations
// onto machine instructions, including the ones selection synthesized: a
// remainder is four instructions, none of which existed in the IR.
func TestSelectCarriesSourcePositions(t *testing.T) {
	bd := newBuilder(t)
	di := llvm.NewDIBuilder(bd.m)
	defer di.Destroy()

	flag := bd.ctx.MDNode([]llvm.Metadata{
		llvm.ConstInt(bd.ctx.Int32Type(), 2, false).ConstantAsMetadata(),
		bd.ctx.MDString("Debug Info Version"),
		llvm.ConstInt(bd.ctx.Int32Type(), 3, false).ConstantAsMetadata(),
	})
	bd.m.AddNamedMetadataOperand("llvm.module.flags", flag)

	di.CreateCompileUnit(llvm.DICompileUnit{Language: llvm.DwarfLang(11), File: "test.c", Dir: ".", Producer: "ic11c"})
	file := di.CreateFile("test.c", ".")
	sig := di.CreateSubroutineType(llvm.DISubroutineType{File: file})
	scope := di.CreateFunction(file, llvm.DIFunction{
		Name: "main", LinkageName: "main", File: file, Line: 1, Type: sig, IsDefinition: true, ScopeLine: 1,
	})
	bd.fn.SetSubprogram(scope)

	bd.b.SetCurrentDebugLocation(12, 5, scope, llvm.Metadata{})
	bd.keep(bd.b.CreateSRem(bd.konst(7), bd.konst(2), ""))
	bd.b.CreateRetVoid()
	di.Finalize()

	result, err := Select(t.Context(), bd.m, Options{File: "test.c"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	for _, block := range result.Program.Funcs[0].Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == isa.OpClr {
				// The zeroing prologue is compiler-introduced: it carries the
				// function's position because no statement asked for it.
				continue
			}
			if !instr.Pos.IsValid() {
				t.Errorf("instruction %q carries no source position", instr)
				continue
			}
			if instr.Pos.File != "test.c" {
				t.Errorf("instruction %q names file %q, want test.c", instr, instr.Pos.File)
			}
			if instr.Pos.Line != 12 {
				t.Errorf("instruction %q is attributed to line %d, want 12", instr, instr.Pos.Line)
			}
		}
	}
}

func TestSelectRejectsNilModule(t *testing.T) {
	if _, err := Select(context.Background(), llvm.Module{}, Options{}); err == nil {
		t.Fatalf("Select accepted a nil module")
	}
}

func TestSelectHonoursCancellation(t *testing.T) {
	bd := newBuilder(t)
	bd.b.CreateRetVoid()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Select(ctx, bd.m, Options{}); err == nil {
		t.Fatalf("Select ignored a cancelled context")
	}
}

// inlineIR is one instruction written in the function itself and one spliced in
// from a callee: the inlined body keeps its own line and names the call it came
// through as inlinedAt.
const inlineIR = `
define void @main() !dbg !4 {
entry:
  %slot = alloca i64
  store i64 1, ptr %slot, !dbg !7
  store i64 2, ptr %slot, !dbg !9
  ret void, !dbg !7
}

!llvm.module.flags = !{!0}
!llvm.dbg.cu = !{!1}
!0 = !{i32 2, !"Debug Info Version", i32 3}
!1 = distinct !DICompileUnit(language: DW_LANG_C99, file: !2, producer: "ic11c", isOptimized: true, emissionKind: FullDebug)
!2 = !DIFile(filename: "case.c", directory: ".")
!3 = !DISubroutineType(types: !5)
!4 = distinct !DISubprogram(name: "main", scope: !2, file: !2, line: 1, type: !3, scopeLine: 1, spFlags: DISPFlagDefinition, unit: !1)
!5 = !{null}
!7 = !DILocation(line: 20, column: 5, scope: !4)
!8 = !DILocation(line: 9, column: 3, scope: !4)
!9 = !DILocation(line: 30, column: 7, scope: !4, inlinedAt: !8)
`

// TestSelectCarriesInlineChains is the half of per-inline-site attribution this
// stage owns. IR generation records which callee each call site expanded and the
// debug locations carry the chain; neither half locates a byte on its own.
func TestSelectCarriesInlineChains(t *testing.T) {
	m := parseIR(t, inlineIR)
	lines := source.NewLineMap(strings.Repeat("x\n", 40))
	result, err := Select(t.Context(), m, Options{
		File:        "case.c",
		Lines:       lines,
		InlineSites: map[source.LineCol]string{{Line: 9, Column: 3}: "helper"},
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}

	var pokes []*mir.Instr
	for _, instr := range result.Program.Funcs[0].AllInstrs() {
		if instr.Op == isa.OpPoke {
			pokes = append(pokes, instr)
		}
	}
	if len(pokes) != 2 {
		t.Fatalf("selected %d stores, want 2:\n%s", len(pokes), m.String())
	}

	if len(pokes[0].Inline) != 0 {
		t.Errorf("the function's own store carries the chain %v, want none", pokes[0].Inline)
	}
	if got, want := pokes[0].Pos.Line, 20; got != want {
		t.Errorf("the function's own store is at line %d, want %d", got, want)
	}
	// The offset a debug location does not carry is what the line map restores,
	// and it is what makes a backend diagnostic sort against a front-end one.
	if got, want := pokes[0].Pos.Offset, lines.Offset(20, 5); got != want {
		t.Errorf("the store is at offset %d, want %d", got, want)
	}

	want := []source.InlineSite{{
		Pos:    source.Position{File: "case.c", Offset: lines.Offset(9, 3), Line: 9, Column: 3},
		Callee: "helper",
	}}
	if !slices.Equal(pokes[1].Inline, want) {
		t.Errorf("the inlined store carries %v, want %v", pokes[1].Inline, want)
	}
	if got, want := pokes[1].Pos.Line, 30; got != want {
		t.Errorf("the inlined store is at line %d, want %d: the callee's own line, not the call site's", got, want)
	}
}

// assertNamesSourceRatherThanIR holds a refusal to describing the construct
// rather than quoting the optimizer's text, which names nothing a programmer can
// act on. It is called from every table's loop rather than from a case of its
// own, since one program checked in one place leaves the other messages free.
func assertNamesSourceRatherThanIR(t *testing.T, err error) {
	t.Helper()
	for _, leak := range []string{"%r", "%slot", "!dbg", "i64 ", " = "} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("the refusal quotes LLVM IR (%q): %v", leak, err)
		}
	}
}

// edgeIR is a critical edge into a block holding a phi, with the whole function
// spliced in from a callee. The edge is critical because the entry branches two
// ways and the merge is reached from both, so the phi's copy needs its own block.
const edgeIR = `
define void @main() !dbg !4 {
entry:
  %slot = alloca i64
  %v = load i64, ptr %slot, !dbg !9
  %c = icmp sgt i64 %v, 0, !dbg !9
  br i1 %c, label %merge, label %other, !dbg !9

other:
  br label %merge, !dbg !9

merge:
  %m = phi i64 [ 1, %entry ], [ 2, %other ], !dbg !9
  store i64 %m, ptr %slot, !dbg !9
  ret void, !dbg !7
}

!llvm.module.flags = !{!0}
!llvm.dbg.cu = !{!1}
!0 = !{i32 2, !"Debug Info Version", i32 3}
!1 = distinct !DICompileUnit(language: DW_LANG_C99, file: !2, producer: "ic11c", isOptimized: true, emissionKind: FullDebug)
!2 = !DIFile(filename: "case.c", directory: ".")
!3 = !DISubroutineType(types: !5)
!4 = distinct !DISubprogram(name: "main", scope: !2, file: !2, line: 1, type: !3, scopeLine: 1, spFlags: DISPFlagDefinition, unit: !1)
!5 = !{null}
!7 = !DILocation(line: 20, column: 5, scope: !4)
!8 = !DILocation(line: 9, column: 3, scope: !4)
!9 = !DILocation(line: 30, column: 7, scope: !4, inlinedAt: !8)
`

// TestEdgeBlockIsOneInlineSite holds a split edge to one unit of the size
// report. Charging its copies to the call the body came from and its jump to the
// enclosing function would report two units where the code has one.
func TestEdgeBlockIsOneInlineSite(t *testing.T) {
	m := parseIR(t, edgeIR)
	lines := source.NewLineMap(strings.Repeat("x\n", 40))
	result, err := Select(t.Context(), m, Options{
		File:        "case.c",
		Lines:       lines,
		InlineSites: map[source.LineCol]string{{Line: 9, Column: 3}: "helper"},
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}

	var edge *mir.Block
	for _, block := range result.Program.Funcs[0].Blocks {
		if strings.Contains(block.Label, ".to.") {
			edge = block
		}
	}
	if edge == nil {
		t.Fatalf("the critical edge was not split: %v", render(result.Program.Funcs[0]))
	}
	if len(edge.Instrs) < 2 {
		t.Fatalf("the edge block holds %d instructions, want the copy and the jump it is built from", len(edge.Instrs))
	}

	want := []source.InlineSite{{
		Pos:    source.Position{File: "case.c", Offset: lines.Offset(9, 3), Line: 9, Column: 3},
		Callee: "helper",
	}}
	for _, instr := range edge.Instrs {
		if !slices.Equal(instr.Inline, want) {
			t.Errorf("%s in the edge block carries %v, want %v", instr, instr.Inline, want)
		}
	}
}

// parseIR builds a module from textual IR, which is how a case reaches a
// construct the IR builder bindings expose no way to create.
func parseIR(t *testing.T, text string) llvm.Module {
	t.Helper()
	path := filepath.Join(t.TempDir(), "case.ll")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("writing IR: %v", err)
	}
	// ParseIR takes the buffer over, so there is nothing here to dispose.
	buf, err := llvm.NewMemoryBufferFromFile(path)
	if err != nil {
		t.Fatalf("reading IR: %v", err)
	}
	ctx := llvm.NewContext()
	m, err := ctx.ParseIR(buf)
	if err != nil {
		ctx.Dispose()
		t.Fatalf("parsing IR: %v\n%s", err, text)
	}
	t.Cleanup(func() {
		m.Dispose()
		ctx.Dispose()
	})
	return m
}

// freezeIR is one freeze feeding two readers, which is the shape the optimizer
// produces when it duplicates a use that would otherwise multiply poison.
const freezeIR = `
define void @main() {
entry:
  %slot = alloca i64
  %x = load i64, ptr %slot
  %frozen = freeze i64 %x
  %sum = add nsw i64 %frozen, %frozen
  store i64 %sum, ptr %slot
  ret void
}
`

// TestFreezeOpcode pins the numeric opcode the bindings do not name. Reading
// the wrong constant would make selection treat some other instruction as a
// free alias, which is a wrong answer on the chip rather than a build failure.
func TestFreezeOpcode(t *testing.T) {
	m := parseIR(t, freezeIR)
	fn := m.NamedFunction("main")

	var found bool
	for in := range llvmir.BlockInstrs(fn.EntryBasicBlock()) {
		if in.Name() != "frozen" {
			continue
		}
		found = true
		if got := in.InstructionOpcode(); got != opcodeFreeze {
			t.Errorf("freeze opcode = %d, and opcodeFreeze is %d", got, opcodeFreeze)
		}
	}
	if !found {
		t.Fatalf("the parsed module holds no freeze:\n%s", m.String())
	}
}

// TestFreezeIsAnAlias checks that a freeze costs no line. The machine has no
// poison for it to stop, so the frozen value is the value it froze.
func TestFreezeIsAnAlias(t *testing.T) {
	m := parseIR(t, freezeIR)

	result, err := Select(t.Context(), m, Options{File: "test.c"})
	if err != nil {
		t.Fatalf("Select: %v\n%s", err, m.String())
	}
	got := render(result.Program.Funcs[0])
	// The leading clr db is the zeroing prologue, which every program that
	// puts anything in the data region carries.
	want := []string{"clr", "get", "add", "poke"}
	if len(got) != len(want) {
		t.Fatalf("selected %v, want one instruction per %v", got, want)
	}
	for i, mnemonic := range mnemonics(got) {
		if mnemonic != want[i] {
			t.Errorf("instruction %d is %q, want %q: %v", i, mnemonic, want[i], got)
		}
	}
}

// TestProducesOperandByWidth pins the gate over both widths of every conversion
// that reaches it. An alias emits nothing and hands the operand's register back,
// so a wrong answer is a value silently something else — never a build failure
// and never a fault on the chip.
func TestProducesOperandByWidth(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "a truth value widened unsigned", body: "%r = zext i1 %c to i64", want: true},
		{name: "a narrower integer widened unsigned", body: "%r = zext i32 %n to i64", want: false},
		{name: "a truth value read as unsigned", body: "%r = uitofp i1 %c to double", want: true},
		{name: "an int read as unsigned", body: "%r = uitofp i64 %x to double", want: true},
		{name: "a truth value read as signed", body: "%r = sitofp i1 %c to double", want: false},
		{name: "an int read as signed", body: "%r = sitofp i64 %x to double", want: true},
		{name: "a freeze of any width", body: "%r = freeze i64 %x", want: true},
		{name: "a freeze of a truth value", body: "%r = freeze i1 %c", want: true},
		// A register holds a pointer as a slot index and LLVM computes with the
		// byte address of that slot, so the cast is the scale between them.
		{name: "a pointer read as an integer", body: "%r = ptrtoint ptr %in to i64", want: false},
		{name: "a truth value widened signed", body: "%r = sext i1 %c to i64", want: false},
		// IR generation reads a register back as an i64 wherever LLVM's type
		// system insists on one, and the register held the whole number before
		// the conversion and after it.
		{name: "a whole double read back as an integer", body: "%r = fptosi double %d to i64", want: true},
		// The machine holds a truth value as 0 or 1, so choosing between those
		// two on one is the value itself. InstCombine forms it out of the
		// widening a comparison read as a MicroC value carries.
		{name: "a choice between one and zero on a truth value", body: "%r = select i1 %c, double 1.000000e+00, double 0.000000e+00", want: true},
		{name: "the same choice stated over integers", body: "%r = select i1 %c, i64 1, i64 0", want: true},
		{name: "a choice between zero and one is the complement, not the value", body: "%r = select i1 %c, double 0.000000e+00, double 1.000000e+00", want: false},
		{name: "a choice between computed values", body: "%r = select i1 %c, i64 %x, i64 1", want: false},
		{name: "an operation that computes something", body: "%r = add i64 %x, 1", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := parseIR(t, `
define void @main() {
entry:
  %in = alloca i64
  %x = load i64, ptr %in
  %n = trunc i64 %x to i32
  %d = sitofp i64 %x to double
  %c = icmp sgt i64 %x, 5
  `+tt.body+`
  ret void
}
`)
			var s selector
			if got := s.producesOperand(namedInstruction(t, m, "r")); got != tt.want {
				t.Errorf("producesOperand(%s) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

// namedInstruction finds the one instruction of main a case gave a name to.
func namedInstruction(t *testing.T, m llvm.Module, name string) llvm.Value {
	t.Helper()
	fn := m.NamedFunction("main")
	for in := range llvmir.BlockInstrs(fn.EntryBasicBlock()) {
		if in.Name() == name {
			return in
		}
	}
	t.Fatalf("the parsed module holds no %%%s:\n%s", name, m.String())
	return llvm.Value{}
}

// assemble runs the stages after selection, so that what selection produced can
// be executed rather than only inspected.
func assemble(t *testing.T, m llvm.Module) string {
	t.Helper()
	selected, err := Select(t.Context(), m, Options{File: "test.c"})
	if err != nil {
		t.Fatalf("Select: %v\n%s", err, m.String())
	}
	cfg := regalloc.Config{Scratch: regalloc.DefaultScratch(), SpillSlotBase: selected.DataSlots}
	for _, fn := range selected.Program.Funcs {
		allocated, allocErr := regalloc.Allocate(fn, cfg)
		if allocErr != nil {
			t.Fatalf("allocating registers for %s: %v", fn.Name, allocErr)
		}
		cfg.SpillSlotBase += allocated.SpillSlots
	}
	out, err := emit.Emit(selected.Program, emit.Options{})
	if err != nil {
		t.Fatalf("emitting: %v", err)
	}
	return out.Text
}

// runOnChip executes assembly on the game's own chip and returns what the
// program left in data slot zero, which is where every case here puts its
// answer.
func runOnChip(t *testing.T, assembly string) float64 {
	t.Helper()
	return runMemory(t, assembly)[0]
}

// runMemory executes assembly and returns the data region it left behind.
func runMemory(t *testing.T, assembly string) [ic10.NumMemorySlots]float64 {
	t.Helper()
	ctx, harness := chiptest.Harness(t)
	got, err := harness.Run(ctx, chip.Request{Source: assembly})
	if err != nil {
		t.Fatalf("running:\n%s\n%v", assembly, err)
	}
	if got.Stop != chip.StopEnded {
		t.Fatalf("the program stopped %q (compile %v, fault %v):\n%s",
			got.Stop, got.CompileError, got.Fault, assembly)
	}
	return got.Stack
}

// TestTargetLabelReportsAnUnknownSuccessor covers the branch whose destination
// no machine block was built for. Naming any other block assembles cleanly and
// sends control somewhere the program never asked to go, which nothing
// downstream catches.
func TestTargetLabelReportsAnUnknownSuccessor(t *testing.T) {
	s := &selector{
		pos:    llvmir.Positions{File: "test.c"},
		blocks: make(map[llvm.BasicBlock]*blockInfo),
		endLbl: "main.exit",
	}

	if target := s.targetLabel(llvm.BasicBlock{}, llvm.BasicBlock{}); target != nil {
		t.Errorf("a successor with no block resolved to %s, want no label at all", target)
	}
	if s.diags.Err() == nil {
		t.Fatalf("a successor with no block was resolved without a diagnostic")
	}
	if got := s.diags.String(); !strings.Contains(got, "laid out no code") {
		t.Errorf("the diagnostic does not say what went wrong: %s", got)
	}
}

// TestFallthroughJumpOverAnEmptyBlockIsDropped covers a jump whose target is not
// the next block laid out but the next one holding an instruction. A block with
// no instructions occupies no line, so its label resolves to the line the
// fallthrough already reaches and the jump is bytes spent on nothing.
func TestFallthroughJumpOverAnEmptyBlockIsDropped(t *testing.T) {
	m := parseIR(t, `
define void @main() {
entry:
  %slot = alloca i64
  %x = load i64, ptr %slot
  %c = icmp sgt i64 %x, 0
  br i1 %c, label %left, label %right

left:
  br label %join

right:
  br label %join

join:
  store i64 %x, ptr %slot
  ret void
}
`)
	result, err := Select(t.Context(), m, Options{File: "test.c"})
	if err != nil {
		t.Fatalf("Select: %v\n%s", err, m.String())
	}
	lines := render(result.Program.Funcs[0])
	if got := slices.Contains(mnemonics(lines), "j"); got {
		t.Errorf("a jump control would reach its target without survived:\n%s", strings.Join(lines, "\n"))
	}
}

// TestSelectReportsAMalformedProgramAsACompilerFault covers what a broken
// structural invariant is: a defect in this stage, not a construct in the source,
// so the caller must not print it against a source line. Labels resolve into one
// flat space, joined by a dot, so two functions can name one label.
func TestSelectReportsAMalformedProgramAsACompilerFault(t *testing.T) {
	m := parseIR(t, `
define void @main() {
entry:
  ret void
}

define void @a() {
b.c:
  ret void
}

define void @"a.b"() {
c:
  ret void
}
`)
	_, err := Select(t.Context(), m, Options{File: "test.c"})
	if err == nil {
		t.Fatalf("selection accepted a program with two blocks sharing one label:\n%s", m.String())
	}
	if !strings.Contains(err.Error(), "already defined") {
		t.Errorf("the failure does not name the duplicate label: %v", err)
	}
	if diags, ok := errors.AsType[source.DiagnosticList](err); ok {
		t.Errorf("an invariant of instruction selection reached the caller as a source diagnostic: %s", diags.String())
	}
}

// unnamedBlockResultSlot is where the cases below leave their answer: the seed
// takes slot 0 and the result slot 1.
const unnamedBlockResultSlot = 1

// TestSelectNamesBlocksTheOptimizerLeftUnnamed covers blocks reaching this stage
// with no name. A block the optimizer split, cloned or rebuilt carries a number
// in the printed module and nothing this stage can read, so a name shared across
// two is a program whose control flow is not what it was compiled from.
func TestSelectNamesBlocksTheOptimizerLeftUnnamed(t *testing.T) {
	cases := []struct {
		name string
		// build fills main with a branch over unnamed blocks, storing the
		// value each arm stands for into out.
		build func(bd *builder, out llvm.Value, cond llvm.Value)
		// want is what each seed leaves behind, taken first.
		want [2]float64
	}{
		{
			// Three unnamed blocks, which is the shape that collides: every one
			// of them is a machine block of its own.
			name: "two arms and the block they join at",
			build: func(bd *builder, out, cond llvm.Value) {
				first, second, join := bd.block(""), bd.block(""), bd.block("")
				bd.b.CreateCondBr(cond, first, second)
				bd.b.SetInsertPointAtEnd(first)
				bd.b.CreateStore(bd.konst(11), out)
				bd.b.CreateBr(join)
				bd.b.SetInsertPointAtEnd(second)
				bd.b.CreateStore(bd.konst(22), out)
				bd.b.CreateBr(join)
				bd.b.SetInsertPointAtEnd(join)
			},
			want: [2]float64{11, 22},
		},
		{
			// The edge block is named after its successor, so the successor
			// needs a name too.
			name: "an edge split into an unnamed successor",
			build: func(bd *builder, out, cond llvm.Value) {
				first, join := bd.block(""), bd.block("")
				entry := bd.b.GetInsertBlock()
				bd.b.CreateCondBr(cond, first, join)
				bd.b.SetInsertPointAtEnd(first)
				bd.b.CreateBr(join)
				bd.b.SetInsertPointAtEnd(join)
				merged := bd.b.CreatePHI(bd.i64, "")
				merged.AddIncoming([]llvm.Value{bd.konst(33), bd.konst(44)},
					[]llvm.BasicBlock{entry, first})
				bd.b.CreateStore(merged, out)
			},
			want: [2]float64{44, 33},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for i, seed := range []int64{1, -1} {
				bd := newBuilder(t)
				slot := bd.b.CreateAlloca(bd.i64, "seed")
				out := bd.b.CreateAlloca(bd.i64, "out")
				bd.b.CreateStore(bd.konst(seed), slot)
				cond := bd.b.CreateICmp(llvm.IntSGT, bd.b.CreateLoad(bd.i64, slot, ""), bd.konst(0), "")
				tc.build(bd, out, cond)
				bd.b.CreateRetVoid()

				assembly := assemble(t, bd.m)
				if got := runMemory(t, assembly)[unnamedBlockResultSlot]; got != tc.want[i] {
					t.Errorf("a seed of %d left %v, want %v:\n%s", seed, got, tc.want[i], assembly)
				}
			}
		})
	}
}
