package vm

import (
	"math"
	"testing"
)

// The saturating casts are where a Go-versus-C# difference would be silent, so
// the expectations below come from what .NET Core guarantees for a double to
// integer conversion rather than from what the Go code happens to do: NaN
// becomes zero, a value outside the range clamps to the nearest end of it, and
// everything else truncates toward zero. Go's own conversion leaves all three
// undefined.

func TestInt32Saturating(t *testing.T) {
	tests := []struct {
		name  string
		input float64
		want  int
	}{
		{name: "NaN becomes zero", input: math.NaN(), want: 0},
		{name: "positive infinity clamps high", input: math.Inf(1), want: math.MaxInt32},
		{name: "negative infinity clamps low", input: math.Inf(-1), want: math.MinInt32},
		{name: "zero", input: 0, want: 0},
		{name: "negative zero", input: math.Copysign(0, -1), want: 0},
		{name: "truncates toward zero", input: 1.9, want: 1},
		{name: "truncates a negative toward zero", input: -1.9, want: -1},
		{name: "a fraction below one truncates to zero", input: -0.5, want: 0},
		{name: "the largest representable int", input: 2147483647, want: math.MaxInt32},
		{name: "one below the largest", input: 2147483646.7, want: 2147483646},
		{name: "past the largest clamps", input: 2147483648, want: math.MaxInt32},
		{name: "far past the largest clamps", input: 1e300, want: math.MaxInt32},
		{name: "the smallest representable int", input: -2147483648, want: math.MinInt32},
		{name: "past the smallest clamps", input: -2147483649, want: math.MinInt32},
		{name: "far past the smallest clamps", input: -1e300, want: math.MinInt32},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := int32Saturating(tt.input); got != tt.want {
				t.Errorf("int32Saturating(%v) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestInt64Saturating(t *testing.T) {
	tests := []struct {
		name  string
		input float64
		want  int64
	}{
		{name: "NaN becomes zero", input: math.NaN(), want: 0},
		{name: "positive infinity clamps high", input: math.Inf(1), want: math.MaxInt64},
		{name: "negative infinity clamps low", input: math.Inf(-1), want: math.MinInt64},
		{name: "truncates toward zero", input: 1.9, want: 1},
		{name: "truncates a negative toward zero", input: -1.9, want: -1},
		{name: "the payload modulus is inside the range", input: twoPow53, want: 1 << 53},
		// 2^63 is the first double above the range and the largest one inside it
		// is 2^63 - 1024, since no double lands between them.
		{name: "the largest double inside the range", input: 9223372036854774784, want: 9223372036854774784},
		{name: "two to the sixty three clamps high", input: 9223372036854775808, want: math.MaxInt64},
		{name: "far past the largest clamps", input: 1e300, want: math.MaxInt64},
		{name: "the smallest representable long", input: -9223372036854775808, want: math.MinInt64},
		{name: "past the smallest clamps", input: -9223372036854777856, want: math.MinInt64},
		{name: "far past the smallest clamps", input: -1e300, want: math.MinInt64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := int64Saturating(tt.input); got != tt.want {
				t.Errorf("int64Saturating(%v) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// unpackASCII6 is ProgrammableChip.UnpackASCII6, the inverse of the STR("...")
// preprocessor form. Nothing the chip runs needs it: it is here so a test can
// state an expectation in text rather than in a packed integer.
//
// A value carrying more than six bytes of payload, which the unsigned
// conversion can produce, yields only the low six: the format has no seventh
// character to name.
func unpackASCII6(packed float64, signed bool) string {
	n := uint64(DoubleToLong(packed, signed))
	width := 0
	for rest := n; rest != 0 && width < packedASCIIBytes; rest >>= 8 {
		width++
	}
	out := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		out[i] = byte(n & 0xff)
		n >>= 8
	}
	return string(out)
}

// TestUnpackASCII6StopsAtSixBytes covers the one input the format cannot
// describe: the unsigned conversion keeps 54 bits, which is seven bytes of
// payload, and the packed form has only six characters to spend them on.
func TestUnpackASCII6StopsAtSixBytes(t *testing.T) {
	tests := []struct {
		name   string
		packed float64
		signed bool
		want   string
	}{
		{name: "empty", packed: 0, want: ""},
		{name: "one byte", packed: 'A', want: "A"},
		{name: "six bytes", packed: 0x414243444546, want: "ABCDEF"},
		{name: "a seventh byte is dropped", packed: 0x01414243444546, want: "ABCDEF"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := unpackASCII6(tt.packed, tt.signed); got != tt.want {
				t.Errorf("unpackASCII6(%v) = %q, want %q", tt.packed, got, tt.want)
			}
		})
	}
}
