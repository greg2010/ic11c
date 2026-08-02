package sema_test

import "testing"

// TestResultMayBeNonFinite covers which intrinsics analysis marks as able to
// put a value outside the finite doubles in a register, which is where the
// taint the backend's guards follow starts.
//
// A batch read matching no device answers Average with NaN and Maximum with
// negative infinity, so the mark depends on the mode the call names. Every
// other marked instruction carries it unconditionally, and the cases below name
// the operands that reach the answer.
func TestResultMayBeNonFinite(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want bool
	}{
		{name: "a batch average, which answers no match with NaN", expr: "__ic_load_batch(1, Temperature, Average)", want: true},
		{name: "a batch maximum, which answers no match with an infinity", expr: "__ic_load_batch(1, Temperature, Maximum)", want: true},
		{name: "a batch sum, which answers no match with zero", expr: "__ic_load_batch(1, Temperature, Sum)"},
		{name: "a batch minimum, which answers no match with zero", expr: "__ic_load_batch(1, Temperature, Minimum)"},
		{name: "a named batch average", expr: "__ic_load_batch_named(1, 2, Temperature, Average)", want: true},
		{name: "a named batch maximum", expr: "__ic_load_batch_named(1, 2, Temperature, Maximum)", want: true},
		{name: "a named batch minimum", expr: "__ic_load_batch_named(1, 2, Temperature, Minimum)"},
		{name: "a batch slot average", expr: "__ic_load_batch_slot(1, 0, Occupied, Average)", want: true},
		{name: "a batch slot maximum", expr: "__ic_load_batch_slot(1, 0, Occupied, Maximum)", want: true},
		{name: "a named batch slot average", expr: "__ic_load_batch_named_slot(1, 2, 0, Occupied, Average)", want: true},
		{name: "a square root, which answers a negative with NaN", expr: "__ic_sqrt(2)", want: true},
		{name: "a logarithm, which answers a negative with NaN and zero with an infinity", expr: "__ic_log(2)", want: true},
		{name: "an arc sine, which answers past 1 with NaN", expr: "__ic_asin(2)", want: true},
		{name: "an arc cosine, which answers past 1 with NaN", expr: "__ic_acos(2)", want: true},
		{name: "a sine, which answers an infinity with NaN", expr: "__ic_sin(2)", want: true},
		{name: "a cosine, which answers an infinity with NaN", expr: "__ic_cos(2)", want: true},
		{name: "a tangent, which answers an infinity with NaN", expr: "__ic_tan(2)", want: true},
		{name: "a power, which answers a negative base under a fractional exponent with NaN", expr: "__ic_pow(2, 2)", want: true},
		{name: "an exponential, which overflows to an infinity", expr: "__ic_exp(2)", want: true},
		{name: "an interpolation, which answers an infinite endpoint with NaN", expr: "__ic_lerp(1, 2, 0.5)", want: true},
		{name: "an arc tangent, whose range is bounded", expr: "__ic_atan(2)"},
		{name: "a two-argument arc tangent, whose range is bounded", expr: "__ic_atan2(1, 2)"},
		{name: "a clamp, which answers with one of its operands", expr: "__ic_clamp(1, 2, 3)"},
		{name: "a sign, which answers 0, 1 or -1", expr: "__ic_sgn(2)"},
		{name: "an absolute value", expr: "__ic_abs(2)"},
		{name: "a rounding", expr: "__ic_round(2)"},
		{name: "a truncation", expr: "__ic_trunc(2)"},
		{name: "a ceiling", expr: "__ic_ceil(2)"},
		{name: "a floor", expr: "__ic_floor(2)"},
		{name: "a random draw, which is between 0 and 1", expr: "__ic_rand()"},
		{name: "a device read, which faults instead", expr: "__ic_load(d0, Setting)"},
		{name: "a reagent read", expr: "__ic_load_reagent(d0, Contents, 1)"},
		{name: "a minimum of two numbers", expr: "__ic_min(1, 2)"},
		{name: "a maximum of two numbers", expr: "__ic_max(1, 2)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prog, diags := analyze(t, "void main(void) { __ic_store(d0, Setting, "+tt.expr+"); }")
			if diags.HasErrors() {
				t.Fatalf("the program was rejected:\n%s", diags.String())
			}
			var got bool
			for _, call := range prog.Intrinsics {
				if call.ResultMayBeNonFinite {
					got = true
				}
			}
			if got != tt.want {
				t.Errorf("ResultMayBeNonFinite over %s = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}
