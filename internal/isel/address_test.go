package isel

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"tinygo.org/x/go-llvm"
)

// foldedOffsetIR subscripts a global at a runtime index and a constant one, so
// the constant offset folds into the literal base while the runtime index keeps
// the address off the compile-time path a wholly constant subscript takes.
func foldedOffsetIR(offset string) string {
	return fmt.Sprintf(`
@table = internal global [4 x i64] zeroinitializer

define void @main() {
entry:
  %%islot = alloca i64
  %%i = load i64, ptr %%islot
  %%p = getelementptr [4 x i64], ptr @table, i64 %%i, i64 %s
  store i64 42, ptr %%p
  ret void
}
`, offset)
}

// TestSelectRefusesAFoldedOffsetOutsideTheArray covers the slot bound on the
// path that folds a literal base and a literal offset into one literal. Only
// the base went through the address resolver, so a sum past the last slot
// reaches the emitted line as a literal poke answers with a stack overflow.
func TestSelectRefusesAFoldedOffsetOutsideTheArray(t *testing.T) {
	tests := []struct {
		name   string
		offset string
		want   []string
	}{
		{
			name:   "past the last slot",
			offset: "600",
			want:   []string{"memory slot 600", "0 through 511"},
		},
		{
			name:   "before the first",
			offset: "-8",
			want:   []string{"memory slot -8", "0 through 511"},
		},
		{
			// mir.Imm renders with %g, so this states in exponent notation,
			// which no operand position takes.
			name:   "far enough out to reach exponent notation",
			offset: "1000000000000000000",
			want:   []string{"memory slot 1000000000000000000"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := parseIR(t, foldedOffsetIR(tt.offset))
			_, err := Select(t.Context(), m, Options{File: "test.c"})
			if err == nil {
				t.Fatalf("selection accepted a memory slot outside the array:\n%s", m.String())
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not mention %q: %v", want, err)
				}
			}
		})
	}
}

// TestSelectAcceptsAFoldedOffsetInsideTheArray is the other side of the bound:
// the fold still has to happen, so a constant index costs no instruction.
func TestSelectAcceptsAFoldedOffsetInsideTheArray(t *testing.T) {
	lines := render(selectProgram(t, foldedOffsetIR("2")).Program.Funcs[0])
	got := mnemonics(lines)
	adds := 0
	for _, mnemonic := range got {
		if mnemonic == "add" {
			adds++
		}
	}
	if adds != 1 {
		t.Errorf("selected %d adds, want the one the runtime index needs: %v", adds, lines)
	}
	if !contains(lines, "add vr1 2 vr2") {
		t.Errorf("the constant offset did not fold into the literal base: %v", lines)
	}
}

// literalIndexIR reaches a global array at a compile-time index, through the
// getelementptr a subscript on a decayed pointer folds to. The second global is
// what a subscript past the first one's length lands in.
func literalIndexIR(index int, write bool) string {
	access := "%v = load i64, ptr %p\n  store i64 %v, ptr @b"
	if write {
		access = "store i64 42, ptr %p"
	}
	return fmt.Sprintf(`
@a = internal global [4 x i64] zeroinitializer
@b = internal global [4 x i64] zeroinitializer

define void @main() {
entry:
  %%p = getelementptr [4 x i64], ptr @a, i64 0, i64 %d
  %s
  ret void
}
`, index, access)
}

// TestSelectRefusesALiteralIndexOutsideTheObject holds a compile-time index
// against the length of the object it starts from. The machine cannot apply that
// bound — the data region has no boundary between objects — and the 512 slot
// bound passes anything in the array, including the return address save slot.
func TestSelectRefusesALiteralIndexOutsideTheObject(t *testing.T) {
	tests := []struct {
		name  string
		index int
		write bool
		want  []string
	}{
		{
			name:  "one past the last element",
			index: 4,
			write: true,
			want:  []string{"this assignment", "element 4", "'a'", "4 elements"},
		},
		{
			name:  "far enough to reach the next global",
			index: 7,
			write: true,
			want:  []string{"element 7", "'a'", "4 elements"},
		},
		{
			name:  "before the first element",
			index: -1,
			write: true,
			want:  []string{"element -1", "'a'", "4 elements"},
		},
		{
			name:  "a read rather than a write",
			index: 4,
			want:  []string{"this read of a variable", "element 4", "'a'"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := parseIR(t, literalIndexIR(tt.index, tt.write))
			_, err := Select(t.Context(), m, Options{File: "test.c"})
			if err == nil {
				t.Fatalf("selection accepted an index outside the object:\n%s", m.String())
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not mention %q: %v", want, err)
				}
			}
		})
	}
}

// TestSelectAcceptsALiteralIndexInsideTheObject is the other side of that
// bound. The last element is the case an off-by-one in the check would refuse.
func TestSelectAcceptsALiteralIndexInsideTheObject(t *testing.T) {
	for _, index := range []int{0, 1, 3} {
		lines := render(selectProgram(t, literalIndexIR(index, true)).Program.Funcs[0])
		if want := fmt.Sprintf("poke %d 42", index); !contains(lines, want) {
			t.Errorf("selecting a[%d] gave %v, want a %q", index, lines, want)
		}
	}
}

func TestSelectMemory(t *testing.T) {
	bd := newBuilder(t)
	global := llvm.AddGlobal(bd.m, bd.i64, "g")
	global.SetInitializer(llvm.ConstInt(bd.i64, 0, false))
	local := bd.b.CreateAlloca(bd.i64, "x")
	bd.b.CreateStore(bd.konst(7), local)
	bd.b.CreateStore(bd.b.CreateLoad(bd.i64, local, "x"), global)
	bd.b.CreateRetVoid()

	result, err := Select(t.Context(), bd.m, Options{File: "test.c"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	got := render(result.Program.Funcs[0])
	for _, want := range []string{"clr db", "poke 1 7", "get vr0 db 1", "poke 0 vr0"} {
		if !contains(got, want) {
			t.Errorf("selected %v, want a %q", got, want)
		}
	}
	if result.DataSlots != 2 {
		t.Errorf("DataSlots = %d, want 2: one global and one local", result.DataSlots)
	}
}

// TestSelectSubscriptDividesTheStrideOut is the byte-budget half of address
// arithmetic. One element is one slot, so the stride is a compile time constant
// and nothing that scales an index may reach the program.
func TestSelectSubscriptDividesTheStrideOut(t *testing.T) {
	cases := []struct {
		name string
		// load reads one element, over the element type the optimizer leaves.
		load string
	}{
		{
			name: "a typed element stride",
			load: `%p = getelementptr inbounds i64, ptr @table, i64 %i
  %v = load i64, ptr %p`,
		},
		{
			name: "a constant index folded to a byte offset",
			load: `%v = load i64, ptr getelementptr inbounds nuw (i8, ptr @table, i64 16)`,
		},
		{
			name: "an index into an array type",
			load: `%p = getelementptr inbounds [4 x i64], ptr @table, i64 0, i64 %i
  %v = load i64, ptr %p`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := parseIR(t, `
@table = internal global [8 x i64] zeroinitializer

define void @main() {
entry:
  %slot = alloca i64
  %i = load i64, ptr %slot
  `+tc.load+`
  store i64 %v, ptr %slot
  ret void
}
`)
			result, err := Select(t.Context(), m, Options{File: "test.c"})
			if err != nil {
				t.Fatalf("Select: %v\n%s", err, m.String())
			}
			got := mnemonics(render(result.Program.Funcs[0]))
			for _, scaling := range []string{"div", "mul", "sll", "srl", "sra", "mod", "trunc"} {
				if contains(got, scaling) {
					t.Errorf("selected %s for a subscript, so the element stride survived: %v", scaling, got)
				}
			}
		})
	}
}

// TestSelectPointerMergingToOneObject is what internal/pointers lets through: a
// phi whose arms name different elements of one array resolves, each to a
// literal slot rather than a computation.
func TestSelectPointerMergingToOneObject(t *testing.T) {
	m := parseIR(t, `
@table = internal global [4 x i64] zeroinitializer

define void @main() {
entry:
  %slot = alloca i64
  %x = load i64, ptr %slot
  %c = icmp sgt i64 %x, 0
  br i1 %c, label %high, label %low
high:
  br label %join
low:
  br label %join
join:
  %p = phi ptr [ getelementptr inbounds (i64, ptr @table, i64 1), %high ],
               [ getelementptr inbounds (i64, ptr @table, i64 2), %low ]
  %v = load i64, ptr %p
  store i64 %v, ptr %slot
  ret void
}
`)
	result, err := Select(t.Context(), m, Options{File: "test.c"})
	if err != nil {
		t.Fatalf("Select: %v\n%s", err, m.String())
	}
	got := render(result.Program.Funcs[0])
	for _, want := range []string{"move vr1 1", "move vr1 2", "get vr2 db vr1"} {
		if !contains(got, want) {
			t.Errorf("selected %v, want a %q", got, want)
		}
	}
}

// TestSelectRefusesMemorySlotsOutsideTheArray covers the address the chip
// answers with the unknown error for get and a stack overflow for put. The
// element is inside the object it starts from, which is the only way past the
// array bound: reaching it needs a data region that does not fit.
func TestSelectRefusesMemorySlotsOutsideTheArray(t *testing.T) {
	cases := []struct {
		name  string
		index string
	}{
		{name: "past the end of the array", index: "600"},
		{name: "the last element of an object that does not fit", index: "699"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := parseIR(t, `
@table = internal global [700 x i64] zeroinitializer

define void @main() {
entry:
  %p = getelementptr inbounds i64, ptr @table, i64 `+tc.index+`
  store i64 1, ptr %p
  ret void
}
`)
			_, err := Select(t.Context(), m, Options{File: "test.c"})
			if err == nil {
				t.Fatalf("selection accepted a memory slot outside the array:\n%s", m.String())
			}
			for _, want := range []string{"memory slot " + tc.index, "0 through 511"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not mention %q: %v", want, err)
				}
			}
		})
	}
}

// arrayIR fills an eight element array, reads one element at a runtime index,
// and writes a marker back over it. The program writes its own contents because
// the zeroing prologue would wipe a seeded data region before the body runs, and
// the index goes through memory so that it stays a runtime value.
func arrayIR(index int) string {
	var b strings.Builder
	b.WriteString("define void @main() {\nentry:\n")
	b.WriteString("  %table = alloca [8 x i64]\n  %index = alloca i64\n  %out = alloca i64\n")
	for i := range arrayLen {
		fmt.Fprintf(&b, "  %%e%d = getelementptr inbounds i64, ptr %%table, i64 %d\n", i, i)
		fmt.Fprintf(&b, "  store i64 %d, ptr %%e%d\n", arrayElement(i), i)
	}
	fmt.Fprintf(&b, "  store i64 %d, ptr %%index\n", index)
	b.WriteString(`  %i = load i64, ptr %index
  %read = getelementptr inbounds i64, ptr %table, i64 %i
  %v = load i64, ptr %read
  store i64 %v, ptr %out
  store i64 -1, ptr %read
  ret void
}
`)
	return b.String()
}

// arrayElement gives element i a value distinct from every other, from the
// marker, and from every index, so a wrong slot cannot give the right answer.
func arrayElement(i int) int { return 100 + 7*i }

const (
	arrayBase  = 0
	arrayLen   = 8
	resultSlot = 9
	marker     = -1
)

// TestSubscriptReachesTheSlotItNames executes the address computation against
// memory: a wrong base or stride assembles, passes every mnemonic check, and
// reads the wrong slot. The store is checked as well as the load, since a read
// from the right slot says nothing about where the write went.
func TestSubscriptReachesTheSlotItNames(t *testing.T) {
	for index := range arrayLen {
		assembly := assemble(t, parseIR(t, arrayIR(index)))
		final := runMemory(t, assembly)
		if got, want := final[resultSlot], float64(arrayElement(index)); got != want {
			t.Errorf("reading element %d gave %v, want %v:\n%s", index, got, want, assembly)
		}
		for slot := range arrayLen {
			want := float64(arrayElement(slot))
			if slot == index {
				want = marker
			}
			if got := final[arrayBase+slot]; got != want {
				t.Errorf("writing element %d left slot %d holding %v, want %v:\n%s",
					index, slot, got, want, assembly)
			}
		}
	}
}

const (
	// tableCols is the row stride, which makes the subscript a scaled index.
	tableCols = 3
	tableRows = 4
	// The table takes slots 0 through 11, the row index slot 12, and what the
	// program read lands at slot 13.
	byteSubscriptIndexSlot  = tableRows * tableCols
	byteSubscriptResultSlot = byteSubscriptIndexSlot + 1
)

// tableElement gives each element a value distinct from every other and from
// every index, so a wrong row or column cannot give the right answer.
func tableElement(row, col int) int { return 100 + 7*(row*tableCols+col) }

// byteOffsetSubscriptIR subscripts a table at a runtime row and a constant
// column, with the whole offset in bytes, which is the shape -Oz leaves a
// two-dimensional subscript in. The byte offset is eight times the slot meant,
// so a stride taken off the element type computes an address in the first row.
func byteOffsetSubscriptIR(row, col int, offset string) string {
	var b strings.Builder
	b.WriteString("@table = internal global [")
	fmt.Fprintf(&b, "%d x i64] zeroinitializer\n\ndefine void @main() {\nentry:\n", tableRows*tableCols)
	b.WriteString("  %index = alloca i64\n  %out = alloca i64\n")
	for r := range tableRows {
		for c := range tableCols {
			fmt.Fprintf(&b, "  %%e%d_%d = getelementptr inbounds i64, ptr @table, i64 %d\n", r, c, r*tableCols+c)
			fmt.Fprintf(&b, "  store i64 %d, ptr %%e%d_%d\n", tableElement(r, c), r, c)
		}
	}
	fmt.Fprintf(&b, "  store i64 %d, ptr %%index\n", row)
	b.WriteString("  %row = load i64, ptr %index\n")
	b.WriteString("  " + offset + "\n")
	fmt.Fprintf(&b, `  %%start = getelementptr i8, ptr @table, i64 %%bytes
  %%elem = getelementptr i8, ptr %%start, i64 %d
  %%v = load i64, ptr %%elem
  store i64 %%v, ptr %%out
  ret void
}
`, col*ic10.SlotBytes)
	return b.String()
}

// TestSubscriptAtAByteOffsetReachesTheSlotItNames executes a subscript whose
// offset survived optimization as a byte count. The element type a
// getelementptr names is not the authority on how its offset divides into
// slots: -Oz restates an offset over i8 wherever that buys it anything.
func TestSubscriptAtAByteOffsetReachesTheSlotItNames(t *testing.T) {
	const rowBytes = tableCols * ic10.SlotBytes

	cases := []struct {
		name string
		// offset computes %bytes, the byte distance from the start of the table
		// to the start of the row in %row.
		offset string
	}{
		{
			name:   "a row stride folded into the index as a byte count",
			offset: fmt.Sprintf("%%bytes = mul nsw i64 %%row, %d", rowBytes),
		},
		{
			name:   "the same multiplication written the other way round",
			offset: fmt.Sprintf("%%bytes = mul nsw i64 %d, %%row", rowBytes),
		},
		{
			name: "a byte count the row and a doubling of it add up to",
			offset: fmt.Sprintf(`%%once = mul nsw i64 %%row, %d
  %%twice = mul nsw i64 %%row, %d
  %%bytes = add nsw i64 %%once, %%twice`, ic10.SlotBytes, 2*ic10.SlotBytes),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for row := range tableRows {
				for col := range tableCols {
					assembly := assemble(t, parseIR(t, byteOffsetSubscriptIR(row, col, tc.offset)))
					got := runMemory(t, assembly)[byteSubscriptResultSlot]
					if want := float64(tableElement(row, col)); got != want {
						t.Errorf("reading row %d column %d gave %v, want %v:\n%s", row, col, got, want, assembly)
					}
				}
			}
		})
	}
}

// TestSubscriptAtAByteOffsetSpendsNoLineOnTheStride checks the size half of the
// same address. Materialising the byte offset and dividing afterwards computes
// the same slot and costs two instructions more, inside whatever loop it sits in.
func TestSubscriptAtAByteOffsetSpendsNoLineOnTheStride(t *testing.T) {
	assembly := assemble(t, parseIR(t, byteOffsetSubscriptIR(1, 2,
		fmt.Sprintf("%%bytes = mul nsw i64 %%row, %d", tableCols*ic10.SlotBytes))))
	for _, dividing := range []string{"div", "srl", "sra", "mod"} {
		for line := range strings.SplitSeq(assembly, "\n") {
			if mnemonic, _, _ := strings.Cut(strings.TrimSpace(line), " "); mnemonic == dividing {
				t.Errorf("the subscript emitted %q, so the byte stride reached the program:\n%s", dividing, assembly)
			}
		}
	}
}

// TestSelectRefusesAByteOffsetThatIsNotWholeSlots is the other side of the same
// rule. The machine has no address for part of a slot, so an offset the walk
// cannot resolve into whole slots is refused rather than rounded.
func TestSelectRefusesAByteOffsetThatIsNotWholeSlots(t *testing.T) {
	cases := []struct {
		name   string
		offset string
	}{
		{
			name:   "a stride that is not a whole number of slots",
			offset: "%bytes = mul nsw i64 %row, 12",
		},
		{
			name:   "a byte offset nothing scaled at all",
			offset: "%bytes = add nsw i64 %row, 0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := parseIR(t, byteOffsetSubscriptIR(0, 0, tc.offset))
			_, err := Select(t.Context(), m, Options{File: "test.c"})
			if err == nil {
				t.Fatalf("the offset was accepted:\n%s", m.String())
			}
			if !strings.Contains(err.Error(), "whole memory slots") {
				t.Errorf("the diagnostic does not say what went wrong: %v", err)
			}
		})
	}
}

const (
	// maskedCells is the length of the table [maskedOffsetIR] builds. The masks
	// below reach every element, so a case landing on the wrong one still lands
	// inside the table and is read rather than refused.
	maskedCells = 16
	// maskedIndexSlot and maskedResultSlot follow the table in the data region.
	maskedIndexSlot  = maskedCells
	maskedResultSlot = maskedIndexSlot + 1
)

// maskedCell gives each element a value distinct from every other and from
// every index it could be reached by.
func maskedCell(index int) int { return 200 + 3*index }

// maskedOffsetIR reads one element at a byte offset a constant mask bounds, over
// the seed in %i, which is what -Oz leaves a bounded index in. The mask is over
// the byte offset rather than the index, so the stride's factors of two are the
// mask's low zero bits and there is no multiply or shift to read it off.
func maskedOffsetIR(seed int, offset string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "@table = internal global [%d x i64] zeroinitializer\n\ndefine void @main() {\nentry:\n", maskedCells)
	b.WriteString("  %index = alloca i64\n  %out = alloca i64\n")
	for cell := range maskedCells {
		fmt.Fprintf(&b, "  %%e%d = getelementptr inbounds i64, ptr @table, i64 %d\n", cell, cell)
		fmt.Fprintf(&b, "  store i64 %d, ptr %%e%d\n", maskedCell(cell), cell)
	}
	fmt.Fprintf(&b, "  store i64 %d, ptr %%index\n", seed)
	b.WriteString("  %i = load i64, ptr %index\n")
	b.WriteString("  " + offset + `
  %elem = getelementptr i8, ptr @table, i64 %bytes
  %v = load i64, ptr %elem
  store i64 %v, ptr %out
  ret void
}
`)
	return b.String()
}

// TestMaskedByteOffsetReachesTheSlotItNames executes a subscript whose byte
// offset a mask bounds. What divides the stride out is the mask's low zero bits,
// which say the value is a multiple of a power of two however the program runs,
// so the division that follows is exact for every index and not just these.
func TestMaskedByteOffsetReachesTheSlotItNames(t *testing.T) {
	cases := []struct {
		name string
		// offset computes %bytes, the byte distance from the start of the table.
		offset string
		// cell gives the element a seed reaches, in the source language's own
		// arithmetic.
		cell func(seed int) int
	}{
		{
			name: "a mask whose low zero bits are the whole stride",
			offset: `%s = shl i64 %i, 4
  %bytes = and i64 %s, 112`,
			cell: func(seed int) int { return (seed & 7) * 2 },
		},
		{
			name: "the mask written with the constant first",
			offset: `%s = shl i64 %i, 4
  %bytes = and i64 112, %s`,
			cell: func(seed int) int { return (seed & 7) * 2 },
		},
		{
			name: "a mask carrying part of the stride and a scale the rest",
			offset: `%m = and i64 %i, 12
  %bytes = mul nsw i64 %m, 2`,
			cell: func(seed int) int { return (seed & 12) / 4 },
		},
		{
			name: "a scale that both divides and multiplies",
			offset: `%m = and i64 %i, 6
  %bytes = mul nsw i64 %m, 12`,
			cell: func(seed int) int { return (seed & 6) / 2 * 3 },
		},
		{
			name: "a masked term the expression subtracts",
			offset: `%s = shl i64 %i, 4
  %m = and i64 %s, 112
  %bytes = sub nsw i64 112, %m`,
			cell: func(seed int) int { return 14 - (seed&7)*2 },
		},
		{
			name: "a masked term added to one the stride divides",
			offset: `%m = and i64 %i, 48
  %bytes = add nsw i64 %m, 8`,
			cell: func(seed int) int { return (seed&48)/8 + 1 },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for seed := range maskedCells {
				assembly := assemble(t, parseIR(t, maskedOffsetIR(seed, tc.offset)))
				got := runMemory(t, assembly)[maskedResultSlot]
				if want := float64(maskedCell(tc.cell(seed))); got != want {
					t.Errorf("a seed of %d read %v, want element %d holding %v:\n%s",
						seed, got, tc.cell(seed), want, assembly)
				}
			}
		})
	}
}

// TestSelectRefusesAMaskThatLeavesPartOfTheStride is the boundary the mask buys
// nothing past. A mask short of the stride leaves an offset landing inside a
// slot for some of the values it admits, which is refused rather than rounded.
func TestSelectRefusesAMaskThatLeavesPartOfTheStride(t *testing.T) {
	cases := []struct {
		name   string
		offset string
	}{
		{
			name:   "a mask one bit short of a whole slot",
			offset: "%bytes = and i64 %i, 12",
		},
		{
			name: "a mask nothing bounds",
			offset: `%j = load i64, ptr %index
  %bytes = and i64 %i, %j`,
		},
		{
			name: "a masked term scaled back under a whole slot",
			offset: `%m = and i64 %i, 48
  %bytes = sdiv i64 %m, 2`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := parseIR(t, maskedOffsetIR(0, tc.offset))
			_, err := Select(t.Context(), m, Options{File: "test.c"})
			if err == nil {
				t.Fatalf("the offset was accepted:\n%s", m.String())
			}
			if !strings.Contains(err.Error(), "whole memory slots") {
				t.Errorf("the diagnostic does not say what went wrong: %v", err)
			}
		})
	}
}

// signedResultSlot is where [signedOffsetIR] leaves what it read: the table
// takes the first slots, the two seeds follow it, and the answer follows them.
const signedResultSlot = maskedCells + 2

// signedOffsetIR reads one element at a byte offset built from two runtime
// values, one of which the offset subtracts. A subscript stated over its element
// type never subtracts an index; a byte offset does, because -Oz reassociates
// one into the difference of two scaled values and states it over i8.
func signedOffsetIR(x, y int, offset string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "@table = internal global [%d x i64] zeroinitializer\n\ndefine void @main() {\nentry:\n", maskedCells)
	b.WriteString("  %xslot = alloca i64\n  %yslot = alloca i64\n  %out = alloca i64\n")
	for cell := range maskedCells {
		fmt.Fprintf(&b, "  %%e%d = getelementptr inbounds i64, ptr @table, i64 %d\n", cell, cell)
		fmt.Fprintf(&b, "  store i64 %d, ptr %%e%d\n", maskedCell(cell), cell)
	}
	fmt.Fprintf(&b, "  store i64 %d, ptr %%xslot\n  store i64 %d, ptr %%yslot\n", x, y)
	b.WriteString("  %x = load i64, ptr %xslot\n  %y = load i64, ptr %yslot\n")
	b.WriteString("  " + offset + `
  %elem = getelementptr i8, ptr @table, i64 %bytes
  %v = load i64, ptr %elem
  store i64 %v, ptr %out
  ret void
}
`)
	return b.String()
}

// TestSubscriptSubtractsTheTermsTheOffsetSubtracts executes a subscript whose
// byte offset subtracts one of its terms. The decomposition records that as a
// negative scale, so a plan that adds every term computes a sum where the
// program asked for a difference and reads a slot it never named.
func TestSubscriptSubtractsTheTermsTheOffsetSubtracts(t *testing.T) {
	cases := []struct {
		name string
		// offset computes %bytes from %x and %y.
		offset string
		// cell gives the element the pair reaches.
		cell func(x, y int) int
	}{
		{
			name: "one scaled term subtracted from another",
			offset: `%a = mul nsw i64 %x, 8
  %b = mul nsw i64 %y, 8
  %bytes = sub nsw i64 %a, %b`,
			cell: func(x, y int) int { return x - y },
		},
		{
			name: "a subtracted term reached before any the offset adds",
			offset: `%a = mul nsw i64 %x, 8
  %b = mul nsw i64 %y, 8
  %na = sub nsw i64 0, %a
  %bytes = add nsw i64 %na, %b`,
			cell: func(x, y int) int { return y - x },
		},
		{
			name: "a subtracted term scaled by more than one slot",
			offset: `%b = mul nsw i64 %y, 16
  %bytes = sub nsw i64 120, %b`,
			cell: func(_, y int) int { return 15 - 2*y },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, pair := range [][2]int{{0, 0}, {5, 2}, {7, 3}, {4, 4}, {2, 7}} {
				x, y := pair[0], pair[1]
				cell := tc.cell(x, y)
				if cell < 0 || cell >= maskedCells {
					continue
				}
				assembly := assemble(t, parseIR(t, signedOffsetIR(x, y, tc.offset)))
				got := runMemory(t, assembly)[signedResultSlot]
				if want := float64(maskedCell(cell)); got != want {
					t.Errorf("x=%d y=%d read %v, want element %d holding %v:\n%s",
						x, y, got, cell, want, assembly)
				}
			}
		})
	}
}

// TestMaskedPointerDifferenceKeepsTheOneDivisionItIs covers a difference whose
// terms each carry part of the byte stride. Dividing term by term would owe a
// division per term where the instruction it replaces is one division over the
// whole difference, so the shift is left to select as itself.
func TestMaskedPointerDifferenceKeepsTheOneDivisionItIs(t *testing.T) {
	differenceIR := func(x, y int) string {
		return fmt.Sprintf(`
@table = internal global [%d x i64] zeroinitializer

define void @main() {
entry:
  %%xslot = alloca i64
  %%yslot = alloca i64
  %%out = alloca i64
  store i64 %d, ptr %%xslot
  store i64 %d, ptr %%yslot
  %%x = load i64, ptr %%xslot
  %%y = load i64, ptr %%yslot
  %%sx = shl i64 %%x, 5
  %%mx = and i64 %%sx, 224
  %%sy = shl i64 %%y, 4
  %%my = and i64 %%sy, 112
  %%d = sub nsw i64 %%mx, %%my
  %%q = ashr exact i64 %%d, 3
  store i64 %%q, ptr %%out
  ret void
}
`, maskedCells, x, y)
	}

	for _, pair := range [][2]int{{0, 0}, {5, 2}, {2, 7}, {7, 7}} {
		x, y := pair[0], pair[1]
		assembly := assemble(t, parseIR(t, differenceIR(x, y)))
		if got, want := runMemory(t, assembly)[signedResultSlot], float64(4*(x&7)-2*(y&7)); got != want {
			t.Errorf("x=%d y=%d gave %v, want %v:\n%s", x, y, got, want, assembly)
		}
		dividing := 0
		for line := range strings.SplitSeq(assembly, "\n") {
			switch mnemonic, _, _ := strings.Cut(strings.TrimSpace(line), " "); mnemonic {
			case "div", "sra", "srl", "mod":
				dividing++
			}
		}
		if dividing != 1 {
			t.Errorf("x=%d y=%d spent %d lines dividing, want the one the difference is:\n%s",
				x, y, dividing, assembly)
		}
	}
}

// zeroScaledTermIR is a pointer difference one of whose terms the offset
// multiplies by nothing, so the walk records it at a stride of zero. The
// difference is the table's base less itself plus that term, zero for every x.
const zeroScaledTermIR = `
@table = internal global [16 x i64] zeroinitializer

define void @main() {
entry:
  %xslot = alloca i64
  %out = alloca i64
  store i64 5, ptr %xslot
  %x = load i64, ptr %xslot
  %base = ptrtoint ptr @table to i64
  %z = mul nsw i64 %x, 0
  %off = add nsw i64 %base, %z
  %d = sub nsw i64 %off, %base
  %q = sdiv exact i64 %d, 8
  store i64 %q, ptr %out
  ret void
}
`

// TestAddressTermWorthNothingCostsNoLine covers the term a stride of zero
// leaves. It contributes no slots whatever its value holds, and its scale is
// neither positive nor negative, which is the sign an addition would be chosen
// by.
func TestAddressTermWorthNothingCostsNoLine(t *testing.T) {
	got := mnemonics(render(selectProgram(t, zeroScaledTermIR).Program.Funcs[0]))
	if contains(got, "mul") {
		t.Errorf("the term worth nothing cost a multiply: %v", got)
	}
	assembly := assemble(t, parseIR(t, zeroScaledTermIR))
	if slots := runMemory(t, assembly); slots[1] != 0 {
		t.Errorf("the difference came out %v, want 0:\n%s", slots[1], assembly)
	}
}

// TestScaleByRefusesAProductWithNoMagnitude covers the one product an int holds
// without holding its negation. math.MinInt negates to itself, so a scale that
// reaches one is a subtracted term emitted as an addition of the wrong size, and
// the overflow check alone lets it through: it divides back to the factor.
func TestScaleByRefusesAProductWithNoMagnitude(t *testing.T) {
	tests := []struct {
		name          string
		scale, factor int
		want          int
		wantOK        bool
	}{
		{name: "an element stride", scale: 1, factor: 8, want: 8, wantOK: true},
		{name: "a subtracted element stride", scale: -1, factor: 8, want: -8, wantOK: true},
		{name: "the largest magnitude an int negates", scale: 1, factor: math.MinInt + 1, want: math.MinInt + 1, wantOK: true},
		{name: "a scale of nothing", scale: 0, factor: math.MinInt, want: 0, wantOK: true},
		{name: "a product past what an int holds", scale: 1 << 40, factor: 1 << 40},
		{name: "the full-width constant itself", scale: 1, factor: math.MinInt},
		{name: "the full-width constant through a negated stride", scale: -1, factor: math.MinInt},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := scaleBy(tt.scale, tt.factor)
			if ok != tt.wantOK {
				t.Fatalf("scaleBy(%d, %d) reported %v, want %v", tt.scale, tt.factor, ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("scaleBy(%d, %d) = %d, want %d", tt.scale, tt.factor, got, tt.want)
			}
		})
	}
}

// fullWidthScaleIR subscripts a table at a byte offset the program scales by a
// constant, so the scale reaches the decomposition as a multiply's factor.
func fullWidthScaleIR(scale string) string {
	return fmt.Sprintf(`
@table = internal global [%d x i64] zeroinitializer

define void @main() {
entry:
  %%islot = alloca i64
  %%out = alloca i64
  store i64 1, ptr %%islot
  %%i = load i64, ptr %%islot
  %%bytes = mul nsw i64 %%i, %s
  %%elem = getelementptr i8, ptr @table, i64 %%bytes
  %%v = load i64, ptr %%elem
  store i64 %%v, ptr %%out
  ret void
}
`, maskedCells, scale)
}

// TestSelectRefusesAnAddressScaleWithNoMagnitude is the same refusal reached
// through a subscript. There is no smaller answer to give: a term stepping 2^60
// slots names nothing in a 512 slot array, so the address is refused rather
// than emitted at some other size.
func TestSelectRefusesAnAddressScaleWithNoMagnitude(t *testing.T) {
	tests := []struct {
		name    string
		scale   string
		refused bool
	}{
		{name: "one element back per index", scale: "-8", refused: false},
		{name: "two elements forward per index", scale: "16", refused: false},
		{name: "the full width of an i64", scale: "-9223372036854775808", refused: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := parseIR(t, fullWidthScaleIR(tt.scale))
			_, err := Select(t.Context(), m, Options{File: "test.c"})
			switch {
			case tt.refused && err == nil:
				t.Fatalf("selection accepted a scale it cannot state the magnitude of:\n%s", m.String())
			case !tt.refused && err != nil:
				t.Fatalf("Select: %v\n%s", err, m.String())
			}
		})
	}
}
