package tsparse

import (
	"cmp"
	"slices"
	"strings"

	"github.com/greg2010/ic11c/internal/lexer"
	"github.com/greg2010/ic11c/internal/source"
	"github.com/greg2010/ic11c/internal/tsnode"
	ts "github.com/tree-sitter/go-tree-sitter"
)

// This file turns the two things the grammar says about a source it could
// not read into the sentences the compiler says about it: a missing node,
// which names the token wanted and sits where it belonged, and an error
// node, whose parse state is zero and leaves only a byte range and its children.

// refusedTokens maps the keyword ISO C spells an excluded construct with to
// the construct itself. A construct the grammar finished reading arrives as
// its own node and is refused by kind; one it could not finish leaves the
// keyword loose inside an error node instead, with no node above it to name.
var refusedTokens = map[tsnode.Kind]tsnode.Kind{
	tsnode.KindAlignas:          tsnode.KindAlignasQualifier,
	tsnode.KindAlignof:          tsnode.KindAlignofExpression,
	tsnode.KindAuto:             tsnode.KindStorageClassSpecifier,
	tsnode.KindEnum:             tsnode.KindEnumSpecifier,
	tsnode.KindExtern:           tsnode.KindStorageClassSpecifier,
	tsnode.KindGoto:             tsnode.KindGotoStatement,
	tsnode.KindHashDefine:       tsnode.KindPreprocDef,
	tsnode.KindHashIf:           tsnode.KindPreprocIf,
	tsnode.KindHashIfdef:        tsnode.KindPreprocIfdef,
	tsnode.KindHashIfndef:       tsnode.KindPreprocIfdef,
	tsnode.KindHashInclude:      tsnode.KindPreprocInclude,
	tsnode.KindInline:           tsnode.KindStorageClassSpecifier,
	tsnode.KindNullptr:          tsnode.KindNull,
	tsnode.KindRegister:         tsnode.KindStorageClassSpecifier,
	tsnode.KindSizeof:           tsnode.KindSizeofExpression,
	tsnode.KindStatic:           tsnode.KindStorageClassSpecifier,
	tsnode.KindStruct:           tsnode.KindStructSpecifier,
	tsnode.KindThreadUnderLocal: tsnode.KindStorageClassSpecifier,
	tsnode.KindTypedef:          tsnode.KindTypeDefinition,
	tsnode.KindUnderAlignas:     tsnode.KindAlignasQualifier,
	tsnode.KindUnderAlignof:     tsnode.KindAlignofExpression,
	tsnode.KindUnderGeneric:     tsnode.KindGenericExpression,
	tsnode.KindUnion:            tsnode.KindUnionSpecifier,
}

// admits says what each construct an error node can sit in was reading when
// the grammar could not go on, which is the half of "expected X, found Y" the
// error node no longer carries. Only constructs whose contents are one
// repeated thing have a row; a guess among several is worse than none.
var admits = map[tsnode.Kind]string{
	tsnode.KindArgumentList:         "an argument",
	tsnode.KindCompoundStatement:    "a statement",
	tsnode.KindFieldDeclarationList: "a member declaration",
	tsnode.KindInitializerList:      "an initializer",
	tsnode.KindParameterList:        "a parameter",
	tsnode.KindTranslationUnit:      "a declaration",
}

// separators names the token that joins one item of a construct to the next.
// Only a construct whose items take exactly one separator has a row: a message
// that picks one of several and is wrong is worse than none.
var separators = map[tsnode.Kind]string{
	tsnode.KindArgumentList:        ",",
	tsnode.KindDeclaration:         ";",
	tsnode.KindExpressionStatement: ";",
	tsnode.KindInitializerList:     ",",
	tsnode.KindReturnStatement:     ";",
}

// expressionKinds is every form an expression can take. One standing alone
// inside an error node is a complete item the grammar could not join to the one
// beside it.
var expressionKinds = formsOf(tsnode.KindExpression)

// declaratorLayers are the forms a declarator wraps a name in, which the walk
// from a name up to the declaration that gives it its type steps through.
var declaratorLayers = map[tsnode.Kind]bool{
	tsnode.KindArrayDeclarator:    true,
	tsnode.KindFunctionDeclarator: true,
	tsnode.KindInitDeclarator:     true,
	tsnode.KindPointerDeclarator:  true,
}

// reportSyntaxErrors names every place the grammar could not read the
// source. It runs before conversion so a construct standing over a hole is
// reported as the hole, and does not descend into a node it has reported: an
// error node spans one mistake however many well-formed subtrees sit under it.
func (c *converter) reportSyntaxErrors(n *ts.Node) {
	if !n.HasError() {
		return
	}
	switch {
	case n.IsMissing():
		if !c.reachesUnreadable(n) {
			c.reportMissing(n)
		}
		return
	case n.IsError():
		if !c.reachesUnreadable(n) {
			c.reportError(n)
		}
		return
	}
	for _, ch := range c.children(n) {
		c.reportSyntaxErrors(ch.node)
	}
}

// reportMissing names a token the grammar expected and the source does not
// have. The node is empty and sits where the token belonged, so the position
// is where the programmer has to type. A token missing at the end of the
// file is reported there instead, not at whatever trailing source follows.
func (c *converter) reportMissing(n *ts.Node) {
	if c.reportUntypedDeclaration(n) || c.reportMissingInitializer(n) {
		return
	}
	found := c.nextToken(n.StartByte())
	at := c.start(n)
	if found.Kind == lexer.EOF {
		at = found.Pos
	}
	c.errorf(at, "expected %s, found %s", expected(n), found.Describe())
}

// reportUntypedDeclaration reports a declaration written without a type, and
// says whether that is what the missing node stands for: a declaration whose
// type is a name the language does not have is one the grammar read the
// variable's name as the type of, leaving a name missing instead of a type.
func (c *converter) reportUntypedDeclaration(n *ts.Node) bool {
	if tsnode.Kind(n.Kind()) != tsnode.KindIdentifier {
		return false
	}
	typ := declaringType(n)
	if typ == nil || tsnode.Kind(typ.Kind()) != tsnode.KindTypeIdentifier {
		return false
	}
	spelling := c.text(typ)
	if _, isType := scalarTypes[spelling]; isType {
		return false
	}
	c.errorf(c.start(typ), "expected a type, found '%s'", spelling)
	return true
}

// reportMissingInitializer reports a declaration whose initializer the
// source left out, and says whether that is what the missing node stands
// for. The grammar's rule behind '=' wants an identifier, but MicroC wants an
// expression, so the sentence is drawn against the '=' instead.
func (c *converter) reportMissingInitializer(n *ts.Node) bool {
	declarator := n.Parent()
	if declarator == nil || tsnode.Kind(declarator.Kind()) != tsnode.KindInitDeclarator {
		return false
	}
	if value := field(declarator, tsnode.FieldValue); value == nil || value.Id() != n.Id() {
		return false
	}
	eq, marked := c.anonymous(declarator, tsnode.KindEq)
	if !marked {
		return false
	}
	c.errorf(eq, "expected an expression after '%s'", tsnode.KindEq)
	return true
}

// declaringType gives the type of the declaration a name is declared by, or nil
// where the name is not a declarator's.
func declaringType(n *ts.Node) *ts.Node {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if !declaratorLayers[tsnode.Kind(p.Kind())] {
			return field(p, tsnode.FieldType)
		}
	}
	return nil
}

// reportError names one place the grammar could not read the source. An
// error node can span several well-formed subtrees, and a message per
// subtree is noise, so the node's children are asked first, then an unclosed
// or mismatched bracket, and only then the token the region begins with.
func (c *converter) reportError(n *ts.Node) {
	named := c.refuseInside(n) || c.reportDanglingOperator(n) || c.reportMissingSeparator(n)
	if c.reportUnclosed(n) || named {
		return
	}
	if c.reportMismatched(n) {
		return
	}
	c.reportUnexpected(n)
}

// refuseInside names the construct MicroC excludes that the grammar could not
// finish reading, and says whether there was one.
func (c *converter) refuseInside(n *ts.Node) bool {
	for _, ch := range c.children(n) {
		if ch.kind == tsnode.KindLbrackLbrack && c.opensAnAttribute(ch.node) {
			c.errorf(c.start(ch.node), "%s", unknownAttrMsg)
			return true
		}
		if msg, named := c.refusalFor(ch.node); named {
			c.errorf(c.start(ch.node), "%s", msg)
			return true
		}
	}
	return false
}

// opensAnAttribute reports whether a '[[' the grammar left loose inside an
// error node opens an attribute rather than a subscript: the grammar spells
// both pairs as one lexeme, so "a[[0]]" is a subscript with no attribute
// anywhere in it. What stands in front of the pair tells the two apart.
func (c *converter) opensAnAttribute(n *ts.Node) bool {
	return !c.endsAValueBefore(c.tokenIndex(c.sourceOffset(n.StartByte())))
}

// refusalFor gives the sentence naming what a node stands for, and says
// whether there is one. It takes the node rather than the kind because a
// storage class is named by the keyword it is written with, reached here
// both as the whole construct and as a token loose in an error node.
func (c *converter) refusalFor(n *ts.Node) (string, bool) {
	kind := tsnode.Kind(n.Kind())
	if construct, opens := refusedTokens[kind]; opens {
		kind = construct
	}
	if kind == tsnode.KindStorageClassSpecifier {
		return storageClassMsg(readableText(c.text(n))), true
	}
	msg, named := refusals[kind]
	return msg, named
}

// reportDanglingOperator names an infix operator the source left without a
// right operand, and says whether that is what the error node holds. The
// grammar has nowhere to put such an operator, so it stands alone.
func (c *converter) reportDanglingOperator(n *ts.Node) bool {
	loose := c.children(n)
	if len(loose) != 1 {
		return false
	}
	op := loose[0]
	_, infix := binaryOps[op.kind]
	_, assigns := assignOps[op.kind]
	if !infix && !assigns {
		return false
	}
	c.errorf(c.start(op.node), "expected an expression after '%s'", op.kind)
	return true
}

// reportMissingSeparator names the token the source left out between one
// complete item and the next, and says whether that is what the error node
// holds: a single expression wrapped whole, with another item beside it. The
// position is where a missing node would have sat had the grammar made one.
func (c *converter) reportMissingSeparator(n *ts.Node) bool {
	inner := c.children(n)
	if len(inner) != 1 || !expressionKinds[inner[0].kind] {
		return false
	}
	sep, known := enclosingSeparator(n)
	if !known {
		return false
	}
	at, joins := c.joinPoint(n, sep)
	if !joins {
		return false
	}
	c.errorf(c.pos(at), "expected '%s', found %s", sep, c.describeAt(at))
	return true
}

// joinPoint gives the byte the separator sep belonged at, and says whether
// the error node has an item beside it to join. A side already carrying sep,
// or an operator standing in an item's place, is not a join: "a 1 = 2" wraps
// '1' with the '=' behind it, and what is missing is between 'a' and '1'.
func (c *converter) joinPoint(n *ts.Node, sep string) (uint, bool) {
	if next := n.NextSibling(); next != nil && expressionKinds[tsnode.Kind(next.Kind())] && c.nextToken(n.EndByte()).Text != sep {
		return n.EndByte(), true
	}
	prev := n.PrevSibling()
	if prev == nil || !expressionKinds[tsnode.Kind(prev.Kind())] || c.nextToken(prev.EndByte()).Text == sep {
		return 0, false
	}
	return prev.EndByte(), true
}

// enclosingSeparator gives the separator of the innermost construct around a
// node that has one, and says whether any does. The walk climbs because the
// item is wrapped in whatever the construct spells it with: a declarator sits
// between an initializer and the declaration that terminates it.
func enclosingSeparator(n *ts.Node) (string, bool) {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if sep, known := separators[tsnode.Kind(p.Kind())]; known {
			return sep, true
		}
	}
	return "", false
}

// reportUnexpected names the first token of an error node against what the
// construct around it was reading.
func (c *converter) reportUnexpected(n *ts.Node) {
	found := c.describeAt(n.StartByte())
	// A type is the one thing that can stand complete before the grammar loses
	// its way and still say what was owed next, since a type in any position is
	// followed by the name it gives.
	if prev := n.PrevSibling(); prev != nil && typeSpecifierKinds[tsnode.Kind(prev.Kind())] {
		c.errorf(c.start(n), "expected a declarator, found %s", found)
		return
	}
	if parent := n.Parent(); parent != nil {
		if wanted, known := admits[tsnode.Kind(parent.Kind())]; known {
			c.errorf(c.start(n), "expected %s, found %s", wanted, found)
			return
		}
	}
	c.errorf(c.start(n), "%s is not expected here", found)
}

// reportUnclosed names the opener inside an error node the source never
// pairs, and says whether there was one: a parse cannot discard a token, so
// its closer never coming swallows a later construct whole. Only the
// outermost opener is named; nested ones are the same mistake seen further in.
func (c *converter) reportUnclosed(n *ts.Node) bool {
	open, _ := c.balance()
	for _, b := range open {
		if c.spans(n, b.pos.Offset) {
			c.errorf(b.pos, "%s", b.unclosed())
			return true
		}
	}
	return false
}

// reportMismatched names a closer inside an error node that pairs with an
// opener taking a different one, and says whether there was such a closer.
// It is reported at the closer, past the region rather than in it: the
// region ends where the parse lost its way over that closer.
func (c *converter) reportMismatched(n *ts.Node) bool {
	_, mismatch := c.balance()
	if mismatch == nil || mismatch.opener == nil || !c.spans(n, mismatch.opener.pos.Offset) {
		return false
	}
	c.errorf(mismatch.pos, "%s", mismatch)
	return true
}

// spans reports whether a byte offset into the source as written falls inside a
// node, which is what the bracket prepass and the tree are compared through:
// the prepass reads the source and the tree may hold braces the source does
// not.
func (c *converter) spans(n *ts.Node, offset int) bool {
	return offset >= c.sourceOffset(n.StartByte()) && offset < c.sourceOffset(n.EndByte())
}

// balance answers what the bracket prepass found, running it the first time
// it is asked for. It reads the lexer's tokens rather than the tree, since
// the tree has already decided what an unpaired bracket swallowed, which is
// the decision being explained.
func (c *converter) balance() ([]balanceOpener, *balanceMismatch) {
	if !c.scanned {
		c.scanned = true
		c.unclosed, c.mismatch = bracketBalance(c.ordered)
	}
	return c.unclosed, c.mismatch
}

// expected spells the token a missing node names the way the lexer's own
// diagnostics spell one, so that "expected ';'" reads the same whichever front
// end said it. Punctuation and keywords are the spelling itself; a grammar rule
// reads as words behind an article.
func expected(n *ts.Node) string {
	kind := tsnode.Kind(n.Kind())
	if !n.IsNamed() {
		return "'" + string(kind) + "'"
	}
	words := readable(kind)
	if strings.ContainsRune("aeiou", rune(words[0])) {
		return "an " + words
	}
	return "a " + words
}

// nextToken gives the first token at or after a byte offset. It reads the
// lexer's tokens rather than the tree's, since what the two front ends call
// a token is the one thing they must not disagree about. The list ends with
// the end-of-file token, which answers for an offset past the last one.
func (c *converter) nextToken(offset uint) lexer.Token {
	i, _ := slices.BinarySearchFunc(c.ordered, c.sourceOffset(offset), func(t lexer.Token, off int) int {
		return cmp.Compare(t.Pos.Offset, off)
	})
	return c.ordered[min(i, len(c.ordered)-1)]
}

// describeAt names the token at or after a byte offset the way a diagnostic
// should.
func (c *converter) describeAt(offset uint) string { return c.nextToken(offset).Describe() }

// reachesUnreadable reports whether a node covers any source the lexer could
// not read, which it has already reported on as a whole.
func (c *converter) reachesUnreadable(n *ts.Node) bool {
	return c.sourceOffset(n.EndByte()) > c.unreadable
}

// unreadableAt reports whether a byte offset falls in source the lexer could
// not read, which [converter.errorf] cannot test directly since a message
// about a token is sometimes reported at a position in front of it.
func (c *converter) unreadableAt(offset uint) bool {
	return c.sourceOffset(offset) >= c.unreadable
}

// endOfPrevToken gives the byte just past the token in front of an offset,
// in the source as written — where a missing separator belonged, which is
// also where the grammar puts a missing node for the same mistake.
func (c *converter) endOfPrevToken(offset uint) source.Position {
	off := c.sourceOffset(offset)
	i, _ := slices.BinarySearchFunc(c.ordered, off, func(t lexer.Token, at int) int {
		return cmp.Compare(t.Pos.Offset, at)
	})
	if i == 0 {
		return c.posOf(off)
	}
	prev := c.ordered[i-1]
	return c.posOf(prev.Pos.Offset + len(prev.Text))
}
