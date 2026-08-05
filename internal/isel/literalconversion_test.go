package isel

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/source"
	"tinygo.org/x/go-llvm"
)

// propagatedOperand puts a value in a mutable local and reads it back through a
// bitwise or shift operator, so the operand reaches selection only because the
// optimizer propagated it out of the alloca. Analysis records a value for a
// constexpr object alone, so a local walks straight past its window.
const propagatedOperand = `const dev out = d1;

void main(void) {
    while (true) {
        %s
        __ic_store(out, Setting, (double)(%s));
        __ic_yield();
    }
}`

// TestBitwiseValueOperandAtOrAboveTheReductionModulusIsRefused holds selection
// to refusing every value magnitude GetVariableLong does not carry. A value
// operand arrives through DoubleToLong, which is (long)(d % 2^53), so 3·2^52
// reaches a shift as 2^52 and nothing on the chip reports the substitution.
func TestBitwiseValueOperandAtOrAboveTheReductionModulusIsRefused(t *testing.T) {
	cases := []struct {
		name  string
		decls string
		expr  string
	}{
		{
			name:  "a right shift of 2^53",
			decls: "long long x = 9007199254740992;",
			expr:  "x >> 1",
		},
		{
			name:  "a right shift of -2^53",
			decls: "long long x = -9007199254740992;",
			expr:  "x >> 1",
		},
		{
			name:  "a left shift of 2^53",
			decls: "long long x = 9007199254740992;",
			expr:  "x << 1",
		},
		{
			name:  "a mask over 2^53",
			decls: "long long x = 9007199254740992;",
			expr:  "x & 255",
		},
		{
			name:  "an or over -2^53",
			decls: "long long x = -9007199254740992;",
			expr:  "x | 1",
		},
		{
			name:  "an exclusive or over 2^53",
			decls: "long long x = 9007199254740992;",
			expr:  "x ^ 1",
		},
		{
			name:  "a complement of 2^53",
			decls: "long long x = 9007199254740992;",
			expr:  "~x",
		},
		{
			name:  "2^53 reached by arithmetic the source never wrote",
			decls: "long long h = 4503599627370496; long long x = h + h;",
			expr:  "x >> 1",
		},
		{
			// The residue is 2^52 rather than 0, so a rule written against the
			// values the reduction sends to zero leaves this one emitted.
			name:  "three halves of the modulus, whose residue is not zero",
			decls: "long long h = 4503599627370496; long long x = h + h + h;",
			expr:  "x >> 1",
		},
		{
			name:  "the negation of three halves of the modulus",
			decls: "long long h = -4503599627370496; long long x = h + h + h;",
			expr:  "x | 1",
		},
		{
			name:  "2^60, which is seven doublings past the modulus",
			decls: "long long h = 4503599627370496; long long x = h * 256;",
			expr:  "x | 1",
		},
		{
			name:  "2^60 as the value a mask is taken over",
			decls: "long long h = 4503599627370496; long long x = h * 256;",
			expr:  "x & 255",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := selectSource(t, fmt.Sprintf(propagatedOperand, tc.decls, tc.expr))
			if err == nil {
				t.Fatalf("selection accepted a value operand the machine reduces: %s", tc.expr)
			}
			assertValueRefusalIsUsable(t, err)
		})
	}
}

// TestBitwiseValueOperandBelowTheReductionModulusKeepsItsValue holds the window
// the refusal above must not close. The cases assert the value the chip wrote:
// a bound stated one value too wide would still emit an instruction, and only
// the answer says which value the operand arrived as.
func TestBitwiseValueOperandBelowTheReductionModulusKeepsItsValue(t *testing.T) {
	cases := []struct {
		name  string
		decls string
		expr  string
		want  float64
	}{
		{
			name:  "a right shift of one below 2^53",
			decls: "long long x = 9007199254740991;",
			expr:  "x >> 1",
			want:  4503599627370495,
		},
		{
			name:  "a right shift of one above -2^53",
			decls: "long long x = -9007199254740991;",
			expr:  "x >> 1",
			want:  -4503599627370496,
		},
		{
			name:  "a mask over one below 2^53",
			decls: "long long x = 9007199254740991;",
			expr:  "x & 255",
			want:  255,
		},
		{
			name:  "an or over two below 2^53",
			decls: "long long x = 9007199254740990;",
			expr:  "x | 1",
			want:  9007199254740991,
		},
		{
			name:  "an exclusive or over half the modulus",
			decls: "long long x = 4503599627370495;",
			expr:  "x ^ 1",
			want:  4503599627370494,
		},
		{
			name:  "a complement well inside the window",
			decls: "long long x = 255;",
			expr:  "~x",
			want:  -256,
		},
		{
			// The refusal reads a magnitude rather than a source line, so the
			// value a step below has to stay open however it was formed.
			name:  "one below the modulus reached by arithmetic",
			decls: "long long h = 4503599627370496; long long x = h + h - 1;",
			expr:  "x >> 1",
			want:  4503599627370495,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assembly := compileSource(t, fmt.Sprintf(propagatedOperand, tc.decls, tc.expr))
			events := runWorld(t, assembly, func(*testing.T, *world) {}, 1)
			assertWrote(t, events, 1, logicType(t, "Setting"), tc.want, assembly)
		})
	}
}

// TestShiftDistanceOutsideTheMachinesWindowIsRefused holds selection to refusing
// every shift distance the machine does not shift by. A distance goes through
// GetVariableInt, which faults past ±2^31 where the value operand's conversion
// reduces instead; see [TestTheChipShiftsByTheLowSixBitsOfTheDistance].
func TestShiftDistanceOutsideTheMachinesWindowIsRefused(t *testing.T) {
	distances := []struct {
		name string
		// decls declares the shifted value and the distance. A distance past the
		// modulus is reached by arithmetic, since sema refuses a literal there.
		decls string
		// faults says the chip stops on this distance rather than shifting by a
		// masked one, which is what decides the outcome the refusal names.
		faults bool
	}{
		{name: "64, the first distance the mask changes", decls: "long long n = 64;"},
		{name: "1000, well inside the conversion range", decls: "long long n = 1000;"},
		{name: "2147483647, the last distance the conversion carries", decls: "long long n = 2147483647;"},
		{name: "2147483648, the first distance the conversion faults on", decls: "long long n = 2147483648;", faults: true},
		{name: "2^52, between the conversion bound and the modulus", decls: "long long n = 4503599627370496;", faults: true},
		{name: "2^53, the modulus the value operand is held to", decls: "long long n = 9007199254740992;", faults: true},
		{name: "three halves of the modulus, which is past it", decls: "long long h = 4503599627370496; long long n = h + h + h;", faults: true},
		{name: "-1, which the mask turns into 63", decls: "long long n = -1;"},
		{name: "-8, which the mask turns into 56", decls: "long long n = -8;"},
		{name: "-2147483648, the last negative the conversion carries", decls: "long long n = -2147483648;"},
		{name: "-2147483649, the first negative the conversion faults on", decls: "long long n = -2147483649;", faults: true},
	}

	forms := []struct{ name, op string }{
		{name: "a right shift", op: ">>"},
		{name: "a left shift", op: "<<"},
	}

	for _, form := range forms {
		for _, tc := range distances {
			t.Run(form.name+" by "+tc.name, func(t *testing.T) {
				decls := "long long v = 6; " + tc.decls
				_, err := selectSource(t, fmt.Sprintf(propagatedOperand, decls, "v "+form.op+" n"))
				if err == nil {
					t.Fatalf("selection accepted a shift distance the machine does not shift by: %s", tc.decls)
				}
				assertDistanceRefusalIsUsable(t, err, tc.faults)
			})
		}
	}
}

// TestShiftDistanceIsRefusedForEveryShiftForm covers srl, which MicroC has no
// unsigned type to write and which arrives only when the optimizer proves the
// value non-negative. The rule is keyed on the operand position rather than on
// the mnemonic a pattern happened to pick.
func TestShiftDistanceIsRefusedForEveryShiftForm(t *testing.T) {
	cases := []struct {
		name  string
		build func(bd *builder, distance int64) llvm.Value
	}{
		{"sll", func(bd *builder, d int64) llvm.Value { return bd.b.CreateShl(bd.opaque("x"), bd.konst(d), "") }},
		{"sra", func(bd *builder, d int64) llvm.Value { return bd.b.CreateAShr(bd.opaque("x"), bd.konst(d), "") }},
		{"srl", func(bd *builder, d int64) llvm.Value { return bd.b.CreateLShr(bd.opaque("x"), bd.konst(d), "") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bd := newBuilder(t)
			bd.keep(tc.build(bd, 64))
			bd.b.CreateRetVoid()
			if _, err := Select(t.Context(), bd.m, Options{File: "test.c"}); err == nil {
				t.Fatalf("selection accepted a %s by 64, which the machine shifts by 0", tc.name)
			}

			bd = newBuilder(t)
			bd.keep(tc.build(bd, 63))
			bd.b.CreateRetVoid()
			if _, err := Select(t.Context(), bd.m, Options{File: "test.c"}); err != nil {
				t.Fatalf("selection refused a %s by 63, which the machine shifts by as written: %v", tc.name, err)
			}
		})
	}
}

// TestShiftDistanceInsideTheMachinesWindowShiftsByIt holds the window the
// refusal above must not close. A bound one distance too narrow refuses a
// program that runs and one too wide emits a shift by a different distance, and
// only the answer separates the two.
func TestShiftDistanceInsideTheMachinesWindowShiftsByIt(t *testing.T) {
	cases := []struct {
		name  string
		decls string
		expr  string
		want  float64
	}{
		{
			name:  "a right shift by 0, which is the identity",
			decls: "long long v = 6; long long n = 0;",
			expr:  "v >> n",
			want:  6,
		},
		{
			name:  "a right shift by 1",
			decls: "long long v = 6; long long n = 1;",
			expr:  "v >> n",
			want:  3,
		},
		{
			name:  "a left shift by 52, the top of the range a long long survives",
			decls: "long long v = 1; long long n = 52;",
			expr:  "v << n",
			want:  4503599627370496,
		},
		{
			name:  "a right shift by 63, the last distance the mask leaves alone",
			decls: "long long v = 6; long long n = 63;",
			expr:  "v >> n",
			want:  0,
		},
		{
			// sra fills from the sign, so 63 separates it from a shift that
			// answers zero for everything.
			name:  "a right shift of a negative value by 63",
			decls: "long long v = -6; long long n = 63;",
			expr:  "v >> n",
			want:  -1,
		},
		{
			// Both operands sit at their own boundary, which a rule carrying one
			// bound across to the other operand would refuse.
			name:  "one below the modulus shifted by 63",
			decls: "long long v = 9007199254740991; long long n = 63;",
			expr:  "v >> n",
			want:  0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assembly := compileSource(t, fmt.Sprintf(propagatedOperand, tc.decls, tc.expr))
			events := runWorld(t, assembly, func(*testing.T, *world) {}, 1)
			assertWrote(t, events, 1, logicType(t, "Setting"), tc.want, assembly)
		})
	}
}

// assertValueRefusalIsUsable holds a refusal over a value operand to naming the
// source it came from and the modulus that governs it.
func assertValueRefusalIsUsable(t *testing.T, err error) {
	t.Helper()
	text := err.Error()
	if !strings.Contains(text, sourceFile+":") {
		t.Errorf("the refusal carries no source position: %s", text)
	}
	if !strings.Contains(text, "2^53") {
		t.Errorf("the refusal does not name the modulus that governs it: %s", text)
	}
}

// assertDistanceRefusalIsUsable holds a refusal over a shift distance to naming
// its source, its window, and its outcome. The two outcomes are not
// interchangeable advice: a distance the chip stops on loses the program's
// writes, where one the shift masks runs and answers wrongly.
func assertDistanceRefusalIsUsable(t *testing.T, err error, faults bool) {
	t.Helper()
	text := err.Error()
	if !strings.Contains(text, sourceFile+":") {
		t.Errorf("the refusal carries no source position: %s", text)
	}
	if !strings.Contains(text, "0 through 63") {
		t.Errorf("the refusal does not name the window a distance has to be in: %s", text)
	}
	if strings.Contains(text, "2^53") {
		t.Errorf("the refusal names the value operand's modulus, which does not govern a distance: %s", text)
	}
	want, unwanted := "stops at this line", "keeps its low"
	if !faults {
		want, unwanted = unwanted, want
	}
	if !strings.Contains(text, want) {
		t.Errorf("the refusal does not name the outcome this distance has: %s", text)
	}
	if strings.Contains(text, unwanted) {
		t.Errorf("the refusal names the outcome this distance does not have: %s", text)
	}
}

// TestUnconvertedRefusesAnOpcodeTheMachineConverts holds the guard the
// instructions no pattern selects rest on. One of them gaining a converting
// operand in a later game build is a compilation that has to stop, not one that
// quietly emits a reduced value.
func TestUnconvertedRefusesAnOpcodeTheMachineConverts(t *testing.T) {
	tests := []struct {
		name     string
		op       ic10.Opcode
		args     []mir.Operand
		accepted bool
	}{
		{name: "the data region zeroing", op: isa.OpClr, args: []mir.Operand{mir.NewDeviceBase()}, accepted: true},
		{name: "the jump onto a split edge", op: isa.OpJ, args: []mir.Operand{mir.Label{Name: "main.entry"}}, accepted: true},
		{
			name:     "the copy a phi becomes",
			op:       isa.OpMove,
			args:     []mir.Operand{mir.VirtReg{ID: 0}, mir.Imm{Value: 1}},
			accepted: true,
		},
		{
			name:     "the return address save",
			op:       isa.OpPush,
			args:     []mir.Operand{mir.PhysReg{Reg: ic10.RegRA}},
			accepted: true,
		},
		{
			name:     "the return address restore",
			op:       isa.OpPop,
			args:     []mir.Operand{mir.PhysReg{Reg: ic10.RegRA}},
			accepted: true,
		},
		{
			// sll reads its value modulo 2^53 and its distance as an int, so a
			// literal in either position is not handed over as written.
			name: "a shift, whose operands the machine converts",
			op:   isa.OpSll,
			args: []mir.Operand{mir.PhysReg{Reg: 0}, mir.PhysReg{Reg: 1}, mir.Imm{Value: 1}},
		},
		{
			// and reads both operands modulo 2^53.
			name: "a bitwise operator, whose operands the machine converts",
			op:   isa.OpAnd,
			args: []mir.Operand{mir.PhysReg{Reg: 0}, mir.PhysReg{Reg: 1}, mir.Imm{Value: 1}},
		},
		{
			name: "an opcode the machine has no entry for",
			op:   ic10.Opcode(math.MaxUint16),
			args: []mir.Operand{mir.PhysReg{Reg: 0}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instr, err := unconverted(tt.op, source.Position{File: sourceFile, Line: 1}, tt.args...)
			if tt.accepted {
				if err != nil {
					t.Fatalf("unconverted(%v) refused an instruction this package builds: %v", tt.op, err)
				}
				if instr == nil {
					t.Fatal("unconverted answered no instruction and no error")
				}
				return
			}
			if err == nil {
				t.Fatalf("unconverted(%v) built %s without the conversion check", tt.op, instr)
			}
		})
	}
}
