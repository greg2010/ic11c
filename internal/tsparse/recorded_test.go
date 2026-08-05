package tsparse_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/tsparse"
)

// What a front end says about a program it will not read is a decision, and
// a decision nobody wrote down is indistinguishable from a defect. Each
// program below carries the sentence it draws and the reason it was chosen
// over the obvious C reading; diagnostics_test.go covers the sentences alone.

// recorded is one program and the answer this front end is held to for it.
type recorded struct {
	name string
	src  string
	// want is what the front end reports, in full and in order.
	want []string
	// why is the decision the row records. It is a field rather than a comment
	// so that a row cannot be added without one.
	why string
}

// decidedWording are the programs whose sentence was chosen over the one C's own
// reading would have produced.
var decidedWording = []recorded{
	{
		name: "a block the file never closes",
		src:  "void f(void) { a = ",
		want: []string{"test.c:1:14: unclosed '{'; no matching '}' before end of file"},
		why: "A brace that never closes is one mistake, and everything the parse " +
			"noticed afterwards is that mistake seen further on. Naming the opener " +
			"sends the reader to the byte they have to fix; the three sentences " +
			"behind it name the end of the file three times over, which is where " +
			"the parse gave up rather than where the programmer went wrong. This " +
			"holds while the grammar can still read the source around the opener. " +
			"Past enough unclosed openers its recovery stops building the nesting " +
			"at all and answers with a flat region of loose tokens, which costs a " +
			"sentence each; see " +
			"[TestABraceTheFileNeverClosesNamesTheOpenerUntilTheGrammarStops].",
	},
	{
		name: "a declaration the file never terminates",
		src:  "long long a = ",
		want: []string{"test.c:1:1: expected a declaration, found 'long'"},
		why: "The same decision one construct out. The grammar cannot read the " +
			"declaration at all once the source stops mid-initializer, so what it " +
			"has to say is about the declaration rather than about each token the " +
			"file does not reach.",
	},
	{
		name: "a source ending without its terminator",
		src:  "long long a = 1 ",
		want: []string{"test.c:1:17: expected ';', found end of file"},
		why: "A sentence about the end of the file is reported at the end of the " +
			"file, past the trailing space rather than at the byte the terminator " +
			"belonged in front of. A reader sent to the latter is shown a byte that " +
			"reads as the last token rather than as the hole.",
	},
	{
		name: "nullptr",
		src:  "long long *p = nullptr;\n",
		want: []string{"test.c:1:16: nullptr is not supported in MicroC; a pointer names a slot and has no null"},
		why: "A programmer who wrote nullptr wanted a null pointer, and being told " +
			"the word is reserved leaves them looking for another spelling of one. " +
			"MicroC has no null pointer to spell, which is the sentence that ends " +
			"the search.",
	},
	{
		name: "a compound literal",
		src:  "long long a = (long long){1};\n",
		want: []string{"test.c:1:15: compound literals are not supported in MicroC"},
		why: "The construct has a name and the programmer used it on purpose. " +
			"Naming it says what to remove; naming the brace says the parse stopped " +
			"there, which is true of every construct the language excludes.",
	},
	{
		name: "a designated initializer",
		src:  "long long a[2] = {[0] = 1};\n",
		want: []string{"test.c:1:19: a designated initializer is not supported in MicroC"},
		why:  "The same decision as the compound literal, at the same byte.",
	},
	{
		name: "adjacent string literals",
		src:  "void f(void) { \"a\" \"b\"; }\n",
		want: []string{"test.c:1:16: adjacent string literals are not joined in MicroC; write one literal"},
		why: "C joins two string literals written side by side and MicroC does not, " +
			"so the programmer wrote something C admits rather than something " +
			"nobody meant. The pair is the construct, so the sentence sits where " +
			"the pair begins.",
	},
	{
		name: "a label",
		src:  "void f(void) { top: ; }\n",
		want: []string{"test.c:1:16: a label is not supported in MicroC, which has no goto to reach one"},
		why: "The same decision, and the position moves with it: a reader told that " +
			"a terminator was owed at the colon will write one and get a second " +
			"refusal, where a reader told the label is excluded deletes the label.",
	},
	{
		name: "the comma operator inside parentheses",
		src:  "void f(long long a) { a = (1, 2); }\n",
		want: []string{"test.c:1:29: the comma operator is not supported in MicroC"},
		why: "One comma is one mistake, and it draws one sentence. Reading the " +
			"group as closed at the comma instead would report again at the " +
			"parenthesis that then has nothing to close, which is the first " +
			"sentence seen twice.",
	},
	{
		name: "a declaration whose initializer is missing",
		src:  "long long a =   ;\n",
		want: []string{"test.c:1:13: expected an expression after '='"},
		why: "The operator that wanted the value is what the reader has to write " +
			"behind, and it is one position however far the terminator ended up " +
			"from it. It is also what the same front end says about \"a = ;\", and " +
			"one mistake written two ways must not draw two sentences.",
	},
	{
		name: "a declarator writing two parameter lists",
		src:  "long long f(void)(void);\n",
		want: []string{"test.c:1:18: a declarator names one array or one parameter list in MicroC, and nothing outside it"},
		why: "C would say a terminator was owed at that byte. MicroC excludes the " +
			"construct outright, and saying so is what tells the programmer that " +
			"no terminator makes the declarator legal.",
	},
	{
		name: "a statement at file scope",
		src:  ";\n",
		want: []string{"test.c:1:1: expected a declaration; a statement is only valid inside a function body"},
		why: "A file scope holds declarations, and a type is only the first thing " +
			"one is written with. Naming the declaration says what belongs there; " +
			"naming the type says what the parse was reading when it stopped.",
	},
	{
		name: "a token the C grammar reads as two",
		src:  "bool f(bool a, bool b) { return a || && !a; }\n",
		want: []string{"test.c:1:38: '&&' is not expected here"},
		why: "The sentence names the byte the '&&' was written at and says no more " +
			"than that the grammar scanned one token as two. That happens wherever " +
			"a whole lexeme will not fit and is as true of a declaration as of an " +
			"expression, so a sentence naming what was expected instead would have " +
			"to guess it. See relexed.go.",
	},
	{
		name: "a type specifier C has and MicroC does not",
		src:  "unsigned long long a = 1;\n",
		want: []string{"test.c:1:1: 'unsigned long long' is not a type in MicroC, whose types are bool, dev, double, long long, void; MicroC has no typedef, so nothing else becomes one"},
		why: "The whole spelling is what the programmer wrote and it is a type name " +
			"in C, so quoting it names something they can look at, and the list of " +
			"types behind it names what to write instead. Naming the one word is " +
			"shorter and says nothing about the replacement.",
	},
}

// rewriteGaps are the programs MicroC admits and this front end does not
// compile, for a reason the rewriting in this package cannot reach — not a
// decision about a sentence, just what the compiler does today. A row that
// stops being refused fails, taking it out of the file when the gap closes.
var rewriteGaps = []recorded{
	{
		name: "an attribute whose brackets are written apart",
		src:  "[ [ic11c::prefab(\"StructureGasSensor\")]] const dev d = d0;\n",
		want: []string{
			"test.c:1:1: expected a declaration, found '['",
			"test.c:1:4: a label is not supported in MicroC, which has no goto to reach one",
			"test.c:1:9: '::' is not expected here",
			"test.c:1:10: 'prefab' is not expected here",
		},
		why: "C23 6.7.11.1 spells an attribute with two '[' tokens, so whitespace " +
			"between them changes nothing and internal/lexer reads it that way. " +
			"The C grammar spells the pair as one lexeme and cannot lex them " +
			"apart, so the compiler does not build this program. Every rewrite " +
			"this package makes writes bytes in, and closing a gap between two the " +
			"source already has would mean taking bytes out, which is a second " +
			"direction for every offset in the file to be mapped through. Nothing " +
			"shipped writes an attribute this way, which is why the cost was " +
			"accepted rather than paid. The third sentence is the grammar reading " +
			"the '::' as two colons once the " +
			"attribute is gone, which is the same lexeme-splitting relexed.go " +
			"reports everywhere else.",
	},
	{
		name: "an attribute whose closing brackets are written apart",
		src:  "[[ic11c::prefab(\"StructureGasSensor\")] ] const dev d = d0;\n",
		want: []string{"test.c:1:1: " + unknownAttr},
		why:  "The same gap at the other end of the attribute.",
	},
}

// recordedDefects are the programs answered at a byte that has nothing to
// do with the mistake: the program is refused, so nothing downstream is
// misled, but the position comes from the grammar's own recovery, which
// this package reports rather than second-guesses.
var recordedDefects = []recorded{
	{
		name: "a string literal the file never closes inside a call",
		src:  "long long a = __ic_hash(\"abc);\nlong long b = 2;\n",
		want: []string{
			"test.c:1:25: unterminated string literal",
			"test.c:1:31: expected '\"', found 'long'",
			"test.c:2:12: expected ')', found '='",
		},
		why: "The third sentence sits on the '=' of the declaration on line 2, " +
			"which is written correctly and has nothing to do with the call that " +
			"was never closed. The grammar reads a string literal across lines " +
			"where internal/lexer ends one at its own, so it carries the call as " +
			"far as the second declaration before it runs out of somewhere to put " +
			"the ')', and the missing node it answers with is where it stopped " +
			"rather than where the ')' belonged.",
	},
}

// TestABraceTheFileNeverClosesNamesTheOpenerUntilTheGrammarStops holds the
// decision recorded above to the source it is true of: one sentence naming
// the opener, until the grammar's own recovery gives up past enough unclosed
// openers and answers with a flat region of loose tokens, one sentence each.
func TestABraceTheFileNeverClosesNamesTheOpenerUntilTheGrammarStops(t *testing.T) {
	const opener = "test.c:1:14: unclosed '{'; no matching '}' before end of file"
	for _, depth := range []int{2, 3, 8, 13} {
		src := "void f(void) " + strings.Repeat("{", depth)
		_, diags, err := tsparse.Parse("test.c", src)
		if err != nil {
			t.Fatalf("tsparse.Parse failed: %v", err)
		}
		if got := renderedDiags(diags); !slices.Equal(got, []string{opener}) {
			t.Errorf("%d unclosed braces report\n\t%q\nwant only\n\t%q", depth, got, opener)
		}
	}
	src := "void f(void) " + strings.Repeat("{", 40)
	_, diags, err := tsparse.Parse("test.c", src)
	if err != nil {
		t.Fatalf("tsparse.Parse failed: %v", err)
	}
	if len(diags) < 2 {
		t.Fatalf("40 unclosed braces report %q; the recovery no longer gives up, so the row above now holds for every depth and should say so", renderedDiags(diags))
	}
	if !slices.Contains(renderedDiags(diags), opener) {
		t.Errorf("40 unclosed braces report\n\t%q\nwhich no longer names the opener at all", renderedDiags(diags))
	}
}

// unknownAttr is the sentence an attribute MicroC does not recognize draws. It
// is spelled out here rather than read from the package, which these tests
// cannot reach, and internal/tsparse/diagnostics_test.go holds the two to each
// other.
const unknownAttr = "the only attribute MicroC recognizes is [[ic11c::prefab(\"PrefabName\")]]" +
	", which states the prefab a dev declaration's pin is wired to"

// TestRecordedAnswers holds the front end to what this file records about it,
// and holds each row to carrying the argument that put it here.
func TestRecordedAnswers(t *testing.T) {
	for _, tt := range slices.Concat(decidedWording, rewriteGaps, recordedDefects) {
		t.Run(tt.name, func(t *testing.T) {
			if tt.why == "" {
				t.Fatal("the row records no reason, so nothing distinguishes it from a defect")
			}
			if len(tt.want) == 0 {
				t.Fatal("the row records no refusal, so it belongs with the programs the front end reads")
			}
			_, diags, err := tsparse.Parse("test.c", tt.src)
			if err != nil {
				t.Fatalf("tsparse.Parse failed: %v", err)
			}
			if got := renderedDiags(diags); !slices.Equal(got, tt.want) {
				t.Errorf("now reports\n\t%q\nwhere the record says\n\t%q", got, tt.want)
			}
		})
	}
}
