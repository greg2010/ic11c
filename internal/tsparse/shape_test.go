package tsparse_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ast"
)

// The shape functions render a tree as a fully parenthesized prefix form, so
// grouping is explicit and a precedence defect cannot hide behind
// reassociated output. Every node visited is held to carrying a valid
// position, which is what makes [checkEveryPosition] a full-tree walk.

func exprShape(t *testing.T, e ast.Expr) string {
	t.Helper()
	checkPos(t, e)
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.IntLit:
		if x.Hex {
			return "hex:" + strconv.FormatInt(x.Value, 10)
		}
		return strconv.FormatInt(x.Value, 10)
	case *ast.CharLit:
		return "char:" + strconv.FormatInt(x.Value, 10)
	case *ast.FloatLit:
		return strconv.FormatFloat(x.Value, 'g', -1, 64)
	case *ast.BoolLit:
		return strconv.FormatBool(x.Value)
	case *ast.StringLit:
		return strconv.Quote(x.Value)
	case *ast.UnaryExpr:
		return sexpr(x.Op.String(), exprShape(t, x.X))
	case *ast.IncDecExpr:
		prefix := "pre"
		if x.Postfix {
			prefix = "post"
		}
		return sexpr(prefix+x.Op.String(), exprShape(t, x.X))
	case *ast.BinaryExpr:
		return sexpr(x.Op.String(), exprShape(t, x.X), exprShape(t, x.Y))
	case *ast.AssignExpr:
		return sexpr(x.Op.String(), exprShape(t, x.Target), exprShape(t, x.Value))
	case *ast.CondExpr:
		return sexpr("?:", exprShape(t, x.Cond), exprShape(t, x.Then), exprShape(t, x.Else))
	case *ast.IndexExpr:
		return sexpr("index", exprShape(t, x.X), exprShape(t, x.Index))
	case *ast.CallExpr:
		return sexpr("call", append([]string{exprShape(t, x.Fun)}, exprShapes(t, x.Args)...)...)
	case *ast.CastExpr:
		return sexpr("cast", typeShape(t, x.Type), exprShape(t, x.X))
	case *ast.InitListExpr:
		return sexpr("init", exprShapes(t, x.Elems)...)
	case *ast.BadExpr:
		return "(badexpr)"
	default:
		t.Errorf("unhandled expression node %T", e)
		return "(?)"
	}
}

func exprShapes(t *testing.T, es []ast.Expr) []string {
	t.Helper()
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = exprShape(t, e)
	}
	return out
}

func typeShape(t *testing.T, typ ast.Type) string {
	t.Helper()
	checkPos(t, typ)
	switch x := typ.(type) {
	case *ast.ScalarType:
		return x.Kind.String()
	case *ast.PointerType:
		return sexpr("ptr", typeShape(t, x.Elem))
	case *ast.ArrayType:
		size := "-"
		if x.Size != nil {
			size = exprShape(t, x.Size)
		}
		return sexpr("array", size, typeShape(t, x.Elem))
	default:
		t.Errorf("unhandled type node %T", typ)
		return "(?)"
	}
}

func stmtShape(t *testing.T, s ast.Stmt) string {
	t.Helper()
	checkPos(t, s)
	switch x := s.(type) {
	case *ast.BlockStmt:
		return sexpr("block", stmtShapes(t, x.Stmts)...)
	case *ast.ExprStmt:
		return sexpr("expr", exprShape(t, x.X))
	case *ast.EmptyStmt:
		return "(empty)"
	case *ast.IfStmt:
		parts := []string{exprShape(t, x.Cond), stmtShape(t, x.Then)}
		if x.Else != nil {
			parts = append(parts, stmtShape(t, x.Else))
		}
		return sexpr("if", parts...)
	case *ast.WhileStmt:
		return sexpr("while", exprShape(t, x.Cond), stmtShape(t, x.Body))
	case *ast.DoStmt:
		return sexpr("do", stmtShape(t, x.Body), optExpr(t, x.Cond))
	case *ast.ForStmt:
		return sexpr("for", optStmt(t, x.Init), optExpr(t, x.Cond), optExpr(t, x.Post), stmtShape(t, x.Body))
	case *ast.SwitchStmt:
		parts := []string{exprShape(t, x.Tag)}
		for _, c := range x.Cases {
			checkPos(t, c)
			label := "default"
			if c.Value != nil {
				label = "case " + exprShape(t, c.Value)
			}
			parts = append(parts, sexpr(label, stmtShapes(t, c.Body)...))
		}
		return sexpr("switch", parts...)
	case *ast.BreakStmt:
		return "(break)"
	case *ast.ContinueStmt:
		return "(continue)"
	case *ast.ReturnStmt:
		if x.Result == nil {
			return "(return)"
		}
		return sexpr("return", exprShape(t, x.Result))
	case *ast.VarDecl:
		return declShape(t, x)
	case *ast.BadStmt:
		return "(badstmt)"
	default:
		t.Errorf("unhandled statement node %T", s)
		return "(?)"
	}
}

func stmtShapes(t *testing.T, ss []ast.Stmt) []string {
	t.Helper()
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = stmtShape(t, s)
	}
	return out
}

func declShape(t *testing.T, d ast.Decl) string {
	t.Helper()
	checkPos(t, d)
	switch x := d.(type) {
	case *ast.FuncDecl:
		parts := []string{typeShape(t, x.Result), x.Name}
		for _, prm := range x.Params {
			checkPos(t, prm)
			label := "param"
			if prm.Const {
				label = "constparam"
			}
			parts = append(parts, sexpr(label, typeShape(t, prm.Type), orEmpty(prm.Name)))
		}
		if x.Body == nil {
			return sexpr("proto", parts...)
		}
		return sexpr("func", append(parts, stmtShape(t, x.Body))...)
	case *ast.VarDecl:
		label := "var"
		switch {
		case x.Constexpr && x.Const:
			label = "constconstexprvar"
		case x.Constexpr:
			label = "constexprvar"
		case x.Const:
			label = "constvar"
		}
		fields := []string{typeShape(t, x.Type)}
		if x.Init != nil {
			fields = append(fields, exprShape(t, x.Init))
		}
		decl := sexpr(label, sexpr(x.Name, fields...))
		if x.Prefab != nil {
			checkPos(t, x.Prefab)
			return sexpr("prefab", strconv.Quote(x.Prefab.Name), decl)
		}
		return decl
	case *ast.BadDecl:
		return "(baddecl)"
	default:
		t.Errorf("unhandled declaration node %T", d)
		return "(?)"
	}
}

func optExpr(t *testing.T, e ast.Expr) string {
	t.Helper()
	if e == nil {
		return "-"
	}
	return exprShape(t, e)
}

func optStmt(t *testing.T, s ast.Stmt) string {
	t.Helper()
	if s == nil {
		return "-"
	}
	return stmtShape(t, s)
}

func orEmpty(name string) string {
	if name == "" {
		return "-"
	}
	return name
}

func sexpr(head string, parts ...string) string {
	if len(parts) == 0 {
		return "(" + head + ")"
	}
	return "(" + head + " " + strings.Join(parts, " ") + ")"
}

// checkEveryPosition holds every node of a tree to carrying a position a reader
// can open, by rendering the tree and discarding the rendering: the renderers
// call [checkPos] at each node they visit, and they visit all of them.
func checkEveryPosition(t *testing.T, f *ast.File) {
	t.Helper()
	fileShape(t, f)
}

// checkPos holds a node's position to a place a reader can open. Validity alone
// is a line number past zero, which a position naming no file and sitting at no
// column still satisfies.
func checkPos(t *testing.T, n ast.Node) {
	t.Helper()
	switch at := n.Pos(); {
	case !at.IsValid():
		t.Errorf("node %T carries no source position", n)
	case at.File == "":
		t.Errorf("node %T sits at %d:%d in no named file", n, at.Line, at.Column)
	case at.Column < 1 || at.Offset < 0:
		t.Errorf("node %T sits at %d:%d, byte %d, which is not a place in a file", n, at.Line, at.Column, at.Offset)
	}
}
