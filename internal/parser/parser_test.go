package parser_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/parser"
)

// parseClean parses src, which must parse without diagnostics.
func parseClean(t *testing.T, src string) *ast.File {
	t.Helper()
	f, diags := parser.Parse("test.c", src)
	if len(diags) != 0 {
		t.Fatalf("parsing\n%s\nreported:\n%s", src, diags)
	}
	if f == nil {
		t.Fatal("Parse returned a nil file")
	}
	return f
}

// parseBodyStmts parses statements written inside a function and returns them.
func parseBodyStmts(t *testing.T, body string) []ast.Stmt {
	t.Helper()
	f := parseClean(t, "void f(void) {\n"+body+"\n}\n")
	if len(f.Decls) != 1 {
		t.Fatalf("body %q produced %d declarations, want 1", body, len(f.Decls))
	}
	fn, ok := f.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("body %q produced %T, want *ast.FuncDecl", body, f.Decls[0])
	}
	if fn.Body == nil {
		t.Fatalf("body %q produced a prototype, want a definition", body)
	}
	return fn.Body.Stmts
}

// parseOneExpr parses src as a single expression statement and returns the
// expression.
func parseOneExpr(t *testing.T, src string) ast.Expr {
	t.Helper()
	stmts := parseBodyStmts(t, src+";")
	if len(stmts) != 1 {
		t.Fatalf("expression %q produced %d statements, want 1", src, len(stmts))
	}
	es, ok := stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expression %q produced %T, want *ast.ExprStmt", src, stmts[0])
	}
	return es.X
}

func TestExpressionPrecedence(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{name: "multiplication binds tighter than addition", src: "a + b * c", want: "(+ a (* b c))"},
		{name: "multiplication on the left", src: "a * b + c", want: "(+ (* a b) c)"},
		{name: "division and remainder are left associative", src: "a % b / c", want: "(/ (% a b) c)"},
		{name: "subtraction is left associative", src: "a - b - c", want: "(- (- a b) c)"},
		{name: "shift binds looser than addition", src: "a << b + c", want: "(<< a (+ b c))"},
		{name: "relational binds looser than shift", src: "a < b << c", want: "(< a (<< b c))"},
		{name: "equality binds looser than relational", src: "a == b < c", want: "(== a (< b c))"},
		{name: "bitwise and binds looser than equality", src: "a & b == c", want: "(& a (== b c))"},
		{name: "bitwise xor binds looser than and", src: "a ^ b & c", want: "(^ a (& b c))"},
		{name: "bitwise or binds looser than xor", src: "a | b ^ c", want: "(| a (^ b c))"},
		{name: "logical and binds looser than bitwise or", src: "a && b | c", want: "(&& a (| b c))"},
		{name: "logical or binds looser than logical and", src: "a && b || c && d", want: "(|| (&& a b) (&& c d))"},
		{name: "parentheses override precedence", src: "(a + b) * c", want: "(* (+ a b) c)"},
		{name: "nested parentheses", src: "((a))", want: "a"},
		{name: "unary minus binds tighter than subtraction", src: "-a - b", want: "(- (- a) b)"},
		{name: "logical not binds tighter than logical and", src: "!a && b", want: "(&& (! a) b)"},
		{name: "bitwise not binds tighter than xor", src: "~a ^ b", want: "(^ (~ a) b)"},
		{name: "unary plus", src: "+a * b", want: "(* (+ a) b)"},
		{name: "dereference binds tighter than addition", src: "*p + 1", want: "(+ (* p) 1)"},
		{name: "address of an element", src: "&a[0]", want: "(& (index a 0))"},
		{name: "dereference of a stepped pointer", src: "*(p + 1)", want: "(* (+ p 1))"},
		{name: "postfix binds tighter than dereference", src: "*p++", want: "(* (post++ p))"},
		{name: "prefix and postfix increment", src: "a++ + ++b", want: "(+ (post++ a) (pre++ b))"},
		{name: "prefix decrement", src: "--a - b--", want: "(- (pre-- a) (post-- b))"},
		{name: "call then index", src: "f(a, b)[1]", want: "(index (call f a b) 1)"},
		{name: "index then call", src: "a[0](b)", want: "(call (index a 0) b)"},
		{name: "chained subscripts", src: "a[i][j]", want: "(index (index a i) j)"},
		{name: "call with no arguments", src: "f()", want: "(call f)"},
		{name: "nested calls", src: "f(g(x), h())", want: "(call f (call g x) (call h))"},
		{name: "assignment is right associative", src: "a = b = c", want: "(= a (= b c))"},
		{name: "assignment binds looser than addition", src: "a = b + c", want: "(= a (+ b c))"},
		{name: "compound assignment takes the whole right side", src: "a += b * c", want: "(+= a (* b c))"},
		{name: "shift assignment", src: "a >>= b + 1", want: "(>>= a (+ b 1))"},
		{name: "conditional is right associative", src: "a ? b : c ? d : e", want: "(?: a b (?: c d e))"},
		{name: "conditional binds looser than logical or", src: "a || b ? c : d", want: "(?: (|| a b) c d)"},
		{name: "conditional inside an assignment", src: "x = a ? b : c", want: "(= x (?: a b c))"},
		{name: "assignment inside a conditional branch", src: "a ? b = 1 : c", want: "(?: a (= b 1) c)"},
		{name: "cast binds tighter than addition", src: "(long long)x + 1", want: "(+ (cast long long x) 1)"},
		{name: "cast of a parenthesized sum", src: "(long long)(a + b)", want: "(cast long long (+ a b))"},
		{name: "cast to bool", src: "(bool)x", want: "(cast bool x)"},
		{name: "cast of a call", src: "(long long)__ic_load(d0, Temperature)", want: "(cast long long (call __ic_load d0 Temperature))"},
		{name: "character literal", src: "c - '0'", want: "(- c char:48)"},
		{name: "boolean literals", src: "a ? true : false", want: "(?: a true false)"},
		{name: "boolean literal in a comparison", src: "f() == true", want: "(== (call f) true)"},
		{name: "string literal argument", src: `__ic_hash("Prefab")`, want: `(call __ic_hash "Prefab")`},
		{name: "hexadecimal literal", src: "v & 0xff", want: "(& v 255)"},
		{name: "mixed arithmetic and logic", src: "a + b < c && d | e", want: "(&& (< (+ a b) c) (| d e))"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exprShape(t, parseOneExpr(t, tt.src)); got != tt.want {
				t.Errorf("parse(%q) = %s, want %s", tt.src, got, tt.want)
			}
		})
	}
}

// TestAssociativityByNodeType checks the two right-associative forms by walking
// the tree rather than by comparing a rendering, so a defect in the renderer
// cannot mask a defect in the parser.
func TestAssociativityByNodeType(t *testing.T) {
	t.Run("assignment", func(t *testing.T) {
		outer, ok := parseOneExpr(t, "a = b = c").(*ast.AssignExpr)
		if !ok {
			t.Fatal("outer node is not an assignment")
		}
		if lhs, ok := outer.Target.(*ast.Ident); !ok || lhs.Name != "a" {
			t.Errorf("outer target = %#v, want identifier a", outer.Target)
		}
		inner, ok := outer.Value.(*ast.AssignExpr)
		if !ok {
			t.Fatalf("outer value = %T, want a nested assignment", outer.Value)
		}
		if lhs, ok := inner.Target.(*ast.Ident); !ok || lhs.Name != "b" {
			t.Errorf("inner target = %#v, want identifier b", inner.Target)
		}
	})

	t.Run("subtraction", func(t *testing.T) {
		outer, ok := parseOneExpr(t, "a - b - c").(*ast.BinaryExpr)
		if !ok {
			t.Fatal("outer node is not a binary expression")
		}
		if _, ok := outer.X.(*ast.BinaryExpr); !ok {
			t.Errorf("outer left = %T, want the nested subtraction", outer.X)
		}
		if rhs, ok := outer.Y.(*ast.Ident); !ok || rhs.Name != "c" {
			t.Errorf("outer right = %#v, want identifier c", outer.Y)
		}
	})

	t.Run("conditional", func(t *testing.T) {
		outer, ok := parseOneExpr(t, "a ? b : c ? d : e").(*ast.CondExpr)
		if !ok {
			t.Fatal("outer node is not a conditional")
		}
		if _, ok := outer.Then.(*ast.Ident); !ok {
			t.Errorf("outer then = %T, want an identifier", outer.Then)
		}
		if _, ok := outer.Else.(*ast.CondExpr); !ok {
			t.Errorf("outer else = %T, want the nested conditional", outer.Else)
		}
	})
}

func TestExpressionPositions(t *testing.T) {
	e := parseOneExpr(t, "a + b * c")
	bin, ok := e.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("got %T, want *ast.BinaryExpr", e)
	}
	// The body is written on line 2 of the wrapper parseBodyStmts builds.
	if got := bin.Pos(); got.Line != 2 || got.Column != 1 {
		t.Errorf("expression position = %d:%d, want 2:1", got.Line, got.Column)
	}
	if got := bin.OpPos; got.Line != 2 || got.Column != 3 {
		t.Errorf("operator position = %d:%d, want 2:3", got.Line, got.Column)
	}
	if got := bin.Y.Pos(); got.Line != 2 || got.Column != 5 {
		t.Errorf("right operand position = %d:%d, want 2:5", got.Line, got.Column)
	}
}

func TestStatementForms(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{name: "empty statement", src: ";", want: []string{"(empty)"}},
		{name: "expression statement", src: "f(x);", want: []string{"(expr (call f x))"}},
		{name: "nested block", src: "{ a = 1; }", want: []string{"(block (expr (= a 1)))"}},
		{name: "if without else", src: "if (a) b = 1;", want: []string{"(if a (expr (= b 1)))"}},
		{
			name: "if with else",
			src:  "if (a) { b = 1; } else { b = 2; }",
			want: []string{"(if a (block (expr (= b 1))) (block (expr (= b 2))))"},
		},
		{
			name: "else binds to the nearest if",
			src:  "if (a) if (b) x = 1; else x = 2;",
			want: []string{"(if a (if b (expr (= x 1)) (expr (= x 2))))"},
		},
		{
			name: "else if chain",
			src:  "if (a) x = 1; else if (b) x = 2; else x = 3;",
			want: []string{"(if a (expr (= x 1)) (if b (expr (= x 2)) (expr (= x 3))))"},
		},
		{name: "while", src: "while (a < b) a++;", want: []string{"(while (< a b) (expr (post++ a)))"}},
		{name: "while with an empty body", src: "while (f());", want: []string{"(while (call f) (empty))"}},
		{name: "do while", src: "do { a++; } while (a < 3);", want: []string{"(do (block (expr (post++ a))) (< a 3))"}},
		{
			name: "for with a declaration",
			src:  "for (long long i = 0; i < n; i++) s += i;",
			want: []string{"(for (var (i long long 0)) (< i n) (post++ i) (expr (+= s i)))"},
		},
		{
			name: "for with an expression initializer",
			src:  "for (i = 0; i < n; i++) s += i;",
			want: []string{"(for (expr (= i 0)) (< i n) (post++ i) (expr (+= s i)))"},
		},
		{name: "for with every clause omitted", src: "for (;;) break;", want: []string{"(for - - - (break))"}},
		{
			name: "for with only a condition",
			src:  "for (; a < b;) a++;",
			want: []string{"(for - (< a b) - (expr (post++ a)))"},
		},
		{name: "break", src: "while (1) break;", want: []string{"(while 1 (break))"}},
		{name: "continue", src: "while (1) continue;", want: []string{"(while 1 (continue))"}},
		{name: "bare return", src: "return;", want: []string{"(return)"}},
		{name: "return with a value", src: "return a + 1;", want: []string{"(return (+ a 1))"}},
		{
			name: "local declaration",
			src:  "long long x = 1;",
			want: []string{"(var (x long long 1))"},
		},
		{
			name: "local const declaration",
			src:  "const long long x = 1;",
			want: []string{"(constvar (x long long 1))"},
		},
		{
			name: "local array declaration",
			src:  "long long buf[4];",
			want: []string{"(var (buf (array 4 long long)))"},
		},
		{
			name: "initializer list",
			src:  "long long t[3] = {1, 2, 3};",
			want: []string{"(var (t (array 3 long long) (init 1 2 3)))"},
		},
		{
			name: "trailing comma in an initializer list",
			src:  "long long t[2] = {1, 2,};",
			want: []string{"(var (t (array 2 long long) (init 1 2)))"},
		},
		{
			name: "boolean literal initializer",
			src:  "bool ready = true;",
			want: []string{"(var (ready bool true))"},
		},
		{
			name: "switch",
			src:  "switch (s) { case 0: a = 1; break; case 1: break; default: a = 2; }",
			want: []string{
				"(switch s " +
					"(case 0 (expr (= a 1)) (break)) " +
					"(case 1 (break)) " +
					"(default (expr (= a 2))))",
			},
		},
		{
			name: "switch labels stacked on one arm",
			src:  "switch (s) { case 0: case 1: a = 1; break; }",
			want: []string{"(switch s (case 0) (case 1 (expr (= a 1)) (break)))"},
		},
		{
			name: "switch on a named constant",
			src:  "switch (s) { case kIdle: break; }",
			want: []string{"(switch s (case kIdle (break)))"},
		},
		{
			name: "several statements in order",
			src:  "long long x = 1;\nx += 2;\nreturn;",
			want: []string{"(var (x long long 1))", "(expr (+= x 2))", "(return)"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmts := parseBodyStmts(t, tt.src)
			got := stmtShapes(t, stmts)
			if len(got) != len(tt.want) {
				t.Fatalf("parse(%q) produced %d statements, want %d: %v", tt.src, len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("parse(%q) statement %d = %s, want %s", tt.src, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestDeclarationForms(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{name: "global scalar", src: "long long x;", want: []string{"(var (x long long))"}},
		{name: "global with an initializer", src: "long long x = 3;", want: []string{"(var (x long long 3))"}},
		{name: "global const", src: "const long long kWindow = 8;", want: []string{"(constvar (kWindow long long 8))"}},
		{
			name: "global constexpr",
			src:  "constexpr long long kWindow = 8;",
			want: []string{"(constexprvar (kWindow long long 8))"},
		},
		{
			name: "constexpr written before const",
			src:  "constexpr const long long kWindow = 8;",
			want: []string{"(constconstexprvar (kWindow long long 8))"},
		},
		{
			name: "const written before constexpr",
			src:  "const constexpr long long kWindow = 8;",
			want: []string{"(constconstexprvar (kWindow long long 8))"},
		},
		{
			name: "constexpr array",
			src:  "constexpr long long kDecades[4] = {1, 10, 100, 1000};",
			want: []string{"(constexprvar (kDecades (array 4 long long) (init 1 10 100 1000)))"},
		},
		{name: "global bool", src: "bool b;", want: []string{"(var (b bool))"}},
		{name: "global bool initialized to false", src: "bool b = false;", want: []string{"(var (b bool false))"}},
		{name: "global pointer", src: "long long *p;", want: []string{"(var (p (ptr long long)))"}},
		{name: "pointer to pointer", src: "long long **p;", want: []string{"(var (p (ptr (ptr long long))))"}},
		{
			name: "array sized by a constant expression",
			src:  "long long samples[kWindow];",
			want: []string{"(var (samples (array kWindow long long)))"},
		},
		{
			name: "function definition",
			src:  "long long add(long long a, long long b) { return a + b; }",
			want: []string{"(func long long add (param long long a) (param long long b) (block (return (+ a b))))"},
		},
		{
			name: "void function with no parameters",
			src:  "void tick(void) { f(); }",
			want: []string{"(func void tick (block (expr (call f))))"},
		},
		{
			name: "empty parameter list",
			src:  "void tick() { }",
			want: []string{"(func void tick (block))"},
		},
		{
			name: "prototype",
			src:  "long long clampInt(long long v, long long lo, long long hi);",
			want: []string{"(proto long long clampInt (param long long v) (param long long lo) (param long long hi))"},
		},
		{
			name: "prototype with unnamed parameters",
			src:  "long long add(long long, long long);",
			want: []string{"(proto long long add (param long long -) (param long long -))"},
		},
		{
			name: "pointer parameters",
			src:  "void swap(long long *a, long long *b) { }",
			want: []string{"(func void swap (param (ptr long long) a) (param (ptr long long) b) (block))"},
		},
		{
			name: "array parameter decays with no bound",
			src:  "long long sum(long long buf[], long long n) { return n; }",
			want: []string{"(func long long sum (param (array - long long) buf) (param long long n) (block (return n)))"},
		},
		{
			name: "const parameter",
			src:  "long long peek(const long long *p) { return *p; }",
			want: []string{"(func long long peek (constparam (ptr long long) p) (block (return (* p))))"},
		},
		{
			name: "function returning a pointer",
			src:  "long long *first(long long buf[]) { return buf; }",
			want: []string{"(func (ptr long long) first (param (array - long long) buf) (block (return buf)))"},
		},
		{
			name: "declarations in order",
			src:  "long long a;\nvoid f(void) { }\nconst long long b = 1;",
			want: []string{"(var (a long long))", "(func void f (block))", "(constvar (b long long 1))"},
		},
		{name: "global double", src: "double t;", want: []string{"(var (t double))"}},
		{
			name: "global double with a fractional initializer",
			src:  "const double kSetpoint = 293.15;",
			want: []string{"(constvar (kSetpoint double 293.15))"},
		},
		{
			name: "a double literal written with an exponent",
			src:  "const double kTiny = 1e-3;",
			want: []string{"(constvar (kTiny double 0.001))"},
		},
		{name: "array of doubles", src: "double s[4];", want: []string{"(var (s (array 4 double)))"}},
		{
			name: "double parameters and result",
			src:  "double half(double v) { return v / 2.0; }",
			want: []string{"(func double half (param double v) (block (return (/ v 2))))"},
		},
		{
			name: "a device pin named by a dev object",
			src:  "const dev sensor = d0;",
			want: []string{"(constvar (sensor dev d0))"},
		},
		{
			name: "a device pin named by a constexpr dev object",
			src:  "constexpr dev sensor = d0;",
			want: []string{"(constexprvar (sensor dev d0))"},
		},
		{
			name: "a dev parameter",
			src:  "void drive(dev target) { }",
			want: []string{"(func void drive (param dev target) (block))"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := parseClean(t, tt.src)
			if len(f.Decls) != len(tt.want) {
				t.Fatalf("parse(%q) produced %d declarations, want %d", tt.src, len(f.Decls), len(tt.want))
			}
			for i, d := range f.Decls {
				if got := declShape(t, d); got != tt.want[i] {
					t.Errorf("parse(%q) declaration %d = %s, want %s", tt.src, i, got, tt.want[i])
				}
			}
		})
	}
}

func TestDeclarationPositions(t *testing.T) {
	f := parseClean(t, "const long long kWindow = 8;\n\nvoid tick(void) {\n    kWindow;\n}\n")
	if len(f.Decls) != 2 {
		t.Fatalf("got %d declarations, want 2", len(f.Decls))
	}

	varDecl, ok := f.Decls[0].(*ast.VarDecl)
	if !ok {
		t.Fatalf("first declaration is %T, want *ast.VarDecl", f.Decls[0])
	}
	if got := varDecl.Pos(); got.Line != 1 || got.Column != 1 {
		t.Errorf("declaration position = %d:%d, want 1:1", got.Line, got.Column)
	}
	if got := varDecl.NamePos; got.Line != 1 || got.Column != 17 {
		t.Errorf("name position = %d:%d, want 1:17", got.Line, got.Column)
	}

	fn, ok := f.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("second declaration is %T, want *ast.FuncDecl", f.Decls[1])
	}
	if got := fn.Pos(); got.Line != 3 || got.Column != 1 {
		t.Errorf("function position = %d:%d, want 3:1", got.Line, got.Column)
	}
	if got := fn.NamePos; got.Line != 3 || got.Column != 6 {
		t.Errorf("function name position = %d:%d, want 3:6", got.Line, got.Column)
	}
	if got := fn.Body.Stmts[0].Pos(); got.Line != 4 || got.Column != 5 {
		t.Errorf("first body statement position = %d:%d, want 4:5", got.Line, got.Column)
	}
}

func TestExcludedConstructs(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
		line int
		col  int
	}{
		{
			name: "struct",
			src:  "struct S { long long a; };\nlong long x;\n",
			want: "structs are not supported in MicroC",
			line: 1, col: 1,
		},
		{
			name: "union",
			src:  "union U { long long a; };\n",
			want: "unions are not supported in MicroC",
			line: 1, col: 1,
		},
		{
			name: "enum",
			src:  "enum E { A };\n",
			want: "enums are not supported in MicroC",
			line: 1, col: 1,
		},
		{
			name: "float",
			src:  "float x;\n",
			want: "the 'float' type specifier is not supported in MicroC",
			line: 1, col: 1,
		},
		{
			name: "cast to dev",
			src:  "void f(long long n) {\n    (dev)n;\n}\n",
			want: "a cast to dev is not supported in MicroC",
			line: 2, col: 6,
		},
		{
			name: "goto",
			src:  "void f(void) {\n    goto end;\n}\n",
			want: "goto is not supported in MicroC",
			line: 2, col: 5,
		},
		{
			name: "typedef",
			src:  "typedef long long myint;\n",
			want: "typedef is not supported in MicroC",
			line: 1, col: 1,
		},
		{
			name: "sizeof",
			src:  "void f(void) {\n    long long n = sizeof(long long);\n}\n",
			want: "sizeof is not supported in MicroC",
			line: 2, col: 19,
		},
		{
			name: "static",
			src:  "static long long x;\n",
			want: "the 'static' storage class is not supported in MicroC",
			line: 1, col: 1,
		},
		{
			name: "extern",
			src:  "extern long long x;\n",
			want: "the 'extern' storage class is not supported in MicroC",
			line: 1, col: 1,
		},
		{
			name: "unsigned",
			src:  "unsigned long long x;\n",
			want: "the 'unsigned' type specifier is not supported in MicroC",
			line: 1, col: 1,
		},
		{
			name: "long alone",
			src:  "long x;\n",
			want: "MicroC's integer type is 'long long'",
			line: 1, col: 1,
		},
		{
			name: "long long int",
			src:  "long long int x;\n",
			want: "without the trailing 'int'",
			line: 1, col: 11,
		},
		{
			name: "int",
			src:  "int x;\n",
			want: "write 'long long'",
			line: 1, col: 1,
		},
		{
			name: "int in a parameter",
			src:  "void f(int x);\n",
			want: "write 'long long'",
			line: 1, col: 8,
		},
		{
			name: "long alone in a cast",
			src:  "void f(void) {\n    (long)1;\n}\n",
			want: "MicroC's integer type is 'long long'",
			line: 2, col: 6,
		},
		{
			name: "volatile",
			src:  "volatile long long x;\n",
			want: "the 'volatile' qualifier is not supported in MicroC",
			line: 1, col: 1,
		},
		{
			name: "varargs",
			src:  "long long f(long long a, ...);\n",
			want: "variadic parameters are not supported in MicroC",
			line: 1, col: 26,
		},
		{
			name: "function pointer declaration",
			src:  "long long (*fp)(long long);\n",
			want: "function pointers are not supported in MicroC",
			line: 1, col: 11,
		},
		{
			name: "function pointer parameter",
			src:  "void f(long long (*cb)(long long));\n",
			want: "function pointers are not supported in MicroC",
			line: 1, col: 18,
		},
		{
			name: "member access with a dot",
			src:  "void f(void) {\n    x.y = 1;\n}\n",
			want: "member access is not supported in MicroC",
			line: 2, col: 6,
		},
		{
			name: "member access through an arrow",
			src:  "void f(void) {\n    p->y = 1;\n}\n",
			want: "member access is not supported in MicroC",
			line: 2, col: 6,
		},
		{
			name: "preprocessor directive",
			src:  "#include <stdio.h>\nlong long x;\n",
			want: "preprocessor directives are not supported in MicroC",
			line: 1, col: 1,
		},
		{
			name: "comma operator",
			src:  "void f(void) {\n    a = 1, b = 2;\n}\n",
			want: "the comma operator is not supported in MicroC",
			line: 2, col: 10,
		},
		{
			name: "nested function definition",
			src:  "void f(void) {\n    long long g(void) { return 1; }\n}\n",
			want: "nested function definitions are not supported in MicroC",
			line: 2, col: 5,
		},
		{
			name: "octal literal",
			src:  "long long x = 0777;\n",
			want: "octal literals are not supported in MicroC",
			line: 1, col: 15,
		},
		{
			name: "case outside a switch",
			src:  "void f(void) {\n    case 1:\n}\n",
			want: "is only valid inside a switch",
			line: 2, col: 5,
		},
		{
			name: "default outside a switch",
			src:  "void f(void) {\n    default:\n}\n",
			want: "is only valid inside a switch",
			line: 2, col: 5,
		},
		{
			name: "statement before the first case label",
			src:  "void f(void) {\n    switch (s) { a = 1; case 0: break; }\n}\n",
			want: "must follow a case or default label",
			line: 2, col: 18,
		},
		{
			name: "char declaration",
			src:  "char c;\n",
			want: "the 'char' type specifier is not supported in MicroC",
			line: 1, col: 1,
		},
		{
			name: "char parameter",
			src:  "long long f(char c);\n",
			want: "the 'char' type specifier is not supported in MicroC",
			line: 1, col: 13,
		},
		{
			name: "several declarators in one declaration",
			src:  "long long a, b;\n",
			want: "MicroC declares one variable per declaration",
			line: 1, col: 12,
		},
		{
			name: "multi-dimensional array",
			src:  "long long grid[2][3];\n",
			want: "multi-dimensional arrays are not supported in MicroC",
			line: 1, col: 18,
		},
		{
			name: "multi-dimensional array parameter",
			src:  "void f(long long grid[][]) { }\n",
			want: "multi-dimensional arrays are not supported in MicroC",
			line: 1, col: 24,
		},
		{
			name: "nested brace initializer",
			src:  "long long t[2] = {{1, 2}};\n",
			want: "nested brace initializers are not supported in MicroC",
			line: 1, col: 19,
		},
		{
			name: "cast to a pointer",
			src:  "void f(void) {\n    long long a = (long long *)p;\n}\n",
			want: "a cast to a pointer type is not supported in MicroC",
			line: 2, col: 30,
		},
		{
			name: "cast to void",
			src:  "void f(void) {\n    (void)f();\n}\n",
			want: "a cast to void is not supported in MicroC",
			line: 2, col: 6,
		},
		{
			name: "array bound omitted outside a parameter list",
			src:  "long long buf[];\n",
			want: "an array bound is required outside a parameter list",
			line: 1, col: 14,
		},
		{
			name: "struct behind a const",
			src:  "const struct S x;\n",
			want: "structs are not supported in MicroC",
			line: 1, col: 7,
		},
		{
			name: "unsigned behind a const",
			src:  "const unsigned long long x;\n",
			want: "the 'unsigned' type specifier is not supported in MicroC",
			line: 1, col: 7,
		},
		{
			name: "struct behind a const on a parameter",
			src:  "void f(const struct S s);\n",
			want: "structs are not supported in MicroC",
			line: 1, col: 14,
		},
		{
			name: "trailing const on a declaration",
			src:  "long long const x = 1;\n",
			want: "const must precede the type in MicroC",
			line: 1, col: 11,
		},
		{
			name: "trailing const on a pointer",
			src:  "long long *const p;\n",
			want: "const must precede the type in MicroC",
			line: 1, col: 12,
		},
		{
			name: "trailing const on a parameter",
			src:  "void f(long long const x);\n",
			want: "const must precede the type in MicroC",
			line: 1, col: 18,
		},
		{
			name: "trailing constexpr on a declaration",
			src:  "long long constexpr x = 1;\n",
			want: "constexpr must precede the type in MicroC",
			line: 1, col: 11,
		},
		{
			name: "const on a function",
			src:  "const long long f(void) { return 1; }\n",
			want: "const is not valid on a function",
			line: 1, col: 1,
		},
		{
			name: "constexpr on a function",
			src:  "constexpr long long f(void) { return 1; }\n",
			want: "constexpr is not valid on a function",
			line: 1, col: 1,
		},
		{
			name: "constexpr on a parameter",
			src:  "void f(constexpr long long x);\n",
			want: "constexpr is not valid on a parameter",
			line: 1, col: 8,
		},
		{
			name: "trailing comma in an argument list",
			src:  "void f(void) {\n    g(1,);\n}\n",
			want: "a trailing comma is not valid in an argument list",
			line: 2, col: 8,
		},
		{
			name: "trailing comma after several arguments",
			src:  "void f(void) {\n    g(a, b,);\n}\n",
			want: "a trailing comma is not valid in an argument list",
			line: 2, col: 11,
		},
		{
			name: "a lone comma in an argument list",
			src:  "void f(void) {\n    g(,);\n}\n",
			want: "expected an expression, found ','",
			line: 2, col: 7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := parser.Parse("test.c", tt.src)
			if len(diags) != 1 {
				t.Fatalf("parse(%q) reported %d diagnostics, want 1:\n%s", tt.src, len(diags), diags)
			}
			d := diags[0]
			if !strings.Contains(d.Msg, tt.want) {
				t.Errorf("message = %q, want it to contain %q", d.Msg, tt.want)
			}
			if d.Pos.Line != tt.line || d.Pos.Column != tt.col {
				t.Errorf("position = %d:%d, want %d:%d", d.Pos.Line, d.Pos.Column, tt.line, tt.col)
			}
		})
	}
}

func TestSyntaxErrorMessages(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
		line int
		col  int
	}{
		{
			name: "missing semicolon",
			src:  "long long x = 1\nlong long y = 2;\n",
			want: "expected ';', found 'long'",
			line: 1, col: 16,
		},
		{
			name: "missing expression",
			src:  "long long x = ;\n",
			want: "expected an expression, found ';'",
			line: 1, col: 15,
		},
		{
			name: "missing closing paren",
			src:  "void f(void) {\n    if (a { }\n}\n",
			want: "expected ')', found '{'",
			line: 2, col: 11,
		},
		{
			name: "missing declaration name",
			src:  "long long = 1;\n",
			want: "expected an identifier, found '='",
			line: 1, col: 11,
		},
		{
			name: "statement at file scope",
			src:  "x = 1;\n",
			want: "expected a type, found 'x'",
			line: 1, col: 1,
		},
		{
			name: "missing while after do",
			src:  "void f(void) {\n    do { } (a);\n}\n",
			want: "expected 'while', found '('",
			line: 2, col: 12,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := parser.Parse("test.c", tt.src)
			if len(diags) == 0 {
				t.Fatalf("parse(%q) reported no diagnostics", tt.src)
			}
			d := diags[0]
			if !strings.Contains(d.Msg, tt.want) {
				t.Errorf("first message = %q, want it to contain %q", d.Msg, tt.want)
			}
			if d.Pos.Line != tt.line || d.Pos.Column != tt.col {
				t.Errorf("first position = %d:%d, want %d:%d", d.Pos.Line, d.Pos.Column, tt.line, tt.col)
			}
		})
	}
}

// TestMissingSemicolonPointsAtTheEndOfTheExpression pins where a missing
// terminator is blamed. Reporting at the token that follows puts the caret on
// whatever line the programmer wrote next, which can be several lines below the
// mistake.
func TestMissingSemicolonPointsAtTheEndOfTheExpression(t *testing.T) {
	tests := []struct {
		name string
		src  string
		line int
		col  int
	}{
		{
			name: "blank lines separate the mistake from the next statement",
			src:  "void main(void) {\n    long long x = 1\n\n\n    x = 2;\n}\n",
			line: 2, col: 20,
		},
		{
			name: "a return whose terminator is missing",
			src:  "long long f(void) {\n    return 1\n}\n",
			line: 2, col: 13,
		},
		{
			name: "a prototype whose terminator is missing",
			src:  "long long f(void)\nlong long g(void);\n",
			line: 1, col: 18,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := parser.Parse("test.c", tt.src)
			if len(diags) != 1 {
				t.Fatalf("parse(%q) reported %d diagnostics, want 1:\n%s", tt.src, len(diags), diags)
			}
			d := diags[0]
			if !strings.Contains(d.Msg, "expected ';'") {
				t.Errorf("message = %q, want it to name the missing terminator", d.Msg)
			}
			if d.Pos.Line != tt.line || d.Pos.Column != tt.col {
				t.Errorf("position = %d:%d, want %d:%d", d.Pos.Line, d.Pos.Column, tt.line, tt.col)
			}
		})
	}
}

// TestErrorRecovery pins the property that makes the parser usable: several
// independent mistakes are all reported, at their own positions, and the
// declarations around them still parse.
func TestErrorRecovery(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		wantLines []int
		wantDecls []string
	}{
		{
			name: "independent bad initializers at file scope",
			src: "long long a = ;\n" +
				"long long b = 2;\n" +
				"long long c = ;\n" +
				"long long d = 4;\n",
			wantLines: []int{1, 3},
			wantDecls: []string{
				"(var (a long long (badexpr)))",
				"(var (b long long 2))",
				"(var (c long long (badexpr)))",
				"(var (d long long 4))",
			},
		},
		{
			name: "excluded declarations between good ones",
			src: "long long a;\n" +
				"struct S { long long q; };\n" +
				"long long b;\n" +
				"float c;\n" +
				"long long d;\n",
			wantLines: []int{2, 4},
			wantDecls: []string{"(var (a long long))", "(baddecl)", "(var (b long long))", "(baddecl)", "(var (d long long))"},
		},
		{
			name: "several bad statements in one body",
			src: "void f(void) {\n" +
				"    long long a = ;\n" +
				"    b = 2;\n" +
				"    c = ;\n" +
				"    d = 4;\n" +
				"}\n",
			wantLines: []int{2, 4},
			wantDecls: []string{
				"(func void f (block " +
					"(var (a long long (badexpr))) (expr (= b 2)) (expr (= c (badexpr))) (expr (= d 4))))",
			},
		},
		{
			name: "a broken function body does not swallow the next function",
			src: "void f(void) {\n" +
				"    goto end;\n" +
				"}\n" +
				"long long g(void) {\n" +
				"    return 1;\n" +
				"}\n",
			wantLines: []int{2},
			wantDecls: []string{
				"(func void f (block (badstmt)))",
				"(func long long g (block (return 1)))",
			},
		},
		{
			name: "a mistake in each of three functions",
			src: "long long f(void) { return 1 + ; }\n" +
				"long long g(void) { return 1 }\n" +
				"long long h(void) { x.y; }\n",
			wantLines: []int{1, 2, 3},
			wantDecls: []string{
				"(func long long f (block (return (+ 1 (badexpr)))))",
				"(func long long g (block (return 1)))",
				"(func long long h (block (expr (badexpr))))",
			},
		},
		{
			name: "excluded keyword recovers onto the type that follows",
			src: "static long long a;\n" +
				"unsigned long long b;\n" +
				"long long c;\n",
			wantLines: []int{1, 2},
			wantDecls: []string{"(baddecl)", "(var (a long long))", "(baddecl)", "(var (b long long))", "(var (c long long))"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, diags := parser.Parse("test.c", tt.src)
			gotLines := make([]int, len(diags))
			for i, d := range diags {
				gotLines[i] = d.Pos.Line
			}
			if !equalInts(gotLines, tt.wantLines) {
				t.Errorf("diagnostic lines = %v, want %v:\n%s", gotLines, tt.wantLines, diags)
			}

			gotDecls := make([]string, len(f.Decls))
			for i, d := range f.Decls {
				gotDecls[i] = declShape(t, d)
			}
			if len(gotDecls) != len(tt.wantDecls) {
				t.Fatalf("got %d declarations, want %d: %v", len(gotDecls), len(tt.wantDecls), gotDecls)
			}
			for i := range gotDecls {
				if gotDecls[i] != tt.wantDecls[i] {
					t.Errorf("declaration %d = %s, want %s", i, gotDecls[i], tt.wantDecls[i])
				}
			}
		})
	}
}

func equalInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestDiagnosticsAreOrderedByPosition(t *testing.T) {
	src := "long long a = @;\nstruct S { long long q; };\nlong long b = ;\n"
	_, diags := parser.Parse("test.c", src)
	if len(diags) < 3 {
		t.Fatalf("got %d diagnostics, want at least 3:\n%s", len(diags), diags)
	}
	for i := 1; i < len(diags); i++ {
		if diags[i-1].Pos.Offset > diags[i].Pos.Offset {
			t.Errorf("diagnostics are out of order at index %d:\n%s", i, diags)
		}
	}
}

func TestDiagnosticCountIsBounded(t *testing.T) {
	src := strings.Repeat("long long a = ;\n", 500)
	f, diags := parser.Parse("test.c", src)
	if len(diags) == 0 {
		t.Fatal("got no diagnostics")
	}
	if len(diags) > 65 {
		t.Errorf("got %d diagnostics, want the list capped near 64", len(diags))
	}
	if last := diags[len(diags)-1]; !strings.Contains(last.Msg, "too many errors") {
		t.Errorf("last message = %q, want it to note truncation", last.Msg)
	}
	if len(f.Decls) != 500 {
		t.Errorf("got %d declarations, want 500 even past the diagnostic cap", len(f.Decls))
	}
}

// TestTruncationMarkerClosesTheReport pins two things about the cap. The marker
// is appended after the list is sorted, so it closes the report rather than
// sorting into the middle of it, and the lexical diagnostics the list already
// holds when parsing starts do not consume the parser's own budget.
func TestTruncationMarkerClosesTheReport(t *testing.T) {
	const (
		badDecls    = 70
		badChars    = 5
		wantParsing = 64
	)
	src := strings.Repeat("long long a = ;\n", badDecls) + strings.Repeat("@\n", badChars)
	_, diags := parser.Parse("test.c", src)

	if len(diags) == 0 {
		t.Fatal("got no diagnostics")
	}
	if last := diags[len(diags)-1]; !strings.Contains(last.Msg, "too many errors") {
		t.Fatalf("last diagnostic = %q, want the truncation marker:\n%s", last.Msg, diags)
	}
	for i := 1; i < len(diags); i++ {
		if diags[i-1].Pos.Compare(diags[i].Pos) > 0 {
			t.Errorf("diagnostics are out of order at index %d:\n%s", i, diags)
		}
	}

	var lexical, parsing int
	for _, d := range diags[:len(diags)-1] {
		if strings.Contains(d.Msg, "unexpected character") {
			lexical++
		} else {
			parsing++
		}
	}
	if lexical != badChars {
		t.Errorf("got %d lexical diagnostics, want %d:\n%s", lexical, badChars, diags)
	}
	if parsing != wantParsing {
		t.Errorf("got %d parser diagnostics, want the full budget of %d:\n%s", parsing, wantParsing, diags)
	}
}

func TestParseTerminatesOnTruncatedInput(t *testing.T) {
	tests := []string{
		"long long f(",
		"long long f(void) {",
		"void f(void) { if (",
		"void f(void) { a[",
		"void f(void) { switch (x) { case",
		"long long a = {1,",
		"/* unterminated",
		"'",
		`"`,
		"long long x = 1 ? 2",
		"const",
		"long long const",
		"void f(void) { g(1,",
		"void f(const",
	}
	for _, src := range tests {
		t.Run(src, func(t *testing.T) {
			f, diags := parser.Parse("test.c", src)
			if f == nil {
				t.Fatal("Parse returned a nil file")
			}
			if len(diags) == 0 {
				t.Errorf("parse(%q) reported no diagnostics", src)
			}
		})
	}
}

func TestTestdataParsesCleanly(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "*.c"))
	if err != nil {
		t.Fatalf("globbing testdata: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no testdata programs found")
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			f, diags := parser.Parse(path, string(src))
			if len(diags) != 0 {
				t.Fatalf("%s did not parse cleanly:\n%s", path, diags)
			}
			if len(f.Decls) == 0 {
				t.Fatalf("%s produced no declarations", path)
			}
			// declShape walks the whole tree and asserts that every node it
			// visits carries a valid position.
			for _, d := range f.Decls {
				declShape(t, d)
			}
		})
	}
}

func TestParseReportsLexicalAndSyntacticErrorsTogether(t *testing.T) {
	f, diags := parser.Parse("test.c", "long long a = 0x;\nlong long b = ;\n")
	if len(diags) != 2 {
		t.Fatalf("got %d diagnostics, want 2:\n%s", len(diags), diags)
	}
	if !strings.Contains(diags[0].Msg, "hexadecimal literal has no digits") {
		t.Errorf("first message = %q, want the lexical error", diags[0].Msg)
	}
	if !strings.Contains(diags[1].Msg, "expected an expression") {
		t.Errorf("second message = %q, want the syntactic error", diags[1].Msg)
	}
	if got := diags.Err(); got == nil {
		t.Error("Err() = nil, want an error")
	}
	if len(f.Decls) != 2 {
		t.Errorf("got %d declarations, want 2", len(f.Decls))
	}
}
