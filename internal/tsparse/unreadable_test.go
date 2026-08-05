package tsparse

import "testing"

// Source the lexer stopped inside is claimed whole, and the offset it
// stopped at is compared against four times over: measuring the claim,
// filtering a diagnostic, and asking whether a node or a position reaches
// into it. The programs below put a construct against that exact offset.

// unreadableCase is one program that stands on the boundary.
type unreadableCase struct {
	name string
	src  string
	want []string
	// boundary names the comparison the program is measured against, so a
	// failure says which of the four moved.
	boundary string
}

var unreadableCases = []unreadableCase{
	{
		name: "a comment the file never closes swallows the declaration behind it",
		src:  "/* unterminated\nlong long b = 2;\n",
		want: []string{"test.c:1:1: unterminated block comment"},
		boundary: "unreadableFrom takes the lexer's diagnostic at the start of the last token: " +
			"an unterminated comment is one token reported at the '/*' it opened with, so a " +
			"claim measured from past that offset claims nothing and the grammar's reading " +
			"of the comment as code reaches the list",
	},
	{
		name: "an opener the file never closes in front of a comment the file never closes",
		src:  "long long a = __ic_hash(/* unterminated",
		want: []string{
			"test.c:1:24: unclosed '('; no matching ')' before end of file",
			"test.c:1:25: unterminated block comment",
		},
		boundary: "reachesUnreadable measures a node's end past the claim rather than at it: the " +
			"error node the '(' opens ends exactly where the comment begins, and a node " +
			"that stops there has read none of the source the lexer could not",
	},
	{
		name: "a comment the file never closes where a terminator was owed",
		src:  "void f(void) { a b /*;\n",
		want: []string{"test.c:1:20: unterminated block comment"},
		boundary: "unreadableAt measures a position at the claim as well as past it: the sentence " +
			"about the missing terminator is reported in front of the token that showed it " +
			"was missing, so the guard errorf makes on the position it is given would not " +
			"see the token at all",
	},
}

// TestTheUnreadableBoundaryStandsWhereItIsArguedTo holds each of the four
// comparisons around [converter.unreadable] to the byte it claims.
func TestTheUnreadableBoundaryStandsWhereItIsArguedTo(t *testing.T) {
	for _, tt := range unreadableCases {
		t.Run(tt.name, func(t *testing.T) {
			_, diags, err := Parse("test.c", tt.src)
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			checkRendered(t, diags, tt.want)
			t.Logf("the boundary this program stands on: %s", tt.boundary)
		})
	}
}

// TestTheUnreadableOffsetIsOneTheLexerReported holds the fourth comparison,
// in [converter.errorf], to the fact that makes it immaterial at the
// boundary: the offset a claim begins at is a lexer diagnostic's, already in
// [converter.seen], so it costs no second message whichever side is chosen.
func TestTheUnreadableOffsetIsOneTheLexerReported(t *testing.T) {
	for _, tt := range unreadableCases {
		t.Run(tt.name, func(t *testing.T) {
			c := newConverter("test.c", tt.src)
			if c.unreadable > len(tt.src) {
				t.Fatalf("the source claims nothing unreadable, so it no longer stands on the boundary")
			}
			if !c.seen[c.unreadable] {
				t.Errorf("the claim begins at byte %d, which carries no lexical diagnostic", c.unreadable)
			}
		})
	}
}
