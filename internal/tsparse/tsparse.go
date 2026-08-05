// Package tsparse builds a MicroC syntax tree using the tree-sitter C
// grammar, which is C's rather than MicroC's: a construct C admits and
// MicroC does not is refused by name (refusals.go), and a mismatch between
// the two grammars is patched by rewriting source bytes before the parse.
package tsparse

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/lexer"
	"github.com/greg2010/ic11c/internal/source"
	"github.com/greg2010/ic11c/internal/tsnode"
	ts "github.com/tree-sitter/go-tree-sitter"
	tsc "github.com/tree-sitter/tree-sitter-c/bindings/go"
)

// maxDiagnostics caps how many problems a parse reports, covering both the
// lexer's diagnostics and the converter's own. Past this point the output is
// noise rather than information.
const maxDiagnostics = 64

// language is the compiled C grammar. It is immutable and shared: building it
// per parse would copy the whole parse table for nothing.
var language = ts.NewLanguage(tsc.Language())

// Parse parses src as a MicroC translation unit. file names the source in
// positions and diagnostics and is never opened. The returned file is never
// nil; diagnostics are ordered by position and capped at [maxDiagnostics].
// The error return reports a defect in the compiler, not in the source.
func Parse(file, src string) (*ast.File, source.DiagnosticList, error) {
	return parse(file, src, maxNestingDepth)
}

// parse is [Parse] with the nesting limit named rather than shipped, which is
// how the bound is held to what it reports without a source large enough to
// reach the real one.
func parse(file, src string, maxDepth int) (*ast.File, source.DiagnosticList, error) {
	p := ts.NewParser()
	defer p.Close()
	if err := p.SetLanguage(language); err != nil {
		return nil, nil, fmt.Errorf("configuring the C grammar: %w", err)
	}
	c := newConverter(file, src)
	tree, err := c.read(p)
	if err != nil {
		return nil, nil, err
	}
	defer tree.Close()

	// The depth is measured first because both walks below it recurse, and a
	// file past the limit is answered with the position rather than descended
	// into.
	root := tree.RootNode()
	var f *ast.File
	nesting, deep := c.nestingRefusal(root, maxDepth)
	if deep {
		f = &ast.File{Name: c.file, Start: c.fileStart()}
	} else {
		c.reportSyntaxErrors(root)
		c.reportRelexedTokens(root)
		f = c.translationUnit(root)
	}
	c.diags.Sort()
	if len(c.diags) > maxDiagnostics {
		stopped := c.diags[maxDiagnostics].Pos
		c.diags = c.diags[:maxDiagnostics]
		c.diags.Overflow(stopped, source.Error)
	}
	if deep {
		c.diags = append(source.DiagnosticList{nesting}, c.diags...)
	}
	return f, c.diags, nil
}

// inserted is one byte this front end writes into the source, at an offset into
// the source as the programmer wrote it. The byte goes in front of that offset.
type inserted struct {
	at int
	ch byte
}

// read gives the tree the grammar builds for the source, having written in
// the braces an unbraced declaration needs and the parentheses that turn an
// expression the grammar will not build into one it will, reparsing once per
// round until a round wraps nothing new. The returned tree is the caller's to close.
func (c *converter) read(p *ts.Parser) (*ts.Tree, error) {
	marks := c.unbracedBodies()
	wrapped := map[span]bool{}
	for {
		tree := p.Parse([]byte(c.applyMarks(marks)), nil)
		if tree == nil {
			return nil, fmt.Errorf("parsing %s: the grammar produced no tree", c.file)
		}
		extra := c.rewrittenExprs(tree.RootNode(), wrapped)
		if len(extra) == 0 {
			return tree, nil
		}
		tree.Close()
		marks = append(marks, extra...)
		slices.SortStableFunc(marks, func(a, b inserted) int { return cmp.Compare(a.at, b.at) })
	}
}

// applyMarks answers the source the grammar reads: [converter.words] with
// marks written into it, ascending by where each goes. It records where they
// landed, which [converter.sourceOffset] and [converter.unwrapped] use to map
// back to the source as written.
func (c *converter) applyMarks(marks []inserted) string {
	c.added, c.wrote = nil, nil
	if len(marks) == 0 {
		return c.words
	}
	c.wrote = make(map[uint]bool, len(marks)/2)
	c.added = make([]int, 0, len(marks))

	var b strings.Builder
	b.Grow(len(c.words) + len(marks))
	prev := 0
	for _, m := range marks {
		b.WriteString(c.words[prev:m.at])
		if m.ch == '{' {
			c.wrote[uint(b.Len())] = true
		}
		c.added = append(c.added, b.Len())
		b.WriteByte(m.ch)
		prev = m.at
	}
	b.WriteString(c.words[prev:])
	return b.String()
}

// converter turns one tree-sitter tree into one syntax tree.
type converter struct {
	file string
	src  string
	// words is src with every word the grammar reserves and MicroC does not
	// masked to underscores; src itself where it holds none. See words.go.
	words string
	// lines holds the byte offset each line begins at, indexed by line-1.
	lines []int
	// tokens indexes the lexer's tokens by byte offset.
	tokens map[int]lexer.Token
	// ordered holds the same tokens in source order, ending with the
	// end-of-file token.
	ordered []lexer.Token
	diags   source.DiagnosticList
	// seen holds the offsets already carrying a diagnostic, so one mistake the
	// lexer and the grammar both notice costs one message.
	seen map[int]bool
	// unreadable is the offset from which the lexer stopped producing tokens,
	// or len(src) where it read to the end. See [unreadableFrom].
	unreadable int
	// unclosed and mismatch hold what the bracket prepass found, and scanned
	// says it has run. See [converter.balance].
	unclosed []balanceOpener
	mismatch *balanceMismatch
	scanned  bool
	// added holds the offsets, in the source the grammar read, of the braces
	// this front end wrote, ascending; wrote holds the opening ones among them.
	// See [converter.applyMarks].
	added []int
	wrote map[uint]bool
}

func newConverter(file, src string) *converter {
	l := lexer.New(file, src)
	tokens := map[int]lexer.Token{}
	var ordered []lexer.Token
	for {
		t := l.Next()
		ordered = append(ordered, t)
		if t.Kind == lexer.EOF {
			break
		}
		tokens[t.Pos.Offset] = t
	}
	diags := l.Diagnostics()

	seen := make(map[int]bool, len(diags))
	for _, d := range diags {
		seen[d.Pos.Offset] = true
	}

	lines := []int{0}
	for i := range len(src) {
		if src[i] == '\n' {
			lines = append(lines, i+1)
		}
	}
	c := &converter{
		file:       file,
		src:        src,
		lines:      lines,
		tokens:     tokens,
		ordered:    ordered,
		diags:      diags,
		seen:       seen,
		unreadable: unreadableFrom(src, ordered, diags),
	}
	c.words = c.masked()
	return c
}

// unreadableFrom gives the offset from which the lexer read no source: one
// past len(src), or earlier if an unterminated comment or literal stopped it
// with a diagnostic at or past its last token's start — which is what keeps
// the grammar's own reading of that text as code from being reported too.
func unreadableFrom(src string, ordered []lexer.Token, diags source.DiagnosticList) int {
	start := 0
	if n := len(ordered); n > 1 {
		start = ordered[n-2].Pos.Offset
	}
	from := len(src) + 1
	for _, d := range diags {
		if d.Pos.Offset >= start && d.Pos.Offset < from {
			from = d.Pos.Offset
		}
	}
	return from
}

// pos gives the position of a byte offset into the source the grammar read.
func (c *converter) pos(offset uint) source.Position {
	return c.posOf(c.sourceOffset(offset))
}

// posOf gives the position of a byte offset into the source as written,
// counting columns in bytes from the start of the line exactly as the lexer
// does.
func (c *converter) posOf(off int) source.Position {
	line, exact := slices.BinarySearch(c.lines, off)
	if !exact {
		line--
	}
	return source.Position{File: c.file, Offset: off, Line: line + 1, Column: off - c.lines[line] + 1}
}

// sourceOffset maps a byte offset into the source the grammar read back to
// the source as written, discounting the braces this front end added in
// front of it. Every position and token goes through here, so nothing
// outside this file has to know the two sources differ. See [converter.applyMarks].
func (c *converter) sourceOffset(offset uint) int {
	off := int(offset)
	if len(c.added) == 0 {
		return off
	}
	before, _ := slices.BinarySearch(c.added, off)
	return off - before
}

// start gives the position of a node's first byte.
func (c *converter) start(n *ts.Node) source.Position { return c.pos(n.StartByte()) }

// token gives the token the lexer scanned at a byte offset. It is absent only
// where the two disagree about where a token begins, which the lexer has
// already reported as a character it could not read.
func (c *converter) token(offset uint) (lexer.Token, bool) {
	t, ok := c.tokens[c.sourceOffset(offset)]
	return t, ok
}

// text is the source a node spans, as the programmer wrote it.
func (c *converter) text(n *ts.Node) string {
	return c.src[c.sourceOffset(n.StartByte()):c.sourceOffset(n.EndByte())]
}

// errorf records a problem at pos, dropping a second one at a byte that
// already carries a diagnostic or lies past the unreadable tail — the lexer,
// the grammar's error nodes, and the conversion each read the source once and
// may all notice the same mistake.
func (c *converter) errorf(pos source.Position, format string, args ...any) {
	if pos.Offset > c.unreadable || c.seen[pos.Offset] {
		return
	}
	c.seen[pos.Offset] = true
	c.diags.Addf(pos, format, args...)
}

// infixRefusals are the constructs [refusals] names that C writes between two
// expressions rather than around one, derived from the grammar's own field
// slots so a construct C gains joins the set without an edit.
var infixRefusals = func() map[tsnode.Kind]bool {
	set := map[tsnode.Kind]bool{}
	for kind := range refusals {
		slots := tsnode.FieldTypes[kind]
		_, operator := slots[tsnode.FieldOperator]
		_, left := slots[tsnode.FieldLeft]
		_, right := slots[tsnode.FieldRight]
		if operator || (left && right) {
			set[kind] = true
		}
	}
	return set
}()

// refuse reports a construct the C grammar admits and MicroC does not, naming
// it with whatever [refusals] says about it.
func (c *converter) refuse(n *ts.Node) {
	if msg, named := c.refusalFor(n); named {
		c.errorf(c.refusedAt(n), "%s", msg)
		return
	}
	// Punctuation reaching a dispatch is a token the grammar's recovery left
	// loose, not a construct, and MicroC has nothing to say about whether it
	// supports a semicolon.
	if !n.IsNamed() {
		c.errorf(c.start(n), "%s is not expected here", c.describeAt(n.StartByte()))
		return
	}
	c.errorf(c.start(n), "%s is not supported in MicroC", readable(tsnode.Kind(n.Kind())))
}

// refusedAt gives the position a refusal reports at: the joining token for a
// construct written between two expressions ([infixRefusals]), since that is
// the byte the reader has to look at, and the construct's own first byte
// otherwise.
func (c *converter) refusedAt(n *ts.Node) source.Position {
	if !infixRefusals[tsnode.Kind(n.Kind())] {
		return c.start(n)
	}
	if op := field(n, tsnode.FieldOperator); op != nil {
		return c.start(op)
	}
	left, right := field(n, tsnode.FieldLeft), field(n, tsnode.FieldRight)
	if left == nil || right == nil {
		return c.start(n)
	}
	for _, ch := range c.children(n) {
		if ch.node.StartByte() >= left.EndByte() && ch.node.EndByte() <= right.StartByte() {
			return c.start(ch.node)
		}
	}
	return c.start(n)
}

// bad reports whether a node stands where the grammar could not read the
// source, which is what a Bad node is built from.
func bad(n *ts.Node) bool { return n.IsError() || n.IsMissing() }

// child is one child of a node together with the field the grammar gave it,
// which is empty for a child the grammar names no slot for.
type child struct {
	node  *ts.Node
	kind  tsnode.Kind
	field tsnode.Field
}

// children lists a node's children in source order, both named and anonymous
// (const vs constexpr differ only in their anonymous child), with kinds in
// [tsnode.Extras] left out. It walks with a cursor rather than indexing, which
// would be quadratic in the width of an error node holding every token.
func (c *converter) children(n *ts.Node) []child {
	out := make([]child, 0, n.ChildCount())
	cursor := n.Walk()
	defer cursor.Close()
	for more := cursor.GotoFirstChild(); more; more = cursor.GotoNextSibling() {
		node := cursor.Node()
		kind := tsnode.Kind(node.Kind())
		if extras[kind] {
			continue
		}
		out = append(out, child{node: node, kind: kind, field: tsnode.Field(cursor.FieldName())})
	}
	return out
}

// field gives the node filling one slot of n, or nil where the slot is empty.
func field(n *ts.Node, f tsnode.Field) *ts.Node { return n.ChildByFieldName(string(f)) }

// anonymous gives the position of the first anonymous child of n spelled k.
// Punctuation the tree does not name a field is how several nodes locate
// themselves: the '[' of a subscript and the '?' of a conditional are the
// positions those expressions report.
func (c *converter) anonymous(n *ts.Node, k tsnode.Kind) (source.Position, bool) {
	for _, ch := range c.children(n) {
		if ch.kind == k {
			return c.start(ch.node), true
		}
	}
	return source.Position{}, false
}

// extras is [tsnode.Extras] as a set, which is how a walk over children tests
// one.
var extras = func() map[tsnode.Kind]bool {
	set := make(map[tsnode.Kind]bool, len(tsnode.Extras))
	for _, kind := range tsnode.Extras {
		set[kind] = true
	}
	return set
}()

// readable spells a kind the way a diagnostic should name it: a grammar rule
// reads as words, and an anonymous node as the text it is written with.
func readable(k tsnode.Kind) string { return strings.ReplaceAll(string(k), "_", " ") }
