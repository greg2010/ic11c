package tsparse

import (
	"testing"

	"github.com/greg2010/ic11c/internal/source"
)

// These tests hold the front end to what it says about a bracket the source
// never pairs: a parse cannot discard a token, so an opener whose closer
// never comes takes a later construct's, and the region the grammar could
// not read grows until it has swallowed that construct whole.

func TestUnpairedBracketsAreReported(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "initializer brace swallows the next definition",
			src: `long long a[2] = {1, {2, 3;
void tick(void) { }
`,
			want: []string{"t.c:1:18: unclosed '{'; no matching '}' before end of file"},
		},
		{
			name: "parameter bracket swallows the next definition",
			src: `void f(unsigned x[2;
void tick(void) { }
`,
			want: []string{"t.c:1:7: unclosed '('; no matching ')' before end of file"},
		},
		{
			name: "struct brace swallows the next definition",
			src: `struct S { long long q;
void tick(void) { }
`,
			want: []string{
				"t.c:1:1: structs are not supported in MicroC",
				"t.c:1:10: unclosed '{'; no matching '}' before end of file",
			},
		},
		{
			// Only the outer opener is named: the second brace is inside the
			// region the first one made, and is the same mistake seen further in.
			name: "two openers the file never closes",
			src: `void f(void) {
void g(void) {
`,
			want: []string{"t.c:1:14: unclosed '{'; no matching '}' before end of file"},
		},
		{
			// The grammar answered this one with a missing node of its own, which
			// names the token and sits where it belonged, so the prepass is not
			// consulted.
			name: "inner opener closes and outer does not",
			src: `void tick(void) {
    if (x) { }
`,
			want: []string{"t.c:3:1: expected '}', found end of file"},
		},
		{
			name: "bracket closed by a paren",
			src: `long long a[2) = {0, 0};
`,
			want: []string{"t.c:1:14: expected ']' to close the '[' at 1:12, found ')'"},
		},
		{
			name: "a mismatch and an unclosed opener behind it",
			src: `long long a[2) = {0};
void tick(void) {
`,
			want: []string{
				"t.c:1:14: expected ']' to close the '[' at 1:12, found ')'",
				"t.c:3:1: expected '}', found end of file",
			},
		},
		{
			name: "closer with nothing open",
			src: `void tick(void) { }
}
`,
			want: []string{"t.c:2:1: expected a declaration, found '}'"},
		},
		{
			name: "opener after a multi-line block comment",
			src: `/* one
 * two {
 */
void tick(void) {
`,
			want: []string{"t.c:5:1: expected '}', found end of file"},
		},
		{
			// The lexer decides what is comment, and it read the rest of the file
			// as one. A brace inside it is text, and the front end that read it as
			// text is the one that reports.
			name: "unterminated block comment hides the closer that would have paired",
			src: `void tick(void) {
/* the rest of the file is comment
}
`,
			want: []string{"t.c:2:1: unterminated block comment"},
		},
		{
			name: "unterminated string costs its own line and no more",
			src: `void tick(void) { report("oops); }
void other(void) { }
`,
			want: []string{
				"t.c:1:17: unclosed '{'; no matching '}' before end of file",
				"t.c:1:26: unterminated string literal",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags, err := Parse("t.c", tt.src)
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			checkRendered(t, diags, tt.want)
		})
	}
}

// TestBracketsInLiteralsAndCommentsDoNotCount covers the other half of the
// scan: a bracket inside a string, a character literal or either form of
// comment is text rather than structure. Every row that draws a diagnostic
// draws a lexical one about the literal, never anything about a bracket.
func TestBracketsInLiteralsAndCommentsDoNotCount(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{name: "empty source", src: ""},
		{
			name: "balanced definition",
			src: `void tick(void) { }
`,
		},
		{
			name: "braces in a string literal",
			src: `void tick(void) { report("}{"); }
`,
		},
		{
			name: "escaped quote in a string literal",
			src: `void tick(void) { report("say \"{\" now"); }
`,
		},
		{
			name: "apostrophe in a string literal",
			src: `void tick(void) { report("don't {"); }
`,
		},
		{
			name: "comment opener in a string literal",
			src: `void tick(void) { report("/* {"); }
`,
		},
		{
			name: "open brace in a character literal",
			src: `void tick(void) { if (c == '{') { } }
`,
		},
		{
			name: "close brace in a character literal",
			src: `void tick(void) { if (c == '}') { } }
`,
		},
		{
			name: "braces in a line comment",
			src: `// a stray } { here
void tick(void) { }
`,
		},
		{
			name: "apostrophe after a brace in a line comment",
			src: `// a { the housing's stack does not open
void tick(void) { }
`,
		},
		{
			name: "braces in a block comment",
			src: `/* a stray } { here */
void tick(void) { }
`,
		},
		{
			name: "apostrophe after a brace in a multi-line block comment",
			src: `/*
 * A { the housing's stack does not open, and neither does a stray ) close.
 */
void tick(void) { }
`,
		},
		{
			name: "unterminated character literal costs its own line",
			src: `long long c = '};
void tick(void) { }
`,
			want: []string{
				"t.c:1:15: unterminated character literal",
				"t.c:2:11: expected an identifier, found 'void'",
				"t.c:2:16: expected ';', found '{'",
				"t.c:2:17: expected a declaration; a statement is only valid inside a function body",
			},
		},
		{
			name: "division is not a comment",
			src: `void tick(void) { q = a / b; }
`,
		},
		{
			name: "slash at end of file",
			src:  `long long q = a /`,
			want: []string{"t.c:1:1: expected a declaration, found 'long'"},
		},
		{
			name: "backslash at end of file inside a string",
			src:  `long long q = "abc\`,
			want: []string{
				"t.c:1:15: unterminated string literal",
				"t.c:1:19: unterminated escape sequence",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags, err := Parse("t.c", tt.src)
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			checkRendered(t, diags, tt.want)
		})
	}
}

// Every finding below is measured back against the source it was made from:
// a position naming the wrong byte sends the reader somewhere they wrote
// nothing wrong, and nothing downstream can notice, since the sentence still
// reads correctly.

// crossHeads are the comment and literal forms whose brackets are not code, and
// crossTails the unpaired brackets that give each source a finding to make. A
// source whose brackets pair answers nothing, so every head is written in front
// of every tail.
var crossHeads = []string{
	"",
	"long long a = 1;\n",
	"// a stray } { here\n",
	"// trailing backslash \\\n",
	"/* a stray } { here */\n",
	"/*\n * a { and a ) over\n * three lines\n */\n",
	"/* one */ /* two */\n",
	"long long a = \"}{\";\n",
	"long long a = \"say \\\"{\\\" now\";\n",
	"long long a = \"/* {\";\n",
	"long long a = '{';\n",
	"long long a = '}';\n",
	"long long a = '\\'';\n",
	"long long a = \"\\\\\";\n",
	"long long a = \"abc\n",
	"long long a = \"abc\\\n",
	"long long a = 'x\n",
	"long long a = \"abc\n\"def\n",
	"long long a = a / b;\n",
	"long long a = 1; // }\n",
	"long long a = 1; /* } */\n",
	"@\n",
	"long long a = 1;\n\n\n\n",
}

var crossTails = []string{
	"void f(void) {\n",
	"void f(void) { g( ;\n}\n",
	"void f(void) ( }\n",
	"void f(void) { ]\n",
	"long long a[2] = {1, {2, 3;\n",
	"void f(void) { }\n",
	"}\n",
}

// TestEveryBracketFindingNamesTheBracketItClaims holds [bracketBalance] to the
// source each finding was made from: the byte it points at, the order it
// answers openers in, and the exclusivity of the two answers.
func TestEveryBracketFindingNamesTheBracketItClaims(t *testing.T) {
	positioned := 0
	for _, head := range crossHeads {
		for _, tail := range crossTails {
			src := head + tail
			open, mismatch := newConverter("t.c", src).balance()
			if len(open) != 0 || mismatch != nil {
				positioned++
			}
			if len(open) != 0 && mismatch != nil {
				t.Errorf("%q answered both %d unclosed openers and the mismatch %v", src, len(open), mismatch)
			}
			checkOpeners(t, src, open)
			checkMismatch(t, src, mismatch)
		}
	}
	// A table whose sources all pair their brackets asserts nothing about a
	// position, which is what this counts to keep it from becoming.
	if want := len(crossHeads) * len(crossTails) / 2; positioned < want {
		t.Errorf("%d of %d sources carried a finding with a position in it, want at least %d",
			positioned, len(crossHeads)*len(crossTails), want)
	}
}

// checkOpeners requires each unclosed opener to sit on the byte it names, and
// the list to run outermost first.
func checkOpeners(t *testing.T, src string, open []balanceOpener) {
	t.Helper()
	prev := -1
	for _, b := range open {
		checkAt(t, src, b.pos, b.ch)
		if b.pos.Offset <= prev {
			t.Errorf("%q answers the opener at %s after one at offset %d, so the list is not outermost first",
				src, b.pos, prev)
		}
		prev = b.pos.Offset
	}
}

// checkMismatch requires the unpaired closer, and the opener it was measured
// against where there is one, to sit on the bytes they name.
func checkMismatch(t *testing.T, src string, mismatch *balanceMismatch) {
	t.Helper()
	if mismatch == nil {
		return
	}
	checkAt(t, src, mismatch.pos, mismatch.closer)
	if mismatch.opener != nil {
		checkAt(t, src, mismatch.opener.pos, mismatch.opener.ch)
	}
}

// checkAt requires pos to name the byte ch of src, by offset and by the line and
// column a reader is sent to.
func checkAt(t *testing.T, src string, pos source.Position, ch byte) {
	t.Helper()
	if pos.Offset < 0 || pos.Offset >= len(src) {
		t.Errorf("%q: offset %d is outside the source", src, pos.Offset)
		return
	}
	if src[pos.Offset] != ch {
		t.Errorf("%q: offset %d is %q, and the finding names %q", src, pos.Offset, src[pos.Offset], ch)
	}
	line, column := lineColumn(src, pos.Offset)
	if pos.Line != line || pos.Column != column {
		t.Errorf("%q: offset %d is at %d:%d, and the finding says %d:%d",
			src, pos.Offset, line, column, pos.Line, pos.Column)
	}
}
