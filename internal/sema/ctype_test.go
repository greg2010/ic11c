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
			name: "the complement of an unsigned int",
			src:  "void main(void) { long long m = /*!*/~0xFFFFFFFF; __ic_store(d0, Setting, m); }",
			want: "does not fit 'unsigned int'",
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
		{name: "a hexadecimal literal C types unsigned", expr: "0xFFFFFFFF", want: 4294967295},
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
