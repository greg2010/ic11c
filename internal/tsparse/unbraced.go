package tsparse

import (
	"maps"

	"github.com/greg2010/ic11c/internal/lexer"
	"github.com/greg2010/ic11c/internal/tsnode"
	ts "github.com/tree-sitter/go-tree-sitter"
)

// MicroC admits a declaration wherever it admits a statement, including a
// control statement's unbraced body, which neither ISO C nor the C grammar
// does. So the braces are written into the source before the grammar sees
// it, which lets it always read the program. See [converter.applyMarks].

// typeWords is the first word of every type MicroC has, derived from
// [scalarTypes] so a type the language gains opens a declaration without an
// edit — a type missing here would silently skip writing the braces rather
// than raise a diagnostic.
var typeWords = func() map[lexer.Kind]bool {
	words := make(map[lexer.Kind]bool, len(scalarTypes))
	for spelling := range scalarTypes {
		words[lexer.New("", spelling).Next().Kind] = true
	}
	return words
}()

// declStarters is every token a MicroC declaration can begin with that begins
// nothing else on its own: the two specifiers, and the first word of every type.
var declStarters = func() map[lexer.Kind]bool {
	starters := map[lexer.Kind]bool{lexer.Const: true, lexer.Constexpr: true}
	maps.Copy(starters, typeWords)
	return starters
}()

// opensDeclaration reports whether the token at index i begins a declaration.
// A '[' begins one only as the first half of the '[[' an attribute opens with;
// no other MicroC construct begins with a leading bracket.
func (c *converter) opensDeclaration(i int) bool {
	if declStarters[c.ordered[i].Kind] {
		return true
	}
	return c.ordered[i].Kind == lexer.Lbrack &&
		i+1 < len(c.ordered) && c.ordered[i+1].Kind == lexer.Lbrack
}

// unbracedBodies lists the braces the declarations written as control
// statement bodies need, ascending by where each goes. A body found inside one
// already claimed is dropped: such source is already past reading, and the
// grammar's own account of it is the honest one.
func (c *converter) unbracedBodies() []inserted {
	var marks []inserted
	claimed := 0
	for i := range c.ordered {
		body, introduced := c.bodyToken(i)
		if !introduced || !c.opensDeclaration(body) {
			continue
		}
		from, to, ends := c.declSpan(body)
		if !ends || from < claimed {
			continue
		}
		claimed = to
		marks = append(marks, inserted{at: from, ch: '{'}, inserted{at: to, ch: '}'})
	}
	return marks
}

// bodyToken gives the index of the first token of what the control statement at
// i runs, and whether the token at i introduces one.
func (c *converter) bodyToken(i int) (int, bool) {
	// Default is the rule: most tokens introduce no control statement.
	//exhaustive:ignore
	switch c.ordered[i].Kind {
	case lexer.Do, lexer.Else:
		return i + 1, i+1 < len(c.ordered)
	case lexer.For, lexer.If, lexer.While:
		return c.afterHead(i + 1)
	}
	return 0, false
}

// afterHead gives the index of the token behind the parenthesized head that
// begins at k, and whether the head closes.
func (c *converter) afterHead(k int) (int, bool) {
	if k >= len(c.ordered) || c.ordered[k].Kind != lexer.Lparen {
		return 0, false
	}
	depth := 0
	for ; k < len(c.ordered); k++ {
		// Default is the rule: a head holds whatever an expression is written
		// with, and only its own parentheses bound it.
		//exhaustive:ignore
		switch c.ordered[k].Kind {
		case lexer.Lparen:
			depth++
		case lexer.Rparen:
			if depth--; depth == 0 {
				return k + 1, k+1 < len(c.ordered)
			}
		}
	}
	return 0, false
}

// declSpan gives the bytes the declaration beginning at the token body spans,
// and whether it ends. A declaration ends at the first ';' outside every
// bracket and encloses no other: the one ';' a declaration can hold is a for
// head's, which is inside parentheses.
func (c *converter) declSpan(body int) (from, to int, ends bool) {
	from = c.ordered[body].Pos.Offset
	depth := 0
	for i := body; i < len(c.ordered); i++ {
		tok := c.ordered[i]
		// Default is the rule: a declaration is written with far more than the
		// brackets that bound it.
		//exhaustive:ignore
		switch tok.Kind {
		case lexer.Lparen, lexer.Lbrack, lexer.Lbrace:
			depth++
		case lexer.Rparen, lexer.Rbrack, lexer.Rbrace:
			// A closer with nothing open ends whatever encloses the
			// declaration, which therefore has no end of its own to find and is
			// left to the grammar to report on.
			if depth--; depth < 0 {
				return 0, 0, false
			}
		case lexer.Semicolon:
			if depth == 0 {
				return from, tok.Pos.Offset + len(tok.Text), true
			}
		case lexer.EOF:
			return 0, 0, false
		}
	}
	return 0, 0, false
}

// unwrapped gives the one statement inside a block this front end wrote, and
// says whether n is such a block. A block holding anything but the declaration
// it was written around is left alone: the source inside it did not read as
// expected, and the grammar's own account of that is the honest one.
func (c *converter) unwrapped(n *ts.Node) (*ts.Node, bool) {
	if len(c.wrote) == 0 || tsnode.Kind(n.Kind()) != tsnode.KindCompoundStatement {
		return nil, false
	}
	if !c.wrote[n.StartByte()] || n.NamedChildCount() != 1 {
		return nil, false
	}
	return n.NamedChild(0), true
}
