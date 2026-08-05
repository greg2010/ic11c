package isel

import (
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
)

// TestSelectPointerDifferenceDropsTheStride covers the division the language's
// byte-stated difference carries. A pointer is a slot index at run time, so the
// subtraction has already produced the element count and a shift or divide means
// the stride was applied twice — wrong by a factor of eight, and it assembles.
func TestSelectPointerDifferenceDropsTheStride(t *testing.T) {
	cases := []struct {
		name string
		// body is the difference, over %p and %q, both pointers into @table.
		body string
		want []string
	}{
		{
			name: "two addresses computed at run time",
			body: `  %a = ptrtoint ptr %p to i64
  %b = ptrtoint ptr %r to i64
  %d = sub nsw i64 %a, %b
  %n = sdiv exact i64 %d, 8`,
			want: []string{"sub"},
		},
		{
			name: "the shift the optimizer canonicalises the division into",
			body: `  %a = ptrtoint ptr %p to i64
  %b = ptrtoint ptr %r to i64
  %d = sub nsw i64 %a, %b
  %n = ashr exact i64 %d, 3`,
			want: []string{"sub"},
		},
		{
			name: "one address against a slot fixed at compile time",
			body: `  %a = ptrtoint ptr %p to i64
  %b = ptrtoint ptr %q to i64
  %d = sub nsw i64 %a, %b
  %n = sdiv exact i64 %d, 8`,
			want: []string{"add"},
		},
		{
			name: "a byte offset the optimizer folded the addresses out of",
			body: `  %scaled = shl nsw i64 %i, 3
  %d = sub nsw i64 %scaled, 16
  %n = ashr exact i64 %d, 3`,
			want: []string{"add"},
		},
		{
			name: "a constant offset the difference subtracts from",
			body: `  %scaled = shl nsw i64 %i, 3
  %d = sub nsw i64 56, %scaled
  %n = ashr exact i64 %d, 3`,
			want: []string{"sub"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := render(selectProgram(t, differenceIR(tc.body)).Program.Funcs[0])
			got := mnemonics(lines)
			text := strings.Join(lines, "\n")
			for _, scaling := range []string{"div", "mul", "sll", "srl", "sra", "mod"} {
				if contains(got, scaling) {
					t.Errorf("the difference emitted %q, so the stride survived:\n%s", scaling, text)
				}
			}
			for _, want := range tc.want {
				if !contains(got, want) {
					t.Errorf("the difference has no %q:\n%s", want, text)
				}
			}
		})
	}
}

// TestSelectPointerDifferenceKeepsAValueItShares checks the one case the
// decomposition must not take apart: a byte offset something else also reads
// still has to be computed.
func TestSelectPointerDifferenceKeepsAValueItShares(t *testing.T) {
	lines := render(selectProgram(t, differenceIR(`  %scaled = shl nsw i64 %i, 3
  %d = sub nsw i64 %scaled, 16
  %n = ashr exact i64 %d, 3
  store i64 %scaled, ptr %jslot`)).Program.Funcs[0])

	if !contains(mnemonics(lines), "sll") {
		t.Errorf("the shift a subscript still reads was dropped:\n%s", strings.Join(lines, "\n"))
	}
}

// byteOffsetDistance is the distance in elements between the two pointers
// [byteOffsetIR] hands a case. Eight times it is the byte offset LLVM states
// that distance in.
const byteOffsetDistance = 5

// byteOffsetResultSlot is where [byteOffsetIR] leaves the answer: the array
// takes slots 0 through 7 and the index sits at slot 8.
const byteOffsetResultSlot = 9

// TestPointerByteOffsetSurvivesTheStrideFold executes what a byte offset the
// stride division did not wrap computes. -Oz folds that division together with
// the arithmetic beside it, and a register holds a pointer as a slot index, so
// a materialised byte offset has to be scaled to what the optimizer computed with.
func TestPointerByteOffsetSurvivesTheStrideFold(t *testing.T) {
	const bytes = byteOffsetDistance * ic10.SlotBytes

	cases := []struct {
		name string
		// body computes %n over %a and %b, the byte addresses of the far and
		// near ends of the distance.
		body string
		want float64
	}{
		{
			name: "the stride division on its own",
			body: `  %d = sub nsw i64 %a, %b
  %n = ashr exact i64 %d, 3`,
			want: byteOffsetDistance,
		},
		{
			name: "a doubling folded into the stride division",
			body: `  %d = sub nsw i64 %a, %b
  %n = ashr exact i64 %d, 2`,
			want: 2 * byteOffsetDistance,
		},
		{
			name: "a quadrupling folded into the stride division",
			body: `  %d = sub nsw i64 %a, %b
  %n = ashr exact i64 %d, 1`,
			want: 4 * byteOffsetDistance,
		},
		{
			name: "a scaling wide enough to leave no division",
			body: `  %d = sub nsw i64 %a, %b
  %n = shl nsw i64 %d, 1`,
			want: 2 * bytes,
		},
		{
			name: "a halving folded into the stride division",
			body: `  %d = sub nsw i64 %a, %b
  %n = ashr exact i64 %d, 4`,
			want: bytes / 16,
		},
		{
			name: "the difference taken the other way round",
			body: `  %d = sub nsw i64 %b, %a
  %n = ashr exact i64 %d, 2`,
			want: -2 * byteOffsetDistance,
		},
		{
			name: "one offset read by a division that absorbs it and one that does not",
			body: `  %d = sub nsw i64 %a, %b
  %near = ashr exact i64 %d, 3
  %far = ashr exact i64 %d, 2
  %n = add nsw i64 %near, %far`,
			want: 3 * byteOffsetDistance,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assembly := assemble(t, parseIR(t, byteOffsetIR(tc.body)))
			if got := runMemory(t, assembly)[byteOffsetResultSlot]; got != tc.want {
				t.Errorf("the offset computed %v, want %v:\n%s", got, tc.want, assembly)
			}
		})
	}
}

// byteOffsetIR wraps one expression over two byte addresses in a program that
// leaves its answer in [byteOffsetResultSlot]. The far pointer is reached at a
// loaded index, so neither the address nor the distance is a number the
// optimizer could have folded before this stage saw it.
func byteOffsetIR(body string) string {
	return `
define void @main() {
entry:
  %table = alloca [8 x i64]
  %index = alloca i64
  %out = alloca i64
  store i64 ` + strconv.Itoa(byteOffsetDistance) + `, ptr %index
  %i = load i64, ptr %index
  %p = getelementptr inbounds i64, ptr %table, i64 %i
  %a = ptrtoint ptr %p to i64
  %b = ptrtoint ptr %table to i64
` + body + `
  store i64 %n, ptr %out
  ret void
}
`
}

// constantSlotNearSlot and constantSlotFarSlot are the two ends [constantSlotIR]
// points into its array at, and constantSlotScalarSlot the scalar after it. A
// subscript of the near end at the far end's own offset lands on the far one,
// within the one array, which is what keeps the address inside an object.
const (
	constantSlotNearSlot   = 0
	constantSlotFarSlot    = 4
	constantSlotScalarSlot = 8
)

// TestConstantSlotDropsAnAddressItResolved covers a pointer cast a subscript at
// a fixed slot took apart. An address the walk decomposed and did not claim is a
// multiply nothing consumes; the address it computes is right either way, so
// nothing downstream would catch it.
func TestConstantSlotDropsAnAddressItResolved(t *testing.T) {
	cases := []struct {
		name string
		// body computes over %near and %far, two arrays at fixed slots, and
		// %out, one scalar past them.
		body string
		// scaling says whether the byte address is still a line, which it is
		// exactly when something other than the subscript reads it.
		scaling bool
		// want is the data region a case leaves behind, by slot.
		want map[int]float64
	}{
		{
			name: "a subscript whose only reader is the address it resolved",
			body: `  %bytes = ptrtoint ptr %far to i64
  %at = getelementptr i8, ptr %near, i64 %bytes
  store i64 7, ptr %at`,
			want: map[int]float64{constantSlotFarSlot: 7},
		},
		{
			name: "the same address something else still reads",
			body: `  %bytes = ptrtoint ptr %far to i64
  %at = getelementptr i8, ptr %near, i64 %bytes
  store i64 7, ptr %at
  store i64 %bytes, ptr %out`,
			scaling: true,
			want: map[int]float64{
				constantSlotFarSlot:    7,
				constantSlotScalarSlot: constantSlotFarSlot * ic10.SlotBytes,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assembly := assemble(t, parseIR(t, constantSlotIR(tc.body)))
			if got := contains(mnemonics(strings.Split(assembly, "\n")), "mul"); got != tc.scaling {
				t.Errorf("the byte address is a line: %v, want %v:\n%s", got, tc.scaling, assembly)
			}
			memory := runMemory(t, assembly)
			for slot, want := range tc.want {
				if got := memory[slot]; got != want {
					t.Errorf("slot %d holds %v, want %v:\n%s", slot, got, want, assembly)
				}
			}
		})
	}
}

// constantSlotIR wraps a body in a function laying out one array and a scalar
// after it, with a pointer to each end of the array at the slots the constants
// above name.
func constantSlotIR(body string) string {
	length := strconv.Itoa(constantSlotScalarSlot - constantSlotNearSlot)
	return `
define void @main() {
entry:
  %table = alloca [` + length + ` x i64]
  %out = alloca i64
  %near = getelementptr [` + length + ` x i64], ptr %table, i64 0, i64 ` + strconv.Itoa(constantSlotNearSlot) + `
  %far = getelementptr [` + length + ` x i64], ptr %table, i64 0, i64 ` + strconv.Itoa(constantSlotFarSlot) + `
` + body + `
  ret void
}
`
}

// differenceIR wraps one difference in a function supplying three pointers into
// one array, two at a runtime index and one at a fixed slot, and stores the
// answer where it cannot be deleted.
func differenceIR(body string) string {
	return `
@table = internal global [8 x i64] zeroinitializer

define void @main() {
entry:
  %islot = alloca i64
  %jslot = alloca i64
  %out = alloca i64
  %i = load i64, ptr %islot
  %j = load i64, ptr %jslot
  %p = getelementptr inbounds i64, ptr @table, i64 %i
  %q = getelementptr inbounds i64, ptr @table, i64 2
  %r = getelementptr inbounds i64, ptr @table, i64 %j
` + body + `
  store i64 %n, ptr %out
  ret void
}
`
}
