package tsparse

import (
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/source"
	"github.com/greg2010/ic11c/internal/tsnode"
)

// Each case asserts the rendered diagnostics rather than the messages, since
// "position: message" is what a reader is shown, and every position is
// recounted from the source by [lineColumn] so a case cannot pass on a line
// and column the converter derived the same wrong way twice.

type syntaxCase struct {
	name string
	src  string
	want []string
}

var syntaxCases = []syntaxCase{
	{
		name: "missing ';' between declarations",
		src:  "long long a = 1\nlong long b = 2;\n",
		want: []string{"test.c:1:16: expected ';', found 'long'"},
	},
	{
		name: "missing ';' after a declaration in a block",
		src:  "void f(void) {\n\tlong long a = 1\n\ta = 2;\n}\n",
		want: []string{"test.c:2:17: expected ';', found 'a'"},
	},
	{
		name: "missing ';' after an expression statement",
		src:  "void f(void) {\n\ta = 1\n\ta = 2;\n}\n",
		want: []string{"test.c:2:7: expected ';', found 'a'"},
	},
	{
		name: "missing ';' after a return",
		src:  "long long f(void) {\n\treturn 1\n}\n",
		want: []string{"test.c:2:10: expected ';', found '}'"},
	},
	{
		name: "missing ',' between arguments",
		src:  "void f(void) { g(1 2); }\n",
		want: []string{"test.c:1:19: expected ',', found an integer literal"},
	},
	{
		name: "missing ',' between initializers",
		src:  "constexpr long long a[2] = {1 2};\n",
		want: []string{"test.c:1:30: expected ',', found an integer literal"},
	},
	{
		// The grammar wraps this literal because of the suffix the lexer
		// refused, not because of a missing separator — the declaration's own
		// separator is already written behind it. Without checking for that,
		// this would answer "expected ';', found ';'" instead.
		name: "a literal the lexer refused with its terminator behind it",
		src:  "long long a = 1long ;\n",
		want: []string{
			"test.c:1:15: an integer literal is not expected here",
			`test.c:1:16: invalid suffix "long" on integer literal`,
		},
	},
	{
		name: "missing ')' closing a call",
		src:  "void f(void) {\n\tg(1;\n}\n",
		want: []string{"test.c:2:5: expected ')', found ';'"},
	},
	{
		name: "missing ')' closing an if condition",
		src:  "void f(void) {\n\tif (1 { }\n}\n",
		want: []string{"test.c:2:7: expected ')', found '{'"},
	},
	{
		name: "missing '}' closing a function body",
		src:  "void f(void) {\n\tlong long a = 1;\n",
		want: []string{"test.c:3:1: expected '}', found end of file"},
	},
	{
		name: "missing ']' closing a subscript",
		src:  "long long a[2];\nvoid f(void) { a[0 = 1; }\n",
		want: []string{"test.c:2:23: expected ']', found ';'"},
	},
	{
		name: "missing ';' before a second expression",
		src:  "long long f(void) { return a b; }\n",
		want: []string{"test.c:1:29: expected ';', found 'b'"},
	},
	{
		name: "a closer pairing with the wrong opener",
		src:  "long long a[2) = {0, 0};\n",
		want: []string{"test.c:1:14: expected ']' to close the '[' at 1:12, found ')'"},
	},
	{
		name: "a call taking the closer of the block around it",
		src:  "void f(void) { g(1 2; }\n",
		want: []string{"test.c:1:23: expected ')' to close the '(' at 1:17, found '}'"},
	},
	{
		name: "missing type on a qualified declaration",
		src:  "const x = 1;\n",
		want: []string{"test.c:1:7: expected a type, found 'x'"},
	},
	{
		name: "a name that is not a type",
		src:  "widget w = 1;\n",
		want: []string{"test.c:1:1: 'widget' is not a type in MicroC, whose types are bool, dev, double, long long, void; MicroC has no typedef, so nothing else becomes one"},
	},
	{
		// Inside a function the same source declares nothing: MicroC has no
		// typedef, so a name outside the closed set opens no declaration and
		// what is written is two expressions with no terminator between them.
		name: "two names where a statement belongs",
		src:  "void f(void) { widget w = 1; }\n",
		want: []string{"test.c:1:22: expected ';', found 'w'"},
	},
	{
		name: "missing declarator",
		src:  "long long = 1;\n",
		want: []string{"test.c:1:11: expected a declarator, found '='"},
	},
	{
		name: "operator left without an operand",
		src:  "void f(void) { long long a = 1 + ; }\n",
		want: []string{"test.c:1:32: expected an expression after '+'"},
	},
	{
		name: "unclosed parenthesis inside an initializer",
		src:  "void f(void) { long long a = (1 + 2; }\n",
		want: []string{"test.c:1:36: expected ')', found ';'"},
	},
	{
		name: "a statement where a declaration belongs",
		src:  "if (1) { }\nvoid tick(void) { }\n",
		want: []string{"test.c:1:1: expected a declaration; a statement is only valid inside a function body"},
	},
	{
		name: "an assignment where a declaration belongs",
		src:  "a = 1;\nvoid tick(void) { }\n",
		want: []string{"test.c:1:1: expected a declaration; a statement is only valid inside a function body"},
	},
	{
		name: "a closer with nothing open",
		src:  "}{)(\n",
		want: []string{"test.c:1:1: expected a declaration, found '}'"},
	},
	{
		name: "an opener the file never closes",
		src:  "long long f(\n",
		want: []string{"test.c:1:12: unclosed '('; no matching ')' before end of file"},
	},
	{
		name: "a comment the file never closes",
		src:  "long long a = 1;\n/* nope\n",
		want: []string{"test.c:2:1: unterminated block comment"},
	},
}

// juxtaposedCases are the programs that write one expression against
// another with nothing joining them, with an assignment operator behind the
// second. parens.go's rewrite must not fire here: parenthesizing the second
// would turn the juxtaposition into a call, a well-formed tree nobody wrote.
var juxtaposedCases = []syntaxCase{
	{
		name: "a literal against a name",
		src:  "void f(long long a) { a 1 = 2; }\n",
		want: []string{"test.c:1:24: expected ';', found an integer literal"},
	},
	{
		name: "a character literal against a name",
		src:  "void f(long long a) { a 'x' = 2; }\n",
		want: []string{"test.c:1:23: expected a statement, found 'a'"},
	},
	{
		name: "a literal against a name behind a compound assignment",
		src:  "void f(long long a) { a 1 += 2; }\n",
		want: []string{"test.c:1:24: expected ';', found an integer literal"},
	},
	{
		name: "a literal against a subscript",
		src:  "long long a[2];\nvoid f(void) { a[0] 1 = 2; }\n",
		want: []string{"test.c:2:20: expected ';', found an integer literal"},
	},
	{
		name: "a literal against a call",
		src:  "long long g(long long);\nvoid f(long long x) { g(x) 1 = 2; }\n",
		want: []string{"test.c:2:28: expected a declarator, found an integer literal"},
	},
	{
		name: "a literal against a group",
		src:  "void f(long long a) { (a) 1 = 2; }\n",
		want: []string{"test.c:1:26: expected ';', found an integer literal"},
	},
	{
		name: "a literal against a postfix increment",
		src:  "void f(long long a) { a++ 1 = 2; }\n",
		want: []string{"test.c:1:23: expected a statement, found 'a'"},
	},
	{
		name: "two string literals",
		src:  "void f(void) { \"a\" \"b\" = 1; }\n",
		want: []string{"test.c:1:16: adjacent string literals are not joined in MicroC; write one literal"},
	},
}

// operandCases are the programs that write a literal or a postfix increment
// where an assignment's target belongs, which the rewrite [juxtaposedCases]
// must not be confused with: each is a program MicroC reads, so nothing
// should be said about it, and the rewrite firing here would refuse it.
var operandCases = []string{
	"void f(long long x, long long a) { if (x) 1 = a; }\n",
	"void f(long long x, long long a) { while (x) 1 = a; }\n",
	"void f(long long a) { for (;;) 1 = a; }\n",
	"void f(long long x, long long a) { if (x) a++ = 1; }\n",
	"void f(double d) { (long long)1 = d; }\n",
	"void f(double d) { (long long)d++ = 1; }\n",
	"void f(long long a) { ++a++ = 1; }\n",
	"void f(long long a) { --a-- >>= 1; }\n",
	"void f(long long a) { a; 1 = a; }\n",
	"void f(long long a, long long b) { a + 1 = 2; }\n",
	"long long g(long long);\nvoid f(long long a) { long long z = g(1 = a); }\n",
}

// TestAnOperandIsNotAJuxtaposition holds the two halves of the rewrite apart. A
// program in [operandCases] that draws a diagnostic is one this front end has
// started refusing and the other still reads; the rows in [juxtaposedCases] are
// the other direction and run with the rest of the syntax cases.
func TestAnOperandIsNotAJuxtaposition(t *testing.T) {
	for _, src := range operandCases {
		t.Run(src, func(t *testing.T) {
			_, diags, err := Parse("test.c", src)
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			if len(diags) != 0 {
				t.Errorf("refused a program MicroC reads:\n%s", diags)
			}
		})
	}
}

// prefabAttrCases cover the four positions a prefab attribute is admitted in,
// each written with one bracket missing. The attribute is the one construct
// MicroC reads and C does not, so a malformed one is where the two grammars are
// furthest apart and where a front end is most easily wrong.
var prefabAttrCases = []syntaxCase{
	{
		name: "malformed prefab attribute at file scope",
		src:  "[[ic11c::prefab(\"X\")] const dev d = d0;\nvoid tick(void) { }\n",
		want: []string{"test.c:1:1: " + unknownAttrMsg},
	},
	{
		name: "malformed prefab attribute in a block",
		src:  "void f(void) { [[ic11c::prefab(\"X\")] const dev d = d0; }\nvoid tick(void) { }\n",
		want: []string{"test.c:1:16: " + unknownAttrMsg},
	},
	{
		name: "malformed prefab attribute on a parameter",
		src:  "void f([[ic11c::prefab(\"X\")] dev d) { }\nvoid tick(void) { }\n",
		want: []string{"test.c:1:8: " + unknownAttrMsg},
	},
	{
		// The grammar spells '[[' as one lexeme and lexes it wherever two
		// brackets are written together, so a subscript of a bracketed
		// expression reaches the same dispatch as an attribute does. Naming the
		// attribute here would answer a program that has none.
		name: "two brackets that are no attribute",
		src:  "long long a[2];\nvoid f(void) { a[[0]]; }\n",
		want: []string{"test.c:2:17: expected a declarator, found '['"},
	},
	{
		name: "two brackets that are no attribute inside an initializer",
		src:  "void f(void) { long long b = a[[0]]; }\n",
		want: []string{"test.c:1:31: '[' is not expected here"},
	},
	{
		name: "malformed prefab attribute in a for init",
		src:  "void f(void) { for ([[ic11c::prefab(\"X\")] long long i = 0; i < 2; i++) { } }\nvoid tick(void) { }\n",
		want: []string{"test.c:1:21: " + unknownAttrMsg},
	},
}

func TestSyntaxDiagnostics(t *testing.T) {
	for _, tt := range slices.Concat(syntaxCases, prefabAttrCases, juxtaposedCases) {
		t.Run(tt.name, func(t *testing.T) {
			_, diags, err := Parse("test.c", tt.src)
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			checkRendered(t, diags, tt.want)
			checkPositions(t, "test.c", tt.src, diags)
		})
	}
}

// TestDiagnosticsAreOrderedAndCapped holds the list to the shape a caller reads
// it in, which must not say which front end built it: one severity, source
// order, and a cut made once at the end with the note at the first problem
// withheld.
func TestDiagnosticsAreOrderedAndCapped(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "every problem the converter found",
			src:  strings.Repeat("struct S { long long a; };\n", 200),
		},
		{
			// The budget covers both phases. A file the lexer has a great deal
			// to say about and the converter has more would spend two budgets
			// of one size if each phase were capped on its own, and the note
			// closing the list would sit above diagnostics it claims are below.
			name: "the lexer's and the converter's together",
			src: strings.Repeat("constexpr long long k = 010;\n", 40) +
				strings.Repeat("struct S { long long a; };\n", 40),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags, err := Parse("test.c", tt.src)
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			if len(diags) != maxDiagnostics+1 {
				t.Fatalf("got %d diagnostics, want %d and the note that the list was cut", len(diags), maxDiagnostics+1)
			}
			last := diags[len(diags)-1]
			if last.Msg != "too many errors" {
				t.Errorf("the list does not close with a note that it was cut short: %s", last)
			}
			if last.Pos.Compare(diags[len(diags)-2].Pos) <= 0 {
				t.Errorf("the note sits at %s, which is not past the last diagnostic shown", last.Pos)
			}
			if !slices.IsSortedFunc(diags, func(a, b source.Diagnostic) int { return a.Pos.Compare(b.Pos) }) {
				t.Error("diagnostics are not in source order")
			}
			checkPositions(t, "test.c", tt.src, diags)
		})
	}
}

// TestTheCapCutsAtTheProblemPastIt pins where the list is cut rather than how
// long the budget is: a file at exactly the budget is reported whole with no
// note, and one problem past it is cut to the budget and closed with the note.
func TestTheCapCutsAtTheProblemPastIt(t *testing.T) {
	const problem = "struct S { long long a; };\n"
	tests := []struct {
		name     string
		problems int
		want     int
		cut      bool
	}{
		{name: "one short of the cap", problems: maxDiagnostics - 1, want: maxDiagnostics - 1},
		{name: "exactly the cap", problems: maxDiagnostics, want: maxDiagnostics},
		{name: "one past the cap", problems: maxDiagnostics + 1, want: maxDiagnostics + 1, cut: true},
		{name: "two past the cap", problems: maxDiagnostics + 2, want: maxDiagnostics + 1, cut: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := strings.Repeat(problem, tt.problems)
			_, diags, err := Parse("test.c", src)
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			if len(diags) != tt.want {
				t.Fatalf("%d problems drew %d diagnostics, want %d:\n%s", tt.problems, len(diags), tt.want, diags)
			}
			cut := diags[len(diags)-1].Msg == "too many errors"
			if cut != tt.cut {
				t.Errorf("%d problems drew a list that %s cut short, want the other:\n%s", tt.problems, wasOrWasNot(cut), diags)
			}
		})
	}
}

// TestTheNoteClosingTheListIsNotCounted holds the number a run reports to
// the number it printed. The note saying the list was cut is a limit the
// compiler imposes on itself, not a problem with the program, so a caller
// counting errors must pass over it.
func TestTheNoteClosingTheListIsNotCounted(t *testing.T) {
	src := strings.Repeat("struct S { long long a; };\n", maxDiagnostics+6)
	_, diags, err := Parse("test.c", src)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(diags) != maxDiagnostics+1 {
		t.Fatalf("got %d diagnostics, want %d and the note that the list was cut", len(diags), maxDiagnostics+1)
	}
	if last := diags[len(diags)-1]; !last.Overflow {
		t.Errorf("the note closing the list is not marked as one: %+v", last)
	}
	if got := diags.Errors(); got != maxDiagnostics {
		t.Errorf("Errors() = %d, want %d", got, maxDiagnostics)
	}
	if !diags.HasErrors() {
		t.Error("HasErrors() = false over a list of errors")
	}
}

func wasOrWasNot(cut bool) string {
	if cut {
		return "was"
	}
	return "was not"
}

// TestRecoveryContinuesPastTheMistake covers three programs a resynchronizing
// parse gets wrong. Each holds one mistake and declarations behind it, and what
// is asserted is that the mistake costs a bounded number of messages and that
// what follows it is still read.
func TestRecoveryContinuesPastTheMistake(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		want      []string
		wantDecls []string
	}{
		{
			name: "a prefab attribute closed with one bracket",
			src: "void f([[ic11c::prefab(\"StructureGasSensor\")] ) { }\n" +
				"void h(void) { totally_undeclared_thing = 1; }\n" +
				"void main(void) { h(); }\n",
			want:      []string{"test.c:1:8: " + unknownAttrMsg},
			wantDecls: []string{"f", "h", "main"},
		},
		{
			name: "an unclosed call around a refused construct",
			src: "long long a = g(sizeof((b);\n" +
				"long long c = 1;\n" +
				"void tick(void) { }\n",
			want: []string{
				"test.c:1:16: unclosed '('; no matching ')' before end of file",
				"test.c:1:17: sizeof is not supported in MicroC",
			},
			// 'a' is the declaration the mistake is in, so it is the one that
			// does not survive; what matters is that the two behind it do.
			wantDecls: []string{"c", "tick"},
		},
		{
			// The brace swallows the definition behind it, so nothing after the
			// struct survives as a declaration. Naming the opener is the whole
			// answer available: a parse cannot discard a token.
			name: "a struct whose brace is never closed",
			src:  "struct S { long long q;\nvoid tick(void) { }\n",
			want: []string{
				"test.c:1:1: structs are not supported in MicroC",
				"test.c:1:10: unclosed '{'; no matching '}' before end of file",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, diags, err := Parse("test.c", tt.src)
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			checkRendered(t, diags, tt.want)
			checkPositions(t, "test.c", tt.src, diags)
			if got := declNames(f); !slices.Equal(got, tt.wantDecls) {
				t.Errorf("read declarations %q, want %q", got, tt.wantDecls)
			}
		})
	}
}

// refusalSamples writes each construct MicroC excludes, so that the sentence
// prepared for it is shown to be one a programmer can actually draw. A message
// no input reaches is a message that cannot be wrong and cannot be right.
var refusalSamples = map[tsnode.Kind]string{
	tsnode.KindAlignasQualifier:          "_Alignas(8) long long a;\n",
	tsnode.KindAlignofExpression:         "long long a = _Alignof(long long);\n",
	tsnode.KindAttributedDeclarator:      "long long a [[gnu::unused]];\n",
	tsnode.KindAttributedStatement:       "void f(void) { [[gnu::unused]] x = 1; }\n",
	tsnode.KindCommaExpression:           "void f(long long a) { a = (1, 2); }\n",
	tsnode.KindCompoundLiteralExpression: "long long f(void) { return (long long){1}; }\n",
	tsnode.KindConcatenatedString:        "long long f(void) { return __ic_hash(\"a\" \"b\"); }\n",
	tsnode.KindEnumSpecifier:             "enum E { A };\n",
	tsnode.KindFieldExpression:           "long long f(long long a) { return a.b; }\n",
	tsnode.KindGenericExpression:         "long long a = _Generic(1, long long: 1);\n",
	tsnode.KindGotoStatement:             "void f(void) { goto done; }\n",
	tsnode.KindInitializerPair:           "constexpr long long a[2] = {[0] = 1};\n",
	tsnode.KindLabeledStatement:          "void f(void) { top: ; }\n",
	tsnode.KindLinkageSpecification:      "extern \"C\" { long long a; }\n",
	tsnode.KindMacroTypeSpecifier:        "T(long long) a;\n",
	tsnode.KindNull:                      "long long *p = nullptr;\n",
	tsnode.KindParenthesizedDeclarator:   "long long (*f)(void);\n",
	tsnode.KindPreprocCall:               "#pragma once\n",
	tsnode.KindPreprocDef:                "#define N 1\n",
	tsnode.KindPreprocFunctionDef:        "#define F(x) x\n",
	tsnode.KindPreprocIf:                 "#if 1\n#endif\n",
	tsnode.KindPreprocIfdef:              "#ifdef X\n#endif\n",
	tsnode.KindPreprocInclude:            "#include <stdio.h>\n",
	tsnode.KindSizeofExpression:          "long long a = sizeof(long long);\n",
	tsnode.KindStorageClassSpecifier:     "static long long a;\n",
	tsnode.KindStructSpecifier:           "struct S { long long a; };\n",
	tsnode.KindTypeDefinition:            "typedef long long word;\n",
	tsnode.KindUnionSpecifier:            "union U { long long a; };\n",
}

// refusalNaming gives the sentence a construct draws when it is written with
// a given spelling, and says whether anything names it. Every construct but
// one is named by [refusals]; a storage class's sentence is built from the
// keyword it is written with and has no entry there.
func refusalNaming(construct tsnode.Kind, spelling string) (string, bool) {
	if construct == tsnode.KindStorageClassSpecifier {
		return storageClassMsg(spelling), true
	}
	msg, named := refusals[construct]
	return msg, named
}

func TestEveryRefusalIsReachable(t *testing.T) {
	for kind, src := range refusalSamples {
		t.Run(string(kind), func(t *testing.T) {
			msg, named := refusalNaming(kind, "static")
			if !named {
				t.Fatalf("a %s is sampled and nothing refuses it by name", kind)
			}
			_, diags, err := Parse("test.c", src)
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			checkPositions(t, "test.c", src, diags)

			i := slices.IndexFunc(diags, func(d source.Diagnostic) bool { return d.Msg == msg })
			if i < 0 {
				t.Fatalf("%q does not draw %q:\n%s", src, msg, diags)
			}
			if at := diags[i].Pos; src[at.Offset:] == "" || strings.HasPrefix(src[at.Offset:], "\n") {
				t.Errorf("the message is reported at %s, which is the end of a line rather than the construct", at)
			}
		})
	}
	for kind := range refusals {
		if _, written := refusalSamples[kind]; !written {
			t.Errorf("no sample writes a %s, so nothing shows its message is one a programmer can draw", kind)
		}
	}
}

// TestEveryRefusedTokenIsReachable holds each keyword row to the sentence it
// promises. The keyword written alone is the sample for every row: a keyword
// the grammar cannot build a construct around is what leaves it loose inside
// an error node, the only place these rows are read.
func TestEveryRefusedTokenIsReachable(t *testing.T) {
	for token, construct := range refusedTokens {
		t.Run(string(token), func(t *testing.T) {
			msg, refused := refusalNaming(construct, string(token))
			if !refused {
				t.Fatalf("%s is read as a %s, which nothing refuses by name, so the row reports nothing", token, construct)
			}
			src := string(token)
			_, diags, err := Parse("test.c", src)
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			checkPositions(t, "test.c", src, diags)

			i := slices.IndexFunc(diags, func(d source.Diagnostic) bool { return d.Msg == msg })
			if i < 0 {
				t.Fatalf("%q does not draw %q:\n%s", src, msg, diags)
			}
			if got := diags[i].Pos.Offset; got != 0 {
				t.Errorf("the message is reported at offset %d rather than at the keyword", got)
			}
		})
	}
}

// checkPositions holds every diagnostic to a place the reader can find. A byte
// offset with no line and column, or a line past the end of the file, names
// nothing a programmer can look at.
func checkPositions(t *testing.T, file, src string, diags source.DiagnosticList) {
	t.Helper()
	for _, d := range diags {
		if d.Severity != source.Error {
			t.Errorf("%s is a %s; a syntax diagnostic rejects the program", d, d.Severity)
		}
		if d.Pos.File != file {
			t.Errorf("%s names the file %q, want %q", d, d.Pos.File, file)
		}
		if d.Pos.Offset < 0 || d.Pos.Offset > len(src) {
			t.Errorf("%s sits at offset %d, outside a source of %d bytes", d, d.Pos.Offset, len(src))
			continue
		}
		line, column := lineColumn(src, d.Pos.Offset)
		if d.Pos.Line != line || d.Pos.Column != column {
			t.Errorf("%s reports %d:%d, but offset %d is at %d:%d",
				d, d.Pos.Line, d.Pos.Column, d.Pos.Offset, line, column)
		}
	}
}

// lineColumn counts a byte offset to a line and column independently of the
// converter, so a shared mistake in the counting cannot pass.
func lineColumn(src string, offset int) (int, int) {
	line, start := 1, 0
	for i := range offset {
		if src[i] == '\n' {
			line++
			start = i + 1
		}
	}
	return line, offset - start + 1
}

func checkRendered(t *testing.T, got source.DiagnosticList, want []string) {
	t.Helper()
	if lines := rendered(got); !slices.Equal(lines, want) {
		t.Errorf("reported\n\t%q\nwant\n\t%q", lines, want)
	}
}

func rendered(diags source.DiagnosticList) []string {
	lines := make([]string, len(diags))
	for i, d := range diags {
		lines[i] = d.Error()
	}
	return lines
}

// declNames names each declaration that survived the parse, which is what says
// whether the text behind a mistake was still read.
func declNames(f *ast.File) []string {
	var names []string
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			names = append(names, d.Name)
		case *ast.VarDecl:
			names = append(names, d.Name)
		}
	}
	return names
}
