package tsparse

import (
	"maps"
	"slices"
	"strings"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/source"
	"github.com/greg2010/ic11c/internal/tsnode"
	ts "github.com/tree-sitter/go-tree-sitter"
)

// The spelling of the one attribute MicroC recognizes. The namespace is the
// compiler's own, which is what C23 reserves a scoped attribute for: an
// implementation must ignore an attribute whose namespace it does not know, so
// the declaration means the same thing to a C toolchain with or without it.
const (
	attrNamespace = "ic11c"
	prefabAttr    = "prefab"
)

// scalarTypes maps every spelling MicroC gives a scalar type to the kind that
// names it, read out of the syntax tree's own enumeration so a type the
// language gains is recognized without an edit. It is also the closed set a
// bare identifier in type position is held to, since MicroC has no typedef.
var scalarTypes = func() map[string]ast.ScalarKind {
	kinds := ast.ScalarKinds()
	types := make(map[string]ast.ScalarKind, len(kinds))
	for _, kind := range kinds {
		types[kind.String()] = kind
	}
	return types
}()

// microCTypes lists what a diagnostic should offer in place of a type MicroC
// does not have.
var microCTypes = strings.Join(slices.Sorted(maps.Keys(scalarTypes)), ", ")

// typeAdvice answers the C spellings a MicroC program is most likely to reach
// for with the reason MicroC has something else instead.
var typeAdvice = map[string]string{
	"char":  "the 'char' type specifier is not supported in MicroC; a character literal is a long long",
	"int":   "the 'int' type specifier is not supported in MicroC; C's int is 32 bits and every value here is exact to 53 — write 'long long', which C guarantees at least 64 bits everywhere",
	"float": "the 'float' type specifier is not supported in MicroC; every register and memory slot holds one whole double, so there is no 32-bit type for it to name — write 'double'",
	"long":  "MicroC's integer type is 'long long'; 'long' alone is 32 bits on some C implementations, which is narrower than the values this machine holds",
}

// castTargets are the types a cast may name. A cast to void discards a value an
// expression statement already discards, and a device pin is a compile-time
// name rather than a value a number could become.
var castTargets = map[ast.ScalarKind]string{
	ast.Void: "a cast to void is not supported in MicroC",
	ast.Dev:  "a cast to dev is not supported in MicroC; a device is named, not computed",
}

// declConverters dispatches on the kind of a file scope declaration.
var declConverters = map[tsnode.Kind]func(*converter, *ts.Node) ast.Decl{
	tsnode.KindDeclaration:        (*converter).declaration,
	tsnode.KindFunctionDefinition: (*converter).funcDef,
}

// translationUnit converts a whole file. Every declaration that converted is
// kept in source order, and one that did not appears as a [ast.BadDecl] so the
// positions after it still mean something.
func (c *converter) translationUnit(n *ts.Node) *ast.File {
	f := &ast.File{Name: c.file, Start: c.fileStart()}
	for _, ch := range c.children(n) {
		// A ';' the grammar could not attach to the declaration in front of it
		// stands loose here. It terminates that declaration rather than
		// declaring anything, and the declaration has already been reported on.
		if ch.kind == tsnode.KindSemi {
			continue
		}
		f.Decls = append(f.Decls, c.decl(ch.node))
	}
	return f
}

// fileStart is where the first token of the file was written, which is what the
// tree records as the file's own position. The grammar's root begins at the
// first comment instead, and a comment is not a declaration.
func (c *converter) fileStart() source.Position {
	return c.posOf(c.ordered[0].Pos.Offset)
}

func (c *converter) decl(n *ts.Node) ast.Decl {
	if bad(n) {
		return &ast.BadDecl{From: c.start(n)}
	}
	convert, known := declConverters[tsnode.Kind(n.Kind())]
	if !known {
		c.refuseAtFileScope(n)
		return &ast.BadDecl{From: c.start(n)}
	}
	return convert(c, n)
}

// formsOf collects a supertype's alternatives into the set a dispatch tests one
// against.
func formsOf(super tsnode.Kind) map[tsnode.Kind]bool {
	forms := tsnode.Subtypes[super]
	set := make(map[tsnode.Kind]bool, len(forms))
	for _, kind := range forms {
		set[kind] = true
	}
	return set
}

// statementKinds is every form the grammar admits where a statement is
// admitted. C reads a translation unit as a list of declarations and this
// grammar admits a statement in one too, so a statement reaching file scope is
// one written outside every function.
var statementKinds = formsOf(tsnode.KindStatement)

// typeSpecifierKinds is every form a type can take. One standing alone where a
// declaration belongs is a declaration whose declarator the grammar could not
// read.
var typeSpecifierKinds = formsOf(tsnode.KindTypeSpecifier)

// Both refusals below ask [refusals] first, and neither may stop doing so: a
// routing that asks what a node is before asking whether it is excluded
// answers one source with the construct's name in one position and the
// punctuation behind it in the other.

// refuseAtFileScope reports a node standing where a declaration belongs. A
// construct MicroC excludes is named as itself; anything else is a mistake
// about where the construct sits: a statement belongs inside a function, and
// a bare type wants a name after it.
func (c *converter) refuseAtFileScope(n *ts.Node) {
	kind := tsnode.Kind(n.Kind())
	switch {
	case refusedByName(kind):
		c.refuse(n)
	case statementKinds[kind]:
		c.errorf(c.start(n), "expected a declaration; a statement is only valid inside a function body")
	case typeSpecifierKinds[kind]:
		c.refuseBareType(n)
	default:
		c.refuse(n)
	}
}

// refuseAsStatement reports a node standing where a statement belongs. A
// function written inside another and a type standing on its own are the two
// the grammar admits there and the language does not, and neither is a
// construct the generic refusal has anything true to say about.
func (c *converter) refuseAsStatement(n *ts.Node) {
	kind := tsnode.Kind(n.Kind())
	switch {
	case refusedByName(kind):
		c.refuse(n)
	case kind == tsnode.KindFunctionDefinition:
		c.errorf(c.start(n), "%s", nestedFuncMsg)
	case typeSpecifierKinds[kind]:
		c.refuseBareType(n)
	default:
		c.refuse(n)
	}
}

// refusedByName reports whether a construct has a sentence naming it, which is
// what [converter.refuse] would answer it with.
func refusedByName(kind tsnode.Kind) bool {
	if kind == tsnode.KindStorageClassSpecifier {
		return true
	}
	_, named := refusals[kind]
	return named
}

// refuseBareType reports a type written with nothing behind it, against what
// follows the type rather than the type itself — unless the grammar took a
// reserved word into the type as its declarator's name, in which case the
// name is what is named instead.
func (c *converter) refuseBareType(n *ts.Node) {
	if name := field(n, tsnode.FieldType); name != nil && tsnode.Kind(name.Kind()) == tsnode.KindTypeIdentifier {
		if _, spelled := c.identifier(name); !spelled {
			return
		}
	}
	tok := c.nextToken(n.EndByte())
	c.errorf(tok.Pos, "expected a declarator, found %s", tok.Describe())
}

// specifiers is everything a declaration writes around its declarator: the
// attribute it leads with, the type, and the qualifiers.
type specifiers struct {
	prefab    *ast.PrefabAttr
	typ       ast.Type
	isConst   bool
	constexpr bool
}

// qualifierSpecifiers records what each qualifier keyword says about the
// declaration it qualifies, keyed on the qualifier node's anonymous child:
// const and constexpr differ only there, so a walk over named children alone
// cannot tell them apart.
var qualifierSpecifiers = map[tsnode.Kind]func(*specifiers){
	tsnode.KindConst:     func(s *specifiers) { s.isConst = true },
	tsnode.KindConstexpr: func(s *specifiers) { s.constexpr = true },
}

// declPunctuation is the punctuation a declaration carries between its parts,
// which says nothing about what it declares.
var declPunctuation = map[tsnode.Kind]bool{
	tsnode.KindComma: true,
	tsnode.KindSemi:  true,
}

// specifiers reads everything a declaration node says outside its
// declarators: a field the caller reads, an attribute, a qualifier,
// punctuation, or something refused by name. Nothing is skipped for being
// unnamed, which is where const and constexpr differ.
func (c *converter) specifiers(n *ts.Node) (specifiers, bool) {
	var s specifiers
	ok := true
	// MicroC writes the attribute in front of everything else and C admits one
	// between any two specifiers, so where it sits is part of what a
	// declaration means rather than a matter of taste.
	leading := true
	// The old-style parameter list is one construct however many declarations
	// it is spelled with, and is owed one sentence.
	oldStyle := false
	// Whether the type has been written, rather than whether it converted: a
	// qualifier behind a type the language does not have is still behind a
	// type.
	typed := false
	for _, ch := range c.children(n) {
		if ch.kind == tsnode.KindAttributeDeclaration {
			if !leading {
				c.errorf(c.start(ch.node), "an attribute leads a declaration in MicroC; write it in front of the specifiers and the type")
				ok = false
				continue
			}
			c.attributes(ch.node, &s)
			continue
		}
		leading = false
		switch {
		case ch.field == tsnode.FieldType:
			typed = true
			typ, read := c.typeSpec(ch.node)
			if !read {
				ok = false
				continue
			}
			s.typ = typ
		case ch.field == tsnode.FieldDeclarator || ch.field == tsnode.FieldBody:
			// Read by the caller, which is what decides whether the declaration
			// declares a variable or a function.
		case ch.kind == tsnode.KindTypeQualifier:
			// Where the qualifier stands is part of what it says: MicroC writes
			// its two qualifiers in front of the type, so one behind it is the
			// same mistake "long long const" is, reached instead through
			// [converter.sizedType].
			if typed {
				c.refuseQualifierAfterType(ch.node)
				continue
			}
			c.qualifier(ch.node, &s)
		case ch.kind == tsnode.KindDeclaration:
			// A declaration between a parameter list and a body is what gives
			// the parameters their types in the form C inherited from K&R.
			if !oldStyle {
				oldStyle = true
				c.errorf(c.start(ch.node), "%s", oldStyleParamsMsg)
			}
			ok = false
		case declPunctuation[ch.kind]:
		default:
			c.refuse(ch.node)
			ok = false
		}
	}
	return s, ok && s.typ != nil
}

func (c *converter) qualifier(n *ts.Node, s *specifiers) {
	for _, ch := range c.children(n) {
		apply, known := qualifierSpecifiers[ch.kind]
		if !known {
			c.refuseQualifier(ch.node)
			continue
		}
		apply(s)
	}
}

// refuseQualifierAfterType reports a qualifier written past the type it
// qualifies, which C admits behind the type name and on the declarator both.
// MicroC writes its own two qualifiers in front of the type and has no others
// to write anywhere.
func (c *converter) refuseQualifierAfterType(n *ts.Node) {
	for _, ch := range c.children(n) {
		if _, admitted := qualifierSpecifiers[ch.kind]; admitted {
			c.errorf(c.start(ch.node), "%s must precede the type in MicroC", ch.kind)
			continue
		}
		c.refuseQualifier(ch.node)
	}
}

// refuseQualifierInCast reports a qualifier written inside a cast, on either
// side of the type name. MicroC's two qualifiers say how a declaration is
// stored, so neither has anything to say about a value a cast converts.
func (c *converter) refuseQualifierInCast(n *ts.Node) {
	for _, ch := range c.children(n) {
		if _, admitted := qualifierSpecifiers[ch.kind]; admitted {
			c.errorf(c.start(ch.node), "'%s' is not supported in a cast; it says how a declaration is stored, and a cast declares nothing", ch.kind)
			continue
		}
		c.refuseQualifier(ch.node)
	}
}

// refuseQualifier names one qualifier MicroC does not have. A qualifier
// spelled as a keyword is anonymous, so the generic refusal would report it
// as loose punctuation instead; one spelled as its own construct (alignas) is
// named by that construct.
func (c *converter) refuseQualifier(n *ts.Node) {
	if n.IsNamed() {
		c.refuse(n)
		return
	}
	c.errorf(c.start(n), "the '%s' qualifier is not supported in MicroC", n.Kind())
}

// attributes reads one attribute specifier. A declaration names one prefab: C
// admits a list and a sequence of specifiers both, and MicroC has one attribute
// to put in either, so a second is a contradiction rather than a shorthand.
func (c *converter) attributes(n *ts.Node, s *specifiers) {
	first := true
	for _, ch := range c.children(n) {
		// Default is the rule: tsnode.Kind is the grammar's whole alphabet, and
		// what a construct is written with is a handful of it.
		//exhaustive:ignore
		switch ch.kind {
		case tsnode.KindLbrackLbrack, tsnode.KindRbrackRbrack, tsnode.KindComma:
			continue
		case tsnode.KindAttribute:
		default:
			c.refuse(ch.node)
			continue
		}
		// The first attribute of a specifier is reported at the specifier's own
		// opening bracket, which is where the declaration a reader sees begins.
		at := c.start(ch.node)
		if first {
			at = c.start(n)
			first = false
		}
		attr, ok := c.prefab(ch.node, at)
		if !ok {
			continue
		}
		if s.prefab == nil {
			s.prefab = attr
			continue
		}
		c.errorf(attr.At, "a declaration names one prefab, already named at %s", s.prefab.At)
	}
}

// prefab reads the one attribute MicroC recognizes. Recognizing only that
// spelling is deliberate: C requires an implementation to ignore an attribute
// it does not know, so an attribute nothing here reads would be a promise the
// compiler silently drops.
func (c *converter) prefab(n *ts.Node, at source.Position) (*ast.PrefabAttr, bool) {
	refuse := func() (*ast.PrefabAttr, bool) {
		c.errorf(at, "%s", unknownAttrMsg)
		return nil, false
	}
	prefix, name := field(n, tsnode.FieldPrefix), field(n, tsnode.FieldName)
	if prefix == nil || name == nil || c.text(prefix) != attrNamespace || c.text(name) != prefabAttr {
		return refuse()
	}
	var literal *ts.Node
	for _, ch := range c.children(n) {
		if ch.kind != tsnode.KindArgumentList {
			continue
		}
		for _, arg := range c.children(ch.node) {
			// Default is the rule: tsnode.Kind is the grammar's whole alphabet, and
			// what a construct is written with is a handful of it.
			//exhaustive:ignore
			switch arg.kind {
			case tsnode.KindLparen, tsnode.KindRparen:
			case tsnode.KindStringLiteral:
				if literal != nil {
					return refuse()
				}
				literal = arg.node
			default:
				return refuse()
			}
		}
	}
	if literal == nil {
		return refuse()
	}
	tok, scanned := c.token(literal.StartByte())
	if !scanned {
		return refuse()
	}
	return &ast.PrefabAttr{At: at, Name: tok.Str, NamePos: c.start(literal)}, true
}

// typeSpecs dispatches on the kind of a type specifier.
var typeSpecs = map[tsnode.Kind]func(*converter, *ts.Node) (ast.Type, bool){
	tsnode.KindPrimitiveType:      (*converter).primitiveType,
	tsnode.KindSizedTypeSpecifier: (*converter).sizedType,
	tsnode.KindTypeIdentifier:     (*converter).namedType,
}

func (c *converter) typeSpec(n *ts.Node) (ast.Type, bool) {
	convert, known := typeSpecs[tsnode.Kind(n.Kind())]
	if !known {
		c.refuse(n)
		return nil, false
	}
	return convert(c, n)
}

func (c *converter) primitiveType(n *ts.Node) (ast.Type, bool) {
	return c.scalarType(n, c.text(n))
}

// namedType reads a type written as a bare identifier, which in MicroC is
// only ever dev. Holding it to the closed set is what keeps "a * b;" from
// being read as a pointer declaration: C resolves that ambiguity with a
// symbol table, and MicroC has none.
func (c *converter) namedType(n *ts.Node) (ast.Type, bool) {
	return c.scalarType(n, c.text(n))
}

func (c *converter) scalarType(n *ts.Node, spelling string) (ast.Type, bool) {
	kind, named := scalarTypes[spelling]
	if !named {
		c.notAType(n, spelling)
		return nil, false
	}
	return &ast.ScalarType{TypePos: c.start(n), Kind: kind}, true
}

// sizedType reads a type written with size specifiers, which is how the
// grammar spells long long. Only the size words reach the spelling a
// diagnostic quotes; a qualifier or a name behind them is reported as what it
// is, rather than folded into a spelling nothing in the language has.
func (c *converter) sizedType(n *ts.Node) (ast.Type, bool) {
	var words []*ts.Node
	var name *ts.Node
	nameLeads := false
	for _, ch := range c.children(n) {
		// Default is the rule: a type is spelled with size words, and the two
		// kinds beside them are not part of one.
		//exhaustive:ignore
		switch ch.kind {
		case tsnode.KindTypeQualifier:
			c.refuseQualifierAfterType(ch.node)
		case tsnode.KindTypeIdentifier:
			if name == nil {
				name, nameLeads = ch.node, len(words) == 0
			}
		default:
			words = append(words, ch.node)
		}
	}
	spelling := c.spelled(words)
	if name != nil {
		c.nameInType(n, name, nameLeads, spelling)
		return nil, false
	}
	if kind, named := scalarTypes[spelling]; named {
		return &ast.ScalarType{TypePos: c.start(n), Kind: kind}, true
	}
	// C writes the integer type with a trailing int as well, and MicroC does
	// not. One diagnostic, drawn against the word to delete rather than against
	// the type that is spelled correctly without it, and the declaration around
	// it still converts.
	if rest, trailing := strings.CutSuffix(spelling, " int"); trailing {
		if kind, named := scalarTypes[rest]; named {
			c.errorf(c.start(words[len(words)-1]), "MicroC writes the integer type as '%s', without the trailing 'int'", rest)
			return &ast.ScalarType{TypePos: c.start(n), Kind: kind}, true
		}
	}
	if extra, over := c.spelledPast(words); over {
		c.errorf(c.start(extra), "expected an identifier, found %s", c.describeAt(extra.StartByte()))
		return nil, false
	}
	c.notAType(n, spelling)
	return nil, false
}

// spelled joins the size words of a type the way a programmer writes them.
func (c *converter) spelled(words []*ts.Node) string {
	parts := make([]string, len(words))
	for i, w := range words {
		parts[i] = c.text(w)
	}
	return strings.Join(parts, " ")
}

// spelledPast gives the first size word past the type the words in front of
// it already spell, and says whether the source writes one. "long long long"
// is the only shape that reaches it, since long long is MicroC's one
// multi-word type; the third word is the declared name, not part of a type.
func (c *converter) spelledPast(words []*ts.Node) (*ts.Node, bool) {
	for i := len(words) - 1; i > 0; i-- {
		if _, isType := scalarTypes[c.spelled(words[:i])]; isType {
			return words[i], true
		}
	}
	return nil, false
}

// nameInType reports a declaration whose type the grammar built the name
// into, which MicroC has no typedef to make a type of anywhere. In front of
// the size words the name is the missing type; behind them it is the
// declared name, and what follows is a second declarator never separated.
func (c *converter) nameInType(n, name *ts.Node, leads bool, spelling string) {
	if leads {
		c.errorf(c.start(name), "expected a type, found '%s'", c.text(name))
		return
	}
	if _, isType := scalarTypes[spelling]; !isType {
		c.notAType(n, spelling)
		return
	}
	c.errorf(c.pos(name.EndByte()), "expected ';', found %s", c.describeAt(name.EndByte()))
}

// readableText spells a node for a diagnostic that quotes what was written,
// collapsing the line breaks a multi-line construct would otherwise drag into
// the message.
func readableText(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func (c *converter) notAType(n *ts.Node, spelling string) {
	spelling = readableText(spelling)
	if advice, known := typeAdvice[spelling]; known {
		c.errorf(c.start(n), "%s", advice)
		return
	}
	c.errorf(c.start(n), "'%s' is not a type in MicroC, whose types are %s; MicroC has no typedef, so nothing else becomes one", spelling, microCTypes)
}

// castType reads the type a cast names, which the grammar wraps in a
// descriptor of its own so an abstract declarator can hang off it. Every
// child is classified rather than only the two slots the type comes from: a
// qualifier fills no slot, and a field-only walk would silently accept it.
func (c *converter) castType(n *ts.Node) (ast.Type, bool) {
	spec := field(n, tsnode.FieldType)
	if spec == nil {
		return nil, false
	}
	ok := true
	for _, ch := range c.children(n) {
		switch {
		case ch.field == tsnode.FieldType:
		case ch.field == tsnode.FieldDeclarator:
			c.errorf(c.start(ch.node), "a cast to a pointer type is not supported in MicroC")
			ok = false
		case ch.kind == tsnode.KindTypeQualifier:
			c.refuseQualifierInCast(ch.node)
			ok = false
		default:
			c.refuse(ch.node)
			ok = false
		}
	}
	// A qualifier behind the type name belongs to the type rather than to the
	// descriptor, and reading it here is what keeps the two spellings of the
	// same mistake from drawing sentences that contradict each other: "const
	// must precede the type" is advice a cast cannot take.
	if tsnode.Kind(spec.Kind()) == tsnode.KindSizedTypeSpecifier {
		for _, ch := range c.children(spec) {
			if ch.kind == tsnode.KindTypeQualifier {
				c.refuseQualifierInCast(ch.node)
				ok = false
			}
		}
	}
	typ, read := c.typeSpec(spec)
	if !ok || !read {
		return nil, false
	}
	if scalar, isScalar := typ.(*ast.ScalarType); isScalar {
		if msg, refused := castTargets[scalar.Kind]; refused {
			c.errorf(c.start(spec), "%s", msg)
			return nil, false
		}
	}
	return typ, true
}

// declaration converts a declaration, which declares either a variable or a
// function without a body.
func (c *converter) declaration(n *ts.Node) ast.Decl {
	spec, ok := c.specifiers(n)
	if !ok {
		return &ast.BadDecl{From: c.start(n)}
	}
	var declarators []*ts.Node
	var comma, joined *ts.Node
	for _, ch := range c.children(n) {
		if ch.kind == tsnode.KindComma {
			comma = ch.node
			continue
		}
		if ch.field != tsnode.FieldDeclarator {
			continue
		}
		if len(declarators) == 1 {
			joined = comma
		}
		declarators = append(declarators, ch.node)
	}
	if len(declarators) == 0 {
		c.errorf(c.start(n), "expected a declarator")
		return &ast.BadDecl{From: c.start(n)}
	}
	// One declaration is one mistake however many names it lists, so the
	// sentence goes on the comma joining the second name to the first: that
	// is the token the reader deletes.
	if len(declarators) > 1 {
		at := c.start(declarators[1])
		if joined != nil {
			at = c.start(joined)
		}
		c.errorf(at, "MicroC declares one variable per declaration")
	}

	declarator, init := declarators[0], (*ts.Node)(nil)
	if tsnode.Kind(declarator.Kind()) == tsnode.KindInitDeclarator {
		init = field(declarator, tsnode.FieldValue)
		declarator = field(declarator, tsnode.FieldDeclarator)
	}
	if declarator == nil {
		return &ast.BadDecl{From: c.start(n)}
	}

	d, read := c.declarator(declarator, spec.typ, true)
	if !read {
		return &ast.BadDecl{From: c.start(n)}
	}
	if d.isFunc {
		if init != nil {
			c.errorf(c.start(init), "a function declaration has no initializer")
		}
		return c.funcDecl(n, spec, d, nil)
	}
	decl := &ast.VarDecl{
		DeclPos:   c.start(n),
		Const:     spec.isConst,
		Constexpr: spec.constexpr,
		Prefab:    spec.prefab,
		Type:      d.typ,
		Name:      d.name,
		NamePos:   d.namePos,
	}
	if init != nil {
		decl.Init = c.initializer(init)
	}
	return decl
}

// funcDef converts a function definition, which is a declaration carrying a
// body.
func (c *converter) funcDef(n *ts.Node) ast.Decl {
	spec, ok := c.specifiers(n)
	if !ok {
		return &ast.BadDecl{From: c.start(n)}
	}
	declarator, body := field(n, tsnode.FieldDeclarator), field(n, tsnode.FieldBody)
	if declarator == nil || body == nil {
		return &ast.BadDecl{From: c.start(n)}
	}
	d, read := c.declarator(declarator, spec.typ, true)
	if !read {
		return &ast.BadDecl{From: c.start(n)}
	}
	if !d.isFunc {
		c.errorf(c.start(n), "expected a parameter list")
		return &ast.BadDecl{From: c.start(n)}
	}
	return c.funcDecl(n, spec, d, c.block(body))
}

func (c *converter) funcDecl(n *ts.Node, spec specifiers, d declared, body *ast.BlockStmt) ast.Decl {
	from := c.start(n)
	if spec.isConst {
		c.errorf(from, "const is not valid on a function")
	}
	if spec.constexpr {
		c.errorf(from, "constexpr is not valid on a function; MicroC has no constexpr function")
	}
	if spec.prefab != nil {
		c.errorf(spec.prefab.At, "%s states which device a pin is wired to and belongs on a dev declaration; a function names no pin", attrSpelling)
	}
	return &ast.FuncDecl{
		DeclPos: from,
		Result:  d.typ,
		Name:    d.name,
		NamePos: d.namePos,
		Params:  d.params,
		Body:    body,
	}
}

// declared is a declarator read out: the type it builds around the
// declaration's own, the name it declares, and the parameters where it declares
// a function.
type declared struct {
	typ     ast.Type
	name    string
	namePos source.Position
	params  []*ast.Param
	isFunc  bool
}

// declaratorForm is what one layer of a declarator does to the type inside it.
type declaratorForm uint8

const (
	formName declaratorForm = iota
	formPointer
	formArray
	formFunction
)

// declaratorForms names every declarator kind the converter reads. A kind the
// grammar admits and this does not is refused by name, which is what keeps a
// parenthesized declarator -- the spelling of a function pointer -- from being
// read as the thing it wraps.
var declaratorForms = map[tsnode.Kind]declaratorForm{
	tsnode.KindAbstractArrayDeclarator:    formArray,
	tsnode.KindAbstractFunctionDeclarator: formFunction,
	tsnode.KindAbstractPointerDeclarator:  formPointer,
	tsnode.KindArrayDeclarator:            formArray,
	tsnode.KindFunctionDeclarator:         formFunction,
	tsnode.KindIdentifier:                 formName,
	tsnode.KindPointerDeclarator:          formPointer,
}

// suffixForms are the declarator layers written behind the name rather than in
// front of it, which are the ones MicroC admits exactly one of.
var suffixForms = map[declaratorForm]bool{formArray: true, formFunction: true}

// nestedSuffix gives the innermost two layers of a declarator that write
// something outside a suffix, outer first, and says whether the declarator
// writes such a pair. The innermost pair is what the reader is sent to: for
// "a[2][3][4]", that is the second bracket, the first past what MicroC admits.
func nestedSuffix(n *ts.Node) (outer, inner *ts.Node, nested bool) {
	var suffix *ts.Node
	for n != nil {
		form, known := declaratorForms[tsnode.Kind(n.Kind())]
		if !known || bad(n) {
			return outer, inner, nested
		}
		if form == formName {
			return outer, inner, nested
		}
		if suffix != nil {
			outer, inner, nested = suffix, n, true
		}
		if suffixForms[form] {
			suffix = n
		}
		n = field(n, tsnode.FieldDeclarator)
	}
	return outer, inner, nested
}

// suffixMark gives the position of the token one declarator layer writes its
// suffix with, and says whether the layer writes one.
func (c *converter) suffixMark(n *ts.Node) (source.Position, bool) {
	if params := field(n, tsnode.FieldParameters); params != nil {
		return c.start(params), true
	}
	return c.anonymous(n, tsnode.KindLbrack)
}

// refuseNestedSuffix reports a declarator writing something outside its
// array or its parameter list, at the outer layer's own bracket or
// parenthesis rather than at the declarator it encloses, which is written
// correctly and needs no change.
func (c *converter) refuseNestedSuffix(outer, inner *ts.Node) {
	at, marked := c.suffixMark(outer)
	if !marked {
		at = c.start(outer)
	}
	if declaratorForms[tsnode.Kind(outer.Kind())] == formArray && declaratorForms[tsnode.Kind(inner.Kind())] == formArray {
		c.errorf(at, "%s", multiDimArrayMsg)
		return
	}
	c.errorf(at, "%s", oneSuffixMsg)
}

// declarator reads a declarator from the outside in, building the type each
// layer wraps around base. Reading it in that direction gets the nesting
// right: '*a[4]' is an array of pointers rather than a pointer to an array,
// since the subscript binds tighter than the star.
func (c *converter) declarator(n *ts.Node, base ast.Type, sized bool) (declared, bool) {
	if outer, inner, nested := nestedSuffix(n); nested {
		c.refuseNestedSuffix(outer, inner)
		return declared{}, false
	}
	d := declared{typ: base}
	for n != nil {
		if bad(n) {
			return declared{}, false
		}
		form, known := declaratorForms[tsnode.Kind(n.Kind())]
		if !known {
			c.refuse(n)
			return declared{}, false
		}
		if !c.declaratorParts(n) {
			return declared{}, false
		}
		switch form {
		case formName:
			name, ok := c.identifier(n)
			if !ok {
				return declared{}, false
			}
			d.name, d.namePos = name, c.start(n)
			return d, true
		case formPointer:
			star, marked := c.anonymous(n, tsnode.KindStar)
			if !marked {
				return declared{}, false
			}
			d.typ = &ast.PointerType{Star: star, Elem: d.typ}
		case formArray:
			typ, ok := c.arrayType(n, d.typ, sized)
			if !ok {
				return declared{}, false
			}
			d.typ = typ
		case formFunction:
			params := field(n, tsnode.FieldParameters)
			if params == nil {
				return declared{}, false
			}
			d.isFunc = true
			d.params = c.params(params)
		}
		n = field(n, tsnode.FieldDeclarator)
	}
	// An abstract declarator names nothing, which is what an unnamed parameter
	// writes.
	return d, true
}

// declaratorTokens are the tokens a MicroC declarator is written with. The
// parentheses of a parameter list are not among them: they belong to the list
// rather than to the declarator that names it.
var declaratorTokens = map[tsnode.Kind]bool{
	tsnode.KindLbrack: true,
	tsnode.KindRbrack: true,
	tsnode.KindStar:   true,
}

// declaratorFields are the slots the declarator forms read.
var declaratorFields = map[tsnode.Field]bool{
	tsnode.FieldDeclarator: true,
	tsnode.FieldParameters: true,
	tsnode.FieldSize:       true,
}

// declaratorParts refuses whatever hangs off one layer of a declarator that
// MicroC does not license, and says whether the layer held nothing else.
// Refusing by default is what keeps a qualifier, storage class or attribute
// — none of which fills a slot — from a silent drop by a field-only walk.
func (c *converter) declaratorParts(n *ts.Node) bool {
	licensed := true
	for _, ch := range c.children(n) {
		if declaratorFields[ch.field] || declaratorTokens[ch.kind] {
			continue
		}
		c.refuseDeclaratorPart(ch.node)
		licensed = false
	}
	return licensed
}

// refuseDeclaratorPart names one part of a declarator MicroC does not license.
func (c *converter) refuseDeclaratorPart(n *ts.Node) {
	kind := tsnode.Kind(n.Kind())
	if kind == tsnode.KindTypeQualifier {
		c.refuseQualifierAfterType(n)
		return
	}
	if msg, named := c.refusalFor(n); named {
		c.errorf(c.start(n), "%s", msg)
		return
	}
	c.errorf(c.start(n), "%s is not part of a declarator in MicroC", c.describeAt(n.StartByte()))
}

func (c *converter) arrayType(n *ts.Node, elem ast.Type, sized bool) (ast.Type, bool) {
	lbrack, marked := c.anonymous(n, tsnode.KindLbrack)
	if !marked {
		return nil, false
	}
	var size ast.Expr
	switch bound := field(n, tsnode.FieldSize); {
	case bound == nil:
		if sized {
			c.errorf(lbrack, "an array bound is required outside a parameter list")
		}
	case tsnode.Kind(bound.Kind()) == tsnode.KindStar:
		c.errorf(c.start(bound), "a variable-length array is not supported in MicroC")
		return nil, false
	default:
		size = c.expr(bound)
	}
	return &ast.ArrayType{Lbrack: lbrack, Elem: elem, Size: size}, true
}

// params converts a parameter list. "(void)" and "()" both yield no parameters.
func (c *converter) params(n *ts.Node) []*ast.Param {
	var declarations []*ts.Node
	oldStyle := false
	for _, ch := range c.children(n) {
		// Default is the rule: tsnode.Kind is the grammar's whole alphabet, and
		// what a construct is written with is a handful of it.
		//exhaustive:ignore
		switch ch.kind {
		case tsnode.KindLparen, tsnode.KindRparen, tsnode.KindComma:
		case tsnode.KindParameterDeclaration:
			declarations = append(declarations, ch.node)
		case tsnode.KindVariadicParameter:
			c.errorf(c.start(ch.node), "variadic parameters are not supported in MicroC")
		case tsnode.KindIdentifier:
			// A bare name where a parameter belongs is the old-style form,
			// whose types are written behind the list rather than in it.
			if !oldStyle {
				oldStyle = true
				c.errorf(c.start(ch.node), "%s", oldStyleParamsMsg)
			}
		case tsnode.KindCompoundStatement:
			c.errorf(c.start(ch.node), "%s", statementExprMsg)
		default:
			c.refuse(ch.node)
		}
	}
	if len(declarations) == 1 && c.isVoidParam(declarations[0]) {
		return nil
	}
	var params []*ast.Param
	for _, declaration := range declarations {
		if prm, ok := c.param(declaration); ok {
			params = append(params, prm)
		}
	}
	return params
}

// isVoidParam reports whether a parameter is the lone "void" that says a
// function takes none. The word has to be the whole parameter: "(const
// void)" is a parameter of a type MicroC has no value of, worth its own
// sentence rather than being read as taking none.
func (c *converter) isVoidParam(n *ts.Node) bool {
	children := c.children(n)
	if len(children) != 1 || children[0].field != tsnode.FieldType {
		return false
	}
	typ := children[0].node
	return tsnode.Kind(typ.Kind()) == tsnode.KindPrimitiveType && c.text(typ) == ast.Void.String()
}

func (c *converter) param(n *ts.Node) (*ast.Param, bool) {
	spec, ok := c.specifiers(n)
	if !ok {
		return nil, false
	}
	from := c.start(n)
	if spec.constexpr {
		// C admits no storage class but 'register' on a parameter, and a
		// parameter names whatever the call site passed, which is not a constant.
		c.errorf(from, "constexpr is not valid on a parameter")
	}
	if spec.prefab != nil {
		// A dev parameter stands for whichever pin each call site wrote, so a
		// prefab named here would be a promise about every one of them at once.
		c.errorf(spec.prefab.At, "%s states which device a pin is wired to, and a dev parameter names whichever pin each call site passes; write it on the dev declaration the call site names", attrSpelling)
	}

	d := declared{typ: spec.typ}
	if declarator := field(n, tsnode.FieldDeclarator); declarator != nil {
		read := false
		if d, read = c.declarator(declarator, spec.typ, false); !read {
			return nil, false
		}
	}
	if d.isFunc {
		c.errorf(from, "function pointers are not supported in MicroC")
		return nil, false
	}
	return &ast.Param{
		ParamPos: from,
		Const:    spec.isConst,
		Type:     d.typ,
		Name:     d.name,
		NamePos:  d.namePos,
	}, true
}
