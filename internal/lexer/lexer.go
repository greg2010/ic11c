// Package lexer turns MicroC source text into tokens.
//
// Scanning never stops at the first malformed construct — the bytes are
// consumed, a diagnostic recorded, and scanning continues.
package lexer

import (
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/greg2010/ic11c/internal/source"
)

// Lexer scans one source file. It is not safe for concurrent use.
type Lexer struct {
	file      string
	src       string
	off       int
	line      int
	lineStart int
	diags     source.DiagnosticList
}

// New returns a Lexer over src. file names the source in positions and
// diagnostics and is never opened.
func New(file, src string) *Lexer {
	return &Lexer{file: file, src: src, line: 1}
}

// Next returns the next token, skipping whitespace and comments, and returns
// EOF indefinitely once the source is exhausted. It never fails: malformed
// input yields a diagnostic and a best-effort token, or is skipped.
func (l *Lexer) Next() Token {
	for {
		l.skipSpaceAndComments()
		if l.off >= len(l.src) {
			return Token{Kind: EOF, Pos: l.posAt(l.off)}
		}
		start := l.off
		c := l.src[start]
		switch {
		case isIdentStart(c):
			return l.scanIdent()
		case isDigit(c):
			return l.scanNumber()
		case c == '.' && start+1 < len(l.src) && isDigit(l.src[start+1]):
			return l.scanNumber()
		case c == '\'':
			return l.scanChar()
		case c == '"':
			return l.scanString()
		case c == '#':
			l.diags.Addf(l.posAt(start), "preprocessor directives are not supported in MicroC")
			l.skipToEndOfLine()
			continue
		}
		if tok, ok := l.scanOperator(); ok {
			return tok
		}
		r, size := utf8.DecodeRuneInString(l.src[start:])
		l.diags.Addf(l.posAt(start), "unexpected character %s", strconv.QuoteRune(r))
		l.off = start + size
	}
}

// Diagnostics returns the lexical errors recorded so far. The list is complete
// only once Next has returned EOF.
func (l *Lexer) Diagnostics() source.DiagnosticList { return l.diags }

func (l *Lexer) posAt(off int) source.Position {
	return source.Position{File: l.file, Offset: off, Line: l.line, Column: off - l.lineStart + 1}
}

func (l *Lexer) skipSpaceAndComments() {
	for l.off < len(l.src) {
		c := l.src[l.off]
		switch {
		case c == '\n':
			l.newline()
		case c == ' ' || c == '\t' || c == '\r' || c == '\v' || c == '\f':
			l.off++
		case c == '/' && l.off+1 < len(l.src) && l.src[l.off+1] == '/':
			l.skipToEndOfLine()
		case c == '/' && l.off+1 < len(l.src) && l.src[l.off+1] == '*':
			l.skipBlockComment()
		default:
			return
		}
	}
}

// newline consumes the newline at the scan point and opens a new line, which is
// the only place Line and Column advance discontinuously.
func (l *Lexer) newline() {
	l.off++
	l.line++
	l.lineStart = l.off
}

func (l *Lexer) skipToEndOfLine() {
	for l.off < len(l.src) && l.src[l.off] != '\n' {
		l.off++
	}
}

func (l *Lexer) skipBlockComment() {
	start := l.posAt(l.off)
	l.off += 2
	for l.off < len(l.src) {
		switch {
		case l.src[l.off] == '\n':
			l.newline()
		case l.src[l.off] == '*' && l.off+1 < len(l.src) && l.src[l.off+1] == '/':
			l.off += 2
			return
		default:
			l.off++
		}
	}
	l.diags.Addf(start, "unterminated block comment")
}

func (l *Lexer) scanIdent() Token {
	start := l.off
	for l.off < len(l.src) && isIdentChar(l.src[l.off]) {
		l.off++
	}
	text := l.src[start:l.off]
	kind := Ident
	if k, ok := keywords[text]; ok {
		kind = k
	}
	return Token{Kind: kind, Pos: l.posAt(start), Text: text}
}

func (l *Lexer) scanOperator() (Token, bool) {
	rest := l.src[l.off:]
	for _, op := range operators {
		if strings.HasPrefix(rest, op.text) {
			tok := Token{Kind: op.kind, Pos: l.posAt(l.off), Text: op.text}
			l.off += len(op.text)
			return tok, true
		}
	}
	return Token{}, false
}

func (l *Lexer) scanNumber() Token {
	start := l.off
	if l.src[l.off] == '0' && l.off+1 < len(l.src) && (l.src[l.off+1]|0x20) == 'x' {
		return l.scanHexNumber(start)
	}
	for l.off < len(l.src) && isDigit(l.src[l.off]) {
		l.off++
	}
	if l.atFloatTail() {
		return l.scanFloat(start)
	}

	digits := l.src[start:l.off]
	pos := l.posAt(start)
	l.rejectLiteralSuffix("integer literal")
	tok := Token{Kind: IntLit, Pos: pos, Text: l.src[start:l.off]}
	if len(digits) > 1 && digits[0] == '0' {
		l.diags.Addf(pos, "octal literals are not supported in MicroC; write %s in decimal or hexadecimal", digits)
	}
	v, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		l.diags.Addf(pos, "integer literal %s does not fit in a 64-bit signed integer", digits)
		return tok
	}
	tok.Int = v
	return tok
}

func (l *Lexer) scanHexNumber(start int) Token {
	l.off += 2
	digitStart := l.off
	for l.off < len(l.src) && isHexDigit(l.src[l.off]) {
		l.off++
	}
	digits := l.src[digitStart:l.off]
	pos := l.posAt(start)
	l.rejectLiteralSuffix("integer literal")
	tok := Token{Kind: IntLit, Pos: pos, Text: l.src[start:l.off]}
	if digits == "" {
		l.diags.Addf(pos, "hexadecimal literal has no digits")
		return tok
	}
	v, err := strconv.ParseUint(digits, 16, 64)
	if err != nil || v > math.MaxInt64 {
		l.diags.Addf(pos, "integer literal %s does not fit in a 64-bit signed integer", tok.Text)
		return tok
	}
	tok.Int = int64(v)
	return tok
}

// atFloatTail reports whether the scan point continues a floating-point literal
// whose integer part has just been consumed.
func (l *Lexer) atFloatTail() bool {
	if l.off < len(l.src) && l.src[l.off] == '.' {
		return true
	}
	_, ok := l.exponentDigits(l.off)
	return ok
}

// exponentDigits reports whether an exponent begins at off, and gives the
// offset of its first digit. An 'e' with no digit after it is not an exponent:
// it is a suffix on the literal, which is rejected as one.
func (l *Lexer) exponentDigits(off int) (int, bool) {
	if off >= len(l.src) || l.src[off]|0x20 != 'e' {
		return 0, false
	}
	digits := off + 1
	if digits < len(l.src) && (l.src[digits] == '+' || l.src[digits] == '-') {
		digits++
	}
	if digits >= len(l.src) || !isDigit(l.src[digits]) {
		return 0, false
	}
	return digits, true
}

// scanFloat consumes a floating-point literal, which has type double.
// Exponent notation is admitted even though the chip's own number parser
// has none: the emitter always writes a decimal expansion, so source
// spelling and emitted spelling are separate questions.
func (l *Lexer) scanFloat(start int) Token {
	if l.off < len(l.src) && l.src[l.off] == '.' {
		l.off++
		for l.off < len(l.src) && isDigit(l.src[l.off]) {
			l.off++
		}
	}
	if digitStart, ok := l.exponentDigits(l.off); ok {
		l.off = digitStart
		for l.off < len(l.src) && isDigit(l.src[l.off]) {
			l.off++
		}
	}
	digits := l.src[start:l.off]
	pos := l.posAt(start)
	l.rejectLiteralSuffix("floating-point literal")
	tok := Token{Kind: FloatLit, Pos: pos, Text: l.src[start:l.off]}

	// The scan admits only spellings ParseFloat accepts, so its sole failure is
	// a range error, and the value it answers with says which one. An underflow
	// keeps the zero, which is what IEEE gives the same expression at run time.
	// An overflow does not: the literal names a magnitude no double holds.
	v, _ := strconv.ParseFloat(digits, 64)
	if math.IsInf(v, 0) {
		l.diags.Addf(pos, "floating-point literal %s is larger than a double holds", digits)
		return tok
	}
	tok.Float = v
	return tok
}

// rejectLiteralSuffix consumes and reports identifier characters butted against
// a literal, which covers C's integer and float suffixes, C23 binary literals,
// and plain typos alike.
func (l *Lexer) rejectLiteralSuffix(what string) {
	if l.off >= len(l.src) || !isIdentChar(l.src[l.off]) {
		return
	}
	start := l.off
	for l.off < len(l.src) && isIdentChar(l.src[l.off]) {
		l.off++
	}
	l.diags.Addf(l.posAt(start), "invalid suffix %q on %s", l.src[start:l.off], what)
}

func (l *Lexer) scanChar() Token {
	start := l.off
	l.off++
	var value int64
	count := 0
	for l.off < len(l.src) && l.src[l.off] != '\'' && l.src[l.off] != '\n' {
		value = l.scanCharValue()
		count++
	}
	closed := l.off < len(l.src) && l.src[l.off] == '\''
	if closed {
		l.off++
	}
	pos := l.posAt(start)
	tok := Token{Kind: CharLit, Pos: pos, Text: l.src[start:l.off], Int: value}
	switch {
	case !closed:
		l.diags.Addf(pos, "unterminated character literal")
	case count == 0:
		l.diags.Addf(pos, "empty character literal")
	case count > 1:
		l.diags.Addf(pos, "character literal contains more than one character")
	}
	return tok
}

func (l *Lexer) scanString() Token {
	start := l.off
	l.off++
	var b strings.Builder
	for l.off < len(l.src) && l.src[l.off] != '"' && l.src[l.off] != '\n' {
		if l.src[l.off] == '\\' {
			b.WriteByte(byte(l.scanCharValue()))
			continue
		}
		_, size := utf8.DecodeRuneInString(l.src[l.off:])
		b.WriteString(l.src[l.off : l.off+size])
		l.off += size
	}
	closed := l.off < len(l.src) && l.src[l.off] == '"'
	if closed {
		l.off++
	}
	pos := l.posAt(start)
	if !closed {
		l.diags.Addf(pos, "unterminated string literal")
	}
	return Token{Kind: StringLit, Pos: pos, Text: l.src[start:l.off], Str: b.String()}
}

// scanCharValue consumes one character or escape sequence and returns its
// value, always consuming at least one byte so callers' loops progress.
// A literal character must be ASCII; a string literal copies runes as
// UTF-8 without reaching this branch.
func (l *Lexer) scanCharValue() int64 {
	if l.src[l.off] != '\\' {
		start := l.off
		r, size := utf8.DecodeRuneInString(l.src[l.off:])
		l.off += size
		if r > unicode.MaxASCII {
			l.diags.Addf(l.posAt(start), "a character literal holds one ASCII character, and %s is not one", strconv.QuoteRune(r))
			return 0
		}
		return int64(r)
	}
	escStart := l.off
	l.off++
	if l.off >= len(l.src) || l.src[l.off] == '\n' {
		l.diags.Addf(l.posAt(escStart), "unterminated escape sequence")
		return 0
	}
	c := l.src[l.off]
	l.off++
	switch c {
	case 'a':
		return 0x07
	case 'b':
		return 0x08
	case 'f':
		return 0x0c
	case 'n':
		return 0x0a
	case 'r':
		return 0x0d
	case 't':
		return 0x09
	case 'v':
		return 0x0b
	case '\\', '\'', '"', '?':
		return int64(c)
	case 'x':
		return l.scanHexEscape(escStart)
	case '0', '1', '2', '3', '4', '5', '6', '7':
		return l.scanOctalEscape(escStart, int64(c-'0'))
	default:
		l.diags.Addf(l.posAt(escStart), "unknown escape sequence %q", l.src[escStart:l.off])
		return int64(c)
	}
}

func (l *Lexer) scanHexEscape(escStart int) int64 {
	digitStart := l.off
	for l.off < len(l.src) && isHexDigit(l.src[l.off]) {
		l.off++
	}
	if digitStart == l.off {
		l.diags.Addf(l.posAt(escStart), "hexadecimal escape has no digits")
		return 0
	}
	v, err := strconv.ParseUint(l.src[digitStart:l.off], 16, 32)
	if err != nil || v > 0xff {
		l.diags.Addf(l.posAt(escStart), "hexadecimal escape %q is out of range", l.src[escStart:l.off])
		return 0
	}
	return int64(v)
}

func (l *Lexer) scanOctalEscape(escStart int, v int64) int64 {
	for i := 0; i < 2 && l.off < len(l.src) && isOctalDigit(l.src[l.off]); i++ {
		v = v*8 + int64(l.src[l.off]-'0')
		l.off++
	}
	if v > 0xff {
		l.diags.Addf(l.posAt(escStart), "octal escape %q is out of range", l.src[escStart:l.off])
		return 0
	}
	return v
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isOctalDigit(c byte) bool { return c >= '0' && c <= '7' }

func isHexDigit(c byte) bool {
	return isDigit(c) || (c|0x20) >= 'a' && (c|0x20) <= 'f'
}

func isIdentStart(c byte) bool {
	return c == '_' || (c|0x20) >= 'a' && (c|0x20) <= 'z'
}

func isIdentChar(c byte) bool { return isIdentStart(c) || isDigit(c) }
