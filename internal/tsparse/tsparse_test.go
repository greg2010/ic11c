package tsparse_test

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/greg2010/ic11c/internal/source"
	"github.com/greg2010/ic11c/internal/tsparse"
)

// TestParsingCostsWhatTheSourceCosts holds the parse to linear time on the
// two shapes that make a node wide and a tree deep: a run of unclosed
// openers, and a chain of nested casts. Wall clock is asserted since both
// costs are inside the grammar's own lookups, which no counter here can see.
func TestParsingCostsWhatTheSourceCosts(t *testing.T) {
	tests := []struct {
		name  string
		what  string
		write func(int) string
	}{
		{
			name: "a region the grammar could not read",
			what: "unclosed '('",
			write: func(n int) string {
				return "void f(void) { double x = " + strings.Repeat("(", n) + "1.0; }"
			},
		},
		{
			name: "constructs inside one another",
			what: "nested casts",
			write: func(n int) string {
				return "void f(void) { double x = " + strings.Repeat("(double)", n) + "1.0; }"
			},
		},
	}
	const small, large = 2000, 8000
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fastest := func(n int) time.Duration {
				src := tt.write(n)
				best := time.Duration(math.MaxInt64)
				for range 3 {
					start := time.Now()
					if _, _, err := tsparse.Parse("test.c", src); err != nil {
						t.Fatalf("Parse failed: %v", err)
					}
					best = min(best, time.Since(start))
				}
				return best
			}
			// A measurement down in the noise would make any ratio meaningless,
			// so the sizes are held to being worth timing rather than the timing
			// being trusted.
			const measurable = 100 * time.Microsecond
			quick := fastest(small)
			if quick < measurable {
				t.Fatalf("%d %s parse in %v, which is too quick to scale against; raise the sizes", small, tt.what, quick)
			}
			slow := fastest(large)
			if ratio := float64(slow) / float64(quick); ratio > 8 {
				t.Errorf("%d %s took %v and %d took %v, a factor of %.1f; linear over this span is 4 and quadratic is 16",
					small, tt.what, quick, large, slow, ratio)
			}
		})
	}
}

// TestRefusesWhatMicroCExcludes covers the constructs C admits and MicroC
// does not; the grammar reads every one, so refusing them by name is the
// converter's job. Each row writes the whole report, since a substring match
// would pass on a sentence at the wrong place or a second, unmentioned one.
func TestRefusesWhatMicroCExcludes(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "struct",
			src:  "struct S { long long a; };\n",
			want: []string{"test.c:1:1: structs are not supported in MicroC"},
		},
		{
			name: "union",
			src:  "union U { long long a; };\n",
			want: []string{"test.c:1:1: unions are not supported in MicroC"},
		},
		{
			name: "enum",
			src:  "enum E { A };\n",
			want: []string{"test.c:1:1: enums are not supported in MicroC"},
		},
		{
			name: "typedef",
			src:  "typedef long long word;\n",
			want: []string{"test.c:1:1: typedef is not supported in MicroC"},
		},
		{
			name: "include",
			src:  "#include <stdio.h>\n",
			want: []string{"test.c:1:1: preprocessor directives are not supported in MicroC"},
		},
		{
			name: "define",
			src:  "#define N 1\n",
			want: []string{"test.c:1:1: preprocessor directives are not supported in MicroC"},
		},
		{
			name: "sizeof",
			src:  "long long f(void) { return sizeof(long long); }\n",
			want: []string{"test.c:1:28: sizeof is not supported in MicroC"},
		},
		{
			name: "goto",
			src:  "void f(void) { goto done; done: ; }\n",
			want: []string{"test.c:1:16: goto is not supported in MicroC", "test.c:1:27: a label is not supported in MicroC, which has no goto to reach one"},
		},
		{
			name: "member access",
			src:  "long long f(long long a) { return a.b; }\n",
			// At the '.', which is the byte joining two expressions the language
			// does have rather than the first byte of the one it does not.
			want: []string{"test.c:1:36: member access is not supported in MicroC; structs and unions are excluded"},
		},
		{
			// At the ',', for the reason member access is reported at its own
			// operator: both are written between two expressions the language
			// does have, and one rule answers for both.
			name: "comma operator",
			src:  "void f(long long a) { a = (1, 2); }\n",
			want: []string{"test.c:1:29: the comma operator is not supported in MicroC"},
		},
		{
			name: "nullptr",
			src:  "long long *p = nullptr;\n",
			want: []string{"test.c:1:16: nullptr is not supported in MicroC; a pointer names a slot and has no null"},
		},
		{
			name: "static",
			src:  "static long long a;\n",
			want: []string{"test.c:1:1: the 'static' storage class is not supported in MicroC"},
		},
		{
			name: "int",
			src:  "int a;\n",
			want: []string{"test.c:1:1: the 'int' type specifier is not supported in MicroC; C's int is 32 bits and every value here is exact to 53 — write 'long long', which C guarantees at least 64 bits everywhere"},
		},
		{
			name: "float",
			src:  "float a;\n",
			want: []string{"test.c:1:1: the 'float' type specifier is not supported in MicroC; every register and memory slot holds one whole double, so there is no 32-bit type for it to name — write 'double'"},
		},
		{
			name: "char",
			src:  "char a;\n",
			want: []string{"test.c:1:1: the 'char' type specifier is not supported in MicroC; a character literal is a long long"},
		},
		{
			name: "unsigned",
			src:  "unsigned long long a;\n",
			want: []string{"test.c:1:1: " + notAType("unsigned long long")},
		},
		{
			name: "a name in type position",
			src:  "widget *w;\n",
			want: []string{"test.c:1:1: " + notAType("widget")},
		},
		// A word past a type the words in front of it already spell is a stray
		// declarator where it repeats one of them, and a C type MicroC does not
		// have where it does not.
		{
			name: "long double",
			src:  "long double a;\n",
			want: []string{"test.c:1:1: " + notAType("long double")},
		},
		{
			name: "long long double",
			src:  "long long double a;\n",
			want: []string{"test.c:1:1: " + notAType("long long double")},
		},
		{
			name: "long unsigned",
			src:  "long unsigned a;\n",
			want: []string{"test.c:1:1: " + notAType("long unsigned")},
		},
		{
			name: "long signed",
			src:  "long signed a;\n",
			want: []string{"test.c:1:1: " + notAType("long signed")},
		},
		{
			name: "unsigned long",
			src:  "unsigned long a;\n",
			want: []string{"test.c:1:1: " + notAType("unsigned long")},
		},
		{
			name: "signed long",
			src:  "signed long a;\n",
			want: []string{"test.c:1:1: " + notAType("signed long")},
		},
		{
			name: "int long",
			src:  "int long a;\n",
			want: []string{"test.c:1:1: " + notAType("int long")},
		},
		// A name the grammar took into a type is not part of a type name, and
		// quoting the words around it offers a spelling nothing in the language
		// has. Where the name stands is what says what it is.
		{
			name: "a name behind the type words",
			src:  "long long a b = 1;\n",
			want: []string{"test.c:1:12: expected ';', found 'b'"},
		},
		{
			name: "a name in front of the type words",
			src:  "widget long long a = 1;\n",
			want: []string{"test.c:1:1: expected a type, found 'widget'"},
		},
		{
			name: "a name behind the short spelling",
			src:  "long a b = 1;\n",
			want: []string{"test.c:1:7: expected ';', found 'b'"},
		},
		{
			// The declarator's name belonged where the third word stands, and
			// "long long long" is no more a type name for having been written.
			name: "a size word past the type",
			src:  "long long long a = 1;\n",
			want: []string{"test.c:1:11: expected an identifier, found 'long'"},
		},
		{
			// At the 'int', which is the word to delete. The rest of the type is
			// spelled correctly and is not what has to change.
			name: "trailing int",
			src:  "long long int a;\n",
			want: []string{"test.c:1:11: MicroC writes the integer type as 'long long', without the trailing 'int'"},
		},
		{
			// The sentence quotes the spelling that was written rather than the
			// canonical one, since the word to delete is all that is wrong.
			name: "trailing int on the short spelling",
			src:  "long int a;\n",
			want: []string{"test.c:1:6: MicroC writes the integer type as 'long', without the trailing 'int'"},
		},
		{
			// At the ',' joining the second name to the first, which is the
			// token to delete.
			name: "two declarators",
			src:  "long long a, b;\n",
			want: []string{"test.c:1:12: MicroC declares one variable per declaration"},
		},
		{
			// One declaration is one mistake however many names it lists, and a
			// row with two declarators passes whether the sentence is drawn per
			// declaration or per extra name. This one is drawn per declaration,
			// so it stays on the first comma rather than moving to the second.
			name: "three declarators",
			src:  "long long a, b, c;\n",
			want: []string{"test.c:1:12: MicroC declares one variable per declaration"},
		},
		{
			name: "function pointer",
			src:  "long long (*f)(void);\n",
			want: []string{"test.c:1:11: a parenthesized declarator is not supported in MicroC; it spells a function pointer, which is excluded"},
		},
		{
			name: "variadic",
			src:  "long long f(long long a, ...);\n",
			want: []string{"test.c:1:26: variadic parameters are not supported in MicroC"},
		},
		{
			// At the second '[', which is the bracket to cut back to. The
			// declarator inside it is written correctly, so a sentence on its
			// first byte would point at the name instead.
			name: "multi-dimensional array",
			src:  "long long a[2][3];\n",
			want: []string{"test.c:1:15: multi-dimensional arrays are not supported in MicroC; index a flat array"},
		},
		{
			// Three brackets is the same one mistake, and the sentence goes on
			// the first past the one MicroC admits rather than the last.
			name: "three-dimensional array",
			src:  "long long a[2][3][4];\n",
			want: []string{"test.c:1:15: multi-dimensional arrays are not supported in MicroC; index a flat array"},
		},
		{
			// A suffix that is not a second array keeps the general sentence,
			// drawn against the same token: the one the declarator has too many
			// of.
			name: "a parameter list outside an array",
			src:  "long long a[2](void);\n",
			want: []string{"test.c:1:15: a declarator names one array or one parameter list in MicroC, and nothing outside it"},
		},
		{
			name: "a parameter list outside a parameter list",
			src:  "long long f(void)(void);\n",
			want: []string{"test.c:1:18: a declarator names one array or one parameter list in MicroC, and nothing outside it"},
		},
		{
			name: "unsized array",
			src:  "long long a[];\n",
			want: []string{"test.c:1:12: an array bound is required outside a parameter list"},
		},
		{
			name: "nested initializer",
			src:  "constexpr long long a[2] = {{1}, 2};\n",
			want: []string{"test.c:1:29: nested brace initializers are not supported in MicroC; arrays are one-dimensional"},
		},
		{
			name: "designated initializer",
			src:  "constexpr long long a[2] = {[0] = 1};\n",
			want: []string{"test.c:1:29: a designated initializer is not supported in MicroC"},
		},
		{
			name: "cast to pointer",
			src:  "long long f(long long a) { return (long long *)a; }\n",
			want: []string{"test.c:1:46: a cast to a pointer type is not supported in MicroC"},
		},
		// A declarator that reads as a postfix expression makes the parentheses
		// a group rather than a cast, and one that does not leaves them the cast
		// the programmer wrote. A star is the second kind wherever it appears in
		// the declarator, since no expression applies one behind its operand.
		{
			name: "cast to a pointer against a tighter operator",
			src:  "long long f(long long a, long long b) { return (a*) & b + 1; }\n",
			want: []string{
				"test.c:1:49: " + notAType("a"),
				"test.c:1:50: a cast to a pointer type is not supported in MicroC",
			},
		},
		{
			name: "cast to an array of pointers against a tighter operator",
			src:  "long long f(long long a, long long b) { return (a*[1]) & b + 1; }\n",
			want: []string{
				"test.c:1:49: " + notAType("a"),
				"test.c:1:50: a cast to a pointer type is not supported in MicroC",
			},
		},
		{
			name: "cast to a parenthesized pointer against a tighter operator",
			src:  "long long f(long long a, long long b) { return (a(*)[1]) & b + 1; }\n",
			want: []string{
				"test.c:1:49: " + notAType("a"),
				"test.c:1:50: a cast to a pointer type is not supported in MicroC",
			},
		},
		{
			name: "cast to void",
			src:  "void f(long long a) { (void)a; }\n",
			want: []string{"test.c:1:24: a cast to void is not supported in MicroC"},
		},
		{
			name: "cast to dev",
			src:  "void f(long long a) { (dev)a; }\n",
			want: []string{"test.c:1:24: a cast to dev is not supported in MicroC; a device is named, not computed"},
		},
		{
			name: "a function declared as an unbraced body",
			src:  "void f(long long x) { if (x) void g(void); }\n",
			want: []string{"test.c:1:30: a function is declared at file scope in MicroC, not inside a block"},
		},
		{
			name: "const on a function",
			src:  "const long long f(void) { return 1; }\n",
			want: []string{"test.c:1:1: const is not valid on a function"},
		},
		{
			name: "constexpr parameter",
			src:  "long long f(constexpr long long a) { return a; }\n",
			want: []string{"test.c:1:13: constexpr is not valid on a parameter"},
		},
		{
			name: "prefab on a function",
			src:  "[[ic11c::prefab(\"X\")]] void f(void) { }\n",
			want: []string{"test.c:1:1: [[ic11c::prefab(\"PrefabName\")]] states which device a pin is wired to and belongs on a dev declaration; a function names no pin"},
		},
		{
			name: "prefab on a parameter",
			src:  "void f([[ic11c::prefab(\"X\")]] dev d) { }\n",
			want: []string{"test.c:1:8: [[ic11c::prefab(\"PrefabName\")]] states which device a pin is wired to, and a dev parameter names whichever pin each call site passes; write it on the dev declaration the call site names"},
		},
		{
			name: "two prefabs",
			src:  "[[ic11c::prefab(\"X\"), ic11c::prefab(\"Y\")]] const dev d = d0;\n",
			want: []string{"test.c:1:23: a declaration names one prefab, already named at test.c:1:1"},
		},
		{
			name: "unknown attribute",
			src:  "[[gnu::unused]] const dev d = d0;\n",
			want: []string{"test.c:1:1: the only attribute MicroC recognizes is [[ic11c::prefab(\"PrefabName\")]], which states the prefab a dev declaration's pin is wired to"},
		},
		{
			name: "attribute behind a specifier",
			src:  "const [[ic11c::prefab(\"X\")]] dev d = d0;\n",
			want: []string{"test.c:1:7: an attribute leads a declaration in MicroC; write it in front of the specifiers and the type"},
		},
		{
			name: "attribute behind the type",
			src:  "const dev [[ic11c::prefab(\"X\")]] d = d0;\n",
			want: []string{"test.c:1:11: an attribute leads a declaration in MicroC; write it in front of the specifiers and the type"},
		},
		{
			name: "attribute behind the type of a parameter",
			src:  "void f(dev [[ic11c::prefab(\"X\")]] d) { }\n",
			want: []string{"test.c:1:12: an attribute leads a declaration in MicroC; write it in front of the specifiers and the type"},
		},
		{
			name: "old-style parameter list",
			src:  "long long f(a, b) long long a; long long b; { return a; }\n",
			want: []string{"test.c:1:19: an old-style parameter list is not supported in MicroC; write each parameter's type inside the parentheses"},
		},
		{
			name: "keyword as a name",
			src:  "long long dev;\n",
			want: []string{"test.c:1:11: expected an identifier, found 'dev'"},
		},
		{
			name: "reserved word as a name",
			src:  "long long nullptr;\n",
			want: []string{"test.c:1:11: expected an identifier, found 'nullptr'"},
		},
		{
			// At the label rather than at the ':' a parse reading C would stop
			// on, and naming the construct rather than the terminator C wanted.
			// See [TestRecordedAnswers].
			name: "label",
			src:  "void f(void) { top: ; }\n",
			want: []string{"test.c:1:16: a label is not supported in MicroC, which has no goto to reach one"},
		},
		{
			name: "case outside a switch",
			src:  "void f(void) { case 1: ; }\n",
			want: []string{"test.c:1:16: a case or default label is only valid inside a switch"},
		},
		{
			name: "statement before a label",
			src:  "void f(long long x) { switch (x) { x = 1; case 1: break; } }\n",
			want: []string{"test.c:1:36: a statement in a switch body must follow a case or default label"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkRefused(t, tt.src, tt.want)
		})
	}
}

// TestRefusesWhatHangsOffADeclarator covers the qualifiers, storage classes
// and modifiers C admits between a declarator's own parts, and behind the
// type — none of which fill a field, so a field-only walk would silently
// drop them. Each row writes the whole report.
func TestRefusesWhatHangsOffADeclarator(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "const on a pointer",
			src:  "long long *const p;\n",
			want: []string{"test.c:1:12: const must precede the type in MicroC"},
		},
		{
			name: "volatile on a pointer",
			src:  "long long a;\nlong long *volatile p = &a;\n",
			want: []string{"test.c:2:12: the 'volatile' qualifier is not supported in MicroC"},
		},
		{
			name: "restrict on a pointer",
			src:  "long long a;\nlong long *restrict p = &a;\n",
			want: []string{"test.c:2:12: the 'restrict' qualifier is not supported in MicroC"},
		},
		{
			name: "atomic on a pointer",
			src:  "long long a;\nlong long *_Atomic p = &a;\n",
			want: []string{"test.c:2:12: the '_Atomic' qualifier is not supported in MicroC"},
		},
		{
			name: "static in a parameter's array bound",
			src:  "long long f(long long a[static 4]) { return a[0]; }\n",
			want: []string{"test.c:1:25: the 'static' storage class is not supported in MicroC"},
		},
		{
			name: "const in a parameter's array bound",
			src:  "long long f(long long a[const 4]) { return a[0]; }\n",
			want: []string{"test.c:1:25: const must precede the type in MicroC"},
		},
		{
			name: "volatile in a parameter's array bound",
			src:  "long long f(long long a[volatile 4]);\n",
			want: []string{"test.c:1:25: the 'volatile' qualifier is not supported in MicroC"},
		},
		{
			name: "const in a cast",
			src:  "long long f(long long x) { return (const long long)x; }\n",
			want: []string{"test.c:1:36: 'const' is not supported in a cast; it says how a declaration is stored, and a cast declares nothing"},
		},
		{
			name: "constexpr in a cast",
			src:  "long long f(long long x) { return (constexpr long long)x; }\n",
			want: []string{"test.c:1:36: 'constexpr' is not supported in a cast; it says how a declaration is stored, and a cast declares nothing"},
		},
		{
			name: "const behind the type in a cast",
			src:  "long long f(long long x) { return (long long const)x; }\n",
			want: []string{"test.c:1:46: 'const' is not supported in a cast; it says how a declaration is stored, and a cast declares nothing"},
		},
		{
			name: "volatile in a cast",
			src:  "long long f(long long x) { return (volatile long long)x; }\n",
			want: []string{"test.c:1:36: the 'volatile' qualifier is not supported in MicroC"},
		},
		{
			name: "restrict in a cast",
			src:  "long long f(long long *p) { return *(restrict long long *)p; }\n",
			want: []string{"test.c:1:38: the 'restrict' qualifier is not supported in MicroC", "test.c:1:57: a cast to a pointer type is not supported in MicroC"},
		},
		{
			name: "atomic in a cast",
			src:  "long long f(long long x) { return (_Atomic long long)x; }\n",
			want: []string{"test.c:1:36: the '_Atomic' qualifier is not supported in MicroC"},
		},
		{
			name: "const behind the type",
			src:  "long long const x = 1;\n",
			want: []string{"test.c:1:11: const must precede the type in MicroC"},
		},
		{
			name: "const behind the type of a parameter",
			src:  "long long f(long long const a);\n",
			want: []string{"test.c:1:23: const must precede the type in MicroC"},
		},
		{
			name: "volatile in front of the type",
			src:  "volatile long long a;\n",
			want: []string{"test.c:1:1: the 'volatile' qualifier is not supported in MicroC"},
		},
		{
			name: "restrict in front of the type",
			src:  "long long a;\nlong long *p = &a;\nvoid f(void) { restrict long long b; }\n",
			want: []string{"test.c:3:16: the 'restrict' qualifier is not supported in MicroC"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkRefused(t, tt.src, tt.want)
		})
	}
}

// TestBrokenSourceStillYieldsAFile is what the grammar buys: an error node
// stands where the source could not be read, so the parse always finishes
// and the text after the mistake is still readable. Each row writes the
// whole report, since recovery's cost is measured in how far it lands.
func TestBrokenSourceStillYieldsAFile(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{
			src:  "long long f(",
			want: []string{"test.c:1:12: unclosed '('; no matching ')' before end of file"},
		},
		{
			src:  "long long f(void) {",
			want: []string{"test.c:1:20: expected '}', found end of file"},
		},
		{
			src:  "void f(void) { if (",
			want: []string{"test.c:1:14: unclosed '{'; no matching '}' before end of file"},
		},
		{
			src:  "void f(void) { a[",
			want: []string{"test.c:1:14: unclosed '{'; no matching '}' before end of file"},
		},
		{
			src:  "void f(void) { switch (x) { case",
			want: []string{"test.c:1:14: unclosed '{'; no matching '}' before end of file"},
		},
		{
			src:  "long long a = {1,",
			want: []string{"test.c:1:15: unclosed '{'; no matching '}' before end of file"},
		},
		{
			src:  "/* unterminated",
			want: []string{"test.c:1:1: unterminated block comment"},
		},
		{
			src:  "'",
			want: []string{"test.c:1:1: unterminated character literal"},
		},
		{
			src:  "\"",
			want: []string{"test.c:1:1: unterminated string literal"},
		},
		{
			src:  "long long x = 1 ? 2",
			want: []string{"test.c:1:1: expected a declaration, found 'long'"},
		},
		{
			src:  "const",
			want: []string{"test.c:1:1: expected a declaration, found 'const'"},
		},
		{
			src:  "long long const",
			want: []string{"test.c:1:16: expected ';', found end of file"},
		},
		{
			src:  "void f(void) { g(1,",
			want: []string{"test.c:1:14: unclosed '{'; no matching '}' before end of file"},
		},
		{
			src:  "void f(const",
			want: []string{"test.c:1:7: unclosed '('; no matching ')' before end of file"},
		},
		{
			src:  "}{)(",
			want: []string{"test.c:1:1: expected a declaration, found '}'"},
		},
		{
			src:  "void f(long long x) { while (x) const long long a = g(if (x) long long b = 1;); }",
			want: []string{"test.c:1:55: expected an identifier, found 'if'", "test.c:1:62: 'long' is not expected here", "test.c:1:77: expected an argument, found ';'"},
		},
		{
			src:  "void f(long long x) { if (x) long long a[if (x) long long b = 1;] = {0}; }",
			want: []string{"test.c:1:30: expected a statement, found 'long'", "test.c:1:65: expected a statement, found ']'", "test.c:1:70: expected a statement, found an integer literal"},
		},
		{
			// The declaration written as the body runs into the '}' that closes
			// the function before it reaches a terminator of its own, so no
			// braces are written around it and the grammar's account stands. A
			// span that ran past the closer would take the block behind it in.
			src: "void f(long long x) { if (x) long long a } { long long b; }",
			want: []string{
				"test.c:1:30: expected an identifier, found 'long'",
				"test.c:1:34: expected ';', found 'long'",
				"test.c:1:41: expected ';', found '}'",
				"test.c:1:42: expected a declarator, found '}'",
				"test.c:1:44: expected a declaration; a statement is only valid inside a function body",
			},
		},
		{
			src:  "void f(long long x) { if (x) long long a = 1",
			want: []string{"test.c:1:21: unclosed '{'; no matching '}' before end of file"},
		},
		{
			src:  "if (1) long long a = 1;",
			want: []string{"test.c:1:1: expected a declaration; a statement is only valid inside a function body"},
		},
		{
			src:  "void f(void) { x; else long long a = 1; }",
			want: []string{"test.c:1:17: expected a declarator, found ';'", "test.c:1:23: expected ';', found 'long'", "test.c:1:24: expected a declarator, found 'long'"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			f, diags, err := tsparse.Parse("test.c", tt.src)
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			if f == nil {
				t.Fatal("Parse returned a nil file")
			}
			checkSame(t, diags, tt.want)
		})
	}
}

// TestARefusalIsTheSameWhereverTheConstructStands is what [refusals] exists
// for: a construct MicroC excludes is named as itself wherever it was
// written, whether at file scope or inside a block. Only the sentence is
// compared, since the two positions are different bytes of different programs.
func TestARefusalIsTheSameWhereverTheConstructStands(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{name: "struct", src: "struct S;\n", want: "structs are not supported in MicroC"},
		{name: "struct with members", src: "struct S { long long x; };\n", want: "structs are not supported in MicroC"},
		{name: "union", src: "union U;\n", want: "unions are not supported in MicroC"},
		{name: "enum", src: "enum E { X };\n", want: "enums are not supported in MicroC"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scopes := map[string]string{
				"file scope": tt.src,
				"a block":    "void f(void) { " + tt.src + " }\n",
			}
			for scope, src := range scopes {
				_, diags, err := tsparse.Parse("test.c", src)
				if err != nil {
					t.Fatalf("Parse failed: %v", err)
				}
				if len(diags) != 1 {
					t.Errorf("at %s %q drew %d diagnostics, want the one refusal:\n%s", scope, src, len(diags), diags)
					continue
				}
				if diags[0].Msg != tt.want {
					t.Errorf("at %s %q drew %q, want %q", scope, src, diags[0].Msg, tt.want)
				}
			}
		})
	}
}

// checkRefused holds a program to the whole report it draws.
func checkRefused(t *testing.T, src string, want []string) {
	t.Helper()
	_, diags, err := tsparse.Parse("test.c", src)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	checkSame(t, diags, want)
}

// checkSame compares a report against what a row writes down, message by
// message and position by position.
func checkSame(t *testing.T, got source.DiagnosticList, want []string) {
	t.Helper()
	lines := make([]string, len(got))
	for i, d := range got {
		lines[i] = d.Error()
	}
	if !slices.Equal(lines, want) {
		t.Errorf("reported\n\t%q\nwant\n\t%q", lines, want)
	}
}

// TestReportsLexicalAndSyntacticErrorsTogether checks that what the lexer found
// and what the grammar found arrive as one list in reading order, and that a
// mistake both notice costs one message.
func TestReportsLexicalAndSyntacticErrorsTogether(t *testing.T) {
	_, diags, err := tsparse.Parse("test.c", "long long a = 010;\nstruct S { long long b; };\n")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(diags) != 2 {
		t.Fatalf("got %d diagnostics, want 2:\n%s", len(diags), diags)
	}
	if !strings.Contains(diags[0].Msg, "octal literals are not supported") {
		t.Errorf("first message = %q, want the lexical error", diags[0].Msg)
	}
	if !strings.Contains(diags[1].Msg, "structs are not supported") {
		t.Errorf("second message = %q, want the refusal", diags[1].Msg)
	}
}
