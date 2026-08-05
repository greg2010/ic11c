package tsparse_test

import (
	"slices"
	"testing"

	"github.com/greg2010/ic11c/internal/tsparse"
)

// A token internal/lexer scans whole and the C grammar scans as several is
// the one mistake no check over the nodes can see: the tree that comes back
// is well formed and the pieces read as ordinary operators. So each program
// below is held to the sentence it draws, its position, and the tree it leaves.

// relexCase is one program the C grammar's lexer scans differently from
// internal/lexer.
type relexCase struct {
	name string
	src  string
	// want is what the front end reports, in full and in order, and shape is
	// the tree it leaves. An empty want means the source holds no token the
	// grammar split.
	want  []string
	shape string
}

var relexCases = []relexCase{
	{
		name:  "'&&' behind an operator",
		src:   "bool f(bool a, bool b) { return a || && !a; }\n",
		want:  []string{"test.c:1:38: '&&' is not expected here"},
		shape: "(func bool f (param bool a) (param bool b) (block (return (|| a (badexpr)))))",
	},
	{
		name:  "'&&' where an expression begins",
		src:   "bool f(bool a) { return && a; }\n",
		want:  []string{"test.c:1:25: '&&' is not expected here"},
		shape: "(func bool f (param bool a) (block (return (badexpr))))",
	},
	{
		name:  "'&&' where a statement begins",
		src:   "void f(bool a, bool b) { a = b; && !a; }\n",
		want:  []string{"test.c:1:33: '&&' is not expected here"},
		shape: "(func void f (param bool a) (param bool b) (block (expr (= a b)) (expr (badexpr))))",
	},
	{
		name:  "'&&' where an initializer begins",
		src:   "long long a = && 1;\n",
		want:  []string{"test.c:1:15: '&&' is not expected here"},
		shape: "(var (a long long (badexpr)))",
	},
	{
		name:  "'&&' behind the operator its first half spells",
		src:   "long long f(long long x) { return x & && x; }\n",
		want:  []string{"test.c:1:39: '&&' is not expected here"},
		shape: "(func long long f (param long long x) (block (return (& x (badexpr)))))",
	},
	{
		name:  "'&&' inside a subscript",
		src:   "void f(long long x) { x[&& 1] = 2; }\n",
		want:  []string{"test.c:1:25: '&&' is not expected here"},
		shape: "(func void f (param long long x) (block (expr (= (index x (badexpr)) 2))))",
	},
	{
		name:  "'::' outside an attribute",
		src:   "long long a = 1;\nlong long b = a::a;\n",
		want:  []string{"test.c:2:16: '::' is not expected here"},
		shape: "(var (a long long 1))\n(baddecl)",
	},
	// The grammar spelling one lexeme over several of the lexer's tokens is
	// the other direction and fabricates nothing: the '[' pair an attribute
	// opens with is two tokens to internal/lexer and one to the grammar.
	{
		name:  "the bracket pair an attribute opens with",
		src:   "[[ic11c::prefab(\"StructureGasSensor\")]] const dev d = d0;\n",
		shape: `(prefab "StructureGasSensor" (constvar (d dev d0)))`,
	},
}

// TestATokenTheGrammarReadsAsSeveral holds this front end to reporting a token
// the C grammar re-lexed and to building no expression out of its pieces.
func TestATokenTheGrammarReadsAsSeveral(t *testing.T) {
	for _, tt := range relexCases {
		t.Run(tt.name, func(t *testing.T) {
			f, diags, err := tsparse.Parse("test.c", tt.src)
			if err != nil {
				t.Fatalf("tsparse.Parse failed: %v", err)
			}
			if got := renderedDiags(diags); !slices.Equal(got, tt.want) {
				t.Errorf("reports\n\t%q\nwant\n\t%q", got, tt.want)
			}
			if got := fileShape(t, f); got != tt.shape {
				t.Errorf("builds\n\t%s\nwant\n\t%s", got, tt.shape)
			}
		})
	}
}
