package tsparse

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/lexer"
	"github.com/greg2010/ic11c/internal/tsnode"
)

// These tests hold the converter to the grammar rather than to a reading of
// it: a kind renamed out from under a table entry is a compile error
// already, so the checks here are for the other half, a kind the grammar
// gains that no table has an entry for.

// TestTheCheckedInGrammarIsTheModulesOwn fails where the checked-in copies
// of the grammar's description of itself are not the ones the module ships
// now. Every table below is rendered from those files, so a stale copy would
// leave them describing a grammar the parser no longer is.
func TestTheCheckedInGrammarIsTheModulesOwn(t *testing.T) {
	const module = "github.com/tree-sitter/tree-sitter-c"
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", module).Output()
	if err != nil {
		t.Fatalf("locating %s: %v", module, err)
	}
	src := filepath.Join(strings.TrimSpace(string(out)), "src")

	// The copies sit beside the Go rendered from them rather than beside this
	// package, so that no generated file shares a directory with hand-written
	// source.
	const checkedIn = "../tsnode"

	for _, name := range []string{"node-types.json", "grammar.json"} {
		t.Run(name, func(t *testing.T) {
			shipped := filepath.Join(src, name)
			want, err := os.ReadFile(shipped)
			if err != nil {
				t.Fatalf("reading %s: %v", shipped, err)
			}
			ours := filepath.Join(checkedIn, name)
			got, err := os.ReadFile(ours)
			if err != nil {
				t.Fatalf("reading %s: %v", ours, err)
			}
			if string(got) != string(want) {
				t.Errorf("the checked-in %s differs from %s; run: go run ./tools/nodegen sync && go run ./tools/nodegen generate", name, shipped)
			}
		})
	}
}

// TestOperatorTablesCoverTheGrammar holds each operator table to the
// alternatives the grammar declares for the field it reads. An operator C gains
// would otherwise be dropped into a Bad node at run time rather than noticed
// here.
func TestOperatorTablesCoverTheGrammar(t *testing.T) {
	tests := []struct {
		name  string
		kind  tsnode.Kind
		field tsnode.Field
		table []tsnode.Kind
	}{
		{"binary", tsnode.KindBinaryExpression, tsnode.FieldOperator, keys(binaryOps)},
		{"assignment", tsnode.KindAssignmentExpression, tsnode.FieldOperator, keys(assignOps)},
		{"unary", tsnode.KindUnaryExpression, tsnode.FieldOperator, keys(unaryOps)},
		{"pointer", tsnode.KindPointerExpression, tsnode.FieldOperator, keys(pointerOps)},
		{"increment", tsnode.KindUpdateExpression, tsnode.FieldOperator, keys(incDecOps)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := tsnode.FieldTypes[tt.kind][tt.field]
			if len(want) == 0 {
				t.Fatalf("the grammar declares no alternatives for the %s of a %s", tt.field, tt.kind)
			}
			checkSameKinds(t, tt.table, want)
		})
	}
}

// TestSupertypesAreAccountedFor holds the converter to every form the grammar
// admits where it reads an expression, a statement, a type or a declarator.
// Each form is either converted or refused by name; a form that is neither
// would reach the generic refusal, which says nothing useful.
func TestSupertypesAreAccountedFor(t *testing.T) {
	tests := []struct {
		name    string
		super   tsnode.Kind
		handled []tsnode.Kind
	}{
		{
			name:    "expression",
			super:   tsnode.KindExpression,
			handled: keys(exprConverters),
		},
		{
			name:    "statement",
			super:   tsnode.KindStatement,
			handled: keys(stmtConverters),
		},
		{
			name:    "type specifier",
			super:   tsnode.KindTypeSpecifier,
			handled: keys(typeSpecs),
		},
		{
			name:    "declarator",
			super:   tsnode.KindDeclarator,
			handled: keys(declaratorForms),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forms := tsnode.Subtypes[tt.super]
			if len(forms) == 0 {
				t.Fatalf("the grammar declares no forms for %s", tt.super)
			}
			for _, form := range forms {
				if slices.Contains(tt.handled, form) || accountedFor(form) {
					continue
				}
				t.Errorf("%s is a %s the converter neither reads nor refuses by name", form, tt.super)
			}
		})
	}
}

// childWalk models one walk over a node's children, making visible what the
// walk claims to account for so the grammar can be asked whether that is
// everything.
type childWalk struct {
	// what names the walk a failure has to send a reader to.
	what string
	// kind is the node whose children it walks. Two walks over one kind get a
	// row each: what a node holds depends on what read it.
	kind tsnode.Kind
	// handles are the child kinds the walk answers, whether by converting one
	// or by naming it in a refusal of its own.
	handles []tsnode.Kind
	// notRead are the child kinds the walk deliberately leaves alone: decided
	// elsewhere, or reachable only inside a construct already refused.
	notRead []tsnode.Kind
}

// converterWalks models every walk the converter makes over a node's children.
// It is a function because the dispatch tables it reads are assigned in init,
// which runs after the package's variables are initialized.
func converterWalks() []childWalk {
	return []childWalk{
		{
			what:    "translationUnit",
			kind:    tsnode.KindTranslationUnit,
			handles: slices.Concat(keys(declConverters), keys(statementKinds), keys(typeSpecifierKinds)),
		},
		{
			what:    "block",
			kind:    tsnode.KindCompoundStatement,
			handles: slices.Concat(keys(stmtConverters), keys(typeSpecifierKinds), []tsnode.Kind{tsnode.KindFunctionDefinition}),
		},
		{
			what:    "switchStmt",
			kind:    tsnode.KindCompoundStatement,
			handles: slices.Concat(keys(stmtConverters), keys(typeSpecifierKinds), []tsnode.Kind{tsnode.KindFunctionDefinition}),
		},
		{
			what:    "caseClause",
			kind:    tsnode.KindCaseStatement,
			handles: slices.Concat(keys(stmtConverters), keys(typeSpecifierKinds), []tsnode.Kind{tsnode.KindFunctionDefinition}),
		},
		{
			what:    "elseClause",
			kind:    tsnode.KindElseClause,
			handles: slices.Concat(keys(stmtConverters), keys(typeSpecifierKinds), []tsnode.Kind{tsnode.KindFunctionDefinition}),
		},
		{what: "exprStmt", kind: tsnode.KindExpressionStatement, handles: keys(exprConverters)},
		{what: "returnStmt", kind: tsnode.KindReturnStatement, handles: keys(exprConverters)},
		{
			what:    "parenExpr",
			kind:    tsnode.KindParenthesizedExpression,
			handles: slices.Concat(keys(exprConverters), []tsnode.Kind{tsnode.KindCompoundStatement}),
			// Written only inside a preprocessor directive, which is refused whole.
			notRead: []tsnode.Kind{tsnode.KindPreprocDefined},
		},
		{
			what:    "callExpr",
			kind:    tsnode.KindArgumentList,
			handles: slices.Concat(keys(exprConverters), []tsnode.Kind{tsnode.KindCompoundStatement}),
			notRead: []tsnode.Kind{tsnode.KindPreprocDefined},
		},
		{
			what:    "prefab",
			kind:    tsnode.KindArgumentList,
			handles: slices.Concat(tsnode.Subtypes[tsnode.KindExpression], []tsnode.Kind{tsnode.KindCompoundStatement, tsnode.KindPreprocDefined}),
		},
		{what: "prefab", kind: tsnode.KindAttribute, handles: []tsnode.Kind{tsnode.KindArgumentList}},
		{what: "attributes", kind: tsnode.KindAttributeDeclaration, handles: []tsnode.Kind{tsnode.KindAttribute}},
		{
			what:    "initializer",
			kind:    tsnode.KindInitializerList,
			handles: slices.Concat(keys(exprConverters), []tsnode.Kind{tsnode.KindInitializerList}),
		},
		{
			what:    "params",
			kind:    tsnode.KindParameterList,
			handles: []tsnode.Kind{tsnode.KindCompoundStatement, tsnode.KindIdentifier, tsnode.KindParameterDeclaration, tsnode.KindVariadicParameter},
		},
		{what: "specifiers", kind: tsnode.KindDeclaration, handles: []tsnode.Kind{tsnode.KindAttributeDeclaration, tsnode.KindTypeQualifier}},
		{what: "specifiers", kind: tsnode.KindParameterDeclaration, handles: []tsnode.Kind{tsnode.KindAttributeDeclaration, tsnode.KindTypeQualifier}},
		{
			what:    "specifiers",
			kind:    tsnode.KindFunctionDefinition,
			handles: []tsnode.Kind{tsnode.KindAttributeDeclaration, tsnode.KindDeclaration, tsnode.KindTypeQualifier},
		},
		{what: "qualifier", kind: tsnode.KindTypeQualifier, handles: keys(qualifierSpecifiers)},
		{what: "castType", kind: tsnode.KindTypeDescriptor, handles: []tsnode.Kind{tsnode.KindTypeQualifier}},
		{what: "sizedType", kind: tsnode.KindSizedTypeSpecifier, handles: []tsnode.Kind{tsnode.KindTypeQualifier}},
		// Each of these answers whatever it does not expect by naming the token
		// the statement's terminator was owed at, so a child the grammar adds
		// here draws a sentence about where the expression ended.
		{
			what:    "misreadExpr",
			kind:    tsnode.KindDeclaration,
			handles: []tsnode.Kind{tsnode.KindAttributeDeclaration, tsnode.KindStorageClassSpecifier, tsnode.KindTypeQualifier},
		},
		{what: "misreadParts", kind: tsnode.KindAbstractArrayDeclarator, handles: []tsnode.Kind{tsnode.KindTypeQualifier}},
		{what: "misreadParts", kind: tsnode.KindArrayDeclarator, handles: []tsnode.Kind{tsnode.KindTypeQualifier}},
		{what: "misreadParts", kind: tsnode.KindFunctionDeclarator, handles: []tsnode.Kind{tsnode.KindCallExpression, tsnode.KindIdentifier}},
		{what: "misreadParts", kind: tsnode.KindPointerDeclarator, handles: []tsnode.Kind{tsnode.KindTypeQualifier}},
		{
			what:    "arguments",
			kind:    tsnode.KindParameterList,
			handles: []tsnode.Kind{tsnode.KindCompoundStatement, tsnode.KindIdentifier, tsnode.KindParameterDeclaration, tsnode.KindVariadicParameter},
		},
		{
			what:    "argumentExpr",
			kind:    tsnode.KindParameterDeclaration,
			handles: []tsnode.Kind{tsnode.KindAttributeDeclaration, tsnode.KindStorageClassSpecifier, tsnode.KindTypeQualifier},
		},
		{what: "argumentExpr", kind: tsnode.KindTypeDescriptor, handles: []tsnode.Kind{tsnode.KindTypeQualifier}},
		{what: "declaratorParts", kind: tsnode.KindAbstractArrayDeclarator, handles: []tsnode.Kind{tsnode.KindTypeQualifier}},
		{what: "declaratorParts", kind: tsnode.KindAbstractPointerDeclarator, handles: []tsnode.Kind{tsnode.KindTypeQualifier}},
		{what: "declaratorParts", kind: tsnode.KindArrayDeclarator, handles: []tsnode.Kind{tsnode.KindTypeQualifier}},
		{
			what:    "declaratorParts",
			kind:    tsnode.KindFunctionDeclarator,
			handles: []tsnode.Kind{tsnode.KindCallExpression, tsnode.KindIdentifier},
		},
		{what: "declaratorParts", kind: tsnode.KindPointerDeclarator, handles: []tsnode.Kind{tsnode.KindTypeQualifier}},
		// A literal's value is internal/lexer's answer, and re-deriving it from the
		// tree is how a compiler comes to disagree with itself about what '\x41'
		// denotes.
		{what: "charLit", kind: tsnode.KindCharLiteral, notRead: []tsnode.Kind{tsnode.KindCharacter, tsnode.KindEscapeSequence}},
		{what: "stringLit", kind: tsnode.KindStringLiteral, notRead: []tsnode.Kind{tsnode.KindEscapeSequence, tsnode.KindStringContent}},
	}
}

// TestWalksAccountForEveryChildTheGrammarDeclares is what catches a walk
// that reads the slots it expects and drops the rest: a qualifier in a cast,
// an old-style parameter declaration behind a parameter list. Nothing in the
// tree goes missing when one is dropped, so only the grammar's own account notices.
func TestWalksAccountForEveryChildTheGrammarDeclares(t *testing.T) {
	for _, walk := range converterWalks() {
		t.Run(walk.what+"/"+string(walk.kind), func(t *testing.T) {
			declared := tsnode.ChildTypes[walk.kind]
			if len(declared) == 0 {
				t.Fatalf("the grammar gives a %s no children, so this walk has nothing to account for", walk.kind)
			}
			for _, form := range expandKinds(declared) {
				switch {
				case slices.Contains(walk.handles, form):
				case slices.Contains(walk.notRead, form):
				case accountedFor(form):
				default:
					t.Errorf("a %s can hold a %s, which %s neither reads nor refuses by name", walk.kind, form, walk.what)
				}
			}
		})
	}
}

// TestEveryKindTheConverterReadsHasAWalk is the half of the check the grammar
// drives on its own. A kind a dispatch table converts and the grammar gives
// children to is one something has to walk; a row missing here is how the
// children of a kind the grammar has just given some go unread.
func TestEveryKindTheConverterReadsHasAWalk(t *testing.T) {
	walked := map[tsnode.Kind]bool{}
	for _, walk := range converterWalks() {
		walked[walk.kind] = true
	}
	converted := slices.Concat(
		keys(declConverters), keys(declaratorForms), keys(exprConverters),
		keys(stmtConverters), keys(typeSpecs),
	)
	for _, kind := range converted {
		if len(tsnode.ChildTypes[kind]) != 0 && !walked[kind] {
			t.Errorf("the converter reads a %s and the grammar gives one children, which no walk in converterWalks accounts for", kind)
		}
	}
}

// TestBodySlotsAdmitEveryFormAStatementTakes holds the grammar's account of
// what a control statement's body can hold against the forms the converter
// reads a statement as — the direction the child check cannot see, since
// MicroC admits a declaration where the grammar admits a statement alone.
func TestBodySlotsAdmitEveryFormAStatementTakes(t *testing.T) {
	tests := []struct {
		name  string
		kind  tsnode.Kind
		field tsnode.Field
	}{
		{"if", tsnode.KindIfStatement, tsnode.FieldConsequence},
		{"while", tsnode.KindWhileStatement, tsnode.FieldBody},
		{"do", tsnode.KindDoStatement, tsnode.FieldBody},
		{"for", tsnode.KindForStatement, tsnode.FieldBody},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			admitted := expandKinds(tsnode.FieldTypes[tt.kind][tt.field])
			if len(admitted) == 0 {
				t.Fatalf("the grammar declares nothing for the %s of a %s", tt.field, tt.kind)
			}
			for _, form := range keys(stmtConverters) {
				if slices.Contains(admitted, form) || bracedForms[form] {
					continue
				}
				t.Errorf("the converter reads a %s as a statement and the grammar admits none in the %s of a %s, which nothing writes braces around",
					form, tt.field, tt.kind)
			}
		})
	}
}

// bracedForms are the statement forms the C grammar refuses as a control
// statement's body and [converter.applyMarks] writes braces around before the
// grammar sees them.
var bracedForms = map[tsnode.Kind]bool{tsnode.KindDeclaration: true}

// accountedFor reports whether a form the converter does not read is one it has
// an answer for anyway: a refusal naming it, or a rewrite that keeps the
// grammar from ever building it.
func accountedFor(form tsnode.Kind) bool {
	if refusedByName(form) {
		return true
	}
	_, unreachable := unreachableWords[form]
	return unreachable
}

// expandKinds replaces each supertype in a list with the forms that stand for
// it, since a supertype is never a node the tree reports.
func expandKinds(kinds []tsnode.Kind) []tsnode.Kind {
	out := make([]tsnode.Kind, 0, len(kinds))
	for _, kind := range kinds {
		if forms := tsnode.Subtypes[kind]; len(forms) != 0 {
			out = append(out, forms...)
			continue
		}
		out = append(out, kind)
	}
	return out
}

// TestTablesNameKindsTheGrammarHas fails on an entry keyed by a kind the
// grammar does not produce, which is what a table left behind by a grammar that
// dropped a form looks like.
func TestTablesNameKindsTheGrammarHas(t *testing.T) {
	all := map[tsnode.Kind]bool{}
	for _, kind := range tsnode.AllKinds {
		all[kind] = true
	}
	tables := map[string][]tsnode.Kind{
		"admits":               keys(admits),
		"assignOps":            keys(assignOps),
		"binaryOps":            keys(binaryOps),
		"declConverters":       keys(declConverters),
		"declPunctuation":      keys(declPunctuation),
		"declaratorForms":      keys(declaratorForms),
		"declaratorTokens":     keys(declaratorTokens),
		"exprConverters":       keys(exprConverters),
		"incDecOps":            keys(incDecOps),
		"misreadForms":         keys(misreadForms),
		"pointerOps":           keys(pointerOps),
		"qualifierSpecifiers":  keys(qualifierSpecifiers),
		"refusals":             keys(refusals),
		"refusedTokens":        keys(refusedTokens),
		"refusedTokens values": slices.Collect(maps.Values(refusedTokens)),
		"separators":           keys(separators),
		"stmtConverters":       keys(stmtConverters),
		"typeSpecs":            keys(typeSpecs),
		"unaryOps":             keys(unaryOps),
		"unreachableWords":     keys(unreachableWords),
	}
	for name, kinds := range tables {
		for _, kind := range kinds {
			if !all[kind] {
				t.Errorf("%s is keyed by %q, which the grammar does not produce", name, kind)
			}
		}
	}
}

// TestQualifiersTellConstFromConstexpr is the one distinction a converter can
// lose without any node going missing: both keywords arrive as anonymous
// children of the same node, so a walk over named children reads the two
// declarations alike.
func TestQualifiersTellConstFromConstexpr(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		isConst   bool
		constexpr bool
	}{
		{name: "plain", src: "long long a = 1;"},
		{name: "const", src: "const long long a = 1;", isConst: true},
		{name: "constexpr", src: "constexpr long long a = 1;", constexpr: true},
		{name: "both", src: "constexpr const long long a = 1;", isConst: true, constexpr: true},
		{name: "both reversed", src: "const constexpr long long a = 1;", isConst: true, constexpr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decl := onlyVarDecl(t, tt.src)
			if decl.Const != tt.isConst {
				t.Errorf("Const = %v, want %v", decl.Const, tt.isConst)
			}
			if decl.Constexpr != tt.constexpr {
				t.Errorf("Constexpr = %v, want %v", decl.Constexpr, tt.constexpr)
			}
		})
	}
}

// TestScalarTypesComeFromTheTree checks the derivation the closed set of type
// names rests on: every spelling it found is one the lexer scans as a keyword,
// so a spelling holding an identifier -- which is what a gap in the tree's own
// name table would produce -- is caught here rather than admitted as a type.
func TestScalarTypesComeFromTheTree(t *testing.T) {
	if len(scalarTypes) == 0 {
		t.Fatal("no scalar types were derived, so every identifier in type position would be refused")
	}
	for name, kind := range scalarTypes {
		if kind.String() != name {
			t.Errorf("%v is keyed by %q", kind, name)
		}
		// A type is spelled with keywords alone, so the whole spelling scans
		// without producing an identifier.
		l := lexer.New("test.c", name)
		for tok := l.Next(); tok.Kind != lexer.EOF; tok = l.Next() {
			if tok.Kind == lexer.Ident {
				t.Errorf("the spelling %q of %v holds the identifier %q, which the lexer does not reserve", name, kind, tok.Text)
			}
		}
		if diags := l.Diagnostics(); len(diags) != 0 {
			t.Errorf("the spelling %q of %v does not lex cleanly:\n%s", name, kind, diags)
		}
	}
}

func checkSameKinds(t *testing.T, got, want []tsnode.Kind) {
	t.Helper()
	got, want = slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))
	if !slices.Equal(got, want) {
		t.Errorf("table covers %v, the grammar declares %v", got, want)
	}
}

func keys[V any](m map[tsnode.Kind]V) []tsnode.Kind {
	out := make([]tsnode.Kind, 0, len(m))
	for kind := range m {
		out = append(out, kind)
	}
	return out
}

func onlyVarDecl(t *testing.T, src string) *ast.VarDecl {
	t.Helper()
	f, diags, err := Parse("test.c", src)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("%q did not parse cleanly:\n%s", src, diags)
	}
	if len(f.Decls) != 1 {
		t.Fatalf("got %d declarations, want 1", len(f.Decls))
	}
	decl, ok := f.Decls[0].(*ast.VarDecl)
	if !ok {
		t.Fatalf("got a %T, want a variable declaration", f.Decls[0])
	}
	return decl
}

// TestRefusalsWrittenBetweenOperandsAreNamedAtTheOperator holds the position
// rule to the two constructs MicroC meets today, pinning that the derivation
// still reaches them (which a renamed slot would quietly empty) rather than
// asserting the set exactly, which would forbid a construct C gains from joining it.
func TestRefusalsWrittenBetweenOperandsAreNamedAtTheOperator(t *testing.T) {
	for _, kind := range []tsnode.Kind{tsnode.KindCommaExpression, tsnode.KindFieldExpression} {
		if !infixRefusals[kind] {
			t.Errorf("%s is written between two expressions and the grammar no longer says so", kind)
		}
	}
	for kind := range infixRefusals {
		if _, named := refusals[kind]; !named {
			t.Errorf("%s is named at its operator and has no refusal to report there", kind)
		}
	}
}
