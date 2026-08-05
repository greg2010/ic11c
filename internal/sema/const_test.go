package sema_test

import (
	"fmt"
	"testing"

	"github.com/greg2010/ic11c/internal/sema"
)

// foldedInitializer is the value analysis folded a global's initializer to, read
// out of [sema.Program.Consts], which is where every later phase reads it.
//
// It exists because a test that only checked the program compiled establishes
// nothing about what it compiled to: a row asserting that '9007199254740991 &
// -1' is accepted passes exactly as well on a fold that answered with the
// operands ored together.
func foldedInitializer(t *testing.T, typ, expr string) sema.Value {
	t.Helper()
	src := fmt.Sprintf("%s g = %s;\nvoid main(void) { __ic_store(d0, Setting, g); }\n", typ, expr)
	prog, diags := analyze(t, src)
	if len(diags) != 0 {
		t.Fatalf("analysis rejected '%s':\n%s", expr, diags.String())
	}
	if len(prog.Consts) != 1 {
		t.Fatalf("'%s' folded to %d constants, want the one the initializer is", expr, len(prog.Consts))
	}
	for _, v := range prog.Consts {
		return v
	}
	t.Fatalf("'%s' folded to no value", expr)
	return sema.Value{}
}

// TestConstantsFoldToTheValueTheSourceWrote covers what each operator answers,
// rather than that a program writing it is accepted.
//
// The fold is what a case label, an array bound, and a constexpr object become,
// so an operator answering with the wrong number puts that number into the
// emitted program with nothing said. Every row states the value and the type it
// carries: the type decides which half of a [sema.Value] the readers consult,
// and a bool that folded to an integer 1 renders identically to one.
//
// The integer arithmetic C computes in a narrower type than the machine's own is
// [TestCTypeFoldsAgreeWithC], which asserts values for a different reason.
func TestConstantsFoldToTheValueTheSourceWrote(t *testing.T) {
	tests := []struct {
		name string
		typ  string
		expr string
		want string
		kind sema.Kind
	}{
		{name: "and at the widest operand the machine holds", typ: "long long", expr: "9007199254740991 & -1", want: "9007199254740991", kind: sema.Int},
		{name: "or", typ: "long long", expr: "1024 | 3", want: "1027", kind: sema.Int},
		{name: "xor", typ: "long long", expr: "0xff ^ 0x0f", want: "240", kind: sema.Int},
		{name: "complement at the widest operand the machine holds", typ: "long long", expr: "~9007199254740991", want: "-9007199254740992", kind: sema.Int},
		{name: "xor carrying every bit above the widest operand", typ: "long long", expr: "-1 ^ 9007199254740991", want: "-9007199254740992", kind: sema.Int},
		{name: "xor of a value and its negation", typ: "long long", expr: "9007199254740991 ^ -9007199254740991", want: "-2", kind: sema.Int},
		{name: "left shift landing one inside the limit", typ: "long long", expr: "(long long)1 << 52", want: "4503599627370496", kind: sema.Int},
		// An arithmetic shift, so the answer rounds toward negative infinity
		// rather than toward zero the way '/' does.
		{name: "right shift of a negative operand", typ: "long long", expr: "-9007199254740991 >> 1", want: "-4503599627370496", kind: sema.Int},
		{name: "product", typ: "long long", expr: "3600 * 1000", want: "3600000", kind: sema.Int},
		{name: "sum landing on the widest value the machine holds", typ: "long long", expr: "9007199254740991 + 1", want: "9007199254740992", kind: sema.Int},
		{name: "difference landing on the widest negative value", typ: "long long", expr: "-9007199254740991 - 1", want: "-9007199254740992", kind: sema.Int},
		{name: "unary negation at the widest value the machine holds", typ: "long long", expr: "-9007199254740992", want: "-9007199254740992", kind: sema.Int},
		// C truncates toward zero, and the remainder takes the dividend's sign.
		{name: "quotient of a negative dividend", typ: "long long", expr: "-7 / 2", want: "-3", kind: sema.Int},
		{name: "remainder of a negative dividend", typ: "long long", expr: "-7 % 2", want: "-1", kind: sema.Int},

		{name: "integer equality", typ: "bool", expr: "1 == 1", want: "true", kind: sema.Bool},
		{name: "integer inequality", typ: "bool", expr: "1 != 1", want: "false", kind: sema.Bool},
		{name: "integer less than", typ: "bool", expr: "1 < 2", want: "true", kind: sema.Bool},
		{name: "integer less or equal", typ: "bool", expr: "2 <= 1", want: "false", kind: sema.Bool},
		{name: "integer greater than", typ: "bool", expr: "2 > 1", want: "true", kind: sema.Bool},
		{name: "integer greater or equal", typ: "bool", expr: "1 >= 2", want: "false", kind: sema.Bool},
		{name: "logical not", typ: "bool", expr: "!7", want: "false", kind: sema.Bool},

		// A bool operand promotes to int, which is the one shape that reaches the
		// C type of a value the fold did not compute from a literal.
		{name: "bool equality", typ: "bool", expr: "true == false", want: "false", kind: sema.Bool},
		{name: "bool equality under a negation", typ: "bool", expr: "!false == true", want: "true", kind: sema.Bool},

		// The left operand decides these two on its own, and the answer is what
		// says so: reaching the right operand at all would answer with its truth
		// instead.
		{name: "and short-circuits on a false left operand", typ: "bool", expr: "false && true", want: "false", kind: sema.Bool},
		{name: "or short-circuits on a true left operand", typ: "bool", expr: "true || false", want: "true", kind: sema.Bool},
		{name: "and reaching its right operand", typ: "bool", expr: "true && false", want: "false", kind: sema.Bool},
		{name: "or reaching its right operand", typ: "bool", expr: "false || true", want: "true", kind: sema.Bool},

		{name: "conditional taking the then arm", typ: "long long", expr: "1 < 2 ? 10 : 20", want: "10", kind: sema.Int},
		{name: "conditional taking the else arm", typ: "long long", expr: "1 > 2 ? 10 : 20", want: "20", kind: sema.Int},

		{name: "double sum", typ: "double", expr: "0.5 + 0.25", want: "0.75", kind: sema.Double},
		{name: "double difference", typ: "double", expr: "0.5 - 0.25", want: "0.25", kind: sema.Double},
		{name: "double product", typ: "double", expr: "0.5 * 4.0", want: "2", kind: sema.Double},
		{name: "double quotient", typ: "double", expr: "1.0 / 4.0", want: "0.25", kind: sema.Double},
		{name: "double negation", typ: "double", expr: "-0.25", want: "-0.25", kind: sema.Double},
		// The machine holds both zeros and the sign decides what dividing by one
		// answers, so a fold that normalized it would answer the next expression
		// with the wrong infinity.
		{name: "negative zero", typ: "double", expr: "-0.0", want: "-0", kind: sema.Double},
		{name: "division by a negative zero", typ: "double", expr: "1.0 / -0.0", want: "-Inf", kind: sema.Double},
		{name: "a long long operand widened to meet a double one", typ: "double", expr: "1 + 0.5", want: "1.5", kind: sema.Double},

		{name: "double equality", typ: "bool", expr: "1.0 == 1.0", want: "true", kind: sema.Bool},
		{name: "double inequality", typ: "bool", expr: "1.0 != 1.0", want: "false", kind: sema.Bool},
		{name: "double less than", typ: "bool", expr: "1.0 < 2.0", want: "true", kind: sema.Bool},
		{name: "double less or equal", typ: "bool", expr: "2.0 <= 1.0", want: "false", kind: sema.Bool},
		{name: "double greater than", typ: "bool", expr: "2.0 > 1.0", want: "true", kind: sema.Bool},
		{name: "double greater or equal", typ: "bool", expr: "1.0 >= 2.0", want: "false", kind: sema.Bool},
		// Two operands the comparison finds equal, which is what separates the
		// two operators that admit equality from the two that do not.
		{name: "double less or equal on equal operands", typ: "bool", expr: "1.0 <= 1.0", want: "true", kind: sema.Bool},
		{name: "double greater or equal on equal operands", typ: "bool", expr: "1.0 >= 1.0", want: "true", kind: sema.Bool},

		{name: "cast of a double to a long long truncates toward zero", typ: "long long", expr: "(long long)-3.9", want: "-3", kind: sema.Int},
		{name: "cast to bool normalizes", typ: "bool", expr: "(bool)7", want: "true", kind: sema.Bool},
		// A double carries its value in the other half of a [sema.Value], so a
		// cast that read the integer half would answer false for every one of
		// these. The fraction is the row a truncation would also get wrong.
		{name: "cast of a double to bool", typ: "bool", expr: "(bool)2.0", want: "true", kind: sema.Bool},
		{name: "cast of a fraction below one to bool", typ: "bool", expr: "(bool)0.5", want: "true", kind: sema.Bool},
		{name: "cast of a negative fraction to bool", typ: "bool", expr: "(bool)-0.5", want: "true", kind: sema.Bool},
		{name: "cast of a double zero to bool", typ: "bool", expr: "(bool)0.0", want: "false", kind: sema.Bool},
		{name: "cast of a negative double zero to bool", typ: "bool", expr: "(bool)-0.0", want: "false", kind: sema.Bool},
		{name: "cast of a long long to a double widens", typ: "double", expr: "(double)3", want: "3", kind: sema.Double},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := foldedInitializer(t, tt.typ, tt.expr)
			if got := v.String(); got != tt.want {
				t.Errorf("'%s' folded to %s, want %s", tt.expr, got, tt.want)
			}
			if got := v.Type.Kind(); got != tt.kind {
				t.Errorf("'%s' folded to a value of kind %d, want %d", tt.expr, got, tt.kind)
			}
		})
	}
}

// TestNaNComparisonsAllAnswerFalse covers the one number that makes a comparison
// and its opposite agree.
//
// Every ordered comparison against a NaN is false, so '<' and '>=' both answer
// 0 on one and neither is the negation of the other. A fold that reached for the
// cheaper reading — answering '>=' as the complement of '<' — would put the
// wrong constant into a program that runs, which is the shape of miscompile this
// compiler has had.
func TestNaNComparisonsAllAnswerFalse(t *testing.T) {
	const nan = "(0.0 / 0.0)"
	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "less than", expr: nan + " < 1.0", want: "false"},
		{name: "less or equal", expr: nan + " <= 1.0", want: "false"},
		{name: "greater than", expr: nan + " > 1.0", want: "false"},
		{name: "greater or equal", expr: nan + " >= 1.0", want: "false"},
		{name: "less than, with the NaN on the right", expr: "1.0 < " + nan, want: "false"},
		{name: "greater or equal, with the NaN on the right", expr: "1.0 >= " + nan, want: "false"},
		{name: "equality", expr: nan + " == " + nan, want: "false"},
		// The one comparison a NaN answers true, and the only reason the six
		// above are not simply a rule about the operand.
		{name: "inequality", expr: nan + " != " + nan, want: "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := foldedInitializer(t, "bool", tt.expr).String(); got != tt.want {
				t.Errorf("'%s' folded to %s, want %s", tt.expr, got, tt.want)
			}
		})
	}
}

// TestDoubleOperandOfAShortCircuitIsRefused covers the operand '&&' and '||' do
// not take. A double is not a truth on this machine — every value a chip reads
// is fractional — so the comparison has to be written rather than left implied.
func TestDoubleOperandOfAShortCircuitIsRefused(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "the right operand of and",
			src: `bool g = 1 && /*!*/1.0;
void main(void) { __ic_store(d0, Setting, g); }
`,
		},
		{
			name: "the right operand of or",
			src: `bool g = 0 || /*!*/1.0;
void main(void) { __ic_store(d0, Setting, g); }
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejected(t, tt.src, "must be a long long or a bool, found double")
		})
	}
}

// TestConstantOperandsAreDiagnosedAnywhere covers the problems constant folding
// finds in the operands themselves rather than in the context they stand in.
//
// A result past the 53 bits the machine holds, a shift count no shift takes, and
// a division by zero are properties of the numbers written. Reporting them only
// where the language demands a constant expression leaves the same expression in
// a local initializer folding to a number the program did not write, with
// nothing said.
func TestConstantOperandsAreDiagnosedAnywhere(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a shift past the width the machine holds, in a local initializer",
			src: `void main(void) {
    long long x = (long long)1 /*!*/<< 60;
    __ic_store(d0, Setting, x);
}
`,
			want: "a left shift that reaches 2^53 answers with -2^53",
		},
		{
			name: "a shift count no shift takes, in a local initializer",
			src: `void main(void) {
    long long y = (long long)1 /*!*/<< 70;
    __ic_store(d0, Setting, y);
}
`,
			want: "a shift count must be between 0 and 63",
		},
		{
			name: "a division by zero in a local initializer",
			src: `void main(void) {
    long long y = 1 /*!*//0;
    __ic_store(d0, Setting, y);
}
`,
			want: "division by zero in a constant expression",
		},
		{
			name: "a remainder by zero in an argument",
			src: `void main(void) {
    __ic_store(d0, Setting, 7 /*!*/% 0);
}
`,
			want: "remainder by zero in a constant expression",
		},
		{
			name: "a shift past the width the machine holds, in an assignment",
			src: `void main(void) {
    long long x = 0;
    x = (long long)3 /*!*/<< 55;
    __ic_store(d0, Setting, x);
}
`,
			want: "a left shift that reaches 2^53 answers with -2^53",
		},
		{
			name: "a shift past the width the machine holds, under an operand that is not constant",
			src: `long long g;
void main(void) {
    long long x = g + ((long long)1 /*!*/<< 60);
    __ic_store(d0, Setting, x);
}
`,
			want: "a left shift that reaches 2^53 answers with -2^53",
		},
		{
			name: "a cast of an infinity to a long long, in a local initializer",
			src: `void main(void) {
    long long x = /*!*/(long long)(1.0 / 0.0);
    __ic_store(d0, Setting, x);
}
`,
			want: "which is not a value a long long holds",
		},
		{
			// A NaN answers false to every comparison the bound is written as,
			// so it is named on its own rather than caught by them.
			name: "a cast of a NaN to a long long, in a local initializer",
			src: `void main(void) {
    long long x = /*!*/(long long)(0.0 / 0.0);
    __ic_store(d0, Setting, x);
}
`,
			want: "the cast truncates NaN, which is not a value a long long holds",
		},
		{
			// The whole part is exactly 2^63, the first magnitude no int64
			// holds, so the bound has to exclude it rather than stop at it.
			name: "a cast that truncates the first double no long long holds",
			src: `void main(void) {
    long long x = /*!*/(long long)9223372036854775808.0;
    __ic_store(d0, Setting, x);
}
`,
			want: "the cast truncates 9.223372036854776e+18, which is not a value a long long holds",
		},
		{
			name: "a sum past the range the machine holds",
			src: `void main(void) {
    long long x = 9007199254740992 /*!*/+ 8;
    __ic_store(d0, Setting, x);
}
`,
			want: "is outside -2^53 to 2^53",
		},
		{
			// Go's int64 wraps this to zero where the machine grows toward
			// infinity, so folding it silently is a compile-time answer the run
			// time does not agree with.
			name: "a product that wraps a signed 64-bit fold",
			src: `void main(void) {
    long long x = 4503599627370496 /*!*/* 4503599627370496;
    __ic_store(d0, Setting, x);
}
`,
			want: "is outside -2^53 to 2^53",
		},
		{
			name: "a difference past the range the machine holds",
			src: `void main(void) {
    long long x = -9007199254740992 /*!*/- 8;
    __ic_store(d0, Setting, x);
}
`,
			want: "is outside -2^53 to 2^53",
		},
		{
			// One past the negative end, where the row above is eight past it.
			// The value one inside is what the accepted rows fold to.
			name: "a difference one past the negative end of the range",
			src: `void main(void) {
    long long x = -9007199254740992 /*!*/- 1;
    __ic_store(d0, Setting, x);
}
`,
			want: "is outside -2^53 to 2^53",
		},
		{
			name: "a sum one past the positive end of the range",
			src: `void main(void) {
    long long x = 9007199254740992 /*!*/+ 1;
    __ic_store(d0, Setting, x);
}
`,
			want: "is outside -2^53 to 2^53",
		},
		{
			name: "an integer literal past the range the machine holds",
			src: `void main(void) {
    long long x = /*!*/9007199254740993;
    __ic_store(d0, Setting, x);
}
`,
			want: "is outside -2^53 to 2^53",
		},
		{
			name: "an integer literal in a global initializer",
			src: `long long g = /*!*/9007199254740993;
void main(void) { __ic_store(d0, Setting, g); }
`,
			want: "is outside -2^53 to 2^53",
		},
		{
			name: "an integer literal in a constexpr initializer",
			src: `constexpr long long k = /*!*/9007199254740993;
void main(void) { __ic_store(d0, Setting, k); }
`,
			want: "is outside -2^53 to 2^53",
		},
		{
			name: "an integer literal in a case label",
			src: `void main(void) {
    switch ((long long)__ic_load(d0, Setting)) {
    case /*!*/9007199254740993: __ic_store(d1, On, 1); break;
    }
}
`,
			want: "is outside -2^53 to 2^53",
		},
		{
			name: "an integer literal in an array bound",
			src: `void main(void) {
    long long a[/*!*/9007199254740993];
    a[0] = 1;
    __ic_store(d0, Setting, a[0]);
}
`,
			want: "is outside -2^53 to 2^53",
		},
		{
			name: "an integer literal as an intrinsic argument",
			src: `void main(void) {
    __ic_store(d0, Setting, /*!*/9007199254740993);
}
`,
			want: "is outside -2^53 to 2^53",
		},
		{
			name: "a cast that truncates a double past the range the machine holds",
			src: `void main(void) {
    long long x = /*!*/(long long)1e18;
    __ic_store(d0, Setting, x);
}
`,
			want: "is outside -2^53 to 2^53",
		},
		{
			name: "a cast of an infinity to a long long, in a constexpr initializer",
			src: `constexpr long long k = /*!*/(long long)(1.0 / 0.0);
void main(void) { __ic_store(d0, Setting, k); }
`,
			want: "which is not a value a long long holds",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejected(t, tt.src, tt.want)
		})
	}
}

// TestARefusedShiftCountIsNotReportedAsInconstant covers a shift whose count is a
// constant expression that was itself refused.
//
// The count in each row folded to nothing because its own mistake was reported,
// not because the program computes it. Naming it as a count the program computes
// asserts something false about the source and advises a cast on the left
// operand, which leaves the count exactly as wrong as it was.
func TestARefusedShiftCountIsNotReportedAsInconstant(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a count that divides by zero",
			src: `long long g = 1 << (1 /*!*//0);
void main(void) { __ic_store(d0, Setting, g); }
`,
			want: "division by zero in a constant expression",
		},
		{
			name: "a count whose own shift count no shift takes",
			src: `long long g = 65536 >> (0x7f /*!*/>> 0x7f);
void main(void) { __ic_store(d0, Setting, g); }
`,
			want: "a shift count must be between 0 and 31",
		},
		{
			name: "a count that overflows the int C computes it in",
			src: `long long g = 1 << (2147483647 /*!*/+ 1);
void main(void) { __ic_store(d0, Setting, g); }
`,
			want: "does not fit 'int'",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejected(t, tt.src, tt.want)
		})
	}
}

// TestBitwiseOperandsStopOneShortOfTheExactRange covers the one value that
// separates the two limits a long long is held to.
//
// A register holds 2^53 and -2^53 unchanged, so both are constants the language
// admits. Neither reaches a bitwise instruction: the machine reads such an
// operand through a conversion that reduces modulo 2^53, which sends both to
// zero, so a fold answering with the number the source wrote answers something
// the same expression does not compute at run time. The magnitude one lower is
// admitted by both, which is what makes these boundary cases rather than range
// cases — a limit narrowed by one more value would take the second half of each
// pair with it.
func TestBitwiseOperandsStopOneShortOfTheExactRange(t *testing.T) {
	rejected := []struct {
		name string
		src  string
		want string
	}{
		{
			// Folded as 2^53, which is what the array bound then lays out; the
			// machine's and answers 0.
			name: "an and at the limit, in an array bound",
			src: `void main(void) {
    long long a[(/*!*/9007199254740992 & -1) - 9007199254740989];
    a[0] = 1;
    __ic_store(d0, Setting, a[0]);
}
`,
			want: "modulo 2^53",
		},
		{
			// A constexpr object is the fold itself rather than the operator, so
			// this one reaches an emitted literal.
			name: "an and at the limit, in a constexpr initializer",
			src: `constexpr long long k = /*!*/9007199254740992 & -1;
void main(void) { __ic_store(d0, Setting, k); }
`,
			want: "modulo 2^53",
		},
		{
			name: "an or whose negative operand is at the limit",
			src: `constexpr long long k = (/*!*/-9007199254740992) | 1;
void main(void) { __ic_store(d0, Setting, k); }
`,
			want: "modulo 2^53",
		},
		{
			name: "an xor at the limit",
			src: `constexpr long long k = /*!*/9007199254740992 ^ 1;
void main(void) { __ic_store(d0, Setting, k); }
`,
			want: "modulo 2^53",
		},
		{
			name: "a complement at the limit",
			src: `constexpr long long k = ~/*!*/9007199254740992;
void main(void) { __ic_store(d0, Setting, k); }
`,
			want: "modulo 2^53",
		},
		{
			// The result is well inside the range, so only the operand test
			// finds this one.
			name: "a right shift of the limit, whose result is small",
			src: `constexpr long long k = /*!*/9007199254740992 >> 1;
void main(void) { __ic_store(d0, Setting, k); }
`,
			want: "modulo 2^53",
		},
		{
			// The operand test comes first and stops the fold, so the result
			// test never runs and one mistake still gets one message.
			name: "a left shift of the limit, whose result is past it as well",
			src: `constexpr long long k = /*!*/9007199254740992 << 1;
void main(void) { __ic_store(d0, Setting, k); }
`,
			want: "modulo 2^53",
		},
		{
			// Both operands are past the window, and the left one is named
			// alone: the operand that carried the diagnostic is what stops the
			// second look.
			name: "an and both of whose operands are at the limit",
			src: `constexpr long long k = /*!*/9007199254740992 & -9007199254740992;
void main(void) { __ic_store(d0, Setting, k); }
`,
			want: "modulo 2^53",
		},
		{
			// The operand is 1 and the answer is 2^53, which the conversion back
			// out of 53 bits and a sign reads as -2^53.
			name: "a left shift landing on the limit",
			src: `constexpr long long k = (long long)1 /*!*/<< 53;
void main(void) { __ic_store(d0, Setting, k); }
`,
			want: "a left shift that reaches 2^53 answers with -2^53",
		},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			expectRejected(t, tt.src, tt.want)
		})
	}

	accepted := []struct {
		name string
		src  string
	}{
		{
			name: "an and one inside the limit",
			src:  "constexpr long long k = 9007199254740991 & -1;\nvoid main(void) { __ic_store(d0, Setting, k); }",
		},
		{
			name: "an or one inside the limit on the negative side",
			src:  "constexpr long long k = (-9007199254740991) | 1;\nvoid main(void) { __ic_store(d0, Setting, k); }",
		},
		{
			name: "a complement one inside the limit",
			src:  "constexpr long long k = ~9007199254740991;\nvoid main(void) { __ic_store(d0, Setting, k); }",
		},
		{
			name: "a right shift one inside the limit",
			src:  "constexpr long long k = 9007199254740991 >> 1;\nvoid main(void) { __ic_store(d0, Setting, k); }",
		},
		{
			name: "a left shift landing one inside the limit",
			src:  "constexpr long long k = (long long)1 << 52;\nvoid main(void) { __ic_store(d0, Setting, k); }",
		},
		{
			// The limit an operand stops one short of is still a constant the
			// machine holds, which is the whole reason there are two of them.
			name: "the limit itself as a plain constant",
			src:  "constexpr long long k = 9007199254740992;\nvoid main(void) { __ic_store(d0, Setting, k); }",
		},
		{
			name: "the limit itself negated as a plain constant",
			src:  "constexpr long long k = -9007199254740992;\nvoid main(void) { __ic_store(d0, Setting, k); }",
		},
	}
	for _, tt := range accepted {
		t.Run(tt.name, func(t *testing.T) {
			expectAccepted(t, tt.src)
		})
	}
}

// TestOperandRulesHoldWhereTheOperatorDoesNotFold covers the bitwise and shift
// operator one of whose operands the program computes.
//
// Every rule these operators carry rests on one operand: the bitwise window on
// the value handed to the instruction, and the shift bound on the count. The
// fold applies them only once every operand is constant, so an operand the
// program computes used to carry the other past its rule entirely — the same
// constant refused in 'k >> 1' reached the instruction in 'k >> n', where the
// machine reduces it modulo 2^53 and answers zero.
//
// Each row is one rule written in every spelling that reaches the instruction,
// and the spellings must agree: what an operand denotes does not depend on what
// stands beside it or on how the operator is written. A compound assignment is
// the spelling that folds least of all, since its left operand is an object no
// fold ever answers for, and a table of binary expressions alone is what let
// '<<=' past every rule here.
func TestOperandRulesHoldWhereTheOperatorDoesNotFold(t *testing.T) {
	tests := []struct {
		name string
		// spellings each reach the same instruction and must each be refused
		// for the same reason.
		spellings []string
		want      string
	}{
		{
			name: "an and whose constant operand is at the limit",
			spellings: []string{
				"__ic_store(d0, Setting, /*!*/9007199254740992 & 1);",
				"__ic_store(d0, Setting, /*!*/9007199254740992 & g);",
				"g &= /*!*/9007199254740992;",
				"g &= /*!*/9007199254740991 + 1;",
			},
			want: "modulo 2^53",
		},
		{
			name: "an or whose constant operand is at the negative limit",
			spellings: []string{
				"__ic_store(d0, Setting, 1 | (/*!*/-9007199254740992));",
				"__ic_store(d0, Setting, g | (/*!*/-9007199254740992));",
				"g |= /*!*/-9007199254740992;",
				"g |= /*!*/-(9007199254740991 + 1);",
			},
			want: "modulo 2^53",
		},
		{
			// The last spelling names the value rather than writing it, which
			// is what the fold answers for and what the machine still reduces.
			name: "an xor whose constant operand is at the limit",
			spellings: []string{
				"__ic_store(d0, Setting, 1 ^ /*!*/9007199254740992);",
				"__ic_store(d0, Setting, g ^ /*!*/9007199254740992);",
				"g ^= /*!*/9007199254740992;",
				"g ^= /*!*/k;",
			},
			want: "modulo 2^53",
		},
		{
			// The shifted operand, which '<<=' and '>>=' cannot spell: theirs
			// is the target, and an object carries no constant to hold.
			name: "a shifted operand at the limit",
			spellings: []string{
				"__ic_store(d0, Setting, /*!*/9007199254740992 >> 1);",
				"__ic_store(d0, Setting, /*!*/9007199254740992 >> g);",
				"__ic_store(d0, Setting, /*!*/9007199254740992 << g);",
				"__ic_store(d0, Setting, /*!*/k >> 1);",
				"__ic_store(d0, Setting, /*!*/k >> g);",
			},
			want: "modulo 2^53",
		},
		{
			// The count, whose bound is the width of the left operand's type
			// rather than the bitwise window.
			name: "a count past the width C gives the left operand",
			spellings: []string{
				"__ic_store(d0, Setting, (long long)1 /*!*/<< 64);",
				"__ic_store(d0, Setting, g /*!*/<< 64);",
				"g /*!*/<<= 64;",
				"g /*!*/>>= 99;",
				"g /*!*/<<= 63 + 1;",
			},
			want: "a shift count must be between 0 and 63",
		},
		{
			name: "a negative count",
			spellings: []string{
				"__ic_store(d0, Setting, (long long)1 /*!*/<< -1);",
				"__ic_store(d0, Setting, g /*!*/<< -1);",
				"g /*!*/<<= -1;",
				"g /*!*/>>= -1;",
			},
			want: "a shift count must be between 0 and 63",
		},
	}
	const program = `constexpr long long k = 9007199254740992;
long long g;
void main(void) {
    %s
}
`
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.spellings) < 2 {
				t.Fatalf("a rule written one way settles nothing about the others, got %d spellings", len(tt.spellings))
			}
			for _, spelling := range tt.spellings {
				t.Run(spelling, func(t *testing.T) {
					expectRejected(t, fmt.Sprintf(program, spelling), tt.want)
				})
			}
		})
	}
}

// TestOperandsInsideTheLimitStandBesideAComputedOperand pins what the rule above
// must not cost: the operand a bitwise or shift instruction does read back
// unchanged is admitted however the operator is written.
func TestOperandsInsideTheLimitStandBesideAComputedOperand(t *testing.T) {
	tests := []struct {
		name  string
		stmts []string
	}{
		{
			name: "an operand one inside the limit",
			stmts: []string{
				"__ic_store(d0, Setting, 9007199254740991 & g);",
				"__ic_store(d0, Setting, (-9007199254740991) | g);",
				"g &= 9007199254740991;",
				"g |= -9007199254740991;",
				"g ^= 9007199254740991;",
			},
		},
		{
			name: "a shifted operand one inside the limit",
			stmts: []string{
				"__ic_store(d0, Setting, 9007199254740991 >> g);",
				"__ic_store(d0, Setting, 9007199254740991 >> 1);",
			},
		},
		{
			name: "the widest count the left operand's type takes",
			stmts: []string{
				"__ic_store(d0, Setting, g << 63);",
				"g <<= 63;",
				"g >>= 63;",
				"g <<= 31 + 32;",
			},
		},
		{
			name: "a count of zero",
			stmts: []string{
				"__ic_store(d0, Setting, g >> 0);",
				"g <<= 0;",
				"g >>= 0;",
			},
		},
		{
			name: "two operands the program computes",
			stmts: []string{
				"__ic_store(d0, Setting, g ^ g);",
				"__ic_store(d0, Setting, ~g);",
				"g ^= g;",
				"g <<= g;",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, stmt := range tt.stmts {
				t.Run(stmt, func(t *testing.T) {
					expectAccepted(t, fmt.Sprintf("long long g;\nvoid main(void) {\n    g = 1;\n    %s\n}\n", stmt))
				})
			}
		})
	}
}

// TestTheLeftShiftResultMessageNamesTheResult holds the one operand message
// that named its limit without ever saying what was found.
//
// The result can be wider than a long long holds, which is why it is not
// reported the way its siblings report an int64.
func TestTheLeftShiftResultMessageNamesTheResult(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a shift landing exactly on the limit",
			src: `constexpr long long k = (long long)1 /*!*/<< 53;
void main(void) { __ic_store(d0, Setting, k); }
`,
			want: "the result of '<<' is 9007199254740992, which is past 9007199254740991, and the machine reads a shift result " +
				"back out of 53 bits and a sign taken from the next, so a left shift that reaches 2^53 answers with -2^53",
		},
		{
			name: "a shift whose result no long long holds",
			src: `constexpr long long k = (long long)9007199254740991 /*!*/<< 63;
void main(void) { __ic_store(d0, Setting, k); }
`,
			want: "the result of '<<' is 83076749736557232833115904412745728, which is past 9007199254740991, and the machine " +
				"reads a shift result back out of 53 bits and a sign taken from the next, so a left shift that reaches 2^53 answers with -2^53",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejectedWith(t, tt.src, tt.want)
		})
	}
}

// TestRuntimeShiftCountIsHeldToTheLeftOperandsWidth covers the shift whose
// count only the C type of its left operand can bound.
//
// constShift bounds a constant count by that width, so 1 << 31 is refused. The
// same expression with a count the program computes reaches no fold at all, and
// the width is still what decides: C shifts a bare literal in 32 bits and this
// machine shifts in 64, so the two answer differently for every count of 32 or
// more. The left operand's type is known whether or not the count is, which is
// what lets the same rule reject the one and admit the other.
func TestRuntimeShiftCountIsHeldToTheLeftOperandsWidth(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a left shift of a bare literal by a runtime count",
			src: `long long g;
void main(void) {
    __ic_store(d0, Setting, 1 /*!*/<< g);
}
`,
			want: "cast the left operand to long long",
		},
		{
			name: "a right shift of a bare literal by a runtime count",
			src: `long long g;
void main(void) {
    __ic_store(d0, Setting, 256 /*!*/>> g);
}
`,
			want: "cast the left operand to long long",
		},
		{
			name: "a left operand C computes in 32 bits through an operator",
			src: `long long g;
void main(void) {
    __ic_store(d0, Setting, (1 + 2) /*!*/<< g);
}
`,
			want: "'int'",
		},
		{
			name: "a runtime count under a hexadecimal literal C gives unsigned int",
			src: `long long g;
void main(void) {
    __ic_store(d0, Setting, 0x80000000 /*!*/<< g);
}
`,
			want: "'unsigned int'",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejected(t, tt.src, tt.want)
		})
	}
}

// TestConstantFoldsThatStayRepresentable pins what folding must not reject: the
// arithmetic a program actually writes, and the expressions that are not
// constant at all.
func TestConstantFoldsThatStayRepresentable(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "a shift that stays inside the width the machine holds",
			src:  "void main(void) { long long x = (long long)1 << 52; __ic_store(d0, Setting, x); }",
		},
		{
			name: "a product that stays inside the range the machine holds",
			src:  "void main(void) { long long x = 3600 * 1000; __ic_store(d0, Setting, x); }",
		},
		{
			name: "a division by a variable that happens to be zero",
			src:  "long long g;\nvoid main(void) { long long x = 1 / (g + 1); __ic_store(d0, Setting, x); }",
		},
		{
			name: "a double division by zero, which answers with an infinity",
			src:  "void main(void) { double d = 1.0 / 0.0; __ic_store(d0, Setting, d); }",
		},
		{
			name: "a shift the program computes at runtime, in the machine's own width",
			src:  "long long g;\nvoid main(void) { long long x = g << g; __ic_store(d0, Setting, x); }",
		},
		{
			name: "a runtime count under a left operand the source widened",
			src:  "long long g;\nvoid main(void) { long long x = (long long)1 << g; __ic_store(d0, Setting, x); }",
		},
		{
			name: "a constant count under a narrow left operand, which the width bounds",
			src:  "void main(void) { long long x = 1 << 4; __ic_store(d0, Setting, x); }",
		},
		{
			name: "the largest integer literal the machine holds exactly",
			src:  "void main(void) { long long x = 9007199254740992; __ic_store(d0, Setting, x); }",
		},
		{
			name: "the most negative integer literal the machine holds exactly",
			src:  "void main(void) { long long x = -9007199254740992; __ic_store(d0, Setting, x); }",
		},
		{
			name: "an integer literal past what a C long long holds",
			src:  "constexpr long long k = 3000000000;\nvoid main(void) { __ic_store(d0, Setting, k); }",
		},
		{
			name: "a device hash written as the literal it folds to",
			src:  "void main(void) { __ic_store_batch(261668164, On, 1); }",
		},
		{
			// The target is an object read through a pointer, which the C type
			// model does not describe. A shape it does not describe restricts
			// nothing, so the count is bounded by the machine's own width and a
			// count of 40 stands.
			name: "a compound shift of an object reached through a pointer",
			src: `void main(void) {
    long long v = 1;
    long long *p = &v;
    *p <<= 40;
    __ic_store(d0, Setting, v);
}
`,
		},
		{
			// The same shape under the rule that bounds a count the program
			// computes, which reaches the model by a different path and has to
			// leave an unmodelled left operand alone for the same reason.
			name: "a runtime count under a left operand reached through a pointer",
			src: `long long g;
void main(void) {
    long long v = 1;
    long long *p = &v;
    __ic_store(d0, Setting, *p << g);
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectAccepted(t, tt.src)
		})
	}
}

// TestShiftCountNamesTheCastThatWidensIt pins the remedy on the count bound.
//
// The bound is the width of the C type of the left operand, and where that type
// is the narrow one it is a type MicroC cannot spell: a declaration taking int
// is refused outright, so a reader told the count is past the width of an int
// has nothing to change. The cast is the move, and it is the one the two sibling
// divergences already name.
//
// It stands only where the left operand is narrower than the machine. A count of
// 64 is past a long long as well, and telling a program that already wrote the
// cast to write it again would be advice that does nothing.
func TestShiftCountNamesTheCastThatWidensIt(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a count the width of an int does not admit",
			src: `void main(void) {
    __ic_store(d0, Setting, 1 /*!*/<< 32);
}
`,
			want: "a shift count must be between 0 and 31, the width of 'int' that C gives the left operand, found 32; " +
				"cast the left operand to long long so that C widens the shift too",
		},
		{
			name: "a count past every width, under a left operand C narrows",
			src: `void main(void) {
    __ic_store(d0, Setting, 1 /*!*/<< 64);
}
`,
			want: "a shift count must be between 0 and 31, the width of 'int' that C gives the left operand, found 64; " +
				"cast the left operand to long long so that C widens the shift too",
		},
		{
			name: "a negative count under a left operand C narrows",
			src: `void main(void) {
    __ic_store(d0, Setting, 1 /*!*/<< (-1));
}
`,
			want: "a shift count must be between 0 and 31, the width of 'int' that C gives the left operand, found -1; " +
				"cast the left operand to long long so that C widens the shift too",
		},
		{
			name: "a count past the width of a long long, which no cast widens",
			src: `void main(void) {
    __ic_store(d0, Setting, (long long)1 /*!*/<< 64);
}
`,
			want: "a shift count must be between 0 and 63, the width of 'long long' that C gives the left operand, found 64",
		},
		{
			name: "a count past the width of a left operand the program computes",
			src: `long long g;
void main(void) {
    g = 1;
    __ic_store(d0, Setting, g /*!*/<< 64);
}
`,
			want: "a shift count must be between 0 and 63, the width of 'long long' that C gives the left operand, found 64",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejectedWith(t, tt.src, tt.want)
		})
	}
}

// TestUnfoldedShiftCountCarriesTheSameRemedy covers the count check reached from
// the path that folds nothing.
//
// A left operand C narrows is a constant expression — every value the program
// computes is a long long — so the shift reaches this path only where that
// constant has a problem of its own. The count is still bounded by the width,
// and the message it carries is the one the folded path renders.
func TestUnfoldedShiftCountCarriesTheSameRemedy(t *testing.T) {
	const src = `void main(void) {
    __ic_store(d0, Setting, (2147483647 + 1) /*!*/<< 40);
}
`
	const want = "a shift count must be between 0 and 31, the width of 'int' that C gives the left operand, found 40; " +
		"cast the left operand to long long so that C widens the shift too"

	expectDiagnosticAt(t, src, want)
}
