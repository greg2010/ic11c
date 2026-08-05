package llvmir

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/source"
	"tinygo.org/x/go-llvm"
)

// walkIR holds every shape the walks have to answer for: two definitions, a
// definition of more than one block, a block of more than one instruction, and
// a declaration with no body at all.
const walkIR = `
declare void @outside()

define void @first() {
entry:
  %slot = alloca i64
  store i64 1, ptr %slot
  br label %tail

tail:
  ret void
}

define void @second() {
entry:
  ret void
}
`

// debugIR carries a function with a subprogram and an instruction with a
// location, alongside one of each without. The recorded file differs from any
// name a caller would pass, so the two can be told apart.
const debugIR = `
define void @located() !dbg !4 {
entry:
  %slot = alloca i64
  store i64 1, ptr %slot, !dbg !6
  ret void
}

define void @bare() {
entry:
  ret void
}

!llvm.module.flags = !{!0}
!llvm.dbg.cu = !{!1}
!0 = !{i32 2, !"Debug Info Version", i32 3}
!1 = distinct !DICompileUnit(language: DW_LANG_C99, file: !2, producer: "ic11c", isOptimized: true, emissionKind: FullDebug)
!2 = !DIFile(filename: "recorded.c", directory: ".")
!3 = !DISubroutineType(types: !5)
!4 = distinct !DISubprogram(name: "located", scope: !2, file: !2, line: 12, type: !3, scopeLine: 12, spFlags: DISPFlagDefinition, unit: !1)
!5 = !{null}
!6 = !DILocation(line: 14, column: 3, scope: !4)
`

func TestModuleInstrs(t *testing.T) {
	tests := []struct {
		name string
		ir   string
		want []string
	}{
		{
			name: "every definition in layout order, and no declaration",
			ir:   walkIR,
			want: []string{"first/alloca", "first/store", "first/br", "first/ret", "second/ret"},
		},
		{
			name: "a module of declarations alone",
			ir:   "declare void @outside()\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for fn, in := range ModuleInstrs(parseIR(t, tt.ir)) {
				got = append(got, fn.Name()+"/"+opcodeName(t, in))
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("ModuleInstrs walked %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFuncInstrs(t *testing.T) {
	tests := []struct {
		name string
		fn   string
		want []string
	}{
		{name: "a definition of two blocks", fn: "first", want: []string{"alloca", "store", "br", "ret"}},
		{name: "a definition of one instruction", fn: "second", want: []string{"ret"}},
		{name: "a declaration has no body", fn: "outside"},
	}
	m := parseIR(t, walkIR)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for in := range FuncInstrs(m.NamedFunction(tt.fn)) {
				got = append(got, opcodeName(t, in))
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("FuncInstrs walked %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBlockInstrs(t *testing.T) {
	m := parseIR(t, walkIR)
	var got []string
	for in := range BlockInstrs(m.NamedFunction("first").EntryBasicBlock()) {
		got = append(got, opcodeName(t, in))
	}
	want := []string{"alloca", "store", "br"}
	if !slices.Equal(got, want) {
		t.Errorf("BlockInstrs walked %v, want %v", got, want)
	}
}

// TestWalksStopWhenTheConsumerDoes covers the early return each walk carries.
// A consumer that breaks out part way must leave the walk with nothing still
// iterating over a module it is done with.
func TestWalksStopWhenTheConsumerDoes(t *testing.T) {
	m := parseIR(t, walkIR)
	first := m.NamedFunction("first")

	tests := []struct {
		name string
		walk func(visit func()) // calls visit once per instruction until it breaks
	}{
		{
			name: "module",
			walk: func(visit func()) {
				for range ModuleInstrs(m) {
					visit()
					break
				}
			},
		},
		{
			name: "function",
			walk: func(visit func()) {
				for range FuncInstrs(first) {
					visit()
					break
				}
			},
		},
		{
			name: "block",
			walk: func(visit func()) {
				for range BlockInstrs(first.EntryBasicBlock()) {
					visit()
					break
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seen := 0
			tt.walk(func() { seen++ })
			if seen != 1 {
				t.Errorf("the walk yielded %d instructions after a break, want 1", seen)
			}
		})
	}
}

func TestPositions(t *testing.T) {
	m := parseIR(t, debugIR)
	located := m.NamedFunction("located")
	bare := m.NamedFunction("bare")
	// The store is the one instruction carrying a location; the alloca before it
	// carries none.
	alloca := located.EntryBasicBlock().FirstInstruction()
	store := llvm.NextInstruction(alloca)

	lines := source.NewLineMap(strings.Repeat("xxxx\n", 20))

	t.Run("an instruction with a location", func(t *testing.T) {
		p := Positions{File: "given.c", Lines: lines}
		pos, ok := p.Instr(store)
		if !ok {
			t.Fatalf("Instr reported no location for an instruction carrying one")
		}
		// Line 14 column 3 of five-byte lines: 13 whole lines, then two bytes.
		want := source.Position{File: "given.c", Offset: 67, Line: 14, Column: 3}
		if pos != want {
			t.Errorf("Instr = %+v, want %+v", pos, want)
		}
	})

	t.Run("an instruction with no location", func(t *testing.T) {
		p := Positions{File: "given.c", Lines: lines}
		if pos, ok := p.Instr(alloca); ok {
			t.Errorf("Instr = %+v, want no location", pos)
		}
	})

	t.Run("a nil line map leaves the offset at zero", func(t *testing.T) {
		p := Positions{File: "given.c"}
		pos, _ := p.Instr(store)
		want := source.Position{File: "given.c", Line: 14, Column: 3}
		if pos != want {
			t.Errorf("Instr = %+v, want %+v", pos, want)
		}
	})

	t.Run("File wins over the file the subprogram names", func(t *testing.T) {
		got := Positions{File: "given.c"}.Func(located)
		want := source.Position{File: "given.c", Line: 12}
		if got != want {
			t.Errorf("Func = %+v, want %+v", got, want)
		}
	})

	t.Run("no File falls back to the subprogram's", func(t *testing.T) {
		got := Positions{}.Func(located)
		want := source.Position{File: "recorded.c", Line: 12}
		if got != want {
			t.Errorf("Func = %+v, want %+v", got, want)
		}
	})

	t.Run("a definition with no subprogram is no position at all", func(t *testing.T) {
		for _, p := range []Positions{{}, {File: "given.c"}} {
			got := p.Func(bare)
			if got.IsValid() {
				t.Errorf("Func = %+v for a definition with no subprogram, want an invalid position", got)
			}
			if got.File != p.File {
				t.Errorf("Func = %+v, want the file left at %q", got, p.File)
			}
		}
	})
}

func opcodeName(t *testing.T, in llvm.Value) string {
	t.Helper()
	// An instruction renders as "  %x = alloca i64" or "  ret void", so the
	// mnemonic is the first word after any assignment.
	text := strings.TrimSpace(in.String())
	if _, after, assigned := strings.Cut(text, " = "); assigned {
		text = after
	}
	name, _, _ := strings.Cut(text, " ")
	return name
}

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
