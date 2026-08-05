package sema_test

import (
	"context"
	"math"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/sema"
	"github.com/greg2010/ic11c/internal/tsparse"
)

// TestAnalysisCostsWhatTheSourceCosts holds analysis to linear time on the
// shapes that make one function deep.
//
// Four steps that look like one node's work are walks of everything around it.
// Every expression is folded where it stands, and folding an operator folds its
// operands, so a chain is folded once more for each operator enclosing it. The
// C type model does the same: an operator asks what type C gives its operands,
// and answering walks the tree beneath them. A chain of arithmetic reaches both,
// which compounds them. The third is reachability: a switch asks whether each
// arm's body runs off its end, and answering walks every statement in it, so a
// nested switch is asked once more for each switch enclosing it. The fourth is
// name resolution: a name is looked for in the scope that mentions it and then
// in each scope enclosing that one, so a mention inside nested blocks costs the
// depth it sits at. Any of the four turns a generated program well inside the
// source size the CLI accepts into a compile that does not return.
//
// Wall clock is what is asserted, because the cost is the shape of the
// recursion rather than anything this package counts. What that costs in
// reliability is bought back with the span: over an eightfold one linear is
// eight and quadratic is sixty-four, and a threshold between two numbers that
// far apart has room on both sides for a machine under load, where one between
// four and sixteen did not. Each shape names its own sizes, since the constant
// chain is the one two of the costs compound on and measuring it where the
// others are measured would cost minutes for as long as it stayed broken.

// maxGrowth is what the larger size may cost over the smaller.
//
// It sits above the eight the work itself grows by, because the cost of a node
// grows with the tree as well: the maps analysis keys by node hold eight times
// as much at the larger size, and reaching into them costs more there. Measured
// over these shapes a linear analysis reaches sixteen and a quadratic one stays
// above fifty, so the threshold is placed between what each was seen to do
// rather than between what each is named for. Restoring any one of the four
// walks moves the shape it belongs to past it and leaves the other four where
// they were, which is what makes each row answer for its own cost.
const maxGrowth = 32

func TestAnalysisCostsWhatTheSourceCosts(t *testing.T) {
	tests := []struct {
		name  string
		what  string
		small int
		large int
		write func(int) string
	}{
		{
			name:  "constant terms in a chain",
			what:  "constant terms",
			small: 250,
			large: 2000,
			write: func(n int) string { return program(strings.Repeat("1 + ", n-1) + "1") },
		},
		{
			name:  "unary operators over one another",
			what:  "'!' operators",
			small: 1000,
			large: 8000,
			write: func(n int) string { return program(strings.Repeat("!", n) + "g") },
		},
		{
			name:  "casts inside one another",
			what:  "nested casts",
			small: 1000,
			large: 8000,
			write: func(n int) string { return program(strings.Repeat("(long long)", n) + "g") },
		},
		{
			name:  "switches inside one another",
			what:  "nested switches",
			small: 1000,
			large: 8000,
			write: func(n int) string {
				return body(strings.Repeat("switch (g) { case 1:\n", n) + "return;\n" +
					strings.Repeat("default: return; }\n", n))
			},
		},
		{
			name:  "blocks inside one another",
			what:  "nested blocks",
			small: 1000,
			large: 8000,
			write: nestedBlocks,
		},
		{
			name:  "pointer steps in a chain",
			what:  "pointer steps",
			small: 1000,
			large: 8000,
			write: steppedPointer,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parse := func(n int) *ast.File {
				file, diags, err := tsparse.Parse("test.c", tt.write(n))
				if err != nil {
					t.Fatalf("parsing %d %s: %v", n, tt.what, err)
				}
				if len(diags) != 0 {
					t.Fatalf("%d %s did not parse:\n%s", n, tt.what, diags)
				}
				return file
			}
			small, large := parse(tt.small), parse(tt.large)
			// Collection is what the ratio is measured around rather than
			// through. A run of the larger size holds four times the tree and
			// so meets the collector more often, which is growth in the heap
			// rather than in the work, and it is the one source of noise here
			// large enough to cross the threshold on its own. The collector is
			// turned off for the measurement and run by hand between samples,
			// so each sample is collection-free and none starts on the last
			// one's garbage.
			automatic := debug.SetGCPercent(-1)
			t.Cleanup(func() { debug.SetGCPercent(automatic) })
			analyze := func(n int, file *ast.File) time.Duration {
				runtime.GC()
				start := time.Now()
				_, diags, err := sema.Analyze(context.Background(), file, sema.Shipped{})
				elapsed := time.Since(start)
				if err != nil {
					t.Fatalf("Analyze failed: %v", err)
				}
				if diags.HasErrors() {
					t.Fatalf("%d %s was rejected, so nothing here folded:\n%s", n, tt.what, diags)
				}
				return elapsed
			}
			// One pass of each size is thrown away. The first analysis in a
			// process builds the scope the machine's constants live in, and the
			// first at each size is the one the allocator has not reached that
			// far for yet; neither is what is being measured, and both land on
			// the smaller size first, where they cost the most as a fraction.
			analyze(tt.small, small)
			analyze(tt.large, large)
			// The two sizes are then measured alternately rather than one after
			// the other. Both are held to the best of three, so load arriving
			// partway through lands on a sample of each rather than on every
			// sample of one, which is what would move the ratio on its own.
			quick, slow := time.Duration(math.MaxInt64), time.Duration(math.MaxInt64)
			for range 3 {
				quick = min(quick, analyze(tt.small, small))
				slow = min(slow, analyze(tt.large, large))
			}
			// A measurement down in the noise would make any ratio meaningless,
			// so the sizes are held to being worth timing rather than the timing
			// being trusted.
			const measurable = 100 * time.Microsecond
			if quick < measurable {
				t.Fatalf("%d %s analyze in %v, which is too quick to scale against; raise the sizes", tt.small, tt.what, quick)
			}
			if ratio := float64(slow) / float64(quick); ratio > maxGrowth {
				t.Errorf("%d %s took %v and %d took %v, a factor of %.1f; linear over this span is 8 and quadratic is 64",
					tt.small, tt.what, quick, tt.large, slow, ratio)
			}
		})
	}
}

// program wraps one expression in the smallest whole program that analysis
// accepts, so that what is timed is a program the compiler would translate
// rather than a fragment it rejects before it folds anything.
func program(expr string) string {
	return body("g = " + expr + ";\n")
}

// body wraps a statement list the same way, for the shapes that make a function
// body deep rather than an expression.
func body(stmts string) string {
	return "long long g;\nvoid main(void) {\n" + stmts + "}\n"
}

// steppedPointer writes a chain of n steps along one array. Each step is
// answered by walking the address beneath it, which is a walk per step and a
// quadratic over the chain unless the answer for each is kept.
//
// The steps alternate so that the chain never leaves the array: one that did
// would be refused rather than timed, and what is being measured is the cost of
// a program the compiler translates.
func steppedPointer(n int) string {
	return "long long a[4];\nlong long g;\nvoid main(void) {\n" +
		"    long long *p = a" + strings.Repeat(" + 1 - 1", n/2) + ";\n" +
		"    g = *p;\n}\n"
}

// nestedBlocks writes n blocks inside one another, each declaring a local of its
// own and each naming a variable declared outside all of them. Both halves are
// needed: the depth is what a name resolved by walking outwards costs, and the
// locals are what give each level a scope with something in it to walk past.
func nestedBlocks(n int) string {
	var b strings.Builder
	for i := range n {
		b.WriteString("long long v" + strconv.Itoa(i) + " = 1;\nif (g) {\n")
	}
	b.WriteString("g = 1;\n")
	b.WriteString(strings.Repeat("}\n", n))
	return body(b.String())
}
