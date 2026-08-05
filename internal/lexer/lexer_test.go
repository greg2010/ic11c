package lexer_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/lexer"
	"github.com/greg2010/ic11c/internal/source"
)

// maxScanTokens bounds a scan so a lexer defect surfaces as a test failure
// rather than a hung run.
const maxScanTokens = 4096

type want struct {
	kind lexer.Kind
	text string
	i    int64
	s    string
}

func scan(t *testing.T, src string) ([]lexer.Token, source.DiagnosticList) {
	t.Helper()
	l := lexer.New("test.c", src)
	var toks []lexer.Token
	for {
		tok := l.Next()
		if tok.Kind == lexer.EOF {
			return toks, l.Diagnostics()
		}
		toks = append(toks, tok)
		if len(toks) > maxScanTokens {
			t.Fatalf("scanning %q produced more than %d tokens", src, maxScanTokens)
		}
	}
}

func checkTokens(t *testing.T, src string, got []lexer.Token, wants []want) {
	t.Helper()
	if len(got) != len(wants) {
		t.Fatalf("scan(%q) produced %d tokens, want %d: %v", src, len(got), len(wants), kinds(got))
	}
	for i, w := range wants {
		g := got[i]
		if g.Kind != w.kind {
			t.Errorf("scan(%q) token %d: kind = %v, want %v", src, i, g.Kind, w.kind)
		}
		if w.text != "" && g.Text != w.text {
			t.Errorf("scan(%q) token %d: text = %q, want %q", src, i, g.Text, w.text)
		}
		if g.Int != w.i {
			t.Errorf("scan(%q) token %d: int = %d, want %d", src, i, g.Int, w.i)
		}
		if g.Str != w.s {
			t.Errorf("scan(%q) token %d: str = %q, want %q", src, i, g.Str, w.s)
		}
	}
}

func kinds(toks []lexer.Token) []lexer.Kind {
	out := make([]lexer.Kind, len(toks))
	for i, t := range toks {
		out[i] = t.Kind
	}
	return out
}

func TestTokenSequences(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []want
	}{
		{
			name: "empty",
			src:  "",
		},
		{
			name: "whitespace only",
			src:  "  \t\r\n  \n",
		},
		{
			name: "keywords",
			src:  "int bool void const constexpr if else while for do break continue return switch case default true false",
			want: []want{
				{kind: lexer.Int, text: "int"},
				{kind: lexer.Bool, text: "bool"},
				{kind: lexer.Void, text: "void"},
				{kind: lexer.Const, text: "const"},
				{kind: lexer.Constexpr, text: "constexpr"},
				{kind: lexer.If, text: "if"},
				{kind: lexer.Else, text: "else"},
				{kind: lexer.While, text: "while"},
				{kind: lexer.For, text: "for"},
				{kind: lexer.Do, text: "do"},
				{kind: lexer.Break, text: "break"},
				{kind: lexer.Continue, text: "continue"},
				{kind: lexer.Return, text: "return"},
				{kind: lexer.Switch, text: "switch"},
				{kind: lexer.Case, text: "case"},
				{kind: lexer.Default, text: "default"},
				{kind: lexer.True, text: "true"},
				{kind: lexer.False, text: "false"},
			},
		},
		{
			name: "excluded keywords still scan as keywords",
			src:  "struct union enum float double goto typedef sizeof static unsigned char",
			want: []want{
				{kind: lexer.Struct, text: "struct"},
				{kind: lexer.Union, text: "union"},
				{kind: lexer.Enum, text: "enum"},
				{kind: lexer.Float, text: "float"},
				{kind: lexer.Double, text: "double"},
				{kind: lexer.Goto, text: "goto"},
				{kind: lexer.Typedef, text: "typedef"},
				{kind: lexer.Sizeof, text: "sizeof"},
				{kind: lexer.Static, text: "static"},
				{kind: lexer.Unsigned, text: "unsigned"},
				{kind: lexer.Char, text: "char"},
			},
		},
		{
			name: "reserved keywords scan as one kind carrying the spelling",
			src:  "nullptr alignas _Atomic typeof _BitInt",
			want: []want{
				{kind: lexer.Reserved, text: "nullptr"},
				{kind: lexer.Reserved, text: "alignas"},
				{kind: lexer.Reserved, text: "_Atomic"},
				{kind: lexer.Reserved, text: "typeof"},
				{kind: lexer.Reserved, text: "_BitInt"},
			},
		},
		{
			name: "words C leaves alone are identifiers",
			src:  "asm offsetof noreturn NULL __attribute__",
			want: []want{
				{kind: lexer.Ident, text: "asm"},
				{kind: lexer.Ident, text: "offsetof"},
				{kind: lexer.Ident, text: "noreturn"},
				{kind: lexer.Ident, text: "NULL"},
				{kind: lexer.Ident, text: "__attribute__"},
			},
		},
		{
			name: "identifiers",
			src:  "x _y a1 __ic_load Temperature d0",
			want: []want{
				{kind: lexer.Ident, text: "x"},
				{kind: lexer.Ident, text: "_y"},
				{kind: lexer.Ident, text: "a1"},
				{kind: lexer.Ident, text: "__ic_load"},
				{kind: lexer.Ident, text: "Temperature"},
				{kind: lexer.Ident, text: "d0"},
			},
		},
		{
			name: "keyword prefix is an identifier",
			src:  "integer iff doing",
			want: []want{
				{kind: lexer.Ident, text: "integer"},
				{kind: lexer.Ident, text: "iff"},
				{kind: lexer.Ident, text: "doing"},
			},
		},
		{
			name: "arithmetic and comparison operators",
			src:  "+ - * / % == != < <= > >= && || ! ~ & | ^ << >>",
			want: []want{
				{kind: lexer.Add}, {kind: lexer.Sub}, {kind: lexer.Mul}, {kind: lexer.Quo},
				{kind: lexer.Rem}, {kind: lexer.Eq}, {kind: lexer.Neq}, {kind: lexer.Lt},
				{kind: lexer.Leq}, {kind: lexer.Gt}, {kind: lexer.Geq}, {kind: lexer.Land},
				{kind: lexer.Lor}, {kind: lexer.Not}, {kind: lexer.Tilde}, {kind: lexer.And},
				{kind: lexer.Or}, {kind: lexer.Xor}, {kind: lexer.Shl}, {kind: lexer.Shr},
			},
		},
		{
			name: "assignment operators",
			src:  "= += -= *= /= %= &= |= ^= <<= >>=",
			want: []want{
				{kind: lexer.Assign}, {kind: lexer.AddAssign}, {kind: lexer.SubAssign},
				{kind: lexer.MulAssign}, {kind: lexer.QuoAssign}, {kind: lexer.RemAssign},
				{kind: lexer.AndAssign}, {kind: lexer.OrAssign}, {kind: lexer.XorAssign},
				{kind: lexer.ShlAssign}, {kind: lexer.ShrAssign},
			},
		},
		{
			name: "punctuation",
			src:  "( ) [ ] { } , ; : :: ? . -> ... ++ --",
			want: []want{
				{kind: lexer.Lparen}, {kind: lexer.Rparen}, {kind: lexer.Lbrack},
				{kind: lexer.Rbrack}, {kind: lexer.Lbrace}, {kind: lexer.Rbrace},
				{kind: lexer.Comma}, {kind: lexer.Semicolon}, {kind: lexer.Colon},
				{kind: lexer.Scope}, {kind: lexer.Question}, {kind: lexer.Period},
				{kind: lexer.Arrow}, {kind: lexer.Ellipsis}, {kind: lexer.Inc},
				{kind: lexer.Dec},
			},
		},
		{
			// The attribute specifier, which is the one construct either
			// spelling appears in. C23 spells its brackets as two tokens and
			// its separator as one, and a scan that took ':' twice would admit
			// ': :' where C admits nothing.
			name: "an attribute specifier",
			src:  `[[ic11c::prefab("StructureGasSensor")]]`,
			want: []want{
				{kind: lexer.Lbrack}, {kind: lexer.Lbrack},
				{kind: lexer.Ident, text: "ic11c"}, {kind: lexer.Scope},
				{kind: lexer.Ident, text: "prefab"}, {kind: lexer.Lparen},
				{kind: lexer.StringLit, s: "StructureGasSensor"},
				{kind: lexer.Rparen}, {kind: lexer.Rbrack}, {kind: lexer.Rbrack},
			},
		},
		{
			name: "maximal munch without spaces",
			src:  "a<<=b>>=c!=d",
			want: []want{
				{kind: lexer.Ident, text: "a"},
				{kind: lexer.ShlAssign},
				{kind: lexer.Ident, text: "b"},
				{kind: lexer.ShrAssign},
				{kind: lexer.Ident, text: "c"},
				{kind: lexer.Neq},
				{kind: lexer.Ident, text: "d"},
			},
		},
		{
			name: "increment is not two additions",
			src:  "a+++b",
			want: []want{
				{kind: lexer.Ident, text: "a"},
				{kind: lexer.Inc},
				{kind: lexer.Add},
				{kind: lexer.Ident, text: "b"},
			},
		},
		{
			name: "line comment is discarded",
			src:  "a // b c\nd",
			want: []want{
				{kind: lexer.Ident, text: "a"},
				{kind: lexer.Ident, text: "d"},
			},
		},
		{
			name: "block comment is discarded",
			src:  "a /* b\n c */ d",
			want: []want{
				{kind: lexer.Ident, text: "a"},
				{kind: lexer.Ident, text: "d"},
			},
		},
		{
			name: "block comment does not nest",
			src:  "a /* /* */ d",
			want: []want{
				{kind: lexer.Ident, text: "a"},
				{kind: lexer.Ident, text: "d"},
			},
		},
		{
			name: "comment markers inside a string are text",
			src:  `"// not a comment"`,
			want: []want{{kind: lexer.StringLit, text: `"// not a comment"`, s: "// not a comment"}},
		},
		{
			name: "division is not a comment",
			src:  "a / b",
			want: []want{
				{kind: lexer.Ident, text: "a"},
				{kind: lexer.Quo},
				{kind: lexer.Ident, text: "b"},
			},
		},
		{
			name: "declaration",
			src:  "const int kWindow = 8;",
			want: []want{
				{kind: lexer.Const, text: "const"},
				{kind: lexer.Int, text: "int"},
				{kind: lexer.Ident, text: "kWindow"},
				{kind: lexer.Assign},
				{kind: lexer.IntLit, text: "8", i: 8},
				{kind: lexer.Semicolon},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, diags := scan(t, tt.src)
			if len(diags) != 0 {
				t.Errorf("scan(%q) reported %v", tt.src, diags)
			}
			checkTokens(t, tt.src, got, tt.want)
		})
	}
}

func TestLiteralValues(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []want
	}{
		{name: "zero", src: "0", want: []want{{kind: lexer.IntLit, text: "0", i: 0}}},
		{name: "decimal", src: "1234", want: []want{{kind: lexer.IntLit, text: "1234", i: 1234}}},
		{name: "hex lower", src: "0xff", want: []want{{kind: lexer.IntLit, text: "0xff", i: 255}}},
		{name: "hex upper", src: "0XFF", want: []want{{kind: lexer.IntLit, text: "0XFF", i: 255}}},
		{name: "hex mixed case digits", src: "0xAbC", want: []want{{kind: lexer.IntLit, text: "0xAbC", i: 0xabc}}},
		{
			name: "exact to two to the fifty-three",
			src:  "9007199254740992",
			want: []want{{kind: lexer.IntLit, text: "9007199254740992", i: 1 << 53}},
		},
		{
			name: "largest signed sixty-four bit",
			src:  "9223372036854775807",
			want: []want{{kind: lexer.IntLit, i: 9223372036854775807}},
		},
		{
			// The hexadecimal path parses into a uint64 and narrows, so its
			// boundary is one the lexer draws rather than one the parse
			// reports, and it is a different boundary from the row above.
			name: "largest signed sixty-four bit in hexadecimal",
			src:  "0x7FFFFFFFFFFFFFFF",
			want: []want{{kind: lexer.IntLit, i: 9223372036854775807}},
		},
		{name: "char plain", src: `'a'`, want: []want{{kind: lexer.CharLit, text: `'a'`, i: 'a'}}},
		{name: "char digit", src: `'0'`, want: []want{{kind: lexer.CharLit, text: `'0'`, i: '0'}}},
		{name: "char newline escape", src: `'\n'`, want: []want{{kind: lexer.CharLit, text: `'\n'`, i: 10}}},
		{name: "char tab escape", src: `'\t'`, want: []want{{kind: lexer.CharLit, i: 9}}},
		{name: "char carriage return escape", src: `'\r'`, want: []want{{kind: lexer.CharLit, i: 13}}},
		{name: "char bell escape", src: `'\a'`, want: []want{{kind: lexer.CharLit, i: 7}}},
		{name: "char backspace escape", src: `'\b'`, want: []want{{kind: lexer.CharLit, i: 8}}},
		{name: "char form feed escape", src: `'\f'`, want: []want{{kind: lexer.CharLit, i: 12}}},
		{name: "char vertical tab escape", src: `'\v'`, want: []want{{kind: lexer.CharLit, i: 11}}},
		{name: "char backslash escape", src: `'\\'`, want: []want{{kind: lexer.CharLit, i: 92}}},
		{name: "char quote escape", src: `'\''`, want: []want{{kind: lexer.CharLit, i: 39}}},
		{name: "char double quote escape", src: `'\"'`, want: []want{{kind: lexer.CharLit, i: 34}}},
		{name: "char question escape", src: `'\?'`, want: []want{{kind: lexer.CharLit, i: 63}}},
		{name: "char nul octal escape", src: `'\0'`, want: []want{{kind: lexer.CharLit, i: 0}}},
		{name: "char three digit octal escape", src: `'\101'`, want: []want{{kind: lexer.CharLit, i: 'A'}}},
		{name: "char hex escape", src: `'\x41'`, want: []want{{kind: lexer.CharLit, i: 'A'}}},
		{name: "char one digit hex escape", src: `'\x7'`, want: []want{{kind: lexer.CharLit, i: 7}}},
		{name: "char highest ascii", src: `'\x7f'`, want: []want{{kind: lexer.CharLit, i: 0x7f}}},
		// The rune branch of scanCharValue rather than an escape, and the last
		// code point it admits. The one above it is in TestMalformedInput.
		{name: "char highest ascii written literally", src: "'\x7f'", want: []want{{kind: lexer.CharLit, i: 0x7f}}},
		// A numeric escape names a byte, not a code point, so the range runs to
		// 0xff and the value above 0x7f is the one a mask would quietly change.
		{name: "char hex escape above ascii", src: `'\xff'`, want: []want{{kind: lexer.CharLit, i: 0xff}}},
		{name: "char hex escape at the top of the range", src: `'\x80'`, want: []want{{kind: lexer.CharLit, i: 0x80}}},
		{name: "char octal escape above ascii", src: `'\377'`, want: []want{{kind: lexer.CharLit, i: 0xff}}},
		{
			name: "string plain",
			src:  `"StructureWallLight"`,
			want: []want{{kind: lexer.StringLit, text: `"StructureWallLight"`, s: "StructureWallLight"}},
		},
		{name: "string empty", src: `""`, want: []want{{kind: lexer.StringLit, text: `""`, s: ""}}},
		{
			name: "string with escapes",
			src:  `"a\tb\\c\"d"`,
			want: []want{{kind: lexer.StringLit, s: "a\tb\\c\"d"}},
		},
		{
			name: "string with hex escape",
			src:  `"\x41\x42"`,
			want: []want{{kind: lexer.StringLit, s: "AB"}},
		},
		// A string literal reaches the compiler only as a __ic_hash argument, so
		// a byte the escape decoded and the builder then changed is a different
		// hash and nothing else. These three sit above 0x7f, where a byte
		// written through a rune-shaped path would lose its top bit.
		{
			name: "string with a hex escape above ascii",
			src:  `"\xff"`,
			want: []want{{kind: lexer.StringLit, s: "\xff"}},
		},
		{
			name: "string with a hex escape at the top of the range",
			src:  `"\x80"`,
			want: []want{{kind: lexer.StringLit, s: "\x80"}},
		},
		{
			name: "string with an octal escape above ascii",
			src:  `"\377"`,
			want: []want{{kind: lexer.StringLit, s: "\xff"}},
		},
		{
			name: "string keeps multi-byte runes",
			src:  `"pi=π"`,
			want: []want{{kind: lexer.StringLit, s: "pi=π"}},
		},
		{
			name: "adjacent strings are separate tokens",
			src:  `"a" "b"`,
			want: []want{
				{kind: lexer.StringLit, s: "a"},
				{kind: lexer.StringLit, s: "b"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, diags := scan(t, tt.src)
			if len(diags) != 0 {
				t.Errorf("scan(%q) reported %v", tt.src, diags)
			}
			checkTokens(t, tt.src, got, tt.want)
		})
	}
}

func TestPositions(t *testing.T) {
	type at struct {
		text string
		line int
		col  int
	}

	tests := []struct {
		name string
		src  string
		want []at
	}{
		{
			name: "first token",
			src:  "int",
			want: []at{{text: "int", line: 1, col: 1}},
		},
		{
			name: "across lines",
			src:  "a\nbb\n  c",
			want: []at{
				{text: "a", line: 1, col: 1},
				{text: "bb", line: 2, col: 1},
				{text: "c", line: 3, col: 3},
			},
		},
		{
			name: "column counts bytes so a tab is one column",
			src:  "\ta\tb",
			want: []at{
				{text: "a", line: 1, col: 2},
				{text: "b", line: 1, col: 4},
			},
		},
		{
			name: "carriage return before newline does not shift the next line",
			src:  "a\r\nb",
			want: []at{
				{text: "a", line: 1, col: 1},
				{text: "b", line: 2, col: 1},
			},
		},
		{
			name: "after a single line block comment",
			src:  "a /* x */ b",
			want: []at{
				{text: "a", line: 1, col: 1},
				{text: "b", line: 1, col: 11},
			},
		},
		{
			name: "after a multi-line block comment",
			src:  "a /* one\ntwo\nthree */ b\nc",
			want: []at{
				{text: "a", line: 1, col: 1},
				{text: "b", line: 3, col: 10},
				{text: "c", line: 4, col: 1},
			},
		},
		{
			name: "after a line comment",
			src:  "a // trailing\nb",
			want: []at{
				{text: "a", line: 1, col: 1},
				{text: "b", line: 2, col: 1},
			},
		},
		{
			name: "after an escaped character literal",
			src:  `x '\n' y`,
			want: []at{
				{text: "x", line: 1, col: 1},
				{text: `'\n'`, line: 1, col: 3},
				{text: "y", line: 1, col: 8},
			},
		},
		{
			name: "after a hex escape in a character literal",
			src:  `x '\x41' y`,
			want: []at{
				{text: "x", line: 1, col: 1},
				{text: `'\x41'`, line: 1, col: 3},
				{text: "y", line: 1, col: 10},
			},
		},
		{
			name: "after a string with escapes",
			src:  `s "a\tb" t`,
			want: []at{
				{text: "s", line: 1, col: 1},
				{text: `"a\tb"`, line: 1, col: 3},
				{text: "t", line: 1, col: 10},
			},
		},
		{
			name: "column counts bytes inside a multi-byte string",
			src:  `"π" x`,
			want: []at{
				{text: `"π"`, line: 1, col: 1},
				{text: "x", line: 1, col: 6},
			},
		},
		{
			name: "operators on a busy line",
			src:  "a<<=b;",
			want: []at{
				{text: "a", line: 1, col: 1},
				{text: "<<=", line: 1, col: 2},
				{text: "b", line: 1, col: 5},
				{text: ";", line: 1, col: 6},
			},
		},
		{
			name: "blank lines still count",
			src:  "a\n\n\n\nb",
			want: []at{
				{text: "a", line: 1, col: 1},
				{text: "b", line: 5, col: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, diags := scan(t, tt.src)
			if len(diags) != 0 {
				t.Errorf("scan(%q) reported %v", tt.src, diags)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("scan(%q) produced %d tokens, want %d", tt.src, len(got), len(tt.want))
			}
			for i, w := range tt.want {
				g := got[i]
				if g.Text != w.text {
					t.Errorf("token %d: text = %q, want %q", i, g.Text, w.text)
				}
				if g.Pos.Line != w.line || g.Pos.Column != w.col {
					t.Errorf("token %d (%q): position = %d:%d, want %d:%d",
						i, g.Text, g.Pos.Line, g.Pos.Column, w.line, w.col)
				}
				if g.Pos.File != "test.c" {
					t.Errorf("token %d (%q): file = %q, want %q", i, g.Text, g.Pos.File, "test.c")
				}
				if !g.Pos.IsValid() {
					t.Errorf("token %d (%q): position is invalid", i, g.Text)
				}
			}
		})
	}
}

func TestOffsetTracksSource(t *testing.T) {
	src := "int x;\nint yy;"
	got, diags := scan(t, src)
	if len(diags) != 0 {
		t.Fatalf("scan reported %v", diags)
	}
	for _, tok := range got {
		if tok.Text == "" {
			continue
		}
		if !strings.HasPrefix(src[tok.Pos.Offset:], tok.Text) {
			t.Errorf("token %q at offset %d does not match the source there", tok.Text, tok.Pos.Offset)
		}
	}
}

func TestMalformedInput(t *testing.T) {
	type at struct {
		msg  string
		line int
		col  int
	}

	tests := []struct {
		name      string
		src       string
		want      []at
		wantKinds []lexer.Kind
	}{
		{
			name:      "unexpected character",
			src:       "a @ b",
			want:      []at{{msg: "unexpected character '@'", line: 1, col: 3}},
			wantKinds: []lexer.Kind{lexer.Ident, lexer.Ident},
		},
		{
			name:      "unexpected character does not stop scanning",
			src:       "a\n@\nb\n$\nc",
			want:      []at{{msg: "unexpected character", line: 2, col: 1}, {msg: "unexpected character", line: 4, col: 1}},
			wantKinds: []lexer.Kind{lexer.Ident, lexer.Ident, lexer.Ident},
		},
		{
			name:      "unterminated block comment",
			src:       "a /* b\nc",
			want:      []at{{msg: "unterminated block comment", line: 1, col: 3}},
			wantKinds: []lexer.Kind{lexer.Ident},
		},
		{
			// The star is the last byte, so the scan asks whether a slash
			// follows it with nothing there to read. It is the one input that
			// reaches past the end of the source.
			name:      "unterminated block comment ending in a star",
			src:       "a /* b *",
			want:      []at{{msg: "unterminated block comment", line: 1, col: 3}},
			wantKinds: []lexer.Kind{lexer.Ident},
		},
		{
			name:      "unterminated block comment ending in a slash",
			src:       "a /* b /",
			want:      []at{{msg: "unterminated block comment", line: 1, col: 3}},
			wantKinds: []lexer.Kind{lexer.Ident},
		},
		{
			name:      "unterminated string",
			src:       "\"abc\nx",
			want:      []at{{msg: "unterminated string literal", line: 1, col: 1}},
			wantKinds: []lexer.Kind{lexer.StringLit, lexer.Ident},
		},
		{
			name:      "unterminated character literal",
			src:       "'a\nx",
			want:      []at{{msg: "unterminated character literal", line: 1, col: 1}},
			wantKinds: []lexer.Kind{lexer.CharLit, lexer.Ident},
		},
		{
			// The escape has no character to name, and the newline that ended
			// the line is not it. Consuming it here would leave the line
			// counter one short for the rest of the file, which the '@' on the
			// second line is what says: it is reported at 2:3 or nowhere.
			name: "a backslash at the end of a line in a character literal",
			src:  "'\\\nx @",
			want: []at{
				{msg: "unterminated escape sequence", line: 1, col: 2},
				{msg: "unterminated character literal", line: 1, col: 1},
				{msg: "unexpected character '@'", line: 2, col: 3},
			},
			wantKinds: []lexer.Kind{lexer.CharLit, lexer.Ident},
		},
		{
			name: "a backslash at the end of the source in a character literal",
			src:  "'\\",
			want: []at{
				{msg: "unterminated escape sequence", line: 1, col: 2},
				{msg: "unterminated character literal", line: 1, col: 1},
			},
			wantKinds: []lexer.Kind{lexer.CharLit},
		},
		{
			name:      "empty character literal",
			src:       "''",
			want:      []at{{msg: "empty character literal", line: 1, col: 1}},
			wantKinds: []lexer.Kind{lexer.CharLit},
		},
		{
			// The first code point that is not ASCII. Every other row here sits
			// well above it, so this is the one that says where the line is
			// drawn rather than that one exists.
			name:      "the code point just past ascii in a character literal",
			src:       "'\u0080'",
			want:      []at{{msg: "a character literal holds one ASCII character", line: 1, col: 2}},
			wantKinds: []lexer.Kind{lexer.CharLit},
		},
		{
			name:      "latin-1 character literal",
			src:       "'é'",
			want:      []at{{msg: `a character literal holds one ASCII character, and 'é' is not one`, line: 1, col: 2}},
			wantKinds: []lexer.Kind{lexer.CharLit},
		},
		{
			name:      "character literal past the basic multilingual plane",
			src:       "'π'",
			want:      []at{{msg: "a character literal holds one ASCII character", line: 1, col: 2}},
			wantKinds: []lexer.Kind{lexer.CharLit},
		},
		{
			name:      "a string literal keeps its multi-byte runes",
			src:       `"π" 'π'`,
			want:      []at{{msg: "a character literal holds one ASCII character", line: 1, col: 7}},
			wantKinds: []lexer.Kind{lexer.StringLit, lexer.CharLit},
		},
		{
			name:      "multi character literal",
			src:       "'ab'",
			want:      []at{{msg: "more than one character", line: 1, col: 1}},
			wantKinds: []lexer.Kind{lexer.CharLit},
		},
		{
			name:      "unknown escape",
			src:       `'\q'`,
			want:      []at{{msg: `unknown escape sequence "\\q"`, line: 1, col: 2}},
			wantKinds: []lexer.Kind{lexer.CharLit},
		},
		{
			name:      "hex escape without digits",
			src:       `'\x'`,
			want:      []at{{msg: "hexadecimal escape has no digits", line: 1, col: 2}},
			wantKinds: []lexer.Kind{lexer.CharLit},
		},
		{
			name:      "hex escape out of range",
			src:       `'\x1ff'`,
			want:      []at{{msg: "out of range", line: 1, col: 2}},
			wantKinds: []lexer.Kind{lexer.CharLit},
		},
		{
			name:      "octal escape out of range",
			src:       `'\777'`,
			want:      []at{{msg: "out of range", line: 1, col: 2}},
			wantKinds: []lexer.Kind{lexer.CharLit},
		},
		{
			// C stops an octal escape after three digits. A fourth is a
			// character of its own, which is what makes this literal hold two.
			name:      "an octal escape stops after three digits",
			src:       `'\0000'`,
			want:      []at{{msg: "more than one character", line: 1, col: 1}},
			wantKinds: []lexer.Kind{lexer.CharLit},
		},
		{
			name:      "a fourth octal digit that would carry the escape out of range",
			src:       `'\1000'`,
			want:      []at{{msg: "more than one character", line: 1, col: 1}},
			wantKinds: []lexer.Kind{lexer.CharLit},
		},
		{
			// Eight and nine end an octal escape rather than extending it, so
			// this literal holds the escape and then the digit.
			name:      "an octal escape stops before an eight",
			src:       `'\08'`,
			want:      []at{{msg: "more than one character", line: 1, col: 1}},
			wantKinds: []lexer.Kind{lexer.CharLit},
		},
		{
			name:      "an octal escape stops before a nine",
			src:       `'\79'`,
			want:      []at{{msg: "more than one character", line: 1, col: 1}},
			wantKinds: []lexer.Kind{lexer.CharLit},
		},
		{
			// The letter just past the hexadecimal digits. Taking it as one
			// would carry it into the parse rather than leaving it as the
			// suffix it is, and the message and the place it points at are what
			// say which of the two happened.
			name:      "a letter past the hexadecimal digits is a suffix",
			src:       "0xfg",
			want:      []at{{msg: `invalid suffix "g" on integer literal`, line: 1, col: 4}},
			wantKinds: []lexer.Kind{lexer.IntLit},
		},
		{
			name:      "a letter past the digits of a hexadecimal escape",
			src:       `'\xfg'`,
			want:      []at{{msg: "more than one character", line: 1, col: 1}},
			wantKinds: []lexer.Kind{lexer.CharLit},
		},
		{
			name:      "hex literal without digits",
			src:       "0x",
			want:      []at{{msg: "hexadecimal literal has no digits", line: 1, col: 1}},
			wantKinds: []lexer.Kind{lexer.IntLit},
		},
		{
			name:      "integer literal overflow",
			src:       "99999999999999999999",
			want:      []at{{msg: "does not fit in a 64-bit signed integer", line: 1, col: 1}},
			wantKinds: []lexer.Kind{lexer.IntLit},
		},
		{
			name:      "hex literal overflow",
			src:       "0xFFFFFFFFFFFFFFFF",
			want:      []at{{msg: "does not fit in a 64-bit signed integer", line: 1, col: 1}},
			wantKinds: []lexer.Kind{lexer.IntLit},
		},
		{
			name:      "octal literal rejected",
			src:       "0777",
			want:      []at{{msg: "octal literals are not supported", line: 1, col: 1}},
			wantKinds: []lexer.Kind{lexer.IntLit},
		},
		{
			// The shortest spelling C reads as octal. A leading zero and one
			// digit is where the rule starts, and '0' alone is the digit before
			// it, which TestLiteralValues holds to no diagnostic at all.
			name:      "two digit octal literal rejected",
			src:       "07",
			want:      []at{{msg: "octal literals are not supported", line: 1, col: 1}},
			wantKinds: []lexer.Kind{lexer.IntLit},
		},
		{
			// C refuses this outright: there is no octal digit 8. Reading it as
			// decimal 8 would give the program a value clang never compiles.
			name:      "a leading zero on a digit no octal literal holds",
			src:       "08",
			want:      []at{{msg: "octal literals are not supported", line: 1, col: 1}},
			wantKinds: []lexer.Kind{lexer.IntLit},
		},
		{
			name:      "integer suffix rejected",
			src:       "12u",
			want:      []at{{msg: `invalid suffix "u" on integer literal`, line: 1, col: 3}},
			wantKinds: []lexer.Kind{lexer.IntLit},
		},
		{
			name:      "binary literal rejected as a suffix",
			src:       "0b1010",
			want:      []at{{msg: `invalid suffix "b1010"`, line: 1, col: 2}},
			wantKinds: []lexer.Kind{lexer.IntLit},
		},
		{
			name:      "float suffix rejected",
			src:       "1.5f",
			want:      []at{{msg: `invalid suffix "f" on floating-point literal`, line: 1, col: 4}},
			wantKinds: []lexer.Kind{lexer.FloatLit},
		},
		{
			// An 'e' with no digit after it is a suffix, not an exponent.
			// Reading it as one leaves a literal ParseFloat refuses, which
			// answers zero and says nothing.
			name:      "an exponent with no digits",
			src:       "1e;",
			want:      []at{{msg: `invalid suffix "e" on integer literal`, line: 1, col: 2}},
			wantKinds: []lexer.Kind{lexer.IntLit, lexer.Semicolon},
		},
		{
			name:      "an exponent whose sign is followed by no digits",
			src:       "1e+;",
			want:      []at{{msg: `invalid suffix "e" on integer literal`, line: 1, col: 2}},
			wantKinds: []lexer.Kind{lexer.IntLit, lexer.Add, lexer.Semicolon},
		},
		{
			name:      "an exponent at the end of the source",
			src:       "1e",
			want:      []at{{msg: `invalid suffix "e" on integer literal`, line: 1, col: 2}},
			wantKinds: []lexer.Kind{lexer.IntLit},
		},
		{
			name:      "an exponent whose sign ends the source",
			src:       "1e+",
			want:      []at{{msg: `invalid suffix "e" on integer literal`, line: 1, col: 2}},
			wantKinds: []lexer.Kind{lexer.IntLit, lexer.Add},
		},
		{
			name:      "floating point literal past the range of a double",
			src:       "1e400",
			want:      []at{{msg: "larger than a double holds", line: 1, col: 1}},
			wantKinds: []lexer.Kind{lexer.FloatLit},
		},
		{
			name:      "preprocessor rejected",
			src:       "#include <stdio.h>\nint x;",
			want:      []at{{msg: "preprocessor directives are not supported", line: 1, col: 1}},
			wantKinds: []lexer.Kind{lexer.Int, lexer.Ident, lexer.Semicolon},
		},
		{
			name: "several problems are all reported",
			src:  "a @ 0x b '' c",
			want: []at{
				{msg: "unexpected character", line: 1, col: 3},
				{msg: "hexadecimal literal has no digits", line: 1, col: 5},
				{msg: "empty character literal", line: 1, col: 10},
			},
			wantKinds: []lexer.Kind{
				lexer.Ident, lexer.IntLit, lexer.Ident, lexer.CharLit, lexer.Ident,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, diags := scan(t, tt.src)
			if diff := kindsEqual(kinds(got), tt.wantKinds); diff != "" {
				t.Errorf("scan(%q) kinds: %s", tt.src, diff)
			}
			if len(diags) != len(tt.want) {
				t.Fatalf("scan(%q) reported %d diagnostics, want %d:\n%s",
					tt.src, len(diags), len(tt.want), diags)
			}
			for i, w := range tt.want {
				d := diags[i]
				if !strings.Contains(d.Msg, w.msg) {
					t.Errorf("diagnostic %d: message = %q, want it to contain %q", i, d.Msg, w.msg)
				}
				if d.Pos.Line != w.line || d.Pos.Column != w.col {
					t.Errorf("diagnostic %d (%q): position = %d:%d, want %d:%d",
						i, d.Msg, d.Pos.Line, d.Pos.Column, w.line, w.col)
				}
			}
		})
	}
}

func kindsEqual(got, want []lexer.Kind) string {
	if len(got) != len(want) {
		return "got " + kindList(got) + ", want " + kindList(want)
	}
	for i := range got {
		if got[i] != want[i] {
			return "got " + kindList(got) + ", want " + kindList(want)
		}
	}
	return ""
}

func kindList(ks []lexer.Kind) string {
	parts := make([]string, len(ks))
	for i, k := range ks {
		parts[i] = k.String()
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// TestFloatingPointLiterals covers the forms a double literal takes.
func TestFloatingPointLiterals(t *testing.T) {
	tests := []struct {
		name string
		src  string
		text string
		want float64
	}{
		{name: "a fraction", src: "1.5", text: "1.5", want: 1.5},
		{name: "a leading dot", src: ".5", text: ".5", want: 0.5},
		{name: "a trailing dot", src: "2.", text: "2.", want: 2},
		{name: "zero", src: "0.0", text: "0.0", want: 0},
		{name: "a small fraction", src: "0.001", text: "0.001", want: 0.001},
		{name: "an exponent", src: "1e5", text: "1e5", want: 100000},
		{name: "a capital exponent", src: "1E5", text: "1E5", want: 100000},
		{name: "a negative exponent", src: "1.5e-3", text: "1.5e-3", want: 0.0015},
		{name: "a signed exponent", src: "2e+2", text: "2e+2", want: 200},
		{name: "an exponent on a leading dot", src: ".25e1", text: ".25e1", want: 2.5},
		{name: "a value that underflows to zero", src: "1e-400", text: "1e-400", want: 0},
		{name: "an exponent butted against a trailing dot", src: "1.e5", text: "1.e5", want: 100000},
		{name: "a signed exponent on a trailing dot", src: "2.e-1", text: "2.e-1", want: 0.2},
		{name: "a leading dot with a capital signed exponent", src: ".5E+2", text: ".5E+2", want: 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, diags := scan(t, tt.src)
			if len(diags) != 0 {
				t.Fatalf("scan(%q) reported:\n%s", tt.src, diags)
			}
			if len(toks) != 1 || toks[0].Kind != lexer.FloatLit {
				t.Fatalf("scan(%q) produced %v, want one floating-point literal", tt.src, kinds(toks))
			}
			if toks[0].Text != tt.text {
				t.Errorf("scan(%q) text = %q, want %q", tt.src, toks[0].Text, tt.text)
			}
			if toks[0].Float != tt.want {
				t.Errorf("scan(%q) value = %v, want %v", tt.src, toks[0].Float, tt.want)
			}
		})
	}
}

// TestDevicePinSpellings covers the pin names, which are ordinary identifiers.
// Nothing binds a colon to one, so the same three tokens spell a conditional
// whether or not the source put spaces around it.
func TestDevicePinSpellings(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []want
	}{
		{
			name: "a bare pin is an identifier",
			src:  "d0",
			want: []want{{kind: lexer.Ident, text: "d0"}},
		},
		{
			name: "the housing is an identifier",
			src:  "db",
			want: []want{{kind: lexer.Ident, text: "db"}},
		},
		{
			name: "a spaced conditional",
			src:  "c ? d0 : 1",
			want: []want{
				{kind: lexer.Ident, text: "c"},
				{kind: lexer.Question},
				{kind: lexer.Ident, text: "d0"},
				{kind: lexer.Colon},
				{kind: lexer.IntLit, text: "1", i: 1},
			},
		},
		{
			name: "a conditional written without spaces",
			src:  "c ? d0:1",
			want: []want{
				{kind: lexer.Ident, text: "c"},
				{kind: lexer.Question},
				{kind: lexer.Ident, text: "d0"},
				{kind: lexer.Colon},
				{kind: lexer.IntLit, text: "1", i: 1},
			},
		},
		{
			name: "a pin followed by a label colon",
			src:  "d0:x",
			want: []want{
				{kind: lexer.Ident, text: "d0"},
				{kind: lexer.Colon},
				{kind: lexer.Ident, text: "x"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, diags := scan(t, tt.src)
			if len(diags) != 0 {
				t.Fatalf("scan(%q) reported:\n%s", tt.src, diags)
			}
			checkTokens(t, tt.src, toks, tt.want)
		})
	}
}

func TestNextReturnsEOFIndefinitely(t *testing.T) {
	l := lexer.New("test.c", "x")
	if got := l.Next().Kind; got != lexer.Ident {
		t.Fatalf("first token kind = %v, want %v", got, lexer.Ident)
	}
	for i := range 3 {
		if got := l.Next().Kind; got != lexer.EOF {
			t.Fatalf("token after end %d: kind = %v, want %v", i, got, lexer.EOF)
		}
	}
}

func TestKindString(t *testing.T) {
	tests := []struct {
		kind lexer.Kind
		want string
	}{
		{lexer.EOF, "end of file"},
		{lexer.Ident, "an identifier"},
		{lexer.IntLit, "an integer literal"},
		{lexer.Semicolon, "';'"},
		{lexer.ShlAssign, "'<<='"},
		{lexer.Int, "'int'"},
		{lexer.True, "'true'"},
		{lexer.Struct, "'struct'"},
		// The last kind the table names, and the first one past it. A kind with
		// no name has to render as something a reader recognizes as missing,
		// which is what TestEveryKindIsNamed detects a gap by.
		{lexer.Ellipsis, "'...'"},
		{lexer.Ellipsis + 1, "token(" + strconv.Itoa(int(lexer.Ellipsis)+1) + ")"},
		{lexer.Kind(200), "token(200)"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("Kind(%d).String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

// TestTokenDescribe covers how a diagnostic names a token it is pointing
// at. An identifier and a reserved word answer with their spelling — the
// programmer recognizes the word, not the category — and every other kind
// answers with its kind's name even though it carries a spelling too.
func TestTokenDescribe(t *testing.T) {
	tests := []struct {
		name string
		tok  lexer.Token
		want string
	}{
		{
			name: "an identifier is named by its spelling",
			tok:  lexer.Token{Kind: lexer.Ident, Text: "pressure"},
			want: "'pressure'",
		},
		{
			name: "a reserved word is named by its spelling",
			tok:  lexer.Token{Kind: lexer.Reserved, Text: "nullptr"},
			want: "'nullptr'",
		},
		{
			name: "a keyword is named by its kind",
			tok:  lexer.Token{Kind: lexer.While, Text: "while"},
			want: "'while'",
		},
		{
			name: "an operator is named by its kind",
			tok:  lexer.Token{Kind: lexer.ShlAssign, Text: "<<="},
			want: "'<<='",
		},
		{
			name: "a literal is named by its kind and not by what it spells",
			tok:  lexer.Token{Kind: lexer.IntLit, Text: "42"},
			want: "an integer literal",
		},
		{
			name: "end of file has no spelling to be named by",
			tok:  lexer.Token{Kind: lexer.EOF},
			want: "end of file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tok.Describe(); got != tt.want {
				t.Errorf("Describe() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEveryKindIsNamed(t *testing.T) {
	for k := lexer.EOF; k <= lexer.Ellipsis; k++ {
		if s := k.String(); strings.HasPrefix(s, "token(") {
			t.Errorf("kind %d has no name", k)
		}
	}
}

// c23Keywords is the full ISO/IEC 9899:2024 §6.4.1 keyword list, written
// out rather than derived from the table it checks, so a program using
// any of these names is not the C23 translation unit every MicroC
// program must be.
var c23Keywords = []struct {
	word string
	kind lexer.Kind
}{
	{"alignas", lexer.Reserved},
	{"alignof", lexer.Reserved},
	{"auto", lexer.Auto},
	{"bool", lexer.Bool},
	{"break", lexer.Break},
	{"case", lexer.Case},
	{"char", lexer.Char},
	{"const", lexer.Const},
	{"constexpr", lexer.Constexpr},
	{"continue", lexer.Continue},
	{"default", lexer.Default},
	{"do", lexer.Do},
	{"double", lexer.Double},
	{"else", lexer.Else},
	{"enum", lexer.Enum},
	{"extern", lexer.Extern},
	{"false", lexer.False},
	{"float", lexer.Float},
	{"for", lexer.For},
	{"goto", lexer.Goto},
	{"if", lexer.If},
	{"inline", lexer.Inline},
	{"int", lexer.Int},
	{"long", lexer.Long},
	{"nullptr", lexer.Reserved},
	{"register", lexer.Register},
	{"restrict", lexer.Restrict},
	{"return", lexer.Return},
	{"short", lexer.Short},
	{"signed", lexer.Signed},
	{"sizeof", lexer.Sizeof},
	{"static", lexer.Static},
	{"static_assert", lexer.Reserved},
	{"struct", lexer.Struct},
	{"switch", lexer.Switch},
	{"thread_local", lexer.Reserved},
	{"true", lexer.True},
	{"typedef", lexer.Typedef},
	{"typeof", lexer.Reserved},
	{"typeof_unqual", lexer.Reserved},
	{"union", lexer.Union},
	{"unsigned", lexer.Unsigned},
	{"void", lexer.Void},
	{"volatile", lexer.Volatile},
	{"while", lexer.While},
	{"_Alignas", lexer.Reserved},
	{"_Alignof", lexer.Reserved},
	{"_Atomic", lexer.Reserved},
	{"_BitInt", lexer.Reserved},
	{"_Bool", lexer.Reserved},
	{"_Complex", lexer.Reserved},
	{"_Decimal128", lexer.Reserved},
	{"_Decimal32", lexer.Reserved},
	{"_Decimal64", lexer.Reserved},
	{"_Generic", lexer.Reserved},
	{"_Imaginary", lexer.Reserved},
	{"_Noreturn", lexer.Reserved},
	{"_Static_assert", lexer.Reserved},
	{"_Thread_local", lexer.Reserved},
}

func TestEveryC23KeywordIsReserved(t *testing.T) {
	for _, tt := range c23Keywords {
		t.Run(tt.word, func(t *testing.T) {
			l := lexer.New("test.c", tt.word)
			tok := l.Next()
			if tok.Kind != tt.kind {
				t.Errorf("%q scans as %v, want %v", tt.word, tok.Kind, tt.kind)
			}
			if tok.Text != tt.word {
				t.Errorf("scanned %q, want the whole word", tok.Text)
			}
			if diags := l.Diagnostics(); len(diags) != 0 {
				t.Errorf("%q does not scan cleanly:\n%s", tt.word, diags)
			}
		})
	}
}

// TestNoWordOutsideC23IsReserved is the other half. A word C23 leaves alone may
// name a variable, so reserving one would refuse a program the language admits.
func TestNoWordOutsideC23IsReserved(t *testing.T) {
	// Compiler extensions, a library macro, an attribute and a macro, then the
	// near misses: a word one edit from a keyword is what a table written by
	// hand gets wrong, and a scanner matching a prefix rather than the whole
	// word would take every one of them.
	outside := []string{
		"asm", "offsetof", "noreturn", "NULL", "__attribute__", "__asm__", "__restrict",
		"_Decimal16", "_Decimal", "_BitIn", "_BitInts", "_bool", "_Boolean",
		"typeof_qual", "typeof_unqualified", "alignat", "nullptrs", "static_asserts",
		"thread_locale", "constexp", "constexprs", "Bool", "Alignas",
	}
	for _, word := range outside {
		t.Run(word, func(t *testing.T) {
			if tok := lexer.New("test.c", word).Next(); tok.Kind != lexer.Ident {
				t.Errorf("%q scans as %v, and C23 reserves no such word", word, tok.Kind)
			}
		})
	}
}
