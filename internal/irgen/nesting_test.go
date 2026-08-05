package irgen

import (
	"os"
	"os/exec"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/corpus"
	"github.com/greg2010/ic11c/internal/sema"
	"github.com/greg2010/ic11c/internal/source"
	"github.com/greg2010/ic11c/internal/tsparse"
)

// These tests build trees rather than parse sources, because analysis bounds
// each declaration on its own and what they cover is the composed walk.

func somewhere() source.Position {
	return source.Position{File: "test.c", Offset: 0, Line: 1, Column: 1}
}

func ident(name string) *ast.Ident { return &ast.Ident{NamePos: somewhere(), Name: name} }

func intType() ast.Type { return &ast.ScalarType{TypePos: somewhere(), Kind: ast.Int} }

func one() ast.Expr { return &ast.IntLit{ValuePos: somewhere(), Value: 1} }

func globalG() ast.Decl {
	return &ast.VarDecl{DeclPos: somewhere(), Type: intType(), Name: "g", NamePos: somewhere()}
}

func assignG(value ast.Expr) ast.Stmt {
	return &ast.ExprStmt{X: &ast.AssignExpr{
		OpPos: somewhere(), Op: ast.Assign, Target: ident("g"), Value: value,
	}}
}

func mainDecl(body ...ast.Stmt) ast.Decl {
	return &ast.FuncDecl{
		DeclPos: somewhere(), Result: &ast.ScalarType{TypePos: somewhere(), Kind: ast.Void},
		Name: "main", NamePos: somewhere(),
		Body: &ast.BlockStmt{Lbrace: somewhere(), Stmts: body, Rbrace: somewhere()},
	}
}

// wrapNots writes n prefix '!' operators over x.
func wrapNots(n int, x ast.Expr) ast.Expr {
	for range n {
		x = &ast.UnaryExpr{OpPos: somewhere(), Op: ast.LogicalNot, X: x}
	}
	return x
}

// wrapIfs wraps s in n unbraced if statements.
func wrapIfs(n int, s ast.Stmt) ast.Stmt {
	for range n {
		s = &ast.IfStmt{IfPos: somewhere(), Cond: ident("g"), Then: s}
	}
	return s
}

// nestedShapes is every chain [singleFile] writes: the constructs lowering
// spends the most stack on per level, which is what makes the dearest of them a
// measurement of the ceiling rather than of one arm.
var nestedShapes = []string{
	"nots", "ifs", "blocks", "whiles", "ands", "andvalues", "conds", "adds", "assigns", "casts",
}

// singleFile writes a program nesting depth constructs of the named shape inside
// main, and nothing else.
func singleFile(shape string, depth int) *ast.File {
	var body ast.Stmt
	switch shape {
	case "nots":
		body = assignG(wrapNots(depth, ident("g")))
	case "ifs":
		body = wrapIfs(depth, assignG(one()))
	case "blocks":
		s := assignG(one())
		for range depth {
			s = &ast.BlockStmt{Lbrace: somewhere(), Stmts: []ast.Stmt{s}, Rbrace: somewhere()}
		}
		body = s
	case "whiles":
		s := assignG(one())
		for range depth {
			s = &ast.WhileStmt{WhilePos: somewhere(), Cond: ident("g"), Body: s}
		}
		body = s
	case "ands":
		body = &ast.IfStmt{IfPos: somewhere(), Cond: wrapAnds(depth), Then: assignG(one())}
	case "andvalues":
		body = assignG(wrapAnds(depth))
	case "conds":
		var x ast.Expr = ident("g")
		for range depth {
			x = &ast.CondExpr{Question: somewhere(), Cond: ident("g"), Then: ident("g"), Else: x}
		}
		body = assignG(x)
	case "adds":
		var x ast.Expr = ident("g")
		for range depth {
			x = &ast.BinaryExpr{OpPos: somewhere(), Op: ast.Add, X: ident("g"), Y: x}
		}
		body = assignG(x)
	case "assigns":
		var x ast.Expr = ident("g")
		for range depth {
			x = &ast.AssignExpr{OpPos: somewhere(), Op: ast.Assign, Target: ident("g"), Value: x}
		}
		body = &ast.ExprStmt{X: x}
	case "casts":
		var x ast.Expr = ident("g")
		for range depth {
			x = &ast.CastExpr{Lparen: somewhere(), Type: intType(), X: x}
		}
		body = assignG(x)
	default:
		panic("unknown shape " + shape)
	}
	return &ast.File{Name: "test.c", Start: somewhere(), Decls: []ast.Decl{globalG(), mainDecl(body)}}
}

// wrapAnds nests n '&&' operators in the right operand, which is the side the
// short-circuit lowering recurses down.
func wrapAnds(n int) ast.Expr {
	var x ast.Expr = ident("g")
	for range n {
		x = &ast.BinaryExpr{OpPos: somewhere(), Op: ast.LogicalAnd, X: ident("g"), Y: x}
	}
	return x
}

// chainFile writes funcs functions each nesting depth constructs of that shape
// around a call to the next, with main calling the first. Every call is inlined,
// so lowering spends depth times funcs at once, while each declaration on its own
// is only as deep as one chain and analysis accepts the file whole.
func chainFile(shape string, depth, funcs int) *ast.File {
	// Declared innermost first, since MicroC has no forward declaration.
	decls := []ast.Decl{globalG()}
	for i := funcs; i >= 1; i-- {
		var inner ast.Expr = ident("x")
		if i < funcs {
			inner = &ast.CallExpr{
				Lparen: somewhere(), Fun: ident("f" + strconv.Itoa(i+1)),
				Args: []ast.Expr{ident("x")},
			}
		}
		decls = append(decls, chainFunc("f"+strconv.Itoa(i), shape, depth, inner))
	}
	call := &ast.CallExpr{Lparen: somewhere(), Fun: ident("f1"), Args: []ast.Expr{ident("g")}}
	return &ast.File{Name: "test.c", Start: somewhere(), Decls: append(decls, mainDecl(assignG(call)))}
}

func chainFunc(name, shape string, depth int, result ast.Expr) ast.Decl {
	var body ast.Stmt
	switch shape {
	case "nots":
		body = &ast.ReturnStmt{ReturnPos: somewhere(), Result: wrapNots(depth, result)}
	default:
		body = wrapIfs(depth, &ast.ReturnStmt{ReturnPos: somewhere(), Result: result})
	}
	return &ast.FuncDecl{
		DeclPos: somewhere(), Result: intType(), Name: name, NamePos: somewhere(),
		Params: []*ast.Param{{ParamPos: somewhere(), Type: intType(), Name: "x", NamePos: somewhere()}},
		Body: &ast.BlockStmt{Lbrace: somewhere(), Stmts: []ast.Stmt{
			body,
			&ast.ReturnStmt{ReturnPos: somewhere(), Result: ident("x")},
		}, Rbrace: somewhere()},
	}
}

// analyzed checks f, failing the test for a program these builders were supposed
// to have written correctly. It also establishes the premise every case below
// rests on: analysis accepts the file whole.
func analyzed(t *testing.T, f *ast.File) *sema.Program {
	t.Helper()
	prog, diags, err := sema.Analyze(t.Context(), f, sema.Shipped{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if diags.HasErrors() {
		t.Fatalf("analysis rejected the program:\n%s", diags)
	}
	return prog
}

// maxProbeBudget is where the searches below give up. Each one opens by
// doubling, and a doubling with no answer to reach is what the bounds under test
// exist to stop being written by hand.
const maxProbeBudget = 1 << 20

// descentOf reports the descent lowering that program spends, found as the
// smallest budget it is not refused under.
func descentOf(t *testing.T, f *ast.File) int {
	t.Helper()
	prog := analyzed(t, f)
	admits := func(budget int) bool {
		result, err := generateWithin(t.Context(), prog, Options{}, budgets{descent: budget, lowered: maxLowered})
		if result != nil {
			result.Dispose()
		}
		return err == nil
	}
	low, high := 0, 1
	for !admits(high) {
		low, high = high, high*2
		if high > maxProbeBudget {
			t.Fatalf("no descent budget under %d lowers the program", maxProbeBudget)
		}
	}
	for low+1 < high {
		mid := low + (high-low)/2
		if admits(mid) {
			high = mid
		} else {
			low = mid
		}
	}
	return high
}

// TestEveryLevelOfDescentIsCharged is what the budget rests on: a construct
// lowered through a recursion that charges nothing is one a chain of which walks
// straight past it. Each row states its own step rather than a total, since a
// shape whose charge stops growing leaves every total in the table plausible.
func TestEveryLevelOfDescentIsCharged(t *testing.T) {
	for _, shape := range nestedShapes {
		t.Run(shape, func(t *testing.T) {
			base := descentOf(t, singleFile(shape, 1))
			for _, n := range []int{2, 4, 16} {
				got := descentOf(t, singleFile(shape, n))
				if want := base + (n - 1); got < want {
					t.Errorf("%d of them charge %d levels, want at least %d: one charges %d, "+
						"and a level charging nothing is a level the budget does not see",
						n, got, want, base)
				}
			}
		})
	}
}

// TestInliningChargesTheCallersDescent is the composition no bound on one
// declaration reaches. A callee's body is generated from inside the caller's own
// recursion, so a chain spends the levels of every function at once, and analysis
// has already accepted each of them separately.
func TestInliningChargesTheCallersDescent(t *testing.T) {
	const depth = 64
	for _, shape := range []string{"nots", "ifs"} {
		t.Run(shape, func(t *testing.T) {
			previous := descentOf(t, chainFile(shape, depth, 1))
			for _, funcs := range []int{2, 4, 8} {
				got := descentOf(t, chainFile(shape, depth, funcs))
				if got-previous < depth {
					t.Errorf("a chain of %d functions charges %d levels against %d for one fewer, "+
						"which is under the %d the added function nests: an expansion is not "+
						"charging the descent it is generated from",
						funcs, got, previous, depth)
				}
				previous = got
			}
		})
	}
}

// TestTheDescentRefusalIsADiagnostic covers what a program past the budget comes
// back as. Dying on the stack is what the budget exists to replace, so the one
// thing that must not happen is a refusal that is not a report.
func TestTheDescentRefusalIsADiagnostic(t *testing.T) {
	const budget = 40
	prog := analyzed(t, singleFile("ifs", 4*budget))
	result, err := generateWithin(t.Context(), prog, Options{}, budgets{descent: budget, lowered: maxLowered})
	if err == nil {
		result.Dispose()
		t.Fatal("lowering accepted a tree past its descent budget")
	}
	var diags source.DiagnosticList
	if !asDiagnostics(err, &diags) {
		t.Fatalf("error is %T, want a source.DiagnosticList: %v", err, err)
	}
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want the one refusal:\n%s", len(diags), diags)
	}
	if !strings.Contains(diags[0].Msg, strconv.Itoa(budget)) {
		t.Errorf("the refusal does not name the budget of %d: %s", budget, diags[0].Msg)
	}
	if !diags[0].Pos.IsValid() {
		t.Error("the refusal carries no source position")
	}
}

// TestTheShippedBudgetIsTheOneApplied ties the cases above to the compiler:
// every one of them names its own budget, so a shipped budget that had drifted
// would leave them all passing. The chain is four functions, so what reaches the
// budget is the composition and each declaration nests well inside analysis.
func TestTheShippedBudgetIsTheOneApplied(t *testing.T) {
	prog := analyzed(t, chainFile("ifs", maxDescent/2, 4))
	result, err := Generate(t.Context(), prog, Options{})
	if err == nil {
		result.Dispose()
		t.Fatal("lowering accepted a chain past the shipped descent budget")
	}
	if !strings.Contains(err.Error(), strconv.Itoa(maxDescent)) {
		t.Errorf("the refusal does not name the shipped budget of %d: %v", maxDescent, err)
	}
}

// defaultMaxStack is the goroutine stack Go refuses to grow past when nothing
// calls [debug.SetMaxStack]. It is what "goroutine stack exceeds
// 1000000000-byte limit" names, and there is no process left to report it.
const defaultMaxStack = 1_000_000_000

// usableStack is the largest stack that limit admits. A stack is doubled rather
// than grown to what was asked, so the last size served under the limit is the
// largest power of two below it, and the budget has to stand under that rather
// than under the limit itself.
const usableStack = 1 << 29

// stackMargin is how far under [usableStack] the deepest descent the budget
// admits has to sit. Two doublings, because what a level costs moves with the
// frame layout and with the toolchain, and a budget placed just below what was
// measured is a stack overflow after the next change to either.
const stackMargin = 4

// probeStack is the stack the measurement runs under. It is small enough that
// the depths it finds are far short of [maxDescent], so what bounds the probe is
// the stack rather than the budget being measured against it.
const probeStack = 4 << 20

// TestTheDescentBudgetStandsUnderTheStackCeiling is the claim the budget exists
// to make: a descent deep enough takes the goroutine stack with it, and a
// compiler that dies on its input has told the programmer nothing. Every level
// charges at least once, so the dearest measured bounds what [maxDescent] costs.
func TestTheDescentBudgetStandsUnderTheStackCeiling(t *testing.T) {
	dearest, shape := 0, ""
	for _, s := range nestedShapes {
		levels := deepestUnder(t, s, probeStack)
		if levels == 0 {
			t.Fatalf("no depth of %s lowers within %d bytes of stack", s, probeStack)
		}
		if cost := probeStack / levels; cost > dearest {
			dearest, shape = cost, s
		}
	}
	spent := maxDescent * dearest
	t.Logf("the dearest level measured is %s at %d bytes, so %d levels spend %d MB",
		shape, dearest, maxDescent, spent>>20)
	if spent*stackMargin > usableStack {
		t.Errorf("a descent of %d levels of %s spends %d bytes, which is inside %d times the %d "+
			"a %d-byte limit serves: the budget is not standing under the ceiling",
			maxDescent, shape, spent, stackMargin, usableStack, defaultMaxStack)
	}
}

// stackProbeEnv names the program [TestStackProbe] is asked to lower and the
// stack it is capped to, written "shape:depth:maxstack". The ceiling the budget
// stands under is a stack overflow, which no process can report about itself, so
// it is measured from outside one.
const stackProbeEnv = "IC11C_IRGEN_STACK_PROBE"

// TestStackProbe lowers one nested program and does nothing else. It is the body
// of the subprocess [lowers] runs, and is skipped in a run that names nothing.
func TestStackProbe(t *testing.T) {
	asked, named := os.LookupEnv(stackProbeEnv)
	if !named {
		t.Skip(stackProbeEnv + " names no program, so there is nothing to lower")
	}
	fields := strings.Split(asked, ":")
	if len(fields) != 3 {
		t.Fatalf("%s = %q, want shape:depth:maxstack", stackProbeEnv, asked)
	}
	depth, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatalf("%s = %q: %v", stackProbeEnv, asked, err)
	}
	maxStack, err := strconv.Atoi(fields[2])
	if err != nil {
		t.Fatalf("%s = %q: %v", stackProbeEnv, asked, err)
	}
	prog := analyzed(t, singleFile(fields[0], depth))

	// The cap is set after analysis and lowering runs on a goroutine of its own,
	// so that what it measures is this stage from a stack analysis had not
	// already grown.
	debug.SetMaxStack(maxStack)
	done := make(chan error, 1)
	go func() {
		result, err := Generate(t.Context(), prog, Options{})
		if result != nil {
			result.Dispose()
		}
		done <- err
	}()
	// A refusal would return the same way a program that fits does, and the
	// search outside reads returning as fitting.
	if err := <-done; err != nil {
		t.Fatalf("lowering %d nested %s: %v", depth, fields[0], err)
	}
}

// lowers reports whether lowering that program returns rather than exhausting
// the stack the probe was capped to.
func lowers(t *testing.T, shape string, depth, maxStack int) bool {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestStackProbe$", "-test.timeout=300s")
	cmd.Env = append(os.Environ(), stackProbeEnv+"="+strings.Join([]string{
		shape, strconv.Itoa(depth), strconv.Itoa(maxStack),
	}, ":"))
	out, err := cmd.CombinedOutput()
	switch {
	case err == nil:
		return true
	case strings.Contains(string(out), "stack overflow"):
		return false
	}
	t.Fatalf("lowering %d nested %s failed for a reason that is not the stack: %v\n%s",
		depth, shape, err, out)
	return false
}

// deepestUnder reports the deepest nesting of that shape lowering completes
// within maxStack bytes of goroutine stack. The search opens by doubling, since
// stating where the answer is would need the cost of a level, which is what the
// caller is measuring.
func deepestUnder(t *testing.T, shape string, maxStack int) int {
	t.Helper()
	low, high := 0, 1
	for lowers(t, shape, high, maxStack) {
		low, high = high, high*2
		if high > maxProbeBudget {
			t.Fatalf("no nesting of %s under %d exhausts %d bytes of stack", shape, maxProbeBudget, maxStack)
		}
	}
	for low+1 < high {
		mid := low + (high-low)/2
		if lowers(t, shape, mid, maxStack) {
			low = mid
		} else {
			high = mid
		}
	}
	return low
}

// fanoutFile writes funcs functions each calling the next twice, with main
// calling the first. Every call is inlined, so the innermost body is expanded
// 2^(funcs-1) times out of a source linear in funcs, and analysis accepts the
// file whole: nothing about the tree says how much this lowers to.
func fanoutFile(funcs int) *ast.File {
	// Declared innermost first, since MicroC has no forward declaration.
	decls := []ast.Decl{globalG()}
	for i := funcs; i >= 1; i-- {
		var body ast.Expr = &ast.BinaryExpr{OpPos: somewhere(), Op: ast.Add, X: ident("x"), Y: one()}
		if i < funcs {
			next := "f" + strconv.Itoa(i+1)
			body = &ast.BinaryExpr{OpPos: somewhere(), Op: ast.Add,
				X: &ast.CallExpr{Lparen: somewhere(), Fun: ident(next), Args: []ast.Expr{ident("x")}},
				Y: &ast.CallExpr{Lparen: somewhere(), Fun: ident(next), Args: []ast.Expr{
					&ast.BinaryExpr{OpPos: somewhere(), Op: ast.Add, X: ident("x"), Y: one()},
				}},
			}
		}
		decls = append(decls, &ast.FuncDecl{
			DeclPos: somewhere(), Result: intType(), Name: "f" + strconv.Itoa(i), NamePos: somewhere(),
			Params: []*ast.Param{{ParamPos: somewhere(), Type: intType(), Name: "x", NamePos: somewhere()}},
			Body: &ast.BlockStmt{Lbrace: somewhere(), Stmts: []ast.Stmt{
				&ast.ReturnStmt{ReturnPos: somewhere(), Result: body},
			}, Rbrace: somewhere()},
		})
	}
	call := &ast.CallExpr{Lparen: somewhere(), Fun: ident("f1"), Args: []ast.Expr{ident("g")}}
	return &ast.File{Name: "test.c", Start: somewhere(), Decls: append(decls, mainDecl(assignG(call)))}
}

// loweredOf reports the levels lowering that program spends in all, found as the
// smallest total budget it is not refused under.
func loweredOf(t *testing.T, f *ast.File) int {
	t.Helper()
	prog := analyzed(t, f)
	admits := func(budget int) bool {
		result, err := generateWithin(t.Context(), prog, Options{}, budgets{descent: maxDescent, lowered: budget})
		if result != nil {
			result.Dispose()
		}
		return err == nil
	}
	low, high := 0, 1
	for !admits(high) {
		low, high = high, high*2
		if high > maxProbeBudget {
			t.Fatalf("no lowering budget under %d lowers the program", maxProbeBudget)
		}
	}
	for low+1 < high {
		mid := low + (high-low)/2
		if admits(mid) {
			high = mid
		} else {
			low = mid
		}
	}
	return high
}

// growthPerFunction is the factor one more function in the chain must multiply
// the lowering by for the case below to have seen the composition rather than
// the source growing. Splicing a body in twice doubles it; a source read once
// per construct would add a couple of levels and multiply it by about one.
const growthPerFunction = 1.5

// TestInliningChargesEveryExpansion is the composition that depth does not see.
// A chain each calling the next twice is linear in the source and exponential in
// what it lowers to, and the module has to be finished before anything
// downstream can refuse its size.
func TestInliningChargesEveryExpansion(t *testing.T) {
	previous := loweredOf(t, fanoutFile(2))
	for _, funcs := range []int{3, 4, 5, 6, 7} {
		got := loweredOf(t, fanoutFile(funcs))
		if want := int(float64(previous) * growthPerFunction); got < want {
			t.Errorf("a chain of %d functions lowers %d levels against %d for one fewer, which is under the "+
				"%v times one more function splices in: an expansion is not being charged",
				funcs, got, previous, growthPerFunction)
		}
		previous = got
	}
}

// TestTheShippedLoweringBudgetIsTheOneApplied ties the case above to the
// compiler, the way [TestTheShippedBudgetIsTheOneApplied] does for the depth.
// The source is a couple of dozen lines and every declaration in it is shallow,
// so what reaches the budget is the expansion and nothing else.
func TestTheShippedLoweringBudgetIsTheOneApplied(t *testing.T) {
	prog := analyzed(t, fanoutFile(maxInlineDepth))
	result, err := Generate(t.Context(), prog, Options{})
	if err == nil {
		result.Dispose()
		t.Fatal("lowering accepted a program whose expansion is exponential in its source")
	}
	if !strings.Contains(err.Error(), strconv.Itoa(maxLowered)) {
		t.Errorf("the refusal does not name the shipped budget of %d: %v", maxLowered, err)
	}
}

// TestTheCorpusIsFarUnderTheLoweringBudget recomputes the headroom [maxLowered]
// leaves over real programs, so a budget drifting down toward the programs the
// compiler exists to accept is caught here rather than by one refused in the
// field.
func TestTheCorpusIsFarUnderTheLoweringBudget(t *testing.T) {
	// The budget is set by what building a module costs rather than by what a
	// program is expected to reach, so it is owed two orders of magnitude over
	// the largest real one: a budget close enough to a whole program for this to
	// bind was derived from the wrong thing.
	const minHeadroom = 100

	programs, err := corpus.Programs()
	if err != nil {
		t.Fatalf("loading the corpus: %v", err)
	}

	most, mostName := 0, ""
	for _, program := range programs {
		file, diags, err := tsparse.Parse(program.Name, program.Source)
		if err != nil {
			t.Fatalf("parsing %s: %v", program.Name, err)
		}
		if diags.HasErrors() {
			t.Fatalf("parsing %s:\n%s", program.Name, diags)
		}
		if n := loweredOf(t, file); n > most {
			most, mostName = n, program.Name
		}
	}
	t.Logf("the heaviest of the %d fixtures is %s at %d levels, %d times under the budget of %d",
		len(programs), mostName, most, maxLowered/most, maxLowered)
	if most*minHeadroom > maxLowered {
		t.Errorf("%s lowers %d levels and the budget is %d, which is under the %d times headroom a real program is owed",
			mostName, most, maxLowered, minHeadroom)
	}
}
