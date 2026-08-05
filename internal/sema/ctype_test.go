package sema_test

import (
	"testing"

	"github.com/greg2010/ic11c/internal/sema"
)

// TestCTypeRefusesFoldsCDisagreesWith covers the constants whose value depends
// on the C type of the literals under them.
//
// MicroC has one integer type and folds in it, so an operation C computes in a
// narrower type is either a compile-time error there or a different number
// there. Both are refused: the first to stay a subset, the second because a
// program that compiles in both places and means two things is worse than one
// that compiles in neither.
func TestCTypeRefusesFoldsCDisagreesWith(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a sum of two decimal literals that overflows int",
			src:  "constexpr long long k = 2147483647 /*!*/+ 1;\nvoid main(void) { __ic_store(d0, Setting, k); }",
			want: "does not fit 'int'",
		},
		{
			name: "the negation of a hexadecimal literal C types unsigned",
			src:  "void main(void) { long long a = /*!*/-0x80000000; __ic_store(d0, Setting, a); }",
			want: "does not fit 'unsigned int'",
		},
		{
			name: "a sum that wraps an unsigned int",
			src:  "void main(void) { long long b = 0xFFFFFFFF /*!*/+ 1; __ic_store(d0, Setting, b); }",
			want: "does not fit 'unsigned int'",
		},
		{
			name: "a negative operand converted to unsigned int",
			src:  "void main(void) { if (/*!*/-1 < 0xFFFFFFFF) { __ic_store(d0, On, 1); } }",
			want: "C converts this operand of '<' to 'unsigned int'",
		},
		{
			name: "a negative dividend converted to unsigned int",
			src:  "void main(void) { long long q = /*!*/-1 / 0x80000000; __ic_store(d0, Setting, q); }",
			want: "C converts this operand of '/' to 'unsigned int'",
		},
		{
			name: "a negative operand of or, whose sign the conversion clears",
			src:  "void main(void) { long long v = /*!*/-1 | 0xFFFFFFFF; __ic_store(d0, Setting, v); }",
			want: "C converts this operand of '|' to 'unsigned int'",
		},
		{
			name: "a negative operand of xor, whose sign the conversion clears",
			src:  "void main(void) { long long v = /*!*/-1 ^ 0xFFFFFFFF; __ic_store(d0, Setting, v); }",
			want: "C converts this operand of '^' to 'unsigned int'",
		},
		{
			name: "a negative operand the conversion makes equal to the other",
			src:  "void main(void) { if (/*!*/-1 == 0xFFFFFFFF) { __ic_store(d0, On, 1); } }",
			want: "C converts this operand of '==' to 'unsigned int'",
		},
		{
			name: "a negative operand the conversion stops being unequal to the other",
			src:  "void main(void) { if (/*!*/-1 != 0xFFFFFFFF) { __ic_store(d0, On, 1); } }",
			want: "C converts this operand of '!=' to 'unsigned int'",
		},
		{
			name: "a negative operand the conversion lifts past the other",
			src:  "void main(void) { if (/*!*/-1 <= 0x80000000) { __ic_store(d0, On, 1); } }",
			want: "C converts this operand of '<=' to 'unsigned int'",
		},
		{
			name: "a negative operand of greater-than the conversion lifts",
			src:  "void main(void) { if (/*!*/-1 > 0x80000000) { __ic_store(d0, On, 1); } }",
			want: "C converts this operand of '>' to 'unsigned int'",
		},
		{
			name: "a negative operand of greater-or-equal the conversion lifts",
			src:  "void main(void) { if (/*!*/-1 >= 0x80000000) { __ic_store(d0, On, 1); } }",
			want: "C converts this operand of '>=' to 'unsigned int'",
		},
		{
			name: "a negative dividend of a remainder converted to unsigned int",
			src:  "void main(void) { long long r = /*!*/-1 % 0x80000000; __ic_store(d0, Setting, r); }",
			want: "C converts this operand of '%' to 'unsigned int'",
		},
		{
			name: "a sum of a negative operand that leaves an unsigned int",
			src:  "void main(void) { long long s = -5 /*!*/+ (0xFFFFFFFF & 1); __ic_store(d0, Setting, s); }",
			want: "the result of '+' is -4, which does not fit 'unsigned int'",
		},
		{
			name: "a difference from a negative operand that leaves an unsigned int",
			src:  "void main(void) { long long d = -1 /*!*/- (0xFFFFFFFF & 1); __ic_store(d0, Setting, d); }",
			want: "the result of '-' is -2, which does not fit 'unsigned int'",
		},
		{
			name: "a product of a negative operand that leaves an unsigned int",
			src:  "void main(void) { long long p = -1 /*!*/* 0x80000000; __ic_store(d0, Setting, p); }",
			want: "the result of '*' is -2147483648, which does not fit 'unsigned int'",
		},
		{
			name: "a product that overflows int",
			src:  "void main(void) { long long p = 2000000000 /*!*/* 3; __ic_store(d0, Setting, p); }",
			want: "does not fit 'int'",
		},
		{
			name: "a product that wraps an unsigned int",
			src:  "void main(void) { long long p = 0x80000000 /*!*/* 2; __ic_store(d0, Setting, p); }",
			want: "does not fit 'unsigned int'",
		},
		{
			name: "a difference that underflows int",
			src:  "void main(void) { long long d = -2000000000 /*!*/- 2000000000; __ic_store(d0, Setting, d); }",
			want: "does not fit 'int'",
		},
		{
			// One below the most negative int, where the row above is far below
			// it. The value one above this one is what every accepted row here
			// spells '(-2147483647 - 1)'.
			name: "a difference one past the most negative int",
			src:  "void main(void) { long long d = -2147483647 /*!*/- 2; __ic_store(d0, Setting, d); }",
			want: "the result of '-' is -2147483649, which does not fit 'int'",
		},
		{
			// The negative value an unsigned int comes closest to holding: its
			// bit pattern is one the type does hold, and the number is not.
			name: "a difference of minus one that leaves an unsigned int",
			src:  "void main(void) { long long d = 0x80000000 /*!*/- 0x80000001; __ic_store(d0, Setting, d); }",
			want: "the result of '-' is -1, which does not fit 'unsigned int'",
		},
		{
			name: "the complement of an unsigned int",
			src:  "void main(void) { long long m = /*!*/~0xFFFFFFFF; __ic_store(d0, Setting, m); }",
			want: "does not fit 'unsigned int'",
		},
		{
			// The shift takes its type from the left operand alone, so the cast
			// on the count widens nothing and the sum is still computed in an
			// int.
			name: "a sum under a shift whose count a cast widens",
			src:  "void main(void) { long long m = (1 << (long long)3) /*!*/+ 2147483647; __ic_store(d0, Setting, m); }",
			want: "does not fit 'int'",
		},
		{
			name: "a sum of a character literal that overflows int",
			src:  "void main(void) { long long m = 'A' /*!*/+ 2147483647; __ic_store(d0, Setting, m); }",
			want: "does not fit 'int'",
		},
		{
			name: "a shift into the sign bit of an int",
			src:  "void main(void) { long long m = 1 /*!*/<< 31; __ic_store(d0, Setting, m); }",
			want: "does not fit 'int'",
		},
		{
			name: "a shift count the width of an int does not admit",
			src:  "void main(void) { long long m = 1 /*!*/<< 32; __ic_store(d0, Setting, m); }",
			want: "the width of 'int'",
		},
		{
			name: "a shift count past the width of a long long",
			src:  "void main(void) { long long m = (long long)1 /*!*/<< 64; __ic_store(d0, Setting, m); }",
			want: "the width of 'long long'",
		},
		{
			name: "a left shift of a negative value",
			src:  "void main(void) { long long m = -1 /*!*/<< 1; __ic_store(d0, Setting, m); }",
			want: "left shift of a negative value",
		},
		{
			name: "a quotient C leaves undefined",
			src:  "void main(void) { long long q = (-2147483647 - 1) /*!*// -1; __ic_store(d0, Setting, q); }",
			want: "C leaves '/' undefined where the quotient does not fit 'int'",
		},
		{
			// The remainder is zero, which every type holds, so nothing about the
			// answer objects to it. C makes '%' undefined wherever '/' is.
			name: "a remainder C leaves undefined",
			src:  "void main(void) { long long r = (-2147483647 - 1) /*!*/% -1; __ic_store(d0, Setting, r); }",
			want: "C leaves '%' undefined where the quotient does not fit 'int'",
		},
		{
			name: "an arm of a conditional the other arm types unsigned",
			src:  "void main(void) { long long v = 1 /*!*/? -1 : 0xFFFFFFFF; __ic_store(d0, Setting, v); }",
			want: "does not fit 'unsigned int'",
		},
		{
			name: "an overflowing fold reached through a constexpr int",
			src:  "constexpr long long kBits = 8;\nvoid main(void) { long long m = 1 /*!*/<< (kBits * 8); __ic_store(d0, Setting, m); }",
			want: "the width of 'int'",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejected(t, tt.src, tt.want)
		})
	}
}

// TestCTypeFoldsAgreeWithC pins the folds that survive, each against the number
// clang computes for the same expression. A fold that agrees is what lets the
// compiler leave the value to be computed in the machine's own 64-bit
// arithmetic, which is the only arithmetic the chip has.
func TestCTypeFoldsAgreeWithC(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want int64
	}{
		// The three boundaries of the list C searches for a hexadecimal
		// constant's type. The negation is what says which type was chosen: an
		// unsigned one has no negative value, so a literal typed unsigned by
		// mistake would be refused here rather than fold.
		{name: "a hexadecimal literal at the largest int, which is signed", expr: "-0x7FFFFFFF", want: -2147483647},
		{name: "a hexadecimal literal C types unsigned", expr: "0xFFFFFFFF", want: 4294967295},
		{name: "a hexadecimal literal one past what an unsigned int holds", expr: "-0x100000000", want: -4294967296},
		{name: "a decimal literal past what an int holds", expr: "2147483648", want: 2147483648},
		{name: "the negation of a decimal literal past an int", expr: "-2147483648", want: -2147483648},
		{name: "a sum widened by a cast on one operand", expr: "(long long)2147483647 + 1", want: 2147483648},
		{name: "a sum of decimal literals that stays inside an int", expr: "2147483646 + 1", want: 2147483647},
		{name: "a sum of decimal literals C types long long", expr: "4294967296 + 1", want: 4294967297},
		{name: "a mask that stays inside an unsigned int", expr: "0xFFFFFFFF & 0xFF", want: 255},
		{name: "an unsigned division whose quotient stays in range", expr: "0xFFFFFFFF / 2", want: 2147483647},
		{name: "an unsigned right shift", expr: "0x80000000 >> 1", want: 1073741824},
		{name: "a shift into the top bit an int admits", expr: "1 << 30", want: 1073741824},
		{name: "a shift widened by a cast on the left operand", expr: "(long long)1 << 40", want: 1099511627776},
		{name: "the complement of an int", expr: "~0xff", want: -256},
		{name: "an arithmetic right shift of a negative int", expr: "-1 >> 1", want: -1},
		{name: "a character literal in an int fold", expr: "'a' + 1", want: 98},
		{name: "a conditional whose arms are both int", expr: "1 ? -1 : 2", want: -1},

		// The conversion to unsigned int changes the negative operand in every
		// row below, and the operator answers alike over the number it produced
		// and the number the source wrote. '&' is the one with no other side:
		// the conversion preserves bits 0 to 31 and clears everything above
		// them in the other operand, so the mask reads only bits both agree on.
		{name: "a mask of a negative operand C converts to unsigned int", expr: "-1 & 0x80000000", want: 2147483648},
		{name: "a mask that keeps only the bits the conversion preserves", expr: "-123456 & 0xDEADBEEF", want: 3735821504},
		{name: "a mask of the most negative int", expr: "(-2147483647 - 1) & 0xFFFFFFFF", want: 2147483648},
		{name: "an equality the conversion does not make true", expr: "(long long)(-1 == 0x80000000)", want: 0},
		{name: "an inequality the conversion does not make false", expr: "(long long)(-1 != 0x80000000)", want: 1},
		{name: "a less-than both arithmetics answer alike", expr: "(long long)((-2147483647 - 1) < 0xFFFFFFFF)", want: 1},
		{name: "a less-or-equal both arithmetics answer alike", expr: "(long long)(-1 <= 0xFFFFFFFF)", want: 1},
		{name: "a greater-than both arithmetics answer alike", expr: "(long long)(-1 > 0xFFFFFFFF)", want: 0},
		{name: "a greater-or-equal both arithmetics answer alike", expr: "(long long)((-2147483647 - 1) >= 0xFFFFFFFF)", want: 0},
		// The conversion makes this pair equal, which is the one shape that
		// separates '>=' from '>' and from '<='. Every other accepted row here
		// compares two values the conversion leaves strictly ordered, where all
		// three operators agree.
		{name: "a greater-or-equal the conversion makes equal", expr: "(long long)(0xFFFFFFFF >= -1)", want: 1},
		{name: "a quotient the conversion does not change", expr: "(-2147483647 - 1) / 0xFFFFFFFF", want: 0},

		// The pair a signed type leaves undefined is the most negative value it
		// holds over -1, so widening the type or moving either operand off that
		// pair is enough for C to define the operation again.
		{name: "the undefined quotient widened by a cast", expr: "(long long)(-2147483647 - 1) / -1", want: 2147483648},
		{name: "the undefined remainder widened by a cast", expr: "(long long)(-2147483647 - 1) % -1", want: 0},
		{name: "the most negative int over a divisor that is not -1", expr: "(-2147483647 - 1) / -2", want: 1073741824},
		{name: "the most negative int over a divisor that is not -1, as a remainder", expr: "(-2147483647 - 1) % -2", want: 0},
		{name: "a dividend one above the most negative int, over -1", expr: "-2147483647 / -1", want: 2147483647},
		{name: "a dividend one above the most negative int, over -1, as a remainder", expr: "-2147483647 % -1", want: 0},

		// A divisor of -1 under an unsigned type is 4294967295 once C has
		// converted it, and nothing about that pair is undefined. Asking the
		// question of the operands the source wrote instead would find the
		// most negative value the type holds — zero — over -1, and refuse a
		// remainder C defines as 0 and a quotient C defines as 0.
		{name: "an unsigned remainder by a divisor the conversion lifts off -1", expr: "(0x80000000 - 0x80000000) % -1", want: 0},
		{name: "an unsigned quotient by a divisor the conversion lifts off -1", expr: "(0x80000000 - 0x80000000) / -1", want: 0},
		{name: "a remainder the conversion does not change", expr: "-1 % (0xFFFFFFFF & 1)", want: 0},
		{name: "a sum whose result an unsigned int holds", expr: "-1 + 0x80000000", want: 2147483647},
		{name: "a difference whose result an unsigned int holds", expr: "0x80000000 - (0 - 1)", want: 2147483649},
		{name: "a product the conversion leaves at zero", expr: "-1 * (0xFFFFFFFF & 0)", want: 0},

		// '|' and '^' are the two the conversion always parts, since it clears a
		// sign bit neither operator can put back. Their other side is a pair
		// unsigned int holds outright, which the conversion does not touch, and
		// a rule refusing the operator rather than the change would take these
		// with it.
		{name: "an or of two operands unsigned int holds", expr: "0x80000000 | 1", want: 2147483649},
		{name: "an xor of two operands unsigned int holds", expr: "0xF0F0F0F0 ^ 0x0F0F0F0F", want: 4294967295},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "constexpr long long k = " + tt.expr + ";\nvoid main(void) { __ic_store(d0, Setting, k); }"
			prog, diags := analyze(t, src)
			if len(diags) != 0 {
				t.Fatalf("analysis rejected %s:\n%s", tt.expr, diags.String())
			}
			got := foldedGlobal(t, prog, "k")
			if got.Type.Kind() != sema.Int {
				t.Fatalf("%s folded to type %s, want long long", tt.expr, got.Type)
			}
			if got.Int != tt.want {
				t.Errorf("%s folded to %d, want %d", tt.expr, got.Int, tt.want)
			}
		})
	}
}

// foldedGlobal returns the value the named constexpr global folded to.
func foldedGlobal(t *testing.T, prog *sema.Program, name string) sema.Value {
	t.Helper()
	for _, sym := range prog.Globals {
		if sym.Name != name {
			continue
		}
		if sym.Value == nil {
			t.Fatalf("'%s' folded to no value", name)
		}
		return *sym.Value
	}
	t.Fatalf("the program declares no global named '%s'", name)
	return sema.Value{}
}
