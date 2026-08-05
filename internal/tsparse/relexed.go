package tsparse

import (
	"cmp"
	"slices"

	"github.com/greg2010/ic11c/internal/lexer"
	ts "github.com/tree-sitter/go-tree-sitter"
)

// The C grammar's lexer reads a token by parse state, so a lexeme
// internal/lexer scans whole is sometimes scanned again as its parts: the
// '&&' of "return a || && !a;" reads as two address-of operators. This file
// holds the tree to the lexer's own tokens, refusing a node built from part of one.

// tokenAt gives the lexer token covering a byte offset into the source as
// written, and says whether one covers it. Whitespace, a comment, and the source
// behind the last token are covered by none.
func (c *converter) tokenAt(off int) (lexer.Token, bool) {
	i, exact := slices.BinarySearchFunc(c.ordered, off, func(t lexer.Token, at int) int {
		return cmp.Compare(t.Pos.Offset, at)
	})
	if exact {
		return c.ordered[i], true
	}
	if i == 0 {
		return lexer.Token{}, false
	}
	if prev := c.ordered[i-1]; off < prev.Pos.Offset+len(prev.Text) {
		return prev, true
	}
	return lexer.Token{}, false
}

// partOfToken reports whether a node covers less than the whole lexer token it
// begins in — the grammar having read one token as several. A node covering
// no source of its own (a missing node, or one holding only bytes this front
// end wrote) is never one.
func (c *converter) partOfToken(n *ts.Node) bool {
	start, end := c.sourceOffset(n.StartByte()), c.sourceOffset(n.EndByte())
	if start == end {
		return false
	}
	tok, covered := c.tokenAt(start)
	if !covered {
		return false
	}
	return start > tok.Pos.Offset || end < tok.Pos.Offset+len(tok.Text)
}

// reportRelexedTokens names every token the grammar read as more than one.
// The walk stops at the first node lying within a token, so a literal's own
// parts (quotes, escapes) are not reported again as relexing.
func (c *converter) reportRelexedTokens(root *ts.Node) {
	cursor := root.Walk()
	defer cursor.Close()
	for {
		if c.reportRelexed(cursor.Node()) && cursor.GotoFirstChild() {
			continue
		}
		for !cursor.GotoNextSibling() {
			if !cursor.GotoParent() {
				return
			}
		}
	}
}

// reportRelexed names the token a node was built from inside of, and says
// whether the walk should go on into the node's children.
func (c *converter) reportRelexed(n *ts.Node) bool {
	start, end := c.sourceOffset(n.StartByte()), c.sourceOffset(n.EndByte())
	if start == end {
		return false
	}
	tok, covered := c.tokenAt(start)
	if !covered {
		return true
	}
	if start > tok.Pos.Offset {
		c.errorf(tok.Pos, "%s is not expected here", tok.Describe())
		return false
	}
	return end > tok.Pos.Offset+len(tok.Text)
}
