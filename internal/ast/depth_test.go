package ast_test

import (
	"testing"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/source"
)

// at gives each node a position of its own, so that a diagnostic naming one node
// cannot be mistaken for a diagnostic naming another.
func at(offset int) source.Position {
	return source.Position{File: "a.c", Offset: offset, Line: 1, Column: offset + 1}
}

func name(n string) *ast.Ident { return &ast.Ident{NamePos: at(0), Name: n} }

func intType() ast.Type { return &ast.ScalarType{TypePos: at(0), Kind: ast.Int} }

// mainWith wraps a statement in the smallest file that holds one.
func mainWith(s ast.Stmt) *ast.File {
	return &ast.File{Name: "a.c", Start: at(0), Decls: []ast.Decl{
		&ast.FuncDecl{
			DeclPos: at(0), Result: &ast.ScalarType{TypePos: at(0), Kind: ast.Void}, Name: "main", NamePos: at(0),
			Body: &ast.BlockStmt{Lbrace: at(0), Stmts: []ast.Stmt{s}, Rbrace: at(0)},
		},
	}}
}

// mainWithExpr wraps an expression the same way.
func mainWithExpr(x ast.Expr) *ast.File { return mainWith(&ast.ExprStmt{X: x}) }

// depthOf reports the smallest limit [ast.DeeperThan] reads f under, which is
// the depth of the deepest node in it.
func depthOf(t *testing.T, f *ast.File) int {
	t.Helper()
	const impossible = 4096
	for limit := range impossible {
		if _, deep := ast.DeeperThan(f, limit); !deep {
			return limit
		}
	}
	t.Fatalf("no limit under %d reads the file", impossible)
	return 0
}

// TestOneMoreConstructIsOneMoreLevel is the property the bound is stated
// in: a limit counted in nodes only bounds recursion if each nested
// construct raises the count by the nodes it is built from. Each row
// states its own step, so a missed field shows as a step of zero.
func TestOneMoreConstructIsOneMoreLevel(t *testing.T) {
	tests := []struct {
		name  string
		build func(n int) *ast.File
		step  int
	}{
		{
			name: "unary operators",
			step: 1,
			build: func(n int) *ast.File {
				var x ast.Expr = name("g")
				for range n {
					x = &ast.UnaryExpr{OpPos: at(0), Op: ast.LogicalNot, X: x}
				}
				return mainWithExpr(x)
			},
		},
		{
			name: "casts",
			step: 1,
			build: func(n int) *ast.File {
				var x ast.Expr = name("g")
				for range n {
					x = &ast.CastExpr{Lparen: at(0), Type: intType(), X: x}
				}
				return mainWithExpr(x)
			},
		},
		{
			name: "calls, nested in an argument",
			step: 1,
			build: func(n int) *ast.File {
				var x ast.Expr = name("g")
				for range n {
					x = &ast.CallExpr{Lparen: at(0), Fun: name("f"), Args: []ast.Expr{x}}
				}
				return mainWithExpr(x)
			},
		},
		{
			name: "subscripts, nested in the index",
			step: 1,
			build: func(n int) *ast.File {
				var x ast.Expr = name("g")
				for range n {
					x = &ast.IndexExpr{Lbrack: at(0), X: name("a"), Index: x}
				}
				return mainWithExpr(x)
			},
		},
		{
			name: "conditionals, nested in the else arm",
			step: 1,
			build: func(n int) *ast.File {
				var x ast.Expr = name("g")
				for range n {
					x = &ast.CondExpr{Question: at(0), Cond: name("c"), Then: name("t"), Else: x}
				}
				return mainWithExpr(x)
			},
		},
		{
			name: "binary operators, nested in the right operand",
			step: 1,
			build: func(n int) *ast.File {
				var x ast.Expr = name("g")
				for range n {
					x = &ast.BinaryExpr{OpPos: at(0), Op: ast.Add, X: name("l"), Y: x}
				}
				return mainWithExpr(x)
			},
		},
		{
			name: "assignments, nested in the value",
			step: 1,
			build: func(n int) *ast.File {
				var x ast.Expr = name("g")
				for range n {
					x = &ast.AssignExpr{OpPos: at(0), Op: ast.Assign, Target: name("t"), Value: x}
				}
				return mainWithExpr(x)
			},
		},
		{
			name: "increments",
			step: 1,
			build: func(n int) *ast.File {
				var x ast.Expr = name("g")
				for range n {
					x = &ast.IncDecExpr{OpPos: at(0), Op: ast.Inc, X: x}
				}
				return mainWithExpr(x)
			},
		},
		{
			name: "brace initializer elements",
			step: 1,
			build: func(n int) *ast.File {
				var x ast.Expr = name("g")
				for range n {
					x = &ast.UnaryExpr{OpPos: at(0), Op: ast.Neg, X: x}
				}
				return &ast.File{Name: "a.c", Start: at(0), Decls: []ast.Decl{
					&ast.VarDecl{
						DeclPos: at(0), Type: intType(), Name: "v", NamePos: at(0),
						Init: &ast.InitListExpr{Lbrace: at(0), Elems: []ast.Expr{x}},
					},
				}}
			},
		},
		{
			name: "blocks",
			step: 1,
			build: func(n int) *ast.File {
				var s ast.Stmt = &ast.ExprStmt{X: name("g")}
				for range n {
					s = &ast.BlockStmt{Lbrace: at(0), Stmts: []ast.Stmt{s}, Rbrace: at(0)}
				}
				return mainWith(s)
			},
		},
		{
			name: "if statements over a braced arm",
			step: 2,
			build: func(n int) *ast.File {
				var s ast.Stmt = &ast.ExprStmt{X: name("g")}
				for range n {
					s = &ast.IfStmt{IfPos: at(0), Cond: name("c"),
						Then: &ast.BlockStmt{Lbrace: at(0), Stmts: []ast.Stmt{s}, Rbrace: at(0)}}
				}
				return mainWith(s)
			},
		},
		{
			name: "if statements over an else arm",
			step: 1,
			build: func(n int) *ast.File {
				var s ast.Stmt = &ast.ExprStmt{X: name("g")}
				for range n {
					s = &ast.IfStmt{IfPos: at(0), Cond: name("c"), Then: &ast.EmptyStmt{Semi: at(0)}, Else: s}
				}
				return mainWith(s)
			},
		},
		{
			name: "while loops",
			step: 1,
			build: func(n int) *ast.File {
				var s ast.Stmt = &ast.ExprStmt{X: name("g")}
				for range n {
					s = &ast.WhileStmt{WhilePos: at(0), Cond: name("c"), Body: s}
				}
				return mainWith(s)
			},
		},
		{
			name: "do loops",
			step: 1,
			build: func(n int) *ast.File {
				var s ast.Stmt = &ast.ExprStmt{X: name("g")}
				for range n {
					s = &ast.DoStmt{DoPos: at(0), Body: s, Cond: name("c")}
				}
				return mainWith(s)
			},
		},
		{
			name: "for loops",
			step: 1,
			build: func(n int) *ast.File {
				var s ast.Stmt = &ast.ExprStmt{X: name("g")}
				for range n {
					s = &ast.ForStmt{ForPos: at(0), Cond: name("c"), Body: s}
				}
				return mainWith(s)
			},
		},
		{
			name: "switches over a case body",
			step: 2,
			build: func(n int) *ast.File {
				var s ast.Stmt = &ast.ExprStmt{X: name("g")}
				for range n {
					s = &ast.SwitchStmt{SwitchPos: at(0), Tag: name("t"), Lbrace: at(0),
						Cases: []*ast.CaseClause{{CasePos: at(0), Body: []ast.Stmt{s}}}}
				}
				return mainWith(s)
			},
		},
		{
			name: "pointers in a variable's type",
			step: 1,
			build: func(n int) *ast.File {
				typ := intType()
				for range n {
					typ = &ast.PointerType{Star: at(0), Elem: typ}
				}
				return &ast.File{Name: "a.c", Start: at(0), Decls: []ast.Decl{
					&ast.VarDecl{DeclPos: at(0), Type: typ, Name: "v", NamePos: at(0)},
				}}
			},
		},
		{
			name: "pointers in a parameter's type",
			step: 1,
			build: func(n int) *ast.File {
				typ := intType()
				for range n {
					typ = &ast.PointerType{Star: at(0), Elem: typ}
				}
				return &ast.File{Name: "a.c", Start: at(0), Decls: []ast.Decl{
					&ast.FuncDecl{
						DeclPos: at(0), Result: intType(), Name: "f", NamePos: at(0),
						Params: []*ast.Param{{ParamPos: at(0), Type: typ, Name: "p", NamePos: at(0)}},
					},
				}}
			},
		},
		{
			name: "array bounds, nested in the bound expression",
			step: 1,
			build: func(n int) *ast.File {
				var size ast.Expr = &ast.IntLit{ValuePos: at(0), Value: 4}
				for range n {
					size = &ast.UnaryExpr{OpPos: at(0), Op: ast.Plus, X: size}
				}
				return &ast.File{Name: "a.c", Start: at(0), Decls: []ast.Decl{
					&ast.VarDecl{
						DeclPos: at(0), Name: "a", NamePos: at(0),
						Type: &ast.ArrayType{Lbrack: at(0), Elem: intType(), Size: size},
					},
				}}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := depthOf(t, tt.build(1))
			for _, n := range []int{2, 3, 8} {
				want := base + (n-1)*tt.step
				if got := depthOf(t, tt.build(n)); got != want {
					t.Errorf("%d of them measure %d levels, want %d: one is %d and each adds %d",
						n, got, want, base, tt.step)
				}
			}
		})
	}
}

// TestTheLimitIsTheDeepestTreeRead is the boundary the answer turns on. A bound
// stated as a maximum and applied one level early refuses a tree it promises to
// read, and every row of the table above would still pass.
func TestTheLimitIsTheDeepestTreeRead(t *testing.T) {
	f := mainWithExpr(&ast.UnaryExpr{OpPos: at(0), Op: ast.Neg, X: name("g")})
	depth := depthOf(t, f)
	if _, deep := ast.DeeperThan(f, depth); deep {
		t.Errorf("a limit of %d refuses a tree %d levels deep", depth, depth)
	}
	if _, deep := ast.DeeperThan(f, depth-1); !deep {
		t.Errorf("a limit of %d reads a tree %d levels deep", depth-1, depth)
	}
}

// TestTheRefusalNamesTheOutermostNodeExcluded pins which node a refusal points
// at. Every node in the chain encloses the next, so any of them would read as
// plausible; what a reader needs is the one to cut back to, which is the first
// past the limit in reading order.
func TestTheRefusalNamesTheOutermostNodeExcluded(t *testing.T) {
	deepest := name("deepest")
	deepest.NamePos = at(30)
	inner := &ast.UnaryExpr{OpPos: at(20), Op: ast.Neg, X: deepest}
	outer := &ast.UnaryExpr{OpPos: at(10), Op: ast.Neg, X: inner}
	f := mainWithExpr(outer)

	depth := depthOf(t, f)
	tests := []struct {
		name  string
		limit int
		want  source.Position
	}{
		{name: "the deepest node alone", limit: depth - 1, want: deepest.NamePos},
		{name: "the operator above it", limit: depth - 2, want: inner.OpPos},
		{name: "the operator above that", limit: depth - 3, want: outer.OpPos},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, deep := ast.DeeperThan(f, tt.limit)
			if !deep {
				t.Fatalf("a limit of %d reads a tree %d levels deep", tt.limit, depth)
			}
			if got != tt.want {
				t.Errorf("named %v, want %v", got, tt.want)
			}
		})
	}
}

// TestTheRefusalReadsInSourceOrder covers a file whose nesting is not in its
// first declaration and not in a node's first child. Reading order is what makes
// the position the point to cut back to, and a walk that answered with whichever
// branch it reached last would name a construct written after the one at fault.
func TestTheRefusalReadsInSourceOrder(t *testing.T) {
	deep := func(from int) ast.Expr {
		var x ast.Expr = &ast.Ident{NamePos: at(from + 2), Name: "g"}
		x = &ast.UnaryExpr{OpPos: at(from + 1), Op: ast.Neg, X: x}
		return &ast.UnaryExpr{OpPos: at(from), Op: ast.Neg, X: x}
	}
	first, second := deep(10), deep(50)
	f := mainWith(&ast.ExprStmt{X: &ast.BinaryExpr{OpPos: at(0), Op: ast.Add, X: first, Y: second}})

	got, refused := ast.DeeperThan(f, depthOf(t, f)-1)
	if !refused {
		t.Fatal("a limit one below the depth read the tree")
	}
	if want := at(12); got != want {
		t.Errorf("named %v, want %v, the deepest node of the left operand", got, want)
	}
}

// TestDeeperThanReadsEveryDeclaration covers the one edge [File] holds rather
// than a node: the walk reaches the declarations itself, so a file whose nesting
// is in a later one has to be refused for it.
func TestDeeperThanReadsEveryDeclaration(t *testing.T) {
	shallow := &ast.VarDecl{DeclPos: at(0), Type: intType(), Name: "v", NamePos: at(0)}
	var x ast.Expr = &ast.Ident{NamePos: at(60), Name: "g"}
	for range 8 {
		x = &ast.UnaryExpr{OpPos: at(50), Op: ast.Neg, X: x}
	}
	nested := mainWithExpr(x).Decls[0]

	f := &ast.File{Name: "a.c", Start: at(0), Decls: []ast.Decl{shallow, nested}}
	if _, deep := ast.DeeperThan(f, depthOf(t, &ast.File{Name: "a.c", Decls: []ast.Decl{shallow}})); !deep {
		t.Error("a limit that reads the first declaration read a file whose second is deeper")
	}
}

// TestDeeperThanReadsNothingFromNoFile covers the argument [sema.Analyze]
// rejects before it reaches the tree, so that the bound is not what reports it.
func TestDeeperThanReadsNothingFromNoFile(t *testing.T) {
	if _, deep := ast.DeeperThan(nil, 0); deep {
		t.Error("no file nests too deeply")
	}
}
