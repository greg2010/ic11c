package chip

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
)

func TestBitsRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		value float64
		want  string
	}{
		{name: "zero", value: 0, want: "0x0000000000000000"},
		{name: "negative zero", value: math.Copysign(0, -1), want: "0x8000000000000000"},
		{name: "one", value: 1, want: "0x3ff0000000000000"},
		{name: "minus one", value: -1, want: "0xbff0000000000000"},
		{name: "pi", value: math.Pi, want: "0x400921fb54442d18"},
		{name: "smallest denormal", value: math.SmallestNonzeroFloat64, want: "0x0000000000000001"},
		{name: "largest", value: math.MaxFloat64, want: "0x7fefffffffffffff"},
		{name: "positive infinity", value: math.Inf(1), want: "0x7ff0000000000000"},
		{name: "negative infinity", value: math.Inf(-1), want: "0xfff0000000000000"},
		{name: "quiet nan", value: math.NaN(), want: "0x7ff8000000000001"},
		{
			name:  "a nan carrying a payload",
			value: math.Float64frombits(0xfff8000000dead01),
			want:  "0xfff8000000dead01",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatBits(tt.value); got != tt.want {
				t.Fatalf("formatBits(%v) = %q, want %q", tt.value, got, tt.want)
			}
			got, err := parseBits(tt.want)
			if err != nil {
				t.Fatalf("parseBits(%q): %v", tt.want, err)
			}
			if math.Float64bits(got) != math.Float64bits(tt.value) {
				t.Errorf("parseBits(%q) = %016x, want %016x",
					tt.want, math.Float64bits(got), math.Float64bits(tt.value))
			}
		})
	}
}

// TestParseBitsRefusesAnythingElse holds the parser to the one spelling the
// harness writes. Every token here would be read as a value by a laxer parser,
// and a value read from a token the far side cannot have written is worse than
// a refusal: it is a number nobody can trace.
func TestParseBitsRefusesAnythingElse(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "empty", text: ""},
		{name: "no prefix", text: "3ff0000000000000"},
		{name: "one digit short", text: "0x3ff000000000000"},
		{name: "one digit long", text: "0x3ff00000000000000"},
		{name: "upper case", text: "0x3FF0000000000000"},
		{name: "an upper case digit among lower case ones", text: "0x3ff000000000000A"},
		{name: "a sign", text: "0x-ff0000000000000"},
		{name: "a leading sign and a short body", text: "-0x3ff0000000000000"},
		{name: "surrounding space", text: " 0x3ff0000000000000"},
		{name: "a decimal", text: "1.5"},
		{name: "the old infinity spelling", text: "Infinity"},
		{name: "the old nan spelling", text: "NaN"},
		{name: "a second prefix", text: "0x0x00000000000000"},
		{name: "an underscore", text: "0x3ff0000_00000000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := parseBits(tt.text); err == nil {
				t.Errorf("parseBits(%q) = %016x, want a refusal", tt.text, math.Float64bits(got))
			}
		})
	}
}

func TestParseExceptionTypeCoversTheTaxonomy(t *testing.T) {
	for want := ExcNone; want <= ExcAliasNotFound; want++ {
		got, err := parseExceptionType(want.String())
		if err != nil {
			t.Fatalf("parseExceptionType(%q): %v", want, err)
		}
		if got != want {
			t.Errorf("parseExceptionType(%q) = %v, want %v", want, got, want)
		}
	}
	if _, err := parseExceptionType("NoSuchException"); err == nil {
		t.Error("parseExceptionType accepted a name the chip cannot produce")
	}
}

func TestEveryExceptionTypeIsNamed(t *testing.T) {
	for e := ExcNone; e <= ExcAliasNotFound; e++ {
		if s := e.String(); strings.Contains(s, "ExceptionType(") {
			t.Errorf("exception type %d has no name, which the round trip above resolves anyway "+
				"because buildExceptionTypes keys the map on this same rendering", e)
		}
	}
	if s := (ExcAliasNotFound + 1).String(); !strings.Contains(s, "ExceptionType(") {
		t.Errorf("ExceptionType(%d) is named %q, so the loop above stops short of the last type", ExcAliasNotFound+1, s)
	}
}

// zeroRegs is a regs line holding eighteen positive zeros.
func zeroRegs() string {
	return "regs " + strings.TrimSpace(strings.Repeat(formatBits(0)+" ", ic10.NumRegisters))
}

func TestParseSnapshot(t *testing.T) {
	complete := []string{zeroRegs(), "ip 3", "lines 7", "err None 0", "cerr None 0", "housing 0"}
	with := func(extra ...string) []string {
		return append(append([]string(nil), complete...), extra...)
	}

	tests := []struct {
		name     string
		lines    []string
		fixtures bool
		check    func(t *testing.T, got Snapshot)
		wants    string
	}{
		{
			name:  "complete block",
			lines: with("stack 4 " + formatBits(-1.5)),
			check: func(t *testing.T, got Snapshot) {
				t.Helper()
				if got.Address != 3 || got.LineCount != 7 {
					t.Errorf("ip/lines = %d/%d, want 3/7", got.Address, got.LineCount)
				}
				if got.Stack[4] != -1.5 {
					t.Errorf("stack[4] = %v, want -1.5", got.Stack[4])
				}
			},
		},
		{
			name:  "a negative zero in a slot is a value, not an omission",
			lines: with("stack 4 " + formatBits(math.Copysign(0, -1))),
			check: func(t *testing.T, got Snapshot) {
				t.Helper()
				if bits := math.Float64bits(got.Stack[4]); bits != 1<<63 {
					t.Errorf("stack[4] = %016x, want 8000000000000000", bits)
				}
			},
		},
		{
			name: "faults carry their line",
			lines: []string{zeroRegs(), "ip 2", "lines 9", "err StackUnderFlow 2",
				"cerr DuplicateNothing 0", "housing 1"},
			wants: "no chip exception is named",
		},
		{
			name:  "a missing key is refused",
			lines: []string{zeroRegs(), "ip 2", "lines 9", "err None 0", "housing 0"},
			wants: `state block has no "cerr" line`,
		},
		{
			name:  "an unknown key is refused",
			lines: with("instructions 12"),
			wants: `unknown state key "instructions"`,
		},
		{
			name:  "a faithful reader refuses a permissive block",
			lines: with("fixtures 3"),
			wants: `unknown state key "fixtures"`,
		},
		{
			name:     "a permissive reader requires the write count",
			lines:    with(),
			fixtures: true,
			wants:    `state block has no "fixtures" line`,
		},
		{
			name:     "and reads it",
			lines:    with("fixtures 3"),
			fixtures: true,
			check: func(t *testing.T, got Snapshot) {
				t.Helper()
				if got.FixtureWrites != 3 {
					t.Errorf("fixture writes = %d, want 3", got.FixtureWrites)
				}
			},
		},
		{
			name:  "a short register file is refused",
			lines: []string{"regs " + formatBits(0), "ip 0", "lines 0", "err None 0", "cerr None 0", "housing 0"},
			wants: "reported 1 registers",
		},
		{
			name:  "a stack address off the array is refused",
			lines: with("stack 512 " + formatBits(1)),
			wants: "outside the 512 slot array",
		},
		{
			name:  "a decimal where a bit pattern belongs is refused",
			lines: with("stack 4 -1.5"),
			wants: "lower case hexadecimal digits",
		},
		{
			// The last line would otherwise win, and the register file the
			// caller read would be one no chip ever held.
			name:  "a key twice over is refused",
			lines: with(zeroRegs()),
			wants: `state block has two "regs" lines`,
		},
		{
			name:  "one slot reported twice is refused",
			lines: with("stack 4 "+formatBits(1), "stack 4 "+formatBits(2)),
			wants: `state block has two "stack 4" lines`,
		},
		{
			name:  "two different slots are not a repeat",
			lines: with("stack 4 "+formatBits(1), "stack 5 "+formatBits(2)),
			check: func(t *testing.T, got Snapshot) {
				t.Helper()
				if got.Stack[4] != 1 || got.Stack[5] != 2 {
					t.Errorf("stack[4]/stack[5] = %v/%v, want 1/2", got.Stack[4], got.Stack[5])
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSnapshot(tt.lines, tt.fixtures)
			if tt.wants != "" {
				if err == nil {
					t.Fatalf("parseSnapshot accepted the block, want an error mentioning %q", tt.wants)
				}
				if !strings.Contains(err.Error(), tt.wants) {
					t.Fatalf("parseSnapshot error = %q, want it to mention %q", err, tt.wants)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSnapshot: %v", err)
			}
			tt.check(t, got)
		})
	}
}

// TestParseRunTo holds the reply to the one shape the harness writes. Every
// refused line here would otherwise be read as a run that ended some way,
// which is a verdict about a program nobody can trace back to a chip.
func TestParseRunTo(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantReason StopReason
		wantTicks  int
		wants      string
	}{
		{name: "a program that ended", line: "ok ended 1", wantReason: StopEnded, wantTicks: 1},
		{name: "a fault", line: "ok faulted 12", wantReason: StopFaulted, wantTicks: 12},
		{name: "a program that did not compile", line: "ok compile_error 1", wantReason: StopCompileError, wantTicks: 1},
		{name: "a yield", line: "ok suspended 1024", wantReason: StopSuspended, wantTicks: 1024},
		{name: "a spent budget", line: "ok budget 1024", wantReason: StopBudget, wantTicks: 1024},
		{name: "a refusal", line: "err ArgumentException: runto takes", wants: `want "ok"`},
		{name: "a bare ok", line: "ok", wants: `want "ok"`},
		{name: "no tick count", line: "ok ended", wants: "want a stop reason and a tick count"},
		{name: "a third word", line: "ok ended 1 0", wants: "want a stop reason and a tick count"},
		{
			name:  "the run's own ending, which no tick has",
			line:  "ok tick_budget 1024",
			wants: `ended a tick "tick_budget"`,
		},
		{name: "an ending nothing names", line: "ok stopped 1", wants: `ended a tick "stopped"`},
		{name: "the flag the run verb answers with", line: "ok 0 1", wants: `ended a tick "0"`},
		{name: "a tick count that is not a number", line: "ok ended many", wants: `tick count "many"`},
		{name: "a tick count with a fraction", line: "ok ended 1.0", wants: `tick count "1.0"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, ticks, err := parseRunTo(tt.line)
			if tt.wants != "" {
				if err == nil {
					t.Fatalf("parseRunTo(%q) = %q, %d, want an error mentioning %q",
						tt.line, reason, ticks, tt.wants)
				}
				if !strings.Contains(err.Error(), tt.wants) {
					t.Fatalf("parseRunTo(%q) error = %q, want it to mention %q", tt.line, err, tt.wants)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseRunTo(%q): %v", tt.line, err)
			}
			if reason != tt.wantReason || ticks != tt.wantTicks {
				t.Errorf("parseRunTo(%q) = %q, %d, want %q, %d",
					tt.line, reason, ticks, tt.wantReason, tt.wantTicks)
			}
		})
	}
}

func TestParseWrite(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		want  Write
		wants string
	}{
		{
			name: "a device write",
			line: "w 2 12 " + formatBits(42),
			want: Write{Pin: 2, Slot: NoSlot, Property: 12, Value: 42},
		},
		{
			name: "a slot write",
			line: "ws 2 1 5 " + formatBits(-0.5),
			want: Write{Pin: 2, Slot: 1, Property: 5, Value: -0.5},
		},
		{
			name: "a write of a negative zero",
			line: "w 0 3 " + formatBits(math.Copysign(0, -1)),
			want: Write{Pin: 0, Slot: NoSlot, Property: 3, Value: math.Copysign(0, -1)},
		},
		{
			name:  "an unknown key",
			line:  "wx 0 3 " + formatBits(1),
			wants: `unknown trace key "wx"`,
		},
		{
			name:  "a device write missing its value",
			line:  "w 0 3",
			wants: "a device write wants",
		},
		{
			name:  "a slot write with a device write's arity",
			line:  "ws 0 3 " + formatBits(1),
			wants: "a slot write wants",
		},
		{
			name:  "a decimal value",
			line:  "w 0 3 1.5",
			wants: "lower case hexadecimal digits",
		},
		{
			name:  "a pin the housing does not have",
			line:  "w " + strconv.Itoa(ic10.NumDevicePins) + " 3 " + formatBits(1),
			wants: "and the housing has",
		},
		{
			name:  "a negative pin",
			line:  "w -1 3 " + formatBits(1),
			wants: "and the housing has",
		},
		{
			// A slot write carrying -1 is not a device write spelled the other
			// way: the two arities are distinct on the wire, and a negative slot
			// index is a place no device has.
			name:  "a negative slot on a slot write",
			line:  "ws 0 -1 3 " + formatBits(1),
			wants: "a slot index counts from zero",
		},
		{
			name:  "a negative property ordinal",
			line:  "w 0 -3 " + formatBits(1),
			wants: "no property has a negative ordinal",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseWrite(tt.line)
			if tt.wants != "" {
				if err == nil {
					t.Fatalf("parseWrite(%q) = %+v, want an error mentioning %q", tt.line, got, tt.wants)
				}
				if !strings.Contains(err.Error(), tt.wants) {
					t.Fatalf("parseWrite(%q) error = %q, want it to mention %q", tt.line, err, tt.wants)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseWrite(%q): %v", tt.line, err)
			}
			if got.Pin != tt.want.Pin || got.Slot != tt.want.Slot || got.Property != tt.want.Property ||
				math.Float64bits(got.Value) != math.Float64bits(tt.want.Value) {
				t.Errorf("parseWrite(%q) = %+v, want %+v", tt.line, got, tt.want)
			}
		})
	}
}
