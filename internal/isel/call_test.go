package isel

import (
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/regalloc"
	"github.com/greg2010/ic11c/internal/sema"
	"tinygo.org/x/go-llvm"
)

// programLines renders every function in emission order, which is what a branch
// target resolves against.
func programLines(prog *mir.Program) []string {
	var lines []string
	for _, fn := range prog.Funcs {
		lines = append(lines, render(fn)...)
	}
	return lines
}

func functionByName(t *testing.T, prog *mir.Program, name string) *mir.Func {
	t.Helper()
	for _, fn := range prog.Funcs {
		if fn.Name == name {
			return fn
		}
	}
	t.Fatalf("the program has no function %q", name)
	return nil
}

// leafIR calls a function that calls nothing, which is the two instruction case
// the inlining default is measured against.
const leafIR = `
define i64 @twice(i64 %n) {
entry:
  %r = mul i64 %n, 2
  ret i64 %r
}

define void @main() {
entry:
  %v = call i64 @twice(i64 21)
  %slot = alloca i64
  store i64 %v, ptr %slot
  ret void
}
`

// nonLeafIR calls a function that calls in turn, so the inner function has to
// keep its own return address across the call it makes.
const nonLeafIR = `
define i64 @inner(i64 %n) {
entry:
  %r = add i64 %n, 1
  ret i64 %r
}

define i64 @outer(i64 %n) {
entry:
  %r = call i64 @inner(i64 %n)
  ret i64 %r
}

define void @main() {
entry:
  %v = call i64 @outer(i64 1)
  %slot = alloca i64
  store i64 %v, ptr %slot
  ret void
}
`

// TestSelectCallShape pins the instructions the convention costs on each side.
func TestSelectCallShape(t *testing.T) {
	cases := []struct {
		name string
		ir   string
		// fn names the function whose instructions are checked.
		fn     string
		want   []string
		absent []string
	}{
		{
			name: "the caller moves its arguments and reads the result back",
			ir:   leafIR,
			fn:   "main",
			want: []string{"move r0 21", "jal twice.entry", "move vr0 r0"},
		},
		{
			name:   "a leaf returns through ra and saves nothing",
			ir:     leafIR,
			fn:     "twice",
			want:   []string{"move vr0 r0", "move r0 vr1", "j ra"},
			absent: []string{"push ra", "pop ra"},
		},
		{
			name: "a function that calls keeps its return address across it",
			ir:   nonLeafIR,
			fn:   "outer",
			want: []string{"push ra", "jal inner.entry", "pop ra", "j ra"},
		},
		{
			name:   "the entry point returns nothing and saves nothing",
			ir:     nonLeafIR,
			fn:     "main",
			absent: []string{"push ra", "pop ra", "j ra"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := selectProgram(t, tc.ir)
			lines := render(functionByName(t, result.Program, tc.fn))
			text := strings.Join(lines, "\n")
			for _, want := range tc.want {
				if !contains(lines, want) {
					t.Errorf("%s has no %q:\n%s", tc.fn, want, text)
				}
			}
			for _, absent := range tc.absent {
				if contains(lines, absent) {
					t.Errorf("%s still has %q:\n%s", tc.fn, absent, text)
				}
			}
		})
	}
}

// TestSelectPutsTheEntryFirst covers the ordering execution depends on: the
// chip starts at line 0, so whatever is emitted first runs first.
func TestSelectPutsTheEntryFirst(t *testing.T) {
	result := selectProgram(t, nonLeafIR)
	if got := result.Program.Funcs[0].Name; got != sema.EntryFunction {
		t.Errorf("the program starts with %q, want %q", got, sema.EntryFunction)
	}
	if !result.CallingConvention {
		t.Errorf("a program with real calls did not report the calling convention in use")
	}
}

// TestSelectEntryReturnLeavesTheProgram checks where the entry point's return
// goes. It must reach past the functions laid out after it, or control falls
// into a body that was never called.
func TestSelectEntryReturnLeavesTheProgram(t *testing.T) {
	result := selectProgram(t, nonLeafIR)
	lines := programLines(result.Program)

	entry := render(result.Program.Funcs[0])
	last := entry[len(entry)-1]
	target, found := strings.CutPrefix(last, "j ")
	if !found {
		t.Fatalf("the entry point ends with %q rather than a jump:\n%s", last, strings.Join(lines, "\n"))
	}

	// The label the entry returns to is the trailing empty block of the last
	// function, so nothing renders on its line and the program counter leaves.
	for _, fn := range result.Program.Funcs {
		for _, block := range fn.Blocks {
			if block.Label != target {
				continue
			}
			if len(block.Instrs) != 0 {
				t.Errorf("the entry point returns into %q, which holds %d instructions", target, len(block.Instrs))
			}
			if fn != result.Program.Funcs[len(result.Program.Funcs)-1] {
				t.Errorf("the entry point returns into %q, which is in %s rather than the last function", target, fn.Name)
			}
			return
		}
	}
	t.Errorf("the entry point returns to %q, which names no block:\n%s", target, strings.Join(lines, "\n"))
}

// TestSelectPrologueOnlyOnTheEntry keeps the zeroing where it can run once: a
// called function reached repeatedly must not carry it.
func TestSelectPrologueOnlyOnTheEntry(t *testing.T) {
	result := selectProgram(t, nonLeafIR)
	for i, fn := range result.Program.Funcs {
		zeroing := contains(mnemonics(render(fn)), "clr")
		if i == 0 && !zeroing {
			t.Errorf("the entry point has no zeroing prologue, and the program uses %d data slots", result.DataSlots)
		}
		if i != 0 && zeroing {
			t.Errorf("%s carries the zeroing prologue, which would run on every call", fn.Name)
		}
	}
}

// TestRecursiveFunctions covers the reentrancy question a fixed data slot turns
// on, over the call graph as it reaches this stage rather than as written.
func TestRecursiveFunctions(t *testing.T) {
	cases := []struct {
		name string
		ir   string
		want []string
	}{
		{
			name: "a chain of calls reaches nothing twice",
			ir:   nonLeafIR,
			want: nil,
		},
		{
			name: "a function that calls itself",
			ir: `
define void @loop() {
entry:
  call void @loop()
  ret void
}
define void @main() {
entry:
  call void @loop()
  ret void
}
`,
			want: []string{"loop"},
		},
		{
			name: "a cycle of two",
			ir: `
define void @ping() {
entry:
  call void @pong()
  ret void
}
define void @pong() {
entry:
  call void @ping()
  ret void
}
define void @main() {
entry:
  call void @ping()
  ret void
}
`,
			want: []string{"ping", "pong"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := parseIR(t, tc.ir)
			var defined []llvm.Value
			for fn := m.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
				if !fn.IsDeclaration() {
					defined = append(defined, fn)
				}
			}
			got := recursiveFunctions(defined)
			if len(got) != len(tc.want) {
				t.Errorf("found %v, want %v", got, tc.want)
			}
			for _, name := range tc.want {
				if !got[name] {
					t.Errorf("%s can reach itself and was not reported: %v", name, got)
				}
			}
		})
	}
}

// TestSelectRejectsADataSlotInARecursiveFunction covers the storage a frame
// cannot supply. The data region is laid out at compile time, so an
// address-taken local has one address for every activation.
func TestSelectRejectsADataSlotInARecursiveFunction(t *testing.T) {
	m := parseIR(t, `
define void @loop(i64 %n) {
entry:
  %held = alloca i64
  store i64 %n, ptr %held
  call void @loop(i64 %n)
  %v = load i64, ptr %held
  store i64 %v, ptr %held
  ret void
}
define void @main() {
entry:
  call void @loop(i64 3)
  ret void
}
`)
	_, err := Select(t.Context(), m, Options{File: "test.c"})
	if err == nil {
		t.Fatalf("Select accepted a fixed slot in a function that re-enters")
	}
	if !strings.Contains(err.Error(), "activation") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// TestConventionRegistersAreNotScratchRegisters pins the one coupling between
// this package's calling convention and the allocator's scratch set: a reload
// into a convention register overwrites an argument or result with nothing to
// report it. It lives here because both sets are only visible from this package.
func TestConventionRegistersAreNotScratchRegisters(t *testing.T) {
	convention := []ic10.Register{resultRegister}
	for i := range maxCallArgs {
		convention = append(convention, argRegister(i))
	}
	// The result register is also the first argument register, so the set is
	// compacted before it is named in a failure.
	convention = slices.Compact(slices.Sorted(slices.Values(convention)))
	scratch := regalloc.DefaultScratch()

	for _, reg := range convention {
		if slices.Contains(scratch, reg) {
			t.Errorf("%s carries a call argument or result and is also a scratch register (convention %v, scratch %v); the two sets are coupled and must stay disjoint, so either lower maxCallArgs or move regalloc.DefaultScratch off this register",
				reg, convention, scratch)
		}
	}
}

// TestCallingConventionFollowsTheSelectedCalls holds Result.CallingConvention to
// what it documents. The module's function count answers a different question:
// the optimizer leaves behind definitions whose every call it inlined, and a
// self-recursive function needs the convention with no second definition around.
func TestCallingConventionFollowsTheSelectedCalls(t *testing.T) {
	tests := []struct {
		name string
		ir   string
		want bool
	}{
		{
			name: "one function calling nothing",
			ir: `
define void @main() {
entry:
  %slot = alloca i64
  %x = load i64, ptr %slot
  %y = add i64 %x, 1
  store i64 %y, ptr %slot
  ret void
}
`,
		},
		{
			name: "a second definition nothing calls",
			ir: `
define i64 @unused(i64 %x) {
entry:
  %y = add i64 %x, 1
  ret i64 %y
}

define void @main() {
entry:
  %slot = alloca i64
  %x = load i64, ptr %slot
  store i64 %x, ptr %slot
  ret void
}
`,
		},
		{
			name: "a call that was selected",
			ir: `
define i64 @plus(i64 %x) {
entry:
  %y = add i64 %x, 1
  ret i64 %y
}

define void @main() {
entry:
  %slot = alloca i64
  %x = load i64, ptr %slot
  %r = call i64 @plus(i64 %x)
  store i64 %r, ptr %slot
  ret void
}
`,
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Select(t.Context(), parseIR(t, tt.ir), Options{File: "test.c"})
			if err != nil {
				t.Fatalf("Select: %v", err)
			}
			if result.CallingConvention != tt.want {
				t.Errorf("CallingConvention is %v, want %v:\n%s",
					result.CallingConvention, tt.want, strings.Join(render(result.Program.Funcs[0]), "\n"))
			}
		})
	}
}
