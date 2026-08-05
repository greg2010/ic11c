package tsparse

import (
	"strings"

	"github.com/greg2010/ic11c/internal/lexer"
	"github.com/greg2010/ic11c/internal/tsnode"
)

// The C grammar reserves words MicroC does not (asm, offsetof, __attribute__,
// NULL, TRUE/FALSE, size_t and friends), which internal/lexer reads as
// ordinary identifiers. Each occurrence is masked to same-length underscores
// before the grammar sees the source, so no position moves. See [converter.text].

// reservedWords are the words the grammar reserves that MicroC does not,
// derived from the grammar's rules rather than hand-listed so a word either
// front end gains updates the set on its own.
var reservedWords = func() map[string]bool {
	words := map[string]bool{}
	for _, spelling := range tsnode.Spellings {
		if namesAVariable(spelling) {
			words[spelling] = true
		}
	}
	return words
}()

// namesAVariable reports whether MicroC leaves a word alone, which is what the
// lexer answers by scanning it as one whole identifier.
func namesAVariable(text string) bool {
	l := lexer.New("", text)
	if tok := l.Next(); tok.Kind != lexer.Ident || tok.Text != text {
		return false
	}
	return l.Next().Kind == lexer.EOF
}

// masked answers the source with every word in [reservedWords] written over,
// and is the source itself where it holds none.
func (c *converter) masked() string {
	var b []byte
	for _, tok := range c.ordered {
		if tok.Kind != lexer.Ident || !reservedWords[tok.Text] {
			continue
		}
		if b == nil {
			b = []byte(c.src)
		}
		copy(b[tok.Pos.Offset:], strings.Repeat("_", len(tok.Text)))
	}
	if b == nil {
		return c.src
	}
	return string(b)
}
