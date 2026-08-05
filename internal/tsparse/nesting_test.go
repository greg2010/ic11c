package tsparse

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/source"
)

// nested writes a program that puts n of one construct inside the next, which
// is what the limit is stated in.
func nested(shape string, n int) string {
	switch shape {
	case "parentheses":
		return "void f(void) { double x = " + strings.Repeat("(", n) + "1.0" + strings.Repeat(")", n) + "; }"
	case "blocks":
		return "void f(void) { " + strings.Repeat("{", n) + strings.Repeat("}", n) + " }"
	case "ifs":
		return "void f(void) { " + strings.Repeat("if (1) {", n) + strings.Repeat("}", n) + " }"
	case "casts":
		return "void f(void) { double x = " + strings.Repeat("(double)", n) + "1.0; }"
	case "subscripts":
		return "void f(void) { double a[1]; double x = a" + strings.Repeat("[0]", n) + "; }"
	case "calls":
		return "double g(double x); void f(void) { double x = " + strings.Repeat("g(", n) + "1.0" + strings.Repeat(")", n) + "; }"
	case "complements":
		return "void f(long long a) { long long x = " + strings.Repeat("!", n) + "a; }"
	case "negations":
		// Spaced, because two written together are the decrement operator and
		// eight of them would be four levels rather than eight.
		return "void f(long long a) { long long x = " + strings.Repeat("- ", n) + "a; }"
	case "dereferences":
		return "void f(long long *p) { long long x = " + strings.Repeat("*", n) + "p; }"
	case "openers":
		return "void f(void) { double x = " + strings.Repeat("(", n) + "1.0; }"
	}
	panic("unknown shape " + shape)
}

// shapes is every construct [nested] writes a chain of, which is what a table
// over the shapes covers. The openers close nothing and are not nesting, so
// they have a test of their own.
var shapes = []string{
	"parentheses", "blocks", "ifs", "casts", "subscripts", "calls",
	"complements", "negations", "dereferences",
}

// cheapestShape is the chain that reaches the ceiling on the deepest source
// soonest, which is what [maxNestingDepth] is measured on. A prefix operator
// costs one byte and one level, and the conversion spends more stack on one than
// on any other shape written that small.
const cheapestShape = "complements"

// TestRefusesNestingPastTheLimit holds the bound to what it reports and
// where, with the limit given rather than shipped since a deep enough source
// is a megabyte of parentheses. Each row names a construct with its exact
// byte: the rule names the first node past the limit in reading order.
func TestRefusesNestingPastTheLimit(t *testing.T) {
	tests := []struct {
		name   string
		shape  string
		depth  int
		limit  int
		column int
		at     byte
	}{
		{name: "parentheses", shape: "parentheses", depth: 8, limit: 9, column: 31, at: '('},
		{name: "blocks", shape: "blocks", depth: 8, limit: 6, column: 19, at: '{'},
		{name: "ifs", shape: "ifs", depth: 8, limit: 9, column: 40, at: 'i'},
		{name: "casts", shape: "casts", depth: 8, limit: 9, column: 52, at: 'd'},
		{name: "subscripts", shape: "subscripts", depth: 8, limit: 9, column: 40, at: 'a'},
		{name: "calls", shape: "calls", depth: 8, limit: 9, column: 51, at: 'g'},
		{name: "complements", shape: "complements", depth: 8, limit: 9, column: 41, at: '!'},
		{name: "negations", shape: "negations", depth: 8, limit: 9, column: 45, at: '-'},
		{name: "dereferences", shape: "dereferences", depth: 8, limit: 9, column: 42, at: '*'},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := nested(tt.shape, tt.depth)
			f, diags, err := parse("test.c", src, tt.limit)
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			if len(diags) != 1 {
				t.Fatalf("got %d diagnostics, want the one refusal:\n%s", len(diags), diags)
			}
			want := fmt.Sprintf("nested too deeply; the compiler reads at most %d constructs inside one another", tt.limit)
			if diags[0].Msg != want {
				t.Errorf("message = %q, want %q", diags[0].Msg, want)
			}
			if diags[0].Pos.Line != 1 || diags[0].Pos.Column != tt.column {
				t.Errorf("position = %d:%d, want 1:%d", diags[0].Pos.Line, diags[0].Pos.Column, tt.column)
			}
			if at := src[diags[0].Pos.Offset]; at != tt.at {
				t.Errorf("position names %q, want the %q the nesting is written with", at, tt.at)
			}
			if len(f.Decls) != 0 {
				t.Errorf("got %d declarations, want none from a file that was not read", len(f.Decls))
			}
		})
	}
}

// TestTheLimitIsTheDeepestNestingRead is the boundary the refusal turns on. A
// bound stated as a maximum and enforced one level early refuses a program it
// promises to read, and every row of the table above would still pass.
func TestTheLimitIsTheDeepestNestingRead(t *testing.T) {
	for _, shape := range shapes {
		t.Run(shape, func(t *testing.T) {
			src := nested(shape, 6)
			admits, found := 0, false
			for limit := range 40 {
				_, diags, err := parse("test.c", src, limit)
				if err != nil {
					t.Fatalf("parse failed: %v", err)
				}
				if len(diags) == 0 {
					admits, found = limit, true
					break
				}
			}
			if !found {
				t.Fatalf("no limit under 40 reads six nested %s", shape)
			}
			_, diags, err := parse("test.c", src, admits-1)
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			if len(diags) != 1 {
				t.Errorf("a limit of %d drew %d diagnostics, want the one refusal that a limit of %d does not:\n%s",
					admits-1, len(diags), admits, diags)
			}
		})
	}
}

// TestUnclosedBracketsDoNotNest is why the bound is stated over the tree rather
// than over the brackets the source writes. A run of openers nothing closes is
// not nesting: the grammar cannot read it, and its recovery hands the whole run
// back as one error node holding a flat list, which no walk descends into.
func TestUnclosedBracketsDoNotNest(t *testing.T) {
	_, diags, err := parse("test.c", nested("openers", 64), 16)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	for _, d := range diags {
		if strings.Contains(d.Msg, "nested too deeply") {
			t.Errorf("64 unclosed '(' drew the nesting refusal at a limit of 16, so the bound is counting brackets rather than tree:\n%s", diags)
		}
	}
	if len(diags) == 0 {
		t.Error("64 unclosed '(' were accepted, so this proves nothing about which diagnostic they draw")
	}
}

// TestTheShippedLimitAdmitsWhatProgramsWrite is the other half of the bound.
// The deepest program the compiler ships nests 45 levels, so a limit anywhere
// near what a program reaches would refuse working source.
func TestTheShippedLimitAdmitsWhatProgramsWrite(t *testing.T) {
	_, diags, err := Parse("test.c", nested("parentheses", 10000))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("10000 nested parentheses were refused under a limit of %d:\n%s", maxNestingDepth, diags)
	}
}

// TestTheNestingRefusalSurvivesTheDiagnosticCap is what makes the refusal
// worth reporting at all: it is the reason the file came back holding
// nothing, and a report that cut it and kept sixty-four stray characters
// instead would leave the emptiness unexplained.
func TestTheNestingRefusalSurvivesTheDiagnosticCap(t *testing.T) {
	src := strings.Repeat("@", maxDiagnostics+8) + nested("parentheses", 16)
	f, diags, err := parse("test.c", src, 8)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(f.Decls) != 0 {
		t.Fatalf("got %d declarations, want none from a file that was not read", len(f.Decls))
	}
	if !slices.ContainsFunc(diags, func(d source.Diagnostic) bool {
		return strings.Contains(d.Msg, "nested too deeply")
	}) {
		t.Errorf("the file came back empty and the report does not say why:\n%s", diags)
	}
}

// stackProbeEnv names the depth [TestStackProbe] is asked to convert. The
// ceiling the limit sits under is a stack overflow, which no process can report
// about itself, so it is measured from outside one.
const stackProbeEnv = "IC11C_TSPARSE_STACK_PROBE_DEPTH"

// TestStackProbe converts one chain of the shape that reaches the ceiling
// soonest and does nothing else. It is the body of the subprocess
// [stackSurvives] runs, and is skipped in a run that names no depth.
func TestStackProbe(t *testing.T) {
	asked, named := os.LookupEnv(stackProbeEnv)
	if !named {
		t.Skip(stackProbeEnv + " names no depth, so there is nothing to convert")
	}
	depth, err := strconv.Atoi(asked)
	if err != nil {
		t.Fatalf("%s = %q: %v", stackProbeEnv, asked, err)
	}
	// No limit at all, so what is measured is the walk rather than the refusal
	// that exists to stand in front of it. A limit stated as the depth asked for
	// would fire on the constructs the chain is written inside and measure
	// nothing.
	if _, _, err := parse("probe.c", nested(cheapestShape, depth), math.MaxInt); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
}

// stackSurvives reports whether converting a chain of that depth returns rather
// than exhausting the goroutine stack. It runs the parse in a subprocess,
// because the answer it is looking for ends whatever process asks the question.
func stackSurvives(t *testing.T, depth int) bool {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestStackProbe$", "-test.timeout=120s")
	cmd.Env = append(os.Environ(), stackProbeEnv+"="+strconv.Itoa(depth))
	out, err := cmd.CombinedOutput()
	switch {
	case err == nil:
		return true
	case strings.Contains(string(out), "stack overflow"):
		return false
	}
	t.Fatalf("converting %d nested %s failed for a reason that is not the stack: %v\n%s",
		depth, cheapestShape, err, out)
	return false
}

// TestTheLimitStandsUnderTheStackCeiling is the claim [maxNestingDepth]
// exists to make: every walk over the tree is a recursion, so a source deep
// enough takes the goroutine stack with it. It is measured on the shape that
// reaches that ceiling soonest — a prefix operator, one byte and one level each.
func TestTheLimitStandsUnderTheStackCeiling(t *testing.T) {
	if !stackSurvives(t, maxNestingDepth) {
		t.Errorf("converting %d nested %s exhausts the stack, and the limit admits that many",
			maxNestingDepth, cheapestShape)
	}
}

// TestNestingPastTheLimitIsRefused is the other direction, and the one a
// programmer sees. Source past the limit comes back with the one sentence
// naming the nesting, which is only true if the limit is reached before any
// walk that recurses descends into it.
func TestNestingPastTheLimitIsRefused(t *testing.T) {
	f, diags, err := Parse("test.c", nested(cheapestShape, maxNestingDepth+1))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(diags) != 1 || !strings.Contains(diags[0].Msg, "nested too deeply") {
		t.Fatalf("got %d diagnostics, want the one refusal:\n%s", len(diags), diags)
	}
	if len(f.Decls) != 0 {
		t.Errorf("got %d declarations, want none from a file that was not read", len(f.Decls))
	}
}
