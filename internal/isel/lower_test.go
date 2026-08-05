package isel

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/source"
	"tinygo.org/x/go-llvm"
)

// conversionIR wraps one width conversion in a function that stores the answer,
// so the conversion has a reader. %x is the loaded int a body converts, or
// compares to reach a truth value.
func conversionIR(body, stored string) string {
	return `
define void @main() {
entry:
  %in = alloca i64
  %out = alloca double
  %x = load i64, ptr %in
  ` + body + `
  store ` + stored + `, ptr %out
  ret void
}
`
}

// TestConversionOfATruthValue covers the widths a conversion arrives at. The
// two one-bit conversions disagree about what the bit means: an i1 read as
// signed is 0 or -1, read as unsigned 0 or 1, so treating the signed one as free
// hands back 1 where the program asked for -1.
func TestConversionOfATruthValue(t *testing.T) {
	// The leading clr db is the data region zeroing and the get is the read the
	// comparison takes its operand from.
	tests := []struct {
		name   string
		body   string
		stored string
		want   []string
	}{
		{
			name:   "an int widened to a double costs nothing",
			body:   "%r = sitofp i64 %x to double",
			stored: "double %r",
			want:   []string{"clr", "get", "poke"},
		},
		{
			name:   "a truth value read as unsigned is already 0 or 1",
			body:   "%c = icmp sgt i64 %x, 5\n  %r = uitofp i1 %c to double",
			stored: "double %r",
			want:   []string{"clr", "get", "sgt", "poke"},
		},
		{
			name:   "a truth value read as signed is 0 or minus 1",
			body:   "%c = icmp sgt i64 %x, 5\n  %r = sitofp i1 %c to double",
			stored: "double %r",
			want:   []string{"clr", "get", "sgt", "sub", "poke"},
		},
		{
			name:   "a truth value zero extended is already 0 or 1",
			body:   "%c = icmp sgt i64 %x, 5\n  %r = zext i1 %c to i64",
			stored: "i64 %r",
			want:   []string{"clr", "get", "sgt", "poke"},
		},
		{
			name:   "a truth value sign extended is 0 or minus 1",
			body:   "%c = icmp sgt i64 %x, 5\n  %r = sext i1 %c to i64",
			stored: "i64 %r",
			want:   []string{"clr", "get", "sgt", "sub", "poke"},
		},
		{
			name:   "an int narrowed to a truth value is its low bit",
			body:   "%c = trunc i64 %x to i1\n  %r = zext i1 %c to i64",
			stored: "i64 %r",
			want:   []string{"clr", "get", "and", "poke"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := render(selectProgram(t, conversionIR(tt.body, tt.stored)).Program.Funcs[0])
			got := mnemonics(lines)
			if len(got) != len(tt.want) {
				t.Fatalf("selected %v, want one instruction per %v", lines, tt.want)
			}
			for i, mnemonic := range got {
				if mnemonic != tt.want[i] {
					t.Errorf("instruction %d is %q, want %q: %v", i, mnemonic, tt.want[i], lines)
				}
			}
		})
	}
}

// Slots the truth value conversion program leaves its answers in, in the order
// slot assignment places the allocas.
const (
	signedSlot   = 1
	shiftedSlot  = 2
	unsignedSlot = 3
	extendedSlot = 4
)

// truthValueIR converts one comparison's result four ways and writes each answer
// to its own slot. The shifted copy makes a wrong conversion visible twice over:
// the same value is one number where stored and another where added to, which is
// the shape the defect took on the chip.
func truthValueIR(input int) string {
	return fmt.Sprintf(`define void @main() {
entry:
  %%in = alloca i64
  %%signed = alloca double
  %%shifted = alloca double
  %%unsigned = alloca double
  %%extended = alloca i64
  store i64 %d, ptr %%in
  %%x = load i64, ptr %%in
  %%c = icmp sgt i64 %%x, 5
  %%s = sitofp i1 %%c to double
  store double %%s, ptr %%signed
  %%t = fadd double %%s, 3.0
  store double %%t, ptr %%shifted
  %%u = uitofp i1 %%c to double
  store double %%u, ptr %%unsigned
  %%e = sext i1 %%c to i64
  store i64 %%e, ptr %%extended
  ret void
}
`, input)
}

// TestTruthValueConversionComputesTheSignedValue executes what the case above
// only inspects. A conversion selected as an alias assembles, emits one fewer
// line than it should, and leaves 1 in a slot the program said holds -1.
func TestTruthValueConversionComputesTheSignedValue(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		signed   float64
		shifted  float64
		unsigned float64
		extended float64
	}{
		{name: "the comparison holds", input: 10, signed: -1, shifted: 2, unsigned: 1, extended: -1},
		{name: "the comparison fails", input: 0, signed: 0, shifted: 3, unsigned: 0, extended: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assembly := assemble(t, parseIR(t, truthValueIR(tt.input)))
			final := runMemory(t, assembly)
			for _, want := range []struct {
				what string
				slot int
				want float64
			}{
				{"the signed conversion", signedSlot, tt.signed},
				{"the signed conversion plus three", shiftedSlot, tt.shifted},
				{"the unsigned conversion", unsignedSlot, tt.unsigned},
				{"the sign extension", extendedSlot, tt.extended},
			} {
				if got := final[want.slot]; got != want.want {
					t.Errorf("%s gave %v, want %v:\n%s", want.what, got, want.want, assembly)
				}
			}
		})
	}
}

// TestPredicateArithmeticIsModuloTwo covers the arithmetic LLVM states over one
// bit. Its add and subtract both wrap, which is an exclusive or; the machine's
// add would answer 2 for true plus true, which no reader can use.
func TestPredicateArithmeticIsModuloTwo(t *testing.T) {
	tests := []struct {
		name string
		op   string
		want [4]float64
	}{
		{name: "add wraps", op: "add", want: [4]float64{0, 1, 1, 0}},
		{name: "sub wraps", op: "sub", want: [4]float64{0, 1, 1, 0}},
		{name: "mul agrees with the machine", op: "mul", want: [4]float64{0, 0, 0, 1}},
		{name: "and agrees with the machine", op: "and", want: [4]float64{0, 0, 0, 1}},
		{name: "or agrees with the machine", op: "or", want: [4]float64{0, 1, 1, 1}},
	}

	inputs := [4][2]int{{0, 0}, {0, 10}, {10, 0}, {10, 10}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i, input := range inputs {
				assembly := assemble(t, parseIR(t, predicateArithmeticIR(tt.op, input[0], input[1])))
				if got := runMemory(t, assembly)[predicateResultSlot]; got != tt.want[i] {
					t.Errorf("%s of %v gave %v, want %v:\n%s", tt.op, input, got, tt.want[i], assembly)
				}
			}
		})
	}
}

// predicateResultSlot is where predicateArithmeticIR leaves its answer, in the
// order slot assignment places the allocas.
const predicateResultSlot = 2

func predicateArithmeticIR(op string, first, second int) string {
	return fmt.Sprintf(`define void @main() {
entry:
  %%firstSlot = alloca i64
  %%secondSlot = alloca i64
  %%out = alloca i64
  store i64 %d, ptr %%firstSlot
  store i64 %d, ptr %%secondSlot
  %%x = load i64, ptr %%firstSlot
  %%y = load i64, ptr %%secondSlot
  %%a = icmp sgt i64 %%x, 5
  %%b = icmp sgt i64 %%y, 5
  %%r = %s i1 %%a, %%b
  %%w = zext i1 %%r to i64
  store i64 %%w, ptr %%out
  ret void
}
`, first, second, op)
}

// truncationIR narrows one loaded value to a truth value and widens it back, so
// that the narrowing has a reader and lands in a slot.
func truncationIR(input int) string {
	return fmt.Sprintf(`define void @main() {
entry:
  %%in = alloca i64
  %%out = alloca i64
  store i64 %d, ptr %%in
  %%x = load i64, ptr %%in
  %%c = trunc i64 %%x to i1
  %%w = zext i1 %%c to i64
  store i64 %%w, ptr %%out
  ret void
}
`, input)
}

// truncationSlot is where truncationIR leaves its answer.
const truncationSlot = 1

// TestTruncationToATruthValueIsTheLowBit covers the narrowing every bitwise test
// of a bool goes through. It is the machine's and against 1, which holds for a
// negative operand too, where taking the value's own truth would answer 1 for
// odd and even alike.
func TestTruncationToATruthValueIsTheLowBit(t *testing.T) {
	tests := []struct {
		name  string
		input int
		want  float64
	}{
		{name: "zero has no low bit", input: 0, want: 0},
		{name: "one is the low bit itself", input: 1, want: 1},
		{name: "an even value has no low bit", input: 2, want: 0},
		{name: "an odd value keeps only the low bit", input: 3, want: 1},
		{name: "a large even value has no low bit", input: 1024, want: 0},
		{name: "minus one is odd", input: -1, want: 1},
		{name: "minus two is even", input: -2, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assembly := assemble(t, parseIR(t, truncationIR(tt.input)))
			if got := runMemory(t, assembly)[truncationSlot]; got != tt.want {
				t.Errorf("narrowing %d gave %v, want %v:\n%s", tt.input, got, tt.want, assembly)
			}
		})
	}
}

// TestComparisonOfTruthValuesReadsThemAsTheMachineHoldsThem covers a comparison
// whose operands are one bit wide. LLVM reads an i1 as 0 or -1 and the machine
// holds a truth value as 0 or 1, so the two order the bits opposite ways and
// selecting slt for LLVM's signed one answers the complement for every pair.
func TestComparisonOfTruthValuesReadsThemAsTheMachineHoldsThem(t *testing.T) {
	tests := []struct {
		name string
		pred string
		want [4]float64
	}{
		{name: "signed less than orders minus one below zero", pred: "slt", want: [4]float64{0, 0, 1, 0}},
		{name: "signed greater than orders zero above minus one", pred: "sgt", want: [4]float64{0, 1, 0, 0}},
		{name: "signed at most", pred: "sle", want: [4]float64{1, 0, 1, 1}},
		{name: "signed at least", pred: "sge", want: [4]float64{1, 1, 0, 1}},
		{name: "unsigned less than orders zero below one", pred: "ult", want: [4]float64{0, 1, 0, 0}},
		{name: "unsigned greater than", pred: "ugt", want: [4]float64{0, 0, 1, 0}},
		{name: "unsigned at most", pred: "ule", want: [4]float64{1, 1, 0, 1}},
		{name: "unsigned at least", pred: "uge", want: [4]float64{1, 0, 1, 1}},
		{name: "equality does not depend on the reading", pred: "eq", want: [4]float64{1, 0, 0, 1}},
		{name: "inequality does not depend on the reading", pred: "ne", want: [4]float64{0, 1, 1, 0}},
	}

	inputs := [4][2]int{{0, 0}, {0, 10}, {10, 0}, {10, 10}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i, input := range inputs {
				ir := predicateArithmeticIR("icmp "+tt.pred, input[0], input[1])
				assembly := assemble(t, parseIR(t, ir))
				if got := runMemory(t, assembly)[predicateResultSlot]; got != tt.want[i] {
					t.Errorf("%s of %v gave %v, want %v:\n%s", tt.pred, input, got, tt.want[i], assembly)
				}
			}
		})
	}
}

// unsignedDivisionIR divides and takes the remainder of one loaded value,
// through the unsigned operations InstCombine rewrites the signed ones into once
// it has proved both operands non-negative.
func unsignedDivisionIR(input int) string {
	return fmt.Sprintf(`define void @main() {
entry:
  %%in = alloca i64
  %%quotient = alloca i64
  %%remainder = alloca i64
  store i64 %d, ptr %%in
  %%x = load i64, ptr %%in
  %%q = udiv i64 %%x, 7
  store i64 %%q, ptr %%quotient
  %%r = urem i64 %%x, 7
  store i64 %%r, ptr %%remainder
  ret void
}
`, input)
}

// Slots unsignedDivisionIR leaves its answers in.
const (
	quotientSlot  = 1
	remainderSlot = 2
)

// TestUnsignedDivisionSelectsTheSignedSynthesis covers the operations a
// dominating sign check produces. Over operands LLVM has proved non-negative an
// unsigned division and a signed one are the same division, so both select the
// same synthesis rather than failing to compile.
func TestUnsignedDivisionSelectsTheSignedSynthesis(t *testing.T) {
	lines := render(selectProgram(t, unsignedDivisionIR(23)).Program.Funcs[0])
	got := mnemonics(lines)
	for _, want := range [][]string{
		{"div", "trunc"},
		{"div", "trunc", "mul", "sub"},
	} {
		if !containsSequence(got, want) {
			t.Errorf("selected %v, want the sequence %v", lines, want)
		}
	}
	if contains(got, "mod") {
		t.Errorf("selected the machine's mod, which is not C's remainder for any divisor: %v", lines)
	}
}

// TestUnsignedDivisionComputesTheQuotientAndRemainder executes the selection,
// since the synthesized sequence is what a wrong mapping would still render
// plausibly.
func TestUnsignedDivisionComputesTheQuotientAndRemainder(t *testing.T) {
	tests := []struct {
		input     int
		quotient  float64
		remainder float64
	}{
		{input: 0, quotient: 0, remainder: 0},
		{input: 6, quotient: 0, remainder: 6},
		{input: 23, quotient: 3, remainder: 2},
		{input: 49, quotient: 7, remainder: 0},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.input), func(t *testing.T) {
			assembly := assemble(t, parseIR(t, unsignedDivisionIR(tt.input)))
			final := runMemory(t, assembly)
			if got := final[quotientSlot]; got != tt.quotient {
				t.Errorf("%d / 7 gave %v, want %v:\n%s", tt.input, got, tt.quotient, assembly)
			}
			if got := final[remainderSlot]; got != tt.remainder {
				t.Errorf("%d %% 7 gave %v, want %v:\n%s", tt.input, got, tt.remainder, assembly)
			}
		})
	}
}

// TestSelectRefusesAnOperationWithNoMachineForm holds the lowering switch's last
// resort to the standard every refusal has: say what the machine lacks and what
// to write instead, not only that something failed.
func TestSelectRefusesAnOperationWithNoMachineForm(t *testing.T) {
	const text = `
define void @main() {
entry:
  %slot = alloca double
  %a = load double, ptr %slot
  %b = load double, ptr %slot
  %r = frem double %a, %b
  store double %r, ptr %slot
  ret void
}
`
	m := parseIR(t, text)
	_, err := Select(t.Context(), m, Options{File: "test.c"})
	if err == nil {
		t.Fatal("selection accepted an operation with no machine instruction")
	}
	for _, want := range []string{"no machine instruction", "write"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// TestSelectArithmetic covers the operations one machine instruction holds
// exactly, so a wrong mapping shows up as a wrong mnemonic rather than as a
// wrong answer at run time.
func TestSelectArithmetic(t *testing.T) {
	cases := []struct {
		name  string
		build func(bd *builder) llvm.Value
		want  string
	}{
		{"add", func(bd *builder) llvm.Value { return bd.b.CreateNSWAdd(bd.opaque("x"), bd.konst(4), "") }, "add"},
		{"sub", func(bd *builder) llvm.Value { return bd.b.CreateNSWSub(bd.opaque("x"), bd.konst(4), "") }, "sub"},
		{"mul", func(bd *builder) llvm.Value { return bd.b.CreateNSWMul(bd.opaque("x"), bd.konst(4), "") }, "mul"},
		{"and", func(bd *builder) llvm.Value { return bd.b.CreateAnd(bd.opaque("x"), bd.konst(4), "") }, "and"},
		{"or", func(bd *builder) llvm.Value { return bd.b.CreateOr(bd.opaque("x"), bd.konst(4), "") }, "or"},
		{"xor", func(bd *builder) llvm.Value { return bd.b.CreateXor(bd.opaque("x"), bd.konst(4), "") }, "xor"},
		{"shl becomes sll", func(bd *builder) llvm.Value { return bd.b.CreateShl(bd.opaque("x"), bd.konst(4), "") }, "sll"},
		{"ashr becomes sra", func(bd *builder) llvm.Value { return bd.b.CreateAShr(bd.opaque("x"), bd.konst(4), "") }, "sra"},
		{"lshr becomes srl", func(bd *builder) llvm.Value { return bd.b.CreateLShr(bd.opaque("x"), bd.konst(4), "") }, "srl"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bd := newBuilder(t)
			bd.keep(tc.build(bd))
			bd.b.CreateRetVoid()

			got := mnemonics(selectFunc(t, bd))
			if !contains(got, tc.want) {
				t.Errorf("selected %v, want one %s", got, tc.want)
			}
		})
	}
}

// TestSelectComplementByWidth covers both widths an exclusive or against all
// ones arrives at. The machine's not complements the whole double, which is what
// xor against all ones means at the machine's own width; at one bit the truth
// value is 0 or 1 in a register whose other bits are not part of it.
func TestSelectComplementByWidth(t *testing.T) {
	cases := []struct {
		name    string
		build   func(bd *builder) llvm.Value
		want    string
		unwant  string
		operand string
	}{
		{
			name:   "an int complement is the machine's not",
			build:  func(bd *builder) llvm.Value { return bd.b.CreateNot(bd.opaque("x"), "") },
			want:   "not",
			unwant: "xor",
		},
		{
			name: "a bool complement is xor against one",
			build: func(bd *builder) llvm.Value {
				cmp := bd.b.CreateICmp(llvm.IntSGT, bd.opaque("x"), bd.konst(5), "cmp")
				return bd.b.CreateZExt(bd.b.CreateNot(cmp, ""), bd.i64, "w")
			},
			want:    "xor",
			unwant:  "not",
			operand: "1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bd := newBuilder(t)
			bd.keep(tc.build(bd))
			bd.b.CreateRetVoid()

			lines := selectFunc(t, bd)
			got := mnemonics(lines)
			if !contains(got, tc.want) {
				t.Errorf("selected %v, want one %s", got, tc.want)
			}
			if contains(got, tc.unwant) {
				t.Errorf("selected %v, want no %s", got, tc.unwant)
			}
			if tc.operand == "" {
				return
			}
			for _, line := range lines {
				fields := strings.Fields(line)
				if fields[0] != tc.want {
					continue
				}
				if last := fields[len(fields)-1]; last != tc.operand {
					t.Errorf("selected %q, want its last operand to be %s", line, tc.operand)
				}
			}
		})
	}
}

// TestComplementOfABoolComputesZeroOrOne executes what the case above only
// inspects. A wrong complement renders a plausible mnemonic and leaves -1 where
// the program said 1; a branch takes any non-zero and so does not notice, which
// is why the defect hides.
func TestComplementOfABoolComputesZeroOrOne(t *testing.T) {
	cases := []struct {
		name       string
		first      int
		second     int
		same       float64
		complement float64
	}{
		{name: "neither holds", first: 0, second: 0, same: 1, complement: 1},
		{name: "the first holds", first: 10, second: 0, same: 0, complement: 0},
		{name: "the second holds", first: 0, second: 10, same: 0, complement: 1},
		{name: "both hold", first: 10, second: 10, same: 1, complement: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assembly := assemble(t, parseIR(t, complementIR(tc.first, tc.second)))
			final := runMemory(t, assembly)
			if got := final[sameSlot]; got != tc.same {
				t.Errorf("the two bools compared equal as %v, want %v:\n%s", got, tc.same, assembly)
			}
			if got := final[complementSlot]; got != tc.complement {
				t.Errorf("the complement of the first bool is %v, want %v:\n%s", got, tc.complement, assembly)
			}
		})
	}
}

// Slots the complement program leaves its answers in, in the order slot
// assignment places the allocas.
const (
	sameSlot       = 2
	complementSlot = 3
)

// complementIR compares two loaded values against constants and writes both the
// equality of the two bools and the complement of the first back to memory. The
// values go through memory so that they stay runtime values.
func complementIR(first, second int) string {
	return fmt.Sprintf(`define void @main() {
entry:
  %%firstSlot = alloca i64
  %%secondSlot = alloca i64
  %%same = alloca i64
  %%complement = alloca i64
  store i64 %d, ptr %%firstSlot
  store i64 %d, ptr %%secondSlot
  %%x = load i64, ptr %%firstSlot
  %%y = load i64, ptr %%secondSlot
  %%a = icmp sgt i64 %%x, 5
  %%b = icmp sgt i64 %%y, 7
  %%differ = xor i1 %%a, %%b
  %%agree = xor i1 %%differ, true
  %%agreeWide = zext i1 %%agree to i64
  store i64 %%agreeWide, ptr %%same
  %%notA = xor i1 %%a, true
  %%notAWide = zext i1 %%notA to i64
  store i64 %%notAWide, ptr %%complement
  ret void
}
`, first, second)
}

// TestSelectDivision pins the two operations that have to be synthesized. The
// machine's div does not truncate, and its mod adds the divisor back to a
// negative remainder, which is C's answer for no divisor and cannot be fixed up.
func TestSelectDivision(t *testing.T) {
	cases := []struct {
		name  string
		build func(bd *builder) llvm.Value
		want  []string
	}{
		{
			name:  "sdiv truncates toward zero",
			build: func(bd *builder) llvm.Value { return bd.b.CreateSDiv(bd.opaque("x"), bd.konst(2), "") },
			want:  []string{"div", "trunc"},
		},
		{
			name:  "srem is a - trunc(a/b)*b, never the machine's mod",
			build: func(bd *builder) llvm.Value { return bd.b.CreateSRem(bd.opaque("x"), bd.konst(2), "") },
			want:  []string{"div", "trunc", "mul", "sub"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bd := newBuilder(t)
			bd.keep(tc.build(bd))
			bd.b.CreateRetVoid()

			got := mnemonics(selectFunc(t, bd))
			if !containsSequence(got, tc.want) {
				t.Errorf("selected %v, want the sequence %v", got, tc.want)
			}
			if contains(got, "mod") {
				t.Errorf("selected the machine's mod, which is not C's remainder for any divisor: %v", got)
			}
		})
	}
}

// TestSelectComparisons covers a comparison read as a value, which has to be
// materialised as 0 or 1.
func TestSelectComparisons(t *testing.T) {
	cases := []struct {
		name string
		pred llvm.IntPredicate
		rhs  int64
		want string
	}{
		{"eq", llvm.IntEQ, 4, "seq"},
		{"ne", llvm.IntNE, 4, "sne"},
		{"slt", llvm.IntSLT, 4, "slt"},
		{"sle", llvm.IntSLE, 4, "sle"},
		{"sgt", llvm.IntSGT, 4, "sgt"},
		{"sge", llvm.IntSGE, 4, "sge"},
		{"eq against zero drops an operand", llvm.IntEQ, 0, "seqz"},
		{"ne against zero drops an operand", llvm.IntNE, 0, "snez"},
		{"slt against zero drops an operand", llvm.IntSLT, 0, "sltz"},
		{"sgt against zero drops an operand", llvm.IntSGT, 0, "sgtz"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bd := newBuilder(t)
			slot := bd.b.CreateAlloca(bd.i64, "x")
			x := bd.b.CreateLoad(bd.i64, slot, "x")
			cmp := bd.b.CreateICmp(tc.pred, x, bd.konst(tc.rhs), "cmp")
			bd.keep(bd.b.CreateZExt(cmp, bd.i64, "wide"))
			bd.b.CreateRetVoid()

			got := mnemonics(selectFunc(t, bd))
			if !contains(got, tc.want) {
				t.Errorf("selected %v, want one %s", got, tc.want)
			}
			// The widening is free: the machine's set instructions already
			// produce 0 or 1, so no copy should stand for it.
			if contains(got, "move") {
				t.Errorf("selected a copy for a zero extension of an i1: %v", got)
			}
		})
	}
}

// TestFusedCompareAndBranch is the byte-budget case: a comparison whose only
// reader is its own block's branch costs one instruction, not a set and a test.
func TestFusedCompareAndBranch(t *testing.T) {
	cases := []struct {
		name   string
		pred   llvm.IntPredicate
		rhs    int64
		want   string
		absent []string
	}{
		{"eq", llvm.IntEQ, 4, "beq", []string{"seq", "bnez"}},
		{"ne", llvm.IntNE, 4, "bne", []string{"sne", "bnez"}},
		{"slt", llvm.IntSLT, 4, "blt", []string{"slt", "bnez"}},
		{"sle", llvm.IntSLE, 4, "ble", []string{"sle", "bnez"}},
		{"sgt", llvm.IntSGT, 4, "bgt", []string{"sgt", "bnez"}},
		{"sge", llvm.IntSGE, 4, "bge", []string{"sge", "bnez"}},
		{"eq against zero", llvm.IntEQ, 0, "beqz", []string{"seqz"}},
		{"slt against zero", llvm.IntSLT, 0, "bltz", []string{"sltz"}},
		{"sgt against zero", llvm.IntSGT, 0, "bgtz", []string{"sgtz"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bd := newBuilder(t)
			slot := bd.b.CreateAlloca(bd.i64, "x")
			then, els := bd.block("then"), bd.block("else")
			x := bd.b.CreateLoad(bd.i64, slot, "x")
			bd.b.CreateCondBr(bd.b.CreateICmp(tc.pred, x, bd.konst(tc.rhs), "cmp"), then, els)
			bd.b.SetInsertPointAtEnd(then)
			bd.b.CreateRetVoid()
			bd.b.SetInsertPointAtEnd(els)
			bd.b.CreateRetVoid()

			got := mnemonics(selectFunc(t, bd))
			if !contains(got, tc.want) {
				t.Errorf("selected %v, want one %s", got, tc.want)
			}
			for _, absent := range tc.absent {
				if contains(got, absent) {
					t.Errorf("selected %s, which is the two-instruction form the fused branch replaces: %v", absent, got)
				}
			}
		})
	}
}

// TestCompareWithSecondReaderIsMaterialised is the other side of fusion: a
// comparison something else reads has to land in a register, and the branch
// then tests it.
func TestCompareWithSecondReaderIsMaterialised(t *testing.T) {
	bd := newBuilder(t)
	slot := bd.b.CreateAlloca(bd.i64, "x")
	then, els := bd.block("then"), bd.block("else")
	x := bd.b.CreateLoad(bd.i64, slot, "x")
	cmp := bd.b.CreateICmp(llvm.IntSLT, x, bd.konst(4), "cmp")
	bd.keep(bd.b.CreateZExt(cmp, bd.i64, "wide"))
	bd.b.CreateCondBr(cmp, then, els)
	bd.b.SetInsertPointAtEnd(then)
	bd.b.CreateRetVoid()
	bd.b.SetInsertPointAtEnd(els)
	bd.b.CreateRetVoid()

	got := mnemonics(selectFunc(t, bd))
	if !contains(got, "slt") {
		t.Errorf("selected %v, want the comparison materialised as slt", got)
	}
	if !contains(got, "bnez") {
		t.Errorf("selected %v, want the branch to test the materialised value with bnez", got)
	}
	if contains(got, "blt") {
		t.Errorf("fused a comparison that has a second reader: %v", got)
	}
}

// TestSelectIsPreferredOverABranch checks the machine's select is taken where
// the IR offers one. A branch would cost the branch, a jump past the other arm,
// and a target the layout has to keep.
func TestSelectIsPreferredOverABranch(t *testing.T) {
	bd := newBuilder(t)
	slot := bd.b.CreateAlloca(bd.i64, "x")
	x := bd.b.CreateLoad(bd.i64, slot, "x")
	cond := bd.b.CreateICmp(llvm.IntSGT, x, bd.konst(0), "cmp")
	bd.keep(bd.b.CreateSelect(cond, bd.konst(1), bd.konst(2), "sel"))
	bd.b.CreateRetVoid()

	got := mnemonics(selectFunc(t, bd))
	if !contains(got, "select") {
		t.Errorf("selected %v, want the machine's select", got)
	}
	for _, branch := range []string{"j", "beq", "bne", "bgt", "bnez"} {
		if contains(got, branch) {
			t.Errorf("selected %s for a select, which needs no control flow: %v", branch, got)
		}
	}
}

// TestSelectRefusesConstantsADoubleDoesNotHold closes the residual gap between
// the two value models. The optimizer folds to the whole of an i64 and every
// register is a double exact to 2^53, so a constant in the band between them
// names one number in the IR and another on the chip.
func TestSelectRefusesConstantsADoubleDoesNotHold(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "within the exact range", value: "9007199254740992"},
		{name: "one past the exact range", value: "9007199254740993", wantErr: true},
		{name: "one below the exact range", value: "-9007199254740993", wantErr: true},
		// A power of two well past 2^53 survives the round trip, so the bound is
		// not a magnitude and a shift that produces one is ordinary arithmetic.
		{name: "a large power of two", value: "1152921504606846976"},
		{name: "the largest int64", value: "9223372036854775807", wantErr: true},
		// The smallest int64 is a power of two, so a double holds it exactly and
		// the bound is a round trip rather than a magnitude.
		{name: "the smallest int64", value: "-9223372036854775808"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := parseIR(t, `
define void @main() {
entry:
  %slot = alloca i64
  store i64 `+tc.value+`, ptr %slot
  ret void
}
`)
			_, err := Select(t.Context(), m, Options{File: "test.c"})
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("selection refused %s, which a double holds exactly: %v", tc.value, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("selection accepted %s, which a double does not hold exactly", tc.value)
			}
			for _, want := range []string{tc.value, "2^53"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not mention %q: %v", want, err)
				}
			}
		})
	}
}

// TestSelectRefusesUnrepresentableWidths covers the width guard on every path
// that selects an instruction. Several of these have a machine instruction of
// the right name — a mul is a mul whatever width LLVM gave it — which is why the
// guard runs before selection rather than inside one pattern.
func TestSelectRefusesUnrepresentableWidths(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "a wide multiply",
			body: `%r = mul i65 123456789, 987654321
  %t = trunc i65 %r to i64
  store i64 %t, ptr %slot`,
			want: "this expression computes on a 65 bit integer",
		},
		{
			name: "a wide shift",
			body: `%r = lshr i65 123456789, 1
  %t = trunc i65 %r to i64
  store i64 %t, ptr %slot`,
			want: "this shift computes on a 65 bit integer",
		},
		{
			name: "a narrow comparison materialised as a value",
			body: `%c = icmp sgt i32 7, 4
  %w = zext i1 %c to i64
  store i64 %w, ptr %slot`,
			want: "this comparison computes on a 32 bit integer",
		},
		{
			name: "a narrow select",
			body: `%c = icmp sgt i64 %x, 0
  %r = select i1 %c, i32 1, i32 2
  %w = zext i32 %r to i64
  store i64 %w, ptr %slot`,
			want: "this conditional expression computes on a 32 bit integer",
		},
		{
			name: "a narrow store",
			body: `store i32 7, ptr %slot`,
			want: "this assignment computes on a 32 bit integer",
		},
		{
			name: "a narrow load",
			body: `%r = load i32, ptr %slot
  %w = zext i32 %r to i64
  store i64 %w, ptr %slot`,
			want: "this read of a variable computes on a 32 bit integer",
		},
		{
			name: "a narrow call argument",
			body: `%r = call i32 @llvm.smin.i32(i32 7, i32 4)
  %w = zext i32 %r to i64
  store i64 %w, ptr %slot`,
			want: "the call to 'llvm.smin.i32' computes on a 32 bit integer",
		},
		{
			name: "a narrow subscript index",
			body: `%p = getelementptr inbounds i64, ptr @table, i32 2
  %r = load i64, ptr %p
  store i64 %r, ptr %slot`,
			want: "this subscript computes on a 32 bit integer",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := `
@table = internal global [4 x i64] zeroinitializer
declare i32 @llvm.smin.i32(i32, i32)

define void @main() {
entry:
  %slot = alloca i64
  %x = load i64, ptr %slot
  ` + tc.body + `
  ret void
}
`
			m := parseIR(t, text)
			_, err := Select(t.Context(), m, Options{File: "test.c"})
			if err == nil {
				t.Fatalf("selection accepted an integer the machine cannot hold:\n%s", m.String())
			}
			for _, want := range []string{tc.want, "no representation"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not mention %q: %v", want, err)
				}
			}
			assertNamesSourceRatherThanIR(t, err)
		})
	}
}

// TestSelectRefusesOneWideChainOnce keeps the guard from burying the source line
// under a diagnostic per instruction. The optimizer's closed form is four
// instructions all carrying the line of the one expression they came from.
func TestSelectRefusesOneWideChainOnce(t *testing.T) {
	m := parseIR(t, `
define void @main() {
entry:
  %slot = alloca i64
  %x = load i64, ptr %slot
  %w = zext i64 %x to i65
  %p = mul i65 %w, %w
  %h = lshr i65 %p, 1
  %t = trunc i65 %h to i64
  store i64 %t, ptr %slot
  ret void
}
`)
	_, err := Select(t.Context(), m, Options{File: "test.c"})
	if err == nil {
		t.Fatalf("selection accepted an i65 chain:\n%s", m.String())
	}
	var diags source.DiagnosticList
	if !errors.As(err, &diags) {
		t.Fatalf("error is %T, want a source.DiagnosticList: %v", err, err)
	}
	if len(diags) != 1 {
		t.Errorf("one wide chain produced %d diagnostics, want 1:\n%s", len(diags), diags.String())
	}
}

// TestSelectAcceptsPredicateWidth is the other side of the guard: an i1 is not a
// value the machine has to hold, so refusing it would refuse every comparison.
func TestSelectAcceptsPredicateWidth(t *testing.T) {
	m := parseIR(t, valueIR(0, "sgt i64 %x, 4"))
	result, err := Select(t.Context(), m, Options{File: "test.c"})
	if err != nil {
		t.Fatalf("Select: %v\n%s", err, m.String())
	}
	if got := mnemonics(render(result.Program.Funcs[0])); !contains(got, "sgt") {
		t.Errorf("selected %v, want one sgt", got)
	}
}

// TestSelectSameSignComparison checks the canonicalisation InstCombine applies
// to a signed comparison whose operands it proved share a sign. Reading it as
// unsigned would reject every optimized program with a comparison under a range
// check.
func TestSelectSameSignComparison(t *testing.T) {
	cases := []struct {
		name string
		ir   string
		want string
	}{
		{"ugt samesign is sgt", "%c = icmp samesign ugt i64 %x, 7", "sgt"},
		{"uge samesign is sge", "%c = icmp samesign uge i64 %x, 7", "sge"},
		{"ult samesign is slt", "%c = icmp samesign ult i64 %x, 7", "slt"},
		{"ule samesign is sle", "%c = icmp samesign ule i64 %x, 7", "sle"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := `
define void @main() {
entry:
  %slot = alloca i64
  %x = load i64, ptr %slot
  ` + tc.ir + `
  %w = zext i1 %c to i64
  store i64 %w, ptr %slot
  ret void
}
`
			m := parseIR(t, text)
			result, err := Select(t.Context(), m, Options{File: "test.c"})
			if err != nil {
				t.Fatalf("Select: %v\n%s", err, m.String())
			}
			got := mnemonics(render(result.Program.Funcs[0]))
			if !contains(got, tc.want) {
				t.Errorf("selected %v, want one %s", got, tc.want)
			}
		})
	}
}

// valueIR puts a comparison where its result has to be materialised as 0 or 1.
// The program writes its own input into slot zero and the answer back over it,
// so one run reports both: a seed from outside would not survive the zeroing
// prologue, and storing and reloading is what keeps the input opaque.
func valueIR(input int64, comparison string) string {
	return `
define void @main() {
entry:
  %slot = alloca i64
  store i64 ` + strconv.FormatInt(input, 10) + `, ptr %slot
  %x = load i64, ptr %slot
  %c = icmp ` + comparison + `
  %w = zext i1 %c to i64
  store i64 %w, ptr %slot
  ret void
}
`
}

// branchIR puts the same comparison in the position selection fuses: its only
// reader is the branch ending its own block.
func branchIR(input int64, comparison string) string {
	return `
define void @main() {
entry:
  %slot = alloca i64
  store i64 ` + strconv.FormatInt(input, 10) + `, ptr %slot
  %x = load i64, ptr %slot
  %c = icmp ` + comparison + `
  br i1 %c, label %yes, label %no
yes:
  store i64 1, ptr %slot
  ret void
no:
  store i64 0, ptr %slot
  ret void
}
`
}

// unsignedBound is the constant every unsigned comparison case is written
// against: small enough that the interesting inputs sit either side of it, and
// positive, which is what makes the rewrite apply.
const unsignedBound = 8

// TestSelectUnsignedComparisonAgainstAConstant pins the sequence each unsigned
// predicate becomes. InstCombine folds a two-sided signed range test into one of
// these, so refusing them rejects an idiom that compiles unoptimized.
func TestSelectUnsignedComparisonAgainstAConstant(t *testing.T) {
	cases := []struct {
		pred string
		// value is the mnemonic sequence the materialised form produces, and
		// branch the one the fused form produces.
		value  []string
		branch []string
	}{
		{"ult", []string{"sgez", "slt", "and"}, []string{"bltz", "blt"}},
		{"ule", []string{"sgez", "sle", "and"}, []string{"bltz", "ble"}},
		{"ugt", []string{"sltz", "sgt", "or"}, []string{"bltz", "bgt"}},
		{"uge", []string{"sltz", "sge", "or"}, []string{"bltz", "bge"}},
	}

	for _, tc := range cases {
		t.Run(tc.pred, func(t *testing.T) {
			comparison := fmt.Sprintf("%s i64 %%x, %d", tc.pred, unsignedBound)
			positions := []struct {
				name string
				ir   string
				want []string
			}{
				{"materialised", valueIR(0, comparison), tc.value},
				{"fused into the branch", branchIR(0, comparison), tc.branch},
			}

			for _, position := range positions {
				t.Run(position.name, func(t *testing.T) {
					m := parseIR(t, position.ir)
					result, err := Select(t.Context(), m, Options{File: "test.c"})
					if err != nil {
						t.Fatalf("Select: %v\n%s", err, m.String())
					}
					got := mnemonics(render(result.Program.Funcs[0]))
					if !containsSequence(got, position.want) {
						t.Errorf("selected %v, want the sequence %v", got, position.want)
					}
				})
			}
		})
	}
}

// TestUnsignedComparisonComputesTheUnsignedAnswer runs each rewritten sequence
// on the chip interpreter, since a wrong rewrite still assembles and still
// passes a mnemonic check.
func TestUnsignedComparisonComputesTheUnsignedAnswer(t *testing.T) {
	inputs := []int64{-(1 << 53), -1, 0, unsignedBound - 1, unsignedBound, unsignedBound + 1}

	for _, pred := range []string{"ult", "ule", "ugt", "uge"} {
		t.Run(pred, func(t *testing.T) {
			comparison := fmt.Sprintf("%s i64 %%x, %d", pred, unsignedBound)
			positions := []struct {
				name string
				ir   func(int64) string
			}{
				{"materialised", func(input int64) string { return valueIR(input, comparison) }},
				{"fused into the branch", func(input int64) string { return branchIR(input, comparison) }},
			}

			for _, position := range positions {
				t.Run(position.name, func(t *testing.T) {
					for _, input := range inputs {
						assembly := assemble(t, parseIR(t, position.ir(input)))
						want := unsignedAnswer(t, pred, input, unsignedBound)
						if got := runOnChip(t, assembly); got != want {
							t.Errorf("%d %s %d gave %v, want %v:\n%s",
								input, pred, unsignedBound, got, want, assembly)
						}
					}
				})
			}
		})
	}
}

// unsignedAnswer is what the LLVM predicate means: both operands read as 64 bit
// unsigned integers. The expectation comes from that rather than from the
// rewrite under test, which is the only way this can contradict it.
func unsignedAnswer(t *testing.T, pred string, value, bound int64) float64 {
	t.Helper()
	x, y := uint64(value), uint64(bound)
	var held bool
	switch pred {
	case "ult":
		held = x < y
	case "ule":
		held = x <= y
	case "ugt":
		held = x > y
	case "uge":
		held = x >= y
	default:
		t.Fatalf("there is no reference answer for the predicate %q", pred)
	}
	if held {
		return 1
	}
	return 0
}

// TestSelectRejectsUnsignedComparisonOfTwoValues checks the case the rewrite
// does not cover. With no constant bound it needs a sign agreement test first,
// and what it should answer past 2^53 is undecided, so it is refused.
func TestSelectRejectsUnsignedComparisonOfTwoValues(t *testing.T) {
	cases := []struct {
		name  string
		build func(bd *builder, cmp llvm.Value)
	}{
		{
			name: "materialised",
			build: func(bd *builder, cmp llvm.Value) {
				bd.keep(bd.b.CreateZExt(cmp, bd.i64, "w"))
				bd.b.CreateRetVoid()
			},
		},
		{
			name: "fused into the branch",
			build: func(bd *builder, cmp llvm.Value) {
				yes, no := bd.block("yes"), bd.block("no")
				bd.b.CreateCondBr(cmp, yes, no)
				bd.b.SetInsertPointAtEnd(yes)
				bd.b.CreateRetVoid()
				bd.b.SetInsertPointAtEnd(no)
				bd.b.CreateRetVoid()
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bd := newBuilder(t)
			x, y := bd.opaque("x"), bd.opaque("y")
			tc.build(bd, bd.b.CreateICmp(llvm.IntULT, x, y, "cmp"))

			_, err := Select(t.Context(), bd.m, Options{File: "test.c"})
			if err == nil {
				t.Fatalf("selection accepted an unsigned comparison of two values:\n%s", bd.m.String())
			}
			for _, want := range []string{"unsigned test the machine cannot answer", "non-negative constant"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not mention %q: %v", want, err)
				}
			}
		})
	}
}

// TestSwitchArmsSharingOneBody covers a switch whose case labels branch to one
// block. The terminator names it once per label, and everything downstream is
// keyed on the block, so a successor recorded twice builds it twice and wires
// the edge twice — none of which the emitter or the chip would object to.
func TestSwitchArmsSharingOneBody(t *testing.T) {
	m := parseIR(t, `
define void @main() {
entry:
  %slot = alloca i64
  %t = load i64, ptr %slot
  switch i64 %t, label %other [
    i64 1, label %shared
    i64 2, label %shared
  ]

shared:
  br label %end

other:
  br label %end

end:
  %y = phi i64 [ 5, %shared ], [ 9, %other ]
  store i64 %y, ptr %slot
  ret void
}
`)
	result, err := Select(t.Context(), m, Options{File: "test.c"})
	if err != nil {
		t.Fatalf("Select: %v\n%s", err, m.String())
	}
	fn := result.Program.Funcs[0]

	labels := make(map[string]int, len(fn.Blocks))
	for _, block := range fn.Blocks {
		labels[block.Label]++
	}
	for label, count := range labels {
		if count > 1 {
			t.Errorf("the label %q names %d blocks, and labels resolve into one flat space:\n%s",
				label, count, strings.Join(render(fn), "\n"))
		}
	}

	// Three case labels reach two blocks, and a successor is an edge liveness
	// walks rather than a label the tag is compared against.
	entry := fn.Blocks[0]
	if got := len(entry.Succs); got != 2 {
		t.Errorf("the switch block has %d successors, want the 2 blocks its three labels reach:\n%s",
			got, strings.Join(render(fn), "\n"))
	}
	seen := make(map[*mir.Block]bool, len(entry.Succs))
	for _, succ := range entry.Succs {
		if seen[succ] {
			t.Errorf("%s is a successor of the switch block twice, which liveness counts twice", succ.Label)
		}
		seen[succ] = true
	}
}

// integerPredicates is every predicate an icmp can carry. A map of predicates
// that misses one selects the zero value, which is not a predicate at all.
var integerPredicates = []llvm.IntPredicate{
	llvm.IntEQ, llvm.IntNE,
	llvm.IntSLT, llvm.IntSGT, llvm.IntSLE, llvm.IntSGE,
	llvm.IntULT, llvm.IntUGT, llvm.IntULE, llvm.IntUGE,
}

// TestPredicateMapsAreTotal holds the two predicate rewrites to covering every
// integer predicate. truthValuePredicates is composed from the other two, so an
// entry dropped from either would silently narrow it and a comparison it did not
// cover would select the complement of what the source asked for.
func TestPredicateMapsAreTotal(t *testing.T) {
	tests := []struct {
		name string
		m    map[llvm.IntPredicate]llvm.IntPredicate
	}{
		{name: "swappedPredicates", m: swappedPredicates},
		{name: "truthValuePredicates", m: truthValuePredicates},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.m) != len(integerPredicates) {
				t.Errorf("%s holds %d predicates, want %d", tt.name, len(tt.m), len(integerPredicates))
			}
			for _, pred := range integerPredicates {
				if _, ok := tt.m[pred]; !ok {
					t.Errorf("%s has no entry for %v", tt.name, pred)
				}
			}
		})
	}

	// The machine's comparison family is signed throughout, so a truth value
	// comparison that came out unsigned would go on to the unsigned rewrites
	// and be tested against a bound the one bit does not have.
	unsigned := []llvm.IntPredicate{llvm.IntULT, llvm.IntUGT, llvm.IntULE, llvm.IntUGE}
	for _, pred := range integerPredicates {
		if got := truthValuePredicates[pred]; slices.Contains(unsigned, got) {
			t.Errorf("truthValuePredicates[%v] = %v, which is unsigned; the machine compares signed and a truth value is 0 or 1 to it", pred, got)
		}
	}

	// Swapping the operands twice is the comparison that was written.
	for _, pred := range integerPredicates {
		if got := swappedPredicates[swappedPredicates[pred]]; got != pred {
			t.Errorf("swapping %v twice gives %v", pred, got)
		}
	}
}
