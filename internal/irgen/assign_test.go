package irgen

import (
	"strings"
	"testing"
)

// TestCompoundAssignmentShapes covers the operator a compound assignment
// applies to the target's own value, which the plain form never reads.
func TestCompoundAssignmentShapes(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
		// absent are substrings whose presence would mean the shape is wrong,
		// not merely different.
		absent []string
	}{
		{
			name:   "an integer target truncates its division and builds its remainder",
			src:    "long long g; void main(void) { g += 2; g -= 1; g *= 3; g /= 2; g %= 5; }",
			want:   []string{"fadd double", "fsub double", "fmul double", "fdiv double", "call double @llvm.trunc.f64"},
			absent: []string{"sdiv", "srem"},
		},
		{
			name: "an integer target reaches the bitwise instructions through a call the optimizer may not move",
			src:  "long long g; void main(void) { g &= 6; g |= 1; g ^= 3; g <<= 2; g >>= 1; }",
			want: []string{
				"call double @__ic_and", "call double @__ic_or", "call double @__ic_xor",
				"call double @__ic_shl", "call double @__ic_shr",
			},
			absent: []string{"and i64", "or i64", "xor i64", "shl i64", "ashr i64", "speculatable"},
		},
		{
			name:   "a double target takes the float instruction",
			src:    "double g; void main(void) { g += 2.0; g -= 1.0; g *= 3.0; g /= 2.0; }",
			want:   []string{"fadd double", "fsub double", "fmul double", "fdiv double"},
			absent: []string{"llvm.trunc"},
		},
		{
			name: "a pointer target steps by a count of elements",
			src: `long long a[4];
void main(void) {
    long long *p = &a[0];
    p += 2;
    p -= 1;
    __ic_store(d0, Setting, *p);
}`,
			want: []string{"getelementptr inbounds double, ptr %0, i64 2", "i64 -1"},
		},
		{
			name: "the value a compound assignment answers with is the one it stored",
			src:  "long long g; void main(void) { long long n = (g += 2); __ic_store(d0, Setting, n); }",
			want: []string{"fadd double"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := generate(t, tc.src)
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("the module holds no %q:\n%s", want, text)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(text, absent) {
					t.Errorf("the module holds %q, which this shape must not need:\n%s", absent, text)
				}
			}
		})
	}
}

// TestTruthTestShapes covers the two comparisons a value is reduced to where a
// condition wants a truth value. A double is tested unordered, which is what the
// machine's own test against zero answers.
func TestTruthTestShapes(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "an integer condition compares against zero",
			src:  "long long g; void main(void) { if (g) { __ic_store(d0, On, 1); } }",
			want: []string{"fcmp une double"},
		},
		{
			// The machine has an instruction for each comparison and no one-bit not.
			name: "a negation takes the equality directly rather than complementing",
			src:  "long long g; long long h; void main(void) { h = !g; }",
			want: []string{"fcmp une double", "fcmp oeq double"},
		},
		{
			name: "a double narrowed to a truth value is unordered, so a NaN is true",
			src:  "double g; long long h; void main(void) { h = (bool)g; }",
			want: []string{"fcmp une double", "uitofp i1"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := generate(t, tc.src)
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("the module holds no %q:\n%s", want, text)
				}
			}
		})
	}
}
