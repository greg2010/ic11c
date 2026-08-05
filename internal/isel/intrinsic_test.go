package isel

import (
	"strings"
	"testing"

	"tinygo.org/x/go-llvm"
)

// TestSelectRefusesAnOptimizerFoldWithNoInstruction covers the diagnostic for a
// call no source line contains. The machine has a signed minimum and maximum
// that propagate a NaN and nothing else of the family, so the advice has to
// follow what folded rather than naming __ic_min for everything.
func TestSelectRefusesAnOptimizerFoldWithNoInstruction(t *testing.T) {
	tests := []struct {
		name    string
		declare string
		call    string
		// result is the type of %r, which the store below has to agree with. It
		// defaults to the machine's own integer width.
		result string
		want   []string
		avoid  []string
	}{
		{
			name:    "an unsigned minimum is a signed one written directly",
			declare: "declare i64 @llvm.umin.i64(i64, i64)",
			call:    "%r = call i64 @llvm.umin.i64(i64 %x, i64 1000)",
			want:    []string{"llvm.umin.i64", "signed", "__ic_min"},
		},
		{
			name:    "an unsigned maximum is the same",
			declare: "declare i64 @llvm.umax.i64(i64, i64)",
			call:    "%r = call i64 @llvm.umax.i64(i64 %x, i64 1000)",
			want:    []string{"llvm.umax.i64", "signed", "__ic_max"},
		},
		{
			name:    "a population count has no signedness to advise about",
			declare: "declare i64 @llvm.ctpop.i64(i64)",
			call:    "%r = call i64 @llvm.ctpop.i64(i64 %x)",
			want:    []string{"llvm.ctpop.i64"},
			avoid:   []string{"__ic_min", "__ic_max", "__ic_clamp"},
		},
		{
			name:    "a byte swap has none either",
			declare: "declare i64 @llvm.bswap.i64(i64)",
			call:    "%r = call i64 @llvm.bswap.i64(i64 %x)",
			want:    []string{"llvm.bswap.i64"},
			avoid:   []string{"__ic_min", "__ic_max", "__ic_clamp"},
		},
		{
			name:    "nor does a saturating addition",
			declare: "declare i64 @llvm.sadd.sat.i64(i64, i64)",
			call:    "%r = call i64 @llvm.sadd.sat.i64(i64 %x, i64 7)",
			want:    []string{"llvm.sadd.sat.i64"},
			avoid:   []string{"__ic_min", "__ic_max", "__ic_clamp"},
		},
		{
			// The machine propagates a NaN where minnum and maxnum hand back the
			// operand that is not one, so selecting it here drops a NaN.
			name:    "a minimum that answers the operand which is not a NaN",
			declare: "declare double @llvm.minnum.f64(double, double)",
			call:    "%r = call double @llvm.minnum.f64(double %d, double 1.000000e+00)",
			result:  "double",
			want:    []string{"llvm.minnum.f64", "NaN", "__ic_min", "__ic_isnan"},
		},
		{
			name:    "a maximum of the same family",
			declare: "declare double @llvm.maxnum.f64(double, double)",
			call:    "%r = call double @llvm.maxnum.f64(double %d, double 1.000000e+00)",
			result:  "double",
			want:    []string{"llvm.maxnum.f64", "NaN", "__ic_max"},
		},
		{
			// Away-from-zero rounding, where the machine's round is banker's.
			// It has no advice: nothing in the language asks for it.
			name:    "a rounding whose ties go the other way",
			declare: "declare double @llvm.round.f64(double)",
			call:    "%r = call double @llvm.round.f64(double %d)",
			result:  "double",
			want:    []string{"llvm.round.f64"},
			avoid:   []string{"__ic_min", "__ic_max", "__ic_clamp"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.result
			if result == "" {
				result = "i64"
			}
			text := tt.declare + `

define void @main() {
entry:
  %slot = alloca i64
  %x = load i64, ptr %slot
  %d = sitofp i64 %x to double
  ` + tt.call + `
  store ` + result + ` %r, ptr %slot
  ret void
}
`
			m := parseIR(t, text)
			_, err := Select(t.Context(), m, Options{File: "test.c"})
			if err == nil {
				t.Fatalf("selection accepted a fold with no machine instruction:\n%s", m.String())
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not mention %q: %v", want, err)
				}
			}
			for _, avoid := range tt.avoid {
				if strings.Contains(err.Error(), avoid) {
					t.Errorf("the refusal advises %q, which does not apply to this fold: %v", avoid, err)
				}
			}
		})
	}
}

func TestSelectIntrinsics(t *testing.T) {
	cases := []struct {
		name   string
		fn     string
		result bool
		args   []int64
		want   string
	}{
		{"device load", "__ic_load", true, []int64{0, 6}, "l vr0 d0 LogicType(6)"},
		{"housing load", "__ic_load", true, []int64{-1, 6}, "l vr0 db LogicType(6)"},
		{"device store moves a literal into a register first", "__ic_store", false, []int64{1, 28, 1}, "s d1 LogicType(28) vr0"},
		{"device presence", "__ic_device_present", true, []int64{2}, "sdse vr0 d2"},
		{"batch load", "__ic_load_batch", true, []int64{99, 6, 1}, "lb vr0 99 LogicType(6) BatchMode(1)"},
		{"slot load", "__ic_load_slot", true, []int64{0, 2, 3}, "ls vr0 d0 2 LogicSlotType(3)"},
		{"reagent load", "__ic_load_reagent", true, []int64{0, 1, 99}, "lr vr0 d0 ReagentMode(1) 99"},
		{"square root", "__ic_sqrt", true, []int64{9}, "sqrt vr0 9"},
		{"clamp", "__ic_clamp", true, []int64{5, 0, 3}, "clamp vr0 5 0 3"},
		{"yield", "__ic_yield", false, nil, "yield"},
		// Where sleep may sit is a property of the finished layout, which
		// mir.Program.CheckPlacement holds it to and this stage cannot see.
		{"sleep", "__ic_sleep", false, []int64{5}, "sleep 5"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bd := newBuilder(t)
			params := make([]llvm.Type, len(tc.args))
			args := make([]llvm.Value, len(tc.args))
			for i, arg := range tc.args {
				params[i] = bd.i64
				args[i] = bd.konst(arg)
			}
			result := bd.ctx.VoidType()
			if tc.result {
				result = bd.i64
			}
			fnType := llvm.FunctionType(result, params, false)
			callee := llvm.AddFunction(bd.m, tc.fn, fnType)
			call := bd.b.CreateCall(fnType, callee, args, "")
			if tc.result {
				bd.keep(call)
			}
			bd.b.CreateRetVoid()

			got := selectFunc(t, bd)
			if !contains(got, tc.want) {
				t.Errorf("selected %v, want a %q", got, tc.want)
			}
		})
	}
}

// TestSelectRefusesOperandsTheChipWouldFaultOn covers the operand values the
// chip assembles and then faults on. Analysis rejects most at the name the
// source wrote, so these reach selection only as a constant in the IR.
func TestSelectRefusesOperandsTheChipWouldFaultOn(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "a device pin the housing does not have",
			body: `call i64 @__ic_load(i64 6, i64 6)`,
			want: []string{"device pin d6", "d0 through d5"},
		},
		{
			name: "a device pin far outside the pattern",
			body: `call i64 @__ic_load(i64 40, i64 6)`,
			want: []string{"device pin d40", "d0 through d5"},
		},
		{
			name: "a logic type wider than the enum it is backed by",
			body: `call i64 @__ic_load(i64 0, i64 70000)`,
			want: []string{"argument 2 of '__ic_load'", "a logic type"},
		},
		{
			name: "a device pin that is not known at compile time",
			body: `%p = load i64, ptr %slot
  %r = call i64 @__ic_load(i64 %p, i64 6)`,
			want: []string{"argument 1 of '__ic_load'", "known at compile time"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := parseIR(t, `
declare i64 @__ic_load(i64, i64)

define void @main() {
entry:
  %slot = alloca i64
  `+tc.body+`
  ret void
}
`)
			_, err := Select(t.Context(), m, Options{File: "test.c"})
			if err == nil {
				t.Fatalf("selection accepted an operand the chip faults on:\n%s", m.String())
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not mention %q: %v", want, err)
				}
			}
		})
	}
}

// TestSelectOptimizerIntrinsics covers the intrinsics InstCombine forms out of
// ordinary comparisons and selects, expecting a target instruction for each.
func TestSelectOptimizerIntrinsics(t *testing.T) {
	cases := []struct {
		name string
		ir   string
		want string
	}{
		{
			name: "smin becomes min",
			ir:   "%r = call i64 @llvm.smin.i64(i64 %x, i64 7)",
			want: "min",
		},
		{
			name: "smax becomes max",
			ir:   "%r = call i64 @llvm.smax.i64(i64 %x, i64 7)",
			want: "max",
		},
		{
			name: "abs drops its poison flag",
			ir:   "%r = call i64 @llvm.abs.i64(i64 %x, i1 true)",
			want: "abs",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := `
declare i64 @llvm.smin.i64(i64, i64)
declare i64 @llvm.smax.i64(i64, i64)
declare i64 @llvm.abs.i64(i64, i1)

define void @main() {
entry:
  %slot = alloca i64
  %x = load i64, ptr %slot
  ` + tc.ir + `
  store i64 %r, ptr %slot
  ret void
}
`
			m := parseIR(t, text)
			result, err := Select(t.Context(), m, Options{File: "test.c"})
			if err != nil {
				t.Fatalf("Select: %v\n%s", err, m.String())
			}
			got := mnemonics(render(result.Program.Funcs[0]))
			if !contains(got, tc.want) {
				t.Errorf("selected %v, want one %s", got, tc.want)
			}
		})
	}
}

// TestSelectDoubleLoweringConversions covers the three shapes the double
// lowering of the integers puts in front of selection. The rounding is the
// machine's trunc exactly; the other two cost nothing, and a wrong answer on any
// of them is a value silently something else, so each is run.
func TestSelectDoubleLoweringConversions(t *testing.T) {
	cases := []struct {
		name string
		// body computes %r as the double stored into slot zero.
		body string
		want float64
		// mnemonic, when set, is the one instruction the body must select.
		mnemonic string
		// lines is how many instructions the whole program takes, the zeroing
		// prologue and the final store included.
		lines int
	}{
		{
			name:     "the rounding is the machine's own trunc",
			body:     "%r = call double @llvm.trunc.f64(double %d)",
			want:     -7,
			mnemonic: "trunc",
			lines:    5,
		},
		{
			// The value comes back unrounded: irgen writes this pair only around
			// a value the register already holds whole.
			name:  "reading a whole double back as an integer costs nothing",
			body:  "%i = fptosi double %d to i64\n  %r = sitofp i64 %i to double",
			want:  -7.5,
			lines: 4,
		},
		{
			name:  "choosing between one and zero on a truth value costs nothing",
			body:  "%c = fcmp olt double %d, 0.000000e+00\n  %r = select i1 %c, double 1.000000e+00, double 0.000000e+00",
			want:  1,
			lines: 5,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := parseIR(t, `
declare double @llvm.trunc.f64(double)

define void @main() {
entry:
  %slot = alloca double
  store double -7.500000e+00, ptr %slot
  %d = load double, ptr %slot
  `+tc.body+`
  store double %r, ptr %slot
  ret void
}
`)
			assembly := assemble(t, m)
			if got := len(assemblyLines(assembly)); got != tc.lines {
				t.Errorf("the program takes %d instructions, want %d:\n%s", got, tc.lines, assembly)
			}
			if tc.mnemonic != "" && !strings.Contains(assembly, tc.mnemonic+" ") {
				t.Errorf("the program holds no %q:\n%s", tc.mnemonic, assembly)
			}
			if got := runOnChip(t, assembly); got != tc.want {
				t.Errorf("the program left %v in slot zero, want %v:\n%s", got, tc.want, assembly)
			}
		})
	}
}

// assemblyLines splits emitted assembly into its instructions.
func assemblyLines(assembly string) []string {
	return strings.Split(strings.TrimSuffix(assembly, "\n"), "\n")
}
