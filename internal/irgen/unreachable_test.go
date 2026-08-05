package irgen

import (
	"testing"
)

// TestStatementsBelowATerminatorLowerCleanly covers the source C admits and this
// stage has no block left to write: anything below a break, a continue or a
// return. That block already ends in the branch the construct above it emitted,
// and an instruction written past a terminator fails [Generate]'s own verify.
func TestStatementsBelowATerminatorLowerCleanly(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "below a break in a for body",
			src:  "long long g; void main(void) { for (long long i = 0; i < 7; i++) { break; g = i; } }",
		},
		{
			name: "below a continue in a for body",
			src:  "long long g; void main(void) { for (long long i = 0; i < 7; i++) { continue; g = i; } }",
		},
		{
			name: "below a break in a while body",
			src:  "long long g; void main(void) { while (g < 7) { break; g = 1; } }",
		},
		{
			name: "below a break in a do body",
			src:  "long long g; void main(void) { do { break; g = 1; } while (g < 7); }",
		},
		{
			name: "below a return at the top of a function",
			src:  "long long g; void main(void) { return; g = 1; }",
		},
		{
			name: "below a return in a value-returning function",
			src:  "long long f(void) { return 1; return 2; } long long g; void main(void) { g = f(); }",
		},
		{
			name: "below a break in a switch arm",
			src:  "long long g; void main(void) { switch (g) { case 0: break; g = 1; default: g = 2; } }",
		},
		{
			name: "below a return in a switch arm",
			src:  "long long f(long long n) { switch (n) { case 0: return 1; n = 2; default: return 3; } } long long g; void main(void) { g = f(g); }",
		},
		{
			name: "below a bare compound statement that returns",
			src:  "long long g; void main(void) { { { return; } } g = 1; }",
		},
		{
			name: "below a break nested two loops deep",
			src:  "long long g; void main(void) { while (g < 7) { for (long long i = 0; i < 3; i++) { break; g = i; } g++; } }",
		},
		{
			name: "a declaration below a return, which would otherwise need storage",
			src:  "long long g; void main(void) { return; long long a[4]; a[0] = 1; g = a[0]; }",
		},
		{
			name: "a whole construct below a return",
			src:  "long long g; void main(void) { return; if (g > 0) { while (g < 7) { g++; } } }",
		},
		{
			name: "below a return in the else arm",
			src:  "long long g; void main(void) { if (g > 0) { g = 1; } else { return; g = 2; } }",
		},
		{
			name: "below a continue that leaves a switch",
			src:  "long long g; void main(void) { while (g < 7) { switch (g) { case 0: continue; g = 1; default: g = 2; } } }",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Generate(t.Context(), analyze(t, tc.src), Options{})
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			result.Dispose()
		})
	}
}
