package tsparse

import (
	"fmt"

	"github.com/greg2010/ic11c/internal/lexer"
	"github.com/greg2010/ic11c/internal/source"
)

// closerForOpener gives the closer each tracked opener takes, and doubles as the
// set of openers.
var closerForOpener = map[byte]byte{'(': ')', '[': ']', '{': '}'}

// openerForCloser inverts closerForOpener, so a stray closer can name the opener
// it wanted.
var openerForCloser = map[byte]byte{')': '(', ']': '[', '}': '{'}

// bracketBytes spells each bracket token the way a diagnostic writes it.
var bracketBytes = map[lexer.Kind]byte{
	lexer.Lparen: '(',
	lexer.Lbrack: '[',
	lexer.Lbrace: '{',
	lexer.Rparen: ')',
	lexer.Rbrack: ']',
	lexer.Rbrace: '}',
}

// balanceOpener is one opener the walk is inside, with the position to blame if
// nothing closes it.
type balanceOpener struct {
	ch  byte
	pos source.Position
}

// unclosed is what a diagnostic says about an opener nothing closes. It is one
// sentence in one place so that the check and the front end that consults it
// cannot come to word the same finding differently.
func (b balanceOpener) unclosed() string {
	return fmt.Sprintf("unclosed '%c'; no matching '%c' before end of file", b.ch, closerForOpener[b.ch])
}

// balanceMismatch is a closer the walk could not pair: either the opener it is
// inside takes a different one, or it is inside no opener at all, which opener
// says by being nil.
type balanceMismatch struct {
	opener *balanceOpener
	closer byte
	pos    source.Position
}

// String is what a diagnostic says about the closer, named after the opener it
// was measured against wherever there was one. That opener is what the reader
// has to look at, and it is usually not on this line.
func (m balanceMismatch) String() string {
	if m.opener == nil {
		return fmt.Sprintf("no opening '%c' matches this '%c'", openerForCloser[m.closer], m.closer)
	}
	return fmt.Sprintf("expected '%c' to close the '%c' at %d:%d, found '%c'",
		closerForOpener[m.opener.ch], m.opener.ch, m.opener.pos.Line, m.opener.pos.Column, m.closer)
}

// bracketBalance walks tokens and answers the openers nothing closes together
// with the closer that pairs with none. The two are exclusive: a walk that met
// an unpaired closer stops there and answers no openers. Unclosed openers are
// answered outermost first, all of them.
func bracketBalance(tokens []lexer.Token) ([]balanceOpener, *balanceMismatch) {
	var open []balanceOpener
	for _, tok := range tokens {
		ch, bracket := bracketBytes[tok.Kind]
		if !bracket {
			continue
		}
		if _, opens := closerForOpener[ch]; opens {
			open = append(open, balanceOpener{ch: ch, pos: tok.Pos})
			continue
		}
		if len(open) == 0 {
			return nil, &balanceMismatch{closer: ch, pos: tok.Pos}
		}
		// The opener is copied out inside the branch rather than in front of it,
		// so that taking its address does not move one to the heap per closer.
		top := len(open) - 1
		if closerForOpener[open[top].ch] != ch {
			opener := open[top]
			return nil, &balanceMismatch{opener: &opener, closer: ch, pos: tok.Pos}
		}
		open = open[:top]
	}
	return open, nil
}
