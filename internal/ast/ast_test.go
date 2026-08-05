package ast_test

import (
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/source"
)

func pos(line, col int) source.Position {
	return source.Position{File: "a.c", Offset: col, Line: line, Column: col}
}

func TestOpStrings(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "scalar long long", got: ast.Int.String(), want: "long long"},
		{name: "scalar bool", got: ast.Bool.String(), want: "bool"},
		{name: "scalar void", got: ast.Void.String(), want: "void"},
		{name: "unary neg", got: ast.Neg.String(), want: "-"},
		{name: "unary deref", got: ast.Deref.String(), want: "*"},
		{name: "unary addr", got: ast.AddrOf.String(), want: "&"},
		{name: "incdec inc", got: ast.Inc.String(), want: "++"},
		{name: "binary mod", got: ast.Mod.String(), want: "%"},
		{name: "binary logical or", got: ast.LogicalOr.String(), want: "||"},
		{name: "assign shl", got: ast.ShlAssign.String(), want: "<<="},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("String() = %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestEveryOpIsNamed(t *testing.T) {
	for k := ast.Int; k <= ast.Void; k++ {
		if s := k.String(); strings.Contains(s, "ScalarKind(") {
			t.Errorf("ScalarKind %d has no name", k)
		}
	}
	for op := ast.Plus; op <= ast.Deref; op++ {
		if s := op.String(); strings.Contains(s, "UnaryOp(") {
			t.Errorf("UnaryOp %d has no name", op)
		}
	}
	for op := ast.Inc; op <= ast.Dec; op++ {
		if s := op.String(); strings.Contains(s, "IncDecOp(") {
			t.Errorf("IncDecOp %d has no name", op)
		}
	}
	for op := ast.Add; op <= ast.LogicalOr; op++ {
		if s := op.String(); strings.Contains(s, "BinaryOp(") {
			t.Errorf("BinaryOp %d has no name", op)
		}
	}
	for op := ast.Assign; op <= ast.XorAssign; op++ {
		if s := op.String(); strings.Contains(s, "AssignOp(") {
			t.Errorf("AssignOp %d has no name", op)
		}
	}
}

// TestAnUnnamedOperatorRendersAsItsNumber covers the answer a value outside
// the name table gets: rendering as its number rather than an empty string,
// which would let a new operator ship unnamed and read as nothing in a
// diagnostic.
func TestAnUnnamedOperatorRendersAsItsNumber(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "scalar kind", got: ast.ScalarKind(200).String(), want: "ScalarKind(200)"},
		{name: "unary op", got: ast.UnaryOp(200).String(), want: "UnaryOp(200)"},
		{name: "increment op", got: ast.IncDecOp(200).String(), want: "IncDecOp(200)"},
		{name: "binary op", got: ast.BinaryOp(200).String(), want: "BinaryOp(200)"},
		{name: "assign op", got: ast.AssignOp(200).String(), want: "AssignOp(200)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("String() = %q, want %q", tt.got, tt.want)
			}
		})
	}
}

// TestNodePositionDelegation pins the nodes whose position is borrowed from a
// child rather than taken from their own operator token, since that is where a
// diagnostic would otherwise point at the wrong place.
func TestNodePositionDelegation(t *testing.T) {
	x := &ast.Ident{NamePos: pos(1, 1), Name: "x"}
	y := &ast.Ident{NamePos: pos(1, 5), Name: "y"}
	opPos := pos(1, 3)

	tests := []struct {
		name string
		node ast.Node
		want source.Position
	}{
		{
			name: "binary borrows its left operand",
			node: &ast.BinaryExpr{OpPos: opPos, Op: ast.Add, X: x, Y: y},
			want: x.NamePos,
		},
		{
			name: "assignment borrows its target",
			node: &ast.AssignExpr{OpPos: opPos, Op: ast.Assign, Target: x, Value: y},
			want: x.NamePos,
		},
		{
			name: "conditional borrows its condition",
			node: &ast.CondExpr{Question: opPos, Cond: x, Then: y, Else: y},
			want: x.NamePos,
		},
		{
			name: "index borrows the indexed expression",
			node: &ast.IndexExpr{Lbrack: opPos, X: x, Index: y},
			want: x.NamePos,
		},
		{
			name: "call borrows the callee",
			node: &ast.CallExpr{Lparen: opPos, Fun: x},
			want: x.NamePos,
		},
		{
			name: "prefix increment uses its operator",
			node: &ast.IncDecExpr{OpPos: opPos, Op: ast.Inc, X: x},
			want: opPos,
		},
		{
			name: "postfix increment uses its operand",
			node: &ast.IncDecExpr{OpPos: opPos, Op: ast.Inc, Postfix: true, X: x},
			want: x.NamePos,
		},
		{
			name: "unary uses its operator",
			node: &ast.UnaryExpr{OpPos: opPos, Op: ast.Neg, X: x},
			want: opPos,
		},
		{
			name: "expression statement borrows its expression",
			node: &ast.ExprStmt{X: x},
			want: x.NamePos,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.node.Pos(); got != tt.want {
				t.Errorf("Pos() = %v, want %v", got, tt.want)
			}
		})
	}
}
