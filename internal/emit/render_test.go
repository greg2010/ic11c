package emit

import (
	"errors"
	"maps"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/chiptest"
	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/mir"
)

func TestMain(m *testing.M) { chiptest.Main(m) }

// maxParsedConnection is int.MaxValue, the largest network index the
// int.TryParse inside Variable._GetNetworkIndex reads back. It is spelled out
// rather than taken from maxConnection: an expectation computed from the
// constant under test accepts whatever that constant happens to hold.
const maxParsedConnection = 2147483647

func lookupLogicType(t *testing.T, name string) ic10.LogicType {
	t.Helper()
	info, ok := ic10.LookupLogicType(name)
	if !ok {
		t.Fatalf("LookupLogicType(%q) found nothing", name)
	}
	return info.Value
}

// TestRenderOperand covers every operand form in both modes. The default is
// what ships; numeric spells the integer behind a machine name.
func TestRenderOperand(t *testing.T) {
	temperature := lookupLogicType(t, "Temperature")
	slotType := ic10.LogicSlotTypes[2]
	pin, err := mir.NewDevicePin(1, mir.NoConnection)
	if err != nil {
		t.Fatalf("NewDevicePin: %v", err)
	}
	connected, err := mir.NewDevicePin(3, 1)
	if err != nil {
		t.Fatalf("NewDevicePin with a connection: %v", err)
	}

	render := renderer{lineOf: map[string]int{"main.loop": 5}}

	tests := []struct {
		name string
		op   mir.Operand
		// wantDefault is the shipped form, wantNumeric the one with every machine
		// name written as the integer behind it. There is no readable column:
		// that form leaves every operand alone, the branch target included.
		wantDefault string
		wantNumeric string
	}{
		{name: "general register", op: mir.PhysReg{Reg: 3}, wantDefault: "r3", wantNumeric: "r3"},
		{name: "stack pointer", op: mir.PhysReg{Reg: ic10.RegSP}, wantDefault: "sp", wantNumeric: "sp"},
		{name: "return address", op: mir.PhysReg{Reg: ic10.RegRA}, wantDefault: "ra", wantNumeric: "ra"},
		{name: "immediate", op: mir.Imm{Value: 42}, wantDefault: "42", wantNumeric: "42"},
		{name: "label", op: mir.Label{Name: "main.loop"}, wantDefault: "5", wantNumeric: "5"},
		{name: "base device", op: mir.NewDeviceBase(), wantDefault: "db", wantNumeric: "db"},
		{name: "device pin", op: pin, wantDefault: "d1", wantNumeric: "d1"},
		{
			// The network connection form, which reaches a device the pin's own
			// device is connected to rather than the one on the pin.
			name:        "device pin through a network connection",
			op:          connected,
			wantDefault: "d3:1",
			wantNumeric: "d3:1",
		},
		{
			// The last index Variable._GetNetworkIndex reads back. One more and
			// its parse fails, which the chip treats as no suffix at all.
			name:        "the widest network connection the chip reads",
			op:          mir.Device{Kind: mir.DevicePin, Pin: 0, Conn: maxParsedConnection},
			wantDefault: "d0:2147483647",
			wantNumeric: "d0:2147483647",
		},
		{
			name:        "a fractional immediate",
			op:          mir.Imm{Value: 293.15},
			wantDefault: "293.15",
			wantNumeric: "293.15",
		},
		{
			// The emitter never produces exponential notation: the chip's own
			// number parser reads none, so a small magnitude expands in full.
			name:        "a small fractional immediate expands rather than taking an exponent",
			op:          mir.Imm{Value: 0.001},
			wantDefault: "0.001",
			wantNumeric: "0.001",
		},
		{
			name:        "logic type",
			op:          mir.LogicType{Value: temperature},
			wantDefault: "Temperature",
			wantNumeric: strconv.FormatUint(uint64(temperature), 10),
		},
		{
			name:        "logic slot type",
			op:          mir.LogicSlotType{Value: slotType.Value},
			wantDefault: slotType.Name,
			wantNumeric: strconv.FormatUint(uint64(slotType.Value), 10),
		},
		{name: "batch mode", op: mir.BatchMode{Value: 1}, wantDefault: "Sum", wantNumeric: "1"},
		{
			name:        "reagent mode",
			op:          mir.ReagentMode{Value: 3},
			wantDefault: "TotalContents",
			wantNumeric: "3",
		},
		{
			name:        "enum value with no name falls back to the number",
			op:          mir.BatchMode{Value: 200},
			wantDefault: "200",
			wantNumeric: "200",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := render.operand(tt.op)
			if err != nil {
				t.Fatalf("operand(%s): %v", tt.op, err)
			}
			if got != tt.wantDefault {
				t.Errorf("operand(%s) = %q, want %q", tt.op, got, tt.wantDefault)
			}
			render.numeric = true
			got, err = render.operand(tt.op)
			if err != nil {
				t.Fatalf("numeric operand(%s): %v", tt.op, err)
			}
			if got != tt.wantNumeric {
				t.Errorf("numeric operand(%s) = %q, want %q", tt.op, got, tt.wantNumeric)
			}
			render.numeric = false
		})
	}
}

func TestRenderOperandErrors(t *testing.T) {
	render := renderer{lineOf: map[string]int{"known": 1}}
	tests := []struct {
		name string
		op   mir.Operand
		want error
		// mention is text the message has to carry, for a rejection the
		// operand's own spelling does not show.
		mention string
	}{
		{name: "virtual register", op: mir.VirtReg{ID: 4}, want: ErrVirtualRegister},
		{name: "label naming no block", op: mir.Label{Name: "nowhere"}, want: ErrUnresolvedLabel},
		// The chip's operand pattern for a device admits d0 through d9 and a
		// housing has six pins, so d6 assembles and then faults once per tick.
		{name: "device pin the housing does not have", op: mir.Device{Kind: mir.DevicePin, Pin: 6}, want: ErrUnspellableOperand},
		{name: "device kind with no spelling", op: mir.Device{Kind: mir.DeviceKind(9)}, want: ErrUnspellableOperand},
		{name: "register outside the file", op: mir.PhysReg{Reg: ic10.NumRegisters}, want: ErrUnspellableOperand},
		{
			// db:n is a spelling the chip reads. What has no spelling is this
			// side, where Device.String writes "db" and drops the index, so the
			// message names the operand rather than the text, which would read
			// as db itself being the problem.
			name:    "the base device carrying a network connection",
			op:      mir.Device{Kind: mir.DeviceBase, Conn: 3},
			want:    ErrUnspellableOperand,
			mention: "db:3",
		},
		{
			// Device is a struct any stage can fill in. Refusing only the
			// indices above NoConnection would let this one through and emit a
			// bare db, the same silent substitution the row above rejects.
			name:    "the base device carrying a negative network connection",
			op:      mir.Device{Kind: mir.DeviceBase, Conn: mir.NoConnection - 1},
			want:    ErrUnspellableOperand,
			mention: "db:-2",
		},
		{
			// Past what a 32 bit parse reads, _GetNetworkIndex falls back to the
			// base network, so the operand reaches the device on the pin rather
			// than the one across the connection it names.
			name:    "a network connection wider than the chip reads",
			op:      mir.Device{Kind: mir.DevicePin, Pin: 0, Conn: maxParsedConnection + 1},
			want:    ErrUnspellableOperand,
			mention: "2147483648",
		},
		{
			name: "a negative network connection",
			op:   mir.Device{Kind: mir.DevicePin, Pin: 0, Conn: mir.NoConnection - 1},
			want: ErrUnspellableOperand,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := render.operand(tt.op)
			if !errors.Is(err, tt.want) {
				t.Fatalf("operand(%s) = %q, %v, want %v", tt.op, got, err, tt.want)
			}
			if tt.mention != "" && !strings.Contains(err.Error(), tt.mention) {
				t.Errorf("operand(%s) failed with %q, want it to mention %q", tt.op, err, tt.mention)
			}
		})
	}
}

// TestEnumOperandSpellsAValueTheChipParses covers the four enum operands against
// the parse that reads one no enum member names. EnumValuedVariable falls back to
// int.TryParse for such an operand, and that parse reads a leading sign and
// refuses anything past the 32 bit range.
func TestEnumOperandSpellsAValueTheChipParses(t *testing.T) {
	tests := []struct {
		name string
		op   mir.Operand
		want string
	}{
		// The widest each can hold, spelled out rather than read from ic10: an
		// expectation computed from the type under test accepts whatever width
		// that type happens to have, which is what a narrower conversion wraps.
		{name: "the widest logic type", op: mir.LogicType{Value: 65535}, want: "65535"},
		{name: "the widest slot type", op: mir.LogicSlotType{Value: 255}, want: "255"},
		{name: "the widest batch mode", op: mir.BatchMode{Value: 2147483647}, want: "2147483647"},
		{name: "the widest reagent mode", op: mir.ReagentMode{Value: 2147483647}, want: "2147483647"},
		// ic10 backs these with int32, so a negative widened to uint64 before
		// rendering spells its twenty digit complement: text the parse will not
		// read, leaving GetVariableValue to raise IncorrectVariable every tick.
		{name: "a negative batch mode", op: mir.BatchMode{Value: -1}, want: "-1"},
		{name: "a negative reagent mode", op: mir.ReagentMode{Value: -7}, want: "-7"},
		{name: "the most negative batch mode", op: mir.BatchMode{Value: -2147483648}, want: "-2147483648"},
		{name: "the most negative reagent mode", op: mir.ReagentMode{Value: -2147483648}, want: "-2147483648"},
	}
	render := renderer{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Both modes, since the name table is what the default form consults
			// and none of these values is in it.
			for _, numeric := range []bool{false, true} {
				render.numeric = numeric
				got, err := render.operand(tt.op)
				if err != nil {
					t.Fatalf("numeric=%v operand(%s): %v", numeric, tt.op, err)
				}
				if got != tt.want {
					t.Errorf("numeric=%v operand(%s) = %q, want %q", numeric, tt.op, got, tt.want)
				}
				// int.TryParse is a 32 bit parse, so a spelling Go reads back as
				// an int32 is one the chip reads the same. Checked here rather
				// than only against the pinned text, which a widening could
				// happen to agree with.
				parsed, err := strconv.ParseInt(got, 10, 32)
				if err != nil {
					t.Errorf("operand(%s) = %q, which no 32 bit parse reads: %v", tt.op, got, err)
				}
				if want, _ := strconv.ParseInt(tt.want, 10, 32); parsed != want {
					t.Errorf("operand(%s) = %q, which reads back as %d, want %d", tt.op, got, parsed, want)
				}
			}
		})
	}
}

// TestFormatImm pins the literal spellings and loads each one back through the
// interpreter.
func TestFormatImm(t *testing.T) {
	tests := []struct {
		name    string
		value   float64
		want    string
		wantErr error
	}{
		{name: "zero", value: 0, want: "0"},
		{name: "integer", value: 42, want: "42"},
		{name: "negative integer", value: -7, want: "-7"},
		{name: "fraction", value: 1.5, want: "1.5"},
		{name: "prefab hash", value: -1252983604, want: "-1252983604"},
		{name: "large integer stays decimal", value: 1e21, want: "1000000000000000000000"},
		{name: "small fraction stays decimal", value: 0.001, want: "0.001"},
		{name: "not a number has no literal", value: math.NaN(), wantErr: ErrUnmaterialisedLiteral},
		{name: "a NaN carrying a payload has none either", value: math.Float64frombits(0x7ff8000000000042), wantErr: ErrUnmaterialisedLiteral},
		{
			// Every decimal spelling of it loads +0.0 on the chip, "-0"
			// included, so what looks like a spelling is a different value.
			name:    "negative zero has no literal",
			value:   math.Copysign(0, -1),
			wantErr: ErrUnmaterialisedLiteral,
		},
		{name: "positive infinity", value: math.Inf(1), want: "pinf"},
		{name: "negative infinity", value: math.Inf(-1), want: "ninf"},
		{name: "pi", value: math.Pi, want: "pi"},
		{name: "tau", value: 2 * math.Pi, want: "tau"},
		{name: "gas constant", value: 8.31446261815324, want: "rgas"},
		{name: "smallest subnormal", value: 5e-324, want: "epsilon"},
		{name: "degrees to radians as the game stores it", value: 0.01745329238474369, want: "deg2rad"},
		{name: "degrees to radians at full precision is not the game's", value: math.Pi / 180, want: "0.017453292519943295"},
		{
			// Nothing bounds the expansion, and nothing can: shortening it needs
			// an exponent the chip's parser does not read, and rounding emits a
			// different value in silence. A literal no line holds is reported as
			// a line width violation, which is the loud failure of the two.
			name:  "a magnitude wider than any line still expands in full",
			value: 1e300,
			want:  "1" + strings.Repeat("0", 300),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := formatImm(tt.value)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("formatImm(%v) = %q, %v, want %v", tt.value, got, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("formatImm(%v): %v", tt.value, err)
			}
			if got != tt.want {
				t.Fatalf("formatImm(%v) = %q, want %q", tt.value, got, tt.want)
			}
			assertLoadsAs(t, got, tt.value)
			assertRoundTrips(t, got, tt.value)
		})
	}
}

// TestFormatImmWidensPastTheGate pins the width of the expansion against the
// digits the shortest one rests on, and holds the widening to being necessary as
// well as sufficient. The misread rows carry the value Mono reads the shortest
// expansion as; each was caught by a clean compile emitting one place along.
func TestFormatImmWidensPastTheGate(t *testing.T) {
	tests := []struct {
		name  string
		value float64
		want  string
		// misread is what the chip reads the shortest expansion as, for a value
		// where that differs from the value itself. Zero means the shortest
		// expansion was not measured to be misread, which is every row the gate
		// leaves alone and some it widens for exactness rather than for Mono.
		misread uint64
	}{
		{name: "eleven digits keep the shortest expansion", value: 1.2345678901, want: "1.2345678901"},
		{name: "eleven digits under one keep it as well", value: 0.00012345678901, want: "0.00012345678901"},
		{name: "twelve digits widen", value: 1.23456789012, want: "1.2345678901199999"},
		{name: "thirteen digits widen", value: 1.234567890123, want: "1.2345678901229999"},
		{name: "seventeen digits are already the full width", value: 1.2345678901234567, want: "1.2345678901234567"},
		{name: "a widened magnitude past the last fractional digit pads", value: 1.234567890123e18, want: "1234567890123000100"},
		{name: "a round number rests on one digit and is left alone", value: 1e11, want: "100000000000"},
		{name: "misread as c0de6137aa3c8efc", value: math.Float64frombits(0xc0de6137aa3c8efb), want: "-31108.869765414838", misread: 0xc0de6137aa3c8efc},
		{name: "misread as c154347f83334cdf", value: math.Float64frombits(0xc154347f83334cde), want: "-5296638.0500061195", misread: 0xc154347f83334cdf},
		{name: "misread as 41eca1819d90d448", value: math.Float64frombits(0x41eca1819d90d447), want: "3842772204.5259128", misread: 0x41eca1819d90d448},
		{name: "misread as bfb360175e56c759", value: math.Float64frombits(0xbfb360175e56c758), want: "-0.075684986621835093", misread: 0xbfb360175e56c759},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := formatImm(tt.value)
			if err != nil {
				t.Fatalf("formatImm(%v): %v", tt.value, err)
			}
			if got != tt.want {
				t.Fatalf("formatImm(%#016x) = %q, want %q", math.Float64bits(tt.value), got, tt.want)
			}
			assertLoadsAs(t, got, tt.value)
			assertRoundTrips(t, got, tt.value)
			if tt.misread == 0 {
				return
			}
			shortest := strconv.FormatFloat(tt.value, 'f', -1, 64)
			if shortest == tt.want {
				t.Fatalf("the shortest expansion of %#016x is %q, which is what the emitter produced: "+
					"the row claims a misreading the gate does not widen away",
					math.Float64bits(tt.value), shortest)
			}
			if bits := math.Float64bits(loadsAs(t, shortest)); bits != tt.misread {
				t.Errorf("the chip reads the shortest expansion %q as %#016x, want the misreading %#016x "+
					"this row is named for", shortest, bits, tt.misread)
			}
		})
	}
}

// TestFormatImmKeepsAnExactExpansion covers the second half of the gate: a
// shortest expansion naming its value outright is kept however wide it is. The
// values are what the corpus moved on when width alone decided it — masks and
// bounds, powers of two, every one spelled whole by the shortest expansion.
func TestFormatImmKeepsAnExactExpansion(t *testing.T) {
	tests := []struct {
		name  string
		value float64
		want  string
	}{
		{name: "two to the fortieth", value: 1099511627776, want: "1099511627776"},
		{name: "two to the forty-eighth", value: 281474976710656, want: "281474976710656"},
		{name: "the widest exact integer", value: 9007199254740992, want: "9007199254740992"},
		{name: "two under it", value: 9007199254740990, want: "9007199254740990"},
		{name: "a half at the gate's width", value: 12345678901.5, want: "12345678901.5"},
		{name: "a quarter past it", value: 123456789012.25, want: "123456789012.25"},
		// The contrast: the same digit count, not exact, so they widen. Without
		// them the table would pass on a gate that had stopped widening at all.
		{name: "an inexact decimal of the same width widens", value: 1.234567890123, want: "1.2345678901229999"},
		{name: "an inexact integer past 2^53 widens", value: 1.234567890123e18, want: "1234567890123000100"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := formatImm(tt.value)
			if err != nil {
				t.Fatalf("formatImm(%v): %v", tt.value, err)
			}
			if got != tt.want {
				t.Fatalf("formatImm(%#016x) = %q, want %q", math.Float64bits(tt.value), got, tt.want)
			}
			assertLoadsAs(t, got, tt.value)
			assertRoundTrips(t, got, tt.value)
		})
	}
}

// TestSpellsExactly covers the reading [formatImm] keeps an expansion on. A row
// states the text rather than formatting the value, so it can name a spelling the
// formatter would not produce: a truncation, and a needless widening.
func TestSpellsExactly(t *testing.T) {
	tests := []struct {
		name  string
		value float64
		text  string
		want  bool
	}{
		{name: "an integer is its own expansion", value: 9007199254740992, text: "9007199254740992", want: true},
		{name: "a dyadic fraction is too", value: 0.25, text: "0.25", want: true},
		{name: "trailing zeros do not change the rational", value: 0.25, text: "0.250000", want: true},
		{name: "a truncation is not exact", value: 0.25, text: "0.2", want: false},
		{name: "a shortest expansion of a repeating value is not exact", value: 0.1, text: "0.1", want: false},
		{name: "the full expansion of that value is", value: 0.1, text: "0.1000000000000000055511151231257827021181583404541015625", want: true},
		{name: "a widened expansion of an exact value stays exact", value: 1.5, text: "1.5000000000000000", want: true},
		{name: "text that is not a decimal at all", value: 1.5, text: "pi", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := spellsExactly(tt.value, tt.text); got != tt.want {
				t.Errorf("spellsExactly(%v, %q) = %v, want %v", tt.value, tt.text, got, tt.want)
			}
		})
	}
}

// TestSignificantDigits covers the count [formatImm] gates on. The trailing zero
// rows are the ones worth pinning: a count taken off the fixed expansion would
// widen every round number in the corpus.
func TestSignificantDigits(t *testing.T) {
	tests := []struct {
		name  string
		value float64
		want  int
	}{
		{name: "zero", value: 0, want: 1},
		{name: "one", value: 1, want: 1},
		{name: "a magnitude written as trailing zeros", value: 1e11, want: 1},
		{name: "a magnitude written as leading zeros", value: 1e-11, want: 1},
		{name: "a sign does not count", value: -42, want: 2},
		{name: "eleven", value: 1.2345678901, want: 11},
		{name: "twelve", value: 1.23456789012, want: 12},
		{name: "thirteen", value: 1.234567890123, want: 13},
		{name: "seventeen", value: 1.2345678901234567, want: 17},
		{name: "the widest double", value: math.MaxFloat64, want: 17},
		{name: "the narrowest subnormal", value: 5e-324, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := significantDigits(tt.value); got != tt.want {
				t.Errorf("significantDigits(%v) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}

// TestFixedNotation covers the three shapes the point can land in: inside the
// digits, past their end with zeros making up the magnitude, and before their
// start with zeros doing the same. The digit count is a parameter so a row can
// state a width small enough to read, which the shapes are the same at.
func TestFixedNotation(t *testing.T) {
	tests := []struct {
		name   string
		value  float64
		digits int
		want   string
	}{
		{name: "point inside the digits", value: 1.5, digits: 4, want: "1.500"},
		{name: "point after the digits", value: 1500, digits: 2, want: "1500"},
		{name: "point before the digits", value: 0.015, digits: 2, want: "0.015"},
		{name: "negative", value: -1.5, digits: 4, want: "-1.500"},
		{name: "zero", value: 0, digits: 3, want: "0.00"},
		{name: "rounding crosses into a wider magnitude", value: 96, digits: 1, want: "100"},
		{name: "rounding crosses into a narrower one", value: 1e88, digits: 17, want: "9999999999999999600000000000000000000000000000000000000000000000000000000000000000000000"},
		{name: "the full width past the last fractional digit", value: 1.234567890123e18, digits: immFullDigits, want: "1234567890123000100"},
		{name: "the full width under one", value: math.Float64frombits(0xbfb360175e56c758), digits: immFullDigits, want: "-0.075684986621835093"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fixedNotation(tt.value, tt.digits)
			if err != nil {
				t.Fatalf("fixedNotation(%v, %d): %v", tt.value, tt.digits, err)
			}
			if got != tt.want {
				t.Errorf("fixedNotation(%v, %d) = %q, want %q", tt.value, tt.digits, got, tt.want)
			}
		})
	}
}

// TestFormatImmWidensAcrossTheGate sweeps the width of the shortest expansion
// across the gate. Each row builds literals resting on exactly that many digits
// rather than drawing doubles at random: a random double rests on seventeen
// almost every time, and every row would pass whether the widening happened or not.
func TestFormatImmWidensAcrossTheGate(t *testing.T) {
	tests := []struct {
		name   string
		digits int
		widens bool
	}{
		{name: "ten digits keep the shortest expansion", digits: 10},
		{name: "eleven digits keep it", digits: 11},
		{name: "twelve digits widen", digits: 12, widens: true},
		{name: "thirteen digits widen", digits: 13, widens: true},
		{name: "fifteen digits widen", digits: 15, widens: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			random := rand.New(rand.NewPCG(0x1c11c, uint64(tt.digits)))
			// The magnitudes sweep the point through the digits and off both
			// ends, since [fixedNotation] reaches each through a different arm.
			// They stop under 1e17, past which the padding zeros cannot be told
			// from significant ones by any reading of the text.
			for integral := range tt.digits {
				for leadingZeros := range 3 {
					if integral > 0 && leadingZeros > 0 {
						continue
					}
					for range 200 {
						text := decimalText(random, tt.digits, integral, leadingZeros)
						value, err := strconv.ParseFloat(text, 64)
						if err != nil {
							t.Fatalf("ParseFloat(%q): %v", text, err)
						}
						if got := significantDigits(value); got != tt.digits {
							t.Fatalf("%q rests on %d digits, want %d", text, got, tt.digits)
						}
						got, err := formatImm(value)
						if err != nil {
							t.Fatalf("formatImm(%q): %v", text, err)
						}
						if strings.ContainsAny(got, "eE") {
							t.Fatalf("formatImm(%q) = %q, which the chip's parser reads as no value at all", text, got)
						}
						switch {
						case tt.widens:
							if digits := writtenDigits(got); digits != immFullDigits {
								t.Fatalf("formatImm(%q) = %q, written across %d digits, want %d", text, got, digits, immFullDigits)
							}
						case got != text:
							t.Fatalf("formatImm(%q) = %q, want the shortest expansion it was built as", text, got)
						}
						assertRoundTrips(t, got, value)
					}
				}
			}
		})
	}
}

// decimalText builds a literal resting on exactly digits significant digits, with
// integral of them before the point and leadingZeros zeros standing between the
// point and the first of the rest. integral must be under digits, so the literal
// always carries a fractional part.
func decimalText(random *rand.Rand, digits, integral, leadingZeros int) string {
	const trailing = "12346789"
	significant := make([]byte, digits)
	for i := range significant {
		significant[i] = byte('0' + random.IntN(10))
	}
	// The leading digit is nonzero so the count is the one asked for. The
	// trailing one avoids {0, 5}: the text denotes n/10^f, which is dyadic only
	// when 5^f divides n, and [formatImm] keeps an exact expansion whatever its
	// width — a literal landing on one would put a row here that widens nothing.
	significant[0] = byte('1' + random.IntN(9))
	significant[digits-1] = trailing[random.IntN(len(trailing))]
	sign := strings.Repeat("-", random.IntN(2))
	if integral == 0 {
		return sign + "0." + strings.Repeat("0", leadingZeros) + string(significant)
	}
	return sign + string(significant[:integral]) + "." + string(significant[integral:])
}

// writtenDigits counts the significant digits a decimal expansion is written
// across, reading only the text — the check's own reading rather than
// [significantDigits], which asks strconv the same question the formatter asked
// and would agree with it about a wrong answer. A trailing zero counts.
func writtenDigits(text string) int {
	return len(strings.TrimLeft(strings.Replace(strings.TrimPrefix(text, "-"), ".", "", 1), "0"))
}

// TestShortestSpellingPrefersTheCheaperForm covers the choice [formatImm] makes
// once it has the expansion in hand. The table is synthetic because the shipped
// one cannot exercise it: every name ic10.Constants carries is shorter than its
// expansion, and that list is generated, so a longer name could arrive later.
func TestShortestSpellingPrefersTheCheaperForm(t *testing.T) {
	const value = 12.5
	bits := math.Float64bits(value)
	tests := []struct {
		name  string
		names map[uint64]string
		want  string
	}{
		{name: "no name for the value leaves the expansion", names: map[uint64]string{}, want: "12.5"},
		{name: "a shorter name wins", names: map[uint64]string{bits: "hf"}, want: "hf"},
		{
			// Both cost four characters, so nothing is saved and the expansion
			// is the spelling that resolves without the constant table.
			name:  "a name of equal length loses",
			names: map[uint64]string{bits: "half"},
			want:  "12.5",
		},
		{
			name:  "a longer name loses",
			names: map[uint64]string{bits: "twelveandahalf"},
			want:  "12.5",
		},
		{
			name:  "a name for a different value is not reached",
			names: map[uint64]string{math.Float64bits(13): "x"},
			want:  "12.5",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shortestSpelling(tt.names, value, "12.5"); got != tt.want {
				t.Errorf("shortestSpelling(%v) = %q, want %q", tt.names, got, tt.want)
			}
		})
	}
}

// TestBuildConstantNamesKeepsTheCheapestSpelling covers two names for one value,
// which ic10.Constants does not hold: its nine entries have nine distinct values
// once the NaN and the two infinities are out. The dedup is over generated data,
// so an alias in a later version of the game is what it is there for.
func TestBuildConstantNamesKeepsTheCheapestSpelling(t *testing.T) {
	tests := []struct {
		name      string
		constants []ic10.Constant
		want      map[uint64]string
	}{
		{
			name:      "the only name for a value is taken",
			constants: []ic10.Constant{{Name: "one", Value: 1}},
			want:      map[uint64]string{math.Float64bits(1): "one"},
		},
		{
			name:      "a longer alias does not displace the name already held",
			constants: []ic10.Constant{{Name: "one", Value: 1}, {Name: "unity", Value: 1}},
			want:      map[uint64]string{math.Float64bits(1): "one"},
		},
		{
			name:      "a shorter alias displaces it",
			constants: []ic10.Constant{{Name: "unity", Value: 1}, {Name: "one", Value: 1}},
			want:      map[uint64]string{math.Float64bits(1): "one"},
		},
		{
			// Neither is cheaper, so the first is kept and the answer does not
			// depend on the order the table happens to list them in.
			name:      "an alias of equal length leaves the first in place",
			constants: []ic10.Constant{{Name: "one", Value: 1}, {Name: "uno", Value: 1}},
			want:      map[uint64]string{math.Float64bits(1): "one"},
		},
		{
			// formatImm answers all three before it reaches the table, and a NaN
			// has no single bit pattern to key on in any case.
			name: "the unspellable values are left out",
			constants: []ic10.Constant{
				{Name: "nan", Value: math.NaN()},
				{Name: "pinf", Value: math.Inf(1)},
				{Name: "ninf", Value: math.Inf(-1)},
			},
			want: map[uint64]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildConstantNames(tt.constants)
			if !maps.Equal(got, tt.want) {
				t.Errorf("buildConstantNames = %v, want %v", got, tt.want)
			}
		})
	}
}

// namedValue is a synthetic enum entry, standing in for the four generated
// tables [reverseIndex] is instantiated over.
type namedValue struct {
	name       string
	value      uint64
	deprecated bool
}

// TestReverseIndexPrefersALiveSpelling covers the choice between two names for
// one value, which none of the four generated tables holds: all 358 logic types,
// 33 slot types, 5 batch modes and 4 reagent modes have distinct values. The
// deprecated flag is live data even so.
func TestReverseIndexPrefersALiveSpelling(t *testing.T) {
	tests := []struct {
		name    string
		entries []namedValue
		want    map[uint64]string
	}{
		{
			name:    "one spelling is taken whether or not it is deprecated",
			entries: []namedValue{{name: "Live", value: 1}, {name: "Gone", value: 2, deprecated: true}},
			want:    map[uint64]string{1: "Live", 2: "Gone"},
		},
		{
			name:    "a deprecated alias does not displace a live name",
			entries: []namedValue{{name: "Live", value: 1}, {name: "Gone", value: 1, deprecated: true}},
			want:    map[uint64]string{1: "Live"},
		},
		{
			name:    "a live name displaces a deprecated one already held",
			entries: []namedValue{{name: "Gone", value: 1, deprecated: true}, {name: "Live", value: 1}},
			want:    map[uint64]string{1: "Live"},
		},
		{
			// Either resolves, so the answer must not depend on where in the
			// table the second one sits.
			name:    "the first of two live names is kept",
			entries: []namedValue{{name: "Live", value: 1}, {name: "Also", value: 1}},
			want:    map[uint64]string{1: "Live"},
		},
		{
			name:    "the last of several deprecated names does not displace the first",
			entries: []namedValue{{name: "Gone", value: 1, deprecated: true}, {name: "Also", value: 1, deprecated: true}},
			want:    map[uint64]string{1: "Gone"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reverseIndex(tt.entries, func(e namedValue) (uint64, string, bool) {
				return e.value, e.name, e.deprecated
			})
			if !maps.Equal(got, tt.want) {
				t.Errorf("reverseIndex = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDeprecatedEnumNameIsStillPrinted covers what the emitter does with the 23
// deprecated logic types. Each holds a value no live name spells, and the game
// resolves a deprecated member and runs it, so the name is both the only thing to
// print and a correct thing to print.
func TestDeprecatedEnumNameIsStillPrinted(t *testing.T) {
	var deprecated []ic10.LogicTypeInfo
	for _, info := range ic10.LogicTypes {
		if info.Deprecated {
			deprecated = append(deprecated, info)
		}
	}
	if len(deprecated) == 0 {
		t.Skip("no deprecated logic type in the shipped table")
	}
	var render renderer
	for _, info := range deprecated {
		t.Run(info.Name, func(t *testing.T) {
			got, err := render.operand(mir.LogicType{Value: info.Value})
			if err != nil {
				t.Fatalf("operand(%s): %v", info.Name, err)
			}
			if got != info.Name {
				t.Errorf("operand(%s) = %q, want the deprecated name %q", info.Name, got, info.Name)
			}
		})
	}
}

// assertRoundTrips checks a decimal spelling against Go's own parser. It is
// weaker than [assertLoadsAs] and is kept for one direction: Go is correctly
// rounded, so a spelling reading back as a different value here names its value
// badly whatever Mono does. A named constant parses as nothing and is skipped.
func assertRoundTrips(t *testing.T, text string, want float64) {
	t.Helper()
	got, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return
	}
	if math.Float64bits(got) != math.Float64bits(want) {
		t.Errorf("%q parses as %v (%#016x), want %v (%#016x)",
			text, got, math.Float64bits(got), want, math.Float64bits(want))
	}
}

// assertLoadsAs fails unless the game's own chip reads text back as the value it
// was formatted from, bit for bit. The chip is the oracle rather than strconv
// because Mono's double.Parse is not correctly rounded: the two spellings
// [immFullDigits] separates are one value to every parser in Go.
func assertLoadsAs(t *testing.T, text string, want float64) {
	t.Helper()
	// Bits rather than values: -0 and 0 compare equal as doubles, and which of
	// the two the formatter was asked for is the whole question for that row.
	if got := loadsAs(t, text); math.Float64bits(got) != math.Float64bits(want) {
		t.Errorf("%q loads as %v (%#016x), want %v (%#016x)",
			text, got, math.Float64bits(got), want, math.Float64bits(want))
	}
}

// loadsAs returns the value the chip reads text as when it stands where an
// instruction wants a number. A spelling the assembler refuses is reported as the
// compile error it is, rather than read as whatever r0 happened to hold.
func loadsAs(t *testing.T, text string) float64 {
	t.Helper()
	ctx, harness := chiptest.Harness(t)
	source := "move r0 " + text
	got, err := harness.Run(ctx, chip.Request{Source: source})
	if err != nil {
		t.Fatalf("running %q: %v", source, err)
	}
	if got.Stop != chip.StopEnded {
		t.Fatalf("running %q stopped %q (compile %v, fault %v), want it to run off its end",
			source, got.Stop, got.CompileError, got.Fault)
	}
	return got.Registers[0]
}

func TestRenderInstr(t *testing.T) {
	render := renderer{lineOf: map[string]int{"loop": 9}}
	tests := []struct {
		name string
		op   ic10.Opcode
		args []mir.Operand
		want string
	}{
		{name: "no operands", op: isa.OpYield, want: "yield"},
		{name: "three operands", op: isa.OpAdd, args: []mir.Operand{mir.PhysReg{Reg: 0}, mir.PhysReg{Reg: 1}, mir.Imm{Value: 2}}, want: "add r0 r1 2"},
		{name: "branch to a label", op: isa.OpJ, args: []mir.Operand{mir.Label{Name: "loop"}}, want: "j 9"},
		{name: "return through ra", op: isa.OpJ, args: []mir.Operand{mir.PhysReg{Reg: ic10.RegRA}}, want: "j ra"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instr, err := mir.NewInstr(tt.op, position(1), tt.args...)
			if err != nil {
				t.Fatalf("NewInstr: %v", err)
			}
			got, err := render.instr(instr)
			if err != nil {
				t.Fatalf("instr: %v", err)
			}
			if got != tt.want {
				t.Errorf("instr = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestAnnotate pins the comment's shape, which is the whole of what readable
// output adds. The instruction text in front of it is never touched: it is what
// the chip reads once it has cut the line at the '#'.
func TestAnnotate(t *testing.T) {
	const lines = 20
	render := renderer{
		lineOf: map[string]int{"loop": 9, "done": 12, "unmangled": 3, "return": lines, "wide": 4},
		names: map[string]string{
			"loop":   "main_loop",
			"done":   "main_done",
			"return": "main_return",
			"wide":   strings.Repeat("w", MaxLineLength),
		},
		count: lines,
	}
	tests := []struct {
		name     string
		starting []string
		op       ic10.Opcode
		args     []mir.Operand
		want     string
		wantErr  error
	}{
		{
			// A '#' with nothing after it would spend two bytes and two
			// characters of the line width on nothing.
			name: "an instruction with nothing to say keeps its bare text",
			op:   isa.OpYield,
			want: "yield",
		},
		{
			name:     "a block starting here",
			starting: []string{"main_loop"},
			op:       isa.OpYield,
			want:     "yield # main_loop:",
		},
		{
			name:     "several blocks resolving to one line",
			starting: []string{"main_empty", "main_loop"},
			op:       isa.OpYield,
			want:     "yield # main_empty: main_loop:",
		},
		{
			name: "a branch names the block its line number reaches",
			op:   isa.OpJ,
			args: []mir.Operand{mir.Label{Name: "loop"}},
			want: "j 9 # -> main_loop",
		},
		{
			name: "a conditional branch annotates past its other operands",
			op:   isa.OpBeq,
			args: []mir.Operand{mir.PhysReg{Reg: 0}, mir.Imm{Value: 1}, mir.Label{Name: "done"}},
			want: "beq r0 1 12 # -> main_done",
		},
		{
			name:     "a block whose first instruction branches",
			starting: []string{"main_loop"},
			op:       isa.OpJ,
			args:     []mir.Operand{mir.Label{Name: "loop"}},
			want:     "j 9 # main_loop: -> main_loop",
		},
		{
			// The block holds no instruction and starts one past the last line,
			// so no line carries its name and a branch there stops the chip.
			name: "a branch past the last line is marked as the end",
			op:   isa.OpJ,
			args: []mir.Operand{mir.Label{Name: "return"}},
			want: "j 20 # -> main_return (end)",
		},
		{
			// The annotation is never trimmed to fit the line width. The editor
			// cuts the line on paste and the chip cuts it at the '#' before
			// that, so what a cut takes is comment text; a name cut here would
			// name the wrong block in the one form whose point is the names.
			name: "a name wider than the line limit is not trimmed",
			op:   isa.OpJ,
			args: []mir.Operand{mir.Label{Name: "wide"}},
			want: "j 4 # -> " + strings.Repeat("w", MaxLineLength),
		},
		{
			// The name table and the line table are built from one block list,
			// so a target only the line table holds is a renderer handed a
			// program neither was built from.
			name:    "a target the name table has no entry for",
			op:      isa.OpJ,
			args:    []mir.Operand{mir.Label{Name: "unmangled"}},
			wantErr: ErrUnresolvedLabel,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, err := mir.NewInstr(tt.op, position(1), tt.args...)
			if err != nil {
				t.Fatalf("NewInstr: %v", err)
			}
			text, err := render.instr(in)
			if err != nil {
				t.Fatalf("instr: %v", err)
			}
			got, err := render.annotate(text, tt.starting, in)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("annotate = %q, %v, want %v", got, err, tt.wantErr)
				}
				if got != "" {
					t.Errorf("annotate returned %q alongside its error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("annotate: %v", err)
			}
			if got != tt.want {
				t.Errorf("annotate = %q, want %q", got, tt.want)
			}
			if !strings.HasPrefix(got, text) {
				t.Errorf("annotate = %q, which does not start with the instruction %q", got, text)
			}
		})
	}
}
