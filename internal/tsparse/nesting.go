package tsparse

import (
	"fmt"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/source"
	ts "github.com/tree-sitter/go-tree-sitter"
)

// maxNestingDepth is how many constructs may enclose one another before the
// parse declines to read the file at all. It applies [ast.MaxNestingDepth],
// the bound every pass over the tree is held to, to the grammar's own finer
// nodes, so a file it admits stays inside the bound however conversion folds it.
const maxNestingDepth = ast.MaxNestingDepth

// nestingRefusal gives the diagnostic naming the construct at which a file
// nests deeper than limit admits, and says whether there was one: the
// outermost construct past the limit. It is returned rather than recorded so
// the caller can put it in front of everything else once the list is cut.
func (c *converter) nestingRefusal(root *ts.Node, limit int) (source.Diagnostic, bool) {
	offset, deep := deeperThan(root, limit)
	if !deep {
		return source.Diagnostic{}, false
	}
	return source.Diagnostic{
		Pos: c.pos(offset),
		Msg: fmt.Sprintf("nested too deeply; the compiler reads at most %d constructs inside one another", limit),
	}, true
}

// deeperThan gives the first byte, in reading order, of the first node more
// than limit levels inside root, and says whether root holds one. The walk is
// a cursor rather than a recursion: establishing that recursive walks are safe
// cannot itself be one.
func deeperThan(root *ts.Node, limit int) (uint, bool) {
	cursor := root.Walk()
	defer cursor.Close()
	depth := 0
	for {
		if depth > limit {
			return cursor.Node().StartByte(), true
		}
		if cursor.GotoFirstChild() {
			depth++
			continue
		}
		for !cursor.GotoNextSibling() {
			if !cursor.GotoParent() {
				return 0, false
			}
			depth--
		}
	}
}
