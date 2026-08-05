package sema_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/sema"
	"github.com/greg2010/ic11c/internal/source"
)

// The nesting bound is a property of the tree rather than of whatever built one,
// so these tests hand analysis a tree built here instead of parsing a source.
// That is the point of where the bound sits: a front end holding to its own
// limit covers the passes behind it only for the trees it produced, and nothing
// stops a tree arriving from somewhere else.

// somewhere is the position the wrapper around a built chain carries.
func somewhere() source.Position { return level(0) }

// level gives the position of the nth construct in a chain, distinct from every
// other, so that a refusal naming one construct cannot pass for a refusal naming
// another.
func level(n int) source.Position {
	return source.Position{File: "test.c", Offset: n, Line: 1, Column: n + 1}
}

// nestedShapes is every chain [nestedTree] writes. Each costs analysis one level
// and one recursion per construct, and they are the two dearest per level the
// language has: a call and an unbraced if statement. A bound checked against a
// cheaper shape is a bound these two walk straight past.
var nestedShapes = []string{"calls", "ifs"}

// nestedTree writes a whole program whose body nests n of one construct inside
// another. Each construct adds exactly one level, so the depth of what comes
// back is the wrapper's own depth plus n.
func nestedTree(shape string, n int) *ast.File {
	pos := somewhere()
	assign := func() ast.Stmt {
		return &ast.ExprStmt{X: &ast.AssignExpr{
			OpPos: pos, Op: ast.Assign,
			Target: &ast.Ident{NamePos: pos, Name: "g"},
			Value:  &ast.IntLit{ValuePos: pos, Value: 1},
		}}
	}

	// The chain is written outermost first, so the construct furthest in is the
	// last one built and carries the highest position.
	var body []ast.Stmt
	switch shape {
	case "calls":
		var x ast.Expr = &ast.IntLit{ValuePos: level(n + 1), Value: 1}
		for i := n; i >= 1; i-- {
			x = &ast.CallExpr{Lparen: level(i), Fun: &ast.Ident{NamePos: level(i), Name: "f"}, Args: []ast.Expr{x}}
		}
		body = []ast.Stmt{&ast.ExprStmt{X: &ast.AssignExpr{
			OpPos: pos, Op: ast.Assign, Target: &ast.Ident{NamePos: pos, Name: "g"}, Value: x,
		}}}
	case "ifs":
		s := assign()
		for i := n; i >= 1; i-- {
			s = &ast.IfStmt{IfPos: level(i), Cond: &ast.Ident{NamePos: level(i), Name: "g"}, Then: s}
		}
		body = []ast.Stmt{s}
	default:
		panic("unknown shape " + shape)
	}

	return &ast.File{Name: "test.c", Start: pos, Decls: []ast.Decl{
		&ast.VarDecl{DeclPos: pos, Type: &ast.ScalarType{TypePos: pos, Kind: ast.Int}, Name: "g", NamePos: pos},
		&ast.FuncDecl{
			DeclPos: pos, Result: &ast.ScalarType{TypePos: pos, Kind: ast.Int}, Name: "f", NamePos: pos,
			Params: []*ast.Param{{ParamPos: pos, Type: &ast.ScalarType{TypePos: pos, Kind: ast.Int}, Name: "x", NamePos: pos}},
			Body: &ast.BlockStmt{Lbrace: pos, Stmts: []ast.Stmt{
				&ast.ReturnStmt{ReturnPos: pos, Result: &ast.Ident{NamePos: pos, Name: "x"}},
			}, Rbrace: pos},
		},
		&ast.FuncDecl{
			DeclPos: pos, Result: &ast.ScalarType{TypePos: pos, Kind: ast.Void}, Name: "main", NamePos: pos,
			Body: &ast.BlockStmt{Lbrace: pos, Stmts: body, Rbrace: pos},
		},
	}}
}

// nestedToDepth writes a program of that shape nesting exactly depth levels, so
// that a test naming the bound hands analysis a tree at it rather than near it.
//
// The wrapper around the chain costs levels of its own, which is a property of
// the tree rather than a number to state here, so it is measured and subtracted.
func nestedToDepth(t *testing.T, shape string, depth int) *ast.File {
	t.Helper()
	one := treeDepth(t, nestedTree(shape, 1))
	if depth < one {
		t.Fatalf("no chain of %s is %d levels deep: one of them is already %d", shape, depth, one)
	}
	f := nestedTree(shape, 1+depth-one)
	if got := treeDepth(t, f); got != depth {
		t.Fatalf("built a chain of %s %d levels deep, want %d", shape, got, depth)
	}
	return f
}

// treeDepth reports how deep f nests, which is the smallest limit that reads it.
func treeDepth(t *testing.T, f *ast.File) int {
	t.Helper()
	low, high := 0, 1
	for {
		if _, deep := ast.DeeperThan(f, high); !deep {
			break
		}
		low, high = high, high*2
	}
	for low+1 < high {
		mid := low + (high-low)/2
		if _, deep := ast.DeeperThan(f, mid); deep {
			low = mid
		} else {
			high = mid
		}
	}
	return high
}

// TestTreeDepthIsMeasuredNotGuessed covers the search the tests below build
// their trees with. A chain built to the wrong depth would put the bound under
// test at the wrong place and say nothing about it.
func TestTreeDepthIsMeasuredNotGuessed(t *testing.T) {
	for _, shape := range nestedShapes {
		t.Run(shape, func(t *testing.T) {
			for _, depth := range []int{8, 9, 40, 137} {
				f := nestedToDepth(t, shape, depth)
				if _, deep := ast.DeeperThan(f, depth); deep {
					t.Errorf("a chain of %s built to %d levels is refused by a limit of %d", shape, depth, depth)
				}
				if _, deep := ast.DeeperThan(f, depth-1); !deep {
					t.Errorf("a chain of %s built to %d levels is read by a limit of %d", shape, depth, depth-1)
				}
			}
		})
	}
}

// refusalWording gives the sentence a bound of limit refuses with.
func refusalWording(limit int) string {
	return fmt.Sprintf("nested too deeply; the compiler reads at most %d constructs inside one another, "+
		"and every pass behind the parser walks this tree with a recursion of its own", limit)
}

// TestNestingPastTheBoundIsRefusedWhole covers what a tree past the bound comes
// back as: the one sentence naming the nesting and a program holding nothing.
// Both halves matter — a partial program is one a later phase would lower — and
// both are only true if the depth is measured before anything descends into the
// tree.
//
// The bound is given rather than shipped, so that the table states a depth on
// either side of it without building a tree of four hundred thousand nodes for
// each row.
func TestNestingPastTheBoundIsRefusedWhole(t *testing.T) {
	tests := []struct {
		name  string
		shape string
		depth int
		limit int
	}{
		{name: "one level past", shape: "calls", depth: 40, limit: 39},
		{name: "far past", shape: "calls", depth: 400, limit: 12},
		{name: "one level past, in statements", shape: "ifs", depth: 40, limit: 39},
		{name: "far past, in statements", shape: "ifs", depth: 400, limit: 12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := nestedToDepth(t, tt.shape, tt.depth)
			prog, diags, err := sema.AnalyzeToDepth(context.Background(), f, sema.Shipped{}, tt.limit)
			if err != nil {
				t.Fatalf("Analyze failed: %v", err)
			}
			if len(diags) != 1 {
				t.Fatalf("got %d diagnostics, want the one refusal:\n%s", len(diags), diags)
			}
			if want := refusalWording(tt.limit); diags[0].Msg != want {
				t.Errorf("message = %q, want %q", diags[0].Msg, want)
			}
			if diags[0].Severity != source.Error {
				t.Errorf("severity = %v, want an error, since nothing was checked", diags[0].Severity)
			}
			// The node the walk excludes is the point a reader cuts back to, so
			// the refusal has to arrive there rather than at the file.
			at, _ := ast.DeeperThan(f, tt.limit)
			if diags[0].Pos != at {
				t.Errorf("diagnostic at %v, want %v, the outermost node the bound excludes", diags[0].Pos, at)
			}
			if prog == nil {
				t.Fatal("Analyze returned no program")
			}
			if len(prog.Funcs) != 0 || len(prog.Globals) != 0 || prog.Main != nil {
				t.Errorf("got %d functions and %d globals from a file that was not read, want none",
					len(prog.Funcs), len(prog.Globals))
			}
		})
	}
}

// TestTheBoundIsTheDeepestTreeAnalyzed is the boundary the refusal turns on. A
// bound stated as a maximum and applied one level early refuses a program it
// promises to read, and every row of the table above would still pass.
func TestTheBoundIsTheDeepestTreeAnalyzed(t *testing.T) {
	for _, shape := range nestedShapes {
		t.Run(shape, func(t *testing.T) {
			const depth = 40
			f := nestedToDepth(t, shape, depth)
			_, diags, err := sema.AnalyzeToDepth(context.Background(), f, sema.Shipped{}, depth)
			if err != nil {
				t.Fatalf("Analyze failed: %v", err)
			}
			if diags.HasErrors() {
				t.Errorf("a tree exactly %d levels deep was rejected under a bound of %d:\n%s", depth, depth, diags)
			}
		})
	}
}

// TestTheShippedBoundIsTheOneApplied is what ties the rows above to the
// compiler. Every one of them names its own bound, so a shipped bound that had
// drifted, or that nothing read, would leave the whole table passing.
func TestTheShippedBoundIsTheOneApplied(t *testing.T) {
	f := nestedToDepth(t, "calls", ast.MaxNestingDepth+1)
	_, diags, err := sema.Analyze(context.Background(), f, sema.Shipped{})
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}
	if len(diags) != 1 || diags[0].Msg != refusalWording(ast.MaxNestingDepth) {
		t.Fatalf("got %d diagnostics, want the refusal naming %d:\n%s", len(diags), ast.MaxNestingDepth, diags)
	}
}

// stackProbeEnv names the shape and depth [TestStackProbe] is asked to analyze,
// written "shape:depth". The ceiling the bound sits under is a stack overflow,
// which no process can report about itself, so it is measured from outside one.
const stackProbeEnv = "IC11C_SEMA_STACK_PROBE"

// TestStackProbe analyzes one chain of the named shape and does nothing else. It
// is the body of the subprocess [stackSurvives] runs, and is skipped in a run
// that names nothing.
func TestStackProbe(t *testing.T) {
	asked, named := os.LookupEnv(stackProbeEnv)
	if !named {
		t.Skip(stackProbeEnv + " names no chain, so there is nothing to analyze")
	}
	shape, count, split := strings.Cut(asked, ":")
	if !split {
		t.Fatalf("%s = %q, want a shape and a depth", stackProbeEnv, asked)
	}
	depth, err := strconv.Atoi(count)
	if err != nil {
		t.Fatalf("%s = %q: %v", stackProbeEnv, asked, err)
	}
	// No bound at all, so what is measured is the recursion rather than the
	// refusal that exists to stand in front of it. The shipped bound would answer
	// every depth past itself with the refusal, which is the one result that
	// would mean nothing here.
	f := nestedToDepth(t, shape, depth)
	if _, _, err := sema.AnalyzeToDepth(context.Background(), f, sema.Shipped{}, math.MaxInt); err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}
}

// stackSurvives reports whether analyzing a chain of that depth returns rather
// than exhausting the goroutine stack. It runs in a subprocess, because the
// answer it is looking for ends whatever process asks the question.
func stackSurvives(t *testing.T, shape string, depth int) bool {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestStackProbe$", "-test.timeout=300s")
	cmd.Env = append(os.Environ(), stackProbeEnv+"="+shape+":"+strconv.Itoa(depth))
	out, err := cmd.CombinedOutput()
	switch {
	case err == nil:
		return true
	case strings.Contains(string(out), "stack overflow"):
		return false
	}
	t.Fatalf("analyzing %d nested %s failed for a reason that is not the stack: %v\n%s", depth, shape, err, out)
	return false
}

// TestTheLimitStandsUnderTheStackCeiling is the claim the bound exists to make.
// Analysis descends this tree with a recursion for each construct enclosing
// another, so a tree deep enough takes the goroutine stack with it, and a
// compiler that dies on its input has told the programmer nothing. The bound is
// worth having only if it stands below the depth that would.
func TestTheLimitStandsUnderTheStackCeiling(t *testing.T) {
	for _, shape := range nestedShapes {
		t.Run(shape, func(t *testing.T) {
			if !stackSurvives(t, shape, ast.MaxNestingDepth) {
				t.Errorf("analyzing %d nested %s exhausts the stack, and the bound admits that many",
					ast.MaxNestingDepth, shape)
			}
		})
	}
}

// ceilingDepth is a depth analysis was measured to give out at. A chain of calls
// is read at 800,000 levels and is not at 1,000,000; this is past both, so that
// what it shows is the probe reporting an overflow rather than where the ceiling
// sits, which is a number that moves with the toolchain.
const ceilingDepth = 1_500_000

// TestTheStackCeilingIsWhereTheProbeSaysItIs is what keeps the test above from
// passing on a probe that measures nothing. A subprocess that returned whatever
// it was given would report every depth as survivable, and the bound would rest
// on a measurement nobody made.
func TestTheStackCeilingIsWhereTheProbeSaysItIs(t *testing.T) {
	if stackSurvives(t, "calls", ceilingDepth) {
		t.Errorf("analyzing %d nested calls returned, so the bound of %d is not measured against a ceiling",
			ceilingDepth, ast.MaxNestingDepth)
	}
}
