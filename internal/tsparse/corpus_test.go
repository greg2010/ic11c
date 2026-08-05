package tsparse_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/corpus"
	"github.com/greg2010/ic11c/internal/source"
	"github.com/greg2010/ic11c/internal/tsparse"
)

// microCRoots are the directories holding MicroC the front end is expected
// to read: the shipped corpus, and cmd/ic11c/testdata/refusals, which holds
// programs the compiler declines but every one of which parses.
func microCRoots(tb testing.TB) []string {
	tb.Helper()
	dir, err := corpus.Dir()
	if err != nil {
		tb.Fatalf("%v", err)
	}
	return []string{dir, filepath.Join("..", "..", "cmd", "ic11c", "testdata")}
}

// TestTheCorpusIsRead holds the front end to every program the compiler
// ships: each is read without complaint, yields declarations, and carries a
// usable position on every node. It fails rather than reports, since this
// breaking is the whole language broken for its own corpus.
func TestTheCorpusIsRead(t *testing.T) {
	paths := microCPaths(t)
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			src := readFile(t, path)
			tree, diags, err := tsparse.Parse(path, src)
			if err != nil {
				t.Fatalf("tsparse.Parse(%s) failed: %v", path, err)
			}
			if len(diags) != 0 {
				t.Fatalf("the front end rejected %s:\n%s", path, diags)
			}
			if len(tree.Decls) == 0 {
				t.Fatalf("%s produced no declarations, so nothing here read anything", path)
			}
			checkEveryPosition(t, tree)
		})
	}
}

// TestTheFragmentsAreRead covers shapes the corpus does not reach: a signed
// literal, an array of pointers, a switch with a default arm, and the
// spellings parens.go and unbraced.go make readable at all. A row that draws
// a diagnostic is a construct the front end has stopped reading.
func TestTheFragmentsAreRead(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"signed initializer", "constexpr long long k[2] = {-1, +2};\n"},
		{"signed index", "long long a[4];\nlong long f(void) { return a[-1 + 2]; }\n"},
		{"negation", "long long f(long long x) { return -x + - 1; }\n"},
		{"array of pointers", "long long f(long long *p[]) { return *p[0]; }\n"},
		{"pointer to array element", "long long a[4];\nlong long f(void) { return *(a + 1); }\n"},
		{"const and constexpr", "constexpr const long long k = 1;\nconst long long c = 2;\n"},
		{"prefab attribute", "[[ic11c::prefab(\"StructureGasSensor\")]] const dev d = d0;\n"},
		{"prototype then definition", "long long f(long long);\nlong long f(long long x) { return x; }\n"},
		{"unnamed parameter", "long long f(long long);\n"},
		{"void parameter list", "void f(void) { }\nvoid g() { }\n"},
		{"switch with default", "void f(long long x) { switch (x) { case 1: break; default: break; } }\n"},
		{"empty statement", "void f(void) { for (long long i = 0; i < 2; i++) ; }\n"},
		{"do while", "void f(void) { long long i = 0; do { i++; } while (i < 2); }\n"},
		{"dangling else", "void f(long long x) { if (x) if (x) x = 1; else x = 2; }\n"},
		{"nested ternary", "long long f(long long x) { return x ? 1 : x ? 2 : 3; }\n"},
		{"casts", "long long f(double d) { return (long long)d + (bool)d; }\n"},
		{"increment forms", "void f(long long x) { x++; ++x; x--; --x; }\n"},
		{"compound assignment", "void f(long long x) { x += 1; x <<= 2; x ^= 3; }\n"},
		{"hexadecimal", "constexpr long long k = 0x7fffffff;\n"},
		{"character escapes", "constexpr long long k = '\\n';\n"},
		{"string hash", "long long f(void) { return __ic_hash(\"a\\tb\"); }\n"},
		{"address and deref", "long long a;\nlong long f(void) { long long *p = &a; return *p; }\n"},
		{"comments everywhere", "// lead\nlong long /* mid */ a = 1; // trail\n"},
		{"chained assignment", "void f(long long a, long long b) { a = b = 1; }\n"},
		{"arithmetic precedence", "long long f(long long a, long long b) { return a + b * 2 - a / b % 3; }\n"},
		{"logical precedence", "bool f(bool a, bool b, bool c) { return a || b && !c; }\n"},
		{"bitwise precedence", "long long f(long long a, long long b) { return a | b ^ a & b; }\n"},
		{"shift against comparison", "bool f(long long a) { return a << 1 < a >> 1; }\n"},
		{"repeated unary", "long long f(long long a) { return - - +a; }\n"},
		{"nested calls", "long long g(long long);\nlong long f(void) { return g(g(1)); }\n"},
		{"postfix on a subscript", "long long a[4];\nvoid f(void) { a[0]++; }\n"},
		{"cast binds tighter than multiplication", "double f(long long a) { return (double)a * 2; }\n"},
		{"parentheses against multiplication", "long long f(long long a, long long b) { return (a) * b; }\n"},
		{"declaration as an if body", "void f(long long x) { if (x) long long a = 1; }\n"},
		{"declaration as an else body", "void f(long long x) { if (x) ; else long long a = 1; }\n"},
		{"declaration as a while body", "void f(long long x) { while (x) const long long a = 1; }\n"},
		{"declaration as a do body", "void f(long long x) { do double a = 1.0; while (x); }\n"},
		{"declaration as a for body", "void f(void) { for (long long i = 0; i < 2; i++) long long a = i; }\n"},
		{"array declaration as an if body", "void f(long long x) { if (x) long long a[2] = {1, 2}; }\n"},
		{"declaration as a nested if body", "void f(long long x) { if (x) if (x) long long a = 1; else long long b = 2; }\n"},
		{"declaration as a body followed by a statement", "void f(long long x) { if (x) long long a = 1; x = 2; }\n"},
		{"declaration as a body inside a case", "void f(long long x) { switch (x) { case 1: if (x) long long a = 1; break; } }\n"},
		{"declaration as a body over two lines", "void f(long long x) {\n\tif (x)\n\t\tlong long a = 1;\n\tx = 2;\n}\n"},
		{"declaration as a body behind a comment", "void f(long long x) { if (x) /* here */ long long a = 1; // trailing\n}\n"},
		{"declaration as a body holding a string", "void f(long long x) { if (x) long long a = __ic_hash(\"a;b\"); }\n"},
		// constexpr opens a declaration and names no type, so it is outside the
		// enumeration [TestEveryTypeOpensAnUnbracedBody] holds the type words to.
		{"constexpr declaration as an if body", "void f(long long x) { if (x) constexpr long long a = 1; }\n"},
		// An attribute opens a declaration too, so the body it introduces is one
		// the braces have to be written around like any other. It is the only
		// declaration MicroC opens with punctuation rather than a word.
		{"attributed declaration as an if body", "void f(long long x) { if (x) [[ic11c::prefab(\"StructureGasSensor\")]] const dev d = d0; }\n"},
		{"attributed declaration as an else body", "void f(long long x) { if (x) ; else [[ic11c::prefab(\"StructureGasSensor\")]] const dev d = d0; }\n"},
		{"attributed declaration as a while body", "void f(long long x) { while (x) [[ic11c::prefab(\"StructureGasSensor\")]] const dev d = d0; }\n"},
		{"attributed declaration as a do body", "void f(long long x) { do [[ic11c::prefab(\"StructureGasSensor\")]] const dev d = d0; while (x); }\n"},
		{"attributed declaration as a for body", "void f(void) { for (;;) [[ic11c::prefab(\"StructureGasSensor\")]] const dev d = d0; }\n"},
		{"attributed declaration as a body followed by a statement", "void f(long long x) { if (x) [[ic11c::prefab(\"StructureGasSensor\")]] const dev d = d0; x = 2; }\n"},
		// A word the C grammar reserves and C23 does not is an ordinary name,
		// which is what internal/lexer says it is. See internal/tsparse/words.go.
		// long is the second spelling of the integer type, and every position
		// the language puts a type in reaches it by a different production.
		{"the short spelling at file scope", "long a;\n"},
		{"the short spelling on a local", "void main(void) { long a = 1; }\n"},
		{"the short spelling on a parameter", "void f(long x);\n"},
		{"the short spelling as a return type", "long f(void);\n"},
		{"the short spelling on an array", "long a[2];\n"},
		{"the short spelling on a pointer", "long *p;\n"},
		{"the short spelling on a pointer to const", "long a;\nconst long *p = &a;\n"},
		{"the short spelling in a cast", "void f(long long x) { x = (long)x; }\n"},
		{"the short spelling on a constant", "const long k = 3;\n"},
		{"the short spelling on a constexpr", "constexpr long c = 4;\n"},
		{"the short spelling as an unbraced body", "void f(long long x) { if (x) long a; }\n"},
		{"the canonical spelling at file scope", "long long a;\n"},
		{"a name the grammar reserves", "void f(void) { long long asm = 1; asm = 2; }\n"},
		{"a name the grammar reserves in a call", "long long offsetof(long long noreturn) { return noreturn; }\n"},
		{"names the grammar reserves in an expression", "long long f(long long NULL) { return NULL + __attribute__; }\n"},
		// The left operand of an assignment is a unary-expression in C and a
		// shorter list in the grammar, so a target outside that list arrives
		// bound the wrong way round. The front end hands the program on rather
		// than refuse it, since whether it names an object is semantics.
		{"a negated target", "void f(long long a) { -a = 1; }\n"},
		{"a twice-negated target", "void f(long long a) { - - a = 1; }\n"},
		{"a complemented target", "void f(long long a) { !a = 1; ~a = 1; +a = 1; }\n"},
		{"a cast target", "void f(double d) { (long long)d = 1; }\n"},
		{"a negated target of a compound assignment", "void f(long long a) { -a += 1; }\n"},
		{"a conditional target", "void f(long long a, long long b) { b = a ? a : b = 2; }\n"},
		{"an assignment inside a conditional's consequence", "void f(long long a, long long b) { a = b ? b = 1 : b; }\n"},
		{"a negated target behind an operator", "void f(long long a, long long b) { b + -a = 1; }\n"},
		{"an assignment the parentheses bind", "void f(long long a) { -(a = 1); }\n"},
		{"an assignment the parentheses bind around a name", "void f(long long a) { -((a) = 1); }\n"},
		// C binds a postfix operator tighter than every prefix one and the
		// grammar gives the two the same precedence, so a prefix operator in
		// front of one arrives outside it and is handed on rather than refused
		// here, since whether it names an object is semantics.
		{"a negated postfix increment", "void f(long long a) { long long z = -a++; }\n"},
		{"a complemented postfix increment", "void f(long long a) { long long z = ~a++; }\n"},
		{"a negated postfix decrement", "void f(long long a) { long long z = -a--; }\n"},
		{"a twice-negated postfix increment", "void f(long long a) { long long z = - -a++; }\n"},
		{"a dereferenced postfix increment", "long long f(long long *p) { return *p++; }\n"},
		{"a postfix increment the parentheses bind", "void f(long long a) { (-a)++; }\n"},
		{"a twice-incremented name", "void f(long long a) { long long z = a++++; }\n"},
		{"a complemented twice-incremented name", "void f(long long b) { long long z = ~b++++; }\n"},
		{"a negated increment then decrement", "void f(long long a) { long long z = -a++--; }\n"},
		{"a twice-negated twice-incremented name", "void f(long long a) { long long z = - -a++++; }\n"},
		{"a dereferenced twice-incremented pointer", "long long f(long long *p) { return *p++++; }\n"},
		{"a complemented call decremented then incremented", "long long g(long long);\nvoid f(long long a) { long long z = !g(a)--++; }\n"},
		{"a twice-updated name inside a subscript", "long long ar[2];\nvoid f(long long b) { long long z = ar[~b++--]; }\n"},
		{"a negated thrice-incremented name", "void f(long long a) { long long z = -a++++++; }\n"},
		// The grammar spells a literal's sign inside the literal where one is
		// written without a space, so the operator that has to move outside the
		// increment is not a node of its own.
		{"a signed literal incremented", "void f(void) { long long z = +2++; }\n"},
		{"a negative literal incremented", "void f(void) { long long z = -3++; }\n"},
		{"a signed literal incremented with a space", "void f(void) { long long z = - 2++; }\n"},
		{"a complemented signed literal incremented", "void f(void) { long long z = ~+0++; }\n"},
		{"a signed literal incremented then decremented", "void f(void) { long long z = -3++--; }\n"},
		{"a negated signed literal incremented", "void f(void) { long long z = - +2++; }\n"},
		{"a signed literal in a call incremented", "long long g(long long);\nvoid f(void) { long long z = g(+2++); }\n"},
		{"a prefix increment as a target", "void f(long long a) { ++a = 1; }\n"},
		// A postfix-expression is a unary-expression in C, so an assignment to
		// one is a program C reads and refuses on its meaning. The grammar's
		// left operand list leaves the increment out, so it cannot read the
		// program at all.
		{"a postfix increment as a target", "void f(long long a) { a++ = 1; }\n"},
		{"a postfix decrement as a compound target", "void f(long long a) { a-- += 2; }\n"},
		// All eleven assignment operators, since each is a token of its own and
		// what stands in front of one is the same program every time.
		{"a postfix increment as the target of every assignment", "void f(long long a) { a++ = 1; a++ += 1; a++ -= 1; a++ *= 1; a++ /= 1; a++ %= 1; a++ &= 1; a++ |= 1; a++ ^= 1; a++ <<= 1; a++ >>= 1; }\n"},
		{"a literal as the target of every assignment", "void f(void) { 1 = 1; 1 += 1; 1 -= 1; 1 *= 1; 1 /= 1; 1 %= 1; 1 &= 1; 1 |= 1; 1 ^= 1; 1 <<= 1; 1 >>= 1; }\n"},
		{"a prefix increment in front of a postfix one as a target", "void f(long long a) { ++a++ = 1; }\n"},
		{"a prefix decrement in front of a postfix one as a target", "void f(long long a) { --a-- >>= 1; }\n"},
		// A prefix increment swallows the whole chain of assignments behind it,
		// which is what [converter.bound] rotates back out. Parentheses written
		// around it instead would bind the chain the other way round.
		{"a prefix decrement as the target of a chain", "void f(long long a, long long c) { --a |= 3 = c; }\n"},
		{"a prefix decrement of an address as the target of a chain", "void f(long long c) { --&c |= 3 = c; }\n"},
		{"a prefix increment as the target of a chain of three", "void f(long long a, long long b, long long c) { ++a = b = c; }\n"},
		{"a subscripted postfix increment as a target", "long long a[2];\nvoid f(void) { a[0]++ = 1; }\n"},
		{"a dereferenced postfix increment as a target", "void f(long long *p) { *p++ = 1; }\n"},
		{"a prefix decrement as a target", "void f(long long a) { --a = 1; }\n"},
		{"a prefix increment the parentheses bind", "void f(long long a) { ++(a = 1); }\n"},
		// A name in parentheses is no cast, since MicroC has no typedef. The
		// grammar reads one as a cast anyway wherever what follows binds tighter
		// than the operator behind the closing parenthesis.
		{"a parenthesized name against a looser operator", "void f(long long a, long long b) { long long c = (a) & b; }\n"},
		{"a parenthesized name against a tighter operator", "void f(long long a, long long b) { long long c = (a) & b + 1; }\n"},
		{"a parenthesized name against multiplication", "void f(long long a, long long b) { long long c = (a) - b * 2; }\n"},
		{"a parenthesized name against a comparison", "void f(long long a, long long b) { long long c = (a) & b < 1; }\n"},
		{"a parenthesized name against an equality", "void f(long long a, long long b) { long long c = (a) & b == 1; }\n"},
		{"a parenthesized name dereferenced", "long long f(long long a, long long *p) { return (a) * *p; }\n"},
		{"a parenthesized name called", "long long g(long long);\nlong long f(long long a) { return (g)(a); }\n"},
		// A cast is a reading only where the parentheses could hold a type. A
		// name applied to a subscript or to an argument list is a postfix
		// expression and nothing else, and the grammar builds the same cast out
		// of it as it does out of a bare name.
		{"a parenthesized subscript against a tighter operator", "long long a[4];\nlong long f(long long c) { return (a[1]) & c + 1; }\n"},
		{"a parenthesized call against a tighter operator", "long long g(long long);\nlong long f(long long b, long long c) { return (g(b)) & c + 1; }\n"},
		{"a parenthesized subscript against multiplication", "long long a[4];\nlong long f(long long c) { return (a[1]) - c * 2; }\n"},
		{"a parenthesized call against multiplication", "long long g(long long);\nlong long f(long long b, long long c) { return (g(b)) + c * 2; }\n"},
		{"a parenthesized call taking no arguments", "long long g(void);\nlong long f(long long c) { return (g()) & c + 1; }\n"},
		{"a parenthesized twice-subscripted name", "long long a[4];\nlong long f(long long c) { return (a[1][2]) & c + 1; }\n"},
		{"a parenthesized call then subscript", "long long g(long long);\nlong long f(long long b, long long c) { return (g(b)[1]) & c + 1; }\n"},
		{"a parenthesized call taking a parenthesized argument", "long long ar[4];\nlong long g(long long);\nlong long f(long long c) { return (g((ar[1]))) & c + 1; }\n"},
		{"a parenthesized call taking a parenthesized name", "long long g(long long);\nlong long f(long long b, long long c) { return (g((b))) & c + 1; }\n"},
		// Two assignments to postfix increments in one statement arrive nested
		// inside one another, and both need the parentheses.
		{"chained assignment to postfix increments", "void f(long long a, long long b) { a++ = b++ = 1; }\n"},
		{"chained assignment to postfix decrements", "void f(long long a, long long b) { a-- = b-- = 1; }\n"},
		{"three chained assignments to postfix increments", "void f(long long a, long long b, long long c) { a++ = b++ = c++ = 1; }\n"},
		{"chained compound assignment to postfix increments", "void f(long long a, long long b) { a++ += b++ += 1; }\n"},
		{"chained assignment to a twice-updated name", "void f(long long a, long long b) { a++ = b++-- = 1; }\n"},
		{"chained assignment from a twice-updated name", "void f(long long a, long long b) { a++-- = b++ = 1; }\n"},
		{"chained compound assignment to twice-updated names", "void f(long long a, long long b) { a++-- += b++-- += 1; }\n"},
		{"chained assignment to an incremented call", "long long g(long long);\nvoid f(long long a, long long b) { g(a)++ = b--++ /= 1; }\n"},
		// A literal is a postfix-expression, so C reads an assignment written to
		// one and refuses it on its meaning. The grammar's left operand list
		// leaves the literals out with the increments.
		{"an assignment to a literal", "void f(long long a) { 1 = a; }\n"},
		{"a compound assignment to a literal", "void f(long long a) { 0 -= a; }\n"},
		{"a shift assignment to a literal", "void f(long long a) { 0 <<= a; }\n"},
		{"an assignment to a signed literal", "void f(long long a) { -3 -= a; }\n"},
		{"an assignment to a literal behind a spaced sign", "void f(long long a) { - 3 -= a; }\n"},
		{"an assignment to a complemented literal", "void f(long long a) { !1 /= a; }\n"},
		{"an assignment to a floating literal", "void f(double a) { 1.5 = a; }\n"},
		{"an assignment to a literal in an argument", "long long g(long long);\nvoid f(long long a) { long long z = g(1 = a); }\n"},
		{"an assignment to a character literal", "void f(long long a) { 'x' = a; }\n"},
		{"an assignment to a string literal", "void f(long long a) { \"s\" = a; }\n"},
		{"an assignment to true", "void f(bool a) { true = a; }\n"},
		{"an assignment to false", "void f(bool a) { false = a; }\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree, diags, err := tsparse.Parse("test.c", tt.src)
			if err != nil {
				t.Fatalf("tsparse.Parse failed: %v", err)
			}
			if len(diags) != 0 {
				t.Fatalf("the front end rejected the fragment:\n%s", diags)
			}
			checkEveryPosition(t, tree)
		})
	}
}

// TestAProductIsNotADeclaration closes the one ambiguity C leaves a grammar
// with no symbol table: "a * b;" declares a pointer named b wherever a names
// a type, and MicroC has no typedef, so a name outside the closed type set
// opens no declaration and the statement is a discarded product instead.
func TestAProductIsNotADeclaration(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		accepted bool
		// refusal is what the front end reports, in full and in order, for a
		// row the language does not read. It is empty for a row it does.
		refusal []string
	}{
		{name: "two names", src: "void f(void) { a * b; }\n", accepted: true},
		{name: "a name that is not a type", src: "void f(void) { widget *w; }\n", accepted: true},
		{name: "a subscript", src: "void f(void) { a * b[2]; }\n", accepted: true},
		{name: "two subscripts", src: "void f(void) { a * b[2][3]; }\n", accepted: true},
		{name: "a subscript by a name", src: "void f(void) { a * b[c]; }\n", accepted: true},
		{name: "a call", src: "void f(void) { a * b(c); }\n", accepted: true},
		{name: "a call taking several arguments", src: "void f(void) { a * b(c, d, e); }\n", accepted: true},
		{name: "a call taking none", src: "void f(void) { a * b(); }\n", accepted: true},
		{name: "a call then a subscript", src: "void f(void) { a * b(c)[2]; }\n", accepted: true},
		{name: "an indexed argument", src: "void f(void) { a * b(c[1]); }\n", accepted: true},
		{name: "an argument indexed by a name", src: "void f(void) { a * b(c[d]); }\n", accepted: true},
		{name: "a twice-indexed argument", src: "void f(void) { a * b(c[1][2]); }\n", accepted: true},
		{name: "a product among the arguments", src: "void f(void) { a * b(c * d); }\n", accepted: true},
		{name: "a call among the arguments", src: "void f(void) { a * b(c()); }\n", accepted: true},
		{name: "a call behind a name among the arguments", src: "void f(void) { a * b(d, c()); }\n", accepted: true},
		{name: "a call taking a call among the arguments", src: "void f(void) { a * b(c(d())); }\n", accepted: true},
		{name: "a dereference", src: "void f(void) { a * *b; }\n", accepted: true},
		{name: "assigned to", src: "void f(void) { a * b = c; }\n", accepted: true},
		{name: "assigned a chain", src: "void f(void) { a * b = c = d; }\n", accepted: true},
		{name: "a subscript assigned to", src: "void f(void) { a * b[2] = c; }\n", accepted: true},
		{name: "in a for initializer", src: "void f(void) { for (a * b; ; ) ; }\n", accepted: true},
		{name: "as an if body", src: "void f(long long x) { if (x) a * b; }\n", accepted: true},
		{name: "inside a case", src: "void f(long long x) { switch (x) { case 1: a * b; break; } }\n", accepted: true},
		{name: "twice over", src: "void f(void) { a * b; b * c; }\n", accepted: true},
		{name: "dev is still a type", src: "const dev d = d0;\n", accepted: true},
		{name: "a dev parameter is still a parameter", src: "void f(dev d) { }\n", accepted: true},
		{
			name:    "nothing between the names",
			src:     "void f(void) { a b; }\n",
			refusal: []string{"test.c:1:17: expected ';', found 'b'"},
		},
		{
			name:    "nothing between a name and a subscript",
			src:     "void f(void) { a b[2]; }\n",
			refusal: []string{"test.c:1:17: expected ';', found 'b'"},
		},
		{
			name:    "a qualifier inside the product",
			src:     "void f(void) { a * const b; }\n",
			refusal: []string{"test.c:1:19: expected ';', found 'const'"},
		},
		{
			name:    "a qualifier in front of the product",
			src:     "void f(void) { const a * b; }\n",
			refusal: []string{"test.c:1:22: " + notAType("a")},
		},
		{
			name:    "a brace initializer",
			src:     "void f(void) { a * b = {1}; }\n",
			refusal: []string{"test.c:1:23: expected ';', found '{'"},
		},
		{
			name:    "a second declarator",
			src:     "void f(void) { a * b, * c; }\n",
			refusal: []string{"test.c:1:21: the comma operator is not supported in MicroC"},
		},
		{
			name:    "a subscript with no index",
			src:     "void f(void) { a * b[]; }\n",
			refusal: []string{"test.c:1:22: expected an expression, found ']'"},
		},
		{
			name:    "a type among the arguments",
			src:     "void f(void) { a * b(void); }\n",
			refusal: []string{"test.c:1:22: expected an expression, found 'void'"},
		},
		{
			name:    "a parameter among the arguments",
			src:     "void f(void) { a * b(long long x); }\n",
			refusal: []string{"test.c:1:22: expected an expression, found 'long'"},
		},
		{
			name:    "an argument subscripted with no index",
			src:     "void f(void) { a * b(c[]); }\n",
			refusal: []string{"test.c:1:24: expected an expression, found ']'"},
		},
		{
			name:    "a qualifier on an argument",
			src:     "void f(void) { a * b(c const); }\n",
			refusal: []string{"test.c:1:24: expected an expression, found 'const'"},
		},
		// A name applied to something the grammar can read as a type is a macro
		// standing for one in C and a call in MicroC, which has no preprocessor
		// to declare the macro and no typedef to name the type.
		{name: "a call taking an indexed name", src: "void f(void) { a(b[2]); }\n", accepted: true},
		{name: "a call taking a call", src: "void f(void) { a(b(c)); }\n", accepted: true},
		{
			name:    "a call taking a subscript with no index",
			src:     "void f(void) { a(b[]); }\n",
			refusal: []string{"test.c:1:20: expected an expression, found ']'"},
		},
		{
			name:    "a call taking a qualified name",
			src:     "void f(void) { a(const b); }\n",
			refusal: []string{"test.c:1:18: expected an expression, found 'const'"},
		},
		{
			name:    "at file scope",
			src:     "a * b;\n",
			refusal: []string{"test.c:1:1: " + notAType("a")},
		},
		{
			name:    "a pointer at file scope",
			src:     "widget *w;\n",
			refusal: []string{"test.c:1:1: " + notAType("widget")},
		},
		{
			name:    "a subscript at file scope",
			src:     "a * b[2];\n",
			refusal: []string{"test.c:1:1: " + notAType("a")},
		},
		{
			name:    "assigned to at file scope",
			src:     "a * b = c;\n",
			refusal: []string{"test.c:1:1: " + notAType("a")},
		},
		{
			name:    "a made-up return type",
			src:     "widget f(void) { }\n",
			refusal: []string{"test.c:1:1: " + notAType("widget")},
		},
		{
			name:    "a made-up parameter type",
			src:     "void f(widget w) { }\n",
			refusal: []string{"test.c:1:8: " + notAType("widget")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree, diags, err := tsparse.Parse("test.c", tt.src)
			if err != nil {
				t.Fatalf("tsparse.Parse failed: %v", err)
			}
			if tt.accepted != (len(tt.refusal) == 0) {
				t.Fatalf("the row says the language %s this program and writes %d refusals", readOrRefused(tt.accepted), len(tt.refusal))
			}
			if tt.accepted {
				if len(diags) != 0 {
					t.Fatalf("refused a program MicroC reads:\n%s", diags)
				}
				checkEveryPosition(t, tree)
				return
			}
			if got := renderedDiags(diags); !slices.Equal(got, tt.refusal) {
				t.Errorf("reported\n\t%q\nwant\n\t%q", got, tt.refusal)
			}
		})
	}
}

// renderedDiags is a diagnostic list as a reader is shown it, which is what
// every expectation in these tests is written against: "position: message" is
// one string, and a message reported in the wrong place is as wrong as the
// wrong message.
func renderedDiags(diags source.DiagnosticList) []string {
	out := make([]string, len(diags))
	for i, d := range diags {
		out[i] = d.Error()
	}
	return out
}

// notAType is the sentence a name outside the closed set of type names draws
// where only a declaration can stand. It is written once because eight rows
// above name it and the list of types inside it is generated.
func notAType(name string) string {
	return "'" + name + "' is not a type in MicroC, whose types are bool, dev, double, long long, void; " +
		"MicroC has no typedef, so nothing else becomes one"
}

func readOrRefused(read bool) string {
	if read {
		return "reads"
	}
	return "refuses"
}

// microCPaths returns every MicroC program under [microCRoots]. The walk is
// held to finding all of the shipped corpus, since a root that stopped
// matching would leave a comparison over the handful of refusals and pass.
func microCPaths(t *testing.T) []string {
	t.Helper()
	var paths []string
	for _, root := range microCRoots(t) {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".c") {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	programs, err := corpus.Programs()
	if err != nil {
		t.Fatalf("loading the corpus: %v", err)
	}
	if len(paths) <= len(programs) {
		t.Fatalf("the walk found %d programs and the corpus alone holds %d", len(paths), len(programs))
	}
	return paths
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(src)
}

// fileShape renders a whole translation unit, one declaration per line.
func fileShape(t *testing.T, f *ast.File) string {
	t.Helper()
	if f == nil {
		t.Fatal("Parse returned a nil file")
	}
	shapes := make([]string, len(f.Decls))
	for i, d := range f.Decls {
		shapes[i] = declShape(t, d)
	}
	return strings.Join(shapes, "\n")
}
