package sema_test

import "testing"

// TestConstexprNamesAConstant covers the positions the language requires a
// constant expression in. A constexpr object is the only way MicroC names one,
// so each position is checked against every scalar type rather than against the
// one a defect was found in.
func TestConstexprNamesAConstant(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "a long long in an array bound",
			src:  "constexpr long long kWindow = 8;\ndouble samples[kWindow];\nvoid main(void) { __ic_store(d0, Setting, samples[0]); }",
		},
		{
			name: "a long long in a case label",
			src: `constexpr long long kIdle = 0;
void main(void) {
    switch ((long long)__ic_load(d0, Setting)) {
    case kIdle:
        break;
    default:
        break;
    }
}
`,
		},
		{
			name: "a bool in a case label",
			src: `constexpr bool kOn = true;
void main(void) {
    switch (__ic_device_present(d0)) {
    case kOn:
        break;
    default:
        break;
    }
}
`,
		},
		{
			name: "a double in a global initializer",
			src:  "constexpr double kHalf = 0.5;\ndouble scaled = kHalf * 4.0;\nvoid main(void) { __ic_store(d0, Setting, scaled); }",
		},
		{
			name: "an array of them",
			src:  "constexpr long long kDecades[4] = {1, 10, 100, 1000};\nvoid main(void) { __ic_store(d0, Setting, kDecades[2]); }",
		},
		{
			name: "a local naming an array bound",
			src:  "void main(void) { constexpr long long k = 3; long long a[k]; a[2] = 1; __ic_store(d0, Setting, a[2]); }",
		},
		{
			name: "a dev object",
			src:  "constexpr dev sensor = d0;\nvoid main(void) { __ic_store(sensor, On, 1); }",
		},
		{
			name: "const written alongside it",
			src:  "const constexpr long long k = 4;\nlong long a[k];\nvoid main(void) { __ic_store(d0, Setting, a[3]); }",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectAccepted(t, tt.src)
		})
	}
}

// TestConstIsNotAConstantExpression pins the rule constexpr exists for. C reads
// a const object's value at run time, so a program naming one where a constant
// is required would mean something different there than it means here.
func TestConstIsNotAConstantExpression(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "in an array bound",
			src: `const long long kWindow = 8;
double samples[/*!*/kWindow];
void main(void) { __ic_store(d0, Setting, samples[0]); }
`,
			want: "'kWindow' is not a constexpr object",
		},
		{
			name: "in a case label",
			src: `const long long kIdle = 0;
void main(void) {
    switch ((long long)__ic_load(d0, Setting)) {
    case /*!*/kIdle:
        break;
    default:
        break;
    }
}
`,
			want: "'kIdle' is not a constexpr object",
		},
		{
			name: "in a global initializer",
			src: `const long long kBits = 8;
long long mask = /*!*/kBits;
void main(void) { __ic_store(d0, Setting, mask); }
`,
			want: "'kBits' is not a constexpr object",
		},
		{
			name: "in a brace initializer element",
			src: `const long long kOne = 1;
void main(void) {
    long long a[2] = {/*!*/kOne, 0};
    __ic_store(d0, Setting, a[0]);
}
`,
			want: "'kOne' is not a constexpr object",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejected(t, tt.src, tt.want)
		})
	}
}

// TestConstexprRequiresAConstantInitializer covers what the specifier promises.
// A constexpr object whose initializer is not constant is rejected wherever it
// is declared, which is what keeps the language from admitting a declaration C
// refuses.
func TestConstexprRequiresAConstantInitializer(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a local initialized by an intrinsic",
			src: `void main(void) {
    constexpr double k = /*!*/__ic_load(d0, Setting);
    __ic_store(d1, Setting, k);
}
`,
			want: "the initializer of a constexpr object must be a constant expression",
		},
		{
			name: "a local initialized by another local",
			src: `void main(void) {
    long long n = 1;
    constexpr long long k = /*!*/n;
    __ic_store(d0, Setting, k);
}
`,
			want: "the initializer of a constexpr object must be a constant expression",
		},
		{
			name: "no initializer at all",
			src: `constexpr long long /*!*/k;
void main(void) { __ic_store(d0, Setting, k); }
`,
			want: "the constexpr object 'k' requires an initializer",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejected(t, tt.src, tt.want)
		})
	}
}

// TestAConstexprWithoutAValueIsNamedWhereItIsUsed covers what such a
// declaration costs beyond its own diagnostic.
//
// The declaration is rejected where it stands, and the name it introduces stays
// in scope. A position that requires a constant expression has to say why this
// one carries none, and neither thing a reader would otherwise be told is true:
// the object is constexpr, and it holds no value to fold.
func TestAConstexprWithoutAValueIsNamedWhereItIsUsed(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "in an array bound",
			src: `long long g;
constexpr long long k = g;
void main(void) {
    long long a[/*!*/k];
    a[0] = 1;
    __ic_store(d0, Setting, a[0]);
}
`,
			want: "an array bound must be a constant expression: 'k' is constexpr but its initializer is not constant",
		},
		{
			name: "in a case label",
			src: `long long g;
constexpr long long k = g;
void main(void) {
    switch (g) {
    case /*!*/k:
        __ic_store(d0, On, 1);
        break;
    }
}
`,
			want: "a case label must be a constant expression: 'k' is constexpr but its initializer is not constant",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectDiagnosticAt(t, tt.src, tt.want)
		})
	}
}

// TestIntegerConstantExpressionRefusesADouble covers the one shape C admits a
// double into a constant expression of integer type: a floating literal that a
// cast converts. Anything else computes the bound or the label from a value the
// integer expression may not hold, which is what makes such a program invalid C
// however readily it folds.
//
// Each of the three positions that require one is checked, since the rule is a
// property of the position rather than of the operand written in it.
func TestIntegerConstantExpressionRefusesADouble(t *testing.T) {
	const want = "a constant expression of integer type cannot compute with a double value"
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "arithmetic on a constexpr double in an array bound",
			src: `constexpr double kHalf = 0.5;
long long a[(long long)(/*!*/kHalf * 8.0)];
void main(void) { __ic_store(d0, Setting, a[3]); }
`,
		},
		{
			name: "a constexpr double cast in an array bound",
			src: `constexpr double kThree = 3.5;
long long a[(long long)/*!*/kThree];
void main(void) { __ic_store(d0, Setting, a[2]); }
`,
		},
		{
			name: "arithmetic on floating literals in an array bound",
			src: `long long a[(long long)(/*!*/3.5 * 2.0)];
void main(void) { __ic_store(d0, Setting, a[6]); }
`,
		},
		{
			name: "a cast that is not the literal's own in an array bound",
			src: `long long a[(long long)/*!*/(double)7.9];
void main(void) { __ic_store(d0, Setting, a[6]); }
`,
		},
		{
			name: "arithmetic on a constexpr double in a case label",
			src: `constexpr double kHalf = 0.5;
void main(void) {
    switch ((long long)__ic_load(d0, Setting)) {
    case (long long)(/*!*/kHalf * 8.0):
        break;
    default:
        break;
    }
}
`,
		},
		{
			name: "a constexpr double cast in a case label",
			src: `constexpr double kThree = 3.5;
void main(void) {
    switch ((long long)__ic_load(d0, Setting)) {
    case (long long)/*!*/kThree:
        break;
    default:
        break;
    }
}
`,
		},
		{
			name: "arithmetic in the initializer of a constexpr long long",
			src: `constexpr double kHalf = 0.5;
constexpr long long n = (long long)(/*!*/kHalf * 2.0);
void main(void) { __ic_store(d0, Setting, n); }
`,
		},
		{
			name: "arithmetic in the initializer of a constexpr bool",
			src: `constexpr double kHalf = 0.5;
constexpr bool b = (bool)(/*!*/kHalf * 2.0);
void main(void) { __ic_store(d0, Setting, b); }
`,
		},
		{
			name: "arithmetic in the brace initializer of a constexpr long long",
			src: `constexpr double kHalf = 0.5;
constexpr long long n = {(long long)(/*!*/kHalf * 2.0)};
void main(void) { __ic_store(d0, Setting, n); }
`,
		},
		{
			name: "arithmetic in an element of a constexpr long long array",
			src: `constexpr double kHalf = 0.5;
constexpr long long ns[2] = {(long long)(/*!*/kHalf * 2.0), 1};
void main(void) { __ic_store(d0, Setting, ns[1]); }
`,
		},
		{
			name: "arithmetic in the slot index of a slot intrinsic",
			src: `void main(void) {
    __ic_store_slot(d0, (long long)(/*!*/1.5 * 2.0), Occupied, 1);
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejected(t, tt.src, want)
		})
	}
}

// TestIntegerConstantExpressionAdmitsALiteralCast covers what the restriction
// above must leave alone: the cast C permits, and every position that takes an
// arithmetic constant expression, where a double computes freely. A constexpr
// double stays declarable and stays usable as an ordinary value.
func TestIntegerConstantExpressionAdmitsALiteralCast(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "a literal cast in an array bound",
			src:  "long long a[(long long)3.5];\nvoid main(void) { __ic_store(d0, Setting, a[2]); }",
		},
		{
			name: "two literal casts combined in an array bound",
			src:  "long long a[(long long)3.5 + (long long)2.5];\nvoid main(void) { __ic_store(d0, Setting, a[4]); }",
		},
		{
			name: "a literal cast in a case label",
			src: `void main(void) {
    switch ((long long)__ic_load(d0, Setting)) {
    case (long long)3.5:
        break;
    default:
        break;
    }
}
`,
		},
		{
			name: "a literal cast in the initializer of a constexpr long long",
			src:  "constexpr long long n = (long long)9.9;\nvoid main(void) { __ic_store(d0, Setting, n); }",
		},
		{
			name: "a literal cast in the slot index of a slot intrinsic",
			src:  "void main(void) { __ic_store_slot(d0, (long long)3.5, Occupied, 1); }",
		},
		{
			name: "arithmetic on a constexpr double in a global initializer",
			src:  "constexpr double kHalf = 0.5;\nlong long scaled = (long long)(kHalf * 8.0);\nvoid main(void) { __ic_store(d0, Setting, scaled); }",
		},
		{
			name: "arithmetic on a constexpr double in a global brace element",
			src:  "constexpr double kHalf = 0.5;\nlong long a[2] = {(long long)(kHalf * 2.0), 1};\nvoid main(void) { __ic_store(d0, Setting, a[0]); }",
		},
		{
			name: "a constexpr double naming another constexpr double",
			src:  "constexpr double kHalf = 0.5;\nconstexpr double kQuarter = kHalf / 2.0;\nvoid main(void) { __ic_store(d0, Setting, kQuarter); }",
		},
		{
			name: "a constexpr double used as a run-time value",
			src:  "constexpr double kHalf = 0.5;\nvoid main(void) { __ic_store(d0, Setting, kHalf * __ic_load(d1, Setting)); }",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectAccepted(t, tt.src)
		})
	}
}
