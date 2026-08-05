package tsparse_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/lexer"
	"github.com/greg2010/ic11c/internal/tsparse"
)

// Every seed below is mutated one token at a time and the front end is asked
// about the result. What is asserted is consistency, not rightness: no
// source may make the parse fail outright, a refusal must name a byte the
// source has, and a clean parse must yield a tree with no hole in it.

// mutationSeeds are the well-formed programs the sweep mutates. Between them
// they write every construct a MicroC program is built from, since a mutation
// can only reach what the seed already holds.
var mutationSeeds = []string{
	"long long a = 1;\n",
	"constexpr long long k[2] = {-1, +2};\n",
	"[[ic11c::prefab(\"StructureGasSensor\")]] const dev d = d0;\n",
	"long long f(long long x, double y);\n",
	"long long f(long long *p) { return *p++; }\n",
	"void f(long long x) { if (x) long long a = 1; else x = 2; }\n",
	"void f(long long x) { while (x) { x--; } }\n",
	"void f(long long x) { do { x--; } while (x); }\n",
	"void f(void) { for (long long i = 0; i < 2; i++) ; }\n",
	"void f(long long x) { switch (x) { case 1: break; default: break; } }\n",
	"long long g(long long);\nlong long f(long long a) { return g(a) + (a) * 2; }\n",
	"void f(long long a, long long b) { a = b = 1; a += 2; a++; ++a; a = b = 1; a += 2; a++; ++a; }\n",
	"long long f(double d) { return (long long)d + (bool)d ? 1 : 2; }\n",
	"long long a[4];\nlong long f(void) { return a[1] & a[0] + 1; }\n",
}

// mutationInserts are the tokens the sweep writes into a seed: punctuation
// and operands a mistyped program is often one token from, and every
// multi-character operator, since any can be read as its parts where the
// grammar's parse-state lexing splits it (see relexed.go).
var mutationInserts = []string{
	";", ",", "=", "(", ")", "[", "]", "*", "&", "|", "1", "'x'", `"s"`, "x", "long", "const",
	"<<=", ">>=", "...", "==", "!=", "<=", ">=", "&&", "||", "<<", ">>", "++", "--", "->",
	"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "::",
}

// minMutants is the floor the sweep is held to. Without it a seed list that
// stopped lexing would read as a sweep that found nothing wrong.
const minMutants = 10000

// TestGeneratedMalformedProgramsAreAnswered sweeps the one-token mutations of
// [mutationSeeds] and holds each answer to being one a caller can act on: no
// error return, no sentence pointing outside the file, and no tree handed
// downstream with an unreported hole in it.
func TestGeneratedMalformedProgramsAreAnswered(t *testing.T) {
	mutants := mutants(t)
	if len(mutants) < minMutants {
		t.Fatalf("generated %d programs, want at least %d; the mutation has stopped reaching what the seeds write", len(mutants), minMutants)
	}
	for _, src := range mutants {
		tree, diags, err := tsparse.Parse("test.c", src)
		if err != nil {
			t.Fatalf("tsparse.Parse failed on %q: %v", src, err)
		}
		checkDiagnosticsAreInSource(t, "the generated program "+strconv.Quote(src), src, diags)
		if len(diags) != 0 {
			continue
		}
		// The shape is what the tree is read through rather than something
		// compared: rendering it visits every node and validates every position,
		// and a hole the front end left is a node with a spelling of its own.
		shape := fileShape(t, tree)
		if hole := holeIn(shape); hole != "" {
			t.Errorf("read this program without complaint and built %s into the tree:\n%s\n%s", hole, src, shape)
		}
	}
}

// holeIn names the first kind of node standing where the front end could not
// read the source, and is empty for a shape holding none. A hole under a
// clean parse is the defect this sweep exists for: the node says the front
// end knew it had failed, and the empty diagnostic list says it told nobody.
func holeIn(shape string) string {
	for _, hole := range []struct{ rendering, what string }{
		{"(baddecl)", "a bad declaration"},
		{"(badstmt)", "a bad statement"},
		{"(badexpr)", "a bad expression"},
	} {
		if strings.Contains(shape, hole.rendering) {
			return hole.what
		}
	}
	return ""
}

// mutants gives every one-token edit of every seed, without duplicates and
// without the seeds themselves. Edits are made a token at a time so what
// comes out is a program somebody could have typed, in a fixed order rather
// than at random so a failure is reproducible from the source alone.
func mutants(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	for _, seed := range mutationSeeds {
		seen[seed] = true
	}
	var out []string
	add := func(src string) {
		if seen[src] {
			return
		}
		seen[src] = true
		out = append(out, src)
	}
	for _, seed := range mutationSeeds {
		spans := tokenSpans(t, seed)
		for i, tok := range spans {
			add(seed[:tok.from] + seed[tok.to:])
			add(seed[:tok.from] + seed[tok.from:tok.to] + " " + seed[tok.from:])
			for _, insert := range mutationInserts {
				add(seed[:tok.from] + insert + " " + seed[tok.from:])
			}
			if i+1 < len(spans) {
				next := spans[i+1]
				add(seed[:tok.from] + seed[next.from:next.to] + " " + seed[tok.from:tok.to] + seed[next.to:])
			}
		}
	}
	return out
}

// tokenSpan is where one token was written in a seed.
type tokenSpan struct{ from, to int }

// tokenSpans gives the bytes each token of a program covers, which is what a
// mutation replaces. A seed the lexer cannot read whole is a defect in the seed.
func tokenSpans(t *testing.T, src string) []tokenSpan {
	t.Helper()
	l := lexer.New("test.c", src)
	var spans []tokenSpan
	for tok := l.Next(); tok.Kind != lexer.EOF; tok = l.Next() {
		spans = append(spans, tokenSpan{from: tok.Pos.Offset, to: tok.Pos.Offset + len(tok.Text)})
	}
	if diags := l.Diagnostics(); len(diags) != 0 {
		t.Fatalf("the seed %q does not lex: %s", src, diags)
	}
	return spans
}
