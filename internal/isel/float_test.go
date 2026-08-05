package isel

import (
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/llvmir"
)

// fnegIR holds a negation of a double, which is the one opcode the bindings
// name no constant for.
const fnegIR = `
define void @main() {
entry:
  %slot = alloca double
  %x = load double, ptr %slot
  %negated = fneg double %x
  store double %negated, ptr %slot
  ret void
}
`

// TestFNegOpcode pins the numeric opcode the bindings do not name. Reading the
// wrong constant would leave a negation unselected, or select some other
// instruction as one.
func TestFNegOpcode(t *testing.T) {
	m := parseIR(t, fnegIR)
	fn := m.NamedFunction("main")

	var found bool
	for in := range llvmir.BlockInstrs(fn.EntryBasicBlock()) {
		if in.Name() != "negated" {
			continue
		}
		found = true
		if got := in.InstructionOpcode(); got != opcodeFNeg {
			t.Errorf("fneg opcode = %d, and opcodeFNeg is %d", got, opcodeFNeg)
		}
	}
	if !found {
		t.Fatalf("the parsed module holds no fneg:\n%s", m.String())
	}
}

// TestFloatArithmeticSelectsTheMachineInstructions covers the half of the value
// model that needs no conversion: every register holds a double, so a float add
// is the machine's add and a float division needs no truncation after it.
func TestFloatArithmeticSelectsTheMachineInstructions(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
		// immediate, when set, is the constant the first instruction after the
		// preamble takes. A mnemonic does not pin one: a multiply by 1 renders
		// the same mul, which [TestDoubleNegationFlipsTheSignOfAZero] catches.
		immediate string
	}{
		{
			name: "addition",
			body: "%r = fadd double %a, %b",
			want: []string{"add", "poke"},
		},
		{
			name: "subtraction",
			body: "%r = fsub double %a, %b",
			want: []string{"sub", "poke"},
		},
		{
			name: "multiplication",
			body: "%r = fmul double %a, %b",
			want: []string{"mul", "poke"},
		},
		{
			name: "division needs no truncation",
			body: "%r = fdiv double %a, %b",
			want: []string{"div", "poke"},
		},
		{
			name:      "negation is a multiplication by minus one",
			body:      "%r = fneg double %a",
			want:      []string{"mul", "poke"},
			immediate: "-1",
		},
		{
			name: "widening an integer costs nothing",
			body: "%w = sitofp i64 7 to double\n  %r = fadd double %a, %w",
			want: []string{"add", "poke"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectFloat(t, tt.body)
			body := mnemonics(got)[floatPreamble:]
			if len(body) != len(tt.want) {
				t.Fatalf("selected %v, want one instruction per %v after the preamble", got, tt.want)
			}
			for i, mnemonic := range body {
				if mnemonic != tt.want[i] {
					t.Errorf("instruction %d is %q, want %q: %v", i, mnemonic, tt.want[i], got)
				}
			}
			if tt.immediate == "" {
				return
			}
			if line := got[floatPreamble]; !strings.HasSuffix(line, " "+tt.immediate) {
				t.Errorf("the selected %q does not take %s: %v", line, tt.immediate, got)
			}
		})
	}
}

// TestFloatComparisonSelectsTheMachinePredicate covers the other half. The six
// ordered predicates are instructions; the unordered ones InstCombine
// canonicalises into are their negations, which a branch takes by swapping
// successors and a value by complementing the answer.
func TestFloatComparisonSelectsTheMachinePredicate(t *testing.T) {
	tests := []struct {
		name string
		pred string
		want []string
	}{
		{name: "ordered equality", pred: "oeq", want: []string{"seq"}},
		{name: "unordered inequality", pred: "une", want: []string{"sne"}},
		{name: "ordered less than", pred: "olt", want: []string{"slt"}},
		{name: "ordered less or equal", pred: "ole", want: []string{"sle"}},
		{name: "ordered greater than", pred: "ogt", want: []string{"sgt"}},
		{name: "ordered greater or equal", pred: "oge", want: []string{"sge"}},
		{name: "unordered less than is a negated greater or equal", pred: "ult", want: []string{"sge", "seqz"}},
		{name: "unordered less or equal is a negated greater than", pred: "ule", want: []string{"sgt", "seqz"}},
		{name: "unordered greater than is a negated less or equal", pred: "ugt", want: []string{"sle", "seqz"}},
		{name: "unordered greater or equal is a negated less than", pred: "uge", want: []string{"slt", "seqz"}},
		{name: "ordered inequality is a less than or a greater than", pred: "one", want: []string{"slt", "sgt", "or"}},
		{name: "unordered equality negates that", pred: "ueq", want: []string{"slt", "sgt", "or", "seqz"}},
		{name: "unordered is a NaN test per operand", pred: "uno", want: []string{"snan", "snan", "or"}},
		{name: "ordered negates that", pred: "ord", want: []string{"snan", "snan", "or", "seqz"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := "%c = fcmp " + tt.pred + " double %a, %b\n" +
				"  %w = uitofp i1 %c to double\n" +
				"  %r = fadd double %w, 0.0"
			got := selectFloat(t, body)
			mnemonic := mnemonics(got)[floatPreamble:]
			// The trailing add and poke are the store the body ends in, which
			// keeps the comparison from being dead.
			if len(mnemonic) != len(tt.want)+2 {
				t.Fatalf("selected %v, want %v followed by an add and a poke", got, tt.want)
			}
			for i, want := range tt.want {
				if mnemonic[i] != want {
					t.Errorf("instruction %d is %q, want %q: %v", i, mnemonic[i], want, got)
				}
			}
		})
	}
}

// TestFloatComparisonFusesIntoItsBranch checks that a comparison whose only
// reader is its block's branch costs no register, and that an unordered
// predicate takes its negation by swapping the two successors rather than by
// complementing a truth value.
func TestFloatComparisonFusesIntoItsBranch(t *testing.T) {
	tests := []struct {
		name  string
		pred  string
		want  string
		avoid string
	}{
		{name: "an ordered branch keeps its predicate", pred: "olt", want: "blt", avoid: "seqz"},
		{name: "an unordered branch swaps its successors", pred: "ult", want: "bge", avoid: "seqz"},
		{name: "an unordered equality branches on both orderings", pred: "ueq", want: "blt", avoid: "seqz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := `
define void @main() {
entry:
  %slot = alloca double
  %a = load double, ptr %slot
  %b = load double, ptr %slot
  %c = fcmp ` + tt.pred + ` double %a, %b
  br i1 %c, label %then, label %done

then:
  store double 1.0, ptr %slot
  br label %done

done:
  ret void
}
`
			m := parseIR(t, text)
			result, err := Select(t.Context(), m, Options{File: "test.c"})
			if err != nil {
				t.Fatalf("Select: %v\n%s", err, m.String())
			}
			got := render(result.Program.Funcs[0])
			joined := strings.Join(mnemonics(got), " ")
			if !strings.Contains(joined, tt.want) {
				t.Errorf("selection does not use %q: %v", tt.want, got)
			}
			if strings.Contains(joined, tt.avoid) {
				t.Errorf("selection materialised the comparison with %q: %v", tt.avoid, got)
			}
		})
	}
}

// TestNegatedFloatComparisonFeedingASelectSwapsTheArms pins the select-position
// twin of fusing a comparison into a branch. Without it every "d >= x ? a : b"
// costs the instruction that turns a truth value back round.
func TestNegatedFloatComparisonFeedingASelectSwapsTheArms(t *testing.T) {
	const text = `
define void @main() {
entry:
  %slot = alloca double
  %a = load double, ptr %slot
  %b = load double, ptr %slot
  %c = fcmp ult double %a, %b
  %r = select i1 %c, double 2.0, double 1.0
  store double %r, ptr %slot
  ret void
}
`
	m := parseIR(t, text)
	result, err := Select(t.Context(), m, Options{File: "test.c"})
	if err != nil {
		t.Fatalf("Select: %v\n%s", err, m.String())
	}
	got := render(result.Program.Funcs[0])
	if joined := strings.Join(mnemonics(got), " "); strings.Contains(joined, "seqz") {
		t.Errorf("the select did not absorb the negation: %v", got)
	}
	var selected string
	for _, line := range got {
		if strings.HasPrefix(line, "select ") {
			selected = line
		}
	}
	if selected == "" {
		t.Fatalf("no select was emitted: %v", got)
	}
	// sge answers what ult negates, so the arms trade places: the true arm is
	// the 1 the source wrote for the false one.
	if !strings.HasSuffix(selected, " 1 2") {
		t.Errorf("select arms were not traded: %q in %v", selected, got)
	}
}

// floatPreamble is how many instructions selectFloat's fixture emits before the
// body under test: the data region zeroing, and the two reads it supplies the
// operands with.
const floatPreamble = 3

// selectFloat selects a function whose body computes over two doubles read from
// a slot and stores the result, and returns the emitted instructions.
func selectFloat(t *testing.T, body string) []string {
	t.Helper()
	text := `
define void @main() {
entry:
  %slot = alloca double
  %a = load double, ptr %slot
  %b = load double, ptr %slot
  ` + body + `
  store double %r, ptr %slot
  ret void
}
`
	m := parseIR(t, text)
	result, err := Select(t.Context(), m, Options{File: "test.c"})
	if err != nil {
		t.Fatalf("Select: %v\n%s", err, m.String())
	}
	return render(result.Program.Funcs[0])
}
