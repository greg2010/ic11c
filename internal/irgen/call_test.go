package irgen

import (
	"strings"
	"testing"
)

// TestGenerateChoosesBetweenCallAndExpansion covers the decision the byte
// budget rests on. A function spliced into its callers leaves no definition
// behind; one that reaches itself cannot be spliced and gets one.
func TestGenerateChoosesBetweenCallAndExpansion(t *testing.T) {
	cases := []struct {
		name string
		src  string
		// defined names the functions the module must hold a body for, main
		// aside. calls are call sites main must make.
		defined []string
		calls   []string
	}{
		{
			name: "a function called once is spliced in",
			src: `long long twice(long long x) { return x * 2; }
void main(void) { __ic_store(d1, Setting, twice(21)); }`,
		},
		{
			name: "a function called from several places is still spliced in",
			src: `long long twice(long long x) { return x * 2; }
void main(void) {
    long long n = (long long)__ic_load(d0, Setting);
    __ic_store(d1, Setting, twice(n) + twice(n + 1) + twice(n + 2));
}`,
		},
		{
			name: "a function that calls itself is compiled out of line",
			src: `long long gcd(long long a, long long b) {
    if (b == 0) { return a; }
    return gcd(b, a % b);
}
void main(void) { __ic_store(d1, Setting, gcd(12, 8)); }`,
			defined: []string{"gcd"},
			calls:   []string{"call double @gcd("},
		},
		{
			name: "mutual recursion puts both out of line",
			src: `bool isOdd(long long n);
bool isEven(long long n) { if (n == 0) { return true; } return isOdd(n - 1); }
bool isOdd(long long n) { if (n == 0) { return false; } return isEven(n - 1); }
void main(void) { __ic_store(d1, Setting, isEven(6)); }`,
			defined: []string{"isEven", "isOdd"},
			calls:   []string{"call double @isEven("},
		},
		{
			name: "a recursive function nothing reaches gets no definition",
			src: `long long gcd(long long a, long long b) {
    if (b == 0) { return a; }
    return gcd(b, a % b);
}
void main(void) { __ic_store(d1, Setting, 1); }`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			module := generate(t, tc.src)
			for _, name := range tc.defined {
				if !strings.Contains(module, "define internal double @"+name+"(") {
					t.Errorf("the module holds no definition of %s:\n%s", name, module)
				}
			}
			for _, call := range tc.calls {
				if !strings.Contains(module, call) {
					t.Errorf("the module has no %q:\n%s", call, module)
				}
			}
			if len(tc.defined) == 0 && strings.Count(module, "\ndefine ") != 1 {
				t.Errorf("the module holds more than main, so a call was not spliced in:\n%s", module)
			}
		})
	}
}

// TestGenerateOutOfLineFunctionsCarryNoInline keeps the choice between a call
// and an expansion this stage's. The optimizer's inliner weighs bytes on a
// conventional machine, which is not the budget that binds here.
func TestGenerateOutOfLineFunctionsCarryNoInline(t *testing.T) {
	module := generate(t, `long long gcd(long long a, long long b) {
    if (b == 0) { return a; }
    return gcd(b, a % b);
}
void main(void) { __ic_store(d1, Setting, gcd(12, 8)); }`)

	if !strings.Contains(module, "noinline") {
		t.Errorf("the out-of-line definition does not carry noinline:\n%s", module)
	}
}

// TestGenerateOutOfLineDefinitionsAreInternal covers the linkage the entry
// point and an out-of-line definition divide between them. Nothing links
// against a MicroC program, so main is the module's one external symbol and
// every caller of a definition is in front of the interprocedural passes.
func TestGenerateOutOfLineDefinitionsAreInternal(t *testing.T) {
	module := generate(t, `long long gcd(long long a, long long b) {
    if (b == 0) { return a; }
    return gcd(b, a % b);
}
void main(void) { __ic_store(d1, Setting, gcd(12, 8)); }`)

	if !strings.Contains(module, "define internal double @gcd(") {
		t.Errorf("gcd is not internal, so the module declares callers that do not exist:\n%s", module)
	}
	if !strings.Contains(module, "define void @main(") {
		t.Errorf("the entry point is not the module's external symbol:\n%s", module)
	}
}

// TestGeneratePointerDifference covers the shape the backend divides the stride
// out of: the distance is stated in bytes and the element size is the divisor.
func TestGeneratePointerDifference(t *testing.T) {
	module := generate(t, `long long table[8];
void main(void) {
    long long i = (long long)__ic_load(d0, Setting);
    long long *p = table + i;
    __ic_store(d1, Setting, p - table);
}`)

	for _, want := range []string{"ptrtoint", "sub nsw", "sdiv exact"} {
		if !strings.Contains(module, want) {
			t.Errorf("the difference has no %q:\n%s", want, module)
		}
	}
}
