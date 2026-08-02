package sema_test

import "testing"

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
			want: "needs more than 53 significant bits",
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
			want: "needs more than 53 significant bits",
		},
		{
			name: "a shift past the width the machine holds, under an operand that is not constant",
			src: `long long g;
void main(void) {
    long long x = g + ((long long)1 /*!*/<< 60);
    __ic_store(d0, Setting, x);
}
`,
			want: "needs more than 53 significant bits",
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
			name: "a sum past the range the machine holds",
			src: `void main(void) {
    long long x = 9007199254740992 /*!*/+ 8;
    __ic_store(d0, Setting, x);
}
`,
			want: "needs more than 53 significant bits",
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
			want: "needs more than 53 significant bits",
		},
		{
			name: "a difference past the range the machine holds",
			src: `void main(void) {
    long long x = -9007199254740992 /*!*/- 8;
    __ic_store(d0, Setting, x);
}
`,
			want: "needs more than 53 significant bits",
		},
		{
			name: "an integer literal past the range the machine holds",
			src: `void main(void) {
    long long x = /*!*/9007199254740993;
    __ic_store(d0, Setting, x);
}
`,
			want: "needs more than 53 significant bits",
		},
		{
			name: "an integer literal in a global initializer",
			src: `long long g = /*!*/9007199254740993;
void main(void) { __ic_store(d0, Setting, g); }
`,
			want: "needs more than 53 significant bits",
		},
		{
			name: "an integer literal in a constexpr initializer",
			src: `constexpr long long k = /*!*/9007199254740993;
void main(void) { __ic_store(d0, Setting, k); }
`,
			want: "needs more than 53 significant bits",
		},
		{
			name: "an integer literal in a case label",
			src: `void main(void) {
    switch ((long long)__ic_load(d0, Setting)) {
    case /*!*/9007199254740993: __ic_store(d1, On, 1); break;
    }
}
`,
			want: "needs more than 53 significant bits",
		},
		{
			name: "an integer literal in an array bound",
			src: `void main(void) {
    long long a[/*!*/9007199254740993];
    a[0] = 1;
    __ic_store(d0, Setting, a[0]);
}
`,
			want: "needs more than 53 significant bits",
		},
		{
			name: "an integer literal as an intrinsic argument",
			src: `void main(void) {
    __ic_store(d0, Setting, /*!*/9007199254740993);
}
`,
			want: "needs more than 53 significant bits",
		},
		{
			name: "a cast that truncates a double past the range the machine holds",
			src: `void main(void) {
    long long x = /*!*/(long long)1e18;
    __ic_store(d0, Setting, x);
}
`,
			want: "needs more than 53 significant bits",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejected(t, tt.src, tt.want)
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
			src:  "void main(void) { __ic_store_batch(-739292323, On, 1); }",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectAccepted(t, tt.src)
		})
	}
}
