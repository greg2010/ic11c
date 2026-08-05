package ast

import (
	"fmt"
	goast "go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/source"
)

func here() source.Position { return source.Position{File: "a.c", Offset: 1, Line: 1, Column: 1} }

// kid gives a node that holds none, so that a field populated with it is
// distinguishable from every other field of the same node.
func kid(name string) *Ident { return &Ident{NamePos: here(), Name: name} }

// everyNode holds one instance of every node kind the package declares,
// with every field that can hold a node holding one, so the
// reflection-based comparison in [TestEveryNodeFieldIsWalked] has
// something to check against. [File] is excluded: it is not a [Node].
func everyNode() []Node {
	return []Node{
		&FuncDecl{
			DeclPos: here(), Result: &ScalarType{TypePos: here(), Kind: Void}, Name: "f", NamePos: here(),
			Params: []*Param{{ParamPos: here(), Type: &ScalarType{TypePos: here(), Kind: Int}, Name: "p", NamePos: here()}},
			Body:   &BlockStmt{Lbrace: here(), Rbrace: here()},
		},
		&VarDecl{
			DeclPos: here(),
			Prefab:  &PrefabAttr{At: here(), Name: "P", NamePos: here()},
			Type:    &ScalarType{TypePos: here(), Kind: Int},
			Name:    "v", NamePos: here(), Init: kid("init"),
		},
		&BadDecl{From: here()},
		&Param{ParamPos: here(), Type: &ScalarType{TypePos: here(), Kind: Int}, Name: "p", NamePos: here()},
		&PrefabAttr{At: here(), Name: "P", NamePos: here()},
		&CaseClause{CasePos: here(), Value: kid("value"), Body: []Stmt{&ExprStmt{X: kid("body")}}},

		&BlockStmt{Lbrace: here(), Stmts: []Stmt{&ExprStmt{X: kid("stmt")}}, Rbrace: here()},
		&ExprStmt{X: kid("x")},
		&EmptyStmt{Semi: here()},
		&IfStmt{IfPos: here(), Cond: kid("cond"), Then: &ExprStmt{X: kid("then")}, Else: &ExprStmt{X: kid("else")}},
		&WhileStmt{WhilePos: here(), Cond: kid("cond"), Body: &ExprStmt{X: kid("body")}},
		&DoStmt{DoPos: here(), Body: &ExprStmt{X: kid("body")}, Cond: kid("cond")},
		&ForStmt{
			ForPos: here(), Init: &ExprStmt{X: kid("init")}, Cond: kid("cond"),
			Post: kid("post"), Body: &ExprStmt{X: kid("body")},
		},
		&SwitchStmt{
			SwitchPos: here(), Tag: kid("tag"), Lbrace: here(),
			Cases: []*CaseClause{{CasePos: here(), Value: kid("case")}},
		},
		&BreakStmt{BreakPos: here()},
		&ContinueStmt{ContinuePos: here()},
		&ReturnStmt{ReturnPos: here(), Result: kid("result")},
		&BadStmt{From: here()},

		kid("ident"),
		&IntLit{ValuePos: here(), Value: 1},
		&CharLit{ValuePos: here(), Value: 'a'},
		&FloatLit{ValuePos: here(), Value: 1.5},
		&BoolLit{ValuePos: here(), Value: true},
		&StringLit{ValuePos: here(), Value: "s"},
		&UnaryExpr{OpPos: here(), Op: Neg, X: kid("x")},
		&IncDecExpr{OpPos: here(), Op: Inc, X: kid("x")},
		&BinaryExpr{OpPos: here(), Op: Add, X: kid("x"), Y: kid("y")},
		&AssignExpr{OpPos: here(), Op: Assign, Target: kid("target"), Value: kid("value")},
		&CondExpr{Question: here(), Cond: kid("cond"), Then: kid("then"), Else: kid("else")},
		&IndexExpr{Lbrack: here(), X: kid("x"), Index: kid("index")},
		&CallExpr{Lparen: here(), Fun: kid("fun"), Args: []Expr{kid("arg")}},
		&CastExpr{Lparen: here(), Type: &ScalarType{TypePos: here(), Kind: Int}, X: kid("x")},
		&InitListExpr{Lbrace: here(), Elems: []Expr{kid("elem")}},
		&BadExpr{From: here()},

		&ScalarType{TypePos: here(), Kind: Int},
		&PointerType{Star: here(), Elem: &ScalarType{TypePos: here(), Kind: Int}},
		&ArrayType{Lbrack: here(), Elem: &ScalarType{TypePos: here(), Kind: Int}, Size: kid("size")},
	}
}

// packageSource parses the package's own non-test files. It is how the two tests
// below ask what this package declares instead of being told: a list kept by
// hand is exactly what they exist to check.
func packageSource(t *testing.T) []*goast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	fset := token.NewFileSet()
	var files []*goast.File
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := goparser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		t.Fatal("the package directory holds no source, so nothing was read")
	}
	return files
}

// declaredNodeTypes gives every type this package declares that implements
// [Node], which is every type declaring Pos: the interface has no other method.
func declaredNodeTypes(t *testing.T) []string {
	t.Helper()
	var named []string
	for _, file := range packageSource(t) {
		for _, decl := range file.Decls {
			fn, isFunc := decl.(*goast.FuncDecl)
			if !isFunc || fn.Name.Name != "Pos" || fn.Recv == nil || len(fn.Recv.List) != 1 {
				continue
			}
			named = append(named, receiverName(t, fn.Recv.List[0].Type))
		}
	}
	if len(named) == 0 {
		t.Fatal("no type in this package declares Pos, so the scan found nothing to hold everyNode to")
	}
	return named
}

// receiverName gives the type a method is declared on, without the pointer.
//
// It fails rather than skipping a receiver it cannot read: a dropped
// receiver is a node type the scan silently stops covering.
func receiverName(t *testing.T, expr goast.Expr) string {
	t.Helper()
	if star, isPointer := expr.(*goast.StarExpr); isPointer {
		expr = star.X
	}
	ident, isName := expr.(*goast.Ident)
	if !isName {
		t.Fatalf("a Pos method is declared on %T, which this scan cannot name", expr)
	}
	return ident.Name
}

// TestEveryNodeTypeIsListed keeps [TestEveryNodeFieldIsWalked] covering the
// whole package rather than whatever [everyNode] remembers to list: a type
// missing from both leaves the suite green while its fields go unwalked and
// a chain through them slips past the depth bound.
func TestEveryNodeTypeIsListed(t *testing.T) {
	listed := make(map[string]bool)
	for _, n := range everyNode() {
		listed[strings.TrimPrefix(fmt.Sprintf("%T", n), "*ast.")] = true
	}
	for _, name := range declaredNodeTypes(t) {
		if !listed[name] {
			t.Errorf("%s implements Node and everyNode holds none, so nothing requires "+
				"appendChildren to walk the fields it holds", name)
		}
	}
}

// TestNoNodeTypeInheritsItsPosition is the premise [declaredNodeTypes] scans
// by: a type embedding another that declares Pos would implement Node
// without declaring Pos itself, and the scan would miss it. No type here
// embeds another.
func TestNoNodeTypeInheritsItsPosition(t *testing.T) {
	for _, file := range packageSource(t) {
		goast.Inspect(file, func(n goast.Node) bool {
			spec, isType := n.(*goast.TypeSpec)
			if !isType {
				return true
			}
			structure, isStruct := spec.Type.(*goast.StructType)
			if !isStruct {
				return true
			}
			for _, field := range structure.Fields.List {
				if len(field.Names) == 0 {
					t.Errorf("%s embeds a type, so it may implement Node without declaring Pos, "+
						"which is the only thing the scan over this package looks for", spec.Name.Name)
				}
			}
			return true
		})
	}
}

var nodeInterface = reflect.TypeFor[Node]()

// declaredChildren gives every node the exported fields of n hold, in field
// order, found by reflection rather than by the hand-written walk it is there to
// check.
func declaredChildren(t testing.TB, n Node) []Node {
	t.Helper()
	v := reflect.ValueOf(n).Elem()
	var found []Node
	for i := range v.NumField() {
		if !v.Type().Field(i).IsExported() {
			continue
		}
		found = appendReflected(t, found, v.Field(i))
	}
	return found
}

func appendReflected(t testing.TB, dst []Node, f reflect.Value) []Node {
	t.Helper()
	if f.Kind() == reflect.Slice {
		if !f.Type().Elem().Implements(nodeInterface) {
			return dst
		}
		for i := range f.Len() {
			dst = appendHeld(t, dst, f.Index(i))
		}
		return dst
	}
	if !f.Type().Implements(nodeInterface) {
		return dst
	}
	return appendHeld(t, dst, f)
}

// appendHeld fails rather than skipping a value it cannot classify. What this
// test compares is a set against a set, so a node dropped here is a node the
// walk is no longer held to, and the comparison would still pass.
func appendHeld(t testing.TB, dst []Node, f reflect.Value) []Node {
	t.Helper()
	if f.IsNil() {
		return dst
	}
	held, ok := f.Interface().(Node)
	if !ok {
		t.Fatalf("a field of type %s holds a %T, which is not a Node", f.Type(), f.Interface())
	}
	return append(dst, held)
}

// TestEveryNodeFieldIsWalked is what the depth bound rests on: a field
// holding nodes that [appendChildren] does not return makes every tree
// written through it read shallower than it is. Order is compared too,
// since a refusal names the first node past the limit in reading order.
func TestEveryNodeFieldIsWalked(t *testing.T) {
	for _, n := range everyNode() {
		t.Run(fmt.Sprintf("%T", n), func(t *testing.T) {
			want := declaredChildren(t, n)
			got := appendChildren(nil, n)
			if len(got) != len(want) {
				t.Fatalf("walked %d children, want the %d fields hold", len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("child %d is %#v, want %#v", i, got[i], want[i])
				}
			}
		})
	}
}
