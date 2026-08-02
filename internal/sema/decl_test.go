package sema_test

import "testing"

// TestBraceInitializerAccepts covers every type a brace initializer can
// initialize.
//
// The scalar rule is stated once per type kind, so a kind the check does not
// name is a kind that takes any number of values, checks none of them against
// the target's type, and requires none of them to be constant. The coverage
// here is per kind rather than per defect for that reason.
func TestBraceInitializerAccepts(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "a long long scalar",
			src:  "void main(void) { long long x = {5}; __ic_store(d0, Setting, x); }",
		},
		{
			name: "a bool scalar",
			src:  "void main(void) { bool b = {true}; __ic_store(d0, Setting, b); }",
		},
		{
			name: "a double scalar",
			src:  "void main(void) { double d = {1.5}; __ic_store(d0, Setting, d); }",
		},
		{
			name: "a double scalar taking a long long, which widens",
			src:  "void main(void) { double d = {1}; __ic_store(d0, Setting, d); }",
		},
		{
			name: "a long long scalar taking a bool",
			src:  "void main(void) { long long x = {true}; __ic_store(d0, Setting, x); }",
		},
		{
			name: "a long long array supplied in full",
			src:  "void main(void) { long long a[2] = {1, 2}; __ic_store(d0, Setting, a[1]); }",
		},
		{
			name: "a bool array supplied in part",
			src:  "void main(void) { bool a[3] = {true}; __ic_store(d0, Setting, a[2]); }",
		},
		{
			name: "a double array supplied with nothing",
			src:  "void main(void) { double a[2] = {}; __ic_store(d0, Setting, a[0]); }",
		},
		{
			name: "a trailing comma",
			src:  "void main(void) { long long a[2] = {1, 2,}; __ic_store(d0, Setting, a[0]); }",
		},
		{
			name: "a global double scalar",
			src:  "double g = {2.5};\nvoid main(void) { __ic_store(d0, Setting, g); }",
		},
		{
			name: "a constexpr long long scalar naming a constant",
			src:  "constexpr long long k = {5};\nlong long a[k];\nvoid main(void) { __ic_store(d0, Setting, a[4]); }",
		},
		{
			name: "a constexpr double scalar naming a constant",
			src:  "constexpr double kHalf = {0.5};\ndouble scaled = kHalf * 8.0;\nvoid main(void) { __ic_store(d0, Setting, scaled); }",
		},
		{
			name: "a constexpr bool scalar naming a case label",
			src: `constexpr bool kOn = {true};
void main(void) {
    bool b = false;
    switch (b) {
    case kOn:
        __ic_yield();
        break;
    default:
        break;
    }
}
`,
		},
		{
			name: "a local constexpr scalar naming an array bound",
			src:  "void main(void) { constexpr long long k = {3}; long long a[k]; __ic_store(d0, Setting, a[2]); }",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectAccepted(t, tt.src)
		})
	}
}

// TestBraceInitializerRejects covers what a brace initializer refuses, again for
// every type kind rather than only the ones a defect was found in.
func TestBraceInitializerRejects(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a long long scalar given two values",
			src: `void main(void) {
    long long x = /*!*/{1, 2};
    __ic_store(d0, Setting, x);
}
`,
			want: "must supply exactly one value, found 2",
		},
		{
			name: "a long long scalar given nothing",
			src: `void main(void) {
    long long x = /*!*/{};
    __ic_store(d0, Setting, x);
}
`,
			want: "must supply exactly one value, found 0",
		},
		{
			name: "a bool scalar given two values",
			src: `void main(void) {
    bool b = /*!*/{true, false};
    __ic_store(d0, Setting, b);
}
`,
			want: "must supply exactly one value, found 2",
		},
		{
			name: "a bool scalar given nothing",
			src: `void main(void) {
    bool b = /*!*/{};
    __ic_store(d0, Setting, b);
}
`,
			want: "must supply exactly one value, found 0",
		},
		{
			name: "a double scalar given three values",
			src: `void main(void) {
    double d = /*!*/{1.0, 2.0, 3.0};
    __ic_store(d0, Setting, d);
}
`,
			want: "must supply exactly one value, found 3",
		},
		{
			// An empty brace initializer writes nothing, and the read of a
			// register nothing wrote is what deletes the guarded store below it.
			name: "a double scalar given nothing",
			src: `void main(void) {
    double x = /*!*/{};
    if (x > 5.0) {
        __ic_store(d0, Setting, 1.0);
    }
    __ic_store(d1, Setting, 2.0);
}
`,
			want: "must supply exactly one value, found 0",
		},
		{
			name: "a pointer scalar given nothing",
			src: `long long a[4];
void main(void) {
    long long *p = /*!*/{};
    *p = 1;
    __ic_store(d0, Setting, a[0]);
}
`,
			want: "must supply exactly one value, found 0",
		},
		{
			name: "a double scalar given a pointer",
			src: `long long a[4];
void main(void) {
    long long *p = &a[0];
    double d = {/*!*/p};
    __ic_store(d0, Setting, d);
}
`,
			want: "cannot use long long * as double",
		},
		{
			name: "a long long scalar given a double, which would narrow",
			src: `void main(void) {
    long long x = {/*!*/1.5};
    __ic_store(d0, Setting, x);
}
`,
			want: "cannot use double as long long",
		},
		{
			name: "a pointer scalar given an address, which is not constant",
			src: `long long a[4];
void main(void) {
    long long *p = {/*!*/&a[0]};
    *p = 1;
    __ic_store(d0, Setting, a[0]);
}
`,
			want: "an element of a brace initializer must be a constant expression",
		},
		{
			name: "a double array element that is not constant",
			src: `double n;
void main(void) {
    double a[2] = {/*!*/n, 1.0};
    __ic_store(d0, Setting, a[0]);
}
`,
			want: "an element of a brace initializer must be a constant expression",
		},
		{
			name: "a bool array given more elements than its bound",
			src: `bool a[2] = {true, false, /*!*/true};
void main(void) {
    __ic_store(d0, Setting, a[0]);
}
`,
			want: "supplies 3 elements for an array of 2",
		},
		{
			name: "a dev given a brace initializer",
			src: `void main(void) {
    const dev t = /*!*/{d0};
    __ic_store(t, On, 1);
}
`,
			want: "must name a device",
		},
		{
			name: "a constexpr long long element computed from a constexpr double",
			src: `constexpr double kHalf = {0.5};
constexpr long long k = {(long long)(/*!*/kHalf * 8.0)};
void main(void) { __ic_store(d0, Setting, k); }
`,
			want: "a constant expression of integer type cannot compute with a double value",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejected(t, tt.src, tt.want)
		})
	}
}

// TestArrayBoundFitsTheDataRegion covers a bound no program can lay out.
//
// The data region is 512 slots and every element occupies one, so a bound past
// it is impossible whatever else the program declares. Refusing it in analysis
// is what keeps the bound out of the LLVM array type built for it: a type with
// billions of elements is one later passes walk, and the compiler stops
// answering.
func TestArrayBoundFitsTheDataRegion(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "a global one slot past the region",
			src: `long long a[/*!*/513];
void main(void) { a[0] = 1; __ic_store(d0, Setting, a[0]); }
`,
		},
		{
			name: "a local bound that would hang the compiler",
			src: `void main(void) {
    long long a[/*!*/9007199254740000];
    a[0] = 1;
    __ic_store(d0, Setting, a[0]);
}
`,
		},
		{
			name: "a bound a constant expression computed",
			src: `constexpr long long kRows = 64;
long long a[/*!*/kRows * 64];
void main(void) { a[0] = 1; __ic_store(d0, Setting, a[0]); }
`,
		},
		{
			name: "a parameter that wrote the bound it discards",
			src: `void f(long long a[/*!*/1024]) { a[0] = 1; }
void main(void) { long long v[2]; f(v); __ic_store(d0, Setting, v[0]); }
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejected(t, tt.src, "the whole data region")
		})
	}
}

// TestArrayBoundFillsTheDataRegion pins the largest bound analysis admits. What
// a program can actually afford is smaller, since the same slots hold every
// other object and the call stack, and instruction selection is what reports
// that.
func TestArrayBoundFillsTheDataRegion(t *testing.T) {
	expectAccepted(t, `long long a[512];
void main(void) { a[0] = 1; __ic_store(d0, Setting, a[0]); }
`)
}
